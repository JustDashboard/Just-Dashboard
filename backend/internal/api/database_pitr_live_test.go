package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/go-chi/chi/v5"
)

// Point-in-time recovery against real servers: a PostgreSQL started through
// the provisioning route, archived by the WAL-G release JD_TEST_WALG names,
// into a directory and — where JD_TEST_PITR_S3 names a bucket
// (http://key:secret@host:port/bucket) — into an S3 store. Rows are written,
// the moment noted, more rows written, and a copy recovered to the moment
// must hold the first rows and none of the rest. Everything the test makes
// is named jdt-pitr-… and removed after it.

func pitrLiveTool(t *testing.T) {
	t.Helper()
	binary := os.Getenv("JD_TEST_WALG")
	if binary == "" {
		t.Skip("set JD_TEST_WALG to a WAL-G release binary to run point-in-time recovery against Docker")
	}
	previous := dbx.WALGBinary
	dbx.WALGBinary = binary
	t.Cleanup(func() { dbx.WALGBinary = previous })
}

type pitrLive struct {
	t      *testing.T
	s      *Server
	router http.Handler
	name   string
	conn   int64
}

func newPITRLive(t *testing.T, name string) *pitrLive {
	t.Helper()
	pitrLiveTool(t)
	s := testServer(t)
	s.modules.docker = dockerx.New(envOr("JD_TEST_DOCKER_HOST", "unix:///var/run/docker.sock"))
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			p := &httpx.Principal{User: &auth.User{ID: 1, Username: "tester"}, Role: auth.RoleAdmin, Kind: "session", IP: "127.0.0.1"}
			next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), p)))
		})
	})
	r.Use(httpx.AuditMutations(s.Audit))
	s.mountDatabaseRoutes(r)
	var router http.Handler = r
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if availability := s.modules.docker.Ping(ctx); !availability.Available {
		t.Skipf("Docker is not reachable: %s", availability.Error)
	}
	live := &pitrLive{t: t, s: s, router: router, name: name}
	t.Cleanup(live.cleanup)

	body, _ := json.Marshal(map[string]string{"engine": "postgres", "version": "17", "name": name, "database": "app", "exposure": "local"})
	if rec := do(t, router, http.MethodPost, "/databases/provision", string(body)); rec.Code != http.StatusAccepted {
		t.Fatalf("provision: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := s.modules.docker.UpdateResources(context.Background(), name, dockerx.ResourceLimits{MemoryMB: 512}); err != nil {
		t.Fatalf("memory limit: %v", err)
	}
	deadline := time.Now().Add(2 * time.Minute)
	for {
		rec := do(t, router, http.MethodPost, "/databases/adopt", fmt.Sprintf(`{"container":%q}`, name))
		var conn dbConnection
		if rec.Code < 300 && json.Unmarshal(rec.Body.Bytes(), &conn) == nil && conn.ID > 0 {
			var ping struct {
				OK bool `json:"ok"`
			}
			_ = json.Unmarshal(do(t, router, http.MethodGet, pathf("/databases/%d/ping", conn.ID), "").Body.Bytes(), &ping)
			if ping.OK {
				live.conn = conn.ID
				return live
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never answered: %d %s", name, rec.Code, rec.Body.String())
		}
		time.Sleep(time.Second)
	}
}

// cleanup removes every container and volume named for the test, whatever
// state it was left in.
func (l *pitrLive) cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	docker := l.s.modules.docker
	containers, _ := docker.ListContainers(ctx, true)
	for _, c := range containers {
		if strings.HasPrefix(c.Name, l.name) {
			_ = docker.RemoveContainer(ctx, c.ID, true, true)
		}
	}
	volumes, _ := docker.ListVolumes(ctx)
	for _, v := range volumes {
		if strings.HasPrefix(v.Name, l.name) {
			if err := docker.RemoveVolume(ctx, v.Name, true); err != nil {
				l.t.Errorf("volume %s was left: %v", v.Name, err)
			}
		}
	}
}

func (l *pitrLive) do(method, path, body string) *httptest.ResponseRecorder {
	return do(l.t, l.router, method, pathf("/databases/%d"+path, l.conn), body)
}

