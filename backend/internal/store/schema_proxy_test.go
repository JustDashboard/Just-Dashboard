package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The proxy tables arrive on installs of every age: a new one, and one that
// has been upgraded since 0.6.6 and already holds deployments. Both must open,
// and keep opening, since Open runs on every boot.
func TestProxySchemaAppliesToFreshAndUpgradedDatabases(t *testing.T) {
	upgraded := t.TempDir()
	script, err := os.ReadFile(filepath.Join("testdata", "0.6.6.sql"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(upgraded, DatabaseFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(script)); err != nil {
		db.Close()
		t.Fatalf("install 0.6.6 fixture: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	for name, dir := range map[string]string{"fresh": t.TempDir(), "0.6.6": upgraded} {
		for i := range 2 {
			st, err := Open(dir)
			if err != nil {
				t.Fatalf("%s open #%d: %v", name, i+1, err)
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

var sqlComment = regexp.MustCompile(`(?m)--.*$`)

// Invariant 5 for the proxy's block: a statement that drops, renames or alters
// what an older install already has, or that fails when the thing it creates
// is already there, is a boot failure or a data loss on exactly the installs
// that upgrade.
func TestProxySchemaIsAdditive(t *testing.T) {
	for _, statement := range strings.Split(sqlComment.ReplaceAllString(proxySchema, ""), ";") {
		words := strings.Fields(strings.ToUpper(statement))
		if len(words) == 0 {
			continue
		}
		text := strings.Join(words, " ")
		switch {
		case words[0] == "CREATE" && !strings.Contains(text, " IF NOT EXISTS "):
			t.Errorf("CREATE without IF NOT EXISTS: %s", text)
		case words[0] == "DROP" || words[0] == "ALTER" || strings.Contains(text, " RENAME "):
			t.Errorf("not additive (columns for a shipped table belong in addedColumns): %s", text)
		}
	}
}
