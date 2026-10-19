package supervisor

import "github.com/FreedomCats/csqtt-cli/internal/datapath"

// Дескриптор адаптера ядру отдаёт tun на Linux.
var _ TunFD = (*datapath.Tun)(nil)
