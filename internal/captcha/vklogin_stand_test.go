package captcha

// Стенд OAuth VK для тестов окна входа на настоящем движке. Окно открывает
// адрес входа на несуществующем хосте (oauth.vk.invalid, RFC 2606), стенд
// уводит его на настоящий https://oauth.vk.ru/blank.html, а браузер ходит в
// сеть только через прокси стенда, который любой CONNECT уводит на стенд
// (loginStand.browserArgs): так проверяется и перехват настоящего
// blank.html, и то, что запрос blank.html до «VK» не доходит.
//
// К настоящему VK тесты не ходят, даже если прокси стенда браузер не
// послушает (--no-proxy-server, PAC или политика ProxySettings из обёртки
// браузера, его политик или WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS): правила
// резолвера браузера не дают разрешить ни одно имя, а адрес входа и так не
// существует.
// Человека нет: страница входа сразу «пропускает» его дальше.
//
// Варианты (параметр scenario в адресе входа):
//
//	redirect — редирект сервера на blank.html#access_token=…;
//	js       — страница уводит на blank.html#access_token=… из JS;
//	query    — токен в строке запроса blank.html?access_token=…;
//	denied   — человек отказал: blank.html#error=access_denied…;
//	expiring — первый токен со сроком, второй бессрочный;
//	expiring-always — токен всегда со сроком;
//	silent   — сначала payload= с «тихим» токеном VK ID, потом токен;
//	notoken  — blank.html без токена;
//	idle     — страница входа ничего не делает (закрытие окна, срок).

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// standUserID — выдуманный id аккаунта, который отдаёт стенд.
const standUserID = "123456789"

// standVKToken — токен n-го входа (с единицы) в варианте scenario.
func standVKToken(scenario string, n int) string {
	return fmt.Sprintf("stand-vk-token-%s-%d-0123456789abcdef", scenario, n)
}

type loginStand struct {
	*httptest.Server

	mu         sync.Mutex
	authorized map[string]int // сколько раз открывали страницу входа, по варианту
	blankHits  int            // сколько запросов blank.html дошло до стенда
	tunnels    map[net.Conn]bool
}

func newLoginStand(t *testing.T) *loginStand {
	t.Helper()
	s := &loginStand{authorized: map[string]int{}, tunnels: map[net.Conn]bool{}}
	s.Server = httptest.NewTLSServer(s.handler())
	t.Cleanup(s.Close)
	return s
}

// authURL — адрес входа с вариантом: несуществующий хост, стенд за прокси.
func (s *loginStand) authURL(scenario string) string {
	return "https://oauth.vk.invalid/authorize?client_id=7793118&display=page&scenario=" + scenario
}

// standResolverRules — правила резолвера браузера: ни одно имя не
// разрешается, кроме адреса прокси стенда. Запросам через прокси они не
// мешают (имя разрешает прокси), а без прокси браузер не уйдёт никуда.
const standResolverRules = "MAP * ~NOTFOUND, EXCLUDE 127.0.0.1"

// browserArgs — аргументы браузера окна входа: вся его сеть — через прокси
// стенда, который любой CONNECT уводит на стенд, а не к VK; мимо прокси
// имена не разрешаются (standResolverRules). Значение с пробелами — одним
// аргументом; в строку WebView2 его кавычит тест Windows.
//
// Прокси свой, а не StartProxy (proxy.go): тот пускает только домены VK, а
// адрес входа стенда — на .invalid.
func (s *loginStand) browserArgs(t *testing.T) []string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("прокси стенда: %v", err)
	}
	t.Cleanup(func() {
		ln.Close()
		s.mu.Lock()
		defer s.mu.Unlock()
		for c := range s.tunnels {
			c.Close()
		}
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.tunnel(c)
		}
	}()
	return []string{
		"--ignore-certificate-errors",
		"--proxy-server=http://" + ln.Addr().String(),
		"--host-resolver-rules=" + standResolverRules,
	}
}

// track запоминает соединение прокси, чтобы закрыть его в конце теста.
func (s *loginStand) track(c net.Conn, on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if on {
		s.tunnels[c] = true
	} else {
		delete(s.tunnels, c)
	}
}

