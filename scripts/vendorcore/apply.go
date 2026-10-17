package main

import (
	"fmt"
	"regexp"
)

// applyRules применяет к тексту файла path все правила для него, по порядку.
// Текст — с LF: шаблоны написаны под него.
func applyRules(path, text string) (string, error) {
	for _, r := range rules {
		if r.File != path {
			continue
		}
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			return "", fmt.Errorf("шаблон для %s: %w", path, err)
		}
		if found := len(re.FindAllStringIndex(text, -1)); found != r.Count {
			return "", fmt.Errorf("замена в %s не сошлась: ожидалось совпадений %d, найдено %d.\n"+
				"Апстрим изменил этот код — поправьте шаблон в scripts/vendorcore/rules.go.\n"+
				"Шаблон: %s", path, r.Count, found, r.Pattern)
		}

		// Группа 1 — отступ совпавшей строки: вставка встаёт на тот же уровень.
		var replacement string
		switch {
		case r.Replace != nil:
			replacement = *r.Replace
		case r.InsertAfter != "":
			replacement = "${0}\n${1}" + r.InsertAfter
		case r.InsertBefore != "":
			replacement = "${1}" + r.InsertBefore + "\n${0}"
		default:
			return "", fmt.Errorf("правило для %s не задаёт ни Replace, ни InsertAfter, ни InsertBefore", path)
		}
		text = re.ReplaceAllString(text, replacement)
	}
	return text, nil
}
