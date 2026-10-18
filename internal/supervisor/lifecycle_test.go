package supervisor

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/FreedomCats/csqtt-cli/internal/coreproc"
	"github.com/FreedomCats/csqtt-cli/internal/datapath"
)

// Подключение по записи и остановка по ctx: линия проходится целиком, datapath
// получает TUNCONF, канал — порт ядра, ядро — Stop, а Run возвращает nil.
func TestConnectAndStop(t *testing.T) {
	h := newHarness(t, Options{}, recConnect)
	done := h.run()
	h.waitState(StateConnected)
	h.cancel()
	if err := h.result(done); err != nil {
		t.Fatalf("Run() = %v", err)
	}

	want := []State{StateIdle, StateStarting, StateCredentials, StateConfiguring, StateConnected, StateStopping}
	if got := h.states(); !slices.Equal(got, want) {
		t.Errorf("состояния %v, ожидались %v", got, want)
	}
	if got := h.tunnel.State(); got != StateStopping {
		t.Errorf("State() = %s", got)
	}

	starts, _ := h.dp.calls()
	wantConf := datapath.TunConf{
		Address: netip.MustParseAddr("192.0.2.2"),
		DNS:     []netip.Addr{netip.MustParseAddr("198.51.100.53")},
	}
	if len(starts) != 1 || starts[0].Address != wantConf.Address || !slices.Equal(starts[0].DNS, wantConf.DNS) {
		t.Errorf("datapath.Start: %v", starts)
	}
	if got := h.link.corePorts(); !slices.Equal(got, []uint16{40123, 0}) {
		t.Errorf("порты ядра у канала: %v — ожидались 40123 и сброс в 0", got)
	}

	cores := h.attempts()
	if len(cores) != 1 {
		t.Fatalf("попыток %d", len(cores))
	}
	cfg := cores[0].cfg
	if cfg.Listen != datapath.CoreListen || cfg.ListenPeer != fakeLinkAddr.String() {
		t.Errorf("канал ядра: -listen %q, -listen-peer %q", cfg.Listen, cfg.ListenPeer)
	}
	if cfg.Password != "test-password" || !slices.Equal(cfg.Hashes, []string{"hash-one-0123456789"}) {
		t.Errorf("секреты ядра: %+v", cfg)
	}
	if !cores[0].wasStopped() {
		t.Error("ядру не послали Stop")
	}
	if d := h.sleeps(); len(d) != 0 {
		t.Errorf("задержки без неудач: %v", d)
	}
}

// READY и TUNCONF ядро шлёт из разных задач, и порядок их не гарантирован.
// Туннель работает, только когда есть и воркер, и datapath.
func TestConnectedNeedsWorkerAndDatapath(t *testing.T) {
	cases := map[string][]string{
		"READY раньше TUNCONF": join(recStart, recCredentials, recReady, recConfig),
		"TUNCONF раньше READY": join(recStart, recCredentials, recConfig, recReady),
		"без PROGRESS":         join(recStart, recConfig, recReady),
	}
	for name, script := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, Options{}, script)
			done := h.run()
			h.waitState(StateConnected)
			h.cancel()
			h.result(done)
			states := h.states()
			if i := slices.Index(states, StateConnected); i < 1 || states[i-1] != StateConfiguring {
				t.Errorf("состояния %v", states)
			}
		})
	}

	// Без READY туннель не работает, даже с поднятым datapath.
	h := newHarness(t, Options{}, join(recStart, recCredentials, recConfig, []string{"wait never"}))
	done := h.run()
	h.waitState(StateConfiguring)
	h.cancel()
	h.result(done)
	if slices.Contains(h.states(), StateConnected) {
		t.Errorf("туннель «работает» без воркеров: %v", h.states())
	}
}

