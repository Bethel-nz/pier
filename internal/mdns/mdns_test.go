package mdns

import (
	"context"
	"net"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func question(t *testing.T, name string, typ dnsmessage.Type, class dnsmessage.Class) []byte {
	t.Helper()
	msg := dnsmessage.Message{
		Header:    dnsmessage.Header{ID: 42},
		Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName(name), Type: typ, Class: class}},
	}
	packet, err := msg.Pack()
	if err != nil {
		t.Fatal(err)
	}
	return packet
}

func owned(names ...string) func(string) bool {
	set := map[string]bool{}
	for _, name := range names {
		set[canonical(name)] = true
	}
	return func(name string) bool { return set[name] }
}

func TestReplyAnswersOwnedAddressQuery(t *testing.T) {
	q, err := parse(question(t, "My-App.local.", dnsmessage.TypeA, dnsmessage.ClassINET))
	if err != nil {
		t.Fatal(err)
	}
	msg := reply(q, owned("my-app.local"), net.ParseIP("192.168.1.20"), false)
	if msg == nil || len(msg.Answers) != 1 {
		t.Fatalf("reply = %#v, want one answer", msg)
	}
	answer := msg.Answers[0]
	if got := net.IP(answer.Body.(*dnsmessage.AResource).A[:]).String(); got != "192.168.1.20" {
		t.Fatalf("address = %s, want 192.168.1.20", got)
	}
	if answer.Header.TTL != hostTTL || answer.Header.Class&cacheFlush == 0 {
		t.Fatalf("header = %+v, want TTL %d with cache-flush", answer.Header, hostTTL)
	}
	if msg.Header.ID != 0 || len(msg.Questions) != 0 {
		t.Fatal("multicast reply must use id 0 and no question section")
	}
}

func TestLegacyReplyEchoesQueryAndShortensTTL(t *testing.T) {
	q, _ := parse(question(t, "my-app.local.", dnsmessage.TypeA, dnsmessage.ClassINET))
	msg := reply(q, owned("my-app.local"), net.ParseIP("10.0.0.5"), true)
	if msg == nil {
		t.Fatal("no reply")
	}
	if msg.Header.ID != 42 || len(msg.Questions) != 1 {
		t.Fatalf("legacy reply id=%d questions=%d, want echo", msg.Header.ID, len(msg.Questions))
	}
	if msg.Answers[0].Header.TTL != legacyTTL || msg.Answers[0].Header.Class&cacheFlush != 0 {
		t.Fatalf("legacy header = %+v, want short TTL without cache-flush", msg.Answers[0].Header)
	}
}

func TestReplyIgnoresNamesPierDoesNotOwn(t *testing.T) {
	q, _ := parse(question(t, "printer.local.", dnsmessage.TypeA, dnsmessage.ClassINET))
	if msg := reply(q, owned("my-app.local"), net.ParseIP("10.0.0.5"), false); msg != nil {
		t.Fatalf("reply = %#v, want nil", msg)
	}
}

func TestIPv6QueryGetsNegativeAnswer(t *testing.T) {
	q, _ := parse(question(t, "my-app.local.", dnsmessage.TypeAAAA, dnsmessage.ClassINET))
	msg := reply(q, owned("my-app.local"), net.ParseIP("10.0.0.5"), false)
	if msg == nil || len(msg.Answers) != 1 || msg.Answers[0].Header.Type != typeNSEC {
		t.Fatalf("reply = %#v, want one NSEC", msg)
	}
	if _, err := msg.Pack(); err != nil {
		t.Fatalf("Pack() = %v", err)
	}
}

func TestKnownAnswerIsSuppressed(t *testing.T) {
	name := dnsmessage.MustNewName("my-app.local.")
	msg := dnsmessage.Message{
		Questions: []dnsmessage.Question{{Name: name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}},
		Answers:   []dnsmessage.Resource{addressRecord(name, net.ParseIP("10.0.0.5"), false, hostTTL)},
	}
	packet, err := msg.Pack()
	if err != nil {
		t.Fatal(err)
	}
	q, _ := parse(packet)
	if got := reply(q, owned("my-app.local"), net.ParseIP("10.0.0.5"), false); got != nil {
		t.Fatalf("reply = %#v, want suppression", got)
	}
}

