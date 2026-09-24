package localproxy

import (
	"net"
	"reflect"
	"strconv"
	"testing"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

func useAddresses(t *testing.T, addrs ...string) {
	t.Helper()
	saved := machineAddresses
	machineAddresses = func() []string { return addrs }
	t.Cleanup(func() { machineAddresses = saved })
}

func TestBindFallsBackWhenAPortIsTakenEverywhere(t *testing.T) {
	busy, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	taken := busy.Addr().(*net.TCPAddr).Port
	next := freePort(t)

	served := 0
	l, err := Bind([]int{taken, next}, func(net.Listener) { served++ })
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if l.Port != next || l.PerAddress || served != 1 {
		t.Fatalf("port=%d perAddress=%v served=%d, want %d on every interface", l.Port, l.PerAddress, served, next)
	}
	if len(l.Skipped) != 1 {
		t.Fatalf("skipped = %v, want the taken port recorded", l.Skipped)
	}
}

// Tailscale Serve holds 8443 on the tailnet address only. Pier must share the
// port by binding its other addresses, not give up on it.
func TestBindSharesAPortHeldOnOneAddress(t *testing.T) {
	port := freePort(t)
	tailscale, err := net.Listen("tcp", "127.0.0.3:"+strconv.Itoa(port))
	if err != nil {
		t.Skip("this system cannot bind 127.0.0.3")
	}
	defer tailscale.Close()
	useAddresses(t, "127.0.0.1", "127.0.0.2", "127.0.0.3")

	l, err := Bind([]int{port}, func(net.Listener) {})
	if err != nil {
		t.Fatalf("Bind() = %v, want the port shared", err)
	}
	defer l.Close()
	if !l.PerAddress || !reflect.DeepEqual(l.Addresses(), []string{"127.0.0.1", "127.0.0.2"}) {
		t.Fatalf("perAddress=%v addresses=%v", l.PerAddress, l.Addresses())
	}

	// Wi-Fi changes: one address goes, another arrives.
	useAddresses(t, "127.0.0.1", "127.0.0.4")
	l.Refresh()
	if got := l.Addresses(); !reflect.DeepEqual(got, []string{"127.0.0.1", "127.0.0.4"}) {
		t.Fatalf("after refresh = %v", got)
	}
}
