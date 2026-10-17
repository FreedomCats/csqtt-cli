//go:build !windows

package datapath

import "syscall"

// exclusiveAddr — на Linux и macOS ничего не делает: на занятый порт UDP
// другой сокет садится, только если SO_REUSEADDR или SO_REUSEPORT стоит у
// обоих, а у сокета Link их нет.
func exclusiveAddr(_, _ string, _ syscall.RawConn) error { return nil }
