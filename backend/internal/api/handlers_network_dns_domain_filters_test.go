package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dnsservice"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

func TestDNSDomainFilterAPIPrivateRetainedCurrentAndDestructiveApply(t *testing.T) {
	admin, s := newClient(t)
	if err := store.InitializeNetworkDNSServices(t.Context(), s.Store.DB); err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/network/dns/services/"
	var mu sync.Mutex
	rules := []string{"# preserve private native comment", "||foreign.example^"}
	writes := 0
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		u, p, ok := r.BasicAuth()
		if !ok || u != "fixture" || p != "sealed-filter-password" {
			w.WriteHeader(401)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/control/filtering/") && r.URL.RawQuery != "" {
			t.Error("native filter secrets or payload entered query")
		}
		var value any
		switch r.URL.Path {
		case "/control/status":
			value = map[string]any{"version": "v0.107.71", "dns_addresses": []string{"127.0.0.1"}, "dns_port": 53, "protection_enabled": false, "running": true}
		case "/control/dns_info":
			value = map[string]any{"upstream_dns": []string{"192.0.2.53:53"}}
		case "/control/access/list":
			value = map[string]any{"allowed_clients": []string{}, "disallowed_clients": []string{}, "blocked_hosts": []string{}}
		case "/control/clients":
			value = map[string]any{"clients": []any{}}
		case "/control/rewrite/list":
			value = []any{}
		case "/control/querylog":
			value = map[string]any{"data": []any{}}
		case "/control/filtering/status":
			value = map[string]any{"enabled": true, "interval": 0, "filters": []any{}, "whitelist_filters": []any{}, "user_rules": rules}
		case "/control/filtering/set_rules":
			var body struct {
				Rules []string `json:"rules"`
			}
			decoder := json.NewDecoder(r.Body)
			decoder.DisallowUnknownFields()
			if r.Method != "POST" || decoder.Decode(&body) != nil || body.Rules == nil {
				t.Error("arbitrary native filter body")
				w.WriteHeader(400)
				return
			}
			rules = body.Rules
			writes++
			value = map[string]any{}
		default:
			t.Error("unexpected native filter path", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(value)
	}))
	defer native.Close()
	req := dnsservice.ConnectionRequest{Name: "Reviewed suffix filters", Engine: dnsservice.AdGuard, Endpoint: native.URL, Management: true, Credential: dnsservice.Credential{Username: "fixture", Password: "sealed-filter-password"}}
	body, _ := json.Marshal(req)
	connected := admin.do("POST", base, string(body), nil)
	var view dnsservice.View
	if connected.Code != 201 || json.Unmarshal(connected.Body.Bytes(), &view) != nil {
		t.Fatal(connected.Code, connected.Body.String())
	}
	changeBody := `{"action":"filter_add","filter":{"domain":"reviewed.example","disposition":"deny","match":"suffix"}}`
	viewer := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "filter-reader", auth.RoleReadOnly)}
	if w := viewer.do("POST", base+view.Connection.ID+"/changes", changeBody, nil); w.Code != 403 {
		t.Fatal("reader staged custom filter", w.Code)
	}
	if w := admin.do("POST", base+view.Connection.ID+"/changes", strings.Replace(changeBody, `"match":"suffix"`, `"match":"suffix","nativeDSL":"arbitrary"`, 1), nil); w.Code != 400 {
		t.Fatal("native DSL request accepted", w.Code)
	}
	preview := admin.do("POST", base+view.Connection.ID+"/changes", changeBody, nil)
	var plan dnsservice.Change
	if preview.Code != 201 || preview.Header().Get("Cache-Control") != "private, no-store" || json.Unmarshal(preview.Body.Bytes(), &plan) != nil || plan.Before.SelectedFilter == nil || plan.Before.SelectedFilter.Match != "suffix" || writes != 0 {
		t.Fatal("closed retained filter review", preview.Code, preview.Body.String())
	}
	var userID int64
	s.Store.DB.QueryRow(`SELECT id FROM users WHERE username='tester'`).Scan(&userID)
	user, err := s.Auth.UserByID(t.Context(), userID)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := s.Auth.CreateAPIToken(t.Context(), user, "Reviewed domain filters", auth.RoleAdmin, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	tokenClient := &client{t: t, h: s.Routes()}
	headers := map[string]string{"Authorization": "Bearer " + token}
	currentPath := base + "changes/" + plan.ID + "/current"
	current := tokenClient.do("GET", currentPath, "", headers)
	var fresh dnsservice.View
	if current.Code != 200 || current.Header().Get("Cache-Control") != "private, no-store" || json.Unmarshal(current.Body.Bytes(), &fresh) != nil || fresh.Snapshot.SelectionFingerprint != plan.Before.SelectionFingerprint || writes != 0 {
		t.Fatal("selection-current admin-token read", current.Code, current.Body.String())
	}
	applyPath := base + "changes/" + plan.ID + "/apply"
	if w := viewer.do("POST", applyPath, `{}`, nil); w.Code != 403 || writes != 0 {
		t.Fatal("reader reached destructive apply", w.Code)
	}
	applied := tokenClient.do("POST", applyPath, `{}`, headers)
	var terminal dnsservice.Change
	if applied.Code != 200 || applied.Header().Get("Cache-Control") != "private, no-store" || json.Unmarshal(applied.Body.Bytes(), &terminal) != nil || terminal.State != "verified" || writes != 1 {
		t.Fatal("destructive admin apply", applied.Code, applied.Body.String())
	}
	if w := tokenClient.do("POST", applyPath, `{}`, headers); w.Code != 409 || writes != 1 {
		t.Fatal("singleuse filter apply replayed", w.Code)
	}
	if w := tokenClient.do("GET", currentPath, "", headers); w.Code != 200 || writes != 1 {
		t.Fatal("consumed current read claimed/replayed", w.Code)
	}
	var count int
	s.Store.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='network.dns.service.apply'`).Scan(&count)
	if count == 0 {
		t.Fatal("filter apply bypassed mutation audit")
	}
	rows, err := s.Store.DB.Query(`SELECT detail FROM audit_log WHERE action LIKE 'network.dns.service.%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var detail string
		if err = rows.Scan(&detail); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(detail, req.Credential.Password) || strings.Contains(detail, "private native comment") {
			t.Fatal("native credential/comment entered audit")
		}
	}
}
