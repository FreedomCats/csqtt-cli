package captcha

// platformWindow — окно капчи на Linux: браузер на Chromium под управлением.
func platformWindow(opts Options) (Window, error) {
	b, err := NewBrowser(BrowserOptions{Out: opts.Out, Dial: opts.Dial, Logf: opts.Logf})
	if err != nil {
		return nil, err
	}
	return b, nil
}

// platformLoginWindow — окно входа VK на Linux: тот же браузер на Chromium.
func platformLoginWindow(opts LoginOptions) (LoginWindow, error) {
	b, err := NewBrowser(BrowserOptions{Out: opts.Out, Logf: opts.Logf, Net: opts.Net})
	if err != nil {
		return nil, err
	}
	return b, nil
}
