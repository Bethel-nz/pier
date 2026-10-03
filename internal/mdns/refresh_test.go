package mdns

import (
	"net"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

type sent struct {
	ip  string
	ttl uint32
}

// recordingResponder has one live name, a fake interface list, and records
// every multicast packet it would send.
func recordingResponder(t *testing.T, ifaces *map[int]netIface) (*Responder, *[]sent) {
	t.Helper()
	var out []sent
	r := &Responder{
		interval:   250 * time.Millisecond,
		names:      map[string]*entry{canonical("myapp.local"): {state: Live, announces: 2}},
		ifaces:     *ifaces, // already joined and announced
		self:       map[string]bool{},
		interfaces: func() map[int]netIface { return *ifaces },
		out: func(packet []byte, _ *net.Interface) {
			q, err := parse(packet)
			if err != nil || len(q.answers) != 1 {
				t.Fatalf("sent %v, want one answer", err)
			}
			out = append(out, sent{ip: addressOf(t, packet), ttl: q.answers[0].Header.TTL})
		},
	}
	return r, &out
}

func addressOf(t *testing.T, packet []byte) string {
	t.Helper()
	q, err := parse(packet)
	if err != nil {
		t.Fatal(err)
	}
	record, ok := q.answers[0].Body.(*dnsmessage.AResource)
	if !ok {
		t.Fatalf("answer %T, want an A record", q.answers[0].Body)
	}
	return net.IP(record.A[:]).String()
}

func wifi(ip string) netIface {
	return netIface{
		ifi:   net.Interface{Index: 4, Name: "en0", Flags: net.FlagUp | net.FlagMulticast},
		addrs: []*net.IPNet{{IP: net.ParseIP(ip).To4(), Mask: net.CIDRMask(24, 32)}},
	}
}

func TestAddressChangeSendsGoodbyeAndAnnouncesAgain(t *testing.T) {
	ifaces := map[int]netIface{4: wifi("192.168.1.20")}
	r, out := recordingResponder(t, &ifaces)

	// DHCP hands this machine a new address on the same network.
	ifaces = map[int]netIface{4: wifi("192.168.1.42")}
	r.refreshInterfaces()

	if len(*out) != 1 || (*out)[0] != (sent{ip: "192.168.1.20", ttl: 0}) {
		t.Fatalf("after refresh sent %+v, want one goodbye for 192.168.1.20", *out)
	}

	*out = nil
	start := time.Now()
	r.advance(start)
	r.advance(start.Add(500 * time.Millisecond)) // too soon for the second announcement
	r.advance(start.Add(1100 * time.Millisecond))
	r.advance(start.Add(2200 * time.Millisecond)) // already announced twice
	want := []sent{{ip: "192.168.1.42", ttl: hostTTL}, {ip: "192.168.1.42", ttl: hostTTL}}
	if len(*out) != len(want) || (*out)[0] != want[0] || (*out)[1] != want[1] {
		t.Fatalf("announcements = %+v, want %+v", *out, want)
	}
}

func TestNewInterfaceAnnouncesWithoutGoodbye(t *testing.T) {
	ifaces := map[int]netIface{4: wifi("192.168.1.20")}
	r, out := recordingResponder(t, &ifaces)

	ethernet := netIface{
		ifi:   net.Interface{Index: 7, Name: "en7", Flags: net.FlagUp | net.FlagMulticast},
		addrs: []*net.IPNet{{IP: net.ParseIP("10.0.0.5").To4(), Mask: net.CIDRMask(24, 32)}},
	}
	ifaces = map[int]netIface{4: wifi("192.168.1.20"), 7: ethernet}
	r.refreshInterfaces()
	if len(*out) != 0 {
		t.Fatalf("sent %+v on a new interface, want no goodbye", *out)
	}
	r.advance(time.Now())
	if len(*out) != 2 {
		t.Fatalf("sent %+v, want an announcement on each interface", *out)
	}
}

func TestUnchangedInterfacesDoNotAnnounceAgain(t *testing.T) {
	ifaces := map[int]netIface{4: wifi("192.168.1.20")}
	r, out := recordingResponder(t, &ifaces)

	r.refreshInterfaces()
	r.advance(time.Now().Add(time.Minute))
	if len(*out) != 0 {
		t.Fatalf("sent %+v with nothing changed, want nothing", *out)
	}
}
