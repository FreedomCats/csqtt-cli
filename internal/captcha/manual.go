// manual.go — ручной режим: тот, который работает всегда.
//
// Окна у него нет и браузером он не управляет — только печатает ссылку и
// принимает ответ. Взамен он не требует ни WebView2 Runtime, ни Chromium и
// работает везде. Окна опираются на него как на запасное дно: не открылось
// окно — Bridge решает ту же капчу здесь.
//
// Почему человека приходится просить заглянуть в инструменты разработчика,
// а не просто скопировать ссылку: редиректа с токеном у VK нет вовсе, токен
// приходит только в теле ответа captchaNotRobot.check (page.go).
package captcha

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/FreedomCats/csqtt-cli/internal/lines"
)

// minTokenLen — короче этого success_token не бывает. Проверка нарочно грубая:
// формат токена VK не описан нигде и может измениться, а вот отличить его
// от «ок», «да» и случайно вставленного слова она успевает. Настоящий токен —
// больше тысячи знаков.
const minTokenLen = 16

// ErrCancelled — человек отказался решать капчу: пустой ответ или конец ввода.
var ErrCancelled = errors.New("капча не решена: ответа нет")

// ManualOptions — настройки ручного режима.
type ManualOptions struct {
	// Lines — откуда читается ответ: общий читатель строк процесса. nil —
	// свой читатель os.Stdin.
	//
	// Общий, а не свой: чтение со stdin не прервать, и брошенное чтение
	// прежней капчи забрало бы строку, которую человек ввёл в ответ на вопрос
	// CLI (lines.Reader).
	Lines *lines.Reader

	// Out — куда уходят приглашение и инструкция. nil — os.Stderr: stdout у
	// CLI занят результатом команды, и `csqtt-cli connect > log.txt` не должен
	// прятать вопрос от человека.
	Out io.Writer

	// Open — открыть ссылку в браузере. nil — только напечатать её.
	//
	// Полем, а не встроенным вызовом: запускать посторонние программы — решение
	// CLI, а не библиотеки, и тестам это не нужно. Готовая реализация рядом —
	// OpenInBrowser.
	Open func(rawURL string) error
}

// Manual — ручной режим.
type Manual struct {
	opts ManualOptions
}

// NewManual собирает ручной режим. Ввод и вывод не трогаются до Solve.
func NewManual(opts ManualOptions) *Manual {
	if opts.Lines == nil {
		opts.Lines = lines.New(os.Stdin)
	}
	if opts.Out == nil {
		opts.Out = os.Stderr
	}
	return &Manual{opts: opts}
}

// Solve печатает инструкцию и ждёт ответа, пока его не дадут или не отменят
// ctx. На негодный ответ объясняет, что не так, и ждёт следующего.
func (m *Manual) Solve(ctx context.Context, req Request) (string, error) {
	// Строка, введённая, пока ответа никто не ждал, — ответ на прежнюю
	// капчу, время которой вышло: ядру он уже не годится.
	m.opts.Lines.DropStale()
	m.prompt(req)
	for {
		answer, err := m.readLine(ctx)
		if err != nil {
			return "", err
		}
		token, err := parseAnswer(answer)
		switch {
		case err == nil:
			return token, nil
		case errors.Is(err, ErrCancelled), errors.Is(err, ErrLimit):
			return "", err
		}
		fmt.Fprintf(m.opts.Out, "  %v\n  Вставьте токен ещё раз (пустая строка — отказаться): ", err)
	}
}

