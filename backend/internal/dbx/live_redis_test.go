package dbx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// Live tests for the Redis layer, against a real server.
//
// Three environment variables, three kinds of server:
//
//   - JD_TEST_REDIS_DSN is the shared fixture. Everything here that only
//     touches keys runs against it, inside the logical database its
//     connection string names and under one key prefix.
//   - JD_TEST_REDIS_ADMIN_DSN is a server this run owns outright. The tests
//     that change the server itself — its configuration, its users, its slow
//     log, its clients — need one, and skip without it rather than do that to
//     a server somebody else is using.
//   - JD_TEST_REDIS_FLAVORS lists other products behind the same protocol, as
//     name=dsn pairs separated by commas: valkey, keydb, dragonfly.
//
// None of them has a default. An address nobody chose is not one to write to.

const redisTestPrefix = "jdb4:"

func redisTestClient(t *testing.T, env string) (*redis.Client, string) {
	t.Helper()
	dsn := os.Getenv(env)
	if dsn == "" {
		t.Skipf("set %s to run this", env)
	}
	client, err := RedisClient(context.Background(), dsn, RedisDSNDatabase)
	if err != nil {
		t.Skipf("Redis unreachable at %s (%v)", env, err)
	}
	t.Cleanup(func() { client.Close() })
	clean := func() {
		ctx := context.Background()
		var cursor uint64
		for {
			keys, next, err := client.Scan(ctx, cursor, redisGlobEscape(redisTestPrefix)+"*", 1000).Result()
			if err != nil {
				return
			}
			if len(keys) > 0 {
				client.Del(ctx, keys...)
			}
			if cursor = next; cursor == 0 {
				return
			}
		}
	}
	clean()
	t.Cleanup(clean)
	return client, dsn
}

func rb(s string) *RedisBytes {
	v := RedisBytes(s)
	return &v
}

func mustWrite(t *testing.T, client *redis.Client, w RedisWrite) *RedisWriteResult {
	t.Helper()
	res, err := RedisWriteValue(context.Background(), client, nil, w)
	if err != nil {
		t.Fatalf("write %s %q: %v", w.Type, w.Key, err)
	}
	return res
}

// allMembers walks a key to its end and returns every row, failing if a page
// ever claims to continue without a cursor to continue with.
func allMembers(t *testing.T, client *redis.Client, o RedisMembersOptions) []RedisRow {
	t.Helper()
	var rows []RedisRow
	for page := 0; page < 1000; page++ {
		got, err := RedisReadMembers(context.Background(), client, nil, o)
		if err != nil {
			t.Fatalf("read %q: %v", o.Key, err)
		}
		rows = append(rows, got.Rows...)
		if got.Done {
			return rows
		}
		if got.Cursor == "" || got.Cursor == o.Cursor {
			t.Fatalf("page %d of %q is not done and gave cursor %q after %q", page, o.Key, got.Cursor, o.Cursor)
		}
		o.Cursor = got.Cursor
	}
	t.Fatalf("%q never finished paging", o.Key)
	return nil
}

func TestLiveRedisStringKeepsItsExpiry(t *testing.T) {
	client, _ := redisTestClient(t, "JD_TEST_REDIS_DSN")
	ctx := context.Background()
	key := RedisBytes(redisTestPrefix + "session")

	ttl := int64(300)
	mustWrite(t, client, RedisWrite{Key: key, Value: rb("v1"), TTL: &ttl})
	// Saving a new value says nothing about the expiry, so the expiry stays.
	// A plain SET here is what made a hand-edited session immortal.
	mustWrite(t, client, RedisWrite{Key: key, Value: rb("v2")})
	if got := client.TTL(ctx, string(key)).Val(); got <= 0 || got > 300*time.Second {
		t.Fatalf("ttl after a save with no ttl = %v, want it kept", got)
	}
	zero := int64(0)
	mustWrite(t, client, RedisWrite{Key: key, Value: rb("v3"), TTL: &zero})
	if got := client.TTL(ctx, string(key)).Val(); got <= 0 {
		t.Fatalf("ttl after a save with ttl 0 = %v, want it kept", got)
	}
	shorter := int64(60)
	mustWrite(t, client, RedisWrite{Key: key, Value: rb("v4"), TTL: &shorter})
	if got := client.TTL(ctx, string(key)).Val(); got <= 0 || got > 60*time.Second {
		t.Fatalf("ttl after a save with ttl 60 = %v", got)
	}
	never := int64(-1)
	mustWrite(t, client, RedisWrite{Key: key, Value: rb("v5"), TTL: &never})
	if got := client.TTL(ctx, string(key)).Val(); got != -1 {
		t.Fatalf("ttl after a save with ttl -1 = %v, want none", got)
	}
	if got := client.Get(ctx, string(key)).Val(); got != "v5" {
		t.Errorf("value = %q", got)
	}
}

func TestLiveRedisWriteGuardsTypeAndExistence(t *testing.T) {
	client, _ := redisTestClient(t, "JD_TEST_REDIS_DSN")
	ctx := context.Background()
	key := RedisBytes(redisTestPrefix + "guarded")
	mustWrite(t, client, RedisWrite{Key: key, Type: "hash", Field: rb("a"), Value: rb("1")})

	// A string written to a key that holds a hash would replace the hash.
	if _, err := RedisWriteValue(ctx, client, nil, RedisWrite{Key: key, Value: rb("oops")}); err == nil {
		t.Fatal("a string was written over a hash")
	}
	if got := client.Type(ctx, string(key)).Val(); got != "hash" {
		t.Fatalf("the key is now a %s", got)
	}
	// A create on a key that is there is refused with the error a page can
	// tell apart.
	_, err := RedisWriteValue(ctx, client, nil, RedisWrite{Key: key, Type: "hash", Field: rb("b"), Value: rb("2"), Create: true})
	var exists *RedisKeyExistsError
	if !errors.As(err, &exists) {
		t.Fatalf("create over an existing key: %v", err)
	}
	if client.HExists(ctx, string(key), "b").Val() {
		t.Error("the refused create wrote its field anyway")
	}

	// Every type can be created, the stream included.
	for typ, w := range map[string]RedisWrite{
		"string": {Value: rb("v")},
		"hash":   {Field: rb("f"), Value: rb("v")},
		"list":   {Value: rb("v")},
		"set":    {Value: rb("v")},
		"zset":   {Value: rb("v"), Field: rb("1.5")},
		"stream": {Entries: [][2]RedisBytes{{"f", "v"}}},
	} {
		w.Key, w.Type, w.Create = RedisBytes(redisTestPrefix+"new:"+typ), typ, true
		ttl := int64(120)
		w.TTL = &ttl
		res := mustWrite(t, client, w)
		if got := client.Type(ctx, string(w.Key)).Val(); got != typ {
			t.Errorf("created %s is a %s", typ, got)
		}
		if got := client.TTL(ctx, string(w.Key)).Val(); got <= 0 {
			t.Errorf("created %s has ttl %v, want the one given", typ, got)
		}
		if typ == "stream" && !redisStreamIDRe.MatchString(res.ID) {
			t.Errorf("a new stream entry came back with id %q", res.ID)
		}
	}
}

// Redis strings are bytes. Every one of these went out as U+FFFD before, and
// came back in as different bytes on the next save.
func TestLiveRedisBinaryRoundTrip(t *testing.T) {
	client, _ := redisTestClient(t, "JD_TEST_REDIS_DSN")
	ctx := context.Background()
	raw := "\xff\x00\xfe\x80binary\xc3"
	p := redisTestPrefix + "bin:"

	mustWrite(t, client, RedisWrite{Key: RedisBytes(p + "s"), Value: rb(raw)})
	mustWrite(t, client, RedisWrite{Key: RedisBytes(p + "h"), Type: "hash", Field: rb(raw), Value: rb(raw)})
	mustWrite(t, client, RedisWrite{Key: RedisBytes(p + "l"), Type: "list", Value: rb(raw)})
	mustWrite(t, client, RedisWrite{Key: RedisBytes(p + "set"), Type: "set", Value: rb(raw)})
	score := RedisScore(1)
	mustWrite(t, client, RedisWrite{Key: RedisBytes(p + "z"), Type: "zset", Value: rb(raw), Score: &score})
	mustWrite(t, client, RedisWrite{Key: RedisBytes(p + "x"), Type: "stream", Entries: [][2]RedisBytes{{RedisBytes(raw), RedisBytes(raw)}}})
	// And a key whose own name is not text.
	mustWrite(t, client, RedisWrite{Key: RedisBytes(p + raw), Value: rb("named in bytes")})

	str, err := RedisReadMembers(ctx, client, nil, RedisMembersOptions{Key: RedisBytes(p + "s")})
	if err != nil || string(str.String.Value) != raw {
		t.Errorf("string = %+v, %v", str.String, err)
	}
	if rows := allMembers(t, client, RedisMembersOptions{Key: RedisBytes(p + "h")}); len(rows) != 1 || string(*rows[0].Field) != raw || string(*rows[0].Value) != raw {
		t.Errorf("hash rows = %+v", rows)
	}
	for _, name := range []string{"l", "set", "z"} {
		if rows := allMembers(t, client, RedisMembersOptions{Key: RedisBytes(p + name)}); len(rows) != 1 || string(*rows[0].Value) != raw {
			t.Errorf("%s rows = %+v", name, rows)
		}
	}
	if rows := allMembers(t, client, RedisMembersOptions{Key: RedisBytes(p + "x")}); len(rows) != 1 || string(rows[0].Fields[0][0]) != raw || string(rows[0].Fields[0][1]) != raw {
		t.Errorf("stream rows = %+v", rows)
	}
	page, err := RedisScanKeys(ctx, client, nil, RedisScanOptions{Pattern: redisGlobEscape(p) + "*", Count: 100})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, k := range page.Keys {
		if string(k.Key) == p+raw {
			found = true
		}
	}
	if !found {
		t.Errorf("the key named in bytes is not in the listing: %+v", page.Keys)
	}
	if n, err := RedisRemoveMembers(ctx, client, RedisRemoval{Key: RedisBytes(p + "set"), Members: []RedisBytes{RedisBytes(raw)}}); err != nil || n != 1 {
		t.Errorf("removing a binary set member: %d, %v", n, err)
	}
}

// The empty string is a member Redis stores. Naming it must remove it — and
// only it. Read as "no member named", the same request deleted the whole key.
func TestLiveRedisEmptyMemberIsAMember(t *testing.T) {
	client, _ := redisTestClient(t, "JD_TEST_REDIS_DSN")
	ctx := context.Background()
	hash, set := redisTestPrefix+"empty:h", redisTestPrefix+"empty:s"
	client.HSet(ctx, hash, "", "nameless", "kept", "v")
	client.SAdd(ctx, set, "", "kept")

	for key, typ := range map[string]string{hash: "hash", set: "set"} {
		n, err := RedisRemoveMembers(ctx, client, RedisRemoval{Key: RedisBytes(key), Type: typ, Members: []RedisBytes{""}})
		if err != nil || n != 1 {
			t.Fatalf("removing the empty member of %s: %d, %v", key, n, err)
		}
		if client.Exists(ctx, key).Val() != 1 {
			t.Fatalf("removing the empty member of %s deleted the key", key)
		}
	}
	if client.HExists(ctx, hash, "").Val() || !client.HExists(ctx, hash, "kept").Val() {
		t.Error("the hash lost the wrong field")
	}
	if client.SIsMember(ctx, set, "").Val() || !client.SIsMember(ctx, set, "kept").Val() {
		t.Error("the set lost the wrong member")
	}
	// The type is looked up when the request does not carry it.
	if n, err := RedisRemoveMembers(ctx, client, RedisRemoval{Key: RedisBytes(hash), Members: []RedisBytes{"kept"}}); err != nil || n != 1 {
		t.Errorf("removing with no type given: %d, %v", n, err)
	}
	if _, err := RedisRemoveMembers(ctx, client, RedisRemoval{Key: RedisBytes(set)}); err == nil {
		t.Error("a removal that names nothing was accepted")
	}
}

