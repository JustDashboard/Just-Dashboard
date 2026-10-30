package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// A deploy key (auth.ScopeDeploy) is the credential the jd command line and a
// CI pipeline use. It deliberately changes one boundary: several deployment
// writes that were a session's alone — drafts, configuration, source, Git
// policy, triggers, variables and shared variables — answer it too, and so do
// the operating verbs beside a deploy: promoting staging (or a pull request's
// approved preview into staging), switching back to
// the warm release or keeping the new one, and the maintenance switch.
// Everything else holds:
//
//   - It reaches the routes deployKeyRoutes lists and nothing else, here or
//     anywhere in the API; a route added later is refused until it is given a
//     line below. Authenticate refuses the key on every other group.
//   - Variables are write-only to it: it lists names and digests, and a value
//     a route would hand back (a generated secret) is withheld. Reveal stays a
//     session's.
//   - Saved registry and Git credentials, the pull-request verbs that speak as
//     the dashboard's GitHub account, and the destructive actions that take a
//     typed phrase stay a session's.
//   - What it writes may not reach past a deployment's own container to the
//     host, nor send a saved credential somewhere new (deployKeyGuard), since
//     either would hand it what the session-only routes keep from it.
//   - Its writes are audited under the key's id and name (httpx.AuditMutations).
//
// A key may be narrowed to chosen deployments (api_token_deployments). It then
// reaches only routes about those, and none that touch what every deployment
// shares. Its drafts are its own (every deploy key's are: a draft records the
// key that made it, and the handlers hold a key to those), and a deployment
// one of its commits creates is added to its grant in the same transaction,
// so a narrowed key keeps reaching what it made.

// deployKeyRule is one route's answer to a deploy key. The zero rule admits
// the key; every narrows it to keys that reach every deployment; session and
// deny refuse it, with the reason in words.
type deployKeyRule struct {
	// every is a route that reaches what all deployments share, which a key
	// narrowed to some deployments may not.
	every bool
	// session is a route that stays an interactive session's whatever the
	// key: answered session_required.
	session string
	// deny is a route an ordinary key reaches and a deploy key does not:
	// answered key_scope.
	deny string
}

var (
	keyOpen  = deployKeyRule{}
	keyEvery = deployKeyRule{every: true}
)

func keySession(reason string) deployKeyRule { return deployKeyRule{session: reason} }
func keyDeny(reason string) deployKeyRule    { return deployKeyRule{deny: reason} }

const (
	whyReveal      = "variables are write-only to a deploy key: a value is read back only in the dashboard"
	whyCredential  = "saved registry and Git credentials are managed only in the dashboard"
	whyGitHub      = "merging and commenting speak on GitHub as the dashboard's own account"
	whyPhrase      = "this removal takes its typed confirmation in the dashboard"
	whyPreview     = "a pull request's preview runs its code, and a person approves that in the dashboard"
	whyChannels    = "notification channels and log drains hold credentials of their own"
	whyAutomations = "schedules and traffic alerts are set in the dashboard"
	whyHost        = "this reads a folder on the server, which a deploy key does not reach"
	whyGitHubApp   = "the GitHub App is the dashboard's own GitHub identity"
	whyLegacy      = "a legacy project's settings are commands that run on this server; use a draft and its environment instead"
	whyArchive     = "archiving a deployment is done in the dashboard or with an ordinary key"
	whyGame        = "operating a game server is not deploying it"
	whySecretStore = "secret store connections hold credentials of their own and are managed only in the dashboard"
	whyEnvironment = "adding or removing an environment is done in the dashboard"
	// The list of secret stores is an ordinary key's to read and a deploy
	// key's not: it carries each connection's address and sign-in fields and
	// which variables of every deployment read it, and the key cannot point a
	// variable at a store (keyReferenceGuard), so nothing in it is the key's
	// to use.
	whySecretStoreList = "the secret-store list carries each connection's address and sign-in fields and which variables of every deployment read it; a deploy key cannot point a variable at a store, so there is nothing in it for the key"
	whyPages           = "a route's maintenance and error pages are files on the server beside the proxy's route, and are edited in the dashboard"
	whyRollbackTerms   = "the terms a release is kept warm and rolled back on are set in the dashboard"
	whyRehearsalTerms  = "a migration rehearsal restores a database's backup, and which database and on what terms is chosen in the dashboard"
	whyOperator        = "the operator reads with a person's leave a call at a time and proposes actions only a person's press runs, in the dashboard"
	whyDataCopy        = "a copy of a release's data is a second copy of production's data, and its address carries a password: it is asked for, read and removed in the dashboard"
	whyPreviewBranch   = "what a database gives previews is a second copy of production's data next to a pull request's code: it is decided in the dashboard"
	whyScanTerms       = "whether an environment's images are scanned, and from which severity up, is set in the dashboard"
	whyDNSRecord       = "a domain's DNS record is read and written in a zone outside this server, with the DNS credentials saved under Certificates, in the dashboard"
	// A key reads the catalogue to choose from it, and makes drafts from a
	// template added here as from a built-in; adding to it is not a key's.
	whyTemplates = "a template is the operator's catalogue, which every deployment is made from, not a deployment a key is narrowed to; templates are added, changed and removed in the dashboard"
)

