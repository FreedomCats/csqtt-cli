package supervisor

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FreedomCats/csqtt-cli/internal/coreproc"
	"github.com/FreedomCats/csqtt-cli/internal/datapath"
)

// Записи вывода ядра — строки в том виде, в каком ядро пишет их в stdout и
// stderr; разбирает их coreproc.ParseLine, как у настоящего ядра. Адреса —
// документационные, хеши выдуманы.
var (
	// recStart — ядро разобрало аргументы и открыло сокет канала.
	recStart = []string{
		`__CSQTT_EVENT__|PROCESS|{"pid":4242}`,
		`[КЛИЕНТ] Воркеров: 18 | Групп: 2 | Хешей: 1`,
		`__CSQTT_EVENT__|LISTEN|{"port":40123}`,
	}
	// recCredentials — VK выдал креды.
	recCredentials = []string{
		`[КРЕД #1] OK, TURN: ["203.0.113.7:3478"], до 9 воркеров`,
		`__CSQTT_EVENT__|PROGRESS|{"kind":"credentials"}`,
	}
	// recConfig — сервер выдал адрес туннеля.
	recConfig = []string{
		`[ВОРКЕР #1] Конфиг получен`,
		`[КЛИЕНТ] Tunnel IP: 192.0.2.2/32 | DNS: 198.51.100.53`,
		`__CSQTT_EVENT__|CONFIG|{"config":"TUNCONF:192.0.2.2:198.51.100.53"}`,
	}
	// recReady — воркер на линии.
	recReady = []string{
		`[ВОРКЕР #1] [READY] Поток готов ✓`,
		`__CSQTT_EVENT__|READY|{"worker":1}`,
	}
	recStats = `__CSQTT_EVENT__|STATS|{"active":1,"bytes_down":0,"bytes_up":0}`
	// recConnect — от запуска до работающего туннеля.
	recConnect = join(recStart, recCredentials, recConfig, recReady, []string{recStats})
)

func join(parts ...[]string) []string {
	var out []string
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func fatalLine(code, message string) string {
	return fmt.Sprintf(`__CSQTT_EVENT__|ERROR|{"code":%q,"fatal":true,"message":%q}`, code, message)
}

func unavailableLine(hash string, code int) string {
	return fmt.Sprintf(`__CSQTT_EVENT__|CALL_UNAVAILABLE|{"code":%d,"hash":%q}`, code, hash)
}

// exitError — код выхода поддельного ядра.
type exitError int

func (e exitError) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

// fakeCore — ядро по записи. Шаги записи:
//
//	строка вывода      — сообщение ядра;
//	"wait <префикс>"   — ждать команду в stdin с этим префиксом;
//	"on stop"          — ждать Stop; остаток записи ядро доигрывает уже
//	                     остановленным, как настоящее, закрывающее сессии;
//	"exit <код>"       — выйти самому, 0 — без ошибки.
//
// Кончилась запись — ядро живёт до Stop, как настоящее: тогда шлёт STOPPED и
// выходит.
type fakeCore struct {
	cfg  coreproc.Config
	msgs chan coreproc.Message
	cmds chan string

	stopOnce sync.Once
	stopReq  chan struct{}
	done     chan struct{}
	err      error

	mu      sync.Mutex
	sent    []string
	stopped bool // звали Stop
}

func newFakeCore(cfg coreproc.Config, script []string) *fakeCore {
	c := &fakeCore{
		cfg:     cfg,
		msgs:    make(chan coreproc.Message),
		cmds:    make(chan string, 16),
		stopReq: make(chan struct{}),
		done:    make(chan struct{}),
	}
	go c.play(script)
	return c
}

func (c *fakeCore) play(script []string) {
	defer close(c.done)
	defer close(c.msgs)
	stopped := false // прошли "on stop": Stop уже позвали
	for _, step := range script {
		if step == "on stop" {
			<-c.stopReq
			stopped = true
			continue
		}
		if stopped {
			c.msgs <- coreproc.ParseLine(step)
			continue
		}
		if prefix, ok := strings.CutPrefix(step, "wait "); ok {
			if !c.waitCommand(prefix) {
				c.msgs <- coreproc.StoppedEvent{}
				return
			}
			continue
		}
		var code int
		if _, err := fmt.Sscanf(step, "exit %d", &code); err == nil {
			if code != 0 {
				c.err = exitError(code)
			}
			return
		}
		select {
		case c.msgs <- coreproc.ParseLine(step):
		case <-c.stopReq:
			c.msgs <- coreproc.StoppedEvent{}
			return
		}
	}
	if !stopped {
		<-c.stopReq
	}
	c.msgs <- coreproc.StoppedEvent{}
}

// waitCommand ждёт команду с префиксом; false — ядро остановили раньше.
func (c *fakeCore) waitCommand(prefix string) bool {
	for {
		select {
		case cmd := <-c.cmds:
			if strings.HasPrefix(cmd, prefix) {
				return true
			}
		case <-c.stopReq:
			return false
		}
	}
}

func (c *fakeCore) Messages() <-chan coreproc.Message { return c.msgs }

func (c *fakeCore) Send(cmd string) error {
	c.mu.Lock()
	c.sent = append(c.sent, cmd)
	c.mu.Unlock()
	select {
	case c.cmds <- cmd:
	default:
	}
	return nil
}

func (c *fakeCore) Stop() error {
	c.mu.Lock()
	c.stopped = true
	c.mu.Unlock()
	c.stopOnce.Do(func() { close(c.stopReq) })
	return c.Wait()
}

func (c *fakeCore) Wait() error {
	<-c.done
	return c.err
}

func (c *fakeCore) wasStopped() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopped
}

