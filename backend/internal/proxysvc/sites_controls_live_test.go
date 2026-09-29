package proxysvc

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// nginx must load the http-level zones as well as the site's server block,
// and a visitor over the rate must get the status promised by the form.
func TestLiveSiteRequestLimitAnswers429(t *testing.T) {
	root := liveNginx(t)
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(app.Close)
	spec := plainSpec("limited", "limited.test")
	spec.Upstream = app.URL
	spec.Limits = &SiteLimits{Request: &RequestLimit{Rate: "1r/m", Burst: 1, NoDelay: true}}
	port := freePort(t)
	installSite(t, root, spec, port)
	if result := runValidator(context.Background(), "nginx", "-t"); !result.Valid {
		t.Fatalf("nginx refused the limits: %s", result.Output)
	}
	startNginx(t, root)
	for i, want := range []int{http.StatusNoContent, http.StatusNoContent, http.StatusTooManyRequests} {
		response, _ := siteGet(t, port, "limited.test", "/")
		if response.StatusCode != want {
			t.Fatalf("request %d answered %d, want %d", i+1, response.StatusCode, want)
		}
	}
}

// The backup must answer after the primary goes away, rather than merely
// surviving nginx -t with a backup token in the rendered file.
func TestLiveSitePoolBackupTakesOver(t *testing.T) {
	root := liveNginx(t)
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "primary")
	}))
	defer primary.Close()
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "backup")
	}))
	defer backup.Close()
	spec := plainSpec("pool", "pool.test")
	spec.Upstream = ""
	spec.Pool = &SitePool{Servers: []PoolServer{
		{Address: strings.TrimPrefix(primary.URL, "http://"), MaxFails: 1, FailTimeout: 30},
		{Address: strings.TrimPrefix(backup.URL, "http://"), Backup: true},
	}}
	port := freePort(t)
	installSite(t, root, spec, port)
	if result := runValidator(context.Background(), "nginx", "-t"); !result.Valid {
		t.Fatalf("nginx refused the pool: %s", result.Output)
	}
	startNginx(t, root)
	if response, body := siteGet(t, port, "pool.test", "/"); response.StatusCode != http.StatusOK || body != "primary" {
		t.Fatalf("healthy primary: status %d, body %q", response.StatusCode, body)
	}
	primary.Close()
	if response, body := siteGet(t, port, "pool.test", "/"); response.StatusCode != http.StatusOK || body != "backup" {
		t.Fatalf("failed primary: status %d, body %q", response.StatusCode, body)
	}
}

// Access controls sit ahead of the application even for paths that nginx
// would otherwise proxy, and same-site referrals must keep working.
func TestLiveSiteBlocksBotsAndHotlinkedMedia(t *testing.T) {
	root := liveNginx(t)
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "app")
	}))
	defer app.Close()
	spec := plainSpec("access", "access.test")
	spec.Upstream = app.URL
	spec.BlockBots = &SiteBotBlock{AI: true}
	spec.Hotlink = &SiteHotlink{}
	port := freePort(t)
	installSite(t, root, spec, port)
	if result := runValidator(context.Background(), "nginx", "-t"); !result.Valid {
		t.Fatalf("nginx refused the access options: %s", result.Output)
	}
	startNginx(t, root)
	for _, tc := range []struct {
		path, header, value string
		want                int
	}{
		{path: "/page", header: "User-Agent", value: "ChatGPT-User", want: http.StatusForbidden},
		{path: "/image.png", header: "Referer", value: "https://other.test/page", want: http.StatusForbidden},
		{path: "/image.png", header: "Referer", value: "https://access.test/page", want: http.StatusOK},
		{path: "/image.png", want: http.StatusOK},
	} {
		var response *http.Response
		if tc.header == "" {
			response, _ = siteGet(t, port, "access.test", tc.path)
		} else {
			response, _ = siteGet(t, port, "access.test", tc.path, tc.header, tc.value)
		}
		if response.StatusCode != tc.want {
			t.Errorf("%s with %s=%q answered %d, want %d", tc.path, tc.header, tc.value, response.StatusCode, tc.want)
		}
	}
}

// A live cache must serve a repeat without visiting the app, while signed-in
// requests bypass it and a purge makes the next anonymous request a miss.
func TestLiveSiteCacheServesBypassesAndPurges(t *testing.T) {
	root := liveNginx(t)
	previous := siteCacheRoot
	siteCacheRoot = filepath.Join(root, "cache")
	t.Cleanup(func() { siteCacheRoot = previous })
	if err := os.MkdirAll(siteCacheRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	var visits atomic.Int32
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, visits.Add(1))
	}))
	defer app.Close()
	spec := plainSpec("cached", "cached.test")
	spec.Upstream = app.URL
	spec.Buffering = true
	spec.ProxyCache = &ProxyCache{MaxSize: "10m", Valid: "1h"}
	port := freePort(t)
	installSite(t, root, spec, port)
	if result := runValidator(context.Background(), "nginx", "-t"); !result.Valid {
		t.Fatalf("nginx refused the cache: %s", result.Output)
	}
	startNginx(t, root)
	check := func(header, value, wantBody, wantCache string) {
		t.Helper()
		var response *http.Response
		var body string
		if header == "" {
			response, body = siteGet(t, port, "cached.test", "/")
		} else {
			response, body = siteGet(t, port, "cached.test", "/", header, value)
		}
		if response.StatusCode != http.StatusOK || body != wantBody || response.Header.Get("X-Cache-Status") != wantCache {
			t.Fatalf("%s request: status %d, body %q, cache %q; want %q and %q", header,
				response.StatusCode, body, response.Header.Get("X-Cache-Status"), wantBody, wantCache)
		}
	}
	check("", "", "1", "MISS")
	check("", "", "1", "HIT")
	check("Authorization", "Bearer private", "2", "BYPASS")
	check("Cookie", "session=private", "3", "BYPASS")
	check("", "", "1", "HIT")
	service := New(root, filepath.Join(root, "Caddyfile"))
	usage, err := service.PurgeSiteCache(spec.Name)
	if err != nil || usage.Files == 0 {
		t.Fatalf("purge found no cached files: %+v, %v", usage, err)
	}
	check("", "", "4", "MISS")
}
