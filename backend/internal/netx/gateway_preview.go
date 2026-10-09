package netx

import (
	"context"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// Change previews for forwards and NAT entries.
//
// A preview runs the same validation and host checks as a save, without the
// lock, the journal or any host change, and adds what a save does not
// refuse but the operator should know: overlapping entries where the first
// rule silently wins, local services a forward would take a port from, the
// limits and blocklists that will also judge the translated connections,
// the connections already riding an entry that a change leaves on their old
// translation, and the modeled verdicts of the host's other chains.

// Impact is one consequence of a proposed change.
type Impact struct {
	// Severity is refused (the save would be refused), warning or info.
	Severity string `json:"severity"`
	Kind     string `json:"kind"`
	Message  string `json:"message"`
	Entry    string `json:"entry,omitempty"`
}

// ConnectionImpact counts tracked connections a change concerns.
type ConnectionImpact struct {
	Tracked   int      `json:"tracked"`
	Sources   int      `json:"sources"`
	Sample    []string `json:"sample"`
	Truncated bool     `json:"truncated"`
	Error     string   `json:"error,omitempty"`
}

// GatewayPreview is what a proposed forward or NAT entry would do.
type GatewayPreview struct {
	Valid       bool              `json:"valid"`
	Error       string            `json:"error,omitempty"`
	Forward     *ForwardView      `json:"forward,omitempty"`
	NAT         *NATView          `json:"nat,omitempty"`
	Impacts     []Impact          `json:"impacts"`
	Connections *ConnectionImpact `json:"connections,omitempty"`
	Flows       []EntryFlow       `json:"flows"`
}

// GatewayPreviewRequest names the entry and its proposed body.
type GatewayPreviewRequest struct {
	Kind    string          `json:"kind"`
	ID      int             `json:"id"`
	Forward *ForwardRequest `json:"forward"`
	NAT     *NATRequest     `json:"nat"`
}

// maxPreviewConnections bounds the conntrack read behind a preview.
const maxPreviewConnections = 200_000

// PreviewGateway reports a proposed change's consequences without making it.
func (s *Service) PreviewGateway(ctx context.Context, req GatewayPreviewRequest, client string, protected []int) (*GatewayPreview, error) {
	sp, err := s.loadSpec()
	if err != nil {
		return nil, err
	}
	switch req.Kind {
	case "forward":
		if req.Forward == nil {
			return nil, fmt.Errorf("a forward preview needs the forward")
		}
		return s.previewForward(ctx, sp, req.ID, *req.Forward, client, protected)
	case "nat":
		if req.NAT == nil {
			return nil, fmt.Errorf("a NAT preview needs the entry")
		}
		return s.previewNAT(ctx, sp, req.ID, *req.NAT, client)
	}
	return nil, fmt.Errorf("a preview is of a forward or a NAT entry")
}

func (s *Service) previewForward(ctx context.Context, sp *Spec, id int, req ForwardRequest, client string, protected []int) (*GatewayPreview, error) {
	p := &GatewayPreview{Impacts: []Impact{}, Flows: []EntryFlow{}}
	var old *ForwardSpec
	for i := range sp.Forwards {
		if sp.Forwards[i].ID == id {
			old = &sp.Forwards[i]
		}
	}
	if id != 0 && old == nil {
		return nil, fmt.Errorf("forward %d: %w", id, ErrNotFound)
	}
	f := ForwardSpec{Name: req.Name, Protocol: req.Protocol, Interface: req.Interface, Ports: req.Ports, Target: req.Target,
		TargetPort: req.TargetPort, Sources: req.Sources, SourceNAT: strings.TrimSpace(req.SourceNAT), Enabled: true}
	if f.SourceNAT == "" {
		f.SourceNAT = natAuto
	}
	if old != nil {
		f.ID, f.Enabled = old.ID, old.Enabled
	}
	if req.Enabled != nil {
		f.Enabled = *req.Enabled
	}
	if _, _, ok := splitNAT(f.SourceNAT); !ok || strings.Contains(f.SourceNAT, ":") {
		p.Error = "source translation is auto, always or never"
		return p, nil
	}
	f, err := normForward(f)
	if err != nil {
		p.Error = err.Error()
		return p, nil
	}
	if f.Interface != "" {
		if _, err := ifaceAddresses(ctx, f.Interface); err != nil {
			p.Error = err.Error()
			return p, nil
		}
	}
	p.Valid = true
	add := func(severity, kind, entry, format string, args ...any) {
		p.Impacts = append(p.Impacts, Impact{Severity: severity, Kind: kind, Entry: entry, Message: fmt.Sprintf(format, args...)})
	}
	for _, other := range sp.Forwards {
		if other.ID == f.ID {
			continue
		}
		entry := "forward:" + strconv.Itoa(other.ID)
		switch {
		case forwardsCollide(other, f):
			add("refused", "collision", entry, "%q already forwards %s/%s from the same sources on the same device; the save is refused.", other.Name, other.Ports, other.Protocol)
		case forwardsOverlap(other, f):
			add("warning", "overlap", entry, "%q also forwards %s/%s and can match the same visitors; whichever rule comes first in the table takes them (%q was made earlier, so it does).", other.Name, other.Ports, other.Protocol, earlier(other, f))
		}
	}
	if f.Enabled {
		if err := s.guardForward(ctx, f, client, protected); err != nil {
			add("refused", "guard", "", "%s", err.Error())
		}
		target, _ := ParseAddr(f.Target)
		if fam := familyDigit(target); !gatewayForwardingOn(fam) {
			add("refused", "forwarding", "", "IPv%s forwarding is off; the save is refused until it is turned on.", fam)
		}
		for _, l := range listenersOn(ctx, f) {
			add("warning", "listener", "", "%s listens on %s; new connections to port %s%s will go to %s instead of it. Connections already open stay with it.", l.process, l.local, f.Ports, onDevice(f.Interface), f.Target)
		}
		for _, l := range sp.Limits {
			if l.Enabled && protocolsOverlap(l.Protocol, f.Protocol) && portsOverlap(l.Ports, f.Ports) {
				add("info", "limit", "limit:"+strconv.Itoa(l.ID), "The limit %q (%s) also judges these connections after translation.", l.Name, l.Ports)
			}
		}
		lists := 0
		for _, bl := range sp.Blocklists {
			if bl.Enabled {
				lists++
			}
		}
		if lists > 0 {
			add("info", "blocklist", "", "%d enabled blocklists drop new connections from their networks before the forward sees them.", lists)
		}
		if family := familyOf(target); !admissionFamilies(sp)[family] {
			add("info", "admission", "", "This is the first %s translation: the owned admission rule is inserted at the top of FORWARD, INPUT and DOCKER-USER for that family.", map[string]string{"inet": "IPv4", "inet6": "IPv6"}[family])
		}
		if choice, _, _ := splitNAT(f.SourceNAT); choice == natAuto {
			decisions := autoNATDecisions(ctx, []ForwardSpec{f})
			if d, ok := decisions[f.ID]; ok && d.Current != nil {
				word := "keeps the visitor's address"
				if *d.Current {
					word = "is masqueraded"
				}
				add("info", "nat-decision", "", "Auto source translation: the flow %s. %s", word, d.Reason)
			}
		}
		if capability := s.GatewayCapability(ctx); capability.listing != nil {
			candidate := &Spec{Forwards: []ForwardSpec{f}}
			p.Flows = evaluateGatewayFlows(capability.listing, modelGatewayFlows(candidate, hostAddresses(ctx)))
			for _, flow := range p.Flows {
				if flow.Verdict == "blocked" || flow.Verdict == "unknown" {
					add("warning", "policy", "", "The host's other chains %s this flow in their supported forms; see the modeled layers.", map[string]string{"blocked": "drop", "unknown": "may drop"}[flow.Verdict])
				}
			}
		}
	} else if old != nil && old.Enabled {
		add("warning", "disable", "", "Switching it off stops new connections to port %s being passed on; connections already translated continue until they close.", old.Ports)
	}
	if old != nil && old.Enabled {
		changes := old.Target != f.Target || targetPort(*old) != targetPort(f) || old.Ports != f.Ports || old.Protocol != f.Protocol
		if changes || !f.Enabled {
			p.Connections = forwardConnections(ctx, *old)
			if p.Connections.Tracked > 0 && f.Enabled {
				add("info", "connections", "", "%d connections already translated to %s keep that translation until they close; only new ones follow the change.", p.Connections.Tracked, hostPortString(old.Target, targetPort(*old)))
			}
		}
	}
	view := forwardView(f, nil)
	if choice, _, _ := splitNAT(f.SourceNAT); choice == natAuto {
		if d, ok := autoNATDecisions(ctx, []ForwardSpec{f})[f.ID]; ok && d.Current != nil {
			view.Masquerade = *d.Current
		}
	}
	p.Forward = &view
	return p, nil
}

func (s *Service) previewNAT(ctx context.Context, sp *Spec, id int, req NATRequest, client string) (*GatewayPreview, error) {
	p := &GatewayPreview{Impacts: []Impact{}, Flows: []EntryFlow{}}
	var old *NATSpec
	for i := range sp.NAT {
		if sp.NAT[i].ID == id {
			old = &sp.NAT[i]
		}
	}
	if id != 0 && old == nil {
		return nil, fmt.Errorf("NAT entry %d: %w", id, ErrNotFound)
	}
	if old != nil && old.Owner != "" {
		p.Error = fmt.Sprintf("it belongs to %s, which keeps it in step with itself; change it there", old.Owner)
		return p, nil
	}
	n := natFromRequest(req)
	if old != nil {
		n.ID, n.Enabled = old.ID, old.Enabled
	}
	if req.Enabled != nil {
		n.Enabled = *req.Enabled
	}
	n, err := normNAT(n)
	if err != nil {
		p.Error = err.Error()
		return p, nil
	}
	p.Valid = true
	add := func(severity, kind, entry, format string, args ...any) {
		p.Impacts = append(p.Impacts, Impact{Severity: severity, Kind: kind, Entry: entry, Message: fmt.Sprintf(format, args...)})
	}
	if err := s.checkNATHost(ctx, n, client); err != nil {
		severity := "refused"
		add(severity, "host", "", "%s", err.Error())
	}
	src, _ := ParsePrefix(n.Source)
	for _, other := range sp.NAT {
		if other.ID == n.ID {
			continue
		}
		entry := "nat:" + strconv.Itoa(other.ID)
		if natsCollide(other, n) {
			add("refused", "collision", entry, "%q already translates %s out of %s; only the first rule would ever apply, so the save is refused.", other.Name, other.Source, other.Interface)
			continue
		}
		if o, err := ParsePrefix(other.Source); err == nil && o.Masked().Overlaps(src.Masked()) && other.Enabled {
			owner := ""
			if other.Owner != "" {
				owner = " (kept by " + other.Owner + ")"
			}
			add("warning", "overlap", entry, "%q%s translates %s out of %s, which overlaps this network.", other.Name, owner, other.Source, other.Interface)
		}
	}
	if n.Enabled {
		if fam := familyDigit(src.Addr()); !gatewayForwardingOn(fam) {
			add("refused", "forwarding", "", "IPv%s forwarding is off; the save is refused until it is turned on.", fam)
		}
		if natMapped(n) {
			to, _ := ParsePrefix(n.Translated)
			add("info", "inbound", "", "Every connection arriving on %s for %s is sent to %s, on every port: services of this server on that address stop answering it.", n.Interface, to, n.Source)
			if !to.IsSingleIP() {
				add("info", "routing", "", "The provider must route %s to this server; nothing here can check that.", to)
			}
			for _, f := range sp.Forwards {
				if f.Enabled && (f.Interface == "" || f.Interface == n.Interface) {
					add("warning", "forward-precedence", "forward:"+strconv.Itoa(f.ID), "The forward %q on port %s comes first: connections to that port on %s go to %s, not through this mapping.", f.Name, f.Ports, to, f.Target)
				}
			}
			for _, l := range listenersAt(ctx, to) {
				add("warning", "listener", "", "%s listens on %s; new connections to it on %s will be mapped to %s instead.", l.process, l.local, to, n.Source)
			}
			if n.Mode == natNPTv6 {
				add("info", "nptv6", "", "This is nftables' stateful prefix translation (netmap): connections are tracked, and checksums are recalculated rather than kept neutral as RFC 6296 describes.")
			}
		}
		if capability := s.GatewayCapability(ctx); capability.listing != nil {
			candidate := &Spec{NAT: []NATSpec{n}}
			p.Flows = evaluateGatewayFlows(capability.listing, modelGatewayFlows(candidate, hostAddresses(ctx)))
		}
	} else if old != nil && old.Enabled {
		add("warning", "disable", "", "Switching it off stops translating %s out of %s for new connections; connections already translated continue until they close.", old.Source, old.Interface)
	}
	if old != nil && old.Enabled {
		p.Connections = natConnections(ctx, *old)
	}
	view := natView(n, nil)
	p.NAT = &view
	return p, nil
}

// forwardsOverlap reports two enabled forwards that can claim the same
// visitor without being the exact collision a save refuses.
func forwardsOverlap(a, b ForwardSpec) bool {
	if !a.Enabled || !b.Enabled || !protocolsOverlap(a.Protocol, b.Protocol) || !portsOverlap(a.Ports, b.Ports) {
		return false
	}
	if a.Interface != "" && b.Interface != "" && a.Interface != b.Interface {
		return false
	}
	if len(a.Sources) == 0 || len(b.Sources) == 0 {
		return true
	}
	return destinationsOverlap(a.Sources, b.Sources)
}

func earlier(a, b ForwardSpec) string {
	if b.ID == 0 || a.ID < b.ID {
		return a.Name
	}
	return b.Name
}

func protocolsOverlap(a, b string) bool { return a == b || a == "both" || b == "both" }

func portsOverlap(a, b string) bool {
	alo, ahi := portBounds(a)
	blo, bhi := portBounds(b)
	return alo <= bhi && blo <= ahi
}

func onDevice(iface string) string {
	if iface == "" {
		return ""
	}
	return " on " + iface
}

func hostPortString(addr, port string) string {
	if strings.Contains(addr, ":") {
		return "[" + addr + "]:" + port
	}
	return addr + ":" + port
}

type listener struct {
	local   string
	process string
	addr    netip.AddrPort
	proto   string
}

// readListeners lists this host's listening TCP and bound UDP sockets.
func readListeners(ctx context.Context) []listener {
	out, err := run(ctx, "ss", "-Hlntup")
	if err != nil {
		return nil
	}
	return parseListeners(out)
}

func parseListeners(out string) []listener {
	var ls []listener
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 {
			continue
		}
		ap, ok := parseSSEndpoint(f[4])
		if !ok {
			continue
		}
		process := "a process"
		if m := ssUser.FindStringSubmatch(line); m != nil {
			process = m[1]
		}
		local := f[4]
		ls = append(ls, listener{local: local, process: process, addr: ap, proto: f[0]})
	}
	return ls
}

