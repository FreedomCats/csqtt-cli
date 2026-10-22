package cli

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/FreedomCats/csqtt-cli/internal/captcha"
	"github.com/FreedomCats/csqtt-cli/internal/config"
)

// testVKToken — выдуманный токен VK: в выводе, журнале и profiles.toml его
// быть не должно.
const testVKToken = "vk1.a.выдуманный-токен-vk-0123456789abcdef"

// fakeLoginWindow — окно входа без браузера: отдаёт заготовленный итог и
// пишет строку в журнал окна (видна только с -v). Что журнал настоящего окна
// токена не несёт, проверяют тесты окна (internal/captcha).
type fakeLoginWindow struct {
	opened int
	tok    captcha.VKToken
	err    error
	logf   func(string, ...any)
	// during зовётся, пока окно «открыто»: так тест меняет файл настроек
	// посреди входа.
	during func()
}

func (w *fakeLoginWindow) Login(ctx context.Context) (captcha.VKToken, error) {
	if w.logf != nil {
		w.logf("[VK] окно входа: переход на blank.html перехвачен")
	}
	if w.during != nil {
		w.during()
	}
	if w.err != nil {
		return captcha.VKToken{}, w.err
	}
	return w.tok, ctx.Err()
}

// vkEnv — окружение теста входа: терминал, ответ на вопрос о согласии и
// подставное окно.
func vkEnv(t *testing.T, answer string, window *fakeLoginWindow) *testEnv {
	te := newTestEnv(t)
	te.tty = true
	te.stdin = strings.NewReader(answer)
	te.vkLogin = func(opts captcha.LoginOptions) (captcha.LoginWindow, error) {
		window.opened++
		window.logf = opts.Logf
		return window, nil
	}
	return te
}

func goodWindow() *fakeLoginWindow {
	return &fakeLoginWindow{tok: captcha.VKToken{AccessToken: testVKToken, UserID: "123456789"}}
}

// noVKToken — токена нет ни в выводе, ни в файле настроек.
func noVKToken(t *testing.T, te *testEnv, texts ...string) {
	t.Helper()
	raw, _ := os.ReadFile(te.configPath())
	for _, text := range append(texts, string(raw)) {
		if strings.Contains(text, testVKToken) {
			t.Errorf("токен VK напечатан или записан в файл:\n%s", text)
		}
	}
}

func TestVKLogin(t *testing.T) {
	for _, verbose := range []bool{false, true} {
		name := "обычно"
		if verbose {
			name = "-v"
		}
		t.Run(name, func(t *testing.T) {
			window := goodWindow()
			te := vkEnv(t, "да\n", window)
			te.addHome()
			args := []string{"vk", "login"}
			if verbose {
				args = append(args, "-v")
			}
			code, stdout, stderr := te.run(args...)
			if code != exitOK || window.opened != 1 {
				t.Fatalf("код %d, окон %d, stderr:\n%s", code, window.opened, stderr)
			}
			noVKToken(t, te, stdout, stderr)
			// Предупреждение — до вопроса, по смыслу целиком.
			for _, want := range []string{"неофициальными", "Блокировку аккаунта VK исключить нельзя",
				"второстепенным", "бессрочный", "все права, кроме сообщений", "не ограничено",
				"согласие на доступ токена", "экрана с правами приложения VK может не показать",
				"Если VK\nпокажет экран доступа", "ничего не нажимает", "[y/N]"} {
				if !strings.Contains(stderr, want) {
					t.Errorf("в предупреждении нет %q:\n%s", want, stderr)
				}
			}
			// Обещания, что экран прав будет, нет: при входе через VK ID его не
			// показали.
			if strings.Contains(stderr, "сами\nподтвердите доступ приложению") || strings.Contains(stderr, "сами подтвердите доступ") {
				t.Errorf("предупреждение обещает экран прав:\n%s", stderr)
			}
			if verbose != strings.Contains(stderr, "переход на blank.html") {
				t.Errorf("журнал окна при -v=%v:\n%s", verbose, stderr)
			}
			if !strings.Contains(stdout, "123456789") {
				t.Errorf("нет id аккаунта:\n%s", stdout)
			}

			file, err := config.Load(te.configPath())
			if err != nil {
				t.Fatal(err)
			}
			if file.VK.UserID != "123456789" || file.Profiles["home"] == nil {
				t.Fatalf("файл настроек: [vk] %+v, профили %v", file.VK, file.Names())
			}
			if got, err := te.store.Get(file.VK.TokenRef); err != nil || got != testVKToken {
				t.Fatalf("токен в хранилище: верный %v, %v", got == testVKToken, err)
			}
		})
	}
}

