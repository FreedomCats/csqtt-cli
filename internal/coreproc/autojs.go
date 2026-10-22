package coreproc

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Режим auto_js (§08а doc/architecture.md): звонок VK создаёт само ядро по
// токену аккаунта. Здесь — то, что у оболочки в этом режиме своё: журнал с
// хешем созданного звонка, остановка с завершением звонка и разбор отказа до
// PROCESS.

// Строки stdin с секретами: первая — SecretsLine, вторая в auto_js — токен
// VK (main.rs, read_vk_js_bootstrap).
const (
	secretsPrefix   = "SECRETS:"
	bootstrapPrefix = "VK_JS_BOOTSTRAP:"
)

// maskStdinSecrets прячет значения строк секретов stdin, где бы они ни
// попались в журнале: значение — до пробела или конца строки.
func maskStdinSecrets(line string) string {
	for _, prefix := range []string{secretsPrefix, bootstrapPrefix} {
		line = maskAfter(line, prefix, " \t", func(n int) string {
			return "<скрыт, " + strconv.Itoa(n) + " зн.>"
		})
	}
	return line
}

// callHashMarkers — после чего в журнале ядра стоит хеш звонка: адреса
// гостевого пути (auth.rs) — ссылка как есть, закодированная в параметре link
// и голый хеш в параметре joinLink шага 5 (адрес целиком попадает в текст
// ошибки HTTP-клиента в строках «[VK JS] Переход на Авто #N не выполнен: …»
// и «[КРЕД #N] Ошибка: …»), и начало хеша в «[КРЕД #N] Запрос (хеш: <8
// знаков>...)» (worker.rs).
var callHashMarkers = []string{"call/join/", "call%2Fjoin%2F", "call%2fjoin%2f", "joinLink=", "(хеш: "}

// callHashEnd — на чём кончается хеш звонка. Сам хеш — латиница, цифры, «-»
// и «_», но кончать его по любому знаку вне этого набора опасно: лишнее
// спрятать не страшно, а недоспрятанный хеш — это ссылка на звонок.
const callHashEnd = " \t?#&/\"'<>()[]{},;|."

// maskCallHash прячет хеш звонка, созданного ядром в auto_js. Оболочке он
// неизвестен — его выдал VK, а не профиль, — поэтому найти его можно только
// по виду строки. Ссылка на звонок чувствительна: по ней в звонок войдёт
// кто угодно.
//
// В режиме с хешами профиля журнал так не правится: там хеш известен, и
// вывод csqtt-cli называет его номером в профиле.
func maskCallHash(line string) string {
	for _, marker := range callHashMarkers {
		line = maskAfter(line, marker, callHashEnd, func(int) string { return "<хеш>" })
	}
	return line
}

// maskAfter заменяет значение после каждого вхождения marker — до первого
// знака из end или конца строки — тем, что вернёт hidden по длине значения.
// Пустое значение остаётся как было.
func maskAfter(line, marker, end string, hidden func(n int) string) string {
	var b strings.Builder
	for {
		i := strings.Index(line, marker)
		if i < 0 {
			b.WriteString(line)
			return b.String()
		}
		start := i + len(marker)
		b.WriteString(line[:start])
		rest := line[start:]
		n := strings.IndexAny(rest, end)
		if n < 0 {
			n = len(rest)
		}
		if n > 0 {
			b.WriteString(hidden(n))
		}
		line = rest[n:]
	}
}

// ──────────────────────────── остановка ────────────────────────────

// Сроки остановки ядра в auto_js. Обычные StopTimeout здесь не годятся: пока
// ядро создаёт звонок (до PROCESS), команд оно не читает, а убитое ядро
// оставит звонок в аккаунте VK.
const (
	// StopProcessTimeoutAutoJS — сколько ждать PROCESS. До него ядро
	// открывает сессию VK Calls и создаёт звонок; STOP оно исполнит только
	// после.
	StopProcessTimeoutAutoJS = 30 * time.Second
	// StopCleanupTimeoutAutoJS — сколько ждать выхода после PROCESS:
	// воркеры закрываются, звонок завершается (calls.forceFinish — до 2 с)
	// или создатель из него выходит, журнал сбрасывается (до 1 с).
	StopCleanupTimeoutAutoJS = 8 * time.Second
)

// Строки журнала ядра об уборке звонка (vk_js_calls.rs). Итог — «K/N»: из
// скольких звонков сколько завершено или покинуто.
const (
	logCallCreated   = "[VK JS] Звонок создан"
	logCallsFinished = "[VK JS] Звонки завершены "
	logCreatorLeft   = "[VK JS] Создатель вышел из звонков "
)

