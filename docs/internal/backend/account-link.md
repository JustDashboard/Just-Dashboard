# Linking to a just-dashboard.com account

A person can link this dashboard to their account on the project's site (`JD_ACCOUNT_URL`, by default
`https://just-dashboard.com`). The site then lists the server with its health, version and the way to reach
it, and, at a scope the person chose on this machine, can ask it to do a few things. It is optional, off until
an administrator does it, and can be switched off outright with `JD_ACCOUNT_LINK=false`. Everything lives in
`backend/internal/accountlink`; the Settings page is `frontend/src/app/(dashboard)/dashboard/link`
([feature map](../frontend/feature-map.md)).

**The wire contract is not in this repository.** The protocol is
[`docs/server-link-protocol.md`](https://github.com/JustDashboard/frontend/blob/main/docs/server-link-protocol.md)
in the site's repository, and both sides build to that one file, including its test vectors, which
`protocol_test.go` carries (key fingerprint, two signed requests, two signed commands). A change to it is a
change to both repositories. This page records how the dashboard implements it and the decisions that file
leaves to the dashboard.

## Trust model

- **Outbound only.** The dashboard is not reachable from the internet (invariants 1 and 7) and linking does
  not change that: the dashboard sends a heartbeat and holds a long poll open, and the site never opens a
  connection to it. No listener, allowlist entry or `JD_ALLOWED_CIDRS` is touched.
- **Two keys, two directions.** This install has an Ed25519 identity, made on first use and sealed with the
  master key in `settings['account.link.identity']`; it signs every request that the protocol asks to be
  signed. The site has one signing key; its public half is pinned in the link state at approval and signs
  every command. A copy of the site's database can read snapshots; it cannot make this server act.
- **The dashboard stays sovereign.** The scope is chosen here, stored here and enforced here. The site only
  draws the controls the reported scope allows. Every command is verified before anything runs and written to
  this dashboard's own audit log, whatever its outcome.
- **A code is a pointer, not a credential.** The code a person types on the site names a request that is bound
  to this install's public key. Completing the link needs the private key, which is only ever in the sealed
  store, so the installer and `manage.sh` handle no secret: they print what the Go command prints.
- **Linking is a human act.** In the browser it is a session (an API key is refused with `session_required`)
  of an account with `system.admin`; at the terminal it is root. Unlinking is in the destructive budget.

## The flow and its three doors

The Settings page (`POST /dashboard/link`), the question in `install.sh` and `manage.sh link` all call
`Service.BeginLink`; there is no second path.

1. `BeginLink` makes the identity if needed, asks `GET /site` whether the site speaks protocol 1, then
   `POST /requests` with the public key, the scope and a description of the machine (name, hostname, version,
   OS, architecture, each cut to the protocol's limits). The answer is checked before it is stored or shown:
   a request id and a `ABCD-1234` code of the documented shape, an approval address **on the configured
   origin** (an address anywhere else is refused, since the page opens it in a new tab; origins are compared
   by what they mean, so `https://Just-Dashboard.com:443/link` is on `https://just-dashboard.com`, which is
   also how `ParseSiteURL` spells `JD_ACCOUNT_URL`), and an expiry within the hour. The request goes to `settings['account.link.request']`.
2. A person opens the approval page, signed in, and approves it.
3. `PollLink` polls `GET /requests/{id}` signed with the install's key. Approval arrives exactly once; the
   site key, server id, owner label and intervals are validated and stored in one transaction
   (`settings['account.link.state']`) and the request is deleted. `request_denied`, `request_expired` and an
   expiry by the local clock end the request with an outcome (`account.link.outcome`) that the page shows once.
4. From then on the connector heartbeats and polls for commands.

**Who polls a pending request** is decided by its `Driver`. A request begun in Settings is polled by the
running dashboard (every `pollSeconds`). A request begun by `--admin link` is polled only by that command, so
the approval, which the site delivers once, is never collected by two processes; if the other process did
collect it (`request_consumed`) the poller looks at the stored state and reports it as linked. A terminal
command that was killed outright leaves its request behind with nobody polling it; the next terminal command
replaces it, whereas a request from Settings is never taken over from the terminal.

**A link made at the terminal reaches a running dashboard without a restart.** While nothing is linked the
connector looks at the stored state every 15 seconds, and a running link checks every 5 seconds that it still
is the stored one. An unlink at the terminal therefore stops the heartbeat and the poll, and a re-link starts
them.

Unlinking tells the site (`POST /unlink`, signed), forgets the link state and everything the site set (the
update policy), and keeps the identity so a later link shows the same fingerprint. If the site cannot be
reached the link is cleared anyway; the result says the site was not told. A `410` on any signed request
means the owner removed the server on the site, and clears the link the same way (audited as
`account.unlink` by `system`).

## Files

| File | What it holds |
| --- | --- |
| `protocol.go` | Scopes and kinds, the key fingerprint, the signed request and command strings, canonical JSON, strict response decoding |
| `identity.go` | The sealed Ed25519 identity; an identity the master key cannot open is replaced only while nothing is linked |
| `state.go` | Settings keys, `State`, `Request`, the persisted sequence (`UPDATE ... RETURNING`, committed before the request is sent) |
| `client.go` | The signed HTTP client: TLS 1.2 at least, 15 s per request plus the long poll's wait, no redirects, a 64 KiB request and 256 KiB response bound, one retry path for `replayed_sequence` |
| `link.go` | `BeginLink`, `PollLink`, `AwaitLink` (the terminal's wait), `CancelLink`, `SetScope`, `Unlink`, `SyncNow` |
| `service.go` | Options, the loops (top, heartbeat, command, policy, watch), backoff, rate pacing, key rotation, `410` handling |
| `commands.go` | `verify` and `process`: the checks, the replay table, the audit entry and the result sent back |
| `executor.go` | The closed switch over kinds and the `Host` interface the `api` package implements |
| `snapshot.go` | The heartbeat body and `Redact(scope)` |
| `policy.go` | The automatic-update policy and when it is due |
| `status.go` | `Status`, the JSON `GET /dashboard/link` returns |
| `siteurl.go` | `ParseSiteURL`, used by `config` and the terminal command |
| `linkstub/` | A signature-less stand-in site for the tests of the packages that drive a link |

Outside the package: `api/handlers_accountlink.go` (routes), `api/accountlink_host.go` (the `Host`
implementation over the services the pages use), `admin/link.go` (the terminal commands), the table in
`store/account_link_schema.go`, and `JD_ACCOUNT_*` in `config`.

## State

All of it is in the `settings` table; only the identity is secret.

| Key | Holds |
| --- | --- |
| `account.link.identity` | The Ed25519 seed, sealed with `auth.Sealer` |
| `account.link.state` | The link: site URL, server id, pinned site keys, scope, who linked and when, owner label, intervals |
| `account.link.request` | The pending request: id, code, approval address, expiry, scope, who asked, which process polls it |
| `account.link.sequence` | The request counter. One counter for the life of the identity, never reset by an unlink |
| `account.link.update-policy`, `.update-attempt` | The update policy the site set, and the last version it was tried for |
| `account.link.outcome`, `.status` | Why the last request ended; the last heartbeat time and the last heartbeat and poll errors |

The link state is read and written back inside one transaction wherever more than one writer can reach it
(`kv.updateState`): a heartbeat storing a rotated key or a new interval and the page changing the scope
cannot read the same copy and each put their own back. Transactions here take the write lock when they begin.

The sequence is taken with one statement that raises and returns it and is committed before the request is
built, so a number is on disk before it is on the wire and the dashboard and a terminal command can never be
handed the same one. Two of this process's own requests can still reach the site in the opposite order to the
numbers they took; a `replayed_sequence` refusal has not been acted on, so the client takes a new number and
sends it again (up to three times).

`account_link_commands` (additive, invariant 8) records every command that passed its signature:
`id` (the site's ULID, the primary key), `kind`, `received_at`, `status` (`running`, then `succeeded`, `failed`
or `refused`), `finished_at`, `error`, `issued_by`. A replay hits the primary key before anything runs. A
command that failed its signature is **not** recorded, so a forged command cannot use up the id of a real
one. Rows older than a week are pruned: a command carries its own expiry, so none can be replayed by then. A
row still `running` when the connector next starts belonged to a process that died; it is closed as failed
and, if the site still has the command open, reported.

## Configuration

Four places, per [the configuration rule](databases-proxy-platform.md#configuration-version-release-self-update):
`config.go`, `.env.example`, `docker-compose.yml` (the explicit `environment:` map) and the README table.

| Variable | Default | Meaning |
| --- | --- | --- |
| `JD_ACCOUNT_URL` | `https://just-dashboard.com` | An origin only: no userinfo, path, query or fragment; `https` everywhere, `http` only for `localhost`, `127.0.0.0/8` or `[::1]`. Anything else refuses to start. A link remembers the URL it was made with; if the setting no longer matches, the connector goes idle and the page says why |
| `JD_ACCOUNT_LINK` | `true` | `false`: nothing is ever sent. The controls, the installer's question and `--admin link` refuse; `Start` runs nothing; an existing link is left in the store, idle; unlink still works and then tells no one |

In agent mode no connector is built and the routes are not mounted: the hub is the only caller and has no
business handing an agent to a site account.

## Scopes

Scopes nest: `maintain` includes `operate`, which includes `observe`. The Settings page's cards use these
exact words.

| Scope | The site sees | The site can also |
| --- | --- | --- |
| `observe` | Version and update state, OS, kernel, architecture, uptime, the access path (tailnet name and address, or the tunnel and its port), CPU, memory, swap, disk, load and network, container, stack and backup counts, alerts firing | `snapshot.now`: take a fresh reading |
| `operate` | Everything above plus the names of containers (with image and state), stacks, backup jobs and the last-deployed project | `container.start`, `container.stop`, `container.restart`, `stack.restart`, `backup.run` |
| `maintain` | Same as `operate` | `dashboard.update.install`, `dashboard.update.policy`, `dashboard.restart` |

**The heartbeat is a fixed field set** (protocol section 6) and `snapshot_test.go` pins it: the full list of
JSON paths at `observe` and the names added at `operate`, and that no sentinel container, stack, job or
project name is anywhere in an `observe` body. Below `operate`, `containers.list`, `stacks`, `backups.list`
and `deployments.lastRun.project` are left out and so are the subject and reading of any alert that names a
container or a backup job (only `cpu_high`, `memory_high` and `disk_filling`, which name a measurement or a
mount point, keep a subject; an alert kind added later is treated as naming something). The counts stay.
**An alert's `reading` is a number at every scope, never its message.** The host alerts' messages are
sentences for the person on the dashboard: the busiest container's name after a CPU alert, what Docker said
about a container that died, the line of a backup's log that failed. `accountHost` takes only the number out of
a message with a pattern that can match nothing else (`alertReading`: `95%` for CPU, memory and a filesystem,
`exit 137` for a container; none for a backup, a restart loop or an unhealthy container), and `Redact` drops
any reading that is not a percentage or an exit code, whatever put it there. `TestTheRealTextOfEveryAlertIsReducedToANumberAtEveryScope`
runs each alert kind's real producer, stores its message as the evaluator does, and reads it back through
`Collect`, `Redact` and JSON at all three scopes. Never
included at any scope: `.env`, `JD_ALLOWED_CIDRS`, accounts, API keys, file paths beyond mount points, logs,
connection strings, deployment variables, backup credentials. A section the dashboard cannot read is left
out, not sent as zero; an empty list that was read is sent as `[]`. The body is kept inside the site's list
caps (200 containers, 100 stacks, 100 jobs, 50 alerts) and the protocol's 64 KiB.

`access.kind` is `tunnel` when `JD_SITE` (from the settings the Configuration page reads, else the running
config) is empty, `localhost` or `127.0.0.1`, otherwise `tailscale`; the tailnet identity is only sent for
`tailscale`. `access.sshHost`, the public address the installer prints, is not sent: the dashboard has no
way to know it that does not mean calling a third party.

## Commands

`process` is the only way a command reaches a service, and `verify` runs first, in the protocol's order: the
`kid` names a pinned key that is not past its `notAfter`; the signature verifies over this server's own
`serverId` (so a command signed for another server fails here); it has not expired and does not expire more
than an hour from now (the site uses ten minutes, and the replay record below is pruned after a week, so a
far-future expiry would be a command that could be offered again once its record was gone); its id is new
(recorded here); its kind is allowed by the locally chosen scope; then the arguments are checked against the
kind with exactly the keys and patterns of protocol section 5, whatever the site sends. A failure is a
`refused` result with the reason (for example `dashboard.update.install needs scope maintain; this server is
linked at observe`), an audit entry and a heartbeat afterwards.

**What a refusal may write about an id.** Only the delivery that recorded an id finishes its record, and only
while the record is still `running`. A failure before the record (an unpinned key, a bad signature, an
expiry) is reported to the site, which is waiting to hear, under an id this dashboard has not recorded. A
second delivery of an id that is recorded, however it fails (a replay, a forgery, a copy with other arguments),
is audited and nothing else: it neither rewrites the record of the real command nor reports a refusal under its
id, which would finish a command the site still holds as running or overwrite how it ended.

Each kind calls one existing service through `Host`, implemented in `api/accountlink_host.go`:

| Kind | Runs through |
| --- | --- |
| `container.start` / `.stop` / `.restart` | `Server.runContainerAction`, the body of the container buttons' handler; **refused for any container of the dashboard's own compose project** |
| `stack.restart` | `Server.runStackAction`, the body of the stack page's POST handler, including the stack's history entry; **refused for the dashboard's own stack** |
| `backup.run` | `Server.startBackupRun`, the body of Run now; the run is recorded with trigger `account_link` rather than `manual`; the result is reported when the run has started, not when it ends |
| `dashboard.update.install` | `selfupdate.Service.Install`, after the same checks as `handleSelfUpdateInstall`; `version_moved` (reported as `refused`) when the version asked for is no longer the newest |
| `dashboard.update.policy` | stored at `settings['account.link.update-policy']` |
| `dashboard.restart` | `selfcfg.Service.Restart` |

The three lifted methods are the one refactor of the handlers; the handlers call them and keep their audit
entries and status codes. The container method takes no actor because nothing on that path records one. The
stack and backup methods take the actor (`account:<owner>`) for the stack's history and the run's trigger.

**A site cannot stop or restart the dashboard itself.** The dashboard's own stack lists as a managed one (its
checkout is under a directory mounted at the same path), so `container.stop` of its backend would stop the
process that received the command and nothing would bring it back (`unless-stopped` does not restart a
container that was stopped on purpose), and `docker compose restart` of the stack stops the backend that is
running it. `accountHost` finds the dashboard's own compose project the way the network pages do
(`proxysvc.SelfProject`: the container mounting the data directory) and refuses, as `refused` with the reason
`... is one of the dashboard's own containers; restart the dashboard itself with dashboard.restart, which
needs the maintain scope`, any `container.*` on a container of that project and any `stack.restart` on that
project or on another stack in the same working directory (a checkout whose directory is not named like its
project lists twice: as the running project and as a compose file on disk, and both restart the same
containers). Both lookups fail closed, so an unreadable Docker is a failure, not a pass. When no own project can
be found (a binary under systemd) there is nothing to protect and nothing is held back. The snapshot's
`containers.list` and `stacks` leave the dashboard's own containers and stack out, so the site is not shown a row
it would be refused on; the counts still cover everything on the host. The browser's own container and stack
buttons are not affected.

**Stack restart does not take a typed phrase.** `composeNeedsPhrase` is true for `down` alone, and the stack
page asks for no phrase to restart, so the site is held to the same rule rather than a stricter or a looser
one. `runStackAction` takes the way to get a phrase as a parameter; a caller with none (the site) is refused
for any action that needs one, so it could not take a stack down even if a later change asked it to. The
authority behind a site's stack restart is the administrator who linked and chose a scope that includes it.

**Budgets.** Stop and restart of a container, restart of a stack, an update install and a dashboard restart
spend the destructive budget (`destrLim`) under the key `account:<owner>`, the same limiter the routes use for
a person; start and a backup run do not, as in the browser. When it is exhausted the result is `failed` with
`too many destructive actions`.

**Audit.** Every command, run or refused, writes `account.command.<kind>` (`account.command.invalid` for a kind
that is not one of ours, so a site cannot choose the action name) with actor and username
`account:<owner>`, the target (container, stack, `job:<id>`, version), success, and a detail of the command
id, the status, the error and the `issuedBy` the site states (informational; it is not covered by the
signature, so the account named is the one that approved the link). The update policy's own installs write
`account.update.auto`; the link flow writes `account.link` (by the dashboard account, or `root` / `cli` from the
terminal), `account.link.begin`, `.cancel`, `.scope` and `account.unlink`.

Results sent back carry the service's message, never a command's output: output is cut at 8 KiB and errors at
1000 characters, and compose's own output stays on the machine because it can carry values from a stack's
environment.

## Automatic updates

`dashboard.update.policy` sets `auto` (`off`, `patch`, `all`) and a window of two `HH:MM` times in the
**server's local time** (the container reads the host's zone through `/etc`); an end earlier than the start
runs past midnight, the end is exclusive, and an empty window is refused unless `auto` is `off`. The
connector evaluates it a minute after it starts and then **every minute** (`policy.go`), reading nothing from the
machine until the window is open: a window of half an hour is shorter than the interval of any slower check, and
one made hourly at a fixed minute can miss it every day:

- `patch` takes a release that differs from the installed one only after the second component (0.8.2 to 0.8.3,
  0.8 to 0.8.1, not 0.8.2 to 0.9); `all` takes any newer one;
- never a release the changelog marks **breaking**: those need a person to read them, and the heartbeat tells
  the site `updateBreaking`;
- only while the link's scope is still `maintain`, only when this install can update itself and nothing is
  already running, and through the same `selfupdate.Install` checks as the button;
- each version is tried once (`account.link.update-attempt`); a failed install is not started again every
  hour, and a newer version or a fresh policy is another chance.

A site outage never blocks an update and never triggers one. The first heartbeat of the new process after an
update reports the new version.

## Local API

All under `/api/v1/dashboard/link`, inside the admin-only `/dashboard` group (`system.admin`), so a read-only
or limited account gets 403 on every route. The shape is the `AccountLinkStatus` type the frontend builds to;
optional fields are omitted, never `null`.

| Route | Guards | Does |
| --- | --- | --- |
| `GET /` | | The status |
| `POST /` `{scope}` | session; audited `account.link.begin` | Begin. `409 already_linked`, `409 link_pending`, `403 account_link_disabled`, `502 site_unreachable` (the message says what failed) |
| `DELETE /request` | session; `account.link.cancel` | Forget the pending request (`204`) |
| `PUT /scope` `{scope}` | session; `account.link.scope` | Change the scope; the next heartbeat tells the site. `409 not_linked` |
| `DELETE /` | session, `s.destructive`; `account.unlink` | Unlink (`204`). `409 not_linked` |
| `POST /sync` | | A heartbeat now, then the status. `409 not_linked`; `409 site_changed` when `JD_ACCOUNT_URL` is no longer the site the link was made with. Held to the site's four a minute: if the window is full no heartbeat is sent and the status comes back as it was |

## Terminal, installer, `manage.sh`

`--admin link [--scope observe|operate|maintain] [--wait 15m]`, `--admin unlink`, `--admin link-status`
(`admin/link.go`), run as `docker compose run --rm --no-deps -T backend --admin ...` like the account
commands, and root-only. They read `JD_ACCOUNT_LINK`, `JD_ACCOUNT_URL` and `JD_MASTER_KEY` from the Compose
environment (the account commands never decrypt anything; these have to, so they build a sealer, with a clear
error when the key is absent; `link-status` needs no key). `link` prints the approval address and the code,
waits, and exits **0** when linked, **2** when the request was declined or not approved before `--wait` (or
the command was interrupted), **1** for anything else (switched off, already linked, key missing, site
unreachable). Interrupted or timed out, it forgets its request. Audited as `account.link` by `root` /
`cli`; failures are audited as failed; `unlink` as `account.unlink`.

`install.sh` asks in the Behaviour step, after the two-factor question and only when linking is not switched
off, **Link this server to your just-dashboard.com account?** (default No). `--link` and `--no-link` answer it
for unattended runs and `--link-scope` names the scope (default `observe`); the flags are validated before
anything is done. On a re-run that keeps `.env` the question is not asked unless `--link` is given, because an
install that has been running may already be linked, and a short line says how to link later. Nothing about
the link is written to `.env`, except that `JD_ACCOUNT_LINK=false` in the environment of a fresh install is
persisted there. A kept `.env` is read as Compose reads it: `JD_ACCOUNT_LINK=false # off`, `"false"` and `0` all
switch linking off. In stage 4, after the health check, `$COMPOSE run ... --admin link --scope S --wait 15m` runs
with its input from `/dev/null`, its output relayed, and **its failure never fails the install**: a decline,
a timeout or an unreachable site gives a warning and `Not linked. Later: sudo ./scripts/manage.sh link, or
Settings → just-dashboard.com` as the summary's line; success gives the command's own `Linked to <owner> as
"<name>" (<scope>).` line, and a re-run with `--link` on a server that is already linked says `This server is
already linked to <owner>.` rather than that it is not linked. `scripts/manage.sh link [scope]`, `unlink` and `link-status` forward to the
commands and keep their exit status.

## Failure modes

| What happens | What the dashboard does |
| --- | --- |
| The site cannot be reached, or answers 5xx | Heartbeat and poll back off from 1 s to 5 min with jitter, showing the last failed exchange as `lastError`; a heartbeat is also held to the site's four a minute. Nothing else on the dashboard is affected |
| `stale_time` | The page tells the operator the clocks differ by more than 60 s; every request is signed with the local clock |
| `bad_signature` / `unknown_server` | The page says the site does not accept this server's signature and suggests unlinking and linking again; the link is not cleared automatically |
| `410` on a signed request | The owner removed the server on the site: the link and the policy are cleared, the identity kept, an `account.unlink` entry is written, the loops stop |
| A response that is not protocol 1 (unknown fields, trailing content, over 256 KiB) | Refused as an error; never partially trusted |
| A redirect | Refused |
| A site key announced in a heartbeat | Added if it is a 32-byte key; a pinned key's material is never replaced; keys past `notAfter` are dropped, but never the last one |
| `JD_MASTER_KEY` changed | The stored identity cannot be opened. An install that is not linked makes a new one; a linked one reports the error and needs an unlink |
| `JD_ACCOUNT_URL` changed after linking | The connector goes idle and says so; set it back or unlink. Nothing is sent to the new site on the old link's account: a manual sync is refused (`site_changed`), an unlink clears the link without telling anyone, and a heartbeat's answer is never merged into keys pinned for another site |
| The scope is narrowed while a heartbeat is being built or waiting for the rate window | The scope is read again after the machine has been read and the body is cut to it, and says so; a link that ended meanwhile sends nothing |
| The dashboard restarts mid-command | The row is closed as failed on the next start and reported; the effect, if any, shows in the next snapshot |

## Verification

```bash
scripts/test-changed.sh                 # the Go packages touched, vet, python3 scripts/test_manage.py
cd backend && go test -race ./internal/accountlink/... ./internal/admin
python3 scripts/test_manage.py          # manage.sh and the installer, through stubs
```

`accountlink` tests run against a fake site that verifies signatures, the time window and the sequence as the
protocol documents them: link requests approved, denied and expired, `410`, the vectors, replay, expired,
wrong key, wrong server, out-of-scope and malformed commands (each refused, reported and audited), every kind
through a fake `Host`, redaction, policy windows and the connector's loops (including a link made by another
process). The `api` tests drive the container, stack, backup and update paths through a fake Docker daemon, a
fake `docker` CLI and a real local backup job, and the routes through the HTTP stack, including that an API key
is refused. The installer is only ever run with `docker`, `curl` and the host paths stubbed.
