package localproxy

import (
	"bufio"
	"net"
	"reflect"
	"strconv"
	"testing"
)

// echo is a line-echo server on loopback, standing in for a database.
func echo(t *testing.T) (string, int) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				line, _ := bufio.NewReader(conn).ReadString('\n')
				_, _ = conn.Write([]byte("echo " + line))
			}()
		}
	}()
	return l.Addr().String(), l.Addr().(*net.TCPAddr).Port
}

func TestTCPForwardRelaysFromLANAddresses(t *testing.T) {
	target, _ := echo(t)
	useAddresses(t, "127.0.0.1", "127.0.0.2")
	port := freePort(t)
	f := ForwardTCP(port, target)
	defer f.Close()
	if got := f.Addresses(); !reflect.DeepEqual(got, []string{"127.0.0.2"}) {
		t.Skipf("this system cannot bind 127.0.0.2 (bound %v)", got)
	}

	conn, err := net.Dial("tcp", "127.0.0.2:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = conn.Write([]byte("select 1\n"))
	reply, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil || reply != "echo select 1\n" {
		t.Fatalf("reply = %q, %v", reply, err)
	}
}

func TestTCPForwardLeavesATargetOnTheLANAlone(t *testing.T) {
	// The service already listens on every address: nothing for Pier to relay.
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	useAddresses(t, "127.0.0.1", "127.0.0.2")
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	f := ForwardTCP(port, l.Addr().String())
	defer f.Close()
	if len(f.Addresses()) != 0 || !f.Reachable() {
		t.Fatalf("bound %v reachable=%v; want nothing bound and the port reachable", f.Addresses(), f.Reachable())
	}
}
