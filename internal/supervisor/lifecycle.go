// lifecycle.go — цикл подключения: то единственное место в супервизоре, где
// есть время, горутины и внешний мир.
//
// Автомат (state.go) помнит, где мы; политика (policy.go) считает задержки и
// годность хешей; здесь они связываются с процессом ядра и datapath. Цикл
// запускает ядро на каждую попытку, раздаёт его сообщения, отдаёт ядру
// дескриптор адаптера (tun на Linux), поднимает datapath по TUNCONF, обрывает
// затянувшуюся деградацию и ждёт назначенную задержку перед следующей
// попыткой.
//
// Главное обязательство файла — ядро не переживает попытку: к возврату из
// попытки процесс вышел, а его вывод прочитан до конца.
package supervisor

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/FreedomCats/csqtt-cli/internal/coreproc"
	"github.com/FreedomCats/csqtt-cli/internal/datapath"
)

// DefaultDegradedRestart — сколько туннель может пробыть без связи, прежде чем
// ядро перезапустят. Воркеров ядро переподключает само, и короткий провал
// перезапуском не лечится; долгий — значит, ядро застряло. Порог —
// предварительный, его уточнит живой прогон.
const DefaultDegradedRestart = 30 * time.Second

// startupLogLines — сколько последних строк журнала ядра показать, если оно
// вышло, не начав работу.
const startupLogLines = 5

// Core — запущенное ядро: *coreproc.Process.
type Core interface {
	Messages() <-chan coreproc.Message
	Send(cmd string) error
	Stop() error
	Wait() error
}

// Link — канал пакетов с ядром: *datapath.Link. Его адрес ядро получает в
// -listen-peer, а порт ядра канал узнаёт из LISTEN.
type Link interface {
	Addr() netip.AddrPort
	SetCorePort(port uint16)
}

// TunFD — канал пакетов дескриптором адаптера (Linux, datapath tun):
// *datapath.Tun. Вместо -listen-peer ядро получает -tun-uds с новым именем на
// каждую попытку (SocketName), а когда оно запустилось (PROCESS), — новый
// дубликат дескриптора адаптера через этот сокет (SendFD).
type TunFD interface {
	SocketName() string
	SendFD(ctx context.Context, socket string, pid int) error
}

// Datapath — куда идут пакеты туннеля: *datapath.Socks, *datapath.Tun. Живёт
// весь сеанс: строит и закрывает его вызывающий, цикл зовёт Start на первый
// TUNCONF каждой попытки и UpdateDNS — на повторный. Если он ещё и
// datapath.Persistent, после неудачной попытки, до задержки, цикл зовёт
// Suspend.
type Datapath interface {
	Start(ctx context.Context, conf datapath.TunConf) error
	UpdateDNS(dns []netip.Addr) error
}

// defaultFDTimeout — сколько ждать, пока ядро примет дескриптор адаптера.
// Слушать сокет ядро начинает сразу после PROCESS, принимает за
// миллисекунды; срок — только чтобы зависшее ядро не держало попытку.
const defaultFDTimeout = 10 * time.Second

// CaptchaFunc решает капчу по запросу ядра и возвращает токен. Ждать она
// обязана не дольше ctx: он отменяется с концом попытки.
//
// Отказ уходит ядру как CAPTCHA_RESULT|error:<причина>: причина —
// Reason у *coreproc.CaptchaError, иначе текст ошибки. На
// coreproc.ErrCaptchaNoReply ядру не отвечают вовсе. Зовут её из отдельной
// горутины на каждый запрос, и следующий запрос может прийти, пока прежний
// вызов не вернулся.
type CaptchaFunc func(ctx context.Context, req coreproc.CaptchaRequest) (token string, err error)

