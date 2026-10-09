package netsec

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

type fakeAnswer struct {
	rcode         dnsmessage.RCode
	authoritative bool
	truncateUDP   bool
	answer        []dnsmessage.Resource
}

// fakeDNSServer answers on loopback UDP and TCP at one port, so the wire
// client is exercised end to end without leaving the machine.
func fakeDNSServer(t *testing.T, answer func(q dnsmessage.Question, recursion bool) fakeAnswer) string {
	t.Helper()
	var udp net.PacketConn
	var tcp net.Listener
	for attempt := 0; attempt < 20; attempt++ {
		var err error
		udp, err = net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		tcp, err = net.Listen("tcp", udp.LocalAddr().String())
		if err == nil {
			break
		}
		udp.Close()
		tcp = nil
	}
	if tcp == nil {
		t.Fatal("no free UDP/TCP port pair")
	}
	t.Cleanup(func() { udp.Close(); tcp.Close() })
	respond := func(packet []byte, overUDP bool) []byte {
		var p dnsmessage.Parser
		h, err := p.Start(packet)
		if err != nil {
			return nil
		}
		q, err := p.Question()
		if err != nil {
			return nil
		}
		a := answer(q, h.RecursionDesired)
		b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: h.ID, Response: true, Authoritative: a.authoritative, RCode: a.rcode, Truncated: overUDP && a.truncateUDP, RecursionDesired: h.RecursionDesired})
		_ = b.StartQuestions()
		_ = b.Question(q)
		_ = b.StartAnswers()
		if !(overUDP && a.truncateUDP) {
			for _, rr := range a.answer {
				switch body := rr.Body.(type) {
				case *dnsmessage.AResource:
					_ = b.AResource(rr.Header, *body)
				case *dnsmessage.AAAAResource:
					_ = b.AAAAResource(rr.Header, *body)
				case *dnsmessage.NSResource:
					_ = b.NSResource(rr.Header, *body)
				case *dnsmessage.SOAResource:
					_ = b.SOAResource(rr.Header, *body)
				case *dnsmessage.MXResource:
					_ = b.MXResource(rr.Header, *body)
				}
			}
		}
		out, _ := b.Finish()
		return out
	}
	go func() {
		buf := make([]byte, 2048)
		for {
			n, addr, err := udp.ReadFrom(buf)
			if err != nil {
				return
			}
			if out := respond(buf[:n], true); out != nil {
				_, _ = udp.WriteTo(out, addr)
			}
		}
	}()
	go func() {
		for {
			conn, err := tcp.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				var size [2]byte
				if _, err := io.ReadFull(conn, size[:]); err != nil {
					return
				}
				packet := make([]byte, binary.BigEndian.Uint16(size[:]))
				if _, err := io.ReadFull(conn, packet); err != nil {
					return
				}
				out := respond(packet, false)
				frame := make([]byte, 2+len(out))
				binary.BigEndian.PutUint16(frame, uint16(len(out)))
				copy(frame[2:], out)
				_, _ = conn.Write(frame)
			}()
		}
	}()
	return udp.LocalAddr().String()
}

func aRecord(name, addr string, ttl uint32) dnsmessage.Resource {
	a := netip.MustParseAddr(addr)
	return dnsmessage.Resource{Header: dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName(name), Class: dnsmessage.ClassINET, TTL: ttl}, Body: &dnsmessage.AResource{A: a.As4()}}
}

func TestDNSWireReadsEverySectionAndRetriesTruncationOverTCP(t *testing.T) {
	server := fakeDNSServer(t, func(q dnsmessage.Question, rd bool) fakeAnswer {
		if rd {
			t.Errorf("an authority question was sent with recursion desired")
		}
		return fakeAnswer{authoritative: true, truncateUDP: true, answer: []dnsmessage.Resource{aRecord("app.example.test.", "192.0.2.10", 300)}}
	})
	reply, err := exchangeDNSWire(t.Context(), server, "app.example.test", "A", false)
	if err != nil {
		t.Fatal(err)
	}
	if reply.RCode != "NOERROR" || !reply.Authoritative || reply.Transport != "TCP after a truncated UDP answer" || !reflect.DeepEqual(rrValues(reply.Answer, "A"), []string{"192.0.2.10"}) || reply.Answer[0].TTL != 300 {
		t.Fatalf("reply = %+v", reply)
	}
	if got := reverseName(netip.MustParseAddr("192.0.2.1")); got != "1.2.0.192.in-addr.arpa" {
		t.Fatalf("reverse = %s", got)
	}
	if got := reverseName(netip.MustParseAddr("2001:db8::1")); !strings.HasPrefix(got, "1.0.0.0.") || !strings.HasSuffix(got, ".8.b.d.0.1.0.0.2.ip6.arpa") {
		t.Fatalf("reverse6 = %s", got)
	}
}

