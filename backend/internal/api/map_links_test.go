package api

import (
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/gitx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/linuxusers"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/Wayy01/Just-Dashboard/backend/internal/selfcfg"
	"github.com/Wayy01/Just-Dashboard/backend/internal/selfupdate"
)

const cacheID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// The sample server, wired every way the server records: a deployment built
// from a checkout and another from a remote, a variable referring to a
// database by name and one through its pooler, the topology seeing a
// container's environment name a database, sites and a stream forwarding
// to an app, a unit, the dashboard and a container, and the accounts, keys,
// rules and jobs that name each other.
func linkedInputs() mapInputs {
	in := sampleInputs()
	in.projects.Value[0].SourcePath = "/srv/shop/app"
	in.projects.Value[0].Images = []string{"docker.io/library/node:22"}
	in.containers.Value[0].ComposeStack = "shop"
	in.projects.Value = append(in.projects.Value, deploy.DeploymentSummary{
		ID: 8, Name: "blog", EnvironmentID: 80, SourceKind: deploy.SourceGit,
		SourceRemote: "https://github.com/Acme/Blog.git",
	})
	in.containers.Value = append(in.containers.Value,
		dockerx.Container{ID: "c-worker", Name: "worker", Image: "python:3", State: "running"},
		dockerx.Container{ID: "c-pooler", Name: "pg-pooler", Image: "pgbouncer", State: "running"},
		dockerx.Container{ID: cacheID, Name: "cache", Image: "redis:7", State: "running"},
		dockerx.Container{
			ID: "c-self", Name: "jd-backend", Image: "just-dashboard", State: "running",
			ComposeStack: "just-dashboard", ComposeSvc: "backend",
			Mounts: []dockerx.MountPoint{{Type: "bind", Source: "/var/lib/just-dashboard", Destination: "/data"}},
		},
	)
	in.dataDir = "/data"
	in.volumes.Value = []dockerx.Volume{
		{Name: "shop-data", Mountpoint: "/var/lib/docker/volumes/shop-data/_data"},
		{Name: "blog-data", Mountpoint: "/var/lib/docker/volumes/blog-data/_data",
			Labels: map[string]string{"com.docker.compose.project": "data"}},
	}
	in.networks.Value[0].Labels = map[string]string{"com.docker.compose.project": "data"}
	in.databases.Value = append(in.databases.Value, fleetEntry{
		dbConnection: &dbConnection{ID: 5, Name: "analytics", Driver: "postgres"},
		State:        dbStateRunning, Unit: "postgresql@16-main.service",
	})
	in.relations = mapGot(mapRelations{
		refs: []mapProjectRef{
			{projectID: 8, kind: "database", target: "analytics.url", via: "reference"},
			{projectID: 7, kind: "database", target: "4.pooled.jdbc", via: "reference"},
			{projectID: 8, kind: "domain", target: "Blog.example.com", via: "reference"},
			{projectID: 8, kind: "storage", target: "blog-data", via: "dependency"},
			{projectID: 8, kind: "backup", target: "2", via: "dependency"},
			{projectID: 8, kind: "database", target: "gone", via: "reference"},
		},
		poolers: map[int64]string{4: "pg-pooler"},
	})
	in.uses = mapGot([]mapUse{
		{connID: 5, projectID: 8, via: "env"},
		{connID: 4, container: "worker", via: "env"},
		{connID: 4, container: "worker", via: "session"},
	})
	in.repos = mapGot([]gitx.Repo{
		{Path: "/srv/shop", Name: "shop"},
		{Path: "/srv", Name: "everything"},
		{Path: "/srv/blog", Name: "blog", Remote: "git@github.com:acme/blog.git"},
		{Path: "/home/deploy/blog", Name: "blog", Remote: "https://github.com/acme/blog"},
		{Path: "/srv/other", Name: "other", Remote: "https://github.com/acme/other.git"},
	})
	in.vhosts.Value = append(in.vhosts.Value,
		proxysvc.VHost{Name: "api.conf", Enabled: true, ServerNames: []string{"api.example.com"}, Upstreams: []string{"http://127.0.0.1:3000"}},
		proxysvc.VHost{Name: "admin.conf", Enabled: true, ServerNames: []string{"admin.example.com"}, Upstreams: []string{"http://localhost:8080"}},
		proxysvc.VHost{Name: "dash.conf", Enabled: true, ServerNames: []string{"dash.example.com"}, Upstreams: []string{"http://127.0.0.1:8090"}},
		proxysvc.VHost{Name: "blog.conf", Enabled: true, ServerNames: []string{"blog.example.com"}},
	)
	in.streams = mapGot(mapStreams{Streams: []proxysvc.StreamEntry{
		{StreamSpec: proxysvc.StreamSpec{Name: "redis", Listen: 16379, Protocol: "tcp", Upstream: "127.0.0.1:6379"}},
		// Forwarded port for port, over UDP.
		{StreamSpec: proxysvc.StreamSpec{Name: "game", Listen: 27015, ListenEnd: 27030, SamePort: true,
			Protocol: "udp", Upstream: "127.0.0.1"}},
	}})
	in.listeners = mapGot([]proxysvc.Listener{
		{Protocol: "tcp", Address: "127.0.0.1", Port: 3000, PID: 4242, Manager: "pm2", ManagerName: "api"},
		{Protocol: "tcp", Address: "0.0.0.0", Port: 8080, PID: 900, Manager: "systemd", ManagerName: "api.service"},
		{Protocol: "tcp", Address: "127.0.0.1", Port: 8090, PID: 1, Self: true},
		{Protocol: "tcp", Address: "127.0.0.1", Port: 6379, Container: &proxysvc.ListenerContainer{ID: cacheID[:12], Name: "cache"}},
		{Protocol: "udp", Address: "127.0.0.1", Port: 27020, PID: 77, Manager: "systemd", ManagerName: "game.service"},
		// A TCP socket on a port only a UDP stream forwards to.
		{Protocol: "tcp", Address: "127.0.0.1", Port: 27021, PID: 78, Manager: "systemd", ManagerName: "web.service"},
		// Nothing forwards here: never looked up.
		{Protocol: "tcp", Address: "0.0.0.0", Port: 22, Manager: "systemd", ManagerName: "ssh.service"},
	})
	in.units = mapGot(&procs.Inventory{Units: []procs.Unit{
		{Name: "certbot.service", LoadState: "loaded"},
		{Name: "api.service", LoadState: "loaded", ActiveState: "active"},
		{Name: "ssh.service", LoadState: "loaded", ActiveState: "active"},
		{Name: "game.service", LoadState: "loaded", ActiveState: "active"},
		{Name: "web.service", LoadState: "loaded", ActiveState: "active"},
		{Name: "postgresql@16-main.service", LoadState: "loaded", ActiveState: "active"},
	}})
	in.timers = mapGot([]procs.Timer{{Unit: "certbot.timer", Activates: "certbot.service", ActiveState: "active"}})
	in.pm2 = mapGot([]procs.PM2Process{{DaemonID: "d", ID: 0, Name: "api", Status: "online", PID: 4242, User: "deploy", CWD: "/srv/shop/api"}})
	in.dashboard = mapGot(selfupdate.Report{Version: "0.8.2"})
	in.terminals = mapGot([]mapTerminal{{ID: "t1", Title: "work", User: "deploy", Windows: 1}})
	in.sysUsers = mapGot([]linuxusers.User{{Username: "deploy"}})
	in.cron = mapGot([]procs.Crontab{{Source: "/etc/cron.d/jobs", Jobs: []procs.CronJob{
		{Line: 3, Schedule: "0 * * * *", User: "deploy", Command: "/srv/shop/bin/sweep"},
	}}})
	in.channels = mapGot([]deploy.NotificationChannel{{ID: 10, Name: "ops", Enabled: true}, {ID: 11, Name: "quiet"}})
	in.alerts = mapGot(mapAlerts{
		Host:  []hostAlertRule{{ID: 1, Kind: "cpu_high", Enabled: true}},
		Proxy: []proxyAlertRule{{ID: 2, Kind: "cert_expiring", Enabled: true, Channels: []int64{11}}},
	})
	in.accounts = mapGot([]*auth.User{{ID: 1, Username: "admin", Role: auth.RoleAdmin}})
	in.keys = mapGot([]auth.APIToken{
		{ID: 3, UserID: 1, Name: "assistant", Role: auth.RoleReadOnly, Scope: "mcp",
			Connections: []auth.TokenConnection{{ConnectionID: 5}}},
		{ID: 4, UserID: 1, Name: "old", Revoked: true, Connections: []auth.TokenConnection{{ConnectionID: 5}}},
	})
	in.backups.Value[0].Sources = []string{"/var/lib/docker/volumes", "/srv/shop"}
	in.backups.Value[0].Excludes = []string{"/var/lib/docker/volumes/shop-data", "*.log"}
	return in
}