// callState — что ядро в auto_js сказало о звонке: создан ли он и чем
// кончилась уборка.
type callState struct {
	created  bool
	finished bool // «Звонки завершены N/N»
	left     bool // «Создатель вышел из звонков N/N»
}

// observe отмечает строку журнала ядра.
func (s *callState) observe(line string) {
	switch {
	case strings.HasPrefix(line, logCallCreated):
		s.created = true
	case strings.HasPrefix(line, logCallsFinished):
		s.finished = allDone(line[len(logCallsFinished):])
	case strings.HasPrefix(line, logCreatorLeft):
		s.left = allDone(line[len(logCreatorLeft):])
	}
}

// allDone — итог «K/N» при K = N > 0.
func allDone(s string) bool {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return false
	}
	k, n, ok := strings.Cut(fields[0], "/")
	if !ok {
		return false
	}
	done, err1 := strconv.Atoi(k)
	total, err2 := strconv.Atoi(n)
	return err1 == nil && err2 == nil && total > 0 && done == total
}

// unclean — звонок создан, а уборку, которую просили, ядро не подтвердило:
// звонок не завершён (finish) или создатель из него не вышел.
func (s callState) unclean(finish bool) bool {
	if !s.created {
		return false
	}
	if finish {
		return !s.finished
	}
	return !s.left
}

// confirmed — ядро подтвердило уборку, которую просили: звонок завершён
// (finish) или создатель из него вышел.
func (s callState) confirmed(finish bool) bool {
	if finish {
		return s.finished
	}
	return s.left
}

// CallLeftError — ядро в auto_js остановлено, а уборка созданного им звонка
// не подтверждена: ядро убито (не вышло в срок или Kill) или само сказало,
// что звонок не завершило (создатель не вышел). Звонок мог остаться в
// аккаунте VK, пока VK не закроет его сам.
type CallLeftError struct {
	// Killed — ядро убито до того, как подтвердило уборку: по сроку или
	// Kill. Тогда звонок мог остаться, даже если ядро о нём ещё ничего не
	// сказало: запрос на создание мог уйти.
	Killed bool
	// Err — ошибка выхода ядра, как у Wait.
	Err error
}

func (e *CallLeftError) Error() string {
	if e.Killed {
		return "ядро убито, не закончив уборку: созданный им звонок VK мог остаться в аккаунте"
	}
	return "ядро не подтвердило, что созданный им звонок VK закрыт: звонок мог остаться в аккаунте"
}

func (e *CallLeftError) Unwrap() error { return e.Err }

// ──────────────────────────── отказ до PROCESS ────────────────────────────

// fatalPrefix — начало строки журнала, с которой ядро выходит при ошибке
// (main.rs, secrets_stdin.rs).
const fatalPrefix = "[ФАТАЛ] "

// FatalKind — что за ошибка в строке «[ФАТАЛ] …».
type FatalKind int

const (
	// FatalOther — всё прочее: ответ VK не того вида (нет нужного поля),
	// нет токена, негодный адрес API из ответа VK. Повтор с теми же
	// настройками вряд ли поможет.
	FatalOther FatalKind = iota
	// FatalNetwork — сеть до VK: запрос не ушёл или ответ не дочитан,
	// HTTP-ответ не 2xx или не JSON. Повтор может помочь.
	FatalNetwork
	// FatalAPI — VK или OK Calls ответили ошибкой API (FatalError.APICode).
	FatalAPI
	// FatalPeer — не разрешилось имя сервера CSQTT (main.rs, resolve_peer):
	// сеть или неверное имя. Ядро ещё не ходило в VK.
	FatalPeer
	// FatalClient — ядро не приняло то, что ему дала оболочка: аргументы
	// («[КЛИЕНТ] …», «[П3] …»), строки секретов или данные auto_js в stdin.
	// Похоже на ядро не из той сборки.
	FatalClient
)

// FatalError — причина выхода ядра из его строки «[ФАТАЛ] …». В auto_js до
// PROCESS ядро ходит в VK (открывает сессию аккаунта, создаёт звонок), и по
// виду ошибки решается, повторять ли попытку.
type FatalError struct {
	// Text — текст строки после «[ФАТАЛ] », как в журнале.
	Text string
	Kind FatalKind
	// Method — к какому методу ядро приписало ошибку API
	// (vchat.startConversation). Пусто — без приписки: так выглядят ошибки
	// API при открытии сессии аккаунта (messages.getCallToken VK,
	// auth.anonymLogin OK Calls) и проверке system.getInfo.
	Method string
	// APICode — код из «API <код>: …»; 0 — не ошибка API или код не число.
	APICode int
}

