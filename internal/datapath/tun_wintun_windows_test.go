package datapath

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// wintunTestEnv включает тест с настоящим адаптером Wintun. Нужны права
// администратора и wintun.dll рядом с тестовым бинарём: Wintun ищет её
// только там и в System32. Из корня модуля:
//
//	go run ./scripts/wintun
//	go test -c -o build/out/datapath.test.exe ./internal/datapath
//	$env:CSQTT_TEST_WINTUN = "1"; build/out/datapath.test.exe -test.run Wintun -test.v
//
// Трафик машины тест не забирает: маршруты — на документационные префиксы
// (tunRoutes), GUID и мьютекс — свои, так что работающему csqtt-cli он не
// мешает. Но DNS машины на время теста медленнее: у адаптера метрика 0
// (SetTunnelRoutes), и клиент DNS Windows первым спрашивает его резолвер
// 192.0.2.53, которого нет, — запросы ждут, пока он не ответит.
const wintunTestEnv = "CSQTT_TEST_WINTUN"

// wintunTestGUID — GUID тестового адаптера, не winsys.AdapterGUID.
var wintunTestGUID = windows.GUID{
	Data1: 0x6f1c2d44, Data2: 0x0b7e, Data3: 0x4d35,
	Data4: [8]byte{0x9a, 0x61, 0x2e, 0x4f, 0x80, 0x13, 0xc7, 0x5b},
}

var (
	wintunTestRoute4 = netip.MustParsePrefix("198.51.100.0/24")
	wintunTestRoute6 = netip.MustParsePrefix("2001:db8:c5::/48")
	wintunTestPeer   = netip.AddrPortFrom(netip.MustParseAddr("198.51.100.7"), 4000)

	windowsDNSPlaceholder = netip.MustParsePrefix("fec0:0:0:ffff::/64")
)

// Настоящий адаптер: Open создаёт его, Start даёт адрес /32, MTU, маршруты
// и DNS, пакеты ходят между программой и «ядром» на loopback через мост в
// обе стороны, IPv6 отбрасывается, Suspend снимает маршруты и DNS, повторный
// Start возвращает их, UpdateDNS дважды подряд меняет DNS адаптера, Close
// убирает адаптер целиком.
func TestWintunAdapter(t *testing.T) {
	if os.Getenv(wintunTestEnv) != "1" {
		t.Skipf("тест с настоящим адаптером Wintun: %s=1, права администратора и %s рядом с тестовым бинарём", wintunTestEnv, WintunDLL)
	}
	if !windows.GetCurrentProcessToken().IsElevated() {
		t.Fatal("нужны права администратора")
	}
	savedGUID, savedRoutes := tunGUID, tunRoutes
	tunGUID, tunRoutes = wintunTestGUID, []netip.Prefix{wintunTestRoute4, wintunTestRoute6}
	t.Cleanup(func() { tunGUID, tunRoutes = savedGUID, savedRoutes })

	var log bridgeLog
	w := NewTun(TunOptions{Name: "csqtt-cli-test", Logf: func(f string, a ...any) {
		log.logf(f, a...)
		t.Logf(f, a...)
	}})
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = w.Close()
		}
	})
	if err := w.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	luid, err := winipcfg.LUIDFromGUID(&wintunTestGUID)
	if err != nil {
		t.Fatalf("адаптер по GUID не найден: %v", err)
	}
	link, err := w.Link()
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	core := fakeCore(t, link)

	addr := netip.MustParseAddr("192.0.2.2")
	dns := []netip.Addr{netip.MustParseAddr("192.0.2.53")}
	start := func(step string) {
		t.Helper()
		if err := w.Start(t.Context(), TunConf{Address: addr, DNS: dns}); err != nil {
			t.Fatalf("%s: Start: %v", step, err)
		}
		expectWintunState(t, luid, step, addr, true, dns)
	}
	start("первый Start")
	if mtu := interfaceMTU(t, luid, windows.AF_INET); mtu != TunnelMTU {
		t.Errorf("MTU IPv4 адаптера %d, ждали %d", mtu, TunnelMTU)
	}

	// Программа → адаптер → мост → ядро: UDP к адресу за маршрутом уходит
	// в адаптер с адресом туннеля.
	app, err := net.ListenUDP("udp4", &net.UDPAddr{IP: addr.AsSlice()})
	if err != nil {
		t.Fatalf("сокет на адресе туннеля: %v", err)
	}
	defer app.Close()
	appPort := uint16(app.LocalAddr().(*net.UDPAddr).Port)
	if _, err := app.WriteToUDPAddrPort([]byte("ping"), wintunTestPeer); err != nil {
		t.Fatalf("отправка в туннель: %v", err)
	}
	p := receiveUDP(t, core, netip.AddrPortFrom(addr, appPort), wintunTestPeer)
	if !bytes.Equal(p, []byte("ping")) {
		t.Fatalf("ядро получило данные %q", p)
	}

	// Ядро → мост → адаптер → программа: ответ с адреса за маршрутом.
	reply := udpPacket(wintunTestPeer, netip.AddrPortFrom(addr, appPort), []byte("pong"))
	send(t, core, link.Addr(), reply)
	_ = app.SetReadDeadline(time.Now().Add(3 * time.Second))
	b := make([]byte, 64)
	n, from, err := app.ReadFromUDPAddrPort(b)
	if err != nil || string(b[:n]) != "pong" || from != wintunTestPeer {
		t.Fatalf("программа получила %q от %v: %v", b[:n], from, err)
	}

	// IPv6 к адресу за маршрутом уходит в адаптер и отбрасывается мостом.
	if before, ok := sendIPv6(t, link); ok {
		deadline := time.Now().Add(2 * time.Second)
		for link.Stats().NotIPv4Out <= before && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if link.Stats().NotIPv4Out <= before {
			t.Errorf("пакет IPv6 не дошёл до моста: %+v", link.Stats())
		}
	}

	if err := w.Suspend(); err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	expectWintunState(t, luid, "после Suspend", addr, false, nil)
	start("Start после Suspend")

	// DNS на ходу дважды подряд: DNS адаптера заменяется, адрес и маршруты
	// остаются.
	for i, next := range [][]netip.Addr{
		{netip.MustParseAddr("192.0.2.54"), netip.MustParseAddr("192.0.2.55")},
		{netip.MustParseAddr("192.0.2.56")},
	} {
		if err := w.UpdateDNS(next); err != nil {
			t.Fatalf("UpdateDNS %d: %v", i+1, err)
		}
		expectWintunState(t, luid, fmt.Sprintf("после UpdateDNS %d", i+1), addr, true, next)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	closed = true
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := winipcfg.LUIDFromGUID(&wintunTestGUID)
		if err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("адаптер остался после Close")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if routeOnAnyInterface(t, windows.AF_INET, wintunTestRoute4) {
		t.Error("маршрут туннеля остался после Close")
	}
	if n := log.count("IPv6 системы уходит в адаптер и отбрасывается"); n != 1 {
		t.Errorf("строк об IPv6 %d, ждали одну", n)
	}
}

