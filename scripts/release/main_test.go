package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/FreedomCats/csqtt-cli/scripts/internal/dist"
)

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// layout — каталог платформы со всем составом архива (dist.Files).
func layout(t *testing.T, parent, name, goos string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	files := map[string]string{}
	for _, f := range dist.Files(goos) {
		files[f] = "содержимое " + f + "\n"
	}
	writeFiles(t, dir, files)
	return dir
}

// touch ставит всем файлам dir время t и права perm — то, что на машине
// сборки разное и в архив попадать не должно.
func touch(t *testing.T, dir string, at time.Time, perm os.FileMode) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, perm); err != nil {
			t.Fatal(err)
		}
	}
}

// Одни и те же файлы с разным временем и правами на диске дают один и тот же
// tar.gz; внутри — каталог, файлы по порядку, права 0755 у бинарей и 0644 у
// остального, владелец 0:0 без имён, время archiveTime, формат ustar.
func TestTarGzReproducible(t *testing.T) {
	const name = "csqtt-cli_1.2.3_linux_amd64"
	a := layout(t, t.TempDir(), name, "linux")
	b := layout(t, t.TempDir(), name, "linux")
	touch(t, a, time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC), 0o600)
	touch(t, b, time.Now(), 0o755)

	pa, pb := filepath.Join(t.TempDir(), "a.tar.gz"), filepath.Join(t.TempDir(), "b.tar.gz")
	for _, p := range [][2]string{{a, pa}, {b, pb}} {
		if err := writeTarGz(p[0], p[1], name, dist.Executables("linux")); err != nil {
			t.Fatal(err)
		}
	}
	ba, _ := os.ReadFile(pa)
	bb, _ := os.ReadFile(pb)
	if !bytes.Equal(ba, bb) {
		t.Fatal("архивы из одинаковых файлов различаются")
	}

	gz, err := gzip.NewReader(bytes.NewReader(ba))
	if err != nil {
		t.Fatal(err)
	}
	if !gz.ModTime.IsZero() || gz.Name != "" || gz.OS != 3 {
		t.Errorf("заголовок gzip: время %v, имя %q, ОС %d", gz.ModTime, gz.Name, gz.OS)
	}
	tr := tar.NewReader(gz)
	var got []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, h.Name)
		if h.Uid != 0 || h.Gid != 0 || h.Uname != "" || h.Gname != "" || !h.ModTime.Equal(archiveTime) || h.Format != tar.FormatUSTAR || len(h.PAXRecords) != 0 {
			t.Errorf("%s: владелец %d:%d %q:%q, время %v, формат %v", h.Name, h.Uid, h.Gid, h.Uname, h.Gname, h.ModTime, h.Format)
		}
		want := int64(0o644)
		switch base := strings.TrimPrefix(h.Name, name+"/"); {
		case h.Typeflag == tar.TypeDir, base == "csqtt-cli", base == "csqtt-core":
			want = 0o755
		}
		if h.Mode != want {
			t.Errorf("%s: права %o, ожидались %o", h.Name, h.Mode, want)
		}
		if h.Typeflag == tar.TypeReg {
			body, _ := io.ReadAll(tr)
			if string(body) != "содержимое "+strings.TrimPrefix(h.Name, name+"/")+"\n" {
				t.Errorf("%s: содержимое %q", h.Name, body)
			}
		}
	}
	want := []string{name + "/", name + "/csqtt-cli", name + "/csqtt-cli.example.toml", name + "/csqtt-core",
		name + "/LICENSE-core.txt", name + "/LICENSE.txt", name + "/README.md", name + "/THIRD_PARTY_NOTICES.txt"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("записи %v, ожидались %v", got, want)
	}
}

