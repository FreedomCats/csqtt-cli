package coreproc

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Команды ядру (main.rs, start_control_input).
const (
	CmdStop   = "STOP"
	CmdPause  = "PAUSE"
	CmdResume = "RESUME"
	// CmdFinishCalls — в auto_js при следующей остановке завершить
	// созданный ядром звонок (calls.forceFinish), а не только выйти из него.
	// Сама по себе ничего не делает; StopFinishingCalls шлёт её вместе со
	// STOP.
	CmdFinishCalls = "FINISH_VK_CALLS"
)

// BindIf — команда сменить физический интерфейс (правка П3): новые сокеты
// ядра пойдут через интерфейс index, старые доживут своё. Действует, только
// если ядро запущено с Config.BindIf; отказ ядро пишет в журнал.
func BindIf(index uint32) string { return "BIND_IF:" + strconv.FormatUint(uint64(index), 10) }

// BindIfRejected — строка журнала ядра об отказе BIND_IF: ядро запущено без
// -bind-if, индекс негоден или интерфейса нет. Сокеты тогда остались на
// прежнем интерфейсе (bind_if.rs). Отдельного события у отказа нет.
func BindIfRejected(text string) bool {
	return strings.Contains(text, "[П3] BIND_IF") && strings.Contains(text, "отклонён")
}

// CaptchaResult — ответ на CaptchaRequest: токен решённой капчи.
func CaptchaResult(token string) string { return "CAPTCHA_RESULT|" + token }

// CaptchaFailed — ответ на CaptchaRequest, если капчу решить не удалось.
func CaptchaFailed(reason string) string { return "CAPTCHA_RESULT|error:" + reason }

// StopTimeout — сколько Stop ждёт ядро после STOP, прежде чем убить. В
// auto_js сроки свои (StopProcessTimeoutAutoJS, StopCleanupTimeoutAutoJS).
const StopTimeout = 3 * time.Second

// maxLine — длиннее строку вывода ядро не пишет; хвост сверх неё
// отбрасывается.
const maxLine = 1 << 20

// Process — запущенное ядро.
type Process struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	messages chan Message

	sendMu sync.Mutex

	// autoJS — ядро запущено в auto_js: свой журнал и своя остановка.
	autoJS bool
	// output закрывается на первой строке вывода ядра, processed — на
	// событии PROCESS. Закрывает их горутина чтения до того, как отдать
	// строку в messages: остановка не должна зависеть от того, читают ли
	// Messages.
	output, processed         chan struct{}
	outputOnce, processedOnce sync.Once
	// call — что ядро в auto_js сказало о созданном звонке.
	callMu sync.Mutex
	call   callState
	// killed — ядро убито Kill: остановка не дождалась уборки.
	killMu sync.Mutex
	killed bool

	done chan struct{}
	err  error
}

// Start запускает ядро path с аргументами cfg.Args() и первой строкой stdin
// пишет ему cfg.SecretsLine(), а в auto_js той же записью — строку с токеном
// VK (cfg.VKToken). Ядро в auto_js без токена не запускается: ErrNoVKToken.
//
// Ядро не видит консоли оболочки, и Ctrl+C до него не доходит: остановить
// его — дело оболочки (Stop). Если оболочка умрёт, ядро умрёт вместе с ней:
// на Windows его держит задание (Job Object) с KILL_ON_JOB_CLOSE, на Linux —
// Pdeathsig.
//
// Messages нужно читать до закрытия канала: пока его не читают, ядро стоит
// на записи в stdout.
//
// Решатель капчи ядра выключен (CoreSolverOffEnv): капчу решает человек в
// окне оболочки. Токен капчи в журнале ядра Messages отдаёт скрытым, а в
// auto_js — и хеш созданного ядром звонка.
func Start(path string, cfg Config) (*Process, error) {
	return startConfig(path, cfg, nil)
}

// startConfig — Start с добавочным окружением (для тестов).
func startConfig(path string, cfg Config, env []string) (*Process, error) {
	input, err := cfg.startInput()
	if err != nil {
		return nil, err
	}
	p, err := start(path, cfg.Args(), env, cfg.AutoJS())
	if err != nil {
		return nil, err
	}
	// Ошибку записи не возвращаем: она бывает, только если ядро уже вышло,
	// и тогда причину скажут его журнал в Messages и код выхода в Wait.
	_ = p.write(input)
	return p, nil
}

// start — Start с готовыми аргументами и добавочным окружением (для тестов).
func start(path string, args, env []string, autoJS bool) (*Process, error) {
	cmd := exec.Command(path, args...)
	cmd.Env = append(append(os.Environ(), "CSQTT_EVENTS=1", CoreSolverOffEnv+"=1"), env...)
	cmd.SysProcAttr = sysProcAttr()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	p := &Process{
		cmd:       cmd,
		stdin:     stdin,
		messages:  make(chan Message, 64),
		autoJS:    autoJS,
		output:    make(chan struct{}),
		processed: make(chan struct{}),
		done:      make(chan struct{}),
	}
	started := make(chan error, 1)
	go p.run(stdout, stderr, started)
	if err := <-started; err != nil {
		return nil, fmt.Errorf("запуск ядра: %w", err)
	}
	return p, nil
}

