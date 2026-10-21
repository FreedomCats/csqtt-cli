//go:build !windows

package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/FreedomCats/csqtt-cli/internal/app"
)

// relaunchElevated запускает csqtt-cli через sudo в том же терминале и ждёт,
// пока он закончит: после остановки подключения человек возвращается в меню.
// Сигналы терминал отдаёт обоим процессам, и разбирает их запущенный
// csqtt-cli (menuSignals.child). Файлы и секреты профиля он берёт у того, кто
// вызвал sudo, — как sudo csqtt-cli connect (§09б).
func relaunchElevated(e *env, args []string) (detached bool, err error) {
	sudo, err := exec.LookPath("sudo")
	if err != nil {
		return false, &noElevationError{reason: fmt.Sprintf("нужны права root, а sudo не найден: "+
			"запустите %s от root", app.Name)}
	}
	exe, err := os.Executable()
	if err != nil {
		return false, fmt.Errorf("не найти исполняемый файл %s: %w", app.Name, err)
	}

	fmt.Fprintln(e.stdout, "Нужны права root: sudo спросит пароль вашей учётной записи.")
	cmd := exec.Command(sudo, append([]string{"--", exe}, args...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = e.stdin, e.stdout, e.stderr
	return false, childExit(e, cmd.Run())
}

// childExit — исход запущенного через sudo csqtt-cli. Что не так, он (или
// sudo) уже напечатал сам; здесь — только строка о том, что меню вернулось.
func childExit(e *env, err error) error {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		if err != nil {
			return fmt.Errorf("sudo не запустился: %w", err)
		}
		return nil
	}

	// Ctrl+C на пароле sudo или на вопросе запущенного меню (menu -connect):
	// человек сам вернулся сюда, это не сбой. Код 130 — так выходит
	// csqtt-cli; sudo, прерванный на пароле, может и сам умереть от SIGINT.
	status, _ := exitErr.Sys().(syscall.WaitStatus)
	if exitErr.ExitCode() == exitInterrupted || status.Signaled() && status.Signal() == syscall.SIGINT {
		fmt.Fprintln(e.stderr, "Прервано: подключения с правами root нет.")
		return nil
	}
	if status.Signaled() {
		return fmt.Errorf("sudo или подключение с правами root завершились сигналом %v", status.Signal())
	}
	// Код 1 — и неверный пароль sudo, и ошибка самого подключения: различить
	// их нельзя, причину каждый напечатал выше.
	return fmt.Errorf("sudo или подключение с правами root завершились с кодом %d — причина выше",
		exitErr.ExitCode())
}
