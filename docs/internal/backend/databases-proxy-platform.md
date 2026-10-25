# Databases, proxy, and platform services

## Databases: every database on the server, each engine as itself

The section is two things. For the machine it is an inventory: every database server, database file
and data directory found here, connected or not, with what is known about each and what keeps the
rest from being opened. For one database it is that engine's own control panel — a table editor, a
statement runner, a schema browser and the server's operations for a SQL engine; keys, a console and
the server's own figures for Redis; documents, pipelines and a command console for MongoDB. Nothing
is offered for an engine that lacks it, and what an engine has is stated once, on the server (the
capability table below), never by a page.

`internal/dbx` is the engines: connections, dialects, statement classifiers, catalogue reads,
discovery, dumps. `internal/api` is the routes. `handlers_db.go` mounts `/databases` behind the
protected-connection guard, and each surface registers its own routes from its own file —
`mountDatabaseAdminRoutes`, `…RedisRoutes`, `…MongoRoutes`, `…InventoryRoutes`, `…ConnectionRoutes`,
`…WorkbenchRoutes`, `…SchemaRoutes`, `…OpsRoutes`, `…TransferRoutes`, `…SeedRoutes`, `…PITRRoutes`,
`…UpgradeRoutes` and `…AssistantRoutes` — grouped by the capability it needs. [Routes](#routes) lists every one with its
capability, whether it is destructive, the
action it is audited as and what a protected connection does with it. The pages over them are the
`/databases/*` row of the [frontend feature map](../frontend/feature-map.md), and the checks that
depend on what a request carries are stated once in
[request lifecycle](../architecture/request-lifecycle.md#checks-that-depend-on-what-a-request-carries).

### Engines, flavours and the capability table

- **Eight engines on pure-Go drivers**, so the image still needs no CGO: PostgreSQL, MySQL/MariaDB,
  SQLite, SQL Server, ClickHouse and Oracle through `database/sql`, each behind its own `Dialect`,
  and MongoDB and Redis through code of their own, because a key or a document is not a row and the
  vocabulary is part of what makes each engine legible. A `Driver` (`postgres`, `mysql`, `sqlite`,
  `sqlserver`, `clickhouse`, `oracle`, `mongodb`, `redis`) is how the dashboard talks to a server;
  what the server *is* is its flavour, below. Discovery knows sixteen more products that no driver
  here speaks to — Memcached, Elasticsearch, OpenSearch, etcd, Cassandra, ScyllaDB, Neo4j, InfluxDB,
  CouchDB, RabbitMQ, NATS, Kafka, Qdrant, Meilisearch, Typesense and DuckDB
  (`dbx/discover_engines.go`) — and lists one it finds with an empty `driver`: "there is a database
  on this server that this page cannot show you" is a fact the operator is owed, where silence would
  read as "there is none".
- **`Dialect` is the whole abstraction**: driver name, quote character, bind marker, pagination tail,
  catalogue queries, DDL keywords, session list, size query — one method each, six implementations. The
  old shape was a `switch driver` inside a dozen functions; it worked at three engines and broke at seven,
  because a missed switch failed at runtime rather than compile time.
- **A driver is a wire protocol; a flavour is the product behind it** (`dbx/flavor.go`: MariaDB, Percona
  and TiDB behind `mysql`; Valkey, KeyDB and Dragonfly behind `redis`; TimescaleDB, CockroachDB and
  YugabyteDB behind `postgres`; FerretDB behind `mongodb`; Azure SQL Edge behind `sqlserver`).
  `dbx.DetectFlavor` and `VersionNumber` are pure functions over what the server said — its version
  string, MySQL's `version_comment`, a Postgres database's extensions, the text of Redis `INFO`, the keys
  of Mongo's `buildInfo` — and the probes that read those are `IdentifySQL`, `IdentifyRedis`,
  `IdentifyMongo` and `ProbeIdentity`. The answer is kept per connection for ten minutes and outlives a
  stop, so a stopped Valkey is still drawn as Valkey, and one that has stopped answering is too, in the
  fleet as in its own summary.
- **The capability table is stated once, on the server.** `dbx.Capabilities(driver, flavor)` is one
  reading of what a product can be asked for; `GET /databases/drivers` serves it per driver and per
  flavour, with the default port, a DSN example and the filter operators the engine's browse takes
  (`dbx.FilterOpsFor`), and `GET /databases/{id}` serves it resolved for the flavour that answered, so
  the page gates on what the server enforces and states nothing a second time. Every flag is stated once.
  The ones more than one engine family answers for are rows of the table in `dbx/capabilities.go`
  (`console`, `sessions`, `kill`, `cancel`, `settings`, `replication`, `export`…), where the SQL engines
  answer from the interfaces their dialects implement (`OpsCapabilities`, `ExplainForms`) and Redis and
  MongoDB are named beside them; the ones that belong to one surface are registered beside its code from
  an `init` (`RegisterCapabilities`, in `discovery_`, `admin_`, `workbench_`, `catalog_`, `ops_`,
  `redis_`, `mongo_`, `transfer_`, `upgrade_`, `orm_`, `seed_` and `alert_capabilities.go`). A registration replaces a row of the same
  name for every engine, so a flag stated twice is a defect and `TestCapabilityTableIsCoherent` refuses
  one. A flag is a yes or a no; a few are a list (`catalogGroups`, `ddlOperations`, `maintenanceActions`,
  `ormTargets`, `exportFormats`, `importFormats`, `alertKinds`; empty and never null) or a word where a feature comes
  in more than one form and `false` where the engine has none (`rowIdentity`, `returnsChangedRow`,
  `readOnlyScope`, `importAtomic`, `json`, `hashFieldTtl`, `commandReference`, `memoryBuffer`). Two names that look alike
  are two features: `cancel` stops what another session is running (`/activity/cancel`, `/mongo/killop`),
  `queryCancel` stops a run the editor itself started (`/query/cancel`). `TestCapabilitiesPerEngine`
  writes out who has each flag by hand, and `TestLiveCapabilityFlagsAnswerOnRealServers` holds a flag
  that is true to a route that answers.
- **The served table is also a file, and a test holds the two together.** The frontend's tests run
  with no server, so what `GET /databases/drivers` answers is kept as
  `backend/internal/api/testdata/database-drivers.json` — eight drivers, each with the same 132
  flags (116 a yes or a no, nine a word, seven a list) and its flavours' rows — which the engine
  registry's tests (`frontend/src/components/database/engine.test.js`) and the browser fixture
  (`frontend/tests/browser/database-fixture.ts`) read. `TestTheDriverCatalogueSnapshotIsCurrent`
  fails the moment the route and the file differ. After giving a flag to an engine or taking one
  away, write the file again from `backend/` and then run the frontend's tests against it:
  `go test ./internal/api -run TestTheDriverCatalogueSnapshotIsCurrent -update-drivers`.

### The inventory: what is on this machine

The section used to list what somebody had connected. It now starts from the machine: the control
center's "Found on this server" and the add flow's third way in are both this reading, and a server
is connected by naming the instance found, not by typing its address again.

- **Everything on the machine that holds a database is listed, connected or not.** `GET
  /databases/inventory` answers `{instances, scans, ignored, detail, checkedAt}`. An instance
  (`dbx.Instance`) is a server, a file, a data directory or a file embedded in a container, with a stable
  `key` (`docker:<container>`, `compose:<project>/<service>`, `host:<unit>`, `host:<engine>:<port or
  socket>`, `file:<path>`, `data:<path>`, `embedded:<container>:<path>`), its engine, driver (empty for a
  product nothing here opens, which is listed all the same), flavour, state, endpoints, how its
  credentials are known, why it was taken for what it is, and where it cannot be connected, the reason.
  Classification is `dbx.Discover`, a pure function of facts; the collectors that read the machine are in
  `api/database_inventory_*.go`, and each reports its own outcome in `scans`, so a missing Docker daemon
  is a stated silence and never a failed request. Containers are read with `all=true` and recognised by a
  ladder — image name, then the variables the stock image sets, then the command, and last a distinctive
  port, labelled a guess and never connected unasked. Every rung is read against the program in the
  command position (`readCommand`): a variable an image sets is inherited by everything built on it, so
  it is believed only while the command names that engine's server or nothing but the image's own
  entrypoint, and an image started as its own client (`psql`, `mongosh`, `redis-cli`) is not a server on
  any rung. The in-container port and `--requirepass` are read from the command line only when it is the
  server's. A container's key is settled before anything is recorded under it: a `docker compose run`
  container is keyed by its own name, and a second container stating one service's labels falls back to
  `docker:<name>`. Host servers are one instance per process with several endpoints (both loopback
  families collapse; MySQL's X port and ClickHouse's HTTP port are side doors), matched on the exact
  process name, joined to their unix sockets (`proxysvc.ListUnixListeners`) and their systemd unit. A
  host server is keyed `host:<unit>` only under a unit named for its engine; under a supervisor's unit
  (`supervisor.service`, `pm2-<user>.service`, `user@<uid>.service`) it is keyed by engine and port, has
  no `host.unit`, and says in its evidence which unit runs it. Two host servers that still share a key
  are numbered by address, not by process id, so a restart does not swap them. A MySQL data directory is
  one only with the `mysql` schema directory beside `ibdata1` or `ib_buffer_pool`; an installed server
  that is stopped is listed from its unit — including one that is disabled, which systemd unloads and
  only its unit file names (`procs.ListInstalled`, `dbx.InstalledUnits`), and a stopped Debian PostgreSQL
  cluster, found by its `/etc/postgresql/<version>/<name>` directory — and a cluster's port is read from
  its `postgresql.conf`. Files are confirmed by their first sixteen bytes and never opened with a driver
  to be listed, and the dashboard's own store is not read at all: closing a descriptor for it would
  release the store's SQLite locks (see [runtime boundaries](../architecture/runtime-boundaries.md));
  the walk is server-chosen roots through `files.Resolve`, bounded by depth, visits and time, on a
  ten-minute cadence or `POST /databases/inventory/scan`. Where `JD_FILE_ROOTS` is narrower
  than those roots — beneath `/home`, or beside all of them — the file roots themselves are walked: a
  broad root above a file root is outside the roots as a whole, and the scan used to walk nothing and
  list no file that `inventory/connect` would still take by its path. A file root with a broad root
  inside it (`/`) is not walked from its top. A database kept in a container's own writable layer is
  found through the Engine's diff and archive reads and listed as `embedded`, never connectable. The
  route is on the read surface: for `system.admin` every container is inspected; for every other role
  nothing is, and the list is a separate reading built from what those roles already see elsewhere
  (`detail: "reduced"`). No reading carries a credential.
- **Connecting is one explicit act about one instance.** `POST /databases/inventory/connect`
  (`system.admin`) takes a key and, optionally, a name, user, password and database. The server looks
  the instance up again, reads what its container states (a `*_FILE` secret is read from the container
  at that moment), signs in, and saves only then, recording the key in `db_connections.origin`. It
  never tries a server that needs a password without one: `409 credentials_required` names the
  account, `409 sign_in_failed` carries the engine's own words when what the container states is
  refused, `400 sign_in_failed` when what the operator typed is. The dashboard's own store is marked
  `self`, refused with `409 self_database`, and refused again in `containDSN` whatever route offers it.
  A user or database name is joined to a connection string only as a name: `dbx.BuildDSN` builds
  nothing for one holding `?`, `/` or a control character (or `@`, `:` in a user), writes MySQL's
  string with the driver's own `FormatDSN`, and a container whose environment states such a name is
  listed and not connectable — a `?` in `MYSQL_DATABASE` would otherwise set driver options such as
  `allowAllFiles` on a saved connection. Signing in to MongoDB with no account runs `listDatabases`,
  because a ping is answered without authentication.
  `POST /databases/inventory/ignore` records a key in `db_inventory_ignored`. `POST /databases/sync`
  keeps its contract and now signs in before saving, records the origin, skips what is ignored and
  reports it, repairs a stranded connection rather than adding a second one (`repaired`), and runs its
  host half with Docker absent; forgetting the last connection to a found
  server marks it ignored (`ignoreOriginOnForget`), so it stays forgotten. It signs in four at a time
  and remembers a refusal against what was tried (`database_inventory_signin.go`): the same container
  stating the same credentials is not asked again until either changes or the operator connects it by
  hand, a server that merely did not answer is retried after two minutes, and a sign-in the request
  ran out of time for is reported as not tried. `/adopt`, `/host` and
  `/host/grant` sign in the same way and match an existing connection by driver, address identity
  (`dbx.AddressIdentity`: every loopback spelling is one place), database and user.
- **What discovery never does.** It reports how an instance's credentials are *known* — `credentials` is
  `env`, `args` or `secret-file` (the container states them: its environment, its command line, a file
  its environment names), `open` (the server takes connections without any), `peer` (the engine admits
  its own system account over its unix socket, so an account can be made from the host), `needed` or
  `unknown` — and never the credential. It does not guess a password: a server that wants one is listed
  as wanting one, and a sign-in is tried only with what the container itself states or the operator
  typed. It does not open a file with a driver to find out what it is, and listing writes nothing to what
  it finds: the only rows discovery writes are the dashboard's own (`db_connections` when a connection is
  saved, `db_inventory_ignored`). How sure a classification is travels with it (`confidence`: `image`,
  `fingerprint`, `command`, `port`, `process`, `socket`, `unit`, `magic`, `marker`), and `evidence` says
  why in sentences that name variables and never their values. One reading of the machine answers for
  twenty seconds (`dbInventoryFresh`), kept apart for administrators and for everybody else because they
  are different readings, not one filtered into the other. A file says whose it is (`file.holder`): the
  dashboard's own store, a container's volume, a deployment's or a compose stack's directory, the
  system's (`/var/lib`, `/var/cache`, an NSS `nssdb`), a backup, a tool's, or an application's. A
  backup is a copy kept aside — a file with the store's own name anywhere but the store (a copy taken
  before a repair, one a test run kept), anything under a directory with `backup`, `backups` or `bak`
  as one of its words (`/var/backups`, `db-backups`), or a file named as a copy (`.bak`, `.backup`,
  `.old`, `.orig`). A tool's is a program's own state — anything under a hidden directory, a
  browser's or a browser driver's profile (a directory naming Playwright, Puppeteer, Chrome,
  Chromium, Firefox or Mozilla), a scratch directory below the top of the tree (`tmp`, `temp`,
  `scratch`, or a name ending in one), or what a build or a test run fills (`node_modules`,
  `testdata`, `fixtures`, `test-results`, `playwright-report`, `coverage`). The control center lists
  only servers and the files an application keeps; a tool's, the system's, a backup, a data
  directory with no server and what was ignored are counted behind its one *Set aside* fold.
- **Testing and saving a connection typed by hand.** `POST /databases/test` uses the same probe for every
  engine (Redis used to be reported unreachable for having no SQL dialect), and `POST /databases/` takes
  `probe: true` to dial before saving.
- **An account made from the host.** `POST /databases/host/grant` (admin) is for the native
  server whose password nobody knows — the apt-installed Postgres whose `postgres` role has never
  had one. It runs the engine's client on the host through `hostexec`: `psql` as the host's
  `postgres` account (looked up in the host's passwd, which is mounted at `/etc`, through
  `setpriv`) over the Unix socket, where peer authentication admits it, with one `DO` block that
  creates or resets the account (`format('%I … %L')`, so the server quotes both); `mysql`
  (or `mariadb`) as root over the socket, making the account for both `localhost` and
  `127.0.0.1`; `mongosh` under the localhost exception; `clickhouse-client` as `default`. Redis
  has no accounts, so its `requirepass` is read from `/etc/redis/redis.conf` (or Valkey's).
  The password is generated on the server unless supplied, reaches the client as one argv
  element and never a shell, and the connection is probed over TCP before anything is saved;
  an existing connection to the same server, database and account is re-sealed rather than
  duplicated, and one to the same address as another account is left as it is. Audited as
  `database.connection.host.grant` with the account and outcome, never the password.
  `TestLiveHostPostgresAccount` exercises it as root against a real native server
  (`JD_TEST_HOST_PG_PORT`).
- **A database started from the dashboard stays on this server unless the request says otherwise.**
  `POST /databases/provision` takes an `exposure`, the word the access route below takes
  (`provisionBinding`; empty means `local`). The
  default used to be every interface with the firewall opened for it, which is what happened to every
  operator who did not read the switch, and the exposure the fleet then flags. `public` still publishes
  on `0.0.0.0` and opens the port through the same `setDBFirewall`, with the exposure, the firewall's
  word and any firewall error in the `database.server.provision` audit and in the `202` reply. The saved
  connection dials loopback either way — `hostAddress` maps a `0.0.0.0` binding to `127.0.0.1`.
  The templates are a closed, server-side list (`provisionTemplates`, offered in `provisionOrder`):
  PostgreSQL, PostgreSQL with pgvector or PostGIS, PostgreSQL full, TimescaleDB, MySQL, MariaDB, Redis,
  Valkey, KeyDB, Dragonfly, MongoDB and ClickHouse. Each carries its own closed list of releases; a
  request names a `version` from it and never an image reference, so what is pulled is always a
  reference written in that file. *The provisioning catalogue* below lists them and says how each
  release's support status and the full template's image are handled. `user` and `password` are optional: the account name is held to `provisionUserRe` (and may
  not be `root` on MySQL or MariaDB, nor given at all to the password-only engines), a supplied password
  to `provisionPasswordRe` — deliberately narrow, because it is written into a Redis configuration
  line, a ClickHouse XML file and a MySQL DSN, and a character that means something in any of those is
  refused rather than escaped three ways. A password the request did not supply is generated and never
  returned. Every template's every release has to be one `dbx.Detect` recognises and reads the same
  account back from (`TestProvisionTemplatesAreCoherent`), or the adopt that follows cannot connect it.
  **A PostgreSQL server counts its statements from its first start.** Every PostgreSQL template
  (pgvector, PostGIS and TimescaleDB among them; `initdb` on the template) is created with a constant
  script in the image's `/docker-entrypoint-initdb.d` (`postgresFirstBoot`), written into the
  container between its creation and its start through `dockerx.ContainerSpec.Files` — a field with
  no JSON name, so no request to the Docker page can carry one — and run once by the image as it
  initialises the data directory. It adds `pg_stat_statements` to `shared_preload_libraries` through
  `ALTER SYSTEM`, after whatever the image already preloads (TimescaleDB's image names its own
  library in `postgresql.conf`, and the list is built by the server with `\gexec`, so it is kept),
  and creates the extension in the new database. The list goes into `postgresql.auto.conf` and never
  onto the container's command line: a `-c` there outranks every configuration file, and a library
  added later from the Parameters page would be stored and silently not loaded. `autoExplain: true`
  adds `auto_explain` with a one-second `log_min_duration`, `log_analyze` and `log_buffers` on and
  `log_timing` off — each step's actual rows, without the per-node timing that costs every statement —
  and every one of them is an ordinary parameter afterwards. The options say which templates do
  either (`statements`, `autoExplain`); any other engine refuses `autoExplain`. Both are in the
  `database.server.provision` audit detail. `TestLiveTemplatesCountStatementsFromTheFirstStart`
  starts each PostgreSQL template under `JD_TEST_STATEMENTS_CONTAINER`.
  The Redis-protocol engines that do not read a password from their environment are started through a
  constant bootstrap (`passwordProvisionBootstrap`) that writes a private configuration and removes it
  first: the kernel refuses to reopen a file in `/tmp` that was handed to the server's account
  (`fs.protected_regular`), and without the `rm` the container started once and died on every restart.

### The provisioning catalogue

The releases each template offers, as refreshed on 2026-10-17. A version is a release line, and its
tag floats only within that line's patch releases; Timescale is pinned to one release of the extension.

| Template | Releases (default first in bold) | Image |
| --- | --- | --- |
| `postgres` | **18**, 17, 16, 15, 14 | `postgres:<major>-alpine` |
| `pgvector` | **18**, 17, 16, 15 | `pgvector/pgvector:pg<major>` |
| `postgis` (x86-64 only) | **18**, 17, 16, 15 | `postgis/postgis:18-3.6-alpine`, `<major>-3.5-alpine` below 18 |
| `postgres-full` | **18**, 17 | `jd-postgres-full:<major>`, built on the server |
| `timescaledb` | **18**, 17, 16 | `timescale/timescaledb:2.30.2-pg<major>` (Timescale stopped building for 15 at 2.28) |
| `mysql` | 9.7, **8.4**, 8.0 | `mysql:<release>` |
| `mariadb` | 12.3, **11.8**, 11.4, 10.11, 10.6 | `mariadb:<release>` |
| `mongodb` | **8.0**, 7.0, 6.0 | `mongo:<release>` |
| `clickhouse` | **26.8**, 26.3, 25.8 | `clickhouse/clickhouse-server:<release>` |
| `redis`, `valkey`, `keydb`, `dragonfly` | unchanged | |

- **PostgreSQL 18 keeps its data a directory higher.** From 18 the official image (and every image
  built on it: pgvector, PostGIS, Timescale, the full template) sets `PGDATA=/var/lib/postgresql/18/docker`
  and declares `/var/lib/postgresql` the volume, so a later `pg_upgrade --link` can see two majors side by
  side. `postgresRelease` gives an 18 release that mount target; a volume at the old
  `/var/lib/postgresql/data` would be left empty while the cluster went into an anonymous volume that a
  recreate does not carry over. `TestProvisionMountsTheVolumeWhereEachMajorKeepsItsData` holds it.
- **Support status is a fact, not a finding.** `GET /databases/provision/options` gives each release
  its `endOfLife` from `dbx.ReleaseEndOfLife(flavour, release)`: the vendors' published dates, a table
  in `dbx/eol.go` (PostgreSQL, MySQL, MariaDB, MongoDB, SQL Server, Oracle, and ClickHouse's long-term
  March and August releases; TimescaleDB is dated as the PostgreSQL it runs on). Redis, Valkey, KeyDB,
  Dragonfly and the forks that keep their own calendar (CockroachDB, YugabyteDB, TiDB, FerretDB) have
  no date and say nothing, and so does a release whose last day is not published yet (MariaDB's newest
  rolling release, MongoDB 8.3). The version picker says the chosen release's "Supported until Nov 2030"
  under it — a day still ahead to the month, in `calendarDate`'s words on the UTC calendar
  (`components/database/support.ts`). A release whose date has passed stays on the closed list —
  each engine's most recent such line, for a server that has to match the one it replaces — and the
  picker keeps it behind an *Older versions* fold that states each one's status; it moves there by
  itself on the day it ends (PostgreSQL 14 on 2026-11-12), with no change here; chosen, it is dated
  there ("End of life since Apr 30, 2026") and not under the releases still maintained. Every default was
  maintained on the day of the refresh (`TestProvisionCatalogueDefaultsWereMaintainedWhenItWasRefreshed`),
  and no offered tag floats across release lines. Existing servers on any release keep working: the
  list decides only what can be started.
- **The full template is built here, never pulled.** No published image carries pgvector, PostGIS,
  pg_cron, pg_partman, pgaudit, hypopg and pg_repack on the official image's layout, so
  `backend/internal/api/recipes/postgres-full/Dockerfile` (embedded in the binary) builds one: the
  official Debian `postgres:<major>-trixie` image plus the PostgreSQL project's own apt packages of each,
  which that image already trusts — nothing is fetched from anywhere else. `POST
  /databases/provision/build` (`system.admin`) writes the recipe into a private temporary directory and
  runs `dockerx.Build` with `--pull` as a job (`database.image.build.<engine>.<version>`; one build at a
  time, `StartExclusive` on `database.image.build.`, 30 minutes), audited `database.image.build` with the
  engine, release, recipe digest, job id and whether the request attached to a running build. Asking
  for the image already being built answers that job, so a returning page attaches to it; another image
  is refused with `409 build_running`. The build is labelled `io.just-dashboard.recipe=<digest>`, the
  first twelve hex digits of the recipe's SHA-256: the options report `build.ready` only for an image
  carrying the current digest (and `build.job` while one runs), so an image built from an older recipe
  is built again, and `POST /databases/provision` refuses the template with `409 image_not_built`
  until it is ready rather than letting Docker try to pull a name that is this server's own. The name
  is bare (`jd-postgres-full:18`), which resolves to Docker's official `library` namespace, where
  nobody can publish an image a pull could mistake for it. The image names `pg_cron,pgaudit` in the
  `postgresql.conf.sample` every new cluster is made from, the way TimescaleDB's image names its own
  library, and a first-boot script it carries points `cron.database_name` at the database the server
  is created for (`ALTER SYSTEM`, through `\gexec`); the container runs the official command, so both
  stay ordinary parameters the Parameters page can change — on the command line they would outrank
  every file. The two extensions that need preloading are then ready to enable on the Extensions page
  like the rest; nothing is created in the database until the operator enables it. `dbx` recognises the image by name
  (`jd-postgres-full`, variant `full`). The add flow builds first when the chosen release is not built,
  drawing *Build the image* as the first stage and the job console with the build's output, and
  quick setup in a project's sheet offers the template only once its image is built.

### A connection: its record, its state and its power

- **A connection is a thing with a state, not only a row.** `db_connections` carries what the operator
  says about one — `environment` (a short tag), `read_only`, `notes` — beside what its DSN says, set
  through `PUT /databases/{id}` field by field (a field left out keeps its value). `GET /databases/` and
  the fleet list a row whose DSN no longer unseals or whose SQLite file left the file roots as `broken`
  with the reason, instead of dropping it; `PUT` with a new DSN repairs one and `DELETE` forgets one.
  Every data route under its id still fails, so the pages outside the section that pick from `GET
  /databases/` — the backup job form, a deployment's database pickers and dependencies, the command
  palette, the overview card — draw such a row as one that cannot be opened, with its reason, rather than
  offer it (`frontend/src/lib/db-connections.ts`).
- **Forgetting is one function, and it tells discovery.** A row is removed in one place,
  `forgetConnection`, whichever road asked for it — forgetting the connection, dropping the database it
  pointed at, removing the container that served it, removing a deployment's resources
  (`deployment_resource_remover.go`): the row goes unless a deployment is bound to it, the pool is
  closed, what was kept under the id is dropped, and discovery is told where the connection came from
  (`originOfConnection` before the row goes, `ignoreOriginOnForget` after), so that when it was the last
  connection to a found server the next sync does not connect the same server again. The audit entry of
  the request that removed the row says when that happened: `origin` and `ignored: true` in the detail of
  `database.connection.delete` and `database.drop`, `ignoredServers` in `deploy.resources.remove`. The
  connection's JSON carries `origin`, the inventory key of the found server or file it was made from, and
  `""` for one typed in by hand.
- **A connection sealed under another master key is repaired from its server.** A reinstall that keeps
  `/var/lib/just-dashboard` but writes a new `JD_MASTER_KEY` leaves every `dsn_enc` unreadable. Such a
  row is not attached to its found server (`savedConnections` skips what will not unseal), so the
  server used to be found as unconnected and connecting it added `name-2` beside the red row and the
  jobs and deployments bound to it. `database_stranded.go` keeps the row instead: a row with an
  `origin` whose DSN does not unseal is *stranded*, and the first sign-in to that server — `POST
  /databases/inventory/connect`, `/adopt`, `/host`, `/host/grant` or the reconcile — reseals the DSN
  that worked into it, under its own id and name (`repaired: true` in the route's audit detail,
  `repaired` in the sync's report and audit). A background pass does the same a minute after boot and
  every thirty minutes after, for servers that sign in with what they state (`signsInUnasked`, the
  reconcile's rule), skipping a server another connection already opens and one marked ignored, honouring
  the reconcile's refusal memory, and auditing `database.connection.repair` as `system`. Two stranded
  rows from one server are left alone, since nothing says which account or database a sign-in stands
  for. A row typed by hand has nothing to be read again from. The reason a stranded row is listed with
  says which case it is: connect it again under *Found on this server*, forget it because another
  connection now opens its server, or (no origin) enter it again. `install.sh` asks for the earlier key
  when it finds the store without one (`docs/internal/operations/terminal-tools.md`).
- **One connection's summary.** `GET /databases/{id}` (read surface) is one connection's reading without
  dialling any other: what the server says it is, whether it answers, where it runs, which power actions
  apply, its exposure, bound deployments and the capability flags for its driver and flavour. Where the
  server is a container, `container` also carries what it may use — `memoryLimit` in bytes, `cpuLimit` in
  processors, each absent where there is none, and `restartPolicy` — under the names the Docker page's
  own route uses, read for a stopped container too (one inspect; the listing inspects only the running
  ones) — Home's *Runs as* draws them, with *Change* to Settings → Resources for an administrator where
  compose does not own the container. It answers 200 for a stopped, unreachable or broken connection;
  those are states, not errors.
  `endOfLife` is when the release the server last said it runs stops being maintained
  (`dbx.VersionEndOfLife`, by driver, flavour and the server's own version string), absent where the
  table has no date: Home's identity line and General's *Name and labels* say it beside the version as
  a plain fact — "supported until Nov 2028", "end of life since Nov 13, 2025", in the words of the
  "added Sep 1, 2026" beside it — and nowhere else, with no
  colour, badge or advice. See *The provisioning catalogue* for the table.
- **Where a server runs is read off the machine each time** (`dbHostView.place`), never stored: the
  container that publishes or answers at the address, then the
  process listening on the port and its unit, then a stopped container whose configuration publishes that
  port, then — for a native server that has stopped — a unit on evidence only: the one the connection was
  last seen running under (kept in memory), or the engine's only unit when the connection is to the
  engine's own port. A connection to another port that nothing answers on is a dropped tunnel or a
  removed container; it is dialled, reads `unreachable`, and is given no unit, because what is decided
  there is what Start starts. A container or unit known to be down is not dialled. One request reads the
  listening sockets, the unit list and each stopped container's published ports once and shares them
  across its connections (`dbHostView`).
- **Power.** `POST /databases/{id}/power` `{action: start|stop|restart}` acts on that container
  (`dockerx.Lifecycle`) or unit (`systemctl` on the host through `hostexec`, the unit name validated,
  an argument vector). The route is in the `service.control` group; `stop` and `restart` additionally
  need `destructive` and spend `destrLim`, checked by hand because they share the path with `start`.
  A container is given `dbStopGrace` (90 s) to shut down before Docker kills it, not Docker's own ten —
  a database answers SIGTERM by writing what it holds — and a request may ask for up to ten minutes
  with `timeoutSeconds`. An action the summary does not offer for the state the server is in (restart
  of a stopped one, start of a running one) is refused with `409 power_unavailable` before Docker or
  systemctl is asked. systemctl is reached through `dbSystemctl`, which a test replaces.
  Audited as `database.power.<action>`. It never guesses: a unit is taken for the server only when it
  is named for the engine (what listens on a database's port may be an ssh tunnel, whose unit is
  sshd's, or `docker-proxy`, whose unit is Docker's), a container found only by its port only when it
  is the engine — by its image's name or, for a private build or a tag that has moved on, by what the
  inventory read from its environment and command (a port alone never counts) — and a remote server or a file is refused with `409 power_unavailable` and the
  reason the summary gave. A compose-owned container is not refused, as it is not on the Docker page:
  nothing here recreates it. A stopped container is found again by the connection's `origin` (`docker:<name>`
  or `compose:<project>/<service>`) before its published port is looked for.
  **A change of power is read by everyone until it settles.** The request is slow by design and the
  browser that sent it was the only one that knew it was happening, so the server keeps it
  (`dbConnState.power`, in memory) and `GET /databases/{id}` and the connection's fleet entry carry
  `inFlight: {action, since}`. It is held while the route is acting, whatever the server reads as —
  a restart reads `running` before it has gone down — and after the route has answered until the
  server reads the state the action leaves it in (`stopped` for a stop, `running` for a start or a
  restart), or `dbPowerSettle` (a minute) has passed: a started container whose engine never answers
  ends as `unreachable` with nothing in flight. An action that was refused or failed leaves none.
  Reading is what ends a change, so nothing has to wake up to do it, and the fleet does not keep a
  failed dial of a connection whose change is still held.
- **Where a database is reachable from is a reading and four answers, not a trip through three pages.** `GET
  /databases/{id}/access` (admin) reports the container behind a connection, whether the server is one
  the sync would connect on its own (`detected`), whether this route can change the binding (`managed`: a
  container that is not compose's and publishes the engine's port), its exposure read off the port
  binding (`local`, `private` with the bound `address`, `public`, `remote`, and `allowlist` for every
  interface with the gateway keeping the port to a list), the machine's public addresses, the host
  addresses the port can be published on alone (`addresses`: tailnet, VPN, private and uplink ones,
  graded by `netsec.HostNetwork.Place`, tailnet first; loopback, public, bridge and link-local ones are
  not offered), the list (`allow`), the gateway's part (`gateway`: whether nftables is there to keep a
  list, the networks every drop in the table lets through first — the dashboard's allowlist ranges and the
  addresses kept on the Protection page — whether the kernel holds the port's rules and how many attempts
  they refused), the firewall's part (present, on, an allow rule for the port from anywhere, the sources
  other allow rules name, editable) and the request's own address (`client`, the SSH session's behind a
  tunnel), which the list's form offers.
  `PUT /databases/{id}/access` takes `exposure: local|private|allowlist|public`, with `address` (one of
  `addresses`, checked again against the host) or `allow` (up to 16 IPv4/IPv6 addresses or networks,
  normalised by `netx.NormalizePortSources`: masked, merged with any they lie inside, IPv4 first; every
  address, a range wider than /8 or /16, loopback, multicast and unspecified are refused). The change is
  planned first (`planDBAccess`, `accessSteps` in `db_reach.go`) and runs in the order that never leaves
  the port open to more than both ends allow: the gateway's list is kept before the binding can widen; the
  container is recreated through `dockerx.Recreate` only when its configured host address does not
  already give the reach (any every-interface binding serves Anywhere and an IPv4 list; a list naming
  IPv6 needs both families, which Docker spells as no address; a binding Docker was given once per
  family is folded into one); the host firewall is then reconciled; the list is released last. A list
  that cannot be kept stops the change before anything else; a recreate that fails puts the list back as
  it was (`gateway: restored`); a firewall or a release that cannot follow is reported beside the change
  (`firewallError`, `gatewayError`). **The gateway is what keeps the port to the list.** Docker's NAT
  rules forward a published port before the host firewall's input chain, so a ufw or firewalld rule from
  one address restricts nothing for a container; `netx.SetPortAllowlist` puts the list in the gateway
  table's prerouting chain, where the connection is still addressed to this host and not yet translated
  ([kept ports](network.md#gateway-and-protection)). The host firewall is given the rules the reach
  wants (`dbFirewallWant`): one from anywhere for Anywhere, one per listed network for a list, none for
  this server or one address. They are worked out against its rules (`dbFirewallPlan`: an add for each
  wanted source no rule admits yet, then a removal for each rule of this route's nothing wants; a rule
  is this route's by its comment `just-dashboard database <port>`, or on firewalld, which keeps none, by
  being exactly the rule written for a listed network; ufw's IPv6 copy goes with the rule beside it) and
  applied as netsec firewall plans of at most `netsec.MaxPlanOperations`, adds before removals, inside
  `protectFirewall` with the request's access context, each step filed in the firewall history.
  `POST /databases/{id}/access/preview` (admin, not audited) plans the same request and changes nothing:
  the gateway's exact rules, whether the container is recreated, the firewall plan's steps as netsec
  reviews them, and what the firewall would refuse. The change sits in the destructive group beside the
  Docker page's Recreate, is refused for a compose-owned container, and audits as `database.access.change`
  with the exposure and binding before and after, the list before and after, the address and its
  interface, what the gateway and the firewall did and each firewall step. Publishing a database port is
  the operator's explicit, audited decision, the same one the Docker page already allows; invariant 7 is
  about what the dashboard itself binds. The summary and the fleet read the same exposure (`listed` on
  the host view upgrades a public binding the gateway keeps to `allowlist`, from the spec alone). Removing
  the container from the danger zone releases its list, which would otherwise go on refusing whatever
  published that port next; removing it from the Docker page does not, and the next database to keep
  that port to a list takes the leftover away first (`releaseOrphanedDBPort`: a `database:` owner whose
  container Docker no longer has). `GET
  /databases/{id}/url?target=public` hands back the saved string with the machine's public address (IPv4
  preferred) in place of loopback, for pasting on another machine — or the bound address, for a port
  published on one of the host's own.
- **A connection pooler is the database's, and goes where it goes.** `PUT /databases/{id}/pooler`
  runs PgBouncer (`edoburu/pgbouncer`, pinned by version and digest in `dbx.PoolerImage`, run with every capability dropped and a 256 MiB limit) in a container
  `<database container>-pgbouncer`, on a private bridge `jd-pool-<id>` it shares with the database, which it
  reaches by the container's name (`api/database_pooler.go`, `api/handlers_db_pooler.go`, `dbx/pooler.go`).
  It is offered where the capability table says `pooler`: PostgreSQL and TimescaleDB, not CockroachDB or
  YugabyteDB, for a container that is running, not compose's and on a bridge network. A request says
  `poolMode` (`transaction` or `session`), `poolSize` (per database and account), `maxClientConn` and
  `authMode`, and is the same call to turn it on and to change it: `ensurePooler` makes the container,
  network and files match the record and is safe to run again.
  - *Two files, no secret in the environment.* `pgbouncer.ini` and `userlist.txt` are rendered by `dbx` from
    the settings and the connection's credentials, copied into the container before it starts, and
    reread by `SIGHUP` (`dockerx.Signal`) so a changed setting or password does not drop a client. The
    userlist is mode 0600 and owned by the PgBouncer account (uid 70); the configuration holds no password. The
    image's own entrypoint, which writes its configuration from environment variables, is bypassed for that
    reason. Only a change of `authMode` restarts it: PgBouncer keeps the accounts it loaded from the userlist
    through a reload and then asks them the wrong way. `userlist` authenticates the connection's own account
    from its password, written as it is because PgBouncer must log in to the database as it. `auth_query`
    adds, in the connection's database and in one transaction, an account `jd_pgbouncer` that can only
    call one `SECURITY DEFINER` function `jd_pgbouncer.get_auth` (search path `pg_catalog`, revoked from
    `PUBLIC`), owned by the connection's superuser. The account is created from a SCRAM verifier computed in
    Go, so its password is in neither the server's log nor its statement statistics. The function reads
    `pg_authid`, answers only for accounts that may log in, returns no hash for one whose password has
    expired, and answers for superusers too: signing in through the pooler takes the same password as signing
    in directly. It is made when the mode is entered and never altered after: a role or schema of those
    names that is already there is refused (`409 pooler_auth_taken`), and so is a connection that does not
    sign in as a superuser. Whatever fails after they are made removes them, and so does leaving the mode,
    turning the pooler off, and forgetting the connection (best effort, reported in the audit entry, and not
    for a protected connection). Dropping the connection's database takes the function with it and leaves
    the role, which is the server's: that is removed from the admin database, where PostgreSQL refuses it
    while anything elsewhere still depends on it. Both modes also list `jd_pooler_stats`, whose sealed password (`db_poolers.secrets_enc`) is how
    the dashboard reads the console and which can only `SHOW`.
  - *Kept in step.* `db_poolers` keeps a digest of the files last written (`config_sha`). `poolerKeeper`
    compares it with the files the connection's current credentials make every twenty seconds and writes
    them again when they differ, which covers a password changed through the adopt that re-seals it, an
    edit of the connection and an account reset alike; `GET …/pooler` reports `inSync`.
  - *Where it is published.* On loopback, always, when it is made or made again and whatever the
    database's exposure is. Only `PUT …/access` moves it: whenever that route is called, the pooler is
    brought to the address it names (every interface or loopback), whether or not the database itself had
    to move, the firewall rule for its port is opened or closed by the same comment-marked rule, and both
    are recorded in `database.access.change` and returned as `pooler`. That is the one road by which it
    leaves loopback, so it fails closed: a move to loopback that cannot be made removes the container and
    closes the rule, and the reply says so (`removed`); one to anywhere that cannot be made leaves the
    pooler where it was. `PUT …/pooler` keeps whatever address the container has and never widens it.
    One request at a time changes a connection's pooler (`lockPooler`), the keeper included.
    A database container has one pooler: a second connection to it, or a container or network of the name that
    is not this connection's, is refused (`409`) before the database or Docker is written to. Its name is `db-<id>-pool.jd.internal`, joined
    to a linked environment's network by the same binding pass that joins the database
    (`attachPooler` in `deployment_database_network.go`), and `${{database.<id>.pooled}}` — with the same
    shape suffixes as the plain reference — resolves to that name and port 6432 (or to the loopback port
    for a host-network workload).
  - *Lifecycle.* `POST /power` start and stop carry the pooler with the database (`pooler` in the reply and
    the audit entry); a restart leaves it, since PgBouncer reconnects by itself. Forgetting the connection,
    dropping its container and `DELETE …/pooler` remove the container, the network (only if it carries the
    connection's mark) and the firewall rule, and keep the database.
    On a protected connection every change to the pooler is refused: one mode writes to the database.
  - `GET …/pooler/stats` answers `SHOW VERSION`, `SHOW POOLS` and `SHOW STATS` from the console on the
    published port (`dbx.ReadPooler`); the `pgbouncer` pseudo-database is left out. `GET /{id}/url?pooled=1`
    is the connection string through the pooler for the same `target`s, audited as a reveal like the direct
    one. `TestLivePooler` (`JD_POOLER_LIVE=1`) runs the whole of it against a PostgreSQL and a PgBouncer
    container of its own.
- **What its container may use.** `GET /databases/{id}/resources` (read surface) reads the limits off the
  container behind the connection — found as the summary finds it, stopped ones included — as
  `limits.memoryBytes` and `limits.cpus` (zero for none), with `usedBytes` (one stats read, while it runs),
  `oomKilled` (the kernel stopped it last time for going past its limit), this machine as Docker sees it
  (`host`, `dockerx.HostCapacity`) and, from a server that is not known to be down, the engine's own
  buffer: `buffer: {setting, bytes}` from `dbx.SQLMemoryBuffer`, `RedisMemoryBuffer` or
  `MongoMemoryBuffer` — `shared_buffers`, `innodb_buffer_pool_size`, `max_server_memory_usage`,
  `maxmemory` (zero where it is not set) or the WiredTiger cache — named by the `memoryBuffer`
  capability, a word and `false` for SQL Server, Oracle, SQLite and the forks whose memory is something
  else's (CockroachDB, YugabyteDB, TiDB, FerretDB); `bufferError` where a server that runs did not
  answer. `editable` is a container that is the engine's own (not one found only by its port with
  another image) and that compose does not own, because compose would put its file's limits back on the
  next recreate; anything else — compose, another machine, a systemd unit, nothing found — carries a
  `refusal` sentence instead. `PUT /databases/{id}/resources` (`system.admin`) takes `memoryMb` and
  `cpus`, either left out to keep it and zero to lift it, held to Docker's floor (6 MiB, 0.01 of a core)
  and to the machine (`dbLimitsProblem`), and refuses a memory limit under what a running container uses
  now, which the kernel would meet by killing the engine. A new or changed limit is `docker update`
  (`dockerx.UpdateResources`): in place, nothing restarted, a stopped container keeping it for its next
  start. Docker cannot take a limit away, so lifting one replaces the container through `dockerx.Recreate`
  with the same configuration and no limit — the access route's park-and-restore path, on the same named
  volume, the connection's pool closed and its reading dropped, `inFlight` held as a `restart` until it
  answers — and is refused (`409 container_stopped`) for a container that is not running, since a
  replacement is started. That is the destructive kind of the request, decided from what the container
  holds, so the handler asks for `destructive` and spends `destrLim` by hand. Audited as
  `database.resources.change` with the container, the limits before and after, `replaced`, what was
  lifted and Docker's warnings. The limits live in the container's configuration and nowhere else: every
  replacement this dashboard makes (a change of exposure, the Docker page's Recreate) reads it back
  through `SpecOf`, so it keeps them, and a copy in the store would be a second answer the first change
  on the Docker page made wrong. The page is Settings → Resources (`ops/settings-resources.tsx`): the two
  limits as the Runtime page's `LimitField`, each with its peak in the last hour and what the machine has,
  the buffer beside the memory limit — with what a limit under it does, said as a fact — and one save bar;
  a draft that lifts a limit says it replaces the container and Save asks first, with the container named.
- **A server the dashboard started can speak TLS, with a certificate from the dashboard's own
  authority** (`database_tls.go`, `database_tls_engines.go`, `database_tls_renew.go`, `dbtls`).
  The authority is made the first time it is needed (`db_tls_ca`): an ECDSA P-256 certificate lasting
  ten years, whose key is sealed with `JD_MASTER_KEY` through `auth.Sealer` like every stored secret
  and is never logged, returned or written anywhere else. Its certificate is public: `GET
  /databases/tls/authority` (any role) downloads it as `just-dashboard-database-ca.pem`, and a copy lies
  at `<JD_DATA_DIR>/tls/ca.pem`, which the dashboard's own saved connections verify against and which
  start-up writes again if the data directory was restored without it. `PUT /databases/{id}/tls`
  (`system.admin` + `destructive`, audited `database.tls.change`) takes `{enabled, require}`. It
  signs a one-year server certificate whose names are loopback (`127.0.0.1`, `::1`, `localhost`), the
  internal name `db-N.jd.internal`, the container's name when that is a valid host name, and the address the
  port is published on when the exposure switch has made it `public` or `private`. The certificate and
  key go into a volume of their own, `<container>-tls`, labelled `jd.tls-of=<connection id>` when the
  dashboard makes it (a volume of that name without the label is somebody else's and is refused, and only
  a labelled one is ever removed, by the source the container actually mounts), mounted at `/jd-tls`, written into the new
  container before it starts (`dockerx.RecreateOptions.BeforeStart`) so the first start already finds
  them. The container is recreated, as the access change recreates it, with a start command that
  keeps the original and adds the engine's TLS arguments after it (`ssl=on` for PostgreSQL,
  `--ssl-*` for MySQL and MariaDB, `--tlsMode` for MongoDB), or, for Redis and Valkey, the start script
  with `tls-port` in place of `port`. **Require TLS** is `hostnossl … reject` in a `pg_hba.conf` the
  volume holds (PostgreSQL has no setting for it), `--require-secure-transport` (MySQL, and MariaDB,
  which has had it since 10.5.2, so every release the template offers), and `requireTLS` (MongoDB). A Redis port speaks one protocol, so
  for Redis and Valkey TLS on is TLS only; the published port, the connection's address and the exposure
  switch keep their meaning. Only a server started from one of the dashboard's templates is changed: a
  compose-owned container, one whose start command or entrypoint was changed, and KeyDB, Dragonfly,
  ClickHouse and the forks that cannot be provisioned are refused with the reason, and `serverTLS` in
  the capability table is the same list. The saved connection is rewritten to verify the certificate
  (`dbx.WithServerTLS`: `sslmode=verify-full&sslrootcert=` for PostgreSQL, `tls=true&tlsCAFile=` for
  MongoDB, `rediss://` with `tlsCAFile=` for Redis, and `tlsCAFile=` for MySQL, which `NormaliseDSN`
  turns into a TLS configuration the driver knows), only once the server has answered with it; if it
  does not within ninety seconds, the container is put back as it was and the volume removed
  (`502 tls_failed`, whose message says what actually happened: if putting it back failed too, it says
  the database may be unreachable and where to look). Turning TLS off recreates the container with the original command and removes
  the volume. **What a linked application is given.** A program in another container (or on the host's network)
  has no copy of the authority and no file at the dashboard's path, so the URL routes — `target=container`,
  `target=host`, `target=public` and the string a host-network deployment resolves — go through
  `applicationDSN` and `dbx.ForApplication`, never the saved string. While TLS is only *allowed* the
  application gets the string it had before TLS and still connects without it. Once TLS is *required* it gets
  a string that uses TLS without checking the certificate where the driver has a word for that (PostgreSQL
  `sslmode=require`, MongoDB `tls=true&tlsInsecure=true`, MySQL `tls=skip-verify`); a Redis URL has none, so
  it is `rediss://` and the application needs the authority's certificate. The traffic stays on the
  internal network, and the Network settings say plainly that linked applications encrypt but do not
  verify the certificate unless they are given the CA, which downloads there (`applications` in `GET
  /databases/{id}/tls`: `plain`, `unverified`, `needsCA`). `GET
  /databases/{id}/tls` (`system.admin`) reports the state, the certificate's names, serial and dates, how
  many deployment environments reach the database by its internal name (they stop connecting once TLS is
  required unless they use it), and the client connection string and command for another machine, with the
  right `sslmode` or its engine's equivalent and a `<password>` placeholder. **Renewal**: a certificate is
  renewed thirty days before it ends, and when the names it carries no longer match the addresses
  the server is reached at, by a worker that looks every six hours (`startDatabaseTLS`); `POST
  /databases/{id}/tls/renew` (`system.admin` + `destructive`, because it can restart the container) does
  it on request. A renewal writes the new files beside the pair
  the server reads, swaps them in with one command (a failure part way leaves the old pair), and asks the
  running server to read them (`pg_reload_conf()`, `ALTER INSTANCE RELOAD TLS`, `FLUSH
  SSL`, `rotateCertificates`, `CONFIG SET tls-cert-file`), run inside the container with the
  password already in its environment. It then connects to the server and compares the certificate it
  presents with the one just issued (`dbx.ServedCertificate`: the engine's own switch to TLS, then the
  handshake, verified against the authority), because `pg_reload_conf()` returns true when PostgreSQL kept
  the old one; where the server declines or kept it, **the container is restarted** (always, on a Redis
  that cannot take the settings at once), the detail says so, and the row is saved only once the new
  certificate is the one presented. Every renewal is audited as `database.tls.renew`, by the requesting account or by actor
  `system`, with the reason (`requested`, `expiry`, `names`), the old and new serial and the
  new end date. TLS changes sit on the protected-connection allowlist beside the access change: they
  alter how the server is reached, not what it holds. A TLS change and an access change take the same lock,
  since each recreates the container, and the access change re-issues the certificate only when the names it
  should carry have changed. A connection whose server speaks TLS is not forgotten, or dropped with its
  database, until TLS is turned off (`409 tls_on`): the record of the certificate goes with the connection, and
  the server would keep a certificate nothing renews.
  **The authority has no rotation path, deliberately.** It lasts ten years. A certificate is never issued to
  outlive it, so in the last year its servers' certificates end where it does, the schedule stops renewing
  such a certificate (a renewal could not lengthen it), and after the authority ends no certificate can be
  issued and the verifying connections stop connecting. What to do then is to turn TLS off on each database;
  replacing the authority is not built. `TestLiveDatabaseServerTLS`
  (`JD_DB_TLS_LIVE=1`, optionally `JD_DB_TLS_ENGINES=postgres,redis`) switches a real server of each engine
  on, requires TLS, checks the certificate on the wire against the recorded serial, renews it by request and by the schedule without a restart, checks the string a linked application is given in both
  modes, and switches TLS off, in containers of its own; MariaDB is run on 11.4 and on 10.11.
- **A database the operator can remove.** `DELETE /databases/{id}/database` needed `DropDatabaseSQL` and
  `AdminDatabase` (Postgres and SQL Server refuse to drop the database the session is inside) and has no
  verb at all on two engines — a SQLite database is a file to unlink, a Redis keyspace can only be
  emptied, which `DropResult.Gone` reports rather than pretending. The connection is deleted with the
  database when it was that connection's own. A managed deployment network binding blocks forgetting the
  connection or dropping its database before data is changed; remove that network through deployment
  lifecycle first. The same route takes `removeContainer: true`, which removes the Docker container
  behind the connection (found by its published port at the saved loopback address) and the named volumes
  it mounted, without signing in — the delete that still works when the stored password no longer
  matches, which is what a container started over an older data volume produces. A compose-owned
  container is refused (`compose_managed`); a volume another container uses is kept by Docker and
  reported as a warning, never forced.
- **The section opens on every database at once.** `GET /databases/fleet` dials every saved
  connection concurrently (six at a time, twelve seconds each) and hands back the row's facts with
  what the server answered: reachable, version, flavour, latency, the database's size where the
  engine reports one, its tables/collections/keys, sessions less this dashboard's own (on PostgreSQL
  every session that connected under `dbx.ApplicationName`, on other engines the asking pool), where it
  runs (`docker`, `host`, `remote`, `file` — read through the same placement the connection's own
  summary uses, so the two agree), its `state`, its exposure, how many deployment environments are
  bound to it, and when it was last backed up (`lastBackup`, below under *The dumps on disk*). Each
  entry also carries the summary's own `power`
  (which of start, stop and restart the power route would take now, or the reason for none),
  `managed` (whether the access route can change the port's reach) and `inFlight`, read off the
  placement the entry already needed — no second look at the machine and no dial — so a card
  offers what the connection's page offers without working either out again. A SQLite connection's
  `bytes` is its file's size, to every role: its catalogue gives none, and the figure was known only
  to whoever may read the inventory. A server whose container or unit is down is `stopped`
  or `paused` and is not dialled; a row that cannot be opened is `broken` and listed all the same. A
  dial's result is kept for ten seconds per connection (`fleetReadingFor`), under a lock held across
  the dial, so several pages polling at once cost one connection rather than one each; a restart or
  replacement of the container drops it. For an administrator it also lists what the sync would report
  and could not connect — a container with no reachable port, a native server waiting for a
  password — read from the inventory's kept reading by the sync's rules (`undetectedServers`),
  without dialling or writing anything: a stopped server, one the operator set aside and one
  recognised only by its port are left out, as the sync leaves them alone. That reading is the last
  one taken, however old (`keptInventory`): the fleet never reads the machine itself, because the
  Databases page draws nothing until the fleet answers, and reading it again — every container, every
  process's sockets, the systemd units, and the first walk for files — took the fleet from a fifth of a
  second to between one and three. A reading that is missing or older than `dbInventoryFresh` is taken
  again in the background for the next caller; before the first one exists both lists are empty, and
  the inventory route the page reads beside the fleet is the fuller account of the same servers.
- **What feeds on what.** `GET /databases/topology` (and `/{id}/consumers` for one connection) joins
  four sources into nodes and edges: `deploy_database_bindings` with the
  project and environment names, running containers whose environment names the server (one
  concurrent inspect pass, eight at a time), compose-stack siblings and shared user networks (drawn
  dashed, as a link that could carry), and the engine's own session list with client addresses mapped
  back to containers by IP, to this host for loopback and bridge-gateway addresses, or to another
  machine. A container that is a deployment's release (`io.just-dashboard.managed` and
  `io.just-dashboard.environment-id` on a live environment) is drawn as that deployment — the
  project, and the environment where it is not production — whether the environment or a session
  revealed it, never as `jd-e<id>-r<n>` — and carries the project's id (`projectId`), which is
  what the frontend draws its website's icon by. What "names the server" means is an address book
  (`topoAddresses`): the `db-N.jd.internal` alias, the address the connection was saved with, the
  container's name, compose service and own addresses with the engine's port, and — for a server
  saved at loopback or a container that publishes its port — every address of this machine
  (`hostAddresses`: loopback, `host.docker.internal`, Docker's default gateway, the hostname, every
  interface's address) with the published port, because an application rarely reaches a database the
  way the dashboard does: a deployment holding `postgres://…@<public address>:5435/app` uses the
  container the dashboard dials at `127.0.0.1:5435`, and was drawn as reading nothing. An address
  counts only whole (`containsAddress`: `10.0.0.3:5432` is not in `110.0.0.3:54321`), and a host and
  a port in two variables of their own (`DB_HOST`, `DB_PORT`) count together. The dashboard's own
  sessions are never a reader: every PostgreSQL connection it opens names it
  (`application_name=just-dashboard`, added by the dialect's `NormaliseDSN` to a string that names no
  application of its own), and a session under that name is left out whichever saved connection's
  pool holds it. Through a published port every client on this machine arrives from the bridge
  gateway, so by address alone the dashboard was drawn as *This server* reading every database it
  watched. On other engines the asking pool's own connections are left out as before. A binding's word
  (`connected`, `stale`, `broken`) outranks an observation's. Both are on the read surface; the
  detected-but-unconnected half is filled only for `system.admin`.
  Fleet, topology and consumer reads reuse one Docker list and one inspection per container within
  their request, including the fleet's administrator-only discovery. Each request starts fresh; the
  snapshot is never used for access-changing actions.
- **A failed dial never quotes the password.** What a failed dial said goes out through `connectError`,
  which takes the connection's password out of it: a driver reports a connection string it could not use
  by quoting it. That holds for every route that opens a connection of its own — the Redis and MongoDB
  pages, the export and the import, a dump, a restore and a copy, the role and privilege routes,
  maintenance, the drop, the probe every connect path runs (`connectFailed`, `withoutSecrets`) — and
  `TestNoRouteQuotesAConnectionStringsPassword` asks every route under a connection, with a string its
  driver refuses, and looks for the password in the answer, the audit trail, the log and the jobs it
  started. It asks each route twice: with `{}`, and with a request written to pass that route's
  validation (`dialingRequests`), because a route that reads its body before it dials is never put to the
  test by an empty one — the privilege routes quoted a password for exactly that reason. The test lifts
  the request budgets (`apiLim`, `destrLim`): several hundred requests from one account in a second were
  being turned away at the door after the first hundred and twenty, and a `429` proves nothing. `GET
  /{id}/url`, whose purpose is to hand the string to an administrator, is the one answer exempt.
- **A failure is marked worth trying again only where it is** (`retryable` in the error envelope, which
  is what the page's error state offers its retry on). `dbx.Unreachable` tells a server that was not
  there to answer — refused, timed out, gone mid-reply, still starting (PostgreSQL's `57P03`, MySQL's
  1040 and 1053, SQL Server's 18401, Redis's `LOADING`, MongoDB's shutdown and step-down codes, Oracle's
  `ORA-01033` and the listener's) — from one that answered no: a wrong password, an unknown database, a
  privilege the account lacks, a connection string nobody could parse. It reads each driver's own error
  type, and a transport error's, before it reads any words. `connectFailed` marks the first kind for
  every route, since nothing has been asked of a server that could not be opened; `queryFailed` marks it
  for a read (`502 query_failed`), Redis's and MongoDB's reads included (`redisFail`, `mongoReadFailed`).
  A write is never marked: its reply may be what was lost.
  `TestLiveAReadWhoseServerWentAwayIsWorthAskingAgain` takes each engine's server away behind an open
  pool, and `TestLiveAWrongPasswordIsNotWorthAskingAgain` has each refuse a login.
- **Redis is read in the database its connection string names.** A Redis connection is pinged, tested,
  summarised and read for the fleet in the logical database its connection string names
  (`dbx.RedisDSNDatabase`), which is where every key route goes: a string naming a database the server
  does not have used to test healthy and then fail each of them.
- **Reads that leave the server leave a trail.** `GET /databases/{id}/url`, `/search` and
  `/backup/download` are written to the audit log with `recordRead`, as `database.connection.reveal`,
  `database.search` and `database.backup.download`, before the read runs. They used to call
  `SetAudit`, which does nothing on a GET. `recordRead` writes on a context that outlives the request,
  so a client that drops the connection does not take the entry with it. `GET /databases/{id}/export`
  writes its own two entries through `recordTransfer`, which records on a context of its own for the
  same reason: `database.export` before the first row leaves, and `database.export.finished` with the
  rows sent and whether it was cut short, recorded as a failure (`502`, with the error) when the
  stream broke.
- **One pool per connection, opened once.** Pool initialization is coordinated per connection ID. Dialing
  and pinging do not hold the manager's map lock, and waiters can cancel independently. Closing or
  editing a connection invalidates an initialization already in progress; its old credentials cannot
  publish a pool afterwards.

### Protected connections

A dashboard that can edit a row can edit the wrong row, and the connection it happens on is always
the one that mattered. Marking a connection protected is one field of its record (`readOnly` on
`PUT /databases/{id}`, `read_only` in the store); taking the mark off is the same request. The rule
itself is stated in [request lifecycle](../architecture/request-lifecycle.md#protected-connections);
this is how it is built, and the Protected column of [Routes](#routes) gives every route its answer.

- **One middleware, and an allowlist.** With `read_only` set, one middleware in front of every database
  route (`protectReadOnlyConnections`, `handlers_db_protect.go`) refuses each request under
  `/databases/{id}` that is not a `GET`, `HEAD` or `OPTIONS` with `409 connection_read_only`, for every
  role, unless its route is on the list in that file (`protectedRoutesAllowed`). It is an allowlist so
  that a mutating route added later is refused until somebody decides otherwise, and
  `TestProtectionCoversEveryMutatingRoute` walks the real router against a second list written out by
  hand that gives every such route its answer (`protectedRouteVerdicts`); a route on neither list fails
  the test and is refused all the same.
  - *Always let through*, because they do not change the database: the connection's own record,
    power and reachability; taking a dump, adding one made elsewhere and deleting one, which are
    files on this machine (`/backup`, `/backups/upload`, `DELETE /backups`); a major-version upgrade's
    check, which only reads, and finishing one, which removes the old server the connection no longer
    reaches (`/upgrade/check`, `/upgrade/complete`); saved queries and the
    diagram's arrangement; the POSTs that only read (`/classify`, `/orm`, `/rows/sql`,
    `/explain/index-candidates`, `/explain/what-if`, `/redis/classify`, MongoDB's find, count, document, explain, aggregate preview, schema,
    validation check and command classify); and stopping work in flight, which changes no data and
    is what the operator of a production server most needs to be able to do (`/query/cancel`,
    `/activity/cancel`, `/activity/kill`, `/redis/clients/kill`, `/mongo/killop`).
  - *By content.* `/query`, `/script`, `/explain` with `analyze`, `/export/query` and `/rls/test` (a
    SELECT run as another role in a transaction that is rolled back) are let through
    when every statement classifies as a read by the rules of the connection's own engine
    (`dbx.ClassifyFor`: what one engine takes for a string another takes for the end of one, and
    `SELECT … INTO`, a `WITH` that leads into a `MERGE` or an `INSERT`, and `INTO OUTFILE` are not
    reads). `/changes` when it is a dry run, which renders the statements and runs none.
    `/maintenance` when the action is one the engine's list marks `readOnly`, a consistency check.
    `/redis/command` when the dashboard's own table calls the command a read — the handler asks
    again once the server has said what the command is there — and `/keys/bulk` when it is a dry
    run. `/aggregate` and `/mongo/command` when MongoDB's own classifier calls the pipeline or the
    command a read, and `PATCH`/`DELETE /mongo/documents` when they only count. Each check judges
    the body as the handler's decoder will read it: either through `protectedFields`, which folds
    names as encoding/json does and refuses a field named twice or with a letter outside ASCII and
    any field it does not recognise that could hold a statement, or by decoding into the handler's
    own request type with the handler's own decoder (`protectedBody`).
  - *Shown, not run.* A schema form asked for its statement (`/ddl/*?preview=1`) is let through:
    `runDDL` answers a preview before it executes. The flag is in the address, so it is asked in the
    middleware itself and not by a check of the body; no other route is opened by it.
  - *Refused*: rows, structure, imports, an import from another server, a restore, a copy, the drop,
    a major-version upgrade and its revert, roles, privileges, settings, extensions, keys, streams, ACL
    users, documents, collections, indexes, validation rules, the profiler, a statistics reset,
    row-level security policies and their switches, and the editor's assistant, which changes nothing
    but would send the connection's structure to a model.
  It guards the dashboard's own controls and is as strong as the classification — a `SELECT` that
  calls a function of the operator's own which writes passes (the built-in ones that run SQL or outlive
  the rollback are not reads, see [the query runner](#the-query-runner)); it is not a sandbox around the
  server. Restoring a backup
  run into a connection goes through `/backups`, outside `/databases/{id}`, and is held to the same
  answer: `handleBackupRestoreDatabase` refuses a protected connection with the same `409`, and the
  adapter behind it (`backupDatabaseDumper.RestoreDatabase`) refuses again whoever calls it.
  - *Protection follows the server* where a request reaches past the connection it was addressed
    to. The mark is on one saved connection, but an account is the server's and a restore or a drop
    can name another database, so the handlers that do either ask
    `protectedNeighbour` (`handlers_db_protect.go`) first — every protected saved connection to the
    same engine at the same address, compared as an identity (`localhost` is `127.0.0.1`):
    - *Its account* is not changed through anything else. `POST /databases/host/grant` refuses to
      reset the account a protected connection signs in with **whichever database the request
      names** (`refuseGrantOnProtected`; it used to match the database too, so a request naming
      another one, or none, reset the account and made it a superuser). `PUT` and `DELETE
      …/server/roles/{name}`, MongoDB's `PUT /mongo/users`, `POST /mongo/users/revoke` and `DELETE
      /mongo/users`, and Redis's `PUT`/`DELETE /redis/acl/{name}` refuse the same account through a
      neighbouring connection (`refuseNeighbourAccount`), before anything is dialled. A Redis
      connection that names no user signs in as `default`.
    - *Its database* is not replaced or removed through a neighbour. `POST /{id}/restore` with
      `database`, `DELETE /{id}/database` with `database`, and `POST
      /backups/runs/{runID}/restore-database` with `database` are refused with `409
      connection_read_only` when a protected connection is on that database
      (`refuseDatabaseOfProtected`); `removeContainer`, a major-version upgrade and its revert are
      refused while a protected connection is on any database of that server
      (`refuseServerOfProtected`). A Redis archive restored with no
      database named writes into the numbered databases its header lists, not into the
      connection's own, so those are the ones asked about (`restoreDestinations`,
      `dbx.RedisArchiveDatabases`) — and the ones the job reports as `database` (`"8"`, `"0,3"`).
    - *Not followed*: a row whose connection string no longer opens has no address to compare, and
      a SQLite file has no neighbours. Two saved connections to the **same** database, one
      protected and one not, are two decisions by the operator: the unprotected one's own routes
      work on it. Granting, creating a database and settings through a neighbour are not held
      back; they take nothing from the protected connection.
    `TestAProtectedDatabaseIsNotReplacedOrDroppedThroughANeighbour`,
    `TestARedisRestoreIsHeldToTheDatabasesItsArchiveNames` and
    `TestAProtectedConnectionsAccountIsNotChangedThroughANeighbour` hold each route to it.

### The query runner

Reading is separated from running: `dbx` decides destructiveness by the rules of the connection's engine
and fails closed (`ParseScript` gives every statement its `Risk`; `ClassifyFor` is the strongest of them
over a whole text), and the handler applies capability and budget by hand (`authoriseSQL`, shared by the
query, script and analysed-plan routes). Every dialect's `ExplainPlan` must describe a statement *without
executing it* — asserted in the interface, proved by `TestLiveExplainDoesNotExecute`.

The query runner reads SQL with one lexer per engine (`dbx/sqltoken.go`), and splitting, the leading
word, the row-returning test and the plan gate all read its tokens — there used to be a splitter and a
separate comment stripper, and text one called a comment the other called code. Each engine is read its
own way (a backtick is a quote on MySQL and an operator on PostgreSQL; `#` is a comment on MySQL, a JSON
operator on PostgreSQL, part of a name on SQL Server and Oracle; PostgreSQL's `$tag$…$tag$` bodies are
read by PostgreSQL's own rule). What depends on something the text does not say is refused: a quote an
odd run of backslashes would escape, an executable or nested comment, a bare carriage return inside a
line comment, a NUL, `--x` on MySQL, Oracle's `q'[…]'`, a `$` or a typographic quote on ClickHouse. A
refusal classifies as destructive. A statement is sent from its first token to its last, so the
comments around it never reach the engine.

Where a statement ends is each engine's own reading too, made the way its client reads a script
(`dbx/statement_split.go`), because the body of a stored program holds semicolons that end nothing.
Splitting at every semicolon used to cut a MySQL procedure, a SQLite trigger, a PL/SQL block or a
T-SQL batch into pieces no engine accepts.
- *PostgreSQL*, as psql: a semicolon inside parentheses, or inside the `BEGIN … END` of a `CREATE
  FUNCTION` or `PROCEDURE` written in SQL (`BEGIN ATOMIC`), ends nothing; a dollar-quoted body is one
  token already.
- *MySQL and MariaDB*, as the mysql client: `DELIMITER`, where a statement would begin, names what
  ends one from the next line on (`END$$` ends at its `$$`), and while that is not `;` a semicolon
  ends nothing. With the plain `;` the blocks of a stored program's body are followed — `BEGIN … END`,
  `IF … END IF`, `CASE`, `LOOP`, `WHILE`, `REPEAT`, a handler's `BEGIN` — so a procedure written the
  way `SHOW CREATE PROCEDURE` prints it, with no `DELIMITER`, is one statement, and so is MariaDB's
  `BEGIN NOT ATOMIC`. `BEGIN` on its own is still a transaction, `IF(…)` a function and `IF [NOT]
  EXISTS` DDL's.
- *SQLite*: `CREATE TRIGGER … BEGIN … END`.
- *SQL Server*, as sqlcmd: `GO` alone on a line ends a batch and `GO n` runs it `n` times, so its
  statements are in the script as many times as they run, inside the 500-statement bound. A `BEGIN …
  END` block (not `BEGIN TRAN`), `TRY` and `CATCH`, and `IF … ; ELSE …` are one statement each, and the
  body of a `CREATE`/`ALTER PROCEDURE`, `FUNCTION` or `TRIGGER` runs to the end of its batch, or to the
  `;` after its outermost `BEGIN … END`.
- *Oracle*, as SQL*Plus: a PL/SQL unit — a `DECLARE` or `BEGIN` block, `<<label>>`, a query that
  declares functions of its own (`WITH FUNCTION`), a `CREATE PROCEDURE`, `FUNCTION`, `PACKAGE [BODY]`,
  `TRIGGER` or `TYPE` — runs to a line holding only `/`, or to the end of the text, and is sent with
  the semicolon after its `END`, which PL/SQL needs. A `/` line ends a plain statement too, and a `/`
  after a statement that already ended is not a second run of it.

A separator that cannot be read is refused like any other text: `DELIMITER` naming nothing, or a
delimiter that is not punctuation (a quote, a backslash, a letter, a comment's opener), a `GO` count
below one or past the bound. The editor reads a script the same way, rule for rule (`readScript` in
`query/sql-text.ts`), to pick the statement under the cursor, mark it and send it, and both readings
are held to one file of scripts (`dbx/testdata/statement-splits.json`, read by
`TestScriptsSplitWhereTheirEngineReadsThem` and by `sql-text.test.js`): the statement Run sends from
the cursor is one the server reads as one. Run everything sends the client's separators with the
statements — the `DELIMITER` lines, the count of a last `GO` — and a statement sent on its own from a
batch `GO` repeats runs once. The strip beside Run names the lines the statement under the cursor runs
over, and says so when its separator did not end it: a PL/SQL unit with no `/` line after it runs to
the end of the text, and a SQL Server routine that went on past its `BEGIN … END` takes in what
follows, up to the next `GO`. Format lays out the plain statements of such a script and leaves its
separators and its bodies as written, line breaks included.

Below "high" a statement must be one whose leading verb is fully known: a read, an `INSERT`, or a
`CREATE` of a table, index, view, schema, sequence or type — what the row and schema forms offer at
the same capability. Merely containing `INTO`, `CREATE` or `INSERT` buys nothing: `VACUUM INTO`,
`LOAD DATA … INTO TABLE`, `CREATE DATABASE`, `CREATE EXTENSION` and ClickHouse's `INSERT INTO
FUNCTION` are high. On PostgreSQL a `U&"…"` identifier is high as well, since it can name a server
function without spelling it. The Redis console applies the same rule to options: `XADD` is judged up
to its entry id whatever options precede it, `HSETEX`/`HGETEX` wherever their expiry stands, and an
expiry that cannot be read as a number is dangerous. A dump's database name may not contain `=` or
begin with `postgres://`, which `pg_dump` would read as a connection string of its own; a connection
string written as pairs (`host=… password=…`, `server=…;password=…`) is parsed as pairs, never
passed through as a name.

Classification is per statement and keeps the strongest verdict. The shape (leading word, `WHERE`,
`RETURNING`) comes from the tokens; the *verbs* are looked for in the statement's raw text, quoted
text and comments included, so no disagreement about where a quote ends can put one out of sight —
it over-reports instead (`SELECT 'delete'` is destructive), which is the direction it may be wrong in.
A dollar-quoted body is at least `high`, and so is any statement that holds statements of its own —
a stored program's definition, a block, whatever was cut as one piece across a semicolon — whatever
word leads it: `WITH FUNCTION` leads with a query and can write through an autonomous transaction, and
the one place this package and the engine might disagree about where a statement ends must not be a
way past the verdict of any piece of it. A protected connection refuses all of them, and neither kind
is taken for a plan. Inside a body each `DELETE` and `UPDATE` is scoped by its own statement's
`WHERE`, not by another's. SQL Server needs no separator between statements, so there
any batch keyword anywhere (`EXEC`, `SET`, `BEGIN`…) ends a statement's claim to be a read. What
replaces rather than adds (`REPLACE INTO`, `… OR REPLACE`), what creates an account, and a PostgreSQL
function that acts on the server (`pg_terminate_backend`, `lo_export`, `set_config`…) are `high` too: the
console must not be a cheaper way to do what the kill and drop routes charge for.

A `SELECT` is not a read when what it calls runs other SQL or acts past the transaction it runs in
(`callRules`, `dbx/statement_calls.go`). Calls are found in the raw text like the verbs, by exact name or
prefix, quoted or not. A function handed SQL as text is never a read, whatever it is given: the text can
be built by concatenation (`'select pg_terminate_'||'backend(…)'`), so nothing that reads the statement
knows what it runs, and such a function is the only way to put a function's name together from pieces.
On PostgreSQL that is the `query_to_xml`, `cursor_to_xml` and `table_to_xml` families, `ts_stat`,
`ts_rewrite`, `crosstab`, `connectby` and `xpath_table` (the last two write their arguments into a query
unquoted), and anything named `dblink…` or `pg_background…`; on Oracle `DBMS_XMLGEN`, `DBMS_XMLQUERY`,
`DBMS_XMLSTORE`, `DBMS_SQL` and `EXECUTE IMMEDIATE`. The rest outlive the rollback a read ends in, on a
pooled connection that the next request is handed (found live: an advisory lock and a `dblink`
connection are both still there): PostgreSQL's advisory locks, statistics resets (`pg_…_reset…`, not the
`pg_stat_get_…` that say when one happened), and its replication-origin, slot, WAL-replay and
`heap_force_…` functions; MySQL's and MariaDB's named locks (`GET_LOCK`, the locking service) and a `:=`
assignment; a sequence's next value on every engine (`nextval`, `setval`, `NEXT VALUE FOR`, Oracle's
`.NEXTVAL`), which SQL Server's rolled-back transaction and Oracle's query-only scope do not give back;
and Oracle's packages that reach outside the database or leave state behind (`UTL_HTTP`, `UTL_INADDR`,
`HTTPURITYPE`, `DBMS_PIPE`, `DBMS_LOCK`…). Each is `high`, with the call named in its reason. What runs a
string at the statement level — MySQL's `PREPARE`/`EXECUTE`, a `sys.` procedure through `CALL`, SQL
Server's `EXEC(@sql)` and `sp_executesql`, an Oracle block — does not lead with a read and was never one.

A statement classified `read` runs inside the engine's own read-only scope (`dbx/run.go`), so a wrong
verdict fails instead of writing: a read-only transaction on PostgreSQL, a read-only *session* on
MySQL/MariaDB (`START TRANSACTION READ ONLY` lets DDL through — it commits implicitly first),
`query_only` on SQLite, `readonly=2` sent with the query on ClickHouse, and on SQL Server — which has
none — a transaction that is always rolled back, so a write there succeeds and is undone rather than
refused. Oracle's read-only transaction is not used: a schema change commits the moment it runs and
ends the transaction that was meant to refuse it (found live — `CREATE TABLE` and `DROP TABLE` both went
through), and a table created or altered a few seconds ago cannot be read inside one at all
(ORA-01466), which failed the last line of every script that makes a table and reads it. What Oracle
does guarantee is that a query changes no data (DML inside one is ORA-14551), so its scope is that
nothing but a query is sent: `beginsAsQuery` reads the characters of the text itself — not the lexer's
or the classifier's reading of it, which is what the scope exists to distrust — and anything that does
not begin with `SELECT` or `WITH` is refused before it reaches the engine, inside a transaction that is
rolled back. A ClickHouse account may refuse the setting for two opposite reasons, and the refusals
read alike, so the server is asked which (`clickhouseReadScope`): an account it already holds to reads
runs under that limit, and one whose profile pins `readonly` at writable has no scope, so a statement
called a read is not run on it at all. The scope each engine gives is the capability `readOnlyScope`
(`enforced`, or `rollback` on SQL Server).

A block that runs as it is sent — a PL/SQL block, T-SQL's `BEGIN … END`, `IF` and `WHILE`, MariaDB's
compound statements — manages a transaction by what is in it, not by its `BEGIN`: a script run as one
transaction refuses it only when it commits, rolls back or sets a savepoint itself. On SQL Server and
MariaDB it goes through the driver's query path, so the rows of the query in it are shown (the first
that returns any). Oracle keeps a stored program it could not compile, marked invalid, and answers the
`CREATE` with a warning go-ora does not pass on, so the runner reads `ALL_ERRORS` for that program on
the same connection straight after (`compileChecker`), and the statement fails with the compiler's
lines instead of the program failing wherever it is first called.

Anything that is not a read runs on a connection of its own that is closed afterwards rather than
pooled, because a pooled connection carries a `SET`, an open transaction or a temporary table into
somebody else's request. So is one whose statement was cancelled part-way: Oracle's driver leaves the
interruption behind for the next statement on that connection to receive.

`POST /databases/{id}/query` takes exactly one statement. `POST …/script` takes several: one
connection, in order, stop at the first error, optionally one transaction, one result per statement,
and the capability is decided by the worst statement in it. Both accept a client-chosen `queryId`
that `POST …/query/cancel` stops; the entry exists only while its request is open, and the statement
is stopped on the server (MySQL needs a `KILL QUERY` from a second connection; the other drivers
cancel on the wire). Every dialect validates explain input as exactly one supported statement before
adding its fixed plan syntax; user-supplied EXPLAIN/ANALYZE options never reach the connection.
`analyze: true` on `POST …/explain` executes the statement, so the handler asks of it what the query
route would, and a data-changing statement is measured inside a transaction that is rolled back.
MySQL from 8.3 gives a measured plan as JSON only in the second version of its JSON plan, so the
session is set to it first (`mysqlDialect.preparePlan`, `explain_json_format_version = 2`) and is
not returned to the pool, where it would change the shape of the next request's plain plan; an
older MySQL has no such variable and its own refusal of the combination is the answer.

SQL Server resets SHOWPLAN using a bounded cancellation-independent context, including after query
failure. An uncertain enable/reset outcome discards the connection instead of returning its mode to
the pool.

The Query page's Plan draws the answer as one tree, read in the browser
(`components/database/query/plan.ts`, the printed forms in `plan-text.ts`, both unit-tested):
PostgreSQL's JSON, MySQL's `query_block` and 8.3's measured JSON, MariaDB's `r_` measurements, SQL
Server's SHOWPLAN rows and SQLite's query plan, and three plans that arrive only as text — Oracle's
DBMS_XPLAN table (its conditions under the step they name, its notes under the statement), MySQL's
measured tree as an older MySQL prints it when the JSON form is refused, and ClickHouse's indented
steps. Any other print stays the engine's text. A step carries what its format has and nothing it
does not: cost, with the step's own share as its bar; rows; and in a measured plan its own time and
the time of it with everything under it, as one bar of two inks against the plan's total, with the
steps orderable slowest first. A measured step whose rows came out ten times more or fewer than
planned says so under its name — `est 12 · actual 48,210 (×4,000)`, both over the whole run.
Whether they are that far apart is decided a loop at a time with each side at least one row,
because PostgreSQL never plans under one row a loop and before 18 rounds what came to whole rows,
and fewer rows than planned is not said beneath a step that stops reading early (a limit, a merge
join's inputs, the inner input of a nested semi- or anti-join), since the planner plans a run to
its end. A MariaDB table's rows are those its condition kept (`rows × filtered` against
`r_rows × r_filtered`, what it read a fact beside them), so a misjudged condition shows where
comparing the rows read never would. These are statements of what the plan says, run on request,
not findings. `TestLivePlansAreTheShapesTheTreeReads` asks every live engine for the shapes these
readers rely on, so a server that changes one fails there rather than quietly drawing text.

**Index advice is a tool the reader runs from the Explain view, never a finding.** 0.7.5 removed the
Advisor and every automatic recommendation, and nothing here comes back as one: both actions are buttons
on the Plan view, nothing is asked until one is pressed, and nothing is kept (`dbx/index_advice*.go`,
`dbx/missing_indexes.go`, `api/handlers_db_index_advice.go`). The capability flags are `indexWhatIf`
(PostgreSQL, not CockroachDB or YugabyteDB, which have no HypoPG) and `missingIndexes` (SQL Server).
- *What if?* (PostgreSQL). `POST …/explain/index-candidates` takes the plan with `EXPLAIN (VERBOSE,
  FORMAT JSON)` — verbose, because only then is every column qualified with its alias and every relation
  named with its schema — and reads the conditions the plan prints (`Filter`, `Join Filter`, `Hash Cond`,
  `Merge Cond`, and a `Sort Key` under a `Limit`) with a small tokenizer, not a SQL parser
  (`index_advice_plan.go`). Only a plain column compared with a constant, or with another table's
  column, proposes anything; an `OR`, an expression, a function of the column, `LIKE` and `<>` propose
  nothing, because a wrong suggestion costs every later write. Per table the candidates are the filters
  (equality columns, then one range column, else the sort) and the join columns, at most three columns
  each. One is dropped when an existing valid, non-partial b-tree already leads with the same columns
  (`covered`), and a table that is a partition, a view or a foreign table, whose catalogue could not
  be read, or that is past the twelfth the request reads (`Detail`), is `skipped`: a partition's index
  belongs to the parent, which a plan of a SELECT never names, so a partitioned table gets no
  candidate. The page words an empty list from those two counts and claims nothing else. The plan
  and the HypoPG probe are read inside the engine's read-only scope. The answer also carries the
  state of HypoPG (`ready`, `not_created` or `not_installed`), which the page prints as a fact naming
  the extension, once; the candidates stay listed, and "Create the real index…" is offered for the
  selected one without a try, previewed like any index.
  `POST …/explain/what-if` takes the statement and a table with its columns, builds the `CREATE INDEX`
  with `PlanCreateIndex` (so every name is quoted by the dialect), and on **one connection that is
  discarded afterwards** plans the statement, calls `hypopg_create_index` (by the schema `pg_extension` says the extension lives in, quoted), and plans it again, inside the
  engine's read-only transaction. It answers both plans, their total costs, the name the plan uses for
  the hypothetical index, and whether the planner chose it. Nothing is executed (`EXPLAIN` without
  `ANALYZE`; a hypothetical index lives in that connection's memory), so both routes sit on the read
  side beside `/explain`, are let through on a protected connection, and are audited
  (`database.explain.index_candidates`, `database.explain.what_if`). Making the index real is not
  theirs: the page opens the schema change dialog on `POST /ddl/index` with `concurrently`, which
  previews the statement the server will run and needs `service.control`, as every index does.
- *Missing indexes* (SQL Server). `GET …/missing-indexes` reads `sys.dm_db_missing_index_groups`,
  `_group_stats` and `_details` for the current database, most valuable first (cost × impact × the
  plans that wanted it, 50 at most), with the `CREATE NONCLUSTERED INDEX … INCLUDE (…)` each entry
  implies, written from names parsed whole and quoted by the dialect, and when the server started,
  because a restart empties the views. A login without `VIEW SERVER STATE` is answered with
  `supported: false` and the engine's words. The page copies a statement or opens it in the editor
  for a role that may run SQL; it does not create the index itself.

What a run leaves behind is the dashboard's own: `GET …/history` is the connection's last hundred
statements with their risk, duration, row counts and the engine's error text (`db_query_history`), and
a saved query
(`GET`/`POST …/queries`, `PUT`/`DELETE …/queries/{qid}`, `db_saved_queries`) is dashboard state, so it
stays editable on a protected connection.

### The editor's assistant

The SQL editor carries an assistant beside the statement (`internal/assistant`,
`api/handlers_db_assistant.go`): it drafts a statement from a question, says why a statement failed
and what a plan does, and on PostgreSQL drafts row-level security policies. It is off until an
administrator chooses who answers it, and what it is sent and what it may do are decided here, not by
the page.

- **The provider is the operator's own.** `PUT /databases/assistant` (`system.admin`) takes
  Anthropic — a Claude model from a closed list (`assistant.AnthropicModels`, Claude Opus 5.5 by
  default) and an API key — or any server that speaks OpenAI's chat completions: OpenAI, a gateway,
  or a model on this machine (Ollama, LM Studio, vLLM, llama.cpp), by its address, a model name and a
  key if it asks for one. The address is HTTPS unless it is loopback, a private range or the
  tailnet's `100.64.0.0/10`, and one with no path is given `/v1`. Nothing is saved until the
  provider has said that the key, the address and the model answer, asked without asking the model
  anything — Anthropic's `GET /v1/models/{model}`, the server's own `GET /models` with the model
  looked for on it (a server that answers that path 404 or 405 lists nothing, and its first question
  says whether the model is there) — so a mistyped key is refused under its field. The settings are one row of
  `settings` (`databases.assistant`), sealed whole with `JD_MASTER_KEY`; the key is never read back,
  and a blank key keeps the stored one only for the same provider at the same address, so a key is
  never sent to a server typed in after it. `GET /databases/assistant` (any role) answers the
  provider, the model, the address, whether a key is set and the model list; `DELETE` forgets it all.
  No provider request follows a redirect, because the key travels in a header.
- **What is sent is the structure.** A conversation begins by reading the schema in focus through the
  catalogue the schema pages use (`assistant.ReadSchema`: `ListTables`, `BuildSchemaGraphWithLimit`
  for 150 tables, and `DescribeTable` for up to eight more the first question names) and writing it
  into the system prompt as tables and views with their columns, types, keys and references — never
  row estimates, defaults or comments, any of which can be a value somebody typed. It is bounded
  (`assistant.SchemaText`): the tables the question names, then the tables they reference and are
  referenced by, then the rest, described up to 48,000 characters, named up to 12,000, then counted.
  A row reaches the model only in a result attached to a question by the person asking: at most 50
  rows of 30 columns and 200 characters a value, held to that on the server whatever the page sent.
  The page cuts to the same bounds before sending, because the editor's results carry long text and
  bytea whole and fifty rows of them would pass the request's 4 MiB limit.
  The editor's statement, an error or a plan go only with the question that carries them, and every
  value of the database stays out of the audit entry.
- **Nothing runs.** A statement the assistant writes is text: the page opens it in a tab or writes it
  at the cursor when the person presses to, and their own Run sends it, through the classifier like
  any other. The one tool the model is offered, `describe_tables`, reads more of the same structure,
  and only once the person allows it: an answer that asks for it ends with the calls pending, and
  `POST …/assistant` with `allow: true` runs them (`false` declines them, which the model is told). A
  question asked instead declines them too. The input of a call is checked field by field before
  anybody is asked (`assistant.Calls`), and a call for anything else, or one that cannot be read,
  runs nothing. An OpenAI-compatible server is offered no tool: many local models refuse a request
  that carries one, so it is told the bounded schema and asked to say what it is missing.
- **A conversation is held on the server** (`assistant.Store`), in memory, per person and
  connection. Its system prompt and its tool are fixed when it begins and every turn is appended, so
  what the provider is sent is the request before with the new turn added — the prompt cache, and the
  reasoning a tool call was made with on the newest Claude models, both depend on that. A turn that
  does not finish — stopped, failed, refused, cut off in the middle of a call — is undone back to
  where it began. At most 200 conversations, 30 minutes idle and 40 turns; a restart, a change of the
  settings (`Settings.Revision`) or another schema begins a new one.
- **Refused on a protected connection, bounded and audited.** `POST /databases/{id}/assistant` is not
  on `protectedRoutesAllowed`, so a protected connection refuses it with `409 connection_read_only`
  before anything is read or sent. It takes `service.control`, the query route's capability. The
  install has one budget of model calls whoever asks (20 a minute, a burst of 6) and answers four at
  once. Each call is audited as `database.assistant.ask` or `database.assistant.tool`: the
  connection, the person, the task, the provider and model, the tokens under `usage` (the audit log
  redacts any field whose name says token; a turn stopped or failed part-way records what the provider
  had counted by then, which for Anthropic is the input and for an OpenAI-compatible server, whose count
  comes last, nothing), how it ended (`answered`, `asked`, `cut`, `refused`,
  `cancelled`, `failed`) and what was attached by kind and count, never the question or a value.
- **The answer streams** as one JSON object a line (`application/x-ndjson`): `start` (the
  conversation, the model, what the schema text holds), `thinking`, `text`, `described` (what an
  allowed call read), and `done` (why it ended, the tokens, the calls pending, whether the turn was
  kept) or `error`. Everything that can refuse the request does so before the provider is asked, as an
  ordinary error with its status, and closing the request stops the provider's answer. The Anthropic
  request is the streamed Messages API, spoken over HTTP rather than through the SDK because it is one
  request and the other provider is plain HTTP anyway: effort `medium`, thinking left adaptive, the
  system prompt cached and the conversation under automatic caching, and the server-side refusal
  fallback (`fallbacks: "default"`) for Opus and Sonnet. The OpenAI-compatible request carries the
  model, the messages, the stream and the usage option, and nothing a server might not know.
- **Engines** come from the capability table: `assistant` is every engine with a dialect, and
  `assistantPolicy` is PostgreSQL and its forks but CockroachDB. The editor offers the assistant to a
  role with `service.control` on those engines; a failed statement and a plan offer a question of
  their own once it is set up.

### The table editor: rows, values and cells

- **Identifiers quoted, values bound, always.** `validateIdent` refuses only what quoting cannot fix (NUL,
  control characters) rather than a conservative character class — a table called `user-profiles` was
  listed and then refused to open.
- **The grid's question is the server's statement.** `GET /databases/{id}/browse` takes `filters`
  (a JSON list of `{column, op, value | values}`, twelve at most), `match=any` to join them with OR,
  `sort` as several keys, `columns` as a projection, and `limit`, which is clamped to
  `MaxBrowseRows` (1000; 100 when absent) rather than reset to the default. The primary key is
  appended to whatever order was asked for as a tie-break, because paging with ties in the order is
  undefined on every engine: two pages of one sort could show a row twice or skip it. The operators
  are one list, `dbx.FilterOps` — `eq`, `ne`, `lt`, `lte`, `gt`, `gte`, `in`, `not_in`, `between`,
  `contains`, `not_contains`, `icontains`, `prefix`, `suffix`, `is_null`, `not_null` — plus `regex`
  where the dialect is a `regexMatcher` (`FilterOpsFor`; PostgreSQL, MySQL/MariaDB, ClickHouse and
  Oracle, flag `regexFilter`), served per driver as `filterOps` so the page keeps no second copy
  that can drift from what the server accepts. A substring operator matches its text as text: `%`
  and `_` are escaped (`likePattern`), with one escape character on every engine since a backslash
  means one thing on MySQL and another under `NO_BACKSLASH_ESCAPES`. An in-list holds at most
  `MaxFilterValues` (200); past that the operator wants a query. One row more than the page is asked
  for and dropped, which is how `truncated` comes to mean "there is another page" without a count,
  and the page carries the table's `primaryKey` and the catalogue's `estimatedRows` beside its rows,
  so one request answers what the grid shows around them. `GET …/count` is the exact figure, on
  request, under the same filters.
- **An unknown row count says so.** `Table.Rows` (`estimatedRows`) is `-1` where the catalogue has no
  estimate — a table PostgreSQL has never analysed, a view, SQLite, which keeps no count at all — and `>=
  0` only where the engine actually answered (the catalogue and `GET /table` answer a SQLite table's
  `sqlite_stat1` count where the file has been analysed; the table list does not). It used to be floored
  to zero in three dialects and leaked a raw `reltuples` of `-1` in a fourth, so a catalogue that could
  not say how many rows a table held was indistinguishable from one saying the table was empty, and every
  table in a fresh database was drawn as having no rows. `Count` on request is the number that is true.
  The ClickHouse query casts before it substitutes (`ifNull(toInt64(total_rows), -1)`): `total_rows` is
  `Nullable(UInt64)`, and asking 24.8 or 25.8 for a supertype of that and a signed literal is
  `NO_COMMON_TYPE`, which fails the whole catalogue — 26.x accepts either form, so the version a
  contributor happens to run decides whether the mistake is visible.
- **A row edit names one row and is held to it** (`dbx/changes.go`). `POST /databases/{id}/changes`
  applies the grid's staged inserts, updates and deletes in one transaction. The key is the table's
  primary key *as the catalogue reports it* — a request cannot choose a looser one — and for a table
  with none it is the row as it was read, a NULL compared as a NULL. Every UPDATE and DELETE must touch
  exactly one row or the whole set is rolled back and answered `409 change_conflict` naming the change
  (`field: changes[i]`, `reason: matched N rows`). MySQL counts rows *changed*, so there the match is
  counted and locked first. SQL Server's driver adds up every "rows affected" in the batch, a trigger's
  included, and reports none when the session has `NOCOUNT` on, so there the statement is made to say
  its own count (`; SELECT @@ROWCOUNT`, read by column name): a table with an audit trigger written
  without `SET NOCOUNT ON` could not be edited at all. Its deadlock victim (error 1205) is run again
  like a serialization failure. A set containing a delete needs the destructive capability, checked in
  the handler. ClickHouse is refused: its sorting key orders rows without identifying one. The older
  single-row routes are this with a set of one. Row values and keys never reach the audit log.
  No value is written as the keyword `NULL`, in the statement that runs as in the one shown, and is
  never bound (`sqlNull`): a bound NULL has to be sent as some type, SQL Server's driver sends it as
  an `nvarchar`, and a `varbinary`, `binary` or `image` column refuses to be converted from one —
  a binary cell could be filled from the grid and never emptied.
  `TestLiveEveryColumnTypeTakesNoValue` sets every type to NULL in an UPDATE and an INSERT on each
  engine.
- **A value goes back the way the grid showed it.** A date is shown as RFC 3339, bytes as `\x…`, a
  decimal as its digits, and an edit — or the whole-row key of a table with no primary key — sends
  those forms back. Five engines read them by themselves. Oracle reads text into a date by the
  session's NLS format and has no `=` for a LOB, so three optional dialect hooks say what it needs:
  `valueWriter` wraps the bind marker of a date column in `TO_DATE` / `TO_TIMESTAMP` /
  `TO_TIMESTAMP_TZ` with a fixed mask (the value is still bound; browse filters go through the same
  hook), `keyMatcher` compares a CLOB, NCLOB or BLOB with `DBMS_LOB.COMPARE` and a JSON column with
  `JSON_EQUAL`, and `columnTexter` gives a substring filter the text the grid shows rather than
  `01-FEB-24`. `TestLiveARowGoesBackAsTheGridShowedIt` writes every type back as displayed and deletes
  the row by it, on every engine. The statements a change set shows for review take the same wrappers
  around their literals, and `TestLiveReviewedStatementsAreTheEnginesOwnSQL` runs them as written.
- **A cell is typed by its column, not its content.** A binary column is hex whatever its bytes spell
  (`\x…`, which an edit may send straight back), a result carries each column's `kinds`, and a value cut
  for the page — a long text, a blob past the preview — is listed in `clipped` with its size and fetched
  whole by `GET /databases/{id}/cell`. That read is bounded at 8 MiB and the value is measured on the
  server before it is fetched: `byteLength` is part of `Dialect`, so an engine cannot be added without
  saying how. Where the engine can only count characters (an Oracle CLOB) the figure is a floor and the
  refusal says "at least"; a type it cannot measure at all (Oracle's LONG) is not read. A cell's `size`
  is the bytes of its text or its bytes, and for a number or a boolean — which travel as `json` — the
  length of its written form; only a NULL has none. A driver's name for a result column is not always
  what the column is. go-ora names its wire types: a JSON column is a BLOB locator, a BOOLEAN a NUMBER, a
  BLOB a long raw. An XMLTYPE it reads laid out afresh, and a NULL one not at all: the statement fails
  or, with other columns in the row, waits until the request runs out of time — one empty XML cell cost
  the page of the whole table. So a dialect may be a `columnReader`: its tables are then selected column
  by column (`projectionFor`, an XMLTYPE through `XMLSERIALIZE … NO INDENT`) and typed from the
  catalogue, in the page, the row a change hands back, the cell read and the value search alike.
  `withCatalog` is the one extra catalogue read that costs, and only Oracle pays it. A query typed into
  the editor has no catalogue to ask and is typed by the driver's names, which `ValueKind` knows. A SQL
  Server `uniqueidentifier` is its GUID text in a page and in a cell, never the sixteen bytes of the wire
  form. A PostgreSQL `bit` or `bit varying` is kind `text`: the server writes and reads one as the text
  of its ones and zeros, and called `binary`, as MySQL's is, the cell was edited as bytes and sent back
  as `\x00001111`, which PostgreSQL refuses.
- **Exact numbers stay exact.** SQL integer values using a 64-bit driver representation and exact decimal
  values travel as decimal strings. SQL mutation JSON uses `DecodeJSONNumbers` (`UseNumber`) and binds
  exact numeric strings without an intermediate JavaScript/Go float. Query results and streamed exports
  also normalize nested ClickHouse integer arrays/maps and wide integer/decimal driver wrappers. Numeric
  JSON syntax is validated without parsing through float64, and clipboard SQL rejects invalid numeric
  literals. `TestSQLiteExactNumericMutationBrowseAndExport` and `TestLiveExactNumericMutations` exercise
  adjacent BIGINT keys beyond JavaScript's safe range through mutation, browsing and export; the latter
  verifies real PostgreSQL/MariaDB DECIMAL columns when their test DSNs are available. SQLite exact
  decimals require suitable storage (for example TEXT); values already rounded by SQLite's NUMERIC/REAL
  affinity cannot be recovered by the API. The Redis SCAN cursor is a decimal JSON string; requests parse
  it with `ParseUint` at 64 bits, preserving the full unsigned range and rejecting negative or
  overflowing cursors.
- **The engines' credential catalogues are not read by name.** The page, the count, the cell, the
  export and the value search are on the read surface and take any schema and table, and the
  connection behind them is usually an administrator's. `guardCredentials` (`dbx/credentials.go`)
  refuses the relations that hold what accounts sign in with — PostgreSQL's `pg_authid`,
  `pg_shadow`, `pg_user_mapping(s)`, `pg_subscription` and the two information_schema views over
  user mappings; MySQL's and MariaDB's `mysql.user`, `global_priv`, `password_history`, `servers`,
  `slave_master_info`; SQL Server's `sys.sql_logins` and `syslogins`; Oracle's `SYS.USER$`,
  `USER_HISTORY$`, `LINK$`, `DBA_USERS`, `KU$_USER_VIEW` — with `403 credentials_withheld`, for
  every role, as MongoDB's `system.users` is. A request that names no schema is resolved as the
  engine would resolve it (pg_catalog and SQL Server's `sys` are searched implicitly, Oracle falls
  back to a public synonym, MySQL is asked which database the session is on, so an application's
  own `user` table is not caught). The search skips them. It is a list of names in front of the
  forms that read a table by name; a statement in the console is the operator's own and needs a
  capability a read-only role does not have. `/count` answers a refused filter, an unknown column
  and a value of the wrong type with `400`, as `/browse` does (`tableReadError`) — it answered `502`.
  A read whose server has gone away since its pool was opened is not the request's fault and is not
  answered as one: the same four routes give it `502 query_failed`, marked `retryable` (below).
- `rowsql.go` is the one exception and does not generalise: it renders a row as an INSERT **for the
  clipboard**. Nothing executes what it produces, and no code path may call it and then run the result.
  `TestLiveRowInsertSQLQuoting` feeds `'); DROP TABLE …` to every live engine and checks the table stands.
  `POST /{id}/rows/sql` reads the table's columns first (`TableRowsInsertSQL`), so the statement
  names them in the table's own order and writes a numeric column's value bare — the grid carries a
  64-bit integer and a decimal as text, and copied out as they arrived they were quoted. Only text
  that is nothing but a number loses its quotes (digits for an integer, digits and a point for a
  decimal, an exponent for a float); `NaN`, a money column's `$1,234.00` and a zero-filled `007`
  keep them. The catalogue read is best effort: a server that does not answer, or a table this
  account cannot see, leaves the statement in the older form, columns by name.
- **Finding a value is bounded three ways at once** (`dbx/search.go`). `GET /databases/{id}/search`
  compares a needle, as a case-insensitive substring, with the text form of every column of every
  table in a schema — which is why each column is cast first: an integer id and a uuid are exactly
  what people search for, and `LIKE` against them is an error on the stricter engines. It visits at
  most 60 tables, compares at most 40 columns of each, returns at most 5 matches a table and 200 in
  all, skips views and the credential relations above, and says how far it got: the tables it read
  (`tablesScanned`), the ones it could not (`tablesSkipped`) and whether a bound stopped it
  (`truncated`), so "no matches" can be told from "gave up before reaching it". The
  bounds are not tuning knobs; they are what makes the feature safe to point at production. It is on
  the read surface and audited (`database.search`), since the needle is the caller's and the read
  crosses every table.

### The schema catalogue and schema changes

- **The catalogue is per engine, and optional per group.** `GET /databases/{id}/catalog` lists the
  schemas and, for one of them, the tables, views, materialized views, routines, triggers, sequences and
  types; `GET /databases/{id}/object` returns one object's CREATE text. Both are on the read surface. The
  queries live in `dbx/catalog_<engine>.go` as methods on the dialect types behind `catalogDialect` — a
  second interface, so the browse, mutate and dump paths do not grow with it. A group an engine lacks is
  absent from the reply, which is how a tree knows not to draw an empty "Sequences" under MySQL; a group
  that could not be read is an empty list with its reason under `errors`, because the least-privileged
  logins are exactly the ones that can read `pg_class` and not `pg_proc`. Each group is bounded (5000,
  `?limit=` up to 20000) and says when it was cut. Postgres is read from `pg_catalog`, never
  `information_schema`: the standard views have no row for a materialized view's columns, spell every
  array `ARRAY` and every enum `USER-DEFINED`, and hide a constraint from a login that holds only SELECT
  on its table — a read-only account saw every table as keyless.
- **Bulk catalogue reads are batched and say when they stop.** Completion, relations and diagram reads
  batch catalogue facts for up to 500 table names per query (`schema_catalog.go`). The diagram draws 120
  tables unless `?limit=` asks for more (up to 1000) and the outline 5000 (up to 20000); both answer with
  `truncated`, `total` and `limit`, so a page says how much it left out rather than drawing a third of a
  schema as if it were all of it. Every map key and node id names a table by `dbx.TableKey(schema,
  name)`: keyed by the bare name, a table called `users` in two schemas was one entry, and one of them
  silently took the other's place. Partitions are listed as `partition` and are not drawn; views and
  materialized views are not asked for foreign keys. Each dialect keeps its type spelling, key order and
  referential actions; SQL Server's bulk columns are therefore read from `sys.columns` and spelled by
  `mssqlColumnType`, as a table's own detail is, because information_schema names an alias type by the
  type under it (`sysname` came back as `nvarchar(128)`) and leaves a `datetime2`'s precision out. A
  refused bulk read falls back to per-table reads so restricted accounts retain partial results. Full
  table details use fresh reads.
- **A table's DDL has two readers.** Where the engine keeps no text of its own (Postgres, SQL Server) the
  CREATE TABLE is generated. `dbx.Detail` returns the form a dump replays ahead of the rows: columns,
  defaults, keys, constraints and indexes, and deliberately not identity or generated-column clauses,
  which refuse the value an INSERT is about to supply. `dbx.DescribeTable` — what `GET
  /databases/{id}/table` serves — is the same read with the DDL written as the table is declared, and a
  view answered with its own definition. Both carry check, unique and exclusion constraints, incoming
  foreign keys, comments, real type names with enum labels, and each index's method, predicate, included
  columns and size. SQLite and ClickHouse keep a table's check constraints nowhere but in the statement
  it was created with, so theirs are read out of that statement (`createTableItems`).
- **A schema change is planned, then shown or run.** There are twenty-four `/databases/{id}/ddl/*` routes:
  create, truncate and drop a table; rename a table or a column; add, alter and drop a column; create and
  drop an index, a foreign key, a unique or check constraint, a view and a schema; comment on a table or
  a column; create an enum type and add a label to one; make, replace and drop a function, a
  procedure or a trigger (below); and, for PostgreSQL, the four of row-level security below. What an engine takes of them is its `ddlOperations` list. Every handler builds a `dbx.DDLPlan` — the exact statements, in order — and
  `runDDL` returns it for `?preview=1` or executes it, so the statement a dialog shows is the server's
  own and not a page's approximation of it. Each planner starts at `ddlDialect(driver, op)`, which
  refuses an operation the engine does not have in words that name the engine (`ddlRefusals`): SQLite
  cannot alter a column or add a constraint to an existing table, and ClickHouse is sent its own form
  (`RENAME TABLE`, `MODIFY COLUMN`, `ALTER TABLE … DROP INDEX`) or a refusal, never generic DDL.
  Capability follows cost. A change that only adds needs `service.control`; every drop is behind
  `s.destructive`; and what depends on the body is decided in `runDDL` and fails closed, for a preview as
  much as for a run — a new column type, which rewrites the column, and any call in the operator's own
  SQL (a CHECK condition, an index predicate, a USING conversion, a function default) outside the short
  lists `pureFunctions` and `safeDefaults`. The engine runs such SQL against rows, and a form must not be
  a cheaper way to run a function than the console is. `unvouchedCalls` reads the fragment as the
  engine's tokens, so a name is one name however it is spaced or qualified, and on Postgres names a
  dotted reference whether or not a parenthesis follows it, because `t.total` there is `total(t)` when
  `t` has no such column.
- **What a form writes into a statement cannot leave its place.** A column type is the one fragment that
  is neither quoted nor bound nor wrapped: it follows the column's name, and what follows it is the rest
  of the statement. `validateType` (`ddl_type.go`) therefore matches a type against what a type is — a
  name, one argument list held to what that engine's types take, one of a closed set of qualifiers, array
  brackets — rather than refusing a list of words: `integer, DROP COLUMN email` was a second action of
  the same ALTER TABLE, for an account refused that drop on its own route. Free SQL fragments are wrapped
  in the server's own parentheses and held there by `validateFragment`, which follows each engine's own
  quoting — a bracket quotes an identifier on SQL Server and is a subscript on Postgres, and a scanner
  that treated it alike on both would let text hide from one of them. A view's query goes through the
  query runner's own splitter and classifier. SQLite statements are written unqualified, so a change
  aimed at an attached database is refused rather than landing on main's table of the same name.
- **A plan that restates a column says everything again.** MySQL's `MODIFY COLUMN` replaces a whole
  column definition, SQL Server's `ALTER COLUMN` restates type, collation and nullability together and
  keeps a default as a constraint object, so those plans read the catalogue first. `readMySQLColumn`
  carries the collation, default, `AUTO_INCREMENT`, `ON UPDATE`, `INVISIBLE`, a spatial reference system
  and a MariaDB column's own `CHECK` — which is all that makes a MariaDB JSON column one — and refuses,
  by name, a column whose `EXTRA` holds an attribute it does not know rather than drop it. A read the
  engine refuses is `dbx.ErrPlanRead` and answers `502`, not `400`: the request was not wrong. A preview
  that could not be drawn up leaves no audit entry — a dialog previews as the operator types — while one
  refused for the capability it would need is recorded under the change's own action. No schema route
  takes a typed phrase, and dropping a schema never cascades: the engine's refusal of a schema that still
  holds something is the guard.
- **Row-level security is a table's, and PostgreSQL's.** `dbx/rls.go`; the `rowSecurity` flag, which
  CockroachDB lacks, gates it. `GET /databases/{id}/rls?schema=&table=` (read surface) answers whether
  the table filters its rows (`relrowsecurity`), whether its owner is held to the filter
  (`relforcerowsecurity`), who no policy filters (superusers and `BYPASSRLS`, `exempt`) and each
  policy of `pg_policy` — name, command, permissive or restrictive, roles, `using`, `withCheck`. The
  forms are `ddl/policy` (create, change, drop) and `ddl/row-security` (on, off, force), planned by
  `PlanCreatePolicy`, `PlanAlterPolicy`, `PlanDropPolicy` and `PlanRowSecurity` and shown or run by
  `runDDL` like the rest, so the dialog's statement is the one that runs. A policy decides who sees
  which rows as a grant decides who sees which tables, so the routes are `system.admin`, not
  `service.control`: dropping a policy is behind `s.destructive` as well, and so is turning security
  off or no longer forcing it on the owner (`RowSecurityChange.TurnsOff`), which the handler asks of
  the destructive capability and its budget by hand because it is a `PATCH`. The
  conditions are the operator's SQL, confined by `validateFragment` as a CHECK condition is, and
  read for calls: `current_setting` and the `has_*_privilege` family are vouched for
  (`policyCalls`), anything else — `auth.uid()`, a subquery's qualified table — is named in the reply
  and needs the destructive capability, for a preview as much as for a run. A command refuses the
  clause it has no use for (an INSERT has no `USING`, a SELECT or DELETE no `WITH CHECK`) before the
  statement is drawn, and an alter reads the policy's command to do so. `ALTER POLICY` cannot change
  a policy's command or kind or take a condition away; a condition sent empty is left as it is.
  Audited as `database.ddl.create_policy`, `.alter_policy`, `.drop_policy` and `.row_security`.
  - *The role test.* `POST …/rls/test` (`service.control`) runs one SELECT as another role and
    answers the rows (`dbx.RunAsRole`). The statement is read first by the query runner's own
    classifier and has to be a `SELECT`, `WITH`, `TABLE` or `VALUES` that classifies as a read; it
    then runs on a connection of its own in a read-only transaction that is rolled back and never
    committed, after `SET LOCAL statement_timeout` and `SET LOCAL ROLE`, and then one bound
    `SELECT set_config($1, $2, true)` for each setting — made *as the role*, so a setting only a
    superuser may change (`pg_stat_statements.track`) is refused to a test as it is to the role.
    A setting has to be named with a dot (`app.tenant_id`), which keeps `role` and the server's own
    settings out of it. A superuser is not become: no policy filters it, so what it reads says
    nothing about them, and `SET ROLE` to one would hand its reach (`pg_read_file`, `pg_authid`) to
    whoever may run a test; the answer is `skipped` with a note and nothing is run. The role of the
    transaction is read again after the statement, to catch a function that left it changed (a role
    left changed would pass its rows off as another's); that is a check of the answer, not what
    bounds the test — the read-only transaction and the requirement that the account be a member of
    the role do. At most 100 rows are read (1000 at most), twenty settings of 4 KiB. The answer says
    the role, every statement sent before the query (`setup`) and, when the table is named, why the
    filter may not apply to that role: security is off, the role owns the table or inherits its
    owner's rights (`pg_has_role(…, 'USAGE')`) and it is not forced, or the role bypasses it. The
    account the connection signs in with has to be a member of the role, as `SET ROLE` requires;
    that is reported in words ("not a member"). A function the SELECT calls that writes is refused
    by the read-only transaction. The classifier's judgement of a function is the query runner's,
    with the gaps it has: it is not a sandbox. On a protected connection the test is let through
    when the statement is a read (`protectedRoleTest`) and refused when it is not. Audited as
    `database.rls.test` with the role, the settings' *names* (their values are the operator's test
    data and may be a token), the statement and the row count; a failure is recorded by its
    SQLSTATE, because the engine's text can quote a value it rejected.
- **Stored programs are written by a form, whole.** `ddlOperations` carries `functions`, `procedures`
  and `triggers`: Postgres, MySQL/MariaDB, SQL Server and Oracle have all three, SQLite has triggers
  only, and TiDB, CockroachDB and ClickHouse none (`ddlRefusals`, and a flavour check in
  `DDLOperations`). `POST`/`DELETE /ddl/routine` take a function or a procedure (`kind`), and
  `POST`/`DELETE /ddl/trigger` a trigger; `dbx/routine.go` renders each from a `RoutineSpec` or
  `TriggerSpec` into **one statement** — a body is the tail of it (the dollar-quoted text on Postgres,
  where the tag is chosen so the body does not contain it; everything after `AS`, `IS` or the header
  elsewhere) and never reaches the console splitter, so a MySQL `BEGIN … END`, an Oracle block and a
  SQLite trigger need no `DELIMITER` or `/`. The header is built from validated parts like every other
  DDL form: names are quoted, types go through `validateType`, defaults and conditions through
  `validateFragment`. **The capability is the console's:** the query runner classifies every
  `CREATE FUNCTION`, `PROCEDURE` and `TRIGGER` as high risk, so these routes sit at `service.control`
  only to let a preview be asked for as the form is filled in, and ask for `destructive` by hand on
  every request (`programRisk`), a preview included; a run spends the budget, and the drops are in the
  destructive group. They are audited as `database.ddl.save_routine`, `save_trigger`, `drop_routine`
  and `drop_trigger`.
  `GET /program?kind=&schema=&name=&signature=&table=` reads a program back as the same fields
  (`dbx.ReadProgram`): `pg_proc`/`pg_trigger` on Postgres, `information_schema` on MySQL, and the text the
  engine kept on SQL Server, Oracle and SQLite, read with the query runner's lexer. It refuses, by name,
  anything the form could not write again — a Postgres `COST` or `SET`, a SQL Server `WITH ENCRYPTION`, an
  Oracle `PIPELINED` or compound trigger, a quoted lower-case Oracle parameter — and answers
  `editable: false` with the reason, so a save never silently drops an attribute.
  An edit is whatever keeps the old definition if the new one is refused, and the plan says which in
  `note`: `CREATE OR REPLACE` (Postgres when the parameters and return type are unchanged, MariaDB,
  Oracle), `CREATE OR ALTER` (SQL Server), a `DROP` and `CREATE` in one transaction (a Postgres routine
  whose signature changes, and Postgres and SQLite triggers; `DDLPlan.atomic`), or — MySQL, which commits
  a `DROP` — a `DROP` and `CREATE` with the old definition put back from the text it was read as if the
  `CREATE` is refused (`DDLPlan.undo`). Oracle accepts a routine that does not compile and keeps it
  `INVALID`, so a plan checks `all_errors` afterwards (`DDLPlan.after`) and puts the old definition back,
  or drops the new object, when there are errors. An undo only follows a statement that ran. Postgres
  re-applies a comment, the owner and the `EXECUTE` privileges the object carried when it drops and
  creates (`pgRoutineRights`); a MySQL definer becomes the connection's user and its `procs_priv`
  grants go with the `DROP`, which the note says, and the new definition is first made under a scratch
  name (`jd_check_<name>`) and dropped, so the account's rights or the binary log's rule for functions
  refuse it while the old routine is still there (`DDLPlan.guard` keeps an undo from running over an
  object that was never touched). The settings a program was stored under — MySQL's `sql_mode`, SQL
  Server's `ANSI_NULLS` and `QUOTED_IDENTIFIER` — are set again on the connection first (`Session`), which
  is then discarded rather than pooled. A program is also refused for editing where saving would change
  its place in an order it cannot restate: a MySQL trigger sharing its event and timing with another,
  a SQL Server trigger with `sp_settriggerorder` or disabled, an Oracle trigger with `FOLLOWS`,
  `UPDATE OF` or `REFERENCING` in its header. A new Oracle trigger is checked and dropped again if it did
  not compile, because Oracle keeps it enabled and then refuses every write to its table (ORA-04098).
  A save reply carries the routine's catalogue `signature` where the engine has them, so a page that
  addressed it by signature can follow it.
- **The diagram remembers.** `GET/PUT/DELETE /databases/{id}/diagram?schema=` keep one JSON document per
  connection and schema in `db_diagram_layouts` — positions, hidden tables, notes, colours, detail level
  and viewport — beside the saved queries that outlive a page for the same reason. Reading it is on the
  read surface; saving and resetting need `service.control` (it is dashboard state, not database state,
  so it is not in the destructive group) and are audited as `database.diagram.save` and
  `database.diagram.reset`. The server checks only that the document is a JSON object under 512 KiB:
  every field is a decision about a picture, and the diagram (`diagram/memory.ts`) is the only thing
  that decodes it, with the browser's storage as a mirror for roles that cannot save.

### Operations: watching, maintaining and administering a server

- **Recorded database activity** (`api/database_metrics.go`) starts in `Server.Start` and stops before
  the database pools. It reads saved SQL servers, Redis-family servers and MongoDB every 30 seconds
  with four workers, an eight-second per-connection deadline and a 25-second collection deadline.
  Redis statistics reads disable command retries and honor the context deadline for socket replies.
  SQLite has no activity counters and is skipped. It uses the same bounded statistics readers as
  `GET /{id}/stats`, never a keyspace scan or application-table read. Successful snapshots are saved
  in `db_metric_samples` for the retention the operator chose (7, 14, 30 or 90 days, the
  `databases.metrics.retention_days` setting, a week until chosen), with pruning even when engines
  fail to answer; a retention that cannot be read prunes nothing. Readings older than seven days
  are thinned once an hour to the newest of each five minutes (`database_metrics_retention.go`):
  the history derives rates from differences of stored totals, so the last total in a step stands
  in for it where an average would corrupt every counter. History is
  keyed by connection and a hash of the saved driver and sealed DSN, so a replaced connection target
  cannot inherit another target's history; an in-flight read is saved only if that target still matches.
  Forgetting the connection cascades to its samples. No credentials, rows, query text or key values
  are recorded. `GET /{id}/stats/history?hours=1` is on the read surface, accepts 1 to 2160 hours,
  answers no more than the retention (a longer range saved before the retention was shortened is
  clamped to it, and `retentionHours` in the answer says how much that is), returns the newest raw totals in each of at most about
  720 time buckets and carries missed intervals as gaps, measured in five-minute steps past the
  seven-day mark so thinning is not drawn as an outage.
  It reads only the dashboard store, so history survives restarts and remains readable during an
  engine outage. Home offers 1h, 6h, 24h and 7d ranges, and 14d, 30d and 90d when the retention reaches them, and derives rates from stored totals; negative
  counter deltas and recording gaps longer than 90 seconds are never drawn as continuous rates.
  History begins when this version of the dashboard starts; past activity cannot be reconstructed
  from engine totals. Each pass ends by handing what it read to the
  [alerts](#alerts-told-when-a-server-or-its-backups-go-wrong), which have it read a server's disk
  and replication as well only while a rule watches that server for them.

- **Statement history** (`api/database_statements.go`, `dbx/statements_history.go`) is recorded
  beside the activity recorder, started and stopped with it, on a clock of its own: every five
  minutes, with two workers, a 30-second deadline per connection and four minutes for the pass.
  Each saved server whose engine has `statementHistory` — every engine with `statements`, so
  PostgreSQL, MySQL, MariaDB and Percona, ClickHouse, SQL Server and Oracle, and not CockroachDB or
  TiDB — is read through `dbx.TakeStatementSnapshot`, which is `TopStatements` at its fullest (200
  statements by total time): the statement list's own read. So the text kept of a statement is the
  shape `/statements` shows any role, by whatever rule that read holds the engine to, and there is no
  other read of statement text for the history to keep. The reads a snapshot adds select ids and
  numbers only: MySQL's `SUM_ROWS_EXAMINED` and, on MySQL 8, `events_statements_histogram_by_digest`;
  ClickHouse's runs between two snapshots. An interval is the difference between two snapshots'
  running totals (`dbx.StatementsBetween`). A statement new to the list is left out of its first
  interval, since its totals may have run up long before; one whose totals went down had its entry
  made again, and everything it has ran since; and the counters starting again —
  `pg_stat_statements_info.stats_reset` moving, or this dashboard's own `POST /statements/reset`,
  which MySQL has no other way to tell (`noteReset`) — makes every total one of what ran after.
  ClickHouse keeps runs rather than totals: its snapshot is the runs that finished between where the
  last one ended and fifteen seconds before the server's own now, since the query log is written in
  batches, so a run is counted once and none is lost at the seam. The first reading after the
  dashboard starts, and one taken more than fifteen minutes after the last, only marks where the next
  interval starts. An interval keeps its whole in `db_statement_intervals` — the time of every
  statement the engine counted, and whether the counters were reset — and the 50 statements that
  took the most time in it in `db_statement_samples`: calls, time, rows returned, rows examined where
  the engine counts them, and a histogram of run times where it keeps one. Each statement's shape is
  in `db_statement_shapes`, and every reading writes the list's text again, so a statement still
  listed is kept as the list shows it now. Seven days are kept and pruned on every pass; the history
  is tied to the exact saved target, as the activity history is, and forgetting the connection
  cascades. Percentiles are read from merged histograms — MySQL's own buckets, about 5% wide, and for
  ClickHouse buckets cut from each run's whole milliseconds, one a millisecond up to 20 ms and a
  sixteenth of a doubling above — as the upper edge of the bucket the quantile falls in, so never
  under the true figure. PostgreSQL, MariaDB, SQL Server and Oracle keep no distribution and have
  none; a mean there is the time added divided by the calls added.
  `GET /{id}/statements/history?hours=&sort=&limit=` (read; no dial) answers the window — how many
  readings, how much of it they cover, the time of everything counted, the resets, the recorder's
  newest reading of the server since this process started and when the next is due — and its
  statements by `total`, `mean`, `calls`, `rows`, `rowsRead` or `p95` (a statement without the
  figure last), `limit` 50 up to 200, each with its share of the window and its time in each of about
  24 buckets, `null` where nothing was recorded. `GET /{id}/statements/history/{statement}?hours=` is
  one statement's figures and about 168 points for its charts, and 404 for a statement never
  recorded. Hours are 1–168, as `/stats/history` takes them. Neither is audited. The page is
  Performance → Statement history.

- **Metrics export** (`api/database_metrics_export.go`, `database_metrics_retention.go`).
  `GET /api/v1/metrics/databases` serves the recorder's newest reading of every connection in the
  Prometheus text format (`text/plain; version=0.0.4`, `no-store`). It reads `db_metric_samples`
  and dials nothing, so a scrape costs the databases nothing and works while one is down. Every
  series carries `driver`, `name` and `id`; the names are `jd_database_*`, counters ending
  `_total`. `up` is 1 while the newest reading is under 90 seconds old, `last_sample_timestamp_seconds`
  says when it was taken, and a server whose reading is older exports those two series alone, so
  stale figures are never charted as current. A connection never recorded, and SQLite, have none.
  SQL engines export their counters and gauges by the engine's own key in snake case, beside the
  shared connection, size, uptime and pool series; Redis and MongoDB export the fixed sets in
  `redisPromReads` and `mongoPromReads`. The route is for **API keys only**: no credential is 401, a session
  cookie is `403 token_required`, and a key narrowed to `metrics.read` is admitted here and
  refused everywhere else ([request lifecycle](../architecture/request-lifecycle.md)). It is not
  audited per scrape, like every read; minting a key records `dashboard.token.create` with its
  `scope`. `GET /databases/metrics-export` (any role) answers the endpoint, the retention, the
  bytes the history takes and an estimate for each window from the average saved reading and the
  servers read in the last day, `null` until there is something to measure; `PUT` (administrator,
  behind `s.destructive` because a shorter window deletes history at the recorder's next pass,
  audited as `database.metrics.retention` with `days` and `previousDays`) chooses it. The UI is the
  *Metrics export* dialog of the control center
  (`components/database/fleet/metrics-export-dialog.tsx`): the URL, a button that makes a
  metrics-only key and shows it once, a copyable `prometheus.yml` job, and the retention choice,
  confirmed when it shortens. The activity charts offer the 14, 30 and 90 day ranges that the
  retention allows.

- **The diagnostic surface is what a data browser usually lacks.** `activity.go` lists what the server is
  running now with the blocking session named, turning twenty "slow" sessions into one culprit, and can
  stop one; it includes our own connections marked `self`, because hiding them made an idle server report
  an empty table that reads as a broken query. `size.go` is the per-table breakdown for when the alert
  fires and nobody knows which table grew (row counts are the engine's estimate — counting forty tables
  exactly is a full scan to answer a question about *relative* size).
- **A size read survives a table dropped under it.** PostgreSQL catalog size queries treat a
  concurrently dropped relation's NULL size as zero; the live regression holds an old catalog
  snapshot to exercise that case deterministically.
- **Watching and maintaining a SQL server** (`handlers_db_ops.go`, `mountDatabaseOpsRoutes`;
  per-engine SQL in `dbx/ops_<engine>.go` behind optional interfaces, so a dialect that has no
  answer reports `supported:false` with a sentence rather than an error — and so does one whose
  account was refused the view it reads: "not permitted" names the grant in `reason` and is never a
  502).
  - Reads, on the read surface: `GET /{id}/stats` for a SQL engine is one snapshot of raw counters and
    gauges stamped with this server's clock (`ServerStats`; rates are derived from recorded totals) with
    the dashboard's own pool under its old key; a server that
    refuses the snapshot answers 200 with `supported:false`, the refusal as `reason`, and the pool, which
    was this route's whole answer before there was a snapshot. `/activity` rows gain `status`, wait type
    and event, application, transaction and query start, `blockedByPids`, and `seconds` is the running
    statement's age and `0` for a session that is not running one (an idle pooled connection used to read
    as the longest query). On MySQL an account without the `PROCESS` privilege sees only its own sessions
    in the process list and is refused the InnoDB transactions it is joined to — and MySQL 8 sends that
    refusal after the columns, where a result read to its `Close` alone took it for an empty list: the
    snapshot said nobody was connected and `/activity` listed nobody. The result is read to its end and
    asked how it ended (`mysqlProcessList`); such an account's `connections` are the server's own totals
    (`Threads_connected`, `Threads_running`, which need no privilege) with a `notes` entry saying so, and
    its own sessions are listed. `/locks` lists waiter–blocker pairs and the lock table, bounded at 500:
    PostgreSQL, MySQL 8 `performance_schema`, MariaDB `INNODB_LOCK_WAITS`, SQL Server
    (`dm_os_waiting_tasks`, needs `VIEW SERVER STATE`), Oracle (`v$session` names each waiter's blocker,
    `v$lock` what it asked for; needs grants an application schema lacks). `/replication`: PostgreSQL
    replicas, slots, publications, subscriptions, receiver; MySQL/MariaDB replica and source status; the
    other engines say `supported:false`. One question is put to MySQL and MariaDB in each one's spelling,
    and when neither answers the refusal reported is the one that denies access and names the privilege
    (`mysqlRefusals`), not the older spelling's syntax error; a note about missing performance_schema
    counts says whether it is off, unreadable to this account, or empty (`mysqlPerformanceSchemaGap`).
    `/tablestats` and `/indexstats`: sizes, row and dead-row estimates, scans, last vacuum/analyze, a
    planner-statistics bloat estimate, unused/duplicate/covered indexes; default 200, clamped to 1000,
    largest first. MySQL, SQLite and Oracle read up to 20 000 names and rank them here because their
    sizes arrive from a second read; Oracle's catalogue views are each read once for the schema and
    joined in Go, because DBA_SEGMENTS joined to ALL_INDEXES in SQL took seconds on a schema of thirty
    tables. Oracle sizes come from `DBA_SEGMENTS`, or from `USER_SEGMENTS` for the account's own schema,
    which needs no grant; an Oracle index is never called unused, because `DBA_INDEX_USAGE` is a sample.
    `/maintenance` is the action list; `/settings[?all=1]`;
    `/clickhouse/{parts,merges,mutations,queries}`; `/sqlite/file`. The stats poll asks the catalogue
    which checkpoint view exists (`to_regclass`) instead of trying `pg_stat_checkpointer` and catching
    the error, which PostgreSQL before 17 wrote to its log on every poll; `pg_stat_statements_info` is
    asked for the same way.
  - `POST /{id}/activity/cancel` stops a statement and keeps the session (`pg_cancel_backend`,
    `KILL QUERY`, Oracle `ALTER SYSTEM CANCEL SQL`); it sits in `s.destructive` beside kill. Audit
    `database.session.cancel`. Oracle answers the kill of a session that is in the middle of a
    statement with `ORA-00031: session marked for kill`: the session is ended once it has rolled
    back, so `oracleKillOutcome` reads that as the kill having worked, where the route answered
    `400` for a session that was gone a second later.
  - `POST /{id}/maintenance {action, schema?, table?, index?, options?}` runs one of a closed set
    per engine and returns the engine's own lines, kept to 500 as they arrive: PostgreSQL (vacuum,
    vacuum_analyze, analyze, vacuum_full, reindex), MySQL/MariaDB (analyze, check, optimize,
    repair), SQLite (analyze, optimize, three checks, wal_checkpoint, reindex, vacuum), ClickHouse
    (optimize), SQL Server (`update_statistics` — `UPDATE STATISTICS` or `sp_updatestats`;
    `reorganize` and `rebuild` — `ALTER INDEX … REORGANIZE|REBUILD`, the latter optionally
    `WITH (ONLINE = ON)`; `check` — `DBCC CHECKTABLE|CHECKDB WITH TABLERESULTS`, whose rows are the
    report and whose severity decides `ok`) and Oracle (`gather_stats` — `DBMS_STATS` for a table
    or a schema, names passed quoted so they are matched as the catalogue spells them). Where the
    engine prints nothing a client can read (SQL Server's three besides `check`, Oracle), the lines
    are read from the catalogue afterwards. The route is `service.control`. **An action that locks
    a table against the application for as long as it runs, or can lose rows — `vacuum_full`,
    `reindex`, MySQL `optimize` and `repair`, SQLite `vacuum`, SQL Server `rebuild` — also needs
    the destructive capability and spends `destrLim`**, checked in the handler before anything is
    dialled (`MaintenanceAction.NeedsDestructive`, published per action as `requires`): the SQL
    console refuses those statements to the same account, and a form is never the cheaper way. The
    actions left on `service.control` read, or work alongside the application; the console's
    stricter answer to them comes from not knowing what a typed statement does, which a closed
    list does. The ones that change nothing at all are marked `readOnly`. The request is held for
    up to thirty minutes; ending it stops the command on the server — pgx sends a cancel request
    when a cancelled context closes its connection, go-mssqldb an attention signal, and for
    MySQL/MariaDB the statement runs on one session whose id is killed (`KILL QUERY`) from a second
    connection, because the driver only closes the socket and the server does not notice that
    inside a table rebuild. Oracle is late: go-ora sends a break and the server acts on it when it
    next looks, seconds into a block that is working and not while one sleeps. Audit
    `database.maintenance`.
  - `PUT /{id}/settings {name, value | reset}` (`system.admin`) persists one parameter where the
    engine can: `ALTER SYSTEM` + `pg_reload_conf()`; `SET PERSIST` (MariaDB: `SET GLOBAL`, said to
    be unpersisted); the four SQLite pragmas stored in the file; SQL Server `sp_configure` +
    `RECONFIGURE` (an advanced option is revealed for the change and hidden again after it, the
    option is put back if `RECONFIGURE` refuses the value, and there is no reset because the server
    publishes no default); Oracle `ALTER SYSTEM SET … [DEFERRED] SCOPE=BOTH` for the parameters a
    running instance can change — the ones read only at start are listed and not editable, since a
    wrong value there is an instance that does not come back, and inside a pluggable database only
    what Oracle lets a container set. The name must be in the engine's own list and its spelling
    there is what reaches the statement; the value is checked against the type, enum and range the
    engine publishes (PostgreSQL integers also in octal and hexadecimal, which is how it prints its
    file modes). `editable` follows the account: a superuser on PostgreSQL, `ALTER SETTINGS` on SQL
    Server, `ALTER SYSTEM` on Oracle. The list withholds, from a viewer without `system.admin`, the
    value of a parameter that can hold a credential (`sensitiveSetting`): a list per engine
    (`primary_conninfo`, the archive and restore commands, `ssl_passphrase_command`,
    `report_password`, `wsrep_sst_auth`…), any name whose last word is one for a secret, and — on
    PostgreSQL — an extension's or application's own dotted parameter with such a word anywhere. It
    is a list and not a pattern on purpose: a pattern on "password" hid `password_encryption`,
    which is a public setting. The same rule keeps the value and the statement
    out of the audit detail. Audit `database.setting.set` / `database.setting.reset`.
  - `POST /{id}/statements/reset` (the destructive group) zeroes `pg_stat_statements` or the
    performance_schema digest table for the whole server. No data goes, but the record does, for
    everyone, and on MySQL the statement is a `TRUNCATE` the console holds to the same capability;
    the Redis slow-log reset sits in that group too. Audit `database.statements.reset`.
- **Statement statistics.** Database Home displays recorded activity and engine readings. The
  Advisor page, advice detectors, capability flags and `GET /databases/{id}/advisor` were removed in
  0.7.5. Query, schema, performance and maintenance controls remain available through their own routes.
  `GET /databases/{id}/statements?sort=&limit=` reads
  `pg_stat_statements` (13+ and older column names both),
  `performance_schema.events_statements_summary_by_digest`, ClickHouse's `system.query_log` over the last
  day, SQL Server's `sys.dm_exec_query_stats` summed by query hash for plans compiled in this database
  (the plan cache: no reset, no start time), or Oracle's `v$sqlstats` (no longest execution, so
  `sort=max` is a 400 there); `sort` is one of a closed set, `limit` clamps at 200, each row carries its
  `share` of everything tracked, and `supported: false` comes with what would enable it (`enable`) or
  with the grant the account lacks (`reason`). On PostgreSQL `enable` says how far the module is from
  loaded: `configured` when the list the next start loads names it and the running list does not —
  that list is the last line the configuration files hold for it, read from `pg_file_settings` by a
  superuser, whatever the server says of that line now, since a line waiting for a restart is marked
  as not applied —, `preload`, the exact `ALTER SYSTEM` that adds it after every library in that
  list, where the account is a superuser and the value is not set on the command line, and `blocked`
  with the reason where it is. Nothing is offered where the module is not installed: a server told
  to load a library it cannot find does not start. `POST /{id}/statements/preload` (`system.admin`,
  refused on a protected connection) runs that statement and `pg_reload_conf()`, answers as a
  parameter change does with `restartRequired`, stores nothing when the list already names the
  module, and is audited as `database.statements.preload` with the value stored. The restart that
  loads it is the power route's and the extension the extensions route's, so the page's guided setup
  (`ops/statements-setup.tsx`, opened by *Turn on statement statistics* in Performance → Statements
  and in Home's busiest-statements block) takes them as three steps, each its own press: the
  statement shown before it runs; the restart, confirmed with what it does to the sessions on the
  server and offered only where the summary's `power.restart` and the role allow it — elsewhere the
  reader is told where it is restarted and the setup resumes from the page once it is back —; and
  the extension once the server answers again. The dialog is drawn by a host above the page's switch
  between a server that answers and one that is down, which the restart fires. The capability is
  `statementsSetup`: PostgreSQL and TimescaleDB, not CockroachDB (no module) nor YugabyteDB (it
  loads the module itself and refuses `ALTER SYSTEM`).
  `TestLiveStatementsSetupKeepsTheLibrariesAServerLoads` runs the three steps on a server of its own
  that loads `auto_explain`, and checks both libraries are loaded after.
  The text returned is a statement's shape on every engine,
  never one execution: the list is on the read surface and a literal is somebody's e-mail address, or in
  a `CREATE ROLE` a password. MySQL and MariaDB keep it that way (`DIGEST_TEXT`), ClickHouse is asked to
  (`normalizeQuery`), and for SQL Server and Oracle `dbx.statementShape` replaces every string and number
  literal with `?` before the text leaves the package. It also drops comments: a comment is whatever the application wrote (a request id,
  a user), and Oracle stores the text with its line breaks turned into spaces, so a `--` comment cannot be
  told from the code after it and takes the rest of the statement with it. An optimizer hint (`/*+ .. */`,
  `--+`) is kept with its literals shaped, and SQL Server's nested block comments are read as it reads them.
  `TestLiveAPIOracleStatementsCarryNoLiterals` (INSERT, INSERT ALL, MERGE, a PL/SQL block, comments, hints)
  and its SQL Server and MySQL siblings read `/statements` as a viewer and look for what was run. PostgreSQL's text is shaped in the package as
  well, by `pgStatementShape` (`dbx/statements_shape.go`): `pg_stat_statements` writes `$n` over the
  constants of the statements it plans (`SELECT`, `INSERT`, `UPDATE`, `DELETE`, `MERGE`) and keeps every
  utility statement as it was sent while `pg_stat_statements.track_utility` is on, its default —
  `CREATE ROLE … PASSWORD '…'` and `ALTER ROLE`, `SET`, `COMMENT`, a column `DEFAULT`, a `DO` body. The
  pass reads the text by PostgreSQL's own lexical rules (dollar quoting; `E''`, `U&''`, `N''`, `B''` and
  `X''` strings; block comments that nest; numbers in every base 16 takes, underscores included), keeps
  the `$n` markers the Statements panel asks a value for, and replaces every literal with `?`. That is
  every literal, not every value the extension missed: a type's modifiers and an `ORDER BY` position read
  `?` too. In a statement the extension keeps as it came, two values that need no quotes are masked
  however they are written, a bare word included: whatever follows a role's `PASSWORD` (`CREATE`/`ALTER`
  `ROLE`, `USER`, `GROUP`, a user mapping), and a setting's value after `SET … TO`/`=` (`SET`, `ALTER
  ROLE`/`DATABASE`/`SYSTEM … SET`, a function's `SET` clause). Whether a backslash escapes in a plain
  `'…'` string depends on `standard_conforming_strings` in the session that sent it, which the text does
  not record, so the text is read both ways and what is a literal in either reading is masked: the wrong
  reading can only hide more. Comments and quoted names are kept. `/activity` and `/querylog` are not
  shaped: they return what the server recorded, as below.
- **The server behind a connection.** `dbx.Admin` is an optional second interface a dialect
  implements — Postgres, MySQL/MariaDB, ClickHouse and SQL Server do; SQLite has no server and
  Oracle's account model does not fit — with Mongo (`usersInfo`/`createUser`/`grantRolesToUser`)
  and Redis (`ACL LIST`/`SETUSER`/`DELUSER`, saved where an aclfile exists) mapped onto the same
  `Role` shape. Routes under `/databases/{id}/server/`: `roles` (list and `/{name}` detail on the
  read surface; create, alter and `/{name}/grant` under `system.admin`; drop under
  `s.destructive`, refused for the account the connection signs in with), `databases` (create,
  and `/connect` to save a sibling connection to another database on the same server under the
  same credentials, probed before it is stored; the list of what the server holds is
  `GET /databases/{id}/schemas`, below), `extensions` (the list and `/{name}` detail on the read
  surface; create, update and drop for Postgres, below; listed only for MySQL's plugins), `preload`
  (a library added to what Postgres loads at start, below) and `settings` (the short list an
  operator asks about; the full one is `/databases/{id}/settings`, above). Identifiers go through the dialect's
  `QuoteIdent`; a grant runs inside the target database on the engines that grant from there
  (`GrantNeedsDatabase`) through a pool opened for that one request; a password is the one value
  no engine binds in `CREATE ROLE`/`CREATE USER`, so it is refused if it carries a control
  character and quoted by the same per-engine rule `dumpString` applies (`passwordLiteral`). On
  PostgreSQL a create or an alter that carries one runs with `pg_stat_statements.track_utility` off
  for its transaction (`SET LOCAL`, `pgExecRole`) where the connection's account is a superuser, the
  account that may change the setting, so the extension keeps no copy of the statement for its readers
  (a member of `pg_read_all_stats`, a monitoring role's usual grant, reads every account's); for any
  other account it is recorded as sent and the statement list shapes it. It is not sent as a SCRAM secret hashed here, which the server would store
  as given: a password check the server runs on a new password (`passwordcheck`, `credcheck`) cannot
  judge a hash, and `credcheck` refuses one.
  Audited as `database.role.create/alter/drop/grant/revoke`, `database.create`,
  `database.connection.sibling`, `database.extension.create/update/drop/preload`.
  - *What else the server holds.* `GET /databases/{id}/schemas` (read surface) is the list the
    engine's picker is filled with, as `[{name, size?, owner?, encoding?}]`: the server's databases
    on PostgreSQL (templates left out), MySQL/MariaDB (each schema, with its tables' size), SQL
    Server (user databases and the one the connection is on) and ClickHouse (`owner` is the
    database engine); Oracle's schemas, because a schema is the unit one browses there; SQLite's
    attached files (`main` first, `owner` the path); MongoDB's databases; and Redis's numbered
    databases with `size` as the count of keys, not bytes. Whether one of them can be opened, or
    another made, is two capability flags, `serverDatabaseConnect` and `serverDatabaseCreate`
    (`dbx.CanConnectSibling`, `dbx.CanCreateDatabase`, in `admin_capabilities.go`), and the two
    routes ask the same functions before they do anything, answering `400 unsupported` where the
    answer is no. ClickHouse can make a database and cannot be pointed at it by a connection
    string, so a create that asks for `connect` there is refused before the database is made; it
    used to be made and then answered as a failure.
  - *Extensions* (`dbx/extensions.go`, `dbx/extension_catalogue.go`, `api/handlers_db_extensions.go`).
    `dbx.ExtensionManager` is an optional half of `Admin` that only the Postgres dialect implements,
    and `editable` on the list is whether the engine has one: MySQL lists its plugins and changes
    none, CockroachDB and TiDB have no `extensions` flag. The list is `pg_available_extensions`
    joined to what is installed, with the default version's `superuser`/`trusted` (`superuserOnly`,
    from 13 where `trusted` exists), laid over a curated catalogue: a category and a sentence for
    about ninety common extensions, the library each needs at start, a row for each *module* — a
    library with no extension, `auto_explain`, `passwordcheck`, `pg_partman_bgw` — listed when its
    file is in the server's library directory or it is loaded, and a row marked `unavailable` for
    each extension that is somebody else's and not in the image (pgvector, PostGIS, TimescaleDB,
    pg_cron…; PostgreSQL's own contrib is never listed as missing, because a release left it out).
    `GET …/server/extensions/{name}` is what a dialog reads before it acts: the versions shipped
    newest first, each with `requires` and the requirements the database lacks (`missing`), the
    versions the installed one has an update path to (`pg_extension_update_paths`, asked only here
    because it works out every pair), the schemas the account may create in and `current_schema()`,
    what it added by kind, what outside it depends on it and the two drop statements. Every option
    of a create, an update and a drop is checked against that catalogue before a statement is written
    — a version the server ships, a schema that exists, the schema a control file fixes, an update
    target with a path — and the three routes end in `runDDL`, so `?preview=1` plans without running
    and is not audited, and the statement shown is the one that runs. A create keeps
    `IF NOT EXISTS` and refuses an extension already installed; the version is a literal the server
    listed. The dependents are `pg_depend` walked the way the engine walks it rather than a trial
    `DROP` in a rolled-back transaction, which would lock everything it would remove and fire event
    triggers from a read: what goes with the extension by itself (members, and what is internal to or
    automatically dropped with one), every object depending on that normally and what depends on
    those, each named as the object a reader knows (a member of another extension is that extension,
    a view's rewrite rule its view) and with whatever is carried by another entry left out — the same
    five objects the engine's own refusal names, up to a hundred. `DELETE …/{name}?cascade=1` is the
    one way to `CASCADE`, chosen by the reader in the dialog after the list.
  - *Libraries loaded at start* (`dbx/preload.go`). `dbx.Preloader` reads `shared_preload_libraries`
    as loaded and as the configuration files name it (`pg_file_settings`, a superuser's; the last
    line naming it, which is `postgresql.auto.conf`'s after an `ALTER SYSTEM`), and whether it can be
    changed from this connection: not on YugabyteDB or CockroachDB, not where the value comes from the
    command line (`-c` outranks `ALTER SYSTEM`, and the shared Postgres fixture is started that way),
    not for an account that is not a superuser. `POST …/server/preload {library}` takes a library or
    the extension it is loaded for from the catalogue's closed list, never a name from the request,
    and refuses one already on the list or one whose file is not in any `dynamic_library_path`
    directory (`pg_stat_file` beside `pg_config`'s `PKGLIBDIR`), because a server told to load a
    missing library does not start. The new list is the files' list — so a change not yet restarted
    for is kept — with the library appended (`citus` first) and every entry kept in its order,
    written as one literal per library (`pgSettingLiteral`), then the parameters the library is
    pointless without where nobody has set them (`cron.database_name` and `pg_partman_bgw.dbname` to
    the connection's database, `auto_explain.log_min_duration` to `1s`) — on 17 and later, which take
    an unknown dotted parameter as a placeholder; before that the plan says to set it in Parameters
    after the restart — and `pg_reload_conf()`. It runs on one connection, `?preview=1` shows it, the
    reply carries the list the next start loads and a note per parameter, and it is audited as
    `database.extension.preload`. Nothing here restarts the server: that is `POST …/power`, with its
    own capability and audit, and the create route makes the extension once the library is loaded.
    The route and the dialog that walks the three steps (`components/database/ops/extension-preload.tsx`)
    take any library in the catalogue, pg_stat_statements included, so another page's guided action
    for one extension is the same two pieces opened on it.
  - *An alter changes what was sent and nothing else.* `roleRequest`'s attributes are pointers and
    `RoleSpec` carries a `Set*` beside each shared flag, so a body with only a password renders
    `ALTER ROLE … WITH PASSWORD …` and leaves `SUPERUSER`/`CREATEDB`/`CREATEROLE` as they were (the
    old renderer wrote all four from booleans that were false when absent, and a password change
    demoted a superuser). A request is honoured whole or refused: `dbx.CheckRoleRequest` runs in the
    handler before any engine is dialled and answers `400` naming the attribute for anything outside
    `EditableRoleAttributes(driver)`, and for `superuser:false` on the engines that can only grant
    (ClickHouse, MongoDB, Redis) — Redis and MongoDB read a password and one flag and used to answer
    `200` to a request to lock an account they had not locked. A create is checked the same way on
    what it *asks for* (a form may send every field at its default); MySQL and SQL Server creates
    now carry the lock and the create-account right, and drop the half-made account when a later
    statement fails. `ownAccountRefusal` refuses `login:false`, `locked:true` and `superuser:false`
    on the account the connection signs in with, as the drop route does: the pool would carry on
    and every later dial would fail with nothing here able to undo it. A password change on that
    account re-seals the saved connection string once the new one has been seen to open
    (`resealOwnPassword`, reported as `connectionUpdated`).
  - *A drop closes what the forms opened.* PostgreSQL refuses to drop a role that still holds a
    privilege, and the three-level grant below gives it several: `postgresDialect.DropRole` runs
    `DROP OWNED BY` and `DROP ROLE` in one transaction — but only for a role that owns nothing in
    this database (`pg_shdepend`), because `DROP OWNED` also drops what a role owns, and dropping an
    account must not drop its tables. A role that owns something, or holds grants in another
    database, is refused in the engine's words and nothing was revoked. SQL Server's `DropRole`
    first drops the user the login has in each database it can reach (found by `SUSER_SID`, the
    one the grant forms made with `CREATE USER … FOR LOGIN`), which `DROP LOGIN` leaves orphaned
    with its grants; a user that owns a schema stops the drop before the login is touched. Both
    refusals are `dbx.ErrRoleInUse` and answer `400`, not `502`. Redis has no database to grant:
    `POST …/server/roles/{name}/grant` answers `400` there, with or without `?preview=1`.
  - *The three-level grant* (`POST …/server/roles/{name}/grant`, `read|write|all`) runs in one transaction
    on PostgreSQL and covers every schema of the database that is not the engine's own, **an
    extension's** (membership in `pg_depend`: pg_cron's `cron`, TimescaleDB's catalogues) **or a
    platform's** (`hdb_catalog`; Supabase's `auth`, `storage`, `vault`… recognised by name only on a
    server that has the `supabase_admin` role) — `pgGrantSchemas`. The reply and the audit detail
    list `schemas` and `skippedSchemas` with the reason; `schema` in the body names one schema and
    grants on it regardless. Default privileges are written for the connection's account and, with
    `FOR ROLE`, for every other role that owns the schema or a table or sequence in it and that the
    account is a member of (`pgFutureOwners`); owners it cannot speak for come back in `notes`.
    `?preview=1` returns the same body with `preview:true` and executes nothing. MySQL escapes `_`
    and `%` in the database name, which is a pattern at that position.
  - *Fine-grained privileges.* `GET …/server/privileges` publishes the closed set per engine and
    level (`PrivilegeLevels`: database, schema, table, sequence, function, column and role membership
    on PostgreSQL;
    database and table on MySQL/ClickHouse; database, schema, table on SQL Server); a request names
    privileges from that set and the keyword that reaches the statement is the set's.
    `POST …/server/roles/{name}/privileges` and its `/revoke` (`system.admin`, `?preview=1`) render
    through `PrivilegeStatements` and run through `ChangePrivileges`, in one transaction where the
    engine has transactional grants, connected to the database that holds the object. `future:true`
    resolves the same owners as the preset and returns them as `futureOwners`; that is the one
    preview that dials. `GET …/server/grants` lists grants by role or by object (`aclexplode` over
    databases, schemas, relations and `pg_default_acl`; `SHOW GRANTS` parsed on MySQL, for every
    host a name has when none is given, the line's password hash never returned;
    `system.grants`; `sys.database_permissions`), bounded at 1000.
    PostgreSQL's two levels beyond the object kinds are `function` — EXECUTE on one function by its
    name and the argument types the catalogue lists (`routine`, `arguments`; no `arguments` names it by
    its name alone), or on every function of a schema, with `future:true` for the ones made later —
    and `column`: SELECT, INSERT, UPDATE and REFERENCES on named `columns` of a table. The statements
    are `GRANT EXECUTE ON ROUTINE …` (a function, a procedure or an aggregate) and `GRANT SELECT
    (a, b), … ON TABLE …`; the argument list is confined by `validateFragment` and a character class
    to its own parentheses, because it follows the function's name and what follows is the rest of
    the statement. `GET …/server/grants` lists both (`aclexplode` over `pg_proc.proacl` and
    `pg_attribute.attacl`; a function by `routine` and `arguments`, a column by `table` and
    `column`), and `pg_default_acl` rows for functions. The Access page's matrix asks for a whole
    schema when every object it lists is pressed alike (`plannedRequests`), and object by object when
    the schema holds ones it does not list — the functions an extension installed, which it hides, or
    a group the catalogue cut off at its limit — because `ALL … IN SCHEMA` would reach those as well.
  - *What a non-administrator is not shown.* The role list and detail are on the read surface, so
    two things are withheld there: the password entries of a Redis ACL rule (`withoutACLSecrets`),
    and the values of a PostgreSQL role's own settings (`rolconfig`) whose name is one the settings
    redaction matches or carries a dot — a parameter PostgreSQL does not define, which is where
    PostgREST's `pgrst.jwt_secret` and applications' tokens live. The names come back in
    `configRedacted` (`redactRoleConfig`).
- **The server's own log, found from its connection.** `GET /databases/{id}/logs/sources`
  (`handlers_db_logs.go`, on the read surface — reading what the server printed is what `/activity`
  already shows any role) answers `{sources, refused?, reason?, note?}`, each source in `/logs/sources`'
  shape plus `primary`, read through the engine's lens by the connection's driver (`driverLens` — a
  Postgres in a custom image is still Postgres). A SQLite file and a server on another machine have none,
  with the reason. A container behind the connection is `docker:<name>` — the name, so the log survives a
  recreate. One the connection dials at its own private address is found by that address whatever its
  image (`containerAt`): one built in-house, or whose moved tag the list names by id, is still the engine
  the connection says. A server on this machine is followed from its port: the listener
  (`proxysvc.ListListeners`; behind `docker-proxy`, the container publishing the port, whatever its image
  says), its process's manager (`procs.ManagerOf`, where a `container` manager is `docker:<name>` again),
  the files it and up to 64 of its children hold open **for appending** (`/proc/<pid>/fd` with `fdinfo`'s
  flags, log-like names only — a data file is opened for writing, a log for appending), the engine
  package's conventional files that exist (`/var/log/postgresql/postgresql-<V>-<C>.log` from the Debian
  unit's instance name, MySQL's, Redis's, MongoDB's, ClickHouse's), then the unit's journal. The first
  file is primary even at 0 bytes — rotation just emptied it, and the page opens it with its rotated set
  — and a journal is primary only when no file is the server's (Debian's Postgres writes nothing there
  but systemd's starts and stops). A path the log roots refuse is listed under `refused` with the reason
  rather than dropped, and the journal beside it is not made primary, since the statements are in that
  file. With nothing listening — stopped, crashed or starting, which is when a log is read most — it is
  found the ways a stopped server can be: a container publishing the port without `docker-proxy`, the
  container the connection is named after (the sync names adopted containers so), running or not, and the
  engine's units by name, skipping one serving another port and Debian's umbrella `postgresql.service`,
  with their files and journals and a `note` saying the server is not answering. Nothing goes through
  `hostexec`: `/proc` is read, the units come from the same systemd listing `/logs/sources` uses, and
  every file through `logs.Allow`. One resolution answers for 45 seconds per connection
  (`Server.dbLogSourcesKept`, keyed on a hash of the DSN, never stored when the request's deadline cut it
  short), since the page and its Queries view ask on different cadences and each asking walks every
  process's sockets.
- **The statements it recorded.** `GET /databases/{id}/querylog?since&until&limit&minMs` (read, 15
  seconds; no `since` is the last day, `limit` 200 up to 500, a malformed bound a 400) answers
  `{supported, reason?, source, enable?, threshold?, entries, truncated}` from wherever the engine keeps
  its slow statements: Postgres's server log searched through its lens for `event:slow`, rotated files
  included, each statement rejoined from its continuation lines (the search budgets 50 lines per row,
  capped at 20 000, and counts rows once joined) with `enable` the `ALTER SYSTEM` for
  `log_min_duration_statement` and its current value from `pg_settings`; MySQL's `mysql.slow_log` where
  `log_output` has `TABLE` and the slow log is on (the `minMs` floor in the SQL), else
  `performance_schema.events_statements_history` from `long_query_time` up; MariaDB's `slow_log` only;
  Redis's `SLOWLOG GET 128`; MongoDB's log through its lens, or `getLog global` over the connection when
  the server is elsewhere or its file is refused; ClickHouse's `system.query_log`, initial queries past
  `QueryStart` that took 250 ms or more (`minMs` raises the floor; `threshold` names it), since the log
  holds every query and the newest few hundred of a busy server's would be its last few seconds, with
  its own read marked and left out. SQL Server, Oracle and SQLite are
  `supported: false` with the reason, and so is a Postgres whose log the roots refuse, naming the path
  and `JD_LOG_ROOTS`. `fp` is `logsx.Fingerprint` — over the statement, over Redis's command and key
  shape, over a Mongo command's shape — shared with the lenses, so a statement found in the log and in
  the Queries list is one group (ClickHouse's is `normalized_query_hash` as 12 hex digits). A read that runs
  out of time sets `truncated` with a reason rather than passing a partial list off as the window. Like
  `/activity`, and unlike `/statements`, it returns statement text with its literals at read capability.

### Alerts: told when a server or its backups go wrong

A database's **Alerts** page (`/databases/{id}/alerts`, in the rail under Operate) holds rules that
tell the deployments' notification channels — webhook, Discord, Slack, Telegram and e-mail, through
`deploy.AutomationStore.DeliverNotification`, so a channel is configured once on
`/deploy/notifications` and nowhere else — when something is wrong with a database, and when it is
over. There is no second delivery system and no second poll: the server kinds are judged on the
recorder's own thirty-second pass (`api/database_metrics.go` hands `dbAlerts.Evaluate` what it read),
and the backup kinds on the same pass from `backup_jobs` and `backup_runs`.

- **Six kinds, stated per engine.** `alertKinds` (`dbx/alert_capabilities.go`, a list flag) is what a
  database of an engine can be watched for, and the create route refuses a rule for one database
  whose engine lacks the kind. A server can be `down` (it did not answer the recorder; a SQL pool
  whose snapshot was refused is pinged to tell a refusal from a server that went away), fill its
  `disk`, run near its `connections` limit (the snapshot's sessions against `max_connections` less
  the superuser reserve, Redis `connected_clients` against `maxclients` — now recorded with the
  counters — and MongoDB `current` against `current + available`), and fall behind in
  `replication`; every engine, SQLite included, can have a backup job that failed
  (`backup_failed`: the job's newest finished run failed) or was missed (`backup_missed`: a scheduled,
  enabled job has had no successful run since the time its schedule was next due after its last
  success — `backups.NextScheduled`, in the zone the scheduler fires in — plus the rule's grace, and
  no run is in flight). Connections are not offered on CockroachDB, TiDB or FerretDB, whose
  snapshots count none; replication is offered exactly where the replication view is.
- **What is read, and only when a rule needs it.** The recorder reads disk and replication only
  for a server an enabled rule watches for them. Replication (`dbx.ReadReplicationLag`,
  `MongoReplicationLag`, `RedisReplicationLag`) is the worst lag a server can see: a PostgreSQL
  standby's replay delay, counted as nothing once it has replayed all it received (an idle primary
  is not lag), or a primary's `replay_lag`; a MySQL or MariaDB replica's `Seconds_Behind_Source`, a
  stopped thread being a stop without the engine's error, which quotes rows; every secondary of a
  MongoDB set against its primary, a member not answering or no primary being a stop; a Redis
  replica's seconds since it heard from its primary, or a primary's slowest replica. A MySQL primary
  cannot see its replicas' lag and says so rather than reading zero. The disk is the engine's own
  answer where it gives one, which also covers a server on another machine (ClickHouse
  `system.disks`, SQL Server `sys.dm_os_volume_stats`, MongoDB `dbStats`' `fsUsedSize` and
  `fsTotalSize`); otherwise the server is placed the way its summary places it — a container's mount
  under the data directory the engine names (`data_directory`, `@@datadir`, Redis `CONFIG GET dir`)
  or under its image's usual one (`dbx.DataDirHints`), Docker's own disk when no mount holds it; a
  server on this machine's data directory, else the root disk — and that filesystem measured through
  `/host` as `df` would (`statDisk`). A server elsewhere that does not report its disk is not judged,
  and says why. The place is kept five minutes per connection and DSN.
- **Hold, hysteresis and cooldown** (`stepDBAlert`, one pure function). A server reading must be
  over the line on two passes in a row and for the rule's minutes before it fires; it recovers once
  it has been under a clearing line — five points under a percentage, a fifth under a lag — on two
  passes, and between the line and the clearing line it stays firing. A backup run is a fact, so one
  reading fires it and one recovers it. The cooldown keeps a subject that fires again soon after its
  last message from being told again until the cooldown has passed, and tells it then if it is still
  firing; a recovery whose firing was kept back is not told either, and a rule may tell firings only
  (`notifyRecovery`). A reading that could not be taken — the server down, the disk unreadable, the
  pass out of time — keeps the subject where it was rather than recovering it. A subject the rule no
  longer reaches (a job that stopped dumping the database) leaves, told it is over if it was firing.
- **State is the store's.** `db_alert_rules` (a `connection_id` of NULL watches every database),
  `db_alert_state` (one row per rule and subject: a database, or `job:<id>`) and `db_alert_events`
  (the newest thousand, with how many channels were told and what was kept back and why) are new
  tables; rules and events of a forgotten connection go with it. A rule's `revision` moves on every
  edit and a pass writes a subject only while the revision it read still holds, so an edit made while
  a pass was deciding is never overwritten and nothing decided on its old terms is told. An edit to
  the kind, the line, the minutes or the database, or a pause, starts the rule over; its channels,
  cooldown and recovery setting do not.
- **The messages.** `database.alert.firing` and `database.alert.recovered` carry a `database` block
  in the webhook envelope (rule, kind, connection or job, label, the reading in words with its line,
  since when, `test`), and the providers render it as "Database disk filling up — shop" with the
  reading as the summary; the link opens the database's Alerts page, or its Backups page (the
  server's, for a rule over every database) for a backup kind. A test sends the rule's message,
  marked `[test]`, to its channels and counts the delivered, failed and paused ones. The reading of a
  server that is down is the driver's error with the connection's secrets scrubbed (`connectError`).
- **Who may.** Reading the rules, their subjects and their events is the read surface, as the
  database's other readings are; creating a rule and sending its test need `system.admin`, and
  editing (which is also how a rule is paused) and removing one sit in `s.destructive` as well, as
  the proxy's alert rules do, because both stop somebody being told. Every mutation is audited as
  `database.alert.create`, `.update`, `.delete` or `.test`, with the rule's terms and never a reading.
  The routes are not under `/{id}`: a rule can watch every database, and one rule for one database
  is the dashboard's own state, which a protected connection does not refuse.

### Redis

How a console line, a bulk action and an overwriting rename or copy are classified — which is what
decides the capability each needs — is in
[request lifecycle](../architecture/request-lifecycle.md#redis-one-route-any-command). What follows
is the surface itself.

- **Keys and the server are two families of routes.** Routes under `/databases/{id}/keys` are about what
  is stored and under `/databases/{id}/redis` about the server (`handlers_db_redis*.go`;
  `dbx/redis*.go`). They serve Redis, Valkey, KeyDB and Dragonfly. No `?db=` means the logical database
  the connection string names (`dbx.RedisDSNDatabase`), which is not database 0; a database the server
  will not select is a `400` when the request named it and `connect_failed` when the connection string
  did. Every key, value, member and field is a `dbx.RedisBytes`: a JSON string when the bytes are UTF-8
  and `{"base64": …}` when they are not, in both directions, so nothing is mangled. A request
  distinguishes an absent field from an empty one: on `DELETE /keys`, `member`, `members`, `index`,
  `expect` or `path` being present makes it a request about the inside of one key, and one that then
  names nothing there (`"members": []`) is refused rather than read as a request for the key. Reads
  inside a key are pages (`/keys/members`: `HSCAN`/`SSCAN`/`ZSCAN` cursors, `LRANGE`/`ZRANGE` windows,
  `XRANGE`, `GETRANGE`); writes name one member and run under `WATCH`, a string save keeps its expiry
  (`KEEPTTL`), a list is edited by position with the element the caller saw there (`expect`), and
  renaming a member or field onto one that exists is a `409`, not a removal. An expiry reaches the server
  as the integer it was given and is read back as the integer the server answers: the driver's
  `time.Duration` holds 292 years, and an expiry set for the year 9999 wrapped to a negative one, which
  Redis reads as a delete. One further off than `redisMaxExpiryMs` (2^53−1 ms since the epoch) is
  refused, because servers before 6.2 overflow near the top of the range. The console and the paged read
  speak the protocol themselves through `redisReplyReader` (`dbx/redis_wire.go`), which keeps only what a
  page shows — a console reply is abandoned past 10 000 values or 2 MiB, a member is carried up to 64 KiB
  with its real size — because the client library reads a whole reply into memory first. `dbx.RedisProbe`
  asks each server what it is (flavour; standalone, cluster node, sentinel) and which optional commands
  it has, and a command the server lacks is not sent: the field is absent and the response says why.
  Redis values never enter the audit log: entries carry key names, types and counts, and for
  administrative commands the parameter or account name; a refusal's sentence, which is recorded, quotes
  no member, field or value. Configuration secrets (`requirepass`, `masterauth`, anything named like a
  password) and ACL password hashes are never returned. `PUT /redis/acl/{name}` refuses to switch off, or
  take commands or keys away from, the account the dashboard itself connects as, and says so when that
  account's password changes under the saved connection. `GET /redis/config` is the configuration proper;
  `/server/settings` for Redis is `INFO`, now with a `counters.*` group. `redisCommandWrites` and
  `redisBulkWrites` (`handlers_db_redis_readonly.go`) say from a request body alone whether the console
  or the bulk route would change anything, and the guard for protected connections stands on them: it
  lets a console read, a classify and a bulk dry run through and nothing else of Redis's. On `DELETE
  /keys` an empty `path` is refused like an empty `members`, and on `DELETE /keys/stream/groups`
  `consumer` is read by presence: `""` removes the consumer named `""`, and only its absence destroys the
  group.
  - *Looking at a key is not using it.* Redis keeps one clock per key and evicts by it, and the
    dashboard's reads reset it: a listing measures each key, a page reads it. The routes that only
    look — `/keys`, `/keys/tree`, `/keys/meta`, `/keys/members`, `/keys/value`, `/keys/raw`,
    `/keys/stream`, `/keys/stream/pending`, `/redis/analysis` — open a quiet client
    (`RedisOpenOptions.Quiet`, `Server.redisReader`), which sends `CLIENT NO-TOUCH ON` on each of
    its connections and on the raw one a page is read over, and a dump does the same, so a nightly
    backup no longer makes every key its server's most recently used. Redis has the command from
    7.2 and Valkey throughout; a server without it answers with an error that is ignored, and
    `features.noTouch` on `GET /redis/server` says which a server is. A write made from the
    dashboard is use, and counts.
  - *One field's expiry.* `POST /keys/field/expire {key, field | members, ttl | ttlMs | at}` and
    `POST /keys/field/persist {key, field | members}` are the key's two routes a level down, for a
    server with per-field expiry (`features.hashFieldTtl`; the flag `hashFieldTtl` names the
    release): the same three ways of saying when, a ttl of zero or less that removes the expiry and
    never the field, and a moment that has passed refused, because setting it deletes the field.
    They answer `{ok, fields: [{field, pttl}]}` (`-1` no expiry, `-2` no such field), `404` when
    none of the fields is there, and `400 unsupported` on a server that keeps none. Both are
    `service.control`, refused on a protected connection, and audited as
    `database.redis.field.expire` / `.persist` with the key and how many fields — a field's name is
    data and is not recorded. A hash row's expiry on the key browser is set and removed through
    them; it used to send `HEXPIRE` and `HPERSIST` down the console's route.
  - *Claiming.* `POST /keys/stream/claim {key, group, consumer, ids, minIdleMs?}` hands a group's
    pending entries to one of its consumers (`XCLAIM … JUSTID`), and with `auto: true` whatever has
    been pending for `minIdleMs`, a page at a time by `cursor` and `count` (`XAUTOCLAIM`,
    `features.streamAutoClaim`; `400 unsupported` without it). Only ids travel, in both
    directions. `service.control`, as the console classes both commands; refused on a protected
    connection; audited as `database.redis.stream.claim`. A group's pending list claims through it:
    *Claim…* hands one entry over, and *Claim idle entries…*, drawn only where the server has
    `XAUTOCLAIM`, hands whatever has been pending past a number of minutes to one consumer, a
    hundred a press, saying how many moved and whether more remain. The page used to send `XCLAIM`
    down the console's route.
  - *A whole member.* `GET /keys/raw` also streams one member of a set or a sorted set. A member
    has no name but itself and the reader holds only its start, so it is asked for by that:
    `member` (or `memberB64`) is how it begins and `bytes` its size, both as the page reported
    them; without `bytes` the member is named whole. The collection is walked with `SSCAN`/`ZSCAN`
    narrowed to that beginning and the first member of that size that begins so is copied through
    from the reply it arrived in (`redisCopyMember`); nothing is held.

### MongoDB

How a pipeline and a console command are read to decide what they need is stated once in
[request lifecycle](../architecture/request-lifecycle.md#mongodb-a-pipeline-or-a-command-is-classified-after-it-is-parsed);
the detail of each classifier is below, with the surface it guards.

- **MongoDB has a surface of its own.** Routes under `/databases/{id}/mongo/`
  (`api/handlers_db_mongo*.go`, `dbx/mongo_*.go`) do not pass documents through the SQL grid. A document
  crosses the wire as **canonical Extended JSON** with a relaxed copy for display (`dbx.MongoDoc`), is
  addressed by its `_id` as Extended JSON whatever type that is, and is decoded into `bson.Raw`, so a
  document read and saved back unchanged is byte-identical (`TestCanonicalRoundTripIsByteIdentical`, and
  live in `TestLiveMongoDocuments`). It also carries a `digest` (SHA-256 of its BSON), which a replace
  sends back as `expectedDigest` so an edit never overwrites a change made since the read: the stored
  document is re-read and compared, and the server repeats the comparison in the operation that writes
  whenever both documents fit one operation (15 MiB together). The two routes that carry whole documents
  read a body of up to 64 MiB (`decodeMongoDocuments`), because a 16 MiB document is several times that
  as canonical text. The display copy leaves a 64-bit integer too large for a double in its `$numberLong`
  form, because a browser would show a different number. Every text field — filter, sort, document,
  pipeline, command — is Extended JSON or the shell's spelling (`ObjectId("…")`, `ISODate("…")`, unquoted
  keys, single quotes): `mongo_shell.go` rewrites the shell form into Extended JSON and is tried only
  when the text is not valid JSON already, since the driver's reader stops at the end of the first value
  and would accept `{} garbage`.
  - *Reads are on the read surface* even where they are POSTs (a filter is a document, not a URL
    parameter): `find` (with a count that is exact when it can be had in five seconds and the
    collection's estimate otherwise), `count`, `document`, `explain`, `aggregate/preview`, `schema`,
    `validation/check`, `export`, and the GETs for collections, indexes, validation, the server
    snapshot, per-database `dbStats`, `currentOp`, the profiler, replication, users and roles. Each
    carries a Max time the server enforces (default 30 s, at most 5 min).
  - *Writes need `service.control`*; `DELETE`s of documents, collections, indexes and `killop` are
    under `s.destructive`; the profiler and accounts need `system.admin`. Several routine routes have
    one option that removes data and check it by hand with `mongoNeedsDestructive` (capability and
    `destrLim`): an update of every document (an empty filter with `many`, which must also say
    `all: true`), a rename with `dropTarget`, a TTL index or a new TTL limit, a smaller cap or a new
    expiry through `collMod`, and a pipeline that writes.
  - *A pipeline is classified after it is parsed* (`dbx.MongoClassifyPipeline`): stage names are read
    off the parsed stages, sub-pipelines of `$lookup`, `$unionWith` and `$facet` included, so
    `$mergeObjects` is not `$merge` and `"$\u006fut"` is `$out`. A stage that is not on the list of
    known reads makes the pipeline destructive. A preview never runs a writing stage and refuses an
    unknown one, which is why it needs no capability.
  - *The console* (`POST …/mongo/command`) classifies a command by its first key against
    `mongoCommands`: read, write, destructive or blocked, plus an `admin` flag for what the forms keep
    to `system.admin` (accounts, the profiler, server parameters). Arguments can raise the class
    (`aggregate` with `$out`, `update` with an empty filter and `multi`, `findAndModify` with `remove`,
    `createIndexes` with a TTL, `profile` with anything beside a negative level, and an `explain` of
    an `aggregate` whose pipeline writes or holds an unknown stage unless its `verbosity` is
    `queryPlanner` — with execution the pipeline runs, and the console does not leave refusing that
    to the server). A flag counts as
    set unless it is absent, null, `false` or a numeric zero (`mongoFlagSet`): servers differ in what
    they read as true, and 7.0 takes a Decimal128 for `showCredentials` and a string for `repair`. A
    command that sets a field twice is refused, since which one a server reads depends on the server.
    A command not in the table is destructive; `shutdown`, the `replSet*`
    reconfiguration commands, `eval`/`mapReduce`, `applyOps`, sessions and transactions, and
    `usersInfo` with `showCredentials` are never run. The audit entry is the command's name and the
    collection it names, never its body, and is set before the gate so a refusal is recorded under
    the command's own name.
  - *Credentials are never documents.* `admin.system.users`, `admin.system.keys` and `local.oplog.rs`
    (whose entries for an account being made carry its verifier) are refused by every route that
    reads or writes documents, the first browser's included (`mongoNamespace`), by a pipeline stage
    that names them, across databases too (`mongoGuardPipeline`), and by the console
    (`MongoGuardCommand`, which also refuses a collection named by UUID in those databases and a
    view made over them). In `admin` and `local` a read first lists the database's views and follows
    the one it was asked for to what it reads (`mongoGuardRead`), so a view somebody made over
    `system.users` with a shell is refused as well. Not followed: a view in any *other* database
    whose pipeline joins the replication log, when it was made outside the dashboard.
  - The first Mongo browser's routes (`/documents`, `/collections`, `/collections/indexes`,
    `/aggregate`) still answer in their old shapes. `/stats` returns the keys it had plus the counters
    a chart is drawn from and the server's own clock (`timestamp`), so rates are a difference of two
    snapshots. Audited as `database.document.insert/replace/update/delete/clone`,
    `database.collection.create/rename/modify/drop`, `database.index.create/modify/drop`,
    `database.validation.set`, `database.aggregate`, `database.mongo.command/killop/profiler`,
    `database.role.create/alter/grant/revoke/drop`, and `database.export` for the Mongo export, which
    connects and opens its cursor before the first header so a failure is an error response rather
    than an empty file. That entry is written before the first byte, and an export that breaks off
    afterwards gets a second one marked as a failure.

### Code generation

- **Code generation is one model and seventeen writers.** `POST /databases/{id}/orm` and
  `GET /databases/orm/targets` stay on the read surface. `orm_catalog.go` reads the schema into an
  `ORMSchema` — the same `Tables`/`Columns`/`Indexes`/`ForeignKeys` calls `Detail` makes, then per-engine
  catalogue facts those do not carry: PostgreSQL's real type names (`format_type`, with a domain resolved
  to its base), which type is an enum and its labels, identity and generated columns, which index is
  partial, over an expression, on another access method or descending (with `pg_get_indexdef` kept for
  the SQL target), which relation is a partition; MySQL's `EXTRA` (auto-increment, expression default,
  `ON UPDATE`); SQL Server's identity, computed columns, filtered indexes and `INCLUDE` columns; Oracle's
  character lengths. Those queries are best effort: one that fails costs a detail and adds a warning.
  Tables are keyed by schema and name throughout, so the same table name in two schemas is two models.
  Types and defaults are parsed per engine (`orm_types.go`, `orm_defaults.go`); a name one engine's
  reader does not know stays unknown and is reported, never treated as PostgreSQL would treat it. Each
  target (`orm_<target>.go`) is a pure function of that model, listed in `orm_targets.go` with its
  language, group, the engines it exists for and its switches — which is what the picker is drawn from,
  and why a target with no connector for an engine (Prisma for ClickHouse or Oracle, Drizzle for SQL
  Server) is refused with the reason before the connection is opened, where it used to come out as a
  PostgreSQL schema. What a target cannot express — a partial index, a composite foreign key in
  Sequelize, a table with no key in Prisma — comes back in `warnings`, and `schema`/`filename` repeat
  `files[0]` for callers that only know those two. The CREATE statement an engine keeps is read only for
  the SQL target (`ORMScope.Statements`): on Oracle `DBMS_METADATA` can take seconds a table. A view's
  statement is asked for as a view (`pg_get_viewdef`, `sys.sql_modules`, `DBMS_METADATA` with `'VIEW'`),
  never taken from the dialect's `CreateSQL`, which on those three engines answers a view with a CREATE
  TABLE built from its columns; SQL Server's goes into the script inside `EXEC`, because CREATE VIEW has
  to open its batch. `counts` are what is in the files: a model a target leaves out (and says so) is not
  counted. The tests are golden files per target, engine and option (`testdata/orm`, rewritten with
  `-update-orm`), line-level expectations beside them, and live tests that skip unless a DSN is set; the
  PostgreSQL, SQL Server and Oracle ones drop what they made, run the generated SQL and require the same
  schema back.

### Seed data

- **Rows of mock data for tables that exist, beside the code generators** (`dbx/seed*.go`,
  `api/handlers_db_seed.go`). `POST /databases/{id}/seed/plan` is a read: it loads the tables through
  `LoadORMSchema` and the generators' prepared model, so it knows what they know — which column the
  engine numbers itself (an identity, a serial, AUTO_INCREMENT, SQLite's rowid), which is computed or a
  rowversion (`importComputedColumns` beside the model's own flag), an enum and its labels, the primary
  key and the usable unique indexes — and answers with the tables in the order they are written, how
  every column is filled and the first eight rows of each. It is open on a protected connection and
  records nothing, as `/orm` does. `POST /databases/{id}/seed` writes the same request at
  `service.control`, is refused on a protected connection, and is audited as `database.seed` with the
  seed number and each table's count — never a value. The seed number fixes the random stream: a
  column's value in row *n* depends on the seed, the table, the column and *n*, and on nothing else, so
  the preview is the rows the write writes. A request that names none is given one, which the answer
  carries; the page sends one always and moves to a new one after a write.
- **A column's generator** (`seed_generators.go`, forty-seven of them: people, places, business,
  internet, text, numbers, moments, a value of the column's own list, JSON, bytes) is chosen from its
  name in words first (`createdAt`, `created_at` and `CREATED_AT` are the same two) and its type
  second (`seed_infer.go`), and never one the type rules out; a bare `name` reads by its table (a
  person's, a product's, a company's, a category's). What the column's definition holds it to wins over
  both: an enum's labels, a set, and a CHECK of the two shapes read — the column in a list (PostgreSQL's
  `= ANY (ARRAY[…])`, SQL Server's chain of `OR`ed equalities) and the column between numeric bounds.
  CHECKs come from PostgreSQL's catalogue with the model, from the CREATE text on MySQL and SQLite,
  and from `sys.check_constraints` and `all_constraints` on SQL Server and Oracle; anything else is the
  table's to refuse. A column with a default and nothing in its name or its checks to say what it holds
  keeps its default. The reader may choose another generator per column from the plan's `choices`; each
  column also says which one it opens with (`inferred`). A choice the table no longer takes — the page
  remembers choices, and a column may since have been dropped, retyped or given to the server — is set
  aside with a warning rather than refusing the seed, and the page forgets it. Every address and number is one that reaches
  nobody: RFC 2606's example domains, RFC 5737's documentation ranges, the 555-01xx and 07700 900xxx
  ranges set aside for fiction. A value is made as the text an import would carry and is bound by the
  import's own `bindValue`, so a seed reaches an engine by the road a file does.
- **Keys.** A foreign key holds a value its parent has. Tables are ordered parents first; a parent the
  same seed fills is written first and its new rows are what the child points at — the values the seed
  made for them, or, for a key the server numbers, the rows past the largest number it held before.
  A parent outside the seed is read for up to 10,000 of the keys it has. A table that points at itself
  is written in chunks of about a fifth of its rows, reading its own keys between them, so its first rows
  are empty there and the rest point at earlier ones. Tables that point at each other are broken at a key
  that can be left empty and closes the cycle, which then points at what its parent held before the
  seed; a table that only points into a cycle keeps its key, and a cycle with no such key leaves its own
  tables out and no others. A one-to-one key points at each parent row once: one that must be filled
  caps the table's rows at the parent's, and one that may be empty is empty past them (on SQL Server,
  whose unique index takes a single NULL, one row past them). A unique column's values are kept apart —
  a number counts up from past the largest one there, a text carries the row's own number counted from
  the table's rows (the one thing a table is counted for, since a count is a scan on some engines) — and
  the keys of several columns are drawn again, from random streams of their own so that no other row
  changes, until the row differs from every earlier one on every key. A required key with
  nothing to point at leaves its table out with the reason, and so does a column of a type no generator
  writes that has no default and cannot be empty.
- **The write is one transaction through the import's writer** (`seed_write.go`, `importRun`): batches
  under each engine's parameter limit, PostgreSQL's savepoints, SQL Server's conversions in the
  statement. A row the table refuses for a reason the seed could not see — a CHECK on an expression, a
  trigger, a unique index over an expression — is left out and counted with its reason, and the rest
  carries on; a table whose parent took none of its seeded rows is left out. On ClickHouse, which has no
  transaction, each table is its own batch and a table written before a failure stays (`atomic: false`):
  a table the server refuses is reported as left out with the reason, and the rest are still reported,
  so a second try is never blind to what the first wrote. The page moves to a new seed number after
  every write, whether or not it succeeded.
  A request may name 100 tables and 10,000 rows a table, 100,000 in all.
- **The page** is a card of its own on the Generate page (`components/database/generate/seed.tsx`,
  `seed-model.ts`), gated on `seed`: a `Notice` that says what it does — made-up test rows inserted
  into the connection, named with its environment label and amber where that label says production,
  the rows already there kept, nothing inserted before the review — then the tables (every one, or
  the ones ticked), the rows each and a table's own count, a tab per table in the order they are
  inserted with what it references and its rows now and after, its first rows under two-line column
  heads (the name with its type, then the generator, a menu or fixed on the same line), the statement
  a row is inserted with, and `SqlReview` before the write. The command is *Insert N rows*, the data
  workbench's verb for adding a row. Tests: `dbx/seed_test.go` (SQLite, and the pure parts),
  `dbx/seed_live_test.go` (PostgreSQL, MariaDB, MySQL 8, SQL Server, Oracle and ClickHouse; the tables
  are named `jdt_seed_…`), `api/handlers_db_seed_test.go` and `tests/browser/database-seed.spec.ts`.

### Moving data: export, import, dumps, restore and copy

- **The export is the view, not the table.** `/databases/{id}/export` takes the grid's `filters`,
  `orderBy` and `dir` and assembles the statement through the same `browseSelect` the page fetch uses,
  so a download taken from a narrowed grid is that grid. It was an unconditional `SELECT * FROM`, which
  made the panel's own promise — "applied on the server, across the whole table" — the reason the file
  looked right. The parameters are parsed **before** any response header is written: once the body has
  started, a rejected filter can only arrive as JSON inside a file named `.csv`. `ExportTable` remains
  as the unfiltered form. The formats are `csv`, `tsv`, `json`, `ndjson` and `sql` (INSERT statements
  in the source dialect, through the dump's literal renderer and from the values as the driver scanned
  them); `columns` is a projection. A binary value goes out whole, not as the grid's preview. Two SQL
  Server types are written as what they are rather than as the driver hands them over
  (`exportValueOf`): a `uniqueidentifier` as its identifier, not the sixteen stored bytes, and a `time`
  as a time of day, not that time on the first day of the year one — in either form the column would
  not take the value back. The row cap is `limit`, clamped to `MaxExportRows`, default
  `DefaultExportRows`.
- **An export says how it ended, three ways, because no one way reaches every reader**
  (`handlers_db_transfer.go`, `exportStream`). A response that ended properly declares two trailers,
  `X-JD-Export-Status` (`complete` or `truncated`) and `X-JD-Export-Rows`. A JSON or NDJSON file that
  is not complete ends with an object whose only key is `__export`; a SQL file always ends with a
  comment. And a caller that names its export (`exportId`) can ask `GET /{id}/export/status` afterwards,
  which is the only way a browser saving a CSV learns it was cut at the limit: browsers show trailers
  to nothing, and a CSV has nowhere to say it. The registry is memory, keyed by account, connection and
  name. A **failure** is never a response that ends: the first 64 KiB are held so that a statement
  refused before then is an ordinary error response, and past that the response is flushed and the
  connection aborted (`http.ErrAbortHandler`), which a browser reports as a failed download and every
  client as a failed transfer. A Mongo server that cannot be reached is `502 connect_failed`, where it
  was an empty file and a 200. `POST /{id}/export/query` exports one statement's result at
  `service.control`, taking only what `dbx.ClassifyFor` reads as a read and running it inside the
  same engine read scope a run of it gets (`session.read`, on every engine: a read-only transaction,
  a session or setting that refuses writes, or a transaction that is rolled back), so what the
  classifier missed is refused by the server or undone. It used to be scoped on Postgres and MySQL
  only, which made the same statement rolled back by `/query` and kept by `/export/query` on SQL
  Server, Oracle, ClickHouse and SQLite.
- **An import is a stream and one transaction** (`dbx/import*.go`). `POST /{id}/import/upload` is
  multipart, read as it arrives: an `options` part of JSON, then the `file`, which is never held whole —
  the cap is `JD_DB_UPLOAD_MAX_MB`, not the JSON body's 4 MiB. The reader is the package's own because an
  import is asked two things `encoding/csv` cannot answer: which quote character, and whether a field was
  quoted (an unquoted empty field is NULL, a quoted one the empty string). UTF-16 and Latin-1 are
  re-encoded as they are read, a byte-order mark deciding over what the caller said. Columns are matched
  to the table by mapping, by position, or by name; the first thousand rows are profiled and set against
  each target column's type, and a mismatch is a **warning with the value and its line**, not a refusal.
  `dryRun` returns that and the first rows as they would be written, and writes nothing. Rows go in as
  multi-row INSERTs sized under each engine's parameter limit; a refused batch is undone and retried a
  row at a time, which is what names the line. On Postgres that needs savepoints — a refused statement
  spoils the transaction, so "skip bad rows" used to import nothing there. `replace` is a `TRUNCATE` only
  where one can be rolled back (Postgres) and a `DELETE` elsewhere, since MySQL and Oracle commit a
  TRUNCATE on their own and a failed import left the table empty; it is destructive, checked from the
  options in the handler like the query runner's SQL. `upsert` is each engine's own form on the primary
  key or a named unique index. `createTable` infers a type per column and creates the table inside the
  transaction where DDL is transactional, and drops it by hand on failure where it is not; a column made
  the key is given a type the engine will index (`nvarchar(450)` on SQL Server, `varchar(255)` on MySQL),
  since the type inferred for text is not one. Where columns are matched by name, a column the server
  fills in itself — generated, computed, virtual, a rowversion (`importComputedColumns`) — is left out
  and the report says so: an export of the table carries it, and matched to it every row was refused.
  **SQL Server** (`import_mssql.go`) ends the batch *and the transaction* on a text that does not
  convert, so "skip bad rows" lost every row before the bad one and failed at the commit. There each
  value is written through `TRY_CONVERT` to the column's own type, and a row with a value that came back
  NULL is refused by a `RAISERROR` placed in front of the statement, which ends nothing; the rows of a
  batch are checked together and written together or not at all. `datetime` and `smalldatetime` are read
  through `datetime2`, because on their own they read `2026-03-04` as the third of April for a login
  whose language puts the day first. A binary column is given `CONVERT(varbinary(max), …)`, since a NULL
  arrives as a text; a file that carries the identity column has `IDENTITY_INSERT` switched on for the
  transaction. The inline route gets the same check from the column types it now looks up, a binary
  column included: it binds a file's cells as text, and no text is a value for one, so only "no value"
  passes — converted like the rest — and a cell that holds text is refused as its own row. Left
  unconverted the NULL was refused as a text, for the whole statement.
  `TestLiveImportLeavesEveryTypedColumnEmpty` imports a row of nothing but NULLs under every type that
  could object, on each engine. **Oracle** reads a text as a date by the session's NLS format, so a value
  in an ISO form is bound as the instant it names (`importISOTime`), and `true`/`false` go into a numeric
  column as 1 and 0 — `NUMBER(1)` is what a column of them is created as there. A row Oracle refuses
  has its statement prepared again before the next one: go-ora sent a statement that had been refused
  every time it ran in a form Oracle 23 takes for a protocol violation, and at the sixth the server
  ended the session (ORA-03138), so an import whose first rows were bad lost its connection and
  imported nothing (`TestLiveOracleImportSkipsMoreThanAFewBadRows`). ClickHouse has no
  transaction and its driver cannot continue past a refused row, so `atomic` is false there and a refused
  row ends the import before anything is sent. Mongo's upload inserts in batches; its `replace` stages
  the documents and swaps them in with `$out`, which keeps the collection's indexes and leaves it
  untouched if the file breaks its rules. The inline `/import` route runs on the same engine and keeps
  its request and response; its Mongo `truncate` now reads the data through before removing anything, and
  empties the collection rather than dropping it.
- **A dump for every engine with no external dependency.** Three have a client tool the image can carry;
  the rest returned `ErrUnsupported` at the moment the operator pressed the button — the worst time to
  learn a backup was never possible. `dump_sql.go` writes SQL over the open connection; `dump_nosql.go`
  does Mongo and Redis as gzipped JSON Lines, Redis via `DUMP`/`RESTORE` so every type survives. A Redis
  dump is always this one — a header line, then each key's `DUMP` payload — and not an RDB file: only the
  dashboard restores it, and the Backups form and every description of it say so. A native
  tool that *fails* falls through to the built-in rather than to an error; one that was *stopped* does
  not. `dumpLiteral` is the second place putting a value into SQL text (unavoidable — a dump is text) and
  is per-engine, since a backslash escapes on MySQL and ClickHouse and is a plain character on the other
  four. `Restore` picks its reader from the file's first bytes, not the driver: a Postgres connection may
  hold a `PGDMP` archive, our SQL, or a plain script somebody uploaded. A mongodump archive is read
  for the database it came from (`mongoArchiveDatabases`) and confined and redirected with
  `--nsInclude`/`--nsFrom`/`--nsTo` — it used to be written back over whichever database it named.
- **A script somebody else wrote is replayed here, never by the engine's client** (`dump_script.go`). A
  plain pg_dump or a mysqldump used to be piped to `psql` or `mysql`, and a client does more with a
  script than send it: `\!` and `system` run a shell, both read other files, both reconnect elsewhere.
  A dump is a file anybody with `service.control` can upload, so restoring one was a second
  request-defined shell (invariant 6). Reading the script first cannot close that, because where a
  string ends depends on settings the script changes as it runs (`standard_conforming_strings`,
  `NO_BACKSLASH_ESCAPES`). So `postgresScript` and `mysqlScript` cut the file into statements by the
  clients' own rules — dollar quotes, `BEGIN ATOMIC` bodies and `COPY … FROM stdin` blocks (sent with
  `PgConn.CopyFrom`); `DELIMITER` and conditional comments, which are statement text — and the
  statements go over the dashboard's connection. A reader that disagrees with the server gets a syntax
  error and nothing else. The script is read through once before anything runs: a psql meta-command
  other than `\restrict`/`\unrestrict`, a mysql client command, `\connect`, or a MySQL statement that
  is a `USE` of another database (wherever on a line, and inside a conditional comment) is refused
  with the database untouched. Postgres replays as one transaction; a MySQL connection used for a
  replay has multi-statement queries switched off, so what one statement is stays decided here. The
  only tools a restore runs are `pg_restore` and `mongorestore`, on archives. This confines the
  session, not the script: a statement that names another database explicitly still reaches it with
  the connection's rights, as it would from the query runner.
- **The built-in dump is a plan, read before a row is** (`dump_sql_plan.go` and the per-engine files).
  The tables alone restore into a database with no unique constraint, no index and no view, so the plan
  carries those and the order they replay in: sequences, tables parents-first, rows, then constraints
  and indexes, foreign keys last, views after everything they read. Postgres is read from `pg_catalog`
  with the server's own deparsers in a session whose `search_path` is empty, so every name is
  schema-qualified; the `information_schema` DDL it replaces called an enum `USER-DEFINED` and did not
  parse. A partition is created `PARTITION OF` its parent and only leaves carry rows; a materialized
  view is created `WITH NO DATA` and refreshed at the end; an identity column's rows go in
  `OVERRIDING SYSTEM VALUE` and its sequence is set through `pg_get_serial_sequence`; extension-owned
  objects are left to `CREATE EXTENSION`. MySQL is scoped to `DATABASE()` — it listed every database the
  account could see, and the restore began by dropping their tables — written unqualified, with
  `FOREIGN_KEY_CHECKS` off and the session zone pinned. SQLite replays `sqlite_master`, triggers after
  the rows. Catalogue text is fenced (`-- jd:statement <word>` … `-- jd:end <word>`) so the reader
  never lexes a trigger body. The word is made per dump (`newDumpFence`) and only the closing line
  that carries it closes the fence: what is fenced is somebody else's text — a view's body with its
  comments kept, a row's long value — and a line of it reading `-- jd:end` used to end the statement
  there and hand the lines after it to the server as statements of their own. The read is one snapshot
  where the engine has one, and the replay one transaction where DDL can be rolled back (Postgres,
  SQLite, SQL Server): a dump that fails on its fortieth statement changes nothing. A restore streams
  the file; it used to read it whole.
- **SQL Server and Oracle are dumped from their own catalogues too** (`dump_sql_mssql.go`,
  `dump_sql_oracle.go`). Both were written from the dialect's column list, which does not say that a
  column numbers itself, is computed, or is a rowversion: a SQL Server restore gave back tables that no
  longer handed out an id and failed outright on a rowversion, and an Oracle one failed on the index
  behind every primary key. SQL Server is read from `sys`, objects the server ships left out: identity,
  computed and rowversion columns, named defaults, checks, unique constraints, filtered and
  included-column indexes, schemas other than `dbo`, sequences, and views ordered by
  `sql_expression_dependencies`. Rows go in under `SET IDENTITY_INSERT` and the counter is reseeded to
  where it stood, which the rows alone do not say. The engine has no cascading drop, so every foreign
  key that touches a dumped table — from a table the dump leaves alone as well — is taken off before
  the drops and put back after the rows. Nothing in the file names the database, which is what lets it
  load into one made a moment ago. Values are written for the column's type (`mssqlDumpTime`: the
  ISO form with a `T`, the one spelling read the same under every language setting; `datetime` takes
  three digits of a second and no more). Oracle's definitions are `DBMS_METADATA`'s, with storage
  clauses left out, asked for on one connection held for the whole dump because how a definition is
  written is a setting of the session. A table that is `GENERATED ALWAYS AS IDENTITY` refuses a number
  given to it and has no "this once", so it is altered to `BY DEFAULT` for its rows and back after
  them, at the counter's position. A row with a value too long for a literal — four thousand bytes;
  bytes past two thousand could not be written at all — is a PL/SQL block that builds the value in
  pieces (`oracleDumpLongRow`); the hex goes through a variable because `HEXTORAW` of a long literal is
  evaluated when the block is compiled and takes seconds a piece. Tables are dropped `PURGE`, or each
  restore leaves a copy of what it replaced in the recycle bin against the same quota. Oracle has no
  `DROP … IF EXISTS` before 23, so its drops are unconditional and one that finds nothing is passed
  over — but only that one (`dumpDropFoundNothing`): a drop refused because a session still holds rows
  in a temporary table, or because the login may not make it, stops the restore there and says so,
  where it used to surface a statement later as a name already taken. A dump is of one
  schema, the session's or the one named; a schema of the server's own (`oracle_maintained`) is refused,
  which is what an administrator's login is in unless told otherwise. The dialect-driven plan from the
  standard catalogue views remains only as the fallback for a server that speaks Postgres's protocol
  without keeping its catalogue. Their live tests do not run where the connection string lands, since
  a restore replaces every table in the database it is pointed at: SQL Server's make a database of
  their own (`JD_TEST_MSSQL_DSN` names the server; the string itself lands in master), Oracle's make a
  user (`JD_TEST_ORACLE_ADMIN_DSN`), and the shared round trips skip in a database that holds tables
  they did not make.
- **Every tool is started through `hostexec` with an argv** (`dump_exec.go`), as a process group, so
  stopping a job stops `pg_restore`'s workers and not only the process that forked them.
  `TestNothingHereStartsAProcessOfItsOwn` keeps `os/exec` out of the package. One thing is not taken
  from `hostexec`: a tool that exists only on the host side of a container boundary is not used — it
  would dial from the host's network and read paths in the host's mount namespace — and the built-in
  dumper runs instead.
- **A dump the operator can take away.** The dump stays on the server (restore reads it) and a copy goes
  to the browser immediately, because a backup whose only copy is on the machine it protects is not one.
  `/databases/{id}/backup/download` takes a **name**, not a path, and contains it with a `files.Service`
  scoped to that connection's dump directory — [invariant
  6](../security/invariants.md#invariants-that-must-not-regress) with the right root, since narrowing
  `JD_FILE_ROOTS` must not stop us handing back a file we wrote. Restore resolves through the same scope
  (`resolveRestoreSource`), by name or by the path the listing reports, and through `files.Resolve` only
  for a file elsewhere; it used to hold every dump to the roots, so narrowing them stopped the dashboard
  restoring what it could still download.
- **The dumps on disk.** `GET /databases/{id}/backups` lists the connection's dump directory newest
  first; `DELETE /databases/{id}/backups` (destructive, not typed: the database is still there to dump
  again) removes one, contained against that directory exactly as the download is. The fleet and a
  connection's summary say when it was last backed up (`lastBackup`) from the same listing and from the
  backup jobs: the later of the newest dump here and the start of the newest successful job run whose
  archive holds the connection's native dump (`backups.Store.LastDatabaseDumps`, by the `CoversDatabase`
  test a restore applies, read once per fleet request). The listing alone called a database a nightly
  job protects never backed up. `GET /databases/backups/summary` is every connection's newest dump in
  the directory in one read that dials nothing, polled every few seconds while a dump runs, so the
  control center takes the later of its answer and the fleet's. What a dump holds — the options it was
  taken with, the tool and version that wrote it, how long it took, a note, who asked — is in a small
  file beside it (`<dump>.meta.json`, `dbx/dump_meta.go`), written to a temporary name and renamed.
  Beside it rather than in a table because the directory is what gets copied off the machine. A file
  with no description is listed with what its first bytes say it is.
  `POST /databases/{id}/backups/upload` (`service.control`) streams a dump made elsewhere into the
  directory through the scoped `files.Service`, under a validated name, capped by
  `JD_DB_UPLOAD_MAX_MB`; it restores nothing.
  A dump has no name until it is whole: it is written, or uploaded, in a hidden directory inside the
  one it is bound for (`dbx.NewDumpStaging`, `.dump-*` and `.upload-*`, cleared after a day if a
  process died over one) and then given its name by `dbx.PlaceDump`, which claims the name with an
  exclusive create before renaming onto it. So the listing never holds a file that is still being
  written, and of two uploads of one name — or two dumps in one second — neither lands on the other.
  These dumps are not encrypted: they are files on this server, beside the key that would open them. A
  backup job that dumps the connection may seal its archive to the server's backup key
  ([archive encryption](git-backups-users.md#archive-encryption)); the page's picture then names the
  schedule with *encrypted*, and its restore decrypts on the way to `dbx.Restore`.
- **A dump, a restore and a copy are jobs** (`handlers_db_transfer.go`). `POST /{id}/backup` and
  `/restore` answer `202` with a `jobs.Job`; the page watches `/jobs/{id}` and its stream, where the
  lines are the tool's own, and stops it with `/jobs/{id}/cancel`. The last line, on stream `result`,
  is JSON: what was written or restored. One transfer per connection at a time
  (`StartExclusive` on `database.transfer.<id>.`; a second is `409 transfer_running` with the running
  job's id in `error.resource`). The request is audited when it is answered and the outcome when the
  job ends (`database.backup.finish`, `.restore.finish`, `.copy.finish`). A dump takes `schemaOnly`,
  `dataOnly`, `tables`, `excludeTables`, `compression`, Redis `databases` and a `note`;
  `dbx.ValidateDumpOptions` refuses what the engine cannot honour before a job exists, and
  `dbx.DumpCapabilities` is the same table for the form. A Redis dump covers every numbered database
  that holds a key unless told which. A restore takes `target` (`"this"` or `{"newDatabase": name}`,
  which also needs `system.admin`, checked in the handler) and `dumpFirst`, which dumps the database
  as it is before replacing it and names that file in the result. The dashboard's pooled sessions are
  closed around a restore. A SQLite file is loaded through the engine's online backup into the file
  that is there: overwriting it by hand gave a program with it open half of each database, and
  renaming a new file into place left that program writing to one with no name. `POST /{id}/copy`
  (`system.admin`) is a dump, a new database and a restore as one job, the dump its own and removed.
- **A copy can go to another saved connection of the same engine** (`copyToServer`). `to` names the
  connection whose server receives it; the job runs, and is stopped from, the connection copied from,
  and the target is only checked — the same engine (`400`), not protected (`409 connection_read_only`,
  because a copy makes a database there) and with no transfer of its own running (`409
  transfer_running`). The new database is made with the target's login and removed again if the copy
  fails. The job's `result` line carries `connection` and `connectionName`, so the page opens the copy
  through the server it is on. `dbx.CopyBetween` does the work (`copy_stream.go`): for a SQL engine the
  built-in dump (`writeBuiltInDump`) is written into one end of an `io.Pipe` and `replayGenericSQL`
  reads the other, so no dump file exists — a row is on the target while the source is still reading the
  tables after it, and a failure on either side stops the other (a failed dump reaches the restore as a
  failed read, a failed restore closes the pipe under the dump). The restore is the one transaction
  `restoreGenericSQL` always was on the engines that can roll back. The native tools are not used:
  `pg_dump`'s custom format cannot be read until it is whole. MongoDB and Redis dump to an archive that
  is read by seeking, so they stage it in a `.copy-*` directory beside the dump directories and remove
  it. ClickHouse, Oracle and SQLite are refused, for the reason a restore under another name is
  (`DumpCapability.NewDatabase`). `dbx.CanCopyBetween` is that answer.
- **A database can be brought over from another server** (`handlers_db_import.go`,
  `dbx/online_pg.go`, `db_online_imports`). The source is a connection string typed on the Import page,
  not a saved connection. It is never returned, never in a job line, an audit entry or an error
  (`withoutSecrets` on every message that could quote it), and only a replication keeps it: sealed with
  the same `Sealer` as a saved connection's, in `db_online_imports.source_enc`, because its cutover
  needs the source hours later. The column is emptied when the import ends (`setImport`), and forgetting
  a connection with an import under way is refused (`409 import_active`). So is replacing the server an
  import is under way into, through any connection to it (`refuseServerOfImport`): a major-version
  upgrade, its revert and switching in a recovered copy would take the subscription away and leave the
  slot holding the source's log. The capability is the word
  flag `onlineImport`: `replication` for PostgreSQL itself, `stream` for every other engine that can be
  restored under another name (CockroachDB, YugabyteDB and TimescaleDB speak PostgreSQL's protocol
  without publications that carry their tables), `false` for ClickHouse, Oracle and SQLite. The page is
  the `import` section, in the Operate group, gated on it.
  - `POST /{id}/imports/inspect` reads the source and keeps nothing: its identity, and for a
    PostgreSQL pair the report `dbx.InspectReplicationSource` and `InspectReplicationTarget` make —
    `wal_level`, the REPLICATION attribute (or `rds_replication`), free slots and WAL senders, every
    table with its replica identity, sequences, unlogged tables and large objects, which replication
    does not carry. A table with no key is a blocker, with the `ALTER TABLE … REPLICA IDENTITY FULL` to
    run: once published the source would refuse its own updates and deletes. A subscriber whose login
    cannot subscribe, and a source that is the target's own cluster (told by `pg_postmaster_start_time()`, which any login
    reads, because a subscription to its own cluster waits for ever for a slot only it could make) are
    blockers too.
    The statement that publishes the tables is handed back for an operator who will run it themselves.
  - `POST /{id}/imports` always makes a **new** database here (`newDatabaseName`), so an import never
    overwrites. A row is written first, `running`, and a `database.transfer.<id>.import` job does the
    rest. Replication: check the source again, create the database, copy the structure through the
    stream above with the rows left out, publish the tables **both servers have** (an extension's
    tables the structure copy leaves out would otherwise fail the subscription), make the slot, then
    `CREATE SUBSCRIPTION` with the slot already made (`create_slot = false`), and the row is
    `replicating`. The slot is made first on purpose: the subscription is created from a temporary
    function whose connection string is a bind parameter, because `CREATE SUBSCRIPTION` carries it as a
    literal and a utility statement is recorded as written by `pg_stat_statements`, which the
    Statements page reads back — and that statement cannot be run from a function while it makes the
    slot itself. The conninfo is rebuilt from the parsed parts (`ReplicationConninfo`), so a string
    cannot make the target server read a file (`sslrootcert`, `passfile`, `service`) or run options.
    Anything the import made and cannot use is removed when it fails: slot, publication, database.
    Stream: the new database, `CopyBetween`, and the row is `done`; progress is held in memory
    (`dbConnState.importProgress`) for the page, and only a line per table goes to the job.
  - `GET /{id}/imports/{n}` is the live reading, any role: the target's `pg_subscription_rel` (each
    table `waiting`, `copying`, `copied`, `synchronizing` or `ready`, with the rows a copy has taken
    from `pg_stat_progress_copy` on PostgreSQL 14+), whether the apply worker runs, the error counts
    from 15, and the source's `confirmed_flush_lsn` against its current WAL position as the lag in
    bytes. A row that says it is running with no job to show for it (the dashboard restarted) is
    settled on the read: a stream is `failed`; a replication is `replicating` if its subscription
    exists and `failed` otherwise.
  - `POST /{id}/imports/{n}/cutover` takes `writesStopped: true` — the operator's word that nothing
    writes to the source, which the cutover cannot see — and refuses a replication whose subscription is
    gone (`409 import_not_replicating`) or whose tables are not all `ready` (`409 import_copying`). The job reads the source's WAL position once, waits until the
    slot has confirmed it (`WaitCaughtUp`, bounded by `CatchUpTimeout`), sets every sequence on the
    target to the source's `last_value` and `is_called` (`SyncSequences`; replication does not carry
    them), disables the subscription and detaches it from its slot, drops it, drops the slot from the
    source's side and the publication if the dashboard made it. A failure before the subscription is
    dropped leaves the import `replicating`, with the reason in `detail`, and the cutover can be asked
    for again; once it is dropped the data is the target's, and a slot or a publication that could not
    be removed from the source is a note in `detail` with the statement to run there, not a failure.
    Nothing is changed on the source but the removal of what the import made there.
  - `DELETE /{id}/imports/{n}` (`system.admin` and `s.destructive`) stops a replicating import by the
    same removals, tolerating a source that no longer answers (the warnings say which statement to run
    there), and forgets one that has ended. The database made here is left; dropping it is the server
    page's. A running import cannot be removed under its job (`409 transfer_running`).
  Every mutation is audited (`database.import.start`/`.finish`, `.cutover`/`.cutover.finish`,
  `.cancel`, `.forget`); the detail has the source's address and user and no secret.

### Major-version upgrades

A PostgreSQL server Databases started is moved to a newer major release from its Settings → Version
page, the way Railway and the managed providers do it rather than the way the other self-hosted panels
do: changing the tag is how a database is lost, because a major release does not open the data
directory of the one before it. `handlers_db_upgrade.go` holds the routes, the record and the recovery;
`database_upgrade.go` the work; `dbx/upgrade.go` everything that is a fact of PostgreSQL — the
questions asked of each server, what their answers mean, the roles' statements and the parameters the
new release still has.

- **Which servers, and to what.** A server is one this upgrades when a running container that is not
  compose's publishes the connection's loopback port, its image is a release of the `postgres`,
  `pgvector` or `postgis` template (`upgradeTemplateOf`, compared as the Engine resolves a reference),
  its data directory is on a named volume, and it does not remove itself when it stops. The releases
  it can go to are the template's newer ones in the closed list, so what is pulled is still always a
  reference written in `provisionTemplates`. TimescaleDB is a template too and is left out:
  its hypertables live in a catalogue of its own that a dump of one release does not carry into the
  next. The capability flag `majorUpgrade` (`dbx/upgrade_capabilities.go`) is PostgreSQL's, and not
  CockroachDB's, YugabyteDB's or TimescaleDB's; MySQL and MariaDB are not offered.
  `GET /{id}/upgrade` answers `{offered, reason, engine, label, container, version, image, volume,
  targets, window, last, job}`: `reason` is the sentence a server that is not one of these is shown
  with, `window` the upgrade whose old server is still kept, `last` the last that ended.
- **The check changes nothing.** `POST /{id}/upgrade/check {version}` is a job: it pulls the release
  where it is not on the machine (its layers' states are the job's lines), starts it beside the server
  as `<name>-pg<to>-check` — from the server's own container settings, with no port, no data volume
  and no first-start scripts — asks both servers what the checks need, and removes it. The server is
  read through its own container (`pgSide`: psql over the socket, as the image's superuser, with a
  query that answers one JSON value), so the same reading works for both servers and needs no
  password the operator never had. The report (`upgradeReport`, the job's `result` line) is one line
  per check, `ok`, `warning` or `blocker`: the version path; every installed extension against the
  new image's catalogue (one it lacks stops the upgrade, one it carries at another version is said);
  the libraries in `shared_preload_libraries`, loaded in the new release (one it has not stops it,
  since the server would not start); disk space — the copy against what `df` reports where the data
  volume is, and the safety dump against the dump directory; the names the upgrade takes; client
  sessions, which will be ended; prepared transactions and tablespaces, which a copy cannot carry and
  stop it; replication slots and subscriptions, which are not carried or arrive switched off; the
  parameters, which are carried as below or, when edited into `postgresql.conf` by hand, are not; and
  `pg_hba.conf` rules the new server will not have. The page offers the upgrade only after a check
  that nothing stops, and the upgrade runs the same checks again first.
- **The upgrade** (`POST /{id}/upgrade {version}`, destructive, a job of the transfer kind
  `major-upgrade` so that no dump, restore or copy of the connection runs beside it) is recorded in
  `db_upgrades` before each step is taken. The new release is started beside the server as
  `<name>-pg<to>-upgrade`, on a loopback port of its own and the new volume `<name>-pg<to>-data`,
  from the old container's own settings read back with what the old image supplied left out
  (`upgradeSpec`: its `PATH`, `PG_MAJOR` and `PG_VERSION`, its command and labels, the host name Docker
  derived from its id) — carried into a newer image they would name the old release's binaries. The
  checks run against it; a safety dump of the connection's database is taken (`takeDump`, origin
  `safety`, listed on Backups with the note of the upgrade); then the old server takes reads only
  (`ALTER SYSTEM SET default_transaction_read_only = on` and a reload, after which every client
  session is ended so a transaction begun before it does not go on writing). Its roles are read with
  `pg_dumpall --globals-only` and replayed on the new server over a connection by `dbx`'s own script
  reader (`PostgresGlobals`), which leaves out the one statement the new server cannot run — creating
  the account it was created with — and refuses any client command, as a restore does. Each database
  is `pg_dump -Fc` in the old container read by `pg_restore --exit-on-error` in the new one as it is
  written, through `dockerx.ExecPipe`: the bytes pass through this process, never a shell, a file or a
  network path between the containers. `postgres` and `template1` are restored into; every other
  database is made by the restore, with its owner, encoding and settings, after the new release's own
  first database is dropped. What `ALTER SYSTEM` set — the Parameters page — is carried as
  `postgresql.auto.conf`'s own text (`CarryPostgresParameters`, since a list such as
  `shared_preload_libraries` set again as one string is one library with a comma in it), less a
  parameter the new release no longer has; a `-c` on the command line goes with the container's
  settings. The new server is restarted to load them, and the copy is counted against the old server:
  the same databases, roles and extensions, and the same number of rows in every table
  (`PostgresRowCountsQuery`; a table an extension owns is the extension's to fill). A difference stops
  the upgrade.
- **The cutover** runs on a context of its own, so a cancel cannot land between the two servers. The
  old server's `postgresql.auto.conf` is put back as it was (without a reload: it is about to stop),
  it is stopped with the database grace and renamed `<name>-pg<from>`, with its restart policy set to
  `no` so a reboot cannot start it on the port the new one holds. The copy's container is removed,
  keeping its volume, and a container of the new release is created under the old name with the old
  one's port binding, restart policy, limits, logging and user networks (with their aliases). It has
  to take the exact port and let the saved connection sign in (`awaitConnection`), which proves that
  nothing that dials the server changes: the saved connection, every other saved connection to the
  same server, the deployments' `${{database.ID}}` references and the port's exposure all name that
  place. The deployment networks are attached to it at once (`deploymentDatabaseNetworks.rebind`, the
  bind reconciliation does on its next pass, which detaches the stopped old container from the
  alias), and every saved connection to the server drops its pool and what was kept about the server,
  so Home reads the new version straight away.
- **Nothing half-done.** A failure before the cutover removes the copy's container and volume and lets
  the old server take writes again (`undoCopy`); a failure during it removes the new container and
  volume and gives the old one back its name and restart policy, starts it and waits for the saved
  connection (`failCutover`). Either is recorded as `failed` with the reason, shown on the page with
  the safety dump that was kept. A dashboard that stops in the middle of an upgrade puts the server
  back when it starts (`recoverDatabaseUpgrades`, from the record's `phase` and the
  `read_only_prior` it kept), and removes a check's release left behind. The upgrade's own containers
  carry `io.just-dashboard.database-upgrade` and are not listed by the inventory, so the sync never
  connects one.
- **The old server is kept** until the operator says. `POST /{id}/upgrade/revert {dumpFirst}`
  (destructive, a job) takes a dump of the new server's database first unless told not to, stops the
  new server and sets it aside, gives the old one back its name and restart policy and waits for the
  saved connection, attaches it to the deployment networks again and removes the new server with its
  volume — what was written to it since the cutover goes with it; if the old one does not come back,
  the new one is put back. `POST /{id}/upgrade/complete` (destructive) removes the old container and
  the named volumes it mounted, keeping one another container uses, and closes the record. Both
  refuse while the old container has been started by hand. The kept container is a stopped server
  like any other to the inventory, which lists it; forgetting the connection forgets its upgrades'
  records with it (`ON DELETE CASCADE`), and a container still kept is then the Docker page's to
  remove.
- **Protection.** The upgrade and its revert replace what a protected connection reaches and are
  refused, through the connection and through a neighbour on the same server
  (`refuseServerOfProtected`); the check only reads and finishing removes what the connection no
  longer reaches, so both are let through. Every route is `system.admin`; the three that change the
  server are in `s.destructive`.
- **Audit.** The requests are `database.upgrade.check`, `database.upgrade`, `database.upgrade.revert`
  and `database.upgrade.complete`; each job's end is `.finish` after its request's action; every step
  of an upgrade is `database.upgrade.step` with the step named (`safety-dump`, `read-only`, `copy`,
  `parameters`, `verify`, `cutover`, `undo`); a recovery after a restart is `database.upgrade.recover`.
  None carries a password: the containers' clients read theirs from their environment, never an
  argument.
- `TestLiveUpgradePostgres16To17` (`JD_TEST_UPGRADE_LIVE=1`) starts a PostgreSQL 16 the way Databases
  does, with data in two databases, a role with a password, `pg_trgm`, `hstore` and
  `pg_stat_statements` preloaded from the command line, and a parameter set with `ALTER SYSTEM`; it
  checks, upgrades, reverts, recovers an interrupted upgrade, upgrades again and finishes, and reads
  each result off the servers themselves.

### Point-in-time recovery

A dump is the database at one moment. A PostgreSQL server this dashboard runs can also hand every
finished segment of its write-ahead log to WAL-G, beside base backups taken on a schedule, and be
recovered as it was at any second the archive covers. It is the Backups page's second panel, offered
where the capability table says `pitr` (PostgreSQL and TimescaleDB; not CockroachDB or YugabyteDB,
which keep no such log) and the server is one this feature can archive: a container that is not
compose's, running one of the provisioning list's PostgreSQL images, with its cluster on a named volume
and the server's own command (`pitrServerOf`, `database_pitr.go`). Anything else is read with the
reason and offered nothing. The engine-side half — settings, WAL-G's configuration, what its listings
say, the window, the base backup a recovery starts from, retention — is `dbx/pitr.go`.

- **WAL-G runs inside the server's own image, never in this process.** The dashboard's image carries a
  pinned WAL-G release (`backend/Dockerfile`, checked against its digest); `dbx.LoadWALGTool` packs it
  with the ELF loader and libraries it was linked against, read from the dashboard's own filesystem,
  and a constant wrapper (`/jd-pitr/wal-g`) that starts it through that loader — most of the images are
  Alpine's and have no glibc. The tool and WAL-G's configuration (`walg.json`, mode 0600, the server's
  account's) are copied into a volume per archive (`<stream>-pitr`) through a created, never-started
  container of the server's image, and mounted read-only at `/jd-pitr`. While the server runs, every
  WAL-G command is a `docker exec` in it as `postgres` (`dockerx.ExecStream`); while it does not, a
  one-shot container of its image (`RunToCompletion`, 512 MiB). A directory destination is only ever a
  bind mount of the operator's path, resolved through `files.Resolve`, handed to the server's account
  by a constant `chown` in a container — so what WAL-G writes is read back by the account that wrote it.
- **Archiving is set up with a restart.** `POST /{id}/pitr` tests the destination as a backup job's is
  tested, makes an OpenPGP key pair (`dbx.NewWALKey`), installs the tool, proves from inside the server's
  image that the destination takes a file (`st put` of the tool's digest), records the archive and
  recreates the container with `archive_mode=on`, `archive_command=/jd-pitr/wal-g wal-push %p` and
  `archive_timeout=60` on its command line (`dbx.PostgresCommandSettings` keeps every other flag;
  `wal_level` is raised only from `minimal`), after a `CHECKPOINT` and the power route's 90-second
  grace. The settings are on the command line rather than in `postgresql.auto.conf` so that a copy
  restored from a base backup does not archive into its source's archive. `preview: true` answers the
  plan — the command line, the mounts and the location — and changes nothing; the form shows it. The
  first base backup follows in the same job.
- **The destinations are a backup job's**: a directory on this server (default
  `JD_BACKUP_DIR/wal`), S3 or Backblaze B2, each archive a folder of its own (`<container>-<UTC
  stamp>`). `fromJob` takes an existing backup job's destination and sealed keys, so a bucket's keys
  are not typed twice. Keys are sealed with `JD_MASTER_KEY` like a job's and never returned.
- **Encrypted with WAL-G's own OpenPGP**, to a key made for the archive. The server is given only the
  public half, which is all encrypting needs: a server, or a superuser reading `walg.json` through
  `pg_read_file`, cannot read its own archive back. The private half is sealed in the store and written
  only into a recovery's own volume, which is removed once the copy has recovered.
- **Base backups and retention.** `POST /{id}/pitr/backup` (and each firing of the archive's cron
  schedule, `database_pitr_schedule.go`, the backup jobs' parser) runs `backup-push` in the server.
  Retention by time follows: `dbx.WALRetentionKeep` keeps the newest base backup that finished before
  the window began — the moments at its start are replayed from it — and `delete before FIND_FULL`
  removes what is older; the newest backup is never deleted. `PUT /{id}/pitr` changes the schedule and
  the days kept.
- **What the panel reads.** `GET /{id}/pitr` is the archive (destination, key fingerprint, schedule,
  days kept, the kept listing of its base backups and newest segment), `pg_stat_archiver` read over the
  connection (last archived segment and when, failures, the `.ready` queue where the account may list
  it), WAL-G's own last error out of the server's log when archiving is failing, the recoverable window
  (from the end of the oldest base backup to the newest archived segment, or the archiver's later
  report), the next base backup, the restore points and the recovered copies. The destination is listed
  again at most once a minute, and only through the running server: a read never starts a container.
  `drift` is an archive whose container was made again without its settings.
- **Recovery is into a new container, never over the running one.** `POST /{id}/pitr/restore` takes
  `latest`, a time or a restore point's name (`preview: true` answers the base backup, the copy's name,
  its loopback port and its recovery settings). The job fetches the base backup into `<copy>-data` in a
  one-shot container, creates the copy from the server's spec — its own volume and port, Docker's
  default network, no deployment labels, restart policy `no` while it recovers, in the image the archive
was written by (a server since moved to a newer major cannot start the older cluster) — with `recovery.signal`
  and `restore_command`/`recovery_target_*` on its command line, follows its log into the job and waits
  for it to leave recovery. A time later than every commit archived ends PostgreSQL's recovery with
  "recovery ended before configured recovery target was reached"; every commit before it is then all
  there is, so the copy is started again to the end of the log. Finally the copy is recreated without
  the recovery settings or the archive's mounts, its key volume removed, and saved as a connection of
  its own (`docker:<copy>`). A port held by a stopped container is never given to a copy: the server
  being recovered is often the stopped one. Anything a recovery made is removed if it does not finish.
- **Then keep both or switch.** `POST /{id}/pitr/restores/{rid}/switch` stops the server, renames it
  `<name>-replaced-<stamp>` with restart policy `no` (kept, with its volume and its archive's tool), and
  recreates the copy under the server's name with its ports, networks and labels, in the copy's own image — so applications,
  saved connections and the deployment network reconciler reach the copy without being told. A server
  that archived goes on archiving into a new archive with the same destination and key, since the copy's
  history parted from the old one; the old archive ends and stays listed under *Earlier archives*, the
  only place the moments before the switch are, until `DELETE /{id}/pitr/archives/{aid}` deletes it.
  A switch that fails puts both containers back.
- **Restore points.** `POST /{id}/pitr/restore-points` runs `pg_create_restore_point` and `pg_switch_wal`
  (so the name reaches the archive now) and records the name and LSN for the recovery form.
- **Turning it off** (`DELETE /{id}/pitr`) restarts the server without the settings and keeps the archive
  unless `deleteArchive` is set; the tool volume, which holds the destination's keys, is removed.
- **Jobs and audit.** Every operation is a job under `database.pitr.<container>.`, one at a time per
  server and refused while a dump or restore runs against the connection; the page watches it as it
  watches a dump. Each request is audited when answered and each job's ending as `….finish` (a
  scheduled base backup as the `system` actor). Setting up, turning off, switching and deleting an
  archive are destructive (`s.destructive`, or by hand for setting up, whose plan shares the path;
  ordinary confirmation); recovery into a new container is `system.admin`; a base backup and a restore point are `service.control`. On a protected connection
  only the switch is refused, since it replaces what the connection reaches.
- **What an interrupted job leaves is swept at start** (`sweepPITR`), judged by the role label every
  container and volume of the feature carries (`io.just-dashboard.pitr.role`) and by the records, never
  by a name: the containers and volumes made for one command or one install, a copy recorded as
  `recovering` — a recovery is recorded before it makes anything — with its data and the volume holding
  the archive's private key, a recovery's key volume nothing mounts, and an archive's tool volume no
  archive was ever recorded for, which holds a destination's keys. A copy that finished recovering is
  a database of its own and is never touched, and its record outlives the archive it came from. The
  schedule ends an archive whose server's container is gone, and keeps the archive.
- **Limits.** One server's archive is one cluster's timeline: after a major-version upgrade, turn
  point-in-time recovery off and on again. An install whose dashboard image predates WAL-G, or an
  architecture WAL-G publishes no build for, is told so and offered nothing.

### The MCP server

An AI client — Claude Code, Cursor, VS Code, anything that speaks the Model Context Protocol over
Streamable HTTP — reaches the databases through one endpoint, `POST /api/v1/mcp`
(`api/handlers_mcp.go`, `handlers_mcp_tools.go`; the protocol is `internal/mcp`). It sits outside the
`/databases` tree, as the metrics export does, because what admits a request there is different.

- **Only an MCP key opens it, and an MCP key opens nothing else.** A key with `scope = 'mcp'` is
  refused by every other group with `403 key_scope`; the endpoint's group authenticates with
  `httpx.AuthenticateMCP`, which reads a bearer key and never the session cookie (so it needs no CSRF
  header), refuses any other key with `403 key_scope`, and answers a missing or wrong key with
  `401` and `WWW-Authenticate: Bearer` naming no authorization server. A request with a foreign
  `Origin` is refused `403` (`wsx.Upgrader.CheckOrigin`), as the transport asks; a page that rebinds
  its own name to this address passes that check and is stopped by the key it cannot know, since
  nothing on the endpoint reads an ambient credential. Then the allowlist, `apiLim` per principal
  and `AuditMutations`, as every group. An agent mounts none of it.
- **What a key reaches** is `api_token_connections`: one row per connection, `read` or `write`, at
  most 64, read again on every call so a grant taken away answers the next request. A key carries
  its owner's role or a narrower one, and like every key never more than its owner holds now.
  `POST /tokens/mcp` (session only) makes one from `{name, role?, ttlDays, connections}` — Account →
  API keys sends the replaced key's role and its databases less those protected or forgotten since;
  `PUT /tokens/{id}/connections` (session only, the owner's own active MCP key) replaces what it
  reaches and keeps the secret. Both refuse a protected connection (`409 connection_read_only`), an
  engine without the `mcp` capability (every SQL engine has it; MongoDB and Redis do not), a missing
  one, an empty list, and a `write` grant for a key whose role lacks `service.control`. The dialog
  lets a database protected since be cleared from a key, the one way to save the rest. They are audited as
  `dashboard.token.create` (with `scope` and the databases by name) and
  `dashboard.token.connections`. Listing, revoking and deleting are the routes every key has.
- **Both revisions of the protocol.** A 2025 client (`2025-03-26`, `2025-06-18`, `2025-11-25`)
  opens with `initialize` and repeats the version in `MCP-Protocol-Version`; a 2026-07-28 client
  sends no handshake and carries its version in every request's `_meta`, mirrored into
  `MCP-Protocol-Version`, `Mcp-Method` and `Mcp-Name`, which must agree with the body
  (`-32020`). The endpoint keeps no session in either, issues no `Mcp-Session-Id`, refuses a batch,
  answers `GET` and `DELETE` with `405` (no stream for the server to speak first on), and takes a
  notification or a reply with `202`. `server/discover`, `ping`, `tools/list` and `tools/call` are
  the methods; `initialize` is recorded as `database.mcp.connect` with the client's name and
  version.
- **The tools** (`mcp.Tools`): `list_connections`, `list_tables`, `describe_schema` (structure only:
  column names and types, nullability, keys and what a foreign key references — no defaults or
  comments, which can be values somebody typed), `sample_rows` (10, at most 50), `explain` (planned,
  never run), `run_query` and `run_write`. `run_query` is offered to an owner whose role may run SQL
  (`service.control`, the query route's own question); it refuses anything `dbx` does not classify as
  a read before sending it, and runs the statement inside the engine's read-only scope (100 rows, at
  most 500, 30 seconds). `run_write` is offered only when the key may change some database, runs one
  statement at a time (60 seconds), and passes it through `authoriseSQL`: a destructive statement
  needs `destructive` and spends `destrLim`, as in the SQL editor. Every value is cut at 1,000 bytes
  and a result at 48 KB. A key has four calls in flight at most. The read-only scope is the
  engine's: on Oracle it is a check of the statement's first word and a rollback, so a function a
  read calls that commits in an autonomous transaction is not stopped there, and the database
  account the connection signs in as is the real limit.
- **A protected connection is refused for every tool, reads included,** whatever the key was given,
  and `list_connections` names it under `unavailable` with why. Unlike the SQL editor, where a
  protected connection still reads, nothing about a database the operator protected reaches an AI
  client.
- **A refusal is a result, not a protocol error**: a tool that was refused, or that the engine
  answered with an error, returns `isError: true` with the reason for the model to read. Its audit
  entry is `database.mcp.<tool>` with the connection as target and the outcome (`ok`, `refused` with
  a reason, `failed`) in the detail, and `httpx.AuditFailed` marks it a failure although the HTTP
  status is 200. A statement is recorded as its shape (`dbx.StatementShape`: every literal and number
  a `?`, a `"…"` too where MySQL or SQLite can read it as a string, comments dropped, read with the
  runner's own lexer for the engine) and an engine error as its code alone (`dbx.ErrorCode`:
  `SQLSTATE 22P02`, `MySQL 1064`, `SQL Server 2627`, `ORA-00001`, …), because a literal is
  somebody's e-mail address and every engine's message can quote one back. A name a client chose
  (its own on `initialize`, a database's it asked for) is cut to 120 characters as a target.
  `tools/list`, `ping` and discovery are not audited.
- **The UI** is the *MCP server* dialog of the control center
  (`components/database/fleet/mcp-server-dialog.tsx`, logic in `mcp-server.ts`): the endpoint, the
  signed-in account's active MCP keys with what each reaches, a form choosing databases (those the
  server would refuse listed with why) and read or change for each, the key shown once with the
  configuration for Claude Code, Cursor and VS Code, editing a key's databases and revoking it.
  Account → API keys lists MCP keys as *MCP only* and replaces one with the same databases.

### Tests

Which variables name which fixture, which tests fall back to a standard port and which never do, and the
per-area browser specs are in
[CONTRIBUTING](../../../CONTRIBUTING.md#running-the-database-tests-against-real-engines).

- **Live tests skip rather than fail**, or a suite failing for want of a database teaches people to ignore
  it. Every bug this feature shipped was a catalogue query a unit test string-matched identically and only
  the engine rejected — SQL Server refusing `ADD COLUMN`, a size query summing every index_id and
  reporting four times the real size, Postgres's `now()` being the *transaction* timestamp and so
  reporting a negative session age. Oracle has an optional live fixture using `JD_TEST_ORACLE_DSN`;
  without a configured server, its unit coverage does not establish live-engine compatibility.
  The table editor and query runner's own live tests (`dbx/live_workbench_test.go`) run each case on
  PostgreSQL, MariaDB, MySQL 8 (`JD_TEST_MYSQL8_DSN`), SQL Server and Oracle, and never fall back to a
  standard port: an engine is named by its variable or skipped. The Oracle cases that use the JSON and
  BOOLEAN types need 23ai, and the two that watch another session (`v$session`) need
  `JD_TEST_ORACLE_ADMIN_DSN`. On a server shared between runs, point `JD_TEST_MSSQL_DSN` at a database
  of the run's own; every table these tests make is named `jdwb_…`. `dbx/live_script_bodies_test.go`
  writes a stored program in the console the way each engine's client takes one — `DELIMITER` and none
  on MariaDB and MySQL 8, `GO` and `GO n` on SQL Server, `/` lines, a package and a procedure that does
  not compile on Oracle, `BEGIN ATOMIC` beside a dollar-quoted body on PostgreSQL, a trigger on SQLite
  — and creates, calls and reads it back; its objects are named `jdt_script_splitter_…`.

### Routes

Every route under `/api/v1/databases`, read off the mount functions (`mountDatabaseRoutes` in
`api/handlers_db.go` and the sixteen it calls): 291 of them. The whole tree sits behind authentication
and `protectReadOnlyConnections`.

- **Capability** is every capability the route's groups require. `read` is any authenticated role,
  `readonly` included.
- **Destructive** is `yes` for a route inside `s.destructive`: the `destructive` capability, which
  the column before it lists, and the `destrLim` budget. `by content` is a route that serves routine
  requests too, so its handler asks for that capability and budget by hand when the body makes the
  request destructive, and fails closed; the word after it is what decides. `by hand: every request` is
  a route that asks for it whatever the body says, and sits at `service.control` only so that a form can
  preview it (the stored-program writes).
- **Audited as** is the action the entry is recorded under. A `GET` writes none unless it is marked
  †: those record themselves, before the read runs or when the socket opens. A `POST` that only
  reads or only probes writes none either (`httpx.SkipAudit`). Every other mutating route names its
  own action, and the column is that name: what a request is recorded under once its handler has
  read it. A request refused before that — by `RequireCapability`, by the protected-connection
  guard, or by the handler's own validation ahead of its first `SetAudit` or `SkipAudit` — is still
  recorded, as a failure under the middleware's default label. `httpx.AuditMutations` derives that
  label when the request enters it, which is before the `/databases` subrouter has matched, so for
  every route here it is the method and the section and nothing more (`post:databases`,
  `delete:databases`); which route was refused is the entry's `path`.
- **Protected** is what a connection marked read-only does with the route: `open`, `by content`
  (let through when the handler's own classification of the body says read), `preview only` (let
  through with `?preview=1`, which shows the statement and runs nothing) or `refused` with
  `409 connection_read_only`. It is `protectedRouteVerdicts` in `api/handlers_db_protect_test.go`,
  the list written out by hand that `TestProtectionCoversEveryMutatingRoute` holds the router to:
  45 open, 13 by content, 28 preview only, 75 refused.

**Every database** (26)

| Method | Path | Capability | Destructive | Audited as | Protected |
| --- | --- | --- | --- | --- | --- |
| `GET` | `/databases/` | read | — | — | — |
| `POST` | `/databases/` | `system.admin` | — | `database.connection.create` | — |
| `POST` | `/databases/adopt` | `system.admin` | — | `database.connection.adopt`, `.refresh` | — |
| `GET` | `/databases/assistant` | read | — | — | — |
| `PUT` | `/databases/assistant` | `system.admin` | — | `database.assistant.configure` | — |
| `DELETE` | `/databases/assistant` | `system.admin` | — | `database.assistant.remove` | — |
| `GET` | `/databases/backups/summary` | read | — | — | — |
| `GET` | `/databases/detected` | `system.admin` | — | — | — |
| `GET` | `/databases/drivers` | read | — | — | — |
| `GET` | `/databases/fleet` | read | — | — | — |
| `POST` | `/databases/host` | `system.admin` | — | `database.connection.host` | — |
| `POST` | `/databases/host/grant` | `system.admin` | — | `database.connection.host.grant` | — |
| `GET` | `/databases/inventory` | read | — | — | — |
| `POST` | `/databases/inventory/connect` | `system.admin` | — | `database.inventory.connect` | — |
| `POST` | `/databases/inventory/ignore` | `system.admin` | — | `database.inventory.ignore`, `.unignore` | — |
| `POST` | `/databases/inventory/scan` | `system.admin` | — | — | — |
| `GET` | `/databases/metrics-export` | read | — | — | — |
| `PUT` | `/databases/metrics-export` | `system.admin` | yes | `database.metrics.retention` | — |
| `GET` | `/databases/orm/targets` | read | — | — | — |
| `POST` | `/databases/provision` | `system.admin` | — | `database.server.provision` | — |
| `POST` | `/databases/provision/build` | `system.admin` | — | `database.image.build` | — |
| `GET` | `/databases/provision/capacity` | `system.admin` | — | — | — |
| `GET` | `/databases/provision/options` | `system.admin` | — | — | — |
| `POST` | `/databases/sync` | `system.admin` | — | `database.connection.sync` | — |
| `POST` | `/databases/test` | `system.admin` | — | — | — |
| `GET` | `/databases/topology` | read | — | — | — |

**Alerts** (5)

| Method | Path | Capability | Destructive | Audited as | Protected |
| --- | --- | --- | --- | --- | --- |
| `GET` | `/databases/alerts?connection=` | read | — | — | — |
| `POST` | `/databases/alerts/rules` | `system.admin` | — | `database.alert.create` | — |
| `PUT` | `/databases/alerts/rules/{rule}` | `system.admin` + `destructive` | yes | `database.alert.update` | — |
| `DELETE` | `/databases/alerts/rules/{rule}` | `system.admin` + `destructive` | yes | `database.alert.delete` | — |
| `POST` | `/databases/alerts/rules/{rule}/test` | `system.admin` | — | `database.alert.test` | — |

**One connection** (15)

| Method | Path | Capability | Destructive | Audited as | Protected |
| --- | --- | --- | --- | --- | --- |
| `GET` | `/databases/{id}` | read | — | — | — |
| `PUT` | `/databases/{id}` | `system.admin` | — | `database.connection.update` | open |
| `DELETE` | `/databases/{id}` | `system.admin` + `destructive` | yes | `database.connection.delete` | open |
| `GET` | `/databases/{id}/access` | `system.admin` | — | — | — |
| `PUT` | `/databases/{id}/access` | `system.admin` + `destructive` | yes | `database.access.change` | open |
| `GET` | `/databases/{id}/tls` | `system.admin` | — | — | — |
| `PUT` | `/databases/{id}/tls` | `system.admin` + `destructive` | yes | `database.tls.change` | open |
| `POST` | `/databases/{id}/tls/renew` | `system.admin` + `destructive` | yes | `database.tls.renew` | open |
| `GET` | `/databases/tls/authority` | any role | — | — | — |
| `POST` | `/databases/{id}/access/preview` | `system.admin` | — | — | open |
| `GET` | `/databases/{id}/consumers` | read | — | — | — |
| `DELETE` | `/databases/{id}/database` | `system.admin` + `destructive` | yes | `database.drop` | refused |
| `GET` | `/databases/{id}/overview` | read | — | — | — |
| `GET` | `/databases/{id}/ping` | read | — | — | — |
| `GET` | `/databases/{id}/pooler` | read | — | — | — |
| `PUT` | `/databases/{id}/pooler` | `system.admin` | — | `database.pooler.enable` / `database.pooler.update` | refused |
| `DELETE` | `/databases/{id}/pooler` | `system.admin` + `destructive` | yes | `database.pooler.disable` | refused |
| `GET` | `/databases/{id}/pooler/stats` | read | — | — | — |
| `POST` | `/databases/{id}/power` | `service.control` | by content: `stop`, `restart` | `database.power.<action>` | open |
| `GET` | `/databases/{id}/resources` | read | — | — | — |
| `PUT` | `/databases/{id}/resources` | `system.admin` | by content: a lifted limit | `database.resources.change` | open |
| `GET` | `/databases/{id}/schemas` | read | — | — | — |
| `GET` | `/databases/{id}/stats` | read | — | — | — |
| `GET` | `/databases/{id}/url` | `system.admin` | — | `database.connection.reveal` † | — |

**Rows and statements** (25)

| Method | Path | Capability | Destructive | Audited as | Protected |
| --- | --- | --- | --- | --- | --- |
| `POST` | `/databases/{id}/assistant` | `service.control` | — | `database.assistant.ask`, `.tool` | refused |
| `GET` | `/databases/{id}/browse` | read | — | — | — |
| `GET` | `/databases/{id}/cell` | read | — | — | — |
| `POST` | `/databases/{id}/changes` | `service.control` | by content: a delete | `database.rows.change` | by content |
| `POST` | `/databases/{id}/classify` | read | — | — | open |
| `GET` | `/databases/{id}/columns` | read | — | — | — |
| `GET` | `/databases/{id}/count` | read | — | — | — |
| `POST` | `/databases/{id}/explain` | read | by content: `analyze` | `database.explain` | by content |
| `POST` | `/databases/{id}/explain/index-candidates` | read | — | `database.explain.index_candidates` | open |
| `POST` | `/databases/{id}/explain/what-if` | read | — | `database.explain.what_if` | open |
| `GET` | `/databases/{id}/missing-indexes` | read | — | — | — |
| `GET` | `/databases/{id}/stats/history` | read | — | — | — |
| `GET` | `/databases/{id}/history` | read | — | — | — |
| `GET` | `/databases/{id}/queries` | read | — | — | — |
| `POST` | `/databases/{id}/queries` | `service.control` | — | `database.query.save` | open |
| `PUT` | `/databases/{id}/queries/{qid}` | `service.control` | — | `database.query.update` | open |
| `DELETE` | `/databases/{id}/queries/{qid}` | `service.control` | — | `database.query.unsave` | open |
| `POST` | `/databases/{id}/query` | `service.control` | by content: statement | `database.query` | by content |
| `POST` | `/databases/{id}/query/cancel` | `service.control` | — | `database.query.cancel` | open |
| `POST` | `/databases/{id}/rows` | `service.control` | — | `database.row.insert` | refused |
| `PATCH` | `/databases/{id}/rows` | `service.control` | — | `database.row.update` | refused |
| `DELETE` | `/databases/{id}/rows` | `destructive` | yes | `database.row.delete` | refused |
| `POST` | `/databases/{id}/rows/sql` | read | — | — | open |
| `POST` | `/databases/{id}/script` | `service.control` | by content: statements | `database.script` | by content |
| `GET` | `/databases/{id}/search` | read | — | `database.search` † | — |
| `GET` | `/databases/{id}/table` | read | — | — | — |
| `GET` | `/databases/{id}/tables` | read | — | — | — |

**Schema** (40)

| Method | Path | Capability | Destructive | Audited as | Protected |
| --- | --- | --- | --- | --- | --- |
| `GET` | `/databases/{id}/catalog` | read | — | — | — |
| `POST` | `/databases/{id}/ddl/column` | `service.control` | by content: plan | `database.ddl.add_column` | preview only |
| `PATCH` | `/databases/{id}/ddl/column` | `service.control` | by content: plan | `database.ddl.alter_column` | preview only |
| `DELETE` | `/databases/{id}/ddl/column` | `destructive` | yes | `database.ddl.drop_column` | preview only |
| `POST` | `/databases/{id}/ddl/comment` | `service.control` | by content: plan | `database.ddl.comment` | preview only |
| `POST` | `/databases/{id}/ddl/constraint` | `service.control` | by content: plan | `database.ddl.add_constraint` | preview only |
| `DELETE` | `/databases/{id}/ddl/constraint` | `destructive` | yes | `database.ddl.drop_constraint` | preview only |
| `POST` | `/databases/{id}/ddl/enum` | `service.control` | by content: plan | `database.ddl.create_enum` | preview only |
| `POST` | `/databases/{id}/ddl/enum/value` | `service.control` | by content: plan | `database.ddl.add_enum_value` | preview only |
| `POST` | `/databases/{id}/ddl/foreign-key` | `service.control` | by content: plan | `database.ddl.add_foreign_key` | preview only |
| `DELETE` | `/databases/{id}/ddl/foreign-key` | `destructive` | yes | `database.ddl.drop_foreign_key` | preview only |
| `POST` | `/databases/{id}/ddl/index` | `service.control` | by content: plan | `database.ddl.create_index` | preview only |
| `DELETE` | `/databases/{id}/ddl/index` | `destructive` | yes | `database.ddl.drop_index` | preview only |
| `POST` | `/databases/{id}/ddl/policy` | `system.admin` | by content: plan | `database.ddl.create_policy` | preview only |
| `PATCH` | `/databases/{id}/ddl/policy` | `system.admin` | by content: plan | `database.ddl.alter_policy` | preview only |
| `DELETE` | `/databases/{id}/ddl/policy` | `system.admin` + `destructive` | yes | `database.ddl.drop_policy` | preview only |
| `POST` | `/databases/{id}/ddl/rename` | `service.control` | by content: plan | `database.ddl.rename` | preview only |
| `PATCH` | `/databases/{id}/ddl/row-security` | `system.admin` | by content: turning it off or no longer forcing it | `database.ddl.row_security` | preview only |
| `POST` | `/databases/{id}/ddl/routine` | `service.control` | by hand: every request | `database.ddl.save_routine` | preview only |
| `DELETE` | `/databases/{id}/ddl/routine` | `destructive` | yes | `database.ddl.drop_routine` | preview only |
| `POST` | `/databases/{id}/ddl/schema` | `service.control` | by content: plan | `database.ddl.create_schema` | preview only |
| `DELETE` | `/databases/{id}/ddl/schema` | `destructive` | yes | `database.ddl.drop_schema` | preview only |
| `POST` | `/databases/{id}/ddl/table` | `service.control` | by content: plan | `database.ddl.create_table` | preview only |
| `DELETE` | `/databases/{id}/ddl/table` | `destructive` | yes | `database.ddl.drop_table` | preview only |
| `POST` | `/databases/{id}/ddl/trigger` | `service.control` | by hand: every request | `database.ddl.save_trigger` | preview only |
| `DELETE` | `/databases/{id}/ddl/trigger` | `destructive` | yes | `database.ddl.drop_trigger` | preview only |
| `POST` | `/databases/{id}/ddl/truncate` | `destructive` | yes | `database.ddl.truncate` | preview only |
| `POST` | `/databases/{id}/ddl/view` | `service.control` | by content: plan | `database.ddl.create_view` | preview only |
| `DELETE` | `/databases/{id}/ddl/view` | `destructive` | yes | `database.ddl.drop_view` | preview only |
| `GET` | `/databases/{id}/diagram` | read | — | — | — |
| `PUT` | `/databases/{id}/diagram` | `service.control` | — | `database.diagram.save` | open |
| `DELETE` | `/databases/{id}/diagram` | `service.control` | — | `database.diagram.reset` | open |
| `GET` | `/databases/{id}/graph` | read | — | — | — |
| `GET` | `/databases/{id}/object` | read | — | — | — |
| `GET` | `/databases/{id}/outline` | read | — | — | — |
| `GET` | `/databases/{id}/program` | read | — | — | — |
| `GET` | `/databases/{id}/relations` | read | — | — | — |
| `GET` | `/databases/{id}/rls` | read | — | — | — |
| `POST` | `/databases/{id}/rls/test` | `service.control` | — | `database.rls.test` | by content |

**Operations** (22)

| Method | Path | Capability | Destructive | Audited as | Protected |
| --- | --- | --- | --- | --- | --- |
| `GET` | `/databases/{id}/activity` | read | — | — | — |
| `POST` | `/databases/{id}/activity/cancel` | `destructive` | yes | `database.session.cancel` | open |
| `POST` | `/databases/{id}/activity/kill` | `destructive` | yes | `database.session.kill` | open |
| `GET` | `/databases/{id}/clickhouse/merges` | read | — | — | — |
| `GET` | `/databases/{id}/clickhouse/mutations` | read | — | — | — |
| `GET` | `/databases/{id}/clickhouse/parts` | read | — | — | — |
| `GET` | `/databases/{id}/clickhouse/queries` | read | — | — | — |
| `GET` | `/databases/{id}/indexstats` | read | — | — | — |
| `GET` | `/databases/{id}/locks` | read | — | — | — |
| `GET` | `/databases/{id}/logs/sources` | read | — | — | — |
| `GET` | `/databases/{id}/maintenance` | read | — | — | — |
| `POST` | `/databases/{id}/maintenance` | `service.control` | by content: action | `database.maintenance` | by content |
| `GET` | `/databases/{id}/querylog` | read | — | — | — |
| `GET` | `/databases/{id}/replication` | read | — | — | — |
| `GET` | `/databases/{id}/settings` | read | — | — | — |
| `PUT` | `/databases/{id}/settings` | `system.admin` | — | `database.setting.set`, `.reset` | refused |
| `GET` | `/databases/{id}/sqlite/file` | read | — | — | — |
| `GET` | `/databases/{id}/statements` | read | — | — | — |
| `GET` | `/databases/{id}/statements/history` | read | — | — | — |
| `GET` | `/databases/{id}/statements/history/{statement}` | read | — | — | — |
| `POST` | `/databases/{id}/statements/preload` | `system.admin` | — | `database.statements.preload` | refused |
| `POST` | `/databases/{id}/statements/reset` | `destructive` | yes | `database.statements.reset` | refused |
| `GET` | `/databases/{id}/tablestats` | read | — | — | — |

**The server behind a connection** (19)

| Method | Path | Capability | Destructive | Audited as | Protected |
| --- | --- | --- | --- | --- | --- |
| `POST` | `/databases/{id}/server/databases` | `system.admin` | — | `database.create`, `database.connection.sibling` | refused |
| `POST` | `/databases/{id}/server/databases/connect` | `system.admin` | — | `database.connection.sibling` | refused |
| `GET` | `/databases/{id}/server/extensions` | read | — | — | — |
| `POST` | `/databases/{id}/server/extensions` | `system.admin` | — | `database.extension.create` | refused |
| `GET` | `/databases/{id}/server/extensions/{name}` | read | — | — | — |
| `PATCH` | `/databases/{id}/server/extensions/{name}` | `system.admin` | — | `database.extension.update` | refused |
| `DELETE` | `/databases/{id}/server/extensions/{name}` | `system.admin` + `destructive` | yes | `database.extension.drop` | refused |
| `GET` | `/databases/{id}/server/grants` | read | — | — | — |
| `POST` | `/databases/{id}/server/preload` | `system.admin` | — | `database.extension.preload` | refused |
| `GET` | `/databases/{id}/server/privileges` | read | — | — | — |
| `GET` | `/databases/{id}/server/roles` | read | — | — | — |
| `POST` | `/databases/{id}/server/roles` | `system.admin` | — | `database.role.create` | refused |
| `GET` | `/databases/{id}/server/roles/{name}` | read | — | — | — |
| `PUT` | `/databases/{id}/server/roles/{name}` | `system.admin` | — | `database.role.alter` | refused |
| `DELETE` | `/databases/{id}/server/roles/{name}` | `system.admin` + `destructive` | yes | `database.role.drop` | refused |
| `POST` | `/databases/{id}/server/roles/{name}/grant` | `system.admin` | — | `database.role.grant` | refused |
| `POST` | `/databases/{id}/server/roles/{name}/privileges` | `system.admin` | — | `database.role.grant` | refused |
| `POST` | `/databases/{id}/server/roles/{name}/privileges/revoke` | `system.admin` | — | `database.role.revoke` | refused |
| `GET` | `/databases/{id}/server/settings` | read | — | — | — |

**Moving data** (21)

| Method | Path | Capability | Destructive | Audited as | Protected |
| --- | --- | --- | --- | --- | --- |
| `POST` | `/databases/{id}/backup` | `service.control` | — | `database.backup`, `.finish` | open |
| `GET` | `/databases/{id}/backup/download` | `service.control` | — | `database.backup.download` † | — |
| `GET` | `/databases/{id}/backups` | read | — | — | — |
| `DELETE` | `/databases/{id}/backups` | `system.admin` + `destructive` | yes | `database.backup.delete` | open |
| `POST` | `/databases/{id}/backups/upload` | `service.control` | — | `database.backup.upload` | open |
| `POST` | `/databases/{id}/copy` | `system.admin` | — | `database.copy`, `.finish` | refused |
| `GET` | `/databases/{id}/export` | read | — | `database.export`, `.finished` † | — |
| `POST` | `/databases/{id}/export/query` | `service.control` | — | `database.export.query`, `.finished` | by content |
| `GET` | `/databases/{id}/export/status` | read | — | — | — |
| `POST` | `/databases/{id}/import` | `service.control` | by content: `truncate` | `database.import` | refused |
| `POST` | `/databases/{id}/import/upload` | `service.control` | by content: `replace` | `database.import` | refused |
| `GET` | `/databases/{id}/imports` | read | — | — | — |
| `POST` | `/databases/{id}/imports` | `system.admin` | — | `database.import.start`, `.finish` | refused |
| `POST` | `/databases/{id}/imports/inspect` | `system.admin` | — | — | refused |
| `GET` | `/databases/{id}/imports/{importId}` | read | — | — | — |
| `DELETE` | `/databases/{id}/imports/{importId}` | `system.admin` + `destructive` | yes | `database.import.cancel`, `.forget` | refused |
| `POST` | `/databases/{id}/imports/{importId}/cutover` | `system.admin` | — | `database.import.cutover`, `.cutover.finish` | refused |
| `POST` | `/databases/{id}/orm` | read | — | `database.orm.generate` | open |
| `POST` | `/databases/{id}/restore` | `destructive` | yes | `database.restore`, `.finish` | refused |
| `POST` | `/databases/{id}/seed` | `service.control` | — | `database.seed` | refused |
| `POST` | `/databases/{id}/seed/plan` | read | — | — | open |

**A server's release** (5)

| Method | Path | Capability | Destructive | Audited as | Protected |
| --- | --- | --- | --- | --- | --- |
| `GET` | `/databases/{id}/upgrade` | `system.admin` | — | — | — |
| `POST` | `/databases/{id}/upgrade` | `system.admin` + `destructive` | yes | `database.upgrade`, `.finish`, `.step` | refused |
| `POST` | `/databases/{id}/upgrade/check` | `system.admin` | — | `database.upgrade.check`, `.finish` | open |
| `POST` | `/databases/{id}/upgrade/complete` | `system.admin` + `destructive` | yes | `database.upgrade.complete` | open |
| `POST` | `/databases/{id}/upgrade/revert` | `system.admin` + `destructive` | yes | `database.upgrade.revert`, `.finish` | refused |

**Point-in-time recovery** (9)

| Method | Path | Capability | Destructive | Audited as | Protected |
| --- | --- | --- | --- | --- | --- |
| `GET` | `/databases/{id}/pitr` | read | — | — | — |
| `POST` | `/databases/{id}/pitr` | `system.admin` | by content: not a `preview` | `database.pitr.enable`, `.finish` | open |
| `PUT` | `/databases/{id}/pitr` | `system.admin` | — | `database.pitr.update` | open |
| `DELETE` | `/databases/{id}/pitr` | `system.admin` + `destructive` | yes | `database.pitr.disable`, `.finish` | open |
| `DELETE` | `/databases/{id}/pitr/archives/{aid}` | `system.admin` + `destructive` | yes | `database.pitr.archive.delete`, `.finish` | open |
| `POST` | `/databases/{id}/pitr/backup` | `service.control` | — | `database.pitr.backup`, `.finish` | open |
| `POST` | `/databases/{id}/pitr/restore` | `system.admin` | — | `database.pitr.restore`, `.finish` | open |
| `POST` | `/databases/{id}/pitr/restore-points` | `service.control` | — | `database.pitr.restore_point` | open |
| `POST` | `/databases/{id}/pitr/restores/{rid}/switch` | `system.admin` + `destructive` | yes | `database.pitr.switch`, `.finish` | refused |

**Redis** (43)

| Method | Path | Capability | Destructive | Audited as | Protected |
| --- | --- | --- | --- | --- | --- |
| `GET` | `/databases/{id}/keys` | read | — | — | — |
| `DELETE` | `/databases/{id}/keys` | `destructive` | yes | `database.redis.delete`, `.member.delete` | refused |
| `POST` | `/databases/{id}/keys/bulk` | `service.control` | by content: `delete`, `expire` | `database.redis.bulk` | by content |
| `POST` | `/databases/{id}/keys/copy` | `service.control` | by content: `overwrite` | `database.redis.copy` | refused |
| `POST` | `/databases/{id}/keys/expire` | `service.control` | — | `database.redis.expire` | refused |
| `POST` | `/databases/{id}/keys/field/expire` | `service.control` | — | `database.redis.field.expire` | refused |
| `POST` | `/databases/{id}/keys/field/persist` | `service.control` | — | `database.redis.field.persist` | refused |
| `GET` | `/databases/{id}/keys/members` | read | — | — | — |
| `GET` | `/databases/{id}/keys/meta` | read | — | — | — |
| `POST` | `/databases/{id}/keys/persist` | `service.control` | — | `database.redis.persist` | refused |
| `GET` | `/databases/{id}/keys/raw` | read | — | — | — |
| `POST` | `/databases/{id}/keys/rename` | `service.control` | by content: `overwrite` | `database.redis.rename` | refused |
| `GET` | `/databases/{id}/keys/stream` | read | — | — | — |
| `POST` | `/databases/{id}/keys/stream/ack` | `service.control` | — | `database.redis.stream.ack` | refused |
| `POST` | `/databases/{id}/keys/stream/claim` | `service.control` | — | `database.redis.stream.claim` | refused |
| `POST` | `/databases/{id}/keys/stream/groups` | `service.control` | — | `database.redis.stream.group.create`, `.setid` | refused |
| `DELETE` | `/databases/{id}/keys/stream/groups` | `destructive` | yes | `database.redis.stream.group.delete`, `.consumer.delete` | refused |
| `GET` | `/databases/{id}/keys/stream/pending` | read | — | — | — |
| `POST` | `/databases/{id}/keys/stream/trim` | `destructive` | yes | `database.redis.stream.trim` | refused |
| `GET` | `/databases/{id}/keys/tree` | read | — | — | — |
| `GET` | `/databases/{id}/keys/value` | read | — | — | — |
| `POST` | `/databases/{id}/keys/value` | `service.control` | — | `database.redis.set` | refused |
| `GET` | `/databases/{id}/redis/acl` | read | — | — | — |
| `PUT` | `/databases/{id}/redis/acl/{name}` | `system.admin` | — | `database.redis.acl.set` | refused |
| `DELETE` | `/databases/{id}/redis/acl/{name}` | `system.admin` + `destructive` | yes | `database.redis.acl.delete` | refused |
| `GET` | `/databases/{id}/redis/analysis` | read | — | — | — |
| `POST` | `/databases/{id}/redis/classify` | read | — | — | open |
| `GET` | `/databases/{id}/redis/clients` | read | — | — | — |
| `POST` | `/databases/{id}/redis/clients/kill` | `destructive` | yes | `database.redis.client.kill` | open |
| `POST` | `/databases/{id}/redis/command` | `service.control` | by content: command | `database.redis.command` | by content |
| `GET` | `/databases/{id}/redis/commands` | read | — | — | — |
| `GET` | `/databases/{id}/redis/commandstats` | read | — | — | — |
| `GET` | `/databases/{id}/redis/config` | read | — | — | — |
| `PUT` | `/databases/{id}/redis/config` | `system.admin` | — | `database.redis.config.set` | refused |
| `GET` | `/databases/{id}/redis/latency` | read | — | — | — |
| `GET` | `/databases/{id}/redis/monitor` | `system.admin` | — | `database.redis.monitor.open` † | — |
| `POST` | `/databases/{id}/redis/publish` | `service.control` | — | `database.redis.publish` | refused |
| `GET` | `/databases/{id}/redis/pubsub` | read | — | — | — |
| `POST` | `/databases/{id}/redis/save` | `service.control` | — | `database.redis.save` | refused |
| `GET` | `/databases/{id}/redis/server` | read | — | — | — |
| `GET` | `/databases/{id}/redis/slowlog` | read | — | — | — |
| `POST` | `/databases/{id}/redis/slowlog/reset` | `destructive` | yes | `database.redis.slowlog.reset` | refused |
| `GET` | `/databases/{id}/redis/subscribe` | `system.admin` | — | `database.redis.subscribe.open` † | — |

**MongoDB** (49)

| Method | Path | Capability | Destructive | Audited as | Protected |
| --- | --- | --- | --- | --- | --- |
| `POST` | `/databases/{id}/aggregate` | `service.control` | by content: pipeline | `database.aggregate` | by content |
| `POST` | `/databases/{id}/collections` | `service.control` | — | `database.collection.create` | refused |
| `DELETE` | `/databases/{id}/collections` | `destructive` | yes | `database.collection.drop` | refused |
| `GET` | `/databases/{id}/collections/indexes` | read | — | — | — |
| `POST` | `/databases/{id}/documents` | `service.control` | — | `database.document.insert` | refused |
| `PATCH` | `/databases/{id}/documents` | `service.control` | — | `database.document.replace` | refused |
| `DELETE` | `/databases/{id}/documents` | `destructive` | yes | `database.document.delete` | refused |
| `POST` | `/databases/{id}/mongo/aggregate/preview` | read | — | — | open |
| `GET` | `/databases/{id}/mongo/collection` | read | — | — | — |
| `GET` | `/databases/{id}/mongo/collections` | read | — | — | — |
| `POST` | `/databases/{id}/mongo/collections` | `service.control` | — | `database.collection.create` | refused |
| `PATCH` | `/databases/{id}/mongo/collections` | `service.control` | by content: cap, expiry | `database.collection.modify` | refused |
| `DELETE` | `/databases/{id}/mongo/collections` | `destructive` | yes | `database.collection.drop` | refused |
| `POST` | `/databases/{id}/mongo/collections/rename` | `service.control` | by content: `dropTarget` | `database.collection.rename` | refused |
| `POST` | `/databases/{id}/mongo/command` | `service.control` | by content: command | `database.mongo.command` | by content |
| `POST` | `/databases/{id}/mongo/command/classify` | read | — | — | open |
| `GET` | `/databases/{id}/mongo/commands` | read | — | — | — |
| `POST` | `/databases/{id}/mongo/count` | read | — | — | open |
| `GET` | `/databases/{id}/mongo/databases` | read | — | — | — |
| `POST` | `/databases/{id}/mongo/document` | read | — | — | open |
| `POST` | `/databases/{id}/mongo/documents` | `service.control` | — | `database.document.insert` | refused |
| `PUT` | `/databases/{id}/mongo/documents` | `service.control` | — | `database.document.replace` | refused |
| `PATCH` | `/databases/{id}/mongo/documents` | `service.control` | by content: every document | `database.document.update` | by content |
| `DELETE` | `/databases/{id}/mongo/documents` | `destructive` | yes | `database.document.delete` | by content |
| `POST` | `/databases/{id}/mongo/documents/clone` | `service.control` | — | `database.document.clone` | refused |
| `POST` | `/databases/{id}/mongo/explain` | read | — | — | open |
| `GET` | `/databases/{id}/mongo/export` | read | — | `database.export` † | — |
| `POST` | `/databases/{id}/mongo/find` | read | — | — | open |
| `GET` | `/databases/{id}/mongo/indexes` | read | — | — | — |
| `POST` | `/databases/{id}/mongo/indexes` | `service.control` | by content: TTL | `database.index.create` | refused |
| `PATCH` | `/databases/{id}/mongo/indexes` | `service.control` | by content: TTL | `database.index.modify` | refused |
| `DELETE` | `/databases/{id}/mongo/indexes` | `destructive` | yes | `database.index.drop` | refused |
| `POST` | `/databases/{id}/mongo/killop` | `destructive` | yes | `database.mongo.killop` | open |
| `GET` | `/databases/{id}/mongo/ops` | read | — | — | — |
| `GET` | `/databases/{id}/mongo/profiler` | read | — | — | — |
| `PUT` | `/databases/{id}/mongo/profiler` | `system.admin` | — | `database.mongo.profiler` | refused |
| `GET` | `/databases/{id}/mongo/replication` | read | — | — | — |
| `GET` | `/databases/{id}/mongo/roles` | read | — | — | — |
| `POST` | `/databases/{id}/mongo/schema` | read | — | — | open |
| `GET` | `/databases/{id}/mongo/server` | read | — | — | — |
| `GET` | `/databases/{id}/mongo/users` | read | — | — | — |
| `POST` | `/databases/{id}/mongo/users` | `system.admin` | — | `database.role.create` | refused |
| `PUT` | `/databases/{id}/mongo/users` | `system.admin` | — | `database.role.alter` | refused |
| `DELETE` | `/databases/{id}/mongo/users` | `system.admin` + `destructive` | yes | `database.role.drop` | refused |
| `POST` | `/databases/{id}/mongo/users/grant` | `system.admin` | — | `database.role.grant` | refused |
| `POST` | `/databases/{id}/mongo/users/revoke` | `system.admin` | — | `database.role.revoke` | refused |
| `GET` | `/databases/{id}/mongo/validation` | read | — | — | — |
| `PUT` | `/databases/{id}/mongo/validation` | `service.control` | — | `database.validation.set` | refused |
| `POST` | `/databases/{id}/mongo/validation/check` | read | — | — | open |

What the table cannot say in a cell:

- `DELETE /databases/{id}/database` is the one typed-phrase route of the section
  ([invariant 3](../security/invariants.md#invariant-3-which-routes-take-a-typed-phrase)).
- `POST …/explain` is on the read surface until it carries `analyze`, which executes the statement:
  the handler then asks for `service.control` and whatever `authoriseSQL` asks of the statement.
- `POST …/restore` with a new database as its target also needs `system.admin`, checked in the
  handler; `POST …/redis/command` and `POST …/mongo/command` ask for `system.admin` for the commands
  their classifiers mark as administrative.
- A `/ddl/*` route in the `service.control` or `system.admin` group needs the destructive capability
  when its plan rewrites a column's type or calls a function `dbx` does not vouch for (`runDDL`), for
  a preview as much as for a run; `PATCH …/ddl/row-security` asks for it when the body turns row-level
  security off or stops forcing it.
- `POST …/pitr` and `POST …/pitr/restore` take `preview: true`, which answers the plan and runs
  nothing. The setup form reads the plan as it is filled in, so `POST …/pitr` asks for the destructive
  capability and spends `destrLim` by hand (`requireDestructive`) only for a request that is not a
  preview; a form filled in key by key would otherwise have spent the budget its real request needs.
- The two Redis live feeds, `…/redis/monitor` and `…/redis/subscribe`, are WebSockets.
- `GET /databases/inventory`, `GET /databases/fleet` are `read` and answer more
  for `system.admin`; the sections above say what.

### Database provisioning for deployments

The standalone Database source on `/deploy/new` and Start a new one on `/databases/new` share
`DatabaseCreation` in `components/database/connect/start.tsx`: the same shelved catalogue, settings
panel, version and account inputs, validation and explicit public-port choice (local by default).
Their progress uses the app deployment's stage animation and moving panel border, tied to provision,
adopt and ping responses. Deploy ends with a masked connection string; Databases opens the connection's
home. Once a container exists, retries continue adoption/verification and never provision another one;
Deploy remembers its identity when the reader switches sources. Project linking sheets retain their
compact `DatabaseQuickDeploy` form and reuse the verified-result renderer, `deploy/database-ready.tsx`.

Deployment setup reuses `/databases/provision`, `/adopt`, `/ping` and the explicit admin URL read, and
draws its engines from the same `GET /databases/provision/options` the add flow reads, so the two offer
one list of templates. A project's Databases settings reuses the same two reads for a linked connection:
`/ping` behind its Test connection verb, and `?target=container` behind Copy application URL, which stays
admin-only and audited there as everywhere else. The add flow can also limit what the new container may
use: *Limit the memory and processors it may use* fills in the template's `defaultLimits` (1 GiB and a
core; 512 MiB for a key–value server, 2 GiB and two cores for ClickHouse), kept to half this machine's
memory and its processors as `GET /databases/provision/capacity` (`system.admin`) reads them, and sends
`memoryMb` and `cpus`; a request that names neither starts with none, as before. `/provision` holds them to
the same rules as a change on Settings → Resources, and to the template's `minMemoryMb`: MySQL's first start
loads its time zone tables into a temporary server before it makes the account, and under 512 MiB the
kernel stops it part way, leaving a data directory the next start does not initialise and a server with no
account to sign in with. Quick setup provisions with `exposure: local`, so those
ports are published to host loopback only, which is also what a request that names no exposure gets (see
the inventory section above). The data volume is named `<container>-data`. With no name supplied,
provisioning reserves the first available `jd-<engine>`, `jd-<engine>-2`, etc., checking both containers
and retained data volumes. In-flight requests reserve distinct names before pulling images. Explicit
names remain exact and provisioning refuses with `409 volume_exists` when that volume already exists: an
official image that finds a populated data directory skips initialisation, so the freshly generated
password is never set and the server refuses every sign-in while looking reachable. The `/ping` reply's
`error` is surfaced by both callers — the add flow (`components/database/connect/start.tsx`) and
deployment quick setup (`components/deploy/quick-database.tsx`) — so an engine's own refusal is not
reported as "not ready". Two of the templates exist for deployments: `pgvector`
(`pgvector/pgvector:pg18`) and `postgis` (`postgis/postgis:18-3.6-alpine`, listed and accepted only on
x86-64, the one architecture it is published for) are the same PostgreSQL 18 contract with the extension
a retrieval or geospatial schema creates on its first migration, which the official image lacks.
Quick setup leaves out the full template until its image has been built from Add a database.
Deployment setup preselects one when detection read that extension from the schema. `mongodb` is refused
before any pull on a CPU without AVX (x86-64) or ARMv8.2 atomics (arm64), where MongoDB 5 and later die
with an illegal instruction. The URL read also takes `format` and `database`; see [deployment database
networks](../deployments/database-networks.md). `/adopt` signs in before it saves, so the caller's retry
loop is what waits for the engine. It is idempotent by driver, address, database and login user: a
matching connection gets that row back, and when the container's password differs the row is re-sealed in
place — once the new password has been seen to work — while preserving its transport and query options
(audited as `database.connection.refresh`, pool dropped). Different databases, users and engines on one
address are never overwritten; ambiguous duplicate matches require an explicit saved-connection choice.
This keeps replacement credentials current — a database removed and created again under the same name
takes the same loopback port back with a new password, and a `${{database.N}}` reference must keep
resolving to a URL that works. The URL endpoint's `target=container` option resolves a matching Docker
database to a stable `db-ID.jd.internal` hostname without changing networking or the saved DSN;
`target=host` and the default retain the original DSN. Replies are non-cacheable and audits contain the
connection identity and target, never credentials. Setup saves the returned typed database reference.
Activation and reconciliation own the environment network and database alias, including replacement with
a different IP. See [deployment database networks](../deployments/database-networks.md).

Redis provisioning writes its generated password to a mode-0600 configuration inside the container
before the official entrypoint drops privileges. Redis requires explicit password configuration;
setting `REDIS_PASSWORD` alone does not enable authentication. The bootstrap is constant shell text,
with the generated secret read from the environment rather than placed in argv.
See [Redis configuration](https://redis.io/docs/latest/operate/oss_and_stack/management/config/).
MongoDB detection records `authSource=admin` when credentials come from `MONGO_INITDB_ROOT_USERNAME`,
so selecting an application database does not change where the root account authenticates.
See the [official MongoDB image](https://hub.docker.com/_/mongo/).

`JD_DEPLOY_LIVE=1 go test ./internal/api -run TestLiveDeploymentDatabaseConnection -count=1 -v`
provisions five of the templates (PostgreSQL, MySQL, MariaDB, Redis and MongoDB), adopts and pings them,
authenticates from separate application containers, replaces each database at a different IP, and
authenticates from the same client container using its unchanged URL after reconciliation. It verifies
loopback-only publication, network ownership and cleanup, then removes its own
containers/volumes/networks.

## Proxy

- Public-address discovery excludes CGNAT (`100.64.0.0/10`) as well as private, loopback and link-local
  addresses, so Tailscale interfaces cannot produce a public deployment hostname or a false public DNS
  match. Deployment HTTP-01 issuance checks DNS destinations before ordering; existing certificates
  remain usable for private names. Certbot error summaries prefer ACME validation details and skip the
  generic community-help footer.
- Deployment certificate orders use the running nginx listener's webroot instead of trying to bind
  standalone Certbot over occupied port 80. A temporary, validated challenge-only route and a file probe
  establish host/container filesystem visibility before ordering, with snapshot restoration on every
  exit. The fixed `/srv/just-dashboard-acme` root survives deployment route activation and site form
  round trips through `managedAcme`; ordinary site's ACME roots remain `/var/www/html`. Docker Caddy
  ownership uses native automatic HTTPS and shared routes; unsupported owners are reported explicitly.
  See [the ingress decision](../deployments/caddy-ingress.md) for provisioning, recovery and live tests.
- **Listening ports** (`ports.go`, `ports_owner.go`) are read from the kernel's own tables,
  `/proc/net/{tcp,tcp6,udp,udp6}` (under `HOST_PROC` when set, the variable gopsutil reads for the
  process details), not from gopsutil's connection list: that list keeps one holder per socket, and its
  `/proc` walk meets PID 1 first, so a socket-activated sshd, whose port systemd holds too, was listed as
  systemd, `/sbin/init`. `socketHolders` keeps every PID holding a listening inode and `ownerOf` sets init
  aside, then names the holder whose parent (`/proc/<pid>/stat`) is not itself a holder: a prefork
  server's master, not one of the workers that inherited the socket. Not the lowest PID, because once the
  PID counter wraps a reload's respawned workers are numbered below their master. The lowest PID decides
  only between unrelated holders, and init only when it holds the socket alone.
  `Listener.Scope` is `loopback`, `interface` (one specific address) or `all`; `Exposed` is every scope but
  loopback. It used to mean "bound to a wildcard", which drew caddy on the tailnet address as loopback and
  failed to identify a database on a public IP as exposed. `Listener.Reach` is
  netsec's grade of the address (`loopback`, `host` for a bridge, `network`, `public`, `all`, see
  observability-security.md), and `Listener.Network` and `Listener.Interface` name what it is made of
  (`tailnet` on `tailscale0`, `docker` on `docker0` or a `br-<network id>`, `public` on `ens3`, `vpn`,
  `uplink` for a private address on the default-route interface, `private`, another `bridge`,
  `link-local`, `loopback`, `all`); all three come from one `HostNetwork.Place`, which needs the
  interfaces, so `GET /ports` fills them and `ListListeners` leaves them empty. `GET /ports` also fills
  `Listener.Level` and `InboundDefault` from `netsec.GradePort`, with the firewall status read beside the
  walk: the service catalogue's reach level for that socket and the firewall's
  inbound default when that is what holds it to a warning, or `PastFirewall` — `docker` for a port
  docker-proxy holds, `rule` with `FirewallRule` for one a rule admits from anywhere — when the default
  does not hold it (see observability-security.md). The ports page words a socket by its network
  ("Tailnet only · tailscale0") and colours a database by the same `Level`. `Listener.Family`
  (`ipv4`/`ipv6`) is the kernel table the socket is in, and `Listener.PPID` the owner's parent; the page
  folds a service's two families on one network into one row and one count when one process holds both,
  or one program run by one account was started by one parent (not init) or with the same command line
  but for its addresses — Docker holds a published port's two families in two docker-proxy processes,
  dockerd's children, each told its own `-host-ip`. The overview's internet-facing fact counts the same
  folded rows. A UDP socket on port 0 is not listed, and `GET /ports` gives the walk ten seconds before
  a retryable 504, which the page offers to try again. `GET /ports/meta` answers `{ephemeralRange: {low,
  high}}` from `EphemeralPorts`, `net.ipv4.ip_local_port_range` read under the same process table
  (`null` where it cannot be read), apart from the list so `/ports` stays the plain array other pages
  read: the page sets aside loopback sockets inside it on request, and the Connections page links a
  connection's local port to the ports page only outside it, where somebody chose the port.
  Every exposed socket carries `firewall` (`netsec.JudgeFirewall` in `reach.go`): `allowed`, `restricted`
  (a rule admits one source ahead of a refusing default), `blocked`, `off`, `docker` (published past ufw's
  or iptables' input chain; firewalld is not claimed) or `unknown`, with the deciding rule's number and
  action, read in first-match order and passing over rules whose target the listing cannot say.
  `GET /ports/firewall` answers `{backend, available, enabled, editable, incoming, orphanRules}`:
  `netsec.OrphanRules` lists the inbound allows on single ports or lists that no socket and no Docker
  publication answers (ranges, every-port, profile and interface rules and ufw's IPv6 twins are left out).
  It reads the firewall afresh each call, uncached, because the page deletes an orphan by number through
  the existing destructive `DELETE /firewall/rules/{n}` and ufw renumbers on every delete; the page also
  re-reads `/firewall/` and refuses when the number now names another rule. Any signed-in account may
  read it, as it may read `/firewall/`.
  `GET /ports` also says what the proxy does with each socket (`proxysvc.AttachProxy` in
  `ports_routes.go`): `routes` are the enabled sites whose upstream reaches the TCP socket
  (`SitesForUpstreamPort`, shared with the container routes view: `localhost`, a loopback, `0.0.0.0` or one
  of the host's own addresses at that exact port — `:30000` is not `:3000` — and only the loopback spellings
  for a loopback socket; a container or other host name never matches), `stream` the nginx stream on an
  nginx-held socket when nginx includes the stream directory, and `servedSites` the enabled nginx sites
  listening on an nginx-held port. `GET /ports/free?protocol=&address=&from=&count=` (system.admin: it binds
  each candidate for a moment on the address named) finds up to 20 ports with `portalloc.Select`, searching
  up from `from` and passing over every host port a container publishes or, stopped, keeps in its
  `PortBindings` (`dockerx.HostPortBindings`, ranges expanded); it answers `{ports, skipped,
  containersChecked}` so the page does not claim to have avoided containers when Docker did not answer.
  It also passes over claims a bind cannot see (`api.freePortPolicy`, `ports_reservations.go`), each
  read beside the search under the owner-source bound and consulted in a fixed order: unexpired
  deployment candidate leases (`deploy_port_leases`, matched by address overlap), the tailnet preview
  range `selfcfg.TailnetPortMin`–`TailnetPortMax` (for a wildcard or tailnet address only), the kernel's
  ephemeral range (outgoing connections take ports from it), an enabled firewall's inbound rule already
  naming the port (admitting it from where the rule says, or refusing it — whatever binds there meets
  that policy at once; read through `netsec.InboundRuleFor`, so an interface-limited or profile rule is
  not taken for one covering every address), and an enabled gateway forward translating it away. A
  loopback search skips the firewall and gateway, which decide only what other hosts reach.
  `JD_PORT_POLICY_LIVE=1` (`TestLiveFreePortPolicyOnThisHost`, run as root from a compiled test binary)
  reads every source on the actual host for a wildcard search and lists what each would pass over,
  binding nothing. `reservations` names each
  passed-over port (at most 32) and why; `sources` lists every owner consulted with `checked`,
  `unavailable` (with the reason) or `not_supplied` — provider reservations, for which no adapter
  exists, always say so rather than being left out.
  `GET /ports/external` (system.admin, like the external checks it reads) joins the external-check
  owner's retained checks to their enrolled sources (`ports_external.go`): per check the port, family,
  source, placement, measured address, whether that address is on this host, lifecycle status and the
  TCP stage's state and basis — an expired or cancelled check stays `unknown`, never a failed
  measurement — plus each enrolled, unrevoked source's scopes, so the page can offer a check for a
  port a scope names. It starts nothing; a check is created through `POST /network/external/checks`.
  An unavailable owner answers 200 with `error` rather than an empty reading.
  Each listener carries `pids`, every process holding the socket, and on TCP `clients` — the ESTABLISHED
  connections in the same socket tables (`countClients`: same family and local port, the exact address
  before a wildcard listener), with the five busiest remote addresses. `POST /ports/identify`
  `{protocol, address, port, serverName?}` (system.admin, audited as `proxy.ports.identify`: it sends
  traffic the dashboard originates) answers 404 `not_listening` unless a TCP socket listens on exactly that
  address and port now (`proxysvc.ListeningAt`), so it cannot be aimed elsewhere; a wildcard is dialled on
  its family's loopback. Within 5 seconds it reads a banner (1.5 s), and only if none came, a TLS handshake
  (certificate read, not verified; ALPN offered h2 and http/1.1; a refusal alert still means TLS) and a
  `HEAD /` over TLS or plain, reporting status, `Server` and `Location`. `serverName` must be a DNS name and
  is sent as SNI and Host.
  `TestListListenersNamesTheDaemonNotInitOnThisHost` checks the owner on the real host and runs only as
  root; it reads `/proc` and changes nothing.
  **Owners.** `ListListeners` also reads, once per holder, its start (`StartedAt`, the processes page's
  own `CreateTime`, so a signal sent from the page can refuse a reused PID), its supervisor from its
  cgroup under the same process table (`procs.ManagerOf`: `Manager`/`ManagerName`, `systemd` and a unit,
  `container` and a short ID, `session`), and `DisplayName` — the executable's base name, or the
  command line's first word where another account's `exe` is unreadable — where the kernel's name is a
  thread's (node names its main thread `MainThread`); `Process` stays the raw comm, which
  `dbx.DetectHost` and the HTTP-01 planner match on. `GET /ports` then runs `AttributeOwners` with what
  `ownerInput` read beside the walk, each source under four seconds and left out when it fails: the
  running containers from `dockerx.ListRunning` (the Engine's list, no inspects), `systemctl list-sockets
  --all --show-types --output=json` through `procs.Systemd.Sockets` (read with `SocketUnitAt`: inet
  stream and datagram addresses only), and — after the walk, and only when some owner's parent is a PM2
  daemon (`UnderPM2`), since `pm2 jlist` starts a daemon where none runs — PM2's app PIDs. A socket systemd listens on for a service
  carries `SocketUnit` and `Activates` (`ssh.socket` → `ssh.service`); a PM2 app's PID becomes `pm2` and
  its name; a socket whose protocol, host address and port a container's published binding names — the
  kernel lets one socket hold them, so whatever holds it is Docker's (docker-proxy, dockerd, or a holder
  this account cannot see) — carries `Container` with `Published`, as does a wildcard docker-proxy or
  unseen socket whose other family's binding names it (a Docker before 20.10 published every interface
  as one proxy on `::`); a container on the host's network is joined by its cgroup's ID. A published
  binding no socket stands for — the userland proxy off, NAT rules alone — becomes a row of its own with
  `Source: "docker-nat"` and no PID. `Container.Deployment` names the project and environment its
  `io.just-dashboard.environment-id` label (on a container also labelled `io.just-dashboard.managed`)
  points at, looked up in `deploy_environments`. `Self` marks the dashboard's own sockets: its own PID,
  and every container of the compose project whose container mounts `JD_DATA_DIR` (the `backend`
  service's where several do); the ingress the dashboard creates serves deployments and is not its own.
- **The ports history** (`ports_history.go`). The kernel keeps no record of what listened before, so
  `PortRecorder`, started from `startProxyExtras` and stopped from `stopProxyExtras`, walks
  `ListListeners` at once and then every minute and compares the result with the stretches still open
  in `listener_observations` (`store/schema_proxy.go`): one row per stretch of time one socket listened
  with one owner, `opened_after`/`first_seen` the samples either side of its opening and
  `gone_after`/`gone_at` of its closing, Unix seconds. `listener_history` is one row, when recording
  began and the latest sample, which is what a change is dated after — across a minute or across a
  restart, so a socket that went while the dashboard was stopped is dated to the whole unsampled span.
  `diffListeners` is pure: a socket is its protocol, family, address and port, and stays the same
  socket while the same program run by the same account holds it; a new PID alone (a restart between
  samples) only updates the row, an owner this account could not read matches any rather than
  recording a close and reopen, and a different program or account closes the stretch and opens
  another. The first sample ever taken is the baseline (`opened_after` NULL: listening when recording
  began, never reported as opened); a walk that fails records nothing rather than every socket
  closing; loopback sockets inside `net.ipv4.ip_local_port_range` are not recorded, as the page sets
  them aside. A partial unique index holds one open stretch per socket. Stretches that closed more than
  30 days ago are pruned at start and hourly; a failing walk is logged once until it recovers.
  `GET /ports/history?hours=&limit=` (any signed-in account, as `/ports`; hours 1–720, default 24;
  limit 1–5000, default 1000, anything else a 400) answers `{recordingSince, lastSample, stalled,
  intervalSeconds, retentionDays, since, events, truncated}`: events newest first and by port within a
  sample, a socket's closing before its opening, each the socket as recorded plus `kind`, `at`, `after`,
  `since` (first seen) and `baseline`, placed and graded by `placeListeners` against the host's network
  and firewall as they are now; `stalled` says the latest sample is over three intervals old by the
  server's clock. `GET /ports` adds `firstSeen` to a socket the history saw open while it was recording
  and whose recorded owner still holds it (`PortRecorder.FirstSeen`), and lists the sockets undated,
  with a warning in the log, if the history cannot be read. Tests: `TestDiffListeners`,
  `TestPortRecorderKeepsWhatOpenedAndClosed` (a fake clock and lister through a restart gap, a failed
  walk, the window, the limit, pruning and `stalled`), `TestPortRecorderRecordsARealSocket` (the real
  kernel tables), `TestListenerObservationsKeepOneOpenStretchPerSocket`, and the API's
  `handlers_ports_history_test.go`.
- **HTTPS from the site form** (`site_certs.go`, `site-certificate.tsx`). `GET /certificates/covering?domains=`
  (system.admin, since it returns the remembered contact email) lists unexpired certificates covering
  every domain (a wildcard covers one label), each paired with its key the way deployment resolution
  pairs one, plus `certbot.email` from settings and the managed webroot. `POST /certificates/issue` stores
  the email it was started with under `certbot.email` and takes `dryRun` (`certbot --dry-run`). A site
  saved with `managedAcme` that answers plain HTTP itself (not TLS-forced, not a redirect, whose
  server-level `return` runs before location selection) renders `location ^~ /.well-known/acme-challenge/`
  rooted at the managed webroot with `allow all; auth_basic off;`, so neither an allow list, a password
  nor the exploit regexes block the validator. "Get a certificate" saves the site first when its file
  does not serve that path (as it is live for an edit, over plain HTTP for a new site), issues over
  webroot (optionally after a dry run), then saves it with HTTPS on; when no save was needed it only
  fills the paths. Wildcards, redirect sites and sites nginx does not read are not offered issuance.
- **Site builder** (`sites.go`, `sites_render.go`, `sites_parse.go`, `sites_apply.go`). `SiteSpec` is our
  shape, not nginx's, for the reason `ContainerSpec` is not `container.Config`; rendering happens **on the
  server** so a spec has one meaning, and the output is hand-written rather than templated because order
  carries meaning to whoever maintains the file after this dashboard is gone. `SaveSite` (`ApplySite` is
  its positional form) puts the **symlink in before `nginx -t`** — a new file in `sites-available` is not
  in the include tree, so the test has nothing to say about it — and undoes both together on failure.
  What a save does, and says, beyond that:
  - **The link is left as it was unless the save asks to enable.** The site form used to post
    `enable: true` for every save, so fixing one field of a disabled site put it back on the internet.
    `POST /proxy/sites/` takes `enable: "enable" | "keep"`; the old `true`/`false` still parse, and
    `false` always meant keep (it declined to make a link and never removed one). A site that has a link
    keeps it, relinked to the file being saved, and `SiteResult.enabled` says which it is.
    `GET /proxy/sites/{name}` and every preview say whether nginx reads the file (`enabled`, from
    `SiteFile`: linked into sites-enabled under its own name or any other — a site linked as
    `sites-enabled/010-app` is enabled, and a save neither stages nor makes a second link for it, which
    had nginx read the file twice and report it conflicting with itself — or in conf.d with a name ending
    in `.conf`) and whether it is in conf.d (`confd`). The form's footer follows them: a disabled site
    offers **Save (stays disabled)** (`keep`, no reload) and **Save and enable** (`enable`, reload)
    instead of a "Save and reload" that did neither, and only the first when its name's link is another
    file's or the site is in conf.d. The listing (`nginxVHosts`) likewise calls a site enabled only when
    its name's link names its own file (`enabledElsewhere`), so it no longer says "serving" beside a form
    that says the link is another site's; a site enabled only under another name is still listed as
    disabled there, which is the listing's and the toggle's to change. A `sites-enabled/<name>` that is a
    file of its own (a `cp` where a link was meant) is listed as serving, since nginx serves it
    (`servedCopy()`). `SiteFile.ServedCopy` reaches the form as `servedCopy`, and the form says nginx
    serves that file and not this one, and offers only **Save** (`keep`, no reload). The save's result
    carries `servedCopy` and a note that it changed nothing nginx serves and was not tested, where it
    used to say "It stays disabled" of a name nginx answers (`TestSaveSiteSaysNginxServesAFileOfItsOwn`).
  - **A conf.d site is saved where the listing found it.** A conf.d file is switched off by a name that
    does not end in `.conf` (`app.conf.disabled`, or `app`). `siteTarget` used to add the suffix to
    every name, so saving that site as "stays disabled" wrote `app.conf.disabled.conf` beside it — a copy
    nginx reads. `confdSite` now targets an existing file of exactly the name, `ReadSiteSpec` keeps the
    `.conf` in a spec name when a file of the shorter name exists beside it (so `app` and `app.conf` are
    two sites, each saved to itself), and `DeleteSite` removes the file the listing names. A save that
    asks to enable such a file is refused: enabling it is renaming it.
  - **A site saved disabled is tested as it would be enabled, in a copy of the configuration**
    (`site_trial.go`). Without that `nginx -t` never read the file, so a disabled site was saved untested
    and failed on the day somebody enabled it. The live tree is never touched: `stageTrial` writes
    `<nginx dir>/.jd-trial-*.conf`, nginx.conf byte for byte except that its include of `sites-enabled`
    (or of `conf.d` for a conf.d site) points at `.jd-trial-*.d`, which links every entry of the real
    directory plus the site under the name enabling it would give it (`<name>`, or `<file>.conf` in
    conf.d), so nginx sorts it where enabling would put it. `nginx -t -c` and, for a conflict's order,
    `nginx -T -c` run on the copy; the copy sits beside nginx.conf so relative includes resolve the same,
    and its paths are renamed back to nginx.conf and the real directory in the result. Linking the site
    into the live `sites-enabled` for the length of the test, as the save first did, raced everything that
    runs nginx without the service lock — `POST /proxy/reload` and `/proxy/test`, the vhost toggle, a site
    delete, a stream save, certbot's hook, systemctl: a reload in the window served the disabled site until
    the next reload, and a broken one failed unrelated reloads with 422
    (`TestLiveReloadsBesideADisabledSaveNeverReadIt` runs reloads beside disabled saves, and
    `TestADisabledSaveNeverPutsTheSiteInTheLiveConfiguration` records what the live directory held during
    every nginx the save ran). Nothing the test finds refuses the save — nginx does not read the file —
    and the result is what enabling it would meet (`testedAsEnabled`): `validation` with nginx's objection
    placed at its file and line, `conflicts` worded as what enabling would do, and `testWarnings`. A reload
    follows only a test that passed. The copy assumes nginx loads `<nginx dir>/nginx.conf`, as the
    htpasswd and stream checks do. It is untested, with `validation.note` saying why, when its name's link
    is another file's, when nginx.conf does not include the directory itself or its pattern would not read
    the name, or when nginx.conf cannot be copied.
  - **What still races an outside reload.** An enabling save, an edit to an enabled site, `Validate` and
    the config editor's write put the file (and an enabling save its link) in the live tree for the length
    of their test and take it back if the test fails: nginx has nothing else to test a file it reads. The
    reload and test endpoints do not take the service lock, so one landing in that window reads the
    candidate — refused over a broken one, or loading one the save is about to take back until the next
    reload.
  - **A name another server block also claims is refused when the save changes who answers it**, 409
    `name_conflict` with `ConflictSummary`'s sentences. `nginx -t` passes a second claim on a name with only
    `[warn] conflicting server name … ignored` and answers from the **first** block it reads, so the save
    could as easily take a working site's domain as leave its own unreachable. The conflicts are nginx's own
    warnings for this site's names on the addresses its file listens on — nginx already knows a wildcard
    `:80` and `127.0.0.1:80` are separate. `orderConflicts` then reads which claim wins from `nginx -T`
    (`dumpNginx` under the held lock, or the trial's dump for a disabled site, then `NginxTree`, so
    `sites-enabled/*` sorted, a `conf.d` include before or after it and a block inline in `nginx.conf` all
    fall where nginx reads them),
    names the other claim's site from that tree, and sets `effect`: `ignored` (the other keeps the name:
    "nginx answers app.example.com on 0.0.0.0:80 from legacy, which it reads first, and ignores this site's
    claim."), `takes` (this site sorts first and takes it: "Saving anyway takes … from legacy, since nginx
    reads this site first.") or `keeps` (this site already answered the name before the save — read from the
    file as nginx loaded it — and still does). A `keeps` conflict alone is **not** refused: an edit to the
    site that is serving a name was refused in the name of the site nginx ignores. When the order cannot be
    read the conflict has no `effect` and says only that both claim the name. The file and link are taken
    back on a refusal; `allowConflict: true` saves anyway and the result lists `conflicts`, which the toast
    words by `effect` (checked against a running nginx in `TestLiveNameConflictSaysWhichSiteNginxAnswers`).
    `ValidateSpec` refuses a domain listed twice, which nginx warns about the same way. Deployment cutovers
    (`applySiteLocked`) are not refused over a conflict.
  - **A reload that fails after a clean test is a saved site**, 200 with `reloaded: false` and
    `reloadError`, where it was a 400 "Not applied" over a file that was written and linked. The toast
    says nginx did not pick it up and to start or reload it, never what nginx is serving: the usual cause
    is an nginx that is not running (`open() "/run/nginx.pid" failed`), which serves nothing. The
    deployment cutovers still get the error, since their recovery is built on it.
  - **A reload is proven from the master's side** (`reload_proof.go`). `nginx -s reload` returning 0
    says only that the signal was delivered; the master then reads the files and either opens what they
    listen on and replaces every worker, or logs `[emerg]` and keeps serving its old configuration. The
    case that matters is a listen address another process holds — `nginx -t` passes it, because its test
    ignores `EADDRINUSE` — where the master logs `bind() … failed (98: Address already in use)` five
    times, half a second apart, then `still could not bind()` (checked against nginx 1.26.3). Every
    reload a save, a switch, a link removal, a bulk change or the engine's own **Reload** asks for now
    goes through `reloadProven`: `markLoad` notes the running master (`markReload`: found by the
    configuration it was started on, as the stream watch finds it), its workers, the end of its error
    log and the SHA-256 of each file the change wrote (the site's file and its `sites-enabled` link);
    `awaitLoad` then watches for `loadWait` (3 s). `LoadProof.state` is `loaded` when every worker
    serving is one that was not there before (`replacedAll`: one new worker beside old ones is a
    crashed worker replaced on the same load, not a load), each file still holds the digest the change
    wrote (`held`), and nginx holds every socket the site's `listen` lines ask for (`siteBinds`, read
    as the stream watch reads them; `listening`); `refused` with nginx's first `[emerg]` line (`error`)
    when the master logged one after the signal — except that a `bind()` failure is a refusal only
    once the master logs `still could not bind()`: the watch reads its attempts on until it does
    (`awaitBindAttempts`, so its later attempts are never read as the next reload's), and a socket
    freed between attempts is bound on the next and the reload read as any other load, `unconfirmed`
    with the first failure in its note when neither is seen; `unconfirmed` with a `note` when none of
    that was seen in time, a file was written again before the load, or nginx
    loaded without the site's socket; `unchecked` when the running nginx could not be read. A save
    whose reload the master gave up **over one of the site's own sockets** is put back exactly as a
    refused test puts it back (file, link and `.bak`) and answered 409 `load_refused` with
    `SiteLoadRefusedError` naming the program holding the port from the host's listener list (audited
    `result: rolled-back`): left in place, the file would fail every later reload on the host. Any
    other refusal keeps the valid file and is `reloaded: false` with `reloadError` ("nginx did not take
    the reload up: …"). The engine's reload answers a refused load as 502 `load_refused`
    (`ErrLoadRefused`, audited `result: refused`) and carries `loadProof` otherwise; every reload
    toast reads it (`reloadToast`: the command palette, the served-certificate and TLS report reloads,
    the import outcome), so none says "nginx reloaded" of a load the master was not seen taking up. The site form says
    "is live" only for `loaded` (or a backend that sends no proof), "saved and reloaded" with the note
    otherwise, and "nginx refused the reload" with nginx's words (`load-proof.ts`, `site-save.ts`); the
    engine's Reload toast and the pending strip read the same. `TestSaveSiteKeepsASiteNginxBoundOnALaterAttempt`
    keeps a site whose port was freed between the master's attempts. `TestLiveReloadIsProvenFromTheMaster`
    runs the host's nginx binary on a private prefix: a loaded save is proven from the real master's
    workers and sockets, a save onto a port the test holds is refused by the master, put back, named as
    held by the test process, and the previous site still answers.
  - `testWarnings` are the test's warnings placed in the site's own file, so "is live" is said only when
    nginx had nothing to say about it: "Saved with 2 warnings" lists them by line, and alongside a
    conflict they are listed after it rather than dropped. A conflict warning names no file, so it is
    never among them.

  Renderer details:
  - The ACME challenge location goes **above** the catch-all redirect, or renewal silently stops and
    nobody finds out for sixty days.
  - `http2 on;` is a directive, not a `listen` parameter (nginx 1.25 warns on every reload).
  - **`proxy_pass` is the upstream as typed, less a path that is the location's own.** Its path is how
    nginx is told to replace the location's prefix: `/api/` to `http://127.0.0.1:4000/` sends `/api/users`
    as `/users`. Trimming its slash sent `/page` to `http://…/app/` as `/apppage`. When the path and the
    upstream disagree about a trailing slash, `SpecWarnings` says what a request becomes. nginx forwards
    the request exactly as sent **only to an upstream without a path**; to one with a path it sends the
    path decoded, so `%2F` arrives as `/` (npm/Verdaccio scoped packages, GitLab project paths). An
    upstream path equal to the location's prefix — `http://x/` pasted on `/`, `http://x/pkg/` on `/pkg/`
    — swaps nothing, so it is left off and the request goes through raw; any other path is kept and
    `SpecWarnings` says `/a%2Fb` reaches the application decoded (both checked against a running nginx).
    A `unix:` upstream is written `http://unix:…`, the only spelling nginx accepts, and read back as
    `unix:`.
  - **A folder is served at its path**: `alias <folder>/;` in `location <path>/`, both ending in a slash
    so a neighbour such as `/assets-private` is never read through `/assets` (checked against a running
    nginx). `root` appended the path, so `/assets` with `/var/www/assets` looked in
    `/var/www/assets/assets`. A location read back from a file that used `root` keeps it
    (`SiteLocation.RootMode = "root"`), since rewriting it as the folder itself would move every file it
    serves; a hand-written `alias` is read back as a folder.
  - **A static site can be a single-page app** (`SiteSpec.SPA`): its `location /` answers a path with no
    file of its own with `/index.html` (`try_files $uri $uri/ /index.html`) instead of 404, so a deep link
    or a reload reaches the app's router; the probe blocks still refuse `/.env` (checked against a running
    nginx). It is read back from that `try_files` and only for a static site.
  - **Compression off is written `gzip off;`**: Debian's `nginx.conf` turns gzip on for the whole http
    block, and a site saying nothing inherits it. A managed file with no gzip line predates this and was
    written with the switch off; a hand-written one reads back as on.
  - **A site file owns its http-level objects** (`renderHTTPBlock`, above the first `server`).
    `sites-enabled/*` and `conf.d/*.conf` are both included *inside* `http {}`, so the top of a site
    file is http context: `map`, `upstream`, `limit_req_zone`, `limit_conn_zone`, `proxy_cache_path`,
    `geo` and `log_format` written there pass `nginx -t` (checked on nginx 1.26.3) and are deleted with
    the site. Their names are global, so each is `NginxIdent(site)` plus a suffix: a second
    `log_format` of one name fails the reload, and a `map` variable defined twice is decided by the
    last file read. `ParseSiteSpec` steps over these statements (and whatever is inside their blocks)
    rather than reading a map entry or a geo range as a server directive.
  - **WebSockets use the standard upgrade map**, per site: `map $http_upgrade $jd_<id>_connection
    { default upgrade; '' close; }`, with `Connection $jd_<id>_connection` in each upgrading location.
    It replaces passing the client's `$http_connection` through, whose comment wrongly said a map had
    to be shared by every site. Both forms read back as WebSockets on (the `Upgrade` header is what
    the parser keys on), and the map is written only when a rendered proxy location upgrades.
  - **Access logs are timed by default** (`SiteSpec.logFormat`: `timed`, the default when empty, or
    `combined`). Timed is `log_format jd_<id>_timed` — combined followed by `rt=$request_time
    urt="$upstream_response_time" host=$host cs=$upstream_cache_status` — so a stock combined reader
    still reads every line and `accesslog.parseCombined` takes latency from `rt=`. An `access_log`
    naming any `jd_*_timed` format reads back as timed; a file with none, or with another format,
    reads back as `combined`, which the drift list already reports as the form writing its own
    `access_log`. The form offers the choice under Hardening beside the access-log switch.
  - **An `allow` list is fenced with `deny all`.** nginx stops at the first match and otherwise permits,
    so a site restricted to `10.0.0.0/8` was reachable from anywhere; expecting the operator to write the
    fence themselves into a box labelled "Deny" fails too, since `0.0.0.0/0` lets in every IPv6 client.
    Explicit denials render *above* the allows for the same first-match reason. One case cannot be fenced
    and is said rather than rendered anyway: nginx answers a `return` in the rewrite phase, before access,
    so an allow list does not restrict a redirect site.

  **`ParseSiteSpec` must round-trip every field the form writes**, or an edit deletes what it cannot read
  back — `Custom` and `BlockExploits` were the two that did not, so opening a site and pressing save
  removed the operator's directives and turned the probe blocks off. `customMarker` and
  `exploitDotLocation` are shared with the renderer and
  `TestCustomConfigurationSurvivesARoundTrip` renders/parses/re-renders; that test is what a new field
  needs. It skips the generated ACME location (reading it back emits it twice), reports whether the file
  carries our marker so the UI can say a hand-written file may not survive, and leaves an `allow`/`deny`
  inside a location where it is — hoisting it into the site-wide list applied one path's restriction to
  the whole site on the next save. A save over a file without the marker first keeps it as
  `<file>.bak`, which the form's notice promised while nothing wrote it. The save then returns that
  path as `SiteResult.backup`, and the toast names it. A later save of the now-managed file leaves that
  `.bak` alone. A save that is refused puts back the `.bak` that was there before, or none
  (`TestSaveSiteKeepsAHandWrittenFileAsBak`). New backups take the original file's permissions;
  replacing an existing backup cannot make its permissions broader (`TestSiteBackupsKeepRestrictedPermissions`).

  **What a save drops is listed before it happens** (`site_drift.go`). A save writes the form's reading
  of the file, so a statement the form has no field for — a `proxy_set_header` added by hand, a second
  `listen`, a location it cannot express, certbot's `include` — went with no word, from a managed file as
  much as from a hand-written one. `DroppedLines` parses the file and what saving a spec writes with
  `ParseNginxFile` and compares them statement by statement where they sit: each server block of the
  file against the written block it shares a name and the most statements with (several may share one:
  a site written as a `:80` block and a `:443` block is one block listening on both in the form, and
  loses nothing), each block against its twin by name and arguments, and the statements in it as a
  multiset. Spacing, quoting and comments do not count, and the spellings the form writes differently
  with the same effect are the same statement: `listen 443 ssl http2` (it writes `http2 on;`),
  `listen *:80`, a `proxy_pass` whose path is the location's own (see the renderer below), a folder
  `alias` and its location without their trailing slashes, and a `server_name` split over several lines.
  What is left is every statement not written back, with its line and its text — its own lines of the
  file, comment and all, or the statement written out on one line where it shares one — and where it
  sits. `SiteDrift` is the form's view of that: the statements the form's own reading of the file leaves
  out, less those the current spec writes back, so an edit made in the form is a change rather than a
  dropped line and a line moved into the extra configuration stops being one. `GET /proxy/sites/{name}`
  carries it for the file as read (`dropped`, `lossless`), and every preview for the spec it is given
  when that site's file exists; both are left out when the form cannot write the file at all, which its
  preview then says.

  A dropped line is **movable** when it sits directly in the server block the form writes and the extra
  configuration, which goes there, keeps it as it is. Not one inside a location (a `proxy_set_header` in
  the server block is not inherited by a location that sets its own), in another server block or outside
  them all, and not one of a name the form writes there itself: nginx refuses most of those twice
  (`http2`, `ssl_session_timeout`, `ssl_session_tickets`, `ssl_prefer_server_ciphers`, `gzip`,
  `gzip_vary`, `client_max_body_size`, `root` and the `auth_basic` pair, checked against nginx 1.26.3 as
  `refusedTwice`) and applies the rest twice — a second access log, a second `X-Frame-Options`.
  `add_header` is compared by header and `listen` by address. An `include` is read for the names of the
  directives it sets (`includedNames`: relative to the nginx directory, a glob without its dotfiles,
  regular files under 1 MiB, and only the names leave, only as far as the reason), since certbot's
  `options-ssl-nginx.conf` sets `ssl_session_timeout`, which the form writes, and moving it fails
  `nginx -t` with "is duplicate" (`TestLiveAnIncludeThatRepeatsTheFormsTLSStaysOut`); its `reason` says
  so. The same statement movable from two places moves once. `TestLiveMovedLinesKeepWhatTheyDid` has a
  real nginx answer on a second port with a header added by hand, moves both into the extra
  configuration, saves and reloads, and checks both still hold — and that the `proxy_set_header` it could
  not move is no longer sent, as it said.

  **A draft is of one version of the file.** `GET /proxy/sites/{name}` and every preview of an existing
  file carry `digest`, the file's sha256 (`ContentDigest`). The form keeps it with the draft, and the
  save sends it back as `baseDigest`: `SaveSite` refuses a file that is no longer that version, or is
  gone, with 409 `site_changed` (audited as `changed_on_disk`) before it writes anything, so another
  tab's edit or the raw editor's is never silently written over
  (`TestSaveRefusesAFileThatChangedSinceTheFormReadIt`, `TestSiteSpecSaysWhatASaveDropsAndWhichVersionItRead`).
  Deployment cutovers send none and are not checked.

  **`POST /proxy/sites/preflight {spec}` (system.admin) makes the checks `nginx -t` cannot**
  (`proxysvc/site_preflight.go`), answering `{checks:[{id,level,title,detail,blocking,domain?}]}`:
  certificate and key exist, parse and match (`keyMatchesCertificate`), the certificate covers every
  domain (`certificateCoversAll`) and its days left; each password file exists, has users and is
  readable by nginx's worker group (`nginxWorkerGID`, mode/owner from the host); a static root and
  location folders exist, with an `index.html` where one is expected; each of the spec's own upstreams
  accepts a TCP connection and answers a HEAD within 2s (never any other target, since it is outbound
  traffic the caller aims); and DNS for every domain (`CheckDomainDNS`). Existence and mode come from
  one `stat -L -c` argv run through `hostexec.CommandOnHost`, because `/var/www` is not shared with the
  container; content (certificate, key, htpasswd) is read in the container and never returned. Each
  input is held to its own ValidateSpec pattern and skipped if it fails it, so a half-filled form still
  gets answers. Only files that break the site as saved are `blocking` (missing/mismatched/expired
  certificate or key, uncovered domain, missing/empty/unreadable-by-root-owned password file, missing
  folder, SPA without index.html); a closed upstream and DNS elsewhere are warnings, since a site is
  often saved before its app or its record exists. The form runs it 800ms after typing stops, shows it
  in a Checks tab and as per-domain DNS lines under Domains, and holds Save while a blocking check fails
  until the operator allows it; a new blocking check holds it again.

  **`POST /proxy/sites/preview` also says where the file goes**: `path` is what a save writes
  (`SiteFile`, through the same `siteTarget` the save uses: `sites-available/<name>`, or
  `conf.d/<name>.conf` on a conf.d host) and `exists` whether a file is there. Both are left out when the
  host has neither directory. A new site's name follows its domain, so it can land on an existing site
  without the operator typing it; the form refuses that at the field before the save refuses it.
  `enabledElsewhere` is the other way a name is taken: a `sites-enabled/<name>` that enables a different
  file (a hand-written site linked under its domain rather than its file name, or a link to a file that is
  gone) or is a file of its own, said as a sentence (`enabledElsewhere()`, which follows relative and
  chained links, so a link naming this very file, even before it is written, is not in the way). The save
  refuses a new site there, and an edit asked to enable itself there, before `linkEnabled` runs: it used to
  replace the link before `nginx -t`, so the test never saw the two sites claim one name and the other
  site silently stopped being served. An edit that keeps its link state leaves such a link alone and
  reports itself not enabled (`TestSaveSiteLeavesAnotherSitesLinkAlone`,
  `TestSiteFileSaysWhatHoldsTheNamesLink`). `DeleteSite` had the same bug: it removed
  `sites-enabled/<name>` wherever it pointed. Now it removes the link only when the link names the
  file being deleted, or points at nothing (a dangling link enables nothing and fails the next reload).
  It deletes the file and leaves any other link where it is. A file of its own in sites-enabled makes
  deletion fail before changing either file: nginx serves that copy and deletion cannot silently remove
  it without a backup (`TestDeleteSiteRefusesToRemoveServedCopy`). A name held only by another site's
  link is "no such site", and the error says what holds the name
  (`TestDeleteSiteLeavesAnotherSitesLinkAlone`). Every other sites-enabled entry that resolves to the
  deleted file goes with it (`linksTo`, skipping dotfiles as nginx's include does). Before, a site
  linked as `010-app` lost only the `sites-enabled/<name>` it never had, and the dangling `010-app`
  failed every later `nginx -t` and reload (`TestDeleteSiteRemovesALinkOfAnotherName`, and the real
  nginx run in `TestLiveDeletingASiteLinkedUnderAnotherNameLeavesNginxValid`).

  The form's side of this lives in pure modules beside `site-form.tsx`, tested with bun.
  `site-draft.ts` holds the draft's rules. A draft survives a trip to another page while its file is
  still the version it started from — the server's copy used to replace it on every open — gives way to
  the file when nothing in it was changed, and otherwise the form asks, with **Reload from disk** or
  **Keep my draft**, as it does after a save refused with `site_changed` (saving waits for the answer).
  Unsaved changes are the draft against the spec it started from, compared as sent: they draw an
  "unsaved" tag, and closing the sheet with them — Escape, the overlay, the close button, one funnel —
  asks **Discard changes?**; a trip to another page keeps them instead. Ctrl/Cmd+S runs the footer's own
  command. The dropped lines are drawn as a diff at their own line numbers, with the reason beneath for
  a line of the server block that cannot move, **Move N lines into Extra configuration** and **Edit the
  raw file** (the config editor over the form, at the first dropped line; saving there reads the file
  again). A hand-written file the form reads whole says so and that it is kept as `<file>.bak`. A
  **Changes** tab diffs the file on disk against the preview, which is what the save writes over it.

  `site-identity.ts` works a new site's file name and certificate paths out of its whole first domain on
  every change, for each part not typed by hand (keeping the first value named a site typed as
  app.example.com "a", after one keystroke), and checks a typed name against the server's rule; the form
  shows the name as its own **File name** field (read-only for an existing site), with **Match the
  domain** and **Use the domain's certificate** to follow the domain again. `site-save.ts` builds the
  request — `keep` for an existing site unless the operator chose **Save and enable**, HSTS only with
  TLS, since the switch is drawn only under HTTPS
  and its hidden default warned on every plain-HTTP site, and `spa` and `permanent` only with the kind
  that draws them — and turns the result into what the toast says, a disabled site's included ("saved;
  enabling it would fail nginx's test" with the line, "saved with a name conflict once enabled"). A
  refused conflict is a Notice in the form with **Save anyway**, which repeats the save that was refused,
  enabling included. A new form opens with the keyboard in **Domains**, and the presets come
  after the file name, so the one field every site needs is in view on a phone.

  A new site can **start from a preset** (`site-presets.ts`: Node.js app, single-page app, Grafana, Home
  Assistant, Docker registry, MinIO, Jellyfin, redirect), a `SiteSpec` partial laid over the form. It
  keeps the operator's name, domains, certificate, access settings and any upstream they typed, and
  swaps only its own lines in the extra configuration (the registry and MinIO turn
  `proxy_request_buffering` off there and lift the upload limit with `client_max_body_size 0`). A
  preset's card stays chosen, with its note about the application's own settings, until the operator
  picks another kind by hand: `leavePreset` then takes out the preset's extra lines and puts back the
  default for each field it set that still holds its value. Each preset is checked in as `proxysvc/testdata/presets/<id>.json`, which `site-presets.test.js` holds equal
  to the TypeScript; `TestPresetsRenderAndReadBackAsThemselves` renders every one plain and over HTTPS
  and reads it back unchanged, `TestLivePresetsPassNginxTest` puts all of them through the host's
  `nginx -t` with no warning, and two more live tests prove the single-page fallback and a 64 MB upload
  through the large-upload presets (which the Node.js preset refuses with 413).

  **Send it to** is an `UpstreamPicker` (`upstream-picker.tsx`) over `upstream-options.ts`: free text,
  plus what `GET /ports` and `GET /docker/containers/?all=false` show — running containers by the port
  they publish (a port they do not publish is listed, cannot be picked, and says to publish it on
  127.0.0.1, since its bridge address changes with every recreate), then loopback and wildcard sockets;
  443 and 8443 are offered as `https://`.
  nginx's own sockets, UDP, sockets bound to one other interface and ports that answer something other
  than HTTP (SSH, mail, databases, Docker's API) are left out. A loopback upstream nothing listens on is
  warned about under the field, not refused: nginx answers 502 until the application starts. The blank
  form's own default (`127.0.0.1:3000`) is not warned about until somebody sets the upstream — by
  typing, picking, a preset or a link — or the site already had it. Both lists are polled while the form
  is open, so an application started half-way through appears.

  **`/proxy/sites?new=1&upstream=<url>&domain=<name>`** opens the form on a new site from elsewhere in
  the dashboard (`site-link.ts`, called from the Sites page). It is read once, taken off the address, and
  only for `system.admin` on a host with nginx; the values go in as the link spelled them, so the form and
  preview say what is wrong with either. `tests/browser/proxy-site-builder.spec.ts` checks each state
  against mocks from `tests/browser/fixtures/proxy/siteform.ts`.

  **Maintenance and error pages** (`site_pages.go`, form section "Pages & files",
  `page-editor.tsx`). `SiteSpec.Maintenance{On, RetryAfter, BypassFrom}`, `ErrorPages` (404, 502, 503,
  504) and `InterceptErrors` (proxy sites: `proxy_intercept_errors on`, so the application's own
  responses with those codes get the pages too). Pages are files at `<nginxDir>/jd-pages/<site>/<page>.html`
  (0644; the embedded `proxysvc/pages/*.html` are written for any page a save needs and the site lacks),
  served from exact `internal` locations `= /__jd/<page>.html` by `alias`, reached with `error_page` to
  that path — a path, not a named location, because nginx turns the request into a GET only on the
  way to a path, and a POST to a static page is 405. `PagesDir` is set by the service, never read from a
  request. Maintenance writes a `geo $jd_<id>_maint_ip` above the server (the bypass list) whenever it is
  configured, and while on a server-level `set`/`if` pair that returns 503 except for the ACME challenge
  and the maintenance page itself: a variable `set` on every pass rather than a `map`, whose value nginx
  keeps across the error page's internal redirect. The maintenance location carries `Retry-After` and
  repeats the server's security headers (its `add_header` stops them inheriting), and every page
  location turns `auth_basic` off. While maintenance holds 503 the site's own 503 page is not routed,
  but its location stays written, which is how the parser reads `ErrorPages` back. `VHost.maintenance`
  reports the switch on the Sites card. Routes: `GET /proxy/sites/{name}/pages/{page}` (any signed-in
  account; the default when the site has no file), `PUT` the same (`system.admin`, at most 256 KiB,
  no reload, audited `proxy.site.page`), and `POST /proxy/sites/{name}/maintenance`
  `{on, retryAfter?, bypassFrom?}` (destructive gate, like disabling; audited
  `proxy.site.maintenance`), which saves the form's reading of the file with the switch changed and
  reloads — refused (409 `not_managed`) for a file the form did not write or would drop lines of. Page
  paths are refused unless the site folder resolves directly under `jd-pages` inside the nginx directory
  and the file is not a link.

  **Limits** (`sites_limits.go`, form section "Limits", a per-path override under the extra paths).
  `SiteSpec.Limits{Request{Rate, Burst, NoDelay, Key}, ConnPerIP, ExemptFrom, DryRun}` and
  `SiteLocation.RateLimit`. Rate is `^[1-9]\d{0,5}r/[sm]$`, Key is empty (per address) or `ip_path`
  (`$binary_remote_addr$uri`), exemptions are IPs/CIDRs. Above the server: `geo $jd_<id>_limit_exempt`
  (listed addresses 1), and per zone a `map` of it to `$<zone>_key` (`""` for exempt, which nginx does
  not count) plus `limit_req_zone`/`limit_conn_zone` — zones `jd_<id>_req`, `jd_<id>_conn` and
  `jd_<id>_req_p<n>` for the n-th extra path. The zone key is always that map variable because nginx
  refuses a reload that finds a live zone counting by a different key. Server level: `limit_req`,
  `limit_req_status 429`, `limit_conn`, `limit_conn_status 429`, and with DryRun `limit_req_dry_run on`
  and `limit_conn_dry_run on` (logged as "dry run"); a path's own `limit_req` replaces the site's there,
  as nginx inherits it. Refused for redirect sites (`return` runs before limits apply) and exemptions or
  log-only with nothing limited. Every limit counts the address nginx sees, so a warning says so unless
  the extra configuration sets `real_ip_header`; the form cannot tell whether a CDN is in front.
  `VHost.rateLimited` puts "rate limited" on the Sites card. The private-prefix nginx test loads the
  rendered zones and confirms that a third request over a `1r/m` limit answers 429.

  **Caching** (`sites_cache.go`, form section "Caching", verb "Purge cache"). `SiteSpec.StaticCache{MaxAge,
  Immutable}` (not for redirects) renders `map $sent_http_content_type $jd_<id>_asset_expires` (CSS, JS,
  images, fonts, woff/wasm → MaxAge, default `off`) with server-level `expires` on it, so pages are never
  kept; Immutable adds a second map and `add_header Cache-Control $jd_<id>_asset_immutable` (empty, so
  unsent, for everything else). `SiteSpec.ProxyCache{MaxSize, Valid, ServeStale, CacheCookies}` (proxy
  sites only, refused unless `Buffering` is on — nginx never caches an unbuffered response; a path with
  buffering off is warned about as uncached) renders `proxy_cache_path /var/lib/just-dashboard/nginx-cache/
  <name> levels=1:2 keys_zone=jd_<id>_cache:10m max_size=… inactive=7d use_temp_path=off` and at server
  level `proxy_cache`, key `$scheme$host$request_uri` (so domains never share entries), `proxy_cache_valid
  200 301 302`, `proxy_cache_lock`, and with ServeStale `proxy_cache_use_stale error timeout updating
  http_5xx` + `proxy_cache_background_update`. `proxy_cache_bypass`/`proxy_no_cache` always carry
  `$http_authorization` (not a toggle: nginx would otherwise store one user's authorised page) and
  `$http_cookie` unless CacheCookies (which warns). `add_header X-Cache-Status $upstream_cache_status
  always` sits with the site's other headers (`writesHeaders`), which the maintenance page repeats.
  `SaveSite` makes the cache root (0755); nginx makes the site's folder at load and chowns it to its
  worker. `GET /proxy/sites/{name}/cache` (the read gate; size only) walks it for blocks on disk and
  files; `DELETE` (system.admin + destructive, audited `proxy.site.cache.purge`) empties it through an
  `os.Root` and keeps the folder, refusing a linked root or site folder. There is no cache_purge module:
  nginx takes a missing file as a MISS, but its shared-memory size accounting only catches up as the
  cache manager evicts. `VHost.cached` shows the verb. A real-nginx test confirms MISS then HIT,
  Authorization and Cookie bypasses, and a MISS after purge.

  **A site's controls as its service policy, and their measured effect** (`site_controls.go`).
  `SitePolicy` reads the site's file (by its listed name) for the request limit (`limit_req` with its
  zone's rate from the file or the loaded tree, burst, `nodelay`), the connection limit, the response
  cache (`proxy_cache`), browser caching of assets (`expires`), HTTP/2 (`http2 on` or a `listen … http2`)
  and HTTP/3 (`http3 on` or a `quic` listen), each with paths that set their own, and says per control
  whether the build nginx runs has what it needs (`nginx -V`: the standard limit, proxy modules unless
  configure left them out with `--without-`, now kept as `streamNginxBuild.without`;
  `--with-http_v2_module` and `--with-http_v3_module`): `built-in`, `module`, `missing` or `unknown`.
  `GET /proxy/sites/{name}/policy` serves it to every account (the site read already shows the file).
  The connection investigator reuses it as the **service policy** of each site that forwards to the
  destination port (`netpath/policy.go`), with the limitation that a direct connection to the port meets
  none of it. `POST /proxy/sites/{name}/controls/verify {path, asset}` (`system.admin`, audited
  `proxy.site.controls.verify` with the request count) measures them against this nginx on loopback —
  or, for a site whose listens on the port all name one address, at that address (`dialAddress`), so
  nginx answers from the block that address picks rather than a catch-all on the wildcard, and only
  where it is one of this host's own (`hostAddress`; otherwise nothing is sent) — naming the site in
  SNI and Host as the request tester does (`localTarget`: only an enabled nginx site
  that takes its first exact name on its first TLS port, else its first plain one, and refused when
  another enabled site wins that name on the port, whose controls would be measured instead): HTTP/2 from a
  browser's ALPN offer (`verified`, `not-effective` when the site asks for it and nginx does not
  negotiate it, or `not-configured` noting that another server block on the address turned it on);
  HTTP/3 from a QUIC Version Negotiation answer on the UDP port (`probeQUIC`) and an `h3` in the HTTPS
  answer's Alt-Svc; the response cache from two GETs of the path and `X-Cache-Status` (`HIT` second, or
  why not: a cookie, `Cache-Control` private/no-store/no-cache, a bypass, an uncacheable status;
  `not-measured` without the header); browser caching from the asset path's `Cache-Control` max-age
  (`not-measured` without one); and, last, since every other check's requests count against it, the
  request limit from burst + 2 requests at once, at most 40, counting the site's `limit_req_status`
  (a dry run is verified by refusing nothing; `not-measured` when the burst needs more than 40, when
  without `nodelay` nginx would hold the check over five seconds, or when the address the requests
  come from — loopback, or the site's own address — is on the site's exempt list). The connection limit is `not-measured`: `limit_conn` counts requests still being
  answered, which a short request does not hold. The requests reach the application. The site page's
  **Controls** panel (`site-controls-panel.tsx`, `site-controls.ts`) lists the policy and, for an
  administrator, measures it with a path and an optional asset path. Tests:
  `TestSitePolicyReadsTheFormsControlsAndTheBuild`, `TestVerifySiteControlsRefusesBeforeSending`,
  `TestControlHelpers`, `TestProxyLayerCarriesTheSitesServicePolicy`, and `TestLiveSiteControlsAreMeasured`,
  where the host's nginx binary on a private prefix serves a TLS site with HTTP/2, a QUIC listen
  advertised by Alt-Svc, a response cache and CSS caching, and a site limited to one request a minute
  with a burst of one, and every control is verified from nginx's answers. The site is written for the
  installed build: HTTP/2 goes in the listen where nginx predates `http2 on;` (1.25.1), and a build
  without `http_v3_module` gets no QUIC listen, so HTTP/3 must read as not configured there.

  **Visitor address** (`site_realip.go`, form "Visitor address"). `SiteSpec.RealIP{Source, Trusted,
  Header, CloudflareOnly}`: `cloudflare` renders `include <nginxDir>/jd-realip/cloudflare.conf;` +
  `real_ip_header CF-Connecting-IP` at server level; `proxies` renders `set_real_ip_from` per `Trusted`
  entry (IP/CIDR, a `/0` refused), `real_ip_header <Header>` (a header name) and, for X-Forwarded-For,
  `real_ip_recursive on`. It sits before the maintenance check, so address lists, limits, maintenance
  bypass and the log all see the visitor. `CloudflareOnly` (Cloudflare only, warns) adds an http-level
  `geo $realip_remote_addr $jd_<id>_edge {default 0; include …/cloudflare.geo;}` and `if ($jd_<id>_edge
  = 0) { return 444; }` in the site server and its redirect server. PROXY protocol is not offered: nginx
  ORs `proxy_protocol` across every server on one address:port, so one site would switch it on for all
  of :443. The two files are owned (`# source=cloudflare|built-in fetched=<RFC3339>` header); `SaveSite`
  writes the built-in list when a Cloudflare site needs them and they are missing. `realIPFiles` refuses
  a `jd-realip` that does not resolve directly under the nginx directory and a non-regular file.
  `GET /proxy/realip/cloudflare` (any signed-in account; public ranges) reads the list;
  `POST /proxy/realip/cloudflare/refresh` (system.admin, audited `proxy.realip.refresh`) downloads
  cloudflare.com/ips-v4 and ips-v6 (10s, 64 KiB each, every line a masked CIDR of its family, v4 ≥ /8,
  v6 ≥ /16), writes both files, runs `nginx -t` and restores both on failure (422), then reloads.

  **Headers** (`sites_headers.go`, form "Headers"). `SiteSpec.Headers{Request, Response, Hide,
  FrameOptions, CSP, Permissions, CORS}`. Every value is rendered in double quotes and refuses `" ' \ ;
  { } $` and control characters; names match `^[A-Za-z0-9][A-Za-z0-9-]{0,63}$`. A request header value may
  instead be one whole `$var`; it is written into every forwarding location after the fixed
  `proxy_set_header` lines (a location's list replaces the server's) and read back from `location /`.
  Request and Hide are proxy-only; Hide refuses what nginx already drops (Server, Date, X-Accel-*). CSP
  and Permissions-Policy are kept as parts (keywords unquoted: `self`, `none`, `nonce-…`), so the stored
  scalars never carry `;` or quotes; the renderer adds them. Custom response headers may not name one a
  control writes (X-Frame-Options, CSP, Permissions-Policy, HSTS, `Access-Control-*`, and whatever the
  security-headers switch or caches send). All response headers go through `renderHeaders`, so the
  maintenance page's location repeats them. CORS (not on redirects): an http-level
  `map $http_origin $jd_<id>_cors` (exact origins → `$http_origin`, else empty, so no ACAO is sent) or a
  literal `*` (refused with credentials), a `map "$request_method:$http_access_control_request_method"
  $jd_<id>_preflight`, and a server-level `if ($jd_<id>_preflight) { return 204; }` — only real preflights
  are answered, before auth and limits; other OPTIONS reach the application. A proxy site hides the
  application's own `Access-Control-Allow-*` headers so browsers never see two.

  **Access options** (`sites_access.go`, form "Who may reach it", "Crawlers & hotlinking", "Pages &
  files"). `BasicAuthRealm` is now in the form. `SatisfyAny` writes `satisfy any;` and needs both an
  allow list and a password file; it is inherited by every location, so the exploit blocks then say
  `satisfy all;` (a password must not open them) and the internal page locations say `allow all;`.
  `AccessList` writes `include <nginxDir>/jd-access/<name>.conf;` (the access lists feature's
  contract; `SetAccessListDir` sets the folder, never a request) and is refused beside the site's own
  allow/deny/password/satisfy, since nginx refuses a second `auth_basic` and merges allow lists. A
  missing list fails `nginx -t`. `ClientCert{CAPath, Mode, PassSubject}` writes
  `ssl_client_certificate` + `ssl_verify_client on|optional` and is refused unless TLS with HTTP
  redirected (port 80 would serve without a certificate); `optional` needs `PassSubject`, which sets
  `X-Client-Verify`/`X-Client-Subject` in each forwarding location (proxy only; both names reserved from
  custom request headers). `BlockBots{AI, Scanners, Custom}` renders an http-level
  `map $http_user_agent $jd_<id>_bot` of `"~*<name>"` entries (custom names: letters, digits, space
  `. _ / -`, dots escaped) and a server-level `if` returning 403; presets are read back when every
  member is present. `Hotlink{Allow}` (not redirects) renders `map $uri $jd_<id>_hotlink_type` for
  media extensions and `valid_referers none blocked server_names …` with a `set`/`if` returning 403
  only for media with a foreign Referer. `SecurityTxt`/`RobotsTxt` (not redirects) serve
  `jd-pages/<site>/security.txt` and `robots.txt` at `= /.well-known/security.txt` and `= /robots.txt`
  (`default_type text/plain`), edited as pages `security` and `robots`; the security.txt default names
  `security@<first domain>` and expires in a year, and the form warns within 30 days of `Expires`.
  Private-prefix nginx tests confirm the crawler and hotlink refusals, same-site referrals, and
  single sign-on's 200, 401 redirect and 500 refusal before the application.

  **Single sign-on** (`sites_forward_auth.go`, form "Who may reach it" and each path's options).
  `SiteSpec.ForwardAuth{Provider, Verify, SignIn, PathsOnly}` writes `auth_request /__jd/auth;` at
  server level (or, with `PathsOnly`, in each location whose `SiteLocation.ForwardAuth` is `on`; a
  site-wide one is skipped where a location says `off`), `auth_request_set $jd_<id>_sso_user|email` from
  the provider's headers (Authelia/custom `Remote-User`/`Remote-Email`, Authentik `X-authentik-*`,
  oauth2-proxy `X-Auth-Request-*`), `error_page 401 =302 <signIn>?rd=$scheme://$http_host$request_uri`
  and an internal `location = /__jd/auth` that proxies to `Verify` without the body, with
  `X-Original-URL`/`X-Original-Method`/`X-Forwarded-*` (and `proxy_cache off` under the proxy cache).
  Only 2xx passes; a 403, 5xx or unreachable server refuses the request (auth_request's own rule). Each
  forwarding location sets `Remote-User`/`Remote-Email` from those variables, so a visitor's own are
  replaced (empty where no check ran). The page and ACME locations say `auth_request off`. Refused on
  a redirect, beside a site or path password, access list or `satisfy any` (a password prompt's 401 would become
  the sign-in redirect; satisfy any would bypass it), with `InterceptErrors` (the application's own
  401s would redirect), and with a custom `Remote-User`/`Remote-Email` request header. The provider
  round-trips through the `# Single sign-on through <provider>.` comment. Session cookies the auth
  server sets on the check response are not relayed, so the sign-in portal must set its own.

  **HSTS and TLS versions** (`sites_tls.go`, form "Encryption"). `HSTSMaxAge` (0 = 15552000, else
  300..63072000), `HSTSOwnNameOnly` (drops `includeSubDomains`) and `HSTSPreload` (refused below a year
  or without subdomains; the form asks for the first domain typed) build the one
  `Strict-Transport-Security` value; `readHSTS` reads it back, six months as 0, so older files save
  unchanged. `TLSProfile` is empty (`ssl_protocols TLSv1.2 TLSv1.3`) or `modern` (`TLSv1.3` only, read
  back from that exact value, with a SpecWarnings line). There is deliberately no cipher/curve profile:
  nginx takes `ssl_ciphers` and `ssl_ecdh_curve` from the socket's default server before SNI, so in a
  named site they do nothing; protocol options do follow SNI. HTTP/3 is not offered: it needs one
  `listen 443 quic reuseport` owner, and the default site does not claim QUIC.

  **Site kinds** (`sites_kinds.go`, form "What it serves"). A redirect's `RedirectCode` is 301, 302,
  307 or 308; 0 falls back to `Permanent` (301/302), and the parser writes back only 307/308 into it so
  pre-existing files round-trip unchanged. `RedirectDropPath` writes `return <code> <to>;` without
  `$request_uri`. `Canonical` (`www`/`apex`, any kind) adds, per non-wildcard domain, a server for its
  other form marked `# Canonical host (<mode>): …` that the parser skips whole; it listens on 80 (and
  443 with the site's certificate when TLS), keeps the ACME path, and 301s to the site's name; a name
  that is already a domain is refused, and a TLS site gets a warning that the certificate must cover
  the other names. `HostRedirects` (`from`, `to`, `code`, at most 16) name redirecting servers outright
  rather than deriving them: each `from` is a name the site does not serve, sent to one it does, with
  308 when `code` is 0, in a server marked `# Host redirect (<code>): <from> is sent to <to>.` that
  keeps the ACME path. The marker carries the whole redirect, so the parser skips the server and still
  reads it back. Deployment routes use it for a plan's redirecting domains. Static sites gain `Autoindex` (warned), `GzipStatic` and `IndexFiles` (also PHP;
  empty = the kind's order, a default read back as empty). Kind `php` needs `Root` and `PHPSocket`
  and renders `location ~ \.php$ { try_files $uri =404; include fastcgi_params; fastcgi_param
  SCRIPT_FILENAME …; fastcgi_pass unix:<socket>; }` plus `location ~ /\.ht { deny all; }`, with
  `PHPFrontController` ending `location /` in `/index.php?$query_string`. fastcgi_params is used
  always rather than Debian's `snippets/fastcgi-php.conf`: the render is pure (it cannot look at the
  host) and the snippet only adds PATH_INFO. Preflight checks a PHP site's root and socket.
  `TestLivePHPSiteRoutesThroughFPM` uses a cached PHP-FPM Docker image and a private nginx prefix to
  verify scripts, front-controller paths, missing scripts and blocked Apache files; it skips without
  the cached image.
  `GET /proxy/php-sockets` (system.admin; runs `find /run/php -maxdepth 1 -type s` on the host via
  hostexec argv) lists sockets for the form.

  **Path routing** (`sites.go` `validLocation`/`validUpstreamTLS`, `sites_render.go` `renderLocation`/
  `renderUpstreamTLS`, form "Paths that go somewhere else" + `site-routes.ts` preview table).
  `SiteLocation.Match` is empty (prefix), `=`, `^~`, `~` or `~*`; `locationPathRe` refuses `; ' " $ ( )`
  in a non-regex path and a regex refuses whitespace, `; { } " ' #`. A regex path may not forward to an
  upstream with a path (nginx refuses it); a folder at an exact/regex path is written with `root`
  (`RootMode root`). `StripPrefix` (prefix paths only) ends both the path and the upstream in `/` and is
  read back from the `# Strips the path before forwarding` comment; otherwise `proxy_pass` is written as
  typed. Folders render `alias <root>/;` and `SPA` falls back to `<path>index.html`. Per path:
  `BodyLimit`, `Timeout`, `Buffering`/`RequestBuffering` (`on`/`off`, empty = the site's — the parser
  folds a value equal to the site's back to empty), `BasicAuthFile`, `AllowFrom`/`DenyFrom` (a path's list
  replaces the site's, as nginx inherits it; a warning says so when both exist). Site level: `Buffering`
  (default off, as before), `StreamUploads` (`proxy_request_buffering off`; named so its zero value is
  nginx's default), `HostHeader` (`$host`, `upstream` = `$proxy_host`, `custom` = `HostHeaderValue`), and
  at server level `proxy_ssl_server_name on` (`UpstreamSNI`), `proxy_ssl_name` (`UpstreamTLSName`, only
  with SNI or verify), `proxy_ssl_verify on` + `proxy_ssl_verify_depth 3` + `proxy_ssl_trusted_certificate`
  (`UpstreamCA`, else `/etc/ssl/certs/ca-certificates.crt`). Password-file paths are absolute-path
  checked like the site's; the route stays system.admin.
- **`sites_pool.go` — several servers.** `SiteSpec.Pool` (`Method` empty/`least_conn`/`ip_hash`/`hash`
  = `hash $request_uri consistent`/`random`, `Scheme` empty = http or `https`, `Servers[]` with `Weight`,
  `MaxFails`, `FailTimeout` seconds, `Backup`, `Down`, zero = nginx's default; `Keepalive`, `RetryOn`
  = `proxy_next_upstream` conditions or `["off"]`, `Tries`) replaces `Upstream` (both set is refused) and
  renders `upstream jd_<ident>_pool { … }` above the servers, which the catch-all forwards to with its
  `proxy_next_upstream`/`_tries`. Keepalive clears `Connection` (`proxy_set_header Connection ""`, or the
  site's upgrade map answers `""` instead of `close`). Refused: backup with `ip_hash`/`hash`/`random`
  (nginx refuses it), a pool of only backups, a scheme or path in a server address (host:port,
  `[v6]:port` or `unix:/path`), Host "upstream's" (`$proxy_host` would be the block name) and an HTTPS
  pool with SNI/verify but no `UpstreamTLSName`. The parser reads any upstream block the catch-all's
  `proxy_pass` names back as the pool, so a Duplicate gets the new name's block. Health checking is
  passive only (`max_fails`/`fail_timeout`); stock nginx has no active checks and the form says so. A
  private-prefix nginx test confirms a request uses the primary and then its backup after the primary
  stops.
- **`tlsscan.go` — what the domain actually serves.** Everything else on the page reads files, which
  cannot see a certificate renewed and never reloaded, a proxy still offering TLS 1.0, or a redirect that
  quietly stopped. Each version is probed on a connection pinned to exactly that version, offering
  every cipher suite Go implements (`probeSuites`: Go leaves RSA key exchange, 3DES and RC4 out by
  default, and a server taking TLS 1.0 only with AES128-SHA read as refusing it). Only the server's own
  answer is `refused` — an alert (recognised by its `remote error` type: OpenSSL's protocol_version
  alert reads "protocol version not supported", and matching those words once filed every correct
  refusal as this client's), a different version picked, or the connection closed on the ClientHello —
  with its words in `detail`. handshake_failure and insufficient_security are the exception and read
  `unknown`: OpenSSL sends them when the version is fine and no suite is shared (a suite Go lacks, such
  as finite-field DHE), Java 8 when it refuses a version, and the alert cannot say which. A version this
  client will not ask for, a probe that could not connect, or one that timed out is `unknown`, **never
  `refused`**, since reporting it absent would be false reassurance about the versions that matter most;
  `TestLiveNginxRefusalsAreReportedAsRefused` and
  `TestLiveNginxLegacyVersionsWithOnlyRSAKeyExchangeAreOffered` hold this against a private real nginx.
  The report's own handshake (`handshake`) offers what a current client does, since that is what it
  calls negotiated. A server answering it with an alert is asked again offering every version and
  suite (`tlsOffer` with TLS 1.0–1.3), then once more as a current client so a passing fault is not
  reported as policy: taken only by the full offer, the scan is reachable and `legacyOnly`, graded F with
  `tls.legacy-only` naming what was negotiated, and its HTTPS request makes the full offer too (a
  server taking TLS 1.2 only with RSA key exchange was reported as nothing answering, with DNS advice).
  Refused by both, it is `tls.refused` with the alert — no certificate for the name
  (`ssl_reject_handshake`, Caddy), a client certificate wanted, or a suite Go lacks;
  `TestLiveNginxWithOnlyRSAKeyExchangeIsReachable` and `TestLiveNginxRejectedHandshakeIsARefusal`.
  **A scan that never completes a handshake says how far it got** (`failure`, `tlsscan_diagnosis.go`):
  `classifyDialError` names the stage — `dns` (no record, or the lookup failed), `connect` (refused,
  timed out, no route) or `handshake` (the server's alert, `plain-http` when the ClientHello is answered
  with `HTTP/` — nginx's `listen 443;` without `ssl` — `not-tls` with the first bytes another protocol
  sent, closed, or no answer). `dialTLS` connects and then handshakes as two steps so a handshake failure
  carries the address it reached (`handshakeError`). `where` says whose answer it was: `here` (loopback or
  an address on this machine's interfaces, private ones included), `cloudflare`, `elsewhere` only when
  this server has a public address of its own in that family, or `unknown` when the provider maps it
  in front of it — a dual-stack VM with its IPv6 on the interface cannot tell its mapped IPv4 from
  another host's (`familyKnown`). Every connect and handshake finding's advice follows it
  (`hereOrThere`): `tcp.refused` here is "nothing here listens on 443", elsewhere it is that host's
  refusal; a handshake with Cloudflare is its edge's, so plain HTTP on 8080 names its plain-HTTP and
  HTTPS ports rather than nginx's `listen … ssl`; plain HTTP on port 80, from any host, is HTTP's own
  port answering as it should (`plainHTTPPort`), never told to add `ssl`, which would break http://,
  the redirect and HTTP-01, and is sent to 443; and an address scanned as itself is never told to
  point a record. `dns` carries `CheckDomainDNS`. `TestLiveNginxListenWithoutSSLIsDiagnosed` holds the plain-HTTP
  case against a real nginx. **Only an HTTP answer is graded on HTTP** (`service`): a port registered to
  a protocol that speaks TLS from its first byte and is not HTTP (`implicitTLSServices`: 465, 993, 995,
  636, 853, 8883 and the rest) is sent no web request at all (`service: other`, `serviceName`); a
  service that answers in its own protocol is `other` with its first line (`banner`, read off the
  connection by `firstBytes`: net/http's error quotes only a fragment of a line that is not HTTP, and
  nothing of a greeting sent on connect before the request, as SMTP and IMAP send theirs); a request
  with no answer is `unknown` with `httpsError` and the `http.https-error` notice. In none of them is an
  HSTS, header or redirect finding made, or port 80 asked. Otherwise the plain-HTTP side is followed by
  hand up to five hops (`redirectChain`), passes when it reaches `https://` on any host, and says where
  it ended (`redirectVerdict`: `same-host`, `other-host`, `stays-http`, `loop`, `too-many`, `dead-end`,
  `internal`); `plainErrorKind` says whether port 80 refused, timed out or did not resolve. The HTTPS
  answer's own redirect is `location`. Every hop after the first goes where the remote site's
  `Location` says, so its dial (checked on the resolved address, in a `net.Dialer` `Control` hook) may
  reach only the address the first request reached or a public one (`hopAllowed`, `IsPublicAddress`):
  a redirect to loopback, a private or link-local network or CGNAT is recorded as an `internal` hop and
  not requested, and graded as a notice (`http.redirect-internal`) rather than a missing redirect,
  because where the chain ends is not known. The serial is colon hex as openssl prints it, and the
  unstapled-OCSP notice needs a responder in the leaf (`ocspServers`; Let's Encrypt names none);
  `crlUrls` and `spkiPin` (base64 SHA-256 of the public key, what `curl --pinnedpubkey` takes) are
  reported beside them. **Chain and revocation** (`tlsscan_chain.go`, `revocation.go`): each `chain`
  link carries its validity, serial, SHA-256, signature algorithm, names, extended key usage and
  `issuedByNext`; `trustPath` is the path `x509.Verify` built to a root in the system store (without
  the name, so a mismatch does not hide it), each step marked sent or supplied by the store.
  `chainAudit` feeds grade rules: `tls.no-server-auth` (EKU set without serverAuth, F), `tls.sha1`
  (a non-root link signed with SHA-1, F), `tls.must-staple` (TLS Feature status_request with no
  staple, F), and for a chain trusted here only, `tls.long-validity` (over 398 days, issued since
  2020-09-01, B) and `tls.no-sct` (no SCT in the certificate, the TLS extension or the stapled OCSP
  answer, B) — a private root in the system store is indistinguishable from a public one, and the
  findings say so; `tls.chain-order` and `tls.root-sent` are notices. `revocation` reads the stapled
  OCSP answer, then each responder (`x/crypto/ocsp`, POST), then each CRL (at most 20 MB,
  `CheckSignatureFrom` the issuer, current by NextUpdate); an answer is believed only when signed by
  the issuer or its delegated responder. Those URLs are the certificate issuer's to write, so the
  fetch dials public addresses only (checked after DNS in a `Control` hook, like redirect hops),
  follows no redirects and fetches `http`/`https` only. Good and revoked answers are cached in memory
  per issuer key and serial until their NextUpdate. A revoked leaf is `tls.revoked` (F); an answer
  that could not be had is the `tls.revocation-unchecked` notice when the leaf names a source.
  **Expiry is judged against the certificate's term** (`tlsscan_lifetime.go`):
  renewal is due in the last third of it, the last half for a term of ten days or less — certbot's rule
  since 4.0, and Caddy's — and never more than 30 days out. `summarise` (`certs.go`) sets `expiring`
  by it, so the certificate inventory, the watch list and the report agree; a 6-day certificate is no
  longer expiring for its whole life. Not everything reads it yet: certbot's lineage cards and an import's warning still count 30 days (`certbot-panel.tsx`,
  `import.go`), and the Certificates page's expiring reading counts by the window under a 30-day label
  (`certs-panel.tsx`). The report notes a certificate inside that window (`tls.renewal-due`) and grades B once
  half of it has passed with the certificate still served (`tls.expiring`); `lifetimeHours` and
  `renewalWindowHours` are in hours because a short-lived term is 160 of them. **HSTS preload** is
  measured for a name on 443 that answered HTTP (`preload`, `tlsscan_preload.go`) against
  hstspreload.org's rules, each with what was seen: a registrable domain by the Public Suffix List (a
  subdomain gets only that rule, naming the parent to scan), a trusted certificate, a first plain-HTTP
  redirect to HTTPS on the same host (a refused port 80 passes), an HTTPS redirect that stays on HTTPS,
  `max-age` of a year, `includeSubDomains`, `preload`, and `www.` serving a trusted certificate when it
  has a record — that handshake runs beside the probes. Six months stays the A+ threshold, as SSL
  Labs'; the list asks for a year, which the report now says. `TestLiveNginxPreloadRules` checks the
  rules against a real nginx. `grade` is a pure function of the scan. The live certificate, TLS and DNS probes require
  `system.admin`: each emits traffic to a caller-chosen destination, the same scanner boundary as
  `/network/probe`. What reaches them is `ParseScanTarget` (`scantarget.go`): a URL, host:port, a
  bracketed IPv6 address or a name in its own script become a host and port, and anything else is a 400
  with the reason, quoting the host as typed rather than the whole pasted URL;
  `frontend/src/lib/scan-target.ts` is the same parser, and both are tested against
  `frontend/src/lib/scan-target-cases.json`, whose `says` holds the two to the same words. A port,
  written after the host or given as `?port=` text (`ParseScanQuery`), must be 1–65535 in digits, so
  `0`, `+993` or `host:+993` is a 400 rather than 443 or 993.
  A scan runs on its request's context, so a caller that leaves (the report's Cancel) ends its
  handshakes and probes at once.
- **The deep scan** (`GET /certificates/scan/deep?domain=&port=`, `system.admin`, handler in
  `handlers_tls_deep.go`) is its own request, which the report makes after the quick scan when `?deep=1`
  is in its address. Go's TLS client offers only the suites Go implements and in TLS 1.3 all of them
  whatever it is told, so the suites are asked with a ClientHello of the scan's own (`tlshello.go`): it
  reads the ServerHello's version, suite and TLS 1.3 group (and a HelloRetryRequest's), and for TLS 1.2
  the ServerKeyExchange's curve or DH prime size, then hangs up; no key is ever derived. A hello between
  256 and 511 bytes is padded to 512 as browsers do (F5), SSL 3.0 carries the renegotiation SCSV and no
  extensions, and signature_algorithms goes only in TLS 1.2 and 1.3 hellos. Each of SSL 3.0 to TLS 1.3 is
  listed by elimination (`tlsciphers.go`): offer the whole catalogue (every ECDHE/DHE/RSA/static-ECDH
  suite with AES, ChaCha20, CCM, ARIA, Camellia, SEED, IDEA, 3DES, RC4 or DES, and the export,
  anonymous and NULL ones; PSK, SRP and GOST need a secret or certificate no scan has), note the pick,
  offer the rest, until a refusal — about one connection per accepted suite — then offer the accepted
  ones reversed to say whose `order` it is. A version stopped short by a probe that heard nothing says
  so (`complete: false`). Suites rate `insecure` (NULL, export, DES, RC4, anonymous), `weak` (no forward
  secrecy, CBC, a 64-bit block, CCM_8) or `strong`. TLS 1.3 groups are asked one at a time — real key
  shares for X25519, P-256/384/521 and the three ML-KEM hybrids (X25519MLKEM768 puts the ML-KEM key
  first), none for X448 and ffdhe, which a server answers with a retry — and then as a browser offers
  them, whose pick is `browserGroup`; without TLS 1.3 that is TLS 1.2's curve for a browser's offer.
  TLS 1.2 curves are not listed one by one: there the offered curves also bound an ECDSA certificate's,
  so one at a time refuses the certificate, not the curve. Every probe goes to the one address the first
  handshake reached, eight at a time, five seconds each, under a 45-second budget (`tlsdeep.go`). That
  first handshake is a browser's (ALPN h2/http/1.1, a session cache): its protocol is held against the
  site form's HTTP/2 switch for the enabled nginx site that claims the name with a TLS listen on the
  scanned port (`SiteForName`: exact, then the longest leading wildcard, then trailing; a name no site
  claims is not compared), only when the connection reached this server. Resumption offers its session
  back (TLS 1.3 tickets arrive after the handshake, so it reads for up to a second, asking a website for
  its headers), plus a TLS 1.2 pair; a TLS 1.2 server that issues no ticket is reported untested, since
  Go cannot resume by session ID. Handshakes naming no site and a `.invalid` name show what a scanner
  gets; a certificate there is `tls.sni.default-certificate`, whose fix is a default server with
  `ssl_reject_handshake on`. Alt-Svc is read from an HTTPS GET, and QUIC asked on the advertised UDP port
  of the same host (or, unadvertised, the scanned port) with one padded packet in a reserved version,
  which a QUIC server must answer with Version Negotiation (`tlsquic.go`); another host's alternative is
  never sent anything. `deepFindings` is pure; version findings reuse the quick scan's ids so the page
  can drop the ones the quick report already lists, and advice for an answer from another machine says
  so. `TestLiveDeepScanOfNginx`, `TestLiveNginxRejectingUnknownNamesLeaksNothing`,
  `TestLiveNginxWithOnlyRSAKeyExchangeHasNoForwardSecrecy` and `TestLiveNginxSmallDHGroup` hold it
  against a private real nginx; Go servers cover ML-KEM, TLS 1.3 suites, ALPN and resumption.
- **A scan with a certificate is traced to the nginx site behind it** (`origin`, `tlsorigin.go`,
  added by `handleTLSScan`): `TraceOrigin` picks among enabled nginx sites listening on the port by
  nginx's order (exact `server_name`, longest leading then trailing wildcard, first regex RE2 can
  compile, then the `default_server` or else the first site by name) at file granularity, finds the
  site's `ssl_certificate` through the inventory's `usedBy`, and compares fingerprints: `current`,
  `stale` (the file is a later certificate for the same names — a renewal nginx has not reloaded),
  `other` (another certificate on this host answered), `foreign` (none on this host did), `unserved`
  or `unknown` (no `ssl_certificate` in the site's own file, or unreadable). It is nil for an
  address, a name Caddy claims, or a host without nginx sites. `stale` is a heuristic: a second
  machine serving an older copy for the same names reads the same way.
  **Every scan says which address it reached** (`address`, and `addressKind` from `whereConnected`:
  `here`, `cloudflare`, `elsewhere`, `unknown`). `?connect=<ip>` (`ScanOptions.ConnectTo`, read by
  `ParseConnectTo`: an IP only, for a name only, a written port must be the scan's) dials that address
  for the handshakes, the HTTPS request and the plain-HTTP chain's requests to the name, with SNI and
  Host still the name's — curl `--resolve` — so the origin behind a CDN or a server before cutover can
  be graded; the scan is a GET, which `AuditMutations` skips, so the handler records it itself
  (`httpx.AuditRead`, action `certificates.scan.connect`). `?all=1` (not with `connect`) handshakes
  with each A and AAAA record beside the scan, eight at a time (`tlsscan_addresses.go`, resolver
  injectable), returns them as `addresses`, and raises the warning `tls.address-mismatch` when
  reachable addresses serve different leaf certificates; it caps no grade, since the grade is of the
  answer this scan got.
- **STARTTLS services are scanned through their own dialogue** (`starttls.go`). `?proto=` is `auto`
  (the default: SMTP on 25 and 587, IMAP 143, POP3 110, FTP 21, PostgreSQL 5432, TLS from the first
  byte elsewhere), `tls`, `smtp`, `imap`, `pop3`, `ftp` or `postgres` (`ParseStartTLS`, anything else a
  400). `dialTLS` holds the dialogue on every connection of the scan — the handshake, the four version
  probes and each `?all=1` address — before its ClientHello, bounded by the handshake's deadline, the
  request's context, 4 KiB a line and 100 lines a reply; EHLO names `localhost`. It is strict: an answer
  the protocol does not define ends it, and so does any byte buffered after the server agreed (the
  STARTTLS injection class). The scan carries `starttls`, sends no web request (`http.service` is
  `other`) and skips the preload check; a dialogue that ends early is a failed handshake with reason
  `starttls-refused`, `starttls-unexpected`, `starttls-closed`, `starttls-timeout` or
  `starttls-error` and what the server said in `answer`, and a version probe it stops is `unknown`.
  `CheckDomain` and `CheckEndpoint` use Auto; a watched endpoint has no per-endpoint protocol.
- **The HTTPS request can be shaped, and its answer is audited.** `?method=` (GET, HEAD or OPTIONS
  only: a scan is a read; POST belongs to the audited request tester), `?path=` and `?host=` go
  through `proxysvc.ParseRequestShape` and change only the HTTPS request — SNI stays the scanned
  name, and the plain-HTTP chain still asks `/` with the name as Host. A custom Host is recorded
  with `httpx.AuditRead`, action `certificates.scan.host`. `http.responseHeaders` lists every header
  of that answer (150 at most, values capped at 2 KiB); Set-Cookie values are replaced with
  `<redacted>` inside the scan and the raw headers are never kept or stored. `httpaudit.go` holds
  the pure rules over those headers — versioned `Server` (with `fix: "server-tokens"`, which the
  page answers by locating `server_tokens` through `/proxy/tools/directive`), `X-Powered-By` and
  kin, cookies without Secure/HttpOnly/SameSite, CSP `'unsafe-inline'` without nonce or hash,
  `'unsafe-eval'`, wildcard script sources, a policy with no script limit or only Report-Only,
  uncompressed text on GET (the request sends `Accept-Encoding: gzip, br` itself so net/http does
  not strip `Content-Encoding`), and `Access-Control-Allow-Origin: *` with credentials. They add
  findings and cap no grade.
- **The watch list is endpoints.** `watched_endpoints` (lane G in `proxySchema`) is a name, a port and
  an address, unique together, so a mail server can be watched on 443 and 993; `watched_domains` held
  one row per name and a second port replaced the first. Its rows are copied in on every boot with
  `INSERT OR IGNORE`, which brings nothing back because an unwatch deletes the `watched_domains` row as
  well. Each endpoint keeps its last check (`checked_at`, `certificate` as JSON) and may name an `ip` to
  reach the name at (an origin behind a CDN): the handshake then goes there and still asks for, and is
  verified against, the name (`CheckEndpointAt`). `GET /certificates/watched` only reads; no visit
  handshakes. The server checks on its own schedule: `proxysvc.TLSMonitor` (`tlsmonitor.go`, started in
  `startProxyExtras`, stopped in `stopProxyExtras`) looks every minute for endpoints whose last check is
  older than the interval (setting `certificates.watch.interval`, default 300 s, 60–86400 s; read by any
  account at `GET /certificates/watch-schedule`, set by `system.admin` at `PUT`), and
  `POST /certificates/watched/check` (`system.admin`, audited) checks all now under a 30-second budget and
  answers with the list. Either way eight run at a time, stalest first, through `CheckEndpoint`, whose dial
  ends with the context and holds the STARTTLS dialogue Auto picks by port, so a watched mail server on
  587 is read as a scan reads it; one still in flight or not started when the budget ends keeps its stored result
  and time. One pass at a time: a caller waiting for a scheduled pass waits only as long as its own budget.
  Every check also goes into `watched_checks` (days left, fingerprint, error; 2000 per endpoint and 90
  days, cascading with the endpoint), read at `GET /certificates/watched/{id}/history`.
  **Network probes are watched endpoints of their own kind**, not a second monitor:
  `watched_endpoints.kind` is `tls` (a handshake and its certificate, every row before this) or `tcp`
  (`POST /certificates/watched {domain, port, ip?, kind: "tcp"}`), checked by the same `TLSMonitor` pass
  on the same interval through `CheckTCP` — one connection, at the pinned address where there is one,
  nothing sent, closed — kept as `ProbeCheck` (`ok`, `state`: connected, refused, timeout, unresolvable,
  error; the address dialled; connect time) in `watched_endpoints.probe`, and in `watched_checks` with
  its error and `ms`. The two tables moved from `proxySchema` into `schema` because they gained columns
  after shipping (`kind`, `probe`, `ms`, brought to older installs by `addedColumns`; every existing row
  reads `tls`). A probe where a TLS watch already exists is 409 `already_watched` (the handshake checks
  the connection too); a TLS watch where a probe exists takes it over, drops the probe's connect times
  from its history in the same transaction, and is checked on the next pass,
  and a probe result still in flight then is not saved onto the row (`… AND kind = 'tcp'`).
  The `watch_unreachable` alert judges a probe by whether it connected ("No TCP connection (refused):
  …"); the untrusted and grade rules and the fleet scan leave probes out. The list (`watched-domains.tsx`)
  draws a probe with its connection and connect-time trend and no TLS report; the Network Tools page's
  TCP port check offers **Watch on a schedule**, and the Network runs page lists **Watched probes**
  (`watched-probes.tsx`, `lib/watched-probes.ts`) with check-now and stop. Tests:
  `TestWatchedEndpointsGainProbeColumns`, `TestTheWatchMonitorProbesNetworkEndpoints`,
  `TestCheckTCPConnectsOrSaysWhyNot`, `TestNetworkProbesAreWatchedEndpoints` (a real loopback listener
  watched, checked, its history, the unreachable alert after it closes, and the kind rules).
- **TLS report history.** Every quick scan that runs to its end (not one its caller cancelled) is stored in
  `tls_scans` (grade, days left, fingerprint, reachability and the report as JSON; 100 per target and 180
  days). `GET /certificates/reports?domain=&port=` lists a target's reports newest first and
  `GET /certificates/reports/{id}` returns one with `changes` against the report before it
  (`proxysvc.DiffScans`, `tlsdiff.go`: reachability, grade, certificate replaced, trust, key, negotiated
  and per-protocol status, HSTS, each security header and the port-80 redirect). Both only read, so every
  account may; `DELETE /certificates/reports?domain=&port=` is destructive and audited. Deep scans are not
  stored, and a successful scan records no connected address, so an address change is not diffed.
- **`dnsdeep.go` — the TLS page's DNS panel** (`GET /certificates/dns?deep=1`, `system.admin` like the
  rest of the probes). `Query` asks one resolver one question on the wire (`x/net/dns/dnsmessage`, UDP
  with EDNS 1232, TCP when the answer is truncated), because Go's resolver hides TTLs and cannot ask
  for CAA. The report reads A/AAAA with TTLs, each tagged this-server, cloudflare, other, or unknown
  when the host has no public address; the CNAME chain; the CAA walk of RFC 8659 from the name up to
  the TLD, judged for `letsencrypt.org` (issue, issuewild falling back to issue, `;` naming nobody, a
  critical unknown tag forbidding everyone, a failed lookup refusing issuance as Let's Encrypt does);
  `_acme-challenge` TXT/CNAME; and A+AAAA from the first `resolv.conf` nameserver beside each compared
  resolver, each marked against the first that answered. Records come from that same first resolver.
  The comparison list is the `proxy.dns.resolvers` setting (IP literals only, at most six, empty
  restores 1.1.1.1, 8.8.8.8, 9.9.9.9), written by `PUT /certificates/dns/resolvers` (`system.admin`,
  audited as `certificates.dns.resolvers`). A name only: an address is refused with 400.
- **Scan every site** (`tlsfleet.go`, `handlers_tls_fleet.go`). `proxysvc.FleetTargets` (pure) is each
  enabled TLS site's names on each port its nginx `listen … ssl` directives name (443 when none does; a
  Caddyfile address unless it says `http://` or is a bare port) plus every watched endpoint, once per
  host and port; wildcards, regex names and `_` are left out, and a watched endpoint pinned to an address
  is scanned by name. `POST /certificates/scan-all` (`system.admin`, audited, 409 while one runs, 400 with
  nothing to scan) starts job `tls.scan-all`: `ScanFleet` runs the quick report four at a time, a minute
  each, streams `[n/total] host:port grade — summary`, and stores each finished report in `tls_scans`;
  cancelling the job starts nothing more and keeps nothing cut short. `GET /certificates/scans/latest`
  (any account; it only reads) is the fleet with each target's latest stored report (grade, days left,
  expiry flags, negotiated version, count of critical and warning findings) and the running job, if any.
- **`probe.go` — the request tester** (`POST /proxy/tools/request {url, method, headers, connectTo}`,
  `system.admin` with the rest of `/proxy/tools`, audited as `proxy.tools.request` with the method and
  URL). It dials only `127.0.0.1` or `::1` on the URL's port, whatever DNS says, and puts the name in
  SNI and Host; the URL's host must be a name an enabled nginx site listening on that port takes by
  `serverFor` (the same order `TraceOrigin` uses), so an address, another engine's name or a name no
  site claims is a 400 before anything is sent. Methods are a closed set, there is no request body, and
  headers are tokens with one-line values, never Host, Content-Length, Transfer-Encoding or Connection.
  Each hop reports status, protocol (HTTP/2 when nginx offers h2), headers, the first 64 KiB of the body
  (none if not UTF-8 text), connect, TLS, first-byte and total times from `httptrace`, and the leaf
  certificate with its trust verdict and `TraceOrigin` against the site's file. Redirects are followed
  by hand, at most five, each Location re-checked as the first URL was: one that names no local site
  ends the chain as `elsewhere`, so the tester never reaches anything but this nginx. Authorization and
  Cookie are dropped when a redirect changes host.
- **`tlsscan_fixes.go` — remedies for TLS findings.** Each `ScanFinding` carries a `fix` hint
  (`renew`, `issue`, `force-https`, `hsts`, `security-headers`, `fullchain`, `protocols`); it is a hint
  only, and every remedy runs through an endpoint that already exists (site preview and apply,
  `/certificates/renew`, `/proxy/config`, test-then-reload). The one new route is
  `GET /proxy/tools/directive?name=` (`system.admin` with the rest of `/proxy/tools`, read-only, not
  audited): the name must match `^[a-z_][a-z0-9_]{0,63}$`, and the answer is every place it is set in
  the tree `NginxTree` builds from `EffectiveConfig` — path, line, arguments, block context and the
  enclosing server's `server_name`s. Files `EffectiveConfig` leaves out (outside the proxy directory,
  such as certbot's `options-ssl-nginx.conf`, or password files) are not searched.
- **`dns01.go` — wildcards and CDN-fronted domains**, which between them are most of the certificates
  people want: Let's Encrypt signs `*.example.com` only against DNS-01, and a Cloudflare-proxied domain
  never receives an HTTP challenge. Eight certbot plugins as a closed set (each names credentials and
  propagation after itself; route53 has neither), credentials 0600 inside certbot's tree. A wildcard over
  HTTP is refused here with what to do instead, rather than relaying certbot's accurate and useless
  "wildcard domains are not supported by the HTTP-01 challenge".
  Route 53 credentials are normalized to an AWS `[default]` profile and atomically saved at
  `/etc/letsencrypt/jd-dns/route53.ini`. Previously saved bare key/value files are validated and
  atomically normalized on the next dashboard certbot operation; invalid files fail without falling back
  to a different AWS identity. Dashboard issuance and renewal set
  `AWS_SHARED_CREDENTIALS_FILE` to this path and `AWS_PROFILE=default`, clearing competing inherited
  AWS credentials; values never appear in command arguments or job logs. If host certbot timers or
  cron jobs renew these certificates, their environment must set the same two variables. Saving a
  dashboard credential does not reconfigure an external timer; an IAM-role-only installation can leave
  the credential form empty.
  Site-validation rollback restores a replaced `sites-enabled` symlink to its exact previous target.
- **Raw configuration edits carry a content revision** (`config_checked.go`). `GET /proxy/config`
  returns `content` and its SHA-256 `revision` under the proxy writer lock, so it cannot expose
  another operator's transient nginx validation candidate. `PUT /proxy/config` requires that
  `expectedRevision` (400 when missing); `WriteConfigChecked` compares the original bytes under the
  same lock before any staging or native validation. A stale or removed file answers 409
  `config_conflict`, audited as a refused conflict, without replacing it. Caddy validates a private
  copy and checks the revision again immediately before atomic publication. nginx stages the checked
  candidate once, runs its native validator and verifies the candidate is still on disk: a native
  writer that replaced it is preserved and causes a conflict, rather than being overwritten by a
  rollback. Invalid or interrupted candidates are restored only while their own bytes still occupy
  the file. Successful responses, including `reload_failed` after a completed save, carry the saved
  revision. Internal generated writes and history restore retain their existing `WriteConfig` owner.
  The shared raw editor keeps its dirty buffer and original revision in RAM per engine/path across
  SPA navigation and Back/Forward. Conflict UI pauses saving, preserves the draft, shows a diff
  against the current file and requires an explicit review against that version before retrying.
  Discard rereads a changed file; a removed/unreadable file retains the draft with a retry action.
  Raw Test config uses the same conflict display if a native writer changes the candidate during
  validation. The config editor's nginx dry runs restore only their own candidate's bytes, including
  prior absence, and refuse to erase a newer native edit. Raw reads, writes and restoration pin the approved parent
  directory through `os.Root`; temporary contents, FD-based permissions, publication and cleanup stay
  on that captured directory. A renamed, replaced or outwardly retargeted parent causes a conflict;
  a candidate still held by the original directory is restored there while replacement directories
  and their files stay untouched. `files.Resolve` rejects outward symlink ancestors even when the
  requested suffix does not exist. Raw file reads are nonblocking and require a regular file, so a
  native FIFO replacement cannot hold the writer lock forever. The native check-to-publish interval
  cannot atomically compare bytes against independent host writers; descriptor confinement prevents
  that remaining interval from redirecting metadata, writes or cleanup into a replacement ancestor.
  Its unload warning remains active even while the editor is closed. Explicit discard and account
  working-state cleanup clear the buffer; no config text is written to Web Storage. Service/API
  regressions cover both nginx and Caddy, simultaneous stale writers, native edits during validation,
  refused-candidate restoration and missing preconditions; `proxy-engine-overview.spec.ts` covers
  retained history drafts and conflict review.
- **The editor's read route is not a password reader.** `ReadConfig` refuses the dashboard's own
  `jd-auth` directory and any `.ht*`/`htpasswd` file with `ErrProtectedFile` (403 `protected_file`):
  `GET /proxy/config` is held by every signed-in account and a list of bcrypt hashes is not
  configuration. The protected-directory comparison uses its absolute and resolved path, so
  relative configured roots and internal `jd-auth` directory aliases retain the same refusal. A file inside the proxy's directories that is not on disk (removed or renamed
  after the list naming it was read) is a 404 `not_found` marked retryable, which the editor shows
  as an error with Try again; a missing path outside them is still 403 `outside_root`. `Validate` now carries a `Note` when the file is outside nginx's include tree —
  no `sites-enabled` link, a `conf.d` file without `.conf`, a stream with no include — because
  `nginx -t` passes a file it never reads, and a dry run that says "valid" about a disabled site is
  a false reassurance. `POST /proxy/test` runs the engine's own test against what is on disk without
  staging or reloading anything (admin, audited as `proxy.config.test`); the overview's Test config
  and Reload buttons post it and `/proxy/reload` with the engine's kind, and a kind the service does
  not know is a 400 rather than nginx. A reload the test refuses is a 422 whose body carries the test
  beside the error, as a refused start's does (below). For the shared Docker Caddy that kind is `caddy-ingress`
  (`docker_caddy_engine.go`): `caddy validate` and `caddy reload` run inside the running container
  against its own `/etc/caddy/Caddyfile`, never the host's caddy, and with no ingress running both
  answer 409 `no_ingress`. The editor's `POST /proxy/validate` and `PUT /proxy/config` take the same
  kinds (none named is nginx, an unknown one is a 400) and refuse `caddy-ingress` with a 400 before
  any file is touched: `WriteConfig` treats every kind but `caddy` as nginx, so the ingress kind wrote
  an nginx file and then reloaded the container, and the ingress's Caddyfile is the deployments' to
  write. `Availability` names an ingress only once it runs (`ingressState:
  "running"`); one the first deployment would start is `ingressState: "provisionable"` with neither
  `caddy` nor `ingressContainer` set, and deploy preflight counts either state as a proxy that can
  serve and certify the domain. A running ingress also carries `ingressId` and `ingressStartedAt` (Docker's
  `State.StartedAt`), so the overview reads "running as a container, up 3d" and offers Restart
  container (`POST /docker/containers/{id}/restart`, destructive, audited as
  `docker.container.restart`) and Container logs (the container page's logs tab) through the Docker
  routes and their own gates; the proxy routes add no container control of their own. Start, restart, stop, enable at boot and clearing a failed state go
  through `POST /proxy/engine/{start|restart|stop|enable|reset-failed}`, which resolves the unit
  itself (`Service.Engine`: `nginx.service` where nginx is installed, else `caddy.service`; 409
  `no_engine_unit` otherwise) and runs start and restart through `WithTestedConfig` — the config
  test under the service lock, then systemctl only if it passed. This host's `nginx.service` runs
  `nginx -t` before it starts, so a restart over a broken file used to stop nginx and leave every
  site down; now it is a 422 whose body is `{error: {code: "invalid_config", message}, validation}`
  — the message is the sentence plus nginx's output, and `validation` is the test itself with its
  parsed `diagnostics`, which the overview places at their file and line — and systemctl never runs.
  Stop, enable and reset-failed start nothing and run no test, so an engine whose file is broken can
  still be set to start at boot or have a hand-fixed failure cleared. A systemctl that refuses (a
  masked unit, a start that fails) is a 502 `command_failed` in systemd's words. Start, enable and
  reset-failed need `system.admin`; restart and stop are destructive as well; each is audited as
  `proxy.engine.<action>`, a refusal with `result: refused` and the 422. There is no disable: taking
  the proxy out of the boot sequence is the Services page's. The unit's state, `UnitFileState`,
  `Result` and `NRestarts` are still read from `/systemd/{unit}`, and a failed unit's last lines from
  `/systemd/{unit}/journal?lines=30`, so the Services page and the overview never disagree about it.
- **`ParseSiteSpec` reads what hand-written files actually look like.** A line holding a whole
  block — `location / { proxy_pass http://x; }` — is split into statements before it is read
  (`splitInline`, quote-aware, since a Content-Security-Policy value carries semicolons of its
  own); the line reader used to take the opener and drop everything after the brace, so the site
  came back with no upstream. `listen` is read as nginx reads it — the address's port and an `ssl`
  parameter — where `4430` and `127.0.0.1:8443` used to count as TLS, and the pre-1.25
  `listen 443 ssl http2` reads back as HTTP/2 on. A file with no `access_log` directive is logging
  to nginx's default, so an unmanaged file round-trips with logging on rather than the first save
  writing `access_log off;`. Only a server-level `access_log`/`error_log` is the site's —
  `access_log off` under `/favicon.ico` silences one path, and read as the site's it told the form a
  site logging to nginx's shared file kept no log at all — and the first that names a file is `SiteSpec.AccessLogPath`
  / `ErrorLogPath` (`logFile`: `off`, `syslog:`, `stderr`, `memory:`, anything under `/dev/`, a path
  with a variable and a relative one are not a file this can read). There is no fallback to the managed
  names: nginx writes nothing there once the directive is gone. `VHost` carries both, read the same way
  in the listing, so a list of sites says which keep a request record of their own without a read per
  site; the renderer and every reader spell the managed paths through `nginxAccessLogPath` /
  `nginxErrorLogPath`.
- **A site's requests are read the way a deployment's are** (`site_access_log.go`,
  `handlers_proxy_site_requests.go`). `GET /proxy/sites/{name}/requests`, `/requests/stream`
  (WebSocket) and `/requests/export` (CSV) are reads, like the site itself, and share
  `requestFilterFrom`, the window, the cursor tail and the export with the deployment routes (one
  `followRequests`/`exportRequests` for both), over the one `accesslog.Store` — two stores would hold
  two caps, and the cap bounds what the whole process keeps. `SiteRequestRoute` resolves the record
  **from the site's file on every request**, so an edit that moves `access_log` — the form, the raw
  sheet or an editor over SSH — reads the new file from the next poll: an nginx site is the record
  `file:<its access log>`, refused unless `logs.Allow` accepts it; a `just-dashboard-*` site whose file
  logs where the renderer puts it, and a Docker Caddy route with no file on the host, are the
  deployment's own route, the record its Logs page already holds — the latter only while the ingress
  holds that route's file, since any reader can type a `just-dashboard-*` name and each would start a
  record and a lookup of its own. An export neutralises a cell a client wrote (`csvText`): an agent, a
  referer, a path or a host that starts with `=`, `+`, `-`, `@`, a tab or a return gets a leading `'`,
  so a spreadsheet reads it as text rather than running it. `SiteRecordReader` asks `logs.Allow`
  again before every open of the live file and of each rotated generation, since a generation beside
  the file can be a link to anywhere, and generations are the names logrotate gives (`logsx.Archives`:
  `.N` and dated), compressed ones skipped — for deployments' nginx files too. A site with no file, no
  access log of its own (off, syslog, or nginx's shared log, whose combined lines do not say which site
  answered) or one outside the roots answers `unavailable` with that sentence, and the stream and export
  refuse it with a 400. The name in the URL is unescaped, so a Caddy host with a colon works. A
  deployment's window carries `ingress` (the Caddy container) or `errorLog` (the nginx site's
  `.error.log`), where the proxy says why it failed a request. The rest of the proxy's logs are read on
  the pages they are about, through the `/logs` routes: a site's own page (its access and error files,
  nginx's shared error log narrowed to its names, the ingress container or `journal:caddy.service`),
  the overview's Engine log (nginx's two files and unit, a host Caddy's unit, and the Docker Caddy
  ingress once `GET /logs/source` finds its container) and Certificates' Renewals (`letsencrypt.log`
  and the renewal unit's journal, through the `certbot` lens).
- **`SetVHostEnabled` changes a link only if nginx still loads.** Enabling a site whose `sites-enabled`
  entry already existed but pointed elsewhere returned success and changed nothing; it goes through
  `linkEnabled` now, so the switch saying on means nginx reads the file. Enabling also used to make the
  link and return: a site nginx could not load was then in the include tree, the reload after it was
  refused, and every later reload — deployment cutovers included — was refused with it until the link
  was removed by hand. Now the link goes in, `nginx -t` runs, and a refusal undoes it (or restores the
  link it replaced, target text and all) and returns a `*RefusedError` carrying the test, which the
  switch answers as 422 `invalid_config` with nginx's first error, file and line
  (`FailureHeadline`) as the message and the test output as `raw`. A disable is tested the same way
  and put back when it breaks a configuration nginx was loading (another site used its upstream or
  zone); out of a configuration nginx was already refusing it stands, because switching sites off is
  how a broken configuration gets fixed. nginx's first error names a file other than the one being
  switched in three cases, and `RefusedError.Lead` says which, so `Reason()` — the 422's message and
  the audit entry's `reason` — does not read as that file's fault: a refused disable or link removal
  is "nginx refuses the configuration without <name>: …" (the error is in the site that needed it);
  a refused enable is tested again with the link undone, and when the same error is still the first
  one it is "nginx already refuses the configuration without <name>: …"; and when it is not, but the
  error is outside the site's own (resolved) file or names no file — a second `default_server` on
  the same address, an upstream name both sites define, which nginx reports in whichever of the two
  it reads second — it is "nginx refuses the configuration with <name>: …". An enable refused in its
  own file carries no lead. A reload that fails after a passing test is a 200 with
  `reloaded: false` and `reloadError` rather than a 502, since the link is in place and correct.
  A file sitting where a link belongs in `sites-enabled` is never removed as a "disable". A link
  under another name to the site's file (`00-default -> ../sites-available/default`) serves it as
  surely as its own: the disable takes every such link out (and puts them all back on a refusal),
  and an enable of a site served that way changes nothing rather than loading it twice.
  `RemoveVHostLink` (`DELETE /proxy/vhosts/{name}/link`, destructive, audited as `proxy.vhost.unlink`)
  takes out a link no site's switch owns — one to nothing, or to a file outside sites-available —
  under the same test-and-restore rule, and refuses a link to any sites-available site and any real
  file. The switch and the removal reload nginx through `ToggleVHost` and `RemoveVHostLink(…, reload)`
  inside the same hold of the service lock (`reloadLocked`): run after it, the reload's test could see
  another request's candidate link and report a change that had worked as "not reloaded". Both routes
  read `{name}` through `httpx.URLParam`, since chi hands back the escaped segment (`vb%3A8080`).
  `DeleteSite` refuses a regular copy at `sites-enabled/<name>`, keeps another site's link there,
  and removes every alias to the deleted site's file, so no dangling link blocks the next reload. The bulk
  deletion path still uses `checkSiteDelete` to refuse cases it cannot remove safely.
  `parseCaddyfile` tracks brace depth so only top-level blocks are site addresses — `handle`,
  `header` and `tls` blocks were listed as server names — and says whether every site address is
  served over HTTPS: an address with a host is unless it is `http://` or on port 80 (localhost and IP
  addresses included, which caddy:2 serves from its local authority), one with no host is not, and
  under `auto_https off` only a block with its own `tls` counts. Caddy's one-site form without braces
  is read (the first line is the address), an `http://` block whose only directives redirect to
  `https://` is not a plain site — as an nginx port-80 block that only redirects is not — and a block
  opened and closed on one line counts. An address list carried onto the next lines by trailing
  commas is joined first; read line by line, its first line began the one-site form and every later
  site was taken for one of its directives. One plain site makes the Caddyfile's single entry plain.
  Names are listed once, without their scheme.
- **What the running nginx has not loaded.** `Pending` (`pending.go`, `GET /proxy/pending`, a read
  every account holds) compares the files nginx reads with the load it is running. nginx replaces all
  of its workers each time it loads its configuration and keeps them when a reload fails in the master
  — a port another process holds is the common case, which `nginx -t` does not check — so the start of
  the oldest worker that is not "shutting down" is when the running configuration was read, and `nginx
  -s reload` exiting 0 says only that the signal went out. The master is found in `/proc` (the
  dashboard's container shares the host's processes): its title names its `-c` and `-p`, or it reads
  the compiled-in configuration `nginx -V` gives, and only a master reading the main file `nginx -T`
  names counts — the host's nginx, one in another container and the live harness's all show up
  beside each other. Two that read the same path are told apart by the `pid` file that main file (or
  the build) names, read under `/host` first. A start in `/proc` counts clock ticks since boot and is
  placed on the wall clock through `CLOCK_BOOTTIME`, not `/proc/stat`'s whole-second `btime`, and
  rounded up to the tick, because a file saved and reloaded by a script lands within a hundredth of a
  second of the new workers. Each file `EffectiveConfig` returns (so only what `ReadConfig` shows) is
  judged by its change time — through its link, and the link's own — not its modification time, so a
  file moved or copied in with an old time still counts. `pendingTracker` (per `Service`) remembers,
  for the running load, the SHA-256 of every file unchanged since it: a file whose content is back to
  what nginx loaded is not pending however new it is (a dry-run test stages its candidate at the live
  path and writes the original back; a switch refused and undone re-creates its link — each of
  these calls `keepLoaded` before it writes, which notes the digest of a file still unchanged since
  the load, so this holds even when nothing asked what is pending first), and a file
  nginx loaded that it no longer reads is "removed" — a site disabled without a reload is still
  served. A file saved since the load that the tracker never saw loaded is "changed", or "added" for a
  link made since; the tracker is in memory, so after the dashboard restarts, a file changed and
  changed back before it first read the load reads as changed until the next reload. Each file names
  the Sites entry it belongs to (`Site`, `Layout`: a link in sites-enabled belongs to the
  sites-available file it serves, a conf.d file to itself). `Generation` (`<master pid>-<ticks>`) names
  the load; `?after=<generation>` waits up to five seconds, reading `/proc` every 100 ms, for a newer
  one, which is how the Sites page reads nginx again after a reload it asked for (a save without a
  reload reads at once). Workers all replaced without a reload — a crash or an OOM kill — keep the
  load when the master's error log says each one that went was killed by a signal (`exited on
  signal`, at alert level) and the new ones started right after; with no error log file to read,
  or one in another time zone, they are taken for a load. The cached `nginx -T` is dropped before a
  read when a file it names, or a directory its glob includes read, changed since it was taken, so a
  link made over SSH shows at once. When `nginx -T` refuses the configuration, `Problem` is its first
  error and the files are found by following the includes on disk from the main file (as nginx
  globs them), so a site linked since the load is still "added"; when the dump waits more than five
  seconds behind the service lock, only the files known to be loaded are judged. `Running` is
  false, with `Reason`, where nginx is not installed, no master reads the configuration, or two do and
  the pid file names neither; a master with no worker is running with no `LastReload`. A certificate
  nginx has not reloaded is not a configuration file, and is left to the served-certificate check.
  writing `access_log off;`. Statements sharing a line without a brace are split too — `gzip on;
  gzip_vary on;` read whole was gzip set to "on; gzip_vary on", which is not on, and the next save wrote
  `gzip off;` — and a comment after a statement is cut off where nginx would read one (outside quotes,
  where a word could start): certbot ends every line it adds with `# managed by Certbot`, which made
  the certificate path `…/fullchain.pem; # managed by Certbot`, so the form could not save a site
  certbot had touched.
- **`SetVHostEnabled` replaces a stale link.** Enabling a site whose `sites-enabled` entry already
  existed but pointed elsewhere returned success and changed nothing; it goes through `linkEnabled`
  now, so the switch saying on means nginx reads the file. `parseCaddyfile` tracks brace depth so
  only top-level blocks are site addresses — `handle`, `header` and `tls` blocks were listed as
  server names.
- **Site rename.** `POST /proxy/sites/{name}/rename` (`system.admin` + destructive, audited
  `proxy.site.rename` with the new name, path and whether it was re-rendered) takes `{to, reload}`.
  `RenameSite` in `sites_ops.go`, under the service lock, finds the file in sites-available or conf.d
  (a conf.d file keeps its `.conf`), refuses a link rather than a file, an existing target name or
  `sites-enabled/<to>`, a `just-dashboard-env-*` deployment route on either side and a `jd-*` owned
  file, then moves the file, moves its own `sites-enabled` link to the new name and re-points links under
  other names, runs `nginx -t` and puts file, content and links back on a refusal (422). A form-written
  file that still renders byte-for-byte is re-rendered under the new name (header and log paths — the
  renderer derives nothing else from the name); a hand-edited managed file moves unchanged and the
  answer carries a warning. History records a delete of the old path and a `rename` write of the new.
- **Bulk site changes.** `POST /proxy/sites/bulk` (`system.admin` + destructive, audited
  `proxy.sites.bulk` with the action, the names and what changed) takes `{action: enable|disable|delete,
  names}` (at most 200, each once) and `BulkSites` in `sites_ops.go` applies it under the service lock
  as one change: every site's link or file is changed with the same checks a single toggle or delete
  makes, `nginx -t` runs once over the result, and a refusal (422, as a link change) or any site the
  action cannot apply to (400 naming it) puts every site back and changes nothing. Unlike a single
  disable it does not go ahead over a configuration nginx already refuses. Deletes keep `<file>.bak`
  only once the test passed, with the original file's permissions; a backup that cannot be read or
  written refuses the whole change (`TestBulkDeleteKeepsRestrictedBackupsAndRefusesAnUnreadableBackup`).
  One reload follows. Sites already as asked come back as `unchanged`.
  The listing's `roots` and `redirects` (from `root` and `return 30x URL`) feed the list's search and
  its Static and Redirect chips.
- **Site files.** `sites_files.go`, all `system.admin` and audited. `GET /proxy/sites/{name}/download`
  (`proxy.site.download`) hands over one site's file, looked up in sites-available, conf.d, then
  sites-enabled, through `allowedPath` and never a password file. `GET /proxy/sites-export`
  (`proxy.sites.export`; under `/proxy`, since `/proxy/sites/export` would shadow reading a site called
  `export`) is a tar.gz of every listed nginx site as `<layout>/<name>` plus `manifest.json` (layout,
  enabled, aliases, server names, sha256); a file outside the nginx directory or a password file is
  listed as skipped, never packed. `POST /proxy/sites/import/preview` and `/import` take
  `{name, content, enable, reload}` (site name rules, no deployment-route name, text up to 256 KiB,
  refused when the name exists in either layout or in sites-enabled): the file is written into
  sites-available (conf.d where there is none) and linked even when it is to stay disabled, `nginx -t`
  runs, and a preview or a refusal (422) takes both back out; warnings name hostnames another site
  already serves and a file with no server block. `GET /proxy/site-backups` lists backup-suffixed
  plain files in sites-available and conf.d with the site each restores to; `POST
  /proxy/site-backups/{dir}/{file}/restore` (`{as, enable, reload}`) places the copy through the same
  test and keeps the backup; `DELETE /proxy/site-backups/{dir}/{file}` (destructive,
  `proxy.site.backup.purge`) removes it.
- **Import from Nginx Proxy Manager.** `npm_import.go`, `system.admin`, under `/proxy`. `POST
  /proxy/import/npm` (multipart `file`, at most 50 MB, audited as `proxy.import.npm.preview`) copies
  NPM's `database.sqlite` to a private temp file under `$TMPDIR/just-dashboard`, opens it
  `mode=ro&immutable=1` (tables only, 2000 rows each, 20 s), deletes the copy, and returns the items it
  maps to: proxy and redirection hosts as `SiteSpec`s (rendered and `ValidateSpec`ed), streams as one
  `StreamSpec` per protocol, access lists with users as password files under `jd-auth/npm-*` (NPM's
  plain passwords bcrypt-hashed at preview and never kept or returned). TLS is on only when a local,
  unexpired certificate and key cover every domain (NPM's own stay in its container); an access list
  the form cannot write faithfully, a disabled stream, a TLS-terminating stream and a disabled host on
  a conf.d host are skipped rather than imported weaker or live. Advanced configuration is shown,
  never written. Redirect hosts keep NPM's 301, 302, 307 or 308 and its preserve-path choice. An
  imported access list keeps `satisfy any` when it has an allowed address and a password, and clears
  `Authorization` to the upstream when NPM's `pass_auth` is off. A local TLS certificate also keeps
  NPM's HSTS subdomain choice. NPM's asset cache policy is reported for manual setup because its
  settings do not map to the site's cache options. A representative SQLite export is covered by a
  preview, apply and real-nginx request test, including automatic password-file dependency import;
  `JD_NPM_AUDIT_DB` can point that test at a private copy of a database initialized by NPM with its
  fixture rows 7–9. Both paths were run against real nginx, and soft-deleted NPM rows stayed out.
  Conflicts (name taken, hostname served, stream port forwarded, password file present)
  are rechecked at apply. The plan lives in memory for 15 minutes behind a token bound to the actor.
  `POST /proxy/import/npm/apply` `{token, ids}` (`proxy.import.npm`) adds required password files,
  writes every file and links every site, runs `nginx -t` once (422 and everything taken back out on a
  refusal), unlinks the sites NPM had off, records the site and stream writes, and reloads once; a
  spent or foreign token is 410.
- **Catch-all default site.** `default_site.go` keeps one owned file, `jd-default` (sites-available plus
  its link, or `conf.d/jd-default.conf`, first line carrying `OwnedMarker`, so the Sites list leaves it
  out). `GET /proxy/default-site` (a read every account holds: listen lines of files the listing already
  shows, no probe) reads who answers an unknown Host on each socket today from `EffectiveConfig` +
  `NginxTree` — the `default_server` claimant, else the first server nginx reads — and the plan: it
  always claims `*:80`, and `[::]:80`, `*:443`, `[::]:443` only where some site already listens there,
  because a reload that has to bind a new port fails where `nginx -t` (which does not bind) passed;
  443 is skipped when a site serves plain HTTP on it, since `ssl` on one listen line turns the socket
  to TLS for every server on it. Sockets on a named address are reported as not covered. `PUT`
  (`system.admin`, `{choice: close|not_found|redirect|page, redirectTo}`, audited
  `proxy.default_site.apply`) answers 409 `other_default` naming each other file's `default_server`
  on a socket it would claim, before writing anything; otherwise it writes the file (and, for
  `page`, a plain `<nginxDir>/jd-pages/default/index.html` when none exists), links it, runs
  `nginx -t` and undoes all of it on a refusal (422, as a link change), then reloads. The choice sits
  in `location /` rather than the server so the ACME location (`/srv/just-dashboard-acme`) still
  answers: a server-level `return` runs before locations are matched. On 443 the second server is
  `ssl_reject_handshake on` with no certificate. A redirect target is a scheme and host only
  (`$request_uri` is appended; `$`, quotes, `;`, braces refused). `DELETE` (destructive, audited
  `proxy.default_site.remove`) removes the file and its link and reloads; the page directory stays.
  QUIC is not claimed: `quic` listen lines are left to the site that has them, since taking
  `reuseport` means rewriting that site's file.
- **Certificates say who uses them.** `listCertificates` joins the sites' `ssl_certificate` paths
  onto the certificate list (`UsedBy`), through symlinks, so a certbot lineage and the site naming
  its `live/` path are one entry; `certificateName` names a file in a generic directory
  (`/etc/nginx/ssl/site.crt`) after itself rather than after "ssl". `CertbotState.RenewUnit` is a
  certbot timer systemd knows but is not running, which the Certificates page enables and starts
  through `/systemd/{unit}/enable` and `/start`. `DNSProvider.HasCredentials` reports a saved
  token without reading it, and `DELETE /certificates/dns-credentials/{provider}` removes one
  (destructive, ordinary confirmation, audited as `certificates.dns.credentials.remove`).
- **Deleting a certificate revokes nothing.** `POST /certificates/delete {names, force}` runs
  `certbot delete --non-interactive --cert-name` (`DeleteArgs`) for each lineage in turn in one
  `certbot.delete` job; `DELETE /certificates/imported/{name}?force=1` removes
  `importedDir/<name>` (a real directory only, named by `importNameRe`; Caddy release copies are
  refused and stay with the evidence prune). Both are destructive with an ordinary confirmation,
  audited as `certificates.delete` / `certificates.imported.delete`, and answer 409 `in_use`, the
  message naming the sites, while an enabled nginx site's `ssl_certificate` resolves inside the
  lineage or import — unless `force`, which the page sends only after its dialog has named those
  sites. Host-Caddy `tls` file references are not detected. The page's "Delete expired and unused"
  checklist offers expired certbot/imported certificates whose `UsedBy` is empty and never forces.
- **Where an issuance or a renewal failed, and whose it is to fix** (`issue_diagnosis.go`).
  `DiagnoseIssuance` reads a certbot run's output — the authority's per-domain report (`Domain:`,
  `Type:`, `Detail:`), or for a run that failed before or beside validation its meaningful lines, never
  progress such as "Account registered." — into problems, each with a stage and that stage's owner:
  `dns` (NXDOMAIN: the DNS provider or registrar; SERVFAIL, timeouts, refusals: the authoritative
  nameservers), `caa` (the zone's CAA records), `connect` (a timeout: a firewall in front of port 80,
  this host's or the provider's; refused on this host's address: whatever answers port 80 here; any
  failure at an address that is not this host's: whoever holds it — the record points there, or a
  provider maps it here), `challenge` (an invalid response: the web server answering the name on
  port 80), `dns-plugin` (the plugin's credentials or zone), `dns-01` (a TXT record not found or wrong:
  the provider and the propagation wait), `rate-limit`, `account`, `local-port` (certbot's own server
  on an occupied port 80), `installer` (the nginx plugin) or `unknown`. The address the authority
  reached is taken from its words, and `here` says whether it is one of this host's interfaces' —
  set only where that can be said: a host with no public address of its own in that family may be
  behind the provider's NAT. Each problem carries an action and links to the page holding the owner's
  evidence: the Network DNS lookup and delegation tools, the TLS page's DNS and CAA panel, the host
  firewall, external checks, the listening ports on 80, the Sites page's URL resolver, the HTTP tool
  on the challenge path, the rate-limit and account panels. `GET /certificates/jobs/{id}/diagnosis`
  (`system.admin`) reads a failed `certbot.*` job's held output (404 for another kind; a job that did
  not fail has none), and `RenewalHealth.problems` carries the last failed renewal run's — none once
  the run is `recovered` — which the `renewal_failed` alert appends as "domain at stage — owner". The Certificates page's issuance
  console and a site form's certificate step show **Where it failed** under a failed job
  (`issuance-problems.tsx`), and the renewal notice shows the renewal's. Tests:
  `TestDiagnoseIssuanceReadsEachDomainsStageAndOwner`, `TestDiagnoseIssuanceReadsRunFailures`,
  `TestFailedRenewalRunCarriesItsProblems`, `TestCertJobDiagnosisReadsTheFailedRun`, and
  `TestLiveCertbotFailureIsReadByStage`, where the host's certbot orders over webroot, into private
  directories, from a certificate authority on loopback that fails the challenge as a real one does
  (connection, DNS, unauthorized and CAA problems), and its real output is read into each stage —
  no request leaves the host and no certificate exists.
- **Issuing (`certbot_issue.go`, `PlanIssue`).** `IssueRequest` takes `keyType` (`ecdsa`/`rsa`),
  `rsaKeySize` (2048/3072/4096) and `certName` (`certNameRe`, passed as `--cert-name`: names are
  added to a lineage by resending its whole list under its name, which avoids `--expand`). A key of
  another type or size than an exact-names lineage's adds that lineage's `--cert-name` (certbot
  refuses the change non-interactively otherwise) and `--force-renewal` on a real run. The email may
  be empty only when `accounts/<host+path>` under certbot's directory holds an account for the server
  this run orders from (staging for a test run). A webroot must pass `test -d` on the side certbot
  runs on. `POST /certificates/issue/preview` (system.admin) answers the same checks with the argv and
  runs nothing; DNS tokens appear only as their file path, and pasted credentials are checked there
  and saved by the issuance job only. `GET /certificates/account` (system.admin) runs `certbot
  show_account` when an account exists (it asks the authority, briefly holding certbot's lock) and
  returns its server, URL, thumbprint and first email contact.
  **Authorities and accounts** (`acme_accounts.go`): `IssueRequest.ca` picks `letsencrypt`, `zerossl`,
  `google` (Google Trust Services) or `custom` with an https `directory`; empty keeps
  `JD_ACME_DIRECTORY`/Let's Encrypt. The choice drives `--server`, the account check, the CAA
  identifier the preflight judges against (`sectigo.com`, `pki.goog`) and whether Let's Encrypt's
  limits apply. Buypass is not offered: it stopped issuing ACME certificates in October 2025 (its
  directory still works as `custom` if that changes). ZeroSSL and Google need External Account
  Binding to register: `eabKeyId`/`eabHmacKey` on the request are checked, the HMAC key saved by the
  job sealed with `auth.Sealer` in `acme_eab` (keyed by directory), and, only when certbot has no
  account there, written with `mktemp` + `tee` (stdin) on certbot's side as a 0600 file passed as
  `--config` and removed when the job ends — never argv. `GET /certificates/accounts` (system.admin)
  walks `accounts/**/regr.json` (never `private_key.json`), asks `certbot show_account --server
  --account` for each (at most 8, sequentially, for the lock) and lists the offered authorities with
  `eabSaved`/`account` flags, never the key. `POST /certificates/accounts/email` (system.admin,
  audited) runs `certbot update_account --no-eff-email -m` for an account found on disk, as an
  exclusive certbot job.
  `POST /certificates/issue/preflight` (system.admin, `issue_preflight.go`) checks the same request
  before a run and changes nothing it leaves behind: each name's resolution (`CheckDomainDNS`), its
  CAA set (a raw CAA query to `/etc/resolv.conf`'s nameservers via `x/net/dns/dnsmessage`, climbing
  to the first non-empty set, judged against `letsencrypt.org`; another authority's identifier is
  unknown, so a restricting set is a warning), port 80 for the method (standalone needs it free,
  nginx held by nginx; the nginx plugin's own challenge block is not probed), and for webroot a
  `mktemp` token written on certbot's side under `<webroot>/.well-known/acme-challenge/`, fetched over
  loopback with each name as Host, then removed with any folder it had to create. The lineage relation
  (identical / replaces / expands — which certbot refuses under `--non-interactive` — / new) comes from
  the renewal confs. `GET /certificates/rate-limits?domains=` (system.admin, `ratelimits.go`) counts
  production certificates in `archive/*/cert*.pem` by notBefore within seven days (duplicates of the
  exact set of 5, per registered domain by the public suffix list of 50) and failed non-test
  `certbot.issue` jobs this process still holds within an hour (5); the preflight turns a reached limit
  into a critical finding for a real run. No audit entry: nothing persists. The Issue dialog runs the
  preflight as its fields settle (and on Re-check), lists what did not pass, draws the limits as
  meters, and holds a real run on a critical finding until "Issue anyway" is switched on for that
  exact request; the server does not enforce it.
- **Two layouts, and files that are not sites.** `nginxVHosts` (`vhosts.go`, with the rest of the
  listing and `SetVHostEnabled`) lists every place nginx takes a site from, each entry carrying its
  `Layout`: sites-available where it exists, conf.d always (a Debian nginx.conf includes it too; on
  every RPM distro, Alpine and Arch it is the only one), and on a Debian host whatever is only in
  sites-enabled — a copied file, a link to a file kept elsewhere, a link to nothing. nginx reads all of
  sites-enabled, so a backup-named file there is listed. A site's link is judged by identity
  (`readEnabledLink`, `os.SameFile`), not by its text: serving the file is `Enabled`, a link to nothing
  is `Broken: "dangling"` — nginx refuses every reload while it is there, and Lstat used to report it as
  serving — and a link to, or a copy of, another file is `Broken: "stale"`, with `LinkTarget` saying
  where it points and `TargetServedElsewhere` whether nginx also reads that file through another name
  in sites-enabled or through conf.d — when it does not, the site's Enable, which points the link at
  the site's own file, takes it out of nginx, and the page asks before it; the other names in sites-enabled that link to a sites-available file are its
  `LinkedAs`, make it `Enabled`, and are not listed as sites of their own. `ResolvesTo` is the real
  path of a file outside the proxy's directories, which the editor refuses to open while its switch
  still works. A conf.d file is listed only when it declares a `server` block (read with
  `ParseNginxFile`; one it cannot parse is listed): a file holding only a `log_format`, `map` or zone
  is configuration other sites depend on, and listed as a site it read "Default host" with a Delete.
  conf.d has no symlink, which reaches the UI as an empty `EnabledPath`, because a switch that can only
  error is worse than none. `FormEditable` says the site form reads the file and
  saves it back to the same place (sites-available, or a `.conf` in conf.d where there is no
  sites-available); a conf.d file on a Debian host or a file only in sites-enabled would be saved beside
  itself under the same names, so it gets the raw editor. `confdPath` stops `app.conf` becoming
  `app.conf.conf`. Names, listens and upstreams are listed once each (a forced-HTTPS site is two server
  blocks with the same names); `CertPaths` holds every `ssl_certificate`, unquoted, including one in a
  snippet the site includes (one level, only files the editor would show), with `CertPath` the first;
  and a `listen … ssl` or `quic` is TLS whether or not the certificate is named in the file. The
  listing **skips backups** in sites-available and conf.d (`isBackupFile`: `.bak`, `~`, `.dpkg-old`,
  `.rpmsave`) — nginx reads none of them there, and since delete keeps `<name>.bak`, without the filter
  deleting a site produced a second site.
- **What a site does, who wrote it, and what is not a site.** `siteDetails` (`site_discovery.go`)
  reads each site file with `ParseNginxFile`. `Pools` are its `upstream` blocks with their servers, so a
  `proxy_pass http://<pool>` reads as the servers behind it. `Features`, in `siteFeatureOrder`, are
  `auth` (`auth_basic`), `sso` (`auth_request`), `allow` (an `allow` anywhere, or a `deny` at the
  server's own level — the form's fences around dotfiles and backups deny inside a location and restrict
  nobody), `ratelimit` (`limit_req`, `limit_conn`), `cache` (`proxy_cache`), `ws` (an `Upgrade` header
  to the upstream), `h2` and `h3` (`http2`/`http3 on`, or `http2`/`quic` on a `listen`), and
  `maintenance` — an `if ($jd_<site>_maint)` in a server block, which the site form must write only
  while maintenance is on. An `include` the site file itself writes is followed one level, for files
  the editor would open, and never into the site's own file: a site that includes itself — by name,
  through its link, or through a glob such as `sites-available/*` in a server block — would otherwise
  be expanded into itself without end, and the stack overflow took the whole process down on every
  listing. `AccessLog` and `ErrorLog` are the files the site writes to: a log one of its
  server blocks names comes first (a forced-HTTPS site logs where its serving block says, not where its
  redirect block inherits), then the one nginx.conf sets — its `http` block's, or for errors the main
  context's before that (`inheritedLogs`; that file only, not what it includes). Both are empty for
  `off`, `syslog:`, `stderr`, a device, and a relative or variable path. The Logs page opens only the
  files it lists, so the API then clears either one it does not (`keepOpenableLogs`,
  `handlers_proxy_vhosts.go`, against `logsx.Discover`) — a file outside `JD_LOG_ROOTS`, one nginx
  has not created yet, a name the inventory skips — and the Sites page offers Access log and Error log
  only for a file the Logs page opens. A file nginx cannot parse gets none of these fields. `Package` is the dpkg package that installed the file, set only while the file still
  matches the md5 dpkg recorded for that conffile (`/var/lib/dpkg/status`, and
  `/host/var/lib/dpkg/status` from the dashboard's container; read again when either changes; obsolete
  entries ignored). That is Ubuntu's untouched `sites-available/default`, which the Sites findings no
  longer call "on disk but not serving" and the page does not sort among the sites needing attention.
  The recorded paths are absolute, so it is set only with `JD_NGINX_DIR=/etc/nginx`, and never on a host
  without dpkg. A file named `jd-*` whose first line contains `OwnedMarker` ("Just Dashboard owned") is
  the dashboard's own plumbing — the catch-all default site, a shared log format — and is left out of the
  listing in sites-available and conf.d, and so is any link in sites-enabled to one — but not a link
  under such a file's name that points at nothing or at another file: nginx refuses every reload over
  the first and serves the second, so it is listed from sites-enabled like any other link (a link to
  nothing with its Remove link); a feature that writes such a file puts the marker on its first line
  and shows the file itself. `Owner` is filled in by the API, not proxysvc (`markDeploymentRoutes`,
  `handlers_proxy_vhosts.go`): a name that `deploy.RouteNameFor` spells (`just-dashboard-env-<id>.conf`,
  on nginx and in the Docker Caddy ingress alike) is joined to `deploy_environments` and
  `deploy_projects` for the project and environment ids and names — an archived project's own name
  from `archived_name`, as the deploy store reads it, since its `name` column holds an
  `__jd_archived_<id>_<hex>` tombstone — with `archived` when either is archived; a name whose
  environment no longer exists has none. The Sites page leads such a route to its deployment in place
  of Edit, Disable, Duplicate and Delete, opens it read-only (its card and a `?site=` link alike), and
  keeps Edit anyway behind a confirmation; an archived owner's route is the operator's again. The backend does not refuse
  those verbs on a route, since deployments call proxysvc directly and an administrator may override.
  A deployment route nothing deploys any more — no owner (its project deleted) or an archived one — and
  that Delete does not apply to (every Docker Caddy route, which has no host file) has **Remove route**,
  on its card and its page: `DELETE /proxy/vhosts/{name}/deployment-route` (system.admin, destructive,
  audited as `proxy.deployment_route.remove`) accepts only a name `deploy.RouteNameFor` spells, answers
  409 `deployment_active` while an unarchived environment still owns it, and runs
  `proxysvc.RetireDeploymentRoute`: `RemoveDeploymentRoute`'s snapshot/restore removal, then on the
  Docker ingress `rm -rf <caddy certificates>/*/<name>` for each plain domain (checked against
  `certDomainRe`; wildcards are left) that only that route served, since Caddy goes on renewing and the
  Certificates page goes on listing a certificate it holds whether or not a route asks for it. A
  certificate it could not remove is reported as `certificateError` beside a removal that succeeded.
  Without this a route outlived its project, serving its domain, with nothing on any page to remove it.
  The overview draws such a route's application as its owner when no socket names its upstream — a
  deployment's container is dialled at its network address (`10.0.0.3:3000`), which no listener holds —
  so the route list, the route map and the traffic band carry the project's name and website icon
  (`ownedBackend`, `route-map.ts`), as the Sites cards already did.
  listing and `SetVHostEnabled`) reads sites-available where it exists and conf.d where it does not
  (every RPM distro, Alpine, Arch — most of the servers this runs on); the difference reaches the UI as an
  empty `EnabledPath`, because conf.d has no symlink and a switch that can only error is worse than none. `confdPath` stops `app.conf` becoming `app.conf.conf`, and `ReadSiteSpec` reads the listing's `app.conf` back as a spec called `app`, the name a save writes to — the spec kept `.conf` once, and the next save renamed the site's logs to `app.conf.access.log`. The listing
  **skips backups** (`isBackupFile`: `.bak`, `~`, `.dpkg-old`, `.rpmsave`) — nginx reads none of them, and
  since a delete, and a save over a hand-written file, keep `<name>.bak`, without the filter deleting a
  site produced a second site.
- **A password file must be readable by the account that reads it.** nginx opens `auth_basic_user_file` in
  a *worker* (www-data/nginx/http), not as the root that wrote it, so a 0640 root:root file is a 403 for
  every visitor and "Permission denied" in the log — which reads exactly like a wrong password.
  `nginxWorkerGID` takes the account from this host's `nginx.conf`, falling back to the three defaults;
  where none resolves the file is 0644, which is what `htpasswd` itself produces. `authDir`/`streamDir`
  hang off `JD_NGINX_DIR`, which exists precisely for hosts whose nginx is elsewhere. A missing file is
  not an nginx error: `nginx -t` passes, reloads succeed, and the site answers every login with 403 and
  an error line per request (checked on 1.26.3), so deleting a password file a site uses fails silently.
  That is why `ListAuthFiles` carries `usedBy` — every nginx site (sites-available, sites-enabled,
  conf.d, enabled or not, one include level deep, relative paths from the nginx directory) whose blocks
  name the file — and why `DELETE /proxy/auth-files/{file}` answers 409 `in_use` for such a file unless
  `?force=1`, auditing the sites it named. Caddy's `basic_auth` inlines its hashes and is not counted.
  Removing a file's last login keeps it empty, which asks for credentials again like a wrong password.
- **`import.go`** checks the key against the certificate **before** writing either: a mismatched pair is
  accepted by every text editor and refused by nginx at reload, which on a live server means finding out
  during an outage. Imports live in `/etc/ssl/just-dashboard`, so a renewal run can never prune a
  certificate it did not issue. The key is found among the blocks pasted (an `EC PARAMETERS` block in
  front of it is skipped; an encrypted key without a password is refused); the leaf is the
  certificate that key belongs to, wherever it sits in the bundle; the chain is followed by signature and
  saved leaf first without the root; `ChainComplete` means the chain reaches a root (the bundle's own,
  one the system trusts, or `JD_ACME_CA_ROOT`), not that more than one block was pasted. A name already
  imported is a 409 `certificate_exists` naming what is there; `replace: true` overwrites it and keeps
  the previous pair as `.bak`, so replacing is a write rather than a destructive act.
  **`import_formats.go`** turns what authorities send into that PEM first (`Service.Import`, and
  `Service.InspectImport` behind `POST /certificates/import/inspect`, system.admin, which runs every
  check with `dryRun` and writes nothing — it returns the chain, a name suggested from the CN, the import
  it would replace and the enabled sites serving it). A PFX (`pfx`, base64) is read by
  `x/crypto/pkcs12`, falling back to the host's `openssl pkcs12` for AES/PBES2 files, with the password
  in the child's environment, never argv; an encrypted key (legacy `Proc-Type` or PKCS#8 via
  `youmark/pkcs8`) is decrypted with `password` and saved decrypted, 0600, since nginx cannot prompt.
  With `fetchIssuer` (the operator's consent on the Inspect step) a chain that stops short follows the top
  certificate's AIA http(s) address, up to three hops: public addresses only on every dial including
  redirects, no proxy, 5 s, 64 KB, and a fetched certificate is kept only if it signed the one below.
  The password is never audited. Caddy's release
  copies (`caddy-<24 hex>`, `docker_caddy_certs.go`) share the directory and are refreshed through
  `keepCaddyEvidence`. `ListCertificates` keeps them: deployment activation
  (`ResolveDeploymentCertificate`), preflight, the route summary and the hostname suggestions find a
  release's certificate there, and on a Docker Caddy host a copy is the only pair covering its domains
  (the route summary names it with `certificateRenewedBy: "caddy"` and neither days left, a link to the
  Certificates page, which does not list it, nor an expiry finding: the copy is refreshed only when a
  release runs, and graded by its own expiry it warned about a certificate Caddy had already renewed).
  `CertificateInventory` — `GET /certificates/` — leaves out the copies no
  nginx site names, since Caddy renews what it serves and never these, and listed they raised an expiry
  finding apiece; their names are refused to an operator's import. On a Docker Caddy host it lists
  what Caddy serves instead, read from `/data/caddy/certificates` in one `docker exec` (a fixed `sh -c`
  script, the directory its argument): source `caddy`, named by domain, `usedBy` the Caddy routes whose
  names it covers, `expiring` always false (Caddy renews a third of the term early; expired stays true),
  and each release copy for its domain folded under it as `evidence`. When Caddy cannot be read the list
  carries one unreadable `caddy` entry saying why rather than silently listing none.
  `GET /certificates/evidence` (system.admin) lists the copies no Caddy route serves and no release names
  (`OrchestrationStore.ReleasedHostnames`, every hostname in any release's runtime snapshot, retired ones
  included, since any can be rolled back to); `DELETE /certificates/evidence` (destructive, audited as
  `certificates.evidence.prune`) takes the confirmed `names`, recomputes the orphans and removes only
  those still orphaned, answering `{removed, kept}`. An unreadable snapshot or Caddy route list refuses
  the prune rather than treating the release as naming nothing.
- **Certificates made here** (`csr.go`, `localca.go`, routes in `api/handlers_certificates_private.go`,
  mounted from `mountCertificateRoutes`). A **signing request** (`POST /certificates/csr`, system.admin)
  makes the key on this server — ECDSA P-256/P-384 or RSA 2048/3072/4096, PKCS#8, 0600 — and a request
  for host names and addresses with optional organisation fields, kept in
  `/etc/ssl/just-dashboard-private/requests/<name>/` (the directory, 0700, is the claim on the name: a
  second request under it is a 409 `request_exists`). `GET /certificates/csr` lists what waits, with the
  PEM request, never the key, and the certificate already kept under the name (`Replaces`), since
  renewing a bought certificate is a new request under the same name. `POST /certificates/csr/{name}/complete`
  takes only the authority's certificate: the leaf is found by the waiting key, a certificate for another
  key is a 400 naming it, and the pair goes through `importCertificate` — ordering, chain verdict,
  `replace` as for an import (409 `certificate_exists` without it) — then the request is removed; a name
  the certificate leaves out of the request is a warning. `DELETE /certificates/csr/{name}` (destructive,
  no phrase) deletes a request and its key. **Self-signed** (`POST /certificates/self-signed`) and
  **local CA** certificates (`POST /certificates/local-ca` makes the root, a 409 `local_ca_exists` after;
  `POST /certificates/local-ca/issue`) are 397-day server certificates for names or addresses, written
  beside the imports by `installPair` (key 0600 then certificate 0644, staged and renamed, a replaced pair
  kept as `.bak`) so sites and the inventory read them as imports. The root is ECDSA P-256,
  ten years, path length zero, in `/etc/ssl/just-dashboard-private/ca/` (0700; `root-key.pem` 0600): no
  route reads or exports its key, and `GET /certificates/local-ca/root.pem` hands every signed-in account
  the certificate alone as `application/x-x509-ca-cert`, since a root is made to be installed. The private
  directory is apart from `importedDir` because every directory there is listed as a certificate.
  A certificate is the local CA's when the root verifies it (`localCALeaves`), not by a note beside it;
  `CertificateInventory` marks those `LocalCA`, and `summarise` now lists a certificate's addresses among
  its `Domains`. `GET /certificates/local-ca` (every signed-in account) is the root, each certificate it
  signed with the sites naming it and when it renews, and the daily check's last pass and next run.
  **The daily check** (`localCARenewal` in `api/modules_proxy.go`'s lane F sections: five minutes after
  start, then every 24 hours) runs `RenewLocalCALeaves`: every certificate within 45 days of expiry — well
  before the page's 30-day warning — is signed again for 397 days on the key it has, so the key a site
  names never changes, the previous certificate kept as `.bak`; nginx is then reloaded once if an enabled
  site names one, and a failed test is the pass's error ("renewed, but nginx was not reloaded …"). A pass
  that renewed or failed anything is audited as the system (`certificates.local-ca.renew`). Self-signed
  certificates are never renewed: a new one is a certificate every device has to be told about again.
  Writes are serialised by `privateMu`, never the service lock. Tests: `csr_test.go`, `localca_test.go`
  (the sweep with a fake clock, openssl cross-checks) and `TestLiveNginxServesALocalCACertificateAndItsRenewal`
  (the host's nginx serving an issued certificate that a client trusting only the root accepts by name
  and address, then serving the renewal the check reloaded it for).
- **`ct.go`** — the Certificate Transparency monitor, off until an administrator switches it on, since
  every check sends this host's domain names to crt.sh (setting `proxy.ct_monitor`; `PUT
  /certificates/transparency` on, `DELETE` off, destructive with no phrase; both audited as
  `certificates.transparency.enable`/`.disable`). `GET /certificates/transparency` (system.admin) is
  `{enabled:false}` while off and asks nothing; on, it reduces every vhost server name and certificate
  name to its registered domain (`RegisteredDomains`, `golang.org/x/net/publicsuffix`; names under no
  public suffix are skipped), asks crt.sh for the first ten (`q=<domain>` and `q=%.<domain>`,
  `exclude=expired`, three at a time, 45s each, answers over 8 MiB refused rather than listed in part)
  and keeps each domain's rows for six hours (`CTMonitor` in `modules_proxy.go`'s lane F sections;
  failures are not cached, one query per domain is shared between readers). Verdicts are made on each
  read: a certificate is `ours` when its serial is one held live, imported or in certbot's `archive/`,
  and `unexpectedIssuer` when it is not ours and its issuer's organisation signed none of this host's
  publicly trusted certificates (never set when there are none to compare with). A precertificate and its
  certificate share a serial and are listed once.
- **`streams.go`** — nginx's `stream` is a sibling of `http`, so a stream cannot live under
  sites-available. They go in `<JD_NGINX_DIR>/stream.d`, and the page says plainly when `nginx.conf` does not
  include it. nginx.conf is never edited silently: the one change the dashboard makes to connect the
  directory (below) is shown before it is made and asked for.
  - **The module comes first.** Debian and Ubuntu build the stream module dynamic and ship it as
    libnginx-mod-stream, which a plain `apt install nginx` leaves out; there a `stream` block is an unknown
    directive, `nginx -t` fails and every reload is refused. `nginx_modules.go` (`StreamModule`) reads
    `nginx -V` word by word (`--with-stream`, `=dynamic`; `--with-stream_ssl_module` is not the module), the
    `load_module` lines of a `nginx -T` dump, nginx's own "unknown directive" when a block is already there,
    and the `.so` on the host, into static / loaded / not-loaded / not-installed / absent / unknown; a `.so`
    that is absent is not-installed even when the dump fails. For not-installed the list handler names the
    package (`ModulePackage`: `libnginx-mod-<name>` for apt, `nginx-mod-<name>` for dnf/yum/apk,
    `http-xslt-filter` for XSLT) only when the package manager has it (`updates.Describe`, remembered ten
    minutes in `proxyExtras.modulePackages`, asked through its `catalogue` — the updates service, which
    tests replace with a fake: an `ErrUnknownPackage` names nothing, any other failure names it for the
    install to explain), and the page installs it through `POST /packages/install` as a job — Debian's
    package links `modules-enabled/50-mod-stream.conf` itself. Unknown is never reported as missing.
  - **The module report** (`NginxModules`, `GET /proxy/modules`, every signed-in account) lists each
    `--with-` module `nginx -V` names — not threads, compat or the compiler flags — with `version`, `openssl`
    and `modulesPath`, each static / loaded / not-loaded / not-installed / unknown (the configuration could
    not be read, `detail` says why) and, for not-installed, the package as above. Kept a minute per service
    (`?fresh=1` asks again); 503 `not_installed` without nginx. Lane A's engine line reads it.
  - **Connecting the directory** (`main_dropin.go`). `GET /proxy/streams/include/plan` (system.admin)
    works out the change without writing: a drop-in `zz-just-dashboard-stream.conf` holding
    `stream { include <dir>/*.conf; }` in the first directory nginx.conf includes at its top level by a
    glob it matches (Ubuntu's `modules-enabled/*.conf`, so nginx.conf stays the conffile its package
    shipped; `zz-` sorts after the modules' own files because nginx refuses a `load_module` after a block —
    a load_module read after the drop-in sends it to the next mode); else the block appended to nginx.conf
    between `# Just Dashboard: streams begin.`/`end.` markers in the file's own line endings; else, where a
    top-level stream block exists (a second is "duplicate"), one `include …; # added by Just Dashboard`
    line before its closing brace, found by an offset scanner that reads comments and quotes as nginx does.
    An nginx.conf edit carries `reason` — `no-directory`, `load-module-after`, `directory-elsewhere` (the
    directory leads outside `JD_NGINX_DIR`) or `name-taken` (a file of the drop-in's name that is not the
    dashboard's), the first such directory's — and `dropIn`, where the drop-in would have gone, so the
    sheet never gives another mode's reason. The plan is checked with `NginxTree` (the changed
    configuration must read the directory in a stream block), refuses a file outside `JD_NGINX_DIR` as the
    connect would, and lists the staged stream files and their sockets that another program or another
    staged stream holds. For an edited file it decides the copy then: `keepsCopy` is false, with a
    warning, where an include in the changed configuration would read `<file>.jd-stream-<unix>.bak` by the
    file's configured name or its resolved one (beside a stream block, a second one), and the connect
    keeps exactly the copy the plan named. Refusals are 409s with codes: `module_missing`, `already_included`, `include_misplaced`,
    `stream_block_elsewhere` (outside `JD_NGINX_DIR`), `config_unreadable`. `POST /proxy/streams/include
    {mode, path, reload}` re-plans under `s.mu`, refuses a plan that changed (`plan_changed`) or has
    conflicts (`port_in_use`: `nginx -t` does not bind, and the failed bind would poison every later
    reload), keeps the plan's copy of an edited file where it named one, writes
    through the resolved path, runs `nginx -t`, and on failure puts the bytes back (mode and CRLF kept, the
    copy removed; 422 `invalid_config`). `POST /proxy/streams/include/remove {reload}` (destructive — a
    POST, since `DELETE /{name}` would take a stream called "include") takes out exactly what a connect
    wrote — the unchanged drop-in, the marked block, the marked line — and nothing written by hand
    (`not_connected`), with the same test and restore. The listing's `connection` names the dashboard's
    own include, and `streamBlock` the file whose stream block the manual snippet (then an `include` line)
    goes into. Audited `proxy.stream.include.add` / `.remove` with mode, file, copy, streams and reload.
  - **Pausing a stream** (`stream_pause.go`): `POST /proxy/streams/{name}/enabled {enabled}` (system.admin,
    destructive, audited `proxy.stream.toggle`) moves `stream.d/<name>.conf` to and from `stream.d/paused/`,
    which the `*.conf` include glob does not reach. Both directions run `nginx -t` when the directory is
    read and move the file back on failure (422 `invalid_config` — a pause fails only under a hand-written
    glob that takes the `paused/` directory itself). Resume refuses a taken name (`stream_exists`) or a
    socket another stream, site or program holds (`port_in_use`), and a reload nginx fails on the stream's
    own port puts it back in `paused/`, as a save does. A symbolic link is not paused (`stream_linked`):
    deleting the link stops it. The listing returns paused files in `paused`, apart from `streams`, with
    state `paused`; a delete takes a paused name too, without a reload, and a new stream or a rename onto
    a paused name is `stream_exists`.
  - **Where stream.d is read** (`stream_state.go`) is `NginxTree` over the files on disk, with a probe file
    placed where a new stream would go: an include inside `http` is named as misplaced, a `stream` block in
    its own file and a relative include count, and `upstream` or `other-stream.d` no longer do. It reads
    from disk because it is asked with the lock held (`includeNote`) and while the configuration fails.
  - **Hand-written files** are parsed token by token. `ParseStreamSpec` returns what the form cannot express
    (an upstream server option other than weight/max_fails/fail_timeout/backup/down, TLS, a port range,
    an unknown directive) and the save refuses to
    overwrite such a file (`HandwrittenStreamError`, 409 `stream_handwritten`); the raw editor is the way
    in. A bind address, `10m`-style times, a direct `proxy_pass`, `unix:` upstreams and the UDP mode are
    read and written back, so a loopback forward is never saved onto every interface. A hand-written file
    the form rewrites is kept as `.bak`.
  - **Access and limits.** `StreamSpec.Rules` (`{action: allow|deny, source}`) with `DefaultAllow` is the
    ordered access list: rendered in order, then `allow all;` or `deny all;`, and read back with the first
    rule for `all` as the default (nothing after it is reachable, so it is dropped). Rules that only allow,
    with everyone else denied, are folded into `AllowFrom` on save and on read, so one file reads back one
    way; `allowFrom` and `rules` together, `all` as a rule's source, and denying everyone with no rule are
    refused. `MaxConnPerIP`/`MaxConnTotal` render `limit_conn_zone $binary_remote_addr` /
    `$server_port zone=<NginxIdent>_conn_ip|_conn_all:1m` at the top of the file (zone names are global to
    nginx, http included) and `limit_conn` in the server; `UploadRate`/`DownloadRate` (KiB/s, per
    connection) render `proxy_upload_rate`/`proxy_download_rate`. Only zones declared under the stream's own
    names, each used once, read back; any other zone or cap is hand-written.
  - **Pools.** `StreamSpec.Servers` (`{address, weight?, maxFails?, failTimeout?, backup?, down?}`) with
    `Balance` (empty round robin, `least-conn`, `client-ip` = `hash $remote_addr consistent`, `random`) and
    `NoRetry` (`proxy_next_upstream off;`) render into the stream's upstream block, the method first. nginx
    defaults are folded (weight 1, max_fails 1, fail_timeout 10s), and a pool of one folds into `Upstream`
    with its method, retry and options dropped, since nginx counts, weighs and retries nothing for a lone
    server; `Upstream` is always the first server. Refused: backup under client-ip or random (nginx has none
    there), every server down, no up server that is not a backup, a duplicate address, more than 32. The
    upstream Test dials every server not marked down.
  - **TLS.** `TLS` with `CertPath`/`KeyPath` puts `ssl` on every listen and writes `ssl_certificate` and
    `ssl_certificate_key`; `UpstreamTLS` writes `proxy_ssl on`, `UpstreamName` adds `proxy_ssl_name` with
    `proxy_ssl_server_name on` (without a name nginx would check against the generated upstream name), and
    `UpstreamVerify` adds `proxy_ssl_verify on` with `proxy_ssl_trusted_certificate UpstreamCA` and needs
    the name. TCP only (no DTLS); paths match `absPathRe`, the name `domainRe` without a wildcard; an end
    that is off drops its fields. `StreamModule.SSL` reads `--with-stream_ssl_module` from `nginx -V`, and
    `ApplyStream` refuses TLS with `ErrNoStreamSSL` (409 `stream_ssl_missing`) when the build lacks it — a
    file saved for later would otherwise fail the reload that connects the directory. `Certificate.UsedByStreams`
    lists the unpaused streams serving a certificate (source `stream:<name>` when only a stream names it).
    `proxy_ssl_verify_depth`, protocols, ciphers and client certificates are hand-written.
  - **Routes by TLS name** (`stream_sni.go`). `Routes []{name, upstream}` writes `map
    $ssl_preread_server_name $<ident>_sni { hostnames; <name> <ident>_sniN; default <ident>_backend; }`, one
    `upstream <ident>_sniN` per route (a host name resolves at load, no resolver needed), and `ssl_preread on;
    proxy_pass $<ident>_sni;` in the server. The pool is the default for any other name, no SNI, or non-TLS.
    TCP only, refused with `TLS` or `UpstreamTLS` (the TLS passes through unopened); names are lowercased,
    must match `domainRe` and hold a dot (so no map keyword like `default` or `include`), at most 64, no
    duplicates; route upstreams go through `validStreamUpstream`. `StreamModule.Preread` reads
    `--with-stream_ssl_preread_module`, and `ApplyStream` refuses routes with `ErrNoStreamPreread` (409
    `stream_preread_missing`). The parser reads the map, the route blocks and `ssl_preread` back and names
    any other shape. A site already on the port (443) is refused by the port check like any other holder.
    The upstream Test dials the pool only, not the routes' backends. Audited as `routes` in `proxy.stream.apply`.
  - **Listen options** (`stream_listen.go`). `ListenEnd` makes `listen <first>-<last>` (at most 100 ports;
    `streamBinds`, `freePort`, the session list and `configClaims` via `listenBinds` all cover every port of a
    range). A range refuses `MaxConnTotal`, whose zone is keyed on `$server_port` and would cap each port
    apart. `SamePort` (range only) writes `proxy_pass <ip>:$server_port` with no upstream block: `Upstream` is
    then an IP address alone (a name would need a `resolver`), one server, no routes. `AcceptProxy` with
    `TrustedProxies` (TCP only, 1–32 addresses/CIDRs, never `all` or a /0) puts `proxy_protocol` on every
    listen and writes `set_real_ip_from` per entry; `StreamModule.RealIP` reads `--with-stream_realip_module`
    and `ApplyStream` refuses without it (`ErrNoStreamRealIP`, 409 `stream_realip_missing`). All three parse
    back; a proxy_protocol listen without `set_real_ip_from`, or on some listens only, is named. `Address` is
    checked against the host's interfaces (`StreamAddressError`; the backend shares the host network
    namespace) unless `ip_nonlocal_bind` is on — refused with 422 `address_not_local` on `spec.address`, and a
    warning in the preview. `GET /proxy/streams` returns the addresses as `addresses [{address, interface}]`
    for the form's picker (link-local IPv6 left out: a listen line cannot name its zone). Audited as
    `listenEnd`/`samePort`, `address` and `trustedProxies`.
  - **Testing a stream** (`stream_dial.go`). `POST /proxy/streams/test {target, port, protocol, mode,
    query?}` (system.admin — it dials an address the caller chose — and audited `proxy.stream.test`) dials
    from the host's network namespace inside 5 s: `mode: upstream` the forward's target, `mode: nginx` the
    stream's own port (the page sends 127.0.0.1 or ::1 for a wildcard listen). TCP waits up to 2 s for a
    banner; `query: dns` (default for port 53) sends a root NS query, over TCP with its length prefix, and
    `ntp` (default for 123, UDP only) a client packet; any other UDP port is answered `silent` without
    sending, since a datagram the service does not understand proves nothing. Outcomes: connected,
    answered, closed (nginx accepted and dropped — an access rule, a cap or an unreachable upstream),
    silent, refused, timeout, dns, unreachable, error. A hostname upstream carries a warning that nginx
    resolves it once per reload. `unix:` upstreams are not tested (the socket path may not be in the
    container). netsec's PortCheck/BannerGrab are not reused: their own 6 s timeouts and a read that
    ignores the context would overrun the 5 s cap, and they answer in prose rather than an outcome.
  - **Traffic** (`stream_traffic.go`). `LogConnections` (on by default for a new stream in the form)
    renders `log_format jd_<ident>_log '$msec $remote_addr $protocol $status $bytes_sent $bytes_received
    $session_time "$upstream_addr" $server_port';` at the top of the stream's own file and
    `access_log /var/log/nginx/stream-<name>.log jd_<ident>_log;` in its server; the parser reads back exactly
    that pair (and `access_log off`), anything else is unsupported. The format lives in each file rather than
    a shared `_jd-*.conf`, so a paused or copied stream carries it and there is no second managed file.
    `GET /proxy/streams/{name}/traffic?window=1h|24h|7d` reads at most 8 MiB from the end of the log and its
    `.1` rotation (a `.2.gz` left unread makes `complete` false): sessions by bucket (by the time they
    ended), status mix, bytes each way, session-length percentiles and the 12 busiest clients.
    `GET /proxy/streams/traffic` is the last hour of every logging stream for the cards (1 MiB each), and
    `GET /proxy/streams/{name}/sessions` lists the ESTABLISHED TCP sockets on the stream's port whose owner
    is nginx or unknown (gopsutil; a UDP-only stream has no per-client socket and says so). All three are
    readable by every account, like the nginx access logs in the log viewer; a Deny or Allow on a client
    is an ordinary save of the stream (system.admin, audited `proxy.stream.apply`). The log line's
    quoted `$upstream_addr` is read too (`streamLogLine.upstream`), and `GET /proxy/streams/{name}/path`
    (`stream_path.go`, every account) joins the forward, its access list, the client sessions, the
    connections nginx holds to each backend now and the last hour by backend, for the connection
    investigator's stream evidence ([network-investigator.md](network-investigator.md#native-streams-on-the-path)).
  - **A save** (`ApplyStream(spec, previous, reload)`) refuses a new name, or a rename, onto a taken one
    (409 `stream_exists`) and a port another stream, a site or another program holds (`PortInUseError`,
    409 `port_in_use` on `spec.listen` with the next free port): `nginx -t` passes all three, and the
    reload then fails inside the master while `nginx -s reload` exits 0. What holds a port
    (`portClaims`) is read from the stream directory's files, the stream and http servers of the
    configuration nginx reads (`configClaims`: a site by its first server name, else its Sites-page name;
    an http `quic` listen is UDP, a listen-less server is `*:80`), and the running nginx's sockets; nginx's
    own sockets are no conflict (its configuration names what it asks for, and one it holds for a server
    that is gone is let go at the reload). `POST /proxy/streams/preview {spec, previous}` answers the same
    refusal as `conflict` (`PortOwner` plus `suggest` and `message`), so the form says it while the port is
    typed. A reload is then **watched** (`stream_probe.go`, `awaitListening`, up to 3 s, only when nginx
    reads the file as a stream): done when a worker that was not there before runs and nginx holds every
    socket (`listening: true`); a `bind()` failure nginx logs for the stream's own address puts the stream
    back as it was (new file removed, edit restored, rename undone) and is the same 409 with nginx's words
    in `BindError` — a stream nginx cannot bind makes every later reload on the host fail, so it is never
    left behind — audited `result: rolled-back`; any other `[emerg]` is `reloaded: false` with
    `reloadError` ("nginx did not take the reload up: …") and the valid file stays; a wait that runs out is
    `listening: false` with `listenNote`; a running nginx that cannot be read is `listenNote` alone. A
    rename moves the old file to `.bak` before
    one test and puts both back if it fails. It reloads only when asked ("Save for later" does not), and a
    reload that fails after a clean test is a 200 with `reloadError`, not "not applied". The upstream block
    is `NginxIdent(name)_backend`, so `a-b` and `a_b` no longer declare one upstream; UDP writes
    `proxy_responses 1` only in the one-reply mode (it made every datagram a new session for a game server
    or WireGuard); protocol `both` writes a TCP and a UDP listen on the same addresses, the port check asks
    for both sockets and names the one that clashed (`514/udp`), and `proxy_responses` touches its UDP side
    only; the idle (`timeout`) and connect timeouts are separate and written as nginx time (`10m`, `1h30m`).
    A file's zero timeout (`proxy_timeout 0`: nginx takes it and drops every connection) is `unsupported`, not unset.
    Delete reloads only when nginx read the directory, and returns a failed reload as `reloadError`. A
    stream directory that cannot be read is a retryable 500 `stream_dir_unreadable` ("Could not read the
    stream directory"), never an empty list.
  - **Each stream's state** (`fillStreamStates`, `state` / `stateReason` / `blocker` / `bindError` on
    every listed stream): `not-read` where the file is not in a top-level stream block of the tree (no
    module first, then the include missing or misplaced, a file that does not parse — every reload refused
    — or an include that does not take it), `shadowed` where a stream read before it has one of its exact
    addresses (nginx warns "conflicting server name" and gives the first every connection; a wildcard and
    one address on a port are not a shadow), `not-listening` where a site nginx reads has its port (nginx
    fails every reload on the second bind, verified on 1.27) or where the running nginx holds no socket
    for a bind — named with the program holding it, nginx's last logged `bind()` failure for that address,
    or "it last loaded its configuration before this file changed" (newest worker's start against the
    file's mtime), `live` where nginx holds a socket taking every bind (its wildcard serves one address;
    a live file changed since it was loaded says so), and `unknown` with why where the running nginx could
    not be read. **The running nginx** (`stream_probe.go`) is the master process whose title names this
    `nginx.conf` (`-c`, `-p`, else the build's `--conf-path`; two that do are told apart by the pid file,
    read on the host where the dashboard runs in a container), its sockets are its network namespace's
    `/proc/<pid>/net/{tcp,tcp6,udp,udp6}` — readable by any user, which is what lets a dashboard see an
    nginx in another namespace — held by the master's descriptors, or, where those cannot be read (the
    dashboard runs as another user), owned by nginx's uid; the host's listener list names another
    program only in nginx's own namespace. The error log is the configuration's first top-level
    `error_log` file, else the build's, read only under `/var/log` or `JD_NGINX_DIR` — the states are
    shown to every account and the dashboard runs as root. `nginx -V` is kept a minute per service.
  - **Links and unreadable files.** The listing names a symbolic link (`link`, and "a symbolic link" in
    `unsupported`, so the form opens read-only; a save over one is `stream_handwritten`) and says a link to
    nothing links to nothing. Delete (`StreamDeletion`) removes a link as a link, leaving its target, and
    removes a file it cannot read without a `.bak`, answering `link` or `unread` in place of `backup`: a
    dangling link fails `nginx -t` for the whole host and was the one file the page could not remove. The
    route unescapes the name (`httpx.URLParam`), since the page encodes it and the listing shows any
    `*.conf`. A directory included in the wrong block counts as read for the validation note
    (`streamDirRead`): nginx reads it there, and its test refuses every stream in it.
- **`htpasswd.go`** does bcrypt in process — `htpasswd` lives in apache2-utils, is not installed on a host
  running nginx, and would put the password in a world-readable argv. Changing a password is the same
  POST as adding a login (it replaces the user's line); the panel's 20-character generator runs in the
  browser, so the password crosses the wire once, in that POST body, and is never stored or audited.
- **`access_lists.go` — one list, every site that includes it.** An office range or a VPN in front of ten
  sites was ten copies of the same lines and ten edits when it changed. A list is
  `<JD_NGINX_DIR>/jd-access/<name>.conf` (name `[a-z0-9][a-z0-9_-]{0,62}`), holding only `satisfy`, the
  `auth_basic` pair and `allow`/`deny` — legal in http, server and location context — and a site takes it
  in with `include <path>;` wherever it wants the restriction. `ValidateAccessList` writes every address
  in `netip`'s canonical spelling, refuses `all` (a non-empty allow list is always closed with `deny all`,
  denials written first, as the site form does), a range with bits past its length (nginx would take
  `10.0.0.5/8` as `10.0.0.0/8` with a warning), a repeat (`10.0.0.1` and `10.0.0.1/32` are one), a zone,
  more than 512 entries a side, a prompt with quotes, `$` (nginx expands variables there), control
  characters or the word `off` (which turns the password off), and `satisfy any` without both an allow
  list and a password file (with no allow list every address passes and the password would be moot).
  `satisfy` is written only when a list has both addresses and a password, so a list adds no directive a
  site might already set. The password file is a `jd-auth` file by name and must exist when saved. Read
  back, a file with anything else — another directive, a block, a password file outside `jd-auth`, rules
  the form would reorder (compared directive by directive with its own rendering) — carries
  `HandWritten` with why, and a named password file that has gone is `AuthFileMissing`. `UsedBy` is every
  site in the listing (`nginxVHosts`, enabled or not) whose file includes the list at any depth, directly
  or through a file it includes (one level, as the listing follows a snippet), by absolute or relative
  path, `./`/`../` or glob. `SaveAccessList` writes, runs `nginx -t` with the list in place — so it is
  tested with every site that includes it — and on a refusal puts the previous bytes back (or removes a
  new list) and returns a `*RefusedError` whose lead says whose error it is: "nginx refuses <name> where
  a site includes it" (the error is in the list's own file: a site that already sets `auth_basic`, an
  include where the directives are not allowed), "nginx refuses the configuration with this change to
  <name>" (in a site's file), or "nginx already refuses the configuration without this change"; a passing
  save is recorded and reloads nginx inside the same hold of the service lock. `DeleteAccessList` is
  refused while any site includes the list (`AccessListInUseError`, naming them and which are disabled —
  a disabled site would fail its next enable), removes it, tests, and puts it back if nginx still reads it
  from somewhere the listing does not follow (nginx.conf, a snippet of a snippet); it keeps
  `<name>.conf.bak`, which the listing and nginx's `*.conf` skip, and reloads nothing, since nothing nginx
  loaded changed. Routes (`api/handlers_proxy_access_lists.go`, `mountAccessListRoutes`, mounted from
  `mountVHostRoutes` at `/proxy/access-lists`, all `system.admin` like the password files they name):
  `GET /` → `{dir, clientAddress, lists}`, where `clientAddress` is `httpx.ClientIP` for the page to check
  each list against; `PUT /{name}` with the spec and `overwrite` (false for a new list, 409 `exists` if
  one has the name) → `{list, validation, reloaded, reloadError?, reload?}`, 422 `invalid_config` with the
  lead and nginx's first error as the message and the test as `raw`, 400 for a spec it refuses; `DELETE
  /{name}` (destructive, ordinary confirmation) → 204, 409 `in_use`, 404. Audited as
  `proxy.accesslist.save` (entry counts, password file, satisfy, created, the sites it reached, reloaded)
  and `proxy.accesslist.delete`, refusals included. `TestLiveAccessListDecidesWhoGetsIn` drives a running
  nginx through each save: an allowed address, a denied one, a password, an IPv4 client against an
  IPv6-only list — and that under `satisfy any` a correct password also opens a location the site closes
  with `deny all`, which the form says.
- **`route_resolve.go`** answers "which site answers this URL": `ResolveRoute(tree, url)` replays nginx's
  precedence over `NginxTree(EffectiveConfig())` — the address:port group (a block on a specific address
  takes every request arriving there; a server without `listen` is `*:80`), `ssl` on that group, then
  server_name (exact, `.name` also exact, longest `*.` wildcard, longest `.*` wildcard, first regex,
  `default_server`, first block), server-level `return`, then locations the way
  `ngx_http_core_find_location` recurses (`=`, longest prefix, its nested locations, `^~` stopping that
  level's regexes, regexes in order, the 301 to a trailing slash for a `*_pass` prefix), then what answers
  (`return`, a `*_pass`, or files under `root`/`alias` with `try_files`/`index`). Each step carries its
  file:line; a `rewrite`, an `if`, or a regex Go's RE2 cannot compile marks the answer `certain: false`
  rather than guessing. `GET /proxy/resolve?url=` (`handlers_proxy_resolve.go`, mounted in
  `mountVHostRoutes`, `system.admin`, read-only, nothing is sent to the URL) → `RouteResolution`; 400 for
  an unusable URL, 409 `config_unreadable` when `nginx -T` fails.
- **Who may reach a URL from one address** (`effective_access.go`, `GET /proxy/resolve/access?url=&source=`,
  `system.admin` beside `/resolve`, read-only, nothing sent). `ExplainAccess` resolves the route, finds the
  server block and location chain again by file and line, and judges each layer for `source` (one IP
  address, as nginx sees the visitor; a range or a zone is a 400) in nginx's order: **Outside this host**
  (provider firewall, security group, CDN, NAT) is always `unknown`; then the host firewall, added by the
  handler with `netsec.JudgeFirewallFrom` (`netsec/reach_source.go`: JudgeFirewall's rule order for one
  source — the first inbound rule whose source holds the address and whose target covers the route's
  port decides, else the inbound default), judged for the source as the connecting address. A rule
  that may concern the port but cannot be judged for an address from its listing is passed over and
  returned rather than dropped, as `JudgeFirewall` drops it: a source that is not an address or a
  range, a ufw rule
  limited to an interface (`443/tcp on tailscale0`, as `ufw allow in on tailscale0` prints it) or
  written as a profile the host does not define (a defined one is read through `ufw app info`), an
  iptables rule limited to an input interface other than loopback, carrying a match other than its
  ports, a connection state, a comment and a REJECT's answer, or jumping to a chain that is not read
  (a state match without `NEW` never meets a new connection and is not one). When any of them acts
  otherwise than the verdict (`netsec.FirewallUndecided`), the layer is `unknown`, naming the rules and
  the verdict past them; otherwise the verdict stands and says how many were passed over
  (`TestJudgeFirewallFromOneSource`, `…ReadsUFWProfiles`, `…ReadsIPTablesListing`, on ufw's and
  iptables' own listings). Then the server block's rewrite-phase checks the site form writes — maintenance (`$jd_<id>_maint` with its `geo $jd_<id>_maint_ip` bypass list and the
  always-exempt ACME and page paths: 503 unless bypassed), Cloudflare only (`$jd_<id>_edge` over the
  Cloudflare geo file: 444 unless the address is one of Cloudflare's), the crawler block (by User-Agent,
  so an ordinary browser is let through); CORS preflight and hotlink checks answer particular requests
  and are not access; any other server-level `if` is a layer of its own that is `unknown`, never a pass.
  Then **Allowed addresses**: the allow and deny lines of the innermost level that sets any (a path's
  replace the site's, an included access list's are read where nginx includes them), first match,
  naming the deciding line and, for a line in `jd-access/`, the shared list as owner
  (`/proxy/sites#access-lists`); a `return` answers before addresses are checked, so the layer is
  `skipped` (an allow list does not restrict a redirect site). Then **Sign-in**: `auth_basic` and its
  user file, `auth_request` (single sign-on), each innermost and turned off by `off`; then the request
  limit in effect (its zone's rate, or the site's exempt list), and `ssl_verify_client` (`on` requires a
  certificate, `optional` lets the application decide). `satisfy any` is applied as nginx applies it: an
  address the allow lines refuse can still sign in, one an `allow` line matches is not asked, and one
  no line names is declined rather than allowed, so it is still asked (an allow list without `deny all`
  under `satisfy any` lets nobody past the password but the listed addresses). The verdict is
  `refused` (naming the refusing layers), `unknown` (a layer, or a route `certain: false` for anything
  but the judged server-level ifs), `credentials` or `admitted`; `no-route` where nothing answers the
  URL. The Sites page's **Which site answers a URL** takes an optional **From address** and draws each
  layer with its verdict, deciding lines and owner link above the route (`route-resolver.tsx`,
  `access-explain.ts`). Tests: `TestExplainAccessFollowsNginxsOrderAndInheritance`,
  `TestExplainAccessJudgesTheFormsOwnChecks` (rendered maintenance, Cloudflare-only, redirect and SSO
  sites), `TestExplainAccessEdges`, `TestExplainAccessDoesNotGuessAnIf`, `TestJudgeFirewallFromOneSource`,
  `TestRouteAccessExplainsLayersForAdmins`, and `TestLiveAccessExplanationAgreesWithNginx`, where the
  host's nginx binary on a private prefix answers 127.0.0.1 exactly as the explanation predicted for
  an open path, a deny ahead of an allow, and `satisfy any` with and without the address listed.
- **`certbot.go`** issues, renews and revokes. `renewalScheduled` has its own field because it is the real
  story behind almost every expired certificate: not a forgotten renewal, a timer that stopped months ago;
  `CertbotState` answers it before reading the lineages, whatever they say. A cron file whose command
  tests `! -d /run/systemd/system` (Debian's and Ubuntu's `/etc/cron.d/certbot`) is no schedule where
  that directory exists: it stands aside for the timer, so a stopped timer there reads as nothing
  renewing, with the timer to turn on. Issuance defaults to a test
  run in the UI (the real limit is five failures an hour), and a test run is `certonly --dry-run` — the
  whole exchange, nothing saved (`--staging` used to write a lineage holding an untrusted certificate and
  make the real issuance that followed a "no action taken" no-op). A real issuance whose names already
  hold a test certificate (a lineage renewed from a staging authority, or one a staging authority
  signed) adds `--force-renewal`, unless the configured directory is itself a staging one; with
  `JD_ACME_DIRECTORY` both name `--server`, except Let's Encrypt's production directory, which is
  certbot's default and is left unnamed (certbot swaps a `--dry-run` to staging only when the server
  is its default spelled exactly). The page offers that
  issuance on a test lineage in place of Renew, since certbot renews from the authority the lineage's
  configuration names. Such a job is titled "Replacing the test certificate for …", audited with
  `replacesTestCertificate`, reloads nginx for the enabled sites naming the certificate (see
  reloading after renewal, below) and ends by saying what each answered. `ServedCertificates` (`cert_served.go`,
  `GET /certificates/served?path=`, system.admin) asks each such site over a TLS handshake on its
  first `listen … ssl` — a wildcard address on loopback, a named one only when it is this host's, a
  PROXY header first behind `proxy_protocol`, with a server name the certificate covers — and
  compares the leaf with the file's. Certificate details (`cert_detail.go`): `GET
  /certificates/detail?path=` (read) answers 403 for a path `listCertificates` does not list, and
  returns the file's chain in file order (subject/issuer DNs, SANs, serial, SHA-256/SHA-1, key type,
  AIA/CRL, embedded SCT count) with a verdict judged offline against the system roots (`complete`,
  `wrong-order`, `missing-intermediate` when the top issuer publishes an AIA URL, else `private-ca`,
  `self-signed`, `invalid`), plus the key's path (the site's `ssl_certificate_key`, certbot's
  `privkey`, or a sibling `privkey.pem`), mode, owner and whether it matches — never the key.
  `GET /certificates/history?name=` (system.admin: it reads the audit trail) merges certbot's
  `archive/<name>/certN.pem` versions (only for a name with a renewal conf), `certificates.*` audit
  entries whose target names it, and renewal failures from the renewal record. `POST
  /certificates/decode` (read, 256 KiB) decodes a pasted PEM chain or CSR in memory and refuses to
  read a private-key block. Downloads and export (`cert_export.go`): `GET
  /certificates/download?path=&part=` (read) answers `fullchain`, `cert` (the leaf) or `chain` (the
  rest) of a listed file as PEM, 403 for an unlisted path; `POST /certificates/export` (system.admin,
  ordinary export confirmation, `{path, format: key|pfx, password, legacy}`) returns the matching key as PEM
  or a PFX (OpenSSL 3's AES-256/PBKDF2 default, or 3DES/SHA-1 with `legacy`; password at least 8
  characters) — see invariants.md for the confirmation policy and how the password stays out of argv. Certificate coverage (`cert_coverage.go`) maps served names to their certificates through
  `GET /certificates/coverage`. The hygiene findings endpoint and its recommendation rules are removed
  in 0.7.5. Existing certificate issuance, renewal and lineage controls retain their own review and
  permission boundaries.

- **The proxy routes are mounted per area**, each from its own file beside its handlers, and composed in
  `mountProxyRoutes` (`api/handlers_proxy.go`): `mountEngineRoutes` (status, the raw config editor,
  validate, test, reload; `handlers_proxy_engine.go`), `mountVHostRoutes` (the listing, what nginx has not
  loaded, the enable switch and the removal of a stray link; `handlers_proxy_vhosts.go`, which also mounts
  `mountAccessListRoutes` at `/proxy/access-lists`) and `mountProxyInsightRoutes` (`handlers_proxy_insights.go`, which
  mounts the live metrics from `handlers_proxy_metrics.go`) inside `/proxy`; `mountSiteBuilderRoutes` (`handlers_proxy_sites.go`) and `mountSiteOpsRoutes`
  (`handlers_proxy_siteops.go`) inside `/proxy/sites`; `mountStreamRoutes` (`handlers_proxy_streams.go`),
  `mountAuthFileRoutes` (`handlers_proxy_auth.go`) and `mountProxyToolRoutes` (`handlers_tls.go`, where
  the whole subtree is `system.admin` because every tool probes a caller-chosen destination) at
  `/proxy/streams`, `/proxy/auth-files` and `/proxy/tools`; `mountCertificateRoutes`
  (`handlers_certificates.go`) and `mountTLSRoutes` (`handlers_tls.go`; the watch list's handlers stay in
  `handlers_domains.go`) inside `/certificates`; and `mountPortRoutes` (`handlers_ports.go`) at `/ports`,
  which chi serves with and without the trailing slash, with `/ports/meta`, `/ports/firewall` and
  `/ports/history` (`handlers_ports_history.go`) beside it.
  `TestProxyRoutesKeepTheirPaths` pins every path, method and gate as they stood before the split. The whole group runs `withProxyActor`, which puts the
  signed-in account on the context for the change record below. Background work and state the proxy
  pages keep beyond `proxysvc.Service` go in `api/modules_proxy.go` (`initProxyExtras`, run last in
  `initModules`; `startProxyExtras` from `Start`; `stopProxyExtras` from `Shutdown`, which also runs for a
  server never started), and their tables in `store/schema_proxy.go`'s `proxySchema`, which `Open` applies
  after `applyAddedColumns` and `TestProxySchemaIsAdditive` holds to `CREATE … IF NOT EXISTS`. A table
  created there that later gains a column through `addedColumns` has to move into `schema` in the same
  change, since `applyAddedColumns` runs first on a fresh install.
- **What the engine's own test says, not only whether it passed.** `runTest` (`testrun.go`) fills
  `ValidationResult.Diagnostics` (`diagnostics.go`: level, message, and file and line where nginx names
  them) and `Warnings`, the warn-level count — nginx exits 0 through a conflicting server name it is
  "ignoring", and that site then never serves. nginx writes a test's messages as `nginx: [warn] … in
  /path:12` when it can open its startup error log (root on the host) and as the timestamped error-log
  line when it cannot; both are read, the path whole from the last ` in /` outside the quotes nginx
  puts round what it repeats, so a file named with a space keeps its place and a message naming two
  places (`used in /a.conf:1 and in /a.conf:2`) opens the second, where nginx stopped.
  `ParseCaddyDiagnostics` reads `caddy validate`'s JSON warnings and its `Error:` line, best effort,
  taking the position after `, at ` before any other path-like text so an upstream URL is never read
  as one. A diagnostic's file is named with its symlinks resolved, the form
  `allowedPath` gives the file being edited, so a Debian site's error names its sites-available file
  rather than the sites-enabled link nginx included. Caddy is validated against a copy in a private
  directory under `/tmp/just-dashboard`, the one temporary directory docker-compose mounts at the same
  path in the container and on the host, where caddy runs through nsenter — a copy in the container's
  own /tmp was invisible to it, so every Caddyfile check failed. That root must be a real directory the
  dashboard's user owns and nobody else can write to, or nothing is written or run. `validateCaddy`
  replaces the copy's name with the file's throughout the result; `Validate` holds a Caddy path to
  the proxy's directories as it does an nginx one.
- **A test that gives no verdict is not a refusal.** `runTest` tells the engine's answer (exit 0, or
  the 1 both engines refuse with) from a run that never gave one: out of its 30 seconds, stopped with
  its caller's context, ended by a signal, not started, or exit 126/127 from nsenter or `docker exec`
  failing to reach the binary; for the ingress, also docker's own "Error response from daemon".
  Those are an `*UnfinishedTestError` (`ErrTestUnfinished`, unwrapping to the context's or exec's
  error), which `mapProxyError` answers as `test_unfinished` — 504 when time ran out, 502 otherwise —
  retryable, with the output as `raw` and no test beside it; a reload adds "so nothing was reloaded"
  and a start or restart "so nginx was not started". It is never kept, and a reload, start or save it
  guards does nothing (a staged candidate is put back). `Test` and `Reload` run their test under
  `context.WithoutCancel`, so a tab closed mid-test no longer cuts it off; `WaitDelay` stops the wait
  for a grandchild (nginx under nsenter, a shim's docker) that holds the output open after the kill.
  `runValidator` is `runTest` for the other callers (sites, streams, deployment routes), where a test
  that did not finish still reads as a failure and refuses what it guards.
- **The engine's last test outlives the toast.** Every test of the files on disk is kept per engine
  kind as a `TestRecord` (`lasttest.go`: kind, `checkedAt` — when the test began — and the result):
  `Test`, the test `Reload` runs first (passed or refused, for nginx, a host Caddy and the ingress),
  the one a start or restart runs under `WithTestedConfig`, and the second test `WriteConfig` runs
  with an nginx file in place when it passes (one it refuses is put back, so the files are those the
  last record describes). A candidate `Validate` stages and restores is not a test of the files on
  disk and is not kept. A test begun before the kept one never replaces it, and every reader gets its
  own copy. `GET /proxy/test/last?kind=` (admin, as the test itself — its output quotes the
  configuration) answers the record or 204 when none has run since the dashboard started; it is held
  in memory, so a restart forgets it rather than vouching for files it did not see. Test config
  opens this retained result without running the test again. The automatic overview finding was
  removed in 0.7.5.
- **A conflicting server name is placed at the sites that claim it.** nginx names no file for its
  commonest warning (`conflicting server name "a.test" on 0.0.0.0:80, ignored`).
  `PlaceNameConflicts` (`nameclaims.go`) reads the effective configuration and gives such a warning
  `claims`: every http server block whose `server_name` names it on that address, in nginx's reading
  order, at its `server_name` line with symlinks resolved — the first serves the name, and each after
  it is `ignored`. A `server_name` in a file the block includes (a snippet several sites share, whose
  one line would name every site) places the claim at the block's own `server` line instead, with the
  snippet's line as `nameFile`/`nameLine`; claims that still land on one file and line (one file
  enabled under two names, or included twice) cannot say which site is served, and are not given. A listen's address is read as nginx prints it (`80`, `*:80` and `0.0.0.0:80` are
  one; no listen is `*:80`, or `*:8000` for an unprivileged nginx). nginx warns once for each block
  after the first, so claims are given only when there are exactly one more than the warnings about
  that name and address, and not at all when a claimant listens on a host name: a block the tree
  cannot see would put the wrong site first. `POST /proxy/test`, `GET /proxy/test/last`,
  `POST /proxy/reload` (passed, which its toast's Show opens, or refused) and a refused start or
  restart place against the configuration as it is now, within three seconds (the dump waits for the
  service lock, which the start's test has released by then); the record itself is kept as nginx
  wrote it.
- **The Configuration page reads the nginx directory as files.** `GET /proxy/files` (every
  signed-in account, as `GET /proxy/config`) is `ConfigFiles` (`config_files.go`): every file three
  folders deep under the nginx directory with its size, modification time and kind (`main`, `link`,
  `password`, `file`), editor and package-manager backups left out unless an include reaches them.
  Whether nginx reads a file is worked out from the disk by following nginx.conf's includes as nginx
  does (relative to the main file, glob(3) order, inside any block), not from `nginx -T`, which prints
  nothing for a configuration that fails its test; a file that does not parse stops the walk and
  `includesKnown` is false with the `problem` placed. A password file is listed as `protected` and
  never read; a file the dashboard wrote carries its marker and is `managed`. `GET /proxy/effective`
  (admin, gated with the test because it runs the host's nginx) answers `nginx -T` file by file with
  `checkedAt`, each link's `target`, and every directive placed with its enclosing blocks
  (`PlaceDirectives`) for a search by directive; a configuration nginx refuses is a 422 carrying the
  test (`DumpRefusedError`), and one held behind the service lock past 20 s is a 503 `busy`. A
  refused `PUT /proxy/config` now carries its test beside the error, as a refused reload's does.
- **Server-wide settings and the config linter** (`settings.go`, `directives.go`, `lint.go`,
  `api/handlers_proxy_settings.go`), all `system.admin` because they run `nginx -T`. `GET /proxy/settings`
  reads server_tokens, client_max_body_size, keepalive_timeout, gzip, ssl_protocols and
  server_names_hash_bucket_size from http and worker_connections from events in the effective tree: value,
  the file:line that sets it or nginx's default (ssl_protocols' default follows `nginx -v`: TLSv1.2 TLSv1.3
  from 1.23.4, older or unreadable versions TLSv1 TLSv1.1 TLSv1.2), how many blocks inside http set it
  again, a level, advice and a recommended value where there is one. worker_connections is judged against
  `worker_rlimit_nofile` or the running master's `Max open files` from `/proc/<pid>/limits` (pid file from
  the `pid` directive or `/run/nginx.pid`). `POST /proxy/settings/preview {changes}` and `PUT
  /proxy/settings {changes, reload}` (audited `proxy.settings.write`) take a closed list of names, each value
  checked by a strict per-directive pattern; `SetDirective` replaces only the words between the name and
  its `;` (tokenised as nginx does, a trailing comment kept, a comment between the words or a directive set
  twice in one block refused) in the file that sets it — found again in the file on disk by line, refused if
  it moved — or adds it to `conf.d/jd-http.conf` (created with the dashboard marker) when nginx.conf's http
  includes that path, otherwise to the main file's http or events block. Each file goes through
  `WriteConfig`; an edit to a file a `/var/lib/dpkg/info/nginx*.conffiles` lists is flagged `conffile`.
  `GET /proxy/lint` answers `Lint(tree)`: add_header dropping outer headers, a prefix location without `/`
  aliased to a directory with one, proxy_pass with a variable and no resolver in scope (IPs, unix sockets and
  upstream names excepted), `$http_host`, allow without `deny all`, return where deny/auth_basic/auth_request
  apply, stub_status neither guarded nor on a loopback-only server, `listen … http2`, SSLv2/3 or TLSv1/1.1,
  two servers with the same name on the same address, and `if` in a location holding anything but return or
  rewrite … last. Files `EffectiveConfig` leaves out (certbot's options, modules) are not linted.
- **The configuration nginx actually loads.** `EffectiveConfig` (`effective.go`) runs `nginx -T` through
  `hostexec` under the service lock — so it never dumps a candidate `Validate` has staged — splits it into
  `ConfigFile`s byte for byte (`ParseEffective`, which takes a `# configuration file` line as a file only
  when it names an absolute path), keeps only the files `ReadConfig` would show — no password file, and
  nothing that resolves outside the proxy's directories, so certbot's options file or a module's
  `load_module` file is left out with its directives — and caches the result for ten seconds; every
  change the service makes and every reload forgets it at once. A main configuration outside
  `JD_NGINX_DIR` is an error rather than a tree rooted at the wrong file. The service lock can be held
  for minutes by a certificate order, so a reader waits only as long as its own context, and readers
  that queue behind one pending dump share it. `NginxTree` (`nginxconf.go`) parses those files the way nginx tokenises them
  (quotes and their escapes, comments, `${var}`, nested blocks) into `Directive`s carrying their file,
  line and enclosing contexts, with each `include` replaced in place by the files its glob matched, sorted
  and without dotfiles as glob(3) would, `[!…]` included. nginx prints a file under the path it was
  included by, `./` and `../` left in, so includes and printed paths are compared cleaned; the files are
  indexed once by path and directory, which keeps thousands of sites linear.
- **Every change to a configuration file is offered to a `ChangeRecorder`** (`changes.go`) once it is
  committed — `WriteConfig`, `SaveSite` and deployment cutovers through `applySiteLocked`, `DeleteSite`,
  `SetVHostEnabled`/`ToggleVHost` (which now take the service lock like every other change, resolve the
  site file as the writes do, and record nothing for a toggle that leaves the links as they were or that
  nginx refused), `RemoveVHostLink` (recorded as a disable of the file behind the link, or of the link
  itself when it points at nothing), `ApplyStream`,
  `DeleteStream`, the live metrics' status server going in and out, and a deployment route's restore —
  with its prior content and the actor
  `WithActor` put on the context (empty for a deployment or a background loop). Password files are never
  recorded, a site file that resolves outside the proxy's directories is recorded without content, and
  the htpasswd writers do not call it; a recorder that fails is logged and the change stands.
- **Config history** (`api/proxy_revisions.go`) is that recorder: `proxy_config_revisions` keeps each
  file as a change left it (`existed = 0` for a removal), the newest 50 per path, nothing over 1 MiB.
  When the before-state differs from the newest revision kept, it is stored first as `outside` (changed
  outside the dashboard); a file with no history keeps its before-state as `baseline`. Toggles record
  the site file's content under `enable`/`disable`; the recorder cannot tell the site form from the raw
  editor, since both reach it as a `write`. Routes, all `system.admin` because a revision is a file's
  full content: `GET /proxy/history/files` (each file's newest revision, and `drift` against disk),
  `GET /proxy/history?path=`, `GET /proxy/history/{id}` (with the previous revision and the file now),
  and `POST /proxy/history/{id}/restore {reload}`, which goes through `WriteConfig` — tested in place,
  a 422 with the test on refusal, the file untouched — after `ReadConfig` refuses a password file or a
  path outside the roots; audited `proxy.history.restore`, recorded as `restore`. A removed site's file
  is restorable from the revision before its removal; its sites-enabled link is not recreated.
- **Served against disk certificates** (`drift.go`, `api/handlers_proxy_drift.go`). `GET /proxy/tls/drift`
  is readable by every signed-in account, like the sites list it is drawn from. For each TLS `server`
  block of an enabled nginx site (parsed with `ParseNginxFile`) it handshakes with the block's first
  `ssl` listen, sending its first exact `server_name` as SNI with `InsecureSkipVerify`, and compares the
  leaf's SHA-256 with every `ssl_certificate` the block names: `ok`; `stale` (another certificate that
  covers the name: renewed, not reloaded); `mismatch` (another site's file, named in `servedBy`, or one
  that does not cover the name); `unreachable`; `skipped` with the reason (no exact name, a certificate
  chosen by a variable, a unix or named listen, an unreadable file). Only this host's addresses are
  dialled — a wildcard listen becomes `127.0.0.1`/`::1`, a specific address must be loopback or one of
  the host's interface addresses — and never anything from the request. The result is cached five
  minutes and dropped on any configuration change or reload; `?refresh=1` runs it now and needs
  `system.admin`. The overview polls it every five minutes, shows stale and mismatch as findings, and
  gives an administrator's stale finding Reload nginx, after which it asks for a fresh check.
- **Live request metrics come from nginx's stub_status** (`stubstatus.go`, `api/handlers_proxy_metrics.go`).
  `GET /proxy/metrics` is open to every signed-in account — nginx's counters are no secret, and the
  sampler reads only the address in the dashboard's own file, so no caller can aim it — and
  `PUT /proxy/metrics {enabled}` is the switch (`system.admin`, audited as `proxy.metrics.enable` and
  `proxy.metrics.disable`). Switching off is not destructive: it stops no site and removes only the
  dashboard's own file. The switch's state is that file, `conf.d/jd-status.conf`, whose first line says
  `Just Dashboard owned` (the mark the site listing leaves `jd-*` files out by once site discovery
  checks it; until that change lands, a conf.d-only host lists the file as a site): one `server` on
  `127.0.0.1:<port>` with `location = /jd-status { stub_status; allow 127.0.0.1; deny all; }`,
  `access_log off`, `keepalive_timeout 0` and a 404 for every other path. The port is the first from
  19081 that `portalloc` can bind on 127.0.0.1 — the backend shares the host's network with nginx — and
  a file already in place keeps its own. Switching on writes the file, runs `nginx -t`, checks with
  `nginx -T` that nginx loads conf.d at all (a test passes over a file nginx never reads), reloads, and
  waits up to ten seconds for the first reading, because nginx binds after the reload command has
  returned and a port it cannot bind (taken since, or refused by SELinux) is only in its error log. A
  step that fails puts the file back, and after a reload reloads again without it, so the switch is on
  and answering or off: 422 `invalid_config` with the test beside it, 409 `metrics_unavailable` (no
  conf.d, conf.d not included, a file at the path without the marker, which both directions leave
  alone), 502 `reload_failed` or `no_answer`. The change runs to its end if the browser goes away.
  Switching off removes the file only while the configuration tests clean without it; a reload that then
  fails leaves it removed, and the 502 says, from whether the address still answers, either that nginx
  serves the status server until its next reload or that nothing answers there (nginx is not running);
  the client reads that as switched off with a warning. A dashboard-owned file whose listen names no
  `127.0.0.1` port the sampler can read (parameters after the port are fine) reports as on with that
  reason as its error rather than as on with nothing read. The
  `StatusSampler` (started from `startProxyExtras`) reads the file's address every five seconds into an
  in-memory hour — the file is the switch, so one removed by hand stops the series — trimmed on every
  poll, failed ones included, and counted only back to an hour before the report's `at`, so readings that
  fail for longer than an hour leave no hour behind — and
  `Report(epoch, after)` sends a client only the readings after its cursor, one a poll rather than 720; a
  new epoch (switched off and on, a moved port, a restarted dashboard) sends the whole series. Rates come
  from counter deltas: none for the first reading, after nginx restarted (its counters fall) or across a
  gap of more than four intervals, and never negative. The sampler's own connection and request are left
  out of every figure — keep-alive is off on both sides, so they are exactly one of each — and `dropped`
  is accepted minus handled, the connections nginx turned away at `worker_connections`.
  `TestLiveStatusServer` runs the whole switch against the host's nginx binary on a private prefix.
- **Upstream health** (`proxysvc/upstreams.go`, `api/handlers_proxy_insights.go`): `UpstreamTargets`
  reads every `proxy_pass`, `grpc_pass`, `fastcgi_pass`, `uwsgi_pass` and `scgi_pass` from the
  effective tree (http and stream), expanding a named upstream block server by server (`down` servers
  left out, unix sockets included); a target with a `$variable` is `dynamic` and not checked.
  `CheckUpstreams` connects to each distinct address once, eight at a time, 1.5 s each, then sends
  `HEAD /` to http/https ones (certificate unverified, as nginx does by default). A unix socket is
  connected to through `hostexec.HostPath` (`/proc/1/root` + path), since the host's `/run` is not
  mounted in the container. States: up, refused, timeout, unresolvable, missing, error, dynamic; a local
  TCP port that answers names its process from `ListListeners`. `GET /proxy/upstreams` is open to every
  signed-in account because destinations come only from the config on disk, and it serves the last check
  for 15 s with concurrent readers sharing one, so polling cannot amplify outbound traffic.
  `POST /proxy/upstreams/check` (system.admin, audited `proxy.upstreams.check`) skips the cache. 503
  `no_nginx` on a host without nginx, 503 `invalid_config` when `nginx -T` refuses.
  **Pools** (`upstream_pools.go`): the same report carries `pools`, the destinations grouped as nginx
  spreads them — one per upstream block a route forwards to (its method: round-robin, `least_conn`,
  `ip_hash`, `hash <key>`, `random`; `keepalive`; every `server` with weight, `max_fails`,
  `fail_timeout`, `backup` and `down`, the down ones listed but never dialled) and one per direct
  address — each with the routes and site files that use it. `balancing` says who spreads the requests:
  `native` for a block of several servers nginx chooses among, `native-dns` for one server named by a
  host name that resolves to several addresses from here now (nginx resolves it when it loads and
  balances across every address; what it resolved then may differ), and `single` for one address,
  whose spreading — a provider's load balancer, a floating address, a Kubernetes Service — nginx
  cannot see and whose failure is the route's; `provider` names the managed balancer a single
  endpoint's host-name suffix suggests (`.elb.amazonaws.com`, `.run.app`, `.svc.cluster.local` and a
  dozen more), as a hint and never proof. Each member carries the check's state and, from nginx's own
  error logs over the last hour (`requestErrorLogs`: every `error_log` the loaded tree names, main,
  http and server, confined to `/var/log/nginx` like the site logs, or the default there), what nginx
  met at it by kind — `refused`, `timeout`, `reset`, `closed`, `disabled` (nginx setting the server aside
  after `max_fails`, logged at warn, so absent where the log level is error), `other` — and when it
  last did; a member written as a name is matched under each address it resolves to, and nginx's
  `no live upstreams` (every server set aside) is counted on the block (`noLive`). `verdict` is what a
  visitor meets: `serving` (every primary up), `degraded` (some), `on-backup` (no primary, a backup),
  `down`, or `unknown` (nothing checkable). `evidence` names the logs read, the window's start and
  whether a log's 8 MB bound cut into it. A site's page draws each of its pools as a plain
  **Balancing** panel (`site-balancing.tsx`, `upstream-pools.ts`): who balances and how, the verdict,
  `no live upstreams`, and per server its role, check and what nginx logged. Tests:
  `TestUpstreamPoolsSayWhoBalances`, `TestUpstreamPoolVerdicts`, `TestUpstreamPoolsCountWhatNginxLogged`,
  `TestRequestErrorLogsStayInsideTheLogDirectory`, and `TestLivePoolOutcomesComeFromNginxItself`, where
  the host's nginx binary balances a private pool with a refusing member and the report reads the
  refusals from nginx's own log.
- **Site traffic and error log** (`proxysvc/site_logs.go`, `proxysvc/site_errors.go`,
  `api/handlers_proxy_traffic.go`): `SiteLogsFor` finds an nginx site by its listed name (never a
  joined path) and reads the `access_log`/`error_log` its file sets at file or server level;
  `confineLog` refuses a relative path, a `$variable`, and anything that is not inside `/var/log/nginx`
  both as written and after symlinks, and `syslog:`/`off` are reported as reasons. A second
  `accesslog.Store` (`proxyExtras.siteTraffic`) is keyed by that log path, its opener re-confining it.
  `parseCombined` also reads trailing `rt=` (latency) and `host=` when a `log_format` appends them; the
  stock `combined` the site form writes has no timing, so p95 shows only for such formats. All reads,
  open to every signed-in account like a deployment's request record: `GET /proxy/traffic` (each site's
  last hour with its minutes as `points` — every nginx site from the log its file names, then every
  route on the Docker Caddy ingress, `engine: "caddy-ingress"`, from the shared request record its
  deployment's Logs page reads, listed by `IngressRouteNames` in one `find` inside the container;
  each record refreshed at most every fifteen seconds, which costs only the bytes appended since), `GET /proxy/traffic/{name}?window=15m|1h|6h|24h|7d`
  plus the deployment filter params (a `RequestWindow` with `logs`), `GET /proxy/traffic/{name}/tail?after=`
  (polled tail from the window's cursor), `GET /proxy/traffic/{name}/export` (CSV), and
  `GET /proxy/errors?site=&window=`: the last 8 MB of the site's error log, or of nginx's (the http
  block's `error_log`, then main's, then `/var/log/nginx/error.log`) narrowed to the site's
  `server_name`s by each line's `server:`/`host:`, grouped by level, message with quoted values and
  numbers replaced, and upstream; recognised lines carry a plain title and advice (refused upstream,
  timeouts, too-large bodies with the size to raise `client_max_body_size` to, missing files, TLS
  handshakes and so on).
- **Proxy alerts** (`api/proxy_alerts.go`, `api/handlers_proxy_alerts.go`; tables `proxy_alert_rules`,
  `proxy_alert_state`, `proxy_alert_events` in `store/schema_proxy.go`): rules of kind `cert_expiring`
  (`params.days` from 21/7/3/1), `cert_expired`, `renewal_failed` (the last certbot run or its reload
  hook failed, with an unreadable or running renewal left unjudged), `served_drift` (a TLS site serves a
  stale or mismatched certificate; unreachable and skipped sites are left unjudged), `engine_down`
  (the engine's systemd unit not active/reloading/activating), `upstream_down` (`params.minutes`, 5–1440;
  the `/proxy/upstreams` check,
  so only addresses nginx's own config names are dialled), `watch_unreachable` / `watch_untrusted`
  (`CheckEndpointAt` on each current `watched_endpoints` row, using its pinned IP when set, with the IP
  in the alert subject so two origins of one name stay distinct), `watch_grade_below` (`params.grade`
  defaults to A; A+, A, B or C allowed; a full `ScanTLSWith` of each current watched endpoint runs
  only while the rule is enabled, up to four at once with a 60 s per-endpoint budget, following pinned
  addresses and STARTTLS ports; watch lists over 12 endpoints rotate in two-pass batches so the hold
  can mature while each pass stays bounded; an unreachable or unfinished scan is unjudged, and a grade
  below the chosen minimum must persist through two checks and a minute) and `site_errors`
  (`threshold` %, `minutes`, `minRequests` over the site's access log through
  `proxyExtras.siteTraffic`). A pass runs every 5 min (`proxyAlerts.Start`
  from `startProxyExtras`; clock, observer and deliverer are fields for tests). State is one row per
  (rule, subject, level); a transition is told once — firing when the subject first holds (engine and
  unreachable: two passes and a minute; upstream: two passes and the rule's minutes), recovered once the
  reading no longer finds it — one message per subject at its most severe level; a source that cannot be
  read, or a subject it cannot judge, keeps its state. Messages go through `DeliverNotification` as
  `proxy.alert.firing` / `proxy.alert.recovered` with `NotificationEnvelope.Proxy`, rendered by
  `renderProxyAlert`; a rule with no channels tells every enabled one, a paused channel gets nothing, a
  muted subject is followed but not told, and every transition is kept in the newest 500 events. Routes,
  all `system.admin` and audited (`proxy.alerts.*`): `GET /proxy/alerts` (rules, firing/pending/muted
  subjects, last 100 events), `POST /proxy/alerts/rules`, `POST /proxy/alerts/test {channelId}` (a
  `[test]` message to one existing channel), `POST /proxy/alerts/evaluate` (a pass now; holds still
  apply); behind `destructive`: `PUT /proxy/alerts/rules/{id}` (can pause; new terms or a pause clear the
  rule's state except mutes), `DELETE /proxy/alerts/rules/{id}`, `PUT /proxy/alerts/mutes {ruleId, subject,
  muted}`. The ordinary watched-check schedule still records a handshake and certificate; only an
  enabled grade rule runs the full grader. The operator chooses the minimum expected grade. The
  rules are read and edited on their own page, `/proxy/alerts` (`components/proxy/alerts-page.tsx`),
  and the overview's identity bar links there with the firing count from the same `GET /proxy/alerts`.
  `TestProxyAlertsRequireAdminAndTellEachTransitionOnce` covers the API gate, audit, firing, unreadable
  source, and recovery; `TestProxyAlertReadingsForRenewalAndServedCertificates` covers the two additional
  readings; `TestProxyAlertsCheckCurrentPinnedWatchEndpoints` and
  `TestWatchedGradeRuleScansPinnedTargetsAndJudgesOnlyCompletedGrades` cover current watch rows,
  pinned origins, STARTTLS and grade decisions; `TestWatchedGradeRuleRotatesLargeWatchListsAfterTwoPasses`
  covers the bounded schedule.
The proxy overview retains native source read errors and retry controls, including a status read that
fails after answering. It no longer folds observations into an attention list or suggests domain/configuration
changes. The finding snooze routes are removed. Existing `proxy_finding_snoozes` schema/migration entries
remain so upgrades preserve the database. Certificate hygiene findings and `/certificates/findings`
are removed; certificate inventory, issuance, renewal, coverage and requested diagnostic tools remain.
nginx Settings returns current values and constraints without levels, advice or recommended values.

## Streaming, jobs, secrets, agent mode

**`internal/wsx`** wraps gorilla/websocket: origin check on upgrade (a WS handshake is not subject to
CORS, so this is what stops a malicious page using the operator's cookie), serialised writes, ping/pong,
1 MB read limit, `Envelope{type, data, error, ts}` frames. **Server-side filtering is the rule** — log
grep and level filters apply before lines are sent. A container log line is capped at 256 KB
(`dockerx.maxLogLine`), so a container emitting a gigabyte without a newline cannot exhaust memory.

**`internal/jobs`** runs what takes minutes — certbot, package upgrades, sshd applies — which as ordinary
handlers held a request open for the length of a certbot run and left a dropped VPN meaning nobody knew
whether their SSH config had applied. It is deliberately **not** the compose runner's shape:
`RunComposeStream` owns its command and refuses to reconnect, because re-issuing the GET runs the command
again. A job inverts that — `context.Background()`, a ring buffer with a sequence per line, and
`Subscribe(id, after)` resuming from what the client already has — so closing the tab leaves the work
running and the transcript complete.

- `Manager.Start(spec, run)` returns immediately; the API answers `202`. `StartExclusive(prefix, …)`
  starts nothing while a job of that kind family runs and returns that job instead, checked and started
  under one lock. `Emitter` gives runners `Status`, `Line`, `Run`/`RunEnv` through
  `hostexec.CommandOnHost`, and `RunCmd` for a command the caller built on the side it chose. A slow subscriber is skipped rather than allowed to stall
  the command — the buffer is the record, the channel only the tail.
- **`RunDetached` is for work that must finish even if the dashboard does not** — today every package
  install, upgrade and removal (`api.runPackageCommand`). A child of the backend lives in its container's
  cgroup and dies when the container stops, and an apt upgrade that configures docker-ce stops it:
  dpkg was killed mid-configure and every later apt command refused with "dpkg was interrupted". So the
  command runs as a transient host unit, `just-dashboard-job-<id>.service`, via `systemd-run` with stdout
  and stderr written to `<data dir>/jobs/<id>.out|.err` rather than to a pipe (a dead reader would kill
  the writer with SIGPIPE). Every argument's `$` is doubled, because systemd expands `$VAR` and `${VAR}` in
  a unit's command line and `$$` is its literal dollar. The job tails the files every 200 ms and asks `systemctl show` once a second;
  cancelling or the timeout runs `systemctl stop` and still waits for the unit to end. A `<id>.json`
  record (job, shown argv, deadline, boot id) written after the unit starts lets `Manager.Adopt`, called
  at startup, re-register the job under the same id — the watching console reconnects to it — and follow
  the unit to its end, or replay a unit that finished while nothing watched. Failed units stay loaded
  until their code is read and `reset-failed`; a successful one is collected by systemd and reads as
  success. A different boot id means the server rebooted under the command, which is reported as a
  failure. Without root, a host systemd or the data directory at the same path on the host, the
  command runs as a child exactly as `RunEnv` would, and a failed `systemd-run` says so in the console.
  `JD_DETACHED_JOBS_LIVE=1` (as root) runs `detached_live_test.go` against the real host systemd.
- Bounded: 5000 lines a job, 64 KB a line, 50 jobs. `prune` runs on finish as well as on start, or a burst
  ending after the last `Start` sits over the cap until something else happens, which on an idle dashboard
  is never. A running job is never pruned.
- **Validation stays synchronous** — a bad email, a wildcard over HTTP, an sshd change that would lock the
  operator out — so a refusal answers the click rather than arriving a minute later as a failed job. That
  is why `certbot.go` exposes `IssueArgs`/`RenewArgs`/`RevokeArgs`, `netsec.PlanSSHSettings` is separate
  from `ApplySSHPlan`, and `updates.UpgradeCommand` returns an argv.
- `GET /jobs/{id}/stream` sends job, backlog, then batches every 120 ms. Subscribing after a job
  finishes still returns its retained backlog and closes the live stream immediately. Output delivery,
  disconnects and completion share the job lock so a closing subscriber cannot interrupt the runner.
  Cancelling is `service.control`.

**Secrets, four places, one goal.** `main.scrubSecretEnv` unsets boot secrets (`JD_` and `VPSD_`) once
consumed. `deploy.mergeEnv` strips every `JD_*`/`VPSD_*` from a deploy child's environment — the command
is content the repository owner controls — and is the half that keeps working when a new secret-bearing
variable is added later. `dbx` never puts a password in argv (`/proc/*/cmdline` is world-readable):
`PGPASSWORD` in the environment, a temporary defaults file for MySQL. `dockerx.RedactEnv` masks
credential-shaped container env, because that is where deployments keep secrets and container detail is
not a `system.admin` route.

**Agent mode** (`JD_AGENT_MODE` / `-agent`) swaps the human login surface for mutual TLS: no password
route, no session, no 2FA. `httpx.HubOnly` admits only the enrolled hub's certificate; `/agent/enrol` is
the one route reachable before enrolment, which is why TLS *asks* every caller for a certificate without
requiring one at the handshake and `HubOnly` enforces per route. The enrolment token is printed once per
boot while unclaimed. Feature routes are the same program either way. Not useful standalone yet.

## Configuration, version, release, self-update

**`internal/config`** resolves `JD_*` at boot and **fails closed**: no `JD_ALLOWED_CIDRS` with a
non-loopback bind is a startup error, a missing or malformed `JD_MASTER_KEY` is fatal. A `loader`
collects *every* malformed setting so `Load` refuses with the whole list rather than one error at a time.
`config.Env` falls back to the legacy `VPSD_*` prefix and `adoptLegacyData` picks up pre-rename
`/var/lib/vps-dashboard`. A handful of variables are **not** in `config.go`: `JD_LOG_LEVEL` and
`JD_BOOTSTRAP_USER`/`JD_BOOTSTRAP_PASSWORD` are read in `cmd/server/main.go`, and `JD_SITE` belongs to
`deploy/Caddyfile`. Wherever a variable is read, its documentation lives in four places that must stay in
step: **the reading site, `.env.example`, `docker-compose.yml`, and the README's configuration table.**

### Cutting a release

When the user names a version ("make this 0.6", "release 0.6.1"), that is the trigger. A release is a commit on the tracked branch, not a `git tag` — that is what every install
compares itself against. Four files carry it and `scripts/release.sh <version>` writes all of them:
`backend/internal/version/version.go`, `frontend/src/lib/version.ts`, `frontend/package.json`, and
`backend/internal/selfupdate/changelog.json`. Two tests fail the run if they drift (`version_test.go`,
which skips when the frontend is absent, and `TestChangelogHeadIsTheProductVersion`).

```bash
# 1. Write the release notes FIRST in backend/internal/selfupdate/changelog.json
#    (anywhere in the array; it is sorted on read).
# 2. Then:
scripts/release.sh 0.6
```

`release.sh` bumps the three version files, regenerates the root `CHANGELOG.md` from the same JSON, and
runs the two tests; it refuses before touching anything if the changelog does not already describe the
version, and prints the skeleton. **`CHANGELOG.md` is generated — never edit it by hand.** The ordering
is the point: a version bumped with no changelog entry is a release nobody is told about, and a changelog
entry with no version bump is every install permanently offering itself an update it already has.

A note added to a release whose version is already bumped — every change merged into an open patch
branch after its release was cut — is in `changelog.json`, and so in every install's notice, but not
in `CHANGELOG.md` until the markdown is generated again. Nothing fails meanwhile:
`TestRootChangelogMentionsThisRelease` checks only that the release has a section there, not what the
section says. Run `scripts/release.sh` with the version the branch already carries (`0.7.5` on
`patch/0.7.5`): the version files are rewritten with the values they hold, so the only file that
changes is `CHANGELOG.md`. Its first step alone does the same without the tests:
`cd backend && go run ./scripts/gen-changelog.go -expect 0.7.5 -out ../CHANGELOG.md`, which refuses
unless the changelog's newest release is the version named. Do it last before the branch is merged into
`main`, because the GitHub release takes its notes from that section.

A release is cut on its patch branch (`patch/0.7.2`) and reaches installs when that branch is merged into
`main`. The GitHub release follows on its own: `.github/workflows/release.yml` runs after Verify passes on
a push to `main`, reads `latest` from the changelog, and when that push changed it and no release
`v<latest>` exists, tags that
commit and publishes the release with the version's `CHANGELOG.md` section as its notes. The version
comes from the changelog, never from the branch name, so a merge without a bump publishes nothing.
Nothing is tagged by hand; a manual run of the workflow on `main` retries a failed publish.

A changelog entry is written for the person deciding whether to upgrade a root-equivalent panel on their
own server. Each change has a `kind` (`added`, `changed`, `fixed`, `removed`, `security`, `deprecated` —
closed set, parser-enforced) and reads as what they can now do; `detail` is for a non-obvious
consequence, and most changes need none. `breaking: true` requires a `breakingNote` naming what must be
done by hand, and the UI refuses to fold it away.

**`internal/selfupdate`** manages the product rather than the server.

- **The changelog is data, not prose**: embedded with `go:embed` *and* fetched from
  `raw.githubusercontent.com`, parsed by the same function both times — so a malformed file fails the test
  run before it can be a malformed file every install downloads. The compiled-in copy is what an install
  with `JD_UPDATE_CHECK=false` still shows.
- **Update discovery is independently controlled**: one unauthenticated
  GET, a user agent with product and version only, and a switch to turn it off. Automatic deployment
  branch monitoring uses separate outbound Git connections regardless of that switch. A failure keeps the
  previous good answer rather than blanking the banner — a dropped tunnel is a normal Tuesday here.
- Cadence is two floors, not a timer, because the moment somebody wants a current answer is the moment
  they open the page. `Freshness`: `Cached` nudges past `checkInterval` (2 h), `OnLoad` past
  `nudgeInterval` (5 min), `Forced` waits. Both nudges answer from cache **immediately** and check behind
  the request — blocking would turn one unreachable repository into page loads that hang for fifteen
  seconds.
- **Where the install lives is discovered, not configured**: which container bind-mounts our own data
  directory (decisive where a service name is not), then its compose `working_dir` label. `JD_UPDATE_DIR`
  is the escape hatch, and an unidentifiable install says so rather than showing a button that fails.
- **The upgrade runs in a sibling container**, and that is the load-bearing decision: `compose up -d
  --build` recreates the container running the command, so a child process is killed mid-way with the
  frontend and proxy never recreated and nothing able to report it. The backend creates a separate
  container through the socket, running its own image (which already carries git, docker and compose), and
  writes `self-update.json` + `self-update.log` into `JD_DATA_DIR` — mounted by both halves, so the *new*
  backend can read what the old one was doing. `Installer.Reconcile` at boot: alive → leave it; gone with
  the version moved → it worked (this process running is the proof); gone with the version unchanged → it
  stopped.
- It **builds one service at a time** (`selfupdate.BuildEachService`, also used by Settings' rebuild,
  and the same loop in `install.sh`). Compose builds every service at once, so the frontend's Turbopack
  peak (2.5–3 GB) and the backend's Go compile (over 1 GB) used to land together; in turn the server
  needs only the larger. The services are read with `compose config --services` from the file being
  built, and if that fails the single all-at-once build runs as before.
- It **fast-forwards, never resets** — unlike a managed deployment checkout, this is the operator's own
  checkout and an
  edited compose file is normal, so a local change survives unless it genuinely collides. Git runs there
  as root, so afterwards the updater hands back to the checkout's owner everything root-owned in `.git`
  and each file the update changed with the folders above it (`handBack`, through `hostexec.GiveBack`).
  Until 0.7.1 an update's new folders stayed root's, and the operator's own next pull stopped halfway in
  them. A checkout cloned with sudo has no owner but root for `handBack` to return to, so at every start
  the backend gives the checkout's root-owned entries to the owner of the directory holding it
  (`Service.adoptCheckout`, through `hostexec.Adopt`), when that is not root: whoever owns `/home/ubuntu`
  can already replace `/home/ubuntu/Just-Dashboard` with a tree of their own, so this grants nothing, and
  a checkout in `/opt` or `/srv` stays root's. Without it that account's git refused the repository as
  of dubious ownership, `git worktree add` with it, and agents cloned beside it instead — a clone the
  Git page rightly lists as a second repository. A walk that changed anything then calls
  `Options.Adopted`, which the API uses to carry root's GitHub login to the new owner
  (`ghx.CarryLogin`; see [GitHub sign-in](processes-terminal-github.md#github-sign-in)). The walk never follows a link (directories opened
  `O_NOFOLLOW` relative to their parent, everything else `O_PATH` and re-checked on the descriptor),
  hands a directory over only after its contents, and leaves alone another account's files, a file with
  more than one link, anything only its owner can read (the installer's `.env`) and other filesystems.
  It runs under the lifecycle lock and skips a start with an upgrade, reconfiguration or uninstall in
  flight; the restart after it settles the tree. And it **waits
  for the health URL to answer** before calling itself finished, since `compose up -d` returns as soon as
  containers start and a backend that starts then dies looks identical from there.
- **The transcript is read two ways.** The report carries `Store.Tail()` — the last 64 KB, with a
  `… earlier output trimmed …` marker when there is more — because it is polled every two seconds during a
  run and a rebuild prints a few hundred kilobytes. `GET /api/v1/dashboard/update/log` answers
  `Store.Transcript()`, the whole file up to 8 MiB from the end, as `text/plain` with `no-store`; it is
  readable by the same every-role audience as the report that already carries its end. The console on
  `/dashboard` reads it once when the tail says it was trimmed and extends it with each polled tail
  (`frontend/src/lib/transcript.ts`).

### The dashboard's own settings

**`internal/selfcfg`** is the settings half of the same idea, and it borrows the same manoeuvre for the
same reason: applying a port change recreates the container serving the request that asked for it, so the
work runs in a sibling container (`just-dashboard-reconfigure`, this image, `-self-restart`) writing
`self-config.json` and `self-config.log` into `JD_DATA_DIR`. It shares `selfupdate`'s answer to "where is
this install" rather than asking Docker the same question twice — `Options.Locate` is
`selfupdate.Service.Location`.

Update, apply, restart, rebuild and uninstall admission share `JD_DATA_DIR/lifecycle.lock`, held before
any run record, transcript or `.env` backup can be replaced. All three durable run records
(`self-update.json`, `self-config.json`, `self-uninstall.json`) are checked under that lock: a
pending/running operation blocks the other paths even after the launching process exits.
Unreadable or corrupt records fail closed and must be dismissed explicitly. This prevents concurrent
requests from overwriting the configuration needed for rollback.

- **One source of truth**: the `.env` beside the compose file, edited the way a person would. `EnvFile`
  keeps the file as lines, so the paragraph of explanation above each setting survives a change made from
  a browser, and a setting already present is replaced in place rather than appended. Every write takes a
  backup (`.env.jd-previous`) first — that copy is what the rollback restores.
- **Validation happens before anything is written**, because the process doing the checking is the process
  about to be restarted. Ports are range-checked and occupied or duplicate preferences are moved to available numbers.
  The lifecycle runner verifies actual ownership before startup and synchronizes `.env`, Caddy, the
  health probe and endpoint through `internal/stackports`; durations must parse; the allowlist must still contain
  loopback **and** the caller's own address. That last rule is the most valuable one in the package — the
  allowlist runs before authentication, so an operator who drops their own network out of it does not get
  an error page, they get a dashboard that has silently stopped existing for them.
- **Rollback is what makes the feature offerable at all.** If the new configuration does not answer its
  health probe, the sibling restores the previous `.env`, brings the stack back up on it and records
  `StatusRolledBack` — a failure of the change and a success of the net, which is why it is a fourth
  status rather than "failed".
- **No typed phrase on the apply**, deliberately. What has to be read is *where the dashboard will be*,
  since the browser that asked cannot follow it there — and the dialog says exactly that, listing every
  before-and-after and stating the URL to open next. Transcribing a forty-character MagicDNS name on top
  of that tested typing rather than attention, and rollback already covers the change that does not come
  back.
- **`Report.Drift`** compares the file with the running process on the fields this backend can observe
  about itself, which is what an operator who edited `.env` over ssh and never restarted is looking at.
- **`DetectTailscale`** is what stops the settings form rejecting its own suggestion. Choosing a
  Tailscale certificate needs three facts — the MagicDNS name (which is on the certificate), the
  tailnet IP (which is what the proxy binds, since it resolves names through Docker's resolver) and
  the tailnet range in the allowlist — and all three are one `tailscale status --json` away. The
  report carries them as `Identity`, cached for 30 s, and the form fills them in when the mode is
  picked. `CertDomains` is Tailscale's own answer to "may this node ask for a certificate", so a
  tailnet with HTTPS switched off is said *before* an apply runs into it. It is a caveat and not a
  refusal, which is the point: **the useful half of the Tailscale mode is the address, not the
  issuer**. `Apply` tries to issue, and where it cannot it drops any certificate left from the
  previous address (`DropStaleCertificate` — a stale one would be served under the new name, which
  browsers reject harder than a self-signed one), records the reason on `Run.Note` and carries on.
  The proxy entrypoint already falls back to `tls internal` when there is no certificate to serve,
  so the dashboard answers at the MagicDNS name from every tailnet device with the warning it
  already had, and `CertKeeper` — retrying every 10 minutes rather than every 12 hours after a
  failure — swaps the real certificate in and restarts the proxy on its own once HTTPS is turned on.
  `Report.Certificate` is what is actually on disk for the configured address, so the settings page
  says "trusted" only when the padlock will agree. `Issue` also rewrites Tailscale's own "your
  account does not support getting TLS certs" into the admin-console sentence, since the refusal is
  a switch rather than anything about the account. `parseTailscaleStatus` is split out from the
  subprocess so the part that reads somebody else's JSON is tested against a fixture.
- **`CertKeeper`** is why a Tailscale install shows an ordinary padlock rather than a warning:
  `tailscale cert` runs on the host through `hostexec` (tailscaled's socket is the host's, and this image
  deliberately carries no Tailscale client) and writes into `JD_DATA_DIR/certs`, which the proxy mounts
  read-only. Checked at boot and every 12 h, renewed with 30 days to spare, and the proxy is restarted
  afterwards because Caddy reads a file-based certificate once. `deploy/proxy-entrypoint.sh` turns
  `JD_TLS` into the scheme and the `tls` directive: a Caddyfile cannot branch, and an installer that
  edited a tracked one would make every later `git pull` a merge conflict.
- Routes: `GET/PUT /api/v1/dashboard/config`, `POST /api/v1/dashboard/restart`,
  `DELETE /api/v1/dashboard/config/run` and `GET /api/v1/dashboard/config/log` (the whole restart
  transcript as `text/plain`, the same two reads the update transcript has), all `system.admin`, the two
  mutations inside `s.destructive`.

Database provisioning uses the shared `internal/portalloc` range selection instead of a 64-port window.
It returns and audits Docker's actual host binding if a competing process claims the initial choice.

`install.sh` sources `scripts/install-dependencies.sh` before any operation requiring curl or OpenSSL.
It groups setup into four labeled stages and prints account-recovery and stack-command examples after
successful health verification. The terminal scripts use the built backend's isolated `--admin` mode
for account operations and Compose for status/logs/recreation; see
[terminal tools](../operations/terminal-tools.md) for behavior, privilege boundaries and verification.
It installs only missing curl/OpenSSL/Certbot packages using apt, dnf, yum, apk, zypper or pacman, validates
Certbot's HTTP authenticators and enables an existing packaged renewal timer. Package failures stop setup.
The same `jd_pkg_install` dispatch (one `apt-get update` per run) backs `jd_install_terminal_extras`,
which `install.sh` runs when the terminal is enabled: it installs `zsh`, `zsh-autosuggestions` and
`zsh-syntax-highlighting` where missing and, on success, appends `JD_TERMINAL_SHELL=<zsh path>` to a fresh
`.env`. A re-run that kept its `.env` asks first, and never touches a file that already names a shell.
That install is best effort: a failure is a warning, and the terminal opens the account's own shell.
`jd_install_host_tools` runs on every install and re-run, before any question: the web terminal is a host
shell, so its git and the Git page's GitHub sign-in need host packages, and Network → Tools runs `whois`
and `traceroute` on the host. It installs whichever of `git`, `git-lfs`, `whois` and `traceroute` are
missing one package at a time, so an unavailable one costs only itself, and `gh` through `jd_install_gh`:
GitHub's signed apt repository on Debian and Ubuntu (their packaged gh is years behind on an LTS release;
the image uses the same repository), the distribution's package on dnf, yum and zypper with GitHub's RPM
repository as the fallback, and `github-cli` on apk and pacman. Failures are named and warned about, never
fatal. Firewalls, fail2ban and cron are deliberately absent: the dashboard manages them when present, and
installing one changes the host's security or scheduling rather than supplying a tool.
The backend image still includes Certbot for container execution; host installation supplies host tooling
and the distribution renewal schedule. `python3 scripts/test_install_dependencies.py` verifies the
installer with fake package commands, without changing host packages. Hostname readiness returns
`certificateIssue` alongside `certificateMethod`, preserving listener/plugin errors for Quick Deploy.
Quick Deploy keeps HTTPS selected when readiness fails, presenting the actual blocker instead of
silently switching the initial configuration to plain HTTP.

### Uninstalling

**`internal/uninstall`** removes the dashboard from the server, from the Danger zone at the end of
`/dashboard/configuration`. It is the third lifecycle operation and the only one nothing comes back from,
so it is built around the moment the page watching it loses its API.

- **One question, two modes.** `keep` removes the stack — its three containers, the two images it builds,
  the lifecycle siblings, `/tmp/just-dashboard` and the held terminal sessions (`systemctl stop
  jd-terminal-*` on the host) — and leaves everything the dashboard made: deployments keep running, and
  `JD_DATA_DIR`, the backups and the checkout's `.env` stay, with a copy of the `.env` written to
  `JD_DATA_DIR/install.env`. `wipe` also removes the deployments and deletes `JD_DATA_DIR`, the proxy's
  two Compose volumes (its CA) and every copy of the `.env` in the checkout (`.env`, `.env.jd-previous`,
  `.env.jd-tmp`, `.env.backup.*`). `deleteBackups` deletes `JD_BACKUP_DIR` too and is honoured only with a
  wipe. Neither mode touches databases the dashboard provisioned, proxy sites and their `jd-*` files,
  certificates, the network module's `/etc/just-dashboard/network` and its boot units (the docs promise a
  reinstall finds them), SSH and fail2ban drop-ins, Linux accounts, or the checkout itself.
- **The work is split across two processes, in the order the stack can lose them.** `Service.Begin`
  admits under the lifecycle lock, records the run (`self-uninstall.json`, transcript `self-uninstall.log`)
  and answers 202; the backend's own half then runs in a goroutine while it still serves the page.
  `Server.pauseForUninstall` stops what would put a deployment back — the Git watcher, the schedules, the
  preview reconciler and quarantine, the database-network keeper, the backup scheduler and the ingress
  keeper — and `deploy.Engine.Shutdown` waits up to 15 minutes for running deployments to reach a point
  they can resume from. For a wipe, `Teardown` removes the deployments (below). Then it starts the sibling
  `just-dashboard-uninstall` from its own image (`-self-uninstall -state-dir`), which takes the stack down
  and removes the rest. The paused modules cannot be restarted in place, so a run that fails in the
  backend's half records the failure and recreates the stack through `selfcfg.Restart` (`Options.Resume`),
  which is what brings them back.
- **What the deployments own is read two ways** (`deployments.go`). Docker marks containers and networks
  — `io.just-dashboard.managed=true`, the `com.docker.compose.project` of a managed container, the
  `com.just-dashboard.ingress=true` ingress — and the database knows the rest: every project's
  `RemovalPlan` (archived or not) supplies managed volumes, bind paths, routes, database networks and image
  digests. The dashboard's own compose project is excluded by name. `Remove` goes in dependency order:
  preview tailnet addresses (`NormalizedStepExecutor.WithdrawPreviewAddresses`), containers, routes
  through `proxysvc.RemoveDeploymentRoute`, the ingress container and `proxysvc.IngressDir`, networks
  (disconnecting any other container still attached — a database a deployment was linked to — first),
  volumes, bind paths through the deployment roots' `files.Delete`, then images, never forced. A
  `just-dashboard/deployment-*` image is removed by tag, so an image the dashboard only retagged keeps its
  other names. Anything that will not go is a warning on the run, not a failure: an uninstall that stopped
  at an in-use volume would leave the dashboard installed.
- **The sibling** (`runner.go`) runs `--privileged --pid host --rm` beside the checkout and data mounts:
  host commands go through `hostexec` and host paths through PID 1's root. Only `docker compose down`
  can fail the run — the stack is then started again so the operator is not left with half of one —
  and everything after it is best effort. Deletions are done in Go and stay on the filesystem they start
  on (`removeContained`), since BusyBox `rm` has no `--one-file-system`; `deletable` refuses the system
  directories whatever the record says. A wipe leaves `JD_DATA_DIR/nginx-cache` when a file under
  `JD_NGINX_DIR` names it, because nginx creates a cache's last directory but not its parents. The image
  the sibling runs from cannot be removed while it runs, so it asks the host to: a `systemd-run` timer 30
  seconds later runs `docker image rm`; without systemd the transcript says to do it by hand.
- **The transcript outlives the stack.** Both halves also write `/var/log/just-dashboard-uninstall.log`,
  outside the data directory a wipe deletes; it is the only record of the sibling's half. The page reads
  a failed poll during a live run as the stack having gone (`frontend/src/lib/uninstall.ts`), whatever
  phase it last saw, because a keep finishes between two polls; a backend that merely crashed restarts
  and answers with the run closed as failed.
- **Reinstalling.** At boot `Service.Start` closes a live run whose sibling is gone as failed, and clears
  a successful one — this process running after a kept uninstall is the reinstall — removing
  `install.env` once the key in it is the key the process runs with. `install.sh` on a fresh clone with
  no `.env` offers to restore `install.env` and continues as a re-run that keeps its settings. The
  suggested self-backup job excludes `install.env`.
- Routes: `GET /api/v1/dashboard/uninstall` (the run and its transcript's tail, polled),
  `GET /api/v1/dashboard/uninstall/plan` (what each mode removes and leaves, read once by the dialog),
  `GET /api/v1/dashboard/uninstall/log`, `DELETE /api/v1/dashboard/uninstall/run` and
  `POST /api/v1/dashboard/uninstall` (`{mode, deleteBackups}`, inside `s.destructive`), all
  `system.admin`. A wipe requires the host name as the typed phrase (invariant 3); a keep does not.

## SQL handoff at tab capacity

A saved-query URL stays pending when all 24 SQL tabs are occupied. Its intent is consumed only after
an existing tab for the same saved query is focused, a blank tab accepts it, or a newly available
slot accepts it. Closing a tab therefore opens the pending
query once rather than silently dropping it. No extra query runs or connection mutations are caused
by this handoff. `database-query.spec.ts` covers the full-capacity case.
