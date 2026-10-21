// Команда buildcore собирает ядро CSQTT из core/ в build/out/csqtt-core[.exe].
//
//	go run ./scripts/buildcore
//	go run ./scripts/buildcore -target aarch64-unknown-linux-gnu -out build/out/linux-arm64
//	go run ./scripts/buildcore -toolchain stable-x86_64-pc-windows-msvc
//	go run ./scripts/buildcore -test
//	go run ./scripts/buildcore -check -target aarch64-unknown-linux-gnu
//
// Обёртка над cargo build --release --locked в core/rust-client. Что добавляет
// к голому cargo:
//
//   - цель по умолчанию — для этой машины: x86_64-pc-windows-msvc,
//     x86_64-unknown-linux-gnu или aarch64-unknown-linux-gnu (пакет
//     scripts/internal/rusttarget, общий с release и notices);
//   - до сборки — понятная строка, если rustc старше rust-version ядра, цель
//     не установлена или для кросс-сборки нет gcc, вместо ошибки cargo на
//     середине. -check — только эти проверки, без сборки: release зовёт его
//     для всех платформ до начала работы;
//   - кросс-сборка под другой процессор — только Linux gnu на Linux
//     (aarch64 на x86_64 и наоборот): линкер и компилятор C для цели —
//     кросс-gcc Debian и Ubuntu (aarch64-linux-gnu-gcc из пакета
//     gcc-aarch64-linux-gnu). Свой тулчейн задают переменными cargo и cc:
//     CARGO_TARGET_<ЦЕЛЬ>_LINKER, CC_<цель>, AR_<цель>. Ядро под другую
//     систему (Windows на Linux и наоборот) здесь не собирается: aws-lc-sys
//     нужен компилятор C этой системы;
//   - каталог сборки — build/cargo, вне core/: vendorcore -check его не видит;
//   - пути этой машины не попадают в бинарь: каталог cargo и репозиторий
//     переписываются в /cargo и /csqtt-cli (--remap-path-prefix), а cl.exe
//     обрезает их в __FILE__ C-исходников aws-lc (/d1trimfile). Иначе бинарь
//     нёс бы домашний каталог с именем пользователя;
//   - ядро Windows (msvc) — со статическим рантаймом C (+crt-static): ему не
//     нужен VC++ Redistributable;
//   - бинарь client копируется под именем csqtt-core в -out (относительно
//     корня репозитория или абсолютный путь).
//
// -test вместо сборки гоняет cargo test ядра — только под свою машину. Тесты,
// которым нужен живой сервер, в ядре помечены #[ignore] и не запускаются.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/FreedomCats/csqtt-cli/scripts/internal/rusttarget"
	"github.com/FreedomCats/csqtt-cli/scripts/internal/script"
)

const self = "buildcore"

func main() {
	target := flag.String("target", "", "цель Rust (по умолчанию — для этой машины)")
	toolchain := flag.String("toolchain", "", "тулчейн rustup, например stable-x86_64-pc-windows-msvc (по умолчанию — активный)")
	out := flag.String("out", filepath.Join("build", "out"), "куда положить csqtt-core: относительно корня репозитория или абсолютный путь")
	test := flag.Bool("test", false, "не собирать, а прогнать cargo test ядра")
	check := flag.Bool("check", false, "только проверить, что ядро для цели здесь собирается")
	flag.Parse()
	if flag.NArg() > 0 {
		flag.Usage()
		os.Exit(2)
	}

	root, err := script.Root()
	if err != nil {
		script.Fail(self, err)
	}
	if *target == "" {
		if *target, err = defaultTarget(runtime.GOOS, runtime.GOARCH); err != nil {
			script.Fail(self, err)
		}
	}
	if *test && *check {
		script.Fail(self, errors.New("-test и -check вместе не задаются"))
	}
	if err := run(root, *target, *toolchain, *out, *test, *check); err != nil {
		script.Fail(self, err)
	}
}

