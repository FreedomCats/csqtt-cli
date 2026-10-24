package coreproc

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// realCoreConfig — запуск настоящего ядра без сервера: пир —
// документационный адрес, хеш выдуман. Пароль и хеш — заметные строки, чтобы
// искать их в командной строке.
var realCoreConfig = Config{
	Peer:       "192.0.2.1:46000",
	Password:   "real-core-password",
	Hashes:     []string{"real-core-hash"},
	Listen:     "127.0.0.1:0",
	ListenPeer: "127.0.0.1:9",
}

func realCorePath(t *testing.T) string {
	path := os.Getenv("CSQTT_TEST_CORE")
	if path == "" {
		t.Skip("CSQTT_TEST_CORE не задан")
	}
	return path
}

// TestRealCore — то же на настоящем ядре: CSQTT_TEST_CORE — абсолютный путь
// к csqtt-core (go run ./scripts/buildcore). Сервер не нужен. Ядро берёт
// секреты из stdin, поднимается, сообщает порт своего сокета, пробует VK и по
// STOP выходит само; пароля и хеша в его командной строке нет.
func TestRealCore(t *testing.T) {
	p, err := Start(realCorePath(t), realCoreConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.stop(0) })
	msgs, seen, drained := collect(p, func(m Message) bool { _, ok := m.(ListenEvent); return ok })
	waitFor(t, seen, "LISTEN")
	for _, m := range msgs() {
		if e, ok := m.(ListenEvent); ok {
			// Порт из события занят сокетом ядра.
			addr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: e.Port}
			if c, err := net.ListenUDP("udp", addr); err == nil {
				c.Close()
				t.Errorf("порт %d из LISTEN свободен", e.Port)
			}
		}
	}

	cmdline := commandLine(t, p.PID())
	if !strings.Contains(cmdline, "--secrets-stdin") || !strings.Contains(cmdline, "--listen-peer=127.0.0.1:9") ||
		strings.Contains(cmdline, realCoreConfig.Password) || strings.Contains(cmdline, realCoreConfig.Hashes[0]) {
		t.Errorf("командная строка ядра: %s", cmdline)
	}
	// Эпоху ядро приняло: с негодным --gen оно не дошло бы до LISTEN.
	if _, _, err := epochArgs(strings.Fields(cmdline)); err != nil {
		t.Errorf("эпоха в командной строке ядра: %v", err)
	}
	time.Sleep(2 * time.Second)

	start := time.Now()
	if err := p.Stop(); err != nil {
		t.Errorf("Stop() = %v", err)
	}
	if d := time.Since(start); d >= StopTimeout {
		t.Errorf("ядро не вышло по STOP за %v", d)
	}
	waitFor(t, drained, "закрытие Messages")
	checkRealCoreOutput(t, p, msgs())
}

// Секреты и STOP одной записью: строку секретов ядро читает так, что
// следующая за ней остаётся в stdin, и STOP доходит до канала управления.
// Перед STOP — ответ на капчу: ядро повторяет его в журнал, и токена там быть
// не должно.
func TestRealCoreSecretsThenStop(t *testing.T) {
	p, err := start(realCorePath(t), realCoreConfig.Args(), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.stop(0) })
	msgs, _, drained := collect(p, func(Message) bool { return false })
	const token = "real-core-captcha-token"
	if _, err := io.WriteString(p.stdin, realCoreConfig.SecretsLine()+"\n"+CaptchaResult(token)+"\n"+CmdStop+"\n"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, drained, "выход ядра по STOP, пришедшему вместе с секретами")
	checkRealCoreOutput(t, p, msgs())
	echo := ""
	for _, m := range msgs() {
		if l, ok := m.(Log); ok && strings.Contains(l.Text, "[STDIN] CAPTCHA_RESULT|") {
			echo = l.Text
		}
	}
	if echo == "" || strings.Contains(echo, token) {
		t.Errorf("эхо CAPTCHA_RESULT в журнале ядра: %q", echo)
	}
}

// checkRealCoreOutput: PROCESS с pid ядра, STOPPED и ни одной фатальной
// ошибки — без секретов ядро упало бы сразу («Нужен -password»).
func checkRealCoreOutput(t *testing.T, p *Process, msgs []Message) {
	t.Helper()
	var pid, stopped bool
	for _, m := range msgs {
		switch m := m.(type) {
		case PIDEvent:
			pid = m.PID == p.PID()
		case StoppedEvent:
			stopped = true
		case Log:
			if strings.Contains(m.Text, "[ФАТАЛ]") {
				t.Errorf("ядро упало: %s", m.Text)
			}
		}
		if testing.Verbose() {
			t.Logf("%#v", m)
		}
	}
	if !pid || !stopped {
		t.Errorf("PROCESS с pid ядра: %v, STOPPED: %v", pid, stopped)
	}
}

// Проверка хешей на настоящем ядре: хеш выдуман, и VK ответит о нём ошибкой
// (или не ответит — без сети). Итог один, на этот хеш; хеш до ядра дошёл через
// stdin, а не аргументом.
func TestRealCoreCheckHashes(t *testing.T) {
	path := realCorePath(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	res, err := CheckHashes(ctx, path, Config{Hashes: realCoreConfig.Hashes}, func(l Log) {
		if testing.Verbose() {
			t.Logf("журнал: %s", l.Text)
		}
	})
	if err != nil {
		t.Fatalf("CheckHashes: %v", err)
	}
	if len(res) != 1 || res[0].Hash != realCoreConfig.Hashes[0] ||
		(res[0].Status != HashInvalid && res[0].Status != HashUnavailable) {
		t.Fatalf("итоги: %+v", res)
	}
	t.Logf("итог: %+v", res[0])
}

// loopbackIndex — индекс loopback этой машины.
func loopbackIndex(t *testing.T) uint32 {
	t.Helper()
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			return uint32(iface.Index)
		}
	}
	t.Skip("нет loopback")
	return 0
}

