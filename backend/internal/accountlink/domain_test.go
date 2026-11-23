package accountlink

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// The app domain protocol's test vectors (app-domains-protocol.md section 7):
// the requests signed with the link protocol's test key, from the exact
// inputs the site lane computes them with. The request goes through the real
// client, so the bytes compared are the bytes this dashboard sends.
func TestAppDomainProtocolVectors(t *testing.T) {
	t.Parallel()
	key := seedKey(t, vectorServerSeed)
	const serverID = vectorServerID
	for _, tc := range []struct {
		name, method, path string
		body               any
		wantBody           string
		at, seq            int64
		signed, signature  string
	}{
		{
			name: "get domain", method: "GET", path: "/api/link/v1/domain", at: 1795000000, seq: 41,
			signed:    "GET\n/api/link/v1/domain\ne3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855\n1795000000\n41",
			signature: "SZe/ZPGAZVoJmYCLszH+MHzV8m2ABwqrFWJFyl5sbaoEpZ5GE/Ays9pusY0fNixpmEaNtbumJwnUjz7h1ZhfAQ==",
		},
		{
			name: "put domain", method: "PUT", path: "/api/link/v1/domain", at: 1795000001, seq: 42,
			body:      putDomainBody{Handle: "acme", Address: "203.0.113.10"},
			wantBody:  `{"handle":"acme","address":"203.0.113.10"}`,
			signed:    "PUT\n/api/link/v1/domain\n263cccd2e38534c497c77a23030af44c9bf648c6777dd54704ca45216c540cc0\n1795000001\n42",
			signature: "FfGYQzAzPaFtBsJwZdCU3JgtUJato/A4TaAl9nfzigRkFQnIdvEFGgJskWaR+VHYPJ6HGMWSpYy+XsWbmf3ZCg==",
		},
		{
			name: "add challenge", method: "POST", path: "/api/link/v1/domain/challenges", at: 1795000002, seq: 43,
			body:      challengeBody{Value: "Zm9vYmFyYmF6cXV4MDEyMzQ1Njc4OWFiY2RlZmdoaWo"},
			wantBody:  `{"value":"Zm9vYmFyYmF6cXV4MDEyMzQ1Njc4OWFiY2RlZmdoaWo"}`,
			signed:    "POST\n/api/link/v1/domain/challenges\nce777e84f57a475cd0b9e642fe38b87ab02a1f7d1484b18a2a69dbc332125e41\n1795000002\n43",
			signature: "13UDWcOl+fQcts+wsnyVhjjBu90gsr0fGd26a8rjgZCId9KQpdFuN5b+J4onmS+QVwdQ6JkqkQ1O10NEZPsKAA==",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotBody, gotSig, gotSeq, gotTime, gotServer string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				gotBody, gotSig = string(raw), r.Header.Get(headerSignature)
				gotSeq, gotTime, gotServer = r.Header.Get(headerSequence), r.Header.Get(headerTime), r.Header.Get(headerServer)
				w.WriteHeader(http.StatusNoContent)
			}))
			defer srv.Close()
			c := &Client{
				origin: srv.URL, http: srv.Client(), id: &Identity{key: key},
				next: func(context.Context) (int64, error) { return tc.seq, nil },
				now:  func() time.Time { return time.Unix(tc.at, 0) }, version: "test", base: basePath, agent: "account-link", peer: "site",
			}
			if _, err := c.call(t.Context(), tc.method, tc.path, tc.body, serverID, 0, nil); err != nil {
				t.Fatal(err)
			}
			if gotBody != tc.wantBody {
				t.Fatalf("body = %q, want %q", gotBody, tc.wantBody)
			}
			if got := requestMessage(tc.method, tc.path, []byte(gotBody), tc.at, tc.seq); got != tc.signed {
				t.Fatalf("signed string = %q\nwant %q", got, tc.signed)
			}
			if gotSig != tc.signature {
				t.Fatalf("signature = %s\nwant %s", gotSig, tc.signature)
			}
			if gotServer != serverID || gotSeq == "" || gotTime == "" {
				t.Fatalf("headers: server %q seq %q time %q", gotServer, gotSeq, gotTime)
			}
		})
	}
}

// domainSite is the site's side of the domain endpoints, enough to drive the
// client: one domain per server, the challenge values, and a refusal to give
// once.
type domainSite struct {
	mu        sync.Mutex
	available bool
	domain    map[string]any
	values    []string
	offer     string
	refuse    struct {
		status    int
		code, msg string
	}
	reply string
	calls []string
}

