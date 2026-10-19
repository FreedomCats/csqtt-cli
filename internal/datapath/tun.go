package datapath

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"sync"

	"github.com/FreedomCats/csqtt-cli/internal/app"
)

// TunName — имя адаптера по умолчанию: по нему следы csqtt-cli находятся в
// системе.
const TunName = app.Name

// tunRoutes4 — маршруты IPv4, которые tun ставит через адаптер: всё адресное
// пространство половинами /1. Маршрут по умолчанию не трогается, половины
// выигрывают у него длиной префикса (§05).
var tunRoutes4 = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/1"),
	netip.MustParsePrefix("128.0.0.0/1"),
}

// TunOptions — настройки datapath tun.
type TunOptions struct {
	// Name — имя адаптера. Пусто — TunName.
	Name string
	// Logf — куда писать журнал; nil — молчать.
	Logf func(format string, args ...any)

	// DNSGuard — защита от утечки DNS: включается, когда маршруты стоят,
	// переключается при смене DNS на ходу, снимается в Suspend и Close. Умеет
	// закрыть IPv6 (IPv6Blocker) — закрывает IPv6 наружу, пока туннель
	// поднят. nil — без неё.
	DNSGuard DNSGuard
}

// Tun — datapath с системным адаптером: весь трафик системы идёт в туннель
// (architecture.md §05). На Linux пакеты ходят между адаптером и ядром
// напрямую: ядро получает дескриптор адаптера (SendFD, Р4).
//
// Tun — Persistent: адаптер живёт весь csqtt-cli connect. Создаётся он при
// первой надобности — Open, первый SendFD или первый Start, — Start каждой
// попытки задаёт ему адрес из TUNCONF, ставит маршруты, защиту DNS и DNS
// системы, Suspend между попытками их снимает, Close убирает адаптер.
//
// Адаптер живёт, пока открыт его дескриптор: после падения процесса он
// исчезает сам, вместе с адресом и маршрутами.
type Tun struct {
	opts TunOptions

	// openAdapter создаёт адаптер. Поле — ради тестов: настоящему нужны
	// права, а логика Start, Suspend и Close от этого не зависит.
	openAdapter func(name string, logf func(string, ...any)) (*tunAdapter, error)

	// mu держится и на время системных вызовов: они короткие, а Close так
	// дожидается Start, идущего в другой горутине.
	mu      sync.Mutex
	closed  bool
	adapter *tunAdapter
	// addr — адрес, заданный адаптеру; не задан — Start ещё не было.
	addr netip.Addr
	// dns — DNS туннеля из последнего TUNCONF.
	dns []netip.Addr
	// up — туннель поднят: Start прошёл, Suspend и Close ещё не было.
	// UpdateDNS меняет DNS системы и защиту только тогда.
	up bool
	// guardDNS — резолверы, которые пускает защита DNS, если guardSet;
	// иначе правил защиты нет или они неизвестны (отказ Enable). sysDNS —
	// DNS системы, если sysSet. Пока DNS меняется на ходу, guardDNS
	// включает и прежние, и новые (applyDNS).
	guardDNS []netip.Addr
	guardSet bool
	sysDNS   []netip.Addr
	sysSet   bool
	// ipv6Told — о закрытии IPv6 уже сказано в журнале (blockIPv6).
	ipv6Told bool

	// closeOnce и closeErr — уборка один раз, ожидание всеми (см. Close).
	closeOnce sync.Once
	closeErr  error
}

// tunAdapter — то, что платформа отдаёт Tun.
type tunAdapter struct {
	// configure задаёт адаптеру адрес /32 и MTU TunnelMTU и поднимает его;
	// повторный вызов меняет адрес.
	configure func(addr netip.Addr) error
	// addRoutes и removeRoutes ставят и снимают маршруты через адаптер; оба
	// безвредны повторно. Сам адаптер при этом остаётся.
	addRoutes    func() error
	removeRoutes func() error
	// setDNS делает servers DNS системы, повторный вызов заменяет прежние;
	// пустой список снимает поставленное. revertDNS снимает поставленное,
	// без него ничего не делает. nil — DNS системы задавать нечем (подделки
	// в тестах).
	setDNS    func(servers []netip.Addr) error
	revertDNS func() error
	// dup — новый дубликат дескриптора адаптера для ядра (Linux).
	dup func() (int, error)
	// close закрывает адаптер — вместе с ним уходят адрес и маршруты — и
	// отпускает блокировку второго запуска.
	close func() error
}

