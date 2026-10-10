package dnsservice

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type queryContractFixture struct {
	engine   Engine
	row      string
	expected Query
	required []string
}

func queryContractFixtures() []queryContractFixture {
	return []queryContractFixture{
		{AdGuard, `{"time":"2026-10-09T12:00:00Z","client":"198.51.100.7","reason":"FilteredBlackList","client_proto":"udp","question":{"name":"xn--bcher-kva.example","unicode_name":"bücher.example","host":"wrong.example","type":"A","class":"IN"},"unknown":{"retained":"native"}}`, Query{"2026-10-09T12:00:00Z", "198.51.100.7", "xn--bcher-kva.example", "A", "FilteredBlackList", "udp"}, []string{"time", "client", "reason", "question", "question.name", "question.type"}},
		{PiHole, `{"time":1791547200.125,"type":"AAAA","domain":"private.example","status":"GRAVITY","client":{"ip":"2001:db8::7","name":null},"unknown":true}`, Query{"1791547200.125", "2001:db8::7", "private.example", "AAAA", "GRAVITY", ""}, []string{"time", "type", "domain", "status", "client", "client.ip"}},
		{Technitium, `{"timestamp":"2026-10-09T12:00:00Z","clientIpAddress":"198.51.100.7","protocol":"Tcp","responseType":"Blocked","qname":"private.example","qtype":"A","qclass":"IN","answer":null}`, Query{"2026-10-09T12:00:00Z", "198.51.100.7", "private.example", "A", "Blocked", "Tcp"}, []string{"timestamp", "clientIpAddress", "protocol", "responseType", "qname", "qtype"}},
	}
}

func TestDNSQueryHistoryNativeFieldsAndNullableContracts(t *testing.T) {
	for _, fixture := range queryContractFixtures() {
		t.Run(string(fixture.engine), func(t *testing.T) {
			client := &nativeClient{engine: fixture.engine}
			queries, err := client.queryHistory(json.RawMessage("[" + fixture.row + "]"))
			if err != nil || !reflect.DeepEqual(queries, []Query{fixture.expected}) {
				t.Fatalf("native row lost its reported fields: %+v %v", queries, err)
			}
			queries, err = client.queryHistory(json.RawMessage(`[]`))
			if err != nil || queries == nil || len(queries) != 0 {
				t.Fatal("explicit empty history became unknown", queries, err)
			}
			queries, err = client.queryHistory(json.RawMessage("[" + strings.Join(repeatQuery(fixture.row, 100), ",") + "]"))
			if err != nil || len(queries) != 100 || queries[99] != fixture.expected {
				t.Fatal("bounded complete history was discarded", len(queries), err)
			}
		})
	}
	for _, test := range []struct {
		name     string
		engine   Engine
		row      string
		expected Query
	}{
		{"adguard_protocol_unreported", AdGuard, `{"time":"","client":"","reason":"","question":{"name":"","type":""}}`, Query{}},
		{"pihole_zero_timestamp_nullable_status", PiHole, `{"time":0,"client":{"ip":""},"domain":"","type":"","status":null}`, Query{At: "0.000"}},
		{"technitium_missing_native_question", Technitium, `{"timestamp":"2026-10-09T12:00:00Z","clientIpAddress":"198.51.100.7","responseType":"Unknown","protocol":"Udp","qname":null,"qtype":null}`, Query{"2026-10-09T12:00:00Z", "198.51.100.7", "", "", "Unknown", "Udp"}},
		{"technitium_explicit_empty_question", Technitium, `{"timestamp":"","clientIpAddress":"","responseType":"","protocol":"","qname":"","qtype":""}`, Query{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			queries, err := (&nativeClient{engine: test.engine}).queryHistory(json.RawMessage("[" + test.row + "]"))
			if err != nil || !reflect.DeepEqual(queries, []Query{test.expected}) {
				t.Fatal("reported zero, empty or nullable native field was invented or discarded", queries, err)
			}
		})
	}
}

func repeatQuery(row string, count int) []string {
	rows := make([]string, count)
	for i := range rows {
		rows[i] = row
	}
	return rows
}

