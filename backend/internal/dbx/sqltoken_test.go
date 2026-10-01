package dbx

import (
	"math/rand"
	"regexp"
	"strings"
	"testing"
)

func statementTexts(t *testing.T, driver Driver, query string) []string {
	t.Helper()
	statements, err := ParseScript(driver, query)
	if err != nil {
		t.Fatalf("ParseScript(%s, %q): %v", driver, query, err)
	}
	out := make([]string, len(statements))
	for i, st := range statements {
		out[i] = st.SQL
	}
	return out
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The same characters are different things to different engines, and the
// splitter has to read each the way the engine that will run the text does.
func TestSplittingFollowsTheEngine(t *testing.T) {
	cases := []struct {
		driver Driver
		query  string
		want   []string
	}{
		// A semicolon inside any quoted region is data.
		{DriverPostgres, `SELECT ';' ; SELECT ";" FROM t`, []string{`SELECT ';'`, `SELECT ";" FROM t`}},
		{DriverMySQL, "SELECT `a;b` FROM t; SELECT 2", []string{"SELECT `a;b` FROM t", "SELECT 2"}},
		{DriverMSSQL, `SELECT [a;b] FROM t; SELECT 2`, []string{`SELECT [a;b] FROM t`, `SELECT 2`}},
		{DriverMSSQL, `SELECT [a]];b] FROM t`, []string{`SELECT [a]];b] FROM t`}},
		// SQLite's [name] has no escape: it ends at the first bracket.
		{DriverSQLite, `SELECT [a] ; SELECT 2`, []string{`SELECT [a]`, `SELECT 2`}},
		// On PostgreSQL a bracket is an array subscript and a backtick is an
		// operator character, so neither opens anything.
		{DriverPostgres, `SELECT a[1] ; SELECT 2`, []string{`SELECT a[1]`, `SELECT 2`}},
		{DriverPostgres, "SELECT 1 ` 2 ; SELECT 3", []string{"SELECT 1 ` 2", "SELECT 3"}},
		// # is a comment on MySQL, a JSON operator on PostgreSQL, part of a
		// name on SQL Server and Oracle.
		{DriverMySQL, "SELECT 1 # ; DROP TABLE t\n; SELECT 2", []string{"SELECT 1", "SELECT 2"}},
		{DriverPostgres, `SELECT doc #> '{a}' FROM t; SELECT 2`, []string{`SELECT doc #> '{a}' FROM t`, `SELECT 2`}},
		{DriverMSSQL, `SELECT * FROM #tmp; SELECT 2`, []string{`SELECT * FROM #tmp`, `SELECT 2`}},
		{DriverOracle, `SELECT serial# FROM v$session; SELECT 2 FROM dual`,
			[]string{`SELECT serial# FROM v$session`, `SELECT 2 FROM dual`}},
		// Comments around a statement are not part of what is sent.
		{DriverPostgres, "/* lead */ SELECT 1 -- trail\n;\n-- only a comment\n", []string{"SELECT 1"}},
		// A comment inside one stays with it.
		{DriverPostgres, "SELECT /*+ hint */ 1 -- note\n FROM t", []string{"SELECT /*+ hint */ 1 -- note\n FROM t"}},
		// Backslashes that every reading agrees about are fine: a Windows path,
		// a regular expression.
		{DriverMySQL, `SELECT 'C:\\dir\\' ; SELECT '\d+'`, []string{`SELECT 'C:\\dir\\'`, `SELECT '\d+'`}},
		{DriverSQLite, `SELECT 'a\' ; SELECT 2`, []string{`SELECT 'a\'`, `SELECT 2`}},
	}
	for _, c := range cases {
		if got := statementTexts(t, c.driver, c.query); !sameStrings(got, c.want) {
			t.Errorf("%s: %q\n split into %q\n      want %q", c.driver, c.query, got, c.want)
		}
	}
}

// Everything here is text whose meaning depends on something this code cannot
// see, or that an engine executes while it looks like a comment. All of it is
// refused, and a refusal classifies as destructive.
func TestTheLexerRefusesWhatItCannotAttribute(t *testing.T) {
	cases := []struct {
		driver Driver
		query  string
	}{
		{DriverPostgres, `SELECT 'a\' ; DROP TABLE t; --'`},    // E'' or standard_conforming_strings off
		{DriverMySQL, `SELECT 'a\' ; DROP TABLE t; --'`},       // NO_BACKSLASH_ESCAPES
		{DriverMySQL, `SELECT "a\\\" ; DROP TABLE t; --"`},     // an odd run in front of the quote
		{DriverClickHouse, "SELECT `a\\` ; DROP TABLE t; --`"}, // backslash escapes in identifiers too
		{DriverMySQL, "SELECT 1--1"},
		{DriverMySQL, "SELECT 1 /*!50000 ; DROP TABLE t */"},
		{DriverMySQL, "SELECT 1 /*M! ; DROP TABLE t */"},
		{DriverMySQL, "SELECT 1 /*T![feature] ; DROP TABLE t */"},
		{DriverPostgres, "SELECT 1 /* a /* b */ ; DROP TABLE t; */"},
		{DriverMSSQL, "SELECT 1 /* a /* b */ ; DROP TABLE t; */"},
		{DriverPostgres, "SELECT 1 /* never closed"},
		{DriverPostgres, "SELECT 'never closed"},
		{DriverPostgres, "SELECT $$ never closed"},
		{DriverPostgres, "SELECT 1 -- comment\rDROP TABLE t\n"},
		{DriverMySQL, "SELECT 1 # comment\rDROP TABLE t\n"},
		{DriverPostgres, "SELECT 1\x00; DROP TABLE t"},
		{DriverOracle, `SELECT q'[it's]' FROM dual`},
		{DriverOracle, `SELECT nQ'{a}' FROM dual`},
		{DriverClickHouse, "SELECT $doc$ ; DROP TABLE t; $doc$"},
		{DriverClickHouse, "SELECT 1 # comment"},
		{DriverClickHouse, "SELECT ‘ ; DROP TABLE t; ’"},
		{"", "SELECT $$ ; DROP TABLE t; $$"},
		{"", "SELECT 1 # comment"},
	}
	for _, c := range cases {
		if _, err := ParseScript(c.driver, c.query); err == nil {
			t.Errorf("%q: %q was read rather than refused", c.driver, c.query)
		}
		if risk := ClassifyFor(c.driver, c.query); !risk.Destructive || risk.Level != "high" {
			t.Errorf("%q: %q classified %+v, want a destructive refusal", c.driver, c.query, risk)
		}
	}
}

// PostgreSQL's dollar quoting, by its own lexer's rules.
func TestDollarQuotedBodies(t *testing.T) {
	cases := []struct {
		query string
		want  []string
	}{
		// The body holds semicolons, quotes, comment openers and other tags.
		{"DO $$ BEGIN PERFORM 1; END $$; DROP TABLE t",
			[]string{"DO $$ BEGIN PERFORM 1; END $$", "DROP TABLE t"}},
		{"SELECT $a$ ; ' \" -- /* $b$ $$ $a$; DELETE FROM t",
			[]string{"SELECT $a$ ; ' \" -- /* $b$ $$ $a$", "DELETE FROM t"}},
		// A near miss of the closing tag is body; the real one still ends it.
		{"SELECT $tag$ $ta$ $tagg$ $tag $tag$; DROP TABLE t",
			[]string{"SELECT $tag$ $ta$ $tagg$ $tag $tag$", "DROP TABLE t"}},
		{"SELECT $$a$b$$; DROP TABLE t", []string{"SELECT $$a$b$$", "DROP TABLE t"}},
		// Any byte above ASCII is a letter in a tag. Reading tags as ASCII only
		// saw no quote here and split inside the body.
		{"SELECT $tagé$ ; $tagé$; DROP TABLE t", []string{"SELECT $tagé$ ; $tagé$", "DROP TABLE t"}},
		// A $ inside an identifier is part of the identifier, so these are
		// names, not quotes, and the semicolon after them separates.
		{"SELECT a$$ FROM t; DROP TABLE x", []string{"SELECT a$$ FROM t", "DROP TABLE x"}},
		{"SELECT a$b$ FROM t; DROP TABLE x", []string{"SELECT a$b$ FROM t", "DROP TABLE x"}},
		// A positional parameter is not a tag: tags cannot start with a digit.
		{"SELECT $1 ; SELECT $2", []string{"SELECT $1", "SELECT $2"}},
		{"SELECT $1$$a;b$$; DROP TABLE t", []string{"SELECT $1$$a;b$$", "DROP TABLE t"}},
		// Directly after a number a quote can start; after a letter glued to a
		// number it cannot.
		{"SELECT 1$$a;b$$; DROP TABLE t", []string{"SELECT 1$$a;b$$", "DROP TABLE t"}},
		{"SELECT 1e$$ ; DROP TABLE t", []string{"SELECT 1e$$", "DROP TABLE t"}},
		// Two bodies back to back, and an empty one.
		{"SELECT $$a$$$$;$$; DROP TABLE t", []string{"SELECT $$a$$$$;$$", "DROP TABLE t"}},
		{"SELECT $x$$x$; DROP TABLE t", []string{"SELECT $x$$x$", "DROP TABLE t"}},
	}
	for _, c := range cases {
		got := statementTexts(t, DriverPostgres, c.query)
		if !sameStrings(got, c.want) {
			t.Errorf("%q\n split into %q\n      want %q", c.query, got, c.want)
		}
	}
	// Left open, a body would swallow everything after it; that is refused.
	for _, query := range []string{
		"SELECT $$ $$$$; drop table t; $$ $$",
		"SELECT $a$ ; DROP TABLE t; $b$",
		"SELECT a$$ FROM t; DROP TABLE x; SELECT $$",
	} {
		if _, err := ParseScript(DriverPostgres, query); err == nil {
			t.Errorf("%q: an unterminated body was accepted", query)
		}
	}
}

// A statement with a dollar-quoted body is at least high, and nothing after
// the body escapes the verdict on the script.
func TestNothingAfterADollarQuotedBodyHides(t *testing.T) {
	for _, body := range []string{
		"$$ x $$", "$f$ ; ' -- /* $f$", "$$$$", "$a$ $b$ $a$", "$é$ ; $é$",
	} {
		quoted, err := ParseScript(DriverPostgres, "SELECT "+body)
		if err != nil || len(quoted) != 1 {
			t.Fatalf("SELECT %s: %v %d", body, err, len(quoted))
		}
		if rank(quoted[0].Risk.Level) < rank("high") {
			t.Errorf("SELECT %s classified %q, want at least high", body, quoted[0].Risk.Level)
		}
		for _, tail := range []string{
			"; DROP TABLE t", ";DELETE FROM t", "\n;\nTRUNCATE t", " ; UPDATE t SET a = 1",
			"; /* x */ DROP TABLE t", "; SELECT 1; DROP TABLE t",
		} {
			query := "SELECT " + body + tail
			statements, err := ParseScript(DriverPostgres, query)
			if err != nil {
				t.Errorf("%q: %v", query, err)
				continue
			}
			last := statements[len(statements)-1]
			if len(statements) < 2 || last.Risk.Level != "critical" {
				t.Errorf("%q: the statement after the body was not seen on its own as critical: %+v", query, statements)
			}
			if risk := ClassifyFor(DriverPostgres, query); risk.Level != "critical" {
				t.Errorf("%q classified %+v, want critical", query, risk)
			}
		}
		// The same verb with no separator is inside the same statement, where
		// the verb scan reads it whatever the lexer made of the quotes.
		if risk := ClassifyFor(DriverPostgres, "SELECT "+body+" DROP"); risk.Level != "critical" {
			t.Errorf("a verb after %s without a separator classified %+v", body, risk)
		}
	}
}

// verbPattern finds a destructive verb standing as a word of its own. It is
// written independently of the classifier's word scan on purpose.
var verbPattern = regexp.MustCompile(`(?i)(^|[^A-Za-z0-9_])(drop|truncate|delete|update|alter|grant|revoke|merge)([^A-Za-z0-9_]|$)`)

// assertSentVerbsAreSeen checks the one property the capability check rests
// on: a statement that will be sent to the engine and spells a destructive
// verb anywhere in its text — code, quoted text or comment — is destructive.
// Text that is not sent (the comments around a statement) cannot run and is
// not held to it.
func assertSentVerbsAreSeen(t *testing.T, driver Driver, query string) {
	t.Helper()
	statements, err := ParseScript(driver, query)
	if err != nil {
		// A refusal runs nothing, and classifies as destructive.
		if risk := ClassifyFor(driver, query); !risk.Destructive {
			t.Fatalf("%q on %q was refused (%v) but classified %+v", query, driver, err, risk)
		}
		return
	}
	sent := 0
	for _, st := range statements {
		sent += len(st.SQL)
		if verbPattern.MatchString(st.SQL) && !st.Risk.Destructive {
			t.Fatalf("%q on %q: the statement %q would be sent as %+v — a verb was hidden",
				query, driver, st.SQL, st.Risk)
		}
		if !strings.Contains(query, st.SQL) {
			t.Fatalf("%q on %q: would send %q, which is not in what was submitted", query, driver, st.SQL)
		}
	}
	if sent > len(query) {
		t.Fatalf("%q on %q: more would be sent than was submitted", query, driver)
	}
}

// The verbs are looked for in the raw text, so no arrangement of quotes and
// comments can put one out of sight — whatever this lexer or the engine's makes
// of where they start and end. Random arrangements, every engine.
func TestNoArrangementOfQuotesHidesAVerb(t *testing.T) {
	fragments := []string{
		"'", "''", `"`, "`", "[", "]", "$$", "$a$", "$", "--", "-- ", "/*", "*/", "#", "\\", "\\'",
		" ", "\n", "\r\n", ";", "(", ")", "q'[", "]'", "E'", "x", "1", "é", "SELECT", "FROM t", "WHERE",
	}
	verbs := []string{"DROP", "drop", "Truncate", "DELETE", "update", "ALTER", "GRANT", "revoke", "MERGE"}
	drivers := append(Drivers()[:6:6], "")
	rng := rand.New(rand.NewSource(20260101))
	destructive := 0
	for i := 0; i < 20000; i++ {
		var b strings.Builder
		b.WriteString("SELECT ")
		at := rng.Intn(12)
		for j := 0; j < 12; j++ {
			if j == at {
				b.WriteString(" " + verbs[rng.Intn(len(verbs))] + " ")
			}
			b.WriteString(fragments[rng.Intn(len(fragments))])
		}
		query := b.String()
		driver := drivers[rng.Intn(len(drivers))]
		assertSentVerbsAreSeen(t, driver, query)
		if ClassifyFor(driver, query).Destructive {
			destructive++
		}
	}
	// The property above is vacuous if the generator rarely gets a verb into a
	// statement that is sent; most of these must have.
	if destructive < 15000 {
		t.Errorf("only %d of 20000 generated queries were destructive; the generator is not testing much", destructive)
	}
}

func FuzzClassifyNeverMissesAVerb(f *testing.F) {
	for _, seed := range []string{
		"SELECT 1", "SELECT '", "SELECT $$ $$", "/* */", "-- x\n", "SELECT [a] `b` \"c\"", "q'[']'",
	} {
		f.Add(seed, uint8(0))
	}
	drivers := append(Drivers()[:6:6], "")
	f.Fuzz(func(t *testing.T, text string, pick uint8) {
		driver := drivers[int(pick)%len(drivers)]
		assertSentVerbsAreSeen(t, driver, text+" DROP "+text)
		assertSentVerbsAreSeen(t, driver, "SELECT "+text+" DROP TABLE t")
	})
}

func TestStatementsCarryTheirLine(t *testing.T) {
	statements, err := ParseScript(DriverPostgres, "SELECT 1;\n\n-- note\nSELECT 2;\nSELECT\n3")
	if err != nil {
		t.Fatal(err)
	}
	var lines []int
	for _, st := range statements {
		lines = append(lines, st.Line)
	}
	if len(lines) != 3 || lines[0] != 1 || lines[1] != 4 || lines[2] != 5 {
		t.Errorf("lines = %v, want [1 4 5]", lines)
	}
}

func TestScriptsAreBounded(t *testing.T) {
	if _, err := ParseScript(DriverPostgres, strings.Repeat("SELECT 1;", maxScriptStatements)); err != nil {
		t.Fatalf("a script at the bound was refused: %v", err)
	}
	if _, err := ParseScript(DriverPostgres, strings.Repeat("SELECT 1;", maxScriptStatements+1)); err == nil {
		t.Error("a script past the bound was accepted")
	}
}
