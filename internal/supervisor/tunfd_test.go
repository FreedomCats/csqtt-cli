package supervisor

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FreedomCats/csqtt-cli/internal/coreproc"
	"github.com/FreedomCats/csqtt-cli/internal/datapath"
)

// recStartTun — запуск ядра с -tun-uds: события LISTEN в этом режиме нет.
func recStartTun(pid int) []string {
	return []string{
		fmt.Sprintf(`__CSQTT_EVENT__|PROCESS|{"pid":%d}`, pid),
		`[КЛИЕНТ] Запуск UDS-слушателя: csqtt-cli-tun-test для получения TUN FD...`,
		`[КЛИЕНТ] Воркеров: 18 | Групп: 2 | Хешей: 1`,
	}
}

// recConnectTun — от запуска с -tun-uds до работающего туннеля.
func recConnectTun(pid int) []string {
	return join(recStartTun(pid), recCredentials, recConfig, recReady, []string{recStats})
}

// tunHarness — цикл в режиме дескриптора: TunFD и datapath, живущий весь
// сеанс. suspends — сколько попыток было начато к каждому Suspend.
type tunHarness struct {
	*harness
	fd *fakeTunFD
	dp *fakePersistent

	suspendMu sync.Mutex
	suspends  []int
}

func newTunHarness(t *testing.T, opts Options, scripts ...[]string) *tunHarness {
	t.Helper()
	failed := make(chan error, 1)
	th := &tunHarness{fd: &fakeTunFD{failed: failed}, dp: &fakePersistent{fakeDatapath: &fakeDatapath{}, failed: failed}}
	opts.TunFD, opts.Datapath = th.fd, th.dp
	th.harness = newHarness(t, opts, scripts...)
	th.dp.onSuspend = func() {
		n := len(th.attempts())
		th.suspendMu.Lock()
		defer th.suspendMu.Unlock()
		th.suspends = append(th.suspends, n)
	}
	return th
}

func (th *tunHarness) suspendedAt() []int {
	th.suspendMu.Lock()
	defer th.suspendMu.Unlock()
	return append([]int(nil), th.suspends...)
}

// Каждая попытка — новое имя сокета в -tun-uds и своя передача дескриптора
// ядру этой попытки (pid из PROCESS); -listen и -listen-peer ядро не
// получает, канала на loopback нет. Между попытками маршруты снимаются
// (Suspend) — до запуска следующего ядра, а по Ctrl+C — нет: адаптер
// убирает вызывающий.
func TestTunFDEachAttempt(t *testing.T) {
	crash := join(recStartTun(101), []string{`[PANIC] Rust client task failed`, "exit 1"})
	h := newTunHarness(t, Options{}, crash, recConnectTun(202))
	done := h.run()
	h.waitState(StateConnected)
	h.cancel()
	if err := h.result(done); err != nil {
		t.Fatalf("Run() = %v", err)
	}

	cores := h.attempts()
	if len(cores) != 2 {
		t.Fatalf("попыток %d", len(cores))
	}
	for i, c := range cores {
		want := fmt.Sprintf("csqtt-cli-tun-test-%d", i+1)
		if c.cfg.TunUDS != want || c.cfg.Listen != "" || c.cfg.ListenPeer != "" {
			t.Errorf("попытка %d: -tun-uds %q, -listen %q, -listen-peer %q; ждали -tun-uds %q без канала",
				i+1, c.cfg.TunUDS, c.cfg.Listen, c.cfg.ListenPeer, want)
		}
	}
	want := []fdSend{{"csqtt-cli-tun-test-1", 101, nil}, {"csqtt-cli-tun-test-2", 202, nil}}
	if got := h.fd.calls(); !slices.Equal(got, want) {
		t.Errorf("передачи дескриптора %v, ждали %v", got, want)
	}
	if got := h.suspendedAt(); !slices.Equal(got, []int{1}) {
		t.Errorf("Suspend после попыток %v, ждали один раз — после первой", got)
	}
	if got := h.link.corePorts(); len(got) != 0 {
		t.Errorf("канал на loopback получил порты %v", got)
	}
	if starts, _ := h.dp.calls(); len(starts) != 1 {
		t.Errorf("datapath.Start: %v", starts)
	}
}

