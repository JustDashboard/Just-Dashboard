package store

import (
	"strings"
	"testing"
)

func TestAccountLinkSchemaOnlyAddsTablesAndIndexes(t *testing.T) {
	for _, statement := range strings.Split(sqlComment.ReplaceAllString(accountLinkSchema, ""), ";") {
		words := strings.Fields(strings.ToUpper(statement))
		if len(words) == 0 {
			continue
		}
		text := strings.Join(words, " ")
		if words[0] != "CREATE" || !strings.Contains(text, " IF NOT EXISTS ") {
			t.Fatalf("non-additive statement: %s", text)
		}
	}
}

// A database written by a release that has never heard of linking opens with
// the new table, and opening it again changes nothing.
func TestAccountLinkSchemaReachesAnExistingInstall(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`DROP TABLE account_link_commands`); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		st, err = Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.DB.Exec(`INSERT INTO account_link_commands(id, kind, received_at, status) VALUES('01JB0000000000000000000000','snapshot.now',1,'running')
			ON CONFLICT(id) DO NOTHING`); err != nil {
			t.Fatalf("table missing after upgrade: %v", err)
		}
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
