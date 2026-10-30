package dnsrecords

import (
	"context"
	"crypto/hmac"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

// A request signed here verifies with the same key and fails with another;
// a name is read back through a compression pointer.
func TestTSIGSignsAndAnswersVerify(t *testing.T) {
	key := tsigKey{name: "jd-key", algorithm: tsigAlgorithms["HMAC-SHA256"].wire, secret: []byte("0123456789abcdef"), hash: tsigAlgorithms["HMAC-SHA256"].hash}
	now := time.Unix(1_800_000_000, 0)
	request := dnsHeader(0x1234, dnsOpQuery, false, [4]uint16{1, 0, 0, 0})
	request, _ = appendName(request, "example.test")
	request = appendUint16(request, dnsTypeSOA)
	request = appendUint16(request, dnsClassIN)
	signed, mac, err := key.sign(request, now)
	if err != nil || binary.BigEndian.Uint16(signed[10:]) != 1 {
		t.Fatalf("signed %x %v", signed, err)
	}
	reply, err := parseReply(signed)
	if err != nil || len(reply.Additional) != 1 || reply.Additional[0].Type != dnsTypeTSIG {
		t.Fatalf("the signed request reads back as %+v %v", reply, err)
	}
	fields, err := readTSIG(signed, reply.Additional[0])
	variables, _ := key.variables(fields.signed, fields.fudge, 0, nil)
	if err != nil || !hmac.Equal(fields.mac, mac) || !hmac.Equal(mac, key.mac(unsigned(signed, reply.Additional[0], fields.original), variables)) {
		t.Fatalf("the request's MAC does not verify: %+v %v", fields, err)
	}
	// An answer signed over the request's MAC, its owner name compressed.
	answer := dnsHeader(0x1234, dnsOpQuery, false, [4]uint16{1, 1, 0, 0})
	answer[2] |= 0x84
	answer, _ = appendName(answer, "example.test")
	answer = appendUint16(answer, dnsTypeCNAME)
	answer = appendUint16(answer, dnsClassIN)
	answer = append(answer, 0xc0, 12)
	answer = appendUint16(answer, dnsTypeCNAME)
	answer = appendUint16(answer, dnsClassIN)
	answer = appendUint32(answer, 60)
	target := []byte{3, 'w', 'w', 'w', 0xc0, 12}
	answer = appendUint16(answer, uint16(len(target)))
	answer = append(answer, target...)
	signedAnswer := signAnswer(t, key, answer, mac, now)
	parsed, err := parseReply(signedAnswer)
	if err != nil || !parsed.Authoritative || len(parsed.Answers) != 1 || parsed.Answers[0].Name != "example.test" {
		t.Fatalf("the answer reads as %+v %v", parsed, err)
	}
	if value, err := (&rfc2136{}).value(parsed, parsed.Answers[0]); err != nil || value != "www.example.test" {
		t.Fatalf("the compressed CNAME = %q %v", value, err)
	}
	if err := key.verify(signedAnswer, parsed, mac, now); err != nil {
		t.Fatalf("the answer does not verify: %v", err)
	}
	other := key
	other.secret = []byte("another secret!!")
	if err := other.verify(signedAnswer, parsed, mac, now); err == nil || !strings.Contains(err.Error(), "does not verify") {
		t.Fatalf("another key's verify = %v", err)
	}
	if err := key.verify(signedAnswer, parsed, mac, now.Add(10*time.Minute)); err == nil || !strings.Contains(err.Error(), "outside the 300 allowed") {
		t.Fatalf("a stale answer = %v", err)
	}
	if err := key.verify(answer, mustParse(t, answer), mac, now); err == nil || !strings.Contains(err.Error(), "not signed") {
		t.Fatalf("an unsigned answer = %v", err)
	}
}

func mustParse(t *testing.T, message []byte) dnsReply {
	t.Helper()
	reply, err := parseReply(message)
	if err != nil {
		t.Fatal(err)
	}
	return reply
}

// signAnswer is a server's half of TSIG: the answer's MAC over the
// request's, the answer, and the variables.
func signAnswer(t *testing.T, key tsigKey, answer, requestMAC []byte, now time.Time) []byte {
	t.Helper()
	variables, err := key.variables(uint64(now.Unix()), tsigFudge, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	mac := key.mac(appendUint16(nil, uint16(len(requestMAC))), requestMAC, answer, variables)
	data, _ := appendName(nil, key.algorithm)
	signed := uint64(now.Unix())
	data = append(data, byte(signed>>40), byte(signed>>32), byte(signed>>24), byte(signed>>16), byte(signed>>8), byte(signed))
	data = appendUint16(data, tsigFudge)
	data = appendUint16(data, uint16(len(mac)))
	data = append(data, mac...)
	data = appendUint16(data, binary.BigEndian.Uint16(answer))
	data = appendUint16(data, 0)
	data = appendUint16(data, 0)
	out := append([]byte(nil), answer...)
	binary.BigEndian.PutUint16(out[10:], binary.BigEndian.Uint16(out[10:])+1)
	out, _ = appendRR(out, key.name, dnsTypeTSIG, dnsClassANY, 0, data)
	return out
}

// dnsFake is an authoritative server over TCP for one zone that takes
// RFC 2136 updates signed with one key, as BIND's update-policy would.
type dnsFake struct {
	t        *testing.T
	zone     string
	key      tsigKey
	listener net.Listener
	mu       sync.Mutex
	sets     map[[2]string][]string
	ttls     map[[2]string]uint32
	updates  int
}

func startDNSFake(t *testing.T, zone string, key tsigKey) *dnsFake {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &dnsFake{t: t, zone: zone, key: key, listener: listener, sets: map[[2]string][]string{}, ttls: map[[2]string]uint32{}}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go fake.serve(conn)
		}
	}()
	return fake
}

