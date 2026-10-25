package datapath

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	ip4 "gvisor.dev/gvisor/pkg/tcpip/network/ipv4"

	"github.com/FreedomCats/csqtt-cli/internal/coreproc"
	"github.com/FreedomCats/csqtt-cli/internal/linuxsys"
)

// Адреса проверки прямого пути. «Физический» интерфейс — свой TUN в
// 198.51.100.0/24 со шлюзом .1; за ним «сайт» на 203.0.113.80 — netstack
// теста. Адрес туннеля — из подсети, которую выдаёт сервер CSQTT. Пир ядра
// — недостижимый документационный адрес: воркеры до него не дойдут, прямому
// пути он не нужен.
var (
	directPhysAddr = netip.MustParsePrefix("198.51.100.2/24")
	directPhysGW   = netip.MustParseAddr("198.51.100.1")
	directSite     = netip.MustParseAddr("203.0.113.80")
	directSiteNet  = netip.MustParsePrefix("203.0.113.0/24")
	directTunAddr  = netip.MustParseAddr("10.66.67.2")
)

const (
	directPhysName = "csqttphy0"
	directTunName  = "csqttdir0"
	directMark     = 0x1075
	directTCPPort  = 80
	directUDPPort  = 7
)

// Прямой путь настоящего ядра (маршрутизация мимо туннеля, правка П3 для
// direct_relay.rs) на настоящей маршрутизации, как в datapath tun: маршруты
// /1 ведут в адаптер, маршрут по умолчанию — через «физический» интерфейс.
// Ядро запущено с -bind-if и -mark и получает правило «203.0.113.0/24 —
// мимо туннеля» той же командой UPDATE_DOMAINS, что оболочка шлёт по правилу
// сервера. Прямое TCP-соединение и UDP до «сайта» проходят, сайт видит
// адрес физического интерфейса; все пакеты ядра к сайту на физическом
// интерфейсе несут метку; в адаптер к адресам сайта — ни пакета (петли нет);
// в «закрыто» kill-switch прямой путь работает, а сокет без метки тем же
// интерфейсом — нет. Без -bind-if то же соединение ядро отправляет в
// адаптер — так видно, что проверка петлю ловит.
//
// Пакеты приложения ядру — по Link, как в datapath socks и в tun на Windows:
// в Linux-tun (-tun-uds) ядро выбрасывает всё, что прочло из адаптера, пока
// нет ни одного воркера (dispatcher.rs, read_tun), а воркеров без сервера
// CSQTT нет. Дальше путь общий: dispatch → DirectRelay. Адаптер здесь читает
// тест — что ядро по ошибке отправит в него, считается и обратно не
// возвращается.
//
// Нужны CSQTT_TEST_CORE (абсолютный путь к csqtt-core внутри контейнера) и
// нестрогий обратный фильтр: ответы сайта приходят на физический интерфейс, а
// маршрут к сайту ведёт в адаптер (rpfilter_linux.go):
//
//	CGO_ENABLED=0 go test -c -o build/out/datapath.test ./internal/datapath
//	docker run --rm --cap-add NET_ADMIN --device /dev/net/tun \
//	  --sysctl net.ipv4.conf.all.rp_filter=2 -e CSQTT_TEST_NETNS=1 \
//	  -e CSQTT_TEST_CORE=/csqtt-cli/csqtt-core -v "$PWD/build/out":/csqtt-cli:ro \
//	  ubuntu:latest /csqtt-cli/datapath.test -test.v -test.run TestRealCoreNetnsDirect
//
// Имя не кончается на Netns: задание netns в CI ядра не имеет, и там тест
// пропустился бы.
func TestRealCoreNetnsDirect(t *testing.T) {
	skipOutsideNetns(t)
	corePath := os.Getenv("CSQTT_TEST_CORE")
	if corePath == "" {
		t.Skip("CSQTT_TEST_CORE не задан")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	site := newDirectNet(t, ctx)
	if mode, err := linuxsys.ReversePathFilter(directPhysName); err != nil {
		t.Fatal(err)
	} else if mode == linuxsys.RPFilterStrict {
		t.Fatalf("на %s строгий rp_filter: ответы сайта будут отброшены; запустите контейнер с "+
			"--sysctl net.ipv4.conf.all.rp_filter=2", directPhysName)
	}

	w := NewTun(TunOptions{Name: directTunName, Logf: t.Logf})
	defer w.Close()
	if err := w.Start(ctx, TunConf{Address: directTunAddr}); err != nil {
		t.Fatalf("Start адаптера: %v", err)
	}
	// Условие петли: сокет без привязки ушёл бы к сайту в адаптер.
	if got := routeLinkName(t, directSite); got != directTunName {
		t.Fatalf("маршрут к %v — через %q, ждали адаптер %s", directSite, got, directTunName)
	}
	tunFD, err := w.dup()
	if err != nil {
		t.Fatal(err)
	}
	// Пакеты к сайту, попавшие в адаптер: всего и по протоколам. Дескриптор
	// закрывается после читателя: иначе адаптер переживёт тест на время его
	// poll, и повтор теста в том же процессе не создаст его заново.
	var looped, loopedTCP, loopedUDP atomic.Int64
	var firstLooped atomic.Value
	readCtx, stopRead := context.WithCancel(ctx)
	readDone := make(chan struct{})
	defer func() {
		stopRead()
		<-readDone
		unix.Close(tunFD)
	}()
	go func() {
		defer close(readDone)
		readTunPackets(readCtx, tunFD, func(p []byte) {
			if dst, ok := ipv4Dst(p); ok && directSiteNet.Contains(dst) {
				switch p[9] {
				case unix.IPPROTO_TCP:
					loopedTCP.Add(1)
				case unix.IPPROTO_UDP:
					loopedUDP.Add(1)
				}
				if looped.Add(1) == 1 {
					firstLooped.Store(describePacket(p))
				}
			}
		})
	}()

	counters := newMarkCounters(t)

	// Сторона приложения: netstack с адресом туннеля, пакеты — по Link.
	link, err := ListenLink()
	if err != nil {
		t.Fatal(err)
	}
	defer link.Close()
	client, err := newNetstack(TunnelMTU)
	if err != nil {
		t.Fatal(err)
	}
	defer client.close()
	if err := client.addAddress(directTunAddr); err != nil {
		t.Fatal(err)
	}
	go client.pump(ctx, link)

	route, err := (&coreproc.RoutingRule{IPs: []string{directSiteNet.String()}}).Update()
	if err != nil || !route.IPs {
		t.Fatalf("правило: %+v, %v", route, err)
	}
	cfg := coreproc.Config{
		Peer:       "192.0.2.1:46000",
		Password:   "direct-test-password",
		Hashes:     []string{"direct-test-hash"},
		Listen:     CoreListen,
		ListenPeer: link.Addr().String(),
		BindIf:     uint32(site.index),
		Mark:       directMark,
	}

	t.Run("bind-if", func(t *testing.T) {
		core := startDirectCore(t, corePath, cfg, link, route.Command)
		defer core.stop()

		conn := directTCP(t, ctx, client)
		defer conn.Close()
		if from := site.nextTCP(t); from.Addr() != directPhysAddr.Addr() {
			t.Errorf("сайт видит TCP с %v, ждали адрес физического интерфейса %v", from, directPhysAddr.Addr())
		}
		directUDP(t, client)
		if from := site.nextUDP(t); from.Addr() != directPhysAddr.Addr() {
			t.Errorf("сайт видит UDP с %v, ждали %v", from, directPhysAddr.Addr())
		}
		// Соединение ещё открыто: все пакеты ядра к сайту до сих пор — от
		// его сокетов, а у них метка.
		marked, all := counters.read(t)
		t.Logf("пакетов к сайту через %s: %d, с меткой %#x: %d", directPhysName, all, directMark, marked)
		if all == 0 || marked != all {
			t.Errorf("к сайту через %s ушло %d пакетов, с меткой — %d: ждали, что все с меткой", directPhysName, all, marked)
		}

		// «Закрыто»: kill-switch пропускает только сокеты с меткой.
		guard, err := linuxsys.OpenDNSGuard(directMark)
		if err != nil {
			t.Fatalf("OpenDNSGuard: %v", err)
		}
		defer guard.Close()
		if err := guard.Lockdown(); err != nil {
			t.Fatalf("Lockdown: %v", err)
		}
		locked := directTCP(t, ctx, client)
		locked.Close()
		if from := site.nextTCP(t); from.Addr() != directPhysAddr.Addr() {
			t.Errorf("в «закрыто» сайт видит TCP с %v", from)
		}
		directUDP(t, client)
		site.nextUDP(t)
		// Сокет без метки тем же интерфейсом «закрыто» не выпускает: прямой
		// путь прошёл именно по метке.
		if err := sendBound(site.index, netip.AddrPortFrom(directSite, directUDPPort)); !errors.Is(err, unix.EPERM) {
			t.Errorf("в «закрыто» UDP без метки через %s: %v, ждали EPERM", directPhysName, err)
		}
		if err := guard.Close(); err != nil {
			t.Errorf("закрытие защиты: %v", err)
		}

		if n := looped.Load(); n != 0 {
			t.Errorf("в адаптер ушло %d пакетов к %v, первый: %v", n, directSiteNet, firstLooped.Load())
		}
		if n := site.accepted.Load(); n != 2 {
			t.Errorf("сайт принял %d TCP-соединений, ждали 2 — по одному на каждое соединение приложения", n)
		}
	})
	if t.Failed() {
		return
	}

	// Без привязки и метки ядро соединяется с сайтом маршрутом /1 — через
	// адаптер. В datapath tun такой SYN ядро прочло бы из адаптера и снова
	// отправило «напрямую»: петля.
	t.Run("без bind-if", func(t *testing.T) {
		unbound := cfg
		unbound.BindIf, unbound.Mark = 0, 0
		core := startDirectCore(t, corePath, unbound, link, route.Command)
		defer core.stop()
		accepted := site.accepted.Load()
		// Повтор датаграма в directUDP мог дойти до сайта вдобавок к первому:
		// его запись — от прежнего ядра.
		for len(site.udpFrom) > 0 {
			<-site.udpFrom
		}
		conn, err := client.dialTCP(ctx, netip.AddrPortFrom(directSite, directTCPPort))
		if err != nil {
			// Рукопожатие с приложением ядро завершает само, до соединения с
			// сайтом: отказа здесь быть не должно.
			t.Fatalf("TCP через ядро: %v", err)
		}
		defer conn.Close()
		udp, err := client.dialUDP(netip.AddrPortFrom(directSite, directUDPPort))
		if err != nil {
			t.Fatal(err)
		}
		defer udp.Close()
		if _, err := udp.Write([]byte("csqtt-unbound-udp")); err != nil {
			t.Fatalf("UDP через ядро: %v", err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for (loopedTCP.Load() == 0 || loopedUDP.Load() == 0) && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		if loopedTCP.Load() == 0 || loopedUDP.Load() == 0 {
			t.Errorf("без -bind-if в адаптер к %v попало TCP %d, UDP %d пакетов, ждали оба — проверка петли ничего не ловит",
				directSiteNet, loopedTCP.Load(), loopedUDP.Load())
		} else {
			t.Logf("без -bind-if в адаптер ушло к %v: TCP %d, UDP %d пакетов, первый: %v",
				directSiteNet, loopedTCP.Load(), loopedUDP.Load(), firstLooped.Load())
		}
		if n := site.accepted.Load(); n != accepted {
			t.Errorf("без -bind-if сайт принял соединение через физический интерфейс")
		}
		select {
		case from := <-site.udpFrom:
			t.Errorf("без -bind-if UDP дошёл до сайта с %v", from)
		default:
		}
	})
}

// directNet — «физический» интерфейс и «сайт» за ним.
type directNet struct {
	index    int
	accepted atomic.Int64
	tcpFrom  chan netip.AddrPort
	udpFrom  chan netip.AddrPort
}

// newDirectNet заводит TUN directPhysName с адресом directPhysAddr и
// маршрутом по умолчанию через directPhysGW. Пакеты из него получает
// netstack «сайта» с адресом directSite: TCP-эхо на directTCPPort и UDP-эхо
// на directUDPPort; адрес каждого собеседника сайт сообщает в tcpFrom и
// udpFrom. Маршрут по умолчанию docker остаётся с меньшей метрикой: сокету,
// привязанному к интерфейсу, ядро Linux ищет маршрут только через этот
// интерфейс.
func newDirectNet(t *testing.T, ctx context.Context) *directNet {
	t.Helper()
	fd, err := createTUN(directPhysName)
	if err != nil {
		t.Fatalf("физический интерфейс: %v", err)
	}
	t.Cleanup(func() { unix.Close(fd) })
	link, err := netlink.LinkByName(directPhysName)
	if err != nil {
		t.Fatal(err)
	}
	addr, _ := netlink.ParseAddr(directPhysAddr.String())
	if err := netlink.AddrAdd(link, addr); err != nil {
		t.Fatal(err)
	}
	if err := netlink.LinkSetUp(link); err != nil {
		t.Fatal(err)
	}
	route := &netlink.Route{LinkIndex: link.Attrs().Index, Gw: directPhysGW.AsSlice(), Priority: 50}
	if err := netlink.RouteAdd(route); err != nil {
		t.Fatalf("маршрут по умолчанию через %s: %v", directPhysName, err)
	}

	ns, err := newNetstack(1500)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ns.close)
	if err := ns.addAddress(directSite); err != nil {
		t.Fatal(err)
	}
	// Горутины, которые держат fd, останавливаются раньше, чем он закроется
	// (t.Cleanup — в обратном порядке): иначе интерфейс переживёт тест на время
	// poll, и повтор теста в том же процессе не создаст его заново.
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		readTunPackets(ctx, fd, func(p []byte) {
			if len(p) > 0 && p[0]>>4 == 4 {
				ns.inject(p)
			}
		})
	}()
	go func() {
		defer wg.Done()
		for ns.next(ctx, func(p []byte) { _, _ = unix.Write(fd, p) }) {
		}
	}()
	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})

	s := &directNet{index: link.Attrs().Index, tcpFrom: make(chan netip.AddrPort, 16), udpFrom: make(chan netip.AddrPort, 16)}
	local := tcpip.FullAddress{NIC: nicID, Addr: tcpip.AddrFrom4(directSite.As4()), Port: directTCPPort}
	ln, err := gonet.ListenTCP(ns.stack, local, ip4.ProtocolNumber)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.accepted.Add(1)
			s.tcpFrom <- c.RemoteAddr().(*net.TCPAddr).AddrPort()
			go func() {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	local.Port = directUDPPort
	pc, err := gonet.DialUDP(ns.stack, &local, nil, ip4.ProtocolNumber)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			s.udpFrom <- from.(*net.UDPAddr).AddrPort()
			_, _ = pc.WriteTo(buf[:n], from)
		}
	}()
	return s
}