// Отказ — ни окна, ни записи. Умолчание — «нет».
func TestVKLoginRefused(t *testing.T) {
	for name, answer := range map[string]string{"нет": "n\n", "Enter": "\n", "конец ввода": ""} {
		t.Run(name, func(t *testing.T) {
			window := goodWindow()
			te := vkEnv(t, answer, window)
			code, stdout, stderr := te.run("vk", "login")
			if code != exitOK || window.opened != 0 {
				t.Fatalf("код %d, окон %d, stderr:\n%s", code, window.opened, stderr)
			}
			if !strings.Contains(stdout, "Отменено") {
				t.Errorf("stdout:\n%s", stdout)
			}
			if _, err := os.Stat(te.configPath()); !os.IsNotExist(err) {
				t.Errorf("файл настроек появился: %v", err)
			}
			if len(te.store.values) != 0 {
				t.Errorf("в хранилище записано: %d", len(te.store.values))
			}
		})
	}
}

// Без терминала согласие спросить не у кого — отказ без вопроса и окна.
func TestVKLoginNoTerminal(t *testing.T) {
	window := goodWindow()
	te := vkEnv(t, "да\n", window)
	te.tty = false
	code, _, stderr := te.run("vk", "login")
	if code != exitError || window.opened != 0 || !strings.Contains(stderr, "в терминале") ||
		strings.Contains(stderr, "[y/N]") {
		t.Fatalf("код %d, окон %d, stderr:\n%s", code, window.opened, stderr)
	}
}

func TestVKLoginFails(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		ctx  func() (context.Context, context.CancelFunc)
		want string
	}{
		{name: "окно закрыто", err: captcha.ErrLoginClosed, want: "окно входа VK закрыто"},
		{name: "отказ VK", err: &captcha.VKAuthError{Code: "access_denied", Description: "User denied your request"},
			want: "User denied your request"},
		{name: "срок", err: context.DeadlineExceeded, want: "время на вход"},
		{name: "Ctrl+C", err: context.Canceled, want: "прервано", ctx: func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx, func() {}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			window := &fakeLoginWindow{err: tc.err}
			te := vkEnv(t, "да\n", window)
			ctx, cancel := context.Background(), func() {}
			if tc.ctx != nil {
				ctx, cancel = tc.ctx()
			}
			defer cancel()
			// Ctrl+C до вопроса — вопрос и сам вернёт отмену; окно тогда не
			// открывается вовсе, и это тоже правильно.
			code, _, stderr := te.runCtx(ctx, "vk", "login")
			if code != exitError || !strings.Contains(stderr, tc.want) {
				t.Fatalf("код %d, stderr:\n%s", code, stderr)
			}
			if tc.ctx == nil && !strings.Contains(stderr, "токен не записан") {
				t.Errorf("не сказано, что токен не записан:\n%s", stderr)
			}
			if _, err := os.Stat(te.configPath()); !os.IsNotExist(err) || len(te.store.values) != 0 {
				t.Errorf("что-то записано: файл %v, секретов %d", err, len(te.store.values))
			}
		})
	}
}

