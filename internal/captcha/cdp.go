// cdp.go — разговор с браузером по протоколу DevTools (CDP): открыть страницу
// капчи и снять токен из ответа VK, не трогая саму страницу.
//
// Транспорт — труба (--remote-debugging-pipe): браузер читает команды из
// дескриптора 3 и пишет ответы в дескриптор 4, сообщения — JSON, каждое
// заканчивается нулевым байтом. Ни порта, ни WebSocket: подключиться к такому
// браузеру чужой процесс не может. Запуск браузера — browser_linux.go; здесь
// только протокол, без ОС, поэтому и тесты его идут на записанных сообщениях
// где угодно.
//
// Ответы API страницы снимаются на сетевом уровне, а не перехватчиком JS, как
// в окне WebView2: Network.responseReceived для captchaNotRobot.* →
// Network.loadingFinished → Network.getResponseBody → pageWatch (page.go).
// Страница при этом не изменена ни на байт.
package captcha

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// ErrBrowser — браузер не запустился или не открыл страницу капчи. Человек
// капчи ещё не видел, и CLI по этой ошибке решает ту же капчу в ручном режиме.
var ErrBrowser = errors.New("браузер для капчи не запустился")

// cdpMessage — сообщение браузера: ответ на команду (ID) или событие (Method).
type cdpMessage struct {
	ID        int64           `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *cdpError       `json:"error,omitempty"`
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *cdpError) Error() string { return fmt.Sprintf("%s (%d)", e.Message, e.Code) }

// cdpCommand — команда браузеру. SessionID пуст — команда самому браузеру,
// иначе — вкладке или фрейму, к которому подключена сессия.
type cdpCommand struct {
	ID        int64  `json:"id"`
	Method    string `json:"method"`
	Params    any    `json:"params,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
}

// callKind — зачем отправлена команда: от этого зависит, что делать с ответом.
type callKind int

const (
	callTargets  callKind = iota // Target.getTargets: найти вкладку браузера
	callCreate                   // Target.createTarget: вкладки не нашлось
	callAttach                   // Target.attachToTarget: сессия вкладки
	callSetup                    // подготовка вкладки: отказ — страницы не будет
	callNavigate                 // Page.navigate на страницу капчи
	callChild                    // подготовка фрейма или воркера: отказ не важен
	callRelease                  // Runtime.runIfWaitingForDebugger: отказ не важен
	callBody                     // Network.getResponseBody ответа VK
)

type pendingCall struct {
	kind    callKind
	method  string
	session string     // для callChild: сессия фрейма или воркера
	key     requestKey // для callBody
	api     string     // для callBody: метод API капчи
}

// requestKey — запрос в сессии: requestId уникальны только внутри неё.
type requestKey struct {
	session string
	request string
}

// cdpCapture — автомат разговора с браузером. Ничего не читает и не пишет
// сам: получает сообщения браузера и отвечает командами, которые ему
// отправить, — или исходом, когда разговор кончен.
//
// Порядок:
//
//  1. Target.getTargets — вкладка, с которой браузер запустился (about:blank);
//     нет её — Target.createTarget. Своя вторая вкладка рядом с пустой
//     человека бы только путала.
//  2. Target.attachToTarget с flatten — сессия вкладки, команды ей идут по той
//     же трубе с её sessionId.
//  3. В сессии: Network.enable и Target.setAutoAttach (фреймы на других
//     сайтах и воркеры живут в своих процессах, и их запросы вкладке не
//     видны), и только когда браузер ответил на обе, — Page.navigate. Ждать
//     ответов нужно: Page.navigate браузер выполняет сам, а Network.enable
//     доходит до процесса страницы позже и ответ на него приходит после
//     ответа на Page.navigate. Отправленные разом, они давали гонку: первые
//     запросы страницы — initSession — уходили, пока Network в ней ещё не
//     включён, и их ответа не было видно или браузер не отдавал его тела.
//     Так же фрейм и выделенный воркер отпускаются, только когда браузер
//     ответил на их подготовку. Service worker и прочие так ждать нельзя: на
//     Network.enable придержанный service worker отвечает, только когда его
//     отпустят, — их отпускаем сразу, вслед за подготовкой, как раньше.
//  4. Читать ответы API капчи в любой из сессий, пока check не даст исход.
type cdpCapture struct {
	url   string
	logf  func(string, ...any)
	watch pageWatch

	nextID  int64
	pending map[int64]pendingCall

	target    string                // вкладка капчи
	session   string                // её сессия
	setupLeft int                   // команд подготовки вкладки без ответа
	ready     bool                  // страница капчи открывается
	children  map[string]int        // фреймы и воркеры: команд подготовки без ответа
	watched   map[requestKey]string // ответы API капчи, тело которых ждём: метод
}

