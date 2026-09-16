# Deployment audit implementation

This is the implementation ledger for the complete 2026-09-16 deployment audit. The original
report and evidence remain a point-in-time record. An item is complete only when its acceptance
criteria have current implementation and verification evidence; a component test does not stand
in for a live product journey.

## Requirements and acceptance evidence

| ID | Required outcome | Status / evidence |
| --- | --- | --- |
| F01 | Preview trust approval, explicit variables/dependencies, isolated storage/network, production-safe cleanup; live sentinel/credential tests | Implemented for container previews, including persisted legacy quarantine and early production database-reference refusal. Real new/legacy Docker isolation and five-engine negative checks passed. Signed API close/reopen, approval generations, trigger scoping and browser workflows passed. Compose previews fail closed; public provider delivery remains unverified. |
| F02 | Complete bind/named/Compose backup manifests, consistency policy, artifact-bound isolated restore verification through an application | Implemented coverage and artifact-bound recovery checks, including linked storage/database coverage and native SQLite snapshots. A live application read the restored canary from an isolated copy and rejected an incompatible schema. API/browser and previous-schema migration checks passed. Native capture for other database engines and multi-container recovery remain unsupported. |
| F03 | Quick setup build values reach real Next/Vite output; private build credentials absent from logs/layers | Implemented; live Next/Vite served values and private install isolation passed. Full quick-setup journey remains part of F07. |
| F04 | Plain HTML and Vite defaults deploy and serve without manual port/readiness repair | Corrected serving defaults and nested static roots; real detected HTML/Vite build/runtime and frontend default tests passed. UI-to-proxy journey pending. |
| F05 | SvelteKit server/static adapters and Containerfile detection work through runtime | Both SvelteKit adapters and actual Containerfile build/serving passed live; unsupported adapters block planning. UI-to-proxy journey pending. |
| F06 | Observable Go overrides, explicit/source version selection, early CGO/version refusals | Implemented; real generated Go program served custom startup behavior under Go 1.26.8. Version/CGO refusals and containment have component tests. |
| F07 | Reliable complete required gate, CI, clean-host real UI/API/build/proxy acceptance; preserve initial failures | After preview upgrades: backend build/vet/full suite, frontend lint/build, 27 unit tests and 103 production browser tests passed; race gate is running. The preceding recovery checkpoint passed all six race packages. CI enforces regular, race, browser and selected live gates and rejects skipped/missing live fixtures; actionlint and result-checker probes passed locally. Hosted CI and the clean-host real product journey remain unverified; disposable VM preparation has begun. |
| F08 | Shared polling/hook branch/path policy, manual-only setting, recorded suppression reasons, commit deduplication | Implemented; policy/watcher, signed-hook/API, real Git tree comparison and focused race tests passed. Production browser policy save/manual-only and existing automation flows passed (3 tests). Included in the subsequent full regular and race gate after database networks. |
| F09 | Managed network and stable database DNS identity survive replacement with a different IP | Implemented for loopback-backed Docker database connections. All five quick-setup engines passed live replacement at a different IP, using the same client container and unchanged logical URL. Owned-network cleanup/refusal, real Compose normalization and full regular/race gates passed. Existing literal-IP configuration requires reconnecting once; applications still need to reconnect after server replacement. |
| F10 | Verified small template catalog with install/health/upgrade/restore/removal; separate game UDP/save/protocol contract | Partial (third pass). Twelve blueprints (PostgreSQL, MariaDB, MongoDB, Redis, MinIO, Adminer, Dozzle, Grafana, n8n, Uptime Kuma, Vaultwarden, nginx static) deploy end to end as image releases with generated secrets, managed volumes, checks and limits; the Redis blueprint is deployed by the real-backend e2e. Blueprints with config files or artifacts (Caddy, Prometheus, Gitea) and game servers remain preview-only with a per-blueprint reason; upgrade/restore/removal fixtures per template are still owed. |
| R11 | GitHub App installation, PR statuses/domain, open/update/close lifecycle; notification integrations | Pending |
| R12 | Declarative project reconciliation, supported CLI/API, artifact promotion with environment-specific inputs | Pending |
| R13 | Traffic during cutovers, crash/cancellation fault matrix, public TLS staging, stateful restore with measured RPO/RTO, durable diagnostics | Partial. Live cutover continuity is measured: 100 real nginx activations under sustained traffic lost none of 54,448 fast requests, 60 long streamed responses, 228 new WebSocket sessions or four persistent WebSocket connections, and an unrelated site on the same proxy was unchanged. Proxy loss during a cutover fails closed, keeps the previous release's bytes and reports unverified recovery rather than claiming it. Real container kills in the blue/green window and at a live release leave exactly one release running. A real second engine process is SIGKILLed inside each of the 16 steps in turn: the restarted engine refuses to guess, ends the run as `restart_evidence_missing`, leaves no claimable run, and the killed process's step output survives. Build-process and worker-subprocess kills, public TLS staging and multi-service RPO/RTO remain unverified. |
| R14 | Explicit remote execution/build architecture, target identity, registry transport, scheduling and disconnect/replacement recovery | Pending |
| R15 | Project/team authorization across API and remote execution with existing network/2FA boundaries preserved | Pending |

