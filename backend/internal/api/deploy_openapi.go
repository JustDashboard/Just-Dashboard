package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/blueprint"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/gameserver"
	"github.com/Wayy01/Just-Dashboard/backend/internal/ghx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/githubapp"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/logdrain"
	"github.com/Wayy01/Just-Dashboard/backend/internal/metrics"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/Wayy01/Just-Dashboard/backend/internal/secretstore"
	"github.com/Wayy01/Just-Dashboard/backend/internal/version"
	"github.com/go-chi/chi/v5"
)

// The deployment API's OpenAPI 3.1 description, served at
// GET /deploy/openapi.json and /deploy/openapi.yaml and committed as
// docs/api/deploy-openapi.json for client generators.
//
// It is generated, not written: the routes come from the router itself
// (chi.Walk over what Routes mounted), the request and response schemas from
// the Go types the handlers decode and encode (deploy_openapi_schema.go), and
// an operation's security from the same two places the gate reads — whether
// httpx.RequireSession is in the route's middleware chain, and the route's
// line in deployKeyRoutes — so the description cannot say a key reaches a
// route the gate refuses. What a person has to say is in deployOperations: one
// sentence per route, its tag, the types it takes and returns, and the shape
// of an answer a handler assembles by hand. A route without a line there, or a
// line without a route, fails TestEveryDeployRouteIsDescribed, the way a
// session-only route without a deploy key decision does.

// deployOperation is what the generator cannot read off the code for one
// route: its summary and tag, and which Go values its body and its successful
// answer are.
type deployOperation struct {
	Summary string
	Tag     string
	// Request is a zero value of the type the handler decodes; nil when the
	// route takes no body. Optional marks a body the handler reads only when
	// one is sent.
	Request  any
	Optional bool
	// Upload is a second media type the route accepts as a raw body.
	Upload string
	// Response is a zero value of what a successful answer encodes, a shape
	// for an answer assembled in a map, or an anyOf of alternatives; nil when
	// the answer has no body (204) or is not JSON (Content, Stream).
	Response any
	// Status is the successful status; 0 means 200.
	Status int
	// Content is the successful answer's media type when it is not JSON.
	Content string
	// Stream marks a WebSocket route: the answer is the upgrade.
	Stream bool
	Query  []openapiQuery
	// Note is read after the summary: what the summary has no room for.
	Note string
}

type openapiQuery struct{ Name, Type, Description string }

// shape is an answer a handler assembles in a map: each key with a zero value
// of what it holds, or another shape.
type shape map[string]any

// anyOf is an answer that is one of several documents, chosen by the request.
// It is anyOf rather than oneOf in the description: with no required lists,
// an object or an empty list matches more than one alternative, which oneOf
// would refuse.
type anyOf []any

// openapiDocument is the generated description, ready to serve.
type openapiDocument struct {
	Document map[string]any
	JSON     []byte
	YAML     []byte
	// Problems is every disagreement between the router and deployOperations:
	// a route with no operation, or an operation naming no route. The tests
	// require none; the served document carries what it could describe.
	Problems []string
}

// The tags, in the order a reader meets them.
var openapiTags = []struct{ Name, Description string }{
	{"fleet", "Every deployment: the list, one deployment's record, its runs and readings, archiving and removal, and the legacy project routes."},
	{"drafts", "How a deployment is created: a revisioned draft that is detected, configured, preflighted and committed."},
	{"environments", "An environment's configuration, source, Git policy, pending changes and rehearsal terms; staging beside production."},
	{"variables", "An environment's variables and the shared variables deployments read. Lists carry names and digests; a value is read back only by the audited reveal, in a session."},
	{"triggers", "What starts a run without a person: hooks and provider webhooks, their deliveries, schedules and the watch-path filter."},
	{"releases", "Retained releases, their comparison, pinning, rollback, the warm release kept after a deploy, and promotion from staging."},
	{"runs", "Runs and their steps, logs, transcript, metrics, settings drift, traffic and diagnosis."},
	{"previews", "Pull requests, their previews and the approvals a preview's revision needs."},
	{"domains", "What a deployment's public route serves: the maintenance switch and pages, hostname suggestions, the requests and traffic it saw."},
	{"database links", "The databases an environment's variables read and the addresses it reaches them at."},
	{"log drains", "Where a deployment's output and requests are copied to."},
	{"notifications", "Notification channels, their deliveries, and a deployment's traffic alerts."},
	{"secret stores", "Connections to Vault, OpenBao, Infisical and Doppler that variables read through."},
	{"github-app", "The dashboard's own GitHub App: its status, repositories, manifest flow and removal."},
	{"blueprints", "The template catalogue and the plan a template renders to."},
	{"game", "Operating a deployed game server: players, console, properties and importing an existing one."},
	{"keys", "What the holder of a key reads to use this API: this description."},
}

// openapiEnums is every closed vocabulary a field holds, by its Go type. The
// values are the type's constants; TestOpenAPIEnumsMatchTheConstants reads
// the source and fails when a constant is added or a type is left out.
var openapiEnums = map[reflect.Type][]string{
	reflect.TypeOf(deploy.WorkloadProfile("")):     {"web", "static", "worker", "image", "compose", "service", "game", "imported"},
	reflect.TypeOf(deploy.EnvironmentKind("")):     {"production", "staging", "preview"},
	reflect.TypeOf(deploy.OwnershipMode("")):       {"managed", "linked", "observed"},
	reflect.TypeOf(deploy.SourceKind("")):          {"git", "local", "image", "compose", "blueprint", "import"},
	reflect.TypeOf(deploy.BuildMethod("")):         {"recipe", "dockerfile", "static", "image", "compose", "none", "legacy_compose"},
	reflect.TypeOf(deploy.ReleaseStrategy("")):     {"blue_green", "stop_first", "canary"},
	reflect.TypeOf(deploy.Operation("")):           {"deploy", "redeploy", "restart", "stop", "start", "force_build", "rollback", "promote", "preview_create", "preview_update", "preview_remove", "scheduled_action", "import_adopt", "remove_managed"},
	reflect.TypeOf(deploy.TriggerKind("")):         {"manual", "git_push", "legacy_hook", "generic_hook", "github", "gitlab", "bitbucket", "gitea", "api", "schedule", "preview", "rollback", "migration", "auto_rollback"},
	reflect.TypeOf(deploy.RunState("")):            {"requested", "validating", "queued", "preparing", "running", "verifying", "activating", "failed_activation", "restoring_previous", "cancelling", "succeeded", "failed", "cancelled", "rolled_back", "superseded"},
	reflect.TypeOf(deploy.StepKey("")):             {"resolve_source", "acquire_source", "analyze_plan", "prepare_context", "build_artifact", "render_runtime", "release_task", "backup_gate", "rehearse_migration", "provision_certificate", "start_candidate", "verify_readiness", "verify_smoke", "replay_traffic", "canary", "activate", "retire_previous", "record_release", "notify", "legacy_pipeline"},
	reflect.TypeOf(metrics.Verdict("")):            {"too_tight", "oversized", "unlimited", "right", "insufficient_history"},
	reflect.TypeOf(deploy.StepState("")):           {"pending", "blocked", "running", "passed", "warning", "failed", "skipped", "cancelled", "unavailable"},
	reflect.TypeOf(deploy.CheckKind("")):           {"http", "tcp", "docker_health", "command", "public_route", "dns", "tls", "game_handshake", "backup_freshness"},
	reflect.TypeOf(deploy.EventType("")):           {"run.state", "step.state", "step.log", "step.evidence", "queue.position", "finding", "artifact", "heartbeat", "resync"},
	reflect.TypeOf(deploy.PreflightSeverity("")):   {"pass", "decision", "warning", "blocked", "unavailable"},
	reflect.TypeOf(deploy.HealthOutcome("")):       {"disabled", "unavailable", "warning", "passed", "failed"},
	reflect.TypeOf(deploy.ReconcileAction("")):     {"resume", "complete", "fail"},
	reflect.TypeOf(deploy.ArtifactKind("")):        {"image", "compose", "runtime_config", "static_bundle", "source_archive", "backup", "diagnostic", "export"},
	reflect.TypeOf(deploy.RunStatus("")):           {"running", "success", "failed"},
	reflect.TypeOf(deploy.SlotClass("")):           {"light", "heavy", "canary"},
	reflect.TypeOf(deploy.DraftStep("")):           {"intent", "source", "detection", "configuration", "preflight"},
	reflect.TypeOf(deploy.SourceMode("")):          {"git_url", "connected_repository", "local_checkout", "image_reference", "compose_paste", "compose_upload", "compose_git", "compose_local", "blueprint", "existing_checkout", "existing_container", "existing_stack"},
	reflect.TypeOf(deploy.DetectionConfidence("")): {"high", "medium", "low"},
	reflect.TypeOf(blueprint.Profile("")):          {"web", "database", "tool", "worker", "game", "compose"},
	reflect.TypeOf(blueprint.Category("")):         {"http", "database", "tool", "automation", "game"},
	reflect.TypeOf(blueprint.InputKind("")):        {"text", "secret", "number", "boolean", "choice", "domain", "memory", "accept"},
	reflect.TypeOf(blueprint.OperationKind("")):    {"set_variable", "write_file", "console_command", "stop_signal", "wait_for_log", "fetch_artifact"},
	reflect.TypeOf(blueprint.CheckKind("")):        {"http", "tcp", "docker_health", "command", "game_handshake"},
	reflect.TypeOf(blueprint.AccessKind("")):       {"setup", "credentials", "token", "client", "open", "unavailable"},
	reflect.TypeOf(blueprint.ServiceRole("")):      {"app", "worker", "database", "cache"},
	reflect.TypeOf(blueprint.StartCondition("")):   {"healthy", "started", "completed"},
	reflect.TypeOf(gameserver.PlayerAction("")):    {"kick", "ban", "pardon", "op", "deop", "whitelist_add", "whitelist_remove"},
}

