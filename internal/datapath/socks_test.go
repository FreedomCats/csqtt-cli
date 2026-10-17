package datapath

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/proxy"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	ip4 "gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
)

var (
	tunAddr  = netip.MustParseAddr("192.0.2.2")
	tunAddr2 = netip.MustParseAddr("192.0.2.3")
	// hostAddr — эхо на порту 7 и HTTP на 80.
	hostAddr = netip.MustParseAddr("198.51.100.1")
	// refuser — адрес без служб: на любой порт — отказ.
	refuser  = netip.MustParseAddr("198.51.100.2")
	dnsAddr  = netip.MustParseAddr("198.51.100.53")
	dns2Addr = netip.MustParseAddr("203.0.113.53")
	// deadDNS — по нему никто не отвечает: пакеты уходят в пустоту.
	deadDNS = netip.MustParseAddr("203.0.113.99")
)

// zone — имена сервера DNS; nil среди адресов — имя без записей A.
type zone map[string][]netip.Addr

// internet — сеть за туннелем: второй netstack за поддельным ядром, которое
// гоняет пакеты между ним и Link.
type internet struct {
	net *netstack

	mu sync.Mutex
	// zones — по серверу DNS; имени нет — NXDOMAIN.
	zones map[netip.Addr]zone
	// truncate — UDP-ответ с флагом TC, полный — только по TCP.
	truncate bool
	// failing — серверы, которые на всё отвечают SERVFAIL.
	failing map[netip.Addr]bool
}

func newInternet(t *testing.T, s *Socks) *internet {
	t.Helper()
	n, err := newNetstack(TunnelMTU)
	if err != nil {
		t.Fatal(err)
	}
	in := &internet{net: n, zones: map[netip.Addr]zone{
		dnsAddr: {
			"echo.example":  {hostAddr},
			"multi.example": {refuser, hostAddr},
			"noa.example":   nil,
		},
		dns2Addr: {"echo.example": {refuser}},
	}}
	for _, a := range []netip.Addr{hostAddr, refuser, dnsAddr, dns2Addr} {
		if err := n.addAddress(a); err != nil {
			t.Fatal(err)
		}
	}

	core := listenUDP(t)
	s.Link().SetCorePort(port(core))
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		b := make([]byte, MaxPacket)
		for {
			k, from, err := core.ReadFromUDPAddrPort(b)
			if err != nil {
				return
			}
			if from == s.Link().Addr() {
				n.inject(b[:k])
			}
		}
	}()
	go func() {
		defer wg.Done()
		for n.next(ctx, func(p []byte) { _, _ = core.WriteToUDPAddrPort(p, s.Link().Addr()) }) {
		}
	}()
	t.Cleanup(func() {
		cancel()
		_ = core.Close()
		wg.Wait()
		n.close()
	})

	in.serveEcho(t)
	in.serveHTTP(t)
	for _, a := range []netip.Addr{dnsAddr, dns2Addr} {
		in.serveDNS(t, a)
	}
	return in
}

func (in *internet) listen(t *testing.T, a netip.AddrPort) *gonet.TCPListener {
	t.Helper()
	ln, err := gonet.ListenTCP(in.net.stack,
		tcpip.FullAddress{NIC: nicID, Addr: tcpip.AddrFrom4(a.Addr().As4()), Port: a.Port()}, ip4.ProtocolNumber)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

// serveEcho — эхо: что пришло, то и обратно, а конец данных — полузакрытием.
func (in *internet) serveEcho(t *testing.T) {
	ln := in.listen(t, netip.AddrPortFrom(hostAddr, 7))
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_, _ = io.Copy(c, c)
				_ = c.(*gonet.TCPConn).CloseWrite()
				_, _ = io.Copy(io.Discard, c)
			}()
		}
	}()
}

