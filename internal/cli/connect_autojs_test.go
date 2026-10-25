package cli

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/FreedomCats/csqtt-cli/internal/config"
	"github.com/FreedomCats/csqtt-cli/internal/coreproc"
	"github.com/FreedomCats/csqtt-cli/internal/supervisor"
)

// autoJSHome — профиль home в режиме auto_js с datapath socks; login — есть
// и вход VK (токен testVKToken в подставном хранилище).
func autoJSHome(t *testing.T, te *testEnv, login bool) {
	t.Helper()
	socksHome(t, te)
	if code, _, stderr := te.run("profile", "set", "home", "-vk-hash-mode", "auto_js"); code != exitOK {
		t.Fatalf("profile set -vk-hash-mode: код %d, stderr:\n%s", code, stderr)
	}
	if login {
		vkLoggedIn(t, te)
	}
}

// vkLoggedIn записывает вход VK так же, как vk login, — без окна.
func vkLoggedIn(t *testing.T, te *testEnv) {
	t.Helper()
	file := loadTestFile(t, te)
	ref, err := te.store.Put(config.VKTokenSecret, testVKToken)
	if err != nil {
		t.Fatal(err)
	}
	file.VK = config.VKSection{TokenRef: ref, UserID: "123456789"}
	if err := config.Save(te.configPath(), file); err != nil {
		t.Fatal(err)
	}
}

// noAutoJSSecrets — ни токена VK (и его base64 в строке ядра), ни хеша
// созданного звонка, ни пароля и хешей профиля в тексте.
func noAutoJSSecrets(t *testing.T, where, text string) {
	t.Helper()
	noSecrets(t, where, text)
	token64 := base64.StdEncoding.EncodeToString([]byte(`{"token":"` + testVKToken + `"}`))
	for _, secret := range []string{testVKToken, token64, fakeCallHash, fakeCallHash[:8]} {
		if strings.Contains(text, secret) {
			t.Errorf("%s: в выводе секрет %q:\n%s", where, secret, text)
		}
	}
}

// Профиль в auto_js без входа VK: понятная ошибка с подсказкой vk login до
// запуска ядра — и раньше отказа tun без прав: входят в VK без sudo.
func TestConnectAutoJSWithoutLogin(t *testing.T) {
	te := newTestEnv(t)
	autoJSHome(t, te, false)
	te.core = "autojs"
	for _, args := range [][]string{{"connect"}, {"connect", "-datapath", "tun"}} {
		code, stdout, stderr := te.run(args...)
		if code != exitError || !strings.Contains(stderr, "входа в VK нет") || !strings.Contains(stderr, "csqtt-cli vk login") {
			t.Errorf("%q: код %d, stderr:\n%s", args, code, stderr)
		}
		if strings.Contains(stderr, "root") || strings.Contains(stdout, "профиль home: сервер") {
			t.Errorf("%q: дошло до прав или ядра:\n%s\n%s", args, stdout, stderr)
		}
	}
}

// Вход есть, а токен не читается (связка заперта, запись пропала) — тоже
// ошибка до ядра с подсказкой войти заново.
func TestConnectAutoJSTokenUnreadable(t *testing.T) {
	te := newTestEnv(t)
	autoJSHome(t, te, true)
	te.core = "autojs"
	if err := te.store.Delete("fake:" + config.VKTokenSecret); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := te.run("connect")
	if code != exitError || !strings.Contains(stderr, "токен входа VK не читается") || !strings.Contains(stderr, "vk login") {
		t.Errorf("код %d, stderr:\n%s", code, stderr)
	}
	if strings.Contains(stdout, "профиль home: сервер") {
		t.Errorf("ядро запускалось:\n%s", stdout)
	}
}

