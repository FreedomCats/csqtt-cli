// autoclick.go — автоклик галочки капчи: общий ход «автофаза → человек».
//
// На запрос selected (ядро в режиме wv) окно сначала пробует пройти капчу
// само, спрятанным за краем экрана: ждёт галочку, ведёт к ней курсор и
// нажимает — доверенными событиями мыши через DevTools, чтобы страница VK
// записала траекторию в check, как у человека. Исход — по ответу check
// (pageWatch.checked): токен уходит ядру обычным путём, окно так и не
// показывается. Не вышло — слайдер, ошибка, галочки нет, страница не
// загрузилась, VK молчит, — окно выводится человеку, и он решает ту же
// капчу на той же сессии; срок общий, ManualTimeout от начала запроса.
//
// Поведение — как у скрытого WebView Android-клиента CSQTT (журнал
// plan-captcha.md): те же признаки галочки и слайдера, паузы и область
// касания; курсор по кривой — сверх того, у десктопа есть мышь.
//
// Платформе остаётся только autoDriver (методы DevTools страницы) и показ
// окна человеку (autoRun.reveal): WebView2 на Windows. Браузер по CDP на
// Linux (browser_linux.go) пока сразу показывает окно человеку — здесь же
// он получит автоклик (К3).
package captcha

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"strings"
	"sync"
	"time"
)

// autoDriver — страница капчи в окне платформы: методы протокола DevTools
// (Runtime.evaluate, Input.dispatchMouseEvent). Ответ — JSON результата.
type autoDriver interface {
	devtools(ctx context.Context, method string, params any) (json.RawMessage, error)
}

// autoTiming — сроки этапов автофазы, каждый — от начала этапа и на всё, что в
// нём делается, вызовы DevTools тоже: страница, которая перестала отвечать
// (диалог JS, зависший рендерер), не держит автофазу дольше срока этапа.
// Страница капчи открывается за 6–10 с, через kill-switch — до ~22 с
// (журнал); на всё про всё автофаза берёт не больше find + click + reply =
// 50 с из 180, человеку остаётся больше двух минут.
type autoTiming struct {
	// find — сколько от начала ждать галочку: загрузка страницы и
	// initSession.
	find time.Duration
	// click — пауза, путь курсора и нажатие (по расчёту — до ~2,5 с).
	click time.Duration
	// poll — как часто смотреть на страницу.
	poll time.Duration
	// reply — сколько после клика ждать ответа VK на check.
	reply time.Duration
}

var defaultAutoTiming = autoTiming{find: 30 * time.Second, click: 5 * time.Second, poll: 300 * time.Millisecond,
	reply: 15 * time.Second}

// Причины, по которым автофаза отдаёт капчу человеку. Текст — для строки в
// выводе connect: «Автоматически пройти капчу не удалось: <причина>».
var (
	errAutoNotLoaded  = errors.New("страница капчи не загрузилась")
	errAutoNoCheckbox = errors.New("на странице нет галочки")
	errAutoSlider     = errors.New("VK просит собрать слайдер")
	errAutoNoReply    = errors.New("VK не ответил на галочку")
	// errAutoLimit — VK ответил на автоклик ERROR_LIMIT: капчу не решить и
	// человеку, исход (ErrLimit) окно отдаёт само, окно человеку не нужно.
	errAutoLimit = errors.New("VK: лимит попыток капчи исчерпан")
	// errAutoUnavailable — окно не спряталось или не даёт DevTools.
	errAutoUnavailable = errors.New("автоклик в этом окне недоступен")
)

// probeJS — что сейчас на странице капчи: слайдер, галочка (её прямоугольник
// в CSS px окна) или ни того, ни другого. Признаки — как у Android-клиента.
const probeJS = `(function () {
	function box(el) {
		if (!el) { return null; }
		var r = el.getBoundingClientRect(), s = getComputedStyle(el);
		if (r.width < 5 || r.height < 5 || s.visibility === 'hidden' || s.display === 'none' || +s.opacity === 0) {
			return null;
		}
		if (r.right <= 0 || r.bottom <= 0 || r.left >= innerWidth || r.top >= innerHeight) { return null; }
		return {x: r.left, y: r.top, w: r.width, h: r.height};
	}
	// До первой загрузки в окне about:blank — тоже «complete», но это не она.
	var ready = /^https?:$/.test(location.protocol) ? document.readyState : 'blank';
	var out = {ready: ready, visibility: document.visibilityState, vw: innerWidth, vh: innerHeight};
	var sliders = document.querySelectorAll('[class*="SliderCaptcha"],[class*="Kaleidoscope"],.vkc__SwipeButton-module__track');
	for (var i = 0; i < sliders.length; i++) {
		if (box(sliders[i])) { out.state = 'slider'; return out; }
	}
	var sel = ['label.vkc__Checkbox-module__Checkbox', 'label[for="not-robot-captcha-checkbox"]', '#not-robot-captcha-checkbox'];
	for (var j = 0; j < sel.length; j++) {
		var b = box(document.querySelector(sel[j]));
		if (b) { out.state = 'checkbox'; out.box = b; return out; }
	}
	out.state = 'none';
	return out;
})()`

