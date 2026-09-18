# Deployment gap-closure plan and re-evaluation

**Date:** 2026-09-16 UTC (second pass). **Baseline worktree:** `patch/0.6.7` at `d1389e7` plus the
uncommitted implementation of F01–F09 and the R13 fixtures recorded in
[`implementation-progress.md`](implementation-progress.md).

This document re-evaluates the deployment feature after the first audit's implementation work, maps
the product against Coolify, Dokploy, CapRover, Dokku and Vercel, records the defects found in this
pass, and defines the work executed in this pass plus what remains. Status columns are updated at the
end of the pass; a row is **Done** only when its verification evidence exists in this checkout.

## 1. Baseline verification of the inherited worktree

| Check | Result | Notes |
| --- | --- | --- |
| `go build ./...` (Go 1.26.8) | Pass | |
| `go vet ./...` | Pass | |
| `go test -count=1 ./...` | Pass | 33 packages; database integration DSNs pointed at closed loopback ports so no operator database was touched. |
| Deployment race gate (`-race`, five packages, parallel) | **1 failure** | `TestFleetReadStaysWithinItsBudgetAtReferenceScale`: p95 515.9 ms against a 500 ms budget while the other four race packages ran concurrently. No `DATA RACE`. Alone: p95 312.7 ms, pass. Same sensitivity the first audit recorded (530 ms). Tracked as D5. |
| `bun run lint` | Pass | |
| `bun run build` | Pass | |

Live evidence from the operator's own server (read-only copy of `vpsd.db`): three normalized projects,
six runs. Four failed runs on one project: three `unsupported_builder` (competing `bun.lock` and
`package-lock.json`, now resolvable through the package-manager setting F05/recipes) and one
`health_gate_failed` whose candidate never answered on its leased port. That last run is the source of
D1 and D2 below: the operator was left with "could not connect after 20 attempts" and nothing else,
because the candidate container was removed by compensation and no notification was sent.

## 2. Re-evaluation of the first audit's findings

| ID | First-audit status | Evidence found in this pass | Verdict |
| --- | --- | --- | --- |
| F01 preview isolation | Implemented | `preview_isolation.go`, approvals API, quarantine controller, live fixtures listed in CONTRIBUTING. | Holds. Compose previews still refuse (documented). |
| F02 backup coverage / restore evidence | Implemented | `backups/manifest.go`, `recovery.go`, `sqlite_snapshots.go`, gate tests. | Holds for SQLite; native capture for other engines still absent (documented). |
| F03 build variables | Implemented | `build_variables.go`, recipe docs. | Holds. |
| F04/F05 defaults, SvelteKit, Containerfile | Implemented | `frameworks_live_test.go`, `deployment-defaults.ts`. | Holds. |
| F06 Go overrides/versions | Implemented | `build_go.go`. | Holds. |
| F07 reliable gate + CI | Partial | `.github/workflows/verify.yml` present; race gate still load-sensitive (D5). Hosted CI unverified because nothing has been pushed. | Partial. |
| F08 polling/hook policy | Implemented | `git_policy.go`, `git_changed_paths.go`. | Holds. |
| F09 database identity | Implemented | `deployment_database_network.go`, live five-engine fixture. | Holds. |
| R13 availability under faults | Partial | Live cutover/traffic/kill fixtures exist. | Partial (public TLS staging, multi-service RPO/RTO unverified). |
| F10, R11, R12, R14, R15 | Pending | No implementation. | Pending; see §5. |

## 3. Defects found in this pass

Severity: **P0** breaks a safety or correctness promise; **P1** impairs ordinary operation or
trustworthy verification; **P2** quality.

