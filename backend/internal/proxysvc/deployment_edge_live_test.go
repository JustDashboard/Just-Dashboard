package proxysvc

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
)

// What the host's nginx answers for a deployment route's edge rules, rate
// limit and CrowdSec list, each visitor on an address of its own so one's
// allowance is not another's: a blocked path refused 403 and the same path
// let through for an allowed address, a user agent answered with the
// rule's status, an old path redirected, a CrowdSec ban refused and lifted
// by a refresh with no release, and an address past its rate answered 429
// with Retry-After while one within it is served — and the request record
// names each deciding rule.
//
// Twice: served as a route always is, and as a route that sleeps renders
// it, where every location names error pages of its own and so must say the
// limit's 429 again (serverErrorPages).
func TestLiveDeploymentRouteEdgeRulesOnNginx(t *testing.T) {
	for _, sleeps := range []bool{false, true} {
		name := "served"
		if sleeps {
			name = "sleeping"
		}
		t.Run(name, func(t *testing.T) { liveEdgeRulesOnNginx(t, sleeps) })
	}
}

func liveEdgeRulesOnNginx(t *testing.T, sleeps bool) {
	root := liveNginx(t)
	ctx := context.Background()
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "application answer")
	}))
	defer app.Close()
	service := New(root, filepath.Join(root, "Caddyfile"))
	route := DeploymentRoute{
		Name: "just-dashboard-env-21.conf", Domains: []string{"edge.example.test"}, Upstream: app.URL,
		// Traced, as activation renders every route.
		AccessLog: true, Trace: true, CrowdSec: true,
		RateLimit: &DeploymentRateLimit{Requests: 1, Per: "second", Burst: 2},
		EdgeRules: CanonicalEdgeRules([]EdgeRule{
			{Name: "office", Match: EdgeMatch{PathPrefix: "/admin", ClientFrom: []string{"127.0.0.3"}}, Action: EdgeAction{Kind: "allow"}},
			{Name: "admin", Match: EdgeMatch{PathPrefix: "/admin"}, Action: EdgeAction{Kind: "block"}},
			{Name: "bots", Match: EdgeMatch{UserAgent: "evilbot"}, Action: EdgeAction{Kind: "status", Status: 418}},
			{Name: "old", Match: EdgeMatch{PathRegex: `^/old/[0-9]+$`}, Action: EdgeAction{Kind: "redirect", Location: "/new", Code: 301}},
		}),
	}
	if sleeps {
		// Awake throughout: the switch is never written, so every request is
		// sent to the application through the sleeping route's locations.
		route.Sleep = &DeploymentSleep{HoldSeconds: 5}
	}
	if err := validateDeploymentRules(route); err != nil {
		t.Fatal(err)
	}
	if err := service.ensureNginxCrowdSecList(); err != nil {
		t.Fatal(err)
	}
	spec := service.deploymentSpec(route)
	if err := service.writeMissingPages(spec); err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	installed := installSite(t, root, spec, port)
	// An unprivileged nginx writes the record where it can.
	record := filepath.Join(root, "logs", "edge.access.log")
	installed = strings.ReplaceAll(installed, nginxAccessLogPath(route.Name), record)
	installed = strings.ReplaceAll(installed, nginxErrorLogPath(route.Name), filepath.Join(root, "logs", "edge.error.log"))
	if err := os.WriteFile(filepath.Join(root, "sites-available", route.Name), []byte(installed), 0o644); err != nil {
		t.Fatal(err)
	}
	if result := runValidator(ctx, "nginx", "-t"); !result.Valid {
		t.Fatalf("nginx refused the route: %s\n%s", result.Output, installed)
	}
	if sleeps != strings.Contains(installed, "sleeping.on") {
		t.Fatalf("sleeps=%v, but the route reads:\n%s", sleeps, installed)
	}
	startNginx(t, root)

	ask := func(from, path, agent string) (*http.Response, string) {
		t.Helper()
		dialer := &net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(from)}, Timeout: 2 * time.Second}
		client := &http.Client{
			Timeout:       3 * time.Second,
			Transport:     &http.Transport{DialContext: dialer.DialContext, DisableKeepAlives: true},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
		request, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(port)+path, nil)
		request.Host = "edge.example.test"
		if agent != "" {
			request.Header.Set("User-Agent", agent)
		}
		var response *http.Response
		var err error
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			if response, err = client.Do(request); err == nil || time.Now().After(deadline) {
				break
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return response, string(body)
	}
	expect := func(from, path, agent string, status int) *http.Response {
		t.Helper()
		response, body := ask(from, path, agent)
		if response.StatusCode != status {
			t.Fatalf("GET %s from %s (%q) = %d %q, want %d", path, from, agent, response.StatusCode, body, status)
		}
		return response
	}

	expect("127.0.0.2", "/", "", 200)
	expect("127.0.0.2", "/admin/users", "", 403)
	// The allow rule is asked first, and lets its address past the block.
	expect("127.0.0.3", "/admin/users", "", 200)
	expect("127.0.0.4", "/", "Mozilla/5.0 EvilBot/2", 418)
	// nginx writes a path's redirect out in full, on the port it was asked on.
	if response := expect("127.0.0.4", "/old/42", "", 301); !strings.HasSuffix(response.Header.Get("Location"), "edge.example.test:"+strconv.Itoa(port)+"/new") {
		t.Fatalf("redirect went to %q", response.Header.Get("Location"))
	}

	// A CrowdSec ban reaches the route with a refresh and a reload, no release.
	refreshed, err := service.RefreshCrowdSecList(ctx, []string{"127.0.0.5"})
	if err != nil || !refreshed.Changed || !refreshed.Reloaded {
		t.Fatalf("refresh = %+v, %v", refreshed, err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if response, _ := ask("127.0.0.5", "/", ""); response.StatusCode == 403 {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("a banned address was served after the refresh: %d", response.StatusCode)
		}
	}
	expect("127.0.0.2", "/", "", 200)
	if lifted, err := service.RefreshCrowdSecList(ctx, nil); err != nil || !lifted.Reloaded {
		t.Fatalf("lifting = %+v, %v", lifted, err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if response, _ := ask("127.0.0.5", "/", ""); response.StatusCode == 200 {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("a lifted ban was still refused: %d", response.StatusCode)
		}
	}

	// One request a second with a burst of two: three at once are served,
	// the fourth is refused with when to come back, and another address in
	// the same second is served on its own allowance.
	for i := 0; i < 3; i++ {
		expect("127.0.0.6", "/", "", 200)
	}
	limited, body := ask("127.0.0.6", "/", "")
	if limited.StatusCode != 429 || limited.Header.Get("Retry-After") != "1" || !strings.Contains(body, "Try again in 1 second") {
		t.Fatalf("past the limit = %d Retry-After %q %q", limited.StatusCode, limited.Header.Get("Retry-After"), body)
	}
	expect("127.0.0.7", "/", "", 200)
	time.Sleep(1100 * time.Millisecond)
	expect("127.0.0.6", "/", "", 200)

	// The record names the rule that decided each request.
	decided := map[string]int{}
	var raw []byte
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		raw, _ = os.ReadFile(record)
		if strings.Count(string(raw), "\n") >= 14 || time.Now().After(deadline) {
			break
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if entry, ok := accesslog.Parse(line); ok && entry.EdgeRule != "" {
			decided[entry.EdgeRule]++
		}
	}
	for _, name := range []string{"admin", "office", "bots", "old", "crowdsec"} {
		if decided[name] == 0 {
			t.Fatalf("the record does not name %s: %v\n%s", name, decided, raw)
		}
	}
}
