package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Сама сборка — `go run ./scripts/buildcore`: cargo и Rust в тестах не
// нужны. Здесь — проверки до сборки и окружение cargo.

func TestDefaultTarget(t *testing.T) {
	for in, want := range map[string]string{
		"windows/amd64": "x86_64-pc-windows-msvc",
		"linux/amd64":   "x86_64-unknown-linux-gnu",
		"linux/arm64":   "aarch64-unknown-linux-gnu",
		"darwin/arm64":  "",
	} {
		goos, goarch, _ := strings.Cut(in, "/")
		got, err := defaultTarget(goos, goarch)
		if got != want || (err != nil) != (want == "") {
			t.Errorf("%s: %q, %v", in, got, err)
		}
	}
}

func TestExeName(t *testing.T) {
	if got := exeName("x86_64-pc-windows-msvc", "csqtt-core"); got != "csqtt-core.exe" {
		t.Errorf("windows: %q", got)
	}
	if got := exeName("aarch64-unknown-linux-gnu", "csqtt-core"); got != "csqtt-core" {
		t.Errorf("linux: %q", got)
	}
}

func TestCheckRustc(t *testing.T) {
	const manifest = "[package]\nname = \"csqtt-client\"\nrust-version = \"1.97.1\"\n"
	for rustc, want := range map[string]string{
		"rustc 1.97.1 (abc 2026-01-01)\n":         "",
		"rustc 1.98.0 (88d9e12ae 2026-08-18)\n":   "",
		"rustc 2.0.0\n":                           "",
		"rustc 1.99.0-nightly (abc 2026-09-01)\n": "",
		"rustc 1.97.0 (abc 2026-01-01)\n":         "старше",
		"rustc 1.96.5\n":                          "старше",
		"cargo 1.98.0\n":                          "не разобрать",
	} {
		err := checkRustc(rustc, manifest)
		if want == "" && err != nil || want != "" && (err == nil || !strings.Contains(err.Error(), want)) {
			t.Errorf("%q: %v", rustc, err)
		}
	}
	if err := checkRustc("rustc 1.98.0\n", "[package]\n"); err == nil || !strings.Contains(err.Error(), "rust-version") {
		t.Errorf("без rust-version: %v", err)
	}
}

// envValue — значение переменной name из списка KEY=VALUE.
func envValue(env []string, name string) string {
	for _, kv := range env {
		if k, v, _ := strings.Cut(kv, "="); k == name {
			return v
		}
	}
	return ""
}

func TestBuildEnvRemap(t *testing.T) {
	cargo := filepath.Join("C:", "home", "user", ".cargo")
	root := filepath.Join("C:", "home", "user", "src", "csqtt-cli")
	env := buildEnv("x86_64-unknown-linux-gnu", cargo, root)
	flags := strings.Split(envValue(env, "CARGO_ENCODED_RUSTFLAGS"), "\x1f")
	want := []string{"--remap-path-prefix=" + cargo + "=/cargo", "--remap-path-prefix=" + root + "=/csqtt-cli"}
	if strings.Join(flags, "|") != strings.Join(want, "|") {
		t.Errorf("флаги %q", flags)
	}
	for _, target := range []string{"x86_64-unknown-linux-gnu", "aarch64-unknown-linux-gnu"} {
		if v := envValue(buildEnv(target, cargo, root), "CARGO_ENCODED_RUSTFLAGS"); strings.Contains(v, "crt-static") {
			t.Errorf("%s: crt-static для gnu: %q", target, v)
		}
	}
	if v := envValue(env, "CFLAGS_x86_64_unknown_linux_gnu"); v != "" {
		t.Errorf("CFLAGS для gcc: %q — aws-lc-sys ставит -ffile-prefix-map сам", v)
	}

	// CARGO_HOME внутри репозитория: вложенный каталог — вторым, иначе
	// репозиторий перебьёт его.
	inner := filepath.Join(root, ".cargo")
	flags = strings.Split(envValue(buildEnv("x86_64-unknown-linux-gnu", inner, root), "CARGO_ENCODED_RUSTFLAGS"), "\x1f")
	if len(flags) != 2 || !strings.Contains(flags[1], "=/cargo") {
		t.Errorf("вложенный CARGO_HOME: %q", flags)
	}
}

