//go:build windows

package captcha

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	webview2 "github.com/jchv/go-webview2"
)

// Автоклик в окне WebView2 на стенде (CSQTT_WEBVIEW2=1). Окно с автокликом
// стоит за краем экрана; тесты, где оно выходит человеку, показывают его без
// переднего плана (quietReveal) — фокус у того, кто за машиной, не пропадает.

// Out окна — syncBuffer (netfilter_test.go): в него пишет и Solve, и
// автофаза из своей горутины.

// watchHidden следит, чтобы окно создалось за краем экрана.
func watchHidden(t *testing.T, s *WebView2) {
	s.hidden = func(hw *hiddenWindow) {
		hiddenMu.Lock()
		where := hw.where
		hiddenMu.Unlock()
		if hw.hwnd == 0 || onScreen(where) || onScreen(windowRect(hw.hwnd)) {
			t.Errorf("окно автоклика на экране: создано в %+v, сейчас %+v", where, windowRect(hw.hwnd))
		}
		if ex, _, _ := procGetWindowLongPtrW.Call(hw.hwnd, uintptr(gwlExStyle)); ex&wsExToolWindow == 0 {
			t.Errorf("у скрытого окна будет кнопка на панели задач: стиль %#x", ex)
		}
	}
}

// Окно за краем экрана: доверенный клик с движениями мыши, страница видна и
// рисуется — и сразу, и спустя десять секунд за краем (slow: галочка
// появляется позже; Chromium считает перекрытие окон не сразу). Галочка
// находится и без признаков VK: спрятанный флажок в label, роль в теневом
// корне, флажок во фрейме, один текст.
func TestWebView2AutoClick(t *testing.T) {
	s := newStandWebView2(t)
	srv := newStand(t)
	outer := t

	for _, tc := range []struct{ name, scenario, extra, found string }{
		{"fast", "auto", "", "признак vk, document"},
		{"slow", "auto", "&boxdelay=10000", "признак vk, document"},
		{"plain", "autoplain", "", "признак input, document"},
		{"shadow", "autoshadow", "", "признак role, shadow"},
		{"frame", "autoframe", "", "признак input, frame"},
		{"text", "autotext", "", "признак text, document"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out syncBuffer
			var log logLines
			s.opts.Out = &out
			s.opts.Logf = func(format string, args ...any) {
				log.logf(format, args...)
				outer.Logf(format, args...)
			}
			watchHidden(t, s)
			s.revealed = func(webview2.WebView) {
				t.Error("окно показано человеку, хотя автоклик должен был пройти")
			}
			before := profilesNow()

			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			start := time.Now()
			token, err := s.Solve(ctx, standAutoRequest(srv.page(tc.scenario, "fetch")+tc.extra, 40*time.Second))
			if err != nil {
				t.Fatalf("Solve: %v\nвывод:%s", err, out.String())
			}
			if want := standToken(tc.scenario, "fetch"); token != want {
				t.Fatalf("токен %q, ожидался %q", token, want)
			}
			if !strings.Contains(log.text(), tc.found) {
				t.Errorf("в журнале нет «%s»:\n%s", tc.found, log.text())
			}
			t.Logf("токен за %s", time.Since(start).Round(time.Millisecond))

			checks := srv.checkQueries()
			q := checks[len(checks)-1]
			t.Logf("check: trusted=%s moves=%s vis=%s fps=%s focus=%s", q.Get("trusted"), q.Get("moves"),
				q.Get("vis"), q.Get("fps"), q.Get("focus"))
			if q.Get("trusted") != "true" {
				t.Errorf("клик недоверенный: isTrusted=%s", q.Get("trusted"))
			}
			if n, _ := strconv.Atoi(q.Get("moves")); n < 10 {
				t.Errorf("движений мыши до клика %d, ожидалось не меньше 10", n)
			}
			if q.Get("vis") != "visible" {
				t.Errorf("страница за краем экрана считает себя %q", q.Get("vis"))
			}
			if n, _ := strconv.Atoi(q.Get("fps")); n < 10 {
				t.Errorf("кадров за секунду до клика %d: страница не рисуется", n)
			}
			if text := out.String(); !strings.Contains(text, "пройдена автоматически") ||
				strings.Contains(text, "не удалось") {
				t.Errorf("вывод:%s", text)
			}
			waitProfilesGone(t, before)
		})
	}
}

