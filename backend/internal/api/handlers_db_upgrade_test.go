package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

var (
	pg16Image = &dockerx.ImageDetail{
		Env: []string{
			"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
			"PG_MAJOR=16", "PG_VERSION=16.15", "PGDATA=/var/lib/postgresql/data",
		},
		Command: []string{"postgres"}, Entrypoint: []string{"docker-entrypoint.sh"},
		Labels: map[string]string{"maintainer": "PostgreSQL Docker Maintainers"}, VolumePaths: []string{"/var/lib/postgresql/data"},
	}
	pg17Image = &dockerx.ImageDetail{
		Env: []string{
			"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
			"PG_MAJOR=17", "PG_VERSION=17.11", "PGDATA=/var/lib/postgresql/data",
		},
		Command: []string{"postgres"}, Entrypoint: []string{"docker-entrypoint.sh"},
		Labels: map[string]string{"maintainer": "PostgreSQL Docker Maintainers"}, VolumePaths: []string{"/var/lib/postgresql/data"},
	}
	pg18Image = &dockerx.ImageDetail{
		Env:     []string{"PG_MAJOR=18", "PG_VERSION=18.1", "PGDATA=/var/lib/postgresql/18/docker"},
		Command: []string{"postgres"}, Entrypoint: []string{"docker-entrypoint.sh"},
		VolumePaths: []string{"/var/lib/postgresql"},
	}
	pgData = dockerx.MountPoint{Type: "volume", Name: "jd-postgres-data", Destination: "/var/lib/postgresql/data"}
)

