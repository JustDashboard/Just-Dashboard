package api

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/redis/go-redis/v9"
)

// The Redis console.
//
// One route, any command. That is the same position POST /query is in for
// SQL, and it is handled the same way: the route asks for the capability
// every command needs, and the handler reads the command and asks for the
// rest by hand. What "the rest" is comes from dbx.RedisClassify, which fails
// closed — a command nobody listed is destructive until shown otherwise.
//
// The rule the classes are drawn to keep is that the console is never the
// cheaper way to do something a form gates. Deleting a key from the browser
// needs the destructive capability, so DEL does here; changing a setting
// from the configuration page needs system.admin, so CONFIG SET does here.

type redisCommandRequest struct {
	// DB is the logical database to run in. Absent, it is ?db=, and absent
	// there too, the one the connection string names.
	DB      *int   `json:"db"`
	Command string `json:"command"`
}

// redisCommandTimeout is how long one console command may take to answer.
// The eight seconds an administrative read gets is not enough for a KEYS on a
// large keyspace, which is slow and allowed.
const redisCommandTimeout = 30 * time.Second

// redisConsoleLine is one console line on its way to being run.
type redisConsoleLine struct {
	conn    *dbConnection
	dsn     string
	args    []string
	db      int
	client  *redis.Client
	flags   *dbx.RedisCommandFlags
	verdict dbx.RedisVerdict
}

// redisConsole parses a console line and classifies it as far as the
// dashboard's own table can. It is shared by the route that runs a command
// and the route that only says what running it would take, so the two cannot
// disagree.
func (s *Server) redisConsole(r *http.Request) (*redisConsoleLine, error) {
	var req redisCommandRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return nil, err
	}
	args, err := dbx.RedisParseCommand(req.Command)
	if err != nil {
		return nil, httpx.BadRequest("%v", err)
	}
	conn, dsn, err := s.redisRow(r)
	if err != nil {
		return nil, err
	}
	db, err := redisDB(r.URL.Query().Get("db"))
	if err != nil {
		return nil, err
	}
	if req.DB != nil {
		if *req.DB < 0 {
			return nil, httpx.BadRequest("db must be a database number")
		}
		db = *req.DB
	}
	return &redisConsoleLine{
		conn: conn, dsn: dsn, args: args, db: db,
		verdict: dbx.RedisClassify(args, nil),
	}, nil
}

// connect opens the connection the line runs on and asks the server what it
// says about the command. For a command the table does not know, that answer
// is the rest of the classification; for one it does, the answer can only
// make the verdict stricter, and never as far as needing another capability.
func (l *redisConsoleLine) connect(r *http.Request) error {
	client, err := dbx.RedisOpen(r.Context(), l.dsn, dbx.RedisOpenOptions{DB: l.db, ReadTimeout: redisCommandTimeout})
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "connect_failed", err.Error())
	}
	l.client, l.db = client, client.Options().DB
	ctx, cancel := timeoutCtx(r, 15*time.Second)
	defer cancel()
	l.flags = dbx.RedisCommandLookup(ctx, client, l.args)
	l.verdict = dbx.RedisClassify(l.args, l.flags)
	return nil
}

func (l *redisConsoleLine) close() {
	if l.client != nil {
		l.client.Close()
	}
}

// requires lists the capabilities the line needs, in the order they are
// checked.
func (l *redisConsoleLine) requires() []auth.Capability {
	out := []auth.Capability{auth.CapServiceControl}
	if l.verdict.Admin {
		out = append(out, auth.CapSystemAdmin)
	}
	if l.verdict.Class == dbx.RedisClassDangerous {
		out = append(out, auth.CapDestructive)
	}
	return out
}

// sentence joins the verdict's reasons into what the caller is told.
func (l *redisConsoleLine) sentence() string {
	if len(l.verdict.Reasons) == 0 {
		return l.verdict.Name
	}
	return l.verdict.Name + " " + strings.Join(l.verdict.Reasons, "; ")
}

// gate applies the verdict: a blocked command is refused, an administrative
// one needs system.admin, a dangerous one the destructive capability and its
// budget.
func (s *Server) redisConsoleGate(r *http.Request, l *redisConsoleLine) error {
	if l.verdict.Class == dbx.RedisClassBlocked {
		return httpx.Err(http.StatusBadRequest, "command_blocked", l.sentence())
	}
	if l.verdict.Admin && !httpx.MustPrincipal(r).Can(auth.CapSystemAdmin) {
		return httpx.Err(http.StatusForbidden, "forbidden", fmt.Sprintf(
			"%s reads or changes the server's own configuration or accounts, and your role does not permit that (system.admin)",
			l.verdict.Name))
	}
	if l.verdict.Class == dbx.RedisClassDangerous {
		return s.redisDestructive(r, l.sentence())
	}
	return nil
}

