package cli

import (
	"bytes"
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/FreedomCats/csqtt-cli/internal/datapath"
)

// fakeLockdownSession — сессия с kill-switch: помнит, какой набор стоит.
type fakeLockdownSession struct {
	fakeGuardSession
	locked      bool
	lockdowns   int
	lockdownErr error
	disableErr  error
}

func (s *fakeLockdownSession) Enable(dns []netip.Addr) error {
	if err := s.fakeGuardSession.Enable(dns); err != nil {
		return err
	}
	s.locked = false
	return nil
}

func (s *fakeLockdownSession) Disable() error {
	if s.disableErr != nil {
		return s.disableErr
	}
	s.locked = false
	return s.fakeGuardSession.Disable()
}

func (s *fakeLockdownSession) Lockdown() error {
	s.lockdowns++
	if s.lockdownErr != nil {
		return s.lockdownErr
	}
	s.locked, s.enabled = true, false
	return nil
}

func setupKillSwitch(t *testing.T, session *fakeLockdownSession, allowLeak bool) (*dnsGuardSetup, *bytes.Buffer, *bytes.Buffer, error) {
	t.Helper()
	stubOpenDNSGuard(t, session, nil)
	var stdout, stderr bytes.Buffer
	setup, err := setupDNSGuard(newPrinter(&stdout, false), &stderr, allowLeak, "")
	return setup, &stdout, &stderr, err
}

// «Закрыто» ставится сразу, до первой попытки; Enable — туннель поднят,
// Disable datapath'а — снова «закрыто», а не пустая таблица.
func TestKillSwitchLocksBeforeFirstAttempt(t *testing.T) {
	session := &fakeLockdownSession{}
	setup, stdout, _, err := setupKillSwitch(t, session, false)
	if err != nil {
		t.Fatal(err)
	}
	if !session.locked || setup.killSwitch == nil || !setup.killSwitch.Locked() {
		t.Fatalf("после открытия: закрыто=%v, признак=%v", session.locked, setup.killSwitch != nil)
	}
	if !strings.Contains(stdout.String(), "kill-switch: пока туннель не поднят, трафик системы заблокирован") {
		t.Errorf("нет строки о kill-switch: %q", stdout.String())
	}
	if locked := setup.killSwitch.lockedFunc(); locked == nil || !locked() {
		t.Error("признак «закрыто» для супервизора не отдан")
	}

	if err := setup.guard.Enable(nil); err != nil {
		t.Fatal(err)
	}
	if session.locked || !session.enabled || setup.killSwitch.Locked() {
		t.Errorf("после Enable: закрыто=%v, защита DNS=%v", session.locked, session.enabled)
	}
	if err := setup.guard.Disable(); err != nil {
		t.Fatal(err)
	}
	if !session.locked || session.enabled || !setup.killSwitch.Locked() {
		t.Errorf("после Disable: закрыто=%v, защита DNS=%v", session.locked, session.enabled)
	}
	if _, ok := setup.guard.(datapath.IPv6Blocker); !ok {
		t.Errorf("%T не IPv6Blocker", setup.guard)
	}
	setup.close()
	if !session.closed {
		t.Error("сессия не закрыта")
	}
}

// Не встал до подключения: без -allow-leak — ошибка с подсказками, сессия
// закрыта; с ним — предупреждение и работа без kill-switch.
func TestKillSwitchSetupFailure(t *testing.T) {
	session := &fakeLockdownSession{lockdownErr: errors.New("правило не встало")}
	setup, _, _, err := setupKillSwitch(t, session, false)
	if err == nil || setup != nil {
		t.Fatal("подключение без kill-switch разрешено без -allow-leak")
	}
	for _, want := range []string{"kill-switch не включился: правило не встало", "-no-kill-switch", "-allow-leak"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в ошибке нет %q: %v", want, err)
		}
	}
	if !session.closed {
		t.Error("сессия не закрыта при отказе")
	}

	session = &fakeLockdownSession{lockdownErr: errors.New("правило не встало")}
	setup, _, stderr, err := setupKillSwitch(t, session, true)
	if err != nil {
		t.Fatalf("с -allow-leak отказ: %v", err)
	}
	defer setup.close()
	if setup.killSwitch != nil {
		t.Error("признак kill-switch есть, а он не встал")
	}
	if _, ok := setup.guard.(tolerantDNSGuard); !ok {
		t.Errorf("защита %T, ждали терпимую", setup.guard)
	}
	if !strings.Contains(stderr.String(), "Предупреждение: kill-switch не включился") {
		t.Errorf("нет предупреждения: %q", stderr.String())
	}
}

