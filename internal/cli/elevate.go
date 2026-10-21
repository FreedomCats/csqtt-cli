// elevate.go — перезапуск csqtt-cli с правами из меню.
//
// Режиму tun — весь трафик системы — нужны права root (на Windows —
// администратора), а человек, открывший меню, их не давал. Меню не отправляет
// его набирать sudo csqtt-cli, а запрашивает права само: на Linux — через sudo
// в том же терминале, на Windows — окном UAC. Перезапускается не всё меню, а
// одно подключение — csqtt-cli menu -connect <профиль>: оно, как и меню,
// спросит новые ссылки, когда звонки закроются.
package cli

import (
	"path/filepath"
	"strings"
)

// noElevationError — прав взять неоткуда: на Windows у учётной записи нет прав
// администратора (UAC запустил бы csqtt-cli от имени другого пользователя, у
// которого нет ни профиля, ни ключа к его секретам), на Linux нет sudo. Меню
// тогда предлагает socks (menu.tunRefused).
type noElevationError struct{ reason string }

func (e *noElevationError) Error() string { return e.reason }

// relaunchArgs — аргументы перезапуска: подключение к профилю, с тем же файлом
// настроек. Путь делается абсолютным: у процесса с правами может быть другой
// текущий каталог (на Windows — System32). Имя — одним аргументом с «=»:
// профиль по имени -h отдельным аргументом стал бы просьбой о помощи.
func relaunchArgs(name, configPath string) []string {
	args := []string{"menu"}
	if configPath = strings.TrimSpace(configPath); configPath != "" {
		if abs, err := filepath.Abs(configPath); err == nil {
			configPath = abs
		}
		args = append(args, "-config", configPath)
	}
	return append(args, "-connect="+name)
}

// relaunch — перезапуск с правами: подмена из env или настоящий. detached —
// подключение ушло в своё окно (Windows), меню здесь больше не нужно.
func (e *env) relaunch(args []string) (detached bool, err error) {
	if e.elevate != nil {
		return e.elevate(args)
	}
	return relaunchElevated(e, args)
}
