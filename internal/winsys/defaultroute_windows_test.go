package winsys

import (
	"net/netip"
	"testing"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

func TestDefaultInterfaceTunnelType(t *testing.T) {
	for _, tc := range []struct {
		name       string
		tunnelType winipcfg.TunnelType
		excluded   bool
	}{
		{"Teredo", winipcfg.TunnelTypeTeredo, true},
		{"ISATAP", winipcfg.TunnelTypeIsatap, true},
		{"physical", winipcfg.TunnelTypeNone, false},
		{"other", winipcfg.TunnelTypeOther, false},
		{"direct", winipcfg.TunnelTypeDirect, false},
		{"6to4", winipcfg.TunnelType6to4, false},
		{"IPHTTPS", winipcfg.TunnelTypeIPHTTPS, false},
		{"unknown", winipcfg.TunnelType(999), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := excludedTunnelType(tc.tunnelType); got != tc.excluded {
				t.Fatalf("excludedTunnelType(%d) = %v, want %v", tc.tunnelType, got, tc.excluded)
			}
		})
	}
}

// Выбор сверяется с GetBestInterfaceEx: без своего туннеля лучший маршрут
// до внешнего адреса и есть маршрут по умолчанию.
func TestDefaultInterfaceMatchesBestRoute(t *testing.T) {
	for _, tc := range []struct {
		name string
		ipv6 bool
		dst  netip.Addr
	}{
		{"IPv4", false, netip.MustParseAddr("1.1.1.1")},
		{"IPv6", true, netip.MustParseAddr("2606:4700:4700::1111")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			iface, ok, err := DefaultInterface(tc.ipv6, 0)
			if err != nil {
				t.Fatal(err)
			}
			best, bestErr := bestInterface(tc.dst)
			t.Logf("DefaultInterface: ok=%v %+v; GetBestInterfaceEx: %d, %v", ok, iface, best, bestErr)
			bestExcluded := false
			if bestErr == nil {
				luid, err := winipcfg.LUIDFromIndex(best)
				if err != nil {
					t.Fatal(err)
				}
				row, err := luid.Interface()
				if err != nil {
					t.Fatal(err)
				}
				bestExcluded = excludedTunnelType(row.TunnelType)
			}
			if !ok {
				if bestErr == nil && !bestExcluded {
					t.Fatalf("маршрута по умолчанию нет, а GetBestInterfaceEx нашёл интерфейс %d", best)
				}
				return
			}
			if bestErr != nil {
				t.Fatalf("выбран %+v, а GetBestInterfaceEx: %v", iface, bestErr)
			}
			if !bestExcluded && iface.Index != best {
				t.Fatalf("выбран интерфейс %d (%s), а GetBestInterfaceEx даёт %d", iface.Index, iface.Alias, best)
			}
		})
	}
}

func TestDefaultInterfaceExclude(t *testing.T) {
	iface, ok, err := DefaultInterface(false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Skip("нет маршрута по умолчанию IPv4")
	}
	again, ok, err := DefaultInterface(false, iface.LUID)
	if err != nil {
		t.Fatal(err)
	}
	if ok && again.LUID == iface.LUID {
		t.Fatalf("исключённый интерфейс %+v выбран снова", iface)
	}
}

func bestInterface(dst netip.Addr) (uint32, error) {
	var sa windows.Sockaddr
	if dst.Is4() {
		sa = &windows.SockaddrInet4{Addr: dst.As4()}
	} else {
		sa = &windows.SockaddrInet6{Addr: dst.As16()}
	}
	var index uint32
	err := windows.GetBestInterfaceEx(sa, &index)
	return index, err
}
