package store

import (
	"strings"
	"testing"
)

func TestRehearsalSchemaOnlyAddsTables(t *testing.T) {
	for _, statement := range strings.Split(sqlComment.ReplaceAllString(rehearsalSchema, ""), ";") {
		words := strings.Fields(strings.ToUpper(statement))
		if len(words) == 0 {
			continue
		}
		if words[0] != "CREATE" || !strings.Contains(strings.Join(words, " "), " IF NOT EXISTS ") {
			t.Fatal("nonadditive migration", statement)
		}
	}
}

// The terms go with their environment.
func TestRehearsalPoliciesGoWithTheirEnvironment(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.DB.Exec(`INSERT INTO deploy_projects(name, repo_path, branch, compose_file, pre_command, post_command, hook_secret, hook_id, enabled, created_at)
		VALUES('rehearsal-schema', '/srv/x', 'main', 'compose.yml', '', '', 'sealed', 'rehearsal-schema-hook', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	var projectID, environmentID int64
	if err := st.DB.QueryRow(`SELECT id FROM deploy_projects WHERE name = 'rehearsal-schema'`).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO deploy_environments(project_id, name, slug, kind, desired_revision, strategy, expected_downtime, protected, created_at, updated_at)
		VALUES(?, 'production', 'production', 'production', 1, 'stop_first', 1, 1, 1, 1)`, projectID); err != nil {
		t.Fatal(err)
	}
	if err := st.DB.QueryRow(`SELECT id FROM deploy_environments WHERE project_id = ?`, projectID).Scan(&environmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO deploy_rehearsal_policies(environment_id, enabled, updated_at) VALUES(?, 1, 1)`, environmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`DELETE FROM deploy_projects WHERE id = ?`, projectID); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM deploy_rehearsal_policies`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("%d terms outlived their environment, %v", left, err)
	}
}
