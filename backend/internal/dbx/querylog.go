package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/logsx"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// The statements a server itself recorded, one by one.
//
// Statements (statements.go) is the week summed up: which shape cost the
// most. This is the other reading, the one a request log gives a website —
// each slow statement as it happened, with when, how long, who ran it and
// against what — so "why was the shop slow at ten past three" has rows to
// read rather than a total to infer from. Every engine keeps it somewhere
// different: MySQL in mysql.slow_log, ClickHouse in system.query_log, Redis in
// SLOWLOG. Postgres and MongoDB write theirs to the server log, which the api
// reads through the log lens rather than here.
//
// Each entry's fp is logsx.Fingerprint of its statement, the same shape the
// server log's lens names, so a statement found here and the same statement
// in the log are one group.

// QueryEntry is one statement the server recorded.
type QueryEntry struct {
	At         time.Time `json:"at"`
	DurationMs float64   `json:"durationMs"`
	Query      string    `json:"query"`
	FP         string    `json:"fp,omitempty"`
	User       string    `json:"user,omitempty"`
	DB         string    `json:"db,omitempty"`
	Client     string    `json:"client,omitempty"`
	Rows       *int64    `json:"rows,omitempty"`
	Examined   *int64    `json:"examined,omitempty"`
	Error      string    `json:"error,omitempty"`
	Code       string    `json:"code,omitempty"`
}

// QueryLogEnable is the setting that keeps the log empty, what it is now, and
// the statement that changes it. The page offers the statement in the query
// console rather than running it: turning logging on is the operator's call
// and the engine's privilege, not a side effect of reading a page.
type QueryLogEnable struct {
	Setting string `json:"setting"`
	Current string `json:"current"`
	SQL     string `json:"sql"`
}

// QueryLog is GET /databases/{id}/querylog.
type QueryLog struct {
	Supported bool   `json:"supported"`
	Reason    string `json:"reason,omitempty"`
	// Source names where the entries were read: the server log, SLOWLOG,
	// system.query_log, mysql.slow_log or performance_schema's history.
	Source string          `json:"source"`
	Enable *QueryLogEnable `json:"enable,omitempty"`
	// Threshold is how slow a statement has to be to be here, in the
	// engine's own words ("250ms", "10 ms"), so an empty list says what it
	// is empty of.
	Threshold string       `json:"threshold,omitempty"`
	Entries   []QueryEntry `json:"entries"`
	Truncated bool         `json:"truncated"`
}

// QueryLogWindow is what a reader of the log asks for.
type QueryLogWindow struct {
	Since time.Time
	Until time.Time
	Limit int
	// MinMs keeps only statements at least this slow.
	MinMs float64
}

// Holds reports whether an entry falls inside the window, for the engines
// whose log is filtered after it is read.
func (w QueryLogWindow) Holds(e QueryEntry) bool {
	if !w.Since.IsZero() && e.At.Before(w.Since) {
		return false
	}
	if !w.Until.IsZero() && e.At.After(w.Until) {
		return false
	}
	return e.DurationMs >= w.MinMs
}

// The query-log sources, as the response names them.
const (
	QuerySourceLog               = "log"
	QuerySourceSlowlog           = "slowlog"
	QuerySourceQueryLog          = "query_log"
	QuerySourceSlowLog           = "slow_log"
	QuerySourceStatementsHistory = "statements_history"
)

// PostgresSlowSetting answers what log_min_duration_statement is, and the
// statement that turns it on when it is off. Postgres writes no statement to
// its log however long it ran until this says how slow is slow, so an empty
// Queries view on a default server is this setting, not a quiet database.
func PostgresSlowSetting(ctx context.Context, db *sql.DB) (current string, enable *QueryLogEnable, err error) {
	var setting, unit string
	if err := db.QueryRowContext(ctx,
		`SELECT setting, COALESCE(unit, '') FROM pg_settings WHERE name = 'log_min_duration_statement'`).
		Scan(&setting, &unit); err != nil {
		return "", nil, err
	}
	if setting == "-1" {
		return "-1", &QueryLogEnable{
			Setting: "log_min_duration_statement",
			Current: "-1",
			SQL:     "ALTER SYSTEM SET log_min_duration_statement = '250ms';\nSELECT pg_reload_conf();",
		}, nil
	}
	return setting + unit, nil, nil
}

