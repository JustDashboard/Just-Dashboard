package netx

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestRenderSysctlIsSortedAndGolden(t *testing.T) {
	sp := emptySpec()
	sp.Sysctls = map[string]string{
		"net.ipv4.tcp_syncookies":         "1",
		"net.ipv4.conf.all.rp_filter":     "2",
		"net.netfilter.nf_conntrack_max":  "524288",
		"net.ipv4.tcp_congestion_control": "bbr",
		"net.core.default_qdisc":          "fq",
	}
	gwGolden(t, "protection-sysctl.conf", renderSysctl(sp))
}

func TestTheClosedListIsComplete(t *testing.T) {
	wantKeys := []string{
		"net.ipv4.tcp_syncookies", "net.ipv4.conf.all.rp_filter", "net.ipv4.conf.default.rp_filter",
		"net.ipv4.conf.all.accept_redirects", "net.ipv6.conf.all.accept_redirects", "net.ipv4.conf.all.send_redirects",
		"net.ipv4.conf.all.accept_source_route", "net.ipv6.conf.all.accept_source_route",
		"net.ipv4.icmp_echo_ignore_broadcasts", "net.ipv4.icmp_ignore_bogus_error_responses", "net.ipv4.tcp_rfc1337",
		"net.ipv4.tcp_max_syn_backlog", "net.ipv4.tcp_synack_retries", "net.ipv4.conf.all.log_martians",
		"net.netfilter.nf_conntrack_max",
	}
	if len(protectionDefs) != len(wantKeys) {
		t.Fatalf("%d settings, want %d", len(protectionDefs), len(wantKeys))
	}
	seen := map[string]bool{}
	for i, d := range protectionDefs {
		if d.Key != wantKeys[i] || seen[d.Key] {
			t.Errorf("setting %d is %s, want %s", i, d.Key, wantKeys[i])
		}
		seen[d.Key] = true
		if d.Label == "" || len(d.Why) < 40 || !strings.HasSuffix(d.Why, ".") {
			t.Errorf("%s has no sentence of why", d.Key)
		}
		if d.Recommended != "" {
			if _, err := d.check(d.Recommended); err != nil {
				t.Errorf("%s recommends %s, which it refuses: %v", d.Key, d.Recommended, err)
			}
		}
	}
	rp, _ := protectionDefFor("net.ipv4.conf.all.rp_filter")
	if rp.Recommended != "2" || !strings.Contains(rp.Why, "Tailscale") || !strings.Contains(rp.Why, "Strict (1)") {
		t.Errorf("rp_filter must recommend loose and say why strict is not offered: %+v", rp)
	}
	if _, err := rp.check("1"); err == nil {
		t.Error("strict reverse-path filtering is offered, though it breaks policy routing")
	}
	if got, _ := protectionDefFor("net.ipv4.tcp_max_syn_backlog"); got.Min != 128 || got.Max != 262144 || got.Recommended != "4096" {
		t.Errorf("syn backlog = %+v", got)
	}
	if got, _ := protectionDefFor("net.ipv4.tcp_synack_retries"); got.Min != 1 || got.Max != 5 || got.Recommended != "2" {
		t.Errorf("synack retries = %+v", got)
	}
	if got, _ := protectionDefFor("net.netfilter.nf_conntrack_max"); got.Min != 65536 || got.Max != 4194304 || got.Recommended != "" {
		t.Errorf("conntrack max = %+v", got)
	}
}

func TestCheckValues(t *testing.T) {
	cases := []struct {
		key, value, want string
		ok               bool
	}{
		{"net.ipv4.tcp_syncookies", "1", "1", true},
		{"net.ipv4.tcp_syncookies", " 0 ", "0", true},
		{"net.ipv4.tcp_syncookies", "2", "", false},
		{"net.ipv4.tcp_syncookies", "on", "", false},
		{"net.ipv4.tcp_max_syn_backlog", "127", "", false},
		{"net.ipv4.tcp_max_syn_backlog", "128", "128", true},
		{"net.ipv4.tcp_max_syn_backlog", "262145", "", false},
		{"net.ipv4.tcp_max_syn_backlog", "0x1000", "", false},
		{"net.ipv4.tcp_max_syn_backlog", "04096", "4096", true},
		{"net.ipv4.tcp_synack_retries", "6", "", false},
		{"net.netfilter.nf_conntrack_max", "65535", "", false},
		{"net.netfilter.nf_conntrack_max", "4194304", "4194304", true},
		{"net.netfilter.nf_conntrack_max", "1\nnet.ipv4.ip_forward=0", "", false},
	}
	for _, c := range cases {
		d, _ := protectionDefFor(c.key)
		got, err := d.check(c.value)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("%s=%q: got %q, %v", c.key, c.value, got, err)
		}
	}
}

