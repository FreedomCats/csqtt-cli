package datapath

import (
	"errors"
	"fmt"
	"log"
	"net/netip"
	"strings"
	"sync"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/tun"

	"github.com/FreedomCats/csqtt-cli/internal/app"
	"github.com/FreedomCats/csqtt-cli/internal/winsys"
)

// WintunDLL — библиотека драйвера Wintun. golang.zx2c4.com/wintun ищет её
// только рядом с исполняемым файлом и в System32 (LoadLibraryEx), поэтому
// она лежит рядом с csqtt-cli.exe (go run ./scripts/wintun).
const WintunDLL = "wintun.dll"

// tunRoutes — маршруты через адаптер: IPv4 половинами /1 и так же IPv6.
// Туннель IPv6 не везёт, но без маршрутов IPv6 машины шёл бы мимо него с
// настоящим адресом; в адаптере мост такие пакеты отбрасывает (Link), и
// программы откатываются на IPv4 (architecture.md §05). Переменная — ради
// теста с настоящим адаптером: ему нельзя забирать трафик машины.
var tunRoutes = append(append([]netip.Prefix{}, tunRoutes4...),
	netip.MustParsePrefix("::/1"),
	netip.MustParsePrefix("8000::/1"),
)

// tunGUID — постоянный GUID адаптера (winsys.AdapterGUID). Переменная —
// ради теста: тестовый адаптер не должен совпасть с адаптером работающего
// csqtt-cli.
var tunGUID = winsys.AdapterGUID

