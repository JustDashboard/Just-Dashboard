package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dnsservice"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

func TestDNSServiceAPIPrivateCapabilitiesSealingAndAudit(t *testing.T) {
	admin, s := newClient(t)
	if err := store.InitializeNetworkDNSServices(t.Context(), s.Store.DB); err != nil {
		t.Fatal(err)
	}
	viewer := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "native-dns-reader", auth.RoleReadOnly)}
	base := "/api/v1/network/dns/services/"
	id := strings.Repeat("a", 32)
	for _, route := range []struct{ method, path, body string }{{"GET", base, ""}, {"POST", base, `{}`}, {"GET", base + id, ""}, {"PUT", base + id, `{}`}, {"DELETE", base + id, ""}, {"POST", base + id + "/changes", `{}`}, {"GET", base + "changes/" + id, ""}, {"POST", base + "changes/" + id + "/apply", `{}`}, {"GET", base + "provisions", ""}, {"POST", base + "provisions", `{}`}, {"GET", base + "provisions/" + id, ""}, {"POST", base + "provisions/" + id + "/apply", `{}`}, {"DELETE", base + "provisions/" + id, ""}} {
		if w := viewer.do(route.method, route.path, route.body, nil); w.Code != http.StatusForbidden {
			t.Fatalf("private native route %s %s returned %d", route.method, route.path, w.Code)
		}
	}
	var adminID int64
	s.Store.DB.QueryRow(`SELECT id FROM users WHERE username='tester'`).Scan(&adminID)
	user, err := s.Auth.UserByID(t.Context(), adminID)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := s.Auth.CreateAPIToken(t.Context(), user, "Private DNS read refusal", auth.RoleReadOnly, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	narrow := &client{t: t, h: s.Routes()}
	if w := narrow.do(http.MethodGet, base, "", map[string]string{"Authorization": "Bearer " + token}); w.Code != 403 {
		t.Fatalf("narrowed admin token private read = %d", w.Code)
	}
	if w := admin.do(http.MethodGet, base+"provisions", "", nil); w.Code != 200 || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("private provision inventory %d %s", w.Code, w.Body.String())
	}
	if w := admin.do(http.MethodPost, base+"provisions", `{"name":"Rejected fixture","engine":"adguard","managementPort":40080,"dnsPort":40053,"memoryMiB":256,"cpus":0.5,"upstreams":["192.0.2.53:53"],"username":"admin","password":"secret8"}`, nil); w.Code != 400 {
		t.Fatalf("invalid native bootstrap password accepted: %d", w.Code)
	}
	const bootstrapPassword = "fixture-only-sealed-bootstrap-password"
	const reviewedImage = "adguard/adguardhome@sha256:92929135ced2554aaf94706f766a98ad348f211df61b0704e2db7e8498cc00b7"
	var dockerMutations atomic.Int32
	docker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasPrefix(path, "/v1.") {
			path = "/" + strings.SplitN(path, "/", 3)[2]
		}
		if path == "/_ping" {
			w.Header().Set("API-Version", "1.51")
			w.WriteHeader(200)
			return
		}
		if r.Method == http.MethodGet && strings.HasPrefix(path, "/images/") {
			json.NewEncoder(w).Encode(map[string]any{"Id": "sha256:" + strings.Repeat("a", 64), "RepoDigests": []string{reviewedImage}})
			return
		}
		dockerMutations.Add(1)
		http.Error(w, "unexpected Docker request", 500)
	}))
	defer docker.Close()
	s.modules.dnsServices.Close()
	s.modules.dnsServices = dnsservice.New(dnsservice.Options{DB: s.Store.DB, Seal: s.Sealer.Seal, Open: s.Sealer.Open, Runtime: dnsservice.NewDockerRuntime(docker.URL)})
	t.Cleanup(func() { s.modules.dnsServices.Close() })
	previewBody, _ := json.Marshal(dnsservice.ProvisionRequest{Name: "Reviewed owned fixture", Engine: dnsservice.AdGuard, ManagementPort: 40080, DNSPort: 40053, MemoryMiB: 256, CPUs: 0.5, Upstreams: []string{"192.0.2.53:53"}, Username: "admin", Password: bootstrapPassword})
	preview := admin.do(http.MethodPost, base+"provisions", string(previewBody), nil)
	var retained dnsservice.Provision
	if preview.Code != 201 || preview.Header().Get("Cache-Control") != "private, no-store" || json.Unmarshal(preview.Body.Bytes(), &retained) != nil || retained.State != "planned" || retained.Request.Management || strings.Contains(preview.Body.String(), bootstrapPassword) || dockerMutations.Load() != 0 {
		t.Fatalf("private owned preview leaked, created resources or changed defaults: %d %s mutations=%d", preview.Code, preview.Body.String(), dockerMutations.Load())
	}
	var sealedBootstrap string
	if err = s.Store.DB.QueryRow(`SELECT secret_enc FROM network_dns_service_provisions WHERE id=?`, retained.ID).Scan(&sealedBootstrap); err != nil || strings.Contains(sealedBootstrap, bootstrapPassword) || sealedBootstrap == "" {
		t.Fatal("owned API preview did not retain sealed bootstrap credentials", err)
	}
	var mutations atomic.Int32
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "fixture-user" || password != "native-password-kept-sealed" {
			http.Error(w, "bad credentials", 401)
			return
		}
		if r.Method != http.MethodGet {
			mutations.Add(1)
			t.Error("default read-only connection mutated native engine")
		}
		var value any
		switch r.URL.Path {
		case "/control/status":
			value = map[string]any{"version": "v0.107.71", "dns_addresses": []string{"127.0.0.1"}, "dns_port": 53, "protection_enabled": true, "running": true}
		case "/control/dns_info":
			value = map[string]any{"upstream_dns": []string{"192.0.2.53:53"}}
		case "/control/access/list":
			value = map[string]any{"allowed_clients": []string{}, "disallowed_clients": []string{}, "blocked_hosts": []string{}}
		case "/control/clients":
			value = map[string]any{"clients": []any{}}
		case "/control/rewrite/list":
			value = []any{}
		case "/control/querylog":
			value = map[string]any{"data": []any{map[string]any{"client": "10.0.0.2", "question": map[string]string{"host": "private.example", "type": "A"}}}}
		default:
			http.Error(w, "unsupported", 404)
			return
		}
		json.NewEncoder(w).Encode(value)
	}))
	defer native.Close()
	req := dnsservice.ConnectionRequest{Name: "Private native engine", Engine: dnsservice.AdGuard, Endpoint: native.URL, Credential: dnsservice.Credential{Username: "fixture-user", Password: "native-password-kept-sealed"}}
	body, _ := json.Marshal(req)
	w := admin.do(http.MethodPost, base, string(body), nil)
	if w.Code != http.StatusCreated || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("native connect %d %s", w.Code, w.Body.String())
	}
	var view dnsservice.View
	if err = json.Unmarshal(w.Body.Bytes(), &view); err != nil || view.Connection.Management || strings.Contains(w.Body.String(), req.Credential.Password) {
		t.Fatalf("default/secret contract: %s %v", w.Body.String(), err)
	}
	if w = admin.do(http.MethodPost, base+view.Connection.ID+"/changes", `{"action":"protection","protection":false}`, nil); w.Code != 403 || mutations.Load() != 0 {
		t.Fatalf("read-only staging %d mutations %d", w.Code, mutations.Load())
	}
	if w = admin.do(http.MethodPost, base+view.Connection.ID+"/changes", `{"action":"protection","protection":false,"url":"http://192.0.2.1:53"}`, nil); w.Code != 400 {
		t.Fatalf("arbitrary proxy field accepted: %d", w.Code)
	}
	if w = admin.do(http.MethodGet, base, "", nil); w.Code != 200 || strings.Contains(w.Body.String(), "private.example") || strings.Contains(w.Body.String(), req.Credential.Password) {
		t.Fatalf("metadata inventory exposed native private data: %s", w.Body.String())
	}
	if w = viewer.do(http.MethodGet, base+view.Connection.ID+"/changes", "", nil); w.Code != 403 {
		t.Fatal("retained native change inventory was not administrator-only", w.Code)
	}
	if w = admin.do(http.MethodGet, base+view.Connection.ID+"/changes", "", nil); w.Code != 200 || w.Header().Get("Cache-Control") != "private, no-store" || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("read-only retained review inventory: %d %s", w.Code, w.Body.String())
	}
	var sealed string
	if err = s.Store.DB.QueryRow(`SELECT secret_enc FROM network_dns_services WHERE id=?`, view.Connection.ID).Scan(&sealed); err != nil || strings.Contains(sealed, req.Credential.Password) {
		t.Fatalf("stored plaintext native credential: %v", err)
	}
	rows, err := s.Store.DB.Query(`SELECT detail FROM audit_log WHERE action LIKE 'network.dns.service.%' OR action='network.dns.provision.preview'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var detail string
		if err = rows.Scan(&detail); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(detail, req.Credential.Password) || strings.Contains(detail, "private.example") || strings.Contains(detail, "secret8") || strings.Contains(detail, bootstrapPassword) {
			t.Fatalf("audit exposed native secrets or query history: %s", detail)
		}
	}
	if w = admin.do(http.MethodDelete, base+view.Connection.ID, "", nil); w.Code != 204 || mutations.Load() != 0 {
		t.Fatalf("disconnect touched foreign engine: %d", w.Code)
	}
}

func TestDNSServiceAPIReviewedAuthoritativeRecordsAndAdminToken(t *testing.T) {
	admin, s := newClient(t)
	if err := store.InitializeNetworkDNSServices(t.Context(), s.Store.DB); err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/network/dns/services/"
	records := []map[string]any{}
	var mutations atomic.Int32
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer native-record-token" {
			t.Error("native records escaped private closed bearer POST")
		}
		r.ParseForm()
		var result any
		zone := map[string]any{"name": "owned.example", "type": "Primary", "internal": false, "disabled": false, "dnssecStatus": "Unsigned"}
		switch r.URL.Path {
		case "/api/settings/get":
			result = map[string]any{"version": "15.6.0", "dnsServerLocalEndPoints": []string{"127.0.0.1:53"}, "recursion": "Deny", "recursionNetworkACL": []string{}, "enableBlocking": false, "forwarders": []string{}, "forwarderProtocol": "Udp"}
		case "/api/zones/list":
			result = map[string]any{"zones": []any{zone}, "totalZones": 1, "totalPages": 1}
		case "/api/apps/list":
			result = map[string]any{"apps": []any{}}
		case "/api/zones/records/get":
			result = map[string]any{"zone": zone, "records": records}
		case "/api/zones/records/add":
			if r.Form.Get("zone") != "owned.example" || r.Form.Get("domain") != "test.owned.example" || r.Form.Get("type") != "AAAA" || r.Form.Get("ipAddress") != "2001:db8::99" || r.Form.Get("ttl") != "60" || r.Form.Get("overwrite") != "false" {
				t.Error("native record scope changed")
			}
			mutations.Add(1)
			records = append(records, map[string]any{"name": "test.owned.example", "type": "AAAA", "ttl": 60, "disabled": false, "rData": map[string]string{"ipAddress": "2001:db8::99"}})
			result = map[string]any{}
		default:
			t.Error("unexpected record path", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "ok", "response": result})
	}))
	defer native.Close()
	req := dnsservice.ConnectionRequest{Name: "Reviewed native authority", Engine: dnsservice.Technitium, Endpoint: native.URL, Management: true, Credential: dnsservice.Credential{Token: "native-record-token"}}
	body, _ := json.Marshal(req)
	connected := admin.do("POST", base, string(body), nil)
	var view dnsservice.View
	if connected.Code != 201 || json.Unmarshal(connected.Body.Bytes(), &view) != nil {
		t.Fatal(connected.Code, connected.Body.String())
	}
	viewer := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "authority-reader", auth.RoleReadOnly)}
	path := base + view.Connection.ID + "/zones/owned.example/records"
	if w := viewer.do("GET", path, "", nil); w.Code != 403 {
		t.Fatal("authoritative records allowed readonly account", w.Code)
	}
	var userID int64
	s.Store.DB.QueryRow(`SELECT id FROM users WHERE username='tester'`).Scan(&userID)
	user, err := s.Auth.UserByID(t.Context(), userID)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := s.Auth.CreateAPIToken(t.Context(), user, "Reviewed native DNS administration", auth.RoleAdmin, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	tokenClient := &client{t: t, h: s.Routes()}
	headers := map[string]string{"Authorization": "Bearer " + token}
	if w := tokenClient.do("GET", path, "", headers); w.Code != 200 || w.Header().Get("Cache-Control") != "private, no-store" || strings.Contains(w.Body.String(), "native-record-token") {
		t.Fatal("admin token/no-store record contract", w.Code, w.Body.String())
	}
	changeBody := `{"action":"record_add","zone":"owned.example","record":{"name":"test.owned.example","type":"AAAA","value":"2001:db8::99","ttl":60}}`
	if w := admin.do("POST", base+view.Connection.ID+"/changes", strings.TrimSuffix(changeBody, "}")+`,"proxy":{"url":"http://127.0.0.1:1"}}`, nil); w.Code != 400 {
		t.Fatal("native record proxy field accepted", w.Code)
	}
	w := tokenClient.do("POST", base+view.Connection.ID+"/changes", changeBody, headers)
	var plan dnsservice.Change
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &plan) != nil || plan.Before == nil || plan.Before.Records == nil || plan.Before.SelectionFingerprint == "" || mutations.Load() != 0 {
		t.Fatal("review did not retain complete zone/RR selection", w.Code, w.Body.String())
	}
	w = tokenClient.do("POST", base+"changes/"+plan.ID+"/apply", `{}`, headers)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &plan) != nil || plan.State != "verified" || mutations.Load() != 1 || plan.After.Records == nil || len(plan.After.Records.Records) != 1 {
		t.Fatal("destructive reviewed native record readback", w.Code, w.Body.String())
	}
	if w = tokenClient.do("POST", base+"changes/"+plan.ID+"/apply", `{}`, headers); w.Code != 409 || mutations.Load() != 1 {
		t.Fatal("native record applied twice", w.Code)
	}
	var detail string
	if err = s.Store.DB.QueryRow(`SELECT detail FROM audit_log WHERE action='network.dns.service.apply' AND target=? AND status=200 ORDER BY id DESC LIMIT 1`, plan.ID).Scan(&detail); err != nil || strings.Contains(detail, "native-record-token") || !strings.Contains(detail, "record_add") {
		t.Fatal("native record mutation audit", detail, err)
	}
}
