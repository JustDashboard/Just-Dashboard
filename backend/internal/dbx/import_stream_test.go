package dbx

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"unicode/utf16"
)

// --- reading -----------------------------------------------------------------

func readAllRecords(t *testing.T, r recordReader) []importRecord {
	t.Helper()
	var out []importRecord
	for {
		rec, err := r.next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		out = append(out, rec)
	}
}

func texts(rec importRecord) []string {
	out := make([]string, len(rec.values))
	for i, v := range rec.values {
		out[i] = v.text
	}
	return out
}

// The delimited reader is this package's own, so it is held to the standard
// library's on everything the two have in common.
func TestDelimitedReaderAgreesWithEncodingCSV(t *testing.T) {
	for _, input := range []string{
		"a,b,c\n1,2,3\n",
		"a,b\r\n1,2\r\n",
		"a,b\n\"x,y\",\"line\nbreak\"\n",
		"a\n\"he said \"\"hi\"\"\"\n",
		"a,b\n,\n\"\",\"\"\n",
		"a,b\n\n1,2\n\n",
		"a,b\n1,2",
		"ünï,cödé\n“smart”,日本語\n",
		"a,b,c\n1,,3\n,,\n",
	} {
		want, err := func() ([][]string, error) {
			cr := csv.NewReader(strings.NewReader(input))
			cr.FieldsPerRecord = -1
			return cr.ReadAll()
		}()
		if err != nil {
			t.Fatalf("encoding/csv refused %q: %v", input, err)
		}
		got := readAllRecords(t, newDelimitedReader(strings.NewReader(input), ',', '"', true))
		if len(got) != len(want) {
			t.Errorf("%q: %d records, encoding/csv has %d", input, len(got), len(want))
			continue
		}
		for i := range want {
			if fmt.Sprint(texts(got[i])) != fmt.Sprint(want[i]) {
				t.Errorf("%q record %d: %q, encoding/csv has %q", input, i, texts(got[i]), want[i])
			}
		}
	}
}

func TestDelimitedReaderOptionsAndLines(t *testing.T) {
	// Another delimiter, another quote, and a record that spans lines: the
	// line a record is reported on is the one it starts on.
	recs := readAllRecords(t, newDelimitedReader(
		strings.NewReader("id;note\n1;'two\nlines; and a semicolon'\n2;'it''s'\n"), ';', '\'', true))
	if len(recs) != 3 {
		t.Fatalf("%d records", len(recs))
	}
	if got := texts(recs[1]); got[1] != "two\nlines; and a semicolon" {
		t.Errorf("quoted field = %q", got[1])
	}
	if recs[1].line != 2 || recs[2].line != 4 {
		t.Errorf("lines = %d, %d; want 2, 4", recs[1].line, recs[2].line)
	}
	if got := texts(recs[2]); got[1] != "it's" {
		t.Errorf("doubled quote = %q", got[1])
	}
	if !recs[1].values[1].quoted || recs[1].values[0].quoted {
		t.Error("the reader lost track of which field was quoted")
	}

	// With no quote character a quote is a character.
	recs = readAllRecords(t, newDelimitedReader(strings.NewReader("a\tb\n\"x\t5\" pipe\n"), '\t', 0, false))
	if got := texts(recs[1]); got[0] != `"x` || got[1] != `5" pipe` {
		t.Errorf("unquoted read = %q", got)
	}

	// A quote that opens and never closes is a bad last record, not a hang
	// and not a row made of the rest of the file.
	recs = readAllRecords(t, newDelimitedReader(strings.NewReader("a,b\n1,\"never closed\n2,3\n"), ',', '"', true))
	if last := recs[len(recs)-1]; last.err == nil {
		t.Errorf("an unterminated quote was read as a row: %q", texts(last))
	}
}

