package script

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Копия получает содержимое, время и права исходника — и поверх старого
// файла с другими правами тоже.
func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "client"), filepath.Join(dir, "csqtt-core")
	if err := os.WriteFile(src, []byte("ELF"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(src, 0o755); err != nil {
		t.Fatal(err)
	}
	mtime := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	if err := os.Chtimes(src, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("старое, длиннее нового"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := CopyFile(src, dst); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "ELF" || !fi.ModTime().Equal(mtime) {
		t.Errorf("копия: %q, время %v", b, fi.ModTime())
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o755 {
		t.Errorf("права копии %v, want 0755", fi.Mode().Perm())
	}

	// Исходник только для чтения: копию всё равно можно перезаписать.
	if err := os.Chmod(src, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := CopyFile(src, dst); err != nil {
		t.Fatal(err)
	}
	if err := CopyFile(src, dst); err != nil {
		t.Errorf("копия поверх копии файла только для чтения: %v", err)
	}
}
