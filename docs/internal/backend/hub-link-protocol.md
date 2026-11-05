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
| Hub key | The hub's Ed25519 key. Its public half is pinned on the dashboard at approval. It signs commands and sign-in tickets. |
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
  response is not used. New response fields need a new protocol version.
- **Lenient requests.** The hub ignores request-body fields it does not know, so the dashboard may add
  snapshot fields without a version change. The hub must not reject a heartbeat for an unknown field.

### 2.2 The hub URL rule

The dashboard links only to a hub on its own tailnet. The rule is applied in this order when a person or the
terminal names a hub, and the first failure is the answer.

1. **Read it as an origin.** The input is trimmed. A bare host (`hub.tailed39ba.ts.net`) is read as
   `https://` plus that host. The result must parse as a URL with a host, and have no user name or password,
   no path other than `/`, no query and no fragment. Failure: `hub_url_invalid`.
2. **HTTPS only.** The scheme must be `https`. Failure: `hub_url_invalid`. (The test origin in 2.3 is the only
   exception.)
3. **Spell it one way.** Scheme and host are lowercased, a trailing dot on the host is removed, and port 443
   is dropped. Any other port is kept. This is the stored hub URL, and every later comparison uses it. It is
   the same spelling `accountlink.originOf` produces.
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
8. **Optional: the name belongs to that machine.** If tailscaled's LocalAPI socket can be reached
   (`tailnetgate.NewLocalAPI`), the dashboard takes the address it would dial (see "Reaching the hub" below)
   and asks `GET /localapi/v0/whois?addr=<ip>`. The answer's node name, without its trailing dot, must equal the
   host. A different node, or no node (`ErrNotAPeer`), is refused with `hub_not_on_tailnet`. If tailscaled
   cannot be asked (`ErrUnavailable`), the check is skipped and the skip is logged: the certificate and steps
   5 to 7 already bind the name. Version 1 dashboards should run this check.

**When the rule runs.** At connect time, on the whole URL. Again each time the connector starts with a stored
link: if the suffix can be read and the stored URL now fails steps 5 to 7 (the server moved to another
tailnet, or the test origin was removed), the link goes idle and is not cleared, and the Settings page says
why (`hub_url_refused`). If the suffix cannot be read at start, nothing is decided and the link runs. The
optional check in step 8 runs only at connect time.

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
- Fleet endpoints and notice links from a hub at the test origin follow the relaxed rules in section 6.

Its name contains `TEST` on purpose. It must never be documented as a way to reach a hub off the tailnet.

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
never handed the same one. It is never reset by an unlink. It starts with the request polls and continues
into the linked server's heartbeats. It is a different counter from the account link's.

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

When a request is approved, the new server row starts with the request's `last_sequence`.

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
| Approval | A short code approved by a person, signed in | A code approved in the hub, **or** a one-time token from the hub (section 4.1) |
| Pending request poll | Shape and status codes | None |
| Response field names | `siteKey`, `siteKeys`, `owner.label` keep their names so one decoder reads both | Here "site key" means the hub key, and `owner.label` is the hub's name |
| Heartbeat body | The snapshot, field for field (section 5) | Adds one optional top-level key, `signIn`, only when hub sign-in is switched on (section 9.3) |
| Heartbeat reply | `ok`, `heartbeatSeconds`, `pendingCommands`, `siteKeys` | Adds `fleet` and `notices` (always), and `signIn` (optional) |
| Heartbeat interval | Clamped to 10–600 s | The hub's default is 15 s, not 30. The hub allows 12 a minute per server, not 4 |
| Key ids | `kid` matches `^[0-9A-Za-z][0-9A-Za-z._-]{0,63}$` | No dot: `^[0-9A-Za-z][0-9A-Za-z_-]{0,63}$`, so a sign-in ticket splits on `.` without ambiguity |
| Storage | `account.link.*` settings, `account_link_commands` | `hub.link.*` settings, `hub_link_commands` (section 8.5) |
| Switch | `JD_ACCOUNT_LINK` | `JD_HUB_LINK`, default on (section 8.6) |
| Audit actor | `account:<owner label>` | `hub:<hub name>` (section 8.7) |

## 4. Endpoints on the hub

