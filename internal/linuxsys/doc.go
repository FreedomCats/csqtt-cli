// Package linuxsys — системная часть Linux: выбор физического интерфейса,
// подписка на смену сети, адаптер, маршруты и DNS. Аналог winsys.
//
// Код пакета живёт в файлах *_linux.go, и только они импортируют netlink
// из github.com/vishvananda/netlink. Под другие системы пакет пустой.
package linuxsys
