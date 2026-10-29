package store

import (
	"strings"
	"testing"
)

func TestCanarySchemaOnlyAddsTables(t *testing.T) {
	for _, statement := range strings.Split(sqlComment.ReplaceAllString(canarySchema, ""), ";") {
		words := strings.Fields(strings.ToUpper(statement))
		if len(words) == 0 {
			continue
		}
		if words[0] != "CREATE" || !strings.Contains(strings.Join(words, " "), " IF NOT EXISTS ") {
			t.Fatal("nonadditive migration", statement)
		}
	}
}

// The table arrives on a new install, and a second open trial for one
// environment is refused by the database itself.
func TestCanariesReachAFreshInstallOneOpenAtATime(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.DB.Exec(`INSERT INTO deploy_projects(name, profile, repo_path, branch, compose_file, pre_command, post_command, hook_secret, hook_id, enabled, created_at, updated_at)
		VALUES('canary', 'web', '/srv/canary', 'main', 'compose.yml', '', '', 'sealed', 'canary-hook', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO deploy_environments(project_id, name, slug, kind, desired_revision, strategy, expected_downtime, protected, created_at, updated_at)
		VALUES(1, 'Production', 'production', 'production', 1, 'canary', 0, 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO deploy_canaries(project_id, environment_id, run_id, release_id, live_release_id, percent,
		duration_seconds, state, started_at, ends_at, created_at, updated_at) VALUES(1, 1, ?, 2, 1, 10, 60, ?, 1, 61, 1, 1)`
	if _, err := st.DB.Exec(insert, 1, "running"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(insert, 2, "holding"); err == nil {
		t.Fatal("a second open trial was accepted")
	}
	if _, err := st.DB.Exec(insert, 3, "aborted"); err != nil {
		t.Fatal(err)
	}
}