| Method and path | Auth | Purpose |
| --- | --- | --- |
| `POST /api/hub/v1/requests` | none; 10 a minute per source address | Start a link request (4.1) |
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
| `server.name`, `server.hostname` | 1–64 characters, printable, no control characters |
| `server.version` | 1–32 characters. The hub may show it; it is never needed for anything else |
| `server.os`, `server.arch` | 1–64 and 1–16 characters |
| `server.endpoint` | Optional. This dashboard's own address, as `access.endpoint` in the snapshot (section 5). Present when the dashboard knows it. At most 255 characters |
| `token` | Optional. A one-time token from the hub. `^jht_[A-Za-z0-9_-]{43}$` |

The dashboard cuts each text to its limit (`fitText`) and sends `unknown` for an empty one, as the account link
does. Everything except `token` and `server.endpoint` is the account link's request body.

The hub answers `200`:

```json
{
  "requestId": "2c9e41d7-0000-4000-8000-000000000001",
  "userCode": "K7QM-29XD",
  "approveUrl": "https://hub.tailed39ba.ts.net/servers/pair?code=K7QM-29XD",
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

The request is bound to `publicKey`. The hub shows the code, the key fingerprint, the scope, the server's
name, hostname, version, OS, endpoint and the source address (and its tailnet node from WhoIs, if it asks) on
its approval screen. The dashboard shows the same code and fingerprint on its Settings page.

**The one-time token.** The hub's "Add by command" screen and the hub installer make tokens for the terminal
path (`manage.sh hub connect … --token-stdin`, section 8.4). Only the terminal path sends a token. The Settings
page never does: there the hub owner approves the code.

- A token is `jht_` followed by 43 base64url characters (32 random bytes). The hub stores only its SHA-256.
- It is single use: the first `POST /requests` that names it consumes it, whatever happens next.
- It expires at most one hour after it is made. The installer makes one that lasts 30 minutes.
- An unknown, used or expired token is answered `403 bad_token`, and **no request is created**. The dashboard
  reports this as a failure. It does not fall back to a code.
- A valid token means the hub owner has approved in advance. The hub creates the request already approved,
  unless it cannot approve yet (for example while it waits for a seat from its licence). Then the request is
  pending and is approved in the hub like any other. The dashboard cannot tell the two apart and does not need
  to: it polls as usual.
- The token never replaces the dashboard's own approval. The terminal path is root on that server, and the
  browser path is a server administrator pressing Connect.

Errors: `400 unsupported_protocol`, `400 bad_request` (a field breaks its rule), `403 bad_token`,
`413 too_large`, `429 rate_limited`.

The hub should accept `POST /requests` only from a tailnet address (it listens only on the tailnet). A public
key that already belongs to a linked server may ask again, for example after the dashboard lost its link state.
When that request is approved the hub ends the old server row, whose requests then get `410 server_removed`.

### 4.2 `GET /requests/{requestId}`

Signed with `X-JD-Server: <requestId>`. Polled every `pollSeconds` until it is decided or the request expires.
Only the process that started the request polls it (section 8.9).

- `200 {"status":"pending"}` until it is decided.
- Once approved, **exactly once**:

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
  | `heartbeatSeconds` | 10–600. The hub sends 15. The dashboard clamps it |
  | `pollWaitSeconds` | 1–25. The hub sends 25. The dashboard clamps it |
  | `owner.label` | The hub's name: `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`. The installer's default is `hub`. It becomes the audit actor `hub:<label>` |

  The request is then consumed. The dashboard stores the link in one transaction, switches `X-JD-Server` to
  `serverId` and keeps its sequence going.
- `410` with code `request_expired`, `request_denied` or `request_consumed` once it is over. `request_consumed`
  means the approval was handed out already; if the link is stored on the dashboard it is linked, and if not,
  the request has failed and a new one is needed. This is the account link's handling, unchanged.
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
| `siteKeys` | Optional. A key rotation announcement, exactly as in the account link ("Key rotation" in section 3 of that protocol): `[{"kid", "publicKey", "notAfter"}]`, with the stricter `kid` pattern. The hub publishes only keys from its own data directory's key files, never from its database. Just Hub 1.0 never rotates its key |
| `fleet` | Always present, possibly `[]`. Section 6.1 |
| `notices` | Always present, possibly `[]`. Section 6.2 |
| `signIn` | Optional. Present only when the hub offers hub sign-in. Section 6.3 |

The dashboard caches `fleet`, `notices` and `signIn` with the time it received them (section 8.5) and serves
them to its own pages. Nothing in the reply can change anything on the dashboard except the heartbeat interval
and the pinned keys.

**Rate.** The hub accepts at most 12 heartbeats per server in any 60 seconds and answers `429 rate_limited`
beyond that. The dashboard paces itself to the same 12 (the account link paces to 4). A manual Sync that would
break the pace is skipped and the status is returned as it was.

Errors: the signed-request errors of 3.1, `400 bad_request` (the body is not a JSON object, or `protocol` is not
`1`), `413 too_large`, `429 rate_limited`, `410 server_removed`.

### 4.4 `GET /commands?wait=25`

Signed. A long poll. `wait` is an integer from 1 to 25; the dashboard sends `pollWaitSeconds`. Anything else is
`400 bad_request`.

- `200 {"commands":[Command, …]}` as soon as at least one command is queued, with every queued command for
  this server up to 20.
- `200 {"commands":[]}` after `wait` seconds with nothing to send.
- One poll per server at a time. A newer poll replaces an older one, which answers `{"commands":[]}` at once
  without claiming anything.
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
| `403` | `bad_token` | `POST /requests` | The token is unknown, used or expired. Connect fails |
| `404` | `unknown_command` | result | Stop reporting this result |
| `404` | `not_found` | all | No such route. Treated as an unreachable hub |
| `410` | `request_expired`, `request_denied`, `request_consumed` | request poll | The request is over (4.2) |
| `410` | `server_removed` | signed | The hub removed this server. The dashboard clears its link (8.9) |
| `413` | `too_large` | all | The body is over 64 KiB. The dashboard never builds one (section 5) |
| `429` | `rate_limited` | all | Backoff |
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

## 6. The heartbeat reply: fleet, notices, sign-in users

These are what make a linked dashboard hub-aware: the server switcher, Servers in Ctrl K, the notice card and
the sign-in opt-in. The browser never talks to the hub; the dashboard's backend serves its cached copy.

The dashboard validates each entry. An entry that breaks a rule is dropped and the rest are kept, because
these lists are for display. A reply that does not decode strictly is a failed heartbeat (2.1).

### 6.1 `fleet`

At most 500 entries. Every server linked to the hub and not removed, including the one receiving the reply. A
server the hub has paused for its plan is listed like any other.

| Field | Type | Rule |
| --- | --- | --- |
| `serverId` | string | The hub's id for that server, `^[0-9A-Za-z][0-9A-Za-z-]{7,63}$` |
| `name` | string | 1–64 printable characters. The name the hub shows |
| `endpoint` | string, optional | The server's last reported `access.endpoint`. Left out when that server is reached by an SSH tunnel or has not reported one |
| `state` | string | `online`, `late`, `offline` or `updating` (below) |
| `version` | string | The server's last reported `server.version`, or `""` before its first heartbeat. At most 32 characters |
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
at most 255 characters. When the hub URL is under a MagicDNS suffix, the endpoint's host must be one label
under the same suffix. When the hub URL is the test origin, any `https` origin and any loopback `http` origin
is accepted. An endpoint that fails is removed from its entry; the entry stays, without a link.

### 6.2 `notices`

At most 20 entries, newest first. Short messages the hub wants every linked dashboard to show, such as an
update ready on some servers or a server offline.

| Field | Type | Rule |
| --- | --- | --- |
| `id` | string | `^[0-9A-Za-z][0-9A-Za-z._:-]{0,63}$`. Stable while the notice stands, so a dashboard can hide one a person dismissed |
| `level` | string | `info`, `warning` or `critical` |
| `title` | string | 1–120 characters, plain text |
| `body` | string | 0–1000 characters, plain text. The dashboard renders it as text, never as markup |
| `href` | string, optional | An absolute `https` URL on the stored hub URL's origin, at most 512 characters. Anything else is removed from the notice |
| `at` | int | Unix seconds the notice was raised |

A notice disappears from the dashboard when a later reply no longer carries it.

### 6.3 `signIn` in the reply

Present only when the hub offers hub sign-in (its own setting is on). Absent means the hub does not offer it.

```json
{ "signIn": { "users": ["owner"] } }
```

`users` lists the hub user names a dashboard account may choose to let sign it in: at most 50, each
`^[a-z0-9][a-z0-9._-]{0,63}$`. Section 9 uses it.

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
| `backup.run` | `{"jobId": int}` | operate | Backups' Run now. Reported when the run has started |
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
`https://vps-07749119-vps-ovh-net.tailed39ba.ts.net:8443/dashboard/hub?connect=hub.tailed39ba.ts.net`. The
dashboard page fills in `https://<hub host>`, shows the scope (default `maintain`) and exactly what the hub
will see and be able to do, and does nothing until a server administrator presses Connect.

