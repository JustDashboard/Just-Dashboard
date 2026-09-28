# Databases, proxy, and platform services

## Databases: eight engines, one shape

The query runner accepts one statement per request. A trailing semicolon and quoted semicolons are
supported; executable/nested comments, ambiguous backslash escapes, hash comments, dollar quoting and
ambiguous double-dash syntax are refused at this authorization boundary. Classification computes the
strongest risk independently for each statement, so a recognized CREATE/INSERT cannot suppress an
unknown operation's risk. Every dialect validates explain input as exactly one supported statement
before adding its fixed plan syntax; user-supplied EXPLAIN/ANALYZE options and additional statements
never reach the connection.

SQL Server resets SHOWPLAN using a bounded cancellation-independent context, including after query
failure. An uncertain enable/reset outcome discards the connection instead of returning its mode to
the pool. PostgreSQL catalog size queries treat a concurrently dropped relation's NULL size as zero;
the live regression holds an old catalog snapshot to exercise that case deterministically.

SQL integer values using a 64-bit driver representation and exact decimal values travel as decimal
strings. SQL mutation JSON uses `DecodeJSONNumbers` (`UseNumber`) and binds exact numeric strings
without an intermediate JavaScript/Go float. Query results and streamed exports also normalize nested
ClickHouse integer arrays/maps and wide integer/decimal driver wrappers. Numeric JSON syntax is
validated without parsing through float64, and clipboard SQL rejects invalid numeric literals.
`TestSQLiteExactNumericMutationBrowseAndExport` and `TestLiveExactNumericMutations` exercise adjacent
BIGINT keys beyond JavaScript's safe range through mutation, browsing and export; the latter verifies
real PostgreSQL/MariaDB DECIMAL columns when their test DSNs are available. SQLite exact decimals
require suitable storage (for example TEXT); values already rounded by SQLite's NUMERIC/REAL affinity
cannot be recovered by the API. The Redis SCAN cursor is a decimal JSON string; requests
parse it with `ParseUint` at 64 bits, preserving the full unsigned range and rejecting negative or
overflowing cursors.

`internal/dbx` drives PostgreSQL, MySQL/MariaDB, SQLite, SQL Server, ClickHouse, Oracle, MongoDB and
Redis on pure-Go drivers, so the image still needs no CGO.

Pool initialization is coordinated per connection ID. Dialing and pinging do not hold the manager's
map lock, and waiters can cancel independently. Closing or editing a connection invalidates an
initialization already in progress; its old credentials cannot publish a pool afterwards.
Completion and diagram reads batch catalogue facts for up to 500 table names per query, with the
diagram's existing 120-table cap applied first. Each dialect keeps its type spelling, key order and
referential actions. A refused bulk read falls back to the existing per-table reads so restricted
accounts retain partial results. Full table details and mutation preconditions use fresh dialect reads.

- **`Dialect` is the whole abstraction**: driver name, quote character, bind marker, pagination tail,
  catalogue queries, DDL keywords, session list, size query — one method each, six implementations. The
  old shape was a `switch driver` inside a dozen functions; it worked at three engines and broke at seven,
  because a missed switch failed at runtime rather than compile time.
- **Identifiers quoted, values bound, always.** `validateIdent` refuses only what quoting cannot fix (NUL,
  control characters) rather than a conservative character class — a table called `user-profiles` was
  listed and then refused to open. Row edits are scoped by primary key and refused without one, or an
  UPDATE the caller thinks touches one row touches all of them.
- `rowsql.go` is the one exception and does not generalise: it renders a row as an INSERT **for the
  clipboard**. Nothing executes what it produces, and no code path may call it and then run the result.
  `TestLiveRowInsertSQLQuoting` feeds `'); DROP TABLE …` to every live engine and checks the table stands.
- **Reading is separated from running**: `dbx.Classify` decides destructiveness and fails closed; the
  handler applies capability and budget by hand. Every dialect's `ExplainPlan` must describe a statement
  *without executing it* — asserted in the interface, proved by `TestLiveExplainDoesNotExecute`.
- **The diagnostic surface is what a data browser usually lacks.** `activity.go` lists what the server is
  running now with the blocking session named, turning twenty "slow" sessions into one culprit, and can
  stop one; it includes our own connections marked `self`, because hiding them made an idle server report
  an empty table that reads as a broken query. `size.go` is the per-table breakdown for when the alert
  fires and nobody knows which table grew (row counts are the engine's estimate — counting forty tables
  exactly is a full scan to answer a question about *relative* size). `search.go` finds which table holds
  a value, bounded three ways at once; those bounds are what make it safe to point at production.
