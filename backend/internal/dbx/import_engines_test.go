package dbx

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// What an import sends SQL Server and Oracle, without a server: the statement
// text and the values bound. That the servers take them is what the live
// tests are for.

func TestMSSQLImportValueConvertsToTheColumnsType(t *testing.T) {
	for _, c := range []struct {
		typeName      string
		value, failed string
	}{
		// Text goes in as it is: any text is a value of the column's type.
		{"nvarchar(100)", "@p1", ""},
		{"nvarchar(MAX)", "@p1", ""},
		{"int", "TRY_CONVERT(int, @p1)", "@p1 IS NOT NULL AND TRY_CONVERT(int, @p1) IS NULL"},
		{"decimal(10,2)", "TRY_CONVERT(decimal(10,2), @p1)", "@p1 IS NOT NULL AND TRY_CONVERT(decimal(10,2), @p1) IS NULL"},
		{"MONEY", "TRY_CONVERT(money, @p1)", "@p1 IS NOT NULL AND TRY_CONVERT(money, @p1) IS NULL"},
		{"uniqueidentifier", "TRY_CONVERT(uniqueidentifier, @p1)", "@p1 IS NOT NULL AND TRY_CONVERT(uniqueidentifier, @p1) IS NULL"},
		{"datetime2", "TRY_CONVERT(datetime2, @p1)", "@p1 IS NOT NULL AND TRY_CONVERT(datetime2, @p1) IS NULL"},
		// The two old types read a date by the login's language unless it is
		// read as the newer one first.
		{"datetime", "COALESCE(TRY_CONVERT(datetime, TRY_CONVERT(datetime2, @p1)), TRY_CONVERT(datetime, @p1))",
			"@p1 IS NOT NULL AND COALESCE(TRY_CONVERT(datetime, TRY_CONVERT(datetime2, @p1)), TRY_CONVERT(datetime, @p1)) IS NULL"},
		{"varbinary(MAX)", "CONVERT(varbinary(max), @p1)", ""},
		{"image", "CONVERT(varbinary(max), @p1)", ""},
		// A type this does not know, and anything that is not a type's shape,
		// is left alone rather than written into the statement.
		{"geography", "@p1", ""},
		{"", "@p1", ""},
		{"int); DROP TABLE t; --", "@p1", ""},
		{"decimal(10,2) NOT NULL", "@p1", ""},
	} {
		value, failed := mssqlImportValue(c.typeName, "@p1", true)
		if value != c.value || failed != c.failed {
			t.Errorf("%q:\n got %s | %s\nwant %s | %s", c.typeName, value, failed, c.value, c.failed)
		}
	}
	// The inline route binds whatever text the file held, so a binary column
	// is left to refuse it.
	if value, _ := mssqlImportValue("varbinary(MAX)", "@p1", false); value != "@p1" {
		t.Errorf("a binary column on the inline route = %s", value)
	}
}

func mssqlImportPlan(t *testing.T, trusted bool, mode ImportMode, types ...string) *importPlan {
	t.Helper()
	d := mustDialect(t, DriverMSSQL)
	p := &importPlan{d: d, spec: ImportSpec{Mode: mode, trusted: trusted}, key: []string{"id"}, rel: "[dbo].[t]"}
	names := []string{"id", "name", "total", "raw", "at"}
	for i, typ := range types {
		q, _ := d.QuoteIdent(names[i])
		p.cols = append(p.cols, plannedColumn{name: names[i], quoted: q, typeName: typ})
	}
	return p
}

