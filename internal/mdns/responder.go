// Package mdns answers multicast DNS queries for the .local names Pier owns.
//
// It runs inside Pier's own process: nothing to install, no helper processes
// to orphan. Each answer carries the address of this machine on the querier's
// own network, so a Wi-Fi renumber or a second network needs no tracking.
package mdns

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// State is the lifecycle of one published name.
type State string

const (
	// Probing means Pier is checking that no other host already uses the name.
	Probing State = "probing"
	// Live means Pier answers for the name.
	Live State = "live"
	// Conflict means another device on the network answers for the name.
	Conflict State = "conflict"
)

// Status describes one published name.
type Status struct {
	Name  string
	State State
	// Other is the address another host claimed, when State is Conflict.
	Other string
}

// Options configure a responder. The zero value serves the real mDNS port.
type Options struct {
	// Addr overrides the listen address. Tests bind 127.0.0.1:0.
	Addr string
	// ProbeInterval is the gap between the three probes; default 250ms.
	ProbeInterval time.Duration
	// Multicast disables group membership when false and Addr is set.
	Multicast bool
}

var group = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

type entry struct {
	state      State
	probes     int
	announces  int
	next       time.Time
	conflictIP string
}

type netIface struct {
	ifi   net.Interface
	addrs []*net.IPNet
	v6    []net.IP // only used to recognize our own IPv6 packets
}

// Responder publishes names over multicast DNS.
type Responder struct {
	conn net.PacketConn
	pc   *ipv4.PacketConn
	// conn6 and pc6 serve mDNS over IPv6 (ff02::fb). Some routers drop IPv4
	// multicast between Wi-Fi clients but pass IPv6, so phones only reach
	// Pier this way. They are nil when IPv6 is unavailable.
	conn6     net.PacketConn
	pc6       *ipv6.PacketConn
	multicast bool
	control   bool
	interval  time.Duration

	mu     sync.Mutex
	names  map[string]*entry
	ifaces map[int]netIface
	self   map[string]bool

	sendMu sync.Mutex

	// sendErr is the last failed send, so a blocked network is reported, not silent.
	errMu     sync.Mutex
	sendErr   error
	sendErrAt time.Time
}

// Listen opens the mDNS socket. It shares UDP 5353 with any system responder
// (mDNSResponder, avahi-daemon, systemd-resolved, the Windows DNS client).
func Listen(opts Options) (*Responder, error) {
	addr := opts.Addr
	multicast := opts.Multicast
	if addr == "" {
		addr = "0.0.0.0:5353"
		multicast = true
	}
	config := net.ListenConfig{Control: reuseControl}
	conn, err := config.ListenPacket(context.Background(), "udp4", addr)
	if err != nil {
		return nil, fmt.Errorf("Pier could not open the mDNS port: %w", err)
	}
	r := &Responder{
		conn:      conn,
		pc:        ipv4.NewPacketConn(conn),
		multicast: multicast,
		interval:  opts.ProbeInterval,
		names:     map[string]*entry{},
		ifaces:    map[int]netIface{},
		self:      map[string]bool{},
	}
	if r.interval <= 0 {
		r.interval = 250 * time.Millisecond
	}
	// Windows cannot report the arrival interface; answers then match by subnet.
	r.control = r.pc.SetControlMessage(ipv4.FlagInterface, true) == nil
	if multicast {
		_ = r.pc.SetMulticastLoopback(true)
		_ = r.pc.SetMulticastTTL(255)
		r.listen6(config)
	}
	r.refreshInterfaces()
	return r, nil
}

// listen6 opens [::]:5353 as well. Failing is not fatal: IPv4 still works.
func (r *Responder) listen6(config net.ListenConfig) {
	conn, err := config.ListenPacket(context.Background(), "udp6", "[::]:5353")
	if err != nil {
		return
	}
	r.conn6 = conn
	r.pc6 = ipv6.NewPacketConn(conn)
	_ = r.pc6.SetControlMessage(ipv6.FlagInterface, true)
	_ = r.pc6.SetMulticastLoopback(true)
	_ = r.pc6.SetMulticastHopLimit(255)
}

// LocalAddr is the bound socket address.
func (r *Responder) LocalAddr() net.Addr { return r.conn.LocalAddr() }

