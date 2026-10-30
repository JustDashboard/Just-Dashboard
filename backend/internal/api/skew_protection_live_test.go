package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

// Skew protection end to end, against a real daemon and a real Caddy
// ingress: release A sets each visitor's jd_release cookie on their first
// answer; after the cutover to release B a visitor with A's cookie is still
// answered by A — its page and its assets — while a fresh visitor, and one
// whose cookie names a release that is gone, get B and B's cookie. A is kept
// running, not paused, through B's window, and the window's read counts
// what A answered. When the window passes A is taken off the route and
// removed, and every visitor is answered by B.
func TestLiveSkewProtectionKeepsAVisitorOnTheirRelease(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 on a Docker host")
	}
	stamp := time.Now().UnixNano()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ingress, endpoint := liveRollbackIngress(t, stamp)
	s := liveRollbackServer(t, ingress)()
	if err := s.modules.deployEngine.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdown, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		_ = s.modules.deployEngine.Shutdown(shutdown)
	})
	s.modules.rollbackWatcher.WithInterval(2 * time.Second).Start(ctx)
	environmentBase := stamp%100_000_000 + 910_000_000
	if _, err := s.Store.DB.Exec(`DELETE FROM sqlite_sequence WHERE name = 'deploy_environments'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`INSERT INTO sqlite_sequence(name, seq) VALUES('deploy_environments', ?)`, environmentBase); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { removeEnvironmentContainers(t, environmentBase) })
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "skew-live", auth.RoleAdmin)}

	domain := fmt.Sprintf("skew-%d.example.test", stamp)
	answer := func(body string) []string { return []string{"caddy", "respond", "--listen", ":8080", body} }
	projectID, environmentID := liveRollbackProject(t, admin, fmt.Sprintf("skew-live-%d", stamp%1_000_000), domain, answer("release A"))
	base := fmt.Sprintf("/api/v1/deploy/%d/environments/%d", projectID, environmentID)
	liveSkewConfigure(t, admin, base, answer("release A"))
	if res := admin.do(http.MethodPut, base+"/rollback-policy", `{"keepWarm":true,"windowSeconds":45,"automatic":false,"errorRateMargin":5,"latencyFactor":2,"minRequests":20}`, nil); res.Code != http.StatusOK {
		t.Fatalf("rollback policy = %d %s", res.Code, res.Body.String())
	}
	first := liveRollbackDeploy(t, admin, projectID, environmentID)
	cookieA := strconv.FormatInt(first, 10)

	// A's first answer names it, as an HttpOnly, Lax cookie of the window's
	// length, and is not repeated once the visitor carries it.
	answered := skewGet(t, endpoint, domain, "/", "")
	if answered.body != "release A" || !strings.HasPrefix(answered.cookie, "jd_release="+cookieA+";") ||
		!strings.Contains(answered.cookie, "Max-Age=45") || !strings.Contains(answered.cookie, "HttpOnly") ||
		!strings.Contains(answered.cookie, "SameSite=Lax") || !strings.Contains(answered.cookie, "Path=/") ||
		strings.Contains(answered.cookie, "Secure") {
		t.Fatalf("release A's first answer = %+v", answered)
	}
	if again := skewGet(t, endpoint, domain, "/", cookieA); again.body != "release A" || again.cookie != "" {
		t.Fatalf("an answer to a visitor who carries the cookie = %+v", again)
	}

	liveSkewConfigure(t, admin, base, answer("release B"))
	second := liveRollbackDeploy(t, admin, projectID, environmentID)
	cookieB := strconv.FormatInt(second, 10)
	watch := liveRollbackWatch(t, admin, base)
	if watch == nil || watch.State != deploy.RollbackWatchWatching || !watch.WarmRunning || watch.WarmReleaseID != first {
		t.Fatalf("after release B: window %+v", watch)
	}
	if warm := inspectContainer(t, watch.WarmRuntimeID); !warm.State.Running || warm.State.Paused {
		t.Fatalf("release A is not running through B's window: %+v", warm.State)
	}

	// The visitor A answered stays on A, assets and all; a fresh visitor
	// and one whose cookie names nothing served get B, and B's cookie.
	const kept = 25
	for index := 0; index < kept; index++ {
		path := "/"
		if index%2 == 1 {
			path = "/_next/static/chunks/app.js"
		}
		if got := skewGet(t, endpoint, domain, path, cookieA); got.body != "release A" || got.cookie != "" {
			t.Fatalf("request %d of the visitor A answered (%s) = %+v", index, path, got)
		}
	}
	fresh := skewGet(t, endpoint, domain, "/", "")
	if fresh.body != "release B" || !strings.HasPrefix(fresh.cookie, "jd_release="+cookieB+";") {
		t.Fatalf("a fresh visitor after the cutover = %+v", fresh)
	}
	if stale := skewGet(t, endpoint, domain, "/", "999999"); stale.body != "release B" || !strings.HasPrefix(stale.cookie, "jd_release="+cookieB+";") {
		t.Fatalf("a visitor whose cookie names no release served = %+v", stale)
	}
	if again := skewGet(t, endpoint, domain, "/", cookieB); again.body != "release B" || again.cookie != "" {
		t.Fatalf("a visitor B answered = %+v", again)
	}

	// The window's read counts A's answers apart from B's.
	deadline := time.Now().Add(30 * time.Second)
	for {
		watch = liveRollbackWatch(t, admin, base)
		if watch.WarmServed != nil && watch.WarmServed.Requests >= kept {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the window never counted release A's answers: %+v", watch.WarmServed)
		}
		time.Sleep(time.Second)
	}
	t.Logf("release A answered %d requests of its visitors; release B %d", watch.WarmServed.Requests, watch.Observed.Requests)
	if watch.Observed == nil || watch.Observed.Requests >= kept {
		t.Fatalf("release B's reading counts release A's answers: observed %+v", watch.Observed)
	}

	// After the window everyone is on B: A's visitor is told so.
	deadline = watch.ExpiresAt.Add(30 * time.Second)
	for {
		watch = liveRollbackWatch(t, admin, base)
		if watch.State == deploy.RollbackWatchClosed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the window did not close: %+v", watch)
		}
		time.Sleep(time.Second)
	}
	if watch.EndReason != deploy.RollbackEndWindowPassed || containerExists(t, watch.WarmRuntimeID) {
		t.Fatalf("after the window: %+v, release A present %t", watch, containerExists(t, watch.WarmRuntimeID))
	}
	if after := skewGet(t, endpoint, domain, "/", cookieA); after.body != "release B" || !strings.HasPrefix(after.cookie, "jd_release="+cookieB+";") {
		t.Fatalf("release A's visitor after the window = %+v", after)
	}
	if after := skewGet(t, endpoint, domain, "/", ""); after.body != "release B" {
		t.Fatalf("a fresh visitor after the window = %+v", after)
	}
}

type skewAnswer struct {
	status int
	body   string
	cookie string
}

// skewGet asks the ingress for path as a visitor carrying the jd_release
// cookie release, or none, and keeps the cookie the answer set.
func skewGet(t *testing.T, endpoint, host, path, release string) skewAnswer {
	t.Helper()
	request, _ := http.NewRequest(http.MethodGet, endpoint+path, nil)
	request.Host = host
	if release != "" {
		request.Header.Set("Cookie", "theme=dark; jd_release="+release)
	}
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("through the ingress: %v", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	cookie := ""
	for _, value := range response.Header.Values("Set-Cookie") {
		if strings.HasPrefix(value, "jd_release=") {
			cookie = value
		}
	}
	return skewAnswer{status: response.StatusCode, body: strings.TrimSpace(string(body)), cookie: cookie}
}

// liveSkewConfigure is liveRollbackConfigure with skew protection on.
func liveSkewConfigure(t *testing.T, c *client, base string, command []string) {
	t.Helper()
	read := c.do(http.MethodGet, base+"/configuration", "", nil)
	var current deploy.EnvironmentConfiguration
	if err := json.Unmarshal(read.Body.Bytes(), &current); err != nil || read.Code != http.StatusOK {
		t.Fatalf("configuration = %d %s", read.Code, read.Body.String())
	}
	current.Runtime.Command = command
	current.Runtime.SkewProtection = true
	body, _ := json.Marshal(deploy.ConfigurationWriteRequest{
		Revision: current.Revision, Build: current.Build, Runtime: current.Runtime,
		Dependencies: current.Dependencies, Checks: current.Checks, Domains: current.Domains,
	})
	if res := c.do(http.MethodPut, base+"/configuration", string(body), nil); res.Code != http.StatusOK {
		t.Fatalf("configuration save = %d %s", res.Code, res.Body.String())
	}
}
