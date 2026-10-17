package coreproc

import (
	"os/exec"
	"strconv"
	"testing"
)

// commandLine — командная строка процесса так, как её видит любой
// пользователь машины: Win32_Process.CommandLine.
func commandLine(t *testing.T, pid int) string {
	t.Helper()
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		"(Get-CimInstance Win32_Process -Filter 'ProcessId="+strconv.Itoa(pid)+"').CommandLine").Output()
	if err != nil || len(out) == 0 {
		t.Fatalf("командная строка %d: %v", pid, err)
	}
	return string(out)
}
