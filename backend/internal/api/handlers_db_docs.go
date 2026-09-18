package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/mongo"
)

// The two engines that are not SQL. Their handlers live together because they
// share the shape their SQL siblings do not: a client opened per request rather
// than drawn from the pool, and a vocabulary of keys and documents rather than
// rows.

// redisClient resolves a connection to a Redis client, refusing any connection
// that is not one. The logical database number rides on the query string
// because Redis selects it per connection rather than per statement.
func (s *Server) redisClient(r *http.Request) (*redis.Client, *dbConnection, error) {
	id, err := parseID(r)
	if err != nil {
		return nil, nil, err
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return nil, nil, err
	}
	if conn.Driver != dbx.DriverRedis {
		return nil, nil, httpx.BadRequest("this endpoint is for Redis connections")
	}
	db := atoiDefault(r.URL.Query().Get("db"), 0)
	client, err := dbx.RedisClient(r.Context(), dsn, db)
	if err != nil {
		return nil, conn, httpx.Err(http.StatusBadGateway, "connect_failed", err.Error())
	}
	return client, conn, nil
}

func (s *Server) handleRedisScan(w http.ResponseWriter, r *http.Request) error {
	client, _, err := s.redisClient(r)
	if err != nil {
		return err
	}
	defer client.Close()
	q := r.URL.Query()
	cursor, err := parseRedisCursor(q.Get("cursor"))
	if err != nil {
		return httpx.BadRequest("invalid Redis cursor")
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	page, err := dbx.RedisScan(ctx, client, q.Get("pattern"), cursor, atoiDefault(q.Get("count"), 100))
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	httpx.JSON(w, http.StatusOK, page)
	return nil
}

func (s *Server) handleRedisGet(w http.ResponseWriter, r *http.Request) error {
	client, _, err := s.redisClient(r)
	if err != nil {
		return err
	}
	defer client.Close()
	key := r.URL.Query().Get("key")
	if key == "" {
		return httpx.BadRequest("key is required")
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	val, err := dbx.RedisGet(ctx, client, key)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, http.StatusOK, val)
	return nil
}

type redisWriteRequest struct {
	Key   string   `json:"key"`
	Type  string   `json:"type"`
	Field string   `json:"field"`
	Value string   `json:"value"`
	TTL   int64    `json:"ttl"`
	Keys  []string `json:"keys"`
	// Member names one entry of a collection to remove, leaving the rest of the
	// collection alone. It is deliberately distinct from Keys, which removes
	// whole keys: the confirmation phrase and the audit entry differ.
	Member string `json:"member"`
	To     string `json:"to"`
	Create bool   `json:"create"`
}

func (s *Server) handleRedisSet(w http.ResponseWriter, r *http.Request) error {
	var req redisWriteRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Key == "" {
		return httpx.BadRequest("key is required")
	}
	client, conn, err := s.redisClient(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	if req.Create {
		if err := dbx.RedisCreateKey(ctx, client, req.Key, req.Type, req.Field, req.Value, req.TTL); err != nil {
			return httpx.BadRequest("%v", err)
		}
	} else if err := dbx.RedisSet(ctx, client, req.Key, req.Type, req.Field, req.Value, req.TTL); err != nil {
		return httpx.BadRequest("%v", err)
	}
	// The value itself is not audited: a Redis value is application data and
	// often a session token or a cached credential. What was written and by
	// whom is the useful record; the payload is not.
	httpx.SetAudit(r, "database.redis.set", conn.Name,
		map[string]any{"key": req.Key, "type": req.Type})
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (s *Server) handleRedisExpire(w http.ResponseWriter, r *http.Request) error {
	var req redisWriteRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Key == "" {
		return httpx.BadRequest("key is required")
	}
	client, conn, err := s.redisClient(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	if err := dbx.RedisExpire(ctx, client, req.Key, req.TTL); err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "database.redis.expire", conn.Name,
		map[string]any{"key": req.Key, "ttl": req.TTL})
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

// handleRedisDelete removes whole keys, or one field of a hash.
//
// Neither takes a typed phrase. Both are the everyday edit of a key browser,
// and the rule that asked for the key name meant a multi-select of eight wanted
// eight names typed — which is how you teach somebody to paste without reading.
func (s *Server) handleRedisDelete(w http.ResponseWriter, r *http.Request) error {
	var req redisWriteRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	keys := req.Keys
	if len(keys) == 0 && req.Key != "" {
		keys = []string{req.Key}
	}
	if len(keys) == 0 {
		return httpx.BadRequest("at least one key is required")
	}
	// No typed phrase. A Redis key is the unit of work in a key browser and a
	// member is smaller still; both are deleted constantly, and the earlier
	// rule asked for eight key names in a row on a multi-select. The dialog
	// names what is going.
	client, conn, err := s.redisClient(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()

	var removed int64
	if req.Member != "" {
		removed, err = dbx.RedisDeleteMember(ctx, client, keys[0], req.Type, req.Member)
	} else {
		removed, err = dbx.RedisDelete(ctx, client, keys...)
	}
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "database.redis.delete", conn.Name,
		map[string]any{"keys": keys, "member": req.Member, "removed": removed})
	httpx.JSON(w, http.StatusOK, map[string]any{"removed": removed})
	return nil
}

// --- MongoDB documents ----------------------------------------------------

func (s *Server) mongoClient(r *http.Request) (*mongo.Client, *dbConnection, error) {
	id, err := parseID(r)
	if err != nil {
		return nil, nil, err
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return nil, nil, err
	}
	if conn.Driver != dbx.DriverMongo {
		return nil, nil, httpx.BadRequest("this endpoint is for MongoDB connections")
	}
	client, err := dbx.MongoClient(r.Context(), dsn)
	if err != nil {
		return nil, conn, httpx.Err(http.StatusBadGateway, "connect_failed", err.Error())
	}
	return client, conn, nil
}

type mongoDocRequest struct {
	Database   string `json:"database"`
	Collection string `json:"collection"`
	Filter     string `json:"filter"`
	Document   string `json:"document"`
	Pipeline   string `json:"pipeline"`
	Many       bool   `json:"many"`
	Limit      int    `json:"limit"`
}

// mongoTarget resolves the database and collection, defaulting the database to
// the one named in the connection string so the common single-database setup
// needs no extra field.
func mongoTarget(req *mongoDocRequest, conn *dbConnection) (string, string, error) {
	db := req.Database
	if db == "" {
		db = conn.Database
	}
	if db == "" {
		return "", "", httpx.BadRequest("a database is required")
	}
	if strings.TrimSpace(req.Collection) == "" {
		return "", "", httpx.BadRequest("a collection is required")
	}
	return db, req.Collection, nil
}

func (s *Server) handleMongoIndexes(w http.ResponseWriter, r *http.Request) error {
	client, conn, err := s.mongoClient(r)
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	q := r.URL.Query()
	db := q.Get("schema")
	if db == "" {
		db = conn.Database
	}
	collection := q.Get("table")
	if db == "" || collection == "" {
		return httpx.BadRequest("schema and table are required")
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	indexes, err := dbx.MongoIndexes(ctx, client, db, collection)
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	stats, _ := dbx.MongoCollStats(ctx, client, db, collection)
	httpx.JSON(w, http.StatusOK, map[string]any{"indexes": indexes, "stats": stats})
	return nil
}

func (s *Server) handleMongoInsert(w http.ResponseWriter, r *http.Request) error {
	var req mongoDocRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	client, conn, err := s.mongoClient(r)
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	db, collection, err := mongoTarget(&req, conn)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	id, err := dbx.MongoInsert(ctx, client, db, collection, req.Document)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "database.document.insert", conn.Name,
		map[string]any{"database": db, "collection": collection, "id": id})
	httpx.JSON(w, http.StatusOK, map[string]any{"insertedId": id})
	return nil
}

func (s *Server) handleMongoReplace(w http.ResponseWriter, r *http.Request) error {
	var req mongoDocRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	client, conn, err := s.mongoClient(r)
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	db, collection, err := mongoTarget(&req, conn)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	n, err := dbx.MongoReplace(ctx, client, db, collection, req.Filter, req.Document)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "database.document.replace", conn.Name,
		map[string]any{"database": db, "collection": collection, "filter": req.Filter, "modified": n})
	httpx.JSON(w, http.StatusOK, map[string]any{"modified": n})
	return nil
}

func (s *Server) handleMongoDelete(w http.ResponseWriter, r *http.Request) error {
	var req mongoDocRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	client, conn, err := s.mongoClient(r)
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	db, collection, err := mongoTarget(&req, conn)
	if err != nil {
		return err
	}
	// No typed phrase: a document is Mongo's row, and deleting one is the same
	// everyday act the SQL side stopped typing for. Dropping the whole
	// collection is the one below, and that still asks.
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	n, err := dbx.MongoDelete(ctx, client, db, collection, req.Filter, req.Many)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "database.document.delete", conn.Name,
		map[string]any{"database": db, "collection": collection, "filter": req.Filter, "deleted": n})
	httpx.JSON(w, http.StatusOK, map[string]any{"deleted": n})
	return nil
}

// handleMongoAggregate runs a pipeline. A pipeline ending in $out or $merge
// writes a whole collection, so it is checked by content the way SQL is
// classified by content — the route cannot tell from its path.
func (s *Server) handleMongoAggregate(w http.ResponseWriter, r *http.Request) error {
	var req mongoDocRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if strings.TrimSpace(req.Pipeline) == "" {
		return httpx.BadRequest("a pipeline is required")
	}
	destructive := dbx.MongoWritesInPipeline(req.Pipeline)
	if destructive {
		p := httpx.MustPrincipal(r)
		if !p.Can(auth.CapDestructive) {
			return httpx.Err(http.StatusForbidden, "forbidden",
				"this pipeline writes a collection and your role does not permit it")
		}
		if err := httpx.RequireTypedConfirmation(w, r, "run pipeline"); err != nil {
			return err
		}
		if !s.destrLim.Allow(p.Username() + "|mongoagg") {
			return httpx.Err(http.StatusTooManyRequests, "rate_limited",
				"too many writing pipelines, slow down")
		}
	}
	client, conn, err := s.mongoClient(r)
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	db, collection, err := mongoTarget(&req, conn)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 120*time.Second)
	defer cancel()
	res, err := dbx.MongoAggregate(ctx, client, db, collection, req.Pipeline, req.Limit)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "database.aggregate", conn.Name, map[string]any{
		"database": db, "collection": collection,
		"writes": destructive, "rowCount": res.RowCount,
	})
	httpx.JSON(w, http.StatusOK, map[string]any{"result": res, "writes": destructive})
	return nil
}

