package netx

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"runtime"
	"sort"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
	"golang.org/x/sys/unix"
)

// A probe is sent from this process, on a socket that carries the member's
// packet mark, is bound to the member's device and, where known, to its
// source address. The mark sends it through the member's own table whatever
// the group table says; the device binding makes a missing member route fail
// rather than leak through another path. Nothing here runs a shell or a
// helper: the backend shares the host's network namespace, and a simulation
// opens its sockets inside a disposable namespace of its own.

// EgressProbeResult is one probe of one member.
type EgressProbeResult struct {
	Kind      string  `json:"kind"`
	Target    string  `json:"target"`
	OK        bool    `json:"ok"`
	RTTMillis float64 `json:"rttMillis,omitempty"`
	Error     string  `json:"error,omitempty"`
}

// egressPath is where a probe is bound.
type egressPath struct {
	Mark   uint32
	Source netip.Addr
	Device string
	// Netns is a network namespace file to open the socket in; empty is this
	// process's own.
	Netns string
}

// egressProbe is a variable so recorded tests answer without a network.
var egressProbe = runEgressProbe

func runEgressProbe(ctx context.Context, path egressPath, p EgressProbe, timeout time.Duration) EgressProbeResult {
	res := EgressProbeResult{Kind: p.Kind, Target: p.Target}
	target, err := netip.ParseAddr(p.Target)
	if err != nil {
		res.Error = "the target is not an address"
		return res
	}
	var rtt time.Duration
	err = inNetns(path.Netns, func() error {
		var err error
		switch p.Kind {
		case "icmp":
			rtt, err = probeICMP(ctx, path, target, timeout)
		case "tcp":
			rtt, err = probeTCP(ctx, path, target, p.Port, timeout)
		case "dns":
			rtt, err = probeDNS(ctx, path, target, p.Port, p.Name, timeout)
		default:
			err = fmt.Errorf("unknown probe %s", p.Kind)
		}
		return err
	})
	if err != nil {
		res.Error = probeError(err)
		return res
	}
	res.OK = true
	res.RTTMillis = float64(rtt.Microseconds()) / 1000
	return res
}

// probeError keeps a failure to the words that explain it.
func probeError(err error) string {
	var errno syscall.Errno
	switch {
	case errors.Is(err, os.ErrDeadlineExceeded), errors.Is(err, context.DeadlineExceeded):
		return "no answer before the timeout"
	case errors.As(err, &errno):
		return errno.Error()
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Err != nil {
		return op.Err.Error()
	}
	return err.Error()
}

// inNetns runs fn with this goroutine's thread inside a network namespace.
// A thread whose namespace cannot be restored is never unlocked, so the
// runtime retires it rather than handing it to other goroutines.
func inNetns(path string, fn func() error) error {
	if path == "" {
		return fn()
	}
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		original, err := os.Open("/proc/thread-self/ns/net")
		if err != nil {
			runtime.UnlockOSThread()
			done <- err
			return
		}
		defer original.Close()
		target, err := os.Open(path)
		if err != nil {
			runtime.UnlockOSThread()
			done <- fmt.Errorf("opening the namespace: %w", err)
			return
		}
		defer target.Close()
		if err := unix.Setns(int(target.Fd()), unix.CLONE_NEWNET); err != nil {
			runtime.UnlockOSThread()
			done <- fmt.Errorf("entering the namespace: %w", err)
			return
		}
		result := fn()
		if err := unix.Setns(int(original.Fd()), unix.CLONE_NEWNET); err != nil {
			done <- errors.Join(result, fmt.Errorf("leaving the namespace: %w", err))
			return
		}
		runtime.UnlockOSThread()
		done <- result
	}()
	return <-done
}

// egressSocketControl binds a socket to the probe's path before it sends.
func egressSocketControl(path egressPath) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, c syscall.RawConn) error {
		var serr error
		err := c.Control(func(fd uintptr) { serr = bindEgressSocket(int(fd), path) })
		if err != nil {
			return err
		}
		return serr
	}
}

func bindEgressSocket(fd int, path egressPath) error {
	if path.Mark != 0 {
		if err := unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_MARK, int(path.Mark)); err != nil {
			return fmt.Errorf("marking the probe socket: %w", err)
		}
	}
	if path.Device != "" {
		if err := unix.BindToDevice(fd, path.Device); err != nil {
			return fmt.Errorf("binding the probe to %s: %w", path.Device, err)
		}
	}
	return nil
}

