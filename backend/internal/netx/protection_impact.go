package netx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// What a protection change would touch, the connection table's pressure,
// and the explicit revocation of sessions a blocklist already refuses.

// LocalOverlap is a network of this host's own that a list would cover.
type LocalOverlap struct {
	Network string `json:"network"`
	What    string `json:"what"`
}

// BlocklistPreview is what saving a list would block.
type BlocklistPreview struct {
	Valid    bool              `json:"valid"`
	Error    string            `json:"error,omitempty"`
	Refused  string            `json:"refused,omitempty"`
	Networks int               `json:"networks"`
	Coverage BlocklistCoverage `json:"coverage"`
	Diff     *BlocklistDiff    `json:"diff,omitempty"`
	Sources  []BlocklistSource `json:"sources"`
	// TrustedOverlap are trusted networks and exceptions inside the list:
	// they keep passing.
	TrustedOverlap []string           `json:"trustedOverlap"`
	LocalOverlap   []LocalOverlap     `json:"localOverlap"`
	Connections    *ConnectionImpact  `json:"connections,omitempty"`
	Impacts        []Impact           `json:"impacts"`
	Geography      *GeographyEvidence `json:"geography,omitempty"`
}

// GeographyEvidence explains what a country list actually is.
type GeographyEvidence struct {
	Source    string   `json:"source"`
	Basis     string   `json:"basis"`
	Limits    []string `json:"limits"`
	Countries []string `json:"countries"`
}

var countryGeography = GeographyEvidence{
	Source: "ipdeny.com aggregated zones, built from the regional internet registries' delegation files.",
	Basis:  "A country list is the address blocks registered to organisations in that country, not where a machine physically is.",
	Limits: []string{
		"Cloud, CDN and VPN providers announce one country's registered blocks from data centres elsewhere, so traffic can come from a blocked country's addresses without being there, and from anywhere through an unblocked one.",
		"Mobile carriers and travellers carry addresses across borders: a legitimate visitor abroad, or a customer on a roaming network, can be refused.",
		"Registries move blocks between holders; the list is only as current as its last fetch.",
		"Blocking a country does not stop an attacker using a host in another one.",
	},
}

// BlocklistPreviewRequest is a list's proposed body, and its id when edited.
type BlocklistPreviewRequest struct {
	ID   int              `json:"id"`
	List BlocklistRequest `json:"list"`
}

