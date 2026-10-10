package netx

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// gatewayTable is the one nftables table the dashboard owns. It only ever
// drops or translates; admitting traffic stays the firewall's job, except for
// the connections a translation here created (admission, below).
const gatewayTable = "jd_gateway"

// The connection mark a translated flow carries, in the top byte so it never
// meets the packet-mark bits Tailscale uses (0x00ff0000).
const (
	connMark = "0x4a000000"
	connMask = "0xff000000"
)

// gatewayMarkConn stamps the mark into the top byte of the connection mark and
// leaves the other three bytes alone. nft prints it back as
// `ct mark & 0x4affffff | 0x4a000000`, which is the same operation: the top
// byte becomes 0x4a whatever it held.
const gatewayMarkConn = "ct mark set ct mark and 0x00ffffff or " + connMark

// The source-translation choices a forward carries. Auto is stored resolved
// (see storeNAT), so the renderer never has to look at the host.
const (
	natAuto   = "auto"
	natAlways = "always"
	natNever  = "never"
)

// storeNAT is how a forward's SourceNAT is written into the spec.
//
// "Auto" depends on the host's networks — is the target one this machine is
// the gateway of — and a renderer that read them would no longer be a pure
// function of the spec, which is what the golden tests and the boot files
// rely on. ForwardSpec has no field for the answer, so the answer rides in
// the value: auto is stored as "auto:always" or "auto:never", decided when the
// entry is made or edited and re-decided each time the gateway is changed.
// The pages see the choice (auto) and the effect (masquerade) as two fields.
func storeNAT(choice string, masquerade bool) string {
	if choice != natAuto {
		return choice
	}
	if masquerade {
		return natAuto + ":" + natAlways
	}
	return natAuto + ":" + natNever
}

// splitNAT reads a stored SourceNAT back into the operator's choice and
// whether the flow is masqueraded. A bare "auto" — a hand-edited spec — is
// masqueraded: the reply then comes back through here in every topology, at
// the price of losing the visitor's address.
func splitNAT(stored string) (choice string, masquerade bool, ok bool) {
	switch stored {
	case natAlways:
		return natAlways, true, true
	case natNever:
		return natNever, false, true
	case natAuto, natAuto + ":" + natAlways:
		return natAuto, true, true
	case natAuto + ":" + natNever:
		return natAuto, false, true
	}
	return "", false, false
}

// needsAdmission reports whether anything in the spec translates traffic that
// the host's other filters would otherwise drop on its way through.
func needsAdmission(sp *Spec) bool {
	for _, f := range sp.Forwards {
		if f.Enabled {
			return true
		}
	}
	for _, n := range sp.NAT {
		if n.Enabled {
			return true
		}
	}
	return false
}

// admissionChains are the iptables chains a marked connection is admitted
// in: FORWARD for a forward to another machine or a tunnel's traffic out,
// INPUT for a forward to a port on this one, and DOCKER-USER because Docker
// drops traffic to container ports it did not publish before FORWARD's later
// rules are reached.
var admissionChains = []string{"FORWARD", "INPUT", "DOCKER-USER"}

// admissionRule is the match and verdict, without the command or the chain.
func admissionRule() []string {
	return []string{"-m", "connmark", "--mark", connMark + "/" + connMask,
		"-m", "comment", "--comment", "just-dashboard-gateway", "-j", "ACCEPT"}
}

// admissionCommands are the iptables and ip6tables invocations that remove the
// admission rule from every chain and, when insert is true, put it back at
// the top. Delete first, so running them twice leaves one rule, not two.
func admissionCommands(insert bool) [][]string {
	var out [][]string
	for _, tool := range []string{"iptables", "ip6tables"} {
		for _, chain := range admissionChains {
			out = append(out, append([]string{tool, "-D", chain}, admissionRule()...))
			if insert {
				out = append(out, append([]string{tool, "-I", chain, "1"}, admissionRule()...))
			}
		}
	}
	return out
}

// gatewayListDir is where a fetched blocklist's networks are cached, one
// file per list. The renderer reads them from here because the spec holds
// only a fetched list's name and count (spec.go: a country list is tens of
// thousands of lines), while renderAll — which the commit path calls and this
// package does not own — hands the renderer nothing but the spec and the
// trusted set. New sets it once from the module's paths; mutations never
// change it.
var gatewayListDir string

// blocklistFile is the cache file of one list.
func blocklistFile(dir string, id int) string {
	return filepath.Join(dir, fmt.Sprintf("%d.txt", id))
}

