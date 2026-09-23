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

func TestParseLinuxDefaultInterfaceSkipsVirtualFirst(t *testing.T) {
	table := "Iface Destination Gateway Flags RefCnt Use Metric Mask MTU Window IRTT\n" +
		"docker0 010011AC 00000000 0001 0 0 0 00FFFFFF 0 0 0\n" +
		"eth0 00000000 0101A8C0 0003 0 0 0 00000000 0 0 0\n"
	if got := ParseLinuxDefaultInterface(table); got != "eth0" {
		t.Fatalf("ParseLinuxDefaultInterface() = %q, want eth0", got)
	}
}

func TestParseWindowsDefaultIPv4(t *testing.T) {
	output := "Network Destination        Netmask          Gateway       Interface  Metric\n" +
		"          0.0.0.0          0.0.0.0    192.168.1.1   192.168.1.182     25\n"
	got := ParseWindowsDefaultIPv4(output)
	if got.String() != "192.168.1.182" {
		t.Fatalf("ParseWindowsDefaultIPv4() = %v, want 192.168.1.182", got)
	}
}

func TestParseDefaultInterface(t *testing.T) {
	output := "destination: default\n    gateway: 192.168.1.1\n  interface: en0\n"
	if got := ParseDefaultInterface(output); got != "en0" {
		t.Fatalf("ParseDefaultInterface() = %q, want en0", got)
	}
}
