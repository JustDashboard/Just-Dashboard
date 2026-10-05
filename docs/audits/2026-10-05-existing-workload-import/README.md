# Existing-workload import overhaul acceptance

This record covers the environment-name, automation, source recovery, native-manager and existing-domain
overhaul following the first existing-workload import release. Evidence comes from isolated owned
fixtures on a real Docker host, public API calls and a production frontend build. No user workload was
restarted, modified or removed to produce it. Fixture names, IDs and environment values are synthetic.

## Root causes and resulting behavior

| Finding | Fix |
| --- | --- |
| Captured per-service environment values appeared as random `JD_IMPORT_ENV_*` variables. | Storage aliases stay internal. Import and settings show original names and service provenance, retain sealed values, distinguish application inputs from inherited image defaults, and require deliberate replacement, including empty values. |
| Dashboard reference syntax and Compose dollar interpolation could reinterpret application values. | Additive literal/reference intent preserves historical inference while new captured and dotenv values stay literal. Effective Compose values round-trip through the real parser with dollars, reference-shaped text, quotes, backslashes, newlines and empty strings intact. |
| Synthetic native argument values could corrupt an original-manager rollback. | The immutable native environment remains independent of desired argument translations; server-derived binding guards apply only to actual captured authority. |
| Absent Compose declarations became unintended deployment targets. | New imports default to existing running and stopped containers. Missing declarations are named exclusions; retained dependencies on an excluded service block the plan. Explicit all-services scope remains available. |
| A preserved runtime image was treated as the only future build source. | Verified Compose contexts and native source trees receive immutable private snapshots, independent of the original image or native baseline. Node/Next.js and Python detection prepares supported build settings from bounded verified evidence. Unreproducible builds retain the pinned image with an explicit reason. Future local source attachment preserves reviewed data exclusions. |
| Native saved startup authority could restart the old application after migration. | Prepare a sealed targeted handoff for exact PM2 saved entries or direct systemd target links. Adoption leaves them unchanged. Later cutover retires only verified authority; failed deployment and rollback restore it, including repeated retire/restore cycles. Shared or ambiguous launchers block. |
| Domain recovery assumed a new dashboard-owned route or missed existing proxy behavior. | Existing nginx/Caddy bindings retain hostname, path, service and original ownership. Stable ports and verified aliases remain unchanged. Isolated literal-IP upstreams receive reversible upstream-only retargeting during later Deploy changes. Unsupported or unreadable known bindings block before stopping the original application. |
| Recovery repeated expensive image work or applied incorrect writable-layer exemptions. | Bounded authoritative capture and immutable n8n evidence reuse keep mutable state fresh. Python bytecode exemption verifies the unchanged source/header rather than ignoring arbitrary writable data. Source snapshot retention tracks references and safely collects orphaned private snapshots. |
| Review duplicated identical warnings and operational views falsely reported no storage/domain. | Grouped review retains every evidence-bound acknowledgement. Runtime views read actual live Compose mounts and external bindings, preserve per-container storage owners and distinct route paths, and mark unavailable evidence explicitly. Domains settings do not mark preserved external routes as removed. Native rollback history does not duplicate one manager's current runtime. |

Adoption records the current deployment without starting a deployment run or changing its runtime.
Desired settings apply only when the operator later uses **Deploy changes**. Recovery automatically
reuses a verified recent successful backup job when it covers all writable data; it does not create or
run a backup implicitly. Image/configuration rollback does not restore database or file changes.

## Real screenshots and recording

The browser recording uses a real four-container Compose fixture, authenticated isolated backend,
actual discovery/recovery/adoption APIs and live Docker stats. Assertions compare container IDs, PIDs,
start times, restart counts, configuration/mount/network digests and the original service data before
and after. No frontend API mocks are used in this recording.

- [Watch the real Compose import](real-compose-import.webm).
- [Original service-specific environment names](native-original-inputs-1280.png).
- [Adoption review, 1280px](native-adoption-review-1280.png), [1720px](native-review-1720.png).
- [Adopted project](native-project-1280.png).
- [Live resource readings](native-runtime-1280.png), [verified service storage](native-storage-1280.png).
- [External path routes and shared storage UI fixture](imported-domains-storage-1280.png) (mocked API).
- [Editable preserved Compose source](native-settings-1280.png).
- [Before/after browser continuity assertions](native-continuity.json).

