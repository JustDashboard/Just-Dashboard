package netx

import (
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gwGolden compares a rendering with its file in testdata. JD_UPDATE_GOLDEN=1
// rewrites the file, for when the change in the diff is the one meant.
func gwGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if os.Getenv("JD_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s differs from the rendering:\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

// gwFullSpec holds one of every kind of gateway entry, in the addresses RFC
// 5737 and RFC 3849 set aside for documentation.
func gwFullSpec() (*Spec, []netip.Prefix, map[int][]netip.Prefix) {
	made := Made{CreatedAt: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC), CreatedBy: "admin"}
	sp := emptySpec()
	sp.Forwards = []ForwardSpec{
		{ID: 1, Name: "web", Protocol: "tcp", Interface: "eth0", Ports: "8080", Target: "10.0.0.5", TargetPort: "80",
			SourceNAT: "auto:always", Sources: []string{"198.51.100.0/24", "203.0.113.7/32"}, Enabled: true, Made: made},
		{ID: 2, Name: "game", Protocol: "both", Ports: "27015-27020", Target: "172.17.0.2", SourceNAT: "never", Enabled: true, Made: made},
		{ID: 3, Name: "web over v6", Protocol: "tcp", Ports: "443", Target: "2001:db8::5", TargetPort: "8443",
			SourceNAT: "always", Enabled: true, Made: made},
		{ID: 4, Name: "paused", Protocol: "udp", Ports: "5353", Target: "10.0.0.9", SourceNAT: "auto:never", Enabled: false, Made: made},
	}
	sp.NAT = []NATSpec{
		{ID: 5, Name: "wg0 clients", Source: "10.8.0.0/24", Interface: "eth0", Owner: "wireguard:wg0", Enabled: true, Made: made},
		{ID: 6, Name: "lab", Source: "10.9.0.0/24", Interface: "eth0", ToAddress: "203.0.113.20", Enabled: true, Made: made},
		{ID: 7, Name: "lab v6", Source: "fd00:9::/64", Interface: "eth0", Enabled: true, Made: made},
	}
	sp.Limits = []LimitSpec{
		{ID: 8, Name: "ssh", Protocol: "tcp", Ports: "22", Rate: 10, Per: "minute", Burst: 5, Action: "reject", Enabled: true, Made: made},
		{ID: 9, Name: "app", Protocol: "tcp", Ports: "8000-8010", Rate: 10, Per: "second", Burst: 20, PerSource: true,
			MaxConnections: 50, Action: "drop", Enabled: true, Made: made},
		{ID: 10, Name: "dns", Protocol: "both", Ports: "53", Rate: 100, Per: "second", Action: "reject", Enabled: true, Made: made},
		{ID: 11, Name: "off", Protocol: "tcp", Ports: "80", MaxConnections: 5, Action: "drop", Enabled: false, Made: made},
	}
	sp.Blocklists = []BlocklistSpec{
		{ID: 12, Name: "scanners", Kind: "manual", Entries: []string{"203.0.113.0/24", "198.51.100.7/32", "2001:db8:bad::/48"}, Enabled: true, Count: 3, Made: made},
		{ID: 13, Name: "countries", Kind: "country", Countries: []string{"xx", "yy"}, Enabled: true, Count: 4, Made: made},
		{ID: 14, Name: "feed", Kind: "feed", URL: "https://feed.example/list.txt", Enabled: false, Made: made},
	}
	trusted := []netip.Prefix{
		netip.MustParsePrefix("100.64.0.0/10"),
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("203.0.113.9/32"),
		netip.MustParsePrefix("::1/128"),
	}
	lists := map[int][]netip.Prefix{
		13: {
			netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
			netip.MustParsePrefix("2001:db8:1::/48"), netip.MustParsePrefix("192.0.2.128/25"),
		},
	}
	return sp, mergePrefixes(trusted), lists
}

func gwRenderFull(t *testing.T) string {
	t.Helper()
	sp, trusted, lists := gwFullSpec()
	out, err := renderGatewayWith(sp, trusted, func(bl BlocklistSpec) []netip.Prefix {
		if bl.Kind == "manual" {
			return blocklistEntries("", bl)
		}
		return mergePrefixes(lists[bl.ID])
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRenderGatewayGolden(t *testing.T) {
	gwGolden(t, "gateway-full.nft", gwRenderFull(t))
}

func TestRenderGatewayIsDeterministic(t *testing.T) {
	if a, b := gwRenderFull(t), gwRenderFull(t); a != b {
		t.Fatal("the same spec rendered two different files")
	}
}

func TestRenderGatewayOfAnEmptySpecHooksNothing(t *testing.T) {
	out, err := renderGateway(emptySpec(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, hook := range []string{"chain ", "hook "} {
		if strings.Contains(out, hook) {
			t.Errorf("an idle table must not hook the packet path, found %q in:\n%s", hook, out)
		}
	}
	for _, want := range []string{"table inet jd_gateway\ndelete table inet jd_gateway\n", "set trusted4", "set trusted6"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestRenderGatewayDisabledEntriesRenderNothing(t *testing.T) {
	out := gwRenderFull(t)
	for _, absent := range []string{"forward:4", "limit:11", "bl_14_4", "5353"} {
		if strings.Contains(out, absent) {
			t.Errorf("a disabled entry reached the ruleset: %q", absent)
		}
	}
}

func TestRenderGatewayEveryRuleCanBeReadBackByComment(t *testing.T) {
	out := gwRenderFull(t)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		isRule := strings.Contains(line, "counter") || strings.Contains(line, "ct mark set")
		if isRule && !strings.Contains(line, "comment \"") {
			t.Errorf("a rule with no comment cannot be matched to its entry: %s", line)
		}
	}
}

func TestRenderGatewayTrustedReturnsPrecedeEveryDrop(t *testing.T) {
	out := gwRenderFull(t)
	for _, chain := range []string{"pre", "input", "forward"} {
		body := out[strings.Index(out, "chain "+chain+" {"):]
		body = body[:strings.Index(body, "\n\t}\n")]
		trusted := strings.Index(body, "@trusted4 return")
		drop := strings.Index(body, "counter drop")
		if reject := strings.Index(body, "counter reject"); drop < 0 || (reject >= 0 && reject < drop) {
			drop = reject
		}
		if trusted < 0 || drop < 0 || trusted > drop {
			t.Errorf("%s: the trusted return must come before the first drop (trusted %d, drop %d)", chain, trusted, drop)
		}
	}
}

func TestRenderGatewayRefusesWhatHandEditingCouldSmuggleIn(t *testing.T) {
	cases := map[string]func(*Spec){
		"interface with a quote":   func(sp *Spec) { sp.Forwards[0].Interface = `eth0" drop` },
		"target that is a command": func(sp *Spec) { sp.Forwards[0].Target = "10.0.0.5; flush ruleset" },
		"ports with braces":        func(sp *Spec) { sp.Forwards[0].Ports = "80 } drop {" },
		"unknown source nat":       func(sp *Spec) { sp.Forwards[0].SourceNAT = "sometimes" },
		"nat source everything":    func(sp *Spec) { sp.NAT[0].Source = "0.0.0.0/0" },
		"limit action":             func(sp *Spec) { sp.Limits[0].Action = "accept" },
		"limit per":                func(sp *Spec) { sp.Limits[0].Per = "fortnight" },
		"manual entry":             func(sp *Spec) { sp.Blocklists[0].Entries[0] = "203.0.113.0/24 accept" },
		"name with a quote":        func(sp *Spec) { sp.Limits[0].Name = `x" accept #` },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			sp, trusted, _ := gwFullSpec()
			mutate(sp)
			if out, err := renderGateway(sp, trusted); err == nil {
				t.Fatalf("rendered a spec it should have refused:\n%s", out)
			}
		})
	}
}

func TestNormForwardRules(t *testing.T) {
	base := ForwardSpec{Name: "x", Protocol: "tcp", Ports: "8000-8010", Target: "10.0.0.5", SourceNAT: "auto"}
	cases := []struct {
		name   string
		mutate func(*ForwardSpec)
		want   string // substring of the refusal; empty means accepted
	}{
		{"range keeps its numbers", func(f *ForwardSpec) { f.TargetPort = "8000-8010" }, ""},
		{"range with no target port", func(f *ForwardSpec) {}, ""},
		{"range to a smaller range", func(f *ForwardSpec) { f.TargetPort = "9000-9005" }, "same size"},
		{"range to a single port", func(f *ForwardSpec) { f.TargetPort = "9000" }, "same size"},
		{"range moved to the same width elsewhere", func(f *ForwardSpec) { f.TargetPort = "9000-9010" }, "one for one"},
		{"single port to a range", func(f *ForwardSpec) { f.Ports, f.TargetPort = "80", "80-90" }, "same size"},
		{"single port moved", func(f *ForwardSpec) { f.Ports, f.TargetPort = "80", "8080" }, ""},
		{"loopback", func(f *ForwardSpec) { f.Target = "127.0.0.1" }, "route_localnet"},
		{"loopback v6", func(f *ForwardSpec) { f.Target = "::1" }, "route_localnet"},
		{"unspecified", func(f *ForwardSpec) { f.Target = "0.0.0.0" }, "not an address"},
		{"source of the other family", func(f *ForwardSpec) { f.Sources = []string{"2001:db8::/32"} }, "same kind"},
		{"bad source", func(f *ForwardSpec) { f.Sources = []string{"not-a-network"} }, "not an IP address"},
		{"bad protocol", func(f *ForwardSpec) { f.Protocol = "icmp" }, "tcp or udp"},
		{"bad interface", func(f *ForwardSpec) { f.Interface = "eth 0" }, "interface name"},
		{"name required", func(f *ForwardSpec) { f.Name = "" }, "name"},
		{"descending range", func(f *ForwardSpec) { f.Ports = "8010-8000" }, "lower port"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := base
			c.mutate(&f)
			_, err := normForward(f)
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Fatalf("err = %v, want one containing %q", err, c.want)
			}
		})
	}
}

func TestNormLimitRules(t *testing.T) {
	base := LimitSpec{Name: "x", Protocol: "tcp", Ports: "22", Rate: 10, Per: "minute", Action: "drop"}
	cases := []struct {
		name   string
		mutate func(*LimitSpec)
		want   string
	}{
		{"rate only", func(l *LimitSpec) {}, ""},
		{"connections only", func(l *LimitSpec) { l.Rate, l.Per = 0, ""; l.MaxConnections = 5 }, ""},
		{"neither", func(l *LimitSpec) { l.Rate, l.Per = 0, "" }, "needs a rate"},
		{"rate too high", func(l *LimitSpec) { l.Rate = 1_000_001 }, "rate"},
		{"negative burst", func(l *LimitSpec) { l.Burst = -1 }, "burst"},
		{"burst too high", func(l *LimitSpec) { l.Burst = 1_000_001 }, "burst"},
		{"connections too high", func(l *LimitSpec) { l.MaxConnections = 1_000_001 }, "connection limit"},
		{"bad per", func(l *LimitSpec) { l.Per = "day" }, "per second"},
		{"bad action", func(l *LimitSpec) { l.Action = "accept" }, "drop or reject"},
		{"bad ports", func(l *LimitSpec) { l.Ports = "0" }, "port"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := base
			c.mutate(&l)
			_, err := normLimit(l)
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Fatalf("err = %v, want one containing %q", err, c.want)
			}
		})
	}
}

func TestStoredSourceNATRoundTrips(t *testing.T) {
	for _, c := range []struct {
		choice string
		masq   bool
		stored string
	}{
		{"auto", true, "auto:always"}, {"auto", false, "auto:never"},
		{"always", true, "always"}, {"never", false, "never"},
	} {
		if got := storeNAT(c.choice, c.masq); got != c.stored {
			t.Errorf("storeNAT(%s,%v) = %s, want %s", c.choice, c.masq, got, c.stored)
		}
		choice, masq, ok := splitNAT(c.stored)
		if !ok || choice != c.choice || masq != c.masq {
			t.Errorf("splitNAT(%s) = %s %v %v", c.stored, choice, masq, ok)
		}
	}
	if choice, masq, ok := splitNAT("auto"); !ok || choice != "auto" || !masq {
		t.Error("a bare auto must read as masqueraded, the topology-independent choice")
	}
	if _, _, ok := splitNAT("sometimes"); ok {
		t.Error("an unknown choice was accepted")
	}
}

func TestRoutedHereDecidesAutoSourceNAT(t *testing.T) {
	var addrs []ipAddr
	if err := json.Unmarshal([]byte(`[
		{"ifname":"lo","addr_info":[{"family":"inet","local":"127.0.0.1","prefixlen":8}]},
		{"ifname":"eth0","addr_info":[{"family":"inet","local":"203.0.113.20","prefixlen":24}]},
		{"ifname":"docker0","addr_info":[{"family":"inet","local":"172.17.0.1","prefixlen":16}]},
		{"ifname":"tailscale0","addr_info":[{"family":"inet","local":"100.110.34.31","prefixlen":32}]}
	]`), &addrs); err != nil {
		t.Fatal(err)
	}
	uplinks := map[string]bool{"eth0": true}
	for target, want := range map[string]bool{
		"172.17.0.2":    true,  // a container on a bridge this host routes
		"203.0.113.20":  true,  // this host's own address
		"203.0.113.77":  false, // on the uplink's LAN: its reply would bypass this host
		"10.0.0.5":      false, // somewhere else entirely
		"100.110.34.31": true,  // own tailnet address
		"100.64.9.9":    false, // a peer, reached by a route, not an attached network
	} {
		if got := routedHere(addrs, uplinks, netip.MustParseAddr(target)); got != want {
			t.Errorf("routedHere(%s) = %v, want %v", target, got, want)
		}
	}
}

func TestParseGatewayCountersSumsRulesByComment(t *testing.T) {
	got, err := parseGatewayCounters(fixture(t, "gateway-table.json"))
	if err != nil {
		t.Fatal(err)
	}
	// "both" protocols are two rules with one comment; a limit is a rule in
	// each of two chains.
	for comment, want := range map[string]RuleCounter{
		"forward:2":    {Packets: 3, Bytes: 180},
		"limit:9":      {Packets: 1, Bytes: 60},
		"blocklist:12": {Packets: 2, Bytes: 120},
		"nat:5":        {Packets: 7, Bytes: 420},
	} {
		if got[comment] != want {
			t.Errorf("%s = %+v, want %+v", comment, got[comment], want)
		}
	}
	if _, ok := got["trusted"]; ok {
		t.Error("a rule with no comment was counted")
	}
}

func TestParseGatewayCountersRefusesGarbage(t *testing.T) {
	if _, err := parseGatewayCounters("not json"); err == nil {
		t.Fatal("garbage parsed")
	}
}