// pageProbe — ответ probeJS.
type pageProbe struct {
	State      string  `json:"state"` // slider, checkbox, none
	Ready      string  `json:"ready"` // document.readyState; blank — страница ещё не открылась
	Visibility string  `json:"visibility"`
	VW         float64 `json:"vw"`
	VH         float64 `json:"vh"`
	Box        *box    `json:"box"`
}

// box — прямоугольник в CSS px окна.
type box struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// point — точка в CSS px окна.
type point struct{ X, Y float64 }

// probe смотрит на страницу.
func probe(ctx context.Context, d autoDriver) (pageProbe, error) {
	raw, err := d.devtools(ctx, "Runtime.evaluate", map[string]any{"expression": probeJS, "returnByValue": true})
	if err != nil {
		return pageProbe{}, err
	}
	var r struct {
		Result struct {
			Value *pageProbe `json:"value"`
		} `json:"result"`
		Exception *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return pageProbe{}, fmt.Errorf("ответ Runtime.evaluate: %w", err)
	}
	if r.Exception != nil {
		return pageProbe{}, fmt.Errorf("скрипт на странице: %s", r.Exception.Text)
	}
	if r.Result.Value == nil {
		return pageProbe{}, errors.New("скрипт на странице ничего не вернул")
	}
	return *r.Result.Value, nil
}

// clickPoint — куда нажать: случайная точка в середине галочки, 15–85 % по
// ширине и 25–75 % по высоте, как касание Android-клиента.
func clickPoint(b box, rnd *rand.Rand) point {
	return point{b.X + b.W*(0.15+0.7*rnd.Float64()), b.Y + b.H*(0.25+0.5*rnd.Float64())}
}

// startPoint — откуда курсор приходит: случайная точка окна не ближе 120 px
// к цели (если окно позволяет).
func startPoint(to point, vw, vh float64, rnd *rand.Rand) point {
	vw, vh = max(vw, 1), max(vh, 1)
	var p point
	for range 16 {
		p = point{vw * (0.05 + 0.9*rnd.Float64()), vh * (0.05 + 0.9*rnd.Float64())}
		if math.Hypot(p.X-to.X, p.Y-to.Y) >= 120 {
			break
		}
	}
	return p
}

// mouseStep — одно движение курсора: куда и когда от начала пути.
type mouseStep struct {
	at   time.Duration
	x, y float64
}

// mousePath — путь курсора от from к to: 10–25 точек за 0,3–0,8 с по кубической
// кривой Безье с боковым прогибом, медленно в начале и в конце, с дрожью в
// пиксель. Последняя точка — ровно to.
func mousePath(from, to point, rnd *rand.Rand) []mouseStep {
	n := 10 + rnd.IntN(16)
	total := 300*time.Millisecond + time.Duration(rnd.Int64N(int64(500*time.Millisecond)))
	dx, dy := to.X-from.X, to.Y-from.Y
	dist := math.Hypot(dx, dy)
	// Нормаль к прямой from→to: по ней прогибаются опорные точки.
	nx, ny := 0.0, 0.0
	if dist > 0 {
		nx, ny = -dy/dist, dx/dist
	}
	bend := func() float64 { return (rnd.Float64()*2 - 1) * 0.3 * dist }
	b1, b2 := bend(), bend()
	c1 := point{from.X + dx*0.3 + nx*b1, from.Y + dy*0.3 + ny*b1}
	c2 := point{from.X + dx*0.7 + nx*b2, from.Y + dy*0.7 + ny*b2}

	steps := make([]mouseStep, n)
	for i := range n {
		f := float64(i+1) / float64(n)
		t := f * f * (3 - 2*f) // плавный разгон и торможение
		u := 1 - t
		x := u*u*u*from.X + 3*u*u*t*c1.X + 3*u*t*t*c2.X + t*t*t*to.X
		y := u*u*u*from.Y + 3*u*u*t*c1.Y + 3*u*t*t*c2.Y + t*t*t*to.Y
		if i < n-1 {
			x += rnd.Float64()*2 - 1
			y += rnd.Float64()*2 - 1
		} else {
			x, y = to.X, to.Y
		}
		steps[i] = mouseStep{at: time.Duration(f * float64(total)), x: x, y: y}
	}
	return steps
}