// deployKeyRoutes is the decision table: every route a deploy key may make,
// and every session-only deployment route it was decided to keep from one.
// Keys are the route's method and chi pattern under /api/v1. A route that is
// not here answers a deploy key with key_scope, so adding one later is a line
// here, and forgetting to is a refusal rather than a hole.
var deployKeyRoutes = map[string]deployKeyRule{
	// The fleet and the catalogue.
	"GET /deploy/":                                   keyOpen, // narrowed to the key's deployments by the handler
	"GET /deploy/hostname":                           keyOpen,
	"GET /deploy/openapi.json":                       keyOpen, // the description of this API, static
	"GET /deploy/openapi.yaml":                       keyOpen,
	"GET /deploy/blueprints/":                        keyOpen,
	"GET /deploy/blueprints/{id}":                    keyOpen,
	"GET /deploy/blueprints/{id}/versions":           keyOpen,
	"GET /deploy/templates":                          keyOpen,
	"GET /deploy/templates/{template}":               keyOpen,
	"GET /deploy/templates/{template}/export":        keyOpen,
	"GET /deploy/pull-requests":                      keyEvery,
	"GET /deploy/traffic":                            keyEvery,
	"GET /deploy/shared-variables":                   keyEvery,
	"GET /deploy/notifications":                      keyEvery,
	"GET /deploy/notifications/options":              keyEvery,
	"GET /deploy/notifications/{channel}/deliveries": keyEvery,
	"GET /deploy/log-drains":                         keyEvery,
	// Right-sizing is a read of the deployments' own container history;
	// the list is narrowed to the key's deployments by the handler, and
	// applying a suggestion is the configuration PUT below.
	"GET /deploy/sizing": keyOpen,

	// One deployment, read: what an ordinary key reads, less the legacy list
	// that prints part of each value.
	"GET /deploy/{id}":                                                  keyOpen,
	"GET /deploy/{id}/pull-requests":                                    keyOpen,
	"GET /deploy/{id}/pull-requests/{number}/checks":                    keyOpen,
	"GET /deploy/{id}/preview-frame":                                    keyOpen,
	"GET /deploy/{id}/favicon":                                          keyOpen,
	"GET /deploy/{id}/runs":                                             keyOpen,
	"GET /deploy/{id}/runs/{run}":                                       keyOpen,
	"GET /deploy/{id}/runs/{run}/logs":                                  keyOpen,
	"GET /deploy/{id}/runs/{run}/transcript":                            keyOpen,
	"GET /deploy/{id}/runs/{run}/metrics":                               keyOpen,
	"GET /deploy/{id}/runs/{run}/stream":                                keyOpen,
	"GET /deploy/{id}/runs/{run}/settings-drift":                        keyOpen,
	"GET /deploy/{id}/runs/{run}/diagnosis":                             keyOpen,
	"GET /deploy/{id}/runs/{run}/traffic":                               keyOpen,
	"GET /deploy/{id}/requests":                                         keyOpen,
	"GET /deploy/{id}/requests/stream":                                  keyOpen,
	"GET /deploy/{id}/requests/export":                                  keyOpen,
	"GET /deploy/{id}/lifecycle":                                        keyOpen,
	"GET /deploy/{id}/lifecycle/stream":                                 keyOpen,
	"GET /deploy/{id}/availability":                                     keyOpen,
	"GET /deploy/{id}/alerts":                                           keyOpen,
	"GET /deploy/{id}/commits":                                          keyOpen,
	"GET /deploy/{id}/operations":                                       keyOpen,
	"GET /deploy/{id}/insights":                                         keyOpen,
	"GET /deploy/{id}/previews":                                         keyOpen,
	"GET /deploy/{id}/previews/approvals":                               keyOpen,
	"GET /deploy/{id}/shared-variables":                                 keyOpen,
	"GET /deploy/{id}/game/":                                            keyOpen,
	"GET /deploy/{id}/game/players":                                     keyOpen,
	"GET /deploy/{id}/game/properties":                                  keyOpen,
	"GET /deploy/{id}/environments":                                     keyOpen,
	"GET /deploy/{id}/environments/{env}/promotion":                     keyOpen,
	"GET /deploy/{id}/environments/{env}/rollback-watch":                keyOpen,
	"GET /deploy/{id}/environments/{env}/canary":                        keyOpen,
	"GET /deploy/{id}/environments/{env}/maintenance":                   keyOpen,
	"GET /deploy/{id}/environments/{env}/sleep":                         keyOpen,
	"GET /deploy/{id}/environments/{env}/pages/{page}":                  keyOpen, // what every visitor is shown
	"GET /deploy/{id}/environments/{env}/rehearsal":                     keyOpen, // the terms; the last report is the run's
	"GET /deploy/{id}/environments/{env}/vulnerabilities":               keyOpen, // the latest scan and the terms
	"GET /deploy/{id}/environments/{env}/releases":                      keyOpen,
	"GET /deploy/{id}/environments/{env}/releases/{release}/comparison": keyOpen,
	"GET /deploy/{id}/environments/{env}/variables":                     keyOpen, // names and digests, never values
	"GET /deploy/{id}/environments/{env}/pending":                       keyOpen,
	"GET /deploy/{id}/environments/{env}/configuration":                 keyOpen,
	"GET /deploy/{id}/environments/{env}/sizing":                        keyOpen,
	"GET /deploy/{id}/environments/{env}/triggers":                      keyOpen,
	"GET /deploy/{id}/environments/{env}/git-watch":                     keyOpen,
	"GET /deploy/{id}/environments/{env}/database-links":                keyOpen,
	"GET /deploy/{id}/environments/{env}/data-copies":                   keyOpen, // what each copy is of and until when, never its sign-in
	"GET /deploy/{id}/environments/{env}/database-addresses":            keyOpen,
	"GET /deploy/{id}/environments/{env}/schedules":                     keyOpen,
	"GET /deploy/{id}/environments/{env}/schedules/{schedule}/runs":     keyOpen,
	"GET /deploy/{id}/env":                                              keyDeny("the legacy variable list prints the first and last characters of each value, and a deploy key reads names and digests only; use the environment's variables"),

	// Running: what an ordinary key may already do.
	"POST /deploy/{id}/run":                         keyOpen,
	"POST /deploy/{id}/environments/{env}/runs":     keyOpen,
	"POST /deploy/{id}/runs/{run}/cancel":           keyOpen,
	"POST /deploy/{id}/runs/{run}/retry":            keyOpen,
	"POST /deploy/{id}/runs/{run}/diagnosis":        keyOpen,
	"POST /deploy/{id}/rollback":                    keyOpen,
	"POST /deploy/{id}/environments/{env}/rollback": keyOpen,
	// Promoting is deploying production with staging's image, or staging
	// with a pull request preview's: the same capability, queue, checks and
	// cutover as a deploy. A preview's takes the administrator's role, which
	// a deploy key carries; approving the commit it was built from stays a
	// person's in the dashboard (whyPreview), and promotion takes only an
	// approved one.
	"POST /deploy/{id}/environments/{env}/promote": keyOpen,
	// The warm release's window. Switching traffic back to it is a rollback,
	// destructive as the one from the release list is, and the key spends
	// the destructive budget for it the same way; keeping the new release
	// retires a container nothing routes to.
	"POST /deploy/{id}/environments/{env}/rollback-watch/rollback": keyOpen,
	"POST /deploy/{id}/environments/{env}/rollback-watch/keep":     keyOpen,
	// A canary's decision is the cutover a deploy makes, or not making it:
	// a key that may deploy and promote may promote or abort one.
	"POST /deploy/{id}/environments/{env}/canary/promote": keyOpen,
	"POST /deploy/{id}/environments/{env}/canary/abort":   keyOpen,
	// The maintenance switch takes the application from its visitors as
	// stopping it does, and is behind the same gate. Who is let past is the
	// configuration's (runtime.maintenance.bypassFrom), which the key writes
	// as any configuration, and reaches the route with the next release.
	"PUT /deploy/{id}/environments/{env}/maintenance": keyOpen,
	// Waking a sleeping environment starts its live container, which any
	// visitor's request does and Start does.
	"POST /deploy/{id}/environments/{env}/sleep/wake": keyOpen,
	// A scan reads what is already deployed and changes none of it.
	"POST /deploy/{id}/environments/{env}/vulnerabilities/scan": keyOpen,

	// Creating a deployment: session-only until a deploy key. A key's drafts
	// are its own — the draft records the key, and the handlers hold a key to
	// the drafts it made — so a key narrowed to some deployments makes them
	// too, and the deployment its commit creates is added to its grant in the
	// same transaction (DraftCommitRequest.GrantToKey). Duplicating takes a
	// deployment the key reaches and leaves a draft that is the key's.
	"GET /deploy/drafts":                    keyOpen,
	"POST /deploy/drafts":                   keyOpen,
	"GET /deploy/drafts/{draft}":            keyOpen,
	"PUT /deploy/drafts/{draft}":            keyOpen,
	"DELETE /deploy/drafts/{draft}":         keyOpen,
	"POST /deploy/drafts/{draft}/detect":    keyOpen,
	"POST /deploy/drafts/{draft}/preflight": keyOpen,
	"POST /deploy/drafts/{draft}/commit":    keyOpen,
	"POST /deploy/{id}/duplicate":           keyOpen,

	// Changing a deployment: session-only until a deploy key.
	"PUT /deploy/{id}/environments/{env}/configuration":            keyOpen,
	"PUT /deploy/{id}/environments/{env}/source":                   keyOpen,
	"POST /deploy/{id}/environments/{env}/detect":                  keyOpen,
	"PUT /deploy/{id}/environments/{env}/git-policy":               keyOpen,
	"POST /deploy/{id}/environments/{env}/triggers":                keyOpen,
	"PUT /deploy/{id}/environments/{env}/triggers/{trigger}":       keyOpen,
	"DELETE /deploy/{id}/environments/{env}/triggers/{trigger}":    keyOpen,
	"POST /deploy/{id}/environments/{env}/triggers/{trigger}/test": keyOpen,
	// A trigger's secret only lets a sender deploy this environment, which the
	// key may do itself, and deleting and remaking the trigger shows a new one
	// anyway: refusing the rotation would only stop a pipeline recovering
	// from its own leaked hook secret without a person.
	"POST /deploy/{id}/environments/{env}/triggers/{trigger}/rotate-secret": keyOpen,
	"GET /deploy/{id}/environments/{env}/triggers/{trigger}/deliveries":     keyOpen,
	"POST /deploy/{id}/environments/{env}/watch-paths/simulate":             keyOpen,
	"PUT /deploy/{id}/environments/{env}/variables/{name}":                  keyOpen,
	"DELETE /deploy/{id}/environments/{env}/variables/{name}":               keyOpen,
	"POST /deploy/{id}/environments/{env}/variables/import":                 keyOpen,
	"POST /deploy/{id}/environments/{env}/variables/{name}/generate":        keyOpen,
	"POST /deploy/{id}/environments/{env}/variables/{name}/rotate":          keyOpen,
	"PUT /deploy/{id}/shared-variables/{name}":                              keyOpen,
	"DELETE /deploy/{id}/shared-variables/{name}":                           keyOpen,
	"PUT /deploy/shared-variables/{name}":                                   keyEvery,
	"DELETE /deploy/shared-variables/{name}":                                keyEvery,

	// Session-only, whatever the key.
	"GET /deploy/{id}/environments/{env}/variables/{name}/reveal": keySession(whyReveal),
	"GET /deploy/shared-variables/{name}/reveal":                  keySession(whyReveal),
	"GET /deploy/{id}/shared-variables/{name}/reveal":             keySession(whyReveal),
	"GET /deploy/{id}/env/reveal":                                 keySession(whyReveal),
	"GET /deploy/credentials":                                     keySession(whyCredential),
	"POST /deploy/credentials":                                    keySession(whyCredential),
	"PUT /deploy/credentials/{id}":                                keySession(whyCredential),
	"POST /deploy/credentials/{id}/test":                          keySession(whyCredential),
	"DELETE /deploy/credentials/{id}":                             keySession(whyCredential),
	"POST /deploy/{id}/pull-requests/{number}/merge":              keySession(whyGitHub),
	"POST /deploy/{id}/pull-requests/{number}/comment":            keySession(whyGitHub),
	"DELETE /deploy/{id}/permanent":                               keySession(whyPhrase),
	"POST /deploy/{id}/remove-managed":                            keySession(whyPhrase),
	"POST /deploy/{id}/removal-plan":                              keySession(whyPhrase),

	// Session-only, and kept so: none of these is shipping or configuring a
	// deployment, and each opens with a line here if it ever should be.
	"POST /deploy/{id}/previews/approvals/{approval}/approve":                        keySession(whyPreview),
	"POST /deploy/{id}/previews/approvals/{approval}/reject":                         keySession(whyPreview),
	"POST /deploy/{id}/pull-requests/{number}/preview":                               keySession(whyPreview),
	"POST /deploy/{id}/pull-requests/{number}/preview/close":                         keySession(whyPreview),
	"POST /deploy/{id}/pull-requests/{number}/preview/branches/{connection}/refresh": keySession(whyPreview),
	"PUT /deploy/{id}/environments/{env}/database-links/{connection}/previews":       keySession(whyPreviewBranch),
	"PUT /deploy/{id}/environments/{env}/releases/{release}/pin":                     keySession("pinning a release is done in the dashboard"),
	"POST /deploy/{id}/unarchive":                                                    keySession("restoring an archived deployment is done in the dashboard"),
	"POST /deploy/{id}/environments/{env}/schedules":                                 keySession(whyAutomations),
	"POST /deploy/{id}/environments/{env}/schedules/test":                            keySession(whyAutomations),
	"PUT /deploy/{id}/environments/{env}/schedules/{schedule}":                       keySession(whyAutomations),
	"DELETE /deploy/{id}/environments/{env}/schedules/{schedule}":                    keySession(whyAutomations),
	"POST /deploy/{id}/alerts":                                                       keySession(whyAutomations),
	"PUT /deploy/{id}/alerts/{alert}":                                                keySession(whyAutomations),
	"DELETE /deploy/{id}/alerts/{alert}":                                             keySession(whyAutomations),
	"POST /deploy/{id}/alerts/{alert}/test":                                          keySession(whyAutomations),
	"POST /deploy/notifications":                                                     keySession(whyChannels),
	"PUT /deploy/notifications/{channel}":                                            keySession(whyChannels),
	"POST /deploy/notifications/{channel}/test":                                      keySession(whyChannels),
	"PUT /deploy/notifications/{channel}/enabled":                                    keySession(whyChannels),
	"DELETE /deploy/notifications/{channel}":                                         keySession(whyChannels),
	"POST /deploy/log-drains":                                                        keySession(whyChannels),
	"PUT /deploy/log-drains/{drain}":                                                 keySession(whyChannels),
	"PUT /deploy/log-drains/{drain}/enabled":                                         keySession(whyChannels),
	"POST /deploy/log-drains/{drain}/test":                                           keySession(whyChannels),
	"DELETE /deploy/log-drains/{drain}":                                              keySession(whyChannels),
	"POST /deploy/secret-stores":                                                     keySession(whySecretStore),
	"PUT /deploy/secret-stores/{store}":                                              keySession(whySecretStore),
	"POST /deploy/secret-stores/{store}/test":                                        keySession(whySecretStore),
	"DELETE /deploy/secret-stores/{store}":                                           keySession(whySecretStore),
	"POST /deploy/{id}/environments":                                                 keySession(whyEnvironment),
	"POST /deploy/{id}/environments/{env}/releases/{release}/data-copies":            keySession(whyDataCopy),
	"GET /deploy/{id}/environments/{env}/data-copies/{copy}/url":                     keySession(whyDataCopy),
	"POST /deploy/{id}/environments/{env}/data-copies/{copy}/extend":                 keySession(whyDataCopy),
	"DELETE /deploy/{id}/environments/{env}/data-copies/{copy}":                      keySession(whyDataCopy),
	"GET /deploy/{id}/environments/{env}/data-copies/{copy}/comparison":              keySession(whyDataCopy),
	"DELETE /deploy/{id}/environments/{env}":                                         keySession(whyEnvironment),
	"PUT /deploy/{id}/environments/{env}/pages/{page}":                               keySession(whyPages),
	"PUT /deploy/{id}/environments/{env}/rollback-policy":                            keySession(whyRollbackTerms),
	"PUT /deploy/{id}/environments/{env}/rehearsal":                                  keySession(whyRehearsalTerms),
	"PUT /deploy/{id}/environments/{env}/vulnerabilities":                            keySession(whyScanTerms),
	"GET /deploy/{id}/domains/{domain}/dns-record":                                   keySession(whyDNSRecord),
	"POST /deploy/{id}/domains/{domain}/dns-record":                                  keySession(whyDNSRecord),
	"POST /deploy/blueprints/{id}/render":                                            keySession("a template's plan is previewed in the dashboard; a draft from it is a deploy key's to make"),
	"POST /deploy/templates":                                                         keySession(whyTemplates),
	"POST /deploy/templates/from-deployment":                                         keySession(whyTemplates),
	"POST /deploy/templates/import":                                                  keySession(whyTemplates),
	"PUT /deploy/templates/{template}":                                               keySession(whyTemplates),
	"DELETE /deploy/templates/{template}":                                            keySession(whyTemplates),
	"POST /deploy/import/preview":                                                    keySession(whyHost),
	"POST /deploy/import/adopt":                                                      keySession(whyHost),
	"POST /deploy/game/import/preview":                                               keySession(whyHost),
	"PUT /deploy/{id}/game/properties":                                               keySession(whyGame),
	"GET /deploy/github-app/":                                                        keySession(whyGitHubApp),
	"GET /deploy/github-app/repositories":                                            keySession(whyGitHubApp),
	"POST /deploy/github-app/manifest":                                               keySession(whyGitHubApp),
	"POST /deploy/github-app/callback":                                               keySession(whyGitHubApp),
	"DELETE /deploy/github-app/":                                                     keySession(whyGitHubApp),
	"POST /deploy/{id}/assistant":                                                    keySession(whyOperator),

	// What an ordinary key reaches and a deploy key does not.
	"GET /deploy/secret-stores":               keyDeny(whySecretStoreList),
	"POST /deploy/":                           keyDeny(whyLegacy),
	"PUT /deploy/{id}":                        keyDeny(whyLegacy),
	"POST /deploy/{id}/rotate-secret":         keyDeny(whyLegacy),
	"PUT /deploy/{id}/env":                    keyDeny(whyLegacy),
	"DELETE /deploy/{id}/env/{key}":           keyDeny(whyLegacy),
	"DELETE /deploy/{id}":                     keyDeny(whyArchive),
	"POST /deploy/{id}/archive":               keyDeny(whyArchive),
	"POST /deploy/{id}/game/console":          keyDeny(whyGame),
	"POST /deploy/{id}/game/players/{action}": keyDeny(whyGame),
}

