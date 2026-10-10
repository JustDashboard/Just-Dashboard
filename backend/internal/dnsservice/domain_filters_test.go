package dnsservice

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type domainFilterFixture struct {
	t                          *testing.T
	engine                     Engine
	mu                         sync.Mutex
	rules                      []string
	rows                       []map[string]any
	sources                    []map[string]any
	groups                     []map[string]any
	reads, writes              int
	readHook                   func(int)
	corruptOther, failMutation bool
	adGuardExtra               map[string]any
	versionReads               int
	versionHook                func(int) string
	blockReads                 bool
}

func domainRuleRow(domain, disposition string, id int) map[string]any {
	return map[string]any{"domain": domain, "unicode": domain, "type": disposition, "kind": "exact", "enabled": true, "groups": []int{0}, "comment": nil, "id": id, "date_added": 1, "date_modified": 1}
}

func newDomainFilterFixture(t *testing.T, engine Engine) (*Service, Connection, *domainFilterFixture) {
	t.Helper()
	s, old, base := newPolicyFixture(t, engine)
	if err := s.Delete(t.Context(), old.ID); err != nil {
		t.Fatal(err)
	}
	f := &domainFilterFixture{t: t, engine: engine, rules: []string{"# private preserved comment", "", "||foreign.example^"}, rows: []map[string]any{domainRuleRow("foreign.example", "deny", 8)}, sources: []map[string]any{{"id": 2, "address": "https://user:private@lists.example/private?token=secret", "type": "block", "enabled": false, "groups": []int{}, "comment": "private subscription", "number": 0}}, groups: []map[string]any{{"id": 0, "name": "Default", "enabled": true}, {"id": 1, "name": "Lab", "enabled": false}}}
	base.groups = f.groups
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/control/status" && r.URL.Path != "/api/info/version" && r.URL.Path != "/control/filtering/status" && r.URL.Path != "/control/filtering/set_rules" && r.URL.Path != "/api/domains" && r.URL.Path != "/api/lists" && r.URL.Path != "/api/groups" && !strings.HasPrefix(r.URL.Path, "/api/domains/") {
			base.serve(w, r)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if engine == AdGuard {
			u, p, ok := r.BasicAuth()
			if !ok || u != "fixture-user" || p != "native-policy-password" {
				t.Error("filter request escaped scoped basic auth")
			}
		} else if r.Header.Get("X-FTL-SID") != "policy-session" {
			t.Error("filter request escaped SID auth")
		}
		if r.URL.RawQuery != "" {
			t.Error("filter request used a query payload")
		}
		var result any
		switch r.URL.Path {
		case "/control/status", "/api/info/version":
			f.versionReads++
			version := "v0.107.71"
			if engine == PiHole {
				version = "v6.7.1"
			}
			if f.versionHook != nil {
				version = f.versionHook(f.versionReads)
			}
			if engine == AdGuard {
				result = map[string]any{"version": version, "dns_addresses": []string{"127.0.0.1"}, "dns_port": 53, "protection_enabled": false, "running": true}
			} else {
				result = map[string]any{"version": map[string]any{"ftl": map[string]any{"local": map[string]any{"version": version}}}}
			}
		case "/control/filtering/status":
			if f.blockReads {
				<-r.Context().Done()
				return
			}
			f.reads++
			if f.readHook != nil {
				f.readHook(f.reads)
			}
			result = map[string]any{"enabled": true, "interval": 0, "filters": []any{}, "whitelist_filters": []any{}, "user_rules": f.rules}
			for k, v := range f.adGuardExtra {
				result.(map[string]any)[k] = v
			}
		case "/api/domains":
			if f.blockReads {
				<-r.Context().Done()
				return
			}
			f.reads++
			if f.readHook != nil {
				f.readHook(f.reads)
			}
			result = map[string]any{"domains": f.rows}
		case "/api/lists":
			result = map[string]any{"lists": f.sources}
		case "/api/groups":
			result = map[string]any{"groups": f.groups}
		case "/control/filtering/set_rules":
			var body struct {
				Rules []string `json:"rules"`
			}
			decoder := json.NewDecoder(r.Body)
			decoder.DisallowUnknownFields()
			if r.Method != "POST" || decoder.Decode(&body) != nil || body.Rules == nil {
				t.Error("unreviewed rule replacement")
				w.WriteHeader(400)
				return
			}
			f.rules = body.Rules
			f.writes++
		default:
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/domains/"), "/")
			if len(parts) < 2 || (parts[0] != "allow" && parts[0] != "deny") || parts[1] != "exact" {
				t.Error("arbitrary native rule path")
				w.WriteHeader(400)
				return
			}
			if r.Method == "POST" && len(parts) == 2 {
				var body struct {
					Domain  string  `json:"domain"`
					Enabled bool    `json:"enabled"`
					Groups  []int   `json:"groups"`
					Comment *string `json:"comment"`
				}
				decoder := json.NewDecoder(r.Body)
				decoder.DisallowUnknownFields()
				if decoder.Decode(&body) != nil || !body.Enabled || body.Groups == nil || body.Comment != nil {
					t.Error("unreviewed native add body")
				}
				row := domainRuleRow(body.Domain, parts[0], 9)
				row["groups"] = body.Groups
				f.rows = append(f.rows, row)
			} else if r.Method == "DELETE" && len(parts) == 3 {
				next := []map[string]any{}
				for _, row := range f.rows {
					if row["domain"] != parts[2] || row["type"] != parts[0] || row["kind"] != "exact" {
						next = append(next, row)
					}
				}
				f.rows = next
			} else {
				t.Error("native DSL or replacement escaped closed action")
				w.WriteHeader(400)
				return
			}
			f.writes++
		}
		if f.writes > 0 && f.corruptOther {
			if engine == AdGuard {
				f.rules[0] = "# changed foreign comment"
			} else {
				f.rows[0]["comment"] = "changed foreign comment"
			}
		}
		if f.writes > 0 && f.failMutation {
			http.Error(w, "native-policy-password private-native-error", 500)
			return
		}
		if result == nil {
			result = map[string]any{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}))
	t.Cleanup(server.Close)
	credential := Credential{Username: "fixture-user", Password: "native-policy-password"}
	if engine == PiHole {
		credential.Username = ""
	}
	view, err := s.Connect(t.Context(), ConnectionRequest{Name: "Domain filters", Engine: engine, Endpoint: server.URL, Management: true, Credential: credential})
	if err != nil {
		t.Fatal(err)
	}
	return s, view.Connection, f
}

