// netguard.go — физический интерфейс для сокетов мимо туннеля (tun).
//
// Когда datapath забирает весь трафик системы (Requirements.Netguard), ядро
// привязывает свои сокеты к физическому интерфейсу само (правка П3): индекс
// оно получает в -bind-if, метку для таблицы защиты — в -mark. Выбирает
// интерфейс netguard, как в qwdtt-cli; его же Control привязывает сокеты
// csqtt-cli — прокси окна капчи (architecture.md §06).
//
// Индекс ядро получает на каждую попытку (coreBinding — Prepare супервизора),
// а за сменой сети следит netguard.Guard.Watch. Сменился интерфейс — по
// умолчанию попытка перезапускается, и новое ядро получает новый индекс: на
// прежнем индексе ядро в новую сеть не пойдёт, сокеты на пропавшем
// интерфейсе получают ENODEV (Linux) или WSAENETUNREACH (Windows), мимо
// привязки они не уходят. Со скрытым флагом -net-switch bindif работающее
// ядро вместо этого получает BIND_IF (supervisor.Tunnel.Rebind) — для
// замера W6.
package cli

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"syscall"

	"github.com/FreedomCats/csqtt-cli/internal/captcha"
	"github.com/FreedomCats/csqtt-cli/internal/coreproc"
	"github.com/FreedomCats/csqtt-cli/internal/netguard"
)

// selectInterfaces выбирает интерфейс и ставит его в guard. Переменная ради
// тестов: настоящая читает таблицу маршрутов.
var selectInterfaces = func(guard *netguard.Guard) (netguard.Choice, error) {
	if _, err := guard.SelectInterfaces(0); err != nil {
		return netguard.Choice{}, err
	}
	return guard.Choice(), nil
}

// watchNetworkChanges — guard.Watch; переменная ради тестов.
var watchNetworkChanges = func(guard *netguard.Guard, opts netguard.WatchOptions) (stop func(), err error) {
	return guard.Watch(opts)
}

// reversePathCheck — переменная ради тестов: настоящая читает /proc.
var reversePathCheck = reversePathProblem

// errNetworkChanged — причина перезапуска при смене сети; её печатает
// строка перехода «переподключение: сеть сменилась».
var errNetworkChanged = errors.New("сеть сменилась")

// Как туннель переходит в новую сеть (-net-switch).
const (
	// netSwitchRestart — перезапуск ядра (умолчание).
	netSwitchRestart = "restart"
	// netSwitchBindIf — BIND_IF работающему ядру, перезапуск — только если
	// перевести нельзя (supervisor.Tunnel.Rebind).
	netSwitchBindIf = "bindif"
)

// netSwitcher — чем слежение за сетью переводит туннель:
// *supervisor.Tunnel.
type netSwitcher interface {
	Restart(reason error)
	Rebind(index uint32, reason error)
}

// errNoNetwork — попытка без интерфейса IPv4: ядру не к чему привязаться.
// Ядро ходит к DNS Яндекса и TURN только по IPv4, интерфейс IPv6 ему не
// заменит.
var errNoNetwork = errors.New("сети нет: у системы нет маршрута по умолчанию IPv4 — подключусь, когда он появится")

// setupNetguard выбирает физический интерфейс и проверяет обратный фильтр:
// строгий rp_filter на нём — отказ до подключения. sysctl csqtt-cli не меняет:
// изменение пережило бы его падение. Сети нет вовсе — не ошибка: попытки
// подождут её (errNoNetwork).
//
// Фильтр обратного пути IPv6 у firewalld (qwdtt-cli проверял его) здесь не
// мешает: маршрутов IPv6 в туннель нет, и обратный путь ответов IPv6 ведёт
// через тот же физический интерфейс. Поэтому NoIPv6 не ставится, а ядро IPv6
// почти не видит: имена VK оно разрешает в A-записи.
func setupNetguard(out *printer, verbose bool) (*netguard.Guard, netguard.Choice, error) {
	guard := &netguard.Guard{}
	if verbose {
		guard.Logf = out.logf
	}
	choice, err := selectInterfaces(guard)
	if err != nil {
		return nil, choice, err
	}
	// Об окне капчи — отдельной строкой в openTun: через этот интерфейс оно
	// ходит, только если мосту дан прокси (Dial), а это решает kill-switch.
	out.line("сокеты ядра идут мимо туннеля через физический интерфейс: %s", choice)
	if choice.Index4 == 0 {
		out.line("Внимание: маршрута по умолчанию IPv4 нет — подключусь, когда сеть появится")
	}
	problem, err := reversePathCheck(choice)
	if err != nil {
		out.line("Внимание: rp_filter не проверен: %v", err)
	}
	if problem != "" {
		return nil, choice, errors.New(problem + "; или подключитесь с -datapath socks")
	}
	return guard, choice, nil
}

