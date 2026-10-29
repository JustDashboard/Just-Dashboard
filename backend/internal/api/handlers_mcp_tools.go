package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/mcp"
)

// What each MCP tool does, and what it is held to. Every answer the model can
// act on — a refusal, a statement the engine rejected, a database that would
// not answer — is a result marked as an error rather than a protocol error,
// so the model reads why; the audit entry records the same outcome as
// "refused" or "failed" and counts as a failure in the trail.

const (
	mcpReadTimeout  = 30 * time.Second
	mcpWriteTimeout = 60 * time.Second
	// mcpPlanBytes bounds a plan's text, which on a wide join is long.
	mcpPlanBytes = 32 << 10
)

// mcpCall is one tool call: who made it, what its key reaches, and what its
// audit entry will say.
type mcpCall struct {
	s      *Server
	r      *http.Request
	p      *httpx.Principal
	grants map[int64]auth.TokenAccess
	target string
	detail map[string]any
	failed bool
}

// mcpDatabase is a granted connection, opened.
type mcpDatabase struct {
	conn   *dbConnection
	access auth.TokenAccess
	pool   *sql.DB
}

func (s *Server) callMCPTool(r *http.Request, p *httpx.Principal, req *mcp.Request) map[string]any {
	c := &mcpCall{s: s, r: r, p: p, detail: map[string]any{"key": p.Token.Name, "prefix": p.Token.Prefix}}
	result := c.run(req.Name, req.Arguments)
	if c.failed {
		httpx.AuditFailed(r)
	}
	area := "database"
	if _, generated := s.mcpOperation(req.Name); mcp.IsDeployTool(req.Name) || generated {
		area = "deploy"
	}
	httpx.SetAudit(r, area+".mcp."+req.Name, c.target, c.detail)
	return result
}

func (c *mcpCall) run(tool string, raw []byte) map[string]any {
	if !c.s.mcp.enter(c.p.TokenID) {
		return c.refuse("busy", fmt.Sprintf("This key already has %d calls running; wait for one to finish.", mcpCallsPerKey))
	}
	defer c.s.mcp.leave(c.p.TokenID)
	if op, generated := c.s.mcpOperation(tool); generated {
		return c.operation(op, raw)
	}
	grants, err := c.s.Auth.TokenConnections(c.r.Context(), c.p.TokenID)
	if err != nil {
		return c.fail("the key's databases could not be read", nil)
	}
	c.grants = map[int64]auth.TokenAccess{}
	for _, g := range grants {
		c.grants[g.ConnectionID] = g.Access
	}
	args, err := mcp.DecodeArguments(tool, raw)
	if err != nil {
		return c.refuse("arguments", err.Error())
	}
	switch tool {
	case mcp.ToolListConnections:
		return c.listConnections()
	case mcp.ToolListTables:
		return c.listTables(args)
	case mcp.ToolDescribeSchema:
		return c.describeSchema(args)
	case mcp.ToolSampleRows:
		return c.sampleRows(args)
	case mcp.ToolExplain:
		return c.explain(args)
	case mcp.ToolRunQuery:
		return c.runQuery(args)
	case mcp.ToolRunWrite:
		return c.runWrite(args)
	case mcp.ToolListDeployments:
		return c.listDeployments()
	case mcp.ToolListRuns:
		return c.listRuns(args)
	case mcp.ToolReadRunLog:
		return c.readRunLog(args)
	case mcp.ToolListReleases:
		return c.listReleases(args)
	case mcp.ToolDeploy:
		return c.deploy(args)
	case mcp.ToolRollback:
		return c.rollback(args)
	}
	return c.refuse("unknown", "Unknown tool: "+tool)
}

func (c *mcpCall) refuse(reason, message string) map[string]any {
	c.failed = true
	c.detail["outcome"], c.detail["reason"] = "refused", reason
	return mcp.Failure(message)
}

// fail is a call the database or the dashboard could not answer. What the
// engine said goes to the model as it is, and to the trail as its code alone
// (dbx.ErrorCode), since its text quotes what it was sent. A failure with no
// error behind it is one of the dashboard's own sentences, which quote nothing.
func (c *mcpCall) fail(message string, err error) map[string]any {
	c.failed = true
	c.detail["outcome"] = "failed"
	switch code := dbx.ErrorCode(err); {
	case err == nil:
		c.detail["error"] = message
	case code != "":
		c.detail["error"] = code
	case errors.Is(err, context.DeadlineExceeded):
		c.detail["error"] = "timeout"
	default:
		c.detail["error"] = "error"
	}
	return mcp.Failure(message)
}

func (c *mcpCall) answer(v any) map[string]any {
	c.detail["outcome"] = "ok"
	return mcp.Result(v)
}

