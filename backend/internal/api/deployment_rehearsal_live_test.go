package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/config"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store/storetest"
	"github.com/go-chi/chi/v5"
)

// Migration rehearsal against real servers, end to end through the real
// engine: a database the dashboard provisioned, a backup of it, and a
// deployment whose release task reaches it through ${{database.N.url}}.
// Before any backup exists the release is stopped as unrehearsable and
// production is not touched. A migration that adds a column passes against
// the restored copy and then runs for real. A migration that fails on the
// data production holds — NOT NULL on a column with a null in it — fails
// against the copy, the release stops before its release task, production's
// schema and rows are exactly as they were, and the copy's containers,
// volumes and network are gone. Every container is the test's and is
// removed with it.

// liveEngine is what differs between the engines a rehearsal restores.
type liveEngine struct {
	template              string
	version               string
	createTable           string
	addColumn, setNotNull string
	// refusal is in the failing migration's error.
	refusal string
	// locks: the copy's lock table is read and the passing migration holds
	// its lock long enough to be measured.
	locks       bool
	placeholder string
}

var livePostgres = liveEngine{
	template:    "postgres",
	createTable: `CREATE TABLE items (id SERIAL PRIMARY KEY, name TEXT)`,
	// One transaction holding its ACCESS EXCLUSIVE lock for two seconds.
	addColumn:   `psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -c "ALTER TABLE items ADD COLUMN sku TEXT; SELECT pg_sleep(2)"`,
	setNotNull:  `psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -c "ALTER TABLE items ALTER COLUMN name SET NOT NULL"`,
	refusal:     "null values",
	locks:       true,
	placeholder: "$1",
}

// The MariaDB client takes no URL: the task reads the parts out of it.
const liveMariaDBClient = `u=${DATABASE_URL#*://}; auth=${u%%@*}; rest=${u#*@}; host=${rest%%:*}; db=${rest#*/}; ` +
	`mariadb -h "$host" -u "${auth%%:*}" -p"${auth#*:}" "$db" -e `

var liveMariaDB = liveEngine{
	template:    "mariadb",
	createTable: `CREATE TABLE items (id INT AUTO_INCREMENT PRIMARY KEY, name TEXT)`,
	addColumn:   liveMariaDBClient + `"ALTER TABLE items ADD COLUMN sku TEXT"`,
	setNotNull:  liveMariaDBClient + `"ALTER TABLE items MODIFY name TEXT NOT NULL"`,
	refusal:     "Data truncated",
	placeholder: "?",
}

// The backup is a backup job's native dump.
func TestLiveMigrationRehearsalBlocksAFailingMigration(t *testing.T) {
	live := startLiveRehearsal(t, livePostgres)
	conn := live.database(fmt.Sprintf("jd-rehearsal-pg-%d", live.stamp))
	live.seed(conn.ID)
	live.rehearseBoth(conn.ID, "dump", func() { live.dump(conn.ID) })
}

// The same against MariaDB, restored from its dump with the image's own
// client; MariaDB's images carry no mysql command from 11 on.
func TestLiveMigrationRehearsalRestoresAMariaDBDump(t *testing.T) {
	live := startLiveRehearsal(t, liveMariaDB)
	conn := live.database(fmt.Sprintf("jd-rehearsal-maria-%d", live.stamp))
	live.seed(conn.ID)
	live.rehearseBoth(conn.ID, "dump", func() { live.dump(conn.ID) })
}

// The backup is the server's point-in-time archive, written by the WAL-G
// release JD_TEST_WALG names into a directory: the copy is recovered to the
// archive's end, then started again without the archive or its key.
func TestLiveMigrationRehearsalRecoversFromThePointInTimeArchive(t *testing.T) {
	pitrLiveTool(t)
	engine := livePostgres
	engine.version = "17"
	live := startLiveRehearsal(t, engine)
	name := fmt.Sprintf("jd-rehearsal-pitr-%d", live.stamp)
	conn := live.database(name)
	archive := filepath.Join(live.s.Cfg.FileRoots[0], "pitr")
	if err := os.MkdirAll(archive, 0o755); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			p := &httpx.Principal{User: &auth.User{ID: 1, Username: "tester"}, Role: auth.RoleAdmin, Kind: "session", IP: "127.0.0.1"}
			next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), p)))
		})
	})
	live.s.mountDatabaseRoutes(router)
	pitr := &pitrLive{t: t, s: live.s, router: router, name: name, conn: conn.ID}
	pitr.job(pitr.do(http.MethodPost, "/pitr", fmt.Sprintf(`{"targetKind":"local","target":{"path":%q},"schedule":"","retentionDays":7}`, archive)))
	// Archiving restarted the server: the pool the test holds is reopened.
	live.s.modules.dbs.Close(conn.ID)
	live.seed(conn.ID)
	pitr.archiveNow()
	live.rehearseBoth(conn.ID, "pitr", nil)
}

