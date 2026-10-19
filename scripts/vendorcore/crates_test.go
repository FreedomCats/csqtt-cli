package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withCrates подменяет набор вендоренных крейтов на время теста.
func withCrates(t *testing.T, cs ...vendoredCrate) {
	t.Helper()
	saved := vendoredCrates
	t.Cleanup(func() { vendoredCrates = saved })
	vendoredCrates = cs
}

// crateEntry — запись архива .crate: путь целиком и содержимое; link — не
// обычный файл, а символическая ссылка.
type crateEntry struct {
	name, body string
	link       bool
}

// crateArchive — архив .crate (tar.gz) из записей.
func crateArchive(t *testing.T, entries ...crateEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		if e.link {
			h.Typeflag, h.Linkname, h.Size = tar.TypeSymlink, e.body, 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if !e.link {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// lockWith — Cargo.lock с пакетом name версии version с crates.io.
func lockWith(name, version, checksum string) string {
	return "[[package]]\nname = \"" + name + "\"\nversion = \"" + version + "\"\n" +
		"source = \"registry+https://github.com/rust-lang/crates.io-index\"\nchecksum = \"" + checksum + "\"\n"
}

var fakeCrate = vendoredCrate{
	Name: "fake-util",
	Dir:  "rust-client/vendor/fake-util",
	Lock: "rust-client/Cargo.lock",
	Skip: map[string]string{".gitignore": "тест"},
}

func goodArchive(t *testing.T) []byte {
	return crateArchive(t,
		crateEntry{name: "fake-util-1.0.0/Cargo.toml", body: "[package]\r\nname = \"fake-util\"\r\n"},
		crateEntry{name: "fake-util-1.0.0/.gitignore", body: "Cargo.lock\n"},
		crateEntry{name: "fake-util-1.0.0/src/lib.rs", body: "pub mod rt;\n"},
	)
}

// setupCrate — поддельный апстрим с Cargo.lock, где записан крейт, и архив
// крейта в cratesDir корня root.
func setupCrate(t *testing.T, archive []byte, lockSum string) (root, up string) {
	t.Helper()
	root, up = t.TempDir(), fakeUpstream(t)
	withCrates(t, fakeCrate)
	write(t, filepath.Join(up, "rust-client", "Cargo.lock"), lockWith("fake-util", "1.0.0", lockSum))
	write(t, filepath.Join(root, filepath.FromSlash(cratesDir), "fake-util-1.0.0.crate"), string(archive))
	return root, up
}

// Крейт из архива ложится в core/ с LF, без пропущенных файлов; правила
// применяются к нему, как к файлам апстрима; -check его сверяет.
func TestRunCrate(t *testing.T) {
	archive := goodArchive(t)
	root, up := setupCrate(t, archive, sum(archive))
	withRules(t, rule{File: "rust-client/vendor/fake-util/src/lib.rs", Count: 1, Pattern: `(?m)^()pub mod rt;$`, InsertAfter: "pub mod csqtt_bind;"})
	core := filepath.Join(root, "core")

	mustRun(t, root, up, false)
	if got := read(t, filepath.Join(core, "rust-client", "vendor", "fake-util", "src", "lib.rs")); got != "pub mod rt;\npub mod csqtt_bind;\n" {
		t.Errorf("lib.rs: правило не применилось:\n%q", got)
	}
	if got := read(t, filepath.Join(core, "rust-client", "vendor", "fake-util", "Cargo.toml")); got != "[package]\nname = \"fake-util\"\n" {
		t.Errorf("Cargo.toml: %q", got)
	}
	if _, err := os.Stat(filepath.Join(core, "rust-client", "vendor", "fake-util", ".gitignore")); !os.IsNotExist(err) {
		t.Errorf(".gitignore перенесён: %v", err)
	}
	if version := read(t, filepath.Join(core, "CORE_VERSION")); !strings.Contains(version, "crate    = fake-util 1.0.0 sha256:"+sum(archive)+"\n") {
		t.Errorf("CORE_VERSION без крейта:\n%s", version)
	}
	if code := mustRun(t, root, up, true); code != 0 {
		t.Errorf("-check после переноса: код %d", code)
	}
	write(t, filepath.Join(core, "rust-client", "vendor", "fake-util", "src", "lib.rs"), "pub mod rt;\n")
	if code := mustRun(t, root, up, true); code != 1 {
		t.Errorf("-check после правки крейта в core/: код %d, ожидался 1", code)
	}
}

// Ошибки крейта — до записи: core/ не создаётся.
func TestRunCrateErrors(t *testing.T) {
	good := goodArchive(t)
	cases := map[string]struct {
		archive []byte
		lockSum string
		spoil   func(t *testing.T, root, up string)
		want    string
	}{
		"нет архива новой версии": {
			archive: good, lockSum: sum(good),
			spoil: func(t *testing.T, root, up string) {
				write(t, filepath.Join(up, "rust-client", "Cargo.lock"), lockWith("fake-util", "1.0.1", sum(good)))
			},
			want: "апстрим требует fake-util 1.0.1, а архива scripts/vendorcore/crates/fake-util-1.0.1.crate нет",
		},
		"сумма не та": {
			archive: good, lockSum: strings.Repeat("0", 64),
			want: "сумма sha256",
		},
		"крейта нет в Cargo.lock": {
			archive: good, lockSum: sum(good),
			spoil: func(t *testing.T, root, up string) {
				write(t, filepath.Join(up, "rust-client", "Cargo.lock"), lockWith("other", "1.0.0", sum(good)))
			},
			want: "нет fake-util с crates.io",
		},
		"две версии": {
			archive: good, lockSum: sum(good),
			spoil: func(t *testing.T, root, up string) {
				write(t, filepath.Join(up, "rust-client", "Cargo.lock"),
					lockWith("fake-util", "1.0.0", sum(good))+"\n"+lockWith("fake-util", "0.9.0", sum(good)))
			},
			want: "2 версий fake-util",
		},
		"путь вне крейта": {
			archive: crateArchive(t, crateEntry{name: "fake-util-1.0.0/../evil.rs", body: "x"}),
			want:    "вне каталога fake-util-1.0.0",
		},
		"чужой каталог": {
			archive: crateArchive(t, crateEntry{name: "other-1.0.0/src/lib.rs", body: "x"}),
			want:    "вне каталога fake-util-1.0.0",
		},
		"ссылка": {
			archive: crateArchive(t, crateEntry{name: "fake-util-1.0.0/src/lib.rs", body: "/etc/passwd", link: true}),
			want:    "не обычный файл",
		},
		"файл есть в апстриме": {
			archive: good, lockSum: sum(good),
			spoil: func(t *testing.T, root, up string) {
				write(t, filepath.Join(up, "rust-client", "vendor", "fake-util", "src", "lib.rs"), "\n")
			},
			want: "есть и в апстриме, и в крейте fake-util",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			withRules(t)
			lockSum := c.lockSum
			if lockSum == "" {
				lockSum = sum(c.archive)
			}
			root, up := setupCrate(t, c.archive, lockSum)
			if c.spoil != nil {
				c.spoil(t, root, up)
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

// Архив настоящего крейта в репозитории — тот, что записан в Cargo.lock
// апстрима (до правила П3, которое снимает сумму, — см. CORE_VERSION).
func TestCrateArchiveMatchesCoreVersion(t *testing.T) {
	root := filepath.Join("..", "..")
	version := read(t, filepath.Join(root, "core", "CORE_VERSION"))
	for _, c := range vendoredCrates {
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(cratesDir), c.Name+"-*.crate"))
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) != 1 {
			t.Fatalf("архивов %s в %s: %d, нужен один — архив прежней версии удалите", c.Name, cratesDir, len(matches))
		}
		raw, err := os.ReadFile(matches[0])
		if err != nil {
			t.Fatal(err)
		}
		v := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(matches[0]), c.Name+"-"), ".crate")
		if line := "crate    = " + c.Name + " " + v + " sha256:" + sum(raw) + "\n"; !strings.Contains(version, line) {
			t.Errorf("в core/CORE_VERSION нет %q — перенесите ядро заново", strings.TrimSpace(line))
		}
	}
}
