package netx

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeDNS answers on a local UDP socket, so the lookup race and the
// verification step run against real DNS packets. The
// handler decides, per question, what the "server" does: answer, fail, stay
// silent or take its time.
type dnsBehavior struct {
	rcode  int
	rdatas [][]byte
	delay  time.Duration
	drop   bool
}

func startFakeDNS(t *testing.T, handle func(name string, qtype uint16) dnsBehavior) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	t.Cleanup(func() {
		conn.Close()
		wg.Wait()
	})
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			req := append([]byte(nil), buf[:n]...)
			wg.Add(1)
			go func() {
				defer wg.Done()
				name, qtype, qend := parseQuestion(req)
				b := handle(name, qtype)
				if b.drop {
					return
				}
				time.Sleep(b.delay)
				conn.WriteTo(buildResponse(req, qend, qtype, b), from)
			}()
		}
	}()
	return conn.LocalAddr().String()
}

// parseQuestion reads the name and type of a one-question query, and where its
// question section ends.
func parseQuestion(req []byte) (string, uint16, int) {
	var labels []string
	i := 12
	for i < len(req) && req[i] != 0 {
		l := int(req[i])
		labels = append(labels, string(req[i+1:i+1+l]))
		i += l + 1
	}
	i++ // the root label
	qtype := binary.BigEndian.Uint16(req[i:])
	return strings.ToLower(strings.Join(labels, ".")), qtype, i + 4
}

func buildResponse(req []byte, qend int, qtype uint16, b dnsBehavior) []byte {
	resp := make([]byte, 12, 512)
	copy(resp, req[:2])
	binary.BigEndian.PutUint16(resp[2:], 0x8180|uint16(b.rcode))
	binary.BigEndian.PutUint16(resp[4:], 1)
	binary.BigEndian.PutUint16(resp[6:], uint16(len(b.rdatas)))
	resp = append(resp, req[12:qend]...)
	for _, rd := range b.rdatas {
		resp = append(resp, 0xC0, 0x0C)
		resp = binary.BigEndian.AppendUint16(resp, qtype)
		resp = binary.BigEndian.AppendUint16(resp, 1)
		resp = binary.BigEndian.AppendUint32(resp, 60)
		resp = binary.BigEndian.AppendUint16(resp, uint16(len(rd)))
		resp = append(resp, rd...)
	}
	return resp
}

const (
	typeA   = 1
	typeMX  = 15
	typeTXT = 16
)

func aData(ip string) []byte { return net.ParseIP(ip).To4() }

func txtData(s string) []byte { return append([]byte{byte(len(s))}, s...) }

func mxData(pref uint16, host string) []byte {
	out := binary.BigEndian.AppendUint16(nil, pref)
	for _, l := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		out = append(out, byte(len(l)))
		out = append(out, l...)
	}
	return append(out, 0)
}

// answerA answers A questions with ip and everything else with "no data".
func answerA(ip string) func(string, uint16) dnsBehavior {
	return func(_ string, qtype uint16) dnsBehavior {
		if qtype == typeA {
			return dnsBehavior{rdatas: [][]byte{aData(ip)}}
		}
		return dnsBehavior{}
	}
}

// routeDNS sends queries for each "server" to its fake and fails the dial for
// any server it was not told about.
func routeDNS(t *testing.T, servers map[string]string) {
	t.Helper()
	prev := dnsDial
	dnsDial = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, _ := net.SplitHostPort(address)
		local, ok := servers[host]
		if !ok {
			return nil, errors.New("no route to host")
		}
		if strings.HasPrefix(network, "tcp") {
			return nil, errors.New("the fake answers over UDP only")
		}
		var d net.Dialer
		return d.DialContext(ctx, "udp", local)
	}
	prevTimeout := lookupTimeout
	t.Cleanup(func() { dnsDial, lookupTimeout = prev, prevTimeout })
}

