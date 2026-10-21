//go:build !windows

package cli

import "os"

// isElevated — запущен ли csqtt-cli с правами root. См. elevated_windows.go.
func isElevated() bool {
	return os.Geteuid() == 0
}
