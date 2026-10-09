package netsec

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	gnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

// Who is talking to this machine right now.
//
// The Ports view answers what is listening; this answers who took it up on the
// offer, which is the question during an incident and the one no panel in this
// class shows. It is deliberately a *summary* by remote address rather than a
// connection list: a busy host holds thousands of sockets and forty of them
// are one client, so a raw table buries the single address with two hundred
// connections underneath four hundred rows of noise.
type Connection struct {
	Protocol   string `json:"protocol"`
	LocalAddr  string `json:"localAddress"`
	LocalPort  uint32 `json:"localPort"`
	RemoteAddr string `json:"remoteAddress"`
	RemotePort uint32 `json:"remotePort"`
	Status     string `json:"status"`
	PID        int32  `json:"pid,omitempty"`
	Process    string `json:"process,omitempty"`
	User       string `json:"user,omitempty"`
	// FirstSeen is the first read of this table that held the tuple; the
	// connection is at least as old as that, and may be older.
	FirstSeen time.Time `json:"firstSeen,omitzero"`
}

// Peer is one remote address, with everything it is connected to.
type Peer struct {
	Address string `json:"address"`
	// Count is how many sockets this address holds; Established is how many
	// of them are carrying traffic rather than closing.
	Count       int      `json:"count"`
	Established int      `json:"established"`
	Ports       []uint32 `json:"ports"`
	Processes   []string `json:"processes"`
	// Private marks a peer inside RFC1918, a tailnet or loopback. Most of a
	// healthy host's connections are private, and being able to say so is
	// what makes the public ones worth looking at.
	Private bool `json:"private"`
	// Service names the local port from the catalogue, so "who is on the
	// database" reads as a sentence.
	Service string `json:"service,omitempty"`
	// Protocols, RemotePorts and States keep what folding by address would
	// otherwise discard: which transports, which ports at the far end (at
	// most maxRemotePorts of them) and how many sockets are in each state.
	Protocols   []string       `json:"protocols"`
	RemotePorts []uint32       `json:"remotePorts"`
	MorePorts   int            `json:"morePorts,omitempty"`
	States      map[string]int `json:"states"`
}

// maxRemotePorts bounds the far-end ports one peer lists; a client opening a
// connection per request uses a new ephemeral port each time.
const maxRemotePorts = 8

// Connections summarises the host's current inbound and outbound sockets.
type Connections struct {
	Peers []Peer `json:"peers"`
	// Total is every connection counted, including the ones folded away.
	Total int `json:"total"`
	// Listening is excluded from the peer list and reported separately so the
	// two numbers add up for a reader.
	Listening int `json:"listening"`
	// Loopback is how many never left the machine.
	Loopback int `json:"loopback"`
	// Quality is what this read could and could not see.
	Quality ConnectionQuality `json:"quality"`
}

// ConnectionQuality is the limits of one read of the connection table. The
// table is a snapshot: whatever opened and closed between two reads was
// never in it.
type ConnectionQuality struct {
	ReadAt time.Time `json:"readAt"`
	// IntervalSeconds is the time since the previous read, zero for the
	// first; a connection shorter than it may not have been seen.
	IntervalSeconds float64 `json:"intervalSeconds"`
	// ClosedSinceLast is how many tuples of the previous read are gone.
	ClosedSinceLast int `json:"closedSinceLast"`
	// UnconnectedUDP is datagram sockets with no fixed peer — servers and
	// resolvers, QUIC among them — whose callers the table cannot name.
	UnconnectedUDP int      `json:"unconnectedUdp"`
	Limits         []string `json:"limits"`
}

var connectionLimits = []string{
	"A snapshot of the kernel's socket table: connections that opened and closed between two reads are not in it.",
	"A close is noticed as a tuple missing from the next read; its time is somewhere between the two reads.",
	"Unconnected UDP sockets have no peer to fold by, so their callers are not listed.",
}