// defaultTarget — цель Rust для машины goos/goarch.
func defaultTarget(goos, goarch string) (string, error) {
	t, err := rusttarget.For(goos, goarch)
	if err != nil {
		return "", fmt.Errorf("%w — укажите -target", err)
	}
	return t, nil
}

// crossPrefixes — префикс кросс-gcc для цели Linux gnu, как у пакетов Debian
// и Ubuntu: gcc-aarch64-linux-gnu ставит aarch64-linux-gnu-gcc.
var crossPrefixes = map[string]string{
	"aarch64-unknown-linux-gnu": "aarch64-linux-gnu-",
	"x86_64-unknown-linux-gnu":  "x86_64-linux-gnu-",
}

// native — цель для той же системы и того же процессора, что host: линкер и
// компилятор C cargo и cc найдут сами, как без кросс-сборки.
func native(host, target string) bool {
	arch := func(t string) string { a, _, _ := strings.Cut(t, "-"); return a }
	system := func(t string) string {
		for _, s := range []string{"-linux-", "-windows-", "-darwin"} {
			if strings.Contains(t, s) {
				return s
			}
		}
		return t
	}
	return arch(host) == arch(target) && system(host) == system(target)
}

// envName — суффикс переменных cc для цели: CC_aarch64_unknown_linux_gnu.
func envName(target string) string { return strings.ReplaceAll(target, "-", "_") }

// crossEnv — окружение cargo для сборки под target на машине с целью host и
// инструменты, которые для этого должны быть в системе. Под свою систему и
// процессор — ничего. Иначе — только Linux gnu на Linux gnu: линкер и
// компилятор C для aws-lc и ring — кросс-gcc, без него rustc позвал бы cc
// своей машины. Заданное в окружении (getenv) не трогается и проверяется
// как есть: так подставляют свой кросс-тулчейн.
func crossEnv(host, target string, getenv func(string) string) (env, tools []string, err error) {
	if native(host, target) {
		return nil, nil, nil
	}
	prefix, ok := crossPrefixes[target]
	if !ok || !strings.HasSuffix(host, "-linux-gnu") {
		if host == "" {
			host = runtime.GOOS + "/" + runtime.GOARCH
		}
		return nil, nil, fmt.Errorf("ядро под %s на этой машине (%s) не собирается: кросс-сборка — только Linux gnu на Linux (aarch64 и x86_64); соберите его на машине с этой системой", target, host)
	}
	for _, v := range []struct{ name, tool string }{
		{"CARGO_TARGET_" + strings.ToUpper(envName(target)) + "_LINKER", "gcc"},
		{"CC_" + envName(target), "gcc"},
		{"AR_" + envName(target), "ar"},
	} {
		value := getenv(v.name)
		if value == "" {
			value = prefix + v.tool
			env = append(env, v.name+"="+value)
		}
		if !slices.Contains(tools, value) {
			tools = append(tools, value)
		}
	}
	return env, tools, nil
}

