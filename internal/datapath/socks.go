package datapath

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"time"
)

// DefaultSocksListen — адрес SOCKS5 по умолчанию.
const DefaultSocksListen = "127.0.0.1:1080"

// SocksOptions — настройки datapath socks.
type SocksOptions struct {
	// Listen — адрес SOCKS5, только loopback: пароля у SOCKS5 нет. Пусто —
	// DefaultSocksListen.
	Listen string
	// Logf — куда писать журнал; nil — молчать.
	Logf func(format string, args ...any)
}

// Socks — datapath без прав: netstack gVisor в памяти процесса — конец
// туннеля, а над ним локальный SOCKS5 (architecture.md §05). Охват — только
// TCP программ, которые умеют ходить через прокси.
//
// Socks живёт весь сеанс, ядро — одну попытку. Link и SOCKS5 открываются в
// NewSocks: адрес Link нужен ядру при запуске, а занятый порт SOCKS5 должен
// выясниться до входа в VK, а не после. Start зовётся на первый TUNCONF
// каждой попытки: тот же адрес — соединения переживают переподключение,
// другой — рвутся, потому что ответы на прежний адрес не придут.
//
// До первого Start запросы получают отказ «сеть недоступна» сразу, между
// попытками — ждут, пока TCP повторяет пакеты.
type Socks struct {
	opts SocksOptions
	link *Link
	net  *netstack
	res  *resolver
	ln   net.Listener

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu sync.Mutex
	// addr — адрес туннеля; не задан — Start ещё не было.
	addr netip.Addr
	// gen — поколение адреса: отменяется при смене адреса и рвёт
	// соединения, открытые на прежнем.
	gen       context.Context
	cancelGen context.CancelFunc
	closed    bool

	closeOnce sync.Once
	closeErr  error
}

// NewSocks открывает Link, netstack и SOCKS5. Пакеты по Link ходят, как
// только ядро сообщит порт (Link().SetCorePort).
func NewSocks(opts SocksOptions) (*Socks, error) {
	if opts.Listen == "" {
		opts.Listen = DefaultSocksListen
	}
	listen, err := netip.ParseAddrPort(opts.Listen)
	if err != nil {
		return nil, fmt.Errorf("socks: адрес %q — не IP:порт", opts.Listen)
	}
	if !listen.Addr().IsLoopback() {
		return nil, fmt.Errorf("socks: адрес %v — не loopback: SOCKS5 без пароля открыл бы туннель всей сети", listen)
	}

	link, err := ListenLink()
	if err != nil {
		return nil, err
	}
	ns, err := newNetstack(TunnelMTU)
	if err != nil {
		_ = link.Close()
		return nil, err
	}
	lc := net.ListenConfig{Control: exclusiveAddr}
	ln, err := lc.Listen(context.Background(), "tcp", listen.String())
	if err != nil {
		_ = link.Close()
		ns.close()
		return nil, fmt.Errorf("socks: %w", err)
	}

	s := &Socks{opts: opts, link: link, net: ns, res: newResolver(ns), ln: ln}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.gen, s.cancelGen = context.WithCancel(s.ctx)
	s.wg.Add(2)
	go func() {
		defer s.wg.Done()
		ns.pump(s.ctx, link)
	}()
	go func() {
		defer s.wg.Done()
		s.accept()
	}()
	s.logf("[SOCKS] слушает %v", ln.Addr())
	return s, nil
}

// Link — канал с ядром: Addr — значение -listen-peer, порт ядра из события
// LISTEN — в SetCorePort.
func (s *Socks) Link() *Link { return s.link }

// ListenAddr — адрес SOCKS5; отличается от заказанного, если порт был 0.
func (s *Socks) ListenAddr() net.Addr { return s.ln.Addr() }