// serveHTTP отвечает, на какой адрес пришёл запрос и с какого.
func (in *internet) serveHTTP(t *testing.T) {
	ln := in.listen(t, netip.AddrPortFrom(hostAddr, 80))
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		local := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
		fmt.Fprintf(w, "to %v from %v", local, r.RemoteAddr)
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
}

func (in *internet) serveDNS(t *testing.T, server netip.Addr) {
	udp, err := gonet.DialUDP(in.net.stack,
		&tcpip.FullAddress{NIC: nicID, Addr: tcpip.AddrFrom4(server.As4()), Port: 53}, nil, ip4.ProtocolNumber)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = udp.Close() })
	go func() {
		b := make([]byte, 512)
		for {
			n, from, err := udp.ReadFrom(b)
			if err != nil {
				return
			}
			in.mu.Lock()
			truncate := in.truncate
			in.mu.Unlock()
			if answer := in.answer(t, server, b[:n], truncate); answer != nil {
				_, _ = udp.WriteTo(answer, from)
			}
		}
	}()

	ln := in.listen(t, netip.AddrPortFrom(server, 53))
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				var size [2]byte
				if _, err := io.ReadFull(c, size[:]); err != nil {
					return
				}
				q := make([]byte, binary.BigEndian.Uint16(size[:]))
				if _, err := io.ReadFull(c, q); err != nil {
					return
				}
				a := in.answer(t, server, q, false)
				_, _ = c.Write(append(binary.BigEndian.AppendUint16(nil, uint16(len(a))), a...))
			}()
		}
	}()
}

func (in *internet) answer(t *testing.T, server netip.Addr, query []byte, truncate bool) []byte {
	var p dnsmessage.Parser
	h, err := p.Start(query)
	if err != nil {
		return nil
	}
	q, err := p.Question()
	if err != nil {
		return nil
	}
	in.mu.Lock()
	addrs, found := in.zones[server][strings.TrimSuffix(q.Name.String(), ".")]
	failing := in.failing[server]
	in.mu.Unlock()

	rh := dnsmessage.Header{ID: h.ID, Response: true, RecursionDesired: h.RecursionDesired, RecursionAvailable: true}
	switch {
	case failing:
		rh.RCode = dnsmessage.RCodeServerFailure
		addrs = nil
	case truncate:
		rh.Truncated = true
	case !found:
		rh.RCode = dnsmessage.RCodeNameError
	}
	b := dnsmessage.NewBuilder(nil, rh)
	b.EnableCompression()
	_ = b.StartQuestions()
	_ = b.Question(q)
	_ = b.StartAnswers()
	if !truncate {
		for _, a := range addrs {
			_ = b.AResource(dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: 300},
				dnsmessage.AResource{A: a.As4()})
		}
	}
	msg, err := b.Finish()
	if err != nil {
		t.Errorf("ответ DNS: %v", err)
	}
	return msg
}

func (in *internet) setZone(server netip.Addr, z zone) {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.zones[server] = z
}

func (in *internet) setFailing(server netip.Addr) {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.failing = map[netip.Addr]bool{server: true}
}

func (in *internet) setTruncate(v bool) {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.truncate = v
}

