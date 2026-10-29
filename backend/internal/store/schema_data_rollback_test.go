package store

import (
	"strings"
	"testing"
)

func TestDataRollbackSchemaOnlyAddsTables(t *testing.T) {
	for _, statement := range strings.Split(sqlComment.ReplaceAllString(dataRollbackSchema, ""), ";") {
		words := strings.Fields(strings.ToUpper(statement))
		if len(words) == 0 {
			continue
		}
		if words[0] != "CREATE" || !strings.Contains(strings.Join(words, " "), " IF NOT EXISTS ") {
			t.Fatal("nonadditive migration", statement)
		}
	}
}

// A release's data points and the copies restored from them go with the
// project, as its releases do.
func TestDataPointsAndCopiesGoWithTheirProject(t *testing.T) {
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
		VALUES('data-rollback-schema', '/srv/x', 'main', 'compose.yml', '', '', 'sealed', 'data-rollback-schema-hook', 1, 1)`)
	var projectID, environmentID, releaseID int64
	if err := st.DB.QueryRow(`SELECT id FROM deploy_projects WHERE name = 'data-rollback-schema'`).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO deploy_environments(project_id, name, slug, kind, desired_revision, strategy, expected_downtime, protected, created_at, updated_at)
		VALUES(?, 'production', 'production', 'production', 1, 'stop_first', 1, 1, 1, 1)`, projectID)
	if err := st.DB.QueryRow(`SELECT id FROM deploy_environments WHERE project_id = ?`, projectID).Scan(&environmentID); err != nil {
		t.Fatal(err)
	}
	var columns []string
	rows, err := st.DB.Query(`SELECT name FROM pragma_table_info('deploy_releases') WHERE "notnull" = 1 AND dflt_value IS NULL AND pk = 0`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, name)
	}
	rows.Close()
	values := map[string]any{"project_id": projectID, "environment_id": environmentID, "release_number": 1, "run_id": 1}
	args := []any{}
	placeholders := []string{}
	for _, column := range columns {
		value, ok := values[column]
		if !ok {
			value = 0
			if strings.HasSuffix(column, "_json") || column == "state" || column == "strategy" || strings.HasSuffix(column, "digest") {
				value = "x"
			}
		}
		args = append(args, value)
		placeholders = append(placeholders, "?")
	}
	exec(`INSERT INTO deploy_releases(`+strings.Join(columns, ", ")+`) VALUES(`+strings.Join(placeholders, ", ")+`)`, args...)
	if err := st.DB.QueryRow(`SELECT id FROM deploy_releases WHERE environment_id = ?`, environmentID).Scan(&releaseID); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO deploy_release_data_points(release_id, connection_id, kind, recorded_at) VALUES(?, 5, 'dump', 1)`, releaseID)
	exec(`INSERT INTO deploy_data_copies(project_id, environment_id, release_id, connection_id, state, source_kind, created_at)
		VALUES(?, ?, ?, 5, 'ready', 'dump', 1)`, projectID, environmentID, releaseID)
	exec(`DELETE FROM deploy_projects WHERE id = ?`, projectID)
	for _, table := range []string{"deploy_release_data_points", "deploy_data_copies"} {
		var left int
		if err := st.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&left); err != nil || left != 0 {
			t.Fatalf("%d rows of %s outlived their project, %v", left, table, err)
		}
	}
}
