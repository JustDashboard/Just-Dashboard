package dbx

import (
	"bytes"
	"math"
	"strings"
	"testing"
	"time"
)

// What the SQL Server and Oracle dumps write, without a server: the pieces
// that are text in, text out. That a server accepts them is what the live
// tests are for.

// --- the fence ---------------------------------------------------------------

// TestAFencedStatementCannotBeClosedByWhatItHolds is the reason a dump's
// fences carry a word of their own. What is fenced is somebody else's text —
// a view's body with its comments kept, a row's long value — and a line of it
// reading "-- jd:end" used to end the statement there, so whatever followed
// was run as statements of its own by whoever restored the dump.
func TestADumpsFencedStatementCannotBeClosedByWhatItHolds(t *testing.T) {
	hostile := "CREATE VIEW v AS SELECT 1\n" +
		rawStatementEnd + "\n" +
		"DROP TABLE everything;\n" +
		rawStatementBegin + "\n" +
		"-- and the rest of the view"
	var buf bytes.Buffer
	w := &countingWriter{w: &buf, fence: "0123456789abcdef"}
	writeStatement(w, stmt("SELECT 1"))
	writeStatement(w, rawStmt(hostile))
	writeStatement(w, stmt("SELECT 2"))

	got := splitSQLStatements(DriverMSSQL, buf.String())
	if len(got) != 3 || got[0] != "SELECT 1" || got[1] != hostile || got[2] != "SELECT 2" {
		t.Fatalf("the fenced text was cut into %d statements:\n%q", len(got), got)
	}
	for _, statement := range got {
		if strings.HasPrefix(statement, "DROP TABLE") {
			t.Errorf("a line inside a fenced statement was read as a statement: %q", statement)
		}
	}

	// Even a line that guesses at a word does not close a fence that carries
	// another.
	forged := rawStatementBegin + " aaaa\nSELECT 'kept'\n" + rawStatementEnd + " bbbb\n" + rawStatementEnd + "\nSTILL INSIDE\n" + rawStatementEnd + " aaaa\nSELECT 3;\n"
	got = splitSQLStatements(DriverOracle, forged)
	if len(got) != 2 || !strings.Contains(got[0], "STILL INSIDE") || got[1] != "SELECT 3" {
		t.Errorf("a fence was closed by another word's line: %q", got)
	}

	// Two dumps do not share a word, and a word is one token.
	a, errA := newDumpFence()
	b, errB := newDumpFence()
	if errA != nil || errB != nil || a == b || len(a) != 16 || strings.ContainsAny(a, " \t\n") {
		t.Errorf("fence words %q, %q (%v, %v)", a, b, errA, errB)
	}
	for line, want := range map[string]bool{
		rawStatementBegin:                true,
		rawStatementBegin + " abc":       true,
		"  " + rawStatementBegin + "  ":  true,
		rawStatementBegin + " two words": false,
		rawStatementBegin + "x":          false,
		"-- jd:state":                    false,
	} {
		if _, ok := dumpFenceOf(line); ok != want {
			t.Errorf("dumpFenceOf(%q) = %v, want %v", line, ok, want)
		}
	}
}

// --- SQL Server --------------------------------------------------------------