// Отказ сервера (неверный пароль) — конец, а не повтор: ядро остановлено,
// Run возвращает *ServerError с текстом без метки FATAL_.
func TestServerRejects(t *testing.T) {
	h := newHarness(t, Options{}, join(recStart, recCredentials, []string{
		`[ВОРКЕР #1] Ошибка: FATAL_AUTH: неверный пароль подключения`,
		fatalLine("handshake_rejected", "FATAL_AUTH: неверный пароль подключения"),
	}))
	err := h.result(h.run())

	var se *ServerError
	if !errors.As(err, &se) || !se.Rejected() {
		t.Fatalf("Run() = %v, ожидался отказ сервера", err)
	}
	if got, want := err.Error(), "сервер CSQTT отказал в подключении: неверный пароль подключения"; got != want {
		t.Errorf("текст %q, ожидался %q", got, want)
	}
	if n := len(h.attempts()); n != 1 {
		t.Errorf("попыток %d после отказа сервера", n)
	}
	if !h.attempts()[0].wasStopped() {
		t.Error("ядру не послали Stop")
	}
	stops := h.transitionsTo(StateStopping)
	if len(stops) != 1 || !errors.Is(stops[0].Cause, err) {
		t.Errorf("переход в остановку: %+v", stops)
	}
}

// Молчание сервера — повод повторить: задержки растут 1, 2, 4 секунды.
func TestServerSilentRetriesWithBackoff(t *testing.T) {
	silent := join(recStart, recCredentials, []string{
		fatalLine("handshake_timeout", "FATAL_HANDSHAKE: сервер не подтвердил конфигурацию после 3 попыток"),
	})
	h := newHarness(t, Options{}, silent, silent, silent)
	if err := h.result(h.run()); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if got, want := h.sleeps(), []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}; !slices.Equal(got, want) {
		t.Errorf("задержки %v, ожидались %v", got, want)
	}
	recs := h.transitionsTo(StateReconnecting)
	if len(recs) != 3 {
		t.Fatalf("переподключений %d", len(recs))
	}
	var se *ServerError
	if !errors.As(recs[0].Cause, &se) || se.Rejected() || recs[0].Delay != time.Second {
		t.Errorf("переподключение: %+v", recs[0])
	}
	if got := recs[0].Cause.Error(); got != "сервер не подтвердил конфигурацию после 3 попыток" {
		t.Errorf("причина %q", got)
	}
	for i, c := range h.attempts()[:3] {
		if !c.wasStopped() {
			t.Errorf("ядру попытки %d не послали Stop", i+1)
		}
	}
}

// Падение ядра — повтор. Поднявшийся туннель обнуляет задержку: после
// падения работающего ядра снова 1 секунда, а не следующая ступень.
func TestCoreCrashRetries(t *testing.T) {
	crash := join(recStart, []string{`[PANIC] Rust client task failed`, "exit 1"})
	h := newHarness(t, Options{}, crash, crash, join(recConnect, []string{"exit 1"}), crash)
	if err := h.result(h.run()); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if got, want := h.sleeps(), []time.Duration{time.Second, 2 * time.Second, time.Second, 2 * time.Second}; !slices.Equal(got, want) {
		t.Errorf("задержки %v, ожидались %v", got, want)
	}
	recs := h.transitionsTo(StateReconnecting)
	if len(recs) == 0 || recs[0].Cause == nil || !strings.Contains(recs[0].Cause.Error(), "exit status 1") {
		t.Errorf("переподключение: %+v", recs)
	}
	if h.attempts()[0].wasStopped() {
		t.Error("вышедшему ядру зачем-то послали Stop")
	}
	if got := h.link.corePorts(); slices.Index(got, 0) != 1 {
		t.Errorf("порт ядра не сброшен после выхода: %v", got)
	}
}

// Ядро вышло до PROCESS — негодные аргументы: перезапуск не поможет, Run
// возвращает *StartupError с последними строками журнала.
func TestCoreExitsBeforeStart(t *testing.T) {
	h := newHarness(t, Options{}, []string{
		`error: unexpected argument '--listen-peer' found`,
		`Usage: client [OPTIONS]`,
		"exit 2",
	})
	err := h.result(h.run())
	var se *StartupError
	if !errors.As(err, &se) {
		t.Fatalf("Run() = %v", err)
	}
	if !strings.Contains(err.Error(), "exit status 2") || !strings.Contains(err.Error(), "--listen-peer") {
		t.Errorf("текст: %v", err)
	}
	if n := len(h.attempts()); n != 1 || len(h.sleeps()) != 0 {
		t.Errorf("попыток %d, задержек %v", n, h.sleeps())
	}
}

