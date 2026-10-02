package dbx

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Redis is the one engine here with no tables, no rows and no SQL, so it shares
// none of the dialect machinery. What it does share is the shape of the
// question an operator asks — "show me what is in here, and let me change one
// thing" — so this file answers that in the vocabulary Redis actually has:
// keys, types and TTLs.
//
// The one rule that carries over unchanged is the reason SCAN is used and KEYS
// is not. KEYS walks the entire keyspace in a single blocking call; on a
// production instance with a few million keys that is a multi-second stall of
// every other client. SCAN is cursor-based and bounded, which is why paging
// here is a cursor the client hands back rather than an offset.

// RedisKey is one key as the browser lists it.
type RedisKey struct {
	Key  RedisBytes `json:"key"`
	Type string     `json:"type"`
	// TTL in seconds; -1 means no expiry, -2 means the key is already gone.
	TTL int64 `json:"ttl"`
	// Size is the length or cardinality — string length, list length, hash
	// field count — which is what tells an operator whether opening it is wise.
	Size int64 `json:"size"`
	// Memory is what the key costs in bytes, by the server's own estimate. It
	// is a command per key, so it is present only when the listing asked for
	// it, and absent on a server with no MEMORY USAGE.
	Memory *int64 `json:"memory,omitempty"`
}

// RedisPage is one turn of the SCAN cursor.
type RedisPage struct {
	Keys []RedisKey `json:"keys"`
	// Cursor is what to send back for the next page; 0 means the scan finished.
	Cursor uint64 `json:"cursor,string"`
	Done   bool   `json:"done"`
	// DB is the logical database that was scanned, which is not always the
	// one that was asked for: no ?db= means the connection string's own.
	DB int `json:"db"`
	// Total is every key in that database, so a page can say "100 of 2.6
	// million" instead of leaving the operator to guess how far a scan has to go.
	Total int64 `json:"total"`
}

// RedisZMember is one scored member of a sorted set.
type RedisZMember struct {
	Member RedisBytes `json:"member"`
	Score  RedisScore `json:"score"`
}

// RedisValue is one key's contents, in whichever field matches its type.
//
// It is the first window of the key and nothing more: RedisMembers is the
// read that pages. This one stays because it is the shape the key routes have
// always answered with.
type RedisValue struct {
	Key       RedisBytes            `json:"key"`
	Type      string                `json:"type"`
	TTL       int64                 `json:"ttl"`
	String    RedisBytes            `json:"string,omitempty"`
	List      []RedisBytes          `json:"list,omitempty"`
	Set       []RedisBytes          `json:"set,omitempty"`
	Hash      map[string]RedisBytes `json:"hash,omitempty"`
	ZSet      []RedisZMember        `json:"zset,omitempty"`
	Stream    []map[string]any      `json:"stream,omitempty"`
	Truncated bool                  `json:"truncated"`
	// Length is the whole key's size — bytes for a string, members otherwise —
	// so a truncated window can say what it is a window of.
	Length int64 `json:"length"`
}

// redisMaxMembers bounds how much of one collection is read. A list with ten
// million entries is a legitimate thing to have and an illegitimate thing to
// send to a browser, so the view is a window and says when it is one.
const redisMaxMembers = 500

// redisMaxStringBytes bounds one read of a string value for the same reason.
// A string may be half a gigabyte.
const redisMaxStringBytes = 1 << 20

// RedisDSNDatabase asks for the logical database the connection string names,
// as opposed to any particular number. It is not zero because zero is a
// database: with "0 means unset" a connection string ending in /3 made
// database 0 unreachable, and what the picker called db0 was db3.
const RedisDSNDatabase = -1

