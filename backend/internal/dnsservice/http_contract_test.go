package dnsservice

import (
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDNSNativeTLSIdentityRedirectAndProxyBoundary(t *testing.T) {
	var calls atomic.Int32
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.Write([]byte(`{"ok":true}`)) }))
	defer tlsServer.Close()
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: tlsServer.Certificate().Raw}))
	req := ConnectionRequest{Name: "TLS engine", Engine: AdGuard, Endpoint: tlsServer.URL, CA: ca, ServerName: "example.com", Credential: Credential{Username: "test", Password: "private-native-password"}}
	c, err := newNativeClient(req)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	var response map[string]bool
	if err = c.request(t.Context(), http.MethodGet, "/control/status", nil, &response); err != nil || c.transport.State != "verified_https" || !response["ok"] {
		t.Fatalf("verified TLS: %v %+v", err, c.transport)
	}
	req.ServerName = "wrong.example.invalid"
	wrong, err := newNativeClient(req)
	if err != nil {
		t.Fatal(err)
	}
	defer wrong.close()
	if err = wrong.request(t.Context(), http.MethodGet, "/control/status", nil, &response); err == nil || calls.Load() != 1 {
		t.Fatalf("wrong certificate identity accepted: %v calls=%d", err, calls.Load())
	}
	req.CA = ""
	req.ServerName = "example.com"
	untrusted, err := newNativeClient(req)
	if err != nil {
		t.Fatal(err)
	}
	defer untrusted.close()
	if err = untrusted.request(t.Context(), http.MethodGet, "/control/status", nil, &response); err == nil || calls.Load() != 1 {
		t.Fatalf("untrusted certificate accepted: %v", err)
	}
	var forwarded atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Add(1) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/secret", http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	t.Setenv("HTTP_PROXY", target.URL)
	t.Setenv("HTTPS_PROXY", target.URL)
	t.Setenv("ALL_PROXY", target.URL)
	req.Endpoint, req.CA, req.ServerName = redirect.URL, "", ""
	redirectClient, err := newNativeClient(req)
	if err != nil {
		t.Fatal(err)
	}
	defer redirectClient.close()
	if err = redirectClient.request(t.Context(), http.MethodGet, "/control/status", nil, &response); err == nil || forwarded.Load() != 0 {
		t.Fatalf("redirect/proxy forwarded secret: %v calls=%d", err, forwarded.Load())
	}
}

func TestDNSNativePiHoleSessionHeaderAndVersionContract(t *testing.T) {
	var login, logout atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" && r.URL.Path != "/api/queries" {
			t.Errorf("unexpected auth query %s", r.URL.RawQuery)
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("Pi-hole auth used cookie or bearer")
		}
		var value any
		if r.URL.Path == "/api/auth" && r.Method == http.MethodPost {
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["password"] != "private-ftl-password" || r.Header.Get("X-FTL-SID") != "" {
				t.Error("wrong initial FTL session auth")
			}
			login.Add(1)
			value = map[string]any{"session": map[string]any{"valid": true, "sid": "private-session-id"}}
		} else {
			if r.Header.Get("X-FTL-SID") != "private-session-id" {
				t.Error("FTL SID header missing")
			}
			switch r.URL.Path {
			case "/api/auth":
				if r.Method != http.MethodDelete {
					t.Error("invalid logout method")
				}
				logout.Add(1)
				w.WriteHeader(204)
				return
			case "/api/info/version":
				value = map[string]any{"version": map[string]any{"ftl": map[string]any{"local": map[string]any{"version": "v6.7.1"}}}}
			case "/api/info/ftl":
				value = map[string]any{"ftl": map[string]any{"pid": 123, "uptime": 10000}}
			case "/api/config":
				value = map[string]any{"config": map[string]any{"dns": map[string]any{"upstreams": []string{"192.0.2.53#5353"}, "listeningMode": "LOCAL", "port": 53, "hosts": []string{"192.0.2.7 private.example"}, "cnameRecords": []string{}}}}
			case "/api/dns/blocking":
				value = map[string]any{"blocking": "enabled", "timer": nil}
			case "/api/clients":
				value = map[string]any{"clients": []any{map[string]any{"client": "10.0.0.2", "comment": "private-session-id", "groups": []int{0}}}}
			case "/api/groups":
				value = map[string]any{"groups": []any{map[string]any{"id": 0, "name": "Default", "enabled": true}}}
			case "/api/queries":
				if r.URL.Query().Get("length") != "100" {
					t.Error("unbounded native history")
				}
				value = map[string]any{"queries": []any{}}
			default:
				t.Errorf("unexpected native path %s", r.URL.Path)
				w.WriteHeader(404)
				return
			}
		}
		json.NewEncoder(w).Encode(value)
	}))
	defer server.Close()
	s, err := inspectNative(t.Context(), ConnectionRequest{Name: "FTL", Engine: PiHole, Endpoint: server.URL, Credential: Credential{Password: "private-ftl-password"}})
	if err != nil {
		t.Fatal(err)
	}
	if login.Load() != 1 || logout.Load() != 1 || s.Version != "v6.7.1" || len(s.LocalOverrides) != 1 || s.Clients[0].Name != "[redacted]" {
		t.Fatalf("FTL native contract lost: %+v auth %d/%d", s, login.Load(), logout.Load())
	}
}

