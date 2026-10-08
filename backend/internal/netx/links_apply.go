package netx

import (
	"context"
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// renderLinks renders the ip batch file the boot unit restores the devices,
// addresses, routes, rules and namespaces from. It is `ip -force -batch`'s
// input, so one line failing — a parent that is gone, a gateway that is not
// reachable yet — leaves the rest to be restored.
func renderLinks(sp *Spec) string {
	var b strings.Builder
	b.WriteString(generatedHeader)
	for _, line := range batchLines(sp) {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// Policy rules need an explicit process family; unlike routes, ip does not
// infer it from a selector. Keep IPv6 rules in a batch started with -6.
func renderIPv6Rules(sp *Spec) string {
	var b strings.Builder
	b.WriteString(generatedHeader)
	for _, r := range sp.Rules {
		if r.Family != "inet6" {
			continue
		}
		if args, err := ruleArgs(r); err == nil {
			b.WriteString("rule add " + strings.Join(args, " ") + "\n")
		}
	}
	return b.String()
}

// batchLines is the one place the spec becomes `ip` commands, and it serves
// both the boot file and the runtime apply of a single change: a device made
// from the page is made with exactly the lines the next boot would run, so
// "it worked now" and "it comes back after a reboot" cannot disagree.
//
// The order is the dependency order. Namespaces and devices first, then who
// is whose port, then sizes and addresses, then up — a route can only be added
// once the address it leaves through exists and the device is up, and a rule
// refers to tables the routes made.
//
// Every value is parsed again on the way out and written from the parsed
// form, so a hand-edited spec cannot put a second command on a line: an entry
// that does not validate is left out rather than escaped.
func batchLines(sp *Spec) []string {
	var out []string
	add := func(args ...string) { out = append(out, strings.Join(args, " ")) }

	for _, ns := range sp.Namespaces {
		if ValidNamespace(ns.Name) == nil {
			add("netns", "add", ns.Name)
		}
	}
	links := make([]LinkSpec, 0, len(sp.Links))
	for _, l := range sp.Links {
		args, err := linkAddArgs(l)
		if err != nil {
			continue
		}
		links = append(links, l)
		add(args...)
	}
	for _, l := range links {
		if l.Master != "" && ValidIfName(l.Master) == nil {
			add("link", "set", l.Name, "master", l.Master)
		}
	}
	for _, l := range links {
		if l.MTU > 0 {
			mtu := strconv.Itoa(l.MTU)
			add("link", "set", l.Name, "mtu", mtu)
			if l.Kind == "veth" && l.Peer != "" {
				add(peerCommand(l, "link", "set", l.Peer, "mtu", mtu)...)
			}
		}
	}
	for _, l := range links {
		for _, raw := range l.Addresses {
			if p, err := ParsePrefix(raw); err == nil {
				add("addr", "add", p.String(), "dev", l.Name)
			}
		}
	}
	for _, a := range sp.Addresses {
		p, err := ParsePrefix(a.CIDR)
		if err != nil || ValidIfName(a.Link) != nil {
			continue
		}
		// An address on a veth's far end is added from inside the namespace
		// it lives in; the device does not exist in this one.
		if peer, ok := peerOf(links, a.Link); ok {
			add(peerCommand(peer, "addr", "add", p.String(), "dev", a.Link)...)
			continue
		}
		add("addr", "add", p.String(), "dev", a.Link)
	}
	seenNS := map[string]bool{}
	for _, l := range links {
		if l.Up {
			add("link", "set", l.Name, "up")
		}
		if l.Kind != "veth" || l.Peer == "" {
			continue
		}
		// A namespace's loopback starts down, and a veth end in it is of no
		// use until it is up; both are only meaningful once the device is.
		if l.PeerNamespace != "" && !seenNS[l.PeerNamespace] {
			seenNS[l.PeerNamespace] = true
			add(peerCommand(l, "link", "set", "lo", "up")...)
		}
		if l.Up {
			add(peerCommand(l, "link", "set", l.Peer, "up")...)
		}
	}
	for _, r := range sp.Routes {
		if args, err := routeArgs(r); err == nil {
			add(append([]string{"route", "add"}, args...)...)
		}
	}
	for _, r := range sp.Rules {
		// IPv6 rules are restored by the separate batch started with -6.
		if r.Family == "inet6" {
			continue
		}
		if args, err := ruleArgs(r); err == nil {
			add(append([]string{"rule", "add"}, args...)...)
		}
	}
	return out
}

// peerOf finds the veth whose far end is named name and lives in a namespace.
func peerOf(links []LinkSpec, name string) (LinkSpec, bool) {
	for _, l := range links {
		if l.Kind == "veth" && l.Peer == name && l.PeerNamespace != "" {
			return l, true
		}
	}
	return LinkSpec{}, false
}

// peerCommand is an ip command run on a veth's far end: in its namespace when
// it has one, in this one when not.
func peerCommand(l LinkSpec, args ...string) []string {
	if l.PeerNamespace == "" {
		return args
	}
	return append([]string{"netns", "exec", l.PeerNamespace, "ip"}, args...)
}

// linkAddArgs is `ip link add` for one managed device, the arguments after
// "ip". An error means the entry does not describe a device ip would accept.
func linkAddArgs(l LinkSpec) ([]string, error) {
	if err := ValidIfName(l.Name); err != nil {
		return nil, err
	}
	args := []string{"link", "add", l.Name}
	need := func(name string) error {
		if name == "" {
			return fmt.Errorf("%s needs a parent device", l.Kind)
		}
		return ValidIfName(name)
	}
	switch l.Kind {
	case "bridge":
		args = append(args, "type", "bridge")
		if l.STP {
			args = append(args, "stp_state", "1")
		}
	case "dummy":
		args = append(args, "type", "dummy")
	case "vlan":
		if err := need(l.Parent); err != nil {
			return nil, err
		}
		if l.VLANID < 1 || l.VLANID > 4094 {
			return nil, fmt.Errorf("a VLAN id is 1 to 4094")
		}
		args = append(args, "link", l.Parent, "type", "vlan", "id", strconv.Itoa(l.VLANID))
	case "macvlan":
		if err := need(l.Parent); err != nil {
			return nil, err
		}
		if !macvlanModes[l.Mode] {
			return nil, fmt.Errorf("a macvlan mode is bridge, private, vepa or passthru")
		}
		args = append(args, "link", l.Parent, "type", "macvlan", "mode", l.Mode)
	case "vxlan":
		if l.VNI < 1 || l.VNI > 16777215 {
			return nil, fmt.Errorf("a VNI is 1 to 16777215")
		}
		args = append(args, "type", "vxlan", "id", strconv.Itoa(l.VNI))
		switch {
		case l.Remote != "" && l.Group != "":
			return nil, fmt.Errorf("a VXLAN has a remote or a group, not both")
		case l.Remote != "":
			a, err := ParseAddr(l.Remote)
			if err != nil {
				return nil, err
			}
			args = append(args, "remote", a.String())
		case l.Group != "":
			a, err := ParseAddr(l.Group)
			if err != nil {
				return nil, err
			}
			args = append(args, "group", a.String())
		default:
			return nil, fmt.Errorf("a VXLAN needs a remote address or a multicast group")
		}
		if l.Local != "" {
			a, err := ParseAddr(l.Local)
			if err != nil {
				return nil, err
			}
			args = append(args, "local", a.String())
		}
		if l.Parent != "" {
			if err := ValidIfName(l.Parent); err != nil {
				return nil, err
			}
			args = append(args, "dev", l.Parent)
		}
		if l.Port != 0 {
			if l.Port < 1 || l.Port > 65535 {
				return nil, fmt.Errorf("a port is 1 to 65535")
			}
			args = append(args, "dstport", strconv.Itoa(l.Port))
		}
	case "gre", "gretap", "ip6gre", "ip6gretap":
		remote, err := ParseAddr(l.Remote)
		if err != nil {
			return nil, err
		}
		args = append(args, "type", l.Kind, "remote", remote.String())
		if l.Local != "" {
			local, err := ParseAddr(l.Local)
			if err != nil {
				return nil, err
			}
			args = append(args, "local", local.String())
		}
		if l.TTL != 0 {
			if l.TTL < 1 || l.TTL > 255 {
				return nil, fmt.Errorf("a TTL is 1 to 255")
			}
			args = append(args, "ttl", strconv.Itoa(l.TTL))
		}
		if l.Key != 0 {
			args = append(args, "key", strconv.FormatUint(uint64(l.Key), 10))
		}
	case "veth":
		if err := ValidIfName(l.Peer); err != nil {
			return nil, err
		}
		args = append(args, "type", "veth", "peer", "name", l.Peer)
		if l.PeerNamespace != "" {
			if err := ValidNamespace(l.PeerNamespace); err != nil {
				return nil, err
			}
			args = append(args, "netns", l.PeerNamespace)
		}
	default:
		return nil, fmt.Errorf("%q is not a device kind that can be made here", l.Kind)
	}
	return args, nil
}

// linkKinds are the devices that can be made here.
var linkKinds = map[string]bool{
	"bridge": true, "vlan": true, "vxlan": true, "gre": true, "gretap": true,
	"ip6gre": true, "ip6gretap": true, "dummy": true, "macvlan": true, "veth": true,
}

var macvlanModes = map[string]bool{"bridge": true, "private": true, "vepa": true, "passthru": true}

// LinkRequest is a device to create. Fields a kind has no use for are ignored.
type LinkRequest struct {
	Name string `json:"name"`
	// Kind is bridge, vlan, vxlan, gre, gretap, ip6gre, ip6gretap, dummy,
	// macvlan or veth.
	Kind string `json:"kind"`
	// Parent is the device a VLAN or macvlan rides on, and a VXLAN's `dev`.
	Parent string `json:"parent,omitempty"`
	VLANID int    `json:"vlanId,omitempty"`
	VNI    int    `json:"vni,omitempty"`
	Local  string `json:"local,omitempty"`
	Remote string `json:"remote,omitempty"`
	Group  string `json:"group,omitempty"`
	Port   int    `json:"port,omitempty"`
	TTL    int    `json:"ttl,omitempty"`
	Key    uint32 `json:"key,omitempty"`
	Mode   string `json:"mode,omitempty"`
	// Peer and PeerNamespace are a veth's other end and the managed
	// namespace it is moved into.
	Peer          string `json:"peer,omitempty"`
	PeerNamespace string `json:"peerNamespace,omitempty"`
	MTU           int    `json:"mtu,omitempty"`
	STP           bool   `json:"stp,omitempty"`
	// Master is a bridge to make the new device a port of.
	Master    string   `json:"master,omitempty"`
	Addresses []string `json:"addresses,omitempty"`
	Up        bool     `json:"up"`
}

// LinkChange says what a change to a device did beyond the moment: whether the
// next boot will do it again. Only what the dashboard made is restored, so a
// change to anything else holds until the device goes away or the host
// restarts, and the page says so in Note.
type LinkChange struct {
	Persisted bool   `json:"persisted"`
	Note      string `json:"note,omitempty"`
}

const notPersisted = "Applied now, not restored at boot: this device was not created by Just Dashboard."

// spec validates a request and returns the entry it would record.
func (req LinkRequest) spec() (LinkSpec, error) {
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	if !linkKinds[kind] {
		return LinkSpec{}, fmt.Errorf("a device kind is bridge, vlan, vxlan, gre, gretap, ip6gre, ip6gretap, dummy, macvlan or veth")
	}
	name := strings.TrimSpace(req.Name)
	if err := ValidIfName(name); err != nil {
		return LinkSpec{}, err
	}
	l := LinkSpec{Name: name, Kind: kind, Up: req.Up}
	if req.MTU != 0 {
		if req.MTU < 68 || req.MTU > 65535 {
			return LinkSpec{}, fmt.Errorf("an MTU is 68 to 65535")
		}
		l.MTU = req.MTU
	}
	switch kind {
	case "bridge":
		l.STP = req.STP
	case "vlan":
		if req.VLANID < 1 || req.VLANID > 4094 {
			return LinkSpec{}, fmt.Errorf("a VLAN id is 1 to 4094")
		}
		l.Parent, l.VLANID = strings.TrimSpace(req.Parent), req.VLANID
	case "macvlan":
		l.Parent = strings.TrimSpace(req.Parent)
		l.Mode = strings.ToLower(strings.TrimSpace(req.Mode))
		if l.Mode == "" {
			l.Mode = "bridge"
		}
		if !macvlanModes[l.Mode] {
			return LinkSpec{}, fmt.Errorf("a macvlan mode is bridge, private, vepa or passthru")
		}
	case "vxlan":
		if req.VNI < 1 || req.VNI > 16777215 {
			return LinkSpec{}, fmt.Errorf("a VNI is 1 to 16777215")
		}
		l.VNI, l.Parent = req.VNI, strings.TrimSpace(req.Parent)
		// 4789 is the port the standard names; the kernel's own default is
		// the older 8472, which nothing but another Linux box answers on.
		l.Port = req.Port
		if l.Port == 0 {
			l.Port = 4789
		}
		if l.Port < 1 || l.Port > 65535 {
			return LinkSpec{}, fmt.Errorf("a port is 1 to 65535")
		}
		var fam []netip.Addr
		remote, err := optionalAddr(req.Remote)
		if err != nil {
			return LinkSpec{}, err
		}
		group, err := optionalAddr(req.Group)
		if err != nil {
			return LinkSpec{}, err
		}
		local, err := optionalAddr(req.Local)
		if err != nil {
			return LinkSpec{}, err
		}
		switch {
		case remote.IsValid() == group.IsValid():
			return LinkSpec{}, fmt.Errorf("a VXLAN has either a remote address or a multicast group")
		case remote.IsValid():
			if remote.IsMulticast() || remote.IsUnspecified() {
				return LinkSpec{}, fmt.Errorf("a VXLAN remote is the other end's unicast address")
			}
			l.Remote = remote.String()
			fam = append(fam, remote)
		default:
			if !group.IsMulticast() {
				return LinkSpec{}, fmt.Errorf("%s is not a multicast group", group)
			}
			if l.Parent == "" {
				return LinkSpec{}, fmt.Errorf("a VXLAN on a multicast group needs the device it sends from")
			}
			l.Group = group.String()
			fam = append(fam, group)
		}
		if local.IsValid() {
			l.Local = local.String()
			fam = append(fam, local)
		}
		if err := sameFamily(fam); err != nil {
			return LinkSpec{}, err
		}
	case "gre", "gretap", "ip6gre", "ip6gretap":
		remote, err := ParseAddr(req.Remote)
		if err != nil {
			return LinkSpec{}, fmt.Errorf("a tunnel needs the other end's address: %w", err)
		}
		local, err := optionalAddr(req.Local)
		if err != nil {
			return LinkSpec{}, err
		}
		fam := []netip.Addr{remote}
		l.Remote = remote.String()
		if local.IsValid() {
			l.Local = local.String()
			fam = append(fam, local)
		}
		if err := sameFamily(fam); err != nil {
			return LinkSpec{}, err
		}
		if want6 := strings.HasPrefix(kind, "ip6"); want6 != remote.Is6() {
			return LinkSpec{}, fmt.Errorf("%s tunnels are %s; the ends must be %s addresses", kind, famName(want6), famName(want6))
		}
		if req.TTL != 0 && (req.TTL < 1 || req.TTL > 255) {
			return LinkSpec{}, fmt.Errorf("a TTL is 1 to 255")
		}
		l.TTL, l.Key = req.TTL, req.Key
	case "veth":
		peer := strings.TrimSpace(req.Peer)
		if err := ValidIfName(peer); err != nil {
			return LinkSpec{}, fmt.Errorf("the veth's other end: %w", err)
		}
		if peer == name {
			return LinkSpec{}, fmt.Errorf("the two ends of a veth need different names")
		}
		l.Peer = peer
		if ns := strings.TrimSpace(req.PeerNamespace); ns != "" {
			if err := ValidNamespace(ns); err != nil {
				return LinkSpec{}, err
			}
			l.PeerNamespace = ns
		}
	}
	if l.Parent != "" {
		if err := ValidIfName(l.Parent); err != nil {
			return LinkSpec{}, err
		}
		if l.Parent == name {
			return LinkSpec{}, fmt.Errorf("a device cannot be its own parent")
		}
	}
	if m := strings.TrimSpace(req.Master); m != "" {
		if err := ValidIfName(m); err != nil {
			return LinkSpec{}, err
		}
		if m == name {
			return LinkSpec{}, fmt.Errorf("a bridge cannot be its own port")
		}
		l.Master = m
	}
	addrs, err := cleanAddresses(req.Addresses)
	if err != nil {
		return LinkSpec{}, err
	}
	l.Addresses = addrs
	return l, nil
}

func famName(v6 bool) string {
	if v6 {
		return "IPv6"
	}
	return "IPv4"
}

func optionalAddr(s string) (netip.Addr, error) {
	if strings.TrimSpace(s) == "" {
		return netip.Addr{}, nil
	}
	return ParseAddr(s)
}

func sameFamily(addrs []netip.Addr) error {
	for _, a := range addrs[1:] {
		if a.Is4() != addrs[0].Is4() {
			return fmt.Errorf("the addresses of one tunnel are all IPv4 or all IPv6")
		}
	}
	return nil
}

// cleanAddresses parses interface addresses and keeps each once, in the order
// given, with the host bits kept.
func cleanAddresses(in []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, raw := range in {
		p, err := ParsePrefix(raw)
		if err != nil {
			return nil, err
		}
		if err := checkInterfaceAddress(p); err != nil {
			return nil, err
		}
		if !seen[p.String()] {
			seen[p.String()] = true
			out = append(out, p.String())
		}
	}
	return out, nil
}

// checkInterfaceAddress refuses the addresses that are never right on a
// device: the unspecified address, a multicast group, loopback, and a /0.
func checkInterfaceAddress(p netip.Prefix) error {
	a := p.Addr()
	switch {
	case a.IsUnspecified(), a.IsMulticast(), a.IsLoopback():
		return fmt.Errorf("%s cannot be the address of a device", a)
	case p.Bits() == 0:
		return fmt.Errorf("%s has no network part", p)
	}
	return nil
}

// linkState is the host's devices as the guards judge them: annotated with
// owners, the uplink and the client path, but without Docker's container
// inventory (which only the api layer holds). Docker's bridges and veths are
// still recognised by name, which is how Docker names everything it makes.
type linkState struct {
	links []Link
	by    map[string]*Link
	path  Path
	// under maps a device to what it carries by being the parent or the
	// bridge of a device that is itself guarded.
	under map[string]string
}

func (s *Service) readLinkState(ctx context.Context, sp *Spec, client string) (*linkState, error) {
	linkOut, err := run(ctx, "ip", "-j", "-d", "link", "show")
	if err != nil {
		return nil, err
	}
	addrOut, err := run(ctx, "ip", "-j", "addr", "show")
	if err != nil {
		return nil, err
	}
	links, err := parseLinks(linkOut, addrOut)
	if err != nil {
		return nil, err
	}
	path, err := clientPath(ctx, client)
	if err != nil {
		return nil, err
	}
	annotate(links, annotation{uplinks: readUplinks(ctx), path: path, spec: sp})
	st := &linkState{links: links, by: make(map[string]*Link, len(links)), path: path}
	for i := range st.links {
		st.by[st.links[i].Name] = &st.links[i]
	}
	st.guardUnderlying()
	return st, nil
}

// guardUnderlying extends the guard to what a guarded device runs on. A VLAN
// that carries the uplink is only as alive as its parent, and a bridge whose
// port carries it is only as alive as the bridge; setting the parent down,
// deleting it, shrinking its MTU or putting it in a bridge takes the child
// with it while the routing table, and so verifyPath, reads exactly as before.
func (st *linkState) guardUnderlying() {
	st.under = map[string]string{}
	for i := range st.links {
		child := &st.links[i]
		if !child.Uplink && !child.ClientPath {
			continue
		}
		purpose := pathPurpose(child)
		seen := map[string]bool{child.Name: true}
		for cur := child; cur != nil; {
			var next *Link
			for _, name := range []string{cur.Parent, cur.Master} {
				if name == "" || seen[name] {
					continue
				}
				under, ok := st.by[name]
				if !ok {
					continue
				}
				seen[name] = true
				if _, taken := st.under[name]; !taken {
					st.under[name] = purpose
				}
				if under.Guard == "" {
					under.Guard = fmt.Sprintf("%s carries %s, which carries %s.", name, child.Name, purpose)
				}
				next = under
			}
			cur = next
		}
	}
}

// carries says what a device must keep for the dashboard: its own path or
// uplink role, or the one of a device that runs on it.
func (st *linkState) carries(l *Link) string {
	switch {
	case l.ClientPath || l.Uplink:
		return pathPurpose(l)
	}
	return st.under[l.Name]
}

// need returns a device that must exist.
func (st *linkState) need(name string) (*Link, error) {
	l, ok := st.by[name]
	if !ok {
		return nil, fmt.Errorf("%s: %w", name, ErrNotFound)
	}
	return l, nil
}

// holdsAddress reports whether a device carries an address something may be
// using. A device's own IPv6 link-local address is made by the kernel and
// belongs to no service, so it does not count.
func holdsAddress(l *Link) bool {
	for _, a := range l.Addresses {
		if a.Family == "inet6" && a.Scope == "link" {
			continue
		}
		return true
	}
	return false
}

// bridgePort is the guard for making a device a port of a bridge: it takes
// the device's addresses out of service, and a device the dashboard may not
// take down may not be taken out of its place either.
func (st *linkState) bridgePort(dev *Link, bridge *Link) error {
	if dev.Name == bridge.Name {
		return fmt.Errorf("a bridge cannot be its own port")
	}
	if dev.Guard != "" {
		return guarded("%s cannot be made a bridge port: %s", dev.Name, dev.Guard)
	}
	if holdsAddress(dev) {
		return guarded("%s holds an address, and a bridge port's addresses stop working; remove them first", dev.Name)
	}
	return nil
}

// bridgeTarget finds the bridge a device would join and refuses one that
// another program manages.
func (st *linkState) bridgeTarget(name string) (*Link, error) {
	b, err := st.need(name)
	if err != nil {
		return nil, err
	}
	if b.Kind != "bridge" {
		return nil, fmt.Errorf("%s is not a bridge", name)
	}
	if b.Owner == "docker" || b.Owner == "tailscale" {
		return nil, guarded("%s belongs to %s and is changed through its own pages", name, b.Owner)
	}
	return b, nil
}

// leavingPath refuses taking a device out of a bridge that carries the
// uplink or the browser's path: its traffic arrives through that port.
func (st *linkState) leavingPath(dev *Link) error {
	if dev.Master == "" {
		return nil
	}
	if m, ok := st.by[dev.Master]; ok && (m.ClientPath || m.Uplink) {
		return guarded("%s is a port of %s, which carries %s", dev.Name, m.Name, pathPurpose(m))
	}
	return nil
}

func pathPurpose(l *Link) string {
	if l.ClientPath {
		return "your connection to the dashboard"
	}
	return "this server's default route"
}

// best runs a command whose failure is not worth reporting beyond the log:
// the undo of a change that already failed.
func (s *Service) best(ctx context.Context, name string, args ...string) {
	if _, err := run(ctx, name, args...); err != nil {
		s.log.Warn("network: rollback step failed", "cmd", name+" "+strings.Join(args, " "), "err", err)
	}
}

// bestBatch is best for a batch of lines.
func (s *Service) bestBatch(ctx context.Context, lines []string) {
	if err := applyBatch(ctx, lines); err != nil {
		s.log.Warn("network: rollback batch failed", "err", err)
	}
}

// applyBatch runs lines through one `ip -batch -`. Without -force the batch
// stops at the first failure, so what ran before it is undone by the caller.
func applyBatch(ctx context.Context, lines []string) error {
	_, err := runStdin(ctx, []byte(strings.Join(lines, "\n")+"\n"), "ip", "-batch", "-")
	return err
}

// applying wraps a runtime change so a failure partway through puts back what
// it did do: commit only calls undo once verify fails, and a batch that stopped
// at its fifth line has already changed the host.
func applying(do func(ctx context.Context) error, undo func(ctx context.Context)) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		if err := do(ctx); err != nil {
			rollback(ctx, undo)
			return err
		}
		return nil
	}
}

// runtimeOnly is commit without the files, for a change to something the
// dashboard did not make and so does not restore: apply, verify, and put it
// back if the check fails.
func runtimeOnly(ctx context.Context, st step) error {
	if err := st.apply(ctx); err != nil {
		return err
	}
	if st.verify != nil {
		if err := st.verify(ctx); err != nil {
			rollback(ctx, st.undo)
			return err
		}
	}
	return nil
}

// stamp is the Made record of a new entry.
func stamp(actor string) Made {
	return Made{CreatedAt: time.Now().UTC(), CreatedBy: actor}
}

// CreateLink makes a device and records it so the next boot makes it again.
func (s *Service) CreateLink(ctx context.Context, req LinkRequest, client, actor string) (*LinkSpec, error) {
	l, err := req.spec()
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
	if _, ok := st.by[l.Name]; ok {
		return nil, fmt.Errorf("%s: %w", l.Name, ErrExists)
	}
	if l.Kind == "veth" {
		if _, ok := st.by[l.Peer]; ok && l.PeerNamespace == "" {
			return nil, fmt.Errorf("%s: %w", l.Peer, ErrExists)
		}
		if l.PeerNamespace != "" {
			if err := s.peerFree(ctx, next, l.PeerNamespace, l.Peer); err != nil {
				return nil, err
			}
		}
	}
	if l.Parent != "" {
		parent, err := st.need(l.Parent)
		if err != nil {
			return nil, err
		}
		// Passthru hands every frame the parent receives to the one macvlan,
		// and the parent's own stack sees none: the host stops answering
		// while every route, and so every check made after it, is unchanged.
		if l.Kind == "macvlan" && l.Mode == "passthru" && st.carries(parent) != "" {
			return nil, guarded("a passthru macvlan on %s would take every frame %s receives, and %s carries %s", parent.Name, parent.Name, parent.Name, st.carries(parent))
		}
	}
	if l.Master != "" {
		bridge, err := st.bridgeTarget(l.Master)
		if err != nil {
			return nil, err
		}
		if len(l.Addresses) > 0 {
			return nil, guarded("%s would be a port of %s, and a bridge port's addresses stop working; give the address to the bridge", l.Name, bridge.Name)
		}
	}
	l.Made = stamp(actor)
	next.Links = append(next.Links, l)

	lines := batchLines(&Spec{Links: []LinkSpec{l}})
	undo := func(ctx context.Context) { s.best(ctx, "ip", "link", "del", l.Name) }
	err = s.commit(ctx, next, step{
		apply:  applying(func(ctx context.Context) error { return applyBatch(ctx, lines) }, undo),
		undo:   undo,
		verify: verifyPath(st.path),
	})
	if err != nil {
		return nil, err
	}
	return &l, nil
}

// peerFree checks a veth's far end can be named in a namespace the dashboard
// made: the namespace must be one of its own, and the name free inside it.
func (s *Service) peerFree(ctx context.Context, sp *Spec, ns, peer string) error {
	if !hasNamespace(sp, ns) {
		return fmt.Errorf("namespace %s: %w", ns, ErrNotFound)
	}
	out, err := run(ctx, "ip", "-n", ns, "-j", "link", "show")
	if err != nil {
		return err
	}
	names, err := linkNames(out)
	if err != nil {
		return err
	}
	if names[peer] {
		return fmt.Errorf("%s in namespace %s: %w", peer, ns, ErrExists)
	}
	return nil
}

func hasNamespace(sp *Spec, name string) bool {
	for _, n := range sp.Namespaces {
		if n.Name == name {
			return true
		}
	}
	return false
}

// linkNames reads the device names out of `ip -j link show`.
func linkNames(out string) (map[string]bool, error) {
	links, err := parseLinks(out, "[]")
	if err != nil {
		return nil, err
	}
	names := make(map[string]bool, len(links))
	for _, l := range links {
		names[l.Name] = true
	}
	return names, nil
}

// dependents names the managed routes and rules that refer to a device, which
// a deleted device would leave behind as lines failing at every boot.
func dependents(sp *Spec, name string) []string {
	var out []string
	for _, r := range sp.Routes {
		if r.Device == name {
			out = append(out, fmt.Sprintf("route to %s", r.Destination))
		}
	}
	for _, r := range sp.Rules {
		if r.IIF == name || r.OIF == name {
			out = append(out, fmt.Sprintf("rule with priority %d", r.Priority))
		}
	}
	for _, sh := range sp.Shaping {
		if sh.Device == name {
			out = append(out, "shaping entry")
		}
	}
	for _, f := range sp.Forwards {
		if f.Interface == name {
			out = append(out, fmt.Sprintf("port forward %q", f.Name))
		}
	}
	for _, n := range sp.NAT {
		if n.Interface == name {
			out = append(out, fmt.Sprintf("NAT entry %q", n.Name))
		}
	}
	return out
}

// notMade is the error for something that exists but was made by someone
// else, or for something that is not there at all.
func notMade(st *linkState, name string) error {
	if _, ok := st.by[name]; ok {
		return fmt.Errorf("%s: %w", name, ErrNotManaged)
	}
	return fmt.Errorf("%s: %w", name, ErrNotFound)
}

// DeleteLink removes a device the dashboard made.
func (s *Service) DeleteLink(ctx context.Context, name, client, actor string) error {
	if err := ValidIfName(name); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, err := s.loadSpec()
	if err != nil {
		return err
	}
	next := sp.clone()
	st, err := s.readLinkState(ctx, next, client)
	if err != nil {
		return err
	}
	managed, ok := next.link(name)
	if !ok {
		return notMade(st, name)
	}
	gone := *managed
	if l, ok := st.by[name]; ok && l.Guard != "" {
		return guarded("%s cannot be deleted: %s", name, l.Guard)
	}
	for _, l := range next.Links {
		if l.Parent == name {
			return fmt.Errorf("%s is the parent of %s, which would be deleted with it; delete %s first", name, l.Name, l.Name)
		}
	}
	for _, l := range st.by {
		if l.Parent == name {
			return fmt.Errorf("%s is the parent of %s, which would be deleted with it; remove %s through its owner first", name, l.Name, l.Name)
		}
	}
	if dep := dependents(next, name); len(dep) > 0 {
		return fmt.Errorf("%s is used by a %s; remove that first", name, dep[0])
	}
	if gone.Kind == "veth" && gone.PeerNamespace == "" {
		if dep := dependents(next, gone.Peer); len(dep) > 0 {
			return fmt.Errorf("%s's peer %s is used by a %s; remove that first", name, gone.Peer, dep[0])
		}
	}
	var kept []LinkSpec
	for _, l := range next.Links {
		if l.Name == name {
			continue
		}
		if l.Master == name {
			l.Master = ""
		}
		kept = append(kept, l)
	}
	next.Links = kept
	var peerAddrs []AddressSpec
	var addrs []AddressSpec
	for _, a := range next.Addresses {
		switch {
		case a.Link == name:
		case gone.Kind == "veth" && a.Link == gone.Peer:
			peerAddrs = append(peerAddrs, a)
		default:
			addrs = append(addrs, a)
		}
	}
	next.Addresses = addrs

	restore := batchLines(&Spec{Links: []LinkSpec{gone}, Addresses: peerAddrs})
	return s.commit(ctx, next, step{
		apply: func(ctx context.Context) error {
			_, err := run(ctx, "ip", "link", "del", name)
			return err
		},
		undo:   func(ctx context.Context) { s.bestBatch(ctx, restore) },
		verify: verifyPath(st.path),
	})
}

// SetLinkState brings a device up or down. Down is refused for the devices
// the guards name; up is always allowed.
func (s *Service) SetLinkState(ctx context.Context, name string, up bool, client, actor string) (*LinkChange, error) {
	if err := ValidIfName(name); err != nil {
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
	dev, err := st.need(name)
	if err != nil {
		return nil, err
	}
	if !up && dev.Guard != "" {
		return nil, guarded("%s cannot be set down: %s", name, dev.Guard)
	}
	if !up {
		if err := st.leavingPath(dev); err != nil {
			return nil, err
		}
	}
	want, was := "down", "down"
	if up {
		want = "up"
	}
	if dev.AdminUp {
		was = "up"
	}
	set := func(state string) func(context.Context) error {
		return func(ctx context.Context) error {
			_, err := run(ctx, "ip", "link", "set", name, state)
			return err
		}
	}
	undo := func(ctx context.Context) { s.best(ctx, "ip", "link", "set", name, was) }
	stp := step{apply: set(want), undo: undo, verify: verifyPath(st.path)}
	if m, ok := next.link(name); ok {
		m.Up = up
		if err := s.commit(ctx, next, stp); err != nil {
			return nil, err
		}
		return &LinkChange{Persisted: true}, nil
	}
	if err := runtimeOnly(ctx, stp); err != nil {
		return nil, err
	}
	return &LinkChange{Note: notPersisted}, nil
}

// SetLinkMTU changes a device's MTU. The device its own traffic and the
// browser's leave through cannot go below the IPv6 minimum, under which the
// kernel drops IPv6 from a device altogether.
func (s *Service) SetLinkMTU(ctx context.Context, name string, mtu int, client, actor string) (*LinkChange, error) {
	if err := ValidIfName(name); err != nil {
		return nil, err
	}
	if mtu < 68 || mtu > 65535 {
		return nil, fmt.Errorf("an MTU is 68 to 65535")
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
	dev, err := st.need(name)
	if err != nil {
		return nil, err
	}
	if dev.Owner == "docker" || dev.Owner == "tailscale" || dev.Kind == "loopback" {
		return nil, guarded("%s's MTU is not changed here: %s", name, dev.Guard)
	}
	if mtu < 1280 {
		if what := st.carries(dev); what != "" {
			return nil, guarded("%s carries %s, and an MTU under 1280 is below what IPv6 needs on it", name, what)
		}
		for _, a := range dev.Addresses {
			if a.Family == "inet6" && a.Scope != "link" {
				return nil, guarded("%s holds an IPv6 address, which the kernel removes from a device with an MTU under 1280", name)
			}
		}
	}
	prev := dev.MTU
	stp := step{
		apply: func(ctx context.Context) error {
			_, err := run(ctx, "ip", "link", "set", name, "mtu", strconv.Itoa(mtu))
			return err
		},
		undo:   func(ctx context.Context) { s.best(ctx, "ip", "link", "set", name, "mtu", strconv.Itoa(prev)) },
		verify: verifyPath(st.path),
	}
	if m, ok := next.link(name); ok {
		m.MTU = mtu
		if err := s.commit(ctx, next, stp); err != nil {
			return nil, err
		}
		return &LinkChange{Persisted: true}, nil
	}
	if err := runtimeOnly(ctx, stp); err != nil {
		return nil, err
	}
	return &LinkChange{Note: notPersisted}, nil
}

// SetLinkMaster makes a device a port of a bridge, or with an empty master
// takes it out of the one it is in.
func (s *Service) SetLinkMaster(ctx context.Context, name, master, client, actor string) (*LinkChange, error) {
	if err := ValidIfName(name); err != nil {
		return nil, err
	}
	master = strings.TrimSpace(master)
	if master != "" {
		if err := ValidIfName(master); err != nil {
			return nil, err
		}
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
	dev, err := st.need(name)
	if err != nil {
		return nil, err
	}
	prev := dev.Master
	var stp step
	if master == "" {
		if prev == "" {
			return nil, fmt.Errorf("%s is not a port of a bridge", name)
		}
		if dev.Owner == "docker" || dev.Owner == "tailscale" {
			return nil, guarded("%s belongs to %s and is changed through its own pages", name, dev.Owner)
		}
		if err := st.leavingPath(dev); err != nil {
			return nil, err
		}
		stp = step{
			apply: func(ctx context.Context) error {
				_, err := run(ctx, "ip", "link", "set", name, "nomaster")
				return err
			},
			undo:   func(ctx context.Context) { s.best(ctx, "ip", "link", "set", name, "master", prev) },
			verify: verifyPath(st.path),
		}
	} else {
		bridge, err := st.bridgeTarget(master)
		if err != nil {
			return nil, err
		}
		if err := st.bridgePort(dev, bridge); err != nil {
			return nil, err
		}
		if prev == master {
			return nil, fmt.Errorf("%s is already a port of %s", name, master)
		}
		if err := st.leavingPath(dev); err != nil {
			return nil, err
		}
		undo := func(ctx context.Context) {
			if prev == "" {
				s.best(ctx, "ip", "link", "set", name, "nomaster")
				return
			}
			s.best(ctx, "ip", "link", "set", name, "master", prev)
		}
		stp = step{
			apply: func(ctx context.Context) error {
				_, err := run(ctx, "ip", "link", "set", name, "master", master)
				return err
			},
			undo:   undo,
			verify: verifyPath(st.path),
		}
	}
	if m, ok := next.link(name); ok {
		m.Master = master
		if err := s.commit(ctx, next, stp); err != nil {
			return nil, err
		}
		return &LinkChange{Persisted: true}, nil
	}
	if err := runtimeOnly(ctx, stp); err != nil {
		return nil, err
	}
	return &LinkChange{Note: notPersisted}, nil
}

// AddAddress gives a device another address. On a device the dashboard made it
// is part of that device's entry; on any other it is recorded on its own, so
// the address is restored at boot even though the device is not.
func (s *Service) AddAddress(ctx context.Context, name, cidr, client, actor string) (*LinkChange, error) {
	if err := ValidIfName(name); err != nil {
		return nil, err
	}
	p, err := ParsePrefix(cidr)
	if err != nil {
		return nil, err
	}
	if err := checkInterfaceAddress(p); err != nil {
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
	dev, err := st.need(name)
	if err != nil {
		return nil, err
	}
	if dev.Kind == "loopback" || dev.Owner == "docker" || dev.Owner == "tailscale" {
		return nil, guarded("%s is managed elsewhere (%s); addresses are not added to it here", name, firstNonEmpty(dev.Owner, dev.Kind))
	}
	want := p.String()
	for _, a := range dev.Addresses {
		if a.CIDR == want {
			return nil, fmt.Errorf("%s on %s: %w", want, name, ErrExists)
		}
	}
	if m, ok := next.link(name); ok {
		m.Addresses = append(m.Addresses, want)
	} else {
		next.Addresses = append(next.Addresses, AddressSpec{ID: next.takeID(), Link: name, CIDR: want, Made: stamp(actor)})
	}
	err = s.commit(ctx, next, step{
		apply: func(ctx context.Context) error {
			_, err := run(ctx, "ip", "addr", "add", want, "dev", name)
			return err
		},
		undo:   func(ctx context.Context) { s.best(ctx, "ip", "addr", "del", want, "dev", name) },
		verify: verifyPath(st.path),
	})
	if err != nil {
		return nil, err
	}
	return &LinkChange{Persisted: true}, nil
}

// errGone matches what ip says about deleting what is already not there; a
// delete whose target has already gone is the state it was asked for.
var errGone = regexp.MustCompile(`(?i)no such process|cannot assign requested address|no such file or directory|cannot find device|does not exist`)

func isGone(err error) bool { return err != nil && errGone.MatchString(err.Error()) }

// RemoveAddress takes away an address the dashboard added, and never the one
// the browser's own reply is sent from.
func (s *Service) RemoveAddress(ctx context.Context, name, cidr, client, actor string) (*LinkChange, error) {
	if err := ValidIfName(name); err != nil {
		return nil, err
	}
	p, err := ParsePrefix(cidr)
	if err != nil {
		return nil, err
	}
	want := p.String()
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
	found := false
	if m, ok := next.link(name); ok {
		var kept []string
		for _, a := range m.Addresses {
			if a == want {
				found = true
				continue
			}
			kept = append(kept, a)
		}
		m.Addresses = kept
	}
	if !found {
		var kept []AddressSpec
		for _, a := range next.Addresses {
			if a.Link == name && a.CIDR == want {
				found = true
				continue
			}
			kept = append(kept, a)
		}
		next.Addresses = kept
	}
	if !found {
		return nil, fmt.Errorf("%s on %s: %w", want, name, ErrNotManaged)
	}
	if st.path.Source != "" && p.Addr().String() == st.path.Source {
		return nil, guarded("%s is the address your connection to the dashboard is answered from; removing it would cut you off", p.Addr())
	}
	err = s.commit(ctx, next, step{
		apply: func(ctx context.Context) error {
			if _, err := run(ctx, "ip", "addr", "del", want, "dev", name); err != nil && !isGone(err) {
				return err
			}
			return nil
		},
		undo:   func(ctx context.Context) { s.best(ctx, "ip", "addr", "add", want, "dev", name) },
		verify: verifyPath(st.path),
	})
	if err != nil {
		return nil, err
	}
	return &LinkChange{Persisted: true}, nil
}
