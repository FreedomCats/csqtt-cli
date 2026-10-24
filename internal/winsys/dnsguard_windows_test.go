package winsys

import (
	"net/netip"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tailscale/wf"
	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// Правила — без WFP: на каждом из четырёх слоёв запрет порта 53 по UDP
// и TCP легче обоих разрешений, своему процессу и резолверам туннеля
// своего семейства с loopback.
func TestDNSGuardRules(t *testing.T) {
	dns := []netip.Addr{
		netip.MustParseAddr("10.8.0.1"),
		netip.MustParseAddr("::ffff:1.1.1.1"), // IPv4 в IPv6-записи — к IPv4
		netip.MustParseAddr("fd00::1"),
	}
	rules, err := dnsGuardRules(dns, "app-id")
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 3*len(dnsGuardLayers) {
		t.Fatalf("правил %d, ждали %d", len(rules), 3*len(dnsGuardLayers))
	}

	ids := map[wf.RuleID]bool{}
	for i, layer := range dnsGuardLayers {
		block, self, allowed := rules[3*i], rules[3*i+1], rules[3*i+2]
		for _, r := range []*wf.Rule{block, self, allowed} {
			if r.Layer != layer.id || r.Sublayer != dnsGuardSublayer || r.Provider != DNSGuardProvider {
				t.Errorf("%s: правило %q не на своём слое, подслое или провайдере", layer.name, r.Name)
			}
			if ids[r.ID] || r.ID.IsZero() {
				t.Errorf("%s: GUID правила %q пустой или повторён", layer.name, r.Name)
			}
			ids[r.ID] = true
		}

		if block.Action != wf.ActionBlock || block.Weight >= self.Weight || block.Weight >= allowed.Weight {
			t.Errorf("%s: запрет %v с весом %d, разрешения %d и %d — запрет должен быть легче",
				layer.name, block.Action, block.Weight, self.Weight, allowed.Weight)
		}
		wantBlock := []wf.Match{
			{Field: wf.FieldIPRemotePort, Op: wf.MatchTypeEqual, Value: uint16(53)},
			{Field: wf.FieldIPProtocol, Op: wf.MatchTypeEqual, Value: wf.IPProtoUDP},
			{Field: wf.FieldIPProtocol, Op: wf.MatchTypeEqual, Value: wf.IPProtoTCP},
		}
		checkMatches(t, layer.name+": запрет", block.Conditions, wantBlock)

		if self.Action != wf.ActionPermit {
			t.Errorf("%s: правило своего процесса — %v", layer.name, self.Action)
		}
		checkMatches(t, layer.name+": свой процесс", self.Conditions, []wf.Match{
			{Field: wf.FieldALEAppID, Op: wf.MatchTypeEqual, Value: "app-id"},
		})

		if allowed.Action != wf.ActionPermit {
			t.Errorf("%s: правило резолверов — %v", layer.name, allowed.Action)
		}
		var want []wf.Match
		if layer.ipv6 {
			want = []wf.Match{
				{Field: wf.FieldIPRemoteAddress, Op: wf.MatchTypeEqual, Value: netip.IPv6Loopback()},
				{Field: wf.FieldIPRemoteAddress, Op: wf.MatchTypeEqual, Value: netip.MustParseAddr("fd00::1")},
			}
		} else {
			want = []wf.Match{
				{Field: wf.FieldIPRemoteAddress, Op: wf.MatchTypeEqual, Value: netip.MustParsePrefix("127.0.0.0/8")},
				{Field: wf.FieldIPRemoteAddress, Op: wf.MatchTypeEqual, Value: netip.MustParseAddr("10.8.0.1")},
				{Field: wf.FieldIPRemoteAddress, Op: wf.MatchTypeEqual, Value: netip.MustParseAddr("1.1.1.1")},
			}
		}
		checkMatches(t, layer.name+": резолверы", allowed.Conditions, want)
	}
}

func checkMatches(t *testing.T, what string, got []*wf.Match, want []wf.Match) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: условий %d, ждали %d: %v", what, len(got), len(want), got)
		return
	}
	for i := range want {
		if *got[i] != want[i] {
			t.Errorf("%s: условие %d = %v, ждали %v", what, i, got[i], want[i])
		}
	}
}

