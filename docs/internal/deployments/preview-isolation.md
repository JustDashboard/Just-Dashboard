# Preview trust and resource isolation

Provider signatures authenticate deliveries; they do not authorize repository contributors to execute
code on a root-equivalent server. Each PR revision requires an authenticated administrator session to
approve the full Git object ID. No preview environment, build or runtime is created while it is pending.
Production network allowlisting, provider signature verification and capability checks still apply.

`deploy_preview_approvals` stores the provider event, trigger, PR number, immutable revision and approving
actor. Its states are `pending`, `approved`, `superseded`, `closed` and `rejected`. A new head supersedes
earlier heads; returning to a closed or superseded head requires approval again. `rejected` is an
administrator's `POST …/previews/approvals/{approval}/reject`: the row stays rejected through every later
attempt at that revision (`recordPreviewRevisionTx` re-enters `pending` only from `closed` or `superseded`),
and only a different head gets a fresh row. Queue admission checks approval and the
preview's saved revision. Manual runs use that saved revision, not the current remote PR head. GitHub
previews fetch the base repository's PR ref, preserving the repository credential boundary for forks.
Approval generations distinguish a reopened or returning head from an earlier run of the same SHA.
Configuration checks approval again inside its transaction so a concurrent close or new-head event
cannot revive a superseded approval. The ref a preview fetches is the provider's own read-only one,
`refs/pull/N/head` (`refs/merge-requests/N/head` on GitLab), and until the pull-request-preview pass it
could not be fetched at all: `validSourceRef` refused every `refs/` namespace but `heads` and `tags`, and
`planningGitRef` would have turned it into `refs/heads/refs/pull/N/head` had it passed. `providerPullRefRE`
(`planning_model.go`) now admits exactly that shape after the traversal and option checks, and
`planningGitRef` passes it through unchanged so `materializeRemoteGit` fetches `+refs/pull/N/head` from
the base repository; every other `refs/` namespace stays refused (`refs/pull/1/merge`, `refs/notes/x`).
The shape is admitted only where the preview flow writes it (`IsProviderPullRef`): `ResolveGitRef`
refuses a pull request head with `ErrInvalidRef` before any git subprocess runs, a manual run's `ref`
naming one on anything but a preview environment answers `400 ref_not_applicable`, and `PUT …/source`
outside a preview and a draft's source save answer `400 invalid_ref` (`refusePullRequestHead`) — a name
like `refs/pull/7/head` typed into a project's source would otherwise build anyone's pull request as
production with no approval at all.

`GET /deploy/{id}/previews/approvals` lists pending/approved requests and whether configuration completed.
Session-only administrator `POST /deploy/{id}/previews/approvals/{approval}/approve` accepts `revision` and
optional `deploy` (default false). Approval is audited even if subsequent configuration fails; the same
request can be retried. `deploy:true` uses a stable approval idempotency key. An approval carries
`headRef`, the branch the pull request proposes — kept apart from the ref the build fetches, which is
the provider's own ref, `refs/pull/N/head` on GitHub and `refs/merge-requests/N/head` on GitLab — and
approvals recorded before it was kept have none. The UI shows the author as their face, the repository, the branch and the revision, warns
before approving a head that comes from a fork, opens the preview's own variable editor, and provides a
separate deployment action.
The key includes the approval generation. Reopening requires completed prior cleanup and a fresh
approval; it cannot reuse an earlier generation's run. New environment slugs include the trigger ID so
the same PR number from different triggers cannot select the same environment.

Preview configuration deliberately inherits no variables or database/dependency links. Every top-level
mount, including read-only mounts, is rewritten to a distinct, managed named volume. Production release
tasks and explicit build-secret mappings are removed. Preview-specific values can then be configured
through the normal scoped-variable API. Existing previews without provenance lose copied variable/link
state when approved configuration is regenerated. Production resource contents are never copied or deleted.
Database references are checked before build or runtime values are returned: known links/bindings to
another environment cannot share a connection or saved endpoint when either environment is a preview.
This includes aliases with another saved connection ID. Runtime joins also compare observed container
identity. Separate preview databases must be configured explicitly; arbitrary literal credentials,
untracked endpoints and outbound network access remain an administrator trust boundary.

