package assistant

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// How a deployment's or the server's assistant reads the calls an answer ends
// on. A model writes a call the way it writes anything: a number past its bound,
// a field the tool does not have, an object sent as the text of one, a page that
// is not in the list. None of that refuses the answer. Each call is read on its
// own: what can be read sensibly is (a number is held to its bound, a field
// nobody reads is left out), and the model is told so in the call's result;
// a call that cannot run at all is answered as a failed call that says what was
// wrong and what the tool takes, so the model corrects it on its next step. Only
// an answer that cannot be read at all — a call with no id, or two under one —
// is refused whole, because a call is answered by its id.

// ErrUnreadableAnswer is an answer whose calls cannot be told apart. Nothing of
// it runs.
var ErrUnreadableAnswer = errors.New("the assistant's answer could not be read: it named a call without an id, or two calls under one id")

// maxCallID bounds a call's id, which is echoed back in its result.
const maxCallID = 256

// CheckCalls is the check a conversation offered these tools reads its calls
// through.
func CheckCalls(offer []Tool) func([]Block) ([]ToolCall, error) {
	names := make([]string, 0, len(offer))
	pages := []string{}
	for _, t := range offer {
		names = append(names, t.Name)
		if t.Name == ToolOpenPage {
			pages = schemaEnum(t.Schema, "page")
		}
	}
	return func(blocks []Block) ([]ToolCall, error) {
		var calls []ToolCall
		seen := map[string]bool{}
		for _, b := range blocks {
			if b.Type != BlockToolUse {
				continue
			}
			// A result answers a call by its id, so a call with none, or two with
			// one, would be answered wrongly or twice on one Allow.
			if b.ToolID == "" || len(b.ToolID) > maxCallID || seen[b.ToolID] {
				return nil, ErrUnreadableAnswer
			}
			seen[b.ToolID] = true
			calls = append(calls, readCall(b, names, pages))
		}
		return calls, nil
	}
}

// AgentCalls reads the calls of a deployment's conversation.
var AgentCalls = CheckCalls(AgentTools)

// readCall reads one call. It never fails: a call that cannot run carries the
// Problem it is answered with.
func readCall(b Block, offered, pages []string) ToolCall {
	call := ToolCall{ID: b.ToolID, Name: b.ToolName}
	if !slices.Contains(offered, b.ToolName) {
		call.Problem = fmt.Sprintf("There is no tool called %q here. Your tools are: %s.", b.ToolName, strings.Join(offered, ", "))
		call.Input = json.RawMessage(`{}`)
		return call
	}
	in, err := readFields(b)
	if err != nil {
		call.Problem = fmt.Sprintf("%s: %v. %s", b.ToolName, err, takes(b.ToolName))
		call.Input = json.RawMessage(`{}`)
		return call
	}
	var value any
	switch b.ToolName {
	case ToolDescribeOperation, ToolCallOperation, ToolWaitForRun, ToolOpenPage:
		value = readAgentInput(b.ToolName, in, pages)
	case ToolFindRoutes:
		value = readFindInput(in)
	case ToolAPIRequest:
		value = readRequestInput(in)
	default:
		value = readOperatorInput(b.ToolName, in)
	}
	if in.problem != "" {
		call.Problem = b.ToolName + ": " + in.problem + " " + takes(b.ToolName)
	}
	if unknown := in.unknown(); len(unknown) > 0 && call.Problem == "" {
		in.note(fmt.Sprintf("%s is not one of %s's fields and was ignored", quoteList(unknown), b.ToolName))
	} else if len(unknown) > 0 {
		call.Problem += fmt.Sprintf(" (%s is not one of its fields.)", quoteList(unknown))
	}
	call.Notes = in.notes
	raw, _ := json.Marshal(value)
	call.Input = raw
	return call
}

