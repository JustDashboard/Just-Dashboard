package httpx

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/agent"
)

// selfSigned stands in for the certificate a hub, or anybody else, presents.
func selfSigned(t *testing.T, name string) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

// Agent mode's listener is the one dashboard socket a hub on another machine
// reaches (invariant 7). Past the allowlist, every feature route is behind
// HubOnly: the enrolled hub's certificate and nothing else, and nothing at
// all before a hub is enrolled.
func TestHubOnlyAdmitsTheEnrolledHubsCertificateAlone(t *testing.T) {
	id, err := agent.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	hub, stranger := selfSigned(t, "hub"), selfSigned(t, "stranger")
	var reached *Principal
	h := HubOnly(id)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached, _ = PrincipalFrom(r.Context())
	}))
	call := func(state *tls.ConnectionState) *httptest.ResponseRecorder {
		reached = nil
		r := httptest.NewRequest(http.MethodGet, "/api/v1/system/info", nil)
		r.TLS = state
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	refused := func(what string, state *tls.ConnectionState, status int, code string) {
		t.Helper()
		w := call(state)
		if w.Code != status || !strings.Contains(w.Body.String(), code) || reached != nil {
			t.Errorf("%s: %d %s, reached %v; want %d %s", what, w.Code, w.Body.String(), reached != nil, status, code)
		}
	}
	presenting := func(c *x509.Certificate) *tls.ConnectionState {
		return &tls.ConnectionState{PeerCertificates: []*x509.Certificate{c}}
	}

	refused("plain HTTP", nil, http.StatusUnauthorized, "hub_certificate_required")
	refused("TLS with no certificate", &tls.ConnectionState{}, http.StatusUnauthorized, "hub_certificate_required")
	refused("before enrolment", presenting(hub), http.StatusServiceUnavailable, "not_enrolled")

	token, err := id.NewEnrolmentToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := id.Enrol(token, hub.Raw); err != nil {
		t.Fatal(err)
	}
	refused("another certificate", presenting(stranger), http.StatusForbidden, "hub_certificate_unknown")

	if w := call(presenting(hub)); w.Code != http.StatusOK || reached == nil || reached.Kind != KindHub {
		t.Fatalf("the enrolled hub: %d %s, principal %+v", w.Code, w.Body.String(), reached)
	}
}