var errTunClosed = errors.New("tun: datapath уже остановлен")

// errTunBusy — отказ второму csqtt-cli с datapath tun на той же машине.
var errTunBusy = errors.New("адаптер уже занят: на этой машине работает другой " + app.Name +
	" с datapath tun; остановите его или подключитесь с -datapath socks")

// NewTun готовит datapath. Система не трогается до Open, SendFD или Start:
// вызывающий строит datapath и ради одного Requires.
func NewTun(opts TunOptions) *Tun {
	if strings.TrimSpace(opts.Name) == "" {
		opts.Name = TunName
	}
	return &Tun{opts: opts, openAdapter: openTunAdapter}
}

// Requires: root, весь трафик системы — значит, сокеты ядра нужно увести
// мимо туннеля, а DNS системы — закрыть от утечки. Где своего адаптера
// нет — Unsupported.
func (t *Tun) Requires() Requirements {
	return Requirements{Unsupported: tunUnsupported(), Admin: true, Netguard: true, DNSGuard: true}
}

// Reserve занимает адаптер на машине до подключения (Reserver): второй
// csqtt-cli с datapath tun узнаёт о первом сразу, а не после похода в VK.
// Открытие адаптера берёт ту же блокировку ещё раз, внутри процесса это не
// конфликт.
func (t *Tun) Reserve() (release func(), err error) {
	release, err = acquireTunLock()
	if err != nil {
		return nil, fmt.Errorf("tun: %w", err)
	}
	return release, nil
}

// Open создаёт адаптер, если его ещё нет: без адреса и опущенным, маршрутов
// нет. Звать необязательно — это сделают SendFD и Start, — но вызывающий
// может открыть адаптер до первой попытки, чтобы отказ (интерфейс с тем же
// именем, нет прав) выяснился до похода в VK.
func (t *Tun) Open() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, err := t.openLocked()
	return err
}

// openLocked — адаптер сеанса, при надобности созданный. Под t.mu.
func (t *Tun) openLocked() (*tunAdapter, error) {
	if t.closed {
		return nil, errTunClosed
	}
	if t.adapter != nil {
		return t.adapter, nil
	}
	adapter, err := t.openAdapter(t.opts.Name, t.logf)
	if err != nil {
		return nil, fmt.Errorf("tun: %w", err)
	}
	t.adapter = adapter
	t.logf("[TUN] адаптер %q создан", t.opts.Name)
	return adapter, nil
}

// Start — первый TUNCONF попытки: адаптер получает адрес /32 (другой, чем в
// прошлой попытке, — заменяется), MTU TunnelMTU, поднимается, через него
// ставятся маршруты, затем — защита DNS и DNS системы. Возвращается, когда
// трафик системы идёт в туннель.
//
// Отказ адаптер не снимает: это отказ попытки, а следующая попробует снова.
// Отказ защиты DNS или DNS системы снимает и маршруты, как Suspend: туннель
// без них не остаётся. ctx Start не хранит: адаптер переживает попытку и
// снимается только Close.
func (t *Tun) Start(_ context.Context, conf TunConf) error {
	a := conf.Address.Unmap()
	if !a.Is4() || a.IsUnspecified() {
		return fmt.Errorf("tun: адрес туннеля %v — не IPv4", conf.Address)
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	adapter, err := t.openLocked()
	if err != nil {
		return err
	}
	if a != t.addr {
		if err := adapter.configure(a); err != nil {
			return fmt.Errorf("tun: адаптер %q: %w", t.opts.Name, err)
		}
		if t.addr.IsValid() {
			t.logf("[TUN] сервер выдал другой адрес: %v вместо %v", a, t.addr)
		}
		t.addr = a
	}
	if err := adapter.addRoutes(); err != nil {
		return fmt.Errorf("tun: маршруты: %w", err)
	}
	t.logf("[TUN] адаптер %q: адрес %v/32, MTU %d, маршруты %v", t.opts.Name, a, TunnelMTU, tunRoutes4)
	t.blockIPv6()
	t.dns = slices.Clone(conf.DNS)
	if err := t.applyDNS(adapter); err != nil {
		return errors.Join(fmt.Errorf("tun: %w", err), t.suspendLocked())
	}
	t.up = true
	return nil
}

// UpdateDNS — повторный TUNCONF с другим DNS: защита DNS и DNS системы
// переходят на новые резолверы (applyDNS). Адаптер и маршруты не меняются.
// Между попытками только запоминает: DNS поставит следующий Start.
//
// Отказ оставляет туннель поднятым, а защиту — пускающей резолверы, на
// которые указывает DNS системы, насколько это удалось (applyDNS).
func (t *Tun) UpdateDNS(dns []netip.Addr) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return errTunClosed
	}
	t.dns = slices.Clone(dns)
	if !t.up {
		return nil
	}
	if err := t.applyDNS(t.adapter); err != nil {
		return fmt.Errorf("tun: %w", err)
	}
	return nil
}

