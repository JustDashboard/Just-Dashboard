package api

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

func insertDeploymentWebsite(t *testing.T, s *Server, address string) int64 {
	t.Helper()
	id, env, _ := insertDeploymentConfigurationAPI(t, s)
	_, err := s.Store.DB.Exec(`INSERT INTO deploy_dependencies(environment_id, kind, ownership, resource_kind, resource_id, created_at) VALUES(?, 'domain', 'managed', 'proxy_site', ?, 1)`, env, address)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestDeploymentFaviconServesTheDeclaredIconFromTheRecordedHostOnly(t *testing.T) {
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{7}, 24)...)
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("a request escaped to another host: %s", r.URL)
	}))
	defer elsewhere.Close()
	var pageReads int
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			pageReads++
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<!doctype html><html><head><link rel="stylesheet" href="/app.css"><link rel="icon" href="%s/steal.png"><link rel="apple-touch-icon" href="/touch.png"><link rel="icon" type="image/png" href="/brand.png"></head><body><a href="/favicon.ico">x</a></body></html>`, elsewhere.URL)
		case "/brand.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(png)
		default:
			http.NotFound(w, r)
		}
	}))
	defer site.Close()

	s := testServer(t)
	id := insertDeploymentWebsite(t, s, site.URL)
	path := fmt.Sprintf("/api/v1/deploy/%d/favicon", id)
	reader := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "favicon-reader", auth.RoleReadOnly)}
	res := reader.do(http.MethodGet, path, "", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("favicon: %d %s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("content type: %s", got)
	}
	if !bytes.Equal(res.Body.Bytes(), png) {
		t.Fatalf("body: %q", res.Body.Bytes())
	}
	if policy := res.Header().Get("Content-Security-Policy"); !strings.Contains(policy, "sandbox") {
		t.Fatalf("policy: %s", policy)
	}
	// The second read is answered from memory: the site is not asked again.
	if res := reader.do(http.MethodGet, path, "", nil); res.Code != http.StatusOK || pageReads != 1 {
		t.Fatalf("cached read: %d after %d page reads", res.Code, pageReads)
	}
	anon := &client{t: t, h: s.Routes()}
	if res := anon.do(http.MethodGet, path, "", nil); res.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", res.Code)
	}
}

func TestDeploymentFaviconFallsBackToTheConventionalPathOrReportsNone(t *testing.T) {
	ico := append([]byte{0, 0, 1, 0, 1, 0}, bytes.Repeat([]byte{1}, 20)...)
	withIcon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><head><title>plain</title></head><body></body></html>`))
		case "/favicon.ico":
			_, _ = w.Write(ico)
		default:
			http.NotFound(w, r)
		}
	}))
	defer withIcon.Close()
	bare := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			_, _ = w.Write([]byte("hello"))
			return
		}
		http.NotFound(w, r)
	}))
	defer bare.Close()

	// The project fixture is one per server, so each website gets its own.
	s := testServer(t)
	reader := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "favicon-fallback", auth.RoleReadOnly)}
	found := insertDeploymentWebsite(t, s, withIcon.URL)
	res := reader.do(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/favicon", found), "", nil)
	if res.Code != http.StatusOK || !strings.HasPrefix(res.Header().Get("Content-Type"), "image/") || !bytes.Equal(res.Body.Bytes(), ico) {
		t.Fatalf("conventional icon: %d %s %q", res.Code, res.Header().Get("Content-Type"), res.Body.Bytes())
	}

	s = testServer(t)
	reader = &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "favicon-none", auth.RoleReadOnly)}
	none := insertDeploymentWebsite(t, s, bare.URL)
	res = reader.do(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/favicon", none), "", nil)
	if res.Code != http.StatusNotFound || !strings.Contains(res.Body.String(), "favicon_unavailable") {
		t.Fatalf("no icon: %d %s", res.Code, res.Body.String())
	}
}
