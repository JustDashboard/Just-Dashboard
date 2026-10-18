package api

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"sync"
	"unicode/utf8"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/mcp"
	"github.com/go-chi/chi/v5"
)

// The MCP endpoint: an AI client's way to the databases its key was granted.
//
// It is POST /api/v1/mcp behind the network allowlist and the rate limit like
// every route, and behind httpx.AuthenticateMCP rather than Authenticate: an
// MCP key opens it and nothing else does, and an MCP key opens nothing else.
// The protocol is internal/mcp's; what is here is who may do what. A tool
// reaches only a connection granted to the key, never a protected one, and
// runs what the key's owner could run through the dashboard's own routes —
// the read surface's reads for anyone, a statement only for a role that may
// run SQL, and a statement that changes data only where the key was granted
// write, judged by authoriseSQL as the query route judges it. Every tool call
// is audited as database.mcp.<tool>, its statement as a shape.

// mcpBodyLimit bounds one message: a statement and its arguments, never a file.
const mcpBodyLimit = 1 << 20

// mcpCallsPerKey bounds the tool calls one key has in flight. An agent that
// sends twenty statements at once is answered for four of them and told to
// wait, rather than holding twenty of the server's connections.
const mcpCallsPerKey = 4

type mcpState struct {
	mu   sync.Mutex
	busy map[int64]int
}

func (st *mcpState) enter(key int64) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.busy == nil {
		st.busy = map[int64]int{}
	}
	if st.busy[key] >= mcpCallsPerKey {
		return false
	}
	st.busy[key]++
	return true
}

func (st *mcpState) leave(key int64) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.busy[key]--; st.busy[key] <= 0 {
		delete(st.busy, key)
	}
}

func (s *Server) mountMCPRoutes(r chi.Router) {
	r.Method(http.MethodPost, "/mcp", s.handle(s.handleMCP))
	// Neither revision gets a stream for the server to speak first on, or a
	// session to end: the endpoint keeps nothing between requests.
	r.Method(http.MethodGet, "/mcp", s.handle(handleMCPNoStream))
	r.Method(http.MethodDelete, "/mcp", s.handle(handleMCPNoStream))
}

// clipTarget bounds a name an MCP client chose — its own, or a database's it
// asked for — before it is an audit entry's target, which has no bound of its
// own.
func clipTarget(name string) string {
	const most = 120
	if utf8.RuneCountInString(name) <= most {
		return name
	}
	return string([]rune(name)[:most]) + "…"
}

func handleMCPNoStream(w http.ResponseWriter, _ *http.Request) error {
	w.Header().Set("Allow", http.MethodPost)
	return httpx.Err(http.StatusMethodNotAllowed, "method_not_allowed", "the MCP endpoint takes one POST per message")
}

func writeMCP(w http.ResponseWriter, status int, body []byte) error {
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, err := w.Write(body)
	return err
}

func mcpRefusal(w http.ResponseWriter, id json.RawMessage, e *mcp.Error) error {
	status := e.Status
	if status == 0 {
		status = http.StatusOK
	}
	return writeMCP(w, status, mcp.ErrorResponse(id, e))
}

func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) error {
	// The transport asks a server to refuse a foreign Origin. That turns away
	// a page on another site; a page that rebinds its own name to this address
	// sends an Origin matching the Host and passes, and is stopped by the key
	// it cannot know, since nothing here reads an ambient credential.
	if !s.WS.CheckOrigin(r) {
		httpx.SetAudit(r, "database.mcp.refused", "", map[string]any{"reason": "origin", "origin": r.Header.Get("Origin")})
		return mcpRefusal(w, nil, &mcp.Error{Status: http.StatusForbidden, Code: mcp.CodeInvalidRequest,
			Message: "a request from a page on another site is refused"})
	}
	if ct, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || ct != "application/json" {
		httpx.SkipAudit(r)
		return mcpRefusal(w, nil, &mcp.Error{Status: http.StatusUnsupportedMediaType, Code: mcp.CodeInvalidRequest,
			Message: "send the message as application/json"})
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, mcpBodyLimit))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpx.SkipAudit(r)
			return mcpRefusal(w, nil, &mcp.Error{Status: http.StatusRequestEntityTooLarge, Code: mcp.CodeInvalidRequest,
				Message: "a message is at most 1 MB"})
		}
		return err
	}
	req, perr := mcp.Parse(body)
	if perr != nil {
		httpx.SkipAudit(r)
		return mcpRefusal(w, nil, perr)
	}
	// A notification asks nothing and a client's reply answers nothing the
	// endpoint asked, since it asks nothing; both are taken and forgotten.
	if req.Notification || req.Reply {
		httpx.SkipAudit(r)
		w.WriteHeader(http.StatusAccepted)
		return nil
	}
	call, serr := mcp.Settle(req, r.Header)
	if serr != nil {
		httpx.SkipAudit(r)
		return mcpRefusal(w, req.ID, serr)
	}

	p := httpx.MustPrincipal(r)
	switch {
	case req.Method == "initialize" && !call.Modern:
		// A client connecting is worth a line in the trail, the way opening
		// a terminal is; the requests after it are their own lines.
		httpx.SetAudit(r, "database.mcp.connect", clipTarget(req.Client.Name), map[string]any{
			"key": p.Token.Name, "prefix": p.Token.Prefix,
			"client": req.Client.Name, "clientVersion": req.Client.Version, "protocolVersion": call.Version,
		})
		return writeMCP(w, http.StatusOK, mcp.Response(req.ID, call.Initialize(mcp.Instructions)))
	case req.Method == "server/discover" && call.Modern:
		httpx.SkipAudit(r)
		return writeMCP(w, http.StatusOK, mcp.Response(req.ID, call.Discover(mcp.Instructions)))
	case req.Method == "ping":
		httpx.SkipAudit(r)
		return writeMCP(w, http.StatusOK, mcp.Response(req.ID, call.Complete(map[string]any{})))
	case req.Method == "tools/list":
		httpx.SkipAudit(r)
		grants, err := s.Auth.TokenConnections(r.Context(), p.TokenID)
		if err != nil {
			return mcpRefusal(w, req.ID, &mcp.Error{Code: mcp.CodeInternalError, Message: "the key's databases could not be read"})
		}
		query, write := mcpToolsFor(p, grants)
		return writeMCP(w, http.StatusOK, mcp.Response(req.ID, call.Cached(map[string]any{"tools": mcp.Tools(query, write)}, 60_000)))
	case req.Method == "tools/call":
		if !mcp.Known(req.Name) {
			httpx.SkipAudit(r)
			return mcpRefusal(w, req.ID, &mcp.Error{Code: mcp.CodeInvalidParams, Message: "Unknown tool: " + req.Name})
		}
		return writeMCP(w, http.StatusOK, mcp.Response(req.ID, call.Complete(s.callMCPTool(r, p, req))))
	}
	httpx.SkipAudit(r)
	return mcpRefusal(w, req.ID, call.MethodNotFound())
}

// mcpToolsFor is which of the two statement tools a key is offered: a query
// for an owner whose role may run SQL at all, which is what the query route
// asks of its caller, and a change where the key may also change something.
func mcpToolsFor(p *httpx.Principal, grants []auth.TokenConnection) (query, write bool) {
	query = p.Can(auth.CapServiceControl)
	for _, g := range grants {
		write = write || (query && g.Access == auth.AccessWrite)
	}
	return query, write
}