// applyDNS ставит защите DNS и DNS системы резолверы туннеля из t.dns. Под
// t.mu.
//
// Сначала защита, потом DNS системы: пока DNS системы прежний, запросы к
// нему защита отбрасывает, а не выпускает мимо туннеля. При смене DNS на
// ходу переход в три шага — защита пускает прежние и новые, DNS системы
// становится новым, защита пускает только новые, — так что нет ни мига,
// когда DNS системы ведёт туда, куда защита не пускает, ни мига без защиты:
// каждый Enable заменяет правила одним пакетом nftables.
func (t *Tun) applyDNS(adapter *tunAdapter) error {
	servers := t.tunnelResolvers()
	wide := servers
	if t.guardSet {
		wide = unionAddrs(t.guardDNS, servers)
	}
	if err := t.guardTo(wide); err != nil {
		return err
	}
	if !t.sysSet || !slices.Equal(servers, t.sysDNS) {
		if adapter.setDNS != nil {
			if err := adapter.setDNS(servers); err != nil {
				// DNS системы мог смениться и частично; защита пускает и
				// прежние резолверы, и новые.
				t.sysSet = false
				return err
			}
		}
		t.sysDNS, t.sysSet = servers, true
	}
	return t.guardTo(servers)
}

// blockIPv6 говорит защите DNS закрыть IPv6 системы наружу при следующем
// Enable: маршрутов IPv6 в туннель нет никогда, и без запрета IPv6 шёл бы
// мимо него. Локальная сеть IPv6 остаётся открытой, как и IPv4.
// Под t.mu. Зовётся перед каждым включением защиты — защита могла смениться
// между сеансами, а вызов дешёвый; строка в журнал — один раз.
func (t *Tun) blockIPv6() {
	blocker, ok := t.opts.DNSGuard.(IPv6Blocker)
	if ok {
		blocker.BlockIPv6(true)
	}
	if t.ipv6Told {
		return
	}
	t.ipv6Told = true
	if ok {
		t.logf("[TUN] IPv6 системы наружу закрыт защитой DNS, пока туннель поднят (открыты локальная сеть, "+
			"loopback и сокеты %s): туннель IPv6 не везёт", app.Name)
	} else {
		t.logf("[TUN] Внимание: туннель не везёт IPv6, а закрыть его без защиты DNS нечем — " +
			"IPv6 системы идёт мимо туннеля")
	}
}

// tunnelResolvers — резолверы туннеля, которые годятся системе: только
// IPv4. Маршрутов IPv6 в туннель нет, и запрос к резолверу IPv6 ушёл бы
// мимо него.
func (t *Tun) tunnelResolvers() []netip.Addr {
	servers := []netip.Addr{}
	var dropped []netip.Addr
	for _, a := range t.dns {
		if a = a.Unmap(); a.Is4() && !a.IsUnspecified() {
			servers = append(servers, a)
		} else {
			dropped = append(dropped, a)
		}
	}
	if len(dropped) > 0 {
		t.logf("[TUN] резолверы %v не годятся: туннель везёт только IPv4", dropped)
	}
	return servers
}

// guardTo ставит защите DNS резолверы servers, если стоят не они. Под t.mu.
//
// На отказе linuxsys.DNSGuard снимает правила целиком, поэтому прежний
// список ставится снова: с ним защита строже, чем без правил.
func (t *Tun) guardTo(servers []netip.Addr) error {
	g := t.opts.DNSGuard
	if g == nil || (t.guardSet && slices.Equal(servers, t.guardDNS)) {
		return nil
	}
	err := g.Enable(servers)
	if err == nil {
		t.guardDNS, t.guardSet = servers, true
		if len(servers) == 0 {
			t.logf("[TUN] Внимание: у туннеля нет резолверов IPv4 — защита DNS закрывает DNS системы целиком")
		} else {
			t.logf("[TUN] защита DNS: DNS системы — только к %v", servers)
		}
		return nil
	}
	if t.guardSet {
		if rerr := g.Enable(t.guardDNS); rerr != nil {
			t.guardSet = false
			err = errors.Join(err, rerr)
		}
	}
	return err
}