// Без прав администратора сессия не открывается — и это ошибка, а не
// паника. С правами — полный круг на настоящем WFP: Enable, повторный
// Enable, Disable, Close.
func TestDNSGuardSession(t *testing.T) {
	g, err := OpenDNSGuard()
	if err != nil {
		t.Skipf("сессия WFP не открылась (нужны права администратора): %v", err)
	}
	dns := []netip.Addr{netip.MustParseAddr("1.1.1.1")}
	if err := g.Enable(dns); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if err := g.Enable(dns); err != nil {
		t.Fatalf("повторный Enable: %v", err)
	}
	if n := len(g.rules); n != 3*len(dnsGuardLayers) {
		t.Errorf("после двух Enable правил %d, ждали %d", n, 3*len(dnsGuardLayers))
	}
	if err := g.Disable(); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if err := g.Disable(); err != nil {
		t.Fatalf("повторный Disable: %v", err)
	}
	if err := g.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := g.Enable(dns); err == nil {
		t.Error("Enable после Close принят")
	}
}

// Правила ставятся, пока идёт сборка мусора. У tailscale/wf арена отдавала
// невыровненную память, и запись структуры с указателями во время разметки
// роняла процесс: fatal error «bulkBarrierPreWrite: unaligned arguments»,
// recover её не ловит (найдено живым прогоном, на первом же Enable).
//
// Выровнено ли — решает длина идентификатора приложения в правиле «свой
// процесс», то есть длина пути к exe. У csqtt-cli.exe путь постоянный, а у
// тестового бинаря каталог go-build каждый раз свой, поэтому длины
// перебираются: дополнение в 0–7 байт даёт и невыровненный случай.
// Без исправления в форке тест падает за доли секунды.
func TestDNSGuardUnderGC(t *testing.T) {
	g, err := OpenDNSGuard()
	if err != nil {
		t.Skipf("сессия WFP не открылась (нужны права администратора): %v", err)
	}
	defer g.Close()

	var stop atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		for !stop.Load() {
			runtime.GC()
		}
	}()
	defer func() {
		stop.Store(true)
		<-done
	}()

	appID := g.appID
	dns := []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("1.0.0.1")}
	for i := range 50 {
		for pad := range 8 {
			g.appID = appID + strings.Repeat("x", pad)
			if err := g.Enable(dns); err != nil {
				t.Fatalf("Enable %d, дополнение %d: %v", i, pad, err)
			}
		}
	}
	if err := g.Disable(); err != nil {
		t.Fatal(err)
	}
}