// handleRedisClassify says what a console line is and what running it would
// need. Nothing is run. The server is asked one thing, and only when the
// dashboard does not know the command: whether it does.
func (s *Server) handleRedisClassify(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	line, err := s.redisConsole(r)
	if err != nil {
		return err
	}
	if !line.verdict.Known {
		if err := line.connect(r); err != nil {
			return err
		}
		defer line.close()
	}
	p := httpx.MustPrincipal(r)
	allowed := line.verdict.Class != dbx.RedisClassBlocked
	requires := line.requires()
	for _, c := range requires {
		if !p.Can(c) {
			allowed = false
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"name": line.verdict.Name, "class": line.verdict.Class, "admin": line.verdict.Admin,
		"slow": line.verdict.Slow, "known": line.verdict.Known, "reasons": line.verdict.Reasons,
		"requires": requires, "allowed": allowed,
	})
	return nil
}

// handleRedisCommand runs one console command.
func (s *Server) handleRedisCommand(w http.ResponseWriter, r *http.Request) error {
	line, err := s.redisConsole(r)
	if err != nil {
		return err
	}
	// What is recorded is the command's name, the keys it named and, for the
	// few commands that act on the server, which parameter or account. Never
	// the arguments: `SET session:9 <token>` is on the trail as SET
	// session:9.
	detail := map[string]any{
		"command": line.verdict.Name, "class": line.verdict.Class, "arguments": len(line.args) - 1,
	}
	if subject := dbx.RedisCommandSubject(line.args); len(subject) > 0 {
		detail["subject"] = subject
	}
	httpx.SetAudit(r, "database.redis.command", line.conn.Name, detail)

	// A command the table knows is judged before anything is dialled. One it
	// does not know needs the server's word first, and is judged after.
	if line.verdict.Known {
		if err := s.redisConsoleGate(r, line); err != nil {
			return err
		}
	}
	if err := line.connect(r); err != nil {
		return err
	}
	defer line.close()
	detail["class"], detail["db"] = line.verdict.Class, line.db
	detail["keys"] = dbx.RedisCommandKeys(line.args, line.flags)
	httpx.SetAudit(r, "database.redis.command", line.conn.Name, detail)
	if !line.verdict.Known {
		if err := s.redisConsoleGate(r, line); err != nil {
			return err
		}
	}

	ctx, cancel := timeoutCtx(r, redisCommandTimeout+5*time.Second)
	defer cancel()
	reply, truncated, elapsed, err := dbx.RedisRunCommand(ctx, line.client, line.args)
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	res := dbx.RedisCommandResult{
		RedisVerdict: line.verdict, DB: line.db,
		Ms:    float64(elapsed.Microseconds()) / 1000,
		Reply: reply, Truncated: truncated,
	}
	// The reply's text stays off the trail as well: the server's "unknown
	// command" error quotes the arguments it was given.
	detail["ms"] = res.Ms
	detail["failed"] = reply.Type == "error"
	httpx.SetAudit(r, "database.redis.command", line.conn.Name, detail)
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

// redisCommandRefs keeps each server's command reference between requests.
// It is a few hundred kilobytes of reply that changes only when the server is
// upgraded or a module is loaded, and the console asks for it every time it
// opens.
var redisCommandRefs sync.Map

type redisCommandRefKey struct {
	server *Server
	id     int64
}

type redisCommandRefEntry struct {
	at time.Time
	// identity is what the reference was built against. A different answer
	// from the same connection — an upgrade, another flavour behind the same
	// address — discards it.
	identity string
	commands []dbx.RedisCommandRef
}

const redisCommandRefTTL = 10 * time.Minute

// handleRedisCommands returns the command reference a console completes
// from: every command this server has, with the server's own documentation
// where it carries any and the class the console gives it.
func (s *Server) handleRedisCommands(w http.ResponseWriter, r *http.Request) error {
	client, conn, err := s.redisClient(r)
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
	modules := make([]string, 0, len(profile.Modules))
	for _, m := range profile.Modules {
		modules = append(modules, fmt.Sprintf("%s@%d", m.Name, m.Version))
	}
	identity := profile.Flavor + " " + profile.Version + " " + strings.Join(modules, ",")
	key := redisCommandRefKey{server: s, id: conn.ID}
	var commands []dbx.RedisCommandRef
	if kept, ok := redisCommandRefs.Load(key); ok {
		entry := kept.(redisCommandRefEntry)
		if entry.identity == identity && time.Since(entry.at) < redisCommandRefTTL {
			commands = entry.commands
		}
	}
	if commands == nil {
		if commands, err = dbx.RedisCommandReference(ctx, client, profile); err != nil {
			return redisFail(err, false)
		}
		redisCommandRefs.Store(key, redisCommandRefEntry{at: time.Now(), identity: identity, commands: commands})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"flavor": profile.Flavor, "version": profile.Version,
		// Documented says whether the server supplied summaries and syntax,
		// which it does from Redis 7; without them the list is names, arity
		// and flags.
		"documented": profile.Features.CommandDocs,
		"commands":   commands,
	})
	return nil
}
