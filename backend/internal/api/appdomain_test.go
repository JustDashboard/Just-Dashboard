package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accountlink"
	"github.com/Wayy01/Just-Dashboard/backend/internal/appdomain"
	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/config"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store/storetest"
)

// These tests drive the app domain's handlers and joins directly, over a
// fake account link and a fake certificate store: nothing is sent to a site,
// no certbot runs, and no route is called as a signed-in administrator.

type appLink struct {
	mu     sync.Mutex
	domain *accountlink.Domain
	added  []string
}

func (l *appLink) Enabled() bool   { return true }
func (l *appLink) SiteURL() string { return "https://just-dashboard.com" }
func (l *appLink) Linked(context.Context) (accountlink.State, bool, error) {
	return accountlink.State{ServerID: "s"}, true, nil
}
func (l *appLink) GetDomain(context.Context) (accountlink.DomainInfo, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	zone := "just-dashboard.com"
	return accountlink.DomainInfo{Available: true, Zone: &zone, MaxHandles: 3, Domain: l.domain}, nil
}
func (l *appLink) PutDomain(_ context.Context, handle, address string) (accountlink.Domain, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.domain = &accountlink.Domain{Handle: handle, Hostname: handle + ".just-dashboard.com", Address: address, Status: "active"}
	return *l.domain, nil
}
func (l *appLink) DeleteDomain(context.Context) error { return nil }
func (l *appLink) AddChallenge(_ context.Context, value string) (accountlink.ChallengeSet, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.added = append(l.added, value)
	return accountlink.ChallengeSet{Name: "_acme-challenge.acme.just-dashboard.com", Values: l.added}, nil
}
func (l *appLink) RemoveChallenge(context.Context, string) error { return nil }

type appCerts struct{ state string }

func (c *appCerts) AppDomainCertificate(_ context.Context, handle, _ string) proxysvc.AppDomainCertificateState {
	return proxysvc.AppDomainCertificateState{Name: proxysvc.AppDomainLineage(handle), State: c.state,
		CertPath: "/etc/letsencrypt/live/" + proxysvc.AppDomainLineage(handle) + "/fullchain.pem"}
}
func (c *appCerts) IssueAppDomainCertificate(_ context.Context, order proxysvc.AppDomainOrder, _ func(string, string)) (proxysvc.DeploymentCertificate, error) {
	c.state = "valid"
	return proxysvc.DeploymentCertificate{Outcome: proxysvc.CertificateIssued, Name: proxysvc.AppDomainLineage(order.Handle)}, nil
}
func (c *appCerts) ServeAppDomainRenewal(context.Context, string) error      { return nil }
func (c *appCerts) DeleteAppDomainCertificate(context.Context, string) error { return nil }

func appDomainServer(t *testing.T, autoAssign bool) (*Server, *appLink, string) {
	t.Helper()
	st, err := storetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	link := &appLink{}
	dir := t.TempDir()
	svc := appdomain.New(appdomain.Options{
		Store: st, Link: link, Certs: &appCerts{state: "valid"}, Jobs: jobs.New(slog.New(slog.DiscardHandler)),
		Enabled: true, DataDir: dir, BackendURL: "http://127.0.0.1:8080",
		PublicAddress: func() string { return "203.0.113.10" },
		WaitTXT:       func(context.Context, string, string) error { return nil },
	})
	if _, err := svc.Claim(t.Context(), "acme", "admin"); err != nil {
		t.Fatal(err)
	}
	if !autoAssign {
		if _, err := svc.SetAutoAssign(t.Context(), false); err != nil {
			t.Fatal(err)
		}
	}
	sealer, err := auth.NewSealer(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Cfg: &config.Config{}, Log: slog.New(slog.DiscardHandler), Store: st, Sealer: sealer, Audit: audit.New(st, slog.New(slog.DiscardHandler))}
	s.modules.appDomain = svc
	raw, err := os.ReadFile(filepath.Join(dir, appdomain.HookFile))
	if err != nil {
		t.Fatal(err)
	}
	var cred appdomain.HookCredential
	_ = json.Unmarshal(raw, &cred)
	return s, link, cred.Token
}

