// Команда notices собирает THIRD_PARTY_NOTICES.txt — лицензии всего, что
// слинковано в csqtt-cli и в ядро csqtt-core.
//
//	go run ./scripts/notices -dest build/out [-goos windows] [-goarch amd64]
//
// Файл — из двух частей, по бинарю на каждую; платформа по умолчанию — эта
// машина.
//
// csqtt-cli. Список модулей Go — то, что реально попадает в бинарь для
// заданной платформы, с учётом replace в go.mod:
//
//	go list -deps -json ./cmd/csqtt-cli   (GOOS, GOARCH, CGO_ENABLED=0)
//
// `go list -m all` для этого не годится: в нём и тестовые зависимости, и
// модули, из которых в бинарь ничего не попало. Сам Go (рантайм и стандартная
// библиотека) в этот список не входит — его LICENSE и PATENTS берутся из
// GOROOT и идут первыми. Вид лицензии модуля определяется по тексту основного
// файла.
//
// csqtt-core. Список crates — те, от которых ядро зависит по Cargo.lock для
// цели Rust этой платформы (scripts/internal/rusttarget):
//
//	cargo metadata --locked --filter-platform <цель>   (core/rust-client)
//
// От ядра — только обычные зависимости: dev-dependencies и build-dependencies
// в бинарь не попадают, процедурные макросы (serde_derive и др.) работают при
// сборке и тоже не попадают — ни они, ни их зависимости. Список — с запасом:
// граф cargo metadata объединяет фичи всех зависимостей, и dev-dependencies
// тоже, поэтому в нём бывают crates, которых в бинаре нет (rustls, quinn,
// ring и др.). Лишнее упоминание безвредно, пропуск — нет. Вид лицензии — поле
// license из Cargo.toml crate; файлы — из каталога его исходников (реестр
// cargo, вендоренные — core/rust-client/vendor). Исходники crates cargo
// скачает сам, если их ещё нет. Стандартная библиотека Rust идёт первой: она
// под MIT или Apache-2.0 на выбор, и кладётся текст Apache-2.0 из поставки
// rustc (у MIT там только шаблон без строки об авторах).
//
// У каждого модуля и crate из корня его каталога берутся файлы LICENSE*,
// LICENCE*, COPYING*, COPYRIGHT*, UNLICENSE*, PATENTS* и NOTICE* (регистр не
// важен) и кладутся целиком. Команда останавливается, если у модуля или crate
// нет файла лицензии (для crates — если случай не разобран ниже, в
// licenseFromCrate и storedLicense), вид лицензии модуля Go не распознан или
// у crate в Cargo.toml не указана лицензия: новая зависимость должна пройти
// через человека, а не тихо уехать в релиз. Текст лицензии там, где его нет в
// исходниках, не придумывается.
//
// Ядро CSQTT само по себе (PolyForm Noncommercial) сюда не входит: его
// лицензия лежит в архиве отдельным файлом LICENSE-core.txt. wintun.dll —
// тоже: её лицензия — wintun-LICENSE.txt (scripts/internal/wintun). В
// уведомлениях — ссылки на оба файла.
//
// Путей машины в файле нет: только имена, версии и тексты лицензий.
// Файл пишется в UTF-8 без BOM; для Windows — с переводами строк CRLF.
// В репозиторий не коммитится, генерируется на каждую сборку.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"

	"github.com/FreedomCats/csqtt-cli/scripts/internal/rusttarget"
)

const self = "github.com/FreedomCats/csqtt-cli"

// licenseFromCrate — crates без файла лицензии в опубликованных исходниках,
// у которых он есть у другого crate из того же репозитория с той же
// лицензией. Ключ — имя@версия: новая версия снова пройдёт через человека.
var licenseFromCrate = map[string]struct{ From, Note string }{
	"alloc-stdlib@0.2.4": {
		From: "alloc-no-stdlib",
		Note: "В опубликованном crate нет файла лицензии. alloc-stdlib и alloc-no-stdlib —\n" +
			"из одного репозитория (github.com/dropbox/rust-alloc-no-stdlib), у них один\n" +
			"автор и одна лицензия (BSD-3-Clause); ниже — файл LICENSE этого репозитория\n" +
			"из исходников alloc-no-stdlib.",
	},
}