### 8.2 `/api/v1/dashboard/hub`

| Route | Who | Does |
| --- | --- | --- |
| `GET /dashboard/hub` | Any signed-in principal (session or API key, any role). The `admin` part only for `system.admin` | The status below |
| `POST /dashboard/hub` `{"url", "scope"}` | `system.admin`, session only | Connect. Audited `hub.link.begin` |
| `DELETE /dashboard/hub/request` | `system.admin`, session only | Forget the pending request (`204`). Audited `hub.link.cancel`. The hub is not told; the request expires there |
| `PUT /dashboard/hub/scope` `{"scope"}` | `system.admin`, session only | Change the scope. Audited `hub.link.scope`. The next heartbeat tells the hub |
| `DELETE /dashboard/hub` | `system.admin`, session only, `s.destructive` | Disconnect (`204`). Audited `hub.unlink` with `hubNotified` |
| `POST /dashboard/hub/sync` | `system.admin` (an API key may) | A heartbeat now, then the status |
| `DELETE /dashboard/hub/signin/{userId}` | `system.admin`, session only | Remove one account's sign-in grant (`204`). Audited `hub.signin.revoke` (section 9) |

"Session only" means an API key is refused with `403 session_required` (`httpx.RequireSession`): an API key
must not be able to hand this server to a hub or take it back. The `GET` sits with the other every-role reads
of the `/dashboard` group; the rest sit in its `system.admin` part.

