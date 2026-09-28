package proxysvc

import (
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Where and how a stream listens beyond one port on every address: a range
// of ports forwarded as one, each to the same port on the backend, and a
// listener behind a load balancer that sends the PROXY header itself.

// maxStreamRange bounds a port range. Every port is a socket the port check
// looks up and nginx binds; a block of game ports is tens, not thousands.
const maxStreamRange = 100

// maxTrustedProxies bounds the load balancers a stream trusts to what a
// form row per entry can show.
const maxTrustedProxies = 32

var (
	// ErrNoStreamRealIP refuses accepting the PROXY header when nginx was
	// built without stream_realip_module: without set_real_ip_from the
	// header is read and the client's address in it used for nothing.
	ErrNoStreamRealIP = errors.New("this nginx was built without stream_realip_module, so a stream cannot take the client's address from a PROXY header")
	// ErrStreamAddressNotLocal refuses a listening address this host does
	// not have. nginx -t passes it and the reload then fails to bind it,
	// which fails every reload on the host until it is fixed.
	ErrStreamAddressNotLocal = errors.New("not an address of this host")
)

// validStreamListen checks the range and the PROXY listener, and drops what
// an option that is off would carry, so a file reads back one way.
func validStreamListen(spec *StreamSpec) error {
	if spec.ListenEnd == spec.Listen {
		spec.ListenEnd = 0
	}
	if spec.ListenEnd != 0 {
		if spec.ListenEnd < spec.Listen || spec.ListenEnd > 65535 {
			return fmt.Errorf("the port range must end on a port above %d, and at most 65535", spec.Listen)
		}
		if spec.ListenEnd-spec.Listen+1 > maxStreamRange {
			return fmt.Errorf("a port range takes at most %d ports", maxStreamRange)
		}
		// The total cap's zone is keyed on $server_port, which differs on
		// every port of a range: each port would get a cap of its own.
		if spec.MaxConnTotal > 0 {
			return fmt.Errorf("a cap on connections in total counts each port of a range on its own — cap connections per client instead")
		}
	}
	if spec.SamePort && spec.ListenEnd == 0 {
		return fmt.Errorf("the same port on the backend is for a port range — with one port, write the backend's port")
	}
	if !spec.AcceptProxy {
		spec.TrustedProxies = nil
		return nil
	}
	if spec.Protocol != "tcp" {
		return fmt.Errorf("the PROXY header is accepted on TCP only")
	}
	if len(spec.TrustedProxies) == 0 {
		return fmt.Errorf("accepting the PROXY header needs the load balancers to trust, or no client's address is taken from it")
	}
	if len(spec.TrustedProxies) > maxTrustedProxies {
		return fmt.Errorf("a stream trusts at most %d load balancers", maxTrustedProxies)
	}
	for i := range spec.TrustedProxies {
		entry := strings.TrimSpace(spec.TrustedProxies[i])
		if entry == "all" || entry == "0.0.0.0/0" || entry == "::/0" {
			return fmt.Errorf("trusting every peer lets any client claim any address — name the load balancers")
		}
		if err := validACLEntry(entry); err != nil {
			return err
		}
		spec.TrustedProxies[i] = entry
	}
	return nil
}

// validStreamSamePort checks the backend of a stream forwarding each port to
// the same port: one server, written as its IP address alone.
func validStreamSamePort(spec *StreamSpec) error {
	if len(spec.Servers) > 1 {
		return fmt.Errorf("the same port on the backend takes one server — an upstream pool cannot carry the port")
	}
	if len(spec.Servers) == 1 {
		spec.Upstream = spec.Servers[0].Address
	}
	spec.Servers, spec.Balance, spec.NoRetry = nil, "", false
	ip := net.ParseIP(strings.Trim(strings.TrimSpace(spec.Upstream), "[]"))
	if ip == nil {
		return fmt.Errorf("with the same port on the backend, the upstream is the backend's IP address alone, like 10.0.0.5 — nginx cannot look a name up there without a resolver")
	}
	spec.Upstream = ip.String()
	if len(spec.Routes) > 0 {
		return fmt.Errorf("routing by TLS name sends each name to a backend port of its own — turn off the same port on the backend")
	}
	return nil
}

// streamPorts is the port part of a listen line: 5432, or 27015-27030.
func streamPorts(spec *StreamSpec) string {
	if spec.ListenEnd > spec.Listen {
		return fmt.Sprintf("%d-%d", spec.Listen, spec.ListenEnd)
	}
	return strconv.Itoa(spec.Listen)
}

// streamLastPort is the last port a spec listens on.
func streamLastPort(spec *StreamSpec) int {
	return max(spec.Listen, spec.ListenEnd)
}

// samePortTarget is the proxy_pass of a stream forwarding each port to the
// same port on the backend.
func samePortTarget(upstream string) string {
	if strings.Contains(upstream, ":") {
		return "[" + upstream + "]:$server_port"
	}
	return upstream + ":$server_port"
}

// readSamePort reads a proxy_pass to the port the client came in on, as
// samePortTarget writes it.
func readSamePort(target string) (string, bool) {
	host, ok := strings.CutSuffix(target, ":$server_port")
	if !ok {
		return "", false
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil || strings.Contains(host, ":") != strings.HasPrefix(host, "[") {
		return "", false
	}
	return ip.String(), true
}

// readListenPorts reads a listen line's port or range.
func readListenPorts(port string) (lo, hi int, ok bool) {
	from, to, isRange := strings.Cut(port, "-")
	lo, err := strconv.Atoi(from)
	if err != nil || lo < 1 || lo > 65535 {
		return 0, 0, false
	}
	if !isRange {
		return lo, lo, true
	}
	hi, err = strconv.Atoi(to)
	if err != nil || hi < lo || hi > 65535 {
		return 0, 0, false
	}
	return lo, hi, true
}

// listenBinds are the sockets of a listen line in nginx's configuration:
// listenBind's one, or one per port of a stream's port range, which a port
// check has to see as it sees any other stream's port.
func listenBinds(args []string, http bool) []bind {
	if b, ok := listenBind(args, http); ok {
		return []bind{b}
	}
	if http || len(args) == 0 {
		return nil
	}
	i := strings.LastIndex(args[0], ":")
	lo, hi, ok := readListenPorts(args[0][i+1:])
	if !ok || hi == lo {
		return nil
	}
	hi = min(hi, lo+maxStreamRange-1)
	var out []bind
	one := slices.Clone(args)
	for n := lo; n <= hi; n++ {
		one[0] = args[0][:i+1] + strconv.Itoa(n)
		if b, ok := listenBind(one, false); ok {
			out = append(out, b)
		}
	}
	return out
}

// checkListenOptions names the listen options the form cannot write back as
// they are: proxy_protocol on some listens only, the header taken with no
// balancer trusted or the other way round, and a range beside what a range
// cannot carry.
func (p *parsedStream) checkListenOptions() {
	switch {
	case p.proxyListens > 0 && p.directListens > 0:
		p.cannot("proxy_protocol on some listens only")
	case p.proxyListens > 0:
		p.spec.AcceptProxy = true
	}
	if p.spec.AcceptProxy && len(p.spec.TrustedProxies) == 0 {
		p.cannot("a proxy_protocol listen with no set_real_ip_from")
	}
	if !p.spec.AcceptProxy && len(p.spec.TrustedProxies) > 0 {
		p.cannot("set_real_ip_from with no proxy_protocol listen")
	}
	if p.spec.AcceptProxy && p.spec.Protocol != "tcp" {
		p.cannot("proxy_protocol on UDP")
	}
	if p.spec.SamePort && p.spec.ListenEnd == 0 {
		p.cannot("the backend's port taken from a single listen port")
	}
	if p.spec.ListenEnd > 0 && p.spec.MaxConnTotal > 0 {
		p.cannot("a total connection cap across a port range")
	}
}

// readTrustedProxy takes one set_real_ip_from line in the shape
// validStreamListen accepts.
func (p *parsedStream) readTrustedProxy(args []string) {
	if len(args) != 1 || args[0] == "all" || validACLEntry(args[0]) != nil {
		p.cannot("set_real_ip_from " + strings.Join(args, " "))
		return
	}
	p.spec.TrustedProxies = append(p.spec.TrustedProxies, args[0])
}

// StreamAddress is one address of this host a stream can listen on.
type StreamAddress struct {
	Address string `json:"address"`
	// Interface is the network interface holding it — lo, eth0,
	// tailscale0 — which is how an operator tells the addresses apart.
	Interface string `json:"interface"`
}

// streamHostAddresses are the addresses of this host's interfaces that are
// up. The backend shares the host's network namespace, so these are the
// host's own. A link-local IPv6 address is left out: nginx needs its zone to
// bind it, and a listen line has no way to name one.
func streamHostAddresses() ([]StreamAddress, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	out := []StreamAddress{}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.IsLinkLocalUnicast() {
				continue
			}
			out = append(out, StreamAddress{Address: ipn.IP.String(), Interface: ifc.Name})
		}
	}
	// Loopback first, then IPv4 before IPv6: the order a picker offers them.
	sort.SliceStable(out, func(i, j int) bool {
		a, b := net.ParseIP(out[i].Address), net.ParseIP(out[j].Address)
		if a.IsLoopback() != b.IsLoopback() {
			return a.IsLoopback()
		}
		return (a.To4() != nil) && (b.To4() == nil)
	})
	return out, nil
}

