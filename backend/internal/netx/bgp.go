package netx

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// BGPView is what FRR's BGP daemon says about its neighbours. It is read-only:
// the daemon's configuration is FRR's own, in its own files and its own shell.
type BGPView struct {
	// Installed is false where there is no vtysh; the page then offers the
	// install and nothing else.
	Installed bool `json:"installed"`
	// Running is whether the frr unit is active.
	Running  bool        `json:"running"`
	Families []BGPFamily `json:"families"`
	// Error is what vtysh said when it could not answer, such as a daemon
	// that is not running. It is a reading, not a failure of this request.
	Error string `json:"error,omitempty"`
}

// BGPFamily is one address family's summary: "ipv4Unicast", "ipv6Unicast", or
// whichever FRR names.
type BGPFamily struct {
	Name     string    `json:"name"`
	RouterID string    `json:"routerId"`
	LocalAS  uint32    `json:"localAs"`
	Peers    []BGPPeer `json:"peers"`
}

// BGPPeer is one neighbour.
type BGPPeer struct {
	Address  string `json:"address"`
	Hostname string `json:"hostname,omitempty"`
	RemoteAS uint32 `json:"remoteAs"`
	// State is Established, Active, Idle, Connect, OpenSent or OpenConfirm,
	// or what FRR prints for a peer it has not reached.
	State string `json:"state"`
	// Uptime is FRR's own rendering ("1d02h03m"); UptimeSeconds is the same
	// as a number, zero for a session that is not up.
	Uptime             string `json:"uptime,omitempty"`
	UptimeSeconds      int64  `json:"uptimeSeconds"`
	PrefixesReceived   int    `json:"prefixesReceived"`
	PrefixesSent       int    `json:"prefixesSent"`
	MessagesReceived   int    `json:"messagesReceived"`
	MessagesSent       int    `json:"messagesSent"`
	Description        string `json:"description,omitempty"`
	ConnectionsDropped int    `json:"connectionsDropped"`
}

// bgpSummary is one family's object in `show bgp summary json`. FRR has
// renamed fields between releases (pfxRcd was prefixReceivedCount, the uptime
// came in milliseconds later), so the older names are read as well and
// whichever is present is used.
type bgpSummary struct {
	RouterID string             `json:"routerId"`
	AS       uint32             `json:"as"`
	Peers    map[string]bgpPeer `json:"peers"`
}

type bgpPeer struct {
	Hostname      string `json:"hostname"`
	RemoteAs      any    `json:"remoteAs"`
	MsgRcvd       int    `json:"msgRcvd"`
	MsgSent       int    `json:"msgSent"`
	PeerUptime    string `json:"peerUptime"`
	PeerUptimeMs  int64  `json:"peerUptimeMsec"`
	PfxRcd        *int   `json:"pfxRcd"`
	PrefixRcvdOld *int   `json:"prefixReceivedCount"`
	PfxSnt        *int   `json:"pfxSnt"`
	State         string `json:"state"`
	PeerState     string `json:"peerState"`
	Desc          string `json:"desc"`
	Dropped       int    `json:"connectionsDropped"`
}

// BGP reads the neighbours of FRR's BGP daemon.
func (s *Service) BGP(ctx context.Context) (*BGPView, error) {
	view := &BGPView{Families: []BGPFamily{}}
	if !has("vtysh") {
		return view, nil
	}
	view.Installed = true
	if out, _ := run(ctx, "systemctl", "is-active", "frr"); strings.TrimSpace(out) == "active" {
		view.Running = true
	}
	out, err := run(ctx, "vtysh", "-c", "show bgp summary json")
	if err != nil {
		view.Error = firstLines(strings.TrimSpace(firstNonEmpty(out, err.Error())), 2)
		return view, nil
	}
	families, err := parseBGPSummary(out)
	if err != nil {
		view.Error = err.Error()
		return view, nil
	}
	view.Families = families
	return view, nil
}

