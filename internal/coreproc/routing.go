package coreproc

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/idna"
)

// RoutingEvent — сервер прислал правило маршрутизации, назначенное паролю в
// панели (ROUTING). Ядро собирает его из кусков и передаёт как есть, само
// ничего не применяет: применить — команда UPDATE_DOMAINS от оболочки.
// Приходит на каждое подключение первого воркера (в том числе при каждом
// запуске ядра), если правило назначено; «правило снято» сервер не шлёт.
type RoutingEvent struct {
	// Raw — JSON правила, как его прислал сервер.
	Raw string
	// Rule — разобранное правило; nil — Raw не объект JSON.
	Rule *RoutingRule
}

// RoutingRule — правило маршрутизации сервера CSQTT 2.5.0 (JSON версии 2).
// Чёрный список — перечисленное идёт мимо туннеля, прочее через него; белый —
// наоборот: через туннель только перечисленное.
//
// Поля сервер шлёт все, но разбор терпимый: поле, которого нет или у которого
// не тот тип, остаётся нулевым, неизвестные пропускаются, в списках — только
// строки.
type RoutingRule struct {
	Version int
	Name    string

	// Домены: суффиксное совпадение, как у --direct-domains ядра.
	DomainsWhitelist bool
	Domains          []string
	// TemplateDomains — встроенный список ядра (--template-domains); в белом
	// списке доменов ядро его не учитывает.
	TemplateDomains bool

	// Адреса IPv4: адрес, CIDR или диапазон a-b, как у --direct-ips.
	IPsWhitelist bool
	IPs          []string

	// Packages — приложения Android, которые клиент на телефоне пускает мимо
	// VPN (чёрный список) или только которые в него берёт (белый). На
	// компьютере применить нечего.
	AppsWhitelist bool
	Packages      []string
}

func (RoutingEvent) message() {}

// parseRouting — {"routing":"<JSON правила>"}. Испорченная обёртка — не
// событие (ok = false); испорченное правило — событие без Rule: что сервер
// прислал правило, знать важно, даже если его не понять.
func parseRouting(payload string) (RoutingEvent, bool) {
	var raw struct {
		Routing *string `json:"routing"`
	}
	if json.Unmarshal([]byte(payload), &raw) != nil || raw.Routing == nil {
		return RoutingEvent{}, false
	}
	e := RoutingEvent{Raw: *raw.Routing}
	e.Rule = parseRoutingRule(e.Raw)
	return e, true
}

func parseRoutingRule(text string) *RoutingRule {
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(text), &fields) != nil || fields == nil {
		return nil
	}
	str := func(key string) string {
		var s string
		_ = json.Unmarshal(fields[key], &s)
		return s
	}
	list := func(key string) []string {
		var items []json.RawMessage
		_ = json.Unmarshal(fields[key], &items)
		var out []string
		for _, item := range items {
			var s string
			if json.Unmarshal(item, &s) == nil && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	}
	// Режим сервер пишет дважды: флагом и словом. Флаг главнее; нет его —
	// по слову.
	whitelist := func(flag, mode string) bool {
		var b bool
		if json.Unmarshal(fields[flag], &b) == nil {
			return b
		}
		return strings.EqualFold(str(mode), "whitelist")
	}
	r := &RoutingRule{
		Name:             strings.TrimSpace(str("name")),
		DomainsWhitelist: whitelist("domains_is_whitelist", "domains_mode"),
		Domains:          list("domains"),
		IPsWhitelist:     whitelist("ip_is_whitelist", "ip_mode"),
		IPs:              list("ips"),
		AppsWhitelist:    whitelist("is_whitelist", "mode"),
		Packages:         list("packages"),
	}
	_ = json.Unmarshal(fields["version"], &r.Version)
	_ = json.Unmarshal(fields["template_domains"], &r.TemplateDomains)
	return r
}