// PreviewBlocklist reports what a list would block without saving it. A
// country or feed list is fetched for the preview, through the same bounds
// and refusals as a save; nothing is cached or loaded.
func (s *Service) PreviewBlocklist(ctx context.Context, req BlocklistPreviewRequest, client string) (*BlocklistPreview, error) {
	sp, err := s.loadSpec()
	if err != nil {
		return nil, err
	}
	p := &BlocklistPreview{Sources: []BlocklistSource{}, TrustedOverlap: []string{}, LocalOverlap: []LocalOverlap{}, Impacts: []Impact{}}
	var existing *BlocklistSpec
	for i := range sp.Blocklists {
		if sp.Blocklists[i].ID == req.ID {
			existing = &sp.Blocklists[i]
		}
	}
	if req.ID != 0 && existing == nil {
		return nil, fmt.Errorf("blocklist %d: %w", req.ID, ErrNotFound)
	}
	kind := strings.ToLower(strings.TrimSpace(req.List.Kind))
	if existing != nil {
		kind = existing.Kind
	}
	if strings.TrimSpace(req.List.Name) == "" {
		req.List.Name = "preview"
	}
	bl, err := buildBlocklist(req.List, kind, client)
	if err != nil {
		var guard *GuardError
		if errors.As(err, &guard) {
			p.Refused = guard.Reason
		} else {
			p.Error = err.Error()
			return p, nil
		}
	}
	var nets []netip.Prefix
	if kind == "manual" {
		for _, raw := range req.List.Entries {
			if pr, err := ParsePrefix(raw); err == nil && pr.Bits() > 0 {
				nets = append(nets, pr.Masked())
			}
		}
		nets = mergePrefixes(nets)
	} else {
		r, err := fetchListWith(ctx, bl.Kind, bl.Countries, bl.URL, fetchOptions{signatureURL: bl.SignatureURL, publicKey: bl.PublicKey})
		if err != nil {
			p.Error = err.Error()
			return p, nil
		}
		nets, p.Sources = r.nets, r.sources
		if kind == "country" {
			g := countryGeography
			g.Countries = bl.Countries
			p.Geography = &g
		}
	}
	p.Valid = p.Refused == ""
	p.Networks, p.Coverage = len(nets), coverageOf(nets)
	if existing != nil {
		before, health := blocklistData(filepath.Join(s.paths.Dir, "lists"), *existing)
		p.Diff = blocklistDiff(before, health.Status == "ready", nets, gatewayNow().UTC())
	}
	covered := func(p netip.Prefix) bool {
		for _, n := range nets {
			if n.Overlaps(p) {
				return true
			}
		}
		return false
	}
	for _, t := range s.trustedFor(sp) {
		if covered(t) && !t.Addr().IsLoopback() {
			p.TrustedOverlap = append(p.TrustedOverlap, t.String()+" (trusted)")
		}
	}
	for _, e := range sp.Exceptions {
		if pr, err := ParsePrefix(e.Address); err == nil && covered(pr) && (e.Scope == "all" || existing != nil && e.Scope == "blocklist:"+strconv.Itoa(existing.ID)) {
			p.TrustedOverlap = append(p.TrustedOverlap, pr.String()+" (exception: "+e.Reason+")")
		}
	}
	p.LocalOverlap = localOverlaps(ctx, sp, nets)
	for _, l := range p.LocalOverlap {
		p.Impacts = append(p.Impacts, Impact{Severity: "warning", Kind: "local", Message: fmt.Sprintf("%s is %s; traffic from it would be refused before anything answers.", l.Network, l.What)})
	}
	trusted := s.trustedFor(sp)
	p.Connections = countConnections(ctx, &ConnectionImpact{Sample: []string{}}, func(e ctEntry) bool {
		for _, t := range trusted {
			if t.Contains(e.Src) {
				return false
			}
		}
		for _, n := range nets {
			if n.Contains(e.Src) {
				return true
			}
		}
		return false
	})
	if p.Connections.Tracked > 0 {
		p.Impacts = append(p.Impacts, Impact{Severity: "info", Kind: "connections", Message: fmt.Sprintf("%d connections from %d of these sources are open now. They continue: a list refuses new connections only. Ending them is a separate, explicit action.", p.Connections.Tracked, p.Connections.Sources)})
	}
	if p.Coverage.IPv4Share > 0.001 {
		p.Impacts = append(p.Impacts, Impact{Severity: "info", Kind: "coverage", Message: fmt.Sprintf("It covers %.2f%% of the IPv4 address space.", p.Coverage.IPv4Share*100)})
	}
	return p, nil
}

// localOverlaps are this host's own networks a list's networks touch.
func localOverlaps(ctx context.Context, sp *Spec, nets []netip.Prefix) []LocalOverlap {
	out := []LocalOverlap{}
	seen := map[string]bool{}
	note := func(p netip.Prefix, what string) {
		for _, n := range nets {
			if n.Overlaps(p) && !seen[p.String()+what] {
				seen[p.String()+what] = true
				out = append(out, LocalOverlap{Network: p.String(), What: what})
				return
			}
		}
	}
	if raw, err := run(ctx, "ip", "-j", "addr", "show"); err == nil {
		var addrs []ipAddr
		if json.Unmarshal([]byte(raw), &addrs) == nil {
			for _, a := range addrs {
				if a.IfName == "lo" {
					continue
				}
				for _, info := range a.AddrInfo {
					if addr, err := netip.ParseAddr(info.Local); err == nil && info.Scope == "global" {
						note(netip.PrefixFrom(addr, info.PrefixLen).Masked(), "the network of "+a.IfName)
					}
				}
			}
		}
	}
	for _, n := range sp.NAT {
		if p, err := ParsePrefix(n.Source); err == nil {
			note(p.Masked(), "the network NAT entry "+strconv.Quote(n.Name)+" shares out")
		}
	}
	return out
}

