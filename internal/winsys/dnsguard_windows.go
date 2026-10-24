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
// резолверов туннеля, loopback и запросов самого csqtt-cli и его ядра.
//
// Сессия WFP динамическая: всё, что в ней создано, система снимает при её
// закрытии или со смертью процесса, поэтому упавший csqtt-cli фильтров не оставляет
// и уборка при запуске не нужна.
//
// Своим процессам — csqtt-cli и ядру — разрешено всё: ядро ходит к DNS
// Яндекса, VK и TURN сокетами мимо туннеля (привязка к интерфейсу, §06), а
// прокси окна капчи — сокетами csqtt-cli, и без этого не подключилось бы.
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
	appIDs  []string   // csqtt-cli и ядро: им разрешено всё
	rules   []*wf.Rule // стоят в сессии
	state   guardState
	// blockIPv6 — закрывать при Enable IPv6 системы наружу (BlockIPv6).
	blockIPv6 bool

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
//
// Всё разрешено самому процессу и программам programs — полным путям к
// exe (ядро рядом с csqtt-cli.exe). WFP узнаёт программу по пути к файлу, а
// не по процессу, поэтому путь — тот, с которого её запустят. Путь
// приводится к длинным именам: процесс WFP знает по ним, а от пути с
// короткими (8.3, «PROGRA~1») идентификатор выйдет другой, и kill-switch
// закрыл бы и само ядро.
func OpenDNSGuard(programs ...string) (*DNSGuard, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("путь к csqtt-cli.exe: %w", err)
	}
	var appIDs []string
	for _, path := range append([]string{exe}, programs...) {
		appID, err := wf.AppID(longPath(path))
		if err != nil {
			return nil, fmt.Errorf("идентификатор приложения WFP для %s: %w", path, err)
		}
		appIDs = append(appIDs, appID)
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
	return &DNSGuard{session: session, appIDs: appIDs}, nil
}

// longPath — path с длинными именами вместо коротких 8.3. Не вышло (файла
// нет, нет прав на каталог) — path как есть: тогда и WFP получит его же.
func longPath(path string) string {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return path
	}
	buf := make([]uint16, windows.MAX_PATH)
	for {
		n, err := windows.GetLongPathName(p, &buf[0], uint32(len(buf)))
		switch {
		case err != nil || n == 0:
			return path
		case int(n) < len(buf):
			// Поместилось: n — длина без нуля в конце.
			return windows.UTF16ToString(buf[:n])
		}
		// Мал буфер: n — нужный размер с нулём.
		buf = make([]uint16, n)
	}
}

// BlockIPv6 задаёт, закрывать ли при следующем Enable IPv6 системы наружу:
// туннель IPv6 не везёт, и если маршрутов IPv6 в адаптер нет (IPv6 выключен
// на нём), IPv6 шёл бы мимо туннеля. Открытыми остаются loopback, свои
// процессы и, как в «закрыто», DHCPv6, ND, RA и локальная сеть IPv6; DNS к
// локальной сети закрыт запретом порта 53.
func (g *DNSGuard) BlockIPv6(block bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.blockIPv6 = block
}

// Enable ставит правила: DNS — только к резолверам dns. Прежние правила
// заменяются (replaceLocked), так что повторный вызов безвреден. На отказе
// правила снимаются, чтобы не осталось и старых, — кроме набора «закрыто»:
// он строже защиты DNS, и открывать систему из-за отказа незачем.
func (g *DNSGuard) Enable(dns []netip.Addr) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	rules, err := dnsGuardRules(dns, g.appIDs, g.blockIPv6)
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
var dnsGuardLayers = []guardLayer{
	{wf.LayerALEAuthConnectV4, false, "исходящие IPv4"},
	{wf.LayerALEAuthRecvAcceptV4, false, "входящие IPv4"},
	{wf.LayerALEAuthConnectV6, true, "исходящие IPv6"},
	{wf.LayerALEAuthRecvAcceptV6, true, "входящие IPv6"},
}

// guardLayer — слой WFP, на котором стоят правила защиты.
type guardLayer struct {
	id   wf.LayerID
	ipv6 bool
	name string
}

// dnsGuardRules строит правила для Enable; отдельно — ради теста без прав.
// На каждом слое: запрет порта 53 по UDP и TCP, разрешение своим
// процессам, разрешение резолверам туннеля своего семейства и loopback.
// Повтор условия с тем же полем WFP читает как «или».
//
// При blockIPv6 на слоях IPv6 ещё набор «закрыто» без своих процессов и
// запрета DNS (они уже есть): loopback, служебные протоколы и локальная
// сеть, запрет всего (lockdownLocalRules).
func dnsGuardRules(dns []netip.Addr, appIDs []string, blockIPv6 bool) ([]*wf.Rule, error) {
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

		if err := add(layer.id, "csqtt-cli: DNS самого csqtt-cli, "+layer.name,
			dnsGuardPermitWeight, wf.ActionPermit, selfMatches(appIDs)); err != nil {
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

		if blockIPv6 && layer.ipv6 {
			local, err := lockdownLocalRules(layer, "csqtt-cli: IPv6 наружу закрыт")
			if err != nil {
				return nil, err
			}
			rules = append(rules, local...)
		}
	}
	return rules, nil
}

// selfMatches — условие «свой процесс»: любой из appIDs.
func selfMatches(appIDs []string) []*wf.Match {
	var m []*wf.Match
	for _, id := range appIDs {
		m = append(m, &wf.Match{Field: wf.FieldALEAppID, Op: wf.MatchTypeEqual, Value: id})
	}
	return m
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
