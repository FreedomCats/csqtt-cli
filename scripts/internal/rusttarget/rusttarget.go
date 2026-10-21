// Package rusttarget — цель Rust, под которую собирается ядро csqtt-core для
// платформы Go. Общий для buildcore, release и notices: ядро, архив и список
// его crates должны быть об одной и той же цели.
//
// Для Linux — цель gnu: musl ядро не собирает (udp_batch.rs передаёт флаги
// recvmmsg как c_int, а у musl они c_uint).
package rusttarget

import "fmt"

var targets = map[string]string{
	"windows/amd64": "x86_64-pc-windows-msvc",
	"linux/amd64":   "x86_64-unknown-linux-gnu",
	"linux/arm64":   "aarch64-unknown-linux-gnu",
}

// For — цель Rust для платформы goos/goarch.
func For(goos, goarch string) (string, error) {
	if t, ok := targets[goos+"/"+goarch]; ok {
		return t, nil
	}
	return "", fmt.Errorf("для %s/%s цели Rust нет: ядро собирается под windows/amd64, linux/amd64 и linux/arm64", goos, goarch)
}
