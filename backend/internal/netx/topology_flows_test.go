package netx

import (
	"context"
	"encoding/binary"
	"errors"
	"net/netip"
	"strings"
	"testing"
)

// nlattr builds one netlink attribute, nested when the value is attributes.
func nlattr(kind uint16, value []byte) []byte {
	b := make([]byte, 4, 4+len(value)+3)
	binary.NativeEndian.PutUint16(b[0:2], uint16(4+len(value)))
	binary.NativeEndian.PutUint16(b[2:4], kind)
	b = append(b, value...)
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

func nested(kind uint16, parts ...[]byte) []byte {
	var body []byte
	for _, p := range parts {
		body = append(body, p...)
	}
	return nlattr(kind|0x8000, body)
}

func be16(v uint16) []byte { b := make([]byte, 2); binary.BigEndian.PutUint16(b, v); return b }
func be32(v uint32) []byte { b := make([]byte, 4); binary.BigEndian.PutUint32(b, v); return b }
func be64(v uint64) []byte { b := make([]byte, 8); binary.BigEndian.PutUint64(b, v); return b }

func ctTupleAttr(kind uint16, src, dst string, proto uint8, dport uint16) []byte {
	s, d := netip.MustParseAddr(src), netip.MustParseAddr(dst)
	srcKind, dstKind := uint16(ctaIPv4Src), uint16(ctaIPv4Dst)
	if s.Is6() {
		srcKind, dstKind = ctaIPv6Src, ctaIPv6Dst
	}
	return nested(kind,
		nested(ctaTupleIP, nlattr(srcKind, s.AsSlice()), nlattr(dstKind, d.AsSlice())),
		nested(ctaTupleProto, nlattr(ctaProtoNum, []byte{proto}), nlattr(2, be16(40000)), nlattr(ctaProtoDstPort, be16(dport))),
	)
}

// ctMessage is one dump reply carrying an entry.
func ctMessage(seq uint32, attrs ...[]byte) []byte {
	body := []byte{0, 0, 0, 0}
	for _, a := range attrs {
		body = append(body, a...)
	}
	return nlmsg(seq, 1<<8, body)
}

func nlmsg(seq uint32, kind uint16, body []byte) []byte {
	b := make([]byte, nlmsgHeaderLen, nlmsgHeaderLen+len(body))
	binary.NativeEndian.PutUint32(b[0:4], uint32(nlmsgHeaderLen+len(body)))
	binary.NativeEndian.PutUint16(b[4:6], kind)
	binary.NativeEndian.PutUint32(b[8:12], seq)
	b = append(b, body...)
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

func TestConntrackMessagesParseTuplesTranslationAndCounters(t *testing.T) {
	masquerade := ctMessage(7,
		ctTupleAttr(ctaTupleOrig, "172.18.0.5", "93.184.216.34", 6, 443),
		ctTupleAttr(ctaTupleReply, "93.184.216.34", "203.0.113.10", 6, 40000),
		nlattr(ctaStatus, be32(ctStatusSrcNAT|0x8)),
		nested(ctaCountersOrig, nlattr(1, be64(3)), nlattr(ctaCountersBytes, be64(900))),
		nested(ctaCountersReply, nlattr(1, be64(2)), nlattr(ctaCountersBytes, be64(4100))),
	)
	v6 := ctMessage(7,
		ctTupleAttr(ctaTupleOrig, "2001:db8::9", "2001:db8:10::5", 17, 53),
		ctTupleAttr(ctaTupleReply, "2001:db8:10::5", "2001:db8::9", 17, 40000),
	)
	stranger := ctMessage(99, ctTupleAttr(ctaTupleOrig, "10.0.0.1", "10.0.0.2", 6, 1))
	done := nlmsg(7, nlmsgDone, []byte{0, 0, 0, 0})
	buf := append(append(append(append([]byte{}, masquerade...), stranger...), v6...), done...)
	flows, finished, err := parseConntrackMessages(buf, 7, nil, flowLimit)
	if err != nil || !finished || len(flows) != 2 {
		t.Fatalf("flows=%+v finished=%v err=%v", flows, finished, err)
	}
	f := flows[0]
	if f.Protocol != 6 || f.OrigSrc.String() != "172.18.0.5" || f.ReplyDst.String() != "203.0.113.10" || f.OrigDstPort != 443 || f.Status&ctStatusSrcNAT == 0 || !f.Counted || f.Bytes != 5000 {
		t.Fatalf("masqueraded entry: %+v", f)
	}
	if flows[1].Protocol != 17 || !flows[1].OrigSrc.Is6() || flows[1].Counted {
		t.Fatalf("an IPv6 entry without accounting: %+v", flows[1])
	}
}

func TestConntrackMessagesStopAtTheLimitAndReportKernelErrors(t *testing.T) {
	one := ctMessage(3, ctTupleAttr(ctaTupleOrig, "10.0.0.1", "10.0.0.2", 6, 80), ctTupleAttr(ctaTupleReply, "10.0.0.2", "10.0.0.1", 6, 1))
	flows, done, err := parseConntrackMessages(append(append([]byte{}, one...), one...), 3, nil, 1)
	if !errors.Is(err, errFlowLimit) || !done || len(flows) != 1 {
		t.Fatalf("limit: %v %v %v", flows, done, err)
	}
	refused := nlmsg(3, nlmsgError, append(binary.NativeEndian.AppendUint32(nil, uint32(0xffffffff)), make([]byte, 16)...))
	_, _, err = parseConntrackMessages(refused, 3, nil, 10)
	if err == nil || !strings.Contains(err.Error(), "CAP_NET_ADMIN") {
		t.Fatalf("EPERM must name the missing capability: %v", err)
	}
	if _, _, err := parseConntrackMessages([]byte{40, 0, 0, 0, 1, 1, 0, 0, 3, 0, 0, 0, 0, 0, 0, 0}, 3, nil, 10); err == nil {
		t.Fatal("a truncated message must not parse")
	}
}

func TestClassifyFlowsDrawsEffectivePathsBetweenTopologyNodes(t *testing.T) {
	links := []Link{
		{Name: "ens3", Uplink: true, Role: "uplink", Addresses: []Address{{CIDR: "203.0.113.10/24", Scope: "global"}}},
		{Name: "tailscale0", Owner: "tailscale", Role: "tunnel", Kind: "tun", Addresses: []Address{{CIDR: "100.110.34.31/32", Scope: "global"}}},
		{Name: "wg0", Owner: "wireguard", Role: "tunnel", Kind: "wireguard", Addresses: []Address{{CIDR: "10.8.0.1/24", Scope: "global"}}},
		{Name: "br-lab", Managed: true, Role: "bridge", Kind: "bridge", Addresses: []Address{{CIDR: "10.60.0.1/24", Scope: "global"}}},
		{Name: "lo", Kind: "loopback", Role: "loopback", Addresses: []Address{{CIDR: "127.0.0.1/8", Scope: "host"}}},
	}
	docker := []DockerNet{{ID: "net1", Name: "web", Bridge: "br-3f2a", Subnets: []string{"172.18.0.0/16"}}}
	addr := netip.MustParseAddr
	flows := []TrackedFlow{
		// A container reaching the internet, masqueraded through the uplink.
		{Protocol: 6, OrigSrc: addr("172.18.0.5"), OrigDst: addr("93.184.216.34"), ReplySrc: addr("93.184.216.34"), ReplyDst: addr("203.0.113.10"), Status: ctStatusSrcNAT},
		{Protocol: 17, OrigSrc: addr("172.18.0.6"), OrigDst: addr("1.1.1.1"), ReplySrc: addr("1.1.1.1"), ReplyDst: addr("203.0.113.10"), Status: ctStatusSrcNAT},
		// A visitor through a port forward to the container.
		{Protocol: 6, OrigSrc: addr("198.51.100.7"), OrigDst: addr("203.0.113.10"), ReplySrc: addr("172.18.0.5"), ReplyDst: addr("198.51.100.7"), Status: ctStatusDstNAT},
		// A tailnet peer reaching the dashboard on the host.
		{Protocol: 6, OrigSrc: addr("100.110.34.9"), OrigDst: addr("100.110.34.31"), ReplySrc: addr("100.110.34.31"), ReplyDst: addr("100.110.34.9")},
		// A WireGuard peer reaching the managed bridge's network unchanged.
		{Protocol: 1, OrigSrc: addr("10.8.0.2"), OrigDst: addr("10.60.0.20"), ReplySrc: addr("10.60.0.20"), ReplyDst: addr("10.8.0.2")},
		// Loopback and host-to-itself are not edges.
		{Protocol: 6, OrigSrc: addr("127.0.0.1"), OrigDst: addr("127.0.0.1"), ReplySrc: addr("127.0.0.1"), ReplyDst: addr("127.0.0.1")},
		{Protocol: 6, OrigSrc: addr("203.0.113.10"), OrigDst: addr("10.60.0.1"), ReplySrc: addr("10.60.0.1"), ReplyDst: addr("203.0.113.10")},
	}
	edges, classified, accounting := classifyFlows(flows, links, docker)
	if classified != 5 || accounting {
		t.Fatalf("classified=%d accounting=%v edges=%+v", classified, accounting, edges)
	}
	byKey := map[string]FlowEdge{}
	for _, e := range edges {
		byKey[e.From+">"+e.To+">"+e.Translation] = e
	}
	out := byKey["docker:net1>internet>masquerade"]
	if out.Flows != 2 || out.Via != "ens3" || strings.Join(out.Protocols, ",") != "tcp,udp" {
		t.Fatalf("container egress: %+v", out)
	}
	if got := strings.Join(out.Path, " → "); got != "web → br-3f2a → this server → masquerade on ens3 → the internet" {
		t.Fatalf("effective path: %q", got)
	}
	in := byKey["internet>docker:net1>forward"]
	if in.Flows != 1 || in.Via != "ens3" || strings.Join(in.Path, " → ") != "the internet → this server → port forward on ens3 → br-3f2a → web" {
		t.Fatalf("port forward: %+v", in)
	}
	if e := byKey["link:tailscale0>host>none"]; e.Flows != 1 || strings.Join(e.Path, " → ") != "the tailnet → tailscale0 → this server" {
		t.Fatalf("tailnet to host: %+v", e)
	}
	if e := byKey["link:wg0>link:br-lab>none"]; e.Flows != 1 || e.Protocols[0] != "icmp" {
		t.Fatalf("tunnel to bridge: %+v", e)
	}
	if edges[0].Flows != 2 {
		t.Fatalf("busiest edge first: %+v", edges)
	}
}

func TestTopologyFlowsReportUnavailableAndFailedReads(t *testing.T) {
	prev := readTrackedFlows
	t.Cleanup(func() { readTrackedFlows = prev })
	s := testService(t)
	readTrackedFlows = func(context.Context, int) ([]TrackedFlow, bool, error) {
		return nil, false, &UnavailableError{Tool: "nf_conntrack"}
	}
	if got := s.TopologyFlows(context.Background(), nil, nil); got.State != "unavailable" || got.Edges == nil {
		t.Fatalf("%+v", got)
	}
	readTrackedFlows = func(context.Context, int) ([]TrackedFlow, bool, error) {
		return nil, false, errors.New("reading connection tracking needs CAP_NET_ADMIN in the host's network namespace")
	}
	if got := s.TopologyFlows(context.Background(), nil, nil); got.State != "failed" || !strings.Contains(got.Error, "CAP_NET_ADMIN") {
		t.Fatalf("%+v", got)
	}
	readTrackedFlows = func(context.Context, int) ([]TrackedFlow, bool, error) {
		return []TrackedFlow{{Protocol: 6, OrigSrc: netip.MustParseAddr("198.51.100.7"), OrigDst: netip.MustParseAddr("203.0.113.10"), ReplySrc: netip.MustParseAddr("203.0.113.10"), ReplyDst: netip.MustParseAddr("198.51.100.7"), Counted: true, Bytes: 10}}, true, nil
	}
	got := s.TopologyFlows(context.Background(), []Link{{Name: "ens3", Uplink: true, Addresses: []Address{{CIDR: "203.0.113.10/24"}}}}, nil)
	if got.State != "ok" || !got.Truncated || !got.Accounting || got.Total != 1 || len(got.Edges) != 1 || got.Edges[0].Bytes != 10 {
		t.Fatalf("%+v", got)
	}
}
