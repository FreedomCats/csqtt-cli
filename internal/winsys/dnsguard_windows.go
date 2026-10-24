package winsys

import (
	"cmp"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"slices"
	"sync"

	"github.com/tailscale/wf"
	"golang.org/x/sys/windows"

	"github.com/FreedomCats/csqtt-cli/internal/app"
)

// Постоянные GUID провайдера и подслоя WFP: по ним наши фильтры узнаются
// в `netsh wfp show filters`, а провайдер ищет scripts/checktunnel. Правила
// получают новые GUID на каждый Enable.
//
// GUID свои, а не унаследованные от проекта-основы: на одной машине могут
// стоять обе программы, и одна не должна принимать фильтры другой за свои.
var (
	DNSGuardProvider = wf.ProviderID{
		Data1: 0xf9b5fed4, Data2: 0x3e33, Data3: 0x45a6,
		Data4: [8]byte{0xbf, 0xa0, 0x2a, 0x21, 0x97, 0x3f, 0x29, 0x80},
	}
	dnsGuardSublayer = wf.SublayerID{
		Data1: 0xfca6e9cd, Data2: 0xb00c, Data3: 0x4ffe,
		Data4: [8]byte{0x9b, 0x69, 0x8f, 0x27, 0x9c, 0x20, 0xd3, 0x29},
	}
)

// Веса правил внутри подслоя: разрешение тяжелее запрета, как в blockDNS
// у wireguard-windows (tunnel/firewall/rules.go, MIT, v1.1.1).
const (
	dnsGuardPermitWeight = 15
	dnsGuardBlockWeight  = 14
)

// DNSGuard — защита от утечки DNS: фильтры WFP
// запрещают обращения на порт 53, UDP и TCP, ко всем адресам, кроме
// резолверов туннеля, loopback и запросов самого csqtt-cli.
//
// Сессия WFP динамическая: всё, что в ней создано, система снимает при её
// закрытии или со смертью процесса, поэтому упавший csqtt-cli фильтров не оставляет
// и уборка при запуске не нужна.
//
// Своему процессу разрешено всё: ядро ходит к DNS VK (резолвер из профиля)
// сокетами мимо туннеля (netguard), и без этого не подключилось бы.
// Системные запросы идут от службы DNS-клиента, а не от программ, — их
// пропускает правило по адресу резолвера туннеля.
//
// Сессия открывается один раз на csqtt-cli connect (OpenDNSGuard), а правила
// ставятся и снимаются по ходу (Enable, Lockdown, Disable): отказ WFP так
// узнаётся до подключения, а не посреди него. В той же сессии живёт и
// kill-switch — набор «закрыто», см. Lockdown.
type DNSGuard struct {
	mu      sync.Mutex
	session *wf.Session
	appID   string
	rules   []*wf.Rule // стоят в сессии
	state   guardState

	// step — ради теста перехода: зовётся после каждого правила, которое
	// встало (added) или снято, пока g.mu взят.
	step func(r *wf.Rule, added bool)
}

// guardState — какой набор правил стоит в сессии.
type guardState int

const (
	guardOpen     guardState = iota // правил нет: ничего не запрещено
	guardTunnel                     // туннель поднят: защита DNS (Enable)
	guardLockdown                   // закрыто: kill-switch (Lockdown)
)

// OpenDNSGuard открывает сессию WFP, заводит в ней провайдера и подслой;
// правил ещё нет. Нужны права администратора и работающая служба BFE
// (Base Filtering Engine).
func OpenDNSGuard() (*DNSGuard, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("путь к csqtt-cli.exe: %w", err)
	}
	appID, err := wf.AppID(exe)
	if err != nil {
		return nil, fmt.Errorf("идентификатор приложения WFP для %s: %w", exe, err)
	}

	session, err := wf.New(&wf.Options{
		Name:        app.Name,
		Description: "csqtt-cli: защита от утечки DNS",
		Dynamic:     true,
	})
	if err != nil {
		return nil, fmt.Errorf("сессия WFP: %w", err)
	}
	if err := session.AddProvider(&wf.Provider{
		ID:          DNSGuardProvider,
		Name:        app.Name,
		Description: "csqtt-cli: защита от утечки DNS",
	}); err != nil {
		session.Close()
		return nil, fmt.Errorf("провайдер WFP: %w", err)
	}
	if err := session.AddSublayer(&wf.Sublayer{
		ID:          dnsGuardSublayer,
		Name:        app.Name + " DNS",
		Description: "csqtt-cli: DNS только через туннель",
		Provider:    DNSGuardProvider,
		Weight:      ^uint16(0),
	}); err != nil {
		session.Close()
		return nil, fmt.Errorf("подслой WFP: %w", err)
	}
	return &DNSGuard{session: session, appID: appID}, nil
}

