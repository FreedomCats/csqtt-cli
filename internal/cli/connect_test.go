package cli

import (
	"context"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/FreedomCats/csqtt-cli/internal/config"
)

func loadTestFile(t *testing.T, te *testEnv) *config.File {
	t.Helper()
	file, err := config.Load(te.configPath())
	if err != nil {
		t.Fatal(err)
	}
	return file
}

// freeLoopbackPort — свободный порт TCP на loopback для SOCKS5 теста.
func freeLoopbackPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// socksHome — профиль home с datapath socks на свободном порту.
func socksHome(t *testing.T, te *testEnv) string {
	t.Helper()
	listen := freeLoopbackPort(t)
	te.addHome("-datapath", "socks", "-socks-listen", listen)
	return listen
}

// Профиль с datapath = tun (умолчание) — отказ с подсказкой, а не тихий
// SOCKS5: ядро не запускается.
func TestConnectRefusesTun(t *testing.T) {
	te := newTestEnv(t)
	te.addHome()
	te.core = "connected"
	for _, args := range [][]string{{"connect"}, {"connect", "-datapath", "tun", "home"}} {
		code, stdout, stderr := te.run(args...)
		if code != exitError {
			t.Errorf("%q: код %d", args, code)
		}
		for _, want := range []string{"tun", "-datapath socks", "home -datapath socks)"} {
			if !strings.Contains(stderr, want) {
				t.Errorf("%q: нет %q:\n%s", args, want, stderr)
			}
		}
		if strings.Contains(stdout, "запуск ядра") {
			t.Errorf("%q: ядро запускалось:\n%s", args, stdout)
		}
	}
	if code, _, stderr := te.run("connect", "-datapath", "wintun"); code != exitUsage || !strings.Contains(stderr, "допустимы") {
		t.Errorf("чужой datapath: код %d, stderr:\n%s", code, stderr)
	}
}

