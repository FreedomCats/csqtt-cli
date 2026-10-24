package cli

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FreedomCats/csqtt-cli/internal/captcha"
	"github.com/FreedomCats/csqtt-cli/internal/datapath"
	"github.com/FreedomCats/csqtt-cli/internal/netguard"
	"github.com/FreedomCats/csqtt-cli/internal/supervisor"
)

// tunJournal — общий журнал шагов connect с tun: подделки адаптера, защиты,
// выбора интерфейса и DNS системы пишут в него, тест сверяет порядок.
type tunJournal struct {
	mu    sync.Mutex
	steps []string
}

func (j *tunJournal) add(format string, args ...any) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.steps = append(j.steps, fmt.Sprintf(format, args...))
}

func (j *tunJournal) list() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Clone(j.steps)
}

// fakeConnectTun — datapath.Tun без адаптера: защиту DNS включает и снимает,
// как настоящий (Start — Enable, Suspend и Close — Disable).
type fakeConnectTun struct {
	j           *tunJournal
	opts        datapath.TunOptions
	unsupported error
	openErr     error
	opened      bool
	// failed — datapath.Failing; nil — адаптер не ломается.
	failed chan error
}

func (f *fakeConnectTun) Failed() <-chan error { return f.failed }

func (f *fakeConnectTun) Requires() datapath.Requirements {
	return datapath.Requirements{Unsupported: f.unsupported, Admin: true, Netguard: true, DNSGuard: true}
}

func (f *fakeConnectTun) Reserve() (func(), error) {
	f.j.add("резерв")
	return func() { f.j.add("резерв снят") }, nil
}

func (f *fakeConnectTun) Open() error {
	f.j.add("адаптер открыт")
	f.opened = f.openErr == nil
	return f.openErr
}

func (f *fakeConnectTun) Start(_ context.Context, conf datapath.TunConf) error {
	f.j.add("Start %v", conf.Address)
	if f.opts.DNSGuard != nil {
		return f.opts.DNSGuard.Enable(conf.DNS)
	}
	return nil
}

func (f *fakeConnectTun) UpdateDNS([]netip.Addr) error { return nil }

func (f *fakeConnectTun) Suspend() error {
	f.j.add("Suspend")
	if f.opts.DNSGuard != nil {
		return f.opts.DNSGuard.Disable()
	}
	return nil
}

func (f *fakeConnectTun) Close() error {
	f.j.add("адаптер закрыт")
	// Как настоящий: защиту снимает только открытый адаптер.
	if f.opts.DNSGuard != nil && f.opened {
		return f.opts.DNSGuard.Disable()
	}
	return nil
}

func (f *fakeConnectTun) SocketName() string { return "csqtt-cli-tun-test" }

func (f *fakeConnectTun) SendFD(context.Context, string, int) error {
	f.j.add("дескриптор ядру")
	return nil
}

func (f *fakeConnectTun) CheckAdapter() error { return nil }

func (f *fakeConnectTun) Lost(err error) {
	f.j.add("адаптер сломан: %v", err)
}

// linkTun — fakeConnectTun с каналом Link вместо дескриптора, как адаптер
// Windows: SendFD и SocketName у него нет.
type linkTun struct {
	tunDatapath
	link *datapath.Link
}

func (l linkTun) Link() (*datapath.Link, error) { return l.link, nil }

// journalGuard — сессия защиты DNS (таблица nftables) с журналом.
type journalGuard struct {
	j           *tunJournal
	lockdownErr error
	closed      bool
}

func (g *journalGuard) Enable([]netip.Addr) error {
	g.j.add("защита DNS")
	return g.check()
}
func (g *journalGuard) Disable() error { g.j.add("правила сняты"); return g.check() }
func (g *journalGuard) Lockdown() error {
	if g.lockdownErr != nil {
		return g.lockdownErr
	}
	g.j.add("закрыто")
	return g.check()
}
func (g *journalGuard) BlockIPv6(bool) {}
func (g *journalGuard) Close() error {
	g.j.add("таблица закрыта")
	g.closed = true
	return nil
}

// check — правила после закрытия таблицы: так быть не должно.
func (g *journalGuard) check() error {
	if g.closed {
		g.j.add("ОШИБКА: правила после закрытия таблицы")
		return errors.New("таблица закрыта")
	}
	return nil
}

// tunStubs — подделки connect с tun и что они видели.
type tunStubs struct {
	j        *tunJournal
	tun      *fakeConnectTun // последний построенный адаптер с защитой
	guard    *journalGuard
	guardErr error
	root     bool
	// choices — что отдаёт выбор интерфейса по порядку; последний
	// повторяется.
	choices    []netguard.Choice
	rpProblem  string
	recoverErr error
	watchErr   error
	// link — адаптер с каналом Link (Windows), а не с дескриптором.
	link *datapath.Link

	mu       sync.Mutex
	netguard *netguard.Guard
	changed  func(now, before netguard.Choice)
	bridge   *captcha.Options
}

