package datapath

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/FreedomCats/csqtt-cli/internal/app"
)

// Передача дескриптора адаптера ядру (Р4): ядро с -tun-uds <имя> слушает
// абстрактный unix-сокет (stream), принимает дескриптор сообщением с
// SCM_RIGHTS и отвечает одним байтом. Ядро принимает его только от процесса
// с тем же эффективным uid (П4), а оболочка отдаёт его только тому, кто
// слушает сокет от имени запущенного ею ядра: абстрактное имя открыто всем
// процессам машины, а командную строку ядра с этим именем видит любой из
// них — чужой мог бы занять имя раньше ядра.

// fdRetry — шаг повтора подключения, пока ядро не слушает сокет.
const fdRetry = 20 * time.Millisecond

// fdAck — байт, которым ядро подтверждает приём дескриптора (tun.rs).
const fdAck = 1

// TunSocketName — новое случайное имя абстрактного сокета для -tun-uds,
// без ведущего «@» (так его ждёт ядро). Имя на каждую попытку своё: сокет
// прежнего ядра мог ещё не закрыться.
func TunSocketName() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // crypto/rand не отказывает
	return app.Name + "-tun-" + hex.EncodeToString(b[:])
}

// SendFD отдаёт дескриптор fd процессу pid через абстрактный сокет socket
// и ждёт подтверждения. fd не закрывает: ядро получает свою копию.
//
// Когда ядро начнёт слушать, оболочка не знает: события об этом в режиме
// -tun-uds нет, а слушатель ядро открывает вскоре после PROCESS. Поэтому
// подключение повторяется с шагом fdRetry, пока сокета нет (ECONNREFUSED) или
// очередь слушателя полна (EAGAIN), — до отмены ctx. ctx ограничивает и
// ожидание подтверждения: ядро, которое приняло соединение и молчит, не
// вешает вызывающего.
//
// Слушает сокет не процесс pid или не тот же эффективный uid — дескриптор
// не отправляется.
func SendFD(ctx context.Context, socket string, pid int, fd int) error {
	if pid <= 0 {
		return fmt.Errorf("дескриптор ядру: неизвестен pid ядра (%d)", pid)
	}
	conn, err := dialTunSocket(ctx, socket)
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()

	if err := checkSocketOwner(conn, pid); err != nil {
		return err
	}
	// Байт данных обязателен: сообщение из одних SCM_RIGHTS stream-сокет не
	// передаёт, а ядро прочитанные 0 байт считает закрытием.
	if _, _, err := conn.WriteMsgUnix([]byte{1}, unix.UnixRights(fd), nil); err != nil {
		return fmt.Errorf("дескриптор ядру: %w", ctxErr(ctx, err))
	}
	var ack [1]byte
	n, err := conn.Read(ack[:])
	switch {
	case err != nil:
		return fmt.Errorf("дескриптор ядру: подтверждения нет: %w", ctxErr(ctx, err))
	case n != 1 || ack[0] != fdAck:
		return fmt.Errorf("дескриптор ядру: вместо подтверждения пришло %q", ack[:n])
	}
	return nil
}

// dialTunSocket подключается к сокету ядра, повторяя, пока его нет.
func dialTunSocket(ctx context.Context, socket string) (*net.UnixConn, error) {
	var d net.Dialer
	for {
		c, err := d.DialContext(ctx, "unix", "@"+socket)
		switch {
		case err == nil:
			return c.(*net.UnixConn), nil
		case ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded):
			// Срок ctx dial видит чуть раньше самого ctx — как «i/o timeout».
			cause := ctx.Err()
			if cause == nil {
				cause = context.DeadlineExceeded
			}
			return nil, fmt.Errorf("дескриптор ядру: ядро не открыло сокет -tun-uds: %w", cause)
		case !errors.Is(err, syscall.ECONNREFUSED) && !errors.Is(err, syscall.EAGAIN):
			return nil, fmt.Errorf("дескриптор ядру: %w", err)
		}
		t := time.NewTimer(fdRetry)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, fmt.Errorf("дескриптор ядру: ядро не открыло сокет -tun-uds: %w", ctx.Err())
		case <-t.C:
		}
	}
}

// checkSocketOwner — сокет слушает процесс pid от того же эффективного uid,
// что у оболочки. SO_PEERCRED у подключившегося — данные того, кто вызвал
// listen.
func checkSocketOwner(conn *net.UnixConn, pid int) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return fmt.Errorf("дескриптор ядру: %w", err)
	}
	var cred *unix.Ucred
	var cerr error
	if err := raw.Control(func(fd uintptr) {
		cred, cerr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return fmt.Errorf("дескриптор ядру: %w", err)
	}
	if cerr != nil {
		return fmt.Errorf("дескриптор ядру: SO_PEERCRED: %w", cerr)
	}
	if int(cred.Pid) != pid || int(cred.Uid) != os.Geteuid() {
		return fmt.Errorf("дескриптор ядру: сокет -tun-uds слушает не ядро (pid %d, uid %d; ядро — pid %d, uid %d) — "+
			"дескриптор не отправлен", cred.Pid, cred.Uid, pid, os.Geteuid())
	}
	return nil
}

// ctxErr — ошибка отмены ctx вместо «i/o timeout» от дедлайна, который
// ставит отмена.
func ctxErr(ctx context.Context, err error) error {
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	return err
}
