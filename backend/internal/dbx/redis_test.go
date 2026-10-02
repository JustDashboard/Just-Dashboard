package dbx

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// Pure-logic tests for the Redis layer: everything here runs without a
// server. What needs one is in live_redis_test.go.

func TestRedisBytesKeepsTextAsTextAndBinaryAsBinary(t *testing.T) {
	cases := []struct {
		raw  string
		json string
	}{
		{"hello", `"hello"`},
		{"", `""`},
		{"héllo ✓", `"héllo ✓"`},
		// A NUL is valid UTF-8 and a control character is still text.
		{"a\x00b\n", `"a\u0000b\n"`},
		// These are not text, and replacing them with U+FFFD — which is what
		// encoding a Go string does — is how a saved value came back
		// different from the one that was read.
		{"\xff\x00\xfe", `{"base64":"/wD+"}`},
		{"caf\xe9", `{"base64":"Y2Fm6Q=="}`},
	}
	for _, c := range cases {
		got, err := json.Marshal(RedisBytes(c.raw))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != c.json {
			t.Errorf("marshal %q = %s, want %s", c.raw, got, c.json)
		}
		var back RedisBytes
		if err := json.Unmarshal(got, &back); err != nil {
			t.Fatalf("unmarshal %s: %v", got, err)
		}
		if string(back) != c.raw {
			t.Errorf("round trip of %q gave %q", c.raw, string(back))
		}
	}

	// Text may also arrive wrapped, which is how a page sends bytes it read
	// from a file without caring whether they are text.
	var wrapped RedisBytes
	if err := json.Unmarshal([]byte(`{"base64":"aGVsbG8="}`), &wrapped); err != nil || wrapped != "hello" {
		t.Errorf("wrapped text = %q, %v", wrapped, err)
	}
	for _, bad := range []string{
		`{"base64":"not base64!"}`, `{}`, `{"base64":"aGk=","extra":1}`, `{"text":"hi"}`, `12`, `null`, `["a"]`,
	} {
		var b RedisBytes
		if err := json.Unmarshal([]byte(bad), &b); err == nil {
			t.Errorf("unmarshal %s was accepted as %q", bad, b)
		}
	}
}

// A request has to be able to say "the member whose name is empty", which is
// a different request from one that names no member.
func TestRedisBytesPointerTellsAbsentFromEmpty(t *testing.T) {
	var req struct {
		Member *RedisBytes `json:"member"`
	}
	if err := json.Unmarshal([]byte(`{}`), &req); err != nil || req.Member != nil {
		t.Errorf("an absent member decoded as %v, %v", req.Member, err)
	}
	if err := json.Unmarshal([]byte(`{"member":""}`), &req); err != nil || req.Member == nil || *req.Member != "" {
		t.Errorf("an empty member decoded as %v, %v", req.Member, err)
	}
}

func TestRedisBytesDisplay(t *testing.T) {
	if got := RedisBytes("plain").Display(); got != "plain" {
		t.Errorf("Display(plain) = %q", got)
	}
	if got := RedisBytes("a\xffb").Display(); got != `a\xffb` {
		t.Errorf("Display(binary) = %q", got)
	}
}

func TestRedisScore(t *testing.T) {
	for in, want := range map[float64]string{
		1.5: `1.5`, 0: `0`, -3: `-3`, 1e21: `1e+21`,
		math.Inf(1): `"inf"`, math.Inf(-1): `"-inf"`,
	} {
		got, err := json.Marshal(RedisScore(in))
		if err != nil || string(got) != want {
			t.Errorf("marshal %v = %s, %v; want %s", in, got, err, want)
		}
	}
	for in, want := range map[string]float64{
		`1.5`: 1.5, `"1.5"`: 1.5, `"inf"`: math.Inf(1), `"+inf"`: math.Inf(1), `"-inf"`: math.Inf(-1), `-2`: -2,
	} {
		var s RedisScore
		if err := json.Unmarshal([]byte(in), &s); err != nil || float64(s) != want {
			t.Errorf("unmarshal %s = %v, %v; want %v", in, float64(s), err, want)
		}
	}
	for _, bad := range []string{`"nan"`, `"abc"`, `""`, `true`} {
		var s RedisScore
		if err := json.Unmarshal([]byte(bad), &s); err == nil {
			t.Errorf("score %s was accepted as %v", bad, float64(s))
		}
	}
	if got := formatRedisScore(math.Inf(-1)); got != "-inf" {
		t.Errorf("formatRedisScore(-inf) = %q", got)
	}
}

// A namespace is a literal, and its name may contain what a glob reads as
// syntax.
func TestRedisGlobEscape(t *testing.T) {
	for literal, want := range map[string]string{
		"plain:":      "plain:",
		"cache[v2]:":  `cache\[v2\]:`,
		"a*b?c:":      `a\*b\?c:`,
		`back\slash:`: `back\\slash:`,
	} {
		if got := redisGlobEscape(literal); got != want {
			t.Errorf("redisGlobEscape(%q) = %q, want %q", literal, got, want)
		}
	}
}

