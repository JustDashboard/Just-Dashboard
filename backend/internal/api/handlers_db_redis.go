package api

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"
)

// Redis has its own file because it shares nothing with the SQL handlers but
// the connection row: a client opened per request rather than drawn from the
// pool, and a vocabulary of keys rather than rows.

// mountDatabaseRedisRoutes registers every route that speaks Redis. It is
// called inside the /databases route, so paths are relative to it and each
// group states the capability it needs.
//
// Paths under /keys are about what is stored; paths under /redis are about
// the server that stores it.
func (s *Server) mountDatabaseRedisRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/{id}/keys", s.handle(s.handleRedisScan))
	r.Method(http.MethodGet, "/{id}/keys/value", s.handle(s.handleRedisGet))
	r.Method(http.MethodGet, "/{id}/keys/tree", s.handle(s.handleRedisTree))
	r.Method(http.MethodGet, "/{id}/keys/meta", s.handle(s.handleRedisMeta))
	r.Method(http.MethodGet, "/{id}/keys/members", s.handle(s.handleRedisMembers))
	// The whole of one value, as a download: what a page cut short.
	r.Method(http.MethodGet, "/{id}/keys/raw", s.handle(s.handleRedisRaw))
	r.Method(http.MethodGet, "/{id}/keys/stream", s.handle(s.handleRedisStream))
	r.Method(http.MethodGet, "/{id}/keys/stream/pending", s.handle(s.handleRedisStreamPending))
	// Reading the server is reading what it prints about itself, which the
	// SQL engines' activity, statements and settings already show any role.
	// What none of these return is a credential: the configuration withholds
	// every password and the user list every hash.
	r.Method(http.MethodGet, "/{id}/redis/server", s.handle(s.handleRedisServer))
	r.Method(http.MethodGet, "/{id}/redis/commandstats", s.handle(s.handleRedisCommandStats))
	r.Method(http.MethodGet, "/{id}/redis/latency", s.handle(s.handleRedisLatency))
	r.Method(http.MethodGet, "/{id}/redis/clients", s.handle(s.handleRedisClients))
	r.Method(http.MethodGet, "/{id}/redis/config", s.handle(s.handleRedisConfig))
	r.Method(http.MethodGet, "/{id}/redis/slowlog", s.handle(s.handleRedisSlowlog))
	r.Method(http.MethodGet, "/{id}/redis/analysis", s.handle(s.handleRedisAnalysis))
	r.Method(http.MethodGet, "/{id}/redis/commands", s.handle(s.handleRedisCommands))
	r.Method(http.MethodGet, "/{id}/redis/acl", s.handle(s.handleRedisACL))
	r.Method(http.MethodGet, "/{id}/redis/pubsub", s.handle(s.handleRedisPubSub))
	// Says what a console line would need, without running it.
	r.Method(http.MethodPost, "/{id}/redis/classify", s.handle(s.handleRedisClassify))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapServiceControl))
		r.Method(http.MethodPost, "/{id}/keys/value", s.handle(s.handleRedisSet))
		r.Method(http.MethodPost, "/{id}/keys/expire", s.handle(s.handleRedisExpire))
		r.Method(http.MethodPost, "/{id}/keys/persist", s.handle(s.handleRedisPersist))
		// Rename, copy and bulk are routine as asked for by default, and each
		// has one option that removes data: overwrite, replace, and the
		// delete and expire actions. The handlers demand the destructive
		// capability for those by hand, as the query runner does for SQL.
		r.Method(http.MethodPost, "/{id}/keys/rename", s.handle(s.handleRedisRename))
		r.Method(http.MethodPost, "/{id}/keys/copy", s.handle(s.handleRedisCopy))
		r.Method(http.MethodPost, "/{id}/keys/bulk", s.handle(s.handleRedisBulk))
		r.Method(http.MethodPost, "/{id}/keys/stream/groups", s.handle(s.handleRedisStreamGroup))
		r.Method(http.MethodPost, "/{id}/keys/stream/ack", s.handle(s.handleRedisStreamAck))
		// The console is a text box that can say anything, so nobody reaches
		// it on the read surface, and what a given line needs beyond this is
		// decided from the line.
		r.Method(http.MethodPost, "/{id}/redis/command", s.handle(s.handleRedisCommand))
		r.Method(http.MethodPost, "/{id}/redis/save", s.handle(s.handleRedisSave))
		r.Method(http.MethodPost, "/{id}/redis/publish", s.handle(s.handleRedisPublish))
	})
	s.destructive(r, func(r chi.Router) {
		r.Method(http.MethodDelete, "/{id}/keys", s.handle(s.handleRedisDelete))
		r.Method(http.MethodPost, "/{id}/keys/stream/trim", s.handle(s.handleRedisStreamTrim))
		r.Method(http.MethodDelete, "/{id}/keys/stream/groups", s.handle(s.handleRedisStreamGroupRemove))
		// Disconnecting a client stops whatever it was doing, the same as
		// killing a SQL session.
		r.Method(http.MethodPost, "/{id}/redis/clients/kill", s.handle(s.handleRedisClientKill))
		r.Method(http.MethodPost, "/{id}/redis/slowlog/reset", s.handle(s.handleRedisSlowlogReset))
	})
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodPut, "/{id}/redis/config", s.handle(s.handleRedisConfigSet))
		r.Method(http.MethodPut, "/{id}/redis/acl/{name}", s.handle(s.handleRedisACLSet))
		// MONITOR shows every command every client sends, with its arguments:
		// session tokens being written, passwords being checked. That is a
		// read of everything, and it is kept to administrators.
		r.Method(http.MethodGet, "/{id}/redis/monitor", s.handle(s.handleRedisMonitor))
		// A subscription is the other live feed, and a pattern of * is every
		// message any application on the server publishes, for as long as the
		// socket stays open. A key is read once, by name; this is whatever
		// comes next, from whoever sends it, and it is kept with MONITOR.
		r.Method(http.MethodGet, "/{id}/redis/subscribe", s.handle(s.handleRedisSubscribe))
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/{id}/redis/acl/{name}", s.handle(s.handleRedisACLDelete))
		})
	})
}

