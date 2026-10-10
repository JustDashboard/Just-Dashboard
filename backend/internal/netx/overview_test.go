package netx

import (
	"strings"
	"testing"
)

func TestOverviewForwardingFindingsRespectAddressFamily(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sp       *Spec
		switches ForwardingSwitches
		want     string
	}{
		{"IPv6 on with IPv4 off", &Spec{Forwards: []ForwardSpec{{Target: "2001:db8::1", Enabled: true}}}, ForwardingSwitches{IPv6: true}, ""},
		{"IPv6 off with IPv4 on", &Spec{NAT: []NATSpec{{Source: "fd00::/64", Enabled: true}}}, ForwardingSwitches{IPv4: true}, "forwarding.off.ipv6"},
		{"IPv4 off", &Spec{NAT: []NATSpec{{Source: "10.0.0.0/24", Enabled: true}}}, ForwardingSwitches{IPv6: true}, "forwarding.off.ipv4"},
		{"disabled entry", &Spec{Forwards: []ForwardSpec{{Target: "2001:db8::1"}}}, ForwardingSwitches{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			findings := OverviewFindings(OverviewInput{Spec: tc.sp, Forwarding: tc.switches})
			got := ""
			for _, f := range findings {
				if strings.HasPrefix(f.ID, "forwarding.off") {
					got = f.ID
				}
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOverviewFailedReadingsAreNotJudgedAsAbsence(t *testing.T) {
	uplink := Link{Name: "ens3", Uplink: true, AdminUp: true, Carrier: true}
	failed := []Observation{
		{Source: "firewall", Label: "The host firewall", State: "failed", Error: "ufw status: timed out", Href: "/network/firewall"},
		{Source: "history", Label: "Recorded interface errors", State: "failed", Error: "database is locked", Href: "/network/traffic"},
		{Source: "forwarding", Label: "The forwarding switches", State: "failed", Error: "permission denied", Href: "/network/routing"},
	}
	findings := OverviewFindings(OverviewInput{
		Links:             []Link{uplink},
		Spec:              &Spec{NAT: []NATSpec{{Source: "10.0.0.0/24", Enabled: true}}},
		LinkHistoryErrors: map[string]uint64{"ens3": 40},
		FirewallAvailable: false,
		ConntrackPercent:  -1,
		Observations:      failed,
	})
	ids := map[string]Finding{}
	for _, f := range findings {
		ids[f.ID] = f
	}
	for _, absent := range []string{"firewall.none", "firewall.off", "link.errors.ens3", "forwarding.off.ipv4"} {
		if _, ok := ids[absent]; ok {
			t.Fatalf("%s was judged from a failed reading: %+v", absent, findings)
		}
	}
	for _, o := range failed {
		f, ok := ids["observation."+o.Source]
		if !ok || f.Source != o.Source || f.Href != o.Href || !strings.Contains(f.Detail, o.Error) || !strings.Contains(f.Detail, "not evidence") {
			t.Fatalf("a failed %s reading must be its own finding: %+v", o.Source, findings)
		}
	}
	ok := OverviewFindings(OverviewInput{Links: []Link{uplink}, LinkHistoryErrors: map[string]uint64{"ens3": 40}, ConntrackPercent: -1})
	found := false
	for _, f := range ok {
		if f.ID == "firewall.none" && f.Source == "firewall" {
			found = true
		}
		if f.ID == "link.errors.ens3" && (f.Source != "history" || f.Href != "/network/interfaces?device=ens3") {
			t.Fatalf("an uplink's errors open its own sheet: %+v", f)
		}
	}
	if !found {
		t.Fatalf("a successful reading still reports an absent firewall: %+v", ok)
	}
}
