# Deployment database connections

Deployment setup stores `${{database.ID}}` in the selected runtime variable and links the saved
connection as a dependency. Execution resolves that identity through Databases using the current
encrypted credentials. The explicit admin URL read remains audited and non-cacheable. It is read-only:
it does not create a network, publish a port, or change the saved host DSN.

`GET /databases/{id}/url` takes `format` (`url`, `jdbc`, `adonet`, `mysql2`) and `database` (another
database on the same server) for an application that parses JDBC (credentials as query parameters),
ADO.NET keywords or Rails' `mysql2://`, and for Rails 8's cache, queue and cable databases. The typed
reference records both after the numeric id — `${{database.5.jdbc}}`, `${{database.5.url.app_cache}}`
— and the release resolves the same shape from the container URL. Only a numeric id takes a suffix, so
a connection name containing dots is never misread. Quick setup also offers PostgreSQL 16 with
pgvector (`pgvector/pgvector:pg16`) or PostGIS (`postgis/postgis:16-3.5-alpine`), which Databases
already recognises, and refuses MongoDB 7 on a CPU without AVX or ARMv8.2 atomics before pulling it.

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
Previews cannot share a linked database endpoint/container with another environment, including another
preview or a different saved connection ID. Reference resolution checks known links and bindings before
returning build/runtime credentials; runtime joins also check observed container identity. This does not
discover arbitrary untracked services, DNS aliases or literal credentials. Preview approval still clears
inherited credentials and dependencies.

Every five seconds, reconciliation observes each retained binding. A replacement must match the saved
container name, or the same Compose project and service; matching a reused port alone is insufficient.
It reattaches the alias, repairs missing owned application attachments and records status. A still-running
old database with the alias or an unrelated alias owner blocks repair. Attach/detach/network mutations
are audited without connection strings. Cancellation stops reconciliation with the server.

`GET /deploy/{project}/environments/{environment}/database-links` reports the connection identity,
its engine and database, hostname, network and last observation, scoped to that environment.
Configuration → Dependencies shows these observations. Connected means the network binding was
observed; it does not prove database schema or application health. Observations older than 30 seconds
are marked stale. The database name is read from the sealed DSN the way every other database route
reads it; a connection whose DSN no longer resolves keeps its row without that name.

A binding that reconciliation could not repair carries `detail`, the reason of the pass that failed —
a replaced container that no longer matches the saved identity, an alias another container holds, a
namespace that cannot join a bridge. It is bounded to 300 characters, holds no connection string, and
is the same sentence the routes performing these operations already return. A connected or stale
binding reports no reason: staleness is the age of the last pass, not a failure. The additive
`deploy_database_bindings.detail` column defaults to empty for existing installs.

## Removal and existing installs

The additive `deploy_database_networks` and `deploy_database_bindings` tables retain ownership across
dashboard restarts. Bindings remain for retained releases and rollback until explicit resource removal.
Removing a variable alone does not detach a database from existing releases.

Configuration's removal preview includes the current managed network ID. Applications/Compose stacks
are removed before their network. Cleanup refuses unknown endpoints, disconnects only bound database
endpoints and leaves the database container and its data intact. Preview close uses the same cleanup.
Removing a stale network ID cannot delete a newer replacement. Successful cleanup removes bindings and
clears the Docker ID while preserving the logical network name for subsequent deployment.

Forgetting a bound connection, or dropping its own database, returns `database_linked` before changing
data. Remove its deployment network first. Permanent project deletion remains records-only; it forgets
these bindings and stops reconciliation while leaving Docker resources intact. Use resource removal
before permanent deletion when cleanup is intended.

Existing literal IP values are not rewritten. Reconnect the database once in deployment setup/settings
to save its typed reference. Saved non-loopback IP connections, remote servers and service URLs do not
receive automatic container discovery. Application drivers must reconnect and resolve DNS after a
database interruption; this feature does not preserve an existing TCP connection through replacement.

External providers remain ordinary saved connections or encrypted application variables: provider
credentials, TLS options and hostnames are preserved. For Supabase, choose the connection URL suited to
the server's network; its direct endpoint generally uses IPv6, while the shared session pooler supports
IPv4. See [Supabase's connection guide](https://supabase.com/docs/guides/database/connecting-to-postgres).
No hosted-provider account or live provider credentials are required by the local test fixtures.

## Verification

- Component/API tests cover reference parsing, replacement identity, scoped status, stale observations,
  the reported engine and recorded reconciliation reason with its bound, safe removal plans,
  linked-database deletion, early preview credential refusal and generated-file containment.
- `JD_DEPLOY_LIVE=1 go test ./internal/api -run TestLiveDeploymentDatabaseConnection -count=1 -v`
  provisions PostgreSQL, MySQL, MariaDB, Redis and MongoDB, authenticates from a separate client container,
  refuses a preview's production credential/network reference before allocation,
  replaces each database with the original persistent volume and a different IP, reconciles, then
  restarts the same client with its unchanged URL and authenticates again. It checks loopback publication,
  cleanup ordering and refusal to adopt a foreign network. This establishes DNS reconnection, not a
  continuous application traffic test.
- `JD_DEPLOY_LIVE=1 go test ./internal/dockerx -run TestLiveComposeDatabaseNetworkMerge -count=1 -v`
  checks the merged network configuration with the real Compose parser.
- Browser coverage checks that new and existing database selection saves a logical reference without
  copying the revealed password into application configuration.
