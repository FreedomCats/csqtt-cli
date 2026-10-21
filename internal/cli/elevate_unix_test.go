//go:build !windows

package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeSudo кладёт в пустой каталог скрипт sudo и делает этот каталог
// единственным в PATH: настоящий sudo тест не найдёт. Скрипт записывает свои
// аргументы в файл и выполняет action.
func fakeSudo(t *testing.T, action string) (argsFile string) {
	t.Helper()
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args")
	// Внутри скрипта PATH прежний: sleep и прочее — настоящие.
	script := "#!/bin/sh\nPATH='" + os.Getenv("PATH") + "'\nprintf '%s\\n' \"$@\" > '" + argsFile + "'\n" +
		action + "\n"
	if err := os.WriteFile(filepath.Join(dir, "sudo"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return argsFile
}

// Перезапуск через sudo: ждёт его конца; код 130 и смерть sudo от SIGINT —
// Ctrl+C, не сбой; прочие коды — ошибка со строкой; без sudo — прав взять
// неоткуда.
func TestRelaunchElevatedSudo(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name   string
		action string
		// fails — ожидаемая ошибка; пусто — без ошибки.
		fails string
		// line — строка в stderr.
		line string
	}{
		{name: "остановлено", action: "exit 0"},
		{name: "Ctrl+C на вопросе", action: "exit 130", line: "Прервано: подключения с правами root нет."},
		{name: "Ctrl+C на пароле", action: "kill -INT $$", line: "Прервано: подключения с правами root нет."},
		{name: "неверный пароль", action: "exit 1", fails: "завершились с кодом 1 — причина выше"},
		{name: "убит", action: "kill -KILL $$", fails: "завершились сигналом killed"},
	} {
		t.Run(c.name, func(t *testing.T) {
			argsFile := fakeSudo(t, "sleep 0.2; "+c.action)
			te := newTestEnv(t)
			var out, errOut syncBuffer
			e := te.env(&out, &errOut)

			start := time.Now()
			detached, err := relaunchElevated(e, relaunchArgs("home", "p.toml"))
			if time.Since(start) < 200*time.Millisecond {
				t.Error("sudo не дождались")
			}
			if detached {
				t.Error("detached на Linux")
			}
			switch {
			case c.fails == "" && err != nil:
				t.Errorf("ошибка: %v", err)
			case c.fails != "" && (err == nil || !strings.Contains(err.Error(), c.fails)):
				t.Errorf("ошибка %v, ожидалось %q", err, c.fails)
			}
			if !strings.Contains(errOut.String(), c.line) {
				t.Errorf("нет строки %q:\n%s", c.line, errOut.String())
			}
			if !strings.Contains(out.String(), "sudo спросит пароль") {
				t.Errorf("не сказано о пароле:\n%s", out.String())
			}

			raw, err := os.ReadFile(argsFile)
			if err != nil {
				t.Fatal(err)
			}
			abs, _ := filepath.Abs("p.toml")
			want := strings.Join([]string{"--", exe, "menu", "-config", abs, "-connect=home"}, "\n") + "\n"
			if string(raw) != want {
				t.Errorf("аргументы sudo:\n%s\nожидалось:\n%s", raw, want)
			}
		})
	}

	t.Run("нет sudo", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		te := newTestEnv(t)
		var out, errOut syncBuffer
		_, err := relaunchElevated(te.env(&out, &errOut), []string{"menu"})
		var no *noElevationError
		if !errors.As(err, &no) || !strings.Contains(err.Error(), "sudo не найден") {
			t.Errorf("ошибка: %v", err)
		}
	})
}

// Меню без -config перезапускает подключение с тем файлом, который читает
// само: под sudo XDG_CONFIG_HOME пропал бы, и csqtt-cli взял бы ~/.config.
func TestMenuRelaunchKeepsDefaultConfig(t *testing.T) {
	if isElevated() {
		t.Skip("под root каталог настроек — у вызвавшего sudo")
	}
	tunSupport(t, nil)
	runAsRoot(t, false)
	te := newMenuEnv(t)
	xdg := filepath.Join(te.dir, "xdg")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	te.answer(testPassword, testPassword, testHashA)
	var out, errOut syncBuffer
	if code := run(context.Background(), te.env(&out, &errOut),
		[]string{"profile", "add", "home", "-peer", "203.0.113.10"}); code != exitOK {
		t.Fatalf("profile add: код %d, stderr:\n%s", code, errOut.String())
	}

	var got []string
	te.elevate = func(args []string) (bool, error) {
		got = args
		return false, nil
	}
	te.input("\n0\n")
	if code := run(context.Background(), te.env(&out, &errOut), []string{"menu"}); code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, errOut.String())
	}
	want := []string{"menu", "-config", filepath.Join(xdg, "csqtt-cli", "profiles.toml"), "-connect=home"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("перезапуск с %q, ожидалось %q", got, want)
	}
}
