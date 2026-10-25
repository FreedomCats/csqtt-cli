package coreproc

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// routingLine — событие ROUTING, как его печатает ядро (events.rs): правило —
// строкой JSON внутри JSON.
func routingLine(t *testing.T, rule string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"routing": rule})
	if err != nil {
		t.Fatal(err)
	}
	return eventPrefix + "ROUTING|" + string(payload)
}

// Правило в том виде, в каком его собирает сервер 2.5.0 (protocol.rs): все
// поля, режим — флагом и словом. Домены и адреса выдуманы.
func TestParseRouting(t *testing.T) {
	rule := `{"domains":["example.ru","shop.example.com"],"domains_is_whitelist":false,` +
		`"domains_mode":"blacklist","ip_is_whitelist":true,"ip_mode":"whitelist",` +
		`"ips":["203.0.113.0/24","198.51.100.7","192.0.2.1-192.0.2.9"],"is_whitelist":false,` +
		`"mode":"blacklist","name":"Офис","packages":["com.example.app"],` +
		`"template_domains":true,"version":2}`
	want := RoutingEvent{Raw: rule, Rule: &RoutingRule{
		Version:         2,
		Name:            "Офис",
		Domains:         []string{"example.ru", "shop.example.com"},
		TemplateDomains: true,
		IPsWhitelist:    true,
		IPs:             []string{"203.0.113.0/24", "198.51.100.7", "192.0.2.1-192.0.2.9"},
		Packages:        []string{"com.example.app"},
	}}
	if got := ParseLine(routingLine(t, rule)); !reflect.DeepEqual(got, want) {
		t.Errorf("ParseLine = %#v (%+v), want %+v", got, ruleOf(got), want.Rule)
	}
}

// Разбор терпимый: лишние поля пропускаются, поле не того типа — нулевое,
// в списках — только непустые строки, режим без флага — по слову.
func TestParseRoutingTolerant(t *testing.T) {
	rule := `{"version":"2","name":5,"future":{"x":1},"domains":["a.example.ru",7,"  ",null," b.example.ru "],` +
		`"domains_mode":"WHITELIST","template_domains":"yes","ips":"203.0.113.1",` +
		`"ip_is_whitelist":false,"ip_mode":"whitelist","mode":"whitelist","packages":null}`
	want := &RoutingRule{
		Domains:          []string{"a.example.ru", "b.example.ru"},
		DomainsWhitelist: true,
		AppsWhitelist:    true,
	}
	got, ok := ParseLine(routingLine(t, rule)).(RoutingEvent)
	if !ok || got.Raw != rule || !reflect.DeepEqual(got.Rule, want) {
		t.Errorf("правило %+v, want %+v (событие %v)", got.Rule, want, ok)
	}
}

// Неразборчивое правило — всё равно событие: что сервер его прислал, знать
// важно. Rule пуст, Raw — как пришло.
func TestParseRoutingUnreadable(t *testing.T) {
	for _, rule := range []string{"", "not json", `{"name":`, "[1,2]", "null", `"text"`} {
		got := ParseLine(routingLine(t, rule))
		if want := (RoutingEvent{Raw: rule}); !reflect.DeepEqual(got, want) {
			t.Errorf("правило %q: ParseLine = %#v", rule, got)
		}
	}
}