// storedLicense — crates, чей файл лицензии потерялся при вендоринге: его
// копия лежит в scripts/notices/licenses/<имя>-<версия>/, взятая без
// изменений из архива той же версии на crates.io. Ключ — имя@версия.
var storedLicense = map[string]struct{ Files []string }{
	// Апстрим CSQTT вендорит primp (core/rust-client/vendor/primp) без
	// LICENSE-MIT, хотя в опубликованном primp 1.3.1 он есть. Строка об
	// авторах в нём — от reqwest, из которого primp вырос: придумать её по
	// полю authors было бы неверно.
	"primp@1.3.1": {Files: []string{"LICENSE-MIT"}},
}

func main() {
	dest := flag.String("dest", "", "каталог, куда положить THIRD_PARTY_NOTICES.txt (обязателен)")
	goos := flag.String("goos", runtime.GOOS, "платформа, для которой собирается список")
	goarch := flag.String("goarch", runtime.GOARCH, "архитектура")
	flag.Parse()
	if *dest == "" || flag.NArg() > 0 {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*dest, *goos, *goarch); err != nil {
		fmt.Fprintln(os.Stderr, "notices:", err)
		os.Exit(1)
	}
}

func run(dest, goos, goarch string) error {
	target, err := rusttarget.For(goos, goarch)
	if err != nil {
		return err
	}
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	goroot, goversion, err := goEnv()
	if err != nil {
		return err
	}
	mods, err := listModules(root, goos, goarch)
	if err != nil {
		return err
	}
	goSections, err := collect(mods, goroot, goversion)
	if err != nil {
		return err
	}
	sysroot, err := rustSysroot()
	if err != nil {
		return err
	}
	crates, err := listCrates(root, target)
	if err != nil {
		return err
	}
	rustSections, err := collectCrates(crates, sysroot, root)
	if err != nil {
		return err
	}
	text := render(goSections, rustSections, goos, goarch, target)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	out := filepath.Join(dest, "THIRD_PARTY_NOTICES.txt")
	if err := os.WriteFile(out, []byte(text), 0o644); err != nil {
		return err
	}
	fmt.Printf("Уведомления: %s (%d компонентов Go, %d — ядра)\n", out, len(goSections), len(rustSections))
	return nil
}

// --- модули Go -------------------------------------------------------------

type module struct {
	Path    string
	Version string
	Dir     string
	Replace *module
}

func moduleRoot() (string, error) {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "", fmt.Errorf("go env GOMOD: %w", err)
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == os.DevNull {
		return "", errors.New("запускать из модуля csqtt-cli")
	}
	return filepath.Dir(gomod), nil
}

func goEnv() (goroot, goversion string, err error) {
	out, err := exec.Command("go", "env", "GOROOT", "GOVERSION").Output()
	if err != nil {
		return "", "", fmt.Errorf("go env: %w", err)
	}
	// По строкам, а не по пробелам: GOROOT бывает «C:\Program Files\Go».
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(string(out), "\r\n", "\n")), "\n")
	if len(lines) != 2 {
		return "", "", fmt.Errorf("go env: неожиданный вывод %q", out)
	}
	return lines[0], lines[1], nil
}

// listModules — модули, из которых в ./cmd/csqtt-cli для goos/goarch попал
// хоть один пакет; replace уже применён, сам проект исключён.
func listModules(root, goos, goarch string) ([]module, error) {
	cmd := exec.Command("go", "list", "-deps", "-json=Standard,Module", "./cmd/csqtt-cli")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w", err)
	}
	return parseModules(out)
}

