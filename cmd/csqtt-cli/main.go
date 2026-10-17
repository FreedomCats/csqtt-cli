// Command csqtt-cli — консольный клиент CSQTT для настольных систем.
//
// Пока это заглушка каркаса (задача F1): команды перечислены, но ни одна,
// кроме version, ещё не работает. Разбор команд переедет в internal/cli
// (задача C10), здесь останутся сведения о сборке и код возврата.
package main

import (
	"fmt"
	"io"
	"os"
)

// version и commit подставляются при сборке через -ldflags (задача I3).
var (
	version = "dev"
	commit  = "unknown"
)

// commands — команды, которые появятся по плану. Описание — для справки.
var commands = []struct{ name, help string }{
	{"connect", "подключиться к серверу CSQTT"},
	{"profile", "профили подключения: add, set, show, rm, list"},
	{"hashes", "проверить ссылки на звонки VK"},
	{"version", "версия программы"},
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stdout)
		return 0
	}
	switch args[0] {
	case "version":
		fmt.Fprintf(stdout, "csqtt-cli %s (%s)\n", version, commit)
		return 0
	case "help", "-h", "-help", "--help":
		usage(stdout)
		return 0
	}
	for _, c := range commands {
		if c.name == args[0] {
			fmt.Fprintf(stderr, "csqtt-cli: команда %q ещё не готова\n", args[0])
			return 1
		}
	}
	fmt.Fprintf(stderr, "csqtt-cli: неизвестная команда %q\n\n", args[0])
	usage(stderr)
	return 2
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "Использование: csqtt-cli <команда> [флаги]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Команды:")
	for _, c := range commands {
		fmt.Fprintf(w, "  %-9s %s\n", c.name, c.help)
	}
}