// MySQLQueryLog reads MySQL's and MariaDB's slow statements.
//
// The slow log is a table only when log_output says TABLE, and it is off by
// default. With it off, MySQL still keeps each thread's last few statements in
// performance_schema, which is read past long_query_time as a stand-in —
// short, but not nothing. MariaDB has no such history, so the table is all
// there is, and the answer says how to turn it on.
func MySQLQueryLog(ctx context.Context, db *sql.DB, w QueryLogWindow) (*QueryLog, error) {
	var (
		version, output string
		slowOn          int
		longQuery       float64
	)
	if err := db.QueryRowContext(ctx,
		`SELECT VERSION(), @@log_output, @@slow_query_log, @@long_query_time`).
		Scan(&version, &output, &slowOn, &longQuery); err != nil {
		return nil, err
	}
	out := &QueryLog{Supported: true, Entries: []QueryEntry{}, Threshold: thresholdSeconds(longQuery)}
	table := strings.Contains(strings.ToUpper(output), "TABLE")
	if slowOn == 1 && table {
		out.Source = QuerySourceSlowLog
		entries, truncated, err := mysqlSlowLog(ctx, db, w)
		if err != nil {
			out.Reason = "mysql.slow_log could not be read: " + err.Error()
			return out, nil
		}
		out.Entries, out.Truncated = entries, truncated
		return out, nil
	}
	if slowOn != 1 {
		out.Enable = &QueryLogEnable{
			Setting: "slow_query_log",
			Current: "OFF",
			SQL:     "SET GLOBAL slow_query_log = ON;\nSET GLOBAL long_query_time = 0.25;\nSET GLOBAL log_output = 'FILE,TABLE';",
		}
	} else {
		out.Enable = &QueryLogEnable{
			Setting: "log_output",
			Current: output,
			SQL:     "SET GLOBAL log_output = 'FILE,TABLE';",
		}
	}
	if strings.Contains(strings.ToLower(version), "mariadb") {
		out.Source = QuerySourceSlowLog
		out.Reason = "MariaDB keeps its slow statements where this page can read them only in the mysql.slow_log table."
		return out, nil
	}
	out.Source = QuerySourceStatementsHistory
	threshold := math.Max(longQuery*1000, w.MinMs)
	entries, truncated, err := mysqlStatementsHistory(ctx, db, w, threshold)
	if err != nil {
		out.Reason = "performance_schema could not be read: " + err.Error()
		return out, nil
	}
	out.Threshold = thresholdMillis(threshold)
	out.Entries, out.Truncated = entries, truncated
	return out, nil
}

