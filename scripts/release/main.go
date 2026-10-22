// Команда release собирает релиз csqtt-cli: для каждой платформы — csqtt-cli,
// ядро csqtt-core рядом, zip для Windows и tar.gz для Linux.
//
//	go run ./scripts/release
//	go run ./scripts/release -platforms linux/amd64 -version 0.1.0
//	go run ./scripts/release -core windows/amd64=build/ci/csqtt-core.exe -core linux/arm64=build/ci/csqtt-core
//
// csqtt-cli собирается Go для всех платформ на любой машине:
//
//	go build -trimpath -ldflags "-s -w -X main.version=… -X main.commit=…"
//
// с CGO_ENABLED=0 — cgo в проекте не нужен, а без него кросс-сборка не ищет
// чужой компилятор. Версия и коммит попадают в `csqtt-cli version` через
// переменные cmd/csqtt-cli/main.go.
//
// Ядро на Rust так не собрать: под каждую цель нужен свой компилятор C для
// aws-lc. Поэтому для каждой платформы ядро либо передаётся готовым (-core,
// так делает CI: ядро там собирается по заданию на цель), либо release
// собирает его сам командой `go run ./scripts/buildcore -target <цель>` — под
// эту машину и, на Linux, под Linux другого процессора кросс-сборкой (см.
// buildcore). Ядро под другую систему здесь не собирается. Что можно собрать,
// проверяется до начала работы (buildcore -check): нет ядра хоть для одной
// платформы — ошибка сразу, с подсказкой -core, а не на середине. Готовое
// ядро проверяется по заголовку: ELF или PE того же процессора, что и
// платформа, — ядро arm64 в архив amd64 не попадёт; у ядра Windows ещё и
// таблица импорта: с динамическим CRT (vcruntime*.dll, msvcp*.dll,
// api-ms-win-crt-*) оно не запустится на Windows без VC++ Redistributable,
// такое не принимается.
//
// Раскладка результата (по умолчанию dist/; ядра, собранные здесь, — в
// build/release-core/<os>_<arch>/):
//
//	dist/csqtt-cli_<версия>_windows_amd64/
//	dist/csqtt-cli_<версия>_linux_amd64/
//	dist/csqtt-cli_<версия>_linux_arm64/
//	dist/csqtt-cli_<версия>_windows_amd64.zip
//	    каталог csqtt-cli_<версия>_windows_amd64/ с csqtt-cli.exe, csqtt-core.exe,
//	    wintun.dll, wintun-LICENSE.txt, THIRD_PARTY_NOTICES.txt,
//	    csqtt-cli.example.toml, README.md, LICENSE.txt, LICENSE-core.txt
//	dist/csqtt-cli_<версия>_linux_amd64.tar.gz
//	dist/csqtt-cli_<версия>_linux_arm64.tar.gz
//	    каталог csqtt-cli_<версия>_linux_<arch>/ с csqtt-cli, csqtt-core,
//	    THIRD_PARTY_NOTICES.txt, csqtt-cli.example.toml, README.md,
//	    LICENSE.txt, LICENSE-core.txt
//
// Состав — пакет scripts/internal/dist, общий с distcheck; перед упаковкой
// каталог сверяется с ним. LICENSE.txt — LICENSE из корня, лицензия оболочки
// (MIT); LICENSE-core.txt — core/LICENSE, лицензия ядра.
// wintun.dll (режим -datapath tun) кладётся в zip пакетом
// scripts/internal/wintun — тем же, что у команды wintun: фиксированная
// версия, проверка SHA256 архива и, на Windows, подписи DLL. В репозитории её
// нет. THIRD_PARTY_NOTICES.txt — лицензии всего, что слинковано в csqtt-cli и
// в ядро, — собирает `go run ./scripts/notices` на каждую сборку.
//
// Архивы воспроизводимы: одинаковые входные файлы дают одинаковые байты, на
// какой бы системе и когда бы ни шла сборка. Документы (README.md, пример
// настроек, LICENSE, core/LICENSE) копируются как есть: в клоне с
// autocrlf=true они в CRLF, и архив из такого клона будет другим. Внутри и zip, и tar.gz файлы лежат в
// каталоге с именем архива (распакованное не смешается с соседними файлами),
// в порядке имён, у всех записей одно и то же время (archiveTime), владелец в
// tar.gz — 0:0 без имён. В tar.gz у csqtt-cli и csqtt-core право на
// исполнение (0755), у остальных файлов — 0644, что бы ни стояло на диске; в
// zip у всех 0644. Архив пишется здесь же, а не tar системы.
//
// В конце команда запускает `csqtt-cli version` у бинаря для своей платформы
// и проверяет, что версия и коммит подставились, а ядро своей платформы — с
// заведомо неизвестным ключом: оно должно запуститься и ответить строкой
// использования clap (в сеть оно при этом не ходит).
//
// Флаги:
//
//	-version         строка версии; по умолчанию `git describe --tags --always --dirty`,
//	                 то есть тег, если он есть, иначе короткий коммит; суффикс
//	                 -dirty при незакоммиченных изменениях
//	-out             каталог результата; по умолчанию dist в корне репозитория.
//	                 Содержимое для собираемых платформ перезаписывается, чужие
//	                 файлы не трогаются
//	-platforms       платформы через запятую в виде os/arch; по умолчанию
//	                 windows/amd64,linux/amd64,linux/arm64
//	-core            готовое ядро для платформы: os/arch=путь; можно несколько раз.
//	                 Для платформ без -core ядро собирает buildcore
//	-toolchain       тулчейн rustup для buildcore, например stable-x86_64-pc-windows-msvc
//	-nozip           не собирать архивы — ни zip, ни tar.gz (и не брать wintun.dll):
//	                 в каталогах — только csqtt-cli и csqtt-core
//	-wintun-archive  уже скачанный wintun-<версия>.zip — сборка без сети
package main

