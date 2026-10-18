// lifecycle.go — цикл подключения: то единственное место в супервизоре, где
// есть время, горутины и внешний мир.
//
// Автомат (state.go) помнит, где мы; политика (policy.go) считает задержки и
// годность хешей; здесь они связываются с процессом ядра и datapath. Цикл
// запускает ядро на каждую попытку, раздаёт его сообщения, поднимает datapath
// по TUNCONF, обрывает затянувшуюся деградацию и ждёт назначенную задержку
// перед следующей попыткой.
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

// Datapath — куда идут пакеты туннеля: *datapath.Socks. Живёт весь сеанс:
// строит и закрывает его вызывающий, цикл зовёт Start на первый TUNCONF каждой
// попытки и UpdateDNS — на повторный.
type Datapath interface {
	Start(ctx context.Context, conf datapath.TunConf) error
	UpdateDNS(dns []netip.Addr) error
}

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
	// ListenPeer ставит цикл по Link.
	Config coreproc.Config

	// Link и Datapath — канал пакетов и datapath сеанса; обязательны.
	Link     Link
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

// StartupError — ядро вышло, не начав работу (до PROCESS): негодные аргументы
// или ядро не той версии. Перезапуск ничего не изменит.
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
	// сервер, без второго тест на задержки шёл бы минуту.
	start func(coreproc.Config) (Core, error)
	sleep func(context.Context, time.Duration) bool
}

// NewTunnel собирает цикл. Ошибку возвращает только на неполных настройках:
// ядро до Run не запускается.
func NewTunnel(opts Options) (*Tunnel, error) {
	switch {
	case opts.CorePath == "":
		return nil, errors.New("супервизор: не задан путь к ядру")
	case opts.Link == nil || opts.Datapath == nil:
		return nil, errors.New("супервизор: не заданы канал пакетов и datapath")
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
		sleep: sleepCtx,
	}, nil
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

		var delay time.Duration
		if errors.Is(res.err, ErrServerRestart) {
			// Перезапуск сервера — не неудача клиента.
			t.backoff.reset()
			delay = ServerRestartDelay
		} else {
			delay = t.backoff.next()
		}
		t.report(t.machine.Reconnect(res.err, delay))
		if !t.sleep(ctx, delay) {
			return nil
		}
	}
}

// result — чем кончилась попытка. final — следующей не будет.
type result struct {
	err   error
	final bool
}

// attempt проводит одну попытку: запускает ядро, ведёт его до выхода и
// возвращает причину конца.
func (t *Tunnel) attempt(ctx context.Context) result {
	t.report(t.machine.BeginAttempt())

	cfg := t.opts.Config
	cfg.Hashes = t.hashes.Alive()
	cfg.Listen = datapath.CoreListen
	cfg.ListenPeer = t.opts.Link.Addr().String()
	core, err := t.start(cfg)
	if err != nil {
		return result{err: fmt.Errorf("запуск ядра: %w", err), final: true}
	}
	// Порт вышедшего ядра может занять чужой процесс: пакеты туда не шлём.
	defer t.opts.Link.SetCorePort(0)

	actx, cancel := context.WithCancel(ctx)
	a := &attempt{tunnel: t, core: core, ctx: actx}
	// Порядок снятия: таймер, отмена (капча бросает ожидание), горутины.
	defer a.wg.Wait()
	defer cancel()
	defer a.stopDegradedTimer()

	return a.run(ctx.Done())
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
		case <-cancelled:
			cancelled = nil
			a.stop(result{})
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
		return result{err: &StartupError{Err: exitErr, Log: a.lastLog}, final: true}
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
		a.started = true
	case coreproc.ListenEvent:
		t.opts.Link.SetCorePort(uint16(e.Port))
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
		first := !a.dpUp
		a.dpUp, a.conf = true, conf
		if first {
			tr, moved := t.machine.DatapathUp()
			t.report(tr, moved)
			if moved {
				a.moved(tr)
			}
		}
	case !slices.Equal(conf.DNS, a.conf.DNS):
		if err := t.opts.Datapath.UpdateDNS(conf.DNS); err != nil {
			t.logf("DNS туннеля не сменился: %v", err)
			return
		}
		a.conf.DNS = conf.DNS
	}
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

// Interrupt возвращает контекст, отменяемый по первому Ctrl+C (SIGINT) или
// SIGTERM, и функцию снятия обработчика.
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
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

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
