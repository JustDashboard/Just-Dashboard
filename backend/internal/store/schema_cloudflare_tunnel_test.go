package store

import (
	"strings"
	"testing"
)

func TestCloudflareTunnelSchemaOnlyAddsTables(t *testing.T) {
	for _, statement := range strings.Split(sqlComment.ReplaceAllString(cloudflareTunnelSchema, ""), ";") {
		words := strings.Fields(strings.ToUpper(statement))
		if len(words) == 0 {
			continue
		}
		if words[0] != "CREATE" || !strings.Contains(strings.Join(words, " "), " IF NOT EXISTS ") {
			t.Fatal("nonadditive migration", statement)
		}
	}
}

// One connection at most: a second row is refused by the table itself.
func TestCloudflareTunnelIsOneRow(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.DB.Exec(`INSERT INTO proxy_cloudflare_tunnel(id, mode, tunnel_id, token_enc, created_at, updated_at) VALUES(1, 'token', 't', 'sealed', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO proxy_cloudflare_tunnel(id, mode, tunnel_id, token_enc, created_at, updated_at) VALUES(2, 'token', 't', 'sealed', 1, 1)`); err == nil {
		t.Fatal("a second tunnel row was accepted")
	}
}
