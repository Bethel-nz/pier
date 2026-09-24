package localname

import (
	"strconv"
	"strings"

	"github.com/Bethel-nz/pier/internal/localproxy"
)

// syncLANPorts serves each route with a LAN port over plain HTTP on every
// address, the fallback that works when .local names do not resolve. It
// returns the bind error for each name whose port could not open.
func (d *daemon) syncLANPorts(wanted map[int]localproxy.Route) map[string]string {
	for port, server := range d.lanPorts {
		if _, ok := wanted[port]; !ok {
			server.close()
			delete(d.lanPorts, port)
		}
	}
	failed := map[string]string{}
	for port, route := range wanted {
		server := d.lanPorts[port]
		if server == nil {
			server = openPort("", port, "LAN")
			d.lanPorts[port] = server
		}
		if server.err != nil {
			failed[route.Host] = server.err.Error()
			delete(d.lanPorts, port) // try again next time, in case the port came free
			continue
		}
		if server.route != route || server.handler.Load() == nil {
			handler, err := d.proxy.Direct(route)
			if err != nil {
				failed[route.Host] = err.Error()
				continue
			}
			server.handler.Store(&handler)
			server.route = route
		}
	}
	return failed
}

func (d *daemon) closeLANPorts() {
	for port, server := range d.lanPorts {
		server.close()
		delete(d.lanPorts, port)
	}
	for port, forward := range d.tcp {
		forward.Close()
		delete(d.tcp, port)
	}
}

// tcpRoute is one TCP service to relay on the LAN.
type tcpRoute struct {
	name   string
	target string // tcp://host:port
}

// syncTCP relays each TCP service's port from the LAN addresses to its
// target, following Wi-Fi changes. It returns why a name's port cannot be
// reached from the LAN, when it cannot.
func (d *daemon) syncTCP(wanted map[int]tcpRoute) map[string]string {
	for port, forward := range d.tcp {
		if route, ok := wanted[port]; !ok || d.tcpTargets[port] != route.target {
			forward.Close()
			delete(d.tcp, port)
			delete(d.tcpTargets, port)
		}
	}
	failed := map[string]string{}
	for port, route := range wanted {
		forward := d.tcp[port]
		if forward == nil {
			forward = localproxy.ForwardTCP(port, strings.TrimPrefix(route.target, "tcp://"))
			d.tcp[port], d.tcpTargets[port] = forward, route.target
		} else {
			forward.Refresh()
		}
		if !forward.Reachable() && lanAddress() != nil {
			failed[route.name] = "TCP port " + strconv.Itoa(port) + " could not be opened on this machine's LAN addresses"
		}
	}
	return failed
}
