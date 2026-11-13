package api

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// The router's own refusals, in the API's envelope.
//
// chi answers a path it does not know with "404 page not found" as text and a
// method a path does not take with an empty 405. Every other refusal of this
// API is {"error":{"code","message"}} written by httpx.WriteError, and a
// client that reads a code from that had nothing to read from these two. The
// handlers below are set on the root router before anything is mounted, so
// every subrouter inherits them.

func routeNotFound(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, httpx.Err(http.StatusNotFound, "not_found",
		"no route at "+r.URL.Path))
}

// routeMethodNotAllowed answers 405 with the methods the path does take in
// Allow, as chi's own handler did: a custom one is not handed them, so they
// are asked of the router again.
func (t *routeMethodTable) methodNotAllowed(w http.ResponseWriter, r *http.Request) {
	writeMethodNotAllowed(w, r, t.allowed(routedPath(r)))
}

func writeMethodNotAllowed(w http.ResponseWriter, r *http.Request, allowed []string) {
	if len(allowed) > 0 {
		w.Header().Set("Allow", strings.Join(allowed, ", "))
	}
	message := r.Method + " is not a method of " + r.URL.Path
	if len(allowed) > 0 {
		message += "; it takes " + strings.Join(allowed, ", ")
	}
	httpx.WriteError(w, r, httpx.Err(http.StatusMethodNotAllowed, "method_not_allowed", message))
}

// routedPath is the path chi routes on: the raw one whenever the request
// carried an escape.
func routedPath(r *http.Request) string {
	if r.URL.RawPath != "" {
		return r.URL.RawPath
	}
	return r.URL.Path
}

// refuseShadowedMethods answers 405 where the router would hand a static path
// to its parameter sibling.
//
// chi tries the static node first, and when that node has no handler for the
// method it backtracks to the parameter node beside it. So GET
// /deploy/templates/import, a path that takes only POST, reached GET
// /deploy/templates/{id} as the template named "import" and answered
// "template not found: import"; PATCH /network/diagnostics/wol-devices
// reached the diagnostic named "wol-devices"; and DELETE /pm2/save would have
// deleted a process that happened to be named "save". A wrong verb on an
// action path is the caller's mistake and is answered as one.
//
// What this takes away: a resource whose name is exactly an action segment of
// its own collection (a volume named "prune", a site named "bulk") is not
// addressable with the methods that action lacks. That was true of the
// methods the action has all along.
//
// It is the last middleware of the feature group, which is where the router's
// own 405 for those routes is answered from: behind authentication, the rate
// limit and the audit trail, so a caller with no credential learns nothing
// from it and a refused write is recorded like any other.
func (t *routeMethodTable) refuseShadowedMethods(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if allowed, shadowed := t.shadowed(r.Method, routedPath(r)); shadowed {
			writeMethodNotAllowed(w, r, allowed)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// routeMethodTable is what one router serves, read off its finished tree the
// first time a request asks: every pattern's methods, and for every parameter
// route the static siblings it would answer for. It is read late because the
// tree is not finished when the middleware is mounted, and because the tests
// build routers by the thousand and most never serve a request.
type routeMethodTable struct {
	root chi.Routes
	once sync.Once
	// methods is the methods each pattern takes, "*" for a route mounted for
	// all of them.
	methods map[string]map[string]bool
	// shadows maps "METHOD pattern" of a parameter route to the static
	// siblings that lack the method.
	shadows map[string][]routePattern
}

type routePattern struct {
	pattern  string
	segments []string
}

// routeVerbs is the methods a route is asked about for Allow.
var routeVerbs = []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}

func (t *routeMethodTable) build() {
	t.once.Do(func() {
		t.methods = map[string]map[string]bool{}
		_ = chi.Walk(t.root, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			pattern = trimRouteSlash(pattern)
			if t.methods[pattern] == nil {
				t.methods[pattern] = map[string]bool{}
			}
			t.methods[pattern][method] = true
			return nil
		})
		// Siblings have the same number of segments and, wherever the section
		// is spelled out (/api/v1/<section>/…), the same section, so only
		// those are compared: every pattern against every other took longer
		// than building the router did.
		type bucket struct {
			size    int
			section string
		}
		buckets := map[bucket][]routePattern{}
		var patterns []routePattern
		for pattern := range t.methods {
			if strings.Contains(pattern, "*") {
				continue
			}
			route := routePattern{pattern, strings.Split(pattern, "/")}
			patterns = append(patterns, route)
			key := bucket{len(route.segments), routeSection(route.segments)}
			buckets[key] = append(buckets[key], route)
		}
		t.shadows = map[string][]routePattern{}
		for _, wide := range patterns {
			if !strings.Contains(wide.pattern, "{") {
				continue
			}
			section := routeSection(wide.segments)
			for key, narrows := range buckets {
				// A parameter where the section would be generalises every section.
				if key.size != len(wide.segments) || (section != "" && key.section != section) {
					continue
				}
				for _, narrow := range narrows {
					if !generalises(wide.segments, narrow.segments) {
						continue
					}
					for method := range t.methods[wide.pattern] {
						if method == "*" || t.takes(narrow.pattern, method) {
							continue
						}
						shadow := method + " " + wide.pattern
						t.shadows[shadow] = append(t.shadows[shadow], narrow)
					}
				}
			}
		}
	})
}

