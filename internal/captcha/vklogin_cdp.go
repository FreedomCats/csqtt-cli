// vklogin_cdp.go — окно входа VK по протоколу DevTools: автомат разговора с
// браузером (cdpLogin). Как и у капчи, здесь только протокол, без ОС; трубу
// ведёт converse (cdp.go), браузер запускает vklogin_linux.go.
//
// Адрес blank.html с токеном окно ловит тремя путями — какой из них покажет
// фрагмент адреса, живьём ещё не проверено (§08а), поэтому слушаются все:
//
//  1. Fetch.requestPaused — запрос окна к blank.html перехвачен до сети:
//     адрес в request.url, фрагмент — отдельно в request.urlFragment. Ответ
//     на него — своя заглушка (Fetch.fulfillRequest), к VK запрос не уходит,
//     как у Android-клиента CSQTT;
//  2. Page.frameNavigated главного фрейма (frame.url и frame.urlFragment) и
//     Page.navigatedWithinDocument (смена фрагмента на той же странице);
//  3. не видно фрагмента ни там, ни там — Runtime.evaluate location.href на
//     открывшейся странице blank.html (это наша заглушка, страницы VK там уже
//     нет). Это последнее слово: токена нет и тут — ошибка.
//
// На страницах VK окно ничего не выполняет и ничего не нажимает.
package captcha

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// loginCallKind — зачем отправлена команда окна входа.
type loginCallKind int

const (
	loginCallTargets  loginCallKind = iota // Target.getTargets: найти вкладку
	loginCallCreate                        // Target.createTarget: вкладки не нашлось
	loginCallAttach                        // Target.attachToTarget: сессия вкладки
	loginCallSetup                         // Page.enable, Fetch.enable: отказ — входа не будет
	loginCallNavigate                      // Page.navigate на страницу входа
	loginCallMinor                         // заглушка, пропуск запроса: отказ не важен
	loginCallLocation                      // Runtime.evaluate location.href на blank.html
)

type loginPending struct {
	kind   loginCallKind
	method string
}

// cdpLogin — автомат окна входа VK.
//
// Порядок: вкладка, с которой браузер запустился (как у капчи); в её сессии
// Page.enable и Fetch.enable с перехватом blank.html, и только когда браузер
// ответил на обе, — Page.navigate на страницу входа: иначе редирект на
// blank.html мог бы проскочить до перехвата. Дальше — ждать адреса с токеном.
type cdpLogin struct {
	url  string
	logf func(string, ...any)
	flow *loginFlow

	nextID  int64
	pending map[int64]loginPending

	target    string // вкладка; её id — и id главного фрейма
	session   string
	setupLeft int
	navigated bool    // Page.navigate отправлена: окно открыто
	result    VKToken // токен, когда разговор кончен удачно
}

func newCDPLogin(authURL string, logf func(string, ...any)) *cdpLogin {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &cdpLogin{url: authURL, logf: logf, flow: newLoginFlow(logf), pending: map[int64]loginPending{}}
}

func (c *cdpLogin) opened() bool { return c.navigated }

func (c *cdpLogin) start() []cdpCommand {
	return []cdpCommand{c.call(loginCallTargets, "", "Target.getTargets", nil)}
}

func (c *cdpLogin) call(kind loginCallKind, session, method string, params any) cdpCommand {
	c.nextID++
	c.pending[c.nextID] = loginPending{kind: kind, method: method}
	return cdpCommand{ID: c.nextID, Method: method, Params: params, SessionID: session}
}

func (c *cdpLogin) handle(m cdpMessage) ([]cdpCommand, *outcome) {
	if m.ID != 0 {
		p, ok := c.pending[m.ID]
		if !ok {
			return nil, nil
		}
		delete(c.pending, m.ID)
		return c.reply(p, m)
	}
	return c.event(m)
}

// blankPatterns — перехват Fetch: только документы blank.html, всё прочее
// идёт мимо, не останавливаясь.
func blankPatterns() []map[string]any {
	var patterns []map[string]any
	for _, host := range oauthHosts {
		patterns = append(patterns, map[string]any{
			"urlPattern": "https://" + host + blankPath + "*", "resourceType": "Document", "requestStage": "Request"})
	}
	return patterns
}