// openapiWriteOnly is every field that carries a secret in: a value, a token,
// a password, a credential. Each is accepted by a write and read back by no
// route, so it is marked writeOnly. The key is the component name and the Go
// field name.
var openapiWriteOnly = map[string]bool{
	"api.envSetRequest.Value":                true,
	"deploy.VariableWriteRequest.Value":      true,
	"deploy.SharedVariableWrite.Value":       true,
	"deploy.DotenvImportRequest.Dotenv":      true,
	"deploy.DraftSaveRequest.Dotenv":         true,
	"deploy.CredentialCreateRequest.Secret":  true,
	"deploy.CredentialUpdateRequest.Secret":  true,
	"deploy.NotificationWrite.Secret":        true,
	"deploy.NotificationWrite.Headers":       true,
	"deploy.NotificationConfig.BotToken":     true,
	"deploy.NotificationConfig.Token":        true,
	"deploy.NotificationConfig.SMTPPassword": true,
	"deploy.NotificationConfig.WebhookURL":   true,
	"deploy.DomainProtection.Password":       true,
	"logdrain.Write.Headers":                 true,
	"logdrain.Header.Value":                  true,
	"secretstore.Write.Credentials":          true,
}

// Query parameters several routes share.
var (
	queryLimit  = openapiQuery{"limit", "integer", "How many to return at most."}
	queryBefore = openapiQuery{"before", "integer", "Return runs before this run id, for the next page."}
)

