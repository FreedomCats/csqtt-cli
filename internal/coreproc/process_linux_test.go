package coreproc

import (
	"syscall"
	"testing"
)

// SIGINT от Ctrl+C терминал шлёт группе процессов оболочки; ядро должно
// быть в своей.
func TestCoreInOwnProcessGroup(t *testing.T) {
	p := startFake(t, "deaf")
	_, seen, _ := collect(p, func(m Message) bool { _, ok := m.(PIDEvent); return ok })
	waitFor(t, seen, "PROCESS")
	pg, err := syscall.Getpgid(p.PID())
	if err != nil {
		t.Fatal(err)
	}
	if pg != p.PID() || pg == syscall.Getpgrp() {
		t.Errorf("группа ядра %d, его pid %d, группа теста %d", pg, p.PID(), syscall.Getpgrp())
	}
}
