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
	// OSPF is ospfd's and ospf6d's neighbours where either answers.
	OSPF []OSPFNeighbor `json:"ospf"`
	// Configuration is not offered: policy, OSPF areas and failover belong
	// to FRR's own configuration, which this page only reads.
	ReadOnly string `json:"readOnly"`
}

// BGPPolicy names what filters a neighbour's routes in each direction, as
// FRR reports it. The names are read; their contents stay in FRR.
type BGPPolicy struct {
	RouteMapIn    string `json:"routeMapIn,omitempty"`
	RouteMapOut   string `json:"routeMapOut,omitempty"`
	PrefixListIn  string `json:"prefixListIn,omitempty"`
	PrefixListOut string `json:"prefixListOut,omitempty"`
	FilterListIn  string `json:"filterListIn,omitempty"`
	FilterListOut string `json:"filterListOut,omitempty"`
}

// OSPFNeighbor is one OSPFv2 or OSPFv3 adjacency.
type OSPFNeighbor struct {
	Version       int    `json:"version"`
	RouterID      string `json:"routerId"`
	Address       string `json:"address,omitempty"`
	Interface     string `json:"interface,omitempty"`
	State         string `json:"state"`
	Role          string `json:"role,omitempty"`
	Priority      int    `json:"priority"`
	DeadSeconds   int64  `json:"deadSeconds,omitempty"`
	UptimeSeconds int64  `json:"uptimeSeconds,omitempty"`
}

const bgpReadOnly = "Read from FRR. Neighbours, policies, OSPF areas and any failover behaviour are configured in FRR itself (vtysh or frr.conf); this page does not write them."

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
	// Policy is the neighbour's filters for this family, when FRR names any.
	Policy *BGPPolicy `json:"policy,omitempty"`
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
	view := &BGPView{Families: []BGPFamily{}, OSPF: []OSPFNeighbor{}, ReadOnly: bgpReadOnly}
	if !has("vtysh") {
		return view, nil
	}
	defer func() { view.OSPF = readOSPF(ctx) }()
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
	if len(families) > 0 {
		if raw, err := run(ctx, "vtysh", "-c", "show bgp neighbors json"); err == nil {
			applyBGPPolicies(families, raw)
		}
	}
	view.Families = families
	return view, nil
}

// applyBGPPolicies attaches each neighbour's per-family filter names from
// `show bgp neighbors json`. A shape it cannot read leaves them out.
func applyBGPPolicies(families []BGPFamily, raw string) {
	var neighbors map[string]struct {
		AF map[string]map[string]any `json:"addressFamilyInfo"`
	}
	if json.Unmarshal([]byte(raw), &neighbors) != nil {
		return
	}
	for fi := range families {
		for pi := range families[fi].Peers {
			peer := &families[fi].Peers[pi]
			info, ok := neighbors[peer.Address].AF[families[fi].Name]
			if !ok {
				continue
			}
			text := func(keys ...string) string {
				for _, k := range keys {
					if v, ok := info[k].(string); ok && v != "" {
						return v
					}
				}
				return ""
			}
			p := BGPPolicy{
				RouteMapIn:    text("routeMapForIncomingAdvertisements"),
				RouteMapOut:   text("routeMapForOutgoingAdvertisements"),
				PrefixListIn:  text("incomingUpdatePrefixFilterList"),
				PrefixListOut: text("outgoingUpdatePrefixFilterList"),
				FilterListIn:  text("incomingUpdateAsPathFilterList", "incomingUpdateNetworkFilterList"),
				FilterListOut: text("outgoingUpdateAsPathFilterList", "outgoingUpdateNetworkFilterList"),
			}
			if p != (BGPPolicy{}) {
				peer.Policy = &p
			}
		}
	}
}

// readOSPF reads both OSPF daemons' neighbours. A daemon that is not
// running, or prints something other than JSON, has no neighbours to show.
func readOSPF(ctx context.Context) []OSPFNeighbor {
	out := []OSPFNeighbor{}
	if raw, err := run(ctx, "vtysh", "-c", "show ip ospf neighbor json"); err == nil {
		out = append(out, parseOSPFNeighbors(raw)...)
	}
	if raw, err := run(ctx, "vtysh", "-c", "show ipv6 ospf6 neighbor json"); err == nil {
		out = append(out, parseOSPF6Neighbors(raw)...)
	}
	return out
}

