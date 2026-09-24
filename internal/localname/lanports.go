package localname

import (
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
}
