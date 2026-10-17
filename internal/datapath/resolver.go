package datapath

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// Ответы резолвера, по которым SOCKS5 выбирает код отказа.
var (
	errNoDNS     = errors.New("сервер не выдал DNS")
	errNotFound  = errors.New("имя не найдено")
	errNoAddress = errors.New("у имени нет адреса IPv4")
)

const (
	// dnsTimeout — ожидание ответа одного сервера на одну попытку.
	dnsTimeout = 2 * time.Second
	// dnsRounds — сколько раз обойти серверы, прежде чем сдаться.
	dnsRounds = 2
	// cacheMax — сколько имён помнить; переполненный кэш очищается целиком.
	cacheMax = 1024
	// cacheTTL — дольше ответ не помнится, какой бы TTL ни назвал сервер.
	cacheTTL = time.Minute
)

// resolver ищет адреса IPv4 имён у DNS туннеля и через туннель: запрос не
// идёт мимо него и не зависит от DNS системы, её файла hosts и доменов
// поиска. Сначала UDP, обрезанный ответ — повтор по TCP.
type resolver struct {
	net *netstack

	mu      sync.Mutex
	dns     []netip.Addr
	timeout time.Duration
	cache   map[string]cached
}

type cached struct {
	addrs   []netip.Addr
	expires time.Time
}

func newResolver(n *netstack) *resolver {
	return &resolver{net: n, timeout: dnsTimeout, cache: map[string]cached{}}
}

// setServers меняет DNS и забывает прежние ответы: они получены от другого
// сервера и могли быть его собственными (сплит-DNS).
func (r *resolver) setServers(dns []netip.Addr) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dns = append([]netip.Addr(nil), dns...)
	clear(r.cache)
}

// lookup — адреса IPv4 имени name.
func (r *resolver) lookup(ctx context.Context, name string) ([]netip.Addr, error) {
	key := strings.ToLower(strings.TrimSuffix(name, "."))
	r.mu.Lock()
	servers, timeout := r.dns, r.timeout
	c, ok := r.cache[key]
	r.mu.Unlock()
	if ok && time.Now().Before(c.expires) {
		return c.addrs, nil
	}
	if len(servers) == 0 {
		return nil, errNoDNS
	}
	q, err := dnsmessage.NewName(key + ".")
	if key == "" || err != nil {
		return nil, fmt.Errorf("имя %q: %w", name, errNotFound)
	}

	var last error
	for range dnsRounds {
		for _, server := range servers {
			addrs, ttl, err := r.ask(ctx, server, q, timeout)
			switch {
			case err == nil:
				r.remember(key, addrs, ttl)
				return addrs, nil
			case errors.Is(err, errNotFound), errors.Is(err, errNoAddress):
				// Ответ окончательный: у другого сервера туннеля он тот же.
				return nil, err
			case ctx.Err() != nil:
				return nil, ctx.Err()
			}
			last = err
		}
	}
	return nil, last
}

func (r *resolver) remember(key string, addrs []netip.Addr, ttl time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.cache) >= cacheMax {
		clear(r.cache)
	}
	r.cache[key] = cached{addrs, time.Now().Add(min(ttl, cacheTTL))}
}

// ask — один запрос A к одному серверу.
func (r *resolver) ask(ctx context.Context, server netip.Addr, name dnsmessage.Name, timeout time.Duration) ([]netip.Addr, time.Duration, error) {
	id := uint16(rand.Uint32())
	query, err := buildQuery(id, name)
	if err != nil {
		return nil, 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	answer, err := r.askUDP(ctx, server, id, query)
	if err != nil {
		return nil, 0, fmt.Errorf("DNS %v: %w", server, err)
	}
	var truncated bool
	addrs, ttl, err := parseAnswer(answer, id, name, &truncated)
	if !truncated {
		return addrs, ttl, err
	}
	if answer, err = r.askTCP(ctx, server, query); err != nil {
		return nil, 0, fmt.Errorf("DNS %v по TCP: %w", server, err)
	}
	return parseAnswer(answer, id, name, nil)
}

func (r *resolver) askUDP(ctx context.Context, server netip.Addr, id uint16, query []byte) ([]byte, error) {
	c, err := r.net.dialUDP(netip.AddrPortFrom(server, 53))
	if err != nil {
		return nil, err
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { _ = c.SetDeadline(time.Now()) })
	defer stop()
	if _, err := c.Write(query); err != nil {
		return nil, err
	}
	b := make([]byte, 1232)
	for {
		n, err := c.Read(b)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, err
		}
		// Ответ на другой запрос — запоздалый или подложный — пропускается.
		if n >= 2 && binary.BigEndian.Uint16(b) == id {
			return b[:n], nil
		}
	}
}