// tunnel — одно соединение прокси стенда: CONNECT куда угодно ведёт на стенд.
func (s *loginStand) tunnel(c net.Conn) {
	s.track(c, true)
	defer s.track(c, false)
	defer c.Close()

	br := bufio.NewReader(c)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	if req.Method != http.MethodConnect {
		io.WriteString(c, "HTTP/1.1 405 Method Not Allowed\r\nContent-Length: 0\r\n\r\n")
		return
	}
	up, err := net.Dial("tcp", s.Listener.Addr().String())
	if err != nil {
		io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
		return
	}
	s.track(up, true)
	defer s.track(up, false)
	defer up.Close()
	if _, err := io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}

	done := make(chan struct{}, 2)
	go func() { io.Copy(up, br); done <- struct{}{} }()
	go func() { io.Copy(c, up); done <- struct{}{} }()
	<-done
}

func (s *loginStand) hits(scenario string) (authorized, blank int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.authorized[scenario], s.blankHits
}

func (s *loginStand) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		scenario := r.URL.Query().Get("scenario")
		s.mu.Lock()
		s.authorized[scenario]++
		n := s.authorized[scenario]
		s.mu.Unlock()

		token := func(expires string) string {
			return "access_token=" + url.QueryEscape(standVKToken(scenario, n)) +
				"&expires_in=" + expires + "&user_id=" + standUserID
		}
		blank := "https://oauth.vk.ru/blank.html"
		redirect := func(to string) { http.Redirect(w, r, to, http.StatusFound) }
		switch scenario {
		case "redirect":
			redirect(blank + "#" + token("0"))
		case "query":
			redirect(blank + "?" + token("0"))
		case "denied":
			redirect(blank + "#error=access_denied&error_reason=user_denied&error_description=User+denied+your+request")
		case "expiring":
			if n == 1 {
				redirect(blank + "#" + token("86400"))
			} else {
				redirect(blank + "#" + token("0"))
			}
		case "expiring-always":
			redirect(blank + "#" + token("86400"))
		case "silent":
			if n == 1 {
				payload := url.QueryEscape(`{"type":"silent_token","token":"stand-silent-token","uuid":"stand"}`)
				redirect(blank + "#payload=" + payload)
			} else {
				redirect(blank + "#" + token("0"))
			}
		case "notoken":
			redirect(blank)
		case "js":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			io.WriteString(w, `<!doctype html><meta charset="utf-8"><title>стенд входа</title>`+
				`<p>Стенд входа VK csqtt-cli</p><script>setTimeout(function () {`+
				`location.href = "`+blank+`#`+token("0")+`"; }, 300);</script>`)
		default: // idle
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			io.WriteString(w, `<!doctype html><meta charset="utf-8"><title>стенд входа</title><p>Войдите</p>`)
		}
	})
	mux.HandleFunc("/blank.html", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.blankHits++
		s.mu.Unlock()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, `<!doctype html><meta charset="utf-8"><title>blank</title>`)
	})
	return mux
}

// loginProfilesNow — каталоги профилей окна входа во временном каталоге.
func loginProfilesNow() map[string]bool {
	matches, _ := filepath.Glob(filepath.Join(os.TempDir(), loginProfilePrefix+"*"))
	set := make(map[string]bool, len(matches))
	for _, m := range matches {
		set[m] = true
	}
	return set
}

// noLoginProfileLeft — после Login профиля окна входа уже нет: вход убирает
// его до того, как вернуться.
func noLoginProfileLeft(t *testing.T, before map[string]bool) {
	t.Helper()
	var left []string
	for dir := range loginProfilesNow() {
		if !before[dir] {
			left = append(left, dir)
		}
	}
	if len(left) > 0 {
		t.Errorf("профиль окна входа не убран: %s", strings.Join(left, ", "))
	}
}

// logBuffer — журнал окна для проверки, что в нём нет токена.
type logBuffer struct {
	mu sync.Mutex
	b  strings.Builder
	t  *testing.T
}

func (l *logBuffer) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	line := fmt.Sprintf(format, args...)
	l.b.WriteString(line + "\n")
	l.t.Log(line)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