func TestConntrackRecommendationIsNeverBelowWhatIsRunning(t *testing.T) {
	d, _ := protectionDefFor("net.netfilter.nf_conntrack_max")
	for current, want := range map[string]string{"65536": "262144", "262144": "262144", "1048576": "1048576", "": "262144"} {
		if got := d.recommended(current); got != want {
			t.Errorf("recommended(%q) = %s, want %s", current, got, want)
		}
	}
}

func TestWeakensProtection(t *testing.T) {
	h := newGwHost(t)
	h.writeSys("net/netfilter/nf_conntrack_max", "262144")
	h.writeSys("net/ipv4/tcp_max_syn_backlog", "4096")
	cases := []struct {
		name   string
		values map[string]string
		want   bool
	}{
		{"syncookies on", map[string]string{"net.ipv4.tcp_syncookies": "1"}, false},
		{"syncookies off", map[string]string{"net.ipv4.tcp_syncookies": "0"}, true},
		{"redirects accepted", map[string]string{"net.ipv4.conf.all.accept_redirects": "1"}, true},
		{"backlog raised", map[string]string{"net.ipv4.tcp_max_syn_backlog": "8192"}, false},
		{"backlog lowered", map[string]string{"net.ipv4.tcp_max_syn_backlog": "1024"}, true},
		{"synack retries raised", map[string]string{"net.ipv4.tcp_synack_retries": "5"}, true},
		{"synack retries lowered", map[string]string{"net.ipv4.tcp_synack_retries": "1"}, false},
		{"conntrack raised", map[string]string{"net.netfilter.nf_conntrack_max": "524288"}, false},
		{"conntrack lowered", map[string]string{"net.netfilter.nf_conntrack_max": "131072"}, true},
		{"an unknown key", map[string]string{"net.ipv4.ip_forward": "0"}, true},
		{"an unreadable value", map[string]string{"net.ipv4.tcp_syncookies": "maybe"}, true},
		{"one weaker among several", map[string]string{"net.ipv4.tcp_syncookies": "1", "net.ipv4.tcp_rfc1337": "0"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := h.WeakensProtection(c.values); got != c.want {
				t.Errorf("WeakensProtection(%v) = %v", c.values, got)
			}
		})
	}
}

func newProtectionHost(t *testing.T) *gwHost {
	t.Helper()
	h := newGwHost(t)
	h.writeSys("net/ipv4/tcp_syncookies", "0")
	h.writeSys("net/ipv4/conf/all/rp_filter", "0")
	h.rec.on("sysctl -w", "")
	h.rec.on("sysctl -n net.ipv4.tcp_syncookies", "1")
	h.rec.on("sysctl -n net.ipv4.conf.all.rp_filter", "2")
	h.rec.on("sysctl -n net.netfilter.nf_conntrack_max", "524288")
	return h
}

func TestSetProtectionsAppliesOnlyWhatChangesAndRecordsEverythingAsked(t *testing.T) {
	h := newProtectionHost(t)
	h.writeSys("net/ipv4/conf/all/rp_filter", "2") // already where it should be
	err := h.SetProtections(context.Background(), map[string]string{
		"net.ipv4.tcp_syncookies": "1", "net.ipv4.conf.all.rp_filter": "2", "net.netfilter.nf_conntrack_max": "524288",
	}, "ops")
	if err != nil {
		t.Fatal(err)
	}
	var sysctls []string
	for _, c := range h.rec.commands() {
		if strings.HasPrefix(c, "sysctl ") {
			sysctls = append(sysctls, c)
		}
	}
	want := []string{
		"sysctl -w net.ipv4.tcp_syncookies=1", "sysctl -w net.netfilter.nf_conntrack_max=524288",
		"sysctl -n net.ipv4.tcp_syncookies", "sysctl -n net.netfilter.nf_conntrack_max",
	}
	if strings.Join(sysctls, "|") != strings.Join(want, "|") {
		t.Fatalf("sysctl commands = %v, want %v", sysctls, want)
	}
	h.order(t, "sysctl -w", "sysctl -n", "systemctl")
	sp := h.spec(t)
	if sp.Sysctls["net.ipv4.conf.all.rp_filter"] != "2" || len(sp.Sysctls) != 3 {
		t.Fatalf("spec = %v: a value the kernel already holds must still be kept for the next boot", sp.Sysctls)
	}
	b, _ := os.ReadFile(h.paths.Sysctl)
	if !strings.Contains(string(b), "net.ipv4.tcp_syncookies = 1") || !strings.Contains(string(b), "net.netfilter.nf_conntrack_max = 524288") {
		t.Fatalf("drop-in:\n%s", b)
	}
}