func TestParseRedisInfoAndKeyspace(t *testing.T) {
	raw := "# Server\r\nredis_version:7.4.11\r\nredis_mode:standalone\r\n\r\n" +
		"# Keyspace\r\ndb0:keys=2610,expires=1800,avg_ttl=7000926,subexpiry=0\r\n" +
		"db3:keys=4,expires=0,avg_ttl=0\r\n" +
		// Dragonfly's form, with fields the others do not have and no TTL.
		"db7:keys=1,expires=0,hits=0,misses=0,hit_ratio=0.00,avg_ttl=-1\r\n"
	sections := parseRedisInfo(raw)
	if len(sections) != 2 || sections[0].Name != "Server" || sections[1].Name != "Keyspace" {
		t.Fatalf("sections = %+v", sections)
	}
	if got := redisInfoMap(sections)["redis_version"]; got != "7.4.11" {
		t.Errorf("redis_version = %q", got)
	}
	want := []RedisKeyspace{
		{DB: 0, Keys: 2610, Expires: 1800, AvgTTLMs: 7000926},
		{DB: 3, Keys: 4},
		{DB: 7, Keys: 1},
	}
	if got := parseRedisKeyspace(sections); !reflect.DeepEqual(got, want) {
		t.Errorf("keyspace = %+v, want %+v", got, want)
	}
	// A reply with no heading still yields its fields.
	if got := parseRedisInfo("role:master\r\n"); len(got) != 1 || got[0].Fields[0].Value != "master" {
		t.Errorf("headless info = %+v", got)
	}
}

func TestRedisFlavorFromInfo(t *testing.T) {
	cases := []struct {
		info            map[string]string
		flavor, version string
	}{
		{map[string]string{"redis_version": "7.4.11"}, RedisFlavorRedis, "7.4.11"},
		// Both of these report a redis_version for old clients' sake, so that
		// field cannot be what identifies them.
		{map[string]string{"redis_version": "7.2.4", "server_name": "valkey", "valkey_version": "8.1.10"}, RedisFlavorValkey, "8.1.10"},
		{map[string]string{"redis_version": "7.4.0", "dragonfly_version": "df-v2.0.0"}, RedisFlavorDragonfly, "2.0.0"},
		{map[string]string{"redis_version": "6.3.4", "mvcc_depth": "0"}, RedisFlavorKeyDB, "6.3.4"},
		{map[string]string{"redis_version": "6.3.4", "executable": "/data/keydb-server"}, RedisFlavorKeyDB, "6.3.4"},
	}
	for _, c := range cases {
		flavor, version := RedisFlavorFromInfo(c.info)
		if flavor != c.flavor || version != c.version {
			t.Errorf("RedisFlavorFromInfo(%v) = %s %s, want %s %s", c.info, flavor, version, c.flavor, c.version)
		}
	}
}

// With no answer from COMMAND INFO the version decides, and an unreadable
// version decides nothing is there.
func TestRedisFeaturesWithoutACommandTable(t *testing.T) {
	old := &RedisProfile{Flavor: RedisFlavorRedis, Mode: "standalone", compat: redisVersionNumbers("5.0.14")}
	f := old.features()
	if f.ScanType || f.KeepTTL || f.Copy || f.ACL || f.HashFieldTTL || f.CommandDocs || f.Functions {
		t.Errorf("Redis 5 was given features it lacks: %+v", f)
	}
	if !f.MemoryUsage || !f.Unlink || !f.Streams || !f.ObjectEncoding || !f.Slowlog {
		t.Errorf("Redis 5 was denied features it has: %+v", f)
	}
	if f.JSON || f.Search {
		t.Error("a module was assumed present without the server saying so")
	}
	unknown := &RedisProfile{Flavor: RedisFlavorRedis, Mode: "standalone", compat: redisVersionNumbers("unstable")}
	if f := unknown.features(); f.MemoryUsage || f.ScanType || f.KeepTTL {
		t.Errorf("an unreadable version was given features: %+v", f)
	}
}

func TestRedisFeaturesFollowTheServersAnswers(t *testing.T) {
	// Dragonfly: answers to Redis 7.4 and has no OBJECT at all.
	df := &RedisProfile{
		Flavor: RedisFlavorDragonfly, Mode: "standalone", compat: redisVersionNumbers("7.4.0"),
		known: map[string]bool{"memory": true, "httl": true, "config": true, "bgrewriteaof": false},
	}
	f := df.features()
	if f.ObjectEncoding || f.ObjectIdleTime || f.ObjectFreq {
		t.Errorf("OBJECT offered on a server that said it has none: %+v", f)
	}
	if f.CommandDocs || f.AOF || f.HashFieldTTL {
		t.Errorf("Dragonfly was given Redis 7's COMMAND DOCS, an AOF or Redis's HTTL: %+v", f)
	}
	if !f.MemoryUsage {
		t.Error("MEMORY USAGE withheld from a server that has it")
	}

	// Idle time and frequency are one or the other, by eviction policy.
	lfu := &RedisProfile{Flavor: RedisFlavorRedis, Mode: "standalone", compat: [2]int{7, 2},
		known: map[string]bool{"object": true}, policy: "allkeys-lfu"}
	if f := lfu.features(); !f.ObjectFreq || f.ObjectIdleTime {
		t.Errorf("under LFU: freq=%v idle=%v", f.ObjectFreq, f.ObjectIdleTime)
	}
	lru := &RedisProfile{Flavor: RedisFlavorRedis, Mode: "standalone", compat: [2]int{7, 2},
		known: map[string]bool{"object": true}, policy: "allkeys-lru"}
	if f := lru.features(); f.ObjectFreq || !f.ObjectIdleTime {
		t.Errorf("under LRU: freq=%v idle=%v", f.ObjectFreq, f.ObjectIdleTime)
	}

	// A sentinel holds no keys, whatever commands its table lists.
	sentinel := &RedisProfile{Flavor: RedisFlavorRedis, Mode: "sentinel", compat: [2]int{7, 2},
		known: map[string]bool{"memory": true, "scan": true, "client": true}}
	if f := sentinel.features(); f.MemoryUsage || f.ScanType || f.Databases || !f.ClientList {
		t.Errorf("sentinel features = %+v", f)
	}
	cluster := &RedisProfile{Flavor: RedisFlavorRedis, Mode: "cluster", compat: [2]int{7, 2}}
	if cluster.features().Databases {
		t.Error("a cluster node was said to have numbered databases")
	}
}

