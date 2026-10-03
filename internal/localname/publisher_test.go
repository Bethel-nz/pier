package localname

import (
	"net"
	"testing"

	"github.com/Bethel-nz/pier/internal/mdns"
)

type fakeBackend struct{ live map[string]*fakeHandle }

type fakeHandle struct {
	address string // the primary address
	addrs   []mdns.Address
	dead    bool
	stopped bool
}

func (b *fakeBackend) kind() string { return "fake" }

func (b *fakeBackend) register(name string, addrs []mdns.Address, _ int) (handle, error) {
	h := &fakeHandle{address: addrs[0].IP.String(), addrs: addrs}
	b.live[name] = h
	return h, nil
}

func (h *fakeHandle) live() bool   { return true }
func (h *fakeHandle) exited() bool { return h.dead }
func (h *fakeHandle) stop()        { h.stopped = true }

func withAddress(t *testing.T, address *string) {
	t.Helper()
	previous := lanAddresses
	lanAddresses = func() []mdns.Address {
		if *address == "" {
			return nil
		}
		return []mdns.Address{{IP: net.ParseIP(*address), Interface: 4}}
	}
	t.Cleanup(func() { lanAddresses = previous })
}

func TestSystemNamesPublishesEveryLAN(t *testing.T) {
	wifi := mdns.Address{IP: net.ParseIP("192.168.1.10"), Interface: 4}
	ethernet := mdns.Address{IP: net.ParseIP("10.0.0.5"), Interface: 7}
	addrs := []mdns.Address{wifi, ethernet}
	previous := lanAddresses
	lanAddresses = func() []mdns.Address { return addrs }
	t.Cleanup(func() { lanAddresses = previous })
	backend := &fakeBackend{live: map[string]*fakeHandle{}}
	p := &systemNames{backend: backend, port: 443, names: map[string]*entry{}}

	p.SetNames([]string{"myapp.local"})
	first := backend.live["myapp.local"]
	if first == nil || len(first.addrs) != 2 {
		t.Fatalf("registered %+v, want both networks", first)
	}

	p.reconcile(nil)
	if first.stopped {
		t.Fatal("re-registered with nothing changed")
	}

	addrs = []mdns.Address{wifi, {IP: net.ParseIP("10.0.0.9"), Interface: 7}} // Ethernet renumbered
	p.reconcile(nil)
	if !first.stopped || backend.live["myapp.local"].addrs[1].IP.String() != "10.0.0.9" {
		t.Fatal("a change on the second network did not replace the record")
	}
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