func parseModules(listJSON []byte) ([]module, error) {
	seen := map[string]module{}
	dec := json.NewDecoder(bytes.NewReader(listJSON))
	for {
		var p struct {
			Standard bool
			Module   *module
		}
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("разбор go list: %w", err)
		}
		if p.Standard || p.Module == nil {
			continue
		}
		m := *p.Module
		if m.Replace != nil {
			m = *m.Replace
		}
		if m.Path == self {
			continue
		}
		seen[m.Path] = module{Path: m.Path, Version: m.Version, Dir: m.Dir}
	}
	mods := make([]module, 0, len(seen))
	for _, m := range seen {
		mods = append(mods, m)
	}
	sort.Slice(mods, func(i, j int) bool {
		return strings.ToLower(mods[i].Path) < strings.ToLower(mods[j].Path)
	})
	return mods, nil
}

// --- crates ядра -----------------------------------------------------------

type crate struct {
	Name, Version string
	// License — поле license из Cargo.toml, LicenseFile — license-file
	// (относительно Dir).
	License, LicenseFile string
	// Dir — каталог исходников crate.
	Dir string
	// Vendored — crate из core/rust-client/vendor, а не из реестра.
	Vendored bool
}

func (c crate) key() string { return c.Name + "@" + c.Version }

func rustSysroot() (string, error) {
	out, err := exec.Command("rustc", "--print", "sysroot").Output()
	if err != nil {
		return "", fmt.Errorf("rustc --print sysroot (%w) — поставьте Rust: https://rustup.rs", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// listCrates — crates, от которых csqtt-core зависит для цели target (с
// запасом, см. описание команды).
func listCrates(root, target string) ([]crate, error) {
	cmd := exec.Command("cargo", "metadata", "--locked", "--format-version", "1", "--filter-platform", target,
		"--manifest-path", filepath.Join(root, "core", "rust-client", "Cargo.toml"))
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("cargo metadata: %w", err)
	}
	return parseCrates(out)
}

// parseCrates обходит граф cargo metadata от ядра по обычным зависимостям.
// Процедурный макрос не входит в бинарь, и обход за него не идёт: его
// зависимости работают только при сборке.
func parseCrates(meta []byte) ([]crate, error) {
	var m struct {
		Packages []struct {
			ID, Name, Version string
			License           *string
			LicenseFile       *string `json:"license_file"`
			ManifestPath      string  `json:"manifest_path"`
			Source            *string
			Targets           []struct{ Kind []string }
		}
		Resolve *struct {
			Root  *string
			Nodes []struct {
				ID   string
				Deps []struct {
					Pkg      string
					DepKinds []struct{ Kind *string } `json:"dep_kinds"`
				}
			}
		}
	}
	if err := json.Unmarshal(meta, &m); err != nil {
		return nil, fmt.Errorf("разбор cargo metadata: %w", err)
	}
	if m.Resolve == nil || m.Resolve.Root == nil {
		return nil, errors.New("в cargo metadata нет графа зависимостей ядра")
	}
	type pkg = struct {
		crate
		procMacro bool
	}
	pkgs := map[string]pkg{}
	for _, p := range m.Packages {
		c := crate{Name: p.Name, Version: p.Version, Dir: filepath.Dir(p.ManifestPath), Vendored: p.Source == nil}
		if p.License != nil {
			c.License = *p.License
		}
		if p.LicenseFile != nil {
			c.LicenseFile = *p.LicenseFile
		}
		macro := slices.ContainsFunc(p.Targets, func(t struct{ Kind []string }) bool { return slices.Contains(t.Kind, "proc-macro") })
		pkgs[p.ID] = pkg{c, macro}
	}
	deps := map[string][]string{}
	for _, n := range m.Resolve.Nodes {
		for _, d := range n.Deps {
			if slices.ContainsFunc(d.DepKinds, func(k struct{ Kind *string }) bool { return k.Kind == nil }) {
				deps[n.ID] = append(deps[n.ID], d.Pkg)
			}
		}
	}

	root := *m.Resolve.Root
	seen := map[string]bool{root: true}
	queue := []string{root}
	var crates []crate
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, d := range deps[id] {
			if seen[d] {
				continue
			}
			seen[d] = true
			p, ok := pkgs[d]
			if !ok {
				return nil, fmt.Errorf("cargo metadata: нет пакета %s", d)
			}
			if p.procMacro {
				continue
			}
			crates = append(crates, p.crate)
			queue = append(queue, d)
		}
	}
	sort.Slice(crates, func(i, j int) bool {
		if a, b := strings.ToLower(crates[i].Name), strings.ToLower(crates[j].Name); a != b {
			return a < b
		}
		return crates[i].Version < crates[j].Version
	})
	return crates, nil
}

// --- лицензии --------------------------------------------------------------

var (
	reLicenseFile = regexp.MustCompile(`(?i)^(?:LICEN[CS]E|COPYING|COPYRIGHT|UNLICENSE|PATENTS|NOTICE)`)
	reMainFile    = regexp.MustCompile(`(?i)^(?:LICEN[CS]E|COPYING|UNLICENSE)`)
	reSpace       = regexp.MustCompile(`\s+`)
	reISC         = regexp.MustCompile(`Permission to use, copy, modify, and(?:/or)? distribute`)
)

// licenseFiles — файлы лицензии из корня dir, основные (LICENSE, COPYING)
// первыми: по первому определяется вид лицензии модуля Go. Регистр имени не
// важен (license-mit у windows-sys), но исходники (license_test.go) — не
// лицензия.
func licenseFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var main, rest []string
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() || !reLicenseFile.MatchString(name) || slices.Contains([]string{".go", ".rs"}, strings.ToLower(filepath.Ext(name))) {
			continue
		}
		if reMainFile.MatchString(name) {
			main = append(main, filepath.Join(dir, name))
		} else {
			rest = append(rest, filepath.Join(dir, name))
		}
	}
	return append(main, rest...), nil
}

