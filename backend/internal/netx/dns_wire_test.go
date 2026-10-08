package netx

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

func dnsNameData(name string) []byte {
	var out []byte
	for _, label := range strings.Split(strings.TrimSuffix(name, "."), ".") {
		out = append(out, byte(len(label)))
		out = append(out, label...)
	}
	return append(out, 0)
}

func TestLookupQueriesTheNamedResolverEvenForLocalhost(t *testing.T) {
	server := startFakeDNS(t, func(name string, qtype uint16) dnsBehavior {
		if name != "localhost" || qtype != typeA {
			t.Errorf("question = %s, %d", name, qtype)
		}
		return dnsBehavior{rdatas: [][]byte{aData("192.0.2.77")}}
	})
	answers, err := lookupVia(context.Background(), server, "localhost", "A")
	if err != nil || len(answers) != 1 || answers[0] != "192.0.2.77" {
		t.Fatalf("resolver was bypassed by hosts file: %v, %v", answers, err)
	}
}

func TestLookupWireRecordTypes(t *testing.T) {
	srvData := binary.BigEndian.AppendUint16(nil, 10)
	srvData = binary.BigEndian.AppendUint16(srvData, 20)
	srvData = binary.BigEndian.AppendUint16(srvData, 5060)
	srvData = append(srvData, dnsNameData("sip.example.com")...)
	for _, tc := range []struct {
		rtype, name, question, answer string
		data                          []byte
	}{
		{"A", "example.com", "example.com", "192.0.2.7", aData("192.0.2.7")},
		{"AAAA", "example.com", "example.com", "2001:db8::7", net.ParseIP("2001:db8::7").To16()},
		{"CNAME", "example.com", "example.com", "target.example.com.", dnsNameData("target.example.com")},
		{"NS", "example.com", "example.com", "ns.example.com.", dnsNameData("ns.example.com")},
		{"MX", "example.com", "example.com", "10 mail.example.com.", mxData(10, "mail.example.com")},
		{"TXT", "example.com", "example.com", "v=spf1 -all", append(txtData("v=spf1 "), txtData("-all")...)},
		{"PTR", "127.0.0.1", "1.0.0.127.in-addr.arpa", "dns.example.com.", dnsNameData("dns.example.com")},
		{"PTR", "::1", reverseDNSName(netip.MustParseAddr("::1")), "dns.example.com.", dnsNameData("dns.example.com")},
		{"SRV", "_sip._tcp.example.com", "_sip._tcp.example.com", "10 20 5060 sip.example.com.", srvData},
	} {
		t.Run(tc.rtype+"/"+tc.name, func(t *testing.T) {
			server := startFakeDNS(t, func(name string, qtype uint16) dnsBehavior {
				if name != tc.question || qtype != uint16(wireLookupTypes[tc.rtype]) {
					t.Errorf("question = %s, %d", name, qtype)
				}
				return dnsBehavior{rdatas: [][]byte{tc.data}}
			})
			answers, err := lookupVia(context.Background(), server, tc.name, tc.rtype)
			if err != nil || len(answers) != 1 || answers[0] != tc.answer {
				t.Fatalf("%s = %v, %v", tc.rtype, answers, err)
			}
		})
	}
}

func TestLookupRetriesTruncatedUDPOverTCP(t *testing.T) {
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	udp, err := net.ListenPacket("udp", tcp.Addr().String())
	if err != nil {
		tcp.Close()
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	t.Cleanup(func() { tcp.Close(); udp.Close(); wg.Wait() })
	wg.Go(func() {
		buf := make([]byte, 1500)
		n, from, err := udp.ReadFrom(buf)
		if err != nil {
			return
		}
		_, qtype, qend := parseQuestion(buf[:n])
		response := buildResponse(buf[:n], qend, qtype, dnsBehavior{})
		binary.BigEndian.PutUint16(response[2:], binary.BigEndian.Uint16(response[2:])|0x0200)
		_, _ = udp.WriteTo(response, from)
	})
	wg.Go(func() {
		conn, err := tcp.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var size [2]byte
		if _, err := io.ReadFull(conn, size[:]); err != nil {
			return
		}
		question := make([]byte, binary.BigEndian.Uint16(size[:]))
		if _, err := io.ReadFull(conn, question); err != nil {
			return
		}
		_, qtype, qend := parseQuestion(question)
		response := buildResponse(question, qend, qtype, dnsBehavior{rdatas: [][]byte{aData("192.0.2.77")}})
		binary.BigEndian.PutUint16(size[:], uint16(len(response)))
		_, _ = conn.Write(size[:])
		_, _ = conn.Write(response)
	})
	answers, err := lookupVia(context.Background(), tcp.Addr().String(), "example.com", "A")
	if err != nil || len(answers) != 1 || answers[0] != "192.0.2.77" {
		t.Fatalf("truncated query = %v, %v", answers, err)
	}
}

func TestDNSResponseRejectsMalformedAndUnrelatedAnswers(t *testing.T) {
	name, err := dnsmessage.NewName("example.com.")
	if err != nil {
		t.Fatal(err)
	}
	question := dnsmessage.Question{Name: name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: 7})
	_ = b.StartQuestions()
	_ = b.Question(question)
	request, _ := b.Finish()
	_, qtype, qend := parseQuestion(request)
	valid := buildResponse(request, qend, qtype, dnsBehavior{rdatas: [][]byte{aData("192.0.2.7")}})
	for _, tc := range []struct {
		name   string
		change func([]byte) []byte
	}{
		{"wrong id", func(b []byte) []byte { b[1]++; return b }},
		{"not a response", func(b []byte) []byte { b[2] &^= 0x80; return b }},
		{"wrong question", func(b []byte) []byte { b[13] = 'X'; return b }},
		{"wrong question type", func(b []byte) []byte { binary.BigEndian.PutUint16(b[qend-4:], 28); return b }},
		{"truncated resource", func(b []byte) []byte { return b[:len(b)-1] }},
		{"broken header", func(b []byte) []byte { return b[:3] }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packet := tc.change(append([]byte(nil), valid...))
			if _, _, err := parseDNSResponse(packet, 7, question); err == nil {
				t.Fatal("malformed or unrelated answer was accepted")
			}
		})
	}
}