- **The export is the view, not the table.** `/databases/{id}/export` takes the grid's `filters`,
  `orderBy` and `dir` and assembles the statement through the same `browseSelect` the page fetch uses,
  so a download taken from a narrowed grid is that grid. It was an unconditional `SELECT * FROM`, which
  made the panel's own promise — "applied on the server, across the whole table" — the reason the file
  looked right. The filters are parsed **before** any response header is written: once the body has
  started, a rejected filter can only arrive as JSON inside a file named `.csv`. `ExportTable` remains
  as the unfiltered form. The row cap is still 100 000 and still reported only to the audit trail, so
  the page states the cap in the menu item that spends it and sends it as the request's own `limit`.
- **An unknown row count says so.** `Table.Rows` (`estimatedRows`) is `-1` where the catalogue has no
  estimate — a table PostgreSQL has never analysed, a view, SQLite, which keeps no count at all — and
  `>= 0` only where the engine actually answered. It used to be floored to zero in three dialects and
  leaked a raw `reltuples` of `-1` in a fourth, so a catalogue that could not say how many rows a table
  held was indistinguishable from one saying the table was empty, and every table in a fresh database
  was drawn as having no rows. `Count` on request is the number that is true. The ClickHouse query casts
  before it substitutes (`ifNull(toInt64(total_rows), -1)`): `total_rows` is `Nullable(UInt64)`, and asking
  24.8 or 25.8 for a supertype of that and a signed literal is `NO_COMMON_TYPE`, which fails the whole
  catalogue — 26.x accepts either form, so the version a contributor happens to run decides whether the
  mistake is visible.
- **The diagram remembers.** `GET/PUT/DELETE /databases/{id}/diagram?schema=` keep one JSON document per
  connection and schema in `db_diagram_layouts` — positions, hidden tables, notes, colours, detail level
  and viewport — beside the saved queries that outlive a page for the same reason. Reading it is on the
  read surface; saving and resetting need `service.control` (it is dashboard state, not database state,
  so it is not in the destructive group) and are audited as `database.diagram.save` and
  `database.diagram.reset`. The server checks only that the document is a JSON object under 512 KiB:
  every field is a decision about a picture, and the diagram (`diagram/memory.ts`) is the only thing
  that decodes it, with the browser's storage as a mirror for roles that cannot save.
- **A dump for every engine with no external dependency.** Three have a client tool the image can carry;
  the rest returned `ErrUnsupported` at the moment the operator pressed the button — the worst time to
  learn a backup was never possible. `dump_sql.go` writes DDL then INSERTs over the open connection,
  ordered so referenced tables come first (alphabetical fails on the first foreign key); `dump_nosql.go`
  does Mongo and Redis as gzipped JSON Lines, Redis via `DUMP`/`RESTORE` so every type survives. A native
  tool that *fails* falls through to the built-in rather than to an error. `dumpLiteral` is the second
  place putting a value into SQL text (unavoidable — a dump is text) and is per-engine, since a backslash
  escapes on MySQL and ClickHouse and is a plain character on the other four. `Restore` picks its reader
  from the file's first bytes, not the driver: a Postgres connection may hold a `PGDMP` archive or our SQL.
- **A dump the operator can take away, and a database they can remove.** The dump stays on the server
  (restore reads it) and a copy goes to the browser immediately, because a backup whose only copy is on
  the machine it protects is not one. `/databases/{id}/backup/download` takes a **name**, not a path, and
  contains it with a `files.Service` scoped to that connection's dump directory — [invariant 6](../security/invariants.md#invariants-that-must-not-regress) with the
  right root, since narrowing `JD_FILE_ROOTS` must not stop us handing back a file we wrote.
  `DELETE /databases/{id}/database` needed `DropDatabaseSQL` and `AdminDatabase` (Postgres and SQL Server
  refuse to drop the database the session is inside) and has no verb at all on two engines — a SQLite
  database is a file to unlink, a Redis keyspace can only be emptied, which `DropResult.Gone` reports
  rather than pretending. The connection is deleted with the database when it was that connection's own.
  A managed deployment network binding blocks forgetting the connection or dropping its database before
  data is changed; remove that network through deployment lifecycle first. The same route takes
  `removeContainer: true`, which removes the Docker container behind the connection (found by its
  published port at the saved loopback address) and the named volumes it mounted, without signing in —
  the delete that still works when the stored password no longer matches, which is what a container
  started over an older data volume produces. A compose-owned container is refused (`compose_managed`);
  a volume another container uses is kept by Docker and reported as a warning, never forced.
