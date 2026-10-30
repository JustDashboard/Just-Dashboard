package dnsrecords

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ovhFake is the OVHcloud API's /domain/zone: it keeps its own clock,
// checks every call's signature against it, and counts the refreshes that
// make a change served.
type ovhFake struct {
	fakeStore
	applicationKey, applicationSecret, consumerKey string
	clock                                          time.Time
	refreshes                                      int
}

func ovhFail(w http.ResponseWriter, status int, code, message string) {
	body := map[string]string{"message": message}
	if code != "" {
		body["errorCode"], body["httpCode"] = code, strconv.Itoa(status)+" "+http.StatusText(status)
	} else {
		body["class"] = "Client::Forbidden"
	}
	writeJSON(w, status, body)
}

func (f *ovhFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record(r)
	if r.URL.Path == "/auth/time" {
		writeJSON(w, http.StatusOK, f.clock.Unix())
		return
	}
	body, _ := io.ReadAll(r.Body)
	timestamp, _ := strconv.ParseInt(r.Header.Get("X-Ovh-Timestamp"), 10, 64)
	sum := sha1.Sum([]byte(strings.Join([]string{f.applicationSecret, r.Header.Get("X-Ovh-Consumer"), r.Method,
		"http://" + r.Host + r.URL.RequestURI(), string(body), r.Header.Get("X-Ovh-Timestamp")}, "+")))
	switch {
	case r.Header.Get("X-Ovh-Application") != f.applicationKey:
		ovhFail(w, http.StatusForbidden, "INVALID_KEY", "Invalid application key")
		return
	case r.Header.Get("X-Ovh-Signature") != "$1$"+hex.EncodeToString(sum[:]):
		ovhFail(w, http.StatusBadRequest, "INVALID_SIGNATURE", "Invalid signature")
		return
	case timestamp < f.clock.Unix()-60 || timestamp > f.clock.Unix()+60:
		ovhFail(w, http.StatusBadRequest, "QUERY_TIME_OUT", "Query out of time")
		return
	case r.Header.Get("X-Ovh-Consumer") != f.consumerKey:
		ovhFail(w, http.StatusForbidden, "", "This call has not been granted")
		return
	}
	parts := segments(r.URL.Path, "/domain/zone")
	if len(parts) == 0 && r.Method == http.MethodGet {
		names := []string{}
		for _, zone := range f.zones {
			names = append(names, zone.Name)
		}
		writeJSON(w, http.StatusOK, names)
		return
	}
	zone, ok := f.zone(parts[0])
	if !ok {
		ovhFail(w, http.StatusNotFound, "", "This service does not exist")
		return
	}
	render := func(record fakeRecord) map[string]any {
		sub := Relative(record.Name, zone.Name)
		if sub == "@" {
			sub = ""
		}
		target := record.Value
		if record.Type == "CNAME" {
			target += "."
		}
		return map[string]any{"id": mustAtoi(record.ID), "zone": zone.Name, "subDomain": sub, "fieldType": record.Type, "target": target, "ttl": record.TTL}
	}
	switch {
	case len(parts) == 2 && parts[1] == "refresh" && r.Method == http.MethodPost:
		f.refreshes++
		writeJSON(w, http.StatusOK, nil)
	case len(parts) == 2 && parts[1] == "record" && r.Method == http.MethodGet:
		ids := []int{}
		name := absolute(r.URL.Query().Get("subDomain"), zone.Name)
		for _, record := range f.find(zone.ID, name, r.URL.Query().Get("fieldType")) {
			ids = append(ids, mustAtoi(record.ID))
		}
		writeJSON(w, http.StatusOK, ids)
	case len(parts) == 3 && parts[1] == "record" && r.Method == http.MethodGet:
		for _, record := range f.find(zone.ID, "", "") {
			if record.ID == parts[2] {
				writeJSON(w, http.StatusOK, render(record))
				return
			}
		}
		ovhFail(w, http.StatusNotFound, "", "The requested object (id = "+parts[2]+") does not exist")
	case (len(parts) == 2 && r.Method == http.MethodPost) || (len(parts) == 3 && r.Method == http.MethodPut):
		var record ovhRecord
		if json.Unmarshal(body, &record) != nil {
			ovhFail(w, http.StatusBadRequest, "", "Invalid JSON")
			return
		}
		value := strings.TrimSuffix(record.Target, ".")
		name := absolute(record.SubDomain, zone.Name)
		if r.Method == http.MethodPost {
			if f.conflicts(zone.ID, name, record.FieldType) {
				ovhFail(w, http.StatusBadRequest, "", "Invalid domain: a CNAME cannot sit beside other records")
				return
			}
			writeJSON(w, http.StatusOK, render(f.add(fakeRecord{Zone: zone.ID, Name: name, Type: record.FieldType, Value: value, TTL: record.TTL})))
			return
		}
		f.update(parts[2], func(stored *fakeRecord) { stored.Value, stored.TTL = value, record.TTL })
		writeJSON(w, http.StatusOK, nil)
	default:
		ovhFail(w, http.StatusNotFound, "", "Got an invalid (or empty) URL")
	}
}

// The API's clock is two minutes ahead of this server's: the signature
// carries the API's time, read once.
func TestOVHSignsEveryCallInTheAPIsClockAndRefreshesTheZone(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	fake := &ovhFake{applicationKey: "ak", applicationSecret: "as", consumerKey: "ck", clock: now.Add(2 * time.Minute),
		fakeStore: fakeStore{zones: []fakeZone{{ID: "example.com", Name: "example.com"}, {ID: "example.eu", Name: "example.eu"}}}}
	server := httptest.NewServer(fake)
	defer server.Close()
	credentials := "dns_ovh_endpoint = ovh-eu\ndns_ovh_application_key = ak\ndns_ovh_application_secret = as\ndns_ovh_consumer_key = ck\n"
	provider := mustProvider(t, "ovh", credentials, Options{BaseURL: server.URL, Now: func() time.Time { return now }})
	exercise(t, provider, "app.example.eu", "example.eu", "tunnel.example.eu", false)
	if fake.refreshes != 4 {
		t.Fatalf("the zone was refreshed %d times after four writes", fake.refreshes)
	}
	if times := strings.Count(strings.Join(fake.requests, "\n"), "GET /auth/time"); times != 1 {
		t.Fatalf("the API's clock was read %d times", times)
	}
}

func TestOVHRefusalsAreItsOwnWords(t *testing.T) {
	now := time.Now()
	fake := &ovhFake{applicationKey: "ak", applicationSecret: "as", consumerKey: "ck", clock: now,
		fakeStore: fakeStore{zones: []fakeZone{{ID: "example.com", Name: "example.com"}}}}
	server := httptest.NewServer(fake)
	defer server.Close()
	wrong := mustProvider(t, "ovh", "dns_ovh_endpoint = ovh-eu\ndns_ovh_application_key = ak\ndns_ovh_application_secret = not-it\ndns_ovh_consumer_key = ck\n", Options{BaseURL: server.URL})
	_, err := wrong.Zones(context.Background(), "x")
	refusedInWords(t, err, "OVH refused to list the zones", "Invalid signature (INVALID_SIGNATURE)")
	ungranted := mustProvider(t, "ovh", "dns_ovh_endpoint = ovh-eu\ndns_ovh_application_key = ak\ndns_ovh_application_secret = as\ndns_ovh_consumer_key = other\n", Options{BaseURL: server.URL})
	_, err = ungranted.Zones(context.Background(), "x")
	refusedInWords(t, err, "This call has not been granted (Client::Forbidden)")
}
