package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	fc "github.com/Wayy01/Just-Dashboard/backend/internal/filecontext"
	"github.com/Wayy01/Just-Dashboard/backend/internal/gitx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// hold puts a reading on the map's shelf as if a source had just answered,
// so the route reads it rather than the machine the test runs on.
func hold(s *Server, key string, value any) {
	s.mapSources.mu.Lock()
	defer s.mapSources.mu.Unlock()
	if s.mapSources.entries == nil {
		s.mapSources.entries = map[string]*mapEntry{}
	}
	s.mapSources.entries[key] = &mapEntry{value: value, have: true, at: time.Now()}
}

// holdNothing leaves a source being read and never answering.
func holdNothing(s *Server, key string) {
	s.mapSources.mu.Lock()
	defer s.mapSources.mu.Unlock()
	if s.mapSources.entries == nil {
		s.mapSources.entries = map[string]*mapEntry{}
	}
	s.mapSources.entries[key] = &mapEntry{inflight: make(chan struct{})}
}

// holdEverything gives every source the route reads an answer, empty unless
// given, and the databases inventory a kept reading.
func holdEverything(s *Server, given map[string]any) {
	empty := map[string]any{
		"containers":    []dockerx.Container{},
		"volumes":       []dockerx.Volume{},
		"stacks":        []dockerx.ComposeStack{},
		"projects":      []deploy.DeploymentSummary{},
		"deployFolders": []contextDeployFolder{},
		"vhosts":        []proxysvc.VHost{},
		"certificates":  []proxysvc.Certificate{},
		"backups":       []*backups.Job{},
		"repositories":  []gitx.Repo{},
		"units":         &procs.Inventory{},
		"pm2":           []procs.PM2Process{},
	}
	for key, value := range empty {
		if v, ok := given[key]; ok {
			value = v
		}
		hold(s, key, value)
	}
	s.dbInventory.mu.Lock()
	s.dbInventory.reduced = &dbInventoryKept{at: time.Now().UTC(), instances: []dbx.Instance{}}
	s.dbInventory.mu.Unlock()
}

func readContext(t *testing.T, c *client, path string) fileContextResponse {
	t.Helper()
	w := c.do(http.MethodGet, query("/api/v1/files/context", map[string]string{"path": path, "hidden": "true"}), "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("context of %s: %d %s", path, w.Code, strings.TrimSpace(w.Body.String()))
	}
	var out fileContextResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func contextFact(facts []fc.Fact, kind string) *fc.Fact {
	for i := range facts {
		if facts[i].Kind == kind {
			return &facts[i]
		}
	}
	return nil
}

// A folder seen through a symlink is the folder it is, an entry that is a
// symlink is what it points at, and /srv/app2 is not inside /srv/app — all
// through the route a reader with the narrowest role reaches.
func TestFileContextSaysWhatAFolderIsThroughTheRoute(t *testing.T) {
	_, s := newClient(t)
	reader := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "context-reader", auth.RoleReadOnly)}
	root := s.modules.files.Roots()[0]
	app := filepath.Join(root, "srv", "app")
	for _, dir := range []string{app, filepath.Join(root, "srv", "app2"), filepath.Join(root, "srv", "app", "data")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(app, filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
	holdEverything(s, map[string]any{
		"deployFolders": []contextDeployFolder{
			{projectID: 7, name: "shop", path: app},
			{projectID: 7, name: "shop", path: filepath.Join(app, "data"), data: true},
		},
		"backups": []*backups.Job{{ID: 2, Name: "srv nightly", Sources: []string{filepath.Join(root, "srv", "app2")},
			TargetKind: backups.TargetB2, Schedule: "0 3 * * *", Enabled: true}},
	})

	top := readContext(t, reader, root)
	link := contextFact(top.Children[filepath.Join(root, "current")], fc.KindDeploySource)
	if link == nil || link.Names[0] != "shop" || link.Href != "/deploy/7" {
		t.Fatalf("the symlinked entry is not the deployment's source: %+v", top.Children)
	}

	through := readContext(t, reader, filepath.Join(root, "current"))
	if through.Path != app {
		t.Fatalf("answered for %q, want the folder the link is: %q", through.Path, app)
	}
	if contextFact(through.Facts, fc.KindDeploySource) == nil {
		t.Fatalf("browsing through the link lost the folder's facts: %+v", through.Facts)
	}
	// The folder matters and nothing covers it; its data child says what it is.
	if contextFact(through.Facts, fc.KindNotBackedUp) == nil {
		t.Fatalf("an uncovered deployment source did not say so: %+v", through.Facts)
	}
	if contextFact(through.Children[filepath.Join(app, "data")], fc.KindDeployData) == nil {
		t.Fatalf("the data folder is not named: %+v", through.Children)
	}

	srv := readContext(t, reader, filepath.Join(root, "srv"))
	if facts := srv.Children[filepath.Join(root, "srv", "app2")]; contextFact(facts, fc.KindDeploySource) != nil {
		t.Fatalf("app2 read as inside app: %+v", facts)
	}
	if contextFact(srv.Children[filepath.Join(root, "srv", "app2")], fc.KindBackup) == nil {
		t.Fatalf("app2's own backup is not said: %+v", srv.Children)
	}
	if len(srv.Partial) != 0 {
		t.Fatalf("every source was held, yet partial = %v", srv.Partial)
	}
}

func TestFileContextLeavesOutASlowSource(t *testing.T) {
	c, s := newClient(t)
	root := s.modules.files.Roots()[0]
	holdEverything(s, nil)
	holdNothing(s, "deployFolders")
	start := time.Now()
	got := readContext(t, c, root)
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("a slow source held the answer for %v", elapsed)
	}
	found := false
	for _, source := range got.Partial {
		found = found || source == fc.SourceDeploy
	}
	if !found {
		t.Fatalf("partial = %v, want the deployments named", got.Partial)
	}
}

