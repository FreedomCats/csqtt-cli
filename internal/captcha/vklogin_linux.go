// vklogin_linux.go — окно входа VK на Linux: браузер на Chromium под
// управлением по CDP, тот же, что у капчи (browser_linux.go), со своим
// временным профилем. Разговор с ним — vklogin_cdp.go.
package captcha

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
)

// Login открывает окно со страницей входа VK и ждёт токена.
//
// В отличие от капчи, уборка всегда дожидается: в профиле после входа —
// cookie сессии VK, и вход не возвращается, пока профиль не убран. Не
// убрался — Login говорит об этом в Out.
func (b *Browser) Login(ctx context.Context) (VKToken, error) {
	authURL := b.authURL
	if authURL == "" {
		authURL = VKAuthURL
	}

	netw, err := startLoginNet(b.opts.Net, b.opts.Dial, b.opts.Out, b.opts.Logf)
	if err != nil {
		return VKToken{}, fmt.Errorf("%w: %v", ErrLoginBrowser, err)
	}
	profile, err := newProfile(loginProfilePrefix)
	if err != nil {
		netw.close()
		return VKToken{}, fmt.Errorf("%w: каталог профиля: %v", ErrLoginBrowser, err)
	}
	browser, err := b.start(insertArgs(browserArgs(profile, "", loginWindowSize), netw.browserArgs()...))
	if err != nil {
		netw.close()
		removeProfile(profile)
		return VKToken{}, fmt.Errorf("%w: %v", ErrLoginBrowser, err)
	}

	fmt.Fprintf(netw.writer(b.opts.Out), "Открыто окно браузера %s со страницей входа VK.\n", filepath.Base(b.exe))

	conv := newCDPLogin(authURL, b.opts.Logf)
	_, err = converse(ctx, browser.out, browser.in, conv, browserStartup)

	browser.stop()
	netw.close()
	removeProfile(profile)
	warnProfileLeft(b.opts.Out, profile)

	var lost *browserLost
	switch {
	case errors.As(err, &lost):
		return VKToken{}, fmt.Errorf("%w: %v", ErrLoginBrowser, lost.cause)
	case errors.Is(err, ErrWindowClosed):
		return VKToken{}, ErrLoginClosed
	case err != nil:
		return VKToken{}, err
	}
	return conv.result, nil
}
