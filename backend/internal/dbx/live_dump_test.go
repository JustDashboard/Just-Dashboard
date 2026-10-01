package dbx

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
)

// Dump and restore, against real servers.
//
// This is the test the "unsupported database driver: clickhouse" report needed
// and did not have: the dump surface had unit coverage for how it picked a
// pg_dump and none at all for whether pressing the button on each of the eight
// engines produced a file. A round trip is the only assertion worth making —
// a dump that cannot be restored is not a backup, it is a file.

// dumpFixtureRows is what every fixture seeds, and what has to come back.
const dumpFixtureRows = 2

func TestLiveDumpRoundTrip(t *testing.T) {
	for _, f := range dumpFixtures() {
		t.Run(string(f.driver), func(t *testing.T) {
			db := liveSQL(t, f.driver, f.env, f.dsn)
			ownsItsDatabase(t, db, f)
			setupFixture(t, db, f)
			ctx := context.Background()
			dsn := liveDSN(t, f.env, f.dsn)
			dir := t.TempDir()

			database := dumpTestDatabase(t, f)
			res, err := Dump(ctx, f.driver, dsn, database, dir)
			if err != nil {
				t.Fatalf("Dump: %v", err)
			}
			if res.Size == 0 {
				t.Fatalf("dump is empty: %s", res.Path)
			}
			// A dump that reports success and contains nothing is the failure
			// this is most likely to have: the summary is what puts it on screen.
			if res.Summary == "" {
				t.Errorf("dump has no summary; the toast would say nothing about what it holds")
			}
			if _, err := os.Stat(res.Path); err != nil {
				t.Fatalf("dump file missing: %v", err)
			}

			// Wipe the data, not the schema: a restore that only works into an
			// empty database is a restore nobody can use, since the case that
			// brings someone here is a table that has the wrong rows in it.
			for _, stmt := range []string{
				deleteAll(f, fixtureRel(t, f, "jd_posts")),
				deleteAll(f, fixtureRel(t, f, "jd_users")),
			} {
				if _, err := db.ExecContext(ctx, stmt); err != nil {
					t.Fatalf("wipe failed: %v\n%s", err, stmt)
				}
			}
			if f.driver == DriverClickHouse {
				// ClickHouse's DELETE is a background mutation, so the rows are
				// still readable for a moment after it returns.
				waitForCount(t, db, fixtureRel(t, f, "jd_users"), 0)
			}

			out, err := Restore(ctx, f.driver, dsn, database, res.Path)
			if err != nil {
				t.Fatalf("Restore: %v", err)
			}
			t.Logf("dump=%s (%d bytes, %s) restore=%s", filepath.Base(res.Path), res.Size, res.Output, out)

			for _, table := range []string{"jd_users", "jd_posts"} {
				if got := countRows(t, db, fixtureRel(t, f, table)); got != dumpFixtureRows {
					t.Errorf("%s has %d rows after restore, want %d", table, got, dumpFixtureRows)
				}
			}
		})
	}
}

// TestLiveDumpKeepsAwkwardValues is the half a round-trip count cannot see: the
// rows come back, but do they come back the same? Every value here is one the
// literal renderer has to escape rather than print, and each of them broke a
// different engine at some point in an earlier life of this code.
func TestLiveDumpKeepsAwkwardValues(t *testing.T) {
	for _, f := range dumpFixtures() {
		if !f.relational {
			// ClickHouse's fixture has no nullable text column to put these in
			// without rewriting the schema, and its own escaping is covered by
			// the round trip above.
			continue
		}
		t.Run(string(f.driver), func(t *testing.T) {
			db := liveSQL(t, f.driver, f.env, f.dsn)
			ownsItsDatabase(t, db, f)
			setupFixture(t, db, f)
			ctx := context.Background()
			dsn := liveDSN(t, f.env, f.dsn)

			awkward := `O'Brien \ "quoted" -- not a comment; DROP TABLE x; /* nor this */ ` + "\ttab\nnewline"
			users := fixtureRel(t, f, "jd_users")
			if _, err := db.ExecContext(ctx,
				`UPDATE `+users+` SET `+fixtureCol(t, f, "name")+` = `+bindOne(f)+` WHERE `+fixtureCol(t, f, "id")+` = 1`,
				awkward); err != nil {
				t.Fatalf("seeding the awkward value failed: %v", err)
			}

			dir := t.TempDir()
			res, err := Dump(ctx, f.driver, dsn, dumpTestDatabase(t, f), dir)
			if err != nil {
				t.Fatalf("Dump: %v", err)
			}
			if _, err := db.ExecContext(ctx, deleteAll(f, fixtureRel(t, f, "jd_posts"))); err != nil {
				t.Fatalf("wipe: %v", err)
			}
			if _, err := db.ExecContext(ctx, deleteAll(f, users)); err != nil {
				t.Fatalf("wipe: %v", err)
			}
			if _, err := Restore(ctx, f.driver, dsn, dumpTestDatabase(t, f), res.Path); err != nil {
				t.Fatalf("Restore: %v", err)
			}

			var got string
			if err := db.QueryRowContext(ctx,
				`SELECT `+fixtureCol(t, f, "name")+` FROM `+users+` WHERE `+fixtureCol(t, f, "id")+` = 1`).
				Scan(&got); err != nil {
				t.Fatalf("reading the value back: %v", err)
			}
			if got != awkward {
				t.Errorf("value did not survive the round trip:\n got %q\nwant %q", got, awkward)
			}
			// And the table the value tried to drop is still there.
			if countRows(t, db, users) != dumpFixtureRows {
				t.Errorf("row count wrong after restoring an escaped value")
			}
		})
	}
}

