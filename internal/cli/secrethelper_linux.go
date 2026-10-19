package cli

import (
	"io"

	"github.com/FreedomCats/csqtt-cli/internal/secretservice"
)

// runSecretHelper выполняет служебную команду связки ключей, если args — она:
// так csqtt-cli под sudo читает и пишет секреты в связке того, кто его вызвал
// (internal/secretservice). ok — команда была эта.
func runSecretHelper(args []string, stdin io.Reader, stdout, stderr io.Writer) (code int, ok bool) {
	if len(args) != 1 || args[0] != secretservice.HelperCommand {
		return 0, false
	}
	return secretservice.RunHelper(stdin, stdout, stderr), true
}