func changedQueryField(t *testing.T, original, field string, value any, remove bool) string {
	t.Helper()
	var row map[string]any
	if err := json.Unmarshal([]byte(original), &row); err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(field, ".")
	parent := row
	for _, part := range parts[:len(parts)-1] {
		parent = parent[part].(map[string]any)
	}
	if remove {
		delete(parent, parts[len(parts)-1])
	} else {
		parent[parts[len(parts)-1]] = value
	}
	encoded, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestDNSQueryHistoryRejectsMalformedRowsWithoutPartialHistory(t *testing.T) {
	for _, fixture := range queryContractFixtures() {
		t.Run(string(fixture.engine), func(t *testing.T) {
			client := &nativeClient{engine: fixture.engine}
			badRows := map[string]string{"null_row": "null", "empty_row": "{}", "array_row": "[]", "string_row": `"native-password"`}
			for _, field := range fixture.required {
				badRows[field+"_missing"] = changedQueryField(t, fixture.row, field, nil, true)
				badRows[field+"_wrong_type"] = changedQueryField(t, fixture.row, field, true, false)
				if fixture.engine == PiHole && field == "status" || fixture.engine == Technitium && (field == "qname" || field == "qtype") {
					continue
				}
				badRows[field+"_null"] = changedQueryField(t, fixture.row, field, nil, false)
			}
			switch fixture.engine {
			case AdGuard:
				badRows["legacy_host_without_name"] = changedQueryField(t, fixture.row, "question.name", nil, true)
				badRows["protocol_null"] = changedQueryField(t, fixture.row, "client_proto", nil, false)
				badRows["protocol_wrong_type"] = changedQueryField(t, fixture.row, "client_proto", true, false)
			case PiHole:
				badRows["timestamp_negative"] = changedQueryField(t, fixture.row, "time", -1, false)
				badRows["timestamp_string"] = changedQueryField(t, fixture.row, "time", "0", false)
				badRows["timestamp_overflow"] = strings.Replace(fixture.row, "1791547200.125", "1e1000", 1)
			case Technitium:
				badRows["question_name_only_null"] = changedQueryField(t, fixture.row, "qname", nil, false)
				badRows["question_type_only_null"] = changedQueryField(t, fixture.row, "qtype", nil, false)
			}
			for name, bad := range badRows {
				t.Run(name, func(t *testing.T) {
					for _, collection := range []string{"[" + bad + "]", "[" + fixture.row + "," + bad + "]"} {
						queries, err := client.queryHistory(json.RawMessage(collection))
						if !errors.Is(err, errQueryHistoryShape) || queries != nil || strings.Contains(err.Error(), "native-password") {
							t.Fatal("malformed history produced a fabricated or partial row", queries, err)
						}
					}
				})
			}
			for _, collection := range []string{"", "null", "{}", `"private.example"`, "1", "["} {
				if queries, err := client.queryHistory(json.RawMessage(collection)); !errors.Is(err, errQueryHistoryShape) || queries != nil {
					t.Fatal("unreadable collection became empty history", collection, queries, err)
				}
			}
			if queries, err := client.queryHistory(json.RawMessage("[" + strings.Join(repeatQuery(fixture.row, 101), ",") + "]")); !errors.Is(err, errQueryHistoryBound) || queries != nil {
				t.Fatal("oversized history was silently truncated", len(queries), err)
			}
		})
	}
}

func TestDNSQueryHistoryRedactsAllReportedFields(t *testing.T) {
	for _, fixture := range queryContractFixtures() {
		t.Run(string(fixture.engine), func(t *testing.T) {
			client := &nativeClient{engine: fixture.engine, credential: Credential{Password: "private-password", Token: "private-token"}, sid: "private-session"}
			row := fixture.row
			fields := append([]string{}, fixture.required...)
			if fixture.engine == AdGuard {
				fields = append(fields, "client_proto")
			}
			for _, field := range fields {
				if field == "question" || field == "client" && fixture.engine == PiHole || field == "time" && fixture.engine == PiHole {
					continue
				}
				row = changedQueryField(t, row, field, "private-password\nprivate-token\u007fprivate-session"+strings.Repeat("x", 600), false)
			}
			queries, err := client.queryHistory(json.RawMessage("[" + row + "]"))
			if err != nil || len(queries) != 1 {
				t.Fatal("valid private history was discarded", err)
			}
			encoded, _ := json.Marshal(queries)
			for _, secret := range []string{"private-password", "private-token", "private-session"} {
				if strings.Contains(string(encoded), secret) {
					t.Fatal("native credential escaped query redaction")
				}
			}
			query := queries[0]
			for _, value := range []string{query.At, query.Client, query.Name, query.Type, query.Status, query.Protocol} {
				if len(value) > 512 || strings.ContainsAny(value, "\n\u007f") {
					t.Fatal("query display field escaped its existing text bound")
				}
			}
		})
	}
}

type queryHistoryHTTPFixture struct {
	t      *testing.T
	engine Engine
	mu     sync.Mutex
	rows   json.RawMessage
}

func (f *queryHistoryHTTPFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	var response any
	switch f.engine {
	case AdGuard:
		user, password, ok := r.BasicAuth()
		if r.Method != http.MethodGet || r.URL.Path != "/control/querylog" || r.URL.RawQuery != "limit=100" || !ok || user != "fixture-user" || password != "native-policy-password" {
			f.t.Error("query history escaped its bounded authenticated AdGuard read")
		}
		response = map[string]any{"data": f.rows}
	case PiHole:
		if r.Method != http.MethodGet || r.URL.Path != "/api/queries" || r.URL.RawQuery != "length=100" || r.Header.Get("X-FTL-SID") != "policy-session" {
			f.t.Error("query history escaped its bounded authenticated FTL read")
		}
		response = map[string]any{"queries": f.rows}
	case Technitium:
		if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer native-policy-token" {
			f.t.Error("query history escaped its pinned Technitium bearer POST")
		}
		if r.URL.Path == "/api/apps/list" {
			response = map[string]any{"apps": []any{map[string]any{"name": "Existing logger", "dnsApps": []any{map[string]any{"classPath": "Existing.QueryLogger", "isQueryLogger": true}}}}}
		} else {
			if err := r.ParseForm(); err != nil || !reflect.DeepEqual(r.PostForm, url.Values{"name": {"Existing logger"}, "classPath": {"Existing.QueryLogger"}, "pageNumber": {"1"}, "entriesPerPage": {"100"}, "descendingOrder": {"true"}}) {
				f.t.Error("query history installed or selected an unreviewed logger, or lost its page bound")
			}
			response = map[string]any{"entries": f.rows}
		}
		response = map[string]any{"status": "ok", "response": response}
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		f.t.Error(err)
	}
}

