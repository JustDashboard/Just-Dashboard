package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRollbackWatchSchemaOnlyAddsTables(t *testing.T) {
	for _, statement := range strings.Split(sqlComment.ReplaceAllString(rollbackWatchSchema, ""), ";") {
		words := strings.Fields(strings.ToUpper(statement))
		if len(words) == 0 {
			continue
		}
		if words[0] != "CREATE" || !strings.Contains(strings.Join(words, " "), " IF NOT EXISTS ") {
			t.Fatal("nonadditive migration", statement)
		}
	}
}

// Both tables arrive on a new install and on one upgraded from 0.6.6, the
// release columns are added to the shipped table with their defaults, and a
// second open window for one environment is refused by the database itself.
func TestRollbackWatchesReachFreshAndUpgradedInstalls(t *testing.T) {
	upgraded := t.TempDir()
	script, err := os.ReadFile(filepath.Join("testdata", "0.6.6.sql"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(upgraded, DatabaseFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(script)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	for name, dir := range map[string]string{"fresh": t.TempDir(), "0.6.6": upgraded} {
		t.Run(name, func(t *testing.T) {
			st, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			columns, err := tableColumns(t.Context(), st.DB, "deploy_releases")
			if err != nil {
				t.Fatal(err)
			}
			if !columns["rolled_back_at"] || !columns["rollback_reason"] {
				t.Fatalf("deploy_releases lacks the rollback columns: %v", columns)
			}
			if _, err := st.DB.Exec(`INSERT INTO deploy_projects(name, repo_path, branch, compose_file, pre_command, post_command, hook_secret, hook_id, enabled, created_at)
				VALUES('watch-schema', '/srv/x', 'main', 'compose.yml', '', '', 'sealed', 'watch-schema-hook', 1, 1)`); err != nil {
				t.Fatal(err)
			}
			var projectID int64
			if err := st.DB.QueryRow(`SELECT id FROM deploy_projects WHERE name = 'watch-schema'`).Scan(&projectID); err != nil {
				t.Fatal(err)
			}
			if _, err := st.DB.Exec(`INSERT INTO deploy_environments(project_id, name, slug, kind, desired_revision, strategy, expected_downtime, protected, created_at, updated_at)
				VALUES(?, 'production', 'production', 'production', 1, 'blue_green', 0, 1, 1, 1)`, projectID); err != nil {
				t.Fatal(err)
			}
			var environmentID int64
			if err := st.DB.QueryRow(`SELECT id FROM deploy_environments WHERE project_id = ?`, projectID).Scan(&environmentID); err != nil {
				t.Fatal(err)
			}
			open := func(state string) error {
				_, err := st.DB.Exec(`INSERT INTO deploy_rollback_watches(project_id, environment_id, release_id, warm_release_id, state, cutover_at, expires_at, created_at, updated_at)
					VALUES(?, ?, 2, 1, ?, 1, 2, 1, 1)`, projectID, environmentID, state)
				return err
			}
			if err := open("watching"); err != nil {
				t.Fatal(err)
			}
			if err := open("closing"); err == nil {
				t.Fatal("a second open window was accepted for one environment")
			}
			if err := open("closed"); err != nil {
				t.Fatalf("a closed window beside an open one was refused: %v", err)
			}
			if _, err := st.DB.Exec(`DELETE FROM deploy_projects WHERE id = ?`, projectID); err != nil {
				t.Fatal(err)
			}
			var left int
			if err := st.DB.QueryRow(`SELECT COUNT(*) FROM deploy_rollback_watches`).Scan(&left); err != nil {
				t.Fatal(err)
			}
			if left != 0 {
				t.Fatalf("%d windows outlived their project", left)
			}
		})
	}
}
