package deploy

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// candidateTarget splits a test server's address into the target the
// activation executor would hand the runner for a leased loopback port.
func candidateTarget(t *testing.T, server *httptest.Server, publicURLs ...string) CheckTarget {
	t.Helper()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	host, portText, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portText)
	return CheckTarget{Host: host, Port: port, PublicURLs: publicURLs}
}

func readinessCheck(t *testing.T, config CheckConfiguration) PlannedCheck {
	t.Helper()
	if config.Attempts == 0 {
		config.Attempts, config.TimeoutSeconds = 1, 2
	}
	raw := mustJSON(config)
	if err := validateCheckConfiguration("http", raw); err != nil {
		t.Fatalf("config %s: %v", raw, err)
	}
	return PlannedCheck{Name: "HTTP readiness", Kind: "http", Phase: "readiness", Required: true, Config: raw}
}

// A Django project configured the way its production checklist says —
// ALLOWED_HOSTS naming the domain, SECURE_SSL_REDIRECT behind a proxy that
// sets X-Forwarded-Proto — refused a probe that introduced itself as
// 127.0.0.1 over plain HTTP. The probe now carries the proxy's headers, so a
// healthy release passes.
func TestHTTPReadinessIntroducesItselfAsThePlannedDomain(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	seen := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen["host"], seen["xfh"], seen["xfp"], seen["xff"], seen["accept"] = r.Host,
			r.Header.Get("X-Forwarded-Host"), r.Header.Get("X-Forwarded-Proto"), r.Header.Get("X-Forwarded-For"), r.Header.Get("Accept")
		mu.Unlock()
		if r.Host != "app.example.com" {
			w.WriteHeader(http.StatusBadRequest) // DisallowedHost
			return
		}
		if r.Header.Get("X-Forwarded-Proto") != "https" {
			http.Redirect(w, r, "https://app.example.com"+r.URL.Path, http.StatusMovedPermanently)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	runner := NewCheckRunner(nil)

	evidence := runner.Run(context.Background(), readinessCheck(t, CheckConfiguration{Path: "/"}),
		candidateTarget(t, server, "https://app.example.com/"))
	if evidence.Outcome != HealthPassed {
		t.Fatalf("planned-domain probe = %+v", evidence)
	}
	mu.Lock()
	if seen["host"] != "app.example.com" || seen["xfh"] != "app.example.com" || seen["xfp"] != "https" ||
		seen["xff"] != "127.0.0.1" || !strings.Contains(seen["accept"], "text/html") {
		t.Fatalf("headers = %v", seen)
	}
	mu.Unlock()

	// With no domain planned there is nothing to introduce: the probe stays
	// the plain loopback request it was, and the allowlist refuses it.
	evidence = runner.Run(context.Background(), readinessCheck(t, CheckConfiguration{Path: "/"}), candidateTarget(t, server))
	if evidence.Outcome != HealthFailed || evidence.Attempts[0].StatusCode != http.StatusBadRequest {
		t.Fatalf("domainless probe = %+v", evidence)
	}
	// A check aimed at another host is the operator's probe of something else.
	target := candidateTarget(t, server, "https://app.example.com/")
	evidence = runner.Run(context.Background(), readinessCheck(t, CheckConfiguration{Path: "/", Host: target.Host, Port: target.Port}), CheckTarget{Host: "10.9.9.9", Port: 1, PublicURLs: target.PublicURLs})
	if evidence.Outcome != HealthFailed || evidence.Attempts[0].StatusCode != http.StatusBadRequest {
		t.Fatalf("explicit-host probe = %+v", evidence)
	}
}

// Redirects that name the release's own domain are the application calling
// itself by its public name, so the same path is asked of the candidate —
// never of the domain, which still routes to the previous release. Another
// site is never requested; a same-site loop is reported as one.
func TestHTTPReadinessFollowsPlannedDomainRedirectsOnTheCandidate(t *testing.T) {
	t.Parallel()
	var externalRequests int
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		externalRequests++
		w.WriteHeader(http.StatusOK)
	}))
	defer external.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			http.Redirect(w, r, "https://app.example.com/ro?from=root", http.StatusTemporaryRedirect)
		case "/ro":
			// The Host follows the domain the redirect named.
			if r.URL.RawQuery != "from=root" || (r.Host != "app.example.com" && r.Host != "www.example.com") {
				w.WriteHeader(http.StatusTeapot)
				return
			}
			w.WriteHeader(http.StatusOK)
		case "/www":
			http.Redirect(w, r, "https://www.example.com/ro?from=root", http.StatusMovedPermanently)
		case "/signin":
			http.Redirect(w, r, external.URL+"/sign-in?state=secret-token", http.StatusFound)
		case "/loop":
			http.Redirect(w, r, "https://app.example.com/loop", http.StatusMovedPermanently)
		}
	}))
	defer server.Close()
	runner := NewCheckRunner(nil)
	target := candidateTarget(t, server, "https://app.example.com/", "https://www.example.com/")
	for _, test := range []struct {
		path, code, origin string
		outcome            HealthOutcome
		status             int
	}{
		{path: "/", outcome: HealthPassed, status: 200},
		{path: "/www", outcome: HealthPassed, status: 200},
		{path: "/signin", outcome: HealthFailed, status: 302, code: "redirect_off_origin", origin: external.URL},
		{path: "/loop", outcome: HealthFailed, status: 301, code: "redirect_loop", origin: "https://app.example.com"},
	} {
		evidence := runner.Run(context.Background(), readinessCheck(t, CheckConfiguration{Path: test.path}), target)
		last := evidence.Attempts[len(evidence.Attempts)-1]
		if evidence.Outcome != test.outcome || last.StatusCode != test.status || last.Code != test.code || last.RedirectOrigin != test.origin {
			t.Fatalf("%s: %+v", test.path, evidence)
		}
		if strings.Contains(last.RedirectOrigin, "secret-token") {
			t.Fatalf("redirect evidence kept the query: %+v", last)
		}
	}
	if externalRequests != 0 {
		t.Fatal("readiness requested another site")
	}
	evidence := runner.Run(context.Background(), readinessCheck(t, CheckConfiguration{Path: "/signin"}), target)
	message := checkFailureMessage("readiness", evidence.Outcome, evidence)
	if !strings.Contains(message, "redirected (HTTP 302) to "+external.URL) || strings.Contains(message, "secret-token") {
		t.Fatalf("failure message = %q", message)
	}
}

