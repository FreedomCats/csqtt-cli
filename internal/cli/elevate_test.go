package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/FreedomCats/csqtt-cli/internal/supervisor"
)

func TestRelaunchArgs(t *testing.T) {
	if got, want := relaunchArgs("home", ""), []string{"menu", "-connect=home"}; !reflect.DeepEqual(got, want) {
		t.Errorf("без -config: %q, ожидалось %q", got, want)
	}

	abs, err := filepath.Abs("profiles.toml")
	if err != nil {
		t.Fatal(err)
	}
	got := relaunchArgs("home", "profiles.toml")
	if want := []string{"menu", "-config", abs, "-connect=home"}; !reflect.DeepEqual(got, want) {
		t.Errorf("с -config: %q, ожидалось %q", got, want)
	}
}

// elevatedMenu — меню с профилем в tun, которому не хватает прав root.
func elevatedMenu(t *testing.T) *testEnv {
	t.Helper()
	tunSupport(t, nil)
	runAsRoot(t, false)
	te := newMenuEnv(t)
	te.addHome()
	te.connectRun = func(context.Context, *connectPlan) error {
		t.Error("подключение без прав в самом меню")
		return nil
	}
	return te
}

// Прав нет — меню перезапускает одно подключение с ними, ждёт его и
// возвращается (Linux: sudo в том же терминале).
func TestMenuConnectRelaunchesElevated(t *testing.T) {
	te := elevatedMenu(t)
	var got [][]string
	te.elevate = func(args []string) (bool, error) {
		got = append(got, args)
		return false, nil
	}
	te.input("\n0\n")

	code, stdout, stderr := te.run("menu")
	if code != exitOK {
		t.Fatalf("код возврата %d, stderr:\n%s", code, stderr)
	}
	want := [][]string{{"menu", "-config", te.configPath(), "-connect=home"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("перезапуск с %q, ожидалось %q", got, want)
	}
	if n := strings.Count(stdout, "1. Подключиться"); n != 2 {
		t.Errorf("меню показано %d раз, ожидалось 2:\n%s", n, stdout)
	}
	if strings.Contains(stderr, "Не получилось") || strings.Contains(stderr, "socks") {
		t.Errorf("лишнее после перезапуска:\n%s", stderr)
	}
}

// Перезапуск не удался (sudo отказал, подключение с правами упало) —
// строка и снова меню, без предложения socks.
func TestMenuConnectElevatedFails(t *testing.T) {
	te := elevatedMenu(t)
	te.elevate = func([]string) (bool, error) {
		return false, errors.New("sudo или подключение с правами root завершились с кодом 1 — причина выше")
	}
	te.input("\n0\n")

	code, stdout, stderr := te.run("menu")
	if code != exitOK {
		t.Fatalf("код возврата %d, stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "Не получилось: sudo или подключение с правами root завершились с кодом 1") {
		t.Errorf("нет строки об отказе:\n%s", stderr)
	}
	if strings.Contains(stderr, "socks") {
		t.Errorf("предложен socks:\n%s", stderr)
	}
	if n := strings.Count(stdout, "1. Подключиться"); n != 2 {
		t.Errorf("меню показано %d раз, ожидалось 2:\n%s", n, stdout)
	}
}

// Подключение ушло в своё окно (Windows) — меню закрывается без паузы.
func TestMenuConnectDetached(t *testing.T) {
	te := elevatedMenu(t)
	te.elevate = func([]string) (bool, error) { return true, nil }
	te.input("\n")

	var out, errOut syncBuffer
	e := te.env(&out, &errOut)
	code := run(context.Background(), e, []string{"menu", "-config", te.configPath()})
	if code != exitOK {
		t.Fatalf("код возврата %d, stderr:\n%s", code, errOut.String())
	}
	if n := strings.Count(out.String(), "1. Подключиться"); n != 1 {
		t.Errorf("меню показано %d раз, ожидалось 1:\n%s", n, out.String())
	}
	if !e.noPause {
		t.Error("перед закрытием окна будет пауза")
	}
}

// menu -connect — так меню перезапускает себя с правами: подключение без
// меню, вопрос о новых ссылках задаёт этот процесс, после остановки — выход.
func TestMenuConnectOnlyAsksNewHashes(t *testing.T) {
	te := newMenuEnv(t)
	socksHome(t, te)
	te.core = "connected"
	var runs [][]string
	te.connectRun = func(_ context.Context, plan *connectPlan) error {
		runs = append(runs, plan.cfg.Hashes)
		if len(runs) == 1 {
			return &supervisor.HashesExhaustedError{}
		}
		return nil
	}
	te.answer("https://vk.com/call/join/" + newHash)
	te.input("да\n")

	code, stdout, stderr := te.run("menu", "-connect", "home")
	if code != exitOK {
		t.Fatalf("код возврата %d, stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "больше не работает") {
		t.Errorf("не сказано, что звонки закрыты:\n%s", stderr)
	}
	if len(runs) != 2 || strings.Join(runs[1], " ") != newHash {
		t.Errorf("подключения: %q", runs)
	}
	if strings.Contains(stdout, "1. Подключиться") || strings.Contains(stdout, "До свидания") {
		t.Errorf("показано меню:\n%s", stdout)
	}
	if strings.Contains(stdout, "снова откроется меню") {
		t.Errorf("обещано меню, которого не будет:\n%s", stdout)
	}
}

// menu -connect: остановка — выход с кодом 0, а не возврат в меню; отказ от
// новых ссылок — тоже выход. Ctrl+C до подъёма туннеля (context.Canceled) —
// не сбой: меню, которое перезапустило процесс, сказало бы о коде 1.
func TestMenuConnectOnlyExits(t *testing.T) {
	for _, c := range []struct {
		name   string
		result error
		input  string
	}{
		{name: "остановлено", result: nil},
		{name: "прервано до подъёма", result: context.Canceled},
		{name: "звонки закрылись, отказ", result: &supervisor.HashesExhaustedError{}, input: "\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			te := newMenuEnv(t)
			socksHome(t, te)
			te.core = "connected"
			runs := 0
			te.connectRun = func(context.Context, *connectPlan) error {
				runs++
				return c.result
			}
			te.input(c.input)

			code, stdout, stderr := te.run("menu", "-connect", "home")
			if code != exitOK {
				t.Fatalf("код возврата %d, stderr:\n%s", code, stderr)
			}
			if runs != 1 {
				t.Errorf("подключений %d, ожидалось 1", runs)
			}
			if strings.Contains(stdout, "1. Подключиться") {
				t.Errorf("показано меню:\n%s", stdout)
			}
		})
	}
}

// menu -connect с ошибкой подключения — код 1 и причина: меню, которое его
// перезапустило, скажет, что не получилось.
func TestMenuConnectOnlyError(t *testing.T) {
	te := newMenuEnv(t)
	socksHome(t, te)
	te.core = "connected"
	te.connectRun = func(context.Context, *connectPlan) error { return errors.New("сервер отказал") }

	code, _, stderr := te.run("menu", "-connect", "home")
	if code != exitError || !strings.Contains(stderr, "сервер отказал") {
		t.Errorf("код %d, stderr:\n%s", code, stderr)
	}

	code, _, stderr = te.run("menu", "-connect", "nope")
	if code != exitError || !strings.Contains(stderr, "nope") {
		t.Errorf("нет профиля: код %d, stderr:\n%s", code, stderr)
	}
}

// Пока идёт подключение с правами, сигналы разбирает запущенный csqtt-cli:
// Ctrl+C меню не закрывает, а SIGTERM и SIGHUP закроют его, когда
// подключение кончится.
func TestMenuSignalsWhileChild(t *testing.T) {
	exits := 0
	s := &menuSignals{exit: func() { exits++ }}
	m := &menu{signals: s}

	done := m.runningChild()
	s.handle(os.Interrupt)
	s.handle(os.Interrupt)
	if exits != 0 || m.leaving() {
		t.Fatalf("Ctrl+C при дочернем: выходов %d, закрытие %v", exits, m.leaving())
	}
	done()
	s.handle(os.Interrupt)
	if exits != 1 {
		t.Errorf("Ctrl+C в меню после дочернего: выходов %d", exits)
	}

	for _, sig := range []os.Signal{syscall.SIGTERM, syscall.SIGHUP} {
		exits := 0
		s := &menuSignals{exit: func() { exits++ }}
		m := &menu{signals: s}
		done := m.runningChild()
		s.handle(sig)
		done()
		if exits != 0 || !m.leaving() {
			t.Errorf("%v при дочернем: выходов %d, закрытие %v", sig, exits, m.leaving())
		}
	}
}

// menu -connect без root — это перезапуск, после которого прав так и нет
// (sudo запустил не от root), или его набрали руками: снова перезапускать
// нельзя — процессы вкладывались бы без конца. Ошибка, код 1.
func TestMenuConnectOnlyWithoutRootDoesNotRelaunch(t *testing.T) {
	for _, c := range []struct {
		name, sudoUser, want string
	}{
		{name: "через sudo", sudoUser: "user", want: "права root не получены: csqtt-cli запущен через sudo, но не от root"},
		{name: "без sudo", want: adminHint},
	} {
		t.Run(c.name, func(t *testing.T) {
			te := elevatedMenu(t)
			t.Setenv("SUDO_USER", c.sudoUser)
			relaunches := 0
			te.elevate = func([]string) (bool, error) {
				relaunches++
				return false, nil
			}

			code, _, stderr := te.run("menu", "-connect", "home")
			if code != exitError || !strings.Contains(stderr, c.want) {
				t.Errorf("код %d, stderr:\n%s", code, stderr)
			}
			if relaunches != 0 {
				t.Errorf("перезапусков %d, ожидалось 0", relaunches)
			}
		})
	}
}

// Профиль по имени -h: перезапуск передаёт имя одним аргументом, и
// запущенный csqtt-cli — меню с его сигналами, а не помощь.
func TestRelaunchProfileNamedLikeFlag(t *testing.T) {
	te := newMenuEnv(t)
	te.answer(testPassword, testPassword, testHashA)
	var out, errOut syncBuffer
	if code := run(context.Background(), te.env(&out, &errOut), []string{"profile", "add", "-config", te.configPath(),
		"-peer", "203.0.113.10", "-datapath", "socks", "--", "-h"}); code != exitOK {
		t.Fatalf("profile add: код %d, stderr:\n%s", code, errOut.String())
	}

	args := relaunchArgs("-h", te.configPath())
	e := te.env(&out, &errOut)
	if !wantsMenu(e, args) {
		t.Fatalf("%q — не меню", args)
	}
	te.core = "connected"
	t.Setenv(fakeCoreEnv, te.core)
	var names []string
	e.connectRun = func(_ context.Context, plan *connectPlan) error {
		names = append(names, plan.profile.Name)
		return nil
	}
	if code := run(context.Background(), e, args); code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, errOut.String())
	}
	if len(names) != 1 || names[0] != "-h" {
		t.Errorf("подключения: %q", names)
	}
}