// redisRow resolves a connection row, refusing any connection that is not
// Redis.
func (s *Server) redisRow(r *http.Request) (*dbConnection, string, error) {
	id, err := parseID(r)
	if err != nil {
		return nil, "", err
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return nil, "", err
	}
	if conn.Driver != dbx.DriverRedis {
		return nil, "", httpx.BadRequest("this endpoint is for Redis connections")
	}
	return conn, dsn, nil
}

// redisDB reads a logical database number. The number rides on the query
// string because Redis selects it per connection rather than per statement.
//
// No number means the database the connection string names, and that is
// different from zero: with one read as the other, a connection string ending
// in /3 made database 0 unreachable and labelled database 3 as it.
func redisDB(raw string) (int, error) {
	if raw == "" {
		return dbx.RedisDSNDatabase, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 || n > 1<<16 {
		return 0, httpx.BadRequest("db must be a database number")
	}
	return n, nil
}

// redisClient resolves a connection to a Redis client on the database the
// request names.
func (s *Server) redisClient(r *http.Request) (*redis.Client, *dbConnection, error) {
	conn, dsn, err := s.redisRow(r)
	if err != nil {
		return nil, nil, err
	}
	db, err := redisDB(r.URL.Query().Get("db"))
	if err != nil {
		return nil, nil, err
	}
	client, err := dbx.RedisOpen(r.Context(), dsn, dbx.RedisOpenOptions{DB: db})
	if err != nil {
		return nil, conn, redisConnectFailed(dsn, err, db)
	}
	return client, conn, nil
}

// redisConnectFailed is the answer for a connection that could not be
// opened. db is the database the request asked for.
//
// A server that will not select the database the request named has nothing
// wrong with it; the request has, and is told so. The same refusal for the
// database the connection string names is the connection that is wrong, and
// reads as one that does not work.
func redisConnectFailed(dsn string, err error, db int) error {
	var refused *dbx.RedisDatabaseError
	if db != dbx.RedisDSNDatabase && errors.As(err, &refused) {
		return httpx.BadRequest("%s", connectError(dsn, err))
	}
	return connectFailed(dsn, err)
}

// redisKeyParam reads a key name from the query string. A key is bytes and a
// query string is text, so a name that is not text arrives as keyB64 instead
// — which is also the only way to name the key whose name is empty.
func redisKeyParam(q url.Values) (dbx.RedisBytes, error) {
	if q.Has("keyB64") {
		raw, err := base64.StdEncoding.DecodeString(q.Get("keyB64"))
		if err != nil {
			if raw, err = base64.RawURLEncoding.DecodeString(q.Get("keyB64")); err != nil {
				return "", httpx.BadRequest("keyB64 is not valid base64")
			}
		}
		return dbx.RedisBytes(raw), nil
	}
	if key := q.Get("key"); key != "" {
		return dbx.RedisBytes(key), nil
	}
	return "", httpx.BadRequest("key is required")
}

// redisFail turns what a Redis operation returned into the response for it.
//
// The typed refusals keep their own codes so a page can act on them: a key
// that expired while it was open is not an error to shout about. For the
// rest the convention is the SQL routes': a server that could not be read is
// a 502, and a request the engine turned down is a 400 carrying the engine's
// own sentence. A read that the engine refuses is the first kind — the
// request asked for nothing unusual — and a write is the second.
//
// A read that failed because the server was not there — a dropped connection,
// a server still loading its data — is marked worth asking again. A write is
// never marked: its reply may be what was lost, and the command may have run.
func redisFail(err error, write bool) error {
	var (
		exists   *dbx.RedisKeyExistsError
		conflict *dbx.RedisConflictError
		replied  redis.Error
		network  net.Error
	)
	switch {
	case errors.Is(err, dbx.ErrRedisKeyNotFound):
		return httpx.Err(http.StatusNotFound, "key_not_found", err.Error())
	case errors.Is(err, dbx.ErrRedisMemberNotFound):
		return httpx.Err(http.StatusNotFound, "member_not_found", err.Error())
	case errors.As(err, &exists):
		return httpx.Err(http.StatusConflict, "key_exists", err.Error())
	case errors.As(err, &conflict):
		return httpx.Err(http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, dbx.ErrRedisSentinel):
		return httpx.Err(http.StatusBadRequest, "sentinel_endpoint", err.Error())
	case errors.As(err, &network), errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		if write {
			return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
		}
		return queryFailed(err)
	case errors.As(err, &replied) && !write:
		return queryFailed(err)
	}
	return httpx.BadRequest("%v", err)
}

// redisDestructive applies what s.destructive applies — the capability and
// the tighter budget — for a route whose effect depends on its body.
func (s *Server) redisDestructive(r *http.Request, what string) error {
	p := httpx.MustPrincipal(r)
	if !p.Can(auth.CapDestructive) {
		return httpx.Err(http.StatusForbidden, "forbidden",
			what+", and your role does not permit that (destructive)")
	}
	if !s.destrLim.Allow(p.Username() + "|redis") {
		return httpx.Err(http.StatusTooManyRequests, "rate_limited",
			"too many destructive requests, slow down")
	}
	return nil
}

func (s *Server) handleRedisScan(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	cursor, err := parseRedisCursor(q.Get("cursor"))
	if err != nil {
		return httpx.BadRequest("invalid Redis cursor")
	}
	client, _, err := s.redisClient(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	page, err := dbx.RedisScanKeys(ctx, client, nil, dbx.RedisScanOptions{
		Pattern: q.Get("pattern"), Cursor: cursor, Count: atoiDefault(q.Get("count"), 100),
		Type: q.Get("type"), Memory: q.Get("memory") == "1" || q.Get("memory") == "true",
	})
	if err != nil {
		return redisFail(err, false)
	}
	httpx.JSON(w, http.StatusOK, page)
	return nil
}

func (s *Server) handleRedisGet(w http.ResponseWriter, r *http.Request) error {
	key, err := redisKeyParam(r.URL.Query())
	if err != nil {
		return err
	}
	client, _, err := s.redisClient(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	val, err := dbx.RedisGet(ctx, client, string(key))
	if err != nil {
		// A 400 whatever went wrong, missing key included: it is what this
		// route has always answered, and /keys/members is the one that says
		// key_not_found.
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, http.StatusOK, val)
	return nil
}

func (s *Server) handleRedisTree(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	cursor, err := parseRedisCursor(q.Get("cursor"))
	if err != nil {
		return httpx.BadRequest("invalid Redis cursor")
	}
	prefix := dbx.RedisBytes(q.Get("prefix"))
	if q.Has("prefixB64") {
		raw, err := base64.StdEncoding.DecodeString(q.Get("prefixB64"))
		if err != nil {
			return httpx.BadRequest("prefixB64 is not valid base64")
		}
		prefix = dbx.RedisBytes(raw)
	}
	client, _, err := s.redisClient(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	tree, err := dbx.RedisKeyTree(ctx, client, nil, dbx.RedisTreeOptions{
		Delimiter: q.Get("delimiter"), Prefix: prefix, Pattern: q.Get("pattern"),
		Type: q.Get("type"), Limit: atoiDefault(q.Get("limit"), 0), Cursor: cursor,
		Depth: atoiDefault(q.Get("depth"), 0), Leaves: atoiDefault(q.Get("leaves"), 0),
	})
	if err != nil {
		return redisFail(err, false)
	}
	httpx.JSON(w, http.StatusOK, tree)
	return nil
}

func (s *Server) handleRedisMeta(w http.ResponseWriter, r *http.Request) error {
	key, err := redisKeyParam(r.URL.Query())
	if err != nil {
		return err
	}
	client, _, err := s.redisClient(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	meta, err := dbx.RedisKeyMetadata(ctx, client, nil, key)
	if err != nil {
		return redisFail(err, false)
	}
	httpx.JSON(w, http.StatusOK, meta)
	return nil
}

func (s *Server) handleRedisMembers(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	key, err := redisKeyParam(q)
	if err != nil {
		return err
	}
	order := q.Get("order")
	if order != "" && order != "asc" && order != "desc" {
		return httpx.BadRequest("order must be asc or desc")
	}
	client, _, err := s.redisClient(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	page, err := dbx.RedisReadMembers(ctx, client, nil, dbx.RedisMembersOptions{
		Key: key, Cursor: q.Get("cursor"), Count: atoiDefault(q.Get("count"), 0),
		Match: q.Get("match"), Desc: order == "desc", Min: q.Get("min"), Max: q.Get("max"),
		From: q.Get("from"), To: q.Get("to"), Path: q.Get("path"),
	})
	if err != nil {
		return redisFail(err, false)
	}
	httpx.JSON(w, http.StatusOK, page)
	return nil
}

// handleRedisRaw sends one whole value — a string, a hash field or a list
// element — as a download.
//
// It reads nothing a page of the same key does not already show the start
// of, which is why it sits on the read surface beside the pages. The value
// goes from the server's reply to the response a buffer at a time; nothing
// here holds it.
func (s *Server) handleRedisRaw(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	key, err := redisKeyParam(q)
	if err != nil {
		return err
	}
	whole := dbx.RedisWhole{Key: key}
	switch {
	case q.Has("fieldB64"):
		raw, err := base64.StdEncoding.DecodeString(q.Get("fieldB64"))
		if err != nil {
			return httpx.BadRequest("fieldB64 is not valid base64")
		}
		field := dbx.RedisBytes(raw)
		whole.Field = &field
	case q.Has("field"):
		field := dbx.RedisBytes(q.Get("field"))
		whole.Field = &field
	}
	if q.Has("index") {
		index, err := strconv.ParseInt(q.Get("index"), 10, 64)
		if err != nil {
			return httpx.BadRequest("index must be a list position")
		}
		whole.Index = &index
	}
	client, _, err := s.redisClient(r)
	if err != nil {
		return err
	}
	defer client.Close()
	started := false
	err = dbx.RedisCopyWhole(r.Context(), client, whole, func(size int64) (io.Writer, error) {
		started = true
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		w.Header().Set("Content-Disposition", `attachment; filename="redis-value.bin"`)
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		return w, nil
	})
	if err != nil && !started {
		return redisFail(err, false)
	}
	// Once the body has begun there is no status left to change. A copy that
	// failed part way ends short of the length it declared, which is how the
	// browser knows the download is not whole.
	return nil
}

// redisKeyRequest is the body of every route that changes a key. One shape
// for all of them, because the request decoder refuses a field it does not
// know and a page should not have to remember which route knows which.
type redisKeyRequest struct {
	// Key is a pointer so that the key named "" — which Redis allows — is
	// told apart from no key at all.
	Key  *dbx.RedisBytes `json:"key"`
	Type string          `json:"type"`
	// Field, Value, Score, Index, Insert, Position, Expect, Replace, ID,
	// Entries and Path describe a write; see dbx.RedisWrite.
	Field    *dbx.RedisBytes     `json:"field"`
	Value    *dbx.RedisBytes     `json:"value"`
	Score    *dbx.RedisScore     `json:"score"`
	Index    *int64              `json:"index"`
	Insert   bool                `json:"insert"`
	Position string              `json:"position"`
	Expect   *dbx.RedisBytes     `json:"expect"`
	Replace  *dbx.RedisBytes     `json:"replace"`
	ID       string              `json:"id"`
	Entries  [][2]dbx.RedisBytes `json:"entries"`
	Path     *string             `json:"path"`
	Create   bool                `json:"create"`
	// TTL is seconds. On a write, absent or zero keeps the key's expiry.
	TTL   *int64 `json:"ttl"`
	TTLMs *int64 `json:"ttlMs"`
	At    *int64 `json:"at"`
	// Keys removes whole keys, several at once.
	Keys []dbx.RedisBytes `json:"keys"`
	// Member and Members name entries of a collection to remove, leaving the
	// rest of it alone. Both are pointers for the same reason Key is: the
	// member named "" is a member, an empty list is a list, and a request
	// that carries either must not be read as a request that carries none —
	// which removed the whole key.
	Member  *dbx.RedisBytes   `json:"member"`
	Members *[]dbx.RedisBytes `json:"members"`
	// To is the new name of a rename or the name of a copy, ToDB the logical
	// database a copy goes into, and Overwrite lets either land on a key that
	// is already there — which deletes that key.
	To        *dbx.RedisBytes `json:"to"`
	ToDB      *int            `json:"toDb"`
	Overwrite bool            `json:"overwrite"`
}

func (req *redisKeyRequest) key() (dbx.RedisBytes, error) {
	if req.Key == nil {
		return "", httpx.BadRequest("key is required")
	}
	return *req.Key, nil
}

func (s *Server) handleRedisSet(w http.ResponseWriter, r *http.Request) error {
	var req redisKeyRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	key, err := req.key()
	if err != nil {
		return err
	}
	if req.Position != "" && req.Position != "head" && req.Position != "tail" {
		return httpx.BadRequest("position must be head or tail")
	}
	client, conn, err := s.redisClient(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	// The value itself is not audited: a Redis value is application data and
	// often a session token or a cached credential. What was written and by
	// whom is the useful record; the payload is not. A set member and a list
	// element are payload too, so neither they nor a field name are recorded.
	httpx.SetAudit(r, "database.redis.set", conn.Name, map[string]any{
		"key": key, "type": defaultStr(req.Type, "string"), "db": client.Options().DB, "create": req.Create,
	})
	write := dbx.RedisWrite{
		Key: key, Type: req.Type, Create: req.Create, Value: req.Value, Field: req.Field,
		Score: req.Score, Index: req.Index, Insert: req.Insert, Head: req.Position == "head",
		Expect: req.Expect, Replace: req.Replace, TTL: req.TTL, ID: req.ID,
		Entries: req.Entries,
	}
	if req.Path != nil {
		write.Path = *req.Path
	}
	res, err := dbx.RedisWriteValue(ctx, client, nil, write)
	if err != nil {
		return redisFail(err, true)
	}
	out := map[string]any{"ok": true}
	if res.ID != "" {
		out["id"] = res.ID
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) handleRedisExpire(w http.ResponseWriter, r *http.Request) error {
	var req redisKeyRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	key, err := req.key()
	if err != nil {
		return err
	}
	client, conn, err := s.redisClient(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	httpx.SetAudit(r, "database.redis.expire", conn.Name, map[string]any{
		"key": key, "db": client.Options().DB, "ttl": req.TTL, "ttlMs": req.TTLMs, "at": req.At,
	})
	pttl, err := dbx.RedisSetExpiry(ctx, client, key, dbx.RedisExpiry{Seconds: req.TTL, Millis: req.TTLMs, At: req.At})
	if err != nil {
		return redisFail(err, true)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "pttl": pttl})
	return nil
}

// handleRedisPersist removes a key's expiry. The expire route does the same
// for a non-positive ttl, and keeps doing it; this is the request that says
// so in its path rather than in a number a form might have mistyped.
func (s *Server) handleRedisPersist(w http.ResponseWriter, r *http.Request) error {
	var req redisKeyRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	key, err := req.key()
	if err != nil {
		return err
	}
	client, conn, err := s.redisClient(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	httpx.SetAudit(r, "database.redis.persist", conn.Name,
		map[string]any{"key": key, "db": client.Options().DB})
	never := int64(0)
	pttl, err := dbx.RedisSetExpiry(ctx, client, key, dbx.RedisExpiry{Seconds: &never})
	if err != nil {
		return redisFail(err, true)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "pttl": pttl})
	return nil
}

// handleRedisDelete removes whole keys, or members of one collection.
//
// Neither takes a typed phrase. Both are the everyday edit of a key browser,
// and the rule that asked for the key name meant a multi-select of eight wanted
// eight names typed — which is how you teach somebody to paste without reading.
func (s *Server) handleRedisDelete(w http.ResponseWriter, r *http.Request) error {
	var req redisKeyRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	keys := req.Keys
	if len(keys) == 0 && req.Key != nil {
		keys = []dbx.RedisBytes{*req.Key}
	}
	if len(keys) == 0 {
		return httpx.BadRequest("at least one key is required")
	}
	// Which of the two this is comes from which fields are present, never
	// from what they hold. A field that addresses the inside of a key makes
	// the request one about the inside of a key, and if it then names nothing
	// there the request is refused — it does not fall back to the whole key.
	inside := req.Member != nil || req.Members != nil || req.Index != nil || req.Expect != nil || req.Path != nil
	if !inside {
		client, conn, err := s.redisClient(r)
		if err != nil {
			return err
		}
		defer client.Close()
		ctx, cancel := timeoutCtx(r, 30*time.Second)
		defer cancel()
		httpx.SetAudit(r, "database.redis.delete", conn.Name,
			map[string]any{"keys": keys, "db": client.Options().DB})
		removed, err := dbx.RedisDeleteKeys(ctx, client, nil, keys)
		if err != nil {
			return redisFail(err, true)
		}
		httpx.SetAudit(r, "database.redis.delete", conn.Name,
			map[string]any{"keys": keys, "db": client.Options().DB, "removed": removed})
		httpx.JSON(w, http.StatusOK, map[string]any{"removed": removed})
		return nil
	}

	if len(keys) != 1 {
		return httpx.BadRequest("members are removed from one key at a time")
	}
	var members []dbx.RedisBytes
	if req.Member != nil {
		members = append(members, *req.Member)
	}
	if req.Members != nil {
		members = append(members, *req.Members...)
	}
	if req.Type == "list" && req.Index == nil && len(members) == 1 {
		// The older form of the request names a list element by its position
		// written as text.
		idx, err := strconv.ParseInt(string(members[0]), 10, 64)
		if err != nil {
			return httpx.BadRequest("a list position must be a number")
		}
		req.Index, members = &idx, nil
	}
	switch {
	case req.Path != nil && *req.Path == "":
		// An empty path reads as the root everywhere else, and the root of a
		// document is the document: the same request for the whole key that
		// an empty list of members used to be.
		return httpx.BadRequest("name the path to remove; $ is the whole document")
	case req.Path != nil && req.Type != "" && req.Type != "json":
		return httpx.BadRequest("a path is part of a JSON document; a %s is addressed by member", req.Type)
	case req.Path != nil && (len(members) > 0 || req.Index != nil):
		return httpx.BadRequest("name a path or members, not both")
	case req.Path == nil && req.Index == nil && len(members) == 0:
		return httpx.BadRequest("name at least one member to remove")
	}
	client, conn, err := s.redisClient(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()

	// A member is a value as often as it is a name — a set's members are its
	// data — so the audit entry counts them and does not list them.
	detail := map[string]any{"key": keys[0], "type": req.Type, "db": client.Options().DB, "members": len(members)}
	httpx.SetAudit(r, "database.redis.member.delete", conn.Name, detail)
	var removed int64
	if req.Path != nil {
		// JSON.DEL refuses a key that is not a JSON document, so a path sent
		// without its type is checked by the server rather than guessed at.
		removed, err = dbx.RedisJSONDelete(ctx, client, keys[0], *req.Path)
	} else {
		removed, err = dbx.RedisRemoveMembers(ctx, client, dbx.RedisRemoval{
			Key: keys[0], Type: req.Type, Members: members, Index: req.Index, Expect: req.Expect,
		})
	}
	if err != nil {
		return redisFail(err, true)
	}
	detail["removed"] = removed
	httpx.SetAudit(r, "database.redis.member.delete", conn.Name, detail)
	httpx.JSON(w, http.StatusOK, map[string]any{"removed": removed})
	return nil
}

// handleRedisRename moves a key to a new name, refusing to clobber an existing
// one — which plain RENAME would do silently — unless the request says to.
func (s *Server) handleRedisRename(w http.ResponseWriter, r *http.Request) error {
	var req redisKeyRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Key == nil || req.To == nil {
		return httpx.BadRequest("both the current and the new key name are required")
	}
	if req.Overwrite {
		if err := s.redisDestructive(r, "renaming over an existing key deletes it"); err != nil {
			return err
		}
	}
	client, conn, err := s.redisClient(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	httpx.SetAudit(r, "database.redis.rename", conn.Name, map[string]any{
		"from": *req.Key, "to": *req.To, "db": client.Options().DB, "overwrite": req.Overwrite,
	})
	if err := dbx.RedisRenameKey(ctx, client, *req.Key, *req.To, req.Overwrite); err != nil {
		return redisFail(err, true)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

// handleRedisCopy duplicates a key under another name, optionally into
// another logical database.
func (s *Server) handleRedisCopy(w http.ResponseWriter, r *http.Request) error {
	var req redisKeyRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Key == nil || req.To == nil {
		return httpx.BadRequest("both the key and the name for its copy are required")
	}
	if req.Overwrite {
		if err := s.redisDestructive(r, "copying over an existing key deletes it"); err != nil {
			return err
		}
	}
	client, conn, err := s.redisClient(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	httpx.SetAudit(r, "database.redis.copy", conn.Name, map[string]any{
		"from": *req.Key, "to": *req.To, "db": client.Options().DB, "toDb": req.ToDB,
		"overwrite": req.Overwrite,
	})
	err = dbx.RedisCopyKey(ctx, client, nil, dbx.RedisCopy{
		From: *req.Key, To: *req.To, DB: req.ToDB, Replace: req.Overwrite,
	})
	if err != nil {
		return redisFail(err, true)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

type redisBulkRequest struct {
	Pattern string `json:"pattern"`
	Type    string `json:"type"`
	Action  string `json:"action"`
	TTL     int64  `json:"ttl"`
	DryRun  bool   `json:"dryRun"`
	Cursor  string `json:"cursor"`
	Limit   int    `json:"limit"`
}

// handleRedisBulk applies one action to every key a pattern matches.
//
// A dry run only counts and is what the confirmation is built from. Of the
// real actions, persist takes nothing away. Delete does, and so does expire:
// an expiry on every matching key is the same removal, a few seconds later.
// Both therefore need the destructive capability and spend its budget, which
// the route cannot demand on its own because persist and the dry run share
// its path.
//
// One request they do not serve: every key there is. That is emptying the
// database, which has a route of its own that asks for the database's name,
// and a pattern box left at * is not somebody typing that name.
func (s *Server) handleRedisBulk(w http.ResponseWriter, r *http.Request) error {
	var req redisBulkRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if !dbx.RedisBulkActions[req.Action] {
		return httpx.BadRequest("action must be delete, expire or persist")
	}
	cursor, err := parseRedisCursor(req.Cursor)
	if err != nil {
		return httpx.BadRequest("invalid Redis cursor")
	}
	if !req.DryRun && req.Action != "persist" {
		if req.Type == "" && req.Pattern != "" && strings.Trim(req.Pattern, "*") == "" {
			return httpx.Err(http.StatusBadRequest, "whole_database",
				"that pattern is every key in the database, and emptying a database is done from its settings, which ask for its name. Narrow the pattern or name a type to remove part of it")
		}
		if err := s.redisDestructive(r, "this removes every key the pattern matches"); err != nil {
			return err
		}
	}
	client, conn, err := s.redisClient(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	detail := map[string]any{
		"pattern": req.Pattern, "type": req.Type, "action": req.Action,
		"db": client.Options().DB, "ttl": req.TTL,
	}
	if req.DryRun {
		// A count changes nothing and is asked for every time the dialog
		// opens.
		httpx.SkipAudit(r)
	} else {
		httpx.SetAudit(r, "database.redis.bulk", conn.Name, detail)
	}
	res, err := dbx.RedisBulk(ctx, client, nil, dbx.RedisBulkOptions{
		Pattern: req.Pattern, Type: req.Type, Action: req.Action, TTL: req.TTL,
		DryRun: req.DryRun, Cursor: cursor, Limit: req.Limit,
	})
	if res != nil && !req.DryRun {
		detail["matched"], detail["affected"], detail["complete"] = res.Matched, res.Affected, res.Complete
		httpx.SetAudit(r, "database.redis.bulk", conn.Name, detail)
	}
	if err != nil {
		return redisFail(err, true)
	}
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) handleRedisStream(w http.ResponseWriter, r *http.Request) error {
	key, err := redisKeyParam(r.URL.Query())
	if err != nil {
		return err
	}
	client, _, err := s.redisClient(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	info, err := dbx.RedisStreamDescribe(ctx, client, key)
	if err != nil {
		return redisFail(err, false)
	}
	httpx.JSON(w, http.StatusOK, info)
	return nil
}

func (s *Server) handleRedisStreamPending(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	key, err := redisKeyParam(q)
	if err != nil {
		return err
	}
	if q.Get("group") == "" {
		return httpx.BadRequest("group is required")
	}
	client, _, err := s.redisClient(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	page, err := dbx.RedisStreamPendingEntries(ctx, client, dbx.RedisStreamPendingOptions{
		Key: key, Group: dbx.RedisBytes(q.Get("group")), Consumer: dbx.RedisBytes(q.Get("consumer")),
		Cursor: q.Get("cursor"), Count: atoiDefault(q.Get("count"), 0),
	})
	if err != nil {
		return redisFail(err, false)
	}
	httpx.JSON(w, http.StatusOK, page)
	return nil
}

type redisStreamRequest struct {
	Key   *dbx.RedisBytes `json:"key"`
	Group dbx.RedisBytes  `json:"group"`
	// Consumer is a pointer for the reason a member is: a consumer may be
	// named "", and a removal that names it must not be read as one that
	// names none — which destroys the whole group.
	Consumer *dbx.RedisBytes `json:"consumer"`
	// ID is where a group starts reading: an entry id, "$" for new entries
	// only, "0" for the whole stream. SetID moves an existing group there
	// instead of creating one.
	ID    string   `json:"id"`
	SetID bool     `json:"setId"`
	IDs   []string `json:"ids"`
	// MaxLen, MinID and Approximate say how much of a stream a trim keeps.
	MaxLen      *int64 `json:"maxLen"`
	MinID       string `json:"minId"`
	Approximate bool   `json:"approximate"`
}

// redisStreamBody decodes a stream request and opens the client for it.
func (s *Server) redisStreamBody(r *http.Request) (*redisStreamRequest, *redis.Client, *dbConnection, error) {
	var req redisStreamRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return nil, nil, nil, err
	}
	if req.Key == nil {
		return nil, nil, nil, httpx.BadRequest("key is required")
	}
	client, conn, err := s.redisClient(r)
	if err != nil {
		return nil, nil, nil, err
	}
	return &req, client, conn, nil
}

// handleRedisStreamGroup creates a consumer group, or moves one's read
// position.
func (s *Server) handleRedisStreamGroup(w http.ResponseWriter, r *http.Request) error {
	req, client, conn, err := s.redisStreamBody(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	action := "database.redis.stream.group.create"
	if req.SetID {
		action = "database.redis.stream.group.setid"
	}
	httpx.SetAudit(r, action, conn.Name, map[string]any{
		"key": *req.Key, "group": req.Group, "id": req.ID, "db": client.Options().DB,
	})
	if req.SetID {
		err = dbx.RedisStreamGroupSetID(ctx, client, *req.Key, req.Group, req.ID)
	} else {
		err = dbx.RedisStreamGroupCreate(ctx, client, *req.Key, req.Group, req.ID)
	}
	if err != nil {
		return redisFail(err, true)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

// handleRedisStreamAck acknowledges pending entries on a group's behalf.
func (s *Server) handleRedisStreamAck(w http.ResponseWriter, r *http.Request) error {
	req, client, conn, err := s.redisStreamBody(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	httpx.SetAudit(r, "database.redis.stream.ack", conn.Name, map[string]any{
		"key": *req.Key, "group": req.Group, "ids": len(req.IDs), "db": client.Options().DB,
	})
	n, err := dbx.RedisStreamAck(ctx, client, *req.Key, req.Group, req.IDs)
	if err != nil {
		return redisFail(err, true)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"acknowledged": n})
	return nil
}

// handleRedisStreamTrim shortens a stream. The entries it drops are gone.
func (s *Server) handleRedisStreamTrim(w http.ResponseWriter, r *http.Request) error {
	req, client, conn, err := s.redisStreamBody(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	detail := map[string]any{
		"key": *req.Key, "maxLen": req.MaxLen, "minId": req.MinID, "db": client.Options().DB,
	}
	httpx.SetAudit(r, "database.redis.stream.trim", conn.Name, detail)
	removed, err := dbx.RedisStreamTrimEntries(ctx, client, dbx.RedisStreamTrim{
		Key: *req.Key, MaxLen: req.MaxLen, MinID: req.MinID, Approximate: req.Approximate,
	})
	if err != nil {
		return redisFail(err, true)
	}
	detail["removed"] = removed
	httpx.SetAudit(r, "database.redis.stream.trim", conn.Name, detail)
	httpx.JSON(w, http.StatusOK, map[string]any{"removed": removed})
	return nil
}

// handleRedisStreamGroupRemove destroys a consumer group, or removes one
// consumer from it. Either way the entries it had pending are forgotten.
func (s *Server) handleRedisStreamGroupRemove(w http.ResponseWriter, r *http.Request) error {
	req, client, conn, err := s.redisStreamBody(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	action := "database.redis.stream.group.delete"
	detail := map[string]any{"key": *req.Key, "group": req.Group, "db": client.Options().DB}
	if req.Consumer != nil {
		action = "database.redis.stream.consumer.delete"
		detail["consumer"] = *req.Consumer
	}
	httpx.SetAudit(r, action, conn.Name, detail)
	n, err := dbx.RedisStreamGroupRemove(ctx, client, *req.Key, req.Group, req.Consumer)
	if err != nil {
		return redisFail(err, true)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"removed": n})
	return nil
}

func parseRedisCursor(raw string) (uint64, error) {
	if raw == "" {
		return 0, nil
	}
	return strconv.ParseUint(raw, 10, 64)
}
