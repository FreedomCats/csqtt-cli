// netfilter.go — диагностический режим сети окна входа VK (vk login
// -net-allow, §08а doc/architecture.md).
//
// Проект нужен и там, где сеть пускает только по белым спискам доменов, а
// на какие домены ходит страница входа VK, заранее не известно. В этом
// режиме окно входа ходит в сеть только через локальный прокси (proxy.go) с
// фильтром доменов, а каждое соединение пишется строкой: хост, порт и
// «пропущен» или «отказ». В конце входа — сводка. Путей, запросов и
// фрагментов адресов в журнале нет: в них токены.
package captcha

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
)

const (
	// NetAllowAll — значение -net-allow: пускать всё, только записывать.
	NetAllowAll = "all"
	// NetAllowVK — значение -net-allow: набор доменов VK (vkLoginDomains).
	NetAllowVK = "vk"
)

// vkLoginDomains — набор «только VK»: домены окна капчи (proxyDomains) и
// ещё домены VK, которые встречаются на его страницах: старый
// vkontakte.ru, видео (vkuservideo.net) и счётчики VK (vk-portal.net).
var vkLoginDomains = append(slices.Clone(proxyDomains), "vkontakte.ru", "vkuservideo.net", "vk-portal.net")

// NetFilter — какие домены пускает окно входа в диагностическом режиме.
type NetFilter struct {
	all     bool
	domains []string
	spec    string
}

// ParseNetFilter разбирает значение -net-allow: NetAllowAll или список
// через запятую, где каждый элемент — домен (пускаются он и его
// поддомены) или NetAllowVK.
func ParseNetFilter(value string) (*NetFilter, error) {
	var items []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.ToLower(strings.TrimSpace(item)); item != "" {
			items = append(items, item)
		}
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("пустой список: нужны домены через запятую, %s или %s", NetAllowVK, NetAllowAll)
	}

	f := &NetFilter{}
	var spec []string
	for _, item := range items {
		switch item {
		case NetAllowAll:
			if len(items) > 1 {
				return nil, fmt.Errorf("%s пускает всё — с другими значениями его не сочетают", NetAllowAll)
			}
			return &NetFilter{all: true, spec: NetAllowAll}, nil
		case NetAllowVK:
			f.domains = append(f.domains, vkLoginDomains...)
		default:
			domain, err := parseDomain(item)
			if err != nil {
				return nil, err
			}
			item = domain
			f.domains = append(f.domains, domain)
		}
		if !slices.Contains(spec, item) {
			spec = append(spec, item)
		}
	}
	slices.Sort(f.domains)
	f.domains = slices.Compact(f.domains)
	f.spec = strings.Join(spec, ",")
	return f, nil
}

// parseDomain — домен из элемента списка; «*.» или «.» в начале и точка в
// конце снимаются.
func parseDomain(item string) (string, error) {
	domain := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(item, "*."), "."), ".")
	if _, err := netip.ParseAddr(domain); err == nil {
		return "", fmt.Errorf("%q — адрес, а нужен домен: фильтр смотрит на имена", item)
	}
	if !isHostname(domain) {
		return "", fmt.Errorf("%q — не домен: только буквы, цифры, «-» и точки, без схемы, порта и пути", item)
	}
	return domain, nil
}

// isHostname — s похоже на имя хоста: латиница, цифры, «-», «_», непустые
// части через точку.
func isHostname(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
				return false
			}
		}
	}
	return true
}

// String — значение фильтра в разобранном виде: для вывода.
func (f *NetFilter) String() string { return f.spec }

// allows — пускать ли окно к host.
func (f *NetFilter) allows(host string) bool {
	return f.all || domainAllowed(host, f.domains)
}

// netJournal — политика прокси окна входа: фильтр и журнал соединений.
// Строка — на первое появление хоста и на первую неудачу соединения с
// ним; повторы только считаются и видны в сводке.
type netJournal struct {
	filter *NetFilter
	out    io.Writer
	logf   func(string, ...any)

	mu      sync.Mutex
	entries map[string]*netEntry
}

type netEntry struct {
	allowed bool
	count   int // сколько раз просили
	failed  int // сколько пропущенных не соединились
}

func newNetJournal(filter *NetFilter, out io.Writer, logf func(string, ...any)) *netJournal {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &netJournal{filter: filter, out: out, logf: logf, entries: map[string]*netEntry{}}
}

// unreadable — что пишется вместо адреса, который не разобрать.
const unreadable = "<адрес не разобрать>"

// journalKey — запись журнала для target: «хост:порт», у обычного HTTP — с
// пометкой. Хост и порт проверяются: в журнал не попадает ничего, кроме
// имени или адреса и номера порта.
func journalKey(target string, plain bool) (key, host string, ok bool) {
	host, port, err := net.SplitHostPort(target)
	ok = err == nil && isPort(port) && (isHostname(host) || isAddr(host))
	if !ok {
		key, host = unreadable, ""
	} else {
		key = net.JoinHostPort(host, port)
	}
	if plain {
		key += " (HTTP)"
	}
	return key, host, ok
}