// Leaving a key's idle time alone and claiming without naming entries are
// each a command some servers lack, decided like the rest: by the server's
// own answer where it gave one, by its release otherwise, and never for a
// fork that answers to a Redis version it is not.
func TestRedisFeaturesForReadsThatDoNotTouchAndForClaims(t *testing.T) {
	for _, c := range []struct {
		name               string
		profile            *RedisProfile
		noTouch, autoClaim bool
	}{
		{"Redis 7.4 by its own answer", &RedisProfile{Flavor: RedisFlavorRedis, Mode: "standalone", compat: [2]int{7, 4},
			known: map[string]bool{"client|no-touch": true, "xautoclaim": true}}, true, true},
		{"Redis 7.0 by its own answer", &RedisProfile{Flavor: RedisFlavorRedis, Mode: "standalone", compat: [2]int{7, 0},
			known: map[string]bool{"client|no-touch": false, "xautoclaim": true}}, false, true},
		{"Redis 7.2 that would not describe its commands", &RedisProfile{Flavor: RedisFlavorRedis, Mode: "standalone", compat: [2]int{7, 2}}, true, true},
		{"Redis 7.0 that would not describe its commands", &RedisProfile{Flavor: RedisFlavorRedis, Mode: "standalone", compat: [2]int{7, 0}}, false, true},
		{"Redis 6.0", &RedisProfile{Flavor: RedisFlavorRedis, Mode: "standalone", compat: [2]int{6, 0}}, false, false},
		{"Valkey 8, which says it is Redis 7.2", &RedisProfile{Flavor: RedisFlavorValkey, Mode: "standalone", compat: [2]int{7, 2},
			known: map[string]bool{"client|no-touch": true, "xautoclaim": true}}, true, true},
		{"KeyDB, a Redis 6.3", &RedisProfile{Flavor: RedisFlavorKeyDB, Mode: "standalone", compat: [2]int{6, 3}}, false, true},
		{"Dragonfly, which says it is Redis 7.4", &RedisProfile{Flavor: RedisFlavorDragonfly, Mode: "standalone", compat: [2]int{7, 4}}, false, true},
		{"a sentinel", &RedisProfile{Flavor: RedisFlavorRedis, Mode: "sentinel", compat: [2]int{7, 4},
			known: map[string]bool{"client|no-touch": true, "xautoclaim": true}}, false, false},
	} {
		if f := c.profile.features(); f.NoTouch != c.noTouch || f.StreamAutoClaim != c.autoClaim {
			t.Errorf("%s: noTouch=%v streamAutoClaim=%v, want %v and %v", c.name, f.NoTouch, f.StreamAutoClaim, c.noTouch, c.autoClaim)
		}
	}
}

// A quiet client asks each of its connections to leave idle times alone. A
// server that has never heard of the command answers with an error, and that
// is not the connection's failure: it is opened, and read as before.
func TestAQuietRedisClientOpensOnAServerWithoutNoTouch(t *testing.T) {
	addr := fakeRedis(t, "*0\r\n")
	client, err := RedisOpen(context.Background(), "redis://"+addr+"/0", RedisOpenOptions{DB: RedisDSNDatabase, Quiet: true})
	if err != nil {
		t.Fatalf("a quiet client was not opened on a server without CLIENT NO-TOUCH: %v", err)
	}
	defer client.Close()
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Errorf("the connection is not usable after the refusal: %v", err)
	}
	// The raw connection a page is read over does the same.
	wire, err := redisDialOptions(context.Background(), client.Options())
	if err != nil {
		t.Fatal(err)
	}
	defer wire.close()
	wire.quiet()
	if reply, err := wire.ask("PING"); err != nil || redisText(reply.generic()) != "PONG" {
		t.Errorf("the raw connection after the refusal: %v %v", reply, err)
	}
}

func TestRedisPersistenceFacts(t *testing.T) {
	redis := redisPersistenceFacts(map[string]string{
		"loading": "0", "rdb_last_save_time": "1790837946", "rdb_changes_since_last_save": "28",
		"rdb_bgsave_in_progress": "1", "rdb_last_bgsave_status": "ok", "rdb_last_bgsave_time_sec": "-1",
		"aof_enabled": "1", "aof_rewrite_in_progress": "0", "aof_current_size": "1024",
	}, &RedisProfile{Features: RedisFeatures{AOF: true}})
	if redis.RDB.LastSaveAt == nil || redis.RDB.LastSaveAt.Unix() != 1790837946 || redis.RDB.ChangesSinceSave != 28 || !redis.RDB.InProgress {
		t.Errorf("rdb = %+v", redis.RDB)
	}
	// -1 is "no save has run", not a duration.
	if redis.RDB.LastDurationSeconds != nil {
		t.Errorf("last duration = %d, want none", *redis.RDB.LastDurationSeconds)
	}
	if !redis.AOF.Enabled || !redis.AOF.Supported || redis.AOF.CurrentSize != 1024 {
		t.Errorf("aof = %+v", redis.AOF)
	}

	// Dragonfly spells the same facts differently and has no AOF.
	df := redisPersistenceFacts(map[string]string{
		"last_success_save": "1790837947", "rdb_changes_since_last_success_save": "2",
		"saving": "0", "last_success_save_duration_sec": "0",
	}, &RedisProfile{})
	if df.RDB.LastSaveAt == nil || df.RDB.ChangesSinceSave != 2 || df.RDB.InProgress || df.AOF.Supported {
		t.Errorf("dragonfly persistence = %+v", df)
	}
}

