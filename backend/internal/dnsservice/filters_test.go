package dnsservice

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func filterFixture(t *testing.T, engine Engine, policy map[string]any) (ConnectionRequest, *atomic.Int32) {
	t.Helper()
	mutations := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Errorf("unexpected native query: %s", r.URL.Path)
		}
		var value any
		path := r.URL.Path
		if engine == PiHole && path == "/api/auth" {
			if r.Method == "POST" {
				value = map[string]any{"session": map[string]any{"valid": true, "sid": "fixture-filter-session-secret"}}
			} else if r.Method == "DELETE" {
				value = map[string]any{}
			} else {
				t.Error("unsupported authentication method")
			}
		} else {
			if (engine == Technitium && r.Method != "POST") || (engine != Technitium && r.Method != "GET") {
				mutations.Add(1)
			}
			switch engine {
			case AdGuard:
				user, password, ok := r.BasicAuth()
				if !ok || user != "fixture-user" || password != "fixture-filter-password" {
					t.Error("missing native basic authentication")
				}
			case PiHole:
				if r.Header.Get("X-FTL-SID") != "fixture-filter-session-secret" {
					t.Error("missing pinned session header")
				}
			case Technitium:
				if r.Header.Get("Authorization") != "Bearer fixture-filter-token" {
					t.Error("missing native bearer header")
				}
			}
			value = policy[path]
			if value == nil {
				http.Error(w, "private-error?password=do-not-echo", 500)
				return
			}
			if engine == Technitium {
				value = map[string]any{"status": "ok", "response": value}
			}
		}
		json.NewEncoder(w).Encode(value)
	}))
	t.Cleanup(server.Close)
	req := ConnectionRequest{Name: "Filter fixture", Engine: engine, Endpoint: server.URL}
	switch engine {
	case AdGuard:
		req.Credential = Credential{Username: "fixture-user", Password: "fixture-filter-password"}
	case PiHole:
		req.Credential = Credential{Password: "fixture-filter-password"}
	case Technitium:
		req.Credential = Credential{Token: "fixture-filter-token"}
	}
	return req, mutations
}

func filterAdGuardStatus() map[string]any {
	return map[string]any{"version": "v0.107.71", "running": true, "protection_enabled": false}
}

func filterAdGuardPolicy() map[string]any {
	return map[string]any{"enabled": true, "interval": 24, "filters": []any{map[string]any{"id": 1, "enabled": false, "name": "private-name?key=secret", "url": "https://user:URL-password@filters.example/private-secret?token=query-secret#fragment-secret", "rules_count": 12, "last_updated": "2026-10-09T12:00:00Z", "futureField": "native-unrepresented"}}, "whitelist_filters": []any{}, "user_rules": []string{"@@||private-rule-secret.example^"}}
}

func TestDNSFilterAdGuardNativeMetadataAndRawDrift(t *testing.T) {
	policy := filterAdGuardPolicy()
	req, mutations := filterFixture(t, AdGuard, map[string]any{"/control/status": filterAdGuardStatus(), "/control/filtering/status": policy})
	first, err := inspectNativeFilters(t.Context(), req)
	if err != nil || first.Sources.Evidence.State != "configured" || first.Rules.Evidence.State != "configured" || first.Fingerprint == "" || len(first.Sources.Entries) != 1 || first.Sources.Entries[0].Origin != "https://filters.example" || *first.Sources.Entries[0].Enabled || *first.Protection || !*first.Filtering {
		t.Fatalf("native metadata lost configuration/runtime distinction: %+v %v", first, err)
	}
	encoded, _ := json.Marshal(first)
	for _, secret := range []string{"URL-password", "private-secret", "query-secret", "fragment-secret", "private-rule-secret", "private-name", "native-unrepresented", "fixture-filter-password"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("filter response exposed %s", secret)
		}
	}
	entry := policy["filters"].([]any)[0].(map[string]any)
	entry["futureField"] = "changed"
	second, err := inspectNativeFilters(t.Context(), req)
	if err != nil || first.Fingerprint == second.Fingerprint || first.Sources.Entries[0].Fingerprint == second.Sources.Entries[0].Fingerprint || mutations.Load() != 0 {
		t.Fatal("raw metadata drift was hidden or native policy changed", err)
	}
	policy["filters"] = append(policy["filters"].([]any), map[string]any{"id": 2, "enabled": true, "name": "another private name", "url": "https://filters.example/second-private?key=second-query", "rules_count": 0})
	third, err := inspectNativeFilters(t.Context(), req)
	if err != nil || len(third.Sources.Entries) != 2 || third.Sources.Entries[0].Origin != third.Sources.Entries[1].Origin || *third.Sources.Entries[0].ID == *third.Sources.Entries[1].ID || third.Sources.Entries[0].Fingerprint == third.Sources.Entries[1].Fingerprint {
		t.Fatal("same-origin native sources collapsed", err)
	}
}

