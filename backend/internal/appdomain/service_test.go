package appdomain

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accountlink"
	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store/storetest"
)

// fakeLink is the account link and the site behind it.
type fakeLink struct {
	mu        sync.Mutex
	linked    bool
	available bool
	domain    *accountlink.Domain
	values    []string
	calls     []string
	fail      error
}

func (f *fakeLink) Enabled() bool   { return true }
func (f *fakeLink) SiteURL() string { return "https://just-dashboard.com" }
func (f *fakeLink) Linked(context.Context) (accountlink.State, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return accountlink.State{ServerID: "server-1"}, f.linked, nil
}

func (f *fakeLink) call(name string) error {
	f.calls = append(f.calls, name)
	if !f.linked {
		return accountlink.ErrNotLinked
	}
	if f.fail != nil {
		err := f.fail
		f.fail = nil
		return err
	}
	return nil
}

func (f *fakeLink) GetDomain(context.Context) (accountlink.DomainInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("GET"); err != nil {
		return accountlink.DomainInfo{}, err
	}
	zone := "just-dashboard.com"
	info := accountlink.DomainInfo{Available: f.available, MaxHandles: 3}
	if f.available {
		info.Zone = &zone
	} else {
		reason := "APP_DOMAINS_ZONE is not set"
		info.Reason = &reason
	}
	if f.domain != nil {
		d := *f.domain
		info.Domain = &d
	}
	return info, nil
}

func (f *fakeLink) PutDomain(_ context.Context, handle, address string) (accountlink.Domain, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("PUT " + handle + " " + address); err != nil {
		return accountlink.Domain{}, err
	}
	f.domain = &accountlink.Domain{Handle: handle, Hostname: handle + ".just-dashboard.com", Address: address, Status: "active", CreatedAt: 1, UpdatedAt: 2}
	return *f.domain, nil
}

func (f *fakeLink) DeleteDomain(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("DELETE"); err != nil {
		return err
	}
	f.domain = nil
	return nil
}

func (f *fakeLink) AddChallenge(_ context.Context, value string) (accountlink.ChallengeSet, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("ADD " + value); err != nil {
		return accountlink.ChallengeSet{}, err
	}
	f.values = append(f.values, value)
	return accountlink.ChallengeSet{Name: "_acme-challenge.acme.just-dashboard.com", Values: f.values}, nil
}

func (f *fakeLink) RemoveChallenge(_ context.Context, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.call("REMOVE " + value)
}

func (f *fakeLink) callLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// fakeCerts is the lineage on disk, as the Certificates feature reads it.
type fakeCerts struct {
	mu      sync.Mutex
	state   string
	issued  []proxysvc.AppDomainOrder
	deleted []string
	fail    error
	gate    chan struct{}
}

func (c *fakeCerts) AppDomainCertificate(_ context.Context, handle, _ string) proxysvc.AppDomainCertificateState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return proxysvc.AppDomainCertificateState{Name: proxysvc.AppDomainLineage(handle), State: c.state, ExpiresAt: 1802000000,
		CertPath: "/etc/letsencrypt/live/" + proxysvc.AppDomainLineage(handle) + "/fullchain.pem"}
}

func (c *fakeCerts) IssueAppDomainCertificate(_ context.Context, order proxysvc.AppDomainOrder, _ func(string, string)) (proxysvc.DeploymentCertificate, error) {
	if c.gate != nil {
		<-c.gate
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.issued = append(c.issued, order)
	if c.fail != nil {
		return proxysvc.DeploymentCertificate{}, c.fail
	}
	if !order.DryRun {
		c.state = "valid"
	}
	return proxysvc.DeploymentCertificate{Outcome: proxysvc.CertificateIssued, Name: proxysvc.AppDomainLineage(order.Handle), Method: "app-domain"}, nil
}

func (c *fakeCerts) ServeAppDomainRenewal(context.Context, string) error { return nil }
func (c *fakeCerts) DeleteAppDomainCertificate(_ context.Context, handle string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deleted = append(c.deleted, handle)
	return nil
}

func (c *fakeCerts) orders() []proxysvc.AppDomainOrder {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]proxysvc.AppDomainOrder(nil), c.issued...)
}

