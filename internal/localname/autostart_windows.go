//go:build windows

package localname

import (
	"os/exec"
	"strings"
)

const runKey = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`

// setLoginItem adds or removes a per-user Run entry; no administrator rights.
func setLoginItem(exe string, on bool) error {
	value := `"` + exe + `" locald`
	current, err := exec.Command("reg", "query", runKey, "/v", "PierLocal").Output()
	present := err == nil
	if !on {
		if !present {
			return nil
		}
		return exec.Command("reg", "delete", runKey, "/v", "PierLocal", "/f").Run()
	}
	if present && strings.Contains(string(current), value) {
		return nil
	}
	return exec.Command("reg", "add", runKey, "/v", "PierLocal", "/t", "REG_SZ", "/d", value, "/f").Run()
}
