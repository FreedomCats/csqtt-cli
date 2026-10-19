// killswitch.go — kill-switch для datapath tun: пока туннель не поднят,
// трафик системы заблокирован.
//
// Отдельного объекта у kill-switch нет: это второй набор правил в той же
// таблице nftables (linuxsys.DNSGuard), что и защита DNS. Datapath о нём не
// знает: обёртка killSwitchGuard подменяет Disable защиты на «закрыто», а
// Enable оставляет как есть — поднятый туннель держит одна защита DNS.
//
// Исключение — капча, которая идёт в сеть мимо прокси csqtt-cli: ручной режим
// (страница в браузере пользователя) или окно, для которого прокси не
// поднялся. При «закрыто» она не откроется, поэтому на время такой капчи —
// от ссылки до ответа — правила снимаются (captchaDirect, крюк Direct моста
// капчи).
package cli

import (
	"errors"
	"net/netip"
	"sync"

	"github.com/FreedomCats/csqtt-cli/internal/datapath"
)

// killSwitchUsage — что делает kill-switch, для подсказок флагов.
const killSwitchUsage = "в режиме tun он блокирует трафик системы, пока туннель не поднят"

// errKillSwitchClosed — Enable после закрытия сессии: туннель поднимать уже
// не в чем.
var errKillSwitchClosed = errors.New("kill-switch: сессия защиты уже закрыта")

// lockdownSession — защита, которая умеет kill-switch (Lockdown):
// linuxsys.DNSGuard.
type lockdownSession interface {
	datapath.DNSGuard
	// Lockdown ставит набор «закрыто» вместо прежних правил, одним
	// переходом.
	Lockdown() error
}

// killSwitchGuard — защита DNS для datapath при включённом kill-switch.
//
// Какой набор стоит, следует из двух признаков: туннель поднят — защита DNS;
// не поднят и идёт капча мимо прокси — правил нет; иначе — «закрыто». Капча и
// туннель друг от друга не зависят: туннель может подняться посреди капчи
// (её просит одна группа воркеров, а поднимает другая) и упасть посреди неё —
// браузер пользователя при этом отрезан быть не должен.
type killSwitchGuard struct {
	session   lockdownSession
	allowLeak bool
	logf      func(string, ...any)

	mu     sync.Mutex
	locked bool // стоит «закрыто»
	up     bool // туннель поднят
	// captchas — сколько капч сейчас идёт в сеть мимо прокси. Счётчик, а не
	// признак: окно прежнего запроса может ещё закрываться, когда открылось
	// новое.
	captchas int
	// closed — сессия закрыта (shutdown): правила ушли вместе с таблицей, и
	// трогать их больше нечем. Окно капчи, брошенное по Ctrl+C, может
	// закрыться уже после этого.
	closed bool
}

// newKillSwitchGuard ставит «закрыто» сразу — до первой попытки: туннеля ещё
// нет, и трафику системы идти некуда, кроме как мимо него.
func newKillSwitchGuard(session lockdownSession, allowLeak bool, logf func(string, ...any)) (*killSwitchGuard, error) {
	if err := session.Lockdown(); err != nil {
		return nil, err
	}
	return &killSwitchGuard{session: session, allowLeak: allowLeak, logf: logf, locked: true}, nil
}

// Enable — туннель поднят: защита DNS вместо «закрыто». Отказ без
// -allow-leak возвращается — datapath откажет в Start, — а система
// остаётся закрытой. С -allow-leak туннель работает, значит, и система
// открыта: правила снимаются целиком, как без kill-switch.
func (g *killSwitchGuard) Enable(dns []netip.Addr) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return errKillSwitchClosed
	}
	err := g.session.Enable(dns)
	if err == nil {
		g.locked, g.up = false, true
		return nil
	}
	if !g.allowLeak {
		g.downLocked()
		return err
	}
	g.logf("[DNS] Внимание: %v — с -allow-leak туннель работает без защиты DNS", err)
	g.locked, g.up = false, true
	if derr := g.session.Disable(); derr != nil {
		g.logf("[DNS] %v", derr)
	}
	return nil
}

