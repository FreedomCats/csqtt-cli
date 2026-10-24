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
	"github.com/FreedomCats/csqtt-cli/internal/datapath"
)

// recTunLost — ядро потеряло дескриптор адаптера и ждёт новый.
var recTunLost = []string{
	`[TUN] I/O сессия завершилась, ожидаем новый TUN FD...`,
	`__CSQTT_EVENT__|TUN_LOST|{}`,
}

const resentLine = "ядро потеряло дескриптор адаптера — передан заново"

// fakeClock — часы цикла (Tunnel.now), которые двигает тест.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(h *harness) *fakeClock {
	c := &fakeClock{now: time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)}
	h.tunnel.now = func() time.Time {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.now
	}
	return c
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// waitLogCount ждёт, пока строк цикла с sub не станет n.
func waitLogCount(t *testing.T, h *harness, sub string, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := 0
		for _, l := range h.logLines() {
			if strings.Contains(l, sub) {
				got++
			}
		}
		if got >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("строк «%s» %d, ждали %d: %q\n%s", sub, got, n, h.logLines(), h.dump())
		}
		time.Sleep(time.Millisecond)
	}
}

// wantAdapterLost — Run кончился потерей адаптера (как на Windows), без
// новой попытки и без строки о переподключении.
func wantAdapterLost(t *testing.T, h *tunHarness, err error, cause string) {
	t.Helper()
	var lost *datapath.AdapterLostError
	if !errors.As(err, &lost) || !strings.Contains(err.Error(), cause) {
		t.Fatalf("Run() = %v, ждали потерю адаптера с «%s»", err, cause)
	}
	if n := len(h.attempts()); n != 1 {
		t.Errorf("попыток %d, ждали одну", n)
	}
	if !h.attempts()[0].wasStopped() {
		t.Error("ядру не послали Stop")
	}
	if recs := h.transitionsTo(StateReconnecting); recs != nil {
		t.Errorf("туннель обещал переподключение: %+v", recs)
	}
	if got := h.fd.lostCalls(); len(got) != 1 || !strings.Contains(got[0].Error(), cause) {
		t.Errorf("Lost: %v", got)
	}
}

// Ядро потеряло дескриптор при живом адаптере: новый дубликат уходит ему
// через сокет той же попытки, с тем же pid, ядро не перезапускается. Потеря
// не раньше чем через минуту после прошлой — снова то же.
func TestTunLostResendsFD(t *testing.T) {
	h := newTunHarness(t, Options{}, join(recConnectTun(101), recTunLost, []string{"wait GO"}, recTunLost))
	clock := newFakeClock(h.harness)
	done := h.run()
	h.waitState(StateConnected)
	waitLogCount(t, h.harness, resentLine, 1)

	clock.advance(tunLostRepeat)
	if err := h.attempts()[0].Send("GO"); err != nil {
		t.Fatal(err)
	}
	waitLogCount(t, h.harness, resentLine, 2)
	h.cancel()
	if err := h.result(done); err != nil {
		t.Fatalf("Run() = %v", err)
	}

	socket := "csqtt-cli-tun-test-1"
	want := []fdSend{{socket, 101, nil}, {socket, 101, nil}, {socket, 101, nil}}
	if got := h.fd.calls(); !slices.Equal(got, want) {
		t.Errorf("передачи дескриптора %v, ждали %v", got, want)
	}
	cores := h.attempts()
	if len(cores) != 1 {
		t.Errorf("попыток %d, ждали одну", len(cores))
	}
	if got := h.fd.lostCalls(); got != nil {
		t.Errorf("Lost: %v", got)
	}
	if recs := h.transitionsTo(StateReconnecting); recs != nil {
		t.Errorf("переподключение: %+v", recs)
	}
}

// Вторая потеря меньше чем через минуту после первой — адаптер сломан: Run
// кончается потерей адаптера, второй раз дескриптор не передаётся.
func TestTunLostRepeatEndsRun(t *testing.T) {
	h := newTunHarness(t, Options{}, join(recConnectTun(101), recTunLost, []string{"wait GO"}, recTunLost))
	clock := newFakeClock(h.harness)
	done := h.run()
	waitLogCount(t, h.harness, resentLine, 1)
	clock.advance(tunLostRepeat - time.Second)
	if err := h.attempts()[0].Send("GO"); err != nil {
		t.Fatal(err)
	}
	wantAdapterLost(t, h, h.result(done), "ядро снова потеряло дескриптор адаптера, через 59 с")
	if n := len(h.fd.calls()); n != 2 {
		t.Errorf("передач дескриптора %d, ждали две — первую и одну повторную", n)
	}
}