func TestRedisReplicationFacts(t *testing.T) {
	primary := redisReplicationFacts(map[string]string{
		"role": "master", "connected_slaves": "2", "master_repl_offset": "500",
		"slave0": "ip=10.0.0.5,port=6379,state=online,offset=498,lag=1",
		"slave1": "ip=10.0.0.6,port=6380,state=wait_bgsave,offset=0,lag=0",
	})
	if primary.Role != "primary" || primary.Primary != nil || len(primary.Replicas) != 2 {
		t.Fatalf("primary = %+v", primary)
	}
	if r := primary.Replicas[0]; r.Addr != "10.0.0.5:6379" || r.State != "online" || r.Offset != 498 || r.LagSeconds != 1 {
		t.Errorf("replica 0 = %+v", r)
	}
	replica := redisReplicationFacts(map[string]string{
		"role": "slave", "master_host": "10.0.0.1", "master_port": "6379", "master_link_status": "up",
		"master_last_io_seconds_ago": "3", "slave_repl_offset": "777", "slave_read_only": "1",
	})
	if replica.Role != "replica" || replica.Primary == nil {
		t.Fatalf("replica = %+v", replica)
	}
	if p := replica.Primary; p.Addr != "10.0.0.1:6379" || !p.Up || p.LastIOSecondsAgo != 3 || p.Offset != 777 || !p.ReadOnly {
		t.Errorf("primary link = %+v", p)
	}
}

func TestParseRedisClient(t *testing.T) {
	c, ok := parseRedisClient("id=4 addr=10.0.0.1:37350 laddr=10.0.0.17:6379 fd=10 name=worker age=120 idle=3 flags=N db=2 sub=1 psub=2 ssub=0 multi=-1 qbuf=26 omem=512 tot-mem=37786 cmd=client|list user=app lib-name=go-redis lib-ver=9.22.0")
	if !ok {
		t.Fatal("a client line was not parsed")
	}
	want := RedisClientInfo{
		ID: 4, Addr: "10.0.0.1:37350", LocalAddr: "10.0.0.17:6379", Name: "worker", User: "app",
		AgeSeconds: 120, IdleSeconds: 3, DB: 2, Command: "CLIENT LIST", Flags: "N", Subscriptions: 3,
		OutputMemory: 512, TotalMemory: 37786, Library: "go-redis 9.22.0",
	}
	if c != want {
		t.Errorf("client = %+v\nwant     %+v", c, want)
	}
	if tx, _ := parseRedisClient("id=5 addr=a:1 flags=x multi=3"); !tx.InTransaction {
		t.Error("a client inside MULTI was not marked")
	}
	if _, ok := parseRedisClient(""); ok {
		t.Error("an empty line was parsed as a client")
	}
}

func TestRedisConfigSecretsAndGroups(t *testing.T) {
	for _, name := range []string{"requirepass", "masterauth", "tls-key-file-pass", "tls-client-key-file-pass", "REQUIREPASS", "some_module_password", "api-token"} {
		if !RedisConfigSecret(name) {
			t.Errorf("%s is not treated as a secret", name)
		}
	}
	for _, name := range []string{"maxmemory", "dir", "bind", "masteruser", "appendonly"} {
		if RedisConfigSecret(name) {
			t.Errorf("%s is treated as a secret", name)
		}
	}
	for name, group := range map[string]string{
		"maxmemory-policy": "memory", "save": "persistence", "appendonly": "persistence",
		"repl-backlog-size": "replication", "requirepass": "security", "bind": "network",
		"slowlog-log-slower-than": "logging", "hash-max-listpack-entries": "data types",
		"cluster-node-timeout": "cluster", "databases": "general", "something-new": "other",
		// Dragonfly's underscores read as the dashes every other flavour uses.
		"slowlog_max_len": "logging", "tls_key_file": "security",
	} {
		if got := redisConfigGroup(name); got != group {
			t.Errorf("redisConfigGroup(%s) = %s, want %s", name, got, group)
		}
	}
}

