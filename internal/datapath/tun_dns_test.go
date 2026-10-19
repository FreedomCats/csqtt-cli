package datapath

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"
)

// fakeDNSGuard — защита DNS без nftables, как linuxsys.DNSGuard: Enable
// заменяет список, отказ Enable снимает правила целиком. Пишет в журнал
// своего fakeAdapter и под его замком проверяет главное: DNS системы всегда
// среди тех, кого защита пускает.
type fakeDNSGuard struct {
	f  *fakeAdapter
	on bool
	// dns — кого защита пускает, если on.
	dns []netip.Addr
	// enableErr, если задан, решает, отказать ли Enable с таким списком.
	enableErr func(dns []netip.Addr) error
}

func (g *fakeDNSGuard) Enable(dns []netip.Addr) error {
	f := g.f
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, fmt.Sprint("enable ", dns))
	if !f.routed {
		f.errorf("защита DNS включена до маршрутов: %v", f.events)
	}
	if g.enableErr != nil {
		if err := g.enableErr(dns); err != nil {
			g.on, g.dns = false, nil
			f.events = append(f.events, "enable failed")
			return err
		}
	}
	for _, a := range f.sysDNS {
		if !slices.Contains(dns, a) {
			f.errorf("защита DNS не пускает %v, а DNS системы — %v: %v", dns, f.sysDNS, f.events)
		}
	}
	g.on, g.dns = true, slices.Clone(dns)
	return nil
}

func (g *fakeDNSGuard) Disable() error {
	f := g.f
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "disable")
	if f.sysDNS != nil {
		f.errorf("защита DNS снята, пока DNS системы — туннеля %v: %v", f.sysDNS, f.events)
	}
	g.on, g.dns = false, nil
	return nil
}

// setDNS — DNS системы адаптера: пустой список — прежний DNS, как у
// linuxsys.SystemDNS.Set.
func (f *fakeAdapter) setDNS(servers []netip.Addr) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, fmt.Sprint("set ", servers))
	if f.setDNSErr != nil {
		return f.setDNSErr
	}
	if !f.routed {
		f.errorf("DNS системы задан до маршрутов: %v", f.events)
	}
	if g := f.guard; g != nil {
		for _, a := range servers {
			if !g.on || !slices.Contains(g.dns, a) {
				f.errorf("DNS системы — %v, а защита пускает %v (включена %v): %v", servers, g.dns, g.on, f.events)
			}
		}
	}
	f.sysDNS = nil
	if len(servers) > 0 {
		f.sysDNS = slices.Clone(servers)
	}
	return nil
}

// revertDNS, как linuxsys.SystemDNS.Revert, без поставленного ничего не
// делает — и в журнал не пишет.
func (f *fakeAdapter) revertDNS() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sysDNS == nil {
		return nil
	}
	f.events = append(f.events, "revert")
	f.sysDNS = nil
	return nil
}

// errorf — нарушение порядка; под f.mu.
func (f *fakeAdapter) errorf(format string, args ...any) {
	if f.t == nil {
		panic(fmt.Sprintf(format, args...))
	}
	f.t.Errorf(format, args...)
}

// takeEvents — журнал с прошлого вызова.
func (f *fakeAdapter) takeEvents() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	ev := f.events
	f.events = nil
	return ev
}

// dnsTun — Tun над fakeAdapter с защитой DNS (withGuard) или без неё.
func dnsTun(t *testing.T, withGuard bool) (*Tun, *fakeAdapter, *fakeDNSGuard) {
	t.Helper()
	f := &fakeAdapter{t: t}
	w, _ := fakeTun(f, nil)
	w.opts.Logf = t.Logf
	var g *fakeDNSGuard
	if withGuard {
		g = &fakeDNSGuard{f: f}
		f.guard = g
		w.opts.DNSGuard = g
	}
	return w, f, g
}

func addrs(s ...string) []netip.Addr {
	var out []netip.Addr
	for _, a := range s {
		out = append(out, netip.MustParseAddr(a))
	}
	return out
}

func expectEvents(t *testing.T, f *fakeAdapter, step string, want ...string) {
	t.Helper()
	if got := f.takeEvents(); !slices.Equal(got, want) {
		t.Errorf("%s:\n  было  %q\n  ждали %q", step, got, want)
	}
}