// tunLockName — именованный мьютекс одного адаптера на машину: два
// csqtt-cli с datapath tun делили бы один GUID, а что драйвер делает со
// вторым адаптером с тем же GUID, не документировано. Мьютекс умирает
// вместе с процессом, так что упавший запуск следующему не мешает.
// Переменная — ради тестов, как на Linux.
var tunLockName = `Global\` + app.Name + "-wintun"

// tunUnsupported — на Windows tun работает.
func tunUnsupported() error { return nil }

// Link — канал с ядром, по которому мост возит пакеты адаптера: Addr —
// значение -listen-peer, порт ядра из события LISTEN — в SetCorePort.
// Адаптера ещё нет — создаёт его: Link живёт вместе с ним весь сеанс.
func (t *Tun) Link() (*Link, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	adapter, err := t.openLocked()
	if err != nil {
		return nil, err
	}
	return adapter.link, nil
}

// openTunAdapter создаёт адаптер Wintun name с постоянным GUID, Link и мост
// между ними. Адреса у адаптера ещё нет, маршрутов тоже: их задают
// configure и addRoutes, DNS адаптера — setDNS. На отказе за собой убирает
// всё.
//
// Адаптер живёт, пока открыт его дескриптор: после падения процесса
// система закрывает дескриптор, и адаптер исчезает вместе с адресом и
// маршрутами, а его остатки драйвер вычищает сам при следующем создании.
//
// DNS на Windows — свойство адаптера: setDNS задаёт его, revertDNS снимает.
// Между попытками он снимается, как и маршруты: клиент DNS Windows шлёт
// запросы к резолверам адаптера через сам адаптер, то есть в туннель без
// ядра, и страница капчи не открылась бы (так же на Linux, §05).
func openTunAdapter(name string, logf func(string, ...any)) (*tunAdapter, error) {
	unlock, err := acquireTunLock()
	if err != nil {
		return nil, err
	}
	restoreLog := captureStdLog(logf)
	release := func() {
		restoreLog()
		unlock()
	}

	tun.WintunTunnelType = app.Name
	guid := tunGUID
	dev, err := tun.CreateTUNWithRequestedGUID(name, &guid, TunnelMTU)
	if err != nil {
		release()
		return nil, fmt.Errorf("адаптер %q: %w", name, explainWintun(err))
	}
	native, ok := dev.(*tun.NativeTun)
	if !ok {
		dev.Close()
		release()
		return nil, fmt.Errorf("адаптер %q: TUN неожиданного типа %T", name, dev)
	}
	luid := native.LUID()
	link, err := ListenLink()
	if err != nil {
		dev.Close()
		release()
		return nil, err
	}
	bridge := startBridge(dev, link, logf)
	routes := tunRoutes
	dnsSet := false

	return &tunAdapter{
		configure: func(addr netip.Addr) error {
			return winsys.ConfigureAdapter(luid, []netip.Addr{addr}, TunnelMTU, logf)
		},
		addRoutes:    func() error { return winsys.SetTunnelRoutes(luid, routes, logf) },
		removeRoutes: func() error { return winsys.ClearTunnelRoutes(luid, routes) },
		routes:       routes,
		dropsIPv6:    true,
		setDNS: func(servers []netip.Addr) error {
			if err := winsys.SetAdapterDNS(luid, servers); err != nil {
				return err
			}
			dnsSet = len(servers) > 0
			if dnsSet {
				logf("[TUN] DNS адаптера: %v", servers)
			} else {
				logf("[TUN] у туннеля нет резолверов IPv4 — DNS адаптера пуст")
			}
			return nil
		},
		revertDNS: func() error {
			if !dnsSet {
				return nil
			}
			if err := winsys.SetAdapterDNS(luid, nil); err != nil {
				return err
			}
			dnsSet = false
			logf("[TUN] DNS адаптера снят")
			return nil
		},
		link: link,
		// Маршруты, адреса и DNS снимаются до закрытия адаптера: стек
		// Windows плохо переносит уничтожение интерфейса с активными
		// маршрутами (winsys.FlushAdapter). Затем мост: закрытые адаптер и
		// Link будят его горутины.
		close: func() error {
			err := winsys.FlushAdapter(luid)
			bridge.stop()
			if cerr := dev.Close(); cerr != nil {
				err = errors.Join(err, cerr)
			}
			_ = link.Close()
			bridge.wait()
			release()
			return err
		},
	}, nil
}

// explainWintun дописывает к отказу создания адаптера, где должна лежать
// wintun.dll, если её не нашли.
func explainWintun(err error) error {
	var dll *windows.DLLError
	if errors.As(err, &dll) || strings.Contains(err.Error(), WintunDLL) {
		return fmt.Errorf("%w; %s должна лежать рядом с %s.exe (go run ./scripts/wintun)", err, WintunDLL, app.Name)
	}
	return err
}

// captureStdLog направляет стандартный log в logf и возвращает функцию,
// которая вернёт прежний вывод. golang.zx2c4.com/wintun передаёт сообщения
// драйвера в log.Default(), а тот по умолчанию пишет прямо в stderr —
// поверх строки состояния. Перехват глобальный и живёт, пока жив адаптер.
func captureStdLog(logf func(string, ...any)) (restore func()) {
	out, flags, prefix := log.Writer(), log.Flags(), log.Prefix()
	log.SetOutput(stdLogWriter{logf: logf})
	log.SetFlags(0)
	log.SetPrefix("")
	return func() {
		log.SetOutput(out)
		log.SetFlags(flags)
		log.SetPrefix(prefix)
	}
}

type stdLogWriter struct {
	logf func(string, ...any)
}

func (w stdLogWriter) Write(p []byte) (int, error) {
	w.logf("[LOG] %s", strings.TrimRight(string(p), "\r\n"))
	return len(p), nil
}

// acquireTunLock занимает мьютекс одного адаптера на машину и возвращает
// функцию, которая его отпустит. Занят другим процессом — значит, на машине
// уже работает csqtt-cli с datapath tun.
//
// Внутри процесса захваты считаются: вызывающий держит мьютекс весь
// csqtt-cli connect (Tun.Reserve), а открытие адаптера берёт его ещё раз.
// Повторный CreateMutex с тем же именем вернул бы ERROR_ALREADY_EXISTS и
// своему же процессу, поэтому дескриптор один и закрывается с последним
// захватом.
func acquireTunLock() (release func(), err error) {
	tunLock.mu.Lock()
	defer tunLock.mu.Unlock()

	if tunLock.refs == 0 {
		name, err := windows.UTF16PtrFromString(tunLockName)
		if err != nil {
			return nil, err
		}
		h, err := windows.CreateMutex(nil, false, name)
		if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			windows.CloseHandle(h)
			return nil, errTunBusy
		}
		if err != nil {
			return nil, fmt.Errorf("мьютекс адаптера: %w", err)
		}
		tunLock.handle = h
	}
	tunLock.refs++

	var once sync.Once
	return func() {
		once.Do(func() {
			tunLock.mu.Lock()
			defer tunLock.mu.Unlock()
			tunLock.refs--
			if tunLock.refs == 0 {
				windows.CloseHandle(tunLock.handle)
				tunLock.handle = 0
			}
		})
	}, nil
}

// tunLock — дескриптор мьютекса и число захватов в этом процессе.
var tunLock struct {
	mu     sync.Mutex
	handle windows.Handle
	refs   int
}
