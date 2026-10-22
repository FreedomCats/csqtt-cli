package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// Load читает и разбирает файл настроек.
//
// Отсутствие файла — ошибка, обёрнутая вокруг os.ErrNotExist: вызывающий
// отличает «настроек ещё нет» от «настройки испорчены» через errors.Is,
// а не разбором текста.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("файл настроек %s: %w", path, err)
	}
	return Parse(data, path)
}

// Parse разбирает содержимое файла настроек и проверяет его.
//
// source попадает в сообщения об ошибках — это путь к файлу или любое другое
// название источника, по которому пользователь поймёт, что чинить.
func Parse(data []byte, source string) (*File, error) {
	var file File

	md, err := toml.Decode(string(data), &file)
	if err != nil {
		var perr toml.ParseError
		if errors.As(err, &perr) {
			// ErrorWithPosition показывает строку с указателем на место —
			// это гораздо понятнее, чем «ошибка на строке 12».
			return nil, fmt.Errorf("%s: %s", source, strings.TrimRight(perr.ErrorWithPosition(), "\n"))
		}
		return nil, fmt.Errorf("%s: %w", source, err)
	}

	for name, profile := range file.Profiles {
		profile.Name = name
	}

	problems := validateFile(&file)
	problems = append(problems, unknownKeyProblems(md)...)
	if len(problems) > 0 {
		return nil, fmt.Errorf("%s:\n  %s", source, strings.Join(problems, "\n  "))
	}

	return &file, nil
}

// unknownKeyProblems сообщает о ключах, которых в формате нет.
//
// Молча пропускать их нельзя: опечатка в имени ключа означает, что настройка
// не применилась, а узнать об этом можно было бы только по странному поведению
// программы. Отдельно ловятся попытки положить секрет прямо в файл — ключ
// назван почти правильно, и без подсказки причина отказа выглядит придиркой.
func unknownKeyProblems(md toml.MetaData) []string {
	var problems []string
	for _, key := range md.Undecoded() {
		full := key.String()
		switch last(key) {
		case "token", "access_token", "vk_token":
			problems = append(problems, fmt.Sprintf(
				"неизвестный ключ %s: токен VK в файле не хранится — `csqtt-cli vk login` кладёт его в хранилище "+
					"секретов, а в секцию [vk] — ссылку token_ref", full))
		case "password", "vk_hashes", "hashes", "vk":
			problems = append(problems, fmt.Sprintf(
				"неизвестный ключ %s: секреты в файле не хранятся — запишите значение через `csqtt-cli profile set`, "+
					"а в профиле оставьте ссылку password_ref или vk_hashes_ref", full))
		default:
			problems = append(problems, fmt.Sprintf("неизвестный ключ %s", full))
		}
	}
	return problems
}

func last(key toml.Key) string {
	if len(key) == 0 {
		return ""
	}
	return key[len(key)-1]
}
