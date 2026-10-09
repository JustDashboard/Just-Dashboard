# Controlled external reachability checks

Just Dashboard remains a control panel for one Linux server. Optional controlled probe agents poll
outbound through the dashboard's existing Caddy HTTPS origin. They create no listening port and have
no dashboard user, shell, Docker, files, firewall or host-service capability. This protocol is separate
from the existing root-equivalent `JD_AGENT_MODE` host-remoting feature.

The Network → External checks report compares retained DNS, TCP and optional TLS evidence from
explicitly enrolled sources. A successful TCP stage means **connected from this source to this exact
address and port at this time**. Verified TLS additionally means that the agent's native trust store
accepted the certificate and service hostname on that same connection. Neither proves HTTP behavior,
authentication, UDP, universal inbound reachability, a provider cause or the correctness of the
operator's geographic label. A controlled namespace fixture is identified as a fixture; it is never
represented as an off-host or regional measurement.

## Identity and ingress

All management and evidence routes under `/api/v1/network/external` require `system.admin` on the
backend. Enrollment and revocation additionally require an authenticated human session; revocation
uses the existing destructive guard. Dedicated `/api/v1/probe-agent/{enroll,poll,result}` routes run
behind the existing network allowlist and their own rate limiter, before human authentication. They
recognize only this protocol's one-use enrollment proof or signed machine requests. Dashboard user
tokens and cookies cannot substitute for a machine proof, and machine credentials cannot authorize
any dashboard feature. Every mutation is audited. Tokens, private keys, signature headers and raw
machine bodies are excluded from audit payloads.

The operator must already authorize the agent's outbound source through the dashboard's normal
allowlist controls. Enrollment does not add a CIDR, change Caddy or expose the backend. Keep the
control-plane URL an HTTPS origin; redirects, embedded credentials, query strings and alternate
paths are refused. The agent requires both normal TLS certificate validation and an explicitly
trusted leaf public-key SPKI SHA256 pin. It also pins the server's Ed25519 job-signing public key.
A pin change requires operator review; the agent never falls back to insecure TLS.

The backend seals its server signing key in the existing settings store. Each agent creates a new
Ed25519 identity. Machine signatures bind the server signing identity, agent identity, HTTP method,
full path, body digest, timestamp and sequence. Requests must be within 60 seconds of server time
and have a sequence strictly above the durable accepted counter. The agent saves and fsyncs its
sequence **before transmission**, so a restart or lost response cannot replay the old request.

Identical credential and database clones represent the same cryptographic identity. Signatures
reject another installation with a different signing key; they cannot distinguish byte-for-byte
copies of the same key and replay state. An intentional clone must explicitly reset this optional
probe identity and re-enroll agents before use. Do this offline with a reviewed database backup:
revoke all old vantage identities, invalidate their enrollments, cancel queued/running checks and
remove only `settings['network.probe.signing-key']` in one transaction. Restarting then creates a new
server signing key. Re-enroll agents with that new public key and the new origin's trusted TLS pin.
For this deliberate reset, stop the dashboard backend and all old pollers first. Take an offline
backup of the configured data directory (including `vpsd.db`, WAL/SHM files and the existing master
key); verify that this is the clone being reset. On that stopped clone only, run these statements
against its `vpsd.db` with SQLite. The old evidence remains attached to revoked sources:

```sql
BEGIN IMMEDIATE;
UPDATE network_probe_vantages
  SET revoked_at=CAST(strftime('%s','now') AS INTEGER)*1000,
      enrollment_expires=0,
      enrollment_hash='reset:' || id;
UPDATE network_probe_checks
  SET status='cancelled', completed_at=CAST(strftime('%s','now') AS INTEGER)*1000
  WHERE status IN ('queued','running');
DELETE FROM settings WHERE key='network.probe.signing-key';
COMMIT;
```

Restart the clone, create fresh enrollments in its session-only management UI, and enroll each source
with a **new private state file**, the clone's HTTPS origin, its trusted TLS SPKI pin and the freshly
returned server signing public key. Retire old private state from every source. The original
installation stays untouched. An ordinary upgrade or recovery restores its existing sealed key
and never runs this reset procedure.

Restore a damaged sealed key from a matching backup instead of silently replacing it. The service
fails closed if its key cannot be opened; agent state and manifest failures also fail closed.

## Enrollment and scopes

An administrator creates an enrollment from the report or `POST /network/external/enrollments`:

```json
{
  "name": "Controlled source A",
  "location": "Operator-declared region A",
  "placement": "external_host",
  "scopes": [{
    "id": "service",
    "target": "service.example",
    "addresses": ["192.0.2.8", "2001:db8::8"],
    "ports": [443],
    "families": ["inet", "inet6"]
  }]
}
```

These documentation addresses are examples, not routable test services. Use only a service and
sources you control. Each immutable enrollment has one to eight scopes, each with one exact DNS
name or literal address, up to sixteen exact unicast addresses, up to eight TCP ports and selected
IPv4/IPv6 families. No CIDR, wildcard, URL, command, arbitrary tool or range scan is supported.
Every selected family needs an approved address. Changing a scope means revoking and enrolling a
new identity; the agent's private manifest independently refuses a signed job that expands it.

The session-only enrollment response uses `Cache-Control: no-store` and exposes its ten-minute,
one-use token once. Inventory, retained checks and audit records contain no enrollment token or
private key. The token is stored as a hash; a claimed enrollment receives a unique nonsecret used
marker. SQLite transactions and constraints enforce one-time claims, unique public identities and
job nonces, monotonic request counters and single result acceptance under concurrent requests.
No PID, executable path or shell capability is persisted.

