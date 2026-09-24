package localname

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Name states reported by the daemon.
const (
	StateLive          = "live"
	StateProbing       = "probing"
	StateConflict      = "conflict"
	StateNoCertificate = "no-certificate"
	StateNoMDNS        = "mdns-unavailable"
)

// staleAfter is how old a heartbeat may be before the daemon counts as down.
const staleAfter = 5 * time.Second

// NameStatus is one served name as the daemon last saw it.
type NameStatus struct {
	Name    string `json:"name"`
	Service string `json:"service"`
	Project string `json:"project"`
	Target  string `json:"target"`
	State   string `json:"state"`
	Detail  string `json:"detail,omitempty"`
}

// Heartbeat is the daemon's view of itself, rewritten every second.
type Heartbeat struct {
	PID       int       `json:"pid"`
	Build     string    `json:"build"`
	StartedAt time.Time `json:"startedAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	HTTPSPort int       `json:"httpsPort"`
	HTTPPort  int       `json:"httpPort,omitempty"`
	// APIPort is the loopback port of the dashboard API.
	APIPort   int          `json:"apiPort,omitempty"`
	MDNS      string       `json:"mdns,omitempty"` // who publishes names: pier, mDNSResponder, or the Windows DNS client
	MDNSError string       `json:"mdnsError,omitempty"`
	Names     []NameStatus `json:"names"`
	Warnings  []string     `json:"warnings,omitempty"`
	Error     string       `json:"error,omitempty"`
}

// Fresh reports whether the daemon wrote this heartbeat recently.
func (h Heartbeat) Fresh(now time.Time) bool {
	return h.PID != 0 && now.Sub(h.UpdatedAt) < staleAfter
}

// URL is the address a browser uses for name while this daemon serves it.
func (h Heartbeat) URL(name string) string {
	if h.HTTPSPort == 0 || h.HTTPSPort == 443 {
		return "https://" + name + "/"
	}
	return "https://" + name + ":" + strconv.Itoa(h.HTTPSPort) + "/"
}

func readHeartbeat() (Heartbeat, error) {
	path, err := heartbeatPath()
	if err != nil {
		return Heartbeat{}, err
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return Heartbeat{}, err
	}
	var h Heartbeat
	if err := json.Unmarshal(contents, &h); err != nil {
		return Heartbeat{}, fmt.Errorf("decode heartbeat: %w", err)
	}
	return h, nil
}

func writeHeartbeat(h Heartbeat) error {
	path, err := heartbeatPath()
	if err != nil {
		return err
	}
	return writeAtomic(path, h)
}

func removeHeartbeat(pid int) {
	path, err := heartbeatPath()
	if err != nil {
		return
	}
	if h, err := readHeartbeat(); err == nil && h.PID != pid {
		return
	}
	_ = os.Remove(path)
}

func writeAtomic(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if _, err := temporary.Write(append(payload, '\n')); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), path)
}

// requestStop asks the daemon with pid to exit cleanly (sending mDNS goodbyes).
// A file works the same on every OS, unlike signals.
func requestStop(pid int) error {
	path, err := stopPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strconv.Itoa(pid)), 0o600)
}

// stopRequested reports and clears a stop request addressed to pid.
func stopRequested(pid int) bool {
	path, err := stopPath()
	if err != nil {
		return false
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	if strings.TrimSpace(string(contents)) != strconv.Itoa(pid) {
		return false
	}
	_ = os.Remove(path)
	return true
}
