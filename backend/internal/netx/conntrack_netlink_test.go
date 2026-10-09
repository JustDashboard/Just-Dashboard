package netx

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net/netip"
	"testing"

	"golang.org/x/sys/unix"
)

// The ctnetlink client against messages encoded the way the kernel sends
// them. Nothing here opens a netlink socket; the namespace test does.

type gwCT struct {
	src, dst         string
	sport, dport     uint16
	replySrc         string
	replySport       uint16
	proto            uint8
	tcpState         uint8
	status, mark, id uint32
	zone             uint16
}

func gwBE16(v uint16) []byte { b := make([]byte, 2); binary.BigEndian.PutUint16(b, v); return b }
func gwBE32(v uint32) []byte { b := make([]byte, 4); binary.BigEndian.PutUint32(b, v); return b }

func gwTuple(src, dst string, sport, dport uint16, proto uint8) []byte {
	s, d := netip.MustParseAddr(src), netip.MustParseAddr(dst)
	var ip []byte
	if s.Is4() {
		a, b := s.As4(), d.As4()
		ip = append(nlAttr(ctaIPv4Src, a[:]), nlAttr(ctaIPv4Dst, b[:])...)
	} else {
		a, b := s.As16(), d.As16()
		ip = append(nlAttr(ctaIPv6Src, a[:]), nlAttr(ctaIPv6Dst, b[:])...)
	}
	l4 := append(append(nlAttr(ctaProtoNum, []byte{proto}), nlAttr(ctaProtoSrcPort, gwBE16(sport))...), nlAttr(ctaProtoDstPort, gwBE16(dport))...)
	return append(nlAttr(ctaTupleIP|nlaFNested, ip), nlAttr(ctaTupleProto|nlaFNested, l4)...)
}

// gwCTMessage is one IPCTNL_MSG_CT_NEW payload of a dump.
func gwCTMessage(c gwCT) []byte {
	family := byte(unix.AF_INET)
	if netip.MustParseAddr(c.src).Is6() {
		family = unix.AF_INET6
	}
	payload := []byte{family, 0, 0, 0}
	payload = append(payload, nlAttr(ctaTupleOrig|nlaFNested, gwTuple(c.src, c.dst, c.sport, c.dport, c.proto))...)
	reply := c.replySrc
	if reply == "" {
		reply = c.dst
	}
	payload = append(payload, nlAttr(ctaTupleReply|nlaFNested, gwTuple(reply, c.src, c.replySport, c.sport, c.proto))...)
	payload = append(payload, nlAttr(ctaStatus, gwBE32(c.status))...)
	payload = append(payload, nlAttr(ctaMark, gwBE32(c.mark))...)
	payload = append(payload, nlAttr(ctaID, gwBE32(c.id))...)
	if c.zone != 0 {
		payload = append(payload, nlAttr(ctaZone, gwBE16(c.zone))...)
	}
	if c.proto == unix.IPPROTO_TCP {
		info := nlAttr(ctaProtoinfoTCP|nlaFNested, nlAttr(ctaTCPState, []byte{c.tcpState}))
		payload = append(payload, nlAttr(ctaProtoinfo|nlaFNested, info)...)
	}
	return payload
}

// gwConntrack stands a fixed table behind the transport and records every
// request; delete requests are answered with ok (or ENOENT for gone ids).
type gwConntrack struct {
	entries  []gwCT
	requests [][]byte
	gone     map[uint32]bool
}

func (g *gwConntrack) install(t *testing.T) {
	t.Helper()
	prev := conntrackTransport
	t.Cleanup(func() { conntrackTransport = prev })
	conntrackTransport = func(ctx context.Context, req []byte, fn func(uint16, []byte) (bool, error)) error {
		g.requests = append(g.requests, append([]byte(nil), req...))
		msg := binary.NativeEndian.Uint16(req[4:6]) & 0xff
		switch msg {
		case ipctnlMsgCTGet:
			for _, e := range g.entries {
				more, err := fn(nfnlSubsysCTNetlink<<8, gwCTMessage(e))
				if err != nil || !more {
					return err
				}
			}
		case ipctnlMsgCTDelete:
			var id uint32
			nlAttrs(req[20:], func(typ uint16, v []byte) {
				if typ == ctaID {
					id = binary.BigEndian.Uint32(v)
				}
			})
			if g.gone[id] {
				return unix.ENOENT
			}
		case ipctnlMsgCTGetStatsCPU:
			_, err := fn(nfnlSubsysCTNetlink<<8|ipctnlMsgCTGetStatsCPU, gwStatsMessage(ctStats{Drop: 2}))
			return err
		}
		return nil
	}
}

