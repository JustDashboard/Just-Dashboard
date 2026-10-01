package dbx

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"
)

// postgresStatements reads a psql script through: each statement, and for a
// COPY the rows that followed it.
func postgresStatements(script string) (statements, copies []string, err error) {
	r := newPostgresScript(strings.NewReader(script))
	for {
		st, err := r.next()
		if err == io.EOF {
			return statements, copies, nil
		}
		if err != nil {
			return statements, copies, err
		}
		statements = append(statements, st.sql)
		if st.copyIn {
			rows, err := io.ReadAll(r.copyData())
			if err != nil {
				return statements, copies, err
			}
			copies = append(copies, string(rows))
		}
	}
}

func mysqlStatements(script string) ([]string, error) {
	r := newMySQLScript(strings.NewReader(script))
	var out []string
	for {
		st, err := r.next()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		out = append(out, st.sql)
	}
}

// A psql script is cut where psql cuts it. The cases are the places a
// semicolon is not the end of a statement, each of which pg_dump writes.
func TestPostgresScriptIsCutWherePsqlCutsIt(t *testing.T) {
	for _, c := range []struct {
		name, script string
		want         []string
	}{
		{"the header a dump opens with",
			"--\n-- PostgreSQL database dump\n--\n\nSET statement_timeout = 0;\nSELECT pg_catalog.set_config('search_path', '', false);\n",
			[]string{"SET statement_timeout = 0", "SELECT pg_catalog.set_config('search_path', '', false)"}},
		{"a semicolon in a string, and a doubled quote",
			"INSERT INTO t VALUES ('a;b', 'it''s; fine');SELECT 2;",
			[]string{"INSERT INTO t VALUES ('a;b', 'it''s; fine')", "SELECT 2"}},
		{"a backslash ends nothing in a plain string, and escapes in an E string",
			`INSERT INTO t VALUES ('C:\dir\'); INSERT INTO t VALUES (E'it\'s; one');`,
			[]string{`INSERT INTO t VALUES ('C:\dir\')`, `INSERT INTO t VALUES (E'it\'s; one')`}},
		{"a quoted name",
			`CREATE TABLE "odd;name" ("a""b" int); SELECT 1;`,
			[]string{`CREATE TABLE "odd;name" ("a""b" int)`, "SELECT 1"}},
		{"a function body between dollars",
			"CREATE FUNCTION f() RETURNS int LANGUAGE plpgsql AS $$\nBEGIN\n  RETURN 1; -- not the end\nEND;\n$$;\nSELECT 1;",
			[]string{"CREATE FUNCTION f() RETURNS int LANGUAGE plpgsql AS $$\nBEGIN\n  RETURN 1; -- not the end\nEND;\n$$", "SELECT 1"}},
		{"a tagged body that holds two dollars",
			"DO $body$ BEGIN PERFORM '$$; $x$'; END $body$; SELECT $1;",
			[]string{"DO $body$ BEGIN PERFORM '$$; $x$'; END $body$", "SELECT $1"}},
		{"a function written in SQL",
			"CREATE OR REPLACE FUNCTION f(a int) RETURNS int LANGUAGE sql\n    BEGIN ATOMIC\n SELECT CASE WHEN a > 0 THEN 1 ELSE 2 END;\n SELECT 2;\nEND;\nSELECT 3;",
			[]string{"CREATE OR REPLACE FUNCTION f(a int) RETURNS int LANGUAGE sql\n    BEGIN ATOMIC\n SELECT CASE WHEN a > 0 THEN 1 ELSE 2 END;\n SELECT 2;\nEND", "SELECT 3"}},
		{"a rule with two actions",
			"CREATE RULE r AS ON INSERT TO t DO ALSO (INSERT INTO a VALUES (1); INSERT INTO b VALUES (2));SELECT 1;",
			[]string{"CREATE RULE r AS ON INSERT TO t DO ALSO (INSERT INTO a VALUES (1); INSERT INTO b VALUES (2))", "SELECT 1"}},
		{"comments, nested and not",
			"/* one /* two; */ still; */ SELECT 1 /* inside; */ + 2; -- trailing; \nSELECT 3",
			[]string{"SELECT 1   + 2", "SELECT 3"}},
		{"the brackets pg_dump has written since 2025",
			"\\restrict AbC123\n\nSET lock_timeout = 0;\n\\unrestrict AbC123\n",
			[]string{"SET lock_timeout = 0"}},
		{"a number is not the E before a string",
			`SELECT 1e'x\'; SELECT 2;`,
			[]string{`SELECT 1e'x\'`, "SELECT 2"}},
	} {
		got, _, err := postgresStatements(c.script)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got %q (%v)\nwant %q", c.name, got, err, c.want)
		}
	}
}

