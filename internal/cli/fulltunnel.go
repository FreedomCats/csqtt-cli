// fulltunnel.go — datapath tun для connect: весь трафик системы в туннель
// и всё, без чего это небезопасно.
//
// До первой попытки, по порядку: адаптер занимается на машине (Reserve),
// выбирается физический интерфейс для сокетов ядра и проверяется обратный
// фильтр (netguard.go), убирается DNS, оставленный упавшим прошлым запуском,
// открывается защита DNS с kill-switch (dnsguard.go, killswitch.go) и
// создаётся адаптер. Всё это — до похода в VK: отказ здесь — понятная
// ошибка, а не цикл переподключений. Снимается в обратном порядке: слежение
// за сетью, адаптер (DNS системы и защита — его Close), сессия защиты,
// резерв.
package cli

import (
	"errors"
	"runtime"

	"github.com/FreedomCats/csqtt-cli/internal/app"
	"github.com/FreedomCats/csqtt-cli/internal/datapath"
	"github.com/FreedomCats/csqtt-cli/internal/netguard"
	"github.com/FreedomCats/csqtt-cli/internal/supervisor"
)

// tunDatapath — то, что connect нужно от datapath.Tun. Канал с ядром у
// платформ разный — дескриптор (supervisor.TunFD, Linux) или Link (tunLink,
// Windows), — поэтому он берётся проверкой типа.
type tunDatapath interface {
	supervisor.Datapath
	datapath.Persistent
	Requires() datapath.Requirements
	Reserve() (release func(), err error)
	Open() error
	Close() error
}

// tunLink — Link адаптера, по которому мост возит пакеты (Windows).
type tunLink interface {
	Link() (*datapath.Link, error)
}

// newTun — переменная ради тестов: настоящему адаптеру нужен root.
var newTun = func(opts datapath.TunOptions) tunDatapath { return datapath.NewTun(opts) }

// openTun готовит datapath tun по порядку из шапки файла. Права и система
// уже проверены в planConnect.
func (plan *connectPlan) openTun(e *env, out *printer, verbose bool) (_ *sessionDatapath, err error) {
	var undo []func()
	closeAll := func() {
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
	}
	defer func() {
		if err != nil {
			closeAll()
		}
	}()

	// Общее на машину (адаптер) — до сети: занятый — ошибка сейчас.
	release, err := newTun(datapath.TunOptions{}).Reserve()
	if err != nil {
		return nil, err
	}
	undo = append(undo, release)

	guard, choice, err := setupNetguard(out, verbose)
	if err != nil {
		return nil, err
	}

	// Второго csqtt-cli с адаптером на машине уже быть не может: остатки
	// упавшего прошлого запуска — его.
	if err := prepareSystemDNS(out); err != nil {
		return nil, err
	}

	setup, err := setupDNSGuard(out, e.stderr, plan.allowLeak, plan.killSwitchOff)
	if err != nil {
		return nil, err
	}
	undo = append(undo, setup.close)
	if setup.guard != nil && runtime.GOOS == "linux" {
		// По метке таблица пропускает сокеты ядра и окна капчи. Без
		// таблицы метка не нужна, а без CAP_NET_ADMIN ядро с ней не
		// стартовало бы вовсе. Меток нет на Windows: ядро с -mark там не
		// стартует.
		guard.Mark = netguard.SocketMark
	}
	if setup.killSwitch != nil {
		// Мост получит прокси (Dial ниже); без kill-switch окно ходит в сеть
		// само, и строки нет.
		out.line("окно капчи — через прокси %s тем же интерфейсом мимо туннеля: kill-switch на время капчи не снимается",
			app.Name)
	}

	tun := newTun(datapath.TunOptions{DNSGuard: setup.guard, Logf: out.logf})
	undo = append(undo, func() {
		if err := tun.Close(); err != nil {
			out.line("Внимание: адаптер снят не полностью: %v", err)
		}
	})
	// Чужой интерфейс с тем же именем, нет TUN, незнакомый resolv.conf —
	// до VK.
	if err := tun.Open(); err != nil {
		return nil, err
	}

	binding := &coreBinding{out: out, guard: guard, last: choice}
	dp := &sessionDatapath{
		datapath:   tun,
		describe:   "адаптер " + datapath.TunName + " — весь трафик системы",
		note:       "весь трафик системы — через адаптер " + datapath.TunName,
		prepare:    binding.prepare,
		killSwitch: setup.killSwitch.lockedFunc(),
		watch: func(sw netSwitcher) func() {
			stop, ok := watchNetwork(out, guard, sw, plan.netSwitch == netSwitchBindIf)
			binding.watching = ok
			return stop
		},
		close: closeAll,
	}
	// Канал пакетов с ядром: на Linux — дескриптор адаптера, на Windows —
	// Link, по которому мост возит пакеты Wintun.
	switch ch := tun.(type) {
	case supervisor.TunFD:
		dp.tunFD = ch
	case tunLink:
		if dp.link, err = ch.Link(); err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("tun: ядру нечем передать адаптер на этой системе")
	}
	if setup.killSwitch != nil {
		// Окно капчи — через прокси csqtt-cli сокетами мимо туннеля, с
		// меткой: kill-switch на время капчи не снимается. Мимо прокси —
		// под Direct, со снятием.
		dp.captchaDial = captchaDial(guard)
		dp.captchaDirect = setup.killSwitch.directFunc()
	}
	return dp, nil
}