// deployOperations is one line per route of the deployment API, keyed as
// deployKeyRoutes is: the method and the chi pattern under /api/v1.
var deployOperations = map[string]deployOperation{
	// The fleet and one deployment.
	"GET /deploy/": {Tag: "fleet", Summary: "List deployments: the fleet view of every production environment with its health and last run, the archived deployments, or the legacy project list.",
		Query:    []openapiQuery{{"view", "string", "fleet for the fleet view, archived for archived deployments; anything else lists the legacy projects."}},
		Response: anyOf{(*deploy.FleetReadModel)(nil), []archivedDeployment(nil), []*deploy.Project(nil)},
		Note:     "A deploy key narrowed to chosen deployments is listed only those, in every view."},
	"POST /deploy/":               {Tag: "fleet", Summary: "Create a legacy project from a repository path and the commands that run on this server; its deploy-hook secret is returned once.", Request: deployProjectRequest{}, Response: shape{"project": (*deploy.Project)(nil), "secret": ""}, Status: http.StatusCreated},
	"GET /deploy/{id}":            {Tag: "fleet", Summary: "Read one deployment: its record, whether a run is active, its deployment summary and the runtime services it uses.", Response: shape{"project": (*deploy.Project)(nil), "running": false, "deployment": (*deploy.DeploymentSummary)(nil), "runtime": deploy.RuntimeServices{}}},
	"PUT /deploy/{id}":            {Tag: "fleet", Summary: "Update a legacy project's settings.", Request: deployProjectUpdateRequest{}, Response: (*deploy.Project)(nil)},
	"DELETE /deploy/{id}":         {Tag: "fleet", Summary: "Archive a deployment: the same safe archive as POST …/archive, kept for compatibility with the legacy delete.", Status: http.StatusNoContent},
	"POST /deploy/{id}/archive":   {Tag: "fleet", Summary: "Archive a deployment: it leaves the fleet and keeps its record and resources until unarchived or removed.", Response: (*deploy.Project)(nil)},
	"POST /deploy/{id}/unarchive": {Tag: "fleet", Summary: "Restore an archived deployment to the fleet.", Response: (*deploy.Project)(nil)},
	"DELETE /deploy/{id}/permanent": {Tag: "fleet", Summary: "Delete an archived deployment's record for good, with the typed confirmation of its name.", Status: http.StatusNoContent,
		Note: "Destructive. The deployment's name is typed as its confirmation, sent in the " + httpx.ConfirmHeader + " header as the dashboard sends it."},
	"POST /deploy/{id}/removal-plan": {Tag: "fleet", Summary: "Plan the removal of a deployment's managed resources: what would be removed, what is kept, and what has to be confirmed.", Response: (*deploy.RemovalPlan)(nil)},
	"POST /deploy/{id}/remove-managed": {Tag: "fleet", Summary: "Remove a deployment's managed resources as planned, with the typed confirmation.", Request: deploy.RemoveManagedRequest{}, Response: (*deploy.RemovalExecution)(nil),
		Note: "Destructive. A removal that stopped part-way answers with its error status and the execution so far, under error and execution."},
	"POST /deploy/{id}/duplicate":                {Tag: "fleet", Summary: "Duplicate a deployment into a new draft under another name.", Request: deploymentDuplicateRequest{}, Response: (*deploy.Draft)(nil), Status: http.StatusCreated},
	"POST /deploy/{id}/rotate-secret":            {Tag: "fleet", Summary: "Issue a new secret for a legacy project's deploy hook, returned once.", Response: shape{"secret": ""}},
	"GET /deploy/{id}/env":                       {Tag: "fleet", Summary: "List a legacy project's variables, each value masked to its first and last characters.", Response: []deploy.EnvVar(nil)},
	"PUT /deploy/{id}/env":                       {Tag: "fleet", Summary: "Set one legacy project variable.", Request: envSetRequest{}, Status: http.StatusNoContent},
	"GET /deploy/{id}/env/reveal":                {Tag: "fleet", Summary: "Reveal a legacy project's variables with their values; audited.", Response: []deploy.EnvVar(nil)},
	"DELETE /deploy/{id}/env/{key}":              {Tag: "fleet", Summary: "Remove one legacy project variable.", Status: http.StatusNoContent},
	"GET /deploy/{id}/commits":                   {Tag: "fleet", Summary: "List the recent commits of a legacy project's checkout.", Query: []openapiQuery{queryLimit}, Response: []deploy.Commit(nil)},
	"GET /deploy/{id}/insights":                  {Tag: "fleet", Summary: "Read a deployment's insights over the last days: run outcomes and durations, and what its route served.", Query: []openapiQuery{{"days", "integer", "How many days back to read."}}, Response: (*deploy.DeploymentInsights)(nil)},
	"GET /deploy/{id}/availability":              {Tag: "fleet", Summary: "Read how much of the last day, week and month the production release was running and healthy, from the persisted container events.", Response: deploy.Availability{}},
	"GET /deploy/sizing":                         {Tag: "fleet", Summary: "Read every environment's right-sizing verdict from its recorded usage, the ones most off first; a narrowed deploy key sees its own deployments.", Response: fleetSizing{}},
	"GET /deploy/{id}/environments/{env}/sizing": {Tag: "fleet", Summary: "Read the memory and CPU limits an environment should have from its recorded usage: p95, peak, the live limit, OOM kills and throttling, and the verdict.", Response: (*environmentSizing)(nil)},
	"GET /deploy/{id}/operations":                {Tag: "fleet", Summary: "Read the operations summary of a deployment: its releases, pending changes and recent work.", Response: (*deploy.OperationsSummary)(nil)},
	"GET /deploy/{id}/lifecycle":                 {Tag: "fleet", Summary: "List a deployment's container lifecycle events, newest first.", Query: []openapiQuery{queryLimit}, Response: deploymentLifecycle{}},
	"GET /deploy/{id}/lifecycle/stream":          {Tag: "fleet", Summary: "Follow a deployment's container lifecycle events over a WebSocket.", Stream: true},
	"GET /deploy/{id}/favicon":                   {Tag: "fleet", Summary: "Serve the favicon of the deployed site, fetched through the dashboard and cached.", Content: "image/*"},
	"GET /deploy/{id}/preview-frame":             {Tag: "fleet", Summary: "Serve the HTML page that frames the deployed site for the dashboard's preview.", Content: "text/html"},
	"POST /deploy/import/preview":                {Tag: "fleet", Summary: "Inspect a folder, checkout, container or stack on this server as a source to adopt, without changing it.", Request: deploy.DraftSourceConfig{}, Response: (*deploy.ImportPreview)(nil)},
	"POST /deploy/import/adopt":                  {Tag: "fleet", Summary: "Adopt an existing workload on this server as a deployment by committing its import draft.", Request: importAdoptRequest{}, Response: (*deploy.DraftCommitResult)(nil), Status: http.StatusCreated, Note: "Answers 200 when the draft was already committed."},
	"GET /deploy/pull-requests":                  {Tag: "fleet", Summary: "Count the open pull requests and previews of every deployment.", Response: shape{"projects": map[string]fleetPullRequestCounts(nil)}},

	// This description.
	"GET /deploy/openapi.json": {Tag: "keys", Summary: "Serve this description of the deployment API as OpenAPI 3.1 JSON.", Content: "application/json", Note: "Static: generated from the router and the handlers' types the first time it is asked for. Any signed-in account and any key reads it, a deploy key included."},
	"GET /deploy/openapi.yaml": {Tag: "keys", Summary: "Serve this description of the deployment API as OpenAPI 3.1 YAML.", Content: "application/yaml", Note: "The same document as openapi.json."},

	// Drafts.
	"GET /deploy/drafts":                    {Tag: "drafts", Summary: "List the caller's unfinished drafts; a deploy key's are its own.", Response: []deploy.DraftSummary(nil)},
	"POST /deploy/drafts":                   {Tag: "drafts", Summary: "Open a new deployment draft; the body, if any, is an empty object.", Request: struct{}{}, Optional: true, Response: (*deploy.Draft)(nil), Status: http.StatusCreated},
	"GET /deploy/drafts/{draft}":            {Tag: "drafts", Summary: "Read a draft: its revision, intent, source, detection, configuration and preflight.", Response: (*deploy.Draft)(nil)},
	"PUT /deploy/drafts/{draft}":            {Tag: "drafts", Summary: "Save one step of a draft at its revision: intent, source, configuration, or a .env whose values are sealed.", Request: deploy.DraftSaveRequest{}, Response: (*deploy.Draft)(nil)},
	"DELETE /deploy/drafts/{draft}":         {Tag: "drafts", Summary: "Discard a draft.", Status: http.StatusNoContent},
	"POST /deploy/drafts/{draft}/detect":    {Tag: "drafts", Summary: "Run framework detection on a draft's source at its revision.", Request: draftRevisionRequest{}, Response: (*deploy.Draft)(nil)},
	"POST /deploy/drafts/{draft}/preflight": {Tag: "drafts", Summary: "Run preflight on a draft: the findings a commit has to pass or acknowledge.", Request: draftRevisionRequest{}, Response: shape{"draft": (*deploy.Draft)(nil), "preflight": (*deploy.PreflightResult)(nil)}},
	"POST /deploy/drafts/{draft}/commit":    {Tag: "drafts", Summary: "Commit a draft into a deployment and its production environment, acknowledging preflight warnings by code.", Request: deploy.DraftCommitRequest{}, Response: (*deploy.DraftCommitResult)(nil), Status: http.StatusCreated, Note: "Answers 200 when the draft was already committed. A deploy key narrowed to chosen deployments is given the deployment it creates."},

	// Environments.
	"GET /deploy/{id}/environments":                             {Tag: "environments", Summary: "List a deployment's environments: production and, when added, staging, each with its live release.", Response: []deploy.EnvironmentSummary(nil)},
	"POST /deploy/{id}/environments":                            {Tag: "environments", Summary: "Add a staging environment copied from production, with its own domain.", Request: stagingCreateRequest{}, Response: (*deploy.StagingCopy)(nil), Status: http.StatusCreated},
	"DELETE /deploy/{id}/environments/{env}":                    {Tag: "environments", Summary: "Remove the staging environment and its containers, and its volumes when asked.", Request: stagingRemoveRequest{}, Response: shape{"environmentId": int64(0), "removed": []string(nil)}, Note: "Destructive. A removal that stopped part-way answers 409 staging_removal_incomplete with what was removed."},
	"GET /deploy/{id}/environments/{env}/configuration":         {Tag: "environments", Summary: "Read an environment's configuration at its revision: build, runtime, dependencies, checks, domains and variables.", Response: (*deploy.EnvironmentConfiguration)(nil)},
	"PUT /deploy/{id}/environments/{env}/configuration":         {Tag: "environments", Summary: "Replace an environment's configuration at its revision; the change reaches the route with the next deploy.", Request: deploy.ConfigurationWriteRequest{}, Response: (*deploy.EnvironmentConfiguration)(nil), Note: "A deploy key may not give the deployment host authority (a privileged container, the host's network, capabilities, devices, a host path, a release task on the host), nor change an environment that already has it."},
	"POST /deploy/{id}/environments/{env}/detect":               {Tag: "environments", Summary: "Run framework detection on an environment's source again; the body is an empty object.", Request: struct{}{}, Response: (*deploy.DetectionProposal)(nil)},
	"PUT /deploy/{id}/environments/{env}/source":                {Tag: "environments", Summary: "Replace an environment's source (a repository, an image or a Compose file) and detect it.", Request: deploymentSourceUpdateRequest{}, Response: deploymentSourceUpdateResult{}, Note: "A deploy key may not attach a saved credential, nor send a source's credential to another repository."},
	"PUT /deploy/{id}/environments/{env}/git-policy":            {Tag: "environments", Summary: "Set how Git changes reach an environment: polling, hooks, manual only, or production by promotion only.", Request: gitPolicyWrite{}, Response: deploy.GitDeploymentPolicy{}},
	"GET /deploy/{id}/environments/{env}/git-watch":             {Tag: "environments", Summary: "Read the Git watcher's state for an environment: what it last saw, and when.", Response: deploy.GitWatchStatus{}},
	"GET /deploy/{id}/environments/{env}/pending":               {Tag: "environments", Summary: "Read what waits for the next deploy: configuration and variable changes not yet released.", Response: (*deploy.PendingState)(nil)},
	"POST /deploy/{id}/environments/{env}/watch-paths/simulate": {Tag: "triggers", Summary: "Try the environment's watch-path filter against a list of changed paths.", Request: watchSimulationRequest{}, Response: shape{"matched": false}},
	"GET /deploy/{id}/environments/{env}/rehearsal":             {Tag: "environments", Summary: "Read an environment's migration rehearsal terms: whether it is on, which database's backup is restored, and the limits.", Response: deploy.RehearsalPolicy{}},
	"PUT /deploy/{id}/environments/{env}/rehearsal":             {Tag: "environments", Summary: "Set an environment's migration rehearsal terms.", Request: deploy.RehearsalPolicy{}, Response: deploy.RehearsalPolicy{}},

	// Domains: what the public route serves.
	"GET /deploy/hostname":                             {Tag: "domains", Summary: "Suggest a public hostname under this server's domain for a deployment name, and say whether the name is taken and how a certificate would cover it.", Query: []openapiQuery{{"name", "string", "The deployment's name to derive a hostname from."}, {"hostname", "string", "A hostname to check instead of deriving one."}}, Response: hostnameSuggestion{}},
	"GET /deploy/{id}/environments/{env}/maintenance":  {Tag: "domains", Summary: "Read the maintenance switch of an environment's public route.", Response: proxysvc.MaintenanceSwitch{}},
	"PUT /deploy/{id}/environments/{env}/maintenance":  {Tag: "domains", Summary: "Switch an environment's maintenance page on or off; it acts at once.", Request: maintenanceSwitchRequest{}, Response: proxysvc.MaintenanceSwitch{}, Note: "Destructive, as stopping the application is. Who is let past is runtime.maintenance.bypassFrom in the configuration, applied by the next release."},
	"GET /deploy/{id}/environments/{env}/pages/{page}": {Tag: "domains", Summary: "Read one of the route's own pages, the maintenance page or an error page, as every visitor is shown it.", Response: (*proxysvc.SitePage)(nil)},
	"PUT /deploy/{id}/environments/{env}/pages/{page}": {Tag: "domains", Summary: "Replace one of the route's own pages.", Request: sitePageWrite{}, Response: (*proxysvc.SitePage)(nil)},
	"GET /deploy/{id}/requests":                        {Tag: "domains", Summary: "Read the requests a deployment's public route served, filtered and windowed.", Query: requestFilterQuery, Response: deploy.RequestWindow{}},
	"GET /deploy/{id}/requests/stream":                 {Tag: "domains", Summary: "Follow the requests a deployment's route serves over a WebSocket.", Query: []openapiQuery{{"after", "integer", "Resume after this sequence number."}}, Stream: true},
	"GET /deploy/{id}/requests/export":                 {Tag: "domains", Summary: "Export the filtered requests as CSV.", Query: requestFilterQuery, Content: "text/csv"},
	"GET /deploy/traffic":                              {Tag: "domains", Summary: "Read the traffic pulse of every deployment's public route, by deployment id.", Response: map[string]deploy.TrafficPulse(nil)},
	"GET /deploy/{id}/runs/{run}/traffic":              {Tag: "runs", Summary: "Read the request readings around a run's cutover.", Response: deploy.RunTraffic{}},

	// Releases.
	"GET /deploy/{id}/environments/{env}/releases":                        {Tag: "releases", Summary: "List an environment's retained releases, newest first.", Query: []openapiQuery{queryLimit}, Response: []deploy.Release(nil)},
	"GET /deploy/{id}/environments/{env}/releases/{release}/comparison":   {Tag: "releases", Summary: "Compare a release with the one before it, or with ?from=: image, settings, variables and artifacts.", Query: []openapiQuery{{"from", "integer", "The release to compare against instead of the previous one."}}, Response: shape{"comparison": (*deploy.ReleaseComparison)(nil), "update": (*deploy.ReleaseUpdateStatus)(nil), "artifacts": []deploy.ReleaseArtifactStatus(nil), "reason": ""}, Note: "The first recorded release has nothing to compare against: comparison is null and reason says so."},
	"PUT /deploy/{id}/environments/{env}/releases/{release}/pin":          {Tag: "releases", Summary: "Pin a release so retention keeps it, or unpin it.", Request: releasePinRequest{}, Response: (*deploy.Release)(nil)},
	"POST /deploy/{id}/environments/{env}/rollback":                       {Tag: "releases", Summary: "Roll an environment back to a retained release, as a new run.", Request: deploymentRollbackRequest{}, Response: (*deploy.EngineRun)(nil), Status: http.StatusAccepted, Note: "Destructive: the live release is taken out of service."},
	"POST /deploy/{id}/rollback":                                          {Tag: "releases", Summary: "Roll a legacy project back to a commit.", Request: rollbackRequest{}, Response: shape{"started": false, "commit": "", "runId": int64(0), "state": deploy.RunState("")}, Status: http.StatusAccepted},
	"GET /deploy/{id}/environments/{env}/promotion":                       {Tag: "releases", Summary: "Preview promoting staging's release into production: the image and its digest, blockers, and a comparison.", Query: []openapiQuery{{"releaseId", "integer", "A staging release other than the live one."}}, Response: (*deploy.PromotionPreview)(nil)},
	"POST /deploy/{id}/environments/{env}/promote":                        {Tag: "releases", Summary: "Promote staging's release into production as the very same image, with no build, as a new run.", Request: promotionRequest{}, Response: (*deploy.EngineRun)(nil), Status: http.StatusAccepted},
	"GET /deploy/{id}/environments/{env}/rollback-watch":                  {Tag: "releases", Summary: "Read the rollback watch: the terms a release is kept warm on, and the open window if there is one.", Response: rollbackWatchResponse{}},
	"POST /deploy/{id}/environments/{env}/rollback-watch/rollback":        {Tag: "releases", Summary: "Switch traffic back to the warm release at once, as a new run.", Response: (*deploy.EngineRun)(nil), Status: http.StatusAccepted, Note: "Destructive, as a rollback from the release list is."},
	"GET /deploy/{id}/environments/{env}/data-copies":                     {Tag: "releases", Summary: "List the side copies of the environment's linked databases restored as of a release: what each is of, its state and until when it is kept; never its sign-in.", Query: []openapiQuery{{"run", "integer", "Only copies the rollback run asked for."}, {"release", "integer", "Only copies as of this release."}}, Response: []deploy.DataCopy(nil)},
	"POST /deploy/{id}/environments/{env}/releases/{release}/data-copies": {Tag: "releases", Summary: "Restore each linked database as it stood when the release went live into a side copy on the deployment's network, kept two hours.", Request: dataCopyRequest{}, Response: []deploy.DataCopy(nil), Status: http.StatusAccepted, Note: "The copy is for comparing or restoring from; production never runs against it."},
	"GET /deploy/{id}/environments/{env}/data-copies/{copy}/url":          {Tag: "releases", Summary: "Reveal a side copy's connection details, audited.", Response: shape{"url": "", "host": "", "port": 0, "user": "", "database": ""}},
	"POST /deploy/{id}/environments/{env}/data-copies/{copy}/extend":      {Tag: "releases", Summary: "Keep a side copy for more hours.", Request: dataCopyExtendRequest{}, Response: (*deploy.DataCopy)(nil)},
	"DELETE /deploy/{id}/environments/{env}/data-copies/{copy}":           {Tag: "releases", Summary: "Remove a side copy now, with its container, volume and network.", Response: (*deploy.DataCopy)(nil)},
	"GET /deploy/{id}/environments/{env}/data-copies/{copy}/comparison":   {Tag: "releases", Summary: "Compare a side copy with the live database: tables and row counts side by side.", Response: (*DataCopyComparison)(nil)},
	"POST /deploy/{id}/environments/{env}/rollback-watch/keep":            {Tag: "releases", Summary: "Keep the new release now and retire the warm one.", Response: (*deploy.RollbackWatch)(nil)},
	"GET /deploy/{id}/environments/{env}/canary":                          {Tag: "releases", Summary: "Read the environment's canary: the open trial, or the latest ended one, with both releases' readings and the verdict.", Response: canaryResponse{}},
	"POST /deploy/{id}/environments/{env}/canary/promote":                 {Tag: "releases", Summary: "Promote the canary now; the run holding the trial carries it out.", Response: (*deploy.CanaryWindow)(nil), Status: http.StatusAccepted},
	"POST /deploy/{id}/environments/{env}/canary/abort":                   {Tag: "releases", Summary: "Abort the canary now; the live release keeps every request and the candidate is removed.", Response: (*deploy.CanaryWindow)(nil), Status: http.StatusAccepted},
	"PUT /deploy/{id}/environments/{env}/rollback-policy":                 {Tag: "releases", Summary: "Set the terms a release is kept warm and rolled back on after a deploy.", Request: deploy.RollbackPolicy{}, Response: deploy.RollbackPolicy{}},

	// Runs.
	"POST /deploy/{id}/run":                      {Tag: "runs", Summary: "Queue a deploy of a legacy project.", Response: shape{"started": false, "project": "", "runId": int64(0), "state": deploy.RunState("")}, Status: http.StatusAccepted},
	"POST /deploy/{id}/environments/{env}/runs":  {Tag: "runs", Summary: "Queue a run for an environment: a deploy, redeploy, restart, stop, start or forced build, of a ref when the source is Git.", Request: deploymentRunCreateRequest{}, Response: (*deploy.EngineRun)(nil), Status: http.StatusAccepted},
	"GET /deploy/{id}/runs":                      {Tag: "runs", Summary: "List a deployment's runs, newest first, and whether one is active.", Query: []openapiQuery{queryLimit, {"view", "string", "engine for the orchestration engine's runs, paged with before; otherwise the legacy run list."}, queryBefore, {"state", "string", "engine view: only runs in this state."}, {"operation", "string", "engine view: only runs of this operation."}}, Response: anyOf{shape{"runs": []deploy.EngineRun(nil), "running": false, "nextBefore": int64(0)}, shape{"runs": []*deploy.Run(nil), "running": false}}},
	"GET /deploy/{id}/runs/{run}":                {Tag: "runs", Summary: "Read a run with its steps.", Response: (*deploy.RunSnapshot)(nil)},
	"POST /deploy/{id}/runs/{run}/cancel":        {Tag: "runs", Summary: "Cancel a run that has not finished.", Response: (*deploy.EngineRun)(nil), Status: http.StatusAccepted},
	"POST /deploy/{id}/runs/{run}/retry":         {Tag: "runs", Summary: "Retry a failed run as a new run with the same request.", Response: (*deploy.EngineRun)(nil), Status: http.StatusAccepted},
	"GET /deploy/{id}/runs/{run}/logs":           {Tag: "runs", Summary: "Read a run's logs, by step.", Response: deploy.RunLogs{}},
	"GET /deploy/{id}/runs/{run}/transcript":     {Tag: "runs", Summary: "Read a run's transcript lines after a sequence number, oldest first, with the run and its steps; what the stream sends, for a client without a socket.", Query: []openapiQuery{{"after", "integer", "Return lines after this sequence number."}, queryLimit}, Response: shape{"run": deploy.EngineRun{}, "steps": []deploy.RunStep(nil), "lines": []transcriptLine(nil), "next": int64(0), "more": false}},
	"GET /deploy/{id}/runs/{run}/stream":         {Tag: "runs", Summary: "Follow a run's events over a WebSocket, from a sequence number.", Query: []openapiQuery{{"after", "integer", "Resume after this sequence number."}}, Stream: true},
	"GET /deploy/{id}/runs/{run}/metrics":        {Tag: "runs", Summary: "Compare the run's release with the previous one on resource readings.", Response: (*deploy.RunMetricComparison)(nil)},
	"GET /deploy/{id}/runs/{run}/settings-drift": {Tag: "runs", Summary: "Read what differed between a run's settings and the environment's at the time.", Response: (*deploy.RunSettingsDrift)(nil)},
	"GET /deploy/{id}/runs/{run}/diagnosis":      {Tag: "runs", Summary: "Read the kept diagnosis of a failed run.", Response: shape{"diagnosis": (*runDiagnosisView)(nil)}},
	"POST /deploy/{id}/runs/{run}/diagnosis":     {Tag: "runs", Summary: "Ask the configured model to diagnose a failed run, or to diagnose it again.", Request: diagnoseRequest{}, Response: shape{"diagnosis": (*runDiagnosisView)(nil)}},

	// Variables.
	"GET /deploy/{id}/environments/{env}/variables":                  {Tag: "variables", Summary: "List an environment's variables: name, sensitivity, scopes, the value's digest and whether it awaits a deploy; never a value.", Response: []deploy.DeploymentVariable(nil)},
	"PUT /deploy/{id}/environments/{env}/variables/{name}":           {Tag: "variables", Summary: "Set a variable's value or reference at the environment's revision.", Request: deploy.VariableWriteRequest{}, Response: (*deploy.VariableMutationResult)(nil), Note: "A deploy key may not point a variable at a credential, database or store reference, nor, when narrowed to some deployments, at a server shared variable."},
	"DELETE /deploy/{id}/environments/{env}/variables/{name}":        {Tag: "variables", Summary: "Remove a variable at the environment's revision.", Request: variableRevisionRequest{}, Response: shape{"desiredRevision": 0}},
	"POST /deploy/{id}/environments/{env}/variables/import":          {Tag: "variables", Summary: "Import a .env text into the environment; ?dryRun=1 previews each line's change without writing.", Query: []openapiQuery{{"dryRun", "string", "1 to preview the changes without writing them."}}, Request: deploy.DotenvImportRequest{}, Response: anyOf{(*deploy.VariableMutationResult)(nil), (*deploy.DotenvImportPreview)(nil)}},
	"POST /deploy/{id}/environments/{env}/variables/{name}/generate": {Tag: "variables", Summary: "Generate a secret for a variable; a session is shown the value once, a key never.", Request: variableGenerateRequest{}, Response: (*deploy.VariableMutationResult)(nil), Status: http.StatusCreated},
	"POST /deploy/{id}/environments/{env}/variables/{name}/rotate":   {Tag: "variables", Summary: "Rotate a generated secret; a session is shown the new value once, a key never.", Request: variableRevisionRequest{}, Response: (*deploy.VariableMutationResult)(nil)},
	"GET /deploy/{id}/environments/{env}/variables/{name}/reveal":    {Tag: "variables", Summary: "Reveal a variable's value; audited.", Response: (*deploy.VariableReveal)(nil)},
	"GET /deploy/shared-variables":                                   {Tag: "variables", Summary: "List the server's shared variables, read by any deployment as ${{server.NAME}}: names, sensitivity and digests.", Response: []deploy.SharedVariable(nil)},
	"PUT /deploy/shared-variables/{name}":                            {Tag: "variables", Summary: "Set a server shared variable.", Request: deploy.SharedVariableWrite{}, Response: (*deploy.SharedVariable)(nil), Note: "Answers 201 when the variable is new, 200 when it changed."},
	"DELETE /deploy/shared-variables/{name}":                         {Tag: "variables", Summary: "Remove a server shared variable nothing reads.", Status: http.StatusNoContent},
	"GET /deploy/shared-variables/{name}/reveal":                     {Tag: "variables", Summary: "Reveal a server shared variable's value; audited.", Response: (*deploy.SharedVariableReveal)(nil)},
	"GET /deploy/{id}/shared-variables":                              {Tag: "variables", Summary: "List a deployment's shared variables, read by its environments as ${{project.NAME}}.", Response: []deploy.SharedVariable(nil)},
	"PUT /deploy/{id}/shared-variables/{name}":                       {Tag: "variables", Summary: "Set a deployment shared variable.", Request: deploy.SharedVariableWrite{}, Response: (*deploy.SharedVariable)(nil), Note: "Answers 201 when the variable is new, 200 when it changed."},
	"DELETE /deploy/{id}/shared-variables/{name}":                    {Tag: "variables", Summary: "Remove a deployment shared variable nothing reads.", Status: http.StatusNoContent},
	"GET /deploy/{id}/shared-variables/{name}/reveal":                {Tag: "variables", Summary: "Reveal a deployment shared variable's value; audited.", Response: (*deploy.SharedVariableReveal)(nil)},

	// Triggers, schedules and the hook.
	"GET /deploy/{id}/environments/{env}/triggers":                          {Tag: "triggers", Summary: "List an environment's triggers, each with its last delivery and recent decisions.", Response: []deploy.Trigger(nil)},
	"POST /deploy/{id}/environments/{env}/triggers":                         {Tag: "triggers", Summary: "Add a trigger, a hook or a provider's webhook; its secret is returned once.", Request: deploy.TriggerWrite{}, Response: (*deploy.TriggerCreated)(nil), Status: http.StatusCreated},
	"PUT /deploy/{id}/environments/{env}/triggers/{trigger}":                {Tag: "triggers", Summary: "Update a trigger.", Request: deploy.TriggerWrite{}, Response: (*deploy.Trigger)(nil)},
	"DELETE /deploy/{id}/environments/{env}/triggers/{trigger}":             {Tag: "triggers", Summary: "Remove a trigger.", Status: http.StatusNoContent},
	"POST /deploy/{id}/environments/{env}/triggers/{trigger}/test":          {Tag: "triggers", Summary: "Check that a trigger is valid, and say whether it is enabled.", Response: shape{"valid": false, "enabled": false, "provider": ""}},
	"POST /deploy/{id}/environments/{env}/triggers/{trigger}/rotate-secret": {Tag: "triggers", Summary: "Issue a new secret for a trigger's hook, returned once.", Response: shape{"secret": ""}},
	"GET /deploy/{id}/environments/{env}/triggers/{trigger}/deliveries":     {Tag: "triggers", Summary: "List a trigger's recent deliveries: event, ref, decision, reason and the run it started.", Query: []openapiQuery{{"limit", "integer", "1 to 200; anything else reads 50."}}, Response: []deploy.TriggerDelivery(nil)},
	"POST /deploy/{id}/hooks/{triggerID}":                                   {Tag: "triggers", Summary: "A generic trigger's webhook: the raw body is signed with the trigger's secret, and a matching ref queues a run or a preview.", Request: json.RawMessage{}, Response: shape{"accepted": false, "reason": "", "runId": int64(0), "state": deploy.RunState(""), "approvalRequired": false}, Status: http.StatusAccepted, Note: "Called by CI, not by a person: it authenticates by the X-JD-Signature-256 header (HMAC-SHA256 of the body with the trigger's secret) and takes no session or key. Idempotency-Key names the delivery."},
	"GET /deploy/{id}/environments/{env}/schedules":                         {Tag: "triggers", Summary: "List an environment's schedules.", Response: []deploy.Schedule(nil)},
	"POST /deploy/{id}/environments/{env}/schedules":                        {Tag: "triggers", Summary: "Add a schedule: a cron expression and the steps it runs.", Request: deploy.ScheduleWrite{}, Response: (*deploy.Schedule)(nil), Status: http.StatusCreated},
	"POST /deploy/{id}/environments/{env}/schedules/test":                   {Tag: "triggers", Summary: "Compute a schedule's next runs without saving it.", Request: deploy.ScheduleWrite{}, Response: shape{"nextRunAt": time.Time{}, "nextRuns": []time.Time(nil)}},
	"PUT /deploy/{id}/environments/{env}/schedules/{schedule}":              {Tag: "triggers", Summary: "Update a schedule.", Request: deploy.ScheduleWrite{}, Response: (*deploy.Schedule)(nil)},
	"DELETE /deploy/{id}/environments/{env}/schedules/{schedule}":           {Tag: "triggers", Summary: "Remove a schedule.", Status: http.StatusNoContent},
	"GET /deploy/{id}/environments/{env}/schedules/{schedule}/runs":         {Tag: "triggers", Summary: "List the runs a schedule started.", Response: shape{"runs": []deploy.EngineRun(nil)}},

	// Previews and pull requests.
	"GET /deploy/{id}/previews":                                                      {Tag: "previews", Summary: "List a deployment's pull-request previews.", Response: []deploy.PreviewRef(nil)},
	"GET /deploy/{id}/previews/approvals":                                            {Tag: "previews", Summary: "List the preview approvals waiting on a person.", Response: []deploy.PreviewApproval(nil)},
	"POST /deploy/{id}/previews/approvals/{approval}/approve":                        {Tag: "previews", Summary: "Approve one exact revision of a preview, and deploy it when asked.", Request: previewApproveRequest{}, Response: shape{"preview": (*deploy.PreviewRef)(nil), "approval": (*deploy.PreviewApproval)(nil), "runId": int64(0)}, Status: http.StatusAccepted},
	"POST /deploy/{id}/previews/approvals/{approval}/reject":                         {Tag: "previews", Summary: "Reject one exact revision of a preview.", Request: previewRejectRequest{}, Response: (*deploy.PreviewApproval)(nil)},
	"GET /deploy/{id}/pull-requests":                                                 {Tag: "previews", Summary: "List a deployment's open pull requests with their previews, checks and what production would do on merge.", Response: projectPullRequests{}},
	"GET /deploy/{id}/pull-requests/{number}/checks":                                 {Tag: "previews", Summary: "List the check runs of a pull request's head.", Query: []openapiQuery{{"head", "string", "The head commit to read checks for."}}, Response: []ghx.CheckRun(nil)},
	"POST /deploy/{id}/pull-requests/{number}/preview":                               {Tag: "previews", Summary: "Test a pull request: approve its head and start or update its preview, copying production's variables when asked.", Request: pullRequestPreviewRequest{}, Response: shape{"preview": (*deploy.PreviewRef)(nil), "runId": int64(0), "copied": []string(nil), "skipped": []string(nil), "address": (*deploy.PreviewAddress)(nil), "variablesFromProduction": false}, Status: http.StatusAccepted},
	"POST /deploy/{id}/pull-requests/{number}/preview/close":                         {Tag: "previews", Summary: "Close a pull request's preview: its container, volumes and address are removed.", Response: shape{"runId": int64(0)}, Status: http.StatusAccepted, Note: "Destructive, with an ordinary confirmation."},
	"POST /deploy/{id}/pull-requests/{number}/merge":                                 {Tag: "previews", Summary: "Merge a pull request on GitHub as the dashboard's own account, and say what production does next.", Request: pullRequestMergeRequest{}, Response: shape{"merged": false, "previewRunId": int64(0), "production": shape{"automatic": false, "awaitingFirstDeployment": false}}},
	"POST /deploy/{id}/pull-requests/{number}/comment":                               {Tag: "previews", Summary: "Comment on a pull request as the dashboard's own account.", Request: pullRequestCommentRequest{}, Response: shape{"ok": false}},
	"POST /deploy/{id}/pull-requests/{number}/preview/branches/{connection}/refresh": {Tag: "previews", Summary: "Restore a preview's database branch again from the newest backup, keeping its address and password.", Response: shape{"branch": (*deploy.PreviewBranch)(nil), "replaced": int64(0)}, Status: http.StatusAccepted, Note: "Everything the preview wrote to the branch is discarded; production is not touched."},

	// Database links.
	"GET /deploy/{id}/environments/{env}/database-links":                       {Tag: "database links", Summary: "List the databases an environment's variables read, and the networks that join them.", Response: []databaseLink(nil)},
	"GET /deploy/{id}/environments/{env}/database-addresses":                   {Tag: "database links", Summary: "List the addresses an environment reaches its linked databases at.", Response: []databaseAddress(nil)},
	"PUT /deploy/{id}/environments/{env}/database-links/{connection}/previews": {Tag: "database links", Summary: "Set what a linked database gives the environment's pull request previews: nothing, a branch from the newest backup, or a branch with columns masked.", Request: previewBranchPolicyRequest{}, Response: deploy.PreviewBranchPolicy{}, Note: "A preview of a fork never gets a branch."},

	// Notifications and alerts.
	"GET /deploy/notifications":                      {Tag: "notifications", Summary: "List the notification channels.", Response: []deploy.NotificationChannel(nil)},
	"GET /deploy/notifications/options":              {Tag: "notifications", Summary: "List the channel kinds, run events and SMTP security modes a channel can be made with.", Response: shape{"kinds": []string(nil), "events": []string(nil), "smtpSecurity": []string(nil)}},
	"POST /deploy/notifications":                     {Tag: "notifications", Summary: "Add a notification channel; a webhook channel's signing secret is returned once.", Request: deploy.NotificationWrite{}, Response: shape{"channel": (*deploy.NotificationChannel)(nil), "secret": ""}, Status: http.StatusCreated},
	"PUT /deploy/notifications/{channel}":            {Tag: "notifications", Summary: "Update a notification channel.", Request: deploy.NotificationWrite{}, Response: (*deploy.NotificationChannel)(nil)},
	"PUT /deploy/notifications/{channel}/enabled":    {Tag: "notifications", Summary: "Switch a notification channel on or off.", Request: enabledRequest{}, Response: (*deploy.NotificationChannel)(nil)},
	"POST /deploy/notifications/{channel}/test":      {Tag: "notifications", Summary: "Send a test message through a channel.", Response: shape{"delivered": false}},
	"DELETE /deploy/notifications/{channel}":         {Tag: "notifications", Summary: "Remove a notification channel.", Status: http.StatusNoContent},
	"GET /deploy/notifications/{channel}/deliveries": {Tag: "notifications", Summary: "List a channel's recent deliveries and their outcomes.", Query: []openapiQuery{queryLimit}, Response: []deploy.NotificationDelivery(nil)},
	"GET /deploy/{id}/alerts":                        {Tag: "notifications", Summary: "List a deployment's traffic alerts, and the kinds an alert can be.", Response: trafficAlertList{}},
	"POST /deploy/{id}/alerts":                       {Tag: "notifications", Summary: "Add a traffic alert.", Request: deploy.TrafficAlertWrite{}, Response: (*deploy.TrafficAlert)(nil), Status: http.StatusCreated},
	"PUT /deploy/{id}/alerts/{alert}":                {Tag: "notifications", Summary: "Update a traffic alert.", Request: deploy.TrafficAlertWrite{}, Response: (*deploy.TrafficAlert)(nil)},
	"DELETE /deploy/{id}/alerts/{alert}":             {Tag: "notifications", Summary: "Remove a traffic alert.", Status: http.StatusNoContent},
	"POST /deploy/{id}/alerts/{alert}/test":          {Tag: "notifications", Summary: "Send a traffic alert's test message through its channels.", Response: shape{"delivered": 0, "failed": 0, "channels": 0}},

	// Log drains.
	"GET /deploy/log-drains":                 {Tag: "log drains", Summary: "List the log drains with what each has shipped.", Response: []logdrain.Drain(nil)},
	"POST /deploy/log-drains":                {Tag: "log drains", Summary: "Add a log drain: an OTLP, Loki or JSON-lines endpoint with sealed headers.", Request: logdrain.Write{}, Response: logdrain.Drain{}, Status: http.StatusCreated},
	"PUT /deploy/log-drains/{drain}":         {Tag: "log drains", Summary: "Update a log drain; headers left out are kept.", Request: logdrain.Write{}, Response: logdrain.Drain{}},
	"PUT /deploy/log-drains/{drain}/enabled": {Tag: "log drains", Summary: "Switch a log drain on or off.", Request: enabledRequest{}, Response: logdrain.Drain{}},
	"POST /deploy/log-drains/{drain}/test":   {Tag: "log drains", Summary: "Send a test batch to a log drain.", Response: shape{"ok": false}},
	"DELETE /deploy/log-drains/{drain}":      {Tag: "log drains", Summary: "Remove a log drain.", Status: http.StatusNoContent},

	// Secret stores.
	"GET /deploy/secret-stores":               {Tag: "secret stores", Summary: "List the secret-store connections with their status and the variables that read each.", Response: []secretStoreView(nil)},
	"POST /deploy/secret-stores":              {Tag: "secret stores", Summary: "Add a secret-store connection; its credentials are sealed.", Request: secretstore.Write{}, Response: secretStoreView{}, Status: http.StatusCreated},
	"PUT /deploy/secret-stores/{store}":       {Tag: "secret stores", Summary: "Update a secret-store connection; credentials left out are kept.", Request: secretstore.Write{}, Response: secretStoreView{}},
	"POST /deploy/secret-stores/{store}/test": {Tag: "secret stores", Summary: "Read one reference through a connection to prove it works.", Request: secretstore.Probe{}, Response: (*secretstore.TestResult)(nil)},
	"DELETE /deploy/secret-stores/{store}":    {Tag: "secret stores", Summary: "Remove a secret-store connection no variable reads.", Status: http.StatusNoContent},

	// The GitHub App.
	"GET /deploy/github-app/":             {Tag: "github-app", Summary: "Read the GitHub App's status: whether one is connected, its slug, installation and webhook address.", Response: githubapp.Status{}},
	"GET /deploy/github-app/repositories": {Tag: "github-app", Summary: "List the repositories the App's installations reach.", Response: []githubapp.Repository(nil)},
	"POST /deploy/github-app/manifest":    {Tag: "github-app", Summary: "Start the manifest flow that creates the dashboard's GitHub App.", Request: githubapp.ManifestRequest{}, Response: githubapp.ManifestStart{}},
	"POST /deploy/github-app/callback":    {Tag: "github-app", Summary: "Finish the manifest flow with the code GitHub sent back.", Request: githubAppCallbackRequest{}, Response: shape{"slug": "", "owner": ""}},
	"DELETE /deploy/github-app/":          {Tag: "github-app", Summary: "Disconnect the GitHub App and forget its credentials.", Status: http.StatusNoContent},

	// Blueprints.
	"GET /deploy/blueprints/":              {Tag: "blueprints", Summary: "List the template catalogue.", Response: []blueprint.Summary(nil)},
	"GET /deploy/blueprints/{id}":          {Tag: "blueprints", Summary: "Read a template, and whether this install can deploy it.", Response: blueprintDetail{}},
	"GET /deploy/blueprints/{id}/versions": {Tag: "blueprints", Summary: "List the upstream versions a game template can be deployed at; other templates follow their image tag.", Response: gameserver.VersionList{}},
	"POST /deploy/blueprints/{id}/render":  {Tag: "blueprints", Summary: "Render a template with its inputs into the plan a draft would commit.", Request: blueprintRenderRequest{}, Response: (*deploy.BlueprintPlan)(nil)},

	// Game servers.
	"GET /deploy/{id}/game/":                  {Tag: "game", Summary: "Read a game server's overview: version, players online and its checks.", Response: gameOverview{}},
	"GET /deploy/{id}/game/players":           {Tag: "game", Summary: "List the players on a game server.", Response: (*gameserver.Players)(nil)},
	"GET /deploy/{id}/game/properties":        {Tag: "game", Summary: "Read a game server's properties file as the template describes it.", Response: gameProperties{}},
	"PUT /deploy/{id}/game/properties":        {Tag: "game", Summary: "Set game server properties, and say whether a restart is needed.", Request: gamePropertiesRequest{}, Response: shape{"applied": []string(nil), "restartRequired": false}},
	"POST /deploy/{id}/game/console":          {Tag: "game", Summary: "Send a console command to a game server.", Request: gameConsoleRequest{}, Response: (*gameserver.ConsoleResult)(nil)},
	"POST /deploy/{id}/game/players/{action}": {Tag: "game", Summary: "Kick, ban, pardon, op, deop or whitelist a player.", Request: gamePlayerRequest{}, Response: (*gameserver.ConsoleResult)(nil)},
	"POST /deploy/game/import/preview":        {Tag: "game", Summary: "Inspect an existing game server, a folder on this server or an uploaded archive, without changing it.", Request: gameImportRequest{}, Upload: "application/zip", Response: anyOf{(*gameserver.ImportPreview)(nil), shape{"root": "", "entries": 0, "totalBytes": int64(0), "paths": []string(nil)}}, Note: "A JSON body names a folder on the server; an application/zip body is the archive itself, answered with its root, entry count, size and paths."},

	// Saved credentials.
	"GET /deploy/credentials":            {Tag: "environments", Summary: "List the saved registry and Git credentials: names, kinds and hosts, never secrets.", Response: []deploy.CredentialSummary(nil)},
	"POST /deploy/credentials":           {Tag: "environments", Summary: "Save a registry or Git credential; the secret is sealed.", Request: deploy.CredentialCreateRequest{}, Response: (*deploy.CredentialSummary)(nil), Status: http.StatusCreated},
	"PUT /deploy/credentials/{id}":       {Tag: "environments", Summary: "Update a saved credential; a secret left out is kept.", Request: deploy.CredentialUpdateRequest{}, Response: (*deploy.CredentialSummary)(nil)},
	"POST /deploy/credentials/{id}/test": {Tag: "environments", Summary: "Try a saved credential against its registry or repository.", Request: deploy.CredentialTestRequest{}, Response: deploy.CredentialTestResult{}},
	"DELETE /deploy/credentials/{id}":    {Tag: "environments", Summary: "Delete a saved credential nothing uses.", Status: http.StatusNoContent, Note: "Destructive, with an ordinary confirmation."},
}

