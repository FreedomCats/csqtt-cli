package supervisor

import (
	"net/netip"
	"sync"
	"testing"

	"github.com/FreedomCats/csqtt-cli/internal/coreproc"
)

var (
	evCredentials = coreproc.ProgressEvent{Kind: "credentials"}
	evConfig      = coreproc.ConfigEvent{Address: netip.MustParseAddr("192.0.2.2")}
	evReady       = coreproc.ReadyEvent{Worker: 1}
)

// connected — автомат в StateConnected.
func connected(t *testing.T) *Machine {
	t.Helper()
	m := New()
	m.BeginAttempt()
	m.Observe(evCredentials)
	m.Observe(evConfig)
	m.Observe(evReady)
	if _, ok := m.DatapathUp(); !ok || m.State() != StateConnected {
		t.Fatalf("состояние %s", m.State())
	}
	return m
}

// Линия проходится только вперёд: поздний PROGRESS и повторный TUNCONF
// работающий туннель не откатывают.
func TestLineGoesForwardOnly(t *testing.T) {
	m := connected(t)
	for _, ev := range []coreproc.Message{evCredentials, evConfig, coreproc.ProgressEvent{Kind: "other"}} {
		if tr, moved := m.Observe(ev); moved {
			t.Errorf("%T сдвинул состояние: %+v", ev, tr)
		}
	}
	if m.State() != StateConnected {
		t.Errorf("состояние %s", m.State())
	}
}

func TestProgressOfOtherKindIgnored(t *testing.T) {
	m := New()
	m.BeginAttempt()
	if _, moved := m.Observe(coreproc.ProgressEvent{Kind: "turn"}); moved || m.State() != StateStarting {
		t.Errorf("состояние %s", m.State())
	}
}

// DatapathUp имеет смысл только после TUNCONF.
func TestDatapathUpOnlyWhenConfiguring(t *testing.T) {
	m := New()
	m.BeginAttempt()
	m.Observe(evReady)
	if _, moved := m.DatapathUp(); moved {
		t.Error("DatapathUp до TUNCONF сдвинул состояние")
	}
	m.Observe(evConfig)
	if tr, moved := m.DatapathUp(); !moved || tr.From != StateConfiguring || tr.To != StateConnected {
		t.Errorf("переход %+v", tr)
	}
}

// BeginAttempt забывает прошлую попытку: datapath и воркеров.
func TestBeginAttemptForgets(t *testing.T) {
	m := connected(t)
	m.Reconnect(nil, 0)
	m.BeginAttempt()
	m.Observe(evConfig)
	if m.State() != StateConfiguring || m.Workers() != 0 {
		t.Errorf("состояние %s, воркеров %d: прошлая попытка не забыта", m.State(), m.Workers())
	}
}

func TestBeginAttemptOnlyFromIdleOrReconnecting(t *testing.T) {
	m := connected(t)
	if _, moved := m.BeginAttempt(); moved {
		t.Error("BeginAttempt посреди попытки")
	}
	m.Reconnect(nil, 0)
	if _, moved := m.BeginAttempt(); !moved {
		t.Error("BeginAttempt из Reconnecting не сработал")
	}
	m.Stop(nil)
	if _, moved := m.BeginAttempt(); moved {
		t.Error("BeginAttempt из Stopping")
	}
}

// Вне попытки события не считаются: ядро прошлой попытки не перерисовывает
// картину.
func TestObserveOutsideAttempt(t *testing.T) {
	m := New()
	if _, moved := m.Observe(evCredentials); moved {
		t.Error("событие в Idle сдвинуло состояние")
	}
	m = connected(t)
	m.Reconnect(nil, 0)
	m.Observe(coreproc.ActiveZeroEvent{})
	m.Observe(evReady)
	if m.State() != StateReconnecting || m.Workers() != 1 {
		t.Errorf("состояние %s, воркеров %d", m.State(), m.Workers())
	}
}

func TestReconnect(t *testing.T) {
	m := New()
	if _, moved := m.Reconnect(nil, 0); moved {
		t.Error("Reconnect из Idle")
	}
	m.BeginAttempt()
	tr, moved := m.Reconnect(ErrServerRestart, ServerRestartDelay)
	if !moved || tr.From != StateStarting || tr.Cause != ErrServerRestart || tr.Delay != ServerRestartDelay {
		t.Errorf("переход %+v", tr)
	}
	if _, moved := m.Reconnect(nil, 0); moved {
		t.Error("Reconnect дважды")
	}
}

func TestStopIsTerminal(t *testing.T) {
	m := New()
	if tr, moved := m.Stop(nil); !moved || tr.From != StateIdle {
		t.Errorf("Stop из Idle: %+v", tr)
	}
	if _, moved := m.Stop(nil); moved {
		t.Error("Stop дважды")
	}
	if _, moved := m.Reconnect(nil, 0); moved {
		t.Error("Reconnect из Stopping")
	}
}

// Сообщения, не влияющие на состояние, автомат пропускает.
func TestObserveIgnoresOtherMessages(t *testing.T) {
	m := connected(t)
	for _, msg := range []coreproc.Message{
		coreproc.PIDEvent{PID: 1}, coreproc.ListenEvent{Port: 1}, coreproc.Log{Text: "x"},
		coreproc.ErrorEvent{Code: "handshake_timeout", Fatal: true}, coreproc.StoppedEvent{},
		coreproc.CallUnavailableEvent{Hash: "x", Code: 951}, coreproc.ServerRestartEvent{Source: "panel"},
		coreproc.CaptchaRequest{Mode: "manual"},
	} {
		if tr, moved := m.Observe(msg); moved {
			t.Errorf("%T сдвинул состояние: %+v", msg, tr)
		}
	}
}

func TestLabels(t *testing.T) {
	for state := range lineRank {
		if state.Label() == string(state) {
			t.Errorf("у %s нет названия", state)
		}
	}
	for _, state := range []State{StateDegraded, StateReconnecting, StateStopping} {
		if state.Label() == string(state) {
			t.Errorf("у %s нет названия", state)
		}
	}
	if State("new").Label() != "new" {
		t.Error("у неизвестного состояния не его код")
	}
}

// State читают из другой горутины, пока цикл ведёт автомат.
func TestMachineConcurrentRead(t *testing.T) {
	m := New()
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 1000 {
			_ = m.State()
			_ = m.Workers()
		}
	})
	for range 100 {
		m.BeginAttempt()
		m.Observe(evConfig)
		m.Observe(evReady)
		m.DatapathUp()
		m.Observe(coreproc.ActiveZeroEvent{})
		m.Reconnect(nil, 0)
	}
	wg.Wait()
}
