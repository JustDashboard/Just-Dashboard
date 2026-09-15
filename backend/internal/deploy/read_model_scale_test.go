package deploy

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	basestore "github.com/Wayy01/Just-Dashboard/backend/internal/store"
	_ "modernc.org/sqlite"
)

// countingDriver wraps the SQLite driver so a read model's statement count can
// be measured directly. A read whose cost grows with the fleet is a regression
// the fleet page pays for on every poll.
type countingDriver struct {
	inner driver.Driver
	count atomic.Int64
	texts struct {
		sync.Mutex
		seen []string
	}
}

var registerCountingDriver sync.Once
var sharedCountingDriver = &countingDriver{}

func (d *countingDriver) Open(name string) (driver.Conn, error) {
	inner, err := d.inner.Open(name)
	if err != nil {
		return nil, err
	}
	return &countingConn{Conn: inner, driver: d}, nil
}

type countingConn struct {
	driver.Conn
	driver *countingDriver
}

func (c *countingConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	c.driver.record(query)
	if preparer, ok := c.Conn.(driver.ConnPrepareContext); ok {
		return preparer.PrepareContext(ctx, query)
	}
	return c.Conn.Prepare(query)
}

func (c *countingConn) Prepare(query string) (driver.Stmt, error) {
	c.driver.record(query)
	return c.Conn.Prepare(query)
}

func (c *countingConn) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	if beginner, ok := c.Conn.(driver.ConnBeginTx); ok {
		return beginner.BeginTx(ctx, options)
	}
	return c.Conn.Begin()
}

func (d *countingDriver) record(query string) {
	normalized := strings.ToLower(strings.TrimSpace(query))
	if strings.HasPrefix(normalized, "begin") || strings.HasPrefix(normalized, "commit") ||
		strings.HasPrefix(normalized, "rollback") || strings.HasPrefix(normalized, "pragma") {
		return
	}
	d.count.Add(1)
	d.texts.Lock()
	d.texts.seen = append(d.texts.seen, normalized)
	d.texts.Unlock()
}

func (d *countingDriver) reset() {
	d.count.Store(0)
	d.texts.Lock()
	d.texts.seen = nil
	d.texts.Unlock()
}

// countingFleet builds a populated fleet on a database whose statements are
// counted. Each deployment gets a live release with runtime, artifacts, checks
// and run history, so the per-deployment joins all have work to do.
func countingFleet(t *testing.T, deployments int) (*OrchestrationStore, *countingDriver) {
	t.Helper()
	registerCountingDriver.Do(func() {
		probe, err := sql.Open("sqlite", ":memory:")
		if err != nil {
			t.Fatal(err)
		}
		sharedCountingDriver.inner = probe.Driver()
		_ = probe.Close()
		sql.Register("sqlite-counting", sharedCountingDriver)
	})
	dir := t.TempDir()
	base, err := basestore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	seedCountingFleet(t, base, now, deployments)
	if err := base.Close(); err != nil {
		t.Fatal(err)
	}
	counted, err := sql.Open("sqlite-counting",
		filepath.Join(dir, basestore.DatabaseFile)+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = counted.Close() })
	counted.SetMaxOpenConns(1)
	store := NewOrchestrationStore(&basestore.Store{DB: counted})
	store.now = func() time.Time { return now }
	sharedCountingDriver.reset()
	return store, sharedCountingDriver
}