func probeTCP(ctx context.Context, path egressPath, target netip.Addr, port int, timeout time.Duration) (time.Duration, error) {
	d := net.Dialer{Timeout: timeout, Control: egressSocketControl(path)}
	if path.Source.IsValid() {
		d.LocalAddr = &net.TCPAddr{IP: path.Source.AsSlice()}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", netip.AddrPortFrom(target, uint16(port)).String())
	if err != nil {
		return 0, err
	}
	rtt := time.Since(start)
	conn.Close()
	return rtt, nil
}

func probeDNS(ctx context.Context, path egressPath, target netip.Addr, port int, name string, timeout time.Duration) (time.Duration, error) {
	d := net.Dialer{Timeout: timeout, Control: egressSocketControl(path)}
	if path.Source.IsValid() {
		d.LocalAddr = &net.UDPAddr{IP: path.Source.AsSlice()}
	}
	qname, err := dnsmessage.NewName(dnsFQDN(name))
	if err != nil {
		return 0, err
	}
	question := dnsmessage.Question{Name: qname, Type: dnsmessage.TypeNS, Class: dnsmessage.ClassINET}
	if name != "." {
		question.Type = dnsmessage.TypeA
		if target.Is6() {
			question.Type = dnsmessage.TypeAAAA
		}
	}
	var idb [2]byte
	_, _ = rand.Read(idb[:])
	id := binary.BigEndian.Uint16(idb[:])
	packet, err := (&dnsmessage.Message{Header: dnsmessage.Header{ID: id, RecursionDesired: true}, Questions: []dnsmessage.Question{question}}).Pack()
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := d.DialContext(ctx, "udp", netip.AddrPortFrom(target, uint16(port)).String())
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)
	start := time.Now()
	if _, err := conn.Write(packet); err != nil {
		return 0, err
	}
	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return 0, err
		}
		var parser dnsmessage.Parser
		header, err := parser.Start(buf[:n])
		if err != nil || header.ID != id || !header.Response {
			continue
		}
		q, err := parser.Question()
		if err != nil || q.Type != question.Type || !dnsNamesEqual(q.Name, question.Name) {
			continue
		}
		// Any answer of the resolver proves the path carried the question
		// and its reply; a refusal does too, so the code is not judged.
		return time.Since(start), nil
	}
}

func dnsFQDN(name string) string {
	if name == "." || name == "" {
		return "."
	}
	return name + "."
}

func dnsNamesEqual(a, b dnsmessage.Name) bool {
	return dnsNameKey(a) == dnsNameKey(b)
}

// probeICMP sends one echo request on a raw socket and waits for its reply,
// matched by identifier, sequence and source.
func probeICMP(ctx context.Context, path egressPath, target netip.Addr, timeout time.Duration) (time.Duration, error) {
	domain, proto := unix.AF_INET, unix.IPPROTO_ICMP
	var echoType, replyType icmp.Type = ipv4.ICMPTypeEcho, ipv4.ICMPTypeEchoReply
	if target.Is6() {
		domain, proto = unix.AF_INET6, unix.IPPROTO_ICMPV6
		echoType, replyType = ipv6.ICMPTypeEchoRequest, ipv6.ICMPTypeEchoReply
	}
	fd, err := unix.Socket(domain, unix.SOCK_RAW|unix.SOCK_CLOEXEC, proto)
	if err != nil {
		return 0, fmt.Errorf("opening an ICMP socket: %w", err)
	}
	defer unix.Close(fd)
	if err := bindEgressSocket(fd, path); err != nil {
		return 0, err
	}
	if target.Is6() {
		// Only echo replies are wanted on this socket.
		var filter icmpv6Filter
		filter.setAll(true)
		filter.pass(uint8(ipv6.ICMPTypeEchoReply))
		if err := unix.SetsockoptString(fd, unix.IPPROTO_ICMPV6, unix.ICMPV6_FILTER, string(filter[:])); err != nil {
			return 0, err
		}
	}
	if path.Source.IsValid() {
		if err := unix.Bind(fd, sockaddr(path.Source)); err != nil {
			return 0, fmt.Errorf("binding the probe to %s: %w", path.Source, err)
		}
	}
	var idb [4]byte
	_, _ = rand.Read(idb[:])
	id, seq := int(binary.BigEndian.Uint16(idb[:2])), int(binary.BigEndian.Uint16(idb[2:]))
	payload := []byte("just-dashboard egress probe")
	msg, err := (&icmp.Message{Type: echoType, Body: &icmp.Echo{ID: id, Seq: seq, Data: payload}}).Marshal(nil)
	if err != nil {
		return 0, err
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < timeout {
		timeout = time.Until(deadline)
	}
	end := time.Now().Add(timeout)
	start := time.Now()
	if err := unix.Sendto(fd, msg, 0, sockaddr(target)); err != nil {
		return 0, err
	}
	buf := make([]byte, 1500)
	for {
		left := time.Until(end)
		if left <= 0 {
			return 0, os.ErrDeadlineExceeded
		}
		tv := unix.NsecToTimeval(left.Nanoseconds())
		_ = unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv)
		n, from, err := unix.Recvfrom(fd, buf, 0)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			if errors.Is(err, unix.EAGAIN) {
				return 0, os.ErrDeadlineExceeded
			}
			return 0, err
		}
		if peer, ok := sockaddrAddr(from); !ok || peer != target {
			continue
		}
		data := buf[:n]
		if !target.Is6() {
			if len(data) < 20 {
				continue
			}
			ihl := int(data[0]&0x0f) * 4
			if ihl < 20 || ihl > len(data) {
				continue
			}
			data = data[ihl:]
		}
		reply, err := icmp.ParseMessage(proto, data)
		if err != nil || reply.Type != replyType {
			continue
		}
		echo, ok := reply.Body.(*icmp.Echo)
		if !ok || echo.ID != id || echo.Seq != seq {
			continue
		}
		return time.Since(start), nil
	}
}

