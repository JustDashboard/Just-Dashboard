package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

// dataRollbackFixture is a production environment linking the databases
// named, with nothing else about them yet.
func dataRollbackFixture(t *testing.T, s *Server, links ...string) (projectID, environmentID int64, connections map[string]int64) {
	t.Helper()
	projectID, environmentID, _ = insertDeploymentConfigurationAPI(t, s)
	now := time.Now().UTC().Unix()
	if _, err := s.Store.DB.Exec(`INSERT INTO deploy_database_networks(environment_id, network_name, owner_key, created_at) VALUES(?, ?, 'owner', ?)`,
		environmentID, fmt.Sprintf("jd-e%d-db-test", environmentID), now); err != nil {
		t.Fatal(err)
	}
	connections = map[string]int64{}
	for _, link := range links {
		name, driver, _ := strings.Cut(link, ":")
		result, err := s.Store.DB.Exec(`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES(?, ?, 'sealed', ?)`, name, driver, now)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := result.LastInsertId()
		connections[name] = id
		if _, err := s.Store.DB.Exec(`INSERT INTO deploy_database_bindings(environment_id, connection_id, container_name, status) VALUES(?, ?, ?, 'connected')`,
			environmentID, id, name); err != nil {
			t.Fatal(err)
		}
	}
	return projectID, environmentID, connections
}

// dumpOf records a successful backup run whose archive holds the
// connection's dump, started at.
func dumpOf(t *testing.T, s *Server, connectionID int64, at time.Time) (runID, jobID int64) {
	t.Helper()
	result, err := s.Store.DB.Exec(`INSERT INTO backup_jobs(name, sources, target_kind, created_at) VALUES(?, '[]', 'local', ?)`,
		fmt.Sprintf("dumps-%d-%d", connectionID, at.UnixNano()), at.Unix())
	if err != nil {
		t.Fatal(err)
	}
	jobID, _ = result.LastInsertId()
	manifest, _ := json.Marshal(map[string]any{
		"version": 1, "complete": true, "artifactDigest": "sha256:" + strings.Repeat("a", 64),
		"databaseDumps": []map[string]any{{"connectionId": connectionID, "digest": "sha256:" + strings.Repeat("b", 64), "archivePath": "databases/x.dump", "bytes": 1024, "method": "pg_dump"}},
	})
	result, err = s.Store.DB.Exec(`INSERT INTO backup_runs(job_id, started_at, ended_at, status, artifact, manifest_json) VALUES(?, ?, ?, 'success', 'archive.tar', ?)`,
		jobID, at.Unix(), at.Unix(), string(manifest))
	if err != nil {
		t.Fatal(err)
	}
	runID, _ = result.LastInsertId()
	return runID, jobID
}

// The facts a release's data point is decided from, read from the store
// alone: a server archiving into a point-in-time archive whose base backup
// predates the release, one with only dumps, one engine no copy is restored
// for — and an environment that links nothing.
func TestReleaseDataFactsAreReadFromTheLinks(t *testing.T) {
	s := testServer(t)
	_, environmentID, connections := dataRollbackFixture(t, s, "orders-db:postgres", "billing:mysql", "cache:redis")
	now := time.Now().UTC().Truncate(time.Second)
	from := now.Add(-6 * time.Hour)
	reading, _ := json.Marshal(pitrReading{Window: windowFrom(from)})
	if _, err := s.Store.DB.Exec(`INSERT INTO db_wal_archives(container, connection_id, stream, target_kind, public_key, private_key_enc, created_at, reading)
		VALUES('orders-db', ?, 'orders-db-stream', 'local', 'age1', 'sealed', ?, ?)`, connections["orders-db"], from.Unix(), string(reading)); err != nil {
		t.Fatal(err)
	}
	runID, jobID := dumpOf(t, s, connections["billing"], now.Add(-time.Hour))
	facts, err := releaseDataFacts{server: s}.ReleaseDataFacts(context.Background(), environmentID, now)
	if err != nil || len(facts) != 3 {
		t.Fatalf("facts = %+v, %v", facts, err)
	}
	points := map[string]deploy.ReleaseDataPoint{}
	for _, fact := range facts {
		points[fact.Name] = deploy.DecideDataPoint(fact, now)
	}
	if point := points["orders-db"]; point.Kind != deploy.DataPointPITR || !point.At.Equal(now) || point.ArchiveID == 0 {
		t.Fatalf("the archived database = %+v", point)
	}
	if point := points["billing"]; point.Kind != deploy.DataPointDump || point.BackupRunID != runID || point.BackupJobID != jobID {
		t.Fatalf("the dumped database = %+v", point)
	}
	if point := points["cache"]; point.Kind != deploy.DataPointNone || !strings.Contains(point.Reason, "redis") {
		t.Fatalf("the cache = %+v", point)
	}

	other := testServer(t)
	_, bare, _ := dataRollbackFixture(t, other)
	if facts, err := (releaseDataFacts{server: other}).ReleaseDataFacts(context.Background(), bare, now); err != nil || len(facts) != 0 {
		t.Fatalf("an environment with no link = %+v, %v", facts, err)
	}
}