func TestMSSQLDumpTypePutsTheSizeBack(t *testing.T) {
	for _, c := range []struct {
		col  mssqlDumpColumn
		want string
	}{
		{mssqlDumpColumn{typeName: "nvarchar", maxLength: 510}, "nvarchar(255)"},
		{mssqlDumpColumn{typeName: "nvarchar", maxLength: -1}, "nvarchar(max)"},
		{mssqlDumpColumn{typeName: "varchar", maxLength: 40}, "varchar(40)"},
		{mssqlDumpColumn{typeName: "nchar", maxLength: 6}, "nchar(3)"},
		{mssqlDumpColumn{typeName: "varbinary", maxLength: -1}, "varbinary(max)"},
		{mssqlDumpColumn{typeName: "binary", maxLength: 4}, "binary(4)"},
		{mssqlDumpColumn{typeName: "decimal", precision: 12, scale: 2}, "decimal(12,2)"},
		{mssqlDumpColumn{typeName: "numeric", precision: 38}, "numeric(38,0)"},
		{mssqlDumpColumn{typeName: "datetime2", scale: 7}, "datetime2(7)"},
		{mssqlDumpColumn{typeName: "time", scale: 3}, "time(3)"},
		{mssqlDumpColumn{typeName: "datetimeoffset", scale: 0}, "datetimeoffset(0)"},
		{mssqlDumpColumn{typeName: "float", precision: 53}, "float(53)"},
		{mssqlDumpColumn{typeName: "float", precision: 24}, "float(24)"},
		// A length that means nothing for the type is not written after it.
		{mssqlDumpColumn{typeName: "int", maxLength: 4, precision: 10}, "int"},
		{mssqlDumpColumn{typeName: "datetime", maxLength: 8, precision: 23, scale: 3}, "datetime"},
		{mssqlDumpColumn{typeName: "uniqueidentifier", maxLength: 16}, "uniqueidentifier"},
		{mssqlDumpColumn{typeName: "xml", maxLength: -1}, "xml"},
	} {
		if got := mssqlDumpType(c.col); got != c.want {
			t.Errorf("mssqlDumpType(%+v) = %s, want %s", c.col, got, c.want)
		}
	}
}

func TestMSSQLDumpTableKeepsWhatTheColumnsAre(t *testing.T) {
	o := &mssqlDumpObject{schema: "dbo", name: "t]x", rel: mssqlDumpRel("dbo", "t]x")}
	columns := []mssqlDumpColumn{
		{name: "id", typeName: "int", identity: true, seed: "10", increment: "5", last: "25"},
		{name: "name", typeName: "nvarchar", maxLength: 100, nullable: true, collation: "Latin1_General_CS_AS"},
		{name: "plain", typeName: "varchar", maxLength: 10, nullable: true, collation: "SQL_Latin1_General_CP1_CI_AS"},
		{name: "flag", typeName: "bit", defaultName: "DF_flag", defaultDefinition: "((1))"},
		{name: "twice", typeName: "int", computed: true, computedDefinition: "([id]*(2))", persisted: true},
		{name: "ver", typeName: "timestamp"},
		{name: "guid", typeName: "uniqueidentifier", rowGUID: true},
	}
	indexes := []mssqlDumpIndex{
		{name: "PK_t", kind: 2, unique: true, primary: true, columns: []mssqlDumpIndexColumn{{name: "id", descending: true}}},
	}
	table, defaults := mssqlDumpTable(o, columns, indexes, "SQL_Latin1_General_CP1_CI_AS")
	want := "CREATE TABLE [dbo].[t]]x] (\n" +
		"  [id] int IDENTITY(10,5) NOT NULL,\n" +
		"  [name] nvarchar(50) COLLATE Latin1_General_CS_AS NULL,\n" +
		"  [plain] varchar(10) NULL,\n" +
		"  [flag] bit CONSTRAINT [DF_flag] DEFAULT ((1)) NOT NULL,\n" +
		"  [twice] AS ([id]*(2)) PERSISTED NOT NULL,\n" +
		"  [ver] timestamp NOT NULL,\n" +
		"  [guid] uniqueidentifier ROWGUIDCOL NOT NULL,\n" +
		"  CONSTRAINT [PK_t] PRIMARY KEY NONCLUSTERED ([id] DESC)\n)"
	if table.create.sql != want || !table.create.raw {
		t.Errorf("create =\n%s\nwant\n%s", table.create.sql, want)
	}
	// Neither the computed column nor the rowversion can be given a value.
	if table.selectSQL != "SELECT [id], [name], [plain], [flag], [guid] FROM [dbo].[t]]x]" {
		t.Errorf("select = %s", table.selectSQL)
	}
	if len(table.beforeData) != 1 || table.beforeData[0].sql != "SET IDENTITY_INSERT [dbo].[t]]x] ON" ||
		len(table.afterData) != 2 || table.afterData[0].sql != "SET IDENTITY_INSERT [dbo].[t]]x] OFF" {
		t.Errorf("around the rows: %+v / %+v", table.beforeData, table.afterData)
	}
	// The counter goes back to the last id used where the table holds rows,
	// and to the one after it where it has never held any.
	reseed := table.afterData[1].sql
	if !strings.Contains(reseed, "IF EXISTS (SELECT 1 FROM [dbo].[t]]x]) DBCC CHECKIDENT (N'[dbo].[t]]x]', RESEED, 25)") ||
		!strings.Contains(reseed, "ELSE DBCC CHECKIDENT (N'[dbo].[t]]x]', RESEED, 30)") {
		t.Errorf("reseed = %s", reseed)
	}
	if len(defaults) != 1 || defaults[0] != "((1))" {
		t.Errorf("defaults = %q", defaults)
	}

	// A table that numbers nothing has nothing switched on around its rows,
	// and one whose every column the server fills has no rows to write.
	plain, _ := mssqlDumpTable(o, []mssqlDumpColumn{{name: "a", typeName: "int", nullable: true}}, nil, "")
	if len(plain.beforeData) != 0 || len(plain.afterData) != 0 || plain.noData {
		t.Errorf("a plain table: %+v", plain)
	}
	stamped, _ := mssqlDumpTable(o, []mssqlDumpColumn{{name: "ver", typeName: "timestamp"}}, nil, "")
	if !stamped.noData {
		t.Error("a table of nothing but a rowversion is read for rows that cannot be written")
	}
	if got := mssqlDumpReseed("[t]", "", "1"); got != "" {
		t.Errorf("a counter nobody has drawn from is reseeded: %s", got)
	}
}