func TestUnicastBit(t *testing.T) {
	q, _ := parse(question(t, "my-app.local.", dnsmessage.TypeA, dnsmessage.ClassINET|unicastReply))
	if !wantsUnicast(q) {
		t.Fatal("QU bit not detected")
	}
}

func TestProbeAndAnnouncementPack(t *testing.T) {
	if _, err := probe("my-app.local", net.ParseIP("10.0.0.5")); err != nil {
		t.Fatal(err)
	}
	packet, err := announcement("my-app.local", net.ParseIP("10.0.0.5"), 0)
	if err != nil {
		t.Fatal(err)
	}
	q, _ := parse(packet)
	if !q.response || len(q.answers) != 1 || q.answers[0].Header.TTL != 0 {
		t.Fatalf("goodbye = %+v, want one TTL 0 answer", q)
	}
}

func TestVirtualInterfacesAreSkipped(t *testing.T) {
	for _, name := range []string{"docker0", "utun3", "tailscale0", "vEthernet (WSL)", "br-1a2b", "veth12"} {
		if !isVirtual(name) {
			t.Fatalf("isVirtual(%q) = false", name)
		}
	}
	for _, name := range []string{"en0", "eth0", "wlan0", "Wi-Fi", "wlp2s0"} {
		if isVirtual(name) {
			t.Fatalf("isVirtual(%q) = true", name)
		}
	}
	if usableIPv4(net.ParseIP("100.101.102.103")) {
		t.Fatal("Tailscale CGNAT address must not be advertised on the LAN")
	}
}

func startResponder(t *testing.T) (*Responder, func()) {
	t.Helper()
	r, err := Listen(Options{Addr: "127.0.0.1:0", ProbeInterval: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = r.Serve(ctx); close(done) }()
	return r, func() { cancel(); <-done }
}

func waitState(t *testing.T, r *Responder, want State) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		statuses := r.Statuses()
		if len(statuses) == 1 && statuses[0].State == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("state = %+v, want %s", r.Statuses(), want)
}

func TestResponderAnswersAfterProbing(t *testing.T) {
	r, stop := startResponder(t)
	defer stop()

	client, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ask := func() *dnsmessage.Message {
		if _, err := client.WriteTo(question(t, "my-app.local.", dnsmessage.TypeA, dnsmessage.ClassINET), r.LocalAddr()); err != nil {
			t.Fatal(err)
		}
		_ = client.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		buf := make([]byte, 1500)
		n, _, err := client.ReadFrom(buf)
		if err != nil {
			return nil
		}
		var msg dnsmessage.Message
		if err := msg.Unpack(buf[:n]); err != nil {
			t.Fatal(err)
		}
		return &msg
	}

	r.SetNames([]string{"my-app.local"})
	waitState(t, r, Live)
	msg := ask()
	if msg == nil || len(msg.Answers) == 0 {
		t.Fatal("no answer from a live name")
	}
	if msg.Header.ID != 42 {
		t.Fatalf("id = %d, want the legacy query id echoed", msg.Header.ID)
	}
	if got := net.IP(msg.Answers[0].Body.(*dnsmessage.AResource).A[:]); !got.Equal(net.IPv4(127, 0, 0, 1)) {
		t.Fatalf("address = %s, want the address that reaches the querier", got)
	}

	r.SetNames(nil)
	if msg := ask(); msg != nil {
		t.Fatalf("answer after removal = %+v, want silence", msg)
	}
}

func TestResponderReportsAnotherHostClaimingTheName(t *testing.T) {
	r, stop := startResponder(t)
	defer stop()
	r.SetNames([]string{"my-app.local"})
	waitState(t, r, Live)

	other, err := announcement("my-app.local", net.ParseIP("192.168.9.9"), hostTTL)
	if err != nil {
		t.Fatal(err)
	}
	sender, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	if _, err := sender.WriteTo(other, r.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	waitState(t, r, Conflict)
	if got := r.Statuses()[0].Other; got != "192.168.9.9" {
		t.Fatalf("conflict address = %q", got)
	}
}