func newSocks(t *testing.T) *Socks {
	t.Helper()
	s, err := NewSocks(SocksOptions{Listen: "127.0.0.1:0", Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// started — Socks с сетью за туннелем и поднятым туннелем.
func started(t *testing.T, dns ...netip.Addr) (*Socks, *internet) {
	t.Helper()
	s := newSocks(t)
	in := newInternet(t, s)
	if dns == nil {
		dns = []netip.Addr{dnsAddr}
	}
	if err := s.Start(context.Background(), TunConf{Address: tunAddr, DNS: dns}); err != nil {
		t.Fatal(err)
	}
	return s, in
}

// socksDial — подключение через SOCKS5 своим клиентом: код ответа, адрес из
// ответа и соединение, при отказе — закрытое.
func socksDial(t *testing.T, s *Socks, cmd, atyp byte, addr []byte, port uint16) (byte, netip.AddrPort, *net.TCPConn) {
	t.Helper()
	c, err := net.DialTimeout("tcp", s.ListenAddr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	tc := c.(*net.TCPConn)
	t.Cleanup(func() { _ = tc.Close() })
	_ = tc.SetDeadline(time.Now().Add(40 * time.Second))
	req := []byte{5, 1, 0, 5, cmd, 0, atyp}
	if atyp == atypDomain {
		req = append(req, byte(len(addr)))
	}
	req = append(req, addr...)
	req = binary.BigEndian.AppendUint16(req, port)
	if _, err := tc.Write(req); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 12)
	if _, err := io.ReadFull(tc, reply); err != nil {
		t.Fatalf("ответ SOCKS5: %v", err)
	}
	if !bytes.Equal(reply[:2], []byte{5, 0}) || reply[2] != 5 || reply[5] != atypIPv4 {
		t.Fatalf("ответ SOCKS5: % x", reply)
	}
	bound := netip.AddrPortFrom(netip.AddrFrom4([4]byte(reply[6:10])), binary.BigEndian.Uint16(reply[10:]))
	_ = tc.SetDeadline(time.Time{})
	if reply[3] != repSucceeded {
		_ = tc.Close()
	}
	return reply[3], bound, tc
}

func connectName(t *testing.T, s *Socks, name string, port uint16) (byte, *net.TCPConn) {
	t.Helper()
	rep, _, c := socksDial(t, s, cmdConnect, atypDomain, []byte(name), port)
	return rep, c
}

// echo пишет data, полузакрывает соединение и ждёт всё обратно.
func echo(t *testing.T, c *net.TCPConn, data []byte) {
	t.Helper()
	_ = c.SetDeadline(time.Now().Add(30 * time.Second))
	go func() {
		_, _ = c.Write(data)
		_ = c.CloseWrite()
	}()
	got, err := io.ReadAll(c)
	if err != nil {
		t.Fatalf("эхо: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("эхо вернуло %d байт из %d", len(got), len(data))
	}
}

func random(t *testing.T, n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

// TCP-эхо через SOCKS5 по имени и по адресу: данные доходят целиком, конец
// данных — полузакрытием, а адрес из ответа — адрес туннеля.
func TestSocksEcho(t *testing.T) {
	s, _ := started(t)

	rep, bound, c := socksDial(t, s, cmdConnect, atypDomain, []byte("echo.example"), 7)
	if rep != repSucceeded {
		t.Fatalf("CONNECT по имени: код %#x", rep)
	}
	if bound.Addr() != tunAddr || bound.Port() == 0 {
		t.Errorf("адрес в ответе %v", bound)
	}
	echo(t, c, random(t, 1<<20))

	a := hostAddr.As4()
	if rep, _, c = socksDial(t, s, cmdConnect, atypIPv4, a[:], 7); rep != repSucceeded {
		t.Fatalf("CONNECT по адресу: код %#x", rep)
	}
	echo(t, c, []byte("ping"))

	// Адрес IPv4 строкой вместо имени — не запрос к DNS.
	if rep, c = connectName(t, s, "198.51.100.1", 7); rep != repSucceeded {
		t.Fatalf("CONNECT по адресу строкой: код %#x", rep)
	}
	echo(t, c, []byte("ping"))
}

// Сторонний клиент SOCKS5 (x/net/proxy) — на случай, если свой клиент и
// сервер ошибаются одинаково.
func TestSocksThirdPartyClient(t *testing.T) {
	s, _ := started(t)
	d, err := proxy.SOCKS5("tcp", s.ListenAddr().String(), nil, proxy.Direct)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Transport: &http.Transport{DialContext: d.(proxy.ContextDialer).DialContext},
		Timeout:   30 * time.Second,
	}
	resp, err := client.Get("http://echo.example/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if want := "to 198.51.100.1:80 from 192.0.2.2:"; !strings.HasPrefix(string(body), want) {
		t.Errorf("ответ %q, ожидалось начало %q", body, want)
	}
}

// curl --socks5-hostname через поддельное ядро получает ответ сервера за
// туннелем (итог C6 по плану).
func TestSocksCurl(t *testing.T) {
	curl, err := exec.LookPath("curl")
	if err != nil {
		t.Skip("curl не найден")
	}
	s, _ := started(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, curl, "-sS", "--max-time", "20",
		"--socks5-hostname", s.ListenAddr().String(), "http://echo.example/")
	cmd.Env = []string{} // без *_proxy окружения
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("curl: %v\n%s", err, out)
	}
	if want := "to 198.51.100.1:80 from 192.0.2.2:"; !strings.HasPrefix(string(out), want) {
		t.Errorf("curl получил %q, ожидалось начало %q", out, want)
	}
}

func TestSocksParallel(t *testing.T) {
	s, _ := started(t)
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			rep, c := connectName(t, s, "echo.example", 7)
			if rep != repSucceeded {
				t.Errorf("CONNECT: код %#x", rep)
				return
			}
			echo(t, c, random(t, 64<<10))
		})
	}
	wg.Wait()
}

