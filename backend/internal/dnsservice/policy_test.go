package dnsservice

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type policyFixture struct {
	t        *testing.T
	engine   Engine
	version  string
	mu       sync.Mutex
	hosts    []string
	cname    []string
	rewrites []map[string]any
	clients  []map[string]any
	groups   []map[string]any
	records  []map[string]any
	zone     map[string]any
	writes   int
	corrupt  bool
	fail     bool
}

func newPolicyFixture(t *testing.T, engine Engine) (*Service, Connection, *policyFixture) {
	t.Helper()
	s, _, _, _ := newServiceFixture(t, true)
	f := &policyFixture{t: t, engine: engine, version: "15.6.0", hosts: []string{"198.51.100.5 foreign.example alias.example"}, cname: []string{"native-alias.example,foreign.example,60"}, rewrites: []map[string]any{{"domain": "foreign.example", "answer": "198.51.100.5"}}, clients: []map[string]any{{"client": "198.51.100.77", "comment": "retained private comment", "groups": []int{0}, "id": 7, "date_added": 1, "date_modified": 1}}, groups: []map[string]any{{"id": 0, "name": "Default", "enabled": true}, {"id": 1, "name": "Lab", "enabled": false}}, zone: map[string]any{"name": "owned.example", "type": "Primary", "internal": false, "disabled": false, "dnssecStatus": "Unsigned"}, records: []map[string]any{{"name": "owned.example", "type": "SOA", "ttl": uint32(900), "disabled": false, "rData": map[string]any{"serial": 1, "primaryNameServer": "ns.owned.example"}}, {"name": "foreign.owned.example", "type": "A", "ttl": uint32(60), "disabled": false, "rData": map[string]any{"ipAddress": "198.51.100.5"}, "comments": "foreign preserved"}}}
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(server.Close)
	cred := Credential{Username: "fixture-user", Password: "native-policy-password"}
	if engine == PiHole {
		cred.Username = ""
	}
	if engine == Technitium {
		cred = Credential{Token: "native-policy-token"}
	}
	view, err := s.Connect(t.Context(), ConnectionRequest{Name: "Policy fixture", Engine: engine, Endpoint: server.URL, Management: true, Credential: cred})
	if err != nil {
		t.Fatal(err)
	}
	return s, view.Connection, f
}