func TestMSSQLImportStatementsCheckBeforeTheyWrite(t *testing.T) {
	p := mssqlImportPlan(t, false, ImportModeInsert, "int", "nvarchar(100)", "decimal(10,2)", "varbinary(MAX)")
	const core = "INSERT INTO [dbo].[t] ([id], [name], [total], [raw]) VALUES " +
		"(TRY_CONVERT(int, @p1), @p2, TRY_CONVERT(decimal(10,2), @p3), CONVERT(varbinary(max), @p4))"
	const guard = "IF @p1 IS NOT NULL AND TRY_CONVERT(int, @p1) IS NULL RAISERROR(N'jd-import-convert:1', 16, 1) ELSE " +
		"IF @p3 IS NOT NULL AND TRY_CONVERT(decimal(10,2), @p3) IS NULL RAISERROR(N'jd-import-convert:3', 16, 1) ELSE "
	if got := p.insertStatement(1); got != guard+core {
		t.Errorf("one row:\n got %s\nwant %s", got, guard+core)
	}
	// What a report shows is the statement that writes the row.
	if got, _ := p.shownStatement(); got != core {
		t.Errorf("shown:\n got %s\nwant %s", got, core)
	}
	// Several rows are checked together, and written together or not at all.
	want := "IF EXISTS (SELECT 1 FROM (VALUES (@p1, @p3), (@p5, @p7)) AS r (g1, g2) WHERE " +
		"(g1 IS NOT NULL AND TRY_CONVERT(int, g1) IS NULL) OR (g2 IS NOT NULL AND TRY_CONVERT(decimal(10,2), g2) IS NULL)) " +
		"RAISERROR(N'jd-import-convert', 16, 1) ELSE " +
		"INSERT INTO [dbo].[t] ([id], [name], [total], [raw]) VALUES " +
		"(TRY_CONVERT(int, @p1), @p2, TRY_CONVERT(decimal(10,2), @p3), CONVERT(varbinary(max), @p4)), " +
		"(TRY_CONVERT(int, @p5), @p6, TRY_CONVERT(decimal(10,2), @p7), CONVERT(varbinary(max), @p8))"
	if got := p.insertStatement(2); got != want {
		t.Errorf("two rows:\n got %s\nwant %s", got, want)
	}

	// An upsert is checked the same way before its MERGE.
	up := mssqlImportPlan(t, false, ImportModeUpsert, "int", "nvarchar(100)")
	got, err := up.upsertStatement()
	if err != nil || !strings.HasPrefix(got, "IF @p1 IS NOT NULL AND TRY_CONVERT(int, @p1) IS NULL RAISERROR(N'jd-import-convert:1', 16, 1) ELSE MERGE INTO [dbo].[t] WITH (HOLDLOCK) AS t USING (VALUES (TRY_CONVERT(int, @p1), @p2)) AS s ([id], [name]) ON t.[id] = s.[id]") ||
		!strings.HasSuffix(got, "OUTPUT $action;") {
		t.Errorf("upsert = %s (%v)", got, err)
	}

	// A table of nothing but text has nothing to check.
	if got := mssqlImportPlan(t, false, ImportModeInsert, "nvarchar(10)", "varchar(20)").insertStatement(1); got != "INSERT INTO [dbo].[t] ([id], [name]) VALUES (@p1, @p2)" {
		t.Errorf("all text = %s", got)
	}
	// The inline route checks what converts and leaves a binary column be.
	legacy := mssqlImportPlan(t, true, ImportModeInsert, "int", "varbinary(MAX)").insertStatement(1)
	if !strings.HasSuffix(legacy, "VALUES (TRY_CONVERT(int, @p1), @p2)") || !strings.HasPrefix(legacy, "IF @p1 IS NOT NULL") {
		t.Errorf("inline route = %s", legacy)
	}
	// And no other engine's statement changes.
	other := &importPlan{d: mustDialect(t, DriverPostgres), rel: `"t"`, cols: []plannedColumn{{name: "id", quoted: `"id"`, typeName: "integer"}}}
	if got := other.insertStatement(2); got != `INSERT INTO "t" ("id") VALUES ($1), ($2)` {
		t.Errorf("postgres = %s", got)
	}
}

func TestMSSQLImportRowErrorNamesTheColumnAndTheValue(t *testing.T) {
	p := mssqlImportPlan(t, false, ImportModeInsert, "int", "nvarchar(100)", "decimal(10,2)")
	args := []any{"4", "di", "oops"}
	got := p.rowError(errors.New("mssql: jd-import-convert:3"), args)
	if got == nil || got.Error() != `total: "oops" is not a value a decimal(10,2) column takes` {
		t.Errorf("rowError = %v", got)
	}
	// Anything else the server says is passed on as it said it.
	plain := errors.New("mssql: Violation of PRIMARY KEY constraint")
	if p.rowError(plain, args) != plain || p.rowError(nil, args) != nil {
		t.Error("an error that is not about conversion was rewritten")
	}
	for _, odd := range []string{"jd-import-convert:99", "jd-import-convert:x", "jd-import-convert"} {
		if err := errors.New(odd); p.rowError(err, args) != err {
			t.Errorf("%q was read as naming a column", odd)
		}
	}
}

