package netx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"
)

// maxPortVLANs bounds one port's membership list, and maxRemotes a VXLAN's
// flood list; both are rendered into the boot unit a line each.
const (
	maxPortVLANs = 64
	maxRemotes   = 64
)

// defaultPortVLANs is what the kernel gives every port of a VLAN-filtering
// bridge: VLAN 1, its native VLAN, sent untagged.
var defaultPortVLANs = []PortVLAN{{VID: 1, PVID: true, Untagged: true}}

// effectiveVLANs is the membership a managed port holds: its own list, or
// the kernel's default where it has none.
func effectiveVLANs(l LinkSpec) []PortVLAN {
	if len(l.VLANs) == 0 {
		return defaultPortVLANs
	}
	return l.VLANs
}

// cleanPortVLANs validates a membership list and returns it sorted by VLAN.
func cleanPortVLANs(in []PortVLAN) ([]PortVLAN, error) {
	if len(in) > maxPortVLANs {
		return nil, fmt.Errorf("a port carries at most %d VLANs here", maxPortVLANs)
	}
	seen := map[int]bool{}
	pvid := 0
	out := make([]PortVLAN, 0, len(in))
	for _, v := range in {
		if v.VID < 1 || v.VID > 4094 {
			return nil, fmt.Errorf("a VLAN id is 1 to 4094")
		}
		if seen[v.VID] {
			return nil, fmt.Errorf("VLAN %d is listed twice", v.VID)
		}
		seen[v.VID] = true
		if v.PVID {
			pvid++
		}
		out = append(out, v)
	}
	if pvid > 1 {
		return nil, fmt.Errorf("a port has one native VLAN (PVID)")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].VID < out[j].VID })
	return out, nil
}

// vlanArgs is `bridge vlan add|del` for one membership, the arguments after
// "bridge". self addresses the bridge's own membership rather than a port's.
func vlanArgs(verb, dev string, v PortVLAN, self bool) []string {
	args := []string{"vlan", verb, "dev", dev, "vid", strconv.Itoa(v.VID)}
	if verb == "add" {
		if v.PVID {
			args = append(args, "pvid")
		}
		if v.Untagged {
			args = append(args, "untagged")
		}
	}
	if self {
		args = append(args, "self")
	}
	return args
}

// fdbArgs is `bridge fdb append|del` for one VXLAN flood destination.
func fdbArgs(verb, dev, dst string) []string {
	return []string{"fdb", verb, "00:00:00:00:00:00", "dev", dev, "dst", dst}
}

// vlanPort reports whether a managed device's VLAN list applies, and whether
// it is the bridge's own (self) membership: a VLAN-filtering bridge the
// dashboard made, or a port of one.
func vlanPort(sp *Spec, l LinkSpec) (self, ok bool) {
	if l.Kind == "bridge" {
		return true, l.VLANFiltering
	}
	if l.Master == "" {
		return false, false
	}
	bridge, found := sp.link(l.Master)
	return false, found && bridge.Kind == "bridge" && bridge.VLANFiltering
}

// bridgeUnitCommands are the boot unit's bridge lines for a spec: each
// managed port's VLAN memberships (VLAN 1 removed where the list leaves it
// out), and each unicast VXLAN's further flood destinations. Every value is
// validated again on the way out, as batchLines does.
func bridgeUnitCommands(sp *Spec) [][]string {
	var out [][]string
	for _, l := range sp.Links {
		if ValidIfName(l.Name) != nil {
			continue
		}
		if self, ok := vlanPort(sp, l); ok && len(l.VLANs) > 0 {
			vlans, err := cleanPortVLANs(l.VLANs)
			if err != nil {
				continue
			}
			keepsDefault := false
			for _, v := range vlans {
				out = append(out, vlanArgs("add", l.Name, v, self))
				keepsDefault = keepsDefault || v.VID == 1
			}
			if !keepsDefault {
				out = append(out, vlanArgs("del", l.Name, PortVLAN{VID: 1}, self))
			}
		}
		if l.Kind == "vxlan" && l.Remote != "" {
			for _, raw := range l.Remotes {
				if a, err := ParseAddr(raw); err == nil {
					out = append(out, fdbArgs("append", l.Name, a.String()))
				}
			}
		}
	}
	return out
}

