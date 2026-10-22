package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/FreedomCats/csqtt-cli/scripts/internal/dist"
)

func writeZip(t *testing.T, path string, names ...string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, n := range names {
		if _, err := zw.Create(n); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func writeTarGz(t *testing.T, path string, modes map[string]int64) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for n, m := range modes {
		if err := tw.WriteHeader(&tar.Header{Name: n, Mode: m, Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
	f.Close()
}

// Полный tar.gz; без права на исполнение у csqtt-cli или у csqtt-core;
// без ядра.
func TestTarGz(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r_linux.tar.gz")
	full := func() map[string]int64 {
		m := map[string]int64{}
		for _, f := range dist.Files("linux") {
			m["r_linux/"+f] = 0o644
		}
		for _, e := range dist.Executables("linux") {
			m["r_linux/"+e] = 0o755
		}
		return m
	}
	writeTarGz(t, path, full())
	if failed, err := run(dir); err != nil || failed {
		t.Fatalf("годный архив: %v, %v", failed, err)
	}

	for _, exe := range []string{"csqtt-cli", "csqtt-core"} {
		m := full()
		m["r_linux/"+exe] = 0o644
		writeTarGz(t, path, m)
		if failed, err := run(dir); err != nil || !failed {
			t.Errorf("%s без права на исполнение: %v, %v", exe, failed, err)
		}
	}

	m := full()
	delete(m, "r_linux/csqtt-core")
	writeTarGz(t, path, m)
	if failed, err := run(dir); err != nil || !failed {
		t.Errorf("без csqtt-core: %v, %v", failed, err)
	}

	// Полный состав и лишняя запись вне каталога — провал; весь архив без
	// каталога (с правами 0755) — тоже.
	m = full()
	m["stray.txt"] = 0o644
	writeTarGz(t, path, m)
	if failed, err := run(dir); err != nil || !failed {
		t.Errorf("запись вне каталога: %v, %v", failed, err)
	}
	flat := map[string]int64{}
	for name, mode := range full() {
		flat[strings.TrimPrefix(name, "r_linux/")] = mode
	}
	writeTarGz(t, path, flat)
	if failed, err := run(dir); err != nil || !failed {
		t.Errorf("архив без каталога: %v, %v", failed, err)
	}
}

func zipNames(prefix string, skip ...string) []string {
	var names []string
	for _, f := range dist.Files("windows") {
		if !slices.Contains(skip, f) {
			names = append(names, prefix+f)
		}
	}
	return names
}

// Полный zip проходит; с записью вне каталога и без wintun.dll, без ядра —
// провал.
func TestZipComposition(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r_windows.zip")
	for _, c := range []struct {
		name  string
		names []string
		fail  bool
	}{
		{"полный", zipNames("r_windows/"), false},
		{"без wintun.dll и с файлом вне каталога", append(zipNames("r_windows/", "wintun.dll"), "stray.txt"), true},
		{"без csqtt-core.exe", zipNames("r_windows/", "csqtt-core.exe"), true},
		{"без LICENSE-core.txt", zipNames("r_windows/", "LICENSE-core.txt"), true},
		{"без LICENSE.txt", zipNames("r_windows/", "LICENSE.txt"), true},
	} {
		writeZip(t, path, c.names...)
		ch := &checker{}
		if err := ch.zipEntries(path, "r_windows.zip"); err != nil {
			t.Fatal(err)
		}
		if ch.failed != c.fail {
			t.Errorf("%s: провал = %v", c.name, ch.failed)
		}
	}
}

func TestNoArchives(t *testing.T) {
	if failed, err := run(t.TempDir()); err != nil || !failed {
		t.Errorf("пустой каталог: %v, %v", failed, err)
	}
}
