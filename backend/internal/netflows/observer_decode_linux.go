package netflows

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"time"
)

type kernelPacket struct {
	at                        uint64
	cookie, cgroup, namespace uint64
	direction                 uint32
	socket                    Socket
	payload                   *uint64
	flags                     byte
	byteGap                   bool
}

func decodeObserver(raw []byte) (kernelPacket, error) {
	var p kernelPacket
	if len(raw) != observerRecordBytes {
		return p, fmt.Errorf("wrong fixed event size")
	}
	p.at = binary.LittleEndian.Uint64(raw[:8])
	p.cookie = binary.LittleEndian.Uint64(raw[8:16])
	p.cgroup = binary.LittleEndian.Uint64(raw[16:24])
	p.namespace = binary.LittleEndian.Uint64(raw[24:32])
	length := int(binary.LittleEndian.Uint32(raw[32:36]))
	gso := binary.LittleEndian.Uint32(raw[36:40])
	p.direction = binary.LittleEndian.Uint32(raw[40:44])
	family := binary.LittleEndian.Uint32(raw[44:48])
	offset := int(binary.LittleEndian.Uint32(raw[48:52]))
	proto := binary.LittleEndian.Uint32(raw[52:56])
	ip, transport := raw[56:96], raw[96:116]
	if p.cookie == 0 || p.namespace == 0 || p.cgroup == 0 || p.direction > 1 || (proto != 6 && proto != 17) {
		return p, fmt.Errorf("unproven fixed event identity")
	}
	var src, dst netip.Addr
	switch family {
	case 4:
		if ip[0]>>4 != 4 || offset != int(ip[0]&15)*4 || offset < 20 || offset > 60 || binary.BigEndian.Uint16(ip[6:8])&0x3fff != 0 || uint32(ip[9]) != proto {
			return p, fmt.Errorf("unsupported IPv4 header")
		}
		src = netip.AddrFrom4([4]byte(ip[12:16]))
		dst = netip.AddrFrom4([4]byte(ip[16:20]))
		if int(binary.BigEndian.Uint16(ip[2:4])) != length {
			p.byteGap = true
		}
	case 6:
		if ip[0]>>4 != 6 || offset != 40 || uint32(ip[6]) != proto {
			return p, fmt.Errorf("unsupported IPv6 header")
		}
		src = netip.AddrFrom16([16]byte(ip[8:24]))
		dst = netip.AddrFrom16([16]byte(ip[24:40]))
		if int(binary.BigEndian.Uint16(ip[4:6]))+40 != length {
			p.byteGap = true
		}
	default:
		return p, fmt.Errorf("unsupported fixed event family")
	}
	sp, dp := int(binary.BigEndian.Uint16(transport[:2])), int(binary.BigEndian.Uint16(transport[2:4]))
	p.socket = Socket{Cookie: fmt.Sprint(p.cookie), State: "packet_observed", Owner: Owner{Status: "unknown", Reason: "A socket cgroup and namespace cookie do not identify a process."}}
	if p.direction == 0 {
		src, dst = dst, src
		sp, dp = dp, sp
	}
	p.socket.LocalAddress, p.socket.RemoteAddress = src.Unmap().String(), dst.Unmap().String()
	p.socket.LocalPort, p.socket.RemotePort = sp, dp
	p.socket.LocalEndpoint = netip.AddrPortFrom(src, uint16(sp)).String()
	p.socket.RemoteEndpoint = netip.AddrPortFrom(dst, uint16(dp)).String()
	header := 8
	if proto == 6 {
		p.socket.Protocol = "tcp"
		header = int(transport[12]>>4) * 4
		p.flags = transport[13]
		if header < 20 || header > 60 {
			return p, fmt.Errorf("invalid TCP header")
		}
	} else {
		p.socket.Protocol = "udp"
		if int(binary.BigEndian.Uint16(transport[4:6])) != length-offset {
			p.byteGap = true
		}
	}
	if length < offset+header {
		return p, fmt.Errorf("short transport header")
	}
	if gso > 1 {
		p.byteGap = true
	}
	if !p.byteGap {
		n := uint64(length - offset - header)
		p.payload = &n
	}
	return p, nil
}
func packetBucket(boot string, p kernelPacket, at time.Time) Bucket {
	ns := fmt.Sprintf("cookie:%d", p.namespace)
	data, _ := json.Marshal([]any{boot, p.namespace, p.cgroup, p.cookie, p.socket.Protocol, p.socket.LocalAddress, p.socket.LocalPort, p.socket.RemoteAddress, p.socket.RemotePort})
	sum := sha256.Sum256(data)
	id := hex.EncodeToString(sum[:])
	b := Bucket{ID: "kernel-v1:" + id, Hour: at.UTC().Truncate(time.Hour), FirstSeen: at, LastSeen: at, SourceID: "kernel-cgroup", SourceName: "Kernel cgroup packet observations", Namespace: ns, BootID: boot, Socket: p.socket, Samples: 1, Evidence: "kernel_transport_payload_observed", SocketCgroup: fmt.Sprint(p.cgroup), ObservedPackets: 1}
	if p.direction == 1 {
		b.ObservedTxBytes = p.payload
		b.ObservedTxPackets = 1
		if p.payload != nil {
			b.ObservedTxKnownPackets = 1
		} else {
			b.ObservedTxByteGaps = 1
		}
	} else {
		b.ObservedRxBytes = p.payload
		b.ObservedRxPackets = 1
		if p.payload != nil {
			b.ObservedRxKnownPackets = 1
		} else {
			b.ObservedRxByteGaps = 1
		}
	}
	if p.flags&2 != 0 {
		b.ObservedSYN = 1
	}
	if p.flags&1 != 0 {
		b.ObservedFIN = 1
	}
	if p.flags&4 != 0 {
		b.ObservedRST = 1
	}
	return b
}
func mergeQuality(a, b ObserverQuality) ObserverQuality {
	a.Events += b.Events
	a.RingDrops += b.RingDrops
	a.BudgetOmissions += b.BudgetOmissions
	a.HeaderGaps += b.HeaderGaps
	a.IdentityGaps += b.IdentityGaps
	a.StateAdmissionGaps += b.StateAdmissionGaps
	a.ParserGaps += b.ParserGaps
	a.ByteGaps += b.ByteGaps
	a.PendingOmissions += b.PendingOmissions
	a.AttributionGaps += b.AttributionGaps
	a.ReaderBudgetPauses += b.ReaderBudgetPauses
	a.UnsavedEvents += b.UnsavedEvents
	a.TimestampGaps += b.TimestampGaps
	a.ShutdownTailUnknown = a.ShutdownTailUnknown || b.ShutdownTailUnknown
	return a
}