func (f *dnsFake) serve(conn net.Conn) {
	defer conn.Close()
	var length [2]byte
	if _, err := io.ReadFull(conn, length[:]); err != nil {
		return
	}
	message := make([]byte, binary.BigEndian.Uint16(length[:]))
	if _, err := io.ReadFull(conn, message); err != nil {
		return
	}
	answer := f.answer(message)
	_, _ = conn.Write(append(appendUint16(nil, uint16(len(answer))), answer...))
}

// answer builds the reply: a header with the request's id and question,
// then sections; signed when the request was.
func (f *dnsFake) answer(message []byte) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	request, err := parseReply(message)
	if err != nil {
		return dnsHeader(0, 0, false, [4]uint16{})
	}
	opcode := int(binary.BigEndian.Uint16(message[2:])>>11) & 0xf
	question, next, _ := readName(message, 12)
	question = Canonical(question)
	qtype := binary.BigEndian.Uint16(message[next:])
	var requestMAC []byte
	signed := len(request.Additional) > 0 && request.Additional[len(request.Additional)-1].Type == dnsTypeTSIG
	if signed {
		record := request.Additional[len(request.Additional)-1]
		fields, err := readTSIG(message, record)
		variables, _ := f.key.variables(fields.signed, fields.fudge, 0, nil)
		if err != nil || record.Name != f.key.name || !hmac.Equal(fields.mac, f.key.mac(unsigned(message, record, fields.original), variables)) {
			// BADSIG, unsigned, as a server answers a key it cannot verify.
			return f.reply(message, question, qtype, 9, nil, nil, nil)
		}
		requestMAC = fields.mac
	}
	if opcode == dnsOpUpdate {
		if !signed {
			return f.reply(message, question, qtype, 5, nil, nil, nil)
		}
		return f.reply(message, question, qtype, f.update(message, request, question), nil, nil, requestMAC)
	}
	if !InZone(question, f.zone) {
		return f.reply(message, question, qtype, 5, nil, nil, requestMAC)
	}
	soa := func() []byte {
		out, _ := appendRR(nil, f.zone, dnsTypeSOA, dnsClassIN, 60, mustSOA(f.zone))
		return out
	}
	if qtype == dnsTypeSOA {
		if question == f.zone {
			return f.reply(message, question, qtype, 0, [][]byte{soa()}, nil, requestMAC)
		}
		return f.reply(message, question, qtype, 0, nil, [][]byte{soa()}, requestMAC)
	}
	kind := map[uint16]string{dnsTypeA: "A", dnsTypeAAAA: "AAAA", dnsTypeCNAME: "CNAME"}[qtype]
	answers := [][]byte{}
	for _, value := range f.sets[[2]string{question, kind}] {
		data, _ := rdata(Type(kind), value)
		// The owner points back at the question, as servers compress it.
		record := []byte{0xc0, 12}
		record = appendUint16(record, qtype)
		record = appendUint16(record, dnsClassIN)
		record = appendUint32(record, f.ttls[[2]string{question, kind}])
		record = appendUint16(record, uint16(len(data)))
		answers = append(answers, append(record, data...))
	}
	return f.reply(message, question, qtype, 0, answers, nil, requestMAC)
}

func mustSOA(zone string) []byte {
	out, _ := appendName(nil, "ns."+zone)
	out, _ = appendName(out, "hostmaster."+zone)
	for range 5 {
		out = appendUint32(out, 60)
	}
	return out
}

