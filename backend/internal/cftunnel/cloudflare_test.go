package cftunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeCloudflare answers the part of Cloudflare's v4 API a tunnel uses, as
// Cloudflare does: the envelope, the errors array, and the record of what
// it was asked.
type fakeCloudflare struct {
	mu            sync.Mutex
	server        *httptest.Server
	token         string
	accounts      []Account
	zones         map[string]string // name → id
	tunnels       map[string]Tunnel
	records       map[string][]DNSRecord // zone id → records
	configuration map[string]json.RawMessage
	calls         []string
	nextID        int
}

func newFakeCloudflare(t *testing.T) *fakeCloudflare {
	t.Helper()
	f := &fakeCloudflare{
		token:         "cf-api-token",
		accounts:      []Account{{ID: "acc123", Name: "Ada's account"}},
		zones:         map[string]string{"example.com": "zone-example"},
		tunnels:       map[string]Tunnel{},
		records:       map[string][]DNSRecord{},
		configuration: map[string]json.RawMessage{},
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeCloudflare) api() API {
	return API{Base: f.server.URL + "/client/v4", Token: f.token, Client: f.server.Client()}
}

func (f *fakeCloudflare) answer(w http.ResponseWriter, status int, result any, errs ...ErrorMessage) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	raw, _ := json.Marshal(result)
	if errs == nil {
		errs = []ErrorMessage{}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"success": status < 300, "errors": errs, "messages": []any{}, "result": json.RawMessage(raw)})
}

func (f *fakeCloudflare) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/client/v4")
	f.calls = append(f.calls, r.Method+" "+path)
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		f.answer(w, http.StatusBadRequest, nil, ErrorMessage{Code: 6003, Message: "Invalid request headers"})
		return
	}
	body, _ := io.ReadAll(r.Body)
	parts := strings.Split(strings.Trim(path, "/"), "/")
	switch {
	case r.Method == http.MethodGet && path == "/accounts":
		f.answer(w, 200, f.accounts)
	case r.Method == http.MethodGet && path == "/zones":
		name := r.URL.Query().Get("name")
		if id, ok := f.zones[name]; ok {
			f.answer(w, 200, []map[string]any{{"id": id, "name": name, "account": map[string]string{"id": "acc123"}}})
			return
		}
		f.answer(w, 200, []any{})
	case len(parts) == 3 && parts[0] == "accounts" && parts[2] == "cfd_tunnel" && r.Method == http.MethodPost:
		var request struct {
			Name      string `json:"name"`
			ConfigSrc string `json:"config_src"`
		}
		_ = json.Unmarshal(body, &request)
		for _, tunnel := range f.tunnels {
			if tunnel.Name == request.Name {
				f.answer(w, http.StatusConflict, nil, ErrorMessage{Code: 1013, Message: "You already have a tunnel with this name."})
				return
			}
		}
		if request.ConfigSrc != "cloudflare" {
			f.answer(w, http.StatusBadRequest, nil, ErrorMessage{Code: 1001, Message: "config_src must be cloudflare"})
			return
		}
		f.nextID++
		tunnel := Tunnel{ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", f.nextID), Name: request.Name, AccountID: parts[1]}
		f.tunnels[tunnel.ID] = tunnel
		f.answer(w, 200, tunnel)
	case len(parts) == 5 && parts[2] == "cfd_tunnel" && parts[4] == "token" && r.Method == http.MethodGet:
		if _, ok := f.tunnels[parts[3]]; !ok {
			f.answer(w, http.StatusNotFound, nil, ErrorMessage{Code: 1003, Message: "Tunnel not found"})
			return
		}
		f.answer(w, 200, "eyJhIjoiYWNjMTIzIn0=")
	case len(parts) == 4 && parts[2] == "cfd_tunnel" && r.Method == http.MethodGet:
		tunnel, ok := f.tunnels[parts[3]]
		if !ok {
			f.answer(w, http.StatusNotFound, nil, ErrorMessage{Code: 1003, Message: "Tunnel not found"})
			return
		}
		f.answer(w, 200, tunnel)
	case len(parts) == 5 && parts[2] == "cfd_tunnel" && parts[4] == "configurations" && r.Method == http.MethodPut:
		var request struct {
			Config json.RawMessage `json:"config"`
		}
		if json.Unmarshal(body, &request) != nil || len(request.Config) == 0 {
			f.answer(w, http.StatusBadRequest, nil, ErrorMessage{Code: 1056, Message: "Bad Configuration: no config"})
			return
		}
		f.configuration[parts[3]] = request.Config
		f.answer(w, 200, map[string]any{"tunnel_id": parts[3], "version": 1})
	case len(parts) == 3 && parts[0] == "zones" && parts[2] == "dns_records" && r.Method == http.MethodGet:
		name := r.URL.Query().Get("name")
		out := []DNSRecord{}
		for _, record := range f.records[parts[1]] {
			if record.Name == name {
				out = append(out, record)
			}
		}
		f.answer(w, 200, out)
	case len(parts) == 3 && parts[0] == "zones" && parts[2] == "dns_records" && r.Method == http.MethodPost:
		var record DNSRecord
		_ = json.Unmarshal(body, &record)
		f.nextID++
		record.ID = fmt.Sprintf("rec%d", f.nextID)
		f.records[parts[1]] = append(f.records[parts[1]], record)
		f.answer(w, 200, record)
	case len(parts) == 4 && parts[0] == "zones" && parts[2] == "dns_records" && r.Method == http.MethodPatch:
		var patch map[string]any
		_ = json.Unmarshal(body, &patch)
		for index, record := range f.records[parts[1]] {
			if record.ID == parts[3] {
				if content, ok := patch["content"].(string); ok {
					record.Content = content
				}
				if proxied, ok := patch["proxied"].(bool); ok {
					record.Proxied = proxied
				}
				f.records[parts[1]][index] = record
				f.answer(w, 200, record)
				return
			}
		}
		f.answer(w, http.StatusNotFound, nil, ErrorMessage{Code: 81044, Message: "Record does not exist."})
	default:
		f.answer(w, http.StatusNotFound, nil, ErrorMessage{Code: 7003, Message: "Could not route to " + path + ", perhaps your object identifier is invalid?"})
	}
}