// Options — из чего собирается цикл подключения.
type Options struct {
	// CorePath — путь к csqtt-core.
	CorePath string

	// Config — настройки ядра с паролем и хешами (config.Profile.Core и
	// секреты). Hashes перед каждой попыткой заменяются годными, Listen и
	// ListenPeer ставит цикл по Link, TunUDS — по TunFD.
	Config coreproc.Config

	// Link или TunFD — канал пакетов с ядром, ровно один из двух: Link —
	// loopback (socks), TunFD — дескриптор адаптера (tun на Linux).
	Link  Link
	TunFD TunFD

	// Datapath — datapath сеанса; обязателен.
	Datapath Datapath

	// Captcha решает капчу. nil — ядро сразу получает отказ: без окна решать
	// нечем.
	Captcha CaptchaFunc

	// OnMessage получает все сообщения ядра, включая журнал. nil — молчать.
	//
	// OnMessage, OnTransition и Logf не зовутся одновременно: получателю —
	// выводу в терминал — не нужен собственный замок.
	OnMessage func(coreproc.Message)

	// OnTransition получает состоявшуюся смену состояния. nil — молчать.
	OnTransition func(Transition)

	// Logf получает строки самого цикла: что хеш исключён, что DNS не
	// сменился. nil — молчать.
	Logf func(format string, args ...any)

	// KillSwitch — стоит ли сейчас kill-switch, набор «закрыто»: с ним
	// datapath, снимая защиту DNS в Suspend, закрывает трафик системы, а не
	// отпускает его мимо туннеля. Цикл спрашивает после Suspend, чтобы строка
	// о конце попытки говорила правду: kill-switch мог и не встать
	// (-allow-leak) или быть снят на время ручной капчи. nil — kill-switch
	// нет.
	KillSwitch func() bool

	// Prepare дописывает в настройки ядра перед каждой попыткой, до его
	// запуска, то, что между попытками меняется: индекс физического
	// интерфейса и метку (BindIf, Mark — правка П3). Ошибка — ядро не
	// запускается, попытка считается неудачной, и следующая начнётся через
	// обычную задержку. nil — настройки как в Config.
	Prepare func(cfg *coreproc.Config) error

	// DegradedRestart — сколько терпеть StateDegraded до перезапуска ядра.
	// 0 — DefaultDegradedRestart.
	DegradedRestart time.Duration
}

// ErrServerRestart — сервер перезапущен из панели (SERVER_RESTART).
var ErrServerRestart = errors.New("сервер перезапущен из панели")

// ServerError — ядро сообщило, что попытка окончена (ERROR с fatal).
type ServerError struct {
	Code    string
	Message string
}

// Rejected — сервер отказал (handshake_rejected): неверный или просроченный
// пароль, пароль другого устройства, другая версия протокола. Повтор тут не
// поможет, в отличие от молчания сервера (handshake_timeout).
func (e *ServerError) Rejected() bool { return e.Code == "handshake_rejected" }

// Error — текст ядра без служебной метки FATAL_*: он уже по-русски.
func (e *ServerError) Error() string {
	msg := e.Message
	if tag, rest, ok := strings.Cut(msg, ": "); ok && strings.HasPrefix(tag, "FATAL_") {
		msg = rest
	}
	if e.Rejected() {
		return "сервер CSQTT отказал в подключении: " + msg
	}
	return msg
}

// StartupError — ядро вышло, не начав работу (до PROCESS), а в этом Run оно
// ещё ни разу не работало: негодные аргументы или ядро не той версии.
// Перезапуск ничего не изменит. Если ядро уже доходило до PROCESS, такой выход
// — обычная неудача попытки (сеть до PROCESS ядру тоже нужна).
type StartupError struct {
	Err error
	// Log — последние строки журнала ядра: в них причина.
	Log []string
}

func (e *StartupError) Error() string {
	msg := "ядро вышло, не начав работу"
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	if len(e.Log) > 0 {
		msg += "\n  " + strings.Join(e.Log, "\n  ")
	}
	return msg
}

