package datapath

import "net/netip"

// TunnelMTU — MTU туннеля, как у адаптера Android-клиента CSQTT.
const TunnelMTU = 1300

// TunConf — что сервер выдал строкой TUNCONF: адрес туннеля и DNS.
type TunConf struct {
	Address netip.Addr
	DNS     []netip.Addr
}