func writeResolverFiles(t *testing.T, hosts, resolv, nsswitch string) {
	t.Helper()
	dir := t.TempDir()
	old := resolverFiles
	t.Cleanup(func() { resolverFiles = old })
	resolverFiles.Hosts, resolverFiles.Resolv, resolverFiles.NSSwitch = filepath.Join(dir, "hosts"), filepath.Join(dir, "resolv.conf"), filepath.Join(dir, "nsswitch.conf")
	for path, body := range map[string]string{resolverFiles.Hosts: hosts, resolverFiles.Resolv: resolv, resolverFiles.NSSwitch: nsswitch} {
		if body == "" {
			continue
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestResolverConfigurationReading(t *testing.T) {
	writeResolverFiles(t, "127.0.0.1 localhost\n192.0.2.7 app.example.test app # pinned\n2001:db8::7 app.example.test\n", "# managed\nnameserver 127.0.0.53\nnameserver 192.0.2.53\nsearch corp.test lab.test\noptions edns0 trust-ad\n", "passwd: files\nhosts: files mdns4_minimal [NOTFOUND=return] dns\n")
	rc, err := readResolvConf(resolverFiles.Resolv)
	if err != nil || !reflect.DeepEqual(rc.Nameservers, []string{"127.0.0.53", "192.0.2.53"}) || !reflect.DeepEqual(rc.Search, []string{"corp.test", "lab.test"}) || len(rc.Options) != 2 {
		t.Fatalf("resolv = %+v %v", rc, err)
	}
	if nss, ok := readNSSHosts(resolverFiles.NSSwitch); !ok || nss != "files mdns4_minimal [NOTFOUND=return] dns" {
		t.Fatalf("nss = %q %v", nss, ok)
	}
	if got, _ := hostsFileMatches(resolverFiles.Hosts, "APP.example.test.", "A"); !reflect.DeepEqual(got, []string{"192.0.2.7"}) {
		t.Fatalf("A = %v", got)
	}
	if got, _ := hostsFileMatches(resolverFiles.Hosts, "app.example.test", "AAAA"); !reflect.DeepEqual(got, []string{"2001:db8::7"}) {
		t.Fatalf("AAAA = %v", got)
	}
	if got, _ := hostsFileMatches(resolverFiles.Hosts, "192.0.2.7", "PTR"); !reflect.DeepEqual(got, []string{"app.example.test"}) {
		t.Fatalf("PTR = %v", got)
	}
	got := attributeAnswers([]string{"192.0.2.7", "192.0.2.8", "192.0.2.9"}, []string{"192.0.2.7"}, []string{"192.0.2.8"})
	want := []answerSource{{"192.0.2.7", "hosts file (NSS files)"}, {"192.0.2.8", "DNS wire answer"}, {"192.0.2.9", "not matched in the hosts file or the direct DNS answers"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("attribution = %+v", got)
	}
}

func TestLookupReportsResolverProvenanceAndWireAnswers(t *testing.T) {
	server := fakeDNSServer(t, func(q dnsmessage.Question, rd bool) fakeAnswer {
		if !rd || q.Name.String() != "app.example.test." {
			t.Errorf("question = %v rd=%v", q, rd)
		}
		return fakeAnswer{answer: []dnsmessage.Resource{aRecord("app.example.test.", "192.0.2.10", 60)}}
	})
	host, port, _ := net.SplitHostPort(server)
	writeResolverFiles(t, "192.0.2.7 pinned.example.test\n", "nameserver "+host+"\nsearch corp.test\n", "hosts: files dns\n")
	oldPort, oldResolver := dnsPort, lookupResolver
	t.Cleanup(func() { dnsPort, lookupResolver = oldPort, oldResolver })
	dnsPort = port
	lookupResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, server)
	}}
	res, err := New().Lookup(t.Context(), "app.example.test.", "A")
	if err != nil || !res.OK || res.Verdict != ProbeOK || !reflect.DeepEqual(res.Records, []string{"192.0.2.10"}) {
		t.Fatalf("%+v %v", res, err)
	}
	if factValue(res, "Name service order") != "files dns" || factValue(res, "Nameservers") != host || !strings.Contains(factValue(res, "Search domains"), "not applied") {
		t.Fatalf("facts = %+v", res.Facts)
	}
	sources := tableByID(res, "sources")
	if sources == nil || len(sources.Rows) != 2 || sources.Rows[0][2] != "no entry" || sources.Rows[1][0] != "nameserver "+host || sources.Rows[1][1] != "UDP" || sources.Rows[1][3] != "192.0.2.10" || sources.Rows[1][4] != "60" {
		t.Fatalf("sources = %+v", sources)
	}
	attribution := tableByID(res, "attribution")
	if attribution == nil || attribution.Rows[0][1] != "DNS wire answer" {
		t.Fatalf("attribution = %+v", attribution)
	}
	if !hasLink(res, "/network/dns") {
		t.Fatal("a loopback nameserver does not point at the stub's upstream configuration")
	}
}

type authorityZone struct {
	ns     map[string][]string
	ips    map[string][]string
	answer map[string]func(name, rtype string) (*dnsReply, error)
}

func (z authorityZone) deps() authorityDeps {
	return authorityDeps{
		lookupNS: func(_ context.Context, name string) ([]string, error) {
			if hosts, ok := z.ns[name]; ok {
				return hosts, nil
			}
			return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
		},
		lookupIP: func(_ context.Context, host string) ([]netip.Addr, error) {
			var out []netip.Addr
			for _, a := range z.ips[host] {
				out = append(out, netip.MustParseAddr(a))
			}
			if len(out) == 0 {
				return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
			}
			return out, nil
		},
		exchange: func(_ context.Context, server, name, rtype string, recursion bool) (*dnsReply, error) {
			if recursion {
				return nil, errors.New("authority checks must not ask for recursion")
			}
			host, _, _ := net.SplitHostPort(server)
			if f, ok := z.answer[host]; ok {
				return f(name, rtype)
			}
			return nil, errors.New("i/o timeout")
		},
	}
}

func authoritative(serial uint32, ns ...string) func(string, string) (*dnsReply, error) {
	return func(name, rtype string) (*dnsReply, error) {
		r := &dnsReply{RCode: "NOERROR", Authoritative: true}
		if rtype == "SOA" {
			r.Answer = []dnsRR{{Name: name, Type: "SOA", Serial: serial}}
		}
		for _, n := range ns {
			r.Answer = append(r.Answer, dnsRR{Name: name, Type: "NS", Value: n})
		}
		if rtype == "SOA" {
			r.Answer = r.Answer[:1]
		}
		return r, nil
	}
}

func referral(ns []string, glue map[string]string) func(string, string) (*dnsReply, error) {
	return func(name, rtype string) (*dnsReply, error) {
		r := &dnsReply{RCode: "NOERROR"}
		for _, n := range ns {
			r.Authority = append(r.Authority, dnsRR{Name: name, Type: "NS", Value: n})
		}
		for host, addr := range glue {
			r.Additional = append(r.Additional, dnsRR{Name: host, Type: "A", Value: addr})
		}
		return r, nil
	}
}

func healthyZone() authorityZone {
	return authorityZone{
		ns:  map[string][]string{"example.test": {"ns1.example.test", "ns2.dns.test"}, "test": {"a.tld.test"}},
		ips: map[string][]string{"ns1.example.test": {"192.0.2.1"}, "ns2.dns.test": {"198.51.100.2"}, "a.tld.test": {"203.0.113.1"}},
		answer: map[string]func(string, string) (*dnsReply, error){
			"203.0.113.1":  referral([]string{"ns1.example.test", "ns2.dns.test"}, map[string]string{"ns1.example.test": "192.0.2.1"}),
			"192.0.2.1":    authoritative(2026100901, "ns1.example.test", "ns2.dns.test"),
			"198.51.100.2": authoritative(2026100901, "ns1.example.test", "ns2.dns.test"),
		},
	}
}

func TestDNSAuthorityChecksDelegationGlueAndConsistency(t *testing.T) {
	res := &ProbeResult{}
	checkAuthority(t.Context(), res, "www.example.test", healthyZone().deps())
	if !res.OK || res.Verdict != ProbeOK || factValue(res, "Zone") != "example.test" || len(res.Findings) != 0 {
		t.Fatalf("healthy = %+v", res)
	}
	delegation := tableByID(res, "delegation")
	if delegation == nil || delegation.Rows[0][3] != "present" || delegation.Rows[1][3] != "not needed (out of zone)" {
		t.Fatalf("delegation = %+v", delegation)
	}
	if v, _ := metricValue(res, "serials"); v != 1 {
		t.Fatalf("serials = %v", v)
	}

	for name, mutate := range map[string]func(z *authorityZone) string{
		"missing glue": func(z *authorityZone) string {
			z.answer["203.0.113.1"] = referral([]string{"ns1.example.test", "ns2.dns.test"}, nil)
			return "glue-missing-ns1.example.test"
		},
		"glue mismatch": func(z *authorityZone) string {
			z.answer["203.0.113.1"] = referral([]string{"ns1.example.test", "ns2.dns.test"}, map[string]string{"ns1.example.test": "192.0.2.99"})
			return "glue-mismatch-ns1.example.test"
		},
		"serial mismatch": func(z *authorityZone) string {
			z.answer["198.51.100.2"] = authoritative(2026100800, "ns1.example.test", "ns2.dns.test")
			return "serial-mismatch"
		},
		"lame server": func(z *authorityZone) string {
			z.answer["198.51.100.2"] = func(string, string) (*dnsReply, error) { return &dnsReply{RCode: "REFUSED"}, nil }
			return "ns-lame-198.51.100.2"
		},
		"silent server": func(z *authorityZone) string {
			delete(z.answer, "198.51.100.2")
			return "ns-silent-198.51.100.2"
		},
		"delegation differs": func(z *authorityZone) string {
			z.answer["203.0.113.1"] = referral([]string{"ns1.example.test", "old.dns.test"}, map[string]string{"ns1.example.test": "192.0.2.1"})
			return "delegation-mismatch"
		},
		"server lists another NS set": func(z *authorityZone) string {
			z.answer["192.0.2.1"] = authoritative(2026100901, "ns1.example.test")
			return "ns-set-192.0.2.1"
		},
	} {
		z := healthyZone()
		want := mutate(&z)
		res := &ProbeResult{}
		checkAuthority(t.Context(), res, "example.test", z.deps())
		if res.Verdict != ProbeFindings || !hasFinding(res, want) {
			t.Errorf("%s: verdict=%s findings=%+v", name, res.Verdict, res.Findings)
		}
	}

	res = &ProbeResult{}
	checkAuthority(t.Context(), res, "nowhere.invalid", authorityZone{}.deps())
	if res.OK || res.Verdict != ProbeFailed {
		t.Fatalf("no zone = %+v", res)
	}
	z := healthyZone()
	delete(z.ns, "test")
	res = &ProbeResult{}
	checkAuthority(t.Context(), res, "example.test", z.deps())
	if res.Verdict != ProbeUnknown || !strings.Contains(factValue(res, "Delegation"), "could not be found") {
		t.Fatalf("unknown parent = %+v", res)
	}
	if _, err := New().DNSAuthority(t.Context(), "192.0.2.1"); err == nil {
		t.Fatal("an address was accepted as a zone")
	}
}

func TestDNSAuthorityLiveWireAgainstLoopbackAuthority(t *testing.T) {
	server := fakeDNSServer(t, func(q dnsmessage.Question, rd bool) fakeAnswer {
		switch q.Type {
		case dnsmessage.TypeSOA:
			return fakeAnswer{authoritative: true, answer: []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: 300},
				Body: &dnsmessage.SOAResource{NS: dnsmessage.MustNewName("ns1.example.test."), MBox: dnsmessage.MustNewName("hostmaster.example.test."), Serial: 7}}}}
		case dnsmessage.TypeNS:
			return fakeAnswer{authoritative: true, answer: []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: 300},
				Body: &dnsmessage.NSResource{NS: dnsmessage.MustNewName("ns1.example.test.")}}}}
		}
		return fakeAnswer{rcode: dnsmessage.RCodeRefused}
	})
	_, port, _ := net.SplitHostPort(server)
	oldPort := dnsPort
	t.Cleanup(func() { dnsPort = oldPort })
	dnsPort = port
	deps := authorityDeps{
		lookupNS: func(_ context.Context, name string) ([]string, error) {
			if name == "example.test" {
				return []string{"ns1.example.test"}, nil
			}
			return nil, &net.DNSError{IsNotFound: true}
		},
		lookupIP: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		},
		exchange: exchangeDNSWire,
	}
	res := &ProbeResult{}
	checkAuthority(t.Context(), res, "example.test", deps)
	auth := tableByID(res, "authorities")
	if auth == nil || len(auth.Rows) != 1 || auth.Rows[0][2] != "yes" || auth.Rows[0][3] != strconv.Itoa(7) || auth.Rows[0][4] != "matches" {
		t.Fatalf("authorities = %+v (%+v)", auth, res)
	}
	if !hasFinding(res, "single-ns") {
		t.Fatal("a single nameserver drew no notice")
	}
}
