package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAssistantChatSchemaOnlyAddsTables(t *testing.T) {
	for _, statement := range strings.Split(sqlComment.ReplaceAllString(assistantChatSchema, ""), ";") {
		words := strings.Fields(strings.ToUpper(statement))
		if len(words) == 0 {
			continue
		}
		if words[0] != "CREATE" || !strings.Contains(strings.Join(words, " "), " IF NOT EXISTS ") {
			t.Fatal("nonadditive migration", statement)
		}
	}
}

// The tables arrive on a new install and on one upgraded from 0.6.6, and a chat
// goes with its owner, with the deployment it is about, and its lines with it.
func TestAssistantChatsReachFreshAndUpgradedInstalls(t *testing.T) {
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
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := st.DB.Exec(query, args...); err != nil {
					t.Fatalf("%v\n%s", err, query)
				}
			}
			exec(`INSERT INTO users(username, password_hash, role, created_at) VALUES('chat-owner', 'x', 'admin', 0)`)
			var owner int64
			if err := st.DB.QueryRow(`SELECT id FROM users WHERE username = 'chat-owner'`).Scan(&owner); err != nil {
				t.Fatal(err)
			}
			exec(`INSERT INTO deploy_projects(name, repo_path, hook_secret, hook_id, created_at) VALUES('chat-shop', '/srv/shop', 's', 'chat-hook', 0)`)
			var project int64
			if err := st.DB.QueryRow(`SELECT id FROM deploy_projects WHERE name = 'chat-shop'`).Scan(&project); err != nil {
				t.Fatal(err)
			}
			exec(`INSERT INTO assistant_chats(id, owner_id, scope_kind, deployment_id, created_at, updated_at) VALUES('a', ?, 'deployment', ?, 1, 1)`, owner, project)
			exec(`INSERT INTO assistant_chats(id, owner_id, scope_kind, created_at, updated_at) VALUES('b', ?, 'server', 1, 1)`, owner)
			exec(`INSERT INTO assistant_chat_lines(chat_id, seq, lines_enc) VALUES('a', 0, 'x'), ('b', 0, 'y')`)
			count := func(query string) int {
				var n int
				if err := st.DB.QueryRow(query).Scan(&n); err != nil {
					t.Fatal(err)
				}
				return n
			}
			exec(`DELETE FROM deploy_projects WHERE id = ?`, project)
			if count(`SELECT COUNT(*) FROM assistant_chats`) != 1 || count(`SELECT COUNT(*) FROM assistant_chat_lines`) != 1 {
				t.Fatal("a deployment's chat did not go with it")
			}
			exec(`DELETE FROM users WHERE id = ?`, owner)
			if count(`SELECT COUNT(*) FROM assistant_chats`) != 0 || count(`SELECT COUNT(*) FROM assistant_chat_lines`) != 0 {
				t.Fatal("a person's chats did not go with them")
			}
		})
	}
}