// takes says what a tool takes, for a call that got it wrong.
func takes(tool string) string {
	what := map[string]string{
		ToolRecentRuns:        fmt.Sprintf("It takes limit (1–%d).", OperatorRunsMax),
		ToolRunTranscript:     fmt.Sprintf("It takes run (a run's number, as recent_runs gives it) and lines (1–%d).", OperatorLinesMax),
		ToolContainerLogs:     fmt.Sprintf("It takes lines (1–%d) and service (a Compose service's name).", OperatorLinesMax),
		ToolReleaseMetrics:    "It takes nothing.",
		ToolLifecycleEvents:   fmt.Sprintf("It takes hours (1–%d).", OperatorHoursMax),
		ToolHostAlerts:        "It takes nothing.",
		ToolFirewallRules:     "It takes nothing.",
		ToolSecurityUpdates:   "It takes nothing.",
		ToolReadSource:        fmt.Sprintf("It takes paths (a list of at most %d files or folders in the repository, such as README.md or src) and deployment (a deployment's id, for one other than this conversation's).", SourcePathsMax),
		ToolDescribeOperation: "It takes name (an operation's name, as your list gives it).",
		ToolCallOperation:     "It takes name (an operation's name, as your list gives it), arguments (an object: path and query parameters by name, the JSON body under body) and reason.",
		ToolWaitForRun:        fmt.Sprintf("It takes run (a run's number) and seconds (1–%d).", AgentWaitMax),
		ToolOpenPage:          "It takes page (one of the pages its schema lists) and note.",
		ToolFindRoutes:        "It takes query (words to look for in the dashboard's routes, or empty for its areas) and optionally method.",
		ToolAPIRequest:        "It takes method (GET, POST, PUT, PATCH or DELETE), path (under /api/v1, such as /databases), query (an object), body (JSON) and reason.",
	}
	return what[tool]
}

// fields is a call's input as it was sent, read a field at a time.
type fields struct {
	values  map[string]json.RawMessage
	used    map[string]bool
	notes   []string
	problem string
}

// readFields reads a call's input: an object, or the text of one, which some
// models write for a tool's whole input.
func readFields(b Block) (*fields, error) {
	raw := bytes.TrimSpace(b.Input)
	if len(raw) == 0 || string(raw) == "null" {
		raw = []byte(`{}`)
	}
	// Anthropic's stream gave input that is not JSON; its text is kept beside it.
	if b.Text != "" && string(raw) == `{}` {
		return nil, fmt.Errorf("its input was not JSON (%s)", clipLine(b.Text, 160))
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("its input was not JSON (%s)", clipLine(string(raw), 160))
	}
	if object, ok := objectOf(raw); ok {
		raw = object
	}
	values := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &values); err != nil || values == nil {
		return nil, errors.New("its input has to be a JSON object of its fields")
	}
	return &fields{values: values, used: map[string]bool{}}, nil
}

// objectOf reads a JSON object, or a JSON string whose text is one.
func objectOf(raw json.RawMessage) (json.RawMessage, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '{' {
		return raw, json.Valid(raw)
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return nil, false
	}
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "{") || !json.Valid([]byte(text)) {
		return nil, false
	}
	return json.RawMessage(text), true
}

func (f *fields) note(text string) { f.notes = append(f.notes, text) }

// fail records why the call cannot run; the first reason is the one told.
func (f *fields) fail(text string) {
	if f.problem == "" {
		f.problem = text
	}
}

// take finds the first of the names given, the tool's own name for a field
// first and the names models use for it after.
func (f *fields) take(names ...string) (json.RawMessage, string) {
	for _, name := range names {
		if v, ok := f.values[name]; ok {
			f.used[name] = true
			if trimmed := bytes.TrimSpace(v); string(trimmed) == "null" {
				continue
			}
			return v, name
		}
	}
	return nil, ""
}

