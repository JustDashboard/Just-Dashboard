// Package mcp is the Model Context Protocol as this dashboard serves it to AI
// clients: one endpoint over Streamable HTTP, every request answered with one
// JSON object, nothing kept between requests.
//
// Two eras of the protocol arrive at that endpoint. A client of the 2025
// revisions opens with an `initialize` handshake and sends its version in the
// MCP-Protocol-Version header afterwards; a client of 2026-07-28 sends no
// handshake and carries its version in every request's `_meta`, mirrored into
// headers that have to agree with the body. A server that answers both is what
// the specification calls dual-era, and it costs little here: the endpoint
// keeps no session in either, so a legacy session is only the version the
// handshake settled on, which the client repeats on every request.
//
// What is in this package is the protocol: reading a message, deciding which
// era and version it speaks, refusing what is malformed with the status and
// code the specification gives, and writing the answer. What a tool does, and
// who may call it, is the API's (api/handlers_mcp.go).
package mcp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/version"
)

// Modern is the stateless revision: every request carries its own version.
const Modern = "2026-07-28"

// Legacy are the revisions that open with `initialize`, newest first. The
// first is what a handshake asking for an unknown version is answered with.
var Legacy = []string{"2025-11-25", "2025-06-18", "2025-03-26"}

// oldestHeaderless is what a legacy request without MCP-Protocol-Version is
// taken to speak: the header arrived in 2025-06-18, and the transport lets a
// server read its absence as the revision before.
const oldestHeaderless = "2025-03-26"

// Supported is every version the endpoint speaks, newest first.
func Supported() []string { return append([]string{Modern}, Legacy...) }

// JSON-RPC's codes and the ones MCP defines for itself.
const (
	CodeParseError         = -32700
	CodeInvalidRequest     = -32600
	CodeMethodNotFound     = -32601
	CodeInvalidParams      = -32602
	CodeInternalError      = -32603
	CodeHeaderMismatch     = -32020
	CodeUnsupportedVersion = -32022
)

// The keys of a request's `_meta` the modern revision reserves.
const (
	metaVersion      = "io.modelcontextprotocol/protocolVersion"
	metaClient       = "io.modelcontextprotocol/clientInfo"
	metaCapabilities = "io.modelcontextprotocol/clientCapabilities"
	metaServer       = "io.modelcontextprotocol/serverInfo"
)

// Error is a refusal as JSON-RPC carries it, with the HTTP status the
// transport gives it.
type Error struct {
	Status  int    `json:"-"`
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *Error) Error() string { return e.Message }

func badRequest(code int, format string, args ...any) *Error {
	return &Error{Status: http.StatusBadRequest, Code: code, Message: fmt.Sprintf(format, args...)}
}

// Implementation is a client's or a server's name for itself.
type Implementation struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version"`
}

// Server is how the endpoint names itself.
func Server() Implementation {
	return Implementation{Name: "just-dashboard", Title: "Just Dashboard", Version: version.Version}
}

// Request is one POST body, read.
type Request struct {
	ID     json.RawMessage
	Method string
	// Name and Arguments are a tools/call's.
	Name      string
	Arguments json.RawMessage
	// Client is who sent it, by its own account: `initialize`'s clientInfo
	// or the modern `_meta`'s. It is for the audit entry, never a decision.
	Client Implementation
	// Notification is a message with no id, and Reply a client's answer to
	// a request; neither is answered with more than 202.
	Notification bool
	Reply        bool

	metaVersion   string
	hasMeta       bool
	hasClientCaps bool
	initVersion   string
}

type wireMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	Result  json.RawMessage `json:"result"`
	Error   json.RawMessage `json:"error"`
}

type wireParams struct {
	Meta            map[string]json.RawMessage `json:"_meta"`
	Name            string                     `json:"name"`
	Arguments       json.RawMessage            `json:"arguments"`
	ProtocolVersion string                     `json:"protocolVersion"`
	ClientInfo      *Implementation            `json:"clientInfo"`
}