func (c *cdpLogin) reply(p loginPending, m cdpMessage) ([]cdpCommand, *outcome) {
	if m.Error != nil {
		switch p.kind {
		case loginCallMinor:
			c.logf("[VK] окно входа: %s: %v", p.method, m.Error)
			return nil, nil
		case loginCallLocation:
			// Страница сменилась, пока читали адрес, — значит, окно уже
			// ушло дальше (повтор входа), и решать нечего.
			c.logf("[VK] окно входа: адрес blank.html не прочитать: %v", m.Error)
			return nil, nil
		default:
			return nil, &outcome{err: fmt.Errorf("%w: %s: %v", ErrLoginBrowser, p.method, m.Error)}
		}
	}

	switch p.kind {
	case loginCallTargets:
		var r struct {
			TargetInfos []struct {
				TargetID string `json:"targetId"`
				Type     string `json:"type"`
			} `json:"targetInfos"`
		}
		_ = json.Unmarshal(m.Result, &r)
		for _, t := range r.TargetInfos {
			if t.Type == "page" {
				return []cdpCommand{c.attach(t.TargetID)}, nil
			}
		}
		return []cdpCommand{c.call(loginCallCreate, "", "Target.createTarget", map[string]any{"url": "about:blank"})}, nil

	case loginCallCreate:
		var r struct {
			TargetID string `json:"targetId"`
		}
		if json.Unmarshal(m.Result, &r) != nil || r.TargetID == "" {
			return nil, &outcome{err: fmt.Errorf("%w: вкладка не открылась", ErrLoginBrowser)}
		}
		return []cdpCommand{c.attach(r.TargetID)}, nil

	case loginCallAttach:
		var r struct {
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(m.Result, &r) != nil || r.SessionID == "" {
			return nil, &outcome{err: fmt.Errorf("%w: к вкладке не подключиться", ErrLoginBrowser)}
		}
		c.session = r.SessionID
		c.setupLeft = 2
		return []cdpCommand{
			c.call(loginCallSetup, c.session, "Page.enable", map[string]any{}),
			c.call(loginCallSetup, c.session, "Fetch.enable", map[string]any{"patterns": blankPatterns()}),
		}, nil

	case loginCallSetup:
		c.setupLeft--
		if c.setupLeft > 0 {
			return nil, nil
		}
		c.navigated = true
		return []cdpCommand{c.navigate(0)}, nil

	case loginCallNavigate:
		var r struct {
			ErrorText string `json:"errorText"`
		}
		_ = json.Unmarshal(m.Result, &r)
		switch {
		case r.ErrorText == "":
		case strings.Contains(r.ErrorText, "ERR_ABORTED"):
			// Переход перебит другим — редиректом страницы или нашим же
			// повтором. Страница в окне есть, ждём дальше.
			c.logf("[VK] окно входа: переход прерван: %s", r.ErrorText)
		default:
			return nil, &outcome{err: fmt.Errorf("страница входа VK не загрузилась: %s", r.ErrorText)}
		}
		return nil, nil

	case loginCallLocation:
		var r struct {
			Result struct {
				Value string `json:"value"`
			} `json:"result"`
		}
		_ = json.Unmarshal(m.Result, &r)
		c.logf("[VK] окно входа: адрес blank.html прочитан со страницы")
		return c.decide(c.flow.observe(r.Result.Value, true))
	}
	return nil, nil
}

func (c *cdpLogin) attach(target string) cdpCommand {
	c.target = target
	return c.call(loginCallAttach, "", "Target.attachToTarget", map[string]any{"targetId": target, "flatten": true})
}

// navigate — открыть страницу входа, через after.
func (c *cdpLogin) navigate(after time.Duration) cdpCommand {
	cmd := c.call(loginCallNavigate, c.session, "Page.navigate", map[string]any{"url": c.url})
	cmd.after = after
	return cmd
}

func (c *cdpLogin) event(m cdpMessage) ([]cdpCommand, *outcome) {
	switch m.Method {
	case "Fetch.requestPaused":
		if m.SessionID != c.session {
			return nil, nil
		}
		var p struct {
			RequestID string `json:"requestId"`
			FrameID   string `json:"frameId"`
			Request   struct {
				URL         string `json:"url"`
				URLFragment string `json:"urlFragment"`
			} `json:"request"`
		}
		if json.Unmarshal(m.Params, &p) != nil || p.RequestID == "" {
			return nil, nil
		}
		if p.FrameID != c.target {
			// blank.html во фрейме — не наш редирект: пусть идёт как шёл.
			return []cdpCommand{c.call(loginCallMinor, c.session, "Fetch.continueRequest",
				map[string]any{"requestId": p.RequestID})}, nil
		}
		c.logf("[VK] окно входа: переход на blank.html перехвачен, фрагмент в запросе %s", seen(p.Request.URLFragment))
		stub := c.call(loginCallMinor, c.session, "Fetch.fulfillRequest", map[string]any{
			"requestId":    p.RequestID,
			"responseCode": 200,
			"responseHeaders": []map[string]string{
				{"name": "Content-Type", "value": "text/html; charset=utf-8"},
				{"name": "Cache-Control", "value": "no-store"},
			},
			"body": base64.StdEncoding.EncodeToString([]byte(loginStubPage)),
		})
		cmds, out := c.decide(c.flow.observe(p.Request.URL+p.Request.URLFragment, false))
		return append([]cdpCommand{stub}, cmds...), out

	case "Page.frameNavigated":
		if m.SessionID != c.session {
			return nil, nil
		}
		var p struct {
			Frame struct {
				ID          string `json:"id"`
				ParentID    string `json:"parentId"`
				URL         string `json:"url"`
				URLFragment string `json:"urlFragment"`
			} `json:"frame"`
		}
		if json.Unmarshal(m.Params, &p) != nil || p.Frame.ParentID != "" {
			return nil, nil
		}
		step := c.flow.observe(p.Frame.URL+p.Frame.URLFragment, false)
		if step.action != loginWait || step.known {
			return c.decide(step)
		}
		if u, err := url.Parse(p.Frame.URL); err == nil && isBlank(u) {
			// Последний способ: адрес со страницы. Если по этой навигации
			// решение уже есть, observe его не повторит.
			c.logf("[VK] окно входа: открыт blank.html, фрагмент в событии %s", seen(p.Frame.URLFragment))
			return []cdpCommand{c.call(loginCallLocation, c.session, "Runtime.evaluate",
				map[string]any{"expression": "location.href", "returnByValue": true})}, nil
		}
		return nil, nil

	case "Page.navigatedWithinDocument":
		if m.SessionID != c.session {
			return nil, nil
		}
		var p struct {
			FrameID string `json:"frameId"`
			URL     string `json:"url"`
		}
		if json.Unmarshal(m.Params, &p) != nil || p.FrameID != c.target {
			return nil, nil
		}
		return c.decide(c.flow.observe(p.URL, false))

	case "Target.detachedFromTarget":
		var p struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(m.Params, &p)
		if c.session != "" && p.SessionID == c.session {
			return nil, &outcome{err: errLoginTabClosed}
		}

	case "Target.targetCrashed":
		var p struct {
			TargetID string `json:"targetId"`
		}
		_ = json.Unmarshal(m.Params, &p)
		if p.TargetID == c.target {
			return nil, &outcome{err: errors.New("вкладка окна входа VK упала")}
		}

	case "Inspector.detached":
		if m.SessionID == c.session {
			return nil, &outcome{err: errLoginTabClosed}
		}
	}
	return nil, nil
}

// errLoginTabClosed — вкладку окна входа закрыли. Для converse это
// ErrWindowClosed: после страницы входа окно закрыл человек, до неё —
// браузер пропал. Текст свой: до страницы он попадает в ErrLoginBrowser, а
// о капче окну входа говорить нечего.
var errLoginTabClosed error = loginTabClosed{}

type loginTabClosed struct{}

func (loginTabClosed) Error() string        { return "вкладка закрыта" }
func (loginTabClosed) Is(target error) bool { return target == ErrWindowClosed }

// loginOutcome переводит исход converse окна входа на его язык: браузер
// пропал до страницы входа — ErrLoginBrowser, окно закрыто — ErrLoginClosed.
func loginOutcome(err error) error {
	var lost *browserLost
	switch {
	case errors.As(err, &lost):
		return fmt.Errorf("%w: %v", ErrLoginBrowser, lost.cause)
	case errors.Is(err, ErrWindowClosed):
		return ErrLoginClosed
	}
	return err
}

// decide превращает решение loginFlow в команды или исход.
func (c *cdpLogin) decide(step loginStep) ([]cdpCommand, *outcome) {
	switch step.action {
	case loginDone:
		c.result = step.token
		return nil, &outcome{token: step.token.AccessToken}
	case loginFail:
		return nil, &outcome{err: step.err}
	case loginRetry:
		return []cdpCommand{c.navigate(step.delay)}, nil
	}
	return nil, nil
}

// seen — нашлось ли значение: для журнала, без самого значения.
func seen(s string) string {
	if s == "" {
		return "не виден"
	}
	return "виден"
}