func TestMSSQLDumpIndexStatement(t *testing.T) {
	keys := []mssqlDumpIndexColumn{{name: "a"}, {name: "b", descending: true}, {name: "c", included: true}}
	for _, c := range []struct {
		ix      mssqlDumpIndex
		want    string
		skipped bool
	}{
		{mssqlDumpIndex{name: "ix", kind: 2, columns: keys},
			"CREATE NONCLUSTERED INDEX [ix] ON [dbo].[t] ([a], [b] DESC) INCLUDE ([c])", false},
		{mssqlDumpIndex{name: "ix", kind: 1, unique: true, columns: keys[:1], filter: "([a] IS NOT NULL)"},
			"CREATE UNIQUE CLUSTERED INDEX [ix] ON [dbo].[t] ([a]) WHERE ([a] IS NOT NULL)", false},
		{mssqlDumpIndex{name: "uq", kind: 2, unique: true, uniqueConstraint: true, columns: keys[:2]},
			"ALTER TABLE [dbo].[t] ADD CONSTRAINT [uq] UNIQUE NONCLUSTERED ([a], [b] DESC)", false},
		// A columnstore index is not a list of keys, and a disabled one may be
		// disabled because the rows break it.
		{mssqlDumpIndex{name: "cs", kind: 6, columns: keys}, "", true},
		{mssqlDumpIndex{name: "off", kind: 2, disabled: true, columns: keys}, "", true},
	} {
		got, reason := mssqlDumpIndexStatement("[dbo].[t]", c.ix)
		if got != c.want || (reason != "") != c.skipped {
			t.Errorf("%+v:\n got %q (%q)\nwant %q", c.ix, got, reason, c.want)
		}
	}
}

func TestMSSQLDumpSequenceNext(t *testing.T) {
	for _, c := range []struct {
		current, increment, low, high string
		cycling, used                 bool
		want                          string
	}{
		// Never drawn from: its first value is still its next.
		{"100", "5", "1", "1000", false, false, ""},
		{"100", "5", "1", "1000", false, true, "105"},
		{"10", "-3", "1", "1000", false, true, "7"},
		// Past the end it starts over where it cycles, and stays put where it
		// does not.
		{"1000", "5", "1", "1000", true, true, "1"},
		{"1", "-1", "1", "1000", true, true, "1000"},
		{"1000", "5", "1", "1000", false, true, "1000"},
		{"9223372036854775807", "1", "-9223372036854775808", "99999999999999999999", false, true, "9223372036854775808"},
	} {
		if got := mssqlDumpSequenceNext(c.current, c.increment, c.low, c.high, c.cycling, c.used); got != c.want {
			t.Errorf("%+v: next = %q, want %q", c, got, c.want)
		}
	}
}