// Parse reads one message. A batch is refused: the transport carries one
// message per POST, and the 2025-06-18 revision took batching out.
func Parse(body []byte) (*Request, *Error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, badRequest(CodeInvalidRequest, "the request has no body")
	}
	if trimmed[0] == '[' {
		return nil, badRequest(CodeInvalidRequest, "send one JSON-RPC message per request; batches are not accepted")
	}
	var msg wireMessage
	if err := json.Unmarshal(trimmed, &msg); err != nil {
		return nil, badRequest(CodeParseError, "the body is not a JSON-RPC message: %v", err)
	}
	if msg.JSONRPC != "2.0" {
		return nil, badRequest(CodeInvalidRequest, `the message must say "jsonrpc": "2.0"`)
	}
	req := &Request{Method: msg.Method}
	if msg.Method == "" {
		if msg.Result == nil && msg.Error == nil {
			return nil, badRequest(CodeInvalidRequest, "the message has no method")
		}
		req.Reply = true
		return req, nil
	}
	if len(msg.ID) == 0 {
		req.Notification = true
	} else if !validID(msg.ID) {
		return nil, badRequest(CodeInvalidRequest, "a request id is a string or an integer, never null")
	} else {
		req.ID = msg.ID
	}
	if len(msg.Params) == 0 || string(msg.Params) == "null" {
		return req, nil
	}
	var p wireParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		return nil, &Error{Status: http.StatusBadRequest, Code: CodeInvalidParams, Message: "params must be an object: " + err.Error()}
	}
	req.Name, req.Arguments = p.Name, p.Arguments
	req.initVersion = p.ProtocolVersion
	if p.ClientInfo != nil {
		req.Client = *p.ClientInfo
	}
	if p.Meta != nil {
		if raw, ok := p.Meta[metaVersion]; ok {
			req.hasMeta = true
			if err := json.Unmarshal(raw, &req.metaVersion); err != nil || req.metaVersion == "" {
				return nil, badRequest(CodeInvalidParams, "_meta's %s must be a version string", metaVersion)
			}
		}
		if raw, ok := p.Meta[metaCapabilities]; ok && len(bytes.TrimSpace(raw)) > 0 && raw[0] == '{' {
			req.hasClientCaps = true
		}
		if raw, ok := p.Meta[metaClient]; ok {
			_ = json.Unmarshal(raw, &req.Client)
		}
	}
	return req, nil
}

func validID(raw json.RawMessage) bool {
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&v) != nil {
		return false
	}
	switch t := v.(type) {
	case string:
		return true
	case json.Number:
		_, err := t.Int64()
		return err == nil
	}
	return false
}

// Call is a request with the version it speaks settled.
type Call struct {
	*Request
	Version string
	// Modern is a 2026-07-28 request, whose results carry resultType and
	// caching hints a legacy client has no use for.
	Modern bool
}

// Settle decides which version a request speaks, from its body and the
// headers that came with it, and refuses one whose headers and body disagree.
//
// A request whose `_meta` names a version is modern, and is held to the
// headers the modern transport mirrors from its body. `initialize` is the
// legacy handshake, answered with the version asked for when it is one of
// ours and the newest legacy one when it is not. Anything else is a request
// in the session a handshake opened, which on this endpoint is no more than
// the MCP-Protocol-Version header it carries.
func Settle(req *Request, h http.Header) (*Call, *Error) {
	if req.hasMeta {
		if req.metaVersion != Modern {
			return nil, unsupported(req.metaVersion)
		}
		if got := h.Get("MCP-Protocol-Version"); got != req.metaVersion {
			return nil, mismatch("MCP-Protocol-Version", got, req.metaVersion)
		}
		if got := h.Get("Mcp-Method"); got != req.Method {
			return nil, mismatch("Mcp-Method", got, req.Method)
		}
		if req.Method == "tools/call" {
			got, ok := headerValue(h.Get("Mcp-Name"))
			if !ok || got != req.Name {
				return nil, mismatch("Mcp-Name", h.Get("Mcp-Name"), req.Name)
			}
		}
		if !req.hasClientCaps {
			return nil, badRequest(CodeInvalidParams, "_meta has no %s", metaCapabilities)
		}
		return &Call{Request: req, Version: Modern, Modern: true}, nil
	}
	if req.Method == "initialize" {
		version := Legacy[0]
		if slices.Contains(Legacy, req.initVersion) {
			version = req.initVersion
		}
		return &Call{Request: req, Version: version}, nil
	}
	switch got := h.Get("MCP-Protocol-Version"); {
	case got == "":
		return &Call{Request: req, Version: oldestHeaderless}, nil
	case slices.Contains(Legacy, got):
		return &Call{Request: req, Version: got}, nil
	case got == Modern:
		return nil, badRequest(CodeInvalidParams, "a %s request carries %s in its _meta", Modern, metaVersion)
	default:
		return nil, unsupported(got)
	}
}