func TestStartupErrorKeepsLastLines(t *testing.T) {
	var script []string
	for i := range 8 {
		script = append(script, strings.Repeat("x", i+1))
	}
	h := newHarness(t, Options{}, append(script, "exit 1"))
	var se *StartupError
	if err := h.result(h.run()); !errors.As(err, &se) || len(se.Log) != startupLogLines || se.Log[0] != "xxxx" {
		t.Errorf("Run() = %v", err)
	}
}

// Ядро не запускается — Run возвращает ошибку сразу.
func TestStartFailure(t *testing.T) {
	h := newHarness(t, Options{})
	h.tunnel.start = func(coreproc.Config) (Core, error) { return nil, errors.New("нет файла") }
	err := h.result(h.run())
	if err == nil || !strings.Contains(err.Error(), "запуск ядра: нет файла") {
		t.Errorf("Run() = %v", err)
	}
	if got := h.states(); !slices.Equal(got, []State{StateIdle, StateStarting, StateStopping}) {
		t.Errorf("состояния %v", got)
	}
}

// Перезапуск сервера из панели: ядро останавливается, следующая попытка — через
// 4 секунды, и это не неудача: следующая настоящая неудача снова ждёт секунду.
func TestPanelRestart(t *testing.T) {
	silent := join(recStart, []string{fatalLine("handshake_timeout", "FATAL_HANDSHAKE: сервер не подтвердил конфигурацию")})
	h := newHarness(t, Options{}, silent, silent,
		join(recConnect, []string{
			`[СЕРВЕР] Перезагрузка из панели`,
			`__CSQTT_EVENT__|SERVER_RESTART|{"source":"panel"}`,
		}),
		silent)
	if err := h.result(h.run()); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	want := []time.Duration{time.Second, 2 * time.Second, ServerRestartDelay, time.Second}
	if got := h.sleeps(); !slices.Equal(got, want) {
		t.Errorf("задержки %v, ожидались %v", got, want)
	}
	recs := h.transitionsTo(StateReconnecting)
	if len(recs) < 3 || !errors.Is(recs[2].Cause, ErrServerRestart) || recs[2].Delay != ServerRestartDelay {
		t.Errorf("переподключение после перезапуска сервера: %+v", recs)
	}
	if !h.attempts()[2].wasStopped() {
		t.Error("ядру не послали Stop после SERVER_RESTART")
	}
}

// Недоступный звонок исключается из следующих попыток; ядро с остальными
// хешами работает дальше.
func TestDeadHashExcluded(t *testing.T) {
	hashes := []string{"hash-a-0123456789", "hash-b-0123456789", "hash-c-0123456789"}
	h := newHarness(t, Options{Config: coreproc.Config{Hashes: hashes}},
		join(recStart, []string{
			`[КРЕД #2] Звонок недоступен`,
			unavailableLine("hash-b-0123456789", 951),
			unavailableLine("hash-unknown-0123", 951), // не из профиля
		}, recCredentials, recConfig, recReady, []string{"exit 1"}),
	)
	if err := h.result(h.run()); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	cores := h.attempts()
	if len(cores) != 2 {
		t.Fatalf("попыток %d", len(cores))
	}
	if cores[0].wasStopped() {
		t.Error("ядро остановлено, хотя годные хеши есть")
	}
	if got := cores[1].cfg.Hashes; !slices.Equal(got, []string{"hash-a-0123456789", "hash-c-0123456789"}) {
		t.Errorf("хеши второй попытки: %v", got)
	}
	if !slices.Contains(h.states(), StateConnected) {
		t.Errorf("туннель не поднялся на остальных хешах: %v", h.states())
	}
	logs := h.logLines()
	if len(logs) != 1 || !strings.Contains(logs[0], "ошибка VK 951") || !strings.Contains(logs[0], "годных осталось 2") {
		t.Errorf("строки цикла: %q", logs)
	}
	for _, l := range logs {
		if strings.Contains(l, "hash-b") {
			t.Errorf("строка цикла печатает хеш: %q", l)
		}
	}
	if dead := h.tunnel.Hashes().Dead(); len(dead) != 1 || dead[0] != (DeadHash{"hash-b-0123456789", 951}) {
		t.Errorf("Dead() = %v", dead)
	}
}