func (s *Server) handleMongoCreateCollection(w http.ResponseWriter, r *http.Request) error {
	var req mongoDocRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	client, conn, err := s.mongoClient(r)
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	db, collection, err := mongoTarget(&req, conn)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	if err := dbx.MongoCreateCollection(ctx, client, db, collection); err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "database.collection.create", conn.Name,
		map[string]any{"database": db, "collection": collection})
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (s *Server) handleMongoDropCollection(w http.ResponseWriter, r *http.Request) error {
	var req mongoDocRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	client, conn, err := s.mongoClient(r)
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	db, collection, err := mongoTarget(&req, conn)
	if err != nil {
		return err
	}
	if err := httpx.RequireTypedConfirmation(w, r, collection); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	if err := dbx.MongoDropCollection(ctx, client, db, collection); err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "database.collection.drop", conn.Name,
		map[string]any{"database": db, "collection": collection})
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

// itoaLocal keeps the confirmation phrase builder from importing strconv for
// one call.
func itoaLocal(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// handleRedisRename moves a key to a new name, refusing to clobber an existing
// one — which plain RENAME would do silently.
func (s *Server) handleRedisRename(w http.ResponseWriter, r *http.Request) error {
	var req redisWriteRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Key == "" || req.To == "" {
		return httpx.BadRequest("both the current and the new key name are required")
	}
	client, conn, err := s.redisClient(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	if err := dbx.RedisRename(ctx, client, req.Key, req.To); err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "database.redis.rename", conn.Name,
		map[string]any{"from": req.Key, "to": req.To})
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func parseRedisCursor(raw string) (uint64, error) {
	if raw == "" {
		return 0, nil
	}
	return strconv.ParseUint(raw, 10, 64)
}
