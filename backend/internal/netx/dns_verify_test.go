package netx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCleanDNSSettingsClearsAndVerificationNames(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		req     DNSSettings
		wantErr string
	}{
		{"clear fallback alone", DNSSettings{Clear: []string{"fallback"}}, ""},
		{"clear every list", DNSSettings{Clear: []string{"servers", "fallback", "domains"}, DNSSEC: "yes"}, ""},
		{"cleared and listed", DNSSettings{Fallback: []string{"9.9.9.9"}, Clear: []string{"fallback"}}, "both listed and cleared"},
		{"unknown list", DNSSettings{Clear: []string{"cache"}}, "servers, fallback or domains"},
		{"required TLS with cleared servers", DNSSettings{Clear: []string{"servers"}, DNSOverTLS: "yes"}, "needs upstreams"},
		{"nine verification names", DNSSettings{Servers: []string{"1.1.1.1"}, VerificationNames: []string{"a.lan", "b.lan", "c.lan", "d.lan", "e.lan", "f.lan", "g.lan", "h.lan", "i.lan"}}, "at most 8"},
		{"bad verification name", DNSSettings{Servers: []string{"1.1.1.1"}, VerificationNames: []string{"bad name"}}, "verification name"},
		{"address as verification name", DNSSettings{Servers: []string{"1.1.1.1"}, VerificationNames: []string{"192.0.2.1"}}, "verification name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := cleanDNSSettings(tc.req)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}
		})
	}
	got, err := cleanDNSSettings(DNSSettings{Servers: []string{"1.1.1.1"}, VerificationName: "NAS.home.arpa.", VerificationNames: []string{"nas.home.arpa", " git.corp.example "}, Clear: []string{"Fallback", "fallback"}})
	if err != nil || !reflect.DeepEqual(got.VerificationNames, []string{"nas.home.arpa", "git.corp.example"}) || got.VerificationName != "" || !reflect.DeepEqual(got.Clear, []string{"fallback"}) {
		t.Fatalf("normalised = %+v, %v", got, err)
	}
}

// An empty assignment that stays empty is a cleared list, which reads back as
// cleared, not as a list left to the host.
func TestRenderResolvedClearsLists(t *testing.T) {
	text := renderResolved(DNSSettings{Servers: []string{"192.0.2.53"}, Clear: []string{"fallback", "domains"}}, "")
	if !strings.Contains(text, "DNS=\nDNS=192.0.2.53\nFallbackDNS=\nDomains=\n") || strings.Contains(text, "FallbackDNS=1") {
		t.Fatalf("drop-in:\n%s", text)
	}
	path := filepath.Join(t.TempDir(), "90.conf")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	m := readManagedDNS(path)
	if !reflect.DeepEqual(m.Cleared, []string{"fallback", "domains"}) || len(m.Fallback) != 0 || !reflect.DeepEqual(m.Servers, []string{"192.0.2.53"}) {
		t.Fatalf("read back = %+v", m)
	}
	// Inherit: nothing about the list is written at all.
	if text := renderResolved(DNSSettings{Servers: []string{"192.0.2.53"}}, ""); strings.Contains(text, "FallbackDNS") {
		t.Fatalf("inherited fallback was written:\n%s", text)
	}
	if m := readManagedDNS(filepath.Join(t.TempDir(), "absent")); m.Cleared == nil {
		t.Fatal("cleared must be an array")
	}
}