// RedisOpenOptions is how a client is opened.
type RedisOpenOptions struct {
	// DB is a logical database number, or RedisDSNDatabase.
	DB int
	// ReadTimeout bounds one command's reply. Zero is the eight seconds an
	// administrative read gets.
	ReadTimeout time.Duration
	// Retry lets the driver send a command again when its reply did not
	// arrive. That is right for a dump walking a keyspace, where every
	// command can be repeated, and wrong for an operator's edit: a command
	// whose reply was lost may well have run, and a second RPUSH or XADD is a
	// second element.
	Retry bool
	// Quiet asks the server not to count what this client reads as use of a
	// key. Redis keeps one clock per key — when it was last read or written —
	// and evicts by it; measuring a key's size or opening it from the
	// dashboard reset that clock, so a key idle for a week read "idle for 0
	// seconds" the moment anybody looked at it, and a nightly dump made every
	// key on the server its most recently used. It is for a client that only
	// looks. An operator's own edit is use, and is counted as such.
	Quiet bool
}

// RedisClient dials an instance. Like the Mongo client this is opened per
// request rather than pooled: the driver multiplexes internally and these are
// short administrative reads.
//
// db is always honoured: every connection the client opens selects it, so the
// answer does not depend on which pooled connection a command happens to get.
// Pass RedisDSNDatabase for the one the connection string names.
func RedisClient(ctx context.Context, dsn string, db int) (*redis.Client, error) {
	return RedisOpen(ctx, dsn, RedisOpenOptions{DB: db, Retry: true})
}

// RedisOpen is RedisClient with a say in the timeouts and the retries.
func RedisOpen(ctx context.Context, dsn string, o RedisOpenOptions) (*redis.Client, error) {
	opt, err := redisOptions(dsn)
	if err != nil {
		return nil, err
	}
	if o.DB >= 0 {
		opt.DB = o.DB
	}
	opt.DialTimeout = 8 * time.Second
	opt.ReadTimeout = 8 * time.Second
	if o.ReadTimeout > 0 {
		opt.ReadTimeout = o.ReadTimeout
	}
	// One attempt to connect. A server that refuses is down, and four more
	// tries at it turn every request into a two-second wait to be told so.
	opt.DialerRetries = 1
	if !o.Retry {
		opt.MaxRetries = -1
	}
	if o.Quiet {
		opt.OnConnect = redisQuietConnection
	}
	client := redis.NewClient(opt)
	pingCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		client.Close()
		if redisRefusedDatabase(err) {
			return nil, &RedisDatabaseError{DB: opt.DB, Reason: err.Error()}
		}
		return nil, err
	}
	return client, nil
}

// redisQuietConnection turns CLIENT NO-TOUCH on for one connection of a quiet
// client, as each is opened. Redis has it from 7.2 and Valkey from its first
// release; a server that has never heard of it answers with an error, which
// is not this connection's failure: its reads count as they always did, and
// the server's profile (Features.NoTouch) is what says so to a page.
func redisQuietConnection(ctx context.Context, conn *redis.Conn) error {
	_ = conn.Do(ctx, "client", "no-touch", "on").Err()
	return nil
}

// RedisDatabaseError is a logical database the server would not select: a
// number past the ones it has, or any number but zero on a cluster node,
// which has one. It is told apart from a server that cannot be reached
// because the fault is in what was asked for, and the server is fine.
type RedisDatabaseError struct {
	DB     int
	Reason string
}

func (e *RedisDatabaseError) Error() string {
	return fmt.Sprintf("the server would not select database %d: %s", e.DB, e.Reason)
}

// redisRefusedDatabase recognises the server's answer to a SELECT it will
// not perform. A new connection selects its database before anything else,
// so the refusal arrives as the error of whatever was sent first.
func redisRefusedDatabase(err error) bool {
	var replied redis.Error
	if !errors.As(err, &replied) {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "db index") || strings.Contains(msg, "select is not allowed")
}

