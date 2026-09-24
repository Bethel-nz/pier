package localname

import (
	"bytes"
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"

	"github.com/Bethel-nz/pier/internal/state"
)

// wantsAutostart reports whether any saved project asks to be served after login.
func wantsAutostart(projects []state.ProjectState) bool {
	for _, project := range projects {
		if project.Local.Autostart && len(project.Domains) > 0 {
			return true
		}
	}
	return false
}

// setAutostart installs or removes the login item that runs `pier locald`.
func (d *Directory) setAutostart(on bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if d.loginItem == nil {
		return nil
	}
	return d.loginItem(exe, on)
}

const launchdLabel = "dev.pier.locald"

// launchdPlist is the macOS LaunchAgent. It starts the daemon at login and
// restarts it only after a crash; a clean idle exit stays exited.
func launchdPlist(exe, logFile string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>locald</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, launchdLabel, html.EscapeString(exe), html.EscapeString(logFile), html.EscapeString(logFile))
}

const systemdUnitName = "pier-locald.service"

// systemdUnit is the Linux user service. Restart=on-failure mirrors launchd:
// a crash restarts it, an idle exit does not.
func systemdUnit(exe string) string {
	return fmt.Sprintf(`[Unit]
Description=Pier local names (.local over HTTPS)
After=network-online.target

[Service]
Type=simple
ExecStart="%s" locald
Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target
`, exe)
}

// writeIfChanged writes contents to path unless it already holds them.
func writeIfChanged(path string, contents []byte) error {
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, contents) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, contents, 0o644)
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