func TestDecodeTextEncodings(t *testing.T) {
	const want = "naïve,€5,日本\n"
	utf16le := func(s string, bom bool) []byte {
		var b bytes.Buffer
		if bom {
			b.Write([]byte{0xFF, 0xFE})
		}
		for _, u := range utf16.Encode([]rune(s)) {
			b.WriteByte(byte(u))
			b.WriteByte(byte(u >> 8))
		}
		return b.Bytes()
	}
	utf16be := func(s string) []byte {
		b := bytes.NewBuffer([]byte{0xFE, 0xFF})
		for _, u := range utf16.Encode([]rune(s)) {
			b.WriteByte(byte(u >> 8))
			b.WriteByte(byte(u))
		}
		return b.Bytes()
	}
	cases := []struct {
		name     string
		in       []byte
		encoding string
		want     string
		detected string
	}{
		{"utf-8", []byte(want), "", want, EncodingUTF8},
		{"utf-8 with a mark", append([]byte{0xEF, 0xBB, 0xBF}, want...), "utf-8", want, EncodingUTF8},
		{"utf-16 little-endian, marked, called utf-8", utf16le(want, true), "utf-8", want, EncodingUTF16},
		{"utf-16 big-endian, marked", utf16be(want), "utf-16", want, EncodingUTF16},
		{"utf-16 with no mark", utf16le(want, false), "utf-16", want, EncodingUTF16},
		{"a surrogate pair", utf16le("😀,x\n", true), "", "😀,x\n", EncodingUTF16},
		// 0x80 is the euro sign in the encoding Windows calls Latin-1.
		{"latin-1", []byte{'n', 'a', 0xEF, 'v', 'e', ',', 0x80, '5', '\n'}, "latin-1", "naïve,€5\n", EncodingLatin1},
	}
	for _, c := range cases {
		r, detected, err := decodeText(bytes.NewReader(c.in), c.encoding)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		got, err := io.ReadAll(r)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if string(got) != c.want || detected != c.detected {
			t.Errorf("%s: read %q as %s, want %q as %s", c.name, got, detected, c.want, c.detected)
		}
	}
	if _, _, err := decodeText(strings.NewReader("x"), "ebcdic"); err == nil {
		t.Error("an encoding this cannot read was accepted")
	}
}