// Правило — в команду UPDATE_DOMAINS со всеми восемью ключами: команда
// заменяет конфигурацию ядра целиком. enabled — есть домены или шаблон в
// чёрном списке, ip_enabled — есть адреса; ru — всегда 0; приложения Android
// не попадают никуда.
func TestRoutingUpdate(t *testing.T) {
	for _, tc := range []struct {
		name string
		rule RoutingRule
		want string
		// direct, white — Direct() и Whitelist() команды.
		direct, white bool
	}{
		{"пустое", RoutingRule{Packages: []string{"com.example.app"}},
			"enabled=0;ru=0;template=0;whitelist=0;domains=;ip_enabled=0;ip_whitelist=0;ips=", false, false},
		{"шаблон", RoutingRule{TemplateDomains: true},
			"enabled=1;ru=0;template=1;whitelist=0;domains=;ip_enabled=0;ip_whitelist=0;ips=", true, false},
		{"чёрные списки", RoutingRule{Domains: []string{"example.ru", "shop.example.com"},
			IPs: []string{"203.0.113.0/24", "198.51.100.7", "192.0.2.1-192.0.2.9"}},
			"enabled=1;ru=0;template=0;whitelist=0;domains=example.ru,shop.example.com;" +
				"ip_enabled=1;ip_whitelist=0;ips=203.0.113.0/24,198.51.100.7,192.0.2.1-192.0.2.9", true, false},
		{"белый список доменов", RoutingRule{DomainsWhitelist: true, Domains: []string{"example.com"}, TemplateDomains: true},
			"enabled=1;ru=0;template=1;whitelist=1;domains=example.com;ip_enabled=0;ip_whitelist=0;ips=", true, true},
		{"белый список адресов", RoutingRule{IPsWhitelist: true, IPs: []string{"203.0.113.0/24"}},
			"enabled=0;ru=0;template=0;whitelist=0;domains=;ip_enabled=1;ip_whitelist=1;ips=203.0.113.0/24", true, true},
		// Пустой белый список ядро поняло бы как «напрямую всё»: не
		// включается, и шаблон в белом списке ядро не учитывает.
		{"пустой белый список", RoutingRule{DomainsWhitelist: true, TemplateDomains: true, IPsWhitelist: true},
			"enabled=0;ru=0;template=1;whitelist=1;domains=;ip_enabled=0;ip_whitelist=1;ips=", false, false},
		{"белый список с годным после чистки", RoutingRule{DomainsWhitelist: true,
			Domains: []string{"https://www.Example.COM:443/path", "example.com.", "shop.example.com"}},
			"enabled=1;ru=0;template=0;whitelist=1;domains=example.com,shop.example.com;" +
				"ip_enabled=0;ip_whitelist=0;ips=", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u, err := tc.rule.Update()
			if err != nil {
				t.Fatal(err)
			}
			if want := "UPDATE_DOMAINS|" + tc.want; u.Command != want {
				t.Errorf("команда\n%s\nждали\n%s", u.Command, want)
			}
			if u.Direct() != tc.direct || u.Whitelist() != tc.white || u.Dropped != 0 || u.WideIP != "" {
				t.Errorf("Direct %v, Whitelist %v, Dropped %d, WideIP %q; ждали %v, %v, 0, пусто", u.Direct(), u.Whitelist(),
					u.Dropped, u.WideIP, tc.direct, tc.white)
			}
		})
	}
}

// Элемент списка, который вписал бы в команду свои ключи или сломал строку
// stdin, отбрасывается и считается. Белый список, от которого ничего не
// осталось, не включается.
func TestRoutingUpdateDropsUnsafe(t *testing.T) {
	bad := []string{"a.ru;ip_whitelist=1", "a.ru|STOP", "a.ru,b.ru", "x=1", "a.ru\nSTOP", "a\tb.ru", "a .ru",
		"a.ru\u00a0", "a\u202e.ru", "a.ru\x00", ""}
	u, err := (&RoutingRule{Domains: append([]string{"ok.example.ru"}, bad...),
		IPs: []string{"203.0.113.1", "203.0.113.2 - 203.0.113.9", "198.51.100.0/24;enabled=1"}}).Update()
	if err != nil {
		t.Fatal(err)
	}
	want := "UPDATE_DOMAINS|enabled=1;ru=0;template=0;whitelist=0;domains=ok.example.ru;ip_enabled=1;ip_whitelist=0;ips=203.0.113.1"
	if u.Command != want || u.Dropped != len(bad)+2 {
		t.Errorf("команда %q, отброшено %d; ждали %q, %d", u.Command, u.Dropped, want, len(bad)+2)
	}

	u, err = (&RoutingRule{DomainsWhitelist: true, Domains: []string{"a.ru;b.ru"},
		IPsWhitelist: true, IPs: []string{"203.0.113.0/24,0.0.0.0/0"}}).Update()
	if err != nil {
		t.Fatal(err)
	}
	if u.Direct() || u.Whitelist() || u.Dropped != 2 {
		t.Errorf("белые списки из отброшенных: %+v", u)
	}
}