func TestDNSFilterMissingMalformedAndBoundsRemainUnknown(t *testing.T) {
	for _, name := range []string{"missing", "null-enable", "wrong-enable", "missing-id", "duplicate-id", "bad-count", "bad-update", "too-many", "too-many-rules", "long-rule", "long-url", "null-response", "body-bound"} {
		t.Run(name, func(t *testing.T) {
			p := filterAdGuardPolicy()
			entry := p["filters"].([]any)[0].(map[string]any)
			switch name {
			case "missing":
				delete(p, "filters")
			case "null-enable":
				p["enabled"] = nil
			case "wrong-enable":
				p["enabled"] = "true"
			case "missing-id":
				delete(entry, "id")
			case "duplicate-id":
				p["whitelist_filters"] = p["filters"]
			case "bad-count":
				entry["rules_count"] = -1
			case "bad-update":
				entry["last_updated"] = "https://secret?password=private"
			case "too-many":
				entries := make([]any, 257)
				for n := range entries {
					entries[n] = entry
				}
				p["filters"] = entries
			case "too-many-rules":
				p["user_rules"] = make([]string, 257)
			case "long-rule":
				p["user_rules"] = []string{strings.Repeat("x", 4097)}
			case "long-url":
				entry["url"] = strings.Repeat("x", 4097)
			case "null-response":
				p = nil
			case "body-bound":
				p["unreadable"] = strings.Repeat("x", maxNativeBody+1)
			}
			req, _ := filterFixture(t, AdGuard, map[string]any{"/control/status": filterAdGuardStatus(), "/control/filtering/status": p})
			got, err := inspectNativeFilters(t.Context(), req)
			wantSources, wantRules := "unknown", "configured"
			if name == "too-many-rules" || name == "long-rule" {
				wantSources, wantRules = "configured", "unknown"
			}
			if name == "null-enable" || name == "wrong-enable" || name == "null-response" || name == "body-bound" {
				wantRules = "unknown"
			}
			if err != nil || got.Sources.Evidence.State != wantSources || got.Rules.Evidence.State != wantRules || got.Fingerprint != "" || (wantSources == "unknown" && (len(got.Sources.Entries) != 0 || got.Sources.Fingerprint != "")) || (wantRules == "unknown" && (len(got.Rules.Entries) != 0 || got.Rules.Fingerprint != "")) {
				t.Fatalf("independent incomplete metadata became healthy/empty or erased supported evidence: %+v %v", got, err)
			}
		})
	}
}

