package netx

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// A small ctnetlink client: dump the connection-tracking table, read its
// per-CPU statistics and delete one entry by its exact tuple.
//
// The kernel this host runs does not always expose /proc/net/nf_conntrack
// (it needs CONFIG_NF_CONNTRACK_PROCFS), and the conntrack tool is not part
// of every host or of the dashboard's image, so the table is read over the
// netlink socket the tool itself uses. The backend shares the host's network
// namespace, so this is the host's table. Nothing here writes except
// conntrackDelete, which only the explicit, destructive session revocation
// calls.

const (
	nfnlSubsysCTNetlink = 1

	ipctnlMsgCTGet                = 1
	ipctnlMsgCTDelete             = 2
	ipctnlMsgCTGetStatsCPU        = 4
	nlaFNested             uint16 = 1 << 15
	nlaTypeMask            uint16 = 0x3fff

	ctaTupleOrig    = 1
	ctaTupleReply   = 2
	ctaStatus       = 3
	ctaProtoinfo    = 4
	ctaMark         = 8
	ctaID           = 12
	ctaZone         = 18
	ctaTupleIP      = 1
	ctaTupleProto   = 2
	ctaIPv4Src      = 1
	ctaIPv4Dst      = 2
	ctaIPv6Src      = 3
	ctaIPv6Dst      = 4
	ctaProtoNum     = 1
	ctaProtoSrcPort = 2
	ctaProtoDstPort = 3
	ctaProtoinfoTCP = 1
	ctaTCPState     = 1

	ctaStatsFound         = 2
	ctaStatsInvalid       = 4
	ctaStatsInsert        = 8
	ctaStatsInsertFailed  = 9
	ctaStatsDrop          = 10
	ctaStatsEarlyDrop     = 11
	ctaStatsError         = 12
	ctaStatsSearchRestart = 13
	ctaStatsClashResolve  = 14
	ctaStatsChainTooLong  = 15

	// Status bits of a tracked connection (enum ip_conntrack_status).
	ctStatusSeenReply = 1 << 1
	ctStatusAssured   = 1 << 2
	ctStatusSrcNAT    = 1 << 4
	ctStatusDstNAT    = 1 << 5
)

// tcpConntrackStates are the kernel's names for enum tcp_conntrack.
var tcpConntrackStates = []string{"NONE", "SYN_SENT", "SYN_RECV", "ESTABLISHED", "FIN_WAIT", "CLOSE_WAIT", "LAST_ACK", "TIME_WAIT", "CLOSE", "SYN_SENT2"}

// ctEntry is one tracked connection: its original tuple, the reply's source
// (a destination-translated flow's real target), state and identity.
type ctEntry struct {
	Family     uint8
	Proto      uint8
	Src, Dst   netip.Addr
	SPort      uint16
	DPort      uint16
	ReplySrc   netip.Addr
	ReplySPort uint16
	ReplyDst   netip.Addr // a source-translated flow's translated address
	TCPState   uint8
	HasTCP     bool
	Status     uint32
	Mark       uint32
	ID         uint32
	HasID      bool
	// origTuple and zone are the raw attributes, sent back unchanged to
	// delete exactly this entry.
	origTuple []byte
	zone      []byte
}

func (e ctEntry) state() string {
	if e.HasTCP && int(e.TCPState) < len(tcpConntrackStates) {
		return tcpConntrackStates[e.TCPState]
	}
	if e.Status&ctStatusSeenReply == 0 {
		return "UNREPLIED"
	}
	if e.Status&ctStatusAssured != 0 {
		return "ASSURED"
	}
	return "REPLIED"
}

func protoName(p uint8) string {
	switch p {
	case unix.IPPROTO_TCP:
		return "tcp"
	case unix.IPPROTO_UDP:
		return "udp"
	case unix.IPPROTO_ICMP:
		return "icmp"
	case unix.IPPROTO_ICMPV6:
		return "icmpv6"
	case unix.IPPROTO_SCTP:
		return "sctp"
	case unix.IPPROTO_GRE:
		return "gre"
	}
	return fmt.Sprintf("proto %d", p)
}

// ctStats are the per-CPU counters summed.
type ctStats struct {
	CPUs          int    `json:"cpus"`
	Found         uint64 `json:"found"`
	Invalid       uint64 `json:"invalid"`
	Insert        uint64 `json:"insert"`
	InsertFailed  uint64 `json:"insertFailed"`
	Drop          uint64 `json:"drop"`
	EarlyDrop     uint64 `json:"earlyDrop"`
	Error         uint64 `json:"error"`
	SearchRestart uint64 `json:"searchRestart"`
	ClashResolve  uint64 `json:"clashResolve"`
	ChainTooLong  uint64 `json:"chainTooLong"`
}

// conntrackTransport is the netlink round trip, a variable so tests answer
// with recorded messages on a machine whose table they must not read.
var conntrackTransport = netlinkConntrack

var ctSequence atomic.Uint32