func TestImportISOTimeReadsTheFormsAFileCarries(t *testing.T) {
	utc := func(y int, m time.Month, d, h, min, s, ns int) time.Time {
		return time.Date(y, m, d, h, min, s, ns, time.UTC)
	}
	for text, want := range map[string]time.Time{
		"2026-03-04":                     utc(2026, 3, 4, 0, 0, 0, 0),
		"2026-03-04 05:06":               utc(2026, 3, 4, 5, 6, 0, 0),
		"2026-03-04T05:06:07":            utc(2026, 3, 4, 5, 6, 7, 0),
		" 2026-03-04 05:06:07.123456789": utc(2026, 3, 4, 5, 6, 7, 123456789),
		"2026-03-04T05:06:07.123Z":       utc(2026, 3, 4, 5, 6, 7, 123000000),
		"2026-03-04T05:06:07+02:00":      utc(2026, 3, 4, 3, 6, 7, 0),
		"2026-03-04 05:06:07 +0200":      utc(2026, 3, 4, 3, 6, 7, 0),
		"2026-03-04 05:06-07":            utc(2026, 3, 4, 12, 6, 0, 0),
	} {
		got, ok := importISOTime(text)
		if !ok || !got.Equal(want) {
			t.Errorf("importISOTime(%q) = %v, %v; want %v", text, got, ok, want)
		}
	}
	// A zone a value names is kept: it is part of what a zoned column holds.
	if got, _ := importISOTime("2026-03-04T05:06:07+02:00"); got.Format("-07:00") != "+02:00" || got.Hour() != 5 {
		t.Errorf("the zone was not kept: %v", got)
	}
	for _, text := range []string{"", "04-MAR-26", "2026-13-40", "2026-03-04T25:00:00", "03/04/2026", "yesterday", "2026-03-04T05"} {
		if got, ok := importISOTime(text); ok {
			t.Errorf("importISOTime(%q) = %v, want it left to the engine", text, got)
		}
	}
}

func TestImportBindValueForTheEnginesThatParseDifferently(t *testing.T) {
	text := func(s string) importValue { return importValue{text: s} }
	// Oracle is given an instant for a date in an ISO form, and the text for
	// one in a form only its session knows.
	if got, err := bindValue(DriverOracle, text("2026-03-04 05:06:07"), kindDatetime, "TIMESTAMP(6)"); err != nil {
		t.Fatal(err)
	} else if at, ok := got.(time.Time); !ok || !at.Equal(time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)) {
		t.Errorf("an ISO instant for Oracle = %#v", got)
	}
	if got, _ := bindValue(DriverOracle, text("2026-03-04"), kindDate, "DATE"); got == any("2026-03-04") {
		t.Error("an ISO date for Oracle was bound as text")
	}
	if got, _ := bindValue(DriverOracle, text("04-MAR-26"), kindDate, "DATE"); got != any("04-MAR-26") {
		t.Errorf("a date in the session's own form = %#v", got)
	}
	// The engines that read ISO themselves are sent the text.
	if got, _ := bindValue(DriverPostgres, text("2026-03-04 05:06:07"), kindDatetime, "timestamp"); got != any("2026-03-04 05:06:07") {
		t.Errorf("an ISO instant for Postgres = %#v", got)
	}
	// true and false into a column of numbers, which is what Oracle's
	// booleans are.
	for value, want := range map[string]any{"true": int64(1), " FALSE ": int64(0), "1": "1", "yes": "yes"} {
		if got, _ := bindValue(DriverOracle, text(value), kindDecimal, "NUMBER(1)"); got != want {
			t.Errorf("%q into NUMBER(1) = %#v, want %#v", value, got, want)
		}
	}
	// SQL Server takes bytes into a binary column, and nothing else.
	if got, err := bindValue(DriverMSSQL, text(`\x00ff41`), "binary", "varbinary(MAX)"); err != nil || string(got.([]byte)) != "\x00\xffA" {
		t.Errorf("hex into varbinary = %#v, %v", got, err)
	}
	if _, err := bindValue(DriverMSSQL, text("plain text"), "binary", "varbinary(MAX)"); err == nil || !strings.Contains(err.Error(), `\x`) {
		t.Errorf("text into varbinary on SQL Server: %v", err)
	}
	if got, err := bindValue(DriverMySQL, text("plain text"), "binary", "blob"); err != nil || got != any("plain text") {
		t.Errorf("text into a blob on MySQL = %#v, %v", got, err)
	}
}