func (d *domainSite) serve(w http.ResponseWriter, r *http.Request, path string, body []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, r.Method+" "+path)
	if d.refuse.code != "" {
		apiError(w, d.refuse.status, d.refuse.code, d.refuse.msg)
		d.refuse.code = ""
		return
	}
	if d.reply != "" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(d.reply))
		return
	}
	switch r.Method + " " + path {
	case "GET /domain":
		zone := any(nil)
		reason := any("APP_DOMAINS_ZONE is not set")
		if d.available {
			zone, reason = "just-dashboard.com", nil
		}
		var domain any
		if d.domain != nil {
			domain = d.domain
		}
		var offer any
		if d.offer != "" {
			offer = d.offer
		}
		writeJSON(w, 200, map[string]any{"available": d.available, "reason": reason, "zone": zone, "maxHandles": 3, "domain": domain, "offer": offer})
	case "PUT /domain":
		var in struct{ Handle, Address string }
		_ = json.Unmarshal(body, &in)
		d.domain = map[string]any{
			"handle": in.Handle, "hostname": in.Handle + ".just-dashboard.com", "address": in.Address,
			"status": "active", "reason": nil, "createdAt": 1795000000, "updatedAt": 1795000000,
		}
		writeJSON(w, 200, map[string]any{"domain": d.domain})
	case "DELETE /domain":
		d.domain = nil
		w.WriteHeader(http.StatusNoContent)
	case "POST /domain/challenges":
		var in struct{ Value string }
		_ = json.Unmarshal(body, &in)
		d.values = append(d.values, in.Value)
		writeJSON(w, 200, map[string]any{"name": "_acme-challenge.acme.just-dashboard.com", "values": d.values})
	case "POST /domain/challenges/remove":
		d.values = nil
		w.WriteHeader(http.StatusNoContent)
	default:
		apiError(w, 404, "not_found", path)
	}
}

func linkedDomainEnv(t *testing.T) (*testEnv, *domainSite) {
	t.Helper()
	e := newEnv(t)
	d := &domainSite{available: true}
	e.site.mu.Lock()
	e.site.domain = d.serve
	e.site.mu.Unlock()
	e.link(ScopeObserve)
	return e, d
}

func TestDomainCallsAreSignedOnTheActiveLink(t *testing.T) {
	t.Parallel()
	e, d := linkedDomainEnv(t)
	ctx := t.Context()

	info, err := e.svc.GetDomain(ctx)
	if err != nil || !info.Available || info.Zone == nil || *info.Zone != "just-dashboard.com" || info.Domain != nil || info.Offer != nil {
		t.Fatalf("get = %+v, %v", info, err)
	}
	d.mu.Lock()
	d.offer = "acme"
	d.mu.Unlock()
	if info, err = e.svc.GetDomain(ctx); err != nil || info.Offer == nil || *info.Offer != "acme" {
		t.Fatalf("offer = %+v, %v", info, err)
	}
	d.mu.Lock()
	d.offer = ""
	d.mu.Unlock()
	dom, err := e.svc.PutDomain(ctx, "acme", "203.0.113.10")
	if err != nil || dom.Handle != "acme" || dom.Status != DomainActive || dom.Hostname != "acme.just-dashboard.com" {
		t.Fatalf("put = %+v, %v", dom, err)
	}
	set, err := e.svc.AddChallenge(ctx, strings.Repeat("a", 43))
	if err != nil || len(set.Values) != 1 {
		t.Fatalf("challenge = %+v, %v", set, err)
	}
	if err := e.svc.RemoveChallenge(ctx, strings.Repeat("a", 43)); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeleteDomain(ctx); err != nil {
		t.Fatal(err)
	}
	// Every call passed the fake site's signature, clock and sequence checks.
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.calls) != 6 {
		t.Fatalf("calls = %v", d.calls)
	}
}

func TestDomainCallsShareTheHeartbeatsSequenceWhileTheyRunTogether(t *testing.T) {
	t.Parallel()
	e, _ := linkedDomainEnv(t)
	ctx := t.Context()
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); _, err := e.svc.GetDomain(ctx); errs <- err }()
		go func() { defer wg.Done(); errs <- e.svc.SyncNow(ctx) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		// The site refuses a number that is not above the last it took;
		// the client takes a new one and sends again.
		if err != nil && !errors.Is(err, ErrTooSoon) {
			t.Fatal(err)
		}
	}
}

func TestDomainCallsNeedAnActiveAccountLink(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	if _, err := e.svc.GetDomain(t.Context()); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("unlinked = %v", err)
	}
	off := newEnv(t, func(o *Options) { o.Enabled = false })
	if _, err := off.svc.GetDomain(t.Context()); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled = %v", err)
	}
	hub := newEnv(t, func(o *Options) { p := AccountProfile; p.KeyPrefix = "hub.link"; o.Profile = &p })
	if _, err := hub.svc.GetDomain(t.Context()); !errors.Is(err, ErrDomainsNotAccountLink) {
		t.Fatalf("hub = %v", err)
	}
}