func TestLiveDumpMongoRoundTrip(t *testing.T) {
	client, dbName := liveMongo(t)
	ctx := context.Background()
	dsn := os.Getenv("JD_TEST_MONGO_DSN")
	if dsn == "" {
		dsn = "mongodb://127.0.0.1:27017/jdtest"
	}
	const coll = "jd_dump"
	_ = MongoDropCollection(ctx, client, dbName, coll)
	t.Cleanup(func() { _ = MongoDropCollection(context.Background(), client, dbName, coll) })

	docs := []any{
		bson.D{{Key: "name", Value: "Ann"}, {Key: "n", Value: int64(1)}},
		bson.D{{Key: "name", Value: "O'Brien \\ \"x\""}, {Key: "at", Value: time.Now().UTC().Truncate(time.Millisecond)}},
	}
	if _, err := client.Database(dbName).Collection(coll).InsertMany(ctx, docs); err != nil {
		t.Fatalf("seed: %v", err)
	}

	dir := t.TempDir()
	res, err := Dump(ctx, DriverMongo, dsn, dbName, dir)
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}
	if res.Size == 0 {
		t.Fatal("dump is empty")
	}
	if err := client.Database(dbName).Collection(coll).Drop(ctx); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, err := Restore(ctx, DriverMongo, dsn, dbName, res.Path); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	n, err := client.Database(dbName).Collection(coll).CountDocuments(ctx, bson.D{})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != int64(len(docs)) {
		t.Errorf("%d documents after restore, want %d", n, len(docs))
	}
}

// TestLiveDumpMongoWithoutTheTools proves the fallback, not just the tool.
// Skipping straight to the driver path is the only way to test the machine that
// has no mongodump — which is every machine that has not installed it, and was
// the whole of the original report.
func TestLiveDumpMongoWithoutTheTools(t *testing.T) {
	client, dbName := liveMongo(t)
	ctx := context.Background()
	dsn := os.Getenv("JD_TEST_MONGO_DSN")
	if dsn == "" {
		dsn = "mongodb://127.0.0.1:27017/jdtest"
	}
	const coll = "jd_dump_fallback"
	_ = MongoDropCollection(ctx, client, dbName, coll)
	t.Cleanup(func() { _ = MongoDropCollection(context.Background(), client, dbName, coll) })
	if _, err := client.Database(dbName).Collection(coll).
		InsertOne(ctx, bson.D{{Key: "k", Value: "v"}}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	dir := t.TempDir()
	res, err := dumpMongoDriver(ctx, dsn, dir, DumpOptions{Database: dbName})
	if err != nil {
		t.Fatalf("dumpMongoDriver: %v", err)
	}
	if !strings.HasSuffix(res.Path, ".jsonl.gz") {
		t.Errorf("fallback wrote %s, expected a .jsonl.gz archive", res.Path)
	}
	if err := client.Database(dbName).Collection(coll).Drop(ctx); err != nil {
		t.Fatalf("drop: %v", err)
	}
	// And Restore must recognise the archive from its contents rather than
	// reaching for mongorestore because the driver is Mongo.
	if _, err := Restore(ctx, DriverMongo, dsn, dbName, res.Path); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	n, err := client.Database(dbName).Collection(coll).CountDocuments(ctx, bson.D{})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("%d documents after restore, want 1", n)
	}
}

