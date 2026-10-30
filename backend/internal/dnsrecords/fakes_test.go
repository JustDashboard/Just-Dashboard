package dnsrecords

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// The fakes below follow each provider's documented API closely enough
// that the client's requests are the ones the real service takes: paths,
// methods, pagination, authentication and the error documents. They share
// one store of zones and records, kept one record per value; a provider
// that speaks in sets groups them.

type fakeZone struct {
	ID, Name, Account string
	Private           bool
	// Kind is Linode's master or slave.
	Kind string
}

type fakeRecord struct {
	ID, Zone, Name, Type, Value string
	TTL                         int
	Proxied                     bool
	Comment                     string
}

type fakeStore struct {
	mu       sync.Mutex
	zones    []fakeZone
	records  []fakeRecord
	next     int
	requests []string
}

func (f *fakeStore) zone(id string) (fakeZone, bool) {
	for _, zone := range f.zones {
		if zone.ID == id {
			return zone, true
		}
	}
	return fakeZone{}, false
}

func (f *fakeStore) find(zone, name, kind string) []fakeRecord {
	out := []fakeRecord{}
	for _, record := range f.records {
		if record.Zone == zone && (name == "" || record.Name == Canonical(name)) && (kind == "" || record.Type == kind) {
			out = append(out, record)
		}
	}
	return out
}

func (f *fakeStore) add(record fakeRecord) fakeRecord {
	f.next++
	record.ID = strconv.Itoa(f.next)
	record.Name = Canonical(record.Name)
	f.records = append(f.records, record)
	return record
}

func (f *fakeStore) update(id string, change func(*fakeRecord)) bool {
	for index := range f.records {
		if f.records[index].ID == id {
			change(&f.records[index])
			return true
		}
	}
	return false
}

// replaceSet is a set provider's write: every record of the name and type
// replaced by the values.
func (f *fakeStore) replaceSet(zone, name, kind string, values []string, ttl int) {
	kept := f.records[:0]
	for _, record := range f.records {
		if !(record.Zone == zone && record.Name == Canonical(name) && record.Type == kind) {
			kept = append(kept, record)
		}
	}
	f.records = kept
	for _, value := range values {
		f.add(fakeRecord{Zone: zone, Name: name, Type: kind, Value: value, TTL: ttl})
	}
}

// conflicts is the rule every DNS server keeps: a CNAME stands alone.
func (f *fakeStore) conflicts(zone, name, kind string) bool {
	for _, record := range f.find(zone, name, "") {
		if (kind == "CNAME") != (record.Type == "CNAME") {
			return true
		}
	}
	return false
}

type fakeSet struct {
	Name, Type string
	TTL        int
	Values     []string
}

// sets groups a name's records, in name and then type order.
func (f *fakeStore) sets(zone string) []fakeSet {
	grouped := map[[2]string]*fakeSet{}
	keys := [][2]string{}
	for _, record := range f.records {
		if record.Zone != zone {
			continue
		}
		key := [2]string{record.Name, record.Type}
		set, ok := grouped[key]
		if !ok {
			set = &fakeSet{Name: record.Name, Type: record.Type, TTL: record.TTL}
			grouped[key] = set
			keys = append(keys, key)
		}
		set.Values = append(set.Values, record.Value)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	out := make([]fakeSet, 0, len(keys))
	for _, key := range keys {
		out = append(out, *grouped[key])
	}
	return out
}

func (f *fakeStore) record(r *http.Request) {
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// segments is a path after a prefix, split.
func segments(path, prefix string) []string {
	rest := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if rest == "" {
		return nil
	}
	return strings.Split(rest, "/")
}

func mustProvider(t *testing.T, key, credentials string, options Options) Provider {
	t.Helper()
	provider, err := New(key, credentials, options)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}
