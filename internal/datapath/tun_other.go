//go:build !linux && !windows

package datapath

import "errors"

// errTunUnsupported — своего адаптера на прочих системах нет.
var errTunUnsupported = errors.New("datapath tun работает только на Linux и Windows")

// tunUnsupported — для Requires: вызывающий откажет до проверки прав.
func tunUnsupported() error { return errTunUnsupported }

// openTunAdapter не создаёт ничего.
func openTunAdapter(string, func(string, ...any), func(error)) (*tunAdapter, error) {
	return nil, errTunUnsupported
}

// acquireTunLock отказывает сразу: так «не поддерживается» выяснится до
// подключения, а не после него.
func acquireTunLock() (func(), error) { return nil, errTunUnsupported }
