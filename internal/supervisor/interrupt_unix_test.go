//go:build !windows

package supervisor

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

// Закрыли терминал — SIGHUP: connect останавливается с уборкой, как по
// Ctrl+C. Тест сам тоже подписан на SIGHUP, чтобы без обработчика Interrupt
// сигнал не убил процесс тестов, а дал понятный провал.
func TestInterruptOnHangup(t *testing.T) {
	guard := make(chan os.Signal, 1)
	signal.Notify(guard, syscall.SIGHUP)
	defer signal.Stop(guard)

	ctx, stop := Interrupt(context.Background())
	defer stop()
	if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("SIGHUP не остановил контекст")
	}
}

// Запуск под nohup: SIGHUP игнорируется, и Interrupt его не трогает —
// подключение переживает закрытый терминал, как до подписки на SIGHUP.
func TestInterruptKeepsIgnoredHangup(t *testing.T) {
	signal.Ignore(syscall.SIGHUP)
	defer signal.Reset(syscall.SIGHUP)

	ctx, stop := Interrupt(context.Background())
	defer stop()
	if !signal.Ignored(syscall.SIGHUP) {
		t.Fatal("Interrupt снял игнорирование SIGHUP")
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
		t.Fatal("SIGHUP под nohup остановил контекст")
	case <-time.After(200 * time.Millisecond):
	}
}

// Второй Ctrl+C, пока туннель останавливается (OnAbort), не убивает процесс,
// а прерывает остановку. Тест сам подписан на SIGINT: сломанный разбор дал
// бы провал, а не смерть процесса тестов.
func TestInterruptSecondSignalAborts(t *testing.T) {
	guard := make(chan os.Signal, 4)
	signal.Notify(guard, os.Interrupt)
	defer signal.Stop(guard)

	ctx, stop := Interrupt(context.Background())
	defer stop()
	aborted := make(chan struct{})
	release := OnAbort(ctx, func() { close(aborted) })
	defer release()

	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("первый Ctrl+C не остановил контекст")
	}
	select {
	case <-aborted:
		t.Fatal("первый Ctrl+C прервал остановку")
	default:
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case <-aborted:
	case <-time.After(2 * time.Second):
		t.Fatal("второй Ctrl+C не прервал остановку")
	}
}
