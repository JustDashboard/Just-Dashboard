package netsec

import (
	"strings"
	"testing"
)

func TestMergeBlocksFoldsEnginesByAddressAndNamesCoveringRanges(t *testing.T) {
	f2b := &Fail2banStatus{Available: true, Running: true, Jails: []Jail{
		{Name: "sshd", BannedIPs: []string{"203.0.113.9", "198.51.100.4"}},
		{Name: "recidive", BannedIPs: []string{"203.0.113.9"}},
	}}
	decisions := []CrowdSecDecision{
		{ID: 11, Origin: "crowdsec", Scenario: "crowdsecurity/ssh-bf", Scope: "Ip", Value: "203.0.113.9", Type: "ban", Until: "2026-10-07T12:00:00Z"},
		{ID: 12, Origin: "cscli", Scenario: "manual", Scope: "Range", Value: "192.0.2.0/24", Type: "ban"},
		{ID: 13, Origin: "CAPI", Scenario: "crowdsecurity/http-probing", Scope: "Ip", Value: "198.51.100.200", Type: "ban"},
		{ID: 14, Origin: "CAPI", Scenario: "crowdsecurity/ssh-bf", Scope: "Ip", Value: "198.51.100.4", Type: "ban"},
		{ID: 15, Origin: "crowdsec", Scenario: "crowdsecurity/http-bf", Scope: "Ip", Value: "192.0.2.80", Type: "captcha"},
		{ID: 16, Origin: "lists", Scenario: "firehol", Scope: "Country", Value: "XX", Type: "ban"},
	}
	fw := &FirewallStatus{Available: true, Rules: []Rule{
		{Number: 1, Action: "DENY", Direction: "IN", From: "203.0.113.0/24"},
		{Number: 2, Action: "ALLOW", Direction: "IN", From: "Anywhere", Port: "22/tcp"},
		{Number: 3, Action: "REJECT", Direction: "IN", From: "192.0.2.77", Port: "443"},
		{Number: 4, Action: "DENY", Direction: "OUT", From: "Anywhere", To: "198.51.100.9"},
		{Number: 5, Action: "DENY", Direction: "IN", From: "2001:db8:bad::1 (v6)", IPv6: true},
	}}
	v := MergeBlocks(f2b, decisions, true, fw)

	by := map[string]BlockedEntry{}
	for _, e := range v.Entries {
		by[e.Value] = e
	}
	held := by["203.0.113.9"]
	if len(held.Sources) != 3 || strings.Join(held.Engines, ",") != "crowdsec,fail2ban" || strings.Join(held.CoveredBy, ",") != "203.0.113.0/24" {
		t.Fatalf("203.0.113.9=%+v", held)
	}
	// A fail2ban ban beside a community decision is listed, with the community
	// source named; a community decision nothing else touches is counted only.
	if e := by["198.51.100.4"]; len(e.Sources) != 2 || !e.Sources[1].Community {
		t.Fatalf("198.51.100.4=%+v", e)
	}
	if _, listed := by["198.51.100.200"]; listed || v.CommunityOnly != 1 {
		t.Fatalf("community-only listed=%v count=%d", listed, v.CommunityOnly)
	}
	if e := by["192.0.2.77"]; e.Sources[0].Engine != "firewall" || e.Sources[0].Detail != "reject to port 443" || strings.Join(e.CoveredBy, ",") != "192.0.2.0/24" {
		t.Fatalf("192.0.2.77=%+v", e)
	}
	if _, ok := by["2001:db8:bad::1"]; !ok {
		t.Fatal("an IPv6 deny was not folded")
	}
	if _, ok := by["192.0.2.80"]; ok {
		t.Fatal("a captcha was listed as a block")
	}
	if _, ok := by["198.51.100.9"]; ok {
		t.Fatal("an outbound deny was listed as a refused source")
	}
	// The most redundant entry leads.
	if v.Entries[0].Value != "203.0.113.9" {
		t.Fatalf("first=%+v", v.Entries[0])
	}
	if v.Distinct != 7 || v.Duplicated != 2 || v.Covered != 2 {
		t.Fatalf("distinct=%d duplicated=%d covered=%d", v.Distinct, v.Duplicated, v.Covered)
	}
	if v.Engines[0].Count != 3 || v.Engines[1].Count != 4 || v.Engines[2].Count != 3 {
		t.Fatalf("engines=%+v", v.Engines)
	}
}

func TestMergeBlocksSaysWhichEnginesWereNotRead(t *testing.T) {
	v := MergeBlocks(&Fail2banStatus{Available: true}, nil, false, nil)
	if len(v.Entries) != 0 || v.Engines[0].Read || v.Engines[0].Note != "not running" || v.Engines[1].Read || v.Engines[2].Read || v.Engines[2].Note != "no firewall" {
		t.Fatalf("view=%+v", v)
	}
	if v := MergeBlocks(nil, nil, true, nil); v.Engines[0].Note != "not installed" || !v.Engines[1].Read {
		t.Fatalf("view=%+v", v)
	}
}

func TestBlockPrefixCanonicalisesMappedAndHostForms(t *testing.T) {
	for in, want := range map[string]string{
		"::ffff:203.0.113.9":   "203.0.113.9",
		"203.0.113.9/32":       "203.0.113.9",
		"203.0.113.77/24":      "203.0.113.0/24",
		"::ffff:192.0.2.0/120": "192.0.2.0/24",
		"2001:db8::1":          "2001:db8::1",
	} {
		p, ok := refusedPrefix(in)
		if !ok || blockValue(p) != want {
			t.Errorf("%s=%v,%v want %s", in, blockValue(p), ok, want)
		}
	}
	for _, bad := range []string{"Anywhere", "", "203.0.113.9/40"} {
		if _, ok := refusedPrefix(bad); ok {
			t.Errorf("accepted %q", bad)
		}
	}
}