// classify — вид лицензии по тексту или "", если он не распознан.
func classify(text string) string {
	t := reSpace.ReplaceAllString(text, " ")
	has := func(s string) bool { return strings.Contains(t, s) }
	switch {
	case has("Apache License") && has("Version 2.0"):
		return "Apache-2.0"
	case has("Permission is hereby granted, free of charge"):
		return "MIT"
	case reISC.MatchString(t):
		return "ISC"
	case has("Redistribution and use in source and binary forms"):
		switch {
		case has("advertising materials"):
			return "BSD-4-Clause"
		case has("endorse or promote"):
			return "BSD-3-Clause"
		default:
			return "BSD-2-Clause"
		}
	}
	return ""
}

func readText(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(strings.ReplaceAll(string(b), "\r\n", "\n"), " \t\n"), nil
}

// --- разделы ---------------------------------------------------------------

type section struct {
	Title string
	Kind  string
	Note  string
	Files []licenseText
}

type licenseText struct {
	Name string
	Text string
}

// readAll читает файлы; в разделе — только имена, без каталогов машины.
func readAll(paths []string) ([]licenseText, error) {
	files := make([]licenseText, 0, len(paths))
	for _, p := range paths {
		text, err := readText(p)
		if err != nil {
			return nil, err
		}
		files = append(files, licenseText{Name: filepath.Base(p), Text: text})
	}
	return files, nil
}

func collect(mods []module, goroot, goversion string) ([]section, error) {
	goFiles, err := readAll([]string{filepath.Join(goroot, "LICENSE"), filepath.Join(goroot, "PATENTS")})
	if err != nil {
		return nil, fmt.Errorf("лицензия Go: %w", err)
	}
	goKind := classify(goFiles[0].Text)
	sections := []section{{
		Title: "Go " + strings.TrimPrefix(goversion, "go") + " (рантайм и стандартная библиотека)",
		Kind:  goKind,
		Files: goFiles,
	}}

	var problems []string
	for _, m := range mods {
		s := section{Title: m.Path + " " + m.Version}
		paths, err := licenseFiles(m.Dir)
		if err == nil {
			s.Files, err = readAll(paths)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", m.Path, err)
		}
		if len(s.Files) == 0 {
			problems = append(problems, fmt.Sprintf("%s: нет файла лицензии в %s", m.Path, m.Dir))
			continue
		}
		if s.Kind = classify(s.Files[0].Text); s.Kind == "" {
			problems = append(problems, fmt.Sprintf("%s: вид лицензии в %s не распознан", m.Path, s.Files[0].Name))
		}
		sections = append(sections, s)
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("уведомления не собраны — разберите вручную:\n  %s", strings.Join(problems, "\n  "))
	}
	return sections, nil
}

