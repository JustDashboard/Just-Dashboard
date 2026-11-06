package store

import (
	"strings"
	"testing"
)

func TestHubLinkSchemaOnlyAddsTablesAndIndexes(t *testing.T) {
	for _, statement := range strings.Split(sqlComment.ReplaceAllString(hubLinkSchema, ""), ";") {
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

// A database written by a release that has never heard of the hub link opens
// with the new table, and opening it again changes nothing. The account link's
// table is not touched.
func TestHubLinkSchemaReachesAnExistingInstall(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO account_link_commands(id, kind, received_at, status) VALUES('01JB0000000000000000000009','snapshot.now',1,'succeeded')`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`DROP TABLE hub_link_commands`); err != nil {
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
		if _, err := st.DB.Exec(`INSERT INTO hub_link_commands(id, kind, received_at, status) VALUES('01JB0000000000000000000000','snapshot.now',1,'running')
			ON CONFLICT(id) DO NOTHING`); err != nil {
			t.Fatalf("table missing after upgrade: %v", err)
		}
		var n int
		if err := st.DB.QueryRow(`SELECT count(*) FROM account_link_commands`).Scan(&n); err != nil || n != 1 {
			t.Fatalf("account link commands after upgrade = %d, %v", n, err)
		}
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