// deployKeyRules is deployKeyRoutes keyed without a trailing slash. A
// router's mount answers its root with and without one — /deploy and
// /deploy/ are the one list route — and Find names the two differently, so
// both are looked up as the one key. No two routes of the API differ by a
// trailing slash alone (TestNoTwoRoutesDifferByATrailingSlash), so nothing
// else is folded together.
var deployKeyRules = func() map[string]deployKeyRule {
	out := make(map[string]deployKeyRule, len(deployKeyRoutes))
	for route, rule := range deployKeyRoutes {
		out[strings.TrimSuffix(route, "/")] = rule
	}
	return out
}()

// ruleForDeployKey is the table's line for a route, by its method and its chi
// pattern under /api/v1.
func ruleForDeployKey(method, pattern string) (deployKeyRule, bool) {
	rule, listed := deployKeyRules[method+" "+strings.TrimSuffix(pattern, "/")]
	return rule, listed
}

// deployKeyGate is the feature group's answer to a deploy key, asked before
// any other middleware of the route runs. It finds the route the router will
// choose — by the router's own Find, so a static route and a parameter one can
// never be confused — and answers it from deployKeyRoutes, then from the key's
// deployments.
func (s *Server) deployKeyGate(root chi.Routes) httpx.DeployKeyGate {
	return func(r *http.Request, p *httpx.Principal) error {
		rctx := chi.NewRouteContext()
		path := r.URL.RawPath
		if path == "" {
			path = r.URL.Path
		}
		pattern := strings.TrimPrefix(root.Find(rctx, r.Method, path), "/api/v1")
		rule, listed := ruleForDeployKey(r.Method, pattern)
		switch {
		case !listed:
			return httpx.Err(http.StatusForbidden, "key_scope",
				"this is a deploy key: it reaches the deployment routes under /api/v1/deploy that it was made for, and not this one")
		case rule.session != "":
			return httpx.Err(http.StatusForbidden, "session_required",
				rule.session+"; it needs an interactive session, not an API key")
		case rule.deny != "":
			return httpx.Err(http.StatusForbidden, "key_scope", "a deploy key cannot do this: "+rule.deny)
		}
		token := p.Token
		if token.EveryDeployment() {
			return nil
		}
		if rule.every {
			return httpx.Err(http.StatusForbidden, "key_scope",
				"this deploy key reaches only the deployments chosen for it, and this needs a key that reaches every deployment")
		}
		if !strings.HasPrefix(pattern, "/deploy/{id}") {
			return nil
		}
		refused := httpx.Err(http.StatusForbidden, "key_scope", "this deploy key was not given this deployment")
		projectID, err := strconv.ParseInt(rctx.URLParam("id"), 10, 64)
		if err != nil || !token.ReachesDeployment(projectID) {
			return refused
		}
		// The handlers check that an environment is the deployment's, but a
		// key narrowed to one deployment is held to it here as well, so no
		// handler's oversight lets it reach another's.
		if raw := rctx.URLParam("env"); raw != "" {
			environmentID, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				return refused
			}
			var one int
			err = s.Store.DB.QueryRowContext(r.Context(),
				`SELECT 1 FROM deploy_environments WHERE id = ? AND project_id = ?`, environmentID, projectID).Scan(&one)
			if errors.Is(err, sql.ErrNoRows) {
				return refused
			}
			if err != nil {
				return httpx.Internal(err)
			}
		}
		return nil
	}
}

