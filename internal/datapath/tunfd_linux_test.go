package datapath

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// fdReceiver — поддельный получатель дескриптора: слушает абстрактный
// сокет, как ядро с -tun-uds, принимает дескриптор сообщением с SCM_RIGHTS
// и отвечает байтом 1 (tun.rs). Принятые дескрипторы приходят в fds; -1 —
// соединение без дескриптора.
type fdReceiver struct {
	fds   chan int
	reply reply
}

// reply — что получатель делает с принятым дескриптором.
type reply int

const (
	replyAck   reply = iota // подтверждает, как ядро
	replyClose              // закрывает соединение молча
	replyHang               // молчит и не закрывает
	replyWrong              // отвечает не тем байтом
)

func listenFD(t *testing.T, name string, mode reply) *fdReceiver {
	t.Helper()
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: "@" + name, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	return serveFD(t, ln, mode)
}

// serveFD принимает соединения ln до его закрытия.
func serveFD(t *testing.T, ln *net.UnixListener, mode reply) *fdReceiver {
	t.Helper()
	r := &fdReceiver{fds: make(chan int, 8), reply: mode}
	t.Cleanup(func() {
		ln.Close()
		for {
			select {
			case fd := <-r.fds:
				if fd >= 0 {
					unix.Close(fd)
				}
			default:
				return
			}
		}
	})
	go func() {
		for {
			c, err := ln.AcceptUnix()
			if err != nil {
				return
			}
			go r.serve(c)
		}
	}()
	return r
}

func (r *fdReceiver) serve(c *net.UnixConn) {
	defer c.Close()
	buf := make([]byte, 1)
	oob := make([]byte, unix.CmsgSpace(4))
	_, oobn, _, _, err := c.ReadMsgUnix(buf, oob)
	fd := -1
	if err == nil && oobn > 0 {
		if msgs, err := unix.ParseSocketControlMessage(oob[:oobn]); err == nil && len(msgs) == 1 {
			if fds, err := unix.ParseUnixRights(&msgs[0]); err == nil && len(fds) == 1 {
				fd = fds[0]
			}
		}
	}
	r.fds <- fd
	switch {
	case fd < 0:
	case r.reply == replyHang:
		_, _ = c.Read(buf) // до закрытия той стороной
	case r.reply == replyAck:
		_, _ = c.Write([]byte{1})
	case r.reply == replyWrong:
		_, _ = c.Write([]byte{0})
	}
}

// received ждёт дескриптор, принятый получателем.
func (r *fdReceiver) received(t *testing.T) int {
	t.Helper()
	select {
	case fd := <-r.fds:
		if fd < 0 {
			t.Fatal("получатель не нашёл дескриптора в сообщении")
		}
		return fd
	case <-time.After(5 * time.Second):
		t.Fatal("получатель ничего не принял")
		return -1
	}
}

// pipeFDs — канал ОС: пишут в w, читают из r.
func pipeFDs(t *testing.T) (r, w int) {
	t.Helper()
	var p [2]int
	if err := unix.Pipe2(p[:], unix.O_CLOEXEC); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unix.Close(p[0]); unix.Close(p[1]) })
	return p[0], p[1]
}

// sameFile — запись в got видна в read: got — копия того же дескриптора.
func sameFile(t *testing.T, got, read int) {
	t.Helper()
	if _, err := unix.Write(got, []byte("ping")); err != nil {
		t.Fatalf("запись в принятый дескриптор: %v", err)
	}
	buf := make([]byte, 8)
	n, err := unix.Read(read, buf)
	if err != nil || string(buf[:n]) != "ping" {
		t.Fatalf("из канала прочитано %q, %v", buf[:n], err)
	}
}