func TestCleanLookupName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, rtype string
		want        string
		wantErr     bool
	}{
		{"example.com", "A", "example.com", false},
		{" Example.COM. ", "MX", "Example.COM", false},
		{"_sip._tcp.example.com", "SRV", "_sip._tcp.example.com", false},
		{"_dmarc.example.com", "TXT", "_dmarc.example.com", false},
		{"_sip._tcp.example.com", "A", "", true},
		{"192.0.2.7", "PTR", "192.0.2.7", false},
		{"2001:db8::7", "PTR", "2001:db8::7", false},
		{"::ffff:192.0.2.7", "PTR", "192.0.2.7", false},
		{"example.com", "PTR", "", true},
		{"192.0.2.7", "A", "", true},
		{"", "A", "", true},
		{"exa mple.com", "A", "", true},
		{"example.com;ls", "A", "", true},
		{"-bad.example.com", "A", "", true},
		{"ex..ample.com", "A", "", true},
		{strings.Repeat("a", 64) + ".com", "A", "", true},
		{strings.Repeat("a.", 130) + "com", "A", "", true},
		{"bücher.example", "A", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name+"/"+tc.rtype, func(t *testing.T) {
			t.Parallel()
			got, err := cleanLookupName(tc.name, tc.rtype)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("cleanLookupName(%q, %s) = %q, %v", tc.name, tc.rtype, got, err)
			}
		})
	}
}

