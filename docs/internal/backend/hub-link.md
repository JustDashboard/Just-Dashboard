# Connecting to Just Hub

A server administrator can connect this dashboard to a Just Hub: a separate app that runs as its own machine
on the same tailnet and shows many dashboards in one place. The hub lists the server with its health and
version, offers the other linked servers in the dashboard's own switcher, and, at a scope chosen on this
machine, can ask it to do the same few things a linked just-dashboard.com account can. It is optional, does
nothing until an administrator connects, and can be switched off outright with `JD_HUB_LINK=false`.

**The contract is [hub-link-protocol.md](hub-link-protocol.md)**, with its vectors in
[`hub-link-vectors.json`](hub-link-vectors.json). That file is normative for both sides; this page records how
the dashboard implements it. It is the account link's protocol pointed at a different peer, so read
[account-link.md](account-link.md) first: everything there about signing, the sequence, the snapshot and its
scopes, command verification, the replay table, budgets, automatic updates and the dashboard's own containers
applies here unchanged, with the names below.

Hub sign-in (protocol section 9) is **not** in this release. The heartbeat never carries `signIn`, there is no
`JD_HUB_SIGNIN`, no grant table and no `/hub/enter`; see [Not yet built](#not-yet-built).

## One engine, two instances

`backend/internal/accountlink` is the engine: identity, signed client, sequence, request flow, heartbeat,
command verification and execution, update policy, loops and backoff. A `Profile` (`accountlink/profile.go`)
says what one instance is, and `Hooks` are where an instance adds behaviour of its own. The account link is
`accountlink.AccountProfile`; `backend/internal/hublink` builds a second instance with the hub's profile. The
two run side by side in one process and share nothing:

| | Account link | Hub link |
| --- | --- | --- |
| Settings keys | `account.link.*` | `hub.link.*` |
| Replay table | `account_link_commands` | `hub_link_commands` (`store/hub_link_schema.go`, additive) |
| Base path, part of every signed string | `/api/link/v1` | `/api/hub/v1` |
| Identity and sequence | `account.link.identity`, `.sequence` | `hub.link.identity`, `.sequence` |
| Where it points | `JD_ACCOUNT_URL`, fixed by configuration | Chosen at connect time, stored with the request and the link (`OriginPerLink`); must pass the tailnet rule |
| Discovery | `GET /site` before a request | None: the protocol header on `POST /requests` is the version check |
| Approval | A code approved on the site | A code approved in the hub, or a one-time token |
| Heartbeat reply | `ok`, `heartbeatSeconds`, `pendingCommands`, `siteKeys` | The same, plus `fleet` and `notices` (required) and `signIn` (optional), strictly decoded (`ReplyView`) |
| Pacing, default interval | 4 a minute, 30 s | 12 a minute, 15 s |
| Key id and owner label | `kid` may hold a dot; any non-empty label | `kid` has no dot, at approval and in a rotation; the label is the hub's name, `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$` |
| Audit actor, destructive-budget key | `account:<owner>` | `hub:<hub name>` |
| Audit actions | `account.link`, `account.unlink`, `account.command.*`, `account.update.auto` | `hub.link`, `hub.unlink`, `hub.command.*`, `hub.update.auto` |
| Messages | "the site", "on the site" | "the hub", "in the hub" |
| Switch | `JD_ACCOUNT_LINK` | `JD_HUB_LINK` |

Each instance has its own `Host` (`newAccountHost(s)` twice in `api`), so their snapshot rates are their own,
while a command does the same thing whichever link sent it. Each keeps and evaluates its own update policy; a
server installs an update when either says so, and the installer refuses a second run while one is under way.
`TestTheAccountLinkAndTheHubLinkRunSideBySide` links both on one store, heartbeats both, runs a hub command,
and ends each link in turn while the other keeps running. `TestTheAccountLinkKeepsItsKeysWhereEarlierReleasesWroteThem`
pins that the refactor moved none of the account link's stored keys.

The hooks the hub link uses:

| Hook | What the hub link does with it |
| --- | --- |
| `Admit` | The URL rule as it applies to a stored link (below). It replaces the account link's "still `JD_ACCOUNT_URL`" check everywhere that check ran: the top loop, the heartbeat, a manual sync and whether an unlink tells anyone |
| `OnReply` | Decodes and checks `fleet`, `notices` and `signIn` and caches them in `hub.link.view`. A reply that does not decode strictly fails the heartbeat and nothing in it is used |
| `OnClear` | Deletes `hub.link.view` in the same transaction that ends a link (disconnect, removal in the hub) or stores a new one |
| `HeartbeatExtra` | Unused. Hub sign-in adds `signIn` to the body here; the engine keeps such keys whole and cuts the snapshot's lists to make room |

## Files

| File | What it holds |
| --- | --- |
| `hublink.go` | The profile, `Options`, `Service` and its methods: `Connect`, `Poll`, `Await`, `Cancel`, `SetScope`, `Disconnect`, `Sync`; the engine's errors under this package's names |
| `url.go` | The URL rule (2.2), the test origin, the decision on a stored link (`admitStored`) |
| `dial.go` | `tailscale status --json` on the host, and the dialer that only reaches a hub over `100.64.0.0/10` |
| `view.go` | The fleet, notices and sign-in offer: strict decoding, per-entry rules, the cache |
| `status.go` | `Status` (`GET /dashboard/hub`), `Session` (the `hub` field of `/auth/session`), `Hello` and its exact bodies |
| `hubstub/` | A signature-less stand-in hub for the `api` and `admin` tests |

Outside the package: `api/handlers_hublink.go` (routes, construction), the `hub` field in `api/handlers_auth.go`,
`admin/hub.go` (terminal commands), `store/hub_link_schema.go`, and `JD_HUB_LINK*` in `config`.

## The URL rule and how a hub is reached

`Connect` reads the address a person typed in the order of protocol 2.2 and stops at the first failure, with
that step's code: an origin only (a bare host is read as `https://`), spelt one way (lowercase, no trailing dot,
no `:443`), `https`, a name not an address (`hub_url_invalid`); this server on a running tailnet with a
`.ts.net` MagicDNS suffix, read from `tailscale status --json` on the host (`CurrentTailnet.MagicDNSSuffix`, else
the top-level field, else the self name without its first label); exactly one label under that suffix
(`hub_url_not_tailnet`); not this server's own name, on any port, because a browser sends a host-only cookie to
every port of its host (`hub_url_same_host`). Then, when tailscaled's LocalAPI can be asked
(`tailnetgate.NewLocalAPI`), `whois` on the address the dashboard would dial must name that same machine
(`hub_not_on_tailnet`); when it cannot be asked the check is skipped and logged, since the certificate and the
steps before already bind the name. "One hub per server" is checked before any of this, so a refused second
connect never asks the tailnet.