// Домен, который ядро после своей чистки выбросило бы, и адрес не той формы
// отбрасываются здесь же и считаются; белый список из одних таких не
// включается. Формы адресов — те, что принимает ядро (sni.rs,
// parse_ip_rules).
func TestRoutingUpdateDropsUnusable(t *testing.T) {
	u, err := (&RoutingRule{DomainsWhitelist: true, Domains: []string{"ru", "localhost", "https://www.ab/", "..x.."},
		IPsWhitelist: true, IPs: []string{"2001:db8::/32", "2001:db8::1", "::ffff:192.0.2.1"}}).Update()
	if err != nil {
		t.Fatal(err)
	}
	if u.Direct() || u.Whitelist() || u.Dropped != 7 || u.Command !=
		"UPDATE_DOMAINS|enabled=0;ru=0;template=0;whitelist=1;domains=;ip_enabled=0;ip_whitelist=1;ips=" {
		t.Errorf("только негодное: %+v", u)
	}

	good := []string{"192.0.2.1", "203.0.113.0/24", "198.51.100.0/0", "192.0.2.9/32", "192.0.2.1-192.0.2.1",
		"192.0.2.1-198.51.100.7"}
	bad := []string{"192.0.2", "192.0.2.256", "01.2.3.4", "192.0.2.0/33", "192.0.2.0/", "/8", "192.0.2.0/-1",
		"192.0.2.9-192.0.2.1", "192.0.2.1-", "192.0.2.1-2001:db8::1", "example.com", "1.2.3.4/8/8"}
	u, err = (&RoutingRule{IPs: append(append([]string(nil), good...), bad...)}).Update()
	if err != nil {
		t.Fatal(err)
	}
	if want := "ips=" + strings.Join(good, ","); !strings.HasSuffix(u.Command, want) || u.Dropped != len(bad) || !u.IPs {
		t.Errorf("адреса: %+v, ждали хвост %q и отброшенных %d", u, want, len(bad))
	}
}

// Домен с национальными знаками уходит ядру в punycode: ядро сравнивает имена
// байт в байт, а в SNI и DNS имя приходит в punycode. Чистка (www., регистр,
// точки) — и до перевода, и после; непереводимый домен отбрасывается; IDN и
// его punycode — один домен, и в пределе MaxRoutingDomains — тоже один. К
// ASCII перевод не применяется: домен с _ остаётся.
func TestRoutingUpdateIDN(t *testing.T) {
	const puny = "xn--e1afmkfd.xn--p1ai" // пример.рф
	domains := []string{"пример.рф", "https://WWW.ПРИМЕР.РФ/путь", "Пример.рф.", "www。пример。рф", puny,
		"XN--E1AFMKFD.XN--P1AI", "_srv.example.ru", "-пример.рф", "пример_1.рф", "рф"}
	u, err := (&RoutingRule{DomainsWhitelist: true, Domains: domains}).Update()
	if err != nil {
		t.Fatal(err)
	}
	if want := "domains=" + puny + ",_srv.example.ru;"; !strings.Contains(u.Command, want) || u.Dropped != 3 {
		t.Errorf("команда %q, отброшено %d; ждали %q, отброшенных 3", u.Command, u.Dropped, want)
	}

	full := make([]string, MaxRoutingDomains)
	for i := range full {
		full[i] = fmt.Sprintf("d%d.example.ru", i)
	}
	full[0] = puny
	if _, err := (&RoutingRule{DomainsWhitelist: true, Domains: append(full, "пример.рф")}).Update(); err != nil {
		t.Errorf("белый список %d доменов с IDN-повтором: %v", MaxRoutingDomains, err)
	}
}

// Чёрный список адресов с правилом на /8 публичных адресов и больше — WideIP,
// первое такое правило; в белом списке и без включённых адресов — нет.
// Частные сети, 0/8, 100.64/10, петля, мультикаст и выше ядро напрямую не
// пускает — они не в счёт.
func TestRoutingUpdateWideIP(t *testing.T) {
	for _, tc := range []struct {
		rule RoutingRule
		want string
	}{
		{RoutingRule{IPs: []string{"203.0.113.0/24", "0.0.0.0/0", "1.0.0.0/8"}}, "0.0.0.0/0"},
		{RoutingRule{IPs: []string{"1.0.0.0-255.255.255.255"}}, "1.0.0.0-255.255.255.255"},
		{RoutingRule{IPs: []string{"10.0.0.0/8", "1.0.0.0/8"}}, "1.0.0.0/8"},
		{RoutingRule{IPs: []string{"1.0.0.0-1.255.255.255"}}, "1.0.0.0-1.255.255.255"},
		{RoutingRule{IPs: []string{"1.0.0.1-2.0.0.0"}}, "1.0.0.1-2.0.0.0"},
		{RoutingRule{IPs: []string{"9.0.0.0-10.255.255.255"}}, "9.0.0.0-10.255.255.255"},
		{RoutingRule{IPs: []string{"1.0.0.0/9", "1.0.0.1-1.255.255.255"}}, ""},
		{RoutingRule{IPs: []string{"10.0.0.0/8", "0.0.0.0/8", "224.0.0.0/4", "240.0.0.0/4", "100.64.0.0/10",
			"127.0.0.0/8", "10.0.0.0-10.255.255.255", "172.16.0.0/12", "192.168.0.0/16"}}, ""},
		{RoutingRule{IPs: []string{"100.0.0.0/8", "172.0.0.0/8", "9.128.0.0-10.255.255.255"}}, ""},
		{RoutingRule{IPsWhitelist: true, IPs: []string{"0.0.0.0/0"}}, ""},
		{RoutingRule{IPsWhitelist: true, IPs: []string{"2001:db8::/32"}}, ""},
	} {
		u, err := tc.rule.Update()
		if err != nil || u.WideIP != tc.want {
			t.Errorf("%+v: WideIP %q, %v; ждали %q", tc.rule, u.WideIP, err, tc.want)
		}
	}
}