// readBlocklist is the convenience read used by callers that also obtain
// cache health. Rendering uses checkedBlocklist and refuses unhealthy data.
func readBlocklist(dir string, id int) []netip.Prefix {
	nets, _ := readBlocklistChecked(dir, id)
	return nets
}

// blocklistEntries is what a blocklist drops: a manual list's own entries, or a
// fetched list's cache.
func blocklistEntries(dir string, bl BlocklistSpec) []netip.Prefix {
	if bl.Kind == "manual" {
		var out []netip.Prefix
		for _, raw := range bl.Entries {
			if p, err := ParsePrefix(raw); err == nil {
				out = append(out, p)
			}
		}
		return mergePrefixes(out)
	}
	return cachedBlocklist(dir, bl.ID)
}

// blocklistMemo keeps a fetched list's parsed, merged networks beside the file
// they came from. A country or feed list is up to half a million lines, and
// the Protection and Gateway pages are polled: reading and sorting a list
// again for each poll to learn whether it holds the reader's address would
// cost more than everything else those routes do. The key is the file's
// modification time and size, so a refresh that rewrites it is seen at once.
var blocklistMemo = struct {
	sync.Mutex
	byPath map[string]memoizedList
}{byPath: map[string]memoizedList{}}

type memoizedList struct {
	mtime      int64
	size       int64
	mode       os.FileMode
	ctime      int64
	info       os.FileInfo
	nets       []netip.Prefix
	generation string
}

// blocklistParses counts the times a list file was actually read, for the
// test that pins the memo.
var blocklistParses atomic.Int64

// cachedBlocklist returns a list's networks, merged. The slice is shared with
// the memo and must not be changed.
func cachedBlocklist(dir string, id int) []netip.Prefix {
	nets, _ := checkedBlocklist(dir, id)
	return nets
}

// forgetBlocklist drops a list's memo, when the list or its file goes.
func forgetBlocklist(dir string, id int) {
	blocklistMemo.Lock()
	delete(blocklistMemo.byPath, blocklistFile(dir, id))
	blocklistMemo.Unlock()
}

// renderGateway renders the gateway table. Loading the file replaces the
// table in one transaction: the table is declared (so the delete that follows
// never fails on a host where it does not exist yet), deleted, and defined
// again whole.
func renderGateway(sp *Spec, trusted []netip.Prefix) (string, error) {
	return renderGatewayCandidate(sp, trusted, nil)
}

func renderGatewayCandidate(sp *Spec, trusted []netip.Prefix, candidate map[int][]netip.Prefix) (string, error) {
	lists := map[int][]netip.Prefix{}
	for _, bl := range sp.Blocklists {
		if !bl.Enabled {
			continue
		}
		if nets, ok := candidate[bl.ID]; ok {
			lists[bl.ID] = nets
			continue
		}
		nets, health := blocklistData(gatewayListDir, bl)
		if health.Status != "ready" {
			return "", fmt.Errorf("blocklist %d (%s) cannot be rendered safely: %s", bl.ID, bl.Name, health.Error)
		}
		lists[bl.ID] = nets
	}
	return renderGatewayWith(sp, trusted, func(bl BlocklistSpec) []netip.Prefix {
		return lists[bl.ID]
	})
}

