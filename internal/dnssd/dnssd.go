//go:build unix

// Package dnssd registers DNS records with the system's mDNSResponder by
// speaking its client protocol over the daemon's Unix socket, the same
// requests the C library's DNSServiceCreateConnection and
// DNSServiceRegisterRecord send. It needs no cgo and no helper process: the
// records live exactly as long as the connection.
package dnssd

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"syscall"
	"time"
)

// SocketPath is where mDNSResponder listens.
const SocketPath = "/var/run/mDNSResponder"

// Record types and flags from dns_sd.h.
const (
	TypeA    uint16 = 1
	TypeAAAA uint16 = 28
	TypeNSEC uint16 = 47

	classIN    uint16 = 1
	flagUnique uint32 = 0x20
)

// Request and reply operations from dnssd_ipc.h.
const (
	opConnection   uint32 = 1
	opRegRecord    uint32 = 2
	opRegRecordRep uint32 = 69
	opAsyncError   uint32 = 73

	ipcVersion = 1
	headerLen  = 28
	timeout    = 5 * time.Second
)

// Record is one resource record to register. Interface 0 means every
// interface; otherwise the record is answered only on that interface index.
type Record struct {
	Name      string
	Type      uint16
	Interface uint32
	Data      []byte
	TTL       uint32
}

// Error is a DNSServiceErrorType returned by the daemon.
type Error int32

// ErrNameConflict is kDNSServiceErr_NameConflict: another host owns the name.
const ErrNameConflict Error = -65548

func (e Error) Error() string {
	if e == ErrNameConflict {
		return "mDNSResponder: another device on the network already uses this name"
	}
	return fmt.Sprintf("mDNSResponder error %d", int32(e))
}

// Conn is one connection to mDNSResponder holding a set of unique records.
type Conn struct {
	conn *net.UnixConn

	mu        sync.Mutex
	next      uint32
	pending   map[uint32]bool // record index -> still probing
	err       error
	done      chan struct{}
	closeOnce sync.Once
}

// Dial connects to the daemon at path.
func Dial(path string) (*Conn, error) {
	raw, err := net.DialTimeout("unix", path, timeout)
	if err != nil {
		return nil, fmt.Errorf("mDNSResponder is not reachable: %w", err)
	}
	c := &Conn{conn: raw.(*net.UnixConn), pending: map[uint32]bool{}, done: make(chan struct{})}
	if err := c.request(opConnection, 0, nil); err != nil {
		_ = raw.Close()
		return nil, err
	}
	go c.readReplies()
	return c, nil
}

// Register adds a unique record. It returns once the daemon accepts the
// request; probing continues afterwards, and Live reports when it is done.
func (c *Conn) Register(rec Record) error {
	if len(rec.Data) > 0xffff {
		return errors.New("dnssd: record data too long")
	}
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return c.err
	}
	c.next++
	index := c.next
	c.pending[index] = true
	c.mu.Unlock()

	body := make([]byte, 0, 32+len(rec.Name)+len(rec.Data))
	body = append(body, 0) // empty control path: the error comes back on the passed socket
	body = binary.BigEndian.AppendUint32(body, flagUnique)
	body = binary.BigEndian.AppendUint32(body, rec.Interface)
	body = append(body, rec.Name...)
	body = append(body, 0)
	body = binary.BigEndian.AppendUint16(body, rec.Type)
	body = binary.BigEndian.AppendUint16(body, classIN)
	body = binary.BigEndian.AppendUint16(body, uint16(len(rec.Data)))
	body = append(body, rec.Data...)
	body = binary.BigEndian.AppendUint32(body, rec.TTL)

	errSock, err := c.returnSocket(index, body)
	if err == nil {
		err = readStatus(errSock)
		_ = errSock.Close()
	}
	if err != nil {
		c.mu.Lock()
		delete(c.pending, index)
		c.mu.Unlock()
		return err
	}
	return nil
}

// Live reports whether every registered record has finished probing.
func (c *Conn) Live() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil || c.next == 0 {
		return false
	}
	for _, probing := range c.pending {
		if probing {
			return false
		}
	}
	return true
}

// Done is closed when the connection ends: Close, a record the daemon
// rejected (such as a name conflict), or the daemon going away.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err says why Done was closed, or nil after Close.
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if errors.Is(c.err, net.ErrClosed) {
		return nil
	}
	return c.err
}

// Close withdraws every record: the daemon sends goodbyes for them.
func (c *Conn) Close() error {
	c.fail(net.ErrClosed)
	return nil
}

func (c *Conn) fail(err error) {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.err = err
		c.mu.Unlock()
		_ = c.conn.Close()
		close(c.done)
	})
}

