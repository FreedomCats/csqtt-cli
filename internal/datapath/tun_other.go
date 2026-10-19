//go:build !linux

package datapath

import "errors"

// errTunUnsupported — свой адаптер на Windows появится с задачей W3, на
// прочих системах его нет.
var errTunUnsupported = errors.New("datapath tun пока работает только на Linux")

// tunUnsupported — для Requires: вызывающий откажет до проверки прав.
func tunUnsupported() error { return errTunUnsupported }

// openTunAdapter не создаёт ничего.
func openTunAdapter(string, func(string, ...any)) (*tunAdapter, error) {
	return nil, errTunUnsupported
}

// acquireTunLock отказывает сразу: так «не поддерживается» выяснится до
// подключения, а не после него.
func acquireTunLock() (func(), error) { return nil, errTunUnsupported }
