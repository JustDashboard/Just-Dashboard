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
// verification step run through Go's real resolver against real packets. The
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

	res, err := s.Lookup(context.Background(), "Example.com", "a")
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

	targets := s.lookupTargets(context.Background())
	if targets[0].server != "198.51.100.53" || targets[0].label != "resolv.conf" {
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