func (c *fakeCore) commands() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.sent...)
}

// fakeLink — канал пакетов: запоминает, какие порты ядра ему сообщали.
type fakeLink struct {
	mu    sync.Mutex
	ports []uint16
}

var fakeLinkAddr = netip.MustParseAddrPort("127.0.0.1:40000")

func (l *fakeLink) Addr() netip.AddrPort { return fakeLinkAddr }

func (l *fakeLink) SetCorePort(port uint16) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ports = append(l.ports, port)
}

func (l *fakeLink) corePorts() []uint16 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]uint16(nil), l.ports...)
}

// fakeDatapath — datapath: запоминает вызовы, Start может отказать.
type fakeDatapath struct {
	mu       sync.Mutex
	starts   []datapath.TunConf
	dns      [][]netip.Addr
	startErr error
}

func (d *fakeDatapath) Start(_ context.Context, conf datapath.TunConf) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.starts = append(d.starts, conf)
	return d.startErr
}

func (d *fakeDatapath) UpdateDNS(dns []netip.Addr) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dns = append(d.dns, dns)
	return nil
}

func (d *fakeDatapath) calls() ([]datapath.TunConf, [][]netip.Addr) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]datapath.TunConf(nil), d.starts...), append([][]netip.Addr(nil), d.dns...)
}

// fakeTunFD — канал дескриптором адаптера: имена сокетов по порядку, вызовы
// SendFD запоминаются. send подменяет поведение SendFD; nil — ядро
// принимает сразу.
type fakeTunFD struct {
	mu    sync.Mutex
	names int
	sends []fdSend
	send  func(ctx context.Context, socket string, pid int) error
}

// fdSend — вызов SendFD: куда, кому и чем кончился.
type fdSend struct {
	socket string
	pid    int
	err    error
}

func (f *fakeTunFD) SocketName() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.names++
	return fmt.Sprintf("csqtt-cli-tun-test-%d", f.names)
}

func (f *fakeTunFD) SendFD(ctx context.Context, socket string, pid int) error {
	var err error
	if f.send != nil {
		err = f.send(ctx, socket, pid)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sends = append(f.sends, fdSend{socket, pid, err})
	return err
}

func (f *fakeTunFD) calls() []fdSend {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fdSend(nil), f.sends...)
}

// fakePersistent — datapath, живущий весь сеанс: Suspend зовёт onSuspend и
// возвращает suspendErr.
type fakePersistent struct {
	*fakeDatapath
	onSuspend  func()
	suspendErr error
}

func (p *fakePersistent) Suspend() error {
	if p.onSuspend != nil {
		p.onSuspend()
	}
	return p.suspendErr
}

// harness — цикл на поддельном ядре: каждая попытка играет свою запись.
// Когда записи кончились, следующая попытка отменяет Run.
type harness struct {
	t      *testing.T
	tunnel *Tunnel
	link   *fakeLink
	dp     *fakeDatapath
	ctx    context.Context
	cancel context.CancelFunc

	transitions chan Transition

	mu      sync.Mutex
	scripts [][]string
	cores   []*fakeCore
	delays  []time.Duration
	trs     []Transition
	by      []coreproc.Message // каким сообщением вызван переход; nil — не сообщением
	logs    []string
	msgs    []coreproc.Message
	seen    int // сколько сообщений уже приписано переходам
}