import (
	"bytes"
	"debug/elf"
	"debug/pe"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/FreedomCats/csqtt-cli/scripts/internal/dist"
	"github.com/FreedomCats/csqtt-cli/scripts/internal/rusttarget"
	"github.com/FreedomCats/csqtt-cli/scripts/internal/script"
	"github.com/FreedomCats/csqtt-cli/scripts/internal/wintun"
)

const self = "release"

type options struct {
	Version       string
	Out           string
	Platforms     []string
	Cores         map[string]string
	Toolchain     string
	NoZip         bool
	WintunArchive string
}

// coreFlag — -core os/arch=путь, сколько угодно раз.
type coreFlag map[string]string

func (c coreFlag) String() string { return "" }

func (c coreFlag) Set(v string) error {
	platform, path, ok := strings.Cut(v, "=")
	if !ok || platform == "" || path == "" {
		return fmt.Errorf("нужно os/arch=путь: %q", v)
	}
	if _, dup := c[platform]; dup {
		return fmt.Errorf("ядро для %s задано дважды", platform)
	}
	c[platform] = path
	return nil
}

func main() {
	o := options{Cores: map[string]string{}}
	platforms := flag.String("platforms", "windows/amd64,linux/amd64,linux/arm64", "платформы через запятую в виде os/arch")
	flag.StringVar(&o.Version, "version", "", "строка версии (по умолчанию git describe --tags --always --dirty)")
	flag.StringVar(&o.Out, "out", "", "каталог результата (по умолчанию dist в корне репозитория)")
	flag.Var(coreFlag(o.Cores), "core", "готовое ядро для платформы: os/arch=путь (можно несколько раз)")
	flag.StringVar(&o.Toolchain, "toolchain", "", "тулчейн rustup для buildcore")
	flag.BoolVar(&o.NoZip, "nozip", false, "не собирать архивы и не брать wintun.dll")
	flag.StringVar(&o.WintunArchive, "wintun-archive", "", "уже скачанный wintun-"+wintun.Version+".zip — сборка без сети")
	flag.Parse()
	if flag.NArg() > 0 {
		flag.Usage()
		os.Exit(2)
	}
	o.Platforms = strings.Split(*platforms, ",")
	// Абсолютный сразу: notices и buildcore идут с рабочим каталогом в корне
	// репозитория, и относительный путь увёл бы их файлы не туда.
	if o.Out != "" {
		out, err := filepath.Abs(o.Out)
		if err != nil {
			script.Fail(self, err)
		}
		o.Out = out
	}

	root, err := script.Root()
	if err != nil {
		script.Fail(self, err)
	}
	if err := run(root, o); err != nil {
		script.Fail(self, err)
	}
}

