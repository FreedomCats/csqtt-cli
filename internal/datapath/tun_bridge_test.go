package datapath

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

// fakeDevice — адаптер без системы: Read отдаёт пакеты из in, Write
// складывает в out. readErr и writeErr — ошибки, которые вернут ближайшие
// вызовы (по одной на вызов), прежде чем устройство заработает снова.
type fakeDevice struct {
	in  chan []byte
	out chan []byte

	mu       sync.Mutex
	readErr  []error
	writeErr []error

	closeOnce sync.Once
	closed    chan struct{}
}

func newFakeDevice() *fakeDevice {
	return &fakeDevice{in: make(chan []byte, 16), out: make(chan []byte, 16), closed: make(chan struct{})}
}

func (d *fakeDevice) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	if err := d.takeErr(&d.readErr); err != nil {
		return 0, err
	}
	select {
	case <-d.closed:
		return 0, os.ErrClosed
	case p := <-d.in:
		sizes[0] = copy(bufs[0][offset:], p)
		return 1, nil
	}
}

func (d *fakeDevice) Write(bufs [][]byte, offset int) (int, error) {
	select {
	case <-d.closed:
		return 0, os.ErrClosed
	default:
	}
	if err := d.takeErr(&d.writeErr); err != nil {
		return 0, err
	}
	for _, b := range bufs {
		d.out <- bytes.Clone(b[offset:])
	}
	return len(bufs), nil
}

func (d *fakeDevice) takeErr(errs *[]error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(*errs) == 0 {
		return nil
	}
	err := (*errs)[0]
	*errs = (*errs)[1:]
	return err
}

func (d *fakeDevice) Close() { d.closeOnce.Do(func() { close(d.closed) }) }

// written — пакет, записанный мостом в адаптер; nil — за wait ничего.
func (d *fakeDevice) written(wait time.Duration) []byte {
	select {
	case p := <-d.out:
		return p
	case <-time.After(wait):
		return nil
	}
}

// bridgeLog — журнал моста для проверок.
type bridgeLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *bridgeLog) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *bridgeLog) count(sub string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return countContaining(l.lines, sub)
}

// runBridge — мост между fakeDevice и Link с поддельным ядром; останавливается
// в Cleanup так же, как его останавливает адаптер.
func runBridge(t *testing.T) (*fakeDevice, *Link, *bridgeLog, func() bool) {
	t.Helper()
	dev := newFakeDevice()
	link := openLink(t)
	log := &bridgeLog{}
	b := startBridge(dev, link, log.logf)
	stopped := false
	stop := func() bool {
		if stopped {
			return true
		}
		stopped = true
		b.stop()
		dev.Close()
		_ = link.Close()
		done := make(chan struct{})
		go func() {
			b.wait()
			close(done)
		}()
		select {
		case <-done:
			return true
		case <-time.After(5 * time.Second):
			return false
		}
	}
	t.Cleanup(func() {
		if !stop() {
			t.Error("мост не остановился за 5 с")
		}
	})
	return dev, link, log, stop
}

// Пакет IPv4 из адаптера уходит ядру с адреса Link, ответ ядра — в адаптер
// как есть.
func TestBridgeExchange(t *testing.T) {
	dev, link, _, _ := runBridge(t)
	core := fakeCore(t, link)

	out := ipv4(1300, 1)
	dev.in <- out
	got, from := receive(t, core, time.Second)
	if !bytes.Equal(got, out) || from != link.Addr() {
		t.Fatalf("ядро получило %d байт от %v, ждали %d от %v", len(got), from, len(out), link.Addr())
	}

	in := ipv4(60, 2)
	send(t, core, link.Addr(), in)
	if got := dev.written(time.Second); !bytes.Equal(got, in) {
		t.Fatalf("в адаптер записано %v, ждали пакет ядра", got)
	}
}

