package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

func boardAuditCount(t *testing.T, s *Server, action string, success bool) int {
	t.Helper()
	var n int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = ? AND success = ?`, action, success).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestBoardLibrarySavesAndRejectsStaleRevisions(t *testing.T) {
	admin, s := newClient(t)
	reader := &client{t: t, h: admin.h, cookie: signInAs(t, s, "libreader", auth.RoleReadOnly)}

	var got struct {
		LibraryItems []json.RawMessage `json:"libraryItems"`
		Revision     int64             `json:"revision"`
	}
	rec := reader.do(http.MethodGet, "/api/v1/boards/library", "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"libraryItems":[]`) || !strings.Contains(rec.Body.String(), `"revision":0`) {
		t.Fatalf("empty library = %d %s", rec.Code, rec.Body.String())
	}

	put := func(c *client, body string) *httptest.ResponseRecorder {
		return c.do(http.MethodPut, "/api/v1/boards/library", body, nil)
	}
	if rec := put(admin, `{"libraryItems":[{"id":"a","status":"published","elements":[]}],"revision":0}`); rec.Code != http.StatusOK ||
		strings.TrimSpace(rec.Body.String()) != `{"revision":1}` {
		t.Fatalf("first save = %d %s", rec.Code, rec.Body.String())
	}
	if rec := put(admin, `{"libraryItems":[],"revision":0}`); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "library_conflict") {
		t.Fatalf("stale save = %d %s", rec.Code, rec.Body.String())
	}
	if rec := put(admin, `{"libraryItems":[{"id":"a"},{"id":"b"}],"revision":1}`); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"revision":2`) {
		t.Fatalf("second save = %d %s", rec.Code, rec.Body.String())
	}
	if rec := put(reader, `{"libraryItems":[],"revision":2}`); rec.Code != http.StatusForbidden {
		t.Fatalf("reader save = %d %s", rec.Code, rec.Body.String())
	}
	rec = reader.do(http.MethodGet, "/api/v1/boards/library", "", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK || len(got.LibraryItems) != 2 || got.Revision != 2 {
		t.Fatalf("stored library = %d %s (%v)", rec.Code, rec.Body.String(), err)
	}

	for label, body := range map[string]string{
		"object":        `{"libraryItems":{},"revision":2}`,
		"null":          `{"libraryItems":null,"revision":2}`,
		"missing":       `{"revision":2}`,
		"non-object":    `{"libraryItems":[1],"revision":2}`,
		"negative":      `{"libraryItems":[],"revision":-1}`,
		"unknown field": `{"libraryItems":[],"revision":2,"extra":1}`,
	} {
		if rec := put(admin, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s", label, rec.Code, rec.Body.String())
		}
	}
	big := fmt.Sprintf(`{"libraryItems":[{"pad":"%s"}],"revision":2}`, strings.Repeat("A", maxBoardLibraryBytes))
	if rec := put(admin, big); rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), "library_too_large") {
		t.Errorf("oversized = %d %.200s", rec.Code, rec.Body.String())
	}
	if n := boardAuditCount(t, s, "board.library.save", true); n != 2 {
		t.Errorf("audited library saves = %d, want 2", n)
	}
}

// libraryHosts sends every request the client makes to one test server, and
// remembers which address it was made for. The checks on the address run
// before the transport sees it, so nothing here weakens them.
type libraryHosts struct {
	target *url.URL
	mu     sync.Mutex
	seen   []string
}

func (l *libraryHosts) RoundTrip(req *http.Request) (*http.Response, error) {
	l.mu.Lock()
	l.seen = append(l.seen, req.URL.String())
	l.mu.Unlock()
	out := req.Clone(req.Context())
	out.Host = req.URL.Host
	out.URL.Scheme, out.URL.Host = l.target.Scheme, l.target.Host
	return http.DefaultTransport.RoundTrip(out)
}

func (l *libraryHosts) requests() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.seen...)
}

func fetchFixture(t *testing.T, handler http.HandlerFunc) (*client, *Server, *libraryHosts) {
	t.Helper()
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	target, _ := url.Parse(upstream.URL)
	admin, s := newClient(t)
	hosts := &libraryHosts{target: target}
	s.boards.fetch.Transport = hosts
	return admin, s, hosts
}

const goodLibrary = `{"type":"excalidrawlib","version":2,"libraryItems":[{"id":"a","elements":[]}]}`

func TestBoardLibraryFetchOnlyReachesExcalidrawsHosts(t *testing.T) {
	admin, s, hosts := fetchFixture(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, goodLibrary)
	})
	reader := &client{t: t, h: admin.h, cookie: signInAs(t, s, "fetchreader", auth.RoleReadOnly)}
	fetch := func(c *client, address string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"url": address})
		return c.do(http.MethodPost, "/api/v1/boards/library/fetch", string(body), nil)
	}

	for _, tc := range []struct{ name, address string }{
		{"wrong host", "https://example.com/libraries/a/b.excalidrawlib"},
		{"look-alike host", "https://libraries.excalidraw.com.evil.test/libraries/a/b.excalidrawlib"},
		{"subdomain", "https://evil.libraries.excalidraw.com/libraries/a/b.excalidrawlib"},
		{"plain http", "http://libraries.excalidraw.com/libraries/a/b.excalidrawlib"},
		{"port", "https://libraries.excalidraw.com:8443/libraries/a/b.excalidrawlib"},
		{"default port spelled out", "https://libraries.excalidraw.com:443/libraries/a/b.excalidrawlib"},
		{"empty port", "https://libraries.excalidraw.com:/libraries/a/b.excalidrawlib"},
		{"userinfo", "https://user:pw@libraries.excalidraw.com/libraries/a/b.excalidrawlib"},
		{"userinfo host trick", "https://libraries.excalidraw.com@evil.test/libraries/a/b.excalidrawlib"},
		{"wrong path", "https://libraries.excalidraw.com/other/a/b.excalidrawlib"},
		{"one segment", "https://libraries.excalidraw.com/libraries/b.excalidrawlib"},
		{"three segments", "https://libraries.excalidraw.com/libraries/a/b/c.excalidrawlib"},
		{"wrong extension", "https://libraries.excalidraw.com/libraries/a/b.json"},
		{"dot segment", "https://libraries.excalidraw.com/libraries/../b.excalidrawlib"},
		{"encoded slash", "https://libraries.excalidraw.com/libraries/a%2Fb/c.excalidrawlib"},
		{"query", "https://libraries.excalidraw.com/libraries/a/b.excalidrawlib?x=1"},
		{"github other repo", "https://raw.githubusercontent.com/evil/repo/main/a.excalidrawlib"},
		{"github traversal", "https://raw.githubusercontent.com/excalidraw/excalidraw-libraries/../../evil/a.excalidrawlib"},
		{"github wrong extension", "https://raw.githubusercontent.com/excalidraw/excalidraw-libraries/main/a.json"},
		{"github port", "https://raw.githubusercontent.com:444/excalidraw/excalidraw-libraries/main/a.excalidrawlib"},
		{"not a url", "libraries"},
		{"empty", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := fetch(admin, tc.address)
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "library_url_refused") {
				t.Fatalf("%q = %d %s", tc.address, rec.Code, rec.Body.String())
			}
		})
	}
	if seen := hosts.requests(); len(seen) != 0 {
		t.Fatalf("a refused address was requested: %v", seen)
	}

	for _, address := range []string{
		"https://libraries.excalidraw.com/libraries/someone/a-set_1.excalidrawlib",
		"https://raw.githubusercontent.com/excalidraw/excalidraw-libraries/main/libraries/a.excalidrawlib",
		"https://libraries.excalidraw.com/libraries/a/b.excalidrawlib#fragment",
	} {
		rec := fetch(admin, address)
		if rec.Code != http.StatusOK || rec.Body.String() != goodLibrary ||
			!strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
			t.Errorf("%s = %d %s %s", address, rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
		}
	}
	if seen := hosts.requests(); len(seen) != 3 || strings.Contains(seen[2], "#") {
		t.Errorf("requests = %v", seen)
	}
	if rec := fetch(reader, "https://libraries.excalidraw.com/libraries/a/b.excalidrawlib"); rec.Code != http.StatusForbidden {
		t.Errorf("reader fetch = %d %s", rec.Code, rec.Body.String())
	}
	if ok, refused := boardAuditCount(t, s, "board.library.fetch", true), boardAuditCount(t, s, "board.library.fetch", false); ok != 3 || refused < 20 {
		t.Errorf("audited fetches = %d ok, %d refused", ok, refused)
	}
}

func TestBoardLibraryFetchChecksWhatComesBack(t *testing.T) {
	const address = `{"url":"https://libraries.excalidraw.com/libraries/a/b.excalidrawlib"}`
	var redirected bool
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		status  int
		code    string
	}{
		{"upstream missing", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }, 502, "library_fetch_failed"},
		{"redirect", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/elsewhere" {
				redirected = true
			}
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
		}, 502, "library_fetch_failed"},
		{"not json", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "<html>") }, 502, "library_fetch_failed"},
		{"json array", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `[]`) }, 502, "library_fetch_failed"},
		{"wrong type", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"type":"excalidraw","libraryItems":[]}`)
		}, 502, "library_fetch_failed"},
		{"no items", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"type":"excalidrawlib","version":2}`)
		}, 502, "library_fetch_failed"},
		{"items not an array", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"type":"excalidrawlib","libraryItems":{}}`)
		}, 502, "library_fetch_failed"},
		{"too large", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"type":"excalidrawlib","libraryItems":[],"pad":"%s"}`, strings.Repeat("A", maxLibraryFetchBytes))
		}, 413, "library_too_large"},
		{"version 1 library", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"type":"excalidrawlib","version":1,"library":[[]]}`)
		}, 200, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			admin, _, _ := fetchFixture(t, tc.handler)
			rec := admin.do(http.MethodPost, "/api/v1/boards/library/fetch", address, nil)
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.code) {
				t.Fatalf("= %d %.200s", rec.Code, rec.Body.String())
			}
		})
	}
	if redirected {
		t.Error("a redirect was followed")
	}
}