// markFlags — метка сокетов ядра: на Linux её пропускает таблица защиты, на
// Windows меток нет, и ядро с -mark не стартует.
func markFlags() []string {
	if runtime.GOOS != "linux" {
		return nil
	}
	return []string{fmt.Sprintf("--mark=%d", netguard.SocketMark)}
}

// withTunGuard задаёт tunGuardMissing на время теста: nil — система с защитой
// DNS и kill-switch (Linux, Windows), ошибка — без неё.
func withTunGuard(t *testing.T, missing error) {
	t.Helper()
	prev := tunGuardMissing
	t.Cleanup(func() { tunGuardMissing = prev })
	tunGuardMissing = missing
}

// stubTun подменяет всё, что connect с tun трогает в системе.
func stubTun(t *testing.T) *tunStubs {
	t.Helper()
	j := &tunJournal{}
	s := &tunStubs{j: j, guard: &journalGuard{j: j}, root: true,
		choices: []netguard.Choice{{Index4: 3, Alias4: "eth0"}}}
	s.tun = &fakeConnectTun{j: j}

	withTunGuard(t, nil)
	prevTun, prevRoot, prevSelect, prevRP, prevDNS, prevWatch, prevGuard :=
		newTun, isRoot, selectInterfaces, reversePathCheck, prepareSystemDNS, watchNetworkChanges, openDNSGuard
	t.Cleanup(func() {
		newTun, isRoot, selectInterfaces, reversePathCheck, prepareSystemDNS, watchNetworkChanges, openDNSGuard =
			prevTun, prevRoot, prevSelect, prevRP, prevDNS, prevWatch, prevGuard
	})
	newTun = func(opts datapath.TunOptions) tunDatapath {
		if opts.DNSGuard == nil && opts.Logf == nil {
			// Проба: Requires и Reserve.
			return &fakeConnectTun{j: j, unsupported: s.tun.unsupported}
		}
		s.tun.opts = opts
		if s.link != nil {
			return linkTun{s.tun, s.link}
		}
		return s.tun
	}
	isRoot = func() bool { return s.root }
	selectInterfaces = func(g *netguard.Guard) (netguard.Choice, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		c := s.choices[0]
		if len(s.choices) > 1 {
			s.choices = s.choices[1:]
		}
		s.netguard = g
		g.SetInterfaces(c.Index4, c.Index6)
		j.add("интерфейс %d", c.Index4)
		return c, nil
	}
	reversePathCheck = func(c netguard.Choice) (string, error) {
		if c.Index4 == 0 {
			return "", nil
		}
		j.add("rp_filter %s", c.Alias4)
		return s.rpProblem, nil
	}
	prepareSystemDNS = func(*printer) error {
		j.add("остатки DNS")
		return s.recoverErr
	}
	watchNetworkChanges = func(g *netguard.Guard, opts netguard.WatchOptions) (func(), error) {
		if s.watchErr != nil {
			return nil, s.watchErr
		}
		s.mu.Lock()
		s.changed = opts.Changed
		s.mu.Unlock()
		j.add("слежение за сетью")
		return func() { j.add("слежение снято") }, nil
	}
	openDNSGuard = func() (dnsGuardSession, error) {
		j.add("таблица открыта")
		if s.guardErr != nil {
			return nil, s.guardErr
		}
		return s.guard, nil
	}
	return s
}

// env — окружение теста с мостом капчи, который запоминает свои опции.
func (s *tunStubs) env(te *testEnv, out, errOut *syncBuffer) *env {
	e := te.env(out, errOut)
	e.captchaBridge = func(opts captcha.Options) supervisor.CaptchaFunc {
		s.mu.Lock()
		s.bridge = &opts
		s.mu.Unlock()
		return captcha.NewBridge(opts).Solve
	}
	return e
}

// network — смена сети, как её делает Watch: новые индексы уже стоят.
func (s *tunStubs) network(now, before netguard.Choice) {
	s.mu.Lock()
	g, changed := s.netguard, s.changed
	s.mu.Unlock()
	g.SetInterfaces(now.Index4, now.Index6)
	changed(now, before)
}

