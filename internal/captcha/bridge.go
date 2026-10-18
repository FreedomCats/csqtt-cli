// bridge.go — мост от запроса капчи ядра к окну (supervisor.CaptchaFunc).
//
// Ядро отдаёт запросы по одному: следующий CAPTCHA_SOLVE оно печатает, только
// когда прежний решён или его время вышло (канал результата в captcha.rs один
// и стоит под замком). Отсюда правила моста:
//
//   - окно одно. Новый запрос значит, что прежний ядро уже не ждёт: прежнее
//     окно закрывается, и на прежний запрос ядру не отвечают ничем — ответ
//     на него ядро приняло бы за ответ на текущий;
//   - запрос с тем же redirect_uri, пока окно открыто, второго окна не
//     открывает: окно переходит к новому запросу, и его время считается
//     заново;
//   - время окна — ManualTimeout, своим таймером: ядро о конце окна не
//     сообщает. Вышло — окно закрывается, ядру не отвечают: оно ждало столько
//     же и уже перестало;
//   - auto — сразу отказ unsupported. Автопрогона в окне на десктопе пока нет,
//     ждать десять секунд незачем.
//
// Решатель ядра при этом выключен (coreproc.CoreSolverOffEnv): его попытки
// портят сессию капчи раньше, чем она дойдёт до окна.
package captcha

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/FreedomCats/csqtt-cli/internal/coreproc"
)

// Window — окно, в котором человек решает капчу: WebView2, браузер под
// управлением или ручной режим. Solve возвращает токен, ErrWindowClosed,
// ErrLimit, ErrPageInit, ErrBrowser (окно не открылось — можно решить ту же капчу
// вручную) или ошибку ctx.
type Window interface {
	Solve(ctx context.Context, req Request) (string, error)
}

// Options — настройки моста.
type Options struct {
	// Out — куда пишутся строки для человека: что открыто окно, что вышло
	// время. nil — os.Stderr.
	Out io.Writer

	// In — откуда ручной режим читает ответ. nil — os.Stdin.
	In io.Reader

	// Open — открыть ссылку в браузере системы для ручного режима; nil —
	// только напечатать её (ManualOptions.Open).
	Open func(rawURL string) error

	// Dial — окно ходит в сеть через локальный прокси, а он — этой функцией
	// (proxy.go). nil — окно ходит в сеть само.
	Dial DialFunc

	// Logf — журнал: что происходит на странице капчи и с окном. Токена в нём
	// нет. nil — молчать.
	Logf func(format string, args ...any)

	// Timeout — время окна; 0 — ManualTimeout.
	Timeout time.Duration
}

// Bridge отвечает на запросы капчи ядра окном (Solve — supervisor.CaptchaFunc).
type Bridge struct {
	opts   Options
	window Window // окно платформы; nil — сразу ручной режим
	manual *Manual

	mu  sync.Mutex
	cur *session
	seq uint64
}

// session — одно открытое окно.
type session struct {
	uri    string
	cancel context.CancelCauseFunc
	timer  *time.Timer
	done   chan struct{}

	// Под Bridge.mu.
	owner uint64        // какой вызов Solve ответит ядру
	moved chan struct{} // закрывается, когда окно уходит от owner

	// Пишутся до close(done).
	token string
	err   error
}

// Причины, по которым мост сам закрывает окно.
var (
	errTimeout    = errors.New("время на капчу вышло")
	errSuperseded = errors.New("ядро прислало новый запрос капчи")
)