**Errors.**

| Status | `code` | When |
| --- | --- | --- |
| `400` | `bad_request` | `scope` is not `observe`, `operate` or `maintain`, or the body is malformed |
| `400` | `hub_url_invalid`, `hub_url_not_tailnet`, `hub_url_same_host`, `hub_not_on_tailnet` | The URL rule, 2.2 |
| `403` | `hub_link_disabled` | `JD_HUB_LINK=false` |
| `409` | `already_linked` | Connect while linked to a hub. One hub per server: disconnect first |
| `409` | `link_pending` | Connect while a request is waiting |
| `409` | `not_linked` | Scope, disconnect or sync with no hub link |
| `409` | `hub_url_refused` | Sync while the stored hub URL fails the rule now (2.2) |
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
      "approveUrl": "https://hub.tailed39ba.ts.net/servers/pair?code=K7QM-29XD",
      "expiresAt": "2026-11-05T10:15:00Z",
      "scope": "maintain"
    },
    "link": {
      "serverId": "7a0d3c1e-0000-4000-8000-000000000002",
      "linkedAt": "2026-11-05T09:00:00Z",
      "linkedBy": "ion",
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
| `fleet`, `notices` | everyone | The cached reply (section 6) with times as RFC 3339. `[]` before the first reply. Only when linked |
| `signIn.enabled` | everyone | `JD_HUB_SIGNIN` |
| `signIn.offered` | everyone | The last reply carried `signIn` |
| `signIn.users` | everyone | Its `users`, or `[]` |
| `signIn.mine` | everyone | The calling account's own grant, if it has one |
| `admin` | `system.admin` | Left out for everyone else |
| `admin.fingerprint` | | The hub link identity's fingerprint, once it exists |
| `admin.testOrigin` | | Only when `JD_HUB_LINK_TEST_ORIGIN` is set |
| `admin.request` | | When pending |
| `admin.link` | | When linked. `lastSnapshot` is the exact body of the last heartbeat sent, so the page shows what is shared and not a description of it. `linkedBy` is the account that connected, or `cli` |
| `admin.lastOutcome` | | When unlinked and the last request ended without a link: `denied`, `expired` or `failed` |
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

In Go this is a field `Hub *hubLinkSession` on `authStatus`, with the tag `json:"hub,omitempty"`. In TypeScript
it is `hub?: { linked: true; name: string; url: string; reachable: boolean }` on `AuthStatus`. The fleet and
notices come from `GET /dashboard/hub`, not from here, so this field stays small.

### 8.4 Terminal: `manage.sh hub`

```
sudo ./scripts/manage.sh hub connect URL [--scope observe|operate|maintain] [--token-stdin | --token TOKEN] [--wait 15m]
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
| `hub connect URL` | Applies the URL rule, starts a request at `--scope` (default `maintain`), prints the hub URL, the code and the fingerprint, and waits up to `--wait` (default 15 minutes, at most 1 hour) for approval. With a token the approval usually arrives on the first poll. On success it prints `Connected to Just Hub "<name>" at <url> as "<server name>" (<scope>).` | `0` linked. `2` declined, expired, not approved within `--wait`, or interrupted; the request is forgotten. `1` anything else: switched off, already linked to a hub, a request already pending from Settings, the URL refused, the hub unreachable, `bad_token`, `JD_MASTER_KEY` missing |
| `hub disconnect` | Tells the hub (`POST /unlink`) and clears the link. Prints whether the hub was told | `0` disconnected. `1` not linked, or the store could not be written |
| `hub status` | One line: not connected (and how the last request ended), the pending code, or connected to whom, at which scope, the last heartbeat and the last error. Says so when `JD_HUB_LINK=false` | `0` printed. `1` the store could not be read |

`--token-stdin` reads one line from standard input, which is how the hub installer passes its token, so it
never appears in a process list. `--token TOKEN` is accepted for people typing it. A request started at the
terminal is polled only by that command; one started in Settings is never taken over from the terminal, as with
the account link. Audited as `hub.link` (or `hub.link` failed, with the outcome) and `hub.unlink`, by `root` /
`cli`.

### 8.5 Storage

All link state is in the `settings` table, under `hub.link.*`. Only the identity is secret, and it is sealed.

| Key | Holds |
| --- | --- |
| `hub.link.identity` | The Ed25519 seed, sealed with `auth.Sealer` |
| `hub.link.state` | The link: hub URL, `serverId`, pinned hub keys, scope, who linked and when, the hub's name, the server's own name, intervals |
| `hub.link.request` | The pending request: id, code, approval address, expiry, scope, who asked, which process polls it |
| `hub.link.sequence` | The request counter. One for the life of the identity, never reset |
| `hub.link.update-policy`, `hub.link.update-attempt` | The update policy the hub set, and the last version it was tried for |
| `hub.link.outcome`, `hub.link.status` | Why the last request ended; the last heartbeat time and the last heartbeat and poll errors |
| `hub.link.view` | The last heartbeat reply's `fleet`, `notices` and `signIn`, with the time received. Not secret, cleared with the link |

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
| `JD_HUB_LINK` | `true` | `false`: no hello route, no connect, no terminal connect, no `hub` field in `/auth/session`, and the connector runs nothing. An existing link stays in the store, idle; disconnect still works and then tells no one. Independent of `JD_ACCOUNT_LINK` |
| `JD_HUB_SIGNIN` | `false` | The operator's switch for hub sign-in (section 9). Off, tickets are refused, grants cannot be made, and the heartbeat carries no `signIn` |
| `JD_HUB_LINK_TEST_ORIGIN` | unset | Tests only, section 2.3 |

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
  polled by the running dashboard; one started at the terminal is polled only by that command.
- **Linked.** Four loops: heartbeat, command poll, update policy (every minute), and a watch that ends the loops
  within 5 seconds when the stored link changes (a disconnect or connect from the terminal). Nothing needs a
  restart.
- **Disconnect** (Settings or terminal): `POST /unlink`, signed, one attempt. Then, in one transaction, the link
  state, request, status, policy, policy attempt, outcome and `hub.link.view` are deleted, and so are every
  sign-in grant and unused ticket record (section 9). The identity and the sequence stay, so a later link shows
  the same fingerprint.
- **Removed in the hub** (`410` on any signed request): the same clearing, audited as `hub.unlink` by `system`
  with the reason "the server was removed in the hub". The Settings page says so once.
- **Identity unreadable** (`JD_MASTER_KEY` changed): an unlinked install makes a new identity; a linked one
  reports the error and needs a disconnect and a new link ("re-link needed").

## 9. Hub sign-in (version 1, off by default)

Hub sign-in lets a person open a server from the hub and land on it already signed in. The hub makes a
single-use ticket that lasts at most 60 seconds, signs it with the hub key, and opens the server's
`/hub/enter` page with the ticket in the URL fragment. The dashboard checks the ticket and starts a session.

### 9.1 The exception to invariant 2

Invariant 2 says an account with an authenticator is always asked for a code on this server. A hub ticket
starts a **complete** session (`twofa_passed = 1`) without that code. The second factor that stands in for it is
the hub user's own TOTP check on the hub, at most 10 minutes before the ticket was made, which the hub states in
the ticket (`mfaAt`) under its pinned key.

This is a deliberate, documented weakening, and it is allowed only when all of these hold:

1. **The operator signed off.** `JD_HUB_SIGNIN=true` is set by root on this server. It defaults to `false`.
   Before a release turns the code on anywhere, a written security review of this section against
   [invariant 2](../security/invariants.md#invariant-2-what-two-factor-still-guarantees) must be done and
   recorded, and invariants.md must name this exception in its invariant 2 section.
2. **The account holder opted in**, while fully signed in on this server, by entering their password again and,
   if the account has an authenticator, a current code (section 9.3). Each account decides for itself.
3. **The grant names one hub user.** Only that hub user's tickets can sign in as that account.
4. **The hub's check is fresh.** `iat − mfaAt` is at most 600 seconds, and the hub must have asked for the code
   itself (section 10).
5. **Nothing on this server says no.** The account is not disabled, not locked, does not owe a password change,
   and is not an unenrolled account on a server with `JD_REQUIRE_2FA=true`.

What it costs, plainly: with hub sign-in on, anyone who takes over the hub can open a session as every account
that opted in. For an administrator that means everything the dashboard can do, including the terminal. Without
hub sign-in, a taken-over hub is limited to the scope's commands. That is why it is off by default and needs
both the operator and the account holder.

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
| `PUT /account/hub-signin` `{"hubUser", "password", "code"}` | Make or replace the caller's grant. `code` is required when the account has an authenticator. Both are checked against the account's shared guess budget (`spendGuess`). `204`. Audited `hub.signin.allow` |
| `DELETE /account/hub-signin` | Remove the caller's grant. No password needed. `204`. Audited `hub.signin.revoke` |

The page reads the caller's own state from `signIn` in `GET /dashboard/hub` (8.2); there is no second read
route.

`PUT` refuses with `403 hub_signin_disabled` when `JD_HUB_SIGNIN` is off, `409 not_linked` with no hub link,
`409 hub_signin_not_offered` when the last heartbeat reply had no `signIn`, `400 bad_request` when `hubUser` is
not in its `users`, `401 invalid_credentials` for a wrong password, `401 invalid_code` for a wrong code, and
`409 hub_user_taken` when another account on this server already granted that hub user. Administrators remove
anyone's grant with `DELETE /dashboard/hub/signin/{userId}` (8.2).

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

**A grant ends** when the account removes it; an administrator removes it; the hub link ends (disconnect,
removal or a new link); the account is deleted or disabled; its password changes or is reset; its
authenticator is reset by an administrator or turned off; its sessions are all revoked; or `JD_HUB_SIGNIN` is
found off at start (all grants are deleted then, so turning the switch back on does not bring old grants back).
Each of these deletes the row in the same transaction as the change.

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
| `nonce` | string | 16 random bytes, base64url without padding: `^[A-Za-z0-9_-]{22}$` |
| `iat` | int | Unix seconds when the hub made it |
| `exp` | int | Unix seconds. `iat < exp ≤ iat + 60` |
| `mfaAt` | int | Unix seconds of the hub user's last successful TOTP check on the hub. `iat − 600 ≤ mfaAt ≤ iat` |
| `next` | string, optional | A path on the dashboard to open after sign-in. Matches `^/[A-Za-z0-9._~!$&'()*+,;=:@%/?-]{0,511}$` and does not start with `//`. Default `/dashboard` |

The dashboard verifies the signature over the exact `payloadSeg` bytes before it parses any JSON, so the hub
may encode the payload with any JSON encoder.

### 9.5 Transport

The hub opens, in a new tab, a top-level navigation to:

```
<endpoint>/hub/enter#ticket=<ticket>
```

`<endpoint>` is the server's last reported `access.endpoint`. The ticket is only ever in the fragment: browsers
do not send a fragment to the server, so it does not reach access logs, proxies or a `Referer`. The hub must not
put a ticket in a query string, a log, a redirect or its own activity log.

`/hub/enter` is a dashboard page outside the signed-in shell, like `/login`. It must:

1. Read `location.hash`, take `ticket=`, and at once call `history.replaceState(null, "", "/hub/enter")` so the
   ticket leaves the address bar and the history entry.
2. `POST /api/v1/auth/hub/enter` with `{"ticket": "<ticket>"}` and the header `X-JD-CSRF: 1`.
3. On `200`, navigate to the response's `next` with `location.replace`.
4. On an error, show the message, a link to `/login`, and nothing else. It never retries a ticket.

### 9.6 `POST /api/v1/auth/hub/enter`

Inside `/api/v1`, behind the allowlist, in the same group as `POST /auth/login`: the sign-in rate limit per
address (`loginLim`), `httpx.RequireCSRF`, and its own audit entry (the handler records it and calls
`SkipAudit`). Not authenticated: the ticket is the credential. Body `{"ticket": string}`, at most 4 KiB.

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
| 2 | A hub link is stored and the connector has not set it idle under the URL rule (2.2) | `403 hub_not_linked` |
| 3 | The body decodes; the ticket is at most 2048 characters, splits on `.` into exactly four parts, the first is `v1`, the second matches the kid pattern, the other two are non-empty base64url | `401 bad_ticket` |
| 4 | The `kid` names a hub key pinned in the link state and not past its `notAfter` | `401 bad_ticket` |
| 5 | The signature decodes to 64 bytes and verifies over the signed string (9.4) with that key | `401 bad_ticket` |
| 6 | The payload decodes from base64url, is one JSON object with exactly the fields of 9.4 and their types, `v` is 1, `typ` is `hub.signin`, `userId ≥ 1`, `mfaAt ≥ 1`, `hubUser`, `nonce` and `next` match their patterns, `iat < exp ≤ iat + 60` | `401 bad_ticket` |
| 7 | `hub` equals the stored hub URL and `serverId` equals the link's `serverId` | `401 wrong_server` |
| 8 | With `now` the dashboard's clock: `iat − 30 ≤ now ≤ exp + 30` | `401 ticket_expired` |
| 9 | `mfaAt ≤ iat` and `iat − mfaAt ≤ 600` | `401 mfa_stale` |
| 10 | The `nonce` is recorded as used (9.8). If it was already there | `401 ticket_used` |
| 11 | A grant exists for `userId`, its `hub_user` equals `hubUser`, and its `server_id` equals the link's `serverId` | `403 not_permitted` |
| 12 | The account exists, is not disabled, is not locked (neither `locked_until` nor `totp_locked_until` is in the future), does not owe a password change, and is not an unenrolled account while `JD_REQUIRE_2FA=true` | `403 account_unavailable` |
| 13 | A session is made (9.9), the cookie is set, and `200` is answered | `500` |

The 30-second grace in step 8 covers a small clock difference between the hub and the server. The longest a
ticket can be accepted is therefore 120 seconds after it was made, and only once. Step 10 comes after the
signature and time checks, so a forged or stale ticket cannot use up a real one's nonce, and before the grant and
account checks, so a ticket that reached step 10 is spent even if it is then refused.

### 9.8 Single use

```sql
CREATE TABLE IF NOT EXISTS hub_signin_tickets (
  nonce      TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL,
  used_at    INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
);
```

Step 10 is one `INSERT … ON CONFLICT(nonce) DO NOTHING`; zero rows affected means `ticket_used`. Rows are pruned
a day after `expires_at` (`exp + 30`), far past the time a ticket could be accepted. The rows are cleared with the
hub link.

### 9.9 The session

Made through `auth.Service` like every other sign-in, with `twofa_passed = 1` and `auth_method = 'hub'`. The
normal limits apply: 12 hours at most, 60 minutes idle. `last_login_at` is stamped. If the request carried a valid
session cookie for this browser, that session is revoked first, so one browser does not hold two sessions. The
account's session list shows the method as signed in through Just Hub, with the hub user.

### 9.10 Audit

| Action | When | Username / actor | Detail |
| --- | --- | --- | --- |
| `hub.signin` (success) | A ticket was redeemed | The account / `hub:<hub name>` | `hubUser`, `kid`, `nonce`, `iat`, `mfaAt`, `sessionId` |
| `hub.signin` (failure) | Any refusal | Username: the account once step 11 found it, else `anonymous`. Actor: `hub:<hub name>` once step 5 passed, else `anonymous` | `code`, and `hubUser`, `userId` and `nonce` once step 5 passed |
| `hub.signin.allow` | An account made or replaced its grant | The account | `hubUser` |
| `hub.signin.revoke` | A grant ended, for any reason in 9.3 | The account, an administrator, or `system` | `hubUser`, `reason` |

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
| The account removed its grant, or the grant was for an earlier link | `not_permitted` |
| The account is disabled, locked or must change its password | `account_unavailable`. Its own sign-in page is the way in |
| The operator never turned it on, or turned it off | `hub_signin_disabled`; all grants were deleted at the start after it went off |
| The hub is reinstalled with a new key | Every ticket fails at step 4 until the server is linked again; the grants were cleared with the old link |
| A browser extension or proxy reads the fragment | It can use the ticket once, within 120 seconds, from inside the allowlist. This is the residual risk of the fragment transport |

### 9.12 Before turning it on

The security review that signs off on 9.1 checks at least: that the hub asks for its own TOTP code within the
10 minutes (section 10); the verification order and its tests, using the ticket vectors; that grants end in every
case listed in 9.3; that the redeem route is rate limited and audited; that `/hub/enter` drops the fragment
before any other script can run and loads nothing from another origin; and that the user-facing text on the
opt-in screen says what the hub can then do.

## 10. What the hub must do

This list is for the hub implementation. Each item is stated above; this is the checklist.

- Serve the six routes of section 4, and nothing else under `/api/hub/v1`, on its tailnet name with a valid
  certificate.
- Check signed requests in the order of 3.1, with the ±60 s window and an atomic sequence compare-and-store.
- Keep request ids and server ids apart (3.1 step 2). Answer `410 server_removed` to a removed server.
- Bind each request to its public key. Approve only after the hub owner compares the code, or on a valid
  one-time token. Store tokens as hashes; make them single use and at most an hour long.
- Sign every command with the pinned key over the receiving server's `serverId`. Use ULIDs and a 10-minute
  expiry. Never send a command outside the scope the server last reported.
- Deliver commands only through the long poll, one poll per server, and keep results.
- Reply to heartbeats with `fleet` and `notices` that follow section 6, and accept 12 a minute per server.
- Never reveal plans, seats or pauses to a dashboard.
- Should confirm a request's or heartbeat's source address with WhoIs, and show it on the approval screen.
- For sign-in: offer it only when its own setting is on; issue a ticket only for a hub user with a complete
  session whose last TOTP check on the hub is at most 10 minutes old (else ask for a code first), only to a server
  whose latest heartbeat carried a grant for that hub user, with a fresh random nonce and `exp ≤ iat + 60`; put the
  ticket only in the fragment of a top-level navigation; never log it.
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
| A key announced in a heartbeat | Added if it is a 32-byte key with a valid `kid`; a pinned key's material is never replaced; keys past `notAfter` are dropped, never the last one |
| `JD_MASTER_KEY` changed | The identity cannot be opened. An unlinked install makes a new one; a linked one says "re-link needed" |
| The server moved to another tailnet, or the test origin was removed | The connector goes idle and says why (`hub_url_refused`). Disconnect, then connect to a hub on this tailnet |
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
| `signInTicketContext`, `signInTickets` | Redeem `ticket` against the context (pinned kid and key, hub origin, `serverId`) at `now`, through steps 3 to 10 of 9.7; the result must be `expect`. The `valid` case redeemed a second time must give `secondRedeem` (`ticket_used`). Steps 1, 2 and 11 to 13 need a real dashboard and are not in the file |
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
The ticket vectors were checked by an independent implementation of 9.7, since the dashboard has no ticket code
yet.

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