// netlinkConntrack sends one request and hands each answering message to
// fn until the kernel says done, fn returns false, or the deadline passes.
func netlinkConntrack(ctx context.Context, request []byte, fn func(msgType uint16, payload []byte) (bool, error)) error {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_NETFILTER)
	if err != nil {
		return &UnavailableError{Tool: "conntrack netlink"}
	}
	defer unix.Close(fd)
	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return fmt.Errorf("binding the conntrack socket: %w", err)
	}
	tv := unix.NsecToTimeval((2 * time.Second).Nanoseconds())
	_ = unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv)
	_ = unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_RCVBUF, 1<<20)
	if err := unix.Sendto(fd, request, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return fmt.Errorf("asking the kernel for its connection table: %w", err)
	}
	seq := binary.NativeEndian.Uint32(request[8:12])
	buf := make([]byte, 1<<17)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, _, err := unix.Recvfrom(fd, buf, 0)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			if errors.Is(err, unix.EAGAIN) {
				return errors.New("the kernel did not answer the connection-table request in time")
			}
			return fmt.Errorf("reading the connection table: %w", err)
		}
		done, err := walkNetlink(buf[:n], seq, fn)
		if err != nil || done {
			return err
		}
	}
}

// walkNetlink reads the messages in one datagram.
func walkNetlink(b []byte, seq uint32, fn func(msgType uint16, payload []byte) (bool, error)) (bool, error) {
	for len(b) >= unix.NLMSG_HDRLEN {
		length := int(binary.NativeEndian.Uint32(b[0:4]))
		if length < unix.NLMSG_HDRLEN || length > len(b) {
			return true, errors.New("the kernel sent a malformed netlink message")
		}
		msgType := binary.NativeEndian.Uint16(b[4:6])
		msgSeq := binary.NativeEndian.Uint32(b[8:12])
		payload := b[unix.NLMSG_HDRLEN:length]
		b = b[nlAlign(length):]
		if msgSeq != seq {
			continue
		}
		switch msgType {
		case unix.NLMSG_DONE:
			return true, nil
		case unix.NLMSG_ERROR:
			if len(payload) < 4 {
				return true, errors.New("the kernel sent a malformed netlink error")
			}
			if code := int32(binary.NativeEndian.Uint32(payload[0:4])); code != 0 {
				return true, syscall.Errno(-code)
			}
			return true, nil
		}
		more, err := fn(msgType, payload)
		if err != nil || !more {
			return true, err
		}
	}
	return false, nil
}

func nlAlign(n int) int { return (n + 3) &^ 3 }

// ctRequest is a ctnetlink request: the netlink header, the netfilter
// header naming a family, and attributes.
func ctRequest(msg uint16, flags uint16, family uint8, attrs []byte) []byte {
	length := unix.NLMSG_HDRLEN + 4 + len(attrs)
	b := make([]byte, length)
	binary.NativeEndian.PutUint32(b[0:4], uint32(length))
	binary.NativeEndian.PutUint16(b[4:6], nfnlSubsysCTNetlink<<8|msg)
	binary.NativeEndian.PutUint16(b[6:8], flags)
	binary.NativeEndian.PutUint32(b[8:12], ctSequence.Add(1))
	b[16] = family
	copy(b[20:], attrs)
	return b
}

// nlAttr encodes one attribute, padded.
func nlAttr(typ uint16, value []byte) []byte {
	b := make([]byte, nlAlign(4+len(value)))
	binary.NativeEndian.PutUint16(b[0:2], uint16(4+len(value)))
	binary.NativeEndian.PutUint16(b[2:4], typ)
	copy(b[4:], value)
	return b
}

// nlAttrs walks a run of attributes.
func nlAttrs(b []byte, fn func(typ uint16, value []byte)) {
	for len(b) >= 4 {
		length := int(binary.NativeEndian.Uint16(b[0:2]))
		if length < 4 || length > len(b) {
			return
		}
		fn(binary.NativeEndian.Uint16(b[2:4])&nlaTypeMask, b[4:length])
		b = b[min(nlAlign(length), len(b)):]
	}
}

// decodeCTEntry reads one IPCTNL_MSG_CT_NEW message of a dump.
func decodeCTEntry(payload []byte) (ctEntry, bool) {
	var e ctEntry
	if len(payload) < 4 {
		return e, false
	}
	e.Family = payload[0]
	nlAttrs(payload[4:], func(typ uint16, v []byte) {
		switch typ {
		case ctaTupleOrig:
			e.origTuple = append([]byte(nil), v...)
			decodeTuple(v, &e.Src, &e.Dst, &e.SPort, &e.DPort, &e.Proto)
		case ctaTupleReply:
			var dport uint16
			var proto uint8
			decodeTuple(v, &e.ReplySrc, &e.ReplyDst, &e.ReplySPort, &dport, &proto)
		case ctaStatus:
			if len(v) >= 4 {
				e.Status = binary.BigEndian.Uint32(v)
			}
		case ctaMark:
			if len(v) >= 4 {
				e.Mark = binary.BigEndian.Uint32(v)
			}
		case ctaID:
			if len(v) >= 4 {
				e.ID, e.HasID = binary.BigEndian.Uint32(v), true
			}
		case ctaZone:
			e.zone = append([]byte(nil), v...)
		case ctaProtoinfo:
			nlAttrs(v, func(t uint16, info []byte) {
				if t != ctaProtoinfoTCP {
					return
				}
				nlAttrs(info, func(k uint16, val []byte) {
					if k == ctaTCPState && len(val) >= 1 {
						e.TCPState, e.HasTCP = val[0], true
					}
				})
			})
		}
	})
	return e, e.Src.IsValid() && e.origTuple != nil
}

