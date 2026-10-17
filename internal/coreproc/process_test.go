package coreproc

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Поддельное ядро — этот же тестовый бинарь: с CSQTT_FAKE_CORE он не
// запускает тесты, а ведёт себя как ядро в заданном режиме. Флаги ядра
// (--peer=…) тогда никто не разбирает.
const fakeCoreEnv = "CSQTT_FAKE_CORE"

func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeCoreEnv); mode != "" {
		os.Exit(fakeCore(mode))
	}
	os.Exit(m.Run())
}

// sampleLog — строки stderr поддельного ядра в формате журнала ядра.
var sampleLog = []string{
	"[КЛИЕНТ] Воркеров: 18 (групп: 2, по 9)",
	"[TURN] Allocate: STUN error 486 (x3)",
}

func fakeCore(mode string) int {
	switch mode {
	// replay печатает образцы вывода, отвечает на команды в журнал и
	// выходит по STOP или закрытому stdin, как ядро.
	case "replay":
		if os.Getenv("CSQTT_EVENTS") != "1" || !slices.ContainsFunc(os.Args, func(a string) bool {
			return strings.HasPrefix(a, "--peer=")
		}) {
			fmt.Fprintln(os.Stderr, "[ФАТАЛ] нет CSQTT_EVENTS=1 или --peer")
			return 3
		}
		for _, s := range sampleOutput {
			if s.want != (StoppedEvent{}) {
				fmt.Println(s.line)
			}
		}
		for _, l := range sampleLog {
			fmt.Fprintln(os.Stderr, l)
		}
		in := bufio.NewScanner(os.Stdin)
		for in.Scan() {
			fmt.Fprintln(os.Stderr, "[STDIN] "+in.Text())
			if in.Text() == CmdStop {
				break
			}
		}
		fmt.Println(`__CSQTT_EVENT__|STOPPED|{}`)
		return 0
	// deaf не слушает stdin и не выходит сам. SIGPIPE не роняет его, как и
	// ядро на Rust. Сон, а не select {}: на пустом select рантайм Go решил
	// бы, что процесс завис, и уронил его — и тест смерти оболочки прошёл бы
	// зря.
	case "deaf":
		signal.Ignore(syscall.SIGPIPE)
		fmt.Printf("__CSQTT_EVENT__|PROCESS|{\"pid\":%d}\n", os.Getpid())
		_, _ = io.Copy(io.Discard, os.Stdin)
		for {
			time.Sleep(time.Hour)
		}
	// fail падает при запуске, как ядро без пароля.
	case "fail":
		fmt.Fprintln(os.Stderr, "[ФАТАЛ] [КЛИЕНТ] Нужен -password")
		return 1
	// shell — оболочка: запускает ядро deaf, печатает его pid, когда ядро
	// поднялось (событие PROCESS), и живёт, пока её не убьют.
	case "shell":
		p, err := start(os.Args[0], nil, []string{fakeCoreEnv + "=deaf"})
		if err != nil {
			fmt.Println(err)
			return 1
		}
		for m := range p.Messages() {
			if e, ok := m.(PIDEvent); ok {
				fmt.Println("PID", e.PID)
			}
		}
		return 0
	}
	return 2
}

func startFake(t *testing.T, mode string) *Process {
	t.Helper()
	cfg := Config{Peer: "203.0.113.10:46000", Password: "p", Hashes: []string{"fake-hash"}}
	p, err := start(os.Args[0], cfg.Args(), []string{fakeCoreEnv + "=" + mode})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.stop(0) })
	return p
}

// collect читает Messages до закрытия; seen закрывается на первом
// сообщении, для которого until вернёт true.
func collect(p *Process, until func(Message) bool) (msgs func() []Message, seen, drained <-chan struct{}) {
	var mu sync.Mutex
	var got []Message
	seenCh, drainedCh := make(chan struct{}), make(chan struct{})
	var once sync.Once
	go func() {
		defer close(drainedCh)
		for m := range p.Messages() {
			mu.Lock()
			got = append(got, m)
			mu.Unlock()
			if until(m) {
				once.Do(func() { close(seenCh) })
			}
		}
	}()
	return func() []Message {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(got)
	}, seenCh, drainedCh
}

func waitFor(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatalf("не дождались: %s", what)
	}
}

