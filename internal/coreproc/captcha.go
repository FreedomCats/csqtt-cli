package coreproc

import (
	"errors"
	"strconv"
	"strings"
)

// CoreSolverOffEnv — переменная окружения, с которой решатель капчи ядра
// (Rust v2, captcha.rs: solve_once) не трогает сессию капчи и сразу
// отказывается. Start ставит её всегда.
//
// Решатель ядра ходит в ту же сессию VK, что потом приходит в окно
// человеку: живая запись показала, что после его попыток (галочка — BOT,
// слайдер — ошибка) VK отвечает человеку в окне ERROR_LIMIT, и капчу в
// окне уже не решить. С выключенным решателем окно получает нетронутую
// сессию. Цепочка ядра при этом та же, только его шаги отказывают сразу.
const CoreSolverOffEnv = "ONLY_PRINT_URL"

// CaptchaError — отказ решателя капчи с причиной для ядра:
// CAPTCHA_RESULT|error:<Reason>. Reason — короткое слово латиницей
// (closed, limit, unsupported): ядро пишет его в свой журнал. Err — то же
// для человека.
type CaptchaError struct {
	Reason string
	Err    error
}

func (e *CaptchaError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return "капча не решена: " + e.Reason
}

func (e *CaptchaError) Unwrap() error { return e.Err }

// ErrCaptchaNoReply — на запрос капчи ядру не отвечают: ядро его уже не ждёт
// (вышло время окна или пришёл новый запрос). Ответить всё равно нельзя:
// ядро приняло бы ответ на прежний запрос за ответ на текущий — свой канал
// результата оно чистит только перед тем, как напечатать запрос.
var ErrCaptchaNoReply = errors.New("ответ ядру не нужен")

// captchaResultPrefix — начало команды с ответом на капчу.
const captchaResultPrefix = "CAPTCHA_RESULT|"

// maskLog прячет токен капчи в строке журнала ядра: каждую принятую команду
// ядро повторяет в журнал строкой «[STDIN] CAPTCHA_RESULT|<токен>», а
// журнал идёт на экран и в файл. Отказы (error:…) остаются как есть — в них
// токена нет, а причина нужна.
func maskLog(line string) string {
	i := strings.Index(line, captchaResultPrefix)
	if i < 0 {
		return line
	}
	start := i + len(captchaResultPrefix)
	rest := line[start:]
	if strings.HasPrefix(rest, "error:") {
		return line
	}
	end := strings.IndexAny(rest, " \t")
	if end < 0 {
		end = len(rest)
	}
	if end == 0 {
		return line
	}
	return line[:start] + "<токен скрыт, " + strconv.Itoa(end) + " зн.>" + maskLog(rest[end:])
}
