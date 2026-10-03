//go:build darwin

package localname

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/Bethel-nz/pier/internal/dnssd"
	"github.com/Bethel-nz/pier/internal/mdns"
)

// recordTTL matches what mDNSResponder uses for host address records.
const recordTTL = 120

// responder registers names with macOS's mDNSResponder over its client
// socket, one connection per name. The connection holds an A record on each
// LAN interface, so a Mac on Ethernet and Wi-Fi answers each network with
// the address that network can reach, and an NSEC that says the name has no
// AAAA, so an IPv6 lookup is answered at once instead of timing out. Closing
// the connection withdraws them all.
type responder struct{}

type registration struct{ conn *dnssd.Conn }

func systemBackend() backend {
	if _, err := os.Stat(dnssd.SocketPath); err != nil {
		return nil
	}
	return responder{}
}

func (responder) kind() string { return "mDNSResponder" }

func (responder) register(name string, addrs []mdns.Address, _ int) (handle, error) {
	conn, err := dnssd.Dial(dnssd.SocketPath)
	if err != nil {
		return nil, err
	}
	for _, rec := range hostRecords(name, addrs) {
		if err := conn.Register(rec); err != nil {
			_ = conn.Close()
			return nil, err
		}
	}
	return registration{conn: conn}, nil
}

// hostRecords are the A records for name, each scoped to its interface, and
// on each of those interfaces an NSEC stating A is the only type.
func hostRecords(name string, addrs []mdns.Address) []dnssd.Record {
	var records []dnssd.Record
	seen := map[int]bool{}
	for _, addr := range addrs {
		typ, data := dnssd.AddressData(addr.IP)
		if typ != dnssd.TypeA {
			continue
		}
		records = append(records, dnssd.Record{Name: name, Type: typ, Interface: uint32(addr.Interface), Data: data, TTL: recordTTL})
		if !seen[addr.Interface] {
			seen[addr.Interface] = true
			records = append(records, dnssd.Record{Name: name, Type: dnssd.TypeNSEC, Interface: uint32(addr.Interface), Data: dnssd.NSECData(name, dnssd.TypeA), TTL: recordTTL})
		}
	}
	return records
}

// live holds once mDNSResponder finished probing every record.
func (r registration) live() bool { return r.conn.Live() }

// exited holds when mDNSResponder dropped the registration, such as when
// another device claimed the name.
func (r registration) exited() bool {
	select {
	case <-r.conn.Done():
		return true
	default:
		return false
	}
}

func (r registration) stop() { _ = r.conn.Close() }

// instancePrefix marks the dns-sd registrations older Pier versions made.
const instancePrefix = "pier-"

// sweepPublishers stops dns-sd registrations an older Pier left behind,
// found by the instance-name marker; those records would otherwise answer
// alongside this Pier's. Pier's own registrations need no sweep: they end
// with the connection, which ends with the process.
func sweepPublishers() {
	out, err := exec.Command("ps", "-Ao", "pid=,command=").Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || !strings.HasSuffix(fields[1], "dns-sd") || fields[2] != "-P" || !strings.HasPrefix(fields[3], instancePrefix) {
			continue
		}
		if pid, err := strconv.Atoi(fields[0]); err == nil {
			_ = syscall.Kill(pid, syscall.SIGTERM)
		}
	}
}
