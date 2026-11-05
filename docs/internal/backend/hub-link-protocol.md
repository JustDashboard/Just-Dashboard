# Hub link protocol, version 1

The contract between a Just Dashboard server and a Just Hub. A hub is a separate app that joins the tailnet as
its own machine and shows many dashboards in one place. This file is normative: the dashboard side (this
repository, `feat/0.8.5-hub-link` and later) and the hub side (the private `just-hub` repository) are both built
from it alone. A change here is a change to both.

The hub link is the account link protocol v1
([`docs/server-link-protocol.md`](https://github.com/JustDashboard/frontend/blob/main/docs/server-link-protocol.md)
in the site's repository, implemented in `backend/internal/accountlink`, described in
[account-link.md](account-link.md)) pointed at a different peer. The signed strings, the canonical JSON, the
fingerprint and the command checks are the same bytes. Section 3.5 lists every difference.

The test vectors are [`hub-link-vectors.json`](hub-link-vectors.json), next to this file. Section 12 says how to
use them.

Amended on 2026-11-05 after an adversarial review. [`hub-link-errata.md`](hub-link-errata.md), next to this file,
lists each change (HL-1 to HL-24), the sections it touched, and what each side must implement and test.

In this file "must" is a requirement on an implementation. "Should" is a recommendation that an implementation
may skip with a reason.

## Contents

1. Roles and words
2. Transport and the hub URL rule
3. Signing
4. Endpoints on the hub
5. The heartbeat body
6. The heartbeat reply: fleet, notices, sign-in users
7. Commands
8. The dashboard side
9. Hub sign-in (version 1, off by default)
10. What the hub must do
11. Failure modes
12. Test vectors
13. Changing this protocol

## 1. Roles and words

| Word | Meaning |
| --- | --- |
| Dashboard, server | One Just Dashboard install. It starts every exchange of the link. |
| Hub | One Just Hub install. It answers the link API at its own tailnet name, for example `https://hub.tailed39ba.ts.net`. |
| Hub owner, hub user | A person signed in to the hub. Version 1 hubs have one owner account. |
| Server administrator | A dashboard account with `system.admin`, in a browser session, or root at the terminal. |
| Identity | The dashboard's Ed25519 key pair for the hub link. Made on first use, sealed with `JD_MASTER_KEY`. |
| Hub key | The hub's Ed25519 key. Its public half is pinned on the dashboard at approval. It signs commands and sign-in tickets. Version 1 never rotates it: a new hub key needs a new link |
| Hub node | The tailnet machine that answers at the hub's name, identified by its Tailscale `StableNodeID`. The dashboard records it at connect and refuses to talk to any other machine at that name (section 2.4) |
| Hub session | A dashboard session made by a hub sign-in ticket (`auth_method = 'hub'`). It is shorter and narrower than a normal session (section 9.9) |
| `requestId` | The hub's id for a link request that is waiting for approval. |
| `serverId` | The hub's id for a linked server. A command names it, so a command for one server fails on another. |
| Scope | `observe`, `operate` or `maintain`. Chosen on the dashboard, stored there and enforced there. |
| Sequence | The persisted, strictly increasing counter every signed request carries. |
| Account link | The dashboard's existing link to just-dashboard.com (`JD_ACCOUNT_LINK`). It is not changed by this protocol. |
| Agent mode | The dashboard's older headless design with an mTLS "hub" principal (`httpx.HubOnly`, `httpx.KindHub`). It is unrelated to this protocol. |

**Naming rule for dashboard code.** New dashboard code for this protocol is named `hublink` / `HubLink` /
"hub link" (package names, types, module fields, frontend types, settings keys `hub.link.*`, the table
`hub_link_commands`). It never reuses or renames agent mode's identifiers, and no new identifier is the bare
word `Hub` where it could be read as agent mode's principal. Audit strings and URL paths may say `hub`.

## 2. Transport and the hub URL rule

### 2.1 Conventions

- Every link exchange is a request **from the dashboard to the hub**. The hub never opens a connection to a
  dashboard, with one exception: before pairing, the hub may probe `GET /api/v1/hub/hello` (section 8.1) to
  find dashboards. A sign-in ticket travels in the person's browser, not between the machines (section 9).
- Base path on the hub: `/api/hub/v1`. JSON in and out, UTF-8, `Content-Type: application/json`.
- HTTPS with TLS 1.2 or later, verified against the system roots. The hub's certificate is the Let's Encrypt
  certificate Tailscale issues for its MagicDNS name. The dashboard never follows a redirect on these routes,
  and the hub never sends one.
- The dashboard bounds every exchange at 15 seconds, plus the `wait` it asks for on the command long poll.
- Request bodies are at most **64 KiB**; the hub answers `413 too_large` above that. Responses are at most
  **256 KiB**; the dashboard treats a larger one as an error.
- Every request carries `X-JD-Link-Protocol: 1`. A hub that does not speak version 1 answers
  `400 unsupported_protocol`.
- The dashboard also sends `Accept: application/json` and `User-Agent: just-dashboard/<version> hub-link`.
- **Base64** is standard base64 with padding (RFC 4648 section 4), except in sign-in tickets, which use
  base64url without padding (RFC 4648 section 5) because they travel in a URL.
- **Ed25519 public keys** travel as base64 of the 32 raw key bytes. **Signatures** are base64 of the 64-byte
  Ed25519 signature over the UTF-8 bytes of the signed string. **Hashes** are lowercase hex SHA-256.
- **Times** on the wire are integer unix seconds. The dashboard's own local API (section 8) uses RFC 3339
  strings, as the account link's does.
- **Errors** use the dashboard's shape: `{"error":{"code":"…","message":"…"}}`. `code` is a stable
  `snake_case` word at most 64 characters long. `message` is one sentence for a person, at most 300 characters.
- **Strict responses.** A v1 hub never adds a field to a v1 response, so the dashboard decodes responses
  strictly: an unknown field, a missing required field or anything after the JSON value is an error, and the
  response is not used. New response fields need a new protocol version. One exception: each element of the
  heartbeat reply's `fleet`, `notices` and `signIn.users` is decoded on its own, so one bad element is dropped
  rather than failing the reply (section 6).
- **Lenient requests.** The hub ignores request-body fields it does not know, so the dashboard may add
  snapshot fields without a version change. The hub must not reject a heartbeat for an unknown field, nor for a
  known field of the wrong type or outside its rule: it drops that field as if it were absent and accepts the
  rest (section 5.3).

### 2.2 The hub URL rule

The dashboard links only to a hub on its own tailnet. The rule is applied in this order when a person or the
terminal names a hub, and the first failure is the answer.

1. **Read it as an origin.** The input is trimmed. A bare host (`hub.tailed39ba.ts.net`) is read as
   `https://` plus that host. The result must parse as a URL with a host, and have no user name or password,
   no path other than `/`, no query and no fragment. Failure: `hub_url_invalid`.
2. **HTTPS only.** The scheme must be `https`. Failure: `hub_url_invalid`. (The test origin in 2.3 is the only
   exception.)
3. **Spell it one way.** Scheme and host are lowercased, a trailing dot on the host is removed, and port 443
   is dropped. Any other port is kept. This is the stored hub URL, and every later comparison uses it.
   `accountlink.originOf` lowercases and drops port 443 but keeps a trailing dot, so the hub link's own wrapper
   removes the dot first and then calls it; the result must equal this step's spelling byte for byte.
4. **A name, not an address.** The host must not be an IP literal. Failure: `hub_url_invalid`.
5. **This server's MagicDNS suffix.** The dashboard reads it from the host's `tailscale status --json`, the
   same call `selfcfg.DetectTailscale` makes: `CurrentTailnet.MagicDNSSuffix`, else the top-level
   `MagicDNSSuffix`, else `Self.DNSName` with its first label and trailing dot removed. It must be non-empty,
   end in `.ts.net`, and `BackendState` must be `Running`. If any of that fails the link is refused with
   `hub_url_not_tailnet` and the message says this server is not on a tailnet with MagicDNS. For
   `vmi3690758` today the suffix is `tailed39ba.ts.net`.
6. **One machine on that tailnet.** The host must be exactly one label followed by `.` and the suffix. The
   label matches `^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`. `hub.tailed39ba.ts.net` and `hub-1.tailed39ba.ts.net`
   pass. `hub.example.com`, `a.hub.tailed39ba.ts.net` and `hub.other.ts.net` fail. Failure: `hub_url_not_tailnet`.
7. **Not this server's own name.** The host must not equal this server's own MagicDNS name (`Self.DNSName`
   without the trailing dot). A hub on the same name, on any port, would receive this dashboard's
   `__Host-jd_session` cookie, because browsers send a host-only cookie to every port of that host. Failure:
   `hub_url_same_host`.
8. **The name belongs to that machine.** The dashboard takes the address it would dial (see "Reaching the
   hub" below) and asks tailscaled's LocalAPI (`tailnetgate.NewLocalAPI`) `GET /localapi/v0/whois?addr=<ip>`.
   The answer's node name, without its trailing dot, must equal the host. A different node, or no node
   (`ErrNotAPeer`), is refused with `hub_not_on_tailnet`. The answer's `Node.StableID`, its node name and its
   owner (the `UserProfile.LoginName`, or the node's tags when it is tagged) are the **hub node**: the
   dashboard shows them before Connect (8.2 `check`), keeps them with the request, and stores them in the link
   state when the link completes (2.4). If tailscaled cannot be asked (`ErrUnavailable`), the check is
   skipped, the skip is logged and recorded in the `hub.link.begin` audit detail, and the hub node is recorded
   later (2.4). A version 1 dashboard must run this check whenever tailscaled answers.

**When the rule runs.** At connect time, on the whole URL. Again each time the connector starts with a stored
link, before it sends anything:

- Steps 1 to 4 need no tailnet and always run: a stored URL that is not `https`, or whose host is an IP literal
  or a loopback name, and that is not the current test origin (2.3), fails. A link made at the test origin
  therefore stops at the first start after the variable is removed, on any machine.
- Steps 5 to 7 run when the suffix can be read. If it cannot be read at this start, they are skipped this time
  and the link runs.
- The hub node check of 2.4 runs before the first request.

A failure sets the link idle; it is not cleared. The Settings page says why (`hub_url_refused`), every hub
session is revoked in the same write transaction that sets the link idle (9.3), and the link stays idle until a
person disconnects.

**What the URL rule does not cover.** The `approveUrl` and notice links the hub returns must be on the stored
hub URL's origin (sections 4.1 and 6.2). Fleet endpoints have their own rule (section 6.1).

**Reaching the hub.** The backend runs with host networking, but whether it can resolve a MagicDNS name
depends on how the host's resolver is set up. So the address the dashboard dials is chosen this way:

1. The system resolver's answer for the host, if it gives an address in `100.64.0.0/10`.
2. Otherwise the tailnet IPv4 address that `tailscale status --json` lists for the peer whose `DNSName`, without
   its trailing dot, equals the host. The dashboard may cache this for a minute and must look again after a
   failed dial.
3. Otherwise the hub is unreachable (`hub_unreachable`, or a heartbeat error).

In every case TLS verifies the certificate for the host name, and the request's `Host` is the host name. The
dashboard never dials an address outside `100.64.0.0/10` for a hub, so link traffic stays on the tailnet. A hub
at the test origin (2.3) is dialled as written.

### 2.3 The test origin (tests only, never production)

Local end-to-end tests run a hub on `http://127.0.0.1:<port>` with no tailnet. For them, and nothing else:

- `JD_HUB_LINK_TEST_ORIGIN` names **one exact origin**, for example `http://127.0.0.1:43180`. It is read once at
  start. Its host must be a loopback host (`localhost`, `127.0.0.0/8` or `[::1]`), its scheme `http` or
  `https`, and it must pass step 1 and step 3. Any other value refuses to start, like a bad `JD_ACCOUNT_URL`.
- A hub URL that equals the test origin after step 3 is accepted without steps 2 and 4 to 8. Every other URL
  still goes through the whole rule. The test origin widens nothing else: signing, scopes and commands are
  unchanged.
- It is set by root only. It is not in `.env.example`, not in `docker-compose.yml`'s `environment:` map and not
  in the configuration guide's table, so a Compose install cannot pick it up from `.env`. Setting it takes a
  deliberate Compose override or a test harness that starts the binary.
- When it is set the backend logs a warning at start (`JD_HUB_LINK_TEST_ORIGIN is set; this is for tests and
  must never be used in production`), `GET /dashboard/hub` reports it as `admin.testOrigin`, and the Settings
  page shows a banner.
- With the stored hub URL at the test origin, exactly two rules relax, and nothing else: a notice `href` must be
  on the stored hub URL's origin whatever its scheme (6.2), and a fleet `endpoint` may be a loopback `http` or
  `https` origin as well as one that passes 6.1's MagicDNS rule (6.1). No WhoIs is asked and no hub node is
  recorded (2.4).
- `JD_HUB_SIGNIN=true` together with a test origin refuses to start, like a bad value, unless the binary was
  built with the Go build tag `hublinktest`. Release builds never carry that tag. The local end-to-end harness
  builds with it when it turns sign-in on.
- Removing the variable stops a link made at the test origin at the next start (2.2 "When the rule runs").

Its name contains `TEST` on purpose. It must never be documented as a way to reach a hub off the tailnet.

### 2.4 The hub's machine

The certificate proves only that the answering machine holds the hub's MagicDNS name. On a tailnet that name
belongs to whichever machine registers it: once the hub's machine is deleted, the next machine called `hub` gets
the name and can get a certificate for it. So the dashboard also pins the hub node.

- **Recorded at connect** (2.2 step 8) in the link state as `hubNode: {id, name, owner, tags}`: the
  `StableID`, the node name without its trailing dot, the owner's login (absent for a tagged node) and its tags.
- **Checked before sending.** The dashboard asks WhoIs for the address it is about to dial when the connector
  starts, whenever that address differs from the last one it checked, and at least every 5 minutes. With a
  recorded hub node:
  - The same `StableID`: the request is sent.
  - A different `StableID`: nothing is sent. The link goes idle with `hub_url_refused` and the message "the
    hub's machine changed", every hub session is revoked in the same write transaction that sets it idle
    (9.3), and `hub.link.node_changed` is audited by `system` with the old and new node. It stays idle until a
    person disconnects and connects again.
  - No node (`ErrNotAPeer`): nothing is sent. The exchange counts as failed (backoff) with the error "the hub's
    machine is not on this tailnet". The link does not go idle.
  - tailscaled cannot be asked (`ErrUnavailable`): the request is sent, and the skip is logged at most once an
    hour.
- **Recorded later.** A link stored without a hub node (tailscaled could not be asked at connect) records the
  first node a later check returns for the stored host, after the same node-name check as 2.2 step 8, and
  audits `hub.link.node_recorded`.
- **While a request is pending.** The approval answer (4.2) is where the hub key is pinned, so the check must
  also run before **every** request poll, by whichever process polls it (the running dashboard or the terminal
  command, 8.4), against the hub node kept with the request (`hub.link.request`, 8.5). A different `StableID`:
  nothing is sent, the request ends as failed (outcome `failed`, "the hub's machine changed"),
  `hub.link.node_changed` is audited, and no key, `serverId` or name is stored. No node (`ErrNotAPeer`): nothing
  is sent and the poll counts as failed. `ErrUnavailable`: the poll is sent. A request kept without a node
  records the first node a later check returns, after the node-name check of 2.2 step 8. The dashboard stores
  an approved answer only from a poll whose check passed or was skipped for `ErrUnavailable`, and the link
  state's `hubNode` is the request's node at that poll.
- **At the test origin** no WhoIs is asked and no hub node is recorded.

A hub restored from its own backup brings its node back (the backup holds the node's state), so the check does
not fire. A hub whose machine was made again from nothing is a new node and needs a new link, as a hub with a
new key does.

## 3. Signing

### 3.1 Request signing (dashboard to hub) — identical to the account link

Every request except `POST /requests` is signed and carries these headers:

```
X-JD-Link-Protocol: 1
X-JD-Server: <serverId, or the requestId while the link request is pending>
X-JD-Time: <unix seconds>              # within ±60 s of the hub's clock
X-JD-Sequence: <int64>                 # greater than the last one the hub accepted; persisted before sending
X-JD-Signature: <base64 Ed25519 signature by the dashboard's identity>
```

The signed string is five lines joined by `\n`, with no trailing newline:

```
<METHOD>
<path with query, exactly as sent, for example /api/hub/v1/commands?wait=25>
<hex sha256 of the raw body bytes, or of "" when there is no body>
<X-JD-Time>
<X-JD-Sequence>
```

This is `accountlink.requestMessage` and `signRequest`, unchanged. The path includes the `/api/hub/v1` prefix.
A signature made for the account link can never verify here, because its path starts `/api/link/v1`.

**The sequence.** One counter for the life of the hub link identity, kept in `settings['hub.link.sequence']`.
It is raised and read back in one statement that is committed before the request is built (`kv.nextSequence`),
so a number is on disk before it is on the wire and two processes (the dashboard and a terminal command) are
never handed the same one. The new value is `max(stored + 1, unix time in milliseconds)`, computed in that same
statement, so a database restored from an older copy (a VPS snapshot rolled back) continues above every number
it used before, as the licence protocol's sequence does. It is never reset by an unlink. It starts with the
request polls and continues into the linked server's heartbeats. It is a different counter from the account
link's. The hub checks only "greater than", so the vectors, which take the sequence as an input, are unchanged.

**What the hub checks, in this order.**

1. `X-JD-Link-Protocol` is `1`. Else `400 unsupported_protocol`.
2. `X-JD-Server` names something:
   - on `GET /requests/{requestId}`, a pending or decided link request whose id equals `{requestId}`;
   - on every other signed route, a linked server.
   A request id is never accepted on any other route. A server the owner removed answers `410 server_removed`.
   Anything else is `401 unknown_server`.
3. `X-JD-Time` is an integer within 60 seconds of the hub's clock, either way. Else `401 stale_time`.
4. `X-JD-Signature` verifies against the public key stored for that request or server. Missing or malformed
   headers are also this error. Else `401 bad_signature`.
5. `X-JD-Sequence` is greater than the stored `last_sequence`, and the hub stores it in the same statement
   that compares it, so two requests with one number cannot both pass. Else `401 replayed_sequence`.

**Nothing about the named request or server changes before step 5 passes.** `X-JD-Server` is not signed, and
every linked server learns every other server's id from `fleet`. Before step 5 the hub may spend only a budget
keyed on the caller's tailnet address. Only after it: the per-server rate budgets (4.3), poll replacement (4.4),
last-seen, the node recorded from WhoIs, activity about the server, and the call's own effect. A request that
fails a step leaves the named row exactly as it was.

When an approval is collected (4.2), the new server row starts with the request's `last_sequence`.

### 3.2 Command signing (hub to dashboard) — identical to the account link

Each command carries `signature`, base64 Ed25519 by the hub key over five lines joined by `\n`, no trailing
newline:

```
<serverId>
<commandId>
<kind>
<hex sha256 of the canonical JSON of args>
<expiresAt>
```

This is `accountlink.commandMessage`, unchanged. The `serverId` is the receiving server's id, so a command
signed for another server does not verify.

### 3.3 Canonical JSON — identical to the account link

Object keys sorted by Unicode code point (a byte sort of UTF-8 is the same order), no whitespace, strings as
JSON string literals with only `"`, `\` and control characters escaped (`\b \f \n \r \t`, others as `\u00XX`
in lowercase hex), no HTML escaping, integers in plain decimal, `true` and `false`, nested objects canonical
too. An empty object is `{}`.

The arguments may only hold objects, strings, integers and booleans. An array, `null`, a fraction, an exponent,
`1.0`, an integer outside ±(2^53 − 1), a non-object at the top, trailing content and nesting deeper than five
objects are refused before canonicalisation (`accountlink.decodeArgs`). This keeps every implementation's
bytes identical.

### 3.4 Key fingerprint — identical to the account link

The first 16 hex characters of SHA-256 over the 32 raw public-key bytes, in four groups of four separated by
`:`. The dashboard and the hub both show it while pairing, so a person can compare them. The server key in the
vectors has the fingerprint `21fe:31df:a154:a261`.

### 3.5 What is identical and what differs

| Part | Identical to the account link | Different for the hub link |
| --- | --- | --- |
| Request signing | Headers, the five-line string, ±60 s, the sequence rule, the hub's check order | The base path inside the signed path: `/api/hub/v1` |
| Command signing | The five-line string, canonical JSON, the argument value set, ULID ids, the one-hour ceiling on `expiresAt`, the replay table | The signer is the hub key, and the table is `hub_link_commands` |
| Fingerprint | Algorithm and format | It is the hub link identity's fingerprint, not the account link's |
| Identity | Ed25519, made on first use, sealed with `JD_MASTER_KEY`, kept across unlinks | A **separate key**, in `settings['hub.link.identity']`. The site and the hub never see the same key, and one link's key or counter never affects the other |
| Where it points | An `https` origin, no redirects | Must pass the tailnet rule in 2.2. There is no environment variable that names the hub; the URL is chosen at connect time and stored in the link state |
| Discovery | — | The account link starts with `GET /site`. The hub link has no such call: the protocol header on `POST /requests` is the version check, and the hub finds dashboards with `GET /api/v1/hub/hello` |
| Approval | A short code approved by a person, signed in | The code typed into the hub by its owner, **or** a one-time token from the hub (section 4.1). The approval is collected by the first signed approved poll (4.2) |
| Pending request poll | Shape and status codes | An approved answer repeats on every poll until the collection window ends (4.2); the account link hands it out once |
| Response field names | `siteKey`, `siteKeys`, `owner.label` keep their names so one decoder reads both | Here "site key" means the hub key, and `owner.label` is the hub's name |
| Key rotation | `siteKeys` in a heartbeat reply adds a successor key and retires old ones | None in version 1. A v1 hub never sends `siteKeys`; a dashboard decodes it if present and never applies it (4.3). A new hub key needs a new link |
| The peer's machine | Web PKI on just-dashboard.com | The hub node is pinned by `StableNodeID` (2.4) |
| Heartbeat body | The snapshot, field for field (section 5) | Adds one optional top-level key, `signIn`, only when hub sign-in is switched on (section 9.3). The hub validates it field by field (5.3) |
| Heartbeat reply | `ok`, `heartbeatSeconds`, `pendingCommands`, `siteKeys` | Adds `fleet` and `notices` (always), and `signIn` (optional). `siteKeys` is never sent |
| Heartbeat interval | Clamped to 10–600 s | The hub's default is 15 s, not 30. The dashboard paces itself to 12 in any 61 s, not 4; the hub allows 15 per server in any 60 s |
| Sequence | Raised by one | `max(stored + 1, unix milliseconds)` (3.1) |
| Key ids | `kid` matches `^[0-9A-Za-z][0-9A-Za-z._-]{0,63}$` | No dot: `^[0-9A-Za-z][0-9A-Za-z_-]{0,63}$`, so a sign-in ticket splits on `.` without ambiguity |
| Storage | `account.link.*` settings, `account_link_commands` | `hub.link.*` settings, `hub_link_commands` (section 8.5) |
| Switch | `JD_ACCOUNT_LINK` | `JD_HUB_LINK`, default on (section 8.6) |
| Audit actor | `account:<owner label>` | `hub:<hub name>` (section 8.7) |

## 4. Endpoints on the hub

| Method and path | Auth | Purpose |
| --- | --- | --- |
| `POST /api/hub/v1/requests` | none; 10 a minute per source address; at most 3 pending per tailnet node and 50 in all | Start a link request (4.1) |
| `GET /api/hub/v1/requests/{requestId}` | signed, `X-JD-Server: <requestId>` | Poll it (4.2) |
| `POST /api/hub/v1/heartbeat` | signed | Send the snapshot; get the fleet, notices and command count (4.3) |
| `GET /api/hub/v1/commands?wait=25` | signed | Long poll for signed commands (4.4) |
| `POST /api/hub/v1/commands/{commandId}/result` | signed | Report how a command ended (4.5) |
| `POST /api/hub/v1/unlink` | signed, empty body | The server leaves (4.6) |

There are no other link routes. Sign-in adds no hub route: its data rides on the heartbeat (section 9.3).

### 4.1 `POST /requests`

Unsigned. The dashboard sends:

```json
{
  "publicKey": "11qYAYKxCrfVS/7TyWQHOg7hcvPapiMlrwIaaPcHURo=",
  "scope": "maintain",
  "server": {
    "name": "vps-07749119-vps-ovh-net",
    "hostname": "vps-07749119-vps-ovh-net",
    "version": "0.8.5",
    "os": "Ubuntu 24.04",
    "arch": "amd64",
    "endpoint": "https://vps-07749119-vps-ovh-net.tailed39ba.ts.net:8443"
  },
  "token": "jht_AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8"
}
```

| Field | Rule |
| --- | --- |
| `publicKey` | Base64 of the 32-byte Ed25519 public key of the dashboard's hub link identity |
| `scope` | `observe`, `operate` or `maintain` |
| `server.name`, `server.hostname`, `server.os` | The text rule of 5.3, 1–64 characters |
| `server.version` | The version rule of 5.3: `^[0-9A-Za-z.+-]{1,32}$`. The hub may show it; it is never needed for anything else |
| `server.arch` | The arch rule of 5.3: `^[0-9A-Za-z_-]{1,16}$` |
| `server.endpoint` | Optional. This dashboard's own address, as `access.endpoint` in the snapshot (section 5). Present when the dashboard knows it. At most 255 characters |
| `token` | Optional. A one-time token from the hub. `^jht_[A-Za-z0-9_-]{43}$` |

The dashboard cuts each text to its limit (`fitText`) and sends `unknown` for an empty one, as the account link
does. Everything except `token` and `server.endpoint` is the account link's request body.

`server` is self-reported, and `server.name` and `server.hostname` become the new server's name on the hub,
which `fleet` relays to every other server until its first heartbeat. So the hub checks each `server` field
against its rule above and never refuses the request for one: a `name`, `hostname`, `os`, `version` or `arch`
that is missing or fails is replaced by `unknown`. An `endpoint` that is not an `https` origin of at most 255
characters (5.3's endpoint rule without its host check) is dropped; one whose host is not the WhoIs node's name
is kept and flagged (`endpoint_not_node`). The request's `endpoint` is only ever shown on the approval screen;
the server's endpoint comes from its heartbeats alone (5.3). Nothing from `server` is stored, shown or relayed
without passing its rule.

The hub answers `200`:

```json
{
  "requestId": "2c9e41d7-0000-4000-8000-000000000001",
  "userCode": "K7QM-29XD",
  "approveUrl": "https://hub.tailed39ba.ts.net/servers/add/?request=2c9e41d7-0000-4000-8000-000000000001",
  "expiresAt": 1761351300,
  "pollSeconds": 2
}
```

| Field | Rule (the dashboard refuses the answer otherwise) |
| --- | --- |
| `requestId` | `^[0-9A-Za-z][0-9A-Za-z-]{7,63}$`. Just Hub uses a lowercase UUID |
| `userCode` | `^[A-HJ-NP-Z2-9]{4}-[A-HJ-NP-Z2-9]{4}$`: eight characters from `ABCDEFGHJKLMNPQRSTUVWXYZ23456789`, shown as `ABCD-2345` |
| `approveUrl` | An absolute URL on the stored hub URL's origin, compared as in 2.2 step 3 |
| `expiresAt` | In the future and at most one hour away. Just Hub uses 15 minutes |
| `pollSeconds` | The dashboard clamps it to 2–30 |

The request is bound to `publicKey`. The hub's approval screen leads with what WhoIs says about the source
address (the tailnet node and its owner's login, or its tags), then the key fingerprint, the scope and what the
server says about itself (name, hostname, version, OS, endpoint). Everything in `server` is self-reported, so
the screen flags a request whose `server.endpoint` host is not the WhoIs node's name, and a pending request
whose name another pending request also uses. **Every approval made in the hub requires the owner to type the
user code the dashboard shows** (its Settings page, or the terminal), a token request the hub could not approve
at once included; only a valid token's own approval at `POST /requests` needs no code. The hub compares the
typed code with the request's code (case, spaces and the hyphen ignored) and refuses a mismatch. Five wrong codes deny the request. The hub never
shows a pending request's code, so the code can only come from the server's own screen; this is what stops an
owner approving a look-alike request picked from the pending list. The dashboard shows the code and the
fingerprint, and the hub's machine and owner (8.2), on its Settings page.

**Pending requests.** At most 3 requests per tailnet node (by WhoIs `StableID`, else the source address) are
pending at once; a fourth from the same node expires the oldest. At most 50 are pending in all; beyond that the
answer is `429 rate_limited` and no request is created. A request approved at once by a token does not count.

**The one-time token.** The hub's "Add by command" screen and the hub installer make tokens for the terminal
path (`manage.sh hub connect … --token-stdin`, section 8.4). Only the terminal path sends a token. The Settings
page never does: there the hub owner approves the code.

- A token is `jht_` followed by 43 base64url characters (32 random bytes). The hub stores only its SHA-256.
- It never appears on a command line. The hub shows the command and the token separately, and `--token-stdin`
  asks for the token with echo off (8.4).
- It is single use: the first `POST /requests` that names it consumes it, whatever happens next, a refusal
  for the wrong node included.
- It expires at most one hour after it is made. Just Hub's own screen makes one that lasts 15 minutes unless
  the owner picks another length; the installer makes one that lasts 30 minutes.
- It may be bound to one tailnet machine. A bound token presented from another node (by WhoIs of the source
  address) is consumed and answered `403 bad_token`, and no request is created. The hub's `local` token, the
  one that makes "This server", is always bound to the hub's own host (`HUB_LOCAL_NODE`).
- An unknown, used or expired token is answered `403 bad_token`, and **no request is created**. The dashboard
  reports this as a failure. It does not fall back to a code.
- A valid token means the hub owner has approved in advance. The hub creates the request already approved,
  unless it cannot approve yet (for example while it waits for a seat from its licence). Then the request is
  pending and is approved in the hub like any other, with the typed code. The dashboard cannot tell the two
  apart and does not need to: it polls as usual.
- The token never replaces the dashboard's own approval. The terminal path is root on that server, and the
  browser path is a server administrator pressing Connect.

Errors: `400 unsupported_protocol`, `400 bad_request` (the body is not a JSON object, or `publicKey`, `scope` or
`token` breaks its rule), `403 bad_token`, `413 too_large`, `429 rate_limited` (the address's budget, or 50
pending requests).

The hub accepts `POST /requests` only from an address WhoIs knows (it listens only on the tailnet).

**Asking again with a known key.** A public key that already belongs to a linked server may ask again, for
example after the dashboard lost its link state. `POST /requests` is unsigned, so anyone who has seen that key
can open such a request; only the key holder can collect its approval (4.2). Approving it changes nothing about
the live server. When the approval is collected, the hub ends the old server row in the same transaction that
creates the new one, and the old row's requests then get `410 server_removed`. The hub treats approving such a
request as a destructive action, and flags it when it comes from another tailnet node than the server it
replaces.

### 4.2 `GET /requests/{requestId}`

Signed with `X-JD-Server: <requestId>`. Polled every `pollSeconds` until it is decided or the request expires.
Only the process that started the request polls it (section 8.9), and it runs the hub node check of 2.4 before
every poll; an approved answer is stored only from a poll whose check passed (or was skipped because tailscaled
could not be asked).

- `200 {"status":"pending"}` until it is decided.
- Once approved, on every poll until the collection window ends (below):

  ```json
  {
    "status": "approved",
    "serverId": "7a0d3c1e-0000-4000-8000-000000000002",
    "siteKey": { "kid": "hub-2026-11", "publicKey": "PUAXw+hDiVqStwqnTRt+vJyYLM8uxJaMwM1V8Sr0Zgw=" },
    "heartbeatSeconds": 15,
    "pollWaitSeconds": 25,
    "owner": { "label": "hub" }
  }
  ```

  | Field | Rule (the dashboard refuses the approval otherwise, and the request ends as failed) |
  | --- | --- |
  | `serverId` | `^[0-9A-Za-z][0-9A-Za-z-]{7,63}$` |
  | `siteKey.kid` | The hub key's id: `^[0-9A-Za-z][0-9A-Za-z_-]{0,63}$` (no dot) |
  | `siteKey.publicKey` | Base64 of a 32-byte Ed25519 key. The dashboard pins it |
  | `heartbeatSeconds` | 10–600. Just Hub sends its configured interval, 15 by default. The dashboard clamps it |
  | `pollWaitSeconds` | 1–25. The hub sends 25. The dashboard clamps it |
  | `owner.label` | The hub's name: `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`. The installer's default is `hub`. It becomes the audit actor `hub:<label>` |

  **Collection.** The first approved answer collects the approval. Only the key holder can make this signed
  poll, so collection, not approval, is where the hub acts on the key: in one transaction it creates the server
  row (its `last_sequence` is the request's), ends any live server row with the same public key (4.1), and
  marks the request `consumed`. Approval itself creates no server row and ends none. Every later poll of the
  same request answers the same body, with the same `serverId`, until 10 minutes after the request's
  `expiresAt`, so an answer lost in transit heals on the next poll. An approval nobody collects by then
  expires: the request becomes `expired`, and what its approval held is released (a seat claimed for its key,
  unless a live server holds that key).

  The dashboard stores the link in one transaction, switches `X-JD-Server` to `serverId`, stops polling the
  request and keeps its sequence going. A dashboard that already stored the link ignores a repeated answer.
- `410` with code `request_expired`, `request_denied` or `request_consumed` once it is over. `request_consumed`
  means the collection window is over; if the link is stored on the dashboard it is linked, and if not, the
  request has failed and a new one is needed. This is the account link's handling, unchanged.
- `401 unknown_server` when the hub no longer knows the request. The dashboard ends the request as failed.

### 4.3 `POST /heartbeat`

Signed. The body is the snapshot (section 5), plus `signIn` when hub sign-in is on (section 9.3). The dashboard
sends one every `heartbeatSeconds`, right after a command finishes, after `snapshot.now`, after a scope change
and when a person presses Sync.

The hub answers `200`:

```json
{
  "ok": true,
  "heartbeatSeconds": 15,
  "pendingCommands": 0,
  "fleet": [
    {
      "serverId": "7a0d3c1e-0000-4000-8000-000000000002",
      "name": "vps-07749119-vps-ovh-net",
      "endpoint": "https://vps-07749119-vps-ovh-net.tailed39ba.ts.net:8443",
      "state": "online",
      "version": "0.8.5",
      "self": true,
      "lastSeenAt": 1761350400
    },
    {
      "serverId": "5b1f9a20-0000-4000-8000-000000000001",
      "name": "vmi3690758",
      "endpoint": "https://vmi3690758.tailed39ba.ts.net:8443",
      "state": "late",
      "version": "0.8.5",
      "self": false,
      "lastSeenAt": 1761350340
    }
  ],
  "notices": [
    {
      "id": "update-0.8.6",
      "level": "info",
      "title": "Update ready on 1 server",
      "body": "0.8.6 is out. vmi3690758 can install it from Just Hub.",
      "href": "https://hub.tailed39ba.ts.net/updates",
      "at": 1761350280
    }
  ]
}
```

| Field | Rule |
| --- | --- |
| `ok` | Always `true` on `200` |
| `heartbeatSeconds` | 10–600. A change takes effect from the next heartbeat. The dashboard clamps it |
| `pendingCommands` | Commands queued for this server and not yet delivered. A hint only; the long poll is what delivers them |
| `siteKeys` | **Never sent by a v1 hub.** The field keeps its place in the account link's shape (`[{"kid", "publicKey", "notAfter"}]`) only so one decoder reads both. A dashboard that receives it on the hub link decodes it, so strict decoding still passes, and then ignores it: it adds no key, changes no pinned key's `notAfter` and drops none, and logs a warning once per link. The reply is authenticated by TLS alone, and on a tailnet a machine name is a weak anchor for a key that can run commands (2.4). A new hub key needs a new link |
| `fleet` | Always present, possibly `[]`. Section 6.1 |
| `notices` | Always present, possibly `[]`. Section 6.2 |
| `signIn` | Optional. Present only when the hub offers hub sign-in. Section 6.3 |

The dashboard caches `fleet`, `notices` and `signIn` with the time it received them (section 8.5) and serves
them to its own pages. Nothing in the reply can change anything on the dashboard except the heartbeat interval,
the cached view, and the end of grants whose hub user the reply no longer offers (9.3).

**The body.** The hub decodes it field by field (5.3). An unknown field, a field of the wrong type, or a value
outside its rule is dropped as if absent, and the heartbeat is still accepted.

**Rate.** The hub accepts at most 15 heartbeats per server in any 60 seconds, counted only after the signature
and sequence pass (3.1), and answers `429 rate_limited` beyond that. The dashboard paces itself to 12 in any 61
seconds at send time (the account link paces to 4); the hub's three extra absorb heartbeats delayed in transit
and the bursts after commands and Sync. A manual Sync that would break the dashboard's pace is skipped and the
status is returned as it was.

**Size.** The hub keeps the encoded reply at most 240 KiB (6.1), under the dashboard's 256 KiB limit.

Errors: the signed-request errors of 3.1, `400 bad_request` (the body is not a JSON object, or `protocol` is not
the integer `1`), `413 too_large`, `429 rate_limited`, `410 server_removed`.

### 4.4 `GET /commands?wait=25`

Signed. A long poll. `wait` is an integer from 1 to 25; the dashboard sends `pollWaitSeconds`. Anything else is
`400 bad_request`.

- `200 {"commands":[Command, …]}` as soon as at least one command is queued, with every queued command for
  this server up to 20.
- `200 {"commands":[]}` after `wait` seconds with nothing to send.
- One poll per server at a time. A newer poll replaces an older one, which answers `{"commands":[]}` at once
  without claiming anything. Only a poll that passed verification (3.1) replaces anything: an unverified poll
  that names a server touches nothing of it.
- A returned command moves to `sent` on the hub and is never returned again.

A command:

```json
{
  "id": "01JB0000000000000000000011",
  "kind": "container.restart",
  "args": { "container": "shop-web" },
  "issuedBy": "owner",
  "issuedAt": 1761350400,
  "expiresAt": 1761351000,
  "kid": "hub-2026-11",
  "signature": "6F9aRT0gyp09zPXKhMteGbeG5YlFP6JeR8T6pDHFliPU2H9k6ZH4b6jwTr9PJo33A8aO/5P/Uo5+GY6LGqPIBQ=="
}
```

| Field | Rule |
| --- | --- |
| `id` | A ULID: `^[0-9A-HJKMNP-TV-Z]{26}$`. Unique for the hub's whole life |
| `kind` | One of section 7's kinds, at most 64 characters |
| `args` | An object in the value set of 3.3 |
| `issuedBy` | The hub user who asked, or `rollout` for a step of an update rollout. At most 254 characters. Informational: it is not signed, and the dashboard records it but trusts only the hub key |
| `issuedAt` | Unix seconds |
| `expiresAt` | Unix seconds. Just Hub sets 10 minutes after `issuedAt`. The dashboard refuses one more than an hour away |
| `kid` | The id of the hub key that signed it |
| `signature` | Section 3.2 |

A command for a server that does not poll stays queued until `expiresAt` and then expires on the hub unsent.

### 4.5 `POST /commands/{commandId}/result`

Signed. The dashboard sends:

```json
{ "status": "succeeded", "startedAt": 1761350401, "finishedAt": 1761350403, "output": "shop-web restarted" }
```

| Field | Rule |
| --- | --- |
| `status` | `succeeded`, `failed` or `refused` |
| `startedAt`, `finishedAt` | Unix seconds |
| `output` | Optional. The dashboard sends at most 8 KiB; the hub accepts up to 64 KiB |
| `error` | Optional. The dashboard sends at most 1000 characters; the hub accepts up to 4 KiB |

The hub answers `204`. A result for a command that is not this server's, or that already has one, is
`404 unknown_command`. A delivered command with no result five minutes after `expiresAt` is marked expired on
the hub; a result that arrives later is still recorded.

### 4.6 `POST /unlink`

Signed, empty body. The hub marks the server removed, frees its seat and answers `204`. Every later signed
request from that server gets `410 server_removed`. The dashboard clears its link state either way (section 8.9).

### 4.7 Status codes

| Status | `code` | Where | Meaning, and what the dashboard does |
| --- | --- | --- | --- |
| `200` / `204` | | all | Success |
| `400` | `unsupported_protocol` | all | The hub does not speak version 1. Connect fails with `hub_unreachable` and says to update |
| `400` | `bad_request` | all | A field breaks its rule. Not retried until something changes |
| `401` | `unknown_server` | signed | The id is not known. While pending: the request ends as failed. While linked: shown as "the hub does not accept this server's signature"; the link is not cleared |
| `401` | `stale_time` | signed | Clocks differ by more than 60 s. Shown as "correct the time on this server"; backoff |
| `401` | `bad_signature` | signed | Shown as for `unknown_server`; backoff |
| `401` | `replayed_sequence` | signed | Two of this process's own requests crossed. Resent at once with a new number, up to 3 times |
| `403` | `bad_token` | `POST /requests` | The token is unknown, used or expired, or was presented from a machine it is not bound to. Connect fails |
| `404` | `unknown_command` | result | Stop reporting this result |
| `404` | `not_found` | all | No such route. Treated as an unreachable hub |
| `410` | `request_expired`, `request_denied`, `request_consumed` | request poll | The request is over (4.2) |
| `410` | `server_removed` | signed | The hub removed this server. The dashboard clears its link (8.9) |
| `413` | `too_large` | all | The body is over 64 KiB. The dashboard never builds one (section 5) |
| `429` | `rate_limited` | all | Backoff. On `POST /requests` it can also mean the hub holds 50 pending requests; Connect fails and says to try again later |
| `5xx` | any | all | Backoff |

### 4.8 Intervals, retries and backoff

These are the dashboard's rules. They are the account link's code (`service.go`, `link.go`, `commands.go`) with
the hub's numbers.

| Exchange | Rule |
| --- | --- |
| Request poll | Every `pollSeconds` (2–30) while pending, by the process that owns the request only. A local expiry by the dashboard's clock ends it too |
| Heartbeat | Every `heartbeatSeconds` (default 15, clamped to 10–600). On failure, back off: 1 s, doubling to at most 5 minutes, each wait multiplied by a random factor from 0.8 to 1.2. A success resets it |
| Command poll | Held for `wait` seconds plus 15. On failure, the same backoff as the heartbeat, kept separately. An empty answer that came back in under a second is followed by a one-second pause |
| Command result | Up to 3 attempts, 1 s then 2 s apart. Stops on `404 unknown_command` or `410` |
| `replayed_sequence` | Resent at once with a new sequence number, up to 3 times. Not counted as a failure |
| Unlink | One attempt, 15 s. The local link is cleared whether or not it succeeded |
| Pacing | At most 12 heartbeats in any 61 seconds, on the real clock |
| A restarted dashboard | A command whose record is still `running` is closed as `failed` and reported once, with the reason "the dashboard restarted before this command's result was sent" |

The hub never retries anything toward a dashboard: it never calls one, except the hello probe.

**When the hub is down.** Every dashboard feature keeps working. The heartbeat and poll back off to 5 minutes,
the Settings page shows the last error, `/auth/session` reports `reachable: false`, and the server switcher
says "Hub unreachable". Nothing on the dashboard depends on the hub.

## 5. The heartbeat body

The heartbeat body is the account link's snapshot, field for field (`accountlink.Snapshot`, pinned by
`snapshot_test.go`), built by the same `Host.Collect` and cut by the same `Snapshot.Redact(scope)`. On the hub
link one optional top-level key is added: `signIn` (section 9.3).

Every section except `protocol`, `sentAt` and `server` may be absent when the dashboard cannot read it, which is
different from empty: `"alerts": []` says nothing is firing, and no `alerts` key says the dashboard does not
know. Optional fields are left out, never `null`.

### 5.1 Every field, and the scope that sends it

"observe" means every scope sends it. "operate" means `operate` and `maintain` send it and `observe` leaves it
out.

| Path | Type | Scope | Meaning |
| --- | --- | --- | --- |
| `protocol` | int | observe | Always `1` |
| `sentAt` | int | observe | Unix seconds when the body was built |
| `server.name` | string | observe | The host name |
| `server.version` | string | observe | The installed dashboard version |
| `server.latest` | string, optional | observe | The newest published version, when known |
| `server.updateAvailable` | bool | observe | A newer version exists |
| `server.updateBreaking` | bool | observe | The newer version's notes mark it breaking |
| `server.updateRun` | object, optional | observe | An update under way, or one that ended in the last 10 minutes |
| `server.updateRun.status` | string | observe | `pending`, `running`, `success` or `failed` |
| `server.updateRun.phase` | string, optional | observe | `queued`, `fetching`, `building`, `restarting` or `finished` |
| `server.updateRun.toVersion` | string, optional | observe | The version it updates to |
| `server.scope` | string | observe | The scope the dashboard enforces now |
| `server.uptimeSeconds` | int | observe | Host uptime |
| `server.os` | string, optional | observe | For example `Ubuntu 24.04` |
| `server.kernel` | string, optional | observe | Kernel version |
| `server.arch` | string, optional | observe | For example `amd64` |
| `server.updatePolicy` | object, optional | observe | The automatic-update policy this link set; absent means off |
| `server.updatePolicy.auto` | string | observe | `patch` or `all` (`off` is sent as absence) |
| `server.updatePolicy.window.start`, `.end` | string | observe | `HH:MM`, server local time |
| `access.kind` | string | observe | `tailscale`, or `tunnel` when `JD_SITE` is empty, `localhost` or `127.0.0.1` |
| `access.endpoint` | string, optional | observe | The address a browser opens this dashboard at. The hub's Open button uses it |
| `access.port` | int, optional | observe | The dashboard's port |
| `access.tls` | string, optional | observe | The TLS mode, for example `tailscale` |
| `access.tailscale` | object, optional | observe | Only when `access.kind` is `tailscale` |
| `access.tailscale.hostname` | string, optional | observe | This server's MagicDNS name |
| `access.tailscale.ip4` | string, optional | observe | This server's tailnet IPv4 address |
| `access.tailscale.running` | bool | observe | tailscaled is up |
| `access.sshHost` | string, optional | observe | Defined, but never sent today |
| `metrics.cpuPercent` | number | observe | 0–100 |
| `metrics.load1` | number | observe | One-minute load |
| `metrics.memPercent` | number | observe | 0–100 |
| `metrics.memUsed`, `metrics.memTotal` | int | observe | Bytes |
| `metrics.swapPercent` | number | observe | 0–100 |
| `metrics.diskPercent` | number | observe | The fullest mount, 0–100 |
| `metrics.diskMount` | string, optional | observe | That mount point |
| `metrics.netRx`, `metrics.netTx` | number | observe | Bytes per second |
| `metrics.cpuSteal` | number | observe | 0–100 |
| `metrics.psiCpu` | number | observe | CPU pressure, 0–100 |
| `containers.running`, `.stopped`, `.unhealthy` | int | observe | Counts over every container on the host |
| `containers.list` | array | operate | Containers, without the dashboard's own |
| `containers.list[].id` | string | operate | Short id |
| `containers.list[].name` | string | operate | Name |
| `containers.list[].image` | string | operate | Image reference |
| `containers.list[].state` | string | operate | Docker state |
| `containers.list[].health` | string, optional | operate | Health status |
| `stacks` | array | operate | Compose stacks, without the dashboard's own |
| `stacks[].name` | string | operate | Project name |
| `stacks[].services`, `stacks[].running` | int | operate | Services, and how many run |
| `stacks[].managed` | bool | operate | A stack whose compose file is on disk |
| `alerts` | array | observe | Alerts firing now, not muted |
| `alerts[].kind` | string | observe | For example `cpu_high`, `memory_high`, `disk_filling`, `container_down`, `backup_failed` |
| `alerts[].subject` | string, optional | observe, see below | What the alert is about |
| `alerts[].reading` | string, optional | observe, see below | A number only: `95%`, `91.2%` or `exit 137` |
| `alerts[].since` | int | observe | Unix seconds it started firing |
| `backups.jobs` | int | observe | Backup jobs on this server (imported ones are not counted) |
| `backups.failed` | int | observe | Jobs whose last run failed |
| `backups.lastRun.status`, `.at` | string, int | observe | The newest run |
| `backups.list` | array | operate | Jobs |
| `backups.list[].id` | int | operate | Job id, the `jobId` of `backup.run` |
| `backups.list[].name` | string | operate | Job name |
| `backups.list[].lastStatus`, `.lastAt` | string, int, optional | operate | Its last run |
| `deployments.projects` | int | observe | Projects that are not archived |
| `deployments.lastRun.status`, `.at` | string, int | observe | The newest run |
| `deployments.lastRun.project` | string | operate | Its project's name |
| `signIn` | object, optional | hub link only, every scope | Section 9.3. Only when `JD_HUB_SIGNIN=true` |

**Alerts below `operate`.** At `observe`, an alert keeps its `subject` and `reading` only for `cpu_high`,
`memory_high` and `disk_filling`, which name a measurement or a mount point. Every other kind, including a
kind added later, is sent with `kind` and `since` only. At every scope a `reading` that is not
`^(exit )?[0-9]{1,3}(\.[0-9]{1,2})?%?$` is dropped.

**Never sent, at any scope:** `.env` contents, `JD_ALLOWED_CIDRS`, dashboard accounts (except the opt-in grants
in `signIn`, section 9.3), API keys, file paths beyond mount points, logs, alert messages, connection strings,
deployment variables, backup credentials.

### 5.2 Size

The body is at most 64 KiB. The dashboard keeps the whole body, `signIn` included, under 63 KiB. Lists are cut to
200 containers, 100 stacks, 100 backup jobs and 50 alerts. If it is still too large, the container list, then the
job list, then the stacks, then the alerts are halved until it fits. The counts always stay, and `signIn` is never
cut (at most 100 grants is a few KiB). The hub stores at most those numbers of each list.

### 5.3 What the hub accepts

The snapshot is written by one server and shown to the owner, put into email and webhook messages, and relayed
to every other linked server in `fleet` and `notices`. So the hub decodes it field by field into typed values
and keeps only what passes. It never stores, serves or relays the raw body.

1. The body must be a JSON object whose `protocol` is the integer `1`. Anything else is `400 bad_request`.
2. Each field is decoded into its type from 5.1 and checked against its rule below. A value of the wrong JSON
   type, or one that breaks its rule, is dropped as if absent. An unknown field is dropped.
3. A list is cut to the counts of 5.2. A list element that fails a rule for a field 5.1 does not mark optional
   is dropped whole; the rest of the list stays. An object other than the top level or `server` that lacks a
   required field after this is dropped whole.
4. `server` is never dropped. A required field of `server` that fails is replaced by the last good value the
   hub holds for that server (or `""`, `false`, `0`, or the scope approved at link time, before there is one),
   so the snapshot the hub serves always has the shape hub-api.md describes. A missing or failing `sentAt` is
   replaced by the hub's receive time.
5. The hub stores and serves the result re-encoded from the typed values, never the body as received. `fleet`,
   notices, alerts, activity and notification messages are built only from these validated values.

**Text rule.** Valid UTF-8, and no character in the Unicode general categories Cc (control: CR and LF among
them), Cf (format: the bidirectional controls among them), Zl or Zp.

| Field | Rule |
| --- | --- |
| `sentAt`, `server.uptimeSeconds`, `alerts[].since`, `backups.lastRun.at`, `backups.list[].lastAt`, `deployments.lastRun.at` | Integer, 0 to 2^53 − 1 |
| `server.name` | Text rule, 1–64 characters |
| `server.version`, `server.latest`, `server.updateRun.toVersion` | `^[0-9A-Za-z.+-]{1,32}$` |
| `server.updateAvailable`, `server.updateBreaking`, `access.tailscale.running`, `stacks[].managed` | Boolean |
| `server.updateRun.status` | `pending`, `running`, `success` or `failed` |
| `server.updateRun.phase` | `queued`, `fetching`, `building`, `restarting` or `finished` |
| `server.scope` | `observe`, `operate` or `maintain` |
| `server.os`, `server.kernel` | Text rule, 1–64 characters |
| `server.arch` | `^[0-9A-Za-z_-]{1,16}$` |
| `server.updatePolicy.auto` | `patch` or `all` |
| `server.updatePolicy.window.start`, `.end` | `^([01][0-9]\|2[0-3]):[0-5][0-9]$` |
| `access.kind` | `tailscale` or `tunnel` |
| `access.endpoint` | The endpoint rule below |
| `access.port` | Integer, 1 to 65535 |
| `access.tls` | `^[a-z0-9_-]{1,32}$` |
| `access.tailscale.hostname` | `^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$` |
| `access.tailscale.ip4` | A dotted IPv4 address in `100.64.0.0/10` |
| `access.sshHost` | Always dropped (never sent today) |
| `metrics.cpuPercent`, `.memPercent`, `.swapPercent`, `.diskPercent`, `.cpuSteal`, `.psiCpu` | A finite number, 0 to 100 |
| `metrics.load1`, `.netRx`, `.netTx` | A finite number, 0 or more |
| `metrics.memUsed`, `.memTotal`, `containers.running`, `.stopped`, `.unhealthy`, `stacks[].services`, `.running`, `backups.jobs`, `.failed`, `deployments.projects` | Integer, 0 to 2^53 − 1 |
| `metrics.diskMount`, `alerts[].subject`, `containers.list[].image` | Text rule, 1–255 characters |
| `containers.list[].id` | `^[0-9a-f]{1,64}$` |
| `containers.list[].name`, `stacks[].name` | `^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$` (the name rule of 7.1) |
| `containers.list[].state`, `.health`, `backups.lastRun.status`, `backups.list[].lastStatus`, `deployments.lastRun.status` | `^[a-z_-]{1,32}$` |
| `alerts[].kind` | `^[a-z0-9_]{1,64}$` |
| `alerts[].reading` | The reading rule of 5.1 |
| `backups.list[].id` | Integer, 1 to 2^53 − 1 |
| `backups.list[].name`, `deployments.lastRun.project` | Text rule, 1–128 characters |
| `signIn.grants[]` | At most 100. `userId` an integer of at least 1, `username` text rule 1–64 characters, `hubUser` the pattern of 6.3. An element that fails is dropped |

**Endpoint rule.** `access.endpoint` is kept only if it is an `https` origin (no user name or password, no path
other than `/`, no query, no fragment, at most 255 characters) whose host equals the node name WhoIs gives for
this heartbeat's source address, without its trailing dot, on any port. A hub in dev mode (no tailnet) accepts a
loopback `http` or `https` origin instead. Otherwise no endpoint is stored for that server from this heartbeat:
the hub does not keep an older one, and Open, the server's links and sign-in tickets are unavailable until a
heartbeat carries a good one. The same rule decides the approval screen's flag for `server.endpoint` in 4.1.

**Messages.** Server-supplied text goes into an email header only Q-encoded (`mime.QEncoding`) after the text
rule has removed CR and LF, and into webhook, Discord, Telegram and ntfy bodies only as a JSON string value or
the channel's own escaping.

## 6. The heartbeat reply: fleet, notices, sign-in users

These are what make a linked dashboard hub-aware: the server switcher, Servers in Ctrl K, the notice card and
the sign-in opt-in. The browser never talks to the hub; the dashboard's backend serves its cached copy.

**Decoding.** The reply object is decoded strictly at the top level (2.1): an unknown top-level field, a missing
`ok`, `heartbeatSeconds`, `pendingCommands`, `fleet` or `notices`, a `fleet` or `notices` that is not an array,
or a `signIn` that is not an object holding exactly an array `users`, fails the heartbeat. Each element of
`fleet`, `notices` and `signIn.users` is then decoded on its own (in Go: the array as `[]json.RawMessage`, then
each element strictly into its type) and checked against its rules. An element that fails, by a wrong type, an
unknown or missing field, or a rule, is dropped and the rest are kept, because these lists are for display and
one server's bad value must not stop every other server's heartbeat. Elements past a list's maximum are
dropped.

**Shown as the hub's.** Everything in these lists is the hub's text. The dashboard shows it as plain text
attributed to the hub (8.10), never as its own prompt.

### 6.1 `fleet`

At most 500 entries. Every server linked to the hub and not removed, including the one receiving the reply. A
server the hub has paused for its plan is listed like any other.

| Field | Type | Rule |
| --- | --- | --- |
| `serverId` | string | The hub's id for that server, `^[0-9A-Za-z][0-9A-Za-z-]{7,63}$` |
| `name` | string | The text rule of 5.3 (no Unicode Cc, Cf, Zl or Zp character), 1–64 characters. The name the hub shows |
| `endpoint` | string, optional | The server's last reported `access.endpoint`. Left out when that server is reached by an SSH tunnel or has not reported one |
| `state` | string | `online`, `late`, `offline` or `updating` (below) |
| `version` | string | On the `self` entry, the server's last reported `server.version`, or `""` before its first heartbeat. On every other entry always `""`: a dashboard does not need other servers' versions, and a rogue linked server should not learn them. At most 32 characters |
| `self` | bool | `true` on exactly one entry: the server receiving the reply |
| `lastSeenAt` | int, optional | Unix seconds of that server's last heartbeat |

**States.** With `h` the server's `heartbeatSeconds` and `age` the time since its last heartbeat:

| State | When |
| --- | --- |
| `online` | `age` is at most `max(45, 3h)` seconds |
| `late` | `age` is over that, up to `max(90, 6h)` seconds |
| `offline` | `age` is over `max(90, 6h)` seconds, or it has never sent one |
| `updating` | Its last snapshot had `server.updateRun.status` of `pending` or `running`, or the hub delivered a `dashboard.update.install` to it that has no result yet. `updating` stands in for `late` and `offline` for up to 10 minutes from the start of the update |

With the default 15 seconds, `late` starts at 45 s and `offline` at 90 s. A server whose plan pauses it on the
hub keeps its real state here: dashboards never learn that plans exist.

**Endpoint rule on the dashboard.** An `endpoint` must be an `https` origin with no path, query or fragment,
at most 255 characters, whose host is one label under the hub URL's MagicDNS suffix. When the hub URL is the
test origin, an endpoint passes if it is a loopback `http` or `https` origin (`localhost`, `127.0.0.0/8` or
`[::1]`), or if it passes the rule just given with this server's own MagicDNS suffix when that can be read.
Nothing else passes. An endpoint that fails is removed from its entry; the entry stays, without a link.

**What the hub shares.** By default `fleet` lists every live server, so the switcher works on every dashboard.
The hub's owner can turn off "Share the server list with linked servers"; then `fleet` holds only the `self`
entry, and `notices` hold only notices about the receiving server or the hub itself, never one that names
another server.

**Size.** The hub keeps the encoded reply at most 240 KiB. It orders `fleet` with `self` first and the rest by
name, and while the reply is too large it removes `fleet` entries from the end, never `self`. Only if `fleet`
then holds `self` alone and the reply is still too large does it remove notices from the end. With the field
limits above an entry is under 1 KiB, so only a large fleet with long names is ever cut.

### 6.2 `notices`

At most 20 entries, newest first. Short messages the hub wants every linked dashboard to show, such as an
update ready on some servers or a server offline.

| Field | Type | Rule |
| --- | --- | --- |
| `id` | string | `^[0-9A-Za-z][0-9A-Za-z._:-]{0,63}$`. Stable while the notice stands, so a dashboard can hide one a person dismissed |
| `level` | string | `info`, `warning` or `critical` |
| `title` | string | 1–120 characters, plain text |
| `body` | string | 0–1000 characters, plain text. The dashboard renders it as text, never as markup |
| `href` | string, optional | An absolute URL on the stored hub URL's origin: the same scheme, host and port, compared as in 2.2 step 3, at most 512 characters. With an `https` hub URL that means `https`; at the test origin it is the test origin's own scheme. Anything else is removed from the notice |
| `at` | int | Unix seconds the notice was raised |

A notice disappears from the dashboard when a later reply no longer carries it. The dashboard shows a notice
under the hub's name, and an `href` as a link that names its host (8.10).

### 6.3 `signIn` in the reply

Present only when the hub offers hub sign-in (its own setting is on). Absent means the hub does not offer it.

```json
{ "signIn": { "users": ["owner"] } }
```

`users` lists the hub user names a dashboard account may choose to let sign it in: at most 50, each
`^[a-z0-9][a-z0-9._-]{0,63}$`. Section 9 uses it.

A hub user name names one person for the hub's whole life: once a name has been used by a hub user, the hub
never gives it to any other hub user, after a rename or a deletion included (architecture 18.2). On the
dashboard, a grant whose `hubUser` the latest reply's `users` does not list, or every grant when the latest reply
carries no `signIn`, ends (9.3). So a rename ends the grants that name the old name, on every server, at its
next heartbeat.

## 7. Commands

### 7.1 Kinds

The account link's nine kinds, unchanged. Anything else is refused.

| Kind | `args` | Scope | On the dashboard |
| --- | --- | --- | --- |
| `snapshot.now` | `{}` | observe | Send a heartbeat now |
| `container.start` | `{"container": name}` | operate | The container page's start |
| `container.stop` | `{"container": name}` | operate | Its stop. Spends the destructive budget |
| `container.restart` | `{"container": name}` | operate | Its restart. Spends the destructive budget |
| `stack.restart` | `{"name": stack}` | operate | The stack page's restart. Spends the destructive budget |
| `backup.run` | `{"jobId": int}` | operate | Backups' Run now. Reported when the run has started. The run's trigger is recorded as `hub_link`, not `manual` |
| `dashboard.update.install` | `{"version": "0.8.6"}` | maintain | The self-update install. Spends the destructive budget. `refused` with `version_moved` in the error if that version is no longer the newest |
| `dashboard.update.policy` | `{"auto": "off" \| "patch" \| "all", "window": {"start": "03:00", "end": "05:00"}}` | maintain | Stored in `settings['hub.link.update-policy']` and evaluated every minute by the dashboard itself |
| `dashboard.restart` | `{"rebuild": false}` | maintain | Restart the dashboard's own stack. Spends the destructive budget |

**Values.** A container or stack name matches `^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`. `jobId` is an integer from 1
to 2^53 − 1. `version` matches `^[0-9]+(\.[0-9]+){1,2}$`. Window times match `^([01][0-9]|2[0-3]):[0-5][0-9]$`
in the server's local time; an end earlier than the start runs past midnight, the end is exclusive, and equal
times are refused unless `auto` is `off`. Each kind takes exactly the keys shown: no extra key and no missing
key.

**Scopes nest.** `operate` includes `observe`. `maintain` includes both.

**The dashboard's own containers.** `container.*` on any container of the dashboard's own Compose project, and
`stack.restart` on that project or another stack in the same directory, are refused with "… is one of the
dashboard's own containers; restart the dashboard itself with dashboard.restart, which needs the maintain
scope". The snapshot leaves those rows out of its lists, so the hub is not shown a row it would be refused on.

### 7.2 What the dashboard checks, in order

This is `Service.verify`, unchanged. The first failure is a `refused` result with the reason.

1. `id` is a ULID.
2. `kind` is 1–64 characters.
3. `kid` names a pinned hub key that is not past its `notAfter`.
4. `signature` is base64 of 64 bytes.
5. `args` decode in the value set of 3.3.
6. The signature verifies over this server's own `serverId`, `id`, `kind`, the hash of canonical `args` and
   `expiresAt`.
7. `expiresAt` is in the future.
8. `expiresAt` is at most one hour from now.
9. `id` has not been seen before. The record is written here, in `hub_link_commands`, before anything runs.
10. `kind` is one of section 7.1.
11. The scope held now includes the kind's scope.
12. `args` have exactly the kind's keys and values.

A command that fails before step 9 is not recorded, so a forged command cannot use up a real command's id. It
is still reported to the hub, which is waiting for a result. A second delivery of an id that is recorded is
audited and nothing else: it neither changes the record nor reports under that id.

After verification the command runs through the same `Host` methods the dashboard's own pages call, under a
10-minute limit. The destructive budget is spent under the key `hub:<hub name>`, separately from people and
from the account link. When it is exhausted the result is `failed` with "too many destructive actions".

### 7.3 Results

`succeeded` with the service's own message as `output`, `failed` with the error, or `refused` with the reason.
Output is cut at 8 KiB and errors at 1000 characters. Compose output never leaves the machine, because it can
carry values from a stack's environment. A refusal reads like:

```json
{ "status": "refused", "startedAt": 1761350400, "finishedAt": 1761350400,
  "error": "dashboard.update.install needs scope maintain; this server is linked at observe" }
```

After each result the dashboard sends a heartbeat, so the hub sees the effect.

## 8. The dashboard side

### 8.1 `GET /api/v1/hub/hello`

The hub's discovery probe. No sign-in, but inside `/api/v1`, so the network allowlist runs first (invariant 1),
and under the API rate limit per address. `Cache-Control: no-store`. It answers `200`:

```json
{"product":"just-dashboard","protocol":1,"hub":"available"}
```

| Field | Values |
| --- | --- |
| `product` | Always `just-dashboard` |
| `protocol` | The hub link protocol version this dashboard speaks: `1` |
| `hub` | `available` (no hub link and no request), `pending` (a request is waiting) or `linked` |

It never carries the version, the host name, the hub's URL or anything else. It is not mounted when
`JD_HUB_LINK=false` or in agent mode, so those answer `404` as an older dashboard does.

**How the hub uses it.** For each online Linux peer, `GET https://<peer MagicDNS name>:<port>/api/v1/hub/hello`
with a 2-second timeout, trying 8443, then 8444 to 8446. `200` with this body: a dashboard that can link. `404`
here but `200 {"status":"ok"}` at `/healthz`: a dashboard older than 0.8.5, shown as "Update this dashboard to
0.8.5 to connect". The probe sends nothing secret. The hub may accept a certificate it cannot verify for the
probe only, and must then mark that result unverified; linking itself never depends on the probe.

**Starting a link from the hub.** The hub's Connect button opens, in a new tab,
`<endpoint>/dashboard/hub?connect=<hub host>`, for example
`https://vps-07749119-vps-ovh-net.tailed39ba.ts.net:8443/dashboard/hub?connect=hub.tailed39ba.ts.net`. Anyone
on the tailnet can send an administrator such a link with a look-alike name, so the page treats `connect` as a
suggestion. It fills in `https://<hub host>`, says that the address came from a link and to check it is the hub
they meant, shows the hub's machine and its owner from `GET /dashboard/hub/check` (8.2), and leaves the scope
unselected: the administrator must choose one before Connect is enabled. It shows exactly what the hub will see
and be able to do at the chosen scope, and does nothing until a server administrator presses Connect. The hub's
new tab is often signed out; the dashboard then sends it to sign in and back to this address afterwards (this
address only, rebuilt from its `connect` value, so a link someone else wrote cannot send a fresh sign-in anywhere
else).

### 8.2 `/api/v1/dashboard/hub`

| Route | Who | Does |
| --- | --- | --- |
| `GET /dashboard/hub` | Any signed-in principal (session or API key, any role). The `admin` part only for `system.admin` | The status below |
| `GET /dashboard/hub/check?url=<url>` | `system.admin`, session only | Apply the URL rule (2.2 steps 1 to 8) without starting anything. `200 {"hubUrl", "machine", "testOrigin"}`, below. Errors as Connect's URL-rule errors. Not audited |
| `POST /dashboard/hub` `{"url", "scope"}` | `system.admin`, session only | Connect. Audited `hub.link.begin`, with the hub node in the detail |
| `DELETE /dashboard/hub/request` | `system.admin`, session only | Forget the pending request (`204`). Audited `hub.link.cancel`. The hub is not told; the request expires there |
| `PUT /dashboard/hub/scope` `{"scope"}` | `system.admin`, session only | Change the scope. Audited `hub.link.scope`. The next heartbeat tells the hub |
| `DELETE /dashboard/hub` | `system.admin`, session only, `s.destructive` | Disconnect. `200 {"hubNotified": bool}`: whether the hub was told, since the link is cleared either way and a hub that was not still lists the server until it is removed there. Audited `hub.unlink` with `hubNotified` |
| `POST /dashboard/hub/sync` | `system.admin` (an API key may) | A heartbeat now, then the status |
| `DELETE /dashboard/hub/signin/{userId}` | `system.admin`, session only | Remove one account's sign-in grant (`204`). Audited `hub.signin.revoke` (section 9) |

"Session only" means an API key is refused with `403 session_required` (`httpx.RequireSession`): an API key
must not be able to hand this server to a hub or take it back. A hub session (9.9) is refused with
`403 hub_session_not_allowed` on `POST /dashboard/hub`, `DELETE /dashboard/hub/request` and
`PUT /dashboard/hub/scope`; it may disconnect, sync and remove grants, which only take power away from the hub.
The `GET`s sit with the other every-role reads of the `/dashboard` group (`check` with `system.admin` added);
the rest sit in its `system.admin` part.

**The check.** `GET /dashboard/hub/check` answers:

```json
{
  "hubUrl": "https://hub.tailed39ba.ts.net",
  "machine": { "id": "nK7f2QbPaa21CNTRL", "name": "hub.tailed39ba.ts.net", "owner": "wayy01@github", "tags": [] },
  "testOrigin": false
}
```

`machine` is left out when tailscaled could not be asked, and the page then says the machine was not checked.
`owner` is the node owner's login; a tagged node has `owner` left out and its tags in `tags`. The Settings page
calls it as the address is typed or filled in, and shows "hub.tailed39ba.ts.net, a machine of wayy01@github" (or
"tagged tag:server") next to the address before Connect.

**Errors.**

| Status | `code` | When |
| --- | --- | --- |
| `400` | `bad_request` | `scope` is not `observe`, `operate` or `maintain`, or the body is malformed |
| `400` | `hub_url_invalid`, `hub_url_not_tailnet`, `hub_url_same_host`, `hub_not_on_tailnet` | The URL rule, 2.2 |
| `403` | `hub_link_disabled` | `JD_HUB_LINK=false` |
| `409` | `already_linked` | Connect while linked to a hub. One hub per server: disconnect first |
| `409` | `link_pending` | Connect while a request is waiting |
| `409` | `not_linked` | Scope, disconnect or sync with no hub link |
| `409` | `hub_url_refused` | Sync while the link is idle: the stored hub URL fails the rule now (2.2), or the hub's machine changed (2.4) |
| `403` | `hub_session_not_allowed` | A hub session (9.9) on Connect, cancel or a scope change |
| `502` | `hub_unreachable` | The hub could not be reached, refused the request, or answered with something that is not protocol 1. The message says which |

**The status.** `GET`, and the `200` answers of `POST`, `PUT` and `sync`, return this shape. Optional fields are
left out, never `null`. Times are RFC 3339 in UTC. The example shows every field at once; a real answer carries
only the ones its state allows, as the table after it says.

```json
{
  "enabled": true,
  "state": "linked",
  "hubUrl": "https://hub.tailed39ba.ts.net",
  "hubName": "hub",
  "scope": "maintain",
  "reachable": true,
  "lastHeartbeatAt": "2026-11-05T10:00:00Z",
  "heartbeatSeconds": 15,
  "idle": true,
  "fleet": [
    {
      "serverId": "7a0d3c1e-0000-4000-8000-000000000002",
      "name": "vps-07749119-vps-ovh-net",
      "endpoint": "https://vps-07749119-vps-ovh-net.tailed39ba.ts.net:8443",
      "state": "online",
      "version": "0.8.5",
      "self": true,
      "lastSeenAt": "2026-11-05T10:00:00Z"
    }
  ],
  "notices": [
    {
      "id": "update-0.8.6",
      "level": "info",
      "title": "Update ready on 1 server",
      "body": "0.8.6 is out. vmi3690758 can install it from Just Hub.",
      "href": "https://hub.tailed39ba.ts.net/updates",
      "at": "2026-11-05T09:58:00Z"
    }
  ],
  "signIn": {
    "enabled": true,
    "offered": true,
    "users": ["owner"],
    "mine": { "hubUser": "owner", "since": "2026-11-05T09:00:00Z" }
  },
  "admin": {
    "fingerprint": "21fe:31df:a154:a261",
    "testOrigin": "http://127.0.0.1:43180",
    "request": {
      "hubUrl": "https://hub.tailed39ba.ts.net",
      "userCode": "K7QM-29XD",
      "approveUrl": "https://hub.tailed39ba.ts.net/servers/add/?request=2c9e41d7-0000-4000-8000-000000000001",
      "expiresAt": "2026-11-05T10:15:00Z",
      "scope": "maintain",
      "hubMachine": { "id": "nK7f2QbPaa21CNTRL", "name": "hub.tailed39ba.ts.net", "owner": "wayy01@github", "tags": [] }
    },
    "link": {
      "serverId": "7a0d3c1e-0000-4000-8000-000000000002",
      "linkedAt": "2026-11-05T09:00:00Z",
      "linkedBy": "ion",
      "hubMachine": { "id": "nK7f2QbPaa21CNTRL", "name": "hub.tailed39ba.ts.net", "owner": "wayy01@github", "tags": [] },
      "lastError": "the command poll failed: the hub answered HTTP 503",
      "lastCommand": {
        "id": "01JB0000000000000000000011",
        "kind": "container.restart",
        "status": "succeeded",
        "issuedBy": "owner",
        "at": "2026-11-05T09:59:03Z"
      },
      "hubKeys": [ { "kid": "hub-2026-11", "fingerprint": "39f7:13d0:a644:253f" } ],
      "updatePolicy": { "auto": "patch", "window": { "start": "03:00", "end": "05:00" } },
      "lastSnapshot": { "protocol": 1, "sentAt": 1761350400, "server": { "name": "vps-07749119-vps-ovh-net" } }
    },
    "lastOutcome": { "result": "denied", "at": "2026-11-05T08:00:00Z", "message": "the request was declined in the hub" },
    "grants": [ { "userId": 1, "username": "ion", "hubUser": "owner", "since": "2026-11-05T09:00:00Z" } ]
  }
}
```

| Field | Who sees it | Meaning |
| --- | --- | --- |
| `enabled` | everyone | `JD_HUB_LINK`. When `false` only `enabled` and `state` are sent, and the page explains the switch |
| `state` | everyone | `unlinked`, `pending` or `linked` |
| `hubUrl` | everyone | The stored hub URL, when pending or linked |
| `hubName` | everyone | `owner.label` from the approval, when linked |
| `scope` | everyone | The scope held, when linked |
| `reachable` | everyone | When linked: the last heartbeat succeeded, no heartbeat has failed since, and it is younger than three intervals |
| `lastHeartbeatAt` | everyone | When linked and one has succeeded |
| `heartbeatSeconds` | everyone | When linked: the interval the hub set (4.2, 4.3), clamped to 10–600 as the heartbeat loop clamps it. The page says how often the server reports in it |
| `idle` | everyone | `true`, only while the stored link is idle (2.2 "When the rule runs", 2.4); left out otherwise. The reason is `admin.link.lastError`. While it is set no page offers the stored hub URL as a link: after "the hub's machine changed" it answers on a machine this server refused |
| `fleet`, `notices` | everyone | The cached reply (section 6) with times as RFC 3339. `[]` before the first reply. Only when linked |
| `signIn.enabled` | everyone | `JD_HUB_SIGNIN` |
| `signIn.offered` | everyone | The last reply carried `signIn` |
| `signIn.users` | everyone | Its `users`, or `[]` |
| `signIn.mine` | everyone | The calling account's own grant, if it has one |
| `admin` | `system.admin` | Left out for everyone else |
| `admin.fingerprint` | | The hub link identity's fingerprint, once it exists |
| `admin.testOrigin` | | Only when `JD_HUB_LINK_TEST_ORIGIN` is set |
| `admin.request` | | When pending. `hubMachine` is the hub node 2.2 step 8 found, left out when it was not checked |
| `admin.link` | | When linked. `lastSnapshot` is the exact body of the last heartbeat sent, so the page shows what is shared and not a description of it. `linkedBy` is the account that connected, or `cli`. `hubMachine` is the pinned hub node (2.4), left out until one is recorded. The page shows each `hubKeys[].fingerprint` with "compare with the key fingerprint on the hub's Settings page". `lastError` is the last heartbeat's failure, else the last command poll's; a poll's failure is cleared once a poll has been held 2 seconds, since the hub answers a failure at once and holds only a poll it took (4.4) |
| `admin.lastOutcome` | | When unlinked and the last request ended without a link: `denied`, `expired` or `failed`; or the last link was removed in the hub (8.9): `removed`, with the message "the server was removed in the hub" |
| `admin.grants` | | Every account's sign-in grant (section 9) |

### 8.3 The `hub` field on `GET /auth/session`

The shell reads it before its first paint and renders hub features only when it is there. It is present only
when the session is fully authenticated (`authenticated: true`), `JD_HUB_LINK` is on, and a hub link is stored.
It is never sent to a session that owes a second factor or a password change, or to nobody. The example
shortens `user`.

```json
{
  "authenticated": true,
  "user": { "id": 1, "username": "ion" },
  "capabilities": ["system.admin"],
  "needsTotp": false,
  "needsEnrollment": false,
  "require2fa": false,
  "hub": {
    "linked": true,
    "name": "hub",
    "url": "https://hub.tailed39ba.ts.net",
    "reachable": true
  }
}
```

| Field | Type | Meaning |
| --- | --- | --- |
| `hub.linked` | bool | Always `true` when `hub` is present. The frontend gates on `auth.hub?.linked` |
| `hub.name` | string | The hub's name (`owner.label`) |
| `hub.url` | string | The stored hub URL, for "Open Just Hub" |
| `hub.reachable` | bool | As `reachable` in 8.2 |
| `hub.idle` | bool, optional | `true` only while the link is idle, as `idle` in 8.2; the shell then offers no link to `hub.url` |

In Go this is a field `Hub *hubLinkSession` on `authStatus`, with the tag `json:"hub,omitempty"`. In TypeScript
it is `hub?: { linked: true; name: string; url: string; reachable: boolean; idle?: true }` on `AuthStatus`. The
fleet and notices come from `GET /dashboard/hub`, not from here, so this field stays small.

### 8.4 Terminal: `manage.sh hub`

```
sudo ./scripts/manage.sh hub connect URL [--scope observe|operate|maintain] [--token-stdin] [--wait 15m]
sudo ./scripts/manage.sh hub disconnect
sudo ./scripts/manage.sh hub status
```

`manage.sh` checks the arguments, then forwards to the backend's admin commands like `link` does:
`docker compose run --rm --no-deps -T backend --admin hub-connect …`, `--admin hub-disconnect` and
`--admin hub-status` (in `internal/admin`). It keeps their exit status with `exec`. The commands read
`JD_HUB_LINK`, `JD_HUB_LINK_TEST_ORIGIN` and `JD_MASTER_KEY` from the Compose environment. `connect` and
`disconnect` need `JD_MASTER_KEY` to open the identity; `status` does not.

| Command | Does | Exit status |
| --- | --- | --- |
| `hub connect URL` | Applies the URL rule, starts a request at `--scope` (default `maintain`), prints the hub URL, the hub's machine and owner (2.2 step 8; "not checked" when tailscaled could not be asked), the code, the fingerprint and the hub's approval screen for the request (`approveUrl` as the hub gave it), and waits up to `--wait` (default 15 minutes, at most 1 hour) for approval. With a token the approval usually arrives on the first poll. On success it prints `Connected to Just Hub "<name>" at <url> as "<server name>" (<scope>).` and the pinned hub key's fingerprint | `0` linked. `2` declined, expired, not approved within `--wait`, or interrupted; the request is forgotten. `1` anything else: switched off, already linked to a hub, a request already pending from Settings or from another `hub connect` still waiting, the URL refused, the hub unreachable, `bad_token`, `JD_MASTER_KEY` missing |
| `hub disconnect` | Clears the link and tells the hub (`POST /unlink`). Prints whether the hub was told | `0` disconnected. `1` not linked, or the store could not be written |
| `hub status` | One line: not connected (and how the last request ended, or that the hub removed the link), the pending code, or connected to whom, at which scope, the last heartbeat and the last error. Says so when `JD_HUB_LINK=false` | `0` printed. `1` the store could not be read |

`--token-stdin` is the only way to give a token. When standard input is a terminal, `manage.sh` prompts
`Link token: ` and reads it with echo off (`read -rs`); otherwise it reads one line, which is how the hub
installer pipes its token in. `manage.sh` hands the token to the admin command on that command's standard input,
never as an argument, so it is in no process list (`/proc/*/cmdline`) and no shell history. There is no option
that takes the token as an argument. A request started at the terminal is polled only by that command; one
started in Settings is never taken over from the terminal, as with the account link. Nor is one whose command
still waits: that command says so every 5 seconds, and a second `hub connect` is refused with the first one's
code. Only a request whose command has been silent for 20 seconds (killed outright) is replaced by the next
`hub connect`; a command whose request was replaced stops. Audited as `hub.link`
(or `hub.link` failed, with the outcome) and `hub.unlink`, by `root` / `cli`.

### 8.5 Storage

All link state is in the `settings` table, under `hub.link.*`. Only the identity is secret, and it is sealed.

| Key | Holds |
| --- | --- |
| `hub.link.identity` | The Ed25519 seed, sealed with `auth.Sealer` |
| `hub.link.state` | The link: hub URL, `serverId`, pinned hub keys, the hub node (2.4), whether it is idle and why, scope, who linked and when, the hub's name, the server's own name, intervals |
| `hub.link.request` | The pending request: id, code, approval address, expiry, scope, the hub node found at connect, who asked, which process polls it |
| `hub.link.sequence` | The request counter. One for the life of the identity, never reset, raised to `max(stored + 1, unix milliseconds)` (3.1) |
| `hub.link.update-policy`, `hub.link.update-attempt` | The update policy the hub set, and the last version it was tried for |
| `hub.link.outcome`, `hub.link.status` | Why the last request ended; the last heartbeat time and the last heartbeat and poll errors |
| `hub.link.view` | The last heartbeat reply's `fleet`, `notices` and `signIn`, with the time received. Not secret, cleared with the link. Written only inside the transaction that checks the link is still the one the heartbeat was sent for (8.9) |

The commands table, added by the normal additive migration (`store/hub_link_schema.go`, invariant 8 in
[invariants.md](../security/invariants.md)):

```sql
CREATE TABLE IF NOT EXISTS hub_link_commands (
  id          TEXT PRIMARY KEY,
  kind        TEXT NOT NULL,
  received_at INTEGER NOT NULL,
  status      TEXT NOT NULL,
  finished_at INTEGER,
  error       TEXT NOT NULL DEFAULT '',
  issued_by   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_hub_link_commands_received ON hub_link_commands(received_at);
```

It is `account_link_commands` with another name: `status` is `running`, then `succeeded`, `failed` or `refused`;
rows older than a week are pruned. Hub sign-in adds two tables (section 9.8). Nothing existing is altered.

### 8.6 Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `JD_HUB_LINK` | `true` | `false`: no hello route, no connect, no terminal connect, no `hub` field in `/auth/session`, and the connector runs nothing. An existing link stays in the store, idle; disconnect still works and then tells no one. Hub sign-in counts as off while this is `false`. Independent of `JD_ACCOUNT_LINK` |
| `JD_HUB_SIGNIN` | `false` | The operator's switch for hub sign-in (section 9). Off, tickets are refused, grants cannot be made, and the heartbeat carries no `signIn`. Found off at start (or with `JD_HUB_LINK=false`), every grant is deleted and every hub session revoked (9.3), in one transaction that commits before the API serves its first request; if it cannot be written the backend does not start. While it is off, a hub session is refused on use even if one was missed (9.3) |
| `JD_HUB_LINK_TEST_ORIGIN` | unset | Tests only, section 2.3. With `JD_HUB_SIGNIN=true` the backend refuses to start unless built with the `hublinktest` tag |

`JD_HUB_LINK` and `JD_HUB_SIGNIN` follow the configuration rule's four places (`config.go`, `.env.example`,
`docker-compose.yml`'s `environment:` map, the table in `docs/guides/configuration.md`). They take the same
boolean spellings as `JD_ACCOUNT_LINK`. The test origin deliberately appears only in `config.go` and in this file.

### 8.7 Audit

Every command, run or refused, writes `hub.command.<kind>`, or `hub.command.invalid` for a kind that is not one
of section 7.1's, so a hub cannot choose the action name. Username and actor are `hub:<hub name>`, method
`LINK`, path `/api/hub/v1/commands`. The target is the container, the stack, `job:<id>`, the version or the
policy. The detail holds the command id, kind, status, error, the `args` as sent (cut at 300 characters) and the
`issuedBy` the hub states.

| Action | Written when | Actor |
| --- | --- | --- |
| `hub.link.begin`, `hub.link.cancel`, `hub.link.scope` | The Settings page starts, cancels or re-scopes | The account |
| `hub.link` | A link completes. Detail: scope, `serverId`, hub URL | The account, or `root` / `cli` |
| `hub.unlink` | A person disconnects (detail `hubNotified`), or the hub removed the server (`system`, with the reason) | The account, `root` / `cli`, or `system` |
| `hub.link.node_recorded`, `hub.link.node_changed` | The hub node was recorded after connect, or a different machine answered at the hub's name and the link went idle (2.4). Detail: the old and new node id and name | `system` |
| `hub.link.idle` | The connector set the link idle under the URL rule (2.2). Detail: the reason, and how many hub sessions were revoked | `system` |
| `hub.command.<kind>`, `hub.command.invalid` | Every command | `hub:<hub name>` |
| `hub.update.auto` | The update policy installs a version | `system`, username `hub:<hub name>` |
| `hub.signin`, `hub.signin.allow`, `hub.signin.revoke` | Section 9.10 | |

The `issuedBy` of a command is recorded but not trusted. The hub's own activity log is what says which hub user
asked.

### 8.8 One hub per server, the account link and agent mode

- **One hub per server.** A server has at most one hub link. Connecting a second hub needs a disconnect first
  (`409 already_linked`).
- **Beside the account link.** A server may be linked to just-dashboard.com and to one hub at the same time.
  The two never share state: separate identity, sequence counter, link state, command table, update policy,
  loops, rate pacing, destructive-budget key and audit actor. Switching one off, unlinking it or losing its key
  does not touch the other. Both use the same `Host` implementation, so a command does the same thing whichever
  link sent it.
- **Two update policies.** Each link keeps its own policy and evaluates it on its own. A server installs an
  update when either policy says so; the installer refuses to start a second run while one is under way. Each
  link's snapshot reports only its own policy.
- **Agent mode.** No hub link connector is built and no hub link route is mounted. Agent mode's mTLS hub is a
  different thing (section 1).

### 8.9 The connector's life

The account link's loops, run as a second instance with the hub's prefix, table, base path, URL rule and switch.

- **Unlinked.** The connector looks at the stored state every 15 seconds. A request started in Settings is
  polled by the running dashboard; one started at the terminal is polled only by that command. Either one runs
  the hub node check before every poll (2.4 "While a request is pending").
- **Start.** Before the first request on a stored link, the connector applies the URL rule (2.2 "When the rule
  runs") and the hub node check (2.4). A failure sets the link idle (`hub_url_refused`) and revokes every hub
  session in one write transaction, and audits `hub.link.idle` or `hub.link.node_changed`. An idle link sends
  nothing until a person disconnects.
- **Linked.** Four loops: heartbeat, command poll, update policy (every minute), and a watch that ends the loops
  within 5 seconds when the stored link changes (a disconnect or connect from the terminal). Nothing needs a
  restart.
- **Writes belong to the link they describe.** What a heartbeat reply changes (the interval, `hub.link.view`, and
  the grants it ends, 9.3) is written in one transaction that first re-reads the link state and writes nothing
  if its `serverId` is no longer the one the request was sent for, as `noteHeartbeat` does with
  `updateState(serverId)`. A status write (`hub.link.status`: last heartbeat, last error) is skipped when the
  link it describes is gone. A reply that arrives after a disconnect therefore cannot bring back the old hub's
  users, notices or fleet.
- **Disconnect** (Settings or terminal): in one transaction, the link state, request, status, policy, policy
  attempt, outcome and `hub.link.view` are deleted, and so are every sign-in grant and ticket record (section 9),
  and every hub session is revoked. Then, when the link may still send (switched on, not idle, the hub's machine
  checked), `POST /unlink`, signed, one attempt. The link is gone before the hub hears: the hub answers `410` to
  every request still in flight on it, the running dashboard's included when the terminal disconnects, and one
  that finds no link stored clears and records nothing, so a disconnect is one `hub.unlink`. `clearLink` and
  `Unlink` both clear in their own transaction, and `clearLink` reads the link inside it. The identity and the
  sequence stay, so a later link shows the same fingerprint.
- **Removed in the hub** (`410` on any signed request): the same clearing, audited as `hub.unlink` by `system`
  with the reason "the server was removed in the hub". The same transaction keeps the outcome `removed` with that
  reason (8.2 `admin.lastOutcome`), so the Settings page says so once; a new request or link clears it.
- **Identity unreadable** (`JD_MASTER_KEY` changed): an unlinked install makes a new identity; a linked one
  reports the error and needs a disconnect and a new link ("re-link needed").

### 8.10 What the dashboard shows from the hub

A hub that is taken over can put any text in notice titles and bodies, any name in `fleet`, any `owner.label`,
and any message in the errors the Settings page shows. Within the rules each of these could read like the
dashboard's own prompt ("your session expired, sign in again"). So:

- Every piece of text that came from the hub (notice titles and bodies, fleet names, `owner.label`, the hub's
  `message` in an error, `admin.lastOutcome.message`, `admin.link.lastError` when it quotes the hub) is shown as
  plain text attributed to the hub, for example "Just Hub (hub) says: …", never as markup.
- It is never styled like the dashboard's own sign-in, session, password or two-factor prompts, and never shown
  in the place those prompts use.
- A notice link shows its host next to its text and opens in a new tab with `rel="noopener noreferrer"`.
- The fleet switcher shows names as text and opens endpoints in a new tab; it never sends a credential or a
  ticket to one.

## 9. Hub sign-in (version 1, off by default)

Hub sign-in lets a person open a server from the hub and land on it already signed in. The hub makes a
single-use ticket that lasts at most 60 seconds, signs it with the hub key, and opens the server's
`/hub/enter` page with the ticket in the URL fragment. The dashboard checks the ticket and starts a session.

### 9.1 The exception to invariant 2

Invariant 2 says an account with an authenticator is always asked for a code on this server. A hub ticket
starts a **complete** session (`twofa_passed = 1`) without that code. The second factor that stands in for it is
a code from the hub user's authenticator app (TOTP), proved in the hub session that asks for the ticket, at most
10 minutes before the ticket was made, which the hub states in the ticket (`mfaAt`) under its pinned key. A
recovery code on the hub does not count.

This is a deliberate, documented weakening, and it is allowed only when all of these hold:

1. **The operator signed off.** `JD_HUB_SIGNIN=true` is set by root on this server. It defaults to `false`.
   Before a release turns the code on anywhere, a written security review of this section against
   [invariant 2](../security/invariants.md#invariant-2-what-two-factor-still-guarantees) must be done and
   recorded, and invariants.md must name this exception in its invariant 2 section, with the limits of 9.9.
2. **The account holder opted in**, while fully signed in on this server, by entering their password again and,
   if the account has an authenticator, a current code (section 9.3). Each account decides for itself. A grant
   made while the account had no authenticator ends when it enrols one, so a grant never stands in for a code
   the account now requires but never gave.
3. **The grant names one hub user.** Only that hub user's tickets can sign in as that account. A hub user name
   is never given to another person (6.3).
4. **The hub's check is fresh.** `iat − mfaAt` is at most 600 seconds, and the hub must have asked for the code
   itself (section 10).
5. **Nothing on this server says no.** The account is not disabled, not locked, does not owe a password change,
   and is not an unenrolled account on a server with `JD_REQUIRE_2FA=true`.
6. **The ticket is used by the device that asked for it.** The ticket names the hub session's tailnet login and
   the tailnet node of the device that asked the hub; the dashboard checks both against WhoIs of the browser's
   address (9.7 step 10).

What it costs, plainly: with hub sign-in on, anyone who takes over the hub can open a session as every account
that opted in, from a device on the tailnet, and do what that account can do, except what 9.9 refuses to a hub
session. 9.9 refuses the dashboard's own forms that create, change, rotate, import, export or reveal a credential
(API, MCP or deploy keys, dashboard accounts, its own password, authenticator, recovery codes or passkeys, another
account's authenticator, Linux users and their keys and passwords, database users and their passwords, basic-auth
users, deployment hook secrets and secret variables, saved Git, registry, DNS and remote-computer credentials,
secret stores, notification channels, VPN peers' keys, GitHub sign-in, TLS keys, the backup key), connecting or
re-scoping a link, and opting in to hub sign-in, because each of those would outlive the grant. A hub session
lasts at most 2 hours and ends at once when its grant ends, when hub sign-in is switched off and when the link
ends or goes idle, and every socket it opened (a terminal, a container's shell, a log tail, a remote desktop)
closes with it. What none of this stops: the dashboard's general tools are not restricted. The Files page reads
every file under `JD_FILE_ROOTS` (the whole server by default) as root, for every role, so a hub session can read
every credential on disk, this dashboard's own master key and database included; with `file.write` (the
administrator and `limited` roles) it can write any file, an SSH key or a sudoers rule included; and an
administrator's hub session has the terminal. So for those two roles a hub session is as good as root, and for
every role it reads what root can read. Ending hub sign-in ends the hub's sessions; it does not undo what was done
in them. Without hub sign-in, a taken-over hub is limited to the scope's commands. That is why it is off by
default and needs both the operator and the account holder, and why the opt-in screen says all of this.

### 9.2 The three switches

| Switch | Where | Default |
| --- | --- | --- |
| Operator | `JD_HUB_SIGNIN` on each server | `false` |
| Hub | The hub's own setting; when on, its heartbeat replies carry `signIn.users` | off |
| Account | "Let Just Hub sign me in", per dashboard account, on Settings → Hub | off |

All three must be on, and the server must be linked to that hub.

### 9.3 Grants

A grant is one account's permission for one hub user to sign it in.

**Routes**, under `/api/v1/account` (the caller's own account, session only, `httpx.RequireSession`):

| Route | Does |
| --- | --- |
| `PUT /account/hub-signin` `{"hubUser", "password", "code"}` | Make or replace the caller's grant; replacing ends the old grant and revokes its hub sessions ("A grant ends", below). `code` is required when the account has an authenticator. The password and code are checked with `auth.Service.VerifyReauthentication`, which spends the account's shared guess budget and uses up the TOTP step, not with a new check. Refused to a hub session. `204`. Audited `hub.signin.allow` |
| `DELETE /account/hub-signin` | Remove the caller's grant, and revoke its hub sessions. No password needed; a hub session may do it. `204`. Audited `hub.signin.revoke` |

The page reads the caller's own state from `signIn` in `GET /dashboard/hub` (8.2); there is no second read
route.

`PUT` refuses with `403 hub_session_not_allowed` from a hub session, `403 hub_signin_disabled` when
`JD_HUB_SIGNIN` is off, `409 not_linked` with no hub link or an idle one, `409 hub_signin_not_offered` when the
last heartbeat reply had no `signIn`, `400 bad_request` when `hubUser` is not in its `users`,
`401 invalid_credentials` for a wrong password, `401 invalid_code` for a wrong code, and `409 hub_user_taken` when
another account on this server already granted that hub user. Administrators remove anyone's grant with
`DELETE /dashboard/hub/signin/{userId}` (8.2).

**Storage** (additive, in M7):

```sql
CREATE TABLE IF NOT EXISTS hub_signin_grants (
  user_id    INTEGER PRIMARY KEY,
  hub_user   TEXT NOT NULL UNIQUE,
  server_id  TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
```

`server_id` is the link's `serverId` when the grant was made. A grant whose `server_id` is not the current
link's is void.

**A grant ends** when:

- the account removes it, or an administrator removes it;
- the account replaces it: a `PUT /account/hub-signin` while a grant exists ends the old grant (its row is
  deleted and its hub sessions revoked) and makes a new row with a new `created_at`, in one transaction, whether
  or not the hub user changed;
- the hub link ends (disconnect, removal or a new link);
- the account is deleted or disabled;
- its password changes or is reset;
- its authenticator is enrolled, replaced, reset by an administrator or turned off (a grant made without a code
  must not outlive the account getting one; the person opts in again with a code);
- its sessions are all revoked;
- a heartbeat reply no longer offers its hub user: the reply carries no `signIn`, or its `users` does not list
  the grant's `hubUser` (6.3). A failed heartbeat changes nothing;
- `JD_HUB_SIGNIN` is found off at start, or `JD_HUB_LINK` is `false` at start (all grants are deleted then, so
  turning the switch back on does not bring old grants back).

Each of these deletes the row in the same transaction as the change, and **in that same transaction revokes every
session of that account whose `auth_method` is `hub`**. Where the event is about the whole link or the switch
(the link ending, the switches found off at start), every hub session of every account is revoked. The link going
idle (2.2, 2.4) revokes every hub session too, in the same write transaction that sets it idle; its grants stay,
unusable, until a person disconnects. The switches found off at start are handled in one transaction that commits
before the API serves its first request; if it cannot be written, the backend does not start. Each event has its
own test: a hub session made before it is refused after it.

**A hub session lives only while what made it still holds.** The revocations above are required, and in addition
every time the dashboard resolves a session whose `auth_method` is `hub` (`auth.Service.ResolveSession`), it checks
that `JD_HUB_LINK` and `JD_HUB_SIGNIN` are both on, that a hub link is stored and not idle, and that a grant
exists for the session's account whose `server_id` is the link's `serverId` and whose `created_at` is not later
than the session's `created_at`. If any of these fails, the session is deleted and the request is answered as for
a revoked session (`401`). So a revocation that was missed, failed, or lost a race with a redeem cannot leave a
hub session working. A test deletes a grant row without revoking its sessions and checks that the hub session's
next request is `401`.

**Telling the hub.** When `JD_HUB_SIGNIN=true`, every heartbeat on the hub link carries one extra top-level key,
at every scope:

```json
{ "signIn": { "grants": [ { "userId": 1, "username": "ion", "hubUser": "owner" } ] } }
```

`grants` is every valid grant, at most 100, possibly `[]`. `userId` is the dashboard account's id, `username` its
current user name (for display on the hub only), `hubUser` the hub user it trusts. When `JD_HUB_SIGNIN` is off the
key is absent, and the hub must treat that as "sign-in off here". A grant change sends a heartbeat at once. This is
the only place an account name leaves the server, and only by that account's own choice; the opt-in screen says
so.

### 9.4 The ticket

```
ticket        = "v1" "." kid "." payloadSeg "." sigSeg
payloadSeg    = base64url-without-padding(payload JSON bytes)
signed string = "jd hub sign-in ticket v1" LF kid LF payloadSeg
sigSeg        = base64url-without-padding(Ed25519(hub key, signed string))
```

`kid` matches `^[0-9A-Za-z][0-9A-Za-z_-]{0,63}$`, so a ticket splits into exactly four parts on `.`. The first line
of the signed string contains a space, which no `serverId` can, so a ticket signature can never be taken for a
command signature (whose first line is a `serverId`) or the other way round. The whole ticket is at most 2048
characters.

The payload is a JSON object with exactly these fields (no others):

| Field | Type | Rule |
| --- | --- | --- |
| `v` | int | `1` |
| `typ` | string | `hub.signin` |
| `hub` | string | The hub's origin, which must equal the dashboard's stored hub URL |
| `serverId` | string | The target server's `serverId` on this hub |
| `userId` | int | The dashboard account's id, from that server's grant. At least 1 |
| `hubUser` | string | The hub user who asked. `^[a-z0-9][a-z0-9._-]{0,63}$` |
| `tsLogin` | string | The tailnet login the hub session is bound to, lower case, for example `wayy01@github`. 1 to 254 printable ASCII characters with no space and no upper-case letter: `^[\x21-\x40\x5b-\x7e]{1,254}$` |
| `tsNode` | string | The `StableNodeID` WhoIs gave the hub for the device that asked for this ticket. `^[0-9A-Za-z_-]{1,64}$` |
| `nonce` | string | 16 random bytes, base64url without padding: `^[A-Za-z0-9_-]{22}$` |
| `iat` | int | Unix seconds when the hub made it |
| `exp` | int | Unix seconds. `iat < exp ≤ iat + 60` |
| `mfaAt` | int | Unix seconds of the hub user's last successful TOTP code in the hub session that asked (a recovery code does not count). `iat − 600 ≤ mfaAt ≤ iat` |
| `next` | string, optional | A path on the dashboard to open after sign-in. Matches `^/[A-Za-z0-9._~!$&'()*+,;=:@%/?-]{0,511}$` and does not start with `//`. Default `/dashboard`. Left out when not wanted, never `null` |

The dashboard verifies the signature over the exact `payloadSeg` bytes before it parses any JSON, so the hub
may encode the payload with any JSON encoder that follows these rules, which the dashboard enforces:

- The payload is one JSON object and nothing else; whitespace between tokens is allowed.
- Keys are compared byte for byte: `UserId` is not `userId`, it is an unknown key.
- A key that appears twice is refused, whatever its values.
- Every field above that is not marked optional is present; no other key is.
- Integers are written in plain decimal, with no fraction, exponent or quotes (`1`, not `1.0`, `1e0` or `"1"`),
  within ±(2^53 − 1). Strings are JSON strings; `null` is never a value.

A Go dashboard therefore scans the payload with `json.Decoder.Token` (as `decodeArgs` already does for command
arguments) rather than decoding into a struct, which would accept a case-variant or repeated key.

### 9.5 Transport

The hub opens, in a new tab, a top-level navigation to:

```
<endpoint>/hub/enter#ticket=<ticket>
```

`<endpoint>` is the server's last reported `access.endpoint`. The ticket is only ever in the fragment: browsers
do not send a fragment to the server, so it does not reach access logs, proxies or a `Referer`. The hub must not
put a ticket in a query string, a log, a redirect or its own activity log.

`/hub/enter` is a dashboard page outside the signed-in shell and outside the `(dashboard)` route group, like
`/login`. It must:

1. **Take the ticket out of the address before anything else runs.** The page's `<head>` carries an inline
   script, with the page's CSP nonce, that is the first script in the document. It reads `location.hash`, takes
   `ticket=`, keeps the value where only the page's redeem code reads it, and calls
   `history.replaceState(null, "", "/hub/enter")`, so the ticket leaves the address bar and the history entry
   before any framework chunk, provider or other script runs. Nothing on this path redirects while the path is
   `/hub/enter`: not the root layout, not the auth provider, not a signed-out redirect. No code anywhere in the
   dashboard copies `location.hash` into a URL, a `next=` parameter, storage or a log.
2. **Do not replace another account's session silently.** Ask `GET /api/v1/auth/session` (same origin; the
   ticket is not sent). If the browser is fully signed in as an account whose id is not the ticket's `userId`
   (read from the payload segment for display only; the server still verifies everything), show "You are
   signed in as ion. Continue through Just Hub? This signs ion out in this browser." with Continue and Stay,
   and redeem only on Continue. If it is signed out (`401`), signed in as that same account, or only half
   signed in (owing a second factor), go on at once. If the answer is anything else (another status, no
   answer at all), ask the same question without a name, never redeem unasked.
3. `POST /api/v1/auth/hub/enter` with `{"ticket": "<ticket>"}`, and `"replace": true` beside it only after
   the person pressed Continue, with the header `X-JD-CSRF: 1`.
4. On `200`, navigate to the response's `next` with `location.replace`.
5. On an error, show the message, a link to `/login`, and nothing else. It never retries a ticket.

The page loads nothing from another origin, and the ticket leaves the page only in the redeem call's body.

### 9.6 `POST /api/v1/auth/hub/enter`

Inside `/api/v1`, behind the allowlist, in the same group as `POST /auth/login`: the sign-in rate limit per
address (`loginLim`), `httpx.RequireCSRF`, and its own audit entry (the handler records it and calls
`SkipAudit`). Not authenticated: the ticket is the credential. Body `{"ticket": string, "replace"?: true}`, at
most 4 KiB; `replace` is the page saying the person chose to end another account's session in this browser
(9.5 step 2).

`200` answers the same body as `POST /auth/login` for a complete session, plus `next`, and sets the
`__Host-jd_session` cookie:

```json
{ "authenticated": true, "user": { "id": 1, "username": "ion" }, "capabilities": ["system.admin"],
  "needsTotp": false, "needsEnrollment": false, "require2fa": false, "next": "/dashboard" }
```

### 9.7 Verification order

The dashboard checks, in this order, and stops at the first failure.

| Step | Check | Failure |
| --- | --- | --- |
| 1 | `JD_HUB_LINK` and `JD_HUB_SIGNIN` are both on | `403 hub_signin_disabled` |
| 2 | A hub link is stored and the connector has not set it idle (2.2, 2.4) | `403 hub_not_linked` |
| 3 | The body decodes; the ticket is at most 2048 characters, splits on `.` into exactly four parts, the first is `v1`, the second matches the kid pattern, the other two are non-empty and use only the base64url alphabet (`A–Z a–z 0–9 - _`; no `=`, `+` or `/`) | `401 bad_ticket` |
| 4 | The `kid` names a hub key pinned in the link state and not past its `notAfter` | `401 bad_ticket` |
| 5 | The signature decodes to 64 bytes and verifies over the signed string (9.4) with that key | `401 bad_ticket` |
| 6 | The payload decodes from base64url to valid UTF-8 and follows every rule of 9.4: one JSON object, exactly the fields of 9.4 with their types, keys case-sensitive and not repeated, integers in plain decimal; `v` is 1, `typ` is `hub.signin`, `userId ≥ 1`, `mfaAt ≥ 1`; `hubUser`, `tsLogin`, `tsNode`, `nonce` and `next` match their patterns; `iat < exp ≤ iat + 60` | `401 bad_ticket` |
| 7 | `hub` equals the stored hub URL and `serverId` equals the link's `serverId` | `401 wrong_server` |
| 8 | With `now` the dashboard's clock: `iat − 30 ≤ now ≤ exp + 30` | `401 ticket_expired` |
| 9 | `mfaAt ≤ iat` and `iat − mfaAt ≤ 600` | `401 mfa_stale` |
| 10 | **Device.** With `addr` the client address (the trusted client address behind the dashboard's proxy, as the allowlist reads it): if `addr` is loopback (an SSH tunnel), or tailscaled cannot be asked (`ErrUnavailable`: no answer, or an error status), the check is skipped and the audit entry says so. Otherwise WhoIs `addr` must name a tailnet peer whose `UserProfile.LoginName`, lower-cased, equals `tsLogin` and whose `Node.StableID` equals `tsNode`. A non-tailnet address (`ErrNotAPeer`), another login or another node fails, and so does an answer that names no node or no login (`ErrNoIdentity`): tailscaled was asked | `401 wrong_device` |
| 11 | The `nonce` is recorded as used (9.8), in its own statement that commits at once. If it was already there | `401 ticket_used` |
| 12 | In one write transaction with steps 13 and 14: the link is re-read and is still stored, not idle, with the ticket's `serverId`, and both switches are still on (else `403 hub_not_linked`); a grant exists for `userId`, its `hub_user` equals `hubUser`, and its `server_id` equals the link's `serverId` | `403 not_permitted` |
| 13 | The account exists, is not disabled, is not locked (neither `locked_until` nor `totp_locked_until` is in the future), does not owe a password change, and is not an unenrolled account while `JD_REQUIRE_2FA=true` | `403 account_unavailable` |
| 14 | A session is made (9.9) and the transaction commits; then the cookie is set and `200` is answered. When the browser's cookie is another account's complete session and the body has no `"replace": true`, nothing is made or revoked | `409 other_account_signed_in`; else `500` |

The 30-second grace in step 8 covers a small clock difference between the hub and the server. The longest a
ticket can be accepted is therefore 120 seconds after it was made, and only once. Steps 10 and 11 come after the
signature and time checks, so a forged or stale ticket cannot use up a real one's nonce, and step 10 comes before
step 11, so a stolen ticket tried from the wrong device does not spend it. Step 11 comes before the grant and
account checks, so a ticket that reached step 11 is spent even if it is then refused.

**Transactions.** Step 11 commits on its own, before step 12 starts. Steps 12 to 14 run in one write transaction
(`BEGIN IMMEDIATE`). A refusal at 12 or 13 commits nothing and does not give the nonce back. Every event that ends
a grant or the link, or sets the link idle (9.3, 8.9, 2.2, 2.4), is itself one write transaction that also revokes
the hub sessions it ends, so the two serialise: either the redeem commits first and the event then revokes the
session it made, or the event commits first and the redeem finds no link, an idle link or no grant. Doing the
event's change and its revocation in two transactions, in either order, is not allowed. A test disconnects between steps 11 and 12, and another between 12 and the
commit, and checks that no live hub session is left.

### 9.8 Single use

```sql
CREATE TABLE IF NOT EXISTS hub_signin_tickets (
  nonce      TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL,
  used_at    INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
);
```

Step 11 is one `INSERT … ON CONFLICT(nonce) DO NOTHING`, committed on its own; zero rows affected means
`ticket_used`. Rows are pruned a day after `expires_at` (`exp + 30`), far past the time a ticket could be
accepted. The rows are cleared with the hub link.

### 9.9 The session

Made through `auth.Service` like every other sign-in, with `twofa_passed = 1`, `auth_method = 'hub'` and the
grant's hub user recorded on the session. It lasts **2 hours at most** (not 12), and 60 minutes idle.
`last_login_at` is stamped. If the request carried a valid session cookie for this browser, that session is
revoked first, so one browser does not hold two sessions. When it is another account's complete session, it is
revoked only when the body says `"replace": true` (the page asked first, 9.5); without that the redeem is refused
with `409 other_account_signed_in` and both accounts keep what they had (the ticket is spent). The account's
session list shows the method as signed in through Just Hub, with the hub user.

**Ending.** A hub session is revoked in the same transaction as the end of its grant, and every hub session is
revoked in the transaction that ends the link or sets it idle, and when hub sign-in is found off at start, before
the API serves a request (9.3). Every time a hub session is resolved, the switches, the link and its grant are
checked again (9.3 "A hub session lives only while what made it still holds"), so a missed revocation cannot keep
one alive. A socket is authenticated once, when it opens, so every socket a hub session opens (a terminal, a
container's shell, a log or event tail, a Remote Desktop session or stream, the stream bridge's proxied sockets)
is tied to the session: it closes at the session's expiry, at once when a revocation of hub sessions commits,
and within 15 seconds when the session ends any other way, by a check that does not count as use of the
session.

**What a hub session cannot do.** The rule decides; the table applies it to today's routes. A route that
creates, changes, rotates, imports or exports a credential, or creates or changes a link, refuses a session whose
`auth_method` is `hub` with `403 hub_session_not_allowed`. Exporting includes a read: a `GET` whose purpose is to
show a stored secret is refused as an export is. A credential is anything that lets someone act on this
server, or on a system it holds access to, after the hub session has ended: a dashboard account, its password,
authenticator, recovery codes and passkeys; an API, MCP or deploy key; a Linux user, its password, its SSH keys and
its groups; a database user, role, privilege or ACL, a database's password (its connection string, a data copy's
login, the Redis server's `requirepass`, `masterauth` and ACL file); a proxy basic-auth user; a deployment
trigger's or hook's secret, a deployment's secret variables; a saved Git, registry or DNS credential, a DNS
server's API credential, a Cloudflare tunnel token, an AI provider's key, a backup destination, a remote computer's
saved sign-in and a stream pairing with it, a secret-store connection, a notification channel or a log drain (each
holds credentials of its own); a WireGuard peer's key; the enrolment token of a machine that runs external checks;
the dashboard's GitHub sign-in and its GitHub App; a TLS private key; the backup key. The rule covers the
dashboard's own forms for these; it does not reach a file read or written through the Files page or the terminal,
which 9.1 says plainly. One middleware does it,
`httpx.RefuseHubSession`, found by function identity as `httpx.RequireSession` is, so a route-walk test lists every
route that carries it and fails when one in the table does not.

| What | Routes (JD 0.8.5) |
| --- | --- |
| API, MCP and deploy keys | `POST /tokens/`, `POST /tokens/mcp`, `POST /tokens/deploy`, `PUT /tokens/{id}/connections` |
| Dashboard accounts | `POST /dashboard-users/`, `PATCH /dashboard-users/{id}`, `POST /dashboard-users/{id}/reset-totp`, `DELETE /dashboard-users/{id}` |
| The caller's own credentials | `POST /account/password`, `POST /account/recovery-codes`, `POST /account/2fa/disable`, `POST /auth/2fa/setup`, `POST /auth/2fa/enable`, `POST /account/passkeys/register/begin`, `POST /account/passkeys/register/finish`, `PATCH /account/passkeys/{id}`, `DELETE /account/passkeys/{id}` |
| Linux accounts and their keys | `POST /system-users/`, `PATCH /system-users/{name}/`, `PUT /system-users/{name}/sudo`, `POST /system-users/{name}/password`, `POST /system-users/{name}/keys/generate`, `POST /system-users/{name}/keys`, `PATCH /system-users/{name}/keys`, `POST /system-users/{name}/keys/import`, `POST /system-users/{name}/keys/copy`, `POST /system-accounts/bulk`, `PATCH /system-accounts/groups/{group}` |
| Database users and roles | `POST /databases/{id}/server/roles`, `PUT /databases/{id}/server/roles/{name}`, `POST /databases/{id}/server/roles/{name}/grant`, `POST /databases/{id}/server/roles/{name}/privileges`, `POST /databases/{id}/mongo/users`, `PUT /databases/{id}/mongo/users`, `POST /databases/{id}/mongo/users/grant`, `PUT /databases/{id}/redis/acl/{name}`, `PUT /databases/{id}/redis/config`, `POST /databases/host/grant` |
| Database passwords and data copies | `GET /databases/{id}/url`, `POST /deploy/{id}/environments/{env}/releases/{release}/data-copies`, `GET /deploy/{id}/environments/{env}/data-copies/{copy}/url` |
| Proxy basic-auth users | `POST /proxy/auth-files/`, `POST /proxy/import/npm`, `POST /proxy/import/npm/apply` |
| Deployment secrets and saved credentials | `POST /deploy/`, `POST /deploy/{id}/environments/{env}/triggers`, `PUT /deploy/{id}/environments/{env}/triggers/{trigger}`, `POST /deploy/{id}/environments/{env}/triggers/{trigger}/rotate-secret`, `POST /deploy/{id}/rotate-secret`, `POST /deploy/credentials`, `PUT /deploy/credentials/{id}`, `POST /deploy/secret-stores`, `PUT /deploy/secret-stores/{store}`, `POST /deploy/notifications`, `PUT /deploy/notifications/{channel}`, `POST /deploy/log-drains`, `PUT /deploy/log-drains/{drain}` |
| Deployment secret variables | `GET /deploy/{id}/env/reveal`, `GET /deploy/{id}/environments/{env}/variables/{name}/reveal`, `POST /deploy/{id}/environments/{env}/variables/{name}/generate`, `POST /deploy/{id}/environments/{env}/variables/{name}/rotate`, `GET /deploy/shared-variables/{name}/reveal`, `GET /deploy/{id}/shared-variables/{name}/reveal` |
| Git and GitHub | `POST /git/github/auth/device`, `POST /git/github/auth/device/{id}`, `POST /git/github/auth/token`, `POST /git/github/auth/configure`, `POST /deploy/github-app/manifest`, `POST /deploy/github-app/callback`, `POST /git/forge/account` |
| DNS credentials, tunnels and TLS keys | `POST /certificates/dns-credentials`, `POST /certificates/import`, `POST /certificates/export`, `POST /network/dns/services/`, `PUT /network/dns/services/{id}`, `PUT /proxy/tunnel/`, `POST /proxy/tunnel/create` |
| Network peers and check machines | `POST /network/vpn/wireguard/{iface}/peers`, `PATCH /network/vpn/wireguard/{iface}/peers/{id}`, `GET /network/vpn/wireguard/{iface}/peers/{id}/config`, `POST /network/vpn/archive/{file}/restore`, `POST /network/external/enrollments` |
| Remote Desktop | `POST /remote-desktop/computers`, `PUT /remote-desktop/computers/{id}`, `POST /remote-desktop/computers/{id}/stream/pair` |
| Backups | `POST /backups/key/import`, `POST /backups/key/export`, `POST /backups/`, `PUT /backups/{id}`, `POST /backups/import` |
| The assistant's provider keys | `PUT /assistant/providers/{preset}` |
| The account link | `POST /dashboard/link/`, `DELETE /dashboard/link/request`, `PUT /dashboard/link/scope` |
| The hub link | `POST /dashboard/hub`, `DELETE /dashboard/hub/request`, `PUT /dashboard/hub/scope` |
| Hub sign-in | `PUT /account/hub-signin` |

Where the table and the rule differ, the rule wins. The change that added the middleware (JD-SIGNIN, M7) walked
every write route of the router, applied the middleware to each one the rule covers, listed them all in
`docs/internal/security/invariants.md`, and made the route-walk test check that list. The walk found seventeen
routes the rule covers that the table first missed (errata HL-3, route walk); they are in the table above: Linux
accounts made in bulk and group membership, a database login and a role's privileges, a saved Git host token, the
DNS servers' API credentials, Cloudflare tunnel tokens, WireGuard peers and the restore of an archived tunnel, a
machine's enrolment for external checks, backup destinations, and AI provider keys. A second walk, of the routes
that show a stored secret as well as of every write route again (errata HL-3, second walk), found seventeen more:
a WireGuard peer's configuration (its private key), a database's connection string, the Redis server's
configuration (its password and ACL file), a production data copy and its address (a database login and its
password), a deployment's secret variables revealed, generated or rotated, a project's webhook secret, NPM's
basic-auth users imported, and a remote computer's saved sign-in and stream pairing; they are in the table above.
A route added later that the rule covers joins the set, the test and the list in the same change; the route-walk
test reads this table and `invariants.md`'s and fails when either differs from the router. Disconnecting either link,
`POST /dashboard/hub/sync`, removing a grant, revoking sessions, deleting a key, user, credential or channel, and
signing GitHub out stay open to a hub session: they only take power away.

### 9.10 Audit

| Action | When | Username / actor | Detail |
| --- | --- | --- | --- |
| `hub.signin` (success) | A ticket was redeemed | The account / `hub:<hub name>` | `hubUser`, `kid`, `nonce`, `iat`, `mfaAt`, `tsLogin`, `tsNode`, `deviceCheck` (`checked`, or `skipped` with the reason), `sessionId` |
| `hub.signin` (failure) | Any refusal | Username: the account once step 12 found it, else `anonymous`. Actor: `hub:<hub name>` once step 5 passed, else `anonymous` | `code`, and `hubUser`, `userId`, `nonce`, `tsLogin` and `tsNode` once step 6 passed; for `wrong_device`, the login and node WhoIs gave |
| `hub.signin.allow` | An account made or replaced its grant | The account | `hubUser` |
| `hub.signin.revoke` | A grant ended, for any reason in 9.3 | The account, an administrator, or `system` | `hubUser`, `reason`, `sessionsRevoked` |
| `hub.session.revoke` | Hub sessions were revoked by a link or switch event (9.3) with no single grant behind it | `system` | `reason`, `sessionsRevoked` |

Refusals at steps 1 and 2 are audited too, with only the code, so probing a server that has sign-in off is
visible.

### 9.11 Failure modes

| What happens | Result |
| --- | --- |
| The ticket is opened twice (refresh, back button, a copy) | The second gets `ticket_used`. The page offers `/login` |
| The person waited too long, or the clocks differ by more than 30 s | `ticket_expired`. The message says to open the server again from the hub, and to check the clock if it keeps happening |
| The hub's TOTP check is over 10 minutes old | `mfa_stale`. The hub should never send one: it asks for a code first |
| The ticket was made for another server, or by another hub | `wrong_server` |
| A key the dashboard did not pin, or a changed payload | `bad_ticket` |
| The account removed or replaced its grant, or the grant was for an earlier link | `not_permitted` for a ticket naming the old grant's hub user; the old grant's hub sessions were revoked with it |
| The hub stopped offering the hub user (sign-in turned off in the hub, or the hub user renamed) | The grant ended at the next heartbeat, with its hub sessions; `not_permitted`. The account opts in again |
| The ticket is opened on another device, or by another tailnet login | `wrong_device`. The ticket is not spent; the right device can still use it |
| The account is disabled, locked or must change its password | `account_unavailable`. Its own sign-in page is the way in |
| The operator never turned it on, or turned it off | `hub_signin_disabled`; all grants were deleted and all hub sessions revoked at the start after it went off |
| The hub is reinstalled with a new key | Every ticket fails at step 4 until the server is linked again; the grants were cleared with the old link |
| A hub session tries to make an API key, an account, a password or a link | `403 hub_session_not_allowed`. The page says to sign in on the server itself for that |
| The browser is signed in as another account, and the page could not ask or was not answered | The page asks without a name; a redeem without `"replace": true` over another account's complete session is `409 other_account_signed_in`, and both sessions stay |
| A hub session is two hours old | It ends. Opening the server from the hub again makes a new one |
| A browser extension or proxy reads the fragment | It can use the ticket once, within 120 seconds, from the same device and tailnet login that asked the hub (step 10), or from the server itself over loopback. This is the residual risk of the fragment transport |

### 9.12 Before turning it on

The security review that signs off on 9.1 checks at least: that the hub asks for its own TOTP code within the
10 minutes and that a recovery code does not count (section 10); the verification order and its tests, using the
ticket vectors; the device check of step 10; the transactions of 9.7, with the disconnect tests; that grants end
in every case listed in 9.3, replacement included, and that each of those events revokes the account's hub
sessions in its own transaction, with a test per event; that going idle and revoking are one transaction; that the
switch-off clean-up commits before the first request is served; that a hub session whose grant row is gone, or
whose link is idle or gone, or whose switch is off, is refused on its next request even when no revocation ran
(9.3); that a hub session is refused on every route of 9.9, by the route-walk test, and ends after 2 hours; that
every socket a hub session opened closes when the session ends, at its expiry included; that
the redeem route is rate limited and audited; that `/hub/enter`'s first script removes the fragment before any
other script runs, that no request leaves the page before that, that the ticket goes only in the redeem call's
body and that the page loads nothing from another origin; that the page asks before replacing another account's
session; that `JD_HUB_SIGNIN` with a test origin refuses to start in a release build; and that the user-facing
text on the opt-in screen says what the hub can then do, what a hub session cannot do, and that turning sign-in
off ends the hub's sessions but does not undo what was done in them.

## 10. What the hub must do

This list is for the hub implementation. Each item is stated above; this is the checklist.

- Serve the six routes of section 4, and nothing else under `/api/hub/v1`, on its tailnet name with a valid
  certificate.
- Check signed requests in the order of 3.1, with the ±60 s window and an atomic sequence compare-and-store.
  Before step 5 passes, spend only a budget keyed on the caller's tailnet address; change nothing about the
  named request or server.
- Keep request ids and server ids apart (3.1 step 2). Answer `410 server_removed` to a removed server.
- Bind each request to its public key. Approve only after the hub owner types the request's code, which the
  hub never shows while the request is pending, or on a valid one-time token. Keep at most 3 pending requests
  per tailnet node and 50 in all. Store tokens as hashes; make them single use and at most an hour long; never
  put one on a command line; bind the `local` token to `HUB_LOCAL_NODE`, and any token the owner bound to a
  machine to that machine.
- Create the server row, end an older row with the same key, and mark the request consumed only when the
  approval is collected (4.2). Repeat the approved answer until 10 minutes after `expiresAt`; then expire an
  uncollected approval and release what it held.
- Sign every command with the pinned key over the receiving server's `serverId`. Use ULIDs and a 10-minute
  expiry. Never send a command outside the scope the server last reported.
- Deliver commands only through the long poll, one verified poll per server, and keep results.
- Decode heartbeats field by field (5.3), keep only what passes, and build everything shown or relayed from those
  values. Accept 15 a minute per server, counted after verification.
- Reply to heartbeats with `fleet` and `notices` that follow section 6: other servers' `version` empty, the
  owner's sharing setting honoured, the whole reply at most 240 KiB.
- Never send `siteKeys`. Never rotate the hub key in version 1.
- Never reveal plans, seats or pauses to a dashboard.
- Confirm each request's and heartbeat's source address with WhoIs, show it first on the approval screen, and
  flag what does not match it.
- For sign-in: offer it only when its own setting is on; issue a ticket only for a hub user with a complete
  session whose last TOTP code (not a recovery code) in that session is at most 10 minutes old (else ask for a
  code first), only to a server whose latest heartbeat carried a grant for that hub user, with the session's
  tailnet login and the asking device's `StableNodeID`, a fresh random nonce and `exp ≤ iat + 60`; put the ticket
  only in the fragment of a top-level navigation; never log it. Never give a hub user name that was ever used to
  another hub user.
- Use the test origin only in tests, and never present a hub at a test origin as a product feature.

## 11. Failure modes

| What happens | What the dashboard does |
| --- | --- |
| The hub cannot be reached, or answers 5xx | Backoff from 1 s to 5 minutes with jitter. The page shows the last error; the switcher says "Hub unreachable". Nothing else changes |
| `stale_time` | The page says the clocks differ by more than 60 s; every request is signed with the local clock |
| `bad_signature`, `unknown_server` | The page says the hub does not accept this server's signature and suggests disconnecting and connecting again. The link is not cleared automatically |
| `410` on a signed request | The hub removed this server: the link, policy, cached view and sign-in grants are cleared, the identity kept, `hub.unlink` written by `system`, the loops stop |
| A response that is not protocol 1 (an unknown field, trailing content, over 256 KiB) | An error; never partly trusted |
| A redirect | Refused |
| A key announced in a heartbeat (`siteKeys`) | Ignored: no key is added, no pinned key's `notAfter` changes and none is dropped. A warning is logged once per link (4.3) |
| Another machine answers at the hub's name | Nothing is sent to it. The connector goes idle (`hub_url_refused`, "the hub's machine changed"), hub sessions are revoked, `hub.link.node_changed` is audited. Disconnect, then connect again once the hub is the machine you expect (2.4). While a request is pending, the request ends as failed instead, and nothing from that machine is pinned |
| `JD_MASTER_KEY` changed | The identity cannot be opened. An unlinked install makes a new one; a linked one says "re-link needed" |
| The database is restored from an older copy | The sequence continues from the clock (`max(stored + 1, unix milliseconds)`, 3.1); the hub accepts it. Nothing to do |
| The server moved to another tailnet, or the test origin was removed | The connector goes idle and says why (`hub_url_refused`), and hub sessions are revoked. Disconnect, then connect to a hub on this tailnet |
| One element of `fleet`, `notices` or `signIn.users` is malformed | That element is dropped; the rest of the reply is used (6) |
| The hub is reinstalled with a new key | Commands fail at the key check (refused, audited). Disconnect and connect again; restoring the hub's backup avoids this |
| The server is mid-update | Its heartbeat stops for about a minute and comes back with the new version; the hub shows `updating` meanwhile |
| The scope is narrowed while a heartbeat is being built | The scope is read again after the machine is read, and the body is cut to it |
| The dashboard restarts mid-command | The record is closed as failed on the next start and reported once |
| `JD_HUB_LINK=false` while linked | The link stays stored and idle. Nothing is sent. Disconnect clears it and tells no one |

## 12. Test vectors

[`hub-link-vectors.json`](hub-link-vectors.json) holds every vector. Ed25519 is deterministic, so each signature is
exact. Both implementations must carry these in their unit tests.

| Section | What to check |
| --- | --- |
| `keys` | Three keys from RFC 8032 section 7.1 (tests 1, 2 and 3): `server`, `hub` (kid `hub-2026-11`) and `other-hub`. Derive the public key from the seed; compute the fingerprint |
| `canonicalJson` | Decode `input` in the value set, canonicalise, compare with `canonical` and its `sha256` |
| `rejectedArgs` | Each `input` must be refused before canonicalisation |
| `requestSignatures` | Build `signedString` from method, path, body, time and sequence; sign with `seedHex`; compare with `signature`. The hub side verifies `signature` against `publicKey` |
| `commandSignatures` | Canonicalise `args`, build `signedString`, sign with `signerSeedHex`, compare. Then verify as the dashboard does, over `verifyServerId` with `verifyPublicKey`: `expect` is `valid` or `refused` |
| `signInTicketContext`, `signInTickets` | Redeem `ticket` against the context (pinned kid and key, hub origin, `serverId`) at `now`, from the case's `client` or else the context's `client` (an address and tailscaled's WhoIs answer for it), through steps 3 to 11 of 9.7; the result must be `expect`, and an accepted ticket's step 10 must record `deviceCheck`. Where a case has `secondRedeem`, redeeming the same ticket again from the context's client must give it (`ticket_used` after a success; `ok` after a `wrong_device`, which spends nothing). `payload` is `null` where the JSON text has a repeated key. A case with `altered` had its ticket changed after signing (a swapped payload, a fifth part, a padded segment, a signature in the standard alphabet), so the identities `ticket = v1.<kid>.<payloadSegment>.<signature>` and `payloadSegment = base64url(payloadJson)` hold only where `altered` is absent. The hub's signer must reproduce `ticket` for every `ok` case with `hubEncoded: true` (compact JSON, keys in the order of the 9.4 table). Steps 1, 2 and 12 to 14 need a real dashboard and are not in the file |
| `helloBodies` | The three exact hello bodies |

The keys and their fingerprints:

| Role | Public key | Fingerprint |
| --- | --- | --- |
| server | `11qYAYKxCrfVS/7TyWQHOg7hcvPapiMlrwIaaPcHURo=` | `21fe:31df:a154:a261` |
| hub | `PUAXw+hDiVqStwqnTRt+vJyYLM8uxJaMwM1V8Sr0Zgw=` | `39f7:13d0:a644:253f` |
| other-hub | `/FHNjmIYoaONpH7QAjDwWAgW7RO6MwOsXeuRFUiQgCU=` | `dac0:73e0:123b:dea5` |

The `server` and `hub` keys are the account link's own vector keys, and the cases marked `"source":
"account-link"` are that protocol's published vectors. They pass unchanged, which shows the algorithms are the
same.

One request vector, for reading without the file. `POST /api/hub/v1/heartbeat`, body
`{"protocol":1,"sentAt":1761350400}`, time `1761350400`, sequence `42`, the server key:

```
signed string   POST\n/api/hub/v1/heartbeat\nee341c8e79b968efdc9c6624e4082ef76da73cb863eea0d9254865a9af8b0d69\n1761350400\n42
signature       8KpmyTh5ycuZjRNnxY3Wi0fFVB1lzQ5mxETekoaX33wET1bqAIMSzWD90zgIBIJZfLNqVmIwUr3cqGY9GwFgDg==
```

One command vector. Server `7a0d3c1e-0000-4000-8000-000000000002`, id `01JB0000000000000000000011`, kind
`container.restart`, args `{"container":"shop-web"}`, expires `1761351000`, the hub key:

```
args sha256     0b9f5b229e2ef3629c47700b7b4e220ba1bef8fa61bd2a1c2c9433c1a9528a36
signed string   7a0d3c1e-0000-4000-8000-000000000002\n01JB0000000000000000000011\ncontainer.restart\n0b9f5b229e2ef3629c47700b7b4e220ba1bef8fa61bd2a1c2c9433c1a9528a36\n1761351000
signature       6F9aRT0gyp09zPXKhMteGbeG5YlFP6JeR8T6pDHFliPU2H9k6ZH4b6jwTr9PJo33A8aO/5P/Uo5+GY6LGqPIBQ==
```

The request and command vectors were checked against the dashboard's own `requestMessage`, `signRequest`,
`decodeArgs`, `canonicalJSON`, `commandMessage`, `Fingerprint` and `Service.verify` when this file was written.
The ticket vectors were regenerated on 2026-11-05 for the amended 9.4 (every payload now carries `tsLogin` and
`tsNode`, so every ticket signature changed) and grew from 12 to 37 cases: the accepted side of each time
boundary, the JSON rules of 9.4, the ticket shape, and the device check. They were checked by two independent
implementations of 9.7 steps 3 to 11, one in Go that scans the payload with `json.Decoder.Token` and one in
TypeScript, since the dashboard has no ticket code yet. A Go decoder that decodes the payload into a struct
fails three of them (a key in another case, a repeated key, `next: null`), which is what they are for.

## 13. Changing this protocol

- A field added to a **response** is a new protocol version. A v1 dashboard decodes responses strictly.
- A field added to a **request** body is not, because the hub ignores what it does not know. A field added to
  the snapshot still has to be added to `snapshot_test.go`'s pinned set and to the scope table on the Settings
  page, and here in 5.1.
- A new command kind is a new protocol version on both sides.
- The hub link must stay byte-compatible with the account link's signing. If one changes, both change, and the
  reference vectors here are updated with the site's.
- Every change updates the vectors file and this document in the same pull request, and the hub repository's
  copy (`just-hub/docs/protocol/`) in its next change.
- The amendments of 2026-11-05 were made in the hub repository's copy first, with
  [`hub-link-errata.md`](hub-link-errata.md); the dashboard's copy takes them, and the errata file, in its next
  change. They change the ticket payload (two required fields) and nothing else that is signed. Until then the
  two copies differ, and `scripts/sync-vectors.sh` reports it.
