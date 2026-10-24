package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/FreedomCats/csqtt-cli/internal/app"
	"github.com/FreedomCats/csqtt-cli/internal/winsys"
)

// tunGuardMissing — защита DNS и kill-switch на Windows есть.
var tunGuardMissing error

// openSystemDNSGuard — сессия WFP (winsys.DNSGuard). Всё разрешено
// csqtt-cli и ядру рядом с ним: WFP узнаёт программу по пути к exe, а ядро
// запускается только оттуда (findCore).
func openSystemDNSGuard() (dnsGuardSession, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("не найден каталог %s: %w", app.Name, err)
	}
	g, err := winsys.OpenDNSGuard(filepath.Join(filepath.Dir(exe), coreName))
	if err != nil {
		return nil, err
	}
	return g, nil
}
