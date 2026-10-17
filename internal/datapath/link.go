// Package datapath отвечает за то, каким способом трафик приложений попадает
// в туннель (architecture.md §05).
//
// Ядро всегда делает одно и то же: принимает сырые IP-пакеты и уносит их через
// TURN. Различается только то, откуда пакеты берутся, — netstack с локальным
// SOCKS5 (socks) или системный адаптер (tun). С ядром их связывает Link — UDP
// на loopback; на Linux адаптер tun отдаёт ядру свой дескриптор напрямую.
package datapath

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
)

// CoreListen — значение -listen ядра: порт выбирает ядро и сообщает его
// событием LISTEN (правка П2), оболочка передаёт его в SetCorePort.
const CoreListen = "127.0.0.1:0"

// MaxPacket — наименьший буфер ReadPacket: пакет IPv4 не длиннее 65535 байт,
// и в такой буфер датаграм помещается целиком. В короткий Linux молча
// обрезал бы датаграм, а Windows вернул бы ошибку WSAEMSGSIZE.
const MaxPacket = 65535

// socketBuffer — буферы сокета, как у ядра (bind_udp в dispatcher.rs):
// пачка ядра не должна теряться на стороне оболочки раньше, чем у ядра.
const socketBuffer = 625 * 1024

var loopback = netip.AddrFrom4([4]byte{127, 0, 0, 1})

var errShortBuffer = errors.New("datapath: буфер ReadPacket короче MaxPacket")

// Link — канал сырых IP-пакетов между оболочкой и ядром: UDP на 127.0.0.1
// (решение Р4).
//
// Порт на loopback открыт любому процессу машины, поэтому отправитель
// закреплён с обеих сторон. Ядро с -listen-peer принимает датаграмы только
// с адреса Link (Addr) и отвечает только ему (правка П2), а Link принимает
// только с порта ядра (SetCorePort): чужой датаграм не уводит ответы сервера
// и не попадает в адаптер. На Windows сокет Link — с SO_EXCLUSIVEADDRUSE:
// без него процесс с SO_REUSEADDR сел бы на тот же порт и забирал пакеты
// ядра.
//
// Link живёт дольше ядра: ядро запускается на каждую попытку и слушает новый
// порт. Между попытками порта нет (SetCorePort(0)) — пакеты сбрасываются, а
// не уходят на порт вышедшего ядра, который мог занять кто-то другой.
//
// Пропускается только IPv4: сервер выдаёт адрес IPv4, пакеты IPv6 программ
// ядру не нужны, а не-IP ответы сервера не нужны адаптеру (§05). Семантика —
// как у UDP: пакет, который некуда или незачем отдавать, сбрасывается и
// считается (Stats), отправитель не ждёт. Переполненный буфер сокета ОС
// сбрасывает сама и не считает.
//
// ReadPacket и WritePacket можно звать из разных горутин одновременно.
type Link struct {
	conn *net.UDPConn
	addr netip.AddrPort
	// core — порт ядра на 127.0.0.1; 0 — ядра нет.
	core atomic.Uint32

	closed    atomic.Bool
	closeOnce sync.Once
	closeErr  error

	foreign    atomic.Uint64
	notIPv4In  atomic.Uint64
	notIPv4Out atomic.Uint64
	noCore     atomic.Uint64
	sendErrors atomic.Uint64
}

// LinkStats — сколько пакетов Link сбросил, по причинам.
type LinkStats struct {
	// Foreign — датаграмы не с порта ядра: от чужих процессов, от ядра
	// прежней попытки, до SetCorePort.
	Foreign uint64
	// NotIPv4In — от ядра пришло не IPv4: служебные ответы сервера, мусор.
	NotIPv4In uint64
	// NotIPv4Out — к ядру шло не IPv4: IPv6 программ, мусор.
	NotIPv4Out uint64
	// NoCore — к ядру, пока порта ядра нет.
	NoCore uint64
	// SendErrors — к ядру, сокет не принял запись.
	SendErrors uint64
}

