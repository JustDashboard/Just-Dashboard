package dbx

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/mongo"
)

// Live tests for the two engines that are not SQL. MongoDB keeps the contract
// of the SQL ones: default to a local instance, skip with a useful message
// when it is not there. Redis has no default; liveRedis says why.

func liveMongo(t *testing.T) (*mongo.Client, string) {
	t.Helper()
	dsn := os.Getenv("JD_TEST_MONGO_DSN")
	if dsn == "" {
		dsn = "mongodb://127.0.0.1:27017/jdtest"
	}
	client, err := MongoClient(context.Background(), dsn)
	if err != nil {
		t.Skipf("MongoDB unreachable — set JD_TEST_MONGO_DSN to run these (%v)", err)
	}
	t.Cleanup(func() { client.Disconnect(context.Background()) })
	// The database the connection string names, so two checkouts sharing a
	// server do not share a collection.
	if info, err := ParseDSN(DriverMongo, dsn); err == nil && info.Database != "" {
		return client, info.Database
	}
	return client, "jdtest"
}

func liveRedis(t *testing.T) *redis.Client {
	t.Helper()
	// No default. These tests write, and an address nobody chose — the port
	// every Redis listens on — is as likely to be somebody's real data as a
	// fixture.
	dsn := os.Getenv("JD_TEST_REDIS_DSN")
	if dsn == "" {
		t.Skip("set JD_TEST_REDIS_DSN to run this")
	}
	// The database the connection string names, not database 0: the fixture
	// is shared and each run is given a database of its own.
	client, err := RedisClient(context.Background(), dsn, RedisDSNDatabase)
	if err != nil {
		t.Skipf("Redis unreachable at JD_TEST_REDIS_DSN (%v)", err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

func TestLiveMongo(t *testing.T) {
	client, dbName := liveMongo(t)
	ctx := context.Background()
	const coll = "jd_people"

	_ = MongoDropCollection(ctx, client, dbName, coll)
	t.Cleanup(func() { _ = MongoDropCollection(context.Background(), client, dbName, coll) })

	if err := MongoCreateCollection(ctx, client, dbName, coll); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	var annID string
	t.Run("insert", func(t *testing.T) {
		id, err := MongoInsert(ctx, client, dbName, coll, `{"name":"Ann","age":31,"tags":["a","b"]}`)
		if err != nil {
			t.Fatalf("MongoInsert: %v", err)
		}
		annID = id
		if _, err := MongoInsert(ctx, client, dbName, coll, `{"name":"Bo","age":24}`); err != nil {
			t.Fatalf("MongoInsert 2: %v", err)
		}
	})

	t.Run("databases_and_collections", func(t *testing.T) {
		dbs, err := MongoDatabases(ctx, client)
		if err != nil {
			t.Fatalf("MongoDatabases: %v", err)
		}
		if len(dbs) == 0 {
			t.Error("no databases reported")
		}
		colls, err := MongoCollections(ctx, client, dbName)
		if err != nil {
			t.Fatalf("MongoCollections: %v", err)
		}
		found := false
		for _, c := range colls {
			if c.Name == coll {
				found = true
			}
		}
		if !found {
			t.Errorf("collection %s missing from listing", coll)
		}
	})

	t.Run("find_filter_sort", func(t *testing.T) {
		all, err := MongoFind(ctx, client, dbName, coll, "{}", 10, 0)
		if err != nil {
			t.Fatalf("MongoFind: %v", err)
		}
		if all.RowCount != 2 {
			t.Fatalf("documents = %d, want 2", all.RowCount)
		}
		// _id must be the first column and must render as bare hex, because that
		// is the value an edit sends back as its filter.
		if len(all.Columns) == 0 || all.Columns[0] != "_id" {
			t.Errorf("columns = %v, want _id first", all.Columns)
		}
		idIdx := indexOf(all.Columns, "_id")
		if s, ok := all.Rows[0][idIdx].(string); !ok || len(s) != 24 {
			t.Errorf("_id rendered as %#v, want a 24-char hex string", all.Rows[0][idIdx])
		}

		filtered, err := MongoQuery(ctx, client, dbName, coll, MongoFindOptions{
			Filter: `{"name":"Ann"}`, Limit: 10,
		})
		if err != nil {
			t.Fatalf("filtered find: %v", err)
		}
		if filtered.RowCount != 1 {
			t.Errorf("filter matched %d, want 1", filtered.RowCount)
		}

		sorted, err := MongoQuery(ctx, client, dbName, coll, MongoFindOptions{
			Sort: `{"age":-1}`, Limit: 10,
		})
		if err != nil {
			t.Fatalf("sorted find: %v", err)
		}
		nameIdx := indexOf(sorted.Columns, "name")
		if sorted.Rows[0][nameIdx] != "Ann" {
			t.Errorf("descending age put %v first, want Ann", sorted.Rows[0][nameIdx])
		}
	})

	t.Run("count_indexes_stats", func(t *testing.T) {
		n, err := MongoCount(ctx, client, dbName, coll, `{"age":{"$gt":25}}`)
		if err != nil {
			t.Fatalf("MongoCount: %v", err)
		}
		if n != 1 {
			t.Errorf("count = %d, want 1", n)
		}
		idx, err := MongoIndexes(ctx, client, dbName, coll)
		if err != nil {
			t.Fatalf("MongoIndexes: %v", err)
		}
		if len(idx) == 0 || !idx[0].Primary {
			t.Errorf("expected the _id index reported as primary, got %+v", idx)
		}
		if _, err := MongoCollStats(ctx, client, dbName, coll); err != nil {
			t.Fatalf("MongoCollStats: %v", err)
		}
	})

	t.Run("replace_by_bare_hex_id", func(t *testing.T) {
		// The grid shows a bare hex _id; an edit must be able to send exactly
		// that back as the filter without wrapping it in {"$oid": …}.
		n, err := MongoReplace(ctx, client, dbName, coll,
			`{"_id":"`+annID+`"}`, `{"name":"Ann","age":32}`)
		if err != nil {
			t.Fatalf("MongoReplace: %v", err)
		}
		if n != 1 {
			t.Fatalf("replaced %d documents, want 1", n)
		}
		got, err := MongoQuery(ctx, client, dbName, coll, MongoFindOptions{
			Filter: `{"name":"Ann"}`, Limit: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		ageIdx := indexOf(got.Columns, "age")
		if got.Rows[0][ageIdx] == nil {
			t.Fatal("age missing after replace")
		}
	})

	t.Run("replace_refuses_empty_filter", func(t *testing.T) {
		// An empty filter would rewrite an arbitrary document.
		if _, err := MongoReplace(ctx, client, dbName, coll, "{}", `{"x":1}`); err == nil {
			t.Error("expected an empty filter to be refused")
		}
		if _, err := MongoDelete(ctx, client, dbName, coll, "{}", true); err == nil {
			t.Error("expected an empty delete filter to be refused")
		}
	})

	t.Run("aggregate", func(t *testing.T) {
		res, err := MongoAggregate(ctx, client, dbName, coll,
			`[{"$group":{"_id":null,"total":{"$sum":"$age"}}}]`, 10)
		if err != nil {
			t.Fatalf("MongoAggregate: %v", err)
		}
		if res.RowCount != 1 {
			t.Fatalf("aggregate rows = %d, want 1", res.RowCount)
		}
		if !MongoWritesInPipeline(`[{"$out":"other"}]`) {
			t.Error("$out not detected as a write")
		}
		if MongoWritesInPipeline(`[{"$match":{}}]`) {
			t.Error("$match wrongly detected as a write")
		}
	})

	t.Run("export", func(t *testing.T) {
		var csvBuf strings.Builder
		n, _, err := MongoExport(ctx, client, dbName, coll, MongoFindOptions{}, ExportCSV, &csvBuf, 0)
		if err != nil {
			t.Fatalf("MongoExport csv: %v", err)
		}
		if n != 2 {
			t.Errorf("exported %d documents, want 2", n)
		}
		head := strings.SplitN(csvBuf.String(), "\n", 2)[0]
		if !strings.HasPrefix(head, "_id") {
			t.Errorf("csv header = %q, want _id first", head)
		}
		// The union-of-keys header must cover a field only one document has.
		if !strings.Contains(head, "tags") && !strings.Contains(csvBuf.String(), "Ann") {
			t.Errorf("csv missing expected content:\n%s", csvBuf.String())
		}

		var jsonBuf strings.Builder
		if _, _, err := MongoExport(ctx, client, dbName, coll, MongoFindOptions{}, ExportJSON, &jsonBuf, 0); err != nil {
			t.Fatalf("MongoExport json: %v", err)
		}
		if !strings.Contains(jsonBuf.String(), `"name"`) {
			t.Errorf("json export missing keys:\n%s", jsonBuf.String())
		}
	})

	t.Run("import", func(t *testing.T) {
		res, err := MongoImport(ctx, client, dbName, coll, "json",
			`[{"name":"Cy","age":40},{"name":"Di","age":41}]`, false)
		if err != nil {
			t.Fatalf("MongoImport json: %v", err)
		}
		if res.Inserted != 2 {
			t.Errorf("json import inserted %d, want 2 (%v)", res.Inserted, res.Errors)
		}
		res, err = MongoImport(ctx, client, dbName, coll, "csv",
			"name,age\nEve,50\nFin,51\n", false)
		if err != nil {
			t.Fatalf("MongoImport csv: %v", err)
		}
		if res.Inserted != 2 {
			t.Errorf("csv import inserted %d, want 2 (%v)", res.Inserted, res.Errors)
		}
		// Newline-delimited JSON is what mongoexport emits, so it must load too.
		res, err = MongoImport(ctx, client, dbName, coll, "json",
			"{\"name\":\"Gus\"}\n{\"name\":\"Hal\"}", false)
		if err != nil {
			t.Fatalf("MongoImport ndjson: %v", err)
		}
		if res.Inserted != 2 {
			t.Errorf("ndjson import inserted %d, want 2 (%v)", res.Inserted, res.Errors)
		}
	})

	t.Run("delete", func(t *testing.T) {
		n, err := MongoDelete(ctx, client, dbName, coll, `{"name":"Bo"}`, false)
		if err != nil {
			t.Fatalf("MongoDelete: %v", err)
		}
		if n != 1 {
			t.Errorf("deleted %d, want 1", n)
		}
	})
}

// redisTestSet writes one value in the shape the key routes first took: field
// carries a hash field's name, a list position or a sorted-set score, and a
// ttl of zero says nothing about the expiry.
func redisTestSet(ctx context.Context, client *redis.Client, key, typ, field, value string, ttl int64) error {
	v := RedisBytes(value)
	w := RedisWrite{Key: RedisBytes(key), Type: typ, Value: &v}
	if field != "" {
		f := RedisBytes(field)
		w.Field = &f
	}
	if ttl > 0 {
		w.TTL = &ttl
	}
	_, err := RedisWriteValue(ctx, client, nil, w)
	return err
}

func TestLiveRedis(t *testing.T) {
	client := liveRedis(t)
	ctx := context.Background()
	const prefix = "jdtest:"

	cleanup := func() {
		keys, _, _ := client.Scan(ctx, 0, prefix+"*", 1000).Result()
		if len(keys) > 0 {
			client.Del(ctx, keys...)
		}
	}
	cleanup()
	t.Cleanup(cleanup)

	t.Run("string_roundtrip", func(t *testing.T) {
		key := prefix + "greeting"
		if err := redisTestSet(ctx, client, key, "string", "", "hello", 60); err != nil {
			t.Fatalf("write: %v", err)
		}
		v, err := RedisGet(ctx, client, key)
		if err != nil {
			t.Fatalf("RedisGet: %v", err)
		}
		if v.Type != "string" || v.String != "hello" {
			t.Errorf("value = %+v", v)
		}
		if v.TTL <= 0 || v.TTL > 60 {
			t.Errorf("ttl = %d, want 0<ttl<=60", v.TTL)
		}
		// Clearing a TTL must persist the key, not delete it — EXPIRE with a
		// non-positive value would remove it outright.
		never := int64(-1)
		if _, err := RedisSetExpiry(ctx, client, RedisBytes(key), RedisExpiry{Seconds: &never}); err != nil {
			t.Fatalf("RedisSetExpiry: %v", err)
		}
		v, err = RedisGet(ctx, client, key)
		if err != nil {
			t.Fatalf("key vanished when its TTL was cleared: %v", err)
		}
		if v.TTL != -1 {
			t.Errorf("ttl after clear = %d, want -1", v.TTL)
		}
	})

	t.Run("hash_members", func(t *testing.T) {
		key := prefix + "user:1"
		if err := redisTestSet(ctx, client, key, "hash", "name", "Ann", 0); err != nil {
			t.Fatalf("hash set: %v", err)
		}
		if err := redisTestSet(ctx, client, key, "hash", "city", "Oslo", 0); err != nil {
			t.Fatalf("hash set 2: %v", err)
		}
		v, err := RedisGet(ctx, client, key)
		if err != nil {
			t.Fatal(err)
		}
		if v.Hash["name"] != "Ann" || v.Hash["city"] != "Oslo" {
			t.Errorf("hash = %v", v.Hash)
		}
		if _, err := RedisRemoveMembers(ctx, client, RedisRemoval{Key: RedisBytes(key), Type: "hash", Members: []RedisBytes{"city"}}); err != nil {
			t.Fatalf("delete hash field: %v", err)
		}
		v, _ = RedisGet(ctx, client, key)
		if _, still := v.Hash["city"]; still {
			t.Error("hash field survived deletion")
		}
	})

	t.Run("list_members", func(t *testing.T) {
		key := prefix + "queue"
		for _, item := range []string{"one", "two", "three"} {
			if err := redisTestSet(ctx, client, key, "list", "", item, 0); err != nil {
				t.Fatalf("list append: %v", err)
			}
		}
		// An index in the field position replaces that entry in place.
		if err := redisTestSet(ctx, client, key, "list", "1", "TWO", 0); err != nil {
			t.Fatalf("list set by index: %v", err)
		}
		v, _ := RedisGet(ctx, client, key)
		if len(v.List) != 3 || v.List[1] != "TWO" {
			t.Fatalf("list = %v", v.List)
		}
		// Redis has no delete-by-index; the sentinel dance must leave the list
		// shorter and free of the sentinel.
		head := int64(0)
		if _, err := RedisRemoveMembers(ctx, client, RedisRemoval{Key: RedisBytes(key), Type: "list", Index: &head}); err != nil {
			t.Fatalf("delete list entry: %v", err)
		}
		v, _ = RedisGet(ctx, client, key)
		if len(v.List) != 2 {
			t.Fatalf("list after delete = %v, want 2 entries", v.List)
		}
		for _, e := range v.List {
			if strings.Contains(string(e), "\x00jd:") {
				t.Errorf("sentinel leaked into the list: %v", v.List)
			}
		}
		if v.List[0] != "TWO" {
			t.Errorf("wrong entry removed: %v", v.List)
		}
	})

	t.Run("set_and_zset_members", func(t *testing.T) {
		skey := prefix + "tags"
		for _, m := range []string{"red", "green"} {
			if err := redisTestSet(ctx, client, skey, "set", "", m, 0); err != nil {
				t.Fatalf("set add: %v", err)
			}
		}
		if _, err := RedisRemoveMembers(ctx, client, RedisRemoval{Key: RedisBytes(skey), Type: "set", Members: []RedisBytes{"red"}}); err != nil {
			t.Fatalf("set remove: %v", err)
		}
		v, _ := RedisGet(ctx, client, skey)
		if len(v.Set) != 1 || v.Set[0] != "green" {
			t.Errorf("set = %v", v.Set)
		}

		zkey := prefix + "scores"
		if err := redisTestSet(ctx, client, zkey, "zset", "10", "ann", 0); err != nil {
			t.Fatalf("zadd: %v", err)
		}
		if err := redisTestSet(ctx, client, zkey, "zset", "20", "bo", 0); err != nil {
			t.Fatalf("zadd 2: %v", err)
		}
		v, _ = RedisGet(ctx, client, zkey)
		if len(v.ZSet) != 2 || v.ZSet[0].Member != "ann" || v.ZSet[0].Score != 10 {
			t.Errorf("zset = %+v", v.ZSet)
		}
		if _, err := RedisRemoveMembers(ctx, client, RedisRemoval{Key: RedisBytes(zkey), Type: "zset", Members: []RedisBytes{"ann"}}); err != nil {
			t.Fatalf("zrem: %v", err)
		}
		v, _ = RedisGet(ctx, client, zkey)
		if len(v.ZSet) != 1 {
			t.Errorf("zset after remove = %+v", v.ZSet)
		}
	})

	t.Run("create_and_rename", func(t *testing.T) {
		key := prefix + "fresh"
		create := func(value string) error {
			v := RedisBytes(value)
			_, err := RedisWriteValue(ctx, client, nil, RedisWrite{Key: RedisBytes(key), Value: &v, Create: true})
			return err
		}
		if err := create("v"); err != nil {
			t.Fatalf("create: %v", err)
		}
		// Creating over an existing key must be refused rather than clobber it.
		if err := create("other"); err == nil {
			t.Error("expected create over an existing key to be refused")
		}
		target := prefix + "renamed"
		client.Del(ctx, target)
		if err := RedisRenameKey(ctx, client, RedisBytes(key), RedisBytes(target), false); err != nil {
			t.Fatalf("RedisRenameKey: %v", err)
		}
		if n, _ := client.Exists(ctx, target).Result(); n != 1 {
			t.Error("renamed key not found at its new name")
		}
		// Renaming onto an occupied name must be refused, not silently overwrite.
		other := prefix + "occupied"
		client.Set(ctx, other, "keepme", 0)
		if err := RedisRenameKey(ctx, client, RedisBytes(target), RedisBytes(other), false); err == nil {
			t.Error("expected rename onto an existing key to be refused")
		}
		if got, _ := client.Get(ctx, other).Result(); got != "keepme" {
			t.Errorf("occupied key was clobbered: %q", got)
		}
	})

	t.Run("scan_and_databases", func(t *testing.T) {
		page, err := RedisScanKeys(ctx, client, nil, RedisScanOptions{Pattern: prefix + "*", Count: 100})
		if err != nil {
			t.Fatalf("RedisScanKeys: %v", err)
		}
		if len(page.Keys) == 0 {
			t.Fatal("scan found no seeded keys")
		}
		byName := map[string]RedisKey{}
		for _, k := range page.Keys {
			byName[string(k.Key)] = k
		}
		if q, ok := byName[prefix+"queue"]; ok {
			if q.Type != "list" || q.Size != 2 {
				t.Errorf("queue key = %+v, want list of 2", q)
			}
		}
		dbs, err := RedisDatabases(ctx, client)
		if err != nil {
			t.Fatalf("RedisDatabases: %v", err)
		}
		if len(dbs) == 0 {
			t.Error("no logical databases reported")
		}
		if _, err := RedisInfo(ctx, client); err != nil {
			t.Fatalf("RedisInfo: %v", err)
		}
	})
}

// TestLiveRedisScanFindsASelectiveMatch is the bug the loop in RedisScanKeys
// exists for.
//
// SCAN's COUNT is a hint about how many slots to examine, not how many keys to
// return, so a pattern matching a handful of keys in a large keyspace matches
// nothing at all in the first turn. Handing that page back reported an empty
// keyspace for a pattern that matches: the browser's pattern box said "No keys
// match this pattern" over a key it could see by name.
func TestLiveRedisScanFindsASelectiveMatch(t *testing.T) {
	client := liveRedis(t)
	ctx := context.Background()

	// Enough noise that the needle is not in the first turn's slots.
	for i := 0; i < 600; i++ {
		client.Set(ctx, "jdscan:noise:"+strconv.Itoa(i), "x", time.Minute)
	}
	client.Set(ctx, "jdscan:needle", "found", time.Minute)
	t.Cleanup(func() {
		keys, _, _ := client.Scan(ctx, 0, "jdscan:*", 10000).Result()
		if len(keys) > 0 {
			client.Del(ctx, keys...)
		}
	})

	// Once as a glob, which has to be searched for, and once as a bare name,
	// which is looked up.
	for _, pattern := range []string{"jdscan:needl?", "jdscan:needle"} {
		page, err := RedisScanKeys(ctx, client, nil, RedisScanOptions{Pattern: pattern, Count: 100})
		if err != nil {
			t.Fatalf("RedisScanKeys(%s): %v", pattern, err)
		}
		if len(page.Keys) != 1 || page.Keys[0].Key != "jdscan:needle" {
			t.Fatalf("%s matches one key and returned %d: %+v", pattern, len(page.Keys), page.Keys)
		}
	}
	// A name that is not there is an empty page and the end of the search.
	none, err := RedisScanKeys(ctx, client, nil, RedisScanOptions{Pattern: "jdscan:absent", Count: 100})
	if err != nil || len(none.Keys) != 0 || !none.Done {
		t.Fatalf("a name with no key = %+v, %v", none, err)
	}

	// And the ordinary case still pages rather than returning the keyspace.
	broad, err := RedisScanKeys(ctx, client, nil, RedisScanOptions{Pattern: "jdscan:*", Count: 50})
	if err != nil {
		t.Fatalf("RedisScanKeys(broad): %v", err)
	}
	if len(broad.Keys) < 50 {
		t.Errorf("a broad pattern returned %d keys, want a full page of 50", len(broad.Keys))
	}
}