// connectTun — connect в горутине; ждёт строку until и возвращает отмену и
// итог.
func connectTun(t *testing.T, te *testEnv, s *tunStubs, args ...string) (out, errOut *syncBuffer, stop func() int) {
	t.Helper()
	t.Setenv(fakeCoreEnv, te.core)
	out, errOut = &syncBuffer{}, &syncBuffer{}
	e := s.env(te, out, errOut)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, e, append(append([]string{"connect", "-v"}, args...), "-config", te.configPath()))
	}()
	stopped := false
	var code int
	stop = func() int {
		if !stopped {
			stopped = true
			cancel()
			select {
			case code = <-done:
			case <-time.After(10 * time.Second):
				t.Fatalf("connect не вернулся:\n%s", out.String())
			}
		}
		return code
	}
	t.Cleanup(func() { stop() })
	return out, errOut, stop
}

// waitFor ждёт строку в выводе.
func waitFor(t *testing.T, out, errOut *syncBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(out.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("нет %q:\n%s\nstderr:\n%s", want, out.String(), errOut.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// coreArgs — аргументы ядра каждой попытки из журнала поддельного ядра.
func coreArgs(out string) []string {
	var args []string
	for _, line := range strings.Split(out, "\n") {
		if _, a, ok := strings.Cut(line, "[ТЕСТ] аргументы ядра: "); ok {
			args = append(args, a)
		}
	}
	return args
}

// Порядок шагов connect с tun: резерв, интерфейс и обратный фильтр, остатки
// DNS, таблица и «закрыто», адаптер — всё до запуска ядра; затем слежение
// за сетью, дескриптор и Start. Ядро получает -tun-uds, -bind-if и -mark, а
// мост капчи — Dial и Direct. По Ctrl+C — обратный порядок: слежение,
// адаптер, таблица, резерв.
func TestConnectTunOrder(t *testing.T) {
	s := stubTun(t)
	te := newTestEnv(t)
	te.addHome()
	te.core = "connected"
	out, errOut, stop := connectTun(t, te, s)
	waitFor(t, out, errOut, "туннель работает")
	if code := stop(); code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, errOut.String())
	}

	want := []string{
		"резерв", "интерфейс 3", "rp_filter eth0", "остатки DNS", "таблица открыта", "закрыто",
		"адаптер открыт", "слежение за сетью", "Start 192.0.2.2", "защита DNS",
		"слежение снято", "адаптер закрыт", "закрыто", "таблица закрыта", "резерв снят",
	}
	steps := s.j.list()
	// Дескриптор уходит из горутины попытки и с Start не упорядочен — лишь
	// бы между запуском слежения и его снятием.
	if i := slices.Index(steps, "дескриптор ядру"); i < slices.Index(steps, "слежение за сетью") ||
		i > slices.Index(steps, "слежение снято") {
		t.Errorf("дескриптор ядру — не в попытке: %v", steps)
	}
	if got := slices.DeleteFunc(steps, func(s string) bool { return s == "дескриптор ядру" }); !slices.Equal(got, want) {
		t.Errorf("шаги:\n  %s\nожидались:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}

	got := out.String()
	args := coreArgs(got)
	if len(args) != 1 {
		t.Fatalf("попыток %d:\n%s", len(args), got)
	}
	for _, flag := range append([]string{"--tun-uds=csqtt-cli-tun-test", "--bind-if=3"}, markFlags()...) {
		if !strings.Contains(args[0], flag) {
			t.Errorf("у ядра нет %s: %s", flag, args[0])
		}
	}
	if strings.Contains(args[0], "--listen") {
		t.Errorf("ядру в tun дали канал loopback: %s", args[0])
	}
	for _, want := range []string{
		"сокеты ядра идут мимо туннеля через физический интерфейс: IPv4 — eth0 (индекс 3)",
		"окно капчи — через прокси csqtt-cli тем же интерфейсом мимо туннеля",
		"защита DNS: пока туннель поднят",
		"kill-switch: пока туннель не поднят, трафик системы заблокирован",
		"адаптер csqtt-cli — весь трафик системы",
		"== туннель работает · весь трафик системы — через адаптер csqtt-cli",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("нет %q:\n%s", want, got)
		}
	}
	noSecrets(t, "stdout", got)

	if s.bridge == nil || s.bridge.Dial == nil || s.bridge.Direct == nil {
		t.Errorf("мост капчи без прокси или без снятия kill-switch: %+v", s.bridge)
	}
	if !s.tun.opts.DNSGuard.(*killSwitchGuard).closed {
		t.Error("kill-switch не помечен закрытым после connect")
	}
}

// Отказы до запуска ядра — с подсказкой; всё, что успело встать, снято.
func TestConnectTunRefusals(t *testing.T) {
	cases := []struct {
		name  string
		setup func(s *tunStubs)
		args  []string
		want  []string
		// steps — журнал целиком: что встало и как снято.
		steps []string
	}{
		{"нет root", func(s *tunStubs) { s.root = false }, nil,
			[]string{adminHint + " csqtt-cli connect -config ", "-datapath socks",
				"home -datapath socks)"}, nil},
		{"tun на этой системе нет", func(s *tunStubs) {
			s.tun.unsupported = errors.New("datapath tun пока работает только на Linux")
		},
			[]string{"-datapath", "tun"},
			[]string{"-datapath tun: режим tun", "только на Linux", "есть только SOCKS5", "-datapath socks"}, nil},
		{"строгий rp_filter", func(s *tunStubs) {
			s.rpProblem = "на eth0 строгий обратный фильтр: sysctl … rp_filter=2"
		}, nil,
			[]string{"rp_filter=2", "-datapath socks"},
			[]string{"резерв", "интерфейс 3", "rp_filter eth0", "резерв снят"}},
		{"остатки DNS не убраны", func(s *tunStubs) { s.recoverErr = errors.New("resolv.conf: незнакомый генератор") }, nil,
			[]string{"незнакомый генератор"},
			[]string{"резерв", "интерфейс 3", "rp_filter eth0", "остатки DNS", "резерв снят"}},
		{"защита не включилась", func(s *tunStubs) { s.guardErr = errors.New("nftables: операция не разрешена") }, nil,
			[]string{"защита от утечки DNS и kill-switch не включились", "операция не разрешена", "-allow-leak"},
			[]string{"резерв", "интерфейс 3", "rp_filter eth0", "остатки DNS", "таблица открыта", "резерв снят"}},
		{"kill-switch не встал", func(s *tunStubs) { s.guard.lockdownErr = errors.New("nftables: нет места") }, nil,
			[]string{"kill-switch не включился", "нет места", "-no-kill-switch", "-allow-leak"},
			[]string{"резерв", "интерфейс 3", "rp_filter eth0", "остатки DNS", "таблица открыта", "таблица закрыта",
				"резерв снят"}},
		{"адаптер не открылся", func(s *tunStubs) { s.tun.openErr = errors.New("tun: интерфейс csqtt-cli чужой") }, nil,
			[]string{"интерфейс csqtt-cli чужой"},
			[]string{"резерв", "интерфейс 3", "rp_filter eth0", "остатки DNS", "таблица открыта", "закрыто",
				"адаптер открыт", "адаптер закрыт", "таблица закрыта", "резерв снят"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := stubTun(t)
			c.setup(s)
			te := newTestEnv(t)
			te.addHome()
			te.core = "connected"
			t.Setenv(fakeCoreEnv, te.core)
			var out, errOut syncBuffer
			code := run(context.Background(), s.env(te, &out, &errOut),
				append(append([]string{"connect"}, c.args...), "-config", te.configPath()))
			if code != exitError {
				t.Errorf("код %d", code)
			}
			for _, want := range c.want {
				if !strings.Contains(errOut.String(), want) {
					t.Errorf("нет %q:\n%s", want, errOut.String())
				}
			}
			if got := s.j.list(); !slices.Equal(got, c.steps) {
				t.Errorf("шаги:\n  %s\nожидались:\n  %s", strings.Join(got, "\n  "), strings.Join(c.steps, "\n  "))
			}
			if args := coreArgs(out.String()); len(args) != 0 {
				t.Errorf("ядро запускалось:\n%s", out.String())
			}
		})
	}
}

// Без root подсказки — готовые команды: с sudo — тот же запуск, и режим,
// заданный флагом, в нём сохраняется (профиль мог бы выбрать socks); -config
// — и в ней, и в profile set.
func TestConnectTunRootHint(t *testing.T) {
	for _, c := range []struct {
		name    string
		profile []string
		args    []string
		sudo    string
	}{
		{"режим из профиля", nil, nil, adminHint + " csqtt-cli connect -config %s home;"},
		{"режим флагом", []string{"-datapath", "socks"}, []string{"-datapath", "tun"},
			adminHint + " csqtt-cli connect -datapath tun -config %s home;"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := stubTun(t)
			s.root = false
			te := newTestEnv(t)
			te.addHome(c.profile...)
			var out, errOut syncBuffer
			code := run(context.Background(), s.env(te, &out, &errOut),
				append(append([]string{"connect"}, c.args...), "-config", te.configPath(), "home"))
			if code != exitError {
				t.Errorf("код %d", code)
			}
			cfg := te.configPath()
			for _, want := range []string{
				fmt.Sprintf(c.sudo, cfg),
				fmt.Sprintf("csqtt-cli connect -datapath socks -config %s home (насовсем — "+
					"csqtt-cli profile set -config %s home -datapath socks)", cfg, cfg),
			} {
				if !strings.Contains(errOut.String(), want) {
					t.Errorf("нет %q:\n%s", want, errOut.String())
				}
			}
		})
	}
}

// С -allow-leak отказ таблицы — предупреждение, и туннель поднимается без
// защиты: метки у ядра нет (пропускать некому), окно капчи — без прокси.
func TestConnectTunAllowLeak(t *testing.T) {
	s := stubTun(t)
	s.guardErr = errors.New("nftables: операция не разрешена")
	te := newTestEnv(t)
	te.addHome()
	te.core = "connected"
	out, errOut, stop := connectTun(t, te, s, "-allow-leak")
	waitFor(t, out, errOut, "туннель работает")
	if code := stop(); code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "Предупреждение: защита от утечки DNS и kill-switch не включились") {
		t.Errorf("нет предупреждения:\n%s", errOut.String())
	}
	args := coreArgs(out.String())
	if len(args) != 1 || !strings.Contains(args[0], "--bind-if=3") || strings.Contains(args[0], "--mark") {
		t.Errorf("аргументы ядра: %q", args)
	}
	if s.bridge.Dial != nil || s.bridge.Direct != nil {
		t.Error("без kill-switch мосту капчи дали прокси или снятие")
	}
	noCaptchaProxyLine(t, out.String())
	if s.tun.opts.DNSGuard != nil {
		t.Errorf("адаптеру дали защиту %T, которой нет", s.tun.opts.DNSGuard)
	}
}

