package captcha

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

const manualToken = "выдуманный.success-token_0123456789"

func manualRequest() Request {
	return Request{
		Mode:         ModeManual,
		RedirectURI:  "https://id.vk.com/not_robot_captcha?domain=vk.com&session_token=выдуманная",
		SessionToken: "выдуманная",
		Deadline:     time.Now().Add(3 * time.Minute),
	}
}

// solveWith прогоняет ручной режим на подставленном вводе.
func solveWith(t *testing.T, input string) (string, string, error) {
	t.Helper()
	out := &bytes.Buffer{}
	m := NewManual(ManualOptions{In: strings.NewReader(input), Out: out})
	token, err := m.Solve(context.Background(), manualRequest())
	return token, out.String(), err
}

func TestManualAcceptsBareToken(t *testing.T) {
	got, _, err := solveWith(t, manualToken+"\n")
	if err != nil || got != manualToken {
		t.Fatalf("Solve = %q, %v", got, err)
	}
}

// Человек под таймером копирует то, что попалось: ответ VK целиком, кусок
// ответа с именем поля, значение в кавычках. Всё это принимается.
func TestManualAcceptsSloppyInput(t *testing.T) {
	cases := map[string]string{
		"с пробелами":          "   " + manualToken + "   ",
		"в кавычках":           `"` + manualToken + `"`,
		"ответ check целиком":  `{"response":{"redirect":"","show_captcha_type":"","status":"OK","success_token":"` + manualToken + `"}}`,
		"кусок JSON с полем":   `"success_token":"` + manualToken + `",`,
		"поле через двоеточие": "success_token: " + manualToken,
		"конец ввода без \\n":  manualToken,
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			sep := "\n"
			if strings.HasPrefix(name, "конец ввода") {
				sep = ""
			}
			got, _, err := solveWith(t, input+sep)
			if err != nil || got != manualToken {
				t.Errorf("Solve = %q, %v", got, err)
			}
		})
	}
}

func TestManualEmptyInput(t *testing.T) {
	for name, input := range map[string]string{
		"пустая строка": "\n",
		"конец ввода":   "",
		"одни пробелы":  "   \n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := solveWith(t, input); !errors.Is(err, ErrCancelled) {
				t.Errorf("ошибка = %v, ожидалась ErrCancelled", err)
			}
		})
	}
}

// На негодный ответ — объяснение и ещё одна попытка.
func TestManualRetriesOnGarbage(t *testing.T) {
	for name, tc := range map[string]struct{ input, explain string }{
		"короткое слово":    {"готово", "коротко"},
		"несколько слов":    {"не знаю что тут", "несколько слов"},
		"ссылка":            {"https://id.vk.com/not_robot_captcha?x=1", "адресной строке"},
		"BOT":               {`{"response":{"status":"BOT","show_captcha_type":"slider"}}`, "status=BOT"},
		"не тот ответ":      {`{"response":{"sensors_delay":200}}`, "success_token"},
		"испорченный JSON":  {`{"response":`, "не разобран"},
		"поле без значения": {`"success_token":""`, "не видно значения"},
	} {
		t.Run(name, func(t *testing.T) {
			got, out, err := solveWith(t, tc.input+"\n"+manualToken+"\n")
			if err != nil || got != manualToken {
				t.Fatalf("Solve = %q, %v", got, err)
			}
			if !strings.Contains(out, tc.explain) || !strings.Contains(out, "ещё раз") {
				t.Errorf("объяснение не то:\n%s", out)
			}
		})
	}
}

// ERROR_LIMIT в ответе — решать эту капчу дальше бесполезно.
func TestManualLimit(t *testing.T) {
	if _, _, err := solveWith(t, `{"response":{"status":"ERROR_LIMIT"}}`+"\n"); !errors.Is(err, ErrLimit) {
		t.Errorf("ошибка = %v, ожидалась ErrLimit", err)
	}
}

func TestManualPromptHasEverythingNeeded(t *testing.T) {
	req := manualRequest()
	_, text, _ := solveWith(t, "\n")
	for _, want := range []string{
		req.RedirectURI,         // куда идти
		"F12",                   // чем открыть инструменты
		"Network",               // какую вкладку
		"captchaNotRobot.check", // какой запрос
		"success_token",         // какое поле
		"3m0s",                  // сколько времени
	} {
		if !strings.Contains(text, want) {
			t.Errorf("в инструкции нет %q:\n%s", want, text)
		}
	}
}

// Открытие браузера — дело вызывающего, и его неудача не срывает решение.
func TestManualBrowserFailureIsNotFatal(t *testing.T) {
	out := &bytes.Buffer{}
	var asked string
	m := NewManual(ManualOptions{
		In:   strings.NewReader(manualToken + "\n"),
		Out:  out,
		Open: func(u string) error { asked = u; return errors.New("браузера нет") },
	})
	got, err := m.Solve(context.Background(), manualRequest())
	if err != nil || got != manualToken {
		t.Fatalf("Solve = %q, %v", got, err)
	}
	if asked != manualRequest().RedirectURI {
		t.Errorf("открыть просили %q", asked)
	}
	if !strings.Contains(out.String(), "откройте вручную") {
		t.Errorf("про неудачу браузера не сказали:\n%s", out.String())
	}
}

// Ctrl+C или конец попытки во время ожидания ответа: Solve возвращается, не
// дожидаясь Enter, а следующий Solve читает тот же ввод.
func TestManualCancelThenNext(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	m := NewManual(ManualOptions{In: r, Out: io.Discard})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := m.Solve(ctx, manualRequest())
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ошибка = %v, ожидалась context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Solve не вернулся после отмены")
	}

	got := make(chan string, 1)
	go func() {
		token, _ := m.Solve(context.Background(), manualRequest())
		got <- token
	}()
	time.Sleep(50 * time.Millisecond)
	if _, err := io.WriteString(w, manualToken+"\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case token := <-got:
		if token != manualToken {
			t.Fatalf("второй Solve = %q", token)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("второй Solve не получил ввода")
	}
}

// Строка, введённая, когда ответа никто не ждал, — ответ на прежнюю капчу:
// новой капче она не достаётся.
func TestManualDropsStaleLine(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	m := NewManual(ManualOptions{In: r, Out: io.Discard})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Solve(ctx, manualRequest()); !errors.Is(err, context.Canceled) {
		t.Fatalf("ошибка = %v", err)
	}
	if _, err := io.WriteString(w, "прежний-токен-0123456789\n"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond) // строка дошла до канала

	got := make(chan string, 1)
	go func() {
		token, _ := m.Solve(context.Background(), manualRequest())
		got <- token
	}()
	time.Sleep(50 * time.Millisecond)
	if _, err := io.WriteString(w, manualToken+"\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case token := <-got:
		if token != manualToken {
			t.Fatalf("Solve = %q, ожидался новый токен", token)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Solve не получил ввода")
	}
}

// После конца ввода следующие Solve сразу отказывают, а не ждут вечно.
func TestManualAfterEOF(t *testing.T) {
	m := NewManual(ManualOptions{In: strings.NewReader(""), Out: io.Discard})
	for range 2 {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := m.Solve(ctx, manualRequest())
		cancel()
		if !errors.Is(err, ErrCancelled) {
			t.Fatalf("ошибка = %v, ожидалась ErrCancelled", err)
		}
	}
}
