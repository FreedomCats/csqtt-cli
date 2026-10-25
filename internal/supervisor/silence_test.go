package supervisor

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/FreedomCats/csqtt-cli/internal/coreproc"
)

// Молчание сервера (ServerSilentError): VK выдал креды, а от сервера ни
// TUNCONF, ни READY. Срок в тестах короткий, ядро молчит шагом "sleep".

// silenceLine — строка о молчании при восстановлении.
const silenceLine = "сервер CSQTT молчит"

// Первое подключение: сервер молчит дольше срока — конец Run с
// *ServerSilentError, ядро остановлено штатно (STOP), повтора нет.
func TestServerSilentFirstConnect(t *testing.T) {
	for _, c := range []struct {
		name string
		opts Options
		stop string
	}{
		{"хеши", Options{}, "stop"},
		// В auto_js остановка — с завершением созданного звонка.
		{"auto_js", autoJSOptions(), "finish"},
	} {
		t.Run(c.name, func(t *testing.T) {
			c.opts.ServerSilence = 50 * time.Millisecond
			h := newHarness(t, c.opts, join(recStart, recCredentials))
			err := h.result(h.run())

			var silent *ServerSilentError
			if !errors.As(err, &silent) {
				t.Fatalf("Run = %v, ожидалось *ServerSilentError; переходы:\n%s", err, h.dump())
			}
			if silent.After != 50*time.Millisecond || !strings.Contains(err.Error(), "сервер CSQTT не отвечает") {
				t.Errorf("ошибка: %+v, %q", silent, err)
			}
			cores := h.attempts()
			if len(cores) != 1 {
				t.Fatalf("попыток %d, ожидалась одна", len(cores))
			}
			if kind := cores[0].stopKind(); kind != c.stop {
				t.Errorf("ядро остановлено так: %q, ожидалось %q", kind, c.stop)
			}
			if n := len(h.transitionsTo(StateReconnecting)); n != 0 {
				t.Errorf("переподключений %d; переходы:\n%s", n, h.dump())
			}
			if trs := h.transitionsTo(StateStopping); len(trs) != 1 || !errors.As(trs[0].Cause, &silent) {
				t.Errorf("остановка: %+v", trs)
			}
		})
	}
}

// Срок идёт с кредов, а не с запуска ядра: долгий путь до кредов (вход, звонок
// в auto_js, ответ VK) в него не входит. Подъём чуть быстрее срока — без
// ошибки.
func TestServerSilenceStartsAtCredentials(t *testing.T) {
	h := newHarness(t, Options{ServerSilence: 400 * time.Millisecond},
		join(recStart, []string{"sleep 500ms"}, recCredentials, []string{"sleep 250ms"},
			recConfig, recReady, []string{recStats}))
	done := h.run()
	h.waitState(StateConnected)
	h.cancel()
	if err := h.result(done); err != nil {
		t.Fatalf("Run = %v", err)
	}
	if kind := h.attempts()[0].stopKind(); kind != "stop" {
		t.Errorf("ядро остановлено так: %q", kind)
	}
}

