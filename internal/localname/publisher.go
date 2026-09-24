package localname

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Bethel-nz/pier/internal/mdns"
)

// publisher makes .local names resolvable on the LAN.
//
// Where the operating system has its own responder (mDNSResponder on macOS,
// the DNS client on Windows), Pier registers names with it: it already speaks
// IPv4 and IPv6 mDNS, works on routers that drop one of them, and is not
// subject to per-app network permissions. Everywhere else, and when the
// system responder is missing, Pier's own Go responder answers.
type publisher interface {
	Serve(ctx context.Context) error
	SetNames(names []string)
	Statuses() []mdns.Status
	SendError() error
}

// backend registers one name at one address with the system responder.
type backend interface {
	kind() string
	register(name, address string, port int) (handle, error)
}

// handle is one live system registration.
type handle interface {
	live() bool   // the record is registered
	exited() bool // the registration died and must be redone
	stop()
}

// lanAddress is the address names point at; tests replace it.
var lanAddress = mdns.LANAddress

// newPublisher prefers the system responder and falls back to Pier's own.
func newPublisher(httpsPort int) (publisher, string, error) {
	if sys := systemBackend(); sys != nil {
		sweepPublishers()
		return &systemNames{backend: sys, port: httpsPort, names: map[string]*entry{}}, sys.kind(), nil
	}
	responder, err := mdns.Listen(mdns.Options{})
	if err != nil {
		return nil, "", err
	}
	return responder, "pier", nil
}

// systemNames keeps one system registration per name, pointed at this
// machine's current LAN address.
type systemNames struct {
	backend backend
	port    int

	mu      sync.Mutex
	names   map[string]*entry
	lastErr error
	errAt   time.Time
}

type entry struct {
	handle  handle
	address string
}

// Serve re-registers every name when the LAN address changes, and redoes any
// registration that died, until ctx ends. Then it withdraws them all.
func (p *systemNames) Serve(ctx context.Context) error {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			p.mu.Lock()
			for name, e := range p.names {
				e.release()
				delete(p.names, name)
			}
			p.mu.Unlock()
			return nil
		case <-tick.C:
			p.reconcile(nil)
		}
	}
}

// SetNames registers new names and withdraws removed ones.
func (p *systemNames) SetNames(names []string) {
	if names == nil {
		names = []string{}
	}
	p.reconcile(names)
}

func (p *systemNames) reconcile(names []string) {
	address := ""
	if ip := lanAddress(); ip != nil {
		address = ip.String()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if names != nil {
		want := map[string]bool{}
		for _, name := range names {
			want[name] = true
		}
		for name, e := range p.names {
			if !want[name] {
				e.release()
				delete(p.names, name)
			}
		}
		for name := range want {
			if _, ok := p.names[name]; !ok {
				p.names[name] = &entry{}
			}
		}
	}
	for name, e := range p.names {
		if e.handle != nil && e.address == address && !e.handle.exited() {
			continue
		}
		e.release() // the address changed or the registration died: never leave a stale record
		if address == "" {
			continue // offline: publish nothing rather than a dead address
		}
		h, err := p.backend.register(name, address, p.port)
		if err != nil {
			p.lastErr, p.errAt = err, time.Now()
			continue
		}
		e.handle, e.address = h, address
	}
}

func (e *entry) release() {
	if e.handle != nil {
		e.handle.stop()
	}
	e.handle, e.address = nil, ""
}

// Statuses reports each name as live once the system holds its record.
func (p *systemNames) Statuses() []mdns.Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]mdns.Status, 0, len(p.names))
	for name, e := range p.names {
		state := mdns.Probing
		if e.handle != nil && !e.handle.exited() && e.handle.live() {
			state = mdns.Live
		}
		out = append(out, mdns.Status{Name: name, State: state})
	}
	return out
}

// SendError reports a registration that failed within the last minute.
func (p *systemNames) SendError() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.lastErr == nil || time.Since(p.errAt) > time.Minute {
		return nil
	}
	return errors.New(p.backend.kind() + ": " + p.lastErr.Error())
}