// Белый список доменов длиннее, чем ядро берёт, не применяется: лишние ушли
// бы мимо туннеля. Повторы не в счёт. Чёрный — применяется как есть.
func TestRoutingUpdateTooManyDomains(t *testing.T) {
	domains := make([]string, MaxRoutingDomains+1)
	for i := range domains {
		domains[i] = fmt.Sprintf("d%d.example.ru", i)
	}
	var many *RoutingTooManyDomainsError
	u, err := (&RoutingRule{DomainsWhitelist: true, Domains: domains}).Update()
	if !errors.As(err, &many) || many.Domains != MaxRoutingDomains+1 || u.Command != "" {
		t.Errorf("белый список %d доменов: %+v, %v", len(domains), u, err)
	}
	if _, err := (&RoutingRule{DomainsWhitelist: true, Domains: append(domains[:MaxRoutingDomains:MaxRoutingDomains],
		"D0.example.ru", "www.d1.example.ru")}).Update(); err != nil {
		t.Errorf("белый список %d доменов с повторами: %v", MaxRoutingDomains, err)
	}
	if u, err := (&RoutingRule{Domains: domains}).Update(); err != nil || !u.Domains {
		t.Errorf("чёрный список %d доменов: %+v, %v", len(domains), u, err)
	}
}

// Команда длиннее строки stdin ядра (64 КиБ с переводом строки) — ошибка:
// такую ядро не примет и остановится. Ровно на пределе — годна.
func TestRoutingUpdateTooLong(t *testing.T) {
	fill := MaxCommandBytes - 1 - len(ProfileRouting(false).Command) // место под один домен
	fits := RoutingRule{Domains: []string{strings.Repeat("a", fill-3) + ".ru"}}
	u, err := fits.Update()
	if err != nil || len(u.Command)+1 != MaxCommandBytes {
		t.Fatalf("на пределе: %d байт, %v", len(u.Command)+1, err)
	}
	over := RoutingRule{Domains: append(fits.Domains, "b.ru"), IPs: []string{"bad;"}}
	u, err = over.Update()
	var long *RoutingTooLongError
	if !errors.As(err, &long) || long.Bytes != MaxCommandBytes+5 || u.Command != "" || u.Dropped != 1 {
		t.Errorf("сверх предела: %+v, %v", u, err)
	}

	// Тысячи адресов — тоже: сервер шлёт правило кусками, ядро его соберёт.
	many := make([]string, 6000)
	for i := range many {
		many[i] = fmt.Sprintf("10.%d.%d.0/24", i/256, i%256)
	}
	if _, err := (&RoutingRule{IPs: many}).Update(); !errors.As(err, &long) {
		t.Errorf("6000 адресов: %v", err)
	}
}

// ProfileRouting — та же маршрутизация, что ядро получило аргументами.
func TestProfileRouting(t *testing.T) {
	if got := ProfileRouting(true); !got.Direct() || !strings.Contains(got.Command, "enabled=1;ru=0;template=1;") {
		t.Errorf("с шаблоном: %+v", got)
	}
	if got := ProfileRouting(false); got.Direct() || got.Command !=
		"UPDATE_DOMAINS|enabled=0;ru=0;template=0;whitelist=0;domains=;ip_enabled=0;ip_whitelist=0;ips=" {
		t.Errorf("без шаблона: %+v", got)
	}
}

func ruleOf(m Message) *RoutingRule {
	if e, ok := m.(RoutingEvent); ok {
		return e.Rule
	}
	return nil
}
