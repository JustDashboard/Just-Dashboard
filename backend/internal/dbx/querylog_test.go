package dbx

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/logsx"
	"github.com/redis/go-redis/v9"
)

func TestMySQLUserHostReadsTheAccountAndTheAddress(t *testing.T) {
	for _, c := range []struct{ in, user, client string }{
		{"app[app] @ localhost [10.0.0.4]", "app", "10.0.0.4"},
		{"root[root] @ localhost []", "root", "localhost"},
		{"app[app] @ web-1.internal [172.18.0.5]", "app", "172.18.0.5"},
		{"[event_scheduler] @ localhost []", "", "localhost"},
		{"", "", ""},
	} {
		user, client := mysqlUserHost(c.in)
		if user != c.user || client != c.client {
			t.Errorf("%q = %q, %q; want %q, %q", c.in, user, client, c.user, c.client)
		}
	}
}

// A SLOWLOG record's shape is its command and key, not the value that was
// written: ten thousand SETs of one session key are one row of shapes.
func TestRedisSlowEntryIsTheCommandAndItsKey(t *testing.T) {
	at := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	e := redisSlowEntry(redis.SlowLog{
		ID: 14, Time: at, Duration: 12_345 * time.Microsecond,
		Args:       []string{"SET", "session:9031", "a value with spaces"},
		ClientAddr: "172.18.0.5:52722", ClientName: "worker",
	})
	if e.Query != `SET session:9031 "a value with spaces"` {
		t.Errorf("query = %q", e.Query)
	}
	if e.DurationMs != 12.345 || !e.At.Equal(at) || e.Client != "172.18.0.5" || e.User != "worker" {
		t.Errorf("entry = %+v", e)
	}
	other := redisSlowEntry(redis.SlowLog{Args: []string{"set", "session:17", "other"}})
	if e.FP == "" || e.FP != other.FP || e.FP != logsx.Fingerprint("SET session:?") {
		t.Errorf("two SETs of one key shape should share an fp: %q %q", e.FP, other.FP)
	}
	keys := redisSlowEntry(redis.SlowLog{Args: []string{"KEYS", "*"}})
	if keys.FP == e.FP {
		t.Error("a different command is a different shape")
	}
	if got := redisClientIP("[::1]:6379"); got != "::1" {
		t.Errorf("v6 client = %q", got)
	}
}

func TestClickHouseShapeIsTwelveDigitsOfTheHash(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"8f3a2b1c0d9e8f7a", "8f3a2b1c0d9e"},
		// ClickHouse's hex() drops the leading zero bytes of a number.
		{"3a2b1c0d9e8f7a", "003a2b1c0d9e"},
		{"0", "000000000000"},
	} {
		if got := clickhouseShape(c.in); got != c.want {
			t.Errorf("%q = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestQueryLogWindowHoldsItsBoundsAndItsFloor(t *testing.T) {
	at := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	w := QueryLogWindow{Since: at.Add(-time.Hour), Until: at.Add(time.Hour), MinMs: 100}
	for _, c := range []struct {
		e    QueryEntry
		want bool
	}{
		{QueryEntry{At: at, DurationMs: 150}, true},
		{QueryEntry{At: at, DurationMs: 99}, false},
		{QueryEntry{At: at.Add(-2 * time.Hour), DurationMs: 500}, false},
		{QueryEntry{At: at.Add(2 * time.Hour), DurationMs: 500}, false},
	} {
		if got := w.Holds(c.e); got != c.want {
			t.Errorf("%+v held = %v", c.e, got)
		}
	}
}

func TestThresholdsReadLikeTheSettings(t *testing.T) {
	if got := thresholdSeconds(10); got != "10 s" {
		t.Errorf("long_query_time 10 = %q", got)
	}
	if got := thresholdSeconds(0.25); got != "250 ms" {
		t.Errorf("long_query_time 0.25 = %q", got)
	}
	if got := thresholdMillis(10); got != "10 ms" {
		t.Errorf("slowlog 10000 µs = %q", got)
	}
}

// Against a real Postgres only when one is named: the setting is read, never
// changed, and a server nobody pointed this at is not dialled.
func TestLivePostgresSlowSetting(t *testing.T) {
	if os.Getenv("JD_TEST_POSTGRES_DSN") == "" {
		t.Skip("set JD_TEST_POSTGRES_DSN to read log_min_duration_statement from a real server")
	}
	db := liveSQL(t, DriverPostgres, "JD_TEST_POSTGRES_DSN", "")
	current, enable, err := PostgresSlowSetting(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if enable != nil {
		if current != "-1" || enable.Setting != "log_min_duration_statement" || !strings.Contains(enable.SQL, "pg_reload_conf") {
			t.Errorf("off = %q %+v", current, enable)
		}
	} else if current == "" || current == "-1" {
		t.Errorf("on = %q", current)
	}
}
