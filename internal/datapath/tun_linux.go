package datapath

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/FreedomCats/csqtt-cli/internal/app"
	"github.com/FreedomCats/csqtt-cli/internal/linuxsys"
)

// tunDevice — устройство, через которое ядро Linux создаёт адаптеры TUN.
const tunDevice = "/dev/net/tun"

// tunFlags — флаги адаптера: TUN (IP-пакеты, без Ethernet) и без заголовка
// PI. IFF_VNET_HDR нет намеренно: с ним перед каждым пакетом шёл бы
// заголовок разгрузки virtio_net, а ядро CSQTT ждёт голый IP-пакет. Поэтому
// и не tun.CreateTUN из wireguard-go: он разгрузку включает.
const tunFlags = unix.IFF_TUN | unix.IFF_NO_PI

// tunLockName — абстрактный unix-сокет одного адаптера на машину: файла не
// оставляет и умирает вместе с процессом, так что упавший запуск следующему
// не мешает. Абстрактные имена у каждого сетевого пространства свои, как и
// имена интерфейсов: csqtt-cli в контейнере с хостовым не конфликтует.
// Переменная — ради тестов: им нельзя занимать блокировку работающего
// csqtt-cli.
var tunLockName = "@" + app.Name + "-tun"

// tunUnsupported — на Linux tun работает, если ядро Linux умеет TUN.
func tunUnsupported() error {
	if _, err := os.Stat(tunDevice); err != nil {
		return fmt.Errorf("нет %s: ядро Linux без поддержки TUN (модуль tun: sudo modprobe tun)", tunDevice)
	}
	return nil
}

// SocketName — новое случайное имя абстрактного сокета, на котором ядро
// попытки ждёт дескриптор адаптера: значение -tun-uds.
func (t *Tun) SocketName() string { return TunSocketName() }

// SendFD отдаёт ядру с pid новый дубликат дескриптора адаптера через
// абстрактный сокет socket (см. пакетную SendFD) и закрывает свой дубликат.
// Адаптера ещё нет — создаёт его. Зовётся на каждую попытку: дескриптор
// прежнего ядра закрылся вместе с ним.
func (t *Tun) SendFD(ctx context.Context, socket string, pid int) error {
	fd, err := t.dup()
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	if err := SendFD(ctx, socket, pid, fd); err != nil {
		return fmt.Errorf("tun: %w", err)
	}
	t.logf("[TUN] дескриптор адаптера передан ядру (pid %d)", pid)
	return nil
}

// openTunAdapter создаёт адаптер name: без адреса и опущенным. Адрес, MTU и
// маршруты задают configure и addRoutes, DNS системы — setDNS. На отказе
// за собой убирает всё.
//
// DNS на Linux — не свойство адаптера, а настройка системы: способ
// (systemd-resolved, resolvconf, перезапись /etc/resolv.conf) выбирает
// linuxsys.DetectSystemDNS по тому, кто ведёт resolv.conf, — до создания
// адаптера, так что незнакомый генератор файла — отказ открытия, а не
// попытки.
//
// Адаптер не постоянный: интерфейс живёт, пока открыт его дескриптор, и
// после падения процесса исчезает. Поэтому чужой интерфейс с тем же
// именем — не остаток прошлого csqtt-cli, а чей-то ещё, и он не удаляется, а
// называется в ошибке.
func openTunAdapter(name string, logf func(string, ...any)) (*tunAdapter, error) {
	unlock, err := acquireTunLock()
	if err != nil {
		return nil, err
	}
	kind, exists, err := linuxsys.LinkKind(name)
	if err != nil {
		unlock()
		return nil, fmt.Errorf("адаптер %q: %w", name, err)
	}
	if exists {
		unlock()
		return nil, foreignLinkError(name, kind)
	}
	dns, err := linuxsys.DetectSystemDNS(name)
	if err != nil {
		unlock()
		return nil, fmt.Errorf("DNS системы: %w", err)
	}
	fd, err := createTUN(name)
	if errors.Is(err, unix.EBUSY) {
		// Чужой интерфейс появился между LinkKind и TUNSETIFF.
		unlock()
		return nil, foreignLinkError(name, "tun")
	}
	if err != nil {
		unlock()
		return nil, fmt.Errorf("адаптер %q: %w", name, err)
	}
	index, err := linuxsys.LinkIndex(name)
	if err != nil {
		unix.Close(fd)
		unlock()
		return nil, fmt.Errorf("адаптер %q: %w", name, err)
	}
	logf("[TUN] DNS системы задаётся так: %s", dns)
	if w := dns.Warning(); w != "" {
		logf("[TUN] Внимание: %s", w)
	}

	return &tunAdapter{
		configure: func(addr netip.Addr) error {
			return linuxsys.ConfigureAdapter(index, []netip.Addr{addr}, TunnelMTU, logf)
		},
		// Маршрутов IPv6 нет: туннель IPv6 не везёт, а отбросить такие
		// пакеты, как мост на Windows, некому — они шли бы мимо оболочки
		// прямо в ядро. IPv6 системы наружу закрывает таблица защиты
		// (blockIPv6).
		addRoutes:    func() error { return linuxsys.SetTunnelRoutes(index, tunRoutes4) },
		removeRoutes: func() error { return linuxsys.ClearTunnelRoutes(index, tunRoutes4) },
		setDNS: func(servers []netip.Addr) error {
			if err := dns.Set(index, servers); err != nil {
				return err
			}
			if len(servers) == 0 {
				logf("[TUN] у туннеля нет резолверов IPv4 — DNS системы %s не трогает", app.Name)
			} else {
				logf("[TUN] DNS системы (%s): %v", dns.Method(), servers)
			}
			return nil
		},
		revertDNS: func() error {
			if !dns.Applied() {
				return nil
			}
			if err := dns.Revert(index); err != nil {
				return err
			}
			logf("[TUN] DNS системы возвращён (%s)", dns.Method())
			return nil
		},
		dup: func() (int, error) {
			return unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 0)
		},
		// Адрес и маршруты уходят вместе с интерфейсом, когда закрывается
		// последний его дескриптор.
		close: func() error {
			err := unix.Close(fd)
			unlock()
			return err
		},
	}, nil
}

