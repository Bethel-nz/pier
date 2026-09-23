package localname

import (
	"bufio"
	"net"
	"strings"
)

// SelectIPv4 chooses the address a phone on the same network can reach.
// Loopback and link-local addresses are ignored. A private address wins over a public one.
func SelectIPv4(ips []net.IP) net.IP {
	var fallback net.IP
	for _, ip := range ips {
		ip = ip.To4()
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		if ip.IsPrivate() {
			return append(net.IP(nil), ip...)
		}
		if fallback == nil {
			fallback = append(net.IP(nil), ip...)
		}
	}
	return fallback
}

// ParseDefaultInterface reads the interface name from `route -n get default` output.
func ParseDefaultInterface(output string) string {
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if after, ok := strings.CutPrefix(line, "interface:"); ok {
			return strings.TrimSpace(after)
		}
	}
	return ""
}