func TestPlanDNSVerificationNamesEveryScope(t *testing.T) {
	t.Parallel()
	rv := ResolvedView{Active: true, Links: []ResolvedLink{
		{Name: "tailscale0", Index: 4, Scopes: []string{"DNS"}, ResolvedScope: ResolvedScope{Servers: []string{"100.100.100.100"}, Domains: []string{"~ts.net"}}},
		{Name: "ens3", Index: 2, Scopes: []string{"DNS"}, DefaultRoute: true, ResolvedScope: ResolvedScope{Servers: []string{"198.51.100.2"}}},
	}}
	req, err := cleanDNSSettings(DNSSettings{
		Servers: []string{"10.0.0.53"}, Domains: []string{"~corp.example", "~lab.example"}, Fallback: []string{"9.9.9.9"},
		DNSOverTLS: "opportunistic", DNSSEC: "allow-downgrade", Cache: "no-negative",
		VerificationNames: []string{"git.corp.example", "box.tail1.ts.net"},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := planDNSVerification(req, rv)
	scopes := map[string]string{}
	required := map[string]bool{}
	for _, c := range plan.Checks {
		scopes[c.Kind+" "+c.Name] = c.Scope
		required[c.Kind+" "+c.Name] = c.Required
		if c.State != "planned" {
			t.Fatalf("a plan check is not planned: %+v", c)
		}
	}
	if scopes["resolution git.corp.example"] != "~corp.example on global upstreams" || scopes["resolution box.tail1.ts.net"] != "~ts.net on tailscale0" {
		t.Fatalf("scopes = %v", scopes)
	}
	if !required["readback "] || !required["resolution git.corp.example"] || required["transport git.corp.example"] || required["dnssec git.corp.example"] {
		t.Fatalf("required = %v", required)
	}
	joined := strings.Join(plan.Unverified, "\n")
	for _, want := range []string{"~lab.example (routing domain set here)", "default route through the global upstreams", "fallback servers", "inherit the global one", "cache mode"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("unverified = %q, want %q", joined, want)
		}
	}
	if strings.Contains(joined, "~corp.example") {
		t.Fatalf("a covered routing domain is listed as unverified: %q", joined)
	}

	// Without names, the public names check the default route; required TLS
	// makes the transport check a required one.
	strict, _ := cleanDNSSettings(DNSSettings{Servers: []string{"1.1.1.1#cloudflare-dns.com"}, DNSOverTLS: "yes"})
	plan = planDNSVerification(strict, ResolvedView{Active: true})
	if len(plan.Checks) != 3 || plan.Checks[1].Scope != "default route via global upstreams" || !plan.Checks[2].Required || plan.Checks[2].Kind != "transport" || len(plan.Unverified) != 0 {
		t.Fatalf("strict plan = %+v", plan)
	}
}

const readbackStatus = `Global
         Protocols: -LLMNR -mDNS +DNSOverTLS DNSSEC=no/unsupported
  resolv.conf mode: stub
       DNS Servers: 1.1.1.1#cloudflare-dns.com 1.0.0.1#cloudflare-dns.com
`

func TestSetDNSReadsBackTheRunningSettings(t *testing.T) {
	s, rec := resolvedHost(t, answerA("192.0.2.1"))
	rec.replies = append([]reply{{prefix: "resolvectl status --no-pager", out: readbackStatus}}, rec.replies...)
	got, err := s.SetDNS(context.Background(), DNSSettings{Servers: []string{"1.1.1.1#cloudflare-dns.com", "1.0.0.1#cloudflare-dns.com"}, DNSOverTLS: "yes"}, "")
	if err != nil {
		t.Fatal(err)
	}
	checks := got.Verification.Checks
	if checks[0].Kind != "readback" || checks[0].State != "passed" || checks[1].State != "passed" || checks[1].Name != "cloudflare.com" || checks[1].Answer != "192.0.2.1" || checks[1].Type != "A" {
		t.Fatalf("checks = %+v", checks)
	}
	// The native adapter is unavailable here: the transport stays unknown, which
	// is reported and does not fail the change.
	if checks[2].Kind != "transport" || checks[2].State != "unknown" || !got.Verified || got.Via != "cloudflare.com" {
		t.Fatalf("transport = %+v / %+v", checks[2], got)
	}
}

// A later drop-in overriding the one written here leaves resolved running with
// other settings: the change is refused and put back.
func TestSetDNSRollsBackWhenAnotherDropInOverridesIt(t *testing.T) {
	s, rec := resolvedHost(t, answerA("192.0.2.1"))
	rec.replies = append([]reply{{prefix: "resolvectl status --no-pager", out: "Global\n       DNS Servers: 9.9.9.9\n Fallback DNS Servers: 1.1.1.1\n"}}, rec.replies...)
	_, err := s.SetDNS(context.Background(), DNSSettings{Servers: []string{"192.0.2.53"}, Clear: []string{"fallback"}}, "")
	var up *UpstreamError
	if !errors.As(err, &up) || !strings.Contains(up.Reason, "not running with the settings written") || !strings.Contains(up.Reason, "global servers are 9.9.9.9 where 192.0.2.53 was written") ||
		!strings.Contains(up.Reason, "fallback servers are 1.1.1.1 where none was written") || !strings.Contains(up.Reason, "previous settings were put back") {
		t.Fatalf("error = %v", err)
	}
	if up.Verification == nil || up.Verification.Checks[0].State != "failed" {
		t.Fatalf("verification = %+v", up.Verification)
	}
	if _, statErr := os.Stat(s.paths.Resolved); !errors.Is(statErr, os.ErrNotExist) || countRestarts(rec) != 2 {
		t.Fatalf("not rolled back: %v %v", statErr, rec.commands())
	}
}

// Every verification name is checked; one that does not answer in its own
// scope rolls the change back even though another answered.
func TestSetDNSChecksEveryVerificationName(t *testing.T) {
	s, rec := resolvedHost(t, func(name string, qtype uint16) dnsBehavior {
		if name == "nas.home.arpa" && qtype == typeA {
			return dnsBehavior{rdatas: [][]byte{aData("192.168.1.7")}}
		}
		return dnsBehavior{rcode: 3}
	})
	_, err := s.SetDNS(context.Background(), DNSSettings{Servers: []string{"192.168.1.53"}, Domains: []string{"~corp.example"}, VerificationNames: []string{"nas.home.arpa", "git.corp.example"}}, "")
	var up *UpstreamError
	if !errors.As(err, &up) || !strings.Contains(up.Reason, "git.corp.example could not be resolved") {
		t.Fatalf("error = %v", err)
	}
	checks := up.Verification.Checks
	if checks[1].Name != "nas.home.arpa" || checks[1].State != "passed" || checks[2].Name != "git.corp.example" || checks[2].State != "failed" || checks[2].Scope != "~corp.example on global upstreams" {
		t.Fatalf("checks = %+v", checks)
	}
	if countRestarts(rec) != 2 {
		t.Fatalf("restarts = %v", rec.commands())
	}
}

func TestSetDNSTransportCheck(t *testing.T) {
	for _, tc := range []struct {
		name, mode, want string
		flags            uint64
		rolledBack       bool
	}{
		{"required TLS carried the answer", "yes", "passed", dnsFlagDNS | dnsFlagNetwork | dnsFlagConfidential | dnsFlagAuthenticated, false},
		{"required TLS was not used", "yes", "failed", dnsFlagDNS | dnsFlagNetwork, true},
		{"opportunistic fell back", "opportunistic", "warning", dnsFlagDNS | dnsFlagNetwork, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, rec := resolvedHost(t, func(name string, qtype uint16) dnsBehavior {
				if name == "secret.corp.example" && qtype == typeA {
					return dnsBehavior{rdatas: [][]byte{aData("192.0.2.7")}}
				}
				return dnsBehavior{rcode: 3}
			})
			var queries atomic.Int32
			dnsVerifyExecutor = dnsEvidenceExecutor(t, tc.flags, &queries)
			servers := []string{"10.0.0.53#dns.corp.example"}
			got, err := s.SetDNS(context.Background(), DNSSettings{Servers: servers, DNSOverTLS: tc.mode, DNSSEC: "allow-downgrade", VerificationNames: []string{"secret.corp.example"}}, "")
			var verification *DNSVerification
			if tc.rolledBack {
				var up *UpstreamError
				if !errors.As(err, &up) || !strings.Contains(up.Reason, "Required DNS over TLS did not carry the answer") || countRestarts(rec) != 2 {
					t.Fatalf("error = %v, restarts %v", err, rec.commands())
				}
				verification = up.Verification
			} else {
				if err != nil {
					t.Fatal(err)
				}
				verification = got.Verification
			}
			var transport, dnssec DNSVerificationCheck
			for _, c := range verification.Checks {
				switch c.Kind {
				case "transport":
					transport = c
				case "dnssec":
					dnssec = c
				}
			}
			if transport.State != tc.want || transport.Name != "secret.corp.example" || queries.Load() != 1 {
				t.Fatalf("transport = %+v (native queries %d)", transport, queries.Load())
			}
			wantDNSSEC := "warning"
			if tc.flags&dnsFlagAuthenticated != 0 {
				wantDNSSEC = "passed"
			}
			if dnssec.State != wantDNSSEC {
				t.Fatalf("dnssec = %+v", dnssec)
			}
		})
	}
}