// A source that failed is left out without being called partial: on a host
// with no proxy the page must not say it is still reading one.
func TestFileContextDoesNotCallAFailedSourcePartial(t *testing.T) {
	c, s := newClient(t)
	root := s.modules.files.Roots()[0]
	holdEverything(s, nil)
	s.mapSources.mu.Lock()
	s.mapSources.entries["deployFolders"] = &mapEntry{err: errors.New("no store"), failedAt: time.Now()}
	s.mapSources.mu.Unlock()
	got := readContext(t, c, root)
	for _, source := range got.Partial {
		if source == fc.SourceDeploy {
			t.Fatalf("a failed source was called partial: %v", got.Partial)
		}
	}
}

func TestFileContextRefusesAPathOutsideTheRoots(t *testing.T) {
	c, s := newClient(t)
	holdEverything(s, nil)
	w := c.do(http.MethodGet, query("/api/v1/files/context", map[string]string{"path": "/etc"}), "", nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("outside the roots: %d %s", w.Code, w.Body.String())
	}
	if w := c.do(http.MethodGet, "/api/v1/files/context", "", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("no path: %d", w.Code)
	}
}

// The checkout on screen is asked for its count afresh rather than read from
// the repository list held for minutes.
func TestFileContextAsksTheCheckoutOnScreenForItsCount(t *testing.T) {
	c, s := newClient(t)
	if s.modules.git == nil || !s.modules.git.Available() {
		t.Skip("git is not installed")
	}
	root := s.modules.files.Roots()[0]
	repo := filepath.Join(root, "work")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	holdEverything(s, map[string]any{
		"repositories": []gitx.Repo{{Path: repo, Name: "work", Branch: "main", Changes: 0}},
	})
	hold(s, "git-summary:"+repo, &gitx.Repo{Path: repo, Name: "work", Branch: "main", Changes: 3, Dirty: true})
	got := readContext(t, c, repo)
	f := contextFact(got.Facts, fc.KindGit)
	if f == nil || f.Count == nil || *f.Count != 3 || f.State != "changed" || f.Detail != "main" {
		t.Fatalf("checkout fact = %+v, want 3 changes on main", f)
	}
}

