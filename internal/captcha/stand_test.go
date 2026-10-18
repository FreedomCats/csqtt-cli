package captcha

// Стенд для тестов окон на настоящем движке: страница, изображающая капчу VK,
// и её API. Поведение — по живой записи (page.go): страница зовёт
// initSession, settings, componentDone, потом «человек ставит галочку» —
// check, после токена — endSession. Человека нет: страница делает всё сама,
// поэтому окна мелькают и закрываются.
//
// Варианты (параметр scenario):
//
//	checkbox — галочка → OK с токеном;
//	slider   — галочка → BOT (тип slider), «слайдер» → OK с токеном;
//	limit    — галочка → ERROR_LIMIT;
//	initerror — initSession отвечает ошибкой VK 10, как было живьём;
//	idle     — до галочки дело не доходит (закрытие окна, срок);
//	webdriver — токен говорит, видит ли страница navigator.webdriver.
//
// via=xhr — те же запросы через XMLHttpRequest вместо fetch.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// standPage — страница капчи стенда. API она зовёт по относительному адресу
// /method/…: стенд — один сервер, а через прокси — «id.vk.com».
const standPage = `<!doctype html><meta charset="utf-8"><title>стенд капчи</title>
<p>Стенд капчи csqtt-cli</p>
<script>
var q = new URLSearchParams(location.search);
var scenario = q.get('scenario') || 'checkbox', via = q.get('via') || 'fetch';
function api(method, extra) {
	var url = '/method/captchaNotRobot.' + method + '?v=5.131&scenario=' + scenario + '&via=' + via + (extra || '');
	if (via === 'xhr') {
		return new Promise(function (resolve) {
			var x = new XMLHttpRequest();
			x.open('POST', url);
			x.onload = function () { resolve(JSON.parse(x.responseText)); };
			x.send('session_token=stand');
		});
	}
	return fetch(url, {method: 'POST', body: 'session_token=stand'}).then(function (r) { return r.json(); });
}
function later(f) { setTimeout(f, 300); }
api('initSession').then(function () { return api('settings'); }).then(function () {
	return api('componentDone');
}).then(function () {
	if (scenario === 'idle') { return; }
	later(function () {
		api('check', '&attempt=1&wd=' + navigator.webdriver).then(function (r) {
			var st = r.response && r.response.status;
			if (st === 'BOT') {
				later(function () {
					api('check', '&attempt=2').then(function (r2) {
						if (r2.response && r2.response.status === 'OK') { api('endSession'); }
					});
				});
			} else if (st === 'OK') {
				api('endSession');
			}
		});
	});
});
</script>`

// stand — сервер стенда; calls — какие методы API звала страница.
type stand struct {
	*httptest.Server

	mu    sync.Mutex
	calls []string
}

func newStand(t *testing.T) *stand {
	t.Helper()
	s := &stand{}
	s.Server = httptest.NewServer(s.handler())
	t.Cleanup(s.Close)
	return s
}

// newTLSStand — то же по HTTPS: для проверки прокси.
func newTLSStand(t *testing.T) *stand {
	t.Helper()
	s := &stand{}
	s.Server = httptest.NewTLSServer(s.handler())
	t.Cleanup(s.Close)
	return s
}

// standToken — токен, который стенд отдаёт в варианте scenario.
func standToken(scenario, via string) string { return "stand-token-" + scenario + "-" + via }

func (s *stand) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/not_robot_captcha", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, standPage)
	})
	mux.HandleFunc("/frame", func(w http.ResponseWriter, r *http.Request) {
		// Капча во фрейме другого сайта: 127.0.0.1 и localhost — разные
		// сайты, и Chromium держит такой фрейм в своём процессе.
		frame := "http://" + strings.Replace(r.Host, "127.0.0.1", "localhost", 1) +
			"/not_robot_captcha?" + r.URL.RawQuery
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, `<!doctype html><meta charset="utf-8"><title>стенд</title>`+
			`<p>Стенд капчи csqtt-cli: фрейм</p><iframe src="`+frame+`"></iframe>`)
	})
	mux.HandleFunc("/method/", func(w http.ResponseWriter, r *http.Request) {
		method := strings.TrimPrefix(r.URL.Path, "/method/captchaNotRobot.")
		q := r.URL.Query()
		s.mu.Lock()
		s.calls = append(s.calls, method)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if method == "initSession" && q.Get("scenario") == "initerror" {
			io.WriteString(w, `{"error":{"error_code":10,"error_msg":"Internal server error"}}`)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"response": standReply(method, q)})
	})
	return mux
}

// standReply — ответ API стенда в том виде, в каком его отдаёт VK.
func standReply(method string, q url.Values) map[string]any {
	scenario, via := q.Get("scenario"), q.Get("via")
	switch method {
	case "initSession":
		return map[string]any{"status": "OK", "show_captcha_type": "checkbox",
			"content_settings": []any{map[string]any{"type": "slider", "settings_key": "stand"}}}
	case "settings":
		return map[string]any{"status": "OK", "sensors_delay": 200}
	case "check":
		ok := func(token string) map[string]any {
			return map[string]any{"status": "OK", "success_token": token, "redirect": "", "show_captcha_type": ""}
		}
		switch scenario {
		case "slider":
			if q.Get("attempt") == "1" {
				return map[string]any{"status": "BOT", "show_captcha_type": "slider"}
			}
			return ok(standToken(scenario, via))
		case "limit":
			return map[string]any{"status": "ERROR_LIMIT"}
		case "webdriver":
			return ok("stand-token-wd-" + q.Get("wd"))
		}
		return ok(standToken(scenario, via))
	}
	return map[string]any{"status": "OK"}
}

// methods — какие методы API звала страница.
func (s *stand) methods() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

// page — адрес страницы капчи стенда с вариантом.
func (s *stand) page(scenario, via string) string {
	return s.URL + "/not_robot_captcha?domain=vk.com&session_token=stand&variant=popup&blank=1&scenario=" +
		scenario + "&via=" + via
}

func standRequest(url string, budget time.Duration) Request {
	return Request{Mode: ModeManual, RedirectURI: url, SessionToken: "stand", Deadline: time.Now().Add(budget)}
}

// profilesNow — каталоги профилей, которые сейчас лежат во временном каталоге.
func profilesNow() map[string]bool {
	matches, _ := filepath.Glob(filepath.Join(os.TempDir(), profilePrefix+"*"))
	set := make(map[string]bool, len(matches))
	for _, m := range matches {
		set[m] = true
	}
	return set
}

// waitProfilesGone ждёт, пока профили, появившиеся после before, будут убраны.
func waitProfilesGone(t *testing.T, before map[string]bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		var left []string
		for dir := range profilesNow() {
			if !before[dir] {
				left = append(left, dir)
			}
		}
		if len(left) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("профиль капчи не убран за 15 с: %s", strings.Join(left, ", "))
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}
