package captcha

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"testing"
	"time"
)

// testVKToken — выдуманный токен VK: его не должно быть ни в журнале, ни в
// сообщениях.
const testVKToken = "vk1.a.выдуманный-токен-0123456789"

func blankURL(fragment string) string {
	return "https://oauth.vk.ru/blank.html#" + fragment
}

func tokenFragment(expires string) string {
	return "access_token=" + url.QueryEscape(testVKToken) + "&expires_in=" + expires + "&user_id=42"
}

func TestLoginFlowToken(t *testing.T) {
	for name, raw := range map[string]string{
		"фрагмент":        blankURL(tokenFragment("0")),
		"строка запроса":  "https://oauth.vk.ru/blank.html?" + tokenFragment("0"),
		"старый домен":    "https://oauth.vk.com/blank.html#" + tokenFragment("0"),
		"без expires_in":  blankURL("access_token=" + url.QueryEscape(testVKToken) + "&user_id=42"),
		"регистр хоста":   "https://OAUTH.VK.RU/blank.html#" + tokenFragment("0"),
		"лишние поля":     blankURL(tokenFragment("0") + "&email=user%40example.com&state=x"),
		"пустой фрагмент": "https://oauth.vk.ru/blank.html?" + tokenFragment("0") + "#",
	} {
		t.Run(name, func(t *testing.T) {
			var logged []string
			f := newLoginFlow(func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) })
			st := f.observe(raw, false)
			if st.action != loginDone || st.token.AccessToken != testVKToken || st.token.UserID != "42" {
				t.Fatalf("шаг: действие %d, user_id %q, ошибка %v", st.action, st.token.UserID, st.err)
			}
			if strings.Contains(strings.Join(logged, "\n"), testVKToken) {
				t.Errorf("в журнале токен: %q", logged)
			}
			// Та же навигация вторым событием — решение уже есть.
			if again := f.observe(raw, true); again.action != loginWait || !again.known {
				t.Errorf("повтор того же адреса: %+v", again)
			}
		})
	}
}

func TestLoginFlowIgnores(t *testing.T) {
	f := newLoginFlow(nil)
	for _, raw := range []string{
		"https://oauth.vk.ru/authorize?client_id=7793118",
		"https://id.vk.ru/auth?redirect_uri=https%3A%2F%2Foauth.vk.ru%2Fblank.html#" + tokenFragment("0"),
		"https://evil.example/blank.html#" + tokenFragment("0"),
		"https://oauth.vk.ru/blank.htmlx#" + tokenFragment("0"),
		"не адрес %zz",
		"",
	} {
		if st := f.observe(raw, true); st.action != loginWait || st.known {
			t.Errorf("%q: %+v", raw, st)
		}
	}
}

func TestLoginFlowError(t *testing.T) {
	f := newLoginFlow(nil)
	st := f.observe(blankURL("error=access_denied&error_reason=user_denied&error_description=User+denied+your+request"), false)
	var e *VKAuthError
	if st.action != loginFail || !errors.As(st.err, &e) || e.Code != "access_denied" ||
		st.err.Error() != "VK отказал во входе: User denied your request (access_denied)" {
		t.Fatalf("шаг: %+v", st)
	}

	// Ошибка в строке запроса, управляющие знаки из текста VK вычищены.
	st = newLoginFlow(nil).observe("https://oauth.vk.ru/blank.html?error=invalid_request&error_description=a%0Ab", true)
	if st.action != loginFail || st.err.Error() != "VK отказал во входе: a b (invalid_request)" {
		t.Fatalf("ошибка в строке запроса: %+v", st)
	}
}

func TestLoginFlowNoToken(t *testing.T) {
	f := newLoginFlow(nil)
	if st := f.observe("https://oauth.vk.ru/blank.html", false); st.action != loginWait {
		t.Fatalf("без токена, но не последнее слово: %+v", st)
	}
	if st := f.observe("https://oauth.vk.ru/blank.html#state=x", true); st.action != loginFail ||
		!errors.Is(st.err, ErrLoginNoToken) {
		t.Fatalf("без токена, последнее слово: %+v", st)
	}
}