Container previews use a dedicated Docker bridge with owner labels and loopback publication through the
existing ingress owner. Docker does not publish ports on containers attached only to an internal network;
the dedicated bridge permits outbound traffic and is not a hostile-code sandbox. Administrator code
review remains required. Host network, privileged mode, devices, added capabilities, any release task,
fixed host ports, production mounts and Compose preview plans are refused at admission and execution.
Dockerfile instructions run only after approval; an approved Dockerfile is trusted executable code.

Checks target the preview's own endpoint rather than inherited production URLs. A configured domain
pattern has one `{number}` placeholder. PR close cancels queued/build work, lets an atomic activation
settle, and queues resource cleanup; the environment is archived
only after that succeeds so a failed cleanup can be retried. Removal checks exact owner labels and volume/
network namespaces, removes preview containers and their anonymous volumes, then preview named volumes
and network. Ordinary run submission cannot invoke preview removal against production.
Closing a PR deletes its preview-owned volumes; this storage is disposable.

## Previews from the dashboard

A preview no longer needs a delivery. `POST /deploy/{id}/pull-requests/{number}/preview` (`system.admin`,
session only, mounted beside the approval route it stands in for) takes `{revision, copyVariables,
acceptFork}` and *is* the administrator's approval of exactly that head: `RecordPreviewApproval` writes
the row a webhook would have left `pending` and approves it in one step, with the caller as
`approved_by`, under the same supersede rule as `requirePreviewApproval` — a later dashboard approval
supersedes a configured head until it is reconfigured, and a rejected head stays rejected. The trigger
it hangs on is one the route makes for the environment, `EnsurePullRequestTrigger`: a GitHub trigger
named "Pull requests" — or "Pull requests (owner/name)" when an unrelated GitHub trigger on the
environment already holds the plain name — with `preview: true`, a quota of 5, no preview domain and no
delivery (a disabled one matching the repository is re-enabled rather than duplicated); with both names
held it answers `ErrTriggerNameTaken`, `409 trigger_name_taken`, asking the operator to rename one. Then `EnsurePreview`
configures the isolated environment as before, `MarkPreviewOrigin` records `origin: dashboard` and the
title, `RecordPreviewHead` the head, and the run is enqueued under `preview-approval:<approval>:<generation>`;
re-testing the same head answers with that run while it is in flight, or succeeded while its address is
still published; after a failed or cancelled run, or once the sweep marked the address unpublished, a
retest enqueues again under `:a<n>` as `preview_update`, since republishing the mapping is the run's
job. Everything is audited as `deploy.preview.test` (target the number): the row is claimed just before
the approval is written, with the project, revision and whether the head is a fork, so a refusal past
that point — `preview_cleanup_pending`, a store failure — is recorded (`success` false) against the head
it was for, and a success replaces the detail with the run and the copied and skipped variable names.
The `Pull requests` panel on the project Overview, the header's **Test a pull request…** and the
Git page's card strip and workspace all post here, and all hold the verb back while the preview is
Ready at this commit, Building, Closing or its cleanup failed (`previewHeld`, `lib/pull-requests.ts`);
the dialog then says "Nothing to build right now" with the reason (`heldReason`) and disables Build.
The dialog reads the head and the preview from the live listing rather than the row it was opened
from, and the fork acknowledgement is keyed to the head sha, so a head that moved after a
`pull_request_head_changed` refusal is reviewed again before Build enables.

The head the route builds is read from GitHub through the trusted identity, never from the request alone
and never from a checkout: the GitHub App's installation where the App is installed on the repository,
else the dashboard's own gh login (`dir ""`). A host account owning a checkout under `JD_GIT_ROOTS` would
otherwise be supplying the commit this server builds and runs. The refusals, in order: `400 bad_request`
(not a full sha), `404 not_github`, `409 not_remote_git` (a local checkout has nothing to fetch the ref
from), `409 preview_compose`, `409 preview_no_port` (no internal port to publish), the identity
(`409 sign_in_required | app_not_installed | not_installed`, `404 pull_request_not_found`,
`429 github_rate_limited`), `409 pull_request_closed`, `409 pull_request_head_changed` (the message
ends with the head GitHub reports now, so the operator reviews the new commits before testing them),
`409 pull_request_fork` unless `acceptFork` (the dialog warns that a fork's Dockerfile and build steps
run on this server once approved, and sends the flag only after "I have reviewed this fork's changes" is
ticked), `400 preview_fork_variables`, `409 tailnet_unavailable` (the tailnet unusable, or its serve
configuration unreadable), `409 trigger_name_taken`, then `previewCapacity` — `409 preview_quota` when
the trigger's open previews already fill its quota and this number's is not among them, `409
preview_address_exhausted` when no port in the range is free of both an address row and a served
mapping and this preview has no row yet — and only then the writes (`preview_cleanup_pending`, and
`preview_quota` again should two requests race past the preflight).

