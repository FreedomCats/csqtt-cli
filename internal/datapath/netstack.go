package datapath

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	ip4 "gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"
)

const nicID tcpip.NICID = 1

// outQueue — очередь исходящих пакетов netstack; переполненная сбрасывает
// пакет, как переполненный адаптер, и TCP его повторит.
const outQueue = 1024

// netstack — сетевой стек gVisor в памяти процесса, конец туннеля: что он
// отправляет, уходит пакетом в канал, а пакеты из канала он принимает как
// пришедшие по сети. Только IPv4, TCP и UDP.
type netstack struct {
	stack *stack.Stack
	ep    *channel.Endpoint
}

func newNetstack(mtu uint32) (*netstack, error) {
	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ip4.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
	})
	sack := tcpip.TCPSACKEnabled(true)
	moderate := tcpip.TCPModerateReceiveBufferOption(true)
	for _, opt := range []tcpip.SettableTransportProtocolOption{&sack, &moderate} {
		if err := s.SetTransportProtocolOption(tcp.ProtocolNumber, opt); err != nil {
			s.Close()
			return nil, fmt.Errorf("netstack: %v", err)
		}
	}
	ep := channel.New(outQueue, mtu, "")
	if err := s.CreateNIC(nicID, ep); err != nil {
		s.Close()
		return nil, fmt.Errorf("netstack: %v", err)
	}
	s.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: nicID}})
	return &netstack{stack: s, ep: ep}, nil
}

// addAddress и removeAddress — адреса стека, /32: туннель точка-точка, и
// весь IPv4 идёт через него по одному маршруту.
func (n *netstack) addAddress(a netip.Addr) error {
	err := n.stack.AddProtocolAddress(nicID, tcpip.ProtocolAddress{
		Protocol:          ip4.ProtocolNumber,
		AddressWithPrefix: tcpip.AddrFrom4(a.As4()).WithPrefix(),
	}, stack.AddressProperties{})
	if err != nil {
		return fmt.Errorf("netstack: адрес %v: %v", a, err)
	}
	return nil
}

func (n *netstack) removeAddress(a netip.Addr) {
	_ = n.stack.RemoveAddress(nicID, tcpip.AddrFrom4(a.As4()))
}

// inject отдаёт стеку пакет, пришедший из туннеля; b можно переиспользовать
// сразу: стек копирует.
func (n *netstack) inject(b []byte) {
	pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(b)})
	n.ep.InjectInbound(ip4.ProtocolNumber, pkt)
	pkt.DecRef()
}

// next ждёт исходящий пакет и отдаёт его write; false — стек закрыт или ctx
// отменён.
func (n *netstack) next(ctx context.Context, write func([]byte)) bool {
	pkt := n.ep.ReadContext(ctx)
	if pkt == nil {
		return false
	}
	v := pkt.ToView()
	pkt.DecRef()
	write(v.AsSlice())
	v.Release()
	return true
}

// pump гоняет пакеты между стеком и Link до закрытия одной из сторон.
func (n *netstack) pump(ctx context.Context, l *Link) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		b := make([]byte, MaxPacket)
		for {
			k, err := l.ReadPacket(b)
			if err != nil {
				return
			}
			n.inject(b[:k])
		}
	}()
	for n.next(ctx, func(p []byte) { _ = l.WritePacket(p) }) {
	}
	<-done
}

// dialTCP — TCP-соединение через стек. В отличие от gonet.DialContextTCP
// отдаёт ошибку стека как есть: по ней выбирается код ответа SOCKS5.
func (n *netstack) dialTCP(ctx context.Context, to netip.AddrPort) (*gonet.TCPConn, error) {
	var wq waiter.Queue
	ep, terr := n.stack.NewEndpoint(tcp.ProtocolNumber, ip4.ProtocolNumber, &wq)
	if terr != nil {
		return nil, &dialError{terr}
	}
	entry, notify := waiter.NewChannelEntry(waiter.WritableEvents)
	wq.EventRegister(&entry)
	defer wq.EventUnregister(&entry)

	terr = ep.Connect(tcpip.FullAddress{NIC: nicID, Addr: tcpip.AddrFrom4(to.Addr().As4()), Port: to.Port()})
	if _, started := terr.(*tcpip.ErrConnectStarted); started {
		select {
		case <-ctx.Done():
			ep.Close()
			return nil, ctx.Err()
		case <-notify:
		}
		terr = ep.LastError()
	}
	if terr != nil {
		ep.Close()
		return nil, &dialError{terr}
	}
	return gonet.NewTCPConn(&wq, ep), nil
}

// dialUDP — UDP-сокет стека, связанный с to.
func (n *netstack) dialUDP(to netip.AddrPort) (*gonet.UDPConn, error) {
	return gonet.DialUDP(n.stack, nil,
		&tcpip.FullAddress{NIC: nicID, Addr: tcpip.AddrFrom4(to.Addr().As4()), Port: to.Port()},
		ip4.ProtocolNumber)
}

func (n *netstack) close() {
	n.ep.Close()
	n.stack.Close()
	n.stack.Wait()
}

type dialError struct{ err tcpip.Error }

func (e *dialError) Error() string { return e.err.String() }

// tcpipError — ошибка стека внутри err, если она там есть.
func tcpipError(err error) tcpip.Error {
	var de *dialError
	if errors.As(err, &de) {
		return de.err
	}
	return nil
}
