package dbx

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- export ------------------------------------------------------------------

func exportOf(t *testing.T, db *sql.DB, query string, opts ExportOptions) (string, int, bool) {
	t.Helper()
	var out bytes.Buffer
	opts.Driver = DriverSQLite
	rows, truncated, err := streamExport(context.Background(), db, query, nil, opts, &out)
	if err != nil {
		t.Fatalf("export %s: %v", opts.Format, err)
	}
	return out.String(), rows, truncated
}

func TestExportFormats(t *testing.T) {
	db, _ := openTestDB(t)
	if _, err := db.Exec(`CREATE TABLE odd (id INTEGER, note TEXT, bytes BLOB, big INTEGER);
		INSERT INTO odd VALUES (1, 'tab	and "quote"', x'00ff', 9007199254740993), (2, NULL, NULL, NULL)`); err != nil {
		t.Fatal(err)
	}
	const query = `SELECT id, note, bytes, big FROM odd ORDER BY id`

	tsv, rows, _ := exportOf(t, db, query, ExportOptions{Format: ExportTSV})
	if rows != 2 || !strings.HasPrefix(tsv, "id\tnote\tbytes\tbig\n") || !strings.Contains(tsv, "\"tab\tand \"\"quote\"\"\"") {
		t.Errorf("tsv:\n%s", tsv)
	}

	ndjson, _, _ := exportOf(t, db, query, ExportOptions{Format: ExportNDJSON})
	lines := strings.Split(strings.TrimSpace(ndjson), "\n")
	if len(lines) != 2 {
		t.Fatalf("ndjson:\n%s", ndjson)
	}
	// Column order, an integer past 2^53 kept exact, binary whole, NULL null.
	if lines[0] != `{"id":"1","note":"tab\tand \"quote\"","bytes":"\\x00ff","big":"9007199254740993"}` {
		t.Errorf("ndjson row 1 = %s", lines[0])
	}
	if lines[1] != `{"id":"2","note":null,"bytes":null,"big":null}` {
		t.Errorf("ndjson row 2 = %s", lines[1])
	}

	sqlText, _, _ := exportOf(t, db, query, ExportOptions{Format: ExportSQL, Table: "odd"})
	for _, want := range []string{
		"-- Just Dashboard export\n", `INSERT INTO "odd" ("id", "note", "bytes", "big") VALUES`,
		`X'00FF'`, `9007199254740993`, `(2, NULL, NULL, NULL)`, "-- export complete: 2 rows",
	} {
		if !strings.Contains(sqlText, want) {
			t.Errorf("sql export lacks %q:\n%s", want, sqlText)
		}
	}
	// An export is not a dump, and a restore must not take it for one.
	if strings.HasPrefix(sqlText, dumpHeader) {
		t.Error("a SQL export carries the dump header")
	}
	if _, _, err := streamExport(context.Background(), db, query, nil, ExportOptions{Format: ExportSQL, Driver: DriverSQLite}, &bytes.Buffer{}); err == nil {
		t.Error("the SQL format was written with no table to insert into")
	}

	// The limit, and what each format says when it is reached.
	for format, marker := range map[ExportFormat]string{
		ExportJSON:   `{"__export":{"rows":1,"status":"truncated"}}`,
		ExportNDJSON: `{"__export":{"rows":1,"status":"truncated"}}`,
		ExportSQL:    "-- export truncated at 1 rows",
	} {
		text, rows, truncated := exportOf(t, db, query, ExportOptions{Format: format, MaxRows: 1, Table: "odd"})
		if rows != 1 || !truncated || !strings.Contains(text, marker) {
			t.Errorf("%s at its limit: rows=%d truncated=%v\n%s", format, rows, truncated, text)
		}
	}
	// A complete file carries rows and nothing else.
	whole, _, _ := exportOf(t, db, query, ExportOptions{Format: ExportJSON})
	var parsed []map[string]any
	if err := json.Unmarshal([]byte(whole), &parsed); err != nil || len(parsed) != 2 || strings.Contains(whole, ExportMarkerKey) {
		t.Errorf("complete json: %v\n%s", err, whole)
	}
	empty, rows, _ := exportOf(t, db, `SELECT id FROM odd WHERE id > 99`, ExportOptions{Format: ExportJSON})
	if rows != 0 || strings.TrimSpace(empty) != "[\n]" {
		t.Errorf("an empty result as json = %q", empty)
	}

	// A projection, in the order asked for; a column that is not there is an
	// error and not a column left out.
	picked, _, _ := exportOf(t, db, query, ExportOptions{Format: ExportCSV, Columns: []string{"big", "id"}})
	if !strings.HasPrefix(picked, "big,id\n9007199254740993,1\n") {
		t.Errorf("projection:\n%s", picked)
	}
	if _, _, err := streamExport(context.Background(), db, query, nil,
		ExportOptions{Format: ExportCSV, Columns: []string{"nope"}}, &bytes.Buffer{}); err == nil {
		t.Error("a projection onto a column the result lacks was accepted")
	}
}

