package coreproc

import (
	"io"
	"os"
	"runtime"
	"syscall"
)

// Своя группа процессов: SIGINT от Ctrl+C терминал шлёт группе оболочки, а
// ядро в ней было бы и остановилось бы само, мимо оболочки. Pdeathsig
// убивает ядро, когда умирает оболочка.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}

func lockThread() func() {
	runtime.LockOSThread()
	return runtime.UnlockOSThread
}

func guardChild(*os.Process) (io.Closer, error) { return nopCloser{}, nil }

type nopCloser struct{}

func (nopCloser) Close() error { return nil }
