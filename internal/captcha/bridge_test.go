package captcha

import (
	"bytes"
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FreedomCats/csqtt-cli/internal/coreproc"
	"github.com/FreedomCats/csqtt-cli/internal/lines"
)

// fakeWindow — окно без окна: каждый Solve — открытие, исход задаёт тест.
type fakeWindow struct {
	mu     sync.Mutex
	opened []*fakeOpen
	notify chan *fakeOpen
}

// fakeOpen — одно открытое окно.
type fakeOpen struct {
	req    Request
	result chan outcome // исход окна; не задан — окно ждёт ctx
	closed chan struct{}
}

func newFakeWindow() *fakeWindow { return &fakeWindow{notify: make(chan *fakeOpen, 16)} }

func (w *fakeWindow) Solve(ctx context.Context, req Request) (string, error) {
	o := &fakeOpen{req: req, result: make(chan outcome, 1), closed: make(chan struct{})}
	w.mu.Lock()
	w.opened = append(w.opened, o)
	w.mu.Unlock()
	w.notify <- o
	defer close(o.closed)
	select {
	case got := <-o.result:
		return got.token, got.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (w *fakeWindow) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.opened)
}

// next ждёт открытия окна.
func (w *fakeWindow) next(t *testing.T) *fakeOpen {
	t.Helper()
	select {
	case o := <-w.notify:
		return o
	case <-time.After(5 * time.Second):
		t.Fatal("окно не открылось")
		return nil
	}
}

func (o *fakeOpen) waitClosed(t *testing.T) {
	t.Helper()
	select {
	case <-o.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("окно не закрылось")
	}
}

func manualReq(uri string) coreproc.CaptchaRequest {
	return coreproc.CaptchaRequest{Mode: ModeManual, RedirectURI: uri, SessionToken: "выдуманная-сессия"}
}

// solveAsync — Bridge.Solve в горутине.
func solveAsync(b *Bridge, ctx context.Context, req coreproc.CaptchaRequest) <-chan outcome {
	ch := make(chan outcome, 1)
	go func() {
		token, err := b.Solve(ctx, req)
		ch <- outcome{token, err}
	}()
	return ch
}

func wait(t *testing.T, ch <-chan outcome) outcome {
	t.Helper()
	select {
	case got := <-ch:
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("Solve не вернулся")
		return outcome{}
	}
}

func reason(err error) string {
	var ce *coreproc.CaptchaError
	if errors.As(err, &ce) {
		return ce.Reason
	}
	return ""
}

func testBridge(w Window, opts Options) (*Bridge, *bytes.Buffer) {
	out := &bytes.Buffer{}
	opts.Out = out
	if opts.Lines == nil {
		opts.Lines = lines.New(strings.NewReader(""))
	}
	return newBridgeWith(opts, w, nil), out
}

// auto — сразу отказ unsupported, окна нет.
func TestBridgeAutoUnsupported(t *testing.T) {
	w := newFakeWindow()
	b, _ := testBridge(w, Options{})
	_, err := b.Solve(context.Background(), coreproc.CaptchaRequest{Mode: ModeAuto, RedirectURI: "https://id.vk.com/x", SessionToken: "s"})
	if reason(err) != "unsupported" {
		t.Fatalf("auto: %v", err)
	}
	if w.count() != 0 {
		t.Error("на auto открылось окно")
	}
}

// Исходы окна — ответ ядру: токен или причина.
func TestBridgeOutcomes(t *testing.T) {
	cases := []struct {
		name   string
		out    outcome
		token  string
		reason string
	}{
		{"токен", outcome{token: "выдуманный-токен"}, "выдуманный-токен", ""},
		{"окно закрыто", outcome{err: ErrWindowClosed}, "", "closed"},
		{"ERROR_LIMIT", outcome{err: ErrLimit}, "", "limit"},
		{"initSession с ошибкой", outcome{err: ErrPageInit}, "", "init"},
		{"прочее", outcome{err: errors.New("страница не загрузилась")}, "", "failed"},
		{"пусто", outcome{}, "", "failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newFakeWindow()
			b, _ := testBridge(w, Options{})
			done := solveAsync(b, context.Background(), manualReq("https://id.vk.com/a"))
			o := w.next(t)
			if o.req.Mode != ModeManual || o.req.SessionToken != "выдуманная-сессия" ||
				o.req.Left(time.Now()) > ManualTimeout || o.req.Left(time.Now()) < ManualTimeout-time.Minute {
				t.Errorf("запрос окну: %+v", o.req)
			}
			o.result <- tc.out
			got := wait(t, done)
			if got.token != tc.token || reason(got.err) != tc.reason {
				t.Fatalf("исход %q, %v; ожидался %q, %q", got.token, got.err, tc.token, tc.reason)
			}
		})
	}
}