func TestClampExportRows(t *testing.T) {
	for in, want := range map[int]int{0: DefaultExportRows, -5: DefaultExportRows, 10: 10, MaxExportRows: MaxExportRows, MaxExportRows + 1: MaxExportRows} {
		if got := ClampExportRows(in); got != want {
			t.Errorf("ClampExportRows(%d) = %d, want %d", in, got, want)
		}
	}
}

// A failure after rows have been written leaves the format's closing remark
// behind, so what was written does not pass for the whole result.
func TestExportMarksAFailurePartway(t *testing.T) {
	db, _ := openTestDB(t)
	const query = `SELECT id, CASE WHEN id = 2 THEN abs(-9223372036854775807 - 1) ELSE id END AS n FROM users ORDER BY id`
	for format, marker := range map[ExportFormat]string{
		ExportJSON: `"status":"failed"`, ExportNDJSON: `"status":"failed"`, ExportSQL: "-- export failed after 1 rows",
	} {
		var out bytes.Buffer
		rows, _, err := streamExport(context.Background(), db, query, nil,
			ExportOptions{Format: format, Driver: DriverSQLite, Table: "users"}, &out)
		if err == nil || rows != 1 || !strings.Contains(out.String(), marker) {
			t.Errorf("%s: rows=%d err=%v\n%s", format, rows, err, out.String())
		}
	}
}

// --- literals ----------------------------------------------------------------

func TestDumpColumnValueKnowsItsColumn(t *testing.T) {
	at := time.Date(2026, 3, 4, 5, 6, 7, 123456000, time.FixedZone("x", 2*3600))
	for _, c := range []struct {
		driver   Driver
		value    any
		typeName string
		want     string
	}{
		// An instant is written in UTC and says so where the engine listens.
		{DriverPostgres, at, "TIMESTAMPTZ", `'2026-03-04 03:06:07.123456+00'`},
		{DriverMySQL, at, "DATETIME", `'2026-03-04 03:06:07.123456'`},
		{DriverClickHouse, at, "DateTime", `toDateTime('2026-03-04 03:06:07', 'UTC')`},
		{DriverClickHouse, at, "Nullable(DateTime64(6, 'Europe/Berlin'))", `toDateTime64('2026-03-04 03:06:07.123456000', 6, 'UTC')`},
		{DriverClickHouse, at, "Date", `'2026-03-04'`},
		// Postgres has spellings for the values nothing else takes.
		{DriverPostgres, math.NaN(), "FLOAT8", `'NaN'`},
		{DriverPostgres, math.Inf(-1), "FLOAT8", `'-Infinity'`},
		{DriverMySQL, math.NaN(), "DOUBLE", `NULL`},
		// A wide integer is its digits, not the fields of the struct holding it.
		{DriverClickHouse, new(big.Int).Exp(big.NewInt(2), big.NewInt(200), nil), "UInt256",
			"1606938044258990275541962092341162602522202993782792835301376"},
		{DriverClickHouse, (*big.Int)(nil), "Nullable(Int128)", `NULL`},
		{DriverMySQL, []byte("text"), "VARCHAR", `'text'`},
		{DriverMySQL, []byte{0, 1}, "BLOB", `0x0001`},
	} {
		binary := isBinaryTypeName(c.typeName)
		if got := dumpColumnValue(c.driver, c.value, binary, c.typeName); got != c.want {
			t.Errorf("%s %T into %s:\n got %s\nwant %s", c.driver, c.value, c.typeName, got, c.want)
		}
	}
	// A string too long for one Oracle literal is written as pieces.
	long := strings.Repeat("é'", 1500)
	got := dumpString(DriverOracle, long)
	if strings.Count(got, "TO_CLOB('") != 3 || !strings.Contains(got, " || ") || strings.Contains(got, "TO_CLOB('')") {
		t.Errorf("a long Oracle string = %.80s… (%d pieces)", got, strings.Count(got, "TO_CLOB('"))
	}
}

// --- the dump file's grammar -------------------------------------------------

