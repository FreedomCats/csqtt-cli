package captcha

import (
	"fmt"
	"os"
	"testing"
)

// TestMain даёт прогону свой временный каталог (TMPDIR): профили окон — в
// нём. Проверки уборки (profilesNow, loginProfilesNow) ищут профили по
// началу имени во временном каталоге, и в общем /tmp они видели бы чужие:
// другого прогона тестов пакета или настоящего окна csqtt-cli, открытого в
// то же время, — и падали бы на «профиль не убран».
//
// Права — как у /tmp (1777): под sudo каталог принадлежит root, а браузер
// работает от имени вызвавшего пользователя, получает этот TMPDIR
// (sudo.SessionEnv) и создаёт в нём свои файлы — например, сокет
// ProcessSingleton; без права записи он выходит сразу.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "captcha-tests-")
	if err == nil {
		err = os.Chmod(dir, 0o777|os.ModeSticky)
	}
	if err == nil {
		err = os.Setenv("TMPDIR", dir)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "временный каталог тестов: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
