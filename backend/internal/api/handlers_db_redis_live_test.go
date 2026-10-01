package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

// The Redis routes end to end, against a real server. The dbx live tests
// prove the engine layer; these prove the handlers are wired to it, decide
// the right capability from a request's content, and keep values off the
// audit trail.
//
// JD_TEST_REDIS_DSN is the shared fixture: only keys under redisAPIPrefix in
// the database its connection string names are written. JD_TEST_REDIS_ADMIN_DSN
// is a server this run owns, for the routes that change the server itself.
// Neither has a default.

const redisAPIPrefix = "jdb4api:"

// redisLive is redisRouter against a server that is really there, with this
// test's keys removed before and after.
func redisLive(t *testing.T, env string, role auth.Role) (*Server, http.Handler, int64, *redis.Client) {
	t.Helper()
	dsn := os.Getenv(env)
	if dsn == "" {
		t.Skipf("set %s to run this", env)
	}
	direct, err := dbx.RedisClient(context.Background(), dsn, dbx.RedisDSNDatabase)
	if err != nil {
		t.Skipf("Redis unreachable at %s (%v)", env, err)
	}
	clean := func() {
		ctx := context.Background()
		var cursor uint64
		for {
			keys, next, err := direct.Scan(ctx, cursor, redisAPIPrefix+"*", 1000).Result()
			if err != nil {
				return
			}
			if len(keys) > 0 {
				direct.Del(ctx, keys...)
			}
			if cursor = next; cursor == 0 {
				return
			}
		}
	}
	clean()
	t.Cleanup(func() {
		clean()
		direct.Close()
	})
	s, h, id := redisRouter(t, role, dsn)
	return s, h, id, direct
}

// call sends one request and decodes the reply into out when it is given.
func call(t *testing.T, h http.Handler, id int64, method, path, body string, out any) *httptest.ResponseRecorder {
	t.Helper()
	rec := do(t, h, method, pathf("/databases/%d", id)+path, body)
	if out != nil && rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("%s %s: %v in %s", method, path, err, rec.Body.String())
		}
	}
	return rec
}

func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, status int, code, what string) {
	t.Helper()
	if rec.Code != status || (code != "" && redisErrorCode(rec) != code) {
		t.Errorf("%s: %d %s, want %d %s", what, rec.Code, strings.TrimSpace(rec.Body.String()), status, code)
	}
}

