package netx

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

func dnsEvidenceJSON(kind string, data ...any) string {
	raw, _ := json.Marshal(map[string]any{"type": kind, "data": data})
	return string(raw)
}
func dnsEvidenceProperty(kind string, data any) any {
	return map[string]any{"type": kind, "data": data}
}
func dnsEvidenceProperties(overrides map[string]any) map[string]any {
	p := map[string]any{"ScopesMask": dnsEvidenceProperty("t", 1), "DNSSEC": dnsEvidenceProperty("s", "yes"), "DNSOverTLS": dnsEvidenceProperty("s", "yes"), "DNSSECNegativeTrustAnchors": dnsEvidenceProperty("as", []string{}), "DefaultRoute": dnsEvidenceProperty("b", true), "Name": dnsEvidenceProperty("s", "vpn0")}
	for key, value := range overrides {
		p[key] = value
	}
	return p
}
func dnsEvidenceRR(name string, kind uint16, body []byte) []byte {
	var wire []byte
	for _, label := range strings.Split(name, ".") {
		wire = append(wire, byte(len(label)))
		wire = append(wire, label...)
	}
	wire = append(wire, 0)
	wire = binary.BigEndian.AppendUint16(wire, kind)
	wire = binary.BigEndian.AppendUint16(wire, 1)
	wire = binary.BigEndian.AppendUint32(wire, 60)
	wire = binary.BigEndian.AppendUint16(wire, uint16(len(body)))
	return append(wire, body...)
}
func dnsEvidenceExecutor(t *testing.T, flags uint64, query *atomic.Int32) TrafficExecutor {
	t.Helper()
	return func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "cat" && len(args) == 1 && args[0] == resolvConfPath {
			return "nameserver 127.0.0.53\n", nil
		}
		if name == "readlink" {
			return "/usr/lib/systemd/systemd-resolved", nil
		}
		if name == "/usr/lib/systemd/systemd-resolved" {
			return "systemd 257 (257.test)", nil
		}
		if name == "ip" {
			return `[{"ifindex":7,"ifname":"vpn0"}]`, nil
		}
		if name == "busctl" && len(args) == 5 && args[3] == "tree" {
			return "/org/freedesktop/resolve1\n/org/freedesktop/resolve1/link/_37\n", nil
		}
		if name != "busctl" {
			return "", fmt.Errorf("unexpected command %s %v", name, args)
		}
		switch args[7] {
		case "GetNameOwner":
			return dnsEvidenceJSON("s", ":1.42"), nil
		case "GetConnectionCredentials":
			return dnsEvidenceJSON("a{sv}", map[string]any{"ProcessID": dnsEvidenceProperty("u", 42)}), nil
		case "GetLink":
			return dnsEvidenceJSON("o", "/org/freedesktop/resolve1/link/_37"), nil
		case "GetAll":
			if strings.HasSuffix(args[5], "/_37") {
				return dnsEvidenceJSON("a{sv}", dnsEvidenceProperties(map[string]any{"DNSEx": dnsEvidenceProperty("a(iayqs)", []any{[]any{2, []int{10, 0, 0, 53}, 0, "dns.corp.example"}}), "Domains": dnsEvidenceProperty("a(sb)", []any{[]any{"corp.example", true}})})), nil
			}
			return dnsEvidenceJSON("a{sv}", dnsEvidenceProperties(map[string]any{
				"ResolvConfMode": dnsEvidenceProperty("s", "stub"),
				"DNSEx":          dnsEvidenceProperty("a(iiayqs)", []any{[]any{0, 2, []int{8, 8, 8, 8}, 0, "dns.google"}, []any{7, 2, []int{10, 0, 0, 53}, 0, "dns.corp.example"}}),
				"Domains":        dnsEvidenceProperty("a(isb)", []any{[]any{0, ".", true}, []any{7, "corp.example", true}}),
			})), nil
		case "ResolveRecord":
			query.Add(1)
			if args[4] != ":1.42" || args[8] != "isqqt" || args[10] != "secret.corp.example." || args[len(args)-1] != fmt.Sprint(dnsFreshFlags) {
				t.Errorf("wrong native query: %v", args)
			}
			return dnsEvidenceJSON("a(iqqay)t", []any{[]any{7, 1, 1, dnsEvidenceRR("secret.corp.example", 1, []byte{192, 0, 2, 7})}}, flags), nil
		}
		return "", fmt.Errorf("unexpected method %v", args)
	}
}
func TestDNSEvidenceNativeRouteTrustAndFreshness(t *testing.T) {
	for _, test := range []struct {
		name                            string
		flags                           uint64
		transport, dnssec, trust, route string
	}{
		{"fresh validated encrypted", dnsFlagDNS | dnsFlagNetwork | dnsFlagAuthenticated | dnsFlagConfidential, "encrypted", "validated", "native_policy_validated", "measured"},
		{"configured without encrypted result", dnsFlagDNS | dnsFlagNetwork, "unencrypted", "not_authenticated", "unknown", "measured"},
		{"cached authenticated is not validation", dnsFlagDNS | 1<<20 | dnsFlagAuthenticated | dnsFlagConfidential, "unknown", "unknown", "unknown", "modeled"},
		{"local synthetic is not TLS or DNSSEC", dnsFlagDNS | 1<<19 | dnsFlagAuthenticated | dnsFlagConfidential, "unknown", "unknown", "unknown", "modeled"},
		{"network cache mixture is not fresh", dnsFlagDNS | dnsFlagNetwork | 1<<20 | dnsFlagAuthenticated | dnsFlagConfidential, "unknown", "unknown", "unknown", "modeled"},
		{"unknown native output cannot prove trust", dnsFlagDNS | dnsFlagNetwork | 1<<27 | dnsFlagAuthenticated | dnsFlagConfidential, "unknown", "unknown", "unknown", "modeled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var queries atomic.Int32
			r, err := (&Service{}).InvestigateDNS(context.Background(), DNSInvestigationRequest{Name: "secret.corp.example", Type: "A"}, dnsEvidenceExecutor(t, test.flags, &queries))
			if err != nil {
				t.Fatal(err)
			}
			if r.Error != "" || r.Transport.State != test.transport || r.DNSSEC.State != test.dnssec || r.Trust.State != test.trust || r.Route.State != test.route || len(r.Policy) != 1 || r.Policy[0].Index != 7 || len(r.Answers) != 1 || queries.Load() != 1 || r.EndedAt.IsZero() {
				t.Fatalf("report: %+v", r)
			}
			if r.AnswerFamily != "inet" || r.UpstreamFamily != "not_measured" || r.NSS.State != "not_measured" {
				t.Fatalf("invented scope: %+v", r)
			}
		})
	}
}
func TestDNSEvidenceWrongLinkAndUnreadablePolicySendNothing(t *testing.T) {
	var queries atomic.Int32
	run := dnsEvidenceExecutor(t, dnsFlagDNS|dnsFlagNetwork, &queries)
	_, err := (&Service{}).InvestigateDNS(context.Background(), DNSInvestigationRequest{Name: "secret.corp.example", Type: "A", ExpectedInterface: "eth0"}, run)
	if err == nil || queries.Load() != 0 {
		t.Fatalf("wrong link accepted: %v", err)
	}
	for _, failure := range []string{"GetNameOwner", "GetAll"} {
		t.Run(failure, func(t *testing.T) {
			r, err := (&Service{}).InvestigateDNS(context.Background(), DNSInvestigationRequest{Name: "secret.corp.example", Type: "A"}, func(ctx context.Context, name string, args ...string) (string, error) {
				if name == "busctl" && len(args) > 7 && args[7] == failure {
					return "", errors.New("unavailable")
				}
				return run(ctx, name, args...)
			})
			if err != nil || r.Error == "" || queries.Load() != 0 {
				t.Fatalf("private fallback: %+v %v", r, err)
			}
		})
	}
}

