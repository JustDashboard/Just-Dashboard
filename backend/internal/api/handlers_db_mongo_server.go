package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"go.mongodb.org/mongo-driver/mongo"
)

// The MongoDB server itself: its counters, what it is running, what it found
// slow, who it replicates with, who may sign in — and the console, which can
// ask it anything.

func (s *Server) handleMongoServer(w http.ResponseWriter, r *http.Request) error {
	client, _, err := s.mongoClient(r)
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	ctx, cancel := timeoutCtx(r, 15*time.Second)
	defer cancel()
	snapshot, err := dbx.MongoServerSnapshot(ctx, client)
	if err != nil {
		return mongoReadFailure(err)
	}
	httpx.JSON(w, http.StatusOK, snapshot)
	return nil
}

func (s *Server) handleMongoDatabases(w http.ResponseWriter, r *http.Request) error {
	client, _, err := s.mongoClient(r)
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	dbs, err := dbx.MongoDatabaseStats(ctx, client)
	if err != nil {
		return mongoReadFailure(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"databases": dbs})
	return nil
}

func (s *Server) handleMongoOps(w http.ResponseWriter, r *http.Request) error {
	client, _, err := s.mongoClient(r)
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	ctx, cancel := timeoutCtx(r, 15*time.Second)
	defer cancel()
	// ?all=1 adds idle connections and the server's own background work.
	ops, err := dbx.MongoCurrentOps(ctx, client, r.URL.Query().Get("all") == "1")
	if err != nil {
		return mongoReadFailure(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"operations": ops})
	return nil
}

type mongoKillOpRequest struct {
	OpID string `json:"opId"`
}

func (s *Server) handleMongoKillOp(w http.ResponseWriter, r *http.Request) error {
	var req mongoKillOpRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	client, conn, err := s.mongoClient(r)
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	ctx, cancel := timeoutCtx(r, 15*time.Second)
	defer cancel()
	if err := dbx.MongoKillOp(ctx, client, req.OpID); err != nil {
		return mongoFailure(err)
	}
	httpx.SetAudit(r, "database.mongo.killop", conn.Name, map[string]any{"opId": req.OpID})
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (s *Server) handleMongoProfiler(w http.ResponseWriter, r *http.Request) error {
	client, conn, err := s.mongoClient(r)
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	q := r.URL.Query()
	db, err := mongoDatabase(q.Get("database"), conn)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 45*time.Second)
	defer cancel()
	profile, err := dbx.MongoProfileRead(ctx, client, db,
		atoiDefault(q.Get("limit"), 0), int64(atoiDefault(q.Get("minMillis"), 0)))
	if err != nil {
		return mongoReadFailure(err)
	}
	httpx.JSON(w, http.StatusOK, profile)
	return nil
}

type mongoProfilerRequest struct {
	Database string `json:"database"`
	dbx.MongoProfilerChange
}

func (s *Server) handleMongoProfilerPut(w http.ResponseWriter, r *http.Request) error {
	var req mongoProfilerRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	client, conn, err := s.mongoClient(r)
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	db, err := mongoDatabase(req.Database, conn)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 15*time.Second)
	defer cancel()
	status, err := dbx.MongoSetProfiler(ctx, client, db, req.MongoProfilerChange)
	if err != nil {
		return mongoFailure(err)
	}
	httpx.SetAudit(r, "database.mongo.profiler", conn.Name, map[string]any{
		"database": db, "level": status.Level, "slowMs": status.SlowMs, "sampleRate": status.SampleRate,
	})
	httpx.JSON(w, http.StatusOK, status)
	return nil
}

func (s *Server) handleMongoReplication(w http.ResponseWriter, r *http.Request) error {
	client, _, err := s.mongoClient(r)
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	ctx, cancel := timeoutCtx(r, 15*time.Second)
	defer cancel()
	status, err := dbx.MongoReplicationStatus(ctx, client)
	if err != nil {
		return mongoReadFailure(err)
	}
	httpx.JSON(w, http.StatusOK, status)
	return nil
}

// --- Accounts and roles ---------------------------------------------------

func (s *Server) handleMongoUsers(w http.ResponseWriter, r *http.Request) error {
	client, _, err := s.mongoClient(r)
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	// Every database's accounts unless one is named.
	users, err := dbx.MongoListUsers(ctx, client, r.URL.Query().Get("database"))
	if err != nil {
		return mongoReadFailure(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"users": users})
	return nil
}