func TestRedisTreeGroupsByDelimiter(t *testing.T) {
	root := newRedisTreeNode()
	keys := map[string]string{
		"user:1:profile": "hash", "user:1:sessions": "set", "user:2:profile": "hash",
		"user:count": "string", "cache:home": "string", "solo": "string",
		// A key that ends in the delimiter sits directly in its namespace
		// under an empty name; it is a key, not another namespace.
		"queue:": "list",
	}
	for k, typ := range keys {
		root.add(strings.Split(k, ":"), typ, 2)
	}
	if root.count != 7 || root.keyCount != 1 {
		t.Fatalf("root count=%d keyCount=%d, want 7 and 1", root.count, root.keyCount)
	}
	folders, omitted := root.folders("", ":", 2)
	if omitted != 0 || len(folders) != 3 {
		t.Fatalf("folders = %+v (omitted %d)", folders, omitted)
	}
	// Largest first, then by name.
	if folders[0].Name != "user" || folders[1].Name != "cache" || folders[2].Name != "queue" {
		t.Errorf("order = %s, %s, %s", folders[0].Name, folders[1].Name, folders[2].Name)
	}
	user := folders[0]
	if user.Prefix != "user:" || user.Pattern != "user:*" || user.Count != 4 || user.KeyCount != 1 || user.Folders != 2 {
		t.Errorf("user = %+v", user)
	}
	if user.Types["hash"] != 2 || user.Types["set"] != 1 || user.Types["string"] != 1 {
		t.Errorf("user types = %v", user.Types)
	}
	if len(user.Children) != 2 || user.Children[0].Prefix != "user:1:" || user.Children[0].Count != 2 || user.Children[0].KeyCount != 2 {
		t.Errorf("user children = %+v", user.Children)
	}
	if q := folders[2]; q.Count != 1 || q.KeyCount != 1 || q.Folders != 0 {
		t.Errorf("queue = %+v", q)
	}

	// One level asked for: no children, but each folder still knows whether
	// there is anything inside to open.
	shallow := newRedisTreeNode()
	for k, typ := range keys {
		shallow.add(strings.Split(k, ":"), typ, 1)
	}
	top, _ := shallow.folders("", ":", 1)
	if top[0].Children != nil || top[0].Folders != 2 {
		t.Errorf("at depth 1 user = %+v", top[0])
	}

	// A namespace whose name is a glob has to list its own keys and nothing
	// else.
	odd := newRedisTreeNode()
	odd.add([]string{"cache[v2]", "k"}, "string", 1)
	f, _ := odd.folders("", ":", 1)
	if string(f[0].Pattern) != `cache\[v2\]:*` {
		t.Errorf("pattern for a bracketed namespace = %q", f[0].Pattern)
	}
}

func TestRedisTrimPartialRune(t *testing.T) {
	whole := "héllo ✓"
	for cut := 1; cut < len(whole); cut++ {
		got := redisTrimPartialRune(whole[:cut])
		if !strings.HasPrefix(whole, got) || len(got) > cut || cut-len(got) > 3 {
			t.Errorf("cut at %d gave %q", cut, got)
		}
		if RedisBytes(got).Binary() {
			t.Errorf("cut at %d left a fragment: %q", cut, got)
		}
	}
	// Bytes that are not UTF-8 to begin with have nothing to complete and are
	// left exactly as they are.
	for _, raw := range []string{"\xff\xfe\xfd", "abc\x80", "\x80\x80\x80\x80"} {
		if got := redisTrimPartialRune(raw); got != raw {
			t.Errorf("binary %q was trimmed to %q", raw, got)
		}
	}
}

func TestRedisStreamNeighbour(t *testing.T) {
	cases := []struct {
		id     string
		before bool
		want   string
		ok     bool
	}{
		{"1700000000000-0", false, "1700000000000-1", true},
		{"1700000000000-5", true, "1700000000000-4", true},
		{"1700000000000-0", true, "1699999999999-18446744073709551615", true},
		{"5-18446744073709551615", false, "6-0", true},
		{"0-0", true, "", false},
		{"18446744073709551615-18446744073709551615", false, "", false},
		{"5", false, "5-1", true},
		{"junk", false, "", false},
	}
	for _, c := range cases {
		got, ok := redisStreamNeighbour(c.id, c.before)
		if got != c.want || ok != c.ok {
			t.Errorf("redisStreamNeighbour(%q, before=%v) = %q %v, want %q %v", c.id, c.before, got, ok, c.want, c.ok)
		}
	}
}

func TestRedisStreamEntriesKeepOrderAndDuplicates(t *testing.T) {
	// XRANGE's reply: two entries, the first with a field name used twice,
	// the second with no fields at all.
	reply, _, _, err := readWire("*2\r\n" +
		"*2\r\n$3\r\n1-1\r\n*6\r\n$1\r\nb\r\n$1\r\n2\r\n$1\r\na\r\n$1\r\n1\r\n$1\r\nb\r\n$1\r\n3\r\n" +
		"*2\r\n$3\r\n1-2\r\n*0\r\n")
	if err != nil {
		t.Fatal(err)
	}
	rows := redisStreamEntries(reply)
	if len(rows) != 2 || rows[0].ID != "1-1" || rows[1].ID != "1-2" {
		t.Fatalf("rows = %+v", rows)
	}
	want := [][2]RedisBytes{{"b", "2"}, {"a", "1"}, {"b", "3"}}
	if !reflect.DeepEqual(rows[0].Fields, want) {
		t.Errorf("fields = %v, want %v", rows[0].Fields, want)
	}
	if rows[1].Fields == nil || len(rows[1].Fields) != 0 {
		t.Errorf("an entry with no fields = %v, want an empty list", rows[1].Fields)
	}
	if rows[0].Truncated || rows[1].Truncated {
		t.Error("a small entry was marked as cut")
	}
}