func domainFilterRequest(engine Engine, action, disposition string) ChangeRequest {
	f := &DomainFilterChange{Domain: "reviewed.example", Disposition: disposition, Match: "suffix"}
	if engine == PiHole {
		f.Match = "exact"
		if action == "filter_add" {
			groups := []int{}
			f.Groups = &groups
		}
	}
	return ChangeRequest{Action: action, Filter: f}
}

func TestDNSDomainFilterClosedEngineSemantics(t *testing.T) {
	for _, engine := range []Engine{AdGuard, PiHole} {
		req := domainFilterRequest(engine, "filter_add", "deny")
		if err := validateChange(req, engine); err != nil {
			t.Fatal(err)
		}
		mutations := []func(*ChangeRequest){func(r *ChangeRequest) { r.Record = &RecordChange{} }, func(r *ChangeRequest) { r.Filter.Domain = "*.example" }, func(r *ChangeRequest) { r.Filter.Domain = "Example.test" }, func(r *ChangeRequest) { r.Filter.Domain = "x.example^$important" }, func(r *ChangeRequest) { r.Filter.Match = "regex" }, func(r *ChangeRequest) { r.Filter.Disposition = "block" }, func(r *ChangeRequest) { r.Filter = nil }, func(r *ChangeRequest) { r.Action = "protection" }}
		for _, mutate := range mutations {
			copy := domainFilterRequest(engine, "filter_add", "deny")
			mutate(&copy)
			if validateChange(copy, engine) == nil {
				t.Fatalf("accepted arbitrary intent %#v", copy)
			}
		}
		if validateChange(req, Technitium) == nil {
			t.Fatal("Technitium manual/app mutation accepted")
		}
	}
	if adGuardDomainRule(*domainFilterRequest(AdGuard, "filter_add", "allow").Filter) != "@@||reviewed.example^" {
		t.Fatal("suffix semantics changed")
	}
}