// granted is every connection the key reaches, by name, from its stored rows.
func (c *mcpCall) granted() ([]*dbConnRecord, error) {
	rows, err := c.s.Store.DB.QueryContext(c.r.Context(), `SELECT `+dbConnColumns+` FROM db_connections ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*dbConnRecord
	for rows.Next() {
		rec, err := scanDBConn(rows.Scan)
		if err != nil {
			return nil, err
		}
		if _, ok := c.grants[rec.conn.ID]; ok {
			out = append(out, rec)
		}
	}
	return out, rows.Err()
}

// open finds the granted connection a call names and opens it, refusing a
// protected one whatever the call would do there: nothing about a database
// the operator protected reaches an AI client.
func (c *mcpCall) open(name string) (*mcpDatabase, map[string]any) {
	if name == "" {
		return nil, c.refuse("arguments", "Name the database: connection is required.")
	}
	recs, err := c.granted()
	if err != nil {
		return nil, c.fail("the key's databases could not be read", nil)
	}
	var rec *dbConnRecord
	for _, one := range recs {
		if one.conn.Name == name {
			rec = one
		}
	}
	if rec == nil {
		var folded []*dbConnRecord
		for _, one := range recs {
			if strings.EqualFold(one.conn.Name, name) {
				folded = append(folded, one)
			}
		}
		if len(folded) == 1 {
			rec = folded[0]
		}
	}
	if rec == nil {
		c.target = clipTarget(name)
		return nil, c.refuse("not_granted", fmt.Sprintf("No database named %q is open to this key; list_connections names the ones that are.", name))
	}
	c.target = rec.conn.Name
	switch {
	case rec.conn.ReadOnly:
		return nil, c.refuse("protected", fmt.Sprintf("%s is a protected database: the dashboard refuses every MCP call to it, reads included.", rec.conn.Name))
	case !rec.conn.Driver.IsSQL():
		return nil, c.refuse("engine", fmt.Sprintf("%s is a %s database, and these tools speak SQL.", rec.conn.Name, driverLabels[rec.conn.Driver].label))
	}
	conn, dsn, err := c.s.openDBConn(rec)
	if err != nil {
		return nil, c.refuse("unusable", fmt.Sprintf("%s cannot be opened: %s.", rec.conn.Name, brokenReason(err)))
	}
	pool, err := c.s.modules.dbs.Pool(c.r.Context(), conn.ID, conn.Driver, dsn)
	if err != nil {
		return nil, c.fail(fmt.Sprintf("%s did not answer: %s", conn.Name, connectError(dsn, err)), err)
	}
	identity := c.s.identityOf(conn.ID, dsn, func() dbx.Identity { return dbx.IdentifySQL(c.r.Context(), pool, conn.Driver) })
	if !dbx.Capable(conn.Driver, identity.Flavor, "mcp") {
		return nil, c.refuse("engine", fmt.Sprintf("%s is %s, which these tools are not offered for.", conn.Name, dbx.FlavorLabel(identity.Flavor)))
	}
	return &mcpDatabase{conn: conn, access: c.grants[conn.ID], pool: pool}, nil
}

type mcpConnection struct {
	Connection  string           `json:"connection"`
	Engine      string           `json:"engine"`
	Database    string           `json:"database,omitempty"`
	Environment string           `json:"environment,omitempty"`
	Access      auth.TokenAccess `json:"access"`
}

type mcpUnavailable struct {
	Connection string `json:"connection"`
	Reason     string `json:"reason"`
}

// listConnections reads the saved rows and dials nothing: what each server
// is comes from what it last said, or from its driver.
func (c *mcpCall) listConnections() map[string]any {
	recs, err := c.granted()
	if err != nil {
		return c.fail("the key's databases could not be read", nil)
	}
	open, unavailable := []mcpConnection{}, []mcpUnavailable{}
	for _, rec := range recs {
		switch {
		case rec.conn.ReadOnly:
			unavailable = append(unavailable, mcpUnavailable{rec.conn.Name, "protected: the dashboard refuses every MCP call to it"})
			continue
		case !rec.conn.Driver.IsSQL():
			unavailable = append(unavailable, mcpUnavailable{rec.conn.Name, driverLabels[rec.conn.Driver].label + ", which these tools do not speak"})
			continue
		}
		conn, dsn, err := c.s.openDBConn(rec)
		if err != nil {
			unavailable = append(unavailable, mcpUnavailable{rec.conn.Name, brokenReason(err)})
			continue
		}
		engine := driverLabels[conn.Driver].label
		if identity, _, ok := c.s.lastIdentity(conn.ID, dsn); ok && identity.Flavor != "" {
			engine = strings.TrimSpace(dbx.FlavorLabel(identity.Flavor) + " " + identity.Number)
		}
		open = append(open, mcpConnection{
			Connection: conn.Name, Engine: engine, Database: conn.Database,
			Environment: conn.Environment, Access: c.grants[conn.ID],
		})
	}
	c.detail["connections"] = len(open)
	return c.answer(map[string]any{"connections": open, "unavailable": unavailable})
}

func (c *mcpCall) context(timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.r.Context(), timeout)
}

func (c *mcpCall) listTables(args mcp.Arguments) map[string]any {
	db, refused := c.open(args.Connection)
	if refused != nil {
		return refused
	}
	c.detail["schema"] = args.Schema
	ctx, cancel := c.context(mcpReadTimeout)
	defer cancel()
	tables, truncated, err := mcp.ListTables(ctx, db.pool, db.conn.Driver, args.Schema)
	if err != nil {
		return c.fail(err.Error(), err)
	}
	c.detail["tables"] = len(tables)
	return c.answer(map[string]any{"connection": db.conn.Name, "tables": tables, "truncated": truncated})
}

func (c *mcpCall) describeSchema(args mcp.Arguments) map[string]any {
	if len(args.Tables) > mcp.DescribeNamed {
		return c.refuse("arguments", fmt.Sprintf("Name at most %d tables at a time.", mcp.DescribeNamed))
	}
	db, refused := c.open(args.Connection)
	if refused != nil {
		return refused
	}
	c.detail["schema"] = args.Schema
	ctx, cancel := c.context(2 * mcpReadTimeout)
	defer cancel()
	schema, err := mcp.DescribeSchema(ctx, db.pool, db.conn.Driver, args.Schema, args.Tables)
	if err != nil {
		return c.fail(err.Error(), err)
	}
	c.detail["tables"] = len(schema.Tables)
	return c.answer(schema)
}

// mcpRows is a result set as a tool answers with it.
type mcpRows struct {
	Connection string `json:"connection"`
	Table      string `json:"table,omitempty"`
	mcp.Rows
	RowsAffected int64 `json:"rowsAffected,omitempty"`
	DurationMs   int64 `json:"durationMs"`
}

func (c *mcpCall) sampleRows(args mcp.Arguments) map[string]any {
	db, refused := c.open(args.Connection)
	if refused != nil {
		return refused
	}
	ctx, cancel := c.context(mcpReadTimeout)
	defer cancel()
	listed, _, err := mcp.ListTables(ctx, db.pool, db.conn.Driver, args.Schema)
	if err != nil {
		return c.fail(err.Error(), err)
	}
	name, ok := mcp.Resolve(args.Table, args.Schema, mcp.Names(listed))
	if !ok {
		c.detail["table"] = args.Table
		return c.refuse("missing", fmt.Sprintf("%s has no table or view named %q, or more than one schema holds one; list_tables names them, and schema.table says which.", db.conn.Name, args.Table))
	}
	c.detail["schema"], c.detail["table"] = name.Schema, name.Name
	limit := clampInt(args.Limit, 1, mcp.SampleMax)
	if args.Limit == 0 {
		limit = mcp.SampleDefault
	}
	start := time.Now()
	page, err := dbx.BrowseTablePage(ctx, db.pool, db.conn.Driver, dbx.BrowseOptions{
		Schema: name.Schema, Table: name.Name, Limit: limit, ClipText: mcp.CellBytes,
	})
	if err != nil {
		return c.fail(err.Error(), err)
	}
	rows := mcp.BoundRows(page.Columns, page.Types, page.Rows, page.Truncated)
	c.detail["rows"] = rows.RowCount
	return c.answer(mcpRows{
		Connection: db.conn.Name, Table: mcp.Qualified(name), Rows: rows,
		DurationMs: time.Since(start).Milliseconds(),
	})
}

// statement reads one statement by the rules of the connection's engine and
// puts its shape in the audit entry, which is all the trail keeps of it.
func (c *mcpCall) statement(db *mcpDatabase, text string, read func(dbx.Driver, string) (*dbx.SQLStatement, error)) (*dbx.SQLStatement, map[string]any) {
	c.detail["statement"] = dbx.StatementShape(db.conn.Driver, text)
	st, err := read(db.conn.Driver, text)
	if err != nil {
		return nil, c.refuse("statement", err.Error())
	}
	c.detail["risk"] = st.Risk.Level
	return st, nil
}

func (c *mcpCall) explain(args mcp.Arguments) map[string]any {
	db, refused := c.open(args.Connection)
	if refused != nil {
		return refused
	}
	st, refused := c.statement(db, args.SQL, dbx.ExplainTarget)
	if refused != nil {
		return refused
	}
	ctx, cancel := c.context(mcpReadTimeout)
	defer cancel()
	plan, err := dbx.Explain(ctx, db.pool, db.conn.Driver, st, dbx.ExplainOptions{})
	if errors.Is(err, dbx.ErrExplainUnsupported) {
		return c.refuse("unsupported", err.Error())
	}
	if err != nil {
		return c.fail(err.Error(), err)
	}
	return c.answer(map[string]any{"connection": db.conn.Name, "plan": planText(plan.Result)})
}

// planText is a plan as the engine printed it, one line a row, the cells of a
// row joined the way psql joins them.
func planText(res *dbx.QueryResult) string {
	if res == nil {
		return ""
	}
	var b strings.Builder
	for _, row := range res.Rows {
		cells := make([]string, len(row))
		for i, v := range row {
			cells[i] = fmt.Sprint(v)
		}
		b.WriteString(strings.Join(cells, " | "))
		b.WriteByte('\n')
		if b.Len() > mcpPlanBytes {
			return b.String()[:mcpPlanBytes] + "…"
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// mayRun is the query route's own question of its caller.
func (c *mcpCall) mayRun() map[string]any {
	if c.p.Can(auth.CapServiceControl) {
		return nil
	}
	return c.refuse("role", "This key's owner has a read-only role, which runs no statements; sample_rows and describe_schema still read.")
}

func (c *mcpCall) runQuery(args mcp.Arguments) map[string]any {
	if refused := c.mayRun(); refused != nil {
		return refused
	}
	db, refused := c.open(args.Connection)
	if refused != nil {
		return refused
	}
	st, refused := c.statement(db, args.SQL, dbx.SingleStatementFor)
	if refused != nil {
		return refused
	}
	if st.Risk.Level != "read" {
		next := "use run_write to change " + db.conn.Name
		if db.access != auth.AccessWrite {
			next = "this key may only read " + db.conn.Name
		}
		return c.refuse("not_a_read", fmt.Sprintf("This statement is not a read (%s). run_query runs reads only; %s.",
			strings.Join(st.Risk.Reasons, "; "), next))
	}
	limit := clampInt(args.MaxRows, 1, mcp.QueryMax)
	if args.MaxRows == 0 {
		limit = mcp.QueryDefault
	}
	return c.execute(db, st, limit, mcpReadTimeout)
}

func (c *mcpCall) runWrite(args mcp.Arguments) map[string]any {
	if refused := c.mayRun(); refused != nil {
		return refused
	}
	db, refused := c.open(args.Connection)
	if refused != nil {
		return refused
	}
	if db.access != auth.AccessWrite {
		return c.refuse("read_only_key", fmt.Sprintf("This key may only read %s; its owner can allow changes on the dashboard's MCP server page.", db.conn.Name))
	}
	st, refused := c.statement(db, args.SQL, dbx.SingleStatementFor)
	if refused != nil {
		return refused
	}
	c.detail["destructive"] = st.Risk.Destructive
	// The same verdict, capability and budget the SQL editor is held to:
	// MCP is never the cheaper way to run a statement.
	if err := c.s.authoriseSQL(c.r, st.Risk); err != nil {
		var apiErr *httpx.APIError
		if errors.As(err, &apiErr) && apiErr.Code == "rate_limited" {
			return c.refuse("rate_limited", "Too many destructive statements in a short time; wait a minute.")
		}
		return c.refuse("role", fmt.Sprintf("This statement is destructive (%s), and the key owner's role does not run destructive statements.",
			strings.Join(st.Risk.Reasons, "; ")))
	}
	return c.execute(db, st, mcp.QueryDefault, mcpWriteTimeout)
}

func (c *mcpCall) execute(db *mcpDatabase, st *dbx.SQLStatement, limit int, timeout time.Duration) map[string]any {
	ctx, cancel := c.context(timeout)
	defer cancel()
	start := time.Now()
	res, err := dbx.RunStatement(ctx, db.pool, db.conn.Driver, st, limit)
	elapsed := time.Since(start).Milliseconds()
	c.detail["ms"] = elapsed
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return c.fail(fmt.Sprintf("The statement ran past %d seconds and was stopped.", int(timeout.Seconds())), ctx.Err())
		}
		return c.fail(err.Error(), err)
	}
	rows := mcp.BoundRows(res.Columns, res.Types, res.Rows, res.Truncated)
	c.detail["rows"], c.detail["rowsAffected"] = rows.RowCount, res.Affected
	return c.answer(mcpRows{Connection: db.conn.Name, Rows: rows, RowsAffected: res.Affected, DurationMs: elapsed})
}