func (s *Service) Connections(ctx context.Context) (*Connections, error) {
	list, listening, unconnected, err := readConnections(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	q := s.conns.observe(now, list)
	q.UnconnectedUDP = unconnected
	out := SummarisePeers(list)
	out.Listening = listening
	out.Quality = q
	return out, nil
}

// readConnections lists every inet socket with a peer, its owning program
// named, and counts the listeners and the unconnected datagram sockets.
func readConnections(ctx context.Context) ([]Connection, int, int, error) {
	conns, err := gnet.ConnectionsWithContext(ctx, "inet")
	if err != nil {
		return nil, 0, 0, err
	}
	cache := map[int32]string{}
	list := make([]Connection, 0, len(conns))
	listening, unconnected := 0, 0
	for _, c := range conns {
		if c.Status == "LISTEN" {
			listening++
			continue
		}
		// A socket with no peer is an unconnected datagram socket, not a
		// listener; counting it as one made the two figures fail to add up.
		if c.Raddr.IP == "" {
			if protocolOf(c.Type) == "udp" {
				unconnected++
			}
			continue
		}
		conn := Connection{
			Protocol:   protocolOf(c.Type),
			LocalAddr:  c.Laddr.IP,
			LocalPort:  c.Laddr.Port,
			RemoteAddr: c.Raddr.IP,
			RemotePort: c.Raddr.Port,
			Status:     c.Status,
			PID:        c.Pid,
		}
		if c.Pid > 0 {
			name, ok := cache[c.Pid]
			if !ok {
				if p, err := process.NewProcessWithContext(ctx, c.Pid); err == nil {
					name, _ = p.NameWithContext(ctx)
				}
				cache[c.Pid] = name
			}
			conn.Process = name
		}
		list = append(list, conn)
	}
	return list, listening, unconnected, nil
}

func protocolOf(sockType uint32) string {
	if sockType == 2 { // SOCK_DGRAM
		return "udp"
	}
	return "tcp"
}

// SummarisePeers folds a connection list by remote address.
func SummarisePeers(conns []Connection) *Connections {
	out := &Connections{Peers: []Peer{}, Total: len(conns), Quality: ConnectionQuality{Limits: connectionLimits}}
	byAddr := map[string]*Peer{}
	ports := map[string]map[uint32]bool{}
	remote := map[string]map[uint32]bool{}
	protos := map[string]map[string]bool{}
	procs := map[string]map[string]bool{}
	for _, c := range conns {
		ip := net.ParseIP(c.RemoteAddr)
		if ip != nil && ip.IsLoopback() {
			out.Loopback++
			continue
		}
		p, ok := byAddr[c.RemoteAddr]
		if !ok {
			p = &Peer{Address: c.RemoteAddr, Ports: []uint32{}, Processes: []string{}, Protocols: []string{}, RemotePorts: []uint32{}, States: map[string]int{}}
			p.Private = ip != nil && (isPrivate(ip) || tailscaleNet.Contains(ip))
			byAddr[c.RemoteAddr] = p
			ports[c.RemoteAddr] = map[uint32]bool{}
			remote[c.RemoteAddr] = map[uint32]bool{}
			protos[c.RemoteAddr] = map[string]bool{}
			procs[c.RemoteAddr] = map[string]bool{}
		}
		p.Count++
		if strings.EqualFold(c.Status, "ESTABLISHED") {
			p.Established++
		}
		state := c.Status
		if state == "" || state == "NONE" {
			state = "CONNECTED"
		}
		p.States[state]++
		protos[c.RemoteAddr][c.Protocol] = true
		remote[c.RemoteAddr][c.RemotePort] = true
		ports[c.RemoteAddr][c.LocalPort] = true
		if c.Process != "" {
			procs[c.RemoteAddr][c.Process] = true
		}
	}
	for addr, p := range byAddr {
		for port := range ports[addr] {
			p.Ports = append(p.Ports, port)
		}
		sort.Slice(p.Ports, func(i, j int) bool { return p.Ports[i] < p.Ports[j] })
		for port := range remote[addr] {
			p.RemotePorts = append(p.RemotePorts, port)
		}
		sort.Slice(p.RemotePorts, func(i, j int) bool { return p.RemotePorts[i] < p.RemotePorts[j] })
		if len(p.RemotePorts) > maxRemotePorts {
			p.MorePorts = len(p.RemotePorts) - maxRemotePorts
			p.RemotePorts = p.RemotePorts[:maxRemotePorts]
		}
		for proto := range protos[addr] {
			p.Protocols = append(p.Protocols, proto)
		}
		sort.Strings(p.Protocols)
		for name := range procs[addr] {
			p.Processes = append(p.Processes, name)
		}
		sort.Strings(p.Processes)
		// Named after the first local port, which for an inbound connection
		// is the service being used. Ambiguous for an outbound one, and the
		// UI shows both ends, so the name is a hint rather than a claim.
		if len(p.Ports) > 0 {
			if preset, ok := PresetFor(portString(p.Ports[0]), ""); ok {
				p.Service = preset.Name
			}
		}
		out.Peers = append(out.Peers, *p)
	}
	// Busiest first: the address holding forty sockets is the one worth a
	// look, and a list sorted by address makes it invisible.
	sort.Slice(out.Peers, func(i, j int) bool {
		a, b := out.Peers[i], out.Peers[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.Address < b.Address
	})
	return out
}

func portString(p uint32) string {
	return strconv.FormatUint(uint64(p), 10)
}

// A connection's identity across reads: both ends and the transport. The
// owner can change (a socket handed to a child) without it being a new
// connection.
type tupleKey struct {
	proto, local string
	lport        uint32
	remote       string
	rport        uint32
}

func keyOf(c Connection) tupleKey {
	return tupleKey{c.Protocol, unmapped(c.LocalAddr), c.LocalPort, unmapped(c.RemoteAddr), c.RemotePort}
}

func unmapped(addr string) string {
	if a, err := netip.ParseAddr(addr); err == nil {
		return a.Unmap().String()
	}
	return addr
}

// ClosedConnection is a tuple that was in one read and not the next.
type ClosedConnection struct {
	Protocol   string `json:"protocol"`
	LocalAddr  string `json:"localAddress"`
	LocalPort  uint32 `json:"localPort"`
	RemoteAddr string `json:"remoteAddress"`
	RemotePort uint32 `json:"remotePort"`
	Process    string `json:"process,omitempty"`
	// FirstSeen and LastSeen are the first and last reads that held it;
	// GoneBy is the read that no longer did. It closed between LastSeen and
	// GoneBy, after being open at least ObservedSeconds.
	FirstSeen       time.Time `json:"firstSeen"`
	LastSeen        time.Time `json:"lastSeen"`
	GoneBy          time.Time `json:"goneBy"`
	ObservedSeconds float64   `json:"observedSeconds"`
}

const (
	// closedKept bounds the closes remembered, and closedFor how long.
	closedKept = 512
	closedFor  = 15 * time.Minute
)

type trackedConn struct {
	conn      Connection
	firstSeen time.Time
	lastSeen  time.Time
}

// connTracker carries the table between reads. Its memory is the sockets of
// the last read and a bounded ring of closes.
type connTracker struct {
	mu     sync.Mutex
	seen   map[tupleKey]trackedConn
	at     time.Time
	closed []ClosedConnection
}

// observe stamps each connection with when it was first seen, records the
// tuples that have gone, and answers this read's quality.
func (t *connTracker) observe(now time.Time, list []Connection) ConnectionQuality {
	t.mu.Lock()
	defer t.mu.Unlock()
	q := ConnectionQuality{ReadAt: now.UTC(), Limits: connectionLimits}
	if !t.at.IsZero() && now.After(t.at) {
		q.IntervalSeconds = now.Sub(t.at).Seconds()
	}
	next := make(map[tupleKey]trackedConn, len(list))
	for i := range list {
		k := keyOf(list[i])
		first := now
		if prev, ok := t.seen[k]; ok {
			first = prev.firstSeen
		}
		list[i].FirstSeen = first.UTC()
		next[k] = trackedConn{conn: list[i], firstSeen: first, lastSeen: now}
	}
	for k, prev := range t.seen {
		if _, ok := next[k]; ok {
			continue
		}
		q.ClosedSinceLast++
		c := prev.conn
		t.closed = append(t.closed, ClosedConnection{Protocol: c.Protocol, LocalAddr: c.LocalAddr, LocalPort: c.LocalPort,
			RemoteAddr: c.RemoteAddr, RemotePort: c.RemotePort, Process: c.Process,
			FirstSeen: prev.firstSeen.UTC(), LastSeen: prev.lastSeen.UTC(), GoneBy: now.UTC(),
			ObservedSeconds: prev.lastSeen.Sub(prev.firstSeen).Seconds()})
	}
	cut := 0
	for cut < len(t.closed) && (len(t.closed)-cut > closedKept || now.Sub(t.closed[cut].GoneBy) > closedFor) {
		cut++
	}
	t.closed = append([]ClosedConnection(nil), t.closed[cut:]...)
	t.seen, t.at = next, now
	return q
}

// closedTo is the remembered closes of one remote address, newest first.
func (t *connTracker) closedTo(addr string) []ClosedConnection {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := []ClosedConnection{}
	for i := len(t.closed) - 1; i >= 0; i-- {
		if unmapped(t.closed[i].RemoteAddr) == addr {
			out = append(out, t.closed[i])
		}
	}
	return out
}

// PeerDetail is one remote address in full: every live tuple with its age,
// and the tuples to it seen closing in the last fifteen minutes.
type PeerDetail struct {
	Address string             `json:"address"`
	Private bool               `json:"private"`
	Sockets []Connection       `json:"sockets"`
	Closed  []ClosedConnection `json:"closed"`
	Quality ConnectionQuality  `json:"quality"`
}

// ErrInvalidAddress is a peer that is not an IP address.
var ErrInvalidAddress = errors.New("the peer is an IPv4 or IPv6 address")

// PeerDetail reads the table again and keeps one address's tuples. The read
// counts as a read of the whole table, so the closes it notices are the
// summary's too.
func (s *Service) PeerDetail(ctx context.Context, address string) (*PeerDetail, error) {
	a, err := netip.ParseAddr(address)
	if err != nil {
		return nil, ErrInvalidAddress
	}
	want := a.Unmap().String()
	list, _, unconnected, err := readConnections(ctx)
	if err != nil {
		return nil, err
	}
	q := s.conns.observe(time.Now(), list)
	q.UnconnectedUDP = unconnected
	ip := net.ParseIP(want)
	d := &PeerDetail{Address: want, Sockets: []Connection{}, Quality: q,
		Private: ip != nil && (isPrivate(ip) || tailscaleNet.Contains(ip))}
	for _, c := range list {
		if unmapped(c.RemoteAddr) == want {
			d.Sockets = append(d.Sockets, c)
		}
	}
	sort.Slice(d.Sockets, func(i, j int) bool {
		x, y := d.Sockets[i], d.Sockets[j]
		if !x.FirstSeen.Equal(y.FirstSeen) {
			return x.FirstSeen.Before(y.FirstSeen)
		}
		if x.LocalPort != y.LocalPort {
			return x.LocalPort < y.LocalPort
		}
		return x.RemotePort < y.RemotePort
	})
	d.Closed = s.conns.closedTo(want)
	return d, nil
}