func TestDNSFilterPiHoleGroupEmptyAndIndependentRuleKinds(t *testing.T) {
	list := map[string]any{"id": 7, "type": "allow", "address": "https://x:secret@filters.example/private?key=private-query", "enabled": true, "groups": []int{}, "number": 0, "date_updated": 0, "status": 0, "comment": "private-comment"}
	rule := map[string]any{"id": 9, "type": "deny", "kind": "regex", "domain": "private-regex-secret", "enabled": false, "groups": []int{0, 3}, "comment": nil}
	policy := map[string]any{"/api/info/version": map[string]any{"version": map[string]any{"ftl": map[string]any{"local": map[string]any{"version": "v6.7.1"}}}}, "/api/dns/blocking": map[string]any{"blocking": "disabled", "timer": nil}, "/api/lists": map[string]any{"lists": []any{list}}, "/api/domains": map[string]any{"domains": []any{rule}}}
	req, mutations := filterFixture(t, PiHole, policy)
	i, err := inspectNativeFilters(t.Context(), req)
	if err != nil || i.Fingerprint == "" || i.Sources.Entries[0].Kind != "allow_subscription" || i.Sources.Entries[0].Groups == nil || len(*i.Sources.Entries[0].Groups) != 0 || i.Rules.Entries[0].Kind != "deny_regex" || *i.Rules.Entries[0].Enabled || mutations.Load() != 0 {
		t.Fatalf("FTL metadata lost group/enable/type semantics: %+v %v", i, err)
	}
	data, _ := json.Marshal(i)
	if !strings.Contains(string(data), `"groups":[]`) || strings.Contains(string(data), "private-") || strings.Contains(string(data), "fixture-filter-session-secret") {
		t.Fatal("FTL secret/empty-group contract failed", string(data))
	}
	list["groups"] = []int{0, 0}
	i, err = inspectNativeFilters(t.Context(), req)
	if err != nil || i.Sources.Evidence.State != "unknown" || i.Rules.Evidence.State != "configured" || i.Fingerprint != "" {
		t.Fatal("malformed FTL source hid independent rule evidence", err)
	}
	list["groups"] = []int{}
	list["address"] = ""
	i, err = inspectNativeFilters(t.Context(), req)
	if err != nil || i.Sources.Evidence.State != "unknown" || len(i.Sources.Entries) != 0 || i.Sources.Fingerprint != "" || i.Rules.Evidence.State != "configured" || i.Fingerprint != "" {
		t.Fatal("an empty native source destination became configured or erased independent rules", err)
	}
}

func TestDNSFilterPiHoleGroupElementsRequireActualIntegers(t *testing.T) {
	for _, section := range []string{"sources", "rules"} {
		t.Run(section, func(t *testing.T) {
			list := map[string]any{"id": 7, "type": "block", "address": "https://filters.example/list", "enabled": true, "groups": []int{0}}
			rule := map[string]any{"id": 9, "type": "deny", "kind": "exact", "domain": "native-rule.example", "enabled": true, "groups": []int{0}}
			policy := map[string]any{"/api/info/version": map[string]any{"version": map[string]any{"ftl": map[string]any{"local": map[string]any{"version": "v6.7.1"}}}}, "/api/dns/blocking": map[string]any{"blocking": "enabled"}, "/api/lists": map[string]any{"lists": []any{list}}, "/api/domains": map[string]any{"domains": []any{rule}}}
			req, mutations := filterFixture(t, PiHole, policy)
			before, err := inspectNativeFilters(t.Context(), req)
			if err != nil || before.Fingerprint == "" || *before.Sources.Entries[0].Groups == nil || (*before.Sources.Entries[0].Groups)[0] != 0 || (*before.Rules.Entries[0].Groups)[0] != 0 {
				t.Fatalf("explicit native group zero was not retained: %+v %v", before, err)
			}
			target := list
			if section == "rules" {
				target = rule
			}
			for _, malformed := range []any{nil, []any{nil}, []any{0, nil}, []any{true}, []any{0.5}, []any{map[string]any{}}, []int{-1}, []int{2147483648}, []int{0, 0}} {
				target["groups"] = malformed
				got, err := inspectNativeFilters(t.Context(), req)
				if err != nil {
					t.Fatal(err)
				}
				unreadable, independent, prior := got.Sources, got.Rules, before.Rules
				if section == "rules" {
					unreadable, independent, prior = got.Rules, got.Sources, before.Sources
				}
				if unreadable.Evidence.State != "unknown" || len(unreadable.Entries) != 0 || unreadable.Fingerprint != "" || got.Fingerprint != "" || independent.Evidence.State != "configured" || independent.Fingerprint != prior.Fingerprint {
					t.Fatalf("malformed memberships acquired a group or erased independent evidence: %+v", got)
				}
			}
			target["groups"] = []int{}
			empty, err := inspectNativeFilters(t.Context(), req)
			if err != nil || empty.Fingerprint == "" || empty.Fingerprint == before.Fingerprint {
				t.Fatalf("explicitly empty memberships lost their own identity: %+v %v", empty, err)
			}
			entry := empty.Sources.Entries[0]
			if section == "rules" {
				entry = empty.Rules.Entries[0]
			}
			if entry.Groups == nil || len(*entry.Groups) != 0 {
				t.Fatal("explicitly empty memberships did not stay an empty array")
			}
			if mutations.Load() != 0 {
				t.Fatal("read-only group-shape inspection mutated native policy")
			}
		})
	}
}

