package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The tools generated from the deployment API: one per operation a deploy key
// may call, named for its operationId, taking the operation's path and query
// parameters and its JSON body. Which operations they are is the API's to
// decide (it reads them off its router and its deploy key table); here is
// what a tool is made of, which key is offered it, how a call's arguments
// become a request, and how the answer becomes a result.

// Grant is what a generated tool asks of a key's deployment grant.
type Grant int

const (
	// GrantRead is a read: a key that may read the deployments.
	GrantRead Grant = iota
	// GrantWrite changes something: a key that may deploy, whose owner's
	// role may control services.
	GrantWrite
	// GrantDestructive is a route behind the destructive capability: a key
	// that may deploy, whose owner's role may also take destructive actions.
	GrantDestructive
)

func (g Grant) String() string {
	switch g {
	case GrantWrite:
		return "write"
	case GrantDestructive:
		return "destructive"
	}
	return "read"
}

// Operation is one API operation offered as a tool.
type Operation struct {
	Tool Tool
	// ID is the operationId the tool is named for.
	ID     string
	Method string
	// Pattern is the route's path under /api/v1, its parameters in braces.
	Pattern string
	// Path is the path parameters in the order the pattern names them, and
	// Query the query parameters, each with its JSON Schema type.
	Path  []Parameter
	Query []Parameter
	// Body is a route that takes a JSON body; BodyRequired one that is sent
	// as {} when the call gives none.
	Body, BodyRequired bool
	Grant              Grant
}

// Parameter is a path or query parameter: its name and JSON Schema type.
type Parameter struct {
	Name, Type string
}

// Reaches reports whether an offer reaches a generated tool: one whose owner
// turned the generated tools on, under the grant the tool asks.
func (o Offer) Reaches(op Operation) bool {
	if !o.Operations {
		return false
	}
	switch op.Grant {
	case GrantRead:
		return o.Deployments
	case GrantWrite:
		return o.Deploy
	case GrantDestructive:
		return o.Rollback
	}
	return false
}

// OperationTools is the generated tools an offer reaches, in the order given.
func OperationTools(o Offer, ops []Operation) []Tool {
	out := make([]Tool, 0, len(ops))
	for _, op := range ops {
		if o.Reaches(op) {
			out = append(out, op.Tool)
		}
	}
	return out
}

// Hints says, for a route's path parameter, where a model finds its value
// ("" for nothing to add).
type Hints func(pattern, name string) string