// Правка П3 на настоящем ядре: с -bind-if ядро поднимается, пишет, через
// какой интерфейс идут его сокеты, и принимает BIND_IF:. Интерфейс — loopback:
// до VK ядро так не дойдёт, но и мимо привязки не уйдёт.
func TestRealCoreBindIf(t *testing.T) {
	path := realCorePath(t)
	lo := loopbackIndex(t)
	cfg := realCoreConfig
	cfg.BindIf = lo
	p, err := Start(path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.stop(0) })
	bound := fmt.Sprintf("[П3] Сокеты ядра — через интерфейс %d", lo)
	changed := fmt.Sprintf("[П3] Интерфейс %d → %d", lo, lo)
	// Негодный индекс — первым: отказ должна узнать BindIfRejected, а
	// принятая следом команда — нет.
	msgs, seen, drained := collect(p, func(m Message) bool { l, ok := m.(Log); return ok && strings.HasPrefix(l.Text, changed) })
	if err := p.Send(BindIf(0x7fff_fff0)); err != nil {
		t.Fatal(err)
	}
	if err := p.Send(BindIf(lo)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, seen, changed)
	var rejected []string
	for _, m := range msgs() {
		if l, ok := m.(Log); ok && BindIfRejected(l.Text) {
			rejected = append(rejected, l.Text)
		}
	}
	if len(rejected) != 1 || !strings.Contains(rejected[0], "2147483632") {
		t.Errorf("отказ BIND_IF в журнале: %q", rejected)
	}
	if !slices.Contains(msgs(), Message(Log{Text: bound})) {
		t.Errorf("в журнале нет %q", bound)
	}
	if cmdline := commandLine(t, p.PID()); !strings.Contains(cmdline, fmt.Sprintf("--bind-if=%d", lo)) {
		t.Errorf("командная строка ядра: %s", cmdline)
	}
	if err := p.Stop(); err != nil {
		t.Errorf("Stop() = %v", err)
	}
	waitFor(t, drained, "закрытие Messages")
	checkRealCoreOutput(t, p, msgs())
}

// Индекс без интерфейса — отказ стартовать с понятной строкой, а не молча
// непривязанные сокеты.
func TestRealCoreBindIfRefused(t *testing.T) {
	cfg := realCoreConfig
	cfg.BindIf = 0x7fff_fff0
	p, err := Start(realCorePath(t), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.stop(0) })
	var fatal []string
	for m := range p.Messages() {
		if l, ok := m.(Log); ok && strings.Contains(l.Text, "[ФАТАЛ]") {
			fatal = append(fatal, l.Text)
		}
	}
	if err := p.Wait(); err == nil {
		t.Error("ядро с -bind-if без интерфейса вышло без ошибки")
	}
	if len(fatal) != 1 || !strings.Contains(fatal[0], "-bind-if 2147483632") || !strings.Contains(fatal[0], "нет интерфейса") {
		t.Errorf("фатальные строки журнала: %q", fatal)
	}
}

// auto_js на настоящем ядре без VK: сокеты ядра привязаны к loopback, и до VK
// оно не дойдёт. Секреты и токен одной записью ядро читает целиком: оно
// падает уже на запросе к VK, ошибкой сети, а не на данных auto_js. Первая
// строка вывода — о привязке сокетов, сразу после чтения секретов (по ней
// остановка шлёт команды); токена в выводе нет.
func TestRealCoreAutoJSBootstrap(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("только Linux: здесь привязка к loopback не выпускает ядро в сеть")
	}
	cfg := realCoreConfig
	cfg.VKHashMode, cfg.VKToken = VKHashModeAutoJS, "real-core-vk-token"
	cfg.BindIf = loopbackIndex(t)
	p, err := Start(realCorePath(t), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.stop(0) })
	input, err := cfg.startInput()
	if err != nil {
		t.Fatal(err)
	}
	bootstrap := strings.Split(input, "\n")[1][len(bootstrapPrefix):]

	var msgs []Message
	timeout := time.AfterFunc(2*time.Minute, func() { _ = p.cmd.Process.Kill() })
	defer timeout.Stop()
	for m := range p.Messages() {
		msgs = append(msgs, m)
		if testing.Verbose() {
			t.Logf("%#v", m)
		}
	}
	if err := p.Wait(); err == nil {
		t.Error("ядро без VK вышло без ошибки")
	}
	if len(msgs) == 0 || msgs[0] != (Log{Text: fmt.Sprintf("[П3] Сокеты ядра — через интерфейс %d", cfg.BindIf)}) {
		t.Errorf("первая строка ядра: %#v", msgs)
	}
	var fatal *FatalError
	for _, m := range msgs {
		text := fmt.Sprintf("%+v", m)
		if strings.Contains(text, cfg.VKToken) || strings.Contains(text, bootstrap) {
			t.Errorf("токен в выводе ядра: %s", text)
		}
		if _, ok := m.(PIDEvent); ok {
			t.Error("ядро без VK дошло до PROCESS")
		}
		if l, ok := m.(Log); ok {
			if f, ok := ParseFatal(l.Text); ok {
				fatal = f
			}
		}
	}
	if fatal == nil || fatal.Kind != FatalNetwork || !strings.HasPrefix(fatal.Text, "messages.getCallToken: ") {
		t.Errorf("фатальная ошибка ядра: %+v", fatal)
	}
}