// SetNames replaces the published set. New names are probed before Pier
// answers for them; removed names get a goodbye so caches drop them at once.
func (r *Responder) SetNames(names []string) {
	want := map[string]bool{}
	for _, name := range names {
		want[canonical(name)] = true
	}
	r.mu.Lock()
	var removed []string
	for name, e := range r.names {
		if !want[name] {
			if e.state == Live {
				removed = append(removed, name)
			}
			delete(r.names, name)
		}
	}
	for name := range want {
		if _, ok := r.names[name]; !ok {
			r.names[name] = &entry{state: Probing, next: time.Now()}
		}
	}
	r.mu.Unlock()
	for _, name := range removed {
		r.announceAll(name, 0)
	}
}

// Statuses reports every published name, sorted.
func (r *Responder) Statuses() []Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Status, 0, len(r.names))
	for name, e := range r.names {
		out = append(out, Status{Name: strings.TrimSuffix(name, "."), State: e.state, Other: e.conflictIP})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Serve answers queries until ctx ends, then sends goodbyes and closes.
func (r *Responder) Serve(ctx context.Context) error {
	done := make(chan error, 1)
	go func() { done <- r.readLoop() }()
	done6 := make(chan struct{})
	if r.pc6 != nil {
		go func() { r.readLoop6(); close(done6) }()
	} else {
		close(done6)
	}

	tick := time.NewTicker(r.interval)
	defer tick.Stop()
	refresh := time.NewTicker(5 * time.Second)
	defer refresh.Stop()
	for {
		select {
		case <-ctx.Done():
			r.goodbyeAll()
			_ = r.conn.Close()
			if r.conn6 != nil {
				_ = r.conn6.Close()
			}
			<-done
			<-done6
			return nil
		case err := <-done:
			return err
		case <-refresh.C:
			r.refreshInterfaces()
		case now := <-tick.C:
			r.advance(now)
		}
	}
}

// advance sends due probes and announcements (RFC 6762 §8).
func (r *Responder) advance(now time.Time) {
	type send struct {
		name     string
		announce bool
	}
	var sends []send
	r.mu.Lock()
	for name, e := range r.names {
		if now.Before(e.next) {
			continue
		}
		switch {
		case e.state == Probing && e.probes < 3:
			e.probes++
			// Half an interval, so ticker jitter never skips a probe.
			e.next = now.Add(r.interval / 2)
			sends = append(sends, send{name: name})
		case e.state == Probing:
			e.state = Live
			e.announces = 1
			e.next = now.Add(time.Second)
			sends = append(sends, send{name: name, announce: true})
		case e.state == Live && e.announces < 2:
			e.announces++
			sends = append(sends, send{name: name, announce: true})
		case e.state == Conflict:
			// Try again: the other device may have left. If it is still
			// there, it answers the probe and the name returns to Conflict.
			e.state = Probing
			e.probes = 0
			e.conflictIP = ""
		}
	}
	r.mu.Unlock()
	for _, s := range sends {
		if s.announce {
			r.announceAll(s.name, hostTTL)
		} else {
			r.probeAll(s.name)
		}
	}
}

func (r *Responder) readLoop() error {
	buf := make([]byte, 9000)
	for {
		n, cm, src, err := r.pc.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			continue
		}
		udp, ok := src.(*net.UDPAddr)
		if !ok {
			continue
		}
		q, err := parse(buf[:n])
		if err != nil {
			continue
		}
		ifIndex := 0
		if cm != nil {
			ifIndex = cm.IfIndex
		}
		if q.response {
			r.observe(q, udp)
			continue
		}
		r.answer(q, udp, ifIndex, false)
	}
}

// readLoop6 answers queries that arrive over IPv6. The answer is still the
// IPv4 address: an mDNS record does not have to match the transport.
func (r *Responder) readLoop6() {
	buf := make([]byte, 9000)
	for {
		n, cm, src, err := r.pc6.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		udp, ok := src.(*net.UDPAddr)
		if !ok {
			continue
		}
		q, err := parse(buf[:n])
		if err != nil {
			continue
		}
		ifIndex := 0
		if cm != nil {
			ifIndex = cm.IfIndex
		}
		if ifIndex == 0 && udp.Zone != "" {
			// Windows has no control messages; a link-local source names its interface.
			if ifi, err := net.InterfaceByName(udp.Zone); err == nil {
				ifIndex = ifi.Index
			}
		}
		if q.response {
			r.observe(q, udp)
			continue
		}
		r.answer(q, udp, ifIndex, true)
	}
}

// observe marks a name as conflicting when another host claims it.
func (r *Responder) observe(q query, src *net.UDPAddr) {
	if r.isSelf(src.IP) {
		return
	}
	claimed := claims(q)
	if len(claimed) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for name, ip := range claimed {
		e, ok := r.names[name]
		if !ok || r.self[ip.String()] {
			continue
		}
		e.state = Conflict
		e.conflictIP = ip.String()
		e.next = time.Now().Add(conflictRetry)
	}
}

