package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// Protected connections.
//
// A dashboard that can edit a row can edit the wrong row, and the connection
// it happens on is always the one that mattered. An operator marks such a
// connection read-only, and from then on the dashboard may look at it and may
// not change it: no row, key or document edit, no DDL, no import or restore,
// no role or setting change, and no statement through the query runner that
// is not a read. Turning the mark off again is one PUT, by somebody allowed
// to edit the connection.
//
// It is enforced in one place, in front of every route under
// /databases/{id}, rather than by a check in each handler. Sixty handlers
// each remembering to ask is a guarantee that holds until the sixty-first is
// written, and that one would be the new feature nobody thought of as a
// write. So the question is asked the other way round: on a protected
// connection every request that is not a read is refused unless its route is
// on the short list below, and a route added tomorrow is refused until
// somebody decides it belongs there.
//
// The list is what stays possible because it does not change the database:
// the connection's own record and power, a dump taken of it, and the
// dashboard's own state about it — saved queries, the diagram's arrangement.
// A few routes are a read or a write depending on what the request carries;
// those are read here, by the same classification the handler uses, and
// allowed only when it says read.
//
// This is a guard against the dashboard's own controls, not a sandbox around
// the server. It is exactly as strong as the classification: a SELECT that
// calls a function which writes is a read to anything that judges a statement
// by its text. An operator who needs the engine to refuse writes gives the
// connection an account that cannot make them.

// protectedRoute is one route that stays open on a protected connection.
type protectedRoute struct {
	method string
	// pattern is the path under /databases/{id}, empty for the connection
	// itself. A {name} segment matches any one segment.
	pattern string
	// check reads the body when what the route does depends on it, and
	// returns why the request is refused. A nil check allows the route
	// whatever it carries. The driver is the connection's: what a statement
	// is depends on the engine that will read it.
	check func(driver dbx.Driver, body []byte) error
}

var protectedRoutesAllowed = []protectedRoute{
	// The connection's own record. Editing it is how protection is taken off
	// again; forgetting it and changing where its server is reachable from
	// leave what is in the database alone, as starting and stopping it do.
	{http.MethodPut, "", nil},
	{http.MethodDelete, "", nil},
	{http.MethodPut, "/access", nil},
	{http.MethodPost, "/power", nil},
	// A dump reads the database and writes a file on this machine. One made
	// elsewhere and added to the list, and one deleted from it, are files on
	// this machine too. Loading any of them back is a restore, which is not
	// on this list.
	{http.MethodPost, "/backup", nil},
	{http.MethodPost, "/backups/upload", nil},
	{http.MethodDelete, "/backups", nil},
	// The dashboard's own state about the connection.
	{http.MethodPost, "/queries", nil},
	{http.MethodPut, "/queries/{qid}", nil},
	{http.MethodDelete, "/queries/{qid}", nil},
	{http.MethodPut, "/diagram", nil},
	{http.MethodDelete, "/diagram", nil},
	// Requests that are POSTs because they carry a body, and read.
	{http.MethodPost, "/classify", nil},
	{http.MethodPost, "/orm", nil},
	{http.MethodPost, "/rows/sql", nil},
	{http.MethodPost, "/redis/classify", nil},
	{http.MethodPost, "/mongo/find", nil},
	{http.MethodPost, "/mongo/count", nil},
	{http.MethodPost, "/mongo/document", nil},
	{http.MethodPost, "/mongo/explain", nil},
	{http.MethodPost, "/mongo/aggregate/preview", nil},
	{http.MethodPost, "/mongo/schema", nil},
	{http.MethodPost, "/mongo/validation/check", nil},
	{http.MethodPost, "/mongo/command/classify", nil},
	// Stopping work in flight. It changes no data and no schema, and the
	// protected connection is usually the production one, where stopping a
	// runaway statement is exactly what its operator needs to be able to do.
	{http.MethodPost, "/query/cancel", nil},
	{http.MethodPost, "/activity/cancel", nil},
	{http.MethodPost, "/activity/kill", nil},
	{http.MethodPost, "/redis/clients/kill", nil},
	{http.MethodPost, "/mongo/killop", nil},
	// Read or write by what they carry.
	{http.MethodPost, "/explain", protectedExplain},
	{http.MethodPost, "/query", protectedStatements},
	{http.MethodPost, "/script", protectedStatements},
	{http.MethodPost, "/export/query", protectedExportQuery},
	{http.MethodPost, "/changes", protectedChanges},
	{http.MethodPost, "/maintenance", protectedMaintenance},
	{http.MethodPost, "/redis/command", protectedRedis(redisCommandWrites)},
	{http.MethodPost, "/keys/bulk", protectedRedis(redisBulkWrites)},
	{http.MethodPost, "/aggregate", protectedMongo(mongoReadOnlyPipeline)},
	{http.MethodPost, "/mongo/command", protectedMongo(mongoReadOnlyCommand)},
	{http.MethodPatch, "/mongo/documents", protectedMongo(mongoReadOnlyDryRun)},
	{http.MethodDelete, "/mongo/documents", protectedMongo(mongoReadOnlyDryRun)},
}

