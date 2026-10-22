// vklogin.go — вход в аккаунт VK для режима auto_js (§08а doc/architecture.md):
// OAuth-страница VK в окне, токен — из адреса, на который VK вернёт окно.
//
// Окна те же, что у капчи: браузер на Chromium по CDP на Linux
// (vklogin_cdp.go, vklogin_linux.go), WebView2 на Windows
// (vklogin_windows.go). Человек сам входит на странице VK и сам подтверждает
// права приложения: окно ничего не нажимает за него, а csqtt-cli сам в VK не
// ходит. Профиль окна временный и убирается до того, как вход вернёт токен:
// cookie VK после входа не остаются.
//
// После согласия VK переводит окно на https://oauth.vk.ru/blank.html, и в
// адресе — access_token, user_id и expires_in: во фрагменте (после #), а
// бывает — в строке запроса. Окна видят этот адрес по-своему, а разбирает его
// одно место — loginFlow.
package captcha

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// VKAuthURL — OAuth-страница входа VK (implicit flow, §08а): приложение
// 7793118, права — все, кроме сообщений, вместе с offline (токен бессрочный);
// display=page — страница целиком, revoke=1 — экран с правами приложения
// показывается каждый раз.
const VKAuthURL = "https://oauth.vk.ru/authorize?client_id=7793118&scope=1073737727" +
	"&redirect_uri=https%3A%2F%2Foauth.vk.ru%2Fblank.html&display=page&response_type=token&revoke=1&v=5.199"

// LoginTimeout — сколько ждать входа: войти, пройти проверки VK и
// подтвердить права.
const LoginTimeout = 5 * time.Minute

// Куда VK возвращает окно с токеном: redirect_uri из VKAuthURL; oauth.vk.com —
// на случай, если VK уведёт на старый домен.
var (
	oauthHosts = []string{"oauth.vk.ru", "oauth.vk.com"}
	// payloadHosts — где во фрагменте бывает payload= с «тихим» токеном VK ID.
	// Только хосты VK: сторонний сайт на вход не влияет, даже повтором.
	payloadHosts = []string{"oauth.vk.ru", "oauth.vk.com"}
)

const blankPath = "/blank.html"

// silentRetryDelay — через сколько после «тихого» токена VK ID открывать
// страницу входа заново: так делает Android-клиент CSQTT.
const silentRetryDelay = 1500 * time.Millisecond

// loginStubPage — что окно показывает вместо blank.html VK, пока закрывается
// или открывает страницу входа заново (окно по CDP).
const loginStubPage = `<!doctype html><meta charset="utf-8"><title>csqtt-cli</title>` +
	`<p>Секунду…</p><p>Если окно не закроется само, закройте его и повторите вход.</p>`

// loginWindowSize — окно входа: страница VK шире капчи.
const loginWindowSize = "820,720"

// VKToken — итог входа. Печатать его нельзя: String и GoString прячут
// AccessToken, чтобы он не попал в вывод даже случайным %v.
type VKToken struct {
	AccessToken string
	// UserID — id аккаунта VK; пусто, если VK его не прислал.
	UserID string
	// ExpiresIn — срок токена в секундах; 0 — бессрочный.
	ExpiresIn int
}

func (t VKToken) String() string {
	return fmt.Sprintf("VKToken{user_id=%s, expires_in=%d, access_token=<скрыт>}", t.UserID, t.ExpiresIn)
}

// GoString — то же для %#v.
func (t VKToken) GoString() string { return t.String() }

// LoginWindow — окно входа VK.
type LoginWindow interface {
	// Login открывает окно со страницей входа и ждёт токена. Возвращается,
	// когда окно закрыто и его профиль убран.
	Login(ctx context.Context) (VKToken, error)
}

// LoginOptions — настройки окна входа.
type LoginOptions struct {
	// Out — куда уходит сообщение, что окно открыто. nil — os.Stderr.
	Out io.Writer
	// Logf — что окно видит: переходы на blank.html, повторы; без адресов и
	// токенов. nil — молчать.
	Logf func(format string, args ...any)
	// Net — диагностический режим (netfilter.go): окно ходит в сеть только
	// через локальный прокси с этим фильтром, а каждое соединение и сводка
	// в конце пишутся в Out. nil — окно ходит в сеть само.
	Net *NetFilter
}

