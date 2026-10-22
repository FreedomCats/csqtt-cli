package captcha

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestParseNetFilter(t *testing.T) {
	for _, tc := range []struct {
		value, spec string
		domains     []string // nil — не проверять (набор VK)
		all         bool
	}{
		{value: "all", spec: "all", all: true},
		{value: " ALL ", spec: "all", all: true},
		{value: "vk.com", spec: "vk.com", domains: []string{"vk.com"}},
		{value: "id.vk.com, *.mail.ru, .ok.ru., MAIL.ru", spec: "id.vk.com,mail.ru,ok.ru",
			domains: []string{"id.vk.com", "mail.ru", "ok.ru"}},
		{value: "ru", spec: "ru", domains: []string{"ru"}},
		{value: "vk.com,", spec: "vk.com", domains: []string{"vk.com"}},
		{value: "vk", spec: "vk"},
		{value: "vk,mail.ru", spec: "vk,mail.ru"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			f, err := ParseNetFilter(tc.value)
			if err != nil {
				t.Fatalf("ошибка: %v", err)
			}
			if f.String() != tc.spec || f.all != tc.all {
				t.Errorf("разобрано %q (all %v), ожидалось %q (all %v)", f, f.all, tc.spec, tc.all)
			}
			if tc.domains != nil && !slices.Equal(f.domains, tc.domains) {
				t.Errorf("домены %q, ожидались %q", f.domains, tc.domains)
			}
		})
	}

	// vk — набор VK целиком, и вместе с другими доменами тоже.
	f, err := ParseNetFilter("vk,mail.ru")
	if err != nil {
		t.Fatal(err)
	}
	for _, domain := range append(slices.Clone(vkLoginDomains), "mail.ru") {
		if !slices.Contains(f.domains, domain) {
			t.Errorf("в vk,mail.ru нет %q: %q", domain, f.domains)
		}
	}
	for _, domain := range append(slices.Clone(proxyDomains), "vk.com", "vk.ru") {
		if !slices.Contains(vkLoginDomains, domain) {
			t.Errorf("в наборе VK нет %q", domain)
		}
	}
}

func TestParseNetFilterErrors(t *testing.T) {
	for _, value := range []string{
		"", " , ", "all,vk", "vk.com,all", "https://vk.com", "vk.com:443", "vk.com/path",
		"203.0.113.1", "2001:db8::1", "вк.рф", "vk..com", "*", "vk.com?x=1",
	} {
		if f, err := ParseNetFilter(value); err == nil {
			t.Errorf("%q разобрано как %q, ожидалась ошибка", value, f)
		}
	}
}

func TestNetFilterAllows(t *testing.T) {
	for _, tc := range []struct {
		filter string
		hosts  map[string]bool
	}{
		{"vk.com", map[string]bool{
			"vk.com": true, "id.vk.com": true, "ID.VK.COM.": true, "vk.ru": false,
			"evilvk.com": false, "vk.com.evil.org": false, "203.0.113.1": false, "": false}},
		{"all", map[string]bool{
			"vk.com": true, "ad.mail.ru": true, "203.0.113.1": true, "2001:db8::1": true}},
		{"vk", map[string]bool{
			"id.vk.ru": true, "st.vk.com": true, "sun9-1.userapi.com": true, "vkontakte.ru": true,
			"ad.mail.ru": false, "github.com": false}},
		{"ru", map[string]bool{"id.vk.ru": true, "ad.mail.ru": true, "vk.com": false}},
	} {
		f, err := ParseNetFilter(tc.filter)
		if err != nil {
			t.Fatal(err)
		}
		for host, want := range tc.hosts {
			if got := f.allows(host); got != want {
				t.Errorf("-net-allow %s: allows(%q) = %v, ожидалось %v", tc.filter, host, got, want)
			}
		}
	}
}

// syncBuffer — вывод, в который пишут горутины прокси.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// startTestLoginNet — прокси окна входа с фильтром filter; диалер ведёт
// всё на echo.
func startTestLoginNet(t *testing.T, filter string, d *recordingDial) (*loginNet, *syncBuffer, *logBuffer) {
	t.Helper()
	f, err := ParseNetFilter(filter)
	if err != nil {
		t.Fatal(err)
	}
	out := &syncBuffer{}
	log := &logBuffer{t: t}
	n, err := startLoginNet(f, d.dial, out, log.logf)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { n.proxy.Close() })
	return n, out, log
}

// testSecret — выдуманный токен в адресах: в журнал и сводку он попасть
// не должен.
const testSecret = "access_token=stand-secret-0123456789"

