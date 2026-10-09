package netx

import (
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The newer gateway forms: mapped NAT in both directions, destination-scoped
// translation, a ceiling for everyone, exceptions and expiring trusted
// addresses. A spec that uses none of them renders the existing golden file
// unchanged (TestRenderGatewayGolden); one that uses all of them is pinned
// here.

func gwModesSpec() (*Spec, []netip.Prefix, map[int][]netip.Prefix) {
	made := Made{CreatedAt: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC), CreatedBy: "admin"}
	until := time.Date(2026, 10, 10, 9, 30, 0, 0, time.UTC)
	sp := emptySpec()
	sp.NAT = []NATSpec{
		{ID: 1, Name: "mail host", Source: "10.0.4.25/32", Interface: "eth0", Mode: natOneToOne, Translated: "203.0.113.25/32", Enabled: true, Made: made},
		{ID: 2, Name: "lab block", Source: "10.0.8.0/29", Interface: "eth0", Mode: natOneToOne, Translated: "203.0.113.80/29", Enabled: true, Made: made},
		{ID: 3, Name: "v6 site", Source: "fd00:3::/48", Interface: "eth0", Mode: natNPTv6, Translated: "2001:db8:3::/48", Enabled: true, Made: made},
		{ID: 4, Name: "partner only", Source: "10.0.9.0/24", Interface: "eth0", Destinations: []string{"192.0.2.0/24", "198.51.100.0/24"}, Enabled: true, Made: made},
	}
	sp.Limits = []LimitSpec{
		{ID: 5, Name: "db", Protocol: "tcp", Ports: "5432", MaxConnections: 20, GlobalConnections: 200, PerSource: true, Action: "reject", Enabled: true, Made: made},
	}
	sp.Blocklists = []BlocklistSpec{
		{ID: 6, Name: "scanners", Kind: "manual", Entries: []string{"198.51.100.0/24"}, Enabled: true, Count: 1, Made: made},
		{ID: 7, Name: "region", Kind: "manual", Entries: []string{"192.0.2.0/24"}, Enabled: true, Count: 1, Made: made},
	}
	sp.Exceptions = []ExceptionSpec{
		{ID: 8, Address: "198.51.100.7/32", Scope: "blocklist:6", Reason: "partner monitor", ExpiresAt: until, Made: made},
		{ID: 9, Address: "198.51.100.64/26", Scope: "blocklist:6", Reason: "office", Made: made},
		{ID: 10, Address: "2001:db8:77::/48", Scope: "all", Reason: "vendor support", ExpiresAt: until, Made: made},
	}
	sp.Trusted = []string{"203.0.113.9/32", "198.51.100.200/32"}
	sp.TrustedNotes = []TrustedNote{{Address: "198.51.100.200/32", Reason: "contractor", ExpiresAt: until, Made: made}}
	trusted := []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128"), netip.MustParsePrefix("203.0.113.9/32")}
	return sp, mergePrefixes(trusted), nil
}

func gwRenderModes(t *testing.T) string {
	t.Helper()
	sp, trusted, _ := gwModesSpec()
	out, err := renderGatewayWith(sp, trusted, func(bl BlocklistSpec) []netip.Prefix { return blocklistEntries("", bl) })
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRenderGatewayModesGolden(t *testing.T) {
	gwGolden(t, "gateway-modes.nft", gwRenderModes(t))
}

func TestRenderGatewayModesArePureFunctionsOfTheSpec(t *testing.T) {
	// The expiry is an absolute instant in the rule, so the boot file does
	// not change as time passes and the kernel enforces it without us.
	prev := gatewayNow
	t.Cleanup(func() { gatewayNow = prev })
	first := gwRenderModes(t)
	gatewayNow = func() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) }
	if second := gwRenderModes(t); first != second {
		t.Fatal("rendering changed with the clock")
	}
	until := time.Date(2026, 10, 10, 9, 30, 0, 0, time.UTC).Unix()
	for _, want := range []string{
		"ip saddr 198.51.100.7 meta time < " + strconv.FormatInt(until, 10) + " counter return comment \"exception:8\"",
		"ip6 saddr 2001:db8:77::/48 meta time < " + strconv.FormatInt(until, 10) + " counter return comment \"exception:10\"",
		"ip saddr 198.51.100.200 meta time < " + strconv.FormatInt(until, 10) + " return comment \"trusted:198.51.100.200/32\"",
	} {
		if !strings.Contains(first, want) {
			t.Errorf("missing %q in:\n%s", want, first)
		}
	}
	if strings.Contains(first, "198.51.100.200 }") || strings.Contains(first, "198.51.100.200,") {
		t.Error("an expiring trusted address must not join the permanent trusted set")
	}
}