// Где защиты DNS и kill-switch нет, tun без -allow-leak —
// отказ до резерва адаптера, с готовыми командами: тот же запуск с
// -allow-leak и socks. Меню узнаёт отказ по типу и предложит socks. Без
// прав отказ сразу говорит и о них.
func TestConnectTunWithoutGuardNeedsAllowLeak(t *testing.T) {
	for _, c := range []struct {
		name, want string
		args       []string
		noRoot     bool
	}{
		{"режим из профиля", "подключиться так: csqtt-cli connect -allow-leak -config %s home;", nil, false},
		{"режим флагом", "подключиться так: csqtt-cli connect -allow-leak -datapath tun -config %s home;", []string{"-datapath", "tun"}, false},
		{"без прав", "подключиться так, " + adminHint + " csqtt-cli connect -allow-leak -config %s home;", nil, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := stubTun(t)
			s.root = !c.noRoot
			withTunGuard(t, errors.New("здесь он работает без защиты от утечки DNS и kill-switch"))
			te := newTestEnv(t)
			te.addHome()
			var out, errOut syncBuffer
			code := run(context.Background(), s.env(te, &out, &errOut),
				append(append([]string{"connect"}, c.args...), "-config", te.configPath(), "home"))
			if code != exitError {
				t.Errorf("код %d", code)
			}
			cfg := te.configPath()
			for _, want := range []string{
				"без защиты от утечки DNS и kill-switch, и DNS-запросы и трафик системы могут уходить мимо туннеля",
				fmt.Sprintf(c.want, cfg),
				fmt.Sprintf("или только SOCKS5: csqtt-cli connect -datapath socks -config %s home", cfg),
			} {
				if !strings.Contains(errOut.String(), want) {
					t.Errorf("нет %q:\n%s", want, errOut.String())
				}
			}
			if steps := s.j.list(); len(steps) != 0 {
				t.Errorf("до отказа встало: %v", steps)
			}
		})
	}
}