// provisionedSpec is a server as Databases starts one, read back the way
// Docker hands a container's configuration back: the image's own environment,
// command and labels mixed in with the operator's.
func provisionedSpec() dockerx.ContainerSpec {
	return dockerx.ContainerSpec{
		Name: "jd-postgres", Image: "postgres:16-alpine", Hostname: "0123456789ab",
		Command: []string{"postgres"}, Entrypoint: []string{"docker-entrypoint.sh"}, StopSignal: "SIGINT",
		Env: []dockerx.EnvVar{
			{Name: "POSTGRES_USER", Value: "jd"}, {Name: "POSTGRES_PASSWORD", Value: "pw"}, {Name: "POSTGRES_DB", Value: "app"},
			{Name: "PATH", Value: "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
			{Name: "PG_MAJOR", Value: "16"}, {Name: "PG_VERSION", Value: "16.15"}, {Name: "PGDATA", Value: "/var/lib/postgresql/data"},
			{Name: "TZ", Value: "Europe/Chisinau"},
		},
		Labels: []dockerx.LabelSpec{{Name: "maintainer", Value: "PostgreSQL Docker Maintainers"}, {Name: "team", Value: "shop"}},
		Mounts: []dockerx.MountSpec{
			{Type: "volume", Source: "jd-postgres-data", Target: "/var/lib/postgresql/data"},
			{Type: "bind", Source: "/srv/shop/init", Target: "/docker-entrypoint-initdb.d", ReadOnly: true},
		},
		Ports:         []dockerx.PortMapping{{HostIP: "127.0.0.1", HostPort: 5433, ContainerPort: 5432, Protocol: "tcp"}},
		Networks:      []string{"bridge", "jd-e4-db-9f2c"},
		RestartPolicy: "unless-stopped",
		Limits:        dockerx.ResourceLimits{MemoryMB: 512},
		Logging:       dockerx.CappedLogging(),
		Start:         true,
	}
}

// The new release runs with the operator's settings and none of the old
// image's: PATH, PG_MAJOR and PG_VERSION carried into a PostgreSQL 17 image
// would name the 16 binaries, and its command and labels are the new image's
// to supply. The data goes in the new volume, where the old data was.
func TestUpgradeSpecLeavesTheOldImagesDefaultsBehind(t *testing.T) {
	spec := upgradeSpec(provisionedSpec(), "0123456789abcdef", pg16Image, pg17Image, "postgres:17-alpine", pgData, "jd-postgres-pg17-data")
	if spec.Image != "postgres:17-alpine" {
		t.Errorf("image = %s", spec.Image)
	}
	env := []string{}
	for _, e := range spec.Env {
		env = append(env, e.Name+"="+e.Value)
	}
	if want := []string{"POSTGRES_USER=jd", "POSTGRES_PASSWORD=pw", "POSTGRES_DB=app", "TZ=Europe/Chisinau"}; !slices.Equal(env, want) {
		t.Errorf("env = %v, want %v", env, want)
	}
	if spec.Command != nil || spec.Entrypoint != nil || spec.Hostname != "" {
		t.Errorf("the image's command %v, entrypoint %v or the old host name %q was kept", spec.Command, spec.Entrypoint, spec.Hostname)
	}
	if len(spec.Labels) != 1 || spec.Labels[0].Name != "team" {
		t.Errorf("labels = %v", spec.Labels)
	}
	if want := []dockerx.MountSpec{
		{Type: "volume", Source: "jd-postgres-pg17-data", Target: "/var/lib/postgresql/data"},
		{Type: "bind", Source: "/srv/shop/init", Target: "/docker-entrypoint-initdb.d", ReadOnly: true},
	}; !slices.Equal(spec.Mounts, want) {
		t.Errorf("mounts = %+v", spec.Mounts)
	}
	// What the operator chose stays: the port, the restart policy, the
	// limits, the logging and how it is stopped.
	if !slices.Equal(spec.Ports, provisionedSpec().Ports) || spec.RestartPolicy != "unless-stopped" ||
		spec.Limits.MemoryMB != 512 || spec.Logging.Driver != "json-file" || spec.StopSignal != "SIGINT" {
		t.Errorf("the operator's settings were not kept: %+v", spec)
	}

	// A command the operator gave is theirs, and goes with the server.
	old := provisionedSpec()
	old.Command = []string{"postgres", "-c", "shared_preload_libraries=pg_stat_statements"}
	if spec := upgradeSpec(old, "x", pg16Image, pg17Image, "postgres:17-alpine", pgData, "v"); !slices.Equal(spec.Command, old.Command) {
		t.Errorf("command = %v", spec.Command)
	}
}

// PostgreSQL 18's image keeps its data a directory further down, under a
// volume declared a level up: the new volume is mounted where the new image
// declares it. A data directory the operator chose stays where it was.
func TestUpgradeSpecMountsTheDataWhereTheNewImageKeepsIt(t *testing.T) {
	spec := upgradeSpec(provisionedSpec(), "x", pg16Image, pg18Image, "postgres:18-alpine", pgData, "jd-postgres-pg18-data")
	if spec.Mounts[0] != (dockerx.MountSpec{Type: "volume", Source: "jd-postgres-pg18-data", Target: "/var/lib/postgresql"}) {
		t.Errorf("18's data mount = %+v", spec.Mounts[0])
	}
	old := provisionedSpec()
	old.Env = append(old.Env, dockerx.EnvVar{Name: "PGDATA", Value: "/var/lib/postgresql/data/pgdata"})
	spec = upgradeSpec(old, "x", pg16Image, pg18Image, "postgres:18-alpine", pgData, "v")
	if spec.Mounts[0].Target != "/var/lib/postgresql/data" {
		t.Errorf("a chosen data directory moved: %+v", spec.Mounts[0])
	}
}

// The upgrade's own containers say what they are for, never restart, stay
// on the default network and off the deployments' ones, and do not run the
// image's first-start scripts, whose work arrives with the copy.
func TestScratchSpecIsTheUpgradesOwn(t *testing.T) {
	next := upgradeSpec(provisionedSpec(), "x", pg16Image, pg17Image, "postgres:17-alpine", pgData, "jd-postgres-pg17-data")
	candidate := scratchSpec(next, "jd-postgres-pg17-upgrade", "candidate", 5434, 5432, "")
	if candidate.Name != "jd-postgres-pg17-upgrade" || candidate.RestartPolicy != "no" || candidate.Networks != nil ||
		!slices.Equal(candidate.Ports, []dockerx.PortMapping{{HostIP: "127.0.0.1", HostPort: 5434, ContainerPort: 5432, Protocol: "tcp"}}) {
		t.Errorf("candidate = %+v", candidate)
	}
	if !slices.Contains(candidate.Labels, dockerx.LabelSpec{Name: upgradeRoleLabel, Value: "candidate"}) {
		t.Errorf("candidate labels = %v", candidate.Labels)
	}
	if !slices.Equal(candidate.Mounts, []dockerx.MountSpec{{Type: "volume", Source: "jd-postgres-pg17-data", Target: "/var/lib/postgresql/data"}}) {
		t.Errorf("candidate mounts = %+v", candidate.Mounts)
	}
	// The labels of the spec it was made from are left as they were.
	if slices.ContainsFunc(next.Labels, func(l dockerx.LabelSpec) bool { return l.Name == upgradeRoleLabel }) {
		t.Errorf("the server's own labels gained the upgrade's: %v", next.Labels)
	}
	check := scratchSpec(next, "jd-postgres-pg17-check", "check", 0, 0, "/var/lib/postgresql/data")
	if len(check.Mounts) != 0 || check.Ports != nil {
		t.Errorf("the release asked what it carries keeps nothing and publishes nothing: %+v %+v", check.Mounts, check.Ports)
	}
}

func TestUpgradeTemplatesAndReleases(t *testing.T) {
	for image, want := range map[string]string{
		"postgres:16-alpine":                   "postgres 16",
		"docker.io/library/postgres:15-alpine": "postgres 15",
		"pgvector/pgvector:pg16":               "pgvector 16",
		"postgis/postgis:16-3.5-alpine":        "postgis 16",
		// Not an image the dashboard starts servers from.
		"postgres:16":                       "",
		"timescale/timescaledb:latest-pg16": "",
		"mysql:8.4":                         "",
	} {
		engine, _, version, ok := upgradeTemplateOf(image)
		got := ""
		if ok {
			got = engine + " " + version.id
		}
		if got != want {
			t.Errorf("upgradeTemplateOf(%s) = %q, want %q", image, got, want)
		}
	}
	for _, c := range []struct {
		a, b string
		want bool
	}{{"17", "16", true}, {"16", "17", false}, {"16", "16", false}, {"11.4", "10.11", true}, {"10.11", "11", false}, {"12", "11.4", true}} {
		if got := versionNewer(c.a, c.b); got != c.want {
			t.Errorf("versionNewer(%s, %s) = %v", c.a, c.b, got)
		}
	}
}

func TestParseDFAvailable(t *testing.T) {
	busybox := "Filesystem           1024-blocks    Used Available Capacity Mounted on\noverlay              202171096 147392604  44436232  77% /\n"
	if got, err := parseDFAvailable(busybox); err != nil || got != 44436232*1024 {
		t.Errorf("busybox df = %d %v", got, err)
	}
	coreutils := "Filesystem     1024-blocks      Used Available Capacity Mounted on\n/dev/sda1        202171096 147392604  44436232      77% /var/lib/postgresql/data\n"
	if got, err := parseDFAvailable(coreutils); err != nil || got != 44436232*1024 {
		t.Errorf("coreutils df = %d %v", got, err)
	}
	if _, err := parseDFAvailable("df: /nope: No such file or directory\n"); err == nil {
		t.Error("an error read as a filesystem")
	}
}

// upgradeEngineFake is the Docker the upgrade routes read, as far as they
// read it outside a job: the containers, the volumes, and the removals.
type upgradeEngineFake struct {
	mu         sync.Mutex
	containers []*upgradeFakeContainer
	volumes    map[string]bool
	removed    []string
}

type upgradeFakeContainer struct {
	id, name, image, state string
	hostPort               int
	labels                 map[string]string
	env                    []string
	volume                 string
}

func (f *upgradeEngineFake) find(ref string) *upgradeFakeContainer {
	for _, c := range f.containers {
		if c.id == ref || c.name == ref {
			return c
		}
	}
	return nil
}

func (f *upgradeEngineFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := dockerAPIVersion.ReplaceAllString(r.URL.Path, "")
	w.Header().Set("Content-Type", "application/json")
	notFound := func() {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"No such object"}`))
	}
	ports := func(c *upgradeFakeContainer) map[string]any {
		return map[string]any{"5432/tcp": []map[string]string{{"HostIp": "127.0.0.1", "HostPort": strconv.Itoa(c.hostPort)}}}
	}
	switch {
	case path == "/_ping":
		w.Header().Set("API-Version", "1.47")
	case path == "/containers/json":
		out := []map[string]any{}
		for _, c := range f.containers {
			if c.state != "running" {
				continue
			}
			out = append(out, map[string]any{
				"Id": c.id, "Names": []string{"/" + c.name}, "Image": c.image, "State": c.state, "Labels": c.labels,
				"Ports": []map[string]any{{"IP": "127.0.0.1", "PrivatePort": 5432, "PublicPort": c.hostPort, "Type": "tcp"}},
			})
		}
		_ = json.NewEncoder(w).Encode(out)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/containers/") && strings.HasSuffix(path, "/json"):
		c := f.find(strings.TrimSuffix(strings.TrimPrefix(path, "/containers/"), "/json"))
		if c == nil {
			notFound()
			return
		}
		mounts := []map[string]any{}
		if c.volume != "" {
			mounts = append(mounts, map[string]any{"Type": "volume", "Name": c.volume, "Destination": "/var/lib/postgresql/data", "RW": true})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Id": c.id, "Name": "/" + c.name, "Image": "sha256:" + c.id,
			"Config":          map[string]any{"Image": c.image, "Labels": c.labels, "Env": c.env},
			"State":           map[string]any{"Status": c.state, "Running": c.state == "running"},
			"HostConfig":      map[string]any{"NetworkMode": "bridge", "PortBindings": ports(c), "RestartPolicy": map[string]any{"Name": "unless-stopped"}},
			"NetworkSettings": map[string]any{"Ports": ports(c)},
			"Mounts":          mounts,
		})
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/containers/"):
		c := f.find(strings.TrimPrefix(path, "/containers/"))
		if c == nil {
			notFound()
			return
		}
		f.removed = append(f.removed, "container "+c.name)
		f.containers = slices.DeleteFunc(f.containers, func(have *upgradeFakeContainer) bool { return have == c })
		w.WriteHeader(http.StatusNoContent)
	case strings.HasPrefix(path, "/volumes/"):
		name := strings.TrimPrefix(path, "/volumes/")
		if !f.volumes[name] {
			notFound()
			return
		}
		if r.Method == http.MethodDelete {
			f.removed = append(f.removed, "volume "+name)
			delete(f.volumes, name)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"Name": name})
	default:
		notFound()
	}
}

func (f *upgradeEngineFake) didRemove() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.removed)
}

// upgradeHarness is a provisioned PostgreSQL 16 on port 5433, saved as a
// connection, with a Docker of the test's own.
func upgradeHarness(t *testing.T) (*connHarness, *upgradeEngineFake, int64) {
	t.Helper()
	h := newConnHarness(t)
	engine := &upgradeEngineFake{volumes: map[string]bool{"jd-postgres-data": true}}
	engine.containers = []*upgradeFakeContainer{{
		id: "pg16id", name: "jd-postgres", image: "postgres:16-alpine", state: "running", hostPort: 5433,
		labels: map[string]string{}, volume: "jd-postgres-data",
		env: []string{"POSTGRES_USER=jd", "POSTGRES_PASSWORD=pw", "POSTGRES_DB=app", "PGDATA=/var/lib/postgresql/data"},
	}}
	srv := httptest.NewServer(engine)
	t.Cleanup(srv.Close)
	h.s.modules.docker = dockerx.New(srv.URL)
	t.Cleanup(func() { _ = h.s.modules.docker.Close() })
	id := h.add("shop", dbx.DriverPostgres, "postgres://jd:pw@127.0.0.1:5433/app?sslmode=disable")
	return h, engine, id
}

// openUpgradeRecord is an upgrade from 16 to 17 that finished its cutover,
// with the old server kept as jd-postgres-pg16.
func openUpgradeRecord(t *testing.T, h *connHarness, id int64) int64 {
	t.Helper()
	res, err := h.s.Store.DB.Exec(`INSERT INTO db_upgrades(connection_id, state, engine, from_version, to_version, from_image, to_image,
		container, previous_id, previous_name, previous_restart, previous_volume, volume, safety_dump, started_by, started_at, upgraded_at)
		VALUES(?, 'upgraded', 'postgres', '16', '17', 'postgres:16-alpine', 'postgres:17-alpine', 'jd-postgres', 'pg16id',
		'jd-postgres-pg16', 'unless-stopped:0', 'jd-postgres-data', 'jd-postgres-pg17-data', 'app-safety.dump', 'tester', ?, ?)`,
		id, time.Now().Add(-time.Hour).Unix(), time.Now().Add(-50*time.Minute).Unix())
	if err != nil {
		t.Fatal(err)
	}
	record, _ := res.LastInsertId()
	return record
}

// The reading says what the server is, what it can be moved to and where its
// data is, to an administrator and to nobody else.
func TestUpgradeReadSaysWhatTheServerCanBeUpgradedTo(t *testing.T) {
	h, _, id := upgradeHarness(t)
	path := pathf("/databases/%d/upgrade", id)
	got := readJSON[dbUpgradeView](t, do(t, h.as(auth.RoleAdmin), http.MethodGet, path, ""))
	if !got.Offered || got.Engine != "postgres" || got.Version != "16" || got.Container != "jd-postgres" ||
		got.Volume != "jd-postgres-data" || got.Window != nil || got.Reason != "" {
		t.Fatalf("reading = %+v", got)
	}
	if len(got.Targets) == 0 || got.Targets[len(got.Targets)-1].Version != "17" {
		t.Errorf("targets = %+v, want 17 among them", got.Targets)
	}
	for _, v := range got.Targets {
		if !versionNewer(v.Version, "16") {
			t.Errorf("%s is offered as an upgrade from 16", v.Version)
		}
	}
	for _, role := range []auth.Role{auth.RoleLimited, auth.RoleReadOnly} {
		if rec := do(t, h.as(role), http.MethodGet, path, ""); rec.Code != http.StatusForbidden {
			t.Errorf("%s reading the upgrade = %d", role, rec.Code)
		}
	}
}

// A server this does not upgrade is said to be one, with the reason, and the
// requests that would act on it are refused with the same sentence.
func TestUpgradeIsRefusedForWhatItCannotUpgrade(t *testing.T) {
	h, engine, id := upgradeHarness(t)
	admin := h.as(auth.RoleAdmin)
	path := pathf("/databases/%d/upgrade", id)
	for _, c := range []struct {
		image  string
		labels map[string]string
		want   string
	}{
		{"postgres:16", nil, "not one of the images Databases starts servers from"},
		{"timescale/timescaledb:latest-pg16", nil, "not one of the images"},
		{"postgres:16-alpine", map[string]string{"com.docker.compose.project": "shop"}, "compose project shop"},
	} {
		engine.mu.Lock()
		engine.containers[0].image, engine.containers[0].labels = c.image, c.labels
		engine.mu.Unlock()
		got := readJSON[dbUpgradeView](t, do(t, admin, http.MethodGet, path, ""))
		if got.Offered || !strings.Contains(got.Reason, c.want) {
			t.Errorf("%s %v: %+v, want a reason saying %q", c.image, c.labels, got, c.want)
		}
		rec := do(t, admin, http.MethodPost, path, `{"version":"17"}`)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "upgrade_unavailable") {
			t.Errorf("upgrading %s = %d %s", c.image, rec.Code, rec.Body.String())
		}
	}
	engine.mu.Lock()
	engine.containers[0].state = "exited"
	engine.mu.Unlock()
	if got := readJSON[dbUpgradeView](t, do(t, admin, http.MethodGet, path, "")); !strings.Contains(got.Reason, "Start the server first") {
		t.Errorf("a stopped server: %+v", got)
	}
	file := h.add("notes", dbx.DriverSQLite, filepath.Join(h.s.Cfg.FileRoots[0], "notes.db"))
	if got := readJSON[dbUpgradeView](t, do(t, admin, http.MethodGet, pathf("/databases/%d/upgrade", file), "")); got.Offered ||
		!strings.Contains(got.Reason, "Only a PostgreSQL server") {
		t.Errorf("a file: %+v", got)
	}
}

// Every route is an administrator's, and the three that change the server
// are destructive. A release that is not one of the newer ones is refused
// before a job exists.
func TestUpgradeRoutesAskForTheirCapability(t *testing.T) {
	h, _, id := upgradeHarness(t)
	base := pathf("/databases/%d/upgrade", id)
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, base, ""},
		{http.MethodPost, base + "/check", `{"version":"17"}`},
		{http.MethodPost, base, `{"version":"17"}`},
		{http.MethodPost, base + "/revert", `{}`},
		{http.MethodPost, base + "/complete", `{}`},
	} {
		for _, role := range []auth.Role{auth.RoleLimited, auth.RoleReadOnly} {
			if rec := do(t, h.as(role), c.method, c.path, c.body); rec.Code != http.StatusForbidden {
				t.Errorf("%s %s %s = %d, want 403", role, c.method, c.path, rec.Code)
			}
		}
	}
	admin := h.as(auth.RoleAdmin)
	for _, path := range []string{base, base + "/check"} {
		rec := do(t, admin, http.MethodPost, path, `{"version":"15"}`)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "PostgreSQL 16 is upgraded to") {
			t.Errorf("POST %s to 15 = %d %s", path, rec.Code, rec.Body.String())
		}
	}
	for _, path := range []string{base + "/revert", base + "/complete"} {
		rec := do(t, admin, http.MethodPost, path, `{}`)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "no_upgrade") {
			t.Errorf("POST %s with nothing to finish = %d %s", path, rec.Code, rec.Body.String())
		}
	}
}

// The three that change the server spend the destructive budget, as every
// route in s.destructive does; reading and checking do not.
func TestUpgradeRoutesThatChangeTheServerAreDestructive(t *testing.T) {
	for _, c := range []struct {
		path, body  string
		destructive bool
	}{
		{"", `{"version":"15"}`, true},
		{"/revert", `{}`, true},
		{"/complete", `{}`, true},
		{"/check", `{"version":"15"}`, false},
	} {
		h, _, id := upgradeHarness(t)
		h.s.destrLim = httpx.NewLimiter(1, 1)
		admin := h.as(auth.RoleAdmin)
		limited := false
		for range 3 {
			rec := do(t, admin, http.MethodPost, pathf("/databases/%d/upgrade", id)+c.path, c.body)
			limited = limited || rec.Code == http.StatusTooManyRequests
		}
		if limited != c.destructive {
			t.Errorf("POST /upgrade%s spends the destructive budget = %v, want %v", c.path, limited, c.destructive)
		}
	}
}

// A protected connection's server is not replaced: the upgrade and its
// revert are refused, through the connection and through a neighbour of it.
// The check reads, and finishing removes what the connection no longer
// reaches, so both are let through.
func TestUpgradeOfAProtectedServerIsRefused(t *testing.T) {
	h, _, id := upgradeHarness(t)
	admin := h.as(auth.RoleAdmin)
	base := pathf("/databases/%d/upgrade", id)
	if _, err := h.s.Store.DB.Exec(`UPDATE db_connections SET read_only = 1 WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{base, base + "/revert"} {
		rec := do(t, admin, http.MethodPost, path, `{"version":"17"}`)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "connection_read_only") {
			t.Errorf("POST %s on a protected connection = %d %s", path, rec.Code, rec.Body.String())
		}
	}
	for _, path := range []string{base + "/check", base + "/complete"} {
		if rec := do(t, admin, http.MethodPost, path, `{"version":"15"}`); strings.Contains(rec.Body.String(), "connection_read_only") {
			t.Errorf("POST %s on a protected connection was refused: %s", path, rec.Body.String())
		}
	}
	// Another connection to the same server is not a way round it.
	other := h.add("shop-admin", dbx.DriverPostgres, "postgres://jd:pw@localhost:5433/postgres?sslmode=disable")
	rec := do(t, admin, http.MethodPost, pathf("/databases/%d/upgrade", other), `{"version":"17"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "shop is protected") {
		t.Errorf("upgrading through a neighbour = %d %s", rec.Code, rec.Body.String())
	}
}

// While the old server is kept, the reading says so and where it is, a
// second upgrade waits for it, and finishing removes it with its data volume
// and records that — through the whole stack, so the audit entry is the one
// the middleware writes.
func TestFinishingAnUpgradeRemovesTheOldServer(t *testing.T) {
	h, engine, id := upgradeHarness(t)
	engine.mu.Lock()
	engine.containers[0].image = "postgres:17-alpine"
	engine.containers[0].volume = "jd-postgres-pg17-data"
	engine.containers = append(engine.containers, &upgradeFakeContainer{
		id: "pg16id", name: "jd-postgres-pg16", image: "postgres:16-alpine", state: "exited", hostPort: 5433,
		volume: "jd-postgres-data",
	})
	engine.containers[0].id = "pg17id"
	engine.volumes["jd-postgres-pg17-data"] = true
	engine.mu.Unlock()
	record := openUpgradeRecord(t, h, id)
	admin := h.as(auth.RoleAdmin)
	base := pathf("/databases/%d/upgrade", id)

	got := readJSON[dbUpgradeView](t, do(t, admin, http.MethodGet, base, ""))
	if got.Offered || got.Window == nil || got.Window.Previous != "jd-postgres-pg16" || got.Window.PreviousState != "exited" ||
		got.Window.SafetyDump != "app-safety.dump" || !strings.Contains(got.Reason, "Finish or revert") {
		t.Fatalf("reading with the old server kept = %+v %+v", got, got.Window)
	}
	if rec := do(t, admin, http.MethodPost, base, `{"version":"18"}`); rec.Code != http.StatusConflict {
		// 18 is the newest release offered; the window refuses it while the
		// old server is kept, and nothing starts.
		t.Errorf("a second upgrade while the first is open = %d %s", rec.Code, rec.Body.String())
	}

	// Started by hand, it is not removed from under whoever started it.
	engine.mu.Lock()
	engine.containers[1].state = "running"
	engine.mu.Unlock()
	if rec := do(t, admin, http.MethodPost, base+"/complete", `{}`); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "previous_running") {
		t.Errorf("finishing with the old server running = %d %s", rec.Code, rec.Body.String())
	}
	engine.mu.Lock()
	engine.containers[1].state = "exited"
	engine.mu.Unlock()

	c := &client{t: t, h: h.s.Routes(), cookie: signIn(t, h.s)}
	rec := c.do(http.MethodPost, "/api/v1"+base+"/complete", `{}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("finish = %d %s", rec.Code, rec.Body.String())
	}
	if removed := engine.didRemove(); !slices.Equal(removed, []string{"container jd-postgres-pg16", "volume jd-postgres-data"}) {
		t.Errorf("removed %v", removed)
	}
	var state string
	if err := h.s.Store.DB.QueryRow(`SELECT state FROM db_upgrades WHERE id = ?`, record).Scan(&state); err != nil || state != upgradeFinished {
		t.Errorf("record = %q %v", state, err)
	}
	var detail string
	if err := h.s.Store.DB.QueryRow(`SELECT detail FROM audit_log WHERE action = 'database.upgrade.complete'`).Scan(&detail); err != nil ||
		!strings.Contains(detail, "jd-postgres-pg16") || !strings.Contains(detail, "jd-postgres-data") {
		t.Errorf("audit = %q %v", detail, err)
	}
	got = readJSON[dbUpgradeView](t, do(t, admin, http.MethodGet, base, ""))
	if got.Window != nil || got.Last == nil || got.Last.State != upgradeFinished {
		t.Errorf("after finishing: window %+v, last %+v", got.Window, got.Last)
	}
}