// keyReaches narrows a list to what a narrowed deploy key reaches. Every other
// principal, and a key reaching every deployment, reads the list as it is.
func keyReaches(p *httpx.Principal, projectID int64) bool {
	return !p.DeployKey() || p.Token.ReachesDeployment(projectID)
}

// refuseForDeployKey is the answer to a write a deploy key may not make on a
// route it otherwise reaches: the action is a person's, in the dashboard.
func refuseForDeployKey(reason string) error {
	return httpx.Err(http.StatusForbidden, "session_required",
		"a deploy key cannot do this: "+reason+"; make this change in the dashboard")
}

// hostAuthority lists what in a deployment's plan reaches past its own
// container to the server itself: a folder on the server as the source,
// Docker Compose (whose file can ask for any of the rest, and is only
// reviewed when a person commits it), a privileged container, the host's
// network, added capabilities, devices, the server's GPUs, a host path
// mounted in, and a release task that runs in a shell on the host. Any of these is the server, and with
// it every secret the session-only routes keep from a deploy key.
func hostAuthority(source *deploy.DraftSourceConfig, build deploy.BuildPlanConfig, runtime deploy.RuntimePlanConfig) []string {
	var out []string
	if source != nil {
		switch {
		case source.LocalPath != "", source.Kind == deploy.SourceLocal, source.Kind == deploy.SourceImport:
			out = append(out, "a folder on this server as its source")
		case source.Kind == deploy.SourceCompose:
			out = append(out, "Docker Compose")
		}
	}
	if (build.Method == deploy.BuildCompose || build.Method == deploy.BuildLegacyCompose) &&
		(source == nil || source.Kind != deploy.SourceCompose) {
		out = append(out, "Docker Compose")
	}
	if runtime.Privileged {
		out = append(out, "a privileged container")
	}
	if runtime.HostNetwork {
		out = append(out, "the host's network")
	}
	if len(runtime.Capabilities) > 0 {
		out = append(out, "added Linux capabilities")
	}
	if len(runtime.Devices) > 0 {
		out = append(out, "host devices")
	}
	if runtime.GPUs != nil {
		out = append(out, "the server's GPUs")
	}
	for _, mount := range runtime.Mounts {
		if filepath.IsAbs(strings.TrimSpace(mount.Source)) {
			out = append(out, "the host path "+strings.TrimSpace(mount.Source))
		}
	}
	for _, task := range build.ReleaseTasks {
		if task.Runner != deploy.ReleaseTaskRunnerImage {
			out = append(out, "the release task "+strconv.Quote(task.Name)+", which runs on the host")
		}
	}
	return out
}