func TestStatementReaderTakesFencedStatementsWhole(t *testing.T) {
	text := dumpHeader + "\n-- a note; with a semicolon\n" +
		"DROP TABLE IF EXISTS t;\n" +
		rawStatementBegin + "\nCREATE TRIGGER trg AFTER INSERT ON t BEGIN\n  UPDATE t SET n = n + 1; -- not the end\n  DELETE FROM u WHERE x = ';';\nEND;\n" + rawStatementEnd + "\n" +
		"INSERT INTO t VALUES ('a;b', 'it''s'), ('c', '--not a comment');\n" +
		rawStatementBegin + "\nDO $jd$ BEGIN\n  PERFORM 1;\nEND $jd$;\n" + rawStatementEnd + "\n" +
		"SELECT 1 -- trailing\n;\n"
	got := splitSQLStatements(DriverPostgres, text)
	want := []string{
		"DROP TABLE IF EXISTS t",
		"CREATE TRIGGER trg AFTER INSERT ON t BEGIN\n  UPDATE t SET n = n + 1; -- not the end\n  DELETE FROM u WHERE x = ';';\nEND",
		"INSERT INTO t VALUES ('a;b', 'it''s'), ('c', '--not a comment')",
		"DO $jd$ BEGIN\n  PERFORM 1;\nEND $jd$",
		"SELECT 1",
	}
	if len(got) != len(want) {
		t.Fatalf("%d statements, want %d:\n%q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("statement %d:\n got %q\nwant %q", i, got[i], want[i])
		}
	}

	// What the writer writes, the reader reads back.
	var buf bytes.Buffer
	writeStatement(&buf, rawStmt("CREATE VIEW v AS SELECT ';' AS semi;"))
	writeStatement(&buf, stmt("SELECT 2;;"))
	writeStatement(&buf, stmt("  "))
	if got := splitSQLStatements(DriverSQLite, buf.String()); len(got) != 2 || got[0] != "CREATE VIEW v AS SELECT ';' AS semi" || got[1] != "SELECT 2" {
		t.Errorf("round trip = %q", got)
	}
}

func TestDumpSelection(t *testing.T) {
	sel, err := newDumpSelection([]string{"public.users", "orders"}, []string{"audit.orders"})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		schema, table string
		want          bool
	}{
		{"public", "users", true},
		{"other", "users", false},
		{"public", "orders", true},
		{"audit", "orders", false},
		{"public", "sessions", false},
	} {
		if got := sel.wants(c.schema, c.table); got != c.want {
			t.Errorf("wants(%s.%s) = %v", c.schema, c.table, got)
		}
	}
	if got := sel.include[0].postgresPattern(); got != `"public"."users"` {
		t.Errorf("pattern = %s", got)
	}
	// A star in a name is a star, not every table.
	if got := (tableRef{table: `we"ird*`}).postgresPattern(); got != `"we""ird*"` {
		t.Errorf("pattern = %s", got)
	}
	if missing := sel.missing(func(ref tableRef) bool { return ref.table == "users" }); fmt.Sprint(missing) != "[orders]" {
		t.Errorf("missing = %v", missing)
	}
	for _, bad := range [][]string{{""}, {"a.b.c"}, {"bad\x00name"}} {
		if _, err := newDumpSelection(bad, nil); err == nil {
			t.Errorf("selection %q was accepted", bad)
		}
	}
	none, _ := newDumpSelection(nil, nil)
	if !none.wants("any", "thing") || none.narrowed() {
		t.Error("an empty selection does not want everything")
	}
}

func TestValidateDumpOptionsRefusesWhatAnEngineCannotHonour(t *testing.T) {
	for _, c := range []struct {
		driver Driver
		opts   DumpOptions
		ok     bool
	}{
		{DriverPostgres, DumpOptions{SchemaOnly: true, Tables: []string{"a"}, Compression: CompressionNone}, true},
		{DriverPostgres, DumpOptions{SchemaOnly: true, DataOnly: true}, false},
		{DriverPostgres, DumpOptions{Compression: "zstd"}, false},
		{DriverPostgres, DumpOptions{RedisDatabases: []int{1}}, false},
		{DriverMySQL, DumpOptions{DataOnly: true, ExcludeTables: []string{"logs"}, Compression: CompressionGzip}, true},
		{DriverSQLite, DumpOptions{SchemaOnly: true}, true},
		{DriverMongo, DumpOptions{Tables: []string{"users"}}, true},
		{DriverMongo, DumpOptions{SchemaOnly: true}, false},
		{DriverMongo, DumpOptions{DataOnly: true}, false},
		{DriverRedis, DumpOptions{RedisDatabases: []int{0, 3}}, true},
		{DriverRedis, DumpOptions{RedisDatabases: []int{-1}}, false},
		{DriverRedis, DumpOptions{Tables: []string{"k"}}, false},
		{DriverRedis, DumpOptions{Compression: CompressionGzip}, false},
		{DriverClickHouse, DumpOptions{Tables: []string{"a.b.c"}}, false},
	} {
		err := ValidateDumpOptions(c.driver, c.opts)
		if (err == nil) != c.ok {
			t.Errorf("%s %+v: err = %v, want ok = %v", c.driver, c.opts, err, c.ok)
		}
	}
	// What the form may offer agrees with what the request will accept.
	for _, driver := range Drivers() {
		has := DumpCapabilities(driver)
		if err := ValidateDumpOptions(driver, DumpOptions{SchemaOnly: true}); (err == nil) != has.SchemaOnly {
			t.Errorf("%s: schemaOnly capability %v, validation %v", driver, has.SchemaOnly, err)
		}
		if err := ValidateDumpOptions(driver, DumpOptions{Tables: []string{"t"}}); (err == nil) != has.Tables {
			t.Errorf("%s: tables capability %v, validation %v", driver, has.Tables, err)
		}
	}
}

