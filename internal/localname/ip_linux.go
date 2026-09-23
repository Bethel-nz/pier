//go:build linux

package localname

import (
	"net"
	"os"
)

// CurrentIPv4 returns the IPv4 address on the interface that owns the default route.
func CurrentIPv4() (net.IP, error) {
	table, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return nil, err
	}
	name := ParseLinuxDefaultInterface(string(table))
	if name == "" {
		return nil, nil
	}
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return nil, err
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, addr := range addrs {
		ipnet, ok := addr.(*net.IPNet)
		if ok {
			ips = append(ips, ipnet.IP)
		}
	}
	return SelectIPv4(ips), nil
}
