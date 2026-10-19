package datapath

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/FreedomCats/csqtt-cli/internal/app"
)

// tunHelperEnv включает режим помощника: тестовый бинарь, запущенный самим
// тестом, ведёт себя как «второй csqtt-cli» и сообщает итог кодом выхода.
// Значения: lock — занять блокировку адаптера; open:<имя> — открыть адаптер
// с этим именем (Tun.Open).
const tunHelperEnv = "CSQTT_TUN_HELPER"

// tunLockEnv — имя блокировки адаптера, которое тест передаёт помощнику.
const tunLockEnv = "CSQTT_TUN_LOCK"

// helperRefused — код выхода помощника, которому отказали.
const helperRefused = 3

// TestMain уводит тесты на свою блокировку адаптера: настоящую
// (@csqtt-cli-tun) держит работающий csqtt-cli, и тесты падали бы ложно, а
// заняв её, мешали бы ему. Помощник берёт имя у теста.
func TestMain(m *testing.M) {
	tunLockName = "@" + app.Name + "-test-tun-" + strconv.Itoa(os.Getpid())
	if name, ok := os.LookupEnv(tunLockEnv); ok {
		tunLockName = name
	}
	if mode, ok := os.LookupEnv(tunHelperEnv); ok {
		os.Exit(runTunHelper(mode))
	}
	os.Exit(m.Run())
}

func runTunHelper(mode string) int {
	var err error
	switch {
	case mode == "lock":
		var release func()
		if release, err = acquireTunLock(); err == nil {
			release()
		}
	case strings.HasPrefix(mode, "open:"):
		w := NewTun(TunOptions{Name: strings.TrimPrefix(mode, "open:")})
		err = w.Open()
		w.Close()
	default:
		os.Stderr.WriteString("неизвестный режим помощника: " + mode)
		return 2
	}
	if err != nil {
		os.Stderr.WriteString(err.Error())
		return helperRefused
	}
	return 0
}

// fromOtherProcess запускает помощника в режиме mode: nil, если ему не
// отказали, иначе текст отказа.
func fromOtherProcess(t *testing.T, mode string) error {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), tunHelperEnv+"="+mode, tunLockEnv+"="+tunLockName)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == helperRefused {
		return errors.New(string(out))
	}
	if err != nil {
		t.Fatalf("помощник: %v\n%s", err, out)
	}
	return nil
}

// Второй csqtt-cli с datapath tun получает отказ, пока жив первый, и
// проходит, когда первый отпустил блокировку. Внутри одного процесса
// захваты считаются: резерв сеанса и открытие адаптера друг другу не
// мешают. Имя блокировки — тестовое (TestMain).
func TestTunLockExcludesOtherProcess(t *testing.T) {
	if tunLockName == "@"+app.Name+"-tun" {
		t.Fatal("тест взял бы блокировку настоящего csqtt-cli")
	}
	session, err := acquireTunLock()
	if err != nil {
		t.Fatalf("захват на сеанс: %v", err)
	}
	adapter, err := acquireTunLock()
	if err != nil {
		session()
		t.Fatalf("повторный захват в том же процессе: %v", err)
	}

	if err := fromOtherProcess(t, "lock"); err == nil {
		t.Error("другой процесс занял блокировку при живом первом")
	} else if !strings.Contains(err.Error(), "-datapath socks") {
		t.Errorf("отказ без совета про socks: %v", err)
	}

	// Адаптер закрыт, сеанс держит: другому процессу по-прежнему нельзя.
	adapter()
	adapter() // повторный release — не второй декремент
	if err := fromOtherProcess(t, "lock"); err == nil {
		t.Error("блокировка отпущена вместе с адаптером, хотя сеанс её держит")
	}

	session()
	if err := fromOtherProcess(t, "lock"); err != nil {
		t.Errorf("после освобождения другой процесс не смог занять блокировку: %v", err)
	}
}