// refuseHostAuthority is the refusal for a plan with host authority.
func refuseHostAuthority(what string, reasons []string) error {
	return refuseForDeployKey(what + " uses " + strings.Join(reasons, ", ") +
		", which reaches the server itself")
}

// keySourceGuard holds a source a deploy key writes to the one it replaces
// (previous is nil for a draft with none yet). The key may not choose a saved
// credential, nor send the one a source already has anywhere new: a Git
// credential goes, as an HTTP header, to whichever host the source names, and
// pointing it at another host would be reading it. A registry credential is
// held to its own registry by the dashboard (registryAuth), so an image may
// change under one. A folder on the server is refused by hostAuthority.
func keySourceGuard(next deploy.DraftSourceConfig, previous *deploy.DraftSourceConfig) error {
	if next.CredentialID == 0 {
		return nil
	}
	if previous == nil || previous.CredentialID != next.CredentialID {
		return refuseForDeployKey("a deploy key cannot attach a saved credential to a source")
	}
	if next.Kind == deploy.SourceImage && previous.Kind == deploy.SourceImage {
		return nil
	}
	if next.Kind != previous.Kind || next.URL != previous.URL || next.Repository != previous.Repository ||
		next.Provider != previous.Provider || next.ProviderBaseURL != previous.ProviderBaseURL {
		return refuseForDeployKey("a deploy key cannot send a source's saved credential to a different repository")
	}
	return nil
}