// Отказ защиты DNS при подъёме: без -allow-leak — отказ Start, система
// остаётся закрытой; с ним туннель работает, и система открыта целиком.
func TestKillSwitchEnableFailure(t *testing.T) {
	session := &fakeLockdownSession{}
	setup, _, _, err := setupKillSwitch(t, session, false)
	if err != nil {
		t.Fatal(err)
	}
	session.enableErr = errors.New("правило не встало")
	if err := setup.guard.Enable(nil); err == nil {
		t.Error("отказ Enable проглочен без -allow-leak")
	}
	if !session.locked || !setup.killSwitch.Locked() {
		t.Error("после отказа Enable система не закрыта")
	}

	session = &fakeLockdownSession{}
	setup, stdout, _, err := setupKillSwitch(t, session, true)
	if err != nil {
		t.Fatal(err)
	}
	session.enableErr = errors.New("правило не встало")
	if err := setup.guard.Enable(nil); err != nil {
		t.Errorf("с -allow-leak Enable = %v", err)
	}
	if session.locked || setup.killSwitch.Locked() {
		t.Error("с -allow-leak туннель поднят, а система закрыта")
	}
	if !strings.Contains(stdout.String(), "[DNS] Внимание: правило не встало") {
		t.Errorf("нет строки об отказе: %q", stdout.String())
	}
}

// «Закрыто» не встало посреди сеанса — громкая строка, правила сняты
// целиком, datapath отказа не видит.
func TestKillSwitchDisableFailure(t *testing.T) {
	session := &fakeLockdownSession{}
	setup, stdout, _, err := setupKillSwitch(t, session, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := setup.guard.Enable(nil); err != nil {
		t.Fatal(err)
	}
	session.lockdownErr = errors.New("правило не встало")
	if err := setup.guard.Disable(); err != nil {
		t.Errorf("Disable = %v, ждали nil", err)
	}
	if session.locked || session.enabled || setup.killSwitch.Locked() {
		t.Errorf("после отказа: закрыто=%v, защита DNS=%v; ждали пустую таблицу", session.locked, session.enabled)
	}
	if !strings.Contains(stdout.String(), "Внимание: kill-switch не встал: правило не встало") {
		t.Errorf("нет громкой строки: %q", stdout.String())
	}
}

// captchaSolve — капча мимо прокси так, как её ведёт мост: Direct, решение
// (during — что случилось с туннелем, пока человек решал), done. Возвращает,
// стояло ли «закрыто», пока человек решал.
func captchaSolve(ks *killSwitchGuard, session *fakeLockdownSession, during func()) (lockedInside bool) {
	done := ks.directFunc()()
	lockedInside = session.locked
	if during != nil {
		during()
	}
	done()
	done() // повторный done ничего не меняет
	return lockedInside
}

// Капча мимо прокси при «закрыто»: на время решения правил нет, после —
// снова «закрыто».
func TestKillSwitchCaptchaWindow(t *testing.T) {
	session := &fakeLockdownSession{}
	setup, stdout, _, err := setupKillSwitch(t, session, false)
	if err != nil {
		t.Fatal(err)
	}
	if captchaSolve(setup.killSwitch, session, nil) {
		t.Error("во время капчи стояло «закрыто»")
	}
	if !session.locked || !setup.killSwitch.Locked() {
		t.Error("после капчи «закрыто» не вернулось")
	}
	for _, want := range []string{"kill-switch снят на время капчи — трафик системы идёт мимо туннеля",
		"kill-switch возвращён"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("нет строки %q: %q", want, stdout.String())
		}
	}
}

// Две капчи внахлёст (окно прежнего запроса ещё закрывается, а новое уже
// открыто): «закрыто» возвращается, только когда кончились обе.
func TestKillSwitchCaptchaOverlap(t *testing.T) {
	session := &fakeLockdownSession{}
	setup, _, _, err := setupKillSwitch(t, session, false)
	if err != nil {
		t.Fatal(err)
	}
	first := setup.killSwitch.captchaDirect()
	second := setup.killSwitch.captchaDirect()
	first()
	if session.locked {
		t.Error("«закрыто» вернулось посреди второй капчи")
	}
	second()
	if !session.locked {
		t.Error("после обеих капч «закрыто» не вернулось")
	}
}