// renderGatewayWith is renderGateway with the blocklists' networks supplied,
// so the golden test pins the output without touching the disk.
//
// Chains exist only when they hold a rule. An idle table with five hooked
// chains costs every packet five hook traversals, and the nat hooks pull
// connection tracking into a host that may not otherwise use it; a table
// holding nothing but the trusted sets costs nothing, and is what is loaded
// at boot on a host that has made no gateway entries.
func renderGatewayWith(sp *Spec, trusted []netip.Prefix, lists func(BlocklistSpec) []netip.Prefix) (string, error) {
	forwards, nats, limits, err := normalizeGateway(sp)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(generatedHeader)
	fmt.Fprintf(&b, "table inet %s\ndelete table inet %s\n", gatewayTable, gatewayTable)
	fmt.Fprintf(&b, "table inet %s {\n", gatewayTable)
	v4, v6 := splitFamilies(trusted)
	writeSet(&b, "trusted4", "ipv4_addr", v4)
	writeSet(&b, "trusted6", "ipv6_addr", v6)

	var pre []string
	excepted := map[int][]string{}
	for _, bl := range sp.Blocklists {
		if !bl.Enabled {
			continue
		}
		l4, l6 := splitFamilies(lists(bl))
		writeSet(&b, fmt.Sprintf("bl_%d_4", bl.ID), "ipv4_addr", l4)
		writeSet(&b, fmt.Sprintf("bl_%d_6", bl.ID), "ipv6_addr", l6)
		// A list with its own exceptions drops through a chain of its own,
		// so an excepted source returns past this list and is still judged
		// by the next one. A list without exceptions keeps its single rule.
		if rules := exceptionRules(sp, fmt.Sprintf("blocklist:%d", bl.ID)); len(rules) > 0 {
			excepted[bl.ID] = rules
			pre = append(pre,
				fmt.Sprintf("ip saddr @bl_%d_4 jump bx_%d", bl.ID, bl.ID),
				fmt.Sprintf("ip6 saddr @bl_%d_6 jump bx_%d", bl.ID, bl.ID))
			continue
		}
		pre = append(pre,
			fmt.Sprintf("ip saddr @bl_%d_4 counter drop comment \"blocklist:%d\"", bl.ID, bl.ID),
			fmt.Sprintf("ip6 saddr @bl_%d_6 counter drop comment \"blocklist:%d\"", bl.ID, bl.ID))
	}
	allowed := allowRules(sp)

	var guard, routed []string
	for _, l := range limits {
		if !l.Enabled {
			continue
		}
		if l.MaxConnections > 0 {
			writeGatewayDynamicSet(&b, fmt.Sprintf("conn_%d_4", l.ID), "ipv4_addr", "")
			writeGatewayDynamicSet(&b, fmt.Sprintf("conn_%d_6", l.ID), "ipv6_addr", "")
		}
		if l.Rate > 0 && l.PerSource {
			writeGatewayDynamicSet(&b, fmt.Sprintf("lim_%d_4", l.ID), "ipv4_addr", gatewayLimitTimeout(l.Per))
			writeGatewayDynamicSet(&b, fmt.Sprintf("lim_%d_6", l.ID), "ipv6_addr", gatewayLimitTimeout(l.Per))
		}
		guard = append(guard, gatewayLimitRules(l, false)...)
		routed = append(routed, gatewayLimitRules(l, true)...)
	}

	// Marking a NAT entry's traffic happens in the forward hook, before the
	// established/trusted returns: a flow from a trusted tunnel must still be
	// admitted, and only the forward hook sees nothing but traffic passing
	// through, so a tunnel's packets for this host's own ports are never
	// admitted past the firewall's input rules by it.
	var marks []string
	for _, n := range nats {
		if !n.Enabled {
			continue
		}
		src, _ := ParsePrefix(n.Source)
		marks = append(marks, fmt.Sprintf("%s %s%s oifname %q ct state new %s comment \"nat-mark:%d\"",
			gwSaddr(src.Addr()), src.Masked(), gwDestinations(src.Addr(), n.Destinations), n.Interface, gatewayMarkConn, n.ID))
	}

	returns := []string{"ct state established,related return", "ip saddr @trusted4 return", "ip6 saddr @trusted6 return"}
	if len(pre) > 0 {
		// Mangle priority, after connection tracking (-200): at raw priority a
		// drop has no connection state to consult, so a list taking an address
		// would cut the sessions already open with it and drop the replies to
		// this server's own outbound connections (DNS, ACME, updates). Only a
		// new connection from a listed network is refused.
		head := []string{"ct state established,related return", "iif \"lo\" return", "ip saddr @trusted4 return", "ip6 saddr @trusted6 return"}
		writeGatewayChain(&b, "pre", "type filter hook prerouting priority mangle; policy accept;",
			append(append(head, allowed...), pre...))
		for _, bl := range sp.Blocklists {
			if rules, ok := excepted[bl.ID]; ok && bl.Enabled {
				writeGatewayRegularChain(&b, fmt.Sprintf("bx_%d", bl.ID),
					append(rules, fmt.Sprintf("counter drop comment \"blocklist:%d\"", bl.ID)))
			}
		}
	}
	if len(guard) > 0 {
		writeGatewayChain(&b, "input", "type filter hook input priority filter - 10; policy accept;", append(append(append([]string{}, returns...), allowed...), guard...))
	}
	var fwd []string
	fwd = append(fwd, marks...)
	if len(routed) > 0 {
		fwd = append(fwd, returns...)
		fwd = append(fwd, allowed...)
		fwd = append(fwd, routed...)
	}
	if len(fwd) > 0 {
		writeGatewayChain(&b, "forward", "type filter hook forward priority filter - 10; policy accept;", fwd)
	}

	var dnat, snat []string
	for _, f := range forwards {
		if !f.Enabled {
			continue
		}
		dnat = append(dnat, gatewayForwardRules(f)...)
		if _, masq, _ := splitNAT(f.SourceNAT); masq {
			snat = append(snat, gatewayForwardMasqRules(f)...)
		}
	}
	// A mapped entry's inbound half comes after every forward: a forward
	// names a port, the mapping takes the whole address.
	for _, n := range nats {
		if n.Enabled && natMapped(n) {
			dnat = append(dnat, gatewayNATInboundRule(n))
		}
	}
	for _, n := range nats {
		if n.Enabled {
			snat = append(snat, gatewayNATRule(n))
		}
	}
	if len(dnat) > 0 {
		writeGatewayChain(&b, "nat_pre", "type nat hook prerouting priority dstnat - 10; policy accept;", dnat)
	}
	if len(snat) > 0 {
		writeGatewayChain(&b, "nat_post", "type nat hook postrouting priority srcnat - 10; policy accept;", snat)
	}
	b.WriteString("}\n")
	return b.String(), nil
}