// NewOperation builds the tool for one operation of an OpenAPI document:
// doc is the whole document (for the schemas a body refers to), item the
// operation's path item, op the operation itself. destructive marks a route
// whose effect a client should confirm (destructiveHint) beyond the ones
// behind the destructive capability, which grant marks.
func NewOperation(doc map[string]any, name, method, pattern string, item, op map[string]any, grant Grant, destructive bool, hints Hints) (Operation, error) {
	out := Operation{Method: method, Pattern: pattern, Grant: grant}
	out.ID, _ = op["operationId"].(string)
	properties := map[string]any{}
	var required []string
	params := append(append([]any{}, list(item["parameters"])...), list(op["parameters"])...)
	for _, raw := range params {
		param, _ := raw.(map[string]any)
		pname, _ := param["name"].(string)
		in, _ := param["in"].(string)
		schema, _ := param["schema"].(map[string]any)
		typ, _ := schema["type"].(string)
		if pname == "" || pname == "body" {
			return out, fmt.Errorf("%s %s: a parameter named %q", method, pattern, pname)
		}
		prop := map[string]any{}
		for k, v := range schema {
			if k != "title" {
				prop[k] = v
			}
		}
		description, _ := param["description"].(string)
		if hint := hintFor(hints, pattern, pname); hint != "" && in == "path" {
			description = strings.TrimSpace(description + " " + hint)
		}
		if description != "" {
			prop["description"] = description
		}
		properties[pname] = prop
		switch in {
		case "path":
			out.Path = append(out.Path, Parameter{pname, typ})
			required = append(required, pname)
		case "query":
			out.Query = append(out.Query, Parameter{pname, typ})
		default:
			return out, fmt.Errorf("%s %s: a parameter in %q", method, pattern, in)
		}
	}
	order := map[string]int{}
	for i, part := range strings.Split(pattern, "/") {
		order[strings.Trim(part, "{}")] = i
	}
	sort.SliceStable(out.Path, func(i, j int) bool { return order[out.Path[i].Name] < order[out.Path[j].Name] })
	sort.SliceStable(required, func(i, j int) bool { return order[required[i]] < order[required[j]] })
	defs := map[string]any{}
	if body, ok := op["requestBody"].(map[string]any); ok {
		content, _ := body["content"].(map[string]any)
		if media, ok := content["application/json"].(map[string]any); ok {
			schema, _ := media["schema"].(map[string]any)
			out.Body = true
			out.BodyRequired, _ = body["required"].(bool)
			bodySchema, err := localSchema(doc, schema, defs)
			if err != nil {
				return out, fmt.Errorf("%s %s: %w", method, pattern, err)
			}
			if bodySchema["description"] == nil {
				bodySchema["description"] = "The request body, as the route takes it."
			}
			properties["body"] = bodySchema
		}
	}
	input := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		input["required"] = required
	}
	if len(defs) > 0 {
		input["$defs"] = defs
	}
	summary, _ := op["summary"].(string)
	note, _ := op["description"].(string)
	access, _ := op["x-deploy-key"].(string)
	description := summary
	if note != "" {
		description += " " + note
	}
	description += " It is " + method + " /api/v1" + pattern + " of the deployment API, called as this key's owner"
	switch grant {
	case GrantRead:
		description += "; it only reads."
	case GrantWrite:
		description += ", and changes what it says."
	case GrantDestructive:
		description += ", and needs a role that may take destructive actions, as the dashboard's own button does."
	}
	if strings.Contains(access, "every deployment") {
		description += " It needs a key that reaches every deployment."
	}
	if out.Body {
		description += " Its JSON body goes under body."
	}
	title := summary
	if i := strings.Index(title, ": "); i > 0 {
		title = title[:i]
	}
	title = strings.TrimSuffix(strings.TrimSpace(title), ".")
	out.Tool = Tool{
		Name:        name,
		Title:       title,
		Description: description,
		InputSchema: input,
		Annotations: Annotations{
			Title:           title,
			ReadOnlyHint:    grant == GrantRead,
			DestructiveHint: grant == GrantDestructive || destructive,
			IdempotentHint:  method == "GET" || method == "PUT" || method == "DELETE",
		},
	}
	return out, nil
}

