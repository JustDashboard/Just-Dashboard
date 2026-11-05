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
| Sequence rule | `stored + 1` | `max(stored + 1, unix milliseconds)` in the same statement (`ClockSequence`), so a database restored from an older copy carries on above every number it sent |
| Where it points | `JD_ACCOUNT_URL`, fixed by configuration | Chosen at connect time, stored with the request and the link (`OriginPerLink`); must pass the tailnet rule |
| Discovery | `GET /site` before a request | None: the protocol header on `POST /requests` is the version check |
| Approval | A code approved on the site | A code approved in the hub, or a one-time token |
| Heartbeat reply | `ok`, `heartbeatSeconds`, `pendingCommands`, `siteKeys` | The same, plus `fleet` and `notices` (required) and `signIn` (optional), strictly decoded (`ReplyView`) |
| Pacing, default interval | 4 a minute, 30 s | 12 a minute, 15 s |
| Key id and owner label | `kid` may hold a dot; any non-empty label | `kid` has no dot; the label is the hub's name, `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$` |
| Key rotation | `siteKeys` in a reply adds a successor and retires old keys | None: `siteKeys` is decoded and ignored (`IgnoreAnnouncedKeys`) |
| Audit actor, destructive-budget key | `account:<owner>` | `hub:<hub name>` |
| Audit actions | `account.link`, `account.unlink`, `account.command.*`, `account.update.auto` | `hub.link`, `hub.unlink`, `hub.command.*`, `hub.update.auto` |
| A `backup.run`'s trigger | `account_link` | `hub_link` |
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
| `Admit` | Before anything is sent on a stored link: an idle link sends nothing, the URL rule as it applies to a stored link, and the hub node check (both below). It replaces the account link's "still `JD_ACCOUNT_URL`" check everywhere that check ran (the top loop, the heartbeat, a manual sync, whether an unlink tells anyone) and also runs before every command poll and every result. Its refusals (an idle link) wrap `accountlink.ErrIdle` as well as `hublink.ErrURLRefused`; any other error is a check that failed this time, which the engine counts as a failed exchange. A manual sync (`SyncNow`) answers with a refusal and puts any other error on the status as a failed heartbeat |
| `BeforePoll` | The hub node check before every poll of a pending request, in whichever process polls it. A `*RequestEndedError` ends the request as failed and nothing is sent |
| `OnReply` | Decodes and checks `fleet`, `notices` and `signIn`, and returns the write that caches them in `hub.link.view`, which the engine runs inside the heartbeat's own transaction only while the link is still the one it was sent for. A reply that does not decode strictly fails the heartbeat and nothing in it is used |
| `OnClear` | Deletes `hub.link.view` in the same transaction that ends a link (disconnect, removal in the hub) or stores a new one |
| `HeartbeatExtra` | Unused. Hub sign-in adds `signIn` to the body here; the engine keeps such keys whole and cuts the snapshot's lists to make room |

## Files

| File | What it holds |
| --- | --- |
| `hublink.go` | The profile, `Options`, `Service` and its methods: `Connect`, `Poll`, `Await`, `Cancel`, `SetScope`, `Disconnect`, `Sync`; the engine's errors under this package's names |
| `url.go` | The URL rule (2.2), the test origin, the decision on a stored link (`admitStored`) |
| `node.go` | The hub's machine (2.4): the check before sending, the dialer's gate, recording a node later, setting a link idle (`goIdle`), `NodeNote` for the begin audit |
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
steps before already bind the name. A name with no tailnet address (see "Reaching the hub") leaves nothing to ask
about: the skip is logged, `Check` shows the machine as not checked, and `Connect` stops there with
`502 hub_unreachable`, so no request goes out whose step 8 never ran. Step 8 here (`confirmNode`) changes nothing:
it compares the name only, never a pinned node, so it never marks an address as checked for the connector, and
a `check` of a linked hub's address cannot stand in for the connector's own check. "One hub per server" is checked
before any of this, so a refused second connect never asks the tailnet.

