# Deployment database connections

Deployment setup stores `${{database.ID}}` in the selected runtime variable and links the saved
connection as a dependency. Execution resolves that identity through Databases using the current
encrypted credentials.

The configure step writes the reference into the variable's own row (`onConnectDatabase`), so it
reaches the draft as a supplied value, and supplied values are committed literal: a pasted or captured
value that only reads like a reference stays the text it is. A supplied value that is the whole
reference to a connection the same plan links — `${{database.5}}`, `.jdbc`, `.pooled`, `.url.app_cache`,
by id — is the link, and `variableModes` (`linkedDatabaseReference`) commits it as a reference. The
deploy-key commit guard reads the same modes, so a key's draft is refused it as before. Settings →
Databases writes its link as the variable's `reference` (`databaseLinkWrite`), never as a value: a
`PUT …/variables/NAME` with a value is literal by definition (`variableWriteMode`).

From 0.7.2 until this was fixed both pages wrote the link as text: the application received the
characters `${{database.5}}` and, with no `db-5.jd.internal` host among its variables, no network to
the database, and a start command that migrates first exited before the server listened. As the server
starts, before the engine claims a run, `repairLinkedDatabaseVariables` gives those links back, but only
where a person provably made them, since a deploy key may never point a variable at a saved database
and could have written the same text and dependency itself:

- a project made on the new-deployment page (`RepairLinkedDatabaseLiterals`): its committing draft is
  a person's (no key), staged exactly that value for that name and linked that connection, and the
  variable's active revision is still commit's literal first one;
- a link made from Settings (`LinkedDatabaseLiterals`, `personWroteVariable`): the audit trail holds a
  `deploy.variable.update` of that variable in that environment by a session, hub session or the
  assistant, with no key, as the revision was written, and none by a key then.

In both the environment must still link the connection and the value must be its whole reference. The
rewrite is a new revision with the same value as a reference (`created_by` `migration`), recorded as
`deploy.variable.repair` by the system; the next deploy resolves it, while a retry replays the failed
run's own snapshot. Anything the repair cannot prove stays literal until it is linked again. The explicit admin URL read remains audited and non-cacheable. It is read-only:
it does not create a network, publish a port, or change the saved host DSN.

`GET /databases/{id}/url` takes `format` (`url`, `jdbc`, `jdbc-mariadb`, `adonet`, `mysql2`) and
`database` (another database on the same server) for an application that parses JDBC (credentials as
query parameters; `jdbc-mariadb` writes MariaDB Connector/J's own `jdbc:mariadb://` scheme, since quick
setup's MariaDB records a `mysql://` address), ADO.NET keywords or Rails' `mysql2://`, and for Rails 8's
cache, queue and cable databases. The typed
reference records both after the numeric id — `${{database.5.jdbc}}`, `${{database.5.url.app_cache}}`
— and the release resolves the same shape from the container URL. Only a numeric id takes a suffix, so
a connection name containing dots is never misread. Relinking a variable from Settings → Databases
keeps the shape its previous reference asked for. Quick setup also offers PostgreSQL 16 with pgvector
(`pgvector/pgvector:pg16`) or PostGIS (`postgis/postgis:16-3.5-alpine`, offered and accepted only on
x86-64, the one architecture that image is published for), which Databases already recognises, and
refuses MongoDB 7 on a CPU without AVX or ARMv8.2 atomics before pulling it. On Linux 6.19 up to 7.0.13
it starts MongoDB 8 with `GLIBC_TUNABLES=glibc.pthread.rseq=1`, without which it does not start there
(MongoDB SERVER-121912; *MongoDB 8 and the kernel* in
[Databases](../backend/databases.md)).

A PostgreSQL connection that has a [connection pooler](../backend/databases.md) also has
`${{database.5.pooled}}` (and `5.pooled.jdbc`, `5.pooled.url.app_cache`, …): the same string with the host
`db-5-pool.jd.internal` and port 6432. The pooler joins the environment's network in the same pass that joins
the database, under that name, and the pooled reference fails with a sentence of its own when the connection
has no pooler. A pooler that cannot be joined to the environment's network (its name is held by another
container, say) is recorded once on the audit trail as `deploy.database.pooler.attach` and does not stop
`${{database.5}}` from binding; a variable that reads the pooled name is refused instead. Relinking a variable never carries `.pooled` over to another database.