func (s *Server) handleMongoRoles(w http.ResponseWriter, r *http.Request) error {
	client, conn, err := s.mongoClient(r)
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	db, err := mongoDatabase(r.URL.Query().Get("database"), conn)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	roles, err := dbx.MongoListRoles(ctx, client, db)
	if err != nil {
		return mongoReadFailure(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"database": db, "roles": roles})
	return nil
}

// mongoUserRequest names an account, and carries whatever the route changes
// about it. The database is the one the account authenticates against, and
// is admin when left out, which is where an account made by the official
// image or by this dashboard lives.
type mongoUserRequest struct {
	Database string             `json:"database"`
	User     string             `json:"user"`
	Password string             `json:"password"`
	Roles    []dbx.MongoRoleRef `json:"roles"`
}

func (req *mongoUserRequest) normalise() {
	req.User = strings.TrimSpace(req.User)
	if req.Database == "" {
		req.Database = "admin"
	}
}

// roleNames renders roles for the audit trail.
func (req mongoUserRequest) roleNames() []string {
	out := make([]string, 0, len(req.Roles))
	for _, role := range req.Roles {
		out = append(out, role.Role+"@"+role.DB)
	}
	return out
}

// mongoUserOp is the shared body of the account routes: decode, connect, do,
// audit. The audit entry never carries the password, only that one was set.
func (s *Server) mongoUserOp(w http.ResponseWriter, r *http.Request, action string,
	op func(ctx context.Context, req mongoUserRequest, client *mongo.Client) error,
	detail func(req mongoUserRequest) map[string]any) error {
	var req mongoUserRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	req.normalise()
	client, conn, err := s.mongoClient(r)
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	if err := op(ctx, req, client); err != nil {
		return mongoFailure(err)
	}
	entry := map[string]any{"user": req.User, "database": req.Database, "driver": conn.Driver}
	for k, v := range detail(req) {
		entry[k] = v
	}
	httpx.SetAudit(r, action, conn.Name, entry)
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (s *Server) handleMongoUserCreate(w http.ResponseWriter, r *http.Request) error {
	return s.mongoUserOp(w, r, "database.role.create",
		func(ctx context.Context, req mongoUserRequest, client *mongo.Client) error {
			return dbx.MongoCreateUserIn(ctx, client, req.Database, req.User, req.Password, req.Roles)
		},
		func(req mongoUserRequest) map[string]any { return map[string]any{"roles": req.roleNames()} })
}

func (s *Server) handleMongoUserPassword(w http.ResponseWriter, r *http.Request) error {
	return s.mongoUserOp(w, r, "database.role.alter",
		func(ctx context.Context, req mongoUserRequest, client *mongo.Client) error {
			return dbx.MongoSetUserPassword(ctx, client, req.Database, req.User, req.Password)
		},
		func(mongoUserRequest) map[string]any { return map[string]any{"password": true} })
}

func (s *Server) handleMongoUserGrant(w http.ResponseWriter, r *http.Request) error {
	return s.mongoUserOp(w, r, "database.role.grant",
		func(ctx context.Context, req mongoUserRequest, client *mongo.Client) error {
			return dbx.MongoGrantRoles(ctx, client, req.Database, req.User, req.Roles)
		},
		func(req mongoUserRequest) map[string]any { return map[string]any{"roles": req.roleNames()} })
}

func (s *Server) handleMongoUserRevoke(w http.ResponseWriter, r *http.Request) error {
	return s.mongoUserOp(w, r, "database.role.revoke",
		func(ctx context.Context, req mongoUserRequest, client *mongo.Client) error {
			return dbx.MongoRevokeRoles(ctx, client, req.Database, req.User, req.Roles)
		},
		func(req mongoUserRequest) map[string]any { return map[string]any{"roles": req.roleNames()} })
}

func (s *Server) handleMongoUserDrop(w http.ResponseWriter, r *http.Request) error {
	return s.mongoUserOp(w, r, "database.role.drop",
		func(ctx context.Context, req mongoUserRequest, client *mongo.Client) error {
			return dbx.MongoDropUserIn(ctx, client, req.Database, req.User)
		},
		func(mongoUserRequest) map[string]any { return nil })
}

// --- The console ----------------------------------------------------------
//
// One route, any command. That is the position POST /query is in for SQL, and
// it is handled the same way: the route asks for the capability every command
// needs, and the handler reads the command and asks for the rest by hand.
// What "the rest" is comes from dbx.MongoClassifyCommand, which fails closed.

type mongoCommandRequest struct {
	// Database is where the command runs: the connection's own when left
	// out, and admin when the connection names none.
	Database string `json:"database"`
	// Command is the command document as Extended JSON or shell syntax.
	Command string `json:"command"`
}

// mongoCommandTimeout is how long one console command may take. A validate
// or a compact on a large collection is slow and allowed.
const mongoCommandTimeout = 5 * time.Minute

// mongoConsoleRequires lists the capabilities a verdict needs, in the order
// they are checked.
func mongoConsoleRequires(v dbx.MongoVerdict) []auth.Capability {
	out := []auth.Capability{auth.CapServiceControl}
	if v.Admin {
		out = append(out, auth.CapSystemAdmin)
	}
	if v.Class == dbx.MongoClassDestructive {
		out = append(out, auth.CapDestructive)
	}
	return out
}

// mongoConsoleDatabase is the database a console command runs in.
func mongoConsoleDatabase(name string, conn *dbConnection) string {
	if name != "" {
		return name
	}
	if conn.Database != "" {
		return conn.Database
	}
	return "admin"
}

// mongoConsoleSentence says what a verdict means to the person who typed the
// command.
func mongoConsoleSentence(v dbx.MongoVerdict) string {
	if v.Reason == "" {
		return v.Command
	}
	return v.Command + ": " + v.Reason
}

func (s *Server) handleMongoCommandClassify(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	var req mongoCommandRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	// The connection is resolved so the route answers 404 for one that does
	// not exist, like every other route under it; nothing is dialled.
	conn, _, err := s.mongoRow(r)
	if err != nil {
		return err
	}
	cmd, verdict, err := dbx.MongoClassifyCommand(req.Command)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	db := mongoConsoleDatabase(req.Database, conn)
	dbx.MongoGuardCommand(db, cmd, &verdict)
	requires := mongoConsoleRequires(verdict)
	allowed := verdict.Class != dbx.MongoClassBlocked
	p := httpx.MustPrincipal(r)
	for _, c := range requires {
		allowed = allowed && p.Can(c)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"verdict": verdict, "requires": requires, "allowed": allowed, "database": db,
	})
	return nil
}

