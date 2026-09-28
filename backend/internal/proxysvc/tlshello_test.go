package proxysvc

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// record wraps handshake messages or an alert in a TLS record.
func record(kind byte, payload []byte) []byte {
	out := []byte{kind, 3, 3}
	out = binary.BigEndian.AppendUint16(out, uint16(len(payload)))
	return append(out, payload...)
}

func handshakeMessage(kind byte, body []byte) []byte {
	return append([]byte{kind, byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))}, body...)
}

// serverHello is a ServerHello body: version, a random, no session ID, the
// suite, no compression and the extensions given.
func serverHello(version, suite uint16, random []byte, extensions ...[]byte) []byte {
	b := binary.BigEndian.AppendUint16(nil, version)
	if random == nil {
		random = make([]byte, 32)
	}
	b = append(b, random...)
	b = append(b, 0)
	b = binary.BigEndian.AppendUint16(b, suite)
	b = append(b, 0)
	var ext []byte
	for _, e := range extensions {
		ext = append(ext, e...)
	}
	if len(extensions) > 0 {
		b = binary.BigEndian.AppendUint16(b, uint16(len(ext)))
		b = append(b, ext...)
	}
	return b
}

func readCrafted(t *testing.T, o helloOffer, stream []byte) helloAnswer {
	t.Helper()
	var a helloAnswer
	readAnswer(bytes.NewReader(stream), o, &a)
	return a
}

func TestReadAnswerReadsWhatTheServerPicked(t *testing.T) {
	tls12 := helloOffer{version: tls.VersionTLS12}
	a := readCrafted(t, tls12, record(recordHandshake, handshakeMessage(handshakeServerHello, serverHello(tls.VersionTLS12, 0xc02f, nil))))
	if !a.accepted || a.version != tls.VersionTLS12 || a.suite != 0xc02f || a.err != nil {
		t.Fatalf("TLS 1.2 ServerHello: %+v", a)
	}

	// TLS 1.3 says its version in supported_versions and its group in
	// key_share; the legacy field says 1.2.
	versions := appendExtension(nil, extSupportedVersions, []byte{3, 4})
	share := appendExtension(nil, extKeyShare, append([]byte{0x11, 0xec, 0, 2}, 1, 2))
	a = readCrafted(t, helloOffer{version: tls.VersionTLS13},
		record(recordHandshake, handshakeMessage(handshakeServerHello, serverHello(tls.VersionTLS12, 0x1301, nil, versions, share))))
	if !a.accepted || a.version != tls.VersionTLS13 || a.suite != 0x1301 || a.group != groupX25519MLKEM768 || a.retry {
		t.Fatalf("TLS 1.3 ServerHello: %+v", a)
	}

	// A HelloRetryRequest names the group it wants and nothing else.
	a = readCrafted(t, helloOffer{version: tls.VersionTLS13},
		record(recordHandshake, handshakeMessage(handshakeServerHello, serverHello(tls.VersionTLS12, 0x1302, helloRetryRandom,
			versions, appendExtension(nil, extKeyShare, []byte{0x00, 0x1e})))))
	if !a.accepted || !a.retry || a.group != groupX448 || a.suite != 0x1302 {
		t.Fatalf("HelloRetryRequest: %+v", a)
	}
}

func TestReadAnswerReadsTheKeyExchange(t *testing.T) {
	offer := helloOffer{version: tls.VersionTLS12, keyExchange: true}
	hello := handshakeMessage(handshakeServerHello, serverHello(tls.VersionTLS12, 0xc02f, nil))
	// A certificate large enough to span records, which the reader must
	// reassemble before it reaches the key exchange.
	certificate := handshakeMessage(11, bytes.Repeat([]byte{0xaa}, 20000))
	ecdhe := handshakeMessage(handshakeServerKeyExchange, []byte{3, 0x00, 0x17, 65})
	stream := append(hello, certificate...)
	stream = append(stream, ecdhe...)
	var records []byte
	for len(stream) > 0 {
		n := min(len(stream), 16384)
		records = append(records, record(recordHandshake, stream[:n])...)
		stream = stream[n:]
	}
	a := readCrafted(t, offer, records)
	if !a.accepted || a.group != groupP256 || a.err != nil {
		t.Fatalf("ECDHE key exchange: %+v", a)
	}

	// A DHE key exchange opens with its prime, whose length is the group's
	// size: 1024 bits here.
	prime := append([]byte{0x80}, bytes.Repeat([]byte{0xff}, 127)...)
	dhe := handshakeMessage(handshakeServerKeyExchange, append(binary.BigEndian.AppendUint16(nil, uint16(len(prime))), prime...))
	a = readCrafted(t, offer, record(recordHandshake, append(handshakeMessage(handshakeServerHello, serverHello(tls.VersionTLS12, 0x009e, nil)), dhe...)))
	if a.dhBits != 1024 {
		t.Fatalf("DHE key exchange: %+v", a)
	}

	// RSA key exchange sends none: the ServerHelloDone ends the question.
	a = readCrafted(t, offer, record(recordHandshake, append(handshakeMessage(handshakeServerHello, serverHello(tls.VersionTLS12, 0x009c, nil)),
		handshakeMessage(handshakeServerHelloDone, nil)...)))
	if !a.accepted || a.group != 0 || a.dhBits != 0 || a.err != nil {
		t.Fatalf("RSA key exchange: %+v", a)
	}
}