func (e *StartupError) Unwrap() error { return e.Err }

// retryable — тот же выход до PROCESS, когда аргументы уже годились (ядро
// в этом Run работало): одной строкой, с последней строкой журнала, без
// типа *StartupError — это не конец цикла.
func (e *StartupError) retryable() error {
	msg := "ядро вышло, не начав работу"
	if len(e.Log) > 0 {
		msg += " (" + e.Log[len(e.Log)-1] + ")"
	}
	if e.Err == nil {
		return errors.New(msg)
	}
	return fmt.Errorf("%s: %w", msg, e.Err)
}

// Tunnel — цикл подключения: ядро, datapath, автомат и политика перезапуска,
// связанные вместе.
//
// Экземпляр одноразовый: Run зовётся один раз и возвращается, когда туннель
// остановлен окончательно.
type Tunnel struct {
	opts    Options
	machine *Machine
	hashes  *HashPool
	backoff backoff

	// outMu сериализует OnMessage, OnTransition и Logf: строки цикла пишут и
	// горутины капчи.
	outMu sync.Mutex

	// start и sleep подменяются в тестах: без первого понадобились бы ядро и
	// сервер, без второго тест на задержки шёл бы минуту. fdTimeout —
	// defaultFDTimeout, в тестах короче.
	start     func(coreproc.Config) (Core, error)
	sleep     func(context.Context, time.Duration) bool
	fdTimeout time.Duration

	// Трафик системы у Persistent-datapath (tun); только горутина цикла.
	// routed — Start прошёл, и Suspend после него ещё не было; bypassed — на
	// время переподключения туннель снят, и об этом сказано строкой, blocked
	// — тогда стояло «закрыто».
	routed, bypassed, blocked bool

	// coreStarted — ядро в этом Run хоть раз дошло до PROCESS: аргументы
	// годны, и выход до PROCESS дальше — не их вина. Только горутина цикла.
	coreStarted bool

	// restart — просьба о перезапуске (Restart), ещё не исполненная. Одной
	// достаточно: перезапуск, попрошенный дважды, — всё равно один.
	restart chan error
}

// NewTunnel собирает цикл. Ошибку возвращает только на неполных настройках:
// ядро до Run не запускается.
func NewTunnel(opts Options) (*Tunnel, error) {
	switch {
	case opts.CorePath == "":
		return nil, errors.New("супервизор: не задан путь к ядру")
	case (opts.Link == nil) == (opts.TunFD == nil) || opts.Datapath == nil:
		return nil, errors.New("супервизор: нужны datapath и один канал пакетов — Link или TunFD")
	}
	hashes := NewHashPool(opts.Config.Hashes)
	if len(hashes.Alive()) == 0 {
		return nil, errors.New("супервизор: нет хешей звонков")
	}
	if opts.DegradedRestart <= 0 {
		opts.DegradedRestart = DefaultDegradedRestart
	}
	path := opts.CorePath
	return &Tunnel{
		opts:    opts,
		machine: New(),
		hashes:  hashes,
		start: func(cfg coreproc.Config) (Core, error) {
			return coreproc.Start(path, cfg)
		},
		sleep:     sleepCtx,
		fdTimeout: defaultFDTimeout,
		restart:   make(chan error, 1),
	}, nil
}

// Restart обрывает идущую попытку с причиной reason и начинает новую сразу:
// без задержки и со сброшенным счётчиком неудач. Идущую задержку перед
// попыткой прерывает. Зовётся из любой горутины — при смене сети: ядро с
// прежним -bind-if через новый интерфейс не пойдёт.
//
// Просьба, пришедшая между попытками, исполняется следующей: она и есть
// перезапуск. Маршруты на время перезапуска снимаются, как после любой
// неудачной попытки.
func (t *Tunnel) Restart(reason error) {
	select {
	case t.restart <- reason:
	default:
		// Просьба уже ждёт.
	}
}

