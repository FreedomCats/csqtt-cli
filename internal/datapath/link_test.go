package datapath

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"
)

// ipv4 — пакет IPv4 длиной size с меткой mark в первом байте данных.
func ipv4(size int, mark byte) []byte {
	p := make([]byte, size)
	p[0] = 0x45
	binary.BigEndian.PutUint16(p[2:4], uint16(size))
	p[8] = 64
	p[9] = 17
	copy(p[12:16], []byte{10, 66, 66, 2})
	copy(p[16:20], []byte{198, 51, 100, 1})
	if size > 20 {
		p[20] = mark
	}
	return p
}

// ipv6 — пакет IPv6 без данных.
func ipv6() []byte {
	p := make([]byte, 40)
	p[0] = 0x60
	return p
}

func openLink(t *testing.T) *Link {
	t.Helper()
	l, err := ListenLink()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	if a := l.Addr(); a.Addr() != loopback || a.Port() == 0 {
		t.Fatalf("Addr() = %v", a)
	}
	return l
}

// fakeCore — сокет на месте ядра; Link закреплён за его портом.
func fakeCore(t *testing.T, l *Link) *net.UDPConn {
	t.Helper()
	c := listenUDP(t)
	l.SetCorePort(port(c))
	return c
}

func listenUDP(t *testing.T) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func port(c *net.UDPConn) uint16 { return uint16(c.LocalAddr().(*net.UDPAddr).Port) }

func send(t *testing.T, from *net.UDPConn, to netip.AddrPort, p []byte) {
	t.Helper()
	if _, err := from.WriteToUDPAddrPort(p, to); err != nil {
		t.Fatal(err)
	}
}

// readPacket — ReadPacket не дольше секунды.
func readPacket(t *testing.T, l *Link) []byte {
	t.Helper()
	type result struct {
		p   []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		b := make([]byte, MaxPacket)
		n, err := l.ReadPacket(b)
		done <- result{b[:n], err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("ReadPacket: %v", r.err)
		}
		return r.p
	case <-time.After(time.Second):
		t.Fatal("ReadPacket не вернулся за секунду")
		return nil
	}
}

// receive — датаграм на сокете ядра и его отправитель; nil — за wait ничего.
func receive(t *testing.T, c *net.UDPConn, wait time.Duration) ([]byte, netip.AddrPort) {
	t.Helper()
	if err := c.SetReadDeadline(time.Now().Add(wait)); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, MaxPacket)
	n, from, err := c.ReadFromUDPAddrPort(b)
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return nil, netip.AddrPort{}
	}
	if err != nil {
		t.Fatal(err)
	}
	return b[:n], from
}

func TestLinkExchange(t *testing.T) {
	l := openLink(t)
	core := fakeCore(t, l)

	// Ядро — эхо: всё, что получило, отправляет обратно отправителю.
	const count = 500
	go func() {
		b := make([]byte, MaxPacket)
		for {
			n, from, err := core.ReadFromUDPAddrPort(b)
			if err != nil {
				return
			}
			_, _ = core.WriteToUDPAddrPort(b[:n], from)
		}
	}()
	// В пути не больше window пакетов: это UDP, и сверх буфера сокета (Linux
	// урезает его до net.core.rmem_max) ОС сбросила бы лишнее.
	const window = 32
	credits := make(chan struct{}, window)
	for range window {
		credits <- struct{}{}
	}
	go func() {
		for i := range count {
			<-credits
			if err := l.WritePacket(ipv4(1300, byte(i))); err != nil {
				t.Errorf("WritePacket: %v", err)
				return
			}
		}
	}()
	for i := range count {
		if got, want := readPacket(t, l), ipv4(1300, byte(i)); !bytes.Equal(got, want) {
			t.Fatalf("пакет %d: %d байт, метка %d", i, len(got), got[20])
		}
		credits <- struct{}{}
	}
	if s := l.Stats(); s != (LinkStats{}) {
		t.Errorf("Stats() = %+v", s)
	}
}

// Ядро видит отправителем ровно Addr — то, что уходит ему в -listen-peer.
func TestLinkSourceIsAddr(t *testing.T) {
	l := openLink(t)
	core := fakeCore(t, l)
	if err := l.WritePacket(ipv4(20, 0)); err != nil {
		t.Fatal(err)
	}
	p, from := receive(t, core, time.Second)
	if p == nil || from != l.Addr() {
		t.Fatalf("ядро получило %d байт от %v, Addr() = %v", len(p), from, l.Addr())
	}
}

func TestLinkDropsNotIPv4(t *testing.T) {
	l := openLink(t)
	core := fakeCore(t, l)

	bad := [][]byte{
		ipv6(),
		{0x45},                         // обрезанный заголовок
		{},                             // пустой
		[]byte("not an ip packet...."), // не-IP, 20 байт
		append(ipv4(40, 0), 0),         // длина из заголовка короче пакета
		ipv4(40, 0)[:30],               // и длиннее
		func() []byte { p := ipv4(40, 0); p[0] = 0x44; return p }(), // IHL 16 байт
	}
	for _, p := range bad {
		if err := l.WritePacket(p); err != nil {
			t.Fatal(err)
		}
	}
	if p, _ := receive(t, core, 100*time.Millisecond); p != nil {
		t.Errorf("ядро получило не-IPv4: % x", p)
	}

	for _, p := range bad {
		if len(p) > 0 {
			send(t, core, l.Addr(), p)
		}
	}
	send(t, core, l.Addr(), ipv4(60, 7))
	if got := readPacket(t, l); !bytes.Equal(got, ipv4(60, 7)) {
		t.Errorf("ReadPacket = % x", got)
	}
	s := l.Stats()
	if s.NotIPv4Out != uint64(len(bad)) || s.NotIPv4In != uint64(len(bad)-1) {
		t.Errorf("Stats() = %+v, ожидалось NotIPv4Out %d, NotIPv4In %d", s, len(bad), len(bad)-1)
	}
}