**A stored link is checked again when the connector starts with it** (`admit`, once per link per process, before
anything is sent): steps 1 to 4 need no tailnet and always run, so an address that is not `https`, is an IP literal
or a loopback name, and is not the current test origin, is refused; steps 5 to 7 run when the suffix can be read
(no longer one machine under this server's suffix, or this server's own name, is refused) and are skipped this time
when it cannot. A refusal **sets the link idle in the store** (`State.Idle`, `{code: "hub_url_refused", reason,
at}` in `hub.link.state`, written by `Engine.SetIdle`), revokes every hub session in that same write transaction
(`revokeHubSessionsTx`, which has nothing to revoke until hub sign-in exists), and audits `hub.link.idle` by `system`
with the reason and `sessionsRevoked`. An idle link stays idle across restarts, whatever the rule or WhoIs says
later: nothing is sent on it, the page shows the reason as the last error and `reachable: false`, a manual sync
answers `409 hub_url_refused`, and only a disconnect clears it (without telling anyone). A link made at the test
origin therefore stops at the first start after the variable is removed, and putting the variable back does not
revive it.

**The hub's machine** (`node.go`, protocol 2.4). The certificate proves only that the answering machine holds the
hub's MagicDNS name, which goes to whichever machine registers it next. So step 8 of the rule keeps the WhoIs answer
as the **hub node** (`{id: StableID, name, owner (the login) or tags}`, `accountlink.PeerNode`): with the request
(`hub.link.request`), then in the link state (`hub.link.state.hubNode`) when the approval is collected, and in the
`hub.link.begin` and `hub.link` audit details (`"not checked: tailscaled could not be asked"` when it was skipped).
Before anything is sent on a stored link, WhoIs is asked about the address about to be dialled: at each connector
start, at least every five minutes (`admitNode` from `Admit`), and whenever the dialer is about to dial an address
it has not checked (`dialGate`, `dialer.gate`, so a DNS or peer change between checks is caught before a byte goes
out). With nothing pinned yet, which is `Connect`'s own request (the one that may carry the one-time token), the
dialer's check is step 8's: a machine of another name at the address is refused with `hub_not_on_tailnet`. A check
that is due when the hub's name has no tailnet address sends nothing and counts as a failed exchange or poll, since
the transport could otherwise carry the request on a connection it keeps open, with no dial and so no check.
Another `StableID` sends nothing and sets the link idle with "the hub's machine changed", in the same way as
above, audited `hub.link.node_changed` with the old and new node. No machine at the address (`ErrNotAPeer`) sends
nothing and counts as a failed exchange with "the hub's machine is not on this tailnet", backing off without going
idle. tailscaled not answering (`ErrUnavailable`, or an answer with no `StableID`) sends, and is logged at most once
an hour. A link or request stored without a node records the first answer whose node name is the stored host (an
answer naming another machine sends nothing), audited `hub.link.node_recorded` for a link. While a request is pending
the check runs before **every** poll (`BeforePoll`), in whichever process polls it: another `StableID` ends the
request as failed with no key, `serverId` or name stored, and audits `hub.link.node_changed`; the link pins the
request's node as it stood at the poll that collected the approval. At the test origin no WhoIs is asked and no node
is recorded.

**Reaching the hub.** The hub link's HTTP client (`dial.go`) uses no proxy and a dialer of its own: the system
resolver's answer if it is in `100.64.0.0/10`, else the tailnet IPv4 address `tailscale status` lists for the
peer of that name (cached for a minute, dropped after a failed dial), else the hub is unreachable. It never
dials anything outside `100.64.0.0/10`. TLS still verifies the certificate for the name, against the system
roots, and the request's `Host` is the name. Redirects are refused.

**The test origin.** `JD_HUB_LINK_TEST_ORIGIN` names one loopback origin (`http` or `https`, `localhost`,
`127.0.0.0/8` or `[::1]`); anything else refuses to start. A hub URL equal to it skips the rule and is dialled as
written, and no WhoIs is asked about it; every other URL still goes through the whole rule, and signing, scopes and
commands are unchanged. With the stored hub URL at the test origin exactly two rules relax (protocol 2.3): a notice
`href` must be on that origin, whatever its scheme, and a fleet `endpoint` may be a loopback `http` or `https`
origin, or one under this server's own MagicDNS suffix; any other `https` origin is still removed. When it is set
the backend logs a warning at start and `GET /dashboard/hub` reports it as `admin.testOrigin` (the Settings page
uses it to accept `?connect=` naming that origin, or its host, exactly). It is read by `config.go` and the terminal
commands only, and on purpose appears in no `.env.example`, no Compose `environment:` map and no operator guide.