// requestFilterQuery is the filter the Requests pane sends, shared by the
// request list and its export.
var requestFilterQuery = []openapiQuery{
	{"limit", "integer", "How many requests to return at most."},
	{"path", "string", "Only requests whose path contains this."},
	{"host", "string", "Only requests to this host."},
	{"status", "string", "Only these statuses, comma-separated."},
	{"classes", "string", "Only these status classes (2xx, 3xx, 4xx, 5xx), comma-separated."},
	{"methods", "string", "Only these methods, comma-separated."},
	{"client", "string", "Only requests from this client address."},
	{"agent", "string", "Only requests whose user agent contains this."},
	{"referer", "string", "Only requests whose referer contains this."},
	{"pages", "string", "1 to keep page requests only, 0 to leave them out."},
	{"minMs", "integer", "Only requests that took at least this many milliseconds."},
	{"maxMs", "integer", "Only requests that took at most this many milliseconds."},
}

// openapiPathParam describes one {param} of a pattern. The same name means
// the same thing on every route but one: {id} is the deployment's except under
// the blueprint and credential routes.
func openapiPathParam(pattern, name string) map[string]any {
	integer := map[string]any{"type": "integer", "format": "int64"}
	var schema map[string]any
	var description string
	switch name {
	case "id":
		switch {
		case strings.HasPrefix(pattern, "/deploy/blueprints/"):
			schema, description = map[string]any{"type": "string"}, "The template's id."
		case strings.HasPrefix(pattern, "/deploy/credentials/"):
			schema, description = integer, "The saved credential's id."
		default:
			schema, description = integer, "The deployment's id."
		}
	case "env":
		schema, description = integer, "The environment's id."
	case "run":
		schema, description = integer, "The run's id."
	case "release":
		schema, description = integer, "The release's id."
	case "trigger", "triggerID":
		schema, description = integer, "The trigger's id."
	case "schedule":
		schema, description = integer, "The schedule's id."
	case "alert":
		schema, description = integer, "The traffic alert's id."
	case "channel":
		schema, description = integer, "The notification channel's id."
	case "drain":
		schema, description = integer, "The log drain's id."
	case "store":
		schema, description = integer, "The secret-store connection's id."
	case "approval":
		schema, description = integer, "The preview approval's id."
	case "number":
		schema, description = integer, "The pull request's number."
	case "draft":
		schema, description = map[string]any{"type": "string"}, "The draft's id."
	case "name":
		schema, description = map[string]any{"type": "string"}, "The variable's name."
	case "key":
		schema, description = map[string]any{"type": "string"}, "The legacy variable's key."
	case "page":
		schema, description = map[string]any{"type": "string"}, "Which of the route's pages: maintenance, or an error status such as 502."
	case "action":
		values := make([]any, 0)
		for _, v := range openapiEnums[reflect.TypeOf(gameserver.PlayerAction(""))] {
			values = append(values, v)
		}
		schema, description = map[string]any{"type": "string", "enum": values}, "The moderation action."
	default:
		schema, description = map[string]any{"type": "string"}, "The "+name+"."
	}
	return map[string]any{"name": name, "in": "path", "required": true, "description": description, "schema": schema}
}