// listenersOn are the local services on a forward's public ports that the
// forward's arrival device reaches.
func listenersOn(ctx context.Context, f ForwardSpec) []listener {
	var device []netip.Addr
	if f.Interface != "" {
		device, _ = ifaceAddresses(ctx, f.Interface)
	}
	lo, hi := portBounds(f.Ports)
	var out []listener
	for _, l := range readListeners(ctx) {
		if !protocolsOverlap(l.proto, f.Protocol) || int(l.addr.Port()) < lo || int(l.addr.Port()) > hi || l.addr.Addr().IsLoopback() {
			continue
		}
		if f.Interface != "" && !l.addr.Addr().IsUnspecified() {
			on := false
			for _, a := range device {
				on = on || a == l.addr.Addr()
			}
			if !on {
				continue
			}
		}
		out = append(out, l)
	}
	return out
}

// listenersAt are local services bound to an address a mapping takes.
func listenersAt(ctx context.Context, to netip.Prefix) []listener {
	var out []listener
	for _, l := range readListeners(ctx) {
		if a := l.addr.Addr(); !a.IsLoopback() && (a.IsUnspecified() && a.Is4() == to.Addr().Is4() || to.Contains(a)) {
			out = append(out, l)
		}
	}
	return out
}

// forwardConnections counts the tracked connections a forward translated:
// marked, destination-translated, answered from its target and port.
func forwardConnections(ctx context.Context, f ForwardSpec) *ConnectionImpact {
	target, err := ParseAddr(f.Target)
	impact := &ConnectionImpact{Sample: []string{}}
	if err != nil {
		impact.Error = err.Error()
		return impact
	}
	lo, hi := portBounds(targetPort(f))
	if f.TargetPort == "" {
		lo, hi = portBounds(f.Ports)
	}
	return countConnections(ctx, impact, func(e ctEntry) bool {
		return e.Status&ctStatusDstNAT != 0 && e.Mark&0xff000000 == 0x4a000000 && e.ReplySrc == target &&
			int(e.ReplySPort) >= lo && int(e.ReplySPort) <= hi
	})
}