- **Where a database is reachable from is a reading and a switch, not a trip through three pages.**
  `GET /databases/{id}/access` (admin) reports the container behind a connection, whether the sync
  would re-adopt it (`detected` — the page draws no Remove row for one that would come straight back),
  its exposure read off the port binding (`local`, `public`, `private`, `remote`), the machine's public
  addresses and the firewall's part of the answer (present, on, an allow rule for the port from
  anywhere, editable). `PUT /databases/{id}/access` with `exposure: local|public` recreates the
  container through `dockerx.Recreate` with the one host address changed — the park-and-restore path
  the Docker page uses, on the same named volume — then adds a `tcp` allow rule for the port where the
  firewall is on and writable, or removes only the rule this dashboard wrote (matched by its
  comment). It sits in the destructive group beside the Docker page's Recreate, is refused for a
  compose-owned container, and audits as `database.access.change` with the binding before and after
  and what the firewall did. Publishing a database port to every interface is the operator's explicit,
  audited decision, the same one the Docker page already allows; invariant 7 is about what the
  dashboard itself binds. `GET /databases/{id}/url?target=public` hands back the saved string with the
  machine's public address (IPv4 preferred) in place of loopback, for pasting on another machine.
- **A database made from the Databases page is reachable from anywhere unless the switch says
  otherwise.** `POST /databases/provision` takes the same `exposure` (`provisionBinding`; empty means
  `public`): the port is published on `0.0.0.0` and the same `setDBFirewall` opens it, with the
  exposure, the firewall's word and any firewall error in the `database.server.provision` audit and in
  the `202` reply (`firewallError` is surfaced as a warning by the dialog). The saved connection still
  dials loopback — `hostAddress` maps a `0.0.0.0` binding to `127.0.0.1` — so the string under "From
  anywhere" is the public-address form of the same row. Deployment quick setup sends `exposure: local`
  because its database is reached over the deployment network.
- **Live tests skip rather than fail**, or a suite failing for want of a database teaches people to ignore
  it. Every bug this feature shipped was a catalogue query a unit test string-matched identically and only
  the engine rejected — SQL Server refusing `ADD COLUMN`, a size query summing every index_id and
  reporting four times the real size, Postgres's `now()` being the *transaction* timestamp and so
  reporting a negative session age. Oracle has an optional live fixture using `JD_TEST_ORACLE_DSN`;
  without a configured server, its unit coverage does not establish live-engine compatibility.

- **The section opens on every database at once.** `GET /databases/fleet` dials every saved
  connection concurrently (six at a time, twelve seconds each) and hands back the row's facts with
  what the server answered: reachable, version, latency, the database's size where the engine
  reports one, its tables/collections/keys, sessions less this dashboard's own pool, where it runs
  (`docker`, `host`, `remote`, `file` — read through the same `describeDBAccess` the Connection
  page uses, so the two agree), its exposure, how many deployment environments are bound to it,
  and when its newest dump landed. For an administrator it also lists what the sync would report
  and could not connect — a container with no reachable port, a native server waiting for a
  password — read without writing anything. `GET /databases/topology` (and `/{id}/consumers` for
  one connection) joins four sources into nodes and edges: `deploy_database_bindings` with the
  project and environment names, running containers whose environment names the server's
  address, container name, compose service or `db-N.jd.internal` alias (one concurrent inspect
  pass, eight at a time), compose-stack siblings and shared user networks (drawn dashed, as a
  link that could carry), and the engine's own session list with client addresses mapped back to
  containers by IP, to this host for loopback and bridge-gateway addresses, or to another
  machine. A binding's word (`connected`, `stale`, `broken`) outranks an observation's. Both are
  on the read surface; the detected-but-unconnected half is filled only for `system.admin`.
  Fleet, topology and consumer reads reuse one Docker list and one inspection per container within
  their request, including the fleet's administrator-only discovery. Each request starts fresh; the
  snapshot is never used for access-changing actions.