func TestMSSQLDumpLiteralsAreWrittenForTheirColumn(t *testing.T) {
	at := time.Date(2026, 3, 4, 5, 6, 7, 123456700, time.UTC)
	offset := time.Date(2026, 3, 4, 5, 6, 7, 123456700, time.FixedZone("", -8*3600))
	for _, c := range []struct {
		value    any
		typeName string
		want     string
	}{
		{at, "DATE", `'2026-03-04'`},
		{time.Date(1, 1, 1, 7, 8, 9, 120000000, time.UTC), "TIME", `'07:08:09.12'`},
		{at, "SMALLDATETIME", `'2026-03-04T05:06:07'`},
		// The old type refuses more than three digits of a second.
		{at, "DATETIME", `'2026-03-04T05:06:07.123'`},
		{at, "DATETIME2", `'2026-03-04T05:06:07.1234567'`},
		// An offset is part of what the column holds.
		{offset, "DATETIMEOFFSET", `'2026-03-04T05:06:07.1234567-08:00'`},
		// A number comes back as the bytes of its digits.
		{[]byte("12.50"), "DECIMAL", `12.50`},
		{[]byte("-1234.5678"), "MONEY", `-1234.5678`},
		{[]byte("12345678901234567890123456789012345678"), "NUMERIC", `12345678901234567890123456789012345678`},
		// Sixteen bytes that are also valid text are still sixteen bytes.
		{[]byte("0123456789abcdef"), "UNIQUEIDENTIFIER", `0x30313233343536373839616263646566`},
		{[]byte{0x58}, "HIERARCHYID", `0x58`},
		{[]byte("caf\xc3\xa9"), "NVARCHAR", `N'café'`},
		{[]byte{0, 0xff}, "VARBINARY", `0x00FF`},
		{true, "BIT", `1`},
		{math.NaN(), "FLOAT", `NULL`},
	} {
		if got := dumpColumnValue(DriverMSSQL, c.value, isBinaryTypeName(c.typeName), c.typeName); got != c.want {
			t.Errorf("%T into %s:\n got %s\nwant %s", c.value, c.typeName, got, c.want)
		}
	}
}

// --- Oracle ------------------------------------------------------------------

func TestOracleDumpLiteralsAreWrittenForTheirColumn(t *testing.T) {
	at := time.Date(2026, 3, 4, 5, 6, 7, 123456789, time.UTC)
	offset := time.Date(2026, 3, 4, 5, 6, 7, 123456000, time.FixedZone("", 2*3600))
	for _, c := range []struct {
		value    any
		typeName string
		want     string
	}{
		{at, "DATE", `TO_DATE('2026-03-04 05:06:07', 'YYYY-MM-DD HH24:MI:SS')`},
		{at, "TimeStampDTY", `TO_TIMESTAMP('2026-03-04 05:06:07.123456789', 'YYYY-MM-DD HH24:MI:SS.FF9')`},
		{offset, "TimeStampTZ_DTY", `TO_TIMESTAMP_TZ('2026-03-04 05:06:07.123456000 +02:00', 'YYYY-MM-DD HH24:MI:SS.FF9 TZH:TZM')`},
		{offset, "TimeStampLTZ_DTY", `TO_TIMESTAMP_TZ('2026-03-04 05:06:07.123456000 +02:00', 'YYYY-MM-DD HH24:MI:SS.FF9 TZH:TZM')`},
		// With no column to go by, it is an instant, in UTC.
		{offset, "", `TO_TIMESTAMP('2026-03-04 03:06:07.123456000', 'YYYY-MM-DD HH24:MI:SS.FF9')`},
		// The driver's exact digits, not a string for the session to convert.
		{"12.5", "NUMBER", `12.5`},
		{"-.000015", "NUMBER", `-.000015`},
		{"12345678901234567890123456789012345678", "NUMBER", `12345678901234567890123456789012345678`},
		{"1E+40", "NUMBER", `1E+40`},
		{"12.5", "NCHAR", `'12.5'`},
		{"1 OR 1=1", "NUMBER", `'1 OR 1=1'`},
		{math.NaN(), "IBDouble", `BINARY_DOUBLE_NAN`},
		{math.Inf(-1), "IBDouble", `-BINARY_DOUBLE_INFINITY`},
		{math.Inf(1), "IBFloat", `BINARY_FLOAT_INFINITY`},
		{[]byte{0, 0xff}, "RAW", `HEXTORAW('00FF')`},
	} {
		if got := dumpColumnValue(DriverOracle, c.value, isBinaryTypeName(c.typeName), c.typeName); got != c.want {
			t.Errorf("%T into %s:\n got %s\nwant %s", c.value, c.typeName, got, c.want)
		}
	}
}

