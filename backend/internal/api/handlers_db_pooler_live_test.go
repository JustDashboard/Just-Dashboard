package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// TestLivePooler runs the whole pooler against a PostgreSQL container and a
// PgBouncer container of its own: turning it on in both authentication modes,
// signing in through it, its statistics, settings, power, a changed password,
// a deployment reaching it by name, and its removal.
//
//	JD_POOLER_LIVE=1 go test ./internal/api -run TestLivePooler -count=1 -v
func TestLivePooler(t *testing.T) {
	if os.Getenv("JD_POOLER_LIVE") != "1" {
		t.Skip("set JD_POOLER_LIVE=1 on a Docker host")
	}
	s, router := dbTestRouter(t)
	s.modules.docker = dockerx.New("unix:///var/run/docker.sock")
	_, environmentID, _ := insertDeploymentConfigurationAPI(t, s)

	name := fmt.Sprintf("jdt-pgbouncer-pg-%d", time.Now().Unix()%100000)
	probe := name + "-probe"
	ctx := context.Background()
	var conn dbConnection
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = s.modules.deployDatabases.RemoveRuntimeNetworks(cleanup, environmentID)
		for _, c := range []string{probe, name + "-pgbouncer", name} {
			_ = s.modules.docker.RemoveContainer(cleanup, c, true, true)
		}
		_ = s.modules.docker.RemoveNetwork(cleanup, poolerNetworkName(conn.ID))
		_ = s.modules.docker.RemoveVolume(cleanup, name+"-data", true)
	})

	port, err := freeHostPort(55900, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.modules.docker.Create(ctx, dockerx.ContainerSpec{
		Name: name, Image: "postgres:17-alpine", Start: true, Pull: "missing",
		Env:    []dockerx.EnvVar{{Name: "POSTGRES_USER", Value: "app"}, {Name: "POSTGRES_PASSWORD", Value: "first-pw"}, {Name: "POSTGRES_DB", Value: "app"}},
		Ports:  []dockerx.PortMapping{{HostIP: "127.0.0.1", HostPort: port, ContainerPort: 5432, Protocol: "tcp"}},
		Mounts: []dockerx.MountSpec{{Type: "volume", Source: name + "-data", Target: "/var/lib/postgresql/data"}},
		Labels: []dockerx.LabelSpec{{Name: "jdt-owner", Value: "pgbouncer"}},
		Limits: dockerx.ResourceLimits{MemoryMB: 512},
	}, nil); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Minute)
	for {
		adopted := do(t, router, http.MethodPost, "/databases/adopt", fmt.Sprintf(`{"container":%q}`, name))
		if adopted.Code >= 200 && adopted.Code < 300 {
			if err := json.Unmarshal(adopted.Body.Bytes(), &conn); err != nil {
				t.Fatal(err)
			}
			var health struct {
				OK bool `json:"ok"`
			}
			_ = json.Unmarshal(do(t, router, http.MethodGet, pathf("/databases/%d/ping", conn.ID), "").Body.Bytes(), &health)
			if health.OK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("PostgreSQL did not accept connections (adopt HTTP %d)", adopted.Code)
		}
		time.Sleep(time.Second)
	}
	base := pathf("/databases/%d", conn.ID)

	put := func(settings string) (int, poolerView) {
		t.Helper()
		rec := do(t, router, http.MethodPut, base+"/pooler", settings)
		var out struct {
			Pooler poolerView `json:"pooler"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if rec.Code >= 300 {
			t.Logf("PUT /pooler: HTTP %d %s", rec.Code, rec.Body)
		}
		return rec.Code, out.Pooler
	}
	pooledURL := func(target string) string {
		t.Helper()
		rec := do(t, router, http.MethodGet, base+"/url?pooled=1&target="+target, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("pooled %s URL: HTTP %d %s", target, rec.Code, rec.Body)
		}
		var out struct {
			URL       string `json:"url"`
			Reference string `json:"reference"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if target == "container" && out.Reference != fmt.Sprintf("${{database.%d.pooled}}", conn.ID) {
			t.Errorf("reference = %q", out.Reference)
		}
		return out.URL
	}
	whoAmI := func(dsn string) (string, error) {
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			return "", err
		}
		defer db.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var who string
		err = db.QueryRowContext(ctx, `SELECT current_user`).Scan(&who)
		return who, err
	}
	waitFor := func(t *testing.T, what string, ok func() bool) {
		t.Helper()
		for end := time.Now().Add(30 * time.Second); time.Now().Before(end); time.Sleep(300 * time.Millisecond) {
			if ok() {
				return
			}
		}
		t.Fatalf("timed out waiting for %s", what)
	}

	// Nothing to begin with, and the engine is offered one.
	var before poolerView
	_ = json.Unmarshal(do(t, router, http.MethodGet, base+"/pooler", "").Body.Bytes(), &before)
	if before.Enabled || !before.Available {
		t.Fatalf("a database with no pooler reads as %+v", before)
	}

	t.Run("userlist mode signs in through the pooler", func(t *testing.T) {
		code, view := put(`{"poolMode":"transaction","poolSize":7,"maxClientConn":50,"authMode":"userlist"}`)
		if code != http.StatusCreated || !view.Enabled || view.State != "running" || view.Exposure != exposureLocal || !view.InSync {
			t.Fatalf("enable: HTTP %d %+v", code, view)
		}
		if view.Container != name+"-pgbouncer" || view.Port == 0 {
			t.Fatalf("container = %q port = %d", view.Container, view.Port)
		}
		direct := do(t, router, http.MethodGet, base+"/url", "")
		if direct.Code != http.StatusOK {
			t.Fatal(direct.Body)
		}
		host := pooledURL("host")
		if !strings.Contains(host, fmt.Sprintf("127.0.0.1:%d", view.Port)) {
			t.Fatalf("pooled host URL = %s", host)
		}
		waitFor(t, "a sign-in through the pooler", func() bool { who, err := whoAmI(host); return err == nil && who == "app" })
		if got := pooledURL("container"); !strings.Contains(got, fmt.Sprintf("@db-%d-pool.jd.internal:6432/", conn.ID)) {
			t.Errorf("pooled container URL = %s", got)
		}
		if rec := do(t, router, http.MethodGet, base+"/url?pooled=1&target=public", ""); rec.Code != http.StatusBadRequest {
			t.Errorf("a pooler on loopback was handed out for anywhere: HTTP %d", rec.Code)
		}
	})

	t.Run("a password the pooler does not have is refused", func(t *testing.T) {
		u, _ := url.Parse(pooledURL("host"))
		u.User = url.UserPassword("app", "not-the-password")
		if who, err := whoAmI(u.String()); err == nil {
			t.Fatalf("signed in through the pooler as %s with a wrong password", who)
		}
	})

	t.Run("statistics come from the console", func(t *testing.T) {
		if _, err := whoAmI(pooledURL("host")); err != nil {
			t.Fatal(err)
		}
		rec := do(t, router, http.MethodGet, base+"/pooler/stats", "")
		var stats poolerStatsView
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &stats) != nil {
			t.Fatalf("stats: HTTP %d %s", rec.Code, rec.Body)
		}
		if !strings.HasPrefix(stats.Version, "1.25") {
			t.Errorf("version = %q", stats.Version)
		}
		found := false
		for _, st := range stats.Stats {
			found = found || (st.Database == "app" && st.TotalQueryCnt > 0)
		}
		if !found {
			t.Errorf("no statistics for app: %+v", stats.Stats)
		}
		for _, p := range stats.Pools {
			if p.Database == "pgbouncer" {
				t.Errorf("the console's own pseudo-database is listed: %+v", p)
			}
		}
	})

	t.Run("a changed setting is a reload that keeps the container", func(t *testing.T) {
		old, _ := s.poolerRecord(ctx, conn.ID)
		code, view := put(`{"poolMode":"session","poolSize":5,"maxClientConn":60,"authMode":"userlist"}`)
		if code != http.StatusOK || view.Settings.PoolMode != "session" || view.Settings.PoolSize != 5 {
			t.Fatalf("update: HTTP %d %+v", code, view)
		}
		now, _ := s.poolerRecord(ctx, conn.ID)
		if now.ContainerID != old.ContainerID {
			t.Fatal("changing a setting replaced the container, which drops every client")
		}
		detail, _ := s.modules.docker.Inspect(ctx, now.Container)
		spec, _ := s.modules.docker.SpecOf(ctx, detail.ID)
		_, hostPort := poolerBinding(spec)
		admin, err := sql.Open("pgx", dbx.PoolerAdminDSN("127.0.0.1", hostPort, now.Secrets.Stats))
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close()
		waitFor(t, "the reload", func() bool {
			rows, err := admin.Query(`SHOW CONFIG`)
			if err != nil {
				return false
			}
			defer rows.Close()
			cols, _ := rows.Columns()
			for rows.Next() {
				vals := make([]sql.NullString, len(cols))
				ptrs := make([]any, len(cols))
				for i := range vals {
					ptrs[i] = &vals[i]
				}
				_ = rows.Scan(ptrs...)
				if vals[0].String == "default_pool_size" && vals[1].String == "5" {
					return true
				}
			}
			return false
		})
	})

	t.Run("a changed password is written to the userlist by the keeper", func(t *testing.T) {
		pool, c, dsn, err := func() (*sql.DB, *dbConnection, string, error) {
			c, dsn, err := s.dbConnRow(ctx, conn.ID)
			if err != nil {
				return nil, nil, "", err
			}
			db, err := sql.Open("pgx", dsn)
			return db, c, dsn, err
		}()
		if err != nil {
			t.Fatal(err)
		}
		defer pool.Close()
		if _, err := pool.Exec(`ALTER ROLE app PASSWORD 'second-pw'`); err != nil {
			t.Fatal(err)
		}
		sealed, err := s.Sealer.Seal(strings.Replace(dsn, "first-pw", "second-pw", 1))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Store.DB.Exec(`UPDATE db_connections SET dsn_enc = ? WHERE id = ?`, sealed, c.ID); err != nil {
			t.Fatal(err)
		}
		view, _ := s.describePooler(ctx, c, strings.Replace(dsn, "first-pw", "second-pw", 1))
		if view.InSync {
			t.Fatal("a changed password is reported as in sync")
		}
		if err := s.syncPooler(ctx, conn.ID); err != nil {
			t.Fatal(err)
		}
		host := pooledURL("host")
		waitFor(t, "the new password through the pooler", func() bool { who, err := whoAmI(host); return err == nil && who == "app" })
		u, _ := url.Parse(host)
		u.User = url.UserPassword("app", "first-pw")
		if _, err := whoAmI(u.String()); err == nil {
			t.Fatal("the old password still signs in through the pooler")
		}
	})

	t.Run("auth_query lets another account in and leaves nothing behind when switched off", func(t *testing.T) {
		direct, err := sql.Open("pgx", func() string { _, dsn, _ := s.dbConnRow(ctx, conn.ID); return dsn }())
		if err != nil {
			t.Fatal(err)
		}
		defer direct.Close()
		if _, err := direct.Exec(`CREATE ROLE jdt_pgbouncer_other LOGIN PASSWORD 'other-pw'`); err != nil {
			t.Fatal(err)
		}
		defer direct.Exec(`DROP ROLE IF EXISTS jdt_pgbouncer_other`)
		other, _ := url.Parse(pooledURL("host"))
		other.User = url.UserPassword("jdt_pgbouncer_other", "other-pw")
		if _, err := whoAmI(other.String()); err == nil {
			t.Fatal("an account outside the userlist signed in")
		}
		code, view := put(`{"poolMode":"transaction","poolSize":5,"maxClientConn":60,"authMode":"auth_query"}`)
		if code != http.StatusOK || view.Settings.AuthMode != "auth_query" {
			t.Fatalf("switch: HTTP %d %+v", code, view)
		}
		waitFor(t, "the other account through auth_query", func() bool {
			who, err := whoAmI(other.String())
			return err == nil && who == "jdt_pgbouncer_other"
		})
		own := pooledURL("host")
		waitFor(t, "the connection's own account through auth_query", func() bool { who, err := whoAmI(own); return err == nil && who == "app" })
		code, _ = put(`{"poolMode":"transaction","poolSize":5,"maxClientConn":60,"authMode":"userlist"}`)
		if code != http.StatusOK {
			t.Fatalf("switch back: HTTP %d", code)
		}
		var left int
		if err := direct.QueryRow(`SELECT (SELECT count(*) FROM pg_roles WHERE rolname = 'jd_pgbouncer') + (SELECT count(*) FROM pg_namespace WHERE nspname = 'jd_pgbouncer')`).Scan(&left); err != nil || left != 0 {
			t.Fatalf("auth_query left %d objects in the database (%v)", left, err)
		}
	})

	t.Run("the pooler follows the database's power", func(t *testing.T) {
		rec, _ := s.poolerRecord(ctx, conn.ID)
		state := func() string {
			d, err := s.modules.docker.Inspect(ctx, rec.Container)
			if err != nil {
				return err.Error()
			}
			return d.State
		}
		if r := do(t, router, http.MethodPost, base+"/power", `{"action":"stop"}`); r.Code != http.StatusOK {
			t.Fatalf("stop: HTTP %d %s", r.Code, r.Body)
		}
		if got := state(); got != "exited" {
			t.Fatalf("pooler is %s after its database stopped", got)
		}
		if r := do(t, router, http.MethodGet, base+"/pooler/stats", ""); r.Code != http.StatusConflict {
			t.Errorf("stats of a stopped pooler: HTTP %d", r.Code)
		}
		if r := do(t, router, http.MethodPost, base+"/power", `{"action":"start"}`); r.Code != http.StatusOK {
			t.Fatalf("start: HTTP %d %s", r.Code, r.Body)
		}
		if got := state(); got != "running" {
			t.Fatalf("pooler is %s after its database started", got)
		}
		host := pooledURL("host")
		waitFor(t, "a sign-in after the restart", func() bool { who, err := whoAmI(host); return err == nil && who == "app" })
	})

	t.Run("the pooler follows a change of the database's binding", func(t *testing.T) {
		conn2, dsn, _ := s.dbConnRow(ctx, conn.ID)
		target, err := s.poolerTarget(ctx, conn2, dsn)
		if err != nil {
			t.Fatal(err)
		}
		rec, _ := s.poolerRecord(ctx, conn.ID)
		oldID := rec.ContainerID
		target.bindIP = "127.0.0.2"
		change, err := s.ensurePooler(ctx, nil, target, rec, dsn, false)
		if err != nil || !change.Recreated {
			t.Fatalf("a changed binding: %+v %v", change, err)
		}
		after, _ := s.poolerRecord(ctx, conn.ID)
		spec, _ := s.modules.docker.SpecOf(ctx, after.ContainerID)
		if ip, _ := poolerBinding(spec); ip != "127.0.0.2" || after.ContainerID == oldID {
			t.Fatalf("pooler bound to %s, container replaced = %v", ip, after.ContainerID != oldID)
		}
		target.bindIP = "127.0.0.1"
		if _, err := s.ensurePooler(ctx, nil, target, after, dsn, false); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("a deployment reaches it by name on its environment's network", func(t *testing.T) {
		application := pooledURL("container")
		networks, err := s.modules.deployDatabases.NetworksForRuntime(ctx, environmentID, deploy.RuntimePlanConfig{}, map[string]string{"DATABASE_URL": application})
		if err != nil || len(networks) != 1 {
			t.Fatalf("network: %v %v", networks, err)
		}
		resolved, err := s.modules.deployDatabases.ResolveVariable(ctx, environmentID, 1, fmt.Sprintf("%d.pooled", conn.ID))
		if err != nil || resolved != application {
			t.Fatalf("pooled reference resolved to %q (%v), want %q", resolved, err, application)
		}
		info, _ := dbx.ParseDSN(dbx.DriverPostgres, application)
		if _, err := s.modules.docker.Create(ctx, dockerx.ContainerSpec{
			Name: probe, Image: "postgres:17-alpine", Start: true, Networks: networks,
			Env:     []dockerx.EnvVar{{Name: "PGPASSWORD", Value: info.Password}},
			Command: []string{"psql", "-h", info.Host, "-p", info.Port, "-U", info.User, "-d", info.Database, "-c", "SELECT 1"},
			Labels:  []dockerx.LabelSpec{{Name: "jdt-owner", Value: "pgbouncer"}},
		}, nil); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "the probe to finish", func() bool {
			d, err := s.modules.docker.Inspect(ctx, probe)
			return err == nil && d.State == "exited"
		})
		if d, _ := s.modules.docker.Inspect(ctx, probe); d.ExitCode != 0 {
			logs, _ := s.modules.docker.CompletedCommandOutput(ctx, probe)
			t.Fatalf("psql through %s exited %d: %s", info.Host, d.ExitCode, logs)
		}
		if err := s.modules.deployDatabases.RemoveRuntimeNetworks(ctx, environmentID); err != nil {
			t.Fatalf("the environment's network could not be removed with the pooler on it: %v", err)
		}
	})

	t.Run("turning it off removes the container and the network and keeps the database", func(t *testing.T) {
		rec, _ := s.poolerRecord(ctx, conn.ID)
		if r := do(t, router, http.MethodDelete, base+"/pooler", ""); r.Code != http.StatusOK {
			t.Fatalf("disable: HTTP %d %s", r.Code, r.Body)
		}
		if _, err := s.modules.docker.Inspect(ctx, rec.Container); err == nil {
			t.Error("the pooler container is still there")
		}
		if _, err := s.modules.docker.InspectNetwork(ctx, rec.Network); err == nil {
			t.Error("the pooler network is still there")
		}
		if left, _ := s.poolerRecord(ctx, conn.ID); left != nil {
			t.Error("the record is still there")
		}
		if r := do(t, router, http.MethodGet, base+"/ping", ""); !strings.Contains(r.Body.String(), `"ok":true`) {
			t.Errorf("the database stopped answering: %s", r.Body)
		}
		if r := do(t, router, http.MethodGet, base+"/url?pooled=1", ""); r.Code != http.StatusConflict {
			t.Errorf("a pooled string without a pooler: HTTP %d", r.Code)
		}
	})

	t.Run("forgetting the connection takes the pooler with it", func(t *testing.T) {
		if code, _ := put(`{"poolMode":"transaction","poolSize":5,"maxClientConn":60,"authMode":"userlist"}`); code != http.StatusCreated {
			t.Fatalf("enable again: HTTP %d", code)
		}
		rec, _ := s.poolerRecord(ctx, conn.ID)
		if r := do(t, router, http.MethodDelete, base, ""); r.Code >= 300 {
			t.Fatalf("forget: HTTP %d %s", r.Code, r.Body)
		}
		if _, err := s.modules.docker.Inspect(ctx, rec.Container); err == nil {
			t.Error("the pooler container outlived its connection")
		}
		if _, err := s.modules.docker.InspectNetwork(ctx, rec.Network); err == nil {
			t.Error("the pooler network outlived its connection")
		}
		if _, err := s.modules.docker.Inspect(ctx, name); err != nil {
			t.Errorf("the database container went with its connection: %v", err)
		}
	})
}