// expectWintunState проверяет адрес, маршруты и DNS адаптера.
func expectWintunState(t *testing.T, luid winipcfg.LUID, step string, addr netip.Addr, routed bool, dns []netip.Addr) {
	t.Helper()
	if _, err := luid.IPAddress(addr); err != nil {
		t.Errorf("%s: адреса %v на адаптере нет: %v", step, addr, err)
	}
	_, err := luid.Route(wintunTestRoute4, netip.IPv4Unspecified())
	if routed && err != nil {
		t.Errorf("%s: маршрута %v нет: %v", step, wintunTestRoute4, err)
	}
	if !routed && !errors.Is(err, windows.ERROR_NOT_FOUND) {
		t.Errorf("%s: маршрут %v на месте (%v)", step, wintunTestRoute4, err)
	}
	if hasIPv6(luid) {
		_, err := luid.Route(wintunTestRoute6, netip.IPv6Unspecified())
		if routed != (err == nil) {
			t.Errorf("%s: маршрут %v: %v, ждали поставлен=%v", step, wintunTestRoute6, err, routed)
		}
	}
	got, err := luid.DNS()
	if err != nil {
		t.Errorf("%s: DNS адаптера: %v", step, err)
	}
	// Без DNS IPv6 Windows показывает у интерфейса свои заглушки
	// fec0:0:0:ffff::1–3 (устаревшие site-local) — это не наши.
	got = slices.DeleteFunc(got, func(a netip.Addr) bool { return windowsDNSPlaceholder.Contains(a) })
	if !slices.Equal(got, dns) && !(len(got) == 0 && len(dns) == 0) {
		t.Errorf("%s: DNS адаптера %v, ждали %v", step, got, dns)
	}
}

func hasIPv6(luid winipcfg.LUID) bool {
	_, err := luid.IPInterface(windows.AF_INET6)
	return err == nil
}