func TestLoginNetJournal(t *testing.T) {
	d := &recordingDial{echo: echoServer(t)}
	n, out, _ := startTestLoginNet(t, "vk", d)
	p := n.proxy

	// Пропущенный CONNECT — дважды: строка одна, в сводке ×2.
	for range 2 {
		c, _, code := ask(t, p, "CONNECT id.vk.ru:443 HTTP/1.1\r\nHost: id.vk.ru:443\r\n\r\n")
		if code != http.StatusOK {
			t.Fatalf("код %d, ожидался 200", code)
		}
		c.Close()
	}
	// Отказ — любой порт: фильтр смотрит только на домен.
	for _, req := range []string{
		"CONNECT ad.mail.ru:443 HTTP/1.1\r\n\r\n",
		"CONNECT top-fwz1.mail.ru:8443 HTTP/1.1\r\n\r\n",
		"CONNECT 203.0.113.1:443 HTTP/1.1\r\n\r\n",
		"GET http://ad.mail.ru/pixel?" + testSecret + " HTTP/1.1\r\nHost: ad.mail.ru\r\n\r\n",
	} {
		if _, _, code := ask(t, p, req); code != http.StatusForbidden {
			t.Errorf("%q: код %d, ожидался 403", req, code)
		}
	}
	// Порт не 443 у домена VK — пропущен.
	if _, _, code := ask(t, p, "CONNECT st.vk.com:8080 HTTP/1.1\r\n\r\n"); code != http.StatusOK {
		t.Errorf("st.vk.com:8080: код %d, ожидался 200", code)
	}
	// Адрес не разобрать — отказ, без самого адреса в журнале.
	if _, _, code := ask(t, p, "GET /"+testSecret+" HTTP/1.1\r\nHost: id.vk.ru\r\n\r\n"); code != http.StatusBadRequest {
		t.Errorf("запрос без адреса: код %d, ожидался 400", code)
	}

	n.close()
	text := out.String()
	for _, want := range []string{
		"-net-allow vk.\n",
		"[VK] окно входа, сеть: id.vk.ru:443 — пропущен\n",
		"[VK] окно входа, сеть: ad.mail.ru:443 — отказ\n",
		"[VK] окно входа, сеть: top-fwz1.mail.ru:8443 — отказ\n",
		"[VK] окно входа, сеть: 203.0.113.1:443 — отказ\n",
		"[VK] окно входа, сеть: ad.mail.ru:80 (HTTP) — отказ\n",
		"[VK] окно входа, сеть: st.vk.com:8080 — пропущен\n",
		"[VK] окно входа, сеть: " + unreadable + " (HTTP) — отказ\n",
		"Сводка сети окна входа (-net-allow vk): пропущено хостов — 2, отказ — 5.\n" +
			"  пропущен  id.vk.ru:443  ×2\n" +
			"  пропущен  st.vk.com:8080  ×1\n" +
			"  отказ     203.0.113.1:443  ×1\n" +
			"  отказ     <адрес не разобрать> (HTTP)  ×1\n" +
			"  отказ     ad.mail.ru:443  ×1\n" +
			"  отказ     ad.mail.ru:80 (HTTP)  ×1\n" +
			"  отказ     top-fwz1.mail.ru:8443  ×1\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("в выводе нет %q:\n%s", want, text)
		}
	}
	if got := strings.Count(text, "id.vk.ru:443 — пропущен"); got != 1 {
		t.Errorf("строк о id.vk.ru:443 — %d, ожидалась одна:\n%s", got, text)
	}
	if strings.Contains(text, "stand-secret") || strings.Contains(text, "pixel") {
		t.Errorf("в выводе путь или токен:\n%s", text)
	}
	if calls := d.calls(); len(calls) != 3 {
		t.Errorf("диалер позван %d раз, ожидалось 3 (только пропущенные): %q", len(calls), calls)
	}
}

