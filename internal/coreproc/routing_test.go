package coreproc

import (
	"encoding/json"
	"reflect"
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

func ruleOf(m Message) *RoutingRule {
	if e, ok := m.(RoutingEvent); ok {
		return e.Rule
	}
	return nil
}