Creation stages entered variables in the draft's encrypted environment column before preflight and
commits them with the initial project transaction. A required connection can therefore be satisfied by
a managed database reference or an external provider URL before the first run. The browser receives
only staged variable names when resuming, never saved values. Closing the database sheet cancels a
pending connection read; a delayed reply cannot add a database to an abandoned form. Standalone
provisioning remembers an already-created container across source-tab changes and resumes its setup.

For a saved loopback connection backed by a recognized running Docker database, the container URL uses
`db-ID.jd.internal` and the engine's internal port. Discovery requires the exact observed loopback
address and published port; ambiguous localhost bindings and unsupported network namespaces fail.
Host-network plans retain the host DSN. External database URLs retain their existing hosts and options.
SQLite and ambiguous MySQL driver-only URL options retain their existing explicit refusals.

## Builds that read their database

A build that reads its database — a page Next.js prerenders, `generateStaticParams`, a Nuxt or Astro
route generated at build time, Rails or Django loading models — needs the database the application
has. Docker's own builder cannot join a user-defined network ("network mode not supported by
buildkit"), and `--network=host` would hand repository build code the server's loopback services, so
until 0.9.1 such a build failed with `getaddrinfo ENOTFOUND db-N.jd.internal`. Every variable is build
and runtime scoped by default, so this was most Next.js projects with a linked database.

**The networked builder** (`deploy/build_database.go`, `dockerx/buildkit_network.go`). When a build
value's host is one of the dashboard's database names — `db-N.jd.internal`, its pooler, a preview's
branch — build_artifact asks `NetworksForRuntime` for the networks those values need, with the same
bindings and refusals a release gets (staging isolation, data copies), and runs the build, whatever its
method, on a builder of the environment's own, named after its database network
(`jd-build-e<environment>-db-<key>`, so another store on the same daemon never names it): buildx's `docker-container`
driver with `moby/buildkit` pinned by digest (`dockerx.NetworkedBuilderImage`), created on the first
network and joined to any other. Its steps share the builder container's network namespace
(`--oci-worker-net=host`, which is the container's, never the server's), and its configuration names
Docker's embedded resolver `127.0.0.11`, which BuildKit otherwise drops from a step's `resolv.conf`; so
a step resolves `db-N.jd.internal` as the application does and reaches the internet through the same
resolver. The step log limits are lifted to match Docker's builder. The builder is created for the
environment's first such build and, once a build is done, waits `dockerx.NetworkedBuilderIdle` (15
minutes) for the next one, which starts on it at once — 0.3 s against 5 to 30 to create it — and is then
removed (`buildx rm --keep-state`); its state volume (`buildx_buildkit_<builder>0_state`) keeps the cache
either way. While it waits it runs no build and listens on no port of the network (the docker CLI reaches
it through the container). The next build that starts on it first checks it still runs and matches its
networks to that build's — joining what it may reach now and leaving what it may not, so a database
unlinked meanwhile is out of reach — and a builder that fails that check, or one a stopped dashboard left
running, is removed and created again. Builds of one environment take turns on it; two environments never
share one, so one environment's build cannot reach another's database. An environment's first build on it
fills its own cache. It pulls base images itself, as BuildKit does, so
the daemon's registry mirrors (`daemon.json`) do not apply to it; registry sign-ins do, through the
docker CLI's own configuration. A build whose values name no such host stays on Docker's builder.