// The same gate in front of a promotion: production's release tasks, in the
// image staging built, rehearsed against a copy of production's database
// with production's variables before they run for real. Before any backup
// the promotion ends rehearsal_unavailable; a migration that passes against
// the copy reaches production; one that fails there ends the promotion
// rehearsal_failed with production's schema, rows and live release as they
// were, and the staging release still the one production last took.
func TestLiveMigrationRehearsalGatesAPromotedRelease(t *testing.T) {
	live := startLiveRehearsal(t, livePostgres)
	live.promote = true
	conn := live.database(fmt.Sprintf("jd-rehearsal-promote-%d", live.stamp))
	live.seed(conn.ID)
	live.rehearseBoth(conn.ID, "dump", func() { live.dump(conn.ID) })
}

// What a process stopped mid-rehearsal left — a copy, its volume, a task
// container and the network — is removed at the next start, found by the
// rehearsal label alone; a managed container without it is left.
func TestLiveRehearsalLeftoversAreRemovedAtStart(t *testing.T) {
	live := startLiveRehearsal(t, livePostgres)
	ctx, docker := live.ctx, live.s.modules.docker
	name := fmt.Sprintf("jd-rehearsal-orphan-%d", live.stamp)
	labels := map[string]string{"io.just-dashboard.managed": "true", rehearsalLabel: "424242"}
	specLabels := []dockerx.LabelSpec{}
	for key, value := range labels {
		specLabels = append(specLabels, dockerx.LabelSpec{Name: key, Value: value})
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), time.Minute)
		defer stop()
		for _, container := range []string{name + "-db", name + "-task", name + "-other"} {
			_ = docker.RemoveContainer(cleanup, container, true, true)
		}
		_ = docker.RemoveVolume(cleanup, name+"-data", true)
		_ = docker.RemoveNetwork(cleanup, name)
	})
	if _, err := docker.CreateNetwork(ctx, dockerx.NetworkSpec{Name: name, Driver: "bridge", Internal: true, Labels: labels}); err != nil {
		t.Fatal(err)
	}
	if _, err := docker.CreateVolume(ctx, dockerx.VolumeSpec{Name: name + "-data", Labels: labels}); err != nil {
		t.Fatal(err)
	}
	image := provisionTemplates["postgres"].image
	for _, spec := range []dockerx.ContainerSpec{
		{Name: name + "-db", Image: image, Entrypoint: []string{"sleep"}, Command: []string{"600"}, Labels: specLabels, Networks: []string{name},
			Mounts: []dockerx.MountSpec{{Type: "volume", Source: name + "-data", Target: "/data"}}, Start: true, Pull: "missing"},
		{Name: name + "-task", Image: image, Entrypoint: []string{"sleep"}, Command: []string{"600"}, Labels: specLabels, Networks: []string{name}, Start: true, Pull: "missing"},
		{Name: name + "-other", Image: image, Entrypoint: []string{"sleep"}, Command: []string{"600"},
			Labels: []dockerx.LabelSpec{{Name: "io.just-dashboard.managed", Value: "true"}}, Start: true, Pull: "missing"},
	} {
		if _, err := docker.Create(ctx, spec, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := live.s.modules.rehearser.RemoveOrphanedRehearsals(ctx); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{name + "-db", name + "-task"} {
		if _, err := docker.Inspect(ctx, gone); err == nil {
			t.Errorf("%s was left", gone)
		}
	}
	if exists, _ := docker.VolumeExists(ctx, name+"-data"); exists {
		t.Error("the copy's volume was left")
	}
	if exec.Command("docker", "network", "inspect", name).Run() == nil {
		t.Error("the rehearsal network was left")
	}
	if _, err := docker.Inspect(ctx, name+"-other"); err != nil {
		t.Errorf("a managed container without the rehearsal label was removed: %v", err)
	}
}

type liveRehearsal struct {
	t      *testing.T
	s      *Server
	admin  *client
	ctx    context.Context
	stamp  int64
	engine liveEngine
	// promote releases production by promoting a staging release of the
	// same image instead of deploying production itself.
	promote bool
}

func startLiveRehearsal(t *testing.T, engine liveEngine) *liveRehearsal {
	t.Helper()
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 on a Docker host")
	}
	stamp := time.Now().UnixNano()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s := liveRehearsalServer(t)
	// Release containers and database networks are named after the
	// environment's id; a fresh store would start at 1.
	environmentBase := stamp%100_000_000 + 800_000_000
	if _, err := s.Store.DB.Exec(`DELETE FROM sqlite_sequence WHERE name = 'deploy_environments'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`INSERT INTO sqlite_sequence(name, seq) VALUES('deploy_environments', ?)`, environmentBase); err != nil {
		t.Fatal(err)
	}
	if err := s.modules.deployEngine.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdown, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		_ = s.modules.deployEngine.Shutdown(shutdown)
	})
	return &liveRehearsal{t: t, s: s, ctx: ctx, stamp: stamp, engine: engine,
		admin: &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "rehearsal-live", auth.RoleAdmin)}}
}

// database provisions and adopts the engine's server through the routes,
// removed with everything named after it when the test ends.
func (l *liveRehearsal) database(name string) dbConnection {
	t := l.t
	t.Helper()
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 2*time.Minute)
		defer stop()
		docker := l.s.modules.docker
		containers, _ := docker.ListContainers(cleanup, true)
		for _, c := range containers {
			if strings.HasPrefix(c.Name, name) {
				_ = docker.RemoveContainer(cleanup, c.ID, true, true)
			}
		}
		volumes, _ := docker.ListVolumes(cleanup)
		for _, v := range volumes {
			if strings.HasPrefix(v.Name, name) || v.Labels[pitrLabel] == name {
				_ = docker.RemoveVolume(cleanup, v.Name, true)
			}
		}
	})
	request := map[string]string{"engine": l.engine.template, "name": name, "database": "app", "exposure": "local"}
	if l.engine.version != "" {
		request["version"] = l.engine.version
	}
	body, _ := json.Marshal(request)
	if res := l.admin.do(http.MethodPost, "/api/v1/databases/provision", string(body), nil); res.Code != http.StatusAccepted {
		t.Fatalf("provision = %d %s", res.Code, res.Body.String())
	}
	var conn dbConnection
	deadline := time.Now().Add(3 * time.Minute)
	for {
		adopted := l.admin.do(http.MethodPost, "/api/v1/databases/adopt", fmt.Sprintf(`{"container":%q}`, name), nil)
		if adopted.Code < 300 && json.Unmarshal(adopted.Body.Bytes(), &conn) == nil && conn.ID > 0 {
			ping := l.admin.do(http.MethodGet, fmt.Sprintf("/api/v1/databases/%d/ping", conn.ID), "", nil)
			if strings.Contains(ping.Body.String(), `"ok":true`) {
				return conn
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the database never answered; last adopt %d %s", adopted.Code, adopted.Body.String())
		}
		time.Sleep(time.Second)
	}
}

// seed writes the table both migrations act on, with a null in the column
// the failing one makes NOT NULL.
func (l *liveRehearsal) seed(connectionID int64) {
	l.t.Helper()
	pool, _, err := l.s.dbPool(l.ctx, connectionID)
	if err != nil {
		l.t.Fatal(err)
	}
	for _, statement := range []string{
		l.engine.createTable,
		`INSERT INTO items(name) VALUES ('first'), (NULL), ('third')`,
	} {
		if _, err := pool.ExecContext(l.ctx, statement); err != nil {
			l.t.Fatalf("%s: %v", statement, err)
		}
	}
}

// dump runs a backup job holding the database's native dump once.
func (l *liveRehearsal) dump(connectionID int64) {
	l.t.Helper()
	source := filepath.Join(l.s.Cfg.FileRoots[0], "app-files")
	if err := os.MkdirAll(source, 0o700); err != nil {
		l.t.Fatal(err)
	}
	job, err := l.s.modules.backupStore.Create(l.ctx, &backups.Job{
		Name: "rehearsal-dump", Sources: []string{source}, DatabaseDumps: []int64{connectionID},
		TargetKind: backups.TargetLocal, Target: backups.TargetConfig{Path: filepath.Join(l.s.Cfg.FileRoots[0], "archives")},
	}, nil)
	if err != nil {
		l.t.Fatal(err)
	}
	if run, err := l.s.modules.backupRunner.Execute(l.ctx, job.ID, "test"); err != nil || run.Status != backups.StatusSuccess {
		l.t.Fatalf("backup run = %+v, %v", run, err)
	}
}

// rehearseBoth deploys the passing migration, then the failing one, and
// checks what each did to production and what each left on the daemon. With
// backup, a deploy before it is stopped for want of one, and backup then
// makes it.
func (l *liveRehearsal) rehearseBoth(connectionID int64, wantSource string, backup func()) {
	t, s, admin, engine := l.t, l.s, l.admin, l.engine
	t.Helper()
	// The release is the database's own image, idle, with a release task
	// that migrates through the database's application URL.
	image := provisionTemplates[engine.template].image
	projectID, environmentID := liveRehearsalProject(t, admin, fmt.Sprintf("rehearsal-live-%d", l.stamp%1_000_000), image, connectionID, engine.addColumn)
	t.Cleanup(func() {
		removeEnvironmentArtifacts(t, environmentID)
		// A promotion tags the image under production's release name; the
		// tag goes with the environment, the image stays the database's.
		removeLiveEnvironment(s, environmentID)
		cleanup, stop := context.WithTimeout(context.Background(), time.Minute)
		defer stop()
		_ = s.modules.deployDatabases.RemoveRuntimeNetworks(cleanup, environmentID)
	})
	base := fmt.Sprintf("/api/v1/deploy/%d/environments/%d", projectID, environmentID)
	if res := admin.do(http.MethodPut, base+"/rehearsal", `{"enabled":true,"whenUnavailable":"block","restoreTimeoutSeconds":600,"maxBackupMb":512}`, nil); res.Code != http.StatusOK {
		t.Fatalf("rehearsal terms = %d %s", res.Code, res.Body.String())
	}
	release := func() deploy.RunSnapshot {
		return liveRehearsalRun(t, admin, projectID, environmentID, "/runs", `{"operation":"deploy"}`)
	}
	var stagingRelease deploy.Release
	if l.promote {
		stagingRelease = l.stageRelease(projectID, environmentID)
		release = func() deploy.RunSnapshot {
			snapshot := liveRehearsalRun(t, admin, projectID, environmentID, "/promote", fmt.Sprintf(`{"releaseId":%d}`, stagingRelease.ID))
			if snapshot.Run.Operation != deploy.OperationPromote || stepState(snapshot, deploy.StepBuildArtifact) != "" {
				t.Fatalf("the promotion run is a %s with build step %q", snapshot.Run.Operation, stepState(snapshot, deploy.StepBuildArtifact))
			}
			if state := stepState(snapshot, deploy.StepRehearseMigration); state == "" {
				t.Fatal("the promotion run has no rehearsal step")
			}
			return snapshot
		}
	}
	liveReleaseID := func() int64 {
		live, err := s.modules.deployRuns.LiveRelease(l.ctx, environmentID)
		if err != nil {
			return 0
		}
		return live.Release.ID
	}

	if backup != nil {
		refused := release()
		if refused.Run.State != deploy.RunFailed || refused.Run.TerminalCode != "rehearsal_unavailable" ||
			!strings.Contains(refused.Run.TerminalReason, "no backup holds") {
			t.Fatalf("a deploy with nothing to restore = %s %s: %s", refused.Run.State, refused.Run.TerminalCode, refused.Run.TerminalReason)
		}
		if state := stepState(refused, deploy.StepReleaseTask); state == deploy.StepPassed || state == deploy.StepFailed || state == deploy.StepRunning {
			t.Fatalf("the release task ran without a rehearsal: %s", state)
		}
		if l.columnExists(connectionID, "sku") {
			t.Fatal("the migration reached production without a rehearsal")
		}
		liveRehearsalLeftNothing(t, refused)
		if l.promote && liveReleaseID() != 0 {
			t.Fatal("a promotion refused for want of a backup changed production's live release")
		}
		t.Logf("without a backup: %s", refused.Run.TerminalReason)
		backup()
	}

	first := release()
	if first.Run.State != deploy.RunSucceeded {
		t.Fatalf("the passing migration's run = %s %s: %s", first.Run.State, first.Run.TerminalCode, first.Run.TerminalReason)
	}
	report := rehearsalReportOf(t, first)
	if !report.Rehearsed || report.Source == nil || report.Source.Kind != wantSource || len(report.Tasks) != 1 || report.Tasks[0].ExitCode != 0 || !report.CleanedUp {
		t.Fatalf("the passing rehearsal's report = %+v", report)
	}
	if stepState(first, deploy.StepReleaseTask) != deploy.StepPassed {
		t.Fatalf("the release task after a passing rehearsal = %s", stepState(first, deploy.StepReleaseTask))
	}
	if report.Isolation == nil || !report.Isolation.Internal || report.Isolation.Published || report.Isolation.VolumesMounted {
		t.Fatalf("the rehearsal's isolation = %+v", report.Isolation)
	}
	held := deploy.RehearsalHeldLock{}
	for _, lock := range report.Locks.Held {
		if lock.Relation == "items" && lock.Mode == "AccessExclusiveLock" {
			held = lock
		}
	}
	if engine.locks && (!report.Locks.Read || report.Locks.Samples == 0 || held.HeldMS < 1000) {
		t.Fatalf("the rehearsal did not measure the migration's lock: %+v", report.Locks)
	}
	if !engine.locks && report.Locks.Read {
		t.Fatalf("the report claims a lock reading the engine has no reader for: %+v", report.Locks)
	}
	t.Logf("passing rehearsal: restored %s from %s (%s) in %d ms, task %d ms, %d lock samples, held %+v, isolation %+v",
		report.Database.Name, report.Source.Kind, report.Source.Method, report.RestoreMS, report.Tasks[0].DurationMS, report.Locks.Samples, report.Locks.Held, *report.Isolation)
	liveRehearsalLeftNothing(t, first)
	if !l.columnExists(connectionID, "sku") {
		t.Fatal("the migration did not reach production after its rehearsal passed")
	}
	promoted := liveReleaseID()
	if l.promote {
		live, err := s.modules.deployRuns.LiveRelease(l.ctx, environmentID)
		if err != nil || live.Release.Promotion == nil || live.Release.Promotion.ReleaseID != stagingRelease.ID {
			t.Fatalf("production's live release after the promotion = %#v, %v", live, err)
		}
		if transcript := liveTranscript(t, s, first.Run.ID); !strings.Contains(transcript, "promoted image") || !strings.Contains(transcript, "no build") {
			t.Fatalf("the promotion's transcript does not say the tasks were rehearsed in the promoted image with no build:\n%s", transcript)
		}
		t.Logf("production runs staging release %d, promoted with no build after its tasks were rehearsed", stagingRelease.Number)
	}
	before := l.columnNullable(connectionID, "name")

	// The migration that fails on the data production holds.
	liveRehearsalReleaseTask(t, admin, base, engine.setNotNull)
	second := release()
	if second.Run.State != deploy.RunFailed || second.Run.TerminalCode != "rehearsal_failed" {
		t.Fatalf("the failing migration's run = %s %s: %s", second.Run.State, second.Run.TerminalCode, second.Run.TerminalReason)
	}
	t.Logf("blocked: %s", second.Run.TerminalReason)
	if !strings.Contains(second.Run.TerminalReason, engine.refusal) || !strings.Contains(second.Run.TerminalReason, "before production was touched") {
		t.Fatalf("the reason does not name the refusal: %s", second.Run.TerminalReason)
	}
	if state := stepState(second, deploy.StepReleaseTask); state == deploy.StepPassed || state == deploy.StepFailed || state == deploy.StepRunning {
		t.Fatalf("the release task ran against production after a failed rehearsal: %s", state)
	}
	if after := l.columnNullable(connectionID, "name"); after != before || after != "YES" {
		t.Fatalf("production's schema changed: name is_nullable %q, was %q", after, before)
	}
	pool, _, err := s.dbPool(l.ctx, connectionID)
	if err != nil {
		t.Fatal(err)
	}
	var nulls int
	if err := pool.QueryRowContext(l.ctx, `SELECT COUNT(*) FROM items WHERE name IS NULL`).Scan(&nulls); err != nil || nulls != 1 {
		t.Fatalf("production's rows changed: %d nulls, %v", nulls, err)
	}
	failed := rehearsalReportOf(t, second)
	if !failed.CleanedUp || len(failed.Tasks) != 1 || failed.Tasks[0].ExitCode == 0 || failed.Source == nil || failed.Source.Kind != wantSource {
		t.Fatalf("the failing rehearsal's report = %+v", failed)
	}
	liveRehearsalLeftNothing(t, second)
	if liveReleaseID() != promoted {
		t.Fatalf("production's live release changed from %d to %d under a release whose rehearsal failed", promoted, liveReleaseID())
	}
	t.Logf("production schema unchanged (items.name is_nullable=%s), the copy's containers, volumes and network removed", before)
}

// stageRelease adds a staging environment beside production, takes its
// release tasks away — staging has no database link, and the task is
// production's to run — deploys it, and returns its live release: the image
// production is promoted from.
func (l *liveRehearsal) stageRelease(projectID, environmentID int64) deploy.Release {
	t, s, admin := l.t, l.s, l.admin
	t.Helper()
	added := admin.do(http.MethodPost, fmt.Sprintf("/api/v1/deploy/%d/environments", projectID), `{"kind":"staging"}`, nil)
	if added.Code != http.StatusCreated {
		t.Fatalf("add staging = %d %s", added.Code, added.Body.String())
	}
	var copied deploy.StagingCopy
	if err := json.Unmarshal(added.Body.Bytes(), &copied); err != nil {
		t.Fatal(err)
	}
	staging := copied.Environment.ID
	t.Cleanup(func() {
		removeEnvironmentArtifacts(t, staging)
		removeLiveEnvironment(s, staging)
		cleanup, stop := context.WithTimeout(context.Background(), time.Minute)
		defer stop()
		_ = s.modules.deployDatabases.RemoveRuntimeNetworks(cleanup, staging)
	})
	liveRehearsalSetReleaseTasks(t, admin, fmt.Sprintf("/api/v1/deploy/%d/environments/%d", projectID, staging), nil)
	built := liveRehearsalRun(t, admin, projectID, staging, "/runs", `{"operation":"deploy"}`)
	if built.Run.State != deploy.RunSucceeded {
		t.Fatalf("staging's deploy = %s %s: %s", built.Run.State, built.Run.TerminalCode, built.Run.TerminalReason)
	}
	live, err := s.modules.deployRuns.LiveRelease(l.ctx, staging)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.modules.deployRuns.PromotionSource(l.ctx, projectID, live.Release.ID); err != nil {
		t.Fatalf("staging's release cannot be promoted: %v", err)
	}
	if _, err := s.modules.deployRuns.LiveRelease(l.ctx, environmentID); err == nil {
		t.Fatal("production has a live release before anything was promoted")
	}
	return live.Release
}

// liveRehearsalLeftNothing checks the daemon for anything a run's rehearsal
// made: by the run's label, and by the copy's name for what the
// point-in-time recovery's own helpers label.
func liveRehearsalLeftNothing(t *testing.T, snapshot deploy.RunSnapshot) {
	t.Helper()
	_, copyName := rehearsalNames(snapshot.Run)
	for _, filter := range []string{rehearsalLabel + "=" + fmt.Sprint(snapshot.Run.ID), pitrLabel + "=" + copyName} {
		for _, args := range [][]string{
			{"ps", "-aq", "--filter", "label=" + filter},
			{"volume", "ls", "-q", "--filter", "label=" + filter},
			{"network", "ls", "-q", "--filter", "label=" + filter},
		} {
			out, err := exec.Command("docker", args...).Output()
			if err != nil || strings.TrimSpace(string(out)) != "" {
				t.Fatalf("run %d's rehearsal left something behind (docker %s): %q %v", snapshot.Run.ID, strings.Join(args, " "), out, err)
			}
		}
	}
	if out, _ := exec.Command("docker", "ps", "-aq", "--filter", "name="+copyName).Output(); strings.TrimSpace(string(out)) != "" {
		t.Fatalf("run %d's copy %s is still there", snapshot.Run.ID, copyName)
	}
}

func liveRehearsalServer(t *testing.T) *Server {
	t.Helper()
	st, err := storetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	sealer, err := auth.NewSealer(strings.Repeat("ef", 32))
	if err != nil {
		t.Fatal(err)
	}
	_, loopback, _ := net.ParseCIDR("127.0.0.1/32")
	root := t.TempDir()
	cfg := &config.Config{
		Addr: "127.0.0.1:8080", DataDir: t.TempDir(), AllowedCIDRs: []*net.IPNet{loopback},
		SessionTTL: time.Hour, IdleTTL: time.Hour, FileRoots: []string{root}, LogRoots: []string{root},
		DeployRoots: []string{root}, NginxDir: filepath.Join(root, "nginx"), CaddyFile: filepath.Join(root, "Caddyfile"),
		DockerHost: "unix:///var/run/docker.sock",
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := auth.NewService(st, sealer, cfg.SessionTTL, cfg.IdleTTL, cfg.Require2FA)
	s := New(cfg, log, st, svc, sealer, audit.New(st, log), nil)
	t.Cleanup(s.Shutdown)
	return s
}

func liveRehearsalProject(t *testing.T, c *client, name, image string, connectionID int64, task string) (int64, int64) {
	t.Helper()
	step := func(method, path string, body any) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(body)
		res := c.do(method, path, string(raw), nil)
		if res.Code >= 300 {
			t.Fatalf("%s %s = %d %s", method, path, res.Code, res.Body.String())
		}
		var out map[string]any
		_ = json.Unmarshal(res.Body.Bytes(), &out)
		return out
	}
	draft := step(http.MethodPost, "/api/v1/deploy/drafts", map[string]any{})
	id := fmt.Sprint(draft["id"])
	revision := func(d map[string]any) any { return d["revision"] }
	draft = step(http.MethodPut, "/api/v1/deploy/drafts/"+id, map[string]any{"revision": revision(draft), "step": "intent", "intent": map[string]any{"name": name, "profile": "worker"}})
	draft = step(http.MethodPut, "/api/v1/deploy/drafts/"+id, map[string]any{"revision": revision(draft), "step": "source", "source": map[string]any{"kind": "image", "mode": "image_reference", "image": image}})
	draft = step(http.MethodPost, "/api/v1/deploy/drafts/"+id+"/detect", map[string]any{"revision": revision(draft)})
	draft = step(http.MethodPut, "/api/v1/deploy/drafts/"+id, map[string]any{
		"revision": revision(draft), "step": "configuration",
		"configuration": map[string]any{
			"build": map[string]any{"method": "image", "noCache": false, "secrets": []any{}, "releaseTasks": []any{
				map[string]any{"name": "migrate", "command": task, "timeoutSeconds": 120, "env": []string{"DATABASE_URL"}, "runner": "image"},
			}},
			"runtime": map[string]any{
				"image": image, "command": []string{"sleep", "3600"}, "internalPort": 0, "hostPort": 0,
				"bindAddress": "127.0.0.1", "strategy": "stop_first", "privileged": false, "hostNetwork": false,
				"capabilities": []any{}, "devices": []any{}, "mounts": []any{}, "stopSignal": "SIGTERM", "gracePeriodSeconds": 1,
			},
			"variables": []any{map[string]any{
				"name": "DATABASE_URL", "reference": fmt.Sprintf("${{database.%d.url}}", connectionID),
				"sensitivity": "secret", "scopes": []string{"runtime", "release_task"},
			}},
			"dependencies": []any{map[string]any{
				"kind": "database", "resourceKind": "database_connection", "resourceId": fmt.Sprint(connectionID),
				"ownership": "linked",
			}},
			"checks": []any{}, "domains": []any{},
		},
	})
	preflight := step(http.MethodPost, "/api/v1/deploy/drafts/"+id+"/preflight", map[string]any{"revision": revision(draft)})
	warnings := []string{}
	for _, raw := range preflight["preflight"].(map[string]any)["findings"].([]any) {
		finding := raw.(map[string]any)
		switch finding["severity"] {
		case "blocked", "decision":
			t.Fatalf("preflight refused the fixture: %v", finding)
		case "warning":
			warnings = append(warnings, finding["code"].(string))
		}
	}
	committed := step(http.MethodPost, "/api/v1/deploy/drafts/"+id+"/commit", map[string]any{
		"revision": revision(preflight["draft"].(map[string]any)), "acknowledgedWarnings": warnings,
	})
	number := func(value any) int64 {
		if float, ok := value.(float64); ok {
			return int64(float)
		}
		var parsed int64
		_, _ = fmt.Sscan(fmt.Sprint(value), &parsed)
		return parsed
	}
	return number(committed["projectId"]), number(committed["environmentId"])
}

func liveRehearsalReleaseTask(t *testing.T, c *client, base, task string) {
	t.Helper()
	liveRehearsalSetReleaseTasks(t, c, base, func(tasks []deploy.ReleaseTaskConfig) []deploy.ReleaseTaskConfig {
		tasks[0].Command = task
		return tasks
	})
}

// liveRehearsalSetReleaseTasks saves the environment's configuration with
// its release tasks changed by edit, or with none when edit is nil.
func liveRehearsalSetReleaseTasks(t *testing.T, c *client, base string, edit func([]deploy.ReleaseTaskConfig) []deploy.ReleaseTaskConfig) {
	t.Helper()
	read := c.do(http.MethodGet, base+"/configuration", "", nil)
	var current deploy.EnvironmentConfiguration
	if err := json.Unmarshal(read.Body.Bytes(), &current); err != nil || read.Code != http.StatusOK {
		t.Fatalf("configuration = %d %s", read.Code, read.Body.String())
	}
	if edit == nil {
		current.Build.ReleaseTasks = []deploy.ReleaseTaskConfig{}
	} else {
		current.Build.ReleaseTasks = edit(current.Build.ReleaseTasks)
	}
	body, _ := json.Marshal(deploy.ConfigurationWriteRequest{
		Revision: current.Revision, Build: current.Build, Runtime: current.Runtime,
		Dependencies: current.Dependencies, Checks: current.Checks, Domains: current.Domains,
	})
	if res := c.do(http.MethodPut, base+"/configuration", string(body), nil); res.Code != http.StatusOK {
		t.Fatalf("configuration save = %d %s", res.Code, res.Body.String())
	}
}

// liveRehearsalRun queues a run through route — a deploy on /runs, a
// promotion on /promote — and waits for it to end.
func liveRehearsalRun(t *testing.T, c *client, projectID, environmentID int64, route, body string) deploy.RunSnapshot {
	t.Helper()
	res := c.do(http.MethodPost, fmt.Sprintf("/api/v1/deploy/%d/environments/%d%s", projectID, environmentID, route), body, nil)
	var run deploy.EngineRun
	if err := json.Unmarshal(res.Body.Bytes(), &run); err != nil || res.Code != http.StatusAccepted {
		t.Fatalf("POST %s = %d %s", route, res.Code, res.Body.String())
	}
	deadline := time.Now().Add(8 * time.Minute)
	for {
		read := c.do(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/runs/%d", projectID, run.ID), "", nil)
		var snapshot deploy.RunSnapshot
		if err := json.Unmarshal(read.Body.Bytes(), &snapshot); err != nil {
			t.Fatalf("run %d = %d %s", run.ID, read.Code, read.Body.String())
		}
		if snapshot.Run.State.Terminal() {
			return snapshot
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %d stayed %s", run.ID, snapshot.Run.State)
		}
		time.Sleep(time.Second)
	}
}

func rehearsalReportOf(t *testing.T, snapshot deploy.RunSnapshot) deploy.RehearsalReport {
	t.Helper()
	for _, step := range snapshot.Steps {
		if step.Key == deploy.StepRehearseMigration {
			var report deploy.RehearsalReport
			if err := json.Unmarshal(step.Evidence, &report); err != nil {
				t.Fatalf("rehearsal evidence = %s", step.Evidence)
			}
			return report
		}
	}
	t.Fatal("the run has no rehearsal step")
	return deploy.RehearsalReport{}
}

func stepState(snapshot deploy.RunSnapshot, key deploy.StepKey) deploy.StepState {
	state := deploy.StepState("")
	for _, step := range snapshot.Steps {
		if step.Key == key {
			state = step.State
		}
	}
	return state
}

// columnExists and columnNullable read production's own schema, through
// the dashboard's pool to it.
func (l *liveRehearsal) columnExists(connectionID int64, column string) bool {
	l.t.Helper()
	var count int
	l.columnQuery(connectionID, `SELECT COUNT(*)`, column, &count)
	return count == 1
}

func (l *liveRehearsal) columnNullable(connectionID int64, column string) string {
	l.t.Helper()
	var nullable string
	l.columnQuery(connectionID, `SELECT MAX(is_nullable)`, column, &nullable)
	return nullable
}

func (l *liveRehearsal) columnQuery(connectionID int64, selection, column string, into any) {
	l.t.Helper()
	pool, _, err := l.s.dbPool(l.ctx, connectionID)
	if err != nil {
		l.t.Fatal(err)
	}
	query := selection + ` FROM information_schema.columns WHERE table_name = 'items' AND column_name = ` + l.engine.placeholder
	if l.engine.placeholder == "?" {
		query += ` AND table_schema = DATABASE()`
	}
	if err := pool.QueryRowContext(l.ctx, query, column).Scan(into); err != nil {
		l.t.Fatalf("%s: %v", query, err)
	}
}

// removeEnvironmentArtifacts removes what an environment's runs left on the
// daemon: its release containers and any rehearsal leftovers.
func removeEnvironmentArtifacts(t *testing.T, environmentID int64) {
	out, _ := exec.Command("docker", "ps", "-aq", "--filter", fmt.Sprintf("label=io.just-dashboard.environment-id=%d", environmentID)).Output()
	for _, id := range strings.Fields(string(out)) {
		_ = exec.Command("docker", "rm", "-f", "-v", id).Run()
	}
}