// С -allow-leak tun поднимается и там, где защиты нет, а без прав подсказка
// с правами сохраняет -allow-leak.
func TestConnectTunWithoutGuardAllowLeak(t *testing.T) {
	s := stubTun(t)
	withTunGuard(t, errors.New("защиты нет"))
	s.guardErr = errors.New("на этой системе их пока нет")
	te := newTestEnv(t)
	te.addHome()
	te.core = "connected"
	out, errOut, stop := connectTun(t, te, s, "-allow-leak")
	waitFor(t, out, errOut, "туннель работает")
	if code := stop(); code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "Предупреждение: защита от утечки DNS и kill-switch не включились") {
		t.Errorf("нет предупреждения:\n%s", errOut.String())
	}

	s = stubTun(t)
	withTunGuard(t, errors.New("защиты нет"))
	s.root = false
	te = newTestEnv(t)
	te.addHome()
	var out2, errOut2 syncBuffer
	code := run(context.Background(), s.env(te, &out2, &errOut2), []string{"connect", "-allow-leak", "-config", te.configPath(), "home"})
	if want := fmt.Sprintf("%s csqtt-cli connect -allow-leak -config %s home;", adminHint, te.configPath()); code != exitError ||
		!strings.Contains(errOut2.String(), want) {
		t.Errorf("код %d, нет %q:\n%s", code, want, errOut2.String())
	}
}

