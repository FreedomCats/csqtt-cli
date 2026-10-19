package supervisor

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/FreedomCats/csqtt-cli/internal/coreproc"
	"github.com/FreedomCats/csqtt-cli/internal/datapath"
)

// pairTunFD — канал дескриптором без адаптера: ядру уходит один конец пары
// сокетов. Адаптер требует root, а приём дескриптора ядром от него не
// зависит.
type pairTunFD struct {
	fd int

	mu      sync.Mutex
	sockets []string
	errs    []error
}

func (p *pairTunFD) SocketName() string { return datapath.TunSocketName() }

func (p *pairTunFD) SendFD(ctx context.Context, socket string, pid int) error {
	err := datapath.SendFD(ctx, socket, pid, p.fd)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sockets = append(p.sockets, socket)
	p.errs = append(p.errs, err)
	return err
}

// TestRealCoreTunFD — цикл на настоящем ядре в режиме -tun-uds
// (CSQTT_TEST_CORE, как у TestRealCore): ядро открывает сокет после PROCESS,
// принимает дескриптор от оболочки (П4 пропускает тот же uid) и
// подтверждает его; дальше — как у TestRealCore, до конца хешей.
func TestRealCoreTunFD(t *testing.T) {
	path := os.Getenv("CSQTT_TEST_CORE")
	if path == "" {
		t.Skip("CSQTT_TEST_CORE не задан")
	}
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(pair[0])
	defer unix.Close(pair[1])
	fd := &pairTunFD{fd: pair[0]}

	var mu sync.Mutex
	var received bool
	var pid int
	tun, err := NewTunnel(Options{
		CorePath: path,
		Config: coreproc.Config{
			Peer:     "192.0.2.1:46000",
			Password: "real-core-password",
			Hashes:   []string{"real-core-hash"},
			DeviceID: "test",
		},
		TunFD:    fd,
		Datapath: &fakeDatapath{},
		OnMessage: func(m coreproc.Message) {
			mu.Lock()
			defer mu.Unlock()
			switch e := m.(type) {
			case coreproc.PIDEvent:
				pid = e.PID
			case coreproc.Log:
				if strings.Contains(e.Text, "TUN FD успешно получен") {
					received = true
				}
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

	fd.mu.Lock()
	defer fd.mu.Unlock()
	mu.Lock()
	defer mu.Unlock()
	if len(fd.errs) != 1 || fd.errs[0] != nil {
		t.Fatalf("передачи дескриптора: %v", fd.errs)
	}
	if !received {
		t.Error("в журнале ядра нет «TUN FD успешно получен»")
	}
	if alive(pid) {
		t.Errorf("ядро (pid %d) живо после Run", pid)
	}
}
