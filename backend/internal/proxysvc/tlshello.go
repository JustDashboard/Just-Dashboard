package proxysvc

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"strings"
	"syscall"
	"time"
)

// The deep scan's own ClientHello.
//
// Go's TLS client offers only the suites Go implements, and in TLS 1.3 it
// offers all of its own whatever it is told. Asked through it, a server can
// say whether it takes AES128-SHA, but not whether it takes a DHE, CCM,
// Camellia, NULL or export suite, nor which TLS 1.3 suites it has, nor SSL
// 3.0 — the very things a deep scan is for. So the deep scan writes the
// ClientHello itself and reads the answer: the ServerHello names the version,
// the suite and the TLS 1.3 group the server picked before any key is used,
// and a TLS 1.2 ServerKeyExchange names the curve or the size of the DH group.
// Once it has read that much it hangs up; no key is ever derived, and nothing
// here is a TLS implementation.

// helloOffer is one ClientHello.
type helloOffer struct {
	// version is the one version asked for, SSL 3.0 to TLS 1.3.
	version uint16
	// serverName is the SNI name; empty sends none, as for an address.
	serverName string
	suites     []uint16
	// groups is supported_groups, and shares the TLS 1.3 key_share entries.
	// A group with no share is asked about through a HelloRetryRequest.
	groups []uint16
	shares []keyShare
	// keyExchange reads on to a TLS 1.2 ServerKeyExchange, which names the
	// curve an ECDHE suite uses and the size of a DHE suite's group.
	keyExchange bool
}

type keyShare struct {
	group uint16
	data  []byte
}

// helloAnswer is what a server said to a ClientHello. accepted is a
// ServerHello, alert (or a closed connection) a refusal; err is set when the
// answer is neither — the probe could not connect, timed out, or heard
// something that is not TLS — and says nothing about the offer.
type helloAnswer struct {
	accepted bool
	version  uint16
	suite    uint16
	// group is the TLS 1.3 key share the server answered with or asked for
	// (retry), or the curve a TLS 1.2 ServerKeyExchange names.
	group  uint16
	retry  bool
	dhBits int
	// alert is the refusal in the server's words, or why it counts as one.
	alert string
	err   error
	// address is where the connection went.
	address string
}

// The extension and message numbers this file writes and reads.
const (
	extServerName          = 0
	extSupportedGroups     = 10
	extPointFormats        = 11
	extSignatureAlgorithms = 13
	extPadding             = 21
	extMasterSecret        = 23
	extSupportedVersions   = 43
	extPSKModes            = 45
	extKeyShare            = 51
	extRenegotiationInfo   = 0xff01

	recordChangeCipherSpec = 20
	recordAlert            = 21
	recordHandshake        = 22
	recordApplicationData  = 23

	handshakeServerHello       = 2
	handshakeServerKeyExchange = 12
	handshakeServerHelloDone   = 14

	// scsvRenegotiation stands in for the renegotiation_info extension in an
	// SSL 3.0 hello, which carries no extensions.
	scsvRenegotiation = 0x00ff
)

// helloRetryRandom is the ServerHello random that marks a HelloRetryRequest
// (RFC 8446, 4.1.3).
var helloRetryRandom = []byte{
	0xcf, 0x21, 0xad, 0x74, 0xe5, 0x9a, 0x61, 0x11, 0xbe, 0x1d, 0x8c, 0x02, 0x1e, 0x65, 0xb8, 0x91,
	0xc2, 0xa2, 0x11, 0x16, 0x7a, 0xbb, 0x8c, 0x5e, 0x07, 0x9e, 0x09, 0xe2, 0xc8, 0xa8, 0x33, 0x9c,
}

// helloSignatures is every signature scheme a current client offers and the
// SHA-1 ones an old server may only have, so no suite is refused for want of
// a signature the certificate could have made.
var helloSignatures = []uint16{
	0x0403, 0x0503, 0x0603, // ecdsa_secp256r1_sha256 … secp521r1_sha512
	0x0807, 0x0808, // ed25519, ed448
	0x0804, 0x0805, 0x0806, // rsa_pss_rsae_sha256 … sha512
	0x0809, 0x080a, 0x080b, // rsa_pss_pss_sha256 … sha512
	0x0401, 0x0501, 0x0601, // rsa_pkcs1_sha256 … sha512
	0x0203, 0x0201, 0x0202, // ecdsa_sha1, rsa_pkcs1_sha1, dsa_sha1
	0x0402, 0x0502, 0x0602, // dsa_sha256 … sha512
}

