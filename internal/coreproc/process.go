package coreproc

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Команды ядру (main.rs, start_control_input).
const (
	CmdStop   = "STOP"
	CmdPause  = "PAUSE"
	CmdResume = "RESUME"
)

// CaptchaResult — ответ на CaptchaRequest: токен решённой капчи.
func CaptchaResult(token string) string { return "CAPTCHA_RESULT|" + token }

// CaptchaFailed — ответ на CaptchaRequest, если капчу решить не удалось.
func CaptchaFailed(reason string) string { return "CAPTCHA_RESULT|error:" + reason }

// StopTimeout — сколько Stop ждёт ядро после STOP, прежде чем убить.
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

	done chan struct{}
	err  error
}

// Start запускает ядро path с аргументами cfg.Args() и первой строкой stdin
// пишет ему cfg.SecretsLine().
//
// Ядро не видит консоли оболочки, и Ctrl+C до него не доходит: остановить
// его — дело оболочки (Stop). Если оболочка умрёт, ядро умрёт вместе с ней:
// на Windows его держит задание (Job Object) с KILL_ON_JOB_CLOSE, на Linux —
// Pdeathsig.
//
// Messages нужно читать до закрытия канала: пока его не читают, ядро стоит
// на записи в stdout.
func Start(path string, cfg Config) (*Process, error) {
	return startConfig(path, cfg, nil)
}

// startConfig — Start с добавочным окружением (для тестов).
func startConfig(path string, cfg Config, env []string) (*Process, error) {
	p, err := start(path, cfg.Args(), env)
	if err != nil {
		return nil, err
	}
	// Ошибку записи не возвращаем: она бывает, только если ядро уже вышло,
	// и тогда причину скажут его журнал в Messages и код выхода в Wait.
	_ = p.Send(cfg.SecretsLine())
	return p, nil
}

// start — Start с готовыми аргументами и добавочным окружением (для тестов).
func start(path string, args, env []string) (*Process, error) {
	cmd := exec.Command(path, args...)
	cmd.Env = append(append(os.Environ(), "CSQTT_EVENTS=1"), env...)
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
		cmd:      cmd,
		stdin:    stdin,
		messages: make(chan Message, 64),
		done:     make(chan struct{}),
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

	var wg sync.WaitGroup
	wg.Go(func() { p.read(stdout, ParseLine) })
	wg.Go(func() { p.read(stderr, func(s string) Message { return Log{Text: s} }) })
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
			p.messages <- parse(line)
		}
		if err != nil {
			return
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
func (p *Process) Send(cmd string) error {
	if strings.ContainsAny(cmd, "\r\n") {
		return errors.New("команда ядру не может содержать перевод строки")
	}
	p.sendMu.Lock()
	defer p.sendMu.Unlock()
	_, err := io.WriteString(p.stdin, cmd+"\n")
	return err
}

// Wait ждёт выхода ядра и прочтения его вывода. Ошибка — как у exec.Cmd.Wait.
func (p *Process) Wait() error {
	<-p.done
	return p.err
}

// Stop посылает ядру STOP и ждёт StopTimeout; не вышло — убивает. Ошибка —
// как у Wait: убитое ядро вернёт ошибку выхода.
func (p *Process) Stop() error { return p.stop(StopTimeout) }

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
