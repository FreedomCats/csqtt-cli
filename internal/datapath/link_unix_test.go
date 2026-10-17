//go:build linux || darwin

package datapath

import (
	"context"
	"net"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// На порт Link не садится даже сокет с SO_REUSEADDR и SO_REUSEPORT: у
// самого Link их нет.
func TestLinkPortCannotBeShared(t *testing.T) {
	l := openLink(t)
	lc := net.ListenConfig{Control: func(_, _ string, c syscall.RawConn) error {
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
	c, err := lc.ListenPacket(context.Background(), "udp4", l.Addr().String())
	if err == nil {
		c.Close()
		t.Fatalf("второй сокет сел на порт Link %v", l.Addr())
	}
}