func TestDNSFilterAdGuardNullRequiresExactPinnedWriterAndPresentFields(t *testing.T) {
	for _, version := range []string{"v0.107.71", "v0.107.70", "v0.107.72"} {
		status := filterAdGuardStatus()
		status["version"] = version
		p := map[string]any{"enabled": true, "interval": 24, "filters": nil, "whitelist_filters": nil, "user_rules": nil}
		req, _ := filterFixture(t, AdGuard, map[string]any{"/control/status": status, "/control/filtering/status": p})
		i, err := inspectNativeFilters(t.Context(), req)
		if err != nil {
			t.Fatal(err)
		}
		if version == "v0.107.71" {
			if i.Sources.Evidence.State != "configured" || i.Rules.Evidence.State != "configured" || len(i.Sources.Entries) != 0 || len(i.Rules.Entries) != 0 || i.Fingerprint == "" {
				t.Fatal("explicit pinned nil-slice writer contract lost")
			}
			for _, field := range []string{"filters", "whitelist_filters", "user_rules"} {
				delete(p, field)
				missing, _ := inspectNativeFilters(t.Context(), req)
				if missing.Fingerprint != "" || (field == "user_rules" && (missing.Rules.Evidence.State != "unknown" || missing.Sources.Evidence.State != "configured")) || (field != "user_rules" && (missing.Sources.Evidence.State != "unknown" || missing.Rules.Evidence.State != "configured")) {
					t.Fatal("missing pinned field inferred empty", field)
				}
				p[field] = nil
			}
		} else if i.Sources.Evidence.State != "unknown" || i.Fingerprint != "" {
			t.Fatal("another native writer's null inferred empty", version)
		}
	}
}

func TestDNSFilterAdGuardRuleElementsRequireActualStrings(t *testing.T) {
	for _, version := range []string{"v0.107.71", "v0.107.72"} {
		t.Run(version, func(t *testing.T) {
			status := filterAdGuardStatus()
			status["version"] = version
			policy := filterAdGuardPolicy()
			policy["user_rules"] = []string{}
			req, mutations := filterFixture(t, AdGuard, map[string]any{"/control/status": status, "/control/filtering/status": policy})
			empty, err := inspectNativeFilters(t.Context(), req)
			if err != nil || empty.Rules.Evidence.State != "configured" || len(empty.Rules.Entries) != 0 || empty.Fingerprint == "" {
				t.Fatalf("explicit empty rule array was not retained: %+v %v", empty, err)
			}
			policy["user_rules"] = []string{""}
			blank, err := inspectNativeFilters(t.Context(), req)
			if err != nil || blank.Rules.Evidence.State != "configured" || len(blank.Rules.Entries) != 1 || blank.Rules.Entries[0].Fingerprint != policyHash("") || blank.Rules.Fingerprint == empty.Rules.Fingerprint || blank.Fingerprint == empty.Fingerprint {
				t.Fatalf("reported empty-string entry collapsed into an empty collection: %+v %v", blank, err)
			}
			for _, malformed := range []any{nil, true, 1, map[string]any{}, []any{}} {
				policy["user_rules"] = []any{"# preserved native comment", malformed, "||native-rule.example^"}
				got, err := inspectNativeFilters(t.Context(), req)
				if err != nil || got.Rules.Evidence.State != "unknown" || len(got.Rules.Entries) != 0 || got.Rules.Fingerprint != "" || got.Fingerprint != "" || got.Sources.Evidence.State != "configured" || got.Sources.Fingerprint != empty.Sources.Fingerprint || len(got.Sources.Entries) != 1 {
					t.Fatalf("malformed rule entry became text or erased independent sources: %+v %v", got, err)
				}
			}
			if mutations.Load() != 0 {
				t.Fatal("read-only malformed rule inspection mutated native policy")
			}
		})
	}
}

