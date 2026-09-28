# Automatic deployment policy

Every normalized environment has one automatic deployment policy. Remote production Git branches
default to five-second outbound polling after the first explicit deployment. The policy is first
decided where the project is created: a Git source's draft commit may carry a `gitPolicy`
(`automatic`, `watchInclude`, `watchExclude`, `commitStatuses`), written as the environment's row at
revision 1 inside the same transaction. A commit that carries no decision writes no row, so every
other caller keeps the defaults described here exactly. Afterwards administrators edit
the policy in Settings → General's Automatic deployment section — which the project identity line's
automatic-deployment fact and the overview's source link to — or through the session-only, audited
`PUT /api/v1/deploy/{project}/environments/{environment}/git-policy`. The request contains `automatic`,
`watchInclude`, `watchExclude` and the last observed integer `revision` (initially zero). Concurrent edits
return HTTP 409. `GET .../git-watch` exposes the effective policy and the latest polling decision.

Manual-only mode rejects new branch-poll, provider-hook, signed-hook, API-trigger and scheduled deploy,
redeploy and force-build requests at transactional queue admission. Manual actions and retries remain
available. Already accepted runs continue; cancel those separately when needed. Scheduled maintenance
such as a restart and explicitly approved preview environments keep their separate lifecycle contracts.

For remote production branches, polling and hooks use `GitWatcher` to observe the configured remote ref,
freeze its full object ID and apply the same complete Git tree comparison against the most recent
attempted deployment (or the saved source identity before the first run). The webhook's repository and
branch must match the saved source. A late event for an older revision is suppressed rather than deployed.
Provider-supplied changed-path arrays are not trusted as complete. Generic signed hooks observe the same
remote branch and policy. Local/image sources have no branch to poll; their hooks still respect
manual-only mode and configured path restrictions.

Watch patterns are repository-relative, one glob per line in the UI. An empty include list accepts any
path, excludes take priority, and `directory/**` includes all descendants. Ordinary `*`, `?` and bracket
patterns follow Go `path.Match`; `*` does not cross a slash. Patterns are limited to 64 per list and 512
bytes each. Parent traversal, absolute paths and malformed patterns are rejected. A filtered empty
change set is suppressed. Excluded commits remain in the baseline comparison until a later deployment,
so an unrelated commit cannot hide a previously relevant unbuilt change.

The source adapter reads trees in a separate private bare mirror, with isolated Git credentials and
explicit `hostexec` argv. It does not check out code or run repository hooks. Comparison is bounded to
30 seconds, 1 MiB and 10,000 paths; missing commits, force-push history unavailable from the remote, or
truncated results pause filtered automation with `changes_unavailable`. Full commits are compared with
rename detection disabled, so both the old and new paths participate. Unfiltered branch monitoring
continues to use `ls-remote` without fetching trees.

Existing installations inherit consistent enabled push-hook filters until an administrator saves an
explicit environment policy. Conflicting legacy filters pause automation and are shown in the editor.
For compatible API clients, explicitly writing a push hook's watch lists atomically updates the shared
policy while preserving manual-only mode. Omitting the lists leaves the policy unchanged. Removing a
hook does not erase an explicitly saved policy. Preview-only hooks retain their own filters.

The additive `deploy_git_policies` table stores policy revisions. `deploy_git_watches.reason` and
`policy_key` and `baseline_revision` migrate with defaults and persist relevant, ignored, unavailable and failed-queue outcomes;
webhook deliveries retain their own reason. A policy digest is checked again in the enqueue transaction
to reject a decision made before a concurrent policy edit. Polling and hooks share the existing latest
attempted-commit fence, allowing force-push reversions while preventing duplicate builds and repeated
attempts of a failed unchanged commit. Decision recording uses the cursor generation so competing
observations cannot overwrite each other. Cached ignored decisions are re-evaluated when a manual
deployment changes the comparison baseline.

Verification: `git_policy_test.go`, `git_changed_paths_test.go`,
`handlers_deploy_git_policy_test.go`, and the browser deployment-policy flow cover shared decisions,
manual-only admission, private comparison failures, changed/deleted/renamed paths, stale deliveries,
concurrent enqueue, policy revisions, authorization and audit evidence.
