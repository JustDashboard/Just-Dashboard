# Databases: every database on the server, each engine as itself

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
`/databases` section of the [frontend feature map](../frontend/feature-map.md), and the checks that
depend on what a request carries are stated once in
[request lifecycle](../architecture/request-lifecycle.md#checks-that-depend-on-what-a-request-carries).

## Engines, flavours and the capability table

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

## The inventory: what is on this machine

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
  For an administrator it also carries `kernelProblems` (*MongoDB 8 and the kernel*, under the
  provisioning catalogue).
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

## The provisioning catalogue

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
- **MongoDB 8 and the kernel.** MongoDB 8 is built with Google's tcmalloc, which keeps per-CPU caches
  through rseq; the official image sets `GLIBC_TUNABLES=glibc.pthread.rseq=0` so glibc leaves rseq to
  it. Linux 6.19's rseq rework broke what tcmalloc relied on and the kernel's fix arrived in 7.0.14, so
  on the kernels between, mongod exits at once with *MongoDB cannot start: Linux kernel versions 6.19
  and newer has a known incompatibility with this version of MongoDB* (MongoDB SERVER-121912), and with
  `unless-stopped` the container restarts for ever. MongoDB 8.0.30's own fix applies only on 7.0.14 and
  later. Handing rseq back to glibc is what starts it: on a GitHub runner with Linux `7.0.0-1017-azure`,
  `mongo:8.0` (8.0.34) refused as shipped and with `rseq=0` set explicitly, started with `rseq=1`
  (mongod then warns that tcmalloc without rseq costs performance), and `mongo:7.0` and `mongo:6.0`, on
  the older tcmalloc that does not use rseq, started unchanged. `deploy/host_kernel.go` holds the facts
  (`MongoKernelAffected` for [6.19, 7.0.14), `MongoReleaseAffected` for 8 and later,
  `MongoKernelMitigated` reading the last `glibc.pthread.rseq` in the colon list); the kernel is
  `sysinfo.KernelRelease` (uname, which inside the dashboard's container is the host's, the kernel every
  container here runs on). `database_mongo_kernel.go` applies them. **Provisioning does it without
  asking**: on such a kernel an 8.0 request is created with the tunable and the label
  `io.just-dashboard.mongo-kernel-mitigation=<kernel>`, and the `202` reply and the
  `database.server.provision` audit carry `kernelMitigation`, the sentence the connection's summary then
  says from the label (`container.kernelMitigation`): on, why, its cost, and that 7.0.14 does not need
  it. Every later recreate keeps both, since `SpecOf` carries the environment and labels. 7.0 and 6.0
  are started as they are. **A server that already exists** — made before this, or by hand — is offered
  the fix where it is drawn: the fleet's `kernelProblem` (`mongoKernelProblem`) is set for a MongoDB 8
  container here whose environment lacks the tunable (its release is the image's `MONGO_VERSION`, else
  its tag) and whose last fifty log lines carry MongoDB's own refusal (`mongoKernelRefused`), with the
  alert *MongoDB cannot start on this kernel; apply the mitigation*, a sentence, and `fixable`. Docker's
  state is not the test: a crash loop reads as running for the second of each attempt, which on CI hid
  the offer from one poll in two. A MongoDB release that learns to start on these kernels never logs
  the refusal, so it is left alone. One that no connection is made to — on such a kernel,
  the one provisioning made before it knew, whose adopt never got an answer — is reported the same way
  by the inventory, for an administrator, as `kernelProblems` by instance key
  (`inventoryKernelProblems`, which reads nothing on any other kernel), and *Found on this server*
  offers the fix as its one press. `POST /databases/{id}/kernel-mitigation`, or `POST
  /databases/kernel-mitigation` with `{"container"}` for one not connected (both `system.admin` +
  `destructive`, audited `database.server.kernel_mitigation` with the kernel, release, tunables, old and
  new container ids, or the refusal; one core, `mitigateKernel`, the connection's route adding the power
  route's *restarting* state and a fresh pool) recreates it through `dockerx.Recreate` from its own
  `SpecOf` with the tunable merged into any other glibc tunables and the label added — same name,
  volumes, port and restart policy, the old container parked and put back if the new one cannot be
  created — and refuses with `409` a container that does not need it (another kernel, another release,
  already set) or that compose owns, whose `kernelProblem` instead says to add the variable to the
  service and redeploy. A protected connection lets it through, as it does an access change. Docker's
  own alert, `container_down`, adds the same reason to what it says of such a container
  (`mongoKernelAlertLine`, from the listing's image).
  `TestProvisionStartsMongoDB8WithWhatThisKernelNeeds`,
  `TestMongoKernelProblemIsOnlyMongoDB8WithoutTheTunable`,
  `TestApplyingTheMitigationKeepsTheRestOfTheEnvironment` and
  `TestContainerDownSaysWhenItIsMongoDBOnThisKernel` hold it, and the live fixture below runs both paths
  on whatever kernel the runner has.
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

## A connection: its record, its state and its power

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
  detected-but-unconnected half is filled only for `system.admin`. The server map draws its `env` and
  `session` lines from the same join (`mapUses`), held for two minutes.
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

## Protected connections

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
    row-level security policies and their switches, and the connection's assistant, which would send
    what is in it to a model (every other chat's call into a protected connection is answered with why by
    `agentProtected`, so nothing in it reaches a model from anywhere).
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

## The query runner

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

## A connection's assistant

A connection has the dashboard's one assistant, told about that connection
(`api/handlers_assistant_database.go`, `POST /databases/{id}/assistant`): the same conversation as a
deployment's and the server's ([the dashboard's assistant](../deployments/operator.md)), on the connection's own
*Assistant* page and in the SQL editor's third column, which are one chat. It replaced the editor's own
assistant, which drafted statements from the structure alone and ran nothing; what it is sent and what it may do
are decided here, not by the page.

- **The provider is the operator's own, set up once for every page.** It is chosen on `/assistant`, among
  Anthropic — a Claude model from a closed list (`assistant.AnthropicModels`) and an API key — hosted providers
  that speak OpenAI's chat completions, or a model on this machine (Ollama, LM Studio, vLLM, llama.cpp), by its
  address, a model name and a key if it asks for one. Several may be saved, one active; see
  [the assistant's providers](../deployments/operator.md#providers), which also covers its routes and the keyring.
  `GET`, `PUT` and `DELETE /databases/assistant` still answer on the same keyring. The address is HTTPS unless it
  is loopback, a private range or the tailnet's `100.64.0.0/10`, and one with no path is given `/v1`. Nothing is
  saved until the provider has said that the key, the address and the model answer, asked without asking the model
  anything — Anthropic's `GET /v1/models/{model}`, the server's own `GET /models` with the model looked for on it
  (a server that answers that path 404 or 405 lists nothing, and its first question says whether the model is
  there) — so a mistyped key is refused under its field. The key is never read back, and a blank key keeps the
  stored one only for the same provider at the same address, so a key is never sent to a server typed in after it.
  No provider request follows a redirect, because the key travels in a header.
- **A chat begins knowing the connection and its structure, never a row.** The system prompt
  (`assistant.DatabaseSystemPrompt`, `Server.databaseContext`) names the connection, its engine and version,
  host, database, what it serves and the operator's notes, and — for a SQL engine that answers within eight
  seconds — the structure of its default schema through the catalogue the schema pages use
  (`assistant.ReadSchema`: `ListTables`, `BuildSchemaGraphWithLimit` for 150 tables) written as tables and views
  with their columns, types, keys and references (`assistant.SchemaText`, bounded to 48,000 characters described
  and 12,000 named) — never row estimates, defaults or comments, any of which can be a value somebody typed. One
  that does not answer in time is read by the model later, through the routes.
- **It reads and changes the connection as the person.** It is offered `api_request`/`find_routes`, `run_command`
  and `open_page` with the connection's pages (`assistant.DatabaseTools`), and reaches the connection through its
  routes under the person's own role: `POST /databases/{id}/query` runs one statement — a read, which the default
  mode runs unasked, when the classifier says the statement only reads — and the rest of its routes read and
  change the structure, the data, users, backups and settings, each through its own handler, classifier and
  audit entry. A row reaches the model when a call reads it, masked like every answer.
- **What the editor attaches goes with the question.** The editor's column can hand over the statement, the last
  result (at most 50 rows of 30 columns, 200 characters a value, as a text table), the last error and the plan,
  each by the person's own toggle, as a question's `attached` items (at most four, a label of 60 characters and
  20,000 characters of text each). They are written after the question for the model; the page, the saved chat
  and the audit entry keep the labels alone.
- **A statement it writes is text until somebody runs it.** In the editor, a finished answer's statement offers
  *Open in a tab*, *Insert* and *Copy*, and the person's own Run sends it through the classifier like any other.
- **Refused on a protected connection, everywhere.** `POST /databases/{id}/assistant` is not on
  `protectedRoutesAllowed`, so a protected connection refuses it with `409 connection_read_only` before anything is
  read or sent, and any other chat's call to a protected connection's routes is answered with why and reads
  nothing (`agentProtected`); only the list of connections and the connection's own record answer, so the
  assistant can say it is protected. It takes `service.control` and a session, as a deployment's does, and its
  presses are audited as `database.assistant.ask`/`.tool` with the connection's id. A connection's chats are
  deleted with it (`forgetConnection`).
- **The same provider answers three other questions**, each sent only when somebody presses for it:
  a drawing's *Describe a diagram* (`POST /boards/diagram`, the prompt alone, audited as
  `board.diagram`), a failed deployment's *Ask why it failed*
  (`POST /deploy/{id}/runs/{run}/diagnosis`, the run's record masked on the server first, audited as
  `deploy.run.diagnose`; [the deployment contract](../deployments/implementation.md) says what is sent),
  and the dashboard's assistant on its other subjects — a deployment's *Diagnose* and page
  (`POST /deploy/{id}/assistant`), a new deployment's (`POST /deploy/drafts/{draft}/assistant`) and the server's
  (`POST /assistant/chat`), session-only, audited as `deploy.assistant.ask`/`.tool` and
  `assistant.chat.ask`/`.tool`. All of them share the install's budget of model calls (20 a minute, a burst of 6)
  and its four answers at once.

## The table editor: rows, values and cells

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

## The schema catalogue and schema changes

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

## Operations: watching, maintaining and administering a server

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
  cookie is `403 token_required`, and a key narrowed to `metrics.read` is admitted here, at the
  deployments' sibling `GET /api/v1/metrics/deployments`
  ([request observability](../deployments/request-observability.md#the-prometheus-exposition)) and
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
  the `$n` markers a statement's Plan view asks a value for, and replaces every literal with `?`. That is
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
    `?preview=1` returns the same body with `preview:true` and executes nothing. The flag is `1`,
    `0`, `true` or `false` on every route under `/databases`, and anything else (`?preview=yes`, an
    empty value, the flag twice) is `400 bad_request` naming `preview` before a handler runs
    (`requirePreviewFlag`): these two routes used to test for `1` alone, so `?preview=true` granted.
    MySQL escapes `_` and `%` in the database name, which is a pattern at that position.
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
  `performance_schema.events_statements_history` from `long_query_time` up, with `enable` the `SET GLOBAL`
  that turns the slow log on or adds `TABLE`; MariaDB's `slow_log` only. Beside its one statement (the
  query console runs one) `enable` carries `changes`, the same change as `{name, value}` parameters in the
  order the page's Turn on sets them through `PUT /databases/{id}/settings` — the Parameters page's checked,
  audited path, which on Postgres runs `ALTER SYSTEM` and `pg_reload_conf()` on one connection. The
  statement alone had left that reload to a second statement, and a server told to log slow statements kept
  logging none. A Postgres connecting role that is not a superuser gets `blocked` with why instead of
  `changes`, since the settings route would refuse it;
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

## Alerts: told when a server or its backups go wrong

A database's **Alerts** page (`/databases/{id}/alerts`, in the rail under Operate) holds rules that
tell the deployments' notification channels — webhook, Discord, Slack, Teams, Mattermost, Telegram,
ntfy, Gotify, Pushover and e-mail, through
`deploy.AutomationStore.DeliverNotification`, so a channel is configured once on
`/deploy/notifications` and nowhere else — when something is wrong with a database, and when it is
over. There is no second delivery system and no second poll: the server kinds are judged on the
recorder's own thirty-second pass (`api/database_metrics.go` hands `dbAlerts.Evaluate` what it read),
and the backup kinds on the same pass from `backup_jobs` and `backup_runs`.

- **Six kinds, stated per engine.** `alertKinds` (`dbx/alert_capabilities.go`, a list flag) is what a
  database of an engine can be watched for, and the create route refuses a rule for one database
  whose engine lacks the kind. The host's own `backup_failed` rule
  ([Host alerts](observability-security.md#host-alerts-told-when-the-machine-itself-goes-wrong))
  follows every backup job, files and volumes included, and leaves to these rules any job an enabled
  one of them already follows, so a failed dump is told once. A server can be `down` (it did not
  answer the recorder; a SQL pool whose snapshot was refused is pinged to tell a refusal from a
  server that went away), fill its
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

## Redis

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

## MongoDB

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

## Code generation

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

## Seed data

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

## Moving data: export, import, dumps, restore and copy

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
  control center takes the later of its answer and the fleet's. Each row also lists when the
  connection's newest fourteen dumps were written and how large each is (`recent`, newest first, from
  the directory entries alone), which a database's card draws as a strip. What a dump holds — the options it was
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

## Major-version upgrades

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

## Point-in-time recovery

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
  are not typed twice; a job whose destination is SFTP or rclone is refused, since WAL-G writes only
  to a directory or an object store. Keys are sealed with `JD_MASTER_KEY` like a job's and never returned.
- **Encrypted with WAL-G's own OpenPGP**, to a key made for the archive. The server is given only the
  public half, which is all encrypting needs: a server, or a superuser reading `walg.json` through
  `pg_read_file`, cannot read its own archive back. The private half is sealed in the store and written
  only into a recovery's own volume, which is removed once the copy has recovered. The pair is made and
  its fingerprint read with `github.com/ProtonMail/go-crypto`, the maintained fork WAL-G itself uses,
  as a version 4 RSA key of 3072 bits named in the code rather than left to the library's defaults;
  `golang.org/x/crypto/openpgp`, which made the keys of installs up to 0.8.5, is withdrawn as unsafe
  (GO-2026-5932), and a key it made reads back with the same fingerprint (`pitr_test.go`).
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

## The MCP server

An AI client — Claude Code, Cursor, VS Code, anything that speaks the Model Context Protocol over
Streamable HTTP — reaches the databases, and the deployments if its key was given them, through one
endpoint, `POST /api/v1/mcp` (`api/handlers_mcp.go`, `handlers_mcp_tools.go`, `handlers_mcp_deploy.go`,
`handlers_mcp_operations.go`; the protocol is `internal/mcp`). It offers 113 tools in all: the seven
database tools, the six hand-written deployment tools, and 100 tools generated from the deployment API,
the last to a key whose owner turned them on for it. It sits outside the `/databases` tree, as the metrics export does,
because what admits a request there is different.

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
  `POST /tokens/mcp` (session only) makes one from `{name, role?, ttlDays, connections, deployments?,
  deploymentIds?, operations?}` — Account → API keys sends the replaced key's role, its databases less
  those protected or forgotten since, its deployment grant (narrowed to `read` for a read-only role),
  the deployments that grant was narrowed to and whether it had the generated tools, so a replacement
  never reaches more;
  `PUT /tokens/{id}/connections` (session only, the owner's own active MCP key) replaces what it
  reaches — its databases, its deployment grant when `deployments` is sent, the deployments it is
  narrowed to when `deploymentIds` is sent (an empty list is every deployment) and the generated tools
  when `operations` is sent (each left out is kept) — and keeps the secret. Both refuse a protected connection (`409 connection_read_only`), an
  engine without the `mcp` capability (every SQL engine has it; MongoDB and Redis do not), a missing
  one, a key that would reach no database and no deployments, a `write` or `deploy` grant for a
  key whose role lacks `service.control`, and chosen deployments for a key given none of them, or one
  missing, archived or named twice (`keyDeploymentNames`, as for a deploy key). The dialog
  lets a database protected since be cleared from a key, the one way to save the rest. They are audited as
  `dashboard.token.create` (with `scope`, the databases by name and `deployments`) and
  `dashboard.token.connections`, with `reaches` (`every`, or the chosen deployments by name) beside a
  deployment grant and `operations`, whether the key has the generated tools. Listing, revoking and deleting are the routes every key has.
- **The deployments are a grant of their own**, `api_tokens.deploy_access` (additive, default `''`,
  so every key made before reaches none): `''` none, `read`, or `deploy`. It reaches every deployment,
  or — like a deploy key — only the ones chosen for it, in `api_token_deployments` (the deploy keys'
  table, keyed by the key's id; no rows is every deployment, so every key made before reaches them
  all). Both are read with the key on every request (`ResolveAPIToken` loads the list for an MCP key as
  for a deploy key), so a deployment taken off the list is refused the next call.
  The deployment tools (`internal/mcp/deployments.go`, `api/handlers_mcp_deploy.go`) act on each
  deployment's production environment — the fleet's rows, archived deployments and previews left out —
  and name a deployment by its name: `list_deployments` (source, build, health, address, live release,
  pending changes, the run in progress and the last one, and the deployment's and its production
  environment's ids, `id` and `environmentId`, which the generated tools take), `list_runs` (10, at most
  50, each run with its `id` beside its number),
  `read_run_log` (the run, its steps' states and errors, and the last transcript lines — 200, at most
  1,000, each cut at 1,000 bytes and the whole at 48 KB, newest kept — read from `deploy_log_chunks`
  as the run page reads them, so the build's secret values are masked as they were written) and
  `list_releases` (10, at most 30) for a `read` grant; `deploy` and `rollback` for `deploy`. `deploy`
  queues what the deployment page's Deploy queues — `enqueueNormalizedDeploymentAtSource` (or the
  legacy Compose path), with `ref` or `revision` as the run route takes them, so the plan checks, the
  backup gate, `RequireSupportedProject`, the refusal to build a pull request's head into production
  and the queue's own rules hold — and needs the owner's role to hold `service.control` now.
  `rollback` queues `OperationRollback` to the release named, or without one the newest retained
  release older than the live one; it needs `destructive` as the rollback route does and spends
  `destrLim` under the route's own per-account-and-address key, and refuses a legacy Compose
  deployment, which rolls back to a commit from its page. A preview's approval, a cancel, a retry and
  every setting stay in the dashboard. `deploy` and `rollback` are offered in `tools/list` only when
  they can succeed for the key and its owner's role, and a key that reaches only the deployments is
  offered no database tool. A key narrowed to chosen deployments finds those alone: the others are
  not listed, and naming one is answered as a deployment that is not there. Each call is audited as
  `deploy.mcp.<tool>` with the deployment as target and the run number, release or ref in the detail.
- **The tools generated from the deployment API** (`api/handlers_mcp_operations.go`, the tool's
  shape in `mcp/operations.go`) are **each key's to turn on**: `api_tokens.mcp_operations`
  (`APIToken.Operations`, additive, `0` for every key made before them, `mcp.Offer.Operations`). A key
  without them is offered exactly the hand-written tools under its grant, its instructions say nothing
  of the others, and a call to one by name is refused (`operations_off`: "this key's generated tools are
  off; its owner can turn them on under Databases → MCP server"), so no key gains a tool with an
  upgrade; the routes take `operations` (left out, a new key has them off), and the dialog makes a new
  key with them on unless they are unticked. With them on, every route under `/api/v1/deploy` whose
  line in `deployKeyRoutes`
  is `keyOpen` or `keyEvery` is a tool named for its operationId in snake case
  (`deploymentConfigurationSave` is `deployment_configuration_save`), its input schema the route's path
  and query parameters by name and its JSON body under `body`, with the components the body refers to
  copied into the schema's own `$defs` so it stands alone; a path parameter's description says which
  tool lists its value (`id` from `list_deployments`, `env` its `environmentId` or
  `deployment_environments`, `run` from `list_runs`). The list is read off the router `Routes` built and
  the OpenAPI description generated from it, once, the first time the endpoint is asked (as the
  description itself is built), so it cannot drift from the routes: a route given a deploy key line is
  a tool, and a session-only route or one the table keeps from a deploy key (`keySession`, `keyDeny`)
  never is. Left out, each with its reason in the catalogue: the six routes a hand-written tool already
  is (`GET /deploy/` is `list_deployments`, `…/runs` `list_runs`, `…/runs/{run}/transcript`
  `read_run_log`, `…/environments/{env}/releases` `list_releases`, `POST …/run` `deploy`,
  `POST …/environments/{env}/rollback` `rollback` — the hand-written one is kept, with its names and
  bounds), the three WebSocket streams, the four routes whose answer is not JSON (the favicon, the
  preview frame, the CSV export, the YAML description) and the JSON description itself: 100 tools
  generated, 113 tools in all. `TestEveryDeployKeyRouteIsAnMCPTool` walks the router and fails when a
  deploy key route is neither a tool nor left out with a reason, when a route no deploy key reaches is
  a tool, or when the count differs from `api.MCPGeneratedTools` and the 113 these pages, `docs/features/databases.md` and
  the deployment implementation notes quote.
  - **Grants.** A `GET` is offered under `read`; any other method under `deploy` to an owner whose role
    holds `service.control`; a route behind `s.destructive` (read off its chain by the `markDestructive`
    marker, the description's `x-destructive`: removing a shared variable, the maintenance switch, the
    instant rollback, the legacy rollback) under `deploy` to an owner whose role also holds
    `destructive`. `destructiveHint` is set for those, for every `DELETE`, and for
    `mcpDestructiveHints`: a run queued from the body (stop and restart are decided there), cancelling
    a run, aborting a canary, rotating a variable or a trigger's secret. `readOnlyHint` marks the reads
    and `idempotentHint` `GET`, `PUT` and `DELETE`. A call under the wrong grant is refused before
    the route is called.
  - **Through the router, as the key.** A call becomes a request to the route made through the
    dashboard's own router (`dispatchMCP`): its path built from the pattern with each parameter
    escaped, the query, the body, the client's resolved address, and the MCP key's principal in its
    context (`httpx.WithMCPDelegate`) instead of a credential. `AuthenticateFeatures` admits it only
    through the deploy key gate — the table's line for the route the router's `Find` resolves, the key's
    chosen deployments and their environments, `keyEvery` for a narrowed key — and marks it `ViaMCP`,
    which `Principal.DeployKey` reads as a deploy key from there: variables are write-only, a value a
    route would hand back is withheld, `keyReferenceGuard`, `keySourceGuard`, `hostAuthority` and
    `keyEnvironmentGuard` hold, a narrowed key's drafts are its own and what it commits is added to its
    list. Every other group refuses a delegated request, and a context value cannot arrive from the
    network. The allowlist, the rate limit, the route's capability checks and its own audit entry (under
    the MCP key's id and name) apply as to any request; the key carries its owner's role, not the deploy
    key's administrator, so an operator's key is refused what needs `system.admin`.
  - **Answers.** The route's JSON is the result (a list or a scalar under `items`, since structured
    content is an object; a `204` as `{status, done}`); past 48 KB the first 48 KB of its text are
    returned with a note of the whole size. A refusal is the route's own code and message as a result
    marked `isError`; a failure the dashboard could not answer is named without its text. Each call is
    audited as `deploy.mcp.<tool>` with the route's path as target and the operationId, the route, the
    status and the outcome in the detail, beside the route's own entry for a write. A call counts against
    the key's four in flight.
- **Both revisions of the protocol.** A 2025 client (`2025-03-26`, `2025-06-18`, `2025-11-25`)
  opens with `initialize` and repeats the version in `MCP-Protocol-Version`; a 2026-07-28 client
  sends no handshake and carries its version in every request's `_meta`, mirrored into
  `MCP-Protocol-Version`, `Mcp-Method` and `Mcp-Name`, which must agree with the body
  (`-32020`). The endpoint keeps no session in either, issues no `Mcp-Session-Id`, refuses a batch,
  answers `GET` and `DELETE` with `405` (no stream for the server to speak first on), and takes a
  notification or a reply with `202`. `server/discover`, `ping`, `tools/list` and `tools/call` are
  the methods; `initialize` is recorded as `database.mcp.connect` with the client's name and
  version. The instructions `initialize` and `server/discover` give (`mcp.Instructions`) describe
  what the key reaches: the databases, the deployments (with how the generated tools are named and
  which ids they take), or both. `tools/list` gives the hand-written tools first, in their fixed
  order, then the generated ones the grant reaches, in the order of their routes.
- **The database tools** (`mcp.Tools`): `list_connections`, `list_tables`, `describe_schema` (structure only:
  column names and types, nullability, keys and what a foreign key references — no defaults or
  comments, which can be values somebody typed), `sample_rows` (10, at most 50), `explain` (planned,
  never run), `run_query` and `run_write`. `run_query` is offered to an owner whose role may run SQL
  (`service.control`, the query route's own question); it refuses anything `dbx` does not classify as
  a read before sending it, and runs the statement inside the engine's read-only scope (100 rows, at
  most 500, 30 seconds). `run_write` is offered only when the key may change some database, runs one
  statement at a time (60 seconds), and passes it through `authoriseSQL`: a destructive statement
  needs `destructive` and spends `destrLim`, as in the SQL editor. Every value is cut at 1,000 bytes
  and a result at 48 KB. A key has four calls in flight at most, the deployment tools' included. The read-only scope is the
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
  signed-in account's active MCP keys with what each reaches ("reads shop, changes notes, deploys",
  "deploys 2 deployments" for a narrowed one),
  a form choosing databases (those the server would refuse listed with why) and read or change for
  each, None, Read or Deploy for the deployments (Deploy disabled for a role without
  `service.control` and for a key made read-only) and, for Read or Deploy, which deployments (every
  one, or those ticked, with the deploy key dialog's own picker) and *Offer every deployment route as
  a tool* (on for a new key, off for one made before the generated tools, its hint saying what it adds
  under each grant and that a destructive route needs a role that may take destructive actions), the
  key shown once with the configuration for
  Claude Code, Cursor and VS Code, editing what a key reaches (*Edit access*) and revoking it.
  Account → API keys lists MCP keys as *MCP only* and replaces one with the same databases and
  deployment grant.

## Tests

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

## Routes

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
| `POST` | `/databases/kernel-mitigation` | `system.admin` + `destructive` | yes | `database.server.kernel_mitigation` | — |
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
| `POST` | `/databases/{id}/kernel-mitigation` | `system.admin` + `destructive` | yes | `database.server.kernel_mitigation` | open |
| `GET` | `/databases/{id}/schemas` | read | — | — | — |
| `GET` | `/databases/{id}/stats` | read | — | — | — |
| `GET` | `/databases/{id}/url` | `system.admin` | — | `database.connection.reveal` † | — |

**Rows and statements** (25)

| Method | Path | Capability | Destructive | Audited as | Protected |
| --- | --- | --- | --- | --- | --- |
| `POST` | `/databases/{id}/assistant` | `service.control`, session | — | `database.assistant.ask`, `.tool` | refused |
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

## Database provisioning for deployments

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
with an illegal instruction, and on Linux 6.19 up to 7.0.13 its 8.0 release is started with
`GLIBC_TUNABLES=glibc.pthread.rseq=1` (*MongoDB 8 and the kernel*, under the provisioning catalogue).
The URL read also takes `format` and `database`; see [deployment database
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
containers/volumes/networks, each container with its anonymous volumes. Each image is pulled, and the
pull timed in the log, before anything is created; the engine then has two minutes from its container
starting to accept a sign-in, and a failure reports the adopt route's last reply with the container's
state and last log lines. The MongoDB subtest holds provisioning to *MongoDB 8 and the kernel*: on Linux
6.19 to 7.0.13 the container must carry the tunable and the label and the reply must say so, and on any
other kernel none of the three is there. If MongoDB still logs its kernel refusal, the subtest fails at
once with that line, the kernel and the container's `GLIBC_TUNABLES`, because the mitigation has stopped
working and waiting cannot help. Then `mongodb-kernel-mitigation` makes a MongoDB 8 server as
provisioning did before it knew of the kernel (the template's image, environment, port and named
volume, no tunable). On a kernel MongoDB 8 refuses it restarts for ever; on any other the subtest waits
for it to start, writes the refusal line a runner logged into its output (as the `mongodb` user, the
one that may open mongod's descriptors), kills it, and has the dashboard read its kernel as
`7.0.0-1017-azure`. Either way the inventory must offer the
mitigation while nothing is connected to it; then it is saved as a connection, the fleet must offer
it, `POST /databases/{id}/kernel-mitigation` must bring the server up within two minutes on the same
volume with the tunable, the label and its restart policy, and the fleet must then offer nothing.

## SQL handoff at tab capacity

A saved-query URL stays pending when all 24 SQL tabs are occupied. Its intent is consumed only after
an existing tab for the same saved query is focused, a blank tab accepts it, or a newly available
slot accepts it. Closing a tab therefore opens the pending
query once rather than silently dropping it. No extra query runs or connection mutations are caused
by this handoff. `database-query.spec.ts` covers the full-capacity case.