**The schema before the build.** A recipe build that fails reading a table or column the database does
not have yet (`build_database_schema_missing`) gets the plan's own migration applied and is built again,
once: the start command's leading migration step, or an image release task that applies one, never a
step that migrates from what the build writes (Adonis's `build/ace.js`, Medusa's `.medusa/server`). The
step runs from the build's own layers: `.just-dashboard/Dockerfile.schema` is the recipe's Dockerfile
cut at the build command's `RUN`, with the migration in its place, `ARG JD_SCHEMA_RUN` before it so it is
never served from the cache, and the runtime values (with a release task's own `release_task` values)
mounted as BuildKit secrets; it runs cache-only on the same builder, so every instruction before it is
the cache the build just filled. It is handed the build's own guard arguments (`JD_BUILD_VALUES_<n>`,
[build values](recipes.md#build-values)) at the digests the build gave them, those its Dockerfile still
declares, since a guard without its value would make the install and every step after it run again. A plan with a backup gate has the gate checked first. An environment
that rehearses its migrations on a copy keeps that order instead — the rehearsal needs the built image
— and the build's failure stands, naming what to change.

**The seed of a new database.** A recipe build that then fails for want of rows —
`build_static_params_empty` (Next.js with Cache Components refuses a `generateStaticParams` that
returns none) or `build_prerender_failed` — gets the project's detected seed (`seedCommand`) the same
way, once, only when every linked database the build reaches holds no row of the application's
(`DatabaseHasRows`, `api/deployment_database_contents.go`: PostgreSQL and MySQL tables, leaving out the
migration tools' records, Django's content types and permissions and PostGIS's reference systems; any
other engine, more than 500 tables, or an error counts as holding rows). A seed usually resets the
tables it fills, so a database with any row is never seeded. The step's log says each of these as it
happens, and build_artifact's evidence lists them (`databaseSteps`). A step that fails ends the build as
`build_schema_step_failed` or `build_seed_step_failed` with the command's last line; a refusal to join
the network ends it before anything builds, as `build_database_network`.

Removing an environment's networks (`removeRuntimeNetworks`) removes its builder, a waiting one or one a
stopped dashboard left on the network included, and its cache first.

## Runtime ownership and replacement

The API's `deploymentDatabaseNetworks` adapter owns the join between Databases, Deployments and Docker.
Before a candidate starts, it creates or validates one bridge per environment, records its Docker ID
and private ownership token, and attaches the database using the logical DNS alias. Application
containers join before startup. Compose receives the external network in its immutable override;
normalization preserves existing service networks, aliases and addresses. A Compose `network_mode`
cannot also join this network and is refused. Generated override files use root-relative filesystem
operations and atomic publication so repository symlinks cannot redirect writes outside the checkout.

Network labels, name, environment and driver must match before adoption or mutation. The bridge does
not change host bindings: a database quick setup provisions is published to loopback only, and a
database the operator opened or created as reachable from anywhere keeps that binding. This is environment
network separation, not a sandbox against an administrator or arbitrary outbound network access.
A preview's [database branch](preview-isolation.md#database-branches) — its own copy of a linked database,
restored from the newest backup — is the one database a preview environment's network holds that is not
bound like the rest: it joins the preview's network when it is made, under
`db-<connection>-branch.jd.internal`, a variable naming that host joins the preview to the network without a
binding (`previewBranchConnectionIDs`), and `${{database.N…}}` in the preview resolves to it. Previews and
staging environments cannot share a linked database endpoint/container with another
environment, including another preview or a different saved connection ID: a staging release task would
otherwise migrate production's schema from code that was never promoted (`staging_isolation_required`;
[staging and promotion](staging-promotion.md)). Reference resolution checks known links and bindings before
returning build/runtime credentials; runtime joins also check observed container identity. This does not
discover arbitrary untracked services, DNS aliases or literal credentials. Preview approval still clears
inherited credentials and dependencies.

Every five seconds, reconciliation observes each retained binding. A replacement must match the saved
container name, or the same Compose project and service; matching a reused port alone is insufficient.
It reattaches the alias, repairs missing owned application attachments and records status. A still-running
old database with the alias or an unrelated alias owner blocks repair. Attach/detach/network mutations
are audited without connection strings. Cancellation stops reconciliation with the server.

`GET /deploy/{project}/environments/{environment}/database-links` reports the connection identity, its
engine and database, hostname, network and last observation, scoped to that environment. A project's
Settings → Databases page shows these observations (`components/deploy/settings/databases.tsx`; the
Runtime and Variables pages read the same route), and each linked connection there opens its database's
own home, `/databases/<id>`. From the other side, the database's home draws what reads it
(`GET /databases/{id}/consumers`) and `/databases/map` draws every such link in each database's
diagram (`GET /databases/topology`, joined with the server map's reading).
Connected means the network binding was observed; it does not prove database schema or application
health. Observations older than 30 seconds are marked stale. The database name is read from the sealed
DSN the way every other database route reads it; a connection whose DSN no longer resolves keeps its row
without that name.

Reconciliation joins the environment's application containers — those labelled with its id — and
leaves every container carrying the rehearsal label where it is: a migration rehearsal's copy and task
belong on their own internal network, and a [copy of a release's data](data-aware-rollback.md) joined
this network when it was made, under `db-<connection>-copy-<copy>.jd.internal`, never the production
database's name. A variable whose host is such a copy's is refused before a release starts: the copy is
removed when it expires, and production never runs against it.

A binding that reconciliation could not repair carries `detail`, the reason of the pass that failed —
a replaced container that no longer matches the saved identity, an alias another container holds, a
namespace that cannot join a bridge. It is bounded to 300 characters, holds no connection string, and
is the same sentence the routes performing these operations already return. A connected or stale
binding reports no reason: staleness is the age of the last pass, not a failure. The additive
`deploy_database_bindings.detail` column defaults to empty for existing installs.

`GET /deploy/{project}/environments/{environment}/database-addresses` reports the saved databases that
the environment's literal variables name by address, with the names of the variables that hold it: an
application handed `postgres://…@<this server's address>:5435/app` at creation uses the container the
dashboard dials at `127.0.0.1:5435`, has no dependency row and no binding, and Settings → Databases said
no database was linked. The variables are decrypted and matched on the server with the address book the
database map reads container environments with (`topoAddresses`, `naming`), and only the connection's
identity and the variable names leave it. The page draws each one not already linked as a card reached
by address — its engine, database and the variables holding it, Test connection, and its database's home
— with no verb that rewrites the variable; Add database links it under the same name.

## Removal and existing installs

The additive `deploy_database_networks` and `deploy_database_bindings` tables retain ownership across
dashboard restarts. Bindings remain for retained releases and rollback until explicit resource removal.
Removing a variable alone does not detach a database from existing releases.

Configuration's removal preview includes the current managed network ID. Applications/Compose stacks
are removed before their network, and so are the environment's data copies and a preview's database
branches (`removeEnvironmentBranches`), which are endpoints on it that are no application's. Cleanup refuses unknown endpoints, disconnects only bound database
endpoints and leaves the database container and its data intact. Preview close uses the same cleanup.
Removing a stale network ID cannot delete a newer replacement. Successful cleanup removes bindings and
clears the Docker ID while preserving the logical network name for subsequent deployment.

Forgetting a bound connection, or dropping its own database, returns `database_linked` before changing
data. Remove its deployment network first. Permanent project deletion remains records-only; it forgets
these bindings and stops reconciliation while leaving Docker resources intact. Use resource removal
before permanent deletion when cleanup is intended.

Existing literal IP values are not rewritten; Settings → Databases reports the saved database one names.
Reconnect the database once in deployment setup/settings to save its typed reference. Saved non-loopback IP connections, remote servers and service URLs do not
receive automatic container discovery. Application drivers must reconnect and resolve DNS after a
database interruption; this feature does not preserve an existing TCP connection through replacement.

External providers remain ordinary saved connections or encrypted application variables: provider
credentials, TLS options and hostnames are preserved. For Supabase, choose the connection URL suited to
the server's network; its direct endpoint generally uses IPv6, while the shared session pooler supports
IPv4. See [Supabase's connection guide](https://supabase.com/docs/guides/database/connecting-to-postgres).
No hosted-provider account or live provider credentials are required by the local test fixtures.

## Verification

- `build_database_test.go` covers the schema Dockerfile cut, which migration the plan runs (and that
  one migrating from the build's output is not run first), which hosts send a build to the network,
  the new signatures from each driver and Next.js, and remedies that follow Cache Components;
  `build_database_executor_test.go` runs build_artifact through a first deploy (network, schema, seed,
  build), a database with rows never seeded, a build off the network, and each failure.
  `buildkit_network_test.go` covers the builder's arguments and configuration, the networks a waiting
  builder joins and leaves, and that one is claimed once; `JD_DEPLOY_LIVE=1 go test ./internal/dockerx -run
  TestLiveNetworkedBuilderWaitsForTheNextBuild` closes a builder on two networks, starts the next build on
  it with one, checks it left the other, and removes it.
- `JD_DEPLOY_LIVE=1 go test ./internal/deploy -run 'TestLiveBuildReachesItsDatabase|TestLiveBuildStepBuildsAnApplication'`
  builds, with the real recipe and BuildKit, a package whose build reads a PostgreSQL that only its own
  network reaches under a `db-N.jd.internal` alias: the build reaches it, gets the migration and then
  the seed applied from its own layers, and bakes the row into the image; the closed builder waits on the
  network until the environment's builder is removed, which leaves no endpoint. `JD_BUILD_DATABASE_LIVE_REPO` names a real checkout to build the same way.
  `JD_DEPLOY_LIVE=1 go test ./internal/api -run TestLiveDatabaseContentsSaysWhenADatabaseIsNew` counts
  rows in a provisioned PostgreSQL and MySQL.

- `linked_database_reference_test.go` saves a draft the way the configure step does (the row in the
  dotenv, the database as a dependency), commits it and checks the variable is a reference that
  resolves, among the draft's references; that the same value without its link stays literal; and that
  the repair gives a committed literal link back once and leaves a key's draft, a link removed since and
  a value set since alone. `deployment_database_repair_test.go` repairs a Settings link a person's
  session wrote once and leaves one a deploy key wrote, or one with no audit entry, literal;
  `deploy-settings-b.spec.ts` checks the page sends the link as `reference` and, removing it, names
  the variable that carries it and deletes it with the link.
- Component/API tests cover reference parsing, replacement identity, scoped status, stale observations,
  the reported engine and recorded reconciliation reason with its bound, safe removal plans,
  linked-database deletion, early preview credential refusal and generated-file containment.
- `JD_DEPLOY_LIVE=1 go test ./internal/api -run TestLiveDeploymentDatabaseConnection -count=1 -v`
  provisions PostgreSQL, MySQL, MariaDB, Redis and MongoDB, authenticates from a separate client container,
  refuses a preview's production credential/network reference before allocation,
  replaces each database with the original persistent volume and a different IP, reconciles, then
  restarts the same client with its unchanged URL and authenticates again. It checks loopback publication,
  cleanup ordering and refusal to adopt a foreign network. This establishes DNS reconnection, not a
  continuous application traffic test. Each engine's image is pulled, and the pull timed in the log,
  before anything is created; the engine then has two minutes from its container starting to accept a
  sign-in, and a failure reports the adopt route's last reply with the container's state and last log
  lines. The MongoDB subtest checks that provisioning gave MongoDB 8 what it needs on Linux 6.19 to
  7.0.13 (MongoDB SERVER-121912) and nothing on any other kernel, and fails at once, naming the kernel,
  the line and the container's `GLIBC_TUNABLES`, if MongoDB still refuses; `mongodb-kernel-mitigation`
  applies the mitigation from the Databases page to a server made without it.
- `JD_DEPLOY_LIVE=1 go test ./internal/dockerx -run TestLiveComposeDatabaseNetworkMerge -count=1 -v`
  checks the merged network configuration with the real Compose parser.
- Browser coverage checks that new and existing database selection saves a logical reference without
  copying the revealed password into application configuration.
