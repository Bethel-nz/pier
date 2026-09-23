//go:build !darwin

package localname

import "net"

// CurrentIPv4 returns the first private IPv4 address on a non-virtual interface.
func CurrentIPv4() (net.IP, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var ips []net.IP
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if ok {
				ips = append(ips, ipnet.IP)
			}
		}
	}
	return SelectIPv4(ips), nil
}
