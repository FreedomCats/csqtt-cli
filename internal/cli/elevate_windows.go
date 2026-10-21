package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/FreedomCats/csqtt-cli/internal/app"
)

// tokenElevationTypeLimited — TOKEN_ELEVATION_TYPE: урезанный токен
// администратора при включённом UAC. Только у такого есть полный двойник,
// и окно UAC поднимет того же пользователя.
const tokenElevationTypeLimited = 3

// relaunchElevated запускает csqtt-cli с правами администратора в новом окне:
// окно UAC, затем отдельная консоль — процесс с правами к чужой консоли не
// присоединится. Меню в старом окне после этого закрывается (detached).
func relaunchElevated(e *env, args []string) (detached bool, err error) {
	// Обычная учётная запись: UAC спросил бы пароль администратора, и
	// csqtt-cli работал бы от его имени — другой %APPDATA%, а секреты DPAPI
	// другой пользователь не расшифрует.
	if elevationType() != tokenElevationTypeLimited {
		return false, &noElevationError{reason: "режиму tun — весь трафик системы — нужны права администратора, " +
			"а у этой учётной записи их нет: с паролем другой учётной записи " + app.Name +
			" запустился бы от её имени и не нашёл бы этот профиль"}
	}

	exe, err := os.Executable()
	if err != nil {
		return false, fmt.Errorf("не найти исполняемый файл %s: %w", app.Name, err)
	}
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = windows.EscapeArg(arg)
	}
	cwd, _ := os.Getwd()

	fmt.Fprintln(e.stdout, "Нужны права администратора: Windows спросит разрешение.")
	err = windows.ShellExecute(0, windows.StringToUTF16Ptr("runas"), windows.StringToUTF16Ptr(exe),
		windows.StringToUTF16Ptr(strings.Join(quoted, " ")), windows.StringToUTF16Ptr(cwd), windows.SW_SHOWNORMAL)
	if errors.Is(err, windows.ERROR_CANCELLED) {
		fmt.Fprintln(e.stderr, "Права администратора не выданы: подключения нет.")
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("запуск с правами администратора: %w", err)
	}

	fmt.Fprintln(e.stdout, "Подключение идёт в новом окне. Остановить — Ctrl+C или закрыть то окно.")
	return true, nil
}

// elevationType — TOKEN_ELEVATION_TYPE процесса; 0 — узнать не удалось.
func elevationType() uint32 {
	var t, n uint32
	err := windows.GetTokenInformation(windows.GetCurrentProcessToken(), windows.TokenElevationType,
		(*byte)(unsafe.Pointer(&t)), uint32(unsafe.Sizeof(t)), &n)
	if err != nil {
		return 0
	}
	return t
}
