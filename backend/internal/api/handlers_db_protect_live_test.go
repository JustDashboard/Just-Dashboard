package api

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
)

// A protected connection against real servers, page by page: what only reads
// still answers, what would change something answers 409, and afterwards the
// server holds exactly what it held before. The routes here are the ones each
// engine's page uses — the grid's review of its staged edits, a schema form's
// preview, a result as a file, the Redis console, MongoDB's reads — so a page
// that stops working on a protected connection shows up here and not in front
// of an operator.
func TestLiveProtectedConnectionKeepsItsReadingPages(t *testing.T) {
	protect := func(t *testing.T, s *Server, id int64) {
		t.Helper()
		if _, err := s.Store.DB.Exec(`UPDATE db_connections SET read_only = 1 WHERE id = ?`, id); err != nil {
			t.Fatal(err)
		}
	}
	type call struct{ method, path, body string }
	served := func(t *testing.T, router http.Handler, base string, calls ...call) {
		t.Helper()
		for _, c := range calls {
			rec := do(t, router, c.method, base+c.path, c.body)
			if rec.Code != http.StatusOK {
				t.Errorf("%s %s %s on a protected connection = %d %s, want 200", c.method, c.path, c.body, rec.Code, strings.TrimSpace(rec.Body.String()))
			}
		}
	}
	refused := func(t *testing.T, router http.Handler, base string, calls ...call) {
		t.Helper()
		for _, c := range calls {
			rec := do(t, router, c.method, base+c.path, c.body)
			if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "connection_read_only") {
				t.Errorf("%s %s %s on a protected connection = %d %s, want 409 connection_read_only", c.method, c.path, c.body, rec.Code, strings.TrimSpace(rec.Body.String()))
			}
		}
	}

	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("JD_TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("JD_TEST_POSTGRES_DSN unset")
		}
		s, router, id := liveAPIRouter(t, dbx.DriverPostgres, dsn)
		pool, _, err := s.dbPool(context.Background(), id)
		if err != nil {
			t.Skip(err)
		}
		const table = "jd_protect_pages"
		exec := func(statement string) {
			t.Helper()
			if _, err := pool.ExecContext(context.Background(), statement); err != nil {
				t.Fatal(err)
			}
		}
		exec(`DROP TABLE IF EXISTS ` + table)
		exec(`CREATE TABLE ` + table + ` (id int PRIMARY KEY, n int NOT NULL)`)
		exec(`INSERT INTO ` + table + ` VALUES (1, 10), (2, 20)`)
		t.Cleanup(func() { _, _ = pool.ExecContext(context.Background(), `DROP TABLE IF EXISTS `+table) })
		protect(t, s, id)
		base := pathf("/databases/%d", id)

		change := `{"schema":"public","table":"` + table + `","changes":[{"op":"update","key":{"id":1},"values":{"n":11}},{"op":"delete","key":{"id":2}}]`
		column := `{"schema":"public","table":"` + table + `","column":{"name":"added","type":"text"}}`
		served(t, router, base,
			call{http.MethodPost, "/changes", change + `,"dryRun":true}`},
			call{http.MethodPost, "/ddl/column?preview=1", column},
			call{http.MethodDelete, "/ddl/table?preview=1", `{"schema":"public","table":"` + table + `"}`},
			call{http.MethodPost, "/export/query", `{"sql":"SELECT id, n FROM ` + table + ` ORDER BY id","format":"csv"}`},
			call{http.MethodPost, "/script", `{"script":"SELECT 1; SELECT count(*) FROM ` + table + `"}`},
			call{http.MethodPost, "/explain", `{"query":"DELETE FROM ` + table + `"}`},
			call{http.MethodPost, "/explain", `{"query":"SELECT * FROM ` + table + `","analyze":true}`},
			call{http.MethodPost, "/classify", `{"query":"DELETE FROM ` + table + `"}`},
			call{http.MethodPost, "/orm", `{"target":"typescript","schema":"public"}`},
		)
		refused(t, router, base,
			call{http.MethodPost, "/changes", change + `}`},
			call{http.MethodPost, "/ddl/column", column},
			call{http.MethodDelete, "/ddl/table", `{"schema":"public","table":"` + table + `"}`},
			call{http.MethodPost, "/ddl/truncate", `{"schema":"public","table":"` + table + `"}`},
			call{http.MethodPost, "/export/query", `{"sql":"DELETE FROM ` + table + ` RETURNING id","format":"csv"}`},
			call{http.MethodPost, "/script", `{"script":"SELECT 1; UPDATE ` + table + ` SET n = 0"}`},
			call{http.MethodPost, "/explain", `{"query":"DELETE FROM ` + table + `","analyze":true}`},
			call{http.MethodPost, "/maintenance", `{"action":"vacuum_full","schema":"public","table":"` + table + `"}`},
			call{http.MethodPost, "/rows", `{"schema":"public","table":"` + table + `","values":{"id":3,"n":30}}`},
			call{http.MethodPost, "/import", `{"schema":"public","table":"` + table + `","rows":[{"id":4,"n":40}]}`},
			call{http.MethodPost, "/restore", `{"file":"nothing.sql"}`},
			call{http.MethodPost, "/copy", `{"name":"jd_protect_copy"}`},
		)

		var rows, total, columns int
		if err := pool.QueryRowContext(context.Background(), `SELECT count(*), coalesce(sum(n), 0) FROM `+table).Scan(&rows, &total); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRowContext(context.Background(),
			`SELECT count(*) FROM information_schema.columns WHERE table_schema = 'public' AND table_name = $1`, table).Scan(&columns); err != nil {
			t.Fatal(err)
		}
		if rows != 2 || total != 30 || columns != 2 {
			t.Errorf("the protected table changed: %d rows summing to %d in %d columns, want 2, 30, 2", rows, total, columns)
		}
	})

	t.Run("redis", func(t *testing.T) {
		dsn := os.Getenv("JD_TEST_REDIS_DSN")
		if dsn == "" {
			t.Skip("JD_TEST_REDIS_DSN unset")
		}
		s, router, id := liveAPIRouter(t, dbx.DriverRedis, dsn)
		ctx := context.Background()
		client, err := dbx.RedisClient(ctx, dsn, dbx.RedisDSNDatabase)
		if err != nil {
			t.Skip(err)
		}
		defer client.Close()
		const kept, made = "jd_protect_pages:kept", "jd_protect_pages:made"
		if err := client.Set(ctx, kept, "before", 0).Err(); err != nil {
			t.Skip(err)
		}
		t.Cleanup(func() { client.Del(context.Background(), kept, made) })
		protect(t, s, id)
		base := pathf("/databases/%d", id)

		served(t, router, base,
			call{http.MethodPost, "/redis/command", `{"command":"GET ` + kept + `"}`},
			call{http.MethodPost, "/redis/command", `{"command":"TTL ` + kept + `"}`},
			call{http.MethodPost, "/redis/classify", `{"command":"FLUSHALL"}`},
			call{http.MethodPost, "/keys/bulk", `{"pattern":"jd_protect_pages:*","action":"delete","dryRun":true}`},
		)
		refused(t, router, base,
			call{http.MethodPost, "/redis/command", `{"command":"SET ` + made + ` x"}`},
			call{http.MethodPost, "/redis/command", `{"command":"DEL ` + kept + `"}`},
			call{http.MethodPost, "/redis/command", `{"command":"GET ` + kept + `","Command":"DEL ` + kept + `"}`},
			call{http.MethodPost, "/keys/bulk", `{"pattern":"jd_protect_pages:*","action":"delete"}`},
			call{http.MethodPost, "/keys/value", `{"key":"` + made + `","type":"string","value":"x"}`},
			call{http.MethodDelete, "/keys", `{"keys":["` + kept + `"]}`},
			call{http.MethodPut, "/redis/config", `{"name":"maxmemory-policy","value":"noeviction"}`},
		)
		if got, _ := client.Get(ctx, kept).Result(); got != "before" {
			t.Errorf("the protected key holds %q, want what it held before", got)
		}
		if n, _ := client.Exists(ctx, made).Result(); n != 0 {
			t.Error("a key was made on a protected connection")
		}
	})

	t.Run("mongodb", func(t *testing.T) {
		dsn := os.Getenv("JD_TEST_MONGO_DSN")
		if dsn == "" {
			t.Skip("JD_TEST_MONGO_DSN unset")
		}
		s, router, id := liveAPIRouter(t, dbx.DriverMongo, dsn)
		info, err := dbx.ParseDSN(dbx.DriverMongo, dsn)
		if err != nil || info.Database == "" {
			t.Skipf("the fixture DSN names no database: %v", err)
		}
		ctx := context.Background()
		client, err := dbx.MongoClient(ctx, dsn)
		if err != nil {
			t.Skip(err)
		}
		defer client.Disconnect(ctx)
		const coll = "jd_protect_pages"
		drop := func() { _ = dbx.MongoDropCollection(ctx, client, info.Database, coll) }
		drop()
		defer drop()
		base := pathf("/databases/%d", id)
		if rec := do(t, router, http.MethodPost, base+"/mongo/documents",
			`{"collection":"`+coll+`","documents":"[{ n: 1 }, { n: 2 }]"}`); rec.Code != http.StatusOK {
			t.Fatalf("seeding the collection = %d %s", rec.Code, rec.Body.String())
		}
		protect(t, s, id)

		served(t, router, base,
			call{http.MethodPost, "/mongo/find", `{"collection":"` + coll + `","filter":"{ n: { $gte: 1 } }"}`},
			call{http.MethodPost, "/mongo/count", `{"collection":"` + coll + `","filter":"{}"}`},
			call{http.MethodPost, "/mongo/schema", `{"collection":"` + coll + `"}`},
			call{http.MethodPost, "/mongo/explain", `{"collection":"` + coll + `","filter":"{ n: 1 }"}`},
			call{http.MethodPost, "/mongo/aggregate/preview", `{"collection":"` + coll + `","pipeline":"[{ $match: { n: 1 } }]"}`},
			call{http.MethodPost, "/aggregate", `{"collection":"` + coll + `","pipeline":"[ { $match: { n: 1 } }, { $limit: 5 } ]"}`},
			call{http.MethodPost, "/mongo/command", `{"command":"{ count: '` + coll + `' }"}`},
			call{http.MethodPost, "/mongo/command/classify", `{"command":"{ drop: '` + coll + `' }"}`},
			call{http.MethodPatch, "/mongo/documents", `{"collection":"` + coll + `","filter":"{ n: 1 }","update":"{ $set: { n: 9 } }","many":true,"dryRun":true}`},
			call{http.MethodDelete, "/mongo/documents", `{"collection":"` + coll + `","filter":"{ n: 1 }","many":true,"dryRun":true}`},
		)
		refused(t, router, base,
			call{http.MethodPost, "/mongo/documents", `{"collection":"` + coll + `","documents":"[{ n: 3 }]"}`},
			call{http.MethodPatch, "/mongo/documents", `{"collection":"` + coll + `","filter":"{ n: 1 }","update":"{ $set: { n: 9 } }","many":true}`},
			call{http.MethodDelete, "/mongo/documents", `{"collection":"` + coll + `","filter":"{ n: 1 }","many":true}`},
			call{http.MethodPost, "/mongo/command", `{"command":"{ delete: '` + coll + `', deletes: [ { q: {}, limit: 0 } ] }"}`},
			call{http.MethodPost, "/aggregate", `{"collection":"` + coll + `","pipeline":"[ { $out: 'jd_protect_pages_out' } ]"}`},
			call{http.MethodDelete, "/mongo/collections", `{"collection":"` + coll + `"}`},
			call{http.MethodPost, "/mongo/indexes", `{"collection":"` + coll + `","keys":"{ n: 1 }"}`},
		)
		counted, err := dbx.MongoCountDocuments(ctx, client, info.Database, coll, dbx.MongoFindSpec{})
		if err != nil {
			t.Fatal(err)
		}
		if counted.Value != 2 {
			t.Errorf("the protected collection holds %d documents, want the 2 it held before", counted.Value)
		}
	})
}
