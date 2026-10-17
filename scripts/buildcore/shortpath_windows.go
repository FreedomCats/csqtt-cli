package main

import "golang.org/x/sys/windows"

// shortPath — короткая форма пути 8.3 (C:\Users\user\CARGO~1). aws-lc-sys
// переводит в неё пути исходников, чтобы не упереться в MAX_PATH, и в __FILE__
// попадает уже она. Нет короткой формы — пустая строка.
func shortPath(path string) string {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return ""
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetShortPathName(p, &buf[0], uint32(len(buf)))
	if err != nil || int(n) > len(buf) {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}
