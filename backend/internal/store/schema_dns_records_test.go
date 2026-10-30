package store

import (
	"strings"
	"testing"
)

func TestDNSRecordSchemaOnlyAddsTables(t *testing.T) {
	for _, statement := range strings.Split(sqlComment.ReplaceAllString(dnsRecordSchema, ""), ";") {
		words := strings.Fields(strings.ToUpper(statement))
		if len(words) == 0 {
			continue
		}
		if words[0] != "CREATE" || !strings.Contains(strings.Join(words, " "), " IF NOT EXISTS ") {
			t.Fatal("nonadditive migration", statement)
		}
	}
}

// One row a name and type: a second write of the same set is an update of
// the row, never a second claim on the record.
func TestDNSRecordIsOneRowANameAndType(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	insert := `INSERT INTO deploy_dns_records(hostname, record_type, created_at, updated_at) VALUES(?, ?, 1, 1)`
	if _, err := st.DB.Exec(insert, "app.example.com", "A"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(insert, "app.example.com", "AAAA"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(insert, "app.example.com", "A"); err == nil {
		t.Fatal("a second row for one name and type was accepted")
	}
}
