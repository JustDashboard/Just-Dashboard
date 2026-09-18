# Preview trust and resource isolation

Provider signatures authenticate deliveries; they do not authorize repository contributors to execute
code on a root-equivalent server. Each PR revision requires an authenticated administrator session to
approve the full Git object ID. No preview environment, build or runtime is created while it is pending.
Production network allowlisting, provider signature verification and capability checks still apply.

`deploy_preview_approvals` stores the provider event, trigger, PR number, immutable revision and approving
actor. Its states are `pending`, `approved`, `superseded` and `closed`. A new head supersedes earlier heads;
returning to a closed or superseded head requires approval again. Queue admission checks approval and the
preview's saved revision. Manual runs use that saved revision, not the current remote PR head. GitHub
previews fetch the base repository's PR ref, preserving the repository credential boundary for forks.
Approval generations distinguish a reopened or returning head from an earlier run of the same SHA.
Configuration checks approval again inside its transaction so a concurrent close or new-head event
cannot revive a superseded approval.

`GET /deploy/{id}/previews/approvals` lists pending/approved requests and whether configuration completed.
Session-only administrator `POST /deploy/{id}/previews/approvals/{approval}/approve` accepts `revision` and
optional `deploy` (default false). Approval is audited even if subsequent configuration fails; the same
request can be retried. `deploy:true` uses a stable approval idempotency key. The UI shows the author,
repository and revision, opens the preview's own variable editor, and provides a separate deployment action.
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
review remains required. Host network, privileged mode, devices, added capabilities, host release tasks,
fixed host ports, production mounts and Compose preview plans are refused at admission and execution.
Dockerfile instructions run only after approval; an approved Dockerfile is trusted executable code.

Checks target the preview's own endpoint rather than inherited production URLs. A configured domain
pattern has one `{number}` placeholder. PR close cancels queued/build work, lets an atomic activation
settle, and queues resource cleanup; the environment is archived
only after that succeeds so a failed cleanup can be retried. Removal checks exact owner labels and volume/
network namespaces, removes preview containers and their anonymous volumes, then preview named volumes
and network. Ordinary run submission cannot invoke preview removal against production.
Closing a PR deletes its preview-owned volumes; this storage is disposable.

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