// icmpv6Filter is struct icmp6_filter: a set bit blocks that type.
type icmpv6Filter [32]byte

func (f *icmpv6Filter) setAll(block bool) {
	for i := range f {
		if block {
			f[i] = 0xff
		} else {
			f[i] = 0
		}
	}
}

func (f *icmpv6Filter) pass(typ uint8) { f[typ>>3] &^= 1 << (typ & 7) }

func sockaddr(a netip.Addr) unix.Sockaddr {
	if a.Is4() {
		return &unix.SockaddrInet4{Addr: a.As4()}
	}
	return &unix.SockaddrInet6{Addr: a.As16()}
}

func sockaddrAddr(sa unix.Sockaddr) (netip.Addr, bool) {
	switch v := sa.(type) {
	case *unix.SockaddrInet4:
		return netip.AddrFrom4(v.Addr), true
	case *unix.SockaddrInet6:
		return netip.AddrFrom16(v.Addr).Unmap(), true
	}
	return netip.Addr{}, false
}

// EgressSample is one round of every probe through one member.
type EgressSample struct {
	At     time.Time           `json:"at"`
	Probes []EgressProbeResult `json:"probes"`
	// OK and Total count this round's probes; Loss is the percentage lost
	// over the threshold window ending here.
	OK    int     `json:"ok"`
	Total int     `json:"total"`
	Loss  float64 `json:"loss"`
	// LatencyMillis is the median round trip of the answered probes.
	LatencyMillis float64 `json:"latencyMillis,omitempty"`
	// Good is this sample judged against the thresholds; Why says why not.
	Good bool   `json:"good"`
	Why  string `json:"why,omitempty"`
}

// sampleMember runs every probe of a group through one member at once.
func sampleMember(ctx context.Context, path egressPath, probes []EgressProbe, timeout time.Duration, at time.Time) EgressSample {
	s := EgressSample{At: at, Total: len(probes), Probes: make([]EgressProbeResult, len(probes))}
	type indexed struct {
		i int
		r EgressProbeResult
	}
	results := make(chan indexed, len(probes))
	for i, p := range probes {
		go func(i int, p EgressProbe) {
			results <- indexed{i, egressProbe(ctx, path, p, timeout)}
		}(i, p)
	}
	for range probes {
		r := <-results
		s.Probes[r.i] = r.r
	}
	var rtts []float64
	for _, r := range s.Probes {
		if r.OK {
			s.OK++
			rtts = append(rtts, r.RTTMillis)
		}
	}
	if len(rtts) > 0 {
		sort.Float64s(rtts)
		s.LatencyMillis = rtts[len(rtts)/2]
		if len(rtts)%2 == 0 {
			s.LatencyMillis = (rtts[len(rtts)/2-1] + rtts[len(rtts)/2]) / 2
		}
	}
	return s
}

// judgeSample scores a sample against the thresholds and the loss over the
// window of samples before it (oldest first).
func judgeSample(s *EgressSample, window []EgressSample, t EgressThresholds) {
	ok, total := s.OK, s.Total
	start := len(window) - (t.Window - 1)
	if start < 0 {
		start = 0
	}
	for _, w := range window[start:] {
		ok += w.OK
		total += w.Total
	}
	if total > 0 {
		s.Loss = float64(total-ok) * 100 / float64(total)
	}
	s.Good = true
	switch {
	case s.OK == 0:
		s.Good, s.Why = false, "no probe answered"
	case s.Loss > float64(t.LossPercent):
		s.Good, s.Why = false, "loss "+strconv.FormatFloat(s.Loss, 'f', 0, 64)+"% over the window is above "+strconv.Itoa(t.LossPercent)+"%"
	case s.LatencyMillis > float64(t.LatencyMillis):
		s.Good, s.Why = false, "median round trip "+strconv.FormatFloat(s.LatencyMillis, 'f', 0, 64)+" ms is above "+strconv.Itoa(t.LatencyMillis)+" ms"
	}
}