- **The server behind a connection.** `dbx.Admin` is an optional second interface a dialect
  implements — Postgres, MySQL/MariaDB, ClickHouse and SQL Server do; SQLite has no server and
  Oracle's account model does not fit — with Mongo (`usersInfo`/`createUser`/`grantRolesToUser`)
  and Redis (`ACL LIST`/`SETUSER`/`DELUSER`, saved where an aclfile exists) mapped onto the same
  `Role` shape. Routes under `/databases/{id}/server/`: `roles` (list on the read surface; create,
  alter and `/{name}/grant` under `system.admin`; drop under `s.destructive`, refused for the
  account the connection signs in with), `databases` (create, and `/connect` to save a sibling
  connection to another database on the same server under the same credentials, probed before it
  is stored), `extensions` (list; create and drop for Postgres, listed only for MySQL's plugins)
  and `settings` (a curated read of `pg_settings`, `global_variables`, `system.server_settings`,
  `sys.configurations`; Mongo's `serverStatus` and Redis's `INFO` flattened to the same rows).
  Identifiers go through the dialect's `QuoteIdent`; a grant runs inside the target database on
  the engines that grant from there (`GrantNeedsDatabase`) through a pool opened for that one
  statement; a password is the one value no engine binds in `CREATE ROLE`/`CREATE USER`, so it is
  refused if it carries a control character and quoted by the same per-engine rule `dumpString`
  applies (`passwordLiteral`). Audited as `database.role.create/alter/drop/grant`,
  `database.create`, `database.connection.sibling`, `database.extension.create/drop`.
- **The advisor and statement statistics.** `GET /databases/{id}/advisor?schema=` runs
  `dbx.Advise`: generic checks over the introspected structure on every SQL engine (tables with no
  primary key, foreign keys no index begins with, capped at 300 tables), plus an engine's own
  `Adviser` where it keeps statistics — Postgres (unused indexes over 1 MiB, identical indexes,
  never-analysed tables, dead rows past a fifth, sequences past 80 % of their ceiling, connections
  past 80 % of `max_connections`, a cache hit ratio under 90 %, sessions idle in a transaction
  for five minutes, `password_encryption = md5`, login superusers besides `postgres`, no
  `pg_stat_statements`) and MySQL (non-InnoDB tables, the slow log and `performance_schema` off,
  superusers at host `%`, a buffer pool under 128 MiB). The handler prepends one finding no
  catalogue can make, for an administrator: the server published on every interface with the
  firewall off or open. Each finding carries the objects it names and, where one statement fixes
  it, that statement; nothing is executed. `GET /databases/{id}/statements` reads
  `pg_stat_statements` (13+ and older column names both) or
  `performance_schema.events_statements_summary_by_digest`, top N by total time, and reports
  `supported: false` with the reason where neither is there.
- **The server's own log, found from its connection.** `GET /databases/{id}/logs/sources`
  (`handlers_db_logs.go`, on the read surface — reading what the server printed is what `/activity`
  already shows any role) answers `{sources, refused?, reason?, note?}`, each source in `/logs/sources`'
  shape plus `primary`, read through the engine's lens by the connection's driver (`driverLens` — a
  Postgres in a custom image is still Postgres). A SQLite file and a server on another machine have none,
  with the reason. A container behind the connection is `docker:<name>` — the name, so the log survives
  a recreate. One the connection dials at its own private address is found by that address whatever
  its image (`containerAt`): one built in-house, or whose moved tag the list names by id, is still the
  engine the connection says. A server on this machine is followed from its port: the listener (`proxysvc.ListListeners`;
  behind `docker-proxy`, the container publishing the port, whatever its image says), its process's
  manager (`procs.ManagerOf`, where a `container` manager is `docker:<name>` again), the files it and up
  to 64 of its children hold open **for appending** (`/proc/<pid>/fd` with `fdinfo`'s flags, log-like
  names only — a data file is opened for writing, a log for appending), the engine package's conventional
  files that exist (`/var/log/postgresql/postgresql-<V>-<C>.log` from the Debian unit's instance name,
  MySQL's, Redis's, MongoDB's, ClickHouse's), then the unit's journal. The first file is primary even at
  0 bytes — rotation just emptied it, and the page opens it with its rotated set — and a journal is
  primary only when no file is the server's (Debian's Postgres writes nothing there but systemd's starts
  and stops). A path the log roots refuse is listed under `refused` with the reason rather than dropped,
  and the journal beside it is not made primary, since the statements are in that file. With nothing
  listening — stopped, crashed or starting, which is when a log is read most — it is found the ways a
  stopped server can be: a container publishing the port without `docker-proxy`, the container the
  connection is named after (the sync names adopted containers so), running or not, and the engine's
  units by name, skipping one serving another port and Debian's umbrella `postgresql.service`, with
  their files and journals and a `note` saying the server is not answering. Nothing goes through
  `hostexec`: `/proc` is read, the units come from the same systemd listing `/logs/sources` uses, and
  every file through `logs.Allow`. One resolution answers for 45 seconds per connection
  (`Server.dbLogSourcesKept`, keyed on a hash of the DSN, never stored when the request's deadline cut
  it short), since the page and its Queries view ask on different cadences and each asking walks every
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
  `/activity` and `/statements`, it returns statement text with its literals at read capability.