func (e *FatalError) Error() string { return e.Text }

// tokenRejectedCodes — коды VK, с которыми токен не годится, и поможет только
// новый вход: 5 — авторизация пользователя не удалась (токен неверный,
// просроченный или отозванный), 27 и 28 — токен сообщества или приложения
// вместо токена пользователя. Код 4 сюда не входит: у VK это неверная
// подпись запроса (токен с ней не связан), а у OK Calls — общая ошибка
// запроса; в тексте ошибки 5 VK пишет «(4)» как уточнение.
var tokenRejectedCodes = map[int]bool{5: true, 27: true, 28: true}

// TokenRejected — VK отверг токен: войти заново (`csqtt-cli vk login`), повтор
// не поможет. Только ошибка без приписки метода: её даёт первый же запрос с
// токеном, messages.getCallToken. У OK Calls таких кодов нет.
func (e *FatalError) TokenRejected() bool {
	return e.Kind == FatalAPI && e.Method == "" && tokenRejectedCodes[e.APICode]
}

// transientCodes — коды VK, за которыми ничего не стоит и которые проходят
// сами: 1 — unknown error, 6 — too many requests per second, 10 — internal
// server error. Код 9 (flood control) сюда не входит: это уже лимит на
// аккаунт, и повтор его только усугубит.
var transientCodes = map[int]bool{1: true, 6: true, 10: true}

// Transient — временная ошибка API VK: повтор через несколько секунд может
// помочь. С припиской метода или без: такой код может прийти и при открытии
// сессии, и при создании звонка.
func (e *FatalError) Transient() bool {
	return e.Kind == FatalAPI && transientCodes[e.APICode]
}

// apiError — «API <код>: » в начале текста или после приписки «<метод>: »
// (vk_js_calls.rs, check_api_error).
var apiError = regexp.MustCompile(`(?:^|: )API ([^\s:]+): `)

// httpStatus — «HTTP <код>» ответа не 2xx (vk_js_calls.rs, post_form).
var httpStatus = regexp.MustCompile(`\bHTTP [1-5][0-9][0-9]\b`)

// networkMarkers — части текста ошибок сети и HTTP ядра: запрос не ушёл,
// ответ не дочитан или не тот (vk_js_calls.rs, post_form).
var networkMarkers = []string{
	"HTTP-запрос",
	"чтение HTTP-ответа",
	"слишком большой HTTP-ответ",
	"некорректный JSON-ответ",
}

// peerMarker — начало ошибки разбора имени сервера (main.rs, resolve_peer).
const peerMarker = "ошибка разбора пира"

// clientPrefixes — начала ошибок аргументов и строки секретов (main.rs,
// listen_peer.rs, secrets_stdin.rs, bind_if.rs); clientMarkers — части
// ошибок чтения данных auto_js из stdin (main.rs, read_vk_js_bootstrap).
var (
	clientPrefixes = []string{"[КЛИЕНТ] ", "[П3] "}
	clientMarkers  = []string{"данных Auto JS", "данные Auto JS"}
)

// ParseFatal разбирает строку журнала ядра «[ФАТАЛ] …»; ok = false — строка
// не такая. Строки ядро пишет по-русски и без кодов, поэтому разбор — по
// тексту, сверенному с ядром (тест TestFatalLinesInCore).
func ParseFatal(line string) (e *FatalError, ok bool) {
	text, ok := strings.CutPrefix(line, fatalPrefix)
	if !ok {
		return nil, false
	}
	e = &FatalError{Text: text, Kind: FatalOther}
	if m := apiError.FindStringSubmatchIndex(text); m != nil {
		e.Kind = FatalAPI
		e.Method = strings.TrimSuffix(text[:m[0]], ": ")
		e.APICode, _ = strconv.Atoi(text[m[2]:m[3]])
		return e, true
	}
	has := func(marker string) bool { return strings.Contains(text, marker) }
	switch {
	case strings.HasPrefix(text, peerMarker):
		e.Kind = FatalPeer
	case slices.ContainsFunc(clientPrefixes, func(p string) bool { return strings.HasPrefix(text, p) }),
		slices.ContainsFunc(clientMarkers, has):
		e.Kind = FatalClient
	case httpStatus.MatchString(text), slices.ContainsFunc(networkMarkers, has):
		e.Kind = FatalNetwork
	}
	return e, true
}