func TestDNSDomainFilterReviewedAddRemovePreservesNativePolicy(t *testing.T) {
	for _, engine := range []Engine{AdGuard, PiHole} {
		for _, disposition := range []string{"allow", "deny"} {
			t.Run(string(engine)+"/"+disposition, func(t *testing.T) {
				s, connection, f := newDomainFilterFixture(t, engine)
				originalRules := append([]string{}, f.rules...)
				originalRow := policyHash(f.rows)
				originalSources := policyHash(f.sources)
				plan, err := s.Preview(t.Context(), connection.ID, domainFilterRequest(engine, "filter_add", disposition))
				if err != nil {
					t.Fatal(err)
				}
				if plan.Before.SelectedFilter.Present || plan.Before.SelectedFilter.Match != plan.Request.Filter.Match || plan.Before.SelectedFilter.OtherPolicyFingerprint == "" || f.writes != 0 {
					t.Fatal("preview lacks immutable configured selection")
				}
				current, err := s.CurrentChange(t.Context(), plan.ID)
				if err != nil || current.Snapshot.SelectionFingerprint != plan.Before.SelectionFingerprint || f.writes != 0 {
					t.Fatalf("selection-current read: %v", err)
				}
				applied, err := s.Apply(t.Context(), plan.ID)
				if err != nil || applied.State != "verified" || f.writes != 1 {
					t.Fatalf("add: state=%s error=%s %v", applied.State, applied.Error, err)
				}
				if _, err = s.Apply(t.Context(), plan.ID); !errors.Is(err, ErrConflict) {
					t.Fatalf("singleuse %v", err)
				}
				if _, err = s.Preview(t.Context(), connection.ID, domainFilterRequest(engine, "filter_add", disposition)); err == nil {
					t.Fatal("duplicate staged")
				}
				remove, err := s.Preview(t.Context(), connection.ID, domainFilterRequest(engine, "filter_remove", disposition))
				if err != nil {
					t.Fatal(err)
				}
				applied, err = s.Apply(t.Context(), remove.ID)
				if err != nil || applied.State != "verified" || f.writes != 2 {
					t.Fatalf("remove state=%s error=%s %v", applied.State, applied.Error, err)
				}
				if !reflect.DeepEqual(f.rules, originalRules) || policyHash(f.rows) != originalRow || policyHash(f.sources) != originalSources {
					t.Fatal("unselected rules/comments/groups/subscription changed")
				}
				if _, err = s.CurrentChange(t.Context(), plan.ID); err != nil || f.writes != 2 {
					t.Fatal("consumed review read replayed native effects")
				}
				body, _ := json.Marshal(applied)
				if strings.Contains(string(body), "token=secret") || strings.Contains(string(body), "user:private") {
					t.Fatal("native destination secret retained")
				}
			})
		}
	}
}