// vlanTransition is the bridge commands that take a device from one
// membership to another: every wanted VLAN added (which also moves the PVID
// and untagged flags), then every unwanted one removed.
func vlanTransition(dev string, from, to []PortVLAN, self bool) [][]string {
	var out [][]string
	want := map[int]bool{}
	for _, v := range to {
		want[v.VID] = true
		out = append(out, vlanArgs("add", dev, v, self))
	}
	for _, v := range from {
		if !want[v.VID] {
			out = append(out, vlanArgs("del", dev, v, self))
		}
	}
	return out
}

// BridgeView is a bridge read as a switch: its settings, what each port
// carries, and the forwarding database it has learned.
type BridgeView struct {
	Name      string    `json:"name"`
	Managed   bool      `json:"managed"`
	CheckedAt time.Time `json:"checkedAt"`

	STP               bool    `json:"stp"`
	VLANFiltering     bool    `json:"vlanFiltering"`
	MulticastSnooping bool    `json:"multicastSnooping"`
	DefaultPVID       int     `json:"defaultPvid"`
	AgeingSeconds     int     `json:"ageingSeconds"`
	VLANProtocol      string  `json:"vlanProtocol,omitempty"`
	SettingsRead      Reading `json:"settingsRead"`

	Ports     []BridgePort `json:"ports"`
	VLANsRead Reading      `json:"vlansRead"`

	// FDB is bounded to the first fdbShown learned or configured entries;
	// FDBTotal counts them all. The bridge's own multicast entries are left
	// out.
	FDB      []FDBEntry `json:"fdb"`
	FDBTotal int        `json:"fdbTotal"`
	FDBRead  Reading    `json:"fdbRead"`
}

// BridgePort is one port, or the bridge itself (Self), with the VLANs the
// kernel holds for it and, for a managed device, the VLANs the spec wants.
type BridgePort struct {
	Name    string     `json:"name"`
	Self    bool       `json:"self"`
	Managed bool       `json:"managed"`
	VLANs   []PortVLAN `json:"vlans"`
	// Desired is the managed list; nil where the dashboard does not own the
	// port's membership.
	Desired []PortVLAN `json:"desired,omitempty"`
}

// FDBEntry is one forwarding-database entry.
type FDBEntry struct {
	MAC   string `json:"mac"`
	Port  string `json:"port"`
	VLAN  int    `json:"vlan,omitempty"`
	State string `json:"state,omitempty"`
	// Dst is a VXLAN entry's remote end.
	Dst string `json:"dst,omitempty"`
	// Static marks an entry somebody configured rather than one learned.
	Static bool `json:"static"`
}

const fdbShown = 256