func TestJSONReadersKeepOrderExactNumbersAndLines(t *testing.T) {
	array := "[\n  {\"b\": 1, \"a\": 9007199254740993},\n\n  {\"a\": null, \"c\": {\"k\": [1]}},\n  7\n]\n"
	recs := readAllRecords(t, newJSONArrayReader(strings.NewReader(array)))
	if len(recs) != 3 {
		t.Fatalf("%d records", len(recs))
	}
	if fmt.Sprint(recs[0].keys) != "[b a]" {
		t.Errorf("keys = %v, want the file's order", recs[0].keys)
	}
	if got := recs[0].values[1].text; got != "9007199254740993" {
		t.Errorf("a 64-bit integer was read as %s", got)
	}
	if recs[0].line != 2 || recs[1].line != 4 || recs[2].line != 5 {
		t.Errorf("lines = %d, %d, %d; want 2, 4, 5", recs[0].line, recs[1].line, recs[2].line)
	}
	if !recs[1].values[0].null || recs[1].values[1].text != `{"k":[1]}` {
		t.Errorf("null and nested values = %+v", recs[1].values)
	}
	if recs[2].err == nil {
		t.Error("an element that is not an object was read as a row")
	}

	// One object per line: a line that does not parse is one bad row.
	lines := "{\"a\": 1}\n\nnot json\n{\"a\": 3}\n"
	recs = readAllRecords(t, newNDJSONReader(strings.NewReader(lines)))
	if len(recs) != 3 || recs[1].err == nil || recs[1].line != 3 || recs[2].line != 4 || recs[2].err != nil {
		t.Fatalf("ndjson records = %+v", recs)
	}

	// Objects with no array around them are read the same way.
	recs = readAllRecords(t, newJSONArrayReader(strings.NewReader("{\"a\":1}\n{\"a\":2}\n")))
	if len(recs) != 2 || recs[1].values[0].text != "2" {
		t.Fatalf("a run of objects = %+v", recs)
	}

	// A file cut off inside a record says so, in a way a preview can tell
	// apart from a file that is wrong.
	r := newJSONArrayReader(strings.NewReader(`[{"a":1},{"a":`))
	if _, err := r.next(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.next(); !errors.Is(err, errTruncatedInput) {
		t.Errorf("err = %v, want the truncated-input error", err)
	}
}

// --- inference ---------------------------------------------------------------

func TestColumnProfilesAndCompatibility(t *testing.T) {
	profile := func(values ...string) *columnProfile {
		p := newColumnProfile()
		for i, v := range values {
			p.add(importValue{text: v}, false, i+2)
		}
		p.settle()
		return p
	}
	for want, values := range map[string][]string{
		kindInteger:  {"1", "-20", "+3"},
		kindDecimal:  {"1", "2.5", "1e6"},
		kindBoolean:  {"true", "FALSE"},
		kindDate:     {"2026-01-02"},
		kindDatetime: {"2026-01-02", "2026-01-02 03:04:05", "2026-01-02T03:04:05.5Z"},
		kindJSON:     {`{"a":1}`, `[1,2]`},
		kindText:     {"1", "n/a"},
		kindEmpty:    {"", "  "},
	} {
		if got := profile(values...).kind; got != want {
			t.Errorf("%q inferred as %s, want %s", values, got, want)
		}
	}
	// A code is not a count.
	if got := profile("00123", "04512").kind; got != kindText {
		t.Errorf("zero-padded codes inferred as %s", got)
	}

	// The warning names the value that will not fit and the line it is on.
	p := profile("1", "2", "n/a")
	warning := compatibilityWarning(p, Column{Name: "qty", Type: "integer"})
	if !strings.Contains(warning, `"n/a"`) || !strings.Contains(warning, "line 4") {
		t.Errorf("warning = %q", warning)
	}
	for _, ok := range []struct {
		values []string
		typ    string
	}{
		{[]string{"1", "2"}, "bigint"},
		{[]string{"1", "2"}, "numeric(10,2)"},
		{[]string{"1.5"}, "double precision"},
		{[]string{"x"}, "varchar(255)"},
		{[]string{"2026-01-02"}, "timestamp with time zone"},
		{[]string{"true"}, "tinyint(1)"},
		{[]string{"anything"}, "mood"},
		{[]string{"1", "0"}, "boolean"},
		{[]string{`{"a":1}`}, "jsonb"},
		{[]string{"2026-01-02 03:04:05"}, "Nullable(DateTime64(3))"},
	} {
		if w := compatibilityWarning(profile(ok.values...), Column{Name: "c", Type: ok.typ}); w != "" {
			t.Errorf("%q into %s warned: %s", ok.values, ok.typ, w)
		}
	}
	for _, bad := range []struct {
		values []string
		typ    string
	}{
		{[]string{"1.5"}, "integer"},
		{[]string{"x"}, "int unsigned"},
		{[]string{"hello"}, "date"},
		{[]string{"2026-01-02 03:04:05"}, "date"},
		{[]string{"maybe"}, "boolean"},
	} {
		if w := compatibilityWarning(profile(bad.values...), Column{Name: "c", Type: bad.typ}); w == "" {
			t.Errorf("%q into %s raised no warning", bad.values, bad.typ)
		}
	}
	// A type name that only looks numeric is not.
	if got := typeFamily("interval"); got != "other" {
		t.Errorf("interval is in family %s", got)
	}
}

func TestMatchColumnFindsTheSameNameSpelledDifferently(t *testing.T) {
	targets := []Column{{Name: "id"}, {Name: "full_name"}, {Name: "Email"}}
	for source, want := range map[string]string{
		"id": "id", "ID": "id", "Full Name": "full_name", "FULL-NAME": "full_name", "email": "Email",
	} {
		got, ok := matchColumn(source, targets)
		if !ok || got.Name != want {
			t.Errorf("matchColumn(%q) = %q, %v; want %q", source, got.Name, ok, want)
		}
	}
	if _, ok := matchColumn("phone", targets); ok {
		t.Error("a column with no counterpart was matched to one")
	}
}

// --- statements --------------------------------------------------------------

func TestUpsertStatementPerDialect(t *testing.T) {
	plan := func(driver Driver, cols ...string) *importPlan {
		d := mustDialect(t, driver)
		p := &importPlan{d: d, spec: ImportSpec{Mode: ImportModeUpsert}, key: []string{"id"}}
		p.rel, _ = qualify(d, "", "things")
		for _, c := range cols {
			q, _ := d.QuoteIdent(c)
			p.cols = append(p.cols, plannedColumn{name: c, quoted: q})
		}
		return p
	}
	for driver, want := range map[Driver]string{
		DriverPostgres: `INSERT INTO "things" ("id", "name") VALUES ($1, $2) ON CONFLICT ("id") DO UPDATE SET "name" = EXCLUDED."name" RETURNING (xmax = 0)`,
		DriverSQLite:   `INSERT INTO "things" ("id", "name") VALUES (?, ?) ON CONFLICT ("id") DO UPDATE SET "name" = EXCLUDED."name"`,
		DriverMySQL:    "INSERT INTO `things` (`id`, `name`) VALUES (?, ?) ON DUPLICATE KEY UPDATE `name` = VALUES(`name`)",
		DriverMSSQL: `MERGE INTO [things] WITH (HOLDLOCK) AS t USING (VALUES (@p1, @p2)) AS s ([id], [name]) ON t.[id] = s.[id]` +
			` WHEN MATCHED THEN UPDATE SET t.[name] = s.[name] WHEN NOT MATCHED THEN INSERT ([id], [name]) VALUES (s.[id], s.[name]) OUTPUT $action;`,
		DriverOracle: `MERGE INTO "things" t USING (SELECT :1 AS "id", :2 AS "name" FROM dual) s ON (t."id" = s."id")` +
			` WHEN MATCHED THEN UPDATE SET t."name" = s."name" WHEN NOT MATCHED THEN INSERT ("id", "name") VALUES (s."id", s."name")`,
	} {
		got, err := plan(driver, "id", "name").upsertStatement()
		if err != nil || got != want {
			t.Errorf("%s:\n got %s\nwant %s\n(%v)", driver, got, want, err)
		}
	}
	// A file that holds only the key has nothing to overwrite with.
	got, _ := plan(DriverPostgres, "id").upsertStatement()
	if !strings.Contains(got, "DO NOTHING") {
		t.Errorf("key-only upsert = %s", got)
	}
	if _, err := plan(DriverClickHouse, "id", "name").upsertStatement(); err == nil {
		t.Error("ClickHouse was given an upsert statement")
	}
}

// --- the engine, on SQLite ---------------------------------------------------

func importInto(t *testing.T, driver Driver, body string, spec ImportSpec) (*ImportReport, error) {
	t.Helper()
	db, _ := openTestDB(t)
	return Import(context.Background(), db, driver, strings.NewReader(body), spec)
}

func ptr[T any](v T) *T { return &v }

func TestImportMapsColumnsAndTellsNullFromEmpty(t *testing.T) {
	db, _ := openTestDB(t)
	ctx := context.Background()
	body := "ID;E-Mail;Full Name;ignored\n10;j@x.io;;x\n11;k@x.io;\"\";y\n12;l@x.io;\\N;z\n"
	report, err := Import(ctx, db, DriverSQLite, strings.NewReader(body), ImportSpec{
		Table: "users", Delimiter: ";",
		Mapping: map[string]string{"ID": "id", "E-Mail": "email", "Full Name": "name", "ignored": ""},
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.Inserted != 3 || report.Skipped != 0 || report.RowsRead != 3 {
		t.Fatalf("report = %+v", report)
	}
	names := map[int]*string{}
	rows, err := db.Query(`SELECT id, name FROM users WHERE id >= 10`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		var name *string
		if err := rows.Scan(&id, &name); err != nil {
			t.Fatal(err)
		}
		names[id] = name
	}
	// An empty field is NULL, a quoted empty field is the empty string, and
	// anything else is itself — \N included, since nobody said it meant NULL.
	if names[10] != nil {
		t.Errorf("an empty field was stored as %q, want NULL", *names[10])
	}
	if names[11] == nil || *names[11] != "" {
		t.Errorf("a quoted empty field was not stored as the empty string")
	}
	if names[12] == nil || *names[12] != `\N` {
		t.Errorf(`\N was not kept as text`)
	}
	if report.Columns[3].Target != "" || report.Columns[2].Target != "name" {
		t.Errorf("columns = %+v", report.Columns)
	}

	// Told that \N means NULL, it does.
	if _, err := Import(ctx, db, DriverSQLite, strings.NewReader("id,email,name\n13,m@x.io,\\N\n14,n@x.io,\n"), ImportSpec{
		Table: "users", NullToken: ptr(`\N`),
	}); err != nil {
		t.Fatal(err)
	}
	var name13, name14 *string
	db.QueryRow(`SELECT name FROM users WHERE id = 13`).Scan(&name13)
	db.QueryRow(`SELECT name FROM users WHERE id = 14`).Scan(&name14)
	if name13 != nil || name14 == nil || *name14 != "" {
		t.Errorf("with a null token of \\N: 13=%v 14=%v", name13, name14)
	}

	// A mapping to a column the table does not have is refused outright.
	if _, err := Import(ctx, db, DriverSQLite, strings.NewReader("a\n1\n"), ImportSpec{
		Table: "users", Mapping: map[string]string{"a": "nope"},
	}); err == nil {
		t.Error("a mapping to a column that does not exist was accepted")
	}
}

func TestImportMatchesColumnsByNameWhenNotTold(t *testing.T) {
	report, err := importInto(t, DriverSQLite, "Email,ID,nickname\nz@x.io,60,zed\n", ImportSpec{Table: "users"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.Inserted != 1 {
		t.Fatalf("report = %+v", report)
	}
	got := map[string]string{}
	for _, c := range report.Columns {
		got[c.Source] = c.Target
	}
	if got["Email"] != "email" || got["ID"] != "id" || got["nickname"] != "" {
		t.Errorf("mapping = %v", got)
	}
}

func TestImportWithoutAHeaderGoesInTheTablesOrder(t *testing.T) {
	db, _ := openTestDB(t)
	report, err := Import(context.Background(), db, DriverSQLite,
		strings.NewReader("70,h@x.io,Hal\n71,i@x.io,Ida\n"), ImportSpec{Table: "users", Header: ptr(false)})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.Inserted != 2 || report.Columns[1].Target != "email" {
		t.Fatalf("report = %+v", report)
	}
}

func TestImportUpsertCountsInsertedAndUpdated(t *testing.T) {
	db, _ := openTestDB(t)
	ctx := context.Background()
	report, err := Import(ctx, db, DriverSQLite,
		strings.NewReader("id,email,name\n1,a@x.io,Ann Renamed\n90,new@x.io,New\n"),
		ImportSpec{Table: "users", Mode: ImportModeUpsert})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.Inserted != 1 || report.Updated != 1 || fmt.Sprint(report.Key) != "[id]" {
		t.Fatalf("report = %+v", report)
	}
	var name string
	db.QueryRow(`SELECT name FROM users WHERE id = 1`).Scan(&name)
	if name != "Ann Renamed" {
		t.Errorf("the existing row holds %q", name)
	}

	// On a named unique index rather than the primary key.
	indexes, _ := sqliteDialect{}.Indexes(ctx, db, "", "users")
	var unique string
	for _, ix := range indexes {
		if ix.Unique && !ix.Primary {
			unique = ix.Name
		}
	}
	report, err = Import(ctx, db, DriverSQLite,
		strings.NewReader("email,name\nb@x.io,Bea\nc@x.io,Cy\n"),
		ImportSpec{Table: "users", Mode: ImportModeUpsert, ConflictConstraint: unique})
	if err != nil {
		t.Fatalf("Import on %s: %v", unique, err)
	}
	if report.Inserted != 1 || report.Updated != 1 || fmt.Sprint(report.Key) != "[email]" {
		t.Fatalf("report = %+v", report)
	}

	// A key the table does not enforce cannot say which row a new one replaces.
	if _, err := Import(ctx, db, DriverSQLite, strings.NewReader("id,name\n1,x\n"),
		ImportSpec{Table: "users", Mode: ImportModeUpsert, ConflictColumns: []string{"name"}}); err == nil {
		t.Error("an upsert on a column with no unique index was accepted")
	}
	// And the file has to carry the key.
	if _, err := Import(ctx, db, DriverSQLite, strings.NewReader("email,name\nq@x.io,x\n"),
		ImportSpec{Table: "users", Mode: ImportModeUpsert}); err == nil {
		t.Error("an upsert with no key column in the file was accepted")
	}
}

func TestImportSkipsBadRowsAndNamesTheirLines(t *testing.T) {
	db, _ := openTestDB(t)
	// Line 3 duplicates a key, line 4 is short, line 6 lacks a required value.
	body := "id,email,name\n100,ok1@x.io,One\n1,dupe@x.io,Dupe\n101,short\n102,ok2@x.io,Two\n103,,Three\n"
	report, err := Import(context.Background(), db, DriverSQLite, strings.NewReader(body),
		ImportSpec{Table: "users", SkipBadRows: true, BatchSize: 2})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.Inserted != 2 || report.Skipped != 3 || report.RowsRead != 5 {
		t.Fatalf("report = %+v", report)
	}
	var lines []int
	for _, e := range report.Errors {
		lines = append(lines, e.Line)
	}
	if fmt.Sprint(lines) != "[4 3 6]" && fmt.Sprint(lines) != "[3 4 6]" {
		t.Errorf("error lines = %v (%+v)", lines, report.Errors)
	}

	// Without the option the first bad row ends it, and nothing is kept.
	db2, _ := openTestDB(t)
	_, err = Import(context.Background(), db2, DriverSQLite, strings.NewReader(body), ImportSpec{Table: "users", BatchSize: 2})
	if err == nil || !strings.Contains(err.Error(), "line 3") {
		t.Fatalf("err = %v, want the import stopped at line 3", err)
	}
	var n int
	db2.QueryRow(`SELECT COUNT(*) FROM users WHERE id >= 100`).Scan(&n)
	if n != 0 {
		t.Errorf("%d rows of a failed import were kept", n)
	}
}

func TestImportReplaceIsUndoneWhenTheImportFails(t *testing.T) {
	db, _ := openTestDB(t)
	// The second row cannot go in: author 99 does not exist.
	body := "id,title,author_id\n50,Fine,1\n51,Orphan,99\n"
	if _, err := Import(context.Background(), db, DriverSQLite, strings.NewReader(body),
		ImportSpec{Table: "posts", Mode: ImportModeReplace}); err == nil {
		t.Fatal("an import with a refused row succeeded")
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM posts`).Scan(&n)
	if n != 2 {
		t.Errorf("the table holds %d rows after a failed replace, want the 2 it had", n)
	}
}

func TestImportDryRunWritesNothingAndShowsWhatItWouldWrite(t *testing.T) {
	db, _ := openTestDB(t)
	body := "id,email,name,extra\n200,d@x.io,Dee,1\nabc,e@x.io,,2\n202,f@x.io,Fay,3\n"
	report, err := Import(context.Background(), db, DriverSQLite, strings.NewReader(body),
		ImportSpec{Table: "users", DryRun: true})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	if n != 2 {
		t.Fatalf("a dry run changed the table: %d rows", n)
	}
	if !report.DryRun || report.Inserted != 0 || report.RowsRead != 3 || report.Preview == nil {
		t.Fatalf("report = %+v", report)
	}
	if fmt.Sprint(report.Preview.Columns) != "[id email name]" || len(report.Preview.Rows) != 3 {
		t.Fatalf("preview = %+v", report.Preview)
	}
	if report.Preview.Rows[1][2] != nil || *report.Preview.Rows[0][2] != "Dee" {
		t.Errorf("preview rows = %v", report.Preview.Rows)
	}
	byName := map[string]ImportColumn{}
	for _, c := range report.Columns {
		byName[c.Source] = c
	}
	if byName["id"].Warning == "" || !strings.Contains(byName["id"].Warning, "abc") {
		t.Errorf("no warning for text headed into an integer column: %+v", byName["id"])
	}
	if byName["extra"].Target != "" || byName["email"].Inferred != kindText || byName["extra"].Inferred != kindInteger {
		t.Errorf("columns = %+v", report.Columns)
	}
	if report.Statement == "" {
		t.Error("a dry run does not say what statement it would run")
	}

	// The first part of a file is enough for a dry run: a last row cut in
	// half is where the part ended, not an error.
	report, err = Import(context.Background(), db, DriverSQLite, strings.NewReader("id,email,name\n300,g@x.io,Gil\n301,h@x"),
		ImportSpec{Table: "users", DryRun: true})
	if err != nil || report.RowsRead != 1 {
		t.Fatalf("a cut-off sample: %+v, %v", report, err)
	}
	report, err = Import(context.Background(), db, DriverSQLite, strings.NewReader(`[{"id":300,"email":"g@x.io"},{"id":30`),
		ImportSpec{Table: "users", Format: ImportFormatJSON, DryRun: true})
	if err != nil || report.RowsRead != 1 {
		t.Fatalf("a cut-off JSON sample: %+v, %v", report, err)
	}
}

func TestImportCreatesATableFromTheFile(t *testing.T) {
	db, _ := openTestDB(t)
	ctx := context.Background()
	body := "sku,qty,price,active,added,note\nA-1,3,9.50,true,2026-01-02,first\nA-2,0,12,false,2026-02-03,\n"
	spec := ImportSpec{Table: "stock", Create: &ImportCreate{Columns: []NewColumn{{Name: "sku", PrimaryKey: true, NotNull: true}}}}

	dry := spec
	dry.DryRun = true
	report, err := Import(ctx, db, DriverSQLite, strings.NewReader(body), dry)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if report.Create == nil || report.Create.Created || !strings.Contains(report.Create.Statement, "CREATE TABLE") {
		t.Fatalf("create = %+v", report.Create)
	}
	types := map[string]string{}
	for _, c := range report.Create.Columns {
		types[c.Name] = c.Type
	}
	if types["qty"] != "INTEGER" || types["price"] != "NUMERIC" || types["active"] != "BOOLEAN" ||
		types["added"] != "DATE" || types["note"] != "TEXT" || types["sku"] != "TEXT" {
		t.Errorf("inferred types = %v", types)
	}
	if cols, _ := (sqliteDialect{}).Columns(ctx, db, "", "stock"); len(cols) != 0 {
		t.Fatal("a dry run created the table")
	}

	report, err = Import(ctx, db, DriverSQLite, strings.NewReader(body), spec)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if !report.Create.Created || report.Inserted != 2 {
		t.Fatalf("report = %+v", report)
	}
	var qty, active int
	if err := db.QueryRow(`SELECT qty, active FROM stock WHERE sku = 'A-1'`).Scan(&qty, &active); err != nil || qty != 3 || active != 1 {
		t.Errorf("row = qty %d active %d (%v)", qty, active, err)
	}

	// The table is there now, so creating it again is refused.
	if _, err := Import(ctx, db, DriverSQLite, strings.NewReader(body), spec); err == nil {
		t.Error("a table that exists was created again")
	}

	// A table made for an import that then fails is not left behind.
	_, err = Import(ctx, db, DriverSQLite, strings.NewReader("k,v\n1,a\n1,b\n"),
		ImportSpec{Table: "pairs", Create: &ImportCreate{Columns: []NewColumn{{Name: "k", PrimaryKey: true}}}})
	if err == nil {
		t.Fatal("a duplicate key in a new table was accepted")
	}
	if cols, _ := (sqliteDialect{}).Columns(ctx, db, "", "pairs"); len(cols) != 0 {
		t.Error("the table of a failed import was left behind")
	}
}

func TestImportReadsJSONAndNDJSON(t *testing.T) {
	db, _ := openTestDB(t)
	ctx := context.Background()
	report, err := Import(ctx, db, DriverSQLite,
		strings.NewReader(`[{"id":400,"email":"j1@x.io","name":"J","unknown":true},{"email":"j2@x.io","id":401}]`),
		ImportSpec{Table: "users", Format: ImportFormatJSON})
	if err != nil || report.Inserted != 2 {
		t.Fatalf("json: %+v, %v", report, err)
	}
	// One object per line, with this dashboard's own "this export was cut
	// short" line at the end: it is a remark, not a row.
	lines := `{"id":410,"email":"n1@x.io","name":"N"}` + "\n" + `{"id":411,"email":"n2@x.io","name":null}` + "\n" +
		`{"__export":{"status":"truncated","rows":2}}` + "\n"
	report, err = Import(ctx, db, DriverSQLite, strings.NewReader(lines), ImportSpec{Table: "users", Format: ImportFormatNDJSON})
	if err != nil || report.Inserted != 2 || report.RowsRead != 2 {
		t.Fatalf("ndjson: %+v, %v", report, err)
	}
	if len(report.Warnings) == 0 || !strings.Contains(report.Warnings[0], "truncated") {
		t.Errorf("warnings = %v, want one saying the file is a truncated export", report.Warnings)
	}
}

// Rows are written in batches whose size the engine's parameter limit bounds,
// and a file larger than several of them arrives whole.
func TestImportWritesInBatches(t *testing.T) {
	db, _ := openTestDB(t)
	var body strings.Builder
	body.WriteString("id,email,name\n")
	const rows = 2500
	for i := 0; i < rows; i++ {
		fmt.Fprintf(&body, "%d,u%d@x.io,User %d\n", 1000+i, i, i)
	}
	report, err := Import(context.Background(), db, DriverSQLite, strings.NewReader(body.String()),
		ImportSpec{Table: "users", BatchSize: 700})
	if err != nil || report.Inserted != rows || report.RowsRead != rows {
		t.Fatalf("report = %+v, %v", report, err)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	if n != rows+2 {
		t.Errorf("the table holds %d rows, want %d", n, rows+2)
	}
}

func TestImportRefusesWhatItCannotRead(t *testing.T) {
	for name, spec := range map[string]ImportSpec{
		"no table":                      {},
		"an unknown format":             {Table: "users", Format: "xlsx"},
		"an unknown mode":               {Table: "users", Mode: "merge"},
		"a two-character delimiter":     {Table: "users", Delimiter: "||"},
		"a quote that is the delimiter": {Table: "users", Delimiter: ";", Quote: ptr(";")},
		"an unknown encoding":           {Table: "users", Encoding: "ebcdic"},
		"a table that is not there":     {Table: "nowhere"},
		"upsert into a new table":       {Table: "fresh", Mode: ImportModeUpsert, Create: &ImportCreate{}},
		"a conflict key on an insert":   {Table: "users", ConflictColumns: []string{"id"}},
	} {
		if _, err := importInto(t, DriverSQLite, "id,email\n1,x@x.io\n", spec); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := importInto(t, DriverSQLite, "", ImportSpec{Table: "users"}); err == nil {
		t.Error("an empty file was imported")
	}
}

// The inline route's JSON import read numbers through a float, so an id past
// 2^53 arrived as a different id.
func TestLegacyJSONImportKeepsLargeIntegersExact(t *testing.T) {
	db, _ := openTestDB(t)
	if _, err := db.Exec(`CREATE TABLE big (id INTEGER PRIMARY KEY, n TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportJSON(context.Background(), db, DriverSQLite,
		strings.NewReader(`[{"id":9007199254740993,"n":"x"}]`), ImportOptions{Table: "big"}); err != nil {
		t.Fatal(err)
	}
	var id string
	db.QueryRow(`SELECT CAST(id AS TEXT) FROM big`).Scan(&id)
	if id != "9007199254740993" {
		t.Errorf("id stored as %s", id)
	}
}

// The inline route's JSON import takes a list of keys, each into the column
// of its own name — not a list of targets by position.
func TestLegacyJSONImportTakesTheKeysItIsGiven(t *testing.T) {
	db, _ := openTestDB(t)
	res, err := ImportJSON(context.Background(), db, DriverSQLite,
		strings.NewReader(`[{"name":"Zed","id":70,"email":"z@x.io","extra":"ignored"}]`),
		ImportOptions{Table: "users", Columns: []string{"id", "email"}})
	if err != nil || res.Inserted != 1 {
		t.Fatalf("import: %+v, %v", res, err)
	}
	var email string
	var name *string
	if err := db.QueryRow(`SELECT email, name FROM users WHERE id = 70`).Scan(&email, &name); err != nil || email != "z@x.io" || name != nil {
		t.Errorf("row = %q, %v (%v); want the two keys asked for and nothing else", email, name, err)
	}
	// And a file with no header needs to be told its columns.
	if _, err := ImportCSV(context.Background(), db, DriverSQLite, strings.NewReader("1,a@x.io\n"), ImportOptions{Table: "users"}); err == nil {
		t.Error("a CSV with no header and no columns was imported")
	}
	res, err = ImportCSV(context.Background(), db, DriverSQLite, strings.NewReader("71,p@x.io\n72\n"),
		ImportOptions{Table: "users", Columns: []string{"id", "email"}})
	if err != nil || res.Inserted != 1 || res.Failed != 1 || !strings.Contains(res.Errors[0], "row 2: row has 1 fields, expected 2") {
		t.Errorf("positional import: %+v, %v", res, err)
	}
}
