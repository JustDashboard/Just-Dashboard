package dnsrecords

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// linodeFake is Linode's v4 domains and records: numeric ids, a slave
// domain the client must leave alone, one domain per page.
type linodeFake struct {
	fakeStore
	token string
}

func linodeFail(w http.ResponseWriter, status int, field, reason string) {
	item := map[string]string{"reason": reason}
	if field != "" {
		item["field"] = field
	}
	writeJSON(w, status, map[string]any{"errors": []any{item}})
}

func (f *linodeFake) render(record fakeRecord, zone fakeZone) map[string]any {
	name := Relative(record.Name, zone.Name)
	if name == "@" {
		name = ""
	}
	return map[string]any{"id": mustAtoi(record.ID), "type": record.Type, "name": name, "target": record.Value, "ttl_sec": record.TTL}
}

func (f *linodeFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record(r)
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		linodeFail(w, http.StatusUnauthorized, "", "Invalid Token")
		return
	}
	parts := segments(r.URL.Path, "/v4")
	switch {
	case r.Method == http.MethodGet && len(parts) == 1 && parts[0] == "domains":
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		data := []any{}
		if page <= len(f.zones) {
			zone := f.zones[page-1]
			data = append(data, map[string]any{"id": mustAtoi(zone.ID), "domain": zone.Name, "type": zone.Kind, "status": "active"})
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": data, "page": page, "pages": len(f.zones), "results": len(f.zones)})
	case len(parts) >= 3 && parts[0] == "domains" && parts[2] == "records":
		zone, ok := f.zone(parts[1])
		if !ok {
			linodeFail(w, http.StatusNotFound, "", "Not found")
			return
		}
		switch {
		case r.Method == http.MethodGet && len(parts) == 3:
			data := []any{}
			for _, record := range f.find(zone.ID, "", "") {
				data = append(data, f.render(record, zone))
			}
			writeJSON(w, http.StatusOK, map[string]any{"data": data, "page": 1, "pages": 1, "results": len(data)})
		case (r.Method == http.MethodPost && len(parts) == 3) || (r.Method == http.MethodPut && len(parts) == 4):
			var body linodeRecord
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				linodeFail(w, http.StatusBadRequest, "", "Malformed JSON")
				return
			}
			kind := body.Type
			if r.Method == http.MethodPut {
				for _, record := range f.find(zone.ID, "", "") {
					if record.ID == parts[3] {
						kind = record.Type
					}
				}
			}
			if ip := net.ParseIP(body.Target); (kind == "A" && (ip == nil || ip.To4() == nil)) || (kind == "AAAA" && (ip == nil || ip.To4() != nil)) {
				linodeFail(w, http.StatusBadRequest, "target", "You must enter a valid "+kind+" address")
				return
			}
			name := absolute(body.Name, zone.Name)
			if r.Method == http.MethodPost {
				if f.conflicts(zone.ID, name, body.Type) {
					linodeFail(w, http.StatusBadRequest, "name", "A CNAME record cannot share a name with another record")
					return
				}
				writeJSON(w, http.StatusOK, f.render(f.add(fakeRecord{Zone: zone.ID, Name: name, Type: body.Type, Value: body.Target, TTL: body.TTL}), zone))
				return
			}
			if !f.update(parts[3], func(record *fakeRecord) { record.Value, record.TTL = body.Target, body.TTL }) {
				linodeFail(w, http.StatusNotFound, "", "Not found")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"id": mustAtoi(parts[3])})
		default:
			linodeFail(w, http.StatusMethodNotAllowed, "", "Method Not Allowed")
		}
	default:
		linodeFail(w, http.StatusNotFound, "", "Not found")
	}
}

func TestLinodeWritesRecordsInTheMasterDomainThatHoldsTheName(t *testing.T) {
	fake := &linodeFake{token: "linode-pat", fakeStore: fakeStore{zones: []fakeZone{
		{ID: "11", Name: "example.com", Kind: "master"},
		{ID: "12", Name: "preview.example.com", Kind: "slave"},
		{ID: "13", Name: "example.org", Kind: "master"},
	}}}
	server := httptest.NewServer(fake)
	defer server.Close()
	provider := mustProvider(t, "linode", "dns_linode_key = linode-pat\ndns_linode_version = 4\n", Options{BaseURL: server.URL})
	zones, err := provider.Zones(context.Background(), "x")
	if err != nil || len(zones) != 2 {
		t.Fatalf("the slave domain is not one to write: %+v %v", zones, err)
	}
	// The slave domain is skipped, so a name inside it falls to the master
	// domain above it.
	exercise(t, provider, "app.preview.example.com", "example.com", "tunnel.example.com", false)
	if records := fake.find("11", "app.preview.example.com", "A"); len(records) != 1 || records[0].TTL != 300 {
		t.Fatalf("the A = %+v", records)
	}
}

func TestLinodeRefusalsAreItsOwnWords(t *testing.T) {
	fake := &linodeFake{token: "right", fakeStore: fakeStore{zones: []fakeZone{{ID: "11", Name: "example.com", Kind: "master"}}}}
	server := httptest.NewServer(fake)
	defer server.Close()
	_, err := mustProvider(t, "linode", "dns_linode_key = wrong", Options{BaseURL: server.URL}).Zones(context.Background(), "x")
	refusedInWords(t, err, "Linode refused to list the domains", "Invalid Token")
	right := mustProvider(t, "linode", "dns_linode_key = right", Options{BaseURL: server.URL})
	err = right.Write(context.Background(), Zone{ID: "11", Name: "example.com"}, nil, Set{Name: "app.example.com", Type: TypeA, Values: []string{"2001:db8::1"}, TTL: 300})
	refusedInWords(t, err, "target: You must enter a valid A address")
}