// writeGatewayChain writes one base chain. The chains are named pre, input, forward,
// nat_pre and nat_post because dnat and snat are keywords nft will not take
// as a name.
func writeGatewayChain(b *strings.Builder, name, header string, rules []string) {
	fmt.Fprintf(b, "\tchain %s {\n\t\t%s\n", name, header)
	for _, r := range rules {
		fmt.Fprintf(b, "\t\t%s\n", r)
	}
	b.WriteString("\t}\n")
}

// writeGatewayRegularChain writes a chain no hook reaches, only a jump.
func writeGatewayRegularChain(b *strings.Builder, name string, rules []string) {
	fmt.Fprintf(b, "\tchain %s {\n", name)
	for _, r := range rules {
		fmt.Fprintf(b, "\t\t%s\n", r)
	}
	b.WriteString("\t}\n")
}

// writeSet writes an interval set, with its elements when it has any.
func writeSet(b *strings.Builder, name, typ string, elems []netip.Prefix) {
	fmt.Fprintf(b, "\tset %s {\n\t\ttype %s\n\t\tflags interval\n\t\tauto-merge\n", name, typ)
	if len(elems) > 0 {
		fmt.Fprintf(b, "\t\telements = { %s }\n", prefixList(elems))
	}
	b.WriteString("\t}\n")
}

// writeGatewayDynamicSet writes a set whose elements the rules add as sources
// arrive: a per-source rate meter, or the connection counter. The meters time
// out so a scan that used ten thousand addresses is forgotten; the connection
// counters take no timeout because an entry goes when its count is zero.
func writeGatewayDynamicSet(b *strings.Builder, name, typ, timeout string) {
	fmt.Fprintf(b, "\tset %s {\n\t\ttype %s\n\t\tflags dynamic\n", name, typ)
	if timeout != "" {
		fmt.Fprintf(b, "\t\ttimeout %s\n", timeout)
	}
	b.WriteString("\t\tsize 65535\n\t}\n")
}

// gatewayLimitTimeout is how long an idle source's meter is kept: a minute at least,
// and long enough that a per-hour meter is not forgotten between hits. Each
// hit refreshes it (the rules use update), so a source still sending keeps
// its state.
func gatewayLimitTimeout(per string) string {
	switch per {
	case "hour":
		return "2h"
	case "minute":
		return "2m"
	}
	return "1m"
}

// gwProtocols is the l4 gwProtocols a "both" expands to. Every rule is written
// once per protocol rather than against a set of them: nft will not take a
// port mapping after a protocol set, and a reject's kind depends on the
// protocol.
func gwProtocols(p string) []string {
	if p == "both" {
		return []string{"tcp", "udp"}
	}
	return []string{p}
}

// saddr names the source match of an address's family.
func gwSaddr(a netip.Addr) string {
	if a.Is4() {
		return "ip saddr"
	}
	return "ip6 saddr"
}