type build struct {
	Platform, GOOS, GOARCH string
	// Target — цель Rust ядра.
	Target string
	// Core — готовое ядро (-core или собранное buildcore).
	Core string
	// Built — ядро собирает buildcore.
	Built          bool
	Dir, Name, Exe string
}

// buildEnv — переменные, которые сборка задаёт go build для платформы и
// снимает после: notices, buildcore и прочие запуски go идут без них.
var buildEnv = []string{"GOOS", "GOARCH", "CGO_ENABLED"}

// cleanEnv — окружение без buildEnv.
func cleanEnv() []string {
	return slices.DeleteFunc(os.Environ(), func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		return slices.ContainsFunc(buildEnv, func(v string) bool { return strings.EqualFold(v, name) })
	})
}

// gitOutput — вывод git в корне без пробелов по краям; "" — git завершился
// с ошибкой. Нет самого git — ошибка.
func gitOutput(root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// sanitizeVersion — версия попадает в имена каталогов и файлов: слэши и
// пробелы там не нужны.
func sanitizeVersion(v string) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || unicode.IsSpace(r) {
			return '-'
		}
		return r
	}, v)
}

// plan разбирает платформы и решает, откуда у каждой ядро. Всё, что мешает
// собрать релиз целиком, — одной ошибкой до начала работы. canBuild
// проверяет, соберёт ли buildcore ядро под цель на этой машине.
func plan(root string, o options, canBuild func(target string) error) ([]build, error) {
	var builds []build
	var problems []string
	for _, platform := range o.Platforms {
		goos, goarch, ok := strings.Cut(platform, "/")
		if !ok || strings.Contains(goarch, "/") {
			return nil, fmt.Errorf("платформа должна быть в виде os/arch: '%s'", platform)
		}
		if slices.ContainsFunc(builds, func(b build) bool { return b.Platform == platform }) {
			return nil, fmt.Errorf("платформа %s указана дважды", platform)
		}
		target, err := rusttarget.For(goos, goarch)
		if err != nil {
			return nil, err
		}
		b := build{Platform: platform, GOOS: goos, GOARCH: goarch, Target: target}
		if path, ok := o.Cores[platform]; ok {
			if !filepath.IsAbs(path) {
				if path, err = filepath.Abs(path); err != nil {
					return nil, err
				}
			}
			if err := checkCore(path, goos, goarch); err != nil {
				problems = append(problems, fmt.Sprintf("%s: %v", platform, err))
			}
			b.Core = path
		} else {
			if err := canBuild(target); err != nil {
				problems = append(problems, fmt.Sprintf("%s: %v\n    передайте готовое ядро: -core %s=<путь к %s>", platform, err, platform, dist.Executables(goos)[1]))
			}
			b.Built = true
			b.Core = filepath.Join(root, "build", "release-core", goos+"_"+goarch, dist.Executables(goos)[1])
		}
		builds = append(builds, b)
	}
	for platform := range o.Cores {
		if !slices.ContainsFunc(builds, func(b build) bool { return b.Platform == platform }) {
			problems = append(problems, fmt.Sprintf("-core для %s, а такой платформы нет в -platforms", platform))
		}
	}
	if len(problems) > 0 {
		slices.Sort(problems)
		return nil, fmt.Errorf("ядро не для всех платформ — ничего не собрано:\n  %s", strings.Join(problems, "\n  "))
	}
	return builds, nil
}

// checkCore — path есть и это исполняемый файл платформы: ELF для Linux, PE
// для Windows, того же процессора.
func checkCore(path, goos, goarch string) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	want := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[goarch]
	got := ""
	switch goos {
	case "linux":
		f, err := elf.Open(path)
		if err != nil {
			return fmt.Errorf("%s: не ELF — это не ядро для Linux", path)
		}
		defer f.Close()
		switch {
		case f.Type != elf.ET_EXEC && f.Type != elf.ET_DYN:
			return fmt.Errorf("%s: ELF, но не исполняемый файл (%s)", path, f.Type)
		case f.Machine == elf.EM_X86_64:
			got = "x86_64"
		case f.Machine == elf.EM_AARCH64:
			got = "aarch64"
		default:
			got = f.Machine.String()
		}
	case "windows":
		f, err := pe.Open(path)
		if err != nil {
			return fmt.Errorf("%s: не PE — это не ядро для Windows", path)
		}
		defer f.Close()
		switch f.Machine {
		case pe.IMAGE_FILE_MACHINE_AMD64:
			got = "x86_64"
		case pe.IMAGE_FILE_MACHINE_ARM64:
			got = "aarch64"
		default:
			got = fmt.Sprintf("машина %#x", f.Machine)
		}
		if got == want {
			crt, err := dynamicCRT(f)
			if err != nil {
				return fmt.Errorf("%s: таблица импорта не читается: %w", path, err)
			}
			if len(crt) > 0 {
				return fmt.Errorf("%s: ядро собрано с динамическим CRT (импортирует %s) и без VC++ Redistributable не запустится — нужно +crt-static (go run ./scripts/buildcore)", path, strings.Join(crt, ", "))
			}
		}
	}
	if got != want {
		return fmt.Errorf("%s: ядро для %s, а нужно для %s", path, got, want)
	}
	return nil
}

