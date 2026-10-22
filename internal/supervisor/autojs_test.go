package supervisor

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/FreedomCats/csqtt-cli/internal/coreproc"
)

// Режим auto_js (§08а): звонок создаёт ядро по токену VK, хешей нет. Строки
// «[ФАТАЛ] …» — в том виде, в каком их пишет ядро (сверено тестом
// coreproc.TestAutoJSStringsInCore); токен выдуман.

const fakeVKToken = "vk1.a.fake-token-0123456789"

func autoJSOptions() Options {
	return Options{Config: coreproc.Config{VKHashMode: coreproc.VKHashModeAutoJS, VKToken: fakeVKToken}}
}

var (
	// recTokenRejected — VK отверг токен на первом же запросе.
	recTokenRejected = []string{
		"[ФАТАЛ] API 5: User authorization failed: invalid access_token (4).",
		"exit 1",
	}
	// recVKUnreachable — запрос к VK не ушёл.
	recVKUnreachable = []string{
		"[ФАТАЛ] messages.getCallToken: HTTP-запрос: error sending request for url " +
			"(https://api.vk.ru/method/messages.getCallToken?v=5.285&client_id=6287487): client error (Connect)",
		"exit 1",
	}
)

// В auto_js хешей не нужно: цикл собирается и подключается без них, ядро
// получает токен, а по Ctrl+C — остановку с завершением звонка и строку о
// ней с подсказкой про второй Ctrl+C.
func TestAutoJSWithoutHashes(t *testing.T) {
	h := newHarness(t, autoJSOptions(), recConnect)
	done := h.run()
	h.waitState(StateConnected)
	h.cancel()
	if err := h.result(done); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	cores := h.attempts()
	if len(cores) != 1 {
		t.Fatalf("попыток %d", len(cores))
	}
	if cfg := cores[0].cfg; len(cfg.Hashes) != 0 || cfg.VKToken != fakeVKToken || !cfg.AutoJS() {
		t.Errorf("настройки ядра: хеши %v, токен передан %v, auto_js %v", cfg.Hashes, cfg.VKToken == fakeVKToken, cfg.AutoJS())
	}
	if got := cores[0].stopKind(); got != "finish" {
		t.Errorf("остановка ядра %q, ожидалась с завершением звонка", got)
	}
	logs := strings.Join(h.logLines(), "\n")
	for _, want := range []string{"завершаем звонок VK… (до 38 с", "Ctrl+C ещё раз"} {
		if !strings.Contains(logs, want) {
			t.Errorf("нет %q в строках цикла:\n%s", want, logs)
		}
	}
	if h.tunnel.Hashes().Exhausted() != true || len(h.tunnel.Hashes().Alive()) != 0 {
		t.Errorf("пул хешей в auto_js не пуст: %v", h.tunnel.Hashes().Alive())
	}
}

func TestNewTunnelAutoJS(t *testing.T) {
	opts := autoJSOptions()
	opts.CorePath, opts.Link, opts.Datapath = "csqtt-core", &fakeLink{}, &fakeDatapath{}
	if _, err := NewTunnel(opts); err != nil {
		t.Errorf("NewTunnel без хешей в auto_js: %v", err)
	}
	opts.Config.VKToken = ""
	if _, err := NewTunnel(opts); err == nil {
		t.Error("NewTunnel в auto_js без токена принят")
	}
}

// VK отверг токен — конец без повторов: и на первой попытке, и после того,
// как ядро уже работало (токен отозвали посреди сеанса).
func TestAutoJSTokenRejected(t *testing.T) {
	for name, scripts := range map[string][][]string{
		"первая попытка":    {recTokenRejected},
		"после работы":      {join(recStart, []string{"exit 1"}), recTokenRejected},
		"после подключения": {join(recConnect, []string{"exit 1"}), recTokenRejected},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, autoJSOptions(), scripts...)
			err := h.result(h.run())
			var tr *TokenRejectedError
			if !errors.As(err, &tr) || tr.Fatal.APICode != 5 {
				t.Fatalf("Run() = %v, ожидался отказ токена", err)
			}
			if !strings.Contains(err.Error(), "User authorization failed") || strings.Contains(err.Error(), fakeVKToken) {
				t.Errorf("текст %q", err)
			}
			if n := len(h.attempts()); n != len(scripts) {
				t.Errorf("попыток %d, ожидалось %d — после отказа токена повторов нет", n, len(scripts))
			}
		})
	}
}