// prompt печатает всё, что нужно человеку, одним куском: второй раз смотреть
// в экран ему будет некогда.
func (m *Manual) prompt(req Request) {
	out := m.opts.Out
	left := req.Left(time.Now()).Round(time.Second)

	fmt.Fprintf(out, "\nVK просит решить капчу. Время на это — %s.\n\n", left)
	fmt.Fprintln(out, "  1. Откройте в браузере ссылку (лучше в окне инкогнито):")
	fmt.Fprintf(out, "     %s\n", req.RedirectURI)
	if m.opts.Open != nil {
		if err := m.opts.Open(req.RedirectURI); err != nil {
			fmt.Fprintf(out, "     (открыть браузер не вышло: %v — откройте вручную)\n", err)
		} else {
			fmt.Fprintln(out, "     (уже открыта в браузере)")
		}
	}
	// Вкладка Network пишет запросы, только пока открыта: открыть её после
	// галочки — значит не увидеть ответа.
	fmt.Fprintln(out, "  2. Ещё до галочки нажмите F12 и откройте вкладку Network (Сеть).")
	fmt.Fprintln(out, "  3. Решите капчу на странице.")
	fmt.Fprintln(out, "  4. В списке запросов найдите captchaNotRobot.check (поможет фильтр check),")
	fmt.Fprintln(out, "     откройте его вкладку Response (Ответ) и скопируйте значение success_token —")
	fmt.Fprintln(out, "     или весь ответ целиком: правой кнопкой по запросу → Copy → Copy response.")
	fmt.Fprint(out, "  5. Вставьте сюда и нажмите Enter (пустая строка — отказаться): ")
}

// readLine ждёт строку ввода или отмены ctx. Пустая строка и конец ввода —
// отказ.
func (m *Manual) readLine(ctx context.Context) (string, error) {
	line, err := m.opts.Lines.Read(ctx)
	switch {
	case err != nil && ctx.Err() != nil:
		fmt.Fprintln(m.opts.Out)
		return "", ctx.Err()
	case errors.Is(err, io.EOF):
		return "", ErrCancelled
	case err != nil:
		return "", fmt.Errorf("чтение ответа: %w", err)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return "", ErrCancelled
	}
	return line, nil
}

// parseAnswer разбирает то, что вставил человек.
//
// Принимается не только голый токен: человек под таймером копирует то, что
// попалось под руку, и отвергать целый ответ VK из-за фигурных скобок было бы
// мелочно. Разбор идёт от более определённого к менее.
func parseAnswer(answer string) (string, error) {
	answer = strings.TrimSpace(answer)
	switch {
	case answer == "":
		return "", ErrCancelled

	case strings.HasPrefix(answer, "{"):
		// Скопировали ответ VK целиком.
		r, ok := parseReply([]byte(answer))
		if !ok {
			return "", errors.New("это не ответ VK: JSON не разобран, скопируйте его целиком")
		}
		if out := checkOutcome(r); out != nil {
			return out.token, out.err
		}
		if r.Response != nil && r.Response.Status != "" {
			return "", fmt.Errorf("в ответе нет токена (status=%s): решите капчу на странице до конца "+
				"и скопируйте следующий ответ captchaNotRobot.check", r.Response.Status)
		}
		return "", errors.New("в ответе нет success_token: нужен ответ на captchaNotRobot.check")

	case looksLikeURL(answer):
		// Самая вероятная ошибка: токена в адресе нет ни у одной ссылки VK.
		return "", errors.New("это ссылка, а не токен: токена в адресной строке не бывает, " +
			"он только в ответе на запрос captchaNotRobot.check")

	case strings.Contains(answer, "success_token"):
		// Скопировали кусок ответа вместе с именем поля.
		if token := afterFieldName(answer); token != "" {
			return token, nil
		}
		return "", errors.New("после success_token не видно значения")

	case strings.ContainsAny(answer, " \t"):
		return "", errors.New("в ответе несколько слов: нужен один токен без пробелов")

	case len(answer) < minTokenLen:
		return "", fmt.Errorf("для токена это слишком коротко (%d знаков): нужно значение success_token",
			len(answer))

	default:
		return strings.Trim(answer, `"'`), nil
	}
}

// afterFieldName достаёт значение, стоящее за именем поля, из обрывка вида
// `"success_token":"…"` или `success_token: …`.
func afterFieldName(answer string) string {
	_, tail, ok := strings.Cut(answer, "success_token")
	if !ok {
		return ""
	}
	tail = strings.TrimLeft(tail, `"': =`)
	if end := strings.IndexAny(tail, `"',} `); end >= 0 {
		tail = tail[:end]
	}
	if len(tail) < minTokenLen {
		return ""
	}
	return tail
}

// looksLikeURL — вставили ссылку, а не токен.
func looksLikeURL(answer string) bool {
	lower := strings.ToLower(answer)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return false
	}
	_, err := url.Parse(answer)
	return err == nil
}