func TestLiveRedisListEdits(t *testing.T) {
	client, _ := redisTestClient(t, "JD_TEST_REDIS_DSN")
	ctx := context.Background()
	key := RedisBytes(redisTestPrefix + "queue")
	list := func() []string { return client.LRange(ctx, string(key), 0, -1).Val() }
	want := func(step string, items ...string) {
		t.Helper()
		if got := list(); strings.Join(got, ",") != strings.Join(items, ",") {
			t.Fatalf("after %s the list is %v, want %v", step, got, items)
		}
	}
	idx := func(n int64) *int64 { return &n }

	for _, v := range []string{"a", "c"} {
		mustWrite(t, client, RedisWrite{Key: key, Type: "list", Value: rb(v)})
	}
	want("two appends", "a", "c")
	mustWrite(t, client, RedisWrite{Key: key, Type: "list", Value: rb("first"), Head: true})
	want("a push onto the head", "first", "a", "c")

	// An insert at a position moves what was there along; it does not
	// replace it, which is what "Add" with a position used to do.
	mustWrite(t, client, RedisWrite{Key: key, Type: "list", Value: rb("b"), Index: idx(2), Insert: true})
	want("an insert before position 2", "first", "a", "b", "c")
	mustWrite(t, client, RedisWrite{Key: key, Type: "list", Value: rb("zero"), Index: idx(0), Insert: true})
	want("an insert at the head", "zero", "first", "a", "b", "c")
	mustWrite(t, client, RedisWrite{Key: key, Type: "list", Value: rb("end"), Index: idx(5), Insert: true})
	want("an insert past the last element", "zero", "first", "a", "b", "c", "end")
	// A list that already holds duplicates of everything involved.
	mustWrite(t, client, RedisWrite{Key: key, Type: "list", Value: rb("a"), Index: idx(3), Insert: true})
	want("inserting a duplicate", "zero", "first", "a", "a", "b", "c", "end")

	mustWrite(t, client, RedisWrite{Key: key, Type: "list", Value: rb("B"), Index: idx(4)})
	want("a replacement at position 4", "zero", "first", "a", "a", "B", "c", "end")

	// A position is only a name for an element until somebody pushes onto the
	// list. With what the caller saw there given, an edit against a list that
	// has since shifted is refused instead of landing on the wrong element.
	var conflict *RedisConflictError
	_, err := RedisWriteValue(ctx, client, nil, RedisWrite{Key: key, Type: "list", Value: rb("X"), Index: idx(1), Expect: rb("a")})
	if !errors.As(err, &conflict) {
		t.Fatalf("an edit against a stale position: %v", err)
	}
	_, err = RedisRemoveMembers(ctx, client, RedisRemoval{Key: key, Type: "list", Index: idx(1), Expect: rb("a")})
	if !errors.As(err, &conflict) {
		t.Fatalf("a removal against a stale position: %v", err)
	}
	want("two refused edits", "zero", "first", "a", "a", "B", "c", "end")

	if n, err := RedisRemoveMembers(ctx, client, RedisRemoval{Key: key, Type: "list", Index: idx(3), Expect: rb("a")}); err != nil || n != 1 {
		t.Fatalf("removing position 3: %d, %v", n, err)
	}
	// The second "a" went, not the first: removal is by position, and the
	// marker it uses is not something a real list can contain.
	want("removing position 3", "zero", "first", "a", "B", "c", "end")
	for _, v := range list() {
		if strings.Contains(v, "\x00jd:") {
			t.Fatalf("the marker leaked into the list: %q", list())
		}
	}
	if _, err := RedisWriteValue(ctx, client, nil, RedisWrite{Key: key, Type: "list", Value: rb("X"), Index: idx(99)}); err == nil {
		t.Error("a replacement outside the list was accepted")
	}
	if _, err := RedisRemoveMembers(ctx, client, RedisRemoval{Key: key, Type: "list", Index: idx(99)}); err == nil {
		t.Error("a removal outside the list was accepted")
	}
}

func TestLiveRedisRenamingAMember(t *testing.T) {
	client, _ := redisTestClient(t, "JD_TEST_REDIS_DSN")
	ctx := context.Background()
	hash := RedisBytes(redisTestPrefix + "rename:h")
	client.HSet(ctx, string(hash), "old", "1", "other", "2")
	mustWrite(t, client, RedisWrite{Key: hash, Type: "hash", Field: rb("new"), Value: rb("1"), Replace: rb("old")})
	if got := client.HGetAll(ctx, string(hash)).Val(); len(got) != 2 || got["new"] != "1" || got["other"] != "2" {
		t.Errorf("hash after renaming a field = %v", got)
	}
	// Renaming onto a field that exists would silently replace its value.
	var conflict *RedisConflictError
	_, err := RedisWriteValue(ctx, client, nil, RedisWrite{Key: hash, Type: "hash", Field: rb("other"), Value: rb("1"), Replace: rb("new")})
	if !errors.As(err, &conflict) {
		t.Errorf("renaming onto an existing field: %v", err)
	}

	zset := RedisBytes(redisTestPrefix + "rename:z")
	client.ZAdd(ctx, string(zset), redis.Z{Score: 5, Member: "old"})
	score := RedisScore(5)
	mustWrite(t, client, RedisWrite{Key: zset, Type: "zset", Value: rb("new"), Score: &score, Replace: rb("old")})
	if got := client.ZRange(ctx, string(zset), 0, -1).Val(); len(got) != 1 || got[0] != "new" {
		t.Errorf("sorted set after renaming a member = %v", got)
	}
}

// HSET clears a field's own expiry the way SET clears a key's, so editing a
// field's value has to put it back.
func TestLiveRedisHashFieldKeepsItsExpiry(t *testing.T) {
	client, _ := redisTestClient(t, "JD_TEST_REDIS_DSN")
	ctx := context.Background()
	profile, err := RedisProbe(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	if !profile.Features.HashFieldTTL {
		t.Skipf("%s has no per-field expiry", redisProductName(profile))
	}
	key := redisTestPrefix + "fieldttl"
	client.HSet(ctx, key, "expiring", "1", "plain", "1")
	if err := client.Do(ctx, "HEXPIRE", key, 600, "FIELDS", 1, "expiring").Err(); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, client, RedisWrite{Key: RedisBytes(key), Type: "hash", Field: rb("expiring"), Value: rb("2")})
	mustWrite(t, client, RedisWrite{Key: RedisBytes(key), Type: "hash", Field: rb("plain"), Value: rb("2")})
	// Renaming a field carries its expiry to the new name.
	mustWrite(t, client, RedisWrite{Key: RedisBytes(key), Type: "hash", Field: rb("renamed"), Value: rb("3"), Replace: rb("expiring")})

	rows := allMembers(t, client, RedisMembersOptions{Key: RedisBytes(key)})
	got := map[string]int64{}
	for _, r := range rows {
		if r.TTL == nil {
			t.Fatalf("field %q came back with no expiry reported", *r.Field)
		}
		got[string(*r.Field)] = *r.TTL
	}
	if len(got) != 2 || got["plain"] != -1 || got["renamed"] <= 0 || got["renamed"] > 600 {
		t.Errorf("field expiries = %v", got)
	}
}

// A collection is read to its end in pages, every member exactly once.
func TestLiveRedisPagesInsideLargeKeys(t *testing.T) {
	client, _ := redisTestClient(t, "JD_TEST_REDIS_DSN")
	ctx := context.Background()
	const n = 2500
	p := redisTestPrefix + "big:"
	pipe := client.Pipeline()
	for i := 0; i < n; i++ {
		s := fmt.Sprintf("m%05d", i)
		pipe.HSet(ctx, p+"h", s, i)
		pipe.SAdd(ctx, p+"s", s)
		pipe.ZAdd(ctx, p+"z", redis.Z{Score: float64(i), Member: s})
		pipe.RPush(ctx, p+"l", s)
		pipe.XAdd(ctx, &redis.XAddArgs{Stream: p + "x", ID: fmt.Sprintf("%d-0", i+1), Values: []string{"n", s}})
	}
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}

	seen := func(rows []RedisRow, pick func(RedisRow) string) map[string]int {
		out := map[string]int{}
		for _, r := range rows {
			out[pick(r)]++
		}
		return out
	}
	// exact is for the types read by position, where a member comes back
	// once. A scan promises every member and may repeat one if the server
	// resizes the table part way through, so there only the coverage is
	// checked.
	complete := func(what string, got map[string]int, exact bool) {
		t.Helper()
		if len(got) != n {
			t.Errorf("%s: %d distinct members read, want %d", what, len(got), n)
		}
		for m, times := range got {
			if exact && times != 1 {
				t.Errorf("%s: %q read %d times", what, m, times)
				return
			}
		}
	}

	// The first page of a hash is a page. It used to be the whole hash.
	first, err := RedisReadMembers(ctx, client, nil, RedisMembersOptions{Key: RedisBytes(p + "h"), Count: 100})
	if err != nil {
		t.Fatal(err)
	}
	if first.Done || len(first.Rows) >= n || first.Length != n {
		t.Fatalf("first page of the hash: %d rows, done=%v, length=%d", len(first.Rows), first.Done, first.Length)
	}
	complete("hash", seen(allMembers(t, client, RedisMembersOptions{Key: RedisBytes(p + "h"), Count: 300}),
		func(r RedisRow) string { return string(*r.Field) }), false)
	complete("set", seen(allMembers(t, client, RedisMembersOptions{Key: RedisBytes(p + "s"), Count: 300}),
		func(r RedisRow) string { return string(*r.Value) }), false)

	for _, desc := range []bool{false, true} {
		rows := allMembers(t, client, RedisMembersOptions{Key: RedisBytes(p + "l"), Count: 400, Desc: desc})
		complete(fmt.Sprintf("list desc=%v", desc), seen(rows, func(r RedisRow) string { return string(*r.Value) }), true)
		// Positions are from the head whichever end the page was read from.
		for i, r := range rows {
			at := int64(i)
			if desc {
				at = int64(n - 1 - i)
			}
			if *r.Index != at || string(*r.Value) != fmt.Sprintf("m%05d", at) {
				t.Fatalf("list desc=%v row %d = index %d value %q", desc, i, *r.Index, *r.Value)
			}
		}

		rows = allMembers(t, client, RedisMembersOptions{Key: RedisBytes(p + "z"), Count: 400, Desc: desc})
		complete(fmt.Sprintf("zset desc=%v", desc), seen(rows, func(r RedisRow) string { return string(*r.Value) }), true)
		if !sort.SliceIsSorted(rows, func(i, j int) bool {
			if desc {
				return *rows[i].Score > *rows[j].Score
			}
			return *rows[i].Score < *rows[j].Score
		}) {
			t.Errorf("zset desc=%v is not in score order", desc)
		}

		rows = allMembers(t, client, RedisMembersOptions{Key: RedisBytes(p + "x"), Count: 400, Desc: desc})
		complete(fmt.Sprintf("stream desc=%v", desc), seen(rows, func(r RedisRow) string { return r.ID }), true)
		if wantFirst := map[bool]string{false: "1-0", true: fmt.Sprintf("%d-0", n)}[desc]; rows[0].ID != wantFirst {
			t.Errorf("stream desc=%v starts at %s, want %s", desc, rows[0].ID, wantFirst)
		}
	}

	// A score range, and a search by member name, each page to their own end.
	ranged := allMembers(t, client, RedisMembersOptions{Key: RedisBytes(p + "z"), Count: 50, Min: "100", Max: "(300"})
	if len(ranged) != 200 || float64(*ranged[0].Score) != 100 || float64(*ranged[199].Score) != 299 {
		t.Errorf("score range gave %d rows", len(ranged))
	}
	distinct := func(rows []RedisRow, pick func(RedisRow) string) int { return len(seen(rows, pick)) }
	if matched := allMembers(t, client, RedisMembersOptions{Key: RedisBytes(p + "z"), Match: "m0001?"}); distinct(matched, func(r RedisRow) string { return string(*r.Value) }) != 10 {
		t.Errorf("zset match gave %d rows, want 10", len(matched))
	}
	if matched := allMembers(t, client, RedisMembersOptions{Key: RedisBytes(p + "h"), Match: "m0245*"}); distinct(matched, func(r RedisRow) string { return string(*r.Field) }) != 10 {
		t.Errorf("hash match gave %d rows, want 10", len(matched))
	}
	// A stream bounded by id.
	if window := allMembers(t, client, RedisMembersOptions{Key: RedisBytes(p + "x"), From: "10", To: "19", Count: 4}); len(window) != 10 {
		t.Errorf("stream id window gave %d rows, want 10", len(window))
	}
	for _, bad := range []RedisMembersOptions{
		{Key: RedisBytes(p + "z"), Min: "ten"},
		{Key: RedisBytes(p + "x"), From: "yesterday"},
		{Key: RedisBytes(p + "l"), Cursor: "-5"},
		{Key: RedisBytes(p + "h"), Cursor: "abc"},
	} {
		if _, err := RedisReadMembers(ctx, client, nil, bad); err == nil {
			t.Errorf("%+v was accepted", bad)
		}
	}
	if _, err := RedisReadMembers(ctx, client, nil, RedisMembersOptions{Key: RedisBytes(p + "missing")}); !errors.Is(err, ErrRedisKeyNotFound) {
		t.Errorf("reading a missing key: %v", err)
	}

	// The older read is a window of the same thing, and now says so for a
	// hash too.
	legacy, err := RedisGet(ctx, client, p+"h")
	if err != nil || !legacy.Truncated || len(legacy.Hash) == 0 || len(legacy.Hash) >= n || legacy.Length != n {
		t.Errorf("RedisGet of a large hash: %d fields truncated=%v length=%d err=%v", len(legacy.Hash), legacy.Truncated, legacy.Length, err)
	}
}