func TestCloudflareAPICreatesATunnelAndSetsItsRoutes(t *testing.T) {
	f := newFakeCloudflare(t)
	ctx := context.Background()
	api := f.api()
	accounts, err := api.Accounts(ctx)
	if err != nil || len(accounts) != 1 || accounts[0].ID != "acc123" {
		t.Fatalf("accounts = %+v, %v", accounts, err)
	}
	tunnel, err := api.CreateTunnel(ctx, "acc123", "production")
	if err != nil || tunnel.ID == "" || tunnel.Name != "production" || tunnel.AccountID != "acc123" {
		t.Fatalf("created = %+v, %v", tunnel, err)
	}
	if token, err := api.TunnelToken(ctx, "acc123", tunnel.ID); err != nil || token == "" {
		t.Fatalf("token = %q, %v", token, err)
	}
	configuration, _ := Render([]string{"b.example.com", "a.example.com"}, Origin{Service: "http://just-dashboard-ingress:80"})
	if err := api.PutConfiguration(ctx, "acc123", tunnel.ID, configuration); err != nil {
		t.Fatal(err)
	}
	var stored Configuration
	if err := json.Unmarshal(f.configuration[tunnel.ID], &stored); err != nil || len(stored.Ingress) != 3 ||
		stored.Ingress[0].Hostname != "a.example.com" || stored.Ingress[2].Service != "http_status:404" {
		t.Fatalf("stored configuration = %s", f.configuration[tunnel.ID])
	}
	// Cloudflare's refusal, in its own words and with its code.
	_, err = api.CreateTunnel(ctx, "acc123", "production")
	var refusal *Error
	if !errors.As(err, &refusal) || refusal.Status != http.StatusConflict ||
		err.Error() != "Cloudflare refused to create the tunnel production: You already have a tunnel with this name. (code 1013)" {
		t.Fatalf("a second tunnel of the name = %v", err)
	}
	if _, err := api.TunnelToken(ctx, "acc123", "missing"); err == nil || !strings.Contains(err.Error(), "Tunnel not found (code 1003)") {
		t.Fatalf("a token for a tunnel that is not there = %v", err)
	}
	// Wrong credentials are Cloudflare's 6003, not a transport error.
	bad := api
	bad.Token = "wrong"
	if _, err := bad.Accounts(ctx); err == nil || !strings.Contains(err.Error(), "Invalid request headers (code 6003)") {
		t.Fatalf("wrong credentials = %v", err)
	}
	if _, err := (API{}).Accounts(ctx); err == nil || !strings.Contains(err.Error(), "under Certificates") {
		t.Fatalf("no credentials = %v", err)
	}
}

func TestCloudflareAPIPointsAHostnameAtTheTunnel(t *testing.T) {
	f := newFakeCloudflare(t)
	ctx := context.Background()
	api := f.api()
	zone, err := api.ZoneFor(ctx, "shop.app.example.com")
	if err != nil || zone.ID != "zone-example" {
		t.Fatalf("zone = %+v, %v", zone, err)
	}
	if _, err := api.ZoneFor(ctx, "shop.elsewhere.org"); err == nil || !strings.Contains(err.Error(), "no zone") {
		t.Fatalf("a name in no zone = %v", err)
	}
	tunnel := "11111111-2222-4333-8444-555555555555"
	outcome, err := api.EnsureCNAME(ctx, zone.ID, "shop.example.com", tunnel)
	if err != nil || outcome != DNSCreated {
		t.Fatalf("first = %s, %v", outcome, err)
	}
	record := f.records[zone.ID][0]
	if record.Type != "CNAME" || record.Content != tunnel+".cfargotunnel.com" || !record.Proxied || record.Comment != recordComment {
		t.Fatalf("record = %+v", record)
	}
	if outcome, err := api.EnsureCNAME(ctx, zone.ID, "shop.example.com", tunnel); err != nil || outcome != DNSPresent {
		t.Fatalf("again = %s, %v", outcome, err)
	}
	// A record this dashboard made for another tunnel moves to this one.
	other := "99999999-2222-4333-8444-555555555555"
	if outcome, err := api.EnsureCNAME(ctx, zone.ID, "shop.example.com", other); err != nil || outcome != DNSUpdated {
		t.Fatalf("moved = %s, %v", outcome, err)
	}
	if f.records[zone.ID][0].Content != other+".cfargotunnel.com" {
		t.Fatalf("record after the move = %+v", f.records[zone.ID][0])
	}
	// Somebody's own record of the name is never replaced.
	f.records[zone.ID] = append(f.records[zone.ID], DNSRecord{ID: "theirs", Type: "A", Name: "www.example.com", Content: "203.0.113.7"})
	if _, err := api.EnsureCNAME(ctx, zone.ID, "www.example.com", tunnel); err == nil || !strings.Contains(err.Error(), "already has a A record to 203.0.113.7") {
		t.Fatalf("a foreign record = %v", err)
	}
	if f.records[zone.ID][1].Content != "203.0.113.7" {
		t.Fatal("the foreign record was changed")
	}
}
