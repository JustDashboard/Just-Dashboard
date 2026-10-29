package store

import (
	"strings"
	"testing"
)

func TestImageScanSchemaOnlyAddsTables(t *testing.T) {
	for _, statement := range strings.Split(sqlComment.ReplaceAllString(imageScanSchema, ""), ";") {
		words := strings.Fields(strings.ToUpper(statement))
		if len(words) == 0 {
			continue
		}
		if words[0] != "CREATE" || !strings.Contains(strings.Join(words, " "), " IF NOT EXISTS ") {
			t.Fatal("nonadditive migration", statement)
		}
	}
}

// A release's scan goes with the release, and an environment's terms with
// the environment.
func TestImageScansGoWithTheirReleaseAndTermsWithTheirEnvironment(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.DB.Exec(`INSERT INTO deploy_projects(name, repo_path, branch, compose_file, pre_command, post_command, hook_secret, hook_id, enabled, created_at)
		VALUES('scan-schema', '/srv/x', 'main', 'compose.yml', '', '', 'sealed', 'scan-schema-hook', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	var projectID, environmentID, releaseID int64
	if err := st.DB.QueryRow(`SELECT id FROM deploy_projects WHERE name = 'scan-schema'`).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO deploy_environments(project_id, name, slug, kind, desired_revision, strategy, expected_downtime, protected, created_at, updated_at)
		VALUES(?, 'production', 'production', 'production', 1, 'stop_first', 1, 1, 1, 1)`, projectID); err != nil {
		t.Fatal(err)
	}
	if err := st.DB.QueryRow(`SELECT id FROM deploy_environments WHERE project_id = ?`, projectID).Scan(&environmentID); err != nil {
		t.Fatal(err)
	}
	result, err := st.DB.Exec(`INSERT INTO deploy_releases(project_id, environment_id, release_number, created_at) VALUES(?, ?, 1, 1)`, projectID, environmentID)
	if err != nil {
		t.Fatal(err)
	}
	releaseID, _ = result.LastInsertId()
	if _, err := st.DB.Exec(`INSERT INTO deploy_image_scans(release_id, environment_id, state, requested_at) VALUES(?, ?, 'queued', 1)`, releaseID, environmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO deploy_image_scan_policies(environment_id, enabled, updated_at) VALUES(?, 0, 1)`, environmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`DELETE FROM deploy_projects WHERE id = ?`, projectID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"deploy_image_scans", "deploy_image_scan_policies"} {
		var left int
		if err := st.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&left); err != nil || left != 0 {
			t.Fatalf("%d rows of %s outlived their release, %v", left, table, err)
		}
	}
}
