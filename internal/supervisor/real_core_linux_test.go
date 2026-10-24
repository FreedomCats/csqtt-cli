package supervisor

import (
	"context"
	"errors"
	"fmt"
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

// Пара сокетов не пропадает, а TUN_LOST ядро на ней не шлёт.
func (p *pairTunFD) CheckAdapter() error { return nil }
func (p *pairTunFD) Lost(error)          {}

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

// seqpacketTunFD — канал дескриптором без адаптера, где «адаптер» можно
// оборвать: на каждую передачу — новая пара SOCK_SEQPACKET, ядру уходит один
// конец, другой остаётся у теста. Закрытый другой конец ядро читает как
// закрытый интерфейс (EOF) — так же, как TUN, который закрыли.
type seqpacketTunFD struct {
	mu    sync.Mutex
	peers []int
	errs  []error
	// sent — сигнал тесту о каждой передаче.
	sent chan struct{}
}

func (p *seqpacketTunFD) SocketName() string { return datapath.TunSocketName() }

func (p *seqpacketTunFD) SendFD(ctx context.Context, socket string, pid int) error {
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return err
	}
	err = datapath.SendFD(ctx, socket, pid, pair[0])
	unix.Close(pair[0])
	p.mu.Lock()
	p.peers = append(p.peers, pair[1])
	p.errs = append(p.errs, err)
	p.mu.Unlock()
	p.sent <- struct{}{}
	return err
}

func (p *seqpacketTunFD) CheckAdapter() error { return nil }
func (p *seqpacketTunFD) Lost(error)          {}

func (p *seqpacketTunFD) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, fd := range p.peers {
		unix.Close(fd)
	}
}

// TestRealCoreTunLost — настоящее ядро потеряло дескриптор (другой конец
// закрыт — EOF) и шлёт TUN_LOST; цикл отдаёт ему новый через тот же сокет, и
// ядро продолжает без перезапуска: «TUN FD заменён без перезапуска потоков».
// Сокеты ядра привязаны к loopback (-bind-if 1): VK недоступен, и ядро не
// кончает работу отказом VK раньше, чем дойдёт до замены.
func TestRealCoreTunLost(t *testing.T) {
	path := os.Getenv("CSQTT_TEST_CORE")
	if path == "" {
		t.Skip("CSQTT_TEST_CORE не задан")
	}
	fd := &seqpacketTunFD{sent: make(chan struct{}, 4)}
	defer fd.close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var mu sync.Mutex
	var lost, replaced, resent bool
	var lines []string
	// Остановка — когда и ядро сказало о замене, и цикл — о передаче.
	done := func() {
		if replaced && resent {
			cancel()
		}
	}
	tun, err := NewTunnel(Options{
		CorePath: path,
		Config: coreproc.Config{
			Peer:     "192.0.2.1:46000",
			Password: "real-core-password",
			Hashes:   []string{"real-core-hash"},
			BindIf:   1,
		},
		TunFD:    fd,
		Datapath: &fakeDatapath{},
		OnMessage: func(m coreproc.Message) {
			mu.Lock()
			defer mu.Unlock()
			switch e := m.(type) {
			case coreproc.TunLostEvent:
				lost = true
			case coreproc.Log:
				if strings.Contains(e.Text, "TUN FD заменён без перезапуска потоков") {
					replaced = true
					done()
				}
			}
		},
		Logf: func(format string, args ...any) {
			mu.Lock()
			defer mu.Unlock()
			line := fmt.Sprintf(format, args...)
			lines = append(lines, line)
			if strings.Contains(line, "передан заново") {
				resent = true
				done()
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		// Первый дескриптор принят — его «интерфейс» закрывается.
		select {
		case <-fd.sent:
		case <-ctx.Done():
			return
		}
		fd.mu.Lock()
		unix.Close(fd.peers[0])
		fd.peers[0] = -1
		fd.mu.Unlock()
	}()
	if err := tun.Run(ctx); err != nil {
		t.Fatalf("Run() = %v, ждали остановку тестом после замены дескриптора", err)
	}

	mu.Lock()
	defer mu.Unlock()
	fd.mu.Lock()
	defer fd.mu.Unlock()
	if !lost || !replaced || !resent {
		t.Fatalf("TUN_LOST: %v, ядро заменило дескриптор: %v, цикл передал заново: %v; строки цикла %q",
			lost, replaced, resent, lines)
	}
	if len(fd.errs) != 2 || fd.errs[0] != nil || fd.errs[1] != nil {
		t.Errorf("передачи дескриптора: %v", fd.errs)
	}
}