// keyReferenceGuard holds a variable reference a deploy key writes. A
// reference to a saved credential or a database is the value of that
// credential or connection string in the container, which the key deploys
// code into, so the key may keep one a person set and never point a variable
// at one itself. A secret-store reference is the same: the store's saved
// connection reads any path it can reach, not only the ones a person gave
// this deployment. A key narrowed to some deployments may not reach a server
// shared variable it was not already reading for the same reason: it is the
// other deployments' value. previous is the reference the variable holds now.
func keyReferenceGuard(p *httpx.Principal, name, reference, previous string) error {
	if reference == "" || reference == previous {
		return nil
	}
	parsed, err := deploy.ParseVariableReference(reference)
	if err != nil {
		return nil // the write refuses it on its own terms
	}
	switch {
	case parsed.Kind == "credential" || parsed.Kind == "database":
		return refuseForDeployKey(fmt.Sprintf("%s would read a saved %s, and a deploy key cannot point a variable at one", name, parsed.Kind))
	case parsed.Kind == deploy.StoreReferenceKind:
		return refuseForDeployKey(fmt.Sprintf("%s would read a secret store through its saved connection, and a deploy key cannot point a variable at one", name))
	case parsed.Kind == deploy.SharedScopeServer && !p.Token.EveryDeployment():
		return refuseForDeployKey(fmt.Sprintf("%s would read a server shared variable, and this deploy key reaches only some deployments", name))
	}
	return nil
}

