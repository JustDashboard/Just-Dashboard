package accountlink

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSignedRequestsRoundTripAgainstTheSite(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	st := e.link(ScopeObserve)
	id, _, err := loadIdentity(t.Context(), e.st, e.svc.sealer)
	if err != nil || id == nil {
		t.Fatalf("identity: %v", err)
	}
	c := e.svc.newClient(id)
	for i := 0; i < 3; i++ {
		if _, err := c.heartbeat(t.Context(), st.ServerID, []byte(`{"protocol":1,"sentAt":1}`)); err != nil {
			t.Fatalf("heartbeat %d: %v", i, err)
		}
	}
	if got := len(e.site.heartbeatBodies()); got != 3 {
		t.Fatalf("site recorded %d heartbeats", got)
	}
}

func TestRequestsCarryTheProtocolHeaderAndAnIdentifyingAgentButNoSecret(t *testing.T) {
	t.Parallel()
	var seen http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		writeJSON(w, 200, siteInfo{Protocol: []int{1}})
	}))
	defer srv.Close()
	e := newEnv(t, func(o *Options) { o.SiteURL = srv.URL })
	id, err := ensureIdentity(t.Context(), e.st, e.svc.sealer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.newClient(id).site(t.Context()); err != nil {
		t.Fatal(err)
	}
	if seen.Get(headerProtocol) != "1" || !strings.HasPrefix(seen.Get("User-Agent"), "just-dashboard/0.8.2") {
		t.Fatalf("headers = %v", seen)
	}
	for _, h := range []string{"Cookie", "Authorization", headerSignature, headerServer} {
		if seen.Get(h) != "" {
			t.Fatalf("unsigned request carried %s", h)
		}
	}
}

func TestSequenceNumbersOnlyGoUpAndSurviveARestart(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	last := int64(0)
	for i := 0; i < 5; i++ {
		n, err := e.svc.kv.nextSequence(t.Context())
		if err != nil || n <= last {
			t.Fatalf("sequence %d after %d (%v)", n, last, err)
		}
		last = n
	}
	// A new process over the same store carries on from the stored number: the
	// counter is read back from disk, never kept in memory.
	again := New(Options{Store: e.st, Sealer: e.svc.sealer, Enabled: true})
	n, err := again.kv.nextSequence(t.Context())
	if err != nil || n != last+1 {
		t.Fatalf("after restart got %d (%v), want %d", n, err, last+1)
	}
}

func TestASequenceTheSiteRefusesIsRetriedWithANewOne(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	st := e.link(ScopeObserve)
	// The site has seen a higher number than this process is about to take,
	// as when two requests overtake one another on the way.
	e.site.mu.Lock()
	e.site.servers[st.ServerID].seq += 2
	e.site.mu.Unlock()
	id, _, _ := loadIdentity(t.Context(), e.st, e.svc.sealer)
	if _, err := e.svc.newClient(id).heartbeat(t.Context(), st.ServerID, []byte(`{}`)); err != nil {
		t.Fatalf("the refused sequence was not retried: %v", err)
	}
}

func TestResponsesAreDecodedStrictly(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case basePath + "/site":
			_, _ = w.Write([]byte(`{"protocol":[1],"siteKeys":[],"approveUrl":"x","surprise":true}`))
		default:
			_, _ = w.Write([]byte(`{"protocol":[1],"siteKeys":[],"approveUrl":"x"} {"second":1}`))
		}
	}))
	defer srv.Close()
	e := newEnv(t, func(o *Options) { o.SiteURL = srv.URL })
	id, _ := ensureIdentity(t.Context(), e.st, e.svc.sealer)
	c := e.svc.newClient(id)
	if _, err := c.site(t.Context()); err == nil || !strings.Contains(err.Error(), "protocol 1") {
		t.Fatalf("unknown field accepted: %v", err)
	}
	var out siteInfo
	if _, err := c.call(t.Context(), http.MethodGet, basePath+"/other", nil, "", 0, &out); err == nil {
		t.Fatal("trailing content accepted")
	}
}