// selected (режим ядра wv) — то же окно, что manual, с тем же сроком, и
// токен из него уходит ядру.
func TestBridgeSelected(t *testing.T) {
	w := newFakeWindow()
	b, _ := testBridge(w, Options{})
	req := manualReq("https://id.vk.com/a")
	req.Mode = ModeSelected
	done := solveAsync(b, context.Background(), req)
	o := w.next(t)
	if o.req.Mode != ModeSelected || o.req.SessionToken != "выдуманная-сессия" ||
		o.req.Left(time.Now()) > ManualTimeout || o.req.Left(time.Now()) < ManualTimeout-time.Minute {
		t.Errorf("запрос окну: %+v", o.req)
	}
	o.result <- outcome{token: "выдуманный-токен"}
	if got := wait(t, done); got.token != "выдуманный-токен" || got.err != nil {
		t.Fatalf("selected: %q, %v", got.token, got.err)
	}
}

// Окно не открылось — та же капча решается вручную.
func TestBridgeFallsBackToManual(t *testing.T) {
	w := newFakeWindow()
	const token = "выдуманный-токен-0123456789"
	b, out := testBridge(w, Options{Lines: lines.New(strings.NewReader(token + "\n"))})
	done := solveAsync(b, context.Background(), manualReq("https://id.vk.com/a"))
	w.next(t).result <- outcome{err: ErrBrowser}
	if got := wait(t, done); got.err != nil || got.token != token {
		t.Fatalf("ручной режим: %q, %v", got.token, got.err)
	}
	if !strings.Contains(out.String(), "решите её вручную") || !strings.Contains(out.String(), "https://id.vk.com/a") {
		t.Errorf("вывод:\n%s", out)
	}
}

// Время вышло — окно закрыто, ядру не отвечают.
func TestBridgeTimeout(t *testing.T) {
	w := newFakeWindow()
	b, out := testBridge(w, Options{Timeout: 200 * time.Millisecond})
	start := time.Now()
	done := solveAsync(b, context.Background(), manualReq("https://id.vk.com/a"))
	o := w.next(t)
	got := wait(t, done)
	if !errors.Is(got.err, coreproc.ErrCaptchaNoReply) {
		t.Fatalf("исход по сроку: %v", got.err)
	}
	if d := time.Since(start); d < 200*time.Millisecond {
		t.Errorf("срок вышел за %s", d)
	}
	o.waitClosed(t)
	if !strings.Contains(out.String(), "вышло") {
		t.Errorf("вывод:\n%s", out)
	}
}

// Новый запрос закрывает прежнее окно; на прежний ядру не отвечают.
func TestBridgeNewRequestClosesPrevious(t *testing.T) {
	w := newFakeWindow()
	b, _ := testBridge(w, Options{})
	first := solveAsync(b, context.Background(), manualReq("https://id.vk.com/a"))
	o1 := w.next(t)

	second := solveAsync(b, context.Background(), manualReq("https://id.vk.com/b"))
	o1.waitClosed(t)
	if got := wait(t, first); !errors.Is(got.err, coreproc.ErrCaptchaNoReply) {
		t.Fatalf("прежний запрос: %q, %v", got.token, got.err)
	}
	o2 := w.next(t)
	if o2.req.RedirectURI != "https://id.vk.com/b" {
		t.Errorf("второе окно: %+v", o2.req)
	}
	o2.result <- outcome{token: "токен-b"}
	if got := wait(t, second); got.token != "токен-b" {
		t.Fatalf("новый запрос: %q, %v", got.token, got.err)
	}
}