// job waits for a job a route started and fails the test unless it
// succeeded.
func (l *pitrLive) job(rec *httptest.ResponseRecorder) (jobs.Job, pitrResult) {
	l.t.Helper()
	if rec.Code != http.StatusAccepted {
		l.t.Fatalf("no job started: %d %s", rec.Code, rec.Body.String())
	}
	var started jobs.Job
	if err := json.Unmarshal(rec.Body.Bytes(), &started); err != nil {
		l.t.Fatal(err)
	}
	deadline := time.Now().Add(6 * time.Minute)
	for {
		job, lines, ok := l.s.modules.jobs.Get(started.ID)
		if !ok {
			l.t.Fatalf("job %s is gone", started.ID)
		}
		if job.Status != jobs.StatusRunning {
			var result pitrResult
			var text []string
			for _, line := range lines {
				if line.Stream == "result" {
					_ = json.Unmarshal([]byte(line.Text), &result)
				}
				text = append(text, line.Text)
			}
			if job.Status != jobs.StatusSucceeded {
				l.t.Fatalf("%s %s: %s\n%s", job.Title, job.Status, job.Error, strings.Join(text, "\n"))
			}
			return job, result
		}
		if time.Now().After(deadline) {
			l.t.Fatalf("job %s is still running", job.Title)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (l *pitrLive) pool(id int64) *sql.DB {
	l.t.Helper()
	conn, dsn, err := l.s.dbConnRow(context.Background(), id)
	if err != nil {
		l.t.Fatal(err)
	}
	pool, err := l.s.modules.dbs.Pool(context.Background(), id, conn.Driver, dsn)
	if err != nil {
		l.t.Fatal(err)
	}
	return pool
}

func (l *pitrLive) exec(id int64, statements ...string) {
	l.t.Helper()
	for _, stmt := range statements {
		if _, err := l.pool(id).Exec(stmt); err != nil {
			l.t.Fatalf("%s: %v", stmt, err)
		}
	}
}

func (l *pitrLive) rows(id int64) (count, highest int) {
	l.t.Helper()
	if err := l.pool(id).QueryRow(`SELECT count(*), coalesce(max(id), 0) FROM jdt_pitr_rows`).Scan(&count, &highest); err != nil {
		l.t.Fatal(err)
	}
	return count, highest
}

// archiveNow closes the segment being written and waits until the server has
// handed it to WAL-G.
func (l *pitrLive) archiveNow() {
	l.t.Helper()
	pool := l.pool(l.conn)
	var segment string
	if err := pool.QueryRow(`SELECT pg_walfile_name(pg_switch_wal())`).Scan(&segment); err != nil {
		l.t.Fatal(err)
	}
	deadline := time.Now().Add(time.Minute)
	for {
		var last sql.NullString
		if err := pool.QueryRow(`SELECT last_archived_wal FROM pg_stat_archiver`).Scan(&last); err != nil {
			l.t.Fatal(err)
		}
		if last.String >= segment {
			return
		}
		if time.Now().After(deadline) {
			l.t.Fatalf("segment %s was never archived; the last was %q", segment, last.String)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (l *pitrLive) reading() dbPITR {
	l.t.Helper()
	rec := l.do(http.MethodGet, "/pitr", "")
	if rec.Code != http.StatusOK {
		l.t.Fatalf("GET /pitr: %d %s", rec.Code, rec.Body.String())
	}
	var out dbPITR
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		l.t.Fatal(err)
	}
	return out
}

// writeAndNote writes rows 1–150, notes the moment, names a restore point
// after more rows, and writes 151–300 around it, archiving all of it.
func (l *pitrLive) writeAndNote() time.Time {
	l.t.Helper()
	l.exec(l.conn, `INSERT INTO jdt_pitr_rows(id) SELECT generate_series(101, 150)`)
	time.Sleep(1100 * time.Millisecond)
	var moment time.Time
	if err := l.pool(l.conn).QueryRow(`SELECT now()`).Scan(&moment); err != nil {
		l.t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	l.exec(l.conn, `INSERT INTO jdt_pitr_rows(id) SELECT generate_series(151, 200)`)
	if rec := l.do(http.MethodPost, "/pitr/restore-points", `{"name":"jdt-before-300"}`); rec.Code != http.StatusCreated {
		l.t.Fatalf("restore point: %d %s", rec.Code, rec.Body.String())
	}
	l.exec(l.conn, `INSERT INTO jdt_pitr_rows(id) SELECT generate_series(201, 300)`)
	l.archiveNow()
	return moment
}

func TestLivePointInTimeRecoveryIntoADirectory(t *testing.T) {
	live := newPITRLive(t, fmt.Sprintf("jdt-pitr-postgres-dir-%d", time.Now().Unix()))
	root, err := os.MkdirTemp("", "jdt-pitr-postgres-archive-")
	if err != nil {
		t.Fatal(err)
	}
	// What WAL-G writes belongs to the server's account, which this test is
	// not: a container of the image removes it.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, _ = live.s.runAsRoot(ctx, "postgres:17-alpine", live.name, []dockerx.MountSpec{{Type: "bind", Source: root, Target: "/a"}},
			[]string{"find", "/a", "-mindepth", "1", "-delete"})
		_ = os.RemoveAll(root)
	})
	live.s.modules.files = files.New([]string{root})
	live.exec(live.conn, `CREATE TABLE jdt_pitr_rows(id int PRIMARY KEY, at timestamptz DEFAULT now())`,
		`INSERT INTO jdt_pitr_rows(id) SELECT generate_series(1, 100)`)

	// The plan is shown before anything happens, and changes nothing.
	body := fmt.Sprintf(`{"targetKind":"local","target":{"path":%q},"schedule":"","retentionDays":7`, root)
	rec := live.do(http.MethodPost, "/pitr", body+`,"preview":true}`)
	var plan pitrPlan
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &plan) != nil ||
		!dbx.ArchivesWithWALG(plan.Command) || !strings.HasPrefix(plan.Location, root+"/"+live.name+"-") {
		t.Fatalf("preview: %d %s", rec.Code, rec.Body.String())
	}
	if before := live.reading(); before.Archive != nil || !before.Available {
		t.Fatalf("before setting up: %+v", before)
	}

	live.job(live.do(http.MethodPost, "/pitr", body+`}`))
	reading := live.reading()
	if reading.Archive == nil || reading.Drift || reading.Window.From == nil || reading.Archiver == nil ||
		reading.Archiver.ArchiveMode != "on" || len(reading.Archive.Reading.Backups) != 1 {
		t.Fatalf("after setting up: %+v", reading)
	}
	if _, err := os.Stat(root + "/" + reading.Archive.Stream); err != nil {
		t.Fatalf("the archive is not in the directory: %v", err)
	}
	if reading.Archive.KeyFingerprint == "" {
		t.Error("the archive names no key")
	}

	moment := live.writeAndNote()

	// A recovery to the moment holds the rows written before it and none after.
	rec = live.do(http.MethodPost, "/pitr/restore", fmt.Sprintf(`{"target":{"kind":"time","time":%q},"preview":true}`, moment.Format(time.RFC3339Nano)))
	var restorePlan pitrRestorePlan
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &restorePlan) != nil ||
		restorePlan.Backup != reading.Archive.Reading.Backups[0].Name || restorePlan.Settings["recovery_target_time"] == "" {
		t.Fatalf("restore preview: %d %s", rec.Code, rec.Body.String())
	}
	_, atMoment := live.job(live.do(http.MethodPost, "/pitr/restore", fmt.Sprintf(`{"target":{"kind":"time","time":%q}}`, moment.Format(time.RFC3339Nano))))
	if atMoment.ConnectionID == 0 || atMoment.RecoveredTo == nil || atMoment.RecoveredTo.After(moment) {
		t.Fatalf("recovered to the moment: %+v", atMoment)
	}
	if count, highest := live.rows(atMoment.ConnectionID); count != 150 || highest != 150 {
		t.Fatalf("the copy recovered to %s holds %d rows up to %d, want 150", moment, count, highest)
	}
	if count, _ := live.rows(live.conn); count != 300 {
		t.Fatalf("the server it was recovered from holds %d rows, want its 300 untouched", count)
	}
	// The copy runs on its own: no recovery settings, no archive mounted.
	copySpec, err := live.s.modules.docker.SpecOf(context.Background(), atMoment.Container)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(copySpec.Command, " "), "restore_command") || len(withPITRMounts(copySpec.Mounts, nil)) != len(copySpec.Mounts) {
		t.Errorf("the copy still carries the recovery: %q %+v", copySpec.Command, copySpec.Mounts)
	}

	// A named restore point.
	_, atPoint := live.job(live.do(http.MethodPost, "/pitr/restore", `{"target":{"kind":"name","name":"jdt-before-300"}}`))
	if count, highest := live.rows(atPoint.ConnectionID); count != 200 || highest != 200 {
		t.Fatalf("the copy recovered to the restore point holds %d rows up to %d, want 200", count, highest)
	}

	// Switching the first copy in: the server's own connection reaches it,
	// the server is kept aside, and archiving goes on into a new archive.
	reading = live.reading()
	var copyRow *pitrRestore
	for _, r := range reading.Restores {
		if r.Container == atMoment.Container {
			copyRow = r
		}
	}
	if copyRow == nil || copyRow.State != "ready" {
		t.Fatalf("the copy is not listed: %+v", reading.Restores)
	}
	_, switched := live.job(live.do(http.MethodPost, fmt.Sprintf("/pitr/restores/%d/switch", copyRow.ID), `{}`))
	if switched.Replaced == "" {
		t.Fatalf("switch: %+v", switched)
	}
	live.s.modules.dbs.Close(live.conn)
	if count, highest := live.rows(live.conn); count != 150 || highest != 150 {
		t.Fatalf("after the switch the server's connection reaches %d rows up to %d, want the copy's 150", count, highest)
	}
	replaced, err := live.s.modules.docker.Inspect(context.Background(), switched.Replaced)
	if err != nil || replaced.State == "running" || replaced.RestartPol != "no" {
		t.Fatalf("the replaced server: %+v %v", replaced, err)
	}
	after := live.reading()
	if after.Archive == nil || after.Archive.Stream == reading.Archive.Stream || len(after.Earlier) != 1 ||
		after.Archive.Reading == nil || len(after.Archive.Reading.Backups) != 1 {
		t.Fatalf("after the switch: %+v", after)
	}

	// Turning it off restarts the server without archiving and keeps the
	// archive; deleting an earlier one empties its folder.
	live.job(live.do(http.MethodDelete, "/pitr", `{}`))
	off := live.reading()
	if off.Archive != nil || len(off.Earlier) != 2 {
		t.Fatalf("after turning off: %+v", off)
	}
	spec, err := live.s.modules.docker.SpecOf(context.Background(), live.name)
	if err != nil || dbx.ArchivesWithWALG(spec.Command) {
		t.Fatalf("the server still archives: %q %v", spec.Command, err)
	}
	live.job(live.do(http.MethodDelete, fmt.Sprintf("/pitr/archives/%d", off.Earlier[len(off.Earlier)-1].ID), `{}`))
	if gone := live.reading(); len(gone.Earlier) != 1 {
		t.Fatalf("after deleting an archive: %+v", gone.Earlier)
	}

	// What a job interrupted by a restart of this process left is removed at
	// the next start, judged by its role and the records and not by its name;
	// a recorded copy is a database of its own and stays.
	ctx := context.Background()
	docker := live.s.modules.docker
	run, orphan := "jd-pitr-run-"+live.name, live.name+"-orphan-pitr"
	unfinished := live.name + "-copy-of-tuesday"
	t.Cleanup(func() { _ = docker.RemoveVolume(context.Background(), run, true) })
	for volume, role := range map[string]string{
		run: pitrRoleRun, orphan: pitrRoleTool,
		unfinished + "-data": pitrRoleCopyData, unfinished + "-pitr-restore": pitrRoleRestore,
	} {
		if _, err := docker.CreateVolume(ctx, dockerx.VolumeSpec{Name: volume, Labels: pitrLabels(live.name, role)}); err != nil {
			t.Fatal(err)
		}
	}
	for _, spec := range []dockerx.ContainerSpec{
		{Name: live.name + "-left-behind", Image: "postgres:17-alpine", Entrypoint: []string{"true"}, Labels: pitrLabelSpecs(live.name, pitrRoleRun)},
		{Name: unfinished, Image: "postgres:17-alpine", Entrypoint: []string{"true"}, Labels: pitrLabelSpecs(live.name, pitrRoleCopy),
			Mounts: []dockerx.MountSpec{
				{Type: "volume", Source: unfinished + "-data", Target: dbx.PostgresDataDir},
				{Type: "volume", Source: unfinished + "-pitr-restore", Target: dbx.WALGMount},
			}},
	} {
		if _, err := docker.Create(ctx, spec, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := live.s.insertPITRRestore(ctx, &pitrRestore{Source: live.name, Container: unfinished, State: "recovering"}); err != nil {
		t.Fatal(err)
	}
	live.s.sweepPITR(ctx)
	for _, name := range []string{live.name + "-left-behind", unfinished} {
		if _, err := docker.Inspect(ctx, name); err == nil {
			t.Errorf("%s was left by the sweep", name)
		}
	}
	for _, name := range []string{run, orphan, unfinished + "-data", unfinished + "-pitr-restore"} {
		if exists, _ := docker.VolumeExists(ctx, name); exists {
			t.Errorf("the volume %s was left by the sweep", name)
		}
	}
	if _, err := docker.Inspect(ctx, atPoint.Container); err != nil {
		t.Errorf("the sweep removed the recorded copy %s: %v", atPoint.Container, err)
	}
	if exists, _ := docker.VolumeExists(ctx, atPoint.Container+"-data"); !exists {
		t.Errorf("the sweep removed the recorded copy's data")
	}

	// Every request was recorded when it was answered, and every job when it
	// ended.
	for action, status := range map[string]int{
		"database.pitr.enable": http.StatusAccepted, "database.pitr.enable.finish": http.StatusOK,
		"database.pitr.restore_point": http.StatusCreated,
		"database.pitr.restore":       http.StatusAccepted, "database.pitr.restore.finish": http.StatusOK,
		"database.pitr.switch": http.StatusAccepted, "database.pitr.switch.finish": http.StatusOK,
		"database.pitr.disable": http.StatusAccepted, "database.pitr.disable.finish": http.StatusOK,
		"database.pitr.archive.delete": http.StatusAccepted, "database.pitr.archive.delete.finish": http.StatusOK,
	} {
		target, _, got := auditEntry(t, live.s, action)
		if got != status || target != live.name {
			t.Errorf("%s audited for %q with %d, want %s and %d", action, target, got, live.name, status)
		}
	}
}

func TestLivePointInTimeRecoveryIntoS3(t *testing.T) {
	raw := os.Getenv("JD_TEST_PITR_S3")
	if raw == "" {
		t.Skip("set JD_TEST_PITR_S3=http://key:secret@host:port/bucket to recover through an S3 store")
	}
	store, err := url.Parse(raw)
	if err != nil || store.User == nil {
		t.Fatalf("JD_TEST_PITR_S3 is not http://key:secret@host:port/bucket: %v", err)
	}
	secret, _ := store.User.Password()
	live := newPITRLive(t, fmt.Sprintf("jdt-pitr-postgres-s3-%d", time.Now().Unix()))
	live.exec(live.conn, `CREATE TABLE jdt_pitr_rows(id int PRIMARY KEY, at timestamptz DEFAULT now())`,
		`INSERT INTO jdt_pitr_rows(id) SELECT generate_series(1, 100)`)
	setup, _ := json.Marshal(map[string]any{
		"targetKind": "s3", "schedule": "", "retentionDays": 7,
		"target":  map[string]string{"bucket": strings.Trim(store.Path, "/"), "endpoint": store.Scheme + "://" + store.Host, "prefix": live.name},
		"secrets": map[string]string{"accessKeyId": store.User.Username(), "secretAccessKey": secret},
	})
	live.job(live.do(http.MethodPost, "/pitr", string(setup)))
	moment := live.writeAndNote()
	archive := live.reading().Archive

	// The server stops; its archive is still there to recover from.
	grace := 30
	if err := live.s.modules.docker.Lifecycle(context.Background(), live.name, dockerx.ActionStop, &grace); err != nil {
		t.Fatal(err)
	}
	stopped := live.reading()
	if stopped.Running || stopped.Archive == nil || stopped.Archive.ID != archive.ID {
		t.Fatalf("a stopped server's archive: %+v", stopped)
	}
	_, copied := live.job(live.do(http.MethodPost, "/pitr/restore", fmt.Sprintf(`{"target":{"kind":"time","time":%q}}`, moment.Format(time.RFC3339Nano))))
	if count, highest := live.rows(copied.ConnectionID); count != 150 || highest != 150 {
		t.Fatalf("the copy recovered from S3 holds %d rows up to %d, want 150", count, highest)
	}
	// Recovering to a moment past everything archived is recovering to the end.
	_, latest := live.job(live.do(http.MethodPost, "/pitr/restore", fmt.Sprintf(`{"target":{"kind":"time","time":%q}}`, time.Now().UTC().Format(time.RFC3339Nano))))
	if count, _ := live.rows(latest.ConnectionID); count != 300 {
		t.Fatalf("the copy recovered past the end holds %d rows, want all 300", count)
	}

	if err := live.s.modules.docker.Lifecycle(context.Background(), live.name, dockerx.ActionStart, nil); err != nil {
		t.Fatal(err)
	}
	if err := live.s.waitForPostgres(context.Background(), live.name, live.s.pitrServerNamed(context.Background(), live.name).account, time.Minute); err != nil {
		t.Fatal(err)
	}
	live.job(live.do(http.MethodDelete, "/pitr", `{"deleteArchive":true}`))
	if off := live.reading(); off.Archive != nil || len(off.Earlier) != 0 {
		t.Fatalf("after turning off and deleting the archive: %+v", off)
	}
	// The bucket's keys reached WAL-G and nothing that is kept.
	var leaked int
	if err := live.s.Store.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE detail LIKE ?`, "%"+secret+"%").Scan(&leaked); err != nil || leaked != 0 {
		t.Errorf("the secret key is in %d audit entries (%v)", leaked, err)
	}
	for _, job := range live.s.modules.jobs.List() {
		_, lines, _ := live.s.modules.jobs.Get(job.ID)
		if text := fmt.Sprintf("%+v %+v", job, lines); strings.Contains(text, secret) {
			t.Errorf("the job %s printed the secret key", job.Title)
		}
	}
}