**A stored link is checked again when the connector starts with it** (`admit`, once per link per process): an
address that is not `https`, is an IP literal, or (when the suffix can be read) is no longer one machine under
this server's suffix, idles the link with `ErrURLRefused`. The link is kept, the page shows the reason as the
last error, a manual sync answers `409 hub_url_refused`, and a disconnect clears it without telling anyone. If
the tailnet cannot be read at start, nothing is decided and the link runs.

**Reaching the hub.** The hub link's HTTP client (`dial.go`) uses no proxy and a dialer of its own: the system
resolver's answer if it is in `100.64.0.0/10`, else the tailnet IPv4 address `tailscale status` lists for the
peer of that name (cached for a minute, dropped after a failed dial), else the hub is unreachable. It never
dials anything outside `100.64.0.0/10`. TLS still verifies the certificate for the name, against the system
roots, and the request's `Host` is the name. Redirects are refused.

**The test origin.** `JD_HUB_LINK_TEST_ORIGIN` names one loopback origin (`http` or `https`, `localhost`,
`127.0.0.0/8` or `[::1]`); anything else refuses to start. A hub URL equal to it skips the rule and is dialled as
written; every other URL still goes through the whole rule, and signing, scopes and commands are unchanged. Fleet
endpoints and notice links from a hub at the test origin follow the relaxed rules of protocol section 6. When it
is set the backend logs a warning at start and `GET /dashboard/hub` reports it as `admin.testOrigin`. It is read
by `config.go` and the terminal commands only, and on purpose appears in no `.env.example`, no Compose
`environment:` map and no operator guide.

## What the dashboard keeps from each reply

