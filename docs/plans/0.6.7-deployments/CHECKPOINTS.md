# 0.6.7 deployment implementation ledger

Update this file in the same commit that completes an item. Replace `Evidence: —` with a test name,
command output artifact, screenshot path, ADR, or source link. An implementation item and its verification
are separate checkboxes on purpose.

Legend: `[ ]` not started, `[~]` in progress (use only on the active branch), `[x]` complete with evidence.

## Planning baseline

- [x] Repository/0.6.6 deployment audit completed. Evidence: `01-current-state.md`
- [x] Official competitor and game-panel research mapped. Evidence: `02-competitive-research.md`
- [x] Product/UX flows and responsive/accessibility contract defined. Evidence: `03-product-ux.md`
- [x] Target state machine, model, API direction and security invariants defined. Evidence: `04-end-to-end-architecture.md`
- [x] Cross-feature ownership and handoffs defined. Evidence: `05-feature-integration.md`
- [x] Blueprint/Minecraft slice and acceptance workload defined. Evidence: `06-blueprints-and-game-servers.md`
- [x] Ordered checkpoint gates defined. Evidence: `07-delivery-roadmap.md`
- [x] Test, failure-drill and release acceptance plan defined. Evidence: `08-test-and-acceptance.md`

## C0 — Contracts and migration

- [x] ADR: orchestration and module ownership frozen. Evidence: `adrs/0001-orchestration-boundary.md`
- [x] ADR: canonical path/host execution boundary frozen. Evidence: `adrs/0002-path-and-execution-boundary.md`; `TestProjectValidateUsesSymlinkAwareDeploymentResolver`; `TestCommandInDirKeepsLocalWorkingDirectoryAndArgv`
- [x] ADR: builder spike and dependency/license decision complete. Evidence: `adrs/0003-builder-selection.md`
- [x] Domain, run, step, error and stream schemas frozen. Evidence: `09-frozen-contracts.md`; exhaustive `TestRunTransitionMatrix` and `TestStepTransitionMatrix`
- [x] Capability, audit and confirmation matrix frozen. Evidence: `09-frozen-contracts.md` API and security matrix
- [x] Additive DDL and populated 0.6.6 migration fixture written. Evidence: `internal/store/testdata/0.6.6.sql`; `TestOpenMigratesPopulated066DeploymentsIdempotently`
- [x] Browser-test approach and workflow documented. Evidence: `adrs/0004-browser-testing.md`; `frontend/playwright.config.ts`; `bun run test:browser` (Chromium 1/1)
- [x] C0 review/test gate passed. Evidence: `10-c0-review.md`; backend build/vet/all tests, frontend lint/build/browser, and `git diff --check`

## C1 — Persistent engine