// Набор «закрыто» — без WFP: на каждом слое сверху вниз по весу свой
// процесс и loopback, запрет DNS, служебные протоколы и локальная сеть
// своего семейства, запрет всего.
func TestLockdownRules(t *testing.T) {
	rules, err := lockdownRules("app-id")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[wf.RuleID]bool{}
	byLayer := map[wf.LayerID][]*wf.Rule{}
	for _, r := range rules {
		if r.Sublayer != dnsGuardSublayer || r.Provider != DNSGuardProvider {
			t.Errorf("правило %q не в своём подслое или провайдере", r.Name)
		}
		if ids[r.ID] || r.ID.IsZero() {
			t.Errorf("GUID правила %q пустой или повторён", r.Name)
		}
		ids[r.ID] = true
		byLayer[r.Layer] = append(byLayer[r.Layer], r)
	}

	for _, layer := range dnsGuardLayers {
		got := byLayer[layer.id]
		want := 6
		if layer.ipv6 {
			want = 7 // и ICMPv6 ND
		}
		if len(got) != want {
			t.Fatalf("%s: правил %d, ждали %d", layer.name, len(got), want)
		}
		for i := 1; i < len(got); i++ {
			if got[i].Weight > got[i-1].Weight {
				t.Errorf("%s: %q тяжелее %q, стоящего выше", layer.name, got[i].Name, got[i-1].Name)
			}
		}
		self, loopback, dns, dhcp, local, block := got[0], got[1], got[2], got[3], got[len(got)-2], got[len(got)-1]

		for _, r := range []*wf.Rule{self, loopback, dhcp, local} {
			if r.Action != wf.ActionPermit {
				t.Errorf("%s: %q — %v, ждали разрешение", layer.name, r.Name, r.Action)
			}
		}
		for _, r := range []*wf.Rule{dns, block} {
			if r.Action != wf.ActionBlock {
				t.Errorf("%s: %q — %v, ждали запрет", layer.name, r.Name, r.Action)
			}
		}
		if self.Weight != dnsGuardPermitWeight || loopback.Weight != dnsGuardPermitWeight {
			t.Errorf("%s: свой процесс и loopback с весами %d и %d", layer.name, self.Weight, loopback.Weight)
		}
		if dns.Weight != dnsGuardBlockWeight || dns.Weight <= local.Weight {
			t.Errorf("%s: запрет DNS с весом %d не тяжелее локальной сети (%d)", layer.name, dns.Weight, local.Weight)
		}
		if block.Weight >= local.Weight || block.Weight >= dhcp.Weight || len(block.Conditions) != 0 {
			t.Errorf("%s: запрет всего — вес %d, условий %d", layer.name, block.Weight, len(block.Conditions))
		}

		checkMatches(t, layer.name+": свой процесс", self.Conditions, []wf.Match{
			{Field: wf.FieldALEAppID, Op: wf.MatchTypeEqual, Value: "app-id"},
		})
		checkMatches(t, layer.name+": loopback", loopback.Conditions, []wf.Match{
			{Field: wf.FieldFlags, Op: wf.MatchTypeFlagsAllSet, Value: wf.ConditionFlagIsLoopback},
		})
		checkMatches(t, layer.name+": запрет DNS", dns.Conditions, []wf.Match{
			{Field: wf.FieldIPRemotePort, Op: wf.MatchTypeEqual, Value: uint16(53)},
			{Field: wf.FieldIPProtocol, Op: wf.MatchTypeEqual, Value: wf.IPProtoUDP},
			{Field: wf.FieldIPProtocol, Op: wf.MatchTypeEqual, Value: wf.IPProtoTCP},
		})
		client, server := uint16(68), uint16(67)
		if layer.ipv6 {
			client, server = 546, 547
		}
		checkMatches(t, layer.name+": DHCP", dhcp.Conditions, []wf.Match{
			{Field: wf.FieldIPProtocol, Op: wf.MatchTypeEqual, Value: wf.IPProtoUDP},
			{Field: wf.FieldIPLocalPort, Op: wf.MatchTypeEqual, Value: client},
			{Field: wf.FieldIPRemotePort, Op: wf.MatchTypeEqual, Value: server},
		})
		if layer.ipv6 {
			ndp := got[4]
			want := []wf.Match{{Field: wf.FieldIPProtocol, Op: wf.MatchTypeEqual, Value: wf.IPProtoICMPV6}}
			for typ := uint16(133); typ <= 137; typ++ {
				want = append(want, wf.Match{Field: wf.FieldIPLocalPort, Op: wf.MatchTypeEqual, Value: typ})
			}
			if ndp.Action != wf.ActionPermit || ndp.Weight != local.Weight {
				t.Errorf("%s: ND — %v с весом %d", layer.name, ndp.Action, ndp.Weight)
			}
			checkMatches(t, layer.name+": ND", ndp.Conditions, want)
		}

		var wantLocal []wf.Match
		for _, p := range localNets {
			if p.Addr().Is6() == layer.ipv6 {
				wantLocal = append(wantLocal, wf.Match{Field: wf.FieldIPRemoteAddress, Op: wf.MatchTypeEqual, Value: p})
			}
		}
		if len(wantLocal) == 0 {
			t.Fatalf("%s: локальной сети нет", layer.name)
		}
		checkMatches(t, layer.name+": локальная сеть", local.Conditions, wantLocal)
	}
}

// Переходы между наборами на настоящем WFP (нужны права администратора):
// «закрыто» → защита DNS → «закрыто» → без правил. После каждого перехода
// в сессии ровно правила нового набора — старые сняты, новые на месте.
func TestDNSGuardLockdownSession(t *testing.T) {
	g, err := OpenDNSGuard()
	if err != nil {
		t.Skipf("сессия WFP не открылась (нужны права администратора): %v", err)
	}
	defer g.Close()
	lockdown, err := lockdownRules(g.appID)
	if err != nil {
		t.Fatal(err)
	}
	dns := []netip.Addr{netip.MustParseAddr("1.1.1.1")}

	check := func(what string, state guardState, want int) {
		t.Helper()
		if g.state != state || len(g.rules) != want {
			t.Errorf("%s: состояние %d, правил %d; ждали %d и %d", what, g.state, len(g.rules), state, want)
		}
		if inSession := sublayerRules(t, g); inSession != want {
			t.Errorf("%s: в подслое WFP правил %d, ждали %d", what, inSession, want)
		}
	}

	steps := []struct {
		what  string
		do    func() error
		state guardState
		want  int
	}{
		{"Lockdown", g.Lockdown, guardLockdown, len(lockdown)},
		{"повторный Lockdown", g.Lockdown, guardLockdown, len(lockdown)},
		{"Enable", func() error { return g.Enable(dns) }, guardTunnel, 3 * len(dnsGuardLayers)},
		{"Lockdown после Enable", g.Lockdown, guardLockdown, len(lockdown)},
		{"Disable", g.Disable, guardOpen, 0},
	}
	for _, s := range steps {
		if err := s.do(); err != nil {
			t.Fatalf("%s: %v", s.what, err)
		}
		check(s.what, s.state, s.want)
	}
}