func TestDNSDomainFilterFreshRawDriftAndLastReadRefusal(t *testing.T) {
	for _, engine := range []Engine{AdGuard, PiHole} {
		for _, last := range []bool{false, true} {
			t.Run(string(engine)+"/last="+map[bool]string{false: "false", true: "true"}[last], func(t *testing.T) {
				s, connection, f := newDomainFilterFixture(t, engine)
				plan, err := s.Preview(t.Context(), connection.ID, domainFilterRequest(engine, "filter_add", "deny"))
				if err != nil {
					t.Fatal(err)
				}
				change := func() {
					if engine == AdGuard {
						f.rules[0] = "# foreign drift"
					} else {
						f.sources[0]["comment"] = "foreign drift"
					}
				}
				f.mu.Lock()
				if last {
					f.readHook = func(reads int) {
						if reads == 3 {
							change()
						}
					}
				} else {
					change()
				}
				f.mu.Unlock()
				if !last {
					current, err := s.CurrentChange(t.Context(), plan.ID)
					if err != nil || current.Snapshot.SelectionFingerprint == plan.Before.SelectionFingerprint {
						t.Fatal("fresh raw metadata drift omitted")
					}
				}
				applied, err := s.Apply(t.Context(), plan.ID)
				if err != nil || applied.State != "refused" || f.writes != 0 {
					t.Fatalf("drift state=%s error=%s writes=%d err=%v", applied.State, applied.Error, f.writes, err)
				}
			})
		}
	}
}

func TestDNSDomainFilterUnknownDuplicateDisabledAndMissingRemovalRefuse(t *testing.T) {
	for _, engine := range []Engine{AdGuard, PiHole} {
		t.Run(string(engine), func(t *testing.T) {
			s, connection, f := newDomainFilterFixture(t, engine)
			if _, err := s.Preview(t.Context(), connection.ID, domainFilterRequest(engine, "filter_remove", "deny")); err == nil {
				t.Fatal("missing target removal staged")
			}
			f.mu.Lock()
			if engine == AdGuard {
				f.adGuardExtra = map[string]any{"user_rules": nil, "interval": nil}
			} else {
				f.sources = nil
			}
			f.mu.Unlock()
			if _, err := s.Preview(t.Context(), connection.ID, domainFilterRequest(engine, "filter_add", "deny")); err == nil {
				t.Fatal("unknown native policy staged")
			}
			f.mu.Lock()
			f.adGuardExtra = nil
			f.sources = []map[string]any{}
			if engine == AdGuard {
				f.rules = append(f.rules, "||reviewed.example^", "||reviewed.example^")
			} else {
				a, b := domainRuleRow("reviewed.example", "deny", 9), domainRuleRow("reviewed.example", "deny", 10)
				f.rows = append(f.rows, a, b)
			}
			f.mu.Unlock()
			if _, err := s.Preview(t.Context(), connection.ID, domainFilterRequest(engine, "filter_remove", "deny")); err == nil {
				t.Fatal("ambiguous target removal staged")
			}
			if f.writes != 0 {
				t.Fatal("refusal sent native effects")
			}
		})
	}
	s, connection, f := newDomainFilterFixture(t, PiHole)
	f.rows = append(f.rows, domainRuleRow("reviewed.example", "deny", 9))
	f.rows[1]["enabled"] = false
	if _, err := s.Preview(t.Context(), connection.ID, domainFilterRequest(PiHole, "filter_remove", "deny")); err == nil {
		t.Fatal("disabled rule silently deleted")
	}
}

func TestDNSDomainFilterUncertainNativeReplyAndForeignReadbackNeverReplay(t *testing.T) {
	for _, engine := range []Engine{AdGuard, PiHole} {
		for _, uncertain := range []bool{false, true} {
			t.Run(string(engine)+"/uncertain="+map[bool]string{false: "false", true: "true"}[uncertain], func(t *testing.T) {
				s, connection, f := newDomainFilterFixture(t, engine)
				plan, err := s.Preview(t.Context(), connection.ID, domainFilterRequest(engine, "filter_add", "deny"))
				if err != nil {
					t.Fatal(err)
				}
				f.mu.Lock()
				f.failMutation = uncertain
				f.corruptOther = !uncertain
				f.mu.Unlock()
				applied, err := s.Apply(t.Context(), plan.ID)
				if err != nil || applied.State != "needs_review" || f.writes != 1 {
					t.Fatalf("uncertainty state=%s error=%s writes=%d err=%v", applied.State, applied.Error, f.writes, err)
				}
				if strings.Contains(applied.Error, "native-policy-password") || strings.Contains(applied.Error, "private-native-error") {
					t.Fatal("native failure body exposed")
				}
				if _, err = s.Apply(t.Context(), plan.ID); !errors.Is(err, ErrConflict) || f.writes != 1 {
					t.Fatal("uncertain operation replayed")
				}
			})
		}
	}
}