func newCDPCapture(url string, logf func(string, ...any)) *cdpCapture {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &cdpCapture{url: url, logf: logf, watch: pageWatch{logf: logf}, pending: map[int64]pendingCall{},
		children: map[string]int{}, watched: map[requestKey]string{}}
}

// start — первые команды разговора.
func (c *cdpCapture) start() []cdpCommand {
	return []cdpCommand{c.call(pendingCall{kind: callTargets}, "", "Target.getTargets", nil)}
}

func (c *cdpCapture) call(p pendingCall, session, method string, params any) cdpCommand {
	c.nextID++
	p.method = method
	c.pending[c.nextID] = p
	return cdpCommand{ID: c.nextID, Method: method, Params: params, SessionID: session}
}

// handle разбирает сообщение браузера. Исход не nil — разговор кончен.
func (c *cdpCapture) handle(m cdpMessage) ([]cdpCommand, *outcome) {
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

// reply — ответ на нашу команду.
func (c *cdpCapture) reply(p pendingCall, m cdpMessage) ([]cdpCommand, *outcome) {
	// Подготовка фрейма или воркера кончилась, удачно или нет, — его
	// отпускают в любом случае: иначе он так и висел бы.
	if p.kind == callChild {
		return c.childReady(p.session), nil
	}
	if m.Error != nil {
		switch p.kind {
		case callRelease:
			return nil, nil
		case callBody:
			c.logf("[CAPTCHA] браузер не отдал ответ VK: %v", m.Error)
			return nil, nil
		default:
			return nil, &outcome{err: fmt.Errorf("%w: %s: %v", ErrBrowser, p.method, m.Error)}
		}
	}

	switch p.kind {
	case callTargets:
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
		return []cdpCommand{c.call(pendingCall{kind: callCreate}, "", "Target.createTarget",
			map[string]any{"url": "about:blank"})}, nil

	case callCreate:
		var r struct {
			TargetID string `json:"targetId"`
		}
		if json.Unmarshal(m.Result, &r) != nil || r.TargetID == "" {
			return nil, &outcome{err: fmt.Errorf("%w: вкладка не открылась", ErrBrowser)}
		}
		return []cdpCommand{c.attach(r.TargetID)}, nil

	case callAttach:
		var r struct {
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(m.Result, &r) != nil || r.SessionID == "" {
			return nil, &outcome{err: fmt.Errorf("%w: к вкладке не подключиться", ErrBrowser)}
		}
		c.session = r.SessionID
		s := c.session
		c.setupLeft = 2
		return []cdpCommand{
			c.call(pendingCall{kind: callSetup}, s, "Network.enable", map[string]any{}),
			c.call(pendingCall{kind: callSetup}, s, "Target.setAutoAttach", autoAttach),
		}, nil

	case callSetup:
		c.setupLeft--
		if c.setupLeft > 0 {
			return nil, nil
		}
		return []cdpCommand{c.call(pendingCall{kind: callNavigate}, c.session, "Page.navigate",
			map[string]any{"url": c.url})}, nil

	case callNavigate:
		var r struct {
			ErrorText string `json:"errorText"`
		}
		_ = json.Unmarshal(m.Result, &r)
		if r.ErrorText != "" {
			// Страница не загрузилась — сеть или прокси. Человеку в окне
			// показали бы страницу ошибки, и он ждал бы впустую.
			return nil, &outcome{err: fmt.Errorf("страница капчи не загрузилась: %s", r.ErrorText)}
		}
		c.ready = true
		return nil, nil

	case callBody:
		return nil, c.body(p.api, m.Result)
	}
	return nil, nil
}

// autoAttach — подключаться ко всем фреймам и воркерам вкладки, придерживая
// их до того, как в них включён Network: иначе первый запрос мог бы уйти
// незамеченным.
var autoAttach = map[string]any{"autoAttach": true, "waitForDebuggerOnStart": true, "flatten": true}

// childReady — ответ на одну команду подготовки фрейма или воркера. Ответы
// пришли на все — его можно отпустить.
func (c *cdpCapture) childReady(session string) []cdpCommand {
	left, ok := c.children[session]
	if !ok {
		return nil
	}
	if left > 1 {
		c.children[session] = left - 1
		return nil
	}
	delete(c.children, session)
	return []cdpCommand{c.call(pendingCall{kind: callRelease}, session, "Runtime.runIfWaitingForDebugger", nil)}
}

func (c *cdpCapture) attach(target string) cdpCommand {
	c.target = target
	return c.call(pendingCall{kind: callAttach}, "", "Target.attachToTarget",
		map[string]any{"targetId": target, "flatten": true})
}

// event — событие браузера.
func (c *cdpCapture) event(m cdpMessage) ([]cdpCommand, *outcome) {
	switch m.Method {
	case "Target.attachedToTarget":
		// Подключение к самой вкладке приходит браузеру без сессии; здесь —
		// только дочерние: фреймы и воркеры вкладки.
		if m.SessionID == "" {
			return nil, nil
		}
		var p struct {
			SessionID  string `json:"sessionId"`
			TargetInfo struct {
				Type string `json:"type"`
			} `json:"targetInfo"`
		}
		if json.Unmarshal(m.Params, &p) != nil || p.SessionID == "" {
			return nil, nil
		}
		s := p.SessionID
		child := pendingCall{kind: callChild, session: s}
		cmds := []cdpCommand{c.call(child, s, "Network.enable", map[string]any{})}
		switch p.TargetInfo.Type {
		case "iframe":
			cmds = append(cmds, c.call(child, s, "Target.setAutoAttach", autoAttach))
			fallthrough
		case "worker":
			// Отпускается, когда на всё это придут ответы (childReady).
			c.children[s] = len(cmds)
		default:
			// Service worker не ответит, пока его не отпустят (шаг 3 у
			// cdpCapture): отпускаем сразу.
			cmds = append(cmds, c.call(pendingCall{kind: callRelease}, s, "Runtime.runIfWaitingForDebugger", nil))
		}
		return cmds, nil

	case "Network.responseReceived":
		var p struct {
			RequestID string `json:"requestId"`
			Type      string `json:"type"`
			Response  struct {
				URL string `json:"url"`
			} `json:"response"`
		}
		if json.Unmarshal(m.Params, &p) != nil {
			return nil, nil
		}
		// Preflight CORS идёт по тому же адресу, но тела у него нет.
		if api, ok := apiMethod(p.Response.URL); ok && p.Type != "Preflight" {
			c.watched[requestKey{m.SessionID, p.RequestID}] = api
		}
		return nil, nil

	case "Network.loadingFinished":
		key := requestKey{m.SessionID, requestID(m.Params)}
		api, ok := c.watched[key]
		if !ok {
			return nil, nil
		}
		delete(c.watched, key)
		return []cdpCommand{c.call(pendingCall{kind: callBody, key: key, api: api}, key.session,
			"Network.getResponseBody", map[string]any{"requestId": key.request})}, nil

	case "Network.loadingFailed":
		delete(c.watched, requestKey{m.SessionID, requestID(m.Params)})
		return nil, nil

	case "Target.detachedFromTarget":
		var p struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(m.Params, &p)
		if c.session != "" && p.SessionID == c.session {
			return nil, &outcome{err: ErrWindowClosed}
		}
		return nil, nil

	case "Target.targetCrashed":
		var p struct {
			TargetID string `json:"targetId"`
		}
		_ = json.Unmarshal(m.Params, &p)
		if p.TargetID == c.target {
			return nil, &outcome{err: errors.New("вкладка капчи упала")}
		}
		return nil, nil

	case "Inspector.detached":
		if m.SessionID == c.session {
			return nil, &outcome{err: ErrWindowClosed}
		}
	}
	return nil, nil
}

// body разбирает тело ответа API капчи (pageWatch). Исход — только токен или
// ERROR_LIMIT: после BOT человек решает дальше в том же окне.
func (c *cdpCapture) body(api string, result json.RawMessage) *outcome {
	var r struct {
		Body          string `json:"body"`
		Base64Encoded bool   `json:"base64Encoded"`
	}
	if json.Unmarshal(result, &r) != nil {
		return nil
	}
	raw := []byte(r.Body)
	if r.Base64Encoded {
		decoded, err := base64.StdEncoding.DecodeString(r.Body)
		if err != nil {
			return nil
		}
		raw = decoded
	}

	return c.watch.observe(api, raw)
}

func requestID(params json.RawMessage) string {
	var p struct {
		RequestID string `json:"requestId"`
	}
	_ = json.Unmarshal(params, &p)
	return p.RequestID
}

// captureToken ведёт разговор по трубе: r — то, что пишет браузер, w — то, что
// он читает. Возвращается с токеном, ErrLimit, ErrWindowClosed, ErrBrowser
// или ошибкой ctx.
//
// startup — сколько ждать, пока браузер начнёт открывать страницу капчи.
// Не уложился — ErrBrowser: окна человек, скорее всего, так и не увидел.
func captureToken(ctx context.Context, r io.Reader, w io.Writer, url string, startup time.Duration,
	logf func(string, ...any)) (string, error) {

	c := newCDPCapture(url, logf)

	msgs := make(chan cdpMessage)
	readErr := make(chan error, 1)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		br := bufio.NewReader(r)
		for {
			raw, err := br.ReadBytes(0)
			if err != nil {
				readErr <- err
				return
			}
			var m cdpMessage
			if json.Unmarshal(raw[:len(raw)-1], &m) != nil {
				continue
			}
			select {
			case msgs <- m:
			case <-stop:
				return
			}
		}
	}()

	// Пишет в трубу своя горутина, а цикл ниже только кладёт команды в
	// очередь: иначе цикл, застряв на записи в полную трубу, перестал бы
	// читать ответы, а браузер, застряв на записи ответов, — команды.
	writes := make(chan cdpCommand)
	writeErr := make(chan error, 1)
	go func() {
		for {
			select {
			case cmd := <-writes:
				data, err := json.Marshal(cmd)
				if err == nil {
					_, err = w.Write(append(data, 0))
				}
				if err != nil {
					writeErr <- err
					return
				}
			case <-stop:
				return
			}
		}
	}()
	queue := c.start()

	// gone — браузер пропал: до страницы капчи это сбой запуска, после — окно
	// закрыл человек (Chromium выходит с последним окном).
	gone := func(cause error) error {
		if !c.ready {
			return fmt.Errorf("%w: %v", ErrBrowser, cause)
		}
		return ErrWindowClosed
	}

	timer := time.NewTimer(startup)
	defer timer.Stop()
	starting := timer.C

	for {
		var next chan<- cdpCommand
		var head cdpCommand
		if len(queue) > 0 {
			next, head = writes, queue[0]
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-starting:
			starting = nil
			if !c.ready {
				return "", fmt.Errorf("%w: за %s страница капчи не открылась", ErrBrowser, startup)
			}
		case next <- head:
			queue = queue[1:]
		case err := <-writeErr:
			return "", gone(err)
		case err := <-readErr:
			return "", gone(err)
		case m := <-msgs:
			cmds, out := c.handle(m)
			if out != nil {
				if errors.Is(out.err, ErrWindowClosed) {
					out.err = gone(out.err)
				}
				if out.err == nil && strings.TrimSpace(out.token) == "" {
					out.err = ErrNoToken
				}
				return out.token, out.err
			}
			queue = append(queue, cmds...)
		}
	}
}
