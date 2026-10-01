package dbx

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		query       string
		level       string
		destructive bool
	}{
		{"SELECT * FROM users", "read", false},
		{"select id from orders where total > 10", "read", false},
		{"DELETE FROM sessions WHERE expires_at < now()", "high", true},
		{"DELETE FROM sessions", "critical", true},
		{"delete\nfrom sessions", "critical", true},
		{"UPDATE users SET role = 'admin' WHERE id = 3", "high", true},
		{"UPDATE users SET role = 'admin'", "critical", true},
		{"DROP TABLE audit_log", "critical", true},
		{"TRUNCATE orders", "critical", true},
		{"INSERT INTO users(name) VALUES('a')", "medium", false},
		{"CREATE INDEX idx ON users(name)", "medium", false},
		{"ALTER TABLE users ADD COLUMN x int", "high", true},

		// Everything below classified as a harmless read before the classifier
		// stripped comments and started failing closed. Each one ran for a
		// "limited" account with no destructive capability and no typed
		// confirmation, because the query runner derives both from this
		// verdict rather than from the route.
		{"DELETE/**/FROM users", "critical", true},
		{"DELETE\t/*x*/ FROM users", "critical", true},
		{"DROP ROLE app_user", "critical", true},
		{"DROP OWNED BY app_user", "critical", true},
		{"DROP FUNCTION public.f()", "critical", true},
		{"COPY t FROM PROGRAM 'id > /tmp/pwned'", "critical", true},
		{"DO $$ BEGIN PERFORM 1; END $$", "high", true},
		{"CALL wipe_everything()", "high", true},
		{"VACUUM FULL", "high", true},

		// A comment marker inside a string literal is data, not a comment:
		// stripping to end of line here would hide the DELETE behind it.
		{"SELECT 'x--' FROM t; DELETE FROM users", "critical", true},

		// ...and the price of that is over-reporting a SELECT whose text
		// happens to contain a verb, which is the side of wrong this
		// classifier is allowed to be on.
		{"SELECT 'delete' FROM t", "critical", true},
	}
	for _, c := range cases {
		got := Classify(c.query)
		if got.Level != c.level {
			t.Errorf("Classify(%q).Level = %q, want %q", c.query, got.Level, c.level)
		}
		if got.Destructive != c.destructive {
			t.Errorf("Classify(%q).Destructive = %v, want %v", c.query, got.Destructive, c.destructive)
		}
	}
}

func TestReturnsRows(t *testing.T) {
	cases := map[string]bool{
		"SELECT 1":                  true,
		"  -- a comment\n SELECT 1": true,
		"/* block */ WITH x AS (SELECT 1) SELECT * FROM x": true,
		"INSERT INTO t(a) VALUES(1)":                       false,
		"INSERT INTO t(a) VALUES(1) RETURNING a":           true,
		"UPDATE t SET a = 1":                               false,
		"SHOW TABLES":                                      true,
		// A parenthesised query leads with SELECT all the same; the prefix test
		// saw "(" and ran it through Exec, which threw the rows away.
		"(SELECT 1) UNION (SELECT 2)": true,
		// The word in a string is data. Matching " returning " in the text sent
		// this UPDATE through Query.
		"UPDATE t SET note = ' returning ' WHERE id = 1": false,
		// A CTE that only writes has no rows to hand back.
		"WITH old AS (SELECT 1) DELETE FROM t WHERE id IN (SELECT * FROM old)": false,
		"WITH gone AS (DELETE FROM t RETURNING id) SELECT * FROM gone":         true,
		"CALL report()": true,
	}
	for query, want := range cases {
		if got := returnsRows(query); got != want {
			t.Errorf("returnsRows(%q) = %v, want %v", query, got, want)
		}
	}
}