func TestBuildEnvMSVC(t *testing.T) {
	cargo, root := `C:\home\user\.cargo`, `C:\home\user\src\csqtt-cli`
	env := buildEnv("x86_64-pc-windows-msvc", cargo, root)

	// Статический CRT — к пути переписанным префиксам, а не вместо них.
	flags := strings.Split(envValue(env, "CARGO_ENCODED_RUSTFLAGS"), "\x1f")
	want := []string{"--remap-path-prefix=" + cargo + "=/cargo", "--remap-path-prefix=" + root + "=/csqtt-cli", "-Ctarget-feature=+crt-static"}
	if strings.Join(flags, "|") != strings.Join(want, "|") {
		t.Errorf("флаги %q", flags)
	}

	v := envValue(env, "CFLAGS_x86_64_pc_windows_msvc")
	for _, want := range []string{`/d1trimfile:` + cargo + `\`, `/d1trimfile:` + root + `\`} {
		if !strings.Contains(v, want) {
			t.Errorf("CFLAGS без %q: %q", want, v)
		}
	}
}

func TestCrossEnv(t *testing.T) {
	none := func(string) string { return "" }

	// Своя система и процессор — без кросс-gcc; другая сборка той же
	// системы (msvc и gnu) — тоже: тулчейн разберётся сам.
	for _, c := range [][2]string{
		{"x86_64-unknown-linux-gnu", "x86_64-unknown-linux-gnu"},
		{"aarch64-unknown-linux-gnu", "aarch64-unknown-linux-gnu"},
		{"x86_64-pc-windows-msvc", "x86_64-pc-windows-gnu"},
	} {
		env, tools, err := crossEnv(c[0], c[1], none)
		if env != nil || tools != nil || err != nil {
			t.Errorf("%s → %s: %q, %q, %v", c[0], c[1], env, tools, err)
		}
	}

	env, tools, err := crossEnv("x86_64-unknown-linux-gnu", "aarch64-unknown-linux-gnu", none)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"CARGO_TARGET_AARCH64_UNKNOWN_LINUX_GNU_LINKER=aarch64-linux-gnu-gcc",
		"CC_aarch64_unknown_linux_gnu=aarch64-linux-gnu-gcc",
		"AR_aarch64_unknown_linux_gnu=aarch64-linux-gnu-ar",
	}
	if strings.Join(env, " ") != strings.Join(want, " ") || strings.Join(tools, " ") != "aarch64-linux-gnu-gcc aarch64-linux-gnu-ar" {
		t.Errorf("x86_64 → aarch64: %q, %q", env, tools)
	}

	// Заданное в окружении не перебивается, а проверяется как есть.
	custom := func(name string) string {
		if name == "CC_aarch64_unknown_linux_gnu" {
			return "/opt/cross/bin/cc"
		}
		return ""
	}
	env, tools, _ = crossEnv("x86_64-unknown-linux-gnu", "aarch64-unknown-linux-gnu", custom)
	if len(env) != 2 || strings.Contains(strings.Join(env, " "), "CC_") || !slices.Contains(tools, "/opt/cross/bin/cc") {
		t.Errorf("свой CC: %q, %q", env, tools)
	}

	if env, _, _ := crossEnv("aarch64-unknown-linux-gnu", "x86_64-unknown-linux-gnu", none); len(env) != 3 || !strings.Contains(env[0], "=x86_64-linux-gnu-gcc") {
		t.Errorf("aarch64 → x86_64: %q", env)
	}

	// Другая система — ошибка до сборки, а не сбой cargo на середине.
	for _, c := range [][2]string{
		{"x86_64-unknown-linux-gnu", "x86_64-pc-windows-msvc"},
		{"x86_64-pc-windows-msvc", "x86_64-unknown-linux-gnu"},
		{"x86_64-pc-windows-msvc", "aarch64-unknown-linux-gnu"},
		{"", "aarch64-unknown-linux-gnu"},
	} {
		if _, _, err := crossEnv(c[0], c[1], none); err == nil || !strings.Contains(err.Error(), "не собирается") {
			t.Errorf("%s → %s: %v", c[0], c[1], err)
		}
	}
}

func TestCheckTools(t *testing.T) {
	err := checkTools("aarch64-unknown-linux-gnu", []string{"csqtt-no-such-gcc"})
	if err == nil || !strings.Contains(err.Error(), "csqtt-no-such-gcc") || !strings.Contains(err.Error(), "gcc-aarch64-linux-gnu") {
		t.Errorf("нет gcc: %v", err)
	}
	if err := checkTools("x86_64-unknown-linux-gnu", []string{"csqtt-no-such-gcc"}); err == nil || !strings.Contains(err.Error(), "gcc-x86-64-linux-gnu") {
		t.Errorf("имя пакета для x86_64: %v", err)
	}
	if err := checkTools("aarch64-unknown-linux-gnu", nil); err != nil {
		t.Errorf("без инструментов: %v", err)
	}
}