// marshal is the offer as one TLS record.
func (o helloOffer) marshal() ([]byte, error) {
	legacy := o.version
	if o.version == tls.VersionTLS13 {
		// TLS 1.3 is asked for in supported_versions; the legacy field
		// says 1.2, as every TLS 1.3 client's does.
		legacy = tls.VersionTLS12
	}
	body := binary.BigEndian.AppendUint16(nil, legacy)
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return nil, err
	}
	body = append(body, random...)
	if o.version == tls.VersionTLS13 {
		// A session ID, as TLS 1.3 clients send for middlebox compatibility.
		session := make([]byte, 32)
		if _, err := rand.Read(session); err != nil {
			return nil, err
		}
		body = append(body, 32)
		body = append(body, session...)
	} else {
		body = append(body, 0)
	}
	suites := o.suites
	if o.version == versionSSL30 {
		suites = append(append([]uint16{}, suites...), scsvRenegotiation)
	}
	body = binary.BigEndian.AppendUint16(body, uint16(2*len(suites)))
	for _, s := range suites {
		body = binary.BigEndian.AppendUint16(body, s)
	}
	body = append(body, 1, 0) // compression: null only

	if o.version != versionSSL30 {
		ext := o.extensions()
		// A hello between 256 and 511 bytes long hangs some F5 load
		// balancers, which is why every browser pads one to 512 (RFC 7685).
		if n := 4 + len(body) + 2 + len(ext); n >= 256 && n < 512 {
			pad := 512 - n - 4
			if pad < 0 {
				pad = 0
			}
			ext = appendExtension(ext, extPadding, make([]byte, pad))
		}
		body = binary.BigEndian.AppendUint16(body, uint16(len(ext)))
		body = append(body, ext...)
	}

	message := []byte{1, byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))}
	message = append(message, body...)
	if len(message) > 16384 {
		return nil, fmt.Errorf("a ClientHello of %d bytes does not fit one record", len(message))
	}
	recordVersion := uint16(tls.VersionTLS10)
	if o.version == versionSSL30 {
		recordVersion = versionSSL30
	}
	record := []byte{recordHandshake}
	record = binary.BigEndian.AppendUint16(record, recordVersion)
	record = binary.BigEndian.AppendUint16(record, uint16(len(message)))
	return append(record, message...), nil
}

func (o helloOffer) extensions() []byte {
	var ext []byte
	if o.serverName != "" && net.ParseIP(o.serverName) == nil {
		name := []byte(o.serverName)
		list := []byte{0} // host_name
		list = binary.BigEndian.AppendUint16(list, uint16(len(name)))
		list = append(list, name...)
		data := binary.BigEndian.AppendUint16(nil, uint16(len(list)))
		ext = appendExtension(ext, extServerName, append(data, list...))
	}
	if len(o.groups) > 0 {
		data := binary.BigEndian.AppendUint16(nil, uint16(2*len(o.groups)))
		for _, g := range o.groups {
			data = binary.BigEndian.AppendUint16(data, g)
		}
		ext = appendExtension(ext, extSupportedGroups, data)
	}
	if o.version >= tls.VersionTLS12 {
		// Older versions have no such extension, and a client offering only
		// them must not send it (RFC 5246, 7.4.1.4.1).
		sigs := binary.BigEndian.AppendUint16(nil, uint16(2*len(helloSignatures)))
		for _, s := range helloSignatures {
			sigs = binary.BigEndian.AppendUint16(sigs, s)
		}
		ext = appendExtension(ext, extSignatureAlgorithms, sigs)
	}
	if o.version == tls.VersionTLS13 {
		ext = appendExtension(ext, extSupportedVersions, []byte{2, 0x03, 0x04})
		ext = appendExtension(ext, extPSKModes, []byte{1, 1}) // psk_dhe_ke
		var shares []byte
		for _, s := range o.shares {
			shares = binary.BigEndian.AppendUint16(shares, s.group)
			shares = binary.BigEndian.AppendUint16(shares, uint16(len(s.data)))
			shares = append(shares, s.data...)
		}
		ext = appendExtension(ext, extKeyShare, append(binary.BigEndian.AppendUint16(nil, uint16(len(shares))), shares...))
	} else {
		ext = appendExtension(ext, extPointFormats, []byte{1, 0}) // uncompressed
		ext = appendExtension(ext, extRenegotiationInfo, []byte{0})
		ext = appendExtension(ext, extMasterSecret, nil)
	}
	return ext
}

func appendExtension(b []byte, id uint16, data []byte) []byte {
	b = binary.BigEndian.AppendUint16(b, id)
	b = binary.BigEndian.AppendUint16(b, uint16(len(data)))
	return append(b, data...)
}

// probeTimeout bounds one probe, the connection and the answer together.
const probeTimeout = 5 * time.Second

// sendHello dials addr, sends the offer and reads the answer.
func sendHello(ctx context.Context, addr string, o helloOffer) helloAnswer {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	hello, err := o.marshal()
	if err != nil {
		return helloAnswer{err: err}
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return helloAnswer{err: err}
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}
	// The deadline alone would leave a cancelled scan waiting out its five
	// seconds on a server that never answers.
	stop := context.AfterFunc(ctx, func() { conn.SetDeadline(time.Now()) })
	defer stop()
	answer := helloAnswer{address: conn.RemoteAddr().String()}
	if _, err := conn.Write(hello); err != nil {
		answer.err = err
		return answer
	}
	readAnswer(conn, o, &answer)
	if answer.err != nil && ctx.Err() != nil && isTimeout(answer.err) {
		answer.err = ctx.Err()
	}
	return answer
}