var openapiParam = regexp.MustCompile(`\{([^}]+)\}`)

// requiresSession reports whether httpx.RequireSession is in a route's
// middleware chain, which is what makes a route a session's alone for an
// ordinary key. A deploy key passes it only through its line in the table.
func requiresSession(middlewares []func(http.Handler) http.Handler) bool {
	want := reflect.ValueOf(httpx.RequireSession).Pointer()
	for _, mw := range middlewares {
		if reflect.ValueOf(mw).Pointer() == want {
			return true
		}
	}
	return false
}

// openapiSecurity is who may call a route, read off the same two places the
// gate decides from: a session always; an ordinary API key unless the route
// requires a session; a deploy key when its line in deployKeyRoutes admits it.
// The second value is the x-deploy-key note: what a deploy key gets and why.
func openapiSecurity(method, pattern string, middlewares []func(http.Handler) http.Handler) ([]any, string) {
	if pattern == "/deploy/{id}/hooks/{triggerID}" {
		return []any{map[string]any{"webhookSignature": []any{}}}, "refused: the hook authenticates by its signature, not a key"
	}
	requirements := []any{map[string]any{"session": []any{}}}
	if !requiresSession(middlewares) {
		requirements = append(requirements, map[string]any{"apiKey": []any{}})
	}
	rule, listed := ruleForDeployKey(method, pattern)
	switch {
	case !listed:
		return requirements, "refused (key_scope): no line in deployKeyRoutes admits it"
	case rule.session != "":
		return requirements, "refused (session_required): " + rule.session
	case rule.deny != "":
		return requirements, "refused (key_scope): " + rule.deny
	case rule.every:
		return append(requirements, map[string]any{"deployKey": []any{}}), "allowed for a key that reaches every deployment; refused to one narrowed to some"
	}
	return append(requirements, map[string]any{"deployKey": []any{}}), "allowed"
}

