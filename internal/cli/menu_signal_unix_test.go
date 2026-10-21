//go:build !windows

package cli

import (
	"context"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"
)

// menuSession — меню в горутине с настоящими сигналами и поддельным ядром:
// stdin — канал, в который тест пишет ответы по ходу.
func menuSession(t *testing.T) (in *os.File, out, errOut *syncBuffer, done chan int) {
	t.Helper()
	// Тест сам подписан на сигналы, чтобы без обработчика меню они не
	// убили процесс тестов, а дали понятный провал.
	guard := make(chan os.Signal, 1)
	signal.Notify(guard, os.Interrupt, syscall.SIGHUP)
	t.Cleanup(func() { signal.Stop(guard) })

	te := newMenuEnv(t)
	socksHome(t, te)
	te.core = "connected"
	t.Setenv(fakeCoreEnv, te.core)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	te.stdin = r

	out, errOut = &syncBuffer{}, &syncBuffer{}
	e := te.env(out, errOut)
	e.signals = true
	done = make(chan int, 1)
	go func() { done <- run(context.Background(), e, []string{"menu", "-config", te.configPath()}) }()

	if _, err := w.WriteString("\n"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, out, errOut, "туннель работает")
	return w, out, errOut, done
}

func waitExit(t *testing.T, done chan int, out, errOut *syncBuffer) int {
	t.Helper()
	select {
	case code := <-done:
		return code
	case <-time.After(10 * time.Second):
		t.Fatalf("меню не закрылось:\n%s\nstderr:\n%s", out.String(), errOut.String())
		return 0
	}
}

// Ctrl+C во время подключения из меню снимает туннель (ядро получает STOP)
// и возвращает в меню, а не закрывает его.
func TestMenuInterruptReturnsToMenu(t *testing.T) {
	in, out, errOut, done := menuSession(t)

	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	waitFor(t, out, errOut, "остановлено")
	deadline := time.Now().Add(10 * time.Second)
	for strings.Count(out.String(), "1. Подключиться") < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("меню не вернулось:\n%s\nstderr:\n%s", out.String(), errOut.String())
		}
		time.Sleep(5 * time.Millisecond)
	}

	if _, err := in.WriteString("0\n"); err != nil {
		t.Fatal(err)
	}
	if code := waitExit(t, done, out, errOut); code != exitOK {
		t.Errorf("код %d, stderr:\n%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "До свидания") {
		t.Errorf("меню закрылось не по выбору:\n%s", out.String())
	}
}

// Закрыли терминал (SIGHUP) во время подключения: туннель снимается, и меню
// закрывается, больше ничего не спрашивая.
func TestMenuHangupClosesMenu(t *testing.T) {
	_, out, errOut, done := menuSession(t)

	if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	if code := waitExit(t, done, out, errOut); code != exitOK {
		t.Errorf("код %d, stderr:\n%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "остановлено") {
		t.Errorf("туннель не снят по правилам:\n%s", out.String())
	}
	if n := strings.Count(out.String(), "1. Подключиться"); n != 1 {
		t.Errorf("меню показано %d раз, ожидался один:\n%s", n, out.String())
	}
}

// Пока идёт подключение, перезапущенное с правами (sudo), настоящий Ctrl+C
// достаётся и меню: оно его не разбирает — подключение остановит запущенный
// csqtt-cli, а меню после этого покажется снова. SIGHUP (закрыли терминал)
// закрывает меню, когда подключение кончится. Сломанный разбор закрыл бы весь
// тестовый процесс с кодом 130 — это тоже провал.
func TestMenuSignalsWhileElevatedChild(t *testing.T) {
	for _, c := range []struct {
		sig syscall.Signal
		// menus — сколько раз показано меню.
		menus int
	}{
		{sig: syscall.SIGINT, menus: 2},
		{sig: syscall.SIGHUP, menus: 1},
	} {
		t.Run(c.sig.String(), func(t *testing.T) {
			guard := make(chan os.Signal, 1)
			signal.Notify(guard, os.Interrupt, syscall.SIGHUP)
			t.Cleanup(func() { signal.Stop(guard) })

			tunSupport(t, nil)
			runAsRoot(t, false)
			te := newMenuEnv(t)
			te.addHome()
			relaunched := make(chan struct{})
			te.elevate = func([]string) (bool, error) {
				// Сигнал приходит, пока «дочерний» идёт; меню успевает его
				// получить до конца перезапуска.
				if err := syscall.Kill(os.Getpid(), c.sig); err != nil {
					t.Error(err)
				}
				time.Sleep(300 * time.Millisecond)
				close(relaunched)
				return false, nil
			}
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { r.Close(); w.Close() })
			te.stdin = r

			out, errOut := &syncBuffer{}, &syncBuffer{}
			e := te.env(out, errOut)
			e.signals = true
			done := make(chan int, 1)
			go func() { done <- run(context.Background(), e, []string{"menu", "-config", te.configPath()}) }()

			if _, err := w.WriteString("\n"); err != nil {
				t.Fatal(err)
			}
			select {
			case <-relaunched:
			case <-time.After(10 * time.Second):
				t.Fatalf("перезапуска не было:\n%s\nstderr:\n%s", out.String(), errOut.String())
			}
			if c.menus == 2 {
				deadline := time.Now().Add(10 * time.Second)
				for strings.Count(out.String(), "1. Подключиться") < 2 {
					if time.Now().After(deadline) {
						t.Fatalf("меню не вернулось:\n%s\nstderr:\n%s", out.String(), errOut.String())
					}
					time.Sleep(5 * time.Millisecond)
				}
				if _, err := w.WriteString("0\n"); err != nil {
					t.Fatal(err)
				}
			}
			if code := waitExit(t, done, out, errOut); code != exitOK {
				t.Errorf("код %d, stderr:\n%s", code, errOut.String())
			}
			if n := strings.Count(out.String(), "1. Подключиться"); n != c.menus {
				t.Errorf("меню показано %d раз, ожидалось %d:\n%s", n, c.menus, out.String())
			}
		})
	}
}