// Подключение в auto_js: токен уходит ядру в stdin, а не в аргументах; хешей
// нет, воркеров не больше 70; по Ctrl+C звонок завершается. Ни токена, ни
// хеша созданного звонка в выводе.
func TestConnectAutoJS(t *testing.T) {
	te := newTestEnv(t)
	autoJSHome(t, te, true)
	if code, _, stderr := te.run("profile", "set", "home", "-workers", "100"); code != exitOK {
		t.Fatalf("profile set -workers: код %d, stderr:\n%s", code, stderr)
	}
	te.core = "autojs"
	t.Setenv(fakeCoreEnv, te.core)

	var out, errOut syncBuffer
	e := te.env(&out, &errOut)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() { done <- run(ctx, e, []string{"connect", "-v", "home", "-config", te.configPath()}) }()

	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(out.String(), "туннель работает") {
		if time.Now().After(deadline) {
			t.Fatalf("туннель не поднялся:\n%s\nstderr:\n%s", out.String(), errOut.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case code := <-done:
		if code != exitOK {
			t.Errorf("код %d, stderr:\n%s", code, errOut.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatal("connect не вернулся после Ctrl+C")
	}

	got := out.String()
	noAutoJSSecrets(t, "stdout", got)
	noAutoJSSecrets(t, "stderr", errOut.String())
	for _, want := range []string{
		"профиль home: сервер 203.0.113.10:46000, звонок VK создаёт ядро (auto_js), воркеров 70",
		"воркеров в профиле 100, а ядро запустит 70 (в auto_js не больше 70)",
		"--workers=70", "--vk-hash-mode=auto_js", "--vk-auth-mode=auto_js",
		"VK_JS_BOOTSTRAP:<скрыт",
		"(хеш: <хеш>...)",
		"завершаем звонок VK… (до 38 с)",
		"[VK JS] Звонки завершены 1/1",
		"остановлено",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("нет %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "мог остаться") {
		t.Errorf("звонок назван оставшимся:\n%s", got)
	}
}

// VK отверг токен — конец без повторов с подсказкой войти заново.
func TestConnectAutoJSTokenRejected(t *testing.T) {
	te := newTestEnv(t)
	autoJSHome(t, te, true)
	te.core = "autojs-rejected"
	code, stdout, stderr := te.run("connect")
	if code != exitError {
		t.Fatalf("код %d:\n%s\n%s", code, stdout, stderr)
	}
	for _, want := range []string{"VK не принял токен входа: API 5", "войдите заново: csqtt-cli vk login"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("нет %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stdout, "переподключение") || strings.Contains(stderr, "той же сборки") {
		t.Errorf("отказ токена повторяли или списали на сборку:\n%s\n%s", stdout, stderr)
	}
	noAutoJSSecrets(t, "вывод", stdout+stderr)
}

// Подсказки к концу подключения в auto_js: о сборке — только когда ядро не
// приняло то, что ему дала оболочка, или причины в журнале нет; отказ и ответ
// VK — без неё.
func TestExplainConnectErrorAutoJS(t *testing.T) {
	fatal := func(line string) *coreproc.FatalError {
		f, ok := coreproc.ParseFatal(line)
		if !ok {
			t.Fatalf("ParseFatal(%q)", line)
		}
		return f
	}
	const build = "той же сборки"
	for _, c := range []struct {
		name    string
		err     error
		want    string
		noBuild bool
	}{
		{"отказ VK", &supervisor.VKError{Fatal: fatal("[ФАТАЛ] vchat.startConversation: API 9: Flood control")},
			"VK отказал: vchat.startConversation: API 9: Flood control\n  попробуйте позже", true},
		{"повторы исчерпаны", &supervisor.VKError{Fatal: fatal("[ФАТАЛ] API 10: Internal server error"), Retries: 5},
			"VK отказал 6 раз подряд, повторы исчерпаны: API 10: Internal server error\n  попробуйте позже", true},
		{"ответ VK не того вида", &supervisor.StartupError{Fatal: fatal("[ФАТАЛ] vchat.startConversation: нет join_link")},
			"VK ответил не так, как ждёт ядро", true},
		{"имя сервера", &supervisor.StartupError{Fatal: fatal("[ФАТАЛ] ошибка разбора пира: пустой DNS-ответ для пира"),
			Log: []string{"[ФАТАЛ] ошибка разбора пира: пустой DNS-ответ для пира"}}, "ошибка разбора пира", true},
		{"аргументы", &supervisor.StartupError{Fatal: fatal("[ФАТАЛ] [КЛИЕНТ] Нужен -password: WRAP ключ выводится из пароля подключения")},
			build, false},
		{"данные auto_js", &supervisor.StartupError{Fatal: fatal("[ФАТАЛ] тайм-аут передачи данных Auto JS: deadline has elapsed")},
			build, false},
		{"без причины", &supervisor.StartupError{Log: []string{"error: unexpected argument '--secrets-stdin' found"}},
			build, false},
	} {
		got := explainConnectError(c.err, "home", nil, Build{}).Error()
		if !strings.Contains(got, c.want) || c.noBuild == strings.Contains(got, build) {
			t.Errorf("%s: %q", c.name, got)
		}
	}
}

// hashes check для профиля в auto_js — отказ: хешей в этом режиме нет.
// Ядро не запускается.
func TestHashesCheckAutoJS(t *testing.T) {
	te := newTestEnv(t)
	autoJSHome(t, te, true)
	code, _, stderr := te.run("hashes", "check")
	if code != exitError || !strings.Contains(stderr, "auto_js") || !strings.Contains(stderr, "проверять нечего") ||
		!strings.Contains(stderr, "-vk-hash-mode manual") {
		t.Errorf("код %d, stderr:\n%s", code, stderr)
	}
	if strings.Contains(stderr, "ядра в этом тесте нет") {
		t.Errorf("искали ядро:\n%s", stderr)
	}
}

// Меню профиля в auto_js не предлагает ссылки на звонки, а подключение из
// него идёт с токеном и без хешей.
func TestMenuAutoJS(t *testing.T) {
	te := newMenuEnv(t)
	autoJSHome(t, te, true)
	var plans []*connectPlan
	// План ищет ядро, но подъём подставной: ядро не запускается.
	te.core = "autojs"
	te.connectRun = func(_ context.Context, plan *connectPlan) error {
		plans = append(plans, plan)
		return nil
	}
	te.input("\n0\n")

	code, stdout, stderr := te.run("menu")
	if code != exitOK {
		t.Fatalf("код возврата %d, stderr:\n%s", code, stderr)
	}
	if strings.Contains(stdout, "ссылки на звонки") {
		t.Errorf("меню в auto_js предлагает ссылки на звонки:\n%s", stdout)
	}
	for _, want := range []string{"звонок VK — auto_js", "4. Заменить пароль или адрес сервера", "5. Удалить профиль home"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("нет %q:\n%s", want, stdout)
		}
	}
	if len(plans) != 1 || plans[0].cfg.VKToken != testVKToken || len(plans[0].cfg.Hashes) != 0 || !plans[0].cfg.AutoJS() {
		t.Errorf("подключения из меню: %d", len(plans))
	}
	noAutoJSSecrets(t, "вывод", stdout+stderr)
}