// update applies an UPDATE's prerequisites and changes, RFC 2136 §3.
func (f *dnsFake) update(message []byte, request dnsReply, zone string) int {
	if zone != f.zone {
		return 10
	}
	kinds := map[uint16]string{dnsTypeA: "A", dnsTypeAAAA: "AAAA", dnsTypeCNAME: "CNAME"}
	for _, prerequisite := range request.Answers {
		key := [2]string{Canonical(prerequisite.Name), kinds[prerequisite.Type]}
		switch prerequisite.Class {
		case dnsClassNONE:
			if len(f.sets[key]) > 0 {
				return 7
			}
		case dnsClassANY:
			if len(f.sets[key]) == 0 {
				return 8
			}
		}
	}
	next := map[[2]string][]string{}
	for key, values := range f.sets {
		next[key] = append([]string(nil), values...)
	}
	for _, change := range request.Authority {
		name, kind := Canonical(change.Name), kinds[change.Type]
		if !InZone(name, f.zone) {
			return 10
		}
		key := [2]string{name, kind}
		switch change.Class {
		case dnsClassANY:
			delete(next, key)
		case dnsClassIN:
			value := ""
			if change.Type == dnsTypeCNAME {
				target, _, _ := readName(message, change.dataAt)
				value = Canonical(target)
			} else {
				address, _ := netip.AddrFromSlice(change.Data)
				value = address.String()
			}
			next[key] = append(next[key], value)
			f.ttls[key] = change.TTL
		}
	}
	f.sets = next
	f.updates++
	return 0
}

// reply writes the answer's sections and signs it when the request was.
func (f *dnsFake) reply(message []byte, question string, qtype uint16, rcode int, answers, authority [][]byte, requestMAC []byte) []byte {
	out := dnsHeader(binary.BigEndian.Uint16(message), int(binary.BigEndian.Uint16(message[2:])>>11)&0xf, false,
		[4]uint16{1, uint16(len(answers)), uint16(len(authority)), 0})
	flags := binary.BigEndian.Uint16(out[2:]) | 1<<15 | 1<<10 | uint16(rcode)
	binary.BigEndian.PutUint16(out[2:], flags)
	out, _ = appendName(out, question)
	out = appendUint16(out, qtype)
	out = appendUint16(out, dnsClassIN)
	for _, record := range append(answers, authority...) {
		out = append(out, record...)
	}
	if requestMAC == nil {
		return out
	}
	return signAnswer(f.t, f.key, out, requestMAC, time.Now())
}

func (f *dnsFake) credentials(secret string) string {
	return "dns_rfc2136_server = 127.0.0.1\ndns_rfc2136_port = " + portOf(f.listener) + "\ndns_rfc2136_name = " + f.key.name +
		".\ndns_rfc2136_secret = " + secret + "\ndns_rfc2136_algorithm = HMAC-SHA512\n"
}

func portOf(listener net.Listener) string {
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	return port
}

// The zone is found by its SOA, sets are read and written by signed
// UPDATEs whose prerequisites hold them as read, and a wrong key is
// refused in the server's words.
func TestRFC2136UpdatesTheZoneWithSignedMessages(t *testing.T) {
	secret := base64.StdEncoding.EncodeToString([]byte("a shared secret of the operator's"))
	key := tsigKey{name: "jd-key", algorithm: tsigAlgorithms["HMAC-SHA512"].wire, secret: []byte("a shared secret of the operator's"), hash: tsigAlgorithms["HMAC-SHA512"].hash}
	fake := startDNSFake(t, "example.test", key)
	provider := mustProvider(t, "rfc2136", fake.credentials(secret), Options{})
	if provider.Name() != "RFC 2136 (127.0.0.1:"+portOf(fake.listener)+")" {
		t.Fatalf("name %q", provider.Name())
	}
	zones, err := provider.Zones(context.Background(), "a.b.example.test")
	if err != nil || len(zones) != 1 || zones[0].Name != "example.test" {
		t.Fatalf("zone discovery = %+v %v", zones, err)
	}
	if _, err := provider.Zones(context.Background(), "app.example.org"); err == nil || !strings.Contains(err.Error(), "answers for no zone holding app.example.org") {
		t.Fatalf("a name outside the server's zones = %v", err)
	}
	exercise(t, provider, "app.example.test", "example.test", "tunnel.example.test", false)
	if fake.updates != 4 {
		t.Fatalf("%d updates for four writes", fake.updates)
	}
	// A set written since it was read fails the prerequisite.
	zone := Zone{ID: "example.test", Name: "example.test"}
	err = provider.Write(context.Background(), zone, nil, Set{Name: "app.example.test", Type: TypeA, Values: []string{"203.0.113.9"}, TTL: 300})
	refusedInWords(t, err, "YXRRSET")

	wrong := mustProvider(t, "rfc2136", fake.credentials(base64.StdEncoding.EncodeToString([]byte("not the operator's secret"))), Options{})
	err = wrong.Write(context.Background(), zone, nil, Set{Name: "new.example.test", Type: TypeA, Values: []string{"203.0.113.9"}, TTL: 300})
	if err == nil || !strings.Contains(err.Error(), "NOTAUTH") {
		t.Fatalf("a wrong key = %v", err)
	}
	var refused *Error
	if errors.As(err, &refused) {
		t.Fatalf("an unverified answer was taken for the server's refusal: %v", err)
	}
}
