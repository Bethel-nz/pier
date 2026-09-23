//go:build darwin

package localname

import (
	"net"
	"os/exec"
)

// CurrentIPv4 returns the IPv4 address of the default route interface.
func CurrentIPv4() (net.IP, error) {
	output, err := exec.Command("route", "-n", "get", "default").Output()
	if err != nil {
		return nil, err
	}
	name := ParseDefaultInterface(string(output))
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
		if !ok {
			continue
		}
		ips = append(ips, ipnet.IP)
	}
	return SelectIPv4(ips), nil
}