// Токен со сроком: один повтор, второй раз — ошибка; со второго раза
// бессрочный — успех.
func TestLoginFlowExpiring(t *testing.T) {
	f := newLoginFlow(nil)
	if st := f.observe(blankURL(tokenFragment("86400")), false); st.action != loginRetry || st.delay != 0 {
		t.Fatalf("первый токен со сроком: %+v", st)
	}
	if st := f.observe(blankURL(tokenFragment("86400")+"&n=2"), false); st.action != loginFail ||
		!errors.Is(st.err, ErrLoginExpiring) {
		t.Fatalf("второй токен со сроком: %+v", st)
	}

	f = newLoginFlow(nil)
	f.observe(blankURL(tokenFragment("мусор")), false)
	if st := f.observe(blankURL(tokenFragment("0")), false); st.action != loginDone {
		t.Fatalf("после повтора бессрочный: %+v", st)
	}
}

func TestLoginFlowSilent(t *testing.T) {
	payload := func(n int) string {
		return "payload=" + url.QueryEscape(fmt.Sprintf(`{"type":"silent_token","token":"тихий-%d","uuid":"x"}`, n))
	}
	f := newLoginFlow(nil)
	if st := f.observe("https://oauth.vk.com/#"+payload(1), false); st.action != loginRetry || st.delay != silentRetryDelay {
		t.Fatalf("первый тихий токен: %+v", st)
	}
	if st := f.observe("https://oauth.vk.ru/#"+payload(2), false); st.action != loginFail ||
		!errors.Is(st.err, ErrLoginSilent) {
		t.Fatalf("второй тихий токен: %+v", st)
	}

	// payload другого вида или на чужом хосте — не повод открывать вход заново.
	f = newLoginFlow(nil)
	for _, raw := range []string{
		"https://oauth.vk.ru/#payload=" + url.QueryEscape(`{"type":"other","token":"x"}`),
		"https://id.vk.ru/#" + payload(1),
	} {
		if st := f.observe(raw, true); st.action != loginWait {
			t.Errorf("%q: %+v", raw, st)
		}
	}
}

// Сторонний сайт (Android-клиент смотрит payload= и на vkhost.github.io) на
// вход не влияет ничем: ни токеном, ни ошибкой, ни повтором.
func TestLoginFlowForeignPayloadHost(t *testing.T) {
	silent := "payload=" + url.QueryEscape(`{"type":"silent_token","token":"тихий","uuid":"x"}`)
	for _, fragment := range []string{silent, silent + "&" + tokenFragment("0"), silent + "&error=access_denied"} {
		st := newLoginFlow(nil).observe("https://vkhost.github.io/#"+fragment, true)
		if st.action != loginWait || st.known || st.token.AccessToken != "" {
			t.Errorf("%q: %+v", fragment, st)
		}
	}
}

// Токен и ошибка принимаются только с blank.html: адрес, который смотрится
// ради payload=, их не даёт, даже если они во фрагменте рядом.
func TestLoginFlowPayloadNoToken(t *testing.T) {
	silent := "payload=" + url.QueryEscape(`{"type":"silent_token","token":"тихий","uuid":"x"}`)
	other := "payload=" + url.QueryEscape(`{"type":"other"}`)
	for _, host := range []string{"oauth.vk.ru", "oauth.vk.com"} {
		for _, fragment := range []string{other + "&" + tokenFragment("0"), other + "&error=access_denied"} {
			raw := "https://" + host + "/#" + fragment
			if st := newLoginFlow(nil).observe(raw, true); st.action != loginWait {
				t.Errorf("%s, %s: %+v", host, fragment[len(other):], st)
			}
		}
		raw := "https://" + host + "/#" + silent + "&" + tokenFragment("0")
		if st := newLoginFlow(nil).observe(raw, true); st.action != loginRetry || st.token.AccessToken != "" {
			t.Errorf("%s, тихий токен и access_token: %+v", host, st)
		}
	}
}

func TestLoginFlowUserID(t *testing.T) {
	st := newLoginFlow(nil).observe(blankURL("access_token=x&expires_in=0&user_id=12ab"), false)
	if st.action != loginDone || st.token.UserID != "" {
		t.Fatalf("не число в user_id: %+v", st)
	}
}

// Токен не печатается ни одним глаголом fmt.
func TestVKTokenString(t *testing.T) {
	tok := VKToken{AccessToken: testVKToken, UserID: "42"}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
		got := fmt.Sprintf(verb, tok)
		if strings.Contains(got, testVKToken) || !strings.Contains(got, "42") {
			t.Errorf("%s: %s", verb, got)
		}
	}
	if got := fmt.Sprintf("%v", &tok); strings.Contains(got, testVKToken) {
		t.Errorf("указатель: %s", got)
	}
}

