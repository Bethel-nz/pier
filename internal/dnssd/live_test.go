//go:build darwin

package dnssd

import (
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestLiveRegistration registers a name with this Mac's mDNSResponder and
// resolves it. It touches the real network, so it runs only on request.
func TestLiveRegistration(t *testing.T) {
	if os.Getenv("PIER_LIVE_DNSSD") == "" {
		t.Skip("set PIER_LIVE_DNSSD=1 to register a name with mDNSResponder")
	}
	const name = "pier-dnssd-test.local"
	c, err := Dial(SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	typ, data := AddressData(net.ParseIP("192.0.2.77"))
	if err := c.Register(Record{Name: name, Type: typ, Data: data, TTL: 120}); err != nil {
		t.Fatalf("Register(A) = %v", err)
	}
	if err := c.Register(Record{Name: name, Type: TypeNSEC, Data: NSECData(name, TypeA), TTL: 120}); err != nil {
		t.Fatalf("Register(NSEC) = %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !c.Live() {
		if time.Now().After(deadline) {
			t.Fatalf("records never went live: %v", c.Err())
		}
		time.Sleep(100 * time.Millisecond)
	}
	out, _ := exec.Command("dscacheutil", "-q", "host", "-a", "name", name).Output()
	if !strings.Contains(string(out), "192.0.2.77") {
		t.Fatalf("lookup = %q, want 192.0.2.77", out)
	}

	// A second owner of the name is refused once it probes.
	other, err := Dial(SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	_, data = AddressData(net.ParseIP("192.0.2.78"))
	if err := other.Register(Record{Name: name, Type: TypeA, Data: data, TTL: 120}); err != nil {
		t.Fatalf("Register(second) = %v", err)
	}
	select {
	case <-other.Done():
		if other.Err() != ErrNameConflict {
			t.Fatalf("second owner ended with %v, want a name conflict", other.Err())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("second owner of the name was never refused")
	}
}
