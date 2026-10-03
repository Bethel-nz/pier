//go:build unix

package dnssd

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// fakeDaemon accepts one client and answers like mDNSResponder: a status for
// the connection request, a status on the passed socket for each record, and
// then an asynchronous probing result from replies.
type fakeDaemon struct {
	path    string
	records chan Record
	replies chan int32 // probing result per record, in order
}

func startDaemon(t *testing.T) *fakeDaemon {
	t.Helper()
	dir, err := os.MkdirTemp("", "dnssd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	d := &fakeDaemon{path: filepath.Join(dir, "sock"), records: make(chan Record, 8), replies: make(chan int32, 8)}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: d.path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go d.serve(t, listener)
	return d
}

func (d *fakeDaemon) serve(t *testing.T, listener *net.UnixListener) {
	conn, err := listener.AcceptUnix()
	if err != nil {
		return
	}
	defer conn.Close()
	for {
		head := make([]byte, headerLen)
		if _, err := io.ReadFull(conn, head); err != nil {
			return
		}
		if binary.BigEndian.Uint32(head[0:]) != ipcVersion {
			t.Errorf("version %d, want %d", binary.BigEndian.Uint32(head[0:]), ipcVersion)
			return
		}
		datalen := binary.BigEndian.Uint32(head[4:])
		op := binary.BigEndian.Uint32(head[12:])
		switch op {
		case opConnection:
			conn.Write([]byte{0, 0, 0, 0})
		case opRegRecord:
			body := make([]byte, datalen)
			if _, err := io.ReadFull(conn, body[:datalen-1]); err != nil {
				return
			}
			oob := make([]byte, syscall.CmsgSpace(4))
			_, oobn, _, _, err := conn.ReadMsgUnix(body[datalen-1:], oob)
			if err != nil {
				t.Errorf("ReadMsgUnix: %v", err)
				return
			}
			msgs, _ := syscall.ParseSocketControlMessage(oob[:oobn])
			fds, err := syscall.ParseUnixRights(&msgs[0])
			if err != nil || len(fds) != 1 {
				t.Errorf("no return socket passed: %v", err)
				return
			}
			status := os.NewFile(uintptr(fds[0]), "status")
			status.Write([]byte{0, 0, 0, 0})
			status.Close()
			d.records <- parseRecord(t, body)
			code := <-d.replies
			reply := append(append([]byte(nil), head...), make([]byte, 12)...)
			binary.BigEndian.PutUint32(reply[4:], 12)
			binary.BigEndian.PutUint32(reply[12:], opRegRecordRep)
			binary.BigEndian.PutUint32(reply[headerLen+8:], uint32(code))
			conn.Write(reply)
		default:
			t.Errorf("unexpected op %d", op)
			return
		}
	}
}

func parseRecord(t *testing.T, body []byte) Record {
	t.Helper()
	if body[0] != 0 {
		t.Errorf("control path %q, want empty", body[0])
	}
	body = body[1:]
	if flags := binary.BigEndian.Uint32(body); flags != flagUnique {
		t.Errorf("flags %#x, want unique", flags)
	}
	rec := Record{Interface: binary.BigEndian.Uint32(body[4:])}
	body = body[8:]
	end := bytes.IndexByte(body, 0)
	rec.Name, body = string(body[:end]), body[end+1:]
	rec.Type = binary.BigEndian.Uint16(body)
	if class := binary.BigEndian.Uint16(body[2:]); class != classIN {
		t.Errorf("class %d, want IN", class)
	}
	n := binary.BigEndian.Uint16(body[4:])
	rec.Data = body[6 : 6+n]
	rec.TTL = binary.BigEndian.Uint32(body[6+n:])
	return rec
}

func TestRegisterSendsRecordsAndTracksProbing(t *testing.T) {
	d := startDaemon(t)
	c, err := Dial(d.path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	typ, data := AddressData(net.ParseIP("192.168.1.20"))
	want := Record{Name: "myapp.local", Type: typ, Interface: 4, Data: data, TTL: 120}
	if err := c.Register(want); err != nil {
		t.Fatalf("Register() = %v", err)
	}
	got := <-d.records
	if got.Name != want.Name || got.Type != TypeA || got.Interface != 4 || !bytes.Equal(got.Data, data) || got.TTL != 120 {
		t.Fatalf("daemon got %+v, want %+v", got, want)
	}
	if c.Live() {
		t.Fatal("Live() before probing finished")
	}
	d.replies <- 0
	waitFor(t, c.Live)
}

func TestRejectedRecordEndsTheConnection(t *testing.T) {
	d := startDaemon(t)
	c, err := Dial(d.path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Register(Record{Name: "myapp.local", Type: TypeA, Data: []byte{10, 0, 0, 5}, TTL: 120}); err != nil {
		t.Fatal(err)
	}
	<-d.records
	d.replies <- int32(ErrNameConflict)
	select {
	case <-c.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("connection still open after a conflict")
	}
	if c.Err() != ErrNameConflict || c.Live() {
		t.Fatalf("Err() = %v Live() = %v, want a name conflict", c.Err(), c.Live())
	}
}

func TestNSECData(t *testing.T) {
	got := NSECData("myapp.local", TypeA)
	want := append([]byte("\x05myapp\x05local\x00"), 0, 1, 0x40)
	if !bytes.Equal(got, want) {
		t.Fatalf("NSECData(A) = %x, want %x", got, want)
	}
	got = NSECData("a.local", TypeA, TypeAAAA)
	if tail := got[len(got)-6:]; !bytes.Equal(tail, []byte{0, 4, 0x40, 0, 0, 0x08}) {
		t.Fatalf("NSECData(A, AAAA) bitmap = %x", tail)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition never held")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