func header(op, datalen, index uint32) []byte {
	h := make([]byte, headerLen)
	binary.BigEndian.PutUint32(h[0:], ipcVersion)
	binary.BigEndian.PutUint32(h[4:], datalen)
	binary.BigEndian.PutUint32(h[8:], 0) // ipc_flags
	binary.BigEndian.PutUint32(h[12:], op)
	binary.BigEndian.PutUint32(h[16:], index) // client_context: echoed in replies
	binary.BigEndian.PutUint32(h[20:], 0)
	binary.BigEndian.PutUint32(h[24:], index) // reg_index
	return h
}

// request sends a request whose status comes back on the main socket.
func (c *Conn) request(op, index uint32, body []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := c.conn.Write(append(header(op, uint32(len(body)), index), body...)); err != nil {
		return err
	}
	return readStatus(c.conn)
}

// returnSocket sends a request whose status comes back on a socket of its
// own, passed to the daemon with the request's last byte, as the C library
// does for record registrations.
func (c *Conn) returnSocket(index uint32, body []byte) (*os.File, error) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, err
	}
	ours := os.NewFile(uintptr(fds[0]), "dnssd-status")
	theirs := fds[1]
	defer syscall.Close(theirs)

	msg := append(header(opRegRecord, uint32(len(body)), index), body...)
	c.mu.Lock()
	_, err = c.conn.Write(msg[:len(msg)-1])
	if err == nil {
		_, _, err = c.conn.WriteMsgUnix(msg[len(msg)-1:], syscall.UnixRights(theirs), nil)
	}
	c.mu.Unlock()
	if err != nil {
		_ = ours.Close()
		return nil, err
	}
	return ours, nil
}

type deadliner interface {
	io.Reader
	SetReadDeadline(time.Time) error
}

func readStatus(r deadliner) error {
	_ = r.SetReadDeadline(time.Now().Add(timeout))
	defer r.SetReadDeadline(time.Time{})
	var status [4]byte
	if _, err := io.ReadFull(r, status[:]); err != nil {
		return fmt.Errorf("mDNSResponder did not answer: %w", err)
	}
	if code := int32(binary.BigEndian.Uint32(status[:])); code != 0 {
		return Error(code)
	}
	return nil
}

// readReplies tracks probing results until the connection ends.
func (c *Conn) readReplies() {
	head := make([]byte, headerLen)
	for {
		if _, err := io.ReadFull(c.conn, head); err != nil {
			c.fail(fmt.Errorf("mDNSResponder closed the connection: %w", err))
			return
		}
		datalen := binary.BigEndian.Uint32(head[4:])
		op := binary.BigEndian.Uint32(head[12:])
		index := binary.BigEndian.Uint32(head[16:])
		if datalen > 1<<16 {
			c.fail(errors.New("mDNSResponder sent an oversized reply"))
			return
		}
		data := make([]byte, datalen)
		if _, err := io.ReadFull(c.conn, data); err != nil {
			c.fail(fmt.Errorf("mDNSResponder closed the connection: %w", err))
			return
		}
		if op != opRegRecordRep && op != opAsyncError {
			continue
		}
		code := int32(0)
		if len(data) >= 12 {
			code = int32(binary.BigEndian.Uint32(data[8:])) // after flags and interface
		}
		if code != 0 {
			c.fail(Error(code)) // the record is gone; the caller registers again later
			return
		}
		c.mu.Lock()
		if _, ok := c.pending[index]; ok {
			c.pending[index] = false
		}
		c.mu.Unlock()
	}
}

// AddressData is the rdata of an A or AAAA record for ip.
func AddressData(ip net.IP) (uint16, []byte) {
	if v4 := ip.To4(); v4 != nil {
		return TypeA, append([]byte(nil), v4...)
	}
	return TypeAAAA, append([]byte(nil), ip.To16()...)
}

// NSECData is the rdata of an mDNS NSEC record (RFC 6762 §6.1) stating that
// name has records of exactly the given types, all below 256.
func NSECData(name string, types ...uint16) []byte {
	data := encodeName(name)
	var bitmap [32]byte
	length := 0
	for _, t := range types {
		if t >= 256 {
			continue
		}
		bitmap[t/8] |= 0x80 >> (t % 8)
		length = max(length, int(t/8)+1)
	}
	data = append(data, 0, byte(length))
	return append(data, bitmap[:length]...)
}

func encodeName(name string) []byte {
	var out []byte
	start := 0
	for i := 0; i <= len(name); i++ {
		if i == len(name) || name[i] == '.' {
			if label := name[start:i]; label != "" {
				out = append(out, byte(len(label)))
				out = append(out, label...)
			}
			start = i + 1
		}
	}
	return append(out, 0)
}