// Пока капчу решает человек, срок не идёт: креды одной группы уже есть, капчу
// просит следующая, и решают её дольше срока.
func TestServerSilenceSkipsCaptcha(t *testing.T) {
	h := newHarness(t, Options{
		ServerSilence: 100 * time.Millisecond,
		Captcha: func(ctx context.Context, _ coreproc.CaptchaRequest) (string, error) {
			select {
			case <-time.After(400 * time.Millisecond):
				return "captcha-token-1", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
	}, join(recStart, recCredentials, []string{
		`CAPTCHA_SOLVE|manual|https://id.vk.example/challenge|session-token-1`,
		"wait CAPTCHA_RESULT|captcha-token-1",
	}, recCredentials, recConfig, recReady, []string{recStats}))
	done := h.run()
	h.waitState(StateConnected)
	h.cancel()
	if err := h.result(done); err != nil {
		t.Fatalf("Run = %v; переходы:\n%s", err, h.dump())
	}
}

// Серия таймаутов TURN (NETWORK_SUSPECT) — не молчание сервера: срок
// начинается заново.
func TestServerSilenceRestartsOnNetworkSuspect(t *testing.T) {
	h := newHarness(t, Options{ServerSilence: 400 * time.Millisecond},
		join(recStart, recCredentials, []string{"sleep 250ms", `__CSQTT_EVENT__|NETWORK_SUSPECT|{}`, "sleep 250ms"},
			recConfig, recReady, []string{recStats}))
	done := h.run()
	h.waitState(StateConnected)
	h.cancel()
	if err := h.result(done); err != nil {
		t.Fatalf("Run = %v; переходы:\n%s", err, h.dump())
	}
}

// Ответ сервера снимает срок: TUNCONF есть, а воркер на линию не вышел —
// это уже не молчание.
func TestServerSilenceAnswered(t *testing.T) {
	h := newHarness(t, Options{ServerSilence: 50 * time.Millisecond},
		join(recStart, recCredentials, recConfig, []string{"sleep 200ms", "exit 1"}))
	done := h.run()
	h.waitState(StateReconnecting)
	h.cancel()
	if err := h.result(done); err != nil {
		t.Fatalf("Run = %v; переходы:\n%s", err, h.dump())
	}
	if h.attempts()[0].wasStopped() {
		t.Error("ядро остановили, хотя сервер ответил")
	}
}

// Туннель уже работал: молчание при восстановлении — строка в выводе, одна на
// эпизод без связи, а не конец; попытки идут дальше, ядро не останавливают.
// Снова заработавший туннель начинает новый эпизод.
func TestServerSilentWhileRecovering(t *testing.T) {
	up := join(recConnect, []string{"exit 1"})
	silent := join(recStart, recCredentials, []string{"sleep 200ms", "exit 1"})
	h := newHarness(t, Options{ServerSilence: 50 * time.Millisecond},
		up, silent, silent, up, silent)
	if err := h.result(h.run()); err != nil {
		t.Fatalf("Run = %v; переходы:\n%s", err, h.dump())
	}

	cores := h.attempts()
	if len(cores) != 6 {
		t.Fatalf("попыток %d, ожидалось 6 (5 записей и отмена); переходы:\n%s", len(cores), h.dump())
	}
	for i, c := range cores[:5] {
		if c.wasStopped() {
			t.Errorf("ядро попытки %d остановили", i+1)
		}
	}
	lines := slices.DeleteFunc(h.logLines(), func(l string) bool { return !strings.Contains(l, silenceLine) })
	if len(lines) != 2 {
		t.Fatalf("строк о молчании %d, ожидалось 2 (по одной на эпизод): %q", len(lines), h.logLines())
	}
	for _, want := range []string{"VK выдал креды TURN", "попытки продолжаются", "CSQTT "} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("нет %q: %q", want, lines[0])
		}
	}
}

// Сервер в этом Run уже отвечал, а туннель не поднялся (перезапуск из панели,
// TUNCONF без воркеров): это сервер нужной версии, и молчание следующей
// попытки — строка в выводе, а не конец.
func TestServerSilentAfterServerAnswered(t *testing.T) {
	silent := join(recStart, recCredentials, []string{"sleep 200ms", "exit 1"})
	for _, c := range []struct {
		name   string
		answer []string
	}{
		{"TUNCONF", join(recStart, recCredentials, recConfig, []string{"exit 1"})},
		{"SERVER_RESTART", join(recStart, recCredentials, []string{`__CSQTT_EVENT__|SERVER_RESTART|{"source":"panel"}`})},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, Options{ServerSilence: 50 * time.Millisecond}, c.answer, silent)
			if err := h.result(h.run()); err != nil {
				t.Fatalf("Run = %v; переходы:\n%s", err, h.dump())
			}
			if n := len(h.transitionsTo(StateConnected)); n != 0 {
				t.Fatalf("туннель поднялся %d раз — тест не о том", n)
			}
			cores := h.attempts()
			if len(cores) != 3 {
				t.Fatalf("попыток %d, ожидалось 3 (2 записи и отмена); переходы:\n%s", len(cores), h.dump())
			}
			if cores[1].wasStopped() {
				t.Error("ядро молчащей попытки остановили")
			}
			lines := slices.DeleteFunc(h.logLines(), func(l string) bool { return !strings.Contains(l, silenceLine) })
			if len(lines) != 1 {
				t.Errorf("строк о молчании %d, ожидалась одна: %q", len(lines), h.logLines())
			}
		})
	}
}
