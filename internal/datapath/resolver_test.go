package datapath

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// message — ответ DNS на запрос A имени name.
func message(t *testing.T, h dnsmessage.Header, name string, build func(b *dnsmessage.Builder, n dnsmessage.Name)) []byte {
	t.Helper()
	n := dnsmessage.MustNewName(name)
	h.Response = true
	b := dnsmessage.NewBuilder(nil, h)
	_ = b.StartQuestions()
	_ = b.Question(dnsmessage.Question{Name: n, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET})
	_ = b.StartAnswers()
	if build != nil {
		build(&b, n)
	}
	msg, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

func TestParseAnswer(t *testing.T) {
	name := dnsmessage.MustNewName("www.example.")
	alias := dnsmessage.MustNewName("cdn.example.")
	a1, a2 := netip.MustParseAddr("198.51.100.7"), netip.MustParseAddr("198.51.100.8")

	// CNAME и за ним два A: адреса — оба, срок — наименьший TTL из A.
	msg := message(t, dnsmessage.Header{ID: 7}, "www.example.", func(b *dnsmessage.Builder, n dnsmessage.Name) {
		_ = b.CNAMEResource(dnsmessage.ResourceHeader{Name: n, Class: dnsmessage.ClassINET, TTL: 5},
			dnsmessage.CNAMEResource{CNAME: alias})
		_ = b.AResource(dnsmessage.ResourceHeader{Name: alias, Class: dnsmessage.ClassINET, TTL: 30},
			dnsmessage.AResource{A: a1.As4()})
		_ = b.AResource(dnsmessage.ResourceHeader{Name: alias, Class: dnsmessage.ClassINET, TTL: 20},
			dnsmessage.AResource{A: a2.As4()})
	})
	addrs, ttl, err := parseAnswer(msg, 7, name, nil)
	if err != nil || !slices.Equal(addrs, []netip.Addr{a1, a2}) || ttl != 20*time.Second {
		t.Errorf("CNAME + A: %v, %v, %v", addrs, ttl, err)
	}

	// Сверх cacheTTL ответ не помнится.
	msg = message(t, dnsmessage.Header{ID: 7}, "www.example.", func(b *dnsmessage.Builder, n dnsmessage.Name) {
		_ = b.AResource(dnsmessage.ResourceHeader{Name: n, Class: dnsmessage.ClassINET, TTL: 86400},
			dnsmessage.AResource{A: a1.As4()})
	})
	if _, ttl, _ := parseAnswer(msg, 7, name, nil); ttl != cacheTTL {
		t.Errorf("TTL сутки: срок %v", ttl)
	}

	if _, _, err := parseAnswer(msg, 8, name, nil); err == nil {
		t.Error("ответ с чужим ID принят")
	}
	if _, _, err := parseAnswer(msg, 7, dnsmessage.MustNewName("other.example."), nil); err == nil {
		t.Error("ответ на другое имя принят")
	}

	nx := message(t, dnsmessage.Header{ID: 7, RCode: dnsmessage.RCodeNameError}, "www.example.", nil)
	if _, _, err := parseAnswer(nx, 7, name, nil); !errors.Is(err, errNotFound) {
		t.Errorf("NXDOMAIN: %v", err)
	}
	empty := message(t, dnsmessage.Header{ID: 7}, "www.example.", nil)
	if _, _, err := parseAnswer(empty, 7, name, nil); !errors.Is(err, errNoAddress) {
		t.Errorf("без записей: %v", err)
	}
	fail := message(t, dnsmessage.Header{ID: 7, RCode: dnsmessage.RCodeServerFailure}, "www.example.", nil)
	if _, _, err := parseAnswer(fail, 7, name, nil); err == nil || errors.Is(err, errNotFound) {
		t.Errorf("SERVFAIL: %v", err)
	}

	tc := message(t, dnsmessage.Header{ID: 7, Truncated: true}, "www.example.", nil)
	var truncated bool
	if _, _, err := parseAnswer(tc, 7, name, &truncated); err != nil || !truncated {
		t.Errorf("TC: %v, truncated %v", err, truncated)
	}
}

func lookup(t *testing.T, s *Socks, name string) ([]netip.Addr, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.res.lookup(ctx, name)
}

// SERVFAIL — к следующему серверу; NXDOMAIN — окончательно.
func TestResolverNextServer(t *testing.T) {
	s, in := started(t, dnsAddr, dns2Addr)
	in.setZone(dns2Addr, zone{"echo.example": {hostAddr}})
	in.setFailing(dnsAddr)
	if addrs, err := lookup(t, s, "echo.example"); err != nil || !slices.Equal(addrs, []netip.Addr{hostAddr}) {
		t.Errorf("SERVFAIL у первого: %v, %v", addrs, err)
	}

	in.setFailing(netip.Addr{})
	in.setZone(dns2Addr, zone{"nx.example": {hostAddr}})
	if _, err := lookup(t, s, "nx.example"); !errors.Is(err, errNotFound) {
		t.Errorf("NXDOMAIN у первого: %v", err)
	}
}

// Ответ помнится: сервер забыл имя, а оно ещё находится; регистр и точка в
// конце — то же имя.
func TestResolverCache(t *testing.T) {
	s, in := started(t)
	if _, err := lookup(t, s, "echo.example"); err != nil {
		t.Fatal(err)
	}
	in.setZone(dnsAddr, zone{})
	for _, name := range []string{"echo.example", "ECHO.example."} {
		if addrs, err := lookup(t, s, name); err != nil || !slices.Equal(addrs, []netip.Addr{hostAddr}) {
			t.Errorf("%s из кэша: %v, %v", name, addrs, err)
		}
	}
}