// State — текущее состояние подключения. Нужно строке состояния, которая
// перерисовывается по таймеру, а не по переходам.
func (t *Tunnel) State() State { return t.machine.State() }

// Workers — сколько воркеров на линии.
func (t *Tunnel) Workers() int { return t.machine.Workers() }

// Hashes — пул хешей: какие ещё годны и какие исключены.
func (t *Tunnel) Hashes() *HashPool { return t.hashes }

// Run держит туннель, пока не отменят ctx или пока продолжать бессмысленно.
//
// Остановка по ctx ошибкой не считается и даёт nil. Ошибка — только когда цикл
// кончился сам: годных хешей не осталось (*HashesExhaustedError), сервер
// отказал (*ServerError с Rejected), ядро не запускается (*StartupError и
// ошибка запуска). Прочие отказы попытки ведут к следующей попытке.
//
// К возврату ядро вышло, горутины цикла закончились, автомат стоит в
// StateStopping.
func (t *Tunnel) Run(ctx context.Context) error {
	var final error
	defer func() { t.report(t.machine.Stop(final)) }()

	for {
		if ctx.Err() != nil {
			return nil
		}
		res := t.attempt(ctx)
		if ctx.Err() != nil {
			// Остановку попросили — не отказ, и следующей попытки не будет.
			return nil
		}
		if res.final {
			final = res.err
			return res.err
		}

		t.suspend()
		var delay time.Duration
		switch {
		case res.restart:
			// Перезапуск попросили (сеть сменилась): новая сеть — новый счёт.
			t.backoff.reset()
		case errors.Is(res.err, ErrServerRestart):
			// Перезапуск сервера — не неудача клиента.
			t.backoff.reset()
			delay = ServerRestartDelay
		default:
			delay = t.backoff.next()
		}
		t.report(t.machine.Reconnect(res.err, delay))
		if !t.pause(ctx, delay) {
			return nil
		}
	}
}

// pause ждёт задержку перед следующей попыткой. Restart её прерывает и
// сбрасывает счётчик неудач. false — цикл остановлен.
func (t *Tunnel) pause(ctx context.Context, delay time.Duration) bool {
	sctx, wake := context.WithCancel(ctx)
	woken := make(chan bool, 1)
	go func() {
		select {
		case <-t.restart:
			woken <- true
			wake()
		case <-sctx.Done():
			woken <- false
		}
	}()
	slept := t.sleep(sctx, delay)
	wake()
	// Горутина должна кончиться здесь: иначе она забрала бы просьбу,
	// пришедшую уже следующей попытке.
	if <-woken {
		t.backoff.reset()
		return ctx.Err() == nil
	}
	return slept && ctx.Err() == nil
}

// result — чем кончилась попытка. final — следующей не будет; restart —
// следующая начинается сразу (Restart).
type result struct {
	err     error
	final   bool
	restart bool
}

// attempt проводит одну попытку: запускает ядро, ведёт его до выхода и
// возвращает причину конца.
func (t *Tunnel) attempt(ctx context.Context) result {
	t.report(t.machine.BeginAttempt())
	// Эта попытка и есть перезапуск, о котором могли попросить раньше.
	select {
	case <-t.restart:
	default:
	}

	cfg := t.opts.Config
	cfg.Hashes = t.hashes.Alive()
	if t.opts.Prepare != nil {
		if err := t.opts.Prepare(&cfg); err != nil {
			return result{err: err}
		}
	}
	var socket string
	if t.opts.TunFD != nil {
		socket = t.opts.TunFD.SocketName()
		cfg.TunUDS = socket
	} else {
		cfg.Listen = datapath.CoreListen
		cfg.ListenPeer = t.opts.Link.Addr().String()
	}
	core, err := t.start(cfg)
	if err != nil {
		return result{err: fmt.Errorf("запуск ядра: %w", err), final: true}
	}
	if t.opts.Link != nil {
		// Порт вышедшего ядра может занять чужой процесс: пакеты туда не шлём.
		defer t.opts.Link.SetCorePort(0)
	}

	actx, cancel := context.WithCancel(ctx)
	a := &attempt{tunnel: t, core: core, ctx: actx, socket: socket,
		fdSent: t.opts.TunFD == nil, fdDone: make(chan error, 1)}
	// Порядок снятия: таймер, отмена (капча бросает ожидание), горутины.
	defer a.wg.Wait()
	defer cancel()
	defer a.stopDegradedTimer()

	return a.run(ctx.Done())
}

