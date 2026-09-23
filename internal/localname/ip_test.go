package localname

import (
	"net"
	"testing"
)

func TestSelectIPv4PrefersAPrivateAddress(t *testing.T) {
	got := SelectIPv4([]net.IP{
		net.ParseIP("127.0.0.1"),
		net.ParseIP("169.254.1.1"),
		net.ParseIP("8.8.8.8"),
		net.ParseIP("192.168.1.182"),
	})
	if got.String() != "192.168.1.182" {
		t.Fatalf("SelectIPv4() = %s, want 192.168.1.182", got)
	}
}

func TestParseDefaultInterface(t *testing.T) {
	output := "destination: default\n    gateway: 192.168.1.1\n  interface: en0\n"
	if got := ParseDefaultInterface(output); got != "en0" {
		t.Fatalf("ParseDefaultInterface() = %q, want en0", got)
	}
}
