package proxysvc

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"time"
)

// Whether QUIC answers on a UDP port, asked the way QUIC itself provides for.
//
// A QUIC packet in a version the server does not speak must be answered with
// a Version Negotiation packet listing the versions it does (RFC 9000, 6),
// before any key exists. So the probe is one datagram with a version reserved
// for exactly this (0x?a?a?a?a, RFC 9000 15), padded to the 1200 bytes a
// server requires before it answers at all, and the answer is the list of
// versions. No QUIC connection is made, which this dashboard could not do
// anyway: there is no QUIC client in Go's standard library.

// QUICProbe is what a UDP port said to a QUIC packet.
type QUICProbe struct {
	Port     int      `json:"port"`
	Answered bool     `json:"answered"`
	Versions []string `json:"versions,omitempty"`
	Detail   string   `json:"detail,omitempty"`
}

// quicProbeVersion is the reserved version the probe asks for.
const quicProbeVersion = 0x1a2a3a4a

// quicWait is how long each of the two datagrams waits for an answer: UDP
// drops packets, so one unanswered datagram proves nothing.
const quicWait = 1500 * time.Millisecond

// probeQUIC sends the Version Negotiation probe to addr (ip:port).
func probeQUIC(ctx context.Context, addr string) *QUICProbe {
	_, portText, _ := net.SplitHostPort(addr)
	out := &QUICProbe{}
	fmt.Sscan(portText, &out.Port)
	conn, err := (&net.Dialer{}).DialContext(ctx, "udp", addr)
	if err != nil {
		out.Detail = "The probe could not send: " + err.Error() + "."
		return out
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.SetDeadline(time.Now()) })
	defer stop()

	dcid, scid := make([]byte, 8), make([]byte, 8)
	rand.Read(dcid)
	rand.Read(scid)
	packet := quicProbePacket(dcid, scid)
	answer := make([]byte, 1500)
	for range 2 {
		if _, err := conn.Write(packet); err != nil {
			out.Detail = "The probe could not send: " + err.Error() + "."
			return out
		}
		conn.SetReadDeadline(time.Now().Add(quicWait))
		for {
			n, err := conn.Read(answer)
			if err != nil {
				if ctx.Err() != nil {
					out.Detail = "The scan ended before an answer."
					return out
				}
				if isTimeout(err) {
					break
				}
				// An ICMP port unreachable surfaces as a refused read.
				out.Detail = "The port refused it: nothing listens for UDP there."
				return out
			}
			if versions, ok := parseVersionNegotiation(answer[:n], dcid, scid); ok {
				out.Answered, out.Versions = true, versions
				return out
			}
		}
	}
	out.Detail = "Two packets went unanswered for 3 seconds: UDP is filtered on the way, or nothing speaks QUIC there."
	return out
}

// quicProbePacket is a long-header packet in the reserved version: flags,
// version, the two connection IDs, then padding to 1200 bytes.
func quicProbePacket(dcid, scid []byte) []byte {
	p := []byte{0xc0}
	p = binary.BigEndian.AppendUint32(p, quicProbeVersion)
	p = append(p, byte(len(dcid)))
	p = append(p, dcid...)
	p = append(p, byte(len(scid)))
	p = append(p, scid...)
	return append(p, make([]byte, 1200-len(p))...)
}

// parseVersionNegotiation reads a Version Negotiation packet answering the
// probe: version zero, the probe's connection IDs swapped back, then the
// versions. Reserved versions a server lists to keep clients honest are left
// out.
func parseVersionNegotiation(b, dcid, scid []byte) ([]string, bool) {
	if len(b) < 7 || b[0]&0x80 == 0 || binary.BigEndian.Uint32(b[1:]) != 0 {
		return nil, false
	}
	rest := b[5:]
	gotDCID, rest, ok := quicConnectionID(rest)
	if !ok || !bytes.Equal(gotDCID, scid) {
		return nil, false
	}
	gotSCID, rest, ok := quicConnectionID(rest)
	if !ok || !bytes.Equal(gotSCID, dcid) || len(rest) == 0 || len(rest)%4 != 0 {
		return nil, false
	}
	versions := []string{}
	for ; len(rest) >= 4; rest = rest[4:] {
		v := binary.BigEndian.Uint32(rest)
		if v&0x0f0f0f0f == 0x0a0a0a0a {
			continue
		}
		versions = append(versions, quicVersionName(v))
	}
	return versions, true
}

func quicConnectionID(b []byte) ([]byte, []byte, bool) {
	if len(b) < 1 || len(b) < 1+int(b[0]) {
		return nil, nil, false
	}
	return b[1 : 1+int(b[0])], b[1+int(b[0]):], true
}

func quicVersionName(v uint32) string {
	switch {
	case v == 1:
		return "QUIC v1"
	case v == 0x6b3343cf:
		return "QUIC v2"
	case v>>8 == 0xff0000:
		return fmt.Sprintf("draft-%d", v&0xff)
	}
	return fmt.Sprintf("0x%08x", v)
}