// handlerName is the handler's method name, read off the function the router
// holds, for an operationId that names what the code calls it.
func handlerName(h http.Handler) string {
	v := reflect.ValueOf(h)
	if v.Kind() != reflect.Func {
		return ""
	}
	fn := runtime.FuncForPC(v.Pointer())
	if fn == nil {
		return ""
	}
	name := fn.Name()
	name = strings.TrimSuffix(name, "-fm")
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	if !strings.HasPrefix(name, "handle") || len(name) == len("handle") {
		return ""
	}
	name = strings.TrimPrefix(name, "handle")
	return strings.ToLower(name[:1]) + name[1:]
}

// pathOperationID is the operationId when the handler's name is not to be had
// or is shared: the method and the pattern's words.
func pathOperationID(method, pattern string) string {
	var sb strings.Builder
	sb.WriteString(strings.ToLower(method))
	for _, part := range strings.Split(strings.Trim(pattern, "/"), "/") {
		part = strings.Trim(part, "{}")
		for _, word := range strings.FieldsFunc(part, func(r rune) bool { return r == '-' || r == '.' || r == '_' }) {
			sb.WriteString(strings.ToUpper(word[:1]) + word[1:])
		}
	}
	return sb.String()
}

// buildDeployOpenAPI reads the deployment routes off the router and describes
// each from deployOperations and the handlers' types.
func buildDeployOpenAPI(root chi.Routes) *openapiDocument {
	type walked struct {
		method, pattern string
		handler         http.Handler
		middlewares     []func(http.Handler) http.Handler
	}
	var routes []walked
	_ = chi.Walk(root, func(method, pattern string, handler http.Handler, middlewares ...func(http.Handler) http.Handler) error {
		if !strings.HasPrefix(pattern, "/api/v1/deploy/") {
			return nil
		}
		routes = append(routes, walked{method, strings.TrimPrefix(pattern, "/api/v1"), handler, middlewares})
		return nil
	})
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].pattern != routes[j].pattern {
			return routes[i].pattern < routes[j].pattern
		}
		return routes[i].method < routes[j].method
	})

	doc := &openapiDocument{}
	schemas := newOpenAPISchemas()
	paths := map[string]any{}
	ids := map[string]bool{}
	described := map[string]bool{}
	for _, rt := range routes {
		key := rt.method + " " + rt.pattern
		described[key] = true
		op, ok := deployOperations[key]
		if !ok {
			doc.Problems = append(doc.Problems, key+" has no line in deployOperations (deploy_openapi.go): say what it does, what it takes and what it answers")
			op = deployOperation{Tag: "fleet", Summary: "This route has not been described yet."}
		}
		item, _ := paths[rt.pattern].(map[string]any)
		if item == nil {
			item = map[string]any{}
			var params []any
			for _, m := range openapiParam.FindAllStringSubmatch(rt.pattern, -1) {
				params = append(params, openapiPathParam(rt.pattern, m[1]))
			}
			if params != nil {
				item["parameters"] = params
			}
			paths[rt.pattern] = item
		}
		id := handlerName(rt.handler)
		if id == "" || ids[id] {
			id = pathOperationID(rt.method, rt.pattern)
		}
		for n := 2; ids[id]; n++ {
			id = fmt.Sprintf("%s%d", id, n)
		}
		ids[id] = true
		item[strings.ToLower(rt.method)] = operationObject(schemas, id, rt.method, rt.pattern, op, rt.middlewares)
	}
	for key := range deployOperations {
		if !described[key] {
			doc.Problems = append(doc.Problems, key+" is described in deployOperations but the router has no such route: rename or remove the line")
		}
	}
	sort.Strings(doc.Problems)

	tags := make([]any, 0, len(openapiTags))
	for _, tag := range openapiTags {
		tags = append(tags, map[string]any{"name": tag.Name, "description": tag.Description})
	}
	components := map[string]any{}
	for name, schema := range schemas.components {
		components[name] = schema
	}
	doc.Document = map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":   "Just Dashboard deployment API",
			"version": version.Version,
			"summary": "The deployment routes of a Just Dashboard, as its router mounts them.",
			"description": "Generated from the dashboard's own router and the Go types its handlers decode and encode, and checked against both by its tests, so it describes exactly what this build serves. " +
				"Every route is under /api/v1/deploy and takes one of three credentials: the browser session (the " + httpx.SessionCookie + " cookie, with the X-JD-CSRF: 1 header on every request that is not a GET), " +
				"an API key as a bearer token, or a deploy key as a bearer token, which reaches only the routes marked deployKey; x-deploy-key on each operation says what a deploy key gets there and why. " +
				"A property is present in an answer whenever its Go field has no omitempty, and may be left out of a request, where it is its zero value; a property that is a pointer, list or map in Go may be null. " +
				"Secrets are marked writeOnly: they are accepted by a write and read back by no route. Every refusal is the Error envelope under default.",
			"license": map[string]any{"name": "AGPL-3.0-only", "identifier": "AGPL-3.0-only"},
		},
		"servers": []any{map[string]any{"url": "/api/v1", "description": "This dashboard, at the address it is reached on."}},
		"tags":    tags,
		"paths":   paths,
		"components": map[string]any{
			"schemas": components,
			"responses": map[string]any{
				"Error": map[string]any{
					"description": "A refusal or failure: the code is what a client branches on, the message is written to be read.",
					"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{
						"type": "object",
						"properties": map[string]any{"error": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"code":    map[string]any{"type": "string", "description": "unauthorized, forbidden, session_required, key_scope, not_found, bad_request, revision_conflict and the route's own codes."},
								"message": map[string]any{"type": "string"},
								"phrase":  map[string]any{"type": "string", "description": "On a removal that takes a typed confirmation: the phrase to type."},
							},
							"required": []any{"code", "message"},
						}},
						"required": []any{"error"},
					}}},
				},
			},
			"securitySchemes": map[string]any{
				"session": map[string]any{"type": "apiKey", "in": "cookie", "name": httpx.SessionCookie,
					"description": "A signed-in browser session. Every request that is not a GET also carries the X-JD-CSRF: 1 header."},
				"apiKey": map[string]any{"type": "http", "scheme": "bearer",
					"description": "An API key made under Account → API keys, sent as Authorization: Bearer. It holds its owner's role and reaches every route that does not require a session."},
				"deployKey": map[string]any{"type": "http", "scheme": "bearer",
					"description": "A deploy key, made by an administrator under Account → API keys → Deploy key, sent as Authorization: Bearer. It reaches the operations marked with it and nothing else; a key narrowed to chosen deployments reaches only routes about those. Variables are write-only to it."},
				"webhookSignature": map[string]any{"type": "apiKey", "in": "header", "name": "X-JD-Signature-256",
					"description": "HMAC-SHA256 of the raw body with the trigger's secret, as a CI system sends it."},
			},
		},
	}
	raw, err := json.MarshalIndent(doc.Document, "", "  ")
	if err != nil {
		doc.Problems = append(doc.Problems, "the document does not encode: "+err.Error())
		raw = []byte("{}")
	}
	doc.JSON = append(raw, '\n')
	// YAML is written from the JSON tree, so the two are one document.
	var tree any
	_ = json.Unmarshal(doc.JSON, &tree)
	var yaml bytes.Buffer
	writeYAML(&yaml, tree, 0)
	doc.YAML = yaml.Bytes()
	return doc
}

