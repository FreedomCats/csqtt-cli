//go:build !linux

package cli

import "errors"

// tunGuardMissing — почему режим tun здесь только с -allow-leak: на Windows
// защита DNS и kill-switch придут позже (W5), на прочих системах tun нет
// вовсе. Переменная ради тестов.
var tunGuardMissing = errors.New("на Windows он пока работает без защиты от утечки DNS и kill-switch")

// openSystemDNSGuard — защиты DNS и kill-switch здесь пока нет.
func openSystemDNSGuard() (dnsGuardSession, error) {
	return nil, errors.New("на этой системе их пока нет, есть только на Linux")
}