The final acceptance audit also covers the report's representative application matrix (at least
25 fixtures), install/redeploy/fail/rollback/restart/restore/remove cycles, 100 stateless cutovers
under HTTP/long-request/WebSocket traffic, declared performance conditions, x86_64/ARM64 coverage,
and truthful unsupported-case UI. External infrastructure requirements must be recorded as
unverified until actually exercised, never reported as passing by omission or skip.

## Worktree and verification

Initial product source: `d1389e7d58196f147607193307e78b4b802c0b0a` on `patch/0.6.7`.
Pre-existing edits: the audit directory and links in `docs/internal/README.md` and
`docs/internal/deployments/README.md`. These are preserved. No commit or push is authorized.

Implementation results are retained in the [evidence index](implementation-evidence/README.md).

First implementation checks (2026-09-16): focused backup/coverage tests and real Compose normalization
passed; focused preview/API tests and a real preview sentinel/credential/network/cleanup fixture passed.
The live fixture initially used Alpine without an HTTP server and failed; it was changed to Caddy and
cleanup now handles failed creation. A second run exposed Docker's refusal to publish an internal-only
network's ports; preview bridges now use separate network ownership with loopback publication.

An initial browser run reused the existing port-3000 app and therefore served stale code. The production
gate on its own port passed all 99 tests in 3.7 minutes, including approval/configuration. Lint initially
traversed generated Playwright trace-viewer code; explicit report-directory exclusions fixed that gate.
These initial failures are recorded and are not counted as product acceptance passes. New recipe changes
made after that gate require fresh verification. The entire audit remains incomplete.

The subsequent database-network checkpoint passed backend build/vet/full tests, the six-package race
gate, frontend lint/build, 27 unit tests and 100 production browser tests. The recovery checkpoint passed
the regular backend gate and frontend lint/build, 27 unit tests and 102 production browser tests. A
previous-schema backup migration fixture was added afterward and passed separately. The recovery race
gate passed all six packages, including that migration fixture. The repeated live SQLite application recovery took 1.105 seconds for the isolated
check on an 8192-byte fixture; this is not a production RTO claim or a multi-service restore measurement.

The recovery fixture's initial failures are preserved: its Docker client lacked an explicit socket,
then its source application had no published port. Those test setup errors were corrected before the
live pass. CI configuration is validated locally, but has not run on GitHub because no push is authorized.

The availability checkpoint added no product code: it added the acceptance fixtures R13 asked for and
the CI gate that runs them. Backend build, vet and the full suite passed (33 packages, no failures),
and the six-package race gate passed. The live additions are cutover continuity under traffic, proxy
loss during a cutover, container loss at a transition, and a real engine process SIGKILLed inside each
of the 16 steps. Their measurements and explicit non-claims are in the
[evidence index](implementation-evidence/README.md). Public TLS staging, multi-service RPO/RTO,
hosted CI and the clean-host product journey are still unverified, and F10 and R11–R15 other than this
R13 work remain outstanding.

