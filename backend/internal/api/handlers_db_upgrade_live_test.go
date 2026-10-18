package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/docker/docker/errdefs"
)

// TestLiveUpgradePostgres16To17 moves a real PostgreSQL 16 server, started
// the way Databases starts one and holding data, roles, extensions, a
// preloaded library and a parameter set with ALTER SYSTEM, to PostgreSQL 17:
// it checks it, upgrades it, reverts it, upgrades it again and finishes, and
// then puts back a server an interrupted upgrade left taking reads only.
//
// It needs Docker and the postgres:16-alpine and postgres:17-alpine images,
// and runs only with JD_TEST_UPGRADE_LIVE=1. It makes containers and volumes
// named jdt-major-upgrade-pg… (never more than two at once) and removes them.
func TestLiveUpgradePostgres16To17(t *testing.T) {
	if os.Getenv("JD_TEST_UPGRADE_LIVE") == "" {
		t.Skip("JD_TEST_UPGRADE_LIVE unset")
	}
	ctx := t.Context()
	docker := dockerx.New(envOr("JD_TEST_DOCKER_HOST", "unix:///var/run/docker.sock"))
	t.Cleanup(func() { _ = docker.Close() })
	h := &connHarness{t: t, s: testServer(t)}
	h.s.Cfg.BackupLocalDir = t.TempDir()
	h.s.modules.docker = docker
	admin := h.as(auth.RoleAdmin)

	const (
		name = "jdt-major-upgrade-pg"
		// A network of the operator's, where applications know the server by
		// an alias of its own, and a deployment environment's.
		appNetwork = "jdt-major-upgrade-app"
		envNetwork = "jdt-major-upgrade-env"
	)
	remove := func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		for _, c := range []string{name, name + "-pg16", name + "-pg17-upgrade", name + "-pg17-check", name + "-pg17-reverted"} {
			_ = docker.RemoveContainer(cleanup, c, true, true)
		}
		for _, v := range []string{name + "-data", name + "-pg17-data"} {
			_ = docker.RemoveVolume(cleanup, v, true)
		}
		for _, n := range []string{appNetwork, envNetwork} {
			_ = docker.RemoveNetwork(cleanup, n)
		}
	}
	remove()
	t.Cleanup(remove)

	port, err := freeHostPort(25433, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	password, err := generatePassword()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := docker.Create(ctx, dockerx.ContainerSpec{
		Name: name, Image: "postgres:16-alpine", Start: true, RestartPolicy: "unless-stopped",
		Env: []dockerx.EnvVar{
			{Name: "POSTGRES_USER", Value: "jd"}, {Name: "POSTGRES_PASSWORD", Value: password}, {Name: "POSTGRES_DB", Value: "app"},
		},
		Command: []string{"postgres", "-c", "shared_preload_libraries=pg_stat_statements"},
		Ports:   []dockerx.PortMapping{{HostIP: "127.0.0.1", HostPort: port, ContainerPort: 5432, Protocol: "tcp"}},
		Mounts:  []dockerx.MountSpec{{Type: "volume", Source: name + "-data", Target: "/var/lib/postgresql/data"}},
		Labels:  []dockerx.LabelSpec{{Name: "jdt-owner", Value: "major-upgrade"}},
		Limits:  dockerx.ResourceLimits{MemoryMB: 512},
	}, nil); err != nil {
		t.Fatal(err)
	}

	// Connected the way the page connects a container it finds.
	var id int64
	deadline := time.Now().Add(3 * time.Minute)
	for {
		rec := do(t, admin, http.MethodPost, "/databases/adopt", `{"container":"`+name+`"}`)
		if rec.Code == http.StatusOK || rec.Code == http.StatusCreated {
			id = readJSON[summaryView](t, rec).ID
			if readJSON[struct {
				OK bool `json:"ok"`
			}](t, do(t, admin, http.MethodGet, pathf("/databases/%d/ping", id), "")).OK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not answer: %d %s", name, rec.Code, rec.Body.String())
		}
		time.Sleep(2 * time.Second)
	}
	_, dsn, err := h.s.dbConnRow(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	open := func(database string) *sql.DB {
		t.Helper()
		db, err := dbx.OpenDatabase(ctx, dbx.DriverPostgres, dsn, database)
		if err != nil {
			t.Fatalf("open %s: %v", database, err)
		}
		t.Cleanup(func() { db.Close() })
		return db
	}
	exec := func(db *sql.DB, statements ...string) {
		t.Helper()
		for _, statement := range statements {
			if _, err := db.ExecContext(ctx, statement); err != nil {
				t.Fatalf("%s: %v", statement, err)
			}
		}
	}
	scalar := func(db *sql.DB, query string) string {
		t.Helper()
		var out string
		if err := db.QueryRowContext(ctx, query).Scan(&out); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return out
	}
	app := open("app")
	exec(app,
		`CREATE EXTENSION pg_trgm`, `CREATE EXTENSION hstore`, `CREATE EXTENSION pg_stat_statements`,
		`CREATE TABLE orders (id bigserial PRIMARY KEY, note text NOT NULL, attrs hstore, placed timestamptz NOT NULL DEFAULT now())`,
		`INSERT INTO orders (note, attrs) SELECT 'order number ' || n, hstore('n', n::text) FROM generate_series(1, 1000) n`,
		`CREATE INDEX orders_note_trgm ON orders USING gin (note gin_trgm_ops)`,
		`CREATE VIEW big_orders AS SELECT id, note FROM orders WHERE id > 900`,
		`CREATE FUNCTION order_count() RETURNS bigint LANGUAGE sql AS 'SELECT count(*) FROM orders'`,
		`CREATE ROLE reader LOGIN PASSWORD 'reader-pw-1'`,
		`GRANT SELECT ON orders TO reader`,
		`CREATE DATABASE shop`,
		`ALTER SYSTEM SET work_mem = '12MB'`,
		`SELECT pg_reload_conf()`,
	)
	exec(open("shop"),
		`CREATE TABLE items (sku text PRIMARY KEY, qty int NOT NULL)`,
		`INSERT INTO items SELECT 'sku-' || n, n FROM generate_series(1, 25) n`,
	)
	// A saved connection to the server's other database follows it too.
	h.add("jdt-major-upgrade-shop", dbx.DriverPostgres, dbx.DSNForDatabase(dbx.DriverPostgres, dsn, "shop"))

	// The server is on a network of the operator's under an alias, and linked
	// to a deployment environment, which reaches it as db-<id>.jd.internal on
	// the environment's own network.
	if _, err := docker.CreateNetwork(ctx, dockerx.NetworkSpec{Name: appNetwork, Driver: "bridge", Labels: map[string]string{"jdt-owner": "major-upgrade"}}); err != nil {
		t.Fatal(err)
	}
	if err := docker.ConnectNetwork(ctx, appNetwork, name, []string{"orders-db"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	res, err := h.s.Store.DB.Exec(`INSERT INTO deploy_projects(name, repo_path, hook_secret, hook_id, created_at) VALUES('jdt-major-upgrade', '/srv/jdt', 's', 'jdt-major-upgrade-hook', ?)`, now)
	if err != nil {
		t.Fatal(err)
	}
	project, _ := res.LastInsertId()
	res, err = h.s.Store.DB.Exec(`INSERT INTO deploy_environments(project_id, name, slug, created_at, updated_at) VALUES(?, 'Production', 'production', ?, ?)`, project, now, now)
	if err != nil {
		t.Fatal(err)
	}
	environment, _ := res.LastInsertId()
	if _, err := docker.CreateNetwork(ctx, dockerx.NetworkSpec{Name: envNetwork, Driver: "bridge", Labels: map[string]string{
		"io.just-dashboard.managed": "true", "io.just-dashboard.environment-id": strconv.FormatInt(environment, 10),
		"io.just-dashboard.database-network": "jdtmajorupgrade", "jdt-owner": "major-upgrade",
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.s.Store.DB.Exec(`INSERT INTO deploy_database_networks(environment_id, network_name, owner_key, created_at) VALUES(?, ?, 'jdtmajorupgrade', ?)`, environment, envNetwork, now); err != nil {
		t.Fatal(err)
	}
	if _, err := h.s.Store.DB.Exec(`INSERT INTO deploy_database_bindings(environment_id, connection_id, container_name) VALUES(?, ?, ?)`, environment, id, name); err != nil {
		t.Fatal(err)
	}
	if moved, err := h.s.modules.deployDatabases.rebind(ctx, id); err != nil || moved != 1 {
		t.Fatalf("linking the deployment = %d, %v", moved, err)
	}
	alias := databaseDNSName(id)
	joined := func(container, network string) []string {
		t.Helper()
		detail, err := docker.Inspect(ctx, container)
		if err != nil {
			t.Fatalf("inspect %s: %v", container, err)
		}
		for _, endpoint := range detail.NetworkList {
			if endpoint.Name == network {
				return endpoint.Aliases
			}
		}
		return nil
	}
	if !slices.Contains(joined(name, envNetwork), alias) {
		t.Fatalf("the deployment's network does not know %s: %v", alias, joined(name, envNetwork))
	}

	base := pathf("/databases/%d/upgrade", id)
	reading := func() dbUpgradeView {
		t.Helper()
		rec := do(t, admin, http.MethodGet, base, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("reading = %d %s", rec.Code, rec.Body.String())
		}
		return readJSON[dbUpgradeView](t, rec)
	}
	view := reading()
	if !view.Offered || view.Version != "16" || view.Container != name || view.Volume != name+"-data" {
		t.Fatalf("reading of a provisioned 16 = %+v", view)
	}

	report := upgradeLiveJob[upgradeReport](t, h.s, do(t, admin, http.MethodPost, base+"/check", `{"version":"17"}`))
	if !report.Ready || !strings.HasPrefix(report.Server, "16.") || !strings.HasPrefix(report.Target, "17.") {
		t.Fatalf("check = %+v", report)
	}
	checks := map[string]dbx.UpgradeCheck{}
	for _, c := range report.Checks {
		checks[c.ID] = c
	}
	if c := checks["extensions"]; c.Verdict != dbx.UpgradeOK || !strings.Contains(strings.Join(c.Items, " "), "pg_trgm") || !strings.Contains(strings.Join(c.Items, " "), "hstore") {
		t.Errorf("extensions = %+v", c)
	}
	if c := checks["libraries"]; c.Verdict != dbx.UpgradeOK {
		t.Errorf("libraries = %+v", c)
	}
	if c := checks["parameters"]; !strings.Contains(strings.Join(c.Items, " "), "work_mem") {
		t.Errorf("parameters = %+v", c)
	}
	if c := checks["disk"]; c.Verdict != dbx.UpgradeOK {
		t.Errorf("disk = %+v", c)
	}
	for _, c := range report.Checks {
		t.Logf("check %s: %s — %s %v", c.ID, c.Verdict, c.Detail, c.Items)
	}
	if _, err := docker.Inspect(ctx, name+"-pg17-check"); !errdefs.IsNotFound(err) {
		t.Errorf("the release the check started is still there: %v", err)
	}

	// A session held open across the upgrade is ended by it.
	held, err := app.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	result := upgradeLiveJob[upgradeResult](t, h.s, do(t, admin, http.MethodPost, base, `{"version":"17"}`))
	if result.Previous != name+"-pg16" || result.Volume != name+"-pg17-data" || result.Tables < 2 || result.Rows < 1025 || result.SafetyDump == "" {
		t.Errorf("upgrade result = %+v", result)
	}
	if err := held.PingContext(ctx); err == nil {
		t.Error("a session from before the upgrade still answers")
	}
	upgraded := func(major string) {
		t.Helper()
		summary := readJSON[liveSummary](t, do(t, admin, http.MethodGet, pathf("/databases/%d", id), ""))
		if !strings.HasPrefix(summary.VersionNumber, major+".") {
			t.Errorf("the connection answers as %q, want %s", summary.VersionNumber, major)
		}
		app := open("app")
		if got := scalar(app, `SELECT current_setting('server_version_num')`); !strings.HasPrefix(got, major) {
			t.Errorf("server_version_num = %s", got)
		}
	}
	upgraded("17")
	app = open("app")
	if got := scalar(app, `SELECT order_count()`); got != "1000" {
		t.Errorf("orders = %s", got)
	}
	if got := scalar(app, `SELECT attrs -> 'n' FROM orders WHERE id = 7`); got != "7" {
		t.Errorf("hstore value = %s", got)
	}
	if got := scalar(app, `SELECT count(*) FROM pg_indexes WHERE indexname = 'orders_note_trgm' AND indexdef LIKE '%gin_trgm_ops%'`); got != "1" {
		t.Errorf("the trigram index is missing: %s", got)
	}
	if got := scalar(app, `SELECT count(*) FROM big_orders`); got != "100" {
		t.Errorf("view rows = %s", got)
	}
	if got := scalar(app, `SELECT string_agg(extname, ',' ORDER BY extname) FROM pg_extension`); got != "hstore,pg_stat_statements,pg_trgm,plpgsql" {
		t.Errorf("extensions = %s", got)
	}
	if got := scalar(app, `SHOW work_mem`); got != "12MB" {
		t.Errorf("work_mem = %s, the ALTER SYSTEM setting was not carried", got)
	}
	if got := scalar(app, `SHOW shared_preload_libraries`); got != "pg_stat_statements" {
		t.Errorf("shared_preload_libraries = %s, the command line was not carried", got)
	}
	if got := scalar(app, `SELECT count(*) FROM pg_stat_statements`); got == "0" {
		t.Error("pg_stat_statements collects nothing on the new server")
	}
	reader, err := sql.Open("pgx", fmt.Sprintf("postgres://reader:reader-pw-1@127.0.0.1:%d/app?sslmode=disable", port))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if got := scalar(reader, `SELECT count(*) FROM orders`); got != "1000" {
		t.Errorf("the reader role reads %s orders", got)
	}
	if got := scalar(open("shop"), `SELECT sum(qty) FROM items`); got != "325" {
		t.Errorf("shop's items add up to %s", got)
	}
	current, err := docker.Inspect(ctx, name)
	if err != nil || current.Image != "postgres:17-alpine" {
		t.Fatalf("%s runs %v (%v)", name, current, err)
	}
	if current.Labels["jdt-owner"] != "major-upgrade" || current.Labels[upgradeRoleLabel] != "" {
		t.Errorf("the new server's labels = %v", current.Labels)
	}
	previous, err := docker.Inspect(ctx, name+"-pg16")
	if err != nil || previous.State == "running" || previous.RestartPol != "no" {
		t.Fatalf("the old server = %+v (%v)", previous, err)
	}
	// The new server answers under the old one's names, and the old one no
	// longer holds the deployment's alias.
	if !slices.Contains(joined(name, appNetwork), "orders-db") {
		t.Errorf("the operator's network lost the alias: %v", joined(name, appNetwork))
	}
	if !slices.Contains(joined(name, envNetwork), alias) {
		t.Errorf("the deployment's network does not reach the new server: %v", joined(name, envNetwork))
	}
	if aliases := joined(name+"-pg16", envNetwork); slices.Contains(aliases, alias) {
		t.Errorf("the old server still holds %s: %v", alias, aliases)
	}
	var bound, bindingStatus string
	if err := h.s.Store.DB.QueryRow(`SELECT container_id, status FROM deploy_database_bindings WHERE connection_id = ?`, id).Scan(&bound, &bindingStatus); err != nil ||
		bound != current.ID || bindingStatus != "connected" {
		t.Errorf("binding = %s %s (%v), want %s connected", bound, bindingStatus, err, current.ID)
	}
	view = reading()
	if view.Window == nil || view.Window.State != upgradeUpgraded || view.Window.PreviousState != "exited" || view.Window.SafetyDump != result.SafetyDump {
		t.Fatalf("reading after the upgrade = %+v %+v", view, view.Window)
	}
	backups := readJSON[struct {
		Files []dbBackupFile `json:"files"`
	}](t, do(t, admin, http.MethodGet, pathf("/databases/%d/backups", id), ""))
	if len(backups.Files) != 1 || backups.Files[0].File != result.SafetyDump || backups.Files[0].Origin != dbx.DumpOriginSafety {
		t.Errorf("dumps = %+v", backups.Files)
	}
	var steps string
	if err := h.s.Store.DB.QueryRow(`SELECT group_concat(step, ',') FROM (SELECT json_extract(detail, '$.step') AS step FROM audit_log WHERE action = 'database.upgrade.step' ORDER BY id)`).Scan(&steps); err != nil ||
		steps != "safety-dump,read-only,copy,parameters,verify,cutover" {
		t.Errorf("audited steps = %q (%v)", steps, err)
	}

	// What is written to 17 goes with it when the upgrade is reverted, and
	// the old server takes writes again.
	exec(app, `INSERT INTO orders (note) VALUES ('written on 17')`)
	upgradeLiveJob[map[string]any](t, h.s, do(t, admin, http.MethodPost, base+"/revert", `{"dumpFirst":true}`))
	upgraded("16")
	app = open("app")
	if got := scalar(app, `SELECT order_count()`); got != "1000" {
		t.Errorf("after the revert orders = %s", got)
	}
	exec(app, `INSERT INTO orders (note) VALUES ('written on 16 again')`)
	if exists, _ := docker.VolumeExists(ctx, name+"-pg17-data"); exists {
		t.Error("the new server's volume outlived the revert")
	}
	if _, err := docker.Inspect(ctx, name+"-pg16"); !errdefs.IsNotFound(err) {
		t.Errorf("the old server is still set aside: %v", err)
	}
	if restored, err := docker.Inspect(ctx, name); err != nil || restored.RestartPol != "unless-stopped" {
		t.Errorf("the old server's restart policy = %v (%v)", restored, err)
	}
	if !slices.Contains(joined(name, envNetwork), alias) || !slices.Contains(joined(name, appNetwork), "orders-db") {
		t.Errorf("after the revert the old server's networks = %v / %v", joined(name, envNetwork), joined(name, appNetwork))
	}
	if view = reading(); view.Window != nil || view.Last == nil || view.Last.State != upgradeReverted || !view.Offered {
		t.Errorf("reading after the revert = %+v", view)
	}

	// A dashboard that stopped in the middle of a copy: its candidate and
	// volume are removed, and the old server takes writes again.
	candidate, err := docker.Create(ctx, dockerx.ContainerSpec{
		Name: name + "-pg17-upgrade", Image: "postgres:17-alpine", RestartPolicy: "no",
		Env:    []dockerx.EnvVar{{Name: "POSTGRES_PASSWORD", Value: password}},
		Mounts: []dockerx.MountSpec{{Type: "volume", Source: name + "-pg17-data", Target: "/var/lib/postgresql/data"}},
		Labels: []dockerx.LabelSpec{{Name: upgradeRoleLabel, Value: "candidate"}, {Name: "jdt-owner", Value: "major-upgrade"}},
		Limits: dockerx.ResourceLimits{MemoryMB: 512},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	exec(app, `ALTER SYSTEM SET default_transaction_read_only = on`, `SELECT pg_reload_conf()`)
	restored, err := docker.Inspect(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.s.Store.DB.Exec(`INSERT INTO db_upgrades(connection_id, state, phase, engine, from_version, to_version, from_image, to_image,
		container, previous_id, previous_name, candidate, volume, started_at) VALUES(?, 'running', 'copy', 'postgres', '16', '17',
		'postgres:16-alpine', 'postgres:17-alpine', ?, ?, ?, ?, ?, ?)`,
		id, name, restored.ID, name+"-pg16", name+"-pg17-upgrade", name+"-pg17-data", time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	h.s.recoverDatabaseUpgrades(ctx)
	if _, err := docker.Inspect(ctx, candidate.ID); !errdefs.IsNotFound(err) {
		t.Errorf("the interrupted upgrade's candidate is still there: %v", err)
	}
	if exists, _ := docker.VolumeExists(ctx, name+"-pg17-data"); exists {
		t.Error("the interrupted upgrade's volume is still there")
	}
	app = open("app")
	exec(app, `INSERT INTO orders (note) VALUES ('written after the recovery')`)
	if view = reading(); view.Window != nil || view.Last == nil || view.Last.State != upgradeFailed || !strings.Contains(view.Last.Error, "put back") {
		t.Errorf("reading after the recovery = %+v %+v", view, view.Last)
	}

	// Again, and finished: the old server goes with its data volume.
	upgradeLiveJob[upgradeResult](t, h.s, do(t, admin, http.MethodPost, base, `{"version":"17"}`))
	upgraded("17")
	if got := scalar(open("app"), `SELECT order_count()`); got != "1002" {
		t.Errorf("after the second upgrade orders = %s", got)
	}
	rec := do(t, admin, http.MethodPost, base+"/complete", `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("finish = %d %s", rec.Code, rec.Body.String())
	}
	if _, err := docker.Inspect(ctx, name+"-pg16"); !errdefs.IsNotFound(err) {
		t.Errorf("the old server outlived the finish: %v", err)
	}
	if exists, _ := docker.VolumeExists(ctx, name+"-data"); exists {
		t.Error("the old data volume outlived the finish")
	}
	if view = reading(); view.Window != nil || view.Last == nil || view.Last.State != upgradeFinished || view.Offered {
		t.Errorf("reading after finishing = %+v", view)
	}
}

// upgradeLiveJob waits for the job a request started and reads its result
// line, failing with every line it printed when it did not succeed.
func upgradeLiveJob[T any](t *testing.T, s *Server, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if rec.Code != http.StatusAccepted {
		t.Fatalf("start = %d %s", rec.Code, rec.Body.String())
	}
	started := readJSON[jobs.Job](t, rec)
	deadline := time.Now().Add(15 * time.Minute)
	for {
		job, lines, ok := s.modules.jobs.Get(started.ID)
		if !ok {
			t.Fatalf("job %s is gone", started.ID)
		}
		if job.Status != jobs.StatusRunning {
			for _, line := range lines {
				t.Logf("%s [%s] %s", job.Kind, line.Stream, line.Text)
				if line.Stream == "result" {
					if err := json.Unmarshal([]byte(line.Text), &out); err != nil {
						t.Fatalf("result: %v", err)
					}
				}
			}
			if job.Status != jobs.StatusSucceeded {
				t.Fatalf("%s %s: %s", job.Title, job.Status, job.Error)
			}
			return out
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s is still running", job.Title)
		}
		time.Sleep(time.Second)
	}
}