Build the standalone rootless executable with the repository's Go 1.26.8 toolchain:

```bash
cd backend
GOTOOLCHAIN=go1.26.8 go build -o /trusted/local/path/jd-vantage ./cmd/vantage
```

On the controlled source, provide the token through standard input, never an argument:

```bash
jd-vantage -enroll -state /private/local/path/probe.json \
  -url https://dashboard.example \
  -id PUBLIC_VANTAGE_ID -server-key PUBLIC_SERVER_SIGNING_KEY \
  -tls-pin TRUSTED_TLS_SPKI_SHA256 < /private/local/path/one-use-token
jd-vantage -state /private/local/path/probe.json
```

The state file must be regular, private (`0600`) and at most 32 KiB; its directory must be controlled
by the agent account. It contains the agent's private identity, pinned manifest and durable sequence.
Store and back it up as a credential, and remove the one-use token file after successful enrollment.
Do not clone active agent state into a second process or host. Enrollment, the long-running poller and `-once` share an exclusive private-state lock. A second
process is refused before transmitting or changing the sequence; each execution reloads the durable
state under that lock. `-once` is intended for a stopped agent's controlled diagnostic run. Run as an
unprivileged account with only outbound HTTPS, DNS and the approved service access it requires.
The dashboard neither installs this executable remotely nor persists a remote shell capability.

To derive a TLS SPKI pin from a certificate already obtained through a trusted channel:

```bash
openssl x509 -in trusted-dashboard-certificate.pem -pubkey -noout |
  openssl pkey -pubin -outform DER |
  openssl dgst -sha256
```

A certificate fetched through an unverified network connection is not an out-of-band trust decision.
Clock synchronization is required for request and lease acceptance.

## Execution, retention and evidence

`POST /network/external/checks` selects an enrolled vantage, scope ID, family, exact port and TLS
boolean. Admission persists a queued check and returns 202. It is not connectivity evidence. There
is at most one outstanding check per source, at most 32 retained sources and 256 retained checks.
The agent polls every ten seconds. A queued check expires after two minutes; polling leases it
once for at most 45 seconds. It is never silently re-leased or re-probed after a lost result, agent
restart or backend restart.

The native Go probe has a 25-second total bound: selected-family absolute DNS query, one approved
literal TCP connection and optional TLS on the same connection. All returned DNS candidates must
fit the approved address set before TCP is attempted. At most eight candidates are retained; one
is chosen and pinned. The agent never forwards a private query to a dashboard-selected public
resolver, but its own resolver configuration determines the upstream. Full NSS, search behavior
and encrypted resolver policy are not traced. The report retains candidates, selected destination,
actual local source address, family, exact port, stage evidence bases, agent timestamps, server
receipt time and a verified certificate fingerprint when present. Labels clearly identify signed
**agent-reported** measurements; a signature authenticates the reporter, not a truthful or accurately
located remote machine.

Accepted results must bind the authenticated identity, leased job, nonce, exact tuple, address
scope, fixed DNS/TCP/TLS stage order, timestamps and size limits. Expired, cancelled, revoked,
replayed, altered or unrelated results are rejected. Cancellation rejects late acceptance; an
already executing agent probe may continue until its fixed local deadline. An offline agent or a
lost upload expires with **unknown** reachability, not a failed measured TCP or TLS stage. Seven-day
retention is enforced on access and bounded by the row caps. Evidence reads do not initiate probes.
Comparison requires retained results for the same target, port and TLS setting, and preserves the
source/family/time distinctions rather than assigning a generic Allowed or Blocked verdict.

The machine request and response limit is 32 KiB (enrollment claims 4 KiB). The protocol is a fixed
closed vocabulary; there is no agent API to execute a named program or arbitrary command. Request
content is authenticated before probe-result JSON is decoded. The service has no background scan,
implicit retry probe, auto-enrollment, automatic public resolver fallback or remotely installed
listener.

## Use from the ports page

`GET /api/v1/ports/external` (system.admin) joins retained checks to their sources by host port for the
ports sheet of an exposed socket: what each source measured (connected, refused or failed at an address
on this host or one that is not), checks that ended unmeasured as unknown, and a "Check from" action for
every enrolled scope naming that port in the socket's family, which posts to `/network/external/checks`
and reads again until the source answers. The inbound path of a Docker-published port shows the same
measurements as its last layer. Neither starts a probe on its own.

## Verification and outstanding acceptance

Focused unit/API checks cover additive fresh/0.6.6 database upgrades, private state and failed keys,
cross-server identity binding, signature body/path binding, concurrent enrollment/replay/result
acceptance, scopes, lease expiry, cancellation, revocation and evidence boundaries. Native socket
fixtures cover DNS → pinned TCP → verified TLS for both IPv4 and IPv6, out-of-scope DNS refusal and
untrusted TLS failure.

```bash
cd backend
go test -race ./internal/netvantage
go test ./internal/store -run NetworkProbe
go test ./internal/api -run 'Test(External|Machine)'
JD_NETVANTAGE_LIVE=1 go test ./internal/netvantage \
  -run '^TestControlledVantageSeparateNamespace$' -count=1 -v
```

The opt-in fixture needs a non-root contributor account with passwordless namespace setup through
sudo. It creates only its uniquely named loopback namespace, drops to the contributor account and
runs native IPv4/IPv6 DNS/TCP/TLS and identity checks there. It removes only that owned namespace.
This proves a separate controlled namespace, not a remote host or Internet region.

Actual off-host and regional acceptance remains pending until the operator supplies explicitly
enrolled controlled hosts and service scopes. No third-party credentials, regional agent fleet,
provider attestation, reachability guarantee or external-host result is fabricated by these fixtures.
