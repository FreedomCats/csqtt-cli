package cli

import "golang.org/x/sys/windows"

// isElevated — запущен ли csqtt-cli с правами администратора: на Windows они
// заменяют root (Geteuid там — -1).
func isElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}
