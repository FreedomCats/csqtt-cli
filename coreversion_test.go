package csqttcli

import (
	"os"
	"regexp"
	"testing"
)

// Версия ядра — та, что в Cargo.toml перенесённого ядра: CORE_VERSION пишется
// из него же (scripts/vendorcore).
func TestCoreVersion(t *testing.T) {
	cargo, err := os.ReadFile("core/rust-client/Cargo.toml")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^version\s*=\s*"([^"]+)"`).FindSubmatch(cargo)
	if m == nil {
		t.Fatal("в Cargo.toml ядра нет version")
	}
	if CoreVersion != string(m[1]) {
		t.Errorf("CoreVersion = %q, в Cargo.toml ядра — %q", CoreVersion, m[1])
	}
}

func TestParseCoreVersion(t *testing.T) {
	for _, c := range []struct{ file, want string }{
		{"# шапка\nupstream = x\nversion  = 2.5.0\nfiles    = 1\n", "2.5.0"},
		{"# шапка\r\nversion  = 2.5.0\r\nfiles    = 1\r\n", "2.5.0"},
		{"version = 9.9.9", "9.9.9"},
		{"upstream = x\n", "неизвестна"},
		{"version =\n", "неизвестна"},
	} {
		if got := parseCoreVersion(c.file); got != c.want {
			t.Errorf("parseCoreVersion(%q) = %q, ожидалось %q", c.file, got, c.want)
		}
	}
}
