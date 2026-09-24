package localname

import (
	"net"
	"testing"

	"github.com/Bethel-nz/pier/internal/mdns"
)

type fakeBackend struct{ live map[string]*fakeHandle }

type fakeHandle struct {
	address string
	dead    bool
	stopped bool
}

func (b *fakeBackend) kind() string { return "fake" }

func (b *fakeBackend) register(name, address string, _ int) (handle, error) {
	h := &fakeHandle{address: address}
	b.live[name] = h
	return h, nil
}

func (h *fakeHandle) live() bool   { return true }
func (h *fakeHandle) exited() bool { return h.dead }
func (h *fakeHandle) stop()        { h.stopped = true }

func withAddress(t *testing.T, address *string) {
	t.Helper()
	previous := lanAddress
	lanAddress = func() net.IP { return net.ParseIP(*address) }
	t.Cleanup(func() { lanAddress = previous })
}

func TestSystemNamesFollowsAddressAndNames(t *testing.T) {
	address := "192.168.1.10"
	withAddress(t, &address)
	backend := &fakeBackend{live: map[string]*fakeHandle{}}
	p := &systemNames{backend: backend, port: 443, names: map[string]*entry{}}

	p.SetNames([]string{"myapp.local", "api.local"})
	first := backend.live["myapp.local"]
	if first == nil || first.address != address {
		t.Fatalf("myapp.local registered at %+v, want %s", first, address)
	}
	for _, status := range p.Statuses() {
		if status.State != mdns.Live {
			t.Fatalf("%s is %v, want live", status.Name, status.State)
		}
	}

	address = "10.0.0.5" // Wi-Fi changed
	p.reconcile(nil)
	if !first.stopped || backend.live["myapp.local"].address != address {
		t.Fatal("address change did not replace the old record")
	}

	backend.live["api.local"].dead = true // the system dropped it
	p.reconcile(nil)
	if backend.live["api.local"].dead {
		t.Fatal("a dead registration was not redone")
	}

	api := backend.live["api.local"]
	p.SetNames([]string{"myapp.local"})
	if !api.stopped || len(p.Statuses()) != 1 {
		t.Fatal("a removed name was not withdrawn")
	}

	address = "" // offline
	myapp := backend.live["myapp.local"]
	p.reconcile(nil)
	if !myapp.stopped || p.Statuses()[0].State == mdns.Live {
		t.Fatal("offline should withdraw instead of publishing a dead address")
	}

	p.SetNames(nil)
	if len(p.Statuses()) != 0 {
		t.Fatal("SetNames(nil) should withdraw everything")
	}
}