| ID | Severity | Defect | Fix in this pass |
| --- | --- | --- | --- |
| D1 | P1 | **Notifications are only sent for successful runs.** `notify` is the last release step; `Engine.failRun` and `finishCancellation` end the run without executing it, so `failed`, `rolled_back`, `failed_activation` and `cancelled` runs never notify anyone. Live run 22 confirms `notify` stayed `pending`. | Engine-level run observers deliver `run.started`, `run.succeeded`, `run.failed`, `run.cancelled` for every run regardless of where it ended; the `notify` step remains the success-path gate. Deliveries stay deduplicated per (channel, run, event). |
| D2 | P1 | **A failed readiness or smoke gate discards the only diagnostic evidence.** Compensation stops and removes the candidate before the operator can read its logs; the transcript contains only "could not connect". | Before compensation the runtime owner inspects the candidate (state, exit code, OOM flag) and writes its bounded, secret-redacted log tail into the run transcript; evidence records the state and counts, never log text. |
| D3 | P1 | **No resource limits or restart policy in the runtime plan.** `RuntimePlanConfig` has no memory/CPU/PID fields although `dockerx.ContainerSpec.Limits` supports them; every deployment runs unbounded with a hard-coded `unless-stopped`. Coolify, Dokploy and CapRover all expose limits. | Additive `memoryMb`, `cpus`, `pidsLimit`, `restartPolicy` with closed validation, preflight warnings against host capacity, runtime owner wiring, and a Runtime settings UI. |
| D4 | P2 | Deployment **Console** tab reports "Integration not available" for every non-game project, although the Docker owner already provides an audited exec WebSocket. | The Console tab opens the live release container (service selector for Compose) through the existing exec route behind the terminal capability. |
| D5 | P2 | Fleet-read performance budget is load-sensitive under `-race`. | Documented; not changed in this pass. The reference-scale statement count (10) is unchanged. CI already runs race packages serially. |
| D6 | P2 | Notification channels support only a signed generic webhook; there is no Discord, Slack, Telegram or e-mail delivery, which every competitor ships. | Channel kinds with sealed per-kind configuration, rich event envelopes, per-kind formatting, test delivery and delivery history. |
| D7 | P2 | No commit status reaches GitHub for a deployed revision, so a developer sees no deployment state on the commit or pull request. | Opt-out commit statuses (`pending`/`success`/`failure`/`error`) posted through the dashboard's own `gh` credential with a link to the run page. Failures to post are warnings and never change a run outcome. |
| D9 | P1 | **A concurrent SQLite writer could end a deployment as `restart_evidence_missing`.** Orchestration transactions began deferred (read, then write); when any other connection committed in between — a notification delivery row, an operator saving settings, an API token write — the worker's write failed instantly with `SQLITE_BUSY_SNAPSHOT` ("database is locked (517)"), the worker exited, the lease expired and the reconciler refused to guess. Reproduced by the real-backend e2e in this pass under CPU load; the failure is pre-existing but the new run observers made it likelier. | Every write transaction now begins `IMMEDIATE` (`_txlock=immediate` in the store DSN): a second writer waits on the 5-second busy timeout instead of failing after its read. Read-only transactions stay deferred. Regression test in `internal/store`. |
| D8 | P1 | **Every restart with a readiness check failed with `artifact_missing`.** `verifyChecks` resolved the release by run id; a restart owns no release, so after the live runtime had already been stopped and started the run ended as failed. Found by the real-backend end-to-end run in this pass (restart of a healthy nginx release), not by any existing test: the only restart test exercised `restartLiveRuntime` alone. | Checks resolve the release through the operation's target (the live release) as `startCandidate` and `recordRelease` already did; `TestRestartVerifiesChecksAgainstTheLiveRelease` drives start, readiness and record-release for a restart. |

Security review of this pass (webhooks, preview frame, variable reads, run logs, notification
delivery): provider and generic hooks use raw-body HMAC with constant-time comparison; GitLab tokens are
compared in constant time; unknown hook ids and bad signatures are indistinguishable and audited; the
preview frame's CSP restricts `frame-src` to the recorded origin and `frame-ancestors` to the
dashboard; variable listing and reveal join environment to project in SQL; run logs check project
membership. No new vulnerability was found. Notification targets remain administrator-configured
outbound URLs; that is the same trust boundary as every competitor and is documented rather than
restricted.

## 4. Competitive position

"Documented" means the competitor's official documentation describes the capability; nothing was
benchmarked head to head. **JD now** reflects this checkout after the pass.

| Capability | JD now | Coolify | Dokploy | Vercel | Where JD stands |
| --- | --- | --- | --- | --- | --- |
| Automatic builds | Node (npm/pnpm/yarn/bun), Go, Python, static, Dockerfile/Containerfile, image, Compose | Nixpacks, Railpack, static, Dockerfile, Compose | Nixpacks, Railpack, buildpacks, static, Dockerfile | Framework presets | Behind on PHP/Ruby/Java/Rust/.NET automation (B05). Ahead on digest-pinned bases and BuildKit secret mounts by default. |
| Health-gated zero-downtime | Blue/green for stateless HTTP with required readiness; stop-first otherwise, downtime declared | Rolling update when eligible; running counts as ready without a healthcheck | Swarm health/rollback | Atomic | Ahead: readiness is a planning requirement and cutover continuity is measured (R13). |
| Rollback fidelity | Frozen artifact **and** frozen configuration/variables | Retained image with *current* config | Registry image | Instant to prior deployment | Ahead. |
| Resource limits | **Memory, CPU, PIDs, restart policy (this pass)** | Yes | Yes | n/a | Parity. |
| Notifications | **Discord, Slack, Telegram, e-mail, signed webhook; started/succeeded/failed/cancelled (this pass)** | Discord, Slack, Telegram, e-mail, Pushover | Discord, Slack, Telegram, e-mail | Slack, webhooks, e-mail | Parity. |
| Failure diagnostics | **Candidate logs and exit state captured before compensation (this pass)**; numbered searchable transcript | Deployment log with container output | Deployment log | Build logs, runtime logs | Parity or ahead. |
| Per-app terminal | **Console tab into the live container (this pass)** | Yes | Yes | n/a | Parity. |
| Commit statuses / PR feedback | **Commit statuses via `gh` (this pass)**; PR comments and GitHub App pending (R11) | GitHub App with PR comments | GitHub App | Full | Behind on PR comments and deployment URLs in PRs. |
| PR previews | Approval-gated, isolated storage/network/secrets | Preview env vars, fork restrictions | Previews with quota | Automatic | Ahead on trust boundary; behind on automatic trusted-author flow. |
| Backups and restore proof | Coverage manifests, SQLite consistent snapshots, artifact-bound restore checks | Engine-aware DB backups to S3 | Volume backups to S3 | n/a | Ahead on proof, behind on native PostgreSQL/MySQL/Mongo dumps and S3 targets (D8 in §5). |
| One-click services | Catalogue exists, deployment refused | Large template library | Template catalogue | Marketplace | Behind (F10). |
| Teams / roles / multi-server | Single server, server-wide capabilities | Teams, remote servers | Remote servers, Swarm | Teams | Behind (R14/R15). |
| Private control plane | Outbound Git polling behind allowlist, no inbound hooks required | Requires reachable hooks or polling | Same | Hosted | Ahead for private single-server operators. |

