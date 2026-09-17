# Git, backups, and host users

## Git working copies

`internal/gitx` discovers repositories at most five levels below `JD_GIT_ROOTS`, skips generated and
hidden trees, and stops descending once it finds `.git` — a `.git` *file* counts as much as a directory,
because that is what a linked worktree and a submodule checkout carry. Summaries are read four at a time,
so a server with thirty checkouts answers the list inside its poll interval. `Resolve` cleans and
symlink-resolves every repository path, checks the configured roots, and verifies either a normal `.git`
directory or worktree file; `ResolveDir` is the containment half alone, for a clone's parent or an
`init` target that is not a repository yet. Remote URLs are scrubbed before they are returned because
credentials embedded in HTTPS remotes must not reach the list page.

The read surface reports repository summary/status, commit history, local and remote branches, tags,
stashes, remotes, one commit's detail, a two-branch comparison, diffs, and a bounded topological graph
whose lane layout spans branches and tags. Status is read from `git status --porcelain=v1 -z`: the
NUL-separated form is the one whose paths are literal, and without it a file named `café.txt` arrived
quoted and octal-escaped, which the page showed verbatim and then could not stage. A file changed on both
sides — staged, then edited again — is one entry with both `staged` and `unstaged` set, and the two
change lists draw it once each with the letter for that side; the unmerged codes that carry no `U` (`AA`,
`DD`) are read as conflicts rather than as additions ready to commit. The summary names the branch
through `symbolic-ref`, so a fresh `git init` reads as `main` with no commits rather than as no branch,
and carries the upstream, whether it is gone, and the staged/untracked/conflict breakdown. Status also
reports the committer identity git resolves here and any merge, rebase, revert, cherry-pick or bisect a
shell left half-finished. History takes a limit and a skip for paging, a literal case-insensitive search
over messages (`--grep` inside one argument, so a dashed term is a term), an author filter, and a single
path followed across renames. `Show` reads one commit's record, body and changed files (status letters
and line counts from two NUL-separated `diff-tree` reads against the first parent, or the empty tree
for a root commit); `Compare` counts what `head` has that `base` lacks and the merge-base diffstat. A
commit's diff is its first-parent diff with no header, for a merge as much as for an ordinary commit,
and is truncated at a line boundary. A diff of an untracked path shows it as the addition it will be:
`git diff` has nothing to say about a file with no earlier version, so `Diff` lists the path with
`ls-files --others` (a directory becomes the files in it) and runs `diff --no-index` against `/dev/null`
for each, accepting the exit status of 1 that means "these differ". The terminal uses `/git/detect` to
find the checkout containing its current directory while keeping "not a repository" and "outside the
configured roots" as honest non-error states.

All Git subprocesses receive explicit argv and run as the checkout owner through `hostexec.AsOwner`, so a
web operation does not leave root-owned files. They run with `GIT_OPTIONAL_LOCKS=0`, so a page polling
`status` never takes `.git/index.lock` from under a commit typed in a terminal, and with `GIT_EDITOR=true`,
so no operation can open an editor the request cannot drive. Refs reject leading dashes, traversal-like
`..`, invalid characters, and `.lock` suffixes (`~` and `^` are allowed, for `HEAD~1`); a working-tree
path is judged by segment, so `v1..v2.diff` is an ordinary file, and rejects absolute, traversing and
option-shaped values behind an explicit `--`. A stash is addressed by integer index and the `stash@{N}`
form is built server-side, because the braces are exactly what the ref validator refuses. Remote URLs
accept `https://`, `http://`, `ssh://`, `git://` and the scp-like `user@host:path` and nothing that could be
a local path or an option; a clone's directory name is one path segment that is neither hidden nor
option-shaped. Pull is fast-forward-only; push never forces and establishes a missing upstream on the
branch's own remote; checkout never forces, and a remote branch is checked out as a new local branch
that tracks it; merge, revert and cherry-pick run their own `--abort` on failure, so a conflict leaves the
tree exactly as it was and git's own message says which files clashed; branch deletion defaults to Git's
merged-only mode; stash includes untracked files; discard restores a tracked path from the index and
deletes an untracked one (`clean -fd`); a hard reset may also clean untracked files; an identity is
written into the repository's own config; a clone lands only in a new directory under a root, as the
root's owner, and `init` starts an existing directory on `main`.