// Тот же redirect_uri при открытом окне второго окна не открывает: окно
// отвечает новому запросу, а время отсчитывается заново.
func TestBridgeSameCaptchaKeepsWindow(t *testing.T) {
	w := newFakeWindow()
	b, _ := testBridge(w, Options{Timeout: 400 * time.Millisecond})
	first := solveAsync(b, context.Background(), manualReq("https://id.vk.com/a"))
	o := w.next(t)
	time.Sleep(250 * time.Millisecond)

	second := solveAsync(b, context.Background(), manualReq("https://id.vk.com/a"))
	if got := wait(t, first); !errors.Is(got.err, coreproc.ErrCaptchaNoReply) {
		t.Fatalf("прежний запрос: %q, %v", got.token, got.err)
	}
	// Без сброса времени окно закрылось бы через 150 мс.
	time.Sleep(250 * time.Millisecond)
	o.result <- outcome{token: "токен-a"}
	if got := wait(t, second); got.token != "токен-a" || got.err != nil {
		t.Fatalf("новый запрос: %q, %v", got.token, got.err)
	}
	if n := w.count(); n != 1 {
		t.Errorf("окон открыто %d, ожидалось одно", n)
	}
}

// auto при открытом окне значит, что ядро ручного ответа уже не ждёт.
func TestBridgeAutoClosesWindow(t *testing.T) {
	w := newFakeWindow()
	b, _ := testBridge(w, Options{})
	first := solveAsync(b, context.Background(), manualReq("https://id.vk.com/a"))
	o := w.next(t)
	if _, err := b.Solve(context.Background(), coreproc.CaptchaRequest{Mode: ModeAuto, RedirectURI: "https://id.vk.com/b", SessionToken: "s"}); reason(err) != "unsupported" {
		t.Fatalf("auto: %v", err)
	}
	o.waitClosed(t)
	if got := wait(t, first); !errors.Is(got.err, coreproc.ErrCaptchaNoReply) {
		t.Fatalf("ручной запрос: %v", got.err)
	}
}

// Конец попытки (ctx) закрывает окно, Solve возвращается сразу.
func TestBridgeCancel(t *testing.T) {
	w := newFakeWindow()
	b, _ := testBridge(w, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	done := solveAsync(b, ctx, manualReq("https://id.vk.com/a"))
	o := w.next(t)
	cancel()
	if got := wait(t, done); !errors.Is(got.err, context.Canceled) {
		t.Fatalf("отмена: %v", got.err)
	}
	o.waitClosed(t)

	// Следующий запрос с тем же адресом — новое окно.
	done = solveAsync(b, context.Background(), manualReq("https://id.vk.com/a"))
	w.next(t).result <- outcome{token: "токен"}
	if got := wait(t, done); got.token != "токен" {
		t.Fatalf("после отмены: %q, %v", got.token, got.err)
	}
}

// Без окна для платформы — сразу ручной режим.
func TestBridgeManualOnly(t *testing.T) {
	const token = "выдуманный-токен-0123456789"
	out := &bytes.Buffer{}
	b := NewBridge(Options{Out: out, Lines: lines.New(strings.NewReader(token + "\n"))})
	b.window = nil
	got, err := b.Solve(context.Background(), manualReq("https://id.vk.com/a"))
	if err != nil || got != token {
		t.Fatalf("Solve: %q, %v", got, err)
	}
}

// directProbe — Options.Direct для тестов: считает, сколько капч сейчас идёт
// в сеть мимо прокси, и сколько раз Direct звали.
type directProbe struct {
	mu     sync.Mutex
	active int
	calls  int
}

func (p *directProbe) direct() func() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.active++
	p.calls++
	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.active--
		})
	}
}

func (p *directProbe) state() (active, calls int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.active, p.calls
}

// probedWindow — окно, которое запоминает, было ли оно под Direct.
type probedWindow struct {
	Window
	probe  *directProbe
	mu     sync.Mutex
	direct []bool
}

func (w *probedWindow) Solve(ctx context.Context, req Request) (string, error) {
	active, _ := w.probe.state()
	w.mu.Lock()
	w.direct = append(w.direct, active > 0)
	w.mu.Unlock()
	return w.Window.Solve(ctx, req)
}