// Автоклик не прошёл — окно выходит человеку со строкой в выводе, человек
// решает ту же капчу в том же окне.
//
// При этом описание страницы — в журнал, без запроса адреса, а с dumpEnv —
// вся страница в файл (autonone: с теневым корнем, без запроса фрейма).
func TestWebView2AutoFallback(t *testing.T) {
	s := newStandWebView2(t)
	srv := newStand(t)
	outer := t
	s.quietReveal = true
	s.autoTiming = &autoTiming{find: 6 * time.Second, click: defaultAutoTiming.click, poll: defaultAutoTiming.poll,
		reply: 5 * time.Second}

	for _, tc := range []struct{ scenario, reason string }{
		{"autobot", "слайдер"},
		{"autonone", "нет галочки"},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			var out syncBuffer
			var log logLines
			s.opts.Out = &out
			s.opts.Logf = func(format string, args ...any) {
				log.logf(format, args...)
				outer.Logf(format, args...)
			}
			dump := filepath.Join(t.TempDir(), "page.html")
			t.Setenv(dumpEnv, dump)
			watchHidden(t, s)
			var revealed sync.WaitGroup
			revealed.Add(1)
			s.revealed = func(w webview2.WebView) {
				defer revealed.Done()
				if r := windowRect(uintptr(w.Window())); !onScreen(r) {
					t.Errorf("окно показано человеку, но не на экране: %+v", r)
				}
				w.Eval("standHuman()")
			}
			before := profilesNow()

			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			token, err := s.Solve(ctx, standAutoRequest(srv.page(tc.scenario, "fetch"), 40*time.Second))
			if err != nil {
				t.Fatalf("Solve: %v\nвывод:%s", err, out.String())
			}
			if want := standToken(tc.scenario, "fetch"); token != want {
				t.Fatalf("токен %q, ожидался %q", token, want)
			}
			revealed.Wait()
			text := out.String()
			if !strings.Contains(text, "Автоматически пройти капчу не удалось: ") ||
				!strings.Contains(text, tc.reason) || strings.Contains(text, "пройдена автоматически") {
				t.Errorf("вывод:%s", text)
			}
			if got := srv.checkQueries(); len(got) == 0 || got[len(got)-1].Get("human") != "1" {
				t.Errorf("последний check — не от человека: %v", got)
			}
			journal := log.text()
			if !strings.Contains(journal, "страница при неудаче автоклика: "+srv.URL+"/not_robot_captcha,") {
				t.Errorf("в журнале нет описания страницы:\n%s", journal)
			}
			for _, bad := range []string{"session_token", "scenario=", "decoy"} {
				if strings.Contains(journal, bad) {
					t.Errorf("в журнале %q:\n%s", bad, journal)
				}
			}
			page, err := os.ReadFile(dump)
			if err != nil {
				t.Fatalf("страница не записана: %v", err)
			}
			if strings.Contains(string(page), "=decoy") || !strings.Contains(string(page), "standHuman") {
				t.Errorf("записанная страница:\n%s", page)
			}
			if tc.scenario == "autonone" {
				foreign := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1) + "/empty "
				for _, want := range []string{"теневых корней 1",
					"фрейм чужого происхождения — внутрь не заглянуть: " + foreign, "button/shadow: button «Обновить»"} {
					if !strings.Contains(journal, want) {
						t.Errorf("в журнале нет %q:\n%s", want, journal)
					}
				}
				if !strings.Contains(string(page), `<template shadowrootmode="open"><button>Обновить</button>`) {
					t.Errorf("теневой корень не записан:\n%s", page)
				}
			}
			waitProfilesGone(t, before)
		})
	}
}

// Срок вышел, пока идёт автофаза: окно закрывается, человеку не показывается.
func TestWebView2AutoDeadline(t *testing.T) {
	s := newStandWebView2(t)
	srv := newStand(t)
	watchHidden(t, s)
	s.revealed = func(webview2.WebView) { t.Error("окно показано человеку после срока") }
	before := profilesNow()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := s.Solve(ctx, standAutoRequest(srv.page("autonone", "fetch"), 3*time.Second))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ошибка %v, ожидался истёкший срок", err)
	}
	waitProfilesGone(t, before)
}

// Автоклик через прокси csqtt-cli (kill-switch): аргументы браузера прокси и
// скрытого окна уходят вместе.
func TestWebView2AutoProxy(t *testing.T) {
	s := newStandWebView2(t)
	srv := newTLSStand(t)
	t.Setenv(browserArgsEnv, "--ignore-certificate-errors")
	watchHidden(t, s)
	s.revealed = func(webview2.WebView) { t.Error("окно показано человеку") }
	var mu sync.Mutex
	var asked []string
	s.opts.Dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		mu.Lock()
		asked = append(asked, address)
		mu.Unlock()
		var d net.Dialer
		return d.DialContext(ctx, "tcp", srv.Listener.Addr().String())
	}
	before := profilesNow()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	page := "https://id.vk.com/not_robot_captcha?session_token=stand&scenario=auto&via=fetch"
	token, err := s.Solve(ctx, standAutoRequest(page, 30*time.Second))
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if token != standToken("auto", "fetch") {
		t.Fatalf("токен %q, ожидался со стенда", token)
	}
	if q := srv.checkQueries(); len(q) != 1 || q[0].Get("trusted") != "true" || q[0].Get("vis") != "visible" {
		t.Errorf("check: %v", q)
	}
	mu.Lock()
	if len(asked) == 0 || asked[0] != "id.vk.com:443" {
		t.Errorf("через прокси: %q", asked)
	}
	mu.Unlock()
	if got := os.Getenv(browserArgsEnv); got != "--ignore-certificate-errors" {
		t.Errorf("%s после окна = %q", browserArgsEnv, got)
	}
	// Окно и его прокси живут чуть дольше Solve (endSession): дождаться, пока
	// закроются, — иначе прокси пишет в журнал теста, которого уже нет.
	waitProfilesGone(t, before)
}