// parseBGPSummary reads `show bgp summary json`. A single-family answer from
// older releases has the summary at the top level; newer ones key each family
// by name. Anything that is not a family object (an "Unknown" or "warning"
// string FRR prints for a daemon with no BGP instance) is skipped, not an
// error: it means there is nothing to show.
func parseBGPSummary(out string) ([]BGPFamily, error) {
	out = strings.TrimSpace(out)
	if out == "" {
		return []BGPFamily{}, nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &top); err != nil {
		return nil, fmt.Errorf("vtysh printed something unreadable: %w", err)
	}
	// Before FRR 7.x split the families, the summary was the one IPv4 unicast
	// family and sat at the top level.
	if _, ok := top["peers"]; ok {
		var sum bgpSummary
		if err := json.Unmarshal([]byte(out), &sum); err != nil {
			return nil, fmt.Errorf("vtysh printed something unreadable: %w", err)
		}
		return []BGPFamily{sum.family("ipv4Unicast")}, nil
	}
	names := make([]string, 0, len(top))
	for name := range top {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return familyRank(names[i]) < familyRank(names[j]) })
	families := []BGPFamily{}
	for _, name := range names {
		var sum bgpSummary
		if err := json.Unmarshal(top[name], &sum); err != nil || (sum.Peers == nil && sum.RouterID == "") {
			continue
		}
		families = append(families, sum.family(name))
	}
	return families, nil
}

func (sum bgpSummary) family(name string) BGPFamily {
	fam := BGPFamily{Name: name, RouterID: sum.RouterID, LocalAS: sum.AS, Peers: []BGPPeer{}}
	for addr, p := range sum.Peers {
		fam.Peers = append(fam.Peers, p.view(addr))
	}
	sort.Slice(fam.Peers, func(i, j int) bool { return fam.Peers[i].Address < fam.Peers[j].Address })
	return fam
}

func familyRank(name string) int {
	switch name {
	case "ipv4Unicast":
		return 0
	case "ipv6Unicast":
		return 1
	}
	return 2
}

func (p bgpPeer) view(addr string) BGPPeer {
	out := BGPPeer{
		Address: addr, Hostname: p.Hostname, State: firstNonEmpty(p.State, p.PeerState, "Unknown"),
		Uptime: p.PeerUptime, MessagesReceived: p.MsgRcvd, MessagesSent: p.MsgSent,
		Description: p.Desc, ConnectionsDropped: p.Dropped,
	}
	switch v := p.RemoteAs.(type) {
	case float64:
		out.RemoteAS = uint32(v)
	case string:
		// FRR prints "external" or "internal" for a peer whose AS is named by
		// type, and the number as a string in some releases.
		var n uint32
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			out.RemoteAS = n
		}
	}
	if out.State == "Established" {
		out.UptimeSeconds = p.PeerUptimeMs / 1000
		if p.PeerUptimeMs == 0 {
			out.UptimeSeconds = parseFRRUptime(p.PeerUptime)
		}
	}
	switch {
	case p.PfxRcd != nil:
		out.PrefixesReceived = *p.PfxRcd
	case p.PrefixRcvdOld != nil:
		out.PrefixesReceived = *p.PrefixRcvdOld
	}
	if p.PfxSnt != nil {
		out.PrefixesSent = *p.PfxSnt
	}
	return out
}

var uptimePart = regexp.MustCompile(`(\d+)([wdhms])`)

// parseFRRUptime reads the uptime FRR prints beside a peer: "00:01:02" for the
// first day, then "1d02h03m", then "2w3d04h". The millisecond field is the
// better source and newer releases have it; this is for the ones that do not.
func parseFRRUptime(s string) int64 {
	if strings.Contains(s, ":") {
		var h, m, sec int64
		if _, err := fmt.Sscanf(s, "%d:%d:%d", &h, &m, &sec); err == nil {
			return h*3600 + m*60 + sec
		}
		return 0
	}
	var total int64
	for _, part := range uptimePart.FindAllStringSubmatch(s, -1) {
		n, _ := strconv.ParseInt(part[1], 10, 64)
		switch part[2] {
		case "w":
			total += n * 7 * 86400
		case "d":
			total += n * 86400
		case "h":
			total += n * 3600
		case "m":
			total += n * 60
		case "s":
			total += n
		}
	}
	return total
}
