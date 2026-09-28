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
    deployment cutovers still get the error, since their recovery is built on it. Not seen: a reload the
    master refuses after `nginx -s reload` has returned 0, which only says the signal was delivered. The
    case that matters is a listen address another process holds — `nginx -t` passes it, because its test
    ignores `EADDRINUSE` — where the master logs `[emerg] bind() … failed (98: Address already in use)`
    five times and keeps its old configuration (checked against nginx 1.26.3), and the save still says
    "is live". Catching it needs the reload verified from the master's side (its error log or its
    workers), which the engine's reload does not do either.
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
  (`TestSaveSiteKeepsAHandWrittenFileAsBak`).

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
  It deletes the file and leaves any other link, or a file of its own in sites-enabled, where it is. A
  name that only such a link holds is "no such site", and the error says what holds the name
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

  **Maintenance and error pages** (`site_pages.go`, form section "Maintenance & error pages",
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
  `VHost.rateLimited` puts "rate limited" on the Sites card.

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
  cache manager evicts. `VHost.cached` shows the verb.

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
  passive only (`max_fails`/`fail_timeout`); stock nginx has no active checks and the form says so.
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
- **Certificates say who uses them.** `listCertificates` joins the sites' `ssl_certificate` paths
  onto the certificate list (`UsedBy`), through symlinks, so a certbot lineage and the site naming
  its `live/` path are one entry; `certificateName` names a file in a generic directory
  (`/etc/nginx/ssl/site.crt`) after itself rather than after "ssl". `CertbotState.RenewUnit` is a
  certbot timer systemd knows but is not running, which the Certificates page enables and starts
  through `/systemd/{unit}/enable` and `/start`. `DNSProvider.HasCredentials` reports a saved
  token without reading it, and `DELETE /certificates/dns-credentials/{provider}` removes one
  (destructive, ordinary confirmation, audited as `certificates.dns.credentials.remove`).
- **Two layouts, and files that are not sites.** `nginxVHosts` (`vhosts.go`, with the rest of the
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
  hang off `JD_NGINX_DIR`, which exists precisely for hosts whose nginx is elsewhere.
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
  validate, test, reload; `handlers_proxy_engine.go`), `mountVHostRoutes` (the listing and the enable
  switch; `handlers_proxy_vhosts.go`) and `mountProxyInsightRoutes` (`handlers_proxy_insights.go`) inside
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
  committed — `WriteConfig`, `SaveSite` and deployment cutovers through `applySiteLocked`, `DeleteSite`,
  `SetVHostEnabled` (which now takes the service lock like every other change, resolves the site file
  as the writes do, and records nothing for a toggle that leaves the link as it was), `ApplyStream`,
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