// Все хеши мёртвые — остановка с подсказкой, без хешей в тексте.
func TestAllHashesDead(t *testing.T) {
	hashes := []string{"hash-a-0123456789", "hash-b-0123456789"}
	h := newHarness(t, Options{Config: coreproc.Config{Hashes: hashes}},
		join(recStart, []string{
			unavailableLine("hash-a-0123456789", 951),
			unavailableLine("hash-b-0123456789", 954),
		}),
	)
	err := h.result(h.run())
	var he *HashesExhaustedError
	if !errors.As(err, &he) {
		t.Fatalf("Run() = %v", err)
	}
	if len(he.Dead) != 2 {
		t.Errorf("Dead = %v", he.Dead)
	}
	msg := err.Error()
	for _, want := range []string{"все звонки VK недоступны (2)", "951, 954", "csqtt-cli profile set"} {
		if !strings.Contains(msg, want) {
			t.Errorf("в тексте нет %q: %s", want, msg)
		}
	}
	if strings.Contains(msg, "hash-") {
		t.Errorf("текст печатает хеш: %s", msg)
	}
	cores := h.attempts()
	if len(cores) != 1 || !cores[0].wasStopped() {
		t.Fatalf("попыток %d, ядру послан Stop: %v", len(cores), cores[0].wasStopped())
	}
}

// Все хеши мёртвые в разных попытках: второй попытке достаётся только живой.
func TestAllHashesDeadAcrossAttempts(t *testing.T) {
	hashes := []string{"hash-a-0123456789", "hash-b-0123456789"}
	h := newHarness(t, Options{Config: coreproc.Config{Hashes: hashes}},
		join(recStart, []string{unavailableLine("hash-a-0123456789", 951), "exit 0"}),
		join(recStart, []string{unavailableLine("hash-b-0123456789", 951)}),
	)
	err := h.result(h.run())
	var he *HashesExhaustedError
	if !errors.As(err, &he) {
		t.Fatalf("Run() = %v", err)
	}
	if got := err.Error(); !strings.Contains(got, "ошибка VK 951:") {
		t.Errorf("повтор кода в тексте: %s", got)
	}
	cores := h.attempts()
	if len(cores) != 2 || !slices.Equal(cores[1].cfg.Hashes, []string{"hash-b-0123456789"}) {
		t.Fatalf("попытки: %d", len(cores))
	}
	if !cores[1].wasStopped() {
		t.Error("ядру без годных хешей не послали Stop")
	}
	if stops := h.transitionsTo(StateStopping); len(stops) != 1 || !errors.As(stops[0].Cause, &he) {
		t.Errorf("переход в остановку: %+v", stops)
	}
}