func TestDNSFilterTechnitiumPinnedNullAndUnsupportedScopes(t *testing.T) {
	for _, version := range []string{"15.6.0", "15.7.0"} {
		p := map[string]any{"version": version, "enableBlocking": true, "blockListUrls": nil, "blockListUpdateIntervalHours": 24, "proxy": map[string]string{"password": "proxy-secret"}, "tsigKeys": []any{map[string]string{"sharedSecret": "tsig-secret"}}}
		req, mutations := filterFixture(t, Technitium, map[string]any{"/api/settings/get": p})
		i, err := inspectNativeFilters(t.Context(), req)
		if err != nil || i.Rules.Evidence.State != "unsupported" || i.AppRules.State != "unsupported" || mutations.Load() != 0 {
			t.Fatal("Technitium scope or read-only contract", err)
		}
		if version == "15.6.0" && (i.Sources.Evidence.State != "configured" || i.Fingerprint == "") {
			t.Fatal("pinned explicit null writer contract lost")
		}
		if version != "15.6.0" && (i.Sources.Evidence.State != "unknown" || i.Fingerprint != "") {
			t.Fatal("unverified null writer treated as empty")
		}
		delete(p, "blockListUrls")
		i, _ = inspectNativeFilters(t.Context(), req)
		if i.Sources.Evidence.State != "unknown" {
			t.Fatal("missing URL field treated as explicit null")
		}
		p["blockListUrls"] = []string{"https://filter.example/secret?token=secret", "!https://allow.example/secret", "#private-comment", "#private-comment"}
		i, err = inspectNativeFilters(t.Context(), req)
		if err != nil || len(i.Sources.Entries) != 4 || i.Sources.Entries[1].Kind != "allow_subscription" || i.Sources.Entries[2].Kind != "subscription_comment" {
			t.Fatal("native allow/comment semantics lost", err)
		}
		data, _ := json.Marshal(i)
		if strings.Contains(string(data), "secret") || strings.Contains(string(data), "private-comment") {
			t.Fatal("Technitium secret material escaped")
		}
	}
}

func TestDNSFilterVersionCancellationAndConnectionGeneration(t *testing.T) {
	status := filterAdGuardStatus()
	status["version"] = "v1.0.0"
	req, _ := filterFixture(t, AdGuard, map[string]any{"/control/status": status})
	if i, err := inspectNativeFilters(t.Context(), req); err == nil || i != nil {
		t.Fatal("unknown native version admitted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if i, err := inspectNativeFilters(ctx, req); err == nil || i != nil {
		t.Fatal("cancelled native read became available")
	}
	s, _, _, _ := newServiceFixture(t, false)
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/control/status" {
			json.NewEncoder(w).Encode(filterAdGuardStatus())
			return
		}
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		json.NewEncoder(w).Encode(filterAdGuardPolicy())
	}))
	defer server.Close()
	req.Endpoint = server.URL
	req.Credential = Credential{Username: "fixture-user", Password: "fixture-filter-password"}
	c, err := s.saveConnection(t.Context(), req, "connected", "")
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := s.Filters(t.Context(), c.ID); result <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("native read did not start")
	}
	if _, err = s.db.Exec(`UPDATE network_dns_services SET generation=generation+1 WHERE id=?`, c.ID); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err = <-result; !errors.Is(err, ErrConflict) {
		t.Fatal("old native generation returned as current", err)
	}
}

func TestDNSFilterCancellationDuringNativeRequestAndUnavailableReadOnly(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/control/status" {
			json.NewEncoder(w).Encode(filterAdGuardStatus())
			return
		}
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	req := ConnectionRequest{Name: "Cancelled filter read", Engine: AdGuard, Endpoint: server.URL, Credential: Credential{Username: "fixture-user", Password: "fixture-filter-password"}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		inventory, err := inspectNativeFilters(ctx, req)
		if inventory != nil {
			done <- errors.New("cancelled request retained inventory")
			return
		}
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("bounded native request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled request became available")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled native request exceeded its cancellation bound")
	}
	s, f, _, view := newServiceFixture(t, false)
	filters, err := s.Filters(t.Context(), view.Connection.ID)
	if err != nil || filters.State != "partial" || filters.Inventory == nil || filters.Inventory.Sources.Evidence.State != "unknown" || filters.Inventory.Fingerprint != "" || filters.Connection.Management || f.mutations.Load() != 0 {
		t.Fatal("unavailable read-only filter metadata lost explicit evidence", err)
	}
	var reviews int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM network_dns_service_changes`).Scan(&reviews); err != nil || reviews != 0 {
		t.Fatal("read-only filter inventory created a retained review", err)
	}
}