func TestRedisExpiryBand(t *testing.T) {
	ms := func(d time.Duration) int64 { return d.Milliseconds() }
	for pttl, want := range map[int64]string{
		-1: "none", 0: "hour", ms(59 * time.Minute): "hour", ms(time.Hour): "day",
		ms(23 * time.Hour): "day", ms(24 * time.Hour): "week", ms(6 * 24 * time.Hour): "week",
		ms(7 * 24 * time.Hour): "later", ms(400 * 24 * time.Hour): "later",
	} {
		if got := redisExpiryBand(pttl); got != want {
			t.Errorf("redisExpiryBand(%d) = %s, want %s", pttl, got, want)
		}
	}
}

func TestRedisWriteNormalise(t *testing.T) {
	b := func(s string) *RedisBytes { v := RedisBytes(s); return &v }
	// The older request form: a list position and a sorted-set score both
	// arrived in "field".
	list := RedisWrite{Type: "list", Value: b("x"), Field: b("3")}
	if err := list.normalise(); err != nil || list.Index == nil || *list.Index != 3 {
		t.Errorf("list field as position: %+v, %v", list.Index, err)
	}
	zset := RedisWrite{Type: "zset", Value: b("m"), Field: b("-inf")}
	if err := zset.normalise(); err != nil || zset.Score == nil || !math.IsInf(float64(*zset.Score), -1) {
		t.Errorf("zset field as score: %v, %v", zset.Score, err)
	}
	untyped := RedisWrite{Value: b("v")}
	if err := untyped.normalise(); err != nil || untyped.Type != "string" {
		t.Errorf("no type = %q, %v", untyped.Type, err)
	}
	// An empty hash field name is a name; a missing one is not.
	if err := (&RedisWrite{Type: "hash", Value: b("v"), Field: b("")}).normalise(); err != nil {
		t.Errorf("the empty hash field was refused: %v", err)
	}
	stream := RedisWrite{Type: "stream", Entries: [][2]RedisBytes{{"f", "v"}}}
	if err := stream.normalise(); err != nil || stream.ID != "*" {
		t.Errorf("stream id = %q, %v", stream.ID, err)
	}
	for name, w := range map[string]RedisWrite{
		"a string with no value":       {Type: "string"},
		"a hash with no field":         {Type: "hash", Value: b("v")},
		"a list position that is text": {Type: "list", Value: b("v"), Field: b("first")},
		"a zset with no score":         {Type: "zset", Value: b("m")},
		"a zset score that is text":    {Type: "zset", Value: b("m"), Field: b("high")},
		"a stream with no fields":      {Type: "stream"},
		"a stream id that is a word":   {Type: "stream", ID: "now", Entries: [][2]RedisBytes{{"f", "v"}}},
		"JSON that is not JSON":        {Type: "json", Value: b("{nope")},
		"a type that does not exist":   {Type: "bitmap", Value: b("v")},
	} {
		if err := w.normalise(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestParseRedisACLNeverKeepsAPassword(t *testing.T) {
	const hash = "#5e884898da28047151d0e56f8dc6292773603d0d6aabbdd62a11ef721d1542d8"
	u, ok := parseRedisACL("user app on " + hash + " sanitize-payload ~app:* %R~cache:* resetchannels &events.* -@all +@read +config|get -keys (~tmp:* +get)")
	if !ok {
		t.Fatal("a user line was not parsed")
	}
	if u.Name != "app" || !u.Enabled || u.NoPassword || u.Passwords != 1 || u.System || u.Unrestricted {
		t.Errorf("user = %+v", u)
	}
	if !reflect.DeepEqual(u.Keys, []string{"~app:*", "%R~cache:*"}) {
		t.Errorf("keys = %v", u.Keys)
	}
	if !reflect.DeepEqual(u.Channels, []string{"resetchannels", "&events.*"}) {
		t.Errorf("channels = %v", u.Channels)
	}
	if !reflect.DeepEqual(u.Commands, []string{"-@all", "+@read", "+config|get", "-keys"}) {
		t.Errorf("commands = %v", u.Commands)
	}
	if !reflect.DeepEqual(u.Selectors, []string{"(~tmp:* +get)"}) || !reflect.DeepEqual(u.Flags, []string{"sanitize-payload"}) {
		t.Errorf("selectors = %v, flags = %v", u.Selectors, u.Flags)
	}
	raw, _ := json.Marshal(u)
	if strings.Contains(string(raw), "5e884898") {
		t.Errorf("the password hash reached the page: %s", raw)
	}

	def, _ := parseRedisACL("user default on nopass ~* &* +@all")
	if !def.System || !def.NoPassword || !def.Unrestricted || def.Passwords != 0 {
		t.Errorf("default = %+v", def)
	}
	// The rule is read left to right: a restriction after +@all restricts.
	narrowed, _ := parseRedisACL("user ops on #abc ~* +@all -@dangerous")
	if narrowed.Unrestricted {
		t.Error("+@all -@dangerous was reported as unrestricted")
	}
	widened, _ := parseRedisACL("user ops on #abc allkeys -@dangerous +@all")
	if !widened.Unrestricted {
		t.Error("-@dangerous +@all was not reported as unrestricted")
	}
	off, _ := parseRedisACL("user old off #abc ~* +@all")
	if off.Enabled {
		t.Error("a user that is off was reported enabled")
	}
	if _, ok := parseRedisACL("not a user line"); ok {
		t.Error("a line that is not a user was parsed as one")
	}
}

func TestRedisACLSpecRules(t *testing.T) {
	on, pw := true, "s3cret pass"
	spec := RedisACLSpec{
		Name: "app", Enabled: &on, Password: &pw,
		Keys:     &[]string{"app:*", "~cache:*", "%R~ro:*", "allkeys"},
		Channels: &[]string{"events.*", "&alerts"},
		Commands: &[]string{"+@read", "-@dangerous", "+CONFIG|GET", "allcommands"},
	}
	got, err := spec.rules()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"on", "resetpass", ">s3cret pass",
		"resetkeys", "~app:*", "~cache:*", "%R~ro:*", "~*",
		"resetchannels", "&events.*", "&alerts",
		"-@all", "+@read", "-@dangerous", "+config|get", "+@all",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rules =\n%q\nwant\n%q", got, want)
	}

	// Nothing given, nothing changed: a page can alter one part of a rule
	// without resetting the rest.
	if got, err := (RedisACLSpec{Name: "app"}).rules(); err != nil || len(got) != 0 {
		t.Errorf("an empty spec = %q, %v", got, err)
	}
	empty := []string{}
	if got, _ := (RedisACLSpec{Name: "app", Commands: &empty}).rules(); !reflect.DeepEqual(got, []string{"-@all"}) {
		t.Errorf("an empty command list = %q, want every command revoked", got)
	}

	bad := "x"
	for name, s := range map[string]RedisACLSpec{
		"a command rule with a space":    {Commands: &[]string{"+get +set"}},
		"a command rule with no sign":    {Commands: &[]string{"get"}},
		"a password rule among commands": {Commands: &[]string{">hunter2"}},
		"a key pattern with a space":     {Keys: &[]string{"a b"}},
		"a key pattern with a newline":   {Keys: &[]string{"a\nb"}},
		"an empty channel":               {Channels: &[]string{""}},
		"a password and no password":     {Password: &bad, NoPassword: true},
		"a password with a newline":      {Password: func() *string { s := "a\nb"; return &s }()},
	} {
		if got, err := s.rules(); err == nil {
			t.Errorf("%s was accepted: %q", name, got)
		}
	}
	for _, name := range []string{"", "has space", "new\nline", strings.Repeat("x", 129)} {
		if err := redisACLName(name); err == nil {
			t.Errorf("user name %q was accepted", name)
		}
	}
}

func TestParseRedisMonitorLine(t *testing.T) {
	ev, ok := parseRedisMonitorLine(`+1339518083.107412 [3 127.0.0.1:60866] "set" "k" "two words" "\xff\x00" "a\"b"`, false)
	if !ok {
		t.Fatal("a monitor line was not parsed")
	}
	if ev.At != 1339518083.107412 || ev.DB != 3 || ev.Client != "127.0.0.1:60866" || ev.Command != "SET" {
		t.Errorf("event = %+v", ev)
	}
	if !reflect.DeepEqual(ev.Args, []string{"k", "two words", `\xff\x00`, `a\"b`}) {
		t.Errorf("args = %q", ev.Args)
	}
	lua, ok := parseRedisMonitorLine(`+1339518083.5 [0 lua] "get" "k"`, false)
	if !ok || lua.Client != "lua" {
		t.Errorf("a script's command = %+v, %v", lua, ok)
	}
	// A long argument is cut and says how long it was.
	long, ok := parseRedisMonitorLine(`+1.5 [0 a:1] "set" "k" "`+strings.Repeat("v", 2000)+`"`, false)
	if !ok || len(long.Args[1]) > redisMonitorMaxArg+40 || !strings.Contains(long.Args[1], "2000 bytes") {
		t.Errorf("long argument = %d bytes: %.60q", len(long.Args[1]), long.Args[1])
	}
	// A line cut short by the reader still yields what came before the cut,
	// and marks the argument it was cut in without claiming to know its size.
	cut, ok := parseRedisMonitorLine(`+1.5 [0 a:1] "set" "k" "unfinis`, true)
	if !ok || cut.Command != "SET" || len(cut.Args) != 2 || cut.Args[1] != "unfinis…" {
		t.Errorf("a truncated line = %+v, %v", cut, ok)
	}
	// Cut immediately after a backslash, where one closing quote would be
	// read as escaped.
	if cut, ok := parseRedisMonitorLine(`+1.5 [0 a:1] "set" "k" "half\`, true); !ok || len(cut.Args) != 2 {
		t.Errorf("a line cut after a backslash = %+v, %v", cut, ok)
	}
	for _, junk := range []string{"", "OK", "+OK", "-ERR nope", "+notatime [0 a:1] \"x\""} {
		if _, ok := parseRedisMonitorLine(junk, false); ok {
			t.Errorf("%q was parsed as a command", junk)
		}
	}
}

func TestRedisWireHelpers(t *testing.T) {
	if got := string(redisEncodeCommand([]string{"AUTH", "p w"})); got != "*2\r\n$4\r\nAUTH\r\n$3\r\np w\r\n" {
		t.Errorf("encoded command = %q", got)
	}
	// A line longer than the limit is kept up to it and the rest discarded,
	// leaving the reader at the start of the next line.
	rd := bufio.NewReaderSize(strings.NewReader("+"+strings.Repeat("a", 5000)+"\r\n+next\r\n"), 16)
	line, truncated, err := redisReadLine(rd, 100)
	if err != nil || !truncated || len(line) != 100 {
		t.Fatalf("long line: %d bytes truncated=%v err=%v", len(line), truncated, err)
	}
	line, truncated, err = redisReadLine(rd, 100)
	if err != nil || truncated || line != "+next" {
		t.Errorf("line after a long one = %q truncated=%v err=%v", line, truncated, err)
	}
	if _, _, err := redisReadLine(rd, 100); err == nil {
		t.Error("reading past the end returned no error")
	}
}

// An expiry reaches the server as the number it was given, and only when the
// moment it names is one every server can hold.
func TestRedisExpiryRange(t *testing.T) {
	now := time.UnixMilli(1_760_000_000_000)
	room := redisMaxExpiryMs - now.UnixMilli()
	cases := []struct {
		n, unitMs int64
		want      bool
	}{
		{1, 1000, true},
		{60, 1000, true},
		// The year 9999, in seconds and in milliseconds from now.
		{251_642_300_800, 1000, true},
		{251_642_300_800_000, 1, true},
		// What used to wrap a time.Duration and delete the key.
		{9_223_372_037, 1000, true},
		{9_223_372_036_855, 1, true},
		// The last moment allowed, and the first one that is not.
		{room, 1, true},
		{room + 1, 1, false},
		{room / 1000, 1000, true},
		{room/1000 + 1, 1000, false},
		{math.MaxInt64, 1000, false},
		{math.MaxInt64, 1, false},
		// Not an expiry at all; what a caller does with it is its own rule.
		{0, 1000, true},
		{-5, 1, true},
	}
	for _, c := range cases {
		if got := redisExpiryInRange(c.n, c.unitMs, now); got != c.want {
			t.Errorf("redisExpiryInRange(%d, %d) = %v, want %v", c.n, c.unitMs, got, c.want)
		}
	}

	far, near := int64(math.MaxInt64/1000), int64(9_223_372_037)
	for _, typ := range []string{"string", "hash", "set", "stream"} {
		w := RedisWrite{Key: "k", Type: typ, Value: rb("v"), Field: rb("f"), TTL: &far, Entries: [][2]RedisBytes{{"f", "v"}}}
		if err := w.normalise(); !errors.Is(err, errRedisExpiryTooFar) {
			t.Errorf("a %s write with an expiry past what a server holds: %v", typ, err)
		}
		w.TTL = &near
		if err := w.normalise(); err != nil {
			t.Errorf("a %s write expiring in 292 years: %v", typ, err)
		}
	}
}

// The number a TTL reply carries is the number reported: a time.Duration in
// between holds 292 years and turns anything longer into a negative one.
func TestRedisTTLKeepsTheServersNumber(t *testing.T) {
	ctx := context.Background()
	for _, n := range []int64{-2, -1, 0, 1, 86400, 9_223_372_037, 251_642_300_800_000, math.MaxInt64} {
		cmd := redis.NewIntCmd(ctx, "pttl", "k")
		cmd.SetVal(n)
		if got := redisTTL(cmd); got != n {
			t.Errorf("redisTTL(%d) = %d", n, got)
		}
	}
	failed := redis.NewIntCmd(ctx, "pttl", "k")
	failed.SetErr(errors.New("connection reset"))
	if got := redisTTL(failed); got != -2 {
		t.Errorf("a reply that could not be read = %d, want -2", got)
	}
}

// The dashboard's own account keeps every command on every key. Anything
// less, and the command that would give them back is among what it lost.
func TestRedisACLSpecNarrowsSelf(t *testing.T) {
	on, off := true, false
	password := "a-long-enough-password"
	list := func(items ...string) *[]string { return &items }
	cases := []struct {
		name   string
		spec   RedisACLSpec
		refuse bool
	}{
		{"switched off", RedisACLSpec{Enabled: &off}, true},
		{"switched on", RedisACLSpec{Enabled: &on}, false},
		{"no commands at all", RedisACLSpec{Commands: list()}, true},
		{"every command but the dangerous ones", RedisACLSpec{Commands: list("+@all", "-@dangerous")}, true},
		{"reads only", RedisACLSpec{Commands: list("+@read")}, true},
		{"every command", RedisACLSpec{Commands: list("+@all")}, false},
		{"every command, by its other name", RedisACLSpec{Commands: list("allcommands")}, false},
		{"taken away and given back", RedisACLSpec{Commands: list("-@dangerous", "+@all")}, false},
		{"no keys", RedisACLSpec{Keys: list()}, true},
		{"some keys", RedisACLSpec{Keys: list("app:*")}, true},
		{"every key", RedisACLSpec{Keys: list("*")}, false},
		{"every key, by its other name", RedisACLSpec{Keys: list("allkeys")}, false},
		{"every key among others", RedisACLSpec{Keys: list("app:*", "~*")}, false},
		{"every command on some keys", RedisACLSpec{Commands: list("+@all"), Keys: list("app:*")}, true},
		// Neither is access to keys or commands.
		{"a new password", RedisACLSpec{Password: &password}, false},
		{"other channels", RedisACLSpec{Channels: list("events.*")}, false},
		{"a new password and fewer commands", RedisACLSpec{Password: &password, Commands: list("+@read")}, true},
	}
	for _, c := range cases {
		c.spec.Name = "dashboard"
		why := c.spec.narrowsSelf()
		if (why != "") != c.refuse {
			t.Errorf("%s: narrowsSelf() = %q, want refused=%v", c.name, why, c.refuse)
		}
		if strings.Contains(why, password) {
			t.Errorf("%s: the refusal carries the password", c.name)
		}
	}
}
