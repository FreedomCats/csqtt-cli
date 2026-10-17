package supervisor

import (
	"context"
	"errors"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/FreedomCats/csqtt-cli/internal/coreproc"
	"github.com/FreedomCats/csqtt-cli/internal/datapath"
)

// TestRealCore — цикл на настоящем ядре: CSQTT_TEST_CORE — абсолютный путь к
// csqtt-core (go run ./scripts/buildcore). Сервер не нужен: хеш выдуман, VK
// отвечает, что звонка нет, и цикл останавливается с подсказкой — ядро
// остановлено, порт канала сброшен.
func TestRealCore(t *testing.T) {
	path := os.Getenv("CSQTT_TEST_CORE")
	if path == "" {
		t.Skip("CSQTT_TEST_CORE не задан")
	}
	link, err := datapath.ListenLink()
	if err != nil {
		t.Fatal(err)
	}
	defer link.Close()

	var mu sync.Mutex
	var states []State
	var pid, port int
	tun, err := NewTunnel(Options{
		CorePath: path,
		Config: coreproc.Config{
			Peer:     "192.0.2.1:46000",
			Password: "real-core-password",
			Hashes:   []string{"real-core-hash"},
			DeviceID: "test",
		},
		Link:     link,
		Datapath: &fakeDatapath{},
		OnTransition: func(tr Transition) {
			mu.Lock()
			defer mu.Unlock()
			states = append(states, tr.To)
		},
		OnMessage: func(m coreproc.Message) {
			mu.Lock()
			defer mu.Unlock()
			switch e := m.(type) {
			case coreproc.PIDEvent:
				pid = e.PID
			case coreproc.ListenEvent:
				port = e.Port
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	err = tun.Run(ctx)
	var he *HashesExhaustedError
	if !errors.As(err, &he) {
		t.Fatalf("Run() = %v, ожидался конец хешей", err)
	}
	t.Logf("Run: %v", err)

	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(states, []State{StateStarting, StateStopping}) {
		t.Errorf("состояния %v", states)
	}
	if pid == 0 || port == 0 {
		t.Errorf("PROCESS %d, LISTEN %d", pid, port)
	}
	if alive(pid) {
		t.Errorf("ядро (pid %d) живо после Run", pid)
	}
}