// Защита DNS и DNS системы — после маршрутов, защита раньше DNS системы.
// Смена DNS на ходу — в три шага: защита пускает прежние и новые, DNS
// системы новый, защита — только новые. Suspend и Close снимают в обратном
// порядке: DNS системы, защита, маршруты (адаптер). Между попытками
// UpdateDNS только запоминает.
func TestTunDNSLifecycle(t *testing.T) {
	w, f, _ := dnsTun(t, true)
	ctx := context.Background()

	if err := w.Start(ctx, conf("192.0.2.2", "198.51.100.53")); err != nil {
		t.Fatalf("Start: %v", err)
	}
	expectEvents(t, f, "Start", "routes", "enable [198.51.100.53]", "set [198.51.100.53]")

	if err := w.UpdateDNS(addrs("198.51.100.53")); err != nil {
		t.Fatalf("UpdateDNS тем же DNS: %v", err)
	}
	expectEvents(t, f, "UpdateDNS тем же DNS")

	if err := w.UpdateDNS(addrs("198.51.100.54", "198.51.100.55")); err != nil {
		t.Fatalf("UpdateDNS: %v", err)
	}
	expectEvents(t, f, "UpdateDNS",
		"enable [198.51.100.53 198.51.100.54 198.51.100.55]",
		"set [198.51.100.54 198.51.100.55]",
		"enable [198.51.100.54 198.51.100.55]")

	if err := w.Suspend(); err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	expectEvents(t, f, "Suspend", "revert", "disable", "unroute")

	// Между попытками: только запомнить.
	if err := w.UpdateDNS(addrs("198.51.100.56")); err != nil {
		t.Fatalf("UpdateDNS между попытками: %v", err)
	}
	expectEvents(t, f, "UpdateDNS между попытками")

	if err := w.Start(ctx, conf("192.0.2.2", "198.51.100.56")); err != nil {
		t.Fatalf("Start второй попытки: %v", err)
	}
	expectEvents(t, f, "Start второй попытки", "routes", "enable [198.51.100.56]", "set [198.51.100.56]")

	// Другой адрес в той же попытке — Start поверх поднятого: DNS — тем же
	// переходом, что UpdateDNS.
	if err := w.Start(ctx, conf("192.0.2.3", "198.51.100.57")); err != nil {
		t.Fatalf("Start другим адресом: %v", err)
	}
	expectEvents(t, f, "Start другим адресом", "routes",
		"enable [198.51.100.56 198.51.100.57]", "set [198.51.100.57]", "enable [198.51.100.57]")

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	expectEvents(t, f, "Close", "revert", "disable", "close")
	if err := w.Close(); err != nil {
		t.Errorf("повторный Close: %v", err)
	}
	if err := w.Suspend(); err != nil {
		t.Errorf("Suspend после Close: %v", err)
	}
	if err := w.UpdateDNS(addrs("198.51.100.58")); !errors.Is(err, errTunClosed) {
		t.Errorf("UpdateDNS после Close: %v", err)
	}
	expectEvents(t, f, "после Close")
}

