package cli

import (
	"bytes"
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/FreedomCats/csqtt-cli/internal/datapath"
)

// fakeGuardSession — сессия защиты DNS без nftables.
type fakeGuardSession struct {
	enableErr error
	enabled   bool
	closed    bool
}

func (s *fakeGuardSession) Enable([]netip.Addr) error {
	if s.enableErr != nil {
		return s.enableErr
	}
	s.enabled = true
	return nil
}
func (s *fakeGuardSession) Disable() error  { s.enabled = false; return nil }
func (s *fakeGuardSession) Lockdown() error { s.enabled = false; return nil }
func (s *fakeGuardSession) Close() error    { s.closed = true; return nil }

func stubOpenDNSGuard(t *testing.T, session dnsGuardSession, err error) {
	t.Helper()
	prev := openDNSGuard
	openDNSGuard = func() (dnsGuardSession, error) {
		if err != nil {
			return nil, err
		}
		return session, nil
	}
	t.Cleanup(func() { openDNSGuard = prev })
}

// Без -allow-leak отказ защиты — ошибка, и она называет флаг.
func TestSetupDNSGuardRefusesByDefault(t *testing.T) {
	stubOpenDNSGuard(t, nil, errors.New("nftables: операция не разрешена"))
	var stdout, stderr bytes.Buffer

	for _, tc := range []struct{ off, what, them string }{
		{"", "защита от утечки DNS и kill-switch не включились", "без них"},
		{"-no-kill-switch", "защита от утечки DNS не включилась", "без неё"},
	} {
		setup, err := setupDNSGuard(newPrinter(&stdout, false), &stderr, false, tc.off)
		if err == nil {
			t.Fatal("подключение без защиты DNS разрешено без -allow-leak")
		}
		for _, want := range []string{tc.what, "операция не разрешена", tc.them, "-allow-leak"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("в ошибке нет %q: %v", want, err)
			}
		}
		if setup != nil {
			t.Error("при отказе вернулась защита")
		}
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("при отказе вывод: %q, %q", stdout.String(), stderr.String())
	}
}

// С -allow-leak отказ — предупреждение, и datapath идёт без защиты.
func TestSetupDNSGuardAllowLeak(t *testing.T) {
	stubOpenDNSGuard(t, nil, errors.New("nftables: операция не разрешена"))
	var stdout, stderr bytes.Buffer

	setup, err := setupDNSGuard(newPrinter(&stdout, false), &stderr, true, "")
	if err != nil {
		t.Fatalf("с -allow-leak отказ: %v", err)
	}
	if setup.guard != nil || setup.killSwitch != nil {
		t.Errorf("защита %T, а сессии нет", setup.guard)
	}
	setup.close()
	if !strings.Contains(stderr.String(), "Предупреждение: защита от утечки DNS и kill-switch не включились: nftables") ||
		!strings.Contains(stderr.String(), "подключаюсь без них") {
		t.Errorf("нет предупреждения в stderr: %q", stderr.String())
	}
	// Признаков kill-switch для супервизора и моста капчи нет.
	if setup.killSwitch.lockedFunc() != nil || setup.killSwitch.directFunc() != nil {
		t.Error("у отсутствующего kill-switch есть признак или Direct")
	}
}

// Сессия открылась: datapath получает её, закрытие закрывает сессию.
func TestSetupDNSGuardOpens(t *testing.T) {
	session := &fakeGuardSession{}
	stubOpenDNSGuard(t, session, nil)
	var stdout, stderr bytes.Buffer

	setup, err := setupDNSGuard(newPrinter(&stdout, false), &stderr, false, "kill_switch = false в профиле")
	if err != nil {
		t.Fatal(err)
	}
	guard := setup.guard
	if err := guard.Enable(nil); err != nil || !session.enabled {
		t.Errorf("Enable: err=%v, включена=%v", err, session.enabled)
	}
	setup.close()
	if !session.closed {
		t.Error("сессия не закрыта")
	}
	if !strings.Contains(stdout.String(), "защита DNS: пока туннель поднят") {
		t.Errorf("нет строки о защите DNS: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "kill-switch выключен (kill_switch = false в профиле)") || setup.killSwitch != nil {
		t.Errorf("нет строки о выключенном kill-switch: %q", stdout.String())
	}

	// Без флага отказ правил доходит до datapath — тот отказывает в Start.
	session.enableErr = errors.New("правило не встало")
	if err := guard.Enable(nil); err == nil {
		t.Error("отказ Enable проглочен без -allow-leak")
	}
}

// С -allow-leak отказ правил посреди сеанса — строка, а не сбой попытки.
func TestSetupDNSGuardAllowLeakTolerantEnable(t *testing.T) {
	session := &fakeGuardSession{enableErr: errors.New("правило не встало")}
	stubOpenDNSGuard(t, session, nil)
	var stdout, stderr bytes.Buffer

	setup, err := setupDNSGuard(newPrinter(&stdout, false), &stderr, true, "-no-kill-switch")
	if err != nil {
		t.Fatal(err)
	}
	defer setup.close()
	if err := setup.guard.Enable(nil); err != nil {
		t.Errorf("с -allow-leak Enable = %v, ждали nil", err)
	}
	if !strings.Contains(stdout.String(), "[DNS] Внимание: правило не встало") {
		t.Errorf("нет предупреждения об отказе правил: %q", stdout.String())
	}
}

// blockingGuardSession — сессия, которая умеет закрывать IPv6.
type blockingGuardSession struct {
	fakeGuardSession
	blockIPv6 bool
}

func (s *blockingGuardSession) BlockIPv6(block bool) { s.blockIPv6 = block }

// Ни терпимая обёртка (-allow-leak), ни kill-switch не прячут от datapath
// умение закрыть IPv6.
func TestSetupDNSGuardBlocksIPv6(t *testing.T) {
	for _, tc := range []struct {
		allowLeak bool
		off       string
	}{{true, "-no-kill-switch"}, {false, ""}, {true, ""}} {
		session := &blockingGuardSession{}
		stubOpenDNSGuard(t, session, nil)
		var stdout, stderr bytes.Buffer
		setup, err := setupDNSGuard(newPrinter(&stdout, false), &stderr, tc.allowLeak, tc.off)
		if err != nil {
			t.Fatal(err)
		}
		blocker, ok := setup.guard.(datapath.IPv6Blocker)
		if !ok {
			t.Fatalf("-allow-leak %v, kill-switch выключен %q: %T не IPv6Blocker", tc.allowLeak, tc.off, setup.guard)
		}
		blocker.BlockIPv6(true)
		if !session.blockIPv6 {
			t.Errorf("-allow-leak %v, kill-switch выключен %q: BlockIPv6 не дошёл до сессии", tc.allowLeak, tc.off)
		}
		setup.close()
	}
}