// SessionRequest names the network whose open sessions end, and the list
// that already refuses its new connections.
type SessionRequest struct {
	Network     string `json:"network"`
	BlocklistID int    `json:"blocklistId"`
}

// SessionPreview is what a revocation would end.
type SessionPreview struct {
	Network     string            `json:"network"`
	Blocklist   string            `json:"blocklist"`
	Connections *ConnectionImpact `json:"connections"`
	Refused     string            `json:"refused,omitempty"`
	Basis       string            `json:"basis"`
}

// SessionResult is what a revocation ended.
type SessionResult struct {
	Network   string `json:"network"`
	Matched   int    `json:"matched"`
	Ended     int    `json:"ended"`
	Failed    int    `json:"failed"`
	Truncated bool   `json:"truncated"`
	Error     string `json:"error,omitempty"`
	Basis     string `json:"basis"`
}

const sessionBasis = "Ending a session removes its connection-tracking entry. Its next packet is then judged as a new connection, which the list drops, so the session stalls and the endpoints time out. Sockets on this server close on their own timeouts."

// maxRevoked bounds one revocation's deletions.
const maxRevoked = 20_000

// sessionTargets validates a revocation and returns the network and its list.
func (s *Service) sessionTargets(ctx context.Context, req SessionRequest, client string) (netip.Prefix, BlocklistSpec, error) {
	p, err := ParsePrefix(req.Network)
	if err != nil {
		return p, BlocklistSpec{}, err
	}
	p = p.Masked()
	if err := exceptionWidth(p); err != nil {
		return p, BlocklistSpec{}, err
	}
	sp, err := s.loadSpec()
	if err != nil {
		return p, BlocklistSpec{}, err
	}
	var bl *BlocklistSpec
	for i := range sp.Blocklists {
		if sp.Blocklists[i].ID == req.BlocklistID {
			bl = &sp.Blocklists[i]
		}
	}
	if bl == nil {
		return p, BlocklistSpec{}, fmt.Errorf("blocklist %d: %w", req.BlocklistID, ErrNotFound)
	}
	if !bl.Enabled {
		return p, *bl, errors.New("the list is switched off, so ended sessions would simply connect again; switch it on first")
	}
	nets, health := blocklistData(filepath.Join(s.paths.Dir, "lists"), *bl)
	if health.Status != "ready" {
		return p, *bl, errors.New("the list's data is not readable, so it cannot be shown to cover this network")
	}
	inside := false
	for _, n := range nets {
		if n.Bits() <= p.Bits() && n.Contains(p.Addr()) {
			inside = true
		}
	}
	if !inside {
		return p, *bl, fmt.Errorf("%s is not inside %q, so its sessions would connect again at once; add it to a list first", p, bl.Name)
	}
	if rt := readBlocklistRuntime(ctx, bl.ID); rt.Status != "present" {
		return p, *bl, fmt.Errorf("%q is not loaded in the kernel (%s), so ended sessions would connect again", bl.Name, rt.Status)
	}
	if addr, err := ParseAddr(client); err == nil && p.Contains(addr) {
		return p, *bl, guarded("%s contains your own address (%s); ending its sessions would end the one this page is on.", p, addr)
	}
	for _, t := range s.trustedFor(sp) {
		if t.Overlaps(p) {
			return p, *bl, guarded("%s overlaps the trusted %s, whose connections the lists never refuse; ending them would only make them reconnect, the operator's included.", p, t)
		}
	}
	for _, a := range hostAddresses(ctx) {
		if p.Contains(a.Addr) {
			return p, *bl, guarded("%s contains this server's own address %s.", p, a.Addr)
		}
	}
	return p, *bl, nil
}