func newHarness(t *testing.T, opts Options, scripts ...[]string) *harness {
	t.Helper()
	h := &harness{
		t:           t,
		link:        &fakeLink{},
		dp:          &fakeDatapath{},
		transitions: make(chan Transition, 256),
		scripts:     scripts,
	}
	h.ctx, h.cancel = context.WithCancel(context.Background())
	t.Cleanup(h.cancel)

	opts.CorePath = "csqtt-core"
	if opts.Config.Hashes == nil {
		opts.Config.Hashes = []string{"hash-one-0123456789"}
	}
	opts.Config.Peer = "203.0.113.10:46000"
	opts.Config.Password = "test-password"
	// Режим дескриптора (TunFD) и свой datapath тест задаёт сам.
	if opts.TunFD == nil {
		opts.Link = h.link
	}
	if opts.Datapath == nil {
		opts.Datapath = h.dp
	}
	opts.OnTransition = func(tr Transition) {
		h.mu.Lock()
		h.trs = append(h.trs, tr)
		// OnMessage зовётся раньше OnTransition того же сообщения; переход
		// не от сообщения последним сообщением не помечается.
		var by coreproc.Message
		if n := len(h.msgs); n > h.seen {
			by = h.msgs[n-1]
			h.seen = n
		}
		h.by = append(h.by, by)
		h.mu.Unlock()
		h.transitions <- tr
	}
	opts.OnMessage = func(m coreproc.Message) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.msgs = append(h.msgs, m)
	}
	opts.Logf = func(format string, args ...any) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.logs = append(h.logs, fmt.Sprintf(format, args...))
	}
	tun, err := NewTunnel(opts)
	if err != nil {
		t.Fatal(err)
	}
	tun.start = h.start
	tun.sleep = func(ctx context.Context, d time.Duration) bool {
		h.mu.Lock()
		h.delays = append(h.delays, d)
		h.mu.Unlock()
		return ctx.Err() == nil
	}
	h.tunnel = tun
	return h
}

func (h *harness) start(cfg coreproc.Config) (Core, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var script []string
	if len(h.scripts) > 0 {
		script, h.scripts = h.scripts[0], h.scripts[1:]
	} else {
		h.cancel()
	}
	c := newFakeCore(cfg, script)
	h.cores = append(h.cores, c)
	return c, nil
}

// run запускает цикл и возвращает канал его итога.
func (h *harness) run() <-chan error {
	done := make(chan error, 1)
	go func() { done <- h.tunnel.Run(h.ctx) }()
	return done
}

// result ждёт итог цикла.
func (h *harness) result(done <-chan error) error {
	h.t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		h.t.Fatalf("цикл не вернулся; переходы:\n%s", h.dump())
		return nil
	}
}

// waitState ждёт перехода в состояние.
func (h *harness) waitState(want State) Transition {
	h.t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case tr := <-h.transitions:
			if tr.To == want {
				return tr
			}
		case <-timeout:
			h.t.Fatalf("нет перехода в %s; переходы:\n%s", want, h.dump())
			return Transition{}
		}
	}
}

func (h *harness) states() []State {
	h.mu.Lock()
	defer h.mu.Unlock()
	states := []State{StateIdle}
	for _, tr := range h.trs {
		states = append(states, tr.To)
	}
	return states
}

func (h *harness) transitionsTo(want State) []Transition {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []Transition
	for _, tr := range h.trs {
		if tr.To == want {
			out = append(out, tr)
		}
	}
	return out
}

// triggered — переходы в want с вызвавшими их сообщениями.
func (h *harness) triggered(want State) []coreproc.Message {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []coreproc.Message
	for i, tr := range h.trs {
		if tr.To == want {
			out = append(out, h.by[i])
		}
	}
	return out
}

func (h *harness) attempts() []*fakeCore {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]*fakeCore(nil), h.cores...)
}

func (h *harness) sleeps() []time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]time.Duration(nil), h.delays...)
}

func (h *harness) logLines() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.logs...)
}

func (h *harness) dump() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var b strings.Builder
	for _, tr := range h.trs {
		fmt.Fprintf(&b, "  %s → %s", tr.From, tr.To)
		if tr.Cause != nil {
			fmt.Fprintf(&b, " (%v)", tr.Cause)
		}
		b.WriteString("\n")
	}
	return b.String()
}
