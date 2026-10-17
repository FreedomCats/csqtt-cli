// Команда vendorcore переносит ядро CSQTT из клона апстрима в core/.
//
//	go run ./scripts/vendorcore
//	go run ./scripts/vendorcore -upstream <клон amurcanov/csqtt>
//	go run ./scripts/vendorcore -check
//
// Единственный способ обновить ядро (решение Р2 в doc/architecture.md).
// Наши правки живут правилами в rules.go, поэтому обновление сводится к
// запуску этой команды и просмотру диффа.
//
// Что делает:
//
//  1. Копирует rust-client/ и shared/ апстрима в core/ целиком, кроме файлов
//     из excludedFiles, и лицензию ядра — в core/LICENSE.
//  2. Применяет правила из rules.go.
//  3. Записывает core/CORE_VERSION: коммит апстрима, версию ядра из
//     Cargo.toml и ревизию протокола.
//  4. Удаляет из core/ всё, чего нет в апстриме: core/ целиком принадлежит
//     команде, своих файлов там нет. Не трогает только выход сборки cargo.
//
// Текстовые файлы пишутся в UTF-8 с LF, как они лежат в git апстрима: клон
// на Windows может отдать их с CRLF. -check концы строк тоже не различает.
//
// -upstream — каталог клона апстрима; по умолчанию — csqtt, соседний с этим
// репозиторием. Клон должен быть свежим: апстрим публикует версии снимками,
// и старый клон перенесёт старое ядро.
//
// -check ничего не пишет, только сообщает, разошёлся ли core/ с апстримом и
// правилами: лишний, недостающий или изменённый файл называется по имени,
// код возврата 1.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/FreedomCats/csqtt-cli/scripts/internal/script"
)

const self = "vendorcore"

// upstreamURL попадает в CORE_VERSION.
const upstreamURL = "https://github.com/amurcanov/csqtt"

func main() {
	upstream := flag.String("upstream", "", "каталог клона "+upstreamURL+" (по умолчанию — соседний csqtt)")
	check := flag.Bool("check", false, "ничего не писать, только сообщить о расхождениях (код 1, если они есть)")
	flag.Parse()
	if flag.NArg() > 0 {
		flag.Usage()
		os.Exit(2)
	}

	root, err := script.Root()
	if err != nil {
		script.Fail(self, err)
	}
	code, err := run(root, *upstream, *check)
	if err != nil {
		script.Fail(self, err)
	}
	os.Exit(code)
}

type upstreamVersion struct {
	Commit, Date string
	Dirty        bool
}

func getUpstreamVersion(dir string) (upstreamVersion, error) {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return upstreamVersion{Commit: "unknown", Date: "unknown"}, nil
	}
	git := func(args ...string) (string, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Stderr = os.Stderr
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return strings.TrimSpace(string(out)), nil
	}
	commit, err := git("rev-parse", "HEAD")
	if err != nil {
		return upstreamVersion{}, err
	}
	date, err := git("log", "-1", "--format=%cI")
	if err != nil {
		return upstreamVersion{}, err
	}
	status, err := git(slices.Concat([]string{"status", "--porcelain", "--"}, vendoredDirs, upstreamFileNames())...)
	if err != nil {
		return upstreamVersion{}, err
	}
	return upstreamVersion{Commit: commit, Date: date, Dirty: status != ""}, nil
}

