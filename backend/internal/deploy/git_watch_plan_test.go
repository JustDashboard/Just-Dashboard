package deploy

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store/storetest"
)

// queryPlan is what SQLite says it will do for a statement, one line a step.
func queryPlan(t *testing.T, db *sql.DB, query string, args ...any) []string {
	t.Helper()
	rows, err := db.Query(`EXPLAIN QUERY PLAN `+query, args...)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return plan
}

// The watcher asks its question every five seconds for as long as the server
// runs, and deploy_runs only grows. Each of its two looks at an environment's
// runs must come from the index that leads on the environment: not a walk of
// the table back to that environment's last deploy, and not an index SQLite
// builds over the table for the one execution.
func TestTheGitWatcherReadsAnEnvironmentsRunsFromAnIndex(t *testing.T) {
	st, err := storetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	plan := queryPlan(t, st.DB, gitWatchTargetsQuery, 0, 0, 0, 0)
	whole := strings.Join(plan, "\n")
	fromIndex := 0
	for _, step := range plan {
		if strings.Contains(step, "AUTOMATIC") {
			t.Errorf("SQLite builds an index for every execution: %s", step)
		}
		// r is deploy_runs in both subqueries.
		if step == "SCAN r" || strings.HasPrefix(step, "SCAN r ") {
			t.Errorf("the watcher scans deploy_runs: %s", step)
		}
		if strings.HasPrefix(step, "SEARCH r ") && strings.Contains(step, "idx_deploy_runs_environment ") {
			fromIndex++
		}
		if strings.Contains(step, "TEMP B-TREE FOR ORDER BY") {
			t.Errorf("the watcher sorts runs it could have read in order: %s", step)
		}
	}
	if fromIndex != 2 {
		t.Errorf("%d of the watcher's 2 reads of deploy_runs use idx_deploy_runs_environment", fromIndex)
	}
	t.Logf("plan:\n%s", whole)

	// The other questions asked about one environment's runs (what is still
	// in flight before it sleeps, is promoted or is quarantined) get the same
	// index instead of a scan.
	inFlight := queryPlan(t, st.DB,
		`SELECT id, state FROM deploy_runs WHERE environment_id = ? AND state NOT IN ('succeeded','failed','cancelled')`, 1)
	if len(inFlight) != 1 || !strings.HasPrefix(inFlight[0], "SEARCH deploy_runs USING INDEX idx_deploy_runs_environment ") {
		t.Errorf("an environment's runs in flight: %v", inFlight)
	}
}
