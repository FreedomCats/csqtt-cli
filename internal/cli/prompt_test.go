package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/FreedomCats/csqtt-cli/internal/captcha"
)

func captchaRequest() captcha.Request {
	return captcha.Request{
		Mode:         captcha.ModeManual,
		RedirectURI:  "https://id.vk.ru/not_robot_captcha?session_token=выдуманная",
		SessionToken: "выдуманная",
		Deadline:     time.Now().Add(time.Minute),
	}
}

// Ручная капча и вопросы CLI читают один stdin через общий читатель: капча,
// брошенная по концу попытки, не уносит строку, которую человек ввёл потом в
// ответ на вопрос CLI.
func TestCaptchaAndPromptShareStdin(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	e := &env{stdin: pr, stderr: io.Discard}
	manual := captcha.NewManual(captcha.ManualOptions{Lines: e.lines(), Out: io.Discard})

	ctx, cancel := context.WithCancel(context.Background())
	solved := make(chan error, 1)
	go func() {
		_, err := manual.Solve(ctx, captchaRequest())
		solved <- err
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := <-solved; !errors.Is(err, context.Canceled) {
		t.Fatalf("Solve = %v", err)
	}

	go func() {
		time.Sleep(50 * time.Millisecond)
		_, _ = io.WriteString(pw, "да\n")
	}()
	ok, err := e.confirm(context.Background(), "Удалить?")
	if err != nil || !ok {
		t.Fatalf("confirm = %v, %v: строку забрала брошенная капча", ok, err)
	}
}

// Строка, введённая, когда её никто не ждал, — не ответ на новый вопрос:
// и вопрос CLI, и следующая капча её выбрасывают.
func TestStaleLineDropped(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	e := &env{stdin: pr, stderr: io.Discard}
	manual := captcha.NewManual(captcha.ManualOptions{Lines: e.lines(), Out: io.Discard})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := manual.Solve(ctx, captchaRequest()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Solve = %v", err)
	}
	if _, err := io.WriteString(pw, "да\n"); err != nil { // нажато, пока ничего не спрашивали
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)

	go func() {
		time.Sleep(50 * time.Millisecond)
		_, _ = io.WriteString(pw, "нет\n")
	}()
	ok, err := e.confirm(context.Background(), "Удалить?")
	if err != nil || ok {
		t.Fatalf("confirm = %v, %v: принят ответ, данный до вопроса", ok, err)
	}
}

// Ctrl+C во время вопроса — отмена, а не вечное ожидание.
func TestReadLineCancel(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	e := &env{stdin: pr, stderr: io.Discard}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	if _, err := e.readLine(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("readLine = %v", err)
	}
}

// Конец ввода на подтверждении — «нет».
func TestConfirmEOFIsNo(t *testing.T) {
	var errOut bytes.Buffer
	e := &env{stdin: strings.NewReader(""), stderr: &errOut}
	if ok, err := e.confirm(context.Background(), "Удалить?"); ok || err != nil {
		t.Fatalf("confirm = %v, %v", ok, err)
	}
	if !strings.Contains(errOut.String(), "[y/N]") {
		t.Errorf("вопрос: %q", errOut.String())
	}
}