// protectReadOnlyConnections refuses every change to a protected connection's
// data or schema. It is mounted once, in front of every database route.
func (s *Server) protectReadOnlyConnections(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := s.refuseOnProtected(r); err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) refuseOnProtected(r *http.Request) error {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return nil
	}
	id, rest, ok := connectionRoute(databaseRoutePath(r))
	if !ok {
		return nil
	}
	var (
		driver   string
		readOnly bool
	)
	err := s.Store.DB.QueryRowContext(r.Context(),
		`SELECT driver, read_only FROM db_connections WHERE id = ?`, id).Scan(&driver, &readOnly)
	if err == sql.ErrNoRows {
		// No such connection, so nothing to protect; the handler says so.
		return nil
	}
	if err != nil {
		// Not knowing whether a connection is protected is not permission.
		return httpx.Internal(err)
	}
	if !readOnly {
		return nil
	}
	// A schema form asked only for its statement runs nothing: runDDL answers
	// a preview before it executes, and every route under /ddl/ ends in runDDL
	// (TestEveryStructureRouteCanBeShownWithoutRunning walks the router and
	// holds a route added there to that). The flag is in the address, which
	// a check of the body cannot see, so it is asked here.
	if strings.HasPrefix(rest, "/ddl/") && ddlPreview(r) {
		return nil
	}
	rule, allowed := protectedRule(r.Method, rest)
	if !allowed {
		return protectedRefusal("this connection is protected: nothing that changes its data or schema can be done from the dashboard until protection is turned off in its settings")
	}
	if rule.check == nil {
		return nil
	}
	// Read here and handed back whole, so the handler decodes exactly the
	// bytes that were judged. The bound is the one every JSON body has.
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 4<<20))
	if err != nil {
		return protectedRefusal("this connection is protected, and the request could not be read to see whether it changes anything")
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return rule.check(dbx.Driver(driver), body)
}

func protectedRefusal(format string, args ...any) error {
	return httpx.Err(http.StatusConflict, "connection_read_only", fmt.Sprintf(format, args...))
}

// databaseRoutePath is the path under /databases that chi is about to route
// on. It is read from the route context rather than cut out of the URL so
// that this decides on exactly the string the router will: a path the two
// read differently is a path one of them can be talked past with.
func databaseRoutePath(r *http.Request) string {
	if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePath != "" {
		return rc.RoutePath
	}
	// Outside a router: the section is the first /databases in the path, and
	// a later one is a route under it (/{id}/server/databases).
	path := r.URL.EscapedPath()
	if i := strings.Index(path, "/databases"); i >= 0 {
		return path[i+len("/databases"):]
	}
	return path
}

// connectionRoute splits a path under /databases into the connection it is
// about and what is asked of it. A first segment that is not a number is one
// of the section's own routes — the fleet, the driver catalogue, provisioning
// — and concerns no connection. The number is parsed as parseID parses it, so
// the two cannot disagree about which connection a path names.
func connectionRoute(path string) (id int64, rest string, ok bool) {
	segment, rest, more := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	id, err := strconv.ParseInt(segment, 10, 64)
	if err != nil {
		return 0, "", false
	}
	if more {
		rest = "/" + rest
	}
	return id, rest, true
}

func protectedRule(method, rest string) (protectedRoute, bool) {
	for _, rule := range protectedRoutesAllowed {
		if rule.method == method && routeMatches(rule.pattern, rest) {
			return rule, true
		}
	}
	return protectedRoute{}, false
}

// routeMatches compares a path with a pattern segment by segment.
func routeMatches(pattern, path string) bool {
	want, got := strings.Split(pattern, "/"), strings.Split(path, "/")
	if len(want) != len(got) {
		return false
	}
	for i := range want {
		if strings.HasPrefix(want[i], "{") && strings.HasSuffix(want[i], "}") {
			if got[i] == "" {
				return false
			}
			continue
		}
		if want[i] != got[i] {
			return false
		}
	}
	return true
}

