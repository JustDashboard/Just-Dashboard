package httpx

import (
	"context"
	"net/http"
	"regexp"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/go-chi/chi/v5"
)

const auditCtxKey ctxKey = 200

// auditInfo is the mutable slot a handler enriches while it runs. Recording
// happens in the middleware after the response is written, so every mutating
// route is logged whether or not the handler remembered to.
type auditInfo struct {
	Action string
	Target string
	Detail string
	Actor  string
	Skip   bool
}

// SetAudit labels the current request for the audit trail.
func SetAudit(r *http.Request, action, target string, detail any) {
	if info, ok := r.Context().Value(auditCtxKey).(*auditInfo); ok {
		info.Action = action
		info.Target = target
		if detail != nil {
			info.Detail = audit.Detail(detail)
		}
	}
}

// SetAuditActor names who acted when it is not the authenticated principal —
// the deploy webhook, which authenticates by HMAC and has no session.
func SetAuditActor(r *http.Request, actor string) {
	if info, ok := r.Context().Value(auditCtxKey).(*auditInfo); ok {
		info.Actor = actor
	}
}

// SkipAudit suppresses the record for a route that is mutating in HTTP terms
// but carries no security meaning (terminal keystrokes, for instance, which
// are logged once at session open rather than per byte).
func SkipAudit(r *http.Request) {
	if info, ok := r.Context().Value(auditCtxKey).(*auditInfo); ok {
		info.Skip = true
	}
}

// AuditMutations records every state-changing request. Reads are excluded:
// they are high-volume and the interesting question for an operator is who
// changed the machine, not who looked at it.
func AuditMutations(log *audit.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}
			info := &auditInfo{Action: defaultAction(r)}
			ctx := context.WithValue(r.Context(), auditCtxKey, info)
			sw := &statusWriter{ResponseWriter: w}
			r = r.WithContext(ctx)
			next.ServeHTTP(sw, r)

			if info.Skip {
				return
			}
			p := MustPrincipal(r)
			actor := info.Actor
			if actor != "" {
				// An explicit actor means this route does not authenticate a
				// principal at all, so MustPrincipal's readonly stand-in must
				// not be recorded as the caller's role.
				p = &Principal{}
			} else {
				actor = principalKind(p)
			}
			detail := info.Detail
			if sw.Status() >= 400 && p.FailureReason != "" {
				detail = strings.TrimSpace(detail + " " + p.FailureReason)
			}
			log.Record(r.Context(), audit.Entry{
				UserID:   p.UserID(),
				Username: p.Username(),
				Role:     string(p.Role),
				IP:       ClientIP(r),
				Actor:    actor,
				Action:   info.Action,
				Target:   info.Target,
				Method:   r.Method,
				Path:     r.URL.Path,
				Status:   sw.Status(),
				Success:  sw.Status() < 400,
				Detail:   detail,
			})
		})
	}
}

func principalKind(p *Principal) string {
	if p == nil || p.Kind == "" {
		return "anonymous"
	}
	return p.Kind
}

// apiVersionSegment matches the leading "v1/" of a trimmed API path.
var apiVersionSegment = regexp.MustCompile(`^v[0-9]+/`)

// chiParam matches a chi route parameter, "{id}" or "{name}".
var chiParam = regexp.MustCompile(`^\{[^}]*\}$`)

// defaultAction derives a stable label from the route so an un-annotated route
// still produces a meaningful audit entry.
//
// It reads chi's route *pattern* rather than the request path, because chi
// already knows which segments are parameters and which are the route's name.
// Guessing from the path did not: any segment of twelve characters or more was
// dropped as an "id", which quietly deleted the route names too —
// POST /dashboard-users was recorded as "post:" with no action at all, and a
// rejected POST /docker/containers/abc/stop as "post:docker.containers.abc",
// losing the verb that distinguishes it from kill or restart. This label is
// what a denied or failed request is recorded under, so those were exactly the
// entries that lost their meaning.
//
// The version segment is stripped along with the prefix: an audit trail should
// read "docker.container.restart" for the life of the action, not gain a "v1."
// today and a "v2." the day the API is revised.
func defaultAction(r *http.Request) string {
	path := r.URL.Path
	if rc := chi.RouteContext(r.Context()); rc != nil {
		if pattern := rc.RoutePattern(); pattern != "" {
			path = pattern
		}
	}
	path = strings.TrimPrefix(path, "/api/")
	path = apiVersionSegment.ReplaceAllString(path, "")
	keep := make([]string, 0, 4)
	for _, p := range strings.Split(path, "/") {
		if p == "" || p == "*" || chiParam.MatchString(p) {
			continue
		}
		keep = append(keep, p)
	}
	return strings.ToLower(r.Method) + ":" + strings.Join(keep, ".")
}

// AuditRead records a read the middleware would skip, for the few GETs that
// send traffic somewhere the caller chose in a way worth a trail. It is
// written before the read runs, so a probe that hangs is still on record.
func AuditRead(log *audit.Logger, r *http.Request, action, target string) {
	p := MustPrincipal(r)
	log.Record(r.Context(), audit.Entry{
		UserID:   p.UserID(),
		Username: p.Username(),
		Role:     string(p.Role),
		IP:       ClientIP(r),
		Actor:    principalKind(p),
		Action:   action,
		Target:   target,
		Method:   r.Method,
		Path:     r.URL.Path,
		Status:   http.StatusOK,
		Success:  true,
	})
}
