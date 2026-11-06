# Hub link errata, 2026-11-05

Amendments to the hub link protocol and hub sign-in after the adversarial review
the Just Hub repository's `docs/reviews/spec-review-hub-link.md` (24 findings: 2 high, 10 medium,
12 low). Entry HL-n answers finding n. Each entry gives the amendment, the exact sections changed, and the
lines a fixer implements and tests without re-reading the review.

The amended texts are:

- [`hub-link-protocol.md`](hub-link-protocol.md). These amendments were made here first, and JD's copy at
  `docs/internal/backend/hub-link-protocol.md` took them on `feat/0.8.5-hub-link`. Since then JD's copy is the
  source of the text: this repository takes it, and `hub-link-vectors.json`, byte for byte at the JD commit in
  [`JD_COMMIT`](JD_COMMIT), which `scripts/sync-vectors.sh` checks. This file stays the hub's own record, and
  JD keeps no copy of it. ["Since the first pin"](#since-the-first-pin-jds-changes-to-the-shared-text) lists
  what JD changed after that and what the hub did about each change.
- [`hub-link-vectors.json`](hub-link-vectors.json): the sign-in ticket context and cases, and the notes. Keys,
  canonical JSON, rejected arguments, request and command signatures and hello bodies are byte-identical to
  before.
- the Just Hub repository's `docs/architecture.md` sections 5, 9, 11, 14 and 18, plus small consistency edits in 3, 4,
  6.2, 7.2, 8 and 22 that these amendments require (listed per entry).
- the Just Hub repository's `docs/hub-api.md` sections 1.3, 5.4, 5.5, 5.10, 7.2, 7.3, 9.5 to 9.7, 13.6, 13.7 and 16, plus the
  index row in 3 and one field in 6.2.

## Owners

| Tag | Who |
| --- | --- |
| **JD-LINK** | Just Dashboard's hub link branch (`feat/0.8.5-hub-link`, worktree `jd-worktrees/hub-link`): the connector, URL rule, local API `/dashboard/hub*`, `manage.sh hub`, storage. Page work it names is done with the JD hub UI branch |
| **JD-SIGNIN** | Just Dashboard's hub sign-in branch (M7): grants, `/api/v1/auth/hub/enter`, `/hub/enter`, hub sessions |
| **HUB link** | `backend/internal/link` (requests, tokens, approval, verification, heartbeat hand-off, long poll) |
| **HUB auth** | `backend/internal/auth` (users, sessions, second factor, gate) |
| **HUB signin** | `backend/internal/signin` (offer, tickets) and the ticket encoding in `backend/internal/protocol` |
| **HUB other** | `fleet`, `notify`, `httpx`, `licence`, `frontend`, where an entry names them |

| HL | Finding | Sev. | JD-LINK | JD-SIGNIN | HUB |
| --- | --- | --- | --- | --- | --- |
| 1 | Unsigned `siteKeys` rotation; hub name not pinned to a machine | high | **yes** | (revokes on idle) | link |
| 2 | Kill switches leave hub sessions alive | high | | **yes** | |
| 3 | Hub session can mint lasting credentials | medium | | **yes** | |
| 4 | Limits and poll replacement keyed on unsigned `X-JD-Server` | medium | | | **link**, httpx |
| 5 | Server values relayed without typed validation | medium | yes (per-element decode) | | **fleet**, link, notify |
| 6 | Relink tombstones the live server at approval | medium | (no change; test) | | **link**, licence (release call) |
| 7 | Ticket not bound to person or device | medium | | **yes** | **signin**, protocol |
| 8 | Grants name a reusable hub username | medium | | yes (end grants) | **auth** |
| 9 | Grant made without an authenticator survives enrolment | medium | | **yes** | |
| 10 | One-time tokens: argv, `local` not bound, stolen token | medium | yes (`manage.sh`) | | **link**, frontend |
| 11 | Pairing phishing | medium | **yes** | | **link**, frontend |
| 12 | Security claims become false | medium | yes (invariant, M1) | yes (invariant 2, SECURITY.md, M7) | docs (done) |
| 13 | Redeem transaction semantics | low | | **yes** | |
| 14 | New link state must use the serverId guard | low | **yes** | yes (clearLink parts) | |
| 15 | Test origin escape hatch | low | **yes** | | |
| 16 | Sequence does not survive a restored database | low | **yes** | | |
| 17 | `/hub/enter` fragment handling in Next | low | | **yes** | |
| 18 | `/hub/enter` replaces another session silently | low | | **yes** | |
| 19 | Reply can exceed 256 KiB | low | | | **fleet** |
| 20 | Heartbeat pacing edge | low | | | **link** |
| 21 | Unbounded pending requests | low | | | **link** |
| 22 | What a rogue hub can show a dashboard | low | **yes** (display) | | fleet |
| 23 | JSON strictness and vectors | low | | **yes** | **signin**, protocol |
| 24 | Smaller two-way items (a to g) | low | c, f | | auth, signin, httpx |

## What changes on the wire

Nothing has shipped, so version 1 keeps its number. The changes, all within v1:

- **Sign-in ticket payload**: two new required fields, `tsLogin` and `tsNode` (HL-7). Every ticket signature
  in the vectors changed. JD-SIGNIN and HUB signin must move together; a ticket without them is `bad_ticket`.
- **Approved request poll**: answers `approved` on every poll until 10 minutes after `expiresAt`, not once
  (HL-6). A dashboard built to "exactly once" already copes: it stores the link on the first answer and stops
  polling.
- **`siteKeys`**: never sent by a hub, ignored by a dashboard (HL-1). The field stays decodable.
- **`fleet[].version`**: `""` on every entry except `self` (HL-22).
- **Heartbeat budget on the hub**: 15 per server in any 60 s, counted after verification (HL-20). The dashboard
  still sends at most 12 in any 61 s.
- **Heartbeat body**: the hub drops bad fields instead of refusing (HL-5). The dashboard decodes `fleet`,
  `notices` and `signIn.users` element by element (HL-5).
- Request signing, command signing, canonical JSON, the fingerprint and their vectors are unchanged. The
  sequence's value changes (HL-16) but the hub only compares it.

Dashboard-local API (JD's own contract, in protocol section 8): new `GET /dashboard/hub/check`, new
`admin.request.hubMachine` and `admin.link.hubMachine`, new `403 hub_session_not_allowed`, `manage.sh hub
connect --token` removed.

Hub browser API: `ApproveRequest {code}` body, `LinkRequestView.code` nullable and `flags`, `LinkTokenView.node`,
`CreateTokenRequest.node`, token TTL default 15, `CreatedToken.command` without the secret, `SigninTicket.nonce`,
`SigninReason "no_endpoint"`, `HubSettings.shareFleet`; errors `422 code_mismatch`, `409 username_retired`,
`409 local_node_unknown`, `409 no_endpoint`, `410 request_denied`, `fresh_2fa_required` with `reason: "totp"`.

Hub schema (architecture 7.2, all additive; nothing is deployed, so they go into the `CREATE TABLE`
statements): `sessions.totp_at`, table `retired_usernames`, `link_requests.code_failures`,
`link_tokens.node_name`, `signin_tickets.nonce`, `.ts_login`, `.ts_node`.

---

## HL-1. No key rotation on the hub link; the hub's machine is pinned (finding 1, high)

**Amendment.** The heartbeat reply is authenticated by TLS alone, and on a tailnet the hub's MagicDNS name goes
to whichever machine registers it next, so a reply must never change the pinned key. A v1 hub never sends
`siteKeys`; a dashboard decodes it and ignores it (no add, no `notAfter` change, no drop). A new hub key needs a
new link. Separately, the dashboard now records the hub's tailnet node (`StableNodeID`) at connect and sends
nothing to a different machine at that name: the link goes idle. If rotation is wanted in a later version it
must be a statement signed by a currently pinned key with its own first line (for example
`jd hub key rotation v1\n<kid>\n<publicKey>\n<notAfter>`), and must never shorten or end a pinned key.

**Sections changed.** Protocol 1 (Hub key, Hub node), 2.2 step 8 (now required when tailscaled answers) and
"When the rule runs", 2.4 (new, including "While a request is pending"), 3.5 (Key rotation, The peer's machine,
Heartbeat reply rows), 4.2 (the check before every poll), 4.3 (`siteKeys` row, "Nothing in the reply can
change…"), 8.2 (`check`, `hubMachine`), 8.5, 8.7 (`hub.link.node_*`, `hub.link.idle`), 8.9 (Unlinked, Start), 10,
11. Architecture 9 (never sends `siteKeys`; node pinning and restores).

**JD must (JD-LINK).**
- Never apply `siteKeys` on the hub link: give `accountlink.Profile` a switch (for example
  `KeyRotation bool`, false for the hub link) so `noteHeartbeat` does not call `mergeSiteKeysMatching` at all.
  Keep decoding the field so strict decoding passes; log one warning per link when it is present.
- Run 2.2 step 8 on every connect when tailscaled answers. Keep `{id: Node.StableID, name, owner (LoginName) or
  tags}` in `hub.link.request`, copy it into `hub.link.state.hubNode` when the link completes, and put it in the
  `hub.link.begin` audit detail. When tailscaled is unavailable, record the skip there.
- Before sending any request on a stored link, WhoIs the address about to be dialled: at connector start, when
  the dial address differs from the last one checked, and at least every 5 minutes.
  - Different `StableID`: send nothing; set the link idle (persisted in `hub.link.state`) with
    `hub_url_refused` and the message "the hub's machine changed"; revoke every hub session (once JD-SIGNIN
    exists); audit `hub.link.node_changed` (actor `system`, old and new node id and name).
  - `ErrNotAPeer`: send nothing; count a failed exchange (normal backoff) with "the hub's machine is not on
    this tailnet"; do not go idle.
  - `ErrUnavailable`: send; log the skip at most once an hour.
  - No node recorded yet: record the first answer whose node name equals the stored host, audit
    `hub.link.node_recorded`.
  - Test origin: no WhoIs, no node.
- While a request is pending, WhoIs the dial address before **every** request poll, in whichever process polls
  (the running dashboard or `--admin hub-connect`), against the node kept in `hub.link.request`. The approval
  answer is where the hub key is pinned. A different `StableID` sends nothing, ends the request as failed ("the
  hub's machine changed"), audits `hub.link.node_changed` and stores no key, `serverId` or name. `ErrNotAPeer`
  sends nothing and counts a failed poll. `ErrUnavailable` sends. Store an approved answer only from a poll whose
  check passed or was skipped for `ErrUnavailable`; `hubNode` in the link state is the request's node at that poll.
- Setting the link idle and revoking every hub session are one write transaction (HL-2).
- An idle link sends nothing until a person disconnects; `POST /dashboard/hub/sync` answers
  `409 hub_url_refused`.

**Hub must (HUB link, protocol).**
- Never put `siteKeys` in a heartbeat reply: remove the `SiteKeys` field from `protocol.HeartbeatReply`
  (`backend/internal/protocol/wire.go`), so nothing can set it. The approval poll's `siteKey` stays.
- Offer no key rotation in v1.

**Tests.** JD: a reply with `siteKeys` that adds key X and gives the pinned key a past `notAfter` leaves
`hub.link.state` keys unchanged, and a command signed by X is refused at 7.2 step 3. A fake LocalAPI that
answers another `StableID` makes the connector send nothing, set the link idle with `hub_url_refused` and audit
`hub.link.node_changed`; `ErrNotAPeer` backs off without going idle; `ErrUnavailable` sends. With a request
pending, a fake LocalAPI that switches to another `StableID` before the poll that would get `approved` makes the
poller send nothing and end the request as failed, and no hub key is stored (once from Settings, once from the
terminal command). Hub: the encoded heartbeat reply never contains the key `siteKeys`.

## HL-2. Every way to stop hub sign-in ends the hub's sessions (finding 2, high)

**Amendment.** Each grant end, replacing a grant included, revokes that account's hub sessions
(`auth_method = 'hub'`) in the same transaction. The link ending (disconnect, removal, new link), the link going
idle, and `JD_HUB_SIGNIN` off (or `JD_HUB_LINK=false`) at start revoke every hub session, each in the one write
transaction that makes that change. And every time a hub session is used, the dashboard checks again that the
switches are on, the link is stored and not idle, and a grant at least as old as the session exists, so a
revocation that was missed or lost a race cannot leave a hub session working.

**Sections changed.** Protocol 2.2 ("When the rule runs"), 2.4, 8.6, 8.9, 9.3 (the `PUT` row, "A grant ends", full
list with replacement, the revocation rule, and "A hub session lives only while what made it still holds"), 9.7
("Transactions": idle included, no split transactions), 9.9 (Ending), 9.10 (`sessionsRevoked`,
`hub.session.revoke`), 9.11, 9.12.

**JD must (JD-SIGNIN).**
- In the same transaction as each grant end in protocol 9.3 (account removes or replaces it, administrator
  removes it, link ends, account deleted or disabled, password changed or reset, authenticator enrolled,
  replaced, reset or turned off, all sessions revoked, the hub stops offering the hub user, switches off at
  start), revoke every session of that account whose `auth_method` is `hub`. A `PUT /account/hub-signin` while
  a grant exists deletes the old row, revokes its hub sessions and inserts a new row with a new `created_at`, in
  one transaction, even when `hubUser` is unchanged.
- On disconnect, `410` removal, a new link, the link going idle (HL-1, HL-15), and at start when `JD_HUB_SIGNIN`
  is off or `JD_HUB_LINK=false`: delete every grant (not on idle) and revoke every hub session, in the same write
  transaction as the event's own change (setting idle, clearing the link). The start-time clean-up commits before
  the API serves its first request; if it fails, the backend does not start.
- In `auth.Service.ResolveSession`, for a session whose `auth_method` is `hub`: refuse it (delete it, answer as
  for a revoked session) unless `JD_HUB_LINK` and `JD_HUB_SIGNIN` are on, a hub link is stored and not idle, and a
  grant exists for the session's account with `server_id` equal to the link's `serverId` and `created_at` not
  later than the session's `created_at`.
- Audit `hub.signin.revoke` with `reason` and `sessionsRevoked`; `hub.session.revoke` for link and switch events.

**Tests.** One per event, replacement included: make a hub session, fire the event, the session's next request is
`401`. Restart with `JD_HUB_SIGNIN=false`: no grants, no hub sessions, and no request is served before the
clean-up commits. A redeem racing the link going idle leaves no live hub session. With the revocation skipped
(the grant row deleted directly, or the link set idle directly in the store), the hub session's next request is
`401`.

**Sockets (JD-SIGNIN, 2026-11-05).** JD's review found that a WebSocket a hub session opened (a terminal, a
container's shell, a log tail, a Remote Desktop session or stream) was checked only at its upgrade, so it outlived
every revocation above and the two hours. Every socket a hub session opens, and every socket JD proxies for one,
now closes at the session's expiry, at once when a revocation of hub sessions commits, and within 15 seconds of
any other end, by a check that does not count as use. Protocol 9.1, 9.9 ("Ending") and 9.12 say so. JD's test
opens a real socket from a hub session and fires every event of this entry. **Hub must:** nothing.

## HL-3. What a hub session cannot do (finding 3, medium)

**Amendment.** Hub sessions are refused (`403 hub_session_not_allowed`) on every route that mints or changes a
credential or a link, through one middleware, and last at most 2 hours. 9.1 now says plainly what a hub session
can and cannot do, including that an administrator's hub session still has the terminal (not restricted, and
said so), and that ending sign-in does not undo what was done.

**Sections changed.** Protocol 9.1 ("What it costs"), 9.3 (`PUT` refused), 8.2 (hub link writes), 9.9 (2 h; the
route table), 9.11, 9.12. Architecture 18.7.

**JD must (JD-SIGNIN).**
- Add `httpx.RefuseHubSession`: refuses a principal whose session `auth_method` is `hub` with
  `403 hub_session_not_allowed`; identifiable by function pointer, as `requiresSession` finds
  `httpx.RequireSession`.
- Apply it to every route protocol 9.9's rule covers: a route that creates, changes, rotates, imports or exports
  a credential (the classes 9.9 lists, from dashboard accounts to the backup key) or creates or changes a link.
  The rule decides, not the table. At least every route in 9.9's table (verification pass, 2026-11-05: the table
  first listed only keys, accounts, the caller's own credentials, Linux users and the links; it now also lists
  JD 0.8.5's database users and roles, proxy basic-auth users, deployment trigger and hook secrets, saved Git,
  registry and DNS credentials, secret stores, notification channels and log drains, GitHub sign-in and the
  GitHub App, TLS key import and export, and backup key import and export, with their paths). Walk every write
  route of the router, classify each against the rule, and list the result in `invariants.md`; report any route
  the table misses so the protocol's next change adds it.
- Leave open to hub sessions: both disconnects, `POST /dashboard/hub/sync`, grant removal, session revocation, and
  deleting a key, user, credential or channel, and GitHub sign-out (they only take power away).
- Hub sessions: 2 hours absolute, 60 minutes idle.
- The frontend shows `hub_session_not_allowed` as "Sign in on this server itself to do this".

**Tests.** A route-walk test (`chi.Walk`) lists every route carrying the middleware and fails if any route in the
table lacks it. Each listed route with a hub session gives `403`; with a password session it behaves as before.
A hub session is refused 2 hours plus one second after it was made.

**Route walk (JD-SIGNIN, 2026-11-05).** JD's walk of every write route found seventeen routes the rule covers and
the 9.9 table did not list. They carry `RefuseHubSession` on `feat/0.8.5-hub-signin`, and protocol 9.9 now lists
them in its table (rows "Linux accounts and their keys", "Database users and roles", "Git and GitHub", "DNS
credentials, tunnels and TLS keys", the new "Network peers and check machines", "Backups" and "The assistant's
provider keys") and names their classes in the rule's credential list: `POST /system-accounts/bulk`,
`PATCH /system-accounts/groups/{group}`, `POST /databases/host/grant`,
`POST /databases/{id}/server/roles/{name}/privileges`, `POST /git/forge/account`, `POST /network/dns/services/`,
`PUT /network/dns/services/{id}`, `PUT /proxy/tunnel/`, `POST /proxy/tunnel/create`,
`POST /network/vpn/wireguard/{iface}/peers`, `PATCH /network/vpn/wireguard/{iface}/peers/{id}`,
`POST /network/vpn/archive/{file}/restore`, `POST /network/external/enrollments`, `POST /backups/`,
`PUT /backups/{id}`, `POST /backups/import`, `PUT /assistant/providers/{preset}`. The table's
`POST /dashboard/link` is spelt as the router has it, `POST /dashboard/link/`. JD's test sends a hub session to
every route of the list and expects `403 hub_session_not_allowed` from each. **Hub must:** nothing.

**Second walk (JD-SIGNIN, 2026-11-05).** JD's review found that the first walk classified write routes only,
while the rule refuses exports, and that 9.1's "cannot create, change, rotate, import or export any credential"
was not true of the dashboard's general tools. Protocol 9.9's rule now says exporting includes a read (a `GET`
that shows a stored secret), names a database's password, a deployment's secret variables and a remote
computer's saved sign-in and stream pairing among the credentials, and says it covers the dashboard's own forms,
not a file read or written through the Files page or the terminal. 9.9's table gains seventeen routes (rows
"Database users and roles", the new "Database passwords and data copies", "Proxy basic-auth users", "Deployment
secrets and saved credentials", the new "Deployment secret variables", "Network peers and check machines" and
the new "Remote Desktop"): `GET /network/vpn/wireguard/{iface}/peers/{id}/config`, `GET /databases/{id}/url`,
`PUT /databases/{id}/redis/config`, `POST /deploy/{id}/environments/{env}/releases/{release}/data-copies`,
`GET /deploy/{id}/environments/{env}/data-copies/{copy}/url`, `GET /deploy/{id}/env/reveal`,
`GET /deploy/{id}/environments/{env}/variables/{name}/reveal`,
`POST /deploy/{id}/environments/{env}/variables/{name}/generate`,
`POST /deploy/{id}/environments/{env}/variables/{name}/rotate`, `GET /deploy/shared-variables/{name}/reveal`,
`GET /deploy/{id}/shared-variables/{name}/reveal`, `POST /deploy/`, `POST /proxy/import/npm`,
`POST /proxy/import/npm/apply`, `POST /remote-desktop/computers`, `PUT /remote-desktop/computers/{id}`,
`POST /remote-desktop/computers/{id}/stream/pair`. 9.1's "What it costs" now says plainly that the Files page
reads every file under `JD_FILE_ROOTS` (`/` by default) as root for every role, that `file.write` (administrator
and `limited`) writes any file, and that for those roles a hub session is as good as root, as the terminal makes an
administrator's. JD's route-walk test now also reads the 9.9 table and `invariants.md`'s and fails when either
differs from the router. **Hub must:** nothing; the hub's own description of what a hub session cannot do follows
9.1 (`docs/architecture.md` 18.7 now does).

**The code route (JD-SIGNIN, 2026-11-05).** The end-to-end run of sign-in found that a correct, unused
authenticator code sent from a hub session to `POST /auth/2fa/verify` was answered `401 invalid_code`, spent a
guess from the account's budget and was audited as a wrong code: the session update that turns a hub session
into an ordinary one skipped it, and the failure read as a wrong code. A hub session is made complete and never
owes a code, and one given from it would end every limit of 9.9, so the route now refuses a hub session with
`403 hub_session_not_allowed` before a guess is spent. Protocol 9.9 lists it under "The caller's own
credentials". **Hub must:** nothing; the hub never sends a code to a server.

## HL-4. Nothing keyed on the named server before verification (finding 4, medium)

**Amendment.** Before the signature and sequence pass, the hub spends only a budget keyed on the caller's tailnet
address. Per-server budgets, poll replacement, last-seen, node recording and activity come after.

**Sections changed.** Protocol 3.1 (new paragraph after the checks), 4.4. Architecture 5.2 (layer line and new
paragraph), 5.3 (`Link` comment), 9 (row texts and the paragraph after the checks), 18.1, 18.5 (new and renamed
rows).

**Hub must (HUB link, httpx).**
- The `/api/hub/v1` group has one limiter before the handler: keyed on the caller's tailnet IP, 240 a minute,
  burst 60 (all routes except `POST /requests`, which keeps its own 10 a minute).
- In `link`, after `verify` passes steps 1 to 5: the per-server heartbeat budget (15 in any 60 s, HL-20) and the
  per-server signed-call budget (120 a minute, burst 30).
- Only after verification: long-poll replacement, `last_seen_at`, `node_id`/`node_name`/`tailnet_ip`,
  `server.node_changed`, any activity about the server.
- A request that fails verification writes nothing to the row it names.

**Tests.** Twenty heartbeats with a victim's `X-JD-Server` and a bad signature, then the victim's real signed
heartbeat: `200`. An unsigned `GET /commands` naming the victim does not end the victim's open poll. A
bad-signature heartbeat from another node records no `server.node_changed`.

## HL-5. The hub validates what servers report; the dashboard decodes lists element by element (finding 5, medium)

**Amendment.** The hub decodes the snapshot field by field into typed values (new protocol 5.3): wrong types and
out-of-rule values are dropped as if absent and the heartbeat is still accepted; strings have length limits and
no control or format characters; `access.endpoint` is kept only when it is an `https` origin whose host is the
heartbeat's WhoIs node name. Only the re-encoded typed snapshot is stored, served and relayed. The dashboard
decodes `fleet`, `notices` and `signIn.users` element by element, dropping a bad element instead of failing the
heartbeat.

**Sections changed.** Protocol 2.1 (both bullets), 4.3 (The body), 5.3 (new: rules, field table, endpoint rule,
messages), 6 (Decoding), 6.1, 6.2. Architecture 7.2 (`servers.snapshot_json` comment), 9 (`POST /heartbeat` row,
relay paragraph), 18.7. Hub-api 7.2 (`endpoint` comment), 7.3 (`Snapshot` comment).

**Hub must (HUB fleet and protocol; link hands over the WhoIs peer).**
- `protocol.ParseSnapshot` today decodes the whole body into the `Snapshot` struct, so one wrong-typed field
  fails the heartbeat, and it requires a `server` section. Replace it with a field-by-field decode (each object as
  `map[string]json.RawMessage`, each value decoded and checked on its own) that never fails on a field.
- `Fleet.Ingest(ctx, serverID, at, body, peer)` decodes into the typed snapshot by protocol 5.3: refuse only a
  non-object or `protocol` other than the integer 1 (`400 bad_request`); drop unknown fields, wrong types and
  values outside their rule; drop a list element whose required field failed; cut lists to 5.2's counts; never
  drop `server` (replace its failed required fields with the last good values); replace a bad `sentAt` with the
  receive time.
- Text rule: valid UTF-8, no Unicode Cc, Cf, Zl or Zp character, with the per-field lengths of the 5.3 table.
- `access.endpoint`: keep only an `https` origin (no user info, path, query or fragment, at most 255 characters)
  whose host equals `peer.NodeName`'s full DNS name without the trailing dot; dev mode accepts loopback
  `http`/`https`. Otherwise store no endpoint for this heartbeat (do not keep an older one).
- Store and serve only the re-encoded typed snapshot. Build `fleet`, notices, alerts, activity and messages
  only from it.
- HUB link: flag `endpoint_not_node` on a link request whose `server.endpoint` fails the same rule (HL-11).
- HUB link (verification pass, 2026-11-05): the `POST /requests` body is server text too, and `server.name` and
  `server.hostname` become the server's name that `fleet` relays until its first heartbeat. Check `server.name`,
  `.hostname` and `.os` with the text rule (1–64), `.version` and `.arch` with 5.3's patterns; replace a missing
  or failing one with `unknown` and still create the request (never `400` for a `server` field, so a dashboard
  with an odd OS string still links). Drop a `server.endpoint` that is not an `https` origin of at most 255
  characters; keep and flag one whose host is not the WhoIs node; never use it as the server's endpoint.
  Protocol 4.1 (field table, the paragraph after it, Errors).
- JD (verification pass): a `fleet` entry's `name` follows the text rule of 5.3 (protocol 6.1), not "printable";
  an entry that fails is dropped like any other bad element.
- HUB notify: server text goes into an email header only via `mime.QEncoding.Encode("utf-8", s)` after the text
  rule; into webhook, Discord, Telegram and ntfy bodies only as JSON string values or the channel's escaping.

**JD must (JD-LINK).**
- Decode the reply strictly at the top level; decode `fleet`, `notices` and `signIn.users` as
  `[]json.RawMessage`, then each element strictly into its type plus its rules; drop failing elements; keep at
  most 500, 20 and 50.

**Tests.** Hub: a heartbeat with `"access":{"endpoint":{"x":1}}`, `"server":{"version":7}`, a 10 KiB
`server.name` and CR/LF in `alerts[].subject` is accepted; the stored snapshot lacks those values; every other
server's next reply decodes. Endpoints `javascript:alert(1)`, `http://db-01…`, and another machine's name are not
stored. An email subject built from a name with CR/LF has no raw CR/LF. JD: a reply whose `fleet` holds
`{"serverId":5}` and one good entry keeps the good entry and the heartbeat succeeds; an unknown top-level field
still fails it.

## HL-6. Only collection, by the key holder, ends an older server row (finding 6, medium)

**Amendment.** Approval claims the seat and assigns the `serverId` but creates no server row and ends none. The
first signed approved poll (only the key holder can make it) collects the approval: it creates the server row,
tombstones any live row with the same key, and marks the request consumed, in one transaction. Approved answers
repeat until 10 minutes after `expiresAt`, so a lost answer heals; an uncollected approval then expires and its
seat is released unless a live server holds the fingerprint. Approving a request that would replace a live
server is destructive and flagged when it comes from another node. As in licence-api.md 4.2.

The seat claim stays at approval (licence-api.md 4.4 is unchanged), so the owner sees `plan_limit` when deciding;
what the review objected to, a seat held by a request nobody collects, is fixed by the release at expiry.

**Sections changed.** Protocol 3.1 (last line), 3.5 (Approval, Pending request poll), 4.1 (Asking again with a
known key), 4.2 (Collection; `request_consumed` meaning). Architecture 4.2 (`ServerLinked` comment), 7.2
(`link_requests.server_id` comment), 8 (request sweep row), 9 (rows and "Approval and collection"). Hub-api 3
(index row), 9.5 (`replacesServerId`, `status`, `serverId`, approve bullet).

**Hub must (HUB link; licence only through `Plan`).**
- Approve (owner or token): `Plan.ClaimSeat`, store a new UUID in `link_requests.server_id`, set
  `status = 'approved'`, `decided_at`, `decided_by`. No `servers` row, no tombstone, no `ServerLinked`.
- On a verified `GET /requests/{id}` of an `approved` request: in one transaction insert the `servers` row
  (`last_sequence` = the request's), tombstone any live row with the same `public_key` (`removed_by = 'relink'`,
  queued commands cancelled, no `Plan.ReleaseSeat`: same fingerprint), set `status = 'consumed'`; then publish
  `ServerLinked`. Answer the approved body.
- On a verified poll of a `consumed` request until `expires_at + 600`: the same approved body, same `serverId`.
  After: `410 request_consumed`.
- Request sweep: `approved` and `now > expires_at + 600` → `expired`, and `Plan.ReleaseSeat(fingerprint)` unless
  a live server has that fingerprint.
- Approving a request with `replacesServerId` goes through the destructive limiter; flag `replaces_other_node`
  when the request's WhoIs `StableID` is not that server's `node_id`.

**JD must (JD-LINK).** Nothing new. Confirm by test that a second approved answer after the link is stored is
ignored, and that one after a lost first answer stores the link.

**Tests.** Hub: a request opened from another node with a live server's public key and approved by the owner
leaves the live server untouched (its heartbeats keep getting `200`); never collected, it expires at `+600` and
releases no seat (the live server holds it). A token-approved request that is never polled leaves no server row
and its seat is released at expiry. The first approved answer dropped in transit, the next poll returns the same
`serverId`. Collection by the real key holder tombstones the old row in the same transaction.

## HL-7. Tickets name the login and device that asked (finding 7, medium)

**Amendment.** The ticket payload gains two required fields: `tsLogin` (the hub session's tailnet login, lower
case) and `tsNode` (the `StableNodeID` WhoIs gave the hub for the device asking for the ticket). The dashboard
checks them in a new step 10, after `mfa_stale` and before the nonce, against WhoIs of the browser's address:
another login, another node or a non-tailnet address is `401 wrong_device` and does not spend the nonce. A
loopback client (an SSH tunnel) or an unavailable tailscaled skips the check and audits the skip. Steps after
it are renumbered: nonce 11, grant 12, account 13, session 14.

**Sections changed.** Protocol 9.1 (condition 6), 9.4 (fields), 9.7 (table, step 10, paragraph), 9.10 (audit
fields), 9.11, 9.12, 12. Vectors: `signInTicketContext.client`, every `signInTickets` case, notes.
Architecture 4.4 (`protocol.Ticket`), 7.2 (`signin_tickets.ts_login`, `.ts_node`), 14. Hub-api 13.7.

**Hub must (HUB signin, protocol).**
- `protocol.Ticket` gains `TSLogin` and `TSNode`; `SignTicket` writes them.
- `tsLogin` is the session's `tailnet_login` (lower case); `tsNode` is `PeerFrom(ctx).NodeID` of the
  `POST /servers/{id}/signin-ticket` request. Dev mode: `HUB_DEV_LOGIN` lower-cased and `dev`.
- Store both in `signin_tickets`.
- The encoder writes the payload compact, each key once, in the order of the protocol 9.4 table (`v, typ, hub,
  serverId, userId, hubUser, tsLogin, tsNode, nonce, iat, exp, mfaAt, next`), `next` left out when empty.
- `backend/internal/protocol/vectors_test.go` (`TestHubLinkSignInTickets`) today asserts
  `payloadSegment == base64url(payloadJson)` and `ticket == v1.<kid>.<payloadSegment>.<signature>` for every
  case, and re-signs every `ok` case. Change it to: always check that Ed25519 over `signedString` equals
  `signature`; check the two identities only when `altered` is absent; re-sign only cases with `expect: "ok"`
  and `hubEncoded: true`; and give `VerifyTicket` (used by `linktest`) the device step, fed from the case's
  `client` or the context's.

**JD must (JD-SIGNIN).**
- Step 10 as protocol 9.7: client address as the allowlist reads it; loopback or `ErrUnavailable` → skip and
  audit `deviceCheck: "skipped"` with the reason; else WhoIs: `strings.ToLower(LoginName) == tsLogin` and
  `Node.StableID == tsNode`, otherwise `401 wrong_device`.
- Run every vector through steps 3 to 11 with the case's `client` (or the context's), check `expect`,
  `deviceCheck` and `secondRedeem`.

**Tests.** The 37 `signInTickets` vectors. In particular "another tailnet login" gives `wrong_device` and then
`ok` from the right client (the nonce was not spent).

**An answer that names nobody (JD-SIGNIN, 2026-11-05).** JD's review found that step 10 skipped on every WhoIs
error but `ErrNotAPeer`, including a tailscaled that answered `200` with no node or no login. Only a tailscaled
that cannot be asked (no answer, an error status) skips now; an answer naming no node or login
(`tailnetgate.ErrNoIdentity`) is `wrong_device`. Protocol 9.7 step 10 says so. **Hub must:** nothing.

## HL-8. A hub username names one person forever (finding 8, medium; latent with one hub user)

**Amendment.** Instead of a new immutable id on the wire, the hub retires every username a user gives up (rename
or deletion) and never gives it to another user; the same user may take it back. This gives the property the
review asks for (a grant's `hubUser` can only ever mean one hub user) with no wire change and readable names on
the dashboard. The dashboard gets a real mechanism for "a rename voids grants": a successful heartbeat reply
whose `signIn` is absent or does not list a grant's `hubUser` ends that grant and its hub sessions.

**Sections changed.** Protocol 6.3, 9.1 (condition 3), 9.3 ("A grant ends"), 9.11, 10. Architecture 3 (`auth`
writes `retired_usernames`), 7.2 (`retired_usernames`), 14, 18.2. Hub-api 5.10, 13.6, 16.

**Hub must (HUB auth).**
- Table `retired_usernames(username, user_id, retired_at)`. A rename or deletion inserts the old name with the
  user's id in the same transaction.
- Setup, rename or creation of a name present there with another `user_id`: `409 username_retired`. The same
  user id may take it back.
- `signIn.users` lists only current usernames; a ticket's `hubUser` is the session user's current username.

**JD must (JD-SIGNIN).**
- When storing a successful heartbeat reply (inside the serverId-guarded transaction of HL-14): no `signIn` in
  the reply → end every grant; otherwise end every grant whose `hub_user` is not in `signIn.users`. Each end
  revokes the account's hub sessions (HL-2) and audits `hub.signin.revoke` with reason `hub_user_not_offered`
  or `hub_signin_not_offered`. A failed heartbeat changes nothing.

**Tests.** Hub: rename `owner` to `ion`; renaming or creating another user as `owner` gives
`409 username_retired`; `ion` renaming back to `owner` succeeds. JD: a grant for `owner`, then a reply with
`users: ["ion"]` → grant gone, hub sessions revoked; a reply without `signIn` → all grants gone; a failed
heartbeat → nothing changes.

## HL-9. Enrolling an authenticator ends the grant (finding 9, medium)

**Amendment.** A grant made with the password alone ends when the account enrols an authenticator (and when it is
replaced, reset or turned off), with its hub sessions; the person opts in again with a code. The opt-in code
check reuses `auth.Service.VerifyReauthentication`.

**Sections changed.** Protocol 9.1 (condition 2), 9.3 (`PUT` row, "A grant ends").

**JD must (JD-SIGNIN).**
- `POST /auth/2fa/enable`, any authenticator replacement, `POST /dashboard-users/{id}/reset-totp` and
  `POST /account/2fa/disable` delete the account's grant and revoke its hub sessions in the same transaction;
  audit `hub.signin.revoke` reason `authenticator_changed`.
- `PUT /account/hub-signin` checks password and code with `auth.Service.VerifyReauthentication` (shared guess
  budget, TOTP step consumed), not a new check.

**Tests.** Grant made without TOTP, then enrol: grant gone, hub session refused. A `PUT` reusing the TOTP code of
the same step is `401 invalid_code`. Wrong `PUT` passwords count against the same budget as sign-in.

## HL-10. One-time tokens: off the command line, short, bindable (finding 10, medium)

**Amendment.** A token never appears on a command line: the hub's command is
`sudo ./scripts/manage.sh hub connect <url> --token-stdin`, which prompts with echo off; `--token TOKEN` is
removed. The `local` token is bound to `HUB_LOCAL_NODE`; any token can be bound to one machine; from another
machine it is consumed and refused. The UI's default lifetime is 15 minutes (installer 30).

**Sections changed.** Protocol 4.1 (token bullets), 4.7 (`bad_token`), 8.4 (usage, `--token-stdin`).
Architecture 7.2 (`link_tokens.node_name`), 9 (tokens paragraph, local server). Hub-api 9.6 (`node`,
`ttlMinutes`, `command`, errors), 9.7, 16.

**Hub must (HUB link, frontend).**
- `CreatedToken.command` is `sudo ./scripts/manage.sh hub connect <hub url> --token-stdin`, never containing
  `jht_`. The sheet shows the secret separately with Copy and says "paste the token when asked".
- `ttlMinutes` 5 to 60, default 15. `just-hub link-token --local` keeps 30.
- `local: true` binds `node_name` to `HUB_LOCAL_NODE` lower-cased; refused with `409 local_node_unknown` when it
  is empty. `node` (optional, not with `local`) binds to that machine (first DNS label, lower case).
- `POST /requests` with a bound token from a node whose WhoIs `NodeName` (first label, case-insensitive) is not
  `node_name`: mark the token used, answer `403 bad_token`, create no request, record `link.token_refused` with
  the node.

**JD must (JD-LINK).**
- `manage.sh hub connect`: drop `--token TOKEN` (given anyway: exit 1, "pass the token on standard input with
  --token-stdin"). With `--token-stdin` and a terminal on standard input, prompt `Link token: ` and read with
  `read -rs`; otherwise read one line. Pass it to `docker compose run --rm --no-deps -T backend --admin
  hub-connect … --token-stdin` on that command's standard input, never as an argument.

**Tests.** Hub: a local token from another node → `403 bad_token`, the token is used, no request; a bound token
from its machine → request created; `CreatedToken.command` contains no `jht_`. JD: `--token X` exits 1; a piped
token links; the arguments built for `docker compose run` contain no token.

## HL-11. Pairing: a typed code on the hub, the hub's machine on the dashboard (finding 11, medium)

**Amendment.** Hub side: every approval made in the hub requires typing the code the dashboard shows (a token
request held for a seat included; only a valid token's own approval at `POST /requests` needs none), and the hub
never shows a pending request's code (otherwise the owner would copy it from the hub); five wrong codes deny the
request. The approval screen leads with the WhoIs node and login and shows flags (same name pending, endpoint not
the node, node unknown, replaces a server on another node). Dashboard side: a new `GET /dashboard/hub/check`
shows the hub's machine and owner before Connect; a `?connect=` link is marked as such and leaves the scope
unchosen; the pinned hub key fingerprint is shown after linking for comparison.

**Sections changed.** Protocol 4.1 (approval paragraph), 8.1 (Starting a link from the hub), 8.2 (`check` route,
The check, `hubMachine`, field table), 8.4 (prints), 10. Architecture 7.2 (`link_requests.code_failures`), 9,
11. Hub-api 9.5 (intro, `LinkRequestFlag`, `code: string | null`, `flags`, `ApproveRequest`, approve bullet), 16
(`code_mismatch`, `request_denied`).

**Hub must (HUB link, frontend).**
- `POST /pairing/requests/{id}/approve` takes `{code}`; normalise with `protocol.NormalizeCode`; a mismatch
  increments `code_failures` and answers `422 code_mismatch` (`field: "code"`); the fifth mismatch sets
  `denied` (`410 request_denied` after).
- `LinkRequestView.code` is `null` while `pending`. `flags` computed per the `LinkRequestFlag` comments.
- The approval screen leads with `node`, shows each flag as a warning, has a code input instead of showing the
  code, and confirms a replacement as destructive.

**JD must (JD-LINK; page with the JD hub UI branch).**
- `GET /dashboard/hub/check?url=` (`system.admin`, session only): URL rule steps 1 to 8, answer
  `{"hubUrl", "machine"?, "testOrigin"}`; errors as Connect's.
- Settings → Hub calls it as the address is typed or filled and shows "<name>, a machine of <owner>" (or
  "tagged <tags>", or "not checked") before Connect.
- `?connect=`: a notice that the address came from a link; no scope preselected; Connect disabled until one is
  chosen.
- `admin.request.hubMachine`, `admin.link.hubMachine`; next to `admin.link.hubKeys[].fingerprint`, "compare with
  the key fingerprint on the hub's Settings page".
- `manage.sh hub connect` prints the hub's machine and owner, and on success the pinned key fingerprint.

**Tests.** Hub: approve without `code` → `400`; five wrong codes → denied; `GET /pairing/requests` never returns a
pending request's code. JD: `check` with a fake LocalAPI returns the machine; with `ErrUnavailable` it omits
`machine`; the `?connect=` page has no default scope.

## HL-12. Security claims (finding 12, medium)

**Amendment.** The hub's own documents now say what hub sign-in adds. JD's documents must say it before the code
lands.

**Sections changed.** Architecture 18.7 (rewritten: commands only within scope, plus full sessions for opted-in
accounts with the limits of protocol 9.9, plus what the hub relays). Protocol 9.1 ("What it costs").
Verification pass, 2026-11-05: 18.7 said a hub session "works only from the device that asked for it", which is
false once the cookie exists (step 10 checks the device only at redeem); it now says the session "can be opened
only from the device that asked the hub for its ticket", and lists 9.9's full credential set.

**JD must (JD-LINK, in the same change as the M1 hub link code).** Add a hub link invariant to
`docs/internal/security/invariants.md`, the analogue of invariant 12: outbound only; the hub key pinned at
approval and never rotated by a reply; the hub's machine pinned (2.4); scope chosen and enforced on the
dashboard; the closed command set; audit as `hub:<hub name>`; link changes session-only; hub text shown as the
hub's (8.10).

**JD must (JD-SIGNIN, in the M7 change).** Add the invariant 2 exception with the six conditions of 9.1, the
hub-session limits (the 9.9 route set, 2 hours) and the kill-switch behaviour of 9.3. Update `SECURITY.md`:
"Two-factor is enforced for every account that has enrolled" gains "except accounts that opted in to hub
sign-in on a server whose operator turned it on"; the link paragraph keeps "outbound only" for the link and
names the one inbound credential, `POST /api/v1/auth/hub/enter`.

**Docs follow-up (not in this change).** `docs/design-report.html`: the threat row "The hub itself is taken over"
("No shell, files, accounts or secrets") and "the link adds no new way into the server" need the same correction
as architecture 18.7.

## HL-13. Redeem transactions (finding 13, low)

**Amendment.** Step 11 (nonce) commits on its own. Steps 12 to 14 run in one write transaction that re-reads the
link and both switches; a refusal there does not give the nonce back. Grant and link events are write
transactions that revoke hub sessions, so either order is safe.

**Sections changed.** Protocol 9.7 (step 12 text, Transactions), 9.8.

**JD must (JD-SIGNIN).** `INSERT … ON CONFLICT(nonce) DO NOTHING` in autocommit; then `BEGIN IMMEDIATE`; re-read
`hub.link.state` (stored, same `serverId`, not idle) and the switches (else `403 hub_not_linked`); read the grant
(`403 not_permitted`) and the account (`403 account_unavailable`); insert the session; commit; set the cookie.

**Tests.** A hook disconnects between steps 11 and 12: `403 hub_not_linked`, no session. A disconnect that commits
right after the redeem's commit revokes the new session.

## HL-14. New link state is written under the serverId guard (finding 14, low)

**Amendment.** Everything a heartbeat reply changes is written in the `updateState(serverId)`-style transaction;
status writes about a link that is gone are skipped; `clearLink` and `Unlink` delete grants and ticket records
and revoke hub sessions.

**Sections changed.** Protocol 8.5 (`hub.link.view`), 8.9 (Writes belong to the link they describe; Disconnect).

**JD must (JD-LINK).** Write `hub.link.view` (and the interval, and HL-8's grant ends once they exist) inside the
transaction that re-reads `hub.link.state` and writes nothing when its `serverId` is not the one the heartbeat
was sent for. Give `setStatus` the `serverId` and skip the write when the stored link is gone or another.

**JD must (JD-SIGNIN).** Add the grant and ticket deletes and the hub-session revocation to the `clearLink` and
`Unlink` transactions.

**Tests.** A heartbeat in flight during a disconnect returns after it: `hub.link.view` stays absent and status is
not rewritten. Reconnect to another hub at once: no old `users` or notices are served before its first reply.

## HL-15. The test origin closes, and its rules are one rule (finding 15, low)

**Amendment.** Steps 1 to 4 of the URL rule run at every connector start without a tailnet, so a stored `http`,
loopback or IP-literal link that is not the current test origin goes idle. At the test origin exactly two rules
relax: a notice `href` on the stored URL's origin whatever its scheme, and fleet endpoints that are loopback
`http`/`https` (or pass the MagicDNS rule). `JD_HUB_SIGNIN=true` with a test origin refuses to start unless built
with the `hublinktest` tag.

**Sections changed.** Protocol 2.2 ("When the rule runs"), 2.3, 6.1 (endpoint rule), 6.2 (`href`), 8.6.

**JD must (JD-LINK).** Re-apply steps 1 to 4 at each connector start (idle with `hub_url_refused`, revoke hub
sessions); apply the two test-origin rules exactly; refuse to start with `JD_HUB_SIGNIN=true` and
`JD_HUB_LINK_TEST_ORIGIN` set unless built with `-tags hublinktest` (a `//go:build hublinktest` file sets the
allowance).

**Tests.** A stored `http://127.0.0.1:43180` link with the variable removed goes idle at start. At the test origin
an `https://example.com` fleet endpoint is dropped and an `http://127.0.0.1:18443` one kept. The start refusal in a
build without the tag.

**Docs follow-up (done).** `jd-local.sh --hub-signin` builds JD with `-tags hublinktest`, and the tags and
`GOFLAGS` are part of the key of the cached build, so a tagged build and an untagged one never stand in for each
other. `docs/e2e.md` 3.1 (the `JD_HUB_SIGNIN=true` row) and `scripts/e2e/README.md` say so;
`scripts/e2e/jd-local_test.sh` builds a stand-in both ways.

## HL-16. The sequence continues above a restored database (finding 16, low)

**Amendment.** The hub link's sequence is `max(stored + 1, unix milliseconds)` in the same statement.

**Sections changed.** Protocol 3.1 (The sequence), 3.5 (Sequence row), 8.5, 11.

**JD must (JD-LINK).** A `Profile` switch (on for the hub link) makes `kv.nextSequence` run
`INSERT INTO settings(key, value) VALUES(?, CAST(? AS TEXT)) ON CONFLICT(key) DO UPDATE SET value =
CAST(MAX(CAST(value AS INTEGER) + 1, ?) AS TEXT) RETURNING value` with the current unix milliseconds. The account
link may adopt it later; nothing here requires that.

**Hub must (HUB link).** Nothing: `last_sequence` is `INTEGER` and compared with "greater than".

**Tests.** Stored 5 and clock 1761350400000 → 1761350400000; stored above the clock → stored + 1; a database copy
restored from before 1,000 requests keeps linking against the hub fake.

## HL-17. `/hub/enter` removes the fragment first (finding 17, low)

**Amendment.** The page is outside the `(dashboard)` group; its first script is a nonce'd inline script in
`<head>` that reads the hash, keeps the ticket, and calls `history.replaceState` before any other script; nothing
redirects on that path; no code copies `location.hash` into a URL, parameter, storage or log.

**Sections changed.** Protocol 9.5 (step 1 and the closing line), 9.12.

**JD must (JD-SIGNIN).** As the amendment. The root layout and `AuthProvider` skip their signed-out redirects
while the path is `/hub/enter`.

**Tests.** Browser: open `/hub/enter#ticket=…`; the first script in the document is the inline one; after load
the URL and the history entry have no fragment; the only requests are `GET /api/v1/auth/session` and the redeem.
A source test fails if any code reads `location.hash` into a URL.

## HL-18. `/hub/enter` asks before replacing another account's session (finding 18, low)

**Amendment.** If the browser is signed in as an account other than the ticket's `userId`, the page asks
("Signed in as ion. Continue through Just Hub?") and redeems only on Continue. The same account goes on at once.
HL-7's device binding already stops another person's ticket from working in this browser.

**Sections changed.** Protocol 9.5 (step 2), 9.9.

**JD must (JD-SIGNIN).** As the amendment; the `userId` is read from the payload segment for display only.

**Tests.** Browser: signed in as account 2, a ticket for account 1 shows the question and no redeem call until
Continue; signed in as account 1, it redeems at once.

**Failing closed, and on the server (JD-SIGNIN, 2026-11-05).** JD's review found that a session read answering
anything but `200` (a `429`, a `5xx`, no answer) let the page redeem unasked. The page now redeems unasked only
for `401`, the same account, or a session owing its second factor, and otherwise asks, without a name when it
cannot read one. After Continue it sends `"replace": true` beside the ticket; without it the dashboard refuses a
redeem over another account's complete session with `409 other_account_signed_in` and leaves both sessions as
they were (the ticket is spent). Protocol 9.5 (steps 2 and 3), 9.6 (body), 9.7 (step 14), 9.9 and 9.11 say so.
**Hub must:** nothing; the hub never calls the redeem.

## HL-19. The hub keeps the reply under 240 KiB (finding 19, low)

**Amendment.** The hub orders `fleet` with `self` first, then by name, and removes entries from the end (never
`self`) until the encoded reply is at most 240 KiB; only then notices from the end.

**Sections changed.** Protocol 4.3 (Size), 6.1 (Size). Architecture 9.

**Hub must (HUB fleet).** As the amendment, measuring the encoded reply.

**Tests.** 500 servers with 64-character multi-byte names and 20 maximal notices: the reply is at most 240 KiB,
`self` is first, notices are all present while `fleet` has more than `self`.

## HL-20. The hub allows 15 heartbeats a minute (finding 20, low)

**Amendment.** The hub accepts 15 per server in any 60 s, counted after verification; the dashboard keeps pacing
to 12 in any 61 s at send time.

**Sections changed.** Protocol 3.5, 4.3 (Rate). Architecture 9, 18.5, 22.

**Hub must (HUB link).** As the amendment. **JD:** no change.

**Tests.** 15 verified heartbeats in 60 s are accepted; the 16th is `429`.

## HL-21. Pending requests are capped (finding 21, low)

**Amendment.** At most 3 pending requests per tailnet node (a fourth expires that node's oldest) and 50 in all
(`429 rate_limited`). A request a token approves at once does not count.

**Sections changed.** Protocol 4 (table), 4.1 (Pending requests), 4.7. Architecture 9, 18.5. Hub-api 9.5.

**Hub must (HUB link).** As the amendment, keyed on the WhoIs `StableID` (the source address if WhoIs gives none).

**Tests.** A fourth request from one node expires the first; the 51st pending request is `429` and creates
nothing.

## HL-22. Hub text is shown as the hub's; fleet sharing is the owner's choice (finding 22, low)

**Amendment.** The dashboard shows every piece of hub text as plain text attributed to the hub, never styled as
its own prompt, with notice links naming their host (new protocol 8.10). The hub sends `version: ""` for every
fleet entry except `self`, and has an owner setting "Share the server list with linked servers" (default on);
off, `fleet` holds only `self` and no notice names another server.

**Sections changed.** Protocol 6 (Shown as the hub's), 6.1 (`version`, What the hub shares), 6.2, 8.10 (new).
Architecture 9, 18.7. Hub-api 6.2 (`shareFleet` in `HubSettings` and the patch).

**JD must (JD-LINK; the page parts with the JD hub UI branch).** Protocol 8.10: "Just Hub (<name>) says: …"
framing for notices, fleet names, `owner.label` and hub error messages; never in the styling or place of the
dashboard's sign-in, session, password or two-factor prompts; notice links show their host and open with
`rel="noopener noreferrer"`.

**Hub must (HUB fleet, frontend).** `version: ""` on non-`self` entries; `link.shareFleet` (default `true`) in
`HubSettings`/`PATCH /settings`; when off, `fleet` is `[self]` and notices about other servers are not sent.

**Tests.** Hub: with sharing off, a reply holds one fleet entry and no `offline-<other id>` notice; other entries'
`version` is always `""`. JD: a notice titled "Your session expired" renders inside the hub-attributed card.

**Docs follow-up.** Architecture 7.3 (owned elsewhere right now) needs the row `link.shareFleet | fleet |
true or false, default true`.

## HL-23. Ticket JSON is strict, and the vectors pin it (finding 23, low)

**Amendment.** Protocol 9.4 now states the payload's JSON rules (keys case-sensitive, no repeated key, exactly
the listed keys, plain decimal integers, no `null`), 9.7 step 3 names the base64url alphabet, and the vectors
grew from 12 to 37 cases: the accepted side of each time boundary (`now = iat − 30`, `now = exp + 30`,
`mfaAt = iat − 600`), `mfaAt = iat + 1`, a pretty-printed payload with keys in another order, a key in another
case, a repeated key, `userId` as `1.0`, as `"1"` and as `0`, an unknown field, `next: null`, `next` with a
backslash, the pre-amendment payload, an upper-case `tsLogin`, five parts, `=` padding, the standard base64
alphabet, a ticket over 2048 characters, and the six device cases of HL-7. They were checked by two independent
implementations (Go with `json.Decoder.Token`; TypeScript); a Go struct decoder fails three of them, as intended.

**Sections changed.** Protocol 9.4 (rules), 9.7 (steps 3 and 6), 12. Vectors (context, cases, notes).

**JD must (JD-SIGNIN).** Decode the payload with `json.Decoder.Token` (as `decodeArgs` does), never into a struct;
pass all 37 cases.

**Hub must (HUB signin, protocol).** The encoder writes each key once, with these spellings, integers in plain
decimal and no `null`; the `protocol` tests read the new per-case fields `client`, `deviceCheck`, `altered`,
`hubEncoded` and `note`, `payload: null`, and `signInTicketContext.client` (vector notes say what each means).
`VerifyTicket`, which `linktest`'s fake dashboard uses, must apply the 9.4 JSON rules too: it must refuse the
key-case, repeated-key and `next: null` cases that a struct decoder accepts.

## HL-24. Smaller items, one rule each (finding 24, low)

- **a. Ticket ids and nonces.** The hub's `ticketId` is a ULID and is not in the ticket; the ticket's `nonce` is
  16 random bytes in base64url (22 characters). The hub stores the nonce in `signin_tickets` and its activity
  detail, and returns it as `SigninTicket.nonce`, so its log matches the server's `hub.signin` entry.
  Sections: architecture 4.1 (`NewULID` comment), 6.2, 7.2 (`signin_tickets`), 14; hub-api 13.7. **Hub must
  (HUB signin):** as stated.
- **b. Where the hub key lives.** Moot: the hub never publishes keys (HL-1). The sentence in protocol 4.3 is gone.
- **c. Trailing dot.** Protocol 2.2 step 3 now says `accountlink.originOf` keeps a trailing dot and the hub link
  wrapper removes it first. **JD must (JD-LINK):** strip the dot in the hub link's wrapper, then call
  `originOf`. Test: `https://HUB.tailed39ba.ts.net.:443` → `https://hub.tailed39ba.ts.net`.
- **d. Which second factor.** A recovery code does not stand in for `mfaAt`. A TOTP code sets
  `sessions.twofa_at` and `sessions.totp_at`; a recovery code sets only `twofa_at`. The ticket route needs
  `totp_at` within 5 minutes, else `403 fresh_2fa_required` with `reason: "totp"`; `mfaAt` is `totp_at`.
  Sections: protocol 9.1, 9.4 (`mfaAt`), 10; architecture 7.2 (`sessions.totp_at`), 14, 18.2; hub-api 5.4, 5.5,
  13.7, 16. **Hub must (HUB auth, signin, frontend):** as stated; the code prompt for `reason: "totp"` offers no
  recovery-code link.
- **e. Cross-site rule scope.** The `Sec-Fetch-Site`/`Origin` refusal applies to `/api/v1` only, never to page
  loads (typed URLs send `none`, links from dashboards send `same-site`). Sections: architecture 18.3; hub-api
  1.3. **Hub must (HUB httpx):** as stated. Test: a page load with `Sec-Fetch-Site: same-site` gets the page; an
  `/api/v1` POST with it gets `403 cross_site`.
- **f. Backups started by the hub.** Protocol 7.1 `backup.run`: the trigger is `hub_link`. **JD must (JD-LINK):**
  in `startBackupRun`, an actor starting `hub:` gives trigger `hub_link` and the failure log "backup started by
  Just Hub failed".
- **g. `approveUrl` example.** Protocol 4.1 and 8.2 now show `/servers/add/?request=<id>`, and 4.2 says Just Hub
  sends its configured heartbeat interval (reconciliation.md errata E1 and E2, applied). Architecture 22 marks it
  settled.

## Since the first pin: JD's changes to the shared text

The first pin, `5e17e45c` on `feat/0.8.5-hub-link`, held this repository's text. JD then changed the protocol
on all three of its branches, and nothing that is signed or in the vectors changed. The pin is now the tip of
`feat/0.8.5-hub-signin`. What changed, and what the hub must do (2026-11-05):

- **8.1, the Connect door when signed out** (JD-LINK UI). A signed-out browser is sent to sign in and back to the
  Connect address, rebuilt from its `connect` value. **Hub must:** nothing; Connect keeps opening
  `<endpoint>/dashboard/hub?connect=<hub dns name>`.
- **8.2, disconnect** (JD-LINK). `DELETE /dashboard/hub` answers `200 {"hubNotified": bool}`, not `204`: the
  link is cleared either way, and a hub that was not told still lists the server until it is removed there.
  **Hub must:** nothing on the wire (the hub never calls it). The link runner's settings door expects `200`
  with `hubNotified: true`.
- **8.2 and 8.3, the status** (JD-LINK UI). New `heartbeatSeconds` and `idle`, `hub.idle` on the session, and
  `admin.lastOutcome` `removed` after a removal in the hub. `admin.link.lastError` drops a poll's failure once a
  poll has been held 2 seconds, "since the hub answers a failure at once and holds only a poll it took (4.4)".
  **Hub must:** answer every refusal of a command poll at once and hold only a verified poll it registered. It
  does: the signature, the budgets and the `wait` check all answer before the poll is registered. The link runner
  checks that a removed server reads `lastOutcome` `removed` and that `hub status` says it was removed in the
  hub.
- **8.4, `hub connect`** (JD-LINK). It prints the hub's approval screen for the request (`approveUrl` as the hub
  gave it), and refuses a second `hub connect` while one still waits. **Hub must:** answer `approveUrl` as
  `<hub url>/servers/add/?request=<requestId>` on the stored hub URL's origin (architecture 9, `POST
  /requests`). It does; the link runner checks that the printed address is the pending request's approval
  screen, and that Settings shows the same `approveUrl`.
- **8.9, disconnect order and removal** (JD-LINK). The dashboard clears the link first and only then sends
  `POST /unlink`, when the link may still send (switched on, not idle, the hub's machine checked). The text
  says "the hub answers `410` to every request still in flight on it, the running dashboard's included when the
  terminal disconnects", and a loop that hears it while no link, or a newer one, is stored records nothing.
  **Hub must:** answer `410 server_removed` to a long poll it is holding when the server is removed, whether by
  the owner or by the server's own `/unlink`, at once, where it used to answer `{"commands": []}`; and answer
  `410 server_removed`, not `500`, to a heartbeat whose server is removed while the hub stores it. Done:
  `link/queue.go` and `link/heartbeat.go`, tested by `TestRemoveServer`, `TestUnlinkAnswersTheHeldPoll410` and
  `TestHeartbeatRacingARemoval`; architecture 9's `GET /commands` and `POST /heartbeat` rows say so.
- **9.1, 9.5 to 9.7, 9.9 and 9.11** (JD-SIGNIN). Already recorded above: HL-2 "Sockets", HL-3 "Route walk",
  "Second walk" and "The code route", HL-7 "An answer that names nobody", and HL-18 "Failing closed". 9.5 step 5 adds that `/hub/enter`
  states each refusal in its own words. **Hub must:** nothing.
- **9.10, audit** (JD-SIGNIN). Every `hub.signin` entry's status is the one the redeem answered. **Hub must:**
  link a sign-in entry to the server's `hub.signin` entries. 9.10 records a sign-in under the account's
  username, with only the actor `hub:<hub name>`, and JD's audit log filters on the username and the action, so
  `?username=hub:<hub name>` shows the hub's commands but never its sign-ins. Done: a `signin` entry's
  `serverAuditUrl` is `<endpoint>/audit?action=hub.signin` (hub-api.md 13.5, `activity/handlers.go`).
- **13, changing the protocol.** Unchanged in JD. Its last bullet still says the dashboard's copy takes "the
  errata file" with the amendments, and links to it. JD keeps no copy of this file, so `sync-vectors.sh` now
  compares the protocol and the vectors only. That bullet is a follow-up for JD (below).

## Follow-ups outside this change

These documents are owned elsewhere or were outside this task; each needs the change named.

| Document | Change | From |
| --- | --- | --- |
| `docs/architecture.md` 7.3 | Row `link.shareFleet` (writer `fleet`, `true`/`false`, default `true`) | HL-22 |
| `docs/architecture.md` 17.4, 17.5 | `just-hub link-token --local` binds the token to `HUB_LOCAL_NODE` and refuses while it is empty (a host without Tailscale cannot link anyway: protocol 2.2 step 5) | HL-10 |
| `docs/ui-guidelines.md` 22.6 (Add server) | Screens 2 and 3: the hub shows no code; the owner types the code the server shows (h1 "Type the code db-01 shows"); lead with the WhoIs node; show `flags`; confirm a replacement as destructive. "By command": the command without the token, the token separately with Copy, "paste the token when asked", 15-minute default. The approved-but-not-collected state ("Waiting for db-01 to pick up the approval…"). Also the spine label "Check the code" becomes "Type the code", the vocabulary row `code` becomes "the 8 characters the server shows and the owner types into the hub", and the "By command" bullet's `--token …` becomes `--token-stdin` | HL-6, HL-10, HL-11 |
| `docs/ui-guidelines.md` 22.11 (Hub, Account) | Turning hub sign-in off ends every grant on every server; renaming retires the old name for good | HL-8 |
| `docs/design-report.html` | "The hub itself is taken over" row and "the link adds no new way into the server"; the installer's `--token <one-time>` command becomes a pipe into `--token-stdin`. Pairing (found in the verification pass): the summary bullet "check that both screens show the same 8-character code", the sequence diagram's step 5 label and `aria-label`, its caption ("approves it after checking that the codes match") and the steps list ("Both tabs show the same code … check they match and click Approve") all describe the one-click compare that HL-11 removed; each becomes "type the code the server shows into the hub" | HL-10, HL-11, HL-12 |
| JD `docs/internal/backend/hub-link-protocol.md` section 13 | Taken: JD's copy and the vectors are the source now (see "Since the first pin"). Its last bullet still says the dashboard's copy takes the errata file and links to `hub-link-errata.md`, which JD does not keep; it becomes "this repository's errata stay in the hub repository" or names the hub repository's path | all |
| JD `docs/internal/security/invariants.md`, `SECURITY.md` | HL-12 | HL-12 |
| `docs/licence-api.md` | Nothing: the seat claim stays at approval, and releasing an uncollected approval's seat uses the existing `DELETE /seats/{fp}` | HL-6 |
