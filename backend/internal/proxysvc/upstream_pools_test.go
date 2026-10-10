package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const poolsConf = `http {
    upstream jd_shop_pool {
        least_conn;
        keepalive 16;
        server 10.0.0.1:8080 weight=3 max_fails=2 fail_timeout=15s;
        server 10.0.0.2:8080;
        server 10.0.0.3:8080 backup;
        server 10.0.0.4:8080 down;
    }
    server {
        server_name shop.example.com;
        location / { proxy_pass http://jd_shop_pool; }
    }
    server {
        server_name api.example.com;
        location / { proxy_pass http://api.internal:9000; }
    }
    server {
        server_name edge.example.com;
        location / { proxy_pass https://my-lb-1.eu-west-1.elb.amazonaws.com; }
    }
    server {
        server_name dyn.example.com;
        location / { proxy_pass http://$backend; }
    }
}
stream {
    upstream db_backend {
        server 10.0.1.5:5432;
        server 10.0.1.6:5432 backup;
    }
    server {
        listen 5432;
        proxy_pass db_backend;
    }
}
`

func poolTree(t *testing.T) []Directive {
	t.Helper()
	tree, err := ParseNginxFile("/etc/nginx/nginx.conf", poolsConf, nil)
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

// checkedAs gives every target the state its address has in states, up by
// default, as CheckUpstreams would have.
func checkedAs(targets []UpstreamTarget, states map[string]UpstreamState) []UpstreamTarget {
	for i := range targets {
		if targets[i].State == UpstreamDynamic {
			continue
		}
		targets[i].State = UpstreamUp
		if s, ok := states[targets[i].Address]; ok {
			targets[i].State = s
		}
	}
	return targets
}

func resolveOnly(names map[string][]string) Resolver {
	return func(_ context.Context, host string) ([]string, error) {
		if addrs, ok := names[host]; ok {
			return addrs, nil
		}
		return nil, errors.New("no such host")
	}
}

func poolNamed(t *testing.T, pools []UpstreamPool, name, site string) UpstreamPool {
	t.Helper()
	for _, p := range pools {
		if p.Name == name && (site == "" || len(p.Sites) > 0 && p.Sites[0] == site) {
			return p
		}
	}
	t.Fatalf("no pool %q for %q in %+v", name, site, pools)
	return UpstreamPool{}
}

// A block of several servers is nginx's own balancing, with its method and
// each server's options, down ones listed though never checked; a single
// address is one endpoint whose spreading, if any, is someone else's.
func TestUpstreamPoolsSayWhoBalances(t *testing.T) {
	tree := poolTree(t)
	checked := checkedAs(UpstreamTargets(tree), nil)
	pools := UpstreamPools(context.Background(), tree, checked, passiveFailures{},
		resolveOnly(map[string][]string{"api.internal": {"10.0.2.2", "10.0.2.1"}}))
	if len(pools) != 4 {
		t.Fatalf("pools = %+v, want shop's block, api, edge and the stream's block (dyn is dynamic)", pools)
	}

	shop := poolNamed(t, pools, "jd_shop_pool", "")
	if shop.Balancing != BalancingNative || shop.Method != "least_conn" || shop.Keepalive != 16 || shop.Verdict != PoolServing {
		t.Fatalf("shop = %+v", shop)
	}
	if fmt.Sprint(shop.Sites) != "[shop.example.com]" || len(shop.Members) != 4 {
		t.Fatalf("shop sites %v members %+v", shop.Sites, shop.Members)
	}
	first, down := shop.Members[0], shop.Members[3]
	if first.Weight != 3 || first.MaxFails != 2 || first.FailTimeout != "15s" || first.State != UpstreamUp {
		t.Fatalf("first member = %+v", first)
	}
	if !down.Down || down.State != "" || !shop.Members[2].Backup {
		t.Fatalf("backup %+v down %+v", shop.Members[2], down)
	}

	api := poolNamed(t, pools, "", "api.example.com")
	if api.Balancing != BalancingNativeDNS || fmt.Sprint(api.Members[0].Resolved) != "[10.0.2.1 10.0.2.2]" {
		t.Fatalf("api = %+v", api)
	}
	edge := poolNamed(t, pools, "", "edge.example.com")
	if edge.Balancing != BalancingSingle || edge.Provider != "AWS Elastic Load Balancing" || edge.Method != "" {
		t.Fatalf("edge = %+v", edge)
	}
	stream := poolNamed(t, pools, "db_backend", "")
	if stream.Kind != "stream" || stream.Balancing != BalancingNative || stream.Method != "round-robin" {
		t.Fatalf("stream = %+v", stream)
	}
}

// The verdict is what a visitor meets: every primary, fewer of them, a
// backup, or nothing.
func TestUpstreamPoolVerdicts(t *testing.T) {
	tree := poolTree(t)
	for _, tc := range []struct {
		name   string
		states map[string]UpstreamState
		want   string
	}{
		{"all primaries", nil, PoolServing},
		{"one primary refused", map[string]UpstreamState{"10.0.0.1:8080": UpstreamRefused}, PoolDegraded},
		{"backup only", map[string]UpstreamState{"10.0.0.1:8080": UpstreamRefused, "10.0.0.2:8080": UpstreamTimeout}, PoolOnBackup},
		{"nothing", map[string]UpstreamState{"10.0.0.1:8080": UpstreamRefused, "10.0.0.2:8080": UpstreamTimeout, "10.0.0.3:8080": UpstreamRefused}, PoolDown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pools := UpstreamPools(context.Background(), tree, checkedAs(UpstreamTargets(tree), tc.states), passiveFailures{}, nil)
			if got := poolNamed(t, pools, "jd_shop_pool", "").Verdict; got != tc.want {
				t.Fatalf("verdict = %s, want %s", got, tc.want)
			}
		})
	}
}

