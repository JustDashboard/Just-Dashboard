package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netvantage"
	"github.com/go-chi/chi/v5"
)

func externalRouter(t *testing.T, role auth.Role, kind string) (*Server, chi.Router) {
	s := testServer(t)
	r := chi.NewRouter()
	_, loop, _ := net.ParseCIDR("127.0.0.1/32")
	r.Use(httpx.AllowlistCIDRs([]*net.IPNet{loop}, s.Log))
	s.mountNetworkVantageMachineRoutes(r)
	r.Group(func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), &httpx.Principal{User: &auth.User{ID: 1, Username: "tester"}, Role: role, Kind: kind, IP: "127.0.0.1"})))
			})
		})
		r.Use(httpx.AuditMutations(s.Audit))
		r.Route("/network", s.mountNetworkVantageRoutes)
	})
	return s, r
}
func externalDo(r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

const externalEnrollment = `{"name":"controlled fixture","location":"declared test location","placement":"controlled_fixture","scopes":[{"id":"service","target":"fixture.private","addresses":["127.0.0.1"],"ports":[443],"families":["inet"]}]}`

func TestExternalManagementRequiresAdminAndEnrollmentSession(t *testing.T) {
	for _, role := range []auth.Role{auth.RoleReadOnly, auth.RoleLimited} {
		_, r := externalRouter(t, role, "session")
		for _, route := range []struct{ method, path, body string }{{"GET", "/network/external/vantages", ""}, {"GET", "/network/external/checks", ""}, {"GET", "/network/external/compare", ""}, {"POST", "/network/external/checks", `{}`}, {"POST", "/network/external/checks/id/cancel", `{}`}, {"POST", "/network/external/enrollments", externalEnrollment}, {"DELETE", "/network/external/vantages/id", ""}} {
			if rec := externalDo(r, route.method, route.path, route.body); rec.Code != 403 {
				t.Fatalf("role %s route %s: %d %s", role, route.path, rec.Code, rec.Body.String())
			}
		}
	}
	_, r := externalRouter(t, auth.RoleAdmin, "token")
	for _, route := range []struct{ method, path string }{{"POST", "/network/external/enrollments"}, {"DELETE", "/network/external/vantages/id"}} {
		if rec := externalDo(r, route.method, route.path, externalEnrollment); rec.Code != 403 {
			t.Fatalf("token enrolled/revoked %d %s", rec.Code, rec.Body.String())
		}
	}
}
func TestExternalAllowlistPrecedesEnrollmentAndAuditExcludesSecret(t *testing.T) {
	s, r := externalRouter(t, auth.RoleAdmin, "session")
	rec := externalDo(r, "POST", "/network/external/enrollments", externalEnrollment)
	var en netvantage.Enrollment
	if rec.Code != 201 || json.Unmarshal(rec.Body.Bytes(), &en) != nil {
		t.Fatalf("enrollment failed %d %s", rec.Code, rec.Body.String())
	}
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	claim := netvantage.SignClaim(key, netvantage.Claim{ServerKey: en.ServerKey, ID: en.Vantage.ID, Token: en.Token, PublicKey: base64.RawStdEncoding.EncodeToString(pub)})
	req := httptest.NewRequest("POST", "/probe-agent/enroll", strings.NewReader(string(netvantage.Marshal(claim))))
	req.RemoteAddr = "203.0.113.8:1234"
	off := httptest.NewRecorder()
	r.ServeHTTP(off, req)
	if off.Code != 403 {
		t.Fatalf("off-network agent reached auth %d", off.Code)
	}
	claimed := externalDo(r, "POST", "/probe-agent/enroll", string(netvantage.Marshal(claim)))
	if claimed.Code != 200 {
		t.Fatalf("off-network request consumed proof %d %s", claimed.Code, claimed.Body.String())
	}
	var audits string
	if e := s.Store.DB.QueryRow(`SELECT COALESCE(group_concat(detail),'') FROM audit_log`).Scan(&audits); e != nil || strings.Contains(audits, en.Token) || strings.Contains(audits, claim.Proof) || strings.Contains(audits, "privateKey") {
		t.Fatalf("enrollment secret audited %s %v", audits, e)
	}
	values := externalDo(r, "GET", "/network/external/vantages", "")
	if strings.Contains(values.Body.String(), en.Token) || strings.Contains(values.Body.String(), "enrollmentHash") {
		t.Fatal("enrollment secret in public inventory")
	}
}
func TestMachineSignatureRequiredBeforeProbeDecodingAndCannotAuthorizeUserFeatures(t *testing.T) {
	_, r := externalRouter(t, auth.RoleReadOnly, "session")
	req := httptest.NewRequest("POST", "/probe-agent/result", strings.NewReader("malformed"))
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Authorization", "Bearer user-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 401 || !strings.Contains(rec.Body.String(), "vantage_authentication_failed") {
		t.Fatalf("human token or malformed content bypassed machine auth %d %s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest("POST", "/network/external/checks", strings.NewReader(`{}`))
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-JD-Vantage", strings.Repeat("a", 32))
	req.Header.Set("X-JD-Probe-Signature", "machine-proof")
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("machine header widened human role %d", rec.Code)
	}
}
func TestMachineReplayAuditedAndRequestCannotSupplyShell(t *testing.T) {
	s, r := externalRouter(t, auth.RoleAdmin, "session")
	rec := externalDo(r, "POST", "/network/external/enrollments", externalEnrollment)
	var en netvantage.Enrollment
	json.Unmarshal(rec.Body.Bytes(), &en)
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	_, e := s.modules.networkVantages.Claim(context.Background(), netvantage.SignClaim(key, netvantage.Claim{ServerKey: en.ServerKey, ID: en.Vantage.ID, Token: en.Token, PublicKey: base64.RawStdEncoding.EncodeToString(pub)}))
	if e != nil {
		t.Fatal(e)
	}
	body := []byte(`{"program":"sh"}`)
	sig := netvantage.SignRequest(key, "POST", "/probe-agent/result", body, netvantage.Signature{ServerKey: en.ServerKey, ID: en.Vantage.ID, Sequence: 1, Timestamp: time.Now().Unix()})
	makeRequest := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/probe-agent/result", strings.NewReader(string(body)))
		req.RemoteAddr = "127.0.0.1:1234"
		req.Header.Set("X-JD-Vantage", sig.ID)
		req.Header.Set("X-JD-Probe-Server", sig.ServerKey)
		req.Header.Set("X-JD-Probe-Sequence", strconv.FormatInt(sig.Sequence, 10))
		req.Header.Set("X-JD-Probe-Time", strconv.FormatInt(sig.Timestamp, 10))
		req.Header.Set("X-JD-Probe-Signature", sig.Value)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	if rec = makeRequest(); rec.Code != 400 {
		t.Fatalf("request-built executable accepted %d %s", rec.Code, rec.Body.String())
	}
	if rec = makeRequest(); rec.Code != 401 {
		t.Fatalf("bad content replay accepted %d %s", rec.Code, rec.Body.String())
	}
	var count int
	if e := s.Store.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='network.vantage.request' AND status>=400`).Scan(&count); e != nil || count < 2 {
		t.Fatalf("machine refusals not audited count=%d %v", count, e)
	}
}