func TestDomainResponsesAreDecodedStrictly(t *testing.T) {
	t.Parallel()
	for name, reply := range map[string]string{
		"unknown field":          `{"available":true,"reason":null,"zone":"just-dashboard.com","maxHandles":3,"domain":null,"offer":null,"extra":1}`,
		"missing field":          `{"available":true,"reason":null,"zone":"just-dashboard.com","domain":null}`,
		"available, no zone":     `{"available":true,"reason":null,"zone":null,"maxHandles":3,"domain":null,"offer":null}`,
		"bad status":             `{"available":true,"reason":null,"zone":"just-dashboard.com","maxHandles":3,"domain":{"handle":"acme","hostname":"acme.just-dashboard.com","address":"203.0.113.10","status":"pending","reason":null,"createdAt":1,"updatedAt":1},"offer":null}`,
		"domain missing a field": `{"available":true,"reason":null,"zone":"just-dashboard.com","maxHandles":3,"domain":{"handle":"acme","hostname":"acme.just-dashboard.com","address":"203.0.113.10","status":"active","createdAt":1,"updatedAt":1},"offer":null}`,
		"no offer field":         `{"available":true,"reason":null,"zone":"just-dashboard.com","maxHandles":3,"domain":null}`,
		"offer not a handle":     `{"available":true,"reason":null,"zone":"just-dashboard.com","maxHandles":3,"domain":null,"offer":"Not A Handle"}`,
		"offer beside a domain":  `{"available":true,"reason":null,"zone":"just-dashboard.com","maxHandles":3,"domain":{"handle":"acme","hostname":"acme.just-dashboard.com","address":"203.0.113.10","status":"active","reason":null,"createdAt":1,"updatedAt":1},"offer":"acme"}`,
		"trailing":               `{"available":false,"reason":"x","zone":null,"maxHandles":3,"domain":null,"offer":null} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			e, d := linkedDomainEnv(t)
			d.mu.Lock()
			d.reply = reply
			d.mu.Unlock()
			if _, err := e.svc.GetDomain(t.Context()); err == nil || !strings.Contains(err.Error(), "protocol") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestEveryDomainRefusalReadsAsASentence(t *testing.T) {
	t.Parallel()
	codes := map[string]int{
		"invalid_handle": 400, "invalid_address": 400, "invalid_challenge": 400, "limit_reached": 403,
		"account_suspended": 403, "domain_suspended": 403, "no_domain": 404, "handle_reserved": 409,
		"handle_taken": 409, "handle_quarantined": 409, "address_mismatch": 409, "too_many_challenges": 409,
		"rate_limited": 429, "dns_failed": 502, "domains_unavailable": 503,
	}
	e, d := linkedDomainEnv(t)
	for code, status := range codes {
		d.mu.Lock()
		d.refuse.status, d.refuse.code, d.refuse.msg = status, code, "the site saw 198.51.100.7"
		d.mu.Unlock()
		_, err := e.svc.PutDomain(t.Context(), "acme", "203.0.113.10")
		var de *DomainError
		if !errors.As(err, &de) || de.Code != code || de.Status != status {
			t.Fatalf("%s: %v", code, err)
		}
		if strings.Contains(de.Message, "answered") || !strings.HasSuffix(strings.TrimSpace(de.Message), ".") && !strings.HasSuffix(de.Message, `"`) {
			t.Fatalf("%s reads as %q", code, de.Message)
		}
	}
	d.mu.Lock()
	d.refuse.status, d.refuse.code, d.refuse.msg = 409, "address_mismatch", "the site saw 198.51.100.7"
	d.mu.Unlock()
	_, err := e.svc.PutDomain(t.Context(), "acme", "203.0.113.10")
	if err == nil || !strings.Contains(err.Error(), "198.51.100.7") {
		t.Fatalf("the address the site saw is not named: %v", err)
	}
}

func TestADomainCallAfterTheServerWasRemovedClearsTheLink(t *testing.T) {
	t.Parallel()
	e, _ := linkedDomainEnv(t)
	st, _, _ := e.svc.Linked(t.Context())
	e.site.remove(st.ServerID)
	if _, err := e.svc.GetDomain(t.Context()); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("err = %v", err)
	}
	if _, linked, _ := e.svc.Linked(t.Context()); linked {
		t.Fatal("the link is still stored after the site answered 410")
	}
}

func TestHandleAndChallengeSyntax(t *testing.T) {
	t.Parallel()
	for handle, want := range map[string]bool{
		"acme": true, "a1b": true, "abc-def": true, strings.Repeat("a", 20): true,
		"ab": false, strings.Repeat("a", 21): false, "-abc": false, "abc-": false, "xn--abc": false, "Acme": false, "a_b": false,
	} {
		if ValidHandle(handle) != want {
			t.Errorf("ValidHandle(%q) != %v", handle, want)
		}
	}
	if !ValidChallenge("Zm9vYmFyYmF6cXV4MDEyMzQ1Njc4OWFiY2RlZmdoaWo") || ValidChallenge("short") || ValidChallenge(strings.Repeat("a", 42)+"=") {
		t.Fatal("challenge syntax")
	}
}