// ─────────────── cdpLogin: автомат окна входа ───────────────

// openLogin проводит автомат до открытой страницы входа: сессия вкладки
// S1, вкладка (и главный фрейм) — PAGE1.
func openLogin(t *testing.T, c *cdpLogin) cdpCommand {
	t.Helper()
	start := c.start()
	cmds := loginStep1(t, c, replyTo(start[0], `{"targetInfos":[{"targetId":"PAGE1","type":"page"}]}`))
	if len(cmds) != 1 || cmds[0].Method != "Target.attachToTarget" {
		t.Fatalf("подключение к вкладке: %+v", cmds)
	}
	setup := loginStep1(t, c, replyTo(cmds[0], `{"sessionId":"S1"}`))
	if got := strings.Join(methods(setup), ","); got != "Page.enable,Fetch.enable" {
		t.Fatalf("подготовка вкладки: %s", got)
	}
	if p := params(t, setup[1]); !strings.Contains(p, `"urlPattern":"https://oauth.vk.ru/blank.html*"`) ||
		!strings.Contains(p, `"urlPattern":"https://oauth.vk.com/blank.html*"`) {
		t.Errorf("перехват: %s", p)
	}
	if cmds := loginStep1(t, c, replyTo(setup[1], `{}`)); len(cmds) != 0 || c.opened() {
		t.Fatalf("до ответа на Page.enable: %v", methods(cmds))
	}
	nav := loginStep1(t, c, replyTo(setup[0], `{}`))
	if len(nav) != 1 || nav[0].Method != "Page.navigate" || nav[0].SessionID != "S1" || !c.opened() {
		t.Fatalf("после подготовки: %+v", nav)
	}
	if p := params(t, nav[0]); !strings.Contains(p, "oauth.vk.ru/authorize") {
		t.Errorf("Page.navigate: %s", p)
	}
	loginStep1(t, c, replyTo(nav[0], `{"frameId":"PAGE1","loaderId":"L1"}`))
	return nav[0]
}

// loginStep1 отдаёт автомату сообщение и требует, чтобы разговор продолжился.
func loginStep1(t *testing.T, c *cdpLogin, m cdpMessage) []cdpCommand {
	t.Helper()
	cmds, out := c.handle(m)
	if out != nil {
		t.Fatalf("разговор кончился раньше времени: %+v", *out)
	}
	return cmds
}

func paused(t *testing.T, frame, rawURL, fragment string) cdpMessage {
	return msg(t, fmt.Sprintf(`{"method":"Fetch.requestPaused","sessionId":"S1","params":{"requestId":"I1",
		"frameId":%q,"resourceType":"Document","request":{"url":%q,"urlFragment":%q}}}`, frame, rawURL, fragment))
}

func frameNavigated(t *testing.T, rawURL, fragment string) cdpMessage {
	return msg(t, fmt.Sprintf(`{"method":"Page.frameNavigated","sessionId":"S1","params":{"frame":{
		"id":"PAGE1","loaderId":"L2","url":%q,"urlFragment":%q}}}`, rawURL, fragment))
}

// Фрагмент виден в перехваченном запросе — сразу исход.
func TestCDPLoginFromFetch(t *testing.T) {
	c := newCDPLogin(VKAuthURL, nil)
	openLogin(t, c)
	cmds, out := c.handle(paused(t, "PAGE1", "https://oauth.vk.ru/blank.html", "#"+tokenFragment("0")))
	if out == nil || out.err != nil || out.token != testVKToken || c.result.UserID != "42" {
		t.Fatalf("исход: %+v", out)
	}
	// Заглушка — своя страница, к VK запрос не уходит.
	if len(cmds) != 1 || cmds[0].Method != "Fetch.fulfillRequest" {
		t.Fatalf("команды: %v", methods(cmds))
	}
	var p struct {
		RequestID string `json:"requestId"`
		Body      string `json:"body"`
	}
	data, _ := json.Marshal(cmds[0].Params)
	_ = json.Unmarshal(data, &p)
	body, _ := base64.StdEncoding.DecodeString(p.Body)
	if p.RequestID != "I1" || !strings.Contains(string(body), "Секунду") {
		t.Errorf("заглушка: %s", data)
	}
}