// Деградация и выход из неё: без воркеров и при подозрении на сеть.
func TestDegradedRecovers(t *testing.T) {
	h := newHarness(t, Options{DegradedRestart: time.Hour}, join(recConnect, []string{
		`__CSQTT_EVENT__|ACTIVE_ZERO|{}`,
		`__CSQTT_EVENT__|STATS|{"active":0,"bytes_down":500,"bytes_up":100}`,
		`__CSQTT_EVENT__|STATS|{"active":2,"bytes_down":500,"bytes_up":100}`,
		`__CSQTT_EVENT__|NETWORK_SUSPECT|{}`,
		// Воркеры числятся, но трафика нет — сеть всё ещё под подозрением.
		`__CSQTT_EVENT__|STATS|{"active":2,"bytes_down":500,"bytes_up":900}`,
		`__CSQTT_EVENT__|STATS|{"active":2,"bytes_down":800,"bytes_up":900}`,
		`__CSQTT_EVENT__|NETWORK_SUSPECT|{}`,
		`__CSQTT_EVENT__|READY|{"worker":5}`,
		"wait never",
	}))
	done := h.run()
	h.waitState(StateConnected)
	first := h.waitState(StateDegraded)
	h.waitState(StateConnected)
	second := h.waitState(StateDegraded)
	h.waitState(StateConnected)
	third := h.waitState(StateDegraded)
	h.waitState(StateConnected)
	h.cancel()
	if err := h.result(done); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if first.Cause != ErrNoWorkers || second.Cause != ErrNetworkSuspect || third.Cause != ErrNetworkSuspect {
		t.Errorf("причины: %v, %v, %v", first.Cause, second.Cause, third.Cause)
	}
	// Из деградации выводит то, что нужно: воркеры, потом трафик, потом
	// новый воркер.
	wantUp := []coreproc.Message{
		coreproc.ReadyEvent{Worker: 1},
		coreproc.StatsEvent{Active: 2, BytesUp: 100, BytesDown: 500},
		coreproc.StatsEvent{Active: 2, BytesUp: 900, BytesDown: 800},
		coreproc.ReadyEvent{Worker: 5},
	}
	if got := h.triggered(StateConnected); !slices.Equal(got, wantUp) {
		t.Errorf("в «работает» вели %v, ожидались %v", got, wantUp)
	}
	wantDown := []coreproc.Message{coreproc.ActiveZeroEvent{}, coreproc.NetworkSuspectEvent{}, coreproc.NetworkSuspectEvent{}}
	if got := h.triggered(StateDegraded); !slices.Equal(got, wantDown) {
		t.Errorf("в «без связи» вели %v, ожидались %v", got, wantDown)
	}
	if n := len(h.attempts()); n != 1 {
		t.Errorf("попыток %d: деградацию лечили перезапуском", n)
	}
	if h.tunnel.Workers() != 3 {
		t.Errorf("Workers() = %d", h.tunnel.Workers())
	}
}

// Долгая деградация — перезапуск ядра.
func TestDegradedTooLongRestarts(t *testing.T) {
	h := newHarness(t, Options{DegradedRestart: 20 * time.Millisecond},
		join(recConnect, []string{`__CSQTT_EVENT__|ACTIVE_ZERO|{}`}))
	if err := h.result(h.run()); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	recs := h.transitionsTo(StateReconnecting)
	if len(recs) != 1 || recs[0].From != StateDegraded || !strings.Contains(recs[0].Cause.Error(), "без связи дольше 20ms") {
		t.Fatalf("переподключение: %+v", recs)
	}
	if !h.attempts()[0].wasStopped() {
		t.Error("ядру не послали Stop")
	}
	// Туннель успел подняться — задержка с начала.
	if got := h.sleeps(); !slices.Equal(got, []time.Duration{time.Second}) {
		t.Errorf("задержки %v", got)
	}
}

// Деградация, из которой вышли, таймер не оставляет: перезапуска нет.
func TestDegradedTimerCancelledOnRecovery(t *testing.T) {
	h := newHarness(t, Options{DegradedRestart: 50 * time.Millisecond}, join(recConnect, []string{
		`__CSQTT_EVENT__|ACTIVE_ZERO|{}`,
		`__CSQTT_EVENT__|READY|{"worker":2}`,
		"wait never",
	}))
	done := h.run()
	h.waitState(StateDegraded)
	h.waitState(StateConnected)
	time.Sleep(150 * time.Millisecond)
	h.cancel()
	h.result(done)
	if recs := h.transitionsTo(StateReconnecting); len(recs) != 0 {
		t.Errorf("перезапуск после восстановления: %+v", recs)
	}
}

