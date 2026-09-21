package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

func TestGitHubAvatarIsServedFromThisOriginAndReadOncePerAccount(t *testing.T) {
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{9}, 24)...)
	var reads int
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Wayy01.png" {
			http.NotFound(w, r)
			return
		}
		reads++
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	}))
	defer github.Close()
	previous := githubWebURL
	githubWebURL = github.URL
	defer func() { githubWebURL = previous }()

	s := testServer(t)
	reader := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "avatar-reader", auth.RoleReadOnly)}
	res := reader.do(http.MethodGet, "/api/v1/git/github/avatar?account=Wayy01", "", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("avatar: %d %s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("content type: %s", got)
	}
	if !bytes.Equal(res.Body.Bytes(), png) {
		t.Fatalf("body: %q", res.Body.Bytes())
	}
	// The picture is served by this server, so the page's own `img-src 'self'`
	// reaches it — and it is a picture, not a document.
	if policy := res.Header().Get("Content-Security-Policy"); !strings.Contains(policy, "sandbox") {
		t.Fatalf("policy: %s", policy)
	}
	if got := res.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("nosniff: %s", got)
	}
	// The second read is answered from memory: GitHub is asked once an hour
	// per account however many rows draw the face.
	if res := reader.do(http.MethodGet, "/api/v1/git/github/avatar?account=Wayy01", "", nil); res.Code != http.StatusOK || reads != 1 {
		t.Fatalf("cached read: %d after %d reads", res.Code, reads)
	}
	// An account GitHub has no picture for is an absence, not a failure: the
	// page falls back to the initials it already draws.
	if res := reader.do(http.MethodGet, "/api/v1/git/github/avatar?account=nobody", "", nil); res.Code != http.StatusNotFound {
		t.Fatalf("unknown account: %d", res.Code)
	}
	anon := &client{t: t, h: s.Routes()}
	if res := anon.do(http.MethodGet, "/api/v1/git/github/avatar?account=Wayy01", "", nil); res.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", res.Code)
	}
}

func TestGitHubAvatarRefusesAnythingThatIsNotALoginAndStaysOnGitHub(t *testing.T) {
	unreachable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("a request escaped to another host: %s", r.URL)
	}))
	defer unreachable.Close()
	previous := githubWebURL
	githubWebURL = unreachable.URL
	defer func() { githubWebURL = previous }()

	s := testServer(t)
	reader := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "avatar-guard", auth.RoleReadOnly)}
	// Nothing the browser sends may choose a host or a path: the address is
	// built from a login, and these are not logins.
	for _, account := range []string{
		"", "..", "a/b", "Wayy01.png", "localhost:8080", "-lead", "trail-",
		"http://example.test", strings.Repeat("a", 40),
	} {
		res := reader.do(http.MethodGet, "/api/v1/git/github/avatar?account="+url.QueryEscape(account), "", nil)
		if res.Code != http.StatusBadRequest {
			t.Fatalf("account %q: %d %s", account, res.Code, res.Body.String())
		}
	}

	// And a redirect out of GitHub is refused, which is what the fetch follows
	// in production: github.com answers with avatars.githubusercontent.com.
	for address, want := range map[string]bool{
		"https://github.com/Wayy01.png":                     true,
		"https://avatars.githubusercontent.com/u/1?v=4":     true,
		"http://github.com/Wayy01.png":                      false,
		"https://github.com.example.test/Wayy01.png":        false,
		"https://example.test/githubusercontent.com/x.png":  false,
		"https://avatars.githubusercontent.com.example/x.p": false,
	} {
		parsed, err := url.Parse(address)
		if err != nil {
			t.Fatal(err)
		}
		if got := insideGitHub(parsed); got != want {
			t.Fatalf("insideGitHub(%s) = %v", address, got)
		}
	}
}
