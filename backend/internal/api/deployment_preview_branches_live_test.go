package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// Database branches against real servers: a database the dashboard
// provisioned and a backup job dumped, a production deployment linking it,
// and pull request previews of it. The preview's branch is restored on the
// preview's own database network with a sign-in of its own, the preview's
// ${{database.N.url}} resolves to it, a row written there is not in
// production and one written to production is not there, a masked column
// is masked, a fork's preview gets none, the start-up sweep keeps it, and
// removing the preview removes it and leaves nothing with its label.

func livePostgresClient(url, sql string) []string {
	return []string{"psql", url, "-v", "ON_ERROR_STOP=1", "-tAc", sql}
}

// The branch is restored from a backup job's dump.
func TestLivePreviewDatabaseBranchIsThePreviewsOwnAndGoesWithIt(t *testing.T) {
	l := startLiveRehearsal(t, livePostgres)
	livePreviewBranch(l, livePostgresClient, "dump", l.dumped)
}

func TestLivePreviewDatabaseBranchOfAMariaDBDump(t *testing.T) {
	l := startLiveRehearsal(t, liveMariaDB)
	livePreviewBranch(l, func(url, sql string) []string {
		return []string{"sh", "-c", `u=${DATABASE_URL#*://}; auth=${u%%@*}; rest=${u#*@}; host=${rest%%:*}; db=${rest#*/}; db=${db%%\?*}; ` +
			`mariadb -h "$host" -u "${auth%%:*}" -p"${auth#*:}" "$db" -N -B -e "$1"`, "client", sql}
	}, "dump", l.dumped)
}

// With JD_TEST_WALG, the branch is recovered from the server's point-in-time
// archive instead: its newest base backup and the log to its end.
func TestLivePreviewDatabaseBranchRecoversFromThePointInTimeArchive(t *testing.T) {
	pitrLiveTool(t)
	engine := livePostgres
	engine.version = "17"
	l := startLiveRehearsal(t, engine)
	livePreviewBranch(l, livePostgresClient, "pitr", func(name string) dbConnection {
		conn := l.database(name)
		archive := filepath.Join(l.s.Cfg.FileRoots[0], "pitr")
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
		l.s.mountDatabaseRoutes(router)
		pitr := &pitrLive{t: t, s: l.s, router: router, name: name, conn: conn.ID}
		pitr.job(pitr.do(http.MethodPost, "/pitr", fmt.Sprintf(`{"targetKind":"local","target":{"path":%q},"schedule":"","retentionDays":7}`, archive)))
		// Archiving restarted the server: the pool the test holds is reopened.
		l.s.modules.dbs.Close(conn.ID)
		l.seed(conn.ID)
		pitr.archiveNow()
		return conn
	})
}

// dumped provisions the database, seeds it and has a backup job dump it.
func (l *liveRehearsal) dumped(name string) dbConnection {
	conn := l.database(name)
	l.seed(conn.ID)
	l.dump(conn.ID)
	return conn
}

