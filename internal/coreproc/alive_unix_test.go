//go:build !windows

package coreproc

import (
	"bytes"
	"os"
	"strconv"
	"syscall"
)

// alive: зомби — уже мёртвый процесс, которого родитель ещё не прибрал.
// Осиротевшее ядро прибирает init, но в контейнере init может оказаться
// кем угодно.
func alive(pid int) bool {
	if b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
		i := bytes.LastIndexByte(b, ')')
		return i < 0 || i+2 >= len(b) || b[i+2] != 'Z'
	}
	return syscall.Kill(pid, 0) == nil
}

func killPID(pid int) { _ = syscall.Kill(pid, syscall.SIGKILL) }