## Second pass (2026-09-16, later the same day)

The [gap-closure plan](gap-closure-plan.md) re-evaluated the worktree above, recorded defects D1–D7 and
executed them. Evidence below is from this checkout; the first-pass ledger above is unchanged.

| Item | Outcome | Evidence |
| --- | --- | --- |
| D1 failure notifications | Done | `run_observer.go`, engine hooks in `engine.go`; `TestRunObserversHearAboutFailedRuns`, `TestRunObserversHearAboutSuccessAndCancellation`, `TestDispatcherDeliversFailedRunsAndDeduplicates`; live e2e below shows `run.failed` reaching a webhook while the `notify` step stayed `pending`. |
| D2 candidate diagnostics | Done | `RuntimeDiagnoser` on the Docker owner, capture in `activation_executor.go`; `TestFailedReadinessCapturesRedactedCandidateOutputBeforeCompensation`; `TestLiveRuntimeDiagnoserReadsExitedContainer` passed against the local Docker daemon; live e2e transcript carries the container's own stderr. |
| D3 resource limits and restart policy | Done | `planning_model.go`, `runtime_owner.go`, `preflight.go`, `release_comparison.go`; `TestRuntimeResourceLimitValidation`, `TestPreflightWarnsWhenLimitsExceedHostCapacity`; live e2e `docker inspect` shows Memory 128 MiB, 0.5 CPU, 64 pids, on-failure. |
| D4 console tab | Done | `deployment-console.tsx`; browser test "the console tab opens a shell inside the live release container". |
| D6 notification kinds | Done | `notifications.go`, additive `kind`/`target`/`config_enc` columns; validation matrix, payload and mailer tests; browser test for Discord creation, pause, test, history and removal. Real Discord/Slack/Telegram/SMTP endpoints were not exercised. |
| D7 GitHub commit statuses | Done (component) | `github_status.go`, `ghx/status.go`, additive `commit_statuses` policy column; `TestCommitStatusPublisherReportsEveryOutcomeOnce`; browser test for the policy toggle. No real GitHub post was made from this environment. |
| D5 fleet p95 under `-race` | Not changed | Package-by-package race run below; parallel race runs remain load-sensitive. |
| D9 `SQLITE_BUSY_SNAPSHOT` ends a run (found in this pass) | Done | `_txlock=immediate` in `store.Open`; `TestConcurrentWritersWaitInsteadOfFailingWithBusySnapshot`; store/deploy/api suites and the e2e rerun below passed with it. |
| D8 restart `artifact_missing` (found in this pass) | Done | `verifyChecks` resolves the operation's target release; `TestRestartVerifiesChecksAgainstTheLiveRelease`; the live e2e restart of the nginx release passes with no notification to a paused channel. |

### Gates run after the changes

| Check | Result |
| --- | --- |
| `go build ./...`, `go vet ./...` | Pass |
| `go test -count=1 ./...` | Pass, 33 packages |
| `go test -race` on deploy, api, proxysvc, backups, store (serially) | Pass for all five packages (deploy 216 s, api 314 s, proxysvc 5 s, backups 26 s, store 12 s); no `DATA RACE`. The parallel form of this gate remains sensitive to the fleet p95 budget (D5). |
| `bun run lint`, `bunx tsc --noEmit`, `bun run build` | Pass |
| `bun test src` | 29 passed |
| `bun run test:browser` (production build, all specs) | 107 passed, 4.6 minutes, including 4 new deployment tests |
| `JD_DEPLOY_LIVE=1 … -run 'TestLiveC5ActivationAdapters\|TestLiveRuntimeDiagnoserReadsExitedContainer'` | Pass on the local Docker daemon |
| Isolated real-backend end-to-end (`scripts/e2e-deployments.py`: fresh data directory, loopback port 8090, local webhook receiver, nginx and busybox images) | 18 of 18 checks passed on the final run. Earlier runs are preserved as findings: the first real run exposed D8 (restart ended `artifact_missing`) and corrected three test assumptions (Go canonical header case, no comparison for a first release, Docker restarting the crashing container); a run under `-race` CPU load exposed D9 (`database is locked (517)` ended the run as `restart_evidence_missing`). |