func TestSetProtectionsRefusals(t *testing.T) {
	cases := []struct {
		name   string
		values map[string]string
		want   string
	}{
		{"nothing", map[string]string{}, "no settings"},
		{"a key outside the list", map[string]string{"net.ipv4.ip_forward": "0"}, "not a setting the dashboard manages"},
		{"a key that smuggles another", map[string]string{"net.ipv4.tcp_syncookies=1\nnet.ipv4.ip_forward": "0"}, "not a setting"},
		{"a value out of range", map[string]string{"net.ipv4.tcp_synack_retries": "9"}, "from 1 to 5"},
		{"a value that is a word", map[string]string{"net.ipv4.tcp_syncookies": "yes"}, "one of 0, 1"},
		{"strict reverse-path filtering", map[string]string{"net.ipv4.conf.all.rp_filter": "1"}, "one of 0, 2"},
		{"a value with a second setting in it", map[string]string{"net.ipv4.tcp_syncookies": "1\nnet.ipv4.ip_forward=0"}, "one of"},
		{"a setting this host does not have", map[string]string{"net.ipv6.conf.all.accept_source_route": "0"}, "not available on this host"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newProtectionHost(t)
			os.Remove(h.sys + "/net/ipv6/conf/all/accept_source_route")
			err := h.SetProtections(context.Background(), c.values, "ops")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want one containing %q", err, c.want)
			}
			if h.rec.ran("sysctl") || h.saved() {
				t.Error("a refused request reached the kernel")
			}
		})
	}
}

func TestSetProtectionsPutsBackWhatItChangedWhenOneIsRefused(t *testing.T) {
	h := newProtectionHost(t)
	h.first("sysctl -w net.netfilter.nf_conntrack_max", "sysctl: permission denied", errors.New("sysctl: permission denied"))
	err := h.SetProtections(context.Background(), map[string]string{"net.ipv4.tcp_syncookies": "1", "net.netfilter.nf_conntrack_max": "524288"}, "ops")
	if err == nil || !strings.Contains(err.Error(), "nf_conntrack_max") {
		t.Fatalf("err = %v", err)
	}
	h.order(t, "sysctl -w net.ipv4.tcp_syncookies=1", "sysctl -w net.netfilter.nf_conntrack_max=524288", "sysctl -w net.ipv4.tcp_syncookies=0")
	if h.saved() {
		t.Error("saved")
	}
}

func TestSetProtectionsUndoesWhenTheKernelClampedTheValue(t *testing.T) {
	h := newProtectionHost(t)
	h.first("sysctl -n net.ipv4.tcp_syncookies", "0", nil) // the kernel kept its own
	err := h.SetProtections(context.Background(), map[string]string{"net.ipv4.tcp_syncookies": "1"}, "ops")
	if err == nil || !strings.Contains(err.Error(), "did not accept") {
		t.Fatalf("err = %v", err)
	}
	h.order(t, "sysctl -w net.ipv4.tcp_syncookies=1", "sysctl -n net.ipv4.tcp_syncookies", "sysctl -w net.ipv4.tcp_syncookies=0")
	if h.saved() {
		t.Error("saved")
	}
}

