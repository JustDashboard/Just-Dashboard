package netx

import (
	"context"
	"errors"
	"net/netip"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// What an operator types is a list of addresses; what a port is kept to is
// the same list as networks, each written once, in the order nft and the
// page both read.
func TestNormalizePortSources(t *testing.T) {
	got, err := NormalizePortSources([]string{
		" 203.0.113.7 ", "198.51.100.77/24", "2001:db8::1", "198.51.100.9", "", "::ffff:192.0.2.4", "2001:db8:aa::/48",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Masked, the address inside the /24 folded into it, a mapped address
	// unmapped, IPv4 before IPv6, and a single address written bare.
	want := []string{"192.0.2.4", "198.51.100.0/24", "203.0.113.7", "2001:db8::1", "2001:db8:aa::/48"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalised = %v, want %v", got, want)
	}

	for _, c := range []struct {
		name string
		in   []string
		says string
	}{
		{"nothing", []string{"", "  "}, "at least one"},
		{"every address", []string{"0.0.0.0/0"}, "choose Anywhere"},
		{"every IPv6 address", []string{"::/0"}, "choose Anywhere"},
		{"most of the internet", []string{"10.0.0.0/7"}, "wider than a /8"},
		{"a wide IPv6 range", []string{"2001::/15"}, "wider than a /8"},
		{"this server", []string{"127.0.0.1"}, "always reaches it"},
		{"loopback over IPv6", []string{"::1"}, "always reaches it"},
		{"no source at all", []string{"0.0.0.0"}, "not an address a connection comes from"},
		{"a multicast group", []string{"239.1.2.3"}, "not an address a connection comes from"},
		{"words", []string{"office"}, "is not an IP address"},
		{"a zone", []string{"fe80::1%eth0"}, "zone"},
		{"a bad prefix", []string{"203.0.113.0/33"}, "CIDR"},
	} {
		if _, err := NormalizePortSources(c.in); err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: err = %v, want it to say %q", c.name, err, c.says)
		}
	}

	var many []string
	for i := 1; i <= maxPortSources+1; i++ {
		many = append(many, "203.0.113."+strconv.Itoa(i*2))
	}
	if _, err := NormalizePortSources(many); err == nil || !strings.Contains(err.Error(), "at most 16") {
		t.Errorf("seventeen addresses: err = %v", err)
	}
	// The bound is on what is kept, after neighbours inside one another fold.
	if got, err := NormalizePortSources(append(many, "203.0.113.0/24")); err != nil || len(got) != 1 {
		t.Errorf("seventeen addresses inside one network = %v, %v", got, err)
	}
}

func keptSpec(sources ...string) PortAllowlistSpec {
	return PortAllowlistSpec{
		ID: 21, Name: "shop-db", Owner: "database:shop-db", Protocol: "tcp", Port: 5432, Sources: sources,
		Made: Made{CreatedAt: time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC), CreatedBy: "admin"},
	}
}

// The rules say the port, this host, and every source but the listed ones,
// once per family — and a family the list names nothing in is refused whole.
func TestPortAllowlistRulesNameThePortThisHostAndTheList(t *testing.T) {
	got := portAllowlistRules(keptSpec("198.51.100.0/24", "203.0.113.7"))
	want := []string{
		`meta l4proto tcp th dport 5432 fib daddr type local ip saddr != { 198.51.100.0/24, 203.0.113.7 } counter drop comment "port-allowlist:21"`,
		`meta l4proto tcp th dport 5432 fib daddr type local meta nfproto ipv6 counter drop comment "port-allowlist:21"`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rules =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	got = portAllowlistRules(keptSpec("2001:db8::1"))
	want = []string{
		`meta l4proto tcp th dport 5432 fib daddr type local meta nfproto ipv4 counter drop comment "port-allowlist:21"`,
		`meta l4proto tcp th dport 5432 fib daddr type local ip6 saddr != 2001:db8::1 counter drop comment "port-allowlist:21"`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("an IPv6-only list =\n%s", strings.Join(got, "\n"))
	}
}

// The drop runs after the table's early returns — established connections,
// loopback, the trusted sets — and before the exceptions and blocklists: an
// exception lets a network past the lists of addresses to refuse, not past
// the list a port is kept to.
func TestKeptPortRendersInPreBetweenTheReturnsAndTheExceptions(t *testing.T) {
	sp := emptySpec()
	sp.PortAllowlists = []PortAllowlistSpec{keptSpec("203.0.113.7")}
	sp.Blocklists = []BlocklistSpec{{ID: 3, Name: "scanners", Kind: "manual", Entries: []string{"192.0.2.0/24"}, Enabled: true}}
	sp.Exceptions = []ExceptionSpec{{ID: 4, Address: "198.51.100.0/24", Scope: "all", Reason: "partner"}}
	out, err := renderGatewayWith(sp, mergePrefixes([]netip.Prefix{netip.MustParsePrefix("100.64.0.0/10")}), func(bl BlocklistSpec) []netip.Prefix {
		return blocklistEntries("", bl)
	})
	if err != nil {
		t.Fatal(err)
	}
	order := []string{
		"chain pre {",
		`ip6 saddr @trusted6 return`,
		`ip saddr != 203.0.113.7 counter drop comment "port-allowlist:21"`,
		`meta nfproto ipv6 counter drop comment "port-allowlist:21"`,
		`counter return comment "exception:4"`,
		`counter drop comment "blocklist:3"`,
	}
	at := 0
	for _, want := range order {
		i := strings.Index(out[at:], want)
		if i < 0 {
			t.Fatalf("%q missing or out of order in:\n%s", want, out)
		}
		at += i + len(want)
	}

	// A kept port on its own still hooks prerouting: it is a drop like any
	// other, and nothing else has to be in the table for it to work.
	only := emptySpec()
	only.PortAllowlists = []PortAllowlistSpec{keptSpec("203.0.113.7")}
	out, err = renderGateway(only, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "chain pre {") || strings.Contains(out, "chain input") || strings.Contains(out, "chain forward") {
		t.Fatalf("a kept port alone renders:\n%s", out)
	}
	if gatewayEmpty(only) || !gatewayHolds(only) {
		t.Fatal("a spec keeping a port is not an empty gateway")
	}
}

// A spec is a file an operator can edit: an entry that does not pass, or two
// that would each drop what the other admits, stop the render.
func TestKeptPortsThatCannotBeRenderedStopTheRender(t *testing.T) {
	for name, entries := range map[string][]PortAllowlistSpec{
		"no sources":      {keptSpec()},
		"a bad owner":     {{ID: 1, Name: "x", Owner: "database shop", Protocol: "tcp", Port: 5432, Sources: []string{"203.0.113.7"}}},
		"no port":         {{ID: 1, Name: "x", Owner: "database:x", Protocol: "tcp", Sources: []string{"203.0.113.7"}}},
		"both protocols":  {{ID: 1, Name: "x", Owner: "database:x", Protocol: "both", Port: 5432, Sources: []string{"203.0.113.7"}}},
		"one port twice":  {keptSpec("203.0.113.7"), {ID: 22, Name: "other", Owner: "database:other", Protocol: "tcp", Port: 5432, Sources: []string{"192.0.2.1"}}},
		"one owner twice": {keptSpec("203.0.113.7"), {ID: 22, Name: "shop-db", Owner: "database:shop-db", Protocol: "tcp", Port: 6379, Sources: []string{"192.0.2.1"}}},
	} {
		sp := emptySpec()
		sp.PortAllowlists = entries
		if _, err := renderGateway(sp, nil); err == nil {
			t.Errorf("%s: rendered", name)
		}
	}
}

func TestSetPortAllowlistLoadsTheTableAndKeepsTheEntryAcrossEdits(t *testing.T) {
	h := newGwHost(t)
	ctx := context.Background()
	req := PortAllowlistRequest{Owner: "database:shop-db", Name: "shop-db", Protocol: "tcp", Port: 5432, Sources: []string{"203.0.113.7", "198.51.100.4/24"}}
	saved, err := h.SetPortAllowlist(ctx, req, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(saved.Sources, []string{"198.51.100.0/24", "203.0.113.7"}) || saved.CreatedBy != "admin" {
		t.Fatalf("saved = %+v", saved)
	}
	if len(h.loaded) == 0 || !strings.Contains(h.loaded[len(h.loaded)-1], `ip saddr != { 198.51.100.0/24, 203.0.113.7 } counter drop comment "port-allowlist:`+strconv.Itoa(saved.ID)+`"`) {
		t.Fatalf("loaded = %v", h.loaded)
	}
	// Nothing about a kept port is the operator's to be protected from: it is
	// not one of their addresses that is trusted for it.
	if len(h.spec(t).Trusted) != 0 {
		t.Fatalf("keeping a port trusted %v", h.spec(t).Trusted)
	}

	req.Sources = []string{"2001:db8::/32"}
	edited, err := h.SetPortAllowlist(ctx, req, "bob")
	if err != nil {
		t.Fatal(err)
	}
	if edited.ID != saved.ID || edited.CreatedBy != "admin" || !reflect.DeepEqual(edited.Sources, []string{"2001:db8::/32"}) {
		t.Fatalf("edited = %+v, want the same entry with the new list", edited)
	}
	if got := h.spec(t).PortAllowlists; len(got) != 1 {
		t.Fatalf("an edit added an entry: %+v", got)
	}

	// The same port for somebody else would drop what this one admits.
	_, err = h.SetPortAllowlist(ctx, PortAllowlistRequest{Owner: "database:other", Name: "other", Protocol: "tcp", Port: 5432, Sources: []string{"192.0.2.1"}}, "admin")
	if !errors.Is(err, ErrExists) {
		t.Fatalf("a second owner of the port: %v", err)
	}
	loads := len(h.loaded)
	if _, err := h.SetPortAllowlist(ctx, PortAllowlistRequest{Owner: "database:shop-db", Name: "shop-db", Protocol: "tcp", Port: 5432, Sources: []string{"0.0.0.0/0"}}, "admin"); err == nil {
		t.Fatal("every address was kept as a list")
	}
	if len(h.loaded) != loads {
		t.Fatal("a refused list loaded the table")
	}

	rules, err := h.PortAllowlistRules(req)
	if err != nil || len(rules) != 2 || !strings.Contains(rules[1], `"port-allowlist:`+strconv.Itoa(saved.ID)+`"`) {
		t.Fatalf("the preview of the entry's own rules = %v, %v", rules, err)
	}
	fresh, err := h.PortAllowlistRules(PortAllowlistRequest{Owner: "database:cache", Name: "cache", Protocol: "tcp", Port: 6379, Sources: []string{"192.0.2.1"}})
	if err != nil || !strings.Contains(fresh[0], `"port-allowlist:`+strconv.Itoa(h.spec(t).NextID)+`"`) {
		t.Fatalf("the preview of a new entry = %v, %v", fresh, err)
	}

	removed, err := h.RemovePortAllowlist(ctx, "database:shop-db")
	if err != nil || !removed {
		t.Fatalf("remove = %v, %v", removed, err)
	}
	if got := h.spec(t).PortAllowlists; len(got) != 0 {
		t.Fatalf("still kept: %+v", got)
	}
	loads = len(h.loaded)
	if removed, err := h.RemovePortAllowlist(ctx, "database:shop-db"); err != nil || removed {
		t.Fatalf("removing what is not there = %v, %v", removed, err)
	}
	if len(h.loaded) != loads {
		t.Fatal("removing nothing loaded the table")
	}
}

// The reading says whether the kernel holds the rules and how much they
// refused, and names who every drop in the table lets in first.
func TestPortAllowlistReadingSaysWhatTheKernelHolds(t *testing.T) {
	h := newGwHost(t, "100.64.0.0/10", "127.0.0.1/32")
	sp := emptySpec()
	sp.Trusted = []string{"198.51.100.23/32"}
	sp.PortAllowlists = []PortAllowlistSpec{keptSpec("203.0.113.7")}
	h.seed(t, sp)
	h.first("nft -t -j list table inet jd_gateway", `{"nftables":[{"metainfo":{}},{"table":{"family":"inet","name":"jd_gateway","handle":9}},`+
		`{"rule":{"family":"inet","table":"jd_gateway","chain":"pre","comment":"port-allowlist:21","expr":[{"counter":{"packets":40,"bytes":2400}}]}},`+
		`{"rule":{"family":"inet","table":"jd_gateway","chain":"pre","comment":"port-allowlist:21","expr":[{"counter":{"packets":2,"bytes":160}}]}}]}`, nil)
	got, err := h.PortAllowlists(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Available || !got.Loaded || len(got.Entries) != 1 {
		t.Fatalf("reading = %+v", got)
	}
	e := got.Entries[0]
	if !e.Enforced || e.Refused != 42 || len(e.Rules) != 2 {
		t.Fatalf("entry = %+v", e)
	}
	if !reflect.DeepEqual(got.AlsoAdmitted, []string{"100.64.0.0/10", "198.51.100.23"}) {
		t.Fatalf("also admitted = %v, want the allowlist range and the kept address, loopback aside", got.AlsoAdmitted)
	}

	// A table that is not in the kernel holds nothing in force.
	h.first("nft -t -j list table inet jd_gateway", "Error: No such file or directory", errors.New("Error: No such file or directory"))
	if got, _ := h.PortAllowlists(context.Background()); got.Loaded || got.Entries[0].Enforced || !got.Available {
		t.Fatalf("an absent table = %+v", got)
	}
	// And a host with no nft at all cannot keep a port to anything.
	h.first("nft -t -j list table inet jd_gateway", "", &UnavailableError{Tool: "nft", Package: "nftables"})
	if got, _ := h.PortAllowlists(context.Background()); got.Available || got.Reason == "" {
		t.Fatalf("no nft = %+v", got)
	}
}

// A change to nothing but a kept port is a gateway change for the journal:
// recovery reloads the saved table, or removes the table it created.
func TestKeptPortChangesAreJournaledWithTheGatewayUndo(t *testing.T) {
	s := testService(t)
	kept := emptySpec()
	kept.PortAllowlists = []PortAllowlistSpec{keptSpec("203.0.113.7")}
	commands, err := s.recoveryPlan(context.Background(), emptySpec(), kept)
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) == 0 || strings.Join(commands[0].Args, " ") != "delete table inet jd_gateway" || !commands[0].AllowGone {
		t.Fatalf("recovery from no table removes it: %+v", commands)
	}
	edited := kept.clone()
	edited.PortAllowlists[0].Sources = []string{"192.0.2.1"}
	commands, err = s.recoveryPlan(context.Background(), kept, edited)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range commands {
		if c.Tool == "nft" && strings.Join(c.Args, " ") == "-f "+filepath.Join(s.paths.Dir, gatewayFile) {
			found = true
		}
	}
	if !found {
		t.Fatalf("recovery reloads the saved gateway table: %+v", commands)
	}
}
