package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dnsservice"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
)

func TestDNSServiceHandoffsJoinDetectionAndConnections(t *testing.T) {
	owned := "4b1c" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789ab"
	detected := []netx.Adblock{
		{Kind: "adguardhome", Name: "AdGuard Home", RunsAs: "container", Container: "adguard", ContainerID: "9f00aa", WebPort: 3000, WebAddress: "0.0.0.0"},
		{Kind: "pihole", Name: "Pi-hole", RunsAs: "container", Container: "jd-dns-x", ContainerID: owned, WebPort: 40080, WebAddress: "127.0.0.1"},
		{Kind: "technitium", Name: "Technitium DNS Server", RunsAs: "container", Container: "tech", WebPort: 5380, WebAddress: "10.0.0.5"},
		{Kind: "adguardhome", Name: "AdGuard Home", RunsAs: "process"},
	}
	connections := []dnsservice.Connection{
		{ID: "a1", Name: "Home filter", Engine: dnsservice.AdGuard, Endpoint: "http://127.0.0.1:3000", Ownership: "connected"},
		{ID: "a2", Name: "Other AdGuard", Engine: dnsservice.AdGuard, Endpoint: "http://127.0.0.1:3001", Ownership: "connected"},
		{ID: "p1", Name: "Owned Pi-hole", Engine: dnsservice.PiHole, Endpoint: "http://127.0.0.1:40080", Ownership: "owned", ContainerID: owned, Management: true},
		{ID: "t1", Name: "Pi-hole on that port", Engine: dnsservice.PiHole, Endpoint: "http://127.0.0.1:5380", Ownership: "connected"},
	}
	got := dnsServiceHandoffs(detected, connections)
	if len(got) != 4 {
		t.Fatalf("handoffs = %+v", got)
	}
	if h := got[0]; h.Engine != "adguard" || h.Endpoint != "http://127.0.0.1:3000" || len(h.Connections) != 1 || h.Connections[0].ID != "a1" || h.Connections[0].Match != "endpoint" {
		t.Fatalf("adguard = %+v", h)
	}
	if h := got[1]; len(h.Connections) != 1 || h.Connections[0].Match != "container" || !h.Connections[0].Management || h.Connections[0].Ownership != "owned" {
		t.Fatalf("owned pihole = %+v", h)
	}
	// A web port bound to another address has no origin a credential may go
	// to, and a connection of another engine on the same port is not this one.
	if h := got[2]; h.Engine != "technitium" || h.Endpoint != "" || h.EndpointProblem == "" || len(h.Connections) != 0 {
		t.Fatalf("technitium = %+v", h)
	}
	if h := got[3]; h.Endpoint != "" || h.EndpointProblem == "" || h.Connections == nil || len(h.Connections) != 0 {
		t.Fatalf("process without a web port = %+v", h)
	}
}

func TestDNSServiceHandoffRouteIsPrivate(t *testing.T) {
	admin, viewer, _ := networkClients(t)
	if w := viewer.do(http.MethodGet, "/api/v1/network/dns/services/handoffs", "", nil); w.Code != http.StatusForbidden {
		t.Fatalf("readonly handoffs = %d", w.Code)
	}
	w := admin.do(http.MethodGet, "/api/v1/network/dns/services/handoffs", "", nil)
	var rows []dnsServiceHandoff
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "private, no-store" || json.Unmarshal(w.Body.Bytes(), &rows) != nil || rows == nil {
		t.Fatalf("handoffs %d %s", w.Code, w.Body.String())
	}
}

// The native DHCP reading is as private as the connection it belongs to.
func TestDNSServiceDHCPRouteIsPrivate(t *testing.T) {
	admin, viewer, _ := networkClients(t)
	path := "/api/v1/network/dns/services/" + strings.Repeat("a", 32) + "/dhcp"
	if w := viewer.do(http.MethodGet, path, "", nil); w.Code != http.StatusForbidden {
		t.Fatalf("readonly dhcp = %d", w.Code)
	}
	if w := admin.do(http.MethodGet, path, "", nil); w.Code != http.StatusNotFound || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("unknown connection dhcp = %d %s", w.Code, w.Body.String())
	}
}
