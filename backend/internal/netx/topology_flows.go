package netx

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"
)

// TrackedFlow is one connection-tracking entry: the tuple as the initiator
// sent it and the tuple the reply came back with. Where the two disagree the
// kernel translated the flow — a masquerade rewrites the original source, a
// port forward the original destination.
type TrackedFlow struct {
	Protocol uint8
	// OrigSrc/OrigDst are the original direction; ReplySrc/ReplyDst the reply.
	OrigSrc, OrigDst, ReplySrc, ReplyDst netip.Addr
	OrigDstPort                          uint16
	Status                               uint32
	// Bytes is both directions' bytes, zero when accounting is off.
	Bytes uint64
	// Counted is set when the entry carried accounting counters at all.
	Counted bool
}

// conntrack status bits (linux/netfilter/nf_conntrack_common.h).
const (
	ctStatusSrcNAT = 1 << 4
	ctStatusDstNAT = 1 << 5
)

// TopologyFlows is the Overview's flow-backed reading of the topology: which
// of its nodes are exchanging traffic, through which translation, as the
// kernel's connection tracking holds it at the moment of reading. It is not a
// packet capture and says nothing about switches outside this host.
type TopologyFlows struct {
	// State is ok, unavailable (no connection tracking to read) or failed.
	State  string    `json:"state"`
	Error  string    `json:"error,omitempty"`
	ReadAt time.Time `json:"readAt"`
	// Total is every entry read; Classified those drawn as an edge (loopback
	// and host-to-itself entries are not).
	Total      int  `json:"total"`
	Classified int  `json:"classified"`
	Truncated  bool `json:"truncated"`
	// Accounting is whether the entries carried byte counters
	// (net.netfilter.nf_conntrack_acct); without it edges count flows only.
	Accounting bool       `json:"accounting"`
	Edges      []FlowEdge `json:"edges"`
}

// FlowEdge is the traffic between two topology nodes, keyed by who started it.
type FlowEdge struct {
	// From started the flows and To answered them, as topology node ids:
	// "internet", "host", "link:<device>" or "docker:<network id>".
	From  string `json:"from"`
	To    string `json:"to"`
	Flows int    `json:"flows"`
	Bytes uint64 `json:"bytes,omitempty"`
	// Protocols are the transport protocols seen, by name.
	Protocols []string `json:"protocols"`
	// Translation is how the kernel rewrote the flows on the way: none,
	// masquerade (source rewritten) or forward (destination rewritten).
	Translation string `json:"translation"`
	// Via is the device a translated flow left or arrived through, where the
	// translated address names one.
	Via string `json:"via,omitempty"`
	// Path is the effective path, hop by hop, in words.
	Path []string `json:"path"`
}

// flowLimit bounds how many entries one reading parses; a busy gateway holds
// hundreds of thousands, and the edges are a summary.
const flowLimit = 65536

// readTrackedFlows is the connection-tracking dump; a variable so tests stand
// recorded entries behind it.
var readTrackedFlows = dumpConntrack

// TopologyFlows reads connection tracking and summarises it as edges between
// the nodes the Overview draws.
func (s *Service) TopologyFlows(ctx context.Context, links []Link, docker []DockerNet) TopologyFlows {
	out := TopologyFlows{State: "ok", ReadAt: time.Now().UTC(), Edges: []FlowEdge{}}
	flows, truncated, err := readTrackedFlows(ctx, flowLimit)
	if err != nil {
		var missing *UnavailableError
		if errors.As(err, &missing) {
			out.State = "unavailable"
		} else {
			out.State = "failed"
		}
		out.Error = trimFlowError(err)
		return out
	}
	out.Total, out.Truncated = len(flows), truncated
	out.Edges, out.Classified, out.Accounting = classifyFlows(flows, links, docker)
	return out
}

// topologyPlace is where an address sits in the picture.
type topologyPlace struct {
	node  string
	label string
	// device is the host device it is reached through, for the path.
	device string
}