// Остановка — не деградация: закрывая сессии по Stop, ядро отчитывается об
// уходе воркеров, но «без связи» туннель от этого не становится — ни при
// остановке по ctx, ни при перезапуске ядра по SERVER_RESTART.
func TestStopIsNotDegraded(t *testing.T) {
	// Так ядро закрывает сессии: воркеры уходят по одному, потом — ноль.
	closing := []string{
		"on stop",
		`__CSQTT_EVENT__|STATS|{"active":0,"bytes_down":500,"bytes_up":100}`,
		`[КЛИЕНТ] Все сессии завершены`,
		`__CSQTT_EVENT__|ACTIVE_ZERO|{}`,
		`__CSQTT_EVENT__|READY|{"worker":2}`,
	}

	t.Run("ctx", func(t *testing.T) {
		h := newHarness(t, Options{DegradedRestart: time.Millisecond}, join(recConnect, closing))
		done := h.run()
		h.waitState(StateConnected)
		h.cancel()
		if err := h.result(done); err != nil {
			t.Fatalf("Run() = %v", err)
		}
		want := []State{StateIdle, StateStarting, StateCredentials, StateConfiguring, StateConnected, StateStopping}
		if got := h.states(); !slices.Equal(got, want) {
			t.Errorf("состояния %v, ожидались %v", got, want)
		}
		if n := len(h.attempts()); n != 1 {
			t.Errorf("попыток %d", n)
		}
		// Счётчики события ядра по-прежнему уточняют.
		if got := h.tunnel.Workers(); got != 1 {
			t.Errorf("Workers() = %d", got)
		}
	})

	t.Run("SERVER_RESTART", func(t *testing.T) {
		h := newHarness(t, Options{DegradedRestart: time.Millisecond}, join(recConnect, []string{
			`__CSQTT_EVENT__|SERVER_RESTART|{"source":"panel"}`,
		}, closing))
		if err := h.result(h.run()); err != nil {
			t.Fatalf("Run() = %v", err)
		}
		if degr := h.transitionsTo(StateDegraded); len(degr) != 0 {
			t.Errorf("«без связи» при остановке: %+v", degr)
		}
		recs := h.transitionsTo(StateReconnecting)
		if len(recs) != 1 || recs[0].From != StateConnected || !errors.Is(recs[0].Cause, ErrServerRestart) {
			t.Errorf("переподключение: %+v", recs)
		}
	})
}

// Новая попытка после остановленной снова видит деградацию: отметка Drain
// живёт одну попытку.
func TestDegradedAfterRestartedAttempt(t *testing.T) {
	h := newHarness(t, Options{DegradedRestart: time.Hour},
		join(recConnect, []string{
			`__CSQTT_EVENT__|SERVER_RESTART|{"source":"panel"}`,
			"on stop",
			`__CSQTT_EVENT__|ACTIVE_ZERO|{}`,
		}),
		join(recConnect, []string{`__CSQTT_EVENT__|ACTIVE_ZERO|{}`, "wait never"}))
	done := h.run()
	h.waitState(StateReconnecting)
	tr := h.waitState(StateDegraded)
	h.cancel()
	if err := h.result(done); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if tr.Cause != ErrNoWorkers {
		t.Errorf("причина %v", tr.Cause)
	}
	if n := len(h.transitionsTo(StateDegraded)); n != 1 {
		t.Errorf("переходов в «без связи» %d, ожидался 1", n)
	}
}

// Отказ datapath — конец попытки.
func TestDatapathStartFailure(t *testing.T) {
	h := newHarness(t, Options{}, recConnect)
	h.dp.startErr = errors.New("адрес не IPv4")
	if err := h.result(h.run()); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	recs := h.transitionsTo(StateReconnecting)
	if len(recs) != 1 || recs[0].From != StateConfiguring || !strings.Contains(recs[0].Cause.Error(), "datapath: адрес не IPv4") {
		t.Fatalf("переподключение: %+v", recs)
	}
	if slices.Contains(h.states(), StateConnected) {
		t.Errorf("туннель «работает» без datapath: %v", h.states())
	}
	if !h.attempts()[0].wasStopped() {
		t.Error("ядру не послали Stop")
	}
}

// Повторный TUNCONF: тот же — ничего, другой DNS — UpdateDNS, другой адрес —
// снова Start.
func TestRepeatedConfig(t *testing.T) {
	h := newHarness(t, Options{}, join(recConnect, []string{
		`__CSQTT_EVENT__|CONFIG|{"config":"TUNCONF:192.0.2.2:198.51.100.53"}`,
		`__CSQTT_EVENT__|CONFIG|{"config":"TUNCONF:192.0.2.2:198.51.100.54,198.51.100.55"}`,
		`__CSQTT_EVENT__|CONFIG|{"config":"TUNCONF:192.0.2.3:198.51.100.54,198.51.100.55"}`,
		`[ВОРКЕР #1] маркер`,
		"wait never",
	}))
	done := h.run()
	h.waitState(StateConnected)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if starts, _ := h.dp.calls(); len(starts) == 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	h.cancel()
	h.result(done)

	starts, dns := h.dp.calls()
	if len(starts) != 2 || starts[1].Address != netip.MustParseAddr("192.0.2.3") {
		t.Errorf("Start: %v", starts)
	}
	wantDNS := []netip.Addr{netip.MustParseAddr("198.51.100.54"), netip.MustParseAddr("198.51.100.55")}
	if len(dns) != 1 || !slices.Equal(dns[0], wantDNS) {
		t.Errorf("UpdateDNS: %v", dns)
	}
	if n := len(h.transitionsTo(StateConnected)); n != 1 {
		t.Errorf("переходов в «работает» %d", n)
	}
}

