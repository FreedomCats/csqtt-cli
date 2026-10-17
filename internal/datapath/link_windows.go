package datapath

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// soExclusiveAddrUse — SO_EXCLUSIVEADDRUSE из winsock2.h, ((int)(~SO_REUSEADDR)):
// в x/sys/windows его нет.
const soExclusiveAddrUse = ^windows.SO_REUSEADDR

// exclusiveAddr — Control для net.ListenConfig: на порт сокета Link никто не
// сядет даже с SO_REUSEADDR.
func exclusiveAddr(_, _ string, c syscall.RawConn) error {
	var serr error
	if err := c.Control(func(fd uintptr) {
		serr = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, soExclusiveAddrUse, 1)
	}); err != nil {
		return err
	}
	return serr
}