// protectedFields reads the top level of a request body the way the handler's
// decoder is about to.
//
// encoding/json fills a struct field from a key spelled in any case, and from
// the last of two keys that name it. A check that looked a field up by its
// exact name judged "pipeline" and let the handler run "Pipeline". So the
// names are folded here as the decoder folds them, and a body that names one
// field twice, in any spelling, is refused rather than second-guessed: nothing
// this dashboard sends does. A name outside ASCII is refused for the same
// reason — the decoder's folding reaches a few letters there, the long s for
// an s among them, and no field is named with one.
func protectedFields(body []byte) (map[string]json.RawMessage, error) {
	unreadable := protectedRefusal("this connection is protected, and the request could not be read to see whether it changes anything")
	if !json.Valid(body) {
		return nil, unreadable
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	if opening, err := dec.Token(); err != nil || opening != json.Delim('{') {
		return nil, unreadable
	}
	fields := map[string]json.RawMessage{}
	for dec.More() {
		token, err := dec.Token()
		name, isName := token.(string)
		if err != nil || !isName {
			return nil, unreadable
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, unreadable
		}
		for i := 0; i < len(name); i++ {
			if name[i] >= 0x80 {
				return nil, protectedRefusal("this connection is protected, and %q is not a field this check can read to see whether it changes anything", name)
			}
		}
		folded := strings.ToLower(name)
		if _, twice := fields[folded]; twice {
			return nil, protectedRefusal("this connection is protected, and the request names %q more than once, so what it asks for cannot be told", folded)
		}
		fields[folded] = value
	}
	return fields, nil
}

// protectedStatementFields are the body fields that carry SQL, in every shape
// the statement routes take it: one statement, or a list of them. The names
// here and below are folded, as protectedFields hands them over.
var protectedStatementFields = map[string]bool{
	"query": true, "sql": true, "script": true, "statements": true, "queries": true,
}

// protectedTextOptions are body fields that are text and are not SQL.
var protectedTextOptions = map[string]bool{
	"queryid": true, "format": true, "schema": true, "database": true, "name": true,
}

// protectedStatementRefusal is why a statement may not run on a protected
// connection, and empty when it may: it has to classify as a read.
//
// The driver is taken because what a statement is depends on the engine that
// reads it — a backslash ends nothing in Postgres and escapes a quote in
// MySQL, and a dollar-quoted body is one string there and a refusal anywhere
// else. Read by its own engine's rules, a statement that starts as a read and
// writes — SELECT … INTO, a WITH that leads into a MERGE or an INSERT, MySQL's
// INTO OUTFILE — is not classified as one.
func protectedStatementRefusal(driver dbx.Driver, statement string) string {
	risk := dbx.ClassifyFor(driver, statement)
	if risk.Level == "read" {
		return ""
	}
	// Never empty: an empty answer is what lets the statement through.
	if why := strings.Join(risk.Reasons, "; "); why != "" {
		return why
	}
	return "it is classified " + risk.Level
}

// protectedStatements allows a body whose every statement is a read.
//
// It fails closed on what it does not recognise. A field it has never heard
// of that holds text, a list or an object could be a statement under a name
// added after this was written, and letting it through unread would be the
// hole this file exists not to have; only numbers and booleans pass as
// options without being named above.
func protectedStatements(driver dbx.Driver, body []byte) error {
	fields, err := protectedFields(body)
	if err != nil {
		return err
	}
	statements, err := statementsIn(fields)
	if err != nil {
		return err
	}
	if len(statements) == 0 {
		return protectedRefusal("this connection is protected, and the request carries no statement to check")
	}
	for _, statement := range statements {
		if why := protectedStatementRefusal(driver, statement); why != "" {
			return protectedRefusal("this connection is protected, and that statement is not a read: %s", why)
		}
	}
	return nil
}

// statementsIn collects the SQL in a decoded object, and refuses one that
// holds anything which could be SQL under a name not listed above.
func statementsIn(fields map[string]json.RawMessage) ([]string, error) {
	var statements []string
	for name, raw := range fields {
		switch {
		case protectedStatementFields[name]:
			found, err := statementTexts(raw)
			if err != nil {
				var refusal *httpx.APIError
				if errors.As(err, &refusal) {
					return nil, err
				}
				return nil, protectedRefusal("this connection is protected, and %q could not be read as SQL to see whether it changes anything", name)
			}
			statements = append(statements, found...)
		case protectedTextOptions[name]:
		default:
			if first := bytes.TrimSpace(raw); len(first) > 0 && (first[0] == '"' || first[0] == '[' || first[0] == '{') {
				return nil, protectedRefusal("this connection is protected, and %q is not a field this check can read to see whether it changes anything", name)
			}
		}
	}
	return statements, nil
}

// statementTexts reads SQL out of a field: a string, or a list whose entries
// are strings or objects naming their statement "sql" or "query". An entry
// that is an object is held to the rule the body is: it names a statement,
// and nothing else in it could be one.
func statementTexts(raw json.RawMessage) ([]string, error) {
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return []string{one}, nil
	}
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(list))
	for _, entry := range list {
		var text string
		if err := json.Unmarshal(entry, &text); err == nil {
			out = append(out, text)
			continue
		}
		named, err := protectedFields(entry)
		if err != nil {
			return nil, err
		}
		found, err := statementsIn(named)
		if err != nil {
			return nil, err
		}
		if len(found) == 0 {
			return nil, fmt.Errorf("an entry names no statement")
		}
		out = append(out, found...)
	}
	return out, nil
}

