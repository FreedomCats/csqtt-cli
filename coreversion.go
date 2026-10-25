// Package csqttcli — версия ядра CSQTT, с которым собран csqtt-cli.
//
// Пакет лежит в корне модуля, потому что go:embed не берёт файлов выше
// каталога пакета, а источник версии один — core/CORE_VERSION: его пишет
// scripts/vendorcore при переносе ядра. Остальной код — в internal/ и cmd/.
package csqttcli

import (
	_ "embed"
	"strings"
)

//go:embed core/CORE_VERSION
var coreVersionFile string

// CoreVersion — версия ядра CSQTT (строка version в core/CORE_VERSION).
// Сервер CSQTT нужен той же версии: метки версии протокола у ядра нет, и
// сервер другой версии обычно просто молчит.
var CoreVersion = parseCoreVersion(coreVersionFile)

// parseCoreVersion достаёт version из CORE_VERSION; "неизвестна", если строки
// нет. Концы строк — любые: git на Windows выкладывает файл с CRLF.
func parseCoreVersion(file string) string {
	for line := range strings.Lines(file) {
		key, value, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(key) == "version" {
			if v := strings.TrimSpace(value); v != "" {
				return v
			}
		}
	}
	return "неизвестна"
}
