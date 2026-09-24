package app

import (
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/Bethel-nz/pier/internal/config"
	"github.com/Bethel-nz/pier/internal/state"
)

// assignTaps gives each throttled or captured service a tap port, reusing the
// saved one so Tailscale routes stay put across pier up runs.
func assignTaps(cfg config.Project, saved []state.Tap, paused map[string]bool) ([]state.Tap, error) {
	ports := map[string]int{}
	for _, tap := range saved {
		ports[tap.Service] = tap.Port
	}
	var taps []state.Tap
	for _, service := range cfg.Services {
		if !service.Tapped() || paused[service.Name] {
			continue
		}
		port := ports[service.Name]
		if port == 0 {
			free, err := freePort()
			if err != nil {
				return nil, fmt.Errorf("Pier could not find a free port to throttle or capture %s: %w", service.Name, err)
			}
			port = free
		}
		tap := state.Tap{Service: service.Name, Target: service.Target, Port: port, CaptureSeconds: int64(service.Capture / time.Second)}
		if shaping := service.Throttle; !shaping.IsZero() {
			tap.Throttle = &state.Throttle{LatencyMS: shaping.Latency.Milliseconds(), Down: shaping.Down, Up: shaping.Up}
		}
		taps = append(taps, tap)
	}
	return taps, nil
}

// freePort asks the system for an unused loopback port.
func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

// firstLANPort is where LAN ports start: easy to type on a phone, and clear
// of the usual dev-server ports.
const firstLANPort = 4100

// lanPortFree reports whether port can be bound on every address; tests replace it.
var lanPortFree = func(port int) bool {
	listener, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

// nextLANPort is the first port from firstLANPort that is neither taken nor busy.
func nextLANPort(taken map[int]bool) int {
	for port := firstLANPort; port < firstLANPort+900; port++ {
		if !taken[port] && lanPortFree(port) {
			return port
		}
	}
	return 0 // none free: the name still works, without a LAN port
}

// throughTaps points each tapped service's Tailscale route at its tap.
func throughTaps(routes []config.ResolvedService, taps []state.Tap) []config.ResolvedService {
	byService := map[string]state.Tap{}
	for _, tap := range taps {
		byService[tap.Service] = tap
	}
	out := append([]config.ResolvedService(nil), routes...)
	for i, service := range out {
		if tap, ok := byService[service.Name]; ok {
			out[i].Target = tap.URL()
		}
	}
	return out
}

func sameTaps(left, right []state.Tap) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		a, b := left[i], right[i]
		if a.Service != b.Service || a.Target != b.Target || a.Port != b.Port || a.CaptureSeconds != b.CaptureSeconds {
			return false
		}
		if (a.Throttle == nil) != (b.Throttle == nil) || (a.Throttle != nil && *a.Throttle != *b.Throttle) {
			return false
		}
	}
	return true
}
