package datapath

import (
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"
)

// packetDevice — адаптер, из которого мост читает пакеты и в который их
// пишет: tun.Device из wireguard-go (Wintun) или подделка в тестах. Read
// кладёт по пакету в bufs (sizes — их длины) и возвращает их число, Write
// пишет пакеты из bufs; после Close оба возвращают os.ErrClosed. Читает
// одна горутина, пишет другая.
type packetDevice interface {
	Read(bufs [][]byte, sizes []int, offset int) (n int, err error)
	Write(bufs [][]byte, offset int) (int, error)
}

// bridgeRetry — пауза после сбоя чтения или записи, чтобы не крутиться
// впустую, пока сбой не пройдёт или мост не остановят.
const bridgeRetry = 100 * time.Millisecond

// bridgeGiveUp — сколько сбой одной стороны может идти без перерыва, прежде
// чем мост сочтёт адаптер сломанным. Повторы лечат занятое кольцо, но не
// испорченное («Send ring corrupt» у wireguard-go): такой сбой не проходит.
// Переменная — ради тестов.
var bridgeGiveUp = 10 * time.Second

// errAdapterGone — адаптер закрыт не мостом: система закрыла его сама.
var errAdapterGone = errors.New("система закрыла адаптер: его отключили или удалили, " +
	"сбросили сетевой стек или обновили драйвер")

// tunBridge возит пакеты между адаптером и ядром, пока адаптер жив (Windows:
// Wintun открыт в процессе оболочки, architecture.md §05). От адаптера к
// ядру — Link.WritePacket: он же отбрасывает IPv6 программ и пакеты, пока
// ядра нет. От ядра к адаптеру — Link.ReadPacket: он пропускает только
// пакеты IPv4 с порта ядра, так что служебные ответы сервера и чужие
// датаграмы в адаптер не попадают.
//
// Мост переживает попытки: ядро меняется, Link и адаптер — нет. Адаптер,
// закрытый не мостом (os.ErrClosed без stop), или сбой дольше bridgeGiveUp —
// конец моста: он зовёт lost с причиной, один раз, и выходит.
type tunBridge struct {
	done     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup

	lost     func(error)
	lostOnce sync.Once
}

// startBridge запускает мост. Остановка — stop, затем закрыть адаптер и
// Link (они разбудят ждущие Read и ReadPacket), затем wait. lost узнаёт, что
// адаптер сломался сам; nil — молчать.
func startBridge(dev packetDevice, link *Link, logf func(string, ...any), lost func(error)) *tunBridge {
	b := &tunBridge{done: make(chan struct{}), lost: lost}
	b.wg.Add(2)
	go func() {
		defer b.wg.Done()
		b.toCore(dev, link, logf)
	}()
	go func() {
		defer b.wg.Done()
		b.toAdapter(dev, link, logf)
	}()
	return b
}

// stop говорит горутинам моста выйти на ближайшей ошибке. Повторный вызов
// безвреден, в том числе одновременный.
func (b *tunBridge) stop() { b.stopOnce.Do(func() { close(b.done) }) }

// wait ждёт выхода горутин моста.
func (b *tunBridge) wait() { b.wg.Wait() }

func (b *tunBridge) toCore(dev packetDevice, link *Link, logf func(string, ...any)) {
	bufs := [][]byte{make([]byte, MaxPacket)}
	sizes := []int{0}
	var fail bridgeFailure
	for {
		n, err := dev.Read(bufs, sizes, 0)
		if err != nil {
			if b.failed(&fail, "чтение адаптера", err, logf) {
				return
			}
			continue
		}
		fail.ok(logf, "чтение адаптера")
		for i := range n {
			if err := link.WritePacket(bufs[i][:sizes[i]]); err != nil {
				// Link закрыт: мост останавливают.
				return
			}
		}
	}
}

func (b *tunBridge) toAdapter(dev packetDevice, link *Link, logf func(string, ...any)) {
	buf := make([]byte, MaxPacket)
	out := [][]byte{nil}
	var fail bridgeFailure
	for {
		n, err := link.ReadPacket(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) || b.stopped() {
				return
			}
			// Сбой приёма на loopback: ядро следующей попытки пришлёт своё.
			select {
			case <-b.done:
				return
			case <-time.After(bridgeRetry):
			}
			continue
		}
		out[0] = buf[:n]
		if _, err := dev.Write(out, 0); err != nil {
			if b.failed(&fail, "запись в адаптер", err, logf) {
				return
			}
			continue
		}
		fail.ok(logf, "запись в адаптер")
	}
}

// failed решает, что делать после ошибки адаптера: true — выйти (мост
// остановлен или адаптер сломался), false — повторить после паузы. О сбое
// пишется один раз, пока он не пройдёт.
func (b *tunBridge) failed(fail *bridgeFailure, what string, err error, logf func(string, ...any)) bool {
	if b.stopped() {
		return true
	}
	if errors.Is(err, os.ErrClosed) {
		b.lose(fmt.Errorf("%s: %w", what, errAdapterGone))
		return true
	}
	if fail.report(logf, what, err) >= bridgeGiveUp {
		b.lose(fmt.Errorf("%s: %w — сбой не проходит дольше %s", what, err, bridgeGiveUp))
		return true
	}
	select {
	case <-b.done:
		return true
	case <-time.After(bridgeRetry):
		return false
	}
}

// lose — адаптер сломался: сказать один раз и остановить обе стороны.
func (b *tunBridge) lose(err error) {
	b.lostOnce.Do(func() {
		if b.lost != nil {
			b.lost(err)
		}
	})
	b.stop()
}

func (b *tunBridge) stopped() bool {
	select {
	case <-b.done:
		return true
	default:
		return false
	}
}

// bridgeFailure — идущий сбой одной стороны моста: сколько раз подряд и с
// какого времени.
type bridgeFailure struct {
	count int
	since time.Time
}

// report отмечает сбой и отдаёт, сколько идёт серия.
func (f *bridgeFailure) report(logf func(string, ...any), what string, err error) time.Duration {
	if f.count == 0 {
		f.since = time.Now()
		if logf != nil {
			logf("[TUN] мост: %s: %v — повторяю", what, err)
		}
	}
	f.count++
	return time.Since(f.since)
}

func (f *bridgeFailure) ok(logf func(string, ...any), what string) {
	if f.count > 0 && logf != nil {
		logf("[TUN] мост: %s снова работает после %d сбоев", what, f.count)
	}
	f.count = 0
}