// classifyFlows is the pure half of TopologyFlows: each entry's initiator
// and responder are placed on a node and the entries are summed per pair.
func classifyFlows(flows []TrackedFlow, links []Link, docker []DockerNet) ([]FlowEdge, int, bool) {
	type subnet struct {
		prefix netip.Prefix
		place  topologyPlace
	}
	var subnets []subnet
	local := map[netip.Addr]string{}
	var uplink string
	for _, l := range links {
		if l.Uplink && uplink == "" {
			uplink = l.Name
		}
	}
	for _, n := range docker {
		for _, raw := range n.Subnets {
			if p, err := netip.ParsePrefix(raw); err == nil {
				subnets = append(subnets, subnet{p.Masked(), topologyPlace{node: "docker:" + n.ID, label: n.Name, device: n.Bridge}})
			}
		}
	}
	for _, l := range links {
		for _, a := range l.Addresses {
			p, err := netip.ParsePrefix(a.CIDR)
			if err != nil {
				continue
			}
			local[p.Addr().Unmap()] = l.Name
			if l.Uplink || l.Kind == "loopback" || a.Scope == "link" || l.Owner == "docker" {
				continue
			}
			node := ""
			switch {
			case l.Role == "tunnel":
				node = "link:" + l.Name
			case l.Master == "" && (l.Managed || l.Role == "bridge" || l.Role == "vlan" || (l.Role == "physical" && len(l.Addresses) > 0)):
				node = "link:" + l.Name
			}
			if node != "" {
				subnets = append(subnets, subnet{p.Masked(), topologyPlace{node: node, label: l.Name, device: l.Name}})
			}
		}
	}
	var tailscale string
	for _, l := range links {
		if l.Owner == "tailscale" {
			tailscale = l.Name
		}
	}
	tailnet6 := netip.MustParsePrefix("fd7a:115c:a1e0::/48")
	// The longest prefix answers, as the kernel's own lookup would.
	sort.SliceStable(subnets, func(i, j int) bool { return subnets[i].prefix.Bits() > subnets[j].prefix.Bits() })
	place := func(a netip.Addr) (topologyPlace, bool) {
		a = a.Unmap()
		if a.IsLoopback() || a.IsUnspecified() || a.IsMulticast() {
			return topologyPlace{}, false
		}
		if dev, ok := local[a]; ok {
			return topologyPlace{node: "host", label: "this server", device: dev}, true
		}
		for _, sn := range subnets {
			if sn.prefix.Contains(a) {
				return sn.place, true
			}
		}
		if tailscale != "" && (cgnat.Contains(a) || tailnet6.Contains(a)) {
			return topologyPlace{node: "link:" + tailscale, label: "the tailnet", device: tailscale}, true
		}
		return topologyPlace{node: "internet", label: "the internet", device: uplink}, true
	}

	type key struct{ from, to, translation string }
	edges := map[key]*FlowEdge{}
	protocols := map[key]map[string]bool{}
	classified, accounting := 0, false
	for _, f := range flows {
		if f.Counted {
			accounting = true
		}
		from, ok1 := place(f.OrigSrc)
		// A port forward rewrites the destination; who answered is the
		// reply's source.
		to, ok2 := place(f.ReplySrc)
		if !ok1 || !ok2 || from.node == to.node {
			continue
		}
		translation, via := "none", ""
		switch {
		case f.Status&ctStatusDstNAT != 0 || f.OrigDst != f.ReplySrc:
			translation = "forward"
			via = local[f.OrigDst.Unmap()]
		case f.Status&ctStatusSrcNAT != 0 || f.OrigSrc != f.ReplyDst:
			translation = "masquerade"
			via = local[f.ReplyDst.Unmap()]
		}
		k := key{from.node, to.node, translation}
		e := edges[k]
		if e == nil {
			e = &FlowEdge{From: from.node, To: to.node, Translation: translation, Via: via, Path: flowPath(from, to, translation, via)}
			edges[k] = e
			protocols[k] = map[string]bool{}
		}
		e.Flows++
		e.Bytes += f.Bytes
		protocols[k][protocolName(f.Protocol)] = true
		classified++
	}
	out := make([]FlowEdge, 0, len(edges))
	for k, e := range edges {
		for p := range protocols[k] {
			e.Protocols = append(e.Protocols, p)
		}
		sort.Strings(e.Protocols)
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Flows != out[j].Flows {
			return out[i].Flows > out[j].Flows
		}
		return out[i].From+out[i].To+out[i].Translation < out[j].From+out[j].To+out[j].Translation
	})
	return out, classified, accounting
}

