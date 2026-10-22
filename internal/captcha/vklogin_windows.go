//go:build windows

// vklogin_windows.go — окно входа VK на WebView2.
//
// Перехватить переход до сети, как окно по CDP, go-webview2 не даёт: события
// NavigationStarting в нём нет. Зато скрипт, который окно выполняет на каждой
// новой странице до её собственного кода (Init), видит адрес целиком —
// location.href с фрагментом, — и отдаёт его в Go через Bind. Так фрагмент
// виден при любом пути VK к blank.html: редиректом сервера, переходом из JS,
// сменой фрагмента. Скрипт только читает адрес и только на blank.html (или
// при payload= во фрагменте); на страницах VK он больше ничего не делает.
// Сама blank.html VK при этом грузится из сети, но фрагмент в запрос не
// попадает никогда.
package captcha

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"time"

	webview2 "github.com/jchv/go-webview2"
)

// loginBindingName — глобальная функция, через которую скрипт окна входа
// отдаёт адрес в Go.
const loginBindingName = "__csqttCliVKLogin"

// loginJS — скрипт окна входа: на blank.html (и при payload= во фрагменте)
// отдать адрес в Go — сразу и при смене фрагмента. Только в главном фрейме.
func loginJS(binding string) string {
	hosts, _ := json.Marshal(payloadHosts)
	name, _ := json.Marshal(binding)
	path, _ := json.Marshal(blankPath)
	return `(function () {
	if (window.top !== window) { return; }
	var hosts = ` + string(hosts) + `, name = ` + string(name) + `, path = ` + string(path) + `;
	function report() {
		var l = window.location;
		if (hosts.indexOf(l.hostname) < 0) { return; }
		if (l.pathname !== path && l.hash.indexOf("#payload=") !== 0) { return; }
		var f = window[name];
		if (typeof f === "function") { f(l.href); }
	}
	report();
	window.addEventListener("hashchange", report);
})();`
}

// Login открывает окно со страницей входа VK и ждёт токена.
//
// Возвращается, только когда окно закрыто и профиль убран: в профиле после
// входа — cookie сессии VK. Не убрался (msedgewebview2.exe держит файлы
// дольше обычного) — Login говорит об этом в Out.
func (s *WebView2) Login(ctx context.Context) (VKToken, error) {
	authURL := s.authURL
	if authURL == "" {
		authURL = VKAuthURL
	}

	profile, err := os.MkdirTemp("", loginProfilePrefix+"*")
	if err != nil {
		return VKToken{}, fmt.Errorf("%w: каталог профиля WebView2: %v", ErrLoginBrowser, err)
	}

	result := make(chan loginStep, 1)
	report := func(st loginStep) {
		select {
		case result <- st:
		default:
		}
	}

	win := &windowHandle{}
	done := make(chan struct{})

	go func() {
		// Окно, его насос сообщений и COM живут на одной прибитой нити — как
		// у окна капчи (webview2_windows.go).
		runtime.LockOSThread()
		defer close(done)

		if err := initCOM(); err != nil {
			report(loginStep{action: loginFail, err: fmt.Errorf("%w: %v", ErrLoginBrowser, err)})
			return
		}

		var browserArgs string
		var restoreArgs func()
		if s.extraArgs != "" {
			browserArgs, restoreArgs = setBrowserArgs(s.extraArgs)
		}
		w := webview2.NewWithOptions(webview2.WebViewOptions{
			DataPath:                   profile,
			AdditionalBrowserArguments: browserArgs,
			WindowOptions: webview2.WindowOptions{
				Title:  "Вход VK — csqtt-cli",
				Width:  820,
				Height: 720,
				Center: true,
			},
		})
		if restoreArgs != nil {
			restoreArgs()
		}
		if w == nil {
			report(loginStep{action: loginFail, err: fmt.Errorf("%w: окно WebView2 не открылось", ErrLoginBrowser)})
			return
		}
		win.set(w)
		defer win.clear()

		// Bind зовёт функцию на нити окна, таймер повтора — на своей, и
		// повтор уходит на нить окна через dispatch: flow живёт только там.
		flow := newLoginFlow(s.opts.Logf)
		_ = w.Bind(loginBindingName, func(href string) {
			st := flow.observe(href, true)
			switch st.action {
			case loginDone, loginFail:
				report(st)
				w.Destroy()
			case loginRetry:
				if st.delay <= 0 {
					w.Navigate(authURL)
					return
				}
				time.AfterFunc(st.delay, func() {
					win.dispatch(func(w webview2.WebView) { w.Navigate(authURL) })
				})
			}
		})
		w.Init(loginJS(loginBindingName))
		w.Navigate(authURL)

		if s.opened != nil {
			s.opened(w)
		}
		w.Run()
	}()

	stop := context.AfterFunc(ctx, win.close)
	defer stop()

	fmt.Fprint(s.opts.Out, "Открыто окно со страницей входа VK.\n"+
		"Если окна не видно, оно может оказаться за консолью: поищите его на панели задач.\n")

	var st loginStep
	select {
	case st = <-result:
	case <-done:
		// Исход и закрытие могли случиться одновременно: исход важнее.
		select {
		case st = <-result:
		default:
			st = loginStep{action: loginFail, err: ErrLoginClosed}
			if ctx.Err() != nil {
				// Окно закрыла отмена, а не человек.
				st.err = ctx.Err()
			}
		}
	case <-ctx.Done():
		st = loginStep{action: loginFail, err: ctx.Err()}
	}

	// Профиль убирается, когда окно закрыто, и не раньше: до этого его файлы
	// держит msedgewebview2.exe.
	win.close()
	<-done
	removeProfile(profile)
	warnProfileLeft(s.opts.Out, profile)

	if st.action != loginDone {
		return VKToken{}, st.err
	}
	return st.token, nil
}
