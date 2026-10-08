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