// routeSection is the segment after /api/v1 when it is static, the part of a
// pattern its siblings always share; empty for a parameter there or a shorter
// path.
func routeSection(segments []string) string {
	if len(segments) <= 3 || isRouteParam(segments[3]) {
		return ""
	}
	return segments[3]
}

func (t *routeMethodTable) takes(pattern, method string) bool {
	return t.methods[pattern][method] || t.methods[pattern]["*"]
}

// shadowed reports whether the router would answer method on path with a
// parameter route although a static route is registered at that very path
// without the method, and the methods that static route does take.
func (t *routeMethodTable) shadowed(method, path string) ([]string, bool) {
	t.build()
	if len(t.shadows) == 0 {
		return nil, false
	}
	// The router's own answer to "which route is this", so a static route
	// that does take the method is never mistaken for a shadowed one.
	matched := trimRouteSlash(t.root.Find(chi.NewRouteContext(), method, path))
	if matched == "" {
		return nil, false
	}
	siblings := t.shadows[method+" "+matched]
	if len(siblings) == 0 {
		return nil, false
	}
	segments := strings.Split(trimRouteSlash(path), "/")
	for _, sibling := range siblings {
		if matchesPattern(sibling.segments, segments) {
			return t.verbsOf(sibling.pattern), true
		}
	}
	return nil, false
}

// allowed is the methods path takes, for a 405's Allow header.
//
// The router's Find alone cannot say: a mount's root answers it for every
// method, because the subrouter mounted there is handed all of them and
// refuses what it does not take itself. So a method counts when the route
// Find names really has it.
func (t *routeMethodTable) allowed(path string) []string {
	t.build()
	var allowed []string
	for _, method := range routeVerbs {
		matched := trimRouteSlash(t.root.Find(chi.NewRouteContext(), method, path))
		if matched == "" || !t.takes(matched, method) {
			continue
		}
		if _, shadowed := t.shadowed(method, path); !shadowed {
			allowed = append(allowed, method)
		}
	}
	return allowed
}

func (t *routeMethodTable) verbsOf(pattern string) []string {
	var verbs []string
	for _, method := range routeVerbs {
		if t.takes(pattern, method) {
			verbs = append(verbs, method)
		}
	}
	sort.Strings(verbs)
	return verbs
}

// generalises reports whether wide is narrow with at least one of narrow's
// static segments turned into a parameter, and nothing else different: the
// pair the router confuses when narrow lacks a method.
func generalises(wide, narrow []string) bool {
	if len(wide) != len(narrow) {
		return false
	}
	widened := false
	for i := range wide {
		wideParam, narrowParam := isRouteParam(wide[i]), isRouteParam(narrow[i])
		switch {
		case wideParam && !narrowParam:
			widened = true
		case wideParam != narrowParam:
			return false
		case !wideParam && wide[i] != narrow[i]:
			return false
		}
	}
	return widened
}

// matchesPattern reports whether a path's segments are an instance of a
// pattern's: equal where the pattern is static, anything where it is a
// parameter.
func matchesPattern(pattern, path []string) bool {
	if len(pattern) != len(path) {
		return false
	}
	for i := range pattern {
		if isRouteParam(pattern[i]) {
			if path[i] == "" {
				return false
			}
			continue
		}
		if pattern[i] != path[i] {
			return false
		}
	}
	return true
}

func isRouteParam(segment string) bool { return strings.HasPrefix(segment, "{") }

// trimRouteSlash makes a mount's root one pattern: the router serves it with
// and without its trailing slash.
func trimRouteSlash(pattern string) string {
	if len(pattern) > 1 {
		return strings.TrimSuffix(pattern, "/")
	}
	return pattern
}