// Фрагмента нет в запросе — он виден в Page.frameNavigated.
func TestCDPLoginFromFrameNavigated(t *testing.T) {
	c := newCDPLogin(VKAuthURL, nil)
	openLogin(t, c)
	cmds := loginStep1(t, c, paused(t, "PAGE1", "https://oauth.vk.ru/blank.html", ""))
	if got := methods(cmds); len(got) != 1 || got[0] != "Fetch.fulfillRequest" {
		t.Fatalf("без фрагмента: %v", got)
	}
	_, out := c.handle(frameNavigated(t, "https://oauth.vk.ru/blank.html", "#"+tokenFragment("0")))
	if out == nil || out.err != nil || out.token != testVKToken {
		t.Fatalf("исход: %+v", out)
	}
}

// Фрагмента не видно нигде — адрес читается со страницы, и это последнее
// слово.
func TestCDPLoginFromLocation(t *testing.T) {
	for _, tc := range []struct {
		name, href string
		want       error
	}{
		{"токен", blankURL(tokenFragment("0")), nil},
		{"пусто", "https://oauth.vk.ru/blank.html", ErrLoginNoToken},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newCDPLogin(VKAuthURL, nil)
			openLogin(t, c)
			loginStep1(t, c, paused(t, "PAGE1", "https://oauth.vk.ru/blank.html", ""))
			cmds := loginStep1(t, c, frameNavigated(t, "https://oauth.vk.ru/blank.html", ""))
			if len(cmds) != 1 || cmds[0].Method != "Runtime.evaluate" || !strings.Contains(params(t, cmds[0]), "location.href") {
				t.Fatalf("команды: %+v", cmds)
			}
			reply, _ := json.Marshal(map[string]any{"result": map[string]any{"type": "string", "value": tc.href}})
			_, out := c.handle(replyTo(cmds[0], string(reply)))
			if out == nil || !errors.Is(out.err, tc.want) || (tc.want == nil && out.token != testVKToken) {
				t.Fatalf("исход: %+v", out)
			}
		})
	}
}

// Повтор: заглушка, затем страница входа заново; та же навигация следующим
// событием решения не меняет и адрес со страницы не читает.
func TestCDPLoginRetry(t *testing.T) {
	c := newCDPLogin(VKAuthURL, nil)
	openLogin(t, c)
	expiring := "#" + tokenFragment("86400")
	cmds := loginStep1(t, c, paused(t, "PAGE1", "https://oauth.vk.ru/blank.html", expiring))
	if got := strings.Join(methods(cmds), ","); got != "Fetch.fulfillRequest,Page.navigate" || cmds[1].after != 0 {
		t.Fatalf("повтор: %s", got)
	}
	if cmds := loginStep1(t, c, frameNavigated(t, "https://oauth.vk.ru/blank.html", expiring)); len(cmds) != 0 {
		t.Fatalf("та же навигация: %v", methods(cmds))
	}

	// «Тихий» токен — повтор через паузу.
	c = newCDPLogin(VKAuthURL, nil)
	openLogin(t, c)
	silent := "#payload=" + url.QueryEscape(`{"type":"silent_token","token":"x"}`)
	cmds = loginStep1(t, c, frameNavigated(t, "https://oauth.vk.ru/", silent))
	if len(cmds) != 1 || cmds[0].Method != "Page.navigate" || cmds[0].after != silentRetryDelay {
		t.Fatalf("тихий токен: %+v", cmds)
	}
}

// blank.html во фрейме — не наш редирект: запрос идёт дальше как шёл.
func TestCDPLoginChildFrame(t *testing.T) {
	c := newCDPLogin(VKAuthURL, nil)
	openLogin(t, c)
	cmds := loginStep1(t, c, paused(t, "CHILD", "https://oauth.vk.ru/blank.html", "#"+tokenFragment("0")))
	if len(cmds) != 1 || cmds[0].Method != "Fetch.continueRequest" {
		t.Fatalf("фрейм: %v", methods(cmds))
	}
	child := msg(t, `{"method":"Page.frameNavigated","sessionId":"S1","params":{"frame":{"id":"CHILD","parentId":"PAGE1",
		"url":"https://oauth.vk.ru/blank.html","urlFragment":"#`+tokenFragment("0")+`"}}}`)
	if cmds := loginStep1(t, c, child); len(cmds) != 0 {
		t.Fatalf("фрейм, frameNavigated: %v", methods(cmds))
	}
}

