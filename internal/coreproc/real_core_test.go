package coreproc

import (
	"os"
	"testing"
	"time"
)

// TestRealCore — то же на настоящем ядре: CSQTT_TEST_CORE — абсолютный путь
// к csqtt-core (go run ./scripts/buildcore). Сервер не нужен: пир —
// документационный адрес, хеш выдуман. Ядро поднимается, пробует VK и по
// STOP выходит само.
func TestRealCore(t *testing.T) {
	path := os.Getenv("CSQTT_TEST_CORE")
	if path == "" {
		t.Skip("CSQTT_TEST_CORE не задан")
	}
	p, err := Start(path, Config{
		Peer:     "192.0.2.1:46000",
		Password: "test",
		Hashes:   []string{"fake-hash"},
		DeviceID: "test",
		Listen:   "127.0.0.1:0",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.stop(0) })
	msgs, seen, drained := collect(p, func(m Message) bool { _, ok := m.(PIDEvent); return ok })
	waitFor(t, seen, "PROCESS")
	time.Sleep(2 * time.Second)

	start := time.Now()
	if err := p.Stop(); err != nil {
		t.Errorf("Stop() = %v", err)
	}
	if d := time.Since(start); d >= StopTimeout {
		t.Errorf("ядро не вышло по STOP за %v", d)
	}
	waitFor(t, drained, "закрытие Messages")

	var pid, stopped bool
	for _, m := range msgs() {
		switch m := m.(type) {
		case PIDEvent:
			pid = m.PID == p.PID()
		case StoppedEvent:
			stopped = true
		}
		if testing.Verbose() {
			t.Logf("%#v", m)
		}
	}
	if !pid || !stopped {
		t.Errorf("PROCESS с pid ядра: %v, STOPPED: %v", pid, stopped)
	}
}
