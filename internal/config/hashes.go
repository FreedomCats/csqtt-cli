package config

import "strings"

// MaxHashes — сколько хешей звонков ядро берёт: первые шесть, остальные
// молча отбрасывает.
const MaxHashes = 6

// ParseHashes разбирает хеши звонков VK так же, как ядро разбирает -vk:
// разделители — запятая, точка с запятой и пробельные знаки; ссылка на звонок
// (…/call/join/<хеш>) сводится к хешу; повторы выбрасываются, порядок
// сохраняется. Совпадать с ядром важно: по числу хешей ядро раздаёт группы
// воркеров, и пустой список для него — отказ.
func ParseHashes(raw string) []string {
	var hashes []string
	seen := map[string]bool{}
	for part := range strings.FieldsFuncSeq(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	}) {
		if h := normalizeHash(part); h != "" && !seen[h] {
			seen[h] = true
			hashes = append(hashes, h)
		}
	}
	return hashes
}

// normalizeHash сводит хеш или ссылку на звонок к хешу; пусто — это не хеш.
// Другую ссылку в интернете ядро за хеш не принимает, и здесь так же.
func normalizeHash(s string) string {
	s = strings.Trim(strings.TrimSpace(s), `<>"'`)
	if i := strings.Index(strings.ToLower(s), "/call/join/"); i >= 0 {
		s = s[i+len("/call/join/"):]
	} else if lower := strings.ToLower(s); strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return ""
	}
	if i := strings.IndexAny(s, "?#/"); i >= 0 {
		s = s[:i]
	}
	return strings.Trim(strings.TrimSpace(s), "/")
}