The `native-*` filenames are retained from the existing opt-in recording harness; this particular
recording imports Docker Compose. Real PM2/systemd lifecycle evidence is separate below. UI fixture
tests additionally cover Next.js source classification, linked external domains, multiple paths on one
hostname, shared storage owners and deliberate empty-value edits; mocked fixtures are not live domain
or framework acceptance proof.

## Acceptance evidence

| Lane | Result and evidence |
| --- | --- |
| Public API adoption continuity | Four containers, two running and two intentionally stopped; 202 HTTP requests, zero failures; configuration, source Compose and persistent bytes unchanged; zero deployment runs. [Report](api-import-continuity.json). |
| Standalone Docker | Adoption, failed replacement compensation, successful managed deployment and original baseline rollback. [Report](docker-container.json). |
| Deleted Docker image | Read-only running-filesystem recovery, managed deployment and baseline rollback. [Report](docker-deleted-image.json). |
| Compose all services | Stopped replicas, absent declaration, unrelated one-off container, environment-file/alias values and persistent data across failure, deployment and rollback. [Report](compose-all-services.json). |
| Compose existing services | Excluded unavailable declaration remains excluded across failure, deployment and baseline rollback. [Report](compose-existing-services.json). |
| Real n8n | Workflow SQLite data and encrypted credential decryption survive adoption, deployment and rollback; original volume retained. Capture 10.084s, recovery 7.983s in this run. [Report](n8n-lifecycle.json). |
| PM2 and systemd | Real builds, deliberate failed candidate, restored native startup authority, later Docker migration, logs/settings/data/UID continuity and original-manager rollback. PM2 67.80s; systemd 141.09s. [Report](native-lifecycles.json). |
| Existing nginx and Docker Caddy | Real HTTPS/authenticated path traffic, multiple services, stable alias/port continuity, IP retarget, rollback, failed reload compensation, stopped original identity and conflict refusal. [Report](proxy-continuity.json). |
| Public deployment engine API | 29/29 assertions over managed deployments, failure diagnosis, signed webhook notifications, limits, blueprint isolation, secrets and database connectivity. [Report](engine-api.json). |
| C4 artifacts / C5 activation | Six artifact-adapter and four activation cases passed locally, using unique owned fixtures. |
| Representative detected framework builds | `next`, `next-standalone` and `fastapi` live builds and HTTP serving passed (207.06s total). |

The final changed-file gate passed formatting, lint, type checking, 2,959 logic tests, selected Go
checks and 582 browser cases; its opt-in live recording passed separately. Race tests covered seven
packages, with the deployment package rerun after the final native changes. Source commits, unchanged
code comparisons, corrected browser assertions and artifact digests are recorded in
[verification.json](verification.json).
Private raw logs, session ready files and traces containing authority are deliberately excluded. The
proxy report retains its raw-log digest and explicit fixture limitations. Evidence gathered before
final operational-view changes remains specific to the unchanged lifecycle paths it tested.

## Support boundaries

- A Docker application can remain on its verified pinned image regardless of framework. A source build
  is enabled only when the actual context, interpreter and build inputs can be verified; detection
  does not execute application JavaScript or invent a Git repository.
- PM2/systemd imports require supported single-application runtime and startup authority. Host-specific
  native modules, shared dependencies, incompatible interpreters, ambiguous launchers or source drift
  remain explicit blockers or review limitations rather than silently changing the application.
  Installed dependency code remains in the original-manager source fence even when excluded from
  desired build snapshots. Unverifiable external dependency/storage symlinks block adoption. Retained
  source files/directories must remain readable/traversable by the preserved runtime UID after Docker
  copies them; recovery never broadens the original permissions or changes the UID to bypass this.
