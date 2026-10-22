package captcha

// platformWindow — окно капчи на Windows: WebView2.
func platformWindow(opts Options) (Window, error) {
	w, err := NewWebView2(WebView2Options{Out: opts.Out, Dial: opts.Dial, Logf: opts.Logf})
	if err != nil {
		return nil, err
	}
	return w, nil
}

// platformLoginWindow — окно входа VK на Windows: WebView2.
func platformLoginWindow(opts LoginOptions) (LoginWindow, error) {
	w, err := NewWebView2(WebView2Options{Out: opts.Out, Logf: opts.Logf, Net: opts.Net})
	if err != nil {
		return nil, err
	}
	return w, nil
}