func TestResetDNSReportsItsCheck(t *testing.T) {
	s, _ := resolvedHost(t, answerA("192.0.2.1"))
	if err := writeFileAtomic(s.paths.Resolved, []byte("[Resolve]\nDNS=9.9.9.9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := s.ResetDNS(context.Background())
	if err != nil || got.Verification == nil || got.Verification.Checks[0].State != "passed" || !got.Verified || got.Via != "cloudflare.com" {
		t.Fatalf("reset = %+v, %v", got, err)
	}
}

func TestPlanDNSValidatesFirst(t *testing.T) {
	rec := record(t)
	s := testService(t)
	if _, err := s.PlanDNS(context.Background(), DNSSettings{Fallback: []string{"9.9.9.9"}, Clear: []string{"fallback"}}); err == nil {
		t.Fatal("an invalid plan was made")
	}
	if len(rec.commands()) != 0 {
		t.Fatalf("an invalid plan asked the host: %v", rec.commands())
	}
}

// resolvectl lists a link's loopback-addressed server under Global as well;
// the read-back belongs that entry to its link, not to the drop-in.
func TestReadbackDiscountsALinksLoopbackServer(t *testing.T) {
	rec := record(t)
	rec.on("resolvectl status --no-pager", `Global
       DNS Servers: 203.0.113.53#resolver.example 127.0.0.2#resolver.example
        DNS Domain: ~.
Link 8 (wg0)
    Current Scopes: DNS
       DNS Servers: 127.0.0.2#resolver.example
        DNS Domain: ~corp.example
`)
	req, _ := cleanDNSSettings(DNSSettings{Servers: []string{"203.0.113.53#resolver.example"}, Domains: []string{"~."}})
	if state, detail := readbackDNS(context.Background(), req); state != "passed" {
		t.Fatalf("read-back = %s %s", state, detail)
	}
	// A loopback server written on purpose is still compared.
	req, _ = cleanDNSSettings(DNSSettings{Servers: []string{"203.0.113.53#resolver.example", "127.0.0.2#resolver.example"}})
	if state, _ := readbackDNS(context.Background(), req); state != "passed" {
		t.Fatalf("a written loopback server was discounted: %s", state)
	}
	req, _ = cleanDNSSettings(DNSSettings{Servers: []string{"198.51.100.53"}})
	if state, detail := readbackDNS(context.Background(), req); state != "failed" || !strings.Contains(detail, "203.0.113.53#resolver.example where 198.51.100.53") {
		t.Fatalf("a different server passed: %s %s", state, detail)
	}
}