// NewBridge выбирает окно для этой системы: WebView2 на Windows, браузер на
// Chromium по CDP на Linux. Нет ни того, ни другого — ручной режим; он же
// запасной, если окно не откроется.
func NewBridge(opts Options) *Bridge {
	if opts.Out == nil {
		opts.Out = os.Stderr
	}
	if opts.Timeout <= 0 {
		opts.Timeout = ManualTimeout
	}
	b := &Bridge{
		opts:   opts,
		manual: NewManual(ManualOptions{In: opts.In, Out: opts.Out, Open: opts.Open}),
	}
	w, err := platformWindow(opts)
	switch {
	case err == nil:
		b.window = w
	case !errors.Is(err, errors.ErrUnsupported):
		b.logf("[CAPTCHA] окна капчи не будет, только ручной режим: %v", err)
	}
	return b
}

// newBridgeWith — мост с заданным окном (для тестов).
func newBridgeWith(opts Options, w Window) *Bridge {
	b := NewBridge(opts)
	b.window = w
	return b
}

// Solve решает капчу по запросу ядра; подходит как supervisor.CaptchaFunc.
//
// Отказы для ядра — *coreproc.CaptchaError с причиной: unsupported (auto),
// closed (окно закрыто), limit (ERROR_LIMIT), init (initSession с ошибкой),
// cancelled (ручной режим без ответа), failed (прочее). Время окна вышло или пришёл новый запрос —
// ошибка с coreproc.ErrCaptchaNoReply: отвечать ядру не нужно.
func (b *Bridge) Solve(ctx context.Context, creq coreproc.CaptchaRequest) (string, error) {
	if creq.Mode != ModeManual {
		b.mu.Lock()
		b.supersede()
		b.mu.Unlock()
		b.logf("[CAPTCHA] запрос %s: автопрогона в окне нет — отказ", creq.Mode)
		return "", &coreproc.CaptchaError{Reason: "unsupported",
			Err: fmt.Errorf("капча в режиме %q не поддерживается: её решает только человек в окне", creq.Mode)}
	}

	b.mu.Lock()
	b.seq++
	me := b.seq
	s := b.cur
	// Stop — false, если время окна уже вышло и окно закрывается: тогда
	// нужно новое.
	if s != nil && s.uri == creq.RedirectURI && !s.finished() && s.timer.Stop() {
		// Та же капча, окно ещё открыто: оно отвечает теперь на этот запрос.
		s.handOver(me)
		s.timer.Reset(b.opts.Timeout)
		b.logf("[CAPTCHA] повторный запрос той же капчи — окно остаётся, время отсчитывается заново")
	} else {
		b.supersede()
		s = b.open(ctx, creq, me)
		b.cur = s
	}
	moved := s.moved
	b.mu.Unlock()

	select {
	case <-s.done:
	case <-moved:
		// Прежний вызов отпускается сразу, не дожидаясь окна.
		return "", fmt.Errorf("окно перешло к новому запросу: %w", coreproc.ErrCaptchaNoReply)
	case <-ctx.Done():
		// Окно закроется само: его ctx — от ctx первого запроса, а это ctx
		// той же попытки.
		b.mu.Lock()
		if b.cur == s && s.owner == me {
			b.cur = nil
		}
		b.mu.Unlock()
		return "", ctx.Err()
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cur == s {
		b.cur = nil
	}
	if s.owner != me {
		return "", fmt.Errorf("окно перешло к новому запросу: %w", coreproc.ErrCaptchaNoReply)
	}
	return s.token, s.err
}

// supersede закрывает текущее окно: ядро прислало новый запрос. Под mu.
func (b *Bridge) supersede() {
	if s := b.cur; s != nil {
		s.handOver(0)
		s.cancel(errSuperseded)
		b.cur = nil
	}
}

// open открывает окно для запроса в своей горутине. Под mu.
func (b *Bridge) open(ctx context.Context, creq coreproc.CaptchaRequest, owner uint64) *session {
	wctx, cancel := context.WithCancelCause(ctx)
	s := &session{uri: creq.RedirectURI, cancel: cancel, done: make(chan struct{}), owner: owner,
		moved: make(chan struct{})}
	s.timer = time.AfterFunc(b.opts.Timeout, func() { cancel(errTimeout) })
	req := Request{
		Mode:         creq.Mode,
		RedirectURI:  creq.RedirectURI,
		SessionToken: creq.SessionToken,
		Deadline:     time.Now().Add(b.opts.Timeout),
	}
	go func() {
		defer close(s.done)
		token, err := b.solveIn(wctx, req)
		s.timer.Stop()
		s.token, s.err = b.verdict(wctx, token, err)
		cancel(nil)
	}()
	return s
}

// solveIn решает капчу в окне, а не открылось окно — вручную.
func (b *Bridge) solveIn(ctx context.Context, req Request) (string, error) {
	if b.window == nil {
		return b.manual.Solve(ctx, req)
	}
	token, err := b.window.Solve(ctx, req)
	if (errors.Is(err, ErrBrowser) || errors.Is(err, ErrProxy)) && ctx.Err() == nil {
		fmt.Fprintf(b.opts.Out, "\nОкно капчи не открылось (%v) — решите её вручную.\n", err)
		b.logf("[CAPTCHA] окно не открылось, ручной режим: %v", err)
		return b.manual.Solve(ctx, req)
	}
	return token, err
}

// verdict — ответ ядру по исходу окна и строка человеку о том, чем оно
// кончилось.
func (b *Bridge) verdict(ctx context.Context, token string, err error) (string, error) {
	cause := context.Cause(ctx)
	switch {
	case err == nil && token != "":
		b.logf("[CAPTCHA] капча решена, токен — ядру")
		return token, nil
	case errors.Is(cause, errTimeout):
		fmt.Fprintf(b.opts.Out, "\nВремя на капчу (%s) вышло — окно закрыто.\n", b.opts.Timeout)
		b.logf("[CAPTCHA] время окна вышло")
		return "", fmt.Errorf("%w: %w", errTimeout, coreproc.ErrCaptchaNoReply)
	case errors.Is(cause, errSuperseded):
		fmt.Fprintln(b.opts.Out, "\nVK прислал новую капчу — прежнее окно закрыто.")
		b.logf("[CAPTCHA] новый запрос — прежнее окно закрыто")
		return "", fmt.Errorf("%w: %w", errSuperseded, coreproc.ErrCaptchaNoReply)
	case ctx.Err() != nil:
		return "", ctx.Err()
	case errors.Is(err, ErrWindowClosed):
		fmt.Fprintln(b.opts.Out, "\nОкно капчи закрыто, капча не решена.")
		return "", &coreproc.CaptchaError{Reason: "closed", Err: err}
	case errors.Is(err, ErrPageInit):
		fmt.Fprintf(b.opts.Out, "\n%v — окно закрыто, ядро попробует снова.\n", err)
		return "", &coreproc.CaptchaError{Reason: "init", Err: err}
	case errors.Is(err, ErrLimit):
		fmt.Fprintln(b.opts.Out, "\nVK: лимит попыток на эту капчу исчерпан — окно закрыто, ядро попробует снова.")
		return "", &coreproc.CaptchaError{Reason: "limit", Err: err}
	case errors.Is(err, ErrCancelled):
		return "", &coreproc.CaptchaError{Reason: "cancelled", Err: err}
	case err == nil:
		err = ErrNoToken
	}
	fmt.Fprintf(b.opts.Out, "\nКапча не решена: %v\n", err)
	b.logf("[CAPTCHA] капча не решена: %v", err)
	return "", &coreproc.CaptchaError{Reason: "failed", Err: err}
}

func (b *Bridge) logf(format string, args ...any) {
	if b.opts.Logf != nil {
		b.opts.Logf(format, args...)
	}
}

// handOver отдаёт окно другому вызову Solve (0 — никому) и отпускает
// прежний. Под Bridge.mu.
func (s *session) handOver(owner uint64) {
	s.owner = owner
	close(s.moved)
	s.moved = make(chan struct{})
}

func (s *session) finished() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}