func TestOracleDumpLongRowBuildsWhatALiteralCannotHold(t *testing.T) {
	cols := []string{`"ID"`, `"BIO"`, `"AVATAR"`, `"NOTE"`}
	binary := []bool{false, false, true, false}
	types := []string{"NUMBER", "LongVarChar", "LongRaw", "NCHAR"}

	// A row whose every value fits a literal is an ordinary INSERT.
	if block, ok := oracleDumpLongRow(`"S"."T"`, cols, []any{"1", strings.Repeat("é", oracleLiteralChars), make([]byte, oracleDumpLiteralBytes), nil}, binary, types); ok {
		t.Fatalf("a row of short values was written as a block:\n%.200s", block)
	}

	text := strings.Repeat("it's é\n", 3000) // 21000 characters: three pieces
	raw := make([]byte, 40_000)              // three pieces
	for i := range raw {
		raw[i] = byte(i)
	}
	block, ok := oracleDumpLongRow(`"S"."T"`, cols, []any{"7", text, raw, "o'k"}, binary, types)
	if !ok {
		t.Fatal("a row with a long value was not written as a block")
	}
	if !oracleDumpIsBlock(block) || !strings.HasSuffix(block, "END;") {
		t.Errorf("not a block the restore will recognise:\n%.200s", block)
	}
	for _, want := range []string{
		"  v2 CLOB;\n", "  v3 BLOB;\n",
		`INSERT INTO "S"."T" ("ID", "BIO", "AVATAR", "NOTE") VALUES (7, v2, v3, 'o''k');`,
		"DBMS_LOB.FREETEMPORARY(v2);", "DBMS_LOB.FREETEMPORARY(v3);",
		"DBMS_LOB.WRITEAPPEND(v3, 16000, HEXTORAW(s));", "DBMS_LOB.WRITEAPPEND(v3, 8000, HEXTORAW(s));",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the block has no %q", want)
		}
	}
	if n := strings.Count(block, "DBMS_LOB.APPEND(v2, TO_CLOB('"); n != 3 {
		t.Errorf("the text went in as %d pieces, want 3", n)
	}
	if n := strings.Count(block, "DBMS_LOB.WRITEAPPEND(v3, "); n != 3 {
		t.Errorf("the bytes went in as %d pieces, want 3", n)
	}
	// Every quote in the text is doubled: the 3000 it holds are 6000 in the
	// file, and nothing ends a literal early.
	if n := strings.Count(block, "it''s"); n != 3000 {
		t.Errorf("%d of the text's 3000 quotes are doubled", n)
	}
	// No piece of hex is longer than a block's literal may be.
	for _, line := range strings.Split(block, "\n") {
		if len(line) > 32767+20 {
			t.Errorf("a line of the block is %d bytes, past what a literal holds", len(line))
		}
	}

	// Bytes that are not text any literal can carry go as bytes, long or not.
	block, ok = oracleDumpLongRow(`"S"."T"`, cols, []any{"8", strings.Repeat("a\x00b", 2000), nil, nil}, binary, types)
	if !ok || !strings.Contains(block, "v2 BLOB;") {
		t.Errorf("text with a NUL in it was not written as bytes:\n%.200s", block)
	}

	for statement, want := range map[string]bool{
		"DECLARE\n  s VARCHAR2(10);\nBEGIN NULL; END": true,
		"begin\n  null;\nend":                         true,
		"BEGIN":                                       true,
		"DECLARED_TABLE":                              false,
		"INSERT INTO t VALUES ('BEGIN ')":             false,
		"CREATE TABLE begin_log (id NUMBER)":          false,
	} {
		if got := oracleDumpIsBlock(statement); got != want {
			t.Errorf("oracleDumpIsBlock(%q) = %v, want %v", statement, got, want)
		}
	}
}
