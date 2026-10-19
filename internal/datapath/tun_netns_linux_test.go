package datapath

import (
	"context"
	"encoding/binary"
	"net"
	"net/netip"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// netnsEnv включает проверки, которые создают настоящий адаптер. Только в
// отдельном сетевом пространстве (контейнер с NET_ADMIN и /dev/net/tun), не
// на машине разработчика:
//
//	CGO_ENABLED=0 go test -c -o build/out/datapath.test ./internal/datapath
//	docker run --rm --cap-add NET_ADMIN --device /dev/net/tun -e CSQTT_TEST_NETNS=1 \
//	  -v "$PWD/build/out":/csqtt-cli:ro ubuntu:latest /csqtt-cli/datapath.test -test.v -test.run Netns
const netnsEnv = "CSQTT_TEST_NETNS"

func skipOutsideNetns(t *testing.T) {
	t.Helper()
	if os.Getenv(netnsEnv) != "1" || os.Geteuid() != 0 {
		t.Skipf("создаёт адаптер: только в контейнере, %s=1 и root", netnsEnv)
	}
}

// linkState — адреса глобальной области интерфейса name, его MTU и поднят ли
// он.
func linkState(t *testing.T, name string) (index int, addrs []netip.Prefix, mtu int, up bool) {
	t.Helper()
	link, err := netlink.LinkByName(name)
	if err != nil {
		t.Fatalf("интерфейс %s: %v", name, err)
	}
	list, err := netlink.AddrList(link, netlink.FAMILY_ALL)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range list {
		if a.Scope == unix.RT_SCOPE_UNIVERSE {
			ip, _ := netip.AddrFromSlice(a.IP)
			ones, _ := a.Mask.Size()
			addrs = append(addrs, netip.PrefixFrom(ip.Unmap(), ones))
		}
	}
	return link.Attrs().Index, addrs, link.Attrs().MTU, link.Attrs().Flags&net.FlagUp != 0
}

// linkRoutes — маршруты таблицы main через интерфейс name обоих семейств,
// кроме тех, что ядро ставит само под адреса.
func linkRoutes(t *testing.T, name string) []netip.Prefix {
	t.Helper()
	link, err := netlink.LinkByName(name)
	if err != nil {
		t.Fatalf("интерфейс %s: %v", name, err)
	}
	list, err := netlink.RouteList(link, netlink.FAMILY_ALL)
	if err != nil {
		t.Fatal(err)
	}
	var out []netip.Prefix
	for _, r := range list {
		if r.Dst == nil || r.Protocol == unix.RTPROT_KERNEL {
			continue
		}
		ones, _ := r.Dst.Mask.Size()
		ip, _ := netip.AddrFromSlice(r.Dst.IP)
		out = append(out, netip.PrefixFrom(ip.Unmap(), ones))
	}
	slices.SortFunc(out, func(a, b netip.Prefix) int { return a.Addr().Compare(b.Addr()) })
	return out
}

func linkExists(name string) bool {
	_, err := netlink.LinkByName(name)
	return err == nil
}

// tunInfo — имя адаптера и флаги дескриптора fd (TUNGETIFF).
func tunInfo(t *testing.T, fd int) (string, uint16) {
	t.Helper()
	ifr, err := unix.NewIfreq("")
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.IoctlIfreq(fd, unix.TUNGETIFF, ifr); err != nil {
		t.Fatalf("TUNGETIFF: %v", err)
	}
	return ifr.Name(), ifr.Uint16()
}

// readUDP ждёт из дескриптора адаптера пакет IPv4/UDP к dst с payload;
// прочее (IPv6 самого ядра Linux и т. п.) пропускает.
func readUDP(t *testing.T, fd int, dst netip.AddrPort, payload string) netip.Addr {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	buf := make([]byte, MaxPacket)
	for time.Now().Before(deadline) {
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		if n, err := unix.Poll(fds, 100); err != nil && err != unix.EINTR {
			t.Fatalf("poll: %v", err)
		} else if n == 0 {
			continue
		}
		n, err := unix.Read(fd, buf)
		if err != nil {
			t.Fatalf("чтение из адаптера: %v", err)
		}
		p := buf[:n]
		if n < 28 || p[0]>>4 != 4 || p[9] != unix.IPPROTO_UDP {
			continue
		}
		ihl := int(p[0]&0x0f) * 4
		to := netip.AddrPortFrom(netip.AddrFrom4([4]byte(p[16:20])), binary.BigEndian.Uint16(p[ihl+2:]))
		if to == dst && string(p[ihl+8:]) == payload {
			return netip.AddrFrom4([4]byte(p[12:16]))
		}
	}
	t.Fatalf("пакет к %v с %q не пришёл в адаптер", dst, payload)
	return netip.Addr{}
}

// udpPacket — пакет IPv4/UDP без контрольной суммы UDP (для IPv4 её можно
// не считать).
func udpPacket(src, dst netip.AddrPort, payload string) []byte {
	p := make([]byte, 28+len(payload))
	p[0] = 0x45
	binary.BigEndian.PutUint16(p[2:], uint16(len(p)))
	p[6] = 0x40 // DF
	p[8] = 64
	p[9] = unix.IPPROTO_UDP
	s, d := src.Addr().As4(), dst.Addr().As4()
	copy(p[12:16], s[:])
	copy(p[16:20], d[:])
	var sum uint32
	for i := 0; i < 20; i += 2 {
		sum += uint32(binary.BigEndian.Uint16(p[i:]))
	}
	for sum > 0xffff {
		sum = sum&0xffff + sum>>16
	}
	binary.BigEndian.PutUint16(p[10:], ^uint16(sum))
	binary.BigEndian.PutUint16(p[20:], src.Port())
	binary.BigEndian.PutUint16(p[22:], dst.Port())
	binary.BigEndian.PutUint16(p[24:], uint16(8+len(payload)))
	copy(p[28:], payload)
	return p
}

// Настоящий адаптер. Дескриптор уходит поддельному получателю до TUNCONF,
// как ядру сразу после запуска: это адаптер TUN без PI и без IFF_VNET_HDR.
// Start даёт адрес /32, MTU 1300, подъём и маршруты IPv4 /1 (IPv6 — нет);
// пакеты ходят в обе стороны между системой и принятым дескриптором.
// Второй csqtt-cli адаптер не откроет. Suspend снимает маршруты, Start с
// другим адресом переадресует тот же интерфейс, следующей попытке уходит
// новый дубликат. Close убирает интерфейс — значит, свои дубликаты Tun
// закрыл.
func TestTunAdapterNetns(t *testing.T) {
	skipOutsideNetns(t)
	const name = "csqtttest0"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	w := NewTun(TunOptions{Name: name, Logf: t.Logf})
	defer w.Close()
	if r := w.Requires(); r.Unsupported != nil {
		t.Fatalf("Requires: %v", r.Unsupported)
	}
	release, err := w.Reserve()
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	defer release()

	// Попытка 1: дескриптор — сразу после запуска ядра, до TUNCONF.
	socket := w.SocketName()
	recv := listenFD(t, socket, replyAck)
	if err := w.SendFD(ctx, socket, os.Getpid()); err != nil {
		t.Fatalf("SendFD: %v", err)
	}
	fd1 := recv.received(t)
	defer func() {
		if fd1 >= 0 {
			unix.Close(fd1)
		}
	}()
	ifname, flags := tunInfo(t, fd1)
	const mask = unix.IFF_TUN | unix.IFF_TAP | unix.IFF_NO_PI | unix.IFF_VNET_HDR | unix.IFF_MULTI_QUEUE | unix.IFF_PERSIST
	if ifname != name || flags&mask != unix.IFF_TUN|unix.IFF_NO_PI {
		t.Fatalf("принятый дескриптор: адаптер %q, флаги %#x; ждали %q с IFF_TUN|IFF_NO_PI и только ими", ifname, flags, name)
	}
	index, addrs, _, up := linkState(t, name)
	if len(addrs) != 0 || up {
		t.Errorf("до Start: адреса %v, поднят %v", addrs, up)
	}

	addr1 := netip.MustParseAddr("192.0.2.2")
	if err := w.Start(ctx, TunConf{Address: addr1, DNS: []netip.Addr{netip.MustParseAddr("198.51.100.53")}}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	_, addrs, mtu, up := linkState(t, name)
	if !slices.Equal(addrs, []netip.Prefix{netip.PrefixFrom(addr1, 32)}) || mtu != TunnelMTU || !up {
		t.Errorf("после Start: адреса %v, MTU %d, поднят %v", addrs, mtu, up)
	}
	if got := linkRoutes(t, name); !slices.Equal(got, tunRoutes4) {
		t.Errorf("маршруты после Start: %v, ждали %v", got, tunRoutes4)
	}

	// Система → адаптер: UDP к адресу за туннелем читается из дескриптора
	// получателя.
	far := netip.MustParseAddrPort("203.0.113.9:9999")
	out, err := net.DialUDP("udp4", nil, net.UDPAddrFromAddrPort(far))
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if _, err := out.Write([]byte("csqtt-out")); err != nil {
		t.Fatal(err)
	}
	if src := readUDP(t, fd1, far, "csqtt-out"); src != addr1 {
		t.Errorf("исходящий пакет с адреса %v, ждали адрес туннеля %v", src, addr1)
	}

	// Адаптер → система: пакет, записанный в дескриптор получателя, доходит
	// до сокета на адресе туннеля.
	in, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.AddrPortFrom(addr1, 0)))
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	local := in.LocalAddr().(*net.UDPAddr).AddrPort()
	if _, err := unix.Write(fd1, udpPacket(far, local, "csqtt-in")); err != nil {
		t.Fatalf("запись в адаптер: %v", err)
	}
	_ = in.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 64)
	n, from, err := in.ReadFromUDPAddrPort(buf)
	if err != nil || string(buf[:n]) != "csqtt-in" || from != far {
		t.Errorf("из адаптера: %q от %v, %v", buf[:n], from, err)
	}

	// Второй csqtt-cli на машине адаптер не откроет.
	if err := fromOtherProcess(t, "open:csqtttest1"); err == nil || !strings.Contains(err.Error(), "-datapath socks") {
		t.Errorf("второй адаптер: %v", err)
	}
	if linkExists("csqtttest1") {
		t.Error("второй адаптер создан")
	}

	// Между попытками: маршрутов нет, адаптер с адресом остаётся.
	if err := w.Suspend(); err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	if got := linkRoutes(t, name); len(got) != 0 {
		t.Errorf("маршруты после Suspend: %v", got)
	}
	if err := w.Suspend(); err != nil {
		t.Errorf("повторный Suspend: %v", err)
	}

	// Попытка 2: новый сокет, новый дубликат того же адаптера, другой адрес.
	socket2 := w.SocketName()
	recv2 := listenFD(t, socket2, replyAck)
	if err := w.SendFD(ctx, socket2, os.Getpid()); err != nil {
		t.Fatalf("SendFD второй попытки: %v", err)
	}
	fd2 := recv2.received(t)
	defer func() {
		if fd2 >= 0 {
			unix.Close(fd2)
		}
	}()
	if ifname, _ := tunInfo(t, fd2); ifname != name {
		t.Errorf("второй дескриптор — адаптер %q", ifname)
	}
	addr2 := netip.MustParseAddr("192.0.2.3")
	if err := w.Start(ctx, TunConf{Address: addr2}); err != nil {
		t.Fatalf("Start второй попытки: %v", err)
	}
	index2, addrs, _, _ := linkState(t, name)
	if index2 != index || !slices.Equal(addrs, []netip.Prefix{netip.PrefixFrom(addr2, 32)}) {
		t.Errorf("после переадресации: индекс %d (был %d), адреса %v", index2, index, addrs)
	}
	if got := linkRoutes(t, name); !slices.Equal(got, tunRoutes4) {
		t.Errorf("маршруты второй попытки: %v", got)
	}

	// Ядра вышли — их дескрипторы закрыты; Close убирает интерфейс.
	unix.Close(fd1)
	unix.Close(fd2)
	fd1, fd2 = -1, -1
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if linkExists(name) {
		t.Error("интерфейс остался после Close: чей-то дескриптор адаптера не закрыт")
	}
	release()
	if err := fromOtherProcess(t, "lock"); err != nil {
		t.Errorf("блокировка не отпущена после Close: %v", err)
	}
}

// Чужой интерфейс с именем адаптера не удаляется: открытие отказывает и
// называет его, блокировка отпускается.
func TestTunForeignLinkNetns(t *testing.T) {
	skipOutsideNetns(t)
	const name = "csqtttest2"
	dummy := &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: name}}
	if err := netlink.LinkAdd(dummy); err != nil {
		t.Fatal(err)
	}
	defer netlink.LinkDel(dummy)

	w := NewTun(TunOptions{Name: name})
	defer w.Close()
	err := w.Start(context.Background(), TunConf{Address: netip.MustParseAddr("192.0.2.2")})
	if err == nil || !strings.Contains(err.Error(), "тип dummy") || !strings.Contains(err.Error(), "ip link del "+name) {
		t.Errorf("Start при чужом интерфейсе: %v", err)
	}
	if !linkExists(name) {
		t.Error("чужой интерфейс удалён")
	}
	if err := fromOtherProcess(t, "lock"); err != nil {
		t.Errorf("блокировка не отпущена после отказа: %v", err)
	}
}
