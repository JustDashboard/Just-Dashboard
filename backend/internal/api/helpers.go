package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// contextWithCancel detaches from the request context's cancellation only in
// the sense of adding our own cancel; the request context still ends the
// stream when the client disconnects.
func contextWithCancel(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithCancel(r.Context())
}

func timeoutCtx(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}

// atoiDefault and defaultStr read query parameters that have a sensible
// fallback. They lived in handlers_auth.go and handlers_docker.go respectively
// and are about neither auth nor Docker.
func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func defaultStr(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func clampDuration(d, lo, hi time.Duration) time.Duration {
	if d < lo {
		return lo
	}
	if d > hi {
		return hi
	}
	return d
}

// recordAudit writes an entry immediately rather than through the mutation
// middleware. WebSocket endpoints are GET requests and long-lived, so the
// interesting event is "a terminal was opened", recorded the moment it
// happens — not a status code written when the socket eventually closes.
func (s *Server) recordAudit(r *http.Request, action, target string, detail any) {
	p := httpx.MustPrincipal(r)
	s.Audit.Record(r.Context(), audit.Entry{
		UserID:   p.UserID(),
		Username: p.Username(),
		Role:     string(p.Role),
		IP:       httpx.ClientIP(r),
		Actor:    p.Kind,
		Action:   action,
		Target:   target,
		Method:   r.Method,
		Path:     r.URL.Path,
		Status:   http.StatusOK,
		Success:  true,
		Detail:   audit.Detail(detail),
		KeyID:    p.KeyID(),
		KeyName:  p.KeyName(),
	})
}

// detachedContext is for work that must outlive the request that started it —
// a backup transfer keeps running after the browser has its 202.
//
// It deliberately descends from context.Background() rather than from anything
// Shutdown cancels. A `docker compose up --build` halfway through is worse
// than one that finishes into a dashboard that is no longer running, so these
// goroutines survive shutdown and the process exits while they are still
// going. The timeout passed here is their only bound; nothing joins them, and
// a restart mid-deploy therefore leaves the deploy to complete or to time out
// on its own.
func detachedContext(seconds int) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), time.Duration(seconds)*time.Second)
}