- [x] Run/step/log/lease stores implemented. Evidence: `internal/deploy/orchestration_{store,queue,steps,events}.go`; `TestEnqueuePersistsRequestStepsEventsAndIdempotency`; `TestClaimFencesRunAndStepTransitions`
- [x] Closed state transitions implemented. Evidence: `internal/deploy/contracts.go`; exhaustive `TestRunTransitionTableExhaustivelyAllowsOnlyFrozenEdges`, `TestTerminalRunStatesHaveNoOutgoingEdge`, and `TestStepTransitionTableExhaustivelyAllowsOnlyFrozenEdges`
- [x] Per-environment queue and host concurrency budget implemented. Evidence: `TestQueueEnforcesPriorityEnvironmentAndHostBudgets`; `TestConcurrentClaimsCannotExceedHeavyBudget`; bounded `JD_DEPLOY_HEAVY_SLOTS`, `JD_DEPLOY_LIGHT_SLOTS`, and `JD_DEPLOY_LEASE_TTL`
- [x] Idempotency, queued superseding and cancellation implemented. Evidence: `TestEnqueuePersistsRequestStepsEventsAndIdempotency`; `TestAutomaticEnqueueSupersedesOnlyCoveredUnclaimedRuns`; `TestQueuedAndClaimedCancellationHaveDistinctCleanup`; `TestRetryCreatesAJoinedIdempotentRunOnlyForFailedOrCancelledInput`
- [x] Resumable sequenced event stream implemented. Evidence: `TestSubscriptionReconnectIsSequencedWithoutCommitPublishDuplicates`; `TestSlowSubscriberIsDroppedWithoutStallingAppend`; `TestTerminalLogCompactionForcesResyncAndBoundsPayloads`; `TestDeploymentRunStreamSendsSnapshotBacklogLiveTerminalAndAuditsOpen`
- [x] Startup lease reconciliation implemented. Evidence: `internal/deploy/engine.go`; `TestRestartAtEveryStepUsesEvidenceWithoutDuplicatingSideEffects`
- [x] Legacy handler compatibility path retained. Evidence: `TestLegacyRunRouteQueuesPersistentCompatibilityStep`; `TestLegacyCompatibilityPipelineRunsThroughPersistentEngine`; legacy manual/rollback/hook routes enqueue `legacy_pipeline` before `202`
- [x] Transition/queue/race/retention tests pass. Evidence: `TestCancellationAndCompletionRaceConvergesToOneFencedOutcome` (`-count=100`); `go test -race ./internal/deploy ./internal/hostexec -count=1`
- [x] Restart-at-every-step drill passes. Evidence: `TestRestartAtEveryStepUsesEvidenceWithoutDuplicatingSideEffects` covers all 15 frozen release steps and refuses release-task replay without a completion token
- [x] C1 gate passed. Evidence: `11-c1-review.md`; `go build ./...`; `go vet ./...`; `go test ./... -count=1`; deployment/host race suites pass

## C2 — Sources, detection and preflight

- [x] Resumable drafts and plan revisions implemented. Evidence: `internal/deploy/planning_{model,store}.go`; `TestDraftRevisionOwnershipExpiryAndAtomicIdempotentCommit`; signed-in planning journey
- [x] Git provider/URL/local adapters implemented. Evidence: `TestRemoteGitAdapterUsesContainedMirrorWorktreeAndEphemeralCredentialFile`; `TestPlanningGitMirrorCreatesDetachedWorktreeAtExactRevision`; `TestProviderRemoteNormalization`; `TestLocalGitImageAndImportSourceAdapters`
- [x] Image and registry adapter implemented. Evidence: `dockerx.DistributionInspect` adapter; `TestRegistryCredentialIsPassedOutOfBandAndNotReturned`; incompatible-platform preflight assertion
- [x] Compose paste/upload/Git/local/multi-file adapters implemented. Evidence: `TestComposeAnalysisAndAdapterPreserveMultiFileOrder`; `TestValidateComposePlanUsesIsolatedPlaceholderEnvironment`; Compose Git/local assertions
- [x] Existing checkout/container/stack import preview implemented. Evidence: `TestLocalGitImageAndImportSourceAdapters` proves observation-only preview and unsupported-field/redaction behavior
- [x] Bounded evidence-based detector implemented. Evidence: `TestDetectorIsDeterministicBoundedAndDoesNotFollowSymlinks`; `TestDetectionResultValidationRejectsTamperedEvidence`
- [x] Preflight and secret-free exact plan preview implemented. Evidence: `TestPreflightIsPureStableAndSecretFree`; `TestComposePreflightCoversVariablesPortsStorageAndAdvancedFields`; `TestPreflightCoversGitChoicesImageArchitectureAndDomains`
- [x] Source/import/security fixture matrix passes. Evidence: `12-c2-review.md`; source/ref/image/Compose/variable/archive/symlink/auth/import fixtures
- [x] Preflight no-side-effect proof passes. Evidence: read-only `PreflightObserver`; `TestHostPreflightObserverDoesNotCreateOrChangeRequestedPaths`; draft stability assertion
- [x] C2 gate passed. Evidence: `12-c2-review.md`; full backend build/vet/test and focused race suite

## C3 — Product UI