func seedCountingFleet(t *testing.T, base *basestore.Store, now time.Time, deployments int) {
	t.Helper()
	for index := 0; index < deployments; index++ {
		name := fmt.Sprintf("scale-%03d", index)
		result, err := base.DB.Exec(`
			INSERT INTO deploy_projects(
			  name, profile, repo_path, branch, compose_file, pre_command, post_command,
			  hook_secret, hook_id, enabled, created_at, updated_at)
			VALUES(?, 'web', ?, 'main', 'compose.yml', '', '', 'sealed', ?, 1, ?, ?)`,
			name, "/srv/"+name, name+"-hook", now.Unix(), now.Unix())
		if err != nil {
			t.Fatal(err)
		}
		projectID, _ := result.LastInsertId()
		result, err = base.DB.Exec(`
			INSERT INTO deploy_environments(
			  project_id, name, slug, kind, desired_revision, strategy, expected_downtime,
			  protected, created_at, updated_at)
			VALUES(?, 'Production', 'production', 'production', 1, 'blue_green', 0, 1, ?, ?)`,
			projectID, now.Unix(), now.Unix())
		if err != nil {
			t.Fatal(err)
		}
		environmentID, _ := result.LastInsertId()
		for statement, args := range map[string][]any{
			`INSERT INTO deploy_sources(environment_id, revision, kind, config_json, identity_json, digest, created_at)
			 VALUES(?, 1, 'local', '{"kind":"local","mode":"local_checkout","localPath":"/srv"}',
			        '{"kind":"local","revision":"` + strings.Repeat("a", 40) + `"}', ?, ?)`: {
				environmentID, fakeContentDigest(name + "-source"), now.Unix()},
			`INSERT INTO deploy_build_plans(environment_id, revision, method, config_json, evidence_json, preview, digest, created_at)
			 VALUES(?, 1, 'dockerfile', '{"method":"dockerfile"}', '{"candidates":[],"gitRequirements":{}}', 'preview', ?, ?)`: {
				environmentID, fakeContentDigest(name + "-build"), now.Unix()},
			`INSERT INTO deploy_runtime_plans(environment_id, revision, config_json, preview, digest, created_at)
			 VALUES(?, 1, '{"strategy":"blue_green","internalPort":3000}', 'preview', ?, ?)`: {
				environmentID, fakeContentDigest(name + "-runtime"), now.Unix()},
		} {
			if _, err := base.DB.Exec(statement, args...); err != nil {
				t.Fatal(err)
			}
		}
		imageDigest := fakeContentDigest(name + "-image")
		snapshot := `{"version":1,"plan":{"strategy":"blue_green","internalPort":3000},` +
			`"image":{"reference":"example.test/` + name + `:v1","digest":"` + imageDigest + `"},` +
			`"variables":[],"dependencies":[],"checks":[{"name":"ready","kind":"http","phase":"readiness","required":true}],` +
			`"domains":[],"planInputsDigest":"sha256:` + strings.Repeat("0", 64) + `"}`
		configDigest := digestBytes([]byte(snapshot))
		// Runs are written before the release so its immutable run_id is set at
		// insert time; the release snapshot cannot be updated afterwards.
		runIDs := []int64{}
		for run := 0; run < 3; run++ {
			result, err := base.DB.Exec(`
				INSERT INTO deploy_runs(
				  project_id, environment_id, started_at, state, status, operation, trigger, actor,
				  requested_at, queued_at, claimed_at, heartbeat_at, ended_at, cancel_requested,
				  superseded_by, retry_of_run_id, idempotency_key, request_digest, plan_revision,
				  release_id, candidate_release_id, terminal_code, terminal_reason, lease_until,
				  priority, slot_class, metadata_json)
				VALUES(?, ?, ?, 'succeeded', 'success', 'deploy', 'manual', 'admin', ?, ?, ?, ?, ?, 0, 0, 0,
				       '', ?, 1, 0, 0, 'success', '', 0, 0, 'heavy', '{}')`,
				projectID, environmentID, now.Unix()+int64(run), now.Unix()+int64(run), now.Unix()+int64(run),
				now.Unix()+int64(run), now.Unix()+int64(run), now.Unix()+int64(run),
				fmt.Sprintf("%s-run-%d", name, run))
			if err != nil {
				t.Fatal(err)
			}
			runID, _ := result.LastInsertId()
			runIDs = append(runIDs, runID)
		}
		releaseRunID := runIDs[len(runIDs)-1]
		result, err = base.DB.Exec(`
			INSERT INTO deploy_releases(
			  project_id, environment_id, release_number, run_id, predecessor_release_id, state,
			  plan_revision, source_id, build_plan_id, runtime_plan_id, source_revision,
			  source_identity_json, image_digest, config_digest, variables_digest, strategy,
			  expected_downtime, provenance_json, blueprint_id, blueprint_version, created_at, activated_at)
			VALUES(?, ?, 1, ?, 0, 'live', 1, 0, 0, 0, ?, '{}', ?, ?, ?, 'blue_green', 0, '{}', '', '', ?, ?)`,
			projectID, environmentID, releaseRunID, strings.Repeat("a", 40), imageDigest, configDigest,
			fakeContentDigest(name+"-vars"), now.Unix(), now.Unix())
		if err != nil {
			t.Fatal(err)
		}
		releaseID, _ := result.LastInsertId()
		if _, err := base.DB.Exec(`
			INSERT INTO deploy_release_artifacts(
			  release_id, kind, reference, digest, metadata_json, size_bytes, retain_until, state, created_at)
			VALUES(?, 'runtime_config', 'runtime-plan.json', ?, ?, ?, 0, 'available', ?)`,
			releaseID, configDigest,
			`{"secretFreePreview":"preview","snapshot":`+snapshot+`}`, len(snapshot), now.Unix()); err != nil {
			t.Fatal(err)
		}
		if _, err := base.DB.Exec(`
			INSERT INTO deploy_release_runtimes(
			  release_id, environment_id, kind, runtime_id, name, working_directory,
			  host, port, state, metadata_json, created_at, updated_at)
			VALUES(?, ?, 'container', ?, ?, '', '127.0.0.1', ?, 'live', '{}', ?, ?)`,
			releaseID, environmentID, name+"-container", name, 31000+index, now.Unix(), now.Unix()); err != nil {
			t.Fatal(err)
		}
		for _, runID := range runIDs {
			if _, err := base.DB.Exec(
				"UPDATE deploy_runs SET release_id = ?, candidate_release_id = ? WHERE id = ?",
				releaseID, releaseID, runID); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := base.DB.Exec(
			"UPDATE deploy_environments SET live_release_id = ? WHERE id = ?", releaseID, environmentID); err != nil {
			t.Fatal(err)
		}
		evidence := `{"phase":"readiness","outcome":"passed","checks":[` +
			`{"name":"ready","kind":"http","phase":"readiness","required":true,"outcome":"passed"}]}`
		if _, err := base.DB.Exec(`
			INSERT INTO deploy_steps(
			  run_id, ordinal, step_key, status, attempt, started_at, ended_at,
			  error_code, error_message, evidence_json)
			VALUES(?, 1, 'verify_readiness', 'passed', 1, ?, ?, '', '', ?)`,
			releaseRunID, now.Unix(), now.Unix(), evidence); err != nil {
			t.Fatal(err)
		}
	}
}

// The fleet read model must cost the same number of statements whether the host
// runs one deployment or fifty. Every per-deployment join is batched.
func TestFleetReadModelStatementCountDoesNotGrowWithTheFleet(t *testing.T) {
	small, driver := countingFleet(t, 2)
	if _, err := small.Fleet(context.Background(), QueueBudget{Heavy: 2, Light: 4}); err != nil {
		t.Fatal(err)
	}
	smallCount := driver.count.Load()

	large, driver := countingFleet(t, 40)
	fleet, err := large.Fleet(context.Background(), QueueBudget{Heavy: 2, Light: 4})
	if err != nil {
		t.Fatal(err)
	}
	largeCount := driver.count.Load()
	if len(fleet.Deployments) != 40 {
		t.Fatalf("fleet returned %d deployments, want 40", len(fleet.Deployments))
	}
	if smallCount == 0 || largeCount != smallCount {
		t.Fatalf("fleet statements grew with the fleet: %d for 2 deployments, %d for 40", smallCount, largeCount)
	}
	if largeCount > 10 {
		t.Fatalf("fleet read model issued %d statements, want a small fixed set", largeCount)
	}
	for _, summary := range fleet.Deployments {
		if summary.Health != string(HealthPassed) {
			t.Fatalf("%s health = %q, want the batched read to reproduce the per-release outcome", summary.Name, summary.Health)
		}
		if summary.LastRun == nil || summary.ActiveRun != nil {
			t.Fatalf("%s runs = last %#v active %#v", summary.Name, summary.LastRun, summary.ActiveRun)
		}
	}
}

// Opening one workspace must read one deployment. The page polls every few
// seconds; loading the whole fleet to answer it is the N+1 in disguise.
func TestDeploymentSummaryReadsOnlyTheRequestedDeployment(t *testing.T) {
	store, driver := countingFleet(t, 40)
	summary, err := store.DeploymentSummary(context.Background(), 1, QueueBudget{Heavy: 2, Light: 4})
	if err != nil {
		t.Fatal(err)
	}
	if summary.ID != 1 || summary.Name != "scale-000" {
		t.Fatalf("summary = %#v", summary)
	}
	driver.texts.Lock()
	defer driver.texts.Unlock()
	for _, statement := range driver.texts.seen {
		if strings.Contains(statement, "from deploy_projects p") && !strings.Contains(statement, "where p.id = ?") {
			t.Fatalf("workspace read loaded the whole fleet: %s", statement)
		}
	}
	if got := driver.count.Load(); got > 10 {
		t.Fatalf("workspace read issued %d statements, want a small fixed set", got)
	}
}
