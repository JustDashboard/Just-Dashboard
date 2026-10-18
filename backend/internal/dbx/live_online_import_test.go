package dbx

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

// A database brought over from another server, against real ones. Everything
// these tests make is called jdt_online_import_*, on both servers.

const importLiveName = "jdt_online_import_sub"

// importLiveDatabase makes an empty database for the test and drops it again.
func importLiveDatabase(t *testing.T, driver Driver, adminDSN, name string) {
	t.Helper()
	ctx := context.Background()
	_, _ = DropDatabase(ctx, driver, adminDSN, name)
	if err := CreateDatabase(ctx, driver, adminDSN, name); err != nil {
		t.Skipf("this login cannot create a database: %v", err)
	}
	t.Cleanup(func() { _, _ = DropDatabase(context.Background(), driver, adminDSN, name) })
}

func importLiveCount(t *testing.T, db *sql.DB, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%v\n%s", err, query)
	}
	return n
}

// The publisher is the shared PostgreSQL fixture, which runs with
// wal_level=logical; the subscriber is a PostgreSQL of the run's own that
// reaches it on 127.0.0.1 (a container on the host's network), because a
// subscription is dialled by the subscribing server and the fixture's address
// means itself from inside the fixture.
func TestLiveOnlineImportReplicatesAndCutsOver(t *testing.T) {
	publisher := os.Getenv("JD_TEST_POSTGRES_DSN")
	subscriber := os.Getenv("JD_TEST_POSTGRES_SUBSCRIBER_DSN")
	if publisher == "" || subscriber == "" {
		t.Skip("JD_TEST_POSTGRES_DSN and JD_TEST_POSTGRES_SUBSCRIBER_DSN name the two servers")
	}
	ctx := context.Background()
	const (
		sourceName = "jdt_online_import_src"
		targetName = "jdt_online_import_dst"
	)
	source := DSNForDatabase(DriverPostgres, publisher, sourceName)
	target := DSNForDatabase(DriverPostgres, subscriber, targetName)

	// Whatever an earlier run left holds the source database open: a database
	// with a replication slot cannot be dropped.
	removeAll := func() {
		bg := context.Background()
		_ = DropSubscription(bg, target, importLiveName)
		_ = DropSlot(bg, source, importLiveName)
		_ = DropPublication(bg, source, importLiveName)
	}
	removeAll()
	importLiveDatabase(t, DriverPostgres, publisher, sourceName)
	importLiveDatabase(t, DriverPostgres, subscriber, targetName)
	t.Cleanup(removeAll)
	// Registered after the databases', so it runs first: the slot goes before
	// the database it belongs to.

	src, err := openPostgres(ctx, source)
	if err != nil {
		t.Skipf("the publisher does not answer: %v", err)
	}
	defer src.Close()
	execAll(t, src,
		`CREATE SCHEMA shop`,
		`CREATE TABLE shop.orders (id bigserial PRIMARY KEY, customer text NOT NULL, total numeric(10,2), note text)`,
		`CREATE TABLE shop.notes (id serial PRIMARY KEY, body text)`,
		`CREATE TABLE shop.tags (name text)`,
		`INSERT INTO shop.orders (customer, total, note) SELECT 'customer ' || g, g * 1.5, CASE WHEN g % 7 = 0 THEN NULL ELSE 'it''s order ' || g END FROM generate_series(1, 2000) g`,
		`INSERT INTO shop.notes (body) VALUES ('first'), ('second')`,
		`INSERT INTO shop.tags (name) VALUES ('a'), ('b'), ('c')`,
	)

	// A table with no key would make the source refuse its own updates once it
	// is published, so the inspection says so and the replication waits for it.
	report, err := InspectReplicationSource(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	if report.Ready() || !strings.Contains(strings.Join(report.Blockers, "\n"), `ALTER TABLE "shop"."tags" REPLICA IDENTITY FULL`) {
		t.Fatalf("a keyless table was not a blocker: %+v", report.Blockers)
	}
	if report.WalLevel != "logical" || len(report.Tables) != 3 || report.Sequences != 2 {
		t.Errorf("report = %+v", report)
	}
	execAll(t, src, `ALTER TABLE shop.tags REPLICA IDENTITY FULL`)
	if report, err = InspectReplicationSource(ctx, source); err != nil || !report.Ready() {
		t.Fatalf("blockers after the fix: %v %+v", err, report)
	}
	tgt, err := InspectReplicationTarget(ctx, target)
	if err != nil || !tgt.CanSubscribe {
		t.Fatalf("target = %+v, %v", tgt, err)
	}

	// The structure first, over the same stream a copy uses.
	var lines []string
	res, err := CopyBetween(ctx, DriverPostgres, source, subscriber, CopyOptions{
		TargetDatabase: targetName, SchemaOnly: true, Progress: func(l string) { lines = append(lines, l) },
	})
	if err != nil {
		t.Fatalf("structure: %v", err)
	}
	if !res.Streamed {
		t.Error("the structure was staged on disk")
	}
	dst, err := openPostgres(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if n := importLiveCount(t, dst, `SELECT count(*) FROM shop.orders`); n != 0 {
		t.Fatalf("the structure copy carried %d rows", n)
	}

	have, err := TablesOf(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	var publish []ReplicationTable
	for _, table := range report.Tables {
		if table.Has(have) {
			publish = append(publish, table)
		}
	}
	if len(publish) != 3 {
		t.Fatalf("publishing %d tables, want 3", len(publish))
	}
	if err := CreatePublication(ctx, source, importLiveName, publish); err != nil {
		t.Fatal(err)
	}
	if exists, tables, err := PublicationExists(ctx, source, importLiveName); err != nil || !exists || tables != 3 {
		t.Fatalf("publication: %v %v %d", err, exists, tables)
	}
	conninfo, err := ReplicationConninfo(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := CreateSlot(ctx, source, importLiveName); err != nil {
		t.Fatal(err)
	}
	if err := CreateSubscription(ctx, target, importLiveName, conninfo, importLiveName); err != nil {
		t.Fatal(err)
	}

	waitForImport(t, 60*time.Second, "the initial copy", func() bool {
		state, err := ReadSubscription(ctx, target, importLiveName)
		if err != nil {
			t.Fatal(err)
		}
		return state.Exists && len(state.Tables) == 3 && state.Ready == 3 && state.Applying
	})
	if n := importLiveCount(t, dst, `SELECT count(*) FROM shop.orders`); n != 2000 {
		t.Fatalf("orders after the copy: %d", n)
	}

	// The source keeps taking writes: an insert, an update and a delete.
	execAll(t, src,
		`INSERT INTO shop.orders (customer, total) VALUES ('late', 1)`,
		`UPDATE shop.orders SET customer = 'renamed' WHERE id = 10`,
		`DELETE FROM shop.orders WHERE id = 11`,
		`DELETE FROM shop.tags WHERE name = 'b'`,
	)
	lag, err := ReadSlotLag(ctx, source, importLiveName)
	if err != nil || !lag.Exists || !lag.Active {
		t.Fatalf("slot = %+v, %v", lag, err)
	}
	var reported []int64
	if err := WaitCaughtUp(ctx, source, importLiveName, func(n int64) { reported = append(reported, n) }); err != nil {
		t.Fatal(err)
	}
	if len(reported) == 0 || reported[len(reported)-1] != 0 {
		t.Errorf("the wait ended without saying the lag was zero: %v", reported)
	}
	if n := importLiveCount(t, dst, `SELECT count(*) FROM shop.orders`); n != 2000 {
		t.Errorf("orders after the changes: %d, want 2000 (one in, one out)", n)
	}
	var renamed string
	if err := dst.QueryRowContext(ctx, `SELECT customer FROM shop.orders WHERE id = 10`).Scan(&renamed); err != nil || renamed != "renamed" {
		t.Errorf("update: %q %v", renamed, err)
	}
	if n := importLiveCount(t, dst, `SELECT count(*) FROM shop.tags`); n != 2 {
		t.Errorf("tags = %d, want 2", n)
	}

	// Replication does not carry the sequences: the target's still stand at the
	// start, and the cutover sets them where the source's are.
	if n := importLiveCount(t, dst, `SELECT last_value FROM shop.orders_id_seq`); n != 1 {
		t.Fatalf("the target's sequence was already at %d", n)
	}
	set, missing, err := SyncSequences(ctx, source, target)
	if err != nil || set != 2 || len(missing) != 0 {
		t.Fatalf("sequences: %d set, missing %v, %v", set, missing, err)
	}
	if n := importLiveCount(t, dst, `SELECT last_value FROM shop.orders_id_seq`); n != 2001 {
		t.Errorf("orders_id_seq = %d, want 2001", n)
	}
	if n := importLiveCount(t, dst, `SELECT nextval('shop.notes_id_seq')`); n != 3 {
		t.Errorf("the next note would be %d, want 3", n)
	}

	if err := DropSubscription(ctx, target, importLiveName); err != nil {
		t.Fatal(err)
	}
	if err := DropSlot(ctx, source, importLiveName); err != nil {
		t.Fatal(err)
	}
	if err := DropPublication(ctx, source, importLiveName); err != nil {
		t.Fatal(err)
	}
	if state, err := ReadSubscription(ctx, target, importLiveName); err != nil || state.Exists {
		t.Errorf("the subscription is still there: %+v %v", state, err)
	}
	if n := importLiveCount(t, src, `SELECT count(*) FROM pg_replication_slots WHERE slot_name = $1`, importLiveName); n != 0 {
		t.Errorf("the slot is still on the source")
	}
	// Dropping what is already gone is not an error: a cutover that failed
	// after this step is run again.
	if err := DropSubscription(ctx, target, importLiveName); err != nil {
		t.Errorf("dropping twice: %v", err)
	}
	if err := DropSlot(ctx, source, importLiveName); err != nil {
		t.Errorf("dropping the slot twice: %v", err)
	}
	// The data is the target's now: a write there does not go to the source.
	execAll(t, dst, `INSERT INTO shop.orders (customer) VALUES ('after the cutover')`)
	if n := importLiveCount(t, src, `SELECT count(*) FROM shop.orders WHERE customer = 'after the cutover'`); n != 0 {
		t.Errorf("a write after the cutover reached the source")
	}
}

func waitForImport(t *testing.T, limit time.Duration, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not finish in %s", what, limit)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// A subscription the target cannot dial is refused, and the refusal neither
// leaves one behind nor quotes the password it was given.
func TestLiveOnlineImportCreatingASubscriptionTheServerRefusesFailsCleanly(t *testing.T) {
	subscriber := os.Getenv("JD_TEST_POSTGRES_SUBSCRIBER_DSN")
	if subscriber == "" {
		t.Skip("JD_TEST_POSTGRES_SUBSCRIBER_DSN names a PostgreSQL of the run's own")
	}
	ctx := context.Background()
	name := "jdt_online_import_refused"
	// Nothing listens on port 1.
	conninfo, err := ReplicationConninfo("postgres://reader:jdt-online-import-secret@127.0.0.1:1/shop?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	err = CreateSubscription(ctx, subscriber, name, conninfo, "jdt_online_import_nothing")
	if err == nil {
		_ = DropSubscription(ctx, subscriber, name)
		t.Fatal("a subscription to a server that does not answer was created")
	}
	if strings.Contains(err.Error(), "jdt-online-import-secret") {
		t.Errorf("the error carries the password: %v", err)
	}
	if state, err := ReadSubscription(ctx, subscriber, name); err != nil || state.Exists {
		t.Errorf("a refused subscription exists: %+v %v", state, err)
	}
}

func TestLiveOnlineImportInspectingASourceThatIsNotLogical(t *testing.T) {
	subscriber := os.Getenv("JD_TEST_POSTGRES_SUBSCRIBER_DSN")
	if subscriber == "" {
		t.Skip("JD_TEST_POSTGRES_SUBSCRIBER_DSN names a PostgreSQL of the run's own")
	}
	// The subscriber is started at wal_level=replica, so as a source it is
	// not ready, and says which setting to change.
	report, err := InspectReplicationSource(context.Background(), subscriber)
	if err != nil {
		t.Fatal(err)
	}
	if report.Ready() || !strings.Contains(strings.Join(report.Blockers, "\n"), "wal_level") {
		t.Errorf("blockers = %v", report.Blockers)
	}
}

// --- a streamed copy ---------------------------------------------------------

// MariaDB and MySQL speak one wire protocol and read one dump, so a database
// goes from the first to the second as it is read.
func TestLiveCopyStreamsFromMariaDBToMySQL(t *testing.T) {
	from := os.Getenv("JD_TEST_MYSQL_ADMIN_DSN")
	to := os.Getenv("JD_TEST_MYSQL8_ADMIN_DSN")
	if from == "" || to == "" {
		t.Skip("JD_TEST_MYSQL_ADMIN_DSN and JD_TEST_MYSQL8_ADMIN_DSN name the MariaDB and the MySQL server")
	}
	ctx := context.Background()
	const (
		sourceName = "jdt_online_import_src"
		targetName = "jdt_online_import_dst"
		structName = "jdt_online_import_structure"
	)
	importLiveDatabase(t, DriverMySQL, from, sourceName)
	importLiveDatabase(t, DriverMySQL, to, targetName)
	importLiveDatabase(t, DriverMySQL, to, structName)

	src, err := OpenDatabase(ctx, DriverMySQL, from, sourceName)
	if err != nil {
		t.Skipf("the MariaDB server does not answer: %v", err)
	}
	defer src.Close()
	execAll(t, src,
		// A collation both servers know: MariaDB 11.4's default, uca1400, is one MySQL does not.
		"CREATE TABLE customers (id INT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(80) NOT NULL, joined DATETIME, UNIQUE KEY by_name (name)) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci",
		"CREATE TABLE orders (id INT AUTO_INCREMENT PRIMARY KEY, customer_id INT NOT NULL, total DECIMAL(10,2), note TEXT, doc BLOB, "+
			"KEY by_customer (customer_id), CONSTRAINT fk_customer FOREIGN KEY (customer_id) REFERENCES customers (id)) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci",
		"INSERT INTO customers (name, joined) VALUES ('ana', '2024-02-29 10:20:30'), ('o''neil', NULL), ('zoë ✓', '2020-01-01 00:00:00')",
		"INSERT INTO orders (customer_id, total, note, doc) SELECT 1 + (seq % 3), seq * 1.25, IF(seq % 5 = 0, NULL, CONCAT('order \\'', seq, '\\'')), UNHEX('00FF41') "+
			"FROM (SELECT a.n + b.n * 10 + c.n * 100 + d.n * 1000 AS seq FROM "+
			"(SELECT 0 n UNION SELECT 1 UNION SELECT 2 UNION SELECT 3 UNION SELECT 4 UNION SELECT 5 UNION SELECT 6 UNION SELECT 7 UNION SELECT 8 UNION SELECT 9) a, "+
			"(SELECT 0 n UNION SELECT 1 UNION SELECT 2 UNION SELECT 3 UNION SELECT 4 UNION SELECT 5 UNION SELECT 6 UNION SELECT 7 UNION SELECT 8 UNION SELECT 9) b, "+
			"(SELECT 0 n UNION SELECT 1 UNION SELECT 2 UNION SELECT 3 UNION SELECT 4 UNION SELECT 5 UNION SELECT 6 UNION SELECT 7 UNION SELECT 8 UNION SELECT 9) c, "+
			"(SELECT 0 n UNION SELECT 1 UNION SELECT 2) d) s",
	)
	want := importLiveCount(t, src, `SELECT COUNT(*) FROM orders`)
	if want != 3000 {
		t.Fatalf("seeded %d orders", want)
	}

	var last CopyProgress
	var lines []string
	res, err := CopyBetween(ctx, DriverMySQL, DSNForDatabase(DriverMySQL, from, sourceName), to, CopyOptions{
		TargetDatabase: targetName,
		Progress:       func(l string) { lines = append(lines, l) },
		Report:         func(p CopyProgress) { last = p },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Streamed || res.Tool != BuiltInDumpTool {
		t.Errorf("result = %+v", res)
	}
	if last.Tables != 2 || last.TablesDone != 2 || last.Rows != 3003 || last.Bytes == 0 {
		t.Errorf("progress = %+v", last)
	}

	dst, err := OpenDatabase(ctx, DriverMySQL, to, targetName)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if n := importLiveCount(t, dst, `SELECT COUNT(*) FROM orders`); n != want {
		t.Errorf("orders = %d, want %d", n, want)
	}
	if name := queryString(t, dst, `SELECT name FROM customers WHERE id = 3`); name != "zoë ✓" {
		t.Errorf("a name came over as %q", name)
	}
	if n := importLiveCount(t, dst, `SELECT COUNT(*) FROM orders WHERE note LIKE 'order \'%\''`); n != 2400 {
		t.Errorf("%d quoted notes came over whole, want 2400", n)
	}
	if n := importLiveCount(t, dst, `SELECT COUNT(*) FROM orders WHERE note IS NULL`); n != 600 {
		t.Errorf("%d NULL notes, want 600", n)
	}
	if n := importLiveCount(t, dst, `SELECT COUNT(*) FROM orders WHERE HEX(doc) = '00FF41'`); n != want {
		t.Errorf("%d blobs came over whole, want %d", n, want)
	}
	if n := importLiveCount(t, dst, `SELECT COUNT(*) FROM information_schema.table_constraints WHERE table_schema = ? AND constraint_name = 'fk_customer'`, targetName); n != 1 {
		t.Errorf("the foreign key did not come over")
	}
	if n := importLiveCount(t, dst, `SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema = ? AND index_name = 'by_name'`, targetName); n != 1 {
		t.Errorf("the unique index did not come over")
	}
	// The source is as it was.
	if got := importLiveCount(t, src, `SELECT COUNT(*) FROM orders`); got != want {
		t.Errorf("the source lost rows: %d", got)
	}

	// Structure alone leaves the rows.
	structure, err := CopyBetween(ctx, DriverMySQL, DSNForDatabase(DriverMySQL, from, sourceName), to, CopyOptions{
		TargetDatabase: structName, SchemaOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(structure.Summary, "structure only") {
		t.Errorf("summary = %q", structure.Summary)
	}
	other, err := OpenDatabase(ctx, DriverMySQL, to, structName)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if n := importLiveCount(t, other, `SELECT COUNT(*) FROM orders`); n != 0 {
		t.Errorf("a structure-only copy carried %d rows", n)
	}
}

// A copy into a database that is not there fails on the load side, and the
// dump that was feeding it stops instead of waiting for a reader.
func TestLiveCopyThatFailsToLoadStopsTheDump(t *testing.T) {
	from := os.Getenv("JD_TEST_MYSQL_ADMIN_DSN")
	to := os.Getenv("JD_TEST_MYSQL8_ADMIN_DSN")
	if from == "" || to == "" {
		t.Skip("JD_TEST_MYSQL_ADMIN_DSN and JD_TEST_MYSQL8_ADMIN_DSN name the MariaDB and the MySQL server")
	}
	ctx := context.Background()
	const sourceName = "jdt_online_import_src"
	importLiveDatabase(t, DriverMySQL, from, sourceName)
	src, err := OpenDatabase(ctx, DriverMySQL, from, sourceName)
	if err != nil {
		t.Skipf("the MariaDB server does not answer: %v", err)
	}
	defer src.Close()
	execAll(t, src, "CREATE TABLE t (id INT PRIMARY KEY)", "INSERT INTO t VALUES (1), (2), (3)")

	done := make(chan error, 1)
	go func() {
		_, err := CopyBetween(ctx, DriverMySQL, DSNForDatabase(DriverMySQL, from, sourceName), to, CopyOptions{
			TargetDatabase: "jdt_online_import_missing",
		})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "loading the target failed") {
			t.Errorf("err = %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the copy is still running")
	}
}

func TestLiveCopyIsStoppedByItsContext(t *testing.T) {
	from := os.Getenv("JD_TEST_MYSQL_ADMIN_DSN")
	to := os.Getenv("JD_TEST_MYSQL8_ADMIN_DSN")
	if from == "" || to == "" {
		t.Skip("JD_TEST_MYSQL_ADMIN_DSN and JD_TEST_MYSQL8_ADMIN_DSN name the MariaDB and the MySQL server")
	}
	const (
		sourceName = "jdt_online_import_src"
		targetName = "jdt_online_import_dst"
	)
	importLiveDatabase(t, DriverMySQL, from, sourceName)
	importLiveDatabase(t, DriverMySQL, to, targetName)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := CopyBetween(ctx, DriverMySQL, DSNForDatabase(DriverMySQL, from, sourceName), to, CopyOptions{TargetDatabase: targetName})
	if err == nil || !strings.Contains(err.Error(), "stopped") {
		t.Errorf("err = %v", err)
	}
}

// MongoDB and Redis dump to a file that is read by seeking, so a copy stages it
// in a directory of its own and takes the directory away when it ends.
func TestLiveCopyStagesAMongoDatabaseAndRemovesTheStaging(t *testing.T) {
	dsn := os.Getenv("JD_TEST_MONGO_DSN")
	if dsn == "" {
		t.Skip("JD_TEST_MONGO_DSN names a MongoDB the tests may write to")
	}
	ctx := context.Background()
	client, err := MongoClient(ctx, dsn)
	if err != nil {
		t.Skipf("MongoDB does not answer: %v", err)
	}
	defer client.Disconnect(context.Background())
	const (
		sourceName = "jdt_online_import_src"
		targetName = "jdt_online_import_dst"
	)
	source, target := client.Database(sourceName), client.Database(targetName)
	_ = source.Drop(ctx)
	_ = target.Drop(ctx)
	t.Cleanup(func() {
		_ = source.Drop(context.Background())
		_ = target.Drop(context.Background())
	})
	if _, err := source.Collection("things").InsertMany(ctx, []any{
		bson.D{{Key: "n", Value: 1}, {Key: "s", Value: "it's"}},
		bson.D{{Key: "n", Value: 2}, {Key: "s", Value: "two"}},
		bson.D{{Key: "n", Value: 3}},
	}); err != nil {
		t.Fatal(err)
	}

	scratch := t.TempDir()
	var reported []string
	res, err := CopyBetween(ctx, DriverMongo, mongoURIForDatabase(dsn, sourceName), dsn, CopyOptions{
		SourceDatabase: sourceName, TargetDatabase: targetName, ScratchDir: scratch,
		Report: func(p CopyProgress) { reported = append(reported, p.Phase) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Streamed {
		t.Error("a MongoDB copy claims to have been streamed")
	}
	if n, err := target.Collection("things").CountDocuments(ctx, bson.D{}); err != nil || n != 3 {
		t.Errorf("copied documents = %d, %v", n, err)
	}
	if n, err := target.Collection("things").CountDocuments(ctx, bson.D{{Key: "s", Value: "it's"}}); err != nil || n != 1 {
		t.Errorf("a quoted value came over %d times, %v", n, err)
	}
	if n, _ := source.Collection("things").CountDocuments(ctx, bson.D{}); n != 3 {
		t.Errorf("the source holds %d documents", n)
	}
	left, err := os.ReadDir(scratch)
	if err != nil || len(left) != 0 {
		t.Errorf("the staging directory kept %d entries: %v", len(left), err)
	}
	if len(reported) < 2 || reported[0] != "dumping" || reported[len(reported)-1] != "loading" {
		t.Errorf("phases = %v", reported)
	}
}