// between — случайный срок от lo до hi.
func between(rnd *rand.Rand, lo, hi time.Duration) time.Duration {
	return lo + time.Duration(rnd.Int64N(int64(hi-lo)+1))
}

// sleep ждёт d или конца ctx.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// mouse — одно событие мыши через DevTools.
func mouse(ctx context.Context, d autoDriver, kind string, p point, buttons int) error {
	params := map[string]any{"type": kind, "x": p.X, "y": p.Y, "button": "none", "buttons": buttons,
		"pointerType": "mouse"}
	if kind != "mouseMoved" {
		params["button"] = "left"
		params["clickCount"] = 1
	}
	_, err := d.devtools(ctx, "Input.dispatchMouseEvent", params)
	return err
}

// click ведёт курсор к галочке и нажимает: движения по пути, нажатие,
// удержание 80–180 мс, отпускание со сдвигом до пикселя.
func click(ctx context.Context, d autoDriver, b box, vw, vh float64, rnd *rand.Rand) error {
	to := clickPoint(b, rnd)
	path := mousePath(startPoint(to, vw, vh, rnd), to, rnd)
	start := time.Now()
	for _, s := range path {
		if err := sleep(ctx, time.Until(start.Add(s.at))); err != nil {
			return err
		}
		if err := mouse(ctx, d, "mouseMoved", point{s.x, s.y}, 0); err != nil {
			return err
		}
	}
	if err := sleep(ctx, between(rnd, 40*time.Millisecond, 120*time.Millisecond)); err != nil {
		return err
	}
	if err := mouse(ctx, d, "mousePressed", to, 1); err != nil {
		return err
	}
	if err := sleep(ctx, between(rnd, 80*time.Millisecond, 180*time.Millisecond)); err != nil {
		return err
	}
	up := point{to.X + float64(rnd.IntN(3)-1), to.Y + float64(rnd.IntN(3)-1)}
	return mouse(ctx, d, "mouseReleased", up, 0)
}

// autoSolve — сама автофаза: галочка, клик, ответ VK. nil — VK принял галочку
// (токен придёт окну через pageWatch), иначе — причина отдать капчу
// человеку или ошибка ctx.
func autoSolve(ctx context.Context, d autoDriver, checks <-chan checkReply, t autoTiming, rnd *rand.Rand,
	logf func(string, ...any)) error {
	start := time.Now()
	box, err := findCheckbox(ctx, d, t, logf)
	if err != nil {
		return err
	}
	logf("[CAPTCHA] автоклик: галочка найдена через %s, страница %s", time.Since(start).Round(time.Millisecond),
		box.Visibility)

	cctx, cancel := context.WithTimeout(ctx, t.click)
	err = pressCheckbox(cctx, d, box, rnd)
	cancel()
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(err, errAutoSlider):
		return err
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("клик не прошёл: страница не ответила за %s", t.click)
	case err != nil:
		return fmt.Errorf("клик не прошёл: %w", err)
	}
	logf("[CAPTCHA] автоклик: галочка нажата, ждём ответа VK")
	return awaitReply(ctx, d, checks, t)
}

// findCheckbox ждёт галочку до find от начала. Пока страница грузится,
// Runtime.evaluate может и не пройти, а может и не ответить вовсе.
func findCheckbox(ctx context.Context, d autoDriver, t autoTiming, logf func(string, ...any)) (pageProbe, error) {
	fctx, cancel := context.WithTimeout(ctx, t.find)
	defer cancel()
	// Страница без фокуса (окно скрыто и не активно) — подмена: пусть
	// считает себя в фокусе, как у человека. Не вышло — не беда.
	if _, err := d.devtools(fctx, "Emulation.setFocusEmulationEnabled", map[string]any{"enabled": true}); err != nil &&
		fctx.Err() == nil {
		logf("[CAPTCHA] автоклик: фокус страницы не подменить: %v", err)
	}
	var last pageProbe
	var lastErr error
	for fctx.Err() == nil {
		p, err := probe(fctx, d)
		switch {
		case fctx.Err() != nil:
		case err != nil:
			lastErr = err
		case p.State == "slider":
			return p, errAutoSlider
		case p.State == "checkbox" && p.Box != nil:
			return p, nil
		default:
			last, lastErr = p, nil
		}
		_ = sleep(fctx, t.poll)
	}
	switch {
	case ctx.Err() != nil:
		return last, ctx.Err()
	case last.Ready == "complete":
		return last, errAutoNoCheckbox
	case lastErr != nil:
		logf("[CAPTCHA] автоклик: страница не отвечает: %v", lastErr)
	}
	return last, fmt.Errorf("%w за %s", errAutoNotLoaded, t.find)
}

