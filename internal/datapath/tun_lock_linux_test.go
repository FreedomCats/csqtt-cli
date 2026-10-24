package datapath

import (
	"strconv"

	"github.com/FreedomCats/csqtt-cli/internal/app"
)

func testTunLockName(pid int) string { return "@" + app.Name + "-test-tun-" + strconv.Itoa(pid) }

// lockNeedsPrivilege — абстрактный сокет прав не требует.
func lockNeedsPrivilege(error) bool { return false }
