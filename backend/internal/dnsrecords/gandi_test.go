package dnsrecords

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// gandiFake is LiveDNS v5: domains, and records as sets addressed by name
// and type, a 404 for a name with none, a 409 for a set POSTed twice, and
// the five-minute TTL floor.
type gandiFake struct {
	fakeStore
	authorization, sharing string
}

func gandiFail(w http.ResponseWriter, status int, message, cause string, errors ...map[string]string) {
	body := map[string]any{"code": status, "message": message, "object": "HTTP" + strings.ReplaceAll(cause, " ", ""), "cause": cause}
	if len(errors) > 0 {
		body["errors"] = errors
	}
	writeJSON(w, status, body)
}

func (f *gandiFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record(r)
	if r.Header.Get("Authorization") != f.authorization {
		gandiFail(w, http.StatusUnauthorized, "The server could not verify that you authorized to access the document you requested.", "Unauthorized")
		return
	}
	if r.URL.Query().Get("sharing_id") != f.sharing {
		gandiFail(w, http.StatusForbidden, "Access was denied to this resource.", "Forbidden")
		return
	}
	parts := segments(r.URL.Path, "")
	switch {
	case r.Method == http.MethodGet && len(parts) == 1 && parts[0] == "domains":
		domains := []any{}
		for _, zone := range f.zones {
			domains = append(domains, map[string]string{"fqdn": zone.Name, "domain_href": "https://api.gandi.net/v5/livedns/domains/" + zone.Name})
		}
		writeJSON(w, http.StatusOK, domains)
	case len(parts) >= 4 && parts[0] == "domains" && parts[2] == "records":
		zone, ok := f.zone(parts[1])
		if !ok {
			gandiFail(w, http.StatusNotFound, "The domain "+parts[1]+" does not exist", "Not Found")
			return
		}
		name := absolute(parts[3], zone.Name)
		if len(parts) == 4 && r.Method == http.MethodGet {
			sets := []any{}
			for _, set := range f.sets(zone.ID) {
				if set.Name != name {
					continue
				}
				values := append([]string(nil), set.Values...)
				if set.Type == "CNAME" {
					for index := range values {
						values[index] += "."
					}
				}
				sets = append(sets, map[string]any{"rrset_name": parts[3], "rrset_type": set.Type, "rrset_ttl": set.TTL, "rrset_values": values})
			}
			if len(sets) == 0 {
				gandiFail(w, http.StatusNotFound, "Can't find the DNS record for "+parts[3]+" in "+zone.Name, "Not Found")
				return
			}
			writeJSON(w, http.StatusOK, sets)
			return
		}
		if len(parts) != 5 || (r.Method != http.MethodPost && r.Method != http.MethodPut) {
			gandiFail(w, http.StatusMethodNotAllowed, "Method not allowed", "Method Not Allowed")
			return
		}
		var body gandiSet
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			gandiFail(w, http.StatusBadRequest, "Invalid JSON", "Bad Request")
			return
		}
		if body.TTL < 300 {
			gandiFail(w, http.StatusBadRequest, "Validation error", "Bad Request", map[string]string{"location": "body", "name": "rrset_ttl", "description": "Must be greater than or equal to 300."})
			return
		}
		kind := parts[4]
		if r.Method == http.MethodPost && len(f.find(zone.ID, name, kind)) > 0 {
			gandiFail(w, http.StatusConflict, "A DNS Record already exists with same value", "Conflict")
			return
		}
		if f.conflicts(zone.ID, name, kind) {
			gandiFail(w, http.StatusConflict, "A CNAME record cannot coexist with other records", "Conflict")
			return
		}
		values := append([]string(nil), body.Values...)
		for index := range values {
			if kind == "CNAME" {
				if !strings.HasSuffix(values[index], ".") {
					gandiFail(w, http.StatusBadRequest, "Validation error", "Bad Request", map[string]string{"location": "body", "name": "rrset_values", "description": "a CNAME target must end with a dot"})
					return
				}
				values[index] = strings.TrimSuffix(values[index], ".")
			}
		}
		f.replaceSet(zone.ID, name, kind, values, body.TTL)
		writeJSON(w, http.StatusCreated, map[string]string{"message": "DNS Record Created"})
	default:
		gandiFail(w, http.StatusNotFound, "Not found", "Not Found")
	}
}

func TestGandiWritesRecordSetsWithATokenOrAnAPIKey(t *testing.T) {
	fake := &gandiFake{authorization: "Bearer gandi-pat", sharing: "org-1", fakeStore: fakeStore{zones: []fakeZone{
		{ID: "example.com", Name: "example.com"}, {ID: "preview.example.com", Name: "preview.example.com"},
	}}}
	server := httptest.NewServer(fake)
	defer server.Close()
	provider := mustProvider(t, "gandi", "dns_gandi_token = gandi-pat\ndns_gandi_sharing_id = org-1\n", Options{BaseURL: server.URL})
	exercise(t, provider, "app.preview.example.com", "preview.example.com", "tunnel.preview.example.com", false)
	if records := fake.find("preview.example.com", "app.preview.example.com", "A"); len(records) != 1 || records[0].TTL != 300 {
		t.Fatalf("the A = %+v", records)
	}
	// The LiveDNS API key the token replaced still signs in, as Apikey.
	fake.authorization, fake.sharing = "Apikey legacy-key", ""
	legacy := mustProvider(t, "gandi", "dns_gandi_api_key = legacy-key", Options{BaseURL: server.URL})
	if zones, err := legacy.Zones(context.Background(), "x"); err != nil || len(zones) != 2 {
		t.Fatalf("with the API key: %+v %v", zones, err)
	}
}

func TestGandiRefusalsAreItsOwnWords(t *testing.T) {
	fake := &gandiFake{authorization: "Bearer right", fakeStore: fakeStore{zones: []fakeZone{{ID: "example.com", Name: "example.com"}}}}
	server := httptest.NewServer(fake)
	defer server.Close()
	_, err := mustProvider(t, "gandi", "dns_gandi_token = wrong", Options{BaseURL: server.URL}).Zones(context.Background(), "x")
	refusedInWords(t, err, "Gandi refused to list the domains", "could not verify that you authorized")
	right := mustProvider(t, "gandi", "dns_gandi_token = right", Options{BaseURL: server.URL})
	err = right.Write(context.Background(), Zone{ID: "example.com", Name: "example.com"}, nil, Set{Name: "app.example.com", Type: TypeA, Values: []string{"203.0.113.7"}, TTL: 60})
	refusedInWords(t, err, "Validation error", "rrset_ttl: Must be greater than or equal to 300.")
}
