//go:build darwin || linux

package localname

import (
	"os/exec"
	"strconv"
	"strings"
)

func processCommand(pid int) string {
	output, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func isLocald(pid int) bool {
	command := processCommand(pid)
	return strings.Contains(command, "locald")
}

func isAdvertiser(pid int) bool {
	command := processCommand(pid)
	return strings.Contains(command, "dns-sd") || strings.Contains(command, "avahi-publish")
}