// The rows of a COPY are not statements, and nothing in them is read as one:
// a row may hold a quote, a semicolon, or a backslash and a word.
func TestPostgresScriptHandsOverCopyRows(t *testing.T) {
	long := strings.Repeat("x", 600_000)
	script := "CREATE TABLE public.t (id integer, name text);\n" +
		"COPY public.t (id, name) FROM stdin;\n" +
		"1\tit's; one\n" +
		"2\t\\\\connect other\n" +
		"3\t" + long + "\n" +
		"\\.\n\n" +
		"copy \"from\" FROM STDIN WITH (FORMAT csv); -- its rows follow\r\n" +
		"\\.x\r\n" +
		"\\.\r\n" +
		"COPY (SELECT a FROM stdin) TO stdout;\n" +
		"SELECT pg_catalog.setval('public.t_id_seq', 3, true);\n"
	statements, copies, err := postgresStatements(script)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{
		"CREATE TABLE public.t (id integer, name text)",
		"COPY public.t (id, name) FROM stdin",
		`copy "from" FROM STDIN WITH (FORMAT csv)`,
		"COPY (SELECT a FROM stdin) TO stdout",
		"SELECT pg_catalog.setval('public.t_id_seq', 3, true)",
	}; !reflect.DeepEqual(statements, want) {
		t.Errorf("statements = %q", statements)
	}
	if want := []string{"1\tit's; one\n2\t\\\\connect other\n3\t" + long + "\n", "\\.x\r\n"}; !reflect.DeepEqual(copies, want) {
		t.Errorf("rows = %.200q", copies)
	}

	// Whatever size the reader is asked in, the rows come out the same.
	r := newPostgresScript(strings.NewReader(script))
	r.next()
	if st, err := r.next(); err != nil || !st.copyIn || st.line != 2 {
		t.Fatalf("second statement = %+v, %v", st, err)
	}
	rows, err := io.ReadAll(iotest.OneByteReader(r.copyData()))
	if err != nil || string(rows) != copies[0] {
		t.Errorf("read a byte at a time: %d bytes, %v", len(rows), err)
	}
	if st, err := r.next(); err != nil || st.line != 8 {
		t.Errorf("the statement after the rows = %+v, %v; want it found on line 8", st, err)
	}

	// Rows that are never closed end where the script does.
	if _, copies, err := postgresStatements("COPY t FROM stdin;\n1\n2"); err != nil || len(copies) != 1 || copies[0] != "1\n2" {
		t.Errorf("an unterminated COPY: %q, %v", copies, err)
	}
	if _, _, err := postgresStatements("COPY t FROM stdin; SELECT 1;\n1\n\\.\n"); err == nil {
		t.Error("a statement on the line of a COPY was accepted, and would have been read as its first row")
	}
}

