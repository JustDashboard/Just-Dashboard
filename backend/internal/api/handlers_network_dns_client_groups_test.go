package api

import (
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

func TestDNSServiceClientGroupAPIRejectsMalformedMembershipBeforeSelection(t *testing.T) {
	admin, s := newClient(t)
	if err := store.InitializeNetworkDNSServices(t.Context(), s.Store.DB); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/network/dns/services/" + strings.Repeat("a", 32) + "/changes"
	for _, fields := range []string{`"groups":[null]`, `"groups":[0,null]`, `"groups":[true]`, `"groups":["0"]`, `"groups":[0.0]`, `"groups":null`, `"groups":[],"nativePayload":true`} {
		body := `{"action":"client_groups","client":{"address":"198.51.100.77",` + fields + `}}`
		response := admin.do("POST", path, body, nil)
		if response.Code != 400 || response.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatalf("invalid membership reached selection: %d %s", response.Code, fields)
		}
	}
	for _, groups := range []string{"[]", "[0]"} {
		body := `{"action":"client_groups","client":{"address":"198.51.100.77","groups":` + groups + `}}`
		if response := admin.do("POST", path, body, nil); response.Code != 404 {
			t.Fatalf("valid membership did not pass decoding to the missing connection: %d", response.Code)
		}
	}
	var count int
	if err := s.Store.DB.QueryRow(`SELECT count(*) FROM network_dns_service_changes`).Scan(&count); err != nil || count != 0 {
		t.Fatal("invalid membership created a retained claim", err, count)
	}
}
