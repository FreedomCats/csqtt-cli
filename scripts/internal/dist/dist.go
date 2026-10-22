// Package dist — состав архива релиза: что раскладывает scripts/release и
// что проверяет scripts/distcheck. Список один, чтобы они не разошлись.
package dist

// Docs — документы в каждом архиве. LICENSE.txt — лицензия оболочки (LICENSE
// в корне, MIT), LICENSE-core.txt — лицензия ядра (core/LICENSE). Обе — с
// .txt, как и wintun-LICENSE.txt: на Windows файл без расширения не
// открывается двойным щелчком.
var Docs = []string{"THIRD_PARTY_NOTICES.txt", "csqtt-cli.example.toml", "README.md", "LICENSE.txt", "LICENSE-core.txt"}

// Wintun — файлы Wintun в архиве Windows (scripts/internal/wintun).
var Wintun = []string{"wintun.dll", "wintun-LICENSE.txt"}

// Executables — исполняемые файлы архива для goos: оболочка и ядро. В tar.gz
// у них право на исполнение.
func Executables(goos string) []string {
	if goos == "windows" {
		return []string{"csqtt-cli.exe", "csqtt-core.exe"}
	}
	return []string{"csqtt-cli", "csqtt-core"}
}

// Files — весь состав архива для goos.
func Files(goos string) []string {
	files := Executables(goos)
	if goos == "windows" {
		files = append(files, Wintun...)
	}
	return append(files, Docs...)
}