// Набор, который не встал целиком, снимается, а прежний остаётся: здесь
// «закрыто» остаётся «закрыто». Отказ — правило с неверным значением
// условия: оно легче прочих и ставится последним, после всех верных.
func TestDNSGuardReplaceRollback(t *testing.T) {
	g, err := OpenDNSGuard()
	if err != nil {
		t.Skipf("сессия WFP не открылась (нужны права администратора): %v", err)
	}
	defer g.Close()
	if err := g.Lockdown(); err != nil {
		t.Fatal(err)
	}
	want := len(g.rules)

	rules, err := dnsGuardRules([]netip.Addr{netip.MustParseAddr("1.1.1.1")}, g.appID)
	if err != nil {
		t.Fatal(err)
	}
	bad, err := newGuardRule(wf.LayerALEAuthConnectV4, "csqtt-cli: тест отказа", 1, wf.ActionBlock,
		[]*wf.Match{{Field: wf.FieldIPRemotePort, Op: wf.MatchTypeEqual, Value: "не порт"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.replaceLocked(guardTunnel, "тест", append(rules, bad)); err == nil {
		t.Fatal("набор с неверным правилом встал")
	}
	if g.state != guardLockdown || len(g.rules) != want {
		t.Errorf("после отказа: состояние %d, правил %d; ждали «закрыто» и %d", g.state, len(g.rules), want)
	}
	if inSession := sublayerRules(t, g); inSession != want {
		t.Errorf("в подслое WFP правил %d, ждали %d — новые не сняты", inSession, want)
	}
}

// DNS на ходу на настоящем WFP: резолверы меняются дважды подряд тем же
// порядком, что у Tun.applyDNS, — сначала пустить прежние и новые, потом
// только новые. После каждого правила, которое встало или снято, в подслое
// на каждом слое есть запрет порта 53, разрешение своему процессу и
// резолверы, к которым ведёт DNS системы на этом шаге, — мига без защиты нет.
// В конце перехода разрешены ровно новые резолверы.
//
// Шагов около сотни, и перебор фильтров WFP после каждого занимает секунды,
// поэтому к резолверам теста добавлены нынешние резолверы системы: DNS
// машины на это время не закрывается.
func TestDNSGuardChangeDNS(t *testing.T) {
	g, err := OpenDNSGuard()
	if err != nil {
		t.Skipf("сессия WFP не открылась (нужны права администратора): %v", err)
	}
	defer g.Close()
	a := netip.MustParseAddr("192.0.2.53")
	b := netip.MustParseAddr("198.51.100.53")
	c := netip.MustParseAddr("203.0.113.53")
	sys := systemResolvers(t)
	with := func(dns ...netip.Addr) []netip.Addr { return append(dns, sys...) }

	known := map[wf.RuleID]*wf.Rule{}
	var keep []netip.Addr
	checking, steps, failed := false, 0, false
	// В крючке t.Fatal нельзя: g.mu взят, и отложенный Close завис бы.
	g.step = func(r *wf.Rule, added bool) {
		if added {
			known[r.ID] = r
		}
		if !checking || failed {
			return
		}
		steps++
		if problem := guardGap(t, g, known, keep, false); problem != "" {
			failed = true
			t.Errorf("шаг %d (%q, встало %v): %s", steps, r.Name, added, problem)
		}
	}

	if err := g.Enable(with(a)); err != nil {
		t.Fatalf("первый Enable: %v", err)
	}
	checking = true
	for _, s := range []struct {
		what      string
		dns, keep []netip.Addr
	}{
		{"прежние и новые", with(a, b), with(a)},
		{"только новые", with(b), with(b)},
		{"второй раз: прежние и новые", with(b, c), with(b)},
		{"второй раз: только новые", with(c), with(c)},
	} {
		keep = s.keep
		if err := g.Enable(s.dns); err != nil {
			t.Fatalf("%s: Enable: %v", s.what, err)
		}
		if problem := guardGap(t, g, known, s.dns, true); problem != "" {
			t.Errorf("%s: после перехода %s", s.what, problem)
		}
		if n := sublayerRules(t, g); n != 3*len(dnsGuardLayers) {
			t.Errorf("%s: в подслое правил %d, ждали %d — старые не сняты", s.what, n, 3*len(dnsGuardLayers))
		}
	}
	if steps == 0 {
		t.Error("крючок перехода не звался")
	}
}

// guardGap смотрит, что стоит в подслое WFP, и возвращает, чего не хватает
// защите DNS: на каждом слое запрет порта 53, разрешение своему процессу,
// loopback и резолверы keep своего семейства; exact — и никаких других. Правило подслоя, которого нет в known, — тоже
// ошибка: его поставил не этот тест.
func guardGap(t *testing.T, g *DNSGuard, known map[wf.RuleID]*wf.Rule, keep []netip.Addr, exact bool) string {
	t.Helper()
	all, err := g.session.Rules()
	if err != nil {
		return "список правил WFP: " + err.Error()
	}
	type layerState struct {
		block, self, loopback bool
		permitted             map[netip.Addr]bool
	}
	layers := map[wf.LayerID]*layerState{}
	for _, l := range dnsGuardLayers {
		layers[l.id] = &layerState{permitted: map[netip.Addr]bool{}}
	}
	for _, inSession := range all {
		if inSession.Sublayer != dnsGuardSublayer {
			continue
		}
		r, ok := known[inSession.ID]
		if !ok {
			return "в подслое чужое правило " + inSession.Name
		}
		st := layers[r.Layer]
		switch {
		case r.Action == wf.ActionBlock:
			st.block = true
		case strings.Contains(r.Name, "DNS самого"):
			st.self = true
		default:
			for _, m := range r.Conditions {
				switch v := m.Value.(type) {
				case netip.Addr:
					if v.IsLoopback() {
						st.loopback = true
					} else {
						st.permitted[v] = true
					}
				case netip.Prefix:
					if v.Addr().IsLoopback() {
						st.loopback = true
					}
				}
			}
		}
	}
	for _, l := range dnsGuardLayers {
		st := layers[l.id]
		switch {
		case !st.block:
			return l.name + ": нет запрета порта 53"
		case !st.self:
			return l.name + ": нет разрешения своему процессу"
		case !st.loopback:
			return l.name + ": нет loopback"
		}
		want := 0
		for _, a := range keep {
			if a.Is6() != l.ipv6 {
				continue
			}
			want++
			if !st.permitted[a] {
				return l.name + ": не разрешён резолвер " + a.String()
			}
		}
		if exact && len(st.permitted) != want {
			return l.name + ": разрешены лишние резолверы"
		}
	}
	return ""
}

// systemResolvers — резолверы, которые сейчас стоят у интерфейсов системы,
// без заглушек fec0:0:0:ffff::1–3, которые Windows показывает у интерфейса
// без DNS IPv6.
func systemResolvers(t *testing.T) []netip.Addr {
	t.Helper()
	placeholder := netip.MustParsePrefix("fec0:0:0:ffff::/64")
	ifaces, err := winipcfg.GetAdaptersAddresses(windows.AF_UNSPEC, winipcfg.GAAFlagDefault)
	if err != nil {
		t.Fatalf("интерфейсы системы: %v", err)
	}
	var out []netip.Addr
	for _, iface := range ifaces {
		for d := iface.FirstDNSServerAddress; d != nil; d = d.Next {
			a, ok := netip.AddrFromSlice(d.Address.IP())
			if a = a.Unmap(); ok && !placeholder.Contains(a) && !slices.Contains(out, a) {
				out = append(out, a)
			}
		}
	}
	return out
}

// sublayerRules — сколько правил стоит в нашем подслое WFP.
func sublayerRules(t *testing.T, g *DNSGuard) int {
	t.Helper()
	all, err := g.session.Rules()
	if err != nil {
		t.Fatalf("список правил WFP: %v", err)
	}
	n := 0
	for _, r := range all {
		if r.Sublayer == dnsGuardSublayer {
			n++
		}
	}
	return n
}