func (s *Server) handleMongoCommandList(w http.ResponseWriter, r *http.Request) error {
	if _, _, err := s.mongoRow(r); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"commands": dbx.MongoCommandNames()})
	return nil
}

func (s *Server) handleMongoCommand(w http.ResponseWriter, r *http.Request) error {
	var req mongoCommandRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	conn, dsn, err := s.mongoRow(r)
	if err != nil {
		return err
	}
	cmd, verdict, err := dbx.MongoClassifyCommand(req.Command)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	db := mongoConsoleDatabase(req.Database, conn)
	dbx.MongoGuardCommand(db, cmd, &verdict)
	// The entry is the command's name and what it was aimed at, set before
	// anything is decided so a refused command is on the trail under its own
	// name. The body is not recorded: an insert's is the documents, a
	// createUser's a password.
	detail := map[string]any{
		"database": db, "command": verdict.Command, "class": verdict.Class, "known": verdict.Known,
	}
	if verdict.Target != "" {
		detail["target"] = verdict.Target
	}
	httpx.SetAudit(r, "database.mongo.command", conn.Name, detail)
	// Everything the verdict demands is settled before the server is
	// dialled, so a refused command never reaches it.
	p := httpx.MustPrincipal(r)
	switch {
	case verdict.Class == dbx.MongoClassBlocked:
		return httpx.Err(http.StatusBadRequest, "command_blocked",
			mongoConsoleSentence(verdict)+"; it is not run from this console")
	case verdict.Admin && !p.Can(auth.CapSystemAdmin):
		return httpx.Err(http.StatusForbidden, "forbidden",
			verdict.Command+" manages accounts or the server's own settings, and your role does not permit that (system.admin)")
	}
	if verdict.Class == dbx.MongoClassDestructive {
		if err := s.mongoNeedsDestructive(r, mongoConsoleSentence(verdict)); err != nil {
			return err
		}
	}
	client, err := dbx.MongoClient(r.Context(), dsn)
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "connect_failed", err.Error())
	}
	defer client.Disconnect(context.Background())
	ctx, cancel := timeoutCtx(r, mongoCommandTimeout)
	defer cancel()
	// The one check that needs the server: a collection the command names
	// may be a view over the credentials the guard above refused by name.
	if err := dbx.MongoGuardCommandViews(ctx, client, db, cmd); err != nil {
		return mongoFailure(err)
	}
	reply, err := dbx.MongoRunCommand(ctx, client, db, cmd)
	if err != nil {
		return mongoFailure(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"verdict": verdict, "database": db, "reply": reply})
	return nil
}