// gatewayLimitRules is a limit's rules for the input chain, or for the forward
// chain when forward is set.
//
// In the forward chain a limit applies only to flows a destination translation
// carried (ct status dnat): a published or forwarded port. Without that, a
// limit on port 443 would throttle every container's and VPN client's
// outbound connections to any remote host's 443.
func gatewayLimitRules(l LimitSpec, forward bool) []string {
	var out []string
	for _, proto := range gwProtocols(l.Protocol) {
		// ct original proto-dst, not dport: a connection the forwards below
		// (or Docker's published ports) translated reaches the forward chain
		// with its destination port already rewritten, and the operator
		// limits the port the visitor used.
		match := fmt.Sprintf("meta l4proto %s ct original proto-dst %s ct state new", proto, l.Ports)
		if forward {
			match = fmt.Sprintf("meta l4proto %s ct original proto-dst %s ct status dnat ct state new", proto, l.Ports)
		}
		action := "drop"
		if l.Action == "reject" {
			action = "reject"
			if proto == "tcp" {
				action = "reject with tcp reset"
			}
		}
		tail := fmt.Sprintf("counter %s comment \"limit:%d\"", action, l.ID)
		if l.MaxConnections > 0 {
			for _, fam := range []string{"4", "6"} {
				out = append(out, fmt.Sprintf("%s add @conn_%d_%s { %s ct count over %d } %s",
					match, l.ID, fam, gwFamSaddr(fam), l.MaxConnections, tail))
			}
		}
		// The ceiling for everyone together has a comment of its own so its
		// refusals read apart from one address's.
		if l.GlobalConnections > 0 {
			out = append(out, fmt.Sprintf("%s ct count over %d counter %s comment \"limit-global:%d\"",
				match, l.GlobalConnections, action, l.ID))
		}
		if l.Rate > 0 {
			// nft refuses "burst 0"; leaving the clause out is its own
			// default of five packets, which is what a burst of zero means
			// here.
			rate := fmt.Sprintf("limit rate over %d/%s", l.Rate, l.Per)
			if l.Burst > 0 {
				rate += fmt.Sprintf(" burst %d packets", l.Burst)
			}
			if !l.PerSource {
				out = append(out, fmt.Sprintf("%s %s %s", match, rate, tail))
				continue
			}
			for _, fam := range []string{"4", "6"} {
				out = append(out, fmt.Sprintf("%s update @lim_%d_%s { %s %s } %s",
					match, l.ID, fam, gwFamSaddr(fam), rate, tail))
			}
		}
	}
	return out
}

func gwFamSaddr(fam string) string {
	if fam == "4" {
		return "ip saddr"
	}
	return "ip6 saddr"
}

// gatewayForwardRules is a forward's rule in the nat_pre chain. The mark is set
// before the translation: a rule after a dnat statement is never reached.
func gatewayForwardRules(f ForwardSpec) []string {
	target, _ := ParseAddr(f.Target)
	var out []string
	for _, proto := range gwProtocols(f.Protocol) {
		var m []string
		if f.Interface != "" {
			m = append(m, fmt.Sprintf("iifname %q", f.Interface))
		}
		if len(f.Sources) > 0 {
			m = append(m, fmt.Sprintf("%s %s", gwSaddr(target), gwBraces(f.Sources)))
		}
		// fib daddr type local: only traffic addressed to this host. Without
		// it a forward of 5432 would also capture a container's or a VPN
		// client's outbound connection to any remote host's 5432, which is
		// merely passing through.
		m = append(m, "fib daddr type local", fmt.Sprintf("meta l4proto %s th dport %s", proto, f.Ports), gatewayMarkConn, "counter")
		out = append(out, fmt.Sprintf("%s %s comment \"forward:%d\"", strings.Join(m, " "), gatewayDNATTo(target, f.TargetPort), f.ID))
	}
	return out
}

// gatewayDNATTo is the translation statement: the address, and the port when the
// forward names one. A port range keeps its numbers one for one (the kernel
// leaves a port alone when it is already inside the range it is given), which
// is why validation allows a range only to itself.
func gatewayDNATTo(target netip.Addr, port string) string {
	switch {
	case target.Is4() && port == "":
		return "dnat ip to " + target.String()
	case target.Is4():
		return fmt.Sprintf("dnat ip to %s:%s", target, port)
	case port == "":
		return "dnat ip6 to " + target.String()
	}
	return fmt.Sprintf("dnat ip6 to [%s]:%s", target, port)
}

// gatewayForwardMasqRules translates the source of exactly the flows a forward
// created: marked, destination-translated, and heading for its target and
// port. Nothing else passing the postrouting hook is touched.
func gatewayForwardMasqRules(f ForwardSpec) []string {
	target, _ := ParseAddr(f.Target)
	port := f.TargetPort
	if port == "" {
		port = f.Ports
	}
	daddr := "ip daddr"
	if target.Is6() {
		daddr = "ip6 daddr"
	}
	var out []string
	for _, proto := range gwProtocols(f.Protocol) {
		out = append(out, fmt.Sprintf("ct status dnat ct mark and %s == %s %s %s meta l4proto %s th dport %s counter masquerade comment \"forward-nat:%d\"",
			connMask, connMark, daddr, target, proto, port, f.ID))
	}
	return out
}