// cmdUpdateDomains — команда ядру: заменить конфигурацию маршрутизации мимо
// туннеля (main.rs, start_control_input → dispatcher.rs,
// update_domain_routing).
const cmdUpdateDomains = "UPDATE_DOMAINS|"

// RoutingUpdate — команда UPDATE_DOMAINS, в которую переводится правило
// сервера (RoutingRule.Update), и что она включает.
type RoutingUpdate struct {
	// Command — строка для Process.Send, без перевода строки.
	Command string
	// Dropped — сколько элементов списков отброшено: ядро их не примет
	// (негодный домен, адрес не IPv4) или они сломали бы команду (знаки ; , =
	// |, пробелы, управляющие).
	Dropped int
	// Domains и IPs — включена маршрутизация по доменам (enabled) и по
	// адресам (ip_enabled).
	Domains, IPs bool
	// DomainsWhitelist и IPsWhitelist — включённый белый список: в туннель
	// только перечисленное, всё прочее ядро шлёт напрямую.
	DomainsWhitelist, IPsWhitelist bool
	// WideIP — первое правило включённого чёрного списка адресов, которое
	// покрывает не меньше WideIPRule публичных адресов: мимо туннеля пойдёт
	// огромная часть трафика. Пусто — таких нет.
	WideIP string
}

// WideIPRule — сколько публичных адресов покрывает «огромное» правило чёрного
// списка (RoutingUpdate.WideIP): сеть /8 и шире. Адреса tunnelOnlyIPs не в
// счёт: их ядро напрямую не пускает.
const WideIPRule = 1 << 24

// tunnelOnlyIPs — адреса IPv4, которые ядро всегда шлёт в туннель, какое бы
// правило ни было (direct_relay.rs, try_handle и is_local_or_private_ip):
// 0/8, частные сети, 100.64/10, петля, link-local, мультикаст и всё выше
// него. По возрастанию, без пересечений.
var tunnelOnlyIPs = [][2]uint32{
	{0x00000000, 0x00ffffff}, // 0.0.0.0/8
	{0x0a000000, 0x0affffff}, // 10.0.0.0/8
	{0x64400000, 0x647fffff}, // 100.64.0.0/10
	{0x7f000000, 0x7fffffff}, // 127.0.0.0/8
	{0xa9fe0000, 0xa9feffff}, // 169.254.0.0/16
	{0xac100000, 0xac1fffff}, // 172.16.0.0/12
	{0xc0a80000, 0xc0a8ffff}, // 192.168.0.0/16
	{0xe0000000, 0xffffffff}, // 224.0.0.0/3
}

// MaxRoutingDomains — сколько доменов ядро берёт из команды; остальные
// отбрасывает молча (dispatcher.rs, update_domain_routing).
const MaxRoutingDomains = 1024

// Direct — команда пускает часть трафика мимо туннеля.
func (u RoutingUpdate) Direct() bool { return u.Domains || u.IPs }

// Whitelist — включён хоть один белый список.
func (u RoutingUpdate) Whitelist() bool { return u.DomainsWhitelist || u.IPsWhitelist }

// RoutingTooLongError — команда для правила длиннее, чем ядро примет одной
// строкой stdin (MaxCommandBytes).
type RoutingTooLongError struct{ Bytes int }

func (e *RoutingTooLongError) Error() string {
	return fmt.Sprintf("команда UPDATE_DOMAINS для правила — %d байт, ядро принимает строку не длиннее %d",
		e.Bytes, MaxCommandBytes)
}

// RoutingTooManyDomainsError — в белом списке доменов больше, чем ядро берёт
// (MaxRoutingDomains): лишние ядро отбросило бы, и они ушли бы мимо туннеля.
type RoutingTooManyDomainsError struct{ Domains int }

func (e *RoutingTooManyDomainsError) Error() string {
	return fmt.Sprintf("в белом списке %d доменов, ядро берёт не больше %d", e.Domains, MaxRoutingDomains)
}