func TestExceptionScopedToOneListLeavesTheOthersJudging(t *testing.T) {
	out := gwRenderModes(t)
	// List 6 has exceptions: it jumps to its own chain, whose returns go
	// back to the pre chain and on to list 7's drop.
	for _, want := range []string{
		"ip saddr @bl_6_4 jump bx_6",
		"ip6 saddr @bl_6_6 jump bx_6",
		"chain bx_6 {",
		"counter drop comment \"blocklist:6\"",
		"ip saddr @bl_7_4 counter drop comment \"blocklist:7\"",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
	bx := out[strings.Index(out, "chain bx_6 {"):]
	bx = bx[:strings.Index(bx, "\t}\n")]
	if strings.Contains(bx, "hook") {
		t.Error("an exception chain must not be a base chain")
	}
	if strings.Index(bx, "exception:9") > strings.Index(bx, "blocklist:6") {
		t.Error("exceptions must come before the list's drop")
	}
}

func TestMappedNATTranslatesBothDirectionsAndMarksTheInboundHalf(t *testing.T) {
	out := gwRenderModes(t)
	for _, want := range []string{
		`iifname "eth0" ip daddr 203.0.113.25 ct mark set ct mark and 0x00ffffff or 0x4a000000 counter dnat ip to 10.0.4.25 comment "nat-in:1"`,
		`ip saddr 10.0.4.25/32 oifname "eth0" counter snat ip to 203.0.113.25 comment "nat:1"`,
		`iifname "eth0" ip daddr 203.0.113.80/29 ct mark set ct mark and 0x00ffffff or 0x4a000000 counter dnat ip prefix to 10.0.8.0/29 comment "nat-in:2"`,
		`ip saddr 10.0.8.0/29 oifname "eth0" counter snat ip prefix to 203.0.113.80/29 comment "nat:2"`,
		`iifname "eth0" ip6 daddr 2001:db8:3::/48 ct mark set ct mark and 0x00ffffff or 0x4a000000 counter dnat ip6 prefix to fd00:3::/48 comment "nat-in:3"`,
		`ip6 saddr fd00:3::/48 oifname "eth0" counter snat ip6 prefix to 2001:db8:3::/48 comment "nat:3"`,
		`ip saddr 10.0.9.0/24 ip daddr { 192.0.2.0/24, 198.51.100.0/24 } oifname "eth0" counter masquerade comment "nat:4"`,
		`ip saddr 10.0.9.0/24 ip daddr { 192.0.2.0/24, 198.51.100.0/24 } oifname "eth0" ct state new ct mark set ct mark and 0x00ffffff or 0x4a000000 comment "nat-mark:4"`,
		`meta l4proto tcp ct original proto-dst 5432 ct state new ct count over 200 counter reject with tcp reset comment "limit-global:5"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestNormNATModes(t *testing.T) {
	cases := []struct {
		name string
		in   NATSpec
		err  string
		want NATSpec
	}{
		{"masquerade stays the empty mode", NATSpec{Name: "x", Source: "10.0.0.0/24", Interface: "eth0", Mode: "masquerade"}, "", NATSpec{Mode: ""}},
		{"one address maps to one", NATSpec{Name: "x", Source: "10.0.0.5", Interface: "eth0", Mode: natOneToOne, Translated: "203.0.113.5"}, "", NATSpec{Mode: natOneToOne, Translated: "203.0.113.5/32"}},
		{"unequal widths", NATSpec{Name: "x", Source: "10.0.0.0/30", Interface: "eth0", Mode: natOneToOne, Translated: "203.0.113.0/29"}, "same size", NATSpec{}},
		{"mixed families", NATSpec{Name: "x", Source: "10.0.0.5", Interface: "eth0", Mode: natOneToOne, Translated: "2001:db8::5"}, "same kind", NATSpec{}},
		{"overlapping sides", NATSpec{Name: "x", Source: "10.0.0.0/29", Interface: "eth0", Mode: natOneToOne, Translated: "10.0.0.0/29"}, "overlap", NATSpec{}},
		{"too wide for one-to-one", NATSpec{Name: "x", Source: "10.0.0.0/16", Interface: "eth0", Mode: natOneToOne, Translated: "203.0.0.0/16"}, "at most 256", NATSpec{}},
		{"nptv6 refuses IPv4", NATSpec{Name: "x", Source: "10.0.0.0/24", Interface: "eth0", Mode: natNPTv6, Translated: "203.0.113.0/24"}, "IPv6 network", NATSpec{}},
		{"nptv6 refuses a /96", NATSpec{Name: "x", Source: "fd00::/96", Interface: "eth0", Mode: natNPTv6, Translated: "2001:db8::/96"}, "between /16 and /64", NATSpec{}},
		{"mapping takes no fixed address", NATSpec{Name: "x", Source: "10.0.0.5", Interface: "eth0", Mode: natOneToOne, Translated: "203.0.113.5", ToAddress: "203.0.113.5"}, "takes no fixed", NATSpec{}},
		{"mapping needs its public side", NATSpec{Name: "x", Source: "10.0.0.5", Interface: "eth0", Mode: natOneToOne}, "needs the public", NATSpec{}},
		{"public side without a mapping", NATSpec{Name: "x", Source: "10.0.0.0/24", Interface: "eth0", Translated: "203.0.113.0/24"}, "belongs to a one-to-one", NATSpec{}},
		{"destinations are merged and masked", NATSpec{Name: "x", Source: "10.0.0.0/24", Interface: "eth0", Destinations: []string{"192.0.2.9/24", "192.0.2.0/25"}}, "", NATSpec{Destinations: []string{"192.0.2.0/24"}}},
		{"a destination of everything", NATSpec{Name: "x", Source: "10.0.0.0/24", Interface: "eth0", Destinations: []string{"0.0.0.0/0"}}, "every address", NATSpec{}},
		{"a destination of the other family", NATSpec{Name: "x", Source: "10.0.0.0/24", Interface: "eth0", Destinations: []string{"2001:db8::/32"}}, "same kind", NATSpec{}},
		{"an unknown mode", NATSpec{Name: "x", Source: "10.0.0.0/24", Interface: "eth0", Mode: "full-cone"}, "one-to-one or nptv6", NATSpec{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := normNAT(c.in)
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) {
					t.Fatalf("err = %v, want %q", err, c.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Mode != c.want.Mode || got.Translated != c.want.Translated || strings.Join(got.Destinations, ",") != strings.Join(c.want.Destinations, ",") {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestNATCollisionsCoverOverlapsNotOnlyEqualSources(t *testing.T) {
	a := NATSpec{ID: 1, Source: "10.0.0.0/24", Interface: "eth0", Enabled: true}
	cases := []struct {
		name string
		b    NATSpec
		want bool
	}{
		{"same network", NATSpec{ID: 2, Source: "10.0.0.0/24", Interface: "eth0", Enabled: true}, true},
		{"a subnet of it", NATSpec{ID: 2, Source: "10.0.0.128/25", Interface: "eth0", Enabled: true}, true},
		{"another device", NATSpec{ID: 2, Source: "10.0.0.0/24", Interface: "eth1", Enabled: true}, false},
		{"disjoint destinations", NATSpec{ID: 2, Source: "10.0.0.0/24", Interface: "eth0", Destinations: []string{"192.0.2.0/24"}, Enabled: true}, true},
		{"switched off", NATSpec{ID: 2, Source: "10.0.0.0/24", Interface: "eth0"}, false},
		{"mappings claiming one public address", NATSpec{ID: 2, Source: "10.1.0.5/32", Interface: "eth1", Mode: natOneToOne, Translated: "203.0.113.5/32", Enabled: true}, false},
	}
	for _, c := range cases {
		if got := natsCollide(a, c.b); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	m1 := NATSpec{ID: 3, Source: "10.2.0.5/32", Interface: "eth0", Mode: natOneToOne, Translated: "203.0.113.5/32", Enabled: true}
	m2 := NATSpec{ID: 4, Source: "10.3.0.5/32", Interface: "eth1", Mode: natOneToOne, Translated: "203.0.113.5/32", Enabled: true}
	if !natsCollide(m1, m2) {
		t.Error("two mappings of one public address must collide")
	}
	b := NATSpec{ID: 2, Source: "10.0.0.0/24", Interface: "eth0", Destinations: []string{"192.0.2.0/24"}, Enabled: true}
	c := NATSpec{ID: 5, Source: "10.0.0.0/24", Interface: "eth0", Destinations: []string{"198.51.100.0/24"}, Enabled: true}
	if natsCollide(b, c) {
		t.Error("entries for disjoint destinations do not compete")
	}
}

func TestNormLimitGlobalCeilingAndProfiles(t *testing.T) {
	base := LimitSpec{Name: "db", Protocol: "tcp", Ports: "5432", Action: "drop"}
	only := base
	only.GlobalConnections = 100
	if _, err := normLimit(only); err != nil {
		t.Fatalf("a ceiling for everyone alone is a limit: %v", err)
	}
	bad := base
	bad.GlobalConnections = -1
	if _, err := normLimit(bad); err == nil {
		t.Fatal("a negative ceiling passed")
	}
	prof := base
	prof.Rate, prof.Per, prof.Profile = 5, "minute", "nope"
	if _, err := normLimit(prof); err == nil || !strings.Contains(err.Error(), "service profile") {
		t.Fatalf("err = %v", err)
	}
	for _, p := range limitProfiles {
		l := LimitSpec{Name: p.Name, Protocol: p.Protocol, Ports: p.Ports, Rate: p.Rate, Per: p.Per, Burst: p.Burst,
			PerSource: p.PerSource, MaxConnections: p.MaxConnections, GlobalConnections: p.GlobalConnections, Action: p.Action, Profile: p.ID}
		if _, err := normLimit(l); err != nil {
			t.Errorf("profile %s is not a valid limit: %v", p.ID, err)
		}
	}
}

func TestCheckExceptionsRefusesWhatWouldBeRendered(t *testing.T) {
	sp := emptySpec()
	sp.Blocklists = []BlocklistSpec{{ID: 1, Name: "l", Kind: "manual", Entries: []string{"198.51.100.0/24"}, Enabled: true}}
	for _, c := range []struct {
		name string
		e    ExceptionSpec
		err  string
	}{
		{"a scope with no list", ExceptionSpec{ID: 2, Address: "198.51.100.7", Scope: "blocklist:9", Reason: "x"}, "names no blocklist"},
		{"too wide", ExceptionSpec{ID: 2, Address: "0.0.0.0/1", Scope: "all", Reason: "x"}, "wider than"},
		{"a reason that breaks a comment", ExceptionSpec{ID: 2, Address: "198.51.100.7", Scope: "all", Reason: "a \"quote\""}, "reason"},
		{"not an address", ExceptionSpec{ID: 2, Address: "nope", Scope: "all", Reason: "x"}, "not an IP address"},
	} {
		sp.Exceptions = []ExceptionSpec{c.e}
		if err := checkExceptions(sp); err == nil || !strings.Contains(err.Error(), c.err) {
			t.Errorf("%s: err = %v", c.name, err)
		}
		if _, err := renderGatewayWith(sp, nil, func(bl BlocklistSpec) []netip.Prefix { return blocklistEntries("", bl) }); err == nil {
			t.Errorf("%s: an invalid exception was rendered", c.name)
		}
	}
}