// zip — тоже воспроизводимый: каталог с именем архива, записи по порядку,
// без записей каталогов и дескрипторов данных, время archiveTime, атрибуты
// Unix 0644, без поля расширенного времени.
func TestZipReproducible(t *testing.T) {
	const name = "csqtt-cli_1.2.3_windows_amd64"
	a := layout(t, t.TempDir(), name, "windows")
	b := layout(t, t.TempDir(), name, "windows")
	touch(t, a, time.Date(2021, 10, 17, 4, 39, 20, 0, time.Local), 0o600)
	touch(t, b, time.Now(), 0o644)

	pa, pb := filepath.Join(t.TempDir(), "a.zip"), filepath.Join(t.TempDir(), "b.zip")
	for _, p := range [][2]string{{a, pa}, {b, pb}} {
		if err := writeZip(p[0], p[1]); err != nil {
			t.Fatal(err)
		}
	}
	ba, _ := os.ReadFile(pa)
	bb, _ := os.ReadFile(pb)
	if !bytes.Equal(ba, bb) {
		t.Fatal("архивы из одинаковых файлов различаются")
	}

	z, err := zip.NewReader(bytes.NewReader(ba), int64(len(ba)))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"csqtt-cli.example.toml", "csqtt-cli.exe", "csqtt-core.exe", "LICENSE-core.txt",
		"LICENSE.txt", "README.md", "THIRD_PARTY_NOTICES.txt", "wintun-LICENSE.txt", "wintun.dll"}
	if len(z.File) != len(want) {
		t.Fatalf("записей %d, ожидалось %d", len(z.File), len(want))
	}
	d, c := dosTime(archiveTime)
	for i, f := range z.File {
		if full := name + "/" + want[i]; f.Name != full {
			t.Errorf("запись %d: %s, ожидалась %s", i, f.Name, full)
		}
		if f.Flags&0x8 != 0 || f.Method != zip.Deflate || f.CreatorVersion != zipCreator || f.ExternalAttrs != zipAttrs || len(f.Extra) != 0 {
			t.Errorf("%s: флаги %#x, метод %d, создатель %#x, атрибуты %#x, extra %d байт", f.Name, f.Flags, f.Method, f.CreatorVersion, f.ExternalAttrs, len(f.Extra))
		}
		if f.ModifiedDate != d || f.ModifiedTime != c {
			t.Errorf("%s: время %v", f.Name, f.Modified)
		}
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(r)
		r.Close()
		if err != nil || string(body) != "содержимое "+want[i]+"\n" {
			t.Errorf("%s: содержимое %q, %v", f.Name, body, err)
		}
	}
	if fi := z.File[0].FileInfo(); fi.Mode().Perm() != 0o644 {
		t.Errorf("права в zip: %v", fi.Mode())
	}
}

func TestFilesRejectsSubdir(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := files(dir); err == nil {
		t.Error("вложенный каталог прошёл")
	}
}

// Каталог платформы и оба её архива от прошлого запуска убираются, чужие
// файлы — нет.
func TestResetPlatform(t *testing.T) {
	out := t.TempDir()
	const name = "csqtt-cli_1.2.3_linux_amd64"
	layout(t, out, name, "linux")
	writeFiles(t, out, map[string]string{name + ".tar.gz": "old", name + ".zip": "old", "csqtt-cli_1.2.3_linux_arm64.tar.gz": "other"})
	if err := resetPlatform(out, name); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "csqtt-cli_1.2.3_linux_arm64.tar.gz" {
		t.Errorf("осталось: %v", entries)
	}
	if err := resetPlatform(out, name); err != nil {
		t.Errorf("повторно, когда удалять нечего: %v", err)
	}
}

func TestCheckComposition(t *testing.T) {
	for _, goos := range []string{"windows", "linux"} {
		dir := layout(t, t.TempDir(), "r", goos)
		if err := checkComposition(dir, goos); err != nil {
			t.Errorf("%s, полный: %v", goos, err)
		}
		core := dist.Executables(goos)[1]
		if err := os.Remove(filepath.Join(dir, core)); err != nil {
			t.Fatal(err)
		}
		writeFiles(t, dir, map[string]string{"LICENSE": "x"})
		err := checkComposition(dir, goos)
		if err == nil || !strings.Contains(err.Error(), "нет: "+core) || !strings.Contains(err.Error(), "лишние: LICENSE") {
			t.Errorf("%s, без ядра и с лишним: %v", goos, err)
		}
	}
	if err := checkComposition(layout(t, t.TempDir(), "r", "linux"), "windows"); err == nil {
		t.Error("каталог Linux прошёл как Windows")
	}
}