// suspend снимает маршруты, DNS системы и защиту DNS Persistent-datapath
// между попытками: пока маршруты ведут в туннель без ядра, страница капчи VK
// не загрузится. Отказ только пишется: следующая попытка всё равно начнётся.
//
// Если туннель в этой попытке был поднят, строка говорит, куда теперь идёт
// трафик системы: окно мимо туннеля нужно видеть, а не узнавать из README.
// С kill-switch трафик закрыт — так и сказано.
func (t *Tunnel) suspend() {
	p, ok := t.opts.Datapath.(datapath.Persistent)
	if !ok {
		return
	}
	if err := p.Suspend(); err != nil {
		t.logf("туннель между попытками снят не полностью (маршруты, DNS системы, защита DNS): %v", err)
	}
	blocked := t.opts.KillSwitch != nil && t.opts.KillSwitch()
	if !t.routed {
		return
	}
	t.routed = false
	t.bypassed, t.blocked = true, blocked
	if blocked {
		t.logf("туннель переподключается — трафик системы заблокирован (kill-switch)")
	} else {
		t.logf("маршруты сняты на время переподключения — трафик и DNS системы идут мимо туннеля")
	}
}

// routedUp — Start Persistent-datapath прошёл: трафик системы снова идёт в
// туннель. Если между попытками об обратном сказано строкой — строка, что
// он вернулся.
func (t *Tunnel) routedUp() {
	if _, ok := t.opts.Datapath.(datapath.Persistent); !ok {
		return
	}
	t.routed = true
	if !t.bypassed {
		return
	}
	t.bypassed = false
	if t.blocked {
		t.logf("туннель снова поднят — трафик системы идёт через него")
	} else {
		t.logf("маршруты возвращены — трафик системы снова идёт через туннель")
	}
}

// report отдаёт состоявшийся переход наружу. Пару (Transition, bool) принимает
// как есть, чтобы вызов писался в одну строку: t.report(t.machine.Stop(nil)).
func (t *Tunnel) report(tr Transition, moved bool) {
	if !moved || t.opts.OnTransition == nil {
		return
	}
	t.outMu.Lock()
	defer t.outMu.Unlock()
	t.opts.OnTransition(tr)
}

// logf отдаёт наружу строку самого цикла.
func (t *Tunnel) logf(format string, args ...any) {
	if t.opts.Logf == nil {
		return
	}
	t.outMu.Lock()
	defer t.outMu.Unlock()
	t.opts.Logf(format, args...)
}

// ──────────────────────────── попытка ────────────────────────────

// attempt — состояние одной попытки. Живёт ровно столько, сколько один
// процесс ядра; поля, кроме wg, трогает только горутина цикла.
type attempt struct {
	tunnel *Tunnel
	core   Core
	ctx    context.Context
	wg     sync.WaitGroup

	started  bool   // пришёл PROCESS: ядро разобрало аргументы и работает
	stopping bool   // ядру послан Stop
	res      result // почему послан Stop

	dpUp bool             // datapath поднят в этой попытке
	conf datapath.TunConf // с каким TUNCONF

	// Дескриптор адаптера (TunFD): socket — значение -tun-uds этой попытки,
	// fdSending — передача начата, fdSent — ядро дескриптор приняло (без
	// TunFD — с начала попытки), fdDone — итог передачи из её горутины.
	socket    string
	fdSending bool
	fdSent    bool
	fdDone    chan error
	upShown   bool // DatapathUp автомату уже сообщён

	degraded *time.Timer
	lastLog  []string
}