// dynamicCRT — библиотеки рантайма C/C++ из VC++ Redistributable и UCRT,
// которые импортирует PE. У ядра с +crt-static их нет. ImportedLibraries в
// debug/pe не реализован (всегда пусто), поэтому библиотеки берутся из
// ImportedSymbols — «функция:библиотека».
func dynamicCRT(f *pe.File) ([]string, error) {
	syms, err := f.ImportedSymbols()
	if err != nil {
		return nil, err
	}
	var crt []string
	for _, s := range syms {
		_, dll, _ := strings.Cut(s, ":")
		lower := strings.ToLower(dll)
		if (strings.HasPrefix(lower, "vcruntime") || strings.HasPrefix(lower, "msvcp") || strings.HasPrefix(lower, "api-ms-win-crt-")) &&
			!slices.Contains(crt, dll) {
			crt = append(crt, dll)
		}
	}
	slices.Sort(crt)
	return crt, nil
}

// buildcoreCheck — `buildcore -check`: соберётся ли ядро под target здесь.
// Ошибка — строка самого buildcore.
func buildcoreCheck(root, toolchain string) func(target string) error {
	return func(target string) error {
		args := []string{"-C", root, "run", "./scripts/buildcore", "-check", "-target", target}
		if toolchain != "" {
			args = append(args, "-toolchain", toolchain)
		}
		cmd := exec.Command("go", args...)
		cmd.Env = cleanEnv()
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Run(); err != nil {
			// «exit status 1» в конце дописывает go run — это не строка
			// buildcore.
			lines := slices.DeleteFunc(strings.Split(strings.TrimSpace(out.String()), "\n"), func(l string) bool {
				return goRunExit.MatchString(strings.TrimSpace(l))
			})
			if len(lines) == 0 {
				return fmt.Errorf("buildcore -check завершился %s", exitText(err))
			}
			return errors.New(strings.Join(lines, "\n    "))
		}
		return nil
	}
}

var goRunExit = regexp.MustCompile(`^exit status \d+$`)