- [x] Deployment fleet and active queue implemented. Evidence: `13-c3-review.md`; fleet read model and responsive queue/table/cards
- [x] Outcome-first `/deploy/new` wizard implemented. Evidence: `13-c3-review.md`; resumable five-stage server-draft flow
- [x] Detection review and contextual configuration implemented. Evidence: public Git and Minecraft novice journeys plus normalized expert controls
- [x] Release-path preflight implemented. Evidence: server findings, exact persisted plan, acknowledgment and save journey
- [x] Project workspace and pending-change state implemented. Evidence: permanent tabs, ownership links, release history and guarded legacy actions
- [x] Permanent resumable run page implemented. Evidence: persisted snapshot, bounded transcript and sequence-resuming WebSocket browser test
- [x] Loading/empty/error/unavailable states complete. Evidence: browser state matrix and honest later-checkpoint boundaries in `13-c3-review.md`
- [x] Responsive 375/768/1024/1440 review passes. Evidence: Playwright assertions and visual review in `13-c3-review.md`
- [x] Keyboard/screen-reader/reduced-motion review passes. Evidence: semantic focus/status/transcript assertions and reduced-motion emulation
- [x] Core browser journeys pass. Evidence: production build/server; `deploy-ui.spec.ts`; 8 passed
- [x] C3 gate passed. Evidence: `13-c3-review.md`; frontend typecheck/lint/build/browser plus full backend and focused race suites

## C4 — Builds and releases

- [x] Selected automatic builder implemented. Evidence: versioned Node/Go/Python recipes; `TestAutomaticRecipesAndExplicitAdaptersRenderPinnedPlans`
- [x] Dockerfile adapter implemented. Evidence: hermetic and live Dockerfile cases
- [x] Static-site adapter implemented. Evidence: pinned nginx OCI output in hermetic and live static cases
- [x] Image/Compose immutable artifact flow implemented. Evidence: digest-pinned image/mixed Compose unit and live cases
- [x] Release/pre-deploy task implemented. Evidence: scope/redaction, timeout/cancellation, containment, and interruption-recovery tests
- [x] Build cache/no-cache and artifact retention implemented. Evidence: force/no-cache transcripts; current+5/pin/shared/lease/actual-prune test
- [x] Provenance and release comparison snapshot implemented. Evidence: atomic immutable candidate, run input rotation/retry, moving-tag and all-service comparison tests
- [x] Hermetic adapter tests pass. Evidence: `go test ./internal/deploy -count=1`
- [x] Live build fixtures pass. Evidence: `JD_DEPLOY_LIVE=1 ... TestLiveC4ArtifactAdapters`; six subtests passed
- [x] Secret-in-layer/argv/log checks pass. Evidence: BuildKit secret/redactor tests and live config/history/`docker save` scan
- [x] C4 gate passed. Evidence: `14-c4-review.md`; full backend/build/vet/race, live Docker matrix, frontend type/lint/build, 9 browser journeys

## C5 — Safe activation and recovery

- [x] HTTP/TCP/Docker/command/public-route checks implemented. Evidence: `internal/deploy/checks.go`; `TestCheckRunnerCoversHTTPRetriesTCPDockerCommandAndClosedOutcomes`; live C5 matrix
- [x] Eligible candidate blue/green activation implemented. Evidence: `internal/deploy/activation_executor.go`; loopback port-lease and immutable runtime-owner tests
- [x] Stop-first stateful/fixed-port path implemented. Evidence: `TestStopFirstExecutorNeverStartsStatefulCandidateConcurrently`; live container/Compose fixtures
- [x] Graceful drain and bounded escalation implemented. Evidence: live `SIGTERM_escalates_to_bounded_SIGKILL`; cancellation cleanup/race tests
- [x] Automatic prior-release restoration implemented. Evidence: `TestCancellationAfterCandidateStartRestoresStopFirstRuntime`; activation failure compensation
- [x] Deploy/redeploy/restart/force/rollback semantics implemented. Evidence: `TestNormalizedActionsUseDistinctImmutableReleaseSemantics`; normalized workspace browser journey
- [x] Failed-build and failed-readiness continuity tests pass. Evidence: C4 live failed-build fixture; `TestFailedReadinessLeavesLiveRuntimeAndRouteUntouched`
- [x] Cutover failure/route restoration drill passes. Evidence: `TestDeploymentRouteReloadFailureRestoresExactPriorSpec`
- [x] Exclusive-volume concurrency proof passes. Evidence: `TestStopFirstExecutorNeverStartsStatefulCandidateConcurrently`
- [x] C5 gate passed. Evidence: `15-c5-review.md`; backend build/vet/all/race, live Docker matrix, frontend type/lint/build and 9 browser journeys

