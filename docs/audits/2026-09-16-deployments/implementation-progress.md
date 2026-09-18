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

## Fourth pass (2026-09-17)

Re-read the subsystem against a fresh backend capability audit (source audit's "Lifecycle" and "Gaps
and rough edges" sections) and executed its nine findings (B1–B9) and eight small features (F13–F20,
continuing the numbering above; F20 is the git-ref/commit override the third pass's inventory had
already scoped and left for this one). Evidence below is from this checkout; earlier ledgers are
unchanged.

| Item | Outcome | Evidence |
| --- | --- | --- |
| B1 archive left schedules firing | Done | `Store.Archive` disables the project's schedules directly (git watch already excluded an archived project); `TestArchiveDisablesSchedulesAndUnarchiveLeavesThemDisabled` |
| B2 remove-managed collapsed to `500` | Done | `deploy.RemovalFailure`/`deploy.Unavailable` classify a remover error as `409 removal_failed` or `503`, carrying its exact sentence and the partial `execution`; `TestRemoveManagedKeepsEarlierSuccessesWhenALaterTargetInTheSameBatchFails`, `TestRemoveManagedReportsAnUnavailableOwnerWithThePartialExecution` |
| B3 failed candidate release stuck at `candidate` | Done | `TransitionRun` moves the run's candidate release to `failed` on `failed`/`failed_activation`/`rolled_back`/`cancelled`; `TestFailedRunFailsItsCandidateReleaseAndRetentionKeepsItSevenDays` exercises the retention planner's seven-day branch directly |
| B4 non-deterministic hostname suggestion | Done | `Sealer.DeriveHMAC` seeds a per-install, per-name suffix with a collision counter; `TestHostnameSlugIsALegalStableLabel`, `TestSuggestHostnameSlugIsDeterministicAndAvoidsTakenDomains` |
| B5 draft detect unknown `selectedId` | Done | Error message now names the id; `TestDeploymentPlanningSignedInJourneyPersistsWithoutDeploying` |
| B6 variable reveal `400` for unknown name | Done | New `ErrVariableNotFound` → `404 variable_not_found`; `TestDeploymentVariableRevealAnswersNotFoundForAnUnknownName` |
| B7 draft `404` hid forbidden/expired | Done | Split into `403 draft_forbidden` / `410 draft_expired` / `404 draft_not_found`; `TestDeploymentDraftRoutesRequireSessionsAndDistinguishForbiddenExpiredAndMissing` |
| B8 `PlanConfiguration.Validate` had no field pointer | Done | Typed `ValidationError{Field,Message}` for ports/mounts/dependencies/domains/variables/checks/release tasks, plus the wrong "ports 1-65535" message; `TestPlanConfigurationValidateAttachesAFieldPointerToCommonRefusals`, `TestDeploymentConfigurationSaveReportsAFieldForAnInvalidPort` |
| B9 supersession compared always-empty `changedPaths` | Done (contract: let an unscoped schedule not cancel a scoped push) | Git watcher and provider webhooks now record resolved changed paths in run metadata; `TestScheduleQueuedBehindAPushDoesNotSupersedeIt`, `TestGitPolicyAPIAndSignedHooksUseCompleteDiffAndPinRevision` |
| F13 unarchive | Done | `Store.Unarchive`, `POST /deploy/{id}/unarchive`; `TestUnarchiveRefusesATakenName`, `TestDeploymentUnarchiveRestoresNameAndRefusesATakenOne` |
| F14 pin/unpin a release | Done | `SetReleasePinned`, `PUT .../releases/{release}/pin`; `TestSetReleasePinnedSurvivesAPrunePlan`, `TestDeploymentReleasePinRoute` |
| F15 run history filters and pagination | Done | `ProjectRunsFiltered`/`RunListFilter`, byte-compatible default; `TestProjectRunsFilteredNarrowsAndPaginates`, `TestDeploymentRunsEngineViewFiltersAndRejectsAnUnknownState` |
| F16 trigger delivery log and secret rotation | Done | `TriggerDeliveries`, `RotateTriggerSecret`; `TestTriggerDeliveriesAndSecretRotation`, `TestTriggerDeliveriesAndRotateSecretRoutes` |
| F17 reject a preview approval | Done | `RejectPreview`, `POST .../approvals/{approval}/reject`; `TestRejectPreviewClosesTheApprovalUntilANewEvent`, `TestPreviewApprovalRejectRoute` |
| F18 list the caller's drafts | Done | `PlanningStore.ListDrafts`, `GET /deploy/drafts`; `TestListDraftsReturnsOnlyTheOwnersUncommittedUnexpiredDrafts`, `TestDeploymentDraftListReturnsOnlyTheCallersOwnDrafts` |
| F19 Compose service count in the fleet summary | Done | `DeploymentSummary.serviceCount` folded into the existing `liveReleaseFacts` batched read, no added statement; `TestFleetSummaryReportsServiceCountFromTheRuntimeSnapshot`; `TestFleetReadModelStatementCountDoesNotGrowWithTheFleet` still passes unchanged |
| F20 deploy a specific Git ref or commit | Done | `deploymentRunCreateRequest.SourceRevision`/`Ref`, `HostSourceAnalyzer.ResolveGitRef`; `TestResolveGitRefDistinguishesNotFoundFromUnavailable`, `TestDeploymentRunCreateAcceptsAnExplicitSourceRevision`, `TestDeploymentRunCreateResolvesAndRecordsAnExplicitRef`, `TestDeploymentRunCreateRefusesARefOverrideOnAnImageProject`, `TestDeploymentRunCreateRefusesBothSourceRevisionAndRefTogether`, `TestDeploymentRunCreateRefusesARefOverrideOnALegacyComposeProject`, `TestDeploymentRunCreateWithAnExplicitRevisionDoesNotAdvanceTheGitWatchCursor` |
| F21 manage Git/registry credentials | Done | `deploy_credentials` CRUD under `/deploy/credentials` (`CreateCredential`/`UpdateCredential`/`DeleteCredential`/`ListCredentials`/`GetCredential`), sealed with the existing `auth.Sealer`; `gitEnvironment` gained a `git_ssh` path (sealed key to a private 0600 file, `GIT_SSH_COMMAND` isolated from the operator's real `~/.ssh`) alongside the existing bearer path; `POST .../test` probes through the same adapter isolation; `TestCreateCredentialValidatesShapePerKindAndSealsTheSecret`, `TestUpdateCredentialKeepsTheStoredSecretWhenOmitted`, `TestCredentialUsageCountingAndDeleteRefusesWhileInUse`, `TestCredentialExistsFailsFastForADraftSourceStep`, `TestGitSSHEnvironmentWritesAPrivateKeyFileAndPointsGitAtItExclusively`, `TestGitBearerEnvironmentScopesTheHeaderToTheExactRemoteAndCleansUp`, `TestGitEnvironmentAgainstARealLocalRepositoryStaysUsableWithoutACredential`, `TestTestCredentialRunsAGitProbeAndReportsFailureWithoutTheSecret`, `TestTestCredentialResolvesARegistryManifestAndReportsFailureWithoutTheSecret`, `TestDeploymentCredentialsCRUDJourneyNeverLeaksTheSecret`, `TestDeploymentCredentialCreateValidatesKindShapeAndInUseDelete`, `TestDeploymentCredentialRoutesRequireSystemAdmin` |
| F22 change a committed project's source | Done | `PlanningStore.SaveEnvironmentSource`, `PUT /deploy/{id}/environments/{env}/source`; no watcher/pending-state change needed since both already key off the source's own digest; `TestSaveEnvironmentSourceRefusesAChangedKindAndAStaleRevision`, `TestSaveEnvironmentSourceRefusesAnUnknownCredential`, `TestSaveEnvironmentSourceChangeIsPickedUpByPendingStateAsASourceChange`, `TestGitWatcherReportsBranchChangedAfterASourceUpdateEvenWithTheSameResolvedRevision`, `TestDeploymentSourceUpdateChangesSubdirectoryAndRefusesAKindChange`, `TestDeploymentSourceUpdateReResolvesTheImageDigest` |
| F23 duplicate a project into a draft | Done | `PlanningStore.Duplicate` (drops domains, flattens variables to a blank required declaration, re-derives managed volume names via `blueprintVolumePrefix`), `POST /deploy/{id}/duplicate`; `TestDuplicateDropsDomainsFlattensVariablesAndRederivesManagedVolumes`, `TestDuplicateCommitsIntoAnIndependentProject`, `TestDuplicateRefusesAnUnknownProjectAndAnInvalidName`, `TestDeploymentDuplicateCreatesAResumableDraft`, `TestDeploymentDuplicateRefusesAnUnknownProjectAndRequiresSystemAdmin` |

Gates run after the changes: `gofmt -l` clean on `internal/deploy`, `internal/api`, `internal/store`;
`go build ./...` and `go vet ./internal/deploy/... ./internal/api/... ./internal/store/...` clean;
`go test ./internal/deploy/... ./internal/store/... -count=1` and `go test ./internal/api/... -run
'Deploy|Deployment|Draft|Preview|Trigger|Hostname|Archive|Release' -count=1` pass; a full `go build
./...`, `go vet ./...` and `go test ./... -count=1` across all 33 packages also passes.

Not verified in this pass: `go test -race`, the frontend gate (`bun run lint`/`build`/`test:browser`) —
this pass touched only `backend/internal/{deploy,api,store,httpx,auth}` and the docs above, per the
brief's scope — real Docker/Git-remote/notification endpoints, hosted CI. The operator's running
dashboard was not rebuilt by this pass.

## Fourth pass, continued: credentials, source change, duplicate (2026-09-17)

F21–F23 above (credentials CRUD and test route with SSH/bearer plumbing, `PUT .../environments/{env}/source`,
`POST .../duplicate`) were added in a second sub-pass over the same "Fourth pass" scope. No schema change was
needed: `target`/`username`/`lastUsedAt` live in `deploy_credentials.config_json`, which already existed.
`registryAuth`'s decoder was widened from a private anonymous struct to the shared `credentialConfig` type so
that additive field (`target`, `lastUsedAt`) does not trip its `DisallowUnknownFields` the first time a
registry credential is used after this change — caught by writing the usage-counting test before assuming the
existing decoder would tolerate it.

Gates run for this sub-pass: `gofmt -l` clean on `internal/deploy`, `internal/api`, `internal/store`;
`go build ./...` and `go vet ./internal/deploy/... ./internal/api/... ./internal/store/...` clean;
`go test ./internal/deploy/... ./internal/store/... -count=1` and `go test ./internal/api/... -run
'Deploy|Deployment|Credential|Source|Duplicate|Draft' -count=1` pass.

Not verified in this sub-pass, for the same reason the brief itself calls out: a real Git remote over
HTTPS/SSH and a real image registry are not reachable from this sandbox. Bearer/SSH plumbing (temp file
permissions and content, `GIT_SSH_COMMAND` construction, environment sanitization) is proven against a real
local bare repository over the file transport and against a real refused TCP connection; the one thing this
cannot show is a remote actually accepting the credential. `go test -race` and the frontend gate were not run,
matching the prior sub-pass's own scope note.

## Fifth pass (2026-09-18): detect the project and have everything ready

Brief: map the deployment system end to end against Coolify, Dokploy, CapRover, Dokku and Vercel, and
close the gaps that make an ordinary first deployment manual — automated defaults first, every
detected value still an editable setting. The largest remaining gap after the fourth pass was breadth
of detection (B05 absent, B04 partial, S09 partial): a Remix or React Router app was read as a Vite
static site, an Astro or Nuxt starter needed a hand-typed start command, every Python project needed a
pinned lockfile and an explicit ASGI/WSGI command, and Rust, Java, .NET and Deno had no recipe at all.

| Item | Outcome | Evidence |
| --- | --- | --- |
| F24 Node framework catalogue | Done | `frameworks_node.go`: ordered catalogue (meta-frameworks before Vite), serving mode, port, entry check, runtime env (`HOST=0.0.0.0` for Astro node), `start:prod` for Nest, `angular.json` output resolution, VitePress docs directory, main-entry fallback with HTTP-library labels; `TestNodeFrameworkCatalogueDetectsServingDefaults` (29 starters), `TestNodeFrameworkDefaultsFollowTheLockfileRunner`, `TestSchemaStepChainsBeforeAFrameworkDefaultStart`, `TestAngularOutputReadsTheWorkspace` |
| F25 Procfile | Done | `web:` outranks start scripts and framework defaults, never turns a site into a server; `TestProcfileWebProcessOutranksGuessesButNotStaticOutput`, `TestProcfileAndVitepressHelpers` |
| F26 Single-page fallback | Done | additive `build.spaFallback`, nginx `try_files` written by `staticServerLines`, on by default for Vite/CRA/Vue CLI/Ember/Parcel/Angular, switch on the configure form and Build settings; `TestRecipesRenderFrameworkEnvironmentsEntriesAndFallbacks`, `TestPlanValidationBoundsTheNewBuildFields`, browser `deploy-settings-a` |
| F27 Python zero-config | Done | `frameworks_python.go` (Django with migrate/collectstatic, FastAPI, Flask incl. factories, Streamlit, Gradio; entry files shallowest first, tests excluded), `build_python.go` (interpreter from `.python-version`/`runtime.txt`/`requires-python`, catalogue 3.10–3.13, uv/Poetry/requirements/bare-pyproject installs, undeclared gunicorn/uvicorn pinned and installed into the same environment), unpinned requirements accepted + `dependencies_unpinned` warning; `TestPythonFrameworkCatalogueDetectsServingDefaults` (14 layouts), `TestPythonVersionSelection`, `TestPythonDependencyReading`, `TestPythonEntriesStayUnderTheirOwnRoot`, `TestPreflightWarnsAboutUnpinnedDependencies` |
| F28 Environment discovery and database suggestions | Done | `env_discovery.go`: `.env.example`-family templates (examples kept unless credential-shaped), a committed `.env` for names only, code references in JS/TS/Python/Go/Ruby/PHP under a separate 400-file/3 MiB budget, ranked ordering (own template, `.env`, repository-level template, code by name), engines from dependencies/Prisma provider/variable names with the right variable; candidate `variables`/`databases` validated in `validateDetectionResult`; `TestEnvironmentDiscoveryReadsTemplatesAndCode`, `TestEnvironmentDiscoveryFollowsRoots`, `TestDatabaseSuggestionsFromDependenciesSchemasAndNames`, `TestEnvironmentDiscoveryStopsQuietlyAtItsBudget`, `TestEnvTemplateFileNamesAndExampleValues` |
| F29 Rust, Java, .NET, Deno recipes | Done | `frameworks_rust.go` (Cargo manifest reader, framework ports, toolchain pin, static musl build), `frameworks_java.go` (Maven/Gradle, release 11/17/21/25, jar selection, `MaxRAMPercentage`), `frameworks_dotnet.go` (csproj choice, net8–10, `ASPNETCORE_HTTP_PORTS` bridge), `frameworks_deno.go` (JSONC, tasks, entry files, `deno install --frozen`); `validRecipe` extended; `TestRustDetectionAndRecipe`, `TestJavaDetectionAndRecipe`, `TestDotnetDetectionAndRecipe`, `TestDenoDetectionAndRecipe`, `TestCompiledLanguagesCoexistAsSeparateRoots` |
| F30 Configure form and Build settings | Done | framework labels (`frameworkLabel`), discovered rows with placeholders and sources merged across re-detection without losing typed values, unset-row count, database buttons opening the sheet on the engine and variable, Python version field, single-page switch, Language select for seven recipes; `deployment-defaults.test.js` (+6), browser `deploy-new.spec.ts` "a detected site opens with its variables, its database and its single-page fallback ready", `deploy-settings-a.spec.ts` "static output offers the single-page fallback and Python shows its version" |
| F31 Deploy link | Done | `/deploy/new?repo=<clone url>&ref=<branch>` prefills the Git tab; only `https://`, `ssh://` and `git@` URLs are accepted; browser "a deploy link arrives on the Git tab with its clone URL and branch filled in" |
| Live fixtures | Done | `TestLiveDetectedFrameworkBuildAndServing` gained Astro 7, Nuxt 4, React Router 8, FastAPI, Flask, Django, axum, Maven, ASP.NET Core and Deno; all seventeen fixtures build and serve on this host's Docker daemon (original seven re-run after the change) |

Not done, deliberately: PHP and Ruby recipes (Laravel needs `APP_KEY`, extensions and a public root the
form cannot yet ask for; Rails ships its own Dockerfile), a GitHub App with PR comments (R11), multi-server
and teams (R14/R15), S3 backup targets, deployment protection (a password in front of a route). Each is
listed in the fifth-pass comparison in the deployment map.

Gates run: `gofmt -l` clean; `go build ./... && go vet ./... && go test ./... -count=1` (all packages);
`JD_DEPLOY_LIVE=1 go test ./internal/deploy -run TestLiveDetectedFrameworkBuildAndServing` for the ten new
fixtures and again for the original seven; `bun run lint`, `bunx tsc --noEmit`, `bun test src` (70 tests),
`bun run build`, and the browser suite against that build. Not verified: a real GitHub push through the
new detection, PHP/Ruby repositories, Gradle/Streamlit/Gradio live builds (rendered Dockerfiles only).

## Sixth pass (2026-09-18): ahead, not behind

Brief: everything the fifth pass listed as still behind, except teams and multi-server — a PHP/Laravel
recipe, a GitHub App with pull request comments, S3 backup targets, password protection on routes,
catalogue breadth, live Gradle/Streamlit/Gradio fixtures and a public ACME test journey — each one
tested and shippable.

| Item | Outcome | Evidence |
| --- | --- | --- |
| F32 PHP recipe | Done | `frameworks_php.go`: Composer version constraints (caret, tilde, ranges, unions; 8.2–8.4, `^7.4` refused), `ext-*` through `install-php-extensions`, Laravel/Symfony/Slim detection, FrankenPHP image with a Composer stage and an optional Vite/Encore asset stage, migrations in the start command; `TestPHPDetectionAndRecipe`, `TestLaravelDatabaseSuggestionFollowsDBConnection`; live `laravel` and `php` fixtures; the form mints `APP_KEY` (`deployment-defaults.test.js`, browser `deploy-new`) |
| F33 Password protection | Done | `domain_protection.go` (seal once at the boundary, validate, canonicalise), `PlannedDomain.Protection`, route rendering for Caddy (`basic_auth`) and nginx (`auth_basic` + htpasswd), previews inherit; `domain_protection_test.go`, `TestDeploymentRoutePasswordProtectionOnBothProxies`, live isolated Caddy 401/200; Domains settings and public-address step; browser `deploy-settings-b`, `deploy-new` |
| F34 Catalogue breadth | Done | 36 definitions with registry-verified tags; `image.command`; paced readiness budgets; secondary direct ports published (`runtime.ports[]`, `PublishedPort`); three stale pins repointed and MinIO's missing command fixed; `TestLiveEveryBlueprintStartsAndAnswersItsOwnChecks` (48 deployable definitions pass; five wrong paths/settings fixed from its first run); `TestSecondaryDirectPortsArePublishedNextToTheRoutedPort`, `TestRuntimePortMappingsPublishTheRoutedAndPinnedPorts`, `TestPublishedPortsMakeAPlanStopFirstOnly` |
| F35 GitHub App | Done | `internal/githubapp` (RS256 JWT, installation tokens, listings, statuses, comment upsert, manifest exchange, sealed store, service), `github_app` table, `github_app` credential kind minting on open, `TriggerConfig.delivery`, `TriggersForAppDelivery`, `PullRequestCommenter`, `dispatchAutomationEvent` shared by both webhook doors, routes and the public `/hooks/github-app`; `githubapp_test.go`, `github_app_credentials_test.go`, `github_comments_test.go`, `TestAppDeliveryTriggersAreFoundByRepository`, `TestGitHubAppRoutesConnectRouteDeliveriesAndDisconnect`; Credentials page card, import picker, webhook form |
| F36 S3 backup targets | Done (were present) | `objectstore.go` already carried S3/B2; `TestLiveObjectStorageBackupUploadsPrunesAndRestores` proves target test, upload, retention and restore against MinIO |
| F37 ACME directory and public-certificate journey | Done | `acme_directory.go` (`JD_ACME_DIRECTORY`, `JD_ACME_CA_ROOT`), Caddy issuer block and root install, verification against configured roots, certbot `--server`; `TestConfiguredACMEDirectoryReachesCaddyAndCertbot`; `TestLiveDockerCaddyIssuesThroughAConfiguredACMEDirectory` against Pebble |
| F38 Gradle, Streamlit, Gradio live | Done | fixtures under `testdata/app-fixtures/{gradle,streamlit,gradio}`; the live suite checks Streamlit's health endpoint and static file and Gradio's embedded config; all three pass |

Not done, deliberately: teams and multi-server placement (an architectural decision the brief excluded).
Not verified: GitHub itself (the App is exercised against a fake GitHub; a real App is the first thing to
connect on a public dashboard), Let's Encrypt itself (Pebble stands in, validating nothing), and a real
S3 or B2 account (MinIO stands in for the same API).