// PreviewSessions counts the sessions a revocation would end.
func (s *Service) PreviewSessions(ctx context.Context, req SessionRequest, client string) (*SessionPreview, error) {
	p, bl, err := s.sessionTargets(ctx, req, client)
	view := &SessionPreview{Network: p.String(), Blocklist: bl.Name, Basis: sessionBasis}
	if err != nil {
		var guard *GuardError
		if !errors.As(err, &guard) {
			return nil, err
		}
		view.Refused = guard.Reason
	}
	view.Connections = countConnections(ctx, &ConnectionImpact{Sample: []string{}}, func(e ctEntry) bool { return p.Contains(e.Src) })
	return view, nil
}

// RevokeSessions ends the tracked connections that came from a network a
// loaded list already refuses. It is not undoable and changes no
// configuration: the journal does not cover it, and the API requires the
// destructive capability for it.
func (s *Service) RevokeSessions(ctx context.Context, req SessionRequest, client string) (*SessionResult, error) {
	p, _, err := s.sessionTargets(ctx, req, client)
	if err != nil {
		return nil, err
	}
	res := &SessionResult{Network: p.String(), Basis: sessionBasis}
	var matched []ctEntry
	_, truncated, err := conntrackDump(ctx, maxPreviewConnections, func(e ctEntry) bool {
		if p.Contains(e.Src) {
			matched = append(matched, e)
		}
		return len(matched) < maxRevoked
	})
	res.Truncated = truncated || len(matched) >= maxRevoked
	if err != nil {
		return nil, fmt.Errorf("reading the connection table: %w", err)
	}
	res.Matched = len(matched)
	for _, e := range matched {
		if ctx.Err() != nil {
			res.Error = ctx.Err().Error()
			break
		}
		if err := conntrackDelete(ctx, e); err != nil {
			res.Failed++
			if res.Error == "" {
				res.Error = err.Error()
			}
			continue
		}
		res.Ended++
	}
	return res, nil
}

// PressureCount is one bucket of a breakdown.
type PressureCount struct {
	Key   string  `json:"key"`
	Count int     `json:"count"`
	Share float64 `json:"share"`
}

// ConntrackBreakdown is what the table holds, bounded.
type ConntrackBreakdown struct {
	Read       int             `json:"read"`
	Truncated  bool            `json:"truncated"`
	ByProtocol []PressureCount `json:"byProtocol"`
	ByState    []PressureCount `json:"byState"`
	TopSources []PressureCount `json:"topSources"`
	TopPorts   []PressureCount `json:"topPorts"`
	Unreplied  int             `json:"unreplied"`
	Assured    int             `json:"assured"`
	Error      string          `json:"error,omitempty"`
}

// PressureCause is one indication of why the table is filling.
type PressureCause struct {
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Evidence string `json:"evidence"`
}

// LimitCapacity is how close a limit's ceilings are to their open counts.
type LimitCapacity struct {
	ID                int             `json:"id"`
	Name              string          `json:"name"`
	Ports             string          `json:"ports"`
	MaxConnections    int             `json:"maxConnections"`
	GlobalConnections int             `json:"globalConnections"`
	Open              int             `json:"open"`
	TopSources        []PressureCount `json:"topSources"`
	Refused           []SeriesPoint   `json:"refused"`
	Meters            *LimitMeters    `json:"meters,omitempty"`
}

// PressureView is the connection table's pressure and its likely causes.
type PressureView struct {
	Conntrack  Conntrack                `json:"conntrack"`
	Stats      *ctStats                 `json:"stats"`
	StatsError string                   `json:"statsError,omitempty"`
	Breakdown  ConntrackBreakdown       `json:"breakdown"`
	Series     map[string][]SeriesPoint `json:"series"`
	Since      time.Time                `json:"since"`
	Causes     []PressureCause          `json:"causes"`
	Limits     []LimitCapacity          `json:"limits"`
	CheckedAt  time.Time                `json:"checkedAt"`
	Basis      string                   `json:"basis"`
}

const pressureBasis = "Read from the kernel's connection table over netlink, bounded to the first entries returned. Causes are indications drawn from the shares below, not a diagnosis."

// maxPressureEntries bounds the breakdown read.
const maxPressureEntries = 200_000