func decodeTuple(b []byte, src, dst *netip.Addr, sport, dport *uint16, proto *uint8) {
	nlAttrs(b, func(typ uint16, v []byte) {
		switch typ {
		case ctaTupleIP:
			nlAttrs(v, func(k uint16, a []byte) {
				switch {
				case (k == ctaIPv4Src || k == ctaIPv4Dst) && len(a) == 4:
					addr := netip.AddrFrom4([4]byte(a))
					if k == ctaIPv4Src {
						*src = addr
					} else {
						*dst = addr
					}
				case (k == ctaIPv6Src || k == ctaIPv6Dst) && len(a) == 16:
					addr := netip.AddrFrom16([16]byte(a))
					if k == ctaIPv6Src {
						*src = addr
					} else {
						*dst = addr
					}
				}
			})
		case ctaTupleProto:
			nlAttrs(v, func(k uint16, p []byte) {
				switch {
				case k == ctaProtoNum && len(p) >= 1:
					*proto = p[0]
				case k == ctaProtoSrcPort && len(p) >= 2:
					*sport = binary.BigEndian.Uint16(p)
				case k == ctaProtoDstPort && len(p) >= 2:
					*dport = binary.BigEndian.Uint16(p)
				}
			})
		}
	})
}

// conntrackDump reads every entry of the table, in both families, handing
// each to fn until fn returns false. limit bounds how many are read.
func conntrackDump(ctx context.Context, limit int, fn func(ctEntry) bool) (read int, truncated bool, err error) {
	request := ctRequest(ipctnlMsgCTGet, unix.NLM_F_REQUEST|unix.NLM_F_DUMP, unix.AF_UNSPEC, nil)
	err = conntrackTransport(ctx, request, func(msgType uint16, payload []byte) (bool, error) {
		if msgType&0xff != 0 { // IPCTNL_MSG_CT_NEW is what a dump answers with
			return true, nil
		}
		e, ok := decodeCTEntry(payload)
		if !ok {
			return true, nil
		}
		if read >= limit {
			truncated = true
			return false, nil
		}
		read++
		return fn(e), nil
	})
	return read, truncated, err
}

// conntrackStats sums the per-CPU statistics.
func conntrackStats(ctx context.Context) (ctStats, error) {
	var s ctStats
	request := ctRequest(ipctnlMsgCTGetStatsCPU, unix.NLM_F_REQUEST|unix.NLM_F_DUMP, unix.AF_UNSPEC, nil)
	err := conntrackTransport(ctx, request, func(msgType uint16, payload []byte) (bool, error) {
		if len(payload) < 4 {
			return true, nil
		}
		s.CPUs++
		nlAttrs(payload[4:], func(typ uint16, v []byte) {
			if len(v) < 4 {
				return
			}
			n := uint64(binary.BigEndian.Uint32(v))
			switch typ {
			case ctaStatsFound:
				s.Found += n
			case ctaStatsInvalid:
				s.Invalid += n
			case ctaStatsInsert:
				s.Insert += n
			case ctaStatsInsertFailed:
				s.InsertFailed += n
			case ctaStatsDrop:
				s.Drop += n
			case ctaStatsEarlyDrop:
				s.EarlyDrop += n
			case ctaStatsError:
				s.Error += n
			case ctaStatsSearchRestart:
				s.SearchRestart += n
			case ctaStatsClashResolve:
				s.ClashResolve += n
			case ctaStatsChainTooLong:
				s.ChainTooLong += n
			}
		})
		return true, nil
	})
	return s, err
}

// conntrackDelete removes exactly one entry: its original tuple, its zone,
// and its id, so an entry recreated for a new connection with the same tuple
// in the meantime is left alone. An entry already gone is not an error.
func conntrackDelete(ctx context.Context, e ctEntry) error {
	attrs := nlAttr(ctaTupleOrig|nlaFNested, e.origTuple)
	if e.zone != nil {
		attrs = append(attrs, nlAttr(ctaZone, e.zone)...)
	}
	if e.HasID {
		id := make([]byte, 4)
		binary.BigEndian.PutUint32(id, e.ID)
		attrs = append(attrs, nlAttr(ctaID, id)...)
	}
	request := ctRequest(ipctnlMsgCTDelete, unix.NLM_F_REQUEST|unix.NLM_F_ACK, e.Family, attrs)
	err := conntrackTransport(ctx, request, func(uint16, []byte) (bool, error) { return true, nil })
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	return err
}