// Туннель поднят — браузер идёт через него: правила не трогаются, строк нет,
// защита DNS остаётся.
func TestKillSwitchCaptchaTunnelUp(t *testing.T) {
	session := &fakeLockdownSession{}
	setup, stdout, _, err := setupKillSwitch(t, session, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := setup.guard.Enable(nil); err != nil {
		t.Fatal(err)
	}
	lockdowns := session.lockdowns
	captchaSolve(setup.killSwitch, session, nil)
	if !session.enabled || session.locked || session.lockdowns != lockdowns {
		t.Errorf("защита DNS=%v, закрыто=%v, Lockdown звали %d раз", session.enabled, session.locked,
			session.lockdowns-lockdowns)
	}
	if strings.Contains(stdout.String(), "на время капчи") || strings.Contains(stdout.String(), "возвращён") {
		t.Errorf("строки капчи при поднятом туннеле: %q", stdout.String())
	}
}

// Туннель поднялся посреди капчи — после неё защита DNS, не «закрыто»;
// упал посреди капчи — браузер не отрезается, «закрыто» — после неё.
func TestKillSwitchCaptchaTunnelChanges(t *testing.T) {
	session := &fakeLockdownSession{}
	setup, stdout, _, err := setupKillSwitch(t, session, false)
	if err != nil {
		t.Fatal(err)
	}
	captchaSolve(setup.killSwitch, session, func() {
		if err := setup.guard.Enable(nil); err != nil {
			t.Error(err)
		}
	})
	if !session.enabled || session.locked || setup.killSwitch.Locked() {
		t.Errorf("туннель поднялся во время капчи: защита DNS=%v, закрыто=%v", session.enabled, session.locked)
	}
	if strings.Contains(stdout.String(), "возвращён") {
		t.Errorf("«закрыто» вернулось при поднятом туннеле: %q", stdout.String())
	}

	var lockedAfterDown bool
	captchaSolve(setup.killSwitch, session, func() {
		if err := setup.guard.Disable(); err != nil {
			t.Error(err)
		}
		lockedAfterDown = session.locked
	})
	if lockedAfterDown || setup.killSwitch.Locked() != session.locked {
		t.Error("туннель упал во время капчи — браузер отрезан")
	}
	if !session.locked || session.enabled {
		t.Errorf("после капчи: закрыто=%v, защита DNS=%v", session.locked, session.enabled)
	}

	// Отказ Enable посреди капчи (без -allow-leak) тоже не отрезает браузер.
	session.enableErr = errors.New("правило не встало")
	captchaSolve(setup.killSwitch, session, func() {
		if err := setup.guard.Enable(nil); err == nil {
			t.Error("отказ Enable проглочен")
		}
		lockedAfterDown = session.locked
	})
	if lockedAfterDown || !session.locked {
		t.Errorf("отказ Enable во время капчи: закрыто внутри=%v, после=%v", lockedAfterDown, session.locked)
	}
}

// Правила не снялись — громкая строка, «закрыто» остаётся; не вернулись —
// громкая строка, правила сняты целиком.
func TestKillSwitchCaptchaFailures(t *testing.T) {
	session := &fakeLockdownSession{}
	setup, stdout, _, err := setupKillSwitch(t, session, false)
	if err != nil {
		t.Fatal(err)
	}
	session.disableErr = errors.New("таблица занята")
	if inside := captchaSolve(setup.killSwitch, session, nil); !inside || !session.locked || !setup.killSwitch.Locked() {
		t.Errorf("отказ снятия: закрыто внутри=%v, после=%v", inside, session.locked)
	}
	if !strings.Contains(stdout.String(), "Внимание: kill-switch не снялся на время капчи: таблица занята") {
		t.Errorf("нет строки об отказе снятия: %q", stdout.String())
	}

	session.disableErr = nil
	captchaSolve(setup.killSwitch, session, func() { session.lockdownErr = errors.New("правило не встало") })
	if session.locked || setup.killSwitch.Locked() {
		t.Error("«закрыто» не встало, а признак стоит")
	}
	if !strings.Contains(stdout.String(), "Внимание: kill-switch не встал: правило не встало") ||
		strings.Contains(stdout.String(), "возвращён") {
		t.Errorf("строки после отказа возврата: %q", stdout.String())
	}
}

// Ctrl+C посреди капчи мимо прокси: мост возвращается, не дожидаясь окна, и
// connect закрывает таблицу раньше, чем окно. Конец такой капчи правил больше
// не трогает и ложной строки «kill-switch не встал» не пишет.
func TestKillSwitchCaptchaAfterClose(t *testing.T) {
	session := &fakeLockdownSession{}
	setup, stdout, _, err := setupKillSwitch(t, session, false)
	if err != nil {
		t.Fatal(err)
	}
	ks := setup.killSwitch
	done := ks.captchaDirect()
	if session.locked {
		t.Fatal("капча мимо прокси не сняла «закрыто»")
	}
	setup.close()
	if !session.closed {
		t.Fatal("сессия не закрыта")
	}
	session.lockdownErr = errors.New("таблица закрыта")
	lockdowns := session.lockdowns
	stdout.Reset()
	done()
	if session.lockdowns != lockdowns {
		t.Error("после закрытия сессии kill-switch снова ставил «закрыто»")
	}
	if err := ks.Disable(); err != nil {
		t.Errorf("Disable после закрытия: %v", err)
	}
	if ks.Enable(nil) == nil {
		t.Error("Enable после закрытия прошёл")
	}
	if stdout.Len() != 0 {
		t.Errorf("строки после закрытия:\n%s", stdout.String())
	}
}
