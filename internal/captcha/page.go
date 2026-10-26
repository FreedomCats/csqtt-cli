// page.go — что делает страница капчи VK и как по её ответам понять исход.
//
// Страница id.vk.ru/not_robot_captcha (id.vk.com) ходит в API запросами
// POST …/method/captchaNotRobot.<метод> (живая запись, задача C9):
//
//   - initSession — status=OK и show_captcha_type: какую капчу показать
//     (checkbox; в content_settings есть и slider, и sound). Бывает и ошибка
//     VK (живьём — 10, Internal server error): капчи тогда нет, страница
//     показывает ошибку, и окно закрывается сразу (ErrPageInit);
//   - settings, componentDone — подготовка;
//   - check — ответ человека. Токен — response.success_token при
//     status=OK. ERROR_LIMIT — попытки на эту сессию кончились, страница
//     пишет «Лимит попыток исчерпан». BOT — VK не поверил галочке, и страница,
//     видимо, переходит к слайдеру: ждём дальше;
//   - endSession — после токена;
//   - refreshSession — через несколько минут без действий.
//
// Редиректа с токеном у VK нет: токен есть только в теле ответа check. Окна
// видят эти ответы по-своему (CDP — сетью браузера, WebView2 — перехватчиком
// JS), а разбирает их одно и то же место — pageWatch.
package captcha

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// API-методы страницы капчи и статусы check, которые что-то значат для исхода.
const (
	methodInit  = "captchaNotRobot.initSession"
	methodCheck = "captchaNotRobot.check"
	statusOK    = "OK"
	statusLimit = "ERROR_LIMIT"
)

// apiMethodRE — метод API капчи в адресе запроса.
var apiMethodRE = regexp.MustCompile(`/method/(captchaNotRobot\.[A-Za-z]+)`)

// apiMethod — метод API капчи по адресу запроса; ok=false — запрос не к нему.
func apiMethod(url string) (method string, ok bool) {
	m := apiMethodRE.FindStringSubmatch(url)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// apiReply — что важно в ответе API капчи.
type apiReply struct {
	Response *struct {
		Status   string `json:"status"`
		ShowType string `json:"show_captcha_type"`
		Token    string `json:"success_token"`
	} `json:"response"`
	Error *struct {
		Code    int    `json:"error_code"`
		Message string `json:"error_msg"`
	} `json:"error"`
}

func parseReply(body []byte) (apiReply, bool) {
	var r apiReply
	if json.Unmarshal(body, &r) != nil || (r.Response == nil && r.Error == nil) {
		return apiReply{}, false
	}
	return r, true
}

// checkOutcome — исход по ответу check: токен, ERROR_LIMIT или nil — ждать
// дальше. Статусы сверяются без учёта регистра, как в ядре.
func checkOutcome(r apiReply) *outcome {
	if r.Response == nil {
		return nil
	}
	switch {
	case strings.EqualFold(r.Response.Status, statusOK) && strings.TrimSpace(r.Response.Token) != "":
		return &outcome{token: strings.TrimSpace(r.Response.Token)}
	case strings.EqualFold(r.Response.Status, statusLimit):
		return &outcome{err: ErrLimit}
	}
	return nil
}

// pageWatch разбирает ответы API, которые окно увидело на странице капчи, и
// пишет в журнал, что происходит: какой метод, статус и тип капчи. Токен в
// журнал не попадает.
type pageWatch struct {
	logf func(string, ...any)
	// checked, если задан, получает каждый ответ на check — и с исходом, и
	// без: по нему автоклик узнаёт, поверил ли VK галочке (autoclick.go).
	checked func(checkReply)
}

// checkReply — что VK ответил на check, без токена.
type checkReply struct {
	status   string
	showType string
	token    bool   // в ответе есть токен
	vkError  int    // код ошибки VK; 0 — ответ без ошибки
	vkMsg    string // её текст
}

func (w pageWatch) notify(r checkReply) {
	if w.checked != nil {
		w.checked(r)
	}
}

// observe — один ответ API. Исход не nil — окно можно закрывать.
func (w pageWatch) observe(method string, body []byte) *outcome {
	logf := w.logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	r, ok := parseReply(body)
	if !ok {
		logf("[CAPTCHA] страница: %s — ответ не разобран", method)
		return nil
	}
	if r.Error != nil {
		logf("[CAPTCHA] страница: %s — ошибка VK %d: %s", method, r.Error.Code, r.Error.Message)
		if method == methodCheck {
			w.notify(checkReply{vkError: r.Error.Code, vkMsg: r.Error.Message})
		}
		if method == methodInit {
			return &outcome{err: fmt.Errorf("%w: initSession — ошибка VK %d: %s", ErrPageInit,
				r.Error.Code, r.Error.Message)}
		}
		return nil
	}
	status, showType := r.Response.Status, r.Response.ShowType
	switch method {
	case methodInit:
		logf("[CAPTCHA] страница: %s — status=%s, тип капчи %q", method, status, showType)
	case methodCheck:
		out := checkOutcome(r)
		w.notify(checkReply{status: status, showType: showType, token: out != nil && out.err == nil})
		switch {
		case out == nil:
			logf("[CAPTCHA] страница: %s — status=%s, тип капчи %q: ждём решения в окне", method, status, showType)
		case out.err != nil:
			logf("[CAPTCHA] страница: %s — status=%s: %v", method, status, out.err)
		default:
			logf("[CAPTCHA] страница: %s — status=%s, токен получен (%d зн.)", method, status, len(out.token))
		}
		return out
	default:
		logf("[CAPTCHA] страница: %s — status=%s", method, status)
	}
	return nil
}
