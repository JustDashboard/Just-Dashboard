package netx

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestEgressIdentitiesSeparateNICSourceFromProviderIdentity(t *testing.T) {
	rec := record(t).
		on("ip -j route get 1.1.1.1", `[{"dst":"1.1.1.1","gateway":"10.0.0.1","dev":"ens3","prefsrc":"10.0.0.5","flags":[],"uid":0,"cache":[]}]`).
		on("ip -j -6 route get 2606:4700:4700::1111", `[{"dst":"2606:4700:4700::1111","gateway":"fe80::1","dev":"ens3","prefsrc":"2001:db8:10::5","flags":[],"uid":0,"cache":[]}]`)
	rtProc(t, nil, "1", "0")
	ids := testService(t).EgressIdentities(context.Background())
	if len(ids) != 2 || len(rec.commands()) != 2 {
		t.Fatalf("both families must be read once each: %+v %v", ids, rec.commands())
	}
	v4, v6 := ids[0], ids[1]
	if v4.Family != "inet" || !v4.Forwarding || v4.Device != "ens3" || v4.Source != "10.0.0.5" || v4.SourceScope != "private" || v4.Public != "translated" {
		t.Fatalf("a private IPv4 source is translated upstream, not a public identity: %+v", v4)
	}
	if !strings.Contains(v4.Detail, "not observed") {
		t.Fatalf("the translated address must be called unobserved: %q", v4.Detail)
	}
	if v6.Family != "inet6" || v6.Forwarding || v6.Source != "2001:db8:10::5" || v6.Public != "nic" || v6.SourceScope != "public" {
		t.Fatalf("a public IPv6 source is the NIC's identity, with forwarding read separately: %+v", v6)
	}
	if !strings.Contains(v6.Detail, "not visible from this host") {
		t.Fatalf("a public NIC source must not claim the provider layers: %q", v6.Detail)
	}
}

func TestEgressIdentityFamilyFailuresStayPerFamily(t *testing.T) {
	record(t).
		on("ip -j route get 1.1.1.1", `[{"dst":"1.1.1.1","gateway":"100.64.0.1","dev":"wwan0","prefsrc":"100.72.3.4"}]`).
		fail("ip -j -6 route get", "RTNETLINK answers: Network is unreachable")
	rtProc(t, nil, "1", "")
	ids := testService(t).EgressIdentities(context.Background())
	if ids[0].SourceScope != "shared" || ids[0].Public != "translated" {
		t.Fatalf("carrier-grade NAT space is translated: %+v", ids[0])
	}
	if ids[1].Public != "no_route" || ids[1].Error != "" || ids[1].ForwardingError == "" {
		t.Fatalf("an unreachable IPv6 family is absence, its unreadable switch an error: %+v", ids[1])
	}
}

func TestJudgeIdentityKeepsUnreadableRoutesUnknown(t *testing.T) {
	for name, tc := range map[string]struct {
		raw    string
		err    error
		public string
		scope  string
	}{
		"command failed":    {"", errors.New("ip: permission denied"), "unknown", "none"},
		"unreadable output": {"not json", nil, "unknown", "none"},
		"discard route":     {`[{"dst":"1.1.1.1","type":"unreachable"}]`, nil, "no_route", "none"},
		"no source":         {`[{"dst":"1.1.1.1","dev":"ens3"}]`, nil, "unobserved", "none"},
		"unique local":      {`[{"dst":"2606:4700:4700::1111","dev":"ens3","prefsrc":"fd12::5"}]`, nil, "translated", "unique-local"},
		"link local":        {`[{"dst":"2606:4700:4700::1111","dev":"ens3","prefsrc":"fe80::5"}]`, nil, "unobserved", "link-local"},
	} {
		t.Run(name, func(t *testing.T) {
			got := judgeIdentity(EgressIdentity{Family: "inet6"}, tc.raw, tc.err)
			if got.Public != tc.public || got.SourceScope != tc.scope {
				t.Fatalf("%+v", got)
			}
			if tc.public == "unknown" && got.Error == "" {
				t.Fatal("an unreadable family must carry its error")
			}
		})
	}
}