// One resolver answers, one is slow, one fails, one is silent and the rest
// cannot be reached at all. The race reports each as what it did, side by
// side, and the slow one's time shows.
func TestLookupRacesEveryResolver(t *testing.T) {
	rec := record(t)
	rec.on("systemctl is-active systemd-resolved", "active\n").
		on("resolvectl status --no-pager", fixture(t, "dns-resolvectl-status.txt"))
	s := testService(t)
	pointResolvConf(t, "stub")

	fast := startFakeDNS(t, answerA("192.0.2.1"))
	slow := startFakeDNS(t, func(name string, qtype uint16) dnsBehavior {
		b := answerA("192.0.2.2")(name, qtype)
		b.delay = 250 * time.Millisecond
		return b
	})
	failing := startFakeDNS(t, func(string, uint16) dnsBehavior { return dnsBehavior{rcode: 2} })
	silent := startFakeDNS(t, func(string, uint16) dnsBehavior { return dnsBehavior{drop: true} })
	routeDNS(t, map[string]string{
		"127.0.0.53":   fast,
		"203.0.113.53": slow,
		"100.64.0.53":  failing,
		"1.1.1.1":      silent,
	})
	lookupTimeout = 600 * time.Millisecond

	res, err := s.LookupWithOptions(context.Background(), "Example.com", "a", LookupOptions{Mode: "compare", AcknowledgeDisclosure: true, Destinations: []string{"127.0.0.53", "203.0.113.53", "100.64.0.53", "fd7a:115c:a1e0::53", "1.1.1.1", "1.1.1.2", "9.9.9.9", "8.8.8.8", "94.140.14.14", "194.242.2.3"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Name != "Example.com" || res.Type != "A" {
		t.Fatalf("result header = %q %q", res.Name, res.Type)
	}
	by := map[string]LookupAnswer{}
	for _, a := range res.Answers {
		by[a.Server] = a
	}
	if a := by["127.0.0.53"]; a.Label != "systemd-resolved stub" || len(a.Answers) != 1 || a.Answers[0] != "192.0.2.1" || a.Error != "" {
		t.Fatalf("stub = %+v", a)
	}
	if a := by["203.0.113.53"]; a.Label != "ens3" || len(a.Answers) != 1 || a.Answers[0] != "192.0.2.2" || a.LatencyMS < 250 {
		t.Fatalf("slow link server = %+v", a)
	}
	if a := by["100.64.0.53"]; a.Error == "" || len(a.Answers) != 0 || a.Answers == nil {
		t.Fatalf("failing server = %+v", a)
	}
	if a := by["1.1.1.1"]; a.Label != "Cloudflare" || a.Error == "" || a.LatencyMS < 500 {
		t.Fatalf("silent preset = %+v", a)
	}
	if a := by["9.9.9.9"]; a.Label != "Quad9" || a.Error == "" {
		t.Fatalf("unreachable preset = %+v", a)
	}
	// 1 stub + 3 link servers + 6 presets' first IPv4 addresses.
	if len(res.Answers) != 10 {
		t.Fatalf("%d resolvers asked: %+v", len(res.Answers), res.Answers)
	}
	for _, label := range []string{"1.1.1.2", "94.140.14.14", "194.242.2.3", "8.8.8.8"} {
		if _, ok := by[label]; !ok {
			t.Errorf("preset server %s was not asked", label)
		}
	}
}

func TestLookupRecordTypes(t *testing.T) {
	rec := record(t)
	rec.on("systemctl is-active systemd-resolved", "inactive\n")
	s := testService(t)
	pointResolvConf(t, "static")
	srv := startFakeDNS(t, func(name string, qtype uint16) dnsBehavior {
		switch qtype {
		case typeMX:
			return dnsBehavior{rdatas: [][]byte{mxData(20, "mail2.example.com"), mxData(10, "mail.example.com")}}
		case typeTXT:
			return dnsBehavior{rdatas: [][]byte{txtData("v=spf1 -all")}}
		}
		return dnsBehavior{}
	})
	routeDNS(t, map[string]string{"198.51.100.53": srv})
	lookupTimeout = 500 * time.Millisecond

	targets, _ := s.lookupDestinationInventory(s.readResolved(context.Background(), false))
	if targets[0].Server != "198.51.100.53" || targets[0].Label != "resolv.conf" {
		t.Fatalf("without resolved the resolv.conf servers are asked: %+v", targets[0])
	}

	mx, err := lookupVia(context.Background(), "198.51.100.53", "example.com", "MX")
	if err != nil || len(mx) != 2 || mx[0] != "10 mail.example.com." {
		t.Fatalf("MX = %v, %v", mx, err)
	}
	txt, err := lookupVia(context.Background(), "198.51.100.53", "example.com", "TXT")
	if err != nil || len(txt) != 1 || txt[0] != "v=spf1 -all" {
		t.Fatalf("TXT = %v, %v", txt, err)
	}
	if _, err := s.Lookup(context.Background(), "example.com", "ANY"); err == nil {
		t.Fatal("a record type outside the list was accepted")
	}
	if _, err := s.Lookup(context.Background(), "bad name", "A"); err == nil {
		t.Fatal("a name with a space was accepted")
	}
}

func TestLookupErrorReadsAnAbsentNameAsAnAnswer(t *testing.T) {
	t.Parallel()
	err := &net.DNSError{Err: "no such host", Name: "x", IsNotFound: true}
	if got := lookupError(fmt.Errorf("wrapped: %w", err)); got != "no such record" {
		t.Fatalf("got %q", got)
	}
}

func TestLookupUsesConfiguredPort(t *testing.T) {
	server := startFakeDNS(t, answerA("192.0.2.7"))
	answers, err := lookupVia(context.Background(), server, "example.com", "A")
	if err != nil || len(answers) != 1 || answers[0] != "192.0.2.7" {
		t.Fatalf("lookup at custom port = %v, %v", answers, err)
	}
}

func TestLookupDoesNotSendPrivateNamesToPublicPresetsByDefault(t *testing.T) {
	rec := record(t)
	rec.on("systemctl is-active systemd-resolved", "inactive\n")
	pointResolvConf(t, "static")
	server := startFakeDNS(t, answerA("192.0.2.7"))
	routeDNS(t, map[string]string{"198.51.100.53": server})
	res, err := testService(t).Lookup(context.Background(), "nas.home.arpa", "A")
	if err != nil || len(res.Answers) != 1 || res.Answers[0].Server != "198.51.100.53" || len(res.Answers[0].Answers) != 1 {
		t.Fatalf("private name lookup = %+v, %v", res, err)
	}
	for _, a := range res.Answers {
		if a.Label != "resolv.conf effective policy" {
			t.Errorf("private name sent outside configured resolvers: %+v", a)
		}
	}
}

func TestLookupRetainsLinkLocalScopeAndDistinctPorts(t *testing.T) {
	rec := record(t)
	rec.on("systemctl is-active systemd-resolved", "active\n").on("resolvectl status --no-pager", `Global
 DNS Servers: 192.0.2.53:5353 192.0.2.53:1053
Link 2 (eth0)
 DNS Servers: fe80::53
Link 3 (eth1)
 DNS Servers: fe80::53
`)
	service := testService(t)
	targets, _ := service.lookupDestinationInventory(service.readResolved(context.Background(), false))
	byServer := map[string]string{}
	for _, target := range targets {
		byServer[target.Server] = target.Label
	}
	for server, label := range map[string]string{"192.0.2.53:5353": "global upstream", "192.0.2.53:1053": "global upstream", "fe80::53%eth0": "eth0", "fe80::53%eth1": "eth1"} {
		if byServer[server] != label {
			t.Errorf("%s label = %q, want %q", server, byServer[server], label)
		}
	}
}

func TestEffectiveLookupDelegatesSplitDNSOnlyToNativeStub(t *testing.T) {
	rec := record(t)
	rec.on("systemctl is-active systemd-resolved", "active\n").on("resolvectl status --no-pager", `Global
 DNS Servers: 1.1.1.1
 Fallback DNS Servers: 8.8.8.8
 DNS Domain: ~.
Link 2 (eth0)
 DNS Servers: 9.9.9.9
 DNS Domain: ~.
 DefaultRoute: yes
Link 3 (vpn0)
 DNS Servers: 10.8.0.53
 DNS Domain: ~corp.example ~home.arpa
 DefaultRoute: no
Link 4 (vpn1)
 DNS Servers: 10.9.0.53
 DNS Domain: ~lab.corp.example
 DefaultRoute: no
`)
	stub := startFakeDNS(t, answerA("10.9.0.7"))
	original := dnsDial
	var destinations []string
	dnsDial = func(ctx context.Context, network, address string) (net.Conn, error) {
		destinations = append(destinations, address)
		if address != "127.0.0.53:53" {
			return nil, fmt.Errorf("private name leaked to %s", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, stub)
	}
	t.Cleanup(func() { dnsDial = original })
	for _, name := range []string{"db.lab.corp.example", "nas.home.arpa", "lab.corp.example"} {
		result, err := testService(t).Lookup(context.Background(), name, "A")
		if err != nil || len(result.Answers) != 1 || result.Answers[0].Error != "" || result.Mode != "effective" {
			t.Fatalf("effective lookup=%+v,%v", result, err)
		}
		link := "vpn1"
		if name == "nas.home.arpa" {
			link = "vpn0"
		}
		if !strings.Contains(result.Route, link) || strings.Contains(result.Route, "global (") {
			t.Fatalf("incorrect longest-suffix evidence: %s", result.Route)
		}
	}
	if len(destinations) != 3 {
		t.Fatalf("effective lookup dialed %v", destinations)
	}
}

func TestEffectiveLookupDoesNotBypassUnavailableNativeStub(t *testing.T) {
	rec := record(t)
	rec.on("systemctl is-active systemd-resolved", "active").fail("resolvectl status --no-pager", "scope evidence unavailable")
	previous := dnsDial
	calls := []string{}
	dnsDial = func(ctx context.Context, network, address string) (net.Conn, error) {
		calls = append(calls, address)
		return nil, errors.New("stub disabled")
	}
	t.Cleanup(func() { dnsDial = previous })
	result, err := testService(t).Lookup(context.Background(), "secret.corp.example", "A")
	if err != nil || len(result.Answers) != 1 || result.Answers[0].Error != "stub disabled" || !strings.Contains(result.Route, "scope evidence unavailable") {
		t.Fatalf("lookup=%+v,%v", result, err)
	}
	if len(calls) != 1 || calls[0] != "127.0.0.53:53" {
		t.Fatalf("native policy bypassed: %v", calls)
	}
}

func TestLookupComparisonRequiresNamedDestinationsAndDisclosure(t *testing.T) {
	rec := record(t)
	rec.on("systemctl is-active systemd-resolved", "inactive")
	pointResolvConf(t, "static")
	for _, opts := range []LookupOptions{
		{Mode: "compare"},
		{Mode: "compare", Destinations: []string{"1.1.1.1"}},
		{Mode: "compare", AcknowledgeDisclosure: true},
		{Mode: "compare", Destinations: []string{"127.0.0.1:9999"}, AcknowledgeDisclosure: true},
		{Mode: "effective", Destinations: []string{"1.1.1.1"}, AcknowledgeDisclosure: true},
		{Mode: "invalid"},
		{Mode: "compare", Destinations: strings.Fields(strings.Repeat("1.1.1.1 ", maxLookupResolvers+1)), AcknowledgeDisclosure: true},
	} {
		if _, err := testService(t).LookupWithOptions(context.Background(), "private.corp.example", "A", opts); err == nil {
			t.Fatalf("accepted disclosure request %+v", opts)
		}
	}
	if _, err := testService(t).Lookup(context.Background(), "private.corp.example", "A", true); err == nil {
		t.Fatal("legacy fan-out flag accepted")
	}
}

func TestLookupComparisonContactsOnlyNamedDestination(t *testing.T) {
	rec := record(t)
	rec.on("systemctl is-active systemd-resolved", "inactive")
	pointResolvConf(t, "static")
	upstream := startFakeDNS(t, answerA("192.0.2.77"))
	routeDNS(t, map[string]string{"1.1.1.1": upstream})
	result, err := testService(t).LookupWithOptions(context.Background(), "nas.home.arpa", "A", LookupOptions{Mode: "compare", Destinations: []string{"1.1.1.1", "1.1.1.1"}, AcknowledgeDisclosure: true})
	if err != nil || len(result.Answers) != 1 || result.Answers[0].Error != "" || result.Answers[0].Server != "1.1.1.1" || !strings.Contains(result.Note, "Private names") {
		t.Fatalf("named comparison=%+v,%v", result, err)
	}
}
