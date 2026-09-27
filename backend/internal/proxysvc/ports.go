package proxysvc

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/shirou/gopsutil/v4/process"
)

// Listener is one socket the host is accepting on, joined to the process that
// owns it. "What is listening on 8080 and who started it" is the question this
// answers, and it is the first question during an incident.
type Listener struct {
	Protocol string `json:"protocol"`
	// Family is the socket's, ipv4 or ipv6: the kernel table it is in. A
	// service on 0.0.0.0 and :: is two sockets, one of each, and the page
	// counts it once.
	Family  string `json:"family"`
	Address string `json:"address"`
	Port    uint32 `json:"port"`
	PID     int32  `json:"pid"`
	Process string `json:"process"`
	Cmdline string `json:"cmdline,omitempty"`
	User    string `json:"user,omitempty"`
	// Scope is how far the bind reaches, as far as its address can say.
	Scope BindScope `json:"scope"`
	// Exposed is every scope but loopback. A socket on one tailnet or public
	// address is reachable from off the machine as surely as one on 0.0.0.0;
	// only who can reach it differs, and that is Reach's to say.
	Exposed bool `json:"exposed"`
	// Reach is netsec's grade of the address — loopback, host (a bridge),
	// network (a tailnet, VPN or private address), public or all — which
	// needs the host's interfaces and routes. GET /ports fills it; the
	// listing itself leaves it empty.
	Reach string `json:"reach,omitempty"`
	// Network and Interface name what Reach is made of — the tailnet on
	// tailscale0, Docker's bridge docker0, a public address on ens3 — so a
	// socket can say where it answers rather than only how far. GET /ports
	// fills them beside Reach; Interface stays empty for a wildcard or
	// loopback bind and an address on no interface the host listed.
	Network   string `json:"network,omitempty"`
	Interface string `json:"interface,omitempty"`
}

// BindScope is where a socket can be reached from, judged from the address it
// is bound to.
type BindScope string

const (
	// ScopeLoopback is 127.0.0.0/8 or ::1: this machine only.
	ScopeLoopback BindScope = "loopback"
	// ScopeInterface is one specific address — a tailnet, a LAN, a Docker
	// bridge or a public IP. Whoever can route to that address can connect.
	ScopeInterface BindScope = "interface"
	// ScopeAll is 0.0.0.0 or ::, every interface the host has or will have.
	ScopeAll BindScope = "all"
)

func bindScope(address string) BindScope {
	if isWildcard(address) {
		return ScopeAll
	}
	ip := net.ParseIP(address)
	switch {
	case ip != nil && ip.IsUnspecified():
		return ScopeAll
	case ip != nil && ip.IsLoopback():
		return ScopeLoopback
	}
	return ScopeInterface
}

// ListListeners enumerates the host's listening sockets from the kernel's own
// socket tables, each joined to the process serving it.
//
// The walk over every process's descriptors is what takes time on a busy
// host, and it stops when ctx does.
func ListListeners(ctx context.Context) ([]Listener, error) {
	root := procRoot()
	sockets, err := readSockets(root)
	if err != nil {
		return nil, err
	}
	wanted := map[uint64]bool{}
	for _, s := range sockets {
		if s.listening() {
			wanted[s.inode] = true
		}
	}
	holders, err := socketHolders(ctx, root, wanted)
	if err != nil {
		return nil, err
	}
	out := listenersFrom(sockets, holders, parentsOf(root, holders))
	cache := map[int32]*process.Process{}
	for i := range out {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		l := &out[i]
		if l.PID <= 0 {
			continue
		}
		p, ok := cache[l.PID]
		if !ok {
			// A process that exited since the walk is cached as nil, so its
			// other sockets do not ask again.
			if np, err := process.NewProcessWithContext(ctx, l.PID); err == nil {
				p = np
			}
			cache[l.PID] = p
		}
		if p != nil {
			l.Process, _ = p.NameWithContext(ctx)
			l.Cmdline, _ = p.CmdlineWithContext(ctx)
			l.User, _ = p.UsernameWithContext(ctx)
		}
	}
	return out, nil
}