// redisOptions reads a connection string. The error is the parser's own
// sentence without the string it was given, which holds the password.
func redisOptions(dsn string) (*redis.Options, error) {
	opt, err := redis.ParseURL(dsn)
	if err == nil {
		return opt, nil
	}
	if strings.Contains(dsn, "://") {
		// go-redis's own complaints ("redis: invalid database number") name
		// the fault and nothing else. The URL parser's quote the whole string.
		if reason, ok := strings.CutPrefix(err.Error(), "redis: "); ok {
			return nil, fmt.Errorf("the connection string is not a usable Redis URL: %s", reason)
		}
		return nil, fmt.Errorf("the connection string is not a Redis URL")
	}
	// A bare host:port is the form people paste out of a compose file, and
	// rejecting it because it lacks a scheme would be pedantry.
	return &redis.Options{Addr: dsn}, nil
}

// RedisDatabases reports the numbered logical databases. Redis has a fixed set
// configured at startup rather than a catalogue to query, and reporting how
// many keys each holds is what makes the picker useful rather than a list of
// sixteen identical numbers.
func RedisDatabases(ctx context.Context, client *redis.Client) ([]Database, error) {
	pipe := client.Pipeline()
	config := redisConfigGet(ctx, pipe, "databases")
	keyspace := pipe.Info(ctx, "keyspace")
	cluster := pipe.Info(ctx, "cluster")
	server := pipe.Info(ctx, "server")
	_, _ = pipe.Exec(ctx)

	// A sentinel has no keyspace to number.
	if raw, err := server.Result(); err == nil &&
		redisInfoMap(parseRedisInfo(raw))["redis_mode"] == "sentinel" {
		return []Database{}, nil
	}

	count := 16
	if v, ok := redisConfigValue(config, "databases"); ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			count = n
		}
	}
	// A cluster node has one keyspace whatever its configuration says, and
	// refuses SELECT for any other.
	if raw, err := cluster.Result(); err == nil &&
		redisInfoMap(parseRedisInfo(raw))["cluster_enabled"] == "1" {
		count = 1
	}
	// INFO keyspace reports only the databases that actually hold keys, which
	// is where the per-database key counts come from.
	sizes := map[int]int64{}
	if raw, err := keyspace.Result(); err == nil {
		for _, ks := range parseRedisKeyspace(parseRedisInfo(raw)) {
			sizes[ks.DB] = ks.Keys
		}
	}
	out := make([]Database, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, Database{Name: strconv.Itoa(i), Size: sizes[i]})
	}
	return out, nil
}

// RedisScanOptions is one listing request.
type RedisScanOptions struct {
	// Pattern is a glob; empty means every key.
	Pattern string
	Cursor  uint64
	// Count is the page size asked for, clamped to redisMaxScanCount.
	Count int
	// Type keeps only keys of one type — "hash", "stream", or a module's own
	// name as TYPE reports it.
	Type string
	// Memory adds what each key costs. It is one more command per key.
	Memory bool
}

const (
	redisMaxScanCount = 1000
	// redisScanBudget is how long one listing request keeps looking before it
	// hands back the cursor it reached.
	redisScanBudget = 5 * time.Second
)

var redisTypeRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)

