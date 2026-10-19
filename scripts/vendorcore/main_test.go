package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Сверка с настоящим апстримом — `go run ./scripts/vendorcore -upstream <клон>
// -check`: клона апстрима в тестах нет. Здесь — движок правил и перенос из
// поддельного апстрима.

// withRules подменяет набор правил на время теста.
func withRules(t *testing.T, rs ...rule) {
	t.Helper()
	saved := rules
	t.Cleanup(func() { rules = saved })
	rules = rs
}

// Замена целиком и вставки до и после — с отступом совпавшей строки.
func TestApplyRules(t *testing.T) {
	withRules(t,
		rule{File: "a.rs", Count: 1, Pattern: `(?m)^( +)const T: u64 = 60;$`, Replace: to("${1}const T: u64 = 180;")},
		rule{File: "a.rs", Count: 1, Pattern: `(?m)^( +)let s = bind\(\)\?;$`, InsertAfter: "bind_if(&s)?;"},
		rule{File: "a.rs", Count: 1, Pattern: `(?m)^( +)run\(s\);$`, InsertBefore: "log(\"run\");"},
		rule{File: "b.rs", Count: 1, Pattern: `x`, Replace: to("y")},
	)
	in := "fn f() {\n    const T: u64 = 60;\n        let s = bind()?;\n    run(s);\n}\n"
	got, err := applyRules("a.rs", in)
	if err != nil {
		t.Fatal(err)
	}
	want := "fn f() {\n    const T: u64 = 180;\n        let s = bind()?;\n        bind_if(&s)?;\n    log(\"run\");\n    run(s);\n}\n"
	if got != want {
		t.Errorf("получилось:\n%s\nожидалось:\n%s", got, want)
	}
}

// Шаблон не совпал нужное число раз — ошибка с путём файла и счётом, а не
// тихий пропуск.
func TestApplyRulesCountMismatch(t *testing.T) {
	withRules(t, rule{File: "rust-client/captcha.rs", Count: 1, Pattern: `nothing`, Replace: to("")})
	_, err := applyRules("rust-client/captcha.rs", "fn main() {}\n")
	if err == nil || !strings.Contains(err.Error(), "rust-client/captcha.rs") || !strings.Contains(err.Error(), "найдено 0") {
		t.Errorf("ошибка: %v", err)
	}

	withRules(t, rule{File: "a.rs", Count: 1, Pattern: `x`, Replace: to("y")})
	if _, err := applyRules("a.rs", "x x\n"); err == nil || !strings.Contains(err.Error(), "найдено 2") {
		t.Errorf("два совпадения вместо одного: %v", err)
	}
}

func TestApplyRulesNoAction(t *testing.T) {
	withRules(t, rule{File: "a.rs", Count: 1, Pattern: `x`})
	if _, err := applyRules("a.rs", "x\n"); err == nil || !strings.Contains(err.Error(), "не задаёт") {
		t.Errorf("ошибка: %v", err)
	}
}

// --- перенос из поддельного апстрима -----------------------------------------

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// fakeUpstream — апстрим с тем же устройством, что настоящий: файлы с CRLF,
// как их отдаёт клон на Windows, подкаталог и сборка cargo, которую
// переносить нельзя.
//
// Вендоренных крейтов и сокетов у него нет: их наборы на время теста пусты.
func fakeUpstream(t *testing.T) string {
	t.Helper()
	withCrates(t)
	withSockets(t)
	up := t.TempDir()
	write(t, filepath.Join(up, "LICENSE"), "PolyForm\r\n")
	write(t, filepath.Join(up, "rust-client", "Cargo.toml"), "[package]\r\nname = \"csqtt-client\"\r\nversion = \"9.9.9\"\r\n")
	write(t, filepath.Join(up, "rust-client", "main.rs"), "#[path = \"../shared/wire_protocol.rs\"]\r\nmod wire_protocol;\r\nconst T: u64 = 60;\r\n")
	write(t, filepath.Join(up, "rust-client", "dispatcher", "tests.rs"), "// tests\r\n")
	write(t, filepath.Join(up, "rust-client", "target", "release", "client.exe"), "MZ\x00\x00")
	write(t, filepath.Join(up, "shared", "wire_protocol.rs"), "pub const WIRE_PROTOCOL_REVISION: &str = \"TEST-WIRE-1\";\r\n")
	for _, f := range excludedFiles {
		write(t, filepath.Join(up, filepath.FromSlash(f.Path)), "android\r\n")
	}
	return up
}

