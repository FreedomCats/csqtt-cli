//go:build !windows

package cli

// ownsConsole — окно терминала открыто ради одного csqtt-cli. Вне Windows так не
// бывает: терминал запускает оболочку, а csqtt-cli — уже из неё. См. pause.go.
func ownsConsole() bool { return false }