**A loopback dashboard's own address**, as the local end-to-end harness runs it, is reported by the rules that
apply everywhere, with nothing special for tests: `access.kind` is `tunnel` when `JD_SITE` is empty, `localhost`
or `127.0.0.1` (protocol 5.1, `accountHost.access`), and `access.endpoint` is the address a browser opens it at,
for example `http://127.0.0.1:18443`. A hub in dev mode keeps a loopback endpoint (5.3's endpoint rule, its dev
clause) and relays it in `fleet`; a dashboard linked at the test origin keeps it (6.1). Protocol 6.1's "left out
when that server is reached by an SSH tunnel" is what 5.3's rule does to such an endpoint in production (it is not
an `https` origin on the hub's WhoIs node), not a separate rule on `access.kind`. The side that was wrong was this
dashboard: at the test origin it used to keep any `https` endpoint as well.

## What the dashboard keeps from each reply

The reply is decoded strictly at the top level: an unknown field, a missing `ok`, `heartbeatSeconds`,
`pendingCommands`, `fleet` or `notices`, `ok` not `true`, a `fleet` or `notices` that is not a list, or a `signIn`
that is not an object holding exactly a list `users`, fails the heartbeat and nothing in it is used. Each element
of `fleet` (at most 500 kept), `notices` (at most 20) and `signIn.users` (at most 50) is then decoded on its own
(`[]json.RawMessage`, then each element strictly into its type) and checked against protocol section 6, because the
lists are for display and one server's bad value must not stop every other server's heartbeat: an element with an
unknown field, a wrong type or a missing required field is dropped, as is a `serverId` of the wrong shape, a name
outside 1 to 64 characters or breaking the text rule of 5.3 (no Unicode Cc, Cf, Zl or Zp character, so no line
break or bidirectional control), an unknown `state`, a second entry claiming `self`; a notice with a bad `id` or
`level`, a title outside 1 to 120 characters or a body over 1000; a hub user that breaks the pattern of 6.3. A
fleet `endpoint` must be an `https` origin, one label under the hub's own MagicDNS suffix, or it is removed and the
entry kept without a link. With the hub at the test origin a loopback `http` or `https` origin passes instead, or
one under this server's own MagicDNS suffix; any other `https` origin is removed there too. A notice's `href` must
be on the hub's own origin (at the test origin, with its scheme) or it is removed. Nothing in a reply
changes anything on the dashboard except the heartbeat interval and the cached view. `siteKeys` is decoded so
strict decoding passes, and never applied (`IgnoreAnnouncedKeys` in the profile): no key is added, no pinned key's
end changes, and a warning is logged once per link. A reply is authenticated by TLS alone, and a key pinned from
one could sign commands; a new hub key needs a new link.

**Writes belong to the link they describe** (protocol 8.9). `OnReply` only checks the reply and hands back its
write; the engine runs it in the transaction that records the heartbeat (`noteHeartbeat`, `kv.updateStateTx`),
which re-reads `hub.link.state` first and writes nothing, neither the interval nor the view, when the stored link
is gone or has another `serverId`. `setStatus` (the last heartbeat time and the last errors in `hub.link.status`)
does the same check in its own transaction. A reply that lands after a disconnect therefore leaves no cached view
and no status behind, and cannot bring an old hub's fleet, notices or sign-in users back under a new link. The
cache also names the `serverId` it came from, and is cleared with the link.

## The hub's words are shown as the hub's

