//go:build !windows

package coreproc

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// commandLine — командная строка процесса так, как её видит любой
// пользователь машины: /proc/<pid>/cmdline.
func commandLine(t *testing.T, pid int) string {
	t.Helper()
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil || len(b) == 0 {
		t.Fatalf("командная строка %d: %v", pid, err)
	}
	return strings.ReplaceAll(string(b), "\x00", " ")
}