func TestProcessReplay(t *testing.T) {
	p := startFake(t, "replay")
	msgs, seen, drained := collect(p, func(m Message) bool { return m == Log{Text: "[STDIN] PAUSE"} })
	if err := p.Send(CmdPause); err != nil {
		t.Fatal(err)
	}
	waitFor(t, seen, "PAUSE в журнале ядра")

	start := time.Now()
	if err := p.Stop(); err != nil {
		t.Fatalf("Stop() = %v", err)
	}
	if d := time.Since(start); d >= StopTimeout {
		t.Errorf("ядро вышло по STOP за %v — похоже, его убили", d)
	}
	waitFor(t, drained, "закрытие Messages")

	var events []Message
	var logs []string
	for _, m := range msgs() {
		if l, ok := m.(Log); ok {
			logs = append(logs, l.Text)
		} else {
			events = append(events, m)
		}
	}
	var want []Message
	for _, s := range sampleOutput {
		want = append(want, s.want)
	}
	if !reflect.DeepEqual(events, want) {
		t.Errorf("события:\n%#v\nwant\n%#v", events, want)
	}
	slices.Sort(logs)
	wantLogs := append(slices.Clone(sampleLog), "[STDIN] PAUSE", "[STDIN] STOP")
	slices.Sort(wantLogs)
	if !slices.Equal(logs, wantLogs) {
		t.Errorf("журнал = %q, want %q", logs, wantLogs)
	}
	if err := p.Send(CmdResume); err == nil {
		t.Error("Send после выхода ядра не вернул ошибку")
	}
}

func TestStopKillsDeafCore(t *testing.T) {
	p := startFake(t, "deaf")
	_, seen, _ := collect(p, func(m Message) bool { _, ok := m.(PIDEvent); return ok })
	waitFor(t, seen, "PROCESS")

	const timeout = 300 * time.Millisecond
	start := time.Now()
	err := p.stop(timeout)
	d := time.Since(start)
	if d < timeout {
		t.Errorf("stop вернулся за %v, раньше срока %v", d, timeout)
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Errorf("stop() = %v, want ошибку выхода убитого ядра", err)
	}
}

func TestStartFailure(t *testing.T) {
	p := startFake(t, "fail")
	msgs, _, drained := collect(p, func(Message) bool { return false })
	err := p.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("Wait() = %v, want код 1", err)
	}
	waitFor(t, drained, "закрытие Messages")
	if got, want := msgs(), []Message{Log{Text: "[ФАТАЛ] [КЛИЕНТ] Нужен -password"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("вывод = %#v, want %#v", got, want)
	}

	if _, err := Start(os.Args[0]+".нет", Config{}); err == nil {
		t.Error("Start несуществующего бинаря не вернул ошибку")
	}
}

func TestSendRejectsNewline(t *testing.T) {
	var p Process
	for _, cmd := range []string{"STOP\nPAUSE", "x\r"} {
		if err := p.Send(cmd); err == nil {
			t.Errorf("Send(%q) не вернул ошибку", cmd)
		}
	}
}

// Оболочка умирает без уборки — ядро должно умереть вместе с ней: на
// Windows его убивает задание, на Linux — Pdeathsig.
func TestShellDeathKillsCore(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		t.Skip("только Windows и Linux")
	}
	shell := exec.Command(os.Args[0])
	shell.Env = append(os.Environ(), fakeCoreEnv+"=shell")
	out, err := shell.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := shell.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = shell.Process.Kill(); _ = shell.Wait() }()

	line := make(chan string, 1)
	go func() {
		s, _ := bufio.NewReader(out).ReadString('\n')
		line <- strings.TrimSpace(s)
	}()
	var pid int
	select {
	case s := <-line:
		if pid, err = strconv.Atoi(strings.TrimPrefix(s, "PID ")); err != nil {
			t.Fatalf("оболочка напечатала %q", s)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("оболочка не запустила ядро")
	}
	if !alive(pid) {
		t.Fatalf("ядро %d мертво ещё до смерти оболочки", pid)
	}

	if err := shell.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = shell.Wait()
	for deadline := time.Now().Add(10 * time.Second); alive(pid); {
		if time.Now().After(deadline) {
			killPID(pid)
			t.Fatalf("ядро %d пережило оболочку", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