// nonlocalBind is whether the kernel lets a program bind an address the host
// does not have, for the address's family — a setting a floating-IP or
// keepalived host turns on so nginx can listen before the address arrives.
func nonlocalBind(ip net.IP) bool {
	path := "/proc/sys/net/ipv4/ip_nonlocal_bind"
	if ip.To4() == nil {
		path = "/proc/sys/net/ipv6/ip_nonlocal_bind"
	}
	b, err := os.ReadFile(path)
	return err == nil && strings.TrimSpace(string(b)) == "1"
}

// StreamAddressError is why a spec's listening address cannot be bound on
// this host, or nil. An address the interfaces could not be read to check is
// left to nginx.
func StreamAddressError(spec *StreamSpec) error {
	ip := net.ParseIP(spec.Address)
	if spec.Address == "" || ip == nil || ip.IsUnspecified() || nonlocalBind(ip) {
		return nil
	}
	addrs, err := streamHostAddresses()
	if err != nil {
		return nil
	}
	have := make([]string, 0, len(addrs))
	for _, a := range addrs {
		if net.ParseIP(a.Address).Equal(ip) {
			return nil
		}
		have = append(have, a.Address)
	}
	return fmt.Errorf("%w: %s is on none of its interfaces, so nginx could not listen on it (it has %s)",
		ErrStreamAddressNotLocal, spec.Address, strings.Join(have, ", "))
}