- **The dumps on disk.** `GET /databases/{id}/backups` lists the connection's dump directory
  newest first with each file's size and format; `DELETE /databases/{id}/backups` (destructive,
  not typed: the database is still there to dump again) removes one, contained against that
  directory exactly as the download is. The fleet reads the same listing for "last backup".
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
  an existing connection to the same address is re-sealed rather than duplicated. Audited as
  `database.connection.host.grant` with the account and outcome, never the password.
  `TestLiveHostPostgresAccount` exercises it as root against a real native server
  (`JD_TEST_HOST_PG_PORT`).

### Database provisioning for deployments

Deployment setup reuses `/databases/provision`, `/adopt`, `/ping` and the explicit admin URL read.
A project's Databases settings reuses the same two reads for a linked connection: `/ping` behind its
Test connection verb, and `?target=container` behind Copy application URL, which stays admin-only and
audited there as everywhere else.
Quick setup provisions with `exposure: local`, so those ports are published to host loopback only;
the Databases page's own dialog defaults to every interface (above). The data volume is named
`<container>-data`. With no name supplied, provisioning reserves the first available `jd-<engine>`,
`jd-<engine>-2`, etc., checking both containers and retained data volumes. In-flight requests reserve
distinct names before pulling images. Explicit names remain exact and provisioning refuses with
`409 volume_exists` when that volume already exists:
an official image that finds a populated data directory skips initialisation, so the freshly generated
password is never set and the server refuses every sign-in while looking reachable. The `/ping` reply's
`error` is surfaced by both creation dialogs so an engine's own refusal is not reported as "not ready".
Besides the five engines, provisioning offers `pgvector` (`pgvector/pgvector:pg16`) and `postgis`
(`postgis/postgis:16-3.5-alpine`, listed and accepted only on x86-64, the one architecture it is
published for): the same PostgreSQL 16 contract with the extension a retrieval or geospatial schema
creates on its first migration, which the official image lacks. Deployment setup
preselects one when detection read that extension from the schema. `mongodb` is refused before any pull
on a CPU without AVX (x86-64) or ARMv8.2 atomics (arm64), where MongoDB 5 and later die with an illegal
instruction. The URL read also takes `format` and `database`; see
[deployment database networks](../deployments/database-networks.md).
`/adopt` is idempotent by driver, address, database and login user: a matching connection gets that row
back, and when the container's password differs the row is re-sealed in place while preserving its
transport and query options (audited as `database.connection.refresh`, pool dropped). Different databases,
users and engines on one address are never overwritten; ambiguous duplicate matches require an explicit
saved-connection choice. This keeps replacement credentials current — a database
removed and created again under the same name takes the same loopback port back with a new password,
and a `${{database.N}}` reference must keep resolving to a URL that works.
The URL endpoint's `target=container`
option resolves a matching Docker database to a stable `db-ID.jd.internal` hostname without changing networking or the saved DSN;
`target=host` and the default retain the original DSN. Replies are non-cacheable and audits contain
the connection identity and target, never credentials. Setup saves the returned typed database reference.
Activation and reconciliation own the environment network and database alias, including replacement
with a different IP. See [deployment database networks](../deployments/database-networks.md).