// Disable — туннеля больше нет: «закрыто» вместо защиты DNS (во время
// капчи мимо прокси — без правил). Отказ «закрыто» — не отказ datapath:
// громкая строка, правила снимаются целиком, и трафик системы, как без
// kill-switch, идёт мимо туннеля. Ошибка возвращается, только если не
// снялись и они.
func (g *killSwitchGuard) Disable() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return nil
	}
	return g.downLocked()
}

// downLocked — набор для упавшего туннеля.
func (g *killSwitchGuard) downLocked() error {
	g.up = false
	if g.captchas > 0 {
		g.locked = false
		return g.session.Disable()
	}
	return g.lockdownLocked()
}

func (g *killSwitchGuard) lockdownLocked() error {
	err := g.session.Lockdown()
	if err == nil {
		g.locked = true
		return nil
	}
	g.locked = false
	g.logf("Внимание: kill-switch не встал: %v — пока туннель не поднят, трафик системы идёт мимо него", err)
	return g.session.Disable()
}

// captchaDirect — капча пошла в сеть мимо прокси (Options.Direct моста):
// при «закрыто» правила снимаются, done возвращает их, если туннель так и не
// поднялся. При поднятом туннеле снимать нечего — браузер идёт через туннель.
func (g *killSwitchGuard) captchaDirect() (done func()) {
	g.openForCaptcha()
	var once sync.Once
	return func() { once.Do(g.closeAfterCaptcha) }
}

func (g *killSwitchGuard) openForCaptcha() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.captchas++
	if !g.locked || g.closed {
		return
	}
	if err := g.session.Disable(); err != nil {
		// Пакет правил nftables атомарен: «закрыто» осталось как было.
		g.logf("Внимание: kill-switch не снялся на время капчи: %v — страница капчи в браузере может не открыться", err)
		return
	}
	g.locked = false
	g.logf("kill-switch снят на время капчи — трафик системы идёт мимо туннеля")
}

// closeAfterCaptcha — капча кончилась (ответ, отказ или срок): если других
// нет и туннель так и не поднят, снова «закрыто».
func (g *killSwitchGuard) closeAfterCaptcha() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.captchas--
	if g.captchas > 0 || g.up || g.locked || g.closed {
		return
	}
	if err := g.lockdownLocked(); err != nil {
		g.logf("[DNS] %v", err)
	}
	if g.locked {
		g.logf("kill-switch возвращён: пока туннель не поднят, трафик системы заблокирован")
	}
}

// shutdown — сессия закрывается (dnsGuardSetup.close): дальше kill-switch
// правил не трогает и строк не пишет. Зовётся до Close сессии, под тем же
// замком, что и переходы, — переход, уже начатый, успевает закончиться.
func (g *killSwitchGuard) shutdown() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closed, g.locked = true, false
}

// directFunc — captchaDirect для Options.Direct моста капчи; nil, если
// kill-switch нет.
func (g *killSwitchGuard) directFunc() func() func() {
	if g == nil {
		return nil
	}
	return g.captchaDirect
}

// Locked — стоит ли сейчас «закрыто»; для строк супервизора.
func (g *killSwitchGuard) Locked() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.locked
}

// lockedFunc — Locked для Options.KillSwitch супервизора; nil, если
// kill-switch нет.
func (g *killSwitchGuard) lockedFunc() func() bool {
	if g == nil {
		return nil
	}
	return g.Locked
}

// BlockIPv6 передаётся защите, если она это умеет (datapath.IPv6Blocker).
func (g *killSwitchGuard) BlockIPv6(block bool) {
	if blocker, ok := g.session.(datapath.IPv6Blocker); ok {
		blocker.BlockIPv6(block)
	}
}
