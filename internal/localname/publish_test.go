package localname

import (
	"context"
	"encoding/binary"
	"net"
	"reflect"
	"testing"
)

func TestAvahiArgsAndInstanceName(t *testing.T) {
	args := AvahiArgs("my-app.local", net.ParseIP("192.168.1.182"))
	want := []string{"-a", "-R", "my-app.local", "192.168.1.182"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("AvahiArgs() = %#v, want %#v", args, want)
	}
	if got := InstanceName("my-app.local"); got != "my-app._http._tcp.local" {
		t.Fatalf("InstanceName() = %q", got)
	}
}

func TestIPv4DwordKeepsOctetOrder(t *testing.T) {
	value := ipv4Dword(net.ParseIP("192.168.1.182"))
	var raw [4]byte
	binary.LittleEndian.PutUint32(raw[:], value)
	want := []byte{192, 168, 1, 182}
	if !reflect.DeepEqual(raw[:], want) {
		t.Fatalf("address bytes = %v, want %v", raw, want)
	}
}

func TestPublishArgsUsesTheCurrentAddress(t *testing.T) {
	got := PublishArgs("my-app.local", 4000, net.ParseIP("192.168.1.182"))
	want := []string{"-P", "my-app", "_http._tcp", "local", "4000", "my-app.local", "192.168.1.182"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PublishArgs() = %#v, want %#v", got, want)
	}
}

type fakeAnnouncer struct {
	next int
	live map[int][]string
	stop []int
}

func (f *fakeAnnouncer) Start(_ context.Context, record Record, ip net.IP) (int, error) {
	f.next++
	if f.live == nil {
		f.live = map[int][]string{}
	}
	f.live[f.next] = []string{record.Name, ip.String()}
	return f.next, nil
}

func (f *fakeAnnouncer) OSProcesses() bool { return true }

func (f *fakeAnnouncer) Stop(pid int) error {
	f.stop = append(f.stop, pid)
	delete(f.live, pid)
	return nil
}

func TestReconcileReplacesTheAddressAndWithdrawsRemovedNames(t *testing.T) {
	ann := &fakeAnnouncer{}
	pub := NewPublisher(ann)
	ctx := context.Background()
	records := []Record{{Name: "my-app.local", Port: 4000}, {Name: "my-api.local", Port: 3001}}

	if err := pub.Reconcile(ctx, records, net.ParseIP("192.168.1.162")); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if err := pub.Reconcile(ctx, records[:1], net.ParseIP("192.168.1.182")); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	if len(ann.live) != 1 {
		t.Fatalf("live advertisements = %d, want 1", len(ann.live))
	}
	var args []string
	for _, item := range ann.live {
		args = item
	}
	if args[len(args)-1] != "192.168.1.182" {
		t.Fatalf("published address = %s, want 192.168.1.182", args[len(args)-1])
	}
	if len(ann.stop) != 2 {
		t.Fatalf("stopped processes = %v, want both the renamed api and the removed ws", ann.stop)
	}
}

func TestReconcileWithNoAddressWithdrawsEverything(t *testing.T) {
	ann := &fakeAnnouncer{}
	pub := NewPublisher(ann)
	ctx := context.Background()
	if err := pub.Reconcile(ctx, []Record{{Name: "my-app.local", Port: 4000}}, net.ParseIP("192.168.1.182")); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if err := pub.StopAll(); err != nil {
		t.Fatalf("StopAll() error = %v", err)
	}
	if len(ann.live) != 0 {
		t.Fatalf("live advertisements = %d, want 0", len(ann.live))
	}
}