Redis provisioning writes its generated password to a mode-0600 configuration inside the container
before the official entrypoint drops privileges. Redis requires explicit password configuration;
setting `REDIS_PASSWORD` alone does not enable authentication. The bootstrap is constant shell text,
with the generated secret read from the environment rather than placed in argv.
See [Redis configuration](https://redis.io/docs/latest/operate/oss_and_stack/management/config/).
MongoDB detection records `authSource=admin` when credentials come from `MONGO_INITDB_ROOT_USERNAME`,
so selecting an application database does not change where the root account authenticates.
See the [official MongoDB image](https://hub.docker.com/_/mongo/).

`JD_DEPLOY_LIVE=1 go test ./internal/api -run TestLiveDeploymentDatabaseConnection -count=1 -v`
provisions all five supported quick-setup engines, adopts and pings them, authenticates from separate
application containers, replaces each database at a different IP, and authenticates from the same client
container using its unchanged URL after reconciliation. It verifies loopback-only publication, network
ownership and cleanup, then removes its own containers/volumes/networks.

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
- **Site builder** (`sites.go`, `sites_render.go`, `sites_parse.go`, `sites_apply.go`). `SiteSpec` is our
  shape, not nginx's, for the reason `ContainerSpec` is not `container.Config`; rendering happens **on the
  server** so a spec has one meaning, and the output is hand-written rather than templated because order
  carries meaning to whoever maintains the file after this dashboard is gone. `ApplySite` puts the **symlink in
  before `nginx -t`** — a new file in `sites-available` is not in the include tree, so the test has
  nothing to say about it — and undoes both together on failure. Four renderer details:
  - The ACME challenge location goes **above** the catch-all redirect, or renewal silently stops and
    nobody finds out for sixty days.
  - `http2 on;` is a directive, not a `listen` parameter (nginx 1.25 warns on every reload).
  - WebSocket upgrades pass `$http_connection` through rather than a `$connection_upgrade` map. The
    reason once given here — that a `map` is only legal in the `http` block and a site file cannot reach
    it — is **false**: `sites-enabled/*` and `conf.d/*.conf` are both included *inside* `http {}`, so the
    top of a site file, outside its `server` blocks, is http context. `map`, `upstream`,
    `limit_req_zone`, `limit_conn_zone`, `proxy_cache_path`, `geo` and `log_format` written there pass
    `nginx -t` (checked on nginx 1.26.3) and are deleted with the site. Their names are global to the
    whole configuration and a duplicate zone is an emergency, so each is named with `NginxIdent(site)`.
    The comment the renderer writes into the generated file still carries the old reason; it is output,
    and changes with the renderer.
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
  the whole site on the next save.
- **`tlsscan.go` — what the domain actually serves.** Everything else on the page reads files, which
  cannot see a certificate renewed and never reloaded, a proxy still offering TLS 1.0, or a redirect that
  quietly stopped. Each version is probed on a connection pinned to exactly that version; a version this
  client will not ask for is `unknown`, **never `refused`**, since reporting it absent would be false
  reassurance about the versions that matter most. `grade` is a pure function of the scan. The live
  certificate, TLS and DNS probes require `system.admin`: each emits traffic to a caller-chosen
  destination, the same scanner boundary as `/network/probe`.
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
- **The editor's read route is not a password reader.** `ReadConfig` refuses the dashboard's own
  `jd-auth` directory and any `.ht*`/`htpasswd` file with `ErrProtectedFile` (403 `protected_file`):
  `GET /proxy/config` is held by every signed-in account and a list of bcrypt hashes is not
  configuration. `Validate` now carries a `Note` when the file is outside nginx's include tree —
  no `sites-enabled` link, a `conf.d` file without `.conf`, a stream with no include — because
  `nginx -t` passes a file it never reads, and a dry run that says "valid" about a disabled site is
  a false reassurance. `POST /proxy/test` runs the engine's own test against what is on disk without
  staging or reloading anything (admin, audited as `proxy.config.test`); the overview's Test config
  button and the reload/restart/start/stop verbs sit beside it, the last four through the existing
  `/systemd/{unit}` routes so the two pages never disagree about the unit.
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
  `DeleteSite` asks `checkSiteDelete` first and refuses when `sites-enabled/<name>` is not the site's
  own link — a copy there (the file nginx really serves, which it removed with no backup), a link to
  another file (which it took out of nginx) — or when a link under another name would be left
  pointing at nothing.
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
- **Certificates say who uses them.** `listCertificates` joins the sites' `ssl_certificate` paths
  onto the certificate list (`UsedBy`), through symlinks, so a certbot lineage and the site naming
  its `live/` path are one entry; `certificateName` names a file in a generic directory
  (`/etc/nginx/ssl/site.crt`) after itself rather than after "ssl". `CertbotState.RenewUnit` is a
  certbot timer systemd knows but is not running, which the Certificates page enables and starts
  through `/systemd/{unit}/enable` and `/start`. `DNSProvider.HasCredentials` reports a saved
  token without reading it, and `DELETE /certificates/dns-credentials/{provider}` removes one
  (destructive, ordinary confirmation, audited as `certificates.dns.credentials.remove`).
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
- **A password file must be readable by the account that reads it.** nginx opens `auth_basic_user_file` in
  a *worker* (www-data/nginx/http), not as the root that wrote it, so a 0640 root:root file is a 403 for
  every visitor and "Permission denied" in the log — which reads exactly like a wrong password.
  `nginxWorkerGID` takes the account from this host's `nginx.conf`, falling back to the three defaults;
  where none resolves the file is 0644, which is what `htpasswd` itself produces. `authDir`/`streamDir`
  hang off `JD_NGINX_DIR`, which exists precisely for hosts whose nginx is elsewhere. A missing file is
  not an nginx error: `nginx -t` passes, reloads succeed, and the site answers every login with 403 and
  an error line per request (checked on 1.26.3), so deleting a password file a site uses fails silently.
  Removing a file's last login keeps it empty, which asks for credentials again like a wrong password.
- **`import.go`** checks the key against the certificate **before** writing either: a mismatched pair is
  accepted by every text editor and refused by nginx at reload, which on a live server means finding out
  during an outage. Imports live in `/etc/ssl/just-dashboard`, so a renewal run can never prune a
  certificate it did not issue.
- **`streams.go`** — nginx's `stream` is a sibling of `http`, so a stream cannot live under
  sites-available. They go in `/etc/nginx/stream.d`, and the page says plainly when `nginx.conf` does not
  include it. nginx.conf itself is never edited from here: everything else on the host depends on it.
- **`htpasswd.go`** does bcrypt in process — `htpasswd` lives in apache2-utils, is not installed on a host
  running nginx, and would put the password in a world-readable argv.
- **`certbot.go`** issues, renews and revokes. `renewalScheduled` has its own field because it is the real
  story behind almost every expired certificate: not a forgotten renewal, a timer that stopped months ago.
  Issuance defaults to `--staging` in the UI (the real limit is five failures an hour). `dns.go` answers
  "does this domain point here yet" and recognises Cloudflare explicitly, since reporting a CDN as a
  misconfiguration is the commonest false alarm of this kind.
- **The proxy routes are mounted per area**, each from its own file beside its handlers, and composed in
  `mountProxyRoutes` (`api/handlers_proxy.go`): `mountEngineRoutes` (status, the raw config editor,
  validate, test, reload; `handlers_proxy_engine.go`), `mountVHostRoutes` (the listing, what nginx has not
  loaded, the enable switch and the removal of a stray link; `handlers_proxy_vhosts.go`) and `mountProxyInsightRoutes` (`handlers_proxy_insights.go`) inside
  `/proxy`; `mountSiteBuilderRoutes` (`handlers_proxy_sites.go`) and `mountSiteOpsRoutes`
  (`handlers_proxy_siteops.go`) inside `/proxy/sites`; `mountStreamRoutes` (`handlers_proxy_streams.go`),
  `mountAuthFileRoutes` (`handlers_proxy_auth.go`) and `mountProxyToolRoutes` (`handlers_tls.go`, where
  the whole subtree is `system.admin` because every tool probes a caller-chosen destination) at
  `/proxy/streams`, `/proxy/auth-files` and `/proxy/tools`; `mountCertificateRoutes`
  (`handlers_certificates.go`) and `mountTLSRoutes` (`handlers_tls.go`; the watch list's handlers stay in
  `handlers_domains.go`) inside `/certificates`; and `mountPortRoutes` (`handlers_ports.go`) at `/ports`,
  which chi serves with and without the trailing slash. `TestProxyRoutesKeepTheirPaths` pins every path,
  method and gate as they stood before the split. The whole group runs `withProxyActor`, which puts the
  signed-in account on the context for the change record below. Background work and state the proxy
  pages keep beyond `proxysvc.Service` go in `api/modules_proxy.go` (`initProxyExtras`, run last in
  `initModules`; `startProxyExtras` from `Start`; `stopProxyExtras` from `Shutdown`, which also runs for a
  server never started), and their tables in `store/schema_proxy.go`'s `proxySchema`, which `Open` applies
  after `applyAddedColumns` and `TestProxySchemaIsAdditive` holds to `CREATE … IF NOT EXISTS`. A table
  created there that later gains a column through `addedColumns` has to move into `schema` in the same
  change, since `applyAddedColumns` runs first on a fresh install.
- **What the engine's own test says, not only whether it passed.** `runValidator` fills
  `ValidationResult.Diagnostics` (`diagnostics.go`: level, message, and file and line where nginx names
  them) and `Warnings`, the warn-level count — nginx exits 0 through a conflicting server name it is
  "ignoring", and that site then never serves. nginx writes a test's messages as `nginx: [warn] … in
  /path:12` when it can open its startup error log (root on the host) and as the timestamped error-log
  line when it cannot; both are read. `ParseCaddyDiagnostics` reads `caddy validate`'s JSON warnings and
  its `Error:` line, best effort, taking the position after `, at ` before any other path-like text so an
  upstream URL is never read as one. A diagnostic's file is named with its symlinks resolved, the form
  `allowedPath` gives the file being edited, so a Debian site's error names its sites-available file
  rather than the sites-enabled link nginx included. Caddy is validated against a temporary copy, and
  `validateCaddy` replaces the copy's name with the file's throughout the result; `Validate` holds a
  Caddy path to the proxy's directories as it does an nginx one.
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
  committed — `WriteConfig`, `ApplySite` and deployment cutovers through `applySiteLocked`, `DeleteSite`,
  `SetVHostEnabled`/`ToggleVHost` (which now take the service lock like every other change, resolve the
  site file as the writes do, and record nothing for a toggle that leaves the links as they were or that
  nginx refused), `RemoveVHostLink` (recorded as a disable of the file behind the link, or of the link
  itself when it points at nothing), `ApplyStream`,
  `DeleteStream`, and a deployment route's restore — with its prior content and the actor
  `WithActor` put on the context (empty for a deployment or a background loop). Password files are never
  recorded, a site file that resolves outside the proxy's directories is recorded without content, and
  the htpasswd writers do not call it; a recorder that fails is logged and the change stands.
  No recorder is attached yet.
- **Certificates carry their fingerprint and serial**, the SHA-256 of the DER and the serial number in
  the uppercase colon form `openssl x509 -fingerprint -sha256` prints, which
  `TestCertificateFingerprintMatchesOpenSSL` checks against openssl itself.

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

- `Manager.Start(spec, run)` returns immediately; the API answers `202`. `Emitter` gives runners `Status`,
  `Line`, and `Run`/`RunEnv` through `hostexec`. A slow subscriber is skipped rather than allowed to stall
  the command — the buffer is the record, the channel only the tail.
- Bounded: 5000 lines a job, 64 KB a line, 50 jobs. `prune` runs on finish as well as on start, or a burst
  ending after the last `Start` sits over the cap until something else happens, which on an idle dashboard
  is never. A running job is never pruned.
- **Validation stays synchronous** — a bad email, a wildcard over HTTP, an sshd change that would lock the
  operator out — so a refusal answers the click rather than arriving a minute later as a failed job. That
  is why `certbot.go` exposes `IssueArgs`/`RenewArgs`/`RevokeArgs`, `netsec.PlanSSHSettings` is separate
  from `ApplySSHPlan`, and `updates.UpgradeCommand` returns an argv.
- `GET /jobs/{id}/stream` sends job, backlog, then batches every 120 ms. Cancelling is `service.control`.

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
- It **fast-forwards, never resets** — unlike a managed deployment checkout, this is the operator's own
  checkout and an
  edited compose file is normal, so a local change survives unless it genuinely collides. And it **waits
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

Update, apply, restart and rebuild admission share `JD_DATA_DIR/lifecycle.lock`, held before either
run record, transcript or `.env` backup can be replaced. Both durable run records are checked under
that lock: a pending/running operation blocks the other path even after the launching process exits.
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
It installs only missing curl/OpenSSL/Certbot packages using apt, dnf, yum, apk, zypper or pacman, validates
Certbot's HTTP authenticators and enables an existing packaged renewal timer. Package failures stop setup.
The same `jd_pkg_install` dispatch (one `apt-get update` per run) backs `jd_install_terminal_extras`,
which `install.sh` runs when the terminal is enabled: it installs `zsh`, `zsh-autosuggestions` and
`zsh-syntax-highlighting` where missing and, on success, appends `JD_TERMINAL_SHELL=<zsh path>` to a fresh
`.env`. A re-run that kept its `.env` asks first, and never touches a file that already names a shell.
That install is best effort: a failure is a warning, and the terminal opens the account's own shell.
`jd_install_host_tools` runs on every install and re-run, before any question: the web terminal is a host
shell, so its git and the Git page's GitHub sign-in need host packages, and Security → Tools runs `whois`
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
