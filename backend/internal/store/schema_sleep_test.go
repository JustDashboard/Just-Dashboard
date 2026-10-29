package store

import (
	"strings"
	"testing"
)

func TestSleepSchemaOnlyAddsTables(t *testing.T) {
	for _, statement := range strings.Split(sqlComment.ReplaceAllString(sleepSchema, ""), ";") {
		words := strings.Fields(strings.ToUpper(statement))
		if len(words) == 0 {
			continue
		}
		if words[0] != "CREATE" || !strings.Contains(strings.Join(words, " "), " IF NOT EXISTS ") {
			t.Fatal("nonadditive migration", statement)
		}
	}
}

// The table arrives on a fresh install, and the database itself refuses a
// second open stretch for one environment.
func TestSleepSpansAreOneOpenPerEnvironment(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.DB.Exec(`INSERT INTO deploy_projects(name, repo_path, branch, compose_file, pre_command, post_command, hook_secret, hook_id, enabled, created_at)
		VALUES('sleep-schema', '/srv/x', 'main', 'compose.yml', '', '', 'sealed', 'sleep-schema-hook', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	var projectID int64
	if err := st.DB.QueryRow(`SELECT id FROM deploy_projects WHERE name = 'sleep-schema'`).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	result, err := st.DB.Exec(`INSERT INTO deploy_environments(project_id, slug, name, kind, created_at, updated_at) VALUES(?, 'production', 'Production', 'production', 1, 1)`, projectID)
	if err != nil {
		t.Fatal(err)
	}
	environmentID, _ := result.LastInsertId()
	insert := `INSERT INTO deploy_sleep_spans(environment_id, release_id, slept_at) VALUES(?, 1, 10)`
	if _, err := st.DB.Exec(insert, environmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(insert, environmentID); err == nil {
		t.Fatal("a second open stretch was accepted")
	}
	if _, err := st.DB.Exec(`UPDATE deploy_sleep_spans SET woke_at = 20 WHERE environment_id = ?`, environmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(insert, environmentID); err != nil {
		t.Fatalf("a new stretch after the last one closed: %v", err)
	}
}