// --- names and descriptions --------------------------------------------------

func TestFreeDumpPathNeverNamesAFileThatIsThere(t *testing.T) {
	dir := t.TempDir()
	first := freeDumpPath(dir, "shop-20260102-030405.sql.gz")
	if filepath.Base(first) != "shop-20260102-030405.sql.gz" {
		t.Fatalf("first = %s", first)
	}
	os.WriteFile(first, nil, 0o600)
	second := freeDumpPath(dir, "shop-20260102-030405.sql.gz")
	if filepath.Base(second) != "shop-20260102-030405-2.sql.gz" {
		t.Fatalf("second = %s", second)
	}
	os.WriteFile(second, nil, 0o600)
	if third := freeDumpPath(dir, "shop-20260102-030405.sql.gz"); filepath.Base(third) != "shop-20260102-030405-3.sql.gz" {
		t.Errorf("third = %s", third)
	}
}

// Of several dumps after one name, one gets it and none is written over.
func TestPlaceDumpGivesANameToOneDumpOnly(t *testing.T) {
	dir := t.TempDir()
	const writers = 16
	var wg sync.WaitGroup
	placed := make([]error, writers)
	for i := range placed {
		staging, discard, err := NewDumpStaging(dir, "upload")
		if err != nil {
			t.Fatal(err)
		}
		defer discard()
		staged := filepath.Join(staging, "shop.sql")
		if err := os.WriteFile(staged, []byte(fmt.Sprintf("-- dump %d\n", i)), 0o600); err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, placed[i] = PlaceDump(staged, dir, "shop.sql")
		}()
	}
	wg.Wait()
	winner := -1
	for i, err := range placed {
		switch {
		case err == nil && winner >= 0:
			t.Fatalf("writers %d and %d were both given the name", winner, i)
		case err == nil:
			winner = i
		case !errors.Is(err, fs.ErrExist):
			t.Errorf("writer %d: %v, want the name reported as taken", i, err)
		case strings.Contains(err.Error(), dir):
			t.Errorf("the refusal names the directory: %v", err)
		}
	}
	if winner < 0 {
		t.Fatal("nobody was given the name")
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "shop.sql")); string(got) != fmt.Sprintf("-- dump %d\n", winner) {
		t.Errorf("the file holds %q, which is not what writer %d put there", got, winner)
	}
}

// A dump has no name until it is whole: while it is written the directory
// holds nothing a listing would take for one, and what a dead process left is
// cleared by the next.
func TestADumpIsWrittenOutOfSightAndNamedWhenWhole(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dumps")
	staging, discard, err := NewDumpStaging(dir, "dump")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(staging) != dir || !strings.HasPrefix(filepath.Base(staging), ".dump-") {
		t.Fatalf("staging = %s, want a hidden directory inside %s", staging, dir)
	}
	// Left by a process that died yesterday, and by one still running.
	stale, running, other := filepath.Join(dir, ".dump-dead"), filepath.Join(dir, ".upload-live"), filepath.Join(dir, ".kept")
	for _, d := range []string{stale, running, other} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(d, "half.sql"), []byte("-- half"), 0o600)
	}
	old := time.Now().Add(-dumpStagingStale - time.Hour)
	os.Chtimes(stale, old, old)
	os.Chtimes(other, old, old)
	discard()
	if _, err := os.Stat(staging); err == nil {
		t.Error("the staging directory outlived its dump")
	}
	if _, _, err := NewDumpStaging(dir, "dump"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Error("what a dead process left is still there")
	}
	for _, kept := range []string{running, other} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s was removed: it is not stale staging", filepath.Base(kept))
		}
	}

	// A real dump, twice in the same second: two files, each whole, each
	// under a name of its own, and nothing else left in the directory.
	source := filepath.Join(t.TempDir(), "shop.db")
	db, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE t (id integer primary key); INSERT INTO t VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	out := filepath.Join(t.TempDir(), "out")
	names := map[string]bool{}
	for range 3 {
		res, err := DumpWith(context.Background(), DriverSQLite, source, out, DumpOptions{})
		if err != nil {
			t.Fatalf("DumpWith: %v", err)
		}
		if filepath.Dir(res.Path) != out || res.File != filepath.Base(res.Path) || names[res.File] {
			t.Fatalf("dump placed at %s (file %s), after %v", res.Path, res.File, names)
		}
		names[res.File] = true
		if err := checkSQLiteFile(res.Path); err != nil {
			t.Errorf("%s is not a whole database: %v", res.File, err)
		}
	}
	entries, _ := os.ReadDir(out)
	if len(entries) != 3 {
		t.Errorf("the directory holds %d entries after three dumps", len(entries))
	}
	// One that fails leaves nothing at all.
	if _, err := DumpWith(context.Background(), DriverSQLite, filepath.Join(t.TempDir(), "missing", "x.db"), out, DumpOptions{}); err == nil {
		t.Fatal("a dump of a file that is not there succeeded")
	}
	if entries, _ := os.ReadDir(out); len(entries) != 3 {
		t.Errorf("a failed dump left something behind: %d entries", len(entries))
	}
}

