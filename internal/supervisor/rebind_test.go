package supervisor

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FreedomCats/csqtt-cli/internal/coreproc"
)

// recRebound — ядро приняло BIND_IF:7, как пишет настоящее (bind_if.rs).
var recRebound = []string{"wait BIND_IF:7", "[П3] Интерфейс 3 → 7: новые сокеты — через него"}

// waitCommand ждёт, пока ядру не пошлют команду cmd.
func waitCommand(t *testing.T, c *fakeCore, cmd string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !slices.Contains(c.commands(), cmd) {
		if time.Now().After(deadline) {
			t.Fatalf("ядру не послали %s; команды: %q", cmd, c.commands())
		}
		time.Sleep(time.Millisecond)
	}
}

// waitAttempts ждёт, пока не запустится n-е ядро.
func waitAttempts(t *testing.T, h *harness, n int) []*fakeCore {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(h.attempts()) < n {
		if time.Now().After(deadline) {
			t.Fatalf("попыток %d, ждали %d:\n%s", len(h.attempts()), n, h.dump())
		}
		time.Sleep(time.Millisecond)
	}
	return h.attempts()
}

// Rebind работающему ядру — BIND_IF, без перезапуска: попытка та же, ядро
// не останавливали. Две просьбы до исполнения — одна, с последним
// интерфейсом.
func TestRebindSendsBindIf(t *testing.T) {
	changed := errors.New("сеть сменилась")
	h := newHarness(t, Options{}, join(recConnect, recRebound))
	onTransition := h.tunnel.opts.OnTransition
	h.tunnel.opts.OnTransition = func(tr Transition) {
		onTransition(tr)
		if tr.To == StateConnected {
			h.tunnel.Rebind(5, changed)
			h.tunnel.Rebind(7, changed)
		}
	}
	done := h.run()
	core := waitAttempts(t, h, 1)[0]
	waitCommand(t, core, "BIND_IF:7")
	time.Sleep(50 * time.Millisecond)
	h.cancel()
	if err := h.result(done); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if n := len(h.attempts()); n != 1 {
		t.Errorf("попыток %d, ждали одну: BIND_IF не перезапускает", n)
	}
	if got := core.commands(); !slices.Equal(got, []string{"BIND_IF:7"}) {
		t.Errorf("команды ядру %q, ждали одну BIND_IF:7", got)
	}
	if h.transitionsTo(StateReconnecting) != nil {
		t.Errorf("туннель переподключался:\n%s", h.dump())
	}
	if !slices.ContainsFunc(h.logLines(), func(l string) bool { return strings.Contains(l, "интерфейс 7 без перезапуска") }) {
		t.Errorf("нет строки о переходе: %q", h.logLines())
	}
}

// Ядро отклонило BIND_IF — перезапуск без задержки с той же причиной.
func TestRebindRejectedRestarts(t *testing.T) {
	changed := errors.New("сеть сменилась")
	rejected := []string{"wait BIND_IF:", "[П3] BIND_IF:7 отклонён, сокеты остаются на интерфейсе 3: нет интерфейса с таким индексом"}
	h := newHarness(t, Options{}, join(recConnect, rejected), recConnect)
	asked := false
	onTransition := h.tunnel.opts.OnTransition
	h.tunnel.opts.OnTransition = func(tr Transition) {
		onTransition(tr)
		if tr.To == StateConnected && !asked {
			asked = true
			h.tunnel.Rebind(7, changed)
		}
	}
	done := h.run()
	h.waitState(StateConnected)
	h.waitState(StateConnected)
	h.cancel()
	if err := h.result(done); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	cores := h.attempts()
	if len(cores) != 2 || !cores[0].wasStopped() {
		t.Fatalf("попыток %d, первое ядро остановлено: %v", len(cores), len(cores) > 0 && cores[0].wasStopped())
	}
	if got := h.sleeps(); !slices.Equal(got, []time.Duration{0}) {
		t.Errorf("задержки %v, ждали одну нулевую", got)
	}
	recs := h.transitionsTo(StateReconnecting)
	if len(recs) != 1 || !errors.Is(recs[0].Cause, changed) || !strings.Contains(recs[0].Cause.Error(), "не перешло") {
		t.Errorf("переподключения: %+v", recs)
	}
}

