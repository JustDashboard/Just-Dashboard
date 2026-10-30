package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSharedVariableSchemaOnlyAddsTables(t *testing.T) {
	for _, statement := range strings.Split(sqlComment.ReplaceAllString(sharedVariableSchema, ""), ";") {
		words := strings.Fields(strings.ToUpper(statement))
		if len(words) == 0 {
			continue
		}
		if words[0] != "CREATE" || !strings.Contains(strings.Join(words, " "), " IF NOT EXISTS ") {
			t.Fatal("nonadditive migration", statement)
		}
	}
}

// The table arrives on a new install and on one upgraded from 0.6.6, a name is
// one per scope however many projects use it, and a project's own go with the
// project while the server's stay.
func TestSharedVariableSchemaReachesFreshAndUpgradedInstalls(t *testing.T) {
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
			result, err := st.DB.Exec(`INSERT INTO deploy_projects(name, repo_path, hook_secret, hook_id, created_at) VALUES('shared-schema', '/srv/app', 'sealed', 'hook', 1)`)
			if err != nil {
				t.Fatal(err)
			}
			project, _ := result.LastInsertId()
			insert := `INSERT INTO deploy_shared_variables(project_id, name, value_enc, created_at, updated_at) VALUES(?, ?, 'sealed', 1, 1)`
			for _, row := range []struct {
				project any
				name    string
			}{{nil, "API_URL"}, {project, "API_URL"}, {nil, "SENTRY_DSN"}} {
				if _, err = st.DB.Exec(insert, row.project, row.name); err != nil {
					t.Fatalf("insert %v %s: %v", row.project, row.name, err)
				}
			}
			if _, err = st.DB.Exec(insert, nil, "API_URL"); err == nil {
				t.Fatal("a second server variable with the same name was accepted")
			}
			if _, err = st.DB.Exec(insert, project, "API_URL"); err == nil {
				t.Fatal("a second project variable with the same name was accepted")
			}
			if _, err = st.DB.Exec(`DELETE FROM deploy_projects WHERE id = ?`, project); err != nil {
				t.Fatal(err)
			}
			var projectRows, serverRows int
			if err = st.DB.QueryRow(`SELECT COUNT(*) FROM deploy_shared_variables WHERE project_id IS NOT NULL`).Scan(&projectRows); err != nil {
				t.Fatal(err)
			}
			if err = st.DB.QueryRow(`SELECT COUNT(*) FROM deploy_shared_variables WHERE project_id IS NULL`).Scan(&serverRows); err != nil {
				t.Fatal(err)
			}
			if projectRows != 0 || serverRows != 2 {
				t.Fatalf("after removing the project: %d project rows, %d server rows; want 0 and 2", projectRows, serverRows)
			}
		})
	}
}
