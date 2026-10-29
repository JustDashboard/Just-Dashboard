# Deployment implementation

`internal/deploy` owns desired deployment configuration, immutable releases, persistent runs,
steps, queue leases, sequenced events, triggers and cross-feature relationships. It orchestrates narrow
interfaces from Docker, Git, Proxy, Backups, Metrics, Logs, Files and Terminal; those packages remain the
only renderer/executor/validation authority for their feature.

- A run and its full step list commit before an enqueue answers `202`. Environments serialize work;
  random claim tokens and expiring leases fence stale workers. Restart recovery uses stored step evidence
  plus owning-feature evidence and never guesses that a non-idempotent side effect is safe to repeat.
- Worker capacity defaults to one heavy and two light slots and is bounded by
  `JD_DEPLOY_HEAVY_SLOTS` / `JD_DEPLOY_LIGHT_SLOTS` (1..8). Claims expire after
  `JD_DEPLOY_LEASE_TTL` (30s by default, 5s..5m); these are boot-time settings, not mutable project data.
  The engine looks for work every 250 ms and on every `Notify`; `ClaimNext` first asks with a plain read
  whether anything is queued, so an idle queue never takes the IMMEDIATE write transaction the claim
  needs.
- The only activation strategies are `blue_green` for an eligible proxy-owned stateless HTTP service and
  `stop_first` for Compose, fixed-port, game and exclusive-storage workloads. A failed candidate cannot
  replace the live release; ambiguous cutover evidence restores or stops for operator recovery. Either
  strategy can run a stateless release as `runtime.replicas` containers (1–8) behind one route
  ([replicas](canary-and-replicas.md)); everything below that speaks of a release's container means
  every one of them.
- Events commit before publish and are monotonically sequenced per run. Reconnect resumes after a sequence;
  compacted history begins with a `resync` snapshot, and a slow subscriber is disconnected rather than
  allowed to stall execution. Streams replay all retained history in pages of at most 5,000 stored
  events before handing off to live events, including the terminal tail of an already completed run.
- Reusing a run's `Idempotency-Key` with a different manual `sourceRevision` or `ref` returns
  `409 idempotency_conflict`. The comparison includes the persisted source-request metadata alongside
  the existing digest, so keys created before this check retain their behavior. Retrying the same ref
  returns the original run and frozen commit even if that ref has since moved.
- `deploy_projects.id` remains the deployment identity. The additive normalized schema and compatibility
  migration retain legacy route/hook/env/history behavior while the persistent engine and UI replace it.
  `PUT /deploy/{id}` keeps its legacy Compose contract byte for byte for a full body — every field
  present replaces `repoPath`/`branch`/`composeFile`/`preCommand`/`postCommand`/`enabled` exactly as
  before — but a body naming only `name` instead renames a project (legacy or normalized) without
  touching any other column: the same name rule draft intent validation uses, uniqueness enforced by
  the existing unique index on `deploy_projects.name` (archived projects hold only an internal
  tombstone there, never their display name), and a taken name answers `409 name_taken`. The rename shape
  refuses an archived project outright with `409 project_archived` rather than writing the requested name
  over that tombstone — a project's display name comes back only through `POST /deploy/{id}/unarchive`.
  Both shapes audit as `deploy.project.update`.
- Deployment-selected paths use a dedicated `files.Service` scoped to `JD_DEPLOY_ROOTS`; Git, Docker,
  Compose and builder argv use `hostexec.CommandInDir`. The sole shell boundary remains an immutable,
  admin-authored stored release task (including migrated pre/post commands) with a resolved working
  directory and explicit scoped environment; a task with `runner: "image"` runs that command inside a
  throwaway container of the release's own image rather than in the dashboard's shell.
- Creation is a revisioned, owner-scoped server draft. `GET /deploy/drafts` (session or deploy key; a deploy key lists
  the drafts it made, recorded in `deploy_drafts.key_id`, and a session every draft of its account's, its keys'
  included) lists the caller's own uncommitted, unexpired drafts newest first — id, name from intent, a one-line source summary,
  `currentStep`, `updatedAt`, `expiresAt` — so the new-project page can offer to resume one instead of
  starting a new setup over; a committed or expired draft has nothing left to resume and is excluded.
  `DELETE /deploy/drafts/{draft}` (session or deploy key, audited `deploy.draft.discard`) throws one away: every
  press of Import creates a draft, so an abandoned attempt would otherwise sit in that list for its
  whole thirty-day life and three tries at one repository would read as three pieces of unfinished
  work. Only the owner's own uncommitted setup can go — a committed draft is the record a project was
  planned from and answers `409 draft_committed`. The page also discards for itself: inspecting a
  second source, or pressing "Change source", throws away the setup being walked away from, because
  that is a different project rather than a revision of the first.
  Remote Git inspection resolves an exact ref into a
  private dashboard-owned bare mirror and detached temporary worktree; it never clones into or resets an
  operator checkout. Inspection's mirror is shallow and blob-filtered so planning stays bounded, so a
  release materializes through a second mirror that is neither: the run workspace is built by fetching the
  recorded object id into a fresh repository, because `git clone --local` ignores its local copy when the
  source is shallow and would silently produce an empty checkout. Registry inspection resolves a digest
  without pulling. An image no registry can name but this server holds — a Compose project's own build,
  a tag made by hand — is identified by its local image id instead (`SourceIdentity.local`, the registry
  lookup tried first), and its release tags that id under the release's own tag rather than pulling, so
  retention owns it like a built image; a Compose service image the registry cannot resolve at build time
  is taken from this server the same way (`source: "local"` on the resolved service). Planning-time Compose validation uses private temporary files, an explicit empty env
  file and inert placeholders for detected variable names, so the backend environment and a checkout
  `.env` cannot influence the result.