// crateFiles — файлы лицензии crate: из корня его исходников и license-file
// из Cargo.toml, если он лежит глубже.
func crateFiles(c crate) ([]licenseText, error) {
	paths, err := licenseFiles(c.Dir)
	if err != nil {
		return nil, err
	}
	if c.LicenseFile != "" {
		lf := filepath.Join(c.Dir, filepath.FromSlash(c.LicenseFile))
		if !slices.Contains(paths, lf) {
			paths = append(paths, lf)
		}
	}
	return readAll(paths)
}

// collectCrates — разделы ядра: стандартная библиотека Rust из sysroot, затем
// crates. root — корень репозитория: там лежат storedLicense и vendor.
func collectCrates(crates []crate, sysroot, root string) ([]section, error) {
	std, err := readAll([]string{filepath.Join(sysroot, "share", "doc", "rust", "licenses", "Apache-2.0.txt")})
	if err != nil {
		return nil, fmt.Errorf("лицензия стандартной библиотеки Rust: %w", err)
	}
	sections := []section{{
		Title: "Rust (стандартная библиотека)",
		Kind:  "MIT OR Apache-2.0",
		Note: "Стандартная библиотека Rust распространяется под MIT или Apache-2.0 на\n" +
			"выбор; ниже — текст Apache-2.0 из поставки rustc. Авторские права — у\n" +
			"авторов Rust (https://www.rust-lang.org); сведения о стороннем коде в ней —\n" +
			"в файле COPYRIGHT-library.html поставки rustc.",
		Files: std,
	}}

	byName := map[string][]crate{}
	for _, c := range crates {
		byName[c.Name] = append(byName[c.Name], c)
	}
	vendor := filepath.Join(root, "core", "rust-client", "vendor")

	var problems []string
	for _, c := range crates {
		s := section{Title: c.Name + " " + c.Version, Kind: c.License}
		if c.Vendored {
			rel, err := filepath.Rel(vendor, c.Dir)
			if err != nil || strings.HasPrefix(rel, "..") {
				problems = append(problems, fmt.Sprintf("%s: crate не из реестра и не из core/rust-client/vendor: %s", c.key(), c.Dir))
				continue
			}
			s.Note = joinNotes(s.Note, "Исходники вендорены вместе с ядром: core/rust-client/vendor/"+filepath.ToSlash(rel)+".")
		}
		if s.Kind == "" && c.LicenseFile != "" {
			s.Kind = "см. " + filepath.Base(c.LicenseFile)
		}
		if s.Kind == "" {
			problems = append(problems, fmt.Sprintf("%s: в Cargo.toml нет ни license, ни license-file", c.key()))
			continue
		}
		files, err := crateFiles(c)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", c.key(), err)
		}
		s.Files = files

		from, fromOK := licenseFromCrate[c.key()]
		stored, storedOK := storedLicense[c.key()]
		switch {
		case len(s.Files) > 0:
		case fromOK:
			donors := byName[from.From]
			if len(donors) != 1 || donors[0].License != c.License {
				problems = append(problems, fmt.Sprintf("%s: лицензию брать у %s, но такого crate с той же лицензией в ядре нет (или их несколько)", c.key(), from.From))
				continue
			}
			if s.Files, err = crateFiles(donors[0]); err != nil {
				return nil, fmt.Errorf("%s: %w", from.From, err)
			}
			if len(s.Files) == 0 {
				problems = append(problems, fmt.Sprintf("%s: у %s тоже нет файла лицензии", c.key(), from.From))
				continue
			}
			s.Note = joinNotes(s.Note, from.Note)
		case storedOK:
			dir := filepath.Join(root, "scripts", "notices", "licenses", c.Name+"-"+c.Version)
			paths := make([]string, len(stored.Files))
			for i, f := range stored.Files {
				paths[i] = filepath.Join(dir, f)
			}
			if s.Files, err = readAll(paths); err != nil {
				return nil, fmt.Errorf("%s: %w", c.key(), err)
			}
			s.Note = joinNotes(s.Note, fmt.Sprintf("В исходниках crate нет файла лицензии, в Cargo.toml указано license = %q.\n"+
				"Ниже — файл из опубликованного архива %s %s на crates.io, без изменений.", c.License, c.Name, c.Version))
		default:
			problems = append(problems, fmt.Sprintf("%s: нет файла лицензии в %s", c.key(), c.Dir))
			continue
		}
		sections = append(sections, s)
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("уведомления ядра не собраны — разберите вручную:\n  %s", strings.Join(problems, "\n  "))
	}
	return sections, nil
}

