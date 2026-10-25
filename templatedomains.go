package csqttcli

import (
	_ "embed"
	"strings"
)

// Встроенный список доменов ядра — тот же файл, что ядро вшивает себе
// (include_str! в sni.rs). Здесь он нужен только ради числа доменов в строке
// connect: какие именно домены идут мимо туннеля, решает ядро.
//
//go:embed core/domains.txt
var templateDomainsFile string

// TemplateDomains — сколько доменов во встроенном списке ядра: опция профиля
// routing.template пускает их мимо туннеля.
var TemplateDomains = countTemplateDomains(templateDomainsFile)

// countTemplateDomains считает домены так же, как ядро читает список:
// строка без пробелов по краям, пустые и с # в начале — не домены, повторы —
// один домен.
func countTemplateDomains(file string) int {
	seen := map[string]bool{}
	for line := range strings.Lines(file) {
		if d := strings.TrimSpace(line); d != "" && !strings.HasPrefix(d, "#") {
			seen[d] = true
		}
	}
	return len(seen)
}