// What only psql can do is refused wherever psql would have read it, and not
// where it would not.
func TestPostgresScriptRefusesWhatOnlyPsqlRuns(t *testing.T) {
	for script, want := range map[string]string{
		"\\connect other\nCREATE TABLE t (id int);\n": `\connect other`,
		"SELECT 1;\n\\c other\n":                      `\c other`,
		"SELECT 1;\n   \\connect other\n":             `\connect other`,
		"SELECT 1; \\connect \"other\" - - 5433\n":    `\connect "other" - - 5433`,
		"SELECT 1 \\c other\n":                        `\c other`,
	} {
		_, _, err := postgresStatements(script)
		reconnects, ok := err.(*scriptReconnectError)
		if !ok || reconnects.command != want {
			t.Errorf("%q: %v, want a refusal naming %s", script, err, want)
		}
	}
	for _, script := range []string{
		"\\! touch /tmp/x\n",
		"SELECT 1;\n  \\! touch /tmp/x\n",
		"SELECT 1 \\gexec\n",
		"\\i /etc/passwd\n",
		"\\copy t from program 'id'\n",
		"\\o |cat\nSELECT 1;\n",
		"SELECT 'a' \\\\ \\! id\n",
		"\\set x 1\n",
		// Outside a COPY this ends nothing.
		"SELECT 1;\n\\.\n",
	} {
		if _, _, err := postgresStatements(script); err == nil || !strings.Contains(err.Error(), "only psql runs") {
			t.Errorf("%q: %v, want it refused as psql's own", script, err)
		}
	}
	for _, script := range []string{
		"SELECT '\\! not a command';",
		"SELECT E'\\'\\! still a string';",
		"SELECT $$ \\connect other $$;",
		"-- \\connect other\nSELECT 1;",
		"/* \\! id */ SELECT 1;",
		`SELECT "\connect";`,
		"COPY t (v) FROM stdin;\n\\\\connect other\n\\! id\n\\.\n",
	} {
		if _, _, err := postgresStatements(script); err != nil {
			t.Errorf("%q: %v, want it read as data", script, err)
		}
	}
}

func TestEndsTransaction(t *testing.T) {
	for statement, want := range map[string]bool{
		"COMMIT": true, "commit work": true, "END": true, "ROLLBACK": true, "abort": true,
		"PREPARE TRANSACTION 'x'": true,
		"ROLLBACK TO SAVEPOINT s": false, "PREPARE q AS SELECT 1": false, "BEGIN": false,
		"SELECT 'commit'": false, "COMMENT ON TABLE t IS 'x'": false,
	} {
		if got := endsTransaction(statement); got != want {
			t.Errorf("endsTransaction(%q) = %v", statement, got)
		}
	}
}

// A script for the mysql client is cut where the client cuts it, and what a
// server is meant to read reaches it: a conditional comment is statement text.
func TestMySQLScriptIsCutWhereTheClientCutsIt(t *testing.T) {
	for _, c := range []struct {
		name, script string
		want         []string
	}{
		{"the header mysqldump opens with",
			"-- MySQL dump 10.13  Distrib 8.4.2\n--\n-- Host: db    Database: shop\n\n" +
				"/*!40101 SET @OLD_CHARACTER_SET_CLIENT=@@CHARACTER_SET_CLIENT */;\n/*!50503 SET NAMES utf8mb4 */;\n",
			[]string{"/*!40101 SET @OLD_CHARACTER_SET_CLIENT=@@CHARACTER_SET_CLIENT */", "/*!50503 SET NAMES utf8mb4 */"}},
		{"the line mariadb-dump opens with",
			"/*M!999999\\- enable the sandbox mode */ \n-- MariaDB dump 10.19\n/*!40101 SET NAMES utf8mb4 */;\n",
			[]string{"/*M!999999\\- enable the sandbox mode */ \n\n/*!40101 SET NAMES utf8mb4 */"}},
		{"strings with what mysqldump escapes",
			`INSERT INTO t VALUES ('O\'Brien; \\','a''b;',"say \"hi\";"),(2,'x');` + "\nSELECT 1;",
			[]string{`INSERT INTO t VALUES ('O\'Brien; \\','a''b;',"say \"hi\";"),(2,'x')`, "SELECT 1"}},
		{"a backticked name, where a backslash is a backslash",
			"CREATE TABLE `odd;\\` (`a``b` int); SELECT 1;",
			[]string{"CREATE TABLE `odd;\\` (`a``b` int)", "SELECT 1"}},
		{"comments of the three kinds",
			"# one; two\nSELECT 1 -- three;\n + 2 /* four; */ + 5--3;\n--\nSELECT 6;\n-- the end",
			[]string{"SELECT 1 \n + 2   + 5--3", "SELECT 6"}},
		{"a trigger as mysqldump writes one",
			"DELIMITER ;;\n/*!50003 CREATE*/ /*!50017 DEFINER=`root`@`%`*/ /*!50003 TRIGGER `t_bi` BEFORE INSERT ON `t` FOR EACH ROW BEGIN\n" +
				"  SET NEW.a = 1; SET NEW.b = ';;';\nEND */;;\nDELIMITER ;\nSELECT 1;\n",
			[]string{"/*!50003 CREATE*/ /*!50017 DEFINER=`root`@`%`*/ /*!50003 TRIGGER `t_bi` BEFORE INSERT ON `t` FOR EACH ROW BEGIN\n" +
				"  SET NEW.a = 1; SET NEW.b = ';;';\nEND */", "SELECT 1"}},
		{"a routine, a longer delimiter, and a last statement left open",
			"  delimiter $$  \nCREATE PROCEDURE p()\nBEGIN\n  SELECT 1;\nEND $$\nDELIMITER ;\nSELECT 'delimiter ;;'\n;\nSELECT 2",
			[]string{"CREATE PROCEDURE p()\nBEGIN\n  SELECT 1;\nEND", "SELECT 'delimiter ;;'", "SELECT 2"}},
		{"NULL the way a load file spells it",
			`SELECT \N;`,
			[]string{`SELECT \N`}},
		{"a USE as the client takes one, with no delimiter",
			"use shop\nCREATE TABLE t (id int);\nUSE `shop`;\nSELECT 1;",
			[]string{"USE shop", "CREATE TABLE t (id int)", "USE `shop`", "SELECT 1"}},
		{"nothing between two delimiters",
			";;\n /* only a comment */ ;\nSELECT 1;",
			[]string{"SELECT 1"}},
	} {
		got, err := mysqlStatements(c.script)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got %q (%v)\nwant %q", c.name, got, err, c.want)
		}
	}
}