- Existing domain discovery requires authoritative active and persisted configuration. nginx and
  supported Caddy configurations are covered. Traefik labels are unverified hints, and manager presence
  alone does not establish a hostname. Unknown/shared/dynamic routes require external handoff.
- TLS/authentication and surrounding proxy bytes stay with the original proxy. The operational summary
  does not claim to validate an external certificate or issue one. Fixtures use test certificates and
  loopback traffic; no public DNS or public ACME issuance is claimed.
- WebSocket configuration and Upgrade-header bytes are preserved; these fixtures do not prove a full
  WebSocket session. Non-isolated multi-upstream Caddy literal-IP rewrites block automatically.
- Single-file bind mounts retain their inode with bounded in-place writes and before/after identity and
  byte checks. Those checks cannot atomically exclude an external writer racing between them.
- Immutable native/Docker baseline rollback restores runtime configuration and reuses original storage.
  Data recovery requires its own verified backup; rollback never pretends to undo data/schema changes.

These limits are part of the shipped behavior. An unsupported case must leave the live workload
unchanged and explain which evidence or configuration is needed.

## Reproduction

Use the commands and prerequisites in [CONTRIBUTING.md](../../../CONTRIBUTING.md#before-you-open-a-pull-request). Run from the
task worktree; preserve the normal umask because fixtures assert file modes. Use Bun in `frontend/`.

```bash
cd frontend
bun run build
bun run start --hostname 127.0.0.1 --port 43161
```

With that fresh production server up, run the final gate from the repository root:

```bash
JD_BROWSER_BASE_URL=http://127.0.0.1:43161 GOFLAGS='-p=2' GOMAXPROCS=2 \
  scripts/test-changed.sh patch/0.7.1
```

Run the required race lane from `backend/`:

```bash
GOMAXPROCS=2 go test -race -p 2 \
  ./internal/deploy ./internal/api ./internal/proxysvc ./internal/backups ./internal/store -count=1
```

For the real browser recording, create private ready/evidence directories, then run the owned API
fixture from `backend/` in a separate terminal:

```bash
mkdir -p /tmp/jd-import-proof-server /tmp/jd-import-proof-recording
chmod 700 /tmp/jd-import-proof-server /tmp/jd-import-proof-recording
JD_WORKLOAD_IMPORT_LIVE=1 JD_WORKLOAD_IMPORT_BROWSER=1 \
  JD_IMPORT_BROWSER_EVIDENCE_DIR=/tmp/jd-import-proof-server \
  go test -p 2 ./internal/api \
  -run '^TestLiveWorkloadAdoptionKeepsFourContainerStackAndHTTPServiceUnchanged$' \
  -count=1 -v -timeout=20m
```

Wait for `/tmp/jd-import-proof-server/ready.json`. Its session cookie is private and must never be
published. The fixture exposes its backend on loopback port 44119 and waits for a `stop` file. Route
`/api/*` on loopback port 43162 directly to that backend and everything else to the freshly built
frontend at 43161 using an isolated owned Caddy instance; this preserves long recovery requests and
WebSocket upgrades. Use this Caddyfile:

```caddyfile
{
  admin off
  auto_https off
}
http://127.0.0.1:43162 {
  handle /api/* {
    reverse_proxy 127.0.0.1:44119
  }
  handle {
    reverse_proxy 127.0.0.1:43161
  }
}
```

From `frontend/`:

```bash
JD_BROWSER_BASE_URL=http://127.0.0.1:43162 \
  JD_IMPORT_NATIVE_READY=/tmp/jd-import-proof-server/ready.json \
  JD_IMPORT_NATIVE_EVIDENCE=/tmp/jd-import-proof-recording \
  bunx playwright test tests/browser/workload-import-native.spec.ts --workers=1
```

Copy the passing video's `test-results/.../video.webm` before another browser invocation resets the
output directory. Publish only sanitized JSON, screenshots and the recording. Finally create
`/tmp/jd-import-proof-server/stop` to release fixture cleanup, and remove only the isolated router
you created. The fixture removes its unique stack and volumes itself. The opt-in native manager,
Docker lifecycle, n8n and proxy fixture commands are documented in CONTRIBUTING.md and their reports.
