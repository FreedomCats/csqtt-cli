//go:build !windows && !linux

package coreproc

import (
	"io"
	"os"
	"syscall"
)

// Других систем программа не поддерживает; пакет лишь собирается. Ядро в
// своей группе, чтобы Ctrl+C до него не доходил; смерть оболочки оно
// заметит само по смене родителя (main.rs, start_parent_monitor).
func sysProcAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }

func lockThread() func() { return func() {} }

func guardChild(*os.Process) (io.Closer, error) { return nopCloser{}, nil }

type nopCloser struct{}

func (nopCloser) Close() error { return nil }