// parseOSPFNeighbors reads ospfd's JSON, keyed by neighbour router ID. FRR
// renamed state to nbrState and added explicit timers in later releases;
// both spellings are read.
func parseOSPFNeighbors(raw string) []OSPFNeighbor {
	var doc struct {
		Neighbors map[string][]struct {
			Priority     int    `json:"priority"`
			NbrPriority  *int   `json:"nbrPriority"`
			State        string `json:"state"`
			NbrState     string `json:"nbrState"`
			Role         string `json:"role"`
			Address      string `json:"address"`
			IfaceAddress string `json:"ifaceAddress"`
			IfaceName    string `json:"ifaceName"`
			DeadMs       int64  `json:"deadTimeMsecs"`
			DeadDueMs    int64  `json:"routerDeadIntervalTimerDueMsec"`
			UpMs         int64  `json:"upTimeInMsec"`
		} `json:"neighbors"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(raw)), &doc) != nil {
		return nil
	}
	var out []OSPFNeighbor
	for id, entries := range doc.Neighbors {
		for _, e := range entries {
			n := OSPFNeighbor{Version: 2, RouterID: id, Address: firstNonEmpty(e.Address, e.IfaceAddress), Priority: e.Priority, Role: e.Role}
			if e.NbrPriority != nil {
				n.Priority = *e.NbrPriority
			}
			state := firstNonEmpty(e.NbrState, e.State)
			if st, role, ok := strings.Cut(state, "/"); ok {
				state = st
				n.Role = firstNonEmpty(n.Role, role)
			}
			n.State = state
			// ifaceName is "eth1:192.0.2.1": the device and its address.
			n.Interface, _, _ = strings.Cut(e.IfaceName, ":")
			n.DeadSeconds = max(e.DeadMs, e.DeadDueMs) / 1000
			n.UptimeSeconds = e.UpMs / 1000
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RouterID < out[j].RouterID })
	return out
}

// parseOSPF6Neighbors reads ospf6d's JSON list.
func parseOSPF6Neighbors(raw string) []OSPFNeighbor {
	var doc struct {
		Neighbors []struct {
			NeighborID    string `json:"neighborId"`
			Priority      int    `json:"priority"`
			DeadTime      string `json:"deadTime"`
			State         string `json:"state"`
			IfState       string `json:"ifState"`
			InterfaceName string `json:"interfaceName"`
			Duration      string `json:"duration"`
		} `json:"neighbors"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(raw)), &doc) != nil {
		return nil
	}
	var out []OSPFNeighbor
	for _, e := range doc.Neighbors {
		out = append(out, OSPFNeighbor{
			Version: 3, RouterID: e.NeighborID, Interface: e.InterfaceName, State: e.State, Role: e.IfState,
			Priority: e.Priority, DeadSeconds: parseFRRUptime(e.DeadTime), UptimeSeconds: parseFRRUptime(e.Duration),
		})
	}
	return out
}

// BGPRoutesView is one family's BGP table as bgpd holds it, read-only.
// A table larger than bgpRouteBrowseLimit is not listed whole; a prefix
// query reads only that prefix's paths.
type BGPRoutesView struct {
	Family    string     `json:"family"`
	Prefix    string     `json:"prefix,omitempty"`
	RouterID  string     `json:"routerId,omitempty"`
	LocalAS   uint32     `json:"localAs,omitempty"`
	Total     int        `json:"total"`
	Truncated bool       `json:"truncated"`
	Routes    []BGPRoute `json:"routes"`
	Error     string     `json:"error,omitempty"`
}

// BGPRoute is one path to a prefix.
type BGPRoute struct {
	Prefix    string   `json:"prefix"`
	Best      bool     `json:"best"`
	Valid     bool     `json:"valid"`
	Multipath bool     `json:"multipath"`
	Nexthops  []string `json:"nexthops"`
	Peer      string   `json:"peer,omitempty"`
	Path      string   `json:"path"`
	Origin    string   `json:"origin,omitempty"`
	LocalPref *int     `json:"localPref,omitempty"`
	MED       *int     `json:"med,omitempty"`
	Weight    int      `json:"weight"`
	From      string   `json:"from,omitempty"`
}

const bgpRouteBrowseLimit = 5000

var bgpFamilyWords = map[string][]string{"ipv4Unicast": {"ipv4", "unicast"}, "ipv6Unicast": {"ipv6", "unicast"}}

