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
// сессию.
//
// Это страховка (правка П7): оболочка запускает ядро в режиме
// CaptchaModeWebView, где решатель не вызывается вовсе. Если ядро запустят
// в режиме auto, его цепочка останется той же, только шаги решателя будут
// отказывать сразу.
const CoreSolverOffEnv = "ONLY_PRINT_URL"

// CaptchaModeWebView — режим капчи ядра (-captcha-mode), с которым оболочка
// его запускает: на капчу ядро сразу печатает CAPTCHA_SOLVE|selected и ждёт
// ответа окна оболочки 180 секунд (правка П5), не трогая страницу капчи
// само. В режиме по умолчанию (auto) окно человеку открывалось бы только
// после пустых шагов цепочки ядра — через 5–9 секунд.
//
// Решатель ядра в этом режиме не вызывается вовсе; CoreSolverOffEnv
// остаётся страховкой на случай, если апстрим это изменит.
const CaptchaModeWebView = "wv"

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

// maskLog прячет в строке журнала ядра то, что живёт минуты, но даёт доступ
// к капче: токен решённой капчи и токен её сессии.
//
// Каждую принятую команду ядро повторяет в журнал строкой «[STDIN]
// CAPTCHA_RESULT|<токен>», а адрес капчи со session_token печатает строкой
// «[КАПЧА] solveOnce URL: …»; журнал идёт на экран и в файл. Отказы (error:…)
// остаются как есть — в них токена нет, а причина нужна.
//
// Заодно прячутся строки секретов stdin — SECRETS: и VK_JS_BOOTSTRAP: (с
// токеном VK): ядро их не повторяет, если получило как надо, но строку,
// попавшую читателю команд, оно повторило бы в журнал целиком, — и
// временные ключи гостевого пути VK Calls в адресах запросов (maskGuestKeys).
func maskLog(line string) string {
	return maskGuestKeys(maskStdinSecrets(maskSessionToken(maskCaptchaResult(line))))
}

func maskCaptchaResult(line string) string {
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
	return line[:start] + "<токен скрыт, " + strconv.Itoa(end) + " зн.>" + maskCaptchaResult(rest[end:])
}

// sessionTokenParam — параметр адреса капчи VK с токеном её сессии.
const sessionTokenParam = "session_token="

// maskSessionToken прячет значение session_token в адресе капчи — до конца
// параметра (&, #) или слова.
func maskSessionToken(line string) string {
	i := strings.Index(line, sessionTokenParam)
	if i < 0 {
		return line
	}
	start := i + len(sessionTokenParam)
	rest := line[start:]
	end := strings.IndexAny(rest, "&# \t")
	if end < 0 {
		end = len(rest)
	}
	if end == 0 {
		return line[:start] + maskSessionToken(rest)
	}
	return line[:start] + "<скрыт, " + strconv.Itoa(end) + " зн.>" + maskSessionToken(rest[end:])
}

// guestKeyParams — параметры адресов гостевого пути VK Calls (auth.rs,
// vk_calls_path) с временными ключами: anonymous_token и secret — шаги 2–3
// (api.vk.me), anonymToken и session_key — шаг 5 (calls.okcdn.ru/fb.do). С
// ними, пока они живы, можно войти в звонок. Адрес запроса целиком попадает
// в текст ошибки HTTP-клиента («… for url (<адрес>)»), а с ним — в строки
// журнала «[VK JS] Переход на Авто #N не выполнен: …» и «[КРЕД #N] Ошибка: …»
// в любом режиме. Путь legacy и запросы auto_js (vk_js_calls.rs) шлют ключи
// в теле запроса, а не в адресе.
var guestKeyParams = []string{"anonymous_token", "secret", "anonymToken", "session_key"}

// guestKeyEnd — на чём кончается значение параметра в адресе: следующий
// параметр, фрагмент, конец слова или скобка вокруг адреса в тексте ошибки.
const guestKeyEnd = "&# \t)"

// maskGuestKeys прячет значения guestKeyParams в адресах. Параметр ищется
// только в форме параметра адреса — после «?» или «&», со знаком «=»:
// «secret (32 chars)» в строках шагов и client_secret не задеваются.
func maskGuestKeys(line string) string {
	for _, param := range guestKeyParams {
		for _, sep := range []string{"?", "&"} {
			line = maskAfter(line, sep+param+"=", guestKeyEnd, func(int) string { return "<скрыто>" })
		}
	}
	return line
}
