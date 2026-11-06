package api

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

type forgeMediaStub func(*http.Request) *http.Response

func (f forgeMediaStub) RoundTrip(r *http.Request) (*http.Response, error) { return f(r), nil }

func forgeMediaReply(status int, contentType, body string, headers ...string) *http.Response {
	h := http.Header{"Content-Type": {contentType}}
	for i := 0; i+1 < len(headers); i += 2 {
		h.Set(headers[i], headers[i+1])
	}
	return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(body))}
}

func stubForgeMedia(t *testing.T, stub forgeMediaStub) {
	t.Helper()
	previous := forgeMediaTransport
	forgeMediaTransport = stub
	t.Cleanup(func() { forgeMediaTransport = previous })
}

func TestForgeMediaAddressesStayOnGitHub(t *testing.T) {
	for address, want := range map[string]bool{
		"https://raw.githubusercontent.com/o/r/abc/a.png":         true,
		"https://user-images.githubusercontent.com/1/a.png":       true,
		"https://camo.githubusercontent.com/abc/def":              true,
		"https://github.com/user-attachments/assets/uuid":         true,
		"https://github.com/o/r/assets/1/uuid":                    true,
		"https://github.com/o/r/raw/main/a.png":                   true,
		"https://github.com/settings/profile":                     false,
		"https://github.com/o/r/pull/1":                           false,
		"http://raw.githubusercontent.com/o/r/abc/a.png":          false,
		"https://raw.githubusercontent.com:8443/o/r/abc/a.png":    false,
		"https://user:pw@raw.githubusercontent.com/o/r/abc/a.png": false,
		"https://githubusercontent.com.evil.test/a.png":           false,
		"https://evilgithubusercontent.com/a.png":                 false,
		"https://169.254.169.254/latest/meta-data/":               false,
		"https://localhost/a.png":                                 false,
		"file:///etc/passwd":                                      false,
	} {
		parsed, err := url.Parse(address)
		if err != nil {
			t.Fatal(err)
		}
		if got := isForgeMedia(parsed); got != want {
			t.Errorf("%s: %v, want %v", address, got, want)
		}
	}
}

func TestGitHubMediaIsServedInertFromThisOriginAndReadOnce(t *testing.T) {
	const png = "\x89PNG\r\n\x1a\nimage-bytes"
	var reads int
	stubForgeMedia(t, func(r *http.Request) *http.Response {
		reads++
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Errorf("a credential went to GitHub: %v", r.Header)
		}
		return forgeMediaReply(http.StatusOK, "image/png", png)
	})
	s := testServer(t)
	reader := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "media-reader", auth.RoleReadOnly)}
	path := "/api/v1/git/github/media?url=" + url.QueryEscape("https://raw.githubusercontent.com/o/r/abc/a.png")
	res := reader.do(http.MethodGet, path, "", nil)
	if res.Code != http.StatusOK || res.Body.String() != png {
		t.Fatalf("media: %d %q", res.Code, res.Body.String())
	}
	if res.Header().Get("Content-Type") != "image/png" ||
		res.Header().Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(res.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("headers: %v", res.Header())
	}
	if res := reader.do(http.MethodGet, path, "", nil); res.Code != http.StatusOK || reads != 1 {
		t.Fatalf("cached read: %d after %d reads", res.Code, reads)
	}
}

func TestGitHubMediaRefusesEverythingElse(t *testing.T) {
	stubForgeMedia(t, func(r *http.Request) *http.Response {
		switch r.URL.Path {
		case "/page.html":
			return forgeMediaReply(http.StatusOK, "text/html", "<script>alert(1)</script>")
		case "/leaves.png":
			return forgeMediaReply(http.StatusFound, "text/plain", "", "Location", "https://evil.test/a.png")
		case "/private.png":
			return forgeMediaReply(http.StatusNotFound, "text/plain", "404")
		}
		t.Errorf("read %s", r.URL)
		return forgeMediaReply(http.StatusOK, "image/png", "\x89PNG\r\n\x1a\n")
	})
	s := testServer(t)
	reader := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "media-refused", auth.RoleReadOnly)}
	for address, want := range map[string]int{
		"https://evil.test/a.png":                       http.StatusBadRequest,
		"https://github.com/settings/profile":           http.StatusBadRequest,
		"https://raw.githubusercontent.com/page.html":   http.StatusNotFound,
		"https://raw.githubusercontent.com/leaves.png":  http.StatusNotFound,
		"https://raw.githubusercontent.com/private.png": http.StatusNotFound,
	} {
		if res := reader.do(http.MethodGet, "/api/v1/git/github/media?url="+url.QueryEscape(address), "", nil); res.Code != want {
			t.Errorf("%s: %d, want %d", address, res.Code, want)
		}
	}
}
