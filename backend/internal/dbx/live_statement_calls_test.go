package dbx

import (
	"context"
	"fmt"
	"os"
	"testing"
)

// Why the classifier has to call these something other than a read, on a real
// PostgreSQL: each runs inside the read-only transaction a read is given, and
// what it did is still done after the rollback.
func TestLivePostgresReadScopeDoesNotStopTheseCalls(t *testing.T) {
	const env = "JD_TEST_POSTGRES_DSN"
	if os.Getenv(env) == "" {
		t.Skipf("set %s to run this", env)
	}
	db := liveSQL(t, DriverPostgres, env, "")
	watch := liveSQL(t, DriverPostgres, env, "")
	ctx := context.Background()
	inScope := func(t *testing.T, query string) {
		t.Helper()
		if got := ClassifyFor(DriverPostgres, query); !got.Destructive {
			t.Errorf("ClassifyFor(%q) = %q, want destructive", query, got.Level)
		}
		if _, err := RunStatement(ctx, db, DriverPostgres, misread(query), 5); err != nil {
			t.Fatalf("%q inside the read-only scope: %v", query, err)
		}
	}

	t.Run("a function name put together inside text that is run", func(t *testing.T) {
		victim, err := watch.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer victim.Close()
		var pid int
		if err := victim.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
			t.Fatal(err)
		}
		inScope(t, fmt.Sprintf(`SELECT query_to_xml('select pg_terminate_'||'backend(%d)', true, false, '')`, pid))
		if err := victim.QueryRowContext(ctx, `SELECT 1`).Scan(new(int)); err == nil {
			t.Error("the session named in the text is still there, so this test proves nothing")
		}
	})

	t.Run("an advisory lock outlives the rollback", func(t *testing.T) {
		// "jdcg", so as not to meet anybody else's lock.
		const key = 0x6a646367
		held := func() int {
			var n int
			if err := watch.QueryRowContext(ctx,
				`SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND classid = 0 AND objid = $1`, key).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}
		inScope(t, fmt.Sprintf(`SELECT pg_advisory_lock(%d)`, key))
		if held() != 1 {
			t.Error("no lock is held after the rollback, so this test proves nothing")
		}
		// The pooled connection holding it ends, and the lock with it.
		db.SetMaxIdleConns(0)
		if n := held(); n != 0 {
			t.Errorf("%d advisory locks left after the connection closed", n)
		}
	})
}
