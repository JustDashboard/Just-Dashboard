package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// Data-aware rollback against a real PostgreSQL, end to end through the real
// routes and engine: a database the dashboard provisioned and linked, two
// releases activated around a change to its data, and a copy of the database
// as of the first release restored beside production. The copy runs on the
// deployment's own network under a name of its own, answers an application
// there with its own password, holds the rows the first release went live
// with while production holds more, and production is untouched. It is kept
// through the sweep at start while its lifetime runs, and removed by its
// owner when that ends, leaving nothing carrying its label. A rollback to the
// first release that asks for the copy too restores it once the rollback is
// over. Everything is the test's and is removed with it.

// The backup is a backup job's native dump, taken before each release.
func TestLiveDataCopyRestoresTheDatabaseAsOfARelease(t *testing.T) {
	live := startLiveRehearsal(t, livePostgres)
	conn := live.database(fmt.Sprintf("jd-datacopy-pg-%d", live.stamp))
	live.seed(conn.ID)
	dump := live.dumper(conn.ID)
	dump()
	live.copyAsOfARelease(conn.ID, deploy.DataPointDump, dump)
}

// dumper is one backup job holding the database's native dump, run each
// time the function is called.
func (l *liveRehearsal) dumper(connectionID int64) func() {
	l.t.Helper()
	source := filepath.Join(l.s.Cfg.FileRoots[0], "app-files")
	if err := os.MkdirAll(source, 0o700); err != nil {
		l.t.Fatal(err)
	}
	job, err := l.s.modules.backupStore.Create(l.ctx, &backups.Job{
		Name: "datacopy-dump", Sources: []string{source}, DatabaseDumps: []int64{connectionID},
		TargetKind: backups.TargetLocal, Target: backups.TargetConfig{Path: filepath.Join(l.s.Cfg.FileRoots[0], "archives")},
	}, nil)
	if err != nil {
		l.t.Fatal(err)
	}
	return func() {
		l.t.Helper()
		// Runs are told apart by the second they started in.
		time.Sleep(1100 * time.Millisecond)
		if run, err := l.s.modules.backupRunner.Execute(l.ctx, job.ID, "test"); err != nil || run.Status != backups.StatusSuccess {
			l.t.Fatalf("backup run = %+v, %v", run, err)
		}
	}
}

// The same against MariaDB: a copy restored from its dump with the image's
// own client, the application's account given its own password through the
// client, and the comparison read through the MySQL readers.
func TestLiveDataCopyRestoresAMariaDBDump(t *testing.T) {
	live := startLiveRehearsal(t, liveMariaDB)
	conn := live.database(fmt.Sprintf("jd-datacopy-maria-%d", live.stamp))
	live.seed(conn.ID)
	dump := live.dumper(conn.ID)
	dump()
	live.copyAsOfARelease(conn.ID, deploy.DataPointDump, dump)
}

// The backup is the server's point-in-time archive, written by the WAL-G
// release JD_TEST_WALG names into a directory: the point is the first
// release's activation itself, and the copy is recovered to that moment.
func TestLiveDataCopyRecoversTheMomentAReleaseWentLive(t *testing.T) {
	pitrLiveTool(t)
	engine := livePostgres
	engine.version = "17"
	live := startLiveRehearsal(t, engine)
	name := fmt.Sprintf("jd-datacopy-pitr-%d", live.stamp)
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
	live.s.modules.dbs.Close(conn.ID)
	live.seed(conn.ID)
	pitr.archiveNow()
	live.copyAsOfARelease(conn.ID, deploy.DataPointPITR, pitr.archiveNow)
}