// До Start ни Close, ни Suspend защиту и DNS системы не трогают.
func TestTunDNSCloseBeforeStart(t *testing.T) {
	w, f, _ := dnsTun(t, true)
	if err := w.Suspend(); err != nil {
		t.Errorf("Suspend: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("повторный Close: %v", err)
	}
	expectEvents(t, f, "Close до Start")
}

// Резолверы IPv6 туннель не везёт — ни защите, ни системе они не
// достаются. Без резолверов IPv4 защита закрывает DNS целиком, а DNS
// системы остаётся прежним.
func TestTunDNSOnlyIPv4(t *testing.T) {
	w, f, _ := dnsTun(t, true)
	defer w.Close()
	if err := w.Start(context.Background(), conf("192.0.2.2", "2001:db8::53", "::ffff:198.51.100.53")); err != nil {
		t.Fatalf("Start: %v", err)
	}
	expectEvents(t, f, "Start", "routes", "enable [198.51.100.53]", "set [198.51.100.53]")

	if err := w.UpdateDNS(addrs("2001:db8::53")); err != nil {
		t.Fatalf("UpdateDNS: %v", err)
	}
	expectEvents(t, f, "UpdateDNS только IPv6", "set []", "enable []")
}

// Отказ защиты DNS в Start — ошибка попытки, и туннель без защиты не
// остаётся: маршруты сняты, DNS системы не тронут. Следующий Start
// проходит.
func TestTunDNSGuardFailure(t *testing.T) {
	w, f, g := dnsTun(t, true)
	defer w.Close()
	g.enableErr = func([]netip.Addr) error { return errors.New("защита DNS: правила nftables: ENOBUFS") }

	err := w.Start(context.Background(), conf("192.0.2.2", "198.51.100.53"))
	if err == nil || !strings.Contains(err.Error(), "tun: защита DNS: правила nftables: ENOBUFS") {
		t.Fatalf("Start: %v", err)
	}
	expectEvents(t, f, "Start с отказом защиты",
		"routes", "enable [198.51.100.53]", "enable failed", "disable", "unroute")

	g.enableErr = nil
	if err := w.Start(context.Background(), conf("192.0.2.2", "198.51.100.53")); err != nil {
		t.Fatalf("повторный Start: %v", err)
	}
	expectEvents(t, f, "повторный Start", "routes", "enable [198.51.100.53]", "set [198.51.100.53]")
}

// Отказ DNS системы в Start — тоже ошибка попытки с уборкой.
func TestTunSystemDNSFailure(t *testing.T) {
	w, f, _ := dnsTun(t, true)
	defer w.Close()
	f.setDNSErr = errors.New("DNS системы (перезапись /etc/resolv.conf): только чтение")

	err := w.Start(context.Background(), conf("192.0.2.2", "198.51.100.53"))
	if err == nil || !strings.Contains(err.Error(), "только чтение") {
		t.Fatalf("Start: %v", err)
	}
	expectEvents(t, f, "Start с отказом DNS системы",
		"routes", "enable [198.51.100.53]", "set [198.51.100.53]", "disable", "unroute")
	f.mu.Lock()
	routed := f.routed
	f.mu.Unlock()
	if routed {
		t.Error("маршруты остались после отказа")
	}
}

// Отказ на ходу оставляет туннель поднятым и защиту строже DNS системы:
// отказ первого Enable — прежний список ставится снова, DNS системы не
// тронут; отказ DNS системы — защита пускает и прежние, и новые. Следующий
// UpdateDNS доводит переход.
func TestTunUpdateDNSFailure(t *testing.T) {
	w, f, g := dnsTun(t, true)
	defer w.Close()
	if err := w.Start(context.Background(), conf("192.0.2.2", "198.51.100.53")); err != nil {
		t.Fatalf("Start: %v", err)
	}
	f.takeEvents()

	g.enableErr = func(dns []netip.Addr) error {
		if slices.Contains(dns, netip.MustParseAddr("198.51.100.54")) {
			return errors.New("защита DNS: правила nftables: ENOBUFS")
		}
		return nil
	}
	if err := w.UpdateDNS(addrs("198.51.100.54")); err == nil || !strings.Contains(err.Error(), "ENOBUFS") {
		t.Fatalf("UpdateDNS с отказом защиты: %v", err)
	}
	expectEvents(t, f, "UpdateDNS с отказом защиты",
		"enable [198.51.100.53 198.51.100.54]", "enable failed", "enable [198.51.100.53]")
	if !g.on || !slices.Equal(g.dns, addrs("198.51.100.53")) {
		t.Errorf("защита после отказа: включена %v, пускает %v", g.on, g.dns)
	}

	g.enableErr = nil
	f.setDNSErr = errors.New("resolvconf: занят")
	if err := w.UpdateDNS(addrs("198.51.100.54")); err == nil || !strings.Contains(err.Error(), "resolvconf: занят") {
		t.Fatalf("UpdateDNS с отказом DNS системы: %v", err)
	}
	expectEvents(t, f, "UpdateDNS с отказом DNS системы",
		"enable [198.51.100.53 198.51.100.54]", "set [198.51.100.54]")

	f.setDNSErr = nil
	if err := w.UpdateDNS(addrs("198.51.100.54")); err != nil {
		t.Fatalf("UpdateDNS после отказов: %v", err)
	}
	expectEvents(t, f, "UpdateDNS после отказов", "set [198.51.100.54]", "enable [198.51.100.54]")

	if err := w.Suspend(); err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	expectEvents(t, f, "Suspend", "revert", "disable", "unroute")
}

// Без защиты DNS (nil) DNS системы всё равно ставится и снимается.
func TestTunDNSWithoutGuard(t *testing.T) {
	w, f, _ := dnsTun(t, false)
	ctx := context.Background()
	if err := w.Start(ctx, conf("192.0.2.2", "198.51.100.53")); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := w.UpdateDNS(addrs("198.51.100.54")); err != nil {
		t.Fatalf("UpdateDNS: %v", err)
	}
	if err := w.Suspend(); err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	if err := w.Start(ctx, conf("192.0.2.2", "198.51.100.54")); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	expectEvents(t, f, "без защиты",
		"routes", "set [198.51.100.53]", "set [198.51.100.54]", "revert", "unroute",
		"routes", "set [198.51.100.54]", "revert", "close")
}

func TestTunRequiresDNSGuard(t *testing.T) {
	if !NewTun(TunOptions{}).Requires().DNSGuard {
		t.Error("tun: DNSGuard = false, а он забирает весь трафик")
	}
}