// run запускает ядро и ждёт его в одной горутине: на Linux Pdeathsig
// срабатывает, когда завершается поток, создавший процесс, а не весь
// процесс оболочки. Поток закреплён за горутиной, пока ядро живо, и никакая
// другая горутина не завершит его раньше.
func (p *Process) run(stdout, stderr io.Reader, started chan<- error) {
	unlock := lockThread()
	defer unlock()
	if err := p.cmd.Start(); err != nil {
		started <- err
		return
	}
	guard, err := guardChild(p.cmd.Process)
	if err != nil {
		_ = p.cmd.Process.Kill()
		_ = p.cmd.Wait()
		started <- err
		return
	}
	started <- nil

	parseOut, parseErr := ParseLine, func(s string) Message { return Log{Text: maskLog(s)} }
	if p.autoJS {
		parseOut, parseErr = parseAutoJS(parseOut), parseAutoJS(parseErr)
	}
	var wg sync.WaitGroup
	wg.Go(func() { p.read(stdout, parseOut) })
	wg.Go(func() { p.read(stderr, parseErr) })
	wg.Wait()
	p.err = p.cmd.Wait()
	guard.Close()
	close(p.messages)
	close(p.done)
}

// read читает строки из r до конца и отдаёт непустые в messages.
func (p *Process) read(r io.Reader, parse func(string) Message) {
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := readLine(br)
		if line != "" {
			m := parse(line)
			p.observe(m)
			p.messages <- m
		}
		if err != nil {
			return
		}
	}
}

// observe отмечает, что ядро уже сказало: первую строку, PROCESS и в auto_js
// — строки о созданном звонке.
func (p *Process) observe(m Message) {
	p.outputOnce.Do(func() { close(p.output) })
	switch m := m.(type) {
	case PIDEvent:
		p.processedOnce.Do(func() { close(p.processed) })
	case Log:
		if p.autoJS {
			p.callMu.Lock()
			p.call.observe(m.Text)
			p.callMu.Unlock()
		}
	}
}

// parseAutoJS — разбор строки ядра в auto_js: хеш созданного звонка прячется
// в журнале (maskCallHash) и не отдаётся в CallUnavailableEvent — оболочке он
// не нужен, а ссылка на звонок чувствительна.
func parseAutoJS(parse func(string) Message) func(string) Message {
	return func(line string) Message {
		switch m := parse(line).(type) {
		case Log:
			return Log{Text: maskCallHash(m.Text)}
		case CallUnavailableEvent:
			m.Hash = ""
			return m
		default:
			return m
		}
	}
}