func TestVKStatusAndLogout(t *testing.T) {
	window := goodWindow()
	te := vkEnv(t, "да\n", window)

	code, stdout, _ := te.run("vk", "status")
	if code != exitOK || !strings.Contains(stdout, "не выполнен") {
		t.Fatalf("до входа: код %d:\n%s", code, stdout)
	}
	if code, _, stderr := te.run("vk", "login"); code != exitOK {
		t.Fatalf("login: код %d:\n%s", code, stderr)
	}

	code, stdout, stderr := te.run("vk", "status")
	if code != exitOK || !strings.Contains(stdout, "Вход в VK выполнен") || !strings.Contains(stdout, "123456789") ||
		!strings.Contains(stdout, "есть") {
		t.Fatalf("после входа: код %d:\n%s%s", code, stdout, stderr)
	}
	noVKToken(t, te, stdout, stderr)

	// Токен не читается — status говорит об этом, но токена не печатает.
	te.store.getErr = errors.New("связка заблокирована")
	code, stdout, _ = te.run("vk", "status")
	if code != exitOK || !strings.Contains(stdout, "не читается") {
		t.Fatalf("токен не читается: код %d:\n%s", code, stdout)
	}
	te.store.getErr = nil

	code, stdout, stderr = te.run("vk", "logout")
	if code != exitOK || !strings.Contains(stdout, "стёрт") || !strings.Contains(stdout, "отозван") {
		t.Fatalf("logout: код %d:\n%s%s", code, stdout, stderr)
	}
	if len(te.store.values) != 0 {
		t.Errorf("в хранилище осталось: %d", len(te.store.values))
	}
	file, err := config.Load(te.configPath())
	if err != nil || file.VK != (config.VKSection{}) {
		t.Fatalf("после logout: %+v, %v", file, err)
	}

	code, stdout, _ = te.run("vk", "status")
	if code != exitOK || !strings.Contains(stdout, "не выполнен") {
		t.Fatalf("после logout status: код %d:\n%s", code, stdout)
	}
	code, stdout, _ = te.run("vk", "logout")
	if code != exitOK || !strings.Contains(stdout, "стирать нечего") {
		t.Fatalf("повторный logout: код %d:\n%s", code, stdout)
	}
}

// Повторный вход заменяет прежний токен.
func TestVKLoginReplaces(t *testing.T) {
	window := goodWindow()
	te := vkEnv(t, "да\n", window)
	if code, _, stderr := te.run("vk", "login"); code != exitOK {
		t.Fatalf("первый вход: %s", stderr)
	}
	te.stdin = strings.NewReader("да\n")
	window.tok = captcha.VKToken{AccessToken: testVKToken + "-2", UserID: "987"}
	code, stdout, stderr := te.run("vk", "login")
	if code != exitOK || !strings.Contains(stdout, "Прежний вход заменён") {
		t.Fatalf("второй вход: код %d:\n%s%s", code, stdout, stderr)
	}
	file, _ := config.Load(te.configPath())
	if got, _ := te.store.Get(file.VK.TokenRef); got != testVKToken+"-2" || file.VK.UserID != "987" || len(te.store.values) != 1 {
		t.Fatalf("после замены: user_id %q, секретов %d", file.VK.UserID, len(te.store.values))
	}
}

// Пока окно открыто, файл настроек меняют другие команды; вход пишет [vk] в
// файл, прочитанный после окна, и их изменений не теряет.
func TestVKLoginKeepsConcurrentChanges(t *testing.T) {
	window := goodWindow()
	te := vkEnv(t, "да\n", window)
	te.addHome()
	window.during = func() {
		file, err := config.Load(te.configPath())
		if err != nil {
			t.Fatal(err)
		}
		work := *file.Profiles["home"]
		work.Name, work.DeviceID = "work", ""
		file.Profiles["work"] = &work
		if err := config.Save(te.configPath(), file); err != nil {
			t.Fatal(err)
		}
	}

	if code, _, stderr := te.run("vk", "login"); code != exitOK {
		t.Fatalf("login: код %d:\n%s", code, stderr)
	}
	file, err := config.Load(te.configPath())
	if err != nil {
		t.Fatal(err)
	}
	if file.Profiles["work"] == nil || file.Profiles["home"] == nil || !file.VK.LoggedIn() {
		t.Fatalf("после входа: профили %v, [vk] записана %v", file.Names(), file.VK.LoggedIn())
	}
}