// Pressure reads the table's breakdown, statistics, recorded history and
// per-limit capacity.
func (s *Service) Pressure(ctx context.Context) (*PressureView, error) {
	sp, err := s.loadSpec()
	if err != nil {
		return nil, err
	}
	now := gatewayNow().UTC()
	v := &PressureView{Conntrack: readConntrack(), Series: map[string][]SeriesPoint{}, Since: now.Add(-24 * time.Hour), Causes: []PressureCause{}, Limits: []LimitCapacity{}, CheckedAt: now, Basis: pressureBasis}
	if stats, err := conntrackStats(ctx); err == nil {
		v.Stats = &stats
	} else {
		v.StatsError = err.Error()
	}
	type limitCount struct {
		spec    LimitSpec
		open    int
		sources map[netip.Addr]int
	}
	var limits []*limitCount
	for _, l := range sp.Limits {
		if l.Enabled && (l.MaxConnections > 0 || l.GlobalConnections > 0) {
			limits = append(limits, &limitCount{spec: l, sources: map[netip.Addr]int{}})
		}
	}
	protocols, states, sources, ports := map[string]int{}, map[string]int{}, map[netip.Addr]int{}, map[string]int{}
	b := &v.Breakdown
	read, truncated, err := conntrackDump(ctx, maxPressureEntries, func(e ctEntry) bool {
		protocols[protoName(e.Proto)]++
		state := e.state()
		if e.HasTCP {
			states["tcp "+state]++
		} else {
			states[protoName(e.Proto)+" "+state]++
		}
		if e.Status&ctStatusSeenReply == 0 {
			b.Unreplied++
		}
		if e.Status&ctStatusAssured != 0 {
			b.Assured++
		}
		sources[e.Src]++
		if e.DPort != 0 {
			ports[fmt.Sprintf("%d/%s", e.DPort, protoName(e.Proto))]++
		}
		for _, l := range limits {
			lo, hi := portBounds(l.spec.Ports)
			if int(e.DPort) >= lo && int(e.DPort) <= hi && protocolsOverlap(l.spec.Protocol, protoName(e.Proto)) && (!e.HasTCP || e.TCPState <= 3) {
				l.open++
				l.sources[e.Src]++
			}
		}
		return true
	})
	b.Read, b.Truncated = read, truncated
	if err != nil {
		b.Error = err.Error()
	}
	b.ByProtocol = topCounts(protocols, read, 8)
	b.ByState = topCounts(states, read, 12)
	b.TopPorts = topCounts(ports, read, 8)
	bySource := map[string]int{}
	for a, n := range sources {
		bySource[a.String()] = n
	}
	b.TopSources = topCounts(bySource, read, 10)
	keys := []string{"conntrack:count", "conntrack:max", "conntrack:drop", "conntrack:early_drop", "conntrack:insert_failed", "conntrack:error"}
	for _, l := range limits {
		keys = append(keys, limitKey(l.spec.ID), "limit-global:"+strconv.Itoa(l.spec.ID))
	}
	series, err := s.telemetry.series(ctx, keys, v.Since)
	if err == nil {
		for _, k := range keys[:6] {
			if pts := series[k]; len(pts) > 0 {
				v.Series[k] = pts
			}
		}
	}
	for _, l := range limits {
		c := LimitCapacity{ID: l.spec.ID, Name: l.spec.Name, Ports: l.spec.Ports, MaxConnections: l.spec.MaxConnections, GlobalConnections: l.spec.GlobalConnections, Open: l.open, Refused: []SeriesPoint{}}
		top := map[string]int{}
		for a, n := range l.sources {
			top[a.String()] = n
		}
		c.TopSources = topCounts(top, l.open, 5)
		c.Refused = mergeSeries(series[limitKey(l.spec.ID)], series["limit-global:"+strconv.Itoa(l.spec.ID)])
		if l.spec.MaxConnections > 0 {
			m := readLimitMeters(ctx, l.spec)
			c.Meters = &m
		}
		v.Limits = append(v.Limits, c)
	}
	v.Causes = pressureCauses(v)
	return v, nil
}