func TestMapDrawsEveryLineTheServerRecords(t *testing.T) {
	m := buildServerMap(linkedInputs())
	for _, want := range []struct{ from, to, via string }{
		// A deployment's database: by a reference to its name, and by the
		// topology seeing it named in the running environment (one line).
		{"project:8", "db:5", "reference"},
		// A container that is no deployment's, named in its environment.
		{"container:c-worker", "db:4", "env"},
		{"container:c-pooler", "db:4", "pooler"},
		{"unit:postgresql@16-main.service", "db:5", "serves"},
		// What a deployment's plan names.
		{"project:8", "domain:blog.example.com", "reference"},
		{"project:8", "volume:blog-data", "dependency"},
		{"project:8", "backup:2", "dependency"},
		// What it is built from and what it runs.
		{"project:7", "repo:/srv/shop", "source"},
		{"project:8", "repo:/srv/blog", "source"},
		{"project:8", "repo:/home/deploy/blog", "source"},
		{"project:7", "image:sha256:img", "image"},
		{"project:7", "stack:shop", "release"},
		// Sites and a stream to what holds their port.
		{"domain:api.example.com", "pm2:d:0", "route"},
		{"domain:admin.example.com", "unit:api.service", "route"},
		{"domain:dash.example.com", "dashboard:self", "route"},
		{"stream:redis", "container:" + cacheID, "route"},
		{"stream:game", "unit:game.service", "route"},
		{"dashboard:self", "stack:just-dashboard", "self"},
		// A stack's own volume and network.
		{"stack:data", "volume:blog-data", "member"},
		{"stack:data", "network:n1", "member"},
		// Where an app runs and as whom.
		{"pm2:d:0", "repo:/srv/shop", "workdir"},
		{"pm2:d:0", "sysuser:deploy", "user"},
		{"terminal:t1", "sysuser:deploy", "user"},
		{"cron:/etc/cron.d/jobs:3", "sysuser:deploy", "user"},
		{"timer:certbot.timer", "unit:certbot.service", "starts"},
		// A rule naming no channel tells every enabled one; one naming a
		// channel tells that one, enabled or not.
		{"alert:host:1", "channel:10", "notifies"},
		{"alert:proxy:2", "channel:11", "notifies"},
		{"key:3", "account:1", "owner"},
		{"key:3", "db:5", "access"},
		// The folders a backup takes, less what it leaves out.
		{"volume:blog-data", "backup:2", "backup"},
		{"repo:/srv/shop", "backup:2", "backup"},
	} {
		if !hasLink(m, want.from, want.to, want.via) {
			t.Errorf("missing %s -%s-> %s", want.from, want.via, want.to)
		}
	}
	for _, unwanted := range []struct{ from, to string }{
		{"alert:host:1", "channel:11"},
		{"volume:shop-data", "backup:2"},
		{"project:8", "repo:/srv/other"},
		// The deepest checkout holding the folder, not every one.
		{"project:7", "repo:/srv"},
		{"key:4", "db:5"},
		{"stream:game", "unit:web.service"},
	} {
		for _, l := range m.Links {
			if (l.From == unwanted.from && l.To == unwanted.to) || (l.From == unwanted.to && l.To == unwanted.from) {
				t.Errorf("drew %v", l)
			}
		}
	}
	for _, l := range m.Links {
		if strings.Contains(l.To, "ssh.service") || strings.Contains(l.From, "ssh.service") {
			t.Errorf("a socket nothing forwards to was joined: %v", l)
		}
	}
}