// Команды profile переписывают файл целиком; секцию [vk] и токен они
// сохраняют.
func TestVKSurvivesProfileCommands(t *testing.T) {
	window := goodWindow()
	te := vkEnv(t, "да\n", window)
	te.addHome()
	if code, _, stderr := te.run("vk", "login"); code != exitOK {
		t.Fatalf("login: код %d:\n%s", code, stderr)
	}
	before, err := config.Load(te.configPath())
	if err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{
		{"profile", "set", "home", "-datapath", "socks"},
		{"profile", "rm", "-y", "home"},
	} {
		if code, _, stderr := te.run(args...); code != exitOK {
			t.Fatalf("%q: код %d:\n%s", args, code, stderr)
		}
		file, err := config.Load(te.configPath())
		if err != nil {
			t.Fatal(err)
		}
		if file.VK != before.VK {
			t.Fatalf("после %q [vk] изменилась: user_id %q, ссылка та же %v", args, file.VK.UserID,
				file.VK.TokenRef == before.VK.TokenRef)
		}
		if got, err := te.store.Get(file.VK.TokenRef); err != nil || got != testVKToken {
			t.Fatalf("после %q токен в хранилище: верный %v, %v", args, got == testVKToken, err)
		}
	}
}

func TestVKUsage(t *testing.T) {
	te := newTestEnv(t)
	for _, args := range [][]string{{"vk"}, {"vk", "nope"}, {"vk", "login", "home"}, {"vk", "status", "x"}} {
		if code, _, _ := te.run(args...); code != exitUsage {
			t.Errorf("%q: код %d", args, code)
		}
	}
}

// -net-allow — диагностика сети окна: фильтр доходит до окна, без флага
// его нет; в помощи флаг не показывается.
func TestVKLoginNetAllow(t *testing.T) {
	for _, tc := range []struct {
		args []string
		spec string // пусто — фильтра нет
	}{
		{nil, ""},
		{[]string{"-net-allow", "vk,mail.ru"}, "vk,mail.ru"},
		{[]string{"-net-allow=all", "-v"}, "all"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			window := goodWindow()
			te := vkEnv(t, "да\n", window)
			var got *captcha.NetFilter
			te.vkLogin = func(opts captcha.LoginOptions) (captcha.LoginWindow, error) {
				window.opened++
				got = opts.Net
				return window, nil
			}
			code, _, stderr := te.run(append([]string{"vk", "login"}, tc.args...)...)
			if code != exitOK || window.opened != 1 {
				t.Fatalf("код %d, окон %d, stderr:\n%s", code, window.opened, stderr)
			}
			switch {
			case tc.spec == "" && got != nil:
				t.Errorf("без флага окну дан фильтр %q", got)
			case tc.spec != "" && (got == nil || got.String() != tc.spec):
				t.Errorf("фильтр окна %v, ожидался %q", got, tc.spec)
			}
		})
	}
}

// Негодное значение — ошибка командной строки до вопроса о согласии.
func TestVKLoginNetAllowInvalid(t *testing.T) {
	for _, value := range []string{"", "https://vk.com", "all,vk", "203.0.113.1"} {
		t.Run(value, func(t *testing.T) {
			window := goodWindow()
			te := vkEnv(t, "да\n", window)
			code, _, stderr := te.run("vk", "login", "-net-allow", value)
			if code != exitUsage || window.opened != 0 {
				t.Fatalf("код %d, окон %d, stderr:\n%s", code, window.opened, stderr)
			}
			if !strings.Contains(stderr, "-net-allow:") || strings.Contains(stderr, "[y/N]") {
				t.Errorf("stderr:\n%s", stderr)
			}
		})
	}
}

func TestVKLoginHelpHidesNetAllow(t *testing.T) {
	te := newTestEnv(t)
	code, stdout, _ := te.run("vk", "login", "-h")
	if code != exitOK {
		t.Fatalf("код %d", code)
	}
	if strings.Contains(stdout, "net-allow") {
		t.Errorf("в помощи скрытый флаг:\n%s", stdout)
	}
	for _, want := range []string{"-config", "-v\t", "подробно"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("в помощи нет %q:\n%s", want, stdout)
		}
	}
	// -v -h: умолчание -v — прежнее, а не то, что задал разбор.
	if _, stdout, _ = te.run("vk", "login", "-v", "-h"); strings.Contains(stdout, "(default true)") {
		t.Errorf("умолчание -v после разбора:\n%s", stdout)
	}
}
