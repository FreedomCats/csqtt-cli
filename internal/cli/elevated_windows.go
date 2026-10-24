package cli

import "golang.org/x/sys/windows"

// isElevated — запущен ли csqtt-cli с правами администратора: на Windows они
// заменяют root (Geteuid там — -1).
func isElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// adminHint — начало подсказки, как запустить команду с правами: за ним
// идёт сама команда.
const adminHint = "нужны права администратора: откройте консоль от имени администратора и повторите:"

// adminNote — о правах для режима tun в мастере.
const adminNote = "Нужны права администратора: меню запросит их при подключении."