// natConnections counts the tracked connections a NAT entry translated.
func natConnections(ctx context.Context, n NATSpec) *ConnectionImpact {
	src, err := ParsePrefix(n.Source)
	impact := &ConnectionImpact{Sample: []string{}}
	if err != nil {
		impact.Error = err.Error()
		return impact
	}
	return countConnections(ctx, impact, func(e ctEntry) bool {
		return e.Mark&0xff000000 == 0x4a000000 && (src.Masked().Contains(e.Src) || natMapped(n) && e.Status&ctStatusDstNAT != 0 && src.Masked().Contains(e.ReplySrc))
	})
}

func countConnections(ctx context.Context, impact *ConnectionImpact, keep func(ctEntry) bool) *ConnectionImpact {
	sources := map[netip.Addr]bool{}
	_, truncated, err := conntrackDump(ctx, maxPreviewConnections, func(e ctEntry) bool {
		if keep(e) {
			impact.Tracked++
			if !sources[e.Src] {
				sources[e.Src] = true
				if len(impact.Sample) < 5 {
					impact.Sample = append(impact.Sample, e.Src.String())
				}
			}
		}
		return true
	})
	impact.Sources, impact.Truncated = len(sources), truncated
	if err != nil {
		impact.Error = "The connection table could not be read: " + err.Error()
	}
	return impact
}
