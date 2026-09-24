//go:build windows

package localname

import (
	"encoding/binary"
	"fmt"
	"net"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
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
	procFree       = dnsapi.NewProc("DnsServiceFreeInstance")
	registerDone   = syscall.NewCallback(onRegisterDone)

	// pending maps a request's query context to its handle, so the
	// completion callback can report a rejected registration.
	pending   sync.Map
	nextToken atomic.Uintptr
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

// dnsClient registers names with the Windows DNS client, which answers mDNS
// for them for as long as this process holds the registration.
type dnsClient struct{}

type winHandle struct {
	addr     uint32
	instance dnsServiceInstance
	request  dnsServiceRegisterRequest
	cancel   dnsServiceCancel
	pin      runtime.Pinner

	failed   atomic.Bool
	stopping atomic.Bool
}

func systemBackend() backend {
	if procRegister.Find() != nil {
		return nil // older than Windows 10 1809
	}
	return dnsClient{}
}

func (dnsClient) kind() string { return "Windows DNS client" }

func (dnsClient) register(name, address string, port int) (handle, error) {
	ip := net.ParseIP(address).To4()
	if ip == nil {
		return nil, fmt.Errorf("%s is not an IPv4 address", address)
	}
	host, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	instanceName, err := syscall.UTF16PtrFromString(strings.TrimSuffix(name, ".local") + "._http._tcp.local")
	if err != nil {
		return nil, err
	}
	h := &winHandle{addr: binary.LittleEndian.Uint32(ip)} // IP4_ADDRESS keeps network byte order in memory
	h.instance = dnsServiceInstance{
		instanceName: instanceName,
		hostName:     host,
		ip4:          &h.addr,
		port:         uint16(port),
	}
	token := nextToken.Add(1)
	h.request = dnsServiceRegisterRequest{
		version:      dnsQueryRequestVersion1,
		instance:     &h.instance,
		callback:     registerDone,
		queryContext: token,
	}
	h.pin.Pin(h)
	h.pin.Pin(instanceName)
	h.pin.Pin(host)
	pending.Store(token, h)
	status, _, callErr := procRegister.Call(
		uintptr(unsafe.Pointer(&h.request)),
		uintptr(unsafe.Pointer(&h.cancel)),
	)
	if status != dnsRequestPending {
		pending.Delete(token)
		h.pin.Unpin()
		if callErr != syscall.Errno(0) {
			return nil, callErr
		}
		return nil, fmt.Errorf("DnsServiceRegister status %d", status)
	}
	return h, nil
}

// onRegisterDone runs when Windows finishes a registration or deregistration.
func onRegisterDone(status, queryContext, instance uintptr) uintptr {
	if instance != 0 {
		_, _, _ = procFree.Call(instance)
	}
	value, ok := pending.Load(queryContext)
	if !ok {
		return 0
	}
	h := value.(*winHandle)
	if h.stopping.Load() {
		pending.Delete(queryContext)
		h.pin.Unpin() // Windows is done with the request memory
		return 0
	}
	if uint32(status) != 0 {
		h.failed.Store(true)
	}
	return 0
}

func (h *winHandle) live() bool   { return !h.failed.Load() }
func (h *winHandle) exited() bool { return h.failed.Load() }

// stop withdraws the record. The request stays pinned until Windows confirms,
// since it still reads it until then.
func (h *winHandle) stop() {
	h.stopping.Store(true)
	_, _, _ = procDeregister.Call(
		uintptr(unsafe.Pointer(&h.request)),
		uintptr(unsafe.Pointer(&h.cancel)),
	)
}

// sweepPublishers has nothing to do on Windows: registrations belong to the
// process and end with it.
func sweepPublishers() {}