// The client's own commands are refused, each by the line it stands on.
func TestMySQLScriptRefusesWhatOnlyTheClientRuns(t *testing.T) {
	for script, want := range map[string]string{
		"SELECT 1;\n\\! touch /tmp/x\n":            "line 2",
		"SELECT 1; \\u other\n":                    `\u`,
		"\\. /etc/passwd\n":                        `\.`,
		"SELECT 1\\G\n":                            `\G`,
		"/*!50000 \\! id */;\n":                    `\!`,
		"SELECT 1;\n-- fine\n\n\\r other host\n":   "line 4",
		"DELIMITER\nSELECT 1;\n":                   "names no delimiter",
		"DELIMITER   \nSELECT 1;\n":                "names no delimiter",
		"SELECT 'a\\'; \\! in a string';\n":        "",
		"SELECT 1; # \\! in a comment\n":           "",
		"SELECT 1; /* \\! in a comment */\n":       "",
		"SELECT `\\!`;\n":                          "",
		"INSERT INTO t VALUES ('x\\\\'); \\! id\n": `\!`,
		// The commands the client knows by name, where it would take them: on
		// a line that begins a statement and does not hold the delimiter.
		"system touch /tmp/x\n":                "client's system",
		"SELECT 1;\n  SOURCE /etc/passwd\n":    "line 2",
		"SELECT 1;\nconnect other otherhost\n": "client's connect",
		"tee /tmp/out\nSELECT 1;\n":            "client's tee",
		"SELECT\nsystem\nFROM t;\n":            "",
		"help contents;\n":                     "",
		"CREATE TABLE source (id int);\n":      "",
	} {
		_, err := mysqlStatements(script)
		switch {
		case want == "" && err != nil:
			t.Errorf("%q: %v, want it read", script, err)
		case want != "" && (err == nil || !strings.Contains(err.Error(), want)):
			t.Errorf("%q: %v, want a refusal naming %s", script, err, want)
		}
	}
}

func TestMySQLUse(t *testing.T) {
	for statement, want := range map[string]string{
		"USE other":                           "other",
		"use `other`":                         "other",
		"USE`odd``name`":                      "odd`name",
		"  \n USE\tother ":                    "other",
		"/*!50000 USE other */":               "other",
		"/*M!100100 use `other`*/":            "other",
		"/*!40101 SET x=1 */":                 "-",
		"/*!32312 IF NOT EXISTS*/ USE other":  "-",
		"/*+ hint */ USE other":               "other",
		"/*!50000 */ /*!50000 USE `other` */": "other",
		"USER":                                "-",
		"SELECT 'USE other'":                  "-",
		"INSERT INTO `use` VALUES (1)":        "-",
		"USE":                                 "-",
		"":                                    "-",
	} {
		name, ok := mysqlUse(statement)
		if !ok {
			name = "-"
		}
		if name != want {
			t.Errorf("mysqlUse(%q) = %q, want %q", statement, name, want)
		}
	}
}

