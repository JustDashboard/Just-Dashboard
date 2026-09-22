# Git workspace expansion

Contracts for the operator-requested expansion of the existing Git workspace.
The repository list and three-column workspace keep their current layout, tokens, panels,
menus and dialogs. New views open in the existing preview column or an existing-style dialog.

An item is complete only after its backend, user-facing controls, relevant real-repository/API
tests and browser checks are verified. GitLab/Gitea use HTTP fixtures and GitHub uses CLI command
fixtures; live account operations require configured accounts and are recorded separately.

## Requested capabilities

- [x] Visual conflict resolution: base/current/incoming/result, resolve, continue and abort.
- [x] Partial staging and unstaging: selected hunks and individual changed lines, stale-diff checks.
- [x] Worktrees: list, create, open in Git/Terminal and safely remove.
- [x] Full branch comparison and pull-request changed-file diffs.
- [x] Pull-request comments, approval and request-changes reviews.
- [x] Reflog recovery timeline and rescue a commit into a new branch.
- [x] Blame with line-to-commit navigation.
- [x] Interactive local rebase: reorder, squash, reword, drop, continue and abort.
- [x] Existing GitHub Actions job/step logs, including failure output.
- [x] Message-only amend with no staged files.
- [x] History author filtering.
- [x] Edit remote URLs and select/change/unset upstream tracking.
- [x] Clone branch, shallow depth and sparse checkout options.
- [x] Graph search, ref filtering and older history.
- [x] GitLab and Gitea repository request integrations with account setup.
- [x] Submodule management.
- [x] Git LFS status and controls.
- [x] Patch import/export.
- [x] Commit-signature visibility.

## Verification

Verified locally on 2026-09-22: backend build/vet and tests for the changed Git/provider/API packages;
race checks for Git and both provider adapters; frontend lint, TypeScript, 122 Bun logic tests,
production build and 50 Git/design-system browser checks. New preview screenshots were inspected.
The real LFS lifecycle passed with an isolated git-lfs 3.6.1 binary on PATH; no host package was installed.
No CI workflows, release changes, commits or pushes are part of this request.

## Local history and working files

- `/conflict` returns base/current/incoming blobs, the working result and a version digest.
  Text edits require both `service.control` and `file.write`; choosing a whole side or deletion is
  destructive and requires `discard changes`. The server re-reads the index and working file before
  writing. Text is limited to 2 MiB; binary files and symlinks use whole-side selection. A submodule
  conflict records the selected gitlink in the parent index; update the child checkout afterward.
  `/operation/start` accepts merge, cherry-pick or revert, starts from a clean tree and keeps conflicts.
  Continue rejects unmerged entries. Abort is destructive with the phrase `abort operation`.
- `/patch` and `/patch/stage` expose and apply selectable raw diff line IDs. The server reconstructs
  the selected index content from its own snapshot, verifies the version and runs `git apply --cached`
  with stdin. Unstaging reverses the selection without writing the working file. File content is
  limited to 2 MiB and selectable diffs to 400 KB; binary files, gitlinks, symlinks and oversized diffs
  retain the ordinary whole-file action. Newline-at-EOF markers and quoted/Unicode paths are preserved.
- `/rebase/plan` lists 1–100 linear commits after an ancestor. The tree must be clean, on a branch and
  outside another operation. Commits reachable from fetched remote refs are refused; fetch first to
  update that knowledge. `/rebase` accepts each planned SHA exactly once with pick/reword/squash/drop,
  verifies the original head, and creates `jd-before-rebase-<timestamp>` before rewriting. First-kept
  squash is refused. The private `--git-editor` entrypoint writes only expected Git editor files from
  `jd-rebase-plan.json` in the checkout's Git metadata. No `exec` or arbitrary todo syntax is accepted.
  Conflicts use the normal resolver and continue/abort; the plan survives server restarts and is removed
  on completion/abort. Existing terminal-created rebases can also continue with their default messages.
- `/reflog` pages 100 HEAD entries. Rescue creates a new branch at the chosen commit without switching
  branches or rewriting files. `/blame` returns 200 lines and navigation to each responsible commit.
  `/signature` distinguishes unsigned, verified, bad, expired, revoked and unchecked signatures;
  verification uses the owner's Git signing configuration. An SSH signature without allowed signers is
  unchecked, never described as unsigned or verified.
- `/worktrees` lists attached checkouts and their accessibility under the roots. Creation chooses an
  existing branch or a new branch and writes only into a new folder under a resolved parent. Removal
  uses the destructive tier but no typed phrase; main/current/locked/dirty checkouts are refused.