type env struct {
	t       *testing.T
	st      *store.Store
	svc     *Service
	link    *fakeLink
	certs   *fakeCerts
	jobs    *jobs.Manager
	address string
	mu      sync.Mutex
	waited  []string
	waitErr error
	dir     string
}

func newEnv(t *testing.T, enabled bool) *env {
	t.Helper()
	st, err := storetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e := &env{t: t, st: st, link: &fakeLink{linked: true, available: true}, certs: &fakeCerts{state: "missing"},
		jobs: jobs.New(slog.New(slog.DiscardHandler)), address: "203.0.113.10", dir: t.TempDir()}
	e.svc = New(Options{
		Store: st, Link: e.link, Certs: e.certs, Jobs: e.jobs, Enabled: enabled,
		Audit: audit.New(st, slog.New(slog.DiscardHandler)), DataDir: e.dir, BackendURL: "http://127.0.0.1:8080",
		PublicAddress: func() string { e.mu.Lock(); defer e.mu.Unlock(); return e.address },
		WaitTXT: func(_ context.Context, name, value string) error {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.waited = append(e.waited, name+" "+value)
			return e.waitErr
		},
	})
	t.Cleanup(e.svc.Stop)
	return e
}

func (e *env) setAddress(a string) { e.mu.Lock(); e.address = a; e.mu.Unlock() }