func TestMapDrawsAPairKnownSeveralWaysOnce(t *testing.T) {
	m := buildServerMap(linkedInputs())
	pairs := map[string]mapLink{}
	for _, l := range m.Links {
		key := l.From + " " + l.To
		if l.To < l.From {
			key = l.To + " " + l.From
		}
		if have, ok := pairs[key]; ok {
			t.Errorf("two lines between one pair: %v and %v", have, l)
		}
		pairs[key] = l
	}
	// Bound, and referred to through its pooler: the binding names the line.
	shop := pairs["db:4 project:7"]
	if shop.Via != "binding" || len(shop.Also) != 1 || shop.Also[0] != "reference" {
		t.Errorf("shop's database = %+v, want a binding also known by reference", shop)
	}
	blog := pairs["db:5 project:8"]
	if blog.Via != "reference" || len(blog.Also) != 1 || blog.Also[0] != "env" {
		t.Errorf("blog's database = %+v, want a reference also seen in its env", blog)
	}
	worker := pairs["container:c-worker db:4"]
	if worker.Via != "env" || len(worker.Also) != 1 || worker.Also[0] != "session" {
		t.Errorf("worker = %+v, want named in env, also connected", worker)
	}
	// The domain's owner route and the deployment's reference to it.
	blogDomain := pairs["domain:blog.example.com project:8"]
	if blogDomain.Via != "reference" {
		t.Errorf("blog's domain = %+v", blogDomain)
	}
}

