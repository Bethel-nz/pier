package localname

import (
	"context"
	"net"
	"reflect"
	"testing"
)

func TestPublishArgsUsesTheCurrentAddress(t *testing.T) {
	got := PublishArgs("holo-api.local", 4000, net.ParseIP("192.168.1.182"))
	want := []string{"-P", "holo-api", "_http._tcp", "local", "4000", "holo-api.local", "192.168.1.182"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PublishArgs() = %#v, want %#v", got, want)
	}
}

type fakeAnnouncer struct {
	next int
	live map[int][]string
	stop []int
}

func (f *fakeAnnouncer) Start(_ context.Context, args []string) (int, error) {
	f.next++
	if f.live == nil {
		f.live = map[int][]string{}
	}
	f.live[f.next] = append([]string(nil), args...)
	return f.next, nil
}

func (f *fakeAnnouncer) Stop(pid int) error {
	f.stop = append(f.stop, pid)
	delete(f.live, pid)
	return nil
}

func TestReconcileReplacesTheAddressAndWithdrawsRemovedNames(t *testing.T) {
	ann := &fakeAnnouncer{}
	pub := NewPublisher(ann)
	ctx := context.Background()
	records := []Record{{Name: "holo-api.local", Port: 4000}, {Name: "holo-ws.local", Port: 3001}}

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
	if err := pub.Reconcile(ctx, []Record{{Name: "holo-api.local", Port: 4000}}, net.ParseIP("192.168.1.182")); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if err := pub.StopAll(); err != nil {
		t.Fatalf("StopAll() error = %v", err)
	}
	if len(ann.live) != 0 {
		t.Fatalf("live advertisements = %d, want 0", len(ann.live))
	}
}