// Адаптер с каналом Link (Windows): ядро получает -listen-peer этого Link, а
// не -tun-uds, и дескриптор не передаётся.
func TestConnectTunOverLink(t *testing.T) {
	s := stubTun(t)
	link, err := datapath.ListenLink()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = link.Close() })
	s.link = link
	te := newTestEnv(t)
	te.addHome()
	te.core = "connected"
	out, errOut, stop := connectTun(t, te, s)
	waitFor(t, out, errOut, "туннель работает")
	if code := stop(); code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, errOut.String())
	}
	args := coreArgs(out.String())
	if len(args) != 1 || !strings.Contains(args[0], "--listen-peer="+link.Addr().String()) ||
		strings.Contains(args[0], "--tun-uds") {
		t.Errorf("аргументы ядра: %q", args)
	}
	if slices.Contains(s.j.list(), "дескриптор ядру") {
		t.Errorf("дескриптор передавался: %v", s.j.list())
	}
}

// -no-kill-switch и kill_switch = false: защита DNS есть, «закрыто» — нет;
// метка у ядра есть (её пропускает защита DNS), окно капчи — без прокси.
func TestConnectTunKillSwitchOff(t *testing.T) {
	for _, c := range []struct {
		name, line string
		profile    []string
		args       []string
	}{
		{"флаг", "kill-switch выключен (-no-kill-switch)", nil, []string{"-no-kill-switch"}},
		{"профиль", "kill-switch выключен (kill_switch = false в профиле)", []string{"-kill-switch=false"}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := stubTun(t)
			te := newTestEnv(t)
			te.addHome(c.profile...)
			te.core = "connected"
			out, errOut, stop := connectTun(t, te, s, c.args...)
			waitFor(t, out, errOut, "туннель работает")
			stop()
			if !strings.Contains(out.String(), c.line) {
				t.Errorf("нет %q:\n%s", c.line, out.String())
			}
			if slices.Contains(s.j.list(), "закрыто") {
				t.Errorf("kill-switch встал: %v", s.j.list())
			}
			args := coreArgs(out.String())
			if len(args) != 1 || strings.Contains(args[0], "--mark=") != (len(markFlags()) > 0) {
				t.Errorf("аргументы ядра: %q", args)
			}
			if s.bridge.Dial != nil || s.bridge.Direct != nil {
				t.Error("без kill-switch мосту капчи дали прокси или снятие")
			}
			noCaptchaProxyLine(t, out.String())
		})
	}
}

// noCaptchaProxyLine — без прокси окно капчи ходит в сеть само: вывод не
// должен обещать, что оно идёт через физический интерфейс.
func noCaptchaProxyLine(t *testing.T, out string) {
	t.Helper()
	if !strings.Contains(out, "сокеты ядра идут мимо туннеля через физический интерфейс") {
		t.Errorf("нет строки об интерфейсе ядра:\n%s", out)
	}
	if strings.Contains(out, "окно капчи —") || strings.Contains(out, "окна капчи идут") {
		t.Errorf("строка об окне капчи без прокси:\n%s", out)
	}
}

// socks — как в Э1: ни интерфейса, ни метки, ни таблицы; флаги tun ему не
// мешают и ничего не меняют.
func TestConnectSocksNoBindIf(t *testing.T) {
	s := stubTun(t)
	te := newTestEnv(t)
	socksHome(t, te)
	te.core = "connected"
	out, errOut, stop := connectTun(t, te, s, "-no-kill-switch", "-allow-leak")
	waitFor(t, out, errOut, "туннель работает")
	if code := stop(); code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, errOut.String())
	}
	args := coreArgs(out.String())
	if len(args) != 1 || strings.Contains(args[0], "--bind-if") || strings.Contains(args[0], "--mark") ||
		!strings.Contains(args[0], "--listen-peer") {
		t.Errorf("аргументы ядра: %q", args)
	}
	if steps := s.j.list(); len(steps) != 0 {
		t.Errorf("socks трогал систему: %v", steps)
	}
	if s.bridge.Dial != nil || s.bridge.Direct != nil {
		t.Error("в socks мосту капчи дали прокси или снятие")
	}
	if got := out.String() + errOut.String(); strings.Contains(got, "kill-switch") || strings.Contains(got, "защита") {
		t.Errorf("в socks — строки защиты:\n%s", got)
	}
}

