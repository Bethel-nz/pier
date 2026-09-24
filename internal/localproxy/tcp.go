package localproxy

import (
	"io"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"
)

// TCPForward relays raw TCP from this machine's LAN addresses on one port to
// a loopback target: db.local:5432 reaching a database on 127.0.0.1:5432.
//
// It binds each LAN address on its own and never loopback, where the target
// itself usually listens. An address where something already answers is left
// alone: often the target listening everywhere, which the LAN reaches directly.
type TCPForward struct {
	Port   int
	target string

	mu       sync.Mutex
	bound    map[string]net.Listener
	answered []string // addresses already served by another program
	closed   bool
}

// ForwardTCP starts relaying port to target, a host:port.
func ForwardTCP(port int, target string) *TCPForward {
	f := &TCPForward{Port: port, target: target, bound: map[string]net.Listener{}}
	f.Refresh()
	return f
}

// Refresh binds LAN addresses that appeared and releases ones that went away.
func (f *TCPForward) Refresh() {
	var want []string
	for _, addr := range machineAddresses() {
		if addr != "127.0.0.1" {
			want = append(want, addr)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	wanted := map[string]bool{}
	for _, addr := range want {
		wanted[addr] = true
	}
	for addr, listener := range f.bound {
		if !wanted[addr] {
			_ = listener.Close()
			delete(f.bound, addr)
		}
	}
	f.answered = f.answered[:0]
	for _, addr := range want {
		if _, ok := f.bound[addr]; ok {
			continue
		}
		hostPort := net.JoinHostPort(addr, strconv.Itoa(f.Port))
		if answers(hostPort) {
			f.answered = append(f.answered, addr)
			continue
		}
		listener, err := net.Listen("tcp", hostPort)
		if err != nil {
			continue
		}
		f.bound[addr] = listener
		go f.accept(listener)
	}
}

// Reachable reports whether the port answers on the LAN at all: through
// Pier's relay, or because the target already listens there.
func (f *TCPForward) Reachable() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.bound) > 0 || len(f.answered) > 0
}

// Addresses lists where Pier relays, for status output and tests.
func (f *TCPForward) Addresses() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.bound))
	for addr := range f.bound {
		out = append(out, addr)
	}
	sort.Strings(out)
	return out
}

// Close stops relaying. Connections already open run to their end.
func (f *TCPForward) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	for addr, listener := range f.bound {
		_ = listener.Close()
		delete(f.bound, addr)
	}
}

func (f *TCPForward) accept(listener net.Listener) {
	for {
		client, err := listener.Accept()
		if err != nil {
			return
		}
		go f.relay(client)
	}
}

func (f *TCPForward) relay(client net.Conn) {
	defer client.Close()
	upstream, err := net.DialTimeout("tcp", f.target, 5*time.Second)
	if err != nil {
		return // the service is down; the client sees the connection close
	}
	defer upstream.Close()
	done := make(chan struct{}, 2)
	pipe := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		if tcp, ok := dst.(*net.TCPConn); ok {
			_ = tcp.CloseWrite() // pass the half-close on, as protocols like SMTP expect
		}
		done <- struct{}{}
	}
	go pipe(upstream, client)
	go pipe(client, upstream)
	<-done
	<-done
}
