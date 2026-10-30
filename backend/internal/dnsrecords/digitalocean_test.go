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

// digitalOceanFake is DigitalOcean's v2 domains and domain_records, one
// domain per page with links.pages.next to follow.
type digitalOceanFake struct {
	fakeStore
	token string
}

func doFail(w http.ResponseWriter, status int, id, message string) {
	writeJSON(w, status, map[string]string{"id": id, "message": message})
}

func (f *digitalOceanFake) render(record fakeRecord, zone fakeZone) map[string]any {
	value := record.Value
	if record.Type == "CNAME" {
		value += "."
	}
	return map[string]any{"id": mustAtoi(record.ID), "type": record.Type, "name": Relative(record.Name, zone.Name), "data": value, "ttl": record.TTL}
}

func mustAtoi(text string) int {
	value, _ := strconv.Atoi(text)
	return value
}

func (f *digitalOceanFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record(r)
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		doFail(w, http.StatusUnauthorized, "unauthorized", "Unable to authenticate you")
		return
	}
	parts := segments(r.URL.Path, "/v2")
	switch {
	case r.Method == http.MethodGet && len(parts) == 1 && parts[0] == "domains":
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		domains := []any{}
		if page <= len(f.zones) {
			domains = append(domains, map[string]any{"name": f.zones[page-1].Name, "ttl": 1800})
		}
		links := map[string]any{}
		if page < len(f.zones) {
			links["pages"] = map[string]string{"next": "https://api.digitalocean.com/v2/domains?page=" + strconv.Itoa(page+1)}
		}
		writeJSON(w, http.StatusOK, map[string]any{"domains": domains, "links": links, "meta": map[string]int{"total": len(f.zones)}})
	case len(parts) >= 3 && parts[0] == "domains" && parts[2] == "records":
		zone, ok := f.zone(parts[1])
		if !ok {
			doFail(w, http.StatusNotFound, "not_found", "The resource you were accessing could not be found.")
			return
		}
		switch {
		case r.Method == http.MethodGet && len(parts) == 3:
			records := []any{}
			for _, record := range f.find(zone.ID, r.URL.Query().Get("name"), r.URL.Query().Get("type")) {
				records = append(records, f.render(record, zone))
			}
			writeJSON(w, http.StatusOK, map[string]any{"domain_records": records, "links": map[string]any{}, "meta": map[string]int{"total": len(records)}})
		case (r.Method == http.MethodPost && len(parts) == 3) || (r.Method == http.MethodPatch && len(parts) == 4):
			var body digitalOceanRecord
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				doFail(w, http.StatusBadRequest, "bad_request", "unparseable body")
				return
			}
			if body.TTL < 30 {
				doFail(w, http.StatusUnprocessableEntity, "unprocessable_entity", "Ttl must be at least 30.")
				return
			}
			if body.Type == "CNAME" && !strings.HasSuffix(body.Data, ".") && body.Data != "@" {
				doFail(w, http.StatusUnprocessableEntity, "unprocessable_entity", "Data needs to end with a dot (.)")
				return
			}
			name := absolute(body.Name, zone.Name)
			value := strings.TrimSuffix(body.Data, ".")
			if r.Method == http.MethodPost {
				if f.conflicts(zone.ID, name, body.Type) {
					doFail(w, http.StatusUnprocessableEntity, "unprocessable_entity", "CNAME records cannot share a name with other records.")
					return
				}
				record := f.add(fakeRecord{Zone: zone.ID, Name: name, Type: body.Type, Value: value, TTL: body.TTL})
				writeJSON(w, http.StatusCreated, map[string]any{"domain_record": f.render(record, zone)})
				return
			}
			if !f.update(parts[3], func(record *fakeRecord) { record.Value, record.TTL = value, body.TTL }) {
				doFail(w, http.StatusNotFound, "not_found", "The resource you were accessing could not be found.")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"domain_record": map[string]any{"id": mustAtoi(parts[3])}})
		default:
			doFail(w, http.StatusMethodNotAllowed, "method_not_allowed", r.Method)
		}
	default:
		doFail(w, http.StatusNotFound, "not_found", "The resource you were accessing could not be found.")
	}
}

func TestDigitalOceanWritesRecordsInTheDomainThatHoldsTheName(t *testing.T) {
	fake := &digitalOceanFake{token: "dop_v1_fake", fakeStore: fakeStore{zones: []fakeZone{
		{ID: "example.com", Name: "example.com"}, {ID: "example.net", Name: "example.net"}, {ID: "shop.example.com", Name: "shop.example.com"},
	}}}
	server := httptest.NewServer(fake)
	defer server.Close()
	provider := mustProvider(t, "digitalocean", "dns_digitalocean_token = dop_v1_fake", Options{BaseURL: server.URL})
	exercise(t, provider, "www.shop.example.com", "shop.example.com", "tunnel.shop.example.com", false)
	if records := fake.find("shop.example.com", "tunnel.shop.example.com", "CNAME"); len(records) != 1 || records[0].Value != "abc.cfargotunnel.com" {
		t.Fatalf("the CNAME = %+v", records)
	}
	// The apex is "@".
	zone := Zone{ID: "example.net", Name: "example.net"}
	if _, err := Apply(context.Background(), provider, zone, Diff([]Set{{Name: "example.net", Type: TypeA, Values: []string{"203.0.113.7"}, TTL: 300}}, nil, true)); err != nil {
		t.Fatal(err)
	}
	if held, err := provider.Read(context.Background(), zone, "example.net"); err != nil || len(held) != 1 || held[0].Values[0] != "203.0.113.7" {
		t.Fatalf("the apex = %+v %v", held, err)
	}
}

func TestDigitalOceanRefusalsAreItsOwnWords(t *testing.T) {
	fake := &digitalOceanFake{token: "right", fakeStore: fakeStore{zones: []fakeZone{{ID: "example.com", Name: "example.com"}}}}
	server := httptest.NewServer(fake)
	defer server.Close()
	_, err := mustProvider(t, "digitalocean", "dns_digitalocean_token = wrong", Options{BaseURL: server.URL}).Zones(context.Background(), "x")
	refusedInWords(t, err, "DigitalOcean refused to list the domains", "Unable to authenticate you (unauthorized)")
	right := mustProvider(t, "digitalocean", "dns_digitalocean_token = right", Options{BaseURL: server.URL})
	err = right.Write(context.Background(), Zone{ID: "example.com", Name: "example.com"}, nil, Set{Name: "app.example.com", Type: TypeA, Values: []string{"203.0.113.7"}, TTL: 10})
	refusedInWords(t, err, "Ttl must be at least 30.", "unprocessable_entity")
}
