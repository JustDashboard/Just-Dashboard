package proxysvc

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	gnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

// A stream as the network page joins it: what its file says, the sockets
// nginx holds for it, the connections nginx has open to each backend now,
// and what its log recorded of sessions reaching them. A configured edge is
// not traversal; the backend legs nginx holds and the sessions it logged are
// what nginx did, each with how it was read.

// StreamPath is one stream's configuration and its traffic's evidence.
type StreamPath struct {
	Name        string `json:"name"`
	State       string `json:"state"`
	StateReason string `json:"stateReason,omitempty"`
	Paused      bool   `json:"paused,omitempty"`
	Protocol    string `json:"protocol"`
	// Listens are the sockets the stream asks for: "port 5432/tcp".
	Listens []string `json:"listens"`
	Balance string   `json:"balance,omitempty"`
	// Access is the access list in a sentence: open, or what it admits.
	Access string `json:"access"`
	// TLS says where nginx ends or starts TLS: "clients", "backend", both
	// or neither; Routes is how many names are routed by SNI.
	TLS    string `json:"tls,omitempty"`
	Routes int    `json:"routes,omitempty"`
	// Clients are the TCP sessions established to the stream's ports now
	// whose owner is nginx (or unknown).
	Clients int `json:"clients"`
	// Servers are the stream's backends, with what nginx did towards each.
	Servers []StreamPathServer `json:"servers"`
	// Logging is whether the stream logs its sessions; the Log* fields are
	// its last hour, Complete false where the read's bound cut into it.
	Logging     bool   `json:"logging"`
	LogSessions int    `json:"logSessions"`
	LogFailed   int    `json:"logFailed"`
	LogDenied   int    `json:"logDenied"`
	LogComplete bool   `json:"logComplete"`
	LogError    string `json:"logError,omitempty"`
	// SocketsRead says whether nginx's established connections could be
	// read; Note says why not.
	SocketsRead bool   `json:"socketsRead"`
	Note        string `json:"note,omitempty"`
}

// StreamPathServer is one backend of a stream.
type StreamPathServer struct {
	Address string `json:"address"`
	Backup  bool   `json:"backup,omitempty"`
	Down    bool   `json:"down,omitempty"`
	// Connections are the TCP connections nginx holds open to this backend
	// now: the backend leg of sessions in progress.
	Connections int `json:"connections"`
	// Sessions and Failed are what the last hour's log says nginx forwarded
	// to it, and how many of those ended in nginx's 5xx.
	Sessions int `json:"sessions"`
	Failed   int `json:"failed"`
	// LastSession is when the latest of them ended, in Unix seconds.
	LastSession int64 `json:"lastSession,omitempty"`
}

// StreamLoggedSession is one session the stream logged.
type StreamLoggedSession struct {
	At       float64 `json:"at"`
	Client   string  `json:"client"`
	Status   int     `json:"status"`
	Upstream string  `json:"upstream"`
	Seconds  float64 `json:"seconds"`
}

// streamConnections lists the host's TCP connections; tests replace it.
var streamConnections = func(ctx context.Context) ([]gnet.ConnectionStat, error) {
	return gnet.ConnectionsWithContext(ctx, "tcp")
}

// nginxPid says whether a pid is nginx's, remembering each answer.
type nginxPid map[int32]bool

func (n nginxPid) is(ctx context.Context, pid int32) bool {
	if pid <= 0 {
		// A connection whose owner this account cannot read is kept, as the
		// session list keeps it: nginx's workers run as another user.
		return true
	}
	is, known := n[pid]
	if !known {
		if p, err := process.NewProcessWithContext(ctx, pid); err == nil {
			name, _ := p.NameWithContext(ctx)
			is = name == "nginx"
		}
		n[pid] = is
	}
	return is
}

// StreamPath reads a stream's configuration, state, sockets and recent log
// for the network page.
func (s *Service) StreamPath(ctx context.Context, name string, now time.Time) (*StreamPath, error) {
	spec, paused, err := s.streamSpecByName(name)
	if err != nil {
		return nil, err
	}
	out := &StreamPath{Name: name, Paused: paused, Protocol: spec.Protocol, Balance: spec.Balance,
		Logging: spec.LogConnections, Routes: len(spec.Routes), Access: streamAccess(spec), Servers: []StreamPathServer{}}
	if out.Protocol == "" {
		out.Protocol = "tcp"
	}
	for _, b := range streamBinds(spec) {
		if label := b.label(); !slices.Contains(out.Listens, label) {
			out.Listens = append(out.Listens, label)
		}
	}
	switch {
	case spec.TLS && spec.UpstreamTLS:
		out.TLS = "clients and backend"
	case spec.TLS:
		out.TLS = "clients"
	case spec.UpstreamTLS:
		out.TLS = "backend"
	}
	for _, server := range streamServers(spec) {
		out.Servers = append(out.Servers, StreamPathServer{Address: server.Address, Backup: server.Backup, Down: server.Down})
	}
	if paused {
		out.State, out.StateReason = "paused", "The stream is paused: nginx does not read its file."
	} else if status, err := s.Streams(ctx); err == nil {
		for _, entry := range status.Streams {
			if entry.Name == name {
				out.State, out.StateReason = entry.State, entry.StateReason
			}
		}
	}
	if out.State == "" {
		out.State = StreamUnknown
	}
	if !paused {
		s.streamSockets(ctx, spec, out)
	}
	s.streamLogged(spec, out, now)
	return out, nil
}

