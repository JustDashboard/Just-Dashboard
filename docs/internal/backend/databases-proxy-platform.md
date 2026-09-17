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
  data is changed; remove that network through deployment lifecycle first.
- **Live tests skip rather than fail**, or a suite failing for want of a database teaches people to ignore
  it. Every bug this feature shipped was a catalogue query a unit test string-matched identically and only
  the engine rejected — SQL Server refusing `ADD COLUMN`, a size query summing every index_id and
  reporting four times the real size, Postgres's `now()` being the *transaction* timestamp and so
  reporting a negative session age. Oracle has an optional live fixture using `JD_TEST_ORACLE_DSN`;
  without a configured server, its unit coverage does not establish live-engine compatibility.

### Database provisioning for deployments

Deployment setup reuses `/databases/provision`, `/adopt`, `/ping` and the explicit admin URL read.
Provisioning publishes database ports to host loopback only. The URL endpoint's `target=container`
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
- **Site builder** (`sites.go`, `sites_render.go`, `sites_apply.go`). `SiteSpec` is our shape, not
  nginx's, for the reason `ContainerSpec` is not `container.Config`; rendering happens **on the server**
  so a spec has one meaning, and the output is hand-written rather than templated because order carries
  meaning to whoever maintains the file after this dashboard is gone. `ApplySite` puts the **symlink in
  before `nginx -t`** — a new file in `sites-available` is not in the include tree, so the test has
  nothing to say about it — and undoes both together on failure. Four renderer details:
  - The ACME challenge location goes **above** the catch-all redirect, or renewal silently stops and
    nobody finds out for sixty days.
  - `http2 on;` is a directive, not a `listen` parameter (nginx 1.25 warns on every reload).
  - WebSocket upgrades pass `$http_connection` through rather than a `$connection_upgrade` map — a `map`
    is only legal in the `http` block and a site file cannot reach there.
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
- **Two layouts, and files that are not sites.** `nginxVHosts` reads sites-available where it exists and
  conf.d where it does not (every RPM distro, Alpine, Arch — most of the servers this runs on); the
  difference reaches the UI as an empty `EnabledPath`, because conf.d has no symlink and a switch that can
  only error is worse than none. `confdPath` stops `app.conf` becoming `app.conf.conf`. The listing
  **skips backups** (`isBackupFile`: `.bak`, `~`, `.dpkg-old`, `.rpmsave`) — nginx reads none of them, and
  since delete keeps `<name>.bak`, without the filter deleting a site produced a second site.
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
  `DELETE /api/v1/dashboard/config/run`, all `system.admin`, the two mutations inside `s.destructive`.

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
The backend image still includes Certbot for container execution; host installation supplies host tooling
and the distribution renewal schedule. `python3 scripts/test_install_dependencies.py` verifies the
installer with fake package commands, without changing host packages. Hostname readiness returns
`certificateIssue` alongside `certificateMethod`, preserving listener/plugin errors for Quick Deploy.
Quick Deploy keeps HTTPS selected when readiness fails, presenting the actual blocker instead of
silently switching the initial configuration to plain HTTP.
