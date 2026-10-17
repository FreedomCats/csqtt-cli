// Команда buildcore собирает ядро CSQTT из core/ в build/out/csqtt-core[.exe].
//
//	go run ./scripts/buildcore
//	go run ./scripts/buildcore -target aarch64-unknown-linux-gnu -out build/out/linux-arm64
//	go run ./scripts/buildcore -toolchain stable-x86_64-pc-windows-msvc
//	go run ./scripts/buildcore -test
//
// Обёртка над cargo build --release --locked в core/rust-client. Что добавляет
// к голому cargo:
//
//   - цель по умолчанию — для этой машины: x86_64-pc-windows-msvc,
//     x86_64-unknown-linux-gnu или aarch64-unknown-linux-gnu (musl ядро не
//     собирает: udp_batch.rs передаёт флаги recvmmsg как c_int, а у musl они
//     c_uint);
//   - до сборки — понятная строка, если rustc старше rust-version ядра или
//     цель не установлена, вместо ошибки cargo на середине;
//   - каталог сборки — build/cargo, вне core/: vendorcore -check его не видит;
//   - пути этой машины не попадают в бинарь: каталог cargo и репозиторий
//     переписываются в /cargo и /csqtt-cli (--remap-path-prefix), а cl.exe
//     обрезает их в __FILE__ C-исходников aws-lc (/d1trimfile). Иначе бинарь
//     нёс бы домашний каталог с именем пользователя;
//   - бинарь client копируется под именем csqtt-core в -out.
//
// -test вместо сборки гоняет cargo test ядра. Тесты, которым нужен живой
// сервер, в ядре помечены #[ignore] и не запускаются.
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

	"github.com/FreedomCats/csqtt-cli/scripts/internal/script"
)

const self = "buildcore"

func main() {
	target := flag.String("target", "", "цель Rust (по умолчанию — для этой машины)")
	toolchain := flag.String("toolchain", "", "тулчейн rustup, например stable-x86_64-pc-windows-msvc (по умолчанию — активный)")
	out := flag.String("out", filepath.Join("build", "out"), "куда положить csqtt-core, относительно корня репозитория")
	test := flag.Bool("test", false, "не собирать, а прогнать cargo test ядра")
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
	if err := run(root, *target, *toolchain, *out, *test); err != nil {
		script.Fail(self, err)
	}
}

// defaultTarget — цель Rust для машины goos/goarch.
func defaultTarget(goos, goarch string) (string, error) {
	switch goos + "/" + goarch {
	case "windows/amd64":
		return "x86_64-pc-windows-msvc", nil
	case "linux/amd64":
		return "x86_64-unknown-linux-gnu", nil
	case "linux/arm64":
		return "aarch64-unknown-linux-gnu", nil
	}
	return "", fmt.Errorf("для %s/%s цели по умолчанию нет — укажите -target", goos, goarch)
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

// buildEnv — переменные окружения cargo, убирающие пути машины из бинаря.
//
// CARGO_ENCODED_RUSTFLAGS, а не RUSTFLAGS: флаги разделены 0x1f, и пробел в
// пути их не разрежет. Из двух подходящих префиксов rustc берёт последний,
// поэтому вложенный каталог идёт вторым: обычно это репозиторий, но
// CARGO_HOME может лежать и внутри него (так бывает в CI).
func buildEnv(target, cargo, root string) []string {
	prefixes := [][2]string{{cargo, "/cargo"}, {root, "/csqtt-cli"}}
	if strings.HasPrefix(cargo, root) {
		prefixes[0], prefixes[1] = prefixes[1], prefixes[0]
	}
	var flags []string
	for _, p := range prefixes {
		flags = append(flags, "--remap-path-prefix="+p[0]+"="+p[1])
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

func run(root, target, toolchain, out string, test bool) error {
	crate := filepath.Join(root, "core", "rust-client")
	manifest, err := os.ReadFile(filepath.Join(crate, "Cargo.toml"))
	if err != nil {
		return fmt.Errorf("ядра нет в core/ — сначала go run ./scripts/vendorcore: %w", err)
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
	cmd.Env = append(os.Environ(), buildEnv(target, cargo, root)...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("cargo %s: %w", verb, err)
	}
	if test {
		return nil
	}

	dst := filepath.Join(root, out, exeName(target, "csqtt-core"))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := script.CopyFile(filepath.Join(targetDir, target, "release", exeName(target, "client")), dst); err != nil {
		return err
	}
	rel, _ := filepath.Rel(root, dst)
	script.Color(script.Green, fmt.Sprintf("Ядро собрано: %s", filepath.ToSlash(rel)))
	return nil
}