// Чужой процесс не попадает в адаптер, а ядро прежней попытки — после смены
// порта.
func TestLinkPinsCorePort(t *testing.T) {
	l := openLink(t)
	core := fakeCore(t, l)
	stranger := listenUDP(t)

	send(t, stranger, l.Addr(), ipv4(40, 1))
	send(t, core, l.Addr(), ipv4(40, 2))
	if got := readPacket(t, l); got[20] != 2 {
		t.Fatalf("прочитан пакет %d, а не ядра", got[20])
	}

	next := fakeCore(t, l)
	send(t, core, l.Addr(), ipv4(40, 3))
	send(t, next, l.Addr(), ipv4(40, 4))
	if got := readPacket(t, l); got[20] != 4 {
		t.Fatalf("прочитан пакет %d, а не нового ядра", got[20])
	}
	if err := l.WritePacket(ipv4(40, 5)); err != nil {
		t.Fatal(err)
	}
	if p, _ := receive(t, next, time.Second); p == nil || p[20] != 5 {
		t.Errorf("новое ядро получило % x", p)
	}
	if p, _ := receive(t, core, 100*time.Millisecond); p != nil {
		t.Errorf("прежнее ядро получило % x", p)
	}
	if s := l.Stats(); s.Foreign != 2 {
		t.Errorf("Stats() = %+v, ожидалось Foreign 2", s)
	}
}

// Между попытками (порт 0) пакеты к ядру сбрасываются, а вышедшее ядро для
// следующего — чужое. Отправитель сверяется при чтении: что лежит в буфере
// сокета, судится по порту ядра на момент ReadPacket.
func TestLinkWithoutCore(t *testing.T) {
	l := openLink(t)
	core := fakeCore(t, l)
	l.SetCorePort(0)

	if err := l.WritePacket(ipv4(40, 1)); err != nil {
		t.Fatal(err)
	}
	if p, _ := receive(t, core, 100*time.Millisecond); p != nil {
		t.Errorf("ядро без порта получило % x", p)
	}
	send(t, core, l.Addr(), ipv4(40, 2))
	next := fakeCore(t, l)
	send(t, next, l.Addr(), ipv4(40, 3))
	if got := readPacket(t, l); got[20] != 3 {
		t.Fatalf("прочитан пакет %d", got[20])
	}
	if s := l.Stats(); s.NoCore != 1 || s.Foreign != 1 {
		t.Errorf("Stats() = %+v, ожидалось NoCore 1, Foreign 1", s)
	}
}

// Переполнение в обе стороны: никто не ждёт, лишнее сбрасывает ОС, и после
// него канал работает.
func TestLinkOverflow(t *testing.T) {
	l := openLink(t)
	core := fakeCore(t, l)
	const burst = 20000 // 26 МБ — больше любого буфера сокета

	// Запись не должна ждать читателя: если ОС блокирует отправку в
	// переполненный сокет, тест падает по сроку, а не висит.
	written := make(chan error, 1)
	go func() {
		for i := range burst {
			if err := l.WritePacket(ipv4(1300, byte(i))); err != nil {
				written <- err
				return
			}
		}
		written <- nil
	}()
	select {
	case err := <-written:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("запись %d пакетов в непрочитанный сокет не кончилась за 10 с", burst)
	}
	got := 0
	for {
		p, _ := receive(t, core, 200*time.Millisecond)
		if p == nil {
			break
		}
		got++
	}
	if got == 0 || got > burst {
		t.Errorf("ядро получило %d из %d", got, burst)
	}
	t.Logf("к ядру дошло %d из %d, ошибок записи %d", got, burst, l.Stats().SendErrors)

	for i := range burst {
		_, _ = core.WriteToUDPAddrPort(ipv4(1300, byte(i)), l.Addr())
	}
	readPacket(t, l)
	if err := l.WritePacket(ipv4(40, 1)); err != nil {
		t.Fatal(err)
	}
	if p, _ := receive(t, core, time.Second); p == nil {
		t.Error("после переполнения пакет до ядра не дошёл")
	}
}

func TestLinkClose(t *testing.T) {
	l := openLink(t)
	fakeCore(t, l)

	done := make(chan error, 1)
	go func() {
		_, err := l.ReadPacket(make([]byte, MaxPacket))
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, net.ErrClosed) {
			t.Errorf("ReadPacket после Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ReadPacket не вернулся после Close")
	}
	if err := l.WritePacket(ipv4(40, 0)); !errors.Is(err, net.ErrClosed) {
		t.Errorf("WritePacket после Close: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Errorf("повторный Close: %v", err)
	}
}

func TestLinkShortBuffer(t *testing.T) {
	l := openLink(t)
	if _, err := l.ReadPacket(make([]byte, 1500)); !errors.Is(err, errShortBuffer) {
		t.Errorf("ReadPacket(1500 байт) = %v", err)
	}
}