// What nginx logged over the window is attributed to the server it names —
// a name's resolved addresses included — and "no live upstreams" to the
// block; a line before the window and one about no upstream are not counted.
func TestUpstreamPoolsCountWhatNginxLogged(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "error.log")
	now := time.Now()
	stamp := func(ago time.Duration) string { return now.Add(-ago).Format("2006/01/02 15:04:05") }
	lines := []string{
		stamp(2*time.Hour) + ` [error] 11#11: *1 connect() failed (111: Connection refused) while connecting to upstream, client: 1.2.3.4, server: shop.example.com, request: "GET / HTTP/1.1", upstream: "http://10.0.0.1:8080/", host: "shop.example.com"`,
		stamp(30*time.Minute) + ` [error] 11#11: *2 connect() failed (111: Connection refused) while connecting to upstream, client: 1.2.3.4, server: shop.example.com, request: "GET / HTTP/1.1", upstream: "http://10.0.0.1:8080/", host: "shop.example.com"`,
		stamp(30*time.Minute) + ` [warn] 11#11: *2 upstream server temporarily disabled while connecting to upstream, client: 1.2.3.4, server: shop.example.com, request: "GET / HTTP/1.1", upstream: "http://10.0.0.1:8080/", host: "shop.example.com"`,
		stamp(20*time.Minute) + ` [error] 11#11: *3 upstream timed out (110: Connection timed out) while reading response header from upstream, client: 1.2.3.4, server: api.example.com, request: "GET / HTTP/1.1", upstream: "http://10.0.2.2:9000/", host: "api.example.com"`,
		stamp(10*time.Minute) + ` [error] 11#11: *4 no live upstreams while connecting to upstream, client: 1.2.3.4, server: shop.example.com, request: "GET / HTTP/1.1", upstream: "http://jd_shop_pool/", host: "shop.example.com"`,
		stamp(5*time.Minute) + ` [error] 11#11: *5 open() "/srv/x" failed (2: No such file or directory), client: 1.2.3.4, server: shop.example.com, request: "GET /x HTTP/1.1", host: "shop.example.com"`,
		stamp(time.Minute) + ` [error] 11#11: *6 connect() failed (111: Connection refused) while connecting to upstream, client: 1.2.3.4, server: 0.0.0.0:5432, upstream: "10.0.1.5:5432", bytes from/to client:0/0, bytes from/to upstream:0/0`,
	}
	if err := os.WriteFile(log, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	failures, complete := readPassive([]string{log}, now.Add(-time.Hour))
	if !complete {
		t.Fatal("a whole log read is complete")
	}
	tree := poolTree(t)
	pools := UpstreamPools(context.Background(), tree, checkedAs(UpstreamTargets(tree), nil), failures,
		resolveOnly(map[string][]string{"api.internal": {"10.0.2.1", "10.0.2.2"}}))
	shop := poolNamed(t, pools, "jd_shop_pool", "")
	if fmt.Sprint(shop.Members[0].Failures) != "map[disabled:1 refused:1]" || shop.Members[0].LastFailure == nil || shop.NoLive != 1 {
		t.Fatalf("shop = %+v", shop)
	}
	if shop.Members[1].Failures != nil {
		t.Fatalf("a quiet server has failures: %+v", shop.Members[1])
	}
	if api := poolNamed(t, pools, "", "api.example.com"); fmt.Sprint(api.Members[0].Failures) != "map[timeout:1]" {
		t.Fatalf("api = %+v", api)
	}
	if db := poolNamed(t, pools, "db_backend", ""); fmt.Sprint(db.Members[0].Failures) != "map[refused:1]" {
		t.Fatalf("stream = %+v", db)
	}
}

// Only logs inside nginx's log directory are read, and with none named the
// default is.
func TestRequestErrorLogsStayInsideTheLogDirectory(t *testing.T) {
	root := t.TempDir()
	previous := nginxLogRoot
	nginxLogRoot = root
	t.Cleanup(func() { nginxLogRoot = previous })
	conf := fmt.Sprintf("error_log %[1]s/error.log;\nhttp {\n error_log /etc/passwd;\n server { error_log %[1]s/shop.error.log warn; }\n server { error_log %[1]s/error.log; }\n}\n", root)
	tree, err := ParseNginxFile("/etc/nginx/nginx.conf", conf, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := requestErrorLogs(tree)
	if fmt.Sprint(got) != fmt.Sprintf("[%[1]s/error.log %[1]s/shop.error.log]", root) {
		t.Fatalf("logs = %v", got)
	}
	if got := requestErrorLogs(nil); fmt.Sprint(got) != fmt.Sprintf("[%s/error.log]", root) {
		t.Fatalf("default = %v", got)
	}
}
