package main

import (
	"path/filepath"
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
	v := envValue(buildEnv("x86_64-pc-windows-msvc", cargo, root), "CFLAGS_x86_64_pc_windows_msvc")
	for _, want := range []string{`/d1trimfile:` + cargo + `\`, `/d1trimfile:` + root + `\`} {
		if !strings.Contains(v, want) {
			t.Errorf("CFLAGS без %q: %q", want, v)
		}
	}
}