// What each inventory turns into: the volume named with the containers using
// it, the dashboard's own containers' mirrors left out, a certificate's
// folder, an imported job's foreign paths ignored.
func TestContextThingsFromEachInventory(t *testing.T) {
	notAfter := time.Now().Add(61 * 24 * time.Hour)
	in := contextInputs{
		dataDir: "/var/lib/just-dashboard",
		containers: mapRead[[]dockerx.Container]{Have: true, Value: []dockerx.Container{
			{ID: "self", Name: "jd-backend", Labels: map[string]string{"com.docker.compose.project.working_dir": "/opt/just-dashboard"},
				Mounts: []dockerx.MountPoint{{Type: "bind", Source: "/var/lib/just-dashboard", Destination: "/var/lib/just-dashboard"}, {Type: "bind", Source: "/home", Destination: "/home"}}},
			{ID: "db1", Name: "shop-db", Image: "postgres:16", State: "running",
				Mounts: []dockerx.MountPoint{{Type: "volume", Name: "shop_pgdata", Source: "/var/lib/docker/volumes/shop_pgdata/_data", Destination: "/var/lib/postgresql/data"}}},
			{ID: "api1", Name: "api", Image: "node:22",
				Mounts: []dockerx.MountPoint{{Type: "bind", Source: "/srv/shop/uploads", Destination: "/app/uploads"}, {Type: "bind", Source: "/srv", Destination: "/srv"}}},
		}},
		volumes: mapRead[[]dockerx.Volume]{Have: true, Value: []dockerx.Volume{{Name: "shop_pgdata", Mountpoint: "/var/lib/docker/volumes/shop_pgdata/_data"}}},
		certs: mapRead[[]proxysvc.Certificate]{Have: true, Value: []proxysvc.Certificate{
			{Name: "example.com", Path: "/etc/letsencrypt/live/example.com/fullchain.pem", Domains: []string{"example.com"}, NotAfter: notAfter, Issuer: "Let's Encrypt"},
		}},
		backups: mapRead[[]*backups.Job]{Have: true, Value: []*backups.Job{
			{ID: 1, Name: "elsewhere", Sources: []string{"/srv"}, Imported: true},
			{ID: 2, Name: "local", Sources: []string{"/srv"}, TargetKind: backups.TargetLocal, Target: backups.TargetConfig{Path: "/var/backups/jd"}, Enabled: true},
		}},
	}
	things, jobs := contextThings(in)
	byKind := map[string][]fc.Thing{}
	for _, th := range things {
		byKind[th.Fact.Kind] = append(byKind[th.Fact.Kind], th)
	}
	if v := byKind[fc.KindVolume]; len(v) != 1 || strings.Join(v[0].Fact.Names, ",") != "shop_pgdata,shop-db" ||
		v[0].Fact.Image != "postgres:16" || len(v[0].Paths) != 2 {
		t.Fatalf("volume = %+v", v)
	}
	for _, b := range byKind[fc.KindBind] {
		if b.Fact.Names[0] == "jd-backend" {
			t.Fatalf("the dashboard's own mirror of the tree became a bind fact: %+v", b)
		}
		if b.Paths[0] == "/srv" && (b.Fact.Inherits || b.Notable) {
			t.Fatalf("a top-level folder bound whole made everything in it the container's: %+v", b)
		}
	}
	if len(byKind[fc.KindBind]) != 2 {
		t.Fatalf("binds = %+v", byKind[fc.KindBind])
	}
	if d := byKind[fc.KindDashboardInstall]; len(d) != 1 || d[0].Paths[0] != "/opt/just-dashboard" {
		t.Fatalf("install = %+v", d)
	}
	cert := byKind[fc.KindCertificate]
	if len(cert) != 1 || len(cert[0].Paths) != 2 || cert[0].Fact.Until == nil || cert[0].Fact.State != "ok" {
		t.Fatalf("certificate = %+v", cert)
	}
	if len(jobs) != 1 || jobs[0].Fact.Names[0] != "local" {
		t.Fatalf("jobs = %+v, want the imported one left out", jobs)
	}
	if target := byKind[fc.KindBackupTarget]; len(target) != 1 || target[0].Paths[0] != "/var/backups/jd" {
		t.Fatalf("backup target = %+v", target)
	}
}

// Every source the resolver knows is behind the capability its own page's
// read routes require; one missing from the table is shown to nobody.
func TestFileContextNamesACapabilityForEverySource(t *testing.T) {
	for _, source := range []string{fc.SourceDocker, fc.SourceProxy, fc.SourceBackups, fc.SourceGit,
		fc.SourceDeploy, fc.SourceDatabases, fc.SourceProcesses, fc.SourceDashboard} {
		if _, ok := contextSourceNeeds[source]; !ok {
			t.Errorf("%s has no capability", source)
		}
	}
}
