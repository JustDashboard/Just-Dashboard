package dnsrecords

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// cloudflareFake is Cloudflare's v4 zones and dns_records, one zone per
// page so the client has to follow result_info.
type cloudflareFake struct {
	fakeStore
	token string
}

func cloudflareFail(w http.ResponseWriter, status, code int, message string) {
	writeJSON(w, status, map[string]any{"success": false, "errors": []map[string]any{{"code": code, "message": message}}, "messages": []any{}, "result": nil})
}

func cloudflareOK(w http.ResponseWriter, result any, info map[string]int) {
	answer := map[string]any{"success": true, "errors": []any{}, "messages": []any{}, "result": result}
	if info != nil {
		answer["result_info"] = info
	}
	writeJSON(w, http.StatusOK, answer)
}

func (f *cloudflareFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record(r)
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		cloudflareFail(w, http.StatusForbidden, 10000, "Authentication error")
		return
	}
	parts := segments(r.URL.Path, "")
	switch {
	case r.Method == http.MethodGet && len(parts) == 1 && parts[0] == "zones":
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		result := []any{}
		if page <= len(f.zones) {
			zone := f.zones[page-1]
			result = append(result, map[string]any{"id": zone.ID, "name": zone.Name, "status": "active",
				"account": map[string]string{"id": zone.Account + "-id", "name": zone.Account}})
		}
		cloudflareOK(w, result, map[string]int{"page": page, "per_page": 1, "total_pages": len(f.zones), "count": len(result), "total_count": len(f.zones)})
	case len(parts) >= 3 && parts[0] == "zones" && parts[2] == "dns_records":
		zone, ok := f.zone(parts[1])
		if !ok {
			cloudflareFail(w, http.StatusNotFound, 7003, "Could not route to /zones/"+parts[1]+"/dns_records, perhaps your object identifier is invalid?")
			return
		}
		f.serveRecords(w, r, zone, parts[3:])
	default:
		cloudflareFail(w, http.StatusNotFound, 7000, "No route for that URI")
	}
}

func (f *cloudflareFake) render(record fakeRecord) map[string]any {
	return map[string]any{"id": record.ID, "type": record.Type, "name": record.Name, "content": record.Value,
		"ttl": record.TTL, "proxied": record.Proxied, "comment": record.Comment}
}

func (f *cloudflareFake) serveRecords(w http.ResponseWriter, r *http.Request, zone fakeZone, rest []string) {
	switch {
	case r.Method == http.MethodGet && len(rest) == 0:
		result := []any{}
		for _, record := range f.find(zone.ID, r.URL.Query().Get("name"), r.URL.Query().Get("type")) {
			result = append(result, f.render(record))
		}
		cloudflareOK(w, result, map[string]int{"page": 1, "per_page": 100, "total_pages": 1})
	case r.Method == http.MethodPost && len(rest) == 0:
		var body struct {
			Type, Name, Content, Comment string
			TTL                          int
			Proxied                      *bool
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Type == "" || body.Content == "" {
			cloudflareFail(w, http.StatusBadRequest, 9000, "DNS record type, name and content are required")
			return
		}
		if !InZone(body.Name, zone.Name) {
			cloudflareFail(w, http.StatusBadRequest, 9005, "Content for the record name is not within the zone")
			return
		}
		proxied := body.Proxied != nil && *body.Proxied
		if body.TTL != 1 && (body.TTL < 60 || body.TTL > 86400) {
			cloudflareFail(w, http.StatusBadRequest, 9021, "Invalid TTL. Must be between 60 and 86400 seconds, or 1 for Automatic.")
			return
		}
		if f.conflicts(zone.ID, body.Name, body.Type) {
			cloudflareFail(w, http.StatusBadRequest, 81053, "An A, AAAA, or CNAME record with that host already exists.")
			return
		}
		record := f.add(fakeRecord{Zone: zone.ID, Name: body.Name, Type: body.Type, Value: body.Content, TTL: body.TTL, Proxied: proxied, Comment: body.Comment})
		cloudflareOK(w, f.render(record), nil)
	case r.Method == http.MethodPatch && len(rest) == 1:
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			cloudflareFail(w, http.StatusBadRequest, 9207, "Request body is invalid.")
			return
		}
		if !f.update(rest[0], func(record *fakeRecord) {
			if content, ok := body["content"].(string); ok {
				record.Value = content
			}
			if ttl, ok := body["ttl"].(float64); ok {
				record.TTL = int(ttl)
			}
			if proxied, ok := body["proxied"].(bool); ok {
				record.Proxied = proxied
			}
			if comment, ok := body["comment"].(string); ok {
				record.Comment = comment
			}
		}) {
			cloudflareFail(w, http.StatusNotFound, 81044, "Record does not exist.")
			return
		}
		cloudflareOK(w, map[string]string{"id": rest[0]}, nil)
	default:
		cloudflareFail(w, http.StatusMethodNotAllowed, 10405, "Method not allowed")
	}
}