- `/deploy/new` is one page, held to the window at `xl`. Unfinished setups (`GET /deploy/drafts`) are
  offered for resumption behind a counted button beside the question; a source strip offers a Git
  repository (connected GitHub list or a pasted URL), Docker (what this server already has, or a
  registry reference), a reviewed template (shelved client-side by topic, with the server's `category`
  as the fallback shelf for a blueprint the frontend does not name) and a database. A Compose stack has
  no tab of its own since 2026-10-05: one is deployed from the repository that holds its file (the
  Project step's *Deploy as a Compose stack*), from its files on this server through the Docker tab, or
  resumed as an unfinished setup. The Docker tab (`new-project/source-image.tsx` over
  `server-sources.ts`) lists the Compose stacks whose files are still on disk — not the dashboard's own,
  nor one a deployment already runs — each deployed from those files as a `compose_local` source, so
  the stack's variables, volumes and network come with it; and every image tag, each saying which
  containers run it. A row deploys its image alone; images ticked together become one project, a
  generated `compose_paste` stack with a service per image (named after the container's Compose service
  where it has one, restarting unless stopped). A deployment runs its own copy: a stack that fixes
  `container_name` or host ports collides with the copy already running until that one is stopped.
  Choosing a source creates a draft, saves the intent and source, and runs detection in one action; when
  detection finds more than one candidate they are offered ranked, each saying why it ranks where it
  does (an example, a docs site, not a service), as a choice that re-runs detection with
  `selectedId`. Each configure screen reads the plan down a rail beside the form
  (`new-project/plan-rail.tsx` over `plan-reading.ts`, the run page's rail before the run): the four
  steps under their segments of the spine, each holding the parts of the plan it decides — source and
  build; container, address, health check, resources and storage; the environment; the server's check
  and automatic deployment — each on the tile of the product it is, with what it says now and, where
  there is one, the command, request, path or variable name as code. Every row is pressable and opens
  the fields that decide it, which is what puts the release strategy and the memory limit on screen
  without opening a fold to find them; an unbounded container reads as "No memory or CPU limit" there
  rather than as silence, and a part that wants a look before Deploy is marked in its tile's corner.
  The form itself holds the name, the type, the detected build and output
  settings, environment variables, an optional database, the public address (with the hostname
  suggestion and certificate readiness), automatic deployment for a Git source, and an Advanced
  disclosure carrying the runtime, health check,
  storage, release task, build secret and container fields. The type is offered for an image source
  as well as a Git one: the profile is plan-time intent that only preflight reads, and offering it
  only for Git is why an HTTP application shipped as a container could not be told it was one —
  it deployed stop-first, with no health gate and no finding to say so. Choosing a gated type seeds
  the readiness check and candidate-first activation that preflight requires for it. A name a live
  project already holds is reported by `GET /deploy/hostname?name=` as `nameTaken` and flagged beside
  the field while it is typed, rather than refused by the schema's UNIQUE constraint at commit after
  the whole setup has been filled in. What detection could not settle for itself (`needsDecision`)
  is shown on the source row, and it decides which screen the sequence opens on — so a decision is
  an *unanswered question*, emitted only when the thing it names is genuinely open. A Dockerfile
  whose single literal `EXPOSE` gave a port, an image whose exposure was read, a Go module (the
  recipe resolves its own executable and starts the binary it writes, and preflight asks a service
  for no readiness gate), a Deno project (the port is `Deno.serve`'s own default) and a static site
  (a marker's root is the directory its files were found in, so the candidate's root *is* the one
  holding the `index.html`, which is what an empty output directory serves) record evidence instead;
  a Dockerfile naming no port or several, an image this host holds no copy of, a Compose file in a
  Git checkout that is a deployment of its own (it is read for its services' shape only, and built only
  as a Compose source), and a submodule on another host still owe one — a submodule on the
  repository's own host and LFS files under the build root switch themselves on at inspection, in the
  Source section beside the branch. A source that is not a service, or the upstream repository of an
  application the template catalogue packages, also opens the project screen. Everything else that is open is carried by the plan
  rather than by prose, which is what lets it open the screen that owns the field: an unset
  `internalPort` opens the runtime screen (and the port field seeds the readiness check a gated
  profile needs, the way choosing the type does); a required plan variable with no reference, value
  or generation — a Compose `${VAR}`, a blueprint input, a duplicated project's copied variable —
  opens the variables screen with its references fold already open, the same rule preflight refuses
  at Deploy as `variable_required_*`; and `nameTaken` opens the project screen, so a name a live
  project already holds is corrected before Deploy rather than by it. Nothing open means Review,
  with the plan read back and Deploy under it. **Review saves the configuration and runs preflight on
  arrival**, not under the button: preflight used to run inside the press, so the screen asking "is
  this right" had checked nothing by the time it was read, its findings landed under a button the
  reader had already pressed, and the first press of Deploy was really a check. It opens on what the server said — the findings to fix and the warnings to acknowledge under one
  line saying what stands between the plan and Deploy — and reads the plan back as what the rail
  beside it cannot carry, each drawn as a row of what it is — the mounts kept between rebuilds and their backup
  coverage, the variables generated on this server and their length, the readiness check's target and
  budget, and what the cutover strategy costs — plus every preflight `pass` as a checked line, where
  before only `blocked`, `decision` and `warning` were drawn and a plan with nothing wrong with it
  showed four facts. A stored result is keyed to the plan it was computed for as well as to the
  draft, so going back to change the port and returning re-checks rather than reading back what this
  server agreed to about the previous plan. The arrival check runs once per plan: a check that fails
  is shown and not retried on its own, and Deploy is the retry. Review and Deploy save edited intent,
  configuration and encrypted environment inputs before preflight. Commit creates the project and its
  initial variable revisions in one transaction, then Deploy enqueues the first run; Save only stops
  after commit. Required variables are checked against those same values, so an entered connection
  string cannot be rejected merely because it used to be imported after commit. The saved draft revision is adopted before preflight, so a failed preflight never
  strands the draft, and a `draft_revision_conflict` re-reads the draft once. `?draft=` resumes a
  draft (including one produced by `POST /deploy/{id}/duplicate`); a draft saved without a
  configuration — every draft abandoned from Configure, since the configuration is saved at Deploy —
  is re-detected rather than refused. `?mode=advanced` opens Advanced. For a Git source the commit carries a `gitPolicy`
  (`automatic`, `watchInclude`, `watchExclude`, `commitStatuses`), written as the environment's
  `deploy_git_policies` row at revision 1 inside the same transaction; no decision writes no row, so
  every caller that does not ask keeps the defaults in `gitDeploymentPolicy` exactly as they were.
  A Git deployment polls its branch from the moment it exists and the default is to deploy every
  push, so until this travelled with the commit "manual only" was a setting reachable only after a
  production service had already released a commit nobody meant to ship. Saved credentials are picked from
  `GET /deploy/credentials`. The page is kept for the tab (`useSessionState`,
  [`../frontend/data-theming.md`](../frontend/data-theming.md)): the source tab and its form, and the
  configure screen's flow, findings and Advanced disclosure, survive a walk to another page and a
  reload until the project is created or the source is changed. `?step=source|project|runtime|variables|review`
  makes step changes browser history entries; Back to the chooser preserves the inspected flow and
  Forward reopens it. Each step remembers field focus and scroll indices without field values,
  as detailed in [`../frontend/workspace-interactions.md`](../frontend/workspace-interactions.md).
  A remembered flow whose draft has expired is dropped with a notice on the way in. Unsaved environment
  values and visitor passwords never enter the URL or browser storage. Configuration saves stage environment values in the additive
  `deploy_drafts.environment_enc` column, sealed with the install key; only `environmentKeys` returns
  to the browser, including names whose explicit empty override must survive a reload. An omitted
  `dotenv` preserves staged inputs, an explicit empty document clears them,
  and `retainEnvironmentKeys` preserves individually named masked values when a resumed form submits
  new input. New values override retained values, plain defaults and generation requests while keeping
  declared scopes. Original defaults and generators remain available if the override is removed; commit
  seals every supplied value as secret regardless of the fallback's sensitivity. A source change clears
  staged values; reinspection of another branch of the same
  repository preserves them. Invalid parsing, revision conflicts and failed commits cannot partially
  save credentials. Required-variable and reference-graph checks use the same effective values as commit.
  The environment section opens with the variables detection found the source reading — the template's
  example as the placeholder, the file it was read from beside the key — and a detected row left empty
  and set up by nothing is skipped at submit rather than set to nothing. A row detection set up says
  how the plan answers it (`detectedVariableDeclarations`): a secret generated when the project is
  created (never shown in the browser), an address that follows the domain (held out of each save until
  a domain is planned, and put back into the plan the server hands back — `withHeldDomainVariables` —
  so a domain added after Review's check still binds it), a documented default, or a secret to paste; a required name is declared required, a browser-compiled name is tagged `public`,
  and a typed value pointing at localhost is flagged under the field. Review lists the generated
  secrets by shape and the plain values the plan carries. Each database the source connects to is one
  button that opens the database sheet on that engine (PostgreSQL with pgvector or PostGIS when the
  schema needs one) and variable, in the connection shape the application parses; a hosted-only driver
  asks for its provider's connection string instead, and a suggestion stays offered while the variable
  holds only a localhost value. The framework is named as its own
  documentation spells it (`frameworkLabel`), a Python recipe shows its interpreter field, and static
  output shows the single-page switch. `?repo=<clone url>&ref=<branch>` arrives on the Git tab with
  the URL filled in (only an `https://`, `ssh://` or `git@` URL is accepted), which is what a deploy
  link in a README points at. `?template=<blueprint id>` (only an identifier's shape is taken) and
  `?image=<reference>` arrive on those tabs with the blueprint chosen or the reference filled in, which
  is where the project step sends the upstream repository of an application the catalogue packages.
  Reviewed blueprints deploy as image releases (below), the two Minecraft game servers included
  ([game servers](game-servers.md)); a definition the release path cannot run — today only one that
  downloads an artifact through a version source, with no pinned checksum — stays preview-only, the
  catalogue names the reason per blueprint, and direct API calls are refused with the same reason
  before deployment work is persisted. The screens share `deployment-defaults.ts` and `deployment-findings.tsx` so a default
  one flow relies on cannot be missing from the other, and a refused plan reads identically in both.
  Detected web and static plans carry a required HTTP readiness check: preflight raises
  `readiness_missing` as a *decision* for those profiles, so an empty check list made the most
  ordinary deployment there is unsavable over a control the operator was never shown. The check is the
  candidate's detected `readiness` — its path, whether any answer counts, Docker health for a
  Dockerfile `HEALTHCHECK` command, and a slow start's budget (recipes.md, "Readiness, workers and
  start commands") — and GET `/` expecting a 2xx only when detection found nothing. A service or a
  published container (`service`, `image`) whose port detection knows is proposed a gate the same way
  (`defaultChecks`, `checksForRuntime`): the image's own health where it declares one, HTTP on a
  health path where the source names one, else a required TCP check on that port, as the catalogue's
  starters carry; the proposal is a draft default, listed under Advanced and removable there, never
  added to a saved plan. A readiness check is **not** required of those profiles: without one the
  release deploys and preflight says what that costs, as a warning (`cutover_unverified`, below).
- A draft route distinguishes why an id did not answer: `403 draft_forbidden` for another user's draft,
  `410 draft_expired` for one past `expiresAt`, and `404 draft_not_found` only for an id that names
  nothing at all — the first two used to collapse into the third, which hid an ownership refusal behind
  the same message as a typo'd id. `POST …/drafts/{draft}/detect` with a `selectedId` absent from the
  detected candidates names the id it did not recognize in its `422 invalid_plan` message instead of
  repeating the generic "invalid deployment plan" text. `GET …/variables/{name}/reveal` for a name that
  exists in no active variable answers `404 variable_not_found`; a name that fails `ValidateEnvKey`
  outright (malformed, not merely absent) stays `400 invalid_variable`.
- `GET /git/github/repos` and `GET /git/github/branches` list what the dashboard's own gh credential can
  reach, so a repository is chosen rather than transcribed. Both take no `?path=`: the credential
  consulted is the one every deployment clone will use, and the repository being chosen has not been
  cloned onto this server yet. A repository name is validated as owner/name before it reaches gh's argv
  or a REST path.
- `GET /deploy/hostname` proposes a public address and reports what HTTPS would take. A wildcard
  certificate the host already holds wins; failing that the answer is `<slug>-<suffix>.<a-b-c-d>.sslip.io`,
  which resolves to this server with no DNS record to create. The suffix is
  `HMAC-SHA256(sha256("jd-derive-v1" || masterKey), "deploy.hostname" || 0x00 || ToLower(TrimSpace(name)))`
  truncated to three bytes, not a random draw, so the same name proposes the same hostname on every call —
  a screen that re-fetches must not see a different address. If that
  deterministic hostname is already a managed domain of another active project, the HMAC input gains a
  `#2`, `#3`, … counter and is recomputed until a free one is found. `covered` is whether a certificate
  already covers the name — the only thing activation accepts — and `certificateMethod` is the HTTP-01
  challenge this host could issue one with. For a typed wildcard (`?hostname=*.example.com`) asked by
  `system.admin` it is `dns-01` with `certificateDnsProvider`, the provider whose saved credentials the
  run would prove it with, or empty with the run's own refusal in `certificateIssue` — one decision,
  `proxysvc.DeploymentDNSProvider`, answers both. Other roles get a sentence saying a DNS challenge
  proves it, because the answer looks up the zone's nameservers for a name the caller chose, which
  stays with administrators for the reason `resolves` does. Both are reported so the screen can say what the run
  will do, not so the operator has to do it. Asked about a hostname the operator typed (`?hostname=`), it also
  answers `address`, this server's public IPv4, which is the A record the name needs, and — for
  `system.admin` only — `resolves`, whether the name already points here, taken from the reverse
  proxy's own DNS check so the Domains sheet and the proxy page never disagree about one name. It is
  a pointer for the reason `nameTaken` is: a host with no public address cannot make the comparison,
  and a lookup the request ran out of time for is no answer, so absent is not false. It stays with
  administrators because resolving a name the caller chose is traffic the caller directs, which the
  proxy page's own check already keeps at `system.admin`.
  Address selection excludes private, loopback, link-local and CGNAT (`100.64.0.0/10`) addresses,
  including Tailscale addresses. Go's `IsGlobalUnicast` and `IsPrivate` alone do not exclude CGNAT.
  A host without a public interface address gets no generated fallback; hosts behind provider NAT
  need an operator-supplied domain pointing to the provider's public address.
- `provision_certificate` is a step of the release path, ordered after `backup_gate` and before
  `start_candidate`. It reads the frozen release snapshot, and for every planned HTTPS domain resolves an
  existing certificate or orders one through `proxysvc.EnsureDeploymentCertificate`. Its position is the
  safety property: a run that cannot obtain the certificate its own plan asked for stops with no
  candidate container to stop, restore or explain. A plan with no HTTPS domain is `skipped`; a host
  without certbot is `unavailable` (`certificate_unavailable`), which is terminal but is not a failure of
  the deployment's own work; a refused order is `failed` (`certificate_issue_failed`). Redeploy and
  rollback carry the step; restart and preview-remove do not, because neither applies a route.
  Reuse comes first, and covers wildcards both ways: a name under an existing wildcard (a preview's
  `pr-7.preview.example.com` under `*.preview.example.com`) and a wildcard plan name an existing
  certificate lists both ask the authority for nothing. Otherwise a plain name is ordered over HTTP-01,
  and a plan with a wildcard among its names is ordered whole over DNS-01
  (`proxysvc/deployment_certificate_dns.go`): only DNS proves a wildcard, and activation serves one
  certificate for every name. The DNS challenge uses credentials the Certificates page already saved;
  the run never asks for one and never reads one — certbot is handed the 0600 file's path, as the page
  hands it. Credentials are kept per provider, not per zone, so the provider is the one the zone's
  nameservers name (`*.ns.cloudflare.com` is Cloudflare, `ns-N.awsdns-N.*` Route 53, and so on through
  the closed set; RFC 2136 servers are the operator's own and name nobody), failing that the single
  provider with saved credentials and a plugin in the certbot that runs. No saved credentials, a zone
  hosted by a provider that has none, its plugin missing, or two candidates the nameservers cannot tell
  apart is `unavailable` (`certificate_wildcard_unavailable`) before certbot is asked: a wrong guess is a
  failed validation the authority counts. The message names what to save and where — `Proxy →
  Certificates → DNS challenge providers (/proxy/certificates)` — so it reads the same in a notification
  or a commit status. The order runs through the Certificates page's certbot (`CertbotCommand`, the
  host's when it has one, with the Route 53 environment), from the configured authority
  (`JD_ACME_DIRECTORY`'s `--server`), bounded at five minutes plus the provider's propagation wait. The
  lineage it leaves records the plugin and the credentials file in its renewal configuration, so
  certbot's own schedule renews it like any lineage the page issued, and the page lists it with the
  same renewal checks (a plugin that later goes missing is a `willFail`). A host whose deployments the
  Docker Caddy ingress serves orders the wildcard the same way: the ingress serves a copy of the
  lineage from a `*.zone` site with an explicit `tls`, and the dashboard refreshes that copy when
  certbot renews — see the [ingress decision](caddy-ingress.md#wildcards). Every order a run places —
  issued, or refused by the authority — is audited as `certificates.deployment.issue` (actor `system`;
  the names, method, DNS provider and lineage, never a credential); a reuse or a refusal before
  ordering changes nothing and is not. Preflight asks the same question for an uncovered wildcard on
  either proxy: `certificate_automatic` naming the provider, or `certificate_wildcard_unavailable`,
  blocked with the refusal and a deep link to `/proxy/certificates`; a covered one is
  `certificate_available`.
  HTTP-01 issuance (`issueDeploymentHTTP`) runs through the same certbot — whose plugins
  `deploymentChallenge` reads, against probe directories, to choose webroot or standalone — names the
  same directory (`deploymentHTTPIssueArgs` appends `ACMEDirectory.certbotArgs`), skips the public-DNS
  check for a private authority as the Caddy path does, and is bounded at five minutes. Both are
  serialized against route application so two
  runs cannot race one account into a rate limit, and pass `--keep-until-expiring` so a redeploy inside
  the renewal window asks the authority for nothing. **One certbot at a time** (`proxysvc/certbot_gate.go`):
  certbot holds a lock on its configuration directory and a second certbot fails on it at once, so
  every certbot the dashboard runs takes the one turn `HoldCertbot` hands out — the Certificates page's
  jobs (`startCertbotJob` wraps every `certbot.` job's runner, renewals and the timer's service run
  included) and a run's HTTP-01 and DNS-01 orders (`holdCertbotFor`). A run that finds the page's job
  holding it waits, within the step's own five-minute bound (plus the propagation wait over DNS-01),
  with a transcript line naming the holder — `Waiting for certbot, which is busy with Renewing
  app.example.com (started by alice from the Certificates page); the certificate step continues when it
  finishes` — and then re-checks for a covering certificate before ordering, since the job may have
  issued exactly that; a wait that outlasts the bound fails the step as `certificate_certbot_busy`, in
  the step's own words (what it waited for and for how long, and to deploy again once that has
  finished), never certbot's "another instance is already running". The page's job is accepted as it
  always was, and a job that finds a run ordering waits in its console the same way, naming the run;
  two of the page's jobs are still kept apart by the job family's `409 certbot_busy`. The turn is taken
  before the route lock, never inside it, because the job holding the turn reloads nginx under that
  lock when it is done. What needs no certbot — a covering certificate, the Docker Caddy ingress, a
  refusal — waits for nobody. certbot's own lock still arbitrates against what the dashboard does not
  run, the renewal timer first among them. A first order on a host with no ACME account *with that directory*
  (`acmeAccountExists`, which files accounts by directory as certbot does; any
  entry under `accounts/` used to count on the HTTP path, so a host registered with Let's Encrypt
  ordering from a private authority skipped registering and was refused for want of a contact)
  registers without a contact address; an operator who wants expiry mail registers once on the
  Certificates page and every later order reuses that account.
  `TestLiveDeploymentHTTP01OrdersFromTheConfiguredDirectory` proves the HTTP-01 order against an
  isolated Pebble: with a Let's Encrypt account already on disk, the run registers with Pebble, the
  lineage's `server =` is Pebble's directory, the chain verifies against the root that Pebble minted,
  and the next order finds Pebble's account rather than Let's Encrypt's. certbot's exit code is not the evidence:
  the certificate is read back and must cover the exact names before the step passes. Step evidence
  carries the lineage name, method, outcome and, over DNS, the provider's name — never a key path, key
  material or a credential. `JD_DEPLOY_LIVE=1 go test ./internal/deploy -run
  TestLiveCertificateStepProvesAWildcardOverDNS01` and `./internal/proxysvc -run
  TestLiveDeploymentWildcardIsProvedOverDNS01AndServed` prove it against Pebble validating real DNS-01
  records in a BIND (`proxysvc/acmetest`): refused without credentials, issued with them, reused by a
  preview — on an nginx host and, through a real `caddy:2-alpine` used by name, on the Docker Caddy
  ingress — renewed by `certbot renew` from the lineage's own configuration, and served by nginx on
  the wildcard's route and the preview's, chaining to that Pebble's root.
  `TestLiveDockerCaddyServesAWildcardProvedOverDNS01` is the same journey served by the ingress, the
  renewal refresh included ([ingress decision](caddy-ingress.md#renewal)).
  Before a new HTTP-01 order, a bounded DNS lookup rejects unresolved names and non-public destinations
  with a corrective message, including old saved sslip.io names based on a Tailscale address. Existing
  certificates are reused before this check, so private services can still use a certificate provisioned
  through DNS-01 for a domain the operator controls. Public DNS alone does not prove inbound port 80 is
  reachable; Certbot remains the validation authority. Errors retain ACME `Detail:` messages or the
  underlying failure line instead of Certbot's generic help footer. Correcting a saved hostname requires
  a new release; retrying an old run retains its frozen hostname.
- HTTP-01 selection consults both Certbot's actual plugins and TCP port 80 ownership. A running host
  nginx uses `webroot` at `/srv/just-dashboard-acme`, shared by the host and backend container. For new
  names, Proxy snapshots and installs a temporary challenge-only site, validates and reloads nginx,
  then reads back a random probe file through each existing listener with the requested Host header.
  Listener bind scope is preserved. Asynchronous nginx reloads are given a bounded probe retry.
  Existing sites that already serve the shared challenge path are reused; conflicting sites are never
  overwritten. Cleanup restores the snapshot using a cancellation-independent context and a cleanup
  failure prevents a successful certificate step. This verifies local routing, not public reachability.
  Deployment TLS routes retain that webroot for renewal; `SiteSpec.managedAcme` and the site parser
  preserve it through Proxy form edits, while ordinary sites keep their existing `/var/www/html` default.
  Standalone is selected only when no TCP port 80 listener is observed. Other or unidentified owners
  produce an actionable error instead of an attempted bind or a stopped server. Hostname suggestions
  use this same selector. `JD_DEPLOY_LIVE=1 go test ./internal/proxysvc -run TestLiveDeploymentWebroot
  -count=1 -v` verifies real nginx routing, continued service and cleanup on an isolated loopback port.
- The project context actions menu exposes **Archive deployment** for the destructive capability on both legacy and
  normalized projects, disabled during an active run. It uses the existing `DELETE /deploy/{id}` archive
  contract with ordinary confirmation, returns to the fleet after success, and keeps the dialog open
  on error. The confirmation explicitly states that history, runtime, routes and data are retained;
  Settings → Danger zone provides the separately previewed path for removing managed resources.
- Activation is unchanged and still the last word: it resolves an already-issued certificate/key pair
  through Proxy and fails closed if none exists. `provision_certificate` only makes that resolution
  succeed for a name the operator has just asked to publish.
- `GET /databases/{id}/url` is an admin read with its own audit entry and `Cache-Control: no-store`.
  Its default (or `?target=host`) returns the saved connection unchanged; `?target=public` swaps a
  loopback host for the machine's public address (see the databases guide). `?target=container` prepares
  an application URL: a known database's loopback binding is matched to its Docker container and
  internal port, with a stable `db-ID.jd.internal` hostname. The binding must match the saved loopback IP; ambiguous
  localhost bindings are refused. It never publishes a new port, attaches networks, or changes the
  saved connection. An unreachable loopback service and SQLite fail with an actionable error.
  Remote URLs retain their credentials, options, SRV discovery and seed lists. Plain TCP MySQL driver
  strings become `mysql://` URLs; driver-specific query/socket options require manual configuration
  instead of being silently discarded. Setup saves a typed database reference; activation joins an owned
  environment network and reconciliation repairs the DNS alias after a matching container replacement.
  Existing literal IP variables require reconnecting once. See [database networks](database-networks.md).
- A JavaScript package's install is one plan (`deploy/build_node_install.go`) that detection,
  preflight and the recipe share, computed from lockfiles and package-manager configuration read as data
  under their own budget (`build_node_lockfile.go`), each lockfile and workspace manifest parsed once per
  detection however many members compare against it. Detection records each committed lockfile compared
  with `package.json` (`lockfiles`: `in_sync`/`stale`/`unknown` with the drift named), the plan under each
  of the four managers with that manager's build and start commands and its preflight findings
  (`nodeInstalls`), and the Node release (`nodeVersion`: the nearest version file or the `package.json`
  volta/devEngines/engines field, on the digest-pinned `node:20`/`22`/`24` catalogue, 22 by default —
  `build_node_runtime.go` — which the plan's `build.nodeVersion` outranks); competing lockfiles resolve through the build
  setting, the manifest's declaration, the one lockfile a frozen install accepts, the one in sync, then
  manager-exclusive files, and only a tie is `package_manager_ambiguous`. Preflight
  (`preflight_node.go`) judges `configuration.build.packageManager` — or the resolved manager when it is
  empty — from that record, so a stale chosen lockfile is `lockfile_out_of_sync` before Deploy, and the
  recipe installs unfrozen instead of failing a frozen install. pnpm and Yarn Berry releases are pinned
  (declaration, or `nodeManagerReleases` keyed by lockfile format) into a `toolchain` stage the server's
  runtime stage shares, Bun is copied beside Node rather than replacing it, and a workspace member
  prepares from its workspace root (`ArtifactBuilder.PrepareWithin`, `prepared.contextDirectory`;
  `workspace_lockfile` names it before Deploy, and a member directory the Dockerfile cannot carry unquoted
  is `workspace_member_path_unsupported`). The
  UI swaps whole commands between managers from `nodeInstalls`, "From the lockfile" follows the resolved
  manager, and at build time a saved command whose plain runner names another manager runs through the
  resolved one with a logged note (`prepared.notes`, `prepared.buildCommand`/`startCommand`) and a
  `runner_mismatch` preflight warning. The configuration read carries `detected`, the stored candidate
  the build still describes, so Build settings show which lockfile matches. The contract is
  [the recipe guide](recipes.md#javascript-installs).
- Detection is a catalogue, not a handful of special cases. `deploy/frameworks_node.go` names Strapi,
  Medusa, Directus, KeystoneJS, AdonisJS, RedwoodJS, Next.js, SvelteKit, Astro, Nuxt, Remix, React Router,
  SolidStart, TanStack Start, Nitro, Qwik City, Analog, Angular, NestJS, Vike, Waku, Gatsby, Docusaurus,
  VitePress, VuePress, Rspress, Eleventy, Hexo, Slidev, Create React App, Vue CLI, Ember, Parcel, Rsbuild,
  Rspack, Farm, webpack and Vite, in an order where a meta-framework built on Vite is read before Vite
  itself, each with its serving mode (site output or server start command), port, runtime environment
  and the entry file the generated Dockerfile checks after the build. Each resolution reads the
  framework's configuration as text (`frameworks_node_config.go`: next.config's output, the adapter
  svelte.config imports, Astro's output and mode, `ssr: false`, a Nitro preset, Vite's base and outDir,
  Nest's layout), passes over a start script that runs a development server
  (`frameworks_node_scripts.go`), and says what it decided as evidence and, for preflight, as
  `nodeBuild.findings` (with `nodeBuild.devScripts`, the scripts that start a development server);
  `build_node_frameworks.go` renders what the plan's own commands need around the build (adapter-node for
  adapter-auto, `NITRO_PRESET`, Next.js standalone assets, the fallback page a static site's framework
  writes, the entry check). An Nx workspace's application projects are candidates at its root
  (`frameworks_node_nx.go`), and a workspace member builds its workspace dependencies first
  (`detect_node_start.go`). `frameworks_python.go` names Django, FastAPI, Litestar,
  Starlette, Sanic, Quart, Falcon, Bottle, Flask, aiohttp, Tornado and the application frameworks
  (Gradio, Chainlit, NiceGUI, Reflex, Mesop, Dash, Panel, Streamlit) from what the project itself
  declares, and finds the application object in the conventional entry files, the root's other scripts
  and the module a runner script imports its factory from; the manifests themselves (requirement
  layouts and includes, pyproject, Pipfile, the uv/Poetry/PDM locks and their drift, setup files, conda
  environments) are read by `detect_python_manifests.go`, `detect_python_lock.go` and
  `detect_python_project.go`, and what the recipe needs beyond the framework is recorded on the
  candidate as `python` (`detect_python.go`) and judged by `preflight_python.go`;
  `frameworks_rust.go`, `frameworks_java.go`, `frameworks_dotnet.go` and `frameworks_deno.go` read
  `Cargo.toml`, `pom.xml`/`build.gradle(.kts)`, `*.csproj`/`*.fsproj`/`*.vbproj` and `deno.json(c)`;
  `detect_php.go` and `detect_deno.go` read, after the walk and under budgets of their own, what those
  recipes decide from beyond the manifest (the lock, the declared release, the code's extension calls, a
  WordPress tree's shape) through one reader that detection and preparation share, and record it as the
  candidate's `php` and `deno` facts for preflight; only a WordPress theme's or plugin's header at the top
  of the checkout is read by the walk itself (at most 32 file heads), since nothing else names a block
  theme's or a create-block plugin's directory. The
  JVM and .NET builds are read through one bounded, symlink-refusing reader over the checkout
  (`build_files.go`) that detection and the recipe share — the Maven reactor and parents, the Gradle
  settings root and version catalog, the .NET props chain, referenced projects and `global.json`
  (`detect_maven.go`, `detect_gradle.go`, `detect_jvm.go`, `detect_dotnet.go`) — so a module or solution
  project builds from the directory that owns it (`PrepareWithin`, `prepared.contextDirectory`) and the
  candidate keeps `javaBuild`/`dotnetBuild` for preflight (`preflight_compiled.go`, which judges
  `build.javaVersion`/`build.dotnetVersion`). `detect_compiled_layout.go` sets aside library modules,
  aggregator POMs, Gradle build logic (`buildSrc`, `build-logic`, convention-plugin projects), test
  projects and Aspire AppHosts before the repository-shape passes rank what is left. A `Procfile`'s `web:`
  process outranks every guess, and another platform's deployment file (`fly.toml`, `render.yaml`,
  `app.json`, Kamal's `config/deploy.yml`, …) outranks the framework's defaults. The candidate carries `spaFallback` (a client-routed site's nginx
  fallback), `pythonVersion`, `unpinnedDependencies` (a `dependencies_unpinned` preflight warning,
  never a refusal), `variables` (the environment names the source reads, with example values, where
  each was read, and how each is supplied and read — `setup`, `generateFormat`, `domainTemplate`,
  `defaultValue`, `phase`, `browserInlined`, `required`, `requiredRead`, `localhostIn`;
  `env_discovery.go`, `env_discovery_languages.go`, `detect_variables.go`), `databases` (the engines
  its manifests and documented URLs name, each with the variable the connection belongs in and its
  `format`, `extensions`, `hosted` driver and Rails `alsoVariables`; `detect_databases.go`),
  `browserPrefixes` and `environmentNotes` (facts preflight answers: a committed Django key, a fatal
  dotenv load, an identity provider's callback, Rails credentials), `listen` (where the source says its
  server listens: a port it fixes, whether it reads PORT, a loopback bind, each naming its file and line
  — `detect_listen.go`, `detect_network.go`) and `networkVariables` (plain runtime variables the proxy
  decides: `AUTH_TRUST_HOST`, `NEXTAUTH_URL` on a domain template, `HOST`), and `go` and `rust` (how a
  Go module or a Rust crate builds beyond its toolchain: its build context, cgo, dependency sources,
  embeds and generated code; its Cargo workspace, binaries, native crates, sqlx and lockfile — from the
  same `planGoBuild` and `readCargoCrate` the recipes run, bounded by `validateDetectedGoBuild` and
  `validateDetectedRustBuild`). The form declares a set-up
  variable once: the classification's declaration first, then a network variable for a name it did not
  set up. The environment is described last for each root, after the state, readiness and network
  passes, so its classification sees the ports and frameworks they settled. A repository's container
  candidates go through the same passes beside the recipe candidates of their build context, each
  reading its own Dockerfile only through the stage it builds (`attachBuiltDockerfiles`), so a
  development stage after it never supplies the health check, volumes or command. A Dockerfile candidate at a
  Rails or Phoenix root is named that framework, and Phoenix's release image gets port 4000 when neither
  its `EXPOSE` nor its source names one. `validateDetectedEnvironment` bounds every added field like the
  rest of a saved detection. Preflight re-checks `listen` against the plan (`preflight_network.go`:
  `listen_loopback`, `port_hardcoded`, `proxy_headers_trusted`, `forwarded_headers_untrusted`,
  `public_url_variable_missing`, `request_body_limit` and the rest, listed in the recipe guide), so a
  certain loopback bind is a blocker before Deploy rather than a readiness timeout after it. The closed
  recipe set is
  `node`, `go`, `python`, `rust`, `java`, `dotnet`, `deno`, `php`, `site`, `ruby`, `elixir`, `scala`,
  `clojure`, `dart`, `gleam` (`validRecipe`; `site` builds Hugo, Zola, mdBook and Jekyll, and Python and
  Deno build a site generator's output when they have an output directory), and `build.pythonVersion`
  (3.10 to 3.14), `build.systemPackages` (at most 32 Debian names, Python recipe only),
  `build.nodeVersion` (the recipes that run the Node install, `nodeInstallRecipes`), `build.phpVersion`,
  `build.spaFallback`, `build.goPackage`, `build.cargoBin` (the binary a Rust recipe serves),
  `build.javaVersion` and `build.dotnetVersion` are additive plan fields, bounded by
  `PlanConfiguration.Validate`.
  The contract per language is [the recipe guide](recipes.md). The framework detection recognised is
  recorded on the build plan when a draft commits (`build.framework` on the configuration read) —
  the chosen candidate's, while the plan still builds that candidate's directory — so a later read,
  a fleet card or the Build settings, names it without detecting again. The server owns it: a value
  the browser sends in a draft or a configuration save is never kept. A save carries it forward only
  while the method, recipe and root directory stay the same. A source change records the framework the
  fresh detection finds at the plan's root with its method, and drops it when detection finds nothing
  there and the code moved — another repository, directory or image, though not a new branch or
  credential. It is left out of the plan's digest
  (`buildPlanDigest`, which every path that writes a build plan uses), so recording or dropping a
  name never shows as a pending build change. Projects created before it was kept have none.
- A repository's own container definitions are candidates read as data, with the same bounds as the
  rest of detection (the walk's file and byte limits, `os.Root` reads of the checkout that a symlink
  cannot redirect). Dockerfile candidates carry `dockerfileRole`, `dockerfileTarget`, `dockerfileArgs`
  (names and whether each has a default, is used in `FROM`, is consumed), `dockerfilePlatforms`,
  `imageBuildIssues` (`{code, severity, line, subject, detail}`, text that names lines, paths and
  variables but never a value) and `releaseCommand`; the result carries `selectionReason`, the sentence
  saying which rule decided. Selection (`detect_ranking.go`) ranks a directory's candidates by whether
  each may be chosen on its own, its standing, buildability, confidence and intent, and directories by
  their best candidate's selectability, standing and confidence, then a monorepo's application area and
  the shallower root — never by which one builds — and lists the winner first. Preflight's `imageBuildFindings`
  (`preflight_image.go`) turns the evidence of the candidate the configuration still builds — same
  method, root and Dockerfile — into one finding per code; a Dockerfile detection never read is the
  warning `dockerfile_unchecked`. Compose sources gain build targets and arguments, optional variables,
  env files, platform evidence and a primary service (`compose_build.go`). The contract is in
  [the recipe guide](recipes.md#repository-dockerfiles-and-compose-files).
- Detection reads the repository's shape as well as each root (`detect_*.go`; the contract is
  [repository shape](recipes.md#repository-shape-and-candidate-selection)). Manifests are read
  breadth-first before any source and the file bound counts only files opened, so bulk assets or
  generated Go cannot hide the root manifest. The shape pass runs twice around the per-root passes:
  first (`shapeCandidates`) it applies what the repository as a whole says — ecosystems, shapes that are
  not services, static roots, other platforms' files, processes, decoys — so the state, readiness,
  network and environment passes read each candidate as it will be offered, with another platform's
  start command and port; last (`rankDetection`) it pairs a split frontend and API by the ports and
  profiles those passes settled, and ranks. Candidates are ranked — a service above one ranked down
  (`demotion`: an example, a docs site beside an app, static files inside a code root, a split
  repository's frontend, a desktop shell's frontend), above one that is not a service
  (`notDeployable`: a library, a CLI, an extension, a mobile or desktop app, notebooks, a GitHub
  Action, a Windows-only program), by the ranking above — and `SelectedID` names the one that outranks
  every other; ties are still the operator's. A candidate also carries `companions` (the other root of a split frontend and
  API), `processes` (workers, schedulers, release commands, second servers), `platformManifests`,
  `serverlessCode` and `importCaseMismatches`; a result carries `setAside` (what was recognised and not
  offered, and why) and `alternatives` (a reviewed template or the project's published image), and
  `gitRequirements` lists each submodule and the LFS-tracked files. All of it is bounded and
  credential-screened by `validateRepoShapeEvidence` (anything it would refuse is dropped first by
  `keepValidShape`, so one odd file never fails the import, and a verdict drawn from a file's absence
  needs a walk that saw the whole root), and preflight turns it into findings before Deploy
  (`preflight_repo_shape.go`): `source_not_a_service`, `static_candidate_nested`,
  `selected_candidate_demoted`, `companion_service_not_deployed`, `desktop_frontend_only`,
  `secondary_process_not_deployed_<name>`, `release_process_not_run`, `edge_runtime_code_not_deployed`,
  `serverless_functions_dropped`, `import_case_mismatch`, `committed_virtualenv`,
  `platform_system_packages_ignored`, `static_redirects_unsupported`, `source_has_packaged_release`,
  `source_publishes_image`, and `git_submodules`/`git_lfs`/`git_lfs_unavailable` judged per build root,
  with `git-lfs` observed on the host when LFS is included. One project still runs one process: a worker
  or a split repository's other half is a second project from the same repository, which the findings
  name with its root and start command. A static site's candidate also carries `staticSite`
  (`detect_static_site.go`, validated by `validateDetectedStaticSite`): the generator, the release it
  builds with and what the repository declared, the sub-path its framework built it for, the theme's Git
  submodule, and how many of another host's redirect and header rules the static server applies or
  leaves out. `preflight_static_site.go` turns it into `static_base_path`, `static_base_path_computed`,
  `hugo_version_unpinned`, `site_generator_version`, `jekyll_ruby_version`, `site_theme_in_submodule`
  (blocked while submodules are off), `static_redirects_unsupported` and `static_hosting_rules`. The
  rules themselves and the base path are read again from the commit when the build is prepared
  (`build_static_serving.go`), with the fallback page the framework's catalogue entry names (SvelteKit's
  `200.html`, React Router's `__spa-fallback.html`, tried before `index.html`), and written into nginx's
  configuration as literals that pass a strict character check, one location block per path so the
  configuration always loads. Every static output is served by that one renderer (`staticServing`): a
  JavaScript, site-generator, Python or Deno site, a plain static site, a Trunk site and a Blazor
  WebAssembly app.
- A detected Node service that declares a migration tool applies its schema before it serves. Detection
  records the tool (`schemaTool`), the command it chose (`schemaCommand`) and whether the package's own
  start script already runs it, and chains the step in front of the start command through the manager's
  binary runner (`npx`/`bunx`/`pnpm exec`/`yarn`). Prisma and Drizzle deploy committed migrations and
  otherwise push the declared model; Knex, Sequelize and MikroORM run their migration command; TypeORM
  is recorded as a decision because its data source cannot be guessed. The rule set lives in
  `deploy/schema_tools.go`; Prisma's schema path follows `package.json`'s `prisma.schema` and
  `prisma.config.*`'s `schema:`/`migrations.path`, and the recipe generates the Prisma client itself with
  placeholders for the names `prisma.config.*` reads through `env()` (`build_node_prisma.go`). The Python
  tools (Django, Alembic, Flask-Migrate, Aerich) and EF Core are in `deploy/detect_schema.go` and share
  the same lookup. Preflight adds `schema_step_missing` (warning, on
  the start command) when a database is linked and neither the start command, a release task nor the
  start script runs the tool, and `schema_step` (pass) when one does; no linked database means no
  finding. A step that pushes the declared model (`prisma db push`, `drizzle-kit push`, recorded as
  `schemaPush` when the package's own start script does it) is `schema_push_unversioned` (warning)
  instead of a pass, linked database or not, and a refused push is named `runtime_schema_push_refused` by the
  failed gate's output classifier. Changing the package manager swaps the whole start command for
  detection's command for that manager, chained step included; detection records each manager's commands
  after every pass that rewrites them (`refreshNodeInstalls`), so a settled detaching start or a preview
  bound to every interface moves with the runner.
- Detection records the state an application writes to its own filesystem as `persistentPaths`
  (`deploy/detect_state.go`, [the recipe guide](recipes.md#persistent-state)): SQLite files, upload
  directories, framework storage, Dockerfile and image `VOLUME`s and ASP.NET's Data Protection key ring,
  each with the directory a managed volume can stand on without hiding code and, when the location is
  read from a variable, the value that moves it there. The walk hands every file to a state scanner with
  its own budgets (configuration apart from source, so a large tree cannot crowd out the schema); it reads
  text only. Per root it runs before the readiness and network passes: the schema step it chains into a
  Python start command is what readiness budgets a slow start for — nothing is chained when the release
  command the repository declares (read per root before any candidate is made) applies it — and the start command it gives
  PocketBase is the one network reads a listener from. The server database it offers in place of a
  Python SQLite default is added after the environment pass, which replaces the root's databases. The
  configure form plans one managed named volume per target with a storage dependency, named per draft,
  declares the moving variable (plain, runtime and release-task scopes, and build when detection saw
  the build read it) and releases stop-first; preflight (`deploy/preflight_state.go`) compares the state with the plan's writable mounts
  and planned values — never echoing a value — and warns per kind (`sqlite_ephemeral`,
  `uploads_ephemeral`, `persistent_path_unmounted`, `declared_volume_unmounted`,
  `dotnet_data_protection_ephemeral`) or passes `persistent_state_kept`; SQLite on a volume also has its
  schema step checked. Detection also records a seed (`seedCommand`, `seedResets`); `seed_available` warns
  when a linked database or a new SQLite volume would start without it, on a first release only — the
  observation's `replacesRuntime` says a live runtime is being replaced.
- A managed Docker volume belongs to one project (`deploy/planning_volume_owner.go`). `SavePreflight`
  adds `storage_owned_by_other_project` (blocked, naming the owner) for a planned managed mount or storage
  dependency another project's desired plan already manages, since only the store knows the owners, and
  `Commit` checks again inside its transaction, refusing with `ErrInvalidPlan` a volume taken in between.
  Linked and bind mounts are never owned.
- Deployment preflight depends on a read-only observer: filesystem/proc capacity, listener inventory,
  Docker/Compose availability, proxy inventory, bounded DNS lookups, the CPU's `avx`/`atomics` flags
  from `/proc/cpuinfo`, and — for a linked PostgreSQL, only when detection says the schema needs
  pgvector or PostGIS — one bounded `pg_available_extensions` read through the dashboard's own pool
  (`PlanningDatabaseExtensions`). It cannot build, pull, start, stop, write proxy/firewall
  configuration, modify a checkout or enqueue a backup. The persisted exact
  plan excludes raw observed import material and accepts only typed secret references. The observer
  reads `MemAvailable` and `SwapFree`, and `build_memory_low` (warning, `preflight_build.go`) compares
  them with the selected recipe's estimated build peak (a Next.js, Nuxt, Angular, Gatsby, Docusaurus,
  Strapi or Payload build ~2 GiB, other JavaScript frameworks ~1 GiB, Rust ~2 GiB, or ~3 GiB with fat
  LTO, one codegen unit or Leptos, Maven/Gradle and .NET ~1.5 GiB); the Rust recipe also caps Cargo's
  jobs at the gigabytes free when the step starts. The JavaScript recipe leaves V8's heap at its default rather than injecting
  `NODE_OPTIONS`, which every worker process of a build would inherit. The same file judges what the
  configuration gives a JavaScript build against what the candidate recorded (`nodeBuild`): an
  env-validation schema's variables without a build value (`build_env_validation_skipped`,
  `build_env_missing`, `build_env_client_missing` — when the recipe runs the build past a schema that
  honours `SKIP_ENV_VALIDATION`, the environment check does not also refuse those names as
  `build_variable_missing_*`, and without the skip it refuses them per field and the schema's summary
  lists only the rest), a prerendering framework's build-scoped database URL that points at a
  `db-N.jd.internal` alias or loopback, or a typed database reference (`build_database_unreachable` —
  BuildKit cannot join the environment's network, and `--network=host` would hand repository build code
  the host's loopback services; when no build-time read detection saw needs the variable — its phase
  is not `build`, or `prisma.config` alone reads it and the recipe supplies that — the finding carries a
  `fix` of kind `remove_variable_scope`, which Review applies to the draft's declaration and the check
  before Deploy and the run page open in the variable editor with the build scope already cleared,
  `?variable=NAME&without=build`), and platform variables an operator's pasted `.env` sets
  (`port_variable_mismatch`, `node_env_not_production`, `host_variable_loopback_hostname`; a loopback
  `HOST` is the environment check's `host_variable_loopback_host`). Values are compared, never echoed,
  except a port number, a `NODE_ENV` word and a host name.
- **Preflight runs before every deployment, against the commit it builds** (`deploy/deployment_check.go`).
  A plan was reviewed against the commit detection read when it was saved; a deployment can build a
  later one (a push, a specific version) of a plan edited since. `analyze_plan` therefore reads the commit
  `acquire_source` materialized (the workspace's marker makes this a lookup): a fresh, bounded, data-only
  `DetectPath` — the same detector the wizard runs, so every detection-backed finding (image build
  issues, where the server listens, readiness, variables, state, the JavaScript install) is judged
  against this commit — and the recipe's own dry run (see [the recipe guide](recipes.md)), which
  prepares within the checkout the way `prepare_context` does, a workspace member from its workspace
  root, and writes nothing. The plan is judged
  against the fresh candidate at its root with its method (and, for a Dockerfile, the plan's
  Dockerfile: a root can hold several), else the candidate stored with the plan, else
  — only for a source with no files, an image or a Compose file — the plan's own shape
  (`candidateSource` `detected`/`recorded`/`plan`). Wizard-only findings are dropped at run time
  (`detection_selected`, `detection_ambiguous`, `detection_empty`, `detection_truncated`,
  `detection_root_mismatch`, a passing `build_method_changed`, and what the repository's shape says
  about the choice: `selected_candidate_demoted`, `static_candidate_nested`, `desktop_frontend_only`,
  `companion_service_not_deployed`), and `plan_drift_*` warnings compare the
  stored and fresh candidates where the plan still carries the old answer: the lockfile set or the manager
  it resolves (`plan_drift_package_manager`), the framework, the output directory, and names the commit
  reads that nothing sets and no other finding names (`plan_drift_variables` leaves out a read without
  a default, a generated or domain-bound value and a registry credential, which the environment and
  install checks name on their own). The candidate's recipe checks read the planned candidate; its
  source facts — where it listens, what it reads and writes — read the candidate at the plan's root
  whatever builds it (`rootDetectionCandidate`), since the same code runs either way. The step's evidence carries `candidate` and
  `findings`, which the run page draws as findings ("Checked before building"); a blocked finding — or
  one of the decisions a run cannot pass: `readiness_missing`, `domain_link_missing`,
  `go_main_ambiguous`, `rust_binary_ambiguous` — stops the run with the finding's code as the terminal code and
  `title: measured. action` as the reason.
- Optional project advisory checks and `POST …/environments/{env}/check` were removed in 0.7.5.
  Project and fleet attention panels, header counts and setting recommendations are gone. Deploy
  commands enqueue the run directly; `analyze_plan`, source validation, readiness gates, permission
  checks, audit and recovery still run as part of execution. Source inspection and detection remain
  operational setup tools. `/operations` returns each owner's runtime/domain/storage/backup/dependency
  observation without a `diagnosis` field.

- A draft's preflight reads the commit its detection reviewed (`PreflightDraftWithSource`): the recipe's
  dry run replaces what detection alone could say about the plan, a root directory edited to one
  detection found nothing in is `detection_root_mismatch` (a decision without a tree, a warning once the
  recipe prepares that directory; the configure form picks the candidate detection found at a typed
  root on its own), and a remote branch that has moved past the reviewed commit is `source_moved`
  (warning, "Inspect again" reads the newer commit keeping the chosen candidate). The first deployment
  of a new remote Git project is pinned to the commit the check before it read (`sourceRevision`) —
  not when that check could not read it (`source_inspection_unavailable`), which leaves the branch
  head as before; later ones follow the branch. Materializing a pinned commit the branch has moved past
  takes it from the branch fetch, behind the new head, or by its id when the branch no longer contains
  it, and names it with its own release ref in the mirror; the workspace is verified against that
  commit, so a moved branch never stands in for it. The configure form's pick of the candidate at a
  typed root happens only when the root or builder was edited away from the picked candidate (the
  plan's recipe first at that root); a candidate picked at its own root is never swapped for another
  sharing it.
- Detection is refreshed for a project that exists. A source change (`PUT …/source`) saves what the
  inspection read — candidates, Compose analysis and Git requirements — as the new revision's
  evidence, rather than copying the old source's forward (a Compose-from-Git source moved to another
  branch builds the new service list), and returns a `proposal`: field by field (package manager when
  set explicitly, build and start commands, output directory, single-page fallback, Go main package,
  a Dockerfile's stage, port), what the plan saves, what detection proposes now, what it proposed when the plan was saved and
  whether it `changed`, plus the variables and databases the source reads that the plan does not set.
  Only the candidate at the plan's root building the plan's way is compared; when there is none the
  proposal names what detection selected instead with `elsewhere` and offers no field (its commands
  describe another directory or builder). A command detection could not tell is never proposed as
  clearing the plan's. Settings → Source shows the changed fields as "Detection changed", each applied
  onto the configuration the proposal was compared with under the proposal's `revision`, so a plan
  saved since refuses the apply. `POST …/detect` (a settings write's capability, audited as
  `deploy.source.detect`) reads the source again without writing, and Build settings' **Detect again**
  shows the same proposal, marks each field that differs and applies into the form's draft.
- Normalized build execution uses the project-owned versioned recipe set or an explicit Dockerfile,
  static, immutable-image, or Compose adapter. Reviewed base tags are resolved before rendering and every
  generated `FROM` is digest-pinned. Build secrets are BuildKit environment-backed secret mounts and
  never argv/build args; custom Dockerfiles with requested secrets, or a secret-named literal in an
  `ENV`/`ARG`, shell assignment, flag or heredoc (read over logical instructions, so a continuation line
  cannot hide one and `SECRET_KEY_BASE_DUMMY=1` is not one), fail closed because their layer history
  cannot be guaranteed — and detection runs the same check, so preflight says so before Deploy. A custom
  Dockerfile build receives `--target` for its chosen stage and `--build-arg NAME` only for plain,
  browser-public build variables it declares, with values in buildx's environment; a Compose service
  build receives its own `target` and `args` the same way, interpolated from the plain runtime and build
  variables only, and refuses an argument or target that reads a secret (`composeBuildArgValues`), so no
  secret ever becomes a build argument or argv. A browser-public value typed into a new project is
  stored plain (`suppliedVariableSensitivity`); every other typed value is stored secret. Recipe and static builds write a
  generated `Dockerfile.dockerignore` beside the generated Dockerfile, so repository metadata and
  dashboard files stay out of the image whatever the repository's own ignore file says.
  Recipe build scope now supplies values automatically, with explicit install-stage restrictions for
  package credentials and an `install_and_build` mapping that mounts one value in both RUN steps; a
  registry credential detected in `.npmrc`, `.yarnrc.yml` or `bunfig.toml` is declared with build scope
  alone and mapped to the install step when a draft supplies it (`Draft.withEnvironmentMetadata`), and
  `registry_token_missing` names one that cannot reach the install, for the PHP asset stage too. Preparation logs its install decisions (an unfrozen install, a moved runner, a
  `.dockerignore` exception) to the run transcript from `prepared.notes`. Serving defaults per framework, the Python install shapes and interpreter
  selection, the Rust, Java, .NET and Deno recipes, the single-page fallback and Go version/command
  behavior are defined in [the recipe contract](recipes.md), including the exact limits of live
  framework verification.
- Enqueue atomically freezes exact variable revision ids plus canonical dependency/check JSON. The header
  exists for an empty set, digests are checked before variable decryption, and retry copies the original
  snapshots instead of observing later rotations. Release runtime snapshots store the actual secret-free
  JSON bytes and verified digest, not a pointer back to mutable desired configuration.
- Release tasks are named, bounded shell gates that run after the backup gate and before the candidate
  starts (`DefaultStepKeys` puts `backup_gate` first, so a migration never changes a database whose
  required backup is unverified, and `rehearse_migration` between them, below). A task with `runner: "image"` — the default for new tasks and for a
  release command detection read — runs once in a throwaway container of the candidate release's image
  (a Compose release's primary service image) through the runtime owner (`release_task_image.go`,
  `dockerx.RunToCompletion`): the runtime variables the release starts with plus the task's
  `release_task`-scoped ones as container environment, on the project's database networks (and the
  preview network for an isolated preview) or on the host's network when the release runs there, with
  the release's resource limits and the plan's mounts (its volumes and binds, read-only where the plan
  says so, recorded in the task evidence as `mounts`) — a Heroku or Render release phase that sees the
  application's own data, so a migration or seed of SQLite on a volume reaches the file the release then
  opens — but no ports, devices, capabilities or privilege, the image's entrypoint replaced by the
  command, and the container removed however the task ends. The task runs while the live release still
  serves from the same volume; a stop-first release stops it only when the candidate starts. The mounts
  are the frozen runtime plan's own — a bind source was resolved and authorised when the plan was saved,
  a preview's are its own preview volumes (and a preview plans no task) — so a task gains no path the
  release it precedes does not already have. The container carries
  `io.just-dashboard.release-task`, so runtime observation leaves it out; `Server.Start` removes any a
  previous process left before the engine resumes runs, a task removes a stale one of its own name
  before it starts, and the step's cleanup records whether removal succeeded. A blank command is
  refused by validation and, stored before that, fails the task instead of the executor. A Compose
  release's image task runs outside the stack, which preflight names
  (`release_task_outside_compose_stack`, blocked when the stack runs its own backing services). A task without a runner is the historical `/bin/sh` over the immutable source workspace in
  the dashboard's container, which has none of an application's dependencies; preflight refuses one
  that runs a tool only installed dependencies provide, or a program a PATH lookup in the dashboard
  does not find (`release_task_tool_missing`, a lookup that executes nothing). Output is redacted of
  the task's own values and every secret runtime value before persistence. Interrupted tasks stop for
  operator review because their side effects cannot be inferred safely. A failed task's own redacted
  output (the last 400 lines / 64 KiB) is classified before the step ends: Prisma's P3009, P3018,
  P3005, P1000 and P1001, a variable the task was not given, and a program where the task ran does
  not have (the shell's line, or exit status 127 without it) become `release_migration_failed_before`,
  `release_migration_failed`, `release_database_not_empty`, `release_database_auth_failed`,
  `release_database_unreachable`, `release_env_missing` and `release_task_command_not_found`; a task
  past its limit is `release_task_timeout`, anything else stays `release_task_failed`. A missing
  program and an unreachable database are worded for where the task ran: in the release image, which
  joins the database's networks, the image lacks the program or the database is down; in the
  dashboard's shell, the fix is running the task in the release image. A generic "`X_Y` is not set /
  must be set / is required" sentence is read only after every other signature here, and never from a
  line that calls itself a warning. The code is the step's and the run's terminal code, the sentence
  joins the message, and the step evidence carries the cause (identifiers only) beside the task
  evidence.
- Migration rehearsal (`deploy/rehearsal.go`, the Databases side in `api/deployment_rehearsal.go`) is
  the `rehearse_migration` step, between `backup_gate` and `release_task`, so it runs only after the
  backup it restores from has been verified and before any task touches production. It is off by
  default and turned on per environment (`deploy_rehearsal_policies`, `GET`/`PUT
  /deploy/{id}/environments/{env}/rehearsal`: the read for whoever reads the environment, a deploy key
  included; the write `system.admin` with a session, audited as
  `deploy.rehearsal.update`; a preview has no row). A run with no release tasks, or with rehearsal
  off, skips the step with the reason. Otherwise the target is the managed database the tasks'
  resolved variables address by its `db-<id>.jd.internal` or `db-<id>-pool.jd.internal` name — the
  only one, or the one the terms name when they reach several, never a guess. The copy is the
  production server's container spec on a fresh volume: its image, command and environment, its
  read-only mounts, none of its writable ones (a socket directory, a log, the WAL-G tool volume and
  archive), none of its ports, labels or networks, and for PostgreSQL its archiving removed and
  `archive_mode=off` on the command line, which outranks the data directory, so a copy never writes
  into production's archive. It runs on a network of its own (`jd-e<env>-run<run>-rehearsal`,
  `Internal`, no gateway out) under the aliases the production database answers by, so the task's
  `DATABASE_URL` reaches the copy unchanged. PostgreSQL with an active point-in-time archive is
  recovered from it — WAL-G fetches the newest base backup and the copy replays the log to its end
  (at most `WALArchiveTimeout`, a minute, behind production), then is removed and started again
  without the archive, the WAL-G tool volume holding its key, or the recovery settings; anything else
  is restored from the newest native dump a backup job made of that connection
  (`backups.Store.LatestDatabaseDump`, `Runner.ExtractDatabaseDump`, the same extraction
  `RestoreDatabase` uses): `pg_restore --no-owner --no-privileges --exit-on-error`, `psql -f` or
  `mysql`, with the application's role made first when it is not the superuser. MySQL and MariaDB
  restore from dumps only; any other engine is reported as not supported. The tasks then run through
  the same release-task runtime as for real (`runImageReleaseTask` with `Rehearsal`), in the release
  image with the same variables and timeout, but on the rehearsal network alone — never the project's
  database networks, the preview network or the host's — with no mounts and a
  `jd-e<env>-run<run>-rehearsal-task<n>` name. While a PostgreSQL task runs the copy's `pg_locks` is
  read every 250 ms for sessions waiting and for locks that block an application's reads or writes
  (ACCESS EXCLUSIVE, EXCLUSIVE, SHARE ROW EXCLUSIVE, SHARE) on user relations, and how long each was
  held is the report's estimate of how long production will wait. The evidence (`RehearsalReport`)
  records the database, the source (dump run and time, or the archive's recovered moment), restore
  time, each task's duration, exit code and last output line, the locks, the isolation and whether the
  copy was removed. A task that fails against the copy fails the step and the run as
  `rehearsal_failed`, naming the task, the copy and its source and saying production was not
  touched; `release_task` never starts. A rehearsal that cannot be carried out — no backup, a dump
  over the size limit (`maxBackupMb`, default 2048) or a base backup unpacking to more, a restore past
  `restoreTimeoutSeconds` (default 600), a shell-runner task, no reachable database, an engine without
  a restore path — is `rehearsal_unavailable` and stops the release, or with `whenUnavailable: "warn"`
  becomes a warning step and the release continues. Everything a rehearsal makes carries
  `io.just-dashboard.rehearsal=<run id>` (the WAL-G tool volume too): the step removes its own
  leftovers before it starts and everything after, however it ends; a resumed run makes its copy
  again (`ReconcileResume`); and `Server.Start` removes every rehearsal before the engine resumes,
  except a [copy of a release's data](data-aware-rollback.md) still within its lifetime, which carries
  the same label with `copy-<id>` and is made by the same path (`prepareCopy`: the newest backup for a
  rehearsal, a recorded dump run or archive moment for a data copy). The environment's network
  reconciler leaves every rehearsal-labelled container where it is: it used to join a rehearsal's copy
  and task, which carry the environment's label, to the deployment's database network on its next
  pass, giving the internal network a way out and production's database the copy's name beside it.
  Runtime observation never counts the copy (it has no release id) or its task (the release-task
  label), and host container alerts leave both out. A promotion takes the same step in the same place
  (`PromotionStepKeys`, [staging and promotion](staging-promotion.md)): production's tasks with
  production's variables, so the copy is of the database production's values reach, run in the image
  staging built; the step reads production's terms, never staging's, and ends the promotion as it
  would a deploy. Build settings carries the terms under the release tasks (`settings/rehearsal.tsx`),
  the Promote confirmation says the tasks are rehearsed first while the terms are on, and the run page
  reads the report as figures (`run-rehearsal.tsx`).
- Artifact retention keeps the live release, five prior successful rollback releases, candidates, pins,
  retain-until windows, recent failed diagnostics, shared physical digests, every environment under
  an active deployment lease, and a staging release an unfinished promotion is about to take its image
  from ([staging and promotion](staging-promotion.md)). Cleanup reports the reason for every retained row and removes a mutable
  Docker tag only after inspection proves it still names the recorded config/digest. A run that fails
  after `render_runtime` moves its candidate release to `failed` the moment the run itself reaches
  `failed`, `failed_activation`, `rolled_back` or `cancelled` — `TransitionRun` does it in the same
  transaction as the run's own state change — so it leaves `candidate` (which retention always keeps,
  reading as a build still in progress) and enters the seven-day failed-diagnostic window like any other
  failed artifact instead of being retained forever.
- Runtime activation consumes only an immutable release snapshot. Direct containers use the recorded
  config digest (or repository plus manifest digest); Compose uses an explicit stable project, the exact
  source file list, and a generated `0600` override that pins every service image with `pull_policy:
  never`. The release's container identity and readiness target is the primary service — the one the
  operator chose (`build.primaryService`), else the analysis's, which builds or publishes a port and is
  never a database — and a snapshot recorded before there was one keeps its first service. Compose interpolation receives only the frozen runtime scope through a temporary `0600` env
  file which is deleted on every exit path. A Compose release keeps no single image of its own: its
  row records the first of its services' image artifacts, so decoding the snapshot requires that
  digest to be one of the snapshot's services (`snapshotNamesReleaseImage`), where a container release
  still has to name exactly its image — compared with the empty single image, every Compose release
  had been refused at its first activation step.
- Archived projects keep their original display name in additive `archived_name` storage while the
  unique database name becomes an internal tombstone. Archiving and upgrading previously archived
  projects release the live name without deleting history or resources. New projects from the same
  repository receive independent identities. Each project displays its own persistent run sequence,
  starting at **Run #1** across its environments and operations. Existing history is numbered in run-ID
  order on upgrade; a SQLite insert trigger allocates subsequent numbers atomically for both legacy
  and normalized runs. Global run IDs still identify API routes, links, events and audit evidence.
- The runtime plan carries optional resource limits — `memoryMb`, `cpus`, `pidsLimit` — and a
  `restartPolicy` from the closed set `unless-stopped` (default), `always`, `on-failure`, `no`. Zero means
  Docker's own unlimited default. Validation bounds them (16 MiB–4 TiB, 0.01–1024 CPUs, 16–1,048,576
  processes); preflight warns when a limit exceeds the host's available memory or CPU count; the Docker
  runtime owner passes them to container creation and release comparison labels them. Compose services keep
  the limits their files declare. What they should be is [right-sizing](right-sizing.md)'s read of the
  environment's retained container samples (`GET …/environments/{env}/sizing`, `GET /deploy/sizing`);
  applying its suggestion is this plan's ordinary configuration save, applied with the next release.
- A failed readiness or smoke gate first asks the runtime owner to diagnose the candidate: container
  state, exit code, OOM flag, restart count and a bounded log tail (200 lines, 32 KiB, most recent kept).
  The tail is written to the run transcript after redaction of every runtime variable value; step
  evidence records state and counts, never output. Compensation runs afterwards, so the operator reads
  why the application never listened instead of only "could not connect". The step message points at the
  transcript when output was captured. A running candidate's listening sockets are read too, by
  `cat /proc/net/tcp /proc/net/tcp6` inside its own container through the check runner's closed-argv
  exec (bounded output, only parsed addresses kept). `applicationOutputCause`
  (`runtime_output_cause.go`, the one table of runtime and release-task causes) then names one cause,
  most specific first: the container's own OOM flag (`runtime_oom`), a missing table
  (`schema_missing`), a refused schema push (`runtime_schema_push_refused`: Prisma's
  `--accept-data-loss` and "changes that cannot be executed", Drizzle's rename and data-loss prompts),
  a SQLite file it cannot open or write (`runtime_sqlite_not_writable`), a variable the application
  reads and was not given (Prisma P1012 and config errors, t3-env, Django, Rails, Phoenix, Auth.js's
  MissingSecret, pydantic-settings, `KeyError` on the environment — `runtime_env_missing`), Rails
  credentials `RAILS_MASTER_KEY` cannot decrypt, an Auth.js host it does not trust (fixed with
  `AUTH_TRUST_HOST=true`), a missing `.env` it exits over, a database address on localhost (Node,
  libpq, Go, MySQL and Rust wordings), Prisma's engine on Alpine, a disallowed Host, a missing entry
  file or app object (with its own sentence for `next start` refusing a static export, whose fix is the
  output directory `out`, and for NestJS's `dist/main` written as `dist/src/main.js`), a missing module
  or shared library (psycopg's `libpq library not found` among them), a PHP extension a call needs
  (`Call to undefined function mysqli_connect()`, `Class "Redis" not found`, WordPress's missing MySQL
  extension — `runtime_php_extension_missing`, naming the `ext-` requirement that installs it), front-end
  assets a page renders and the image lacks (Laravel's Vite manifest, `Mix manifest does not exist` —
  `runtime_assets_missing`), a cgo-less binary, an architecture mismatch, a runner that is not installed
  (`runtime_command_not_found`), Phoenix's origin
  check (fixed with `PHX_HOST`), Play's PID file, a server that prints a loopback bind (uvicorn,
  gunicorn, werkzeug, puma, Kestrel — not Next.js, which prints `localhost` whatever it binds) or whose
  every listening socket is on loopback (`runtime_loopback_bind`, named in the step message even when
  the application printed nothing, with the listener as its subject), a port other than the plan's
  printed by a framework's own startup line, then any program's generic "`X_Y` is not set / must be
  set / is required" (read this late, and never from a line that calls itself a warning, because
  programs print it as a warning and carry on: `SENTRY_DSN is not set` beside the port the server
  really took must name the port), and a single container that stopped with exit code 0 instead of
  serving (`runtime_start_exited`; several containers are a Compose stack, where a one-off service
  exits 0 by design), and a Django candidate whose check was answered with a 5xx while the output
  holds no error at all (`runtime_errors_hidden`, a 400 read as `runtime_host_disallowed`, a 401, 403 or
  404 left to the readiness classification: with DEBUG off
  and no `LOGGING`, Django mails request errors instead of printing them — the failed checks' status
  codes reach the classifier as `runtimeCauseContext.checks`). The cause is the step's code and the run's
  terminal code in place of `health_gate_failed`, with the health evidence kept; it carries identifiers
  only (a variable, a port, a module, a listener) and, where the plan can supply one, a fix: the
  variable or its runtime scope, the internal port, a start command bound to `0.0.0.0`, for the
  Python recipe the Debian package that provides a missing shared library (`build.systemPackages`),
  or — for a Node recipe whose start runs a package manager the image lacks — the start command moved to
  the manager the image carries (`nodeRunnerFor`, the install planner's own rewrite: `bun run start` in
  an npm image is `npm run start`). That manager is the one this run's build installed with (its
  prepared install line), else the plan's, else the one detection resolved; a start the rewrite cannot
  carry (a file Bun ran as a script, the image's own manager missing, a Dockerfile's start) is left to
  review.
- Container applications receive `PORT` from the frozen internal-port setting unless a runtime variable
  explicitly supplies it. A Python recipe image whose gunicorn or uvicorn start leaves the worker count
  to `WEB_CONCURRENCY`, and which loads no machine-learning model, is recorded as such
  (`PreparedBuild.webConcurrency`, copied into the runtime snapshot), and `startContainer` sets
  `WEB_CONCURRENCY` for it (`runtime_concurrency.go`: 2×CPU+1 within 256 MiB of the memory limit per
  worker, 2 without a limit) unless the plan sets the variable; the start step's log states the value.
  Detection gives a websocket application's generated uvicorn command `--workers 1`, so it is never
  marked. Compose and host-network applications keep their own environment conventions.
  This keeps application startup aligned with Docker publication; the host port may still move. The
  recipes switch framework trust of the proxy's `X-Forwarded-*` headers on (`FORWARDED_ALLOW_IPS=*`,
  `ASPNETCORE_FORWARDEDHEADERS_ENABLED`, `SERVER_FORWARD_HEADERS_STRATEGY`, Quarkus proxy forwarding,
  SvelteKit's header variables), which is safe only while the proxy fronts the release alone. The
  release's runtime snapshot records which of those settings the recipe image's final stage sets
  (`proxyTrust`, read from the rendered Dockerfile by `imageProxyTrust`); a repository Dockerfile, a
  pulled image records none. When the release has no route, or its port is
  reachable directly (a `0.0.0.0`/`::` bind, host networking, or the application's port published again
  on every interface), `startContainer` writes the withdrawn value of each recorded setting unless the
  plan sets the variable itself (`network_trust.go`). Nothing is written over a user's own image.
- HTTP/TCP readiness checks follow the recorded runtime publication when a saved check refers to the
  primary service's original internal or requested host port. Explicit unrelated ports, remote hosts
  and full URLs retain their configured targets. Quick deploy leaves the check port unset to follow
  allocation. Failed gates log the check name, safe address, attempt count and specific status/reason;
  saved older failures derive the same explanation from retained check evidence when read.
- HTTP, TCP, Docker-health, command and public-route checks have closed configuration, per-attempt
  timeouts and bounded retries. Persisted evidence contains status/state/error codes and a digest of
  bounded command output, never response bodies, command output, URL credentials/queries or runtime
  variable values. Disabled, unavailable, warning, passed and failed remain distinct outcomes.
  `game_handshake` (the Minecraft Java status handshake over TCP) and `udp_probe` (a datagram sent,
  an answer beginning with an expected prefix received) run too; see [game servers](game-servers.md#readiness).
  The closed kind list also names `dns`, `tls` and `backup_freshness`, which no runner
  executes. Saving one (Settings or a draft's configuration) is refused with the reason and what to use
  instead (`refuseUnrunnableChecks`): a `public_route` check on the https address, or a storage
  dependency's backup requirement. `Validate` still accepts them, so a
  plan stored with one keeps deploying; its check reports `check_owner_unavailable`, a warning unless
  it is required.
  An HTTP check against the candidate introduces itself as the proxy does when the release has a
  domain — `Host`/`X-Forwarded-Host` the first domain that is not a wildcard, `X-Forwarded-Proto` its
  scheme, `X-Forwarded-For 127.0.0.1`, a browser's `Accept` — while connecting only to the candidate.
  Default HTTP checks require a final 2xx response and follow at most four redirects that stay on the
  candidate's address or name one of the release's own domains; the latter are re-asked of the
  candidate, never of the domain. Redirects elsewhere are never requested and fail with their origin
  in the message (`redirect_off_origin`), as do loops (`redirect_loop`), so a candidate cannot pass by
  redirecting to the old public release or to a page that returns 500. `acceptAnyAnswer` checks accept
  the first answer below 500 other than 400 and 421 (what host allowlists answer); explicit
  expected-status lists retain exact-status, no-redirect behavior, and a check cannot set both.
- Blue/green is limited to stateless proxy-owned HTTP candidates without fixed ports, host networking or
  writable mounts; dynamic candidate ports are loopback-leased, one per replica. Fixed-port, Compose,
  game and exclusive writable-storage plans are honest `stop_first` deployments, advertise expected
  downtime and run one container per release (`replicaRefusal`). A route moves only after the plan's
  required readiness/smoke checks pass on every replica of the candidate (one that fails removes the
  whole candidate) — and a plan with none is cut over
  to ungated: `verify_readiness` is skipped (`disabled`, with the reason in its evidence and a
  transcript line saying the candidate counts as ready because it started), and `activate` records
  `readinessGated: false` and writes, in the strategy's words, that traffic moved (or the release went
  live) without a readiness check; the run page says it again on the Route stage's line and in the
  outcome, with the way to the health-check settings. A readiness check is required only of a web or
  static plan (`readiness_missing`, a decision, refused by a deploy's `analyze_plan`); a redeploy,
  rollback or promotion has no `analyze_plan` and runs with whatever checks the plan carries. For every
  other profile that serves — a service or published container on a port, a game server, a Compose
  stack or anything behind a domain — preflight raises `cutover_unverified` as a warning
  (`ungatedCutoverFinding`), with the port and strategy measured, field `checks`, and in a run's
  preflight step a deep link to Settings → Runtime → Health checks; a worker is never asked. Not a
  refusal: an existing plan without a check keeps deploying and is told what that costs.
- Traffic replay (`deploy/replay.go`) is the `replay_traffic` step, after `verify_smoke` and before
  `activate` in `DefaultStepKeys` and `PromotionStepKeys` (a redeploy, a rollback and an instant
  rollback do not take it): the last gate before anything is routed to the candidate, and the one that
  asks it what visitors actually asked rather than one health path. It is the plan's
  (`runtime.replay`: `enabled`, `requests` 1–500 with 0 for 50, `paths` up to 32 prefixes that begin
  with `/`, `maxStatusDrift` 0–100 percent with 0 for 2, `latencyFactor` 1–10 with 0 for 2,
  `timeoutSeconds` 1–300 with 0 for 60), off by default, validated with the other runtime fields
  (`validateReplayPlan`), stored as nothing when it asks for nothing (`canonicalReplayPlan`, so a
  switch turned on and off is no change), edited in Runtime settings beside the release strategy
  (`settings/replay.tsx`) and listed in the release comparison as `traffic replay`. Off, a restart, a
  host with no request record, a plan whose every domain redirects, an empty record or one with
  nothing eligible each skip the step with the reason in its transcript and evidence; a record that
  cannot be read is a warning and the release continues. Otherwise the step reads the same held
  record the Logs page and the rollback watch read (`RequestRecord.Window` on the environment's route,
  the last 24 hours, GET and HEAD, the newest 5,000 rows) and takes the newest distinct requests —
  method, host, path and query — answered 2xx, 3xx or 4xx, leaving out 401, 403 and 407 (decided by who
  asked), scanner probes (`accesslog.IsProbe`), a query that names a credential (`token`, `code`,
  `state`, `sig`, …), a path or query with a control character, a space or non-ASCII, a host no served
  domain names (wildcards by one label), and — where the record says which requests came over TLS, as
  Caddy's does — a plain request for an HTTPS domain, which the ingress's redirect answered. A row
  without a host (nginx's `combined`) is the first concrete served domain's. Each request is sent
  only to the candidate's own address (`targetForRuntime`, the readiness checks' target), built from
  the record alone: method, path and query, the recorded `Host` and `X-Forwarded-Host`, the scheme the
  request came over as `X-Forwarded-Proto`, `X-Forwarded-For 127.0.0.1`, `X-JD-Replay: 1` for an
  application that keeps replays out of its analytics, and no cookie, authorization or body — the
  record holds none. Four at a time, ten seconds each, the whole within `timeoutSeconds`; no
  environment proxy, no redirect followed (the record holds the live release's first answer), at
  most 256 KiB of each answer read and discarded so the time is the answer's. A request the deadline
  left unsent is unanswered (`out_of_time`). The verdict (`summarizeReplay`): nothing answered is
  `unanswered`; a share of requests answered in another status class than the record, or not at all,
  above `maxStatusDrift` is `status_drift`; the candidate's p95 over requests timed on both sides
  (at least ten, nearest-rank, `accesslog.Percentile`) past `latencyFactor` × the recorded p95 and at
  least 250 ms slower — the rollback watch's rule, `latencyRegressed` — is `latency`; a record without
  durations (host nginx) is never judged on p95. A tripped verdict stops the candidate and restores a
  stop-first predecessor through `stopCandidateAndRestore`, the failed health gate's own recovery, and
  fails the run `replay_<code>` with nothing routed; the live release is not touched. The evidence
  (`ReplayReport`) is the counts (read, eligible, sent, answered, drifted and their share), both p95s,
  the target and domains, at most 50 drifted requests by method, path, query, both statuses and both
  times, the verdict and the recovery — never an answer's body or a header. The transcript names each
  drifted request and the verdict. An interrupted replay resumes (`ReconcileResume`): it sends reads to a
  candidate nothing routes to and changes nothing but its own evidence. Preflight says what it will
  hold the candidate to (`replay_before_cutover`, a pass) or that there is nothing to replay
  (`replay_without_record`, a warning: no served domain, or no proxy for one). The run page draws the
  report in the Verify stage (`run-replay.tsx`). It is not a load test: the latency rule compares one
  pass over a cold candidate with what the ingress recorded, which includes the time to send the answer
  to the visitor, so it catches a release that is several times slower, not one a few percent slower.
- A managed route lets request bodies up to `runtime.maxRequestBodyMb` through (1–10240). Zero is each
  proxy's deployment default, which differs: nginx's own default is 1 MB, which refused a phone photo
  before the application saw it, so an nginx route always writes `client_max_body_size` (64 MB unless the
  plan names a limit); Caddy has no default limit, so a Caddy route writes `request_body { max_size }`
  only when the plan names one and otherwise stays unlimited, as before. The project's Runtime settings
  (Where it listens) and the new-project limits fold edit it; preflight's `request_body_limit` pass and
  release comparison state it, zero as "64 MB on nginx, no limit on Caddy".
- Deployment-owned nginx cutover is serialized and snapshots the prior bytes, mode and exact symlink
  target. Apply/reload/verification failure restores, reloads and verifies that exact snapshot before a
  run may report recovery. The snapshot content is held only for compensation; persisted activation
  evidence carries its digest, not the configuration bytes. The rendered nginx route proxies each
  request over a fresh upstream connection (no `upstream` block for one container, and a replicated
  release's `upstream` block — `least_conn`, passive health, a 2-second connect timeout — carries no
  `keepalive`, so no upstream keepalive either way): under
  sustained traffic in the high hundreds of requests a second the proxy's side of every closed
  connection sits in TIME_WAIT, its ephemeral ports run out after roughly half a minute, and every
  proxied request answers 502 until they drain. The cutover continuity test throttles its own
  clients to stay under that ceiling; the ceiling itself is a known limit, not a cutover defect.
- Stop uses the configured signal and a bounded grace period before Docker escalation; predecessor drain
  and activation/cancellation compensation use bounded contexts detached from request cancellation. A
  stop-first failure restarts the predecessor from its exact immutable runtime spec. Cancellation between
  persisted steps removes an uncommitted candidate, or finishes predecessor retirement after a committed
  cutover, and records sequenced cleanup evidence.
- Metric-gated rollback keeps the release a blue/green cutover replaced **warm** instead of removing
  it (`deploy/rollback_watch.go`, `rollback_executor.go`, `rollback_watcher.go`). `retire_previous`
  drains it as before, then — when the environment's terms allow, the environment is not a preview,
  both releases are the runtime owner's own containers rather than Compose stacks and the host still has
  `max(256 MB, a tenth of its memory)` available — records a window (`deploy_rollback_watches`, state
  `watching`) and the predecessor's runtime `warm` in one leased transaction and only then `docker
  pause`s it, every replica of it. A replicated warm release is one state (`warmStateOf`): any replica
  gone is the release gone (`warm_lost`), one a Docker restart brought back running is paused again, and
  resuming or removing it is every replica (`warmEach`). Paused rather than running:
  the old release must not keep running schedulers, consumers or writes against a schema the new release
  may have migrated, and a frozen process costs memory and nothing else; Docker counts it as not
  running, so "exactly one release running" still holds. The step's evidence says `warm.kept` with the
  window, or why not (`warm.reason`). A unique partial index allows one open window per environment, so
  at most one release is ever warm. Terms are per environment (`deploy_rollback_policies`; no row is
  `DefaultRollbackPolicy`: kept warm 10 minutes, automatic, 5 points over the old 5xx share or 2× its
  p95 and at least 250 ms slower, over at least 20 requests) and are copied into the window when it
  opens, so an edit applies from the next cutover. **On by default**: blue/green already runs both
  containers at once for every cutover, so the peak memory is unchanged and only its duration grows,
  the floor gives the memory back on a tight host (also re-checked on every pass of the window), and a
  new run ends the window before it builds.
  The watcher (`RollbackWatcher`, started after the engine, every 10 s) reads each open window: it
  re-pauses a warm container a Docker restart brought back running, closes the window as `warm_lost`
  when the container is gone and as `memory` below the floor, takes the warm release's baseline once
  from the request record (its own traffic over the 30 minutes before the cutover, or since it went
  live if later) and the watched release's reading since the cutover, and judges them with
  `JudgeRollback`: nothing is decided over fewer than `minRequests` requests or fewer than five 5xx
  answers; a baseline under `minRequests` is not trusted (the 5xx rule judges against a clean share, the
  p95 rule does not judge); a record without durations (host nginx) never judges p95; a release with no
  domain is never judged (`no_route`) — it is still kept warm for the button. A tripped verdict asks
  for an instant rollback through the API's own enqueue (`enqueueAutomaticRollback`, trigger
  `auto_rollback`, idempotency key `rollback-watch-<id>-<attempt>`, at most two automatic attempts per
  window) and audits `deploy.release.auto_rollback` as `system`. A window that passes clean is closed
  (`window_passed`): the warm container is resumed so it receives its stop signal, stopped and removed,
  its runtime `retired` and its release `retired_at` — exactly the retirement it would have had — and
  `deploy.release.warm_retired` is audited. Closing is a compare-and-set `watching → closing`, the same
  row an instant rollback claims `watching → rolling_back`, so exactly one of them wins; a `closing`
  window whose container could not be removed yet is retried on every pass.
- An **instant rollback** is a `rollback` run with `{"instant":true,"watchId":…}` in its metadata and
  six steps (`InstantRollbackSteps`): `start_candidate` claims the window under the run's lease and
  resumes the warm container (the same container id, never recreated), `verify_readiness` runs the
  warm release's own readiness checks against it, `activate` applies the warm release's own frozen
  route (domains, redirects, headers, allow list, sign-in), runs its public-route checks, verifies the
  route and moves the pointer in one transaction — the warm release `live` again with
  `activated_at` now, the watched release `retained` with `rolled_back_at` and `rollback_reason` —,
  `retire_previous` drains and removes the rolled-back release and closes the window `rolled_back`,
  then `record_release` and `notify`. It takes a light slot at priority 1000 through the environment's
  queue like every run, so nothing else is cutting over at the same moment; no release row is created,
  because the release serving is the warm one with its own frozen configuration, and the release
  history says so. A warm container that will not resume or fails its readiness check is removed and
  the window closed `rollback_failed`; a refused route or commit restores the route, pauses the warm
  container again and hands the window back. Every other run of the environment (except a scheduled
  marker) ends an open window at its first step (`superseded`), so a deploy started during a window
  never runs beside two warm releases and an instant rollback queued behind it finds the window closed.
  Restart recovery: a window is only rows, so a restarted process's watcher resumes it where it was; an
  instant rollback the engine ended as `restart_evidence_missing` is settled from the live pointer — if
  it moved, the rolled-back release is removed and the window closed; if not, the route is put back on
  the watched release, the warm container paused again and the window handed back to `watching`.
  Cancellation does the same. `GET …/environments/{env}/rollback-watch` reads the terms and the latest
  window; `PUT …/rollback-policy` (system admin, session) saves terms, audited
  `deploy.rollback_policy.update`; `POST …/rollback-watch/rollback` is the instant rollback by hand
  (destructive, ordinary confirmation, `409 no_warm_release` without a watching window, audited
  `deploy.release.instant_rollback`); `POST …/rollback-watch/keep` (service control) ends the window now
  and removes the warm container, audited `deploy.release.keep`. The read, the instant rollback and
  keep are a deploy key's too (`jd status`, `jd rollback --instant`, `jd keep`); the terms stay a
  session's. The live proof is
  `TestLiveMetricGatedRollbackSwitchesBackToTheWarmRelease` ([CONTRIBUTING](../../../CONTRIBUTING.md)).
- **Scale to zero** ([sleep when idle](scale-to-zero.md), `deploy/sleep.go`): a plan's `runtime.sleep
  {enabled, idleMinutes 5–1440 (15), wakeTimeoutSeconds 5–600 (60)}`, off by default and refused for a
  Compose stack, host networking, a plan without a served domain, and a required `docker_health` or
  `public_route` readiness check; the API also refuses it beside an enabled schedule that runs a
  command in the container, and that schedule beside it. The `Sleeper` (after the engine, every minute)
  stops the live container — not removed — once the route has served no request for the idle minutes,
  read from the held request record, never while a run is queued or running, a warm release is watched,
  the runtime was stopped by hand, or the wake is not listening; the stretch is a row of
  `deploy_sleep_spans`, written before the route's `sleeping.on` switch and the stop. A release that
  sleeps has a route that wakes it (`DeploymentRoute.Sleep`): a request meeting the switch, or the
  application not answering, is held by the proxy while the dashboard — asked over a Unix socket only the
  proxy reaches, answering only a peer that is nginx or Caddy — starts the container, waits for its own
  readiness checks and answers with its address; the proxy then sends the request on as it came, or
  serves the waking page past the hold (`min(wakeTimeoutSeconds, 30)`). Every run wakes a sleeping
  environment at its first step (`endSleep`), a stop only ends the stretch. The idle decision, the sleep
  and the wake are audited (`deploy.environment.idle`, `.sleep`, `.wake`) and are what the lifecycle
  feed attributes the stop and the start to; the summary's `asleepSince` makes the project read Asleep
  with Wake now (`POST …/sleep/wake`), the host's container alert reads it as fine and the availability
  figure leaves it out. `GET …/environments/{env}/sleep` is the reading.
- A failed build names its cause instead of `internal_error` and exec's `exit status 1`. `dockerx`
  reads buildx's plain progress as it streams (over `os.Pipe`s read to EOF, because a `StdoutPipe` is
  closed by `Wait` and dropped BuildKit's closing lines; once buildx has exited a read waits at most
  five seconds for more, since a process it left behind can hold the pipes open) and returns a
  `BuildError` with the failed RUN's command, exit code and step number, or BuildKit's own reason for
  a failure that was not a process exit;
  a build that reaches its 30-minute limit while the run is alive returns `ErrBuildTimeout`, which does
  not wrap a context error and so is never recorded as a cancellation. The builder redacts the
  transcript of the run's secret build values and of any plain one carrying credential material, never
  of other plain values (`buildLogRedactions`, with the sensitivity `OpenRunScopedVariables` returns).
  The executor feeds the persisted, already redacted transcript to a bounded collector (`build_output_cause.go`): a ring per open BuildKit
  step (400 lines, 64 KiB), one over the stream (64 KiB), BuildKit's replayed and Dockerfile-excerpt
  lines refused. A step that finishes (`DONE` or `CACHED`) is dropped from both: what a passing step
  printed on its way — npm retrying a request that hung up, a config probing for `git` — is never a
  later step's cause. The failed step's lines are read first against one ordered signature table
  (`build_output_signatures.go`), then the stream's, which by then holds only the steps still unfinished
  and BuildKit's closing replay; the phase comes from which rendered instruction failed (the recipe's
  install line, the plan's build command, the output guard, a setup line, or any step of a custom
  Dockerfile). The resulting `BuildCause` — code, phase, command
  (redacted, 160 characters), exit code, at most five validated identifiers, the transcript line that
  proves it and a fix — is the step's evidence `cause`; its code is the step's and the run's terminal
  code (`build_failed` when nothing matched) and its sentence the message, also written to the
  transcript as `Diagnosis:`. The fix is computed from analyze_plan's recorded `candidate` when present,
  else the plan's stored candidate for the same root and method (told apart by the plan's build and
  start commands when several share them, as an Nx workspace's applications do), and the run's variable names and
  scopes: the package manager whose lockfile is in sync, a variable or its build scope, a rewritten
  runner, a Go or Python release the recipe offers, `NODE_OPTIONS` sized to three quarters of the
  server's memory, `prisma generate` before the build, the detected output directory. A Compose
  service's build is announced on the status stream so its steps are read apart and named with the
  service; an image or Compose pull failure is named from the registry's error, including a refusal the
  daemon returns before the pull's stream starts (`dockerx` prefixes it `pull image:` like one inside
  the stream; only an unreachable daemon keeps its own error). A base image that cannot be resolved
  keeps its cause (`registry_rate_limited`, `registry_unreachable` — DNS, TLS, refused and timed-out
  connections, Go's `Client.Timeout exceeded` and a resolver's `server misbehaving` —,
  `base_image_missing` — also Docker Hub's "repository does not exist or may require 'docker login'" —,
  `registry_auth_failed`, `builder_missing`) with the daemon's own bounded reason in the transcript — a
  .NET SDK image a `global.json` pin names that was never published is `dotnet_sdk_pin_unavailable`
  instead —, a JDK or .NET release the recipe refuses at prepare_context ends the run with preflight's
  own code (`java_version_unsupported`, `gradle_wrapper_incompatible`, `dotnet_version_unsupported`), and
  a candidate's start names `runtime_port_in_use`, `image_missing` and `mount_invalid` from Docker's
  refusal without repeating it. A context deadline that reaches a step while its run is alive is
  `step_timeout`; only a cancelled context is a cancellation. The signature table and fixes are listed
  in [the recipe contract](recipes.md#failure-diagnosis).
- A run that failed or rolled back can be asked about, on request only, of the provider an
  administrator set up for the assistant ([the editor's assistant](../backend/databases-proxy-platform.md#the-editors-assistant)):
  `POST /deploy/{id}/runs/{run}/diagnosis` (`service.control`, the assistant's own capability, and the
  install's one budget of model calls and four answers at once; `api/handlers_deploy_diagnosis.go`).
  What is sent is decided in `deploy/run_diagnosis.go` and is what the run's page already shows any
  signed-in account: the run's outcome, the failed step (the run page's rule: the first failed or
  blocked step of the latest attempts) with its code, message and the cause its evidence records —
  a build's or release task's `cause`, a health gate's `diagnostics.cause` and its scalar readings of
  the container — including the fix it offers; the settings the run used (`ExecutionPlan`: source
  kind, branch and commit, build method, recipe, framework, versions, package manager, root, commands,
  output, release tasks without their environment, runtime port, strategy and limits, checks); its
  variables by name, scope and sensitivity, never a value; every step's state; and the end of the
  transcript — the failed step's own last 170 non-blank lines and up to 30 written after it, each
  without terminal escapes and cut at 400 bytes, 24 KiB in all, newest kept. The whole text is masked
  again on the way out (`DiagnosisMasker`), because the transcript was masked when it was written with
  the values the run had then: by value, every secret variable of the environment as it is now and as
  the run had it, every plain one that carries credential material or looks like a key
  (`tokenShaped`: twenty characters or more, one word, letters and digits, not an address or a
  digest), what a `credential` or `database` reference points at in the store (the secret, the saved
  address and its password), every saved deployment credential, the environment's trigger secrets and
  a carried-over project's `deploy_env` values (`PlanningStore.DiagnosisSecrets`, which contacts
  nothing; values under four characters are left, since masking them would mask ordinary words); and by
  shape, a private key, a URL's password, an `Authorization` value, a value assigned to a name that
  says password, secret, token or key, and the tokens of GitHub, GitLab, Slack, AWS, Anthropic and
  OpenAI, Stripe, Google, npm, Hugging Face, Docker and JWTs. A variable that cannot be opened stops the
  request with `409 diagnosis_unmasked` before anything is sent. The model is told the shape to answer in
  (`DiagnosisSystemPrompt`: CAUSE, EVIDENCE quoted lines, numbered STEPS), and `ReadDiagnosisAnswer`
  reads it leniently; a quoted line is kept only when it is a line that was sent (matched by its
  sequence or its text, BuildKit's own step and cache lines left out, at most four, in transcript
  order), and the rest are counted as `unmatched`. An answer in no shape is kept whole. The answer, the
  exact masked text that was sent, the provider, model, tokens, who asked and when are kept in
  `deploy_run_diagnoses` (one row per run, replaced by Ask again, gone with its run), which
  `GET …/diagnosis` reads for any account that may read the run; a second press without `again` answers
  the kept one and costs nothing, and a press while one is in flight is `409 diagnosis_running`. Every
  press is audited as `deploy.run.diagnose` with the provider, model, tokens, lines sent, values masked
  and outcome (`answered`, `kept`, `failed`, `unusable`, `unmasked`), never what was sent or answered.
  Nothing the answer says is applied. The live check against a real model is
  `TestLiveFailedBuildIsDiagnosedByARealModel` ([CONTRIBUTING](../../../CONTRIBUTING.md)).
- A deployment's environment can be asked about as a conversation from the overview's **Diagnose**:
  `POST /deploy/{id}/assistant` (`service.control` and a session; `api/handlers_deploy_operator.go`,
  the tools and prompt in `assistant/operator.go`). The same provider and budget answer it; it begins
  knowing what the overview shows and reads more — runs, a run's transcript, the live containers' last
  lines, usage and the sizing verdict, container events, host alerts (`system.admin`), the firewall's
  rules for the deployment's ports, pending security updates — only through tools whose every call the
  person allows or declines first, each bounded, read with the person's role and masked by the
  diagnosis's masker. It proposes a restart, redeploy, rollback, instant rollback, the suggested
  limits or a maintenance switch through `propose_action`, which runs nothing: the page draws a card,
  and its press is the request the project's own button makes to that action's route, with its
  capability, confirmation and audit. See [the deployment operator](operator.md).
- Deploy and force-build resolve the current desired revision (force-build disables cache); redeploy and
  rollback clone only available immutable artifacts and traverse the same checks/cutover path (a clone
  carries its target's own source identity and promotion provenance; a rollback moves no data, and the
  data step is separate: the rollback dialog can ask, once the rollback succeeds, for each linked
  database restored as of the target release into a side copy — `POST …/releases/{release}/data-copies`
  with `afterRunId` — never into production, see [data-aware rollback](data-aware-rollback.md)); promote takes a staging release's
  exact image with production's current plan and variables, builds nothing, and runs a deploy's steps from
  `render_runtime` on, the migration rehearsal included — or, into staging, an administrator-approved pull
  request preview's exact image with staging's plan and variables, refused into production
  ([staging and promotion](staging-promotion.md)); restart
  stops and starts the existing live runtime without creating a release, then verifies the plan's
  readiness and smoke checks against that live release before recording it. Rollback, stop and restart use
  the destructive capability and ordinary confirmation, not a typed phrase; because stop and restart share
  their enqueue route with deploy and redeploy, which stay at `service.control` alone, the handler checks
  the capability and the shared destructive rate budget by hand instead of wrapping the whole route in
  `s.destructive`. Stop and start each perform one half of a
  restart against the same live release: stop stops the runtime in place and records it `stopped`,
  skipping every other step but `record_release` and `notify`; start starts an already-stopped runtime,
  verifies the plan's readiness and smoke checks against the live release exactly as restart does, and
  records it `live` again. Both are light-slot manual actions that refuse a release target that is no
  longer live, refuse a legacy Compose or preview environment, and refuse a runtime that is not in the
  state the operation expects (`already_stopped`, `not_stopped`); a start that fails leaves the runtime
  as stopped as it was found, since no compensation is possible. A runtime still recorded `live` whose
  release containers Docker reports all down counts as stopped for start (`ReleaseRuntimeDown`, checked
  at admission and again in `start_candidate`): containers that exit, are stopped in Docker, or are not
  brought back after a daemon restart never pass through a stop run, and the project page already draws
  that observation as stopped and offers Start. A Stop's own run page — where pressing Stop lands — reads
  as the runtime it left rather than as a release that went live: the run is *Stopped* (`runStopped`),
  its one stage is Stop and `start_candidate` is *Stop live release* (Restart's and Start's are named for
  the live release the same way, in the run lists' current step too: `liveReleaseStepLabels`), the
  outcome says the release is stopped with Start beside it until a newer run starts it again, and it
  offers neither Redeploy nor Visit and fires no confetti. Only a rollback's identity line names a
  release it rolls back to; every other operation's `targetReleaseId` is the live release it acts on. The fleet and workspace read models report the
  live runtime's stopped state as `stopped`, folded into the existing batched runtime join, and the Git
  watcher persists the decision reason `stopped` instead of enqueueing an automatic deployment while the
  live runtime is stopped; a manual deploy remains allowed and, on success, leaves the new release live
  and running. The watcher's cursor still advances with each push while stopped — unlike its `manual_only`
  pause, which leaves the cursor at its prior revision — so a commit pushed during that time is deployed
  by the next push, not retroactively by `start`.
- `PlanConfiguration.Validate`'s most common refusals — runtime ports (0 is unset and always allowed; the
  message no longer implies otherwise), a runtime mount, a dependency, a domain, a variable (keyed by its
  own name, not a position) and a check or release task (keyed by array index) — wrap a typed
  `ValidationError{Field, Message}` behind `ErrInvalidPlan` (`fmt.Errorf("%w: %w", ErrInvalidPlan,
  validationErr)`, so an existing `errors.Is(err, ErrInvalidPlan)` check is unaffected). `PUT
  …/configuration` and the draft configuration step surface it as `{"error":{"code":"invalid_plan",
  "message":…, "field":"runtime.internalPort"}}`, so the UI can attach the refusal to the one control it
  is about instead of a plan-wide banner; a refusal outside those categories still has no `field`.
- Normalized configuration edits are desired state only. Saving runtime/domain/dependency settings or a
  variable clones the source/build/runtime rows into the next complete revision and never moves the live
  release pointer. Pending state compares that desired revision with the live release's exact plan and
  frozen variable/dependency/check snapshots, by names and digests only; a run clears only the revision it
  actually applied, so a change saved after enqueue stays pending. It is pending only when that comparison
  names a change: the revision number counts saves, so an edit undone (a variable added then removed) moves
  it on with nothing to deploy. A variable compares by value digest, sensitivity, scope set and literal/reference mode, so a
  scope-only edit is still named. The fleet summary's `pendingChanges` — the header's "Deploy changes",
  the Settings dot, the fleet chip — runs the same comparison for each deployment whose desired revision
  is not the live one.
- Deployment variables are encrypted, immutable revisions with an exact closed scope set (`build`,
  `runtime`, `release_task`). Lists use a fixed mask; reveal is a separate session-only admin read with an
  explicit audit entry. Stored `value_mode` keeps literal values literal, including reference-shaped
  text. New values and dotenv entries are literal; an explicit reference uses reference mode. Empty
  mode preserves historical value-shape inference. Bulk dotenv parsing is bounded and inert. Full typed references are parsed into a
  closed kind/target model; missing variable references and cycles fail before commit, secret leaves stay
  masked, and enqueue freezes exact variable revision ids so retries cannot observe a later variable
  rotation. Execution resolves external credential/database/domain/Compose-service references only through
  their owning stores; missing or ambiguous targets fail closed instead of reaching a workload as literal
  reference text. Run-scoped domain and Compose references use the frozen run plan/dependency snapshots.
  `POST …/variables/import?dryRun=1` (the import's own tier: `system.admin`, session or deploy key) takes the
  import's body and writes nothing: it answers one verdict per name, in the order the names were
  written, with the line — `added`, `changed` (the value, sensitivity or scopes differ), `unchanged`,
  `skipped` (a name the body's `skip` list leaves out: the sheet sends `PORT` and `NODE_ENV`, which the
  deployment sets itself, unless the operator keeps them), or `refused` with `invalid_name`,
  `duplicate` or `invalid_value` — so the import sheet can say what pressing Import will do before it
  is pressed. The import leaves out the same names. Values meet the stored ones only as digests, on the
  server, and the answer carries neither a value nor a digest. It is audited under an action of its
  own, `deploy.variable.import_preview` (the name count, sensitivity and scopes), rather than as the
  import it did not perform. What the import refuses as a whole — a
  line the parser cannot read past or a bad sensitivity or scope —
  is refused with the import's own error; a refusal that belongs to one name is that name's verdict,
  so every other line can still be read. `dryRun` must parse as a boolean, because a preview that
  failed to parse must never fall through to the import. Both paths share `parseDotenvEntries`, and
  `components/deploy/settings/dotenv.ts` is that parser written again line for line, so the sheet
  can draw a preview while the server's is in flight, and the server's verdicts replace it.
- Shared variables (`deploy/shared_variables.go`, table `deploy_shared_variables`) are values defined
  once and read by reference: the server's as `${{server.NAME}}` from any project, a project's as
  `${{project.NAME}}` from that project's environments only (a preview environment is one of them).
  Nothing is injected by name — an environment reads a shared value only through a variable of its own,
  with that variable's scopes, so a shared name never shadows or fills in one of the environment's. A
  shared name starts with a letter (it is a reference target); a shared value is sealed like a
  variable's, has no revisions, and is always a literal, so reference-shaped text in one is never followed
  and shared values cannot chain or cycle across scopes. The reference is what a variable revision holds,
  so a release freezes the reference and a run opens the value when it reads its variables, exactly as a
  credential reference does: a change reaches a release through its next deploy, and a rollback to an
  older release reads today's value. A run whose shared variable is gone fails closed rather than handing
  the workload the reference's own text. Saving a variable that references one checks that it exists in that scope and refuses a plain
  variable reading a secret one; a new project's wizard refuses `${{project.…}}` (the project does not
  exist yet) and checks `${{server.…}}` before commit. Only values saved in `reference` mode are read as
  shared references: a value saved before modes were recorded keeps the shape inference of the kinds that
  existed then, so a legacy `${{server.X}}` stays the literal text it always was (`parseStoredReference`).
  Routes: `GET /deploy/shared-variables` and `GET /deploy/{id}/shared-variables` list them masked with
  every active variable that reads each (`usedBy`, with `waiting` where the environment's live release
  started before the value last changed) for anyone who may read an environment's variables; `PUT …/{name}`
  (create or change; an omitted value keeps the sealed one) is an administrator's in a session or a
  deploy key's, like a variable's, and `GET …/{name}/reveal` a session's only; `DELETE …/{name}` is
  `s.destructive` in a session or with a deploy key and
  answers `409 shared_variable_in_use` naming each reader while any active environment's variable reads it.
  Audited as `deploy.shared_variable.{create,update,reveal,delete}` with the scope and never the value. A
  project's shared variables go with the project when it is purged.
- Store references (`deploy/secret_store_references.go`, connections in `internal/secretstore`, table
  `deploy_secret_stores`) read a value from Vault, OpenBao, Infisical, Doppler, AWS Secrets Manager or Azure
  Key Vault as `${{store.NAME/path#KEY}}` (`@N` pins a version; for the last two the path is the secret's
  name, the key is optional and the version is the store's own id or staging label). They resolve through the same graph, as secret
  literals, and only from a secret variable. A run reads every store it references at its first step
  (`resolve_source`, a rollback's `render_runtime`, a restart's `start_candidate`; never a stop's),
  fresh, keeps the values in memory for its own later steps, and fails as `secret_store_unreadable` in the
  store's words with nothing touched when a store refuses. The transcript and `secretStoreReads` in the
  run's metadata record each reference's version, never its value. A release freezes the reference, so a
  rollback re-reads the store and says when the version differs from the restored release's; a pinned
  version rolls back exactly. A preview reads only a store that allows previews. Contract, routes and
  audit: [`secret-stores.md`](secret-stores.md).
- Domains, persistent storage, backup jobs and database entries remain resources of Proxy, Docker,
  Backups and Databases. Deployments store typed ownership links and use read-only owner observations for
  domain conflicts, DNS, existing certificate pairs, ports, public binds, firewall policy and dependency
  availability. A deploy re-runs those host observations from its frozen configuration in `analyze_plan`
  — alongside the source checks above — before build or backup work; only its exact managed proxy site and exact live Docker runtime may be
  treated as reusable ownership. HTTPS activation resolves an already-issued certificate/key pair through
  Proxy and fails closed if it no longer exists; deployment activation never invents certificate paths or
  performs issuance itself.
- A configured backup dependency executes as a step before candidate start. A linked database whose
  connection the backup job dumps natively is covered by that dump (`BackupGateEvidence.databaseDumps`
  lists it); only a database the job does not dump falls back to covering the engine's files. The
  Backups adapter verifies coverage, success, freshness and any required restore-test evidence and
  returns only bounded evidence;
  a required failure terminates the run before candidate startup or a live-pointer change. Restore evidence
  comes from the Backups owner's isolated application checker, bound to the exact artifact, image,
  schema, canary and completed cleanup. Missing or failed evidence blocks; it is never inferred from
  artifact existence. See [restore verification](restore-verification.md).
  Coverage resolves named volumes and every writable merged Compose service mount, verifies the immutable
  manifest and artifact checksum, and rejects filtered or uncovered data. See [backup coverage](backup-coverage.md).
- Existing-workload discovery and adoption were removed by reverting #136 and #145. The
  `/deploy/import` page and workload discovery/recovery endpoints are absent. The older
  `/deploy/import/preview` and `/deploy/import/adopt` APIs remain only for Git-checkout imports;
  container and Compose workload imports are refused.
  Previously adopted projects and legacy external observations retain their database records and
  runtime resources, but project mutations, new runs, queued execution, provider hooks and schedules
  are refused. Their baseline and native startup authority require the removed engine; use the
  original Docker/Compose/PM2/systemd tools, or restore an import-capable version to manage them.
  Existing Git-checkout sources remain supported. The additive `adoption_enc` and `value_mode`
  columns and the immutable-value trigger remain in the migration path. Literal/reference value
  interpretation and variable snapshot digests are retained for compatibility with saved releases;
  no stored data is erased.
  Stop or drain deployment runs before switching versions. An import review draft cannot be committed
  through the ordinary draft endpoint. Archiving disables deployment triggers, the project's schedules
  and visibility; it never removes runtime or data. Schedules are disabled directly (`ClaimDueSchedules`
  already excludes an archived *environment*, not an archived *project*, so without this a schedule kept
  firing and failing every occurrence at `environment_not_found`, invisibly, because even its summary
  marker run could not be enqueued); `POST /deploy/{id}/unarchive` (`system.admin`, session) restores the
  display name from `archived_name` — `409 name_taken` if another active project has since taken it — and
  clears `archived_at`, but leaves triggers and schedules exactly as disabled as Archive left them, audited
  as `deploy.unarchive`: undoing the archive is not a decision to resume automatic deployment, and the
  operator re-enables what they actually want running. A separate destructive route first returns a
  digest-bound, managed-only target list; data targets require their exact resource name and every removal
  is delegated to its owning feature and audited. Linked and observed targets never enter that plan. A
  remover's own failure — "Docker is unavailable", "remove application containers before removing their
  database network" — answers `409 removal_failed` (or `503` when the failure is specifically a missing
  owner) with that exact sentence and the partial `execution` (`removed`, `remaining`) beside the error,
  rather than collapsing to a generic `500`.
- `PUT /deploy/{id}/environments/{env}/releases/{release}/pin` `{"pinned": bool}` (`system.admin`, audited
  `deploy.release.pin`) sets `deploy_releases.pinned`, which `ArtifactRetentionPlan` already reads: a
  pinned release survives a prune plan regardless of its age or rank among the environment's other
  releases.
- **The data point.** When a release goes live, `activate` records, right after the pointer commits,
  where each database the environment links stood (`deploy_release_data_points`, once per release and
  database): the activation moment when the server's point-in-time archive reaches back that far, else
  the newest backup run holding its dump, else none with the reason. It is read from the store alone and
  never fails the activation. The release list carries `dataPoints` and `dataCopy {eligible, reason}`,
  the comparison both releases' points and `changes.dataPoint`; the rest is
  [data-aware rollback](data-aware-rollback.md).
- Remote Git production branches default to monitoring after the first explicit deployment,
  including existing projects. The environment's [automatic deployment policy](git-policy.md) supplies
  manual-only mode and shared include/exclude filters for polling and push hooks. `GitWatcher` checks refs every five seconds
  with four bounded concurrent observations using the source adapter's credential isolation and
  `git ls-remote`. This works behind the dashboard's private network allowlist without public ingress
  or GitHub hook registration. Outages and slow Git reads can delay detection; it is polling, not an
  instantaneous push-delivery guarantee. Tags, local checkouts, legacy Compose and archived projects
  are excluded. Monitoring status and access failures appear in the project identity line
  and on Settings → General's Automatic deployment section.
  `deploy_git_watches` persists the last observed revision, policy digest, decision reason and observation generation: restarts,
  failed runs, duplicate provider deliveries and a crash after enqueue cannot cause repeated builds;
  a later branch change (including a force-push back to an older commit) remains eligible. Watching
  never advances a live release pointer. Eligible web/static projects retain blue/green health-gated
  activation; exclusive-storage and other stop-first workloads retain their documented downtime and
  recovery contract. Archiving stops monitoring.
- Deploy/force-build source resolution records the current remote Git object in the additive,
  immutable `deploy_runs.source_revision` field before enqueue; execution derives its source identity
  and digest from that frozen revision. A manual `deploy`/`force_build` on a normalized project whose
  source is a remote Git repository may instead name `sourceRevision` (a 40- or 64-hex object id, frozen
  as-is) or `ref` (a branch or tag name, resolved through the same `git ls-remote` path
  `ResolveGitRevision` uses for the configured branch, `HostSourceAnalyzer.ResolveGitRef`) — never both.
  Anything other than a remote Git source refuses either field with `400 ref_not_applicable`, and so
  does a `ref` naming a pull request head (`refs/pull/N/head`, `refs/merge-requests/N/head`) on anything
  but a preview environment, before git runs — the same name in `PUT …/source` outside a preview or in
  a draft's source answers `400 invalid_ref`, since such a head is built only through "Test this pull
  request"; a name the remote does not have answers `400 ref_not_found` (git's own `ls-remote --exit-code` exit status 2, "read
  the remote fine, no such ref"), and a remote that could not be read at all answers `502
  source_unavailable`. The configured branch's own resolution distinguishes the same exit status: a
  renamed or deleted branch is `ref_not_found` for enqueue, detection and the watcher alike. Every other
  git failure is classified inside `runPlanningGit` from its bounded output against a fixed substring
  table (`source_failure.go`) and only the code crosses the boundary — git's stderr is remote-controlled
  and still never reaches an error, an audit detail or a log: `source_auth_failed`,
  `source_repository_missing`, `source_unreachable` (also any fetch that timed out) and
  `source_revision_unavailable` (a recorded commit a force-push or deleted branch removed; the release
  mirror names it only when a full fetch of the branch no longer contains the commit, since a branch
  that merely moved on still holds it as an ancestor and the release builds it) each carry a fixed
  sentence as `SourceFailure`, which unwraps to `ErrSourceUnavailable`. The API answers them `502` with
  the code; the run's `acquire_source` step and terminal code carry it; the watcher records it as its
  cursor reason, which the header's auto-deploy reading and the diagnosis (`auto_deploy_stopped`,
  linking to the source settings or Credentials) name. The resolved revision and, when given, the
  requested ref name are recorded as `{"requestedRevision", "requestedRef"}` in the run's metadata so
  the UI can say "Deploy of v1.4.2". This never touches `deploy_git_watches`: that cursor is written
  only by the watcher's own poll/webhook path, so a manual deploy of an older commit cannot make the
  watcher believe the branch lives there and suppress its next real push. Retries copy it, while
  redeploy/rollback reuse their selected release's identity. Old runs keep their saved plan identity.
  The deprecated draft `autoDeploy` input remains accepted for compatibility but has no effect; draft
  commit no longer creates a hook without a secret or provider registration. Automatic runs are audited
  as `deploy.git.change` and use the same persistent queue, configuration/variable snapshots, checks
  and activation as manual runs. For a Git source, `acquire_source` additionally reads the frozen
  revision's subject, author and date with one `git log` against whichever repository it already has
  open — the materialized workspace, or a local checkout — and folds a `commit` object (`sha`,
  `subject` capped at 200 characters, `author`, `authoredAt`) into the run's metadata through
  `OrchestrationStore.MergeRunMetadata`, the one path that mutates `deploy_runs.metadata_json` after
  enqueue. An unreadable history logs a transcript line and leaves the field absent rather than failing
  the step. That line goes to `stderr`: the transcript accepts only `stdout`, `stderr` and `status`,
  and the `warning` stream it was first written on was refused, which failed the append and silenced
  every line the step logged after it. A retry's wholesale metadata copy, plus every run independently
  re-deriving the same commit from the same frozen revision, is what carries it forward through a
  retry, redeploy or rollback. `GET /deploy/{id}/commits` still reads a legacy project's local checkout
  only; a normalized Git-URL project's private release mirror keeps only narrow per-revision snapshot
  refs, not a maintained branch tip, so listing its history cheaply from that mirror remains open.
- **Deploy keys and `jd`.** A deploy key (`api_tokens.scope = 'deploy'`, `POST /tokens/deploy`, an
  administrator's in a session) is admitted to the deployment routes `deployKeyRoutes` in
  `api/deploy_keys.go` lists and to no other route; wherever this document gives a drafts,
  configuration, source, Git policy, trigger, variable or shared-variable write the tier
  "`system.admin`, session", a deploy key is admitted too, and every other session-only deployment route
  (reveals, saved credentials, secret-store connections, pull-request merge/comment and previews,
  schedules, alerts, channels, drains, adding and removing an environment, a route's maintenance and
  error pages, the warm-release terms, the migration rehearsal's terms, a copy of a release's data —
  asking for one, its address, keeping it longer, removing it and comparing it — the
  typed-phrase removals) stays a session's and answers it `session_required`. The operating verbs an
  ordinary key already had are the deploy key's as well: `POST …/promote` (a pull request preview's
  image into staging too, which takes the administrator role the key carries; approving the preview's
  commit stays a session's),
  `POST …/rollback-watch/rollback` and `…/keep`, `PUT …/maintenance` (who is let past is the
  configuration's `runtime.maintenance.bypassFrom`, applied by the next release), with the reads
  `GET /deploy/{id}/environments`, `…/promotion`, `…/rollback-watch`, `…/maintenance`,
  `…/pages/{page}`, `…/rehearsal`, `…/data-copies` (what each copy is of and until when, never its
  sign-in) and `…/sizing`, and the fleet's `GET /deploy/sizing` narrowed to the key's deployments;
  `GET /deploy/secret-stores`, an ordinary key's read, answers it
  `key_scope`, since the list carries each connection's address and sign-in fields and every
  deployment's variables that read a store, and the key cannot point a variable at one. Its writes are
  held to what a person in a session could not be refused: a generate or rotate answers it without
  `generatedValue`; a plan it commits, or an environment change it makes, may not carry host authority
  (`hostAuthority`: a folder on the server, Docker Compose, privileged, host network, capabilities,
  devices, a host path mounted, a release task without `runner: "image"`), and an environment that
  already does, or whose variables read a `credential` reference, is changed by a person only (it may
  still be deployed and have its variables set); a source may not gain a saved credential or send its
  Git credential to another repository; a variable may not be pointed at a `credential`, `database` or
  `store` reference (on a draft, checked at commit with `Draft.VariableReferences`, which reads a value's mode
  as the commit stores it), nor, for a key narrowed to some deployments, at a `server` shared
  variable. A key narrowed to chosen deployments
  (`api_token_deployments`) lists and reaches only those. Drafts are every deploy key's own
  (`deploy_drafts.key_id`; `AuthorizeDraftForKey`): a key never reads, changes, commits or discards a
  session's or another key's, and a narrowed key's commit adds the deployment it creates to its grant
  in the commit's transaction (`DraftCommitRequest.GrantToKey`), so the key reaches what it made;
  duplicating is of a deployment the key reaches, and the draft is the key's. `GET /deploy/{id}/runs/{run}/transcript?after=&limit=`
  reads a run's log lines after a sequence number, oldest first, with the run and its steps and
  `{next, more}` (`OrchestrationStore.RunTranscriptAfter`): what the stream route sends over its socket,
  for a client without one — `jd logs --follow` and `jd deploy --wait` poll it. The `jd` program itself
  (`backend/cmd/jd`) is described in the README. Every route of this API is described as OpenAPI 3.1 at
  `GET /deploy/openapi.json` (`.yaml`; `jd openapi`), generated from the router and the handlers' types
  and kept honest by its tests — see [`../backend/deploy-api.md`](../backend/deploy-api.md).
- Additional automation provider hooks verify each provider's exact raw-body signature before parsing and then fence
  event, repository, ref and delivery identity. The delivery row is reserved before preview or queue side
  effects, while legacy HMAC and scoped generic hooks retain their existing contracts. Environment branch/
  path policy applies to polling and provider/generic hooks; manual and rollback runs are not path-filtered.
  `GET …/triggers/{trigger}/deliveries` (`system.admin`, session or deploy key) reads the last 50 `deploy_webhook_deliveries`
  rows for a trigger — delivery id, event, ref, decision, reason, run id, received at — or `?limit=` of
  them, 1 to 200 (anything else reads 50), and
  `POST …/triggers/{trigger}/rotate-secret` (same tier, audited `deploy.trigger.rotate_secret`) issues a new
  HMAC secret shown once, mirroring the legacy project's own secret rotation. The trigger list carries a
  summary of that log — each trigger's `lastDelivery` and `recent`, its last fourteen decisions oldest
  first, read for every trigger in one windowed statement (`AttachTriggerDeliveries`) — but only for a
  session or deploy key holding `system.admin`, the caller who could read the log itself; the list is readable by any
  account, and a read-only one still sees `lastStatus` alone. Absent means not allowed or never
  delivered, and the page then draws no strip rather than an error.
  A new *automatic* run only supersedes an older queued one in the same environment when its own
  `changedPaths` cover the older run's — an empty set can only cover another empty set — so the git
  watcher and provider webhook handlers record `run.metadata.changedPaths` only from the watcher's own
  complete baseline diff (computed when a watch-path filter is configured) instead of always freezing
  `[]`. The provider payload's own per-push commit list is not a baseline diff — two disjoint pushes each
  have a non-empty, non-covering list — so it stays scoped to matching the webhook's own watch-path filter
  and never reaches run metadata; leaving metadata unset in every other case (the common unfiltered push,
  and every preview push) reads as "unscoped" instead. A schedule's own dispatch never claims to
  know changed paths, so its `[]` reads as "unscoped": a routine scheduled action queued behind a real
  push no longer supersedes it, while a real push still supersedes a stale unscoped schedule ahead of it.
- The scheduler advances a persisted next-run claim atomically and executes a bounded, ordered action
  chain. Chain history stores only action/status/error-code/duration evidence. A listed schedule carries
  `nextRuns`, its next five firings: the stored `nextRunAt` first, because that is the dispatcher's own
  answer, then four more walked from it in the schedule's time zone (`NextCronRuns`), so a
  daylight-saving change lands on the page where the dispatcher will put it. A paused schedule has
  none, and an expression that no longer walks — a zone gone from the host's tzdata — is left to the
  dispatcher to disable. `POST …/schedules/test` (audited `deploy.schedule.test`) answers the same five
  beside `nextRunAt`, which is what the schedule sheet's check shows before a schedule is saved.
  Preview environments require
  session administrator approval of each exact PR revision before creation or execution. An approval
  carries `headRef`, the branch the pull request proposes, kept apart from the ref the build fetches
  (the provider's own `refs/pull/N/head` on GitHub and `refs/merge-requests/N/head` on GitLab, names
  nobody chose); approvals recorded before it was kept have none.
  `POST …/previews/approvals/{approval}/reject` (`system.admin`) moves a `pending` or `approved` row to a
  new closed state, `rejected`: `ApprovePreview`'s own `WHERE state IN ('pending','approved')` already
  refuses it afterward without any extra check, and the trigger's next delivery for that pull request
  inserts an independent new `pending` row at its own revision — a rejection does not follow it. They start without
  inherited variables, linked databases or release tasks; container mounts become fresh preview volumes on
  a dedicated bridge. A Compose stack is isolated service by service — every service on the preview's
  bridge alone, its mounts on preview volumes (a read-only file of the commit itself excepted), no secret,
  fixed name or published port but the public service's loopback one — and a stack whose service needs the
  server itself, a compatibility Compose plan and other host-access plans fail closed ([Compose
  previews](preview-isolation.md#compose-previews)). An environment can give its previews a
  [database branch](preview-isolation.md#database-branches) of a database it links — the preview's own
  copy, restored from the newest backup through the rehearsal's `prepareCopy` onto the preview's database
  network with a password made for it, chosen columns masked in their shape, never for a fork, removed with
  the preview — and the preview's `${{database.N…}}` resolves to it. PR close retires only owned
  resources and archives after successful cleanup. Reopening waits for cleanup and a new approval
  generation; closing cancels queued/build work. Startup quarantines older unsafe previews, stops owned
  containers without deleting data, withdraws routes, and blocks old releases from activation. Failed
  isolation retries while the UI reports the block. See [preview isolation](preview-isolation.md).
  A preview is also started from the dashboard without any delivery: `POST /deploy/{id}/pull-requests/{number}/preview`
  (`system.admin`, session; `{revision, copyVariables, acceptFork}` → `202 {preview, runId, copied,
  skipped, address, variablesFromProduction}`) is the administrator's approval of that exact head
  (`RecordPreviewApproval`, audited `deploy.preview.test`), hung on a "Pull requests" trigger the route
  makes per environment (`EnsurePullRequestTrigger`: GitHub, `preview: true`, quota 5, no domain, no
  delivery; named "Pull requests (owner/name)" when an unrelated GitHub trigger holds the plain name,
  `409 trigger_name_taken` when both are held), optionally copying production's non-reference variables
  the preview has not set for itself into the preview
  (`PlanningStore.CopyEnvironmentVariables`, refused for a fork with `400 preview_fork_variables`) and
  allocating the preview a tailnet port (`AllocatePreviewAddress`, 21000–21999). The trigger's quota and
  a free port are checked before the approval is written (`previewCapacity`: `409 preview_quota`, `409
  preview_address_exhausted`), and the audit row is claimed before it too, so a refusal past that point
  is recorded against the head it was for. The pull request's head, fork-ness and state are read through the GitHub
  App's installation, else the dashboard's own gh login, never through a checkout (`api.pullRequests`,
  identity cached two minutes, listings 30 s, four gh subprocesses at a time). `GET /deploy/{id}/pull-requests`
  answers `{repository, host, available, reason?, identity?, checkoutPath?, pulls[] (each with its
  preview), previews[], production {environmentId, automatic, intervalSeconds, awaitingFirstDeployment},
  tailnet, compose, composePreview?, databaseBranches, localCheckout, internalPort, releaseTasks}` —
  `databaseBranches` are the databases production gives its previews a branch of, each preview carries its
  `branches`, `composePreview` is a
  Compose project's `{public, services[], refusals[]}` and a live Compose preview carries its `services`
  as Docker reports them — `reason` is one of `not_github`,
  `sign_in_required`, `app_not_installed`, `not_installed`; `previews` lists the open ones and a closed
  one only while its newest `preview_remove` run has not succeeded; a read GitHub refuses answers an
  error (`429 github_rate_limited` for a quota refusal — a 429, or a 403 or gh's text saying rate limit,
  never a bare number such as pull request #429; `502 pull_request_unreadable`; or gh's words) rather than
  `available:false`, and the Overview hides the panel. `GET …/pull-requests/{number}/checks[?head=]`
  answers `[]CheckRun`; `GET /deploy/pull-requests` answers `{projects: {<id>: {open, previews}}}` for
  the fleet, one gh read per repository under a 20 s budget. `POST …/preview/close` is destructive
  (session, no phrase; `202 {runId}`, `404 preview_not_found`; idempotent under
  `pull-request-close:<preview>[:a<n>]`; audited `deploy.preview.close`). `POST …/merge` (`service.control`,
  session; `{method, deleteBranch, headSha?}` → `200 {merged, previewRunId, production {automatic,
  awaitingFirstDeployment}}`; `409 github_login_required` without the dashboard's own login) and
  `POST …/comment` (`{body}`, on gh's stdin, `200 {ok}`) speak as the dashboard's own account and are
  audited `github.pull.merge` and `github.pull.comment`; the merge reconciles the repository's previews
  before answering. Storage: `deploy_preview_addresses` (`environment_id`, `kind`, `port`, `upstream_port`,
  `url`, `published`, `updated_at`, `UNIQUE(kind, port)`) and the added columns `deploy_preview_refs.origin`,
  `title`, `head_revision`, `variables_copied_revision` and `deploy_variable_revisions.copied_from_environment`,
  all in `store.addedColumns`. The activation step publishes the tailnet address after the route is
  verified and before the release pointer moves (`TailnetPublisher`, `selfcfg.TailnetServe`; step codes
  `tailnet_unavailable`, `preview_port_missing`, `tailnet_publish_failed`, `tailnet_withdraw_failed`) —
  to the address's front on loopback, never to the container, since the address is a port of the name
  the dashboard is reached by and the front keeps the dashboard's session cookie from the pull
  request's code ([the dashboard's session cookie](tailnet-apps.md#the-dashboards-session-cookie);
  `tailnet_gate_failed` when the front cannot be written) —
  restores the predecessor's mapping on a failed activation while the predecessor runs and otherwise
  withdraws its own and marks the row unpublished (`tailnetRestored`), and withdraws it on removal
  (`addressWithdrawn`) — restore and removal touch only a mapping whose upstream the address row
  vouches for; `SweepTailnet`, run before the engine starts (`Server.startDeployEngine`), withdraws
  loopback mappings in the range the address table does not vouch for (a port no preview owns, or an
  owned port serving another upstream; an operator's directory or funnel is never turned off) and
  unpublishes addresses nothing serves, quietly when the serve config is unreadable and no published
  tailnet address exists, and `FrontTailnetAddresses` then moves an address an earlier release mapped
  straight to its container behind its front; `PreviewReconciler` reads every open preview's pull
  request once a minute and closes the preview only on a decoded closed or merged state, recording a
  moved head as out of date. All of it in [preview isolation](preview-isolation.md#previews-from-the-dashboard).
  Outbound notifications are delivered by engine run observers for every terminal outcome and for run
  start, through Discord, Slack, Microsoft Teams, Mattermost, Telegram, ntfy, Gotify, Pushover, e-mail or a
  signed webhook; a failed or cancelled run reaches
  the same channels as a successful one. Credentials are sealed and never listed, deliveries record only
  a status class and are deduplicated per run and event, and a delivery failure never changes a run's
  outcome. Commit statuses and preview comments — on GitHub, GitLab, Gitea and Forgejo, the last two
  written with a saved token for the host — ride the same hook, and the newest outcome of each is kept
  for Settings. The channel list carries each channel's newest
  attempt and last fourteen outcomes, and each delivery the project and run it announced, so the
  page reads whether messages arrive without a request per channel. See
  [notifications](notifications.md).
- Log drains copy each deployment container's output and the requests its ingress served to an
  OpenTelemetry (OTLP/HTTP), Loki or JSON-lines endpoint while they happen, through
  `logdrain.Manager`, started with the server: a follower per wanted container, a reader per wanted
  request record, and a bounded, retried, gzip-compressed queue per drain whose status the page shows.
  An OTLP drain's `traces` stream sends each request again as a span to `/v1/traces`, under the W3C
  trace the ingress now sends the application (`DeploymentRoute.Trace`: Caddy's `request_header` and
  `log_append`, nginx's `proxy_set_header` and log fields), so the application's own spans join it.
  See [log drains](log-drains.md) and [trace context](request-observability.md#trace-context).
- Each release's image, and the base images its recipe pinned, is read for known vulnerabilities by
  Trivy in a container with no network, no capabilities and nobody's account
  (`imagescan.ScanSpec`), after the build and daily while it is live (`deploy.ImageScanWatcher`, a run
  observer that never holds a run up). The vulnerability database is fetched by the dashboard itself,
  checked against its digest and copied into the `jd-image-scan-db` volume. One row per release keeps
  the counts by severity, the fixable findings, the database's age and whether each pinned base's tag
  now names a newer digest — `ReleaseUpdate`'s registry read, compared with the digest the release
  pinned. A fixable finding at the environment's floor with a newer base is the `image_vulnerable`
  host alert and the overview's Rebuild, which is the runs route's own `force_build` of the live
  commit. See [image vulnerabilities](image-vulnerabilities.md).
- An AI client reaches the deployments over the MCP endpoint when its key was given them
  (`api_tokens.deploy_access`: none, `read`, `deploy`): it lists deployments, runs and releases,
  reads a run's transcript tail (`OrchestrationStore.RunTranscriptTail`, the stored and already
  masked `step.log` chunks, or a pre-engine run's bounded `deploy_runs.log`), and with `deploy`
  queues a deploy or a rollback of a production environment through the same enqueue functions and
  capability checks as the run and rollback routes. Previews, approvals, cancels, retries and
  settings are not offered. See [the MCP server](../backend/databases-proxy-platform.md#the-mcp-server).
- Deployment detail includes a C8 `runtime` observation for the production environment. Docker filters
  managed environment labels at the daemon before inspecting matching containers once each — running
  or stopped, since the set is one environment's (`ListContainersWithLastRun`; the plain labelled
  listing that cleanup and recovery read still inspects only running ones).
  The five-second bounded read returns container/release/Compose identities, the image reference the
  container was created from as Docker reports it (`image`, so a service is drawn as the product it
  runs without a join to the container list), state, health and start
  time, and from the same inspect `restartCount`, `exitCode` (only for an exited, dead or restarting
  container; a running or never-started one has no exit to report) and `oomKilled`, each omitted when
  unknown or zero, without command text, environment values or arbitrary labels. `liveRelease` identifies the
  persisted live release, not a current health verdict. Failed or missing Docker is `unavailable` with
  a fixed recovery hint; a successful empty inventory is `available`. Missing health inspection evidence
  remains `unavailable`. The overview renders these services with live/other-release labels and links to
  the exact Docker container or Compose stack panel. Empty managed inventory, unavailable evidence and
  unassessed diagnosis have distinct wording.
  A Runtime service's Logs verb (and the game console's) opens the project's Logs page with
  `?service=<container>`, which opens its Output view on that container.
  Logs preserves explicit time-window
  links and refuses to substitute another source when the requested container is no longer discoverable;
  `GET /deploy/{id}/runs/{run}/logs` checks project membership and filters Docker by the run's own
  environment and candidate/release id, including preview environments. It provides live source links,
  each source with its container's image, and a ten-minute history window around the latest
  successful activation step's completion only when
  its persisted evidence identifies that release and contains no recovery. It never uses the current
  live release's activation timestamp to describe an older run. Missing/failed activation has no history
  link; removed runtimes point the operator to the transcript or an external log archive. No page
  reads it since the run page's Runtime logs view was removed on 2026-10-05 (the endpoint, like
  `…/runs/{run}/metrics` and `…/traffic`, stays for API callers). The project Logs page's Output view
  reads the observed runtime sources (the live release's containers, "All services" for a stack, then
  older releases') through the service logs (`components/logs/service-logs.tsx`) for live streaming,
  pause/resume, filters, each image's lens and historical search without leaving the deployment. A
  removed selected service is reported as unavailable rather than silently replaced by another
  service.
- `GET /deploy/{id}/operations` is the one operational read. It resolves the live release's own runtime
  snapshot and asks each feature owner once: Docker for containers, Proxy for routes and certificates, and
  the C6 dependency observer for volumes, bind paths, backup jobs and database connections in a single
  batched call. Every section carries its own availability, so a host without nginx or Backups renders
  named unavailable evidence rather than an empty success. A domain row names who issued its
  certificate (`certificateIssuer`, the issuer's common name — `R10`, `E6` for Let's Encrypt — read
  from the same certificate as its name and days left), so the issuer is observed rather than
  inferred from how the domain is owned. A domain covered by the copy a Docker Caddy release kept reads
  `valid` with `certificateRenewedBy: "caddy"` and no days left or certificate link: Caddy renews the
  certificate it serves and never the copy, so the copy's expiry is not the domain's and raises no
  finding. A dependency's `deepLink` is the page that owns it: a backup
  job's own page (`/backups/<id>`) and the linked database's Settings page
  (`/databases/<id>/settings`), each only once the resource is known to exist and the list page
  (`/backups`, `/databases`) until then. A database is a path now — `/databases/<id>` is its home and
  each of its pages a segment under it — so the link no longer selects a connection by query; the
  older `/databases/connection?conn=<id>` a previous release emitted still resolves, through the
  section's redirect for such addresses. Each observation retains its own availability and reason; an unavailable owner is not a healthy result. Release comparison (`.../releases/{release}/comparison`) names source,
  image, command, ports, runtime plan, storage, dependencies, checks and domains, compares variables by
  name and value digest only, and reports artifact retention through the same planner a prune uses.
- The fleet read model batches every per-deployment join. `liveReleaseFacts`, `activeProjectRuns` and
  `recentProjectRuns` use `IN (…)` and `ROW_NUMBER() OVER (PARTITION BY …)`, and `DeploymentSummary`
  reads one deployment rather than filtering the whole fleet in Go. Both are pinned by
  statement-counting tests: the cost of a fleet read is fixed in the number of deployments, and a
  regression fails rather than slows.
  `DeploymentSummary.serviceCount` rides the same batched artifact read `liveReleaseFacts` already runs
  for health: the live release's `runtime_config` snapshot names its Compose service count directly, or
  1 for any release that is not a Compose build, and the count is read from the snapshot regardless of
  whether the runtime is currently live or stopped — no per-project query added.
  The summary, on the fleet and on `GET /deploy/{id}` alike, also says what each project is and how its
  last runs went, which is what a card draws without a request of its own: `sourceRepository`
  (owner/name for Git; the image reference itself for an image source, whose `sourceRef` is empty; the
  image a template renders for a blueprint source, whose `sourceRef` is the template's `id@version`),
  `sourceRemote` (the fetch URL reduced to scheme, host and path, or the
  SCP-style `git@host:path`, with any userinfo dropped — a remote recorded before credentials were
  refused in URLs can carry a token there — and left out when it does not parse), the desired build
  plan's `recipe` and `framework`, `images` (the distinct references the live release's runtime
  snapshot runs, read from the same snapshot as the service count) and `recentRuns` (the newest
  fourteen, newest first). The strip and `lastRun` are one statement, `recentProjectRuns`: a
  `ROW_NUMBER()` ranking of every card's runs that carries only ids, answered in order by
  `idx_deploy_runs_project_requested (project_id, requested_at DESC, id DESC)`, joined back to the
  whole rows it keeps, so `recentRuns[0]` is `lastRun` by construction. Ranking whole rows in a second
  window was the fleet read's most expensive statement and pushed its `-race` p95 against the
  budget. A fleet read stays ten fixed statements, and eleven while anything is in flight (the step
  read below), beside the active-work list's own reads of each run and its project; the counting
  tests measure a fleet at rest. A run that
  has not ended carries `currentStep` — the step it is running, else the one blocking it, else a
  failed one, else the next one waiting, with the name the release path gives it (`Fetch source`,
  `Readiness checks`, `Switch traffic`) — on the fleet's active work, on a summary's `lastRun` and
  `activeRun`, and on `GET /deploy/{id}/runs?view=engine`, so a row can say where a run stands without
  loading its snapshot. It is one windowed statement over the runs in flight, where the active-work
  list used to ask once per run. `GET /deploy/?view=archived` returns each archived project's record
  with the facts its plan recorded beside it — `sourceKind`, `sourceRef`, `sourceRepository`,
  `buildMethod`, `recipe`, `framework` — read for every archived project in one statement
  (`ArchivedDeploymentFacts`), so the archive can draw each as what it deployed without a read per
  row; a legacy project with no production environment has none.
  `GET /deploy/{id}/runs?view=engine` additionally accepts `environment=<id>`, `operation=<op>`,
  `state=<state|terminal|active>` (a literal `RunState`, or the closed keywords for "any non-terminal
  state" and "any of the five terminal states"; anything else is `400`), `limit` (≤ 200) and a
  `before=<runId>` cursor; the response gains `nextBefore` only when another page remains, so the
  unfiltered default response stays byte-identical to before these were added, apart from
  `currentStep` on a run that has not ended, which every read of the list now carries.
- A blueprint deploys as an image release with reviewed defaults. `blueprint.DeploymentSupport`
  decides per definition: game profiles, install-time files (`write_file`), artifacts pinned by
  checksum and UDP ports all deploy ([game servers](game-servers.md)); an artifact found through a
  version source with no checksum stays preview-only with a specific reason, exposed as
  `deploymentSupported`/`unavailableReason` on the catalogue and detail responses and enforced by
  `DraftSourceConfig.ValidateForDeployment` at source save, preflight, commit, materialization, Git
  watching and queue admission (the stored source is re-validated, so a stale draft cannot enter the
  queue). Detection renders the definition and resolves its image through the registry exactly as an
  operator-typed image is resolved: the source identity carries the normalized image reference
  (`repository`), the reviewed `id@version` (`ref`), the render digest (`revision`) and the image digest
  (`digest`); a preview-only blueprint renders without a registry lookup and has no digest. Saving that
  detection stores the rendered `PlanConfiguration` on the draft — image, command, stop signal, memory
  and CPU from the definition's resources, managed `docker_volume` mounts named
  `<slug>-<hash>-<volume>`, command/HTTP checks with bounded retries, domains — and the configure form offers
  it for review instead of composing a default; the runtime and variable sections open for a blueprint.
  The template panel answers what it can before the press: a `domain` input arrives filled in with
  `GET /deploy/hostname`'s suggestion (the same `<slug>.<address>.sslip.io` the public-address field
  takes, and `Render` turns that input into the plan's own domain). Reviewed plain variables derived
  from that primary domain carry `domainTemplate` metadata using only `{{hostname}}` and `{{scheme}}`.
  Runtime-screen hostname and HTTPS edits update values still matching their old automatic value;
  explicit overrides remain unchanged. Removing publication clears matching automatic values; a
  required domain setting then needs a value before deployment. Editing a route retains its visitor
  password protection, and disabling public publication removes all aliases as well as the primary.
  A required input the panel cannot answer is marked and
  refused there rather than reaching `Render` — which used to refuse `"domain" is required` for the
  eight definitions that declare one, after a draft had already been created. A press that fails
  discards the draft it started, so a refused attempt is not an unfinished setup.
  Ordinary input values become plain variables through the additive `PlannedVariable.value` field
  (refused for secrets and for anything shaped like a reference). A `secret` input is deferred to the
  Variables screen as a required secret variable, never accepted in `blueprintInputs`; Mongo Express
  uses this for its MongoDB URI and offers the MongoDB connection sheet. Declared generated secrets become
  `PlannedVariable.generate` (16–128, secret only) with an optional `generateFormat` (`hex`, `base64`,
  `laravel`, `keylist`; empty is alphanumeric, and the length counts random bytes for the base64
  shapes), and commit produces each value from `crypto/rand` so it exists only sealed, revealed through
  the audited reveal route. A detected self-issued secret is declared the same way, and Generate or
  Rotate in the Variables settings keeps the shape the environment's recorded detection gives the
  name, so a rotated Laravel `APP_KEY` keeps its `base64:` prefix. Preflight accepts a
  literal, generated, referenced or encrypted draft value for a required variable, and a managed `docker_volume` that Docker has not
  created yet passes as `storage_pending_creation` (Docker creates it on first start); a linked or
  uninspectable volume still blocks. Default automation presets that need a backup job arrive paused so
  the operator links a job under Settings → Automation instead of a nightly `invalid_plan` failure. Preview
  volume names include a hash of the unnormalized name and blueprint id, Docker-socket mounts are linked
  and read-only, startup budgets use bounded retries, and Bedrock does not receive Java save commands.
  UDP is a runtime protocol only for a fixed, unrouted publication (`runtime.protocol: udp`, or
  `tcp+udp` for one number on both, needs `hostPort` and refuses a domain), never silently converted
  to TCP; a definition's direct primary
  port is published on loopback until the operator chooses every interface, and its install files
  and pinned artifacts become `runtime.seeds[]`, written into the managed volume by a helper before
  the container starts (`runtime_seeds.go`, [game servers](game-servers.md)).
  HTTP tool templates use the web profile so their default candidate-first strategy is eligible.
  Retired definitions cannot create new projects; existing release operations retain their compatibility
  contract. The template chooser keeps its detail column during loading, displays retryable failures,
  retains edits separately per template and ignores abandoned inspections. Below the desktop two-column
  breakpoint, selecting a template brings its settings above the catalogue and focuses them; Choose
  another template returns to the catalogue, respecting reduced motion. Successful inspection consumes
  the source/deploy-link query parameters so a reload returns to Configure. Git branch typing stays local
  until blur or Enter, and reinspection preserves explicit configuration overrides. Session snapshots
  omit raw Compose documents, detection previews and the serialized exact-plan preview.
  `scripts/e2e-deployments.py` deploys the PostgreSQL blueprint against a real backend and Docker daemon
  and checks the digest, rendered plan, container limit and volume, generated-password authentication
  over TCP (including rejection of an incorrect password), and paused backup automation without a policy.
- A definition's `image.command` (an argument vector, never a shell string, never templated) replaces the
  image's default arguments and renders as `Plan.Command`; MinIO needs `server /data --console-address
  :9001` and had none, so it printed its usage and never listened. A definition's readiness timeout is a
  startup budget and always becomes paced retries (every two seconds up to two minutes, every ten
  beyond), never ten instant attempts before the process has bound its port. A second `direct` port
  next to a routed primary (Gitea's SSH, Syncthing's sync protocol) becomes an additional
  `runtime.ports[]` publication on its own number (see published ports below) instead of silently
  replacing the routed port; the primary always stays the one the proxy and the checks reach. The
  catalogue ships 102 reviewed definitions, 97 of them offered (see "how the first sign-in works"
  below for the five that are retired; 35 are the multi-service templates of the next item). The
  three counts the competitive report quotes — built in, deployable, Compose — are constants in
  `blueprint/catalogue_counts.go`, and `catalogue_counts_test.go` fails when the shipped
  definitions disagree, so they are bumped deliberately in the change that adds a template. Every
  image reference resolved against its registry (`docker manifest inspect`) and every deployable one
  started live. An image whose registry publishes no release tag (Excalidraw and BentoPDF on Docker
  Hub ship only a moving `latest`) is pinned as `name:tag@sha256:…`, the content digest the
  validator already admits, so the definition still names exactly one build; a newer build is a new
  digest in the definition. The sweep: `TestLiveEveryBlueprintStartsAndAnswersItsOwnChecks` renders each single-image
  definition from its fixture inputs, pulls the image, starts it through the real runtime
  owner with generated secrets, runs the definition's own readiness checks against the published
  loopback port (Mongo Express with a catalogue MongoDB beside it) and removes what it pulled;
  `JD_BLUEPRINT_ONLY=a,b` narrows it. Applications that validate the `Host` header (Homepage) are
  configured to accept any, since the proxy in front only ever forwards the domain.
- **Multi-service templates** (`blueprint/compose_validate.go`, `compose_render.go`,
  `deploy/blueprint_source.go`). A definition that declares `services` (profile `compose`) is a
  Compose template: an application with the database, cache or worker it needs, deployed through the
  existing Compose release path rather than a runtime of its own, so backups, logs, requests, alerts,
  domains and rollback treat it as any Compose deployment. Each service declares its image, role
  (`app`, `worker`, `database`, `cache`), purpose, environment, ports, volumes, memory and CPU,
  a Docker healthcheck and its start order (`dependsOn` with Compose's `healthy`, `started` or
  `completed`); the top-level image, ports, volumes and resources stay empty. Environment values are
  literal text with two typed references: `{{secret.<name>}}`, a secret the definition generates, and
  `{{variable.<NAME>}}`, a plan variable an input writes or a startup operation sets. Both render as
  `${NAME}` interpolations (literal dollars are doubled), so the rendered document holds no value and
  is a function of the definition alone; a generated password two services share is one sealed
  variable, generated at commit and handed to each through the release's private `0600` env file.
  `Render` returns the document as `Plan.Compose` (inside the fixture digest) with `Plan.Services`,
  the public service's image and port, and the stack's summed memory. The document publishes exactly
  one port — the public service's proxy port, on `127.0.0.1` at a preference derived from the
  template id (`TemplateHostPort`, moved by Compose's own port recovery when taken) — puts the public
  service on Compose's `default` network and the private `internal` one, and every other service on
  `internal` only, which is declared `internal: true` (no route out; a private service that sends mail
  or downloads models declares `egress` and joins a separate `egress` network). The ingress joins the
  public container on the first of its networks by name (`proxysvc.publishedTarget`), and `default`
  sorts before `internal`, so the proxy facing the internet shares a network with the public service
  and nothing behind it. Every service gets `mem_limit` (and `cpus`) from its own resources, a CMD-form
  healthcheck and the capped `json-file` log every dashboard container gets. Validation refuses at
  catalogue load — that is, at build time — fewer than two or more than eight services, a template
  with no proxy port or two, a public service that is not an `app`, any `direct` or UDP port, the
  Docker socket, a database without a data volume, a shared volume described two ways, a literal for a
  credential-shaped variable (the Compose planner's `secretShapedKey` set and the single-image set
  together) or a literal password inside a URL, a reference to a secret or variable nothing declares,
  any other placeholder, a templated command or healthcheck, a healthcheck that is missing, untimed or
  prefixed `CMD`/`CMD-SHELL`, a dependency cycle, a generated secret no service references,
  privileges, configuration files, and install, release or stop operations. A secret's `alphabet`
  (`hex`) becomes `PlannedVariable.generateFormat`.
  A template deploys as `kind: compose, mode: blueprint` — `validModeForKind` allows the blueprint mode
  under the Compose kind, and `blueprintSourceKindMatches` refuses a stack saved as a blueprint source or
  an image saved as a Compose one (`BlueprintSourceKind` names the right one; the summary carries it as
  `sourceKind`). Saving the source — the draft's source step and the settings source route alike —
  stores the server's render in `composeFiles` (`WithTemplateDocuments`), replacing anything the
  browser sent, and `validateForNewDeployment` refuses a document that is not that render. From then on
  the stored document is the release: materialization writes it exactly as a pasted file, so a later
  dashboard that no longer ships the version still redeploys it byte for byte
  (`ValidateForDeployment` needs only the stored document), and moving to a newer version is a source
  change that renders it. Detection (`analyzeComposeTemplate`) runs the same structural analysis and
  Docker Compose validation as a pasted file over the render, and names the definition in
  `SourceIdentity.template` (`id@version`; the render digest is `revision`, the document's is `digest`),
  surfaced as `sourceTemplate` on the fleet summary and archived facts. The rendered configuration is a
  Compose build whose `primaryService` is the public service, `runtime.internalPort` its proxy port,
  stop-first, with the definition's variables, checks and domains; every variable the document
  interpolates is planned, so preflight has no `compose_variable_*` decision left. `GET
  /docker/templates` leaves stacks out. The template picker labels a stack on its card (`N services`),
  lists its services with role, image, limit and the public one marked, adds up the footprint, and
  states before the press what the stack does by itself: only the public service is reachable, the
  rest publish nothing on a network with no route out, each deploy stops the stack first, shared
  passwords are generated here, and a backup is a file copy of a running database rather than a dump.
  A template is not a Git source, so it is never offered a pull-request preview, though a Compose
  stack from a Git source is ([Compose previews](preview-isolation.md#compose-previews)). `TestLiveEveryComposeTemplateStartsAndAnswersItsOwnChecks` brings every stack up
  through the real build step and runtime owner and requires every check to pass, every service's
  healthcheck to report healthy, and only the public port to be published, on loopback.
- **How the first sign-in works**, declared rather than described. Every definition carries an
  `access` block: a closed `kind` — `setup` (the application asks the first visitor to create the
  account), `credentials` (a name from an input plus a password from a generated secret, both
  injected as environment before the container starts), `token` (one generated secret is the whole
  credential), `client` (nothing signs in through a browser; a program connects with the named
  secret), `open` (no authentication of its own) or `unavailable` — plus the sentence the operator
  reads. `validateAccess` refuses a `credentials` or `token` kind that does not name a declared
  generated secret, refuses a credential named on a `setup` or `open` kind, refuses a `client` kind
  with nothing to hand over, and allows `unavailable` only on a definition that also carries
  `retired`. That last pair is the whole point: an image whose first password exists only in its own
  container log cannot be offered. The block is catalogue metadata, not plan input — `Render` never
  reads it, so it carries no digest — and it reaches the picker on `Summary.access` (a word on every
  card, the full sentence beside the chosen one) and the project overview's First sign-in card, which
  reveals the username and password through the audited variable reveal route.
  `retired` is why a definition is no longer offered. A retired definition stays shipped, stays
  parsed and stays held to every rule, because `ValidateForDeployment` re-resolves the definition on
  every redeploy: deleting the file would turn a running service into an unredeployable one.
  `Catalog()` is the offered set, `All()` is everything. Retired in this pass: File Browser (upstream
  archived; first password only in the container log; `FB_PASSWORD` takes a hash), wallabag (its only
  account is `wallabag`/`wallabag` with no environment variable for either half), MinIO (upstream
  archived, no pullable newer release, and the console is not the routed port), Syncthing (no
  environment variable sets the web interface's password before it starts, and until one is set the
  interface is full control of the sync configuration) and Healthchecks (sign-up completes by
  clicking an emailed link, and one image has no mail server, so the sign-up page answers 500).
  Added in the same pass, each one image with its own embedded store, each one started live and each
  one admitted for a first sign-in this catalogue can state: Open WebUI (the interface Ollama shipped
  without), ntfy, Beszel, Opengist, Qdrant, NocoDB, DocuSeal, Seerr and SearXNG. Speedtest Tracker was
  rejected on a rule the catalogue cannot express: Laravel's `APP_KEY` must be exactly 32 bytes, and
  `RotateVariable` always hands back 43 characters of base64, so the dashboard's own Rotate button
  would stop the application from starting.
  Two more global refusals landed with it: an operation may not template an input that is optional
  with no default (`expand` substitutes the empty string, so Grafana's root URL rendered as
  `https:///`), and an `accept` input may not declare a `variable` (`Render` records the acceptance
  and moves on, so both Minecraft definitions' `EULA` never reached the container — it is a startup
  operation now).
- Published ports. `runtime.ports[]` (`hostPort`, `containerPort`, `protocol` tcp/udp, optional
  `bindAddress`, every interface when empty) publishes container ports beside the routed one for
  protocols a reverse proxy cannot carry. The fixed primary itself may be UDP, or TCP and UDP on
  one number (`runtime.protocol` `udp` or `tcp+udp`), published once per protocol on
  `runtime.bindAddress`, and a published port clashes with it only within the same protocol. Validation bounds each, refuses a repeated host port per
  protocol (including the fixed `hostPort`) and refuses them with host networking; preflight observes
  each as a port conflict; a plan with any is stop-first only (`validateRuntimeActivationStrategy` and the
  preflight strategy finding); previews drop them; the release comparison lists them; the runtime owner
  maps each in addition to the leased routed port. The Domains settings show them as **Also published**.
- Edge rules, rate limit and CrowdSec. `runtime.edgeRules[]` (ordered `{name, match, action}`;
  the first rule a request meets decides), `runtime.rateLimit` (`{requests, per, burst}` per client
  address) and `runtime.crowdSec` are the public route's, kept canonical by `canonicalConfiguration`
  and validated by `validateRouteRules` with the other route rules (`runtime.edgeRules`,
  `runtime.rateLimit` in a refusal); a tailnet-only environment refuses them. nginx renders maps and
  first-match returns, `limit_req` with 429 and Retry-After, and a CrowdSec `geo` list; Caddy renders
  named matchers each excluding the rules before it and the imported CrowdSec list, and has no
  rate-limit module, so its limit is the host's gateway limit the Domains page offers. The request
  record names the deciding rule (`er=` / `edge_rule`) and the requests summary counts each. See
  [edge rules](edge-rules.md).
- Password protection. `domains[].protection` (`username`, `password` on write, `hash` at rest) puts
  HTTP basic authentication in front of a deployment's public route without the application knowing.
  `sealDomainProtection` bcrypts the password once at the API boundary (configuration save and the draft
  configuration step; 8–72 characters, bcrypt's limit) so a stored plan, a release snapshot, a preview and
  the rendered route carry only what an htpasswd file holds; `Validate` refuses a plan that still carries a
  password, and the secret-shaped-key scan is why the JSON key is `hash`. Activation hands the proxy the
  union of the protected domains' credentials once per user: Caddy renders a `basic_auth` block, nginx an
  `auth_basic` with an htpasswd file beside the site written before the site and removed with it. The
  Domains settings and the public-address step offer **Ask visitors for a password**; the overview tags a
  protected domain. Previews inherit the plan's protection.
- Domain redirects and route rules. `domains[].redirectTo` names another of the plan's hostnames
  (`redirectCode` 301, 302, 307 or 308; empty is 308) that this one sends visitors to, with the path and
  query kept, instead of serving the application: the www. a bare domain keeps for old links, or the
  reverse. `validateDomainRedirects` refuses a redirect to itself, to a name the plan lacks, to or from a
  wildcard, or to a name that redirects too, and a plan in which no name serves. `runtime.allowFrom`
  (addresses and ranges, kept in `proxysvc.NormalizeAccessEntry`'s spelling, at most 64) is the only
  place the route answers, with 403 for everyone else ahead of any password; `runtime.responseHeaders`
  (at most 32) are set on every answer and replace the application's header of the same name. Headers are
  checked by the rules a Proxy site's are (`proxysvc.ValidateDeploymentHeaders`, on the nginx spelling a
  deployment gets), so quotes, `$`, `;`, braces and the connection's own headers are refused before either
  proxy sees them. The redirect is kept in the domain's dependency row only when present, and the two
  runtime fields only when non-empty, so a plan without them keeps its digest. Activation hands the proxy
  the redirecting names apart from the served ones (`DeploymentRoute.Redirects`); its certificate is
  resolved for every HTTPS name the route answers (`TLSHostnames`), and public-route checks ask only the
  served names, each on its own scheme. nginx writes each redirecting name a server of its own marked `# Host redirect (<code>): …`,
  which keeps the ACME path and which the site parser reads back; Caddy writes it a site block of its own
  with `redir`. The release comparison lists the redirect on the domain's line and the two runtime fields
  as their own. The Domains settings offer **Redirect to another domain** in each row's menu and an
  **Access and headers** form under the list, which writes only those two runtime fields and the sign-in
  below; the Runtime form neither holds nor writes them.
- Per-domain TLS and HSTS. Each `domains[].https` is honoured on its own: the route is TLS as soon as
  one domain is HTTPS (`DeploymentRoute.TLS`, `ForceHTTPS`), and the domains with HTTPS off are its
  `PlainHostnames`, served over plain HTTP with no certificate, no redirect to HTTPS and no HSTS, whatever
  the other names on the route do. Before this one HTTPS domain made every name on the route TLS with a
  forced redirect, and activation asked for a certificate covering the plain names too. Activation now
  resolves the certificate for `TLSHostnames` alone, and the certificate step was already issuing for the
  HTTPS domains alone. nginx writes the plain names a server of their own on port 80, after the site's
  own, marked `# Plain HTTP: <name> is served without TLS.` (one marker per name, read back by the site
  parser as `SiteSpec.PlainDomains`), and a redirecting plain name a server on port 80 alone marked
  `… over plain HTTP`; a redirecting HTTPS name on a route whose served names are all plain keeps its
  443 listen and is marked `… over HTTPS`, from which the parser reads the site's TLS and, from that
  skipped server, its certificate and HTTP/2. A redirect sends visitors on over the scheme its target is
  served on. Caddy writes the HTTPS names one site block and the plain names another with `http://`
  addresses, the same handling in each, because Caddy refuses a `tls` directive beside an `http://`
  address; a plain redirecting name is an `http://` block with `redir http://…`. The reconcile pass
  repairs `reverse_proxy` and `forward_auth` in every block. `runtime.hsts` (`maxAge` seconds, absent is
  six months, 300 to 63072000; `includeSubDomains`; `preload`, which needs a year and the subdomains —
  `proxysvc.ValidateDeploymentHSTS`, the bounds a Proxy site's HSTS control is held to; field
  `runtime.hsts`) sends `Strict-Transport-Security` on every HTTPS answer: nginx `add_header … always`
  in the TLS server with `proxy_hide_header Strict-Transport-Security` in every server, Caddy a line of
  the HTTPS block's deferred `header` block with `-Strict-Transport-Security` in the plain block, so the
  route's policy replaces the application's own and a plain name is sent none. It is off until chosen,
  because the policy outlives a mistake, and it is carried to a route only while the route is TLS.
  A custom response header named `Strict-Transport-Security` is refused with the control named.
  Preflight's `hsts` finding (`proxy`, `runtime.hsts`) says which served domains receive it and which plain
  ones do not, and warns when no served domain is HTTPS, since then nobody does. The release comparison
  lists `hsts` as the header's value or `off`. The Domains settings offer **HSTS** under Access and headers
  while a served domain is HTTPS, or while a policy is saved so it stays removable, with the lifetimes,
  subdomains and preload confirmation the Proxy site form has, and each served domain's row says whether
  plain HTTP is redirected to HTTPS or served as asked. Previews copy the policy with the other rules.
- Route sign-in. `runtime.forwardAuth` (`provider` authelia, authentik, oauth2-proxy or custom;
  `verify`, the auth server's check address; `signIn`, the page a visitor without a session is sent to;
  `previewsOnly`) has the public route ask an auth server about every request before the application
  sees it, the way a Proxy site's single sign-on does, and it is held to the same rules
  (`proxysvc.ValidForwardAuthProvider`, `ValidateForwardAuthCheck`, `ValidateForwardAuthSignIn`; fields
  `runtime.forwardAuth.provider|verify|signIn`). A 2xx lets the request through with the user handed on
  as `Remote-User` and `Remote-Email`, replacing whatever the visitor sent; a 401 sends the visitor to
  `signIn` with `rd=` set to the address they asked for; anything else, the server not answering
  included, refuses the request. It is the route's, like the address list: the allow list refuses first
  (403), then the sign-in, and redirecting names answer before either, since they serve nothing.
  `validateRouteSignIn` refuses, on a route the sign-in gates, a domain password (the browser would send
  it to the auth server as its own credentials; field `runtime.forwardAuth`) and a served name without
  HTTPS (the session cookie would cross networks in clear, and Authelia refuses to answer for one). A
  preview's plan is a copy of production's runtime, so previews sign in too; `previewsOnly` keeps this
  environment's own route open and gates only the previews (`RuntimePlanConfig.SignInGate`: the gate is
  on a plan marked `previewIsolation`, which a preview's always is). A preview's tailnet address is not
  a public route and is not gated. Activation (`publicRoute`) hands the proxy the release snapshot's gate
  as `DeploymentRoute.ForwardAuth`, so a rollback restores the sign-in its release had. nginx renders it as
  the site feature on every path; Caddy as `forward_auth` asking the same check address with the same
  headers ([Caddy ingress](caddy-ingress.md#sign-in)), so one address answers on either proxy. The field
  is kept only when set, so a plan without it keeps its digest; the release comparison lists it as
  **sign-in**. A `public_route` smoke check meets the sign-in redirect like any visitor, so on a gated
  route give it an expected status of 302 or accept any answer. The **Access and headers** form's
  **Require sign-in** offers the Proxy page's four presets (`lib/sso-providers.ts`), **Here and on
  previews** or **Previews only**, and names a password or plain-HTTP name the server would refuse before
  the save (`settings/route-access.ts`).
- Tailnet-only environments. `runtime.tailnet` publishes an environment on this host's tailnet and
  nowhere else, from the previews' own address range and table, with no public domain and no public
  certificate; `runtime.tailnet.allow` (Tailscale logins and tags) makes the front every tailnet address
  has a gate — nginx's `auth_request` or the dashboard's own loopback Caddy's `forward_auth` — that asks
  `GET /api/v1/tailnet/whois` about every request, which asks tailscaled who holds the address it came
  from. Previews and staging inherit the list. See [tailnet-only deployments](tailnet-apps.md).
- The dashboard's session cookie stays the dashboard's. It is `__Host-jd_session`, which a browser
  sends to no other name, so a preview domain or a deployment's own domain never sees it; a tailnet
  address's front and a deployment route on the dashboard's own name (`JD_SITE`) take it out of the
  request, since a browser sends a cookie to every port of its name
  ([the dashboard's session cookie](tailnet-apps.md#the-dashboards-session-cookie)).
- Maintenance and error pages. Maintenance is a switch beside the live route, not a plan field, so it
  applies at once and no release turns it on or off. Every route a release writes carries the check
  (`DeploymentRoute.Maintenance`, rendered on nginx as a Proxy site's maintenance behind
  `SiteMaintenance.Switch`, on Caddy as a `file` matcher), and the check looks on every request for
  `maintenance.on` in the route's pages folder: `<nginxDir>/jd-pages/<route>/` on nginx,
  `/config/just-dashboard/pages/<route without .conf>/` inside the Docker Caddy ingress. Turning it on
  writes that file and turning it off removes it (`proxysvc.SetDeploymentMaintenance`, under the
  service lock): nothing is reloaded and the route's bytes never change, so the switch never rewrites a
  route outside activation, never meets a cutover's snapshot, verify and restore, and survives the next
  release, a rollback and a cutover in the middle of which it was turned. Turning it on is refused
  (`ErrMaintenanceUnready`, 409 `no_public_route` or `maintenance_unready`) for an environment with no
  route and for a route written before the switch existed, which gets the check with its next
  deployment; turning it off is always allowed. While on, every visitor gets the maintenance page with a 503
  except the ACME challenge and the plan's `runtime.maintenance.bypassFrom` (addresses and ranges in
  `NormalizeAccessEntry`'s spelling, at most 64); `runtime.maintenance.retryAfter` (0–86400, 0 sends
  none) is the `Retry-After`. It is decided before the password and the sign-in and after the address
  list, on both proxies, so a refused address is still refused and nobody signs in to read the page.
  `runtime.errorPages` (404, 502, 503, 504) answer those codes with the route's own page, and
  `runtime.interceptErrors` gives the application's own answers with them the page too (refused with
  a sign-in, whose 401s it would replace). Those three plan fields are kept only when set, so a plan
  without them keeps its digest, and change with the next deployment like the rest of the plan; the
  release comparison lists them as **let past maintenance**, **maintenance retry after** and **error
  pages**. Activation writes the pages a route serves that it has no file for, from the ones Proxy
  ships, before the route, and route removal takes the folder, switch included. A page is edited at
  `GET`/`PUT /deploy/{id}/environments/{env}/pages/{page}` (`maintenance`, `404`, `502`, `503`, `504`;
  the write is `system.admin` in a session, at most 256 KiB, audited `deploy.page`) and served from the
  next answer. The switch is `GET`/`PUT /deploy/{id}/environments/{env}/maintenance` `{on}`, the write
  behind `s.destructive` as stopping a deployment and a Proxy site's maintenance are, audited
  `deploy.maintenance` with the outcome; a deploy key reaches the switch and reads a page
  (`jd maintenance APP on|off`, whose `--bypass` writes `runtime.maintenance.bypassFrom` through the
  configuration first), while a page's write stays a session's. Readiness and smoke checks dial the candidate directly and are
  untouched; a `public_route` check, which goes through the route and would read the maintenance page,
  is not run while the switch is on and records `maintenance` as its code, so a release can be deployed
  during a migration window. The Domains settings show the switch as its own **Maintenance** form,
  which the save bar applies immediately (the destructive capability), and **Maintenance page and error
  pages** under it for the plan fields and the page editor (`settings/route-pages.ts`).
- The [GitHub App](github-app.md): one App per dashboard through the manifest flow, installation tokens
  for private clones through a `github_app` credential per installation, triggers on `delivery: "app"`
  fed by the App's single webhook (`POST /api/v1/hooks/github-app`, routed by repository through the
  same `dispatchAutomationEvent` decision as the per-trigger hooks), statuses posted as the App where it
  is installed, and one edited-in-place comment per preview on its pull request.
- `GET /deploy/{id}/insights?days=N` (7–365, default 30) computes delivery figures from persisted runs
  only: release runs (`deploy`, `redeploy`, `force_build`, `rollback`, `promote`) that reached a terminal
  state, production's and its previews' (a staging environment's runs are read with `?environment=`),
  success rate over decided runs, median and p95 claim-to-finish duration of successful releases,
  successful releases per week, mean time from a failed release's end to the next successful one,
  the current failure streak, a per-day series with zero-filled days, and the five most common terminal
  codes. Restarts and scheduler markers are excluded. The Deployments tab renders it with each figure's
  basis and a window selector; nothing is estimated in the browser.
- Legacy compatibility logs redact stored environment values before persistent output, including secrets
  split across stdout/stderr writes. Error text passes through the same redaction. Generated `.env` files
  use private, random staging files and atomic replacement; dotenv escaping preserves literal dollars.
- `internal/blueprint` is the one workload catalogue. A blueprint is data parsed with unknown fields
  rejected, validated at package initialization, and rendered by a pure function with no clock,
  randomness, network or filesystem — so the same version and inputs always produce the same secret-free
  plan digest. Install/release/startup/stop steps come from a closed operation vocabulary with no shell
  escape. `validate.go` enforces the supply-chain policy as refusals: no default credential, no published
  database port, no stateful workload without persistence, no download without https plus a size limit and
  a checksum or declared version source, no uncontained path, no privilege without a stated reason, and a
  render fixture whose digest CI recomputes. The container form's starting points are rendered from the
  same catalogue through `GET /docker/templates`; there is no second template list.
- `internal/gameserver` holds the game-specific adapters. The console runs one command through the
  container's own client as separate argv and refuses anything carrying a shell metacharacter, newline or
  NUL *before* it looks at whether a container is running. Player identities come from the server's own
  reply; a server whose reply cannot be parsed reports "not supported" rather than an empty list. The
  settings editor writes only keys the blueprint declares and retains unrelated raw lines and comments.
  The console is RCON, which a definition declares as its `rcon` port: Bedrock has none, so its
  console and player list say so (`consoleReason`, `409 console_unsupported`) instead of failing.
  `JavaStatusHandshake` is the bounded Server List Ping the `game_handshake` check runs.
  Public reads include only declared editable properties, excluding RCON and plugin credentials. Parsing
  understands escaped separators, Unicode escapes and continued lines; duplicate declarations use the
  last value and edits update every duplicate. Changed values escape backslashes and numeric bounds
  enforce a zero minimum. Upstream versions come from a bounded cached adapter that reports
  unavailable or explicitly stale rather than substituting an unverified `latest`. A game server's data
  volume is named from the deployment, not the release, which is what makes rollback restore the previous
  server build against the same world.
- `deploy_credentials` rows are managed under `/deploy/credentials` (`system.admin`, session, every mutation
  audited): `GET` lists `{id, name, kind, target, username?, createdAt, updatedAt, lastUsedAt?, usedBy,
  usedByProjectIds?}` without the secret — `usedByProjectIds` names, sorted, the projects behind
  `usedBy`, read from the same join so the two can never describe different sets (a provider token's
  `reportsForProjectIds`, the projects whose GitLab or Gitea statuses it writes, is computed apart and is
  not `usedBy`; see [notifications](notifications.md#what-settings-shows)), and since `usedBy`
  counts environments a project with two on one credential is one id and two in the count; an
  archived project drops out of both. `POST`/`PUT` seal the secret with the same `auth.Sealer` the
  variables use and validate
  its shape per kind — a PEM private key for `git_ssh`, a bounded token for `git_bearer`/`provider_token`, a
  non-empty opaque value plus a required target host and username for `registry` (`registryAuth` already
  required both); an omitted `secret` on `PUT` keeps the sealed one. `DELETE` answers `409 credential_in_use`
  while any non-archived project's current desired source still resolves to it, the same "recreate it from
  the same form" reasoning the invariants already give a saved DNS-provider credential, and is behind
  `s.destructive` without a typed phrase for the same reason. `POST .../test` runs `git ls-remote` (a full
  URL, or an `owner/name` shorthand combined with the credential's own saved target) for the Git kinds or an
  authenticated manifest resolution of the image named in `repository` for `registry` — every kind needs
  something concrete to try, and the page's Test dialog asks for it — through the exact adapter isolation
  detection uses, and never returns the secret. `target`/`username`/`lastUsedAt` live in the row's existing `config_json`, so this
  needed no schema change; `lastUsedAt` is set by `OpenCredential`'s own callers after a real use, best-effort.
  `gitEnvironment` branches on kind: `git_bearer`/`provider_token` (and a minted App token) go out as an
  `http.<remote>.extraHeader` of HTTP Basic with `x-access-token` (`x-token-auth` on bitbucket.org) and the
  token as the password — the form a Git host's smart-HTTP endpoint actually accepts; `Authorization: Bearer`,
  the REST convention it used to send, is answered by GitHub with a username prompt, so every token clone
  failed while the API calls through the same token succeeded. `registryAuth` encodes `X-Registry-Auth` with
  padded `base64.URLEncoding`, which is what the daemon decodes with; the unpadded form was dropped silently
  whenever the JSON's length was not a multiple of three, and the registry saw an anonymous pull. `git_ssh` writes the
  sealed private key to a private 0600 file for the lifetime of one Git invocation and points
  `GIT_SSH_COMMAND` at it exclusively, with `-F /dev/null` and both known-hosts files pointed at `/dev/null`
  so the connection never reads or writes the operator's real `~/.ssh`; `SSH_AUTH_SOCK` was already stripped
  from this environment, so an agent identity cannot be tried either. A draft's source step and the
  source-change route below both refuse a `credentialId` that names no row before saving, rather than
  waiting for detection to discover it.
- `PUT /deploy/{id}/environments/{env}/source` (`system.admin`, session or deploy key, audited `deploy.source.update`)
  changes a committed project's source without touching the live release: the same `DraftSourceConfig` shape
  a draft's source step uses, plus the caller's current `revision`. The handler validates it with
  `ValidateForDeployment`, runs the exact read-only inspection a draft's detect step runs
  (`HostSourceAnalyzer.Analyze`, resolving the Git ref or the registry digest) so an unreachable branch or
  image is refused with the adapter's own message before anything is written, then `PlanningStore.
  SaveEnvironmentSource` writes a new `deploy_sources` revision and clones build/runtime forward unchanged —
  exactly as a configuration save clones the source row the other way. The source kind cannot change; build
  and runtime were built for the kind that is already live. Response is the refreshed
  `EnvironmentConfiguration` plus the saved `source` and the freshly resolved `identity`, since the
  configuration response alone carries no source field. Nothing else needed to change for this to work:
  `PendingState` already compares the live release's frozen source digest against the desired one, so a new
  source revision alone is what makes it report a `source` pending change, and the Git watcher already keys
  its cursor on the desired source's own config digest, so a changed URL/ref/subdirectory resets that cursor
  and the very next poll reports `branch_changed` on its own.
- `POST /deploy/{id}/duplicate` `{name}` (`system.admin`, session or a deploy key that reaches the deployment, the draft then
  being the key's own, audited `deploy.project.duplicate`) creates
  a draft pre-filled from the project's current desired source and configuration — the same draft the
  new-project page resumes with `?draft=`, landing at the `configuration` step with no `detection` yet, so
  the operator still runs detect/preflight/commit like any other draft. `PlanningStore.Duplicate` copies the
  source and the build/runtime/dependency/check rows verbatim and changes exactly three things: domains are
  dropped (a hostname belongs to one project), variables are flattened to name/sensitivity/scopes with every
  value cleared and `required` set (a secret's plaintext is never available to copy, and a reference names
  something scoped to the source project), and a managed Docker volume's literal name is re-derived with the
  same `<slug>-<hash>` shape a blueprint's own volumes already get (`blueprintVolumePrefix`, keyed on the new
  name and the old literal volume name so two mounts never collide) — committing the duplicate unchanged
  would otherwise hand it the source project's own live volume. A linked or observed dependency, and a
  bind-path mount, are left exactly as saved: nothing about their identity is owned by lifecycle re-derivation.
- Closed vocabularies, route capabilities/confirmations/audit actions, retention limits and error codes
  are contracts. Change one only with an ADR plus migration and exhaustive transition/route tests. The
  `promote` operation's record is the Decisions section of [staging and promotion](staging-promotion.md).

## Public deployment ingress

Docker Caddy public listeners are reused for automatic HTTPS and deployment routes. On a fresh host
with unclaimed TCP 80/443, the first deployment provisions a persisted public Caddy automatically.
The [ingress decision](caddy-ingress.md) specifies ownership, additive snapshot fields, certificate
evidence, lifecycle repair, supported layouts and verification. The dashboard remains private.

The certificate authority is Let's Encrypt unless `JD_ACME_DIRECTORY` names another ACME directory
(`proxysvc.ACMEDirectory`, read on each use): the staging endpoint for a rehearsal, or a private
authority such as Pebble or step-ca with `JD_ACME_CA_ROOT` naming the PEM bundle to trust — the roots
that sign its certificates and, if private too, the root behind the directory's own TLS listener. A
Caddy-managed route (the certificate preparation route and any route served from a `caddy-` import)
renders `tls { issuer acme { dir … trusted_roots … } }`, the bundle is copied to
`/config/just-dashboard/acme-root.pem` in the container before the route is written, the issued chain
is verified against those roots when it is read back, and the public-DNS precheck is skipped for a
private authority (public DNS says nothing about it; Let's Encrypt and its staging twin still require the
name to resolve here). Every certbot order the dashboard places — the Certificates page's, a run's
HTTP-01 and DNS-01 — adds `--server <directory>` for any directory but Let's Encrypt's production one
(certbot's own default, left unnamed so a `--dry-run` still goes to staging), checks for an account
under that directory, and skips the public-DNS check for a private authority; `CertbotCommand` hands
every certbot it runs the bundle as `REQUESTS_CA_BUNDLE`, which is how python-requests, and so certbot,
trusts a private listener. The
journey is proven end to end by `TestLiveDockerCaddyIssuesThroughAConfiguredACMEDirectory`: an isolated
Caddy on loopback ports and a Pebble that validates nothing (`PEBBLE_VA_ALWAYS_VALID=1`) on one Docker
network; the certificate is issued, verified against Pebble's freshly minted root, reused on the second
request, and served on a route whose chain verifies the same way — everything Let's Encrypt would do
differently is the validation Pebble skipped and the roots browsers trust.

A domain can instead be served through **Cloudflare Tunnel** (`domains[i].tunnel`): a host with no public
address or open port runs Cloudflare's connector, cloudflared, as the managed container
`just-dashboard-cloudflared`, connected from Proxy › Tunnel with a pasted token (sealed) or a tunnel
created with the Cloudflare API token saved for DNS-01. Such a name skips the public-IP DNS precheck and
the certificate step — Cloudflare terminates TLS — and the route serves it over plain HTTP on the
private network the connector shares with the ingress; activation sets the tunnel's ingress rules and
the name's proxied CNAME to `<tunnel>.cfargotunnel.com` before the public-route checks. The tunnel's
health is cloudflared's own (its `/ready` and its log), and the host alert kind **Tunnel down** tells the
channels when the connector loses the edge. See [Cloudflare Tunnel](cloudflare-tunnel.md).

## Deployment topology

```
browser ──(Tailscale / SSH tunnel)──▶ Caddy :8443
                                        ├─ /api/* ─▶ backend :8080  (loopback)
                                        └─ /*     ─▶ frontend :3000 (loopback)
```

Ports are variables — `JD_PORT` (8443), `JD_BACKEND_PORT`, `JD_FRONTEND_PORT` — read by
`docker-compose.yml` and `deploy/Caddyfile` from one `.env`. `install.sh` keeps the memorable default for
the one port a person types and walks it upward only if something already holds it; the two internal ports
are **picked at random from 20000–59999** and checked free, because their historical defaults (3000, 8080)
are the two most contested numbers on a Linux server and nothing outside this machine ever addresses them.
Installation, restart, rebuild and self-update share `internal/stackports`. Before startup it identifies
this checkout's running services using Compose labels and host process ownership, reserves distinct
available ports, and atomically updates only the three port values in `.env`. Existing dashboard-owned
ports stay put; foreign listeners stay running. Private `.env.jd-port-previous-*` backups preserve the
prior configuration. Startup waits for container health and retries a newly observed port conflict up to
three times. Health URLs and the dashboard endpoint follow the persisted choices. Settings changes use
the same free-port policy and preserve configuration rollback.

New container and Compose starts recover published-port conflicts automatically. Bind scope, protocols
and internal ports are preserved, and replacement host ports are concrete so stop/start keeps the same
addresses. Container creation returns actual published ports; deployment checks, proxy routes and fleet
summaries use the recorded runtime port. Compose stores only port mappings in `.just-dashboard-ports.yml`
(or next to the immutable release override), never resolved secrets. Port edits invalidate that override.
Port replacement uses Compose's `!override` support (Compose 2.24.4+). Host-network application ports and
ACME's public challenge ports cannot be moved by rewriting Docker publication rules.

**The frontend is the one service not on the host network**, which is what makes a taken port survivable
rather than silent. On the host namespace Next failed to bind, the container restart-looped, and Caddy's
catch-all forwarded to whatever already held 3000 — the operator got a stranger's application over the
dashboard's own certificate, with nothing in any log saying so. Published on loopback, Docker refuses
first, before anything serves. Inside the container the port is always 3000; only the host side varies,
because only the host side can collide.

Caddy is the only listener on anything but loopback and binds `{$JD_BIND}` (falling back to `{$JD_SITE}`)
**plus** loopback explicitly — site addresses alone would leave it listening on every interface. The bind
is separable from the site because a Tailscale install answers for a MagicDNS name whose certificate is
issued to that name while the socket must be opened on the tailnet IP: this container resolves names
through Docker's resolver, not the host's. `deploy/proxy-entrypoint.sh` derives the scheme and the `tls`
directive from `JD_TLS` — `tailscale` (a real certificate from `JD_DATA_DIR/certs`, mounted read-only and
renewed by the backend), `internal` (Caddy's CA, the source of the browser warning) or `off` (plain HTTP,
loopback only, which browsers treat as a secure context and therefore do not warn about). A Caddyfile
cannot branch, and generating one would put a tracked file in the way of every `git pull`. One origin for UI and API is
load-bearing: `SameSite=Strict` cookies, the mutation CSRF header and the WebSocket origin check all
depend on it. The frontend's `src/proxy.ts` creates a fresh CSP nonce per document and passes the policy
into Next so framework scripts and the pre-paint theme script receive it; no production policy grants
`script-src 'unsafe-inline'`. Caddy preserves that header and supplies a deny-all fallback for
non-document responses, plus Permissions-Policy. Caddy rewrites
`X-Forwarded-For` to the real client address (what makes `JD_TRUSTED_PROXIES` safe); `flush_interval -1`
and zero read/write timeouts keep the long-lived streams alive. The backend container runs `privileged`,
`pid: host`, `network_mode: host` with the Docker socket and real host paths mounted **at their real
names** — remove a mount and git discovery, compose scanning and the other path-taking features
silently operate on the container's own empty filesystem. The Files page alone does not depend on them:
it runs in the host's mount namespace.

## Deployment workspace

`/deploy` opens on the runs in progress as rows that open the run (it has no row of figures: each
card carries its own traffic and the counts are the chips), then the projects under a search field
and native state chips, ordered failed, unhealthy, deploying, ready with pending changes, ready,
stopped and not deployed. Automatic fleet attention rules and their list were removed in 0.7.5.
A card carries the project drawn as itself
(`ProjectMark`), its address, its source as its forge, repository and branch, its last commit, its
hour of traffic from `GET /deploy/traffic`, its last fourteen runs (`recentRuns`) and who started
the last one, and the verbs the project context row offers (`useProjectVerbs`). Every card draws
every part at one fixed height, so a row of cards is one height with nothing stretched: the second
source line is the commit, or for anything not built from a repository the images it runs (an
image project's first line then names its registry); the traffic band says "No web traffic" over a
flat rule for a project with no address on the web; and the footer never wraps, its words
truncating instead. The list view carries the same readings from 1024 in columns two lines tall — the
state over the address or pending changes and the run strip over the last run, with the traffic
joining from 1280 — so a row stays one row's height. The fleet is read every five seconds and the traffic
every thirty; the archive is read once, for the count beside its link, and again when a card
archives its project, because each archived row costs the server a history read.
`/deploy?view=archived` lists archived projects drawn as what they deployed (`ArchivedDeployment`),
with Restore (`POST /deploy/{id}/unarchive`) and permanent deletion. `/deploy/notifications` holds
the fleet-level channels: it reads the list, which carries each channel's recent history, every ten
seconds, and each channel's log (`?limit=200`) every thirty only for the two counts over a day.
`/deploy/credentials` holds saved credentials (Git tokens, SSH keys, registry logins, provider
tokens) with add, edit, test, and a removal that is refused while a project uses the credential,
and reads the fleet to draw the projects each one serves. Projects are also reachable from the
command palette.

The project pages under `/deploy/[id]` share one read of the project through a layout-level provider
(`detail`, runs, releases and the slower operational evidence, plus the environment's configuration
read once, the Git watch every fifteen seconds for a normalized Git source — one poll the header and
the Overview share — and a template's definition), so moving between a project's pages never
refetches the project, and a configuration save on any page is re-read there when the desired
revision moves. The shell draws the project identity line as one compact header at the top of
every project page, so the page's own content starts directly under it: the project drawn as
itself, its address with the certificate's state in the lock's colour, then its state — deploying,
ready, failed, unhealthy, stopped, not deployed, derived from the summary and the runtime
observation — and the diagnosis verdict or stage in flight on that same line, with the command at
the far end. One line of facts runs under the address: source, tracked branch, runtime (the
framework, else the language and version), the live release's age and automatic-deployment status.
Exact commits, authors and trigger details remain in the deployment history and run pages; the
Overview's wiring also names the live commit. The header has no back link and no Visit button —
the rail's panel leads back to Deployments and the address is the link to the site. Below `sm` the
state and the facts each take the full width under the mark, address and command. Its actions are
the one command (`projectCommand`: View deployment while a run
is active, else Start, Deploy, Deploy changes or Redeploy) and a verbs menu grouped Running,
Building and Project — Restart and Stop; Redeploy live release, Retry, Rebuild without cache and
Deploy a specific version; Duplicate project and Open in Docker — with Archive and Delete
permanently under the danger rule. Starting a run goes through `useProjectStart`, the fleet card's
own.

**The project's pages are on the sidebar, not in a strip above them.** `ProjectShell` publishes them
through `useNavScope` (`components/nav-scope.tsx`) and the rail draws a third level inside Deployments:
Overview, Deployments, Logs, Runtime, Console, Players and Server settings for a game server, then a
Settings group of the nine settings pages, each marked while it holds a saved change that is not live
yet (`PENDING_KIND_PAGE` maps a pending change's kind to its page). The panel's head is the project's
favicon or product and its name as written, and a run page keeps the project's panel with Deployments
marked current, because that is where a run is opened from. The strip of eight tabs and
the settings rail beside them are both gone; the lists themselves live in `components/nav.ts` as
`PROJECT_NAV` and `PROJECT_SETTINGS_NAV`, which is also what the rail draws from the route alone until
the project's read lands. Stop and Restart need the destructive capability, as the Docker container
verbs do, and each asks first, since either is one press from a fleet card's menu; the menu hides
them otherwise. Legacy `?tab=` links
redirect to these routes. Operation failures are titled by the operation and re-read the project at
once so the header and the Danger zone never disagree about a stopped service.

The Overview is, in order: the run in flight, as the runs list's own row with its stage and a light
round its edge (and confetti, once, if the reader watched it go live); the production block — the
site preview, the website laid out at desktop width (or a phone's, from a switch in its strip) and
shrunk into one tile that is a link to it, beside the way a request reaches the project (source
with automatic deployments, the live release with who deployed it, the runtime's containers and
health, domains with their certificates), each drawn as its product, over four readings (requests
a minute, the failing share, processor and memory, each carrying its last hour and each a way to
the page that has the rest); first sign-in, for a template that declares one; the delivery
figures; the recent deployments; then release history, whose columns share the split of the lists
above and whose single failure cause is a count instead of a full-width bar. The Overview keeps the
delivery figures compact; the Deployments page retains the release-time trend. From `2xl`, recent
deployments sit beside the preview environments. For a project deploying a github.com repository, that
**Pull requests** panel (`GET /deploy/{id}/pull-requests`, polled once a
minute; hidden when the read fails, since the panel is a window onto GitHub rather than a fact about the
project) listing the open requests with the preview built from each — its address as a link once it is
reachable, its state in the words `lib/pull-requests.ts` decides, "Out of date" when the head moved — then
the previews whose request is no longer open, with **Test this pull request** / **Update preview**
(held while the preview is Ready at this commit, Building, Closing or its cleanup failed, `previewHeld`),
**Merge** (its detail promises the preview's close only when one is open and a redeploy only when
auto-deploy is on, `mergeDetail`), **Close preview** and **Retry cleanup** (for a `preview_remove` run
that ended short of success, cancelled and superseded included) as the row's verbs
(`pull-request-verbs.tsx` for the Overview; the Git page's strip and workspace draw the same verbs under
the same readings from `lib/pull-requests.ts`) and a link to the checkout on the Git page when one is
under the roots. The header's verbs menu gains **Test a pull request…** for an administrator, which picks
from the open requests (`pull-request-picker.tsx`). A service with no
public address draws its product and where it answers inside Docker in the preview's place. Deploy
a specific version picks from the commits the project's runs recorded, with no remote call, and
Connect a database takes a saved connection by its row. `GET /deploy/{id}/preview-frame` is an
authenticated, non-cacheable static HTML wrapper with no scripts. Its CSP permits a child frame only
from the recorded endpoint's origin and allows the wrapper itself to be framed only by this dashboard.
This is a deliberate exception to the API's default frame denial; the dashboard document's CSP stays
unchanged. The embedded website is sandboxed without top navigation, popups, or downloads. No website
request is made by the backend for the preview and dashboard request headers are never forwarded to
it. The browser
applies its normal cookie and mixed-content policies. Sites that disallow embedding, require login,
or use an insecure URL under an HTTPS dashboard may require the direct website link. A preview is
not a deployment health check and does not bypass the site's own framing policy.

`GET /deploy/{id}/favicon` is the one request the backend makes to a deployed website, so every place
the dashboard draws a project can carry the site's own icon (the dashboard's image policy allows only
its own origin): the project's own pages, and wherever another section names one — a database's
readers and sessions, a site's application and upstreams, a port's owner, a backup's source and job,
the Overview's activity, the logs' request records, the command palette and a drawing's card picker.
Those payloads carry the project's id (`projectId` on a topology node, a backup resource, a deploy
event; `deployment.projectId` on a listener; `owner.projectId` on a site), and the frontend reads the
fleet once per page to learn whether the project has a website and a live release to ask about
(`useProjects`, `projectFavicon`). It reads the recorded endpoint's page for `<link rel="icon">` (then
`apple-touch-icon`), falling back to `/favicon.ico`, `/favicon.png` and `/apple-touch-icon.png`.
Every request stays on the recorded scheme and host, port included: a declared icon or a redirect
anywhere else is refused, not followed. It reads at most 512 KB of page and 1 MB of icon within five
seconds, accepts only a response that is an image, and remembers each project's answer for an hour
(an absence for ten minutes). The icon is served with `nosniff`, a sandboxing CSP and private
caching, so an SVG opened directly is a picture and not a document; `404 favicon_unavailable` means
`ProjectMark` draws the product the project is instead (`projectProduct`: its template, its image's
product, Compose, its framework, its recipe's language, Docker or nginx), and only a project no
product names keeps its workload glyph.

Deployments carries delivery insights as two charts over the same days: success rate and weekly
frequency over releases per day, and median release time and recovery time over release time per
day — a bar for each day a release succeeded, the window's median as a dashed rule and the slowest
day naming the scale's top. It is not drawn for a window in which nothing succeeded. These replace
the history page's four tiles; the Overview keeps its delivery tiles. Why releases failed runs under
both charts, counting the failed releases and when the last one was; each cause's bar is its share
of every failed release, and a single cause is a line with its count and no bar. A failure reason
narrows the list to the failed runs. The runs sit under a strip of the last twenty outcomes and the environment picker,
grouped under In progress and then by day, narrowed by counted, underlined status filters. Zero-count
filters remain selectable and show the empty state with Clear filters. Switching status or environment
reserves the results' current height, including pagination, for this page visit so a shorter list
cannot clamp the shell's scroll position and move the controls. History rows appear without replaying
the arrival stagger when a filter changes. A run is one shared row
(`run-row.tsx`): who started it as a face or a product, `#N Deploy` and the commit subject, then
branch (or the requested tag or commit) · sha · author · trigger, and its state, duration and time in
fixed columns, the duration with a bar that turns amber past twice the median of at least five
finished runs listed. A failed run's row names its cause's title rather than its step. Its verbs are
declared once in `run-verbs.tsx` and shared with the run page's header: Redeploy (live), Retry (only a
`failed` or `cancelled` run — and, once the settings moved on since it, preceded by Deploy with current
settings and relabelled "Retry with the settings it used", since Retry replays the run's own plan and
variable snapshots; the list reads the settings drift of its newest run when that run can be retried,
so a variable given another scope counts, and goes by the plan revision alone for older rows, as the
project header's and the fleet's Retry does), Cancel (never during activation or a rollback) and,
under the release's own name, What changed in this release (a comparison with
the release before it, with each linked database's data point), Compare with live (a sheet), Roll back
to this release (a retained release, through a two-step dialog that names the domains, draws the swap,
says what a rollback does not restore and offers, off by default, the database as of that release in a
side copy once the rollback is over), Restore data as of this release (a release with a restorable data
point, `system.admin`) and Pin or Unpin. A release's row carries its data point as a tag, and the
Data copies panel above the list holds the copies with their address, the comparison with the live
database, Keep 1 more hour, Restore in place (a link to the database's own restore) and Remove now
([data-aware rollback](data-aware-rollback.md)). Older runs load on request through the `before` cursor. Runtime draws the
managed services as cards of their image's product, joined with Docker's container list, its stats
every ten seconds and an hour of history, the volumes every five minutes (for sizes and which
container uses one), the saved connections and the policy's backup job with its runs — every join
failing silently — then the live usage, the domains, storage beside backups, the dependency evidence
and the recorded charts, each block saying in its header when its owner could not be read. Logs and
Console keep their contracts ([request observability](request-observability.md) has the Logs page);
Logs offers the activation window of the live run when the run's log handoff names one.

Settings are nine pages — General, Build, Runtime, Environment variables, Domains, Storage, Databases
& backups, Automation, Danger zone — drawn in one frame (`settings/setting-card.tsx`): what is saved
but not live yet at the top, as a strip that names each change and links to its page but carries no
command (the header's is the one), then, on the page that holds it, the newest failed deployment's
fix while that failure is still the news (`settings/last-failure.tsx`), then the page's readings,
then its forms, each head over its fields in one centred 48rem column. Each form saves the whole
configuration with the revision it read, keeps its draft keyed on a digest of its own saved value
(`useSettingDraft`) so a save of the form beside it no longer throws the draft away, and ends in a
foot with when the change applies, Discard and Save; a refusal that names a field lands on that
control (a refused row is marked in place), and one that names none lands on the form. General holds
the name, the Source (a repository, branch, root directory, credential — read through the App's
installation for a connected GitHub repository — submodule and LFS choice, or an image reference and
platform, changed in place through
`PUT …/environments/{env}/source` and checked before it is saved) and Automatic deployment: the Git
policy with a picture of what a push does, the last decision and the GitHub commit-status switch.
None of the settings pages opens on a row of figures; each section's head says what it currently
is. Build is the builder — every recipe, a Dockerfile or a static site
— the package manager, the commands, the image and the build variables as one form and the release
tasks, reorderable and
each with its last run, as another; choosing a builder that is not a recipe clears the recipe's
versions, secrets and package manager, which the plan would otherwise refuse; the Build and Image
heads name what the live release's own Build step prepared. Runtime is the runtime, where it
listens, resources (each limit against the live release's last-hour peak), the release strategy —
naming a blue/green plan the executor would refuse, before it is deployed — and container access as
one form, and the health checks, grouped by phase, as another. Environment variables is the list, its counts on
filter chips, each variable drawn as the service its name names and a reference as its database, with
Reveal, Copy value (the audited reveal route; the value is never drawn), Rotate and Remove, and a
removed name still live as a struck-through row; the editor is a sheet, and editing an existing
variable asks for its value again (an administrator can reveal the current one into it) so a scope
change can never blank a secret; the `.env` import is a sheet that previews every name through the
dry run before anything is written, and cannot import while any name is refused. Domains warns in
its section when the environment binds a public address, and checks a new hostname in its Add
domain sheet through `GET /deploy/hostname`, showing the A record to create and, for an
administrator, whether it already resolves here. The ownership selector shared with mounts and
volume dependencies shows only the chosen word in its button and plain option names in its menu.
The selected removal consequence stays beside it and is connected as its accessible description;
managed, linked and observed semantics are unchanged. Storage draws each host path as the Files page's
folder in its colour, in the mount editor and the live release's mounts as cards, each volume with
its size from Docker where that read is allowed, with a card to back up any volume nothing copies. Databases & backups links databases
through the same sheet the creation flow uses — in a section of its own, since linking is its own write
— and never commits a half-filled sibling row when a database is connected or removed. It draws each
link as a card carrying its engine, database, managed hostname, observed status, the variables that carry
it and the reason reconciliation recorded when it could not repair one, under a picture of how the
application reaches them. A saved database a literal variable names by address is a card of its own,
reached by address, rather than missing from a page that said nothing was linked. Because runtime activation attaches a database by reading the variable that
holds its address rather than the dependency row, removing a link offers to delete the variables that
reference it, and says so plainly when it is bound by a value it cannot name. The backup job a release
gates on is drawn as the Backups page draws it — its products, last run, destination, last fourteen
runs, next run and stored size — with the live release's observation folded onto the same card, runs
on demand, and warns when it takes no native dump of a linked database — which the gate would
otherwise refuse mid-deployment — with one press to add the dump; Automation opens on a picture of
what deploys the project, and holds webhooks (each with its deliveries in a sheet,
the hook URL shown absolute, and a signed hook's secret shown once when it is made and when it is
rotated), schedules (a builder in the schedule's own time zone, the server's check before saving,
and each schedule's past firings and next five runs), previews (approve with a fork warning, reject,
variables, deploy; a preview's row carries the address the engine published for it — a `tailnet` tag and
the `https://<node>.<tailnet>.ts.net:<port>` link once it answers, the bare host until then — and a
`production variables` tag while production's variables are copied into it) and traffic alerts (the rule form in a sheet it shares with the Logs page,
removal confirmed), every block reading one set of polls (`useAutomation`); Danger zone holds Stop
or Start, Archive or Restore, managed-resource removal — whose plan loads as soon as the project is
archived — and permanent deletion, which asks for the project's name.

The deployment page (`/deploy/[id]/runs/[run]`) keeps the sequence-based stream, resync and the
5,000-event cap. It opens on the header every page of its project opens on, saying what the run is
(`run-header.tsx`): the project's tile, the commit as the title, the run's state with the step at work
and how long it has taken, the verbs that act on it at the far end (Cancel, Retry, Redeploy, Visit and
the release's menu), and one line of provenance — the run's number, branch and commit with their forge,
the commit's author, what the run did and who or what started it, the environment, when, and how long it
queued. Then the release path (a stage the run did not include drawn dashed), and how the run ended: a
failure (`run-outcome.tsx`) drawn on the tile of the step it stopped at, in words with the engine's
code beside it, where in the run it stopped, the engine's reason, the last four lines the step wrote and
what to do — the fix, Show in build logs (Show the line when the cause names one) and Step details —
the live address, or which release is live now. A failure is titled from the cause
the failed step recorded (`failure-cause.ts` reads a build's or release task's `cause` and a health
gate's `diagnostics.cause`), lists the identifiers it named, and offers its fix — to an administrator —
as a button that opens the field it targets: the Build section for a package manager or version, the
commands for a command, the output or the root, Runtime for the port or memory, Databases for a
localhost database, and Variables with the editor opened on the variable (`?variable=NAME&scope=…`,
plus `&value=…` for a computed flag) with the scope it lacked. "Show the line" narrows the console to
the failing step, brings it into view and scrolls it to the transcript line the cause points at,
clearing a search or the errors filter that could hide it. When
`GET /deploy/{id}/runs/{run}/settings-drift` says the plan or
the variables changed since the run — the plan revision's fields and the variables' names, digests
and scopes, never a value — the header's command becomes Deploy with current settings (the run's own
commit for a remote Git source whose drift names no source change, a plain deploy otherwise — a source
moved to another branch or repository is never asked for the old commit), Retry moves to the menu as
"Retry with the settings it used", and one line under the failure says what changed. A run that failed
with `source_revision_unavailable` offers Deploy the branch head the same way, since its commit cannot
be fetched again. Under a run that failed or rolled back, *Ask why it failed* (`run-diagnosis.tsx`)
asks the assistant's provider about it — never unasked, and quieter than the failure, whose own fix
stays the command. Before the press one line says which model, where the question goes and what of the
run, with secret values masked; an administrator whose assistant is off sets it up there in the
assistant's settings sheet. The answer is the likely cause, the transcript lines it quoted (each a line
the run wrote, opening the build logs at it), what to try, who asked and when, and *What was sent* folded
under it with the exact text; *Ask again* replaces it. An account without `service.control` sees an answer
somebody asked for, and nothing else. Under that the run is two parts with no strip between them. The build console paints
its lines through the painter the dashboard's own transcripts use (`components/transcript-line.tsx`),
numbers them, strips terminal escapes, groups each step's lines under a sticky rule, filters by stage
and errors (with a count), shows the time since the run began, wraps, follows, copies and downloads
`deployment-N.log`; the release path shows each group's duration. Details (`run-steps.tsx`) is one
working surface: a rail of every step attempt under the stage it belongs to — each step on the tile of
the product it works with (the forge for the source, the toolchain for the build context, Docker for the
build and the runtime, Let's Encrypt for a certificate, a glyph for the dashboard's own bookkeeping),
what it concluded, how long it took and where in the run it ran — beside an inspector of the picked
step, which opens on the failed step, else the one at work, else the build. The inspector draws the
step's state and times, its error, preflight's findings for Check plan, the last eight lines it wrote
with Build output into the console, and its evidence read by shape (`run-evidence.tsx`): facts with
the products they name, digests cut short with a copy, a commit as the Git page draws one, images and
health checks as rows, a Dockerfile, a command and any record nested past two levels as code in the
`--tag-*` hues, and the record as it was kept in a fold. Runtime logs and Metrics around activation
were views of this page until 2026-10-05; what a release does once it runs is the project's Logs and
Runtime pages. A successful run shows Visit
only when its recorded release is the current live release of the run's own environment; the success
block waits for the project read so it never flashes Superseded first. A pull request preview is an
environment of its own, so its run is judged against that preview's `liveReleaseId` and visited at its
address while it is published (`previewURL`, `lib/pull-requests.ts`), its identity line names the
environment `PR <n>`, and it offers no Redeploy or Start, which act on production's terms — the pull
request is tested again instead. The Deployments list reads a preview run's Live state and Visit the same
way.

Archived deployments are searchable at `/deploy?view=archived`, each drawn as what it deployed. They
offer Restore, which says that automatic deployments and schedules stay off, and a separate permanent
record deletion that asks for the project's name and lists what is deleted and what stays on the
server, preserving host resources and the audit log. See the
[permanent-deletion decision](permanent-deletion.md) for the API and transaction contract.