// Обычный HTTP: запрос уходит сайту в обычном виде, ответ — клиенту; в
// журнале — только хост и порт, без пути, запроса и имени с
// паролем из адреса.
func TestLoginNetPlainHTTP(t *testing.T) {
	var mu sync.Mutex
	var got []string
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, r.Method+" "+r.RequestURI+" "+r.Host+" "+r.Header.Get("Proxy-Connection"))
		mu.Unlock()
		io.WriteString(w, "ответ сайта")
	}))
	t.Cleanup(site.Close)

	d := &recordingDial{echo: site.Listener.Addr().String()}
	n, out, _ := startTestLoginNet(t, "all", d)

	req := "GET http://user:pass@st.vk.com/js/app.js?" + testSecret + " HTTP/1.1\r\n" +
		"Host: st.vk.com\r\nProxy-Connection: keep-alive\r\n\r\n"
	_, br, code := ask(t, n.proxy, req)
	if code != http.StatusOK {
		t.Fatalf("код %d, ожидался 200", code)
	}
	body, _ := io.ReadAll(br)
	if !strings.Contains(string(body), "ответ сайта") {
		t.Errorf("ответ: %q", body)
	}
	mu.Lock()
	if len(got) != 1 || got[0] != "GET /js/app.js?"+testSecret+" st.vk.com " {
		t.Errorf("сайт получил %q", got)
	}
	mu.Unlock()
	if calls := d.calls(); len(calls) != 1 || calls[0] != "tcp st.vk.com:80" {
		t.Errorf("диалер позван так: %q", calls)
	}

	n.close()
	text := out.String()
	if !strings.Contains(text, "st.vk.com:80 (HTTP) — пропущен") || !strings.Contains(text, "-net-allow all") {
		t.Errorf("вывод:\n%s", text)
	}
	for _, secret := range []string{"stand-secret", "app.js", "user", "pass"} {
		if strings.Contains(text, secret) {
			t.Errorf("в выводе %q:\n%s", secret, text)
		}
	}
}

// Пропущен, но не соединился: строка без текста ошибки (в нём бывает адрес
// резолвера), текст — только в журнал -v; в сводке — счёт неудач.
func TestLoginNetDialFails(t *testing.T) {
	d := &recordingDial{err: errors.New("lookup id.vk.ru on 192.0.2.53:53: no such host")}
	n, out, log := startTestLoginNet(t, "vk.ru", d)
	for range 2 {
		if _, _, code := ask(t, n.proxy, "CONNECT id.vk.ru:443 HTTP/1.1\r\n\r\n"); code != http.StatusBadGateway {
			t.Errorf("код %d, ожидался 502", code)
		}
	}
	n.close()
	text := out.String()
	for _, want := range []string{
		"id.vk.ru:443 — пропущен\n",
		"id.vk.ru:443 — пропущен, но не соединился\n",
		"  пропущен  id.vk.ru:443  ×2, не соединился ×2\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("в выводе нет %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "192.0.2.53") {
		t.Errorf("текст ошибки в выводе без -v:\n%s", text)
	}
	if !strings.Contains(log.String(), "no such host") {
		t.Errorf("текста ошибки нет в журнале -v:\n%s", log)
	}
}

func TestLoginNetNoConnections(t *testing.T) {
	n, out, _ := startTestLoginNet(t, "vk", &recordingDial{})
	n.close()
	if want := "пропущено хостов — 0, отказ — 0.\n  соединений не было\n"; !strings.Contains(out.String(), want) {
		t.Errorf("в выводе нет %q:\n%s", want, out)
	}
}

// Без фильтра режима нет: ни прокси, ни аргументов, ни сводки.
func TestLoginNetOff(t *testing.T) {
	n, err := startLoginNet(nil, nil, io.Discard, nil)
	if n != nil || err != nil {
		t.Fatalf("без фильтра: %v, %v", n, err)
	}
	if args := n.browserArgs(); args != nil {
		t.Errorf("аргументы браузера без фильтра: %q", args)
	}
	var w bytes.Buffer
	if n.writer(&w) != &w {
		t.Error("без фильтра вывод подменён")
	}
	n.close()
}

func TestJournalKey(t *testing.T) {
	for _, tc := range []struct {
		target string
		plain  bool
		key    string
	}{
		{"id.vk.ru:443", false, "id.vk.ru:443"},
		{"[2001:db8::1]:443", false, "[2001:db8::1]:443"},
		{"id.vk.ru:80", true, "id.vk.ru:80 (HTTP)"},
		{"", true, unreadable + " (HTTP)"},
		{"id.vk.ru", false, unreadable},
		{"id.vk.ru:https", false, unreadable},
		{"id.vk.ru/" + testSecret + ":443", false, unreadable},
		{fmt.Sprintf("%s:443", strings.Repeat("a", 300)), false, unreadable},
	} {
		if key, _, _ := journalKey(tc.target, tc.plain); key != tc.key {
			t.Errorf("journalKey(%q, %v) = %q, ожидалось %q", tc.target, tc.plain, key, tc.key)
		}
	}
}
