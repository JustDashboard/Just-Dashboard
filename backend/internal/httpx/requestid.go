package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

// RequestIDHeader carries a request's id: echoed on every response, and read
// from a request that brings a usable one of its own.
const RequestIDHeader = "X-Request-Id"

const requestIDKey ctxKey = 302

// RequestID gives every request an id and puts it where the three people who
// meet a failure can each quote it: the response header, the error envelope of
// a 5xx (WriteError), and the log line.
//
// httpx.Internal hides the cause of a 500 from the client, which is right, and
// used to leave the person holding it with "internal error" and nothing to
// name: the log had the cause, keyed by method, path and second, so two
// failures of one polled route could not be told apart. An id was generated
// for every request before this and then read by nothing.
//
// A caller's own X-Request-Id is kept when it is short and plain, so a script
// can follow its request into the log; anything else is replaced, since the
// value goes into a header and a log line. A request the dashboard makes of
// itself (the assistant's and an MCP tool's calls, through the root router)
// keeps the id of the request that caused it.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := RequestIDFrom(r.Context())
		if id == "" {
			id = r.Header.Get(RequestIDHeader)
			if !usableRequestID(id) {
				id = newRequestID()
			}
			r = r.WithContext(context.WithValue(r.Context(), requestIDKey, id))
		}
		w.Header().Set(RequestIDHeader, id)
		next.ServeHTTP(w, r)
	})
}

// RequestIDFrom is the id RequestID gave the request, or "" outside one.
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}

// usableRequestID admits what tracing systems send (hex, UUIDs, dotted and
// dashed tokens) and nothing that could break a header or a log line.
func usableRequestID(id string) bool {
	if len(id) < 8 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
		default:
			return false
		}
	}
	return true
}