func upstreamFileNames() []string {
	var names []string
	for name := range vendoredFiles {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// isText — текстовый ли файл. Двоичных в ядре нет, но если появятся,
// их нельзя ни править правилами, ни перекодировать концы строк.
func isText(raw []byte) bool { return !bytes.Contains(raw, []byte{0}) }

// normalize — содержимое файла для записи и сравнения: у текста CRLF
// становится LF, двоичный файл остаётся как есть.
func normalize(raw []byte) []byte {
	if !isText(raw) {
		return raw
	}
	return bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
}

func ignored(rel string) bool {
	return slices.ContainsFunc(ignoredPaths, func(p string) bool {
		return rel == p || strings.HasPrefix(rel, p+"/")
	})
}

// listFiles — пути всех файлов под dir относительно base, через «/», без
// ignoredPaths. Отсутствующий dir — пустой список.
func listFiles(base, dir string) ([]string, error) {
	start := filepath.Join(base, dir)
	if _, err := os.Stat(start); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	var files []string
	err := filepath.WalkDir(start, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(base, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if ignored(rel) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		switch {
		case d.IsDir():
		case d.Type().IsRegular():
			files = append(files, rel)
		default:
			return fmt.Errorf("%s: не обычный файл", p)
		}
		return nil
	})
	slices.Sort(files)
	return files, err
}

var (
	cargoVersion     = regexp.MustCompile(`(?m)^version\s*=\s*"([^"]+)"`)
	protocolRevision = regexp.MustCompile(`WIRE_PROTOCOL_REVISION: &str = "([^"]+)"`)
)

// field — первая группа первого совпадения re в файле name плана.
func field(plan map[string][]byte, name string, re *regexp.Regexp) (string, error) {
	m := re.FindSubmatch(plan[name])
	if m == nil {
		return "", fmt.Errorf("в %s нет %s — апстрим изменил формат, поправьте scripts/vendorcore", name, re)
	}
	return string(m[1]), nil
}

func run(root, upstream string, check bool) (int, error) {
	target := filepath.Join(root, "core")
	if upstream == "" {
		upstream = filepath.Join(filepath.Dir(root), "csqtt")
	}
	for _, dir := range vendoredDirs {
		if fi, err := os.Stat(filepath.Join(upstream, dir)); err != nil || !fi.IsDir() {
			return 0, fmt.Errorf("в %s нет каталога %s — укажите клон %s через -upstream", upstream, dir, upstreamURL)
		}
	}

	// --- сборка нового содержимого ----------------------------------------------

	version, err := getUpstreamVersion(upstream)
	if err != nil {
		return 0, err
	}

	var sources []string
	for _, dir := range vendoredDirs {
		files, err := listFiles(upstream, dir)
		if err != nil {
			return 0, err
		}
		sources = append(sources, files...)
	}

	// Исключённый файл, пропавший из апстрима, — повод остановиться: скорее всего
	// его переименовали, и вместе с ним вернулось то, что мы выбрасывали.
	for _, f := range excludedFiles {
		if !slices.Contains(sources, f.Path) {
			return 0, fmt.Errorf("исключённого файла %s нет в апстриме — его переименовали или удалили", f.Path)
		}
	}

	// plan — что должно лежать в core/: путь через «/» → содержимое.
	plan := map[string][]byte{}
	copyFile := func(from, to string) error {
		raw, err := os.ReadFile(filepath.Join(upstream, filepath.FromSlash(from)))
		if err != nil {
			return err
		}
		plan[to] = normalize(raw)
		return nil
	}
	for _, rel := range sources {
		if slices.ContainsFunc(excludedFiles, func(f excludedFile) bool { return f.Path == rel }) {
			continue
		}
		if err := copyFile(rel, rel); err != nil {
			return 0, err
		}
	}
	for _, from := range upstreamFileNames() {
		if err := copyFile(from, vendoredFiles[from]); err != nil {
			return 0, err
		}
	}

	// Счётчик внутри applyRules не сработает, если апстрим переименовал файл:
	// правило тогда не применится ни разу и пропадёт молча.
	for _, r := range rules {
		data, ok := plan[r.File]
		if !ok {
			return 0, fmt.Errorf("правило ссылается на %s, которого нет в апстриме", r.File)
		}
		if !isText(data) {
			return 0, fmt.Errorf("правило ссылается на двоичный файл %s", r.File)
		}
	}
	for name, data := range plan {
		if !isText(data) {
			continue
		}
		text, err := applyRules(name, string(data))
		if err != nil {
			return 0, err
		}
		plan[name] = []byte(text)
	}

	coreVersion, err := field(plan, "rust-client/Cargo.toml", cargoVersion)
	if err != nil {
		return 0, err
	}
	protocol, err := field(plan, "shared/wire_protocol.rs", protocolRevision)
	if err != nil {
		return 0, err
	}
	header := []string{
		"# Версия ядра CSQTT, перенесённого в core/.",
		"# Файл создаётся командой go run ./scripts/vendorcore — руками не править.",
		"",
		"upstream = " + upstreamURL,
		"commit   = " + version.Commit,
		"date     = " + version.Date,
		"version  = " + coreVersion,
		"protocol = " + protocol,
		fmt.Sprintf("files    = %d", len(plan)),
		fmt.Sprintf("rules    = %d", len(rules)),
	}
	if version.Dirty {
		header = append(header, "dirty    = true  # в апстриме есть незакоммиченные правки")
	}
	plan["CORE_VERSION"] = []byte(strings.Join(header, "\n") + "\n")

	names := make([]string, 0, len(plan))
	for name := range plan {
		names = append(names, name)
	}
	slices.Sort(names)

	present, err := listFiles(target, ".")
	if err != nil {
		return 0, err
	}
	var extra []string
	for _, rel := range present {
		if _, ok := plan[rel]; !ok {
			extra = append(extra, rel)
		}
	}

	// --- режим проверки ---------------------------------------------------------

	if check {
		var problems []string
		for _, name := range names {
			current, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(name)))
			if errors.Is(err, fs.ErrNotExist) {
				problems = append(problems, "нет файла: "+name)
				continue
			}
			if err != nil {
				return 0, err
			}
			if !bytes.Equal(normalize(current), plan[name]) {
				problems = append(problems, "расходится: "+name)
			}
		}
		for _, rel := range extra {
			line := "лишний файл: " + rel
			if i := slices.IndexFunc(excludedFiles, func(f excludedFile) bool { return f.Path == rel }); i >= 0 {
				line += " (" + excludedFiles[i].Reason + ")"
			}
			problems = append(problems, line)
		}

		if len(problems) > 0 {
			script.Color(script.Yellow, fmt.Sprintf("core/ разошёлся с апстримом %s и правилами:", version.Commit))
			for _, p := range problems {
				script.Say("  " + p)
			}
			return 1, nil
		}
		script.Color(script.Green, fmt.Sprintf("core/ совпадает с апстримом %s и правилами.", version.Commit))
		return 0, nil
	}

	// --- запись -----------------------------------------------------------------

	for _, name := range names {
		p := filepath.Join(target, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return 0, err
		}
		if err := os.WriteFile(p, plan[name], 0o644); err != nil {
			return 0, err
		}
	}
	for _, rel := range extra {
		if err := os.Remove(filepath.Join(target, filepath.FromSlash(rel))); err != nil {
			return 0, err
		}
		script.Say("  удалён: " + rel)
		// Опустевшие каталоги — следом; непустой os.Remove не снимет.
		for dir := path.Dir(rel); dir != "."; dir = path.Dir(dir) {
			if os.Remove(filepath.Join(target, filepath.FromSlash(dir))) != nil {
				break
			}
		}
	}

	script.Color(script.Green, fmt.Sprintf("Ядро CSQTT %s (%s) перенесено в core/: файлов %d, правил %d.",
		coreVersion, protocol, len(plan)-1, len(rules)))
	script.Sayf("Исключено: %d", len(excludedFiles))
	script.Sayf("Апстрим: %s от %s", version.Commit, version.Date)
	if version.Dirty {
		script.Color(script.Yellow, "ВНИМАНИЕ: в апстриме есть незакоммиченные правки.")
	}
	return 0, nil
}