// flowPath says the hops an edge's flows take through this host.
func flowPath(from, to topologyPlace, translation, via string) []string {
	hops := []string{from.label}
	if from.node != "host" && from.device != "" && from.device != from.label && from.device != via {
		hops = append(hops, from.device)
	}
	if from.node != "host" && to.node != "host" {
		hops = append(hops, "this server")
	}
	switch translation {
	case "masquerade":
		hops = append(hops, "masquerade"+viaSuffix(via))
	case "forward":
		hops = append(hops, "port forward"+viaSuffix(via))
	}
	if to.node != "host" && to.device != "" && to.device != to.label && to.device != via {
		hops = append(hops, to.device)
	}
	return append(hops, to.label)
}

func viaSuffix(device string) string {
	if device == "" {
		return ""
	}
	return " on " + device
}

func protocolName(p uint8) string {
	switch p {
	case 1:
		return "icmp"
	case 6:
		return "tcp"
	case 17:
		return "udp"
	case 58:
		return "icmpv6"
	case 132:
		return "sctp"
	}
	return fmt.Sprintf("ip-%d", p)
}

// The netlink attribute numbers of a conntrack entry
// (linux/netfilter/nfnetlink_conntrack.h).
const (
	ctaTupleOrig     = 1
	ctaTupleReply    = 2
	ctaStatus        = 3
	ctaCountersOrig  = 9
	ctaCountersReply = 10

	ctaTupleIP    = 1
	ctaTupleProto = 2

	ctaIPv4Src = 1
	ctaIPv4Dst = 2
	ctaIPv6Src = 3
	ctaIPv6Dst = 4

	ctaProtoNum     = 1
	ctaProtoDstPort = 3

	ctaCountersBytes = 2

	nlaTypeMask = 0x3fff
)

type ctTuple struct {
	src, dst netip.Addr
	proto    uint8
	dstPort  uint16
}

// parseConntrackEntry reads one entry's attributes (after the netfilter
// generic header).
func parseConntrackEntry(attrs []byte) (TrackedFlow, error) {
	var f TrackedFlow
	var orig, reply ctTuple
	err := walkAttributes(attrs, func(kind uint16, value []byte) error {
		switch kind {
		case ctaTupleOrig:
			t, err := parseTuple(value)
			orig = t
			return err
		case ctaTupleReply:
			t, err := parseTuple(value)
			reply = t
			return err
		case ctaStatus:
			if len(value) >= 4 {
				f.Status = binary.BigEndian.Uint32(value)
			}
		case ctaCountersOrig, ctaCountersReply:
			f.Counted = true
			return walkAttributes(value, func(kind uint16, value []byte) error {
				if kind == ctaCountersBytes && len(value) >= 8 {
					f.Bytes += binary.BigEndian.Uint64(value)
				}
				return nil
			})
		}
		return nil
	})
	if err != nil {
		return f, err
	}
	if !orig.src.IsValid() || !reply.src.IsValid() {
		return f, errors.New("a conntrack entry without both tuples")
	}
	f.Protocol, f.OrigSrc, f.OrigDst, f.OrigDstPort = orig.proto, orig.src, orig.dst, orig.dstPort
	f.ReplySrc, f.ReplyDst = reply.src, reply.dst
	return f, nil
}

func parseTuple(b []byte) (ctTuple, error) {
	var t ctTuple
	err := walkAttributes(b, func(kind uint16, value []byte) error {
		switch kind {
		case ctaTupleIP:
			return walkAttributes(value, func(kind uint16, value []byte) error {
				var a netip.Addr
				var ok bool
				switch kind {
				case ctaIPv4Src, ctaIPv4Dst, ctaIPv6Src, ctaIPv6Dst:
					a, ok = netip.AddrFromSlice(value)
					if !ok {
						return errors.New("a conntrack address of the wrong length")
					}
				default:
					return nil
				}
				if kind == ctaIPv4Src || kind == ctaIPv6Src {
					t.src = a
				} else {
					t.dst = a
				}
				return nil
			})
		case ctaTupleProto:
			return walkAttributes(value, func(kind uint16, value []byte) error {
				switch {
				case kind == ctaProtoNum && len(value) >= 1:
					t.proto = value[0]
				case kind == ctaProtoDstPort && len(value) >= 2:
					t.dstPort = binary.BigEndian.Uint16(value)
				}
				return nil
			})
		}
		return nil
	})
	return t, err
}