// Bridge reads one bridge's settings, port VLANs and forwarding database.
func (s *Service) Bridge(ctx context.Context, name string) (*BridgeView, error) {
	if err := ValidIfName(name); err != nil {
		return nil, err
	}
	linkOut, err := run(ctx, "ip", "-j", "-d", "link", "show")
	if err != nil {
		return nil, err
	}
	links, err := parseLinks(linkOut, "[]")
	if err != nil {
		return nil, err
	}
	var bridge *Link
	ports := []string{}
	for i := range links {
		if links[i].Name == name {
			bridge = &links[i]
		}
		if links[i].Master == name {
			ports = append(ports, links[i].Name)
		}
	}
	if bridge == nil {
		return nil, fmt.Errorf("%s: %w", name, ErrNotFound)
	}
	if bridge.Kind != "bridge" {
		return nil, fmt.Errorf("%s is not a bridge", name)
	}
	sp, err := s.loadSpec()
	if err != nil {
		sp = emptySpec()
	}
	v := &BridgeView{Name: name, CheckedAt: time.Now().UTC(), Ports: []BridgePort{}, FDB: []FDBEntry{}}
	_, v.Managed = sp.link(name)
	v.SettingsRead = readBridgeSettings(linkOut, name, v)

	if out, err := run(ctx, "bridge", "-j", "vlan", "show"); err != nil {
		v.VLANsRead = readingOf(err)
	} else if held, err := parseBridgeVLANs(out); err != nil {
		v.VLANsRead = Reading{State: "failed", Reason: err.Error()}
	} else {
		v.VLANsRead = readingOK()
		sort.Strings(ports)
		for _, port := range append([]string{name}, ports...) {
			p := BridgePort{Name: port, Self: port == name, VLANs: held[port]}
			if p.VLANs == nil {
				p.VLANs = []PortVLAN{}
			}
			if m, ok := sp.link(port); ok {
				p.Managed = true
				if _, applies := vlanPort(sp, *m); applies {
					p.Desired = effectiveVLANs(*m)
				}
			}
			v.Ports = append(v.Ports, p)
		}
	}

	if out, err := run(ctx, "bridge", "-j", "fdb", "show", "br", name); err != nil {
		v.FDBRead = readingOf(err)
	} else if entries, err := parseFDB(out); err != nil {
		v.FDBRead = Reading{State: "failed", Reason: err.Error()}
	} else {
		v.FDBRead, v.FDBTotal = readingOK(), len(entries)
		if len(entries) > fdbShown {
			entries = entries[:fdbShown]
		}
		v.FDB = entries
	}
	return v, nil
}

// readBridgeSettings fills a view from the bridge's info_data in `ip -j -d
// link show`.
func readBridgeSettings(linkOut, name string, v *BridgeView) Reading {
	var raw []struct {
		IfName   string `json:"ifname"`
		LinkInfo struct {
			InfoData struct {
				STP           *int   `json:"stp_state"`
				VLANFiltering *int   `json:"vlan_filtering"`
				Snooping      *int   `json:"mcast_snooping"`
				DefaultPVID   int    `json:"vlan_default_pvid"`
				Ageing        int    `json:"ageing_time"`
				VLANProtocol  string `json:"vlan_protocol"`
			} `json:"info_data"`
		} `json:"linkinfo"`
	}
	if err := json.Unmarshal([]byte(linkOut), &raw); err != nil {
		return Reading{State: "failed", Reason: "ip printed something unreadable"}
	}
	for _, l := range raw {
		if l.IfName != name {
			continue
		}
		d := l.LinkInfo.InfoData
		if d.STP == nil && d.VLANFiltering == nil {
			return Reading{State: "unavailable", Reason: "This ip does not print bridge settings."}
		}
		v.STP = d.STP != nil && *d.STP != 0
		v.VLANFiltering = d.VLANFiltering != nil && *d.VLANFiltering != 0
		v.MulticastSnooping = d.Snooping != nil && *d.Snooping != 0
		v.DefaultPVID, v.VLANProtocol = d.DefaultPVID, d.VLANProtocol
		// The kernel reports ageing in hundredths of a second.
		v.AgeingSeconds = d.Ageing / 100
		return readingOK()
	}
	return Reading{State: "failed", Reason: "the bridge left the listing while it was read"}
}

