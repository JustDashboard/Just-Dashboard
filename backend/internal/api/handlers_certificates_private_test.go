package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// privateCertificateServer is a test server whose certificates, imports and
// private directory are the test's own.
func privateCertificateServer(t *testing.T) (*client, *Server) {
	t.Helper()
	root := t.TempDir()
	t.Cleanup(proxysvc.UseCertificateDirsForTest(filepath.Join(root, "letsencrypt"), filepath.Join(root, "imported")))
	t.Cleanup(proxysvc.UsePrivateDirForTest(filepath.Join(root, "private")))
	s := testServer(t)
	s.Cfg.NginxDir = filepath.Join(root, "nginx")
	s.initModules()
	return &client{t: t, h: s.Routes(), cookie: signIn(t, s)}, s
}

func decodeBody[T any](t *testing.T, body []byte) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return out
}

// The routes' gates: the lists and the root to everyone signed in, every
// write to system.admin, and discarding a request's key destructive with no
// typed phrase.
func TestPrivateCertificateRoutesAreGated(t *testing.T) {
	s := testServer(t)
	h := s.Routes()
	gates := routeGates(t, h)
	want := map[proxyAccess][2]int{proxyRead: {0, 1}, proxyAdmin: {1, 1}, proxyDestructive: {2, 2}}
	refused := []*client{
		{t: t, h: h, cookie: signInAs(t, s, "reader", auth.RoleReadOnly)},
		{t: t, h: h, cookie: signInAs(t, s, "limited", auth.RoleLimited)},
	}
	for _, rt := range []struct {
		method, path string
		access       proxyAccess
	}{
		{http.MethodGet, "/api/v1/certificates/csr", proxyRead},
		{http.MethodGet, "/api/v1/certificates/local-ca", proxyRead},
		{http.MethodGet, "/api/v1/certificates/local-ca/root.pem", proxyRead},
		{http.MethodPost, "/api/v1/certificates/csr", proxyAdmin},
		{http.MethodPost, "/api/v1/certificates/csr/{name}/complete", proxyAdmin},
		{http.MethodPost, "/api/v1/certificates/self-signed", proxyAdmin},
		{http.MethodPost, "/api/v1/certificates/local-ca", proxyAdmin},
		{http.MethodPost, "/api/v1/certificates/local-ca/issue", proxyAdmin},
		{http.MethodDelete, "/api/v1/certificates/csr/{name}", proxyDestructive},
	} {
		got, ok := gates[rt.method+" "+rt.path]
		if !ok {
			t.Errorf("%s %s is not mounted", rt.method, rt.path)
			continue
		}
		if got != want[rt.access] {
			t.Errorf("%s %s: %d capability checks and %d rate budgets, want %v", rt.method, rt.path, got[0], got[1], want[rt.access])
		}
		if rt.access == proxyRead {
			continue
		}
		for _, c := range refused {
			if w := c.do(rt.method, routeParam.ReplaceAllString(rt.path, "app"), `{}`, nil); w.Code != http.StatusForbidden {
				t.Errorf("%s %s: got %d, want 403", rt.method, rt.path, w.Code)
			}
		}
	}
}

