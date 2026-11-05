package api

import (
	"net/http"
	"os"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// hubSessionRefused is every route a session made by a Just Hub sign-in ticket
// is refused on (hub-link-protocol.md section 9.9): each one creates, changes,
// rotates, imports or exports a credential, or creates or changes a link, and
// what it made would outlive the two-hour hub session and the grant behind it.
// The first block is the protocol's table as it was first written; the second
// is what the walk of every write route found that the rule covers, which the
// protocol's table now lists too (docs/internal/security/invariants.md names
// both). A route added later that the rule covers joins this list in the same
// change.
var hubSessionRefused = []string{
	// API, MCP and deploy keys.
	"POST /api/v1/tokens/",
	"POST /api/v1/tokens/mcp",
	"POST /api/v1/tokens/deploy",
	"PUT /api/v1/tokens/{id}/connections",
	// Dashboard accounts.
	"POST /api/v1/dashboard-users/",
	"PATCH /api/v1/dashboard-users/{id}",
	"POST /api/v1/dashboard-users/{id}/reset-totp",
	"DELETE /api/v1/dashboard-users/{id}",
	// The caller's own credentials.
	"POST /api/v1/account/password",
	"POST /api/v1/account/recovery-codes",
	"POST /api/v1/account/2fa/disable",
	"POST /api/v1/auth/2fa/setup",
	"POST /api/v1/auth/2fa/enable",
	"POST /api/v1/account/passkeys/register/begin",
	"POST /api/v1/account/passkeys/register/finish",
	"PATCH /api/v1/account/passkeys/{id}",
	"DELETE /api/v1/account/passkeys/{id}",
	// Linux accounts and their keys.
	"POST /api/v1/system-users/",
	"PATCH /api/v1/system-users/{name}/",
	"PUT /api/v1/system-users/{name}/sudo",
	"POST /api/v1/system-users/{name}/password",
	"POST /api/v1/system-users/{name}/keys/generate",
	"POST /api/v1/system-users/{name}/keys",
	"PATCH /api/v1/system-users/{name}/keys",
	"POST /api/v1/system-users/{name}/keys/import",
	"POST /api/v1/system-users/{name}/keys/copy",
	// Database users.
	"POST /api/v1/databases/{id}/server/roles",
	"PUT /api/v1/databases/{id}/server/roles/{name}",
	"POST /api/v1/databases/{id}/server/roles/{name}/grant",
	"POST /api/v1/databases/{id}/mongo/users",
	"PUT /api/v1/databases/{id}/mongo/users",
	"POST /api/v1/databases/{id}/mongo/users/grant",
	"PUT /api/v1/databases/{id}/redis/acl/{name}",
	// Proxy basic-auth users.
	"POST /api/v1/proxy/auth-files/",
	// Deployment secrets and saved credentials.
	"POST /api/v1/deploy/{id}/environments/{env}/triggers",
	"PUT /api/v1/deploy/{id}/environments/{env}/triggers/{trigger}",
	"POST /api/v1/deploy/{id}/environments/{env}/triggers/{trigger}/rotate-secret",
	"POST /api/v1/deploy/{id}/rotate-secret",
	"POST /api/v1/deploy/credentials",
	"PUT /api/v1/deploy/credentials/{id}",
	"POST /api/v1/deploy/secret-stores",
	"PUT /api/v1/deploy/secret-stores/{store}",
	"POST /api/v1/deploy/notifications",
	"PUT /api/v1/deploy/notifications/{channel}",
	"POST /api/v1/deploy/log-drains",
	"PUT /api/v1/deploy/log-drains/{drain}",
	// GitHub.
	"POST /api/v1/git/github/auth/device",
	"POST /api/v1/git/github/auth/device/{id}",
	"POST /api/v1/git/github/auth/token",
	"POST /api/v1/git/github/auth/configure",
	"POST /api/v1/deploy/github-app/manifest",
	"POST /api/v1/deploy/github-app/callback",
	// DNS credentials and TLS keys.
	"POST /api/v1/certificates/dns-credentials",
	"POST /api/v1/certificates/import",
	"POST /api/v1/certificates/export",
	// The backup key.
	"POST /api/v1/backups/key/import",
	"POST /api/v1/backups/key/export",
	// The account link.
	"POST /api/v1/dashboard/link/",
	"DELETE /api/v1/dashboard/link/request",
	"PUT /api/v1/dashboard/link/scope",
	// The hub link.
	"POST /api/v1/dashboard/hub",
	"DELETE /api/v1/dashboard/hub/request",
	"PUT /api/v1/dashboard/hub/scope",
	// Hub sign-in.
	"PUT /api/v1/account/hub-signin",

	// Found by the walk of every write route: the rule covers them, and the
	// protocol's table lists them since.
	"POST /api/v1/git/forge/account",                             // a saved Git host token
	"POST /api/v1/databases/host/grant",                          // makes or resets a database login and its password
	"POST /api/v1/databases/{id}/server/roles/{name}/privileges", // widens a database role
	"POST /api/v1/system-accounts/bulk",                          // makes Linux accounts
	"PATCH /api/v1/system-accounts/groups/{group}",               // group membership, sudo and docker included
	"POST /api/v1/network/vpn/wireguard/{iface}/peers",           // a peer key that lets a device onto this server's network
	"PATCH /api/v1/network/vpn/wireguard/{iface}/peers/{id}",
	"POST /api/v1/network/vpn/archive/{file}/restore", // brings an archived tunnel's peer keys back
	"POST /api/v1/network/external/enrollments",       // a token a probe machine enrols with, then keeps calling this server
	"PUT /api/v1/proxy/tunnel/",                       // saves a Cloudflare tunnel token
	"POST /api/v1/proxy/tunnel/create",                // spends the saved Cloudflare token on a new tunnel token
	"POST /api/v1/network/dns/services/",              // a DNS server's saved API credential
	"PUT /api/v1/network/dns/services/{id}",
	"POST /api/v1/backups/", // a backup destination holds credentials of its own
	"PUT /api/v1/backups/{id}",
	"POST /api/v1/backups/import",
	"PUT /api/v1/assistant/providers/{preset}", // an AI provider's saved API key

	// Found by the review of the walk (errata HL-3, second walk): routes that
	// export a stored secret, read ones included, and writes the first walk
	// missed.
	"GET /api/v1/network/vpn/wireguard/{iface}/peers/{id}/config",                // a peer's private key
	"GET /api/v1/databases/{id}/url",                                             // the connection string, password included
	"PUT /api/v1/databases/{id}/redis/config",                                    // requirepass, masterauth and the ACL file
	"POST /api/v1/deploy/{id}/environments/{env}/releases/{release}/data-copies", // a database login with a new password
	"GET /api/v1/deploy/{id}/environments/{env}/data-copies/{copy}/url",          // that login's password
	"GET /api/v1/deploy/{id}/env/reveal",                                         // a project's secret values
	"GET /api/v1/deploy/{id}/environments/{env}/variables/{name}/reveal",         // a variable's secret value
	"POST /api/v1/deploy/{id}/environments/{env}/variables/{name}/generate",      // a new secret, shown
	"POST /api/v1/deploy/{id}/environments/{env}/variables/{name}/rotate",        // a rotated secret, shown
	"GET /api/v1/deploy/shared-variables/{name}/reveal",                          // a shared secret value
	"GET /api/v1/deploy/{id}/shared-variables/{name}/reveal",                     // a project's shared secret value
	"POST /api/v1/deploy/",                                   // a project's webhook secret
	"POST /api/v1/proxy/import/npm",                          // basic-auth users and their passwords
	"POST /api/v1/proxy/import/npm/apply",                    // writes them as password files
	"POST /api/v1/remote-desktop/computers",                  // a computer's saved sign-in
	"PUT /api/v1/remote-desktop/computers/{id}",              // changes it, or where it is sent
	"POST /api/v1/remote-desktop/computers/{id}/stream/pair", // a client certificate the computer trusts
}

// hubSessionCarriers is every route of the router that carries
// httpx.RefuseHubSession, found by function identity.
func hubSessionCarriers(t *testing.T, h http.Handler) map[string]bool {
	t.Helper()
	want := runtime.FuncForPC(reflect.ValueOf(httpx.RefuseHubSession).Pointer()).Name()
	out := map[string]bool{}
	err := chi.Walk(h.(chi.Routes), func(method, pattern string, _ http.Handler, mws ...func(http.Handler) http.Handler) error {
		for _, mw := range mws {
			if runtime.FuncForPC(reflect.ValueOf(mw).Pointer()).Name() == want {
				out[method+" "+pattern] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Every route of section 9.9 refuses a hub session, and nothing else does:
// hubSessionRefused is exactly what the router enforces, and
// TestTheDocumentedRouteTablesAreHubSessionRefused holds the documents to it.
func TestEveryCredentialAndLinkRouteRefusesAHubSession(t *testing.T) {
	s := testServer(t)
	got := hubSessionCarriers(t, s.Routes())
	for _, rt := range hubSessionRefused {
		if !got[rt] {
			t.Errorf("%s does not refuse a hub session", rt)
		}
		delete(got, rt)
	}
	var extra []string
	for rt := range got {
		extra = append(extra, rt)
	}
	sort.Strings(extra)
	if len(extra) > 0 {
		t.Errorf("routes refuse a hub session but are not in hubSessionRefused: %s", strings.Join(extra, ", "))
	}
}

// documentedRoute is a route as the documents' tables write it, in backticks
// and without the /api/v1 prefix.
var documentedRoute = regexp.MustCompile("`(GET|POST|PUT|PATCH|DELETE) (/[^`]*)`")

// routeTable reads the table that starts with the header line in the document
// at path, and returns every route its rows name, with the /api/v1 prefix.
func routeTable(t *testing.T, path, header string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	start := slices.Index(lines, header)
	if start < 0 {
		t.Fatalf("%s has no table headed %q", path, header)
	}
	var out []string
	for _, line := range lines[start+1:] {
		if !strings.HasPrefix(line, "|") {
			break
		}
		for _, m := range documentedRoute.FindAllStringSubmatch(line, -1) {
			out = append(out, m[1]+" /api/v1"+m[2])
		}
	}
	if len(out) == 0 {
		t.Fatalf("the table headed %q in %s names no route", header, path)
	}
	return out
}

// The route table in invariants.md and the one in protocol section 9.9 are
// what the router enforces, route for route: neither can drift from
// hubSessionRefused without this failing.
func TestTheDocumentedRouteTablesAreHubSessionRefused(t *testing.T) {
	want := slices.Clone(hubSessionRefused)
	slices.Sort(want)
	for _, doc := range []struct{ path, header string }{
		{"../../../docs/internal/security/invariants.md", "| What | Routes, under `/api/v1` |"},
		{"../../../docs/internal/backend/hub-link-protocol.md", "| What | Routes (JD 0.8.5) |"},
	} {
		got := routeTable(t, doc.path, doc.header)
		slices.Sort(got)
		for i := 1; i < len(got); i++ {
			if got[i] == got[i-1] {
				t.Errorf("%s lists %s twice", doc.path, got[i])
			}
		}
		got = slices.Compact(got)
		for _, rt := range want {
			if _, found := slices.BinarySearch(got, rt); !found {
				t.Errorf("%s does not list %s, which refuses a hub session", doc.path, rt)
			}
		}
		for _, rt := range got {
			if _, found := slices.BinarySearch(want, rt); !found {
				t.Errorf("%s lists %s, which does not refuse a hub session", doc.path, rt)
			}
		}
	}
}