func TestDNSQueryHistoryUnknownSectionKeepsNativePolicyAndPrivateReviews(t *testing.T) {
	for _, fixture := range queryContractFixtures() {
		t.Run(string(fixture.engine), func(t *testing.T) {
			service, _, policy := newPolicyFixture(t, fixture.engine)
			history := &queryHistoryHTTPFixture{t: t, engine: fixture.engine, rows: json.RawMessage("[" + fixture.row + "]")}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/control/querylog" || r.URL.Path == "/api/queries" || fixture.engine == Technitium && (r.URL.Path == "/api/apps/list" || r.URL.Path == "/api/logs/query") {
					history.serve(w, r)
					return
				}
				policy.serve(w, r)
			}))
			t.Cleanup(server.Close)
			credential := Credential{Username: "fixture-user", Password: "native-policy-password"}
			if fixture.engine == PiHole {
				credential.Username = ""
			} else if fixture.engine == Technitium {
				credential = Credential{Token: "native-policy-token"}
			}
			view, err := service.Connect(t.Context(), ConnectionRequest{Name: "History fixture", Engine: fixture.engine, Endpoint: server.URL, Management: true, Credential: credential})
			if err != nil || view.Snapshot == nil || view.Snapshot.QueryEvidence.State != "native_history" || !reflect.DeepEqual(view.Snapshot.Queries, []Query{fixture.expected}) {
				t.Fatal("fresh native history did not retain its actual contract", view, err)
			}
			fingerprint := view.Snapshot.PolicyFingerprint
			for _, rows := range []string{"[" + fixture.row + ",null]", "null", "{}"} {
				history.mu.Lock()
				history.rows = json.RawMessage(rows)
				history.mu.Unlock()
				view, err = service.Inspect(t.Context(), view.Connection.ID)
				if err != nil || view.Snapshot == nil || view.Snapshot.QueryEvidence.State != "unknown" || len(view.Snapshot.Queries) != 0 || view.Snapshot.PolicyFingerprint != fingerprint || len(view.Snapshot.Listeners) == 0 || policy.writes != 0 {
					t.Fatal("bad query rows fabricated history, changed native policy or hid independent configuration", view, err)
				}
			}
			history.mu.Lock()
			history.rows = json.RawMessage("[" + fixture.row + "]")
			history.mu.Unlock()
			enabled := true
			plan, err := service.Preview(t.Context(), view.Connection.ID, ChangeRequest{Action: "protection", Protection: &enabled})
			if err != nil || plan.Before == nil || len(plan.Before.Queries) != 0 || plan.Before.PolicyFingerprint != fingerprint || policy.writes != 0 {
				t.Fatal("private query entries entered a reviewed policy baseline", plan, err)
			}
			var retained string
			if err = service.db.QueryRow(`SELECT before_json FROM network_dns_service_changes WHERE id=?`, plan.ID).Scan(&retained); err != nil || strings.Contains(retained, `"`+fixture.expected.Name+`"`) || strings.Contains(retained, `"`+fixture.expected.Client+`"`) {
				t.Fatal("private query row was persisted with a retained review", err)
			}
			history.mu.Lock()
			history.rows = json.RawMessage("[" + strings.Join(repeatQuery(fixture.row, 101), ",") + "]")
			history.mu.Unlock()
			oversized, err := service.Inspect(t.Context(), view.Connection.ID)
			if err != nil || oversized.State != "unavailable" || oversized.Snapshot != nil || oversized.Error != errQueryHistoryBound.Error() || policy.writes != 0 {
				t.Fatal("oversized history escaped its existing hard snapshot bound", oversized, err)
			}
		})
	}
}