// Сбой сети до PROCESS в auto_js — повтор и на самой первой попытке: VK мог
// просто не ответить. В режиме с хешами первая такая попытка — конец
// (TestCoreExitsBeforeStart).
func TestAutoJSNetworkRetries(t *testing.T) {
	h := newHarness(t, autoJSOptions(), recVKUnreachable, recVKUnreachable, recConnect)
	done := h.run()
	h.waitState(StateConnected)
	h.cancel()
	if err := h.result(done); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if got, want := h.sleeps(), []time.Duration{time.Second, 2 * time.Second}; !slices.Equal(got, want) {
		t.Errorf("задержки %v, ожидались %v", got, want)
	}
	recs := h.transitionsTo(StateReconnecting)
	var se *StartupError
	if len(recs) != 2 || errors.As(recs[0].Cause, &se) || !strings.Contains(recs[0].Cause.Error(), "HTTP-запрос") {
		t.Errorf("переподключения: %+v", recs)
	}
}

// Прочие ошибки API VK до PROCESS в auto_js, кроме временных, — тоже конец
// на любой попытке (*VKError): каждая попытка открывает сессии под аккаунтом,
// и повтор вслепую (Flood control) только добавит следов.
func TestAutoJSVKErrorFinal(t *testing.T) {
	flood := []string{"[ФАТАЛ] vchat.startConversation: API 9: Flood control", "exit 1"}
	for name, scripts := range map[string][][]string{
		"первая попытка": {flood},
		"после работы":   {join(recStart, []string{"exit 1"}), flood},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, autoJSOptions(), append(scripts, recConnect)...)
			err := h.result(h.run())
			var ve *VKError
			if !errors.As(err, &ve) || ve.Fatal.APICode != 9 || ve.Fatal.Method != "vchat.startConversation" {
				t.Fatalf("Run() = %v, ожидался отказ VK", err)
			}
			if got, want := err.Error(), "VK отказал: vchat.startConversation: API 9: Flood control"; got != want {
				t.Errorf("текст %q, ожидался %q", got, want)
			}
			if n := len(h.attempts()); n != len(scripts) {
				t.Errorf("попыток %d, ожидалось %d — после отказа VK повторов нет", n, len(scripts))
			}
		})
	}
}

var (
	// Временные ошибки API VK (коды 1, 6, 10): с припиской метода и без.
	recVKUnknown  = []string{"[ФАТАЛ] API 1: Unknown error occurred", "exit 1"}
	recVKTooMany  = []string{"[ФАТАЛ] vchat.startConversation: API 6: Too many requests per second", "exit 1"}
	recVKInternal = []string{"[ФАТАЛ] API 10: Internal server error", "exit 1"}
	// vkRetryDelays — паузы повторов: 2, 4, 8, 16, 32 с.
	vkRetryDelays = []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second}
)

// Временная ошибка VK до PROCESS повторяется с паузами 2, 4, 8, 16, 32 с, а
// шестая подряд — конец (*VKError), и в тексте видно, что повторы исчерпаны.
// Строка переподключения называет ответ VK и номер повтора.
func TestAutoJSTransientExhausted(t *testing.T) {
	h := newHarness(t, autoJSOptions(), recVKTooMany, recVKTooMany, recVKTooMany, recVKTooMany, recVKTooMany, recVKTooMany, recConnect)
	err := h.result(h.run())
	var ve *VKError
	if !errors.As(err, &ve) || ve.Fatal.APICode != 6 || ve.Retries != 5 {
		t.Fatalf("Run() = %v, ожидался отказ VK после повторов", err)
	}
	if got, want := err.Error(), "VK отказал 6 раз подряд, повторы исчерпаны: vchat.startConversation: API 6: Too many requests per second"; got != want {
		t.Errorf("текст %q, ожидался %q", got, want)
	}
	if n := len(h.attempts()); n != 6 {
		t.Errorf("попыток %d, ожидалось 6", n)
	}
	if got := h.sleeps(); !slices.Equal(got, vkRetryDelays) {
		t.Errorf("задержки %v, ожидались %v", got, vkRetryDelays)
	}
	recs := h.transitionsTo(StateReconnecting)
	if len(recs) != 5 {
		t.Fatalf("переподключений %d: %+v", len(recs), recs)
	}
	for i, tr := range recs {
		want := fmt.Sprintf("VK отказал: vchat.startConversation: API 6: Too many requests per second (повтор %d из 5)", i+1)
		if errors.As(tr.Cause, &ve) || tr.Cause.Error() != want || tr.Delay != vkRetryDelays[i] {
			t.Errorf("переподключение %d: %v через %v, ожидалось %q через %v", i+1, tr.Cause, tr.Delay, want, vkRetryDelays[i])
		}
	}
}