func TestLocalCAThroughTheAPI(t *testing.T) {
	c, s := privateCertificateServer(t)
	state := decodeBody[map[string]any](t, c.do(http.MethodGet, "/api/v1/certificates/local-ca", "", nil).Body.Bytes())
	if state["exists"] != false || state["renewBefore"] != float64(45) {
		t.Fatalf("before: %v", state)
	}
	if w := c.do(http.MethodGet, "/api/v1/certificates/local-ca/root.pem", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("a root with none: %d", w.Code)
	}
	issue := `{"name":"nas","names":["nas.lan.test","192.168.1.10"],"keyType":"ecdsa-p256"}`
	if w := c.do(http.MethodPost, "/api/v1/certificates/local-ca/issue", issue, nil); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"no_local_ca"`) {
		t.Fatalf("issued without a local CA: %d %s", w.Code, w.Body)
	}
	if w := c.do(http.MethodPost, "/api/v1/certificates/local-ca", "", nil); w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if w := c.do(http.MethodPost, "/api/v1/certificates/local-ca", "", nil); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"local_ca_exists"`) {
		t.Fatalf("a second root: %d %s", w.Code, w.Body)
	}
	w := c.do(http.MethodPost, "/api/v1/certificates/local-ca/issue", issue, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("issue: %d %s", w.Code, w.Body)
	}
	issued := decodeBody[proxysvc.ImportResult](t, w.Body.Bytes())
	if !issued.Cert.LocalCA || !slices.Equal(issued.Cert.Domains, []string{"nas.lan.test", "192.168.1.10"}) {
		t.Fatalf("issued %+v", issued.Cert)
	}
	if w := c.do(http.MethodPost, "/api/v1/certificates/local-ca/issue", issue, nil); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"certificate_exists"`) {
		t.Fatalf("a name in use: %d %s", w.Code, w.Body)
	}
	for body, want := range map[string]int{
		`{"name":"x","names":["not a name"]}`:            http.StatusBadRequest,
		`{"name":"../x","names":["x.lan.test"]}`:         http.StatusBadRequest,
		`{"name":"x","names":["x.lan"],"keyType":"dsa"}`: http.StatusBadRequest,
		`{"name":"x","names":["x.lan"],"days":9000}`:     http.StatusBadRequest,
	} {
		if w := c.do(http.MethodPost, "/api/v1/certificates/local-ca/issue", body, nil); w.Code != want {
			t.Errorf("%s: %d %s", body, w.Code, w.Body)
		}
	}

	reader := &client{t: t, h: c.h, cookie: signInAs(t, s, "reader", auth.RoleReadOnly)}
	reading := decodeBody[struct {
		Exists bool                   `json:"exists"`
		Name   string                 `json:"name"`
		Leaves []proxysvc.LocalCALeaf `json:"leaves"`
	}](t, reader.do(http.MethodGet, "/api/v1/certificates/local-ca", "", nil).Body.Bytes())
	if !reading.Exists || len(reading.Leaves) != 1 || reading.Leaves[0].Name != "nas" {
		t.Fatalf("a reader's reading: %+v", reading)
	}
	root := reader.do(http.MethodGet, "/api/v1/certificates/local-ca/root.pem", "", nil)
	if root.Code != http.StatusOK || root.Header().Get("Content-Type") != "application/x-x509-ca-cert" ||
		!strings.HasPrefix(root.Header().Get("Content-Disposition"), `attachment; filename=just-dashboard-local-ca`) {
		t.Fatalf("root download: %d %v", root.Code, root.Header())
	}
	block, _ := pem.Decode(root.Body.Bytes())
	if block == nil || block.Type != "CERTIFICATE" || strings.Contains(root.Body.String(), "PRIVATE") {
		t.Fatalf("root body %q", root.Body)
	}
	certs := decodeBody[[]proxysvc.Certificate](t, reader.do(http.MethodGet, "/api/v1/certificates/", "", nil).Body.Bytes())
	found := false
	for _, cert := range certs {
		if cert.Name == "nas" && cert.LocalCA {
			found = true
		}
	}
	if !found {
		t.Fatalf("the inventory has no local CA leaf: %+v", certs)
	}
	if w := c.do(http.MethodPost, "/api/v1/certificates/self-signed", `{"name":"printer","names":["10.0.0.7"]}`, nil); w.Code != http.StatusOK {
		t.Fatalf("self-signed: %d %s", w.Code, w.Body)
	}

	entries, _, err := s.Audit.List(t.Context(), audit.Filter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, e := range entries {
		if strings.HasPrefix(e.Action, "certificates.") && e.Success {
			actions = append(actions, e.Action+" "+e.Target)
		}
	}
	for _, want := range []string{"certificates.local-ca.create Just Dashboard local CA", "certificates.local-ca.issue nas", "certificates.self-signed printer"} {
		if !slices.ContainsFunc(actions, func(a string) bool { return strings.HasPrefix(a, want) }) {
			t.Errorf("no audit entry %q in %v", want, actions)
		}
	}
}

func TestSigningRequestsThroughTheAPI(t *testing.T) {
	c, _ := privateCertificateServer(t)
	w := c.do(http.MethodPost, "/api/v1/certificates/csr", `{"name":"shop","names":["shop.example.com"],"keyType":"rsa-2048","subject":{"organization":"Example Ltd","country":"MD"}}`, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	request := decodeBody[proxysvc.SigningRequest](t, w.Body.Bytes())
	if request.KeyType != proxysvc.KeyRSA2048 || !strings.Contains(request.CSR, "BEGIN CERTIFICATE REQUEST") || strings.Contains(w.Body.String(), "PRIVATE KEY") {
		t.Fatalf("created %+v", request)
	}
	if w := c.do(http.MethodPost, "/api/v1/certificates/csr", `{"name":"shop","names":["shop.example.com"]}`, nil); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"request_exists"`) {
		t.Fatalf("a second request: %d %s", w.Code, w.Body)
	}
	listed := decodeBody[[]proxysvc.SigningRequest](t, c.do(http.MethodGet, "/api/v1/certificates/csr", "", nil).Body.Bytes())
	if len(listed) != 1 || listed[0].Name != "shop" {
		t.Fatalf("listed %+v", listed)
	}

	// The authority's side: sign the request's key.
	block, _ := pem.Decode([]byte(request.CSR))
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Example CA"}, IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour * 365)}
	leafDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: csr.DNSNames,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(90 * 24 * time.Hour)}, ca, csr.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	signed, _ := json.Marshal(map[string]any{"certificate": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}))})
	if w := c.do(http.MethodPost, "/api/v1/certificates/csr/nothing/complete", string(signed), nil); w.Code != http.StatusNotFound {
		t.Fatalf("completing no request: %d %s", w.Code, w.Body)
	}
	if w := c.do(http.MethodPost, "/api/v1/certificates/csr/shop/complete", `{"certificate":"nope"}`, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("no certificate: %d %s", w.Code, w.Body)
	}
	w = c.do(http.MethodPost, "/api/v1/certificates/csr/shop/complete", string(signed), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("complete: %d %s", w.Code, w.Body)
	}
	if res := decodeBody[proxysvc.ImportResult](t, w.Body.Bytes()); res.Name != "shop" || res.Replaced {
		t.Fatalf("completed %+v", res)
	}
	if listed := decodeBody[[]proxysvc.SigningRequest](t, c.do(http.MethodGet, "/api/v1/certificates/csr", "", nil).Body.Bytes()); len(listed) != 0 {
		t.Fatalf("still waiting: %+v", listed)
	}

	// Discarding asks for no phrase.
	if w := c.do(http.MethodPost, "/api/v1/certificates/csr", `{"name":"spare","names":["10.0.0.3"]}`, nil); w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if w := c.do(http.MethodDelete, "/api/v1/certificates/csr/spare", "", nil); w.Code != http.StatusNoContent {
		t.Fatalf("discard: %d %s", w.Code, w.Body)
	}
	if w := c.do(http.MethodDelete, "/api/v1/certificates/csr/spare", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("discard again: %d %s", w.Code, w.Body)
	}
}

