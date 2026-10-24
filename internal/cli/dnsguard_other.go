//go:build !linux && !windows

package cli

import "errors"

// tunGuardMissing — защиты DNS и kill-switch здесь нет. Режима tun здесь нет
// тоже, и отказ по нему (Requirements.Unsupported) приходит раньше.
// Переменная ради тестов.
var tunGuardMissing = errors.New("здесь он работает без защиты от утечки DNS и kill-switch")

// openSystemDNSGuard — защиты DNS и kill-switch здесь нет.
func openSystemDNSGuard() (dnsGuardSession, error) {
	return nil, errors.New("на этой системе их нет, есть только на Linux и Windows")
}