// protectedExplainFields are what a request for a plan may carry beside its
// statement and the text options. A plan route that grew a second way of
// running its statement would grow it as a field, and one not named here is
// refused until somebody has decided what it does.
var protectedExplainFields = map[string]bool{"analyze": true, "maxrows": true}

// protectedExplain allows a plan that is only planned. Asking for the plan
// with the statement actually run is running the statement, and is held to
// what running it would be.
func protectedExplain(driver dbx.Driver, body []byte) error {
	fields, err := protectedFields(body)
	if err != nil {
		return err
	}
	for name := range fields {
		if !protectedStatementFields[name] && !protectedTextOptions[name] && !protectedExplainFields[name] {
			return protectedRefusal("this connection is protected, and %q is not a field this check can read to see whether it changes anything", name)
		}
	}
	switch strings.TrimSpace(string(fields["analyze"])) {
	case "", "false", "null":
		return nil
	}
	return protectedStatements(driver, body)
}

// protectedBody reads a request body into the type its handler reads it into,
// with the handler's own decoder: httpx.DecodeJSON, or DecodeJSONNumbers for a
// handler that keeps numbers exact. A check that starts from it judges the
// request the handler will run — the same field for a key spelled in another
// case, the same one of two that name it, the same refusal of a field it does
// not have — because there is no second reading for the two to disagree about.
func protectedBody(body []byte, dst any, exactNumbers bool) error {
	req, err := http.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if exactNumbers {
		return httpx.DecodeJSONNumbers(req, dst)
	}
	return httpx.DecodeJSON(req, dst)
}

// protectedUnreadable is the refusal of a body no check could read.
func protectedUnreadable() error {
	return protectedRefusal("this connection is protected, and the request could not be read to see whether it changes anything")
}

// protectedExportQuery allows a statement's result as a file when the
// statement is a read. The handler asks the same of every export; here it is
// asked before the handler, of the same field, by the same classifier.
func protectedExportQuery(driver dbx.Driver, body []byte) error {
	var req exportQueryRequest
	if err := protectedBody(body, &req, false); err != nil {
		return protectedUnreadable()
	}
	if strings.TrimSpace(req.SQL) == "" {
		return protectedRefusal("this connection is protected, and the request carries no statement to check")
	}
	if why := protectedStatementRefusal(driver, req.SQL); why != "" {
		return protectedRefusal("this connection is protected, and that statement is not a read: %s", why)
	}
	return nil
}

// protectedChanges allows a change set that is only rendered: the grid asks
// for the statements its staged edits would be, to show them, and nothing
// runs. Applying them is the edit the protection is there to refuse.
func protectedChanges(_ dbx.Driver, body []byte) error {
	var req changesRequest
	if err := protectedBody(body, &req, true); err != nil {
		return protectedUnreadable()
	}
	if !req.DryRun {
		return protectedRefusal("this connection is protected: its rows cannot be changed from the dashboard until protection is turned off in its settings")
	}
	return nil
}

// protectedMaintenance lets a consistency check through and nothing else: an
// action the engine's own list marks as one that reads and reports.
func protectedMaintenance(driver dbx.Driver, body []byte) error {
	var req maintenanceRequest
	if err := protectedBody(body, &req, false); err != nil {
		return protectedUnreadable()
	}
	if action, ok := dbx.MaintenanceActionFor(driver, strings.TrimSpace(req.Action)); ok && action.ReadOnly {
		return nil
	}
	return protectedRefusal("this connection is protected: only a check that changes nothing can be run on it")
}

// protectedRedis makes a route check of one of the Redis body checks
// (handlers_db_redis_readonly.go), which say why a request writes.
func protectedRedis(writes func(body []byte) string) func(dbx.Driver, []byte) error {
	return func(_ dbx.Driver, body []byte) error {
		if why := writes(body); why != "" {
			return protectedRefusal("this connection is protected: %s", why)
		}
		return nil
	}
}

// protectedMongo makes a route check of one of the MongoDB body checks
// (handlers_db_mongo_readonly.go). The body is first read as every check here
// reads one, so a field named twice or from outside ASCII is refused; then the
// MongoDB check judges it with the handler's own request type and classifier.
func protectedMongo(check func([]byte) error) func(dbx.Driver, []byte) error {
	return func(_ dbx.Driver, body []byte) error {
		if _, err := protectedFields(body); err != nil {
			return err
		}
		if err := check(body); err != nil {
			return protectedRefusal("this connection is protected, and %v", err)
		}
		return nil
	}
}
