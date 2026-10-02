package dbx

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
)

// Dropping a database, against real servers.
//
// Every engine is a different verb — DROP DATABASE, DROP USER, unlink a file,
// FLUSHDB — and three of them refuse to do it from a session that is inside the
// database being removed. None of that shows up in a unit test: the statement
// is well-formed in all six cases and the server is the only thing that knows
// it will not run.
//
// Each test creates its own database first. Where the fixture's login lacks the
// privilege to create one the test skips saying so, rather than dropping
// something it did not make.

const dropTestDB = "jd_droptest"

func TestLiveDropDatabase(t *testing.T) {
	for _, f := range sqlFixtures() {
		t.Run(string(f.driver), func(t *testing.T) {
			ctx := context.Background()
			dsn := liveDSN(t, f.env, f.dsn)

			if f.driver == DriverSQLite {
				// SQLite's database is a file, so "create" is opening one and
				// the drop is unlinking it.
				path := filepath.Join(t.TempDir(), "drop-me.db")
				db := liveSQL(t, f.driver, "JD_TEST_SQLITE_DROP_DSN", path)
				if _, err := db.ExecContext(ctx, `CREATE TABLE t (id INTEGER)`); err != nil {
					t.Fatalf("seed: %v", err)
				}
				db.Close()
				res, err := DropDatabase(ctx, DriverSQLite, path, "")
				if err != nil {
					t.Fatalf("DropDatabase: %v", err)
				}
				if !res.Gone {
					t.Error("a removed file should report Gone")
				}
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Errorf("file still there: %v", err)
				}
				return
			}

			// Creating and dropping a database is a server-wide privilege, and
			// the fixture logins deliberately do not have it — jdtest is
			// granted its own database and nothing else, which is what a sane
			// application account looks like. So this one test uses the
			// administrative login where the engine needs one, and skips
			// saying so when it cannot reach it.
			env, adminDSN := dropAdminDSN(f)
			dsn = liveDSN(t, env, adminDSN)
			db := liveSQL(t, f.driver, env, adminDSN)
			d := mustDialect(t, f.driver)
			q, err := d.QuoteIdent(dropTestDB)
			if err != nil {
				t.Fatal(err)
			}
			create := "CREATE DATABASE " + q
			if f.driver == DriverOracle {
				// Oracle's databases are users; making one needs privileges the
				// application login does not have.
				create = `CREATE USER ` + q + ` IDENTIFIED BY jdDrop1234`
			}
			if _, err := db.ExecContext(ctx, create); err != nil {
				t.Skipf("cannot create a database to drop as this login (%v)", err)
			}
			t.Cleanup(func() {
				stmts, _ := d.DropDatabaseSQL(dropTestDB)
				for _, s := range stmts {
					_, _ = db.ExecContext(context.Background(), s.SQL, s.Args...)
				}
			})

			// Connect to it, so the drop has to deal with a session inside the
			// database — which is the case Postgres and SQL Server refuse and
			// the reason AdminDatabase exists.
			inside, err := openForDump(ctx, d, dsnForDatabase(f.driver, dsn, dropTestDB))
			if err == nil {
				defer inside.Close()
				_, _ = inside.ExecContext(ctx, "SELECT 1")
			}

			if _, err := DropDatabase(ctx, f.driver, dsn, dropTestDB); err != nil {
				t.Fatalf("DropDatabase: %v", err)
			}
			if databaseExists(t, db, f.driver, dropTestDB) {
				t.Errorf("%s is still listed after being dropped", dropTestDB)
			}
		})
	}
}

func TestLiveDropMongoDatabase(t *testing.T) {
	client, _ := liveMongo(t)
	ctx := context.Background()
	const name = "jd_droptest"
	if _, err := client.Database(name).Collection("c").
		InsertOne(ctx, bson.D{{Key: "k", Value: 1}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() { _ = client.Database(name).Drop(context.Background()) })

	dsn := os.Getenv("JD_TEST_MONGO_DSN")
	if dsn == "" {
		dsn = "mongodb://127.0.0.1:27017/jdtest"
	}
	res, err := DropDatabase(ctx, DriverMongo, dsn, name)
	if err != nil {
		t.Fatalf("DropDatabase: %v", err)
	}
	if !res.Gone {
		t.Error("a dropped Mongo database should report Gone")
	}
	names, err := client.ListDatabaseNames(ctx, bson.D{})
	if err != nil {
		t.Fatalf("ListDatabaseNames: %v", err)
	}
	for _, n := range names {
		if n == name {
			t.Errorf("%s is still listed", name)
		}
	}
}

// Redis is the engine where "delete this database" cannot mean what it means
// everywhere else: the keyspaces are configured at startup and exist whether or
// not anything is in them. The result has to say so rather than report a
// removal that did not happen.
func TestLiveDropRedisEmptiesRatherThanRemoves(t *testing.T) {
	// liveRedis skips when the variable is unset: there is no default address,
	// least of all for a test that empties what it is pointed at.
	client := liveRedis(t)
	ctx := context.Background()
	dsn := os.Getenv("JD_TEST_REDIS_DSN")
	// db9, not the one the other tests use: this empties whatever it points at.
	if err := client.Do(ctx, "SELECT", 9).Err(); err != nil {
		t.Fatalf("select: %v", err)
	}
	if err := client.Set(ctx, "jd:drop:key", "v", 0).Err(); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res, err := DropDatabase(ctx, DriverRedis, dsn, "db9")
	if err != nil {
		t.Fatalf("DropDatabase: %v", err)
	}
	if res.Gone {
		t.Error("Redis reported the keyspace gone; it can only be emptied")
	}
	if !strings.Contains(res.Detail, "emptied") {
		t.Errorf("detail does not say what happened: %q", res.Detail)
	}
	if err := client.Do(ctx, "SELECT", 9).Err(); err != nil {
		t.Fatalf("select: %v", err)
	}
	if n, err := client.DBSize(ctx).Result(); err != nil || n != 0 {
		t.Errorf("db9 holds %d keys after the flush (%v)", n, err)
	}
}

// dropAdminDSN is the login that may create and drop databases on this engine,
// and the variable to override it with. Postgres, SQL Server and ClickHouse
// already connect as an account that can; MySQL and Oracle do not.
func dropAdminDSN(f engineFixture) (string, string) {
	switch f.driver {
	case DriverMySQL:
		return "JD_TEST_MYSQL_ADMIN_DSN", "root:rootpw@tcp(127.0.0.1:3306)/"
	case DriverOracle:
		return "JD_TEST_ORACLE_ADMIN_DSN", "oracle://system:JdTest2024@127.0.0.1:1521/FREEPDB1"
	default:
		return f.env, f.dsn
	}
}

// databaseExists asks the engine's own catalogue, which is what the picker in
// the UI reads — a drop that left the name listed would look like it failed.
func databaseExists(t *testing.T, db *sql.DB, driver Driver, name string) bool {
	t.Helper()
	dbs, err := ListDatabases(context.Background(), db, driver)
	if err != nil {
		t.Fatalf("ListDatabases: %v", err)
	}
	for _, d := range dbs {
		if strings.EqualFold(d.Name, name) {
			return true
		}
	}
	return false
}