// parseBridgeVLANs reads `bridge -j vlan show` into each device's VLANs.
func parseBridgeVLANs(out string) (map[string][]PortVLAN, error) {
	out = strings.TrimSpace(out)
	held := map[string][]PortVLAN{}
	if out == "" {
		return held, nil
	}
	var raw []struct {
		IfName string `json:"ifname"`
		VLANs  []struct {
			VLAN    int      `json:"vlan"`
			VLANEnd int      `json:"vlanEnd"`
			Flags   []string `json:"flags"`
		} `json:"vlans"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("bridge printed something unreadable: %w", err)
	}
	for _, d := range raw {
		for _, v := range d.VLANs {
			last := v.VLAN
			if v.VLANEnd > v.VLAN {
				last = v.VLANEnd
			}
			for vid := v.VLAN; vid <= last && vid-v.VLAN < 4094; vid++ {
				pv := PortVLAN{VID: vid}
				for _, f := range v.Flags {
					switch f {
					case "PVID":
						pv.PVID = true
					case "Egress Untagged":
						pv.Untagged = true
					}
				}
				held[d.IfName] = append(held[d.IfName], pv)
			}
		}
	}
	return held, nil
}

// parseFDB reads `bridge -j fdb show`, dropping the bridge's own multicast
// entries, which every bridge has and nobody configured.
func parseFDB(out string) ([]FDBEntry, error) {
	out = strings.TrimSpace(out)
	entries := []FDBEntry{}
	if out == "" {
		return entries, nil
	}
	var raw []struct {
		MAC    string   `json:"mac"`
		IfName string   `json:"ifname"`
		VLAN   int      `json:"vlan"`
		Flags  []string `json:"flags"`
		State  string   `json:"state"`
		Dst    string   `json:"dst"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("bridge printed something unreadable: %w", err)
	}
	for _, e := range raw {
		self := false
		for _, f := range e.Flags {
			if f == "self" {
				self = true
			}
		}
		if self && e.Dst == "" && (strings.HasPrefix(e.MAC, "33:33:") || strings.HasPrefix(e.MAC, "01:00:5e:")) {
			continue
		}
		entries = append(entries, FDBEntry{
			MAC: e.MAC, Port: e.IfName, VLAN: e.VLAN, State: e.State, Dst: e.Dst,
			Static: e.State == "permanent" || e.State == "static",
		})
	}
	return entries, nil
}

// SetPortVLANs replaces the VLAN memberships of a managed port (or of a
// managed VLAN-filtering bridge's own interface). It is applied now, written
// into the boot unit, and taken back if the operator's path moves.
func (s *Service) SetPortVLANs(ctx context.Context, name string, vlans []PortVLAN, client, actor string) (*LinkChange, error) {
	if err := ValidIfName(name); err != nil {
		return nil, err
	}
	want, err := cleanPortVLANs(vlans)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, err := s.loadSpec()
	if err != nil {
		return nil, err
	}
	next := sp.clone()
	st, err := s.readLinkState(ctx, next, client)
	if err != nil {
		return nil, err
	}
	m, ok := next.link(name)
	if !ok {
		return nil, notMade(st, name)
	}
	self, applies := vlanPort(next, *m)
	if !applies {
		return nil, fmt.Errorf("%s is not on a VLAN-filtering bridge the dashboard made; VLANs are set on its ports", name)
	}
	bridgeName := m.Master
	if self {
		bridgeName = name
	}
	for _, dev := range []string{name, bridgeName} {
		if l, ok := st.by[dev]; ok {
			if what := st.carries(l); what != "" {
				return nil, guarded("%s carries %s, and changing which VLANs it passes could cut it off", dev, what)
			}
		}
	}
	out, err := run(ctx, "bridge", "-j", "vlan", "show")
	if err != nil {
		return nil, err
	}
	held, err := parseBridgeVLANs(out)
	if err != nil {
		return nil, err
	}
	current := held[name]
	target := want
	if len(target) == 0 {
		target = defaultPortVLANs
	}
	forward := vlanTransition(name, current, target, self)
	back := vlanTransition(name, target, current, self)
	if len(want) == 0 || (len(want) == 1 && want[0] == defaultPortVLANs[0]) {
		m.VLANs = nil
	} else {
		m.VLANs = want
	}
	undo := func(ctx context.Context) {
		for _, args := range back {
			s.best(ctx, "bridge", args...)
		}
	}
	err = s.commit(ctx, next, step{
		apply: applying(func(ctx context.Context) error {
			for _, args := range forward {
				if _, err := run(ctx, "bridge", args...); err != nil {
					return err
				}
			}
			return nil
		}, undo),
		undo:   undo,
		verify: verifyPath(st.path),
	})
	if err != nil {
		return nil, err
	}
	return &LinkChange{Persisted: true}, nil
}