// conflictRetry is how long a name stays in Conflict before Pier probes again.
const conflictRetry = 30 * time.Second

func (r *Responder) answer(q query, src *net.UDPAddr, ifIndex int, v6 bool) {
	local := r.addressFor(src.IP, ifIndex)
	if local == nil {
		return
	}
	legacy := src.Port != 5353
	msg := reply(q, r.owns, local, legacy)
	if msg == nil {
		return
	}
	packet, err := msg.Pack()
	if err != nil {
		return
	}
	if legacy || wantsUnicast(q) || !r.multicast {
		conn := r.conn
		if v6 {
			conn = r.conn6
		}
		_, err := conn.WriteTo(packet, src)
		r.noteSend(err)
		return
	}
	// Reply on the transport the query used: that is the one the network delivers.
	ifi := r.interfaceFor(src.IP, ifIndex)
	if v6 {
		r.send6(packet, ifi)
		return
	}
	r.send4(packet, ifi)
}

func (r *Responder) owns(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.names[name]
	return ok && e.state == Live
}

// addressFor picks this machine's address on the querier's network:
// the interface the query arrived on, else the interface sharing its subnet,
// else whatever address the kernel would use to reach the querier.
func (r *Responder) addressFor(src net.IP, ifIndex int) net.IP {
	r.mu.Lock()
	iface, known := r.ifaces[ifIndex]
	if !known {
		iface, known = r.ifaceBySubnetLocked(src)
	}
	r.mu.Unlock()
	if known {
		for _, addr := range iface.addrs {
			if addr.Contains(src) {
				return addr.IP
			}
		}
		if len(iface.addrs) > 0 {
			return iface.addrs[0].IP
		}
	}
	return routeTo(src)
}

func (r *Responder) interfaceFor(src net.IP, ifIndex int) *net.Interface {
	r.mu.Lock()
	defer r.mu.Unlock()
	if iface, ok := r.ifaces[ifIndex]; ok {
		return &iface.ifi
	}
	if iface, ok := r.ifaceBySubnetLocked(src); ok {
		return &iface.ifi
	}
	return nil
}

func (r *Responder) ifaceBySubnetLocked(src net.IP) (netIface, bool) {
	for _, iface := range r.ifaces {
		for _, addr := range iface.addrs {
			if addr.Contains(src) {
				return iface, true
			}
		}
	}
	return netIface{}, false
}

func (r *Responder) isSelf(ip net.IP) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.self[ip.String()]
}

func routeTo(dst net.IP) net.IP {
	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: dst, Port: 5353})
	if err != nil {
		return nil
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.To4()
}

func (r *Responder) probeAll(name string) {
	for _, iface := range r.snapshot() {
		if len(iface.addrs) == 0 {
			continue
		}
		packet, err := probe(name, iface.addrs[0].IP)
		if err == nil {
			r.sendMulticast(packet, &iface.ifi)
		}
	}
}

func (r *Responder) announceAll(name string, ttl uint32) {
	for _, iface := range r.snapshot() {
		if len(iface.addrs) == 0 {
			continue
		}
		packet, err := announcement(name, iface.addrs[0].IP, ttl)
		if err == nil {
			r.sendMulticast(packet, &iface.ifi)
		}
	}
}

func (r *Responder) goodbyeAll() {
	r.mu.Lock()
	var live []string
	for name, e := range r.names {
		if e.state == Live {
			live = append(live, name)
		}
	}
	r.mu.Unlock()
	for _, name := range live {
		r.announceAll(name, 0)
	}
}

// sendMulticast sends to both mDNS groups, so a network that drops one
// family of multicast still carries the other.
func (r *Responder) sendMulticast(packet []byte, ifi *net.Interface) {
	r.send4(packet, ifi)
	r.send6(packet, ifi)
}

func (r *Responder) send4(packet []byte, ifi *net.Interface) {
	if !r.multicast {
		return
	}
	r.sendMu.Lock()
	defer r.sendMu.Unlock()
	if ifi != nil {
		if err := r.pc.SetMulticastInterface(ifi); err != nil {
			return
		}
	}
	_, err := r.pc.WriteTo(packet, nil, group)
	r.noteSend(err)
}

var group6 = &net.UDPAddr{IP: net.ParseIP("ff02::fb"), Port: 5353}

