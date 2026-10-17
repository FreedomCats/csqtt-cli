package script

import (
	"io"
	"os"
)

// CopyFile копирует src поверх dst и переносит время изменения, как
// Copy-Item: время попадает в zip релиза. Права — тоже от src (собранный
// бинарь на Linux должен остаться исполняемым), но с записью для владельца:
// иначе следующая копия поверх не откроется.
func CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	perm := fi.Mode().Perm() | 0o200
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	// Права у OpenFile — только для нового файла, и их режет umask.
	if err := os.Chmod(dst, perm); err != nil {
		return err
	}
	return os.Chtimes(dst, fi.ModTime(), fi.ModTime())
}