// operationObject is one route's operation: parameters, body, answers and
// who may call it.
func operationObject(schemas *openapiSchemas, id, method, pattern string, op deployOperation, middlewares []func(http.Handler) http.Handler) map[string]any {
	object := map[string]any{"operationId": id, "summary": op.Summary, "tags": []any{op.Tag}}
	description := op.Note
	security, deployKey := openapiSecurity(method, pattern, middlewares)
	object["security"] = security
	object["x-deploy-key"] = deployKey
	if description != "" {
		object["description"] = description
	}
	if len(op.Query) > 0 {
		params := make([]any, 0, len(op.Query))
		for _, q := range op.Query {
			params = append(params, map[string]any{"name": q.Name, "in": "query", "required": false, "description": q.Description, "schema": map[string]any{"type": q.Type}})
		}
		object["parameters"] = params
	}
	if op.Request != nil {
		content := map[string]any{"application/json": map[string]any{"schema": schemas.schemaForValue(op.Request)}}
		if op.Upload != "" {
			content[op.Upload] = map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}}
		}
		object["requestBody"] = map[string]any{"required": !op.Optional, "content": content}
	}
	status := op.Status
	if status == 0 {
		status = http.StatusOK
	}
	responses := map[string]any{"default": map[string]any{"$ref": "#/components/responses/Error"}}
	switch {
	case op.Stream:
		responses["101"] = map[string]any{"description": "The connection is upgraded to a WebSocket that carries the events as JSON messages."}
	case op.Content != "":
		// A JSON answer that is not one of the handlers' types (this
		// description itself) is an object; text is a string; anything else
		// is bytes.
		schema := map[string]any{"type": "string"}
		switch {
		case op.Content == "application/json":
			schema = map[string]any{"type": "object"}
		case !strings.HasPrefix(op.Content, "text/") && op.Content != "application/yaml":
			schema["format"] = "binary"
		}
		responses[fmt.Sprint(status)] = map[string]any{"description": http.StatusText(status), "content": map[string]any{op.Content: map[string]any{"schema": schema}}}
	case op.Response == nil:
		responses[fmt.Sprint(status)] = map[string]any{"description": http.StatusText(status) + ": done, with no body."}
	default:
		responses[fmt.Sprint(status)] = map[string]any{"description": http.StatusText(status), "content": map[string]any{"application/json": map[string]any{"schema": schemas.schemaForValue(op.Response)}}}
	}
	object["responses"] = responses
	return object
}