// readLine — строка без \n и \r; длиннее maxLine обрезается.
func readLine(br *bufio.Reader) (string, error) {
	var b []byte
	for {
		chunk, err := br.ReadSlice('\n')
		if len(b) < maxLine {
			b = append(b, chunk[:min(len(chunk), maxLine-len(b))]...)
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		return strings.TrimRight(string(b), "\r\n"), err
	}
}

// Messages — вывод ядра по строкам. Закрывается, когда ядро вышло и его
// вывод прочитан до конца.
func (p *Process) Messages() <-chan Message { return p.messages }

// PID — pid процесса ядра.
func (p *Process) PID() int { return p.cmd.Process.Pid }

// Send пишет команду в stdin ядра.
//
// В auto_js команду, записанную до первой строки вывода ядра, оно может
// выбросить вместе со строкой токена (startInput), а до PROCESS команд не
// читает вовсе. Остановку поэтому шлёт Stop, а не Send.
func (p *Process) Send(cmd string) error {
	if strings.ContainsAny(cmd, "\r\n") {
		return errors.New("команда ядру не может содержать перевод строки")
	}
	return p.write(cmd + "\n")
}

// write пишет s в stdin ядра одной записью.
func (p *Process) write(s string) error {
	p.sendMu.Lock()
	defer p.sendMu.Unlock()
	_, err := io.WriteString(p.stdin, s)
	return err
}

// Wait ждёт выхода ядра и прочтения его вывода. Ошибка — как у exec.Cmd.Wait.
func (p *Process) Wait() error {
	<-p.done
	return p.err
}

// Stop посылает ядру STOP и ждёт StopTimeout; не вышло — убивает. Ошибка —
// как у Wait: убитое ядро вернёт ошибку выхода.
//
// В auto_js — по правилам stopAutoJS, и созданный ядром звонок не
// завершается: создатель из него только выходит. Не подтвердило ядро и
// этого или убито — *CallLeftError.
func (p *Process) Stop() error {
	if p.autoJS {
		return p.stopAutoJS(false, StopProcessTimeoutAutoJS, StopCleanupTimeoutAutoJS)
	}
	return p.stop(StopTimeout)
}

// StopFinishingCalls — Stop, который в auto_js ещё и завершает созданный
// ядром звонок: FINISH_VK_CALLS и STOP одной записью. Звонок мог остаться —
// ядро убито или не подтвердило завершение — *CallLeftError. Не в auto_js —
// то же, что Stop.
func (p *Process) StopFinishingCalls() error {
	if p.autoJS {
		return p.stopAutoJS(true, StopProcessTimeoutAutoJS, StopCleanupTimeoutAutoJS)
	}
	return p.Stop()
}

func (p *Process) stop(timeout time.Duration) error {
	if p.Send(CmdStop) == nil {
		t := time.NewTimer(timeout)
		defer t.Stop()
		select {
		case <-p.done:
			return p.err
		case <-t.C:
		}
	}
	// Ядро уже могло выйти само — тогда Kill вернёт ошибку, и она не важна.
	_ = p.cmd.Process.Kill()
	return p.Wait()
}

// stopAutoJS останавливает ядро в auto_js; finish — завершить звонок.
//
// Команды остановки уходят не раньше первой строки вывода ядра или его
// выхода. Команду, попавшую в канал до того, как ядро прочло секреты, оно
// выбросит вместе со строкой токена (startInput), а до чтения секретов ядро
// ничего не пишет, кроме «[ФАТАЛ]» перед выходом. Ждать недолго: первая
// строка — «[П3] Сокеты ядра — через интерфейс …» сразу после секретов
// (с -bind-if), без -bind-if — «[VK JS] Сессия Calls подтверждена» перед
// созданием звонка. До PROCESS ядро команд всё равно не исполняет.
//
// Сроки: PROCESS — processTimeout с вызова (до него ядро создаёт звонок),
// после PROCESS — cleanupTimeout на уборку. Не уложилось — ядро убивается.
func (p *Process) stopAutoJS(finish bool, processTimeout, cleanupTimeout time.Duration) error {
	deadline := time.NewTimer(processTimeout)
	defer deadline.Stop()
	select {
	case <-p.output:
	case <-p.done:
		return p.stopped(finish, false)
	case <-deadline.C:
		return p.kill(finish)
	}
	cmds := CmdStop + "\n"
	if finish {
		cmds = CmdFinishCalls + "\n" + cmds
	}
	// Ошибка записи бывает, только если ядро уже вышло: тогда done
	// закроется и так.
	_ = p.write(cmds)
	select {
	case <-p.processed:
	case <-p.done:
		return p.stopped(finish, false)
	case <-deadline.C:
		return p.kill(finish)
	}
	cleanup := time.NewTimer(cleanupTimeout)
	defer cleanup.Stop()
	select {
	case <-p.done:
		return p.stopped(finish, false)
	case <-cleanup.C:
		return p.kill(finish)
	}
}

// Kill убивает ядро сразу, не дожидаясь уборки, — когда ждать остановку
// человек не хочет (второй Ctrl+C): в auto_js она длится до
// StopProcessTimeoutAutoJS + StopCleanupTimeoutAutoJS. Идущая остановка
// (Stop, StopFinishingCalls) тогда возвращается сразу, в auto_js — с
// *CallLeftError, если ядро не успело подтвердить уборку звонка. Вышедшее
// ядро Kill не трогает.
func (p *Process) Kill() {
	p.killMu.Lock()
	defer p.killMu.Unlock()
	select {
	case <-p.done:
		return
	default:
	}
	if p.cmd.Process.Kill() == nil {
		p.killed = true
	}
}

// kill убивает ядро в auto_js, не дождавшись уборки.
func (p *Process) kill(finish bool) error {
	// Ядро уже могло выйти само — тогда Kill вернёт ошибку, и она не важна.
	_ = p.cmd.Process.Kill()
	return p.stopped(finish, true)
}

// stopped — итог остановки ядра в auto_js: ошибка выхода, а если звонок мог
// остаться, — она внутри *CallLeftError.
func (p *Process) stopped(finish, killed bool) error {
	err := p.Wait()
	// Kill держит замок, пока шлёт сигнал: ядро, убитое им, выходит не
	// раньше, и отметка к этому месту уже стоит.
	p.killMu.Lock()
	killed = killed || p.killed
	p.killMu.Unlock()
	p.callMu.Lock()
	unclean := p.call.unclean(finish)
	confirmed := p.call.confirmed(finish)
	p.callMu.Unlock()
	// Убитое ядро, успевшее подтвердить уборку, звонка не оставило.
	if killed && !confirmed || unclean {
		return &CallLeftError{Killed: killed, Err: err}
	}
	return err
}
