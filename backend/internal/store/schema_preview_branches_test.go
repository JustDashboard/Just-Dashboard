package store

import (
	"strings"
	"testing"
)

func TestPreviewBranchSchemaOnlyAddsTables(t *testing.T) {
	for _, statement := range strings.Split(sqlComment.ReplaceAllString(previewBranchSchema, ""), ";") {
		words := strings.Fields(strings.ToUpper(statement))
		if len(words) == 0 {
			continue
		}
		if words[0] != "CREATE" || !strings.Contains(strings.Join(words, " "), " IF NOT EXISTS ") {
			t.Fatal("nonadditive migration", statement)
		}
	}
}

// What an environment gives its previews, and a preview's branches, go
// with the environment they belong to.
func TestPreviewBranchRowsGoWithTheirEnvironment(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.DB.Exec(query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	exec(`INSERT INTO deploy_projects(name, repo_path, branch, compose_file, pre_command, post_command, hook_secret, hook_id, enabled, created_at)
		VALUES('preview-branch-schema', '/srv/x', 'main', 'compose.yml', '', '', 'sealed', 'preview-branch-schema-hook', 1, 1)`)
	var projectID int64
	if err := st.DB.QueryRow(`SELECT id FROM deploy_projects WHERE name = 'preview-branch-schema'`).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"production", "preview"} {
		exec(`INSERT INTO deploy_environments(project_id, name, slug, kind, desired_revision, strategy, expected_downtime, protected, created_at, updated_at)
			VALUES(?, ?, ?, ?, 1, 'stop_first', 1, 0, 1, 1)`, projectID, kind, kind, kind)
	}
	var production, preview int64
	if err := st.DB.QueryRow(`SELECT id FROM deploy_environments WHERE project_id = ? AND kind = 'production'`, projectID).Scan(&production); err != nil {
		t.Fatal(err)
	}
	if err := st.DB.QueryRow(`SELECT id FROM deploy_environments WHERE project_id = ? AND kind = 'preview'`, projectID).Scan(&preview); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO deploy_preview_branch_policies(environment_id, connection_id, mode) VALUES(?, 5, 'branch')`, production)
	exec(`INSERT INTO deploy_preview_branches(project_id, environment_id, source_environment_id, connection_id, mode, state, created_at)
		VALUES(?, ?, ?, 5, 'branch', 'waiting', 1)`, projectID, preview, production)
	var mode, columns, state string
	if err := st.DB.QueryRow(`SELECT mode, masked_columns FROM deploy_preview_branch_policies WHERE environment_id = ?`, production).Scan(&mode, &columns); err != nil || mode != "branch" || columns != "[]" {
		t.Fatalf("policy = %s %s, %v", mode, columns, err)
	}
	if err := st.DB.QueryRow(`SELECT state FROM deploy_preview_branches WHERE environment_id = ?`, preview).Scan(&state); err != nil || state != "waiting" {
		t.Fatalf("branch = %s, %v", state, err)
	}
	exec(`DELETE FROM deploy_environments WHERE id IN (?, ?)`, production, preview)
	var left int
	if err := st.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM deploy_preview_branch_policies) + (SELECT COUNT(*) FROM deploy_preview_branches)`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("%d rows outlived their environments, %v", left, err)
	}
}