func gatewayNATRule(n NATSpec) string {
	src, _ := ParsePrefix(n.Source)
	verdict := "masquerade"
	switch {
	case natMapped(n):
		to, _ := ParsePrefix(n.Translated)
		verdict = "snat " + gwNATFamily(src.Addr()) + gwNATTarget(to.Masked())
	case n.ToAddress != "":
		to, _ := ParseAddr(n.ToAddress)
		verdict = "snat ip to " + to.String()
		if to.Is6() {
			verdict = "snat ip6 to " + to.String()
		}
	}
	return fmt.Sprintf("%s %s%s oifname %q counter %s comment \"nat:%d\"", gwSaddr(src.Addr()), src.Masked(), gwDestinations(src.Addr(), n.Destinations), n.Interface, verdict, n.ID)
}

// gatewayNATInboundRule is a mapped entry's other direction: a connection
// arriving for the public side is sent to the private one and marked, so the
// firewall's chains admit it the way they admit a port forward.
func gatewayNATInboundRule(n NATSpec) string {
	src, _ := ParsePrefix(n.Source)
	to, _ := ParsePrefix(n.Translated)
	daddr := "ip daddr"
	if to.Addr().Is6() {
		daddr = "ip6 daddr"
	}
	return fmt.Sprintf("iifname %q %s %s %s counter dnat %s%s comment \"nat-in:%d\"",
		n.Interface, daddr, gwPrefixWord(to.Masked()), gatewayMarkConn, gwNATFamily(src.Addr()), gwNATTarget(src.Masked()), n.ID)
}

// natMapped reports an entry that translates both directions.
func natMapped(n NATSpec) bool { return n.Mode == natOneToOne || n.Mode == natNPTv6 }

const (
	natOneToOne = "one-to-one"
	natNPTv6    = "nptv6"
)

func gwNATFamily(a netip.Addr) string {
	if a.Is4() {
		return "ip "
	}
	return "ip6 "
}

// gwNATTarget is a translation's destination: one address, or a whole network
// mapped host for host (nft's prefix NAT, a stateful netmap).
func gwNATTarget(p netip.Prefix) string {
	if p.IsSingleIP() {
		return "to " + p.Addr().String()
	}
	return "prefix to " + p.String()
}

// gwPrefixWord is a network as nft takes it in a match: bare for one address.
func gwPrefixWord(p netip.Prefix) string {
	if p.IsSingleIP() {
		return p.Addr().String()
	}
	return p.String()
}

// gwDestinations is a NAT entry's destination match, empty when it has none.
func gwDestinations(src netip.Addr, dests []string) string {
	if len(dests) == 0 {
		return ""
	}
	daddr := " ip daddr "
	if src.Is6() {
		daddr = " ip6 daddr "
	}
	return daddr + gwBraces(dests)
}

// braces writes a list of networks as an nft anonymous set, or bare when
// there is one.
func gwBraces(nets []string) string {
	if len(nets) == 1 {
		return nets[0]
	}
	return "{ " + strings.Join(nets, ", ") + " }"
}

// normalizeGateway checks every gateway entry and returns each in canonical
// form. The spec is a file an operator can edit, and everything below is
// written into an nft ruleset, so the renderer trusts nothing in it: an
// entry that does not pass is an error that stops the render, never text in
// a ruleset.
func normalizeGateway(sp *Spec) ([]ForwardSpec, []NATSpec, []LimitSpec, error) {
	forwards := make([]ForwardSpec, 0, len(sp.Forwards))
	for _, f := range sp.Forwards {
		n, err := normForward(f)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("forward %d (%s): %w", f.ID, f.Name, err)
		}
		forwards = append(forwards, n)
	}
	nats := make([]NATSpec, 0, len(sp.NAT))
	for _, n := range sp.NAT {
		c, err := normNAT(n)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("NAT entry %d (%s): %w", n.ID, n.Name, err)
		}
		nats = append(nats, c)
	}
	limits := make([]LimitSpec, 0, len(sp.Limits))
	for _, l := range sp.Limits {
		c, err := normLimit(l)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("limit %d (%s): %w", l.ID, l.Name, err)
		}
		limits = append(limits, c)
	}
	for _, bl := range sp.Blocklists {
		if err := checkBlocklist(bl); err != nil {
			return nil, nil, nil, fmt.Errorf("blocklist %d (%s): %w", bl.ID, bl.Name, err)
		}
	}
	if err := checkExceptions(sp); err != nil {
		return nil, nil, nil, err
	}
	return forwards, nats, limits, nil
}

