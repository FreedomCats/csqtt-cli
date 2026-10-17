package datapath

import (
	"context"
	"net"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

// reuse — сокет с SO_REUSEADDR: так на Windows садятся на чужой порт.
var reuse = net.ListenConfig{Control: func(_, _ string, c syscall.RawConn) error {
	var serr error
	if err := c.Control(func(fd uintptr) {
		serr = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, windows.SO_REUSEADDR, 1)
	}); err != nil {
		return err
	}
	return serr
}}

// SO_EXCLUSIVEADDRUSE: на порт Link не садится сокет с SO_REUSEADDR —
// без него Windows пустила бы.
func TestLinkPortCannotBeShared(t *testing.T) {
	l := openLink(t)
	c, err := reuse.ListenPacket(context.Background(), "udp4", l.Addr().String())
	if err == nil {
		c.Close()
		t.Fatalf("сокет с SO_REUSEADDR сел на порт Link %v", l.Addr())
	}
}

// Обратная проверка: без SO_EXCLUSIVEADDRUSE у первого сокета второй с
// SO_REUSEADDR на его порт садится — значит, тест выше проверяет именно
// флаг Link.
func TestReuseAddrSharesPlainSocket(t *testing.T) {
	first := listenUDP(t)
	c, err := reuse.ListenPacket(context.Background(), "udp4", first.LocalAddr().String())
	if err != nil {
		t.Skipf("Windows не пустил SO_REUSEADDR и без SO_EXCLUSIVEADDRUSE: %v", err)
	}
	c.Close()
}

// То же для слушателя SOCKS5.
func TestSocksPortCannotBeShared(t *testing.T) {
	s := newSocks(t)
	ln, err := reuse.Listen(context.Background(), "tcp4", s.ListenAddr().String())
	if err == nil {
		ln.Close()
		t.Fatalf("сокет с SO_REUSEADDR сел на порт SOCKS5 %v", s.ListenAddr())
	}
}