// До PROCESS ядро команд не читает: Rebind — перезапуск, BIND_IF не
// посылается.
func TestRebindBeforeProcessRestarts(t *testing.T) {
	h := newHarness(t, Options{}, []string{"wait never"}, recConnect)
	done := h.run()
	first := waitAttempts(t, h, 1)[0]
	h.tunnel.Rebind(7, errors.New("сеть сменилась"))
	h.waitState(StateConnected)
	h.cancel()
	if err := h.result(done); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if !first.wasStopped() || len(first.commands()) != 0 {
		t.Errorf("первое ядро: остановлено %v, команды %q", first.wasStopped(), first.commands())
	}
	if got := h.sleeps(); !slices.Equal(got, []time.Duration{0}) {
		t.Errorf("задержки %v, ждали одну нулевую", got)
	}
}

// Rebind во время задержки прерывает её и сбрасывает счётчик неудач, как
// Restart.
func TestRebindDuringDelay(t *testing.T) {
	crash := join(recStart, []string{"exit 1"})
	h := newHarness(t, Options{}, crash, crash, crash, recConnect)
	slept := make(chan time.Duration, 4)
	blocked := false
	h.tunnel.sleep = func(ctx context.Context, d time.Duration) bool {
		slept <- d
		if d < 2*time.Second || blocked {
			return ctx.Err() == nil
		}
		blocked = true
		<-ctx.Done()
		return false
	}
	done := h.run()
	<-slept
	if d := <-slept; d != 2*time.Second {
		t.Fatalf("вторая задержка %v", d)
	}
	h.tunnel.Rebind(7, errors.New("сеть сменилась"))
	if d := <-slept; d != time.Second {
		t.Errorf("задержка после прерванной %v, ждали 1s: счётчик неудач не сброшен", d)
	}
	h.waitState(StateConnected)
	h.cancel()
	if err := h.result(done); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	for i, c := range h.attempts() {
		if len(c.commands()) != 0 {
			t.Errorf("ядру %d послали %q", i, c.commands())
		}
	}
}

// fakeFailing — datapath, который может сломаться насовсем
// (datapath.Failing).
type fakeFailing struct {
	*fakeDatapath
	failed chan error
}

func (d *fakeFailing) Failed() <-chan error { return d.failed }

func newFakeFailing() *fakeFailing {
	return &fakeFailing{fakeDatapath: &fakeDatapath{}, failed: make(chan error, 1)}
}

// Datapath сломался посреди попытки: ядро останавливается, Run кончается
// этой ошибкой, новых попыток нет.
func TestDatapathFailedEndsRun(t *testing.T) {
	dp := newFakeFailing()
	h := newHarness(t, Options{Datapath: dp}, recConnect, recConnect)
	done := h.run()
	h.waitState(StateConnected)
	gone := errors.New("адаптер пропал")
	dp.failed <- gone
	if err := h.result(done); !errors.Is(err, gone) {
		t.Fatalf("Run() = %v, ждали отказ datapath", err)
	}
	cores := h.attempts()
	if len(cores) != 1 || !cores[0].wasStopped() {
		t.Errorf("попыток %d, ядро остановлено: %v", len(cores), len(cores) > 0 && cores[0].wasStopped())
	}
	if h.tunnel.State() != StateStopping {
		t.Errorf("состояние %s", h.tunnel.State())
	}
}

// Datapath сломался между попытками: задержка прерывается, Run кончается
// этой ошибкой.
func TestDatapathFailedDuringDelay(t *testing.T) {
	dp := newFakeFailing()
	crash := join(recStart, []string{"exit 1"})
	h := newHarness(t, Options{Datapath: dp}, crash, recConnect)
	sleeping := make(chan struct{})
	h.tunnel.sleep = func(ctx context.Context, d time.Duration) bool {
		close(sleeping)
		<-ctx.Done()
		return false
	}
	done := h.run()
	<-sleeping
	gone := errors.New("адаптер пропал")
	dp.failed <- gone
	if err := h.result(done); !errors.Is(err, gone) {
		t.Fatalf("Run() = %v, ждали отказ datapath", err)
	}
	if n := len(h.attempts()); n != 1 {
		t.Errorf("попыток %d, ждали одну", n)
	}
}

