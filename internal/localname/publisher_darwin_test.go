//go:build darwin

package localname

import (
	"net"
	"testing"

	"github.com/Bethel-nz/pier/internal/dnssd"
	"github.com/Bethel-nz/pier/internal/mdns"
)

func TestHostRecordsScopeEachAddressToItsInterface(t *testing.T) {
	records := hostRecords("myapp.local", []mdns.Address{
		{IP: net.ParseIP("192.168.1.10"), Interface: 4},
		{IP: net.ParseIP("10.0.0.5"), Interface: 7},
	})
	type kind struct {
		typ   uint16
		iface uint32
	}
	var got []kind
	for _, rec := range records {
		if rec.Name != "myapp.local" {
			t.Fatalf("record for %q", rec.Name)
		}
		got = append(got, kind{rec.Type, rec.Interface})
	}
	want := []kind{{dnssd.TypeA, 4}, {dnssd.TypeNSEC, 4}, {dnssd.TypeA, 7}, {dnssd.TypeNSEC, 7}}
	if len(got) != len(want) {
		t.Fatalf("records = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("records = %+v, want %+v", got, want)
		}
	}
}
