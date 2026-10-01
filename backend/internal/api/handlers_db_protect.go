package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
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
	// whatever it carries.
	check func(body []byte) error
}

var protectedRoutesAllowed = []protectedRoute{
	// The connection's own record. Editing it is how protection is taken off
	// again; forgetting it and changing where its server is reachable from
	// leave what is in the database alone, as starting and stopping it do.
	{http.MethodPut, "", nil},
	{http.MethodDelete, "", nil},
	{http.MethodPut, "/access", nil},
	{http.MethodPost, "/power", nil},
	// A dump reads the database and writes a file on this machine.
	{http.MethodPost, "/backup", nil},
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
	{http.MethodPost, "/query/cancel", nil},
	// Read or write by what they carry.
	{http.MethodPost, "/explain", protectedExplain},
	{http.MethodPost, "/query", protectedStatements},
	{http.MethodPost, "/script", protectedStatements},
	{http.MethodPost, "/aggregate", protectedPipeline},
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
	var readOnly bool
	err := s.Store.DB.QueryRowContext(r.Context(),
		`SELECT read_only FROM db_connections WHERE id = ?`, id).Scan(&readOnly)
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
	return rule.check(body)
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

// protectedStatementFields are the body fields that carry SQL, in every shape
// the statement routes take it: one statement, or a list of them.
var protectedStatementFields = map[string]bool{
	"query": true, "sql": true, "script": true, "statements": true, "queries": true,
}

// protectedTextOptions are body fields that are text and are not SQL.
var protectedTextOptions = map[string]bool{
	"queryId": true, "format": true, "schema": true, "database": true, "name": true,
}

// protectedStatements allows a body whose every statement classifies as a
// read.
//
// It fails closed on what it does not recognise. A field it has never heard
// of that holds text, a list or an object could be a statement under a name
// added after this was written, and letting it through unread would be the
// hole this file exists not to have; only numbers and booleans pass as
// options without being named above.
func protectedStatements(body []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return protectedRefusal("this connection is protected, and the request could not be read to see whether it changes anything")
	}
	var statements []string
	for name, raw := range fields {
		switch {
		case protectedStatementFields[name]:
			found, err := statementTexts(raw)
			if err != nil {
				return protectedRefusal("this connection is protected, and %q could not be read as SQL to see whether it changes anything", name)
			}
			statements = append(statements, found...)
		case protectedTextOptions[name]:
		default:
			if first := bytes.TrimSpace(raw); len(first) > 0 && (first[0] == '"' || first[0] == '[' || first[0] == '{') {
				return protectedRefusal("this connection is protected, and %q is not a field this check can read to see whether it changes anything", name)
			}
		}
	}
	if len(statements) == 0 {
		return protectedRefusal("this connection is protected, and the request carries no statement to check")
	}
	for _, statement := range statements {
		if risk := dbx.Classify(statement); risk.Level != "read" {
			return protectedRefusal("this connection is protected, and that statement is not a read: %s", strings.Join(risk.Reasons, "; "))
		}
	}
	return nil
}

// statementTexts reads SQL out of a field: a string, or a list whose entries
// are strings or objects naming their statement "sql" or "query".
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
		var named map[string]json.RawMessage
		if err := json.Unmarshal(entry, &named); err != nil {
			return nil, err
		}
		found := false
		for _, key := range []string{"sql", "query"} {
			if value, ok := named[key]; ok {
				if err := json.Unmarshal(value, &text); err != nil {
					return nil, err
				}
				out, found = append(out, text), true
			}
		}
		if !found {
			return nil, fmt.Errorf("an entry names no statement")
		}
	}
	return out, nil
}

// protectedExplain allows a plan that is only planned. Asking for the plan
// with the statement actually run is running the statement, and is held to
// what running it would be.
func protectedExplain(body []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return protectedRefusal("this connection is protected, and the request could not be read to see whether it changes anything")
	}
	switch strings.TrimSpace(string(fields["analyze"])) {
	case "", "false", "null":
		return nil
	}
	return protectedStatements(body)
}

// protectedPipeline allows an aggregation with no stage that writes.
//
// The pipeline is parsed and its keys compared after decoding, at every
// depth. Looking for the stage names in the request's text would miss one
// spelled with an escape, which the server decodes and this would not.
func protectedPipeline(body []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || len(fields["pipeline"]) == 0 {
		return protectedRefusal("this connection is protected, and the request carries no pipeline to check")
	}
	raw := fields["pipeline"]
	// The pipeline travels as a JSON document inside a string.
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		raw = json.RawMessage(text)
	}
	var pipeline any
	if err := json.Unmarshal(raw, &pipeline); err != nil {
		return protectedRefusal("this connection is protected, and the pipeline could not be read to see whether it writes")
	}
	if stage := writingStage(pipeline); stage != "" {
		return protectedRefusal("this connection is protected, and a pipeline with a %s stage writes to a collection", stage)
	}
	return nil
}

// writingStage is the first stage name in a decoded pipeline that writes.
func writingStage(v any) string {
	switch x := v.(type) {
	case map[string]any:
		for key, value := range x {
			if strings.EqualFold(key, "$out") || strings.EqualFold(key, "$merge") {
				return strings.ToLower(key)
			}
			if stage := writingStage(value); stage != "" {
				return stage
			}
		}
	case []any:
		for _, value := range x {
			if stage := writingStage(value); stage != "" {
				return stage
			}
		}
	}
	return ""
}
