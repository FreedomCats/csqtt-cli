// Package config — профили csqtt-cli (profiles.toml) и хранилище секретов
// рядом с ними.
//
// Профиль заменяет флаги ядра: набор именованных настроек, между которыми
// переключаются одним словом в командной строке (§10 doc/architecture.md).
// Секретов в profiles.toml нет: пароль подключения и хеши звонков лежат в
// хранилище секретов (secret.go), а в профиле — только ссылки на них.
package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/FreedomCats/csqtt-cli/internal/app"
	"github.com/FreedomCats/csqtt-cli/internal/sudo"
)

// Имя каталога и файла настроек. Каталог общий для всей программы: рядом
// с profiles.toml лежит хранилище секретов (secret.go).
const (
	AppDir   = app.Name
	FileName = "profiles.toml"
)

// Path возвращает путь к profiles.toml по правилам платформы:
// %APPDATA%\csqtt-cli\profiles.toml на Windows и
// $XDG_CONFIG_HOME/csqtt-cli/profiles.toml (по умолчанию ~/.config) на прочих.
// Под sudo — ~/.config/csqtt-cli того, кто вызвал sudo, а не root.
//
// os.UserConfigDir здесь не годится: на macOS он даёт ~/Library/Application
// Support, а клиент консольный, и его настройки логичнее держать там же, где
// у остальных консольных программ.
func Path() (string, error) {
	dir, err := appDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, FileName), nil
}

// appDir — каталог настроек: каталог платформы плюс AppDir. В нём лежат
// profiles.toml и хранилище секретов (secret.go), поэтому путь считается
// в одном месте.
func appDir() (string, error) {
	invoker, err := sudo.Invoker()
	if err != nil {
		return "", err
	}
	dir, err := configDir(runtime.GOOS, os.Getenv, invoker)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, AppDir), nil
}

// configDir вынесен отдельно от Path и берёт платформу с окружением
// аргументами: иначе ветку другой системы нечем было бы проверить.
//
// invoker — кто вызвал sudo (nil — не через sudo). Окружение под sudo — уже
// root'а: HOME=/root, а XDG_CONFIG_HOME, если и дожил, может быть чьим угодно.
// Поэтому берётся домашний каталог вызвавшего из базы пользователей, а его
// собственный XDG_CONFIG_HOME sudo не передаёт, и узнать его неоткуда.
func configDir(goos string, env func(string) string, invoker *sudo.User) (string, error) {
	if goos == "windows" {
		if appData := strings.TrimSpace(env("APPDATA")); appData != "" {
			return appData, nil
		}
		return "", errors.New("не задана переменная окружения APPDATA: непонятно, где искать настройки")
	}

	if invoker != nil {
		return filepath.Join(invoker.HomeDir, ".config"), nil
	}

	if xdg := strings.TrimSpace(env("XDG_CONFIG_HOME")); xdg != "" {
		return xdg, nil
	}
	if home := strings.TrimSpace(env("HOME")); home != "" {
		return filepath.Join(home, ".config"), nil
	}
	return "", errors.New("не заданы переменные окружения XDG_CONFIG_HOME и HOME: непонятно, где искать настройки")
}
