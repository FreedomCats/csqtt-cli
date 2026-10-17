// Package winsys — системная часть Windows: выбор физического
// интерфейса, маршруты, DNS и подписка на смену сети.
//
// Код пакета живёт в файлах *_windows.go, и только они импортируют winipcfg
// из golang.zx2c4.com/wireguard/windows. Под другие системы пакет пустой.
package winsys
