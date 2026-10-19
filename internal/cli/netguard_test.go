package cli

import (
	"context"
	"net"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// Прокси окна капчи при kill-switch разрешает имена своим резолвером — к
// заданному DNS, а не к DNS системы, — и все его сокеты, и DNS, и TCP,
// проходят через хук netguard.
func TestGuardedDialer(t *testing.T) {
	site, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer site.Close()
	go func() {
		for {
			c, err := site.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	dns := fakeDNS(t, "id.vk.example.", [4]byte{127, 0, 0, 1})

	var mu sync.Mutex
	var hooked []string
	control := func(network, address string, _ syscall.RawConn) error {
		mu.Lock()
		defer mu.Unlock()
		hooked = append(hooked, network+" "+address)
		return nil
	}
	dial := guardedDialer(control, []string{dns})
	_, port, _ := net.SplitHostPort(site.Addr().String())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := dial(ctx, "tcp4", net.JoinHostPort("id.vk.example", port))
	if err != nil {
		t.Fatalf("соединение по имени: %v", err)
	}
	c.Close()

	mu.Lock()
	defer mu.Unlock()
	if !slices.ContainsFunc(hooked, func(s string) bool { return strings.HasSuffix(s, " "+dns) }) {
		t.Errorf("запрос DNS не через хук или не к заданному DNS: %v", hooked)
	}
	if !slices.Contains(hooked, "tcp4 "+site.Addr().String()) {
		t.Errorf("соединение с сайтом не через хук: %v", hooked)
	}
}

// fakeDNS — DNS на loopback по UDP: на A-запрос name отвечает addr, на
// прочее — пустым ответом.
func fakeDNS(t *testing.T, name string, addr [4]byte) string {
	t.Helper()
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 512)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			var m dnsmessage.Message
			if m.Unpack(buf[:n]) != nil || len(m.Questions) != 1 {
				continue
			}
			q := m.Questions[0]
			m.Header.Response, m.Header.RecursionAvailable = true, true
			if q.Type == dnsmessage.TypeA && strings.EqualFold(q.Name.String(), name) {
				m.Answers = []dnsmessage.Resource{{
					Header: dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 60},
					Body:   &dnsmessage.AResource{A: addr},
				}}
			}
			out, err := m.Pack()
			if err == nil {
				_, _ = pc.WriteTo(out, from)
			}
		}
	}()
	return pc.LocalAddr().String()
}