- Remote URL edits and set/unset upstream use validated explicit argv. Clone optionally selects a
  branch/tag, shallow depth (1–100,000) and up to 100 sparse directories. Sparse setup failure leaves
  the new clone in place and reports its path. Graph pages contain up to 400 commits with literal search
  and ref filtering. Comparison file lists and diffs use frozen base/head SHAs. Message-only amend is
  supported without staging a file.

## Submodules, LFS and patches

The existing More menu opens each tool in the preview. Submodules list the expected/checked-out commits,
initialization and dirty state, with add, initialize/update, URL sync, open-in-Git, deinitialize and
remove controls. Updates explicitly select checkout mode and one literal path; file/ext transports are
disabled. Children with local changes cannot be removed or deinitialized. Git retains removed modules'
metadata for recovery. Nested modules are managed by opening the child repository in Git.

The runtime image includes `git-lfs`; local runs without it show an unavailable state. LFS offers
repository-local install, status, file/pattern lists, track/untrack, fetch and download of working
objects. It never migrates history or prunes LFS objects. Tracking edits `.gitattributes`; commit that
file with the relevant assets. LFS mutations require `service.control` plus `file.write`.

Patch exchange exports staged, unstaged tracked, or one commit's changes (first parent for merges) with full object IDs and binary
patch data, up to 2 MiB. Import first checks the supplied patch and its paths in a clean checkout, then
returns a version and file summary. Applying re-checks both, uses stdin, and can stage the result.
`/patch/check` is read-only despite its POST body; import requires `service.control` and `file.write`.
Imported paths pass through the checkout's file resolver, and Git's native apply checks remain enabled.
Patches import changes, not email authorship or a commit series.

## Provider accounts

GitHub retains its existing per-owner `gh` authentication. Its preview adds changed-file patches,
conversation pages, comment/approve/request-changes reviews tied to the viewed head, and Actions job/step
logs. Review publication uses the normal confirmation dialog. No workflow creation or modification is
performed. See [`processes-terminal-github.md`](processes-terminal-github.md#github-sign-in).

The More menu's **GitLab and Gitea** opens `forgex` request management. A `system.admin` account connects
an HTTPS base URL (with an optional server subpath), project path and token. HTTP is accepted only for
literal loopback addresses. GitLab tokens need API access; Gitea tokens need repository/issue access and
user-read access. Setup verifies the account and project before replacing the saved connection.
The whole credential record is sealed with `auth.Sealer` under a settings key derived from the resolved
checkout path and Linux UID. The public response never includes the token. Moving a checkout or changing
its owner requires reconnection. Disconnect removes the saved connection; it does not revoke the token
on the provider. Git SSH keys and credential helpers remain the source of clone/push authentication.

Both adapters list/page requests, show descriptions/diffs/conversations, open new requests, comment,
approve and merge. Gitea additionally offers request-changes reviews; GitLab uses comments for that
feedback and its project approval rules decide eligibility. Source branches must already be pushed.
GitLab supports merge/squash and Gitea merge/squash/rebase. Mutations require `service.control`, check the
viewed head and pin approvals/merges where the provider API supports it. Remote branch protections still
apply. GitLab diff pages report omitted files; Gitea reads the complete diff within the 4 MiB response
limit. Expired/insufficient tokens and provider limits are surfaced as errors with the provider link.

Provider requests have 30-second timeouts, bounded bodies and no authenticated redirects. Request data
can choose only the documented operation, branch, page and request number; it cannot supply a new API
URL outside administrator account setup. Audits record the provider operation, request number and SHA,
never token or comment text. Existing SQLite settings store the sealed account records, so no schema
migration or separate credential database is introduced.

## Verification commands and limits

Run backend build/vet and tests for `internal/gitx`, `internal/ghx`, `internal/forgex`, and `internal/api`.
Race-check the three Git/provider packages. Real repository tests cover selected lines/chunks, Unicode
and newline handling, stale edits, conflict continue/abort, worktrees, rescue/blame/signatures, rebase,
submodules and patch containment. A loopback Git daemon verifies branch/shallow/sparse cloning.
`TestLFSLifecycleUsesRepositoryConfig` needs `git-lfs` on PATH and uses only a temporary repository.
GitHub tests replace the CLI runner; GitLab/Gitea tests use local HTTP fixtures to prove payloads,
credential isolation, encryption, head checks and redirect refusal. They do not post live reviews.

Frontend verification is lint, `bunx tsc --noEmit`, `bun test src`, production build and the Git browser
specs (`git-ui.spec.ts`, `git-features.spec.ts`) plus `design-system.spec.ts`, against the freshly built
server. This verifies local behavior and mocked provider interactions. Live provider account tests and
a complete Docker image rebuild are separate operational acceptance, not implied by those checks.
