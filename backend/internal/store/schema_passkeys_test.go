package store

import (
	"strings"
	"testing"
)

func TestPasskeySchemaOnlyAddsTables(t *testing.T) {
	for _, statement := range strings.Split(sqlComment.ReplaceAllString(passkeySchema, ""), ";") {
		words := strings.Fields(strings.ToUpper(statement))
		if len(words) == 0 {
			continue
		}
		if words[0] != "CREATE" || !strings.Contains(strings.Join(words, " "), " IF NOT EXISTS ") {
			t.Fatal("nonadditive migration", statement)
		}
	}
}

// A credential id is one account's alone, and an account's passkeys go with
// the account.
func TestPasskeyCredentialsAreUniqueAndGoWithTheirAccount(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	result, err := st.DB.Exec(`INSERT INTO users(username, password_hash, role, created_at) VALUES('keyholder', 'x', 'admin', 1)`)
	if err != nil {
		t.Fatal(err)
	}
	user, _ := result.LastInsertId()
	insert := `INSERT INTO webauthn_credentials(user_id, credential_id, credential_json, rp_id, name, created_at) VALUES(?, ?, '{}', 'dash.example.test', 'laptop', 1)`
	if _, err := st.DB.Exec(insert, user, "cred-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(insert, user, "cred-1"); err == nil {
		t.Fatal("a credential id was stored twice")
	}
	if _, err := st.DB.Exec(`DELETE FROM users WHERE id = ?`, user); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM webauthn_credentials`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("passkeys left after their account went: %d, %v", left, err)
	}
}
