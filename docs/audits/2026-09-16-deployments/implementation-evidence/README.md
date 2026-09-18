# Implementation verification evidence

These logs describe the uncommitted audit implementation on `patch/0.6.7`, based on
`d1389e7d58196f147607193307e78b4b802c0b0a`. They supplement, and do not replace, the original audit's
[`evidence/`](../evidence/README.md). See the [implementation ledger](../implementation-progress.md)
for outstanding requirements. A passing component or mocked-browser test is not a clean-host product
acceptance pass.

The local verification environment uses Linux x86_64, Go 1.26.8 (`/tmp/jd-go1.26.8/bin/go`), Bun 1.4.0,
Docker Engine/client 29.8.0 and Compose 5.5.1. CI selects the Bun version declared in
`frontend/package.json`; a hosted CI run has not yet been performed. Browser checks run the production
frontend on the isolated port configured in `playwright.config.ts`.

## Preview upgrade checkpoint

- Backend [build](backend-build-after-preview-upgrade.txt), [vet](backend-vet-after-preview-upgrade.txt)
  and [full tests](backend-full-after-preview-upgrade.txt) passed using the commands below.
- Frontend [lint](frontend-lint-after-preview-upgrade.txt), [build](frontend-build-after-preview-upgrade.txt),
  [27 unit tests](frontend-unit-after-preview-upgrade.txt) and [103 browser tests](browser-after-preview-upgrade.txt)
  passed. [Focused browser evidence](preview-upgrade-browser.txt) covers approval and visible quarantine blocks.
- [Preview component evidence](preview-upgrade-components.txt) covers persisted startup quarantine,
  failed cleanup retry, exact revision review, close/reopen generations, trigger scoping and early database
  credential refusal.
- [Live preview evidence](preview-upgrade-live.txt), from
  `JD_DEPLOY_LIVE=1 go test ./internal/deploy -run '^TestLive(PreviewStorageCredentialsNetworkAndCleanup|LegacyPreviewQuarantinePreservesProduction)$' -count=1 -v`,
  proves new-preview isolation/cleanup and legacy-container quarantine without deleting production data.
  Legacy quarantine's route adapter is a fixture; it does not claim a live ingress withdrawal test.
- [Five-engine evidence](preview-database-isolation-live.txt), from
  `JD_DEPLOY_LIVE=1 go test ./internal/api -run '^TestLiveDeploymentDatabaseConnection$' -count=1 -v`,
  adds early production-reference/network refusal to the replacement/reconnection checks. All five passed.

The race gate after preview upgrades is still running. Disposable VM preparation is not installation
acceptance evidence. Neither checkpoint is a public DNS/TLS/provider-delivery pass.

## Recovery checkpoint

| Check | Command / evidence | Result and scope |
| --- | --- | --- |
| Backend build | `cd backend && go build ./...`; [log](backend-build-after-recovery.txt) | Exit 0; successful Go builds have no output. |
| Backend vet | `cd backend && go vet ./...`; [log](backend-vet-after-recovery.txt) | Exit 0; no diagnostics. |
| Backend tests | `cd backend && go test ./...`; [log](backend-full-after-recovery.txt) | Passed; opt-in live tests are separate. |
| Backend race gate | `go test -race -p 1 ./internal/deploy ./internal/api ./internal/proxysvc ./internal/backups ./internal/store ./internal/dockerx -count=1`; [log](backend-race-after-recovery.txt) | All six packages passed, including the additional previous-schema migration fixture. |
| Frontend lint | `cd frontend && bun run lint`; [log](frontend-lint-after-recovery.txt) | Passed. |
| Frontend build | `cd frontend && bun run build`; [log](frontend-build-after-recovery.txt) | Passed. |
| Frontend unit tests | `cd frontend && bun run test`; [log](frontend-unit-after-recovery.txt) | 27 passed. |
| Production browser suite | `cd frontend && bun run test:browser`; [log](browser-after-recovery.txt) | 102 passed in 3.9 minutes; most application API responses are mocked by the browser fixtures. |
| Recovery components | `go test ./internal/backups ./internal/api ./internal/deploy` with recovery/coverage test selection; [log](recovery-components.txt) | Passed, including uncovered linked dependencies. |
| Previous backup schema migration | `go test ./internal/backups -run TestRecoveryMigration -count=1`; [log](recovery-migration.txt) | Passed after the full backend checkpoint; preserves existing jobs/runs without inventing recovery evidence. |
| Isolated application recovery | `JD_DEPLOY_LIVE=1 go test ./internal/api -run '^TestLiveSQLiteApplicationRecoveryVerification$' -count=1 -v`; [first pass](sqlite-application-recovery-live.txt), [repeat](sqlite-application-recovery-repeat.txt) | Native live-WAL SQLite snapshot, fixture data destruction, application-level canary read from the restored copy, incompatible schema refusal and owned cleanup passed. |
| Recovery browser workflow | [log](backup-recovery-browser.txt) | Two production-browser tests passed: configuration and failed-check retry. |

The regular/race API tests use deliberately unavailable loopback DSNs for optional external database
integration tests, avoiding discovery or modification of operator databases. The dedicated five-engine
fixture owns its real disposable databases. Later preview-quarantine changes are outside this checkpoint.

## Earlier implementation checkpoints

- [Database replacement](database-replacement-five-engines.txt): PostgreSQL, MySQL, MariaDB, Redis and
  MongoDB each moved to a different container IP. The same client container reconnected with the same
  logical URL; this does not prove existing TCP sessions survive database replacement.
- [Network ownership](database-network-ownership.txt), [database-reference browser](database-reference-browser.txt)
  and [activation after network changes](activation-after-networks.txt): refusal/cleanup, actual Compose
  normalization and the existing live C5 activation scenarios passed.