// Start — первый TUNCONF попытки: адрес туннеля и DNS.
func (s *Socks) Start(_ context.Context, conf TunConf) error {
	a := conf.Address.Unmap()
	if !a.Is4() || a.IsUnspecified() {
		return fmt.Errorf("socks: адрес туннеля %v — не IPv4", conf.Address)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("socks: datapath закрыт")
	}
	if a != s.addr {
		if err := s.net.addAddress(a); err != nil {
			return err
		}
		if s.addr.IsValid() {
			s.net.removeAddress(s.addr)
			s.cancelGen()
			s.gen, s.cancelGen = context.WithCancel(s.ctx)
		}
		s.addr = a
	}
	dns := s.setDNS(conf.DNS)
	s.logf("[SOCKS] адрес туннеля %v, DNS %v", a, dns)
	return nil
}

// UpdateDNS — повторный TUNCONF с другим DNS: соединения не рвутся.
func (s *Socks) UpdateDNS(dns []netip.Addr) error {
	s.logf("[SOCKS] DNS %v", s.setDNS(dns))
	return nil
}

// setDNS оставляет годные адреса DNS и отдаёт их резолверу.
func (s *Socks) setDNS(dns []netip.Addr) []netip.Addr {
	var good []netip.Addr
	for _, d := range dns {
		if d = d.Unmap(); d.Is4() && !d.IsUnspecified() {
			good = append(good, d)
		}
	}
	if len(good) == 0 {
		s.logf("[SOCKS] сервер не выдал DNS IPv4: запросы по имени получат отказ")
	}
	s.res.setServers(good)
	return good
}

// Close закрывает SOCKS5, соединения через него, netstack и Link и ждёт
// конца своих горутин. Повторный вызов безвреден.
func (s *Socks) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		err := s.ln.Close()
		s.cancel()
		if lerr := s.link.Close(); err == nil {
			err = lerr
		}
		s.wg.Wait()
		s.net.close()
		s.closeErr = err
	})
	return s.closeErr
}

func (s *Socks) accept() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			if s.ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			// Временная нехватка (дескрипторов, памяти): не крутиться впустую.
			s.logf("[SOCKS] accept: %v", err)
			select {
			case <-s.ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.serve(c)
		}()
	}
}

// serve — одно соединение SOCKS5 от приветствия до конца переноса.
func (s *Socks) serve(c net.Conn) {
	defer c.Close()
	s.mu.Lock()
	ctx, ready := s.gen, s.addr.IsValid()
	s.mu.Unlock()
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()

	_ = c.SetDeadline(time.Now().Add(handshakeTimeout))
	if err := negotiate(c); err != nil {
		return
	}
	req, err := readRequest(c)
	var se *socksError
	if err != nil && !errors.As(err, &se) {
		return // клиент ушёл или говорит не на SOCKS5
	}
	if err == nil && !ready {
		err = &socksError{repNetworkUnreachable, errors.New("туннель ещё не поднят")}
	}
	var target net.Conn
	if err == nil {
		target, err = s.connect(ctx, req)
	}
	if err != nil {
		_ = writeReply(c, replyCode(err), netip.AddrPort{})
		s.logf("[SOCKS] %v: %v", req, err)
		return
	}
	defer target.Close()
	stopTarget := context.AfterFunc(ctx, func() { _ = target.Close() })
	defer stopTarget()

	bound, _ := netip.ParseAddrPort(target.LocalAddr().String())
	if err := writeReply(c, repSucceeded, bound); err != nil {
		return
	}
	_ = c.SetDeadline(time.Time{})
	relay(c, target)
}

// connect находит адрес цели и подключается к ней через netstack; у имени
// с несколькими адресами пробуются все по очереди.
func (s *Socks) connect(ctx context.Context, req socksRequest) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	addrs := []netip.Addr{req.addr}
	if req.host != "" {
		var err error
		if addrs, err = s.res.lookup(ctx, req.host); err != nil {
			return nil, err
		}
	}
	var last error
	for _, a := range addrs {
		c, err := s.net.dialTCP(ctx, netip.AddrPortFrom(a, req.port))
		if err == nil {
			return c, nil
		}
		last = err
		if ctx.Err() != nil {
			break
		}
	}
	return nil, last
}

func (s *Socks) logf(format string, args ...any) {
	if s.opts.Logf != nil {
		s.opts.Logf(format, args...)
	}
}