// auditTrail is everything recorded so far, as one string to search.
func auditTrail(t *testing.T, s *Server) string {
	t.Helper()
	rows, err := s.Store.DB.Query(`SELECT action, target, detail, status FROM audit_log ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out strings.Builder
	for rows.Next() {
		var action, target, detail string
		var status int
		if err := rows.Scan(&action, &target, &detail, &status); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&out, "%s %s %d %s\n", action, target, status, detail)
	}
	return out.String()
}

func TestLiveAPIRedisDatabaseDefaultsToTheConnectionStrings(t *testing.T) {
	_, h, id, direct := redisLive(t, "JD_TEST_REDIS_DSN", auth.RoleAdmin)
	own := direct.Options().DB
	if own == 0 {
		t.Skip("the connection string names database 0, so there is nothing to tell apart")
	}
	key := redisAPIPrefix + "where"
	wantStatus(t, call(t, h, id, http.MethodPost, "/keys/value", `{"key":"`+key+`","value":"here"}`, nil), 200, "", "write with no db")
	if direct.Exists(context.Background(), key).Val() != 1 {
		t.Fatal("a write with no ?db= did not land in the connection string's database")
	}

	var page dbx.RedisPage
	call(t, h, id, http.MethodGet, "/keys?pattern="+redisAPIPrefix+"*", "", &page)
	if page.DB != own || len(page.Keys) != 1 {
		t.Errorf("a listing with no ?db= read database %d and found %d keys, want database %d and the key", page.DB, len(page.Keys), own)
	}
	// db=0 is database 0, which this test only reads: the key is not there.
	var zero dbx.RedisPage
	call(t, h, id, http.MethodGet, "/keys?db=0&pattern="+redisAPIPrefix+"*", "", &zero)
	if zero.DB != 0 || len(zero.Keys) != 0 {
		t.Errorf("db=0 read database %d and found %d keys", zero.DB, len(zero.Keys))
	}
	var named dbx.RedisPage
	call(t, h, id, http.MethodGet, fmt.Sprintf("/keys?db=%d&pattern=%s*", own, redisAPIPrefix), "", &named)
	if named.DB != own || len(named.Keys) != 1 {
		t.Errorf("db=%d found %d keys", own, len(named.Keys))
	}
	var server struct {
		DB int `json:"db"`
	}
	call(t, h, id, http.MethodGet, "/redis/server", "", &server)
	if server.DB != own {
		t.Errorf("/redis/server says the connection's database is %d, want %d", server.DB, own)
	}
	// It is where a picker starts, so it does not follow the picker.
	server.DB = -1
	call(t, h, id, http.MethodGet, "/redis/server?db=0", "", &server)
	if server.DB != own {
		t.Errorf("/redis/server?db=0 says the connection's database is %d, want %d", server.DB, own)
	}
	// A database the server does not have is a fault in the request, not a
	// server that cannot be reached.
	wantStatus(t, call(t, h, id, http.MethodGet, "/keys?db=9999", "", nil), 400, "bad_request", "listing database 9999")
	wantStatus(t, call(t, h, id, http.MethodPost, "/redis/command", `{"command":"PING","db":9999}`, nil), 400, "bad_request", "a console on database 9999")
	wantStatus(t, call(t, h, id, http.MethodPost, "/redis/command?db=9999", `{"command":"PING"}`, nil), 400, "bad_request", "a console on ?db=9999")
	var result dbx.RedisCommandResult
	call(t, h, id, http.MethodPost, "/redis/command", `{"command":"EXISTS `+key+`"}`, &result)
	if result.DB != own || result.Reply.Value != float64(1) {
		t.Errorf("the console with no db ran in database %d and answered %+v", result.DB, result.Reply)
	}
	call(t, h, id, http.MethodPost, "/redis/command", `{"command":"EXISTS `+key+`","db":0}`, &result)
	if result.DB != 0 || result.Reply.Value != float64(0) {
		t.Errorf("the console with db 0 ran in database %d and answered %+v", result.DB, result.Reply)
	}
}

func TestLiveAPIRedisKeys(t *testing.T) {
	_, h, id, direct := redisLive(t, "JD_TEST_REDIS_DSN", auth.RoleAdmin)
	ctx := context.Background()
	p := redisAPIPrefix

	t.Run("a string save keeps its expiry", func(t *testing.T) {
		wantStatus(t, call(t, h, id, http.MethodPost, "/keys/value", `{"key":"`+p+`s","value":"one","ttl":300,"create":true}`, nil), 200, "", "create")
		wantStatus(t, call(t, h, id, http.MethodPost, "/keys/value", `{"key":"`+p+`s","type":"string","value":"two"}`, nil), 200, "", "save")
		if ttl := direct.TTL(ctx, p+"s").Val(); ttl <= 0 {
			t.Errorf("ttl after a save with no ttl = %v", ttl)
		}
		wantStatus(t, call(t, h, id, http.MethodPost, "/keys/value", `{"key":"`+p+`s","value":"again","create":true}`, nil), 409, "key_exists", "create over an existing key")
		var out struct {
			PTTL int64 `json:"pttl"`
		}
		call(t, h, id, http.MethodPost, "/keys/persist", `{"key":"`+p+`s"}`, &out)
		if out.PTTL != -1 || direct.TTL(ctx, p+"s").Val() != -1 {
			t.Errorf("persist left pttl %d", out.PTTL)
		}
		call(t, h, id, http.MethodPost, "/keys/expire", `{"key":"`+p+`s","ttlMs":90000}`, &out)
		if out.PTTL <= 0 || out.PTTL > 90000 {
			t.Errorf("expire in milliseconds left pttl %d", out.PTTL)
		}
		wantStatus(t, call(t, h, id, http.MethodPost, "/keys/expire", `{"key":"`+p+`gone","ttl":5}`, nil), 404, "key_not_found", "expire on a missing key")
	})

	t.Run("the empty member is a member", func(t *testing.T) {
		direct.HSet(ctx, p+"h", "", "nameless", "kept", "v")
		var out struct {
			Removed int64 `json:"removed"`
		}
		// Present and empty: remove that one field.
		wantStatus(t, call(t, h, id, http.MethodDelete, "/keys", `{"key":"`+p+`h","type":"hash","member":""}`, &out), 200, "", "remove the empty field")
		if out.Removed != 1 || direct.HExists(ctx, p+"h", "").Val() || !direct.HExists(ctx, p+"h", "kept").Val() {
			t.Fatalf("removing the empty field removed %d and left %v", out.Removed, direct.HGetAll(ctx, p+"h").Val())
		}
		// Absent: remove the key, as it always did.
		wantStatus(t, call(t, h, id, http.MethodDelete, "/keys", `{"key":"`+p+`h"}`, &out), 200, "", "remove the key")
		if out.Removed != 1 || direct.Exists(ctx, p+"h").Val() != 0 {
			t.Errorf("removing the key removed %d", out.Removed)
		}
		direct.SAdd(ctx, p+"set", "a", "b", "c", "")
		call(t, h, id, http.MethodDelete, "/keys", `{"key":"`+p+`set","members":["a","","nope"]}`, &out)
		if out.Removed != 2 || direct.SCard(ctx, p+"set").Val() != 2 {
			t.Errorf("removing three members, two of them there, removed %d", out.Removed)
		}
	})

	t.Run("bytes that are not text survive", func(t *testing.T) {
		raw := "\xff\x00\xfe"
		b64 := base64.StdEncoding.EncodeToString([]byte(raw))
		keyB64 := base64.StdEncoding.EncodeToString([]byte(p + raw))
		wantStatus(t, call(t, h, id, http.MethodPost, "/keys/value",
			`{"key":{"base64":"`+keyB64+`"},"type":"hash","field":{"base64":"`+b64+`"},"value":{"base64":"`+b64+`"}}`, nil), 200, "", "binary write")
		if got := direct.HGet(ctx, p+raw, raw).Val(); got != raw {
			t.Fatalf("the server holds %q", got)
		}
		rec := call(t, h, id, http.MethodGet, "/keys/members?keyB64="+url.QueryEscape(keyB64), "", nil)
		want := `{"field":{"base64":"` + b64 + `"},"value":{"base64":"` + b64 + `"}`
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), want) || !strings.Contains(rec.Body.String(), `"key":{"base64":"`+keyB64+`"}`) {
			t.Errorf("binary read = %d %s", rec.Code, rec.Body.String())
		}
		// Text is still plain text, with no wrapper.
		direct.Set(ctx, p+"plain", "héllo", 0)
		rec = call(t, h, id, http.MethodGet, "/keys/members?key="+p+"plain", "", nil)
		if !strings.Contains(rec.Body.String(), `"string":{"value":"héllo","offset":0}`) {
			t.Errorf("text read = %s", rec.Body.String())
		}
		// The listing carries the binary name the same way.
		rec = call(t, h, id, http.MethodGet, "/keys?pattern="+p+"*", "", nil)
		if !strings.Contains(rec.Body.String(), `{"base64":"`+keyB64+`"}`) {
			t.Errorf("the listing lost the binary key name: %s", rec.Body.String())
		}
		wantStatus(t, call(t, h, id, http.MethodDelete, "/keys", `{"keys":[{"base64":"`+keyB64+`"}]}`, nil), 200, "", "delete by binary name")
		if direct.Exists(ctx, p+raw).Val() != 0 {
			t.Error("the binary-named key is still there")
		}
	})

	t.Run("a list insert does not overwrite", func(t *testing.T) {
		for _, body := range []string{
			`{"key":"` + p + `l","type":"list","value":"a"}`,
			`{"key":"` + p + `l","type":"list","value":"c"}`,
			`{"key":"` + p + `l","type":"list","value":"b","index":1,"insert":true}`,
			`{"key":"` + p + `l","type":"list","value":"first","position":"head"}`,
			// The older form: a position in "field" replaces what is there.
			`{"key":"` + p + `l","type":"list","field":"3","value":"C"}`,
		} {
			wantStatus(t, call(t, h, id, http.MethodPost, "/keys/value", body, nil), 200, "", body)
		}
		if got := strings.Join(direct.LRange(ctx, p+"l", 0, -1).Val(), ","); got != "first,a,b,C" {
			t.Fatalf("list = %s", got)
		}
		wantStatus(t, call(t, h, id, http.MethodPost, "/keys/value",
			`{"key":"`+p+`l","type":"list","value":"X","index":0,"expect":"not what is there"}`, nil), 409, "conflict", "an edit against a stale position")
		wantStatus(t, call(t, h, id, http.MethodDelete, "/keys",
			`{"key":"`+p+`l","type":"list","index":0,"expect":"not what is there"}`, nil), 409, "conflict", "a removal against a stale position")
		wantStatus(t, call(t, h, id, http.MethodDelete, "/keys", `{"key":"`+p+`l","type":"list","index":1,"expect":"a"}`, nil), 200, "", "remove by position")
		// And the older form of that, a position written as text.
		wantStatus(t, call(t, h, id, http.MethodDelete, "/keys", `{"key":"`+p+`l","type":"list","member":"0"}`, nil), 200, "", "remove by position as text")
		if got := strings.Join(direct.LRange(ctx, p+"l", 0, -1).Val(), ","); got != "b,C" {
			t.Errorf("list after two removals = %s", got)
		}
	})

	t.Run("paging and the older read", func(t *testing.T) {
		pipe := direct.Pipeline()
		for i := 0; i < 1500; i++ {
			pipe.HSet(ctx, p+"big", fmt.Sprintf("f%04d", i), i)
		}
		pipe.Exec(ctx)
		seen, cursor := map[string]bool{}, ""
		for pages := 0; pages < 100; pages++ {
			var page dbx.RedisMembers
			wantStatus(t, call(t, h, id, http.MethodGet, "/keys/members?count=200&key="+p+"big&cursor="+cursor, "", &page), 200, "", "page")
			if page.Length != 1500 {
				t.Fatalf("length = %d", page.Length)
			}
			for _, r := range page.Rows {
				seen[string(*r.Field)] = true
			}
			if page.Done {
				break
			}
			cursor = page.Cursor
		}
		if len(seen) != 1500 {
			t.Errorf("paging a hash of 1500 reached %d fields", len(seen))
		}
		// The route that predates paging answers its first window and says
		// there is more, which for a hash it never used to.
		var legacy dbx.RedisValue
		call(t, h, id, http.MethodGet, "/keys/value?key="+p+"big", "", &legacy)
		if !legacy.Truncated || len(legacy.Hash) == 0 || len(legacy.Hash) >= 1500 || legacy.Length != 1500 {
			t.Errorf("/keys/value of a large hash: %d fields, truncated=%v", len(legacy.Hash), legacy.Truncated)
		}
		wantStatus(t, call(t, h, id, http.MethodGet, "/keys/members?key="+p+"missing", "", nil), 404, "key_not_found", "members of a missing key")
		wantStatus(t, call(t, h, id, http.MethodGet, "/keys/meta?key="+p+"missing", "", nil), 404, "key_not_found", "metadata of a missing key")
		wantStatus(t, call(t, h, id, http.MethodGet, "/keys/value?key="+p+"missing", "", nil), 400, "bad_request", "the older read of a missing key")
		var meta dbx.RedisKeyMeta
		call(t, h, id, http.MethodGet, "/keys/meta?key="+p+"big", "", &meta)
		if meta.Type != "hash" || meta.Length != 1500 || meta.Memory == nil || meta.Encoding == "" {
			t.Errorf("meta = %+v", meta)
		}
	})

	t.Run("a value a page cut short can be had whole", func(t *testing.T) {
		big := strings.Repeat("0123456789abcdef", 20000) // 320 kB: past what a row carries
		direct.HSet(ctx, p+"wide", "blob", big, "", "nameless")
		direct.RPush(ctx, p+"widelist", "first", big)
		direct.Set(ctx, p+"widestring", big+"\xff", 0)
		direct.SAdd(ctx, p+"wideset", "m")

		var page dbx.RedisMembers
		call(t, h, id, http.MethodGet, "/keys/members?key="+p+"wide", "", &page)
		var cut *dbx.RedisRow
		for i := range page.Rows {
			if string(*page.Rows[i].Field) == "blob" {
				cut = &page.Rows[i]
			}
		}
		if cut == nil || !cut.Truncated || cut.Bytes == nil || *cut.Bytes != int64(len(big)) || len(*cut.Value) >= len(big) {
			t.Fatalf("the long field's row = %+v", cut)
		}
		for path, want := range map[string]string{
			"/keys/raw?key=" + p + "wide&field=blob":   big,
			"/keys/raw?key=" + p + "wide&field=":       "nameless",
			"/keys/raw?key=" + p + "widelist&index=1":  big,
			"/keys/raw?key=" + p + "widelist&index=0":  "first",
			"/keys/raw?key=" + p + "widelist&index=-1": big,
			"/keys/raw?key=" + p + "widestring":        big + "\xff",
		} {
			rec := call(t, h, id, http.MethodGet, path, "", nil)
			if rec.Code != 200 || rec.Body.String() != want {
				t.Errorf("%s: %d, %d bytes, want %d", path, rec.Code, rec.Body.Len(), len(want))
			}
			if got := rec.Header().Get("Content-Length"); got != fmt.Sprint(len(want)) || rec.Header().Get("Content-Type") != "application/octet-stream" {
				t.Errorf("%s: Content-Length %s, Content-Type %s", path, got, rec.Header().Get("Content-Type"))
			}
		}
		wantStatus(t, call(t, h, id, http.MethodGet, "/keys/raw?key="+p+"nothing", "", nil), 404, "key_not_found", "a missing key")
		wantStatus(t, call(t, h, id, http.MethodGet, "/keys/raw?key="+p+"wide&field=nope", "", nil), 404, "member_not_found", "a missing field")
		wantStatus(t, call(t, h, id, http.MethodGet, "/keys/raw?key="+p+"widelist&index=99", "", nil), 404, "member_not_found", "a position past the end")
		wantStatus(t, call(t, h, id, http.MethodGet, "/keys/raw?key="+p+"wide", "", nil), 400, "bad_request", "a hash with no field named")
		wantStatus(t, call(t, h, id, http.MethodGet, "/keys/raw?key="+p+"wideset", "", nil), 400, "bad_request", "a set")
	})

	t.Run("listing, tree, rename, copy", func(t *testing.T) {
		pipe := direct.Pipeline()
		for i := 0; i < 20; i++ {
			pipe.Set(ctx, fmt.Sprintf("%sns:a:%d", p, i), "v", 0)
			pipe.HSet(ctx, fmt.Sprintf("%sns:b:%d", p, i), "f", "v")
		}
		pipe.Exec(ctx)
		var page dbx.RedisPage
		call(t, h, id, http.MethodGet, "/keys?type=hash&memory=1&count=500&pattern="+p+"ns:*", "", &page)
		if len(page.Keys) != 20 || page.Keys[0].Type != "hash" || page.Keys[0].Memory == nil || page.Total < 40 {
			t.Errorf("typed listing: %d keys, total %d", len(page.Keys), page.Total)
		}
		var tree dbx.RedisTree
		call(t, h, id, http.MethodGet, "/keys/tree?limit=50000&prefix="+p+"ns:", "", &tree)
		if !tree.Complete || tree.Count != 40 || len(tree.Folders) != 2 || tree.Folders[0].Count != 20 || tree.Types["hash"] != 20 {
			t.Errorf("tree = %+v", tree)
		}
		wantStatus(t, call(t, h, id, http.MethodPost, "/keys/rename", `{"key":"`+p+`ns:a:0","to":"`+p+`ns:a:1"}`, nil), 409, "key_exists", "rename onto an existing key")
		wantStatus(t, call(t, h, id, http.MethodPost, "/keys/rename", `{"key":"`+p+`ns:a:0","to":"`+p+`ns:a:moved"}`, nil), 200, "", "rename")
		wantStatus(t, call(t, h, id, http.MethodPost, "/keys/rename", `{"key":"`+p+`ns:a:0","to":"`+p+`x"}`, nil), 404, "key_not_found", "rename a missing key")
		wantStatus(t, call(t, h, id, http.MethodPost, "/keys/copy", `{"key":"`+p+`ns:b:0","to":"`+p+`ns:b:1"}`, nil), 409, "key_exists", "copy onto an existing key")
		wantStatus(t, call(t, h, id, http.MethodPost, "/keys/copy", `{"key":"`+p+`ns:b:0","to":"`+p+`ns:b:copy"}`, nil), 200, "", "copy")
		wantStatus(t, call(t, h, id, http.MethodPost, "/keys/copy", `{"key":"`+p+`ns:b:0","to":"`+p+`ns:b:1","overwrite":true}`, nil), 200, "", "copy with overwrite")
		if direct.HGet(ctx, p+"ns:b:copy", "f").Val() != "v" {
			t.Error("the copy is not there")
		}
	})

	t.Run("streams", func(t *testing.T) {
		var added struct {
			ID string `json:"id"`
		}
		for i := 0; i < 5; i++ {
			call(t, h, id, http.MethodPost, "/keys/value", `{"key":"`+p+`x","type":"stream","entries":[["n","`+fmt.Sprint(i)+`"],["n","again"]]}`, &added)
		}
		if added.ID == "" {
			t.Fatal("an added entry came back with no id")
		}
		wantStatus(t, call(t, h, id, http.MethodPost, "/keys/stream/groups", `{"key":"`+p+`x","group":"g","id":"0"}`, nil), 200, "", "create group")
		direct.XReadGroup(ctx, &redis.XReadGroupArgs{Group: "g", Consumer: "c1", Streams: []string{p + "x", ">"}, Count: 3})
		var info dbx.RedisStreamInfo
		call(t, h, id, http.MethodGet, "/keys/stream?key="+p+"x", "", &info)
		if info.Length != 5 || len(info.Groups) != 1 || info.Groups[0].Pending != 3 || len(info.Groups[0].Consumers) != 1 {
			t.Fatalf("stream = %+v", info)
		}
		var pending dbx.RedisStreamPendingPage
		call(t, h, id, http.MethodGet, "/keys/stream/pending?group=g&key="+p+"x", "", &pending)
		if len(pending.Entries) != 3 || pending.Entries[0].Consumer != "c1" {
			t.Fatalf("pending = %+v", pending)
		}
		var acked struct {
			Acknowledged int64 `json:"acknowledged"`
		}
		call(t, h, id, http.MethodPost, "/keys/stream/ack", `{"key":"`+p+`x","group":"g","ids":["`+pending.Entries[0].ID+`"]}`, &acked)
		if acked.Acknowledged != 1 {
			t.Errorf("acknowledged %d", acked.Acknowledged)
		}
		var removed struct {
			Removed int64 `json:"removed"`
		}
		call(t, h, id, http.MethodDelete, "/keys", `{"key":"`+p+`x","type":"stream","members":["`+added.ID+`"]}`, &removed)
		if removed.Removed != 1 {
			t.Errorf("deleting an entry removed %d", removed.Removed)
		}
		call(t, h, id, http.MethodPost, "/keys/stream/trim", `{"key":"`+p+`x","maxLen":2}`, &removed)
		if removed.Removed != 2 || direct.XLen(ctx, p+"x").Val() != 2 {
			t.Errorf("trimming to 2 removed %d", removed.Removed)
		}
		call(t, h, id, http.MethodDelete, "/keys/stream/groups", `{"key":"`+p+`x","group":"g"}`, &removed)
		if removed.Removed != 1 {
			t.Errorf("destroying the group reported %d", removed.Removed)
		}
		// The entry's pairs come back in order, repeats included.
		rec := call(t, h, id, http.MethodGet, "/keys/members?key="+p+"x", "", nil)
		if !strings.Contains(rec.Body.String(), `"fields":[["n","`) || !strings.Contains(rec.Body.String(), `["n","again"]]`) {
			t.Errorf("stream rows = %s", rec.Body.String())
		}
	})
}

// The expiries that used to delete: far enough off that the number wrapped
// on its way to the server. Sent by a role that may edit and may not delete.
func TestLiveAPIRedisFarExpiryKeepsTheKey(t *testing.T) {
	_, _, _, direct := redisLive(t, "JD_TEST_REDIS_DSN", auth.RoleAdmin)
	_, h, id := redisRouter(t, auth.RoleLimited, os.Getenv("JD_TEST_REDIS_DSN"))
	ctx := context.Background()
	p := redisAPIPrefix + "far:"
	key := p + "k"
	direct.Set(ctx, key, "v", 0)
	direct.HSet(ctx, p+"h", "a", "1", "b", "2")
	left := func(k string) int64 {
		ms, _ := direct.Do(ctx, "PTTL", k).Int64()
		return ms
	}

	for _, body := range []string{
		`{"key":"` + key + `","at":253402300800000}`,
		`{"key":"` + key + `","ttl":9223372037}`,
		`{"key":"` + key + `","ttlMs":9223372036855}`,
	} {
		var out struct {
			OK   bool  `json:"ok"`
			PTTL int64 `json:"pttl"`
		}
		rec := call(t, h, id, http.MethodPost, "/keys/expire", body, &out)
		if rec.Code != 200 || !out.OK || out.PTTL < 9_000_000_000_000 {
			t.Errorf("%s = %d %s", body, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
		if direct.Get(ctx, key).Val() != "v" || left(key) < 9_000_000_000_000 {
			t.Fatalf("%s: the key is gone, or has %d ms left", body, left(key))
		}
	}
	rec := call(t, h, id, http.MethodPost, "/keys/value", `{"key":"`+p+`h","type":"hash","field":"c","value":"3","ttl":9223372037}`, nil)
	wantStatus(t, rec, 200, "", "a hash write with a far expiry")
	if direct.HLen(ctx, p+"h").Val() != 3 || left(p+"h") < 9_000_000_000_000 {
		t.Fatalf("the hash has %d fields and %d ms left", direct.HLen(ctx, p+"h").Val(), left(p+"h"))
	}
	direct.Persist(ctx, key)
	rec = call(t, h, id, http.MethodPost, "/keys/value", `{"key":"`+key+`","value":"w","ttl":9223372037}`, nil)
	wantStatus(t, rec, 200, "", "a string write with a far expiry")
	if direct.Get(ctx, key).Val() != "w" || left(key) < 9_000_000_000_000 {
		t.Errorf("the string has %d ms left", left(key))
	}
	var meta dbx.RedisKeyMeta
	call(t, h, id, http.MethodGet, "/keys/meta?key="+key, "", &meta)
	if meta.PTTL < 9_000_000_000_000 || meta.TTL < 9_000_000_000 || meta.ExpiresAt == nil {
		t.Errorf("metadata of a key expiring in 292 years: %+v", meta)
	}

	// Past what a server is trusted to hold: refused, and nothing changes.
	for _, rt := range []redisRoute{
		{http.MethodPost, "/keys/expire", `{"key":"` + key + `","ttl":9223372036854775}`},
		{http.MethodPost, "/keys/expire", `{"key":"` + key + `","ttlMs":9223372036854775807}`},
		{http.MethodPost, "/keys/expire", `{"key":"` + key + `","at":9223372036854775807}`},
		{http.MethodPost, "/keys/value", `{"key":"` + p + `h","type":"hash","field":"d","value":"4","ttl":9223372036854775}`},
		{http.MethodPost, "/keys/value", `{"key":"` + key + `","value":"x","ttl":9223372036854775}`},
	} {
		rec := redisDo(t, h, id, rt)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "too far off") {
			t.Errorf("%s %s = %d %s", rt.path, rt.body, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
	if direct.Get(ctx, key).Val() != "w" || direct.HLen(ctx, p+"h").Val() != 3 {
		t.Error("a refused expiry changed a key")
	}
}

// A request that addresses the inside of a key and names nothing there is
// refused. It is never read as a request for the key.
func TestLiveAPIRedisMemberRequestsNeverRemoveTheKey(t *testing.T) {
	s, h, id, direct := redisLive(t, "JD_TEST_REDIS_DSN", auth.RoleAdmin)
	ctx := context.Background()
	key := redisAPIPrefix + "inside:s"
	direct.SAdd(ctx, key, "a", "b", "")
	for _, body := range []string{
		`{"key":"` + key + `","members":[]}`,
		`{"key":"` + key + `","type":"set","members":[]}`,
		`{"key":"` + key + `","expect":"a"}`,
		// A path is a JSON document's; the server says this key is not one.
		`{"key":"` + key + `","path":"$.user.email"}`,
		`{"key":"` + key + `","type":"json","path":"$"}`,
		`{"key":"` + key + `","type":"set","path":"$"}`,
	} {
		rec := call(t, h, id, http.MethodDelete, "/keys", body, nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s", body, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
		if direct.SCard(ctx, key).Val() != 3 {
			t.Fatalf("%s removed the key or a member: %d left", body, direct.SCard(ctx, key).Val())
		}
	}
	var out struct {
		Removed int64 `json:"removed"`
	}
	call(t, h, id, http.MethodDelete, "/keys", `{"key":"`+key+`","members":[""]}`, &out)
	if out.Removed != 1 || direct.SCard(ctx, key).Val() != 2 {
		t.Errorf("removing the empty member removed %d and left %d", out.Removed, direct.SCard(ctx, key).Val())
	}
	// A stream entry that is not an id is refused without being quoted: the
	// sentence is recorded, and what stood in a member's place is not.
	stream := redisAPIPrefix + "inside:x"
	direct.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: map[string]any{"f": "v"}})
	rec := call(t, h, id, http.MethodDelete, "/keys", `{"key":"`+stream+`","type":"stream","members":["S3CRET-not-an-id"]}`, nil)
	wantStatus(t, rec, 400, "bad_request", "removing a stream entry by something that is not an id")
	hash := redisAPIPrefix + "inside:h"
	direct.HSet(ctx, hash, "S3CRET-field", "1", "other", "2")
	rec = call(t, h, id, http.MethodPost, "/keys/value", `{"key":"`+hash+`","type":"hash","field":"S3CRET-field","value":"9","replace":"other"}`, nil)
	wantStatus(t, rec, 409, "conflict", "renaming a field onto an existing one")
	if strings.Contains(rec.Body.String(), "S3CRET") {
		t.Errorf("the refusal quotes the field: %s", rec.Body.String())
	}
	if trail := auditTrail(t, s); strings.Contains(trail, "S3CRET") {
		t.Errorf("a member or field name reached the audit trail:\n%s", trail)
	}
}

func TestLiveAPIRedisBulkAndConsoleFollowTheCallersRole(t *testing.T) {
	_, admin, adminID, direct := redisLive(t, "JD_TEST_REDIS_DSN", auth.RoleAdmin)
	ctx := context.Background()
	p := redisAPIPrefix + "role:"
	seed := func() {
		pipe := direct.Pipeline()
		for i := 0; i < 300; i++ {
			pipe.Set(ctx, fmt.Sprintf("%sk:%d", p, i), "v", 0)
		}
		pipe.HSet(ctx, p+"h", "f", "v")
		pipe.Exec(ctx)
	}
	seed()
	_, limited, limitedID := redisRouter(t, auth.RoleLimited, os.Getenv("JD_TEST_REDIS_DSN"))

	bulk := func(h http.Handler, id int64, body string) (*httptest.ResponseRecorder, dbx.RedisBulkResult) {
		var res dbx.RedisBulkResult
		rec := call(t, h, id, http.MethodPost, "/keys/bulk", body, &res)
		return rec, res
	}
	// Anyone who may write may count.
	rec, dry := bulk(limited, limitedID, `{"pattern":"`+p+`k:*","action":"delete","dryRun":true}`)
	if rec.Code != 200 || dry.Matched != 300 || dry.Affected != 0 || !dry.Complete || !dry.DryRun {
		t.Fatalf("dry run as limited = %d %+v", rec.Code, dry)
	}
	if direct.Exists(ctx, p+"k:0").Val() != 1 {
		t.Fatal("a dry run deleted a key")
	}
	// Removing is another matter, and an expiry on every key is removing.
	rec, _ = bulk(limited, limitedID, `{"pattern":"`+p+`k:*","action":"delete"}`)
	wantStatus(t, rec, 403, "forbidden", "bulk delete as limited")
	rec, _ = bulk(limited, limitedID, `{"pattern":"`+p+`k:*","action":"expire","ttl":1}`)
	wantStatus(t, rec, 403, "forbidden", "bulk expire as limited")
	if direct.Exists(ctx, p+"k:0").Val() != 1 || direct.TTL(ctx, p+"k:0").Val() != -1 {
		t.Fatal("a refused bulk request changed a key")
	}
	rec, persisted := bulk(limited, limitedID, `{"pattern":"`+p+`k:*","action":"persist"}`)
	if rec.Code != 200 || persisted.Matched != 300 {
		t.Errorf("bulk persist as limited = %d %+v", rec.Code, persisted)
	}
	rec, gone := bulk(admin, adminID, `{"pattern":"`+p+`k:*","action":"delete"}`)
	if rec.Code != 200 || gone.Affected != 300 || !gone.Complete {
		t.Fatalf("bulk delete as admin = %d %+v", rec.Code, gone)
	}
	if direct.Exists(ctx, p+"h").Val() != 1 {
		t.Error("the bulk delete took a key its pattern did not match")
	}

	console := func(h http.Handler, id int64, command string) (*httptest.ResponseRecorder, dbx.RedisCommandResult) {
		body, _ := json.Marshal(map[string]string{"command": command})
		var res dbx.RedisCommandResult
		rec := call(t, h, id, http.MethodPost, "/redis/command", string(body), &res)
		return rec, res
	}
	seed()
	rec, res := console(limited, limitedID, "GET "+p+"k:1")
	if rec.Code != 200 || res.Class != dbx.RedisClassRead || res.Reply.Type != "string" || res.Reply.Value != "v" || res.Ms <= 0 {
		t.Errorf("GET as limited = %d %+v", rec.Code, res)
	}
	rec, res = console(limited, limitedID, `SET `+p+`k:1 "new value"`)
	if rec.Code != 200 || res.Class != dbx.RedisClassWrite || direct.Get(ctx, p+"k:1").Val() != "new value" {
		t.Errorf("SET as limited = %d %+v", rec.Code, res)
	}
	// What the browser's delete button needs, the console needs.
	for _, command := range []string{
		"DEL " + p + "k:1", "UNLINK " + p + "k:1", "HDEL " + p + "h f", "FLUSHDB", "EXPIRE " + p + "k:1 0",
		`EVAL "return redis.call('del', KEYS[1])" 1 ` + p + "k:1",
		// Not a command the dashboard or the server knows: destructive until
		// shown otherwise.
		"JDB4.NOSUCHCOMMAND " + p + "k:1",
	} {
		rec, _ = console(limited, limitedID, command)
		wantStatus(t, rec, 403, "forbidden", command+" as limited")
	}
	for _, command := range []string{"CONFIG GET maxmemory", "CONFIG SET maxmemory 0", "ACL LIST", "ACL WHOAMI"} {
		rec, _ = console(limited, limitedID, command)
		wantStatus(t, rec, 403, "forbidden", command+" as limited")
	}
	if direct.Exists(ctx, p+"k:1", p+"h").Val() != 2 {
		t.Fatal("a refused console command ran")
	}
	// Removing by another name is still removing. Storing an empty result
	// over a key deletes it; an expiry already past deletes whichever command
	// sets it; and one quoted word that spells a subcommand is a command
	// nobody knows, not that subcommand.
	direct.SAdd(ctx, p+"victim", "a", "b", "c")
	for _, command := range []string{
		"SINTERSTORE " + p + "victim " + p + "nosuch",
		"SUNIONSTORE " + p + "victim " + p + "nosuch",
		"ZRANGESTORE " + p + "victim " + p + "nosuch 0 -1",
		"SORT " + p + "nosuch STORE " + p + "victim",
		"BITOP AND " + p + "victim " + p + "nosuch",
		"SET " + p + "victim x PXAT 1",
		"GETEX " + p + "k:1 PXAT 1",
		"HEXPIRE " + p + "h 0 FIELDS 1 f",
		`"CONFIG GET"`, `'acl deluser'`, `"FLUSHDB"`,
	} {
		rec, _ = console(limited, limitedID, command)
		wantStatus(t, rec, 403, "forbidden", command+" as limited")
	}
	// Waiting on a stream for good is refused whatever the group is called.
	rec, _ = console(limited, limitedID, "XREADGROUP GROUP STREAMS c BLOCK 0 STREAMS "+p+"nostream >")
	wantStatus(t, rec, 400, "command_blocked", "XREADGROUP with a group named STREAMS")
	if direct.SCard(ctx, p+"victim").Val() != 3 || direct.Exists(ctx, p+"k:1", p+"h").Val() != 2 || direct.HExists(ctx, p+"h", "f").Val() != true {
		t.Fatal("a refused console command ran")
	}
	// Renaming a member onto one that is there would only remove the old
	// one: a removal, and refused as an edit.
	rec = call(t, limited, limitedID, http.MethodPost, "/keys/value", `{"key":"`+p+`victim","type":"set","value":"a","replace":"b"}`, nil)
	wantStatus(t, rec, 409, "conflict", "renaming a set member onto an existing one as limited")
	direct.ZAdd(ctx, p+"z", redis.Z{Score: 1, Member: "a"}, redis.Z{Score: 2, Member: "b"})
	rec = call(t, limited, limitedID, http.MethodPost, "/keys/value", `{"key":"`+p+`z","type":"zset","value":"a","score":5,"replace":"b"}`, nil)
	wantStatus(t, rec, 409, "conflict", "renaming a sorted-set member onto an existing one as limited")
	if direct.SCard(ctx, p+"victim").Val() != 3 || direct.ZCard(ctx, p+"z").Val() != 2 || direct.ZScore(ctx, p+"z", "a").Val() != 1 {
		t.Fatal("a refused rename changed the collection")
	}
	// An expiry still to come is the edit it has always been, however soon.
	rec, res = console(limited, limitedID, "PEXPIRE "+p+"k:2 600000")
	if rec.Code != 200 || res.Class != dbx.RedisClassWrite {
		t.Errorf("PEXPIRE as limited = %d %+v", rec.Code, res)
	}
	// Listening to what is published is kept with MONITOR.
	wantStatus(t, call(t, limited, limitedID, http.MethodGet, "/redis/subscribe?pattern=*", "", nil), 403, "forbidden", "subscribing as limited")
	wantStatus(t, call(t, limited, limitedID, http.MethodGet, "/redis/monitor", "", nil), 403, "forbidden", "MONITOR as limited")

	rec, res = console(admin, adminID, `"CONFIG GET"`)
	if rec.Code != 200 || res.Known || res.Class != dbx.RedisClassDangerous || res.Reply.Type != "error" {
		t.Errorf("a quoted two-word name as admin = %d %+v", rec.Code, res)
	}
	rec, res = console(admin, adminID, "SINTERSTORE "+p+"victim "+p+"nosuch")
	if rec.Code != 200 || res.Class != dbx.RedisClassDangerous || direct.Exists(ctx, p+"victim").Val() != 0 {
		t.Errorf("SINTERSTORE as admin = %d %+v", rec.Code, res)
	}

	rec, res = console(admin, adminID, "DEL "+p+"k:1 "+p+"k:2")
	if rec.Code != 200 || res.Class != dbx.RedisClassDangerous || res.Reply.Value != float64(2) || len(res.Reasons) == 0 {
		t.Errorf("DEL as admin = %d %+v", rec.Code, res)
	}
	rec, res = console(admin, adminID, "HGETALL "+p+"h")
	if rec.Code != 200 || res.Reply.Type != "map" || len(res.Reply.Entries) != 1 {
		t.Errorf("HGETALL = %d %+v", rec.Code, res.Reply)
	}
	rec, res = console(admin, adminID, "KEYS "+p+"h")
	if rec.Code != 200 || !res.Slow || res.Class != dbx.RedisClassRead || len(res.Reply.Items) != 1 {
		t.Errorf("KEYS = %d %+v", rec.Code, res)
	}
	// An error from the server is a result of the command.
	rec, res = console(admin, adminID, "LPUSH "+p+"h x")
	if rec.Code != 200 || res.Reply.Type != "error" || !strings.HasPrefix(res.Reply.Value.(string), "WRONGTYPE") {
		t.Errorf("a wrong-type command = %d %+v", rec.Code, res.Reply)
	}
	rec, res = console(admin, adminID, "JDB4.NOSUCHCOMMAND x")
	if rec.Code != 200 || res.Class != dbx.RedisClassDangerous || res.Known || res.Reply.Type != "error" {
		t.Errorf("an unknown command as admin = %d %+v", rec.Code, res)
	}
	// A long reply is cut and says so.
	direct.Del(ctx, p+"long")
	members := make([]any, 12000)
	for i := range members {
		members[i] = i
	}
	direct.RPush(ctx, p+"long", members...)
	rec, res = console(admin, adminID, "LRANGE "+p+"long 0 -1")
	if rec.Code != 200 || !res.Truncated || !res.Reply.Truncated || res.Reply.Length != 12000 || len(res.Reply.Items) >= 12000 {
		t.Errorf("a long reply: truncated=%v length=%d items=%d", res.Truncated, res.Reply.Length, len(res.Reply.Items))
	}
}

// What is written to a key is application data — as often as not a session
// token or a cached credential — and none of it belongs on the audit trail.
// Every route that carries a value is driven here with one that is easy to
// find, and the trail is searched for it.
func TestLiveAPIRedisAuditNamesKeysAndNeverValues(t *testing.T) {
	s, h, id, _ := redisLive(t, "JD_TEST_REDIS_DSN", auth.RoleAdmin)
	const secret = "S3CRET-PAYLOAD-7f3a"
	p := redisAPIPrefix + "audit:"
	for _, rt := range []redisRoute{
		{http.MethodPost, "/keys/value", `{"key":"` + p + `s","value":"` + secret + `"}`},
		{http.MethodPost, "/keys/value", `{"key":"` + p + `h","type":"hash","field":"f","value":"` + secret + `"}`},
		{http.MethodPost, "/keys/value", `{"key":"` + p + `set","type":"set","value":"` + secret + `"}`},
		{http.MethodPost, "/keys/value", `{"key":"` + p + `l","type":"list","value":"` + secret + `"}`},
		{http.MethodPost, "/keys/value", `{"key":"` + p + `z","type":"zset","score":1,"value":"` + secret + `"}`},
		{http.MethodPost, "/keys/value", `{"key":"` + p + `x","type":"stream","entries":[["f","` + secret + `"]]}`},
		{http.MethodPost, "/keys/value", `{"key":"` + p + `l","type":"list","index":0,"value":"new","expect":"` + secret + `"}`},
		{http.MethodPost, "/keys/value", `{"key":"` + p + `set","type":"set","value":"renamed","replace":"` + secret + `"}`},
		{http.MethodDelete, "/keys", `{"key":"` + p + `z","type":"zset","member":"` + secret + `"}`},
		{http.MethodPost, "/redis/command", `{"command":"SET ` + p + `c ` + secret + `"}`},
		{http.MethodPost, "/redis/command", `{"command":"HSET ` + p + `ch field ` + secret + `"}`},
		// An unknown command's error quotes its arguments back.
		{http.MethodPost, "/redis/command", `{"command":"JDB4.NOPE ` + secret + `"}`},
		{http.MethodPost, "/redis/command", `{"command":"APPEND ` + p + `s ` + secret + `"}`},
		{http.MethodPost, "/redis/publish", `{"channel":"jdb4api.audit","message":"` + secret + `"}`},
		{http.MethodPost, "/keys/rename", `{"key":"` + p + `s","to":"` + p + `s2"}`},
		{http.MethodPost, "/keys/expire", `{"key":"` + p + `s2","ttl":600}`},
		{http.MethodPost, "/keys/bulk", `{"pattern":"` + p + `nothing*","action":"delete"}`},
		{http.MethodDelete, "/keys", `{"keys":["` + p + `h","` + p + `set"]}`},
	} {
		if rec := redisDo(t, h, id, rt); rec.Code != http.StatusOK {
			t.Fatalf("%s %s %s: %d %s", rt.method, rt.path, rt.body, rec.Code, rec.Body.String())
		}
	}
	trail := auditTrail(t, s)
	if strings.Contains(trail, secret) {
		t.Errorf("a value reached the audit trail:\n%s", trail)
	}
	// And what was done, to which key, is there.
	for _, want := range []string{
		"database.redis.set cache 200", `"key":"` + p + `s"`, `"type":"hash"`,
		"database.redis.member.delete cache 200", `"key":"` + p + `z"`,
		"database.redis.command cache 200", `"command":"SET"`, `"keys":["` + p + `c"]`, `"command":"HSET"`,
		`"command":"JDB4.NOPE"`, `"class":"dangerous"`,
		"database.redis.publish cache 200", `"channel":"jdb4api.audit"`,
		"database.redis.rename cache 200", `"to":"` + p + `s2"`,
		"database.redis.expire cache 200", "database.redis.bulk cache 200", `"pattern":"` + p + `nothing*"`,
		"database.redis.delete cache 200", `"removed":2`,
	} {
		if !strings.Contains(trail, want) {
			t.Errorf("the audit trail has no %s:\n%s", want, trail)
		}
	}
	// A refusal is on the trail too, under the name of what was refused.
	redisDo(t, h, id, redisRoute{http.MethodPost, "/redis/command", `{"command":"SUBSCRIBE jdb4api"}`})
	if trail := auditTrail(t, s); !strings.Contains(trail, "database.redis.command cache 400") || !strings.Contains(trail, `"command":"SUBSCRIBE"`) {
		t.Errorf("a blocked command is not on the trail:\n%s", trail)
	}
}

func TestLiveAPIRedisServerReads(t *testing.T) {
	_, h, id, direct := redisLive(t, "JD_TEST_REDIS_DSN", auth.RoleReadOnly)
	direct.Set(context.Background(), redisAPIPrefix+"present", "v", time.Hour)
	for _, rt := range redisReads {
		if strings.Contains(rt.path, "key=k") || strings.HasPrefix(rt.path, "/keys/stream") {
			continue
		}
		rec := redisDo(t, h, id, rt)
		if rec.Code != http.StatusOK {
			t.Errorf("%s as readonly: %d %s", rt.path, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
	// The stats the fleet reads still carry what they always did, as text,
	// and now the counters a chart needs beside them.
	var stats struct {
		Server struct {
			RedisVersion    string         `json:"redis_version"`
			UsedMemoryHuman string         `json:"used_memory_human"`
			Counters        map[string]any `json:"counters"`
			Role            string         `json:"role"`
			Mode            string         `json:"mode"`
			SampledAtMs     int64          `json:"sampledAtMs"`
			Flavor          string         `json:"flavor"`
			Version         string         `json:"version"`
		} `json:"server"`
	}
	call(t, h, id, http.MethodGet, "/stats", "", &stats)
	sv := stats.Server
	if sv.RedisVersion == "" || sv.UsedMemoryHuman == "" || sv.Role != "primary" || sv.Mode != "standalone" || sv.SampledAtMs == 0 || sv.Flavor == "" {
		t.Errorf("stats = %+v", sv)
	}
	for _, name := range []string{"total_commands_processed", "keyspace_hits", "keyspace_misses", "used_memory", "maxmemory", "connected_clients", "instantaneous_ops_per_sec", "evicted_keys", "expired_keys", "uptime_in_seconds"} {
		if _, ok := sv.Counters[name].(float64); !ok {
			t.Errorf("counter %s = %#v, want a number", name, sv.Counters[name])
		}
	}

	var config dbx.RedisConfig
	call(t, h, id, http.MethodGet, "/redis/config", "", &config)
	for _, g := range config.Groups {
		for _, p := range g.Params {
			if (p.Name == "requirepass" || p.Name == "masterauth") && (!p.Secret || p.Value != "") {
				t.Errorf("%s = %+v", p.Name, p)
			}
		}
	}
	var acl struct {
		Supported bool               `json:"supported"`
		Users     []dbx.RedisACLUser `json:"users"`
	}
	rec := call(t, h, id, http.MethodGet, "/redis/acl", "", &acl)
	if !acl.Supported || len(acl.Users) == 0 || strings.Contains(rec.Body.String(), "#") {
		t.Errorf("acl = %s", rec.Body.String())
	}
	// The same users through the accounts route every engine has, which used
	// to hand the rule over with its password hashes in it.
	if roles := do(t, h, http.MethodGet, pathf("/databases/%d/server/roles", id), ""); strings.Contains(roles.Body.String(), "#") {
		t.Errorf("the accounts route carries a password hash: %s", roles.Body.String())
	}

	var refs struct {
		Documented bool                  `json:"documented"`
		Commands   []dbx.RedisCommandRef `json:"commands"`
	}
	call(t, h, id, http.MethodGet, "/redis/commands", "", &refs)
	if len(refs.Commands) < 150 {
		t.Fatalf("the reference lists %d commands", len(refs.Commands))
	}
	// Asked for again, it is the same list, from memory.
	var again struct {
		Commands []dbx.RedisCommandRef `json:"commands"`
	}
	call(t, h, id, http.MethodGet, "/redis/commands", "", &again)
	if len(again.Commands) != len(refs.Commands) {
		t.Errorf("the reference changed between two reads: %d then %d", len(refs.Commands), len(again.Commands))
	}

	var analysis dbx.RedisAnalysis
	call(t, h, id, http.MethodGet, "/redis/analysis?sample=50000&top=3", "", &analysis)
	if analysis.Sampled == 0 || len(analysis.Expiry) != 5 || len(analysis.TopKeys) > 3 || len(analysis.Types) == 0 {
		t.Errorf("analysis = %+v", analysis)
	}
}

// The routes that change the server itself, on a server this run owns.
func TestLiveAPIRedisAdmin(t *testing.T) {
	s, h, id, direct := redisLive(t, "JD_TEST_REDIS_ADMIN_DSN", auth.RoleAdmin)
	ctx := context.Background()
	const secret = "hunter2-NOT-FOR-THE-TRAIL"

	t.Run("config", func(t *testing.T) {
		var change dbx.RedisConfigChange
		wantStatus(t, call(t, h, id, http.MethodPut, "/redis/config", `{"name":"slowlog-max-len","value":"77"}`, &change), 200, "", "CONFIG SET")
		if change.Name != "slowlog-max-len" || change.Secret || change.Rewritten {
			t.Errorf("change = %+v", change)
		}
		if got := direct.ConfigGet(ctx, "slowlog-max-len").Val()["slowlog-max-len"]; got != "77" {
			t.Errorf("slowlog-max-len = %q", got)
		}
		// Asked to be written down on a server started without a file: the
		// change stands, and the reply says the rewrite did not.
		call(t, h, id, http.MethodPut, "/redis/config", `{"name":"slowlog-max-len","value":"128","rewrite":true}`, &change)
		if change.Rewritten || change.RewriteError == "" {
			t.Errorf("a rewrite with no config file = %+v", change)
		}
		wantStatus(t, call(t, h, id, http.MethodPut, "/redis/config", `{"name":"no-such-parameter","value":"1"}`, nil), 400, "bad_request", "an unknown parameter")
		wantStatus(t, call(t, h, id, http.MethodPut, "/redis/config", `{"name":"max*","value":"1"}`, nil), 400, "bad_request", "a pattern for a name")

		t.Cleanup(func() { direct.ConfigSet(context.Background(), "masterauth", "") })
		rec := call(t, h, id, http.MethodPut, "/redis/config", `{"name":"masterauth","value":"`+secret+`"}`, &change)
		if rec.Code != 200 || !change.Secret || strings.Contains(rec.Body.String(), secret) {
			t.Errorf("setting a password: %d %s", rec.Code, rec.Body.String())
		}
		if rec := call(t, h, id, http.MethodGet, "/redis/config", "", nil); strings.Contains(rec.Body.String(), secret) {
			t.Error("the configuration read hands the password back")
		}
	})

	t.Run("slow log and save", func(t *testing.T) {
		direct.ConfigSet(ctx, "slowlog-log-slower-than", "0")
		direct.Set(ctx, redisAPIPrefix+"slow", "v", time.Minute)
		var slow dbx.RedisSlowlog
		call(t, h, id, http.MethodGet, "/redis/slowlog?count=20", "", &slow)
		if !slow.Supported || len(slow.Entries) == 0 || slow.ThresholdUs == nil || *slow.ThresholdUs != 0 {
			t.Errorf("slow log = %+v", slow)
		}
		direct.ConfigSet(ctx, "slowlog-log-slower-than", "10000000")
		wantStatus(t, call(t, h, id, http.MethodPost, "/redis/slowlog/reset", "", nil), 200, "", "reset")
		call(t, h, id, http.MethodGet, "/redis/slowlog", "", &slow)
		if slow.Length != 0 {
			t.Errorf("%d entries after a reset", slow.Length)
		}
		direct.ConfigSet(ctx, "slowlog-log-slower-than", "10000")

		var saved struct {
			Status string `json:"status"`
		}
		wantStatus(t, call(t, h, id, http.MethodPost, "/redis/save", `{"mode":"bgsave"}`, &saved), 200, "", "bgsave")
		if saved.Status == "" {
			t.Error("BGSAVE came back with no status")
		}
	})

	t.Run("clients", func(t *testing.T) {
		victim := redis.NewClient(direct.Options())
		defer victim.Close()
		victim.Do(ctx, "CLIENT", "SETNAME", "jdb4api-victim")
		var list struct {
			Clients []dbx.RedisClientInfo `json:"clients"`
		}
		call(t, h, id, http.MethodGet, "/redis/clients", "", &list)
		var target int64
		for _, c := range list.Clients {
			if c.Name == "jdb4api-victim" {
				target = c.ID
			}
		}
		if target == 0 {
			t.Fatalf("the named client is not listed: %+v", list.Clients)
		}
		wantStatus(t, call(t, h, id, http.MethodPost, "/redis/clients/kill", fmt.Sprintf(`{"id":%d}`, target), nil), 200, "", "kill")
		wantStatus(t, call(t, h, id, http.MethodPost, "/redis/clients/kill", fmt.Sprintf(`{"id":%d}`, target), nil), 404, "client_not_found", "kill again")
	})

	t.Run("users", func(t *testing.T) {
		direct.Do(ctx, "ACL", "DELUSER", "jdb4api-app")
		t.Cleanup(func() { direct.Do(context.Background(), "ACL", "DELUSER", "jdb4api-app") })
		var res dbx.RedisACLResult
		rec := call(t, h, id, http.MethodPut, "/redis/acl/jdb4api-app",
			`{"create":true,"enabled":true,"password":"`+secret+`","keys":["app:*"],"channels":[],"commands":["+@read","-@dangerous"]}`, &res)
		if rec.Code != 200 || res.User.Name != "jdb4api-app" || !res.User.Enabled || res.User.Passwords != 1 || strings.Join(res.User.Keys, " ") != "~app:*" {
			t.Fatalf("create user: %d %s", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), secret) || strings.Contains(rec.Body.String(), "#") {
			t.Errorf("the reply carries the password or its hash: %s", rec.Body.String())
		}
		wantStatus(t, call(t, h, id, http.MethodPut, "/redis/acl/jdb4api-app", `{"create":true}`, nil), 400, "bad_request", "create a user that exists")
		call(t, h, id, http.MethodPut, "/redis/acl/jdb4api-app", `{"enabled":false}`, &res)
		if res.User.Enabled || res.User.Passwords != 1 || strings.Join(res.User.Keys, " ") != "~app:*" {
			t.Errorf("after disabling: %+v", res.User)
		}
		wantStatus(t, call(t, h, id, http.MethodPut, "/redis/acl/jdb4api-app", `{"commands":["get set"]}`, nil), 400, "bad_request", "a malformed command rule")
		// A password typed into the wrong box is refused, and is neither
		// quoted back nor recorded.
		rec = call(t, h, id, http.MethodPut, "/redis/acl/jdb4api-app", `{"commands":[">`+secret+`"]}`, nil)
		if rec.Code != 400 || strings.Contains(rec.Body.String(), secret) {
			t.Errorf("a password among the command rules: %d %s", rec.Code, rec.Body.String())
		}
		// The account this connection uses, whichever it is, is marked and
		// cannot be switched off or removed from here.
		var list struct {
			Users []dbx.RedisACLUser `json:"users"`
		}
		call(t, h, id, http.MethodGet, "/redis/acl", "", &list)
		self := ""
		for _, u := range list.Users {
			if u.Self {
				self = u.Name
			}
		}
		if self == "" {
			t.Fatalf("no user is marked as the dashboard's own: %+v", list.Users)
		}
		wantStatus(t, call(t, h, id, http.MethodPut, "/redis/acl/"+self, `{"enabled":false}`, nil), 400, "bad_request", "switching off the dashboard's own user")
		wantStatus(t, call(t, h, id, http.MethodDelete, "/redis/acl/"+self, "", nil), 400, "bad_request", "removing the dashboard's own user")
		// Nor is it one to take commands or keys away from.
		for _, body := range []string{`{"commands":[]}`, `{"commands":["+@all","-@dangerous"]}`, `{"keys":[]}`, `{"keys":["app:*"]}`} {
			rec := call(t, h, id, http.MethodPut, "/redis/acl/"+self, body, nil)
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "the dashboard connects as") {
				t.Errorf("PUT /redis/acl/%s %s = %d %s", self, body, rec.Code, strings.TrimSpace(rec.Body.String()))
			}
		}
		call(t, h, id, http.MethodGet, "/redis/acl", "", &list)
		for _, u := range list.Users {
			if u.Self && (!u.Unrestricted || !u.Enabled) {
				t.Fatalf("the dashboard's own user after the refused changes: %+v", u)
			}
		}
		wantStatus(t, call(t, h, id, http.MethodDelete, "/redis/acl/default", "", nil), 400, "bad_request", "removing the default user")
		wantStatus(t, call(t, h, id, http.MethodDelete, "/redis/acl/jdb4api-app", "", nil), 200, "", "remove the user")
		wantStatus(t, call(t, h, id, http.MethodDelete, "/redis/acl/jdb4api-app", "", nil), 400, "bad_request", "remove it again")
	})

	t.Run("publish", func(t *testing.T) {
		var out struct {
			Receivers int64 `json:"receivers"`
		}
		wantStatus(t, call(t, h, id, http.MethodPost, "/redis/publish", `{"channel":"jdb4api.nobody","message":"`+secret+`"}`, &out), 200, "", "publish")
		if out.Receivers != 0 {
			t.Errorf("a message to a channel nobody listens on reached %d", out.Receivers)
		}
	})

	trail := auditTrail(t, s)
	if strings.Contains(trail, secret) {
		t.Errorf("a password or a message reached the audit trail:\n%s", trail)
	}
	for _, want := range []string{
		"database.redis.config.set cache 200", `"name":"slowlog-max-len"`, `"value":"77"`, `"name":"masterauth"`,
		"database.redis.slowlog.reset cache 200", "database.redis.save cache 200", "database.redis.client.kill cache 200",
		"database.redis.client.kill cache 404", "database.redis.acl.set cache 200", `"user":"jdb4api-app"`,
		`"authentication":"changed"`, `"commands":["+@read","-@dangerous"]`, "database.redis.acl.delete cache 200",
		"database.redis.publish cache 200",
	} {
		if !strings.Contains(trail, want) {
			t.Errorf("the audit trail has no %s:\n%s", want, trail)
		}
	}
}

// feed opens one of the two sockets and collects its frames until it closes.
func feed(t *testing.T, h http.Handler, path string, during func()) (frames []map[string]any, closed error) {
	t.Helper()
	server := httptest.NewServer(h)
	defer server.Close()
	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+path, nil)
	if err != nil {
		body := ""
		if resp != nil {
			buf := make([]byte, 512)
			n, _ := resp.Body.Read(buf)
			body = string(buf[:n])
		}
		t.Fatalf("dial %s: %v %s", path, err, body)
	}
	defer conn.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn.SetReadDeadline(time.Now().Add(20 * time.Second))
			_, raw, err := conn.ReadMessage()
			if err != nil {
				closed = err
				return
			}
			var frame map[string]any
			if json.Unmarshal(raw, &frame) == nil {
				frames = append(frames, frame)
			}
		}
	}()
	if during != nil {
		during()
	}
	select {
	case <-done:
	case <-time.After(25 * time.Second):
		t.Fatalf("%s never closed", path)
	}
	return frames, closed
}

func frameTypes(frames []map[string]any) string {
	types := make([]string, len(frames))
	for i, f := range frames {
		types[i], _ = f["type"].(string)
	}
	return strings.Join(types, " ")
}

func TestLiveAPIRedisMonitorIsBoundedAndClosesCleanly(t *testing.T) {
	s, h, id, direct := redisLive(t, "JD_TEST_REDIS_ADMIN_DSN", auth.RoleAdmin)
	ctx := context.Background()
	base := pathf("/databases/%d/redis/monitor", id)
	traffic := func(stop <-chan struct{}) {
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			direct.Set(ctx, redisAPIPrefix+"watched", fmt.Sprintf("value-%d", i), time.Minute)
			time.Sleep(5 * time.Millisecond)
		}
	}

	// Ended by its message cap.
	stop := make(chan struct{})
	go traffic(stop)
	frames, closed := feed(t, h, base+"?seconds=20&max=25", nil)
	close(stop)
	if len(frames) < 3 || frames[0]["type"] != "meta" || frames[len(frames)-1]["type"] != "end" {
		t.Fatalf("frames = %s", frameTypes(frames))
	}
	end := frames[len(frames)-1]["data"].(map[string]any)
	if end["reason"] != "limit" || end["count"] != float64(25) {
		t.Errorf("end = %v", end)
	}
	seen := 0
	found := false
	for _, f := range frames[1 : len(frames)-1] {
		if f["type"] != "commands" {
			t.Fatalf("a %v frame in the feed", f["type"])
		}
		for _, raw := range f["data"].([]any) {
			ev := raw.(map[string]any)
			seen++
			if ev["command"] == "SET" && fmt.Sprint(ev["args"].([]any)[0]) == redisAPIPrefix+"watched" {
				found = true
			}
		}
	}
	if seen != 25 || !found {
		t.Errorf("%d commands in the feed (want 25), the SET among them: %v", seen, found)
	}
	if !websocket.IsCloseError(closed, websocket.CloseNormalClosure) {
		t.Errorf("the socket ended with %v, want a normal close", closed)
	}

	// Ended by its clock, on a quiet server.
	started := time.Now()
	frames, closed = feed(t, h, base+"?seconds=1&max=100000", nil)
	if took := time.Since(started); took < 900*time.Millisecond || took > 6*time.Second {
		t.Errorf("a one-second feed ran for %v", took)
	}
	if len(frames) < 2 || frames[len(frames)-1]["type"] != "end" || frames[len(frames)-1]["data"].(map[string]any)["reason"] != "duration" {
		t.Errorf("frames = %s", frameTypes(frames))
	}
	if !websocket.IsCloseError(closed, websocket.CloseNormalClosure) {
		t.Errorf("the socket ended with %v, want a normal close", closed)
	}

	// Nothing is left attached to the server afterwards.
	time.Sleep(200 * time.Millisecond)
	for _, line := range strings.Split(direct.ClientList(ctx).Val(), "\n") {
		if strings.Contains(line, "cmd=monitor") {
			t.Errorf("a monitoring connection is still open: %s", line)
		}
	}
	// Opening one is recorded when it opens, with its bounds.
	if trail := auditTrail(t, s); strings.Count(trail, "database.redis.monitor.open cache 200") != 2 || !strings.Contains(trail, `"max":25`) {
		t.Errorf("audit trail:\n%s", trail)
	}
	// Out-of-range bounds are clamped rather than obeyed.
	req := httptest.NewRequest(http.MethodGet, "/x?seconds=999999&max=99999999", nil)
	if d, n := redisFeedBounds(req, redisMonitorDefaultSeconds, redisMonitorMaxSeconds, redisMonitorDefaultEvents, redisMonitorMaxEvents); d != redisMonitorMaxSeconds*time.Second || n != redisMonitorMaxEvents {
		t.Errorf("bounds = %v, %d", d, n)
	}
}

func TestLiveAPIRedisSubscribeIsBoundedAndClosesCleanly(t *testing.T) {
	s, h, id, direct := redisLive(t, "JD_TEST_REDIS_ADMIN_DSN", auth.RoleAdmin)
	ctx := context.Background()
	path := pathf("/databases/%d/redis/subscribe", id) + "?channel=jdb4api.exact&pattern=jdb4api.p.*&seconds=15&max=3"
	frames, closed := feed(t, h, path, func() {
		// Published once somebody is listening, and until the feed has had
		// its fill.
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if n, _ := direct.Publish(ctx, "jdb4api.exact", "hello").Result(); n > 0 {
				direct.Publish(ctx, "jdb4api.p.one", "\xff\x00")
				direct.Publish(ctx, "jdb4api.exact", "third")
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
	if len(frames) < 3 || frames[0]["type"] != "meta" || frames[len(frames)-1]["type"] != "end" {
		t.Fatalf("frames = %s", frameTypes(frames))
	}
	if end := frames[len(frames)-1]["data"].(map[string]any); end["reason"] != "limit" || end["count"] != float64(3) {
		t.Errorf("end = %v", end)
	}
	var messages []map[string]any
	for _, f := range frames[1 : len(frames)-1] {
		for _, raw := range f["data"].([]any) {
			messages = append(messages, raw.(map[string]any))
		}
	}
	if len(messages) != 3 || messages[0]["channel"] != "jdb4api.exact" || messages[0]["payload"] != "hello" {
		t.Fatalf("messages = %v", messages)
	}
	if messages[1]["pattern"] != "jdb4api.p.*" || fmt.Sprint(messages[1]["payload"]) != "map[base64:/wA=]" {
		t.Errorf("the pattern message = %v", messages[1])
	}
	if !websocket.IsCloseError(closed, websocket.CloseNormalClosure) {
		t.Errorf("the socket ended with %v, want a normal close", closed)
	}
	time.Sleep(300 * time.Millisecond)
	if n := direct.PubSubNumSub(ctx, "jdb4api.exact").Val()["jdb4api.exact"]; n != 0 {
		t.Errorf("%d subscribers are still attached after the feed closed", n)
	}
	if trail := auditTrail(t, s); !strings.Contains(trail, "database.redis.subscribe.open cache 200") || !strings.Contains(trail, `"channels":["jdb4api.exact"]`) {
		t.Errorf("audit trail:\n%s", trail)
	}
}

// An endpoint that is not a single plain server answers with what it is.
func TestLiveAPIRedisSentinelIsNamedAsOne(t *testing.T) {
	dsn := os.Getenv("JD_TEST_REDIS_SENTINEL_DSN")
	if dsn == "" {
		t.Skip("set JD_TEST_REDIS_SENTINEL_DSN to run this")
	}
	_, h, id := redisRouter(t, auth.RoleAdmin, dsn)
	var server struct {
		Mode      string `json:"mode"`
		Role      string `json:"role"`
		Notice    string `json:"notice"`
		Databases int    `json:"databases"`
	}
	rec := call(t, h, id, http.MethodGet, "/redis/server", "", &server)
	if rec.Code == http.StatusBadGateway {
		t.Skipf("sentinel unreachable: %s", rec.Body.String())
	}
	if server.Mode != "sentinel" || server.Role != "sentinel" || server.Notice == "" || server.Databases != 0 {
		t.Errorf("server = %+v", server)
	}
	for _, rt := range []redisRoute{
		{http.MethodGet, "/keys", ``},
		{http.MethodGet, "/keys/tree", ``},
		{http.MethodGet, "/keys/meta?key=k", ``},
		{http.MethodGet, "/redis/analysis", ``},
		{http.MethodPost, "/keys/value", `{"key":"k","value":"v"}`},
		{http.MethodPost, "/keys/bulk", `{"pattern":"*","action":"delete","dryRun":true}`},
	} {
		wantStatus(t, redisDo(t, h, id, rt), 400, "sentinel_endpoint", rt.method+" "+rt.path)
	}
	// The console still reaches it — it has commands of its own — and a key
	// command's refusal is the sentence, not "unknown command".
	var res dbx.RedisCommandResult
	call(t, h, id, http.MethodPost, "/redis/command", `{"command":"GET k"}`, &res)
	if res.Reply.Type != "error" || !strings.Contains(fmt.Sprint(res.Reply.Value), "Sentinel") {
		t.Errorf("GET on a sentinel = %+v", res.Reply)
	}
	call(t, h, id, http.MethodPost, "/redis/command", `{"command":"PING"}`, &res)
	if res.Reply.Type != "status" || res.Reply.Value != "PONG" {
		t.Errorf("PING on a sentinel = %+v", res.Reply)
	}
	// What a sentinel can answer about itself, it does.
	for _, path := range []string{"/redis/clients", "/redis/config", "/redis/slowlog", "/redis/latency", "/redis/commands", "/stats", "/schemas"} {
		if rec := call(t, h, id, http.MethodGet, path, "", nil); rec.Code != http.StatusOK {
			t.Errorf("%s on a sentinel: %d %s", path, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
}