Closing is `POST …/pull-requests/{number}/preview/close`: destructive (`s.destructive`, session) with an
ordinary confirmation and no typed phrase, since the pull request can be tested again in a minute. It
runs the same `preview_remove` path a webhook close does, idempotently under
`pull-request-close:<preview>[:a<n>]` — a second close while the removal runs answers the same run id, a
failed removal is retried through the run's own retry route rather than closed again: **Retry cleanup**
on the Overview and on the Git page's strip and workspace (`service.control`, `POST
/deploy/{id}/runs/{run}/retry`, then the run's page), offered while the newest `preview_remove` run
ended short of success, cancelled and superseded included, which the server's listing
(`listedPreviews`) keeps and the UI reads as "Cleanup failed" — and is audited
`deploy.preview.close` with reason `closed`. The reconciler below closes through the same helper with
actor `reconciler` and reason `merged` or `closed`.

## Copying production variables

The one thing a dashboard-started preview may inherit is production's variables, and only when asked:
`copyVariables` is on by default in the dialog for a head that lives in the same repository and refused
outright for a fork (`400 preview_fork_variables`; the dialog's box is off and disabled, with the reason
beside it). The copy is
`PlanningStore.CopyEnvironmentVariables`, one transaction: the target must be a preview of the same
project, the preview's earlier copies (`deploy_variable_revisions.copied_from_environment <> 0`) are
deactivated first, then every active production variable — sensitive or not — whose value is not a
`${{` reference and whose key the preview has not set for itself is written through
`writeVariableRevisionTx` with `copied_from_environment` set to production's environment id. The rest
are skipped and named in `skipped`: a database, credential, domain or service reference resolves to
production's own resources, which a preview must never reach, a variable alias means nothing without
its target, and a key with an active revision of the preview's own (`copied_from_environment` 0,
`ownVariableKeysTx`, read once the earlier copies are deactivated) is the operator's word over
production's — the copy never overwrites it, and neither `DeactivateCopiedVariables` nor the close drops it. The head the copies were made for is kept in
`deploy_preview_refs.variables_copied_revision`, so the copy is per revision: testing a new head with the
box unchecked deactivates the copies (`DeactivateCopiedVariables`) and clears the column, and
`CompletePreviewRemoval` does both when the preview closes, leaving whatever the operator typed into the
preview's own editor untouched. A webhook preview still starts with none. The Automation page's previews
tag such a preview `production variables`.

## Tailnet-only addresses

A preview's container publishes on loopback exactly as production's does, and what reaches it is
`tailscale serve` on the host: tailscaled listens on the node's tailnet address and forwards one port to
`http://127.0.0.1:<upstream>`. The dashboard never binds a routable address itself, and the exception to
invariant 7 is written down in [invariants](../security/invariants.md). `deploy_preview_addresses` holds
one row per preview environment (`kind`, `port`, `upstream_port`, `url`, `published`, `updated_at`,
`UNIQUE(kind, port)`); `AllocatePreviewAddress` takes the lowest free port in **21000–21999** that
tailscaled is not already serving (`ServedTailnetPorts` is passed in as the exclusion) and answers
`ErrPreviewAddressExhausted` (`409 preview_address_exhausted`) when the range is full. A non-preview
environment is refused with `ErrPreviewIsolation`.

Publishing is a step of activation (`activation_executor.go`): after the route is verified and before
`ActivateCandidate` moves the release pointer, so a mapping that cannot be made never leaves a live
release nobody can reach. `selfcfg.TailnetServe.Publish` validates both port numbers before any argv
exists, requires a usable tailnet (`DetectTailscale`), reads `tailscale serve status --json` and refuses
a port served with any target that is not its own (`ErrTailnetPortTaken`) or funnelled to the public
internet, withdraws and re-adds its own mapping when the tailnet's HTTPS setting changed (tailscaled
will not switch a port's scheme in place), runs `tailscale serve --bg --yes --https=<port>|--http=<port>
http://127.0.0.1:<upstream>`, and reads the status back: a mapping that did not take or came back
funnelled is withdrawn and reported, not handed over as an address. The address is
`https://<node>.<tailnet>.ts.net:<port>` when the tailnet has HTTPS certificates and `http://…` when it
does not; a `Served` status that cannot be parsed is an error, never an empty configuration to overwrite.
The evidence carries `tailnet {url, port, reachable, probeError}`; `reachable` is advisory because the
node's certificate is minted on the first request. A mapping whose row could not be written (`MarkPreviewAddressPublished` failing) is withdrawn
before the failure is reported, under `context.WithoutCancel`: left standing, it would refuse every
later publish of the preview as somebody else's. A refused or failed publish stops the candidate and
puts the port back the way it was (`restoreTailnet`; recovery evidence `tailnetRestored`): pointed at
the predecessor while the predecessor runs, and otherwise — no predecessor, or a stop-first predecessor
that could not be restarted — withdrawn and the row marked unpublished, so the preview reads "Not
reachable" rather than offering a dead link. Step codes are `tailnet_unavailable`,
`preview_port_missing`, `tailnet_publish_failed` and, on removal, `tailnet_withdraw_failed` (retryable:
the row stays so the retried `preview_remove` withdraws again). `preview_remove` withdraws the mapping on
both of its exits (`addressWithdrawn`) and deletes the row. Restore and removal withdraw only a mapping
the row vouches for (`ownsServedTailnetPort`: the row's `upstream_port` is non-zero and tailscaled
serves that very loopback port there); a row with upstream 0 vouches for nothing, so a mapping the
operator put on the port after ours came down is never turned off.

At start, before the engine — `Server.startDeployEngine` sweeps and only then calls
`deployEngine.Start`, since a run resumed at boot may publish a preview the moment it starts and a sweep
still reading the config would take that fresh mapping for a dead one — `SweepTailnet` reconciles the
serve configuration with the table under a two-minute budget. It reads the address table first and the
serve configuration second, so no mapping is judged against a record older than itself.
`ServedTailnetPorts` answers a map of served port to the loopback port a plain, unfunnelled
`http://127.0.0.1:<port>` proxy points at — the one shape of mapping the dashboard makes — and 0 for
anything else served there (a directory, a funnel, a raw forwarder; `selfcfg.ServeEntry.LoopbackUpstream`).
A loopback mapping inside the range that no address row owns is withdrawn; a port the operator serves
a directory or a funnel on is left alone, and allocation steps around every served port whatever its
shape. An owned port served with a loopback upstream other than the row's `upstream_port` — the row of
an unpublished address too — is a publish whose record never landed: the mapping is withdrawn and a
`published` row marked unpublished. A row marked `published` on a port tailscaled no longer serves is
marked unpublished (its URL kept, `upstream_port` 0) so the UI says "not reachable, redeploy" instead of
offering a dead link. Ports outside the range are never touched. A serve configuration that cannot be
read is an error only while some row is a published tailnet address; otherwise the sweep answers
nothing, and the adapter answers an empty configuration on a host without the `tailscale` binary, so a
host without Tailscale is not warned at every boot. The server logs `withdrawnPorts` and
`unpublishedEnvironments` when there were any, and a warning when the sweep erred.

## Reconciliation with GitHub

A tailnet-only install receives no webhook, so `PreviewReconciler` (`preview_reconciler.go`) asks GitHub
about every open preview whose source identity resolves to the trigger's own github.com repository
(`OpenPreviewTargets`; a preview fetched from another host or another repository is not handed over)
every 60 seconds, through the same trusted identity as
the route above (`PullRequestStateReader`, implemented by `api.pullRequests`). It closes a preview only
on a state it decoded as not open — `merged` decides the audited reason — and never on a failure to
read: a 404, a revoked credential, a rate limit or no network wraps as `ErrPullRequestUnreadable` and
the preview stays. A head that moved is recorded (`RecordPreviewHead`), which is what the panels draw as
"Out of date" with an **Update preview** verb; a changed title is written through `MarkPreviewOrigin`.
Each pass spends at most 50 reads over 4 workers, rotating where it starts so a large fleet is covered
across passes, and the first failure in a repository stops that repository's remaining previews for the
pass. A quota refusal wraps as `ErrPullRequestRateLimited` — as `api.pullRequests` decides it, and it
alone: a 429 from the App, a 403 saying rate limit or abuse detection, or `HTTP 429` / `status 429` /
those words relayed by gh as text (`rateLimitWordsRE`), never a bare number, so pull request #429 or a
request id mentioning one is not a refusal — and stands the reconciler down for ten minutes; the
reconciler backs off on nothing but that sentinel (`rateLimited` is `errors.Is`), since a false backoff
pauses every repository. `ReconcileRepository`, which
the merge routes on both pages call synchronously after a successful merge, reads through that backoff
because the merge just proved the credential. Provider webhooks keep closing previews as before, and
`ProviderEvent` now carries `Merged` and `BaseRef` for GitHub, Gitea, GitLab and Bitbucket. Production
redeploys through the existing git watcher or App push delivery.

## Existing previews on upgrade

Before the deployment engine starts, `PreviewQuarantineController` finds previews without a reviewed,
isolated desired plan or with older release plans that lack isolation. It persists an additive
`deploy_preview_quarantines` record, fails old nonterminal work with trace events, and removes its leases
before touching Docker or ingress. An existing quarantine record survives restart.

Quarantine stops only containers whose managed/environment/release labels match the saved preview
releases, rechecks immutable container IDs, disables Docker restart policy and verifies the stopped
state. It withdraws only the environment's route, including when a container needs another stop attempt.
It never invokes the old Compose files, deletes a container, or removes a volume. Stored container IDs
with missing/mismatched ownership keep the quarantine incomplete rather than granting deletion rights.
All phases are audited as `deploy.preview.quarantine.*`.

Docker/proxy failures retain the admission block and retry every 30 seconds. The dashboard remains
available and the preview list explains incomplete isolation. Success clears the live pointer and
marks old releases/runtimes quarantined, preventing restart, redeploy or rollback through deployment
controls. Stopped legacy containers and their storage remain available for deliberate operator cleanup.

After isolation, redeliver the PR event and approve its exact revision. Reconfiguration clears inherited
variable/dependency links and creates isolated plans before enabling a fresh deployment. Already exposed
external credentials cannot be revoked by stopping a container; rotate any credentials shared with an
older preview. Compose previews remain unsupported and need a supported container plan.

## Verification

`TestPreviewWebhookRequiresSessionApprovalAndCloseRemainsRetryable` verifies the API trust boundary and
retry semantics. `TestLivePreviewStorageCredentialsNetworkAndCleanup` serves distinct canary contents,
checks that production credentials are absent and the production bridge endpoint is unreachable, then
removes preview resources while production continues serving. Browser coverage verifies revision review,
empty preview variables and separate deployment. These tests do not certify arbitrary Dockerfile code,
Compose previews, public provider delivery, or public DNS/TLS.
`TestLiveLegacyPreviewQuarantinePreservesProduction` stops a real legacy container with a production
mount/credential, refuses a production container identity, preserves production's HTTP canary and volume,
and verifies approved reconfiguration. Its route adapter is a fixture; proxy removal has separate owner
tests. The five-engine database live fixture refuses production references before returning credentials
or allocating a preview database network, then verifies production replacement/reconnection.

The dashboard-started path: `TestRecordPreviewApprovalConfiguresAPreviewWithoutAWebhook`,
`TestRecordPreviewApprovalKeepsARejectedHeadRejected`,
`TestLaterDashboardApprovalSupersedesTheConfiguredHeadUntilReconfigured`,
`TestEnsurePullRequestTriggerCreatesOnceAndReusesTheRepositoryTrigger`,
`TestEnsurePullRequestTriggerQualifiesItsNameWhenPullRequestsIsTaken`,
`TestMaterializeFetchesAPullRequestHeadFromTheBaseRepository` (a real fetch of `refs/pull/1/head` from a
local bare repository) and the `refs/pull/…` rows of
`TestSourceAndPlanValidationRejectsTraversalAndPlaintextCredentials`; the pull-ref refusals: the
`IsProviderPullRef` table in `planning_test.go`, `TestResolveGitRefDistinguishesNotFoundFromUnavailable`,
`TestDeploymentRunCreateRefusesAPullRequestHeadOutsideAPreview` and
`TestDeploymentSourceUpdateAndDraftRefuseAPullRequestHead`; the variable copy:
`TestCopyEnvironmentVariablesIntoAPreview` (a preview-authored key survives the copy) and
`TestCompletePreviewRemovalDropsCopiedVariables`; the
tailnet address: `TestAllocatePreviewAddressPicksTheLowestFreePortOnce`,
`TestAllocatePreviewAddressUnderConcurrencyNeverSharesAPort`,
`TestAllocatePreviewAddressReportsAnExhaustedRange`,
`TestActivatePublishesThePreviewOnTheTailnetBeforeTheReleasePointerMoves`,
`TestActivateStopsTheCandidateWhenTheTailnetRefusesThePort`,
`TestActivateReportsTheTailnetUnavailableWithoutAPublisher`,
`TestFailedTailnetPublishPointsTheAddressBackAtThePredecessor`,
`TestFailedTailnetPublishWithdrawsTheAddressWhenThePredecessorStaysDown`,
`TestPublishPreviewAddressWithdrawsAMappingItCouldNotRecord`,
`TestRestoreTailnetLeavesAForeignMappingAloneAndUnpublishesTheRow`,
`TestWithdrawPreviewAddressWithdrawsOnlyTheMappingTheRowVouchesFor`,
`TestRemovePreviewWithdrawsTheTailnetAddressOnBothExits`,
`TestSweepTailnetWithdrawsOrphanedPortsAndUnpublishesDeadAddresses`,
`TestSweepTailnetWithdrawsAMappingTheRowDoesNotVouchFor`,
`TestSweepTailnetReadsTheAddressTableBeforeTheServeConfig` and
`TestSweepTailnetStaysQuietAboutAnUnreadableServeConfigWithoutLivePreviews`, with `tailscale serve` itself
driven through a fake in `selfcfg/tailscale_serve_test.go` (`TestPublishArgvFollowsTheTailnetsHTTPS`,
`TestPublishRefusesAPortSomebodyElseServes`, `TestPublishRefusesAFunnelledPort`,
`TestPublishWithdrawsAMappingThatDidNotTake`, `TestWithdrawFallsBackToTheOtherScheme` and
`TestLoopbackUpstreamReadsOnlyAPlainLoopbackProxy` among them); the
reconciler: `TestPreviewReconcilerClosesOnlyAPullRequestItReadAsClosed`,
`TestPreviewReconcilerRecordsEachHeadMoveAndTheTitle`, `TestPreviewReconcilerBacksOffAfterARateLimit`
(only the wrapped sentinel backs off; three unreadable texts, "429" among their words, do not),
`TestOpenPreviewTargetsHandOverOnlyPreviewsOfTheTriggersGitHubRepository`,
`TestPreviewReconcilerSpendsItsReadBudgetAcrossPassesAndRepositories`,
`TestPreviewReconcilerStopsAtARepositorysFirstFailure` and
`TestPreviewReconcilerStartRunsAPassAndStopWaitsForIt`; the routes:
`TestTestPullRequestPreviewApprovesBuildsAndCopiesVariables`, `TestTestPullRequestPreviewRefusals` (a
pull request numbered 429 is not a rate limit), `TestPullRequestRateLimitIsAQuotaRefusalNotANumber`,
`TestTestPullRequestPreviewRefusesQuotaAndPortsBeforeWriting`,
`TestTestPullRequestPreviewAuditsARefusalAfterTheApproval`,
`TestTestPullRequestPreviewRebuildsAfterItsAddressWentDead`,
`TestPullRequestListsDropAPreviewWhoseCleanupSucceeded` (both listings),
`TestClosePullRequestPreviewIsDestructiveIdempotentAndAudited` and
`TestMergePullRequestFromDeployPageReconcilesAndRequiresSession`;
`TestOpenAddsPullRequestPreviewColumnsToAPreExistingDatabase` covers the added columns and table on a
pre-feature database. Browser coverage (`deploy-project.spec.ts`, `git-features.spec.ts`, `git-ui.spec.ts`)
posts the exact head with the variable choice and the fork acceptance, reads a fork before building it,
refuses a held preview from the picker and re-reads a head that moved under the dialog, retries a failed
cleanup from the Git page's row rather than testing again, holds the strip's verb on a Ready preview, and
confirms the close against its project; `lib/pull-requests.test.js` pins the readings the surfaces share
(a removal that ended short of success is a failed cleanup, which previews hold the verb back, the Merge
verb's promise, why a listing could not be read). Not verified here: a real tailnet — `tailscale serve`
is exercised through its recorded status shapes; the sweep's place before the engine is
`TestStartSweepsPreviewAddressesBeforeTheEngineResumesRuns` (an expired lease the fake publisher observes
still expired when the sweep reads the config), while the reconciler's start is compile-checked, not
driven end to end.
