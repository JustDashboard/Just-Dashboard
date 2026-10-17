package dbx

import (
	"strings"
	"testing"
)

// Oracle hands the text over with its line breaks turned into spaces, so a --
// comment cannot be told from the code after it: the rest goes, and what an
// application wrote in the comment is not shown.
func TestStatementShapeKeepsNoCommentText(t *testing.T) {
	for _, c := range []struct {
		name, in, want string
		brackets       bool
	}{
		{"line comment flattened by Oracle", "SELECT 1 -- note for jane@example.com WHERE x = 'tok_live'", "SELECT ? ", false},
		{"line comment before more code", "-- trace 7731\nSELECT 'a' FROM t", "\nSELECT ? FROM t", true},
		{"block comment", "SELECT /* user=4711 */ a FROM t", "SELECT a FROM t", false},
		{"block comment holding a string", "SELECT a /* it's 'x */ FROM t WHERE b = 'y'", "SELECT a FROM t WHERE b = ?", false},
		{"unterminated block comment", "SELECT a FROM t /* user=4711", "SELECT a FROM t ", false},
		{"comment between tokens still separates them", "SELECT a/*c*/FROM t", "SELECT a FROM t", false},
		{"T-SQL nests its comments", "SELECT 1 /* a /* b */ secret-word */ FROM t", "SELECT ? FROM t", true},
		{"others do not", "SELECT 1 /* a /* b */ FROM t WHERE c = 'x'", "SELECT ? FROM t WHERE c = ?", false},
		{"a comment marker inside a string is the string's", "SELECT '-- not /* a comment' FROM t", "SELECT ? FROM t", false},
		{"a comment marker inside an identifier is the name's", `SELECT "a--b" FROM t -- c`, `SELECT "a--b" FROM t `, false},
		// A hint is read by the server, so it stays; the literals in it do not.
		{"hint", "SELECT /*+ opt_param('parallel_execution_enabled', 'false') FULL(t) */ a FROM t WHERE n = 5", "SELECT /*+ opt_param(?, ?) FULL(t) */ a FROM t WHERE n = ?", false},
		{"line hint", "SELECT --+ FIRST_ROWS(10) 'x'", "SELECT --+ FIRST_ROWS(?) ?", false},
		{"hint cut short", "SELECT /*+ opt_param('a', 'sec", "SELECT /*+ opt_param(?, ?", false},
	} {
		if got := statementShape(c.in, c.brackets); got != c.want {
			t.Errorf("%s: statementShape(%q)\n got %q\nwant %q", c.name, c.in, got, c.want)
		}
	}
}

// The forms of an Oracle statement that carry data, written the way the
// shared pool stores them. Not one of the secrets may survive.
func TestStatementShapeOracleStatementsCarryNoData(t *testing.T) {
	secrets := []string{"s3cret", "9988776655", "4711", "jane@example.com", "tok_live", "LONGSTRING"}
	for name, in := range map[string]string{
		"insert":       `INSERT INTO accounts (id, email, pin, note) VALUES (4711, 'jane@example.com', 9988776655, 's3cret')`,
		"insert all":   `INSERT ALL INTO t (a, b) VALUES (4711, 'tok_live') INTO t (a, b) VALUES (9988776655, N's3cret') SELECT 1 FROM dual`,
		"merge":        `MERGE INTO t d USING (SELECT 4711 AS id, q'[jane@example.com]' AS email FROM dual) s ON (d.id = s.id) WHEN MATCHED THEN UPDATE SET d.email = s.email, d.note = 's3cret' WHEN NOT MATCHED THEN INSERT (id, email) VALUES (s.id, 'tok_live')`,
		"plsql":        `BEGIN UPDATE t SET note = 's3cret' WHERE id = 4711; v_mail := 'jane@example.com'; v_pin := 9988776655; v_tok := q'#tok_live#'; END;`,
		"plsql block":  `DECLARE v_n NUMBER := 4711; v_s VARCHAR2(40) := nq'<s3cret>'; BEGIN INSERT INTO t VALUES (v_n, v_s || 'tok_live'); END;`,
		"exponent":     `SELECT * FROM t WHERE x > 9.988776655E+9 AND y = 4.711e2`,
		"typed":        `SELECT DATE '2024-01-02', TIMESTAMP '2024-01-02 03:04:05', INTERVAL '4711' DAY FROM dual`,
		"comment":      `SELECT * FROM t WHERE a = 1 -- jane@example.com tok_live`,
		"block":        `SELECT /* request s3cret */ * FROM t`,
		"cut mid-text": `INSERT INTO t VALUES ('` + strings.Repeat("LONGSTRING", 3),
	} {
		got := statementShape(in, false)
		for _, secret := range secrets {
			if strings.Contains(got, secret) {
				t.Errorf("%s: %q survived in %q", name, secret, got)
			}
		}
	}
}
