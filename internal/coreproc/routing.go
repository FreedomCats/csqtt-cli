package coreproc

import (
	"encoding/json"
	"strings"
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