// Suspend снимает DNS системы, защиту DNS и маршруты и оставляет адаптер с
// адресом (Persistent): трафик системы идёт мимо туннеля, пока следующий
// Start их не вернёт.
func (t *Tun) Suspend() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.suspendLocked()
}

// suspendLocked — Suspend под t.mu. Порядок — обратный Start: DNS системы
// возвращается, пока защита ещё стоит (запросы к прежнему DNS на этот миг
// отбрасываются, а не уходят мимо туннеля), защита снимается раньше
// маршрутов (с kill-switch её Disable закрывает трафик системы, и мига,
// когда маршрутов уже нет, а закрыть ещё не успели, быть не должно).
// Отказ одного шага не отменяет следующих.
func (t *Tun) suspendLocked() error {
	if t.adapter == nil {
		return nil
	}
	t.up = false
	err := t.dropDNS()
	if rerr := t.adapter.removeRoutes(); rerr != nil {
		err = errors.Join(err, fmt.Errorf("tun: снятие маршрутов: %w", rerr))
	}
	return err
}

// dropDNS возвращает DNS системы и снимает защиту DNS. Под t.mu, при
// открытом адаптере.
func (t *Tun) dropDNS() error {
	var err error
	if t.adapter.revertDNS != nil {
		if rerr := t.adapter.revertDNS(); rerr != nil {
			err = fmt.Errorf("tun: %w", rerr)
		}
	}
	t.sysDNS, t.sysSet = nil, false
	if t.opts.DNSGuard != nil {
		if derr := t.opts.DNSGuard.Disable(); derr != nil {
			err = errors.Join(err, fmt.Errorf("tun: %w", derr))
		}
	}
	t.guardDNS, t.guardSet = nil, false
	return err
}

// Close возвращает DNS системы, снимает защиту DNS и убирает адаптер, а с
// ним адрес и маршруты. Безопасен до Start и при повторном вызове;
// одновременный вызов возвращается, только когда уборка закончена, —
// иначе процесс мог бы выйти посреди неё. Сессию защиты DNS Close не
// закрывает: она принадлежит вызывающему.
//
// Дубликат дескриптора, отданный ядру, держит адаптер, пока ядро живо:
// Close зовут, когда ядро уже вышло (супервизор не возвращается раньше).
func (t *Tun) Close() error {
	t.closeOnce.Do(func() { t.closeErr = t.close() })
	return t.closeErr
}

func (t *Tun) close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	t.up = false
	adapter := t.adapter
	if adapter == nil {
		return nil
	}
	// DNS и защита — раньше адаптера, по той же причине, что в Suspend.
	err := t.dropDNS()
	t.adapter, t.addr, t.dns = nil, netip.Addr{}, nil
	if cerr := adapter.close(); cerr != nil {
		return errors.Join(err, fmt.Errorf("tun: адаптер %q: %w", t.opts.Name, cerr))
	}
	t.logf("[TUN] адаптер %q снят", t.opts.Name)
	return err
}

// unionAddrs — адреса a, затем те из b, которых в a нет.
func unionAddrs(a, b []netip.Addr) []netip.Addr {
	out := slices.Clone(a)
	for _, x := range b {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

// dup — новый дубликат дескриптора адаптера, при надобности созданного.
// Под t.mu: Close не закроет дескриптор между проверкой и дубликатом.
func (t *Tun) dup() (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	adapter, err := t.openLocked()
	if err != nil {
		return -1, err
	}
	if adapter.dup == nil {
		return -1, errors.New("tun: дескриптор адаптера ядру на этой системе не передаётся")
	}
	fd, err := adapter.dup()
	if err != nil {
		return -1, fmt.Errorf("tun: дубликат дескриптора: %w", err)
	}
	return fd, nil
}

func (t *Tun) logf(format string, args ...any) {
	if t.opts.Logf != nil {
		t.opts.Logf(format, args...)
	}
}