func interfaceMTU(t *testing.T, luid winipcfg.LUID, family winipcfg.AddressFamily) int {
	t.Helper()
	ipif, err := luid.IPInterface(family)
	if err != nil {
		t.Fatalf("параметры интерфейса: %v", err)
	}
	return int(ipif.NLMTU)
}

func routeOnAnyInterface(t *testing.T, family winipcfg.AddressFamily, prefix netip.Prefix) bool {
	t.Helper()
	rows, err := winipcfg.GetIPForwardTable2(family)
	if err != nil {
		t.Fatalf("таблица маршрутов: %v", err)
	}
	for i := range rows {
		if rows[i].DestinationPrefix.Prefix() == prefix {
			return true
		}
	}
	return false
}

// sendIPv6 шлёт датаграм к адресу за маршрутом IPv6 и возвращает счётчик
// сброшенных мостом IPv6 до отправки; ok == false — IPv6 на машине нет или
// система не нашла адреса источника, проверять нечего.
func sendIPv6(t *testing.T, link *Link) (before uint64, ok bool) {
	t.Helper()
	c, err := net.ListenUDP("udp6", nil)
	if err != nil {
		t.Logf("IPv6 не проверен: %v", err)
		return 0, false
	}
	defer c.Close()
	target := netip.AddrPortFrom(netip.MustParseAddr("2001:db8:c5::7"), 4000)
	before = link.Stats().NotIPv4Out
	if _, err := c.WriteToUDPAddrPort([]byte("ping6"), target); err != nil {
		t.Logf("IPv6 не проверен: %v", err)
		return 0, false
	}
	return before, true
}

// receiveUDP ждёт на сокете ядра пакет IPv4 UDP from → to и возвращает его
// данные. Прочее, что система шлёт в новый адаптер (IGMP, LLMNR, NetBIOS),
// пропускается.
func receiveUDP(t *testing.T, core *net.UDPConn, from, to netip.AddrPort) []byte {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var seen []string
	for time.Now().Before(deadline) {
		p, _ := receive(t, core, time.Until(deadline))
		if p == nil {
			break
		}
		src, dst, data, ok := parseUDP(p)
		if ok && src == from && dst == to {
			return data
		}
		seen = append(seen, fmt.Sprintf("%d байт", len(p)))
	}
	t.Fatalf("ядро не получило UDP %v → %v; было: %s", from, to, strings.Join(seen, ", "))
	return nil
}

func parseUDP(p []byte) (src, dst netip.AddrPort, data []byte, ok bool) {
	if len(p) < 28 || p[0]>>4 != 4 || p[9] != 17 {
		return src, dst, nil, false
	}
	h := int(p[0]&0x0f) * 4
	if len(p) < h+8 {
		return src, dst, nil, false
	}
	sa, _ := netip.AddrFromSlice(p[12:16])
	da, _ := netip.AddrFromSlice(p[16:20])
	src = netip.AddrPortFrom(sa, binary.BigEndian.Uint16(p[h:]))
	dst = netip.AddrPortFrom(da, binary.BigEndian.Uint16(p[h+2:]))
	return src, dst, p[h+8:], true
}

// udpPacket — пакет IPv4 UDP с верными контрольными суммами.
func udpPacket(src, dst netip.AddrPort, data []byte) []byte {
	p := make([]byte, 28+len(data))
	p[0] = 0x45
	binary.BigEndian.PutUint16(p[2:], uint16(len(p)))
	p[8] = 64
	p[9] = 17
	s, d := src.Addr().As4(), dst.Addr().As4()
	copy(p[12:16], s[:])
	copy(p[16:20], d[:])
	binary.BigEndian.PutUint16(p[10:], checksum(p[:20], 0))
	u := p[20:]
	binary.BigEndian.PutUint16(u[0:], src.Port())
	binary.BigEndian.PutUint16(u[2:], dst.Port())
	binary.BigEndian.PutUint16(u[4:], uint16(len(u)))
	copy(u[8:], data)
	pseudo := uint32(17) + uint32(len(u))
	for i := 0; i < 4; i += 2 {
		pseudo += uint32(binary.BigEndian.Uint16(s[i:])) + uint32(binary.BigEndian.Uint16(d[i:]))
	}
	sum := checksum(u, pseudo)
	if sum == 0 {
		sum = 0xffff
	}
	binary.BigEndian.PutUint16(u[6:], sum)
	return p
}

func checksum(b []byte, sum uint32) uint16 {
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(b[i:]))
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	for sum > 0xffff {
		sum = sum>>16 + sum&0xffff
	}
	return ^uint16(sum)
}