## C6 — Configuration and feature joins

- [x] Scoped variables, bulk dotenv and typed references implemented. Evidence: variable graph/owner-resolution tests; `16-c6-review.md`
- [x] Generated secret/rotation/redaction/pending diff implemented. Evidence: configuration store and signed-in route tests; masked browser journey
- [x] Proxy/DNS/TLS/domain integration implemented. Evidence: C6 preflight, TLS route/certificate and activation refusal tests
- [x] Port/firewall conflict and exposure integration implemented. Evidence: execution-time conflict/self-ownership tests; C6 preflight matrix
- [x] Storage/backup/database dependency integration implemented. Evidence: real feature-owner adapter test
- [x] Backup-before-deploy gate implemented. Evidence: backup ordering, failure continuity and real Backups artifact tests
- [x] Managed/linked/observed ownership lifecycle implemented. Evidence: lifecycle/adoption/removal route tests
- [x] Security posture and full audit joins implemented. Evidence: workspace posture/browser journey; route audit assertions
- [x] Variable/domain/storage/backup integration tests pass. Evidence: full backend suite and 11 browser journeys
- [x] Signed-in route security matrix passes. Evidence: configuration and planning signed-in route tests under normal/race gates
- [x] C6 gate passed. Evidence: `16-c6-review.md`; backend build/vet/all/race, frontend type/lint/build and 11 browser journeys

## C7 — Automation and previews

- [x] GitHub provider event integration implemented. Evidence: provider verifier and signed route tests; `17-c7-review.md`
- [x] GitLab/Bitbucket/Gitea/generic verifier matrix implemented. Evidence: provider fixture matrix; `17-c7-review.md`
- [x] Legacy and scoped generic hooks/API implemented. Evidence: scoped HMAC and API idempotency route tests
- [x] Watch paths and simulator implemented. Evidence: matcher fixtures and signed-in simulator route
- [x] Scheduled actions and chain history implemented. Evidence: scheduler claim, required-chain and run-history tests
- [x] PR preview create/update/cleanup implemented. Evidence: immutable preview lifecycle and exact route-removal tests
- [x] Signed outbound webhook notifications implemented. Evidence: signature, bounded history and failure-isolation tests
- [x] Replay/wrong-source/burst/idempotency tests pass. Evidence: provider fence, queue supersession and API retry tests
- [x] Preview lifecycle isolation test passes. Evidence: preview inheritance/rotation/close and named proxy-route tests
- [x] Schedule timezone/DST tests pass. Evidence: `TestNextCronDSTAndInvalid`
- [x] C7 gate passed. Evidence: `17-c7-review.md`; backend build/vet/race, frontend type/lint/build and 12 browser journeys

## C8 — Operations and diagnosis

