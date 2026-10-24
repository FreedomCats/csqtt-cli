package cli

import (
	"github.com/FreedomCats/csqtt-cli/internal/linuxsys"
	"github.com/FreedomCats/csqtt-cli/internal/netguard"
)

// tunGuardMissing — защита DNS и kill-switch на Linux есть.
var tunGuardMissing error

// openSystemDNSGuard — таблица nftables (linuxsys.DNSGuard); сокеты ядра
// csqtt-cli она узнаёт по метке netguard.SocketMark, которую ядро ставит им
// по -mark.
func openSystemDNSGuard() (dnsGuardSession, error) {
	g, err := linuxsys.OpenDNSGuard(netguard.SocketMark)
	if err != nil {
		return nil, err
	}
	return g, nil
}