func timeout(t *testing.T, d time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

// Дескриптор доходит до получателя и подтверждается; получатель держит
// копию того же канала ОС.
func TestSendFD(t *testing.T) {
	name := TunSocketName()
	r := listenFD(t, name, replyAck)
	rd, wr := pipeFDs(t)
	if err := SendFD(timeout(t, 5*time.Second), name, os.Getpid(), wr); err != nil {
		t.Fatalf("SendFD: %v", err)
	}
	got := r.received(t)
	defer unix.Close(got)
	if got == wr {
		t.Fatal("получатель видит тот же номер — это не копия")
	}
	sameFile(t, got, rd)
	// Свой дескриптор SendFD не закрывает.
	if _, err := unix.FcntlInt(uintptr(wr), unix.F_GETFD, 0); err != nil {
		t.Errorf("дескриптор отправителя закрыт: %v", err)
	}
}

// Имена сокетов на каждый вызов новые и годятся ядру: без «@», короче
// sun_path.
func TestTunSocketName(t *testing.T) {
	a, b := TunSocketName(), TunSocketName()
	if a == b || strings.HasPrefix(a, "@") || len(a) >= 107 || !strings.HasPrefix(a, "csqtt-cli-tun-") {
		t.Errorf("имена %q и %q", a, b)
	}
}

// Ядро открывает сокет не сразу после запуска: SendFD повторяет
// подключение, пока сокета нет.
func TestSendFDWaitsForListener(t *testing.T) {
	name := TunSocketName()
	rd, wr := pipeFDs(t)
	done := make(chan error, 1)
	go func() { done <- SendFD(timeout(t, 5*time.Second), name, os.Getpid(), wr) }()
	time.Sleep(150 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("SendFD вернулся без получателя: %v", err)
	default:
	}
	r := listenFD(t, name, replyAck)
	if err := <-done; err != nil {
		t.Fatalf("SendFD: %v", err)
	}
	got := r.received(t)
	defer unix.Close(got)
	sameFile(t, got, rd)
}

// Очередь слушателя полна (у ядра — 4 соединения): connect получает EAGAIN,
// и SendFD повторяет, пока место не освободится.
func TestSendFDFullBacklog(t *testing.T) {
	name := TunSocketName()
	s, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Bind(s, &unix.SockaddrUnix{Name: "@" + name}); err != nil {
		unix.Close(s)
		t.Fatal(err)
	}
	// Очередь 0: одно соединение, не принятое слушателем, её заполняет.
	if err := unix.Listen(s, 0); err != nil {
		unix.Close(s)
		t.Fatal(err)
	}
	f := os.NewFile(uintptr(s), "listener")
	fl, err := net.FileListener(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	ln := fl.(*net.UnixListener)
	defer ln.Close()

	filler, err := net.Dial("unix", "@"+name)
	if err != nil {
		t.Fatal(err)
	}
	defer filler.Close()
	// Убедиться, что очередь действительно полна.
	probe, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	err = unix.Connect(probe, &unix.SockaddrUnix{Name: "@" + name})
	unix.Close(probe)
	if !errors.Is(err, unix.EAGAIN) {
		t.Fatalf("очередь 0 не заполнилась одним соединением (connect: %v)", err)
	}

	rd, wr := pipeFDs(t)
	done := make(chan error, 1)
	go func() { done <- SendFD(timeout(t, 5*time.Second), name, os.Getpid(), wr) }()
	time.Sleep(100 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("SendFD вернулся при полной очереди: %v", err)
	default:
	}
	// Слушатель разбирает очередь: первое соединение — заполнитель.
	c, err := ln.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	r := serveFD(t, ln, replyAck)
	if err := <-done; err != nil {
		t.Fatalf("SendFD: %v", err)
	}
	got := r.received(t)
	defer unix.Close(got)
	sameFile(t, got, rd)
}

// Сокета нет до конца срока — ошибка с причиной, без зависания.
func TestSendFDNoListener(t *testing.T) {
	_, wr := pipeFDs(t)
	start := time.Now()
	err := SendFD(timeout(t, 100*time.Millisecond), TunSocketName(), os.Getpid(), wr)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "не открыло сокет") {
		t.Errorf("SendFD: %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("SendFD ждал %v", d)
	}
}

// Сокет слушает не ядро (другой pid) — дескриптор не отправляется.
func TestSendFDWrongOwner(t *testing.T) {
	name := TunSocketName()
	r := listenFD(t, name, replyAck)
	_, wr := pipeFDs(t)
	err := SendFD(timeout(t, 5*time.Second), name, os.Getpid()+1, wr)
	if err == nil || !strings.Contains(err.Error(), "слушает не ядро") {
		t.Fatalf("SendFD: %v", err)
	}
	select {
	case fd := <-r.fds:
		if fd >= 0 {
			unix.Close(fd)
			t.Fatal("дескриптор ушёл чужому слушателю")
		}
	case <-time.After(time.Second):
		t.Fatal("соединение к чужому слушателю не закрыто")
	}
}

func TestSendFDBadPID(t *testing.T) {
	_, wr := pipeFDs(t)
	if err := SendFD(context.Background(), TunSocketName(), 0, wr); err == nil {
		t.Error("pid 0 принят")
	}
}

// Получатель закрыл соединение, не подтвердив (так ядро с П4 закрывает
// чужое), — ошибка.
func TestSendFDNoAck(t *testing.T) {
	name := TunSocketName()
	listenFD(t, name, replyClose)
	_, wr := pipeFDs(t)
	err := SendFD(timeout(t, 5*time.Second), name, os.Getpid(), wr)
	if err == nil || !strings.Contains(err.Error(), "подтверждения нет") {
		t.Errorf("SendFD: %v", err)
	}
}

// Получатель принял и молчит — SendFD возвращается по ctx.
func TestSendFDSilentReceiver(t *testing.T) {
	name := TunSocketName()
	listenFD(t, name, replyHang)
	_, wr := pipeFDs(t)
	start := time.Now()
	err := SendFD(timeout(t, 200*time.Millisecond), name, os.Getpid(), wr)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("SendFD: %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("SendFD ждал %v", d)
	}
}

// Ответ не тем байтом — не подтверждение.
func TestSendFDWrongAck(t *testing.T) {
	name := TunSocketName()
	listenFD(t, name, replyWrong)
	_, wr := pipeFDs(t)
	err := SendFD(timeout(t, 5*time.Second), name, os.Getpid(), wr)
	if err == nil || !strings.Contains(err.Error(), "вместо подтверждения") {
		t.Errorf("SendFD: %v", err)
	}
}
