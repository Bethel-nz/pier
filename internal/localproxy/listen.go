package localproxy

import (
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"sync"
)

// Listeners serve one port for the local proxy.
//
// Pier first binds the port on every interface. When something else already
// holds it on one address (Tailscale Serve and Funnel bind 443 and 8443 on the
// tailnet address, or everywhere), Pier binds this machine's own addresses
// one by one instead: loopback plus each LAN address. Refresh keeps that set
// current when Wi-Fi changes.
type Listeners struct {
	Port int
	// PerAddress is true when the port is shared with another program.
	PerAddress bool
	// Skipped says why earlier ports in the list could not be used.
	Skipped []error

	serve func(net.Listener)
	mu    sync.Mutex
	bound map[string]net.Listener
}

// Bind takes the first port in ports that Pier can serve on, either on every
// interface or on each of this machine's addresses, and hands each listener to serve.
func Bind(ports []int, serve func(net.Listener)) (*Listeners, error) {
	var errs []error
	for _, port := range ports {
		listener, err := net.Listen("tcp", ":"+strconv.Itoa(port))
		if err == nil {
			l := &Listeners{Port: port, Skipped: errs, serve: serve, bound: map[string]net.Listener{"*": listener}}
			serve(listener)
			return l, nil
		}
		l := &Listeners{Port: port, PerAddress: true, Skipped: errs, serve: serve, bound: map[string]net.Listener{}}
		if l.bindAddresses(machineAddresses()) {
			return l, nil
		}
		l.Close()
		errs = append(errs, fmt.Errorf("port %d: %w", port, err))
	}
	return nil, errors.Join(errs...)
}

// Refresh binds addresses that appeared and releases ones that went away.
// It only matters when the port is shared.
func (l *Listeners) Refresh() {
	if l == nil || !l.PerAddress {
		return
	}
	l.bindAddresses(machineAddresses())
}

// Addresses lists what is bound, for status output and tests.
func (l *Listeners) Addresses() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, 0, len(l.bound))
	for addr := range l.bound {
		out = append(out, addr)
	}
	sort.Strings(out)
	return out
}

// Close releases every listener.
func (l *Listeners) Close() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for addr, listener := range l.bound {
		_ = listener.Close()
		delete(l.bound, addr)
	}
}

// bindAddresses makes the bound set match want. It reports whether loopback
// and at least one LAN address (when the machine has one) are served.
func (l *Listeners) bindAddresses(want []string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	wanted := map[string]bool{}
	for _, addr := range want {
		wanted[addr] = true
	}
	for addr, listener := range l.bound {
		if !wanted[addr] {
			_ = listener.Close()
			delete(l.bound, addr)
		}
	}
	for _, addr := range want {
		if _, ok := l.bound[addr]; ok {
			continue
		}
		listener, err := net.Listen("tcp", net.JoinHostPort(addr, strconv.Itoa(l.Port)))
		if err != nil {
			continue
		}
		l.bound[addr] = listener
		l.serve(listener)
	}
	_, loopback := l.bound["127.0.0.1"]
	lan := len(l.bound) - boolInt(loopback)
	return loopback && (lan > 0 || len(want) == 1)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// machineAddresses is loopback plus every IPv4 address a LAN device could
// reach. Link-local and Tailscale (100.64/10) addresses are left out, since
// those are the ones another program is likely to hold.
var machineAddresses = func() []string {
	addrs := []string{"127.0.0.1"}
	ifaceAddrs, err := net.InterfaceAddrs()
	if err != nil {
		return addrs
	}
	for _, addr := range ifaceAddrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipNet.IP.To4()
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || cgnat.Contains(ip) {
			continue
		}
		addrs = append(addrs, ip.String())
	}
	return addrs
}