// listenersFrom keeps the sockets that are accepting and names each one's
// owner. A TCP socket is listening in the LISTEN state. UDP has no such
// state, so a bound socket is the closest thing to a listener there is — but
// a *connected* one is not: every DNS lookup on the host opens one, and
// listing those turned a page about what the server is accepting on into a
// page of the machine's own outbound traffic.
//
// Sockets sharing a protocol, family, address and port are one row: with
// SO_REUSEPORT each worker holds its own socket on the same endpoint.
func listenersFrom(sockets []socketRow, holders map[uint64][]int32, parents map[int32]int32) []Listener {
	type endpoint struct {
		proto, family, address string
		port                   uint32
	}
	order := []endpoint{}
	held := map[endpoint][]int32{}
	for _, s := range sockets {
		if !s.listening() {
			continue
		}
		key := endpoint{s.proto, s.family, s.address, s.port}
		if _, ok := held[key]; !ok {
			order = append(order, key)
			held[key] = []int32{}
		}
		held[key] = append(held[key], holders[s.inode]...)
	}
	out := make([]Listener, 0, len(order))
	for _, key := range order {
		scope := bindScope(key.address)
		out = append(out, Listener{
			Protocol: key.proto,
			Family:   key.family,
			Address:  key.address,
			Port:     key.port,
			PID:      ownerOf(held[key], parents),
			Scope:    scope,
			Exposed:  scope != ScopeLoopback,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Port != out[j].Port {
			return out[i].Port < out[j].Port
		}
		if out[i].Protocol != out[j].Protocol {
			return out[i].Protocol < out[j].Protocol
		}
		return out[i].Address < out[j].Address
	})
	return out
}

// socketRow is one line of /proc/net/{tcp,tcp6,udp,udp6}.
type socketRow struct {
	proto string
	// family is the table's: ipv4 for tcp and udp, ipv6 for tcp6 and udp6,
	// whose v4-mapped addresses read as IPv4 but belong to an IPv6 socket.
	family     string
	address    string
	port       uint32
	remotePort uint32
	// state is the kernel's hex code; 0A is TCP's LISTEN.
	state string
	inode uint64
}

// listening reports a socket that accepts. Port 0 is a socket that was
// created and never bound; it listens on nothing.
func (s socketRow) listening() bool {
	if s.port == 0 {
		return false
	}
	if s.proto == "tcp" {
		return s.state == "0A"
	}
	return s.remotePort == 0
}

// procRoot is the kernel's process table: /proc, or HOST_PROC where a
// container mounts the host's elsewhere — the variable gopsutil reads for
// the process details, so the sockets and their owners come from one host.
func procRoot() string {
	if root := os.Getenv("HOST_PROC"); root != "" {
		return root
	}
	return "/proc"
}

// readSockets reads the four inet tables. Their net/ is the reader's own
// network namespace, which in production is the host's (network_mode: host).
// A kernel without IPv6 has no tcp6 or udp6, which is not an error.
func readSockets(root string) ([]socketRow, error) {
	out := []socketRow{}
	for _, table := range []struct {
		file, proto, family string
	}{{"tcp", "tcp", "ipv4"}, {"tcp6", "tcp", "ipv6"}, {"udp", "udp", "ipv4"}, {"udp6", "udp", "ipv6"}} {
		content, err := os.ReadFile(filepath.Join(root, "net", table.file))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) && strings.HasSuffix(table.file, "6") {
				continue
			}
			return nil, err
		}
		rows := parseSocketTable(content, table.proto)
		for i := range rows {
			rows[i].family = table.family
		}
		out = append(out, rows...)
	}
	return out, nil
}

// parseSocketTable reads one /proc/net table. A line that does not parse is
// skipped rather than failing the listing, as gopsutil did before it.
func parseSocketTable(content []byte, proto string) []socketRow {
	out := []socketRow{}
	lines := strings.Split(string(content), "\n")
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		address, port, err := decodeSocketAddress(fields[1])
		if err != nil {
			continue
		}
		_, remotePort, err := decodeSocketAddress(fields[2])
		if err != nil {
			continue
		}
		inode, err := strconv.ParseUint(fields[9], 10, 64)
		if err != nil {
			continue
		}
		out = append(out, socketRow{
			proto: proto, address: address, port: port, remotePort: remotePort,
			state: fields[3], inode: inode,
		})
	}
	return out
}

// decodeSocketAddress reads "0100007F:0016": the address in hex with each
// 32-bit word in the host's byte order, which on every architecture the
// dashboard ships for is little-endian, and the port in hex.
func decodeSocketAddress(field string) (string, uint32, error) {
	hexAddress, hexPort, ok := strings.Cut(field, ":")
	if !ok {
		return "", 0, fmt.Errorf("no port in %q", field)
	}
	port, err := strconv.ParseUint(hexPort, 16, 16)
	if err != nil {
		return "", 0, fmt.Errorf("port in %q: %w", field, err)
	}
	raw, err := hex.DecodeString(hexAddress)
	if err != nil || (len(raw) != net.IPv4len && len(raw) != net.IPv6len) {
		return "", 0, fmt.Errorf("address in %q", field)
	}
	for word := 0; word < len(raw); word += 4 {
		raw[word], raw[word+1], raw[word+2], raw[word+3] = raw[word+3], raw[word+2], raw[word+1], raw[word]
	}
	return net.IP(raw).String(), uint32(port), nil
}

func isWildcard(ip string) bool {
	return ip == "0.0.0.0" || ip == "::" || ip == "*" || ip == ""
}