// RedisScanKeys returns one page of keys, optionally of one type.
//
// The per-key TYPE and TTL calls are pipelined. Issued one at a time they would
// be three round trips per key — 300 for a 100-key page, which over a VPN tunnel
// is the difference between a page that appears and a page that times out.
//
// profile may be nil when neither the type filter nor the memory column is
// asked for; they are the two things that depend on what the server is.
func RedisScanKeys(ctx context.Context, client *redis.Client, profile *RedisProfile, o RedisScanOptions) (*RedisPage, error) {
	if o.Pattern == "" {
		o.Pattern = "*"
	}
	if o.Count <= 0 {
		o.Count = 100
	}
	if o.Count > redisMaxScanCount {
		o.Count = redisMaxScanCount
	}
	if o.Type != "" && !redisTypeRe.MatchString(o.Type) {
		return nil, fmt.Errorf("%q is not a Redis type name", o.Type)
	}
	if (o.Type != "" || o.Memory) && profile == nil {
		var err error
		if profile, err = RedisProbe(ctx, client); err != nil {
			return nil, err
		}
	}
	if profile != nil && profile.Mode == "sentinel" {
		return nil, ErrRedisSentinel
	}
	// The server filters by type where it can. Where it cannot — SCAN … TYPE
	// arrived in Redis 6 — every key is fetched and the filter is applied to
	// the TYPE replies below, which costs more round trips and gives the same
	// page.
	serverType := ""
	if o.Type != "" && profile.Features.ScanType {
		serverType = o.Type
	}

	// SCAN is asked repeatedly rather than once, because an empty turn does not
	// mean an empty result. COUNT is a hint about how many slots to *examine*,
	// not how many to return, so a selective pattern routinely matches nothing
	// in the first turn and everything in the ninth: `user:*` against a
	// keyspace of three hundred sessions answered "No keys match this pattern"
	// while user:1 sat in it. Returning that page verbatim made the pattern box
	// — the whole point of scanning server-side — report an empty keyspace and
	// leave the operator to click Next blindly.
	//
	// The loop is bounded in both directions that matter: it stops as soon as
	// it has a page's worth, and it gives up after scanTurns turns whatever
	// happens, handing back the cursor it reached. A pattern matching nothing
	// at all therefore costs a bounded amount of work per request and the
	// client can carry on from where this left off, which is what Done and
	// Cursor are for.
	const scanTurns = 50
	page := &RedisPage{Keys: []RedisKey{}, DB: client.Options().DB}
	keep := func(names []string) error {
		keys, err := redisDescribeKeys(ctx, client, profile, names, o.Memory)
		if err != nil {
			return err
		}
		for _, k := range keys {
			if o.Type == "" || k.Type == o.Type {
				page.Keys = append(page.Keys, k)
			}
		}
		return nil
	}
	// A pattern with nothing in it to match is a name, and a name is looked
	// up, not searched for: one command instead of a walk of the keyspace to
	// find the one key that could ever match.
	if !strings.ContainsAny(o.Pattern, `*?[\`) {
		if err := keep([]string{o.Pattern}); err != nil {
			return nil, err
		}
		page.Done = true
		page.Total, _ = client.DBSize(ctx).Result()
		return page, nil
	}
	started := time.Now()
	next, hint := o.Cursor, int64(o.Count)
	for turn := 0; turn < scanTurns; turn++ {
		var (
			batch []string
			cur   uint64
			err   error
		)
		if serverType != "" {
			batch, cur, err = client.ScanType(ctx, next, o.Pattern, hint, serverType).Result()
		} else {
			batch, cur, err = client.Scan(ctx, next, o.Pattern, hint).Result()
		}
		if err != nil {
			return nil, RedisExplainError(ctx, client, err)
		}
		next = cur
		if err := keep(batch); err != nil {
			return nil, err
		}
		if next == 0 || len(page.Keys) >= o.Count || time.Since(started) > redisScanBudget {
			break
		}
		if len(page.Keys) == 0 {
			// Nothing yet, so the pattern is selective: each further turn
			// looks at more of the keyspace, or finding one key in a million
			// takes the operator two hundred clicks of "more".
			hint = redisScanBatch
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	page.Cursor, page.Done = next, next == 0
	page.Total, _ = client.DBSize(ctx).Result()
	return page, nil
}

// redisDescribeKeys fills in type, TTL and size for a batch of key names.
//
// A key expiring between the SCAN and these pipelines is normal, not an
// error: the commands for it fail individually and are read as best effort,
// and a key that is gone by the time it is described is left out.
func redisDescribeKeys(ctx context.Context, client *redis.Client, profile *RedisProfile, names []string, memory bool) ([]RedisKey, error) {
	if len(names) == 0 {
		return nil, nil
	}
	memory = memory && profile != nil && profile.Features.MemoryUsage
	pipe := client.Pipeline()
	types := make([]*redis.StatusCmd, len(names))
	ttls := make([]*redis.IntCmd, len(names))
	usage := make([]*redis.IntCmd, len(names))
	for i, k := range names {
		types[i] = pipe.Type(ctx, k)
		ttls[i] = redisTTLCmd(ctx, pipe, "ttl", k)
		if memory {
			usage[i] = pipe.MemoryUsage(ctx, k)
		}
	}
	if _, err := pipe.Exec(ctx); err != nil && types[0].Err() != nil && types[0].Err() != redis.Nil {
		// Every command failing the same way is the connection, not a key.
		return nil, RedisExplainError(ctx, client, types[0].Err())
	}

	sizePipe := client.Pipeline()
	sizes := make([]*redis.IntCmd, len(names))
	for i, k := range names {
		sizes[i] = redisLengthCmd(ctx, sizePipe, types[i].Val(), k)
	}
	_, _ = sizePipe.Exec(ctx)

	out := make([]RedisKey, 0, len(names))
	for i, k := range names {
		typ := types[i].Val()
		if typ == "none" || typ == "" {
			continue
		}
		rk := RedisKey{Key: RedisBytes(k), Type: typ, TTL: redisTTL(ttls[i])}
		if sizes[i] != nil {
			rk.Size = sizes[i].Val()
		}
		if usage[i] != nil && usage[i].Err() == nil {
			n := usage[i].Val()
			rk.Memory = &n
		}
		out = append(out, rk)
	}
	return out, nil
}

// redisLengthCmd queues the command that measures a key of the given type, or
// nothing for a type with no such command — a module's own.
func redisLengthCmd(ctx context.Context, pipe redis.Pipeliner, typ, key string) *redis.IntCmd {
	switch typ {
	case "string":
		return pipe.StrLen(ctx, key)
	case "list":
		return pipe.LLen(ctx, key)
	case "set":
		return pipe.SCard(ctx, key)
	case "zset":
		return pipe.ZCard(ctx, key)
	case "hash":
		return pipe.HLen(ctx, key)
	case "stream":
		return pipe.XLen(ctx, key)
	}
	return nil
}

// redisTTLCmd queues TTL or PTTL and keeps the number the server answers with.
//
// The driver's own TTL and PTTL hand back a time.Duration, which holds 292
// years. An expiry written for the year 9999 — a common way of saying "never"
// — does not fit, wraps to a negative number, and a negative TTL reads as a
// key that is already gone.
func redisTTLCmd(ctx context.Context, pipe redis.Pipeliner, verb, key string) *redis.IntCmd {
	cmd := redis.NewIntCmd(ctx, verb, key)
	_ = pipe.Process(ctx, cmd)
	return cmd
}

// redisTTL reads a TTL or PTTL reply in the server's own unit, keeping its
// own -1 (no expiry) and -2 (no such key) rather than inventing a third way
// to say the same thing. A reply that could not be read is a key that is not
// there.
func redisTTL(cmd *redis.IntCmd) int64 {
	n, err := cmd.Result()
	if err != nil {
		return -2
	}
	return n
}

// RedisGet reads the first window of one key's value: redisMaxMembers of a
// collection, redisMaxStringBytes of a string. Truncated says when the key
// holds more than that, for every type.
func RedisGet(ctx context.Context, client *redis.Client, key string) (*RedisValue, error) {
	page, err := RedisReadMembers(ctx, client, nil, RedisMembersOptions{
		Key: RedisBytes(key), Count: redisMaxMembers, legacy: true,
	})
	if err != nil {
		return nil, err
	}
	// This shape has fields for the six built-in types and nothing else. A
	// JSON document, like a module's own type, is read page by page instead.
	if page.Unsupported != "" || page.JSON != nil {
		return nil, fmt.Errorf("unsupported Redis type %q", page.Type)
	}
	v := &RedisValue{Key: page.Key, Type: page.Type, TTL: page.TTL, Length: page.Length, Truncated: !page.Done}
	for _, r := range page.Rows {
		// This shape has nowhere to say which member was cut short, only that
		// what it carries is not all there is.
		v.Truncated = v.Truncated || r.Truncated
	}
	switch page.Type {
	case "string":
		v.String = page.String.Value
	case "list":
		for _, r := range page.Rows {
			v.List = append(v.List, *r.Value)
		}
	case "set":
		for _, r := range page.Rows {
			v.Set = append(v.Set, *r.Value)
		}
	case "hash":
		v.Hash = map[string]RedisBytes{}
		for _, r := range page.Rows {
			v.Hash[string(*r.Field)] = *r.Value
		}
	case "zset":
		for _, r := range page.Rows {
			v.ZSet = append(v.ZSet, RedisZMember{Member: *r.Value, Score: *r.Score})
		}
	case "stream":
		for _, r := range page.Rows {
			values := map[string]any{}
			for _, f := range r.Fields {
				values[string(f[0])] = f[1]
			}
			v.Stream = append(v.Stream, map[string]any{"id": r.ID, "values": values})
		}
	}
	return v, nil
}

// RedisInfo returns the server statistics an operator watches, parsed out of
// the INFO text block into the same shape the other engines' stats use.
//
// The thirteen fields it has always carried are still there as the text the
// server printed. Beside them are what a chart needs and text cannot give:
// the counters as numbers, under "counters", and the moment they were read,
// so two polls make a rate.
func RedisInfo(ctx context.Context, client *redis.Client) (map[string]any, error) {
	// No section names: INFO with several of them is Redis 7, and the default
	// set holds every field read here on every version.
	raw, err := client.Info(ctx).Result()
	sampled := time.Now()
	if err != nil {
		return nil, err
	}
	sections := parseRedisInfo(raw)
	info := redisInfoMap(sections)
	// Only the fields worth a row on screen are kept; INFO is a few hundred
	// lines and most of it is internal counters.
	out := map[string]any{}
	for _, key := range []string{
		"redis_version", "uptime_in_seconds", "connected_clients",
		"used_memory_human", "used_memory_peak_human", "maxmemory_human",
		"total_commands_processed", "instantaneous_ops_per_sec",
		"keyspace_hits", "keyspace_misses", "evicted_keys",
		"expired_keys", "rejected_connections",
	} {
		if v, ok := info[key]; ok {
			out[key] = v
		}
	}
	counters := map[string]any{}
	for _, key := range redisCounterFields {
		if v, ok := info[key]; ok {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				counters[key] = n
			} else if f, err := strconv.ParseFloat(v, 64); err == nil {
				counters[key] = f
			}
		}
	}
	var keys, expires int64
	for _, ks := range parseRedisKeyspace(sections) {
		keys += ks.Keys
		expires += ks.Expires
	}
	counters["keys"], counters["expires"] = keys, expires
	out["counters"] = counters
	flavor, version := RedisFlavorFromInfo(info)
	out["flavor"], out["version"] = flavor, version
	out["role"] = redisRole(info["role"])
	out["mode"] = info["redis_mode"]
	switch out["mode"] {
	case "":
		out["mode"] = "standalone"
	case "sentinel":
		// A sentinel replicates nothing and prints no role of its own.
		out["role"] = "sentinel"
	}
	out["sampledAtMs"] = sampled.UnixMilli()
	return out, nil
}

// redisCounterFields are the INFO fields a chart is drawn from: monotonic
// counters whose difference between two polls is a rate, and gauges read as
// they are.
var redisCounterFields = []string{
	"uptime_in_seconds", "connected_clients", "blocked_clients",
	"used_memory", "used_memory_rss", "used_memory_peak", "maxmemory",
	"mem_fragmentation_ratio",
	"total_connections_received", "total_commands_processed",
	"instantaneous_ops_per_sec", "total_net_input_bytes", "total_net_output_bytes",
	"instantaneous_input_kbps", "instantaneous_output_kbps",
	"rejected_connections", "expired_keys", "evicted_keys",
	"keyspace_hits", "keyspace_misses", "pubsub_channels", "pubsub_patterns",
	"connected_slaves", "used_cpu_sys", "used_cpu_user",
	"rdb_changes_since_last_save", "rdb_last_save_time",
}
