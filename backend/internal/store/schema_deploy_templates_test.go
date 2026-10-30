package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeployTemplateSchemaOnlyAddsTables(t *testing.T) {
	for _, statement := range strings.Split(sqlComment.ReplaceAllString(deployTemplateSchema, ""), ";") {
		words := strings.Fields(strings.ToUpper(statement))
		if len(words) == 0 {
			continue
		}
		if words[0] != "CREATE" || !strings.Contains(strings.Join(words, " "), " IF NOT EXISTS ") {
			t.Fatal("nonadditive migration", statement)
		}
	}
}

// The table arrives on a new install and on one upgraded from 0.6.6, and a
// template's id is one per dashboard.
func TestDeployTemplateSchemaReachesFreshAndUpgradedInstalls(t *testing.T) {
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
			insert := `INSERT INTO deploy_templates(slug, name, version, source, definition_json, created_at, updated_at)
				VALUES(?, 'Stack', '1.0.0', 'compose', '{}', 1, 1)`
			if _, err := st.DB.Exec(insert, "user-stack"); err != nil {
				t.Fatal(err)
			}
			if _, err := st.DB.Exec(insert, "user-stack"); err == nil {
				t.Fatal("a second template with the same id was accepted")
			}
			var fromProject int64
			if err := st.DB.QueryRow(`SELECT from_project_id FROM deploy_templates WHERE slug = 'user-stack'`).Scan(&fromProject); err != nil || fromProject != 0 {
				t.Fatalf("from_project_id = %d, %v", fromProject, err)
			}
		})
	}
}