// foreignLinkError — отказ, когда интерфейс name уже есть и создал его не
// csqtt-cli.
func foreignLinkError(name, kind string) error {
	return fmt.Errorf("адаптер %q: в системе уже есть интерфейс с этим именем (тип %s), и создал его не %s; "+
		"если он не нужен, удалите его: sudo ip link del %s", name, kind, app.Name, name)
}

// createTUN открывает /dev/net/tun и заводит на нём адаптер name с
// tunFlags. IFF_TUN_EXCL — только новый: существующий постоянный TUN с тем
// же именем без него молча подхватился бы, а с ним — EBUSY.
func createTUN(name string) (int, error) {
	fd, err := unix.Open(tunDevice, unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("%s: %w", tunDevice, err)
	}
	ifr, err := unix.NewIfreq(name)
	if err != nil {
		unix.Close(fd)
		return -1, err
	}
	ifr.SetUint16(tunFlags | unix.IFF_TUN_EXCL)
	if err := unix.IoctlIfreq(fd, unix.TUNSETIFF, ifr); err != nil {
		unix.Close(fd)
		return -1, fmt.Errorf("TUNSETIFF: %w", err)
	}
	return fd, nil
}

// acquireTunLock занимает абстрактный сокет одного адаптера на машину и
// возвращает функцию, которая его отпустит. Занят другим процессом —
// значит, на машине уже работает csqtt-cli с datapath tun.
//
// Внутри процесса захваты считаются: вызывающий держит сокет весь
// csqtt-cli connect (Tun.Reserve), а открытие адаптера берёт его ещё раз.
func acquireTunLock() (release func(), err error) {
	tunLock.mu.Lock()
	defer tunLock.mu.Unlock()

	if tunLock.refs == 0 {
		l, err := net.Listen("unix", tunLockName)
		if errors.Is(err, syscall.EADDRINUSE) {
			return nil, errTunBusy
		}
		if err != nil {
			return nil, fmt.Errorf("блокировка адаптера: %w", err)
		}
		tunLock.listener = l
	}
	tunLock.refs++

	var once sync.Once
	return func() {
		once.Do(func() {
			tunLock.mu.Lock()
			defer tunLock.mu.Unlock()
			tunLock.refs--
			if tunLock.refs == 0 {
				tunLock.listener.Close()
				tunLock.listener = nil
			}
		})
	}, nil
}

// tunLock — сокет блокировки и число захватов в этом процессе.
var tunLock struct {
	mu       sync.Mutex
	listener net.Listener
	refs     int
}
