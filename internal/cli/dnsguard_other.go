//go:build !linux

package cli

import "errors"

// openSystemDNSGuard — защиты DNS и kill-switch здесь пока нет: на Windows
// они придут с tun (задача W5), на прочих системах tun нет вовсе.
func openSystemDNSGuard() (dnsGuardSession, error) {
	return nil, errors.New("на этой системе их пока нет, есть только на Linux")
}
