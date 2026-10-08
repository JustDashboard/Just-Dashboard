package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

func dnsEvidenceClients(t *testing.T) (*client, *client, *Server, string) {
	t.Helper()
	c, s := newClient(t)
	if err := store.InitializeNetworkDNSEvidence(t.Context(), s.Store.DB); err != nil {
		t.Fatal(err)
	}
	if err := s.modules.network.ReconcileDNSEvidence(t.Context()); err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("a", 32)
	request := netx.DNSInvestigationRequest{Name: "private.corp.example", Type: "AAAA"}
	result := netx.DNSInvestigation{Version: 1, Request: request, StartedAt: time.Now(), EndedAt: time.Now(), Answers: []string{"2001:db8::7"}}
	result.Hops = []netx.DNSQueryEvidence{{Name: "alias.corp.example", Type: "CNAME", OwnerIdentity: ":1.42", AliasTarget: "private.corp.example", Answers: []string{"private.corp.example."}, DNSSEC: netx.DNSEvidenceReading{State: "validated", Basis: "native_reply", Summary: "Native authentication for this alias record."}}}
	req, _ := json.Marshal(request)
	artifact, _ := json.Marshal(result)
	if _, err := s.Store.DB.Exec(`INSERT INTO network_dns_evidence(id,request_json,started_at,ended_at,status,artifact_json) VALUES(?,?,?,?,?,?)`, id, string(req), time.Now().UnixMilli(), time.Now().UnixMilli(), "completed", string(artifact)); err != nil {
		t.Fatal(err)
	}
	viewer := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "dns-reader", auth.RoleReadOnly)}
	return c, viewer, s, id
}
func TestDNSEvidenceAPIPrivateCapabilitiesAndAudit(t *testing.T) {
	c, viewer, s, id := dnsEvidenceClients(t)
	base := "/api/v1/network/dns/evidence/"
	for _, request := range []struct{ method, path, body string }{{"GET", base, ""}, {"GET", base + id, ""}, {"GET", base + id + "/export", ""}, {"POST", base, `{"name":"private.corp.example","type":"A"}`}, {"DELETE", base + id, ""}} {
		if w := viewer.do(request.method, request.path, request.body, nil); w.Code != http.StatusForbidden {
			t.Fatalf("private route readable %s %s = %d %s", request.method, request.path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{base, base + id, base + id + "/export"} {
		w := c.do(http.MethodGet, path, "", nil)
		if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatalf("read/export %s: %d %s", path, w.Code, w.Body.String())
		}
		if path == base && strings.Contains(w.Body.String(), "2001:db8::7") {
			t.Fatal("list exposed artifact body")
		}
		if path == base && strings.Contains(w.Body.String(), "alias.corp.example") {
			t.Fatal("list exposed a private alias edge")
		}
		if strings.HasSuffix(path, "/export") {
			var exported struct {
				Version       int                   `json:"version"`
				Investigation netx.SavedDNSEvidence `json:"investigation"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &exported); err != nil || exported.Version != 1 || exported.Investigation.Result == nil || len(exported.Investigation.Result.Hops) != 1 || exported.Investigation.Result.Hops[0].AliasTarget != "private.corp.example" || exported.Investigation.Result.Hops[0].DNSSEC.Basis != "native_reply" {
				t.Fatalf("export lost immutable private alias provenance: %s %v", w.Body.String(), err)
			}
		}
	}
	if w := c.do(http.MethodPost, base, `{"name":"bad name","type":"A"}`, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid question %d %s", w.Code, w.Body.String())
	}
	if w := c.do(http.MethodPost, base, `{"name":"private.corp.example","type":"A","destinations":["8.8.8.8"]}`, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("adapter accepted alternate destination %d", w.Code)
	}
	if w := c.do(http.MethodDelete, base+id, "", nil); w.Code != http.StatusNoContent {
		t.Fatalf("delete %d %s", w.Code, w.Body.String())
	}
	if w := c.do(http.MethodGet, base+id+"/export", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("deleted artifact export %d", w.Code)
	}
	var count int
	if err := s.Store.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='network.dns.evidence.delete' AND target=? AND success=1`, id).Scan(&count); err != nil || count != 1 {
		t.Fatalf("delete audit=%d %v", count, err)
	}
}
func TestDNSEvidenceNarrowedAdminTokenCannotReadPrivateAnswers(t *testing.T) {
	_, _, s, id := dnsEvidenceClients(t)
	var adminID int64
	if err := s.Store.DB.QueryRow(`SELECT id FROM users WHERE username='tester'`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	user, err := s.Auth.UserByID(t.Context(), adminID)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := s.Auth.CreateAPIToken(t.Context(), user, "DNS reader", auth.RoleReadOnly, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	c := &client{t: t, h: s.Routes()}
	for _, path := range []string{"/api/v1/network/dns/evidence/", "/api/v1/network/dns/evidence/" + id + "/export"} {
		if w := c.do(http.MethodGet, path, "", map[string]string{"Authorization": "Bearer " + token}); w.Code != http.StatusForbidden {
			t.Fatalf("narrowed token read DNS scope: %d", w.Code)
		}
	}
}
