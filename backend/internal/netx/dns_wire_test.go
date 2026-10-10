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

func dnsResponsePacket(t *testing.T, question dnsmessage.Question, answers ...dnsmessage.Resource) []byte {
	t.Helper()
	message := dnsmessage.Message{
		Header:    dnsmessage.Header{ID: 7, Response: true, RecursionAvailable: true},
		Questions: []dnsmessage.Question{question}, Answers: answers,
	}
	packet, err := message.Pack()
	if err != nil {
		t.Fatal(err)
	}
	return packet
}

func dnsAnswer(owner string, body dnsmessage.ResourceBody) dnsmessage.Resource {
	return dnsmessage.Resource{
		Header: dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName(owner), Class: dnsmessage.ClassINET, TTL: 60},
		Body:   body,
	}
}

func dnsAlias(owner, target string) dnsmessage.Resource {
	return dnsAnswer(owner, &dnsmessage.CNAMEResource{CNAME: dnsmessage.MustNewName(target)})
}

func TestDNSResponseIgnoresUnrelatedRecordOwners(t *testing.T) {
	for _, tc := range []struct {
		qtype dnsmessage.Type
		body  dnsmessage.ResourceBody
	}{
		{dnsmessage.TypeA, &dnsmessage.AResource{A: [4]byte{192, 0, 2, 99}}},
		{dnsmessage.TypeAAAA, &dnsmessage.AAAAResource{AAAA: netip.MustParseAddr("2001:db8::99").As16()}},
		{dnsmessage.TypeCNAME, &dnsmessage.CNAMEResource{CNAME: dnsmessage.MustNewName("canonical.example.")}},
		{dnsmessage.TypeMX, &dnsmessage.MXResource{Pref: 10, MX: dnsmessage.MustNewName("mail.example.")}},
		{dnsmessage.TypeTXT, &dnsmessage.TXTResource{TXT: []string{"unrelated text"}}},
		{dnsmessage.TypeNS, &dnsmessage.NSResource{NS: dnsmessage.MustNewName("ns.example.")}},
		{dnsmessage.TypePTR, &dnsmessage.PTRResource{PTR: dnsmessage.MustNewName("host.example.")}},
		{dnsmessage.TypeSRV, &dnsmessage.SRVResource{Port: 443, Target: dnsmessage.MustNewName("service.example.")}},
	} {
		t.Run(tc.qtype.String(), func(t *testing.T) {
			question := dnsmessage.Question{Name: dnsmessage.MustNewName("www.example."), Type: tc.qtype, Class: dnsmessage.ClassINET}
			packet := dnsResponsePacket(t, question, dnsAnswer("unrelated.example.", tc.body))
			answers, _, err := parseDNSResponse(packet, 7, question)
			if err != nil || len(answers) != 0 {
				t.Fatalf("unrelated owner was attributed to the question: %v, %v", answers, err)
			}
		})
	}
}

func TestDNSResponseFollowsOnlyTheQuestionsCNAMEChain(t *testing.T) {
	question := dnsmessage.Question{Name: dnsmessage.MustNewName("www.example."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}
	packet := dnsResponsePacket(t, question,
		dnsAnswer("Canonical.Example.", &dnsmessage.AResource{A: [4]byte{192, 0, 2, 7}}),
		dnsAnswer("unrelated.example.", &dnsmessage.AResource{A: [4]byte{192, 0, 2, 99}}),
		dnsAlias("intermediate.example.", "canonical.example."),
		dnsAlias("WWW.Example.", "Intermediate.Example."),
		// An unrelated cycle must not change the answer for this question.
		dnsAlias("unrelated.example.", "unrelated.example."),
	)
	answers, _, err := parseDNSResponse(packet, 7, question)
	if err != nil || len(answers) != 1 || answers[0] != "192.0.2.7" {
		t.Fatalf("canonical answer = %v, %v", answers, err)
	}

	question.Type = dnsmessage.TypeCNAME
	packet = dnsResponsePacket(t, question,
		dnsAlias("intermediate.example.", "canonical.example."),
		dnsAlias("www.example.", "intermediate.example."),
	)
	answers, _, err = parseDNSResponse(packet, 7, question)
	if err != nil || len(answers) != 1 || answers[0] != "intermediate.example." {
		t.Fatalf("CNAME question should return its own alias, not subsequent aliases: %v, %v", answers, err)
	}
}

func TestDNSResponseRejectsMalformedCNAMEChains(t *testing.T) {
	question := dnsmessage.Question{Name: dnsmessage.MustNewName("www.example."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}
	for _, tc := range []struct {
		name    string
		answers []dnsmessage.Resource
	}{
		{"self cycle", []dnsmessage.Resource{dnsAlias("www.example.", "www.example.")}},
		{"two-node cycle", []dnsmessage.Resource{dnsAlias("www.example.", "other.example."), dnsAlias("other.example.", "www.example.")}},
		{"conflicting targets", []dnsmessage.Resource{dnsAlias("www.example.", "one.example."), dnsAlias("www.example.", "two.example.")}},
		{"alias also has requested data", []dnsmessage.Resource{dnsAlias("www.example.", "canonical.example."), dnsAnswer("www.example.", &dnsmessage.AResource{A: [4]byte{192, 0, 2, 99}})}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packet := dnsResponsePacket(t, question, tc.answers...)
			if answers, _, err := parseDNSResponse(packet, 7, question); err == nil {
				t.Fatalf("malformed chain accepted: %v", answers)
			}
		})
	}
}

func TestDNSResponseUsesASCIICaseFolding(t *testing.T) {
	question := dnsmessage.Question{Name: dnsmessage.MustNewName("k.example."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}
	answer := &dnsmessage.AResource{A: [4]byte{192, 0, 2, 7}}
	packet := dnsResponsePacket(t, question, dnsAnswer("K.Example.", answer))
	if answers, _, err := parseDNSResponse(packet, 7, question); err != nil || len(answers) != 1 {
		t.Fatalf("ASCII case variant rejected: %v, %v", answers, err)
	}
	caseQuestion := question
	caseQuestion.Name = dnsmessage.MustNewName("K.EXAMPLE.")
	packet = dnsResponsePacket(t, caseQuestion, dnsAnswer("K.Example.", answer))
	if answers, _, err := parseDNSResponse(packet, 7, question); err != nil || len(answers) != 1 {
		t.Fatalf("ASCII case variation in response question rejected: %v, %v", answers, err)
	}
	packet = dnsResponsePacket(t, question, dnsAnswer("K.example.", answer))
	if answers, _, err := parseDNSResponse(packet, 7, question); err != nil || len(answers) != 0 {
		t.Fatalf("Unicode case equivalent treated as DNS equality: %v, %v", answers, err)
	}
	otherQuestion := question
	otherQuestion.Name = dnsmessage.MustNewName("K.example.")
	packet = dnsResponsePacket(t, otherQuestion)
	if _, _, err := parseDNSResponse(packet, 7, question); err == nil {
		t.Fatal("response to a Unicode-equivalent question accepted")
	}
}