// insertDataRollbackRelease adds a release of the environment in state.
func insertDataRollbackRelease(t *testing.T, s *Server, projectID, environmentID int64, number int, state string) int64 {
	t.Helper()
	result, err := s.Store.DB.Exec(
		"INSERT INTO deploy_releases(project_id, environment_id, release_number, state, plan_revision, config_digest, variables_digest, strategy, created_at) VALUES(?, ?, ?, ?, 1, ?, ?, 'stop_first', ?)",
		projectID, environmentID, number, state, "sha256:"+strings.Repeat("c", 64), "sha256:"+strings.Repeat("d", 64), time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	return id
}

// The routes: anyone who reads the environment lists its copies and never
// their sign-in; asking for one, reading its address, keeping it and
// removing it are an administrator's, each audited, and its address is
// handed out only once it is ready.
func TestDataCopyRoutesAreAdministrativeAndAudited(t *testing.T) {
	s := testServer(t)
	routes := s.Routes()
	admin := &client{t: t, h: routes, cookie: signInAs(t, s, "data-copy-admin", auth.RoleAdmin)}
	limited := &client{t: t, h: routes, cookie: signInAs(t, s, "data-copy-operator", auth.RoleLimited)}
	reader := &client{t: t, h: routes, cookie: signInAs(t, s, "data-copy-reader", auth.RoleReadOnly)}
	projectID, environmentID, connections := dataRollbackFixture(t, s, "orders-db:postgres")
	retained := insertDataRollbackRelease(t, s, projectID, environmentID, 1, "retained")
	unrecorded := insertDataRollbackRelease(t, s, projectID, environmentID, 2, "live")
	at := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	runID, jobID := dumpOf(t, s, connections["orders-db"], at)
	if err := s.modules.deployRuns.RecordReleaseDataPoints(context.Background(), retained, []deploy.ReleaseDataPoint{
		deploy.DecideDataPoint(deploy.DataPointFacts{ConnectionID: connections["orders-db"], Name: "orders-db", Engine: "postgres", DumpRunID: runID, DumpJobID: jobID, DumpAt: at}, at.Add(time.Minute)),
	}); err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/api/v1/deploy/%d/environments/%d", projectID, environmentID)

	// The release list says which release can bring its data back.
	var releases []deploy.Release
	if res := reader.do(http.MethodGet, base+"/releases", "", nil); res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &releases) != nil {
		t.Fatalf("releases = %d %s", res.Code, res.Body.String())
	}
	for _, release := range releases {
		want := release.ID == retained
		if release.DataCopy == nil || release.DataCopy.Eligible != want {
			t.Fatalf("release %d's eligibility = %+v", release.Number, release.DataCopy)
		}
	}

	ask := base + fmt.Sprintf("/releases/%d/data-copies", retained)
	for _, who := range []*client{reader, limited} {
		if res := who.do(http.MethodPost, ask, `{}`, nil); res.Code != http.StatusForbidden {
			t.Fatalf("a copy asked for without system.admin = %d %s", res.Code, res.Body.String())
		}
	}
	// A release with no point is refused with the reason, and so is a copy
	// waiting for another environment's run.
	if res := admin.do(http.MethodPost, base+fmt.Sprintf("/releases/%d/data-copies", unrecorded), `{}`, nil); res.Code != http.StatusConflict ||
		!strings.Contains(res.Body.String(), "data_copy_unavailable") || !strings.Contains(res.Body.String(), "no data point was recorded") {
		t.Fatalf("a release without a point = %d %s", res.Code, res.Body.String())
	}
	if res := admin.do(http.MethodPost, ask, `{"afterRunId":424242}`, nil); res.Code != http.StatusConflict {
		t.Fatalf("a copy waiting for a run that is not the environment's = %d %s", res.Code, res.Body.String())
	}
	var copies []deploy.DataCopy
	if res := admin.do(http.MethodPost, ask, `{}`, nil); res.Code != http.StatusAccepted || json.Unmarshal(res.Body.Bytes(), &copies) != nil || len(copies) != 1 ||
		copies[0].State != deploy.DataCopyWaiting || copies[0].Source.BackupRunID != runID {
		t.Fatalf("ask = %d %s", res.Code, res.Body.String())
	}
	if res := admin.do(http.MethodPost, ask, `{}`, nil); res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "data_copy_exists") {
		t.Fatalf("a second copy while the first waits = %d %s", res.Code, res.Body.String())
	}
	var asked string
	if err := s.Store.DB.QueryRow(`SELECT detail FROM audit_log WHERE action = 'deploy.release.data_copy' AND success = 1`).Scan(&asked); err != nil || !strings.Contains(asked, `"copies":[`) {
		t.Fatalf("the request's audit = %q, %v", asked, err)
	}
	copy := copies[0]
	copyPath := base + fmt.Sprintf("/data-copies/%d", copy.ID)
	if res := admin.do(http.MethodGet, copyPath+"/url", "", nil); res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "data_copy_not_ready") {
		t.Fatalf("the address of a copy still waiting = %d %s", res.Code, res.Body.String())
	}
	// Restored: what the owner records once the copy runs.
	if claimed, err := s.modules.deployRuns.ClaimDataCopy(context.Background(), copy.ID); err != nil || !claimed {
		t.Fatalf("claim = %v %v", claimed, err)
	}
	secret, _ := json.Marshal(dataCopySecret{User: "app", Password: "made-for-the-copy", URL: "postgres://app:made-for-the-copy@db-1-copy-1.jd.internal:5432/app"})
	sealed, err := s.Sealer.Seal(string(secret))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.modules.deployRuns.DataCopyReady(context.Background(), copy.ID, deploy.DataCopyReadiness{
		Container: "jd-e1-copy1-db", Network: "jd-e1-db-test", Host: dataCopyHost(connections["orders-db"], copy.ID), Port: 5432,
		Database: "app", SecretSealed: sealed,
	}); err != nil {
		t.Fatal(err)
	}

	list := reader.do(http.MethodGet, base+"/data-copies", "", nil)
	if list.Code != http.StatusOK || strings.Contains(list.Body.String(), "made-for-the-copy") || !strings.Contains(list.Body.String(), `"state":"ready"`) {
		t.Fatalf("list = %d %s", list.Code, list.Body.String())
	}
	if res := reader.do(http.MethodGet, base+"/data-copies?run=999", "", nil); res.Code != http.StatusOK || strings.TrimSpace(res.Body.String()) != "[]" {
		t.Fatalf("another run's copies = %d %s", res.Code, res.Body.String())
	}
	for _, who := range []*client{reader, limited} {
		for _, call := range [][2]string{{http.MethodGet, copyPath + "/url"}, {http.MethodPost, copyPath + "/extend"}, {http.MethodDelete, copyPath}, {http.MethodGet, copyPath + "/comparison"}} {
			if res := who.do(call[0], call[1], `{"hours":1}`, nil); res.Code != http.StatusForbidden {
				t.Fatalf("%s %s without system.admin = %d", call[0], call[1], res.Code)
			}
		}
	}
	reveal := admin.do(http.MethodGet, copyPath+"/url", "", nil)
	if reveal.Code != http.StatusOK || !strings.Contains(reveal.Body.String(), "made-for-the-copy") {
		t.Fatalf("reveal = %d %s", reveal.Code, reveal.Body.String())
	}
	var detail string
	if err := s.Store.DB.QueryRow(`SELECT detail FROM audit_log WHERE action = 'deploy.data_copy.reveal'`).Scan(&detail); err != nil || strings.Contains(detail, "made-for-the-copy") {
		t.Fatalf("the reveal's audit = %q, %v", detail, err)
	}
	if res := admin.do(http.MethodPost, copyPath+"/extend", `{"hours":30}`, nil); res.Code != http.StatusBadRequest {
		t.Fatalf("thirty hours longer = %d %s", res.Code, res.Body.String())
	}
	var extended deploy.DataCopy
	if res := admin.do(http.MethodPost, copyPath+"/extend", `{"hours":1}`, nil); res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &extended) != nil ||
		extended.ExpiresAt == nil || extended.ExpiresAt.Before(time.Now().Add(deploy.DataCopyLifetime+50*time.Minute)) {
		t.Fatalf("extend = %d %s", res.Code, res.Body.String())
	}
	if res := admin.do(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/environments/%d/data-copies/%d/url", projectID, environmentID+99, copy.ID), "", nil); res.Code != http.StatusNotFound {
		t.Fatalf("the copy through another environment = %d", res.Code)
	}
	if !s.liveDataCopies(context.Background())[dataCopyRehearsal(copy.ID)] {
		t.Fatal("the sweep at start would remove a copy within its lifetime")
	}
	var removed deploy.DataCopy
	if res := admin.do(http.MethodDelete, copyPath, "", nil); res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &removed) != nil || removed.State != deploy.DataCopyRemoved {
		t.Fatalf("remove = %d %s", res.Code, res.Body.String())
	}
	if res := admin.do(http.MethodGet, copyPath+"/url", "", nil); res.Code != http.StatusConflict {
		t.Fatalf("the address of a removed copy = %d %s", res.Code, res.Body.String())
	}
	if s.liveDataCopies(context.Background())[dataCopyRehearsal(copy.ID)] {
		t.Fatal("the sweep at start would keep a removed copy")
	}
	for _, action := range []string{"deploy.data_copy.extend", "deploy.data_copy.remove"} {
		var count int
		if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = ? AND success = 1`, action).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s audited %d times, %v", action, count, err)
		}
	}
}

// A copy's name is not a managed database's: a variable that uses one is
// refused before a release starts, with the reason.
func TestAVariableNamingADataCopyIsRefused(t *testing.T) {
	if _, err := databaseURLConnectionIDs(map[string]string{"DATABASE_URL": "postgres://app:x@" + dataCopyHost(5, 3) + ":5432/app"}); err == nil ||
		!strings.Contains(err.Error(), "production never runs against it") {
		t.Fatalf("a variable naming a copy = %v", err)
	}
	if ids, err := databaseURLConnectionIDs(map[string]string{"DATABASE_URL": "postgres://app:x@db-5.jd.internal:5432/app"}); err != nil || len(ids) != 1 || ids[0] != 5 {
		t.Fatalf("the database itself = %v, %v", ids, err)
	}
}

// A copy expires when it is ready and its time is up; one a restart caught
// being restored ends failed, so the sweep at start removes what it made.
func TestDataCopiesInterruptedByARestartEndFailed(t *testing.T) {
	s := testServer(t)
	projectID, environmentID, connections := dataRollbackFixture(t, s, "orders-db:postgres")
	release := insertDataRollbackRelease(t, s, projectID, environmentID, 1, "retained")
	at := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	runID, jobID := dumpOf(t, s, connections["orders-db"], at)
	ctx := context.Background()
	if err := s.modules.deployRuns.RecordReleaseDataPoints(ctx, release, []deploy.ReleaseDataPoint{
		deploy.DecideDataPoint(deploy.DataPointFacts{ConnectionID: connections["orders-db"], Name: "orders-db", DumpRunID: runID, DumpJobID: jobID, DumpAt: at}, at.Add(time.Minute)),
	}); err != nil {
		t.Fatal(err)
	}
	copies, err := s.modules.deployRuns.CreateDataCopies(ctx, deploy.Release{ID: release, ProjectID: projectID, EnvironmentID: environmentID, Number: 1, State: "retained"}, 0, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := s.modules.deployRuns.ClaimDataCopy(ctx, copies[0].ID); err != nil || !claimed {
		t.Fatal(claimed, err)
	}
	s.modules.dataCopies.SettleInterrupted(ctx)
	ended, err := s.modules.deployRuns.DataCopy(ctx, copies[0].ID)
	if err != nil || ended.State != deploy.DataCopyFailed || !strings.Contains(ended.Reason, "restarted") {
		t.Fatalf("an interrupted copy = %+v, %v", ended, err)
	}
	var audited int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'deploy.data_copy.finish' AND actor = 'system'`).Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("the interruption audited %d times, %v", audited, err)
	}
}

func windowFrom(from time.Time) dbx.WALWindow {
	return dbx.WALWindow{From: &from}
}