// BGPRoutes reads one family's table, or one prefix of it. The command is
// built from the closed family list and a parsed prefix.
func (s *Service) BGPRoutes(ctx context.Context, family, prefix string) (*BGPRoutesView, error) {
	words, ok := bgpFamilyWords[family]
	if !ok {
		return nil, fmt.Errorf("the family is ipv4Unicast or ipv6Unicast")
	}
	view := &BGPRoutesView{Family: family, Routes: []BGPRoute{}}
	if prefix != "" {
		p, err := ParsePrefix(prefix)
		if err != nil {
			return nil, err
		}
		if (family == "ipv6Unicast") != p.Addr().Is6() {
			return nil, fmt.Errorf("%s is not an %s prefix", p, words[0])
		}
		view.Prefix = p.Masked().String()
	}
	if !has("vtysh") {
		return nil, &UnavailableError{Tool: "vtysh", Package: "frr"}
	}
	command := "show bgp " + strings.Join(words, " ")
	if view.Prefix != "" {
		command += " " + view.Prefix
	} else {
		summary, err := run(ctx, "vtysh", "-c", "show bgp summary json")
		if err != nil {
			view.Error = firstLines(strings.TrimSpace(firstNonEmpty(summary, err.Error())), 2)
			return view, nil
		}
		families, err := parseBGPSummary(summary)
		if err != nil {
			view.Error = err.Error()
			return view, nil
		}
		for _, f := range families {
			if f.Name != family {
				continue
			}
			for _, p := range f.Peers {
				view.Total += p.PrefixesReceived
			}
		}
		if view.Total > bgpRouteBrowseLimit {
			view.Truncated = true
			view.Error = fmt.Sprintf("Neighbours sent %d prefixes; a table over %d is not listed whole. Name a prefix to read its paths.", view.Total, bgpRouteBrowseLimit)
			return view, nil
		}
	}
	out, err := run(ctx, "vtysh", "-c", command+" json")
	if err != nil {
		view.Error = firstLines(strings.TrimSpace(firstNonEmpty(out, err.Error())), 2)
		return view, nil
	}
	if err := parseBGPRoutes(out, view); err != nil {
		view.Error = err.Error()
	}
	return view, nil
}

// bgpPath reads one path in either of FRR's shapes: the table listing's
// flat fields or a single prefix's nested aspath, bestpath and peer.
type bgpPath struct {
	Valid     bool            `json:"valid"`
	BestPath  json.RawMessage `json:"bestpath"`
	Multipath bool            `json:"multipath"`
	PathFrom  string          `json:"pathFrom"`
	Network   string          `json:"network"`
	Metric    *int            `json:"metric"`
	MED       *int            `json:"med"`
	LocPrf    *int            `json:"locPrf"`
	Weight    int             `json:"weight"`
	PeerID    string          `json:"peerId"`
	Path      json.RawMessage `json:"path"`
	ASPath    json.RawMessage `json:"aspath"`
	Origin    string          `json:"origin"`
	Peer      *struct {
		PeerID string `json:"peerId"`
		Type   string `json:"type"`
	} `json:"peer"`
	Nexthops []struct {
		IP string `json:"ip"`
	} `json:"nexthops"`
}

func (p bgpPath) route(prefix string) BGPRoute {
	r := BGPRoute{Prefix: firstNonEmpty(p.Network, prefix), Valid: p.Valid, Multipath: p.Multipath, Weight: p.Weight,
		Peer: p.PeerID, Origin: p.Origin, LocalPref: p.LocPrf, From: p.PathFrom, Nexthops: []string{}}
	r.MED = p.MED
	if r.MED == nil {
		r.MED = p.Metric
	}
	var flag bool
	var best struct {
		Overall bool `json:"overall"`
	}
	if json.Unmarshal(p.BestPath, &flag) == nil {
		r.Best = flag
	} else if json.Unmarshal(p.BestPath, &best) == nil {
		r.Best = best.Overall
	}
	var text string
	var nested struct {
		String string `json:"string"`
	}
	for _, raw := range []json.RawMessage{p.Path, p.ASPath} {
		if json.Unmarshal(raw, &text) == nil {
			r.Path = text
		} else if json.Unmarshal(raw, &nested) == nil && nested.String != "" {
			r.Path = nested.String
		}
	}
	if p.Peer != nil {
		r.Peer, r.From = firstNonEmpty(r.Peer, p.Peer.PeerID), firstNonEmpty(r.From, p.Peer.Type)
	}
	for _, n := range p.Nexthops {
		if n.IP != "" {
			r.Nexthops = append(r.Nexthops, n.IP)
		}
	}
	return r
}

// parseBGPRoutes reads a table listing ({"routes": {prefix: [paths]}}) or a
// single prefix ({"prefix": ..., "paths": [paths]}).
func parseBGPRoutes(out string, view *BGPRoutesView) error {
	var doc struct {
		RouterID string               `json:"routerId"`
		LocalAS  uint32               `json:"localAS"`
		Routes   map[string][]bgpPath `json:"routes"`
		Prefix   string               `json:"prefix"`
		Paths    []bgpPath            `json:"paths"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &doc); err != nil {
		return fmt.Errorf("vtysh printed something unreadable: %w", err)
	}
	view.RouterID, view.LocalAS = doc.RouterID, doc.LocalAS
	prefixes := make([]string, 0, len(doc.Routes))
	for p := range doc.Routes {
		prefixes = append(prefixes, p)
	}
	sort.Strings(prefixes)
	for _, p := range prefixes {
		for _, path := range doc.Routes[p] {
			view.Routes = append(view.Routes, path.route(p))
		}
	}
	for _, path := range doc.Paths {
		view.Routes = append(view.Routes, path.route(doc.Prefix))
	}
	if view.Prefix == "" {
		view.Total = len(prefixes)
	} else {
		view.Total = len(view.Routes)
	}
	return nil
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