func TestReadAnswerTellsARefusalFromSilence(t *testing.T) {
	offer := helloOffer{version: tls.VersionTLS12}
	if a := readCrafted(t, offer, record(recordAlert, []byte{2, 40})); a.accepted || a.alert != "handshake failure" || a.err != nil {
		t.Fatalf("alert: %+v", a)
	}
	if a := readCrafted(t, offer, nil); a.alert != "closed the connection" || a.err != nil {
		t.Fatalf("a hang-up is a refusal: %+v", a)
	}
	// nginx's `listen 443;` without ssl answers a ClientHello with HTTP.
	if a := readCrafted(t, offer, []byte("HTTP/1.1 400 Bad Request\r\n")); !errors.Is(a.err, errNotTLS) || a.alert != "" {
		t.Fatalf("plain HTTP: %+v", a)
	}
}

// The hello must be one a real stack takes: Go's own server reads it, picks
// from it, and says so.
func TestAGoServerAnswersTheHello(t *testing.T) {
	cert, _ := scanTestCert(t, 40, nil)
	addr := tlsListener(t, &tls.Config{Certificates: []tls.Certificate{cert}}, func(*tls.Conn) {})
	for _, o := range []helloOffer{
		{version: tls.VersionTLS12, suites: suitesFor(tls.VersionTLS12), groups: []uint16{groupX25519}},
		{version: tls.VersionTLS13, suites: suitesFor(tls.VersionTLS13), groups: []uint16{groupX25519}},
	} {
		shares, err := keyShares(o.groups...)
		if err != nil {
			t.Fatal(err)
		}
		o.shares = shares
		a := sendHello(context.Background(), addr, o)
		if !a.accepted || a.version != o.version || a.err != nil {
			t.Fatalf("%s: %+v", versionName(o.version), a)
		}
	}
}

func TestTheHelloIsPaddedPastTheF5Range(t *testing.T) {
	for _, o := range []helloOffer{
		{version: tls.VersionTLS12, serverName: "scan.test", suites: suitesFor(tls.VersionTLS12), groups: []uint16{groupX25519}},
		{version: tls.VersionTLS12, suites: []uint16{0xc02f}, groups: []uint16{groupX25519}},
		{version: tls.VersionTLS10, suites: suitesFor(tls.VersionTLS10)},
	} {
		hello, err := o.marshal()
		if err != nil {
			t.Fatal(err)
		}
		if n := len(hello) - 5; n >= 256 && n < 512 {
			t.Errorf("a hello of %d bytes is in the range that hangs F5 load balancers", n)
		}
		if int(binary.BigEndian.Uint16(hello[3:])) != len(hello)-5 {
			t.Errorf("the record length disagrees with the hello")
		}
	}
	// SSL 3.0 carries no extensions, and signals secure renegotiation with
	// the SCSV instead.
	hello, _ := helloOffer{version: versionSSL30, suites: []uint16{0x002f}}.marshal()
	if !bytes.Contains(hello, []byte{0x00, 0x2f, 0x00, 0xff, 1, 0}) || !bytes.HasSuffix(hello, []byte{1, 0}) {
		t.Errorf("SSL 3.0 hello % x", hello)
	}
}

// A server that accepts the connection and never answers holds a probe only
// for its five seconds, and a cancelled scan not even that.
func TestAProbeEndsWithItsScan(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go io.Copy(io.Discard, conn)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	started := time.Now()
	a := sendHello(ctx, ln.Addr().String(), helloOffer{version: tls.VersionTLS12, suites: []uint16{0xc02f}})
	if took := time.Since(started); took > 2*time.Second {
		t.Fatalf("the probe outlived its scan by %s", took)
	}
	if a.err == nil || a.accepted || a.alert != "" {
		t.Fatalf("silence is neither an answer nor a refusal: %+v", a)
	}

	cancelled, stop := context.WithCancel(context.Background())
	stop()
	a = sendHello(cancelled, ln.Addr().String(), helloOffer{version: tls.VersionTLS12, suites: []uint16{0xc02f}})
	if a.err == nil || !strings.Contains(probeFailure(a.err), "cancelled") {
		t.Fatalf("a cancelled scan: %+v (%s)", a, probeFailure(a.err))
	}
}
