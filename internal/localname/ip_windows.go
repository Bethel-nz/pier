//go:build windows

package localname

import (
	"net"
	"os/exec"
)

// CurrentIPv4 returns the IPv4 address on the Windows default route.
func CurrentIPv4() (net.IP, error) {
	output, err := exec.Command("route", "print", "-4").Output()
	if err != nil {
		return nil, err
	}
	return ParseWindowsDefaultIPv4(string(output)), nil
}
