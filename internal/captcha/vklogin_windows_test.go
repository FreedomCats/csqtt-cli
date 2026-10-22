//go:build windows

package captcha

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	webview2 "github.com/jchv/go-webview2"
)

// Окно входа на стенде OAuth (vklogin_stand_test.go), как стенд окна капчи,
// включается руками:
//
//	CSQTT_WEBVIEW2=1 go test ./internal/captcha -run WebView2Login -v

func newLoginWebView2(t *testing.T, s *loginStand, scenario string) (*WebView2, *bytes.Buffer, *logBuffer) {
	t.Helper()
	w := newStandWebView2(t)
	out := &bytes.Buffer{}
	log := &logBuffer{t: t}
	w.opts.Out = out
	w.opts.Logf = log.logf
	w.extraArgs = joinBrowserArgs(s.browserArgs(t))
	w.authURL = s.authURL(scenario)
	return w, out, log
}

// joinBrowserArgs собирает аргументы в одну строку для WebView2: значение
// с пробелами (правила резолвера стенда) — в кавычках, как в командной
// строке Windows.
func joinBrowserArgs(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		if name, value, ok := strings.Cut(arg, "="); ok && strings.Contains(value, " ") {
			arg = name + `="` + value + `"`
		}
		quoted = append(quoted, arg)
	}
	return strings.Join(quoted, " ")
}

func TestWebView2LoginToken(t *testing.T) {
	for _, tc := range []struct {
		scenario string
		logins   int
	}{
		{"redirect", 1}, {"js", 1}, {"query", 1}, {"expiring", 2}, {"silent", 2},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			s := newLoginStand(t)
			w, out, log := newLoginWebView2(t, s, tc.scenario)
			before := loginProfilesNow()

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			tok, err := w.Login(ctx)
			if err != nil {
				t.Fatalf("Login: %v", err)
			}
			if tok.AccessToken != standVKToken(tc.scenario, tc.logins) || tok.UserID != standUserID {
				t.Fatalf("итог входа: user_id %q, токен ожидаемый: %v", tok.UserID,
					tok.AccessToken == standVKToken(tc.scenario, tc.logins))
			}
			for _, text := range []string{log.String(), out.String()} {
				if strings.Contains(text, "stand-vk-token") {
					t.Errorf("в выводе или журнале токен:\n%s", text)
				}
			}
			noLoginProfileLeft(t, before)
		})
	}
}

func TestWebView2LoginDenied(t *testing.T) {
	s := newLoginStand(t)
	w, _, _ := newLoginWebView2(t, s, "denied")
	_, err := w.Login(context.Background())
	var e *VKAuthError
	if !errors.As(err, &e) || e.Code != "access_denied" {
		t.Fatalf("ошибка %v", err)
	}
}

// Крестик: окно закрывается тем же WM_CLOSE, что шлёт Destroy.
func TestWebView2LoginClosed(t *testing.T) {
	s := newLoginStand(t)
	w, _, _ := newLoginWebView2(t, s, "idle")
	w.opened = func(v webview2.WebView) { time.AfterFunc(3*time.Second, v.Destroy) }
	before := loginProfilesNow()

	_, err := w.Login(context.Background())
	if !errors.Is(err, ErrLoginClosed) {
		t.Fatalf("ошибка %v, ожидалось «окно входа закрыто»", err)
	}
	noLoginProfileLeft(t, before)
}

func TestWebView2LoginDeadline(t *testing.T) {
	s := newLoginStand(t)
	w, _, _ := newLoginWebView2(t, s, "idle")
	before := loginProfilesNow()

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	_, err := w.Login(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ошибка %v, ожидался истёкший срок", err)
	}
	noLoginProfileLeft(t, before)
}

// Скрипт окна входа знает те же хосты и путь, что loginFlow.
func TestLoginJS(t *testing.T) {
	js := loginJS("__binding")
	for _, want := range []string{`"__binding"`, `"oauth.vk.ru"`, `"oauth.vk.com"`,
		`"/blank.html"`, `"#payload="`, "hashchange", "window.top !== window"} {
		if !strings.Contains(js, want) {
			t.Errorf("в скрипте нет %s:\n%s", want, js)
		}
	}
	if strings.Contains(js, "vkhost") {
		t.Errorf("в скрипте сторонний хост:\n%s", js)
	}
}