func TestSocksReplies(t *testing.T) {
	s, _ := started(t)
	host := hostAddr.As4()
	v6 := netip.MustParseAddr("2001:db8::1").As16()
	cases := []struct {
		name      string
		cmd, atyp byte
		addr      []byte
		port      uint16
		rep       byte
	}{
		{"BIND", 0x02, atypIPv4, host[:], 7, repCommandNotSupported},
		{"UDP ASSOCIATE", 0x03, atypIPv4, host[:], 7, repCommandNotSupported},
		{"адрес IPv6", cmdConnect, atypIPv6, v6[:], 7, repAddressNotSupported},
		{"IPv6 строкой", cmdConnect, atypDomain, []byte("2001:db8::1"), 7, repAddressNotSupported},
		{"нет имени", cmdConnect, atypDomain, []byte("nx.example"), 7, repHostUnreachable},
		{"нет записи A", cmdConnect, atypDomain, []byte("noa.example"), 7, repHostUnreachable},
		{"пустое имя", cmdConnect, atypDomain, nil, 7, repHostUnreachable},
		{"порт закрыт", cmdConnect, atypIPv4, host[:], 9, repConnectionRefused},
	}
	for _, c := range cases {
		if rep, _, _ := socksDial(t, s, c.cmd, c.atyp, c.addr, c.port); rep != c.rep {
			t.Errorf("%s: код %#x, ожидался %#x", c.name, rep, c.rep)
		}
	}
}