// A token scoped to two accounts reaches a zone in each; the delegated
// preview zone is the one written, and its tunnel CNAME is proxied with
// TTL 1.
func TestCloudflareWritesRecordsInTheZoneThatHoldsTheName(t *testing.T) {
	fake := &cloudflareFake{token: "scoped-token", fakeStore: fakeStore{zones: []fakeZone{
		{ID: "zone-root", Name: "example.com", Account: "Main"},
		{ID: "zone-preview", Name: "preview.example.com", Account: "Agency"},
		{ID: "zone-other", Name: "example.org", Account: "Agency"},
	}}}
	server := httptest.NewServer(fake)
	defer server.Close()
	provider := mustProvider(t, "cloudflare", "dns_cloudflare_api_token = scoped-token\n", Options{BaseURL: server.URL})
	zones, err := provider.Zones(context.Background(), "app.example.com")
	if err != nil || len(zones) != 3 || zones[1].Account != "Agency" {
		t.Fatalf("zones across accounts and pages = %+v %v", zones, err)
	}
	exercise(t, provider, "app.preview.example.com", "preview.example.com", "tunnel.preview.example.com", true)
	for _, record := range fake.find("zone-preview", "tunnel.preview.example.com", "CNAME") {
		if !record.Proxied || record.TTL != 1 {
			t.Fatalf("the tunnel's CNAME = %+v, want proxied with TTL 1", record)
		}
	}
	for _, record := range fake.find("zone-preview", "app.preview.example.com", "A") {
		if record.Proxied || record.Comment != "Just Dashboard" || record.Value != "203.0.113.8" {
			t.Fatalf("the address = %+v", record)
		}
	}
	if len(fake.find("zone-root", "", "")) != 0 {
		t.Fatal("a record was written into the parent zone")
	}

	// An operator's proxied A keeps its proxied flag through a change.
	fake.add(fakeRecord{Zone: "zone-root", Name: "shop.example.com", Type: "A", Value: "198.51.100.4", TTL: 1, Proxied: true})
	zone := Zone{ID: "zone-root", Name: "example.com"}
	held, _ := provider.Read(context.Background(), zone, "shop.example.com")
	if _, err := Apply(context.Background(), provider, zone, Diff([]Set{{Name: "shop.example.com", Type: TypeA, Values: []string{"203.0.113.7"}, TTL: 300}}, held, true)); err != nil {
		t.Fatal(err)
	}
	if record := fake.find("zone-root", "shop.example.com", "A")[0]; !record.Proxied || record.TTL != 1 || record.Value != "203.0.113.7" {
		t.Fatalf("the proxied record after a change = %+v", record)
	}
}

func TestCloudflareRefusalsAreItsOwnWords(t *testing.T) {
	fake := &cloudflareFake{token: "right", fakeStore: fakeStore{zones: []fakeZone{{ID: "z", Name: "example.com", Account: "Main"}}}}
	server := httptest.NewServer(fake)
	defer server.Close()
	wrong := mustProvider(t, "cloudflare", "dns_cloudflare_api_token = wrong", Options{BaseURL: server.URL})
	_, err := wrong.Zones(context.Background(), "app.example.com")
	refusedInWords(t, err, "Cloudflare refused to list the zones", "Authentication error (code 10000)")

	right := mustProvider(t, "cloudflare", "dns_cloudflare_api_token = right", Options{BaseURL: server.URL})
	err = right.Write(context.Background(), Zone{ID: "z", Name: "example.com"}, nil, Set{Name: "app.example.com", Type: TypeA, Values: []string{"203.0.113.7"}, TTL: 5})
	refusedInWords(t, err, "create A app.example.com", "Invalid TTL", "code 9021")
	if !strings.Contains(strings.Join(fake.requests, "\n"), "POST /zones/z/dns_records") {
		t.Fatalf("requests %v", fake.requests)
	}
}