// NewLoginWindow находит окно входа этой системы: браузер на Chromium на
// Linux, WebView2 на Windows. Окна нет — ошибка (ErrNoBrowser, отказ
// WebView2, errors.ErrUnsupported).
func NewLoginWindow(opts LoginOptions) (LoginWindow, error) {
	return platformLoginWindow(opts)
}

var (
	// ErrLoginClosed — окно входа закрыл человек.
	ErrLoginClosed = errors.New("окно входа VK закрыто до конца входа")
	// ErrLoginBrowser — окно входа не открылось: браузер не запустился.
	ErrLoginBrowser = errors.New("окно входа VK не открылось")
	// ErrLoginNoToken — VK вернул окно на blank.html, а токена в адресе нет.
	ErrLoginNoToken = errors.New("VK завершил вход, но токена в адресе нет")
	// ErrLoginExpiring — VK и со второго раза выдал токен со сроком, а
	// режиму auto_js нужен бессрочный.
	ErrLoginExpiring = errors.New("VK выдал токен с ограниченным сроком действия, а нужен бессрочный")
	// ErrLoginSilent — VK и со второго раза вернул «тихий» токен VK ID, а не
	// токен приложения.
	ErrLoginSilent = errors.New("VK вернул токен VK ID вместо токена приложения")
)

// VKAuthError — VK вернул вместо токена ошибку: человек отказал приложению
// в доступе или VK отказал сам.
type VKAuthError struct {
	Code        string // error
	Reason      string // error_reason
	Description string // error_description
}

func (e *VKAuthError) Error() string {
	text := e.Description
	if text == "" {
		text = e.Reason
	}
	if text == "" {
		return "VK отказал во входе: " + e.Code
	}
	return fmt.Sprintf("VK отказал во входе: %s (%s)", text, e.Code)
}

// loginAction — что делать окну с адресом, который оно увидело.
type loginAction int

const (
	loginWait  loginAction = iota // адрес не тот или токена в нём не видно: ждать дальше
	loginDone                     // токен получен
	loginFail                     // вход кончился ошибкой
	loginRetry                    // открыть страницу входа заново через delay
)

// loginStep — решение по одному адресу.
type loginStep struct {
	action loginAction
	token  VKToken
	err    error
	delay  time.Duration
	// known — решение по этому адресу уже принято раньше (loginWait).
	known bool
}

// loginFlow разбирает адреса, на которые приходит окно входа, и решает, что
// дальше. Сам ничего не открывает: открывают окна. Повторы — как у
// Android-клиента CSQTT: токен со сроком и «тихий» токен VK ID — по одному
// разу открыть страницу входа заново, второй раз — ошибка.
type loginFlow struct {
	logf func(string, ...any)

	// decided — адреса, по которым решение уже принято. Одну и ту же
	// навигацию окно по CDP видит несколькими событиями, а WebView2 —
	// повтором скрипта; решать по ней второй раз нельзя — повтор посчитался
	// бы дважды.
	decided       map[string]bool
	expiryRetried bool
	silentRetried bool
}

func newLoginFlow(logf func(string, ...any)) *loginFlow {
	return &loginFlow{logf: logf, decided: map[string]bool{}}
}

func (f *loginFlow) log(format string, args ...any) {
	if f.logf != nil {
		f.logf(format, args...)
	}
}

// isBlank — адрес — blank.html, куда VK возвращает окно с токеном.
func isBlank(u *url.URL) bool {
	return slices.Contains(oauthHosts, strings.ToLower(u.Hostname())) && u.Path == blankPath
}

