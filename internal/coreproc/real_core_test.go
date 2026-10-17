package coreproc

import (
	"io"
	"net"
	"os"
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
	DeviceID:   "test",
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
func TestRealCoreSecretsThenStop(t *testing.T) {
	p, err := start(realCorePath(t), realCoreConfig.Args(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.stop(0) })
	msgs, _, drained := collect(p, func(Message) bool { return false })
	if _, err := io.WriteString(p.stdin, realCoreConfig.SecretsLine()+"\n"+CmdStop+"\n"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, drained, "выход ядра по STOP, пришедшему вместе с секретами")
	checkRealCoreOutput(t, p, msgs())
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