// pressCheckbox — пауза человека, галочка заново (за паузу вёрстка могла
// сдвинуться) и клик.
func pressCheckbox(ctx context.Context, d autoDriver, last pageProbe, rnd *rand.Rand) error {
	if err := sleep(ctx, between(rnd, 650*time.Millisecond, 1200*time.Millisecond)); err != nil {
		return err
	}
	p, err := probe(ctx, d)
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case err != nil:
	case p.State == "slider":
		return errAutoSlider
	case p.State == "checkbox" && p.Box != nil:
		last = p
	}
	return click(ctx, d, *last.Box, last.VW, last.VH, rnd)
}

// awaitReply ждёт ответа VK на check до reply; заодно — не появился ли
// слайдер (ответ мог пройти мимо перехвата).
func awaitReply(ctx context.Context, d autoDriver, checks <-chan checkReply, t autoTiming) error {
	rctx, cancel := context.WithTimeout(ctx, t.reply)
	defer cancel()
	tick := time.NewTicker(t.poll)
	defer tick.Stop()
	for {
		select {
		case r := <-checks:
			return checkVerdict(r)
		case <-tick.C:
			if p, err := probe(rctx, d); err == nil && p.State == "slider" {
				return errAutoSlider
			}
		case <-rctx.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("%w за %s", errAutoNoReply, t.reply)
		}
	}
}

// checkVerdict — что значит ответ VK на автоклик: nil — галочку приняли.
func checkVerdict(r checkReply) error {
	switch {
	case r.vkError != 0:
		return fmt.Errorf("VK ответил на галочку ошибкой %d: %s", r.vkError, r.vkMsg)
	case r.token:
		return nil
	case strings.EqualFold(r.status, statusLimit):
		return errAutoLimit
	case r.showType == "slider":
		return errAutoSlider
	case r.status == "":
		return errors.New("VK ответил на галочку без статуса")
	}
	return fmt.Errorf("VK не поверил галочке (%s)", r.status)
}

// humanHint — подсказка, где искать окно, если его не видно.
const humanHint = "Если окна не видно, оно может оказаться за консолью: поищите его на панели задач."

// autoRun — автофаза одного окна и что из неё видит человек: строка о начале,
// при неудаче — строка с причиной и окно на экране, при успехе — строка, что
// капча пройдена сама.
type autoRun struct {
	out    io.Writer
	logf   func(string, ...any)
	req    Request
	timing autoTiming
	// reveal показывает окно человеку (платформа). Зовётся не больше раза.
	reveal func()

	mu       sync.Mutex
	revealed bool
}

func newAutoRun(out io.Writer, logf func(string, ...any), req Request, reveal func()) *autoRun {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &autoRun{out: out, logf: logf, req: req, timing: defaultAutoTiming, reveal: reveal}
}

// announce — строка о том, что капча пробуется автоматически.
func (a *autoRun) announce() {
	fmt.Fprintln(a.out, "\nVK просит решить капчу — пробую пройти её автоматически.")
}

// run — автофаза на странице d; неудача — окно человеку. checks — ответы на
// check (pageWatch.checked). Возвращается, когда автофаза кончилась.
func (a *autoRun) run(ctx context.Context, d autoDriver, checks <-chan checkReply) {
	rnd := rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
	err := autoSolve(ctx, d, checks, a.timing, rnd, a.logf)
	switch {
	case err == nil:
		a.logf("[CAPTCHA] автоклик: VK принял галочку")
		return
	case errors.Is(err, errAutoLimit):
		// Решать человеку нечего: исход ErrLimit окно отдаёт само.
		a.logf("[CAPTCHA] автоклик: %v", err)
		return
	case ctx.Err() != nil:
		return
	}
	a.fail(err)
}

// fail отдаёт капчу человеку: строка с причиной и окно на экране.
func (a *autoRun) fail(err error) {
	a.mu.Lock()
	if a.revealed {
		a.mu.Unlock()
		return
	}
	a.revealed = true
	a.mu.Unlock()
	a.logf("[CAPTCHA] автоклик не прошёл: %v — окно человеку", err)
	fmt.Fprintf(a.out, "\nАвтоматически пройти капчу не удалось: %v — решите её в окне, время на это %s.\n%s\n",
		err, a.req.Left(time.Now()).Round(time.Second), humanHint)
	a.reveal()
}

// solved — токен получен: если человеку окно не показывали, сказать, что
// капча пройдена автоматически.
func (a *autoRun) solved() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.revealed {
		fmt.Fprintln(a.out, "\nКапча пройдена автоматически.")
	}
}