// normForward validates a forward and returns it in the form it is stored and
// rendered in.
func normForward(f ForwardSpec) (ForwardSpec, error) {
	var err error
	if f.Name, err = CleanLabel(f.Name, 64); err != nil {
		return f, err
	}
	if f.Protocol, err = ParseProtocol(f.Protocol, true); err != nil {
		return f, err
	}
	f.Interface = strings.TrimSpace(f.Interface)
	if f.Interface != "" {
		if err := ValidIfName(f.Interface); err != nil {
			return f, err
		}
	}
	if f.Ports, err = ParsePorts(f.Ports); err != nil {
		return f, err
	}
	target, err := ParseAddr(f.Target)
	if err != nil {
		return f, err
	}
	switch {
	case target.IsLoopback():
		return f, fmt.Errorf("a forward to %s cannot work: the kernel does not route translated traffic to the loopback address unless route_localnet is set, which also exposes every service bound to it. Forward to the address the service listens on", target)
	case target.IsUnspecified() || target.IsMulticast():
		return f, fmt.Errorf("%s is not an address a forward can send traffic to", target)
	}
	f.Target = target.String()
	if f.TargetPort = strings.TrimSpace(f.TargetPort); f.TargetPort != "" {
		if f.TargetPort, err = ParsePorts(f.TargetPort); err != nil {
			return f, err
		}
		in, out := portSpan(f.Ports), portSpan(f.TargetPort)
		switch {
		case in != out:
			return f, fmt.Errorf("the ports are %d wide and the target ports %d: a range must be sent to a range of the same size", in, out)
		case in > 1 && f.TargetPort != f.Ports:
			return f, fmt.Errorf("a range keeps its port numbers one for one: leave the target port empty, or give the same range. To move a port, forward one port at a time")
		}
	}
	var sources []string
	seen := map[string]bool{}
	for _, raw := range f.Sources {
		p, err := ParsePrefix(raw)
		if err != nil {
			return f, err
		}
		if p.Addr().Is4() != target.Is4() {
			return f, fmt.Errorf("%s is not the same kind of address as the target %s", raw, target)
		}
		if s := p.Masked().String(); !seen[s] {
			seen[s] = true
			sources = append(sources, s)
		}
	}
	f.Sources = sources
	if _, _, ok := splitNAT(f.SourceNAT); !ok {
		return f, fmt.Errorf("source translation is auto, always or never")
	}
	return f, nil
}

// normNAT validates a NAT entry.
func normNAT(n NATSpec) (NATSpec, error) {
	var err error
	if n.Name, err = CleanLabel(n.Name, 64); err != nil {
		return n, err
	}
	src, err := ParsePrefix(n.Source)
	if err != nil {
		return n, err
	}
	if src.Bits() == 0 {
		return n, fmt.Errorf("a source of %s would translate everything this server sends; name the network behind it", src)
	}
	n.Source = src.Masked().String()
	if err := ValidIfName(n.Interface); err != nil {
		return n, err
	}
	n.ToAddress, n.Translated = strings.TrimSpace(n.ToAddress), strings.TrimSpace(n.Translated)
	switch n.Mode {
	case "", "masquerade", "snat":
		n.Mode = ""
		if n.Translated != "" {
			return n, fmt.Errorf("a public address or network belongs to a one-to-one or prefix entry")
		}
	case natOneToOne, natNPTv6:
		return normMappedNAT(n, src.Masked())
	default:
		return n, fmt.Errorf("the translation is masquerade, snat, one-to-one or nptv6")
	}
	if n.ToAddress != "" {
		to, err := ParseAddr(n.ToAddress)
		if err != nil {
			return n, err
		}
		if to.Is4() != src.Addr().Is4() {
			return n, fmt.Errorf("the translated address %s is not the same kind of address as the source %s", to, src)
		}
		n.ToAddress = to.String()
	}
	dests, err := normNATDestinations(src.Addr(), n.Destinations)
	if err != nil {
		return n, err
	}
	n.Destinations = dests
	return n, nil
}

// maxNATDestinations bounds a policy entry's destination networks.
const maxNATDestinations = 16

func normNATDestinations(src netip.Addr, raw []string) ([]string, error) {
	if len(raw) > maxNATDestinations {
		return nil, fmt.Errorf("a NAT entry names at most %d destination networks", maxNATDestinations)
	}
	var out []netip.Prefix
	for _, r := range raw {
		p, err := ParsePrefix(r)
		if err != nil {
			return nil, err
		}
		if p.Addr().Is4() != src.Is4() {
			return nil, fmt.Errorf("the destination %s is not the same kind of address as the source", r)
		}
		if p.Bits() == 0 {
			return nil, fmt.Errorf("a destination of %s is every address; leave the destinations empty instead", p)
		}
		out = append(out, p.Masked())
	}
	var dests []string
	for _, p := range mergePrefixes(out) {
		dests = append(dests, p.String())
	}
	return dests, nil
}