// observe разбирает адрес окна. final — другого способа увидеть этот адрес
// у окна нет: blank.html без токена тогда — ошибка, а не ожидание.
//
// Ни адрес, ни токен в журнал не попадают: только то, что в адресе нашлось.
func (f *loginFlow) observe(raw string, final bool) loginStep {
	u, err := url.Parse(raw)
	if err != nil {
		return loginStep{}
	}
	blank := isBlank(u)
	fragment := u.EscapedFragment()
	payload := strings.HasPrefix(fragment, "payload=") &&
		slices.Contains(payloadHosts, strings.ToLower(u.Hostname()))
	if !blank && !payload {
		return loginStep{}
	}
	if f.decided[raw] {
		return loginStep{known: true}
	}

	fromFragment, _ := url.ParseQuery(fragment)

	// Токен и ошибку принимать только с blank.html — redirect_uri входа. С
	// адреса, который смотрится лишь ради payload= (другая страница oauth),
	// — нет: туда окно пришло не по redirect_uri.
	if blank {
		params, where := fromFragment, "во фрагменте"
		if params.Get("access_token") == "" && params.Get("error") == "" {
			params, where = u.Query(), "в строке запроса"
		}

		switch {
		case params.Get("error") != "":
			f.decided[raw] = true
			e := &VKAuthError{
				Code:        printable(params.Get("error")),
				Reason:      printable(params.Get("error_reason")),
				Description: printable(params.Get("error_description")),
			}
			f.log("[VK] вход: VK вернул ошибку %s", where)
			return loginStep{action: loginFail, err: e}

		case strings.TrimSpace(params.Get("access_token")) != "":
			f.decided[raw] = true
			return f.token(params, where)
		}
	}

	if payload && isSilentToken(fromFragment.Get("payload")) {
		f.decided[raw] = true
		if f.silentRetried {
			f.log("[VK] вход: снова «тихий» токен VK ID")
			return loginStep{action: loginFail, err: ErrLoginSilent}
		}
		f.silentRetried = true
		f.log("[VK] вход: «тихий» токен VK ID вместо токена приложения — страница входа откроется заново")
		return loginStep{action: loginRetry, delay: silentRetryDelay}
	}

	if blank && final {
		f.decided[raw] = true
		f.log("[VK] вход: в адресе blank.html нет ни токена, ни ошибки")
		return loginStep{action: loginFail, err: ErrLoginNoToken}
	}
	return loginStep{}
}

// isSilentToken — payload= во фрагменте несёт «тихий» токен VK ID.
func isSilentToken(payload string) bool {
	var p struct {
		Type  string `json:"type"`
		Token string `json:"token"`
	}
	return json.Unmarshal([]byte(payload), &p) == nil && p.Type == "silent_token" && p.Token != ""
}

// token — шаг по адресу с access_token.
func (f *loginFlow) token(params url.Values, where string) loginStep {
	t := VKToken{AccessToken: strings.TrimSpace(params.Get("access_token"))}

	// expires_in=0 — бессрочный токен, как и просит право offline. Без поля
	// срок не известен, и токен принимается: так делают оба настольных
	// клиента CSQTT. Срок или мусор — один повтор, потом ошибка, как у
	// Android-клиента.
	if raw := strings.TrimSpace(params.Get("expires_in")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n != 0 {
			if f.expiryRetried {
				f.log("[VK] вход: токен снова со сроком (expires_in=%q)", printable(raw))
				return loginStep{action: loginFail, err: ErrLoginExpiring}
			}
			f.expiryRetried = true
			f.log("[VK] вход: токен со сроком (expires_in=%q) — страница входа откроется заново", printable(raw))
			return loginStep{action: loginRetry}
		}
	}

	if id := strings.TrimSpace(params.Get("user_id")); isUserID(id) {
		t.UserID = id
	} else if id != "" {
		f.log("[VK] вход: user_id не число — не записывается")
	}
	f.log("[VK] вход: токен получен %s (%d зн.), user_id %s", where, len(t.AccessToken), presence(t.UserID))
	return loginStep{action: loginDone, token: t}
}

// isUserID — id аккаунта VK: только цифры.
func isUserID(s string) bool {
	if s == "" || len(s) > 20 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func presence(s string) string {
	if s == "" {
		return "нет"
	}
	return "есть"
}

// printable — текст от VK для сообщения: без управляющих знаков и не
// длиннее 200 знаков.
func printable(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.TrimSpace(s))
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200]) + "…"
	}
	return s
}

// warnProfileLeft говорит, если временный профиль окна входа убрать не
// вышло: в нём cookie сессии VK. Следующий запуск уберёт его по возрасту
// (sweepStaleProfiles), но человек должен знать, что он лежит.
func warnProfileLeft(out io.Writer, profile string) {
	if _, err := os.Stat(profile); err == nil {
		fmt.Fprintf(out, "Предупреждение: временный профиль окна входа не удалён: %s — в нём cookie VK; "+
			"удалите каталог вручную.\n", profile)
	}
}
