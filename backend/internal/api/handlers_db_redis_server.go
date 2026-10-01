package api

import (
	"net/http"
	"net/url"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// The Redis server's own pages: what it is, what it is doing, who is
// connected, how it is configured, who may log in.

func (s *Server) handleRedisServer(w http.ResponseWriter, r *http.Request) error {
	client, _, err := s.redisClient(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	out, err := dbx.RedisServerOverview(ctx, client)
	if err != nil {
		return redisFail(err, false)
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) handleRedisCommandStats(w http.ResponseWriter, r *http.Request) error {
	client, _, err := s.redisClient(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	out, err := dbx.RedisCommandStatistics(ctx, client)
	if err != nil {
		return redisFail(err, false)
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) handleRedisLatency(w http.ResponseWriter, r *http.Request) error {
	client, _, err := s.redisClient(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	out, err := dbx.RedisLatencyLatest(ctx, client, nil)
	if err != nil {
		return redisFail(err, false)
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) handleRedisClients(w http.ResponseWriter, r *http.Request) error {
	client, _, err := s.redisClient(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	clients, err := dbx.RedisClients(ctx, client)
	if err != nil {
		return redisFail(err, false)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"clients": clients})
	return nil
}

type redisClientKillRequest struct {
	ID int64 `json:"id"`
}

// handleRedisClientKill disconnects one client. Whatever it was in the
// middle of is abandoned; a well-behaved client reconnects.
func (s *Server) handleRedisClientKill(w http.ResponseWriter, r *http.Request) error {
	var req redisClientKillRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	client, conn, err := s.redisClient(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	httpx.SetAudit(r, "database.redis.client.kill", conn.Name, map[string]any{"id": req.ID})
	killed, err := dbx.RedisKillClient(ctx, client, req.ID)
	if err != nil {
		return redisFail(err, true)
	}
	if !killed {
		return httpx.Err(http.StatusNotFound, "client_not_found",
			"no client has that id; it may already have disconnected")
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (s *Server) handleRedisConfig(w http.ResponseWriter, r *http.Request) error {
	client, _, err := s.redisClient(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	out, err := dbx.RedisConfigRead(ctx, client, nil)
	if err != nil {
		return redisFail(err, false)
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

type redisConfigRequest struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	// Rewrite also writes the server's configuration file, so the change
	// survives a restart.
	Rewrite bool `json:"rewrite"`
}

// handleRedisConfigSet changes one parameter of the running server.
func (s *Server) handleRedisConfigSet(w http.ResponseWriter, r *http.Request) error {
	var req redisConfigRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Name == "" {
		return httpx.BadRequest("name is required")
	}
	client, conn, err := s.redisClient(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	// What a parameter was set to is the record, except where the parameter
	// is a password: then that it changed is the record and its value is
	// nobody's to read back.
	detail := map[string]any{"name": req.Name, "rewrite": req.Rewrite}
	if !dbx.RedisConfigSecret(req.Name) {
		detail["value"] = req.Value
	}
	httpx.SetAudit(r, "database.redis.config.set", conn.Name, detail)
	change, err := dbx.RedisConfigSet(ctx, client, req.Name, req.Value, req.Rewrite)
	if err != nil {
		return redisFail(err, true)
	}
	detail["rewritten"] = change.Rewritten
	httpx.SetAudit(r, "database.redis.config.set", conn.Name, detail)
	httpx.JSON(w, http.StatusOK, change)
	return nil
}

func (s *Server) handleRedisSlowlog(w http.ResponseWriter, r *http.Request) error {
	client, _, err := s.redisClient(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	out, err := dbx.RedisSlowlogRead(ctx, client, nil, atoiDefault(r.URL.Query().Get("count"), 0))
	if err != nil {
		return redisFail(err, false)
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// handleRedisSlowlogReset empties the slow log. The request has no body; the
// route is the whole of it.
func (s *Server) handleRedisSlowlogReset(w http.ResponseWriter, r *http.Request) error {
	client, conn, err := s.redisClient(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	httpx.SetAudit(r, "database.redis.slowlog.reset", conn.Name, nil)
	if err := dbx.RedisSlowlogReset(ctx, client); err != nil {
		return redisFail(err, true)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

type redisSaveRequest struct {
	Mode string `json:"mode"`
}

// handleRedisSave starts a background snapshot or a rewrite of the
// append-only file. Both are what the server does on its own schedule
// anyway; asking for one early loses nothing, which is why this sits with
// taking a dump rather than with the destructive routes.
func (s *Server) handleRedisSave(w http.ResponseWriter, r *http.Request) error {
	var req redisSaveRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if !dbx.RedisSaveModes[req.Mode] {
		return httpx.BadRequest("mode must be bgsave or bgrewriteaof")
	}
	client, conn, err := s.redisClient(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	httpx.SetAudit(r, "database.redis.save", conn.Name, map[string]any{"mode": req.Mode})
	status, err := dbx.RedisSave(ctx, client, nil, req.Mode)
	if err != nil {
		return redisFail(err, true)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "status": status})
	return nil
}

// handleRedisAnalysis samples one logical database and accounts for its
// memory. It is a read, and a heavy one: up to fifty thousand keys measured
// with four commands each, bounded by its own clock and by the request's.
func (s *Server) handleRedisAnalysis(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	client, _, err := s.redisClient(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	out, err := dbx.RedisAnalyze(ctx, client, nil, dbx.RedisAnalysisOptions{
		Sample: atoiDefault(q.Get("sample"), 0), Delimiter: q.Get("delimiter"),
		Top: atoiDefault(q.Get("top"), 0),
	})
	if err != nil {
		return redisFail(err, false)
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// handleRedisACL lists the server's users and their rules, without the
// password hashes ACL LIST prints beside them.
func (s *Server) handleRedisACL(w http.ResponseWriter, r *http.Request) error {
	client, _, err := s.redisClient(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	profile, err := dbx.RedisProbe(ctx, client)
	if err != nil {
		return redisFail(err, false)
	}
	unsupported := func(reason string) error {
		httpx.JSON(w, http.StatusOK, map[string]any{
			"supported": false, "users": []dbx.RedisACLUser{}, "reason": reason,
		})
		return nil
	}
	if !profile.Features.ACL {
		// A server from before Redis 6 has one password and no users.
		return unsupported("This server has no ACL: it has one password and no named users.")
	}
	users, err := dbx.RedisACLUsers(ctx, client)
	if err != nil {
		// The command exists and this connection may not run it, which is the
		// case on a managed service or for a user without @admin.
		return unsupported(err.Error())
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"supported": true, "users": users})
	return nil
}

type redisACLRequest struct {
	// Create refuses the change when the user already exists; without it the
	// user must exist.
	Create  bool  `json:"create"`
	Enabled *bool `json:"enabled"`
	// Password replaces every password the user has. NoPassword removes them
	// and lets any password in.
	Password   *string `json:"password"`
	NoPassword bool    `json:"noPassword"`
	// Each of these, when present, replaces that part of the rule whole.
	Keys     *[]string `json:"keys"`
	Channels *[]string `json:"channels"`
	Commands *[]string `json:"commands"`
}

// redisACLName reads the user a request is about from its path.
func redisACLName(r *http.Request) (string, error) {
	name, err := url.PathUnescape(chi.URLParam(r, "name"))
	if err != nil || name == "" {
		return "", httpx.BadRequest("a user name is required")
	}
	return name, nil
}

// handleRedisACLSet creates a user or changes one.
func (s *Server) handleRedisACLSet(w http.ResponseWriter, r *http.Request) error {
	var req redisACLRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	name, err := redisACLName(r)
	if err != nil {
		return err
	}
	spec := dbx.RedisACLSpec{
		Name: name, Create: req.Create, Enabled: req.Enabled, Password: req.Password,
		NoPassword: req.NoPassword, Keys: req.Keys, Channels: req.Channels, Commands: req.Commands,
	}
	// Checked before anything is recorded: a rule that does not parse is not
	// a rule, and its text does not belong on the trail.
	if err := spec.Validate(); err != nil {
		return httpx.BadRequest("%v", err)
	}
	client, conn, err := s.redisClient(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	// The rule is the record. The password is not: only that one was set, or
	// that the user was opened to any. The field is not called "password"
	// because the audit log blanks whatever is.
	detail := map[string]any{
		"user": name, "create": req.Create, "enabled": req.Enabled,
		"keys": req.Keys, "channels": req.Channels, "commands": req.Commands,
	}
	switch {
	case req.NoPassword:
		detail["authentication"] = "any accepted"
	case req.Password != nil:
		detail["authentication"] = "changed"
	}
	httpx.SetAudit(r, "database.redis.acl.set", conn.Name, detail)
	out, err := dbx.RedisACLSetUser(ctx, client, spec)
	if err != nil {
		return redisFail(err, true)
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// handleRedisACLDelete removes a user and disconnects whoever was using it.
func (s *Server) handleRedisACLDelete(w http.ResponseWriter, r *http.Request) error {
	name, err := redisACLName(r)
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
	httpx.SetAudit(r, "database.redis.acl.delete", conn.Name, map[string]any{"user": name})
	out, err := dbx.RedisACLDeleteUser(ctx, client, name)
	if err != nil {
		return redisFail(err, true)
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// handleRedisPubSub lists the channels somebody is subscribed to right now.
func (s *Server) handleRedisPubSub(w http.ResponseWriter, r *http.Request) error {
	client, _, err := s.redisClient(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	out, err := dbx.RedisPubSubChannels(ctx, client, r.URL.Query().Get("pattern"))
	if err != nil {
		return redisFail(err, false)
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

type redisPublishRequest struct {
	Channel *dbx.RedisBytes `json:"channel"`
	Message dbx.RedisBytes  `json:"message"`
}

// handleRedisPublish sends one message to a channel.
func (s *Server) handleRedisPublish(w http.ResponseWriter, r *http.Request) error {
	var req redisPublishRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Channel == nil || *req.Channel == "" {
		return httpx.BadRequest("channel is required")
	}
	client, conn, err := s.redisClient(r)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	// The channel is recorded and the message is not, for the reason a
	// value is not.
	httpx.SetAudit(r, "database.redis.publish", conn.Name, map[string]any{"channel": *req.Channel})
	receivers, err := dbx.RedisPublish(ctx, client, *req.Channel, req.Message)
	if err != nil {
		return redisFail(err, true)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"receivers": receivers})
	return nil
}