func run(root string, o options) error {
	if o.Out == "" {
		o.Out = filepath.Join(root, "dist")
	}

	builds, err := plan(root, o, buildcoreCheck(root, o.Toolchain))
	if err != nil {
		return err
	}

	// --- сведения о сборке ---------------------------------------------------

	version := o.Version
	if version == "" {
		v, err := gitOutput(root, "describe", "--tags", "--always", "--dirty")
		if err != nil {
			return err
		}
		version = v
		if version == "" {
			version = "dev"
		}
	}
	version = sanitizeVersion(version)

	commit, err := gitOutput(root, "rev-parse", "--short", "HEAD")
	if err != nil {
		return err
	}
	if commit == "" {
		commit = "unknown"
	} else {
		status, err := gitOutput(root, "status", "--porcelain")
		if err != nil {
			return err
		}
		if status != "" {
			commit += "-dirty"
		}
	}

	ldflags := fmt.Sprintf("-s -w -X main.version=%s -X main.commit=%s", version, commit)

	script.Sayf("Версия: %s, коммит: %s", version, commit)
	script.Say("Каталог: " + o.Out)

	// Прошлое для всех платформ — до первой сборки: сбой на середине не
	// оставит старых архивов ни у одной.
	for i := range builds {
		b := &builds[i]
		b.Name = fmt.Sprintf("csqtt-cli_%s_%s_%s", version, b.GOOS, b.GOARCH)
		b.Dir = filepath.Join(o.Out, b.Name)
		b.Exe = filepath.Join(b.Dir, dist.Executables(b.GOOS)[0])
		if err := resetPlatform(o.Out, b.Name); err != nil {
			return err
		}
	}

	// --- ядро ----------------------------------------------------------------

	for _, b := range builds {
		if !b.Built {
			script.Sayf("Ядро %s: %s", b.Platform, b.Core)
			continue
		}
		args := []string{"-C", root, "run", "./scripts/buildcore", "-target", b.Target, "-out", filepath.Dir(b.Core)}
		if o.Toolchain != "" {
			args = append(args, "-toolchain", o.Toolchain)
		}
		cmd := exec.Command("go", args...)
		cmd.Env = cleanEnv()
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("ядро для %s не собрано: buildcore завершился %s", b.Platform, exitText(err))
		}
		if err := checkCore(b.Core, b.GOOS, b.GOARCH); err != nil {
			return err
		}
	}

	// --- сборка --------------------------------------------------------------

	if err := os.MkdirAll(o.Out, 0o755); err != nil {
		return err
	}

	for _, b := range builds {
		exes := dist.Executables(b.GOOS)
		if err := os.MkdirAll(b.Dir, 0o755); err != nil {
			return err
		}

		script.Sayf("Сборка %s → %s", b.Platform, b.Exe)
		cmd := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags, "-o", b.Exe, "./cmd/csqtt-cli")
		cmd.Dir = root
		cmd.Env = append(cleanEnv(), "GOOS="+b.GOOS, "GOARCH="+b.GOARCH, "CGO_ENABLED=0")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("go build для %s завершился %s", b.Platform, exitText(err))
		}
		if err := script.CopyFile(b.Core, filepath.Join(b.Dir, exes[1])); err != nil {
			return err
		}
	}

	// --- архивы --------------------------------------------------------------

	var archives []string
	if !o.NoZip {
		for _, b := range builds {
			if err := addDocs(root, b); err != nil {
				return err
			}
			if b.GOOS == "windows" {
				if err := wintun.Fetch(wintun.Options{Arch: b.GOARCH, Dest: b.Dir, Archive: o.WintunArchive}); err != nil {
					return err
				}
			}
			if err := notices(root, b); err != nil {
				return err
			}
			if err := checkComposition(b.Dir, b.GOOS); err != nil {
				return err
			}
			archive := filepath.Join(o.Out, b.Name+".tar.gz")
			write := func(path string) error { return writeTarGz(b.Dir, path, b.Name, dist.Executables(b.GOOS)) }
			// Внутри zip — каталог с именем архива, как в tar.gz: «Извлечь
			// всё» не рассыплет файлы по Загрузкам, а csqtt-cli.exe не потеряет
			// ядро и wintun.dll.
			if b.GOOS == "windows" {
				archive = filepath.Join(o.Out, b.Name+".zip")
				write = func(path string) error { return writeZip(b.Dir, path) }
			}
			if err := removeFile(archive); err != nil {
				return err
			}
			if err := write(archive); err != nil {
				return err
			}
			archives = append(archives, archive)
			script.Say("Архив: " + archive)
		}
	}

	// --- проверка ------------------------------------------------------------

	native := slices.IndexFunc(builds, func(b build) bool { return b.Platform == runtime.GOOS+"/"+runtime.GOARCH })
	if native >= 0 {
		b := builds[native]
		cmd := exec.Command(b.Exe, "version")
		cmd.Stderr = os.Stderr
		out, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("csqtt-cli version завершился %s", exitText(err))
		}
		versionOut := strings.TrimSpace(strings.ReplaceAll(string(out), "\r\n", "\n"))
		lower := strings.ToLower(versionOut)
		if !strings.Contains(lower, strings.ToLower(version)) || !strings.Contains(lower, strings.ToLower(commit)) {
			return fmt.Errorf("csqtt-cli version не показывает версию и коммит: '%s'", versionOut)
		}
		script.Say("Проверка: " + versionOut)
		if err := probeCore(filepath.Join(b.Dir, dist.Executables(b.GOOS)[1])); err != nil {
			return err
		}
		script.Say("Проверка: csqtt-core запускается")
	} else {
		script.Sayf("Проверка csqtt-cli version и csqtt-core пропущена: бинаря для %s/%s среди собранных нет", runtime.GOOS, runtime.GOARCH)
	}

	// --- итог ----------------------------------------------------------------

	script.Say("")
	script.Say("Готово:")
	for _, b := range builds {
		for _, exe := range dist.Executables(b.GOOS) {
			path := filepath.Join(b.Dir, exe)
			size, err := sizeMB(path)
			if err != nil {
				return err
			}
			script.Sayf("  %-14s %s  (%s МБ)", b.Platform, path, size)
		}
	}
	for _, archive := range archives {
		size, err := sizeMB(archive)
		if err != nil {
			return err
		}
		kind := "tar.gz"
		if strings.HasSuffix(archive, ".zip") {
			kind = "zip"
		}
		script.Sayf("  %-14s %s  (%s МБ)", kind, archive, size)
	}
	return nil
}

