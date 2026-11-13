package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/go-chi/chi/v5"
)

// anonymous sends a request with no credential from an allowlisted address.
// Nothing it sends reaches a handler: a route that exists answers 401 from its
// group's authentication, which is how these tests tell "routed" from
// "refused by the router" without running anything.
func anonymous(h http.Handler, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = "127.0.0.1:5555"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// envelopeCode is the error code of a refusal, or "" when the body is not the
// API's envelope.
func envelopeCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		return ""
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error.Message == "" {
		return ""
	}
	return body.Error.Code
}

// An unknown path used to answer "404 page not found" as text and a wrong
// method an empty 405: the two refusals a client could not read a code from.
//
// The signed-in requests here are a read-only account's, and each is one the
// router itself refuses: none reaches a handler.
func TestTheRoutersOwnRefusalsAreInTheEnvelope(t *testing.T) {
	s := testServer(t)
	h := s.Routes()
	viewer := &client{t: t, h: h, cookie: signInAs(t, s, "viewer", auth.RoleReadOnly)}

	for _, path := range []string{"/api/v1/nope", "/nope", "/api/v2/system/host"} {
		rec := anonymous(h, http.MethodGet, path)
		if rec.Code != http.StatusNotFound || envelopeCode(t, rec) != "not_found" {
			t.Errorf("GET %s = %d %q %q, want 404 not_found in the envelope",
				path, rec.Code, rec.Header().Get("Content-Type"), strings.TrimSpace(rec.Body.String()))
		}
	}
	// Under a group the router is asked after authentication, as before.
	if rec := anonymous(h, http.MethodGet, "/api/v1/deploy/1/nope/nope"); rec.Code != http.StatusUnauthorized {
		t.Errorf("an unknown path under the feature routes with no credential = %d, want 401", rec.Code)
	}
	for _, path := range []string{"/api/v1/deploy/1/nope/nope", "/api/v1/tokens/1/2/3", "/api/v1/system/nope"} {
		rec := viewer.do(http.MethodGet, path, "", nil)
		if rec.Code != http.StatusNotFound || envelopeCode(t, rec) != "not_found" {
			t.Errorf("GET %s = %d %q, want 404 not_found in the envelope", path, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
	for _, c := range []struct{ method, path, allow string }{
		{http.MethodPatch, "/api/v1/tokens/", "GET, POST"},
		{http.MethodPatch, "/api/v1/tokens", "GET, POST"},
		{http.MethodOptions, "/api/v1/system/host", "GET"},
		{http.MethodPost, "/api/v1/system/host", "GET"},
	} {
		rec := viewer.do(c.method, c.path, "", nil)
		if rec.Code != http.StatusMethodNotAllowed || envelopeCode(t, rec) != "method_not_allowed" {
			t.Errorf("%s %s = %d %q, want 405 method_not_allowed in the envelope",
				c.method, c.path, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
		if got := rec.Header().Get("Allow"); got != c.allow {
			t.Errorf("%s %s: Allow = %q, want %q", c.method, c.path, got, c.allow)
		}
	}
	if rec := anonymous(h, http.MethodDelete, "/healthz"); rec.Code != http.StatusMethodNotAllowed ||
		envelopeCode(t, rec) != "method_not_allowed" || rec.Header().Get("Allow") != "GET" {
		t.Errorf("DELETE /healthz = %d %q, Allow %q", rec.Code, strings.TrimSpace(rec.Body.String()), rec.Header().Get("Allow"))
	}
	// HEAD has no body to carry the envelope; it still says what the path takes.
	if rec := viewer.do(http.MethodHead, "/api/v1/system/host", "", nil); rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET" {
		t.Errorf("HEAD = %d, Allow %q", rec.Code, rec.Header().Get("Allow"))
	}
}

// chi hands a static path to the {param} route beside it when the static one
// lacks the method, so a wrong verb on an action path ran the parameter
// route's handler with the action's name as the parameter: GET
// /deploy/templates/import answered "template not found: import". Every such
// pair the router holds is refused with 405 on the static path instead.
//
// The walk asks the table and sends nothing with a credential: a pair the
// table missed would otherwise run the parameter route's handler here, and
// some of those delete things.
func TestAStaticPathNeverAnswersForItsParameterSibling(t *testing.T) {
	s := testServer(t)
	h := s.Routes()
	root := h.(chi.Routes)
	table := &routeMethodTable{root: root}

	methods := map[string]map[string]bool{}
	_ = chi.Walk(root, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		pattern = trimRouteSlash(pattern)
		if methods[pattern] == nil {
			methods[pattern] = map[string]bool{}
		}
		methods[pattern][method] = true
		return nil
	})
	// The pairs, found the plain way and apart from the table under test: a
	// static last segment, and a pattern that differs only by a parameter there.
	type pair struct{ method, static, param string }
	var pairs []pair
	for static := range methods {
		cut := strings.LastIndex(static, "/")
		if !strings.HasPrefix(static, "/api/v1/") || strings.ContainsAny(static[cut+1:], "{*") || static[cut+1:] == "" {
			continue
		}
		for param := range methods {
			last := strings.LastIndex(param, "/")
			if param[:last] != static[:cut] || !strings.HasPrefix(param[last+1:], "{") {
				continue
			}
			for method := range methods[param] {
				if method != "*" && !methods[static][method] && !methods[static]["*"] {
					pairs = append(pairs, pair{method, static, param})
				}
			}
		}
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].static+pairs[i].method < pairs[j].static+pairs[j].method })
	refused := 0
	for _, p := range pairs {
		path := instancePath(p.static)
		// With no credential nothing is routed past its group's sign-in, which
		// is also where the refusal now stands.
		if rec := anonymous(h, p.method, path); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s with no credential = %d %s, want 401", p.method, path, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
		allowed, shadowed := table.shadowed(p.method, path)
		if routed := trimRouteSlash(root.Find(chi.NewRouteContext(), p.method, path)); routed != p.param {
			// A mount's root: the subrouter mounted there is handed every
			// method and refuses this one itself, so nothing falls through.
			if routed != p.static || shadowed {
				t.Errorf("%s %s is routed to %q (shadowed %v); want the mount at %s, left to the router", p.method, path, routed, shadowed, p.static)
			}
			continue
		}
		if !shadowed {
			t.Errorf("%s %s is routed to %s and not refused", p.method, path, p.param)
			continue
		}
		refused++
		allow := strings.Join(allowed, ", ")
		for method := range methods[p.static] {
			if method != "*" && !strings.Contains(allow, method) {
				t.Errorf("%s %s: Allow = %q leaves out %s, which the path takes", p.method, path, allow, method)
			}
		}
		if strings.Contains(allow, p.method) {
			t.Errorf("%s %s: Allow = %q offers the method that was just refused", p.method, path, allow)
		}
	}
	// The audit counted 146 pairs; a count near zero would mean the walk
	// above stopped finding them, not that the router stopped having them.
	if refused < 100 {
		t.Fatalf("%d static paths refused for their parameter sibling's methods; the router holds well over a hundred", refused)
	}

	// The ones the audit measured, by name, and that nothing else moved: the
	// action still takes its own method, and the parameter route still
	// answers for every other name.
	for _, c := range []struct {
		method, path string
		shadowed     bool
	}{
		{http.MethodGet, "/api/v1/deploy/templates/import", true},
		{http.MethodPatch, "/api/v1/network/diagnostics/wol-devices", true},
		{http.MethodGet, "/api/v1/backups/catalogue", true},
		{http.MethodGet, "/api/v1/packages/install", true},
		{http.MethodDelete, "/api/v1/pm2/save", true},
		{http.MethodDelete, "/api/v1/docker/volumes/prune", true},
		{http.MethodDelete, "/api/v1/proxy/sites/bulk", true},
		{http.MethodPost, "/api/v1/deploy/templates/import", false},
		{http.MethodGet, "/api/v1/deploy/templates/user-wiki", false},
		{http.MethodDelete, "/api/v1/pm2/api", false},
		{http.MethodPost, "/api/v1/pm2/save", false},
		{http.MethodDelete, "/api/v1/docker/volumes/pgdata", false},
		{http.MethodDelete, "/api/v1/proxy/sites/example.com", false},
		{http.MethodDelete, "/api/v1/proxy/sites/bulky", false},
		{http.MethodGet, "/api/v1/nope", false},
	} {
		if _, shadowed := table.shadowed(c.method, c.path); shadowed != c.shadowed {
			t.Errorf("%s %s: shadowed = %v, want %v", c.method, c.path, shadowed, c.shadowed)
		}
	}

	// And through the mounted router, for a read-only account, on two pairs
	// whose parameter route only looks a record up: it is the middleware that
	// answers, in the envelope, with what the path does take.
	viewer := &client{t: t, h: h, cookie: signInAs(t, s, "viewer", auth.RoleReadOnly)}
	for path, allow := range map[string]string{
		"/api/v1/deploy/templates/import": "POST",
		"/api/v1/backups/catalogue":       "POST",
	} {
		rec := viewer.do(http.MethodGet, path, "", nil)
		if rec.Code != http.StatusMethodNotAllowed || envelopeCode(t, rec) != "method_not_allowed" || rec.Header().Get("Allow") != allow {
			t.Errorf("GET %s = %d %s, Allow %q; want 405 and Allow %q", path, rec.Code, strings.TrimSpace(rec.Body.String()), rec.Header().Get("Allow"), allow)
		}
	}
}

// instancePath is a path a pattern matches, with a number for each parameter.
func instancePath(pattern string) string {
	segments := strings.Split(pattern, "/")
	for i, segment := range segments {
		if isRouteParam(segment) {
			segments[i] = "7"
		}
	}
	return strings.Join(segments, "/")
}

func TestOneRoutePatternGeneralisesAnotherOnlyByItsParameters(t *testing.T) {
	split := func(p string) []string { return strings.Split(p, "/") }
	for _, c := range []struct {
		wide, narrow string
		want         bool
	}{
		{"/pm2/{name}", "/pm2/save", true},
		{"/databases/{id}/ddl/{kind}", "/databases/{id}/ddl/table", true},
		{"/a/{x}/c", "/a/b/c", true},
		{"/pm2/{name}", "/pm2/{id}", false},                 // the same route by another name
		{"/pm2/save", "/pm2/{name}", false},                 // the other way round
		{"/pm2/{name}", "/pm3/save", false},                 // different collections
		{"/pm2/{name}", "/pm2/save/now", false},             // different depth
		{"/deploy/{id}/x", "/deploy/templates/{id}", false}, // each wider than the other somewhere
	} {
		if got := generalises(split(c.wide), split(c.narrow)); got != c.want {
			t.Errorf("generalises(%s, %s) = %v, want %v", c.wide, c.narrow, got, c.want)
		}
	}
}