func hintFor(hints Hints, pattern, name string) string {
	if hints == nil {
		return ""
	}
	return hints(pattern, name)
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

const componentPrefix = "#/components/schemas/"

// localSchema is a schema with every component it refers to copied into
// defs and each reference pointed there, so the tool's input schema stands
// alone, as a client validates it.
func localSchema(doc map[string]any, schema map[string]any, defs map[string]any) (map[string]any, error) {
	components := map[string]any{}
	if c, ok := doc["components"].(map[string]any); ok {
		components, _ = c["schemas"].(map[string]any)
	}
	var copyNode func(node any) (any, error)
	copyNode = func(node any) (any, error) {
		switch value := node.(type) {
		case map[string]any:
			out := make(map[string]any, len(value))
			for k, v := range value {
				if k == "$ref" {
					ref, _ := v.(string)
					if !strings.HasPrefix(ref, componentPrefix) {
						return nil, fmt.Errorf("a $ref outside the components: %q", ref)
					}
					name := strings.TrimPrefix(ref, componentPrefix)
					if _, done := defs[name]; !done {
						component, ok := components[name]
						if !ok {
							return nil, fmt.Errorf("$ref %q names no component", ref)
						}
						defs[name] = nil // reserved before descending: a type may refer to itself
						copied, err := copyNode(component)
						if err != nil {
							return nil, err
						}
						defs[name] = copied
					}
					out[k] = "#/$defs/" + name
					continue
				}
				copied, err := copyNode(v)
				if err != nil {
					return nil, err
				}
				out[k] = copied
			}
			return out, nil
		case []any:
			out := make([]any, len(value))
			for i, v := range value {
				copied, err := copyNode(v)
				if err != nil {
					return nil, err
				}
				out[i] = copied
			}
			return out, nil
		}
		return node, nil
	}
	copied, err := copyNode(schema)
	if err != nil {
		return nil, err
	}
	out, _ := copied.(map[string]any)
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

// RouteRequest is a generated tool's call as the route takes it: its path under
// /api/v1 with the parameters escaped into it, its query and its body.
type RouteRequest struct {
	Path  string
	Query url.Values
	Body  []byte
}

// DecodeRequest reads a generated tool's arguments into the request it makes,
// refusing a name the tool does not take and a path parameter left out, as
// DecodeArguments does for the hand-written tools.
func DecodeRequest(op Operation, raw json.RawMessage) (RouteRequest, error) {
	var req RouteRequest
	if len(bytes.TrimSpace(raw)) == 0 || string(raw) == "null" {
		raw = json.RawMessage(`{}`)
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal(raw, &args); err != nil {
		return req, errors.New("arguments must be an object")
	}
	known := map[string]bool{}
	for _, p := range op.Path {
		known[p.Name] = true
	}
	for _, q := range op.Query {
		known[q.Name] = true
	}
	if op.Body {
		known["body"] = true
	}
	for name := range args {
		if !known[name] {
			return req, fmt.Errorf("%s takes no argument %q", op.Tool.Name, name)
		}
	}
	values := map[string]string{}
	for _, p := range op.Path {
		v, ok := args[p.Name]
		if !ok {
			return req, fmt.Errorf("%s needs %q", op.Tool.Name, p.Name)
		}
		text, err := scalar(v, p.Type)
		if err != nil || text == "" {
			return req, fmt.Errorf("%s: %s must be %s", op.Tool.Name, p.Name, article(p.Type))
		}
		values[p.Name] = text
	}
	var path strings.Builder
	for _, part := range strings.SplitAfter(op.Pattern, "/") {
		name := strings.TrimSuffix(part, "/")
		if strings.HasPrefix(name, "{") && strings.HasSuffix(name, "}") {
			path.WriteString(url.PathEscape(values[strings.Trim(name, "{}")]))
			if strings.HasSuffix(part, "/") {
				path.WriteString("/")
			}
			continue
		}
		path.WriteString(part)
	}
	req.Path = path.String()
	req.Query = url.Values{}
	for _, q := range op.Query {
		v, ok := args[q.Name]
		if !ok || string(v) == "null" {
			continue
		}
		text, err := scalar(v, q.Type)
		if err != nil {
			return req, fmt.Errorf("%s: %s must be %s", op.Tool.Name, q.Name, article(q.Type))
		}
		req.Query.Set(q.Name, text)
	}
	if op.Body {
		body, given := args["body"]
		switch {
		case given && string(body) != "null":
			req.Body = body
		case op.BodyRequired:
			req.Body = []byte(`{}`)
		}
	}
	return req, nil
}

// scalar is a parameter's value as text: a string as it is, a number or a
// boolean as JSON writes it, and for an integer a numeric string too, which
// is how a model often spells an id.
func scalar(v json.RawMessage, typ string) (string, error) {
	var s string
	if json.Unmarshal(v, &s) == nil {
		if typ == "integer" {
			if _, err := strconv.ParseInt(s, 10, 64); err != nil {
				return "", err
			}
		}
		return s, nil
	}
	switch typ {
	case "integer":
		var n int64
		if err := json.Unmarshal(v, &n); err != nil {
			return "", err
		}
		return strconv.FormatInt(n, 10), nil
	case "number":
		var f float64
		if err := json.Unmarshal(v, &f); err != nil {
			return "", err
		}
		return strconv.FormatFloat(f, 'f', -1, 64), nil
	case "boolean":
		var b bool
		if err := json.Unmarshal(v, &b); err != nil {
			return "", err
		}
		return strconv.FormatBool(b), nil
	}
	return "", errors.New("not a string")
}

func article(typ string) string {
	switch typ {
	case "integer":
		return "an integer"
	case "":
		return "a value"
	}
	return "a " + typ
}

// OperationResult is a generated tool's answer: the route's JSON as the
// result, under items when it is not an object (structured content is an
// object), and, past ResultBytes, the first part of its text with a note
// saying how much was left out, since a model's context is the limit.
func OperationResult(status int, body []byte, total int) map[string]any {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return Result(map[string]any{"status": status, "done": true})
	}
	if total > ResultBytes || len(body) > ResultBytes {
		cut := body
		if len(cut) > ResultBytes {
			cut = cut[:ResultBytes]
		}
		for !utf8.Valid(cut) {
			cut = cut[:len(cut)-1]
		}
		return map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": string(cut) + "…"},
				{"type": "text", "text": fmt.Sprintf("The answer is %d bytes; these are its first %d. Ask for less with the tool's parameters.", total, len(cut))},
			},
			"isError": false,
		}
	}
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return Failure("The dashboard's answer was not JSON.")
	}
	if object, ok := value.(map[string]any); ok {
		return Result(object)
	}
	return Result(map[string]any{"items": value})
}