// fakeELF — заголовок ELF64 исполняемого файла для процессора machine
// (62 — x86_64, 183 — aarch64): его читает debug/elf.
func fakeELF(machine uint16) []byte {
	h := make([]byte, 64)
	copy(h, "\x7fELF")
	h[4], h[5], h[6] = 2, 1, 1               // 64 бита, little-endian, версия 1
	binary.LittleEndian.PutUint16(h[16:], 3) // ET_DYN, как у PIE
	binary.LittleEndian.PutUint16(h[18:], machine)
	binary.LittleEndian.PutUint32(h[20:], 1)
	binary.LittleEndian.PutUint16(h[52:], 64) // e_ehsize
	binary.LittleEndian.PutUint16(h[54:], 56) // e_phentsize
	binary.LittleEndian.PutUint16(h[58:], 64) // e_shentsize
	return h
}

// fakePE — заголовки DOS и COFF файла PE для машины machine (0x8664 —
// x86_64, 0xaa64 — arm64): их читает debug/pe. Не короче 96 байт — столько
// он читает заголовка DOS.
func fakePE(machine uint16) []byte {
	h := make([]byte, 128)
	copy(h, "MZ")
	binary.LittleEndian.PutUint32(h[0x3c:], 64)
	copy(h[64:], "PE\x00\x00")
	binary.LittleEndian.PutUint16(h[68:], machine)
	return h
}

// fakePEImports — PE32+ с одним разделом и таблицей импорта: из каждой
// библиотеки dlls по одной функции. Его таблицу читает
// debug/pe.ImportedSymbols.
func fakePEImports(machine uint16, dlls ...string) []byte {
	const (
		optSize = 240    // OptionalHeader64 с 16 каталогами данных
		rawOff  = 0x200  // данные раздела в файле
		rva     = 0x1000 // и в памяти
	)
	// Раздел: дескрипторы (по 20 байт и нулевой в конце), за ними на каждую
	// библиотеку — массив из одного thunk и нуля, «подсказка + имя функции» и
	// имя библиотеки.
	sec := make([]byte, 20*(len(dlls)+1))
	for i, dll := range dlls {
		thunk := len(sec)
		sec = append(sec, make([]byte, 16)...)
		hint := len(sec)
		sec = append(sec, 0, 0)
		sec = append(sec, "f\x00"...)
		name := len(sec)
		sec = append(sec, dll+"\x00"...)
		binary.LittleEndian.PutUint64(sec[thunk:], uint64(rva+hint))
		binary.LittleEndian.PutUint32(sec[20*i:], uint32(rva+thunk))
		binary.LittleEndian.PutUint32(sec[20*i+12:], uint32(rva+name))
	}
	sec = append(sec, make([]byte, (0x200-len(sec)%0x200)%0x200)...)

	h := make([]byte, rawOff)
	copy(h, "MZ")
	binary.LittleEndian.PutUint32(h[0x3c:], 64)
	copy(h[64:], "PE\x00\x00")
	binary.LittleEndian.PutUint16(h[68:], machine)
	binary.LittleEndian.PutUint16(h[70:], 1) // разделов
	binary.LittleEndian.PutUint16(h[84:], optSize)
	opt := h[88:]
	binary.LittleEndian.PutUint16(opt, 0x20b) // PE32+
	binary.LittleEndian.PutUint32(opt[108:], 16)
	binary.LittleEndian.PutUint32(opt[120:], rva) // каталог импорта
	binary.LittleEndian.PutUint32(opt[124:], uint32(20*(len(dlls)+1)))
	sh := h[88+optSize:]
	copy(sh, ".idata")
	binary.LittleEndian.PutUint32(sh[8:], uint32(len(sec)))
	binary.LittleEndian.PutUint32(sh[12:], rva)
	binary.LittleEndian.PutUint32(sh[16:], uint32(len(sec)))
	binary.LittleEndian.PutUint32(sh[20:], rawOff)
	return append(h, sec...)
}