func TestLiveRedisStringChunks(t *testing.T) {
	client, _ := redisTestClient(t, "JD_TEST_REDIS_DSN")
	ctx := context.Background()
	key := RedisBytes(redisTestPrefix + "long")
	// Multi-byte characters throughout, so nearly every window boundary falls
	// inside one.
	text := strings.Repeat("héllo ✓ wörld ", 400)
	client.Set(ctx, string(key), text, 0)

	var out strings.Builder
	o := RedisMembersOptions{Key: key, Count: 1000}
	for pages := 0; ; pages++ {
		page, err := RedisReadMembers(ctx, client, nil, o)
		if err != nil {
			t.Fatal(err)
		}
		if page.String.Value.Binary() {
			t.Fatalf("a window of text came back as binary at offset %d", page.String.Offset)
		}
		if page.String.Offset != int64(out.Len()) || page.Length != int64(len(text)) {
			t.Fatalf("window at %d, length %d; read so far %d of %d", page.String.Offset, page.Length, out.Len(), len(text))
		}
		out.WriteString(string(page.String.Value))
		if page.Done {
			break
		}
		if pages > 100 {
			t.Fatal("the string never finished")
		}
		o.Cursor = page.Cursor
	}
	if out.String() != text {
		t.Errorf("the windows do not add up to the value: %d bytes of %d", out.Len(), len(text))
	}
	// A window smaller than one character still makes progress.
	client.Set(ctx, string(key), "✓✓✓", 0)
	tiny, err := RedisReadMembers(ctx, client, nil, RedisMembersOptions{Key: key, Count: 1})
	if err != nil || tiny.String.Value != "✓" || tiny.Done {
		t.Errorf("a one-byte window of a three-byte character = %+v done=%v, %v", tiny.String, tiny.Done, err)
	}
}