// Капча: решатель получает запрос, ядро — токен.
func TestCaptchaSolved(t *testing.T) {
	var got coreproc.CaptchaRequest
	h := newHarness(t, Options{Captcha: func(_ context.Context, req coreproc.CaptchaRequest) (string, error) {
		got = req
		return "captcha-token-1", nil
	}}, join(recStart, []string{
		`CAPTCHA_SOLVE|manual|https://id.vk.example/challenge?x=1|session-token-1`,
		"wait CAPTCHA_RESULT|captcha-token-1",
	}, recCredentials, recConfig, recReady))
	done := h.run()
	h.waitState(StateConnected)
	h.cancel()
	h.result(done)
	if got.Mode != "manual" || got.SessionToken != "session-token-1" {
		t.Errorf("запрос капчи: %+v", got)
	}
}

func TestCaptchaFailures(t *testing.T) {
	cases := []struct {
		name  string
		solve CaptchaFunc
		want  string
	}{
		{"нет решателя", nil, "CAPTCHA_RESULT|error:unsupported"},
		{"ошибка решателя", func(context.Context, coreproc.CaptchaRequest) (string, error) {
			return "", errors.New("окно\nзакрыто")
		}, "CAPTCHA_RESULT|error:окно закрыто"},
		{"пустой токен", func(context.Context, coreproc.CaptchaRequest) (string, error) {
			return "", nil
		}, "CAPTCHA_RESULT|error:empty"},
		{"причина для ядра", func(context.Context, coreproc.CaptchaRequest) (string, error) {
			return "", fmt.Errorf("окно: %w", &coreproc.CaptchaError{Reason: "closed", Err: errors.New("окно капчи закрыто")})
		}, "CAPTCHA_RESULT|error:closed"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, Options{Captcha: tt.solve}, join(recStart, []string{
				`CAPTCHA_SOLVE|manual|https://id.vk.example/challenge|session-token-1`,
				"wait CAPTCHA_RESULT|",
			}, recConfig, recReady))
			done := h.run()
			h.waitState(StateConnected)
			h.cancel()
			h.result(done)
			if cmds := h.attempts()[0].commands(); !slices.Equal(cmds, []string{tt.want}) {
				t.Errorf("команды ядру: %q", cmds)
			}
		})
	}
}

// Ядро этого ответа уже не ждёт — ему не отвечают ничем: ответ на прежний
// запрос ядро приняло бы за ответ на текущий.
func TestCaptchaNoReply(t *testing.T) {
	h := newHarness(t, Options{Captcha: func(context.Context, coreproc.CaptchaRequest) (string, error) {
		return "", fmt.Errorf("время вышло: %w", coreproc.ErrCaptchaNoReply)
	}}, join(recStart, []string{
		`CAPTCHA_SOLVE|manual|https://id.vk.example/challenge|session-token-1`,
	}, recConfig, recReady))
	done := h.run()
	h.waitState(StateConnected)
	h.cancel()
	h.result(done)
	if cmds := h.attempts()[0].commands(); len(cmds) != 0 {
		t.Errorf("команды ядру: %q", cmds)
	}
}