func (r *Responder) send6(packet []byte, ifi *net.Interface) {
	if !r.multicast || r.pc6 == nil || ifi == nil {
		return // link-local multicast needs an interface
	}
	r.sendMu.Lock()
	defer r.sendMu.Unlock()
	if err := r.pc6.SetMulticastInterface(ifi); err != nil {
		return
	}
	_, _ = r.pc6.WriteTo(packet, nil, group6) // IPv4 errors already report a blocked network
}

// noteSend remembers a failed send; a later success clears it.
func (r *Responder) noteSend(err error) {
	r.errMu.Lock()
	defer r.errMu.Unlock()
	if err == nil {
		r.sendErr = nil
		return
	}
	r.sendErr, r.sendErrAt = err, time.Now()
}

// SendError is the most recent send failure within the last minute, or nil.
// Every send failing (for example "no route to host") usually means the
// operating system is blocking Pier from the local network.
func (r *Responder) SendError() error {
	r.errMu.Lock()
	defer r.errMu.Unlock()
	if r.sendErr == nil || time.Since(r.sendErrAt) > time.Minute {
		return nil
	}
	return r.sendErr
}

func (r *Responder) snapshot() []netIface {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]netIface, 0, len(r.ifaces))
	for _, iface := range r.ifaces {
		out = append(out, iface)
	}
	return out
}

// refreshInterfaces joins the mDNS group on new LAN interfaces and forgets
// ones that went away, so a new Wi-Fi network is served without a restart.
func (r *Responder) refreshInterfaces() {
	found := lanInterfaces()
	self := map[string]bool{}
	for _, iface := range found {
		for _, addr := range iface.addrs {
			self[addr.IP.String()] = true
		}
		for _, ip := range iface.v6 {
			self[ip.String()] = true // our own IPv6 announcements are not conflicts
		}
	}
	r.mu.Lock()
	previous := r.ifaces
	r.mu.Unlock()
	if r.multicast {
		for index, iface := range found {
			if _, ok := previous[index]; !ok {
				ifi := iface.ifi
				_ = r.pc.JoinGroup(&ifi, &net.UDPAddr{IP: group.IP})
				if r.pc6 != nil {
					_ = r.pc6.JoinGroup(&ifi, &net.UDPAddr{IP: group6.IP})
				}
			}
		}
		for index, iface := range previous {
			if _, ok := found[index]; !ok {
				ifi := iface.ifi
				_ = r.pc.LeaveGroup(&ifi, &net.UDPAddr{IP: group.IP})
				if r.pc6 != nil {
					_ = r.pc6.LeaveGroup(&ifi, &net.UDPAddr{IP: group6.IP})
				}
			}
		}
	}
	r.mu.Lock()
	r.ifaces = found
	r.self = self
	r.mu.Unlock()
}

// virtualPrefixes are interfaces phones on the LAN cannot reach.
var virtualPrefixes = []string{
	"docker", "br-", "veth", "virbr", "vmnet", "vboxnet", "vethernet",
	"utun", "tun", "tap", "tailscale", "zt", "wg", "awdl", "llw", "anpi", "bridge",
}

func lanInterfaces() map[int]netIface {
	out := map[int]netIface{}
	ifis, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, ifi := range ifis {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		if isVirtual(ifi.Name) {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		var nets []*net.IPNet
		var v6 []net.IP
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			if ipNet.IP.To4() == nil {
				v6 = append(v6, ipNet.IP)
				continue
			}
			if usableIPv4(ipNet.IP) {
				nets = append(nets, &net.IPNet{IP: ipNet.IP.To4(), Mask: ipNet.Mask})
			}
		}
		if len(nets) > 0 {
			out[ifi.Index] = netIface{ifi: ifi, addrs: nets, v6: v6}
		}
	}
	return out
}

// LANAddress is the IPv4 address other devices on the LAN should use for this
// machine: the one on the default route when that is a LAN interface (not a
// VPN), else the first LAN address. It is nil when the machine is offline.
func LANAddress() net.IP {
	var lan []net.IP
	for _, iface := range lanInterfaces() {
		for _, addr := range iface.addrs {
			lan = append(lan, addr.IP)
		}
	}
	if len(lan) == 0 {
		return nil
	}
	if route := routeTo(net.IPv4(192, 0, 2, 1)); route != nil { // no packet is sent
		for _, ip := range lan {
			if ip.Equal(route) {
				return ip
			}
		}
	}
	sort.Slice(lan, func(i, j int) bool { return lan[i].String() < lan[j].String() })
	return lan[0]
}

func isVirtual(name string) bool {
	lower := strings.ToLower(name)
	for _, prefix := range virtualPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

func usableIPv4(ip net.IP) bool {
	return !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !cgnat.Contains(ip)
}