func livePreviewBranch(l *liveRehearsal, client func(url, sql string) []string, wantSource string, prepare func(name string) dbConnection) {
	t, engine := l.t, l.engine
	s, ctx := l.s, l.ctx
	name := fmt.Sprintf("jdt-branch-%s-%d", engine.template, l.stamp%1_000_000)
	conn := prepare(name)
	image := provisionTemplates[engine.template].image
	projectID, productionID := liveRehearsalProject(t, l.admin, name+"-app", image, conn.ID, "true")
	t.Cleanup(func() { removeEnvironmentArtifacts(t, productionID) })

	policy := func(body string) {
		t.Helper()
		path := fmt.Sprintf("/api/v1/deploy/%d/environments/%d/database-links/%d/previews", projectID, productionID, conn.ID)
		if res := l.admin.do("PUT", path, body, nil); res.Code != 200 {
			t.Fatalf("policy %s = %d %s", body, res.Code, res.Body.String())
		}
	}
	trigger, err := s.modules.deployAutomation.EnsurePullRequestTrigger(ctx, projectID, productionID, "acme/app", "main")
	if err != nil {
		t.Fatal(err)
	}
	preview := func(number int, headRepository string) int64 {
		t.Helper()
		event := deploy.ProviderEvent{
			Event: "pull_request", Action: "synchronize", Repository: "acme/app", PreviewNumber: number,
			PreviewRef: fmt.Sprintf("refs/pull/%d/head", number), Revision: strings.Repeat(strconv.Itoa(number%10), 40),
			HeadRef: "feature", HeadRepository: headRepository, Author: "dev", BaseRef: "main",
		}
		if _, err := s.modules.deployAutomation.RecordPreviewApproval(ctx, trigger, event, "rehearsal-live"); err != nil {
			t.Fatal(err)
		}
		ref, _, err := s.modules.deployAutomation.EnsurePreview(ctx, trigger, event)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			removeEnvironmentArtifacts(t, ref.EnvironmentID)
			cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			_ = s.modules.deployRuntime.RemovePreviewResources(cleanup, ref.EnvironmentID)
		})
		return ref.EnvironmentID
	}
	s.modules.previewBranches.Start(ctx)
	t.Cleanup(s.modules.previewBranches.Stop)
	ready := func(environmentID int64) deploy.PreviewBranch {
		t.Helper()
		s.modules.previewBranches.Poke()
		deadline := time.Now().Add(5 * time.Minute)
		for {
			branch, err := s.modules.deployRuns.ActivePreviewBranch(ctx, environmentID, conn.ID)
			if err != nil {
				branches, _ := s.modules.deployRuns.PreviewBranches(ctx, environmentID)
				t.Fatalf("no active branch: %v %+v", err, branches)
			}
			if branch.State == deploy.PreviewBranchReady {
				return *branch
			}
			if time.Now().After(deadline) {
				t.Fatalf("the branch never became ready: %+v", branch)
			}
			time.Sleep(time.Second)
		}
	}
	revisionOf := func(environmentID int64) int {
		var revision int
		if err := s.Store.DB.QueryRow(`SELECT desired_revision FROM deploy_environments WHERE id=?`, environmentID).Scan(&revision); err != nil {
			t.Fatal(err)
		}
		return revision
	}
	// What the preview's own container would run: the preview's networks, its
	// resolved URL, the engine's own client.
	query := func(environmentID int64, url, sql string) (string, error) {
		t.Helper()
		networks, err := s.modules.deployDatabases.NetworksForRuntime(ctx, environmentID, deploy.RuntimePlanConfig{PreviewIsolation: true}, map[string]string{"DATABASE_URL": url})
		if err != nil {
			t.Fatalf("the preview's networks: %v", err)
		}
		if len(networks) != 1 || !strings.HasPrefix(networks[0], fmt.Sprintf("jd-e%d-db-", environmentID)) {
			t.Fatalf("the preview joins %v, want its own database network alone", networks)
		}
		args := append([]string{"run", "--rm", "--network", networks[0], "-e", "DATABASE_URL=" + url, "--entrypoint", ""}, image)
		out, err := exec.CommandContext(ctx, "docker", append(args, client(url, sql)...)...).CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	production := func(sql string, args ...any) int {
		t.Helper()
		pool, _, err := s.dbPool(ctx, conn.ID)
		if err != nil {
			t.Fatal(err)
		}
		var count int
		if err := pool.QueryRowContext(ctx, sql, args...).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}

	policy(`{"mode":"branch"}`)
	first := preview(1, "acme/app")
	branch := ready(first)
	if branch.Host != deploy.PreviewBranchHost(conn.ID) || branch.Source.Kind != wantSource || branch.Engine == "" || branch.Database != "app" {
		t.Fatalf("branch = %+v", branch)
	}
	url, err := s.modules.deployDatabases.ResolveVariable(ctx, first, revisionOf(first), fmt.Sprintf("%d.url", conn.ID))
	if err != nil {
		t.Fatal(err)
	}
	savedConn, productionDSN, err := s.dbConnRow(ctx, conn.ID)
	if err != nil {
		t.Fatal(err)
	}
	productionInfo, err := dbx.ParseDSN(savedConn.Driver, productionDSN)
	if err != nil || productionInfo.Password == "" {
		t.Fatalf("production's sign-in: %v", err)
	}
	if !strings.Contains(url, "@"+deploy.PreviewBranchHost(conn.ID)+":") || strings.Contains(url, ":"+productionInfo.Password+"@") {
		t.Fatalf("the preview resolves %q: not the branch's own address and sign-in", url)
	}
	// Production's own sign-in does not open the branch.
	scheme, _, _ := strings.Cut(url, "://")
	withProductionSignIn := fmt.Sprintf("%s://%s:%s@%s:%d/%s", scheme, productionInfo.User, productionInfo.Password, branch.Host, branch.Port, branch.Database)
	if out, err := query(first, withProductionSignIn, "SELECT COUNT(*) FROM items"); err == nil {
		t.Fatalf("production's password opened the branch: %s", out)
	}
	if out, err := query(first, url, "SELECT COUNT(*) FROM items"); err != nil || out != "3" {
		t.Fatalf("the branch holds %q rows, %v; want the 3 the dump holds", out, err)
	}
	// Written on the branch, not in production; written in production, not
	// on the branch.
	if out, err := query(first, url, "INSERT INTO items(name) VALUES ('from-the-preview')"); err != nil {
		t.Fatalf("write to the branch: %v %s", err, out)
	}
	if got := production(`SELECT COUNT(*) FROM items WHERE name = 'from-the-preview'`); got != 0 {
		t.Fatal("a row written to the branch reached production")
	}
	pool, _, _ := s.dbPool(ctx, conn.ID)
	if _, err := pool.ExecContext(ctx, `INSERT INTO items(name) VALUES ('from-production')`); err != nil {
		t.Fatal(err)
	}
	if out, err := query(first, url, "SELECT COUNT(*) FROM items WHERE name = 'from-production'"); err != nil || out != "0" {
		t.Fatalf("production's row is on the branch: %q, %v", out, err)
	}
	// Production's own name is not on the preview's network.
	networks, _ := s.modules.deployDatabases.NetworksForRuntime(ctx, first, deploy.RuntimePlanConfig{}, map[string]string{"DATABASE_URL": url})
	if out, err := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", networks[0], "--entrypoint", "", image,
		"getent", "hosts", fmt.Sprintf("db-%d.jd.internal", conn.ID)).CombinedOutput(); err == nil {
		t.Fatalf("production's database answers on the preview's network: %s", out)
	}

	// The sweep at start keeps a ready branch.
	if err := s.modules.rehearser.RemoveOrphanedRehearsals(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.modules.docker.Inspect(ctx, branch.Container); err != nil {
		t.Fatalf("the start-up sweep removed a ready branch: %v", err)
	}

	// A masked branch: the column is overwritten in its shape, NULL kept.
	policy(`{"mode":"masked","maskedColumns":["items.name"]}`)
	second := preview(2, "acme/app")
	masked := ready(second)
	// A dump holds the two names it was taken with; an archive recovered to
	// its end may hold production's later row as well.
	if masked.MaskedRows < 2 || masked.MaskedRows > 3 || (wantSource == "dump" && masked.MaskedRows != 2) || strings.Join(masked.MaskedColumns, ",") != "items.name" {
		t.Fatalf("masked branch = %+v (want the backup's names masked and its NULL kept)", masked)
	}
	maskedURL, err := s.modules.deployDatabases.ResolveVariable(ctx, second, revisionOf(second), fmt.Sprint(conn.ID))
	if err != nil {
		t.Fatal(err)
	}
	out, err := query(second, maskedURL, "SELECT COUNT(*) FROM items WHERE name IN ('first', 'third', 'from-production')")
	if err != nil || out != "0" {
		t.Fatalf("unmasked names on the masked branch: %q, %v", out, err)
	}
	if out, err := query(second, maskedURL, "SELECT COUNT(*) FROM items WHERE name IS NULL"); err != nil || out != "1" {
		t.Fatalf("the NULL was not kept: %q, %v", out, err)
	}
	if out, err := query(second, maskedURL, "SELECT COUNT(*) FROM items WHERE LENGTH(name) = 5"); err != nil || out != "2" {
		t.Fatalf("the masked names lost their shape: %q, %v", out, err)
	}

	// A fork's preview gets no branch, and its reference meets production's
	// isolation as before.
	forked := preview(3, "someone/app")
	if _, err := s.modules.deployRuns.ActivePreviewBranch(ctx, forked, conn.ID); err == nil {
		t.Fatal("a fork's preview was given a branch")
	}
	if _, err := s.modules.deployDatabases.ResolveVariable(ctx, forked, revisionOf(forked), fmt.Sprint(conn.ID)); err == nil {
		t.Fatal("a fork's preview resolved production's database")
	}

	// Removing the preview removes its branch, and nothing is left with the
	// branch's label, its container or its volume.
	for _, item := range []struct {
		environmentID int64
		branch        deploy.PreviewBranch
	}{{first, branch}, {second, masked}} {
		if err := s.modules.deployRuntime.RemovePreviewResources(ctx, item.environmentID); err != nil {
			t.Fatal(err)
		}
		ended, err := s.modules.deployRuns.PreviewBranch(ctx, item.branch.ID)
		if err != nil || ended.State != deploy.PreviewBranchRemoved {
			t.Fatalf("after removal the branch reads %+v, %v", ended, err)
		}
		for _, kind := range []string{"ps", "volume", "network"} {
			args := []string{kind, "ls", "-q", "--filter", "label=" + previewBranchLabel + "=" + strconv.FormatInt(item.branch.ID, 10)}
			if kind == "ps" {
				args = []string{"ps", "-aq", "--filter", "label=" + previewBranchLabel + "=" + strconv.FormatInt(item.branch.ID, 10)}
			}
			if out, err := exec.CommandContext(ctx, "docker", args...).Output(); err != nil || strings.TrimSpace(string(out)) != "" {
				t.Fatalf("docker %s left %q, %v", kind, out, err)
			}
		}
		if out, _ := exec.CommandContext(ctx, "docker", "network", "ls", "-q", "--filter", fmt.Sprintf("label=io.just-dashboard.environment-id=%d", item.environmentID)).Output(); strings.TrimSpace(string(out)) != "" {
			t.Fatalf("the preview's database network survived: %q", out)
		}
	}
	// Production is as it was, plus its own row.
	if got := production(`SELECT COUNT(*) FROM items`); got != 4 {
		t.Fatalf("production holds %d rows, want its 3 and its own fourth", got)
	}
}
