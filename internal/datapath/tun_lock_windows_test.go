package datapath

import (
	"errors"
	"strconv"

	"golang.org/x/sys/windows"

	"github.com/FreedomCats/csqtt-cli/internal/app"
)

func testTunLockName(pid int) string {
	return `Global\` + app.Name + "-test-wintun-" + strconv.Itoa(pid)
}

// lockNeedsPrivilege — мьютекс в пространстве Global без прав
// (SeCreateGlobalPrivilege) не создать.
func lockNeedsPrivilege(err error) bool { return errors.Is(err, windows.ERROR_ACCESS_DENIED) }