// Неизвестный тип адреса: дочитать запрос нельзя, но код ответа уходит.
func TestSocksUnknownAddressType(t *testing.T) {
	s, _ := started(t)
	c, err := net.Dial("tcp", s.ListenAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = c.Write([]byte{5, 1, 0, 5, cmdConnect, 0, 0x09})
	reply := make([]byte, 12)
	if _, err := io.ReadFull(c, reply); err != nil {
		t.Fatal(err)
	}
	if reply[3] != repAddressNotSupported {
		t.Errorf("ответ % x", reply)
	}
}

// Клиент без метода «без пароля» получает отказ 0xFF.
func TestSocksNoAcceptableMethod(t *testing.T) {
	s := newSocks(t)
	c, err := net.Dial("tcp", s.ListenAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = c.Write([]byte{5, 1, 2})
	reply, _ := io.ReadAll(c)
	if !bytes.Equal(reply, []byte{5, 0xff}) {
		t.Errorf("ответ % x", reply)
	}
}

// У имени два адреса, первый отказывает — подключение идёт ко второму.
func TestSocksTriesEveryAddress(t *testing.T) {
	s, _ := started(t)
	rep, c := connectName(t, s, "multi.example", 7)
	if rep != repSucceeded {
		t.Fatalf("CONNECT: код %#x", rep)
	}
	echo(t, c, []byte("ping"))
}

// До первого Start — отказ «сеть недоступна» сразу, а не ожидание.
func TestSocksBeforeStart(t *testing.T) {
	s := newSocks(t)
	newInternet(t, s)
	begin := time.Now()
	if rep, _ := connectName(t, s, "echo.example", 7); rep != repNetworkUnreachable {
		t.Errorf("код %#x", rep)
	}
	if d := time.Since(begin); d > time.Second {
		t.Errorf("отказ через %v", d)
	}
}

// Без DNS по имени — отказ, по адресу — подключение.
func TestSocksWithoutDNS(t *testing.T) {
	s := newSocks(t)
	newInternet(t, s)
	if err := s.Start(context.Background(), TunConf{Address: tunAddr}); err != nil {
		t.Fatal(err)
	}
	if rep, _ := connectName(t, s, "echo.example", 7); rep != repHostUnreachable {
		t.Errorf("по имени: код %#x", rep)
	}
	if rep, _ := connectName(t, s, "198.51.100.1", 7); rep != repSucceeded {
		t.Errorf("по адресу: код %#x", rep)
	}
}

// Первый DNS молчит — ответ даёт второй.
func TestSocksDNSFallback(t *testing.T) {
	s := newSocks(t)
	newInternet(t, s)
	s.res.mu.Lock()
	s.res.timeout = 200 * time.Millisecond
	s.res.mu.Unlock()
	if err := s.Start(context.Background(), TunConf{Address: tunAddr, DNS: []netip.Addr{deadDNS, dnsAddr}}); err != nil {
		t.Fatal(err)
	}
	if rep, _ := connectName(t, s, "echo.example", 7); rep != repSucceeded {
		t.Errorf("код %#x", rep)
	}
}

// Обрезанный ответ по UDP — повтор по TCP.
func TestSocksDNSOverTCP(t *testing.T) {
	s, in := started(t)
	in.setTruncate(true)
	if rep, _ := connectName(t, s, "echo.example", 7); rep != repSucceeded {
		t.Errorf("код %#x", rep)
	}
}

func get(t *testing.T, c *net.TCPConn) string {
	t.Helper()
	_ = c.SetDeadline(time.Now().Add(30 * time.Second))
	if _, err := c.Write([]byte("GET / HTTP/1.0\r\nHost: x\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(c)
	if err != nil {
		t.Fatal(err)
	}
	_, body, _ := strings.Cut(string(b), "\r\n\r\n")
	return body
}

// Повторный TUNCONF с другим DNS: имена ищутся у нового сервера, прежние
// ответы забыты, открытые соединения живут.
func TestSocksUpdateDNS(t *testing.T) {
	s, in := started(t)
	rep, open := connectName(t, s, "echo.example", 7)
	if rep != repSucceeded {
		t.Fatalf("CONNECT: код %#x", rep)
	}
	if err := s.UpdateDNS([]netip.Addr{dns2Addr}); err != nil {
		t.Fatal(err)
	}
	// У нового сервера имени больше нет: ответ из кэша прежнего не годится.
	in.setZone(dns2Addr, zone{})
	if rep, _ := connectName(t, s, "echo.example", 7); rep != repHostUnreachable {
		t.Errorf("после UpdateDNS: код %#x", rep)
	}
	echo(t, open, []byte("ping"))
}

// Новая попытка с тем же адресом — соединения живут; с другим — рвутся, а
// новые идут с нового адреса.
func TestSocksAddressChange(t *testing.T) {
	s, _ := started(t)
	rep, kept := connectName(t, s, "echo.example", 7)
	if rep != repSucceeded {
		t.Fatalf("CONNECT: код %#x", rep)
	}
	rep, dropped := connectName(t, s, "echo.example", 7)
	if rep != repSucceeded {
		t.Fatalf("CONNECT: код %#x", rep)
	}
	if err := s.Start(context.Background(), TunConf{Address: tunAddr, DNS: []netip.Addr{dnsAddr}}); err != nil {
		t.Fatal(err)
	}
	echo(t, kept, []byte("ping"))

	if err := s.Start(context.Background(), TunConf{Address: tunAddr2, DNS: []netip.Addr{dnsAddr}}); err != nil {
		t.Fatal(err)
	}
	_ = dropped.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := dropped.Read(make([]byte, 1)); err == nil || isTimeout(err) {
		t.Errorf("соединение прежнего адреса живо: %v", err)
	}
	rep, c := connectName(t, s, "echo.example", 80)
	if rep != repSucceeded {
		t.Fatalf("CONNECT: код %#x", rep)
	}
	if body, want := get(t, c), "from 192.0.2.3:"; !strings.Contains(body, want) {
		t.Errorf("ответ %q, ожидалось %q", body, want)
	}
}

func isTimeout(err error) bool {
	ne, ok := err.(net.Error)
	return ok && ne.Timeout()
}

// Между попытками порта ядра нет, и пакеты сбрасываются; TCP повторяет их,
// и соединение переживает перезапуск ядра.
func TestSocksSurvivesCoreRestart(t *testing.T) {
	s, _ := started(t)
	rep, c := connectName(t, s, "echo.example", 7)
	if rep != repSucceeded {
		t.Fatalf("CONNECT: код %#x", rep)
	}
	corePort := s.Link().core.Load()
	s.Link().SetCorePort(0)
	_ = c.SetDeadline(time.Now().Add(30 * time.Second))
	if _, err := c.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if s.Link().Stats().NoCore == 0 {
		t.Error("без ядра пакеты не сбрасывались")
	}
	s.Link().SetCorePort(uint16(corePort))
	got := make([]byte, 4)
	if _, err := io.ReadFull(c, got); err != nil || string(got) != "ping" {
		t.Errorf("после перезапуска: %q, %v", got, err)
	}
}

func TestSocksClose(t *testing.T) {
	s, _ := started(t)
	rep, c := connectName(t, s, "echo.example", 7)
	if rep != repSucceeded {
		t.Fatalf("CONNECT: код %#x", rep)
	}
	done := make(chan error, 1)
	go func() {
		_, err := c.Read(make([]byte, 1))
		done <- err
	}()
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close не вернулся за 5 с")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Error("соединение живо после Close")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("соединение не закрылось за 5 с")
	}
	if c, err := net.DialTimeout("tcp", s.ListenAddr().String(), time.Second); err == nil {
		c.Close()
		t.Error("SOCKS5 принимает после Close")
	}
	if err := s.Close(); err != nil {
		t.Errorf("повторный Close: %v", err)
	}
	if err := s.Start(context.Background(), TunConf{Address: tunAddr}); err == nil {
		t.Error("Start после Close без ошибки")
	}
}

func TestNewSocksRejectsAddress(t *testing.T) {
	for _, listen := range []string{"0.0.0.0:1080", "192.0.2.1:1080", "[::]:1080", "localhost:1080", "1080"} {
		if s, err := NewSocks(SocksOptions{Listen: listen}); err == nil {
			s.Close()
			t.Errorf("NewSocks(%q) без ошибки", listen)
		}
	}
}

func TestNewSocksBusyPort(t *testing.T) {
	s := newSocks(t)
	if s2, err := NewSocks(SocksOptions{Listen: s.ListenAddr().String()}); err == nil {
		s2.Close()
		t.Fatal("второй SOCKS5 на занятом порту")
	}
}

func TestSocksStartRejectsAddress(t *testing.T) {
	s := newSocks(t)
	for _, a := range []netip.Addr{{}, netip.IPv4Unspecified(), netip.MustParseAddr("2001:db8::2")} {
		if err := s.Start(context.Background(), TunConf{Address: a}); err == nil {
			t.Errorf("Start(%v) без ошибки", a)
		}
	}
}