- [x] Runtime Docker service/stack summaries and deep links implemented. Evidence: backend `runtime` projection and `components/deploy/deployment-runtime.tsx`; `TestRuntimeServicesScopeAvailabilityAndSecretFreeProjection`; `TestFilteredContainerInventoryInspectsOnlySelectedRuntime`; `TestDeploymentReadModelsExposeFleetDetailAndEngineRuns`; browser journeys `runtime services hand off to exact Docker panels and survive history and reload` and `runtime evidence distinguishes unavailable Docker from an empty managed inventory` cover 375/768/1024/1440, light/dark, keyboard, reload/history and disappeared resources.
- [x] Runtime Logs and activation-window links implemented. Evidence: `TestRunLogHandoffUsesRunActivationAndExactPreviewRelease`; `TestDeploymentRunRoutesPersistBeforeAcceptedAndResumeByID` includes membership/auth/read-only log-route assertions; browser tests `log links preserve exact activation windows and never substitute a missing source` and `run runtime logs open the server-provided activation window and withhold unproven windows` verify the run-to-Logs journey, exact UTC/millisecond bounds through DST and reload, missing sources and invalid-window recovery. The project Logs tab uses runtime source links. Frontend lint/type/build and all 16 Chromium journeys passed; backend build/vet/tests passed with inherited `JD_TERMINAL_SHELL` unset for the environment-sensitive legacy-prefix test.
- [~] Metrics/release comparison implemented. Evidence: Metrics now records exact container IDs and provides bounded batched identity history; `TestContainerIdentityHistorySeparatesReplacementsAndSurvivesRename`, `TestContainerIdentityHistoryBoundsAndAggregation`, and `TestContainerIdentityMigrationPreservesUnattributedHistory`. Release-window aggregation and the deployment UI remain open.
- [ ] Domain/TLS/storage/backup summaries implemented. Evidence: —
- [ ] Conservative cross-feature diagnosis implemented. Evidence: —
- [ ] Release/artifact/update comparison implemented. Evidence: —
- [ ] Optional-module degraded states implemented. Evidence: —
- [ ] Diagnosis claim/silence tests pass. Evidence: —
- [ ] N+1/performance checks pass. Evidence: —
- [ ] C8 gate passed. Evidence: —

## C9 — Blueprints and Minecraft

- [ ] Blueprint schema/parser/renderer/provenance implemented. Evidence: —
- [ ] Existing Docker templates migrated to one foundation. Evidence: —
- [ ] Initial reviewed blueprint set shipped. Evidence: —
- [ ] Minecraft Java/Bedrock new-server flow implemented. Evidence: —
- [ ] Minecraft directory/archive import implemented. Evidence: —
- [ ] Console and supported player operations implemented. Evidence: —
- [ ] Properties, schedules, backups and update workflow implemented. Evidence: —
- [ ] Version/checksum adapters and unavailable states implemented. Evidence: —
- [ ] Blueprint safety/fixture CI passes. Evidence: —
- [ ] Minecraft new/import/update/rollback matrix passes. Evidence: —
- [ ] World-preservation and EULA audit proofs pass. Evidence: —
- [ ] C9 gate passed. Evidence: —

## C10 — Hardening

- [ ] Threat-model review complete. Evidence: —
- [ ] Fuzz/property/race/resource-bound suites pass. Evidence: —
- [ ] Failure drill table completed. Evidence: —
- [ ] Reference-scale performance budgets pass. Evidence: —
- [ ] Full dark/light/responsive/accessibility review passes. Evidence: —
- [ ] Affected `docs/internal/` architecture, command and invariant guides updated. Evidence: —
- [ ] README/API/recovery/blueprint documentation complete. Evidence: —
- [ ] No unresolved Critical/High findings. Evidence: —
- [ ] C10 gate passed. Evidence: —

## C11 — Release

- [ ] 0.6.7 changelog data written before version bump. Evidence: —
- [ ] Backend build/vet/unit/integration/live suites pass. Evidence: —
- [ ] Frontend lint/build/browser suites pass. Evidence: —
- [ ] Fresh install and degraded-module install pass. Evidence: —
- [ ] Populated 0.6.6 -> 0.6.7 upgrade rehearsal passes. Evidence: —
- [ ] Legacy project/hook/env/history compatibility passes. Evidence: —
- [ ] Fresh guided web deployment acceptance passes. Evidence: —
- [ ] Fresh Minecraft deployment/recovery acceptance passes. Evidence: —
- [ ] `scripts/release.sh 0.6.7` succeeds and version tests agree. Evidence: —
- [ ] Release commit created on `patch/0.6.7`. Evidence: —
- [ ] C11/0.6.7 release gate passed. Evidence: —