func TestDNSDomainFilterRetainedIntentGenerationAndExpiryRefuse(t *testing.T) {
	for _, mutation := range []string{"unknown-intent", "generation", "expired"} {
		t.Run(mutation, func(t *testing.T) {
			s, connection, f := newDomainFilterFixture(t, AdGuard)
			plan, err := s.Preview(t.Context(), connection.ID, domainFilterRequest(AdGuard, "filter_add", "deny"))
			if err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "unknown-intent":
				_, err = s.db.Exec(`UPDATE network_dns_service_changes SET request_json=? WHERE id=?`, `{"action":"filter_add","filter":{"domain":"reviewed.example","disposition":"deny","match":"suffix","nativeDSL":"arbitrary"}}`, plan.ID)
			case "generation":
				_, err = s.db.Exec(`UPDATE network_dns_services SET generation=generation+1 WHERE id=?`, connection.ID)
			case "expired":
				_, err = s.db.Exec(`UPDATE network_dns_service_changes SET expires_at=1 WHERE id=?`, plan.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.Apply(t.Context(), plan.ID); err == nil || f.writes != 0 {
				t.Fatal("stale or changed retained intent reached native effects")
			}
		})
	}
}

func TestDNSDomainFilterGroupBoundsAndUnsupportedSelectedShapeRefuse(t *testing.T) {
	s, connection, f := newDomainFilterFixture(t, PiHole)
	for _, groups := range [][]int{{0, 0}, {-1}, {2147483648}, {42}} {
		req := domainFilterRequest(PiHole, "filter_add", "allow")
		req.Filter.Groups = &groups
		if _, err := s.Preview(t.Context(), connection.ID, req); err == nil {
			t.Fatalf("unsupported group IDs %v staged", groups)
		}
	}
	f.rows = append(f.rows, domainRuleRow("reviewed.example", "deny", 9))
	f.rows[1]["expires_at"] = 123
	if _, err := s.Preview(t.Context(), connection.ID, domainFilterRequest(PiHole, "filter_remove", "deny")); err == nil || f.writes != 0 {
		t.Fatal("unknown selected rule policy deleted")
	}
}

func TestDNSDomainFilterAdGuardPreservesBlankCommentsAndRefusesModifiedTarget(t *testing.T) {
	for _, native := range []string{"||reviewed.example^$important", "@@||reviewed.example^$badfilter", "||reviewed.example", "||reviewed.example/path", "||reviewed.example*", " REVIEWED.EXAMPLE "} {
		t.Run(native, func(t *testing.T) {
			s, connection, f := newDomainFilterFixture(t, AdGuard)
			f.rules = append(f.rules, native)
			for _, action := range []string{"filter_add", "filter_remove"} {
				if _, err := s.Preview(t.Context(), connection.ID, domainFilterRequest(AdGuard, action, "deny")); err == nil {
					t.Fatalf("modified target accepted for %s", action)
				}
			}
			if f.writes != 0 {
				t.Fatal("unsupported DSL shape mutated")
			}
		})
	}
}

func TestDNSDomainFilterMalformedNativeIdentityNeverBecomesEmptyOrGroupZero(t *testing.T) {
	for _, malformed := range []string{"group-null-id", "group-missing-id", "group-null-enabled", "rule-null-group", "source-null-group", "rule-null-date", "adguard-null-rule"} {
		t.Run(malformed, func(t *testing.T) {
			engine := PiHole
			if malformed == "adguard-null-rule" {
				engine = AdGuard
			}
			s, connection, f := newDomainFilterFixture(t, engine)
			switch malformed {
			case "group-null-id":
				f.groups[0]["id"] = nil
			case "group-missing-id":
				delete(f.groups[0], "id")
			case "group-null-enabled":
				f.groups[0]["enabled"] = nil
			case "rule-null-group":
				f.rows[0]["groups"] = []any{nil}
			case "source-null-group":
				f.sources[0]["groups"] = []any{nil}
			case "rule-null-date":
				f.rows[0]["date_added"] = nil
			case "adguard-null-rule":
				f.adGuardExtra = map[string]any{"user_rules": []any{"# preserved native comment", nil}}
			}
			if _, err := s.Preview(t.Context(), connection.ID, domainFilterRequest(engine, "filter_add", "deny")); err == nil || f.writes != 0 {
				t.Fatal("malformed native rule or identity established an editable baseline")
			}
		})
	}
}

func TestDNSDomainFilterJSONGroupsRejectNullAndKeepExplicitEmpty(t *testing.T) {
	for _, groups := range []string{"null", "[null]", "[0,null]", "[true]", "[0,0]", "[-1]", "[2147483648]"} {
		var req ChangeRequest
		body := `{"action":"filter_add","filter":{"domain":"reviewed.example","disposition":"deny","match":"exact","groups":` + groups + `}}`
		if json.Unmarshal([]byte(body), &req) == nil {
			t.Fatalf("malformed groups %s decoded as usable membership", groups)
		}
	}
	var req ChangeRequest
	if err := json.Unmarshal([]byte(`{"action":"filter_add","filter":{"domain":"reviewed.example","disposition":"deny","match":"exact","groups":[]}}`), &req); err != nil || req.Filter.Groups == nil || len(*req.Filter.Groups) != 0 || validateChange(req, PiHole) != nil {
		t.Fatal("explicit empty group membership was lost", err)
	}
}

func TestDNSDomainFilterVersionBoundsAndCancellationRefuseWithoutEffects(t *testing.T) {
	for _, engine := range []Engine{AdGuard, PiHole} {
		for _, failure := range []string{"version-drift", "unsupported-version", "rule-count", "entry-bytes", "cancelled-read"} {
			t.Run(string(engine)+"/"+failure, func(t *testing.T) {
				s, connection, f := newDomainFilterFixture(t, engine)
				ctx := t.Context()
				f.versionReads = 0
				switch failure {
				case "version-drift", "unsupported-version":
					f.versionHook = func(n int) string {
						if failure == "version-drift" && n == 1 {
							if engine == AdGuard {
								return "v0.107.71"
							}
							return "v6.7.1"
						}
						if engine == AdGuard {
							return "v0.108.1"
						}
						return "v5.30.0"
					}
				case "rule-count":
					if engine == AdGuard {
						f.rules = make([]string, 257)
					} else {
						for n := 1; n <= 257; n++ {
							f.rows = append(f.rows, domainRuleRow("foreign.example", "deny", 100+n))
						}
					}
				case "entry-bytes":
					if engine == AdGuard {
						f.rules = []string{strings.Repeat("x", 4097)}
					} else {
						f.rows[0]["domain"] = strings.Repeat("x", 4097)
					}
				case "cancelled-read":
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
					defer cancel()
					f.blockReads = true
				}
				if _, err := s.Preview(ctx, connection.ID, domainFilterRequest(engine, "filter_add", "deny")); err == nil || f.writes != 0 {
					t.Fatal("unsupported, oversized or cancelled selection reached effects")
				}
				var retained int
				if err := s.db.QueryRow(`SELECT count(*) FROM network_dns_service_changes WHERE connection_id=?`, connection.ID).Scan(&retained); err != nil || retained != 0 {
					t.Fatal("invalid selection became an applicable retained review", err)
				}
			})
		}
	}
}