// После одной-двух временных ошибок VK ответил — цикл идёт дальше, как при
// сбое сети, только с паузами повторов VK.
func TestAutoJSTransientThenConnect(t *testing.T) {
	h := newHarness(t, autoJSOptions(), recVKUnknown, recVKInternal, recConnect)
	done := h.run()
	h.waitState(StateConnected)
	h.cancel()
	if err := h.result(done); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if got, want := h.sleeps(), vkRetryDelays[:2]; !slices.Equal(got, want) {
		t.Errorf("задержки %v, ожидались %v", got, want)
	}
	recs := h.transitionsTo(StateReconnecting)
	if len(recs) != 2 || !strings.Contains(recs[0].Cause.Error(), "API 1: Unknown error occurred") ||
		!strings.Contains(recs[1].Cause.Error(), "API 10: Internal server error") {
		t.Errorf("переподключения: %+v", recs)
	}
}

// Работающий туннель обнуляет счёт повторов: после пяти временных ошибок и
// подключения новые временные ошибки снова повторяются с начала, а не
// кончают цикл. Общий backoff после подключения тоже с начала (1 с на выход
// работавшего ядра).
func TestAutoJSTransientResetAfterConnect(t *testing.T) {
	five := [][]string{recVKTooMany, recVKUnknown, recVKInternal, recVKTooMany, recVKTooMany}
	scripts := append(slices.Clone(five), join(recConnect, []string{"exit 1"}), recVKTooMany, recVKTooMany, recVKTooMany, recConnect)
	h := newHarness(t, autoJSOptions(), scripts...)
	done := h.run()
	h.waitState(StateConnected)
	h.waitState(StateConnected)
	h.cancel()
	if err := h.result(done); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if n := len(h.attempts()); n != len(scripts) {
		t.Errorf("попыток %d, ожидалось %d", n, len(scripts))
	}
	want := slices.Concat(vkRetryDelays, []time.Duration{time.Second}, vkRetryDelays[:3])
	if got := h.sleeps(); !slices.Equal(got, want) {
		t.Errorf("задержки %v, ожидались %v", got, want)
	}
}

// Прочие отказы до PROCESS в auto_js — как в режиме с хешами: первая попытка
// — конец (*StartupError, в Fatal — разобранная причина), после работавшего
// ядра — повтор. Так и с именем сервера: ядро разрешает его до VK, и неверное
// имя на первой попытке — конец, а не вечный повтор.
func TestAutoJSOtherFatal(t *testing.T) {
	for name, c := range map[string]struct {
		line string
		kind coreproc.FatalKind
	}{
		"ответ VK":    {"[ФАТАЛ] vchat.startConversation: нет join_link", coreproc.FatalOther},
		"имя сервера": {"[ФАТАЛ] ошибка разбора пира: пустой DNS-ответ для пира", coreproc.FatalPeer},
	} {
		t.Run(name, func(t *testing.T) {
			fatal := []string{c.line, "exit 1"}
			h := newHarness(t, autoJSOptions(), fatal, recConnect)
			err := h.result(h.run())
			var se *StartupError
			if !errors.As(err, &se) || se.Fatal == nil || se.Fatal.Kind != c.kind {
				t.Fatalf("Run() = %v", err)
			}
			if n := len(h.attempts()); n != 1 {
				t.Errorf("попыток %d", n)
			}

			crash := join(recStart, []string{"exit 1"})
			h = newHarness(t, autoJSOptions(), crash, fatal, recConnect)
			done := h.run()
			h.waitState(StateConnected)
			h.cancel()
			if err := h.result(done); err != nil {
				t.Fatalf("после работавшего ядра: Run() = %v", err)
			}
			if n := len(h.attempts()); n != 3 {
				t.Errorf("попыток %d, ожидалось 3", n)
			}
		})
	}
}

// Перезапуск попытки в auto_js — тоже с завершением звонка: следующая
// попытка всё равно создаст новый. Подсказки про второй Ctrl+C тут нет:
// остановку не человек просил.
func TestAutoJSRestartFinishesCall(t *testing.T) {
	h := newHarness(t, autoJSOptions(), recConnect, recConnect)
	onTransition := h.tunnel.opts.OnTransition
	restarted := false
	h.tunnel.opts.OnTransition = func(tr Transition) {
		onTransition(tr)
		if tr.To == StateConnected && !restarted {
			restarted = true
			h.tunnel.Restart(errors.New("сеть сменилась"))
		}
	}
	done := h.run()
	deadline := time.Now().Add(10 * time.Second)
	for len(h.transitionsTo(StateConnected)) < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	h.cancel()
	if err := h.result(done); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	cores := h.attempts()
	if len(cores) < 2 {
		t.Fatalf("попыток %d", len(cores))
	}
	for i, c := range cores[:2] {
		if got := c.stopKind(); got != "finish" {
			t.Errorf("попытка %d: остановка %q, ожидалась с завершением звонка", i+1, got)
		}
	}
	logs := h.logLines()
	if len(logs) < 2 || strings.Contains(logs[0], "Ctrl+C") || !strings.Contains(logs[0], "завершаем звонок VK") {
		t.Errorf("строки цикла: %q", logs)
	}
}