// Enable ставит правила: DNS — только к резолверам dns. Прежние правила
// заменяются (replaceLocked), так что повторный вызов безвреден. На отказе
// правила снимаются, чтобы не осталось и старых, — кроме набора «закрыто»:
// он строже защиты DNS, и открывать систему из-за отказа незачем.
func (g *DNSGuard) Enable(dns []netip.Addr) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	rules, err := dnsGuardRules(dns, g.appID)
	if err == nil {
		err = g.replaceLocked(guardTunnel, "защита DNS", rules)
	}
	if err == nil || g.state == guardLockdown {
		return err
	}
	return errors.Join(err, g.disableLocked())
}

// replaceLocked ставит rules вместо прежних правил и при успехе запоминает
// состояние state; what — чьи правила, для ошибок.
//
// Транзакций WFP у tailscale/wf нет, поэтому сначала ставятся новые
// правила, потом снимаются старые: мига без правил на переходе нет. Новые
// ставятся от тяжёлых к лёгким, старые снимаются от лёгких к тяжёлым, так
// что разрешение самому csqtt-cli стоит всё время, а запреты появляются после
// своих разрешений. Пока стоят оба набора «закрыто» и защиты DNS, действует
// «закрыто», к которому добавлены резолверы туннеля.
//
// Не встало новое правило — снимаются уже поставленные новые, прежний
// набор остаётся. Не снялось правило — оно остаётся в g.rules (снимет
// следующий Disable или Close), а ошибка возвращается.
func (g *DNSGuard) replaceLocked(state guardState, what string, rules []*wf.Rule) error {
	if g.session == nil {
		return fmt.Errorf("%s: сессия WFP закрыта", what)
	}
	rules = slices.Clone(rules)
	slices.SortStableFunc(rules, func(a, b *wf.Rule) int { return cmp.Compare(b.Weight, a.Weight) })
	for i, r := range rules {
		if err := g.session.AddRule(r); err != nil {
			err = fmt.Errorf("%s: правило %q: %w", what, r.Name, err)
			left, derr := g.deleteLocked(rules[:i])
			g.rules = append(g.rules, left...)
			return errors.Join(err, derr)
		}
		if g.step != nil {
			g.step(r, true)
		}
	}
	old := g.rules
	g.rules, g.state = rules, state
	left, err := g.deleteLocked(old)
	g.rules = append(g.rules, left...)
	return err
}

// Disable снимает правила; сессия остаётся для следующего Enable. Без
// правил — ничего не делает.
func (g *DNSGuard) Disable() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.disableLocked()
}

func (g *DNSGuard) disableLocked() error {
	left, err := g.deleteLocked(g.rules)
	g.rules = left
	if len(left) == 0 {
		g.state = guardOpen
	}
	return err
}

// deleteLocked снимает rules от лёгких к тяжёлым: запрет всего уходит
// первым, разрешение самому csqtt-cli — последним. Возвращает правила, которые
// снять не удалось.
func (g *DNSGuard) deleteLocked(rules []*wf.Rule) ([]*wf.Rule, error) {
	rules = slices.Clone(rules)
	slices.SortStableFunc(rules, func(a, b *wf.Rule) int { return cmp.Compare(a.Weight, b.Weight) })
	var left []*wf.Rule
	var errs []error
	for _, r := range rules {
		if err := g.session.DeleteRule(r.ID); err != nil && !errors.Is(err, windows.Errno(fwpEFilterNotFound)) {
			left = append(left, r)
			errs = append(errs, err)
			continue
		}
		if g.step != nil {
			g.step(r, false)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return left, fmt.Errorf("защита DNS: снятие правил: %w", err)
	}
	return nil, nil
}

// Close закрывает сессию: система снимает всё, что в ней создано.
// Повторный вызов безвреден.
func (g *DNSGuard) Close() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.session == nil {
		return nil
	}
	err := g.session.Close()
	g.session, g.rules, g.state = nil, nil, guardOpen
	return err
}