// variableReference is the reference a deployment variable holds as the
// planner spells it, or "" for a literal.
func variableReference(v deploy.DeploymentVariable) string {
	if v.Reference == nil {
		return ""
	}
	return "${{" + v.Reference.Kind + "." + v.Reference.Target + "}}"
}

// keyEnvironmentGuard refuses a deploy key's change to an environment whose
// plan already has host authority, or whose variables a person pointed at a
// saved credential: the authority, or the credential, was given to what runs
// there now, and a new source or a new command would carry it to code the
// key chose. The key may still deploy what is there and set its variables.
func (s *Server) keyEnvironmentGuard(ctx context.Context, projectID, environmentID int64) (*deploy.EnvironmentConfiguration, error) {
	current, err := s.modules.deployPlanning.EnvironmentConfiguration(ctx, projectID, environmentID)
	if err != nil {
		return nil, mapDeploymentPlanningError(err)
	}
	if reasons := hostAuthority(current.Source, current.Build, current.Runtime); len(reasons) > 0 {
		return nil, refuseHostAuthority("this deployment", reasons)
	}
	for _, variable := range current.Variables {
		if variable.Reference != nil && variable.Reference.Kind == "credential" {
			return nil, refuseForDeployKey(fmt.Sprintf("this deployment's %s reads a saved credential, and a deploy key cannot change what code receives it", variable.Name))
		}
	}
	return current, nil
}

