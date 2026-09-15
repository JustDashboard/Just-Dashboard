package dbx

import (
	"context"
	"fmt"
	"sort"
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
	Key  string `json:"key"`
	Type string `json:"type"`
	// TTL in seconds; -1 means no expiry, -2 means the key is already gone.
	TTL int64 `json:"ttl"`
	// Size is the length or cardinality — string length, list length, hash
	// field count — which is what tells an operator whether opening it is wise.
	Size int64 `json:"size"`
}

// RedisPage is one turn of the SCAN cursor.
type RedisPage struct {
	Keys []RedisKey `json:"keys"`
	// Cursor is what to send back for the next page; 0 means the scan finished.
	Cursor uint64 `json:"cursor,string"`
	Done   bool   `json:"done"`
}

// RedisZMember is one scored member of a sorted set.
type RedisZMember struct {
	Member string  `json:"member"`
	Score  float64 `json:"score"`
}

// RedisValue is one key's contents, in whichever field matches its type.
type RedisValue struct {
	Key       string            `json:"key"`
	Type      string            `json:"type"`
	TTL       int64             `json:"ttl"`
	String    string            `json:"string,omitempty"`
	List      []string          `json:"list,omitempty"`
	Set       []string          `json:"set,omitempty"`
	Hash      map[string]string `json:"hash,omitempty"`
	ZSet      []RedisZMember    `json:"zset,omitempty"`
	Stream    []map[string]any  `json:"stream,omitempty"`
	Truncated bool              `json:"truncated"`
}

// redisMaxMembers bounds how much of one collection is read. A list with ten
// million entries is a legitimate thing to have and an illegitimate thing to
// send to a browser, so the view is a window and says when it is one.
const redisMaxMembers = 500

// RedisClient dials an instance. Like the Mongo client this is opened per
// request rather than pooled: the driver multiplexes internally and these are
// short administrative reads.
func RedisClient(ctx context.Context, dsn string, db int) (*redis.Client, error) {
	opt, err := redis.ParseURL(dsn)
	if err != nil {
		// A bare host:port is the form people paste out of a compose file, and
		// rejecting it because it lacks a scheme would be pedantry.
		opt = &redis.Options{Addr: strings.TrimPrefix(dsn, "redis://")}
	}
	if db > 0 {
		opt.DB = db
	}
	opt.DialTimeout = 8 * time.Second
	opt.ReadTimeout = 8 * time.Second
	client := redis.NewClient(opt)
	pingCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		client.Close()
		return nil, err
	}
	return client, nil
}

// RedisDatabases reports the numbered logical databases. Redis has a fixed set
// configured at startup rather than a catalogue to query, and reporting how
// many keys each holds is what makes the picker useful rather than a list of
// sixteen identical numbers.
func RedisDatabases(ctx context.Context, client *redis.Client) ([]Database, error) {
	count := 16
	if res, err := client.ConfigGet(ctx, "databases").Result(); err == nil {
		if v, ok := res["databases"]; ok {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				count = n
			}
		}
	}
	// INFO keyspace reports only the databases that actually hold keys, which
	// is where the per-database key counts come from.
	sizes := map[int]int64{}
	if info, err := client.Info(ctx, "keyspace").Result(); err == nil {
		for _, line := range strings.Split(info, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "db") {
				continue
			}
			colon := strings.IndexByte(line, ':')
			if colon < 0 {
				continue
			}
			idx, err := strconv.Atoi(line[2:colon])
			if err != nil {
				continue
			}
			for _, part := range strings.Split(line[colon+1:], ",") {
				if strings.HasPrefix(part, "keys=") {
					if n, err := strconv.ParseInt(part[5:], 10, 64); err == nil {
						sizes[idx] = n
					}
				}
			}
		}
	}
	out := make([]Database, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, Database{Name: strconv.Itoa(i), Size: sizes[i]})
	}
	return out, nil
}