// IPv6 программ ядру не уходит, не-IPv4 от ядра в адаптер не пишется; и то
// и другое считается в Link.
func TestBridgeDropsNotIPv4(t *testing.T) {
	dev, link, _, _ := runBridge(t)
	core := fakeCore(t, link)

	dev.in <- ipv6()
	dev.in <- ipv4(40, 3)
	got, _ := receive(t, core, time.Second)
	if len(got) != 40 || got[20] != 3 {
		t.Fatalf("ядро получило %v, ждали только пакет IPv4", got)
	}
	send(t, core, link.Addr(), []byte("служебный ответ сервера"))
	send(t, core, link.Addr(), ipv4(40, 4))
	if got := dev.written(time.Second); len(got) != 40 || got[20] != 4 {
		t.Fatalf("в адаптер записано %v, ждали только пакет IPv4", got)
	}
	if s := link.Stats(); s.NotIPv4Out != 1 || s.NotIPv4In != 1 {
		t.Errorf("счётчики Link: %+v", s)
	}
}

// Пока ядра нет (между попытками), пакеты адаптера сбрасываются, а мост
// работает дальше и доставляет ядру следующей попытки.
func TestBridgeSurvivesCoreRestart(t *testing.T) {
	dev, link, _, _ := runBridge(t)
	dev.in <- ipv4(40, 5)
	deadline := time.Now().Add(time.Second)
	for link.Stats().NoCore == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if link.Stats().NoCore != 1 {
		t.Fatalf("пакет без ядра не сброшен: %+v", link.Stats())
	}
	core := fakeCore(t, link)
	dev.in <- ipv4(40, 6)
	if got, _ := receive(t, core, time.Second); len(got) != 40 || got[20] != 6 {
		t.Fatalf("ядро новой попытки получило %v", got)
	}
}

// Сбой чтения или записи адаптера не останавливает мост: строка в журнал
// одна на серию, после неё — строка о восстановлении.
func TestBridgeRetriesDeviceErrors(t *testing.T) {
	dev, link, log, _ := runBridge(t)
	core := fakeCore(t, link)
	boom := errors.New("кольцо Wintun занято")
	dev.mu.Lock()
	dev.writeErr = []error{boom, boom}
	dev.mu.Unlock()

	send(t, core, link.Addr(), ipv4(40, 7))
	send(t, core, link.Addr(), ipv4(40, 8))
	send(t, core, link.Addr(), ipv4(40, 9))
	if got := dev.written(2 * time.Second); len(got) != 40 || got[20] != 9 {
		t.Fatalf("после сбоев записано %v, ждали третий пакет", got)
	}
	if n := log.count("запись в адаптер: кольцо Wintun занято"); n != 1 {
		t.Errorf("строк о сбое записи %d, ждали одну", n)
	}
	if n := log.count("снова работает после 2 сбоев"); n != 1 {
		t.Errorf("строк о восстановлении %d", n)
	}

	dev.mu.Lock()
	dev.readErr = []error{boom}
	dev.mu.Unlock()
	// Read уже ждёт пакет в первом вызове; ошибку вернёт следующий.
	dev.in <- ipv4(40, 10)
	if got, _ := receive(t, core, time.Second); len(got) != 40 || got[20] != 10 {
		t.Fatalf("ядро получило %v", got)
	}
	dev.in <- ipv4(40, 11)
	if got, _ := receive(t, core, 2*time.Second); len(got) != 40 || got[20] != 11 {
		t.Fatalf("после сбоя чтения ядро получило %v", got)
	}
	if n := log.count("чтение адаптера: кольцо Wintun занято"); n != 1 {
		t.Errorf("строк о сбое чтения %d, ждали одну", n)
	}
}

// Остановка будит обе горутины, даже если адаптер сбоит без конца.
func TestBridgeStopsWhileFailing(t *testing.T) {
	dev, _, _, stop := runBridge(t)
	boom := errors.New("сбой")
	dev.mu.Lock()
	for range 1000 {
		dev.readErr = append(dev.readErr, boom)
	}
	dev.mu.Unlock()
	time.Sleep(3 * bridgeRetry)
	start := time.Now()
	if !stop() {
		t.Fatal("мост не остановился")
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("остановка заняла %v", d)
	}
}