// probeArg — ключ, которого у ядра нет: clap отвечает строкой использования и
// кодом 2, до разбора остальных аргументов и до сети.
const probeArg = "--csqtt-cli-release-probe"

// probeCore запускает ядро своей платформы с probeArg: оно должно
// запуститься (тот процессор, хватает библиотек) и ответить как clap.
func probeCore(path string) error {
	out, err := exec.Command(path, probeArg).CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 2 || !strings.Contains(string(out), "Usage:") {
		return fmt.Errorf("csqtt-core %s: ждали строку использования и код 2, получили %s: %q", probeArg, exitText(err), strings.TrimSpace(string(out)))
	}
	return nil
}

// addDocs кладёт рядом с бинарями документы из репозитория.
func addDocs(root string, b build) error {
	for dst, src := range map[string]string{
		"csqtt-cli.example.toml": "csqtt-cli.example.toml",
		"README.md":              "README.md",
		"LICENSE.txt":            "LICENSE",
		"LICENSE-core.txt":       filepath.Join("core", "LICENSE"),
	} {
		if err := script.CopyFile(filepath.Join(root, src), filepath.Join(b.Dir, dst)); err != nil {
			return err
		}
	}
	return nil
}

// checkComposition сверяет каталог перед упаковкой с составом архива
// (scripts/internal/dist): лишний или недостающий файл — ошибка здесь, а не
// в distcheck после.
func checkComposition(dir, goos string) error {
	names, err := files(dir)
	if err != nil {
		return err
	}
	want := dist.Files(goos)
	var missing, extra []string
	for _, w := range want {
		if !slices.Contains(names, w) {
			missing = append(missing, w)
		}
	}
	for _, n := range names {
		if !slices.Contains(want, n) {
			extra = append(extra, n)
		}
	}
	if len(missing) > 0 || len(extra) > 0 {
		return fmt.Errorf("%s: состав не тот — нет: %s; лишние: %s", dir, strings.Join(missing, ", "), strings.Join(extra, ", "))
	}
	return nil
}

func notices(root string, b build) error {
	cmd := exec.Command("go", "-C", root, "run", "./scripts/notices", "-dest", b.Dir, "-goos", b.GOOS, "-goarch", b.GOARCH)
	cmd.Env = cleanEnv()
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("уведомления для %s не собраны", b.Platform)
	}
	return nil
}

// exitText — «с кодом N» для завершившейся команды, иначе текст ошибки.
func exitText(err error) string {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return fmt.Sprintf("с кодом %d", exit.ExitCode())
	}
	if err == nil {
		return "успешно"
	}
	return "с ошибкой: " + err.Error()
}

// resetPlatform убирает из out каталог платформы name и оба её архива от
// прошлого запуска: упадёт сборка на середине — в out не останется старого
// архива, который distcheck принял бы за новый.
func resetPlatform(out, name string) error {
	if err := os.RemoveAll(filepath.Join(out, name)); err != nil {
		return err
	}
	for _, ext := range []string{".zip", ".tar.gz"} {
		if err := removeFile(filepath.Join(out, name+ext)); err != nil {
			return err
		}
	}
	return nil
}

func removeFile(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// sizeMB — размер в мегабайтах с одной цифрой после точки, округлённый к
// чётному; целое — без «.0».
func sizeMB(path string) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	mb := math.RoundToEven(float64(fi.Size())/(1<<20)*10) / 10
	return strconv.FormatFloat(mb, 'f', -1, 64), nil
}