// copyAsOfARelease deploys two releases around a change to the database's
// rows and restores a copy as of the first. between runs after the change
// and before the second release: a dump that covers it, or archiving the
// segment the change is in.
func (l *liveRehearsal) copyAsOfARelease(connectionID int64, wantKind string, between func()) {
	t, s, admin := l.t, l.s, l.admin
	t.Helper()
	owner := s.modules.dataCopies
	owner.every = time.Second
	owner.Start(l.ctx)
	t.Cleanup(owner.Stop)

	image := provisionTemplates[l.engine.template].image
	projectID, environmentID := liveRehearsalProject(t, admin, fmt.Sprintf("datacopy-live-%d", l.stamp%1_000_000), image, connectionID, "true")
	t.Cleanup(func() {
		removeEnvironmentArtifacts(t, environmentID)
		removeLiveEnvironment(s, environmentID)
		cleanup, stop := context.WithTimeout(context.Background(), 2*time.Minute)
		defer stop()
		if err := s.modules.deployDatabases.RemoveRuntimeNetworks(cleanup, environmentID); err != nil {
			t.Errorf("the environment's network could not be removed: %v", err)
		}
		liveDataCopiesLeftNothing(t, environmentID)
	})
	base := fmt.Sprintf("/api/v1/deploy/%d/environments/%d", projectID, environmentID)
	deployOnce := func() deploy.RunSnapshot {
		snapshot := liveRehearsalRun(t, admin, projectID, environmentID, "/runs", `{"operation":"deploy"}`)
		if snapshot.Run.State != deploy.RunSucceeded {
			t.Fatalf("deploy = %s %s: %s", snapshot.Run.State, snapshot.Run.TerminalCode, snapshot.Run.TerminalReason)
		}
		return snapshot
	}
	releases := func() map[int64]deploy.Release {
		res := admin.do(http.MethodGet, base+"/releases", "", nil)
		var list []deploy.Release
		if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil || res.Code != http.StatusOK {
			t.Fatalf("releases = %d %s", res.Code, res.Body.String())
		}
		out := map[int64]deploy.Release{}
		for _, release := range list {
			out[release.Number] = release
		}
		return out
	}
	rows := func(id int64) int {
		pool, _, err := s.dbPool(l.ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		var count int
		if err := pool.QueryRowContext(l.ctx, `SELECT COUNT(*) FROM items`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}

	first := deployOnce()
	if transcript := liveTranscript(t, s, first.Run.ID); !strings.Contains(transcript, "Data point:") {
		t.Fatalf("the first release's transcript records no data point:\n%s", transcript)
	}
	asWent := rows(connectionID)
	time.Sleep(1100 * time.Millisecond)
	pool, _, err := s.dbPool(l.ctx, connectionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.ExecContext(l.ctx, `INSERT INTO items(name) VALUES ('after-1'), ('after-2'), ('after-3'), ('after-4'), ('after-5')`); err != nil {
		t.Fatal(err)
	}
	between()
	deployOnce()
	now := rows(connectionID)
	if now != asWent+5 {
		t.Fatalf("production holds %d rows, want %d", now, asWent+5)
	}

	listed := releases()
	one, two := listed[1], listed[2]
	if len(one.DataPoints) != 1 || one.DataPoints[0].Kind != wantKind || one.DataCopy == nil || !one.DataCopy.Eligible {
		t.Fatalf("release 1's data point = %+v, %+v", one.DataPoints, one.DataCopy)
	}
	if len(two.DataPoints) != 1 || two.DataPoints[0].Kind != wantKind || !two.DataPoints[0].At.After(*one.DataPoints[0].At) {
		t.Fatalf("release 2's data point = %+v (release 1's %+v)", two.DataPoints, one.DataPoints)
	}
	compared := admin.do(http.MethodGet, base+fmt.Sprintf("/releases/%d/comparison?from=%d", two.ID, one.ID), "", nil)
	if compared.Code != http.StatusOK || !strings.Contains(compared.Body.String(), `"dataPoint":true`) {
		t.Fatalf("comparison = %d %s", compared.Code, compared.Body.String())
	}
	t.Logf("release 1 data point %s at %s, release 2 at %s", one.DataPoints[0].Kind, one.DataPoints[0].At, two.DataPoints[0].At)

	// A copy as of release 1.
	copy := l.checkCopy(base, l.askForCopy(base, one.ID, 0), connectionID, environmentID, asWent, now)

	// The sweep at start keeps a copy within its lifetime.
	if err := s.modules.rehearser.RemoveOrphanedRehearsals(l.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.modules.docker.Inspect(l.ctx, copy.Container); err != nil {
		t.Fatalf("the sweep at start removed a copy within its lifetime: %v", err)
	}
	// Once its lifetime is over its owner removes it, and nothing carrying
	// its label is left.
	if _, err := s.Store.DB.Exec(`UPDATE deploy_data_copies SET expires_at = ? WHERE id = ?`, time.Now().Add(-time.Second).Unix(), copy.ID); err != nil {
		t.Fatal(err)
	}
	owner.Poke()
	expired := l.waitForCopy(base, copy.ID, deploy.DataCopyRemoved)
	if !strings.Contains(expired.Reason, "expired") {
		t.Fatalf("the expired copy's reason = %q", expired.Reason)
	}
	liveDataCopyLeftNothing(t, copy.ID)
	t.Logf("the copy expired and was removed: %s", expired.Reason)

	// A rollback to release 1 that asks for its data too: the copy waits for
	// the rollback, then is restored.
	rollback := admin.do(http.MethodPost, base+"/rollback", fmt.Sprintf(`{"releaseId":%d}`, one.ID), nil)
	var run deploy.EngineRun
	if err := json.Unmarshal(rollback.Body.Bytes(), &run); err != nil || rollback.Code != http.StatusAccepted {
		t.Fatalf("rollback = %d %s", rollback.Code, rollback.Body.String())
	}
	waiting := l.askForCopy(base, one.ID, run.ID)
	if waiting.AfterRunID != run.ID {
		t.Fatalf("the copy asked for with the rollback = %+v", waiting)
	}
	after := l.waitForCopy(base, waiting.ID, deploy.DataCopyReady)
	finished, err := s.modules.deployRuns.Run(l.ctx, run.ID)
	if err != nil || finished.State != deploy.RunSucceeded || finished.EndedAt == nil || after.ReadyAt == nil || after.ReadyAt.Before(*finished.EndedAt) {
		t.Fatalf("the copy was ready at %v and the rollback %+v, %v", after.ReadyAt, finished, err)
	}
	listedForRun := admin.do(http.MethodGet, base+fmt.Sprintf("/data-copies?run=%d", run.ID), "", nil)
	if !strings.Contains(listedForRun.Body.String(), fmt.Sprintf(`"id":%d`, waiting.ID)) {
		t.Fatalf("the rollback's run does not list its copy: %s", listedForRun.Body.String())
	}
	l.checkCopy(base, after, connectionID, environmentID, asWent, rows(connectionID))
	if res := admin.do(http.MethodDelete, base+fmt.Sprintf("/data-copies/%d", after.ID), "", nil); res.Code != http.StatusOK {
		t.Fatalf("remove = %d %s", res.Code, res.Body.String())
	}
	liveDataCopyLeftNothing(t, after.ID)
}

func (l *liveRehearsal) askForCopy(base string, releaseID, afterRunID int64) deploy.DataCopy {
	l.t.Helper()
	body := `{}`
	if afterRunID > 0 {
		body = fmt.Sprintf(`{"afterRunId":%d}`, afterRunID)
	}
	res := l.admin.do(http.MethodPost, base+fmt.Sprintf("/releases/%d/data-copies", releaseID), body, nil)
	var copies []deploy.DataCopy
	if err := json.Unmarshal(res.Body.Bytes(), &copies); err != nil || res.Code != http.StatusAccepted || len(copies) != 1 {
		l.t.Fatalf("ask for a copy = %d %s", res.Code, res.Body.String())
	}
	return copies[0]
}

func (l *liveRehearsal) waitForCopy(base string, copyID int64, want string) deploy.DataCopy {
	l.t.Helper()
	deadline := time.Now().Add(8 * time.Minute)
	for {
		res := l.admin.do(http.MethodGet, base+"/data-copies", "", nil)
		var copies []deploy.DataCopy
		_ = json.Unmarshal(res.Body.Bytes(), &copies)
		for _, copy := range copies {
			if copy.ID != copyID {
				continue
			}
			if copy.State == want {
				return copy
			}
			if !deploy.DataCopyActive(copy.State) {
				l.t.Fatalf("copy %d ended %s: %s", copyID, copy.State, copy.Reason)
			}
		}
		if time.Now().After(deadline) {
			l.t.Fatalf("copy %d never became %s: %s", copyID, want, res.Body.String())
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// checkCopy reads a ready copy every way it is offered: its container, its
// address from an application container on the deployment's network, and
// the comparison beside production.
func (l *liveRehearsal) checkCopy(base string, copy deploy.DataCopy, connectionID, environmentID int64, asWent, production int) deploy.DataCopy {
	t, s, admin := l.t, l.s, l.admin
	t.Helper()
	if copy.State != deploy.DataCopyReady {
		copy = l.waitForCopy(base, copy.ID, deploy.DataCopyReady)
	}
	if copy.Host != dataCopyHost(connectionID, copy.ID) || copy.ExpiresAt == nil || copy.ExpiresAt.Sub(*copy.ReadyAt) != deploy.DataCopyLifetime {
		t.Fatalf("the ready copy = %+v", copy)
	}
	network, err := s.modules.deployDatabases.network(l.ctx, environmentID, false)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := s.modules.docker.Inspect(l.ctx, copy.Container)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.NetworkList) != 1 || detail.NetworkList[0].Name != network.Name || copy.Network != network.Name {
		t.Fatalf("the copy is on %+v, want the deployment's network %s alone", detail.NetworkList, network.Name)
	}
	aliases := strings.Join(detail.NetworkList[0].Aliases, ",")
	if !strings.Contains(aliases, copy.Host) || strings.Contains(aliases, databaseDNSName(connectionID)) {
		t.Fatalf("the copy answers to %s", aliases)
	}
	published := false
	for _, port := range detail.Ports {
		published = published || port.PublicPort != 0
	}
	if published || detail.Labels[rehearsalLabel] != dataCopyRehearsal(copy.ID) || detail.Labels[dataCopyLabel] != fmt.Sprint(copy.ID) {
		t.Fatalf("the copy's ports %+v, labels %v", detail.Ports, detail.Labels)
	}

	reveal := admin.do(http.MethodGet, base+fmt.Sprintf("/data-copies/%d/url", copy.ID), "", nil)
	var address struct {
		URL  string `json:"url"`
		Host string `json:"host"`
	}
	if err := json.Unmarshal(reveal.Body.Bytes(), &address); err != nil || reveal.Code != http.StatusOK || !strings.Contains(address.URL, "@"+copy.Host+":") {
		t.Fatalf("reveal = %d %s", reveal.Code, reveal.Body.String())
	}
	_, productionDSN, err := s.dbConnRow(l.ctx, connectionID)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := parseLiveDSN(productionDSN); err == nil && info != "" && strings.Contains(address.URL, ":"+info+"@") {
		t.Fatal("the copy's address carries production's password")
	}

	// An application on the deployment's network reaches the copy by its
	// address, and the copy holds what production held when the release went
	// live.
	image := provisionTemplates[l.engine.template].image
	read := exec.Command("docker", "run", "--rm", "--network", network.Name, "--entrypoint", "psql", image,
		address.URL, "-tAc", "SELECT COUNT(*) FROM items")
	if l.engine.template == "mariadb" {
		read = exec.Command("docker", "run", "--rm", "--network", network.Name, "-e", "DATABASE_URL="+address.URL, "--entrypoint", "sh", image,
			"-c", liveMariaDBClient+`"SELECT COUNT(*) FROM items" -N`)
	}
	out, err := read.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != fmt.Sprint(asWent) {
		t.Fatalf("an application on the deployment's network read %q from the copy, want %d: %v", out, asWent, err)
	}

	compared := admin.do(http.MethodGet, base+fmt.Sprintf("/data-copies/%d/comparison", copy.ID), "", nil)
	var comparison DataCopyComparison
	if err := json.Unmarshal(compared.Body.Bytes(), &comparison); err != nil || compared.Code != http.StatusOK || comparison.CopyError != "" || comparison.LiveError != "" {
		t.Fatalf("comparison = %d %s", compared.Code, compared.Body.String())
	}
	found := false
	for _, table := range comparison.Tables {
		if table.Name == "items" {
			found = true
			if table.CopyRows == nil || table.LiveRows == nil || *table.CopyRows != int64(asWent) || *table.LiveRows != int64(production) || table.Estimated {
				t.Fatalf("items in the comparison = %+v, want %d in the copy and %d live", table, asWent, production)
			}
		}
	}
	if !found {
		t.Fatalf("the comparison has no items table: %+v", comparison.Tables)
	}
	t.Logf("copy %d of %s: %d rows beside production's %d, at %s on %s, restored in %d ms from %s",
		copy.ID, copy.Name, asWent, production, copy.Host, copy.Network, copy.RestoreMS, copy.Source.Kind)

	// The reconciler that joins an environment's applications to its network
	// leaves a rehearsal's containers where they are: one on a network of
	// its own stays alone there.
	name := fmt.Sprintf("jd-e%d-run9-rehearsal", environmentID)
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), time.Minute)
		defer stop()
		_ = s.modules.docker.RemoveContainer(cleanup, name+"-db", true, true)
		_ = s.modules.docker.RemoveNetwork(cleanup, name)
	})
	if _, err := s.modules.docker.Inspect(l.ctx, name+"-db"); err != nil {
		labels := map[string]string{"io.just-dashboard.managed": "true", "io.just-dashboard.environment-id": fmt.Sprint(environmentID), rehearsalLabel: "9"}
		if _, err := s.modules.docker.CreateNetwork(l.ctx, dockerx.NetworkSpec{Name: name, Driver: "bridge", Internal: true, Labels: labels}); err != nil {
			t.Fatal(err)
		}
		specLabels := []dockerx.LabelSpec{}
		for key, value := range labels {
			specLabels = append(specLabels, dockerx.LabelSpec{Name: key, Value: value})
		}
		if _, err := s.modules.docker.Create(l.ctx, dockerx.ContainerSpec{Name: name + "-db", Image: image, Entrypoint: []string{"sleep"}, Command: []string{"600"},
			Labels: specLabels, Networks: []string{name}, Start: true, Pull: "missing"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.modules.deployDatabases.Reconcile(l.ctx)
	rehearsal, err := s.modules.docker.Inspect(l.ctx, name+"-db")
	if err != nil {
		t.Fatal(err)
	}
	if len(rehearsal.NetworkList) != 1 || rehearsal.NetworkList[0].Name != name {
		t.Fatalf("a rehearsal's copy was joined to %+v", rehearsal.NetworkList)
	}
	if again, err := s.modules.docker.Inspect(l.ctx, copy.Container); err != nil || len(again.NetworkList) != 1 {
		t.Fatalf("the data copy after a reconcile = %+v, %v", again, err)
	}
	return copy
}

// parseLiveDSN is the password a saved connection string carries.
func parseLiveDSN(dsn string) (string, error) {
	at := strings.LastIndex(dsn, "@")
	scheme := strings.Index(dsn, "://")
	if at < 0 || scheme < 0 {
		return "", nil
	}
	user, password, _ := strings.Cut(dsn[scheme+3:at], ":")
	_ = user
	return password, nil
}

// liveDataCopyLeftNothing checks the daemon for anything one copy made.
func liveDataCopyLeftNothing(t *testing.T, copyID int64) {
	t.Helper()
	filter := "label=" + rehearsalLabel + "=" + dataCopyRehearsal(copyID)
	for _, args := range [][]string{
		{"ps", "-aq", "--filter", filter},
		{"volume", "ls", "-q", "--filter", filter},
	} {
		out, err := exec.Command("docker", args...).Output()
		if err != nil || strings.TrimSpace(string(out)) != "" {
			t.Fatalf("copy %d left something behind (docker %s): %q %v", copyID, strings.Join(args, " "), out, err)
		}
	}
	// What the label alone would not find: the container by its name.
	if out, _ := exec.Command("docker", "ps", "-aq", "--filter", fmt.Sprintf("name=-copy%d-db", copyID)).Output(); strings.TrimSpace(string(out)) != "" {
		t.Fatalf("copy %d's container is still there", copyID)
	}
}

// liveDataCopiesLeftNothing checks the daemon for any copy of an
// environment once its network is removed.
func liveDataCopiesLeftNothing(t *testing.T, environmentID int64) {
	t.Helper()
	out, err := exec.Command("docker", "ps", "-aq", "--filter", "label="+dataCopyLabel, "--filter", fmt.Sprintf("label=io.just-dashboard.environment-id=%d", environmentID)).Output()
	if err != nil || strings.TrimSpace(string(out)) != "" {
		t.Errorf("the environment's data copies were left: %q %v", out, err)
	}
}
