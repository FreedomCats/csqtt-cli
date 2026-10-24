package cli

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	"github.com/FreedomCats/csqtt-cli/internal/datapath"
	"github.com/FreedomCats/csqtt-cli/internal/netguard"
)

// netnsEnv включает проверки на настоящих адаптере и nftables. Только в
// отдельном сетевом пространстве (контейнер с NET_ADMIN и /dev/net/tun), не
// на машине разработчика:
//
//	CGO_ENABLED=0 go test -c -o build/out/cli.test ./internal/cli
//	docker run --rm --cap-add NET_ADMIN --device /dev/net/tun \
//	  --sysctl net.ipv6.conf.all.disable_ipv6=0 -e CSQTT_TEST_NETNS=1 \
//	  -v "$PWD/build/out":/csqtt-cli:ro ubuntu:latest /csqtt-cli/cli.test -test.v -test.run 'Netns$'
//
// Без --sysctl … disable_ipv6=0 проверки IPv6 пропускаются (SKIP подтеста).
const netnsEnv = "CSQTT_TEST_NETNS"

// Kill-switch на настоящих Tun и linuxsys.DNSGuard, открытых так, как их
// откроет connect (setupDNSGuard): до Start и после Suspend трафик системы
// наружу закрыт — кроме сокетов с меткой netguard.SocketMark, loopback и
// локальной сети (DNS закрыт и к ней); после Start — только защита DNS:
// трафик идёт в адаптер, DNS — лишь к резолверу туннеля IPv4, IPv6 наружу
// закрыт, локальный IPv6 открыт, как в «закрыто». Капча мимо прокси снимает «закрыто» на время решения. После
// закрытия сессии правил нет.
//
// Публичные адреса — документационные: 198.51.100.1 уходит по маршруту
// контейнера, 203.0.113.5 — после Start в адаптер. Локальная сеть —
// 192.168.1.5, тоже по маршруту по умолчанию.
func TestKillSwitchTunNetns(t *testing.T) {
	if os.Getenv(netnsEnv) != "1" || os.Geteuid() != 0 {
		t.Skipf("меняет сеть: только в контейнере, %s=1 и root", netnsEnv)
	}
	// TestMain закрыл настоящую защиту от тестов; здесь она и проверяется.
	stubOpenDNSGuardFunc(t, openSystemDNSGuard)
	const name = "csqttks0"
	mark := int(netguard.SocketMark)
	ipv6 := ipv6Dummy(t)
	checkIPv6 := func(step string, f func(t *testing.T)) {
		t.Helper()
		t.Run("IPv6 "+step, func(t *testing.T) {
			if !ipv6 {
				t.Skip("IPv6 в контейнере выключен: нужен --sysctl net.ipv6.conf.all.disable_ipv6=0")
			}
			f(t)
		})
	}
	// Локальный IPv6 открыт и в «закрыто», и при поднятом туннеле: link-local,
	// ULA и мультикаст проходят, DNS к ним — нет.
	localIPv6 := func(t *testing.T, step string) {
		t.Helper()
		expectSend(t, step, "udp", "[fe80::5%csqttks6]:443", 0, nil)
		expectSend(t, step, "udp", "[fd00:db8:1::5]:443", 0, nil)
		expectSend(t, step, "udp", "[ff02::fb%csqttks6]:5353", 0, nil)
		expectSend(t, step, "udp", "[fe80::5%csqttks6]:53", 0, unix.EPERM)
		expectSend(t, step, "udp", "[fd00:db8:1::5]:53", 0, unix.EPERM)
	}

	var stdout, stderr bytes.Buffer
	setup, err := setupDNSGuard(newPrinter(&stdout, false), &stderr, false, "")
	if err != nil {
		t.Fatalf("setupDNSGuard: %v", err)
	}
	closed := false
	defer func() {
		if !closed {
			setup.close()
		}
	}()
	ks := setup.killSwitch
	if ks == nil {
		t.Fatalf("kill-switch не включился; вывод:\n%s%s", stdout.String(), stderr.String())
	}

	locked := func(step string) {
		t.Helper()
		if !ks.Locked() {
			t.Errorf("%s: признак «закрыто» не стоит", step)
		}
		expectSend(t, step, "udp", "198.51.100.1:443", 0, unix.EPERM)
		expectSend(t, step, "udp", "198.51.100.1:443", mark, nil)
		expectSend(t, step, "udp", "198.51.100.1:53", mark, nil)
		expectSend(t, step, "udp", "192.168.1.5:443", 0, nil)
		expectSend(t, step, "udp", "192.168.1.5:53", 0, unix.EPERM)
		expectSend(t, step, "udp", "127.0.0.1:53", 0, nil)
		checkIPv6(step, func(t *testing.T) {
			expectSend(t, step, "udp", "[2001:db8:1::5]:443", 0, unix.EPERM)
			expectSend(t, step, "udp", "[2001:db8:1::5]:443", mark, nil)
			expectSend(t, step, "udp", "[::1]:443", 0, nil)
			localIPv6(t, step)
		})
	}

	locked("до Start")

	w := datapath.NewTun(datapath.TunOptions{Name: name, Logf: t.Logf, DNSGuard: setup.guard})
	defer w.Close()
	ctx := context.Background()
	// Резолвер IPv6 туннель не везёт: ни системе, ни защите он не достаётся.
	conf := datapath.TunConf{Address: netip.MustParseAddr("192.0.2.2"),
		DNS: []netip.Addr{netip.MustParseAddr("198.51.100.53"), netip.MustParseAddr("fd00:db8:1::53")}}
	if err := w.Start(ctx, conf); err != nil {
		t.Fatalf("Start: %v", err)
	}
	up := func(step string) {
		t.Helper()
		if ks.Locked() {
			t.Errorf("%s: признак «закрыто» стоит при поднятом туннеле", step)
		}
		if got := routeLink(t, "203.0.113.5"); got != name {
			t.Errorf("%s: 203.0.113.5 уходит в %q, ждали адаптер %s", step, got, name)
		}
		expectSend(t, step, "udp", "203.0.113.5:443", 0, nil)
		expectSend(t, step, "udp", "203.0.113.5:53", 0, unix.EPERM)
		expectSend(t, step, "udp", "198.51.100.53:53", 0, nil)
		expectSend(t, step, "udp", "198.51.100.1:53", mark, nil)
		checkIPv6(step, func(t *testing.T) {
			expectSend(t, step, "udp", "[2001:db8:1::5]:443", 0, unix.EPERM)
			expectSend(t, step, "udp", "[2001:db8:1::5]:443", mark, nil)
			expectSend(t, step, "udp", "[::1]:443", 0, nil)
			expectSend(t, step, "udp", "[fd00:db8:1::53]:53", 0, unix.EPERM)
			localIPv6(t, step)
		})
	}
	up("после Start")

	if err := w.Suspend(); err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	locked("после Suspend")

	// Капча мимо прокси: на время решения правил нет, потом — снова
	// «закрыто».
	done := ks.captchaDirect()
	expectSend(t, "капча", "udp", "198.51.100.1:443", 0, nil)
	if ks.Locked() {
		t.Error("капча: признак «закрыто» стоит")
	}
	done()
	locked("после капчи")

	if err := w.Start(ctx, conf); err != nil {
		t.Fatalf("Start второй попытки: %v", err)
	}
	up("после Start второй попытки")

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	locked("после Close адаптера")

	setup.close()
	closed = true
	expectSend(t, "после закрытия сессии", "udp", "198.51.100.1:443", 0, nil)
	expectSend(t, "после закрытия сессии", "udp", "192.168.1.5:53", 0, nil)

	for _, want := range []string{"защита DNS:", "kill-switch: пока туннель не поднят",
		"kill-switch снят на время капчи", "kill-switch возвращён"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("в выводе нет %q:\n%s", want, stdout.String())
		}
	}
}