func (f *fields) unknown() []string {
	var out []string
	for name := range f.values {
		if !f.used[name] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// text reads a string field. A number or a boolean is read as its text.
func (f *fields) text(names ...string) string {
	raw, _ := f.take(names...)
	if raw == nil {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed != "" && trimmed[0] != '{' && trimmed[0] != '[' {
		return trimmed
	}
	f.note(fmt.Sprintf("%s has to be text and was ignored", names[0]))
	return ""
}

// whole reads a whole-number field: a number, rounded if it has a fraction,
// or its text ("12", "#12"). Absent is 0.
func (f *fields) whole(names ...string) (int64, bool) {
	raw, name := f.take(names...)
	if raw == nil {
		return 0, false
	}
	var n float64
	if json.Unmarshal(raw, &n) != nil {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			f.fail(fmt.Sprintf("%s has to be a whole number, and %s is not one.", name, clipLine(string(raw), 40)))
			return 0, true
		}
		s = strings.TrimPrefix(strings.TrimSpace(s), "#")
		if s == "" {
			return 0, false
		}
		parsed, err := strconv.ParseFloat(s, 64)
		if err != nil {
			f.fail(fmt.Sprintf("%s has to be a whole number, and %q is not one.", name, clipLine(s, 40)))
			return 0, true
		}
		n = parsed
	}
	if math.IsNaN(n) || math.IsInf(n, 0) || math.Abs(n) > 1e15 {
		f.fail(fmt.Sprintf("%s is out of any range.", name))
		return 0, true
	}
	return int64(math.Round(n)), true
}

// bounded reads a whole number held to low..high: past the top it is the top,
// and below the bottom, or absent, it is the default. Either is said.
func (f *fields) bounded(name string, low, high, fallback int64, aliases ...string) int64 {
	n, given := f.whole(append([]string{name}, aliases...)...)
	switch {
	case !given || n == 0:
		return fallback
	case n > high:
		f.note(fmt.Sprintf("%s was %d; at most %d is read, so %d was", name, n, high, high))
		return high
	case n < low:
		f.note(fmt.Sprintf("%s was %d; it is at least %d, so %d was used", name, n, low, fallback))
		return fallback
	}
	return n
}

// object reads an object field: an object, or the text of one.
func (f *fields) object(names ...string) (json.RawMessage, bool) {
	raw, name := f.take(names...)
	if raw == nil {
		return nil, false
	}
	if object, ok := objectOf(raw); ok {
		return object, true
	}
	var s string
	if json.Unmarshal(raw, &s) == nil && strings.TrimSpace(s) == "" {
		return nil, false
	}
	f.fail(fmt.Sprintf("%s has to be an object, and %s is not one.", name, clipLine(string(raw), 80)))
	return nil, false
}

func clipLine(s string, bound int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= bound {
		return s
	}
	cut := bound
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

func quoteList(names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = "'" + name + "'"
	}
	return strings.Join(quoted, ", ")
}

// schemaEnum reads a property's enum from a tool's schema.
func schemaEnum(schema json.RawMessage, property string) []string {
	var parsed struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	_ = json.Unmarshal(schema, &parsed)
	return parsed.Properties[property].Enum
}

func readOperatorInput(tool string, f *fields) OperatorInput {
	var in OperatorInput
	switch tool {
	case ToolRecentRuns:
		in.Limit = int(f.bounded("limit", 1, OperatorRunsMax, OperatorRunsDefault, "count", "n"))
	case ToolRunTranscript, ToolContainerLogs:
		in.Lines = int(f.bounded("lines", 1, OperatorLinesMax, OperatorLinesDefault, "tail", "limit"))
		if tool == ToolRunTranscript {
			run, _ := f.whole("run", "run_number", "runNumber", "number")
			if run <= 0 {
				f.fail("run is required: the run's number, as recent_runs gives it (#12 is 12).")
			}
			in.Run = run
			break
		}
		in.Service = f.text("service", "container")
		if len(in.Service) > operatorServiceMax || strings.ContainsAny(in.Service, "\x00\r\n/") {
			f.fail(fmt.Sprintf("service is a Compose service's name, and %q is not one.", clipLine(in.Service, 60)))
			in.Service = ""
		}
	case ToolLifecycleEvents:
		in.Hours = int(f.bounded("hours", 1, OperatorHoursMax, OperatorHoursDefault, "since_hours"))
	case ToolReadSource:
		in.Paths = f.list(SourcePathsMax, sourcePathMax, "paths", "path", "files", "file", "folders", "folder")
		if id, given := f.whole("deployment", "deployment_id", "deploymentId", "project", "project_id", "id"); given {
			if id <= 0 {
				f.fail("deployment is a deployment's id, a whole number above 0.")
			}
			in.Deployment = id
		}
	}
	return in
}

// list reads a field of text values: a list of them, the text of a list, or one
// value written on its own. Past bound values, the rest are left out and said.
func (f *fields) list(bound, each int, names ...string) []string {
	raw, name := f.take(names...)
	if raw == nil {
		return nil
	}
	var values []any
	if json.Unmarshal(raw, &values) != nil {
		var one string
		if json.Unmarshal(raw, &one) != nil {
			f.fail(fmt.Sprintf("%s has to be a list of text, and %s is not one.", name, clipLine(string(raw), 80)))
			return nil
		}
		if trimmed := strings.TrimSpace(one); !strings.HasPrefix(trimmed, "[") || json.Unmarshal([]byte(trimmed), &values) != nil {
			values = []any{one}
		}
	}
	out := []string{}
	for _, v := range values {
		text, ok := v.(string)
		text = strings.TrimSpace(text)
		switch {
		case !ok:
			f.note(fmt.Sprintf("%s holds %s, which is not text and was ignored", name, clipLine(fmt.Sprint(v), 40)))
		case text == "":
		case len(text) > each || strings.ContainsRune(text, 0):
			f.fail(fmt.Sprintf("%s holds %q, which is not a path.", name, clipLine(text, 60)))
		default:
			out = append(out, text)
		}
	}
	if len(out) > bound {
		f.note(fmt.Sprintf("%s held %d values; the first %d were read", name, len(out), bound))
		out = out[:bound]
	}
	return out
}

func readAgentInput(tool string, f *fields, pages []string) AgentInput {
	var in AgentInput
	switch tool {
	case ToolDescribeOperation:
		in.Name = f.text("name", "operation", "operation_name", "operationName", "op")
		if in.Name == "" {
			f.fail("name is required.")
		}
		f.take("reason")
	case ToolCallOperation:
		in.Name = f.text("name", "operation", "operation_name", "operationName", "op")
		if in.Name == "" {
			f.fail("name is required.")
		}
		in.Reason = clip(f.text("reason", "why"), operatorReasonMax)
		arguments, given := f.object("arguments", "args", "parameters", "params", "input")
		// What a model put beside arguments rather than in them — the id, the
		// body — is read as one of them, unless arguments says otherwise.
		args := map[string]json.RawMessage{}
		if given {
			_ = json.Unmarshal(arguments, &args)
		}
		moved := []string{}
		for _, name := range f.unknown() {
			if _, taken := args[name]; !taken {
				args[name] = f.values[name]
				moved = append(moved, name)
			}
			f.used[name] = true
		}
		if len(moved) > 0 {
			f.note(fmt.Sprintf("%s was read as one of the operation's arguments", quoteList(moved)))
		}
		if len(args) > 0 {
			in.Arguments, _ = json.Marshal(args)
		}
	case ToolWaitForRun:
		run, _ := f.whole("run", "run_number", "runNumber", "number")
		if run <= 0 {
			f.fail("run is required: the run's number (#12 is 12).")
		}
		in.Run = run
		in.Seconds = int(f.bounded("seconds", 1, AgentWaitMax, AgentWaitDefault, "timeout", "wait"))
	case ToolOpenPage:
		page := strings.ToLower(strings.Trim(f.text("page", "name", "section"), "/ "))
		page = strings.NewReplacer("-", "_", " ", "_", "settings/", "").Replace(page)
		if !slices.Contains(pages, page) {
			f.fail(fmt.Sprintf("there is no page %q here; the pages are %s.", page, strings.Join(pages, ", ")))
		}
		in.Page = page
		in.Note = clip(f.text("note", "reason", "message"), agentNoteMax)
	}
	if strings.ContainsRune(in.Reason+in.Note+in.Name, 0) {
		f.fail("its text holds a NUL.")
	}
	return in
}

// FindInput is what find_routes takes.
type FindInput struct {
	Query  string `json:"query"`
	Method string `json:"method,omitempty"`
}

func readFindInput(f *fields) FindInput {
	in := FindInput{Query: clip(f.text("query", "q", "search", "area", "keyword", "keywords", "topic"), 200)}
	in.Method = strings.ToUpper(f.text("method"))
	if in.Method != "" && !slices.Contains(requestMethods, in.Method) {
		f.note(fmt.Sprintf("method %q is not one the API has and was ignored", in.Method))
		in.Method = ""
	}
	return in
}

// FindInput reads a find_routes call's checked input back.
func (c ToolCall) FindInput() FindInput {
	var in FindInput
	_ = json.Unmarshal(c.Input, &in)
	return in
}

var requestMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}

// RequestInput is what api_request takes, as its check read it: the path is
// under /api/v1 and has no query, which is in Query.
type RequestInput struct {
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Query  url.Values      `json:"query,omitempty"`
	Body   json.RawMessage `json:"body,omitempty"`
	Reason string          `json:"reason,omitempty"`
}

// RequestInput reads an api_request call's checked input back.
func (c ToolCall) RequestInput() RequestInput {
	var in RequestInput
	_ = json.Unmarshal(c.Input, &in)
	return in
}

const maxRequestBody = 1 << 20

func readRequestInput(f *fields) RequestInput {
	var in RequestInput
	in.Reason = clip(f.text("reason", "why"), operatorReasonMax)
	body, hasBody := f.take("body", "json", "data", "payload")
	if hasBody != "" {
		body = bytes.TrimSpace(body)
		// A body sent as the text of JSON is the JSON.
		var s string
		if json.Unmarshal(body, &s) == nil {
			if trimmed := strings.TrimSpace(s); (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) && json.Valid([]byte(trimmed)) {
				body = json.RawMessage(trimmed)
			}
		}
		if len(body) > maxRequestBody {
			f.fail(fmt.Sprintf("body is %d bytes, and at most %d are sent.", len(body), maxRequestBody))
			body = nil
		}
		in.Body = body
	}
	in.Method = strings.ToUpper(f.text("method", "verb", "http_method"))
	switch {
	case in.Method == "" && in.Body == nil:
		in.Method = "GET"
	case in.Method == "":
		f.fail("method is required when a body is sent: POST, PUT, PATCH or DELETE.")
	case !slices.Contains(requestMethods, in.Method):
		f.fail(fmt.Sprintf("method %q is not one the API takes: GET, POST, PUT, PATCH or DELETE.", in.Method))
	}
	if in.Method == "GET" && in.Body != nil {
		f.note("a GET sends no body, so body was left out")
		in.Body = nil
	}
	path := f.text("path", "url", "route", "endpoint")
	query := url.Values{}
	if path != "" {
		// A whole address is read for its path: the dashboard is the only server.
		if parsed, err := url.Parse(path); err == nil && parsed.Scheme != "" {
			path = parsed.RequestURI()
		}
		if head, rest, found := strings.Cut(path, "?"); found {
			path = head
			if values, err := url.ParseQuery(rest); err == nil {
				query = values
			}
		}
		path = "/" + strings.TrimLeft(path, "/")
		path = strings.TrimPrefix(path, "/api/v1")
		if path == "" {
			path = "/"
		}
	}
	switch {
	case path == "":
		f.fail("path is required: a route under /api/v1, such as /databases (find_routes lists them).")
	case strings.ContainsAny(path, "\x00\r\n\\") || strings.Contains(path, "/../") || strings.HasSuffix(path, "/..") || !CanonicalPath(path):
		f.fail(fmt.Sprintf("path %q is not a route's path.", clipLine(path, 120)))
	}
	in.Path = path
	if raw, name := f.take("query", "params", "query_params", "queryParams", "search"); raw != nil {
		values, ok := queryValues(raw)
		if !ok {
			f.fail(fmt.Sprintf("%s has to be an object of names and values, or text like a=1&b=2.", name))
		}
		for key, list := range values {
			query[key] = append(query[key], list...)
		}
	}
	if len(query) > 0 {
		in.Query = query
	}
	return in
}

// CanonicalPath reports whether a path reads the same to the router as it is
// written: no fragment, no query, no host, and escaped as it would be sent. The
// route a call is classified by is looked up from the path as written, and the
// request is routed from the path as parsed, so the two have to be one.
func CanonicalPath(path string) bool {
	u, err := url.Parse(path)
	return err == nil && u.Scheme == "" && u.Host == "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" &&
		!u.ForceQuery && strings.HasPrefix(path, "/") && u.EscapedPath() == path
}

// queryValues reads a query given as an object, or as its text.
func queryValues(raw json.RawMessage) (url.Values, bool) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		values, err := url.ParseQuery(strings.TrimPrefix(strings.TrimSpace(text), "?"))
		return values, err == nil
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return nil, false
	}
	values := url.Values{}
	for key, value := range object {
		var list []json.RawMessage
		if json.Unmarshal(value, &list) != nil {
			list = []json.RawMessage{value}
		}
		for _, item := range list {
			var s string
			if json.Unmarshal(item, &s) == nil {
				values.Add(key, s)
				continue
			}
			trimmed := strings.TrimSpace(string(item))
			if trimmed == "null" {
				continue
			}
			if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
				return nil, false
			}
			values.Add(key, trimmed)
		}
	}
	return values, true
}

// Notes is what was read differently than it was asked, said before a call's
// result so the model knows what it got.
func (c ToolCall) NotesText() string {
	if len(c.Notes) == 0 {
		return ""
	}
	return "Note: " + strings.Join(c.Notes, "; ") + ".\n"
}
