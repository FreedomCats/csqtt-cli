package datapath

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAdapter — адаптер без системы: запоминает вызовы, может отказать.
type fakeAdapter struct {
	mu           sync.Mutex
	configured   []netip.Addr
	routesAdded  int
	routesRemove int
	closed       int
	configureErr error
	// closeGate — если задан, close ждёт его закрытия.
	closeGate chan struct{}
	// routes6 — addRoutes ставит и маршруты IPv6, как на Windows (dropsIPv6).
	routes6 bool
	// presentErr — ответ проверки, что адаптер на месте (present).
	presentErr error

	// DNS системы и общий с fakeDNSGuard журнал (tun_dns_test.go).
	routed    bool
	sysDNS    []netip.Addr // nil — DNS системы прежний
	setDNSErr error
	events    []string
	guard     *fakeDNSGuard
	t         *testing.T
}

func (f *fakeAdapter) adapter() *tunAdapter {
	var a *tunAdapter
	a = &tunAdapter{
		configure: func(addr netip.Addr) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.configureErr != nil {
				return f.configureErr
			}
			f.configured = append(f.configured, addr)
			return nil
		},
		addRoutes: func() error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.routesAdded++
			f.routed = true
			f.events = append(f.events, "routes")
			a.dropsIPv6 = f.routes6
			return nil
		},
		removeRoutes: func() error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.routesRemove++
			f.routed = false
			f.events = append(f.events, "unroute")
			return nil
		},
		setDNS:    f.setDNS,
		revertDNS: f.revertDNS,
		present: func() error {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.presentErr
		},
		close: func() error {
			if f.closeGate != nil {
				<-f.closeGate
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			f.closed++
			f.events = append(f.events, "close")
			return nil
		},
	}
	return a
}

func (f *fakeAdapter) state() (configured []netip.Addr, added, removed, closed int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.configured), f.routesAdded, f.routesRemove, f.closed
}

// fakeTun — Tun над fakeAdapter; opens — сколько раз открывался адаптер.
func fakeTun(f *fakeAdapter, openErr error) (*Tun, *int) {
	opens := 0
	w := NewTun(TunOptions{})
	w.openAdapter = func(string, func(string, ...any), func(error)) (*tunAdapter, error) {
		opens++
		if openErr != nil {
			return nil, openErr
		}
		return f.adapter(), nil
	}
	return w, &opens
}

func conf(addr string, dns ...string) TunConf {
	c := TunConf{Address: netip.MustParseAddr(addr)}
	for _, d := range dns {
		c.DNS = append(c.DNS, netip.MustParseAddr(d))
	}
	return c
}

// Адаптер открывается один раз на сеанс. Start с тем же адресом только
// возвращает маршруты, с другим — переадресует тот же адаптер; Suspend
// снимает маршруты, Close — адаптер, после него Start отказывает.
func TestTunLifecycle(t *testing.T) {
	f := &fakeAdapter{}
	w, opens := fakeTun(f, nil)
	ctx := context.Background()

	if err := w.Start(ctx, conf("192.0.2.2", "198.51.100.53")); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := w.Suspend(); err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	if err := w.Start(ctx, conf("192.0.2.2", "198.51.100.53")); err != nil {
		t.Fatalf("Start тем же адресом: %v", err)
	}
	if err := w.Start(ctx, conf("192.0.2.3", "198.51.100.54")); err != nil {
		t.Fatalf("Start другим адресом: %v", err)
	}
	configured, added, removed, closed := f.state()
	want := []netip.Addr{netip.MustParseAddr("192.0.2.2"), netip.MustParseAddr("192.0.2.3")}
	if *opens != 1 || !slices.Equal(configured, want) || added != 3 || removed != 1 || closed != 0 {
		t.Errorf("открытий %d, адреса %v, маршруты поставлены %d и сняты %d раз, закрытий %d",
			*opens, configured, added, removed, closed)
	}
	if !slices.Equal(w.dns, conf("192.0.2.3", "198.51.100.54").DNS) {
		t.Errorf("DNS после Start: %v", w.dns)
	}
	if err := w.UpdateDNS(conf("192.0.2.3", "198.51.100.55").DNS); err != nil || w.dns[0] != netip.MustParseAddr("198.51.100.55") {
		t.Errorf("UpdateDNS: %v, DNS %v", err, w.dns)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("повторный Close: %v", err)
	}
	if _, _, _, closed := f.state(); closed != 1 {
		t.Errorf("адаптер закрыт %d раз", closed)
	}
	if err := w.Start(ctx, conf("192.0.2.2")); !errors.Is(err, errTunClosed) {
		t.Errorf("Start после Close: %v", err)
	}
	if err := w.Open(); !errors.Is(err, errTunClosed) {
		t.Errorf("Open после Close: %v", err)
	}
	if _, err := w.dup(); !errors.Is(err, errTunClosed) {
		t.Errorf("дубликат после Close: %v", err)
	}
	if err := w.Suspend(); err != nil {
		t.Errorf("Suspend после Close: %v", err)
	}
	if *opens != 1 {
		t.Errorf("после Close адаптер открывался снова: %d", *opens)
	}
}

