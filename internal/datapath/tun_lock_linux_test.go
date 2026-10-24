package datapath

import (
	"strconv"

	"github.com/FreedomCats/csqtt-cli/internal/app"
)

// realTunLockName — блокировка работающего csqtt-cli (tunLockName до
// TestMain).
const realTunLockName = "@" + app.Name + "-tun"

func testTunLockName(pid int) string { return "@" + app.Name + "-test-tun-" + strconv.Itoa(pid) }

// lockNeedsPrivilege — абстрактный сокет прав не требует.
func lockNeedsPrivilege(error) bool { return false }