// Смена сети: попытка перезапускается сразу, новое ядро получает новый
// индекс. Пропажа сети попытку не трогает.
func TestConnectTunNetworkChange(t *testing.T) {
	s := stubTun(t)
	te := newTestEnv(t)
	te.addHome()
	te.core = "connected"
	out, errOut, stop := connectTun(t, te, s)
	waitFor(t, out, errOut, "туннель работает")

	eth := netguard.Choice{Index4: 3, Alias4: "eth0"}
	wifi := netguard.Choice{Index4: 7, Alias4: "wlan0"}
	s.network(netguard.Choice{}, eth)
	waitFor(t, out, errOut, "сеть пропала")
	s.network(wifi, netguard.Choice{})
	waitFor(t, out, errOut, "--bind-if=7")
	if code := stop(); code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, errOut.String())
	}

	got := out.String()
	args := coreArgs(got)
	if len(args) != 2 || !strings.Contains(args[0], "--bind-if=3") || !strings.Contains(args[1], "--bind-if=7") {
		t.Errorf("аргументы ядра по попыткам: %q", args)
	}
	for _, want := range []string{
		"сеть сменилась: IPv4 — wlan0 (индекс 7)",
		"== переподключение: сеть сменилась",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("нет %q:\n%s", want, got)
		}
	}
	if !slices.Contains(s.j.list(), "rp_filter wlan0") {
		t.Errorf("обратный фильтр нового интерфейса не проверен: %v", s.j.list())
	}
}

// -net-switch bindif: новый интерфейс работающее ядро получает командой
// BIND_IF, без перезапуска. Интерфейс, который пропадал и вернулся тем же, —
// всё равно перезапуск.
func TestConnectTunNetworkChangeBindIf(t *testing.T) {
	s := stubTun(t)
	te := newTestEnv(t)
	te.addHome()
	te.core = "connected"
	out, errOut, stop := connectTun(t, te, s, "-net-switch", "bindif")
	waitFor(t, out, errOut, "туннель работает")

	eth := netguard.Choice{Index4: 3, Alias4: "eth0"}
	wifi := netguard.Choice{Index4: 7, Alias4: "wlan0"}
	s.network(wifi, eth)
	waitFor(t, out, errOut, "[П3] Интерфейс 3 → 7")
	if args := coreArgs(out.String()); len(args) != 1 {
		t.Fatalf("после BIND_IF ядро перезапускалось: %q", args)
	}
	s.network(wifi, wifi)
	waitFor(t, out, errOut, "--bind-if=7")
	if code := stop(); code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, errOut.String())
	}

	got := out.String()
	if args := coreArgs(got); len(args) != 2 || !strings.Contains(args[0], "--bind-if=3") ||
		!strings.Contains(args[1], "--bind-if=7") {
		t.Errorf("аргументы ядра по попыткам: %q", args)
	}
	for _, want := range []string{
		"сеть сменилась: IPv4 — wlan0 (индекс 7)",
		"ядро переходит на интерфейс 7 без перезапуска",
		"сеть переподключилась: IPv4 — wlan0 (индекс 7)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("нет %q:\n%s", want, got)
		}
	}
	if n := strings.Count(got, "== переподключение: сеть сменилась"); n != 1 {
		t.Errorf("переподключений по смене сети %d, ждали одно — на вернувшийся интерфейс:\n%s", n, got)
	}
	if !slices.Contains(s.j.list(), "rp_filter wlan0") {
		t.Errorf("обратный фильтр нового интерфейса не проверен: %v", s.j.list())
	}
}

// -net-switch bindif: смена того интерфейса, к которому ядро уже привязано, —
// перезапуск, а не BIND_IF на тот же индекс: кабель выдернули и вставили,
// сменился только IPv6.
func TestConnectTunSameInterfaceBindIf(t *testing.T) {
	s := stubTun(t)
	te := newTestEnv(t)
	te.addHome()
	te.core = "connected"
	out, errOut, stop := connectTun(t, te, s, "-net-switch", "bindif")
	waitFor(t, out, errOut, "туннель работает")

	eth := netguard.Choice{Index4: 3, Alias4: "eth0"}
	s.network(netguard.Choice{}, eth)
	waitFor(t, out, errOut, "сеть пропала")
	s.network(eth, netguard.Choice{})
	waitAttemptsCLI(t, out, errOut, 2)
	eth6 := netguard.Choice{Index4: 3, Alias4: "eth0", Index6: 5, Alias6: "eth1"}
	s.network(eth6, eth)
	waitAttemptsCLI(t, out, errOut, 3)
	if code := stop(); code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, errOut.String())
	}

	got := out.String()
	for i, a := range coreArgs(got) {
		if !strings.Contains(a, "--bind-if=3") {
			t.Errorf("попытка %d: аргументы ядра %q", i+1, a)
		}
	}
	if strings.Contains(got, "без перезапуска") {
		t.Errorf("ядру слали BIND_IF на тот же интерфейс:\n%s", got)
	}
}