A hub that is taken over can put any text in what it sends, and within the rules some of it could read like this
dashboard's own prompt ("your session expired, sign in again"). Protocol 8.10 says every piece of it is shown as
plain text attributed to the hub. In the backend that is the hub's `message` in an error: the profile's `Speaker`
("Just Hub") makes `APIError` quote it as `the hub answered 503 maintenance. Just Hub (hub) says: "…"`, named by the
link's `owner.label`, or by the hub's host before a link names it (`Connect`'s `hub_unreachable`). The message is
cut to 300 characters with its control, format (bidirectional), line and paragraph separator characters made
spaces (`plainText`), so it cannot reorder the sentence around it. So `admin.link.lastError` and the Connect
errors never carry the hub's words unattributed; the account link's messages are unchanged. Notices, fleet names
and `owner.label` are structured fields and stay raw in the API; the pages render them as text under the hub's
name.

## Local API

`GET /api/v1/hub/hello` is the hub's discovery probe: no sign-in, but inside `/api/v1`, so the allowlist runs
first, under the API rate limit per address, `Cache-Control: no-store`, and nothing in it but
`{"product":"just-dashboard","protocol":1,"hub":"available|pending|linked"}` (written by hand, byte for byte the
vectors' bodies). It is not mounted with `JD_HUB_LINK=false` or in agent mode, so those answer `404` as an older
dashboard does. It is the one route under `/api/v1` that answers without a credential: `publicAPIRoutes` in
`routes_test.go` lists it with its exact body, `TestNoAPIRouteAnswersWithoutAuthentication` checks that body, and
the walks of what an API, deploy or MCP key may open read the same list.

| Route | Guards | Does |
| --- | --- | --- |
| `GET /dashboard/hub` | any signed-in principal; the `admin` part only for `system.admin` | The status (shape below) |
| `GET /dashboard/hub/check?url=` | `system.admin`, session; not audited | The URL rule, steps 1 to 8, without starting anything or changing anything: `{hubUrl, machine?, testOrigin}`, `machine` left out when tailscaled could not be asked or the name has no tailnet address. Connect's rule errors and `403 hub_link_disabled`. Settings calls it before Connect to show whose machine the address is |
| `POST /dashboard/hub` `{url, scope}` | `system.admin`, session; audited `hub.link.begin` with `hubNode` | Connect. `400` with the rule's code, `409 already_linked`, `409 link_pending`, `403 hub_link_disabled`, `502 hub_unreachable` (the message says what failed, `bad_token` and `unsupported_protocol` included; a `429` says the hub is not taking new requests now and to try again later, before the hub's own words, protocol 4.7) |
| `DELETE /dashboard/hub/request` | `system.admin`, session; `hub.link.cancel` | Forget the pending request (`204`); the hub is not told |
| `PUT /dashboard/hub/scope` `{scope}` | `system.admin`, session; `hub.link.scope` | Change the scope; the next heartbeat tells the hub. `409 not_linked` |
| `DELETE /dashboard/hub` | `system.admin`, session, `s.destructive`; `hub.unlink` with `hubNotified` | Disconnect (`204`). `409 not_linked` |
| `POST /dashboard/hub/sync` | `system.admin` (an API key may) | A heartbeat now, then the status. `409 not_linked`, `409 hub_url_refused` (an idle link only). A check of the hub's machine that fails this time (no machine at the address, or no address) sends nothing and answers `200` with the reason as `admin.link.lastError`. Held to twelve a minute: a press that would break the pace is skipped |

The `GET`s sit with the every-role reads of the `/dashboard` group (`handlers_selfupdate.go`; `check` with
`system.admin` and `RequireSession` added); the rest sit in its `system.admin` part. The status is `hublink.Status`: optional fields are left out, never `null`; times are
RFC 3339 in UTC. With `JD_HUB_LINK=false` it is exactly `{"enabled":false,"state":"…"}`. Otherwise:

- everyone: `enabled`, `state` (`unlinked`, `pending`, `linked`), `hubUrl` (pending or linked), and while linked
  `hubName`, `scope`, `reachable` (the last heartbeat succeeded, none failed since, younger than three
  intervals), `lastHeartbeatAt` once one succeeded, and `fleet` and `notices` (`[]` before the first reply);
  `signIn` is `{enabled: false, offered, users}`, `offered` and `users` from the last reply.
- `system.admin` also gets `admin`: `fingerprint` (once the identity exists), `testOrigin`, `request` (pending:
  `hubUrl`, `userCode`, `approveUrl`, `expiresAt`, `scope`, and `hubMachine` when step 8 found one), `link` (linked:
  `serverId`, `linkedAt`, `linkedBy`, `hubMachine` once one is pinned, `lastError` (an idle link's reason, whatever
  the last exchange left), `lastCommand`, `hubKeys` of `{kid, fingerprint, notAfter?}`, `updatePolicy`, and
  `lastSnapshot`, the exact body of the last heartbeat sent), `lastOutcome` (unlinked after a request that ended without a
  link). `admin.grants` belongs to hub sign-in and is never sent yet.

`GET /auth/session` carries `hub: {linked: true, name, url, reachable}` only for a fully signed-in session (not one
owing a second factor or a password change, and never to nobody), with `JD_HUB_LINK` on and a hub link stored.
The fleet and notices come from `GET /dashboard/hub`, so this stays small.

## Terminal

`scripts/manage.sh hub connect URL [--scope observe|operate|maintain] [--token-stdin] [--wait 15m]`,
`hub disconnect` and `hub status` check their arguments and run `docker compose run --rm --no-deps -T backend
--admin hub-connect|hub-disconnect|hub-status …` (`admin/hub.go`), keeping the exit status with `exec`. They read
`JD_HUB_LINK`, `JD_HUB_LINK_TEST_ORIGIN` and `JD_MASTER_KEY` from the Compose environment; `connect` and
`disconnect` need the master key, `status` does not.

`hub connect` applies the rule, prints the hub URL, the hub's machine and owner (`hublink.DescribeMachine`: "<name>,
a machine of <owner>", "<name>, tagged <tags>", or "not checked" with why), the code and this server's fingerprint,
and waits (default 15 minutes, at most an hour). The default scope is `maintain`. **A token is only ever read from
standard input** (protocol 8.4): `--token TOKEN` and `--token=` exit 1 with "pass the token on standard input with
--token-stdin", in `manage.sh` and in `admin/hub.go` both. With `--token-stdin`, `manage.sh` prompts `Link token: `
and reads with `read -rs` at a terminal, or one line from a pipe (the hub's installer), and passes it to the admin
command on its standard input, so it is in no process list (`/proc/*/cmdline`) and no shell history. A token that
is not `jht_` and 43 base64url characters is refused before anything is sent, and one the hub refuses
(`bad_token`) is a failure, never a fallback to a code. Exit **0** connected (`Connected to Just Hub "<name>" at
<url> as "<server>" (<scope>).`, then the pinned hub key's fingerprint to compare with the hub's Settings page),
**2** declined, not approved within `--wait`, or interrupted (the request is forgotten), **1** anything else.
`hub status` says when the link is idle, with the reason, instead of the last error. A request begun in Settings is never taken over from the terminal. Audited as `hub.link` (by
`root` / `cli`; failures as failed with the outcome) and `hub.unlink`.

## Failure modes

The account link's table applies, with these for the hub:

| What happens | What the dashboard does |
| --- | --- |
| The hub is down or unreachable | Heartbeat and poll back off from 1 s to 5 minutes; `reachable` goes false; nothing else on the dashboard depends on the hub |
| A reply with an unknown top-level field, or without `ok`, `heartbeatSeconds`, `pendingCommands`, `fleet` or `notices` | A failed heartbeat; the cached view is left as it was and nothing in the reply is used |
| A bad element in `fleet`, `notices` or `signIn.users` | That element is dropped; the heartbeat succeeds with the rest |
| A reply that arrives after a disconnect | Nothing is written: no view, no interval, no status |
| `410` on any signed request | The hub removed this server: link, policy, status and cached view cleared in one transaction, identity and sequence kept, `hub.unlink` by `system` with the reason "the server was removed in the hub" |
| The server moved to another tailnet, or the test origin was removed | The link is set idle in the store at the next start and says why; it stays idle until a person disconnects |
| Another machine answers at the hub's name | Nothing is sent; the link is set idle ("the hub's machine changed") and `hub.link.node_changed` audited; a pending request ends as failed with nothing stored |
| No machine holds the hub's address | Nothing is sent; the exchange fails and backs off; the link stays as it is. A manual sync answers `200` with the reason on the status |
| The hub's name has no tailnet address | Connect fails with `502 hub_unreachable` before its request. A check that is due sends nothing (not even on a connection kept open) and the exchange or poll fails and backs off |
| The hub holds too many waiting requests (`429` on Connect) | Connect fails with `502 hub_unreachable`, saying to try again later |
| The hub is reinstalled with a new key | Its commands fail the key check (refused, audited); disconnect and connect again |
| `JD_HUB_LINK=false` while linked | The link stays stored and idle; nothing is sent; a disconnect clears it and tells no one |

## Not yet built

Hub sign-in (protocol section 9) arrives on a later branch. What is in place for it: the reply's `signIn` is
decoded and cached, and `GET /dashboard/hub` reports it as `signIn.offered` and `signIn.users`; the engine's
`HeartbeatExtra` hook carries extra heartbeat keys whole within the 64 KiB budget; `OnClear` is the transaction in
which grants and ticket records will be deleted with a link, and `OnReply`'s write runs in the heartbeat's own
serverId-guarded transaction, where ending the grants a reply no longer offers belongs; `revokeHubSessionsTx` is
called inside the one write transaction that sets a link idle (an address the rule refuses at start, or another
machine at the hub's name) and is where the hub sessions are revoked. Still to come with it: `JD_HUB_SIGNIN`,
`hub_signin_grants` and `hub_signin_tickets`, `PUT`/`DELETE /account/hub-signin`,
`DELETE /dashboard/hub/signin/{userId}`, `admin.grants` and `signIn.mine`, `POST /auth/hub/enter`, the
`/hub/enter` page, `403 hub_session_not_allowed`, and the ticket vectors. The written security review of the
exception to invariant 2 must be done before that code is switched on anywhere.

The pages' half of protocol 8.10 and 8.2 (the Settings page calling `check` and showing the machine before Connect,
`hubMachine` and the key fingerprint to compare, `?connect=` leaving the scope unchosen, and notices, fleet names
and the hub's name drawn under "Just Hub (<name>) says") is the hub UI branch's.

## Verification

```bash
scripts/test-changed.sh origin/patch/0.8.5
cd backend && go test ./internal/hublink/... ./internal/accountlink/... ./internal/admin ./internal/config
cd backend && go test ./internal/api -run 'Hub|Link|NoAPIRoute|EveryAPIRoute'
python3 scripts/test_manage.py
```

`hublink` tests run against a fake hub that checks the protocol header, the signature, the time window and the
sequence of every signed request in section 3.1's order with its own reading of the signed string, and answers an
approved poll on every poll until it is collected and after (4.2): connecting with a code and with a token, a
refused token creating nothing, declines, a lost approved answer healing and a repeated one ignored, one hub per
server, the heartbeat body and the cached view (bad elements of each list dropped with the heartbeat still
succeeding, endpoints and links removed, the text rule), strict top-level replies, a reply after a disconnect
writing nothing, `siteKeys` in a reply changing no pinned key, the clock-based sequence surviving a restored
database, the hub's words quoted as the hub's, every kind at every scope run as `hub:hub` or refused, replay,
expiry, a forged key, an unpinned `kid`, a command for another server and an unknown kind (refused, reported and
audited, and only commands past the signature recorded in `hub_link_commands`), disconnect, removal in the hub, a
stored link that no longer passes the rule, and both links side by side. `node_test.go` reaches the same fake hub at
`https://hub.tailed39ba.ts.net` through a test transport, so the URL rule and the hub node check run: the node
pinned at connect, another `StableID` idling the link and surviving a restart, `ErrNotAPeer` backing off and
`ErrUnavailable` sending, the five-minute cadence, a node recorded later, a pending request ended by another machine
(from the running dashboard and from the terminal command), a test-origin link idled once the variable is gone,
`Check` (and that a `Check` of the linked hub's address while another machine holds the name still lets the next
heartbeat find it, idle the link and send nothing), the dialer's gate (with nothing pinned, another machine's name
refused), a name with no address sending no poll and no heartbeat, a sync that finds no machine succeeding with the
reason on the status, and a `429` on Connect saying to try again later. The vectors test reads
`hub-link-vectors.json` and runs every key, canonical-JSON, rejected-args, request and command case and the hello
bodies against the dashboard's own code, the hub-link command cases through the real connector. The URL rule, the
test origin, `tailscale status` parsing and the dialer have table tests. The `api` tests cover the routes and their
role rules through the HTTP stack, `check` with tailscaled answering and not, `sync` answering `200` with the reason
when no machine holds the hub's address and `409 hub_url_refused` once another machine holds its name (the stand-in
hub answers at a tailnet name through `hubstub.At`), the begin audit's node, the session field and the probe; the
`admin` and `test_manage.py` tests cover the terminal path, a token refused as an argument and read with echo off at
a terminal included.