// run читает вывод ядра до конца — иначе оно встанет на записи — и решает,
// чем кончилась попытка.
func (a *attempt) run(cancelled <-chan struct{}) result {
	msgs := a.core.Messages()
	for msgs != nil {
		var degradedC <-chan time.Time
		if a.degraded != nil {
			degradedC = a.degraded.C
		}
		select {
		case m, ok := <-msgs:
			if !ok {
				msgs = nil
				continue
			}
			a.handle(m)
		case err := <-a.fdDone:
			a.fdHandedOff(err)
		case <-cancelled:
			cancelled = nil
			a.stop(result{})
		case reason := <-a.tunnel.restart:
			if a.stopping {
				// Попытка и так кончается — следующая начнётся сразу.
				a.res.restart = true
			} else {
				a.stop(result{err: reason, restart: true})
			}
		case <-degradedC:
			a.stopDegradedTimer()
			a.stop(result{err: fmt.Errorf("туннель без связи дольше %s — ядро перезапускается",
				a.tunnel.opts.DegradedRestart)})
		}
	}

	exitErr := a.core.Wait()
	switch {
	case a.stopping:
		return a.res
	case !a.started:
		startup := &StartupError{Err: exitErr, Log: a.lastLog}
		if a.tunnel.coreStarted {
			// До PROCESS ядро уже ходит в сеть: проверяет интерфейс -bind-if и
			// разрешает имя сервера. В новой сети, которая ещё не пропускает
			// наружу, это кончается выходом — обычная неудача, а не конец.
			return result{err: startup.retryable()}
		}
		return result{err: startup, final: true}
	case exitErr != nil:
		return result{err: fmt.Errorf("ядро завершилось: %w", exitErr)}
	default:
		return result{err: errors.New("ядро завершилось само")}
	}
}

// handle разбирает одно сообщение ядра.
func (a *attempt) handle(m coreproc.Message) {
	t := a.tunnel

	tr, moved := t.machine.Observe(m)
	// Сообщение и вызванный им переход уходят под одним замком: иначе строка
	// цикла из горутины капчи могла бы вклиниться между ними.
	t.outMu.Lock()
	if t.opts.OnMessage != nil {
		t.opts.OnMessage(m)
	}
	if moved && t.opts.OnTransition != nil {
		t.opts.OnTransition(tr)
	}
	t.outMu.Unlock()
	if moved {
		a.moved(tr)
	}

	switch e := m.(type) {
	case coreproc.PIDEvent:
		a.started, t.coreStarted = true, true
		a.sendFD(e.PID)
	case coreproc.ListenEvent:
		if t.opts.Link != nil {
			t.opts.Link.SetCorePort(uint16(e.Port))
		}
	case coreproc.ConfigEvent:
		a.config(e)
	case coreproc.ErrorEvent:
		if e.Fatal {
			err := &ServerError{Code: e.Code, Message: e.Message}
			a.stop(result{err: err, final: err.Rejected()})
		}
	case coreproc.CallUnavailableEvent:
		a.callUnavailable(e)
	case coreproc.ServerRestartEvent:
		a.stop(result{err: ErrServerRestart})
	case coreproc.CaptchaRequest:
		a.captcha(e)
	case coreproc.Log:
		if !a.started {
			a.lastLog = append(a.lastLog, e.Text)
			if len(a.lastLog) > startupLogLines {
				a.lastLog = slices.Delete(a.lastLog, 0, 1)
			}
		}
	}
}