func writeBytes(t *testing.T, dir, name string, b []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCheckCore(t *testing.T) {
	dir := t.TempDir()
	amd64 := writeBytes(t, dir, "amd64", fakeELF(62))
	arm64 := writeBytes(t, dir, "arm64", fakeELF(183))
	exe := writeBytes(t, dir, "core.exe", fakePE(0x8664))
	text := writeBytes(t, dir, "text", []byte("#!/bin/sh\n"))
	static := writeBytes(t, dir, "static.exe", fakePEImports(0x8664, "KERNEL32.dll", "ws2_32.dll", "api-ms-win-core-synch-l1-2-0.dll"))
	vcrt := writeBytes(t, dir, "vcrt.exe", fakePEImports(0x8664, "KERNEL32.dll", "VCRUNTIME140.dll"))
	ucrt := writeBytes(t, dir, "ucrt.exe", fakePEImports(0x8664, "api-ms-win-crt-runtime-l1-1-0.dll", "api-ms-win-crt-heap-l1-1-0.dll"))

	for _, c := range []struct {
		path, goos, goarch, err string
	}{
		{amd64, "linux", "amd64", ""},
		{arm64, "linux", "arm64", ""},
		{exe, "windows", "amd64", ""},
		{static, "windows", "amd64", ""},
		{vcrt, "windows", "amd64", "динамическим CRT (импортирует VCRUNTIME140.dll)"},
		{ucrt, "windows", "amd64", "импортирует api-ms-win-crt-heap-l1-1-0.dll, api-ms-win-crt-runtime-l1-1-0.dll) и без VC++ Redistributable не запустится — нужно +crt-static"},
		{arm64, "linux", "amd64", "ядро для aarch64, а нужно для x86_64"},
		{amd64, "linux", "arm64", "ядро для x86_64, а нужно для aarch64"},
		{exe, "linux", "amd64", "не ELF"},
		{amd64, "windows", "amd64", "не PE"},
		{text, "linux", "amd64", "не ELF"},
		{filepath.Join(dir, "missing"), "linux", "amd64", "missing"},
	} {
		err := checkCore(c.path, c.goos, c.goarch)
		if c.err == "" && err != nil || c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)) {
			t.Errorf("%s как %s/%s: %v", filepath.Base(c.path), c.goos, c.goarch, err)
		}
	}
}