// Решатель, ждущий человека, не держит остановку: ctx попытки отменяется.
func TestCaptchaCancelledOnStop(t *testing.T) {
	asked := make(chan struct{})
	h := newHarness(t, Options{Captcha: func(ctx context.Context, _ coreproc.CaptchaRequest) (string, error) {
		close(asked)
		<-ctx.Done()
		return "late-token", nil
	}}, join(recStart, []string{`CAPTCHA_SOLVE|manual|https://id.vk.example/challenge|session-token-1`}))
	done := h.run()
	<-asked
	h.cancel()
	if err := h.result(done); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if cmds := h.attempts()[0].commands(); len(cmds) != 0 {
		t.Errorf("ядру после конца попытки ушло: %q", cmds)
	}
}

// Ctrl+C во время задержки обрывает её.
func TestStopDuringDelay(t *testing.T) {
	h := newHarness(t, Options{}, join(recStart, []string{"exit 1"}))
	h.tunnel.sleep = sleepCtx
	done := h.run()
	h.waitState(StateReconnecting)
	start := time.Now()
	h.cancel()
	if err := h.result(done); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Errorf("остановка в задержке заняла %v", d)
	}
	if got := h.states(); got[len(got)-1] != StateStopping || len(h.attempts()) != 1 {
		t.Errorf("состояния %v, попыток %d", got, len(h.attempts()))
	}
}

// После Run не остаётся горутин: ни капчи, ни остановки ядра, ни таймеров.
func TestNoGoroutinesLeftAfterStop(t *testing.T) {
	before := runtime.NumGoroutine()
	for range 3 {
		h := newHarness(t, Options{
			Captcha: func(ctx context.Context, _ coreproc.CaptchaRequest) (string, error) {
				<-ctx.Done()
				return "", ctx.Err()
			},
			DegradedRestart: time.Hour,
		}, join(recConnect, []string{
			`CAPTCHA_SOLVE|manual|https://id.vk.example/challenge|session-token-1`,
			`__CSQTT_EVENT__|ACTIVE_ZERO|{}`,
		}))
		done := h.run()
		h.waitState(StateDegraded)
		h.cancel()
		h.result(done)
	}
	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Errorf("горутин было %d, стало %d: цикл что-то оставил за собой", before, after)
	}
}

func TestNewTunnelChecks(t *testing.T) {
	ok := Options{
		CorePath: "csqtt-core",
		Config:   coreproc.Config{Hashes: []string{"hash-one-0123456789"}},
		Link:     &fakeLink{},
		Datapath: &fakeDatapath{},
	}
	if _, err := NewTunnel(ok); err != nil {
		t.Fatalf("NewTunnel: %v", err)
	}
	cases := map[string]func(o *Options){
		"нет ядра":     func(o *Options) { o.CorePath = "" },
		"нет канала":   func(o *Options) { o.Link = nil },
		"нет datapath": func(o *Options) { o.Datapath = nil },
		"нет хешей":    func(o *Options) { o.Config.Hashes = nil },
		"пустой хеш":   func(o *Options) { o.Config.Hashes = []string{""} },
	}
	for name, change := range cases {
		o := ok
		change(&o)
		if _, err := NewTunnel(o); err == nil {
			t.Errorf("%s: принято", name)
		}
	}
	tun, _ := NewTunnel(ok)
	if tun.opts.DegradedRestart != DefaultDegradedRestart {
		t.Errorf("DegradedRestart = %v", tun.opts.DegradedRestart)
	}
}

// Настоящие типы подходят под интерфейсы цикла.
var (
	_ Core     = (*coreproc.Process)(nil)
	_ Link     = (*datapath.Link)(nil)
	_ Datapath = (*datapath.Socks)(nil)
)

// ──────────────────────────── Ctrl+C ────────────────────────────

// Сигнала в тесте не послать — на Windows его не подделать, — но проверить
// можно то, ради чего Interrupt нужен циклу: контекст живой, пока не
// остановят, и мёртвый после.
func TestInterruptContextStops(t *testing.T) {
	ctx, stop := Interrupt(context.Background())
	if ctx.Err() != nil {
		t.Fatalf("контекст мёртв сразу после Interrupt: %v", ctx.Err())
	}
	stop()
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("контекст не отменился после stop()")
	}
	stop() // повторный вызов безвреден: он идёт из defer
}

func TestInterruptFollowsParent(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	ctx, stop := Interrupt(parent)
	defer stop()
	cancel()
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("отмена родителя не дошла")
	}
}