func TestDumpMetaIsWrittenBesideTheDumpAndReadBack(t *testing.T) {
	dir := t.TempDir()
	dump := filepath.Join(dir, "shop-20260102-030405.dump")
	if err := os.WriteFile(dump, []byte("PGDMP…"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ReadDumpMeta(dump); got != nil {
		t.Fatalf("a dump with no description has one: %+v", got)
	}
	started := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	res := &DumpResult{
		Path: dump, File: filepath.Base(dump), Size: 6, Duration: "1.5s", Database: "shop",
		Driver: DriverPostgres, Summary: "written by pg_dump", StartedAt: started, Tool: "pg_dump", ToolVersion: "17.8",
	}
	opts := DumpOptions{SchemaOnly: true, Tables: []string{"public.users"}, Compression: CompressionNone}
	meta := MetaOf(res, opts)
	meta.Note, meta.By, meta.Connection = "before the migration", "ana", "shop"
	if err := WriteDumpMeta(dump, meta); err != nil {
		t.Fatal(err)
	}
	got := ReadDumpMeta(dump)
	if got == nil || got.Tool != "pg_dump" || got.ToolVersion != "17.8" || got.DurationMs != 1500 || got.Note != "before the migration" ||
		!got.Contents.SchemaOnly || len(got.Contents.Tables) != 1 || got.Origin != DumpOriginDump || !got.StartedAt.Equal(started) {
		t.Fatalf("read back %+v", got)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		// No temporary file is left, and the description is known for what it is.
		if e.Name() != filepath.Base(dump) && !IsDumpMetaFile(e.Name()) {
			t.Errorf("stray file beside the dump: %s", e.Name())
		}
	}
	if st, err := os.Stat(DumpMetaPath(dump)); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("the description is not a private file: %v %v", st, err)
	}
	// A description that cannot be read is no description, not an error.
	os.WriteFile(DumpMetaPath(dump), []byte("{not json"), 0o600)
	if ReadDumpMeta(dump) != nil {
		t.Error("a corrupt description was read")
	}
	if err := RemoveDumpMeta(dump); err != nil {
		t.Fatal(err)
	}
	if err := RemoveDumpMeta(dump); err != nil {
		t.Errorf("removing a description that is gone: %v", err)
	}
}

func TestDumpKindReadsTheFileNotItsName(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, content []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	gz := func(content string) []byte {
		path := filepath.Join(dir, "tmp.gz")
		df, err := newDumpFile(path, true)
		if err != nil {
			t.Fatal(err)
		}
		df.Write([]byte(content))
		df.Close()
		raw, _ := os.ReadFile(path)
		os.Remove(path)
		return raw
	}
	for want, path := range map[string]string{
		"pg_dump archive": write("renamed.txt", []byte("PGDMP\x01\x0e\x00")),
		"SQL":             write("ours.sql", []byte(dumpHeader+"\n-- engine: mysql\n")),
		"compressed SQL":  write("ours.sql.gz", gz(dumpHeader+"\n")),
		"SQLite file":     write("data.bin", []byte(sqliteFileHeader+"rest")),
		"JSON Lines":      write("redis-db0.jsonl.gz", gz(`{"format":"jd-redis"}`+"\n")),
	} {
		if got := DumpKind(path); got != want {
			t.Errorf("%s is listed as %q, want %q", filepath.Base(path), got, want)
		}
	}
	// SQL somebody else wrote is still SQL, and is told apart from ours.
	foreign := write("plain.sql", []byte("--\n-- PostgreSQL database dump\n--\nCREATE TABLE t (id int);\n"))
	if DumpKind(foreign) != "SQL" || dumpFormatOf(foreign) != dumpFormatForeignSQL {
		t.Errorf("a plain SQL dump: %q / %v", DumpKind(foreign), dumpFormatOf(foreign))
	}
	if dumpFormatOf(write("mysqldump.sql.gz", gz("-- MySQL dump 10.13\n"))) != dumpFormatNativeGzip {
		t.Error("a compressed script from another tool was taken for one of ours")
	}
}

func TestVersionNumberOutOfAToolsOwnLine(t *testing.T) {
	for line, want := range map[string]string{
		"pg_dump (PostgreSQL) 17.8 (Ubuntu 17.8-1.pgdg25.04+1)":                        "17.8",
		"mysqldump  Ver 8.4.11 for Linux on x86_64 (MySQL Community Server - GPL)":     "8.4.11",
		"mongodump version: 100.18.0":                                                  "100.18.0",
		"mariadb-dump from 11.8.9-MariaDB, client 10.20 for debian-linux-gnu (x86_64)": "11.8.9-MariaDB",
	} {
		if got := versionNumber(line); got != want {
			t.Errorf("versionNumber(%q) = %q, want %q", line, got, want)
		}
	}
}

// --- the built-in dump, on SQLite --------------------------------------------

// TestSQLiteBuiltInDumpCarriesTheObjectsBesideTheTables is the built-in SQL
// dump end to end without a server: indexes, a view over a view, a trigger
// whose body has semicolons in it, a generated column, an AUTOINCREMENT
// counter that is ahead of its rows.
func TestSQLiteBuiltInDumpCarriesTheObjectsBesideTheTables(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rich.db")
	db, err := sql.Open("sqlite", sqliteDialect{}.NormaliseDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{
		`CREATE TABLE authors (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL UNIQUE, shout TEXT GENERATED ALWAYS AS (upper(name)) STORED)`,
		`CREATE TABLE books (id INTEGER PRIMARY KEY, title TEXT NOT NULL, author_id INTEGER NOT NULL REFERENCES authors(id) ON DELETE CASCADE, cover BLOB)`,
		`CREATE TABLE log (line TEXT)`,
		`CREATE TABLE sqlitex (n INTEGER)`,
		`CREATE INDEX books_by_author ON books (author_id) WHERE title <> ''`,
		`CREATE VIEW titled AS SELECT b.id, b.title, a.name FROM books b JOIN authors a ON a.id = b.author_id`,
		`CREATE VIEW counted AS SELECT count(*) AS n FROM titled`,
		`CREATE TRIGGER books_logged AFTER INSERT ON books BEGIN
			INSERT INTO log VALUES ('added; ' || NEW.title);
			UPDATE log SET line = line WHERE 1 = 0;
		END`,
		`INSERT INTO authors (name) VALUES ('Ann'), ('Bo'), ('Gone')`,
		`DELETE FROM authors WHERE name = 'Gone'`,
		`INSERT INTO books VALUES (1, 'One; two', 1, x'00ff'), (2, 'It''s', 2, NULL)`,
		`INSERT INTO sqlitex VALUES (7)`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%v\n%s", err, s)
		}
	}
	db.Close()
	ctx := context.Background()

	// A whole dump with no options is the file itself; asked for anything
	// narrower, or compressed, it is the SQL dump.
	res, err := DumpWith(ctx, DriverSQLite, path, filepath.Join(dir, "dumps"), DumpOptions{Compression: CompressionGzip})
	if err != nil {
		t.Fatalf("DumpWith: %v", err)
	}
	if !strings.HasSuffix(res.File, ".sql.gz") || res.Tool != BuiltInDumpTool {
		t.Fatalf("result = %+v", res)
	}
	text := readDump(t, res.Path)
	if !strings.Contains(text, `"sqlitex"`) {
		t.Errorf("a table whose name only resembles the engine's own was left out:\n%s", text)
	}

	// Restored over a database that has drifted: a table emptied, the view
	// dropped, the trigger gone, an index gone.
	db, _ = sql.Open("sqlite", sqliteDialect{}.NormaliseDSN(path))
	defer db.Close()
	for _, s := range []string{`DELETE FROM books`, `DROP VIEW counted`, `DROP TRIGGER books_logged`, `DROP INDEX books_by_author`, `DELETE FROM log`} {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := RestoreWith(ctx, DriverSQLite, path, res.Path, RestoreOptions{}); err != nil {
		t.Fatalf("RestoreWith: %v\n%s", err, text)
	}
	for query, want := range map[string]string{
		`SELECT count(*) FROM books`:                "2",
		`SELECT title FROM books WHERE id = 1`:      "One; two",
		`SELECT hex(cover) FROM books WHERE id = 1`: "00FF",
		`SELECT shout FROM authors WHERE id = 1`:    "ANN",
		`SELECT n FROM counted`:                     "2",
		`SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = 'books_by_author'`: "1",
		`SELECT count(*) FROM sqlite_master WHERE type = 'trigger' AND name = 'books_logged'`:  "1",
		// The rows were put back without the trigger firing for each of them.
		`SELECT count(*) FROM log`: "2",
		// And the counter is where it was, past the row that was deleted.
		`SELECT seq FROM sqlite_sequence WHERE name = 'authors'`: "3",
		`SELECT n FROM sqlitex`:                                  "7",
	} {
		var got string
		if err := db.QueryRow(query).Scan(&got); err != nil || got != want {
			t.Errorf("%s = %q (%v), want %q", query, got, err, want)
		}
	}
	if _, err := db.Exec(`INSERT INTO books VALUES (9, 'orphan', 99, NULL)`); err == nil {
		t.Error("the foreign key is not enforced after the restore")
	}

	// A restore that fails changes nothing: the file is replayed as one
	// transaction.
	broken := filepath.Join(dir, "broken.sql")
	os.WriteFile(broken, []byte(dumpHeader+"\nDROP TABLE IF EXISTS \"books\";\nCREATE TABLE \"books\" (id INTEGER);\nINSERT INTO nowhere VALUES (1);\n"), 0o600)
	if _, err := RestoreWith(ctx, DriverSQLite, path, broken, RestoreOptions{}); err == nil || !strings.Contains(err.Error(), "nothing was changed") {
		t.Fatalf("err = %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM books`).Scan(&n); err != nil || n != 2 {
		t.Errorf("after a failed restore books holds %d rows (%v), want the 2 it had", n, err)
	}

	// Narrowed: structure only, one table.
	res, err = DumpWith(ctx, DriverSQLite, path, filepath.Join(dir, "dumps"), DumpOptions{SchemaOnly: true, Tables: []string{"main.books"}})
	if err != nil {
		t.Fatalf("DumpWith(narrow): %v", err)
	}
	narrow := readDump(t, res.Path)
	if strings.Contains(narrow, "INSERT INTO") || strings.Contains(narrow, `"authors"`) && strings.Contains(narrow, `CREATE TABLE authors`) ||
		!strings.Contains(narrow, "CREATE TABLE books") || !strings.Contains(narrow, "books_by_author") || strings.Contains(narrow, "CREATE VIEW") {
		t.Errorf("a structure-only dump of one table holds:\n%s", narrow)
	}
	if _, err := DumpWith(ctx, DriverSQLite, path, dir, DumpOptions{Tables: []string{"nothere"}}); err == nil {
		t.Error("a dump of a table that does not exist succeeded")
	}
}

// A restore of a SQLite file goes in through the engine, so a connection that
// was open before it reads the restored database afterwards — and the file
// that was there is kept.
func TestSQLiteFileRestoreIsSeenByAnOpenConnection(t *testing.T) {
	db, path := openTestDB(t)
	ctx := context.Background()
	dir := filepath.Join(filepath.Dir(path), "dumps")
	res, err := Dump(ctx, DriverSQLite, path, "", dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM posts; DELETE FROM users`); err != nil {
		t.Fatal(err)
	}
	out, err := Restore(ctx, DriverSQLite, path, "", res.Path)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil || n != 2 {
		t.Errorf("the open connection sees %d users (%v), want the 2 restored", n, err)
	}
	kept, _ := filepath.Glob(path + ".bak-*")
	if len(kept) != 1 || !strings.Contains(out, filepath.Base(kept[0])) {
		t.Fatalf("kept copies = %v; output %q", kept, out)
	}
	// The copy is of what was there: the emptied tables.
	old, _ := sql.Open("sqlite", kept[0])
	defer old.Close()
	if err := old.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil || n != 0 {
		t.Errorf("the kept copy holds %d users (%v), want the 0 there were", n, err)
	}
	// A second restore in the same second keeps a second copy.
	if _, err := Restore(ctx, DriverSQLite, path, "", res.Path); err != nil {
		t.Fatalf("second restore: %v", err)
	}
	if kept, _ := filepath.Glob(path + ".bak-*"); len(kept) != 2 {
		t.Errorf("after two restores %d copies are kept", len(kept))
	}
	// What is not a database is refused before anything is touched.
	junk := filepath.Join(dir, "junk.sqlite")
	os.WriteFile(junk, []byte("-- not a database"), 0o600)
	if _, err := Restore(ctx, DriverSQLite, path, "", junk); err == nil || !strings.Contains(err.Error(), "not a SQLite database") {
		t.Errorf("err = %v", err)
	}
}

// mongodump refuses a --db that is not the database its connection string
// names, so the string is moved — and the sign-in must not move with it.
func TestMongoURIForDatabaseKeepsWhereTheAccountIs(t *testing.T) {
	for _, c := range []struct{ dsn, database, want string }{
		{"mongodb://127.0.0.1:27017/app", "app", "mongodb://127.0.0.1:27017/app"},
		{"mongodb://127.0.0.1:27017/app", "other", "mongodb://127.0.0.1:27017/other"},
		{"mongodb://u:p@h:27017/app", "other", "mongodb://u:p@h:27017/other?authSource=app"},
		{"mongodb://u:p@h:27017/app?authSource=admin&tls=true", "other", "mongodb://u:p@h:27017/other?authSource=admin&tls=true"},
		{"mongodb://u:p@h:27017/app", "", "mongodb://u:p@h:27017/?authSource=app"},
		{"mongodb://u:p@h:27017", "other", "mongodb://u:p@h:27017/other"},
	} {
		if got := mongoURIForDatabase(c.dsn, c.database); got != c.want {
			t.Errorf("mongoURIForDatabase(%q, %q) = %q, want %q", c.dsn, c.database, got, c.want)
		}
	}
}

// Every tool this package runs is started through hostexec with an argument
// vector. Starting one any other way is the finding this closed, and the way
// it would come back is one import.
func TestNothingHereStartsAProcessOfItsOwn(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(src), `"os/exec"`) {
			t.Errorf("%s imports os/exec; run the tool through hostexec (dump_exec.go)", name)
		}
	}
}

// A name is a name. Whatever a request or a connection string puts in one, it
// reaches the tool as the value of an option or after the end of the options,
// never as an option of its own.
func TestToolArgumentsCannotBeTurnedIntoOptions(t *testing.T) {
	hostile := "--result-file=/etc/cron.d/x"
	info := &ConnInfo{Host: hostile, Port: "5432", User: hostile}
	sel, err := newDumpSelection([]string{"public.users"}, []string{"logs"})
	if err != nil {
		t.Fatal(err)
	}
	opts := DumpOptions{SchemaOnly: true, Compression: CompressionNone}

	for name, args := range map[string][]string{
		"pg_dump":      pgDumpArgs(info, hostile, "/dumps/x.dump", opts, sel),
		"pg_restore":   pgRestoreArgs(info, hostile, "/dumps/x.dump"),
		"mongodump":    mongodumpArgs("/tmp/m.yaml", hostile, "/dumps/x.archive", true, hostile, []string{hostile}),
		"mongorestore": mongorestoreArgs("/tmp/m.yaml", "/dumps/x.archive", true, hostile, "other"),
	} {
		ended := false
		for _, arg := range args {
			if arg == "--" {
				ended = true
				continue
			}
			if !ended && strings.HasPrefix(arg, hostile) {
				t.Errorf("%s is handed %q as an argument of its own: %q", name, hostile, args)
			}
		}
	}
	// mongorestore is confined to the database it was asked to restore into,
	// whichever one the archive came from.
	if got := strings.Join(mongorestoreArgs("/c", "/d/x.archive", true, "prod", "staging"), " "); got !=
		"--config=/c --archive=/d/x.archive --drop --gzip --nsInclude=prod.* --nsFrom=prod.* --nsTo=staging.*" {
		t.Errorf("mongorestore arguments = %s", got)
	}
	if got := strings.Join(mongorestoreArgs("/c", "/d/x.archive", false, "", "staging"), " "); got !=
		"--config=/c --archive=/d/x.archive --drop --nsInclude=staging.*" {
		t.Errorf("mongorestore arguments = %s", got)
	}
	if got := mongoArchiveDatabases("/nonexistent"); got != nil {
		t.Errorf("a file that is not there holds %v", got)
	}

	// mysqldump takes its database and tables as bare words, so they follow
	// the end of the options.
	args := mysqldumpArgs("/tmp/my.cnf", "shop", opts, sel)
	end := -1
	for i, arg := range args {
		if arg == "--" {
			end = i
		}
	}
	if end < 0 || strings.Join(args[end:], " ") != "-- shop users" {
		t.Errorf("mysqldump arguments = %q", args)
	}
	if got := strings.Join(pgDumpArgs(&ConnInfo{Host: "h", Port: "5", User: "u"}, "shop", "/d/x.dump", opts, sel), " "); got !=
		`--host=h --port=5 --username=u --no-password --format=custom --verbose --file=/d/x.dump --schema-only --compress=0 --table="public"."users" --exclude-table="logs" --dbname=shop` {
		t.Errorf("pg_dump arguments = %s", got)
	}

	// And such a name is refused before it gets that far.
	if err := validateDumpDatabase(hostile); err == nil {
		t.Error("a database name beginning with a dash was accepted")
	}
	if _, err := newDumpSelection([]string{"--all-databases"}, nil); err == nil {
		t.Error("a table name beginning with a dash was accepted")
	}
	if err := validateDumpDatabase("my-app"); err != nil {
		t.Errorf("a dash inside a name was refused: %v", err)
	}
}
