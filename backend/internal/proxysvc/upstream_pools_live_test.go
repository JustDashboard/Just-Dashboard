package proxysvc

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// The host's nginx binary balancing a pool of two primaries, one of which
// refuses, and a backup: the report finds the pool nginx loads, its servers'
// states, and nginx's own record of the dead one refusing it.
func TestLivePoolOutcomesComeFromNginxItself(t *testing.T) {
	root := liveNginx(t)
	previous := nginxLogRoot
	nginxLogRoot = filepath.Join(root, "logs")
	t.Cleanup(func() { nginxLogRoot = previous })

	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "live") }))
	defer live.Close()
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "backup") }))
	defer backup.Close()
	closed, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := closed.Addr().String()
	closed.Close()

	spec := plainSpec("pool", "pool.test")
	spec.Upstream = ""
	spec.Pool = &SitePool{Servers: []PoolServer{
		{Address: strings.TrimPrefix(live.URL, "http://")},
		{Address: dead, MaxFails: 1, FailTimeout: 60},
		{Address: strings.TrimPrefix(backup.URL, "http://"), Backup: true},
	}}
	port := freePort(t)
	installSite(t, root, spec, port)
	startNginx(t, root)
	for i := 0; i < 6; i++ {
		if response, body := siteGet(t, port, "pool.test", "/"); response.StatusCode != http.StatusOK || body != "live" {
			t.Fatalf("request %d: status %d, body %q", i+1, response.StatusCode, body)
		}
	}

	service := New(root, filepath.Join(root, "Caddyfile"))
	report, err := NewUpstreamMonitor(service).Report(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if report.Evidence == nil || len(report.Evidence.Logs) != 1 || !report.Evidence.Complete {
		t.Fatalf("evidence = %+v", report.Evidence)
	}
	var pool *UpstreamPool
	for i := range report.Pools {
		if report.Pools[i].Name == "jd_pool_pool" {
			pool = &report.Pools[i]
		}
	}
	if pool == nil {
		t.Fatalf("no pool in %+v", report.Pools)
	}
	if pool.Balancing != BalancingNative || pool.Verdict != PoolDegraded || len(pool.Members) != 3 {
		t.Fatalf("pool = %+v", pool)
	}
	byAddress := map[string]PoolMember{}
	for _, m := range pool.Members {
		byAddress[m.Address] = m
	}
	// nginx logs setting a server aside at warn, below the default error
	// level this prefix logs at; the refusals themselves are errors.
	if m := byAddress[dead]; m.State != UpstreamRefused || m.Failures["refused"] == 0 || m.LastFailure == nil {
		t.Fatalf("the refusing server = %+v", m)
	}
	if m := byAddress[strings.TrimPrefix(live.URL, "http://")]; m.State != UpstreamUp || len(m.Failures) != 0 {
		t.Fatalf("the live server = %+v", m)
	}
}