// A script that would carry on in another database is refused before any of
// it runs, wherever in a line the USE stands, and one that names the database
// it is being restored into is not that.
func TestAMySQLScriptThatLeavesItsDatabaseIsRefused(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, content []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	long := strings.Repeat("x", 300_000)
	for _, c := range []struct {
		script string
		leaves bool
	}{
		{"CREATE TABLE t (id int);\nINSERT INTO t VALUES (1);\n", false},
		{"USE `shop`;\nCREATE TABLE t (id int);\n", false},
		{"CREATE DATABASE `other`;\n\nUSE `other`;\nCREATE TABLE t (id int);\n", true},
		{"use other ;\n", true},
		// Not at the start of a line, which is all that used to be looked at.
		{"SELECT 1;USE other;\nCREATE TABLE t (id int);\n", true},
		{"SELECT 1;\n   USE other;\n", true},
		{"SELECT 1; /* quietly */ USE other;\n", true},
		{"-- a comment\n\tUSE other;\n", true},
		{"/*!50000 USE other */;\n", true},
		{"use other\nCREATE TABLE t (id int);\n", true},
		{"use shop\nCREATE TABLE t (id int);\n", false},
		{"DELIMITER //\nSELECT 1//USE other//\n", true},
		// A value that mentions USE is a value.
		{"INSERT INTO t VALUES ('" + long + "\\nUSE `other`;');\n", false},
		{"INSERT INTO t VALUES ('a;\nUSE other;\n');\n", false},
		{"DELIMITER ;;\nCREATE PROCEDURE p() BEGIN SELECT 'x'; USE other; END;;\nDELIMITER ;\n", false},
	} {
		err := checkMySQLScript(write("my.sql", []byte(c.script)), "shop")
		if c.leaves != (err != nil && strings.Contains(err.Error(), "switches to another database")) {
			t.Errorf("checkMySQLScript(%.60q) = %v, want refused: %v", c.script, err, c.leaves)
		}
	}

	// Compressed, it is read the same way.
	var packed bytes.Buffer
	gz := gzip.NewWriter(&packed)
	gz.Write([]byte("SELECT 1;USE other;\n"))
	gz.Close()
	if err := checkMySQLScript(write("my.sql.gz", packed.Bytes()), "shop"); err == nil {
		t.Error("a compressed script that switches database was accepted")
	}
}

// One statement is bounded, so a file that is all one string is not read into
// memory to find that out.
func TestScriptStatementIsBounded(t *testing.T) {
	my := newMySQLScript(strings.NewReader("SELECT 1;\nSELECT '" + strings.Repeat("x", 200) + "';\nSELECT 2;"))
	my.text.max = 64
	if st, err := my.next(); err != nil || st.sql != "SELECT 1" {
		t.Fatalf("first statement = %q, %v", st.sql, err)
	}
	if _, err := my.next(); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Errorf("an oversized statement: %v, want it refused by its line", err)
	}
	pg := newPostgresScript(strings.NewReader("SELECT $$" + strings.Repeat("x", 200) + "$$;"))
	pg.text.max = 64
	if _, err := pg.next(); err == nil {
		t.Error("an oversized statement was returned")
	}
}

// A connection that replays a file sends one statement at a time, whatever
// the saved connection string asks for.
func TestMySQLOneStatementDSN(t *testing.T) {
	plain := "app:secret@tcp(db:3306)/shop?parseTime=true"
	if got := mysqlOneStatementDSN(plain); got != plain {
		t.Errorf("a string that never asked for it was rewritten: %s", got)
	}
	got := mysqlOneStatementDSN("app:secret@tcp(db:3306)/shop?multiStatements=true&parseTime=true")
	if strings.Contains(got, "multiStatements") || !strings.Contains(got, "app:secret@tcp(db:3306)/shop") || !strings.Contains(got, "parseTime=true") {
		t.Errorf("mysqlOneStatementDSN = %s", got)
	}
}