// moved — побочные действия перехода: сброс задержек и таймер деградации.
func (a *attempt) moved(tr Transition) {
	if tr.To == StateConnected {
		// Туннель поднялся: задержки, заработанные прошлыми неудачами,
		// больше не в счёт.
		a.tunnel.backoff.reset()
	}
	if tr.From == StateDegraded {
		a.stopDegradedTimer()
	}
	if tr.To == StateDegraded {
		a.degraded = time.NewTimer(a.tunnel.opts.DegradedRestart)
	}
}

func (a *attempt) stopDegradedTimer() {
	if a.degraded != nil {
		a.degraded.Stop()
		a.degraded = nil
	}
}

// config — TUNCONF: первый в попытке поднимает datapath, повторный с другим
// DNS меняет DNS на ходу. Другой адрес — тот же Start: соединения на прежнем
// адресе всё равно не жильцы.
func (a *attempt) config(e coreproc.ConfigEvent) {
	t := a.tunnel
	conf := datapath.TunConf{Address: e.Address, DNS: e.DNS}
	switch {
	case !a.dpUp || conf.Address != a.conf.Address:
		if err := t.opts.Datapath.Start(a.ctx, conf); err != nil {
			// Ядро живо и держит воркеров, а подавать в них нечего.
			a.stop(result{err: fmt.Errorf("datapath: %w", err)})
			return
		}
		a.dpUp, a.conf = true, conf
		t.routedUp()
		a.datapathUp()
	case !slices.Equal(conf.DNS, a.conf.DNS):
		if err := t.opts.Datapath.UpdateDNS(conf.DNS); err != nil {
			t.logf("DNS туннеля не сменился: %v", err)
			return
		}
		a.conf.DNS = conf.DNS
	}
}

// datapathUp сообщает автомату, что datapath поднят: Start прошёл, а ядро
// получило дескриптор адаптера (с TunFD). Обычно дескриптор уходит задолго
// до TUNCONF, но туннель без него не работает, в каком бы порядке они ни
// пришли.
func (a *attempt) datapathUp() {
	if !a.dpUp || !a.fdSent || a.upShown {
		return
	}
	a.upShown = true
	t := a.tunnel
	tr, moved := t.machine.DatapathUp()
	t.report(tr, moved)
	if moved {
		a.moved(tr)
	}
}

// sendFD — ядро запустилось (PROCESS): с TunFD оно вот-вот откроет сокет
// -tun-uds, и ему уходит дубликат дескриптора адаптера. Передача ждёт
// ядро, поэтому — в своей горутине, итог приходит в run через fdDone.
func (a *attempt) sendFD(pid int) {
	t := a.tunnel
	if t.opts.TunFD == nil || a.fdSending {
		return
	}
	a.fdSending = true
	a.wg.Go(func() {
		ctx, cancel := context.WithTimeout(a.ctx, t.fdTimeout)
		defer cancel()
		a.fdDone <- t.opts.TunFD.SendFD(ctx, a.socket, pid)
	})
}

// fdHandedOff — итог передачи дескриптора. Отказ — конец попытки: ядро без
// адаптера держит воркеров, а подавать в них нечего.
func (a *attempt) fdHandedOff(err error) {
	if err != nil {
		if a.ctx.Err() == nil {
			a.stop(result{err: fmt.Errorf("дескриптор адаптера ядру: %w", err)})
		}
		return
	}
	a.fdSent = true
	a.datapathUp()
}

// callUnavailable исключает хеш из следующих попыток. Ядро тем временем
// снимает воркеров этого хеша само и держит остальные; годных не осталось —
// туннель останавливается.
func (a *attempt) callUnavailable(e coreproc.CallUnavailableEvent) {
	t := a.tunnel
	if !t.hashes.Exclude(e.Hash, e.Code) {
		return
	}
	alive := len(t.hashes.Alive())
	t.logf("звонок VK недоступен (ошибка VK %d) — хеш исключён из следующих попыток, годных осталось %d",
		e.Code, alive)
	if alive == 0 {
		a.stop(result{err: &HashesExhaustedError{Dead: t.hashes.Dead()}, final: true})
	}
}