// Отказ передачи — конец попытки, а не зависание: ядро остановлено, цикл
// ждёт обычную задержку, и следующая попытка с новым сокетом подключается.
// Туннель без дескриптора «работающим» не считается, хоть TUNCONF и READY
// пришли.
func TestTunFDFailureEndsAttempt(t *testing.T) {
	h := newTunHarness(t, Options{}, recConnectTun(101), recConnectTun(202))
	h.fd.send = func(_ context.Context, socket string, _ int) error {
		if strings.HasSuffix(socket, "-1") {
			return errors.New("сокет -tun-uds слушает не ядро")
		}
		return nil
	}
	done := h.run()
	h.waitState(StateConnected)
	h.cancel()
	if err := h.result(done); err != nil {
		t.Fatalf("Run() = %v", err)
	}

	recs := h.transitionsTo(StateReconnecting)
	if len(recs) != 1 || !strings.Contains(recs[0].Cause.Error(), "дескриптор адаптера ядру: сокет -tun-uds слушает не ядро") {
		t.Fatalf("переподключение: %+v", recs)
	}
	states := h.states()
	if i, j := slices.Index(states, StateConnected), slices.Index(states, StateReconnecting); i < j {
		t.Errorf("туннель «работал» без дескриптора: %v", states)
	}
	cores := h.attempts()
	if len(cores) != 2 || !cores[0].wasStopped() {
		t.Errorf("попыток %d; первое ядро остановлено: %v", len(cores), len(cores) > 0 && cores[0].wasStopped())
	}
	if got := h.sleeps(); !slices.Equal(got, []time.Duration{time.Second}) {
		t.Errorf("задержки %v", got)
	}
	if got := h.suspendedAt(); !slices.Equal(got, []int{1}) {
		t.Errorf("Suspend после попыток %v", got)
	}
}

// Ядро, которое так и не приняло дескриптор, не держит попытку дольше срока
// передачи.
func TestTunFDTimeout(t *testing.T) {
	h := newTunHarness(t, Options{}, join(recStartTun(101), recCredentials))
	h.tunnel.fdTimeout = 50 * time.Millisecond
	h.fd.send = func(ctx context.Context, _ string, _ int) error {
		<-ctx.Done()
		return ctx.Err()
	}
	done := h.run()
	tr := h.waitState(StateReconnecting)
	if tr.Cause == nil || !errors.Is(tr.Cause, context.DeadlineExceeded) {
		t.Errorf("причина переподключения: %v", tr.Cause)
	}
	// Записей больше нет: следующая попытка отменяет Run.
	if err := h.result(done); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if !h.attempts()[0].wasStopped() {
		t.Error("ядру не послали Stop")
	}
}