Not verified in this pass: real Discord, Slack, Telegram or SMTP delivery; a real GitHub commit status;
hosted CI (nothing was pushed); the clean-host product journey; public TLS. The operator's running
dashboard was not rebuilt by this pass; the changes live in the worktree until it is.

## Third pass (2026-09-16, later the same day)

The [third-pass plan](third-pass-plan.md) re-read the subsystem, recorded defects D10–D17 and executed
blueprint deployment (F10, partial), native database dumps in backup jobs, delivery insights and
notification retries. Evidence below is from this checkout.

| Item | Outcome | Evidence |
| --- | --- | --- |
| D10 manual-run operation whitelist | Done | `TestManualRunRouteRefusesLifecycleOperations` |
| D11 rolled-back chain step | Done | `TestScheduledChainTreatsRollbackAsFailure` |
| D12/D17 preview approval labelling and idempotency | Done | `TestPreviewWebhookRequiresSessionApprovalAndCloseRemainsRetryable` |
| D13 paused backup presets | Done | `TestBlueprintDefaultSchedulesTranslateExactlyOrNotAtAll`; e2e "no failing default schedule" |
| D14 Slack escaping / Discord caps | Done | `TestProviderPayloadsEscapeAndBoundTheirText` |
| D15 atomic delivery reservation, F13 retries | Done | `TestConcurrentObserversDeliverOneNotification`, `TestFailedDeliveriesAreRetriedWithBackoffAndThenGiveUp` |
| D16 managed volume preflight | Done | `TestPreflightLetsAManagedVolumeBeCreatedOnFirstStart`; e2e Redis blueprint |
| F10 blueprint deployment | Partial | `TestBlueprintDraftCommitsGeneratedSecretsAndInputValues`, `TestUnsupportedBlueprintsAreRefusedBeforeAnyResourceExists`, `TestPlannedVariableValuesAndGenerationAreBounded`; browser "a supported blueprint reaches a reviewed plan…"; e2e Redis blueprint (27/27) |
| F11 native database dumps | Done | `TestBackupCapturesNativeDatabaseDumpsBesideFilesAndRestoresThem`, `TestBackupFailsWhenADatabaseDumpFails`, `TestBackupJobRefusesUnknownOrDuplicateDatabaseDumps`, `TestDeploymentBackupGateAcceptsNativeDatabaseDumps`; live `TestLiveBackupDumpsAndRestoresAPostgresDatabase` passed on the local daemon; browser "a backup job dumps saved databases natively…" |
| F12 delivery insights | Done | `TestInsightsSummariseDeliveryFrequencyFailuresAndRecovery`, `TestProjectInsightsReadsPersistedRuns`, `TestDeploymentInsightsRouteReadsProjectHistory`; browser "the deployments tab reports delivery figures…" |

Gates after the changes: `go build`/`go vet`/`go test -count=1 ./...` (33 packages) pass; the serial race gate passes for deploy, api, backups, store, dockerx and proxysvc with no `DATA RACE`; `bun run lint`, `bunx tsc --noEmit`, `bun run build`, `bun test src` (29) pass; the production browser suite passes 110 of 110 in 4.8 minutes; `scripts/e2e-deployments.py` passes 27 of 27.

Not verified in this pass: real Discord/Slack/Telegram/SMTP delivery, a real GitHub status, hosted CI,
public TLS, `pg_dump`/`mysqldump`/`mongodump` binaries (the live dump used the built-in driver dump
because the tools are not installed on this host; the native path is exercised by `dbx`'s own live
tests). The operator's running dashboard was not rebuilt by this pass.