func unsupported(requested string) *Error {
	return &Error{
		Status: http.StatusBadRequest, Code: CodeUnsupportedVersion, Message: "Unsupported protocol version",
		Data: map[string]any{"supported": Supported(), "requested": requested},
	}
}

func mismatch(header, got, body string) *Error {
	if got == "" {
		return badRequest(CodeHeaderMismatch, "Header mismatch: the %s header is missing", header)
	}
	return badRequest(CodeHeaderMismatch, "Header mismatch: %s header value %q does not match body value %q", header, got, body)
}

// headerValue reads a header that may carry its value Base64-encoded, as
// =?base64?…?=, which is how the transport sends a name a header cannot hold.
func headerValue(v string) (string, bool) {
	inner, ok := strings.CutPrefix(v, "=?base64?")
	if !ok {
		return v, v != ""
	}
	inner, ok = strings.CutSuffix(inner, "?=")
	if !ok {
		return "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(inner)
	return string(decoded), err == nil
}

// MethodNotFound refuses a method the endpoint does not have. A modern
// request is told so with a 404, which is how its client tells a modern
// server that lacks the method from an endpoint that is not one at all.
func (c *Call) MethodNotFound() *Error {
	status := http.StatusOK
	if c.Modern {
		status = http.StatusNotFound
	}
	return &Error{Status: status, Code: CodeMethodNotFound, Message: "Method not found: " + c.Method}
}

// Complete is a result as the call's era expects it: a modern result says it
// is complete and names the server; a legacy one is left as it is.
func (c *Call) Complete(result map[string]any) map[string]any {
	if c.Modern {
		result["resultType"] = "complete"
		result["_meta"] = map[string]any{metaServer: Server()}
	}
	return result
}

// Cached adds the caching hints a modern list or discovery result must carry.
// Every list here depends on the key that asked, so none may be shared.
func (c *Call) Cached(result map[string]any, ttlMs int) map[string]any {
	if c.Modern {
		result["ttlMs"] = ttlMs
		result["cacheScope"] = "private"
	}
	return c.Complete(result)
}

// Initialize is the legacy handshake's answer.
func (c *Call) Initialize(instructions string) map[string]any {
	return map[string]any{
		"protocolVersion": c.Version,
		"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
		"serverInfo":      Server(),
		"instructions":    instructions,
	}
}

// Discover is server/discover's answer, which every modern server keeps.
func (c *Call) Discover(instructions string) map[string]any {
	return c.Cached(map[string]any{
		"supportedVersions": Supported(),
		"capabilities":      map[string]any{"tools": map[string]any{}},
		"instructions":      instructions,
	}, 300_000)
}

// Response is the body answering a request.
func Response(id json.RawMessage, result any) []byte {
	b, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	if err != nil {
		return ErrorResponse(id, &Error{Code: CodeInternalError, Message: "the answer could not be written"})
	}
	return b
}

// ErrorResponse is the body refusing one. An id the request did not get as
// far as giving is left out.
func ErrorResponse(id json.RawMessage, e *Error) []byte {
	body := map[string]any{"jsonrpc": "2.0", "error": e}
	if len(id) > 0 {
		body["id"] = id
	}
	b, _ := json.Marshal(body)
	return b
}