func (f *policyFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var result any
	path := r.URL.Path
	if f.engine == Technitium {
		if r.Method != "POST" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer native-policy-token" {
			f.t.Error("Technitium request escaped pinned bearer POST")
		}
		r.ParseForm()
		if r.Form.Get("token") != "" {
			f.t.Error("token escaped header")
		}
	}
	if f.fail && f.writes > 0 {
		http.Error(w, "native-policy-password", 503)
		return
	}
	switch path {
	case "/control/status":
		result = map[string]any{"version": "v0.107.71", "dns_addresses": []string{"127.0.0.1"}, "dns_port": 53, "protection_enabled": false, "running": true}
	case "/control/dns_info":
		result = map[string]any{"upstream_dns": []string{"192.0.2.53:53"}}
	case "/control/access/list":
		result = map[string]any{"allowed_clients": []string{}, "disallowed_clients": []string{}, "blocked_hosts": []string{}}
	case "/control/clients":
		result = map[string]any{"clients": []any{}}
	case "/control/querylog":
		result = map[string]any{"data": []any{}}
	case "/control/rewrite/list":
		result = f.rewrites
	case "/control/rewrite/add", "/control/rewrite/delete":
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if len(body) != 3 || body["enabled"] != true || r.Method != "POST" {
			f.t.Error("unreviewed rewrite fields")
		}
		f.writes++
		if path == "/control/rewrite/add" {
			f.rewrites = append(f.rewrites, map[string]any{"domain": body["domain"], "answer": body["answer"], "enabled": true})
		} else {
			next := []map[string]any{}
			for _, v := range f.rewrites {
				if v["domain"] != body["domain"] || v["answer"] != body["answer"] {
					next = append(next, v)
				}
			}
			f.rewrites = next
		}
		if f.corrupt {
			f.rewrites[0]["answer"] = "198.51.100.6"
		}
		result = map[string]any{}
	case "/api/auth":
		if r.Method == "DELETE" {
			w.WriteHeader(204)
			return
		}
		result = map[string]any{"session": map[string]any{"valid": true, "sid": "policy-session"}}
	case "/api/info/version":
		result = map[string]any{"version": map[string]any{"ftl": map[string]any{"local": map[string]any{"version": "v6.7.1"}}}}
	case "/api/info/ftl":
		result = map[string]any{}
	case "/api/config":
		if r.Method == "PATCH" {
			var body struct {
				Config map[string]map[string]json.RawMessage `json:"config"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if len(body.Config) != 1 || len(body.Config["dns"]) != 1 || body.Config["dns"]["hosts"] == nil {
				f.t.Error("FTL override replaced unrelated native configuration")
			}
			json.Unmarshal(body.Config["dns"]["hosts"], &f.hosts)
			f.writes++
			if f.corrupt {
				f.hosts[0] = "198.51.100.6 foreign.example alias.example"
			}
		}
		result = map[string]any{"config": map[string]any{"dns": map[string]any{"upstreams": []string{"192.0.2.53#53"}, "listeningMode": "ALL", "port": 53, "hosts": f.hosts, "cnameRecords": f.cname}}}
	case "/api/dns/blocking":
		result = map[string]any{"blocking": "disabled", "timer": nil}
	case "/api/clients":
		result = map[string]any{"clients": f.clients}
	case "/api/clients/198.51.100.77":
		var body map[string]json.RawMessage
		json.NewDecoder(r.Body).Decode(&body)
		if r.Method != "PUT" || len(body) != 2 || body["comment"] == nil || body["groups"] == nil || r.Header.Get("X-FTL-SID") != "policy-session" {
			f.t.Error("FTL client replacement omitted native comment or session")
		}
		var comment any
		var groups []int
		json.Unmarshal(body["comment"], &comment)
		json.Unmarshal(body["groups"], &groups)
		f.clients[0]["comment"], f.clients[0]["groups"], f.clients[0]["date_modified"] = comment, groups, 2
		f.writes++
		if f.corrupt {
			f.clients[0]["comment"] = "unexpected replacement"
		}
		result = map[string]any{"clients": f.clients}
	case "/api/groups":
		result = map[string]any{"groups": f.groups}
	case "/api/queries":
		result = map[string]any{"queries": []any{}}
	case "/api/settings/get":
		result = map[string]any{"version": f.version, "dnsServerLocalEndPoints": []string{"127.0.0.1:53"}, "recursion": "Deny", "recursionNetworkACL": []string{}, "enableBlocking": false, "forwarders": []string{}, "forwarderProtocol": "Udp"}
	case "/api/zones/list":
		result = map[string]any{"zones": []any{f.zone}, "totalZones": 1, "totalPages": 1}
	case "/api/apps/list":
		result = map[string]any{"apps": []any{}}
	case "/api/zones/records/get":
		if r.Form.Get("zone") != "owned.example" || r.Form.Get("domain") != "owned.example" || r.Form.Get("listZone") != "true" {
			f.t.Error("zone ownership was inferred")
		}
		result = map[string]any{"zone": f.zone, "records": f.records}
	case "/api/zones/records/add", "/api/zones/records/delete":
		if r.Form.Get("zone") != "owned.example" || r.Form.Get("updateSvcbHints") != "false" {
			f.t.Error("native mutation has inferred ownership or unreviewed hints")
		}
		if path == "/api/zones/records/add" {
			if r.Form.Get("overwrite") != "false" || r.Form.Get("ptr") != "false" || r.Form.Get("createPtrZone") != "false" {
				f.t.Error("native implicit side effects allowed")
			}
			f.records = append(f.records, map[string]any{"name": r.Form.Get("domain"), "type": r.Form.Get("type"), "ttl": uint32(60), "disabled": false, "rData": map[string]any{"ipAddress": r.Form.Get("ipAddress")}})
		} else {
			next := []map[string]any{}
			for _, v := range f.records {
				if v["name"] != r.Form.Get("domain") || v["type"] != r.Form.Get("type") || v["rData"].(map[string]any)["ipAddress"] != r.Form.Get("ipAddress") {
					next = append(next, v)
				}
			}
			f.records = next
		}
		f.records[0]["rData"].(map[string]any)["serial"] = f.writes + 2
		f.writes++
		if f.corrupt {
			f.records[1]["comments"] = "foreign changed"
		}
		result = map[string]any{}
	default:
		f.t.Error("unexpected policy request", r.Method, path)
		w.WriteHeader(404)
		return
	}
	if f.engine == Technitium {
		result = map[string]any{"status": "ok", "response": result}
	}
	json.NewEncoder(w).Encode(result)
}

func TestDNSServiceReviewedRecordsPreserveForeignPolicy(t *testing.T) {
	for _, engine := range []Engine{AdGuard, PiHole, Technitium} {
		t.Run(string(engine), func(t *testing.T) {
			s, c, f := newPolicyFixture(t, engine)
			req := ChangeRequest{Action: "override_add", Record: &RecordChange{Name: "test.owned.example", Type: "A", Value: "198.51.100.99"}}
			if engine == Technitium {
				req.Action = "record_add"
				req.Zone = "owned.example"
				req.Record.TTL = 60
			}
			plan, err := s.Preview(t.Context(), c.ID, req)
			if err != nil {
				t.Fatal(err)
			}
			if plan.Before.SelectionFingerprint == "" || engine == Technitium && (plan.Before.Records == nil || len(plan.Before.Records.Records) != 2) {
				t.Fatal("selected inventory omitted from retained review")
			}
			plan, err = s.Apply(t.Context(), plan.ID)
			if err != nil || plan.State != "verified" || f.writes != 1 {
				t.Fatal(plan.State, plan.Error, err)
			}
			if _, err = s.Apply(t.Context(), plan.ID); !errors.Is(err, ErrConflict) || f.writes != 1 {
				t.Fatal("mutation replay", err)
			}
			if _, err = s.Preview(t.Context(), c.ID, req); err == nil {
				t.Fatal("duplicate native record staged")
			}
			req.Action = "override_remove"
			if engine == Technitium {
				req.Action = "record_remove"
			}
			plan, err = s.Preview(t.Context(), c.ID, req)
			if err != nil {
				t.Fatal(err)
			}
			plan, err = s.Apply(t.Context(), plan.ID)
			if err != nil || plan.State != "verified" || f.writes != 2 {
				t.Fatal(plan.State, plan.Error, err)
			}
		})
	}
}

func TestDNSServiceSelectedPolicyDriftAndReadbackUncertainty(t *testing.T) {
	for _, engine := range []Engine{AdGuard, PiHole, Technitium} {
		for _, mode := range []string{"drift", "foreign_readback", "unavailable_readback"} {
			t.Run(string(engine)+"/"+mode, func(t *testing.T) {
				s, c, f := newPolicyFixture(t, engine)
				req := ChangeRequest{Action: "override_add", Record: &RecordChange{Name: "test.owned.example", Type: "AAAA", Value: "2001:db8::99"}}
				if engine == Technitium {
					req.Action = "record_add"
					req.Zone = "owned.example"
					req.Record.TTL = 60
				}
				plan, err := s.Preview(t.Context(), c.ID, req)
				if err != nil {
					t.Fatal(err)
				}
				f.mu.Lock()
				switch mode {
				case "drift":
					if engine == Technitium {
						f.records[1]["comments"] = "external metadata change"
					} else if engine == PiHole {
						f.cname = append(f.cname, "external.example,foreign.example,60")
					} else {
						f.rewrites[0]["answer"] = "198.51.100.6"
					}
				case "foreign_readback":
					f.corrupt = true
				case "unavailable_readback":
					f.fail = true
				}
				f.mu.Unlock()
				plan, err = s.Apply(t.Context(), plan.ID)
				if err != nil {
					t.Fatal(err)
				}
				wanted, writes := "needs_review", 1
				if mode == "drift" {
					wanted, writes = "refused", 0
				}
				if plan.State != wanted || f.writes != writes || strings.Contains(plan.Error, "native-policy-password") {
					t.Fatal("drift or readback classification", plan.State, plan.Error, f.writes)
				}
				if _, err = s.Apply(t.Context(), plan.ID); !errors.Is(err, ErrConflict) || f.writes != writes {
					t.Fatal("uncertain operation replayed", err)
				}
			})
		}
	}
}

func TestDNSServiceClientGroupCommentAndUnknownGroupGuard(t *testing.T) {
	for _, mode := range []string{"verified", "comment_drift", "group_drift", "comment_readback"} {
		t.Run(mode, func(t *testing.T) {
			s, c, f := newPolicyFixture(t, PiHole)
			req := ChangeRequest{Action: "client_groups", Client: &ClientGroupChange{Address: "198.51.100.77", Groups: []int{1}}}
			plan, err := s.Preview(t.Context(), c.ID, req)
			if err != nil {
				t.Fatal(err)
			}
			if plan.Before.SelectedClient == nil || plan.Before.SelectedClient.Comment == nil || *plan.Before.SelectedClient.Comment != "retained private comment" {
				t.Fatal("review omitted native comment")
			}
			f.mu.Lock()
			switch mode {
			case "comment_drift":
				f.clients[0]["comment"] = "native changed"
			case "group_drift":
				f.groups[1]["enabled"] = true
			case "comment_readback":
				f.corrupt = true
			}
			f.mu.Unlock()
			plan, err = s.Apply(t.Context(), plan.ID)
			if err != nil {
				t.Fatal(err)
			}
			expected := "verified"
			if strings.HasSuffix(mode, "drift") {
				expected = "refused"
			}
			if mode == "comment_readback" {
				expected = "needs_review"
			}
			if plan.State != expected {
				t.Fatal(plan.State, plan.Error)
			}
			if expected == "verified" && (plan.After.SelectedClient.CommentFingerprint != plan.Before.SelectedClient.CommentFingerprint || !reflect.DeepEqual(plan.After.SelectedClient.Groups, []int{1})) {
				t.Fatal("client comment or groups changed unexpectedly")
			}
		})
	}
	s, c, _ := newPolicyFixture(t, PiHole)
	for _, req := range []ChangeRequest{{Action: "client_groups", Client: &ClientGroupChange{Address: "198.51.100.77", Groups: []int{88}}}, {Action: "client_groups", Client: &ClientGroupChange{Address: "198.51.100.78", Groups: []int{0}}}} {
		if _, err := s.Preview(t.Context(), c.ID, req); err == nil {
			t.Fatal("unknown client/group adopted")
		}
	}
}

func TestDNSServiceClosedRecordAndClientInputs(t *testing.T) {
	for _, req := range []ChangeRequest{
		{Action: "record_add", Zone: "owned.example", Record: &RecordChange{Name: "x.owned.example", Type: "TXT", Value: "arbitrary", TTL: 60}},
		{Action: "record_add", Zone: "owned.example", Record: &RecordChange{Name: "badowned.example", Type: "A", Value: "198.51.100.99", TTL: 60}},
		{Action: "record_add", Zone: "owned.example", Record: &RecordChange{Name: "X.owned.example", Type: "A", Value: "198.51.100.99", TTL: 60}},
		{Action: "record_add", Zone: "owned.example", Record: &RecordChange{Name: "x.owned.example", Type: "AAAA", Value: "::ffff:198.51.100.99", TTL: 60}},
		{Action: "record_add", Zone: "owned.example", Record: &RecordChange{Name: "x.owned.example", Type: "A", Value: "0.0.0.0", TTL: 60}},
		{Action: "override_add", Record: &RecordChange{Name: "x.owned.example", Type: "A", Value: "198.51.100.99", TTL: 60}},
		{Action: "client_groups", Client: &ClientGroupChange{Address: "198.51.100.77", Groups: []int{0, 0}}},
		{Action: "client_groups", Client: &ClientGroupChange{Address: "http://example.com", Groups: []int{0}}},
		{Action: "protection", Record: &RecordChange{Name: "x.owned.example", Type: "A", Value: "198.51.100.99"}},
	} {
		for _, engine := range []Engine{AdGuard, PiHole, Technitium} {
			if validateChange(req, engine) == nil {
				t.Fatal("unsafe mixed/unsupported policy accepted", engine, req)
			}
		}
	}
	s, c, f := newPolicyFixture(t, Technitium)
	req := ChangeRequest{Action: "record_add", Zone: "owned.example", Record: &RecordChange{Name: "x.owned.example", Type: "A", Value: "198.51.100.99", TTL: 60}}
	for _, field := range []string{"internal", "disabled", "dnssecStatus", "type"} {
		f.mu.Lock()
		old := f.zone[field]
		f.zone[field] = true
		if field == "dnssecStatus" {
			f.zone[field] = "SignedWithNSEC"
		}
		if field == "type" {
			f.zone[field] = "Secondary"
		}
		f.mu.Unlock()
		if _, err := s.Preview(t.Context(), c.ID, req); err == nil {
			t.Fatal("unsupported zone mutation admitted", field)
		}
		f.mu.Lock()
		f.zone[field] = old
		f.mu.Unlock()
	}
}

func TestDNSServiceRecordConflictAndLastReadGuards(t *testing.T) {
	for _, engine := range []Engine{AdGuard, PiHole, Technitium} {
		t.Run(string(engine), func(t *testing.T) {
			s, c, f := newPolicyFixture(t, engine)
			req := ChangeRequest{Action: "override_add", Record: &RecordChange{Name: "test.owned.example", Type: "A", Value: "198.51.100.99"}}
			if engine == Technitium {
				req.Action = "record_add"
				req.Zone = "owned.example"
				req.Record.TTL = 60
			}
			plan, err := s.Preview(t.Context(), c.ID, req)
			if err != nil {
				t.Fatal(err)
			}
			_, native, err := s.connection(t.Context(), c.ID)
			if err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			if engine == Technitium {
				f.records[1]["comments"] = "changed after inspected preflight"
			} else if engine == PiHole {
				f.hosts = append(f.hosts, "198.51.100.6 another.example")
			} else {
				f.rewrites = append(f.rewrites, map[string]any{"domain": "another.example", "answer": "198.51.100.6"})
			}
			f.mu.Unlock()
			if err = applyNative(t.Context(), native, req, plan.Before); !errors.Is(err, ErrConflict) || f.writes != 0 {
				t.Fatal("last native read did not fence changed configuration", err)
			}
		})
	}
	s, c, f := newPolicyFixture(t, Technitium)
	req := ChangeRequest{Action: "record_add", Zone: "owned.example", Record: &RecordChange{Name: "test.owned.example", Type: "A", Value: "198.51.100.99", TTL: 60}}
	for _, record := range []map[string]any{
		{"name": "test.owned.example", "type": "CNAME", "ttl": 60, "disabled": false, "rData": map[string]string{"cname": "foreign.example"}},
		{"name": "test.owned.example", "type": "A", "ttl": 120, "disabled": false, "rData": map[string]string{"ipAddress": "198.51.100.5"}},
		{"name": "test.owned.example", "type": "A", "ttl": 60, "disabled": false, "expiryTtl": 1, "rData": map[string]string{"ipAddress": "198.51.100.5"}},
	} {
		f.mu.Lock()
		f.records = append(f.records, record)
		f.mu.Unlock()
		if _, err := s.Preview(t.Context(), c.ID, req); err == nil {
			t.Fatal("conflicting native record staged", record)
		}
		f.mu.Lock()
		f.records = f.records[:len(f.records)-1]
		f.mu.Unlock()
	}
	s, c, f = newPolicyFixture(t, PiHole)
	req = ChangeRequest{Action: "override_remove", Record: &RecordChange{Name: "alias.example", Type: "A", Value: "198.51.100.5"}}
	if _, err := s.Preview(t.Context(), c.ID, req); err == nil || f.writes != 0 {
		t.Fatal("multi-alias foreign hosts entry was adopted")
	}
}

func TestDNSNativeRecordOwnershipPinsAbsentLegacyFlag(t *testing.T) {
	for _, version := range []string{"15.6", "15.6.0", "15.5.0", "15.7.0"} {
		t.Run(version, func(t *testing.T) {
			s, c, f := newPolicyFixture(t, Technitium)
			f.mu.Lock()
			f.version = version
			delete(f.zone, "internal")
			f.mu.Unlock()
			inv, err := s.Records(t.Context(), c.ID, "owned.example")
			if err != nil || inv.Internal != nil || inv.NativeVersion != version {
				t.Fatal("missing native classification was invented", inv, err)
			}
			req := ChangeRequest{Action: "record_add", Zone: "owned.example", Record: &RecordChange{Name: "test.owned.example", Type: "A", Value: "198.51.100.99", TTL: 60}}
			plan, err := s.Preview(t.Context(), c.ID, req)
			if version != "15.6" && version != "15.6.0" {
				if err == nil || f.writes != 0 {
					t.Fatal("unknown absent-flag version admitted")
				}
				return
			}
			if err != nil || plan.Before.Records.Internal != nil {
				t.Fatal("pinned native Primary contract refused", err)
			}
			plan, err = s.Apply(t.Context(), plan.ID)
			if err != nil || plan.State != "verified" {
				t.Fatal(plan.State, plan.Error, err)
			}
		})
	}
}

func TestDNSServiceUnknownNativePolicyFieldsRefuseEffects(t *testing.T) {
	for _, engine := range []Engine{AdGuard, PiHole, Technitium} {
		t.Run(string(engine), func(t *testing.T) {
			s, c, f := newPolicyFixture(t, engine)
			req := ChangeRequest{Action: "override_remove", Record: &RecordChange{Name: "foreign.example", Type: "A", Value: "198.51.100.5"}}
			f.mu.Lock()
			if engine == AdGuard {
				f.rewrites[0]["foreign_enabled_policy"] = false
			}
			if engine == PiHole {
				f.clients[0]["foreign_client_policy"] = true
				req = ChangeRequest{Action: "client_groups", Client: &ClientGroupChange{Address: "198.51.100.77", Groups: []int{1}}}
			}
			if engine == Technitium {
				f.records[1]["foreign_managed_policy"] = true
				req = ChangeRequest{Action: "record_remove", Zone: "owned.example", Record: &RecordChange{Name: "foreign.owned.example", Type: "A", Value: "198.51.100.5", TTL: 60}}
			}
			f.mu.Unlock()
			if _, err := s.Preview(t.Context(), c.ID, req); err == nil || f.writes != 0 {
				t.Fatal("unknown native policy fields admitted effects")
			}
		})
	}
}

func TestDNSServiceAdGuardRewriteEnabledPolicyIsRetainedAndFenced(t *testing.T) {
	s, c, f := newPolicyFixture(t, AdGuard)
	req := ChangeRequest{Action: "override_remove", Record: &RecordChange{Name: "foreign.example", Type: "A", Value: "198.51.100.5"}}
	f.mu.Lock()
	f.rewrites[0]["enabled"] = true
	f.mu.Unlock()
	plan, err := s.Preview(t.Context(), c.ID, req)
	if err != nil || len(plan.Before.LocalOverrides) != 1 || plan.Before.LocalOverrides[0].Enabled == nil || !*plan.Before.LocalOverrides[0].Enabled {
		t.Fatal("native enable state absent from retained review", plan.Before, err)
	}
	f.mu.Lock()
	f.rewrites[0]["enabled"] = false
	f.mu.Unlock()
	plan, err = s.Apply(t.Context(), plan.ID)
	if err != nil || plan.State != "refused" || f.writes != 0 {
		t.Fatal("changed native enable state did not fence removal", plan.State, err)
	}
	if _, err = s.Preview(t.Context(), c.ID, req); err == nil || f.writes != 0 {
		t.Fatal("disabled native override was adopted for removal")
	}
	for _, value := range []any{nil, "true", 1} {
		f.mu.Lock()
		f.rewrites[0]["enabled"] = value
		f.mu.Unlock()
		if _, err = s.Preview(t.Context(), c.ID, req); err == nil || f.writes != 0 {
			t.Fatal("invalid native enable state admitted", value)
		}
	}
	f.mu.Lock()
	f.rewrites[0]["enabled"] = true
	f.mu.Unlock()
	plan, err = s.Preview(t.Context(), c.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = s.Apply(t.Context(), plan.ID)
	if err != nil || plan.State != "verified" || f.writes != 1 {
		t.Fatal("exact enabled native rewrite removal failed", plan.State, plan.Error, err)
	}
}