// One pass of the daily renewal, with its clock moved: it renews what is
// due, keeps the reading the page shows, and records the renewal as the
// system's own.
func TestLocalCARenewalRecordsItsPass(t *testing.T) {
	c, s := privateCertificateServer(t)
	if w := c.do(http.MethodPost, "/api/v1/certificates/local-ca", "", nil); w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if w := c.do(http.MethodPost, "/api/v1/certificates/local-ca/issue", `{"name":"nas","names":["nas.lan.test"]}`, nil); w.Code != http.StatusOK {
		t.Fatalf("issue: %d %s", w.Code, w.Body)
	}
	renewal := s.modules.proxyExtras.localCA
	if last, next := renewal.reading(); last != nil || !next.IsZero() {
		t.Fatalf("a loop never started read %v, %v", last, next)
	}
	renewal.Stop()
	moved := time.Now().Add(360 * 24 * time.Hour)
	renewal.now = func() time.Time { return moved }
	check := renewal.check(context.Background())
	if !slices.Equal(check.Renewed, []string{"nas"}) || check.Error != "" {
		t.Fatalf("check %+v", check)
	}
	reading := decodeBody[struct {
		LastCheck *proxysvc.LocalCACheck `json:"lastCheck"`
		NextCheck time.Time              `json:"nextCheck"`
	}](t, c.do(http.MethodGet, "/api/v1/certificates/local-ca", "", nil).Body.Bytes())
	if reading.LastCheck == nil || !slices.Equal(reading.LastCheck.Renewed, []string{"nas"}) || !reading.NextCheck.Equal(moved.Add(24*time.Hour)) {
		t.Fatalf("reading %+v", reading)
	}
	entries, _, err := s.Audit.List(t.Context(), audit.Filter{Action: "certificates.local-ca.renew"})
	if err != nil || len(entries) != 1 || entries[0].Actor != "system" || entries[0].Target != "nas" || !entries[0].Success {
		t.Fatalf("audit %+v, %v", entries, err)
	}
	// Nothing due is not worth an entry.
	renewal.check(context.Background())
	if entries, _, _ := s.Audit.List(t.Context(), audit.Filter{Action: "certificates.local-ca.renew"}); len(entries) != 1 {
		t.Fatalf("a pass that renewed nothing was recorded: %+v", entries)
	}
}

func TestLocalCARenewalLoopRunsAndStops(t *testing.T) {
	_, s := privateCertificateServer(t)
	renewal := s.modules.proxyExtras.localCA
	renewal.delay, renewal.interval = 10*time.Millisecond, time.Hour
	renewal.Start(context.Background())
	deadline := time.Now().Add(5 * time.Second)
	for {
		if last, _ := renewal.reading(); last != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the first check never ran")
		}
		time.Sleep(10 * time.Millisecond)
	}
	renewal.Stop()
	renewal.Stop()
}