func TestMergeLinksKeepsTheSurestWayAndItsDirection(t *testing.T) {
	got := mergeLinks([]mapLink{
		{From: "db:1", To: "project:1", Via: "env"},
		{From: "project:1", To: "db:1", Via: "binding"},
		{From: "project:1", To: "db:1", Via: "session"},
		{From: "db:1", To: "project:1", Via: "env"},
		{From: "a", To: "b", Via: "mount"},
	})
	if len(got) != 2 {
		t.Fatalf("links = %v", got)
	}
	if l := got[0]; l.From != "project:1" || l.To != "db:1" || l.Via != "binding" ||
		strings.Join(l.Also, ",") != "env,session" {
		t.Errorf("merged = %+v", l)
	}
	if got[1].Also != nil {
		t.Errorf("a pair known one way has no also: %+v", got[1])
	}
}

func TestRemoteKeyReadsOneRepositoryEveryWayItIsWritten(t *testing.T) {
	same := []string{
		"https://github.com/Acme/Shop.git",
		"https://token@github.com/acme/shop",
		"git@github.com:acme/shop.git",
		"ssh://git@github.com/acme/shop.git",
		"github.com:acme/shop/",
	}
	for _, r := range same {
		if got := remoteKey(r); got != "github.com/acme/shop" {
			t.Errorf("remoteKey(%q) = %q", r, got)
		}
	}
	if got := remoteKey("/srv/git/shop.git"); got != "/srv/git/shop" {
		t.Errorf("a folder remote = %q", got)
	}
	for _, r := range []string{"", "shop", "https://"} {
		if got := remoteKey(r); got != "" {
			t.Errorf("remoteKey(%q) = %q, want nothing", r, got)
		}
	}
}

func TestWithinIsAWholeFolder(t *testing.T) {
	for _, c := range []struct {
		path, dir string
		want      bool
	}{
		{"/srv/shop", "/srv/shop", true},
		{"/srv/shop/api", "/srv/shop/", true},
		{"/srv/shopping", "/srv/shop", false},
		{"/anything", "/", true},
		{"/srv", "/srv/shop", false},
	} {
		if got := within(c.path, c.dir); got != c.want {
			t.Errorf("within(%q, %q) = %v", c.path, c.dir, got)
		}
	}
}