func TestLiveRedisKeyMetadataAndScan(t *testing.T) {
	client, _ := redisTestClient(t, "JD_TEST_REDIS_DSN")
	ctx := context.Background()
	p := redisTestPrefix + "meta:"
	client.HSet(ctx, p+"h", "a", "1", "b", "2")
	client.Set(ctx, p+"s", "value", time.Hour)
	client.SAdd(ctx, p+"set", "x")

	meta, err := RedisKeyMetadata(ctx, client, nil, RedisBytes(p+"s"))
	if err != nil {
		t.Fatal(err)
	}
	if meta.Type != "string" || meta.Length != 5 || meta.PTTL <= 0 || meta.TTL != 3600 || meta.ExpiresAt == nil {
		t.Errorf("meta = %+v", meta)
	}
	if until := time.Until(*meta.ExpiresAt); until < 59*time.Minute || until > 61*time.Minute {
		t.Errorf("expiresAt is %v away", until)
	}
	if meta.Memory == nil || *meta.Memory <= 0 || meta.Encoding == "" {
		t.Errorf("memory = %v, encoding = %q", meta.Memory, meta.Encoding)
	}
	// One of the two is there and the other says why it is not.
	if (meta.IdleSeconds == nil) == (meta.Frequency == nil) {
		t.Errorf("idle = %v and frequency = %v; exactly one should be reported", meta.IdleSeconds, meta.Frequency)
	}
	if meta.IdleSeconds != nil && meta.Unavailable["frequency"] == "" {
		t.Error("frequency is missing with no reason given")
	}
	if _, err := RedisKeyMetadata(ctx, client, nil, RedisBytes(p+"nope")); !errors.Is(err, ErrRedisKeyNotFound) {
		t.Errorf("metadata of a missing key: %v", err)
	}

	page, err := RedisScanKeys(ctx, client, nil, RedisScanOptions{Pattern: redisGlobEscape(p) + "*", Type: "hash", Memory: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Keys) != 1 || string(page.Keys[0].Key) != p+"h" || page.Keys[0].Size != 2 || page.Keys[0].Memory == nil {
		t.Errorf("a scan for hashes gave %+v", page.Keys)
	}
	if page.DB != client.Options().DB || page.Total < 3 {
		t.Errorf("page db=%d total=%d", page.DB, page.Total)
	}
	plain, err := RedisScanKeys(ctx, client, nil, RedisScanOptions{Pattern: redisGlobEscape(p) + "*", Count: 100})
	if err != nil || len(plain.Keys) != 3 || plain.Keys[0].Memory != nil {
		t.Errorf("an unfiltered scan gave %+v, %v", plain, err)
	}
	if _, err := RedisScanKeys(ctx, client, nil, RedisScanOptions{Type: "not a type"}); err == nil {
		t.Error("a type with a space in it was accepted")
	}
}

func TestLiveRedisKeyTree(t *testing.T) {
	client, _ := redisTestClient(t, "JD_TEST_REDIS_DSN")
	ctx := context.Background()
	p := redisTestPrefix + "tree:"
	pipe := client.Pipeline()
	for i := 0; i < 40; i++ {
		pipe.HSet(ctx, fmt.Sprintf("%suser:%d:profile", p, i), "n", i)
		pipe.SAdd(ctx, fmt.Sprintf("%suser:%d:tags", p, i), "t")
	}
	for i := 0; i < 15; i++ {
		pipe.Set(ctx, fmt.Sprintf("%scache:%d", p, i), "v", time.Hour)
	}
	pipe.Set(ctx, p+"solo", "v", 0)
	pipe.Set(ctx, p+"cache[v2]:a", "v", 0)
	pipe.Set(ctx, p+"cache[v2]:b", "v", 0)
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}

	tree, err := RedisKeyTree(ctx, client, nil, RedisTreeOptions{Prefix: RedisBytes(p), Depth: 2, Limit: redisTreeMaxLimit})
	if err != nil {
		t.Fatal(err)
	}
	if !tree.Complete || tree.Count != 98 || tree.KeyCount != 1 || len(tree.Keys) != 1 || string(tree.Keys[0].Key) != p+"solo" {
		t.Fatalf("tree: complete=%v count=%d keyCount=%d keys=%+v", tree.Complete, tree.Count, tree.KeyCount, tree.Keys)
	}
	if tree.Types["hash"] != 40 || tree.Types["set"] != 40 || tree.Types["string"] != 18 {
		t.Errorf("types = %v", tree.Types)
	}
	byName := map[string]RedisTreeFolder{}
	for _, f := range tree.Folders {
		byName[string(f.Name)] = f
	}
	user := byName["user"]
	if user.Count != 80 || user.KeyCount != 0 || user.Folders != 40 || len(user.Children) != 40 || string(user.Prefix) != p+"user:" {
		t.Errorf("user = count %d keyCount %d folders %d children %d prefix %q", user.Count, user.KeyCount, user.Folders, len(user.Children), user.Prefix)
	}
	if c := user.Children[0]; c.Count != 2 || c.KeyCount != 2 || c.Types["hash"] != 1 {
		t.Errorf("a user's own folder = %+v", c)
	}
	if tree.Folders[0].Name != "user" {
		t.Errorf("the largest namespace is not first: %s", tree.Folders[0].Name)
	}
	if byName["cache"].Count != 15 || byName["cache[v2]"].Count != 2 {
		t.Errorf("cache = %d, cache[v2] = %d", byName["cache"].Count, byName["cache[v2]"].Count)
	}

	// The pattern a folder hands out lists that folder's keys and no others —
	// including for a namespace whose name is itself glob syntax.
	for name, want := range map[string]int{"cache": 15, "cache[v2]": 2} {
		page, err := RedisScanKeys(ctx, client, nil, RedisScanOptions{Pattern: string(byName[name].Pattern), Count: 500})
		if err != nil || len(page.Keys) != want || !page.Done {
			t.Errorf("scanning %q with its own pattern gave %d keys, want %d (%v)", name, len(page.Keys), want, err)
		}
		inside, err := RedisKeyTree(ctx, client, nil, RedisTreeOptions{Prefix: byName[name].Prefix, Limit: redisTreeMaxLimit})
		if err != nil || inside.KeyCount != int64(want) || len(inside.Folders) != 0 {
			t.Errorf("looking inside %q: %+v, %v", name, inside, err)
		}
	}

	// Narrowed by type and by pattern.
	hashes, err := RedisKeyTree(ctx, client, nil, RedisTreeOptions{Prefix: RedisBytes(p), Type: "hash", Limit: redisTreeMaxLimit})
	if err != nil || hashes.Count != 40 || len(hashes.Types) != 1 {
		t.Errorf("tree of hashes: %+v, %v", hashes, err)
	}
	tags, err := RedisKeyTree(ctx, client, nil, RedisTreeOptions{Prefix: RedisBytes(p), Pattern: "*:tags", Limit: redisTreeMaxLimit})
	if err != nil || tags.Count != 40 {
		t.Errorf("tree of *:tags counted %d, %v", tags.Count, err)
	}

	// A walk cut short says so and can be carried on, and the pieces cover
	// the keyspace. (At least: a scan may hand a key over twice if the server
	// resizes its table between two calls.)
	var total int64
	var cursor uint64
	for calls := 0; ; calls++ {
		part, err := RedisKeyTree(ctx, client, nil, RedisTreeOptions{Prefix: RedisBytes(p), Limit: 1, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		total += part.Count
		if part.Complete != (part.Cursor == 0) {
			t.Fatalf("complete=%v with cursor %d", part.Complete, part.Cursor)
		}
		if part.Complete {
			break
		}
		if calls > 10000 {
			t.Fatalf("the walk had not finished after %d calls", calls)
		}
		cursor = part.Cursor
	}
	if total < 98 {
		t.Errorf("a walk taken in pieces counted %d keys, want all 98", total)
	}
}

func TestLiveRedisBulk(t *testing.T) {
	client, _ := redisTestClient(t, "JD_TEST_REDIS_DSN")
	ctx := context.Background()
	p := redisTestPrefix + "bulk:"
	pattern := redisGlobEscape(p) + "*"
	pipe := client.Pipeline()
	for i := 0; i < 1200; i++ {
		pipe.Set(ctx, fmt.Sprintf("%ss:%d", p, i), "v", 0)
	}
	for i := 0; i < 30; i++ {
		pipe.HSet(ctx, fmt.Sprintf("%sh:%d", p, i), "f", "v")
	}
	pipe.Exec(ctx)
	count := func() int64 {
		res, err := RedisBulk(ctx, client, nil, RedisBulkOptions{Pattern: pattern, Action: "delete", DryRun: true})
		if err != nil {
			t.Fatal(err)
		}
		return res.Matched
	}

	dry, err := RedisBulk(ctx, client, nil, RedisBulkOptions{Pattern: pattern, Action: "delete", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if dry.Matched != 1230 || dry.Affected != 0 || !dry.Complete || len(dry.Sample) == 0 || len(dry.Sample) > redisBulkSample {
		t.Fatalf("dry run = %+v", dry)
	}
	if got := client.Exists(ctx, p+"s:0").Val(); got != 1 {
		t.Fatal("a dry run deleted something")
	}

	// A count that stops early says it stopped, and where, and carrying on
	// from there reaches the rest. How far one turn of the scan gets is the
	// server's business, so the pieces are checked for what they must add up
	// to rather than for where each one ends.
	var pieces int64
	var cursor uint64
	for calls := 0; ; calls++ {
		short, err := RedisBulk(ctx, client, nil, RedisBulkOptions{Pattern: pattern, Action: "delete", DryRun: true, Limit: 10, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		pieces += short.Matched
		if short.Complete != (short.Cursor == 0) {
			t.Fatalf("complete=%v with cursor %d", short.Complete, short.Cursor)
		}
		if short.Complete {
			break
		}
		if short.Matched < 10 || calls > 2000 {
			t.Fatalf("a limited dry run stopped after %d matches and %d calls without finishing", short.Matched, calls)
		}
		cursor = short.Cursor
	}
	if pieces < 1230 {
		t.Errorf("a dry run taken in pieces counted %d keys, want all 1230", pieces)
	}

	ttl, err := RedisBulk(ctx, client, nil, RedisBulkOptions{Pattern: pattern, Type: "hash", Action: "expire", TTL: 600})
	if err != nil || ttl.Matched != 30 || ttl.Affected != 30 {
		t.Fatalf("bulk expire of the hashes = %+v, %v", ttl, err)
	}
	if client.TTL(ctx, p+"h:3").Val() <= 0 || client.TTL(ctx, p+"s:3").Val() != -1 {
		t.Error("the expiry landed on the wrong keys")
	}
	persist, err := RedisBulk(ctx, client, nil, RedisBulkOptions{Pattern: pattern, Action: "persist"})
	if err != nil || persist.Matched != 1230 || persist.Affected != 30 {
		t.Fatalf("bulk persist = %+v, %v", persist, err)
	}

	gone, err := RedisBulk(ctx, client, nil, RedisBulkOptions{Pattern: redisGlobEscape(p+"s:") + "*", Action: "delete"})
	if err != nil || gone.Affected != 1200 || !gone.Complete {
		t.Fatalf("bulk delete = %+v, %v", gone, err)
	}
	if left := count(); left != 30 {
		t.Errorf("%d keys left after deleting the strings, want the 30 hashes", left)
	}
	for name, o := range map[string]RedisBulkOptions{
		"no pattern":         {Action: "delete"},
		"no action":          {Pattern: pattern},
		"an unknown action":  {Pattern: pattern, Action: "flush"},
		"expire with no ttl": {Pattern: pattern, Action: "expire"},
	} {
		if _, err := RedisBulk(ctx, client, nil, o); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestLiveRedisRenameCopyExpire(t *testing.T) {
	client, _ := redisTestClient(t, "JD_TEST_REDIS_DSN")
	ctx := context.Background()
	p := redisTestPrefix + "move:"
	a, b, c := RedisBytes(p+"a"), RedisBytes(p+"b"), RedisBytes(p+"c")
	client.Set(ctx, string(a), "A", time.Hour)
	client.Set(ctx, string(b), "B", 0)

	var exists *RedisKeyExistsError
	if err := RedisRenameKey(ctx, client, a, b, false); !errors.As(err, &exists) {
		t.Fatalf("renaming onto an existing key: %v", err)
	}
	if err := RedisRenameKey(ctx, client, a, a, false); err == nil {
		t.Error("renaming a key to its own name was accepted")
	}
	if err := RedisRenameKey(ctx, client, RedisBytes(p+"nope"), c, false); !errors.Is(err, ErrRedisKeyNotFound) {
		t.Errorf("renaming a missing key: %v", err)
	}
	if err := RedisCopyKey(ctx, client, nil, RedisCopy{From: a, To: b}); !errors.As(err, &exists) {
		t.Fatalf("copying onto an existing key: %v", err)
	}
	if err := RedisCopyKey(ctx, client, nil, RedisCopy{From: RedisBytes(p + "nope"), To: c}); !errors.Is(err, ErrRedisKeyNotFound) {
		t.Errorf("copying a missing key: %v", err)
	}
	if err := RedisCopyKey(ctx, client, nil, RedisCopy{From: a, To: c}); err != nil {
		t.Fatal(err)
	}
	// A copy is the value and its expiry.
	if client.Get(ctx, string(c)).Val() != "A" || client.TTL(ctx, string(c)).Val() <= 0 {
		t.Error("the copy lost its value or its expiry")
	}
	if err := RedisCopyKey(ctx, client, nil, RedisCopy{From: a, To: b, Replace: true}); err != nil || client.Get(ctx, string(b)).Val() != "A" {
		t.Errorf("copy with replace: %v", err)
	}
	if err := RedisRenameKey(ctx, client, c, b, true); err != nil || client.Exists(ctx, string(c)).Val() != 0 {
		t.Errorf("rename with overwrite: %v", err)
	}

	sixty, soon := int64(60), int64(1500)
	if pttl, err := RedisSetExpiry(ctx, client, a, RedisExpiry{Seconds: &sixty}); err != nil || pttl <= 0 || pttl > 60000 {
		t.Errorf("expiry in seconds: %d, %v", pttl, err)
	}
	if pttl, err := RedisSetExpiry(ctx, client, a, RedisExpiry{Millis: &soon}); err != nil || pttl <= 0 || pttl > 1500 {
		t.Errorf("expiry in milliseconds: %d, %v", pttl, err)
	}
	at := time.Now().Add(2 * time.Hour).UnixMilli()
	if pttl, err := RedisSetExpiry(ctx, client, a, RedisExpiry{At: &at}); err != nil || pttl < 7000000 {
		t.Errorf("expiry at a moment: %d, %v", pttl, err)
	}
	// A moment already past is a delete under another name.
	past := time.Now().Add(-time.Minute).UnixMilli()
	if _, err := RedisSetExpiry(ctx, client, a, RedisExpiry{At: &past}); err == nil {
		t.Error("an expiry in the past was accepted")
	}
	if client.Exists(ctx, string(a)).Val() != 1 {
		t.Fatal("the key is gone")
	}
	zero := int64(0)
	if pttl, err := RedisSetExpiry(ctx, client, a, RedisExpiry{Seconds: &zero}); err != nil || pttl != -1 {
		t.Errorf("clearing the expiry: %d, %v", pttl, err)
	}
	if _, err := RedisSetExpiry(ctx, client, a, RedisExpiry{}); err == nil {
		t.Error("an expiry given no way was accepted")
	}
	if _, err := RedisSetExpiry(ctx, client, a, RedisExpiry{Seconds: &sixty, Millis: &soon}); err == nil {
		t.Error("an expiry given two ways was accepted")
	}
	if _, err := RedisSetExpiry(ctx, client, RedisBytes(p+"nope"), RedisExpiry{Seconds: &sixty}); !errors.Is(err, ErrRedisKeyNotFound) {
		t.Errorf("expiry on a missing key: %v", err)
	}
}

func TestLiveRedisStreams(t *testing.T) {
	client, _ := redisTestClient(t, "JD_TEST_REDIS_DSN")
	ctx := context.Background()
	key := RedisBytes(redisTestPrefix + "events")
	for i := 1; i <= 20; i++ {
		mustWrite(t, client, RedisWrite{Key: key, Type: "stream", ID: fmt.Sprintf("%d-0", i),
			Entries: [][2]RedisBytes{{"n", RedisBytes(strconv.Itoa(i))}}})
	}
	if err := RedisStreamGroupCreate(ctx, client, key, "workers", "0"); err != nil {
		t.Fatal(err)
	}
	if err := RedisStreamGroupCreate(ctx, client, key, "workers", "0"); err == nil {
		t.Error("creating a group twice was accepted")
	}
	// A group on a key that is not there must not create the stream.
	if err := RedisStreamGroupCreate(ctx, client, RedisBytes(redisTestPrefix+"typo"), "workers", "$"); err == nil {
		t.Error("a group was created on a missing key")
	}
	if client.Exists(ctx, redisTestPrefix+"typo").Val() != 0 {
		t.Error("creating a group conjured a stream")
	}
	// Two consumers take five entries each and acknowledge nothing.
	for _, consumer := range []string{"alice", "bob"} {
		if err := client.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group: "workers", Consumer: consumer, Streams: []string{string(key), ">"}, Count: 5,
		}).Err(); err != nil {
			t.Fatal(err)
		}
	}

	info, err := RedisStreamDescribe(ctx, client, key)
	if err != nil {
		t.Fatal(err)
	}
	if info.Length != 20 || info.FirstID != "1-0" || info.LastID != "20-0" || len(info.Groups) != 1 {
		t.Fatalf("stream = %+v", info)
	}
	g := info.Groups[0]
	if g.Name != "workers" || g.Pending != 10 || g.OldestPendingID != "1-0" || g.NewestPendingID != "10-0" || g.LastDeliveredID != "10-0" || len(g.Consumers) != 2 {
		t.Errorf("group = %+v", g)
	}
	if g.Lag != nil && *g.Lag != 10 {
		t.Errorf("lag = %d, want 10", *g.Lag)
	}
	sort.Slice(g.Consumers, func(i, j int) bool { return g.Consumers[i].Name < g.Consumers[j].Name })
	if g.Consumers[0].Name != "alice" || g.Consumers[0].Pending != 5 {
		t.Errorf("consumers = %+v", g.Consumers)
	}

	pending, err := RedisStreamPendingEntries(ctx, client, RedisStreamPendingOptions{Key: key, Group: "workers", Count: 4})
	if err != nil || len(pending.Entries) != 4 || pending.Done || pending.Entries[0].ID != "1-0" || pending.Entries[0].Consumer != "alice" || pending.Entries[0].Deliveries != 1 {
		t.Fatalf("pending page = %+v, %v", pending, err)
	}
	var ids []string
	o := RedisStreamPendingOptions{Key: key, Group: "workers", Count: 4}
	for {
		page, err := RedisStreamPendingEntries(ctx, client, o)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range page.Entries {
			ids = append(ids, e.ID)
		}
		if page.Done {
			break
		}
		o.Cursor = page.Cursor
	}
	if len(ids) != 10 {
		t.Errorf("paging the pending list gave %d entries, want 10", len(ids))
	}
	bobs, err := RedisStreamPendingEntries(ctx, client, RedisStreamPendingOptions{Key: key, Group: "workers", Consumer: "bob"})
	if err != nil || len(bobs.Entries) != 5 || bobs.Entries[0].ID != "6-0" {
		t.Errorf("bob's pending = %+v, %v", bobs, err)
	}

	if n, err := RedisStreamAck(ctx, client, key, "workers", []string{"1-0", "2-0"}); err != nil || n != 2 {
		t.Errorf("ack = %d, %v", n, err)
	}
	if _, err := RedisStreamAck(ctx, client, key, "workers", []string{"not-an-id"}); err == nil {
		t.Error("acknowledging something that is not an id was accepted")
	}
	if err := RedisStreamGroupSetID(ctx, client, key, "workers", "$"); err != nil {
		t.Errorf("moving a group's position: %v", err)
	}
	if n, err := RedisRemoveMembers(ctx, client, RedisRemoval{Key: key, Type: "stream", Members: []RedisBytes{"19-0", "20-0"}}); err != nil || n != 2 {
		t.Errorf("deleting two entries: %d, %v", n, err)
	}
	keep := int64(5)
	if n, err := RedisStreamTrimEntries(ctx, client, RedisStreamTrim{Key: key, MaxLen: &keep}); err != nil || n != 13 {
		t.Errorf("trim to 5 removed %d, %v", n, err)
	}
	if n, err := RedisStreamTrimEntries(ctx, client, RedisStreamTrim{Key: key, MinID: "17-0"}); err != nil || n != 3 {
		t.Errorf("trim by id removed %d, %v", n, err)
	}
	if _, err := RedisStreamTrimEntries(ctx, client, RedisStreamTrim{Key: key}); err == nil {
		t.Error("a trim that says nothing about how much to keep was accepted")
	}
	if n, err := RedisStreamGroupRemove(ctx, client, key, "workers", "bob"); err != nil || n != 5 {
		t.Errorf("removing a consumer reported %d pending, %v", n, err)
	}
	if n, err := RedisStreamGroupRemove(ctx, client, key, "workers", ""); err != nil || n != 1 {
		t.Errorf("destroying the group: %d, %v", n, err)
	}
	after, err := RedisStreamDescribe(ctx, client, key)
	if err != nil || len(after.Groups) != 0 || after.Length != 2 {
		t.Errorf("stream afterwards = %+v, %v", after, err)
	}
	client.Set(ctx, redisTestPrefix+"notstream", "v", 0)
	if _, err := RedisStreamDescribe(ctx, client, RedisBytes(redisTestPrefix+"notstream")); err == nil {
		t.Error("describing a string as a stream was accepted")
	}
}

// No ?db= means the database the connection string names, and db=0 means
// database 0. They were the same thing, which made 0 unreachable for any
// connection string that named another.
func TestLiveRedisDatabaseSelection(t *testing.T) {
	client, dsn := redisTestClient(t, "JD_TEST_REDIS_DSN")
	ctx := context.Background()
	own := client.Options().DB
	if own == 0 {
		t.Skip("the connection string names database 0, so there is nothing to tell apart")
	}
	key := redisTestPrefix + "which-db"
	client.Set(ctx, key, "here", time.Minute)

	zero, err := RedisClient(ctx, dsn, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer zero.Close()
	if zero.Options().DB != 0 {
		t.Fatalf("asking for database 0 gave %d", zero.Options().DB)
	}
	// Read only: database 0 of the fixture is not this test's to write to.
	if zero.Exists(ctx, key).Val() != 0 {
		t.Error("database 0 can see a key written to the connection string's own database")
	}
	// Every connection of the pool is on the database asked for, not only
	// the first.
	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := map[int]bool{}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			info, err := client.ClientInfo(ctx).Result()
			if err == nil {
				mu.Lock()
				seen[info.DB] = true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != 1 || !seen[own] {
		t.Errorf("pooled connections are on databases %v, want only %d", seen, own)
	}
	other, err := RedisClient(ctx, dsn, own)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if other.Exists(ctx, key).Val() != 1 {
		t.Error("asking for the database by number did not reach it")
	}
}

func TestLiveRedisConsole(t *testing.T) {
	client, _ := redisTestClient(t, "JD_TEST_REDIS_DSN")
	ctx := context.Background()
	key := redisTestPrefix + "console"
	run := func(line string) RedisReply {
		t.Helper()
		args, err := RedisParseCommand(line)
		if err != nil {
			t.Fatal(err)
		}
		reply, _, _, err := RedisRunCommand(ctx, client, args)
		if err != nil {
			t.Fatalf("%s: %v", line, err)
		}
		return reply
	}
	if r := run("SET " + key + ` "two words"`); r.Type != "string" || r.Value != RedisBytes("OK") {
		t.Errorf("SET = %+v", r)
	}
	if r := run("GET " + key); r.Value != RedisBytes("two words") {
		t.Errorf("GET = %+v", r)
	}
	if r := run("STRLEN " + key); r.Type != "integer" || r.Value != int64(9) {
		t.Errorf("STRLEN = %+v", r)
	}
	if r := run("GET " + key + ":missing"); r.Type != "nil" {
		t.Errorf("GET of a missing key = %+v", r)
	}
	if r := run("RPUSH " + key + ":l a b c"); r.Value != int64(3) {
		t.Errorf("RPUSH = %+v", r)
	}
	if r := run("LRANGE " + key + ":l 0 -1"); r.Type != "array" || len(r.Items) != 3 || r.Items[2].Value != RedisBytes("c") {
		t.Errorf("LRANGE = %+v", r)
	}
	// An error from the server is an answer, not a failed request.
	if r := run("LPUSH " + key + " x"); r.Type != "error" || !strings.HasPrefix(r.Value.(string), "WRONGTYPE") {
		t.Errorf("a wrong-type command = %+v", r)
	}
	if r := run("NOSUCHCOMMAND"); r.Type != "error" {
		t.Errorf("an unknown command = %+v", r)
	}

	flags := RedisCommandLookup(ctx, client, []string{"set", key, "v"})
	if flags == nil || flags.FirstKey != 1 {
		t.Fatalf("COMMAND INFO set = %+v", flags)
	}
	if got := RedisCommandKeys([]string{"SET", key, "v"}, flags); len(got) != 1 || string(got[0]) != key {
		t.Errorf("keys of SET = %q", got)
	}
	if RedisCommandLookup(ctx, client, []string{"nosuchcommand"}) != nil {
		t.Error("the server described a command it does not have")
	}

	refs, err := RedisCommandReference(ctx, client, nil)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]RedisCommandRef{}
	for _, r := range refs {
		byName[r.Name] = r
	}
	if len(refs) < 150 {
		t.Errorf("the reference lists %d commands", len(refs))
	}
	get := byName["GET"]
	if get.Class != RedisClassRead || get.Arity != 2 || get.Group != "string" {
		t.Errorf("GET in the reference = %+v", get)
	}
	if byName["FLUSHALL"].Class != RedisClassDangerous || byName["SUBSCRIBE"].Class != RedisClassBlocked {
		t.Errorf("FLUSHALL = %s, SUBSCRIBE = %s", byName["FLUSHALL"].Class, byName["SUBSCRIBE"].Class)
	}
	// A blocking pop is listed as what it is without arguments to judge by,
	// not as "blocked for want of a timeout".
	if byName["BLPOP"].Class != RedisClassDangerous {
		t.Errorf("BLPOP in the reference = %s", byName["BLPOP"].Class)
	}
	profile, err := RedisProbe(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Features.CommandDocs {
		if get.Summary == "" || get.Syntax != "key" || get.Since == "" {
			t.Errorf("GET's documentation = %+v", get)
		}
		set := byName["CONFIG SET"]
		if set.Class != RedisClassDangerous || !set.Admin || set.Summary == "" {
			t.Errorf("CONFIG SET in the reference = %+v", set)
		}
		if _, listed := byName["CONFIG"]; listed {
			t.Error("a container with subcommands is listed as a command to run")
		}
	}
}

func TestLiveRedisServerReads(t *testing.T) {
	client, _ := redisTestClient(t, "JD_TEST_REDIS_DSN")
	ctx := context.Background()
	client.Set(ctx, redisTestPrefix+"present", "v", time.Hour)

	server, err := RedisServerOverview(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	if server.Flavor == "" || server.Version == "" || server.Mode != "standalone" || server.Role != "primary" {
		t.Errorf("identity = %s %s %s %s", server.Flavor, server.Version, server.Mode, server.Role)
	}
	if server.DB != client.Options().DB || server.Databases < 1 || server.UptimeSeconds <= 0 || len(server.Sections) < 5 {
		t.Errorf("db=%d databases=%d uptime=%d sections=%d", server.DB, server.Databases, server.UptimeSeconds, len(server.Sections))
	}
	if server.Memory.Used <= 0 {
		t.Errorf("memory = %+v", server.Memory)
	}
	found := false
	for _, ks := range server.Keyspace {
		if ks.DB == server.DB && ks.Keys > 0 && ks.Expires > 0 {
			found = true
		}
	}
	if !found {
		t.Errorf("keyspace = %+v, want this database with keys and expiries", server.Keyspace)
	}

	stats, err := RedisCommandStatistics(ctx, client)
	if err != nil || len(stats.Commands) == 0 || stats.TotalCalls <= 0 {
		t.Fatalf("command stats = %+v, %v", stats, err)
	}
	if !sort.SliceIsSorted(stats.Commands, func(i, j int) bool { return stats.Commands[i].Usec > stats.Commands[j].Usec }) {
		t.Error("command stats are not sorted by total time")
	}

	clients, err := RedisClients(ctx, client)
	if err != nil || len(clients) == 0 {
		t.Fatalf("clients = %+v, %v", clients, err)
	}
	self := 0
	for _, c := range clients {
		if c.Self {
			self++
		}
	}
	if self != 1 {
		t.Errorf("%d of %d clients are marked as this connection", self, len(clients))
	}

	slow, err := RedisSlowlogRead(ctx, client, nil, 10)
	if err != nil || !slow.Supported || slow.ThresholdUs == nil || slow.MaxLen == nil {
		t.Errorf("slow log = %+v, %v", slow, err)
	}
	if _, err := RedisLatencyLatest(ctx, client, nil); err != nil {
		t.Errorf("latency: %v", err)
	}

	config, err := RedisConfigRead(ctx, client, nil)
	if err != nil || !config.Supported || len(config.Groups) < 4 {
		t.Fatalf("config = supported %v, %d groups, %v", config.Supported, len(config.Groups), err)
	}
	secrets := 0
	for _, g := range config.Groups {
		for _, p := range g.Params {
			if p.Secret {
				secrets++
				if p.Value != "" || p.Set == nil {
					t.Errorf("secret %s came back with value %q", p.Name, p.Value)
				}
			}
			if p.Name == "requirepass" && !p.Secret {
				t.Error("requirepass is not marked secret")
			}
		}
	}
	if secrets == 0 {
		t.Error("no parameter was recognised as a secret")
	}

	info, err := RedisInfo(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	// The fields the stats route has always carried are still text.
	if _, ok := info["redis_version"].(string); !ok {
		t.Errorf("redis_version = %#v", info["redis_version"])
	}
	if _, ok := info["used_memory_human"].(string); !ok {
		t.Errorf("used_memory_human = %#v", info["used_memory_human"])
	}
	counters, ok := info["counters"].(map[string]any)
	if !ok {
		t.Fatalf("counters = %#v", info["counters"])
	}
	for _, name := range []string{"total_commands_processed", "keyspace_hits", "keyspace_misses", "used_memory", "connected_clients", "instantaneous_ops_per_sec", "evicted_keys", "expired_keys", "uptime_in_seconds", "keys"} {
		if _, ok := counters[name].(int64); !ok {
			t.Errorf("counter %s = %#v, want a number", name, counters[name])
		}
	}
	if info["role"] != "primary" || info["mode"] != "standalone" || info["version"] == "" || info["flavor"] == "" {
		t.Errorf("identity in stats = %v %v %v %v", info["role"], info["mode"], info["version"], info["flavor"])
	}
	if at, ok := info["sampledAtMs"].(int64); !ok || time.Since(time.UnixMilli(at)) > time.Minute {
		t.Errorf("sampledAtMs = %#v", info["sampledAtMs"])
	}
}

func TestLiveRedisAnalysis(t *testing.T) {
	client, _ := redisTestClient(t, "JD_TEST_REDIS_DSN")
	ctx := context.Background()
	pipe := client.Pipeline()
	for i := 0; i < 300; i++ {
		pipe.Set(ctx, fmt.Sprintf("%ssession:%d", redisTestPrefix, i), strings.Repeat("s", 64), 30*time.Minute)
	}
	for i := 0; i < 100; i++ {
		pipe.HSet(ctx, fmt.Sprintf("%suser:%d", redisTestPrefix, i), "name", "x", "mail", "y")
	}
	pipe.Set(ctx, redisTestPrefix+"huge", strings.Repeat("x", 200000), 0)
	pipe.Set(ctx, redisTestPrefix+"week", "v", 3*24*time.Hour)
	pipe.Exec(ctx)

	a, err := RedisAnalyze(ctx, client, nil, RedisAnalysisOptions{Sample: redisAnalysisMax})
	if err != nil {
		t.Fatal(err)
	}
	if !a.Complete || a.Scale != 1 || a.Sampled != a.Total || a.Sampled < 402 || a.EstimatedMemory != a.Memory {
		t.Fatalf("a complete sample: complete=%v scale=%v sampled=%d total=%d", a.Complete, a.Scale, a.Sampled, a.Total)
	}
	types := map[string]RedisAnalysisGroup{}
	for _, g := range a.Types {
		types[string(g.Name)] = g
	}
	if types["string"].Keys < 302 || types["hash"].Keys < 100 || types["string"].Memory < 200000 {
		t.Errorf("types = %+v", a.Types)
	}
	if len(a.TopKeys) == 0 || string(a.TopKeys[0].Key) != redisTestPrefix+"huge" || a.TopKeys[0].Memory < 200000 || a.TopKeys[0].Size != 200000 {
		t.Errorf("largest key = %+v", a.TopKeys[0])
	}
	if !sort.SliceIsSorted(a.TopKeys, func(i, j int) bool { return a.TopKeys[i].Memory > a.TopKeys[j].Memory }) {
		t.Error("top keys are not largest first")
	}
	ns := map[string]RedisAnalysisGroup{}
	for _, g := range a.Namespaces {
		ns[string(g.Name)] = g
	}
	if ns["jdb4"].Keys < 402 {
		t.Errorf("namespaces = %+v", a.Namespaces)
	}
	if len(a.Expiry) != 5 {
		t.Fatalf("expiry bands = %+v", a.Expiry)
	}
	bands := map[string]int64{}
	for i, g := range a.Expiry {
		if string(g.Name) != redisExpiryBands[i] {
			t.Errorf("expiry band %d is %s", i, g.Name)
		}
		bands[string(g.Name)] = g.Keys
	}
	if bands["hour"] < 300 || bands["none"] < 101 || bands["week"] < 1 {
		t.Errorf("expiry = %v", bands)
	}
	if len(a.Encodings) == 0 {
		t.Errorf("no encodings reported; unavailable = %v", a.Unavailable)
	}

	// A sample smaller than the database says how much it stood in for.
	part, err := RedisAnalyze(ctx, client, nil, RedisAnalysisOptions{Sample: 100, Top: 5})
	if err != nil {
		t.Fatal(err)
	}
	if part.Complete || part.Sampled != 100 || part.Scale <= 1 || len(part.TopKeys) > 5 {
		t.Fatalf("a partial sample: complete=%v sampled=%d scale=%v top=%d", part.Complete, part.Sampled, part.Scale, len(part.TopKeys))
	}
	var estimated int64
	for _, g := range part.Types {
		estimated += g.EstimatedKeys
	}
	if diff := estimated - part.Total; diff < -3 || diff > 3 {
		t.Errorf("estimated keys by type add up to %d of %d", estimated, part.Total)
	}

	// Cancelled by the request that asked for it.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := RedisAnalyze(cancelled, client, nil, RedisAnalysisOptions{}); err == nil {
		t.Error("an analysis on a cancelled context ran to the end")
	}
}

// The tests below change the server itself and run only against one this
// run owns.

func TestLiveRedisAdminConfigAndSlowlog(t *testing.T) {
	client, _ := redisTestClient(t, "JD_TEST_REDIS_ADMIN_DSN")
	ctx := context.Background()

	change, err := RedisConfigSet(ctx, client, "slowlog-log-slower-than", "0", false)
	if err != nil || change.Rewritten || change.Secret {
		t.Fatalf("CONFIG SET: %+v, %v", change, err)
	}
	t.Cleanup(func() { client.ConfigSet(context.Background(), "slowlog-log-slower-than", "10000") })
	client.Set(ctx, redisTestPrefix+"slow", "payload", time.Minute)
	slow, err := RedisSlowlogRead(ctx, client, nil, 50)
	if err != nil {
		t.Fatal(err)
	}
	if slow.ThresholdUs == nil || *slow.ThresholdUs != 0 || len(slow.Entries) == 0 || slow.Length == 0 {
		t.Fatalf("slow log with a threshold of 0 = %+v", slow)
	}
	found := false
	for _, e := range slow.Entries {
		if e.Command == "SET" && len(e.Args) >= 2 && string(e.Args[1]) == redisTestPrefix+"slow" {
			found = true
			if e.At.IsZero() || e.ID < 0 || e.Client == "" {
				t.Errorf("entry = %+v", e)
			}
		}
	}
	if !found {
		t.Errorf("the SET just run is not in the slow log: %+v", slow.Entries)
	}
	// With the threshold back up, nothing this test does next is slow enough
	// to be logged, so what is left after a reset is what the reset left.
	if _, err := RedisConfigSet(ctx, client, "slowlog-log-slower-than", "10000000", false); err != nil {
		t.Fatal(err)
	}
	if err := RedisSlowlogReset(ctx, client); err != nil {
		t.Fatal(err)
	}
	if after, _ := RedisSlowlogRead(ctx, client, nil, 50); after.Length != 0 || len(after.Entries) != 0 {
		t.Errorf("the slow log holds %d entries after a reset", after.Length)
	}

	// Asked to write a configuration file this server was not started with:
	// the change stands and the reply says the rewrite did not happen.
	rewritten, err := RedisConfigSet(ctx, client, "slowlog-max-len", "64", true)
	if err != nil {
		t.Fatal(err)
	}
	if rewritten.Rewritten || rewritten.RewriteError == "" {
		t.Errorf("a rewrite with no config file = %+v", rewritten)
	}
	if got := client.ConfigGet(ctx, "slowlog-max-len").Val()["slowlog-max-len"]; got != "64" {
		t.Errorf("slowlog-max-len = %q", got)
	}
	for _, name := range []string{"", "has space", "glob*", "a\nb"} {
		if _, err := RedisConfigSet(ctx, client, name, "1", false); err == nil {
			t.Errorf("parameter name %q was accepted", name)
		}
	}
	if _, err := RedisConfigSet(ctx, client, "no-such-parameter", "1", false); err == nil {
		t.Error("an unknown parameter was accepted")
	}

	// A password is set, is marked as one, and is not read back.
	secret, err := RedisConfigSet(ctx, client, "masterauth", "hunter2-not-for-display", false)
	if err != nil || !secret.Secret {
		t.Fatalf("setting masterauth: %+v, %v", secret, err)
	}
	t.Cleanup(func() { client.ConfigSet(context.Background(), "masterauth", "") })
	config, err := RedisConfigRead(ctx, client, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range config.Groups {
		for _, p := range g.Params {
			if strings.Contains(p.Value, "hunter2") {
				t.Errorf("%s came back with the password in it", p.Name)
			}
			if p.Name == "masterauth" && (p.Set == nil || !*p.Set) {
				t.Errorf("masterauth = %+v, want it reported as set", p)
			}
		}
	}

	if status, err := RedisSave(ctx, client, nil, "bgsave"); err != nil || status == "" {
		t.Errorf("BGSAVE: %q, %v", status, err)
	}
	if _, err := RedisSave(ctx, client, nil, "save"); err == nil {
		t.Error("a foreground save was accepted")
	}
	// The snapshot has to finish before the rewrite may start.
	for i := 0; i < 100; i++ {
		if server, err := RedisServerOverview(ctx, client); err == nil && !server.Persistence.RDB.InProgress {
			if server.Persistence.RDB.LastSaveAt == nil || time.Since(*server.Persistence.RDB.LastSaveAt) > time.Minute {
				t.Errorf("after a BGSAVE the last save is %v", server.Persistence.RDB.LastSaveAt)
			}
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if status, err := RedisSave(ctx, client, nil, "bgrewriteaof"); err != nil || status == "" {
		t.Errorf("BGREWRITEAOF: %q, %v", status, err)
	}
}

func TestLiveRedisAdminLatencyMonitor(t *testing.T) {
	client, _ := redisTestClient(t, "JD_TEST_REDIS_ADMIN_DSN")
	ctx := context.Background()
	off, err := RedisLatencyLatest(ctx, client, nil)
	if err != nil || !off.Supported {
		t.Fatalf("latency: %+v, %v", off, err)
	}
	if _, err := RedisConfigSet(ctx, client, "latency-monitor-threshold", "1", false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		client.ConfigSet(context.Background(), "latency-monitor-threshold", "0")
		client.Do(context.Background(), "LATENCY", "RESET")
	})
	// Something that takes the server well over a millisecond.
	if err := client.Eval(ctx, "local i = 0 while i < 3000000 do i = i + 1 end return i", nil).Err(); err != nil {
		t.Skipf("this server will not run a script (%v)", err)
	}
	on, err := RedisLatencyLatest(ctx, client, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !on.Enabled || on.ThresholdMs == nil || *on.ThresholdMs != 1 || len(on.Events) == 0 {
		t.Fatalf("latency with a threshold of 1 ms = %+v", on)
	}
	if ev := on.Events[0]; ev.Event == "" || ev.MaxMs < 1 || ev.LatestMs < 1 || time.Since(ev.At) > time.Minute {
		t.Errorf("event = %+v", ev)
	}
}

func TestLiveRedisAdminCopyAcrossDatabases(t *testing.T) {
	client, dsn := redisTestClient(t, "JD_TEST_REDIS_ADMIN_DSN")
	ctx := context.Background()
	here := client.Options().DB
	there := here + 1
	key := RedisBytes(redisTestPrefix + "crossing")
	client.Set(ctx, string(key), "v", time.Hour)
	other, err := RedisClient(ctx, dsn, there)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	other.Del(ctx, string(key))
	t.Cleanup(func() { other.Del(context.Background(), string(key)) })

	if err := RedisCopyKey(ctx, client, nil, RedisCopy{From: key, To: key, DB: &there}); err != nil {
		t.Fatalf("copy into database %d: %v", there, err)
	}
	if other.Get(ctx, string(key)).Val() != "v" || other.TTL(ctx, string(key)).Val() <= 0 {
		t.Error("the copy did not arrive with its value and expiry")
	}
	var exists *RedisKeyExistsError
	if err := RedisCopyKey(ctx, client, nil, RedisCopy{From: key, To: key, DB: &there}); !errors.As(err, &exists) {
		t.Errorf("copying onto the copy: %v", err)
	}
	// The same name in the same database is not a copy.
	if err := RedisCopyKey(ctx, client, nil, RedisCopy{From: key, To: key, DB: &here}); err == nil {
		t.Error("copying a key onto itself was accepted")
	}
}

// The server's place in a replication pair, read from each end.
func TestLiveRedisReplicationFacts(t *testing.T) {
	primary, _ := redisTestClient(t, "JD_TEST_REDIS_ADMIN_DSN")
	dsn := os.Getenv("JD_TEST_REDIS_REPLICA_DSN")
	if dsn == "" {
		t.Skip("set JD_TEST_REDIS_REPLICA_DSN to a replica of JD_TEST_REDIS_ADMIN_DSN to run this")
	}
	ctx := context.Background()
	replica, err := RedisClient(ctx, dsn, RedisDSNDatabase)
	if err != nil {
		t.Skipf("replica unreachable: %v", err)
	}
	defer replica.Close()

	var down *RedisServer
	for i := 0; i < 100; i++ {
		if down, err = RedisServerOverview(ctx, replica); err != nil {
			t.Fatal(err)
		}
		if down.Replication.Primary != nil && down.Replication.Primary.Up {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	link := down.Replication.Primary
	if down.Role != "replica" || down.Replication.Role != "replica" || link == nil || !link.Up || !link.ReadOnly || link.Addr == ":" {
		t.Fatalf("the replica's view = %+v, link %+v", down.Replication, link)
	}
	up, err := RedisServerOverview(ctx, primary)
	if err != nil {
		t.Fatal(err)
	}
	if up.Role != "primary" || up.Replication.Primary != nil || len(up.Replication.Replicas) == 0 {
		t.Fatalf("the primary's view = %+v", up.Replication)
	}
	if r := up.Replication.Replicas[0]; r.State != "online" || r.Addr == ":" {
		t.Errorf("the primary's replica = %+v", r)
	}
	info, err := RedisInfo(ctx, replica)
	if err != nil || info["role"] != "replica" {
		t.Errorf("stats role on a replica = %v, %v", info["role"], err)
	}
	// A write to a read-only replica is refused by the server and nothing
	// here pretends otherwise.
	if _, err := RedisWriteValue(ctx, replica, nil, RedisWrite{Key: RedisBytes(redisTestPrefix + "ro"), Value: rb("v")}); err == nil {
		t.Error("a write to a read-only replica was reported as done")
	}
}

func TestLiveRedisAdminClientsAndPubSub(t *testing.T) {
	client, dsn := redisTestClient(t, "JD_TEST_REDIS_ADMIN_DSN")
	ctx := context.Background()

	victim, err := RedisClient(ctx, dsn, RedisDSNDatabase)
	if err != nil {
		t.Fatal(err)
	}
	defer victim.Close()
	victim.Do(ctx, "CLIENT", "SETNAME", "jdb4-victim")
	clients, err := RedisClients(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	for _, c := range clients {
		if c.Name == "jdb4-victim" {
			id = c.ID
		}
	}
	if id == 0 {
		t.Fatalf("the named client is not in the list: %+v", clients)
	}
	if killed, err := RedisKillClient(ctx, client, id); err != nil || !killed {
		t.Fatalf("kill: %v, %v", killed, err)
	}
	if killed, err := RedisKillClient(ctx, client, id); err != nil || killed {
		t.Errorf("killing a client that is gone: %v, %v", killed, err)
	}
	if _, err := RedisKillClient(ctx, client, 0); err == nil {
		t.Error("killing client 0 was accepted")
	}

	// A subscription sees what is published, and stops when its context does.
	subCtx, stop := context.WithCancel(ctx)
	got := make(chan RedisPubSubMessage, 16)
	done := make(chan error, 1)
	go func() {
		done <- RedisSubscribe(subCtx, client, []string{"jdb4.exact"}, []string{"jdb4.pat.*"}, func(m RedisPubSubMessage) { got <- m })
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		state, err := RedisPubSubChannels(ctx, client, "jdb4.*")
		if err == nil && len(state.Channels) == 1 && state.Channels[0].Subscribers == 1 && state.Patterns >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the subscription never showed up: %+v, %v", state, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if n, err := RedisPublish(ctx, client, "jdb4.exact", "plain"); err != nil || n != 1 {
		t.Errorf("publish reached %d subscribers, %v", n, err)
	}
	RedisPublish(ctx, client, "jdb4.pat.one", RedisBytes("\xff\x00"))
	RedisPublish(ctx, client, "jdb4.exact", RedisBytes(strings.Repeat("x", redisPubSubMaxPayload+10)))
	var msgs []RedisPubSubMessage
	for len(msgs) < 3 {
		select {
		case m := <-got:
			msgs = append(msgs, m)
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of 3 messages arrived", len(msgs))
		}
	}
	if msgs[0].Channel != "jdb4.exact" || msgs[0].Payload != "plain" || msgs[0].Pattern != "" {
		t.Errorf("first message = %+v", msgs[0])
	}
	if msgs[1].Pattern != "jdb4.pat.*" || string(msgs[1].Payload) != "\xff\x00" {
		t.Errorf("pattern message = %+v", msgs[1])
	}
	if !msgs[2].Truncated || msgs[2].Bytes != redisPubSubMaxPayload+10 || len(msgs[2].Payload) != redisPubSubMaxPayload {
		t.Errorf("long message: truncated=%v bytes=%d kept=%d", msgs[2].Truncated, msgs[2].Bytes, len(msgs[2].Payload))
	}
	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("a subscription ended by its context returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the subscription did not stop with its context")
	}
	if err := RedisSubscribe(ctx, client, nil, nil, func(RedisPubSubMessage) {}); err == nil {
		t.Error("a subscription to nothing was accepted")
	}
}

func TestLiveRedisAdminMonitor(t *testing.T) {
	client, dsn := redisTestClient(t, "JD_TEST_REDIS_ADMIN_DSN")
	ctx := context.Background()
	key := redisTestPrefix + "watched"

	monCtx, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	events := make(chan RedisMonitorEvent, 256)
	done := make(chan error, 1)
	go func() {
		done <- RedisMonitor(monCtx, dsn, func(ev RedisMonitorEvent) {
			select {
			case events <- ev:
			default:
			}
		})
	}()
	// MONITOR is attached some time after the goroutine starts; keep issuing
	// the command until it is seen.
	var seen *RedisMonitorEvent
	deadline := time.After(8 * time.Second)
	for seen == nil {
		client.Set(ctx, key, "the value \xff", time.Minute)
		select {
		case ev := <-events:
			if ev.Command == "SET" && len(ev.Args) > 0 && ev.Args[0] == key {
				seen = &ev
			}
		case err := <-done:
			t.Fatalf("MONITOR ended early: %v", err)
		case <-deadline:
			t.Fatal("the SET never appeared in the feed")
		case <-time.After(20 * time.Millisecond):
		}
	}
	if seen.At < 1e9 || seen.Client == "" || seen.DB != client.Options().DB {
		t.Errorf("event = %+v", seen)
	}
	if len(seen.Args) < 2 || seen.Args[1] != `the value \xff` {
		t.Errorf("arguments = %q", seen.Args)
	}
	// A value far longer than a feed should carry arrives cut, and the feed
	// carries on past it.
	client.Set(ctx, key+":huge", strings.Repeat("x", redisMonitorMaxLine+5000), time.Minute)
	client.Set(ctx, key+":after", "small", time.Minute)
	var huge, after bool
	for !after {
		select {
		case ev := <-events:
			if ev.Command != "SET" || len(ev.Args) < 2 {
				continue
			}
			switch ev.Args[0] {
			case key + ":huge":
				huge = len(ev.Args[1]) < redisMonitorMaxArg+40 && strings.HasSuffix(ev.Args[1], "…")
			case key + ":after":
				after = true
			}
		case err := <-done:
			t.Fatalf("MONITOR ended on a long line: %v", err)
		case <-deadline:
			t.Fatal("the feed did not get past a long line")
		}
	}
	if !huge {
		t.Error("the long value was not reported cut")
	}
	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("MONITOR ended by its context returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("MONITOR did not stop with its context")
	}
	// Nothing is left attached to the server.
	time.Sleep(100 * time.Millisecond)
	clients, _ := RedisClients(ctx, client)
	for _, c := range clients {
		if c.Command == "MONITOR" || strings.Contains(c.Flags, "O") {
			t.Errorf("a monitoring connection is still open: %+v", c)
		}
	}
	if err := RedisMonitor(ctx, "redis://127.0.0.1:1/0", func(RedisMonitorEvent) {}); err == nil {
		t.Error("monitoring an address nothing listens on returned no error")
	}
}

func TestLiveRedisAdminACL(t *testing.T) {
	client, _ := redisTestClient(t, "JD_TEST_REDIS_ADMIN_DSN")
	ctx := context.Background()
	const name = "jdb4-app"
	client.Do(ctx, "ACL", "DELUSER", name)
	t.Cleanup(func() { client.Do(context.Background(), "ACL", "DELUSER", name) })

	on, off, password := true, false, "correct horse battery"
	created, err := RedisACLSetUser(ctx, client, RedisACLSpec{
		Name: name, Create: true, Enabled: &on, Password: &password,
		Keys: &[]string{"app:*", "%R~shared:*"}, Channels: &[]string{"app.*"},
		Commands: &[]string{"+@read", "+@write", "-@dangerous", "+select"},
	})
	if err != nil {
		t.Fatal(err)
	}
	u := created.User
	if u.Name != name || !u.Enabled || u.Passwords != 1 || u.NoPassword || u.Unrestricted || u.Self || u.System {
		t.Errorf("created user = %+v", u)
	}
	if strings.Join(u.Keys, " ") != "~app:* %R~shared:*" || strings.Join(u.Channels, " ") != "resetchannels &app.*" {
		t.Errorf("keys = %v, channels = %v", u.Keys, u.Channels)
	}
	// The server's ACL has no file here, and the reply says the user will
	// not survive a restart rather than failing the change.
	if created.Persisted || !strings.Contains(created.Notice, "until the server restarts") {
		t.Errorf("persisted = %v, notice = %q", created.Persisted, created.Notice)
	}
	if _, err := RedisACLSetUser(ctx, client, RedisACLSpec{Name: name, Create: true}); err == nil {
		t.Error("creating a user that exists was accepted")
	}
	if _, err := RedisACLSetUser(ctx, client, RedisACLSpec{Name: "jdb4-nobody", Enabled: &on}); err == nil {
		t.Error("changing a user that does not exist was accepted")
	}

	// The rule works as written: the user reads its own keys and nothing else.
	opt := client.Options()
	as := redis.NewClient(&redis.Options{Addr: opt.Addr, Username: name, Password: password, DB: opt.DB})
	defer as.Close()
	client.Set(ctx, "app:k", "v", time.Minute)
	t.Cleanup(func() { client.Del(context.Background(), "app:k") })
	if got, err := as.Get(ctx, "app:k").Result(); err != nil || got != "v" {
		t.Errorf("the user reading its own key: %q, %v", got, err)
	}
	if err := as.Get(ctx, redisTestPrefix+"other").Err(); err == nil || !strings.Contains(err.Error(), "NOPERM") {
		t.Errorf("the user reading somebody else's key: %v", err)
	}
	if err := as.FlushDB(ctx).Err(); err == nil || !strings.Contains(err.Error(), "NOPERM") {
		t.Errorf("the user running FLUSHDB: %v", err)
	}

	// Changing one part of the rule leaves the others — and the password —
	// alone.
	changed, err := RedisACLSetUser(ctx, client, RedisACLSpec{Name: name, Keys: &[]string{"other:*"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(changed.User.Keys, " ") != "~other:*" || changed.User.Passwords != 1 || !changed.User.Enabled {
		t.Errorf("after changing only the keys: %+v", changed.User)
	}
	if strings.Join(changed.User.Commands, " ") != strings.Join(u.Commands, " ") {
		t.Errorf("commands changed from %v to %v", u.Commands, changed.User.Commands)
	}
	disabled, err := RedisACLSetUser(ctx, client, RedisACLSpec{Name: name, Enabled: &off})
	if err != nil || disabled.User.Enabled {
		t.Errorf("disabling: %+v, %v", disabled, err)
	}

	// Nothing the page is given carries the password or its hash.
	users, err := RedisACLUsers(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	roles, err := RedisUsers(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	raw := fmt.Sprintf("%+v %+v %+v", users, roles, created)
	for _, line := range redisStringList(client.Do(ctx, "ACL", "LIST").Val()) {
		for _, token := range strings.Fields(line) {
			if strings.HasPrefix(token, "#") && strings.Contains(raw, token[1:]) {
				t.Errorf("a password hash reached the page: %s", token)
			}
		}
	}
	if strings.Contains(raw, password) {
		t.Error("the password reached the page")
	}
	var self string
	var def bool
	for _, u := range users {
		if u.Self {
			self = u.Name
		}
		def = def || (u.Name == "default" && u.System)
	}
	if self == "" || !def {
		t.Fatalf("users = %+v; want the default user marked, and the dashboard's own", users)
	}

	// The account this connection uses is not one to switch off or remove,
	// whichever account that is.
	if _, err := RedisACLSetUser(ctx, client, RedisACLSpec{Name: self, Enabled: &off}); err == nil {
		t.Errorf("switching off %q, the user the dashboard connects as, was accepted", self)
	}
	if _, err := RedisACLDeleteUser(ctx, client, self); err == nil {
		t.Errorf("removing %q, the user the dashboard connects as, was accepted", self)
	}
	if _, err := RedisACLDeleteUser(ctx, client, "default"); err == nil {
		t.Error("removing the default user was accepted")
	}
	if _, err := RedisACLDeleteUser(ctx, client, name); err != nil {
		t.Fatal(err)
	}
	if _, err := RedisACLDeleteUser(ctx, client, name); err == nil {
		t.Error("removing a user twice was accepted")
	}

	// The older entry points still work on top of the same code.
	if err := RedisCreateUser(ctx, client, RoleSpec{Name: name, Password: password}); err != nil {
		t.Fatal(err)
	}
	if err := RedisAlterUser(ctx, client, RoleSpec{Name: name, Password: "another password", SetPassword: true}); err != nil {
		t.Error(err)
	}
	roles, _ = RedisUsers(ctx, client)
	for _, r := range roles {
		if r.Name == name {
			if !r.Login || r.Superuser || len(r.MemberOf) != 1 || strings.Contains(r.MemberOf[0], "#") {
				t.Errorf("role = %+v", r)
			}
		}
	}
	if err := RedisDropUser(ctx, client, name); err != nil {
		t.Error(err)
	}
}

// Every product behind the protocol answers every read, with the facts it
// cannot give left out rather than failing the request.
func TestLiveRedisFlavors(t *testing.T) {
	list := os.Getenv("JD_TEST_REDIS_FLAVORS")
	if list == "" {
		t.Skip("set JD_TEST_REDIS_FLAVORS to run this")
	}
	for _, pair := range strings.Split(list, ",") {
		want, dsn, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok {
			t.Fatalf("JD_TEST_REDIS_FLAVORS entry %q is not name=dsn", pair)
		}
		t.Run(want, func(t *testing.T) {
			ctx := context.Background()
			client, err := RedisClient(ctx, dsn, RedisDSNDatabase)
			if err != nil {
				t.Skipf("%s unreachable: %v", want, err)
			}
			defer client.Close()
			p := redisTestPrefix + "flavor:"
			defer func() {
				keys, _, _ := client.Scan(ctx, 0, redisGlobEscape(p)+"*", 10000).Result()
				if len(keys) > 0 {
					client.Del(ctx, keys...)
				}
			}()

			profile, err := RedisProbe(ctx, client)
			if err != nil {
				t.Fatal(err)
			}
			if profile.Flavor != want {
				t.Fatalf("detected %s, want %s", profile.Flavor, want)
			}
			f := profile.Features
			t.Logf("%s %s (redis %s): %+v", profile.Flavor, profile.Version, profile.RedisVersion, f)

			ttl := int64(500)
			mustWrite(t, client, RedisWrite{Key: RedisBytes(p + "s"), Value: rb("v1"), TTL: &ttl})
			mustWrite(t, client, RedisWrite{Key: RedisBytes(p + "s"), Value: rb("v2")})
			if got := client.TTL(ctx, p+"s").Val(); got <= 0 {
				t.Errorf("a string save cleared the expiry: %v", got)
			}
			mustWrite(t, client, RedisWrite{Key: RedisBytes(p + "h"), Type: "hash", Field: rb("f"), Value: rb("v")})
			idx := int64(0)
			mustWrite(t, client, RedisWrite{Key: RedisBytes(p + "l"), Type: "list", Value: rb("b")})
			mustWrite(t, client, RedisWrite{Key: RedisBytes(p + "l"), Type: "list", Value: rb("a"), Index: &idx, Insert: true})
			if got := client.LRange(ctx, p+"l", 0, -1).Val(); strings.Join(got, "") != "ab" {
				t.Errorf("list after an insert = %v", got)
			}
			mustWrite(t, client, RedisWrite{Key: RedisBytes(p + "x"), Type: "stream", Entries: [][2]RedisBytes{{"f", "v"}}})

			meta, err := RedisKeyMetadata(ctx, client, profile, RedisBytes(p+"h"))
			if err != nil {
				t.Fatalf("metadata: %v", err)
			}
			// What the server has is reported; what it lacks is explained.
			for fact, have := range map[string]bool{
				"memory": meta.Memory != nil, "encoding": meta.Encoding != "",
			} {
				supported := map[string]bool{"memory": f.MemoryUsage, "encoding": f.ObjectEncoding}[fact]
				if have != supported {
					t.Errorf("%s: reported=%v, server has it=%v", fact, have, supported)
				}
				if !have && meta.Unavailable[fact] == "" {
					t.Errorf("%s is missing with no reason given", fact)
				}
			}
			if !f.ObjectEncoding && (meta.Unavailable["idleSeconds"] == "" || meta.Unavailable["frequency"] == "") {
				t.Errorf("unavailable = %v", meta.Unavailable)
			}

			if rows := allMembers(t, client, RedisMembersOptions{Key: RedisBytes(p + "h")}); len(rows) != 1 {
				t.Errorf("hash rows = %+v", rows)
			}
			if _, err := RedisStreamDescribe(ctx, client, RedisBytes(p+"x")); err != nil {
				t.Errorf("stream: %v", err)
			}
			page, err := RedisScanKeys(ctx, client, profile, RedisScanOptions{Pattern: redisGlobEscape(p) + "*", Type: "hash", Memory: true})
			if err != nil || len(page.Keys) != 1 {
				t.Errorf("typed scan: %+v, %v", page, err)
			}
			if tree, err := RedisKeyTree(ctx, client, profile, RedisTreeOptions{Prefix: RedisBytes(p)}); err != nil || tree.Count != 4 {
				t.Errorf("tree: %+v, %v", tree, err)
			}
			if err := RedisCopyKey(ctx, client, profile, RedisCopy{From: RedisBytes(p + "h"), To: RedisBytes(p + "h2")}); err != nil {
				t.Errorf("copy: %v", err)
			}
			if res, err := RedisBulk(ctx, client, profile, RedisBulkOptions{Pattern: redisGlobEscape(p+"h") + "*", Action: "delete"}); err != nil || res.Affected != 2 {
				t.Errorf("bulk delete: %+v, %v", res, err)
			}
			if a, err := RedisAnalyze(ctx, client, profile, RedisAnalysisOptions{}); err != nil || a.Sampled < 3 {
				t.Errorf("analysis: %+v, %v", a, err)
			} else if (len(a.Encodings) > 0) != f.ObjectEncoding {
				t.Errorf("analysis encodings = %d with objectEncoding=%v", len(a.Encodings), f.ObjectEncoding)
			}
			if _, err := RedisServerOverview(ctx, client); err != nil {
				t.Errorf("overview: %v", err)
			}
			if stats, err := RedisCommandStatistics(ctx, client); err != nil || len(stats.Commands) == 0 {
				t.Errorf("command stats: %v", err)
			}
			if clients, err := RedisClients(ctx, client); err != nil || len(clients) == 0 {
				t.Errorf("clients: %v", err)
			}
			if config, err := RedisConfigRead(ctx, client, profile); err != nil || !config.Supported {
				t.Errorf("config: %+v, %v", config, err)
			}
			if _, err := RedisSlowlogRead(ctx, client, profile, 10); err != nil {
				t.Errorf("slow log: %v", err)
			}
			if _, err := RedisLatencyLatest(ctx, client, profile); err != nil {
				t.Errorf("latency: %v", err)
			}
			if f.ACL {
				if users, err := RedisACLUsers(ctx, client); err != nil || len(users) == 0 {
					t.Errorf("users: %v", err)
				}
				// The rule a page builds is one every flavour accepts.
				const name = "jdb4-flavor"
				client.Do(ctx, "ACL", "DELUSER", name)
				on, password := true, "a password for the test"
				made, err := RedisACLSetUser(ctx, client, RedisACLSpec{
					Name: name, Create: true, Enabled: &on, Password: &password,
					Keys: &[]string{"app:*"}, Commands: &[]string{"+@read", "-@dangerous"},
				})
				if err != nil {
					t.Errorf("create user: %v", err)
				} else {
					if u := made.User; !u.Enabled || u.Passwords != 1 || len(u.Keys) != 1 || u.Unrestricted {
						t.Errorf("created user = %+v", u)
					}
					changed, err := RedisACLSetUser(ctx, client, RedisACLSpec{Name: name, Keys: &[]string{"other:*", "more:*"}})
					if err != nil || len(changed.User.Keys) != 2 || changed.User.Passwords != 1 {
						t.Errorf("change keys: %+v, %v", changed, err)
					}
					if _, err := RedisACLDeleteUser(ctx, client, name); err != nil {
						t.Errorf("delete user: %v", err)
					}
				}
			}
			if !f.Copy {
				elsewhere := client.Options().DB + 1
				if err := RedisCopyKey(ctx, client, profile, RedisCopy{From: RedisBytes(p + "s"), To: RedisBytes(p + "s"), DB: &elsewhere}); err == nil {
					t.Error("a copy into another database was accepted on a server with no COPY")
				}
			}
			if !f.AOF {
				if _, err := RedisSave(ctx, client, profile, "bgrewriteaof"); err == nil {
					t.Error("an AOF rewrite was accepted on a server with no AOF")
				}
			}
			if dbs, err := RedisDatabases(ctx, client); err != nil || len(dbs) == 0 {
				t.Errorf("databases: %v", err)
			}
			refs, err := RedisCommandReference(ctx, client, profile)
			if err != nil || len(refs) < 100 {
				t.Fatalf("command reference: %d, %v", len(refs), err)
			}
			documented := 0
			for _, r := range refs {
				if r.Summary != "" {
					documented++
				}
			}
			if (documented > 0) != f.CommandDocs {
				t.Errorf("%d documented commands with commandDocs=%v", documented, f.CommandDocs)
			}

			if f.JSON {
				mustWrite(t, client, RedisWrite{Key: RedisBytes(p + "j"), Type: "json", Value: rb(`{"a":{"b":[1,2,3]},"n":1}`), Create: true})
				doc, err := RedisReadMembers(ctx, client, profile, RedisMembersOptions{Key: RedisBytes(p + "j"), Path: "$.a.b"})
				if err != nil || doc.JSON == nil || string(doc.JSON.Matches) != "[[1,2,3]]" {
					t.Errorf("JSON read: %+v, %v", doc, err)
				}
				mustWrite(t, client, RedisWrite{Key: RedisBytes(p + "j"), Type: "json", Path: "$.n", Value: rb(`2`)})
				if n, err := RedisJSONDelete(ctx, client, RedisBytes(p+"j"), "$.a"); err != nil || n != 1 {
					t.Errorf("JSON delete: %d, %v", n, err)
				}
				whole, _ := RedisReadMembers(ctx, client, profile, RedisMembersOptions{Key: RedisBytes(p + "j")})
				if whole.JSON == nil || string(whole.JSON.Matches) != `[{"n":2}]` {
					t.Errorf("document afterwards = %s", whole.JSON.Matches)
				}
				if _, err := RedisWriteValue(ctx, client, profile, RedisWrite{Key: RedisBytes(p + "j"), Type: "json", Value: rb(`{}`), Create: true}); err == nil {
					t.Error("creating a JSON document over one that exists was accepted")
				}
				// A document too large for a page says so, and a path into it
				// still reads.
				large := `{"blob":"` + strings.Repeat("x", redisMaxJSONBytes) + `","small":7}`
				mustWrite(t, client, RedisWrite{Key: RedisBytes(p + "jbig"), Type: "json", Value: rb(large)})
				big, err := RedisReadMembers(ctx, client, profile, RedisMembersOptions{Key: RedisBytes(p + "jbig")})
				if err != nil || big.JSON == nil || !big.JSON.TooLarge || big.JSON.Matches != nil || big.JSON.Bytes <= redisMaxJSONBytes {
					t.Errorf("a large document: %+v, %v", big.JSON, err)
				}
				part, err := RedisReadMembers(ctx, client, profile, RedisMembersOptions{Key: RedisBytes(p + "jbig"), Path: "$.small"})
				if err != nil || part.JSON == nil || string(part.JSON.Matches) != "[7]" {
					t.Errorf("a path into a large document: %+v, %v", part.JSON, err)
				}
				if _, err := RedisReadMembers(ctx, client, profile, RedisMembersOptions{Key: RedisBytes(p + "j"), Path: "a.b"}); err == nil {
					t.Error("a path that is not a JSONPath was accepted")
				}
			} else if _, err := RedisWriteValue(ctx, client, profile, RedisWrite{Key: RedisBytes(p + "j"), Type: "json", Value: rb(`{}`)}); err == nil {
				t.Error("a JSON write was accepted on a server with no JSON commands")
			}
		})
	}
}

// An endpoint that is not a plain single server is reported as what it is.
// Without that a sentinel is a server where every key command is "unknown",
// and a cluster node one where some keys answer MOVED.
func TestLiveRedisClusterNodeAndSentinel(t *testing.T) {
	ctx := context.Background()
	open := func(t *testing.T, env string) *redis.Client {
		t.Helper()
		dsn := os.Getenv(env)
		if dsn == "" {
			t.Skipf("set %s to run this", env)
		}
		client, err := RedisClient(ctx, dsn, RedisDSNDatabase)
		if err != nil {
			t.Skipf("unreachable at %s (%v)", env, err)
		}
		t.Cleanup(func() { client.Close() })
		return client
	}

	t.Run("cluster", func(t *testing.T) {
		client := open(t, "JD_TEST_REDIS_CLUSTER_DSN")
		server, err := RedisServerOverview(ctx, client)
		if err != nil {
			t.Fatal(err)
		}
		if server.Mode != "cluster" || server.Notice == "" || server.Cluster == nil || server.Databases != 1 || server.Features.Databases {
			t.Errorf("overview = mode %s, notice %q, cluster %+v, databases %d", server.Mode, server.Notice, server.Cluster, server.Databases)
		}
		if dbs, err := RedisDatabases(ctx, client); err != nil || len(dbs) != 1 {
			t.Errorf("databases = %+v, %v", dbs, err)
		}
		// The keyspace it does hold still lists.
		if _, err := RedisScanKeys(ctx, client, nil, RedisScanOptions{Pattern: "*", Count: 10}); err != nil {
			t.Errorf("scan: %v", err)
		}
		// A key this node does not serve answers with a sentence about
		// clusters, not with the protocol's redirect.
		_, err = RedisKeyMetadata(ctx, client, nil, "somewhere-else")
		if err != nil && !strings.Contains(err.Error(), "Cluster") && !errors.Is(err, ErrRedisKeyNotFound) {
			t.Errorf("a key on another node: %v", err)
		}
	})

	t.Run("sentinel", func(t *testing.T) {
		client := open(t, "JD_TEST_REDIS_SENTINEL_DSN")
		server, err := RedisServerOverview(ctx, client)
		if err != nil {
			t.Fatal(err)
		}
		if server.Mode != "sentinel" || server.Role != "sentinel" || server.Notice == "" || server.Databases != 0 {
			t.Errorf("overview = mode %s, role %s, notice %q, databases %d", server.Mode, server.Role, server.Notice, server.Databases)
		}
		if f := server.Features; f.ScanType || f.MemoryUsage || f.Streams || f.Monitor {
			t.Errorf("a sentinel was given key features: %+v", f)
		}
		if dbs, err := RedisDatabases(ctx, client); err != nil || len(dbs) != 0 {
			t.Errorf("databases = %+v, %v", dbs, err)
		}
		if _, err := RedisScanKeys(ctx, client, nil, RedisScanOptions{Pattern: "*", Count: 10}); !errors.Is(err, ErrRedisSentinel) {
			t.Errorf("scan: %v", err)
		}
		if _, err := RedisKeyTree(ctx, client, nil, RedisTreeOptions{}); !errors.Is(err, ErrRedisSentinel) {
			t.Errorf("tree: %v", err)
		}
		if _, err := RedisKeyMetadata(ctx, client, nil, "k"); !errors.Is(err, ErrRedisSentinel) {
			t.Errorf("metadata: %v", err)
		}
		if _, err := RedisWriteValue(ctx, client, nil, RedisWrite{Key: "k", Value: rb("v")}); !errors.Is(err, ErrRedisSentinel) {
			t.Errorf("write: %v", err)
		}
		if _, err := RedisAnalyze(ctx, client, nil, RedisAnalysisOptions{}); !errors.Is(err, ErrRedisSentinel) {
			t.Errorf("analysis: %v", err)
		}
		if _, err := RedisBulk(ctx, client, nil, RedisBulkOptions{Pattern: "*", Action: "delete", DryRun: true}); !errors.Is(err, ErrRedisSentinel) {
			t.Errorf("bulk: %v", err)
		}
	})
}