// checkTools — понятная строка до сборки, если кросс-gcc нет.
func checkTools(target string, tools []string) error {
	var missing []string
	for _, t := range tools {
		if _, err := exec.LookPath(t); err != nil {
			missing = append(missing, t)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	pkg := "gcc-" + strings.ReplaceAll(strings.TrimSuffix(crossPrefixes[target], "-"), "_", "-")
	return fmt.Errorf("для кросс-сборки под %s нет %s: поставьте %s (Debian, Ubuntu) или задайте свои CARGO_TARGET_%s_LINKER, CC_%s и AR_%s",
		target, strings.Join(missing, ", "), pkg, strings.ToUpper(envName(target)), envName(target), envName(target))
}

// exeName — имя бинаря ядра для цели: client.exe → csqtt-core.exe.
func exeName(target, base string) string {
	if strings.Contains(target, "-windows-") {
		return base + ".exe"
	}
	return base
}

var (
	rustVersionField = regexp.MustCompile(`(?m)^rust-version\s*=\s*"([^"]+)"`)
	rustcVersionLine = regexp.MustCompile(`^rustc (\d+\.\d+\.\d+)`)
)

// version — номер вида 1.97.1 тремя числами; недостающие — нули.
func version(s string) ([3]int, error) {
	var v [3]int
	parts := strings.Split(s, ".")
	if len(parts) > 3 {
		return v, fmt.Errorf("версия %q", s)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return v, fmt.Errorf("версия %q", s)
		}
		v[i] = n
	}
	return v, nil
}

// checkRustc сверяет вывод rustc --version с rust-version из Cargo.toml ядра.
func checkRustc(rustcOut, manifest string) error {
	m := rustVersionField.FindStringSubmatch(manifest)
	if m == nil {
		return errors.New("в core/rust-client/Cargo.toml нет rust-version")
	}
	need, err := version(m[1])
	if err != nil {
		return err
	}
	r := rustcVersionLine.FindStringSubmatch(strings.TrimSpace(rustcOut))
	if r == nil {
		return fmt.Errorf("не разобрать вывод rustc --version: %q", strings.TrimSpace(rustcOut))
	}
	have, err := version(r[1])
	if err != nil {
		return err
	}
	for i := range have {
		if have[i] != need[i] {
			if have[i] < need[i] {
				return fmt.Errorf("rustc %s старше, чем нужно ядру (rust-version = %s): обновите его — rustup update", r[1], m[1])
			}
			break
		}
	}
	return nil
}

// cargoHome — каталог cargo, как его находит сам cargo.
func cargoHome() (string, error) {
	if h := os.Getenv("CARGO_HOME"); h != "" {
		return filepath.Abs(h)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cargo"), nil
}

// buildEnv — переменные окружения cargo, убирающие пути машины из бинаря, а
// для Windows — ещё и зависимость от VC++ Redistributable.
//
// CARGO_ENCODED_RUSTFLAGS, а не RUSTFLAGS: флаги разделены 0x1f, и пробел в
// пути их не разрежет. Из двух подходящих префиксов rustc берёт последний,
// поэтому вложенный каталог идёт вторым: обычно это репозиторий, но
// CARGO_HOME может лежать и внутри него (так бывает в CI).
//
// Для *-windows-msvc — +crt-static: рантайм C (vcruntime, UCRT) вшивается в
// csqtt-core.exe, и ядро из архива запускается на чистой Windows без VC++
// Redistributable. C-код aws-lc, ring и zstd собирается с /MT тем же
// флагом: cargo передаёт сборочным сценариям CARGO_CFG_TARGET_FEATURE с
// crt-static, и cc сам выбирает /MT вместо /MD.
func buildEnv(target, cargo, root string) []string {
	prefixes := [][2]string{{cargo, "/cargo"}, {root, "/csqtt-cli"}}
	if strings.HasPrefix(cargo, root) {
		prefixes[0], prefixes[1] = prefixes[1], prefixes[0]
	}
	var flags []string
	for _, p := range prefixes {
		flags = append(flags, "--remap-path-prefix="+p[0]+"="+p[1])
	}
	if strings.HasSuffix(target, "-windows-msvc") {
		flags = append(flags, "-Ctarget-feature=+crt-static")
	}
	env := []string{"CARGO_ENCODED_RUSTFLAGS=" + strings.Join(flags, "\x1f")}

	// aws-lc-sys сам передаёт -ffile-prefix-map gcc и clang, но не cl.exe.
	// /d1trimfile — недокументированный ключ cl, которым пользуются Chromium и
	// LLVM: срезает префикс из __FILE__. Путь — с обратной косой и с ней на
	// конце, как его видит cl, и в двух видах: aws-lc-sys отдаёт cl пути
	// исходников в короткой форме 8.3.
	if strings.HasSuffix(target, "-windows-msvc") {
		var trims []string
		for _, p := range prefixes {
			for _, dir := range []string{p[0], shortPath(p[0])} {
				trim := "/d1trimfile:" + strings.TrimSuffix(dir, `\`) + `\`
				if dir != "" && !slices.Contains(trims, trim) {
					trims = append(trims, trim)
				}
			}
		}
		env = append(env, "CFLAGS_"+strings.ReplaceAll(target, "-", "_")+"="+strings.Join(trims, " "))
	}
	return env
}

func run(root, target, toolchain, out string, test, check bool) error {
	crate := filepath.Join(root, "core", "rust-client")
	manifest, err := os.ReadFile(filepath.Join(crate, "Cargo.toml"))
	if err != nil {
		return fmt.Errorf("ядра нет в core/ — сначала go run ./scripts/vendorcore: %w", err)
	}
	// Собирается ли цель здесь вообще — раньше версии rustc и установленной
	// цели: Windows на Linux не соберётся и с поставленной целью.
	host, _ := rusttarget.For(runtime.GOOS, runtime.GOARCH)
	cross, tools, err := crossEnv(host, target, os.Getenv)
	if err != nil {
		return err
	}
	if test && tools != nil {
		return fmt.Errorf("cargo test ядра — только под свою машину: тесты под %s здесь не запустятся", target)
	}

	// rustup понимает тулчейн первым аргументом: rustc +stable-… --version.
	withToolchain := func(args ...string) []string {
		if toolchain == "" {
			return args
		}
		return append([]string{"+" + toolchain}, args...)
	}
	rustup := ""
	if toolchain != "" {
		rustup = " --toolchain " + toolchain
	}

	rustcOut, err := exec.Command("rustc", withToolchain("--version")...).Output()
	if err != nil {
		return fmt.Errorf("rustc не запускается (%w) — поставьте Rust: https://rustup.rs", err)
	}
	if err := checkRustc(string(rustcOut), string(manifest)); err != nil {
		return err
	}
	libdir, err := exec.Command("rustc", withToolchain("--print", "target-libdir", "--target", target)...).Output()
	if err != nil {
		return fmt.Errorf("rustc не знает цель %s: %w", target, err)
	}
	if _, err := os.Stat(strings.TrimSpace(string(libdir))); errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("цель %s не установлена: rustup target add%s %s", target, rustup, target)
	}
	if err := checkTools(target, tools); err != nil {
		return err
	}
	if check {
		script.Sayf("%s: ядро под %s здесь собирается (%s)", self, target, strings.TrimSpace(string(rustcOut)))
		return nil
	}

	cargo, err := cargoHome()
	if err != nil {
		return err
	}
	targetDir := filepath.Join(root, "build", "cargo")
	verb := "build"
	args := []string{"--locked", "--target", target, "--target-dir", targetDir}
	if test {
		verb = "test"
	} else {
		args = append([]string{"--release"}, args...)
	}
	script.Sayf("%s: cargo %s %s (%s)", self, verb, target, strings.TrimSpace(string(rustcOut)))
	cmd := exec.Command("cargo", withToolchain(append([]string{verb}, args...)...)...)
	cmd.Dir = crate
	cmd.Env = append(append(os.Environ(), buildEnv(target, cargo, root)...), cross...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("cargo %s: %w", verb, err)
	}
	if test {
		return nil
	}

	if !filepath.IsAbs(out) {
		out = filepath.Join(root, out)
	}
	dst := filepath.Join(out, exeName(target, "csqtt-core"))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := script.CopyFile(filepath.Join(targetDir, target, "release", exeName(target, "client")), dst); err != nil {
		return err
	}
	shown := dst
	if rel, err := filepath.Rel(root, dst); err == nil && !strings.HasPrefix(rel, "..") {
		shown = filepath.ToSlash(rel)
	}
	script.Color(script.Green, fmt.Sprintf("Ядро собрано: %s", shown))
	return nil
}