func (s *directNet) nextTCP(t *testing.T) netip.AddrPort {
	t.Helper()
	return nextFrom(t, s.tcpFrom, "TCP")
}

func (s *directNet) nextUDP(t *testing.T) netip.AddrPort {
	t.Helper()
	return nextFrom(t, s.udpFrom, "UDP")
}

func nextFrom(t *testing.T, ch <-chan netip.AddrPort, what string) netip.AddrPort {
	t.Helper()
	select {
	case from := <-ch:
		return netip.AddrPortFrom(from.Addr().Unmap(), from.Port())
	case <-time.After(5 * time.Second):
		t.Fatalf("сайт не получил %s", what)
		return netip.AddrPort{}
	}
}

// directTCP соединяется через ядро с TCP-эхо сайта и проверяет эхо.
func directTCP(t *testing.T, ctx context.Context, client *netstack) net.Conn {
	t.Helper()
	conn, err := client.dialTCP(ctx, netip.AddrPortFrom(directSite, directTCPPort))
	if err != nil {
		t.Fatalf("TCP через ядро: %v", err)
	}
	const msg = "csqtt-direct-tcp"
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Write([]byte(msg)); err != nil {
		conn.Close()
		t.Fatalf("TCP через ядро, запись: %v", err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != msg {
		conn.Close()
		t.Fatalf("TCP-эхо сайта через ядро: %q, %v", buf, err)
	}
	return conn
}

// directUDP шлёт через ядро датаграм UDP-эхо сайта и ждёт его обратно.
func directUDP(t *testing.T, client *netstack) {
	t.Helper()
	conn, err := client.dialUDP(netip.AddrPortFrom(directSite, directUDPPort))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	const msg = "csqtt-direct-udp"
	buf := make([]byte, 64)
	// Первый датаграм ядро могло получить раньше, чем завело сокет сессии:
	// UDP повторяется, как повторило бы приложение.
	for i := 0; i < 5; i++ {
		if _, err := conn.Write([]byte(msg)); err != nil {
			t.Fatalf("UDP через ядро: %v", err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		n, err := conn.Read(buf)
		if err == nil && string(buf[:n]) == msg {
			return
		}
	}
	t.Fatal("UDP-эхо сайта через ядро не вернулось")
}

// directCore — запущенное ядро и его журнал: при провале теста он
// печатается.
type directCore struct {
	p        *coreproc.Process
	mu       sync.Mutex
	log      []string
	done     chan struct{}
	stopOnce sync.Once
}

// startDirectCore запускает ядро, ждёт LISTEN, отдаёт порт Link и шлёт
// команду маршрутизации route; возвращается, когда ядро её применило.
func startDirectCore(t *testing.T, path string, cfg coreproc.Config, link *Link, route string) *directCore {
	t.Helper()
	p, err := coreproc.Start(path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	c := &directCore{p: p, done: make(chan struct{})}
	listening, routed := make(chan struct{}), make(chan struct{})
	var listenOnce, routeOnce sync.Once
	go func() {
		defer close(c.done)
		for m := range p.Messages() {
			switch m := m.(type) {
			case coreproc.ListenEvent:
				link.SetCorePort(uint16(m.Port))
				listenOnce.Do(func() { close(listening) })
			case coreproc.Log:
				c.mu.Lock()
				c.log = append(c.log, m.Text)
				c.mu.Unlock()
				if strings.Contains(m.Text, "Горячее обновление доменов") {
					routeOnce.Do(func() { close(routed) })
				}
			default:
				c.mu.Lock()
				c.log = append(c.log, fmt.Sprintf("%T %+v", m, m))
				c.mu.Unlock()
			}
		}
	}()
	t.Cleanup(func() {
		c.stop()
		if t.Failed() {
			c.mu.Lock()
			defer c.mu.Unlock()
			t.Logf("журнал ядра:\n%s", strings.Join(c.log, "\n"))
		}
	})
	wait := func(ch <-chan struct{}, what string) {
		t.Helper()
		select {
		case <-ch:
		case <-c.done:
			t.Fatalf("ядро вышло до %s: %v", what, p.Wait())
		case <-time.After(30 * time.Second):
			t.Fatalf("ядро не дошло до %s", what)
		}
	}
	wait(listening, "LISTEN")
	if err := p.Send(route); err != nil {
		t.Fatal(err)
	}
	wait(routed, "применения UPDATE_DOMAINS")
	return c
}

func (c *directCore) stop() {
	c.stopOnce.Do(func() { _ = c.p.Stop() })
	<-c.done
}

// markCounters — счётчики nftables исходящих пакетов к сайту через
// физический интерфейс: всех и с меткой directMark. Цепочка — после
// таблицы защиты: считается то, что она выпустила.
type markCounters struct {
	table *nftables.Table
	chain *nftables.Chain
}

func newMarkCounters(t *testing.T) *markCounters {
	t.Helper()
	conn, err := nftables.New()
	if err != nil {
		t.Fatal(err)
	}
	table := conn.AddTable(&nftables.Table{Name: "csqtttest", Family: nftables.TableFamilyIPv4})
	accept := nftables.ChainPolicyAccept
	chain := conn.AddChain(&nftables.Chain{
		Name:     "output",
		Table:    table,
		Type:     nftables.ChainTypeFilter,
		Hooknum:  nftables.ChainHookOutput,
		Priority: nftables.ChainPriorityRef(*nftables.ChainPriorityFilter + 10),
		Policy:   &accept,
	})
	ifname := make([]byte, unix.IFNAMSIZ)
	copy(ifname, directPhysName)
	toSite := []expr.Any{
		&expr.Meta{Key: expr.MetaKeyOIFNAME, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: ifname},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 16, Len: 4},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: directSite.AsSlice()},
	}
	marked := append(append([]expr.Any{}, toSite...),
		&expr.Meta{Key: expr.MetaKeyMARK, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.NativeEndian.PutUint32(directMark)},
		&expr.Counter{})
	all := append(append([]expr.Any{}, toSite...), &expr.Counter{})
	conn.AddRule(&nftables.Rule{Table: table, Chain: chain, Exprs: marked})
	conn.AddRule(&nftables.Rule{Table: table, Chain: chain, Exprs: all})
	if err := conn.Flush(); err != nil {
		t.Fatalf("nftables: %v", err)
	}
	t.Cleanup(func() {
		conn.DelTable(table)
		_ = conn.Flush()
	})
	return &markCounters{table: table, chain: chain}
}

// read — пакетов с меткой и всех.
func (m *markCounters) read(t *testing.T) (marked, all uint64) {
	t.Helper()
	conn, err := nftables.New()
	if err != nil {
		t.Fatal(err)
	}
	rules, err := conn.GetRules(m.table, m.chain)
	if err != nil || len(rules) != 2 {
		t.Fatalf("правила счётчиков: %d, %v", len(rules), err)
	}
	var n [2]uint64
	for i, r := range rules {
		for _, e := range r.Exprs {
			if c, ok := e.(*expr.Counter); ok {
				n[i] = c.Packets
			}
		}
	}
	return n[0], n[1]
}

// readTunPackets отдаёт f пакеты из дескриптора адаптера fd до отмены ctx.
func readTunPackets(ctx context.Context, fd int, f func([]byte)) {
	buf := make([]byte, MaxPacket)
	for ctx.Err() == nil {
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		if n, err := unix.Poll(fds, 100); err != nil || n == 0 || fds[0].Revents&unix.POLLIN == 0 {
			if err != nil && err != unix.EINTR {
				return
			}
			continue
		}
		n, err := unix.Read(fd, buf)
		if err != nil {
			return
		}
		f(buf[:n])
	}
}

// ipv4Dst — адрес назначения пакета IPv4.
func ipv4Dst(p []byte) (netip.Addr, bool) {
	if len(p) < 20 || p[0]>>4 != 4 {
		return netip.Addr{}, false
	}
	return netip.AddrFrom4([4]byte(p[16:20])), true
}

// describePacket — источник, назначение и протокол пакета IPv4 для журнала.
func describePacket(p []byte) string {
	dst, _ := ipv4Dst(p)
	src := netip.AddrFrom4([4]byte(p[12:16]))
	return fmt.Sprintf("%v → %v, протокол %d", src, dst, p[9])
}

// routeLinkName — через какой интерфейс система отправит пакет на addr.
func routeLinkName(t *testing.T, addr netip.Addr) string {
	t.Helper()
	routes, err := netlink.RouteGet(addr.AsSlice())
	if err != nil || len(routes) == 0 {
		t.Fatalf("маршрут до %v: %v", addr, err)
	}
	link, err := netlink.LinkByIndex(routes[0].LinkIndex)
	if err != nil {
		t.Fatal(err)
	}
	return link.Attrs().Name
}

// sendBound отправляет датаграм на to сокетом без метки, привязанным к
// интерфейсу index, — как сокет ядра, только без метки.
func sendBound(index int, to netip.AddrPort) error {
	d := &net.Dialer{Control: func(_, _ string, c syscall.RawConn) error {
		var serr error
		if err := c.Control(func(fd uintptr) {
			serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_BINDTOIFINDEX, index)
		}); err != nil {
			return err
		}
		return serr
	}}
	conn, err := d.Dial("udp4", to.String())
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.Write([]byte("csqtt-unmarked"))
	return err
}