type deployTokenRequest struct {
	Name    string `json:"name"`
	TTLDays int    `json:"ttlDays"`
	// DeploymentIDs narrows the key to those deployments; empty is every one.
	DeploymentIDs []int64 `json:"deploymentIds"`
}

// handleCreateDeployToken mints a deploy key. It sits behind RequireSession
// and system.admin: a key is made by a person, and a deploy key holds the
// administrator role its writes ask for.
func (s *Server) handleCreateDeployToken(w http.ResponseWriter, r *http.Request) error {
	var req deployTokenRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	names := make([]string, 0, len(req.DeploymentIDs))
	for _, id := range req.DeploymentIDs {
		project, err := s.modules.deployStore.Get(r.Context(), id)
		if errors.Is(err, deploy.ErrNotFound) {
			return httpx.BadRequest("deployment %d does not exist", id)
		}
		if err != nil {
			return httpx.Internal(err)
		}
		if project.ArchivedAt != nil {
			return httpx.Err(http.StatusConflict, "project_archived", project.Name+" is archived")
		}
		names = append(names, project.Name)
	}
	sort.Strings(names)
	var ttl time.Duration
	if req.TTLDays > 0 {
		ttl = time.Duration(req.TTLDays) * 24 * time.Hour
	}
	tok, secret, err := s.Auth.CreateDeployToken(r.Context(), httpx.MustPrincipal(r).User, req.Name, ttl, req.DeploymentIDs)
	if errors.Is(err, auth.ErrDeployKeyOwner) {
		return httpx.Err(http.StatusForbidden, "forbidden", err.Error())
	}
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	detail := map[string]any{"role": tok.Role, "scope": tok.Scope, "ttlDays": req.TTLDays, "deployments": "every"}
	if len(names) > 0 {
		detail["deployments"] = names
	}
	httpx.SetAudit(r, "dashboard.token.create", tok.Name, detail)
	// The secret is returned exactly once; only its hash is retained.
	httpx.JSON(w, http.StatusCreated, map[string]any{"token": tok, "secret": secret})
	return nil
}
