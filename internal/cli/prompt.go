// prompt.go — ввод, который нельзя брать из аргументов командной строки:
// секреты и подтверждение опасного действия.
//
// Строки stdin читает один общий читатель (env.input, lines.Reader): его же
// получает ручной режим капчи, и брошенное капчей чтение не уносит строку,
// введённую в ответ на вопрос CLI. Скрытый ввод читает терминал мимо него —
// у терминала надо погасить эхо, — и поэтому спрашивается до подключения,
// пока капчи нет и общий читатель stdin не читает.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/FreedomCats/csqtt-cli/internal/lines"
)

// errNoInput — ввод кончился, не начавшись: команду запустили без терминала
// и без данных на stdin. Отдельная ошибка, чтобы вызывающий мог объяснить,
// чего он ждал.
var errNoInput = errors.New("ввод пуст")

// askSecret спрашивает значение, не показывая его на экране.
//
// Приглашение уходит в stderr: stdout остаётся чистым для того, что у команды
// является результатом. Если stdin не терминал (значение подают из конвейера
// или из теста), эха отключать нечего — читается обычная строка. Это не
// нарушает правила «секрет не приходит аргументом»: в историю оболочки и
// в список процессов конвейер не попадает.
func (e *env) askSecret(ctx context.Context, prompt string) (string, error) {
	if e.secrets != nil {
		return e.secrets(prompt)
	}

	fmt.Fprint(e.stderr, prompt)

	if fd, ok := e.terminalFD(); ok {
		return readHidden(ctx, fd, e.stderr)
	}
	// Эха нет и из конвейера: перевод строки за приглашением ставим сами,
	// иначе следующая строка вывода приклеилась бы к нему.
	line, err := e.readLine(ctx)
	fmt.Fprintln(e.stderr)
	return line, err
}

// readHidden читает строку с терминала без эха. Ctrl+C во время ввода
// отменяет ctx (supervisor.Interrupt), а сам ReadPassword его не слышит: тогда
// режим терминала возвращается здесь, иначе эхо осталось бы выключенным и
// после выхода. Брошенное чтение доживает до выхода процесса — команда на
// этом кончается.
func readHidden(ctx context.Context, fd int, out io.Writer) (string, error) {
	state, _ := term.GetState(fd)
	type result struct {
		raw []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		raw, err := term.ReadPassword(fd)
		done <- result{raw, err}
	}()

	select {
	case r := <-done:
		fmt.Fprintln(out)
		if r.err != nil {
			return "", fmt.Errorf("скрытый ввод: %w", r.err)
		}
		return strings.TrimSpace(string(r.raw)), nil
	case <-ctx.Done():
		if state != nil {
			_ = term.Restore(fd, state)
		}
		fmt.Fprintln(out)
		return "", ctx.Err()
	}
}

// askNewSecret спрашивает новое значение секрета.
//
// Пустое значение не записывается: пустой пароль ядро всё равно не пропустит,
// а в хранилище осталась бы бесполезная запись. Если спрашивают человека,
// значение просят повторить — опечатку в невидимом вводе иначе не заметить,
// а обнаружится она невнятной ошибкой при подключении. Из конвейера значение
// читается один раз: повторять там нечего.
func (e *env) askNewSecret(ctx context.Context, prompt, again, what string, confirm bool) (string, error) {
	value, err := e.askSecret(ctx, prompt)
	if errors.Is(err, errNoInput) {
		return "", fmt.Errorf("%s: ввод пуст", what)
	}
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", fmt.Errorf("%s: пустое значение не записывается", what)
	}

	if !confirm || !e.interactive() {
		return value, nil
	}

	repeat, err := e.askSecret(ctx, again)
	if err != nil && !errors.Is(err, errNoInput) {
		return "", err
	}
	if repeat != value {
		return "", fmt.Errorf("%s: введённые значения не совпали", what)
	}
	return value, nil
}

// interactive — спрашиваем ли человека. Подставленный ввод (тесты) считается
// человеком: повторный вопрос — часть поведения, и проверять его надо.
func (e *env) interactive() bool {
	if e.secrets != nil {
		return true
	}
	_, ok := e.terminalFD()
	return ok
}

// confirm спрашивает согласие на необратимое действие. Умолчание — «нет»:
// пустой ввод и конец ввода означают отказ.
func (e *env) confirm(ctx context.Context, question string) (bool, error) {
	fmt.Fprintf(e.stderr, "%s [y/N] ", question)

	line, err := e.readLine(ctx)
	if errors.Is(err, errNoInput) {
		fmt.Fprintln(e.stderr)
		return false, nil
	}
	if err != nil {
		return false, err
	}

	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes", "д", "да":
		return true, nil
	default:
		return false, nil
	}
}

// lines — общий читатель строк stdin. Заводится один раз: у команды бывает
// несколько вопросов подряд и ещё капча, а второй читатель того же stdin
// уносил бы с собой то, что успел прочитать.
func (e *env) lines() *lines.Reader {
	if e.input == nil {
		e.input = lines.New(e.stdin)
	}
	return e.input
}

// readLine читает одну строку со stdin. Строку, введённую, пока ответа никто
// не ждал (брошенное чтение ручной капчи), выбрасывает: это ответ не на этот
// вопрос.
func (e *env) readLine(ctx context.Context) (string, error) {
	in := e.lines()
	in.DropStale()
	line, err := in.Read(ctx)
	switch {
	case errors.Is(err, io.EOF):
		return "", errNoInput
	case err != nil && ctx.Err() != nil:
		return "", ctx.Err()
	case err != nil:
		return "", fmt.Errorf("чтение ввода: %w", err)
	}
	return strings.TrimSpace(line), nil
}

// terminalFD отдаёт дескриптор stdin, если это терминал. Только у настоящего
// файла есть дескриптор, а у терминала — режим, в котором можно погасить эхо.
func (e *env) terminalFD() (int, bool) {
	return fileTerminal(e.stdin)
}

// stdoutTerminal говорит, терминал ли stdout, и отдаёт функцию, которая
// возвращает его ширину в знаках (ноль — узнать не удалось). Спрашивать
// ширину каждый раз, а не один, нужно потому, что окно растягивают по ходу
// работы. Для строки состояния `csqtt-cli connect` (output.go).
func (e *env) stdoutTerminal() (width func() int, ok bool) {
	fd, ok := fileTerminal(e.stdout)
	if !ok {
		return nil, false
	}
	return func() int {
		w, _, err := term.GetSize(fd)
		if err != nil {
			return 0
		}
		return w
	}, true
}

func fileTerminal(stream any) (int, bool) {
	f, ok := stream.(*os.File)
	if !ok {
		return 0, false
	}
	fd := int(f.Fd())
	return fd, term.IsTerminal(fd)
}
