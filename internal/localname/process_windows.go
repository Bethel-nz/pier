//go:build windows

package localname

import (
	"context"
	"fmt"
	"net"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

const (
	dnsQueryRequestVersion1 = 1
	dnsRequestPending       = 9506
)

var (
	dnsapi         = syscall.NewLazyDLL("dnsapi.dll")
	procRegister   = dnsapi.NewProc("DnsServiceRegister")
	procDeregister = dnsapi.NewProc("DnsServiceDeRegister")
	registerDone   = syscall.NewCallback(func(uintptr, uintptr, uintptr) uintptr { return 0 })
)

// dnsServiceInstance matches the 64-bit Windows DNS_SERVICE_INSTANCE layout.
type dnsServiceInstance struct {
	instanceName   *uint16
	hostName       *uint16
	ip4            *uint32
	ip6            uintptr
	port           uint16
	priority       uint16
	weight         uint16
	padPort        uint16
	propertyCount  uint32
	keys           uintptr
	values         uintptr
	interfaceIndex uint32
	padEnd         uint32
}

// dnsServiceRegisterRequest matches the 64-bit Windows DNS_SERVICE_REGISTER_REQUEST layout.
type dnsServiceRegisterRequest struct {
	version        uint32
	interfaceIndex uint32
	instance       *dnsServiceInstance
	callback       uintptr
	queryContext   uintptr
	credentials    uintptr
	unicast        uint32
	padEnd         uint32
}

type dnsServiceCancel struct {
	reserved uintptr
}

type windowsRegistration struct {
	addr     uint32
	instance dnsServiceInstance
	request  dnsServiceRegisterRequest
	cancel   dnsServiceCancel
	pin      runtime.Pinner
}

// DNSAnnouncer publishes names through the Windows DNS-SD API.
type DNSAnnouncer struct {
	mu   sync.Mutex
	next int
	live map[int]*windowsRegistration
}

// NewAnnouncer publishes names through the Windows DNS-SD API.
func NewAnnouncer() Announcer { return &DNSAnnouncer{} }

// OSProcesses reports that a Windows registration is not a process id.
func (a *DNSAnnouncer) OSProcesses() bool { return false }

// Start registers my-app.local at the current address.
func (a *DNSAnnouncer) Start(_ context.Context, record Record, ip net.IP) (int, error) {
	if ip.To4() == nil {
		return 0, fmt.Errorf("Pier could not publish %s without an IPv4 address", record.Name)
	}
	host, err := syscall.UTF16PtrFromString(record.Name)
	if err != nil {
		return 0, err
	}
	instanceName, err := syscall.UTF16PtrFromString(InstanceName(record.Name))
	if err != nil {
		return 0, err
	}
	reg := &windowsRegistration{}
	reg.addr = ipv4Dword(ip)
	reg.instance = dnsServiceInstance{
		instanceName: instanceName,
		hostName:     host,
		ip4:          &reg.addr,
		port:         record.Port,
	}
	reg.request = dnsServiceRegisterRequest{
		version:  dnsQueryRequestVersion1,
		instance: &reg.instance,
		callback: registerDone,
	}
	reg.pin.Pin(reg)
	status, _, callErr := procRegister.Call(
		uintptr(unsafe.Pointer(&reg.request)),
		uintptr(unsafe.Pointer(&reg.cancel)),
	)
	if status != 0 && status != dnsRequestPending {
		reg.pin.Unpin()
		if callErr != syscall.Errno(0) {
			return 0, fmt.Errorf("Pier could not publish %s: %w", record.Name, callErr)
		}
		return 0, fmt.Errorf("Pier could not publish %s: Windows DNS-SD status %d", record.Name, status)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.next++
	if a.live == nil {
		a.live = map[int]*windowsRegistration{}
	}
	a.live[a.next] = reg
	return a.next, nil
}

// Stop withdraws one Windows registration.
func (a *DNSAnnouncer) Stop(id int) error {
	a.mu.Lock()
	reg := a.live[id]
	delete(a.live, id)
	a.mu.Unlock()
	if reg == nil {
		return nil
	}
	_, _, _ = procDeregister.Call(
		uintptr(unsafe.Pointer(&reg.request)),
		uintptr(unsafe.Pointer(&reg.cancel)),
	)
	reg.pin.Unpin()
	return nil
}

func stopPID(int) error { return nil }

func ipv4Dword(ip net.IP) uint32 {
	b := ip.To4()
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}