// plan: ядро — готовое (-core) или от buildcore; всё, что мешает, — одной
// ошибкой до начала работы и со всеми платформами сразу.
func TestPlan(t *testing.T) {
	root := t.TempDir()
	cores := t.TempDir()
	exe := writeBytes(t, cores, "csqtt-core.exe", fakePE(0x8664))
	arm64 := writeBytes(t, cores, "csqtt-core-arm64", fakeELF(183))
	amd64 := writeBytes(t, cores, "csqtt-core-amd64", fakeELF(62))

	// buildcore -check здесь собирает только linux/amd64.
	canBuild := func(target string) error {
		if target == "x86_64-unknown-linux-gnu" {
			return nil
		}
		return errors.New("buildcore: ядро под " + target + " на этой машине не собирается")
	}
	all := []string{"windows/amd64", "linux/amd64", "linux/arm64"}

	builds, err := plan(root, options{Platforms: all, Cores: map[string]string{"windows/amd64": exe, "linux/arm64": arm64}}, canBuild)
	if err != nil {
		t.Fatal(err)
	}
	if len(builds) != 3 {
		t.Fatalf("сборок %d", len(builds))
	}
	if b := builds[0]; b.Built || b.Core != exe || b.Target != "x86_64-pc-windows-msvc" {
		t.Errorf("windows — готовое ядро: %+v", b)
	}
	if b := builds[1]; !b.Built || b.Core != filepath.Join(root, "build", "release-core", "linux_amd64", "csqtt-core") || b.Target != "x86_64-unknown-linux-gnu" {
		t.Errorf("linux/amd64 — buildcore: %+v", b)
	}
	if b := builds[2]; b.Built || b.Core != arm64 || b.Target != "aarch64-unknown-linux-gnu" {
		t.Errorf("linux/arm64 — готовое ядро: %+v", b)
	}

	// Ядра для Windows и arm64 нет и здесь его не собрать; ядро amd64 вместо
	// arm64 — тоже ошибка. Названы все сразу, с подсказкой -core.
	_, err = plan(root, options{Platforms: all, Cores: map[string]string{"linux/arm64": amd64}}, canBuild)
	for _, want := range []string{
		"windows/amd64: buildcore: ядро под x86_64-pc-windows-msvc",
		"-core windows/amd64=<путь к csqtt-core.exe>",
		"linux/arm64: " + amd64 + ": ядро для x86_64, а нужно для aarch64",
	} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("нет %q в ошибке: %v", want, err)
		}
	}

	for _, c := range []struct {
		name string
		o    options
		err  string
	}{
		{"-core чужой платформы", options{Platforms: []string{"linux/amd64"}, Cores: map[string]string{"linux/arm64": arm64}}, "такой платформы нет в -platforms"},
		{"macOS", options{Platforms: []string{"darwin/arm64"}}, "цели Rust нет"},
		{"без arch", options{Platforms: []string{"linux"}}, "os/arch"},
		{"лишний слэш", options{Platforms: []string{"linux/amd64/v3"}}, "os/arch"},
		{"дважды", options{Platforms: []string{"linux/amd64", "linux/amd64"}}, "дважды"},
		{"нет файла", options{Platforms: []string{"linux/amd64"}, Cores: map[string]string{"linux/amd64": filepath.Join(cores, "nope")}}, "nope"},
	} {
		if _, err := plan(root, c.o, canBuild); err == nil || !strings.Contains(err.Error(), c.err) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

func TestCoreFlag(t *testing.T) {
	c := coreFlag{}
	if err := c.Set("linux/arm64=build/ci/csqtt-core"); err != nil || c["linux/arm64"] != "build/ci/csqtt-core" {
		t.Errorf("%v, %v", c, err)
	}
	for _, bad := range []string{"linux/arm64=x", "linux/amd64", "=x", "linux/amd64="} {
		if err := c.Set(bad); err == nil {
			t.Errorf("%q принят", bad)
		}
	}
}

// probeCore ждёт от ядра строку использования clap и код 2.
func TestProbeCore(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("подставное ядро — сценарий sh")
	}
	dir := t.TempDir()
	good := writeBytes(t, dir, "good", []byte("#!/bin/sh\necho \"error: unexpected argument '$1' found\" >&2\necho 'Usage: csqtt-core [OPTIONS]' >&2\nexit 2\n"))
	if err := probeCore(good); err != nil {
		t.Errorf("ответ как у clap: %v", err)
	}
	zero := writeBytes(t, dir, "zero", []byte("#!/bin/sh\necho 'Usage: x'\nexit 0\n"))
	if err := probeCore(zero); err == nil {
		t.Error("код 0 прошёл")
	}
	silent := writeBytes(t, dir, "silent", []byte("#!/bin/sh\nexit 2\n"))
	if err := probeCore(silent); err == nil {
		t.Error("без строки использования прошёл")
	}
	if err := probeCore(filepath.Join(dir, "missing")); err == nil {
		t.Error("нет файла — прошёл")
	}
}

func TestSanitizeVersion(t *testing.T) {
	if got := sanitizeVersion("feature/x y\\z w"); got != "feature-x-y-z-w" {
		t.Errorf("%q", got)
	}
}

func TestSizeMB(t *testing.T) {
	for size, want := range map[int64]string{
		18 << 20:                "18",
		(18 << 20) + (2<<20)/10: "18.2",
		(1 << 20) / 4:           "0.2", // 0.25 к чётному
		(1 << 20) * 3 / 4:       "0.8", // 0.75 к чётному
	} {
		path := filepath.Join(t.TempDir(), "f")
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(path, size); err != nil {
			t.Fatal(err)
		}
		if got, _ := sizeMB(path); got != want {
			t.Errorf("%d байт: %s, ожидалось %s", size, got, want)
		}
	}
}