// fwpEFilterNotFound — FWP_E_FILTER_NOT_FOUND: правила уже нет.
const fwpEFilterNotFound = 0x80320003

// dnsGuardLayers — слои, где действуют правила: исходящие соединения и
// первый входящий пакет несвязанного сокета, IPv4 и IPv6, как у blockDNS
// в wireguard-windows.
var dnsGuardLayers = []struct {
	id   wf.LayerID
	ipv6 bool
	name string
}{
	{wf.LayerALEAuthConnectV4, false, "исходящие IPv4"},
	{wf.LayerALEAuthRecvAcceptV4, false, "входящие IPv4"},
	{wf.LayerALEAuthConnectV6, true, "исходящие IPv6"},
	{wf.LayerALEAuthRecvAcceptV6, true, "входящие IPv6"},
}

// dnsGuardRules строит правила для Enable; отдельно — ради теста без прав.
// На каждом слое: запрет порта 53 по UDP и TCP, разрешение своему
// процессу, разрешение резолверам туннеля своего семейства и loopback.
// Повтор условия с тем же полем WFP читает как «или».
func dnsGuardRules(dns []netip.Addr, appID string) ([]*wf.Rule, error) {
	var rules []*wf.Rule
	add := func(layer wf.LayerID, name string, weight uint64, action wf.Action, conds []*wf.Match) error {
		r, err := newGuardRule(layer, name, weight, action, conds)
		if err != nil {
			return err
		}
		rules = append(rules, r)
		return nil
	}

	for _, layer := range dnsGuardLayers {
		if err := add(layer.id, "csqtt-cli: запрет DNS мимо туннеля, "+layer.name,
			dnsGuardBlockWeight, wf.ActionBlock, dnsPortMatches()); err != nil {
			return nil, err
		}

		self := []*wf.Match{{Field: wf.FieldALEAppID, Op: wf.MatchTypeEqual, Value: appID}}
		if err := add(layer.id, "csqtt-cli: DNS самого csqtt-cli, "+layer.name,
			dnsGuardPermitWeight, wf.ActionPermit, self); err != nil {
			return nil, err
		}

		var allowed []*wf.Match
		if layer.ipv6 {
			allowed = append(allowed, &wf.Match{Field: wf.FieldIPRemoteAddress, Op: wf.MatchTypeEqual, Value: netip.IPv6Loopback()})
		} else {
			allowed = append(allowed, &wf.Match{Field: wf.FieldIPRemoteAddress, Op: wf.MatchTypeEqual, Value: netip.MustParsePrefix("127.0.0.0/8")})
		}
		for _, a := range dns {
			a = a.Unmap()
			if a.Is6() == layer.ipv6 {
				allowed = append(allowed, &wf.Match{Field: wf.FieldIPRemoteAddress, Op: wf.MatchTypeEqual, Value: a})
			}
		}
		if err := add(layer.id, "csqtt-cli: DNS туннеля и loopback, "+layer.name,
			dnsGuardPermitWeight, wf.ActionPermit, allowed); err != nil {
			return nil, err
		}
	}
	return rules, nil
}

// dnsPortMatches — условия запрета DNS: порт 53, UDP или TCP.
func dnsPortMatches() []*wf.Match {
	return []*wf.Match{
		{Field: wf.FieldIPRemotePort, Op: wf.MatchTypeEqual, Value: uint16(53)},
		{Field: wf.FieldIPProtocol, Op: wf.MatchTypeEqual, Value: wf.IPProtoUDP},
		{Field: wf.FieldIPProtocol, Op: wf.MatchTypeEqual, Value: wf.IPProtoTCP},
	}
}

// newGuardRule — правило в нашем подслое с новым GUID.
func newGuardRule(layer wf.LayerID, name string, weight uint64, action wf.Action, conds []*wf.Match) (*wf.Rule, error) {
	guid, err := windows.GenerateGUID()
	if err != nil {
		return nil, fmt.Errorf("защита DNS: GUID правила: %w", err)
	}
	return &wf.Rule{
		ID:         wf.RuleID(guid),
		Name:       name,
		Layer:      layer,
		Sublayer:   dnsGuardSublayer,
		Provider:   DNSGuardProvider,
		Weight:     weight,
		Conditions: conds,
		Action:     action,
	}, nil
}