func TestMapRelationsReadsReferencesDependenciesAndPoolers(t *testing.T) {
	s := testServer(t)
	db := s.Store.DB
	seal := func(v string) string {
		sealed, err := s.Sealer.Seal(v)
		if err != nil {
			t.Fatal(err)
		}
		return sealed
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO deploy_projects(id,name,repo_path,hook_secret,hook_id,created_at) VALUES(7,'shop','/srv/shop','s','h7',1)`, nil},
		{`INSERT INTO deploy_projects(id,name,repo_path,hook_secret,hook_id,created_at,archived_at) VALUES(8,'gone','/srv/gone','s','h8',1,5)`, nil},
		{`INSERT INTO deploy_environments(id,project_id,name,slug,created_at,updated_at) VALUES(70,7,'production','production',1,1)`, nil},
		{`INSERT INTO deploy_environments(id,project_id,name,slug,created_at,updated_at) VALUES(80,8,'production','production',1,1)`, nil},
		{`INSERT INTO deploy_variable_revisions(environment_id,key,revision,value_enc,active,created_at,value_mode) VALUES(70,'DATABASE_URL',2,?,1,1,'reference')`,
			[]any{seal("${{database.main.url}}")}},
		// An older revision, no longer the variable's value.
		{`INSERT INTO deploy_variable_revisions(environment_id,key,revision,value_enc,active,created_at,value_mode) VALUES(70,'DATABASE_URL',1,?,0,1,'reference')`,
			[]any{seal("${{database.old.url}}")}},
		// Written before modes were recorded: read for a reference, and a
		// literal is dropped.
		{`INSERT INTO deploy_variable_revisions(environment_id,key,revision,value_enc,active,created_at,value_mode) VALUES(70,'SITE',1,?,1,1,'')`,
			[]any{seal("${{domain.shop.example.com}}")}},
		{`INSERT INTO deploy_variable_revisions(environment_id,key,revision,value_enc,active,created_at,value_mode) VALUES(70,'TOKEN',1,?,1,1,'')`,
			[]any{seal("s3cr3t")}},
		{`INSERT INTO deploy_variable_revisions(environment_id,key,revision,value_enc,active,created_at,value_mode) VALUES(70,'API_KEY',1,?,1,1,'reference')`,
			[]any{seal("${{credential.stripe}}")}},
		{`INSERT INTO deploy_variable_revisions(environment_id,key,revision,value_enc,active,created_at,value_mode) VALUES(80,'DATABASE_URL',1,?,1,1,'reference')`,
			[]any{seal("${{database.main.url}}")}},
		{`INSERT INTO deploy_dependencies(environment_id,release_id,kind,ownership,resource_kind,resource_id,created_at) VALUES(70,0,'storage','managed','docker_volume','shop-data',1)`, nil},
		{`INSERT INTO deploy_dependencies(environment_id,release_id,kind,ownership,resource_kind,resource_id,created_at) VALUES(70,0,'storage','linked','bind_path','/srv/shop/uploads',1)`, nil},
		// A release's own copy of the plan, not the environment's.
		{`INSERT INTO deploy_dependencies(environment_id,release_id,kind,ownership,resource_kind,resource_id,created_at) VALUES(70,3,'backup','linked','backup_job','9',1)`, nil},
		{`INSERT INTO db_connections(id,name,driver,dsn_enc,created_at) VALUES(4,'main','postgres','x',1)`, nil},
		{`INSERT INTO db_poolers(connection_id,container_name,network_name,server_name,server_port,pool_mode,pool_size,max_client_conn,auth_mode,secrets_enc,created_at,updated_at)
		   VALUES(4,'jd-pooler-4','net','pg',5432,'transaction',10,100,'scram','x',1,1)`, nil},
	} {
		if _, err := db.Exec(q.sql, q.args...); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
	}
	got, err := s.mapRelations(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := []mapProjectRef{
		{projectID: 7, kind: "database", target: "main.url", via: "reference"},
		{projectID: 7, kind: "domain", target: "shop.example.com", via: "reference"},
		{projectID: 7, kind: "storage", target: "shop-data", via: "dependency"},
	}
	if len(got.refs) != len(want) {
		t.Fatalf("refs = %+v, want %+v", got.refs, want)
	}
	for i := range want {
		if got.refs[i] != want[i] {
			t.Errorf("refs[%d] = %+v, want %+v", i, got.refs[i], want[i])
		}
	}
	if got.poolers[4] != "jd-pooler-4" {
		t.Errorf("poolers = %v", got.poolers)
	}
}

// This dashboard is drawn as a system of its own: the address it answers on,
// the tailnet that carries it where its configuration says so, and the store
// kept in the container mounting its data directory.
func TestMapDrawsTheDashboardsAddressAndStore(t *testing.T) {
	in := linkedInputs()
	in.site, in.tlsMode = "box.tailnet.ts.net", selfcfg.TLSTailscale
	in.store = mapStore{Path: "/data/vpsd.db", Size: 5 << 20}
	in.vpn = mapGot(netx.VPNSummary{Tailscale: netx.TSSummary{Installed: true, Running: true}})
	m := buildServerMap(in)

	site := itemOf(t, m, "dashboard", "dashboard:site")
	if site.Name != "box.tailnet.ts.net" || site.State != "https" || !strings.Contains(site.Detail, "Tailscale") {
		t.Fatalf("site = %+v", site)
	}
	store := itemOf(t, m, "dashboard", "dashboard:store")
	if store.Name != "vpsd.db" || store.Product != "sqlite" || store.Detail != "5.0 MB" {
		t.Fatalf("store = %+v", store)
	}
	for _, f := range store.Facts {
		if strings.Contains(f.Value, "/data") {
			t.Fatalf("the store's folder is not the map's to send: %+v", store.Facts)
		}
	}
	for _, want := range [][3]string{
		{"dashboard:site", "dashboard:self", "route"},
		{"dashboard:site", "vpn:tailscale", "tailnet"},
		{"container:c-self", "dashboard:store", "mount"},
		{"dashboard:self", "stack:just-dashboard", "self"},
	} {
		if !hasLink(m, want[0], want[1], want[2]) {
			t.Errorf("no %s → %s (%s) in %v", want[0], want[1], want[2], m.Links)
		}
	}
}

func TestMapDashboardSiteIsOnTheTailnetOnlyWhereItsConfigurationSays(t *testing.T) {
	for _, tc := range []struct {
		site, tls string
		tailnet   bool
		state     string
	}{
		{"dash.example.com", selfcfg.TLSInternal, false, "https"},
		{"https://Box.Tailnet.TS.net:8443", selfcfg.TLSInternal, true, "https"},
		{"localhost", selfcfg.TLSOff, false, "http"},
	} {
		in := linkedInputs()
		in.site, in.tlsMode = tc.site, tc.tls
		in.vpn = mapGot(netx.VPNSummary{Tailscale: netx.TSSummary{Installed: true, Running: true}})
		m := buildServerMap(in)
		if got := hasLink(m, "dashboard:site", "vpn:tailscale", "tailnet"); got != tc.tailnet {
			t.Errorf("%s over %s: tailnet line = %v, want %v", tc.site, tc.tls, got, tc.tailnet)
		}
		if got := itemOf(t, m, "dashboard", "dashboard:site").State; got != tc.state {
			t.Errorf("%s over %s: state = %q, want %q", tc.site, tc.tls, got, tc.state)
		}
	}
	// No address configured, no box; and no store read, none either.
	in := linkedInputs()
	m := buildServerMap(in)
	for _, item := range kindOf(t, m, "dashboard").Items {
		if item.ID != "dashboard:self" {
			t.Fatalf("unexpected %s with no site and no store", item.ID)
		}
	}
}