`fleet` (at most 500), `notices` (at most 20) and `signIn` are decoded strictly: an unknown field, in the reply
or in an entry, fails the heartbeat. Within that, the lists are for display, so an entry that breaks a rule of
protocol section 6 is dropped and the rest kept: a `serverId` of the wrong shape, a name that is empty, over 64
characters or has control characters, an unknown `state`, a missing required field, a second entry claiming
`self`; a notice with a bad `id` or `level`, a title outside 1 to 120 characters or a body over 1000. A fleet
`endpoint` must be an `https` origin, one label under the hub's own MagicDNS suffix, or it is removed and the
entry kept without a link. A notice's `href` must be on the hub's own origin or it is removed. Nothing in a reply
changes anything on the dashboard except the heartbeat interval and the pinned keys.

The cache (`hub.link.view`) names the `serverId` it came from, so a reply that lands after the link changed is
never shown for the new one, and it is cleared with the link.

## Local API

`GET /api/v1/hub/hello` is the hub's discovery probe: no sign-in, but inside `/api/v1`, so the allowlist runs
first, under the API rate limit per address, `Cache-Control: no-store`, and nothing in it but
`{"product":"just-dashboard","protocol":1,"hub":"available|pending|linked"}` (written by hand, byte for byte the
vectors' bodies). It is not mounted with `JD_HUB_LINK=false` or in agent mode, so those answer `404` as an older
dashboard does. It is the one route under `/api/v1` that answers without a session; `routes_test.go` lists it as
the deliberate exception and checks its body.

| Route | Guards | Does |
| --- | --- | --- |
| `GET /dashboard/hub` | any signed-in principal; the `admin` part only for `system.admin` | The status (shape below) |
| `POST /dashboard/hub` `{url, scope}` | `system.admin`, session; audited `hub.link.begin` | Connect. `400` with the rule's code, `409 already_linked`, `409 link_pending`, `403 hub_link_disabled`, `502 hub_unreachable` (the message says what failed, `bad_token` and `unsupported_protocol` included) |
| `DELETE /dashboard/hub/request` | `system.admin`, session; `hub.link.cancel` | Forget the pending request (`204`); the hub is not told |
| `PUT /dashboard/hub/scope` `{scope}` | `system.admin`, session; `hub.link.scope` | Change the scope; the next heartbeat tells the hub. `409 not_linked` |
| `DELETE /dashboard/hub` | `system.admin`, session, `s.destructive`; `hub.unlink` with `hubNotified` | Disconnect (`204`). `409 not_linked` |
| `POST /dashboard/hub/sync` | `system.admin` (an API key may) | A heartbeat now, then the status. `409 not_linked`, `409 hub_url_refused`. Held to twelve a minute: a press that would break the pace is skipped |

The `GET` sits with the every-role reads of the `/dashboard` group (`handlers_selfupdate.go`); the rest sit in
its `system.admin` part. The status is `hublink.Status`: optional fields are left out, never `null`; times are
RFC 3339 in UTC. With `JD_HUB_LINK=false` it is exactly `{"enabled":false,"state":"…"}`. Otherwise:

- everyone: `enabled`, `state` (`unlinked`, `pending`, `linked`), `hubUrl` (pending or linked), and while linked
  `hubName`, `scope`, `reachable` (the last heartbeat succeeded, none failed since, younger than three
  intervals), `lastHeartbeatAt` once one succeeded, and `fleet` and `notices` (`[]` before the first reply);
  `signIn` is `{enabled: false, offered, users}`, `offered` and `users` from the last reply.
- `system.admin` also gets `admin`: `fingerprint` (once the identity exists), `testOrigin`, `request` (pending:
  `hubUrl`, `userCode`, `approveUrl`, `expiresAt`, `scope`), `link` (linked: `serverId`, `linkedAt`, `linkedBy`,
  `lastError`, `lastCommand`, `hubKeys` of `{kid, fingerprint, notAfter?}`, `updatePolicy`, and `lastSnapshot`,
  the exact body of the last heartbeat sent), `lastOutcome` (unlinked after a request that ended without a
  link). `admin.grants` belongs to hub sign-in and is never sent yet.

`GET /auth/session` carries `hub: {linked: true, name, url, reachable}` only for a fully signed-in session (not one
owing a second factor or a password change, and never to nobody), with `JD_HUB_LINK` on and a hub link stored.
The fleet and notices come from `GET /dashboard/hub`, so this stays small.

## Terminal

`scripts/manage.sh hub connect URL [--scope observe|operate|maintain] [--token-stdin | --token TOKEN] [--wait 15m]`,
`hub disconnect` and `hub status` check their arguments and run `docker compose run --rm --no-deps -T backend
--admin hub-connect|hub-disconnect|hub-status …` (`admin/hub.go`), keeping the exit status with `exec`. They read
`JD_HUB_LINK`, `JD_HUB_LINK_TEST_ORIGIN` and `JD_MASTER_KEY` from the Compose environment; `connect` and
`disconnect` need the master key, `status` does not.

`hub connect` applies the rule, prints the hub URL, the code and this server's fingerprint, and waits (default 15
minutes, at most an hour). The default scope is `maintain`. `--token-stdin` reads one line from standard input,
which is how the hub's installer passes its token so it never appears in a process list; a token that is not
`jht_` and 43 base64url characters is refused before anything is sent, and one the hub refuses (`bad_token`) is a
failure, never a fallback to a code. Exit **0** connected (`Connected to Just Hub "<name>" at <url> as "<server>"
(<scope>).`), **2** declined, not approved within `--wait`, or interrupted (the request is forgotten), **1**
anything else. A request begun in Settings is never taken over from the terminal. Audited as `hub.link` (by
`root` / `cli`; failures as failed with the outcome) and `hub.unlink`.