func mustRun(t *testing.T, root, up string, check bool) int {
	t.Helper()
	code, err := run(root, up, check)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func TestRunWriteAndCheck(t *testing.T) {
	withRules(t, rule{File: "rust-client/main.rs", Count: 1, Pattern: `(?m)^()const T: u64 = 60;$`, Replace: to("${1}const T: u64 = 180;")})
	root, up := t.TempDir(), fakeUpstream(t)
	core := filepath.Join(root, "core")

	if code := mustRun(t, root, up, false); code != 0 {
		t.Fatalf("перенос: код %d", code)
	}
	if got := read(t, filepath.Join(core, "rust-client", "main.rs")); got != "#[path = \"../shared/wire_protocol.rs\"]\nmod wire_protocol;\nconst T: u64 = 180;\n" {
		t.Errorf("main.rs: правило не применилось или остался CRLF:\n%q", got)
	}
	if got := read(t, filepath.Join(core, "LICENSE")); got != "PolyForm\n" {
		t.Errorf("LICENSE: %q", got)
	}
	version := read(t, filepath.Join(core, "CORE_VERSION"))
	for _, line := range []string{"version  = 9.9.9\n", "protocol = TEST-WIRE-1\n", "files    = 5\n", "rules    = 1\n"} {
		if !strings.Contains(version, line) {
			t.Errorf("CORE_VERSION без %q:\n%s", line, version)
		}
	}
	for _, rel := range []string{"rust-client/target", "rust-client/build_android_abis.ps1", "rust-client/build_so.bat"} {
		if _, err := os.Stat(filepath.Join(core, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Errorf("%s перенесён: %v", rel, err)
		}
	}

	if code := mustRun(t, root, up, true); code != 0 {
		t.Fatalf("-check сразу после переноса: код %d", code)
	}

	// Повторный перенос ничего не меняет.
	before := read(t, filepath.Join(core, "rust-client", "main.rs"))
	mustRun(t, root, up, false)
	if after := read(t, filepath.Join(core, "rust-client", "main.rs")); after != before {
		t.Errorf("повторный перенос изменил main.rs")
	}

	// git на Windows может выложить core/ с CRLF — это не расхождение. Выход
	// сборки cargo — тоже.
	write(t, filepath.Join(core, "shared", "wire_protocol.rs"), "pub const WIRE_PROTOCOL_REVISION: &str = \"TEST-WIRE-1\";\r\n")
	write(t, filepath.Join(core, "rust-client", "target", "debug", "client"), "ELF")
	if code := mustRun(t, root, up, true); code != 0 {
		t.Errorf("-check на CRLF и target/: код %d", code)
	}
}

// Каждое расхождение роняет -check: правка руками, лишний файл, в том числе
// исключённый, и пропавший файл.
func TestRunCheckFails(t *testing.T) {
	withRules(t)
	up := fakeUpstream(t)
	for name, spoil := range map[string]func(t *testing.T, core string){
		"правка": func(t *testing.T, core string) {
			write(t, filepath.Join(core, "rust-client", "dispatcher", "tests.rs"), "// tests!\n")
		},
		"лишний": func(t *testing.T, core string) {
			write(t, filepath.Join(core, "shared", "extra.rs"), "\n")
		},
		"исключённый": func(t *testing.T, core string) {
			write(t, filepath.Join(core, "rust-client", "build_so.bat"), "android\n")
		},
		"пропал": func(t *testing.T, core string) {
			if err := os.Remove(filepath.Join(core, "LICENSE")); err != nil {
				t.Fatal(err)
			}
		},
		"CORE_VERSION": func(t *testing.T, core string) {
			write(t, filepath.Join(core, "CORE_VERSION"), "x\n")
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			mustRun(t, root, up, false)
			spoil(t, filepath.Join(root, "core"))
			if code := mustRun(t, root, up, true); code != 1 {
				t.Errorf("-check: код %d, ожидался 1", code)
			}
			// Перенос возвращает core/ к апстриму.
			mustRun(t, root, up, false)
			if code := mustRun(t, root, up, true); code != 0 {
				t.Errorf("-check после переноса: код %d", code)
			}
		})
	}
}

// Свои файлы из addedDir ложатся в core/ с LF, -check их сверяет, а файл с
// тем же путём в апстриме — ошибка до записи.
func TestRunAddedFiles(t *testing.T) {
	withRules(t)
	root, up := t.TempDir(), fakeUpstream(t)
	core := filepath.Join(root, "core")
	own := filepath.Join(root, filepath.FromSlash(addedDir), "rust-client", "own.rs")
	write(t, own, "// своё\r\n")

	mustRun(t, root, up, false)
	if got := read(t, filepath.Join(core, "rust-client", "own.rs")); got != "// своё\n" {
		t.Errorf("own.rs: %q", got)
	}
	if version := read(t, filepath.Join(core, "CORE_VERSION")); !strings.Contains(version, "files    = 6\n") || !strings.Contains(version, "added    = 1\n") {
		t.Errorf("CORE_VERSION:\n%s", version)
	}
	if code := mustRun(t, root, up, true); code != 0 {
		t.Errorf("-check после переноса: код %d", code)
	}
	write(t, filepath.Join(core, "rust-client", "own.rs"), "// изменено\n")
	if code := mustRun(t, root, up, true); code != 1 {
		t.Errorf("-check после правки своего файла в core/: код %d, ожидался 1", code)
	}

	root = t.TempDir()
	write(t, filepath.Join(root, filepath.FromSlash(addedDir), "rust-client", "main.rs"), "\n")
	if _, err := run(root, up, false); err == nil || !strings.Contains(err.Error(), "есть и в апстриме") {
		t.Errorf("ошибка %v, ожидалась о совпадении с апстримом", err)
	}
	if _, err := os.Stat(filepath.Join(root, "core")); !os.IsNotExist(err) {
		t.Errorf("core/ создан: %v", err)
	}
}

// Лишние файлы перенос удаляет вместе с опустевшими каталогами.
func TestRunRemovesExtra(t *testing.T) {
	withRules(t)
	root, up := t.TempDir(), fakeUpstream(t)
	core := filepath.Join(root, "core")
	write(t, filepath.Join(core, "rust-client", "old", "gone.rs"), "\n")
	mustRun(t, root, up, false)
	if _, err := os.Stat(filepath.Join(core, "rust-client", "old")); !os.IsNotExist(err) {
		t.Errorf("каталог old остался: %v", err)
	}
}

// Ошибки апстрима — до записи: core/ не создаётся.
func TestRunUpstreamErrors(t *testing.T) {
	cases := map[string]struct {
		rules []rule
		spoil func(t *testing.T, up string)
		want  string
	}{
		"правило на пропавший файл": {
			rules: []rule{{File: "rust-client/gone.rs", Count: 1, Pattern: `x`, Replace: to("y")}},
			want:  "правило ссылается на rust-client/gone.rs",
		},
		"правило не сошлось": {
			rules: []rule{{File: "rust-client/main.rs", Count: 1, Pattern: `nothing`, Replace: to("")}},
			want:  "найдено 0",
		},
		"исключённый переименован": {
			spoil: func(t *testing.T, up string) {
				if err := os.Remove(filepath.Join(up, filepath.FromSlash(excludedFiles[0].Path))); err != nil {
					t.Fatal(err)
				}
			},
			want: "исключённого файла",
		},
		"нет shared": {
			spoil: func(t *testing.T, up string) {
				if err := os.RemoveAll(filepath.Join(up, "shared")); err != nil {
					t.Fatal(err)
				}
			},
			want: "нет каталога shared",
		},
		"нет ревизии протокола": {
			spoil: func(t *testing.T, up string) {
				write(t, filepath.Join(up, "shared", "wire_protocol.rs"), "// пусто\n")
			},
			want: "shared/wire_protocol.rs",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			withRules(t, c.rules...)
			root, up := t.TempDir(), fakeUpstream(t)
			if c.spoil != nil {
				c.spoil(t, up)
			}
			_, err := run(root, up, false)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("ошибка %v, ожидалась с %q", err, c.want)
			}
			if _, err := os.Stat(filepath.Join(root, "core")); !os.IsNotExist(err) {
				t.Errorf("core/ создан: %v", err)
			}
		})
	}
}