// schemaForValue is the schema of a body or an answer: a zero value, a shape
// or an anyOf. A body or an answer is never null itself — a handler answers a
// missing thing with an error, not with null — so a pointer here is its
// element; inside a shape or a struct a pointer keeps its null.
func (s *openapiSchemas) schemaForValue(v any) map[string]any {
	switch value := v.(type) {
	case shape, anyOf:
		return s.schemaForNested(value)
	}
	t := reflect.TypeOf(v)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return s.schemaOf(t)
}

// schemaForNested is the schema of a value inside a shape: what the handler
// put under a key, null included where the Go value can be nil.
func (s *openapiSchemas) schemaForNested(v any) map[string]any {
	switch value := v.(type) {
	case shape:
		props := map[string]any{}
		for _, k := range openapiSortedKeys(value) {
			props[k] = s.schemaForNested(value[k])
		}
		return map[string]any{"type": "object", "properties": props, "description": "Assembled by the handler; these are its properties."}
	case anyOf:
		alternatives := make([]any, 0, len(value))
		for _, one := range value {
			alternatives = append(alternatives, s.schemaForValue(one))
		}
		return map[string]any{"anyOf": alternatives}
	}
	return s.schemaOf(reflect.TypeOf(v))
}

// deployOpenAPISource is the router one Routes call built, and the
// description read off it once somebody asks: building it walks every
// deployment route and reflects over every type they decode and encode.
type deployOpenAPISource struct {
	root chi.Routes
	once sync.Once
	doc  *openapiDocument
}

func (src *deployOpenAPISource) document() *openapiDocument {
	src.once.Do(func() { src.doc = buildDeployOpenAPI(src.root) })
	return src.doc
}

// handleDeployOpenAPIJSON and handleDeployOpenAPIYAML serve the description
// of the router Routes built. They read nothing from the request and nothing
// from the store: the document is the same for every caller.
func (s *Server) handleDeployOpenAPIJSON(w http.ResponseWriter, r *http.Request) error {
	return s.serveDeployOpenAPI(w, "application/json; charset=utf-8", func(doc *openapiDocument) []byte { return doc.JSON })
}

func (s *Server) handleDeployOpenAPIYAML(w http.ResponseWriter, r *http.Request) error {
	return s.serveDeployOpenAPI(w, "application/yaml; charset=utf-8", func(doc *openapiDocument) []byte { return doc.YAML })
}

func (s *Server) serveDeployOpenAPI(w http.ResponseWriter, contentType string, body func(*openapiDocument) []byte) error {
	src := s.deployOpenAPI.Load()
	if src == nil {
		return httpx.Internal(fmt.Errorf("the deployment API description was not built"))
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(body(src.document()))
	return err
}