## Failure modes

The account link's table applies, with these for the hub:

| What happens | What the dashboard does |
| --- | --- |
| The hub is down or unreachable | Heartbeat and poll back off from 1 s to 5 minutes; `reachable` goes false; nothing else on the dashboard depends on the hub |
| A reply with an unknown field, or without `fleet` or `notices` | A failed heartbeat; the cached view is left as it was and nothing in the reply is used |
| `410` on any signed request | The hub removed this server: link, policy, status and cached view cleared in one transaction, identity and sequence kept, `hub.unlink` by `system` with the reason "the server was removed in the hub" |
| The server moved to another tailnet, or the test origin was removed | The link idles and says why; disconnect, then connect to a hub on this tailnet |
| The hub is reinstalled with a new key | Its commands fail the key check (refused, audited); disconnect and connect again |
| `JD_HUB_LINK=false` while linked | The link stays stored and idle; nothing is sent; a disconnect clears it and tells no one |

## Not yet built

Hub sign-in (protocol section 9) arrives on a later branch. What is in place for it: the reply's `signIn` is
decoded and cached, and `GET /dashboard/hub` reports it as `signIn.offered` and `signIn.users`; the engine's
`HeartbeatExtra` hook carries extra heartbeat keys whole within the 64 KiB budget; and `OnClear` is the
transaction in which grants and ticket records will be deleted with a link. Still to come with it: `JD_HUB_SIGNIN`,
`hub_signin_grants` and `hub_signin_tickets`, `PUT`/`DELETE /account/hub-signin`,
`DELETE /dashboard/hub/signin/{userId}`, `admin.grants` and `signIn.mine`, `POST /auth/hub/enter`, the
`/hub/enter` page, and the ticket vectors. The written security review of the exception to invariant 2 must be
done before that code is switched on anywhere.

## Verification

```bash
scripts/test-changed.sh origin/patch/0.8.5
cd backend && go test ./internal/hublink/... ./internal/accountlink/... ./internal/admin ./internal/config
cd backend && go test ./internal/api -run 'Hub|Link|NoAPIRoute|EveryAPIRoute'
python3 scripts/test_manage.py
```

`hublink` tests run against a fake hub that checks the protocol header, the signature, the time window and the
sequence of every signed request in section 3.1's order with its own reading of the signed string: connecting with
a code and with a token, a refused token creating nothing, declines, one hub per server, the heartbeat body and the
cached view (bad entries dropped, endpoints and links removed), strict replies, every kind at every scope run as
`hub:hub` or refused, replay, expiry, a forged key, an unpinned `kid`, a command for another server and an unknown
kind (refused, reported and audited, and only commands past the signature recorded in `hub_link_commands`),
disconnect, removal in the hub, a stored link that no longer passes the rule, and both links side by side. The
vectors test reads `hub-link-vectors.json` and runs every key, canonical-JSON, rejected-args, request and command
case and the hello bodies against the dashboard's own code, the hub-link command cases through the real
connector. The URL rule, the test origin, `tailscale status` parsing and the dialer have table tests. The `api`
tests cover the routes and their role rules through the HTTP stack, the session field and the probe; the `admin`
and `test_manage.py` tests cover the terminal path.