func mysqlSlowLog(ctx context.Context, db *sql.DB, w QueryLogWindow) ([]QueryEntry, bool, error) {
	// The stamp as a number and the duration from its parts: start_time is in
	// the session's zone and query_time is a TIME, and neither survives a
	// driver that was not asked to parse times.
	query := `
	  SELECT UNIX_TIMESTAMP(start_time),
	         (HOUR(query_time) * 3600 + MINUTE(query_time) * 60 + SECOND(query_time)) * 1000
	           + MICROSECOND(query_time) / 1000,
	         user_host, COALESCE(db, ''), rows_sent, rows_examined,
	         CONVERT(LEFT(sql_text, 8192) USING utf8mb4)
	  FROM mysql.slow_log
	  WHERE start_time >= FROM_UNIXTIME(?) AND start_time <= FROM_UNIXTIME(?)
	  ORDER BY start_time DESC LIMIT ?`
	since, until := windowBounds(w)
	rows, err := db.QueryContext(ctx, query, since, until, w.Limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []QueryEntry{}
	for rows.Next() {
		var (
			at, ms         float64
			userHost, text string
			sent, examined int64
			e              QueryEntry
		)
		if err := rows.Scan(&at, &ms, &userHost, &e.DB, &sent, &examined, &text); err != nil {
			return nil, false, err
		}
		if ms < w.MinMs {
			continue
		}
		e.At = unixSeconds(at)
		e.DurationMs = ms
		e.User, e.Client = mysqlUserHost(userHost)
		e.Query = strings.TrimSpace(text)
		e.FP = logsx.Fingerprint(e.Query)
		e.Rows, e.Examined = &sent, &examined
		out = append(out, e)
	}
	return capEntries(out, w.Limit), len(out) > w.Limit, rows.Err()
}

func mysqlStatementsHistory(ctx context.Context, db *sql.DB, w QueryLogWindow, thresholdMs float64) ([]QueryEntry, bool, error) {
	// TIMER_START counts picoseconds from the server's start, so the clock
	// time is the server's start plus it: now less the uptime.
	var uptime float64
	if err := db.QueryRowContext(ctx,
		`SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME = 'Uptime'`).
		Scan(&uptime); err != nil {
		return nil, false, err
	}
	started := time.Now().Add(-time.Duration(uptime * float64(time.Second)))
	rows, err := db.QueryContext(ctx, `
	  SELECT h.TIMER_START / 1e12, h.TIMER_WAIT / 1e9, LEFT(h.SQL_TEXT, 8192),
	         COALESCE(h.CURRENT_SCHEMA, ''), h.ROWS_SENT, h.ROWS_EXAMINED,
	         COALESCE(h.MYSQL_ERRNO, 0), COALESCE(h.MESSAGE_TEXT, ''),
	         COALESCE(t.PROCESSLIST_USER, ''), COALESCE(t.PROCESSLIST_HOST, '')
	  FROM performance_schema.events_statements_history h
	  LEFT JOIN performance_schema.threads t ON t.THREAD_ID = h.THREAD_ID
	  WHERE h.SQL_TEXT IS NOT NULL AND h.TIMER_WAIT >= ? * 1e9
	  ORDER BY h.TIMER_START DESC LIMIT ?`, thresholdMs, w.Limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []QueryEntry{}
	for rows.Next() {
		var (
			offset, ms     float64
			text, message  string
			sent, examined int64
			errno          int64
			e              QueryEntry
		)
		if err := rows.Scan(&offset, &ms, &text, &e.DB, &sent, &examined, &errno, &message, &e.User, &e.Client); err != nil {
			return nil, false, err
		}
		e.At = started.Add(time.Duration(offset * float64(time.Second))).UTC()
		if !w.Holds(QueryEntry{At: e.At, DurationMs: ms}) {
			continue
		}
		e.DurationMs = ms
		e.Query = strings.TrimSpace(text)
		e.FP = logsx.Fingerprint(e.Query)
		e.Rows, e.Examined = &sent, &examined
		if errno != 0 {
			e.Code = strconv.FormatInt(errno, 10)
			e.Error = message
		}
		out = append(out, e)
	}
	return capEntries(out, w.Limit), len(out) > w.Limit, rows.Err()
}

// mysqlUserHost reads slow_log's "app[app] @ localhost [10.0.0.4]": the
// account, and the address when there is one, else the host name.
func mysqlUserHost(s string) (user, client string) {
	who, where, _ := strings.Cut(s, "@")
	who = strings.TrimSpace(who)
	if at := strings.IndexByte(who, '['); at >= 0 {
		who = who[:at]
	}
	user = strings.TrimSpace(who)
	where = strings.TrimSpace(where)
	if open := strings.IndexByte(where, '['); open >= 0 {
		if ip := strings.Trim(strings.TrimSpace(where[open:]), "[]"); ip != "" {
			return user, ip
		}
		where = where[:open]
	}
	return user, strings.TrimSpace(where)
}

// ClickHouseQueryLog reads system.query_log, which ClickHouse keeps on by
// default: every finished or failed query the clients asked for, not the
// ones the server ran on their behalf.
func ClickHouseQueryLog(ctx context.Context, db *sql.DB, w QueryLogWindow) (*QueryLog, error) {
	since, until := windowBounds(w)
	rows, err := db.QueryContext(ctx, `
	  SELECT toUnixTimestamp64Micro(event_time_microseconds), toFloat64(query_duration_ms),
	         substring(query, 1, 8192), lower(hex(normalized_query_hash)), user, current_database,
	         IPv6NumToString(address), toInt64(result_rows), toInt64(read_rows),
	         toInt32(exception_code), substring(exception, 1, 1024)
	  FROM system.query_log
	  WHERE is_initial_query AND type != 'QueryStart'
	    AND event_date >= toDate(toDateTime(?)) AND event_time >= toDateTime(?) AND event_time <= toDateTime(?)
	    AND query_duration_ms >= ?
	  ORDER BY event_time_microseconds DESC LIMIT ?`,
		int64(since), int64(since), int64(math.Ceil(until)), w.MinMs, w.Limit+1)
	if err != nil {
		return &QueryLog{Source: QuerySourceQueryLog, Entries: []QueryEntry{}, Reason: err.Error()}, nil
	}
	defer rows.Close()
	out := &QueryLog{Supported: true, Source: QuerySourceQueryLog, Entries: []QueryEntry{}}
	for rows.Next() {
		var (
			micros        int64
			hash, address string
			result, read  int64
			code          int32
			exception     string
			e             QueryEntry
		)
		if err := rows.Scan(&micros, &e.DurationMs, &e.Query, &hash, &e.User, &e.DB, &address,
			&result, &read, &code, &exception); err != nil {
			return nil, err
		}
		e.At = time.UnixMicro(micros).UTC()
		e.Query = strings.TrimSpace(e.Query)
		e.FP = clickhouseShape(hash)
		e.Client = strings.TrimPrefix(address, "::ffff:")
		e.Rows, e.Examined = &result, &read
		if code != 0 {
			e.Code = strconv.Itoa(int(code))
			e.Error, _, _ = strings.Cut(exception, "\n")
		}
		out.Entries = append(out.Entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out.Truncated = len(out.Entries) > w.Limit
	out.Entries = capEntries(out.Entries, w.Limit)
	return out, nil
}

// clickhouseShape is normalized_query_hash in the twelve hex digits every
// other fp is: ClickHouse has already taken the literals out, better than a
// regular expression over its dialect would.
func clickhouseShape(hash string) string {
	hash = strings.TrimLeft(hash, "0")
	if len(hash) < 16 {
		hash = strings.Repeat("0", 16-len(hash)) + hash
	}
	return hash[:12]
}

// redisSlowlogDepth is how many entries SLOWLOG GET asks for: Redis keeps
// 128 by default (slowlog-max-len), so this is all of them on most servers.
const redisSlowlogDepth = 128

// RedisQueryLog reads SLOWLOG, which Redis keeps on by default for commands
// slower than ten milliseconds.
func RedisQueryLog(ctx context.Context, client *redis.Client, w QueryLogWindow) (*QueryLog, error) {
	out := &QueryLog{Supported: true, Source: QuerySourceSlowlog, Entries: []QueryEntry{}}
	if res, err := client.ConfigGet(ctx, "slowlog-log-slower-than").Result(); err == nil {
		if us, err := strconv.ParseFloat(res["slowlog-log-slower-than"], 64); err == nil {
			if us < 0 {
				out.Reason = "SLOWLOG is switched off on this server (slowlog-log-slower-than is negative)."
			} else {
				out.Threshold = thresholdMillis(us / 1000)
			}
		}
	}
	logs, err := client.SlowLogGet(ctx, redisSlowlogDepth).Result()
	if err != nil {
		return &QueryLog{Source: QuerySourceSlowlog, Entries: []QueryEntry{},
			Reason: "SLOWLOG could not be read: " + err.Error()}, nil
	}
	for _, l := range logs {
		e := redisSlowEntry(l)
		if w.Holds(e) {
			out.Entries = append(out.Entries, e)
		}
	}
	out.Truncated = len(out.Entries) > w.Limit
	out.Entries = capEntries(out.Entries, w.Limit)
	return out, nil
}

// redisSlowEntry turns one SLOWLOG record into a row. Its shape is the
// command and its key and nothing after — "SET session:?" rather than the
// value that was set — which is what groups ten thousand of the same call.
func redisSlowEntry(l redis.SlowLog) QueryEntry {
	e := QueryEntry{
		At:         l.Time.UTC(),
		DurationMs: float64(l.Duration.Microseconds()) / 1000,
		Client:     redisClientIP(l.ClientAddr),
		User:       l.ClientName,
	}
	quoted := make([]string, 0, len(l.Args))
	for _, arg := range l.Args {
		if arg == "" || strings.ContainsAny(arg, " \t\n\"") {
			arg = strconv.Quote(arg)
		}
		quoted = append(quoted, arg)
	}
	e.Query = strings.Join(quoted, " ")
	if len(l.Args) > 0 {
		shape := strings.ToUpper(l.Args[0])
		if len(l.Args) > 1 && !strings.HasPrefix(l.Args[1], "...") {
			shape += " " + l.Args[1]
		}
		e.FP = logsx.Fingerprint(shape)
	}
	return e
}

// redisClientIP is the address of "10.0.0.4:52722", without its port.
func redisClientIP(addr string) string {
	if addr == "" {
		return ""
	}
	if at := strings.LastIndexByte(addr, ':'); at > 0 {
		addr = addr[:at]
	}
	return strings.Trim(addr, "[]")
}

// MongoGlobalLog is the server's recent log as it keeps it in memory, a
// thousand lines or so, for a server on another machine whose log file this
// host cannot read. The lines are the same JSON the file holds.
func MongoGlobalLog(ctx context.Context, client *mongo.Client) ([]string, error) {
	var res struct {
		Log []string `bson:"log"`
	}
	if err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "getLog", Value: "global"}}).Decode(&res); err != nil {
		return nil, err
	}
	return res.Log, nil
}

// windowBounds is the window as Unix seconds, an open end read as "from the
// beginning" or "until now".
func windowBounds(w QueryLogWindow) (since, until float64) {
	if !w.Since.IsZero() {
		since = float64(w.Since.UnixMicro()) / 1e6
	}
	until = float64(time.Now().Add(time.Minute).Unix())
	if !w.Until.IsZero() {
		until = float64(w.Until.UnixMicro()) / 1e6
	}
	return since, until
}

func unixSeconds(s float64) time.Time {
	whole, frac := math.Modf(s)
	return time.Unix(int64(whole), int64(math.Round(frac*1e6))*1000).UTC()
}

func capEntries(entries []QueryEntry, limit int) []QueryEntry {
	if limit > 0 && len(entries) > limit {
		return entries[:limit]
	}
	return entries
}

func thresholdSeconds(s float64) string {
	return thresholdMillis(s * 1000)
}

// thresholdMillis writes a threshold the way the engines' own settings read.
func thresholdMillis(ms float64) string {
	if ms >= 1000 {
		return strconv.FormatFloat(ms/1000, 'f', -1, 64) + " s"
	}
	return strconv.FormatFloat(ms, 'f', -1, 64) + " ms"
}

// QueryLogUnsupported is the answer for an engine that keeps no per-statement
// record this page can read.
func QueryLogUnsupported(driver Driver) *QueryLog {
	reason := fmt.Sprintf("%s keeps no per-statement record this page can read.", driver)
	switch driver {
	case DriverMSSQL:
		reason = "SQL Server keeps its statements in Query Store and Extended Events, which this page does not read."
	case DriverOracle:
		reason = "Oracle keeps its statements in V$SQL and the AWR, which this page does not read."
	case DriverSQLite:
		reason = "A SQLite database is a file, not a server: nothing records its statements but this dashboard."
	}
	return &QueryLog{Reason: reason, Entries: []QueryEntry{}}
}