// normMappedNAT validates an entry that translates both directions: a
// private address or network and a public one of exactly the same width,
// which nft maps host for host.
func normMappedNAT(n NATSpec, src netip.Prefix) (NATSpec, error) {
	if n.ToAddress != "" || len(n.Destinations) > 0 {
		return n, fmt.Errorf("a %s entry maps every connection between its two sides; it takes no fixed source address or destinations", n.Mode)
	}
	if n.Translated == "" {
		return n, fmt.Errorf("a %s entry needs the public address or network it maps to", n.Mode)
	}
	to, err := ParsePrefix(n.Translated)
	if err != nil {
		return n, err
	}
	to = to.Masked()
	switch {
	case to.Addr().Is4() != src.Addr().Is4():
		return n, fmt.Errorf("the public side %s is not the same kind of address as %s", to, src)
	case to.Bits() != src.Bits():
		return n, fmt.Errorf("the public side is a /%d and the private side a /%d: a mapping translates host for host, so both are the same size", to.Bits(), src.Bits())
	case to.Overlaps(src):
		return n, fmt.Errorf("%s and %s overlap; a network cannot be mapped onto itself", src, to)
	}
	if n.Mode == natNPTv6 {
		if src.Addr().Is4() || src.Bits() < 16 || src.Bits() > 64 {
			return n, fmt.Errorf("IPv6 prefix translation maps an IPv6 network between /16 and /64")
		}
	} else if (src.Addr().Is4() && src.Bits() < 24) || (src.Addr().Is6() && src.Bits() < 112) {
		return n, fmt.Errorf("a one-to-one mapping covers at most 256 addresses (a /24, or a /112 in IPv6); use IPv6 prefix translation for a larger IPv6 network")
	}
	n.Translated = to.String()
	return n, nil
}

// normLimit validates a limit.
func normLimit(l LimitSpec) (LimitSpec, error) {
	var err error
	if l.Name, err = CleanLabel(l.Name, 64); err != nil {
		return l, err
	}
	if l.Protocol, err = ParseProtocol(l.Protocol, true); err != nil {
		return l, err
	}
	if l.Ports, err = ParsePorts(l.Ports); err != nil {
		return l, err
	}
	if l.Rate < 0 || l.Rate > 1_000_000 {
		return l, fmt.Errorf("the rate is between 1 and 1000000 new connections")
	}
	if l.Burst < 0 || l.Burst > 1_000_000 {
		return l, fmt.Errorf("the burst is between 0 and 1000000")
	}
	if l.MaxConnections < 0 || l.MaxConnections > 1_000_000 {
		return l, fmt.Errorf("the connection limit is between 1 and 1000000")
	}
	if l.GlobalConnections < 0 || l.GlobalConnections > 10_000_000 {
		return l, fmt.Errorf("the ceiling for everyone together is between 1 and 10000000")
	}
	if l.Rate == 0 && l.MaxConnections == 0 && l.GlobalConnections == 0 {
		return l, fmt.Errorf("a limit needs a rate, a number of connections, or both")
	}
	if l.Profile != "" {
		if _, ok := limitProfileFor(l.Profile); !ok {
			return l, fmt.Errorf("%q is not a service profile this dashboard offers", l.Profile)
		}
	}
	if l.Rate > 0 {
		switch l.Per {
		case "second", "minute", "hour":
		default:
			return l, fmt.Errorf("the rate is per second, minute or hour")
		}
	} else {
		l.Per, l.Burst, l.PerSource = "", 0, false
	}
	switch l.Action {
	case "drop", "reject":
	default:
		return l, fmt.Errorf("the action is drop or reject")
	}
	return l, nil
}

// checkBlocklist validates what is rendered of a list: its kind and, for a
// manual list, its entries. A fetched list's entries are read through
// ParsePrefix when its cache is, so nothing unparsed reaches the ruleset.
func checkBlocklist(bl BlocklistSpec) error {
	if _, err := CleanLabel(bl.Name, 64); err != nil {
		return err
	}
	switch bl.Kind {
	case "manual":
		for _, e := range bl.Entries {
			if _, err := ParsePrefix(e); err != nil {
				return err
			}
		}
	case "country", "feed":
	default:
		return fmt.Errorf("a list is manual, a country or a feed")
	}
	return nil
}

// stamp is the Made record of an entry created now.
func gwStamp(actor string) Made {
	return Made{CreatedAt: time.Now().UTC(), CreatedBy: actor}
}