// waitRebindTaken ждёт, пока цикл не заберёт просьбу Rebind.
func waitRebindTaken(t *testing.T, h *harness) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(h.tunnel.rebind) > 0 {
		if time.Now().After(deadline) {
			t.Fatal("цикл не забрал просьбу Rebind")
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
}

// Попытка началась, когда новый интерфейс уже стоял, и Prepare дал его
// ядру: Rebind на него же до PROCESS не перезапускает, после — не шлёт
// BIND_IF.
func TestRebindToCurrentInterface(t *testing.T) {
	prepare := func(cfg *coreproc.Config) error {
		cfg.BindIf = 7
		return nil
	}
	changed := errors.New("сеть сменилась")
	t.Run("до PROCESS", func(t *testing.T) {
		h := newHarness(t, Options{Prepare: prepare}, []string{"wait never"})
		done := h.run()
		core := waitAttempts(t, h, 1)[0]
		h.tunnel.Rebind(7, changed)
		waitRebindTaken(t, h)
		if core.wasStopped() || len(h.attempts()) != 1 {
			t.Errorf("ядро на том же интерфейсе перезапустили: попыток %d", len(h.attempts()))
		}
		h.cancel()
		if err := h.result(done); err != nil {
			t.Fatalf("Run() = %v", err)
		}
	})
	t.Run("после PROCESS", func(t *testing.T) {
		h := newHarness(t, Options{Prepare: prepare}, recConnect)
		done := h.run()
		h.waitState(StateConnected)
		h.tunnel.Rebind(7, changed)
		waitRebindTaken(t, h)
		h.cancel()
		if err := h.result(done); err != nil {
			t.Fatalf("Run() = %v", err)
		}
		if got := h.attempts()[0].commands(); len(got) != 0 || len(h.attempts()) != 1 {
			t.Errorf("попыток %d, команды ядру %q", len(h.attempts()), got)
		}
	})
}

// Datapath сломался, пока ядро останавливается по отказу сервера: Run
// кончается отказом сервера — его причину сбой адаптера не заменяет.
func TestDatapathFailedKeepsFinalCause(t *testing.T) {
	dp := newFakeFailing()
	h := newHarness(t, Options{Datapath: dp}, join(recStart, recCredentials, []string{
		fatalLine("handshake_rejected", "FATAL_AUTH: неверный пароль подключения"),
		"on stop", "hold",
	}))
	done := h.run()
	core := waitAttempts(t, h, 1)[0]
	deadline := time.Now().Add(5 * time.Second)
	for !core.wasStopped() {
		if time.Now().After(deadline) {
			t.Fatal("ядру не послали Stop")
		}
		time.Sleep(time.Millisecond)
	}
	dp.failed <- errors.New("адаптер пропал")
	for len(dp.failed) > 0 {
		time.Sleep(time.Millisecond)
	}
	if !h.abort() {
		t.Fatal("остановку не прервать")
	}
	var se *ServerError
	if err := h.result(done); !errors.As(err, &se) || !se.Rejected() {
		t.Fatalf("Run() = %v, ждали отказ сервера", err)
	}
}

// failingOnRelease — Link, на котором datapath ломается, когда попытка
// кончилась и порт ядра снят: уже после её цикла, до следующей.
type failingOnRelease struct {
	*fakeLink
	once sync.Once
	fail func()
}

func (l *failingOnRelease) SetCorePort(port uint16) {
	l.fakeLink.SetCorePort(port)
	if port == 0 {
		l.once.Do(l.fail)
	}
}

// Datapath сломался, пока попытка кончалась сама: Run кончается его
// ошибкой без снятия маршрутов и без строки о переподключении.
func TestDatapathFailedAsAttemptEnds(t *testing.T) {
	dp := newFakeFailing()
	gone := errors.New("адаптер пропал")
	h := newHarness(t, Options{Datapath: dp}, join(recConnect, []string{"exit 1"}), recConnect)
	h.tunnel.opts.Link = &failingOnRelease{fakeLink: h.link, fail: func() { dp.failed <- gone }}
	if err := h.result(h.run()); !errors.Is(err, gone) {
		t.Fatalf("Run() = %v, ждали отказ datapath", err)
	}
	if n := len(h.attempts()); n != 1 {
		t.Errorf("попыток %d, ждали одну", n)
	}
	if recs := h.transitionsTo(StateReconnecting); recs != nil {
		t.Errorf("туннель обещал переподключение: %+v", recs)
	}
	if len(h.sleeps()) != 0 {
		t.Errorf("задержки %v", h.sleeps())
	}
}
