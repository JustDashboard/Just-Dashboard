package proxysvc

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The auth_request directives must be checked against nginx itself: its
// access phase decides which responses reach the application.
func TestLiveSiteSingleSignOnChecksBeforeForwarding(t *testing.T) {
	root := liveNginx(t)
	var forwarded atomic.Int32
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded.Add(1)
		fmt.Fprintf(w, "%s|%s", r.Header.Get("Remote-User"), r.Header.Get("Remote-Email"))
	}))
	t.Cleanup(app.Close)
	checks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/verify" || r.Header.Get("X-Original-Method") != http.MethodGet ||
			!strings.HasPrefix(r.Header.Get("X-Original-URL"), "http://app.test/") {
			t.Errorf("the auth server received the wrong request: %s %s, headers %+v", r.Method, r.URL, r.Header)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		switch r.Header.Get("X-Original-URL") {
		case "http://app.test/allowed":
			w.Header().Set("Remote-User", "alice")
			w.Header().Set("Remote-Email", "alice@example.test")
			w.WriteHeader(http.StatusOK)
		case "http://app.test/sign-in":
			w.WriteHeader(http.StatusUnauthorized)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(checks.Close)
	spec := plainSpec("app", "app.test")
	spec.Upstream = app.URL
	spec.ForwardAuth = &SiteForwardAuth{
		Provider: "custom", Verify: checks.URL + "/verify", SignIn: "https://login.example.test/start",
	}
	port := freePort(t)
	installSite(t, root, spec, port)
	if result := runValidator(context.Background(), "nginx", "-t"); !result.Valid {
		t.Fatalf("nginx refused the single sign-on site: %s", result.Output)
	}
	startNginx(t, root)

	request := func(path string) (*http.Response, string) {
		t.Helper()
		client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
		var response *http.Response
		var err error
		for deadline := time.Now().Add(5 * time.Second); ; {
			req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(port)+path, nil)
			req.Host = "app.test"
			req.Header.Set("Remote-User", "spoofed")
			response, err = client.Do(req)
			if err == nil || time.Now().After(deadline) {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response, string(body)
	}

	allowed, body := request("/allowed")
	if allowed.StatusCode != http.StatusOK || body != "alice|alice@example.test" || forwarded.Load() != 1 {
		t.Fatalf("an accepted user reached the app as %q: status %d, forwarded %d", body, allowed.StatusCode, forwarded.Load())
	}
	denied, _ := request("/sign-in")
	location, err := url.Parse(denied.Header.Get("Location"))
	if err != nil || denied.StatusCode != http.StatusFound || location.Host != "login.example.test" ||
		location.Query().Get("rd") != "http://app.test/sign-in" || forwarded.Load() != 1 {
		t.Fatalf("a 401 did not go to sign-in: status %d, location %q, forwarded %d", denied.StatusCode, denied.Header.Get("Location"), forwarded.Load())
	}
	failed, _ := request("/auth-error")
	if failed.StatusCode != http.StatusInternalServerError || forwarded.Load() != 1 {
		t.Fatalf("a failed auth check reached the app: status %d, forwarded %d", failed.StatusCode, forwarded.Load())
	}
}