// Адаптера нет — ядру дескриптор не передаётся, Run кончается потерей
// адаптера.
func TestTunLostAdapterGone(t *testing.T) {
	h := newTunHarness(t, Options{}, join(recConnectTun(101), recTunLost))
	gone := errors.New(`адаптера "csqtt-cli" (индекс 7) в системе больше нет`)
	h.fd.check = func() error { return gone }
	err := h.result(h.run())
	wantAdapterLost(t, h, err, "ядро потеряло дескриптор адаптера: "+gone.Error())
	if !errors.Is(err, gone) {
		t.Errorf("Run() = %v, ждали причину проверки адаптера", err)
	}
	if n := len(h.fd.calls()); n != 1 {
		t.Errorf("передач дескриптора %d, ждали только первую", n)
	}
}

// afterHandoff — ядро ждёт GO: тест шлёт его, когда первый дескриптор
// принят (Connected), чтобы передачи шли по порядку.
func afterHandoff(h *tunHarness) {
	h.t.Helper()
	h.waitState(StateConnected)
	if err := h.attempts()[0].Send("GO"); err != nil {
		h.t.Fatal(err)
	}
}

// Повторная передача не удалась — адаптер сломан.
func TestTunLostResendFails(t *testing.T) {
	h := newTunHarness(t, Options{}, join(recConnectTun(101), []string{"wait GO"}, recTunLost))
	var mu sync.Mutex
	calls := 0
	h.fd.send = func(context.Context, string, int) error {
		mu.Lock()
		defer mu.Unlock()
		if calls++; calls > 1 {
			return errors.New("ядро не ответило")
		}
		return nil
	}
	done := h.run()
	afterHandoff(h)
	wantAdapterLost(t, h, h.result(done), "новый передать не удалось: ядро не ответило")
}

// Ядро потеряло дескриптор и вышло с кодом 3, не дождавшись нового (передача
// повисла), — конец Run потерей адаптера, а не новая попытка. Тот же код
// выхода без TUN_LOST — обычная неудача попытки.
func TestTunLostCoreExit(t *testing.T) {
	t.Run("после TUN_LOST", func(t *testing.T) {
		h := newTunHarness(t, Options{}, join(recConnectTun(101), []string{"wait GO"}, recTunLost, []string{"exit 3"}),
			recConnectTun(202))
		var mu sync.Mutex
		calls := 0
		h.fd.send = func(ctx context.Context, _ string, _ int) error {
			mu.Lock()
			calls++
			n := calls
			mu.Unlock()
			if n > 1 {
				<-ctx.Done()
				return ctx.Err()
			}
			return nil
		}
		done := h.run()
		afterHandoff(h)
		err := h.result(done)
		var lost *datapath.AdapterLostError
		if !errors.As(err, &lost) || !strings.Contains(err.Error(), "не дождавшись нового дескриптора адаптера") {
			t.Fatalf("Run() = %v, ждали потерю адаптера", err)
		}
		if n := len(h.attempts()); n != 1 {
			t.Errorf("попыток %d, ждали одну", n)
		}
		if recs := h.transitionsTo(StateReconnecting); recs != nil {
			t.Errorf("туннель обещал переподключение: %+v", recs)
		}
	})
	t.Run("без TUN_LOST", func(t *testing.T) {
		h := newTunHarness(t, Options{}, join(recConnectTun(101), []string{"exit 3"}), recConnectTun(202))
		done := h.run()
		waitAttempts(t, h.harness, 2)
		h.waitState(StateConnected)
		h.cancel()
		if err := h.result(done); err != nil {
			t.Fatalf("Run() = %v", err)
		}
		if got := h.fd.lostCalls(); got != nil {
			t.Errorf("Lost: %v", got)
		}
	})
}

// В режиме -listen дескриптора у ядра нет: TUN_LOST — только сообщение
// (строка журнала с -v), ни передач, ни остановки.
func TestTunLostListenIgnored(t *testing.T) {
	h := newHarness(t, Options{}, join(recConnect, recTunLost))
	done := h.run()
	h.waitState(StateConnected)
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.mu.Lock()
		seen := slices.ContainsFunc(h.msgs, func(m coreproc.Message) bool { return m == coreproc.TunLostEvent{} })
		h.mu.Unlock()
		if seen {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("TUN_LOST не дошёл до OnMessage")
		}
		time.Sleep(time.Millisecond)
	}
	if h.attempts()[0].wasStopped() {
		t.Error("ядро остановлено по TUN_LOST")
	}
	h.cancel()
	if err := h.result(done); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if len(h.attempts()) != 1 || len(h.logLines()) != 0 {
		t.Errorf("попыток %d, строки цикла %q", len(h.attempts()), h.logLines())
	}
}
