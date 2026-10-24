//go:build !windows

package cli

import "os"

// isElevated — запущен ли csqtt-cli с правами root. См. elevated_windows.go.
func isElevated() bool {
	return os.Geteuid() == 0
}

// adminHint — начало подсказки, как запустить команду с правами: за ним
// идёт сама команда.
const adminHint = "нужны права root: sudo"

// adminNote — о правах для режима tun в мастере.
const adminNote = "Нужны права root: меню запросит их при подключении (sudo)."
