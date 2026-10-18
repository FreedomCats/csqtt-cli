//go:build !windows

// webview2_other.go — заглушка окна капчи там, где WebView2 нет.
//
// На Linux окно капчи даёт браузер под управлением по CDP (browser_linux.go),
// на прочих системах — только ручной режим.
package captcha

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// WebView2Options — настройки решателя с окном. См. webview2_windows.go.
type WebView2Options struct {
	Out  io.Writer
	Dial DialFunc
	Logf func(format string, args ...any)
}

// WebView2 — решатель с окном; вне Windows его не бывает.
type WebView2 struct{}

// NewWebView2 вне Windows всегда отказывает. Отказ обёрнут в
// errors.ErrUnsupported: Bridge по нему молча берёт другое окно или ручной
// режим, не предупреждая о том, чего на этой системе и не ждут.
func NewWebView2(WebView2Options) (*WebView2, error) {
	return nil, fmt.Errorf("окно капчи есть только на Windows: %w", errors.ErrUnsupported)
}

// Solve недостижим: NewWebView2 экземпляра не возвращает.
func (*WebView2) Solve(context.Context, Request) (string, error) {
	return "", errors.ErrUnsupported
}