// ProfileRouting — команда, возвращающая ядру ту маршрутизацию, с которой
// оно запущено (Config.TemplateDomains): правила сервера больше нет.
func ProfileRouting(template bool) RoutingUpdate {
	u, _ := (&RoutingRule{TemplateDomains: template}).Update()
	return u
}

// Update переводит правило в команду UPDATE_DOMAINS. Команда заменяет
// конфигурацию ядра целиком — отсутствующий ключ ядро считает выключенным, —
// поэтому в ней все восемь ключей.
//
//   - ru=0 всегда: «российских зон» в правиле нет;
//   - template — template_domains правила, whitelist и ip_whitelist — режимы
//     списков; приложения Android (packages) на компьютере неприменимы;
//   - домены — почищенные так же, как их чистит ядро (routingDomains),
//     адреса — только те формы, что ядро принимает (routingIPs); прочее и
//     элементы со знаками, которые ядро считает разделителями, отбрасываются
//     (Dropped): иначе текст сервера дописал бы в команду свои ключи;
//   - enabled — есть домены или шаблон в чёрном списке; ip_enabled — есть
//     адреса. Белый список, пустой после отбора, не включается: в нём ядро
//     пустило бы напрямую вообще всё (sni.rs, evaluate_route).
//
// Белый список доменов длиннее MaxRoutingDomains — *RoutingTooManyDomainsError,
// команда длиннее MaxCommandBytes — *RoutingTooLongError: такую строку ядро не
// примет и остановится.
func (r *RoutingRule) Update() (RoutingUpdate, error) {
	domains, droppedDomains := routingDomains(r.Domains)
	ips, droppedIPs, wide := routingIPs(r.IPs)
	u := RoutingUpdate{Dropped: droppedDomains + droppedIPs}
	if r.DomainsWhitelist && len(domains) > MaxRoutingDomains {
		return u, &RoutingTooManyDomainsError{Domains: len(domains)}
	}
	u.Domains = len(domains) > 0 || r.TemplateDomains && !r.DomainsWhitelist
	u.IPs = len(ips) > 0
	u.DomainsWhitelist = u.Domains && r.DomainsWhitelist
	u.IPsWhitelist = u.IPs && r.IPsWhitelist
	if u.IPs && !r.IPsWhitelist {
		u.WideIP = wide
	}
	flag := func(b bool) string {
		if b {
			return "1"
		}
		return "0"
	}
	u.Command = cmdUpdateDomains + strings.Join([]string{
		"enabled=" + flag(u.Domains),
		"ru=0",
		"template=" + flag(r.TemplateDomains),
		"whitelist=" + flag(r.DomainsWhitelist),
		"domains=" + strings.Join(domains, ","),
		"ip_enabled=" + flag(u.IPs),
		"ip_whitelist=" + flag(r.IPsWhitelist),
		"ips=" + strings.Join(ips, ","),
	}, ";")
	if n := len(u.Command) + 1; n > MaxCommandBytes {
		return RoutingUpdate{Dropped: u.Dropped}, &RoutingTooLongError{Bytes: n}
	}
	return u, nil
}

// unsafeItem — элемент нельзя вписать в команду как есть: ядро режет её по ;
// и =, списки — по , и пробелам; | и управляющие знаки (переводы строк)
// ломают саму строку stdin, знаки форматирования исказили бы журнал ядра с
// -v.
func unsafeItem(item string) bool {
	return item == "" || strings.ContainsAny(item, ";|,=") || strings.IndexFunc(item, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
	}) >= 0
}

// routingDomains — домены правила, почищенные, как их чистит ядро (sni.rs,
// parse_direct_domains): без схемы, пути, порта, www. и точек по краям, в
// нижнем регистре, без повторов. Домен с национальными знаками переводится в
// punycode (непереводимый отброшен); повторы и предел MaxRoutingDomains
// считаются уже после перевода. Остался не домен — короче трёх знаков или без
// точки — элемент отброшен: ядро выбросило бы его молча, а белый список из
// одних таких ядро приняло бы пустым — и пустило бы напрямую всё. Повторы не
// считаются отброшенными.
func routingDomains(items []string) (kept []string, dropped int) {
	seen := make(map[string]bool)
	for _, item := range items {
		d, ok := coreDomain(item)
		if unsafeItem(item) || !ok {
			dropped++
			continue
		}
		if !seen[d] {
			seen[d] = true
			kept = append(kept, d)
		}
	}
	return kept, dropped
}