func TestImportKeyColumnTypeIsOneTheEngineWillIndex(t *testing.T) {
	for _, c := range []struct {
		driver         Driver
		inferred, want string
	}{
		{DriverMSSQL, "nvarchar(max)", "nvarchar(450)"},
		{DriverMSSQL, "bigint", "bigint"},
		{DriverMySQL, "text", "varchar(255)"},
		{DriverMySQL, "bigint", "bigint"},
		{DriverPostgres, "text", "text"},
		{DriverOracle, "VARCHAR2(4000)", "VARCHAR2(4000)"},
	} {
		if got := importKeyColumnType(c.driver, c.inferred); got != c.want {
			t.Errorf("%s key of %s = %s, want %s", c.driver, c.inferred, got, c.want)
		}
	}
}

// An export carries a table's computed column like any other. Matched to it
// by name, every row of the file would be refused, so it is left out. (On
// SQLite the table's own column list already leaves a generated column out;
// the engines that list one are told apart in the live tests, where the
// report says so as well.) Told outright to write it, the import does as it
// is told and the engine answers.
func TestImportLeavesOutWhatTheServerComputes(t *testing.T) {
	db, _ := openTestDB(t)
	ctx := context.Background()
	if _, err := db.Exec(`CREATE TABLE prices (id INTEGER PRIMARY KEY, net REAL NOT NULL, gross REAL GENERATED ALWAYS AS (net * 1.2) STORED)`); err != nil {
		t.Fatal(err)
	}
	if got := importSQLiteComputedColumns(ctx, db, "prices"); len(got) != 1 || !got["gross"] {
		t.Errorf("computed columns = %v", got)
	}
	const file = "id,net,gross\n1,10,12\n2,20,999\n"
	report, err := Import(ctx, db, DriverSQLite, strings.NewReader(file), ImportSpec{Table: "prices"})
	if err != nil || report.Inserted != 2 {
		t.Fatalf("Import: %+v, %v", report, err)
	}
	if report.Columns[2].Target != "" || strings.Contains(report.Statement, "gross") {
		t.Errorf("the computed column is still written: %+v\n%s", report.Columns[2], report.Statement)
	}
	var gross float64
	if err := db.QueryRow(`SELECT gross FROM prices WHERE id = 2`).Scan(&gross); err != nil || gross != 24 {
		t.Errorf("gross = %v (%v), want what the table computes", gross, err)
	}
	// Mapped to it on purpose, it is not quietly dropped.
	_, err = Import(ctx, db, DriverSQLite, strings.NewReader("id,net,gross\n3,30,36\n"),
		ImportSpec{Table: "prices", Mapping: map[string]string{"id": "id", "net": "net", "gross": "gross"}})
	if err == nil {
		t.Error("a value mapped onto a generated column was accepted")
	}
}

func TestExportWritesSQLServersOwnTypesAsWhatTheyAre(t *testing.T) {
	stored := []byte{0xFF, 0x19, 0x96, 0x6F, 0x86, 0x8B, 0x11, 0xD0, 0xB4, 0x2D, 0x00, 0xC0, 0x4F, 0xC9, 0x64, 0xFF}
	if got := exportValueOf(stored, "UNIQUEIDENTIFIER"); got != "6F9619FF-8B86-D011-B42D-00C04FC964FF" {
		t.Errorf("a uniqueidentifier = %v", got)
	}
	// The same bytes in a column that is bytes stay bytes.
	if got := exportValueOf(stored, "VARBINARY"); got != `\xff19966f868b11d0b42d00c04fc964ff` {
		t.Errorf("sixteen bytes of a binary column = %v", got)
	}
	at := time.Date(1, 1, 1, 7, 8, 9, 123000000, time.UTC)
	if got := exportValueOf(at, "TIME"); got != "07:08:09.123" {
		t.Errorf("a time of day = %v", got)
	}
	if got := exportValueOf(time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC), "DATETIME2"); got != exportValue(time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)) {
		t.Errorf("an instant = %v", got)
	}
}