func (r *resolver) askTCP(ctx context.Context, server netip.Addr, query []byte) ([]byte, error) {
	c, err := r.net.dialTCP(ctx, netip.AddrPortFrom(server, 53))
	if err != nil {
		return nil, err
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { _ = c.SetDeadline(time.Now()) })
	defer stop()
	framed := binary.BigEndian.AppendUint16(nil, uint16(len(query)))
	if _, err := c.Write(append(framed, query...)); err != nil {
		return nil, err
	}
	var size [2]byte
	if _, err := io.ReadFull(c, size[:]); err != nil {
		return nil, err
	}
	answer := make([]byte, binary.BigEndian.Uint16(size[:]))
	if _, err := io.ReadFull(c, answer); err != nil {
		return nil, err
	}
	return answer, nil
}

func buildQuery(id uint16, name dnsmessage.Name) ([]byte, error) {
	b := dnsmessage.NewBuilder(make([]byte, 0, 512),
		dnsmessage.Header{ID: id, RecursionDesired: true})
	if err := b.StartQuestions(); err != nil {
		return nil, err
	}
	if err := b.Question(dnsmessage.Question{Name: name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}); err != nil {
		return nil, err
	}
	return b.Finish()
}

// parseAnswer разбирает ответ на запрос A. truncated не nil — обрезанный
// ответ (флаг TC) не разбирается, а отмечается в нём.
func parseAnswer(b []byte, id uint16, name dnsmessage.Name, truncated *bool) ([]netip.Addr, time.Duration, error) {
	var p dnsmessage.Parser
	h, err := p.Start(b)
	if err != nil {
		return nil, 0, err
	}
	if h.ID != id || !h.Response {
		return nil, 0, errors.New("ответ не на тот запрос")
	}
	if h.Truncated && truncated != nil {
		*truncated = true
		return nil, 0, nil
	}
	q, err := p.Question()
	if err != nil || !strings.EqualFold(q.Name.String(), name.String()) || q.Type != dnsmessage.TypeA {
		return nil, 0, errors.New("ответ не на тот запрос")
	}
	switch h.RCode {
	case dnsmessage.RCodeSuccess:
	case dnsmessage.RCodeNameError:
		return nil, 0, fmt.Errorf("%s: %w", strings.TrimSuffix(name.String(), "."), errNotFound)
	default:
		return nil, 0, fmt.Errorf("сервер ответил %v", h.RCode)
	}
	if err := p.SkipAllQuestions(); err != nil {
		return nil, 0, err
	}
	var addrs []netip.Addr
	ttl := cacheTTL
	for {
		rh, err := p.AnswerHeader()
		if errors.Is(err, dnsmessage.ErrSectionDone) {
			break
		}
		if err != nil {
			return nil, 0, err
		}
		if rh.Type != dnsmessage.TypeA || rh.Class != dnsmessage.ClassINET {
			if err := p.SkipAnswer(); err != nil {
				return nil, 0, err
			}
			continue
		}
		a, err := p.AResource()
		if err != nil {
			return nil, 0, err
		}
		addrs = append(addrs, netip.AddrFrom4(a.A))
		ttl = min(ttl, time.Duration(rh.TTL)*time.Second)
	}
	if len(addrs) == 0 {
		return nil, 0, fmt.Errorf("%s: %w", strings.TrimSuffix(name.String(), "."), errNoAddress)
	}
	return addrs, ttl, nil
}

// errTimeout — ошибка истёкшего ожидания, сетевого или по ctx.
func errTimeout(err error) bool {
	var ne net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.As(err, &ne) && ne.Timeout()
}
