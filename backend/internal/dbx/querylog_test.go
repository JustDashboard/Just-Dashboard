package dbx

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
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

// fakeRedis answers the handful of commands the query log sends, the way a
// server with SLOWLOG on or refused would: enough of RESP to test the reading
// end to end without a Redis on the machine running the tests.
func fakeRedis(t *testing.T, slowlog string) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				r := bufio.NewReader(conn)
				for {
					args, err := readRESP(r)
					if err != nil {
						return
					}
					reply := "-ERR unknown command\r\n"
					switch strings.ToUpper(args[0]) {
					case "PING":
						reply = "+PONG\r\n"
					case "CONFIG":
						reply = "*2\r\n$23\r\nslowlog-log-slower-than\r\n$5\r\n10000\r\n"
					case "SLOWLOG":
						reply = slowlog
					}
					if _, err := conn.Write([]byte(reply)); err != nil {
						return
					}
				}
			}()
		}
	}()
	return l.Addr().String()
}

func readRESP(r *bufio.Reader) ([]string, error) {
	head, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(head, "*")))
	if err != nil || n < 1 {
		return nil, fmt.Errorf("not a command: %q", head)
	}
	args := make([]string, n)
	for i := range args {
		if _, err := r.ReadString('\n'); err != nil {
			return nil, err
		}
		arg, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		args[i] = strings.TrimSuffix(arg, "\r\n")
	}
	return args, nil
}

// SLOWLOG as a server answers it: the entries in the window, the threshold
// in milliseconds, and a refusal handed back as the error it is — the page
// says the server could not be read, not that Redis keeps no such log.
func TestRedisQueryLogReadsSlowlog(t *testing.T) {
	at := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	entry := func(id int, when time.Time, key string) string {
		return fmt.Sprintf("*6\r\n:%d\r\n:%d\r\n:15000\r\n*2\r\n$4\r\nHGET\r\n$%d\r\n%s\r\n$15\r\n172.18.0.5:5272\r\n$6\r\nworker\r\n",
			id, when.Unix(), len(key), key)
	}
	addr := fakeRedis(t, "*2\r\n"+entry(2, at, "cart:77")+entry(1, at.Add(-3*time.Hour), "cart:12"))
	client := redis.NewClient(&redis.Options{Addr: addr, Protocol: 2})
	t.Cleanup(func() { _ = client.Close() })
	got, err := RedisQueryLog(context.Background(), client, QueryLogWindow{Since: at.Add(-time.Hour), Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Supported || got.Source != QuerySourceSlowlog || got.Threshold != "10 ms" {
		t.Errorf("query log = %+v", got)
	}
	if len(got.Entries) != 1 || got.Entries[0].Query != "HGET cart:77" || got.Entries[0].DurationMs != 15 || got.Entries[0].User != "worker" {
		t.Errorf("only the entry inside the window: %+v", got.Entries)
	}

	refused := redis.NewClient(&redis.Options{Addr: fakeRedis(t, "-ERR unknown command 'SLOWLOG'\r\n"), Protocol: 2})
	t.Cleanup(func() { _ = refused.Close() })
	if out, err := RedisQueryLog(context.Background(), refused, QueryLogWindow{Limit: 10}); err == nil || out != nil {
		t.Errorf("a refused SLOWLOG = %+v, %v", out, err)
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
