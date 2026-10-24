package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	const redist = "Redistribution and use in source and binary forms, with or without\nmodification, are permitted provided that the following conditions are met: "
	cases := []struct {
		name, text, want string
	}{
		{"apache", "Apache License\n   Version 2.0, January 2004", "Apache-2.0"},
		{"mit", "Permission is hereby granted, free of charge, to any person", "MIT"},
		{"isc", "Permission to use, copy, modify, and/or distribute this software", "ISC"},
		{"isc без or", "Permission to use, copy, modify, and distribute this software", "ISC"},
		{"bsd-4", redist + "3. All advertising materials mentioning features", "BSD-4-Clause"},
		{"bsd-3", redist + "Neither the name of Google Inc. nor the names of its\ncontributors may be used to endorse or promote products", "BSD-3-Clause"},
		{"bsd-2", redist + "2. Redistributions in binary form must reproduce", "BSD-2-Clause"},
		{"неизвестная", "All rights reserved. Do whatever.", ""},
	}
	for _, c := range cases {
		if got := classify(c.text); got != c.want {
			t.Errorf("%s: classify = %q, want %q", c.name, got, c.want)
		}
	}
}

// Основные файлы — первыми; регистр имени не важен (license-mit у
// windows-sys), каталоги, исходники и прочие файлы не берутся.
func TestLicenseFilesMainFirst(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"PATENTS", "LICENSE.md", "NOTICE", "README.md", "license_test.go", "license.rs", "license-mit", "COPYRIGHT", "UNLICENSE"} {
		write(t, dir, name, "x")
	}
	if err := os.Mkdir(filepath.Join(dir, "LICENSES"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := licenseFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range got {
		names = append(names, filepath.Base(p))
	}
	if want := "LICENSE.md UNLICENSE license-mit COPYRIGHT NOTICE PATENTS"; strings.Join(names, " ") != want {
		t.Errorf("licenseFiles = %v, want %s", names, want)
	}
}

func TestParseModules(t *testing.T) {
	list := `{"Standard": true}
{"Module": {"Path": "golang.org/x/sys", "Version": "v0.1.0", "Dir": "/m/sys"}}
{"Module": {"Path": "golang.org/x/sys", "Version": "v0.1.0", "Dir": "/m/sys"}}
{"Module": {"Path": "github.com/tailscale/wf", "Version": "v1", "Dir": "/m/wf",
  "Replace": {"Path": "github.com/FreedomCats/tailscale-wf", "Version": "v2", "Dir": "/m/fork"}}}
{"Module": {"Path": "github.com/FreedomCats/csqtt-cli", "Dir": "/src"}}
{"Module": {"Path": "github.com/BurntSushi/toml", "Version": "v1.6.0", "Dir": "/m/toml"}}
`
	mods, err := parseModules([]byte(list))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range mods {
		got = append(got, m.Path+"@"+m.Version+":"+m.Dir)
	}
	want := []string{
		"github.com/BurntSushi/toml@v1.6.0:/m/toml",
		"github.com/FreedomCats/tailscale-wf@v2:/m/fork",
		"golang.org/x/sys@v0.1.0:/m/sys",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("parseModules:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestCollect(t *testing.T) {
	goroot := t.TempDir()
	write(t, goroot, "LICENSE", "Redistribution and use in source and binary forms ... endorse or promote")
	write(t, goroot, "PATENTS", "grant")

	withLicense := t.TempDir()
	write(t, withLicense, "COPYING", "Permission is hereby granted, free of charge\r\n")
	unknown := t.TempDir()
	write(t, unknown, "LICENSE", "Proprietary")
	missing := t.TempDir()

	sections, err := collect([]module{{Path: "example.com/mit", Version: "v1", Dir: withLicense}}, goroot, "go1.26.2")
	if err != nil {
		t.Fatal(err)
	}
	if len(sections) != 2 || sections[0].Title != "Go 1.26.2 (рантайм и стандартная библиотека)" || sections[0].Kind != "BSD-3-Clause" {
		t.Fatalf("раздел Go: %+v", sections)
	}
	if s := sections[1]; s.Kind != "MIT" || len(s.Files) != 1 || s.Files[0].Name != "COPYING" || s.Files[0].Text != "Permission is hereby granted, free of charge" {
		t.Errorf("модуль с COPYING: %+v", s)
	}

	_, err = collect([]module{
		{Path: "example.com/unknown", Dir: unknown},
		{Path: "example.com/missing", Dir: missing},
	}, goroot, "go1.26.2")
	if err == nil || !strings.Contains(err.Error(), "example.com/unknown: вид лицензии") || !strings.Contains(err.Error(), "example.com/missing: нет файла лицензии") {
		t.Errorf("ошибка должна назвать оба модуля: %v", err)
	}
}

// metadata — cargo metadata в том виде, в каком его разбирает parseCrates.
type metaPkg struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	Version      string           `json:"version"`
	License      *string          `json:"license"`
	ManifestPath string           `json:"manifest_path"`
	Source       *string          `json:"source"`
	Targets      []map[string]any `json:"targets"`
}

func metadata(t *testing.T, root string, pkgs []metaPkg, edges map[string][][2]string) []byte {
	t.Helper()
	type dep struct {
		Pkg      string           `json:"pkg"`
		DepKinds []map[string]any `json:"dep_kinds"`
	}
	type node struct {
		ID   string `json:"id"`
		Deps []dep  `json:"deps"`
	}
	var nodes []node
	for id, ds := range edges {
		n := node{ID: id}
		for _, d := range ds {
			var kind any
			if d[1] != "" {
				kind = d[1]
			}
			n.Deps = append(n.Deps, dep{Pkg: d[0], DepKinds: []map[string]any{{"kind": kind, "target": nil}}})
		}
		nodes = append(nodes, n)
	}
	b, err := json.Marshal(map[string]any{
		"packages": pkgs,
		"resolve":  map[string]any{"root": root, "nodes": nodes},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func str(s string) *string { return &s }

// В список идут только обычные зависимости ядра; dev, build и процедурные
// макросы с их зависимостями — нет. Повтор через два пути — один раз.
func TestParseCrates(t *testing.T) {
	registry := str("registry+https://github.com/rust-lang/crates.io-index")
	lib := []map[string]any{{"kind": []string{"lib"}}}
	pkgs := []metaPkg{
		{ID: "core", Name: "csqtt-client", Version: "2.5.0", ManifestPath: "/r/core/rust-client/Cargo.toml", Targets: []map[string]any{{"kind": []string{"bin"}}}},
		{ID: "a", Name: "a", Version: "1.0.0", License: str("MIT"), ManifestPath: "/reg/a-1.0.0/Cargo.toml", Source: registry, Targets: lib},
		{ID: "b", Name: "B", Version: "0.2.0", License: str("MIT"), ManifestPath: "/reg/b-0.2.0/Cargo.toml", Source: registry, Targets: lib},
		{ID: "macro", Name: "serde_derive", Version: "1.0.0", License: str("MIT"), ManifestPath: "/reg/m/Cargo.toml", Source: registry, Targets: []map[string]any{{"kind": []string{"proc-macro"}}}},
		{ID: "syn", Name: "syn", Version: "2.0.0", License: str("MIT"), ManifestPath: "/reg/syn/Cargo.toml", Source: registry, Targets: lib},
		{ID: "cc", Name: "cc", Version: "1.0.0", License: str("MIT"), ManifestPath: "/reg/cc/Cargo.toml", Source: registry, Targets: lib},
		{ID: "proptest", Name: "proptest", Version: "1.0.0", License: str("MIT"), ManifestPath: "/reg/pt/Cargo.toml", Source: registry, Targets: lib},
		{ID: "vendored", Name: "primp", Version: "1.3.1", License: str("MIT"), ManifestPath: "/r/core/rust-client/vendor/primp/Cargo.toml", Targets: lib},
	}
	meta := metadata(t, "core", pkgs, map[string][][2]string{
		"core":     {{"a", ""}, {"macro", ""}, {"proptest", "dev"}, {"vendored", ""}},
		"a":        {{"b", ""}, {"cc", "build"}},
		"vendored": {{"b", ""}},
		"macro":    {{"syn", ""}},
	})
	crates, err := parseCrates(meta)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range crates {
		got = append(got, c.key()+":"+filepath.ToSlash(c.Dir))
		if c.Name == "primp" != c.Vendored {
			t.Errorf("%s: vendored = %v", c.key(), c.Vendored)
		}
	}
	want := "a@1.0.0:/reg/a-1.0.0 B@0.2.0:/reg/b-0.2.0 primp@1.3.1:/r/core/rust-client/vendor/primp"
	if strings.Join(got, " ") != want {
		t.Errorf("parseCrates = %v, want %s", got, want)
	}

	if _, err := parseCrates([]byte(`{"packages": []}`)); err == nil {
		t.Error("metadata без resolve (--no-deps) должна быть ошибкой")
	}
}

func TestCollectCrates(t *testing.T) {
	root := t.TempDir()
	sysroot := t.TempDir()
	write(t, filepath.Join(sysroot, "share", "doc", "rust", "licenses"), "Apache-2.0.txt", "Apache License\nVersion 2.0")
	reg := t.TempDir()

	dual := filepath.Join(reg, "dual-1.0.0")
	write(t, dual, "LICENSE-MIT", "mit text")
	write(t, dual, "LICENSE-APACHE", "apache text")
	deep := filepath.Join(reg, "deep-0.1.0")
	write(t, filepath.Join(deep, "legal"), "TERMS.txt", "terms")
	donor := filepath.Join(reg, "alloc-no-stdlib-2.0.4")
	write(t, donor, "LICENSE", "Copyright (c) 2016 Dropbox, Inc.")
	orphan := filepath.Join(reg, "alloc-stdlib-0.2.4")
	write(t, orphan, "README.md", "readme")
	vendored := filepath.Join(root, "core", "rust-client", "vendor", "primp")
	write(t, vendored, "Cargo.toml", "")
	write(t, filepath.Join(root, "scripts", "notices", "licenses", "primp-1.3.1"), "LICENSE-MIT", "Copyright (c) someone\r\n\r\nPermission is hereby granted")
	shim := filepath.Join(reg, "defmt-0.3.100")
	write(t, shim, "README.md", "readme")
	shimDonor := filepath.Join(reg, "defmt-1.1.1")
	write(t, shimDonor, "LICENSE-MIT", "Copyright (c) Ferrous Systems")
	write(t, shimDonor, "LICENSE-APACHE", "apache text")
	repoDir := filepath.Join(root, "scripts", "notices", "licenses", "etherparse-0.16.0")
	write(t, repoDir, "LICENSE-MIT", "MIT License\r\n\r\nCopyright (c) 2024 Julian Schmid")
	write(t, repoDir, "LICENSE-APACHE", "apache text")

	sections, err := collectCrates([]crate{
		{Name: "alloc-no-stdlib", Version: "2.0.4", License: "BSD-3-Clause", Dir: donor},
		{Name: "alloc-stdlib", Version: "0.2.4", License: "BSD-3-Clause", Dir: orphan},
		{Name: "deep", Version: "0.1.0", LicenseFile: "legal/TERMS.txt", Dir: deep},
		{Name: "defmt", Version: "0.3.100", License: "MIT OR Apache-2.0", Dir: shim},
		{Name: "defmt", Version: "1.1.1", License: "MIT OR Apache-2.0", Dir: shimDonor},
		{Name: "dual", Version: "1.0.0", License: "MIT OR Apache-2.0", Dir: dual},
		{Name: "etherparse", Version: "0.16.0", License: "MIT OR Apache-2.0", Dir: orphan},
		{Name: "primp", Version: "1.3.1", License: "MIT", Dir: vendored, Vendored: true},
	}, sysroot, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(sections) != 9 || sections[0].Kind != "MIT OR Apache-2.0" || sections[0].Files[0].Name != "Apache-2.0.txt" {
		t.Fatalf("раздел Rust: %+v", sections)
	}
	if s := sections[2]; s.Kind != "BSD-3-Clause" || len(s.Files) != 1 || !strings.Contains(s.Files[0].Text, "Dropbox") || !strings.Contains(s.Note, "alloc-no-stdlib") {
		t.Errorf("alloc-stdlib — файл alloc-no-stdlib с пояснением: %+v", s)
	}
	if s := sections[3]; s.Kind != "см. TERMS.txt" || len(s.Files) != 1 || s.Files[0].Name != "TERMS.txt" {
		t.Errorf("license-file глубже корня: %+v", s)
	}
	// Прослойка defmt 0.3.100 берёт оба файла у тёзки 1.1.1 — донора по
	// имени@версии; сам донор — как обычный crate, без примечания.
	if s := sections[4]; s.Title != "defmt 0.3.100" || len(s.Files) != 2 || s.Files[1].Name != "LICENSE-MIT" || !strings.Contains(s.Files[1].Text, "Ferrous") || !strings.Contains(s.Note, "defmt 1.1.1") {
		t.Errorf("defmt 0.3.100 — файлы defmt 1.1.1 с пояснением: %+v", s)
	}
	if s := sections[5]; s.Title != "defmt 1.1.1" || len(s.Files) != 2 || s.Note != "" {
		t.Errorf("defmt 1.1.1 — обычный crate: %+v", s)
	}
	if s := sections[6]; s.Kind != "MIT OR Apache-2.0" || len(s.Files) != 2 || s.Note != "" {
		t.Errorf("crate с двумя файлами: %+v", s)
	}
	if s := sections[7]; s.Kind != "MIT OR Apache-2.0" || len(s.Files) != 2 || s.Files[0].Name != "LICENSE-MIT" || s.Files[0].Text != "MIT License\n\nCopyright (c) 2024 Julian Schmid" ||
		!strings.Contains(s.Note, "github.com/JulianSchmid/etherparse") || !strings.Contains(s.Note, "тег v0.16.0") || !strings.Contains(s.Note, "LICENSE-MIT и LICENSE-APACHE") {
		t.Errorf("etherparse — файлы из репозитория с пояснением: %+v", s)
	}
	s := sections[8]
	if s.Kind != "MIT" || len(s.Files) != 1 || s.Files[0].Text != "Copyright (c) someone\n\nPermission is hereby granted" ||
		!strings.Contains(s.Note, "core/rust-client/vendor/primp") || !strings.Contains(s.Note, "crates.io") {
		t.Errorf("primp — сохранённый файл с пояснением: %+v", s)
	}
	for _, sec := range sections {
		if strings.Contains(sec.Note, root) || strings.Contains(sec.Note, reg) {
			t.Errorf("%s: путь машины в пояснении: %q", sec.Title, sec.Note)
		}
	}

	// Без файла лицензии, без license в Cargo.toml, с донором другой
	// лицензии — ошибка с именами всех трёх, а не пропуск.
	_, err = collectCrates([]crate{
		{Name: "alloc-no-stdlib", Version: "2.0.4", License: "MIT", Dir: donor},
		{Name: "alloc-stdlib", Version: "0.2.4", License: "BSD-3-Clause", Dir: orphan},
		{Name: "bare", Version: "1.0.0", License: "MIT", Dir: orphan},
		{Name: "nolicense", Version: "1.0.0", Dir: dual},
	}, sysroot, root)
	for _, want := range []string{"alloc-stdlib@0.2.4: лицензию брать у alloc-no-stdlib", "bare@1.0.0: нет файла лицензии", "nolicense@1.0.0: в Cargo.toml нет"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("нет %q в ошибке: %v", want, err)
		}
	}

	// Донор имя@версия — только ровно эта версия: без неё в графе (или с
	// другой лицензией) — ошибка, а не файлы случайного тёзки.
	for _, d := range []crate{
		{Name: "defmt", Version: "1.2.0", License: "MIT OR Apache-2.0", Dir: shimDonor},
		{Name: "defmt", Version: "1.1.1", License: "MIT", Dir: shimDonor},
	} {
		_, err := collectCrates([]crate{{Name: "defmt", Version: "0.3.100", License: "MIT OR Apache-2.0", Dir: shim}, d}, sysroot, root)
		if err == nil || !strings.Contains(err.Error(), "defmt@0.3.100: лицензию брать у defmt@1.1.1") {
			t.Errorf("defmt с донором %s (%s): %v", d.key(), d.License, err)
		}
	}

	// Сохранённый файл — только для той же версии: новая проходит через
	// человека.
	if _, err := collectCrates([]crate{{Name: "primp", Version: "1.3.2", License: "MIT", Dir: vendored, Vendored: true}}, sysroot, root); err == nil {
		t.Error("primp другой версии без файла лицензии прошёл")
	}
	if _, err := collectCrates([]crate{{Name: "etherparse", Version: "0.17.0", License: "MIT OR Apache-2.0", Dir: orphan}}, sysroot, root); err == nil {
		t.Error("etherparse другой версии без файла лицензии прошёл")
	}

	// Crate не из реестра и не из vendor — ошибка.
	if _, err := collectCrates([]crate{{Name: "local", Version: "1.0.0", License: "MIT", Dir: dual, Vendored: true}}, sysroot, root); err == nil || !strings.Contains(err.Error(), "core/rust-client/vendor") {
		t.Errorf("crate по пути вне vendor: %v", err)
	}
}

// Копия LICENSE-MIT primp — та, что в архиве primp 1.3.1 на crates.io
// (sha256 файла оттуда; переводы строк — LF, как в архиве).
func TestStoredPrimpLicense(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("licenses", "primp-1.3.1", "LICENSE-MIT"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(strings.ReplaceAll(string(b), "\r\n", "\n")))
	if got := hex.EncodeToString(sum[:]); got != "47d4e1803702728e03d30f8a848ae2249ba274bbd9f8443e803f7924d83cd371" {
		t.Errorf("licenses/primp-1.3.1/LICENSE-MIT изменён: sha256 %s", got)
	}
	for key, s := range storedLicense {
		name, version, _ := strings.Cut(key, "@")
		for _, f := range s.Files {
			if _, err := os.Stat(filepath.Join("licenses", name+"-"+version, f)); err != nil {
				t.Errorf("%s: %v", key, err)
			}
		}
	}
}

// Копии LICENSE-MIT/LICENSE-APACHE из repoLicense — те, что в репозиториях
// проектов на названных теге или коммите (sha256 файлов оттуда; LF). У
// каждой записи карты файлы на месте, и каждый — та лицензия, что обещана
// именем.
func TestStoredRepoLicenses(t *testing.T) {
	sums := map[string]string{
		"etherparse-0.16.0/LICENSE-MIT":    "4be7e6a9b3e7293807bbebfd081941c2d87fa445733e1ad0a9fc6efbc4f3029a",
		"etherparse-0.16.0/LICENSE-APACHE": "affcd3a3acd2a70d6405c6a5088413da8f97b91f5074ae313fbaa2ec4b45b19f",
		"yasna-0.5.2/LICENSE-MIT":          "2a7df90e60fd5512b8bca35d3ce90e068f21c52d962855053fd1d9e2e7ca0f16",
		"yasna-0.5.2/LICENSE-APACHE":       "a60eea817514531668d7e00765731449fe14d059d3249e0bc93b36de45f759f2",
	}
	kinds := map[string]string{"LICENSE-MIT": "MIT", "LICENSE-APACHE": "Apache-2.0"}
	seen := 0
	for key, r := range repoLicense {
		name, version, _ := strings.Cut(key, "@")
		if r.Repo == "" || r.Rev == "" || len(r.Files) == 0 {
			t.Errorf("%s: в записи нужны репозиторий, тег/коммит и файлы: %+v", key, r)
		}
		for _, f := range r.Files {
			rel := name + "-" + version + "/" + f
			b, err := os.ReadFile(filepath.Join("licenses", filepath.FromSlash(rel)))
			if err != nil {
				t.Errorf("%s: %v", key, err)
				continue
			}
			seen++
			text := strings.ReplaceAll(string(b), "\r\n", "\n")
			sum := sha256.Sum256([]byte(text))
			if got := hex.EncodeToString(sum[:]); got != sums[rel] {
				t.Errorf("licenses/%s изменён или не учтён в тесте: sha256 %s", rel, got)
			}
			if got := classify(text); got != kinds[f] {
				t.Errorf("licenses/%s: вид лицензии %q, по имени файла ждём %q", rel, got, kinds[f])
			}
		}
	}
	if seen != len(sums) {
		t.Errorf("в repoLicense учтено файлов: %d, в тесте: %d", seen, len(sums))
	}
}

func TestRender(t *testing.T) {
	goSections := []section{{Title: "m v1", Kind: "MIT", Files: []licenseText{{Name: "LICENSE", Text: "a\nb"}}}}
	rustSections := []section{{Title: "c 1.0.0", Kind: "MIT OR Apache-2.0", Note: "заметка", Files: []licenseText{{Name: "LICENSE-MIT", Text: "c"}}}}
	win := render(goSections, rustSections, "windows", "amd64", "x86_64-pc-windows-msvc")
	if strings.Count(win, "\n") != strings.Count(win, "\r\n") {
		t.Error("для windows все переводы строк должны быть CRLF")
	}
	for _, want := range []string{"csqtt-cli.exe распространяется под лицензией MIT", "LICENSE.txt рядом", "wintun-LICENSE.txt", "LICENSE-core.txt", "csqtt-core.exe:", "c 1.0.0 — MIT OR Apache-2.0 (csqtt-core.exe)", "m v1 — MIT (csqtt-cli.exe)"} {
		if !strings.Contains(win, want) {
			t.Errorf("для windows нет %q", want)
		}
	}
	linux := render(goSections, rustSections, "linux", "arm64", "aarch64-unknown-linux-gnu")
	if strings.Contains(linux, "\r") || strings.Contains(linux, "wintun") || strings.Contains(linux, ".exe") {
		t.Error("для linux — LF, без wintun и .exe")
	}
	for _, want := range []string{"aarch64-unknown-linux-gnu", "csqtt-cli распространяется под лицензией MIT", "LICENSE.txt рядом", "LICENSE-core.txt"} {
		if !strings.Contains(linux, want) {
			t.Errorf("для linux нет %q", want)
		}
	}
}

// TestRealModule собирает уведомления по настоящему ./cmd/csqtt-cli для
// всех платформ релиза: у каждой зависимости Go должна найтись лицензия.
// Часть ядра (cargo metadata) — только с CSQTT_TEST_NOTICES=1: ей нужны cargo
// и, если исходников crates ещё нет, crates.io, а обычный go test от них
// зависеть не должен.
func TestRealModule(t *testing.T) {
	if testing.Short() {
		t.Skip("go list по всему модулю")
	}
	root, err := moduleRoot()
	if err != nil {
		t.Fatal(err)
	}
	goroot, goversion, err := goEnv()
	if err != nil {
		t.Fatal(err)
	}
	platforms := [][3]string{
		{"windows", "amd64", "x86_64-pc-windows-msvc"},
		{"linux", "amd64", "x86_64-unknown-linux-gnu"},
		{"linux", "arm64", "aarch64-unknown-linux-gnu"},
	}
	for _, p := range platforms {
		mods, err := listModules(root, p[0], p[1])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := collect(mods, goroot, goversion); err != nil {
			t.Fatalf("%s/%s: %v", p[0], p[1], err)
		}
	}

	if os.Getenv("CSQTT_TEST_NOTICES") != "1" {
		t.Skip("crates ядра не проверены: нужен CSQTT_TEST_NOTICES=1 (cargo metadata, возможно — crates.io)")
	}
	sysroot, err := rustSysroot()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range platforms {
		crates, err := listCrates(root, p[2])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := collectCrates(crates, sysroot, root); err != nil {
			t.Fatalf("%s: %v", p[2], err)
		}
	}
}

func write(t *testing.T, dir, name, text string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}