// captcha отдаёт запрос капчи решателю и его ответ — ядру. Решатель ждёт
// человека до трёх минут, поэтому — в своей горутине: вывод ядра всё это время
// нужно читать.
func (a *attempt) captcha(req coreproc.CaptchaRequest) {
	solve := a.tunnel.opts.Captcha
	if solve == nil {
		_ = a.core.Send(coreproc.CaptchaFailed("unsupported"))
		return
	}
	a.wg.Go(func() {
		token, err := solve(a.ctx, req)
		if a.ctx.Err() != nil || errors.Is(err, coreproc.ErrCaptchaNoReply) {
			return // попытка кончилась или ядро этого ответа уже не ждёт
		}
		line := coreproc.CaptchaResult(token)
		if err != nil || token == "" || strings.ContainsAny(token, "\r\n") {
			reason := "empty"
			var ce *coreproc.CaptchaError
			switch {
			case errors.As(err, &ce) && ce.Reason != "":
				reason = ce.Reason
			case err != nil:
				reason = strings.Join(strings.Fields(err.Error()), " ")
			}
			line = coreproc.CaptchaFailed(reason)
		}
		_ = a.core.Send(line)
	})
}

// stop просит ядро выйти; первая причина побеждает: отказ datapath роняет
// воркеров, и деградация после этого — следствие, а не причина. Stop ждёт
// выхода ядра, поэтому — в горутине: вывод в это время читает run.
//
// Закрывая сессии, ядро отчитывается об уходе воркеров. Это не «туннель без
// связи», а наша же остановка: автомат с этого момента состояния не меняет
// (Drain), таймер деградации снимается.
func (a *attempt) stop(res result) {
	if a.stopping {
		return
	}
	a.stopping, a.res = true, res
	a.tunnel.machine.Drain()
	a.stopDegradedTimer()
	a.wg.Go(func() { _ = a.core.Stop() })
}

// ──────────────────────────── Ctrl+C ────────────────────────────

// Interrupt возвращает контекст, отменяемый по первому Ctrl+C (SIGINT),
// SIGTERM или SIGHUP, и функцию снятия обработчика. SIGHUP — закрыли
// терминал, в котором шёл connect (sudo передаёт его дальше): без уборки
// адаптер ушёл бы сам, а resolv.conf остался бы на резолвере туннеля до
// следующего запуска. На Windows SIGHUP не приходит, подписка на него ничего
// не меняет.
//
// Если SIGHUP при запуске игнорируется (nohup), на него не подписываемся:
// Notify снял бы игнорирование, и connect, запущенный под nohup, умирал бы с
// терминалом или сеансом SSH. Проверка — на момент вызова, до Notify.
//
// Второй Ctrl+C убивает процесс немедленно: по первому сигналу обработчик
// снимается, и следующий уходит системе, у которой поведение по умолчанию —
// завершить процесс. Ждать корректной остановки дважды пользователь не обязан.
// Ядро при этом не осиротеет: его держат Job Object и Pdeathsig (coreproc).
//
// Возвращённую функцию нужно вызвать при выходе: она снимает обработчик
// и отменяет контекст.
func Interrupt(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)

	signals := make(chan os.Signal, 1)
	stopOn := []os.Signal{os.Interrupt, syscall.SIGTERM}
	if !signal.Ignored(syscall.SIGHUP) {
		stopOn = append(stopOn, syscall.SIGHUP)
	}
	signal.Notify(signals, stopOn...)

	go func() {
		select {
		case <-signals:
			signal.Stop(signals)
			cancel()
		case <-ctx.Done():
			signal.Stop(signals)
		}
	}()

	return ctx, func() {
		signal.Stop(signals)
		cancel()
	}
}

// sleepCtx ждёт d или отмены ctx. Возвращает false, если ждать перестали.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