// Ядро не подтвердило, что звонок завершён (*coreproc.CallLeftError), —
// человеку об этом строка.
func TestAutoJSCallLeft(t *testing.T) {
	h := newHarness(t, autoJSOptions(), recConnect)
	start := h.tunnel.start
	h.tunnel.start = func(cfg coreproc.Config) (Core, error) {
		c, err := start(cfg)
		c.(*fakeCore).stopErr = &coreproc.CallLeftError{}
		return c, err
	}
	done := h.run()
	h.waitState(StateConnected)
	h.cancel()
	if err := h.result(done); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	logs := strings.Join(h.logLines(), "\n")
	if !strings.Contains(logs, "мог остаться в аккаунте, пока VK не закроет его сам") {
		t.Errorf("о звонке не сказано:\n%s", logs)
	}
}

// Второй Ctrl+C во время остановки убивает ядро, не дожидаясь уборки: ядро,
// которое по Stop не выходит (в auto_js оно создаёт звонок или ждёт VK),
// убито, Run возвращается сразу, звонок назван оставшимся. В режиме с хешами
// — то же, только без строк о звонке.
func TestAbortKillsStoppingCore(t *testing.T) {
	for name, opts := range map[string]Options{"auto_js": autoJSOptions(), "с хешами": {}} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, opts, join(recStart, recCredentials, []string{"hold"}))
			done := h.run()
			h.waitState(StateCredentials)
			h.cancel()
			deadline := time.Now().Add(5 * time.Second)
			for !h.attempts()[0].wasStopped() && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			select {
			case err := <-done:
				t.Fatalf("Run вернулся, не дождавшись ядра: %v", err)
			case <-time.After(100 * time.Millisecond):
			}
			start := time.Now()
			if !h.abort() {
				t.Fatal("abort: прерывать некому")
			}
			if err := h.result(done); err != nil {
				t.Fatalf("Run() = %v", err)
			}
			if d := time.Since(start); d > 2*time.Second {
				t.Errorf("после abort Run шёл ещё %v", d)
			}
			kind := h.attempts()[0].stopKind()
			logs := strings.Join(h.logLines(), "\n")
			if opts.Config.AutoJS() {
				if kind != "finish+kill" || !strings.Contains(logs, "мог остаться в аккаунте") {
					t.Errorf("остановка %q, строки:\n%s", kind, logs)
				}
			} else if kind != "stop+kill" || logs != "" {
				t.Errorf("остановка %q, строки:\n%s", kind, logs)
			}
			// После Run прерывать нечего.
			if h.abort() {
				t.Error("abort после Run всё ещё что-то прерывает")
			}
		})
	}
}

// CALL_UNAVAILABLE в auto_js ядро не шлёт; если пришёл — без хеша, и
// исключать нечего: туннель живёт дальше.
func TestAutoJSCallUnavailableIgnored(t *testing.T) {
	h := newHarness(t, autoJSOptions(), join(recConnect, []string{unavailableLine("", 954)}))
	done := h.run()
	h.waitState(StateConnected)
	time.Sleep(50 * time.Millisecond)
	if st := h.tunnel.State(); st != StateConnected {
		t.Errorf("состояние %s после CALL_UNAVAILABLE", st)
	}
	h.cancel()
	if err := h.result(done); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if len(h.logLines()) != 1 {
		t.Errorf("строки цикла: %q", h.logLines())
	}
}

// Режим с хешами не изменился: обычный Stop, без строк о звонке.
func TestManualStopUnchanged(t *testing.T) {
	h := newHarness(t, Options{}, recConnect)
	done := h.run()
	h.waitState(StateConnected)
	h.cancel()
	if err := h.result(done); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if got := h.attempts()[0].stopKind(); got != "stop" {
		t.Errorf("остановка %q", got)
	}
	if logs := h.logLines(); len(logs) != 0 {
		t.Errorf("строки цикла: %q", logs)
	}
}