func (w *probedWindow) seen() []bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]bool(nil), w.direct...)
}

func noDial(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("не нужен")
}

// Direct — только для того, что идёт в сеть мимо прокси: окно без Dial,
// окно без прокси, когда прокси не поднялся, и ручной режим. Окно через
// прокси и отказ auto его не трогают; после капчи Direct снят.
func TestBridgeDirect(t *testing.T) {
	const token = "выдуманный-токен-0123456789"
	cases := []struct {
		name   string
		dial   bool
		window outcome // исход окна (через прокси, если dial)
		direct bool    // есть окно без прокси
		// Ожидания: окно под Direct; окно без прокси открыто; ручной режим.
		windowDirect, directOpened, manual bool
		calls                              int
	}{
		{name: "окно без Dial", window: outcome{token: token}, windowDirect: true, calls: 1},
		{name: "окно через прокси", dial: true, direct: true, window: outcome{token: token}},
		{name: "прокси не поднялся", dial: true, direct: true, window: outcome{err: ErrProxy},
			directOpened: true, calls: 1},
		{name: "прокси не поднялся, окна без прокси нет", dial: true, window: outcome{err: ErrProxy},
			manual: true, calls: 1},
		{name: "браузер не запустился", dial: true, direct: true, window: outcome{err: ErrBrowser},
			manual: true, calls: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			probe := &directProbe{}
			opts := Options{Out: &bytes.Buffer{}, Lines: lines.New(strings.NewReader(token + "\n")),
				Direct: probe.direct}
			if tc.dial {
				opts.Dial = noDial
			}
			window := &probedWindow{Window: newFakeWindow(), probe: probe}
			fakeDirect := newFakeWindow()
			direct := &probedWindow{Window: fakeDirect, probe: probe}
			var d Window
			if tc.direct {
				d = direct
			}
			b := newBridgeWith(opts, window, d)

			done := solveAsync(b, context.Background(), manualReq("https://id.vk.com/a"))
			window.Window.(*fakeWindow).next(t).result <- tc.window
			if tc.directOpened {
				fakeDirect.next(t).result <- outcome{token: token}
			}
			if got := wait(t, done); got.token != token || got.err != nil {
				t.Fatalf("Solve = %q, %v", got.token, got.err)
			}
			if got := window.seen(); !slices.Equal(got, []bool{tc.windowDirect}) {
				t.Errorf("окно под Direct: %v, ждали %v", got, tc.windowDirect)
			}
			if got := direct.seen(); tc.directOpened != (len(got) > 0) || tc.directOpened && !got[0] {
				t.Errorf("окно без прокси: %v, ждали открытым под Direct: %v", got, tc.directOpened)
			}
			active, calls := probe.state()
			if active != 0 || calls != tc.calls {
				t.Errorf("Direct: активно %d, вызовов %d; ждали 0 и %d", active, calls, tc.calls)
			}
		})
	}

	probe := &directProbe{}
	b := newBridgeWith(Options{Out: &bytes.Buffer{}, Lines: lines.New(strings.NewReader("")), Direct: probe.direct},
		newFakeWindow(), nil)
	if _, err := b.Solve(context.Background(), coreproc.CaptchaRequest{Mode: ModeAuto, RedirectURI: "https://id.vk.com/x"}); reason(err) != "unsupported" {
		t.Fatalf("auto: %v", err)
	}
	if _, calls := probe.state(); calls != 0 {
		t.Error("auto звал Direct")
	}
}

// Без окна для платформы ручной режим — под Direct, и после ответа он снят.
func TestBridgeManualOnlyDirect(t *testing.T) {
	const token = "выдуманный-токен-0123456789"
	probe := &directProbe{}
	b := newBridgeWith(Options{Out: &bytes.Buffer{}, Lines: lines.New(strings.NewReader(token + "\n")),
		Direct: probe.direct}, nil, nil)
	if got, err := b.Solve(context.Background(), manualReq("https://id.vk.com/a")); err != nil || got != token {
		t.Fatalf("Solve: %q, %v", got, err)
	}
	if active, calls := probe.state(); active != 0 || calls != 1 {
		t.Errorf("Direct: активно %d, вызовов %d", active, calls)
	}
}