// До Start ни Suspend, ни Close адаптер не открывают.
func TestTunCloseBeforeStart(t *testing.T) {
	w, opens := fakeTun(&fakeAdapter{}, nil)
	if err := w.Suspend(); err != nil {
		t.Errorf("Suspend до Start: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("Close до Start: %v", err)
	}
	if *opens != 0 {
		t.Errorf("адаптер открыт %d раз", *opens)
	}
}

// Адрес не IPv4 — отказ до системы.
func TestTunStartRejectsNonIPv4(t *testing.T) {
	w, opens := fakeTun(&fakeAdapter{}, nil)
	for _, a := range []string{"2001:db8::2", "0.0.0.0"} {
		if err := w.Start(context.Background(), conf(a)); err == nil || !strings.Contains(err.Error(), "не IPv4") {
			t.Errorf("Start(%s): %v", a, err)
		}
	}
	if err := w.Start(context.Background(), TunConf{}); err == nil {
		t.Error("Start без адреса принят")
	}
	if *opens != 0 {
		t.Errorf("адаптер открыт %d раз", *opens)
	}
}

// Отказ открытия — отказ Start; следующий Start пробует снова.
func TestTunOpenFailure(t *testing.T) {
	w, opens := fakeTun(&fakeAdapter{}, errTunBusy)
	for range 2 {
		if err := w.Start(context.Background(), conf("192.0.2.2")); !errors.Is(err, errTunBusy) {
			t.Errorf("Start: %v", err)
		}
	}
	if err := w.Open(); !errors.Is(err, errTunBusy) {
		t.Errorf("Open: %v", err)
	}
	if *opens != 3 {
		t.Errorf("открытий %d, ждали 3", *opens)
	}
}

// Отказ настройки адреса оставляет адаптер: следующий Start пробует
// задать адрес снова, а маршрутов без адреса нет.
func TestTunConfigureFailure(t *testing.T) {
	f := &fakeAdapter{configureErr: errors.New("нет прав")}
	w, opens := fakeTun(f, nil)
	defer w.Close()
	if err := w.Start(context.Background(), conf("192.0.2.2")); err == nil || !strings.Contains(err.Error(), "нет прав") {
		t.Fatalf("Start: %v", err)
	}
	f.mu.Lock()
	f.configureErr = nil
	f.mu.Unlock()
	if err := w.Start(context.Background(), conf("192.0.2.2")); err != nil {
		t.Fatalf("повторный Start: %v", err)
	}
	configured, added, _, closed := f.state()
	if *opens != 1 || len(configured) != 1 || added != 1 || closed != 0 {
		t.Errorf("открытий %d, адреса %v, маршрутов %d, закрытий %d", *opens, configured, added, closed)
	}
}

// Одновременные Close возвращаются только после конца уборки.
func TestTunConcurrentClose(t *testing.T) {
	f := &fakeAdapter{closeGate: make(chan struct{})}
	w, _ := fakeTun(f, nil)
	if err := w.Open(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 3)
	for range 3 {
		go func() { done <- w.Close() }()
	}
	select {
	case <-done:
		t.Fatal("Close вернулся, пока адаптер не закрыт")
	case <-time.After(50 * time.Millisecond):
	}
	close(f.closeGate)
	for range 3 {
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Close: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Close не вернулся")
		}
	}
	if _, _, _, closed := f.state(); closed != 1 {
		t.Errorf("адаптер закрыт %d раз", closed)
	}
}