func TestRedirectsAreNeverFollowed(t *testing.T) {
	t.Parallel()
	elsewhere := 0
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { elsewhere++ }))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	e := newEnv(t, func(o *Options) { o.SiteURL = srv.URL })
	id, _ := ensureIdentity(t.Context(), e.st, e.svc.sealer)
	if _, err := e.svc.newClient(id).site(t.Context()); err == nil {
		t.Fatal("a redirect was treated as an answer")
	}
	if elsewhere != 0 {
		t.Fatal("the redirect was followed")
	}
}

func TestResponsesAboveTheLimitAreRefused(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat(" ", maxResponseBody+10)))
	}))
	defer srv.Close()
	e := newEnv(t, func(o *Options) { o.SiteURL = srv.URL })
	id, _ := ensureIdentity(t.Context(), e.st, e.svc.sealer)
	if _, err := e.svc.newClient(id).site(t.Context()); err == nil || !strings.Contains(err.Error(), "larger") {
		t.Fatalf("oversize response accepted: %v", err)
	}
}

func TestANoContentAnswerNeedsNoBody(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	st := e.link(ScopeObserve)
	id, _, _ := loadIdentity(t.Context(), e.st, e.svc.sealer)
	c := e.svc.newClient(id)
	if err := c.result(t.Context(), st.ServerID, ulid(1), CommandResult{Status: resultSucceeded}); err != nil {
		t.Fatalf("204 treated as an error: %v", err)
	}
	if err := c.unlink(t.Context(), st.ServerID); err != nil {
		t.Fatalf("unlink: %v", err)
	}
}

func TestErrorsKeepTheSitesCodeAndAreBounded(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiError(w, 429, "rate_limited", strings.Repeat("slow down ", 100)+"\x07")
	}))
	defer srv.Close()
	e := newEnv(t, func(o *Options) { o.SiteURL = srv.URL })
	id, _ := ensureIdentity(t.Context(), e.st, e.svc.sealer)
	_, err := e.svc.newClient(id).site(t.Context())
	if !isCode(err, 429, "rate_limited") {
		t.Fatalf("code lost: %v", err)
	}
	if len(err.Error()) > 500 || strings.Contains(err.Error(), "\x07") {
		t.Fatalf("error text not bounded or cleaned: %q", err.Error())
	}
}

func TestALongPollAsksForItsWaitAndIsAllowedToHoldForIt(t *testing.T) {
	t.Parallel()
	// The long poll holds for up to 25 seconds, so a flat 15 second timeout
	// would cut every empty poll off; the wait is added to the allowance.
	if requestTimeout != 15*time.Second {
		t.Fatalf("request timeout = %s", requestTimeout)
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("wait"); got != strconv.Itoa(25) {
			t.Errorf("wait = %q", got)
		}
		time.Sleep(50 * time.Millisecond)
		writeJSON(w, 200, commandsResponse{Commands: []Command{}})
	}))
	defer slow.Close()
	e := newEnv(t, func(o *Options) { o.SiteURL = slow.URL })
	id, _ := ensureIdentity(t.Context(), e.st, e.svc.sealer)
	if _, err := e.svc.newClient(id).commands(t.Context(), "server-1", 25); err != nil {
		t.Fatal(err)
	}
}

func TestTransportRequiresTLS12AndVerifiesCertificates(t *testing.T) {
	t.Parallel()
	c := NewHTTPClient()
	cfg := c.Transport.(*http.Transport).TLSClientConfig
	if cfg == nil || cfg.MinVersion != tls.VersionTLS12 || cfg.InsecureSkipVerify {
		t.Fatalf("tls config = %+v", cfg)
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	if _, err := c.Get(srv.URL); err == nil {
		t.Fatal("a certificate no root vouches for was accepted")
	}
}