// walkAttributes visits each netlink attribute in b. Lengths are host-endian
// and every attribute is padded to four bytes.
func walkAttributes(b []byte, visit func(kind uint16, value []byte) error) error {
	for len(b) >= 4 {
		length := int(binary.NativeEndian.Uint16(b[0:2]))
		kind := binary.NativeEndian.Uint16(b[2:4]) & nlaTypeMask
		if length < 4 || length > len(b) {
			return errors.New("a truncated netlink attribute")
		}
		if err := visit(kind, b[4:length]); err != nil {
			return err
		}
		aligned := (length + 3) &^ 3
		if aligned > len(b) {
			return nil
		}
		b = b[aligned:]
	}
	return nil
}

// The netlink message framing a conntrack dump arrives in.
const (
	nlmsgHeaderLen = 16
	nfgenHeaderLen = 4
	nlmsgError     = 2
	nlmsgDone      = 3
)

// parseConntrackMessages reads one receive buffer of a dump: zero or more
// netlink messages. It reports done at the dump's end, and an error the
// kernel returned for the request.
func parseConntrackMessages(buf []byte, seq uint32, into []TrackedFlow, limit int) ([]TrackedFlow, bool, error) {
	for len(buf) >= nlmsgHeaderLen {
		length := int(binary.NativeEndian.Uint32(buf[0:4]))
		kind := binary.NativeEndian.Uint16(buf[4:6])
		got := binary.NativeEndian.Uint32(buf[8:12])
		if length < nlmsgHeaderLen || length > len(buf) {
			return into, false, errors.New("a truncated netlink message")
		}
		body := buf[nlmsgHeaderLen:length]
		buf = buf[(length+3)&^3:]
		if got != seq {
			continue
		}
		switch kind {
		case nlmsgDone:
			return into, true, nil
		case nlmsgError:
			if len(body) >= 4 {
				if code := int32(binary.NativeEndian.Uint32(body[0:4])); code != 0 {
					return into, true, conntrackErrno(-code)
				}
			}
			continue
		}
		if len(body) < nfgenHeaderLen {
			continue
		}
		f, err := parseConntrackEntry(body[nfgenHeaderLen:])
		if err != nil {
			continue
		}
		into = append(into, f)
		if len(into) >= limit {
			return into, true, errFlowLimit
		}
	}
	return into, false, nil
}

var errFlowLimit = errors.New("conntrack limit reached")

// conntrackErrno names the errors a dump request meets in practice.
func conntrackErrno(code int32) error {
	switch code {
	case 1, 13:
		return errors.New("reading connection tracking needs CAP_NET_ADMIN in the host's network namespace")
	case 2, 93, 97:
		return &UnavailableError{Tool: "nf_conntrack"}
	}
	return fmt.Errorf("the kernel refused the connection-tracking dump (errno %d)", code)
}

// conntrackDumpRequest is IPCTNL_MSG_CT_GET with NLM_F_DUMP for every family.
func conntrackDumpRequest(seq uint32) []byte {
	const (
		ctnlSubsys = 1
		ctGet      = 1
		flags      = 0x1 | 0x300 // NLM_F_REQUEST | NLM_F_DUMP
	)
	b := make([]byte, nlmsgHeaderLen+nfgenHeaderLen)
	binary.NativeEndian.PutUint32(b[0:4], uint32(len(b)))
	binary.NativeEndian.PutUint16(b[4:6], ctnlSubsys<<8|ctGet)
	binary.NativeEndian.PutUint16(b[6:8], flags)
	binary.NativeEndian.PutUint32(b[8:12], seq)
	// nfgenmsg: AF_UNSPEC dumps every family, version NFNETLINK_V0.
	return b
}

// trimFlowError keeps a reading's error to what the page can hold.
func trimFlowError(err error) string {
	return strings.TrimSpace(firstLines(err.Error(), 2))
}