func TestCDPLoginEnds(t *testing.T) {
	t.Run("окно закрыто", func(t *testing.T) {
		c := newCDPLogin(VKAuthURL, nil)
		openLogin(t, c)
		_, out := c.handle(msg(t, `{"method":"Target.detachedFromTarget","params":{"sessionId":"S1"}}`))
		if out == nil || !errors.Is(out.err, ErrWindowClosed) {
			t.Fatalf("исход: %+v", out)
		}
	})
	t.Run("страница не загрузилась", func(t *testing.T) {
		c := newCDPLogin(VKAuthURL, nil)
		start := c.start()
		attach := loginStep1(t, c, replyTo(start[0], `{"targetInfos":[{"targetId":"PAGE1","type":"page"}]}`))
		setup := loginStep1(t, c, replyTo(attach[0], `{"sessionId":"S1"}`))
		loginStep1(t, c, replyTo(setup[0], `{}`))
		nav := loginStep1(t, c, replyTo(setup[1], `{}`))
		_, out := c.handle(replyTo(nav[0], `{"frameId":"PAGE1","errorText":"net::ERR_NAME_NOT_RESOLVED"}`))
		if out == nil || out.err == nil || !strings.Contains(out.err.Error(), "ERR_NAME_NOT_RESOLVED") {
			t.Fatalf("исход: %+v", out)
		}
	})
	t.Run("переход прерван", func(t *testing.T) {
		c := newCDPLogin(VKAuthURL, nil)
		nav := openLogin(t, c)
		loginStep1(t, c, replyTo(nav, `{"frameId":"PAGE1","errorText":"net::ERR_ABORTED"}`))
	})
	t.Run("подготовка отклонена", func(t *testing.T) {
		c := newCDPLogin(VKAuthURL, nil)
		start := c.start()
		attach := loginStep1(t, c, replyTo(start[0], `{"targetInfos":[{"targetId":"PAGE1","type":"page"}]}`))
		setup := loginStep1(t, c, replyTo(attach[0], `{"sessionId":"S1"}`))
		_, out := c.handle(cdpMessage{ID: setup[1].ID, SessionID: "S1", Error: &cdpError{Code: -32601, Message: "нет Fetch"}})
		if out == nil || !errors.Is(out.err, ErrLoginBrowser) {
			t.Fatalf("исход: %+v", out)
		}
	})
}

// ─────────────── converse: отложенная команда ───────────────

// delayedConv — разговор: команда Now, по ответу на неё — Later, отложенная
// на after; кончается ответом на Later.
type delayedConv struct{ after time.Duration }

func (d delayedConv) opened() bool        { return true }
func (d delayedConv) start() []cdpCommand { return []cdpCommand{{ID: 2, Method: "Now"}} }
func (d delayedConv) handle(m cdpMessage) ([]cdpCommand, *outcome) {
	if m.ID == 1 {
		return nil, &outcome{token: "ok"}
	}
	return []cdpCommand{{ID: 1, Method: "Later", after: d.after}}, nil
}

func TestConverseDelayed(t *testing.T) {
	cmdR, cmdW := io.Pipe()
	resR, resW := io.Pipe()
	defer cmdR.Close()
	defer resW.Close()

	// Браузер: отвечает на каждую команду и запоминает, когда она пришла.
	got := make(chan time.Time, 4)
	go func() {
		buf := make([]byte, 4096)
		var pending []byte
		for {
			n, err := cmdR.Read(buf)
			if err != nil {
				return
			}
			pending = append(pending, buf[:n]...)
			for {
				i := strings.IndexByte(string(pending), 0)
				if i < 0 {
					break
				}
				var cmd cdpCommand
				_ = json.Unmarshal(pending[:i], &cmd)
				pending = pending[i+1:]
				got <- time.Now()
				fmt.Fprintf(resW, `{"id":%d,"result":{}}`+"\x00", cmd.ID)
			}
		}
	}()

	start := time.Now()
	token, err := converse(context.Background(), resR, cmdW, delayedConv{after: 300 * time.Millisecond}, time.Second)
	if err != nil || token != "ok" {
		t.Fatalf("converse = %q, %v", token, err)
	}
	now, later := <-got, <-got
	if d := later.Sub(now); d < 300*time.Millisecond || later.Sub(start) > 5*time.Second {
		t.Errorf("отложенная команда ушла через %s", d)
	}
}