func TestTunRequires(t *testing.T) {
	r := NewTun(TunOptions{}).Requires()
	if !r.Admin || !r.Netguard {
		t.Errorf("Requires: %+v", r)
	}
	if w := NewTun(TunOptions{}); w.opts.Name != TunName {
		t.Errorf("имя по умолчанию %q", w.opts.Name)
	}
}

var _ Failing = (*Tun)(nil)

// Адаптер сломался сам: Failed отдаёт причину один раз, с именем адаптера.
func TestTunFailed(t *testing.T) {
	f := &fakeAdapter{}
	w := NewTun(TunOptions{Name: "csqtt-test"})
	var lost func(error)
	w.openAdapter = func(_ string, _ func(string, ...any), l func(error)) (*tunAdapter, error) {
		lost = l
		return f.adapter(), nil
	}
	if err := w.Open(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-w.Failed():
		t.Fatalf("Failed до пропажи: %v", err)
	default:
	}
	gone := errors.New("система закрыла адаптер")
	lost(gone)
	lost(errors.New("второй раз"))
	var err error
	select {
	case err = <-w.Failed():
	case <-time.After(time.Second):
		t.Fatal("Failed молчит")
	}
	var le *AdapterLostError
	if !errors.As(err, &le) || le.Name != "csqtt-test" || !errors.Is(err, gone) {
		t.Errorf("Failed: %v", err)
	}
	select {
	case err := <-w.Failed():
		t.Errorf("Failed второй раз: %v", err)
	default:
	}
	if err := w.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// CheckAdapter: до открытия адаптера и после Close — ошибка, открытый —
// ответ проверки платформы. Lost — тот же Failed, что у адаптера, пропавшего
// сам.
func TestTunCheckAdapterAndLost(t *testing.T) {
	f := &fakeAdapter{}
	w, _ := fakeTun(f, nil)
	if err := w.CheckAdapter(); err == nil || !strings.Contains(err.Error(), "не создан") {
		t.Errorf("CheckAdapter до Open: %v", err)
	}
	if err := w.Open(); err != nil {
		t.Fatal(err)
	}
	if err := w.CheckAdapter(); err != nil {
		t.Errorf("CheckAdapter: %v", err)
	}
	gone := errors.New(`адаптера "csqtt-cli" (индекс 7) в системе больше нет`)
	f.mu.Lock()
	f.presentErr = gone
	f.mu.Unlock()
	if err := w.CheckAdapter(); !errors.Is(err, gone) {
		t.Errorf("CheckAdapter без адаптера: %v", err)
	}

	lost := errors.New("ядро потеряло дескриптор адаптера")
	w.Lost(lost)
	w.Lost(errors.New("второй раз"))
	var le *AdapterLostError
	select {
	case err := <-w.Failed():
		if !errors.As(err, &le) || le.Name != TunName || !errors.Is(err, lost) {
			t.Errorf("Failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Failed молчит")
	}
	select {
	case err := <-w.Failed():
		t.Errorf("Failed второй раз: %v", err)
	default:
	}

	if err := w.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if err := w.CheckAdapter(); !errors.Is(err, errTunClosed) {
		t.Errorf("CheckAdapter после Close: %v", err)
	}
}
