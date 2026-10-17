//go:build linux || darwin

package datapath

import (
	"context"
	"net"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// reuse — сокет с SO_REUSEADDR и SO_REUSEPORT: так садятся на чужой порт.
var reuse = net.ListenConfig{Control: func(_, _ string, c syscall.RawConn) error {
	var serr error
	if err := c.Control(func(fd uintptr) {
		if serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1); serr == nil {
			serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
		}
	}); err != nil {
		return err
	}
	return serr
}}

// На порт Link не садится даже сокет с SO_REUSEADDR и SO_REUSEPORT: у
// самого Link их нет.
func TestLinkPortCannotBeShared(t *testing.T) {
	l := openLink(t)
	c, err := reuse.ListenPacket(context.Background(), "udp4", l.Addr().String())
	if err == nil {
		c.Close()
		t.Fatalf("второй сокет сел на порт Link %v", l.Addr())
	}
}

// То же для SOCKS5: SO_REUSEADDR у слушателя ставит Go, но на порт, который
// слушают, Linux второй сокет не пускает, а SO_REUSEPORT нужен у обоих.
func TestSocksPortCannotBeShared(t *testing.T) {
	s := newSocks(t)
	ln, err := reuse.Listen(context.Background(), "tcp4", s.ListenAddr().String())
	if err == nil {
		ln.Close()
		t.Fatalf("второй сокет сел на порт SOCKS5 %v", s.ListenAddr())
	}
}
