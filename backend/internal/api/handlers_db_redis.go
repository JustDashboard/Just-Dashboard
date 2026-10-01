package api

import (
	"net/http"
	"strconv"
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
func (s *Server) mountDatabaseRedisRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/{id}/keys", s.handle(s.handleRedisScan))
	r.Method(http.MethodGet, "/{id}/keys/value", s.handle(s.handleRedisGet))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapServiceControl))
		r.Method(http.MethodPost, "/{id}/keys/value", s.handle(s.handleRedisSet))
		r.Method(http.MethodPost, "/{id}/keys/expire", s.handle(s.handleRedisExpire))
		r.Method(http.MethodPost, "/{id}/keys/rename", s.handle(s.handleRedisRename))
	})
	s.destructive(r, func(r chi.Router) {
		r.Method(http.MethodDelete, "/{id}/keys", s.handle(s.handleRedisDelete))
	})
}

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