// expectSend отправляет пакет на address сокетом с меткой mark (0 — без
// неё) и сверяет ошибку отправки с want: отброшенный на выходе пакет UDP —
// EPERM при отправке.
func expectSend(t *testing.T, step, network, address string, mark int, want error) {
	t.Helper()
	conn, err := markedDialer(mark).Dial(network, address)
	if err == nil {
		_, err = conn.Write([]byte("csqtt-cli"))
		conn.Close()
	}
	switch {
	case want == nil && err != nil:
		t.Errorf("%s: %s %s, метка %#x: %v, ждали успех", step, network, address, mark, err)
	case want != nil && !errors.Is(err, want):
		t.Errorf("%s: %s %s, метка %#x: %v, ждали %v", step, network, address, mark, err, want)
	}
}

func markedDialer(mark int) *net.Dialer {
	d := &net.Dialer{Timeout: 2 * time.Second}
	if mark != 0 {
		d.Control = func(_, _ string, c syscall.RawConn) error {
			var serr error
			if err := c.Control(func(fd uintptr) {
				serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MARK, mark)
			}); err != nil {
				return err
			}
			return serr
		}
	}
	return d
}

// routeLink — через какой интерфейс система отправит пакет на addr.
func routeLink(t *testing.T, addr string) string {
	t.Helper()
	routes, err := netlink.RouteGet(net.ParseIP(addr))
	if err != nil || len(routes) == 0 {
		t.Fatalf("маршрут до %s: %v", addr, err)
	}
	link, err := netlink.LinkByIndex(routes[0].LinkIndex)
	if err != nil {
		t.Fatalf("интерфейс маршрута до %s: %v", addr, err)
	}
	return link.Attrs().Name
}

// ipv6Dummy заводит интерфейс csqttks6 с 2001:db8:1::2/64 и ULA
// fd00:db8:1::2/64, чтобы пакетам IPv6 было куда уйти (link-local и
// мультикаст — через него же); false — IPv6 в контейнере выключен.
func ipv6Dummy(t *testing.T) bool {
	t.Helper()
	link := &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: "csqttks6"}}
	if err := netlink.LinkAdd(link); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { netlink.LinkDel(link) })
	if err := netlink.LinkSetUp(link); err != nil {
		t.Fatal(err)
	}
	for i, a := range []string{"2001:db8:1::2/64", "fd00:db8:1::2/64"} {
		addr, _ := netlink.ParseAddr(a)
		addr.Flags = unix.IFA_F_NODAD
		if err := netlink.AddrAdd(link, addr); err != nil {
			if i > 0 {
				t.Fatal(err)
			}
			t.Logf("IPv6 не проверяется: %v", err)
			return false
		}
	}
	return true
}