func serveHook(s *Server, remote, token string, header map[string]string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/app-domain/hook/challenge", bytes.NewReader(raw))
	r.RemoteAddr = remote
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range header {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	if err := s.handleAppDomainHook(w, r); err != nil {
		httpx.WriteError(w, r, err)
	}
	return w
}

func TestTheHookRouteAnswersOnlyALoopbackCallerWithItsToken(t *testing.T) {
	s, link, token := appDomainServer(t, true)
	value := "Zm9vYmFyYmF6cXV4MDEyMzQ1Njc4OWFiY2RlZmdoaWo"
	body := map[string]string{"action": "add", "domain": "acme.just-dashboard.com", "value": value}
	for name, tc := range map[string]struct {
		remote, token string
		header        map[string]string
		want          int
	}{
		"another machine": {"198.51.100.7:4000", token, nil, http.StatusForbidden},
		"through a proxy": {"127.0.0.1:4000", token, map[string]string{"X-Forwarded-For": "198.51.100.7"}, http.StatusForbidden},
		"no token":        {"127.0.0.1:4000", "", nil, http.StatusUnauthorized},
		"another token":   {"127.0.0.1:4000", "jdh_" + strings.Repeat("x", 43), nil, http.StatusUnauthorized},
		"the hook":        {"127.0.0.1:4000", token, nil, http.StatusNoContent},
		"over IPv6":       {"[::1]:4000", token, nil, http.StatusNoContent},
	} {
		w := serveHook(s, tc.remote, tc.token, tc.header, body)
		if w.Code != tc.want {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	if len(link.added) != 2 {
		t.Fatalf("published %v", link.added)
	}
	w := serveHook(s, "127.0.0.1:4000", token, nil, map[string]string{"action": "add", "domain": "other.just-dashboard.com", "value": value})
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "not_this_domain") {
		t.Fatalf("another handle: %d %s", w.Code, w.Body)
	}
	w = serveHook(s, "127.0.0.1:4000", token, nil, map[string]string{"action": "add", "domain": "acme.just-dashboard.com", "value": "short"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("a bad value: %d %s", w.Code, w.Body)
	}
}

type recordingIssuer struct{ asked [][]string }

func (r *recordingIssuer) EnsureDeploymentCertificate(_ context.Context, domains []string, _ func(string, string)) (proxysvc.DeploymentCertificate, error) {
	r.asked = append(r.asked, domains)
	return proxysvc.DeploymentCertificate{Outcome: proxysvc.CertificateReused, Name: "own"}, nil
}

// Lifecycle (DESIGN 3.5): moving an existing app over adds the app name
// beside its own, and the release orders nothing under the zone: the app name
// is the wildcard's and the rest go to the Certificates feature alone.
func TestTheCertificateStepGivesAppNamesTheWildcardAndTheRestTheirOwn(t *testing.T) {
	s, _, _ := appDomainServer(t, true)
	inner := &recordingIssuer{}
	issuer := appDomainCertificateIssuer{s: s, issuer: inner}
	ctx := t.Context()

	got, err := issuer.EnsureDeploymentCertificate(ctx, []string{"shop.acme.just-dashboard.com"}, nil)
	if err != nil || got.Name != "jd-app-domain-acme" || len(inner.asked) != 0 {
		t.Fatalf("app name alone = %+v, %v; inner asked %v", got, err, inner.asked)
	}
	if _, err := issuer.EnsureDeploymentCertificate(ctx, []string{"shop.example.test", "shop.acme.just-dashboard.com"}, nil); err != nil {
		t.Fatal(err)
	}
	if len(inner.asked) != 1 || strings.Join(inner.asked[0], ",") != "shop.example.test" {
		t.Fatalf("inner asked %v", inner.asked)
	}
	if _, err := issuer.EnsureDeploymentCertificate(ctx, []string{"shop.other.just-dashboard.com"}, nil); !errors.Is(err, appdomain.ErrNotHeldName) {
		t.Fatalf("another handle = %v", err)
	}
	// Lifecycle: deployments that existed before a handle was claimed keep
	// their names, and their certificates come from where they always did.
	if _, err := issuer.EnsureDeploymentCertificate(ctx, []string{"app.203-0-113-10.sslip.io"}, nil); err != nil || len(inner.asked) != 2 {
		t.Fatalf("an sslip name = %v, inner %v", err, inner.asked)
	}
	if !(appDomainNames{s: s}).DetachedAppDomain(ctx, "shop.other.just-dashboard.com") ||
		(appDomainNames{s: s}).DetachedAppDomain(ctx, "shop.acme.just-dashboard.com") ||
		(appDomainNames{s: s}).DetachedAppDomain(ctx, "shop.example.test") {
		t.Fatal("the executor's view of names")
	}
}

func askHostname(t *testing.T, s *Server, query string) hostnameSuggestion {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/deploy/hostname?"+query, nil)
	w := httptest.NewRecorder()
	if err := s.handleDeploymentHostname(w, r); err != nil {
		t.Fatal(err)
	}
	var out hostnameSuggestion
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("%s: %v", w.Body, err)
	}
	return out
}

// Lifecycle (DESIGN 3.5): a new project with autoAssign on is proposed the
// app name by default; with it off the page can still ask for it; a tunnel
// is never offered one.
func TestTheHostnameSuggestionOffersTheAppDomain(t *testing.T) {
	s, _, _ := appDomainServer(t, true)
	got := askHostname(t, s, "name=Shop")
	if got.Method != "app-domain" || got.Hostname != "shop.acme.just-dashboard.com" || got.Base != "acme.just-dashboard.com" ||
		got.AppDomain != "held" || got.CertificateMethod != "app-domain" || got.NameTaken == nil {
		t.Fatalf("default = %+v", got)
	}
	got = askHostname(t, s, "name=shop&method=app-domain&tunnel=1")
	if got.Hostname != "" || !strings.Contains(got.Detail, "Tunnel") {
		t.Fatalf("tunnel = %+v", got)
	}
	got = askHostname(t, s, "name=shop&tunnel=1")
	if got.Method == "app-domain" {
		t.Fatalf("a tunnel was offered the address: %+v", got)
	}
	// The alternative the new-project step offers beside the default is the
	// backend's own suggestion without the address, not a name it composed.
	if got := askHostname(t, s, "name=shop&method=standard"); got.Method == "app-domain" || got.AppDomain != "" {
		t.Fatalf("method=standard still answered the app domain: %+v", got)
	}

	off, _, _ := appDomainServer(t, false)
	if got := askHostname(t, off, "name=shop"); got.Method == "app-domain" {
		t.Fatalf("autoAssign off still defaulted to it: %+v", got)
	}
	if got := askHostname(t, off, "name=shop&method=app-domain"); got.Hostname != "shop.acme.just-dashboard.com" {
		t.Fatalf("asked for = %+v", got)
	}
}

func TestMapAppDomainErrorSpeaksTheDashboardsCodes(t *testing.T) {
	for err, want := range map[error]string{
		&appdomain.InUseError{Uses: []appdomain.Use{{Hostname: "shop.acme.just-dashboard.com", Project: "shop", Environment: "production"}}}: "domain_in_use",
		appdomain.ErrDisabled:      "app_domains_disabled",
		accountlink.ErrNotLinked:   "not_linked",
		appdomain.ErrInvalidHandle: "invalid_handle",
		appdomain.ErrNoAddress:     "no_public_address",
		&accountlink.DomainError{Status: 409, Code: "handle_taken", Message: "Another server already holds that handle."}: "handle_taken",
		&accountlink.DomainError{Status: 401, Code: "bad_signature", Message: "x"}:                                        "bad_signature",
	} {
		var api *httpx.APIError
		if !errors.As(mapAppDomainError(err), &api) || api.Code != want {
			t.Errorf("%v -> %v, want %s", err, mapAppDomainError(err), want)
		}
		if api != nil && api.Code == "bad_signature" && api.Status != http.StatusBadGateway {
			t.Errorf("the site refusing the server's signature is %d", api.Status)
		}
	}
}
