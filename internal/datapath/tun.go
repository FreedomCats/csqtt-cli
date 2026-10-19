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
}

// Tun — datapath с системным адаптером: весь трафик системы идёт в туннель
// (architecture.md §05). На Linux пакеты ходят между адаптером и ядром
// напрямую: ядро получает дескриптор адаптера (SendFD, Р4).
//
// Tun — Persistent: адаптер живёт весь csqtt-cli connect. Создаётся он при
// первой надобности — Open, первый SendFD или первый Start, — Start каждой
// попытки задаёт ему адрес из TUNCONF и ставит маршруты, Suspend между
// попытками их снимает, Close убирает адаптер.
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
// мимо туннеля. Где своего адаптера нет — Unsupported.
func (t *Tun) Requires() Requirements {
	return Requirements{Unsupported: tunUnsupported(), Admin: true, Netguard: true}
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
// прошлой попытке, — заменяется), MTU TunnelMTU, поднимается, и через него
// ставятся маршруты. Возвращается, когда трафик системы идёт в туннель.
//
// Отказ адаптер не снимает: это отказ попытки, а следующая попробует снова.
// ctx Start не хранит: адаптер переживает попытку и снимается только Close.
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
	t.setDNS(conf.DNS)
	t.logf("[TUN] адаптер %q: адрес %v/32, MTU %d, маршруты %v", t.opts.Name, a, TunnelMTU, tunRoutes4)
	return nil
}

// UpdateDNS — повторный TUNCONF с другим DNS. Адаптер и маршруты не
// меняются.
func (t *Tun) UpdateDNS(dns []netip.Addr) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return errTunClosed
	}
	t.setDNS(dns)
	return nil
}

// setDNS запоминает DNS туннеля. Под t.mu.
//
// DNS системы и защита DNS сюда добавятся с таблицей nftables (задача L3):
// без неё запросы шли бы мимо туннеля, поэтому connect до L3 tun и не
// включает.
func (t *Tun) setDNS(dns []netip.Addr) {
	if slices.Equal(dns, t.dns) {
		return
	}
	t.dns = slices.Clone(dns)
	t.logf("[TUN] DNS туннеля %v", t.dns)
}

// Suspend снимает маршруты и оставляет адаптер с адресом (Persistent):
// трафик системы идёт мимо туннеля, пока следующий Start их не вернёт.
func (t *Tun) Suspend() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.adapter == nil {
		return nil
	}
	if err := t.adapter.removeRoutes(); err != nil {
		return fmt.Errorf("tun: снятие маршрутов: %w", err)
	}
	return nil
}

// Close убирает адаптер, а с ним адрес и маршруты. Безопасен до Start и при
// повторном вызове; одновременный вызов возвращается, только когда уборка
// закончена, — иначе процесс мог бы выйти посреди неё.
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
	adapter := t.adapter
	t.adapter, t.addr, t.dns = nil, netip.Addr{}, nil
	if adapter == nil {
		return nil
	}
	if err := adapter.close(); err != nil {
		return fmt.Errorf("tun: адаптер %q: %w", t.opts.Name, err)
	}
	t.logf("[TUN] адаптер %q снят", t.opts.Name)
	return nil
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