func TestDNSEvidenceUnavailableDeclaredPrivateScopeSendsNothing(t *testing.T) {
	for _, mode := range []string{"inactive", "no servers", "scope unreadable"} {
		t.Run(mode, func(t *testing.T) {
			var queries atomic.Int32
			run := dnsEvidenceExecutor(t, dnsFlagDNS|dnsFlagNetwork, &queries)
			r, err := (&Service{}).InvestigateDNS(t.Context(), DNSInvestigationRequest{Name: "secret.corp.example", Type: "A"}, func(ctx context.Context, name string, args ...string) (string, error) {
				out, err := run(ctx, name, args...)
				if name != "busctl" || len(args) <= 7 || args[7] != "GetAll" {
					return out, err
				}
				if strings.HasSuffix(args[5], "/_37") {
					switch mode {
					case "inactive":
						out = strings.ReplaceAll(out, `"ScopesMask":{"data":1,"type":"t"}`, `"ScopesMask":{"data":0,"type":"t"}`)
					case "scope unreadable":
						out = strings.ReplaceAll(out, `"ScopesMask":{"data":1,"type":"t"}`, `"ScopesMask":{"data":1,"type":"s"}`)
					case "no servers":
						out = strings.ReplaceAll(out, `[[2,[10,0,0,53],0,"dns.corp.example"]]`, `[]`)
					}
				} else if mode == "no servers" {
					out = strings.ReplaceAll(out, `,[7,2,[10,0,0,53],0,"dns.corp.example"]`, "")
				}
				return out, err
			})
			if err != nil || r.Error == "" || queries.Load() != 0 {
				t.Fatalf("unavailable private scope fell back: %+v %v queries=%d", r, err, queries.Load())
			}
			if mode != "scope unreadable" && (len(r.Policy) != 1 || r.Policy[0].Index != 7) {
				t.Fatalf("unavailable declared policy was discarded: %+v", r.Policy)
			}
		})
	}
}
func TestDNSEvidenceOpportunisticEncryptionDoesNotProveTrust(t *testing.T) {
	r := &DNSInvestigation{Policy: []DNSPolicyScope{{Index: 7, DNSOverTLS: "opportunistic"}}, AnswerInterfaces: []int{7}}
	dnsInterpretNative(r, dnsFlagDNS|dnsFlagNetwork|dnsFlagConfidential)
	if r.Transport.State != "encrypted" || r.Trust.State != "unknown" {
		t.Fatalf("opportunistic claimed trust: %+v", r)
	}
}
func TestDNSEvidenceOwnerChangeDiscardsTrust(t *testing.T) {
	var queries atomic.Int32
	run := dnsEvidenceExecutor(t, dnsFlagDNS|dnsFlagNetwork|dnsFlagAuthenticated|dnsFlagConfidential, &queries)
	r, err := (&Service{}).InvestigateDNS(context.Background(), DNSInvestigationRequest{Name: "secret.corp.example", Type: "A"}, func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "busctl" && len(args) > 7 && args[7] == "GetNameOwner" && queries.Load() > 0 {
			return dnsEvidenceJSON("s", ":1.43"), nil
		}
		return run(ctx, name, args...)
	})
	if err != nil || !strings.Contains(r.Error, "identity changed") || r.Trust.State != "unknown" || r.DNSSEC.State != "unknown" || len(r.Answers) != 0 {
		t.Fatalf("changed owner retained trust: %+v %v", r, err)
	}
}
func TestDNSEvidenceMalformedNativeRecordRefused(t *testing.T) {
	for _, body := range []string{`{"type":"a(iqqay)t","data":[[],1]}`, `{"type":"a(iqqay)t","data":[[[7,1,1,[192,12,0,1]]],8389121]}`} {
		r := &DNSInvestigation{}
		if _, err := decodeDNSNativeRecords(body, "secret.corp.example", "A", r); err == nil {
			t.Fatal("malformed record accepted")
		}
	}
}
func TestDNSEvidenceRetentionInterruptedAndImmutableScope(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.DB.Close()
	if err = store.InitializeNetworkDNSEvidence(context.Background(), st.DB); err != nil {
		t.Fatal(err)
	}
	s := &Service{db: st.DB}
	var queries atomic.Int32
	record, err := s.CreateDNSEvidence(context.Background(), DNSInvestigationRequest{Name: "secret.corp.example", Type: "a"}, "admin", dnsEvidenceExecutor(t, dnsFlagDNS|dnsFlagNetwork, &queries))
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != "completed" || record.Result == nil || record.Result.Request.Type != "A" {
		t.Fatalf("saved: %+v", record)
	}
	rows, err := s.ListDNSEvidence(context.Background())
	if err != nil || len(rows) != 1 || rows[0].Result != nil {
		t.Fatalf("list leaked artifact: %+v %v", rows, err)
	}
	got, err := s.DNSEvidence(context.Background(), record.ID)
	if err != nil || got.Result == nil || got.Result.NativeFlags == "" {
		t.Fatalf("lost typed artifact: %+v %v", got, err)
	}
	if _, err = st.DB.Exec(`UPDATE network_dns_evidence SET request_json='{}' WHERE id=?`, record.ID); err == nil {
		t.Fatal("scope was mutable")
	}
	if _, err = st.DB.Exec(`UPDATE network_dns_evidence SET status='running',artifact_json='',ended_at=0 WHERE id=?`, record.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.ReconcileDNSEvidence(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err = s.DNSEvidence(context.Background(), record.ID)
	if err != nil || got.Status != "interrupted" || got.Result != nil || queries.Load() != 1 {
		t.Fatalf("restart reran/lied: %+v %v queries=%d", got, err, queries.Load())
	}
	if err = s.DeleteDNSEvidence(context.Background(), record.ID); err != nil {
		t.Fatal(err)
	}
}

func TestDNSEvidencePolicyChangeKeepsEncryptionButNotStrictTrust(t *testing.T) {
	var queries atomic.Int32
	run := dnsEvidenceExecutor(t, dnsFlagDNS|dnsFlagNetwork|dnsFlagConfidential, &queries)
	r, err := (&Service{}).InvestigateDNS(context.Background(), DNSInvestigationRequest{Name: "secret.corp.example", Type: "A"}, func(ctx context.Context, name string, args ...string) (string, error) {
		out, err := run(ctx, name, args...)
		if name == "busctl" && len(args) > 7 && args[7] == "GetAll" && strings.HasSuffix(args[5], "/_37") && queries.Load() > 0 {
			out = strings.ReplaceAll(out, `"data":"yes","type":"s"`, `"data":"opportunistic","type":"s"`)
		}
		return out, err
	})
	if err != nil || r.PolicyStable || r.Transport.State != "encrypted" || r.Trust.State != "unknown" {
		t.Fatalf("changed snapshot proved certificate policy: %+v %v", r, err)
	}
}

func TestDNSEvidenceNativeVersionAndOtherOwnersRefuseWithoutQueries(t *testing.T) {
	for _, test := range []struct{ version, exe string }{{"systemd 255", "/usr/lib/systemd/systemd-resolved"}, {"systemd 257", "/usr/bin/other-resolver"}} {
		var queries atomic.Int32
		run := dnsEvidenceExecutor(t, dnsFlagDNS|dnsFlagNetwork, &queries)
		r, err := (&Service{}).InvestigateDNS(context.Background(), DNSInvestigationRequest{Name: "secret.corp.example", Type: "A"}, func(ctx context.Context, name string, args ...string) (string, error) {
			if name == "readlink" {
				return test.exe, nil
			}
			if name == "/usr/lib/systemd/systemd-resolved" {
				return test.version, nil
			}
			return run(ctx, name, args...)
		})
		if err != nil || r.Error == "" || queries.Load() != 0 {
			t.Fatalf("unsupported owner/version queried: %+v %v", r, err)
		}
	}
}

func TestDNSEvidenceConcurrentAdmissionAndBoundedRetention(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.DB.Close()
	if err = store.InitializeNetworkDNSEvidence(t.Context(), st.DB); err != nil {
		t.Fatal(err)
	}
	s := &Service{db: st.DB}
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	failures := make(chan error, 4)
	var wg sync.WaitGroup
	runner := func(ctx context.Context, name string, args ...string) (string, error) {
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return "", errors.New("no native owner")
	}
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.CreateDNSEvidence(t.Context(), DNSInvestigationRequest{Name: "secret.corp.example", Type: "A"}, "admin", runner)
			failures <- err
		}()
	}
	for range 4 {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			close(release)
			wg.Wait()
			t.Fatal("concurrent admission did not start")
		}
	}
	if _, err = s.CreateDNSEvidence(t.Context(), DNSInvestigationRequest{Name: "secret.corp.example", Type: "A"}, "admin", func(context.Context, string, ...string) (string, error) {
		t.Error("fifth request executed")
		return "", nil
	}); err == nil {
		t.Fatal("fifth native investigation admitted")
	}
	close(release)
	wg.Wait()
	for range 4 {
		if err := <-failures; err != nil {
			t.Fatal(err)
		}
	}
	for i := range 140 {
		id := fmt.Sprintf("%032x", i+100)
		_, err = st.DB.Exec(`INSERT INTO network_dns_evidence(id,request_json,started_at,ended_at,status) VALUES(?,'{"name":"history.corp.example","type":"A"}',?,?,'completed')`, id, time.Now().Add(-time.Duration(i)*time.Second).UnixMilli(), time.Now().UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.ListDNSEvidence(t.Context())
	if err != nil || len(rows) != 128 {
		t.Fatalf("retention bound %d %v", len(rows), err)
	}
	_, err = st.DB.Exec(`INSERT INTO network_dns_evidence(id,request_json,started_at,ended_at,status) VALUES(?,'{"name":"expired.corp.example","type":"A"}',?,?,'completed')`, strings.Repeat("f", 32), time.Now().Add(-8*24*time.Hour).UnixMilli(), time.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DNSEvidence(t.Context(), strings.Repeat("f", 32)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired private artifact remained readable: %v", err)
	}
}

func TestDNSEvidenceBusEscapesAndGlobalEndpointScopes(t *testing.T) {
	for path, want := range map[string]int{"/org/freedesktop/resolve1/link/_37": 7, "/org/freedesktop/resolve1/link/_3123": 123} {
		index, err := dnsNativeLinkIndex(path)
		if err != nil || index != want {
			t.Fatalf("native object identity %s: %d %v", path, index, err)
		}
	}
	var queries atomic.Int32
	run := dnsEvidenceExecutor(t, dnsFlagDNS|dnsFlagNetwork, &queries)
	r, err := (&Service{}).InvestigateDNS(context.Background(), DNSInvestigationRequest{Name: "secret.corp.example", Type: "A"}, func(ctx context.Context, name string, args ...string) (string, error) {
		out, err := run(ctx, name, args...)
		if name == "busctl" && len(args) > 7 && args[7] == "GetAll" && args[5] == dnsNativePath {
			out = strings.ReplaceAll(out, `[7,2,[10,0,0,53]`, `[1,2,[127,0,0,2]`)
		}
		if name == "busctl" && len(args) > 7 && args[7] == "GetAll" && strings.HasSuffix(args[5], "/_37") {
			out = strings.ReplaceAll(out, `[2,[10,0,0,53]`, `[2,[127,0,0,2]`)
		}
		return out, err
	})
	if err != nil || r.Error != "" || len(r.Policy) != 1 || r.Policy[0].Index != 7 {
		t.Fatalf("address scope became policy owner: %+v %v", r, err)
	}
}