Route capabilities reflect recoverability: reads require `read`; fetch/pull/push (and pushing tags),
checkout, branch create/rename, merge, revert, cherry-pick, tag create, stash push/pop/apply,
stage/unstage/commit, identity, adding a remote, clone and init require `service.control`; discard, reset,
stash drop, and deleting a branch, a remote branch, a tag or a remote pass through `s.destructive`.
Discard, hard reset and stash drop require typed confirmation because they overwrite or throw away
uncommitted work; the deletions use ordinary confirmation because Git preserves the commits in
reflogs/remotes. Every mutation lands in the audit log as `git.<verb>` with the repository it touched.
`api/handlers_git_test.go` drives the routes against a real repository and pins the phrase policy and the
capability tiers. GitHub authentication, pull requests and workflow runs are detailed in
[`processes-terminal-github.md`](processes-terminal-github.md#github-sign-in).

## Backups

Backup sources and local targets pass through the configured file resolver on create/update and again
before test/execution. Local artifacts are independently contained for inspection, restore and
retention. Existing invalid jobs remain viewable, editable and deletable; a stored path never overrides
a later root restriction. Each run uses a private staging directory and an artifact name containing
job/run IDs plus randomness. Artifact creation and publication refuse collisions instead of truncating
an existing archive.

`internal/backups` stores job definitions and run history in SQLite. A job has source paths, exclusion
globs, a local/S3/Backblaze-B2 destination, a five-field cron schedule, an enabled flag, and a retention
count. Displayable target configuration is separate from access keys; credentials are sealed by
`auth.Sealer` and never returned by the API. The target-test route checks local writability or object-store
bucket access before the operator depends on a schedule.

`Scheduler` rebuilds its in-memory robfig/cron entries from enabled jobs at start and after edits. Manual
and scheduled runs share `Runner.Execute`; a per-job running set prevents overlap. Execution records a run,
streams a gzip-compressed tar archive to a `0600` staging file, fails on unreadable or changing files,
applies exclusions to both full paths and basenames, then moves locally or uploads through the
AWS S3 multipart client. B2 uses the same client with its endpoint and path-style addressing. Run logs are
bounded before persistence.

New runs persist an immutable manifest of resolved sources, exclusions, destination and SHA-256 artifact
digest. Each source has a separate `source-0001`, `source-0002`, etc. archive root, so identically named
Docker volume directories restore without overwriting one another. Listing and restore verify the
artifact digest and use the run's original destination even after job edits. Legacy runs remain readable
but cannot prove deployment coverage. Jobs may explicitly select SQLite files for native `VACUUM INTO`
capture, with snapshot digests in the manifest and live sidecars omitted. Jobs may also name saved
database connections (`database_dumps`): each run asks the Databases owner for a native dump of every
one (`dbx.Dump`: pg_dump, mysqldump, mongodump, Redis, or the built-in driver dump) into `database-NNNN/`
inside the archive and records connection, engine, method, file, digest and size in the manifest; a
failed dump fails the run. `POST /backups/runs/{runID}/restore-database` (destructive, typed
confirmation of the target database name) extracts one recorded dump bounded by its manifest size,
verifies its digest and restores it through `dbx.Restore` into the connection or a named drill
database. Other sources retain ordinary filesystem capture. See
[`../deployments/backup-coverage.md`](../deployments/backup-coverage.md).

An optional recovery plan runs a pinned application image against a private extracted copy with no
network access or production mounts. Check status, exact-artifact evidence and cleanup results are
persisted in `backup_restore_tests` and shown in run history. Recovery can run after each backup, manually
through the admin `POST /backups/runs/{runID}/verify-restore`, or to satisfy a deployment gate. Existing
destination restores retain destructive/typed confirmation. See
[`../deployments/restore-verification.md`](../deployments/restore-verification.md) for the checker contract
and supported consistency protocols.

Retention deletes the oldest successful artifacts and their run rows, but skips an artifact while archive
listing or restore holds a read reference; deletion reserves an exclusive lease against new readers.
Active or cleanup-pending recovery records also block pruning and job deletion. Cross-device local moves
fall back from rename to copy. Deleting a job deliberately leaves its existing artifacts alone.

Archive listing is bounded and does not extract. Restore requires a successful artifact, downloads remote
objects into private staging, refuses `/`, and is typed-confirmed with the destination. The API resolves the
destination through `files.Resolve`; extraction uses `safepath` for every directory, regular file, and
symlink, refusing traversal and unsafe link targets. Backup-before-deploy behavior and restore-evidence
limits are covered in [`../deployments/implementation.md`](../deployments/implementation.md).

## Host users and SSH keys

`internal/linuxusers` manages local operating-system accounts, not dashboard identities. The entire
`/system-users` route tree requires `system.admin`, including reads, because it exposes shells, groups,
login history, lock state, and SSH-key counts. Account state is assembled from the mounted host
`/etc/passwd`, `/etc/group`, `/etc/shadow`, `lastlog`, and each home directory's `authorized_keys`; system
accounts are hidden by default in the UI.

Usernames and group names follow a closed 32-character Unix pattern. New accounts are created with a
locked password, so access must be added deliberately with a public key; shells must be absolute, comments
cannot inject passwd fields, and the group picker comes from the host group database. A protected-account
set cannot be deleted or locked. Account deletion is destructive and typed with the username, especially
because it may remove the home directory; key removal is destructive but ordinarily confirmed.

SSH public keys are parsed with `x/crypto/ssh`, reject private/multi-line input, expose SHA-256 fingerprints,
and de-duplicate by fingerprint. Home traversal and `.ssh`/`authorized_keys` opens refuse symlinks.
Directory descriptors anchor reads and atomic replacements even if the account renames a directory.
New `.ssh` directories belong to the account at `0700`; existing directories must already belong to it
before their mode is tightened. Keys are written into exclusive random `0600` files and renamed into
place, so a preexisting symlink or hard link cannot redirect a privileged write or ownership change.
Removal matches a fingerprint and retains comments and unrecognised lines. Reads are bounded to 2 MiB.

The current host-execution implementation and its relationship to the documented invariant are recorded in
[`../reference/verification-findings.md`](../reference/verification-findings.md).
