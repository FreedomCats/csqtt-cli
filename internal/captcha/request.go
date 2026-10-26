// Package captcha — капча VK: окно, в котором её решает человек, и мост от
// запроса ядра к этому окну (§08 doc/architecture.md).
//
// Ядро печатает CAPTCHA_SOLVE|<режим>|<redirect_uri>|<session_token> и ждёт
// CAPTCHA_RESULT. Bridge отвечает на auto отказом, а на manual и selected
// открывает окно: WebView2 на Windows, браузер на Chromium под управлением по
// CDP на Linux, иначе — ручной режим в консоли. Токен окно снимает с ответа
// API страницы капчи (page.go), не трогая саму страницу. На selected окно
// WebView2 сначала само нажимает галочку, спрятанным за краем экрана, и
// выходит человеку при неудаче (autoclick.go).
package captcha

import (
	"errors"
	"time"
)

// Режимы запроса капчи (captcha.rs): auto — десятисекундный автопрогон в
// цепочке ядра, manual — ручное окно в её конце, selected — окно режима wv
// (coreproc.CaptchaModeWebView), с которым оболочка запускает ядро: оно
// приходит сразу, без цепочки. Окно на manual и selected — одно и то же, но
// на selected окно WebView2 сначала пробует автоклик (autoclick.go).
const (
	ModeAuto     = "auto"
	ModeManual   = "manual"
	ModeSelected = "selected"
)

// ManualTimeout — сколько живёт окно капчи (manual и selected): столько же
// ждёт ядро (правка П5). Конца окна ядро оболочке не сообщает, поэтому у
// окна свой таймер.
const ManualTimeout = 180 * time.Second

// windowMode — на запрос в этом режиме мост открывает окно.
func windowMode(mode string) bool { return mode == ModeManual || mode == ModeSelected }

// Request — что нужно окну, чтобы показать капчу.
type Request struct {
	Mode         string
	RedirectURI  string
	SessionToken string
	// Deadline — когда ядро перестанет ждать ответа.
	Deadline time.Time
}

// Left — сколько осталось до Deadline; без срока — ManualTimeout.
func (r Request) Left(now time.Time) time.Duration {
	if r.Deadline.IsZero() {
		return ManualTimeout
	}
	return max(r.Deadline.Sub(now), 0)
}

var (
	// ErrWindowClosed — окно капчи закрыл человек.
	ErrWindowClosed = errors.New("окно капчи закрыто")
	// ErrLimit — VK ответил на проверку ERROR_LIMIT: попытки на эту сессию
	// капчи кончились, в окне её уже не решить («Лимит попыток исчерпан»).
	ErrLimit = errors.New("VK: лимит попыток капчи исчерпан")
	// ErrPageInit — VK ответил ошибкой на initSession: капчи на странице не
	// будет, страница показывает ошибку, и человеку в окне делать нечего.
	ErrPageInit = errors.New("VK не открыл капчу")
	// ErrNoToken — исход есть, а токена в нём нет.
	ErrNoToken = errors.New("капча решена, но токена в ответе VK нет")
)

// outcome — исход окна: токен или ошибка.
type outcome struct {
	token string
	err   error
}
