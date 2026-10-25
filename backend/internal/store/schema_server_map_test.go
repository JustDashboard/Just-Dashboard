package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServerMapSchemaOnlyAddsTables(t *testing.T) {
	for _, statement := range strings.Split(sqlComment.ReplaceAllString(serverMapSchema, ""), ";") {
		words := strings.Fields(strings.ToUpper(statement))
		if len(words) == 0 {
			continue
		}
		if words[0] != "CREATE" || !strings.Contains(strings.Join(words, " "), " IF NOT EXISTS ") {
			t.Fatal("nonadditive migration", statement)
		}
	}
}

// The table arrives on a new install and on one upgraded from 0.6.6, an
// arrangement survives the next boot, and it goes when its account does.
func TestServerMapLayoutsReachFreshAndUpgradedInstalls(t *testing.T) {
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
			if _, err = st.DB.Exec(`INSERT INTO users(username, password_hash, role, created_at) VALUES('mapper', 'x', 'admin', 1)`); err != nil {
				t.Fatal(err)
			}
			var id int64
			if err = st.DB.QueryRow(`SELECT id FROM users WHERE username='mapper'`).Scan(&id); err != nil {
				t.Fatal(err)
			}
			if _, err = st.DB.Exec(`INSERT INTO map_layouts(user_id, layout, updated_at) VALUES(?, '{"version":1}', 5)`, id); err != nil {
				t.Fatal(err)
			}
			st.Close()
			st, err = Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			var layout string
			if err = st.DB.QueryRow(`SELECT layout FROM map_layouts WHERE user_id=?`, id).Scan(&layout); err != nil || layout != `{"version":1}` {
				t.Fatal("reopening lost the map's arrangement", layout, err)
			}
			if _, err = st.DB.Exec(`DELETE FROM users WHERE id=?`, id); err != nil {
				t.Fatal(err)
			}
			var left int
			if err = st.DB.QueryRow(`SELECT COUNT(*) FROM map_layouts`).Scan(&left); err != nil || left != 0 {
				t.Fatal("a removed account left its arrangement behind", left, err)
			}
		})
	}
}