// coreDomain — домен, который ядро получит из элемента (routingDomains), и
// годен ли он.
func coreDomain(item string) (string, bool) {
	if _, rest, ok := strings.Cut(item, "://"); ok {
		item = rest
	}
	item, _, _ = strings.Cut(item, "/")
	item, _, _ = strings.Cut(item, ":")
	item = trimDomain(item)
	if strings.ContainsFunc(item, func(r rune) bool { return r > unicode.MaxASCII }) {
		// Ядро сравнивает имена байт в байт и IDN не переводит, а в SNI и DNS
		// имя уходит в punycode: без перевода такой домен не совпал бы ни с
		// чем. Перевод — UTS46 без transitional, как у браузеров. К ASCII он
		// не применяется: правила STD3 отбросили бы домены с _.
		a, err := idna.Lookup.ToASCII(item)
		if err != nil {
			return "", false
		}
		item = trimDomain(a)
	}
	return item, len(item) >= 3 && strings.Contains(item, ".")
}

// trimDomain — домен без www. и точек по краям, в нижнем регистре.
func trimDomain(item string) string {
	if len(item) >= 4 && strings.EqualFold(item[:4], "www.") {
		item = item[4:]
	}
	return strings.ToLower(strings.Trim(item, "."))
}

// routingIPs — правила адресов в тех формах, что ядро принимает (sni.rs,
// parse_ip_rules): адрес IPv4, сеть IPv4 «адрес/длина» (0–32) или диапазон
// «a-b» из двух адресов IPv4 по возрастанию. Прочее — IPv6, мусор — ядро
// выбросило бы молча, здесь оно отброшено. wide — первое правило не меньше
// WideIPRule публичных адресов (publicIPs).
func routingIPs(items []string) (kept []string, dropped int, wide string) {
	for _, item := range items {
		first, last, ok := ipRuleRange(item)
		if unsafeItem(item) || !ok {
			dropped++
			continue
		}
		kept = append(kept, item)
		if wide == "" && publicIPs(first, last) >= WideIPRule {
			wide = item
		}
	}
	return kept, dropped, wide
}

// ipRuleRange — первый и последний адрес правила адресов; ok = false — ядро
// такое правило не примет.
func ipRuleRange(item string) (first, last uint32, ok bool) {
	ipv4 := func(s string) (uint32, bool) {
		a, err := netip.ParseAddr(s)
		if err != nil || !a.Is4() {
			return 0, false
		}
		b := a.As4()
		return binary.BigEndian.Uint32(b[:]), true
	}
	if addr, bits, found := strings.Cut(item, "/"); found {
		n, err := strconv.ParseUint(bits, 10, 8)
		a, ok := ipv4(addr)
		if !ok || err != nil || n > 32 {
			return 0, 0, false
		}
		host := uint32(uint64(1)<<(32-n) - 1)
		return a &^ host, a | host, true
	}
	if from, to, found := strings.Cut(item, "-"); found {
		a, okA := ipv4(from)
		b, okB := ipv4(to)
		if !okA || !okB || b < a {
			return 0, 0, false
		}
		return a, b, true
	}
	a, ok := ipv4(item)
	return a, a, ok
}

// publicIPs — сколько адресов от first до last ядро может пустить напрямую:
// все, кроме tunnelOnlyIPs.
func publicIPs(first, last uint32) uint64 {
	n := uint64(last) - uint64(first) + 1
	for _, r := range tunnelOnlyIPs {
		if lo, hi := max(first, r[0]), min(last, r[1]); lo <= hi {
			n -= uint64(hi) - uint64(lo) + 1
		}
	}
	return n
}
