// fulltunnel_linux.go — проверки Linux для datapath tun: обратный фильтр и
// остатки DNS прошлого запуска.
package cli

import (
	"fmt"

	"github.com/FreedomCats/csqtt-cli/internal/app"
	"github.com/FreedomCats/csqtt-cli/internal/datapath"
	"github.com/FreedomCats/csqtt-cli/internal/linuxsys"
	"github.com/FreedomCats/csqtt-cli/internal/netguard"
)

// reversePathProblem — почему ответы на выбранный интерфейс IPv4 отбросит
// строгий rp_filter; "" — не отбросит. Ответы VK и TURN сокетам ядра
// приходят на физический интерфейс, а обратный маршрут к источнику ведёт в
// туннель (architecture.md §09б).
func reversePathProblem(c netguard.Choice) (string, error) {
	if c.Index4 == 0 {
		return "", nil
	}
	mode, err := linuxsys.ReversePathFilter(c.Alias4)
	if err != nil {
		return "", err
	}
	if mode != linuxsys.RPFilterStrict {
		return "", nil
	}
	// Ядро берёт большее из all и значения интерфейса, поэтому нестрогий
	// режим на самом интерфейсе перекрывает строгий all.
	return fmt.Sprintf("на %[1]s строгий обратный фильтр (rp_filter = 1): ответы VK и TURN сокетам ядра "+
		"будут отбрасываться, и туннель не поднимется; включите нестрогий режим: "+
		"sudo sysctl -w net.ipv4.conf.%[1]s.rp_filter=2 (насовсем — файлом в /etc/sysctl.d)", c.Alias4), nil
}

// prepareSystemDNS убирает DNS, оставленный упавшим прошлым запуском:
// запись resolvconf или перезаписанный resolv.conf. Звать после Reserve —
// второго csqtt-cli с адаптером на машине тогда быть не может. Незнакомый
// генератор resolv.conf — отказ сейчас, а не в каждой попытке после похода
// в VK. Способ и его предупреждение печатает адаптер при открытии.
// Переменная ради тестов.
var prepareSystemDNS = func(out *printer) error {
	dns, err := linuxsys.DetectSystemDNS(datapath.TunName)
	if err != nil {
		return fmt.Errorf("DNS туннеля в системе: %w; или подключитесь с -datapath socks", err)
	}
	done, err := dns.Recover()
	if err != nil {
		return fmt.Errorf("DNS системы после прошлого запуска %s: %w", app.Name, err)
	}
	if done != "" {
		out.line("DNS системы: %s", done)
	}
	return nil
}