// ListenLink открывает сокет Link на 127.0.0.1 со случайным портом.
func ListenLink() (*Link, error) {
	lc := net.ListenConfig{Control: exclusiveAddr}
	pc, err := lc.ListenPacket(context.Background(), "udp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("канал с ядром: %w", err)
	}
	conn := pc.(*net.UDPConn)
	// ОС может урезать буферы до своего предела — это не ошибка.
	_ = conn.SetReadBuffer(socketBuffer)
	_ = conn.SetWriteBuffer(socketBuffer)
	local := conn.LocalAddr().(*net.UDPAddr).AddrPort()
	return &Link{conn: conn, addr: netip.AddrPortFrom(local.Addr().Unmap(), local.Port())}, nil
}

// Addr — адрес сокета Link: значение -listen-peer ядра.
func (l *Link) Addr() netip.AddrPort { return l.addr }

// SetCorePort закрепляет порт ядра из события LISTEN; 0 — ядра нет (вышло,
// следующее ещё не сообщило порт).
func (l *Link) SetCorePort(port uint16) { l.core.Store(uint32(port)) }

// ReadPacket ждёт пакет IPv4 от ядра и кладёт его в b; b — не короче
// MaxPacket. Чужие датаграмы и не-IPv4 пропускаются. После Close —
// ошибка с net.ErrClosed.
func (l *Link) ReadPacket(b []byte) (int, error) {
	if len(b) < MaxPacket {
		return 0, errShortBuffer
	}
	for {
		n, from, err := l.conn.ReadFromUDPAddrPort(b)
		if err != nil {
			return 0, err
		}
		if !l.fromCore(from) {
			l.foreign.Add(1)
			continue
		}
		if !isIPv4(b[:n]) {
			l.notIPv4In.Add(1)
			continue
		}
		return n, nil
	}
}

func (l *Link) fromCore(from netip.AddrPort) bool {
	port := l.core.Load()
	return port != 0 && uint32(from.Port()) == port && from.Addr().Unmap() == loopback
}

// WritePacket отдаёт пакет ядру. Не IPv4 или ядра нет — пакет сбрасывается
// без ошибки, как датаграм в закрытый порт; ошибка — только net.ErrClosed
// после Close. Буфер p можно переиспользовать сразу.
func (l *Link) WritePacket(p []byte) error {
	if l.closed.Load() {
		return net.ErrClosed
	}
	if !isIPv4(p) {
		l.notIPv4Out.Add(1)
		return nil
	}
	port := uint16(l.core.Load())
	if port == 0 {
		l.noCore.Add(1)
		return nil
	}
	if _, err := l.conn.WriteToUDPAddrPort(p, netip.AddrPortFrom(loopback, port)); err != nil {
		if errors.Is(err, net.ErrClosed) {
			return err
		}
		l.sendErrors.Add(1)
	}
	return nil
}

// Stats — счётчики сброшенных пакетов с открытия Link.
func (l *Link) Stats() LinkStats {
	return LinkStats{
		Foreign:    l.foreign.Load(),
		NotIPv4In:  l.notIPv4In.Load(),
		NotIPv4Out: l.notIPv4Out.Load(),
		NoCore:     l.noCore.Load(),
		SendErrors: l.sendErrors.Load(),
	}
}

// Close закрывает сокет: ждущий ReadPacket возвращает net.ErrClosed.
// Повторный вызов безвреден.
func (l *Link) Close() error {
	l.closeOnce.Do(func() {
		l.closed.Store(true)
		l.closeErr = l.conn.Close()
	})
	return l.closeErr
}

// isIPv4 — пакет IPv4 с целым заголовком, а длина из заголовка равна длине
// пакета.
func isIPv4(p []byte) bool {
	if len(p) < 20 || p[0]>>4 != 4 {
		return false
	}
	header := int(p[0]&0x0f) * 4
	total := int(binary.BigEndian.Uint16(p[2:4]))
	return header >= 20 && header <= total && total == len(p)
}