func isPort(s string) bool {
	if s == "" || len(s) > 5 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isAddr(s string) bool {
	_, err := netip.ParseAddr(s)
	return err == nil
}

func (j *netJournal) allow(target string, plain bool, _ string) int {
	key, host, ok := journalKey(target, plain)
	allowed := ok && j.filter.allows(host)

	j.mu.Lock()
	defer j.mu.Unlock()
	e := j.entries[key]
	if e == nil {
		e = &netEntry{allowed: allowed}
		j.entries[key] = e
		fmt.Fprintf(j.out, "[VK] окно входа, сеть: %s — %s\n", key, verdict(allowed))
	}
	e.count++
	if !allowed {
		if ok {
			return http.StatusForbidden
		}
		return http.StatusBadRequest
	}
	return 0
}

func (j *netJournal) failed(target string, plain bool, err error) {
	key, _, _ := journalKey(target, plain)

	j.mu.Lock()
	defer j.mu.Unlock()
	e := j.entries[key]
	if e == nil {
		return
	}
	e.failed++
	if e.failed == 1 {
		fmt.Fprintf(j.out, "[VK] окно входа, сеть: %s — пропущен, но не соединился\n", key)
		// Текст ошибки — только с -v: в нём бывает адрес резолвера сети.
		j.logf("[VK] окно входа, сеть: %s: %v", key, err)
	}
}

func verdict(allowed bool) string {
	if allowed {
		return "пропущен"
	}
	return "отказ"
}

// summary пишет сводку: какие хосты пропущены и какие получили отказ, со
// счётом обращений. Сначала пропущенные, потом отказы, внутри — по имени.
func (j *netJournal) summary(w io.Writer) {
	j.mu.Lock()
	defer j.mu.Unlock()

	keys := make([]string, 0, len(j.entries))
	for key := range j.entries {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(a, b string) int {
		ea, eb := j.entries[a], j.entries[b]
		if ea.allowed != eb.allowed {
			if ea.allowed {
				return -1
			}
			return 1
		}
		return strings.Compare(a, b)
	})

	var allowed, refused int
	for _, e := range j.entries {
		if e.allowed {
			allowed++
		} else {
			refused++
		}
	}
	fmt.Fprintf(w, "Сводка сети окна входа (-net-allow %s): пропущено хостов — %d, отказ — %d.\n",
		j.filter, allowed, refused)
	if len(keys) == 0 {
		fmt.Fprintln(w, "  соединений не было")
	}
	for _, key := range keys {
		e := j.entries[key]
		line := fmt.Sprintf("  %-8s  %s  ×%d", verdict(e.allowed), key, e.count)
		if e.failed > 0 {
			line += fmt.Sprintf(", не соединился ×%d", e.failed)
		}
		fmt.Fprintln(w, line)
	}
}

// loginNet — сеть окна входа в диагностическом режиме: прокси с журналом.
// Методы nil-безопасны: без режима окно ходит в сеть само.
type loginNet struct {
	proxy   *Proxy
	journal *netJournal
	out     *lockedWriter
}

// startLoginNet поднимает прокси окна входа с фильтром filter. nil filter —
// режим выключен, nil и в ответе. dial nil — обычный net.Dialer. Строки
// журнала уходят в out: их и запускают ради этого режима, поэтому они
// видны и без -v.
func startLoginNet(filter *NetFilter, dial DialFunc, out io.Writer, logf func(string, ...any)) (*loginNet, error) {
	if filter == nil {
		return nil, nil
	}
	if dial == nil {
		var d net.Dialer
		dial = d.DialContext
	}
	lw := &lockedWriter{w: out}
	journal := newNetJournal(filter, lw, logf)
	proxy, err := startProxy(dial, journal)
	if err != nil {
		return nil, fmt.Errorf("прокси окна входа не поднялся: %v", err)
	}
	fmt.Fprintf(lw, "Окно входа идёт в сеть через прокси csqtt-cli %s: -net-allow %s.\n", proxy.Addr(), filter)
	return &loginNet{proxy: proxy, journal: journal, out: lw}, nil
}

// browserArgs — аргументы браузера окна: прокси и WebRTC только через
// него (иначе UDP WebRTC прошёл бы мимо журнала).
func (n *loginNet) browserArgs() []string {
	if n == nil {
		return nil
	}
	return []string{
		"--proxy-server=http://" + n.proxy.Addr(),
		"--force-webrtc-ip-handling-policy=disable_non_proxied_udp",
	}
}

// writer — куда окну писать, пока работает прокси: строки журнала идут из
// его горутин в тот же поток.
func (n *loginNet) writer(out io.Writer) io.Writer {
	if n == nil {
		return out
	}
	return n.out
}

// close закрывает прокси и пишет сводку. Звать, когда окно закрыто: тогда
// в сводке все его соединения.
func (n *loginNet) close() {
	if n == nil {
		return
	}
	n.proxy.Close()
	n.journal.summary(n.out)
}

// lockedWriter — запись из нескольких горутин целыми строками.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// Проверка на этапе сборки: журнал — политика прокси.
var _ proxyPolicy = (*netJournal)(nil)
