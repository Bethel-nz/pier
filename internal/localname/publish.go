package localname

import (
	"context"
	"net"
	"sort"
	"strconv"
	"strings"
)

// Record is one name Pier should publish on the local network.
type Record struct {
	Name string
	Port uint16
}

// Announcer starts and stops one advertisement.
// OSProcesses reports whether the ids from Start are operating system processes.
// Windows registrations are not processes, so they must not be killed by pid.
type Announcer interface {
	Start(ctx context.Context, record Record, ip net.IP) (int, error)
	Stop(pid int) error
	OSProcesses() bool
}

// PublishArgs is the dns-sd registration for one name at the machine's current address.
// The address is an argument, never saved inside the name.
func PublishArgs(name string, port uint16, ip net.IP) []string {
	label := strings.TrimSuffix(name, ".local")
	return []string{
		"-P", label, "_http._tcp", "local",
		strconv.Itoa(int(port)), name, ip.String(),
	}
}

type published struct {
	pid     int
	port    uint16
	ip      string
	process bool
}

// Publisher keeps advertisement processes matched to the current address.
// When the address changes, the old process is stopped before a new one starts.
type Publisher struct {
	announce Announcer
	live     map[string]published
}

// NewPublisher returns a publisher that uses announce to own its processes.
func NewPublisher(announce Announcer) *Publisher {
	return &Publisher{announce: announce, live: map[string]published{}}
}

// Reconcile stops names that should not exist and starts names at ip.
// A nil ip withdraws every advertisement so a dead address is not left published.
func (p *Publisher) Reconcile(ctx context.Context, records []Record, ip net.IP) error {
	want := map[string]Record{}
	if ip != nil {
		for _, record := range records {
			want[record.Name] = record
		}
	}
	names := make([]string, 0, len(p.live)+len(want))
	seen := map[string]bool{}
	for name := range p.live {
		seen[name] = true
		names = append(names, name)
	}
	for name := range want {
		if !seen[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	var first error
	for _, name := range names {
		current, exists := p.live[name]
		next, needed := want[name]
		if needed && exists && current.ip == ip.String() && current.port == next.Port {
			continue
		}
		if exists {
			if err := p.announce.Stop(current.pid); err != nil && first == nil {
				first = err
			}
			delete(p.live, name)
		}
		if !needed {
			continue
		}
		pid, err := p.announce.Start(ctx, next, ip)
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		p.live[name] = published{
			pid: pid, port: next.Port, ip: ip.String(), process: p.announce.OSProcesses(),
		}
	}
	return first
}

// StopAll withdraws every advertisement this publisher started.
func (p *Publisher) StopAll() error {
	return p.Reconcile(context.Background(), nil, nil)
}

// PIDs returns operating system processes this publisher started.
// Windows registrations are omitted because they are not processes.
func (p *Publisher) PIDs() []int {
	pids := make([]int, 0, len(p.live))
	for _, item := range p.live {
		if item.process {
			pids = append(pids, item.pid)
		}
	}
	sort.Ints(pids)
	return pids
}

// AvahiArgs is the avahi-publish command that publishes one address record.
func AvahiArgs(name string, ip net.IP) []string {
	return []string{"-a", "-R", name, ip.String()}
}

// InstanceName is the DNS-SD service instance for a .local host.
func InstanceName(host string) string {
	label := strings.TrimSuffix(host, ".local")
	return label + "._http._tcp.local"
}