- [Backend full gate](backend-full-after-networks.txt), [six-package race gate](backend-race-after-networks.txt),
  [frontend build](frontend-build-after-networks.txt), [unit tests](frontend-unit-after-networks.txt) and
  [100 browser tests](browser-after-networks.txt) passed after database-network and Git-policy changes.
- [Framework recipes](frameworks-live.txt) and [Go recipe](go-live.txt): actual builds and served output,
  including build variables, SvelteKit adapters, static HTML/Vite, Containerfile and Go command overrides.
- [Preview isolation](preview-live.txt): distinct sentinel storage, absent production credentials,
  separate network and production-preserving cleanup. Legacy previews and full provider lifecycle remain
  separate work.
- [Git policy race checks](git-policy-race.txt) and [browser workflows](git-policy-browser.txt): shared
  hook/poll policy, manual-only settings and decision evidence passed.
- [Recipe backend full gate](backend-full-after-recipes.txt), [recipe race gate](backend-race-after-recipes.txt),
  [production browser gate](browser-recipes-production-build.txt) and [frontend unit tests](frontend-unit.txt)
  record the earlier recipe checkpoint.

## Failures retained and acceptance limits

The [initial recovery fixture failure](recovery-live-initial-fixture-failure.txt) used an unconfigured
Docker client. The [second fixture failure](recovery-live-second-fixture-failure.txt) did not publish the
source application's HTTP port. Both were corrected before the passing fixture. Earlier preview and
browser setup failures are described in the ledger; a stale port-3000 process was not accepted as browser
verification of this worktree.

No logs here certify public ACME/DNS/provider delivery, remote execution, ARM64, a fresh supported host,
the entire 25-application matrix, 100 loaded cutovers, native recovery of every database engine or a
production RPO/RTO. CI workflow syntax passed actionlint 1.7.12, and its Go-JSON checker was exercised
against pass, skip, missing-fixture and failure inputs; no hosted CI result is claimed.

## Availability checkpoint

The first R13 work: cutover continuity under real traffic, and the proxy/runtime half of the fault
matrix. No product code changed for this checkpoint — these are the acceptance fixtures the audit
asked for, plus the CI gate that runs them.

| Check | Command / evidence | Result and scope |
| --- | --- | --- |
| Cutover continuity | `JD_DEPLOY_LIVE=1 JD_CUTOVER_EVIDENCE=… go test ./internal/proxysvc -run '^TestLiveCutoverTrafficContinuity$' -count=1 -v`; [log](cutover-traffic.txt), [measurements](cutover-traffic.json) | 100 real activations in 35.853 s, slowest 442 ms. 54,448 fast requests, 60 long streamed responses, 228 new WebSocket sessions and four persistent WebSocket connections (1,432 messages) — **zero failures**. An unrelated site in the same nginx was byte-identical afterwards. |
| Proxy loss at a transition | `JD_DEPLOY_LIVE=1 go test ./internal/proxysvc -run '^TestLiveCutoverSurvivesProxyLoss$' -count=1 -v` | A cutover attempted with the proxy killed fails, reports recovery as **unverified** rather than claiming it, leaves the previous release's exact bytes on disk, and serves the previous release once the proxy returns. The same cutover then applies cleanly on retry. |
| Runtime loss at a transition | `JD_DEPLOY_LIVE=1 go test ./internal/deploy -run '^TestLiveRuntimeLossKeepsExactlyOneReleaseLive$' -count=1 -v` | A SIGKILLed candidate in the blue/green window is removed without error and leaves exactly one running owned container; a SIGKILLed live release restarts in place as the same release. Ownership is read from Docker, not from the deployment tables. |
| Backend kill at every step | `JD_DEPLOY_LIVE=1 go test ./internal/deploy -run '^TestLiveBackendKillAtEveryStepLeavesOneRecoveredRun$' -count=1 -v`; [log](backend-process-fault.txt) | A real `internal/deploy/testdata/engine-host` process claims the run, stalls inside one step and is SIGKILLed — 16 subtests, one per step. Each time the restarted engine ends the run with `restart_evidence_missing`, leaves no claimable run for the environment, and the killed process's own step output is still readable. |
| Backend build / vet / full suite | [build](backend-build-after-availability.txt), [vet](backend-vet-after-availability.txt), [tests](backend-full-after-availability.txt) | Run after the additions above; 33 packages, no failures. |
| Race gate | [log](backend-race-after-availability.txt) | `go test -race -p 1 ./internal/deploy ./internal/api ./internal/proxysvc ./internal/backups ./internal/store ./internal/dockerx -count=1`; all six packages passed, no data races. |

### What this does not establish

- The traffic fixture runs on this host with two loopback-published release containers. It is not a
  public-internet, TLS, or multi-node measurement, and it reports no competitor comparison.
- The process-kill matrix kills the **engine** process. Killing a build subprocess or an adapter's own
  child process mid-command is not covered, and neither is a kill during a real Docker or proxy call:
  the stalled step in that fixture performs no side effect, so the test proves the reconciler's
  refusal-to-guess contract rather than side-effect convergence.
- Public TLS staging (an ACME staging order against a real domain) remains unverified; no domain was
  used and none is claimed.
- Recovery timing remains the single-file SQLite measurement from the recovery checkpoint. No
  multi-service RPO/RTO has been measured, and durable-diagnostics survival across a restart has not
  been tested.
- nginx renders `listen 80`, so the fixture runs its proxy in a container with port 80 published on
  loopback. Container-to-host-gateway traffic is blocked on this host, so the release fixtures run as
  containers on the test's own bridge network.