// Туннель поднимается, Ctrl+C его останавливает: ядро получает STOP и выходит,
// connect возвращает 0. В выводе — SOCKS5, но ни секретов, ни токена сессии
// капчи из журнала ядра.
func TestConnectUpThenInterrupt(t *testing.T) {
	te := newTestEnv(t)
	listen := socksHome(t, te)
	te.core = "connected"
	t.Setenv(fakeCoreEnv, te.core)

	var out, errOut syncBuffer
	e := te.env(&out, &errOut)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, e, []string{"connect", "-datapath", "socks", "-v", "home", "-config", te.configPath()})
	}()

	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(out.String(), "туннель работает") {
		if time.Now().After(deadline) {
			t.Fatalf("туннель не поднялся:\n%s\nstderr:\n%s", out.String(), errOut.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	// SOCKS5 слушает, пока туннель жив.
	if c, err := net.DialTimeout("tcp", listen, time.Second); err != nil {
		t.Errorf("SOCKS5 не слушает %s: %v", listen, err)
	} else {
		c.Close()
	}

	cancel()
	select {
	case code := <-done:
		if code != exitOK {
			t.Errorf("код %d, stderr:\n%s", code, errOut.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("connect не вернулся после Ctrl+C")
	}

	got := out.String()
	noSecrets(t, "stdout", got)
	noSecrets(t, "stderr", errOut.String())
	if strings.Contains(got, fakeSecretSession) {
		t.Errorf("токен сессии капчи в выводе:\n%s", got)
	}
	for _, want := range []string{
		"профиль home: сервер 203.0.113.10:46000, хешей 3, воркеров 18, SOCKS5 " + listen,
		"== туннель работает · SOCKS5 на " + listen,
		"session_token=<скрыт",
		"хеш <хеш №1>",
		"ядро остановилось",
		"остановлено",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("нет %q:\n%s", want, got)
		}
	}
	// После остановки порт SOCKS5 свободен.
	if ln, err := net.Listen("tcp", listen); err != nil {
		t.Errorf("SOCKS5 не закрыт: %v", err)
	} else {
		ln.Close()
	}
}

// Сервер отказал (другая версия протокола) — остановка без повтора и
// подсказка про версию сервера.
func TestConnectServerRejected(t *testing.T) {
	te := newTestEnv(t)
	socksHome(t, te)
	te.core = "rejected"
	code, stdout, stderr := te.run("connect")
	if code != exitError {
		t.Fatalf("код %d:\n%s\n%s", code, stdout, stderr)
	}
	for _, want := range []string{"сервер CSQTT отказал", "разные версии протокола", "другой версии"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("нет %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stdout, "переподключение") {
		t.Errorf("отказ сервера повторяли:\n%s", stdout)
	}
}

// Отказ по паролю: подсказка — по причине отказа сервера. Тексты — те, что
// ядро пишет на DENIED:<причина> (core/rust-client/protocol.rs).
func TestConnectServerRejectedAuth(t *testing.T) {
	cases := []struct {
		name, message string
		want          []string
		notWant       string
	}{
		{"wrong_password", "FATAL_AUTH: неверный пароль подключения",
			[]string{"неверный пароль подключения", "проверьте пароль подключения: csqtt-cli profile set home -password"}, "панел"},
		{"expired", "FATAL_AUTH: срок действия пароля истёк",
			[]string{"срок действия пароля истёк", "новый пароль из панели сервера", "profile set home -password"}, "проверьте"},
		{"device_mismatch", "FATAL_AUTH: пароль привязан к другому устройству",
			[]string{"привязан к другому устройству", "повторный ввод не поможет", "отдельный пароль",
				"profile set home -password", "снимите привязку"}, "проверьте"},
		{"прочее", "FATAL_AUTH: доступ запрещён (deactivated)",
			[]string{"доступ запрещён (deactivated)", "состояние в панели сервера", "profile set home -password"}, "устройств"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			te := newTestEnv(t)
			socksHome(t, te)
			te.core = "rejected:" + c.message
			code, stdout, stderr := te.run("connect")
			if code != exitError {
				t.Fatalf("код %d:\n%s\n%s", code, stdout, stderr)
			}
			for _, want := range append([]string{"сервер CSQTT отказал"}, c.want...) {
				if !strings.Contains(stderr, want) {
					t.Errorf("нет %q:\n%s", want, stderr)
				}
			}
			if strings.Contains(stderr, c.notWant) {
				t.Errorf("лишнее %q:\n%s", c.notWant, stderr)
			}
			if strings.Contains(stdout, "переподключение") {
				t.Errorf("отказ сервера повторяли:\n%s", stdout)
			}
		})
	}
}

// Причина различается по тексту ядра — без учёта регистра и «ё», и по коду
// причины, если ядро передаст его как есть.
func TestRejectHint(t *testing.T) {
	cases := map[string]string{
		"FATAL_AUTH: Пароль привязан к другому устройству": "занят другим устройством",
		"FATAL_AUTH: доступ запрещён (device_mismatch)":    "занят другим устройством",
		"FATAL_AUTH: срок действия пароля истек":           "новый пароль",
		"FATAL_AUTH: доступ запрещён (expired)":            "новый пароль",
		"FATAL_AUTH: доступ запрещён (wrong_password)":     "проверьте пароль подключения: ",
		"FATAL_AUTH: доступ запрещён (deactivated)":        "состояние в панели",
		"FATAL_PROTOCOL: разные версии протокола":          "другой версии",
		"что-то без метки":                                 "или сервер другой версии",
	}
	for msg, want := range cases {
		if got := rejectHint(msg, "home"); !strings.Contains(got, want) {
			t.Errorf("rejectHint(%q) = %q, ожидалось %q", msg, got, want)
		}
	}
}

// Все звонки недоступны — конец с номерами хешей и подсказкой; профиль не
// меняется.
func TestConnectHashesExhausted(t *testing.T) {
	te := newTestEnv(t)
	socksHome(t, te)
	te.core = "dead"
	before, _ := os.ReadFile(te.configPath())
	code, stdout, stderr := te.run("connect")
	if code != exitError {
		t.Fatalf("код %d:\n%s\n%s", code, stdout, stderr)
	}
	noSecrets(t, "вывод", stdout+stderr)
	for _, want := range []string{"хеш №1 профиля", "хеш №3 профиля"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("нет %q:\n%s", want, stdout)
		}
	}
	for _, want := range []string{"№1, №2, №3", "профиль не менялся", "profile set home -hashes"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("нет %q:\n%s", want, stderr)
		}
	}
	after, _ := os.ReadFile(te.configPath())
	if string(before) != string(after) {
		t.Error("профиль изменён")
	}
}

// Ядро упало, не начав работу, — ошибка с его журналом.
func TestConnectStartupError(t *testing.T) {
	te := newTestEnv(t)
	socksHome(t, te)
	te.core = "startup"
	code, _, stderr := te.run("connect")
	if code != exitError || !strings.Contains(stderr, "не начав работу") || !strings.Contains(stderr, "Нужны -peer") {
		t.Errorf("код %d, stderr:\n%s", code, stderr)
	}
}

// Профиль без device_id (файл написан руками) записывается заново до
// подключения: Save заводит id, и ядро не представится серверу как unknown.
func TestConnectSavesDeviceID(t *testing.T) {
	te := newTestEnv(t)
	socksHome(t, te)
	raw := strings.ReplaceAll(mustRead(t, te.configPath()), "device_id", "# device_id")
	if err := os.WriteFile(te.configPath(), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if loadTestFile(t, te).Profiles["home"].DeviceID != "" {
		t.Fatal("device_id не убран")
	}

	te.core = "rejected"
	_, stdout, _ := te.run("connect")
	if !strings.Contains(stdout, "не было device_id") {
		t.Errorf("не сказано про device_id:\n%s", stdout)
	}
	if loadTestFile(t, te).Profiles["home"].DeviceID == "" {
		t.Error("device_id не записан")
	}
}

// Секрет не читается — понятная ошибка до запуска ядра.
func TestConnectSecretUnreadable(t *testing.T) {
	te := newTestEnv(t)
	socksHome(t, te)
	te.core = "connected"
	te.store.getErr = config.ErrKeyringLocked
	code, stdout, stderr := te.run("connect")
	if code != exitError || !strings.Contains(stderr, "пароль профиля home") {
		t.Errorf("код %d, stderr:\n%s", code, stderr)
	}
	if strings.Contains(stdout, "запуск ядра") {
		t.Errorf("ядро запускалось:\n%s", stdout)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
