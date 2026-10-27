package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecretStoreSchemaOnlyAddsTables(t *testing.T) {
	for _, statement := range strings.Split(sqlComment.ReplaceAllString(secretStoreSchema, ""), ";") {
		words := strings.Fields(strings.ToUpper(statement))
		if len(words) == 0 {
			continue
		}
		if words[0] != "CREATE" || !strings.Contains(strings.Join(words, " "), " IF NOT EXISTS ") {
			t.Fatal("nonadditive migration", statement)
		}
	}
}

// The table arrives on a new install and on one upgraded from 0.6.6, with one
// connection per name, since a name is what a reference says.
func TestSecretStoreSchemaReachesFreshAndUpgradedInstalls(t *testing.T) {
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
			insert := `INSERT INTO deploy_secret_stores(name, kind, address, created_at, updated_at) VALUES(?, 'vault', 'https://vault.example.test', 1, 1)`
			if _, err := st.DB.Exec(insert, "vault-prod"); err != nil {
				t.Fatal(err)
			}
			if _, err := st.DB.Exec(insert, "vault-prod"); err == nil {
				t.Fatal("a second store with the same name was accepted")
			}
			var previews int
			var settings string
			if err := st.DB.QueryRow(`SELECT allow_previews, settings_json FROM deploy_secret_stores WHERE name = 'vault-prod'`).Scan(&previews, &settings); err != nil {
				t.Fatal(err)
			}
			if previews != 0 || settings != "{}" {
				t.Fatalf("a new store allows previews %d with settings %q; want 0 and {}", previews, settings)
			}
		})
	}
}