func TestDNSNativeTechnitiumBearerPOSTAndTypedNativeViews(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer private-native-token" {
			t.Error("Technitium token or method escaped the closed contract")
		}
		r.ParseForm()
		if r.Form.Get("token") != "" {
			t.Error("token exposed in form")
		}
		var value any
		switch r.URL.Path {
		case "/api/settings/get":
			value = map[string]any{"version": "15.6.0", "dnsServerLocalEndPoints": []string{"0.0.0.0:53", "[::]:53"}, "recursion": "UseSpecifiedNetworkACL", "recursionNetworkACL": []string{"10.0.0.0/24", "!0.0.0.0/0"}, "enableBlocking": true, "forwarders": []string{"192.0.2.53:53"}, "forwarderProtocol": "Udp"}
		case "/api/zones/list":
			value = map[string]any{"zones": []any{map[string]any{"name": "private.example", "type": "Primary", "dnssecStatus": "Unsigned"}}, "totalZones": 1, "totalPages": 1}
		case "/api/apps/list":
			value = map[string]any{"apps": []any{map[string]any{"name": "Split Horizon", "dnsApps": []any{map[string]any{"classPath": "SplitHorizon.SimpleAddress"}}}, map[string]any{"name": "Advanced Blocking", "dnsApps": []any{map[string]any{"classPath": "AdvancedBlocking.App"}}}}}
		case "/api/apps/config/get":
			if r.Form.Get("name") == "Split Horizon" {
				value = map[string]any{"config": `{"networkGroupMap":{"10.0.0.0/24":"office"},"groups":[{"name":"office","enabled":true,"externalToInternalTranslation":{"192.0.2.7":"10.0.0.7"}}]}`}
			} else {
				value = map[string]any{"config": `{"enableBlocking":false,"networkGroupMap":{"10.0.0.0/24":"office"},"groups":[{"name":"office","enableBlocking":true}]}`}
			}
		default:
			t.Errorf("unexpected native path %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "ok", "response": value})
	}))
	defer server.Close()
	s, err := inspectNative(t.Context(), ConnectionRequest{Name: "Native DNS", Engine: Technitium, Endpoint: server.URL, Credential: Credential{Token: "private-native-token"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Zones) != 1 || s.Views.State != "configured" || len(s.ViewGroups) != 1 || len(s.FilterGroups) != 1 || s.AppProtection == nil || *s.AppProtection || !s.Protection || s.QueryEvidence.State != "unsupported" {
		t.Fatalf("native role/settings conflated: %+v", s)
	}
}

func TestDNSNativeResponseBoundsAndErrorSecretRefusal(t *testing.T) {
	for _, kind := range []string{"error", "oversized", "missing"} {
		t.Run(kind, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch kind {
				case "error":
					http.Error(w, "private-native-password", 500)
				case "oversized":
					w.Write([]byte(strings.Repeat("x", maxNativeBody+1)))
				default:
					w.Write([]byte(`{}`))
				}
			}))
			defer server.Close()
			req := ConnectionRequest{Name: "Native", Engine: AdGuard, Endpoint: server.URL, Credential: Credential{Username: "test", Password: "private-native-password"}}
			_, err := inspectNative(t.Context(), req)
			if err == nil || strings.Contains(err.Error(), req.Credential.Password) {
				t.Fatalf("missing contract/secret body accepted: %v", err)
			}
		})
	}
}