func TestDecodeConntrackEntries(t *testing.T) {
	e, ok := decodeCTEntry(gwCTMessage(gwCT{src: "198.51.100.7", dst: "203.0.113.20", sport: 40000, dport: 8080, replySrc: "172.17.0.2", replySport: 80,
		proto: unix.IPPROTO_TCP, tcpState: 3, status: ctStatusSeenReply | ctStatusAssured | ctStatusDstNAT, mark: 0x4a000000, id: 77, zone: 3}))
	if !ok {
		t.Fatal("not decoded")
	}
	if e.Src.String() != "198.51.100.7" || e.Dst.String() != "203.0.113.20" || e.SPort != 40000 || e.DPort != 8080 {
		t.Fatalf("tuple = %+v", e)
	}
	if e.ReplySrc.String() != "172.17.0.2" || e.ReplySPort != 80 || e.state() != "ESTABLISHED" || e.Mark != 0x4a000000 || e.ID != 77 || !e.HasID {
		t.Fatalf("details = %+v", e)
	}
	if !bytes.Equal(e.zone, gwBE16(3)) || len(e.origTuple) == 0 {
		t.Fatalf("identity for deletion = %+v", e)
	}
	u, _ := decodeCTEntry(gwCTMessage(gwCT{src: "2001:db8::7", dst: "2001:db8::20", sport: 5353, dport: 53, proto: unix.IPPROTO_UDP}))
	if u.Src.String() != "2001:db8::7" || u.state() != "UNREPLIED" || protoName(u.Proto) != "udp" {
		t.Fatalf("udp = %+v", u)
	}
	if _, ok := decodeCTEntry([]byte{2, 0}); ok {
		t.Fatal("a short payload decoded")
	}
}

func TestConntrackDumpIsBoundedAndDeleteSendsTheExactIdentity(t *testing.T) {
	g := &gwConntrack{gone: map[uint32]bool{9: true}}
	for i := 0; i < 5; i++ {
		g.entries = append(g.entries, gwCT{src: "198.51.100.7", dst: "203.0.113.20", sport: uint16(40000 + i), dport: 443, proto: unix.IPPROTO_TCP, tcpState: 3, id: uint32(i + 1)})
	}
	g.install(t)
	var seen []ctEntry
	read, truncated, err := conntrackDump(context.Background(), 3, func(e ctEntry) bool { seen = append(seen, e); return true })
	if err != nil || read != 3 || !truncated || len(seen) != 3 {
		t.Fatalf("read %d truncated %v err %v", read, truncated, err)
	}
	if err := conntrackDelete(context.Background(), seen[1]); err != nil {
		t.Fatal(err)
	}
	req := g.requests[len(g.requests)-1]
	if binary.NativeEndian.Uint16(req[4:6]) != nfnlSubsysCTNetlink<<8|ipctnlMsgCTDelete || binary.NativeEndian.Uint16(req[6:8])&unix.NLM_F_ACK == 0 {
		t.Fatalf("delete header = %x", req[:20])
	}
	var tuple []byte
	var id uint32
	nlAttrs(req[20:], func(typ uint16, v []byte) {
		switch typ {
		case ctaTupleOrig:
			tuple = v
		case ctaID:
			id = binary.BigEndian.Uint32(v)
		}
	})
	if !bytes.Equal(tuple, seen[1].origTuple) || id != 2 {
		t.Fatalf("delete identity: tuple %v id %d", bytes.Equal(tuple, seen[1].origTuple), id)
	}
	// An entry already gone is not a failure.
	if err := conntrackDelete(context.Background(), ctEntry{Family: unix.AF_INET, origTuple: seen[0].origTuple, ID: 9, HasID: true}); err != nil {
		t.Fatalf("ENOENT: %v", err)
	}
	stats, err := conntrackStats(context.Background())
	if err != nil || stats.CPUs != 1 || stats.Drop != 2 {
		t.Fatalf("stats = %+v %v", stats, err)
	}
}

func TestWalkNetlinkStopsAtDoneAndReportsErrors(t *testing.T) {
	msg := func(typ uint16, seq uint32, payload []byte) []byte {
		b := make([]byte, unix.NLMSG_HDRLEN)
		binary.NativeEndian.PutUint32(b[0:4], uint32(unix.NLMSG_HDRLEN+len(payload)))
		binary.NativeEndian.PutUint16(b[4:6], typ)
		binary.NativeEndian.PutUint32(b[8:12], seq)
		b = append(b, payload...)
		for len(b)%4 != 0 {
			b = append(b, 0)
		}
		return b
	}
	calls := 0
	buf := append(append(msg(0x100, 7, []byte{2, 0, 0, 0}), msg(0x100, 8, []byte{2, 0, 0, 0})...), msg(unix.NLMSG_DONE, 7, []byte{0, 0, 0, 0})...)
	done, err := walkNetlink(buf, 7, func(uint16, []byte) (bool, error) { calls++; return true, nil })
	if !done || err != nil || calls != 1 {
		t.Fatalf("done %v err %v calls %d (another sequence's message must be skipped)", done, err, calls)
	}
	errPayload := make([]byte, 4)
	code := -int32(unix.EPERM)
	binary.NativeEndian.PutUint32(errPayload, uint32(code))
	if _, err := walkNetlink(msg(unix.NLMSG_ERROR, 7, errPayload), 7, nil); !errors.Is(err, unix.EPERM) {
		t.Fatalf("err = %v", err)
	}
	if _, err := walkNetlink([]byte{200, 0, 0, 0, 1, 1, 0, 0, 7, 0, 0, 0, 0, 0, 0, 0}, 7, nil); err == nil {
		t.Fatal("a length past the datagram was accepted")
	}
}
