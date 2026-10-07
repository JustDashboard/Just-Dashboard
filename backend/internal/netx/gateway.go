package netx

import (
	"fmt"
	"net/netip"
	"strings"
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

// renderGateway renders the gateway table. Loading the file replaces the
// table in one transaction: the table is declared (so the delete that follows
// never fails on a host where it does not exist yet), deleted, and defined
// again whole.
func renderGateway(sp *Spec, trusted []netip.Prefix) (string, error) {
	var b strings.Builder
	b.WriteString(generatedHeader)
	fmt.Fprintf(&b, "table inet %s\ndelete table inet %s\n", gatewayTable, gatewayTable)
	fmt.Fprintf(&b, "table inet %s {\n", gatewayTable)
	v4, v6 := splitFamilies(trusted)
	writeSet(&b, "trusted4", "ipv4_addr", v4)
	writeSet(&b, "trusted6", "ipv6_addr", v6)
	b.WriteString("}\n")
	return b.String(), nil
}

// writeSet writes an interval set, with its elements when it has any.
func writeSet(b *strings.Builder, name, typ string, elems []netip.Prefix) {
	fmt.Fprintf(b, "\tset %s {\n\t\ttype %s\n\t\tflags interval\n\t\tauto-merge\n", name, typ)
	if len(elems) > 0 {
		fmt.Fprintf(b, "\t\telements = { %s }\n", prefixList(elems))
	}
	b.WriteString("\t}\n")
}