// RedisScan returns one page of keys matching a glob pattern.
//
// The per-key TYPE and TTL calls are pipelined. Issued one at a time they would
// be three round trips per key — 300 for a 100-key page, which over a VPN tunnel
// is the difference between a page that appears and a page that times out.
func RedisScan(ctx context.Context, client *redis.Client, pattern string, cursor uint64, count int) (*RedisPage, error) {
	if pattern == "" {
		pattern = "*"
	}
	if count <= 0 || count > 500 {
		count = 100
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
	keys := []string{}
	next := cursor
	for turn := 0; turn < scanTurns; turn++ {
		batch, cur, err := client.Scan(ctx, next, pattern, int64(count)).Result()
		if err != nil {
			return nil, err
		}
		keys = append(keys, batch...)
		next = cur
		if next == 0 || len(keys) >= count {
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	page := &RedisPage{Keys: make([]RedisKey, 0, len(keys)), Cursor: next, Done: next == 0}
	if len(keys) == 0 {
		return page, nil
	}

	pipe := client.Pipeline()
	types := make([]*redis.StatusCmd, len(keys))
	ttls := make([]*redis.DurationCmd, len(keys))
	for i, k := range keys {
		types[i] = pipe.Type(ctx, k)
		ttls[i] = pipe.TTL(ctx, k)
	}
	// A key expiring between the SCAN and this pipeline is normal, not an
	// error: the commands for it fail individually and are read as best effort.
	_, _ = pipe.Exec(ctx)

	sizePipe := client.Pipeline()
	sizes := make([]*redis.IntCmd, len(keys))
	for i, k := range keys {
		switch types[i].Val() {
		case "string":
			sizes[i] = sizePipe.StrLen(ctx, k)
		case "list":
			sizes[i] = sizePipe.LLen(ctx, k)
		case "set":
			sizes[i] = sizePipe.SCard(ctx, k)
		case "zset":
			sizes[i] = sizePipe.ZCard(ctx, k)
		case "hash":
			sizes[i] = sizePipe.HLen(ctx, k)
		case "stream":
			sizes[i] = sizePipe.XLen(ctx, k)
		}
	}
	_, _ = sizePipe.Exec(ctx)

	for i, k := range keys {
		rk := RedisKey{Key: k, Type: types[i].Val(), TTL: ttlSeconds(ttls[i])}
		if sizes[i] != nil {
			rk.Size = sizes[i].Val()
		}
		page.Keys = append(page.Keys, rk)
	}
	return page, nil
}

// ttlSeconds renders go-redis's TTL result as whole seconds, keeping Redis's
// own -1 (no expiry) and -2 (no such key) sentinels rather than inventing a
// third way to say the same thing.
func ttlSeconds(cmd *redis.DurationCmd) int64 {
	d, err := cmd.Result()
	if err != nil {
		return -2
	}
	switch d {
	case -1 * time.Nanosecond:
		return -1
	case -2 * time.Nanosecond:
		return -2
	}
	if d < 0 {
		return int64(d)
	}
	return int64(d.Seconds())
}

// RedisGet reads one key's value, bounded to redisMaxMembers for collections.
func RedisGet(ctx context.Context, client *redis.Client, key string) (*RedisValue, error) {
	typ, err := client.Type(ctx, key).Result()
	if err != nil {
		return nil, err
	}
	if typ == "none" {
		return nil, fmt.Errorf("key %q does not exist", key)
	}
	v := &RedisValue{Key: key, Type: typ, TTL: ttlSeconds(client.TTL(ctx, key))}
	switch typ {
	case "string":
		v.String, err = client.Get(ctx, key).Result()
	case "list":
		v.List, err = client.LRange(ctx, key, 0, redisMaxMembers-1).Result()
		v.Truncated = len(v.List) >= redisMaxMembers
	case "set":
		v.Set, err = client.SRandMemberN(ctx, key, redisMaxMembers).Result()
		sort.Strings(v.Set)
		v.Truncated = len(v.Set) >= redisMaxMembers
	case "hash":
		v.Hash, err = client.HGetAll(ctx, key).Result()
	case "zset":
		var members []redis.Z
		members, err = client.ZRangeWithScores(ctx, key, 0, redisMaxMembers-1).Result()
		for _, m := range members {
			v.ZSet = append(v.ZSet, RedisZMember{Member: fmt.Sprint(m.Member), Score: m.Score})
		}
		v.Truncated = len(members) >= redisMaxMembers
	case "stream":
		var entries []redis.XMessage
		entries, err = client.XRangeN(ctx, key, "-", "+", redisMaxMembers).Result()
		for _, e := range entries {
			v.Stream = append(v.Stream, map[string]any{"id": e.ID, "values": e.Values})
		}
		v.Truncated = len(entries) >= redisMaxMembers
	default:
		return nil, fmt.Errorf("unsupported Redis type %q", typ)
	}
	if err != nil {
		return nil, err
	}
	return v, nil
}

// RedisSet writes a value.
//
// Every collection type is writable, but each writes the *member* the caller
// named rather than the collection as a whole. That distinction is the whole
// design: a form showing 500 of a list's 10,000 entries must never be able to
// save "the list" — it would silently drop the 9,500 it never showed. So a
// write here always identifies one member, and replacing a whole collection is
// something the operator does deliberately through the Query tab.
func RedisSet(ctx context.Context, client *redis.Client, key, typ, field, value string, ttlSeconds int64) error {
	var ttl time.Duration
	if ttlSeconds > 0 {
		ttl = time.Duration(ttlSeconds) * time.Second
	}
	switch typ {
	case "string", "":
		if err := client.Set(ctx, key, value, ttl).Err(); err != nil {
			return err
		}
		return nil
	case "hash":
		if field == "" {
			return fmt.Errorf("a hash field name is required")
		}
		if err := client.HSet(ctx, key, field, value).Err(); err != nil {
			return err
		}
	case "set":
		if err := client.SAdd(ctx, key, value).Err(); err != nil {
			return err
		}
	case "list":
		// An empty field means append; an index means replace that position,
		// which is the only in-place list edit Redis offers.
		if field == "" {
			if err := client.RPush(ctx, key, value).Err(); err != nil {
				return err
			}
		} else {
			idx, err := strconv.ParseInt(field, 10, 64)
			if err != nil {
				return fmt.Errorf("a list position must be a number")
			}
			if err := client.LSet(ctx, key, idx, value).Err(); err != nil {
				return err
			}
		}
	case "zset":
		score, err := strconv.ParseFloat(field, 64)
		if err != nil {
			return fmt.Errorf("a numeric score is required for a sorted set member")
		}
		if err := client.ZAdd(ctx, key, redis.Z{Score: score, Member: value}).Err(); err != nil {
			return err
		}
	default:
		return fmt.Errorf("editing a %s is not supported from here; use the Query tab", typ)
	}
	// SET carries its own expiry; the other writes do not, so an explicit TTL is
	// applied after the fact rather than silently dropped.
	if ttl > 0 {
		return client.Expire(ctx, key, ttl).Err()
	}
	return nil
}

// RedisDeleteMember removes one member of a collection, leaving the rest.
//
// Lists are the awkward case: Redis has no "delete by index". The documented
// idiom is to write a sentinel into the position and then LREM it, which is
// what this does — and it is done in a MULTI so no other client can observe the
// sentinel as if it were real data.
func RedisDeleteMember(ctx context.Context, client *redis.Client, key, typ, member string) (int64, error) {
	switch typ {
	case "hash":
		return client.HDel(ctx, key, member).Result()
	case "set":
		return client.SRem(ctx, key, member).Result()
	case "zset":
		return client.ZRem(ctx, key, member).Result()
	case "list":
		idx, err := strconv.ParseInt(member, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("a list position must be a number")
		}
		const sentinel = "__jd_deleted__"
		pipe := client.TxPipeline()
		pipe.LSet(ctx, key, idx, sentinel)
		removed := pipe.LRem(ctx, key, 1, sentinel)
		if _, err := pipe.Exec(ctx); err != nil {
			return 0, err
		}
		return removed.Val(), nil
	default:
		return 0, fmt.Errorf("a %s has no members to remove individually", typ)
	}
}

// RedisCreateKey makes a new key of the requested type with one initial member,
// which is the only way to create a collection: Redis has no empty collections,
// and a key with no members does not exist.
func RedisCreateKey(ctx context.Context, client *redis.Client, key, typ, field, value string, ttl int64) error {
	exists, err := client.Exists(ctx, key).Result()
	if err != nil {
		return err
	}
	if exists > 0 {
		return fmt.Errorf("key %q already exists", key)
	}
	return RedisSet(ctx, client, key, typ, field, value, ttl)
}

// RedisRename moves a key. NX so an existing destination is refused rather than
// silently overwritten, which is what plain RENAME would do.
func RedisRename(ctx context.Context, client *redis.Client, from, to string) error {
	ok, err := client.RenameNX(ctx, from, to).Result()
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("a key named %q already exists", to)
	}
	return nil
}

// RedisDelete removes whole keys.
func RedisDelete(ctx context.Context, client *redis.Client, keys ...string) (int64, error) {
	return client.Del(ctx, keys...).Result()
}

// RedisExpire sets or clears a key's TTL. A ttl of zero or less persists the
// key, matching what PERSIST does, rather than deleting it immediately — which
// is what EXPIRE with a non-positive value would otherwise do and is never what
// somebody clearing a TTL in a form meant.
func RedisExpire(ctx context.Context, client *redis.Client, key string, ttl int64) error {
	if ttl <= 0 {
		return client.Persist(ctx, key).Err()
	}
	return client.Expire(ctx, key, time.Duration(ttl)*time.Second).Err()
}

// RedisInfo returns the server statistics an operator watches, parsed out of
// the INFO text block into the same shape the other engines' stats use.
func RedisInfo(ctx context.Context, client *redis.Client) (map[string]any, error) {
	raw, err := client.Info(ctx, "server", "clients", "memory", "stats", "keyspace").Result()
	if err != nil {
		return nil, err
	}
	// Only the fields worth a row on screen are kept; INFO is a few hundred
	// lines and most of it is internal counters.
	wanted := map[string]bool{
		"redis_version": true, "uptime_in_seconds": true, "connected_clients": true,
		"used_memory_human": true, "used_memory_peak_human": true, "maxmemory_human": true,
		"total_commands_processed": true, "instantaneous_ops_per_sec": true,
		"keyspace_hits": true, "keyspace_misses": true, "evicted_keys": true,
		"expired_keys": true, "rejected_connections": true,
	}
	out := map[string]any{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		key := line[:colon]
		if wanted[key] {
			out[key] = line[colon+1:]
		}
	}
	return out, nil
}