// coreBinding даёт ядру каждой попытки физический интерфейс (Prepare
// супервизора).
type coreBinding struct {
	out   *printer
	guard *netguard.Guard
	// watching — за сетью следит Watch и ставит выбор в guard сам. Без него
	// выбор освежается здесь, перед каждой попыткой: тогда новая сеть
	// подхватывается, когда попытка оборвётся сама (ядро вышло, долгая
	// деградация). Пишется до Run, читается в его цикле.
	watching bool
	// last — последний выбор, сделанный здесь, для строки о смене сети.
	last netguard.Choice
}

// prepare ставит ядру -bind-if (индекс IPv4) и -mark (метку guard, если
// она есть: только вместе с -bind-if и только на Linux — так её и ставит
// openTun).
func (b *coreBinding) prepare(cfg *coreproc.Config) error {
	if !b.watching {
		c, err := selectInterfaces(b.guard)
		if err != nil {
			return err
		}
		if !c.Same(b.last) {
			b.out.line("сеть сменилась: %s", c)
			b.warnReversePath(c)
		}
		b.last = c
	}
	index4, _ := b.guard.Interfaces()
	if index4 == 0 {
		return errNoNetwork
	}
	cfg.BindIf, cfg.Mark = index4, b.guard.Mark
	return nil
}

// warnReversePath — строгий фильтр у нового интерфейса не останавливает
// переподключения (сеть может смениться ещё раз), но объясняет, почему они
// не удаются.
func (b *coreBinding) warnReversePath(c netguard.Choice) {
	problem, err := reversePathCheck(c)
	switch {
	case err != nil:
		b.out.line("Внимание: rp_filter не проверен: %v", err)
	case problem != "":
		b.out.line("Внимание: %s", problem)
	}
}

// watchNetwork следит за сменой физического интерфейса: новые индексы
// netguard ставит сам, выбор печатается строкой, туннель переходит в новую
// сеть — перезапуском или, если rebind, командой BIND_IF работающему ядру.
// Интерфейс, который пропадал и вернулся тем же, — всегда перезапуск: BIND_IF
// на тот же индекс ничего не меняет, а сокеты через него мертвы. ok = false —
// подписаться не вышло: подключению это не мешает, но новая сеть подхватится
// только с концом попытки — об этом строка.
func watchNetwork(out *printer, guard *netguard.Guard, sw netSwitcher, rebind bool) (stop func(), ok bool) {
	b := &coreBinding{out: out}
	stop, err := watchNetworkChanges(guard, netguard.WatchOptions{
		Changed: func(now, before netguard.Choice) {
			switch {
			case now.Empty():
				// Перезапускать незачем: новой попытке не на чем
				// подключиться, а возврат сети — ещё одна смена, и
				// перезапуск — один на выдернутый и вставленный кабель.
				out.line("сеть пропала — туннель переподключится, когда она вернётся")
				return
			case now.Same(before):
				// Интерфейс пропадал и вернулся: сокеты через него мертвы.
				out.line("сеть переподключилась: %s", now)
			default:
				out.line("сеть сменилась: %s", now)
				if rebind && now.Index4 != 0 {
					b.warnReversePath(now)
					sw.Rebind(now.Index4, errNetworkChanged)
					return
				}
			}
			b.warnReversePath(now)
			sw.Restart(errNetworkChanged)
		},
	})
	if err != nil {
		out.line("Внимание: за сменой сети не слежу (%v) — интерфейс выбирается заново перед каждой попыткой, "+
			"и новая сеть подхватится, когда попытка оборвётся сама", err)
		return func() {}, false
	}
	return stop, true
}

// captchaDNS — DNS, которым прокси окна капчи разрешает имена VK: DNS
// Яндекса, как у самого ядра.
var captchaDNS = []string{"77.88.8.8:53", "77.88.8.1:53"}

// captchaDial — чем прокси окна капчи соединяется с VK при kill-switch:
// сокетами csqtt-cli на физическом интерфейсе с меткой (guard.Control). Имена
// он разрешает так же: DNS системы при «закрыто» закрыт, а её резолвер
// (systemd-resolved) ходит своими сокетами, без метки. Поэтому резолвер свой,
// к DNS Яндекса, теми же сокетами.
func captchaDial(guard *netguard.Guard) captcha.DialFunc {
	return guardedDialer(guard.Control, captchaDNS)
}

// guardedDialer — captchaDial с заданными хуком сокетов и DNS (тесты).
func guardedDialer(control func(network, address string, c syscall.RawConn) error, dns []string) captcha.DialFunc {
	var next atomic.Uint32
	dnsDialer := &net.Dialer{Control: control}
	resolver := &net.Resolver{
		PreferGo: true,
		// Адрес из resolv.conf не годится: это DNS системы.
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			server := dns[int(next.Add(1)-1)%len(dns)]
			return dnsDialer.DialContext(ctx, network, server)
		},
	}
	return (&net.Dialer{Control: control, Resolver: resolver}).DialContext
}