// deployment adds a project whose environment serves hostname.
func (e *env) deployment(project, environment, hostname string) {
	e.t.Helper()
	ctx := e.t.Context()
	res, err := e.st.DB.ExecContext(ctx, `INSERT INTO deploy_projects(name, repo_path, hook_secret, hook_id, created_at) VALUES(?, '', 's', ?, 1)`, project, "hook-"+project)
	if err != nil {
		e.t.Fatal(err)
	}
	pid, _ := res.LastInsertId()
	res, err = e.st.DB.ExecContext(ctx, `INSERT INTO deploy_environments(project_id, name, slug, created_at, updated_at) VALUES(?, ?, ?, 1, 1)`, pid, environment, environment)
	if err != nil {
		e.t.Fatal(err)
	}
	eid, _ := res.LastInsertId()
	if _, err := e.st.DB.ExecContext(ctx, `INSERT INTO deploy_dependencies(environment_id, kind, ownership, resource_kind, resource_id, created_at) VALUES(?, 'domain', 'managed', 'domain', ?, 1)`, eid, hostname); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) waitIssued(n int) {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(e.certs.orders()) < n {
		if time.Now().After(deadline) {
			e.t.Fatalf("issued %d, want %d", len(e.certs.orders()), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
	for {
		e.svc.mu.Lock()
		issuing := e.svc.issuing
		e.svc.mu.Unlock()
		if !issuing {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestAClaimPublishesTheAddressAndIssuesTheWildcardInTheBackground(t *testing.T) {
	e := newEnv(t, true)
	e.certs.gate = make(chan struct{})
	v, err := e.svc.Claim(t.Context(), " Acme ", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if v.Domain == nil || v.Domain.Handle != "acme" || v.Domain.Hostname != "acme.just-dashboard.com" || v.Domain.Wildcard != "*.acme.just-dashboard.com" ||
		v.Domain.Address != "203.0.113.10" || v.Domain.Status != "active" {
		t.Fatalf("domain = %+v", v.Domain)
	}
	if v.Certificate == nil || v.Certificate.State != "issuing" || v.Certificate.Name != "jd-app-domain-acme" {
		t.Fatalf("certificate = %+v", v.Certificate)
	}
	if got := e.link.callLog(); len(got) != 1 || got[0] != "PUT acme 203.0.113.10" {
		t.Fatalf("calls = %v", got)
	}
	close(e.certs.gate)
	e.waitIssued(1)
	if order := e.certs.orders()[0]; order.Handle != "acme" || order.Zone != "just-dashboard.com" || order.Credentials != filepath.Join(e.dir, HookFile) {
		t.Fatalf("order = %+v", order)
	}
	v, _ = e.svc.View(t.Context())
	if v.Certificate.State != "valid" || v.Certificate.JobID == nil {
		t.Fatalf("after = %+v", v.Certificate)
	}

	// The hook's credential: 0600, the backend's address, a token whose
	// hash alone is stored.
	info, err := os.Stat(filepath.Join(e.dir, HookFile))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("credential = %v, %v", info, err)
	}
	raw, _ := os.ReadFile(filepath.Join(e.dir, HookFile))
	var cred HookCredential
	if err := json.Unmarshal(raw, &cred); err != nil || cred.URL != "http://127.0.0.1:8080" || !strings.HasPrefix(cred.Token, "jdh_") {
		t.Fatalf("credential = %s", raw)
	}
	if !e.svc.CheckHookToken(t.Context(), cred.Token) || e.svc.CheckHookToken(t.Context(), "jdh_wrong") || e.svc.CheckHookToken(t.Context(), "") {
		t.Fatal("the token check")
	}
	var stored string
	_ = e.st.DB.QueryRow(`SELECT value FROM settings WHERE key = ?`, hookKey).Scan(&stored)
	if strings.Contains(stored, cred.Token) {
		t.Fatal("the token itself is stored")
	}
}

func TestAClaimIsRefusedWithoutWhatItNeeds(t *testing.T) {
	e := newEnv(t, true)
	if _, err := e.svc.Claim(t.Context(), "a--b", "admin"); !errors.Is(err, ErrInvalidHandle) {
		t.Fatalf("handle = %v", err)
	}
	e.setAddress("")
	if _, err := e.svc.Claim(t.Context(), "acme", "admin"); !errors.Is(err, ErrNoAddress) {
		t.Fatalf("address = %v", err)
	}
	e.setAddress("203.0.113.10")
	e.link.linked = false
	if _, err := e.svc.Claim(t.Context(), "acme", "admin"); !errors.Is(err, accountlink.ErrNotLinked) {
		t.Fatalf("unlinked = %v", err)
	}
	off := newEnv(t, false)
	if _, err := off.svc.Claim(t.Context(), "acme", "admin"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("off = %v", err)
	}
	if v, _ := off.svc.View(t.Context()); v.Enabled || v.Reason == nil {
		t.Fatalf("off view = %+v", v)
	}
	off.svc.Start(t.Context())
	time.Sleep(20 * time.Millisecond)
	if calls := off.link.callLog(); len(calls) != 0 {
		t.Fatalf("JD_APP_DOMAINS=false sent %v", calls)
	}
}

// Lifecycle (DESIGN 3.5): changing the handle is refused while any
// deployment uses the address, and the error lists them; so is a release.
func TestAHandleInUseCannotBeChangedOrReleased(t *testing.T) {
	e := newEnv(t, true)
	if _, err := e.svc.Claim(t.Context(), "acme", "admin"); err != nil {
		t.Fatal(err)
	}
	e.waitIssued(1)
	e.deployment("shop", "production", "shop.acme.just-dashboard.com")
	e.deployment("blog", "staging", "blog.example.test")
	v, _ := e.svc.View(t.Context())
	if len(v.UsedBy) != 1 || v.UsedBy[0].Hostname != "shop.acme.just-dashboard.com" || v.UsedBy[0].Project != "shop" || v.UsedBy[0].Environment != "production" {
		t.Fatalf("usedBy = %+v", v.UsedBy)
	}
	var inUse *InUseError
	if _, err := e.svc.Claim(t.Context(), "other", "admin"); !errors.As(err, &inUse) || !strings.Contains(err.Error(), "shop.acme.just-dashboard.com") {
		t.Fatalf("change = %v", err)
	}
	if err := e.svc.Release(t.Context()); !errors.As(err, &inUse) {
		t.Fatalf("release = %v", err)
	}
	if calls := e.link.callLog(); len(calls) != 1 {
		t.Fatalf("the site was asked after a refusal: %v", calls)
	}
	// The same handle again is a repair, not a change.
	if _, err := e.svc.Claim(t.Context(), "acme", "admin"); err != nil {
		t.Fatal(err)
	}
}

func TestAReleaseGivesTheHandleBackAndRemovesTheWildcard(t *testing.T) {
	e := newEnv(t, true)
	if _, err := e.svc.Claim(t.Context(), "acme", "admin"); err != nil {
		t.Fatal(err)
	}
	e.waitIssued(1)
	if _, err := e.svc.SetAutoAssign(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Release(t.Context()); err != nil {
		t.Fatal(err)
	}
	st, _ := e.svc.state(t.Context())
	if st.Handle != "" || st.AutoAssign || st.Zone != "just-dashboard.com" {
		t.Fatalf("state = %+v", st)
	}
	if e.link.domain != nil || len(e.certs.deleted) != 1 || e.certs.deleted[0] != "acme" {
		t.Fatalf("site %+v, deleted %v", e.link.domain, e.certs.deleted)
	}
}

// Lifecycle (DESIGN 3.5): the server's IP changes, and the address is
// updated by the follow-up.
func TestTheAddressFollowsTheServer(t *testing.T) {
	e := newEnv(t, true)
	if _, err := e.svc.Claim(t.Context(), "acme", "admin"); err != nil {
		t.Fatal(err)
	}
	e.waitIssued(1)
	e.svc.followAddress(t.Context())
	if calls := e.link.callLog(); len(calls) != 1 {
		t.Fatalf("an unchanged address was sent again: %v", calls)
	}
	e.setAddress("198.51.100.20")
	e.svc.followAddress(t.Context())
	calls := e.link.callLog()
	if calls[len(calls)-1] != "PUT acme 198.51.100.20" {
		t.Fatalf("calls = %v", calls)
	}
	if v, _ := e.svc.View(t.Context()); v.Domain.Address != "198.51.100.20" {
		t.Fatalf("address = %s", v.Domain.Address)
	}
}

// Lifecycle (DESIGN 3.5): released, suspended or detached on the site, and
// the state says so; unsuspended, and it is active again.
func TestTheStateFollowsTheSite(t *testing.T) {
	e := newEnv(t, true)
	if _, err := e.svc.Claim(t.Context(), "acme", "admin"); err != nil {
		t.Fatal(err)
	}
	e.waitIssued(1)
	reason := "phishing report"
	e.link.domain.Status, e.link.domain.Reason = "suspended", &reason
	e.svc.refresh(t.Context())
	st, _ := e.svc.state(t.Context())
	if st.Status != StatusSuspended || st.Reason != reason {
		t.Fatalf("suspended = %+v", st)
	}
	if e.svc.Classify(t.Context(), "shop.acme.just-dashboard.com") != Detached {
		t.Fatal("a suspended handle's names are still served")
	}
	e.link.domain.Status, e.link.domain.Reason = "active", nil
	e.svc.refresh(t.Context())
	if st, _ = e.svc.state(t.Context()); st.Status != StatusActive {
		t.Fatalf("unsuspended = %+v", st)
	}
	// The site is unreachable: nothing changes but the error.
	e.link.fail = errors.New("connection refused")
	e.svc.refresh(t.Context())
	if st, _ = e.svc.state(t.Context()); st.Status != StatusActive || st.LastError != "connection refused" {
		t.Fatalf("unreachable = %+v", st)
	}
	// Released on the site.
	e.link.domain = nil
	e.svc.refresh(t.Context())
	if st, _ = e.svc.state(t.Context()); st.Status != StatusDetached || !strings.Contains(st.Reason, "released") {
		t.Fatalf("released = %+v", st)
	}
}

func TestUnlinkingDetachesTheHandle(t *testing.T) {
	e := newEnv(t, true)
	if _, err := e.svc.Claim(t.Context(), "acme", "admin"); err != nil {
		t.Fatal(err)
	}
	e.waitIssued(1)
	// The OnClear hook, in the transaction that ends the link.
	tx, err := e.st.DB.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := DetachInTx(t.Context(), tx, 5); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	st, _ := e.svc.state(t.Context())
	if st.Status != StatusDetached || st.UpdatedAt != 5 {
		t.Fatalf("state = %+v", st)
	}
	// And a terminal unlink, which runs no hook here, is found by the loop.
	e2 := newEnv(t, true)
	if _, err := e2.svc.Claim(t.Context(), "acme", "admin"); err != nil {
		t.Fatal(err)
	}
	e2.waitIssued(1)
	e2.link.linked = false
	e2.svc.refresh(t.Context())
	if st, _ := e2.svc.state(t.Context()); st.Status != StatusDetached {
		t.Fatalf("state = %+v", st)
	}
	// A detached handle is released here without asking the site.
	calls := len(e2.link.callLog())
	if err := e2.svc.Release(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(e2.link.callLog()) != calls {
		t.Fatal("a detached release asked the site")
	}
}

func TestNamesAreClassifiedAgainstTheHandleHeldNow(t *testing.T) {
	e := newEnv(t, true)
	// Nothing claimed: the zone is the site's own host, and nothing under
	// it is this server's.
	if e.svc.Classify(t.Context(), "shop.acme.just-dashboard.com") != Detached || e.svc.Classify(t.Context(), "shop.example.test") != NotAppDomain {
		t.Fatal("before a claim")
	}
	if _, err := e.svc.Claim(t.Context(), "acme", "admin"); err != nil {
		t.Fatal(err)
	}
	e.waitIssued(1)
	for name, want := range map[string]NameState{
		"shop.acme.just-dashboard.com": Held, "SHOP.Acme.just-dashboard.com.": Held, "acme.just-dashboard.com": Held,
		"a.b.acme.just-dashboard.com": Detached, "shop.other.just-dashboard.com": Detached, "just-dashboard.com": Detached,
		"shop.example.test": NotAppDomain, "acme.just-dashboard.com.evil.test": NotAppDomain,
	} {
		if got := e.svc.Classify(t.Context(), name); got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	if base, auto, ok := e.svc.Active(t.Context()); !ok || base != "acme.just-dashboard.com" || !auto {
		t.Fatalf("active = %q %v %v", base, auto, ok)
	}
	e.deployment("old", "production", "old.other.just-dashboard.com")
	if v, _ := e.svc.View(t.Context()); len(v.Detached) != 1 || v.Detached[0].Hostname != "old.other.just-dashboard.com" {
		t.Fatalf("detached = %+v", v.Detached)
	}
}

func TestTheHookPublishesOnlyTheHeldHandlesChallengeAndWaitsForIt(t *testing.T) {
	e := newEnv(t, true)
	value := strings.Repeat("v", 43)
	if err := e.svc.AddChallenge(t.Context(), "acme.just-dashboard.com", value); !errors.Is(err, ErrNoDomain) {
		t.Fatalf("no domain = %v", err)
	}
	if _, err := e.svc.Claim(t.Context(), "acme", "admin"); err != nil {
		t.Fatal(err)
	}
	e.waitIssued(1)
	if err := e.svc.AddChallenge(t.Context(), "other.just-dashboard.com", value); !errors.Is(err, ErrNotThisDomain) {
		t.Fatalf("other name = %v", err)
	}
	if err := e.svc.AddChallenge(t.Context(), "acme.just-dashboard.com", value); err != nil {
		t.Fatal(err)
	}
	if len(e.waited) != 1 || e.waited[0] != "_acme-challenge.acme.just-dashboard.com "+value {
		t.Fatalf("waited = %v", e.waited)
	}
	e.waitErr = errors.New("ns1 did not answer")
	if err := e.svc.AddChallenge(t.Context(), "*.acme.just-dashboard.com", value); err == nil || !strings.Contains(err.Error(), "ns1 did not answer") {
		t.Fatalf("slow zone = %v", err)
	}
	if err := e.svc.RemoveChallenge(t.Context(), "acme.just-dashboard.com", value); err != nil {
		t.Fatal(err)
	}
}

// Lifecycle (DESIGN 3.5): a redeploy of an app that uses the address needs
// nothing central: a valid wildcard is reused without asking anyone.
func TestADeploymentReusesAValidWildcardAndIssuesAMissingOne(t *testing.T) {
	e := newEnv(t, true)
	if _, err := e.svc.Claim(t.Context(), "acme", "admin"); err != nil {
		t.Fatal(err)
	}
	e.waitIssued(1)
	calls := len(e.link.callLog())
	got, err := e.svc.EnsureCertificate(t.Context(), []string{"shop.acme.just-dashboard.com"}, nil)
	if err != nil || got.Outcome != proxysvc.CertificateReused || got.Name != "jd-app-domain-acme" {
		t.Fatalf("reuse = %+v, %v", got, err)
	}
	if len(e.certs.orders()) != 1 || len(e.link.callLog()) != calls {
		t.Fatal("a valid wildcard was ordered again, or the site was asked")
	}
	e.certs.mu.Lock()
	e.certs.state = "expiring"
	e.certs.mu.Unlock()
	if got, err := e.svc.EnsureCertificate(t.Context(), []string{"shop.acme.just-dashboard.com"}, nil); err != nil || got.Outcome != proxysvc.CertificateIssued {
		t.Fatalf("renew = %+v, %v", got, err)
	}
	if _, err := e.svc.EnsureCertificate(t.Context(), []string{"shop.other.just-dashboard.com"}, nil); !errors.Is(err, ErrNotHeldName) {
		t.Fatalf("other handle = %v", err)
	}
}

func TestAFailedIssuanceIsShownUntilOneSucceeds(t *testing.T) {
	e := newEnv(t, true)
	e.certs.fail = errors.New("certbot could not issue the certificate: rate limited")
	if _, err := e.svc.Claim(t.Context(), "acme", "admin"); err != nil {
		t.Fatal(err)
	}
	e.waitIssued(1)
	v, _ := e.svc.View(t.Context())
	if v.Certificate.State != "failed" || v.Certificate.Error == nil || !strings.Contains(*v.Certificate.Error, "rate limited") {
		t.Fatalf("certificate = %+v", v.Certificate)
	}
	e.certs.mu.Lock()
	e.certs.fail = nil
	e.certs.mu.Unlock()
	if _, err := e.svc.IssueCertificate(t.Context(), IssueOptions{}, "admin"); err != nil {
		t.Fatal(err)
	}
	e.waitIssued(2)
	if v, _ := e.svc.View(t.Context()); v.Certificate.State != "valid" || v.Certificate.Error != nil {
		t.Fatalf("after = %+v", v.Certificate)
	}
}

func TestTheViewSaysWhyNothingIsOffered(t *testing.T) {
	e := newEnv(t, true)
	e.link.linked = false
	v, _ := e.svc.View(t.Context())
	if v.Linked || v.Available != nil || v.Reason == nil || !strings.Contains(*v.Reason, "not linked") || v.UsedBy == nil {
		t.Fatalf("unlinked = %+v", v)
	}
	e.link.linked, e.link.available = true, false
	e.svc.Refresh(t.Context())
	v, _ = e.svc.View(t.Context())
	if v.Available == nil || *v.Available || v.Reason == nil || *v.Reason != "APP_DOMAINS_ZONE is not set" || v.Zone != nil {
		t.Fatalf("not offered = %+v", v)
	}
	raw, _ := json.Marshal(v)
	for _, key := range []string{`"enabled":true`, `"linked":true`, `"publicAddress":"203.0.113.10"`, `"domain":null`, `"certificate":null`, `"usedBy":[]`} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("view %s misses %s", raw, key)
		}
	}
}

func TestBackendURL(t *testing.T) {
	for addr, want := range map[string]string{
		"127.0.0.1:8080": "http://127.0.0.1:8080", ":9000": "http://127.0.0.1:9000", "0.0.0.0:8080": "http://127.0.0.1:8080",
		"[::1]:8080": "http://[::1]:8080", "[::]:8080": "http://[::1]:8080", "nonsense": "",
	} {
		if got := BackendURL(addr); got != want {
			t.Errorf("%s = %q, want %q", addr, got, want)
		}
	}
}