// SetVXLANRemotes replaces a managed unicast VXLAN's further flood
// destinations: the other ends every broadcast, unknown and multicast frame
// is copied to beside the device's own remote.
func (s *Service) SetVXLANRemotes(ctx context.Context, name string, remotes []string, client, actor string) (*LinkChange, error) {
	if err := ValidIfName(name); err != nil {
		return nil, err
	}
	if len(remotes) > maxRemotes {
		return nil, fmt.Errorf("a VXLAN floods to at most %d further ends here", maxRemotes)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, err := s.loadSpec()
	if err != nil {
		return nil, err
	}
	next := sp.clone()
	st, err := s.readLinkState(ctx, next, client)
	if err != nil {
		return nil, err
	}
	m, ok := next.link(name)
	if !ok {
		return nil, notMade(st, name)
	}
	if m.Kind != "vxlan" || m.Remote == "" {
		return nil, fmt.Errorf("%s is not a unicast VXLAN; further ends are added to one with a remote", name)
	}
	primary, err := ParseAddr(m.Remote)
	if err != nil {
		return nil, err
	}
	var want []string
	seen := map[netip.Addr]bool{primary: true}
	for _, raw := range remotes {
		a, err := ParseAddr(raw)
		if err != nil {
			return nil, err
		}
		if a.IsMulticast() || a.IsUnspecified() || a.IsLoopback() {
			return nil, fmt.Errorf("%s is not another end's unicast address", a)
		}
		if a.Is4() != primary.Is4() {
			return nil, fmt.Errorf("every end of %s is %s, like its remote", name, famName(primary.Is6()))
		}
		if seen[a] {
			continue
		}
		seen[a] = true
		want = append(want, a.String())
	}
	sort.Strings(want)
	have := map[string]bool{}
	for _, r := range m.Remotes {
		have[r] = true
	}
	wanted := map[string]bool{}
	var forward, back [][]string
	for _, r := range want {
		wanted[r] = true
		if !have[r] {
			forward = append(forward, fdbArgs("append", name, r))
			back = append(back, fdbArgs("del", name, r))
		}
	}
	for _, r := range m.Remotes {
		if !wanted[r] {
			forward = append(forward, fdbArgs("del", name, r))
			back = append(back, fdbArgs("append", name, r))
		}
	}
	m.Remotes = want
	undo := func(ctx context.Context) {
		for _, args := range back {
			s.best(ctx, "bridge", args...)
		}
	}
	err = s.commit(ctx, next, step{
		apply: applying(func(ctx context.Context) error {
			for _, args := range forward {
				if _, err := run(ctx, "bridge", args...); err != nil && !(args[1] == "del" && isGone(err)) {
					return err
				}
			}
			return nil
		}, undo),
		undo:   undo,
		verify: verifyPath(st.path),
	})
	if err != nil {
		return nil, err
	}
	return &LinkChange{Persisted: true}, nil
}

// MasterPreview is what joining a device to a bridge (or taking it out of
// one) would do, read before anything is changed: the guard's verdict, and
// every address, route and dependent entry the move would leave behind.
// The dashboard does not carry addresses or routes across a migration; the
// preview names them so the operator can.
type MasterPreview struct {
	Device string `json:"device"`
	// Bridge is the bridge joined; empty previews leaving the current one.
	Bridge    string          `json:"bridge,omitempty"`
	Allowed   bool            `json:"allowed"`
	Refusal   string          `json:"refusal,omitempty"`
	Persisted bool            `json:"persisted"`
	Steps     []string        `json:"steps"`
	Effects   []PreviewEffect `json:"effects"`
}

// PreviewEffect is one consequence, by kind: address, route, dependent,
// vlan, stp or path.
type PreviewEffect struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

// PreviewMaster previews SetLinkMaster without changing anything.
func (s *Service) PreviewMaster(ctx context.Context, name, master, client string) (*MasterPreview, error) {
	if err := ValidIfName(name); err != nil {
		return nil, err
	}
	master = strings.TrimSpace(master)
	if master != "" {
		if err := ValidIfName(master); err != nil {
			return nil, err
		}
	}
	sp, err := s.loadSpec()
	if err != nil {
		return nil, err
	}
	st, err := s.readLinkState(ctx, sp, client)
	if err != nil {
		return nil, err
	}
	dev, err := st.need(name)
	if err != nil {
		return nil, err
	}
	p := &MasterPreview{Device: name, Bridge: master, Steps: []string{}, Effects: []PreviewEffect{}}
	_, p.Persisted = sp.link(name)
	effect := func(kind, format string, args ...any) {
		p.Effects = append(p.Effects, PreviewEffect{Kind: kind, Detail: fmt.Sprintf(format, args...)})
	}
	refuse := func(err error) {
		if p.Refusal == "" {
			p.Refusal = err.Error()
		}
	}
	if master == "" {
		if dev.Master == "" {
			return nil, fmt.Errorf("%s is not a port of a bridge", name)
		}
		p.Steps = append(p.Steps, fmt.Sprintf("ip link set %s nomaster", name))
		if dev.Owner == "docker" || dev.Owner == "tailscale" {
			refuse(guarded("%s belongs to %s and is changed through its own pages", name, dev.Owner))
		}
		if err := st.leavingPath(dev); err != nil {
			refuse(err)
		}
		effect("path", "%s stops receiving the frames %s switches; whatever reached it through %s stops reaching it.", name, dev.Master, dev.Master)
	} else {
		p.Steps = append(p.Steps, fmt.Sprintf("ip link set %s master %s", name, master))
		bridge, err := st.bridgeTarget(master)
		if err != nil {
			refuse(err)
		}
		if dev.Master == master {
			refuse(fmt.Errorf("%s is already a port of %s", name, master))
		}
		if dev.Guard != "" {
			refuse(guarded("%s cannot be made a bridge port: %s", name, dev.Guard))
		}
		if err := st.leavingPath(dev); err != nil {
			refuse(err)
		}
		for _, a := range dev.Addresses {
			if a.Family == "inet6" && a.Scope == "link" {
				continue
			}
			effect("address", "%s stops answering at %s: a bridge port's addresses stop working. To keep it, remove it here and add it to %s.", name, a.CIDR, master)
		}
		if holdsAddress(dev) {
			refuse(guarded("%s holds an address, and a bridge port's addresses stop working; remove them first", name))
		}
		for _, family := range [][]string{{"-j", "route", "show", "dev", name}, {"-j", "-6", "route", "show", "dev", name}} {
			out, err := run(ctx, "ip", family...)
			if err != nil {
				effect("route", "The routes through %s could not be read (%s); whether any would stop working is unknown.", name, firstLines(err.Error(), 1))
				continue
			}
			var routes []struct {
				Dst      string `json:"dst"`
				Protocol string `json:"protocol"`
			}
			if json.Unmarshal([]byte(out), &routes) != nil {
				continue
			}
			for _, r := range routes {
				if r.Protocol == "kernel" || strings.HasPrefix(r.Dst, "fe80::") {
					continue
				}
				effect("route", "The route to %s leaves through %s and stops working once %s is a port.", r.Dst, name, name)
			}
		}
		if bridge != nil {
			var settings BridgeView
			out, err := run(ctx, "ip", "-j", "-d", "link", "show", "dev", master)
			switch {
			case err != nil:
				effect("vlan", "%s's settings could not be read (%s); whether it filters by VLAN or runs STP is unknown.", master, firstLines(err.Error(), 1))
			case readBridgeSettings(out, master, &settings).State == "ok":
				if settings.VLANFiltering {
					effect("vlan", "%s filters by VLAN: %s joins carrying only VLAN %d, untagged, until its VLANs are set.", master, name, max(settings.DefaultPVID, 1))
				}
				if settings.STP {
					effect("stp", "%s runs STP: %s listens and learns for about thirty seconds before it forwards.", master, name)
				}
			}
		}
	}
	for _, d := range dependents(sp, name) {
		effect("dependent", "A managed %s names %s and keeps doing so after the move.", d, name)
	}
	if !p.Persisted {
		effect("persistence", "%s was not made by Just Dashboard, so the change holds until the next boot.", name)
	}
	p.Allowed = p.Refusal == ""
	return p, nil
}

// RemovesPortVLAN reports whether replacing a managed port's memberships
// with want takes a VLAN away from it, which is what makes the change
// destructive. An unknown device decides nothing here; the apply refuses it.
func RemovesPortVLAN(sp *Spec, name string, want []PortVLAN) bool {
	m, ok := sp.link(name)
	if !ok {
		return false
	}
	target := want
	if len(target) == 0 {
		target = defaultPortVLANs
	}
	kept := map[int]bool{}
	for _, v := range target {
		kept[v.VID] = true
	}
	for _, v := range effectiveVLANs(*m) {
		if !kept[v.VID] {
			return true
		}
	}
	return false
}

// RemovesVXLANRemote reports whether replacing a managed VXLAN's flood ends
// with want drops one.
func RemovesVXLANRemote(sp *Spec, name string, want []string) bool {
	m, ok := sp.link(name)
	if !ok {
		return false
	}
	kept := map[string]bool{}
	for _, raw := range want {
		if a, err := ParseAddr(raw); err == nil {
			kept[a.String()] = true
		}
	}
	for _, r := range m.Remotes {
		if !kept[r] {
			return true
		}
	}
	return false
}

// bridgeRecovery is the bridge commands that put the VLAN memberships and
// VXLAN flood destinations of the named devices back from next to old, for
// the independent recovery journal.
func bridgeRecovery(old, next *Spec, names map[string]bool) []recoveryCommand {
	var out []recoveryCommand
	add := func(args []string) {
		out = append(out, recoveryCommand{Tool: "bridge", Args: args, AllowGone: args[1] == "del", AllowExists: args[1] != "del"})
	}
	for _, before := range old.Links {
		if !names[before.Name] || ValidIfName(before.Name) != nil {
			continue
		}
		after, exists := next.link(before.Name)
		self, wasPort := vlanPort(old, before)
		if wasPort {
			from := defaultPortVLANs
			if exists {
				if _, isPort := vlanPort(next, *after); isPort {
					from = effectiveVLANs(*after)
				}
			}
			if !exists || !equalVLANs(from, effectiveVLANs(before)) {
				for _, args := range vlanTransition(before.Name, from, effectiveVLANs(before), self) {
					add(args)
				}
			}
		}
		if before.Kind == "vxlan" && before.Remote != "" {
			had := map[string]bool{}
			for _, r := range before.Remotes {
				had[r] = true
			}
			if exists {
				for _, r := range after.Remotes {
					if !had[r] {
						add(fdbArgs("del", before.Name, r))
					}
				}
			}
			now := map[string]bool{}
			if exists {
				for _, r := range after.Remotes {
					now[r] = true
				}
			}
			for _, r := range before.Remotes {
				if !now[r] {
					add(fdbArgs("append", before.Name, r))
				}
			}
		}
	}
	return out
}

func equalVLANs(a, b []PortVLAN) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