func topCounts(m map[string]int, total, n int) []PressureCount {
	out := make([]PressureCount, 0, len(m))
	for k, c := range m {
		share := 0.0
		if total > 0 {
			share = float64(c) / float64(total)
		}
		out = append(out, PressureCount{Key: k, Count: c, Share: share})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Key < out[j].Key
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// mergeSeries adds two series point by point.
func mergeSeries(a, b []SeriesPoint) []SeriesPoint {
	by := map[int64]SeriesPoint{}
	for _, p := range append(append([]SeriesPoint{}, a...), b...) {
		q := by[p.TS]
		q.TS, q.Value, q.Bytes = p.TS, q.Value+p.Value, q.Bytes+p.Bytes
		by[p.TS] = q
	}
	out := make([]SeriesPoint, 0, len(by))
	for _, p := range by {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TS < out[j].TS })
	return out
}

// pressureCauses draws the indications the breakdown supports.
func pressureCauses(v *PressureView) []PressureCause {
	causes := []PressureCause{}
	add := func(kind, severity, message, evidence string) {
		causes = append(causes, PressureCause{Kind: kind, Severity: severity, Message: message, Evidence: evidence})
	}
	c := v.Conntrack
	if c.Available && c.Percent >= conntrackWarning {
		add("fullness", "warning", "The table is close to full; at its maximum the kernel drops new connections.", fmt.Sprintf("%d of %d entries (%.0f%%)", c.Count, c.Max, c.Percent))
	}
	recent := func(key string) uint64 {
		var n uint64
		cutoff := v.CheckedAt.Add(-time.Hour).Unix()
		for _, p := range v.Series[key] {
			if p.TS >= cutoff {
				n += p.Value
			}
		}
		return n
	}
	if drop, early, failed := recent("conntrack:drop"), recent("conntrack:early_drop"), recent("conntrack:insert_failed"); drop+early+failed > 0 {
		add("drops", "warning", "The kernel refused or evicted connections in the last hour because the table was full or contended.", fmt.Sprintf("%d dropped, %d early-dropped, %d failed inserts", drop, early, failed))
	}
	b := v.Breakdown
	if b.Read < 50 {
		return causes
	}
	share := func(list []PressureCount, key string) (float64, int) {
		for _, p := range list {
			if p.Key == key {
				return p.Share, p.Count
			}
		}
		return 0, 0
	}
	if s, n := share(b.ByState, "tcp SYN_RECV"); s >= 0.2 {
		add("syn", "warning", "Half-open TCP connections are a large share: the pattern of a SYN flood, or of many clients that never complete their handshake.", fmt.Sprintf("%d entries in SYN_RECV (%.0f%%)", n, s*100))
	}
	tw, twn := share(b.ByState, "tcp TIME_WAIT")
	cl, cln := share(b.ByState, "tcp CLOSE")
	if tw+cl >= 0.5 {
		add("churn", "info", "Most entries are connections already closing: many short connections (an API, a health check, a crawler) rather than many open ones.", fmt.Sprintf("%d TIME_WAIT and %d CLOSE (%.0f%%)", twn, cln, (tw+cl)*100))
	}
	if b.Read > 0 && float64(b.Unreplied)/float64(b.Read) >= 0.4 {
		add("unreplied", "warning", "Many flows never got a reply: a scan, a flood of spoofed sources, or this server's own requests to something unreachable.", fmt.Sprintf("%d of %d entries unreplied", b.Unreplied, b.Read))
	}
	if len(b.TopSources) > 0 && b.TopSources[0].Share >= 0.25 && b.TopSources[0].Count >= 100 {
		top := b.TopSources[0]
		add("source", "warning", "One source holds a large share of the table: a single client flooding, or many clients behind one address.", fmt.Sprintf("%s holds %d entries (%.0f%%)", top.Key, top.Count, top.Share*100))
	}
	if len(b.TopPorts) > 0 && b.TopPorts[0].Share >= 0.5 {
		top := b.TopPorts[0]
		add("port", "info", "One destination port dominates the table.", fmt.Sprintf("%s: %d entries (%.0f%%)", top.Key, top.Count, top.Share*100))
	}
	return causes
}