// waitAttemptsCLI ждёт, пока поддельное ядро не запустят n раз.
func waitAttemptsCLI(t *testing.T, out, errOut *syncBuffer, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for len(coreArgs(out.String())) < n {
		if time.Now().After(deadline) {
			t.Fatalf("попыток меньше %d:\n%s\nstderr:\n%s", n, out.String(), errOut.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Негодное значение -net-switch — ошибка командной строки до подключения;
// bindif с socks — тоже: за сетью там не следят. В помощи флага нет.
func TestConnectNetSwitchFlag(t *testing.T) {
	s := stubTun(t)
	te := newTestEnv(t)
	te.addHome()
	code, _, stderr := te.run("connect", "-net-switch", "bind", "-config", te.configPath())
	if code != exitUsage || !strings.Contains(stderr, "-net-switch: restart или bindif") {
		t.Errorf("код %d, stderr:\n%s", code, stderr)
	}
	code, _, stderr = te.run("connect", "-datapath", "socks", "-net-switch", "bindif", "-config", te.configPath())
	if code != exitUsage || !strings.Contains(stderr, "-net-switch bindif — только с tun") {
		t.Errorf("socks: код %d, stderr:\n%s", code, stderr)
	}
	if steps := s.j.list(); len(steps) != 0 {
		t.Errorf("с негодным флагом connect трогал систему: %v", steps)
	}
	code, stdout, _ := te.run("connect", "-h")
	if code != exitOK || strings.Contains(stdout, "net-switch") {
		t.Errorf("код %d, помощь:\n%s", code, stdout)
	}
}

// Адаптер сломался сам: connect кончается ошибкой с подсказкой, всё
// снимается, как при остановке.
func TestConnectTunAdapterLost(t *testing.T) {
	s := stubTun(t)
	s.tun.failed = make(chan error, 1)
	te := newTestEnv(t)
	te.addHome()
	te.core = "connected"
	out, errOut, stop := connectTun(t, te, s)
	waitFor(t, out, errOut, "туннель работает")
	s.tun.failed <- &datapath.AdapterLostError{Name: datapath.TunName, Err: errors.New("система закрыла адаптер")}
	// connect должен кончиться сам: stop отменил бы его раньше.
	waitFor(t, errOut, out, "перестал работать")
	if code := stop(); code != exitError {
		t.Fatalf("код %d, stdout:\n%s\nstderr:\n%s", code, out.String(), errOut.String())
	}
	if msg := errOut.String(); !strings.Contains(msg, "перестал работать") || !strings.Contains(msg, "connect home") {
		t.Errorf("stderr:\n%s", msg)
	}
	steps := s.j.list()
	for _, want := range []string{"адаптер закрыт", "таблица закрыта", "резерв снят"} {
		if !slices.Contains(steps, want) {
			t.Errorf("нет шага %q: %v", want, steps)
		}
	}
	if n := len(coreArgs(out.String())); n != 1 {
		t.Errorf("попыток %d, ждали одну", n)
	}
}

// Сети нет при запуске: попытки ждут её, ядро не запускается; вернулась —
// перезапуск без задержки, ядро получает интерфейс.
func TestConnectTunNoNetworkAtStart(t *testing.T) {
	s := stubTun(t)
	s.choices = []netguard.Choice{{}}
	te := newTestEnv(t)
	te.addHome()
	te.core = "connected"
	out, errOut, stop := connectTun(t, te, s)
	waitFor(t, out, errOut, "переподключение через 1с: сети нет")
	s.network(netguard.Choice{Index4: 3, Alias4: "eth0"}, netguard.Choice{})
	waitFor(t, out, errOut, "туннель работает")
	stop()
	if args := coreArgs(out.String()); len(args) != 1 || !strings.Contains(args[0], "--bind-if=3") {
		t.Errorf("аргументы ядра: %q", args)
	}
}

// Подписаться на смену сети не вышло: строка об этом, а интерфейс
// выбирается заново перед каждой попыткой.
func TestConnectTunWithoutWatch(t *testing.T) {
	s := stubTun(t)
	s.watchErr = errors.New("netlink: нет прав")
	s.choices = []netguard.Choice{{Index4: 3, Alias4: "eth0"}, {Index4: 3, Alias4: "eth0"}, {Index4: 7, Alias4: "wlan0"}}
	te := newTestEnv(t)
	te.addHome()
	te.core = "crash"
	out, errOut, stop := connectTun(t, te, s)
	waitFor(t, out, errOut, "--bind-if=7")
	stop()
	got := out.String()
	for _, want := range []string{"за сменой сети не слежу (netlink: нет прав)", "сеть сменилась: IPv4 — wlan0 (индекс 7)"} {
		if !strings.Contains(got, want) {
			t.Errorf("нет %q:\n%s", want, got)
		}
	}
	if args := coreArgs(got); len(args) < 2 || !strings.Contains(args[0], "--bind-if=3") {
		t.Errorf("аргументы ядра: %q", args)
	}
}
