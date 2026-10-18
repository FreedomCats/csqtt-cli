//go:build !linux && !windows

package captcha

import "errors"

// platformWindow — окна капчи здесь нет, только ручной режим.
func platformWindow(Options) (Window, error) { return nil, errors.ErrUnsupported }