## 5. Work plan

### Executed in this pass

| Item | Scope | Verification |
| --- | --- | --- |
| D1 run observers | `deploy.RunObserver` interface; engine calls `RunStarted`/`RunFinished`; notification dispatcher implements it; `notify` step unchanged for success. | Unit tests: failed run notifies, cancelled run notifies, success notifies once, observer errors never change run state. |
| D6 notification kinds | Additive `kind`/`config_enc` columns; Discord, Slack, Telegram, e-mail (SMTP STARTTLS/TLS), webhook; rich envelope; formatter per kind; validation of hosts/tokens/addresses; test endpoint; edit/toggle/delete UI; delivery history. | Unit tests per formatter and validator; HTTP fixture tests for Discord/Slack/Telegram payloads; SMTP fixture for e-mail; browser tests for channel creation/editing. |
| D2 candidate diagnostics | `RuntimeDiagnoser` on the Docker runtime owner: inspect + bounded tail (200 lines / 32 KiB) per container, redacted through the run's variable values; transcript lines and evidence counts. | Unit tests with a fake diagnoser; live check inside the existing activation fixture path. |
| D3 resource limits | Plan fields, validation, preflight findings, runtime wiring, release comparison labels, Runtime settings UI. | Unit tests for validation/preflight/wiring; browser test for the form. |
| D4 console | Console tab for container/Compose deployments with service selector behind `terminal`. | Browser test. |
| D7 commit statuses | `deploy.CommitStatusPublisher` observer using `ghx`; per-environment `commitStatuses` policy flag (default on); target URL from the dashboard's recorded endpoint. | Unit tests with a fake runner; documented manual verification. |
| D8 restart verification | `verifyChecks` uses the operation's target release. | Regression test through start, readiness and record-release; real-backend e2e restart. |
| D9 immediate write transactions | `_txlock=immediate` in `store.Open`. | `TestConcurrentWritersWaitInsteadOfFailingWithBusySnapshot`; full store/deploy/api suites; real-backend e2e rerun. |
| Real-backend e2e | `scripts/e2e-deployments.py`: isolated backend on loopback, fresh data directory, local webhook receiver, nginx (limits) and busybox (crash) image deployments driven through the public API. | Recorded in `implementation-progress.md`. |
| Docs | `implementation.md`, `recipes.md`/new `notifications.md`, frontend feature map, README feature list, CONTRIBUTING gate notes. | Reviewed against the diff before completion. |

### Next phases (not executed here)

| Phase | Work | Exit condition |
| --- | --- | --- |
| A | **D8 native database backups**: `pg_dump`/`mysqldump`/`mongodump`/`redis --rdb` capture adapters in Backups with S3-compatible destinations, wired into the coverage gate. | Deployment with a linked PostgreSQL passes the gate with a native dump, restore drill verifies a canary. |
| B | **F10 template catalogue**: connect blueprint materialization, secret generation and lifecycle to the engine for a first set (PostgreSQL, Redis, MinIO, Uptime Kuma, n8n, Vaultwarden). | Each template passes install/health/upgrade/restore/removal fixtures. |
| C | **B05 build breadth**: Rust, Java (Maven/Gradle), .NET, Ruby, PHP recipes or a vetted Railpack/Nixpacks adapter with digest pinning and secret mounts preserved. | Locked fixtures per language build and serve through detection. |
| D | **R11 GitHub App**: installation flow, PR comments with preview URL, check runs, trusted-author policy. | Real PR open/update/close cycle with status and preview URL. |
| E | **R12 declarative config, promotion, CLI**: `just-dashboard.yml` reconciliation, staging → production promotion of the same artifact, API tokens for CI. | Promotion retains artifact identity and enforces environment-specific inputs. |
| F | **R14/R15 multi-server and teams**: remote execution boundary, registry transport, project-scoped roles. | Cross-project access denied; remote deploy survives agent reconnect. |
| G | **D5 fleet performance**: profile the fleet read and index/caching work so the p95 budget holds under `-race` with parallel packages. | Race gate green in a parallel run three times consecutively. |

## 6. Acceptance evidence for this pass

Recorded in [`implementation-progress.md`](implementation-progress.md) under "Second pass" once the
gates ran: backend build/vet/test, deployment race gate, frontend lint/build/unit/browser, and the
focused fixtures named above. Anything not run is listed there as unverified, never as passing.