// A wildcard domain is no name a request carries, and Django answers a Host
// with a "*" in it 400 whatever ALLOWED_HOSTS says. The probe introduces
// itself by the first concrete domain, or not at all, and a redirect to one
// name under the wildcard carries that name.
func TestHTTPReadinessNeverSendsAWildcardHost(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var hosts []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hosts = append(hosts, r.Host)
		mu.Unlock()
		switch {
		case strings.Contains(r.Host, "*"):
			w.WriteHeader(http.StatusBadRequest)
		case r.URL.Path == "/tenant":
			http.Redirect(w, r, "https://acme.example.com/home", http.StatusFound)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()
	runner := NewCheckRunner(nil)
	for _, test := range []struct {
		path   string
		public []string
		hosts  []string
	}{
		{path: "/", public: []string{"https://*.example.com/", "https://app.example.com/"}, hosts: []string{"app.example.com"}},
		{path: "/", public: []string{"https://*.example.com/"}},
		{path: "/tenant", public: []string{"https://*.example.com/"}, hosts: []string{"", "acme.example.com"}},
	} {
		mu.Lock()
		hosts = nil
		mu.Unlock()
		target := candidateTarget(t, server, test.public...)
		evidence := runner.Run(context.Background(), readinessCheck(t, CheckConfiguration{Path: test.path}), target)
		if evidence.Outcome != HealthPassed {
			t.Fatalf("%s %v: %+v", test.path, test.public, evidence)
		}
		mu.Lock()
		for index, want := range test.hosts {
			if want == "" {
				want = net.JoinHostPort(target.Host, strconv.Itoa(target.Port))
			}
			if index >= len(hosts) || hosts[index] != want {
				t.Fatalf("%s %v: hosts %q", test.path, test.public, hosts)
			}
		}
		if len(test.hosts) == 0 && (len(hosts) != 1 || strings.Contains(hosts[0], "example")) {
			t.Fatalf("%s %v: hosts %q", test.path, test.public, hosts)
		}
		mu.Unlock()
	}
}

// An any-answer check proves the server answers without requiring a page at
// the path: an API's 404, a login wall's 401, a redirect to a hosted sign-in.
// It still refuses a 5xx, and the 400/421 a host allowlist answers with.
func TestHTTPReadinessAnyAnswerAcceptsServingStatusesOnly(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/"))
		if status >= 300 && status < 400 {
			w.Header().Set("Location", "https://accounts.example.dev/sign-in")
		}
		w.WriteHeader(status)
	}))
	defer server.Close()
	runner := NewCheckRunner(nil)
	for status, outcome := range map[int]HealthOutcome{
		200: HealthPassed, 204: HealthPassed, 302: HealthPassed, 307: HealthPassed, 401: HealthPassed,
		403: HealthPassed, 404: HealthPassed, 405: HealthPassed, 426: HealthPassed,
		400: HealthFailed, 421: HealthFailed, 500: HealthFailed, 502: HealthFailed, 503: HealthFailed,
	} {
		evidence := runner.Run(context.Background(),
			readinessCheck(t, CheckConfiguration{Path: "/" + strconv.Itoa(status), AcceptAnyAnswer: true}),
			candidateTarget(t, server, "https://api.example.com/"))
		if evidence.Outcome != outcome || evidence.Attempts[0].StatusCode != status {
			t.Fatalf("%d: %+v", status, evidence)
		}
	}
}

func TestCheckConfigurationValidatesAnyAnswer(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		kind, raw string
		valid     bool
	}{
		{"http", `{"path":"/","acceptAnyAnswer":true}`, true},
		{"http", `{"path":"/","acceptAnyAnswer":true,"expectedStatus":[200]}`, false},
		{"tcp", `{"acceptAnyAnswer":true}`, false},
		{"docker_health", `{"acceptAnyAnswer":true}`, false},
		{"command", `{"command":["true"],"acceptAnyAnswer":true}`, false},
		{"dns", `{"acceptAnyAnswer":true}`, false},
	} {
		if err := validateCheckConfiguration(test.kind, json.RawMessage(test.raw)); (err == nil) != test.valid {
			t.Fatalf("%s %s: %v", test.kind, test.raw, err)
		}
	}
}

// The public-route smoke check already asks the domain itself; it gains no
// headers and no candidate rewriting.
func TestPublicRouteCheckKeepsItsOwnAddress(t *testing.T) {
	t.Parallel()
	var host string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host = r.Host
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	parsed, _ := url.Parse(server.URL)
	evidence := NewCheckRunner(nil).Run(context.Background(), PlannedCheck{
		Name: "public", Kind: "public_route", Phase: "smoke", Required: true,
		Config: json.RawMessage(`{"attempts":1,"timeoutSeconds":1}`),
	}, CheckTarget{PublicURLs: []string{server.URL + "/"}})
	if evidence.Outcome != HealthPassed || host != parsed.Host {
		t.Fatalf("public route = %+v host %q", evidence, host)
	}
}
