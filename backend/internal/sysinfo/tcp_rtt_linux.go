package sysinfo

import (
	"encoding/binary"
	"net/netip"
	"sort"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// ReadTCPLatency asks the kernel for every established TCP connection's
// tcp_info over sock_diag and summarises the smoothed RTT of those to peers
// off this host's networks. It is a reading of connections the host already
// has, so it costs no packets and measures nothing when nobody is connected —
// which it says, rather than reporting zero milliseconds.
//
// Loopback, link-local and private (RFC 1918, ULA) peers are left out: a
// container talking to the host over a bridge answers in microseconds and
// would pull the median toward a network nobody is waiting on. The tailnet's
// shared address range is kept, since a tailnet peer is somewhere else.
func ReadTCPLatency() TCPLatency {
	var rtts []float64
	supported := false
	for _, family := range []uint8{unix.AF_INET, unix.AF_INET6} {
		got, ok := tcpInfoRTTs(family, maxLatencySockets-len(rtts), externalPeer)
		supported = supported || ok
		rtts = append(rtts, got...)
	}
	return summariseRTTs(rtts, supported)
}

// maxLatencySockets bounds one reading: a busy proxy holds tens of thousands
// of connections and the median of the first ten thousand is the same answer.
const maxLatencySockets = 10_000

const (
	sockDiagByFamily = 20
	inetDiagInfo     = 2
	tcpEstablished   = 1
	inetDiagMsgLen   = 72
	// tcpi_rtt's offset inside struct tcp_info, in microseconds.
	tcpInfoRTTOffset = 68
)

// tcpInfoRTTs dumps one family's established sockets with INET_DIAG_INFO and
// returns the RTT of those whose peer keep accepts.
func tcpInfoRTTs(family uint8, limit int, keep func(netip.Addr) bool) ([]float64, bool) {
	if limit <= 0 {
		return nil, true
	}
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, unix.NETLINK_INET_DIAG)
	if err != nil {
		return nil, false
	}
	defer unix.Close(fd)
	tv := unix.NsecToTimeval(int64(2 * time.Second))
	_ = unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv)
	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return nil, false
	}

	req := make([]byte, 16+56)
	binary.NativeEndian.PutUint32(req[0:], uint32(len(req)))
	binary.NativeEndian.PutUint16(req[4:], sockDiagByFamily)
	binary.NativeEndian.PutUint16(req[6:], unix.NLM_F_REQUEST|unix.NLM_F_DUMP)
	binary.NativeEndian.PutUint32(req[8:], 1)
	req[16] = family
	req[17] = unix.IPPROTO_TCP
	req[18] = 1 << (inetDiagInfo - 1)
	binary.NativeEndian.PutUint32(req[20:], 1<<tcpEstablished)
	if err := unix.Sendto(fd, req, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return nil, false
	}

	var rtts []float64
	buf := make([]byte, 64<<10)
	for {
		n, _, err := unix.Recvfrom(fd, buf, 0)
		if err != nil {
			return rtts, len(rtts) > 0
		}
		msgs, err := syscall.ParseNetlinkMessage(buf[:n])
		if err != nil {
			return rtts, len(rtts) > 0
		}
		for _, m := range msgs {
			switch m.Header.Type {
			case unix.NLMSG_DONE:
				return rtts, true
			case unix.NLMSG_ERROR:
				return rtts, false
			}
			if rtt, ok := peerRTT(family, m.Data, keep); ok {
				rtts = append(rtts, rtt)
				if len(rtts) >= limit {
					return rtts, true
				}
			}
		}
	}
}

// peerRTT reads one inet_diag_msg: the peer address and, from the
// INET_DIAG_INFO attribute, tcpi_rtt in milliseconds.
func peerRTT(family uint8, data []byte, keep func(netip.Addr) bool) (float64, bool) {
	if len(data) < inetDiagMsgLen {
		return 0, false
	}
	var peer netip.Addr
	if family == unix.AF_INET {
		peer = netip.AddrFrom4([4]byte(data[24:28]))
	} else {
		peer = netip.AddrFrom16([16]byte(data[24:40])).Unmap()
	}
	if !keep(peer) {
		return 0, false
	}
	attrs := data[inetDiagMsgLen:]
	for len(attrs) >= 4 {
		length := int(binary.NativeEndian.Uint16(attrs[0:]))
		kind := binary.NativeEndian.Uint16(attrs[2:])
		if length < 4 || length > len(attrs) {
			return 0, false
		}
		if kind == inetDiagInfo && length >= 4+tcpInfoRTTOffset+4 {
			micros := binary.NativeEndian.Uint32(attrs[4+tcpInfoRTTOffset:])
			return float64(micros) / 1000, true
		}
		attrs = attrs[(length+3)&^3:]
	}
	return 0, false
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// externalPeer is a peer somewhere else: not this host, not a link, not a
// private network — but a tailnet peer is, since it is reached over the
// internet.
func externalPeer(a netip.Addr) bool {
	if !a.IsValid() || a.IsLoopback() || a.IsUnspecified() || a.IsLinkLocalUnicast() || a.IsMulticast() {
		return false
	}
	if cgnat.Contains(a) {
		return true
	}
	return !a.IsPrivate()
}

func summariseRTTs(rtts []float64, supported bool) TCPLatency {
	l := TCPLatency{Supported: supported, Sockets: len(rtts)}
	if len(rtts) == 0 {
		return l
	}
	sort.Float64s(rtts)
	l.MedianMs = round1(percentile(rtts, 0.5))
	l.P90Ms = round1(percentile(rtts, 0.9))
	return l
}

// percentile reads a sorted slice by nearest rank.
func percentile(sorted []float64, q float64) float64 {
	i := int(q*float64(len(sorted))+0.5) - 1
	if i < 0 {
		i = 0
	}
	if i >= len(sorted) {
		i = len(sorted) - 1
	}
	return sorted[i]
}
