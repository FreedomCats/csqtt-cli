package captcha

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Окно входа на стенде OAuth (vklogin_stand_test.go) с настоящим браузером:
// как тесты окна капчи, идут в обычном прогоне, если браузер на Chromium
// установлен, и без окна (--headless=new), если нет графического сеанса.

// newLoginBrowser — браузер окна входа, который видит стенд вместо VK.
func newLoginBrowser(t *testing.T, s *loginStand, scenario string) (*Browser, *bytes.Buffer, *logBuffer) {
	t.Helper()
	b := newBrowserStand(t)
	out := &bytes.Buffer{}
	log := &logBuffer{t: t}
	b.opts.Out = out
	b.opts.Logf = log.logf
	b.extraArgs = append(b.extraArgs, s.browserArgs(t)...)
	b.authURL = s.authURL(scenario)
	return b, out, log
}

func TestBrowserLoginToken(t *testing.T) {
	for _, tc := range []struct {
		scenario string
		logins   int // сколько раз открывалась страница входа
	}{
		{"redirect", 1}, {"js", 1}, {"query", 1}, {"expiring", 2}, {"silent", 2},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			s := newLoginStand(t)
			b, out, log := newLoginBrowser(t, s, tc.scenario)
			before := loginProfilesNow()

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			start := time.Now()
			tok, err := b.Login(ctx)
			if err != nil {
				t.Fatalf("Login: %v", err)
			}
			t.Logf("токен за %s", time.Since(start).Round(time.Millisecond))

			want := standVKToken(tc.scenario, tc.logins)
			if tok.AccessToken != want || tok.UserID != standUserID || tok.ExpiresIn != 0 {
				t.Fatalf("итог входа: user_id %q, expires_in %d, токен ожидаемый: %v",
					tok.UserID, tok.ExpiresIn, tok.AccessToken == want)
			}
			authorized, blank := s.hits(tc.scenario)
			if authorized != tc.logins {
				t.Errorf("страница входа открыта %d раз, ожидалось %d", authorized, tc.logins)
			}
			// blank.html отвечает заглушка окна: к «VK» запрос не уходит.
			if blank != 0 {
				t.Errorf("запросов blank.html дошло до стенда: %d", blank)
			}
			for n := 1; n <= tc.logins; n++ {
				for where, text := range map[string]string{"журнал": log.String(), "вывод": out.String()} {
					if strings.Contains(text, standVKToken(tc.scenario, n)) {
						t.Errorf("%s: в нём токен:\n%s", where, text)
					}
				}
			}
			noLoginProfileLeft(t, before)
		})
	}
}

func TestBrowserLoginFailures(t *testing.T) {
	for _, tc := range []struct {
		scenario string
		check    func(error) bool
	}{
		{"denied", func(err error) bool {
			var e *VKAuthError
			return errors.As(err, &e) && e.Code == "access_denied" &&
				strings.Contains(err.Error(), "User denied your request")
		}},
		{"expiring-always", func(err error) bool { return errors.Is(err, ErrLoginExpiring) }},
		{"notoken", func(err error) bool { return errors.Is(err, ErrLoginNoToken) }},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			s := newLoginStand(t)
			b, _, log := newLoginBrowser(t, s, tc.scenario)
			before := loginProfilesNow()

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, err := b.Login(ctx)
			if !tc.check(err) {
				t.Fatalf("ошибка %v", err)
			}
			if strings.Contains(log.String(), "stand-vk-token") {
				t.Errorf("в журнале токен:\n%s", log)
			}
			noLoginProfileLeft(t, before)
		})
	}
}

// Человек закрыл окно — ErrLoginClosed, а не срок. Здесь окно закрывает
// SIGTERM: Chromium выходит так же, как с последним окном.
func TestBrowserLoginClosed(t *testing.T) {
	s := newLoginStand(t)
	b, _, _ := newLoginBrowser(t, s, "idle")
	b.started = func(pid int) {
		time.AfterFunc(3*time.Second, func() { _ = syscall.Kill(pid, syscall.SIGTERM) })
	}
	before := loginProfilesNow()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := b.Login(ctx)
	if !errors.Is(err, ErrLoginClosed) {
		t.Fatalf("ошибка %v, ожидалось «окно входа закрыто»", err)
	}
	if ctx.Err() != nil {
		t.Fatal("Login дождался срока, а не закрытия окна")
	}
	noLoginProfileLeft(t, before)
}

// Срок или Ctrl+C: браузер закрыт, профиль убран, пока Login не вернулся.
func TestBrowserLoginDeadline(t *testing.T) {
	s := newLoginStand(t)
	b, _, _ := newLoginBrowser(t, s, "idle")
	var pid int
	b.started = func(p int) { pid = p }
	before := loginProfilesNow()

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	start := time.Now()
	_, err := b.Login(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ошибка %v, ожидался истёкший срок", err)
	}
	if took := time.Since(start); took > 4*time.Second+browserStop+time.Second {
		t.Fatalf("Login вернулся через %s", took)
	}
	if pid != 0 && syscall.Kill(pid, 0) == nil {
		t.Errorf("браузер %d жив после Login", pid)
	}
	noLoginProfileLeft(t, before)
}

// Браузер не запустился — ErrLoginBrowser, и профиль убран.
func TestBrowserLoginStartFailure(t *testing.T) {
	b := newBrowserStand(t)
	b.exe = "/bin/false"
	b.opts.Out = &bytes.Buffer{}
	before := loginProfilesNow()

	_, err := b.Login(context.Background())
	if !errors.Is(err, ErrLoginBrowser) {
		t.Fatalf("ошибка %v, ожидалась ErrLoginBrowser", err)
	}
	noLoginProfileLeft(t, before)
}