func TestResetProtectionForgetsTheValueAndLeavesTheRuntime(t *testing.T) {
	h := newProtectionHost(t)
	ctx := context.Background()
	if err := h.SetProtections(ctx, map[string]string{"net.ipv4.tcp_syncookies": "1"}, "ops"); err != nil {
		t.Fatal(err)
	}
	h.rec.mu.Lock()
	n := len(h.rec.calls)
	h.rec.mu.Unlock()
	if err := h.ResetProtection(ctx, "net.ipv4.tcp_syncookies"); err != nil {
		t.Fatal(err)
	}
	for _, c := range h.rec.commands()[n:] {
		if strings.HasPrefix(c, "sysctl") {
			t.Fatalf("reset touched the running kernel: %s", c)
		}
	}
	if len(h.spec(t).Sysctls) != 0 {
		t.Fatal("still in the spec")
	}
	b, _ := os.ReadFile(h.paths.Sysctl)
	if strings.Contains(string(b), "tcp_syncookies") {
		t.Fatalf("the boot drop-in still sets it:\n%s", b)
	}
	if err := h.ResetProtection(ctx, "net.ipv4.tcp_syncookies"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
	if err := h.ResetProtection(ctx, "net.ipv4.ip_forward"); err == nil || !strings.Contains(err.Error(), "not a setting") {
		t.Errorf("err = %v", err)
	}
}

func TestResetOfOtherSlicesSysctlsIsRefused(t *testing.T) {
	h := newProtectionHost(t)
	sp := emptySpec()
	sp.Sysctls = map[string]string{"net.ipv4.ip_forward": "1"}
	h.seed(t, sp)
	if err := h.ResetProtection(context.Background(), "net.ipv4.ip_forward"); err == nil {
		t.Fatal("the forwarding switch is not this page's to forget")
	}
}

func TestConntrackLevels(t *testing.T) {
	cases := []struct {
		count, max string
		percent    float64
		level      string
	}{
		{"1200", "262144", 0.46, "ok"},
		{"209716", "262144", 80.0, "warning"},
		{"209715", "262144", 80.0, "ok"},
		{"249036", "262144", 95.0, "warning"},
		{"249037", "262144", 95.0, "critical"},
		{"262144", "262144", 100, "critical"},
		{"0", "262144", 0, "ok"},
	}
	for _, c := range cases {
		h := newGwHost(t)
		h.writeSys("net/netfilter/nf_conntrack_count", c.count)
		h.writeSys("net/netfilter/nf_conntrack_max", c.max)
		got := readConntrack()
		if !got.Available || got.Level != c.level || got.Percent < c.percent-0.01 || got.Percent > c.percent+0.01 {
			t.Errorf("%s/%s = %+v, want %.2f%% %s", c.count, c.max, got, c.percent, c.level)
		}
	}
	h := newGwHost(t)
	os.Remove(h.sys + "/net/netfilter/nf_conntrack_count")
	if got := readConntrack(); got.Available || got.Level != "ok" {
		t.Errorf("a host without the module = %+v", got)
	}
	h.writeSys("net/netfilter/nf_conntrack_count", "5")
	h.writeSys("net/netfilter/nf_conntrack_max", "0")
	if got := readConntrack(); got.Available {
		t.Errorf("a zero maximum = %+v", got)
	}
}

func TestProtectionViewJoinsEverythingTheProtectionPageDraws(t *testing.T) {
	srv := newFeedServer(t)
	srv.set("/v4/xx.zone", "100.64.0.0/10\n198.51.100.0/24\n") // the first is private space and is dropped
	srv.set("/v4/yy.zone", "192.0.2.0/24\n")
	h := newGwHost(t, "10.8.0.0/16")
	ctx := context.Background()
	if _, err := h.AddBlocklist(ctx, BlocklistRequest{Name: "countries", Kind: "country", Countries: []string{"xx"}}, "198.51.100.44", "ops"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.AddBlocklist(ctx, BlocklistRequest{Name: "other", Kind: "country", Countries: []string{"yy"}}, "198.51.100.44", "ops"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.AddLimit(ctx, LimitRequest{Name: "app", Protocol: "tcp", Ports: "8000-8010", Rate: 10, Per: "second", PerSource: true, MaxConnections: 50}, "198.51.100.44", "ops"); err != nil {
		t.Fatal(err)
	}
	// The counters come from a recorded `nft -j list table`: limit:9 is the fixture's id, so give it that one.
	sp := h.spec(t)
	sp.Limits[0].ID = 9
	h.seed(t, sp)

	v, err := h.Protection(ctx, "198.51.100.44")
	if err != nil {
		t.Fatal(err)
	}
	if !v.Loaded || !v.ClientTrusted || v.Client != "198.51.100.44" {
		t.Fatalf("view = %+v", v)
	}
	if len(v.Blocklists) != 2 || !v.Blocklists[0].ContainsYou || v.Blocklists[1].ContainsYou || v.Blocklists[0].Count != 1 {
		t.Fatalf("blocklists = %+v", v.Blocklists)
	}
	if l := v.Limits[0]; l.Packets != 1 || l.Bytes != 60 || !l.PerSource {
		t.Fatalf("limit = %+v", l)
	}
	origins := map[string]string{}
	for _, e := range v.Trusted {
		origins[e.Address] = e.Origin
	}
	want := map[string]string{"127.0.0.0/8": "loopback", "::1/128": "loopback", "10.8.0.0/16": "allowlist", "198.51.100.44/32": "you"}
	for addr, origin := range want {
		if origins[addr] != origin {
			t.Errorf("%s origin = %q, want %q (all: %v)", addr, origins[addr], origin, origins)
		}
	}
	if len(v.Presets) != 2 || len(v.Settings) != len(protectionDefs) || v.Conntrack.Max != 262144 || v.ResetNote == "" {
		t.Fatalf("view = %+v", v)
	}
	b, _ := json.Marshal(v)
	for _, field := range []string{`"clientTrusted":true`, `"containsYou":true`, `"settings":[`, `"conntrack":{"available":true`, `"presets":[`, `"origin":"allowlist"`, `"atRecommended"`} {
		if !strings.Contains(string(b), field) {
			t.Errorf("json lacks %s", field)
		}
	}
	// A reader the allowlist covers is trusted without a record of their own.
	v, _ = h.Protection(ctx, "10.8.1.2")
	if !v.ClientTrusted {
		t.Error("an allowlisted client reads as untrusted")
	}
	v, _ = h.Protection(ctx, "192.0.2.77")
	if v.ClientTrusted || v.Blocklists[1].ContainsYou != true {
		t.Errorf("an unknown client: trusted %v, in list %v", v.ClientTrusted, v.Blocklists[1].ContainsYou)
	}
}

func TestSettingsViewMarksWhatIsSetHereAndWhatIsAtTheRecommendation(t *testing.T) {
	h := newProtectionHost(t)
	h.writeSys("net/netfilter/nf_conntrack_max", "1048576")
	sp := emptySpec()
	sp.Sysctls = map[string]string{"net.ipv4.tcp_syncookies": "1"}
	h.seed(t, sp)
	v, err := h.Protection(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]ProtectionSetting{}
	for _, s := range v.Settings {
		by[s.Key] = s
	}
	if s := by["net.ipv4.tcp_syncookies"]; !s.SetHere || s.Value != "1" || s.Current != "0" || s.AtRecommended || s.Kind != "choice" {
		t.Errorf("syncookies = %+v", s)
	}
	if s := by["net.netfilter.nf_conntrack_max"]; s.Recommended != "1048576" || !s.AtRecommended || s.Kind != "number" || s.Min != 65536 || len(s.Allowed) != 0 {
		t.Errorf("conntrack max = %+v", s)
	}
	b, _ := json.Marshal(by["net.ipv4.tcp_syncookies"])
	if !strings.Contains(string(b), `"allowed":["0","1"]`) {
		t.Errorf("json = %s", b)
	}
}

func TestRemoveTrusted(t *testing.T) {
	h := newGwHost(t, "10.8.0.0/16")
	ctx := context.Background()
	if _, err := h.AddLimit(ctx, LimitRequest{Name: "x", Protocol: "tcp", Ports: "22", Rate: 5, Per: "second"}, "198.51.100.44", "ops"); err != nil {
		t.Fatal(err)
	}
	sp := h.spec(t)
	sp.Trusted = append(sp.Trusted, "192.0.2.9/32")
	h.seed(t, sp)

	var g *GuardError
	if err := h.RemoveTrusted(ctx, "198.51.100.44", "198.51.100.44"); !errors.As(err, &g) {
		t.Fatalf("removing the reader's own address while nothing else trusts it: err = %v", err)
	}
	if len(h.spec(t).Trusted) != 2 {
		t.Fatal("it was removed anyway")
	}
	// Another reader may remove it.
	if err := h.RemoveTrusted(ctx, "192.0.2.9", "198.51.100.44"); err != nil {
		t.Fatal(err)
	}
	if got := h.spec(t).Trusted; len(got) != 1 || got[0] != "198.51.100.44/32" {
		t.Fatalf("trusted = %v", got)
	}
	if strings.Contains(h.loaded[len(h.loaded)-1], "192.0.2.9") {
		t.Error("the table still trusts it")
	}
	// An address the allowlist covers can be taken off the list freely: it stays trusted.
	sp = h.spec(t)
	sp.Trusted = append(sp.Trusted, "10.8.1.1/32")
	h.seed(t, sp)
	if err := h.RemoveTrusted(ctx, "10.8.1.1", "10.8.1.1"); err != nil {
		t.Fatalf("an address still trusted by the allowlist was refused: %v", err)
	}
	// What the dashboard does not keep cannot be removed from here.
	if err := h.RemoveTrusted(ctx, "10.8.0.0/16", "198.51.100.44"); !errors.Is(err, ErrNotManaged) {
		t.Errorf("allowlist range: err = %v", err)
	}
	if err := h.RemoveTrusted(ctx, "127.0.0.0/8", "198.51.100.44"); !errors.Is(err, ErrNotManaged) {
		t.Errorf("loopback: err = %v", err)
	}
	if err := h.RemoveTrusted(ctx, "203.0.113.5", "198.51.100.44"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown: err = %v", err)
	}
	if err := h.RemoveTrusted(ctx, "not-an-address", "198.51.100.44"); err == nil {
		t.Error("garbage accepted")
	}
}
