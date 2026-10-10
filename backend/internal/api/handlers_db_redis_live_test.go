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

// On a server that has JSON, a path removes what it selects and an empty one
// is refused: read as the root, it removed the document.
func TestLiveAPIRedisAJSONPathRemovesOnlyWhatItNames(t *testing.T) {
	_, h, id, direct := redisLive(t, "JD_TEST_REDIS_DSN", auth.RoleAdmin)
	ctx := context.Background()
	var server struct {
		Features dbx.RedisFeatures `json:"features"`
	}
	call(t, h, id, http.MethodGet, "/redis/server", "", &server)
	if !server.Features.JSON {
		t.Skip("the server at JD_TEST_REDIS_DSN has no JSON commands")
	}
	doc := redisAPIPrefix + "inside:j"
	if err := direct.Do(ctx, "JSON.SET", doc, "$", `{"user":{"email":"a@b","name":"Ann"}}`).Err(); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"key":"` + doc + `","path":""}`,
		`{"key":"` + doc + `","type":"json","path":""}`,
		`{"key":"` + doc + `","path":"user.email"}`,
	} {
		rec := call(t, h, id, http.MethodDelete, "/keys", body, nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s", body, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
		if got, _ := direct.Do(ctx, "JSON.GET", doc, "$.user.email").Text(); got != `["a@b"]` {
			t.Fatalf("%s changed the document: %s", body, got)
		}
	}
	var out struct {
		Removed int64 `json:"removed"`
	}
	wantStatus(t, call(t, h, id, http.MethodDelete, "/keys", `{"key":"`+doc+`","path":"$.user.email"}`, &out), 200, "", "removing one path")
	if got, _ := direct.Do(ctx, "JSON.GET", doc, "$").Text(); out.Removed != 1 || got != `[{"user":{"name":"Ann"}}]` {
		t.Errorf("removing one path removed %d and left %s", out.Removed, got)
	}
	// The root, asked for by name, is the document.
	wantStatus(t, call(t, h, id, http.MethodDelete, "/keys", `{"key":"`+doc+`","path":"$"}`, &out), 200, "", "removing the root")
	if out.Removed != 1 || direct.Exists(ctx, doc).Val() != 0 {
		t.Errorf("removing the root removed %d and left the key", out.Removed)
	}
}

// A consumer may be called "". Naming it removes that consumer; read as
// naming none, the same request destroyed the group and what every other
// consumer had pending.
func TestLiveAPIRedisRemovingAConsumerLeavesItsGroup(t *testing.T) {
	s, h, id, direct := redisLive(t, "JD_TEST_REDIS_DSN", auth.RoleAdmin)
	ctx := context.Background()
	key := redisAPIPrefix + "consumers:x"
	for i := 0; i < 2; i++ {
		direct.XAdd(ctx, &redis.XAddArgs{Stream: key, Values: map[string]any{"f": "v"}})
	}
	direct.XGroupCreate(ctx, key, "g", "0")
	// Each takes one entry; a negative Block keeps a read that finds none
	// from waiting for one.
	for _, consumer := range []string{"c1", ""} {
		err := direct.XReadGroup(ctx, &redis.XReadGroupArgs{Group: "g", Consumer: consumer, Streams: []string{key, ">"}, Count: 1, Block: -1}).Err()
		if err != nil && consumer == "" {
			t.Skipf("this server has no consumer with an empty name: %v", err)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	groups := func() []dbx.RedisStreamGroup {
		var info dbx.RedisStreamInfo
		call(t, h, id, http.MethodGet, "/keys/stream?key="+key, "", &info)
		return info.Groups
	}
	if g := groups(); len(g) != 1 || len(g[0].Consumers) != 2 {
		t.Fatalf("before: %+v", g)
	}
	wantStatus(t, call(t, h, id, http.MethodDelete, "/keys/stream/groups", `{"key":"`+key+`","group":"g","consumer":""}`, nil), 200, "", "removing the consumer with no name")
	if g := groups(); len(g) != 1 || len(g[0].Consumers) != 1 || g[0].Consumers[0].Name != "c1" || g[0].Pending != 1 {
		t.Fatalf("removing the consumer with no name left %+v", g)
	}
	wantStatus(t, call(t, h, id, http.MethodDelete, "/keys/stream/groups", `{"key":"`+key+`","group":"g","consumer":"c1"}`, nil), 200, "", "removing a named consumer")
	if g := groups(); len(g) != 1 || len(g[0].Consumers) != 0 {
		t.Fatalf("removing a named consumer left %+v", g)
	}
	// With no consumer in the request at all, it is the group that goes.
	wantStatus(t, call(t, h, id, http.MethodDelete, "/keys/stream/groups", `{"key":"`+key+`","group":"g"}`, nil), 200, "", "destroying the group")
	if g := groups(); len(g) != 0 {
		t.Fatalf("destroying the group left %+v", g)
	}
	trail := auditTrail(t, s)
	if strings.Count(trail, "database.redis.stream.consumer.delete cache 200") != 2 || strings.Count(trail, "database.redis.stream.group.delete cache 200") != 1 {
		t.Errorf("audit trail:\n%s", trail)
	}
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

// redisFeatures is what the server behind a live connection says it has.
func redisFeatures(t *testing.T, h http.Handler, id int64) dbx.RedisFeatures {
	t.Helper()
	var server struct {
		Features dbx.RedisFeatures `json:"features"`
	}
	wantStatus(t, call(t, h, id, http.MethodGet, "/redis/server", "", &server), 200, "", "the server's profile")
	return server.Features
}

// A key's idle time is how long since anything used it, and the dashboard
// looking at a key is not the application using it. Listing a key read its
// size and opening it read its contents, and each reset the clock: a key
// nothing had touched for a week read "idle for 0 seconds" to whoever came to
// find out. Every route that only looks now leaves the clock alone, on a
// server that can be asked to (Redis 7.2, Valkey). A write is still use.
func TestLiveAPIRedisLookingAtAKeyDoesNotUseIt(t *testing.T) {
	_, h, id, direct := redisLive(t, "JD_TEST_REDIS_DSN", auth.RoleAdmin)
	ctx := context.Background()
	features := redisFeatures(t, h, id)
	if !features.NoTouch || !features.ObjectIdleTime {
		t.Skipf("this server cannot leave a key's idle time alone, or keeps none: %+v", features)
	}
	prefix := redisAPIPrefix + "idle:"
	keys := map[string]string{
		"string": prefix + "string", "hash": prefix + "hash", "list": prefix + "list",
		"set": prefix + "set", "zset": prefix + "zset", "stream": prefix + "stream",
	}
	direct.Set(ctx, keys["string"], "value", 0)
	direct.HSet(ctx, keys["hash"], "f", "v")
	direct.RPush(ctx, keys["list"], "a", "b")
	direct.SAdd(ctx, keys["set"], "m")
	direct.ZAdd(ctx, keys["zset"], redis.Z{Score: 1, Member: "m"})
	direct.XAdd(ctx, &redis.XAddArgs{Stream: keys["stream"], Values: map[string]any{"f": "v"}})
	direct.XGroupCreate(ctx, keys["stream"], "g", "0")
	// Put back as a key nothing has used for an hour: a restore may say how
	// long its key had been idle, which saves the test an hour of waiting.
	const hour = 3600
	for _, key := range keys {
		payload, err := direct.Dump(ctx, key).Result()
		if err != nil {
			t.Fatal(err)
		}
		direct.Del(ctx, key)
		if err := direct.Do(ctx, "RESTORE", key, 0, payload, "IDLETIME", hour).Err(); err != nil {
			t.Skipf("this server restores no idle time: %v", err)
		}
	}
	idle := func(key string) int64 {
		t.Helper()
		// OBJECT does not count as use on any version.
		n, err := direct.Do(ctx, "OBJECT", "IDLETIME", key).Int64()
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	for kind, key := range keys {
		if n := idle(key); n < hour {
			t.Fatalf("the %s was restored idle for %d seconds, want an hour", kind, n)
		}
	}

	q := url.QueryEscape
	for _, path := range []string{
		"/keys?pattern=" + q(prefix+"*") + "&memory=1",
		"/keys?pattern=" + q(prefix+"*") + "&type=hash",
		"/keys?pattern=" + q(keys["list"]),
		"/keys/tree?prefix=" + q(prefix),
		"/keys/meta?key=" + q(keys["string"]),
		"/keys/meta?key=" + q(keys["hash"]),
		"/keys/members?key=" + q(keys["string"]),
		"/keys/members?key=" + q(keys["hash"]),
		"/keys/members?key=" + q(keys["list"]),
		"/keys/members?key=" + q(keys["set"]),
		"/keys/members?key=" + q(keys["zset"]),
		"/keys/members?key=" + q(keys["stream"]),
		"/keys/value?key=" + q(keys["hash"]),
		"/keys/raw?key=" + q(keys["string"]),
		"/keys/raw?key=" + q(keys["hash"]) + "&field=f",
		"/keys/raw?key=" + q(keys["list"]) + "&index=1",
		"/keys/raw?key=" + q(keys["set"]) + "&member=m",
		"/keys/raw?key=" + q(keys["zset"]) + "&member=m",
		"/keys/stream?key=" + q(keys["stream"]),
		"/keys/stream/pending?key=" + q(keys["stream"]) + "&group=g",
		"/redis/analysis",
	} {
		wantStatus(t, call(t, h, id, http.MethodGet, path, "", nil), 200, "", "GET "+path)
		for kind, key := range keys {
			if n := idle(key); n < hour {
				t.Fatalf("GET %s reset the idle time of the %s to %d seconds", path, kind, n)
			}
		}
	}
	// And what the page is told is the application's hour, twice running.
	for range 2 {
		var meta dbx.RedisKeyMeta
		call(t, h, id, http.MethodGet, "/keys/meta?key="+q(keys["hash"]), "", &meta)
		if meta.IdleSeconds == nil || *meta.IdleSeconds < hour {
			t.Fatalf("the key's facts say it has been idle for %v seconds, want the hour", meta.IdleSeconds)
		}
	}

	// An edit made from the dashboard is use of the key, and counts.
	wantStatus(t, call(t, h, id, http.MethodPost, "/keys/value", `{"key":"`+keys["string"]+`","value":"edited"}`, nil), 200, "", "an edit")
	if n := idle(keys["string"]); n >= hour {
		t.Errorf("an edit left the key idle for %d seconds: a write is use", n)
	}
}

// One field of a hash may have an expiry of its own, on Redis 7.4 and Valkey
// 9. The page could show it and had no route to change it with, so it went
// through the console's. The two routes are the key's own, a level down: the
// same three ways of saying when, a ttl of zero that removes the expiry and
// never the field, and a moment that has passed refused as the delete it is.
func TestLiveAPIRedisAFieldsExpiryIsSetAndRemoved(t *testing.T) {
	s, h, id, direct := redisLive(t, "JD_TEST_REDIS_DSN", auth.RoleAdmin)
	ctx := context.Background()
	key := redisAPIPrefix + "fieldttl:h"
	const named = "field-whose-name-is-data"
	direct.HSet(ctx, key, named, "1", "b", "2", "c", "3")
	direct.Set(ctx, redisAPIPrefix+"fieldttl:s", "v", 0)

	if !redisFeatures(t, h, id).HashFieldTTL {
		// A server that keeps none says so, and is asked for nothing.
		rec := call(t, h, id, http.MethodPost, "/keys/field/expire", `{"key":"`+key+`","field":"b","ttl":60}`, nil)
		wantStatus(t, rec, 400, "unsupported", "a field expiry on a server without one")
		rec = call(t, h, id, http.MethodPost, "/keys/field/persist", `{"key":"`+key+`","field":"b"}`, nil)
		wantStatus(t, rec, 400, "unsupported", "removing a field expiry on a server without one")
		if n := direct.HLen(ctx, key).Val(); n != 3 {
			t.Errorf("the hash holds %d fields after the refusals, want 3", n)
		}
		return
	}
	type answer struct {
		OK     bool `json:"ok"`
		Fields []struct {
			Field dbx.RedisBytes `json:"field"`
			PTTL  int64          `json:"pttl"`
		} `json:"fields"`
	}
	left := func(field string) int64 {
		t.Helper()
		ttls, err := direct.Do(ctx, "HPTTL", key, "FIELDS", 1, field).Int64Slice()
		if err != nil || len(ttls) != 1 {
			t.Fatalf("HPTTL %s: %v %v", field, ttls, err)
		}
		return ttls[0]
	}

	var got answer
	wantStatus(t, call(t, h, id, http.MethodPost, "/keys/field/expire", `{"key":"`+key+`","field":"`+named+`","ttl":120}`, &got), 200, "", "an expiry in seconds")
	if len(got.Fields) != 1 || string(got.Fields[0].Field) != named || got.Fields[0].PTTL <= 0 || got.Fields[0].PTTL > 120_000 {
		t.Errorf("answer = %+v, want the field with two minutes left", got)
	}
	if ms := left(named); ms <= 0 || ms > 120_000 {
		t.Errorf("the server says %d ms are left, want up to two minutes", ms)
	}
	// Several at once, one of them not there.
	got = answer{}
	wantStatus(t, call(t, h, id, http.MethodPost, "/keys/field/expire", `{"key":"`+key+`","members":["b","nope"],"ttlMs":60000}`, &got), 200, "", "an expiry in milliseconds on several fields")
	if len(got.Fields) != 2 || got.Fields[0].PTTL <= 0 || got.Fields[1].PTTL != -2 {
		t.Errorf("answer = %+v, want time left on b and -2 for a field that is not there", got)
	}
	at := time.Now().Add(time.Hour).UnixMilli()
	wantStatus(t, call(t, h, id, http.MethodPost, "/keys/field/expire", fmt.Sprintf(`{"key":%q,"field":"c","at":%d}`, key, at), nil), 200, "", "an expiry at a moment")
	if ms := left("c"); ms < 3_500_000 || ms > 3_600_000 {
		t.Errorf("a field set to expire in an hour has %d ms left", ms)
	}

	// Removed, by the route that says so and by a ttl of zero. Neither takes
	// the field.
	got = answer{}
	wantStatus(t, call(t, h, id, http.MethodPost, "/keys/field/persist", `{"key":"`+key+`","field":"`+named+`"}`, &got), 200, "", "removing an expiry")
	if len(got.Fields) != 1 || got.Fields[0].PTTL != -1 || left(named) != -1 {
		t.Errorf("after persist: %+v, the server says %d", got, left(named))
	}
	wantStatus(t, call(t, h, id, http.MethodPost, "/keys/field/expire", `{"key":"`+key+`","field":"b","ttl":0}`, nil), 200, "", "a ttl of zero")
	if left("b") != -1 || !direct.HExists(ctx, key, "b").Val() {
		t.Errorf("a ttl of zero left b with %d ms and present=%v, want no expiry and the field kept", left("b"), direct.HExists(ctx, key, "b").Val())
	}

	// A moment that has passed would delete the field, and is refused as that.
	past := time.Now().Add(-time.Minute).UnixMilli()
	wantStatus(t, call(t, h, id, http.MethodPost, "/keys/field/expire", fmt.Sprintf(`{"key":%q,"field":"c","at":%d}`, key, past), nil), 400, "bad_request", "an expiry that has passed")
	wantStatus(t, call(t, h, id, http.MethodPost, "/keys/field/expire", `{"key":"`+key+`","field":"c","ttl":60,"ttlMs":5}`, nil), 400, "bad_request", "an expiry given two ways")
	if n := direct.HLen(ctx, key).Val(); n != 3 {
		t.Errorf("the hash holds %d fields after the refusals, want 3", n)
	}
	wantStatus(t, call(t, h, id, http.MethodPost, "/keys/field/expire", `{"key":"`+key+`","field":"nope","ttl":60}`, nil), 404, "member_not_found", "a field that is not there")
	wantStatus(t, call(t, h, id, http.MethodPost, "/keys/field/expire", `{"key":"`+redisAPIPrefix+`fieldttl:none","field":"a","ttl":60}`, nil), 404, "key_not_found", "a key that is not there")
	wantStatus(t, call(t, h, id, http.MethodPost, "/keys/field/expire", `{"key":"`+redisAPIPrefix+`fieldttl:s","field":"a","ttl":60}`, nil), 400, "bad_request", "a key that is not a hash")

	// service.control may, as it may set a key's expiry; a reader may not.
	_, limited, limitedID := redisRouter(t, auth.RoleLimited, os.Getenv("JD_TEST_REDIS_DSN"))
	wantStatus(t, call(t, limited, limitedID, http.MethodPost, "/keys/field/expire", `{"key":"`+key+`","field":"b","ttl":60}`, nil), 200, "", "a field expiry by service.control")
	_, reader, readerID := redisRouter(t, auth.RoleReadOnly, os.Getenv("JD_TEST_REDIS_DSN"))
	wantStatus(t, call(t, reader, readerID, http.MethodPost, "/keys/field/persist", `{"key":"`+key+`","field":"b"}`, nil), 403, "forbidden", "a field expiry by a reader")

	// The trail says which key and how many fields, never which fields: a
	// field's name is data as often as it is a label.
	trail := auditTrail(t, s)
	if !strings.Contains(trail, "database.redis.field.expire cache 200") || !strings.Contains(trail, "database.redis.field.persist cache 200") ||
		!strings.Contains(trail, `"fields":2`) || !strings.Contains(trail, key) {
		t.Errorf("audit trail:\n%s", trail)
	}
	if strings.Contains(trail, named) {
		t.Errorf("a field's name is on the audit trail:\n%s", trail)
	}
}

// A group's pending entries belong to the consumer that was handed them, and
// when that consumer is gone they sit there. The page could acknowledge one
// and could not give it to anybody else. A claim moves the ones named, or
// whatever has been pending long enough, to a consumer of the group — the
// ids only, in both directions: what an entry holds is not asked for.
func TestLiveAPIRedisPendingEntriesAreClaimed(t *testing.T) {
	s, h, id, direct := redisLive(t, "JD_TEST_REDIS_DSN", auth.RoleAdmin)
	ctx := context.Background()
	key := redisAPIPrefix + "claim:x"
	var ids []string
	for i := 0; i < 3; i++ {
		entry, err := direct.XAdd(ctx, &redis.XAddArgs{Stream: key, Values: map[string]any{"secret": "entry-payload"}}).Result()
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, entry)
	}
	direct.XGroupCreate(ctx, key, "g", "0")
	if err := direct.XReadGroup(ctx, &redis.XReadGroupArgs{Group: "g", Consumer: "alice", Streams: []string{key, ">"}, Count: 3, Block: -1}).Err(); err != nil {
		t.Fatal(err)
	}
	owners := func() map[string]string {
		t.Helper()
		var page dbx.RedisStreamPendingPage
		call(t, h, id, http.MethodGet, "/keys/stream/pending?key="+key+"&group=g", "", &page)
		out := map[string]string{}
		for _, e := range page.Entries {
			out[e.ID] = string(e.Consumer)
		}
		return out
	}
	if got := owners(); len(got) != 3 || got[ids[0]] != "alice" {
		t.Fatalf("before: %v", got)
	}

	var claimed dbx.RedisStreamClaimed
	body := fmt.Sprintf(`{"key":%q,"group":"g","consumer":"bob","ids":[%q]}`, key, ids[0])
	wantStatus(t, call(t, h, id, http.MethodPost, "/keys/stream/claim", body, &claimed), 200, "", "claiming one entry")
	if len(claimed.Claimed) != 1 || claimed.Claimed[0] != ids[0] || !claimed.Done {
		t.Errorf("claimed = %+v, want the one entry", claimed)
	}
	if got := owners(); got[ids[0]] != "bob" || got[ids[1]] != "alice" || got[ids[2]] != "alice" {
		t.Errorf("after the claim: %v", got)
	}

	// Only what has been pending long enough: nothing has been for an hour.
	claimed = dbx.RedisStreamClaimed{}
	body = fmt.Sprintf(`{"key":%q,"group":"g","consumer":"carol","ids":[%q,%q],"minIdleMs":3600000}`, key, ids[1], ids[2])
	wantStatus(t, call(t, h, id, http.MethodPost, "/keys/stream/claim", body, &claimed), 200, "", "claiming entries that are not idle yet")
	if len(claimed.Claimed) != 0 || owners()[ids[1]] != "alice" {
		t.Errorf("entries a consumer was handed a moment ago were taken from it: %+v %v", claimed, owners())
	}

	// Whatever is pending, without naming it.
	if redisFeatures(t, h, id).StreamAutoClaim {
		claimed = dbx.RedisStreamClaimed{}
		body = fmt.Sprintf(`{"key":%q,"group":"g","consumer":"carol","auto":true,"count":2}`, key)
		wantStatus(t, call(t, h, id, http.MethodPost, "/keys/stream/claim", body, &claimed), 200, "", "an automatic claim")
		if len(claimed.Claimed) != 2 || claimed.Done || claimed.Cursor == "" || claimed.Cursor == "0-0" {
			t.Fatalf("the first page of an automatic claim = %+v, want two entries and somewhere to continue", claimed)
		}
		next := dbx.RedisStreamClaimed{}
		body = fmt.Sprintf(`{"key":%q,"group":"g","consumer":"carol","auto":true,"count":2,"cursor":%q}`, key, claimed.Cursor)
		wantStatus(t, call(t, h, id, http.MethodPost, "/keys/stream/claim", body, &next), 200, "", "the rest of an automatic claim")
		if len(next.Claimed) != 1 || !next.Done {
			t.Errorf("the second page = %+v, want the last entry and the end", next)
		}
		for entry, owner := range owners() {
			if owner != "carol" {
				t.Errorf("%s is pending for %q after everything was claimed for carol", entry, owner)
			}
		}
	} else {
		body = fmt.Sprintf(`{"key":%q,"group":"g","consumer":"carol","auto":true}`, key)
		wantStatus(t, call(t, h, id, http.MethodPost, "/keys/stream/claim", body, nil), 400, "unsupported", "an automatic claim on a server without one")
	}

	for what, body := range map[string]string{
		"no consumer":            fmt.Sprintf(`{"key":%q,"group":"g","ids":[%q]}`, key, ids[0]),
		"no group":               fmt.Sprintf(`{"key":%q,"consumer":"bob","ids":[%q]}`, key, ids[0]),
		"nothing to claim":       fmt.Sprintf(`{"key":%q,"group":"g","consumer":"bob"}`, key),
		"both ways at once":      fmt.Sprintf(`{"key":%q,"group":"g","consumer":"bob","auto":true,"ids":[%q]}`, key, ids[0]),
		"not an entry id":        fmt.Sprintf(`{"key":%q,"group":"g","consumer":"bob","ids":["1-0; FLUSHALL"]}`, key),
		"a negative idle time":   fmt.Sprintf(`{"key":%q,"group":"g","consumer":"bob","ids":[%q],"minIdleMs":-1}`, key, ids[0]),
		"a group that is not it": fmt.Sprintf(`{"key":%q,"group":"nope","consumer":"bob","ids":[%q]}`, key, ids[0]),
	} {
		wantStatus(t, call(t, h, id, http.MethodPost, "/keys/stream/claim", body, nil), 400, "bad_request", what)
	}
	if n := direct.XLen(ctx, key).Val(); n != 3 {
		t.Errorf("the stream holds %d entries after being claimed from, want 3", n)
	}

	_, reader, readerID := redisRouter(t, auth.RoleReadOnly, os.Getenv("JD_TEST_REDIS_DSN"))
	body = fmt.Sprintf(`{"key":%q,"group":"g","consumer":"bob","ids":[%q]}`, key, ids[0])
	wantStatus(t, call(t, reader, readerID, http.MethodPost, "/keys/stream/claim", body, nil), 403, "forbidden", "a claim by a reader")

	trail := auditTrail(t, s)
	if !strings.Contains(trail, "database.redis.stream.claim cache 200") || !strings.Contains(trail, `"consumer":"bob"`) || !strings.Contains(trail, `"claimed":1`) {
		t.Errorf("audit trail:\n%s", trail)
	}
	if strings.Contains(trail, "entry-payload") {
		t.Errorf("an entry's contents are on the audit trail:\n%s", trail)
	}
}

// A page shows the first 64 KiB of a long member and says how long it really
// is. A string, a hash field and a list element could then be downloaded
// whole; a member of a set or of a sorted set could not, because it has no
// name to ask for it by except itself, and the page holds only its start. It
// is asked for by that: how it begins, and how many bytes it is.
func TestLiveAPIRedisAWholeMemberOfASetIsDownloaded(t *testing.T) {
	_, h, id, direct := redisLive(t, "JD_TEST_REDIS_DSN", auth.RoleReadOnly)
	ctx := context.Background()
	// Not text, and long enough to be cut twice over.
	long := make([]byte, 200<<10)
	for i := range long {
		long[i] = byte(i*7 + i>>8)
	}
	// The same beginning and another size, and the same size with another
	// beginning: neither is the one asked for.
	longer := append(append([]byte{}, long...), "tail"...)
	other := append([]byte{}, long...)
	other[0] ^= 0xff
	for kind, key := range map[string]string{"set": redisAPIPrefix + "whole:set", "zset": redisAPIPrefix + "whole:zset"} {
		for i, member := range [][]byte{longer, other, long, []byte("small"), []byte("")} {
			var err error
			if kind == "set" {
				err = direct.SAdd(ctx, key, member).Err()
			} else {
				err = direct.ZAdd(ctx, key, redis.Z{Score: float64(i), Member: member}).Err()
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		// What the page was given: the start of it, marked as cut, with its size.
		var page dbx.RedisMembers
		call(t, h, id, http.MethodGet, "/keys/members?key="+key, "", &page)
		var begins []byte
		for _, row := range page.Rows {
			if row.Truncated && row.Bytes != nil && *row.Bytes == int64(len(long)) && row.Value != nil && (*row.Value)[0] == dbx.RedisBytes(long)[0] {
				begins = []byte(*row.Value)
			}
		}
		if len(begins) == 0 || len(begins) >= len(long) {
			t.Fatalf("%s: the page did not hand over the start of the long member: %d rows", kind, len(page.Rows))
		}

		for what, prefix := range map[string][]byte{"all the page kept": begins, "its first kilobyte": begins[:1024]} {
			path := fmt.Sprintf("/keys/raw?key=%s&memberB64=%s&bytes=%d", key, url.QueryEscape(base64.StdEncoding.EncodeToString(prefix)), len(long))
			rec := call(t, h, id, http.MethodGet, path, "", nil)
			if rec.Code != http.StatusOK || rec.Body.String() != string(long) {
				t.Fatalf("%s named by %s: %d with %d bytes, want the %d of the member", kind, what, rec.Code, rec.Body.Len(), len(long))
			}
			if rec.Header().Get("Content-Length") != fmt.Sprint(len(long)) || !strings.Contains(rec.Header().Get("Content-Disposition"), "attachment") {
				t.Errorf("%s: headers = %v", kind, rec.Header())
			}
		}
		// A member that is whole is named by itself, the empty one included.
		for _, member := range []string{"small", ""} {
			rec := call(t, h, id, http.MethodGet, "/keys/raw?key="+key+"&member="+member, "", nil)
			if rec.Code != http.StatusOK || rec.Body.String() != member {
				t.Errorf("%s: the member %q = %d %q", kind, member, rec.Code, rec.Body.String())
			}
		}
		wantStatus(t, call(t, h, id, http.MethodGet, "/keys/raw?key="+key+"&member=nope", "", nil), 404, "member_not_found", kind+": a member that is not there")
		wantStatus(t, call(t, h, id, http.MethodGet, "/keys/raw?key="+key+"&member=small&bytes=9", "", nil), 404, "member_not_found", kind+": the right beginning and the wrong size")
		wantStatus(t, call(t, h, id, http.MethodGet, "/keys/raw?key="+key+"&member=small&bytes=2", "", nil), 400, "bad_request", kind+": a size shorter than the beginning")
		wantStatus(t, call(t, h, id, http.MethodGet, "/keys/raw?key="+key+"&member=&bytes=5", "", nil), 400, "bad_request", kind+": a size and no beginning")
		wantStatus(t, call(t, h, id, http.MethodGet, "/keys/raw?key="+key, "", nil), 400, "bad_request", kind+": no member named")
	}
}