// Дескриптор ушёл позже TUNCONF и READY: туннель «работает» только с ним.
func TestTunFDConnectedWaitsForHandoff(t *testing.T) {
	release := make(chan struct{})
	h := newTunHarness(t, Options{}, recConnectTun(101))
	h.fd.send = func(ctx context.Context, _ string, _ int) error {
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	done := h.run()

	// Ждём, пока цикл разберёт всю запись, включая последний STATS.
	deadline := time.Now().Add(10 * time.Second)
	for {
		h.mu.Lock()
		n := len(h.msgs)
		last := false
		if n > 0 {
			_, last = h.msgs[n-1].(coreproc.StatsEvent)
		}
		h.mu.Unlock()
		if last {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("запись не разобрана; переходы:\n%s", h.dump())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := h.tunnel.State(); got != StateConfiguring {
		t.Fatalf("до передачи дескриптора состояние %s", got)
	}
	if starts, _ := h.dp.calls(); len(starts) != 1 {
		t.Fatalf("datapath.Start: %v", starts)
	}

	close(release)
	h.waitState(StateConnected)
	h.cancel()
	if err := h.result(done); err != nil {
		t.Fatalf("Run() = %v", err)
	}
}

// Настоящий tun подходит под интерфейсы цикла.
var (
	_ Datapath            = (*datapath.Tun)(nil)
	_ datapath.Persistent = (*datapath.Tun)(nil)
	_ datapath.Failing    = (*datapath.Tun)(nil)
)

// Строка после конца попытки говорит правду о трафике системы: если туннель
// в попытке был поднят (Start прошёл) — с kill-switch трафик закрыт, без него
// идёт мимо туннеля; попытка, в которой туннель не поднимался, строк не даёт;
// Start следующей — строка, что трафик вернулся в туннель. Kill-switch
// спрашивается после каждого Suspend: снятый на время капчи — «мимо туннеля».
//
// Все попытки падают сами, а когда записи кончились, цикл останавливается:
// Start и конец попытки идут в порядке строк ядра, без гонки с Connected.
func TestTunKillSwitchLines(t *testing.T) {
	crash := []string{`[PANIC] Rust client task failed`, "exit 1"}
	const (
		blockedLine  = "туннель переподключается — трафик системы заблокирован (kill-switch)"
		bypassLine   = "маршруты сняты на время переподключения — трафик и DNS системы идут мимо туннеля"
		upAgainLine  = "туннель снова поднят — трафик системы идёт через него"
		returnedLine = "маршруты возвращены — трафик системы снова идёт через туннель"
	)
	cases := []struct {
		name string
		// locked — что ответит KillSwitch после каждого Suspend; nil —
		// kill-switch нет.
		locked []bool
		want   []string
	}{
		{"без kill-switch", nil, []string{bypassLine, returnedLine, bypassLine}},
		{"kill-switch", []bool{true, true, true}, []string{blockedLine, upAgainLine, blockedLine}},
		{"kill-switch снят на время капчи", []bool{false, true, true}, []string{bypassLine, returnedLine, blockedLine}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var opts Options
			var asked []int
			var th *tunHarness
			if tc.locked != nil {
				opts.KillSwitch = func() bool {
					n := len(th.suspendedAt())
					asked = append(asked, n)
					return tc.locked[n-1]
				}
			}
			th = newTunHarness(t, opts,
				join(recStartTun(101), recCredentials, recConfig, crash), // поднят и упал
				join(recStartTun(202), crash),                            // упал, не поднявшись
				join(recStartTun(303), recCredentials, recConfig, crash)) // снова поднят и упал
			if err := th.result(th.run()); err != nil {
				t.Fatalf("Run() = %v", err)
			}
			if got := th.suspendedAt(); !slices.Equal(got, []int{1, 2, 3}) {
				t.Fatalf("Suspend после попыток %v", got)
			}
			if tc.locked != nil && !slices.Equal(asked, []int{1, 2, 3}) {
				t.Errorf("KillSwitch спрошен после Suspend №%v, ждали после каждого", asked)
			}
			var got []string
			for _, l := range th.logLines() {
				if strings.Contains(l, "трафик") {
					got = append(got, l)
				}
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("строки о трафике:\n  было  %q\n  ждали %q", got, tc.want)
			}
		})
	}
}

// Отказ Suspend — строка о том, что снято не всё; попытка всё равно следующая.
func TestTunSuspendFailureLine(t *testing.T) {
	crash := []string{`[PANIC] Rust client task failed`, "exit 1"}
	th := newTunHarness(t, Options{}, join(recStartTun(101), recCredentials, recConfig, crash))
	th.dp.suspendErr = errors.New("tun: DNS системы: resolvconf -d: сбой")
	if err := th.result(th.run()); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if len(th.attempts()) != 2 {
		t.Errorf("попыток %d, ждали следующую после отказа Suspend", len(th.attempts()))
	}
	if !slices.ContainsFunc(th.logLines(), func(l string) bool {
		return strings.Contains(l, "снят не полностью") && strings.Contains(l, "resolvconf -d: сбой")
	}) {
		t.Errorf("нет строки об отказе Suspend: %q", th.logLines())
	}
}