func TestLiveDumpRedisRoundTrip(t *testing.T) {
	client := liveRedis(t)
	ctx := context.Background()
	dsn := os.Getenv("JD_TEST_REDIS_DSN")
	if dsn == "" {
		dsn = "redis://127.0.0.1:6379/0"
	}
	keys := []string{"jd:dump:str", "jd:dump:hash", "jd:dump:zset", "jd:dump:list"}
	t.Cleanup(func() { client.Del(context.Background(), keys...) })
	client.Del(ctx, keys...)

	if err := client.Set(ctx, keys[0], "O'Brien \\ \"x\"", time.Hour).Err(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := client.HSet(ctx, keys[1], "f", "v", "g", "w").Err(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := client.ZAdd(ctx, keys[2], redisZ(1.5, "a"), redisZ(2.5, "b")).Err(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := client.RPush(ctx, keys[3], "one", "two", "three").Err(); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// The connection's own numbered database, by name. Left unnamed a dump is
	// of every database on the server, and this server is shared: restoring
	// that would write into databases this test has no business in.
	own, err := redisDatabaseIndex(dsn, "")
	if err != nil {
		t.Fatal(err)
	}
	database := strconv.Itoa(own)

	dir := t.TempDir()
	res, err := Dump(ctx, DriverRedis, dsn, database, dir)
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}
	if res.Size == 0 {
		t.Fatal("dump is empty")
	}
	if res.Database != database {
		t.Fatalf("dump covers %q, want only database %s", res.Database, database)
	}
	if err := client.Del(ctx, keys...).Err(); err != nil {
		t.Fatalf("wipe: %v", err)
	}
	if _, err := Restore(ctx, DriverRedis, dsn, "", res.Path); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if got := client.Get(ctx, keys[0]).Val(); got != "O'Brien \\ \"x\"" {
		t.Errorf("string came back as %q", got)
	}
	// A TTL has to survive too: restoring a key that was going to expire as one
	// that never will is a slow leak nobody notices.
	if ttl := client.TTL(ctx, keys[0]).Val(); ttl <= 0 || ttl > time.Hour {
		t.Errorf("ttl came back as %v", ttl)
	}
	if got := client.HGetAll(ctx, keys[1]).Val(); len(got) != 2 || got["f"] != "v" {
		t.Errorf("hash came back as %v", got)
	}
	if got := client.ZScore(ctx, keys[2], "b").Val(); got != 2.5 {
		t.Errorf("zset score came back as %v", got)
	}
	if got := client.LRange(ctx, keys[3], 0, -1).Val(); len(got) != 3 || got[0] != "one" {
		t.Errorf("list came back as %v", got)
	}
}

// --- helpers --------------------------------------------------------------

// dumpFixtures are the SQL fixtures as a dump test addresses them. An Oracle
// schema is a user, so a run given a user of its own finds its tables under
// that name and not under the one the fixture list spells.
func dumpFixtures() []engineFixture {
	fixtures := sqlFixtures()
	for i, f := range fixtures {
		if f.driver != DriverOracle {
			continue
		}
		if info, err := ParseDSN(f.driver, os.Getenv(f.env)); err == nil && info.User != "" {
			fixtures[i].schema = strings.ToUpper(info.User)
		}
	}
	return fixtures
}

// ownsItsDatabase skips a round trip in a database that holds anything the
// fixture did not make.
//
// A dump is of a whole database and its restore drops and recreates every
// table in the file. On the two engines whose fixture server is one database
// shared by whoever is testing — SQL Server, where a connection string names
// master unless it says otherwise, and Oracle, where the unit is a user — that
// is somebody else's tables dropped. The test runs where the database is its
// own and says what it found where it is not.
func ownsItsDatabase(t *testing.T, db *sql.DB, f engineFixture) {
	t.Helper()
	if f.driver != DriverMSSQL && f.driver != DriverOracle {
		return
	}
	schema := ""
	if f.driver == DriverOracle {
		schema = f.schema
	}
	tables, err := mustDialect(t, f.driver).Tables(context.Background(), db, schema)
	if err != nil {
		t.Fatalf("listing the tables already there: %v", err)
	}
	mine := map[string]bool{"jd_users": true, "jd_posts": true, "jd_widgets": true}
	var others []string
	for _, table := range tables {
		if !mine[table.Name] {
			others = append(others, table.Name)
		}
	}
	if len(others) > 0 {
		t.Skipf("%s points at a database holding tables this test did not make (%s); a dump round trip replaces every table, so give it a database of its own",
			f.env, strings.Join(others, ", "))
	}
}

// dumpTestDatabase is which database to dump for a fixture. It is the schema
// for the engines where those are the same thing, and the connection string's
// own for the rest.
func dumpTestDatabase(t *testing.T, f engineFixture) string {
	t.Helper()
	switch f.driver {
	case DriverClickHouse, DriverOracle:
		return f.schema
	case DriverSQLite:
		return ""
	default:
		info, err := ParseDSN(f.driver, liveDSN(t, f.env, f.dsn))
		if err != nil {
			t.Fatalf("ParseDSN: %v", err)
		}
		return info.Database
	}
}

func bindOne(f engineFixture) string {
	d, err := DialectFor(f.driver)
	if err != nil {
		return "?"
	}
	return d.Placeholder(1)
}

// deleteAll empties a table. ClickHouse has no bare DELETE — its lightweight
// delete is a filtered one — so the predicate is spelled out for everybody.
func deleteAll(f engineFixture, rel string) string {
	return "DELETE FROM " + rel + " WHERE 1 = 1"
}

// countRows is the assertion the whole round trip comes down to.
func countRows(t *testing.T, db *sql.DB, rel string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+rel).Scan(&n); err != nil {
		t.Fatalf("counting %s: %v", rel, err)
	}
	return n
}

// waitForCount gives an engine whose deletes are asynchronous a moment to
// finish. ClickHouse's DELETE returns before the mutation has been applied, so
// reading straight after it sees the rows that were just deleted — and the test
// would then be asserting that the restore put back rows that had never left.
func waitForCount(t *testing.T, db *sql.DB, rel string, want int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if countRows(t, db, rel) == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s still does not hold %d rows", rel, want)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func redisZ(score float64, member string) redis.Z {
	return redis.Z{Score: score, Member: member}
}