func joinNotes(a, b string) string {
	if a == "" {
		return b
	}
	return a + "\n\n" + b
}

// --- текст -----------------------------------------------------------------

func render(goSections, rustSections []section, goos, goarch, target string) string {
	var b strings.Builder
	line := func(s string) { b.WriteString(s + "\n") }
	rule := strings.Repeat("=", 79)
	exe := ""
	if goos == "windows" {
		exe = ".exe"
	}

	line("Сторонние компоненты csqtt-cli")
	line("==============================")
	line("")
	line(fmt.Sprintf("Сборка %s/%s. В ней два исполняемых файла: csqtt-cli%s на Go", goos, goarch, exe))
	line(fmt.Sprintf("и csqtt-core%s — ядро CSQTT на Rust (цель %s).", exe, target))
	line("Ниже для каждого — компоненты, от которых он зависит, вид лицензии и")
	line("полный текст её файлов из исходников компонента. Список crates ядра —")
	line("по Cargo.lock для этой цели и с запасом: в нём могут быть и crates, код")
	line("которых в csqtt-core не вошёл.")
	line("")
	line("Ядро CSQTT распространяется под PolyForm Noncommercial 1.0.0; текст и")
	line("обязательные уведомления его автора — в файле LICENSE-core.txt рядом.")
	line("Поэтому сборка целиком — некоммерческая.")
	if goos == "windows" {
		line("")
		line("wintun.dll — отдельный бинарь от WireGuard LLC, распространяется")
		line("без изменений; его лицензия — в файле wintun-LICENSE.txt рядом.")
	}
	line("")
	line("Содержание:")

	parts := []struct {
		name     string
		sections []section
	}{{"csqtt-cli" + exe, goSections}, {"csqtt-core" + exe, rustSections}}
	width := 0
	for _, s := range slices.Concat(goSections, rustSections) {
		width = max(width, len([]rune(s.Title)))
	}
	for _, part := range parts {
		line("")
		line(part.name + ":")
		for _, s := range part.sections {
			pad := strings.Repeat(" ", width-len([]rune(s.Title)))
			line("  " + s.Title + pad + "  " + s.Kind)
		}
	}
	for _, part := range parts {
		for _, s := range part.sections {
			line("")
			line("")
			line(rule)
			line(s.Title + " — " + s.Kind + " (" + part.name + ")")
			line(rule)
			if s.Note != "" {
				line("")
				line(s.Note)
			}
			for _, f := range s.Files {
				line("")
				line("--- " + f.Name + " ---")
				line("")
				line(f.Text)
			}
		}
	}

	text := b.String()
	if goos == "windows" {
		text = strings.ReplaceAll(text, "\n", "\r\n")
	}
	return text
}