// errNotTLS is an answer that is not a TLS record at all.
var errNotTLS = errors.New("the answer is not TLS")

// readAnswer reads records until the offer's question is answered: the
// ServerHello, and for keyExchange the ServerKeyExchange after it.
func readAnswer(r io.Reader, o helloOffer, answer *helloAnswer) {
	var messages []byte
	read := 0
	for {
		header := make([]byte, 5)
		if _, err := io.ReadFull(r, header); err != nil {
			closedAnswer(answer, err)
			return
		}
		kind, length := header[0], int(binary.BigEndian.Uint16(header[3:]))
		if kind < recordChangeCipherSpec || kind > recordApplicationData || header[1] != 3 || length > 18432 {
			answer.err = fmt.Errorf("%w: it began %q", errNotTLS, printable(header))
			return
		}
		read += length
		if read > 1<<16 {
			answer.err = errors.New("the server sent more than 64 KB before its key exchange")
			return
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(r, payload); err != nil {
			closedAnswer(answer, err)
			return
		}
		switch kind {
		case recordAlert:
			if len(payload) < 2 {
				answer.err = errors.New("the server sent a truncated alert")
				return
			}
			answer.alert = strings.TrimPrefix(tls.AlertError(payload[1]).Error(), "tls: ")
			return
		case recordChangeCipherSpec:
			continue
		case recordApplicationData:
			// Encrypted records follow a TLS 1.3 ServerHello, which has been
			// read by then; before it, they are nonsense.
			answer.err = errors.New("the server sent encrypted data before its ServerHello")
			return
		}
		messages = append(messages, payload...)
		for len(messages) >= 4 {
			size := int(messages[1])<<16 | int(messages[2])<<8 | int(messages[3])
			if len(messages) < 4+size {
				break
			}
			kind, body := messages[0], messages[4:4+size]
			messages = messages[4+size:]
			switch kind {
			case handshakeServerHello:
				if err := parseServerHello(body, answer); err != nil {
					answer.err = err
					return
				}
				if !o.keyExchange || answer.version == tls.VersionTLS13 || answer.version != o.version {
					return
				}
			case handshakeServerKeyExchange:
				if !answer.accepted {
					answer.err = errors.New("the server sent its key exchange before its ServerHello")
					return
				}
				parseKeyExchange(body, answer)
				return
			case handshakeServerHelloDone:
				// No ServerKeyExchange: the suite exchanges no ephemeral key.
				return
			}
		}
	}
}

// closedAnswer reads a connection that ended before the answer did. A server
// that hangs up on a ClientHello has refused it, as the version probe reads
// it: OpenSSL and others close without an alert when nothing is shared.
func closedAnswer(answer *helloAnswer, err error) {
	switch {
	case answer.accepted:
		// The ServerHello was read; what came after is not the question.
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, syscall.ECONNRESET):
		answer.alert = "closed the connection"
	default:
		answer.err = err
	}
}

// parseServerHello reads the version, the suite and, for TLS 1.3, the group.
func parseServerHello(b []byte, answer *helloAnswer) error {
	bad := errors.New("the server sent a malformed ServerHello")
	if len(b) < 2+32+1 {
		return bad
	}
	answer.version = binary.BigEndian.Uint16(b)
	answer.retry = string(b[2:34]) == string(helloRetryRandom)
	rest := b[34:]
	idLen := int(rest[0])
	if len(rest) < 1+idLen+3 {
		return bad
	}
	rest = rest[1+idLen:]
	answer.suite = binary.BigEndian.Uint16(rest)
	rest = rest[3:] // suite and compression
	if len(rest) >= 2 {
		extLen := int(binary.BigEndian.Uint16(rest))
		rest = rest[2:]
		if len(rest) < extLen {
			return bad
		}
		rest = rest[:extLen]
		for len(rest) >= 4 {
			id, n := binary.BigEndian.Uint16(rest), int(binary.BigEndian.Uint16(rest[2:]))
			if len(rest) < 4+n {
				return bad
			}
			data := rest[4 : 4+n]
			rest = rest[4+n:]
			switch {
			case id == extSupportedVersions && n == 2:
				answer.version = binary.BigEndian.Uint16(data)
			case id == extKeyShare && n >= 2:
				// A ServerHello's entry and a retry's selected group both
				// begin with the group.
				answer.group = binary.BigEndian.Uint16(data)
			}
		}
	}
	answer.accepted = true
	return nil
}

// parseKeyExchange reads the curve of an ECDHE key exchange, or the size of a
// DHE one's prime. Anything else leaves both unset.
func parseKeyExchange(b []byte, answer *helloAnswer) {
	if len(b) >= 3 && b[0] == 3 && isECDHE(answer.suite) {
		answer.group = binary.BigEndian.Uint16(b[1:])
		return
	}
	if len(b) >= 2 && isDHE(answer.suite) {
		n := int(binary.BigEndian.Uint16(b))
		if len(b) >= 2+n {
			answer.dhBits = new(big.Int).SetBytes(b[2 : 2+n]).BitLen()
		}
	}
}
