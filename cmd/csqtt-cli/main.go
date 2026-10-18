// Command csqtt-cli — консольный клиент CSQTT для настольных систем.
//
// Здесь только сведения о сборке и код возврата: разбор команд и сами команды
// — в internal/cli.
package main

import (
	"os"

	"github.com/FreedomCats/csqtt-cli/internal/cli"
)

// version и commit подставляются при сборке через -ldflags (задача I3).
var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], cli.Build{Version: version, Commit: commit}))
}
