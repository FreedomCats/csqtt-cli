//go:build !linux

package cli

import "io"

// runSecretHelper: связка ключей с запуском от имени вызвавшего sudo есть
// только на Linux (secrethelper_linux.go).
func runSecretHelper([]string, io.Reader, io.Writer, io.Writer) (int, bool) { return 0, false }