// streamServers are a stream's backends: its pool, or its one upstream.
func streamServers(spec *StreamSpec) []StreamServer {
	if len(spec.Servers) > 0 {
		return spec.Servers
	}
	if spec.Upstream == "" {
		return nil
	}
	return []StreamServer{{Address: spec.Upstream}}
}

// streamAccess says who the access list admits.
func streamAccess(spec *StreamSpec) string {
	switch {
	case len(spec.Rules) > 0:
		rules := make([]string, 0, len(spec.Rules))
		for _, r := range spec.Rules {
			rules = append(rules, r.Action+" "+r.Source)
		}
		rest := "deny"
		if spec.DefaultAllow != nil && *spec.DefaultAllow {
			rest = "allow"
		}
		return fmt.Sprintf("%s, then %s everyone else", strings.Join(rules, ", "), rest)
	case len(spec.AllowFrom) > 0:
		return "allow " + strings.Join(spec.AllowFrom, ", ") + ", deny everyone else"
	}
	return "open to every address that reaches the port"
}

// streamSockets counts the client sessions on the stream's ports and the
// connections nginx holds to each backend, from the host's socket table.
func (s *Service) streamSockets(ctx context.Context, spec *StreamSpec, out *StreamPath) {
	if spec.Protocol == "udp" {
		out.Note = "A UDP stream has no connection per session: nginx answers every client from its one socket, so its legs cannot be counted."
		return
	}
	conns, err := streamConnections(ctx)
	if err != nil {
		out.Note = "The host's connections could not be read: " + err.Error()
		return
	}
	out.SocketsRead = true
	var bound net.IP
	if spec.Address != "" {
		bound = net.ParseIP(spec.Address)
	}
	backends := map[string]int{}
	for i, server := range out.Servers {
		for _, addr := range resolvedAddresses(ctx, server.Address) {
			backends[addr] = i
		}
	}
	owners := nginxPid{}
	for _, c := range conns {
		if c.Status != "ESTABLISHED" || !owners.is(ctx, c.Pid) {
			continue
		}
		local := c.Laddr.Port >= uint32(spec.Listen) && c.Laddr.Port <= uint32(streamLastPort(spec)) &&
			(bound == nil || bound.Equal(net.ParseIP(c.Laddr.IP)))
		if local {
			out.Clients++
			continue
		}
		remote := net.JoinHostPort(normalIP(c.Raddr.IP), strconv.Itoa(int(c.Raddr.Port)))
		if i, ok := backends[remote]; ok {
			out.Servers[i].Connections++
		}
	}
}

// resolvedAddresses are host:port spellings of a backend as a socket shows
// its far end: the address itself, or each address its name resolves to.
func resolvedAddresses(ctx context.Context, address string) []string {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		return []string{net.JoinHostPort(normalIP(host), port)}
	}
	rctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupHost(rctx, host)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, net.JoinHostPort(normalIP(a), port))
	}
	return out
}

func normalIP(raw string) string {
	if ip := net.ParseIP(raw); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return v4.String()
		}
		return ip.String()
	}
	return raw
}

// streamLogged reads the stream's last hour of sessions, by the backend
// nginx forwarded each to.
func (s *Service) streamLogged(spec *StreamSpec, out *StreamPath, now time.Time) {
	if !spec.LogConnections {
		return
	}
	sessions, _, complete, err := readStreamLog(StreamLogPath(spec.Name), now.Add(-time.Hour), streamTrafficBudget)
	if err != nil {
		out.LogError = err.Error()
		return
	}
	out.LogComplete = complete
	byAddress := map[string]int{}
	for i, server := range out.Servers {
		byAddress[server.Address] = i
		host, port, err := net.SplitHostPort(server.Address)
		if err == nil && net.ParseIP(host) != nil {
			byAddress[net.JoinHostPort(normalIP(host), port)] = i
		}
	}
	for _, session := range sessions {
		out.LogSessions++
		failed := session.status >= 500
		switch {
		case session.status == 403:
			out.LogDenied++
		case failed:
			out.LogFailed++
		}
		// A retried session names every server it tried, the last one
		// last: that is where it ended.
		tried := strings.Split(session.upstream, ",")
		last := strings.TrimSpace(tried[len(tried)-1])
		if i, ok := byAddress[last]; ok {
			out.Servers[i].Sessions++
			if failed {
				out.Servers[i].Failed++
			}
			if at := int64(session.at); at > out.Servers[i].LastSession {
				out.Servers[i].LastSession = at
			}
		}
	}
}

// AwaitStreamSession waits up to wait for the stream's log to hold a session
// from client that ended at or after since: nginx writes a session's line
// when it closes. It answers nil when none came, and an error when the
// stream does not log or its log cannot be read.
func (s *Service) AwaitStreamSession(ctx context.Context, name, client string, since time.Time, wait time.Duration) (*StreamLoggedSession, error) {
	spec, _, err := s.streamSpecByName(name)
	if err != nil {
		return nil, err
	}
	if !spec.LogConnections {
		return nil, fmt.Errorf("the stream %s does not log its sessions", name)
	}
	deadline := time.Now().Add(wait)
	for {
		sessions, _, _, err := readStreamLog(StreamLogPath(name), since.Add(-time.Second), streamSummaryBudget)
		if err != nil {
			return nil, err
		}
		for i := len(sessions) - 1; i >= 0; i-- {
			session := sessions[i]
			if session.client == client && session.at >= float64(since.UnixNano())/1e9 {
				return &StreamLoggedSession{At: session.at, Client: session.client, Status: session.status,
					Upstream: session.upstream, Seconds: session.seconds}, nil
			}
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return nil, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
}
