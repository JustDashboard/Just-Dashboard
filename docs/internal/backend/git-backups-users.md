# Git, backups, and host users

## Git working copies

The frontend keeps a commit-message draft per checkout for the tab, walks changed files with
Alt+Up/Down, and returns focus to the selected file when Escape closes its preview. These read and
navigation interactions are specified in [workspace interactions](../frontend/workspace-interactions.md).

Inline file edits keep each draft and its opened content/revision in memory per checkout and path.
Selecting another file or preview, History, Blame, Close and dashboard navigation preserve them;
the workspace's unsaved-file count reopens a held draft. Contents never reach browser storage or the
URL. Save uses the Files revision precondition, retains the draft on `409 content_conflict`, and
offers **Review current file** or confirmed discard/reload. A successful save or confirmed discard
clears that file's held draft. The unload guard remains active while any repository has a draft,
including previews closed or left on another page; signing out clears the memory store and guard.
A reload or closed tab still loses those in-memory drafts after the browser's confirmation.
`git-features.spec.ts` checks preserved drafts, stale revisions, storage exclusion and unload cleanup.

`internal/gitx` discovers repositories at most five levels below `JD_GIT_ROOTS`, skips generated and
hidden trees, and stops descending once it finds `.git` — a `.git` *file* counts as much as a directory,
because that is what a linked worktree and a submodule checkout carry. Hidden directories are where
worktrees usually live (`.worktrees/<task>`, `.claude/worktrees/<task>`), so every checkout found also
contributes the linked worktrees its repository records, read from `.git/worktrees/*/gitdir` without a
git process: those whose checkout still exists and resolves inside the roots are added, compared by
resolved path so a symlinked root does not list one twice, and a worktree whose directory is gone (what
`git worktree prune` would clear) is left out. Summaries are read four at a time, so a server with
thirty checkouts answers the list inside its poll interval.

A summary says whether the checkout is a linked worktree (`worktree`) and, when it is, the main
checkout it belongs to (`main`), from the files git keeps: a `.git` file whose git directory holds a
`commondir` is a worktree, while a submodule's `.git/modules/<name>` has none; the main checkout is the
directory holding the common `.git` (a bare repository's worktrees have none). The list alone also
carries `languages` — at most six `{name, share}` pairs, largest first, measured by the bytes of tracked
files (`git ls-files` and a stat each, the first 50,000 files) in the languages the page has a mark
for, by extension and by `Dockerfile`'s name. Vendored and generated paths (`node_modules/`,
`vendor/`, `third_party/`, `dist/`, `build/`, minified `.js` and `.css`) and prose, data and
configuration (Markdown, JSON, YAML, lockfiles, images) are left out as Linguist leaves them out of a
repository's language bar; a language under one per cent is dropped unless it is the only one, and
shares are rounded down so they never add up past the whole. The answer is cached per checkout against
the HEAD it was measured at, so the minute's poll re-reads it only after HEAD moves. `Resolve` cleans and
symlink-resolves every repository path, checks the configured roots, and verifies either a normal `.git`
directory or worktree file; `ResolveDir` is the containment half alone, for a clone's parent or an
`init` target that is not a repository yet. Remote URLs are scrubbed before they are returned because
credentials embedded in HTTPS remotes must not reach the list page.

Summary reads combine the abbreviated HEAD with commit metadata in one `log` call and use
`for-each-ref`'s upstream tracking counts for divergence. A normal attached summary needs five Git
commands, with fresh status on every request. Empty repositories, detached HEAD and gone upstreams
retain their previous meanings; a failed metadata read can still recover HEAD with `rev-parse`.

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
reports the committer identity git resolves here and any merge, rebase, revert, cherry-pick or bisect
in progress, including operations started by the workspace. History takes a limit and a skip for paging, a literal case-insensitive search
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
web operation does not leave root-owned files; what another root process left behind is caught at Pull
(below). They run with `GIT_OPTIONAL_LOCKS=0`, so a page polling
`status` never takes `.git/index.lock` from under a commit typed in a terminal, and with `GIT_EDITOR=true`,
so no operation can open an interactive editor. Local rebase instead uses the server's private
`--git-editor` entrypoint with a validated JSON plan; caller text never becomes an editor command.
Refs reject leading dashes, traversal-like
`..`, invalid characters, and `.lock` suffixes (`~` and `^` are allowed, for `HEAD~1`); a working-tree
path is judged by segment, so `v1..v2.diff` is an ordinary file, and rejects absolute, traversing and
option-shaped values behind an explicit `--`. A stash is addressed by integer index and the `stash@{N}`
form is built server-side, because the braces are exactly what the ref validator refuses. Remote URLs
accept `https://`, `http://`, `ssh://`, `git://` and the scp-like `user@host:path` and nothing that could be
a local path or an option; a clone's directory name is one path segment that is neither hidden nor
option-shaped. Pull is fast-forward-only and preserves a configured upstream, including one that is
gone or has not been fetched. Without a merge ref, it checks the remotes for the current branch's exact
name and establishes tracking with `pull --ff-only --set-upstream`. A configured branch remote takes
precedence over `checkout.defaultRemote`; otherwise exactly one remote must have that branch. Remote
lookups use `ls-remote`, so stale or missing fetched refs cannot choose an upstream. Missing or ambiguous
matches direct the operator to **Branches → Set upstream**; an unpublished branch can be pushed first.
Lookup failures stop the pull. The whole operation keeps its three-minute deadline, and Git still
refuses divergent history or overwriting local edits. A pull refused for local work, or one that would
write into a folder another account owns, answers `409 pull_blocked` with `blocked` beside the error:
git's own `changed` and `untracked` lists, or the `unwritable` folders (path and owner) with the `owner`
git runs as. The folder check runs only when a directory holding a file — tracked, or untracked and not
ignored, listed by `ls-files --cached --others --exclude-standard --directory` so an untracked or empty
folder counts once — belongs to someone else; untracked files count because a folder holding only copies
of what the pull adds is where the pull lands them, and missing it offered Discard, which git could not
carry out in that folder either. It then fetches what the pull would merge and refuses only when the incoming paths land in such a
folder, because git writes every file it can, stops at the first it cannot, and leaves the written
ones as local changes that block every later pull. Three routes take the ways past it: `POST
/git/pull/stash` stashes everything, untracked included, and pulls; `POST /git/pull/discard` (inside
`s.destructive`) restores exactly the files git named to HEAD, deletes the untracked ones it named and
pulls; `POST /git/pull/own` (also `file.write`) hands the blocking folders to the checkout's owner
through `hostexec.GiveBack` — `openat` with `O_NOFOLLOW` for every component and `fchown` on the
descriptor, under a root that must already be the owner's — audits them with their previous owners as
`git.pull.own`, and pulls. Each works out again on the server what is in the way; none takes a path
from the request. Push never forces and establishes a missing
upstream on the branch's own remote; checkout never forces, and a remote branch is checked out as a new
local branch that tracks it, or, when that local branch already exists, as a switch to it followed by a
fast-forward to the remote branch (`/git/checkout` with `update`, and `from` naming the remote branch). A
local branch behind its upstream takes the same switch from its own row. Being unable to fast-forward does
not fail the switch: git's reason comes back beside it. The legacy merge, revert and cherry-pick endpoints still abort on failure.
The workspace uses `/operation/start`, which requires a clean checkout and retains conflicts for the
visual resolver, `/operation/continue` and ordinarily confirmed `/operation/abort`. Branch deletion
defaults to Git's merged-only mode; stash includes untracked files; discard restores a tracked path from the index and
deletes an untracked one (`clean -fd`); a hard reset may also clean untracked files; an identity is
written into the repository's own config; a clone lands only in a new directory under a root, as the
root's owner, and `init` starts an existing directory on `main`.

**Match origin** makes a checkout look like a fresh copy of its remote in one confirmed step, for the
server whose list has grown to hundreds of finished branches. The page fetches with prune, then reads
`GET /git/match-remote` (read tier). The remote is the current branch's, then `origin`, then the only one
there is. The answer sorts every local branch by what acting on it would lose: `update` and `delete` hold
nothing the remote lacks, judged by whether the tip is in `rev-list --branches --not --remotes=<remote>`,
one process for any number of branches; `unique` holds commits no ref of the remote reaches, each with its
count; `inUse` is checked out by another worktree. A branch following another remote that still has it is
left out. `staleRefs` are remote-tracking refs under a name no configured remote claims, which no fetch can
update or prune. `blocked` says why nothing can happen yet: no remote, an operation in progress, or a
detached HEAD on commits the remote lacks.

`POST /git/match-remote` (inside `s.destructive`, ordinary confirmation) reads the plan again rather than
taking it from the request. The request only names the `unique` branches to discard, each with the commit
the operator saw, so a commit made after the dialog opened keeps its branch. A named branch is deleted, or
moved back to its remote branch if there is one. The run stashes uncommitted work (untracked included) only
when the working tree has to move, moves other branches with `update-ref` against the commit it read, moves
the branch it will land on before switching so the tree goes straight to its final state, switches to the
remote's default branch, fast-forwards it if it was already current, deletes branches and then stale refs
in batches of 100. A linked worktree cannot leave its branch, so it updates or keeps that branch and
switches nothing. Tags and existing stashes are not touched. A failure stops the run and answers with what
had already happened. It is audited as `git.match.remote`.

Deleting a checkout from the server starts with `GET /git/removal` (read tier), which answers what the
delete would lose: `changes`, `untracked`, `conflicts`, `stashes`, `unpushed` (commits on local
branches no remote-tracking ref reaches — with no remote, every commit), `localBranches` (each branch
holding such commits, its count and upstream, at most 50), `remotes`, the main checkout's linked
`worktrees` (path, branch, dirty), `nested` checkouts inside its directory (an independent repository
or another repository's worktree; its own submodules and worktrees are part of it), and `protected` —
the sentence saying why the delete would be refused. A linked worktree's stashes and branches belong to
its repository and survive it, so for one only the uncommitted counts are filled. `POST
/git/repository/delete` (`{path}`, inside `s.destructive`) re-reads the same answer and refuses with
`409 protected` a configured root itself, the checkout this dashboard was installed from
(`JD_UPDATE_DIR`) or any directory holding it, a main checkout with linked worktrees still attached
(they are named), a checkout with other checkouts inside it, and a locked worktree — before it asks for
the phrase, so nobody types a name for a delete that would not happen. It then requires the typed
phrase of the checkout's directory name (`428`/`412` without or with the wrong one; invariant 3). A
linked worktree is removed through its repository — `git worktree remove --force` in the main
checkout, then `git worktree prune` — so no record of it is left; anything else is deleted from disk.
It is audited as `git.repository.delete`.

Route capabilities reflect recoverability: reads require `read`; fetch/pull/push (and pushing tags), stash-and-pull,
checkout, branch create/rename, merge, revert, cherry-pick, tag create, stash push/pop/apply,
stage/unstage/commit, identity, adding a remote, clone and init require `service.control`, and giving a
blocked pull's folders back also `file.write`; discard, reset, discard-and-pull, stash drop, deleting a branch, a remote branch, a tag or a remote, matching a checkout to its remote, and deleting a checkout pass through
`s.destructive`. Discard, hard reset, stash drop, and Git deletions use ordinary confirmation; deleting
a whole checkout is the one Git route that takes a typed phrase. Every mutation lands in
the audit log as `git.<verb>` with the repository it touched.
`api/handlers_git_test.go` drives the routes against a real repository and pins the confirmation policy and the
capability tiers. GitHub authentication, pull requests and workflow runs are detailed in
[`processes-terminal-github.md`](processes-terminal-github.md#github-sign-in).

The expanded reads, mutation capabilities, consistency guards and operational limits are documented in
[`git-workspace-expansion.md`](git-workspace-expansion.md). Message-only amend uses the existing commit
endpoint. Branch comparisons now include changed files and frozen base/head SHAs; the diff endpoint uses
those revisions. Graph search/ref filtering narrow the graph and `skip`/`hasMore` page through older
history. Every page is laid out from the newest commit and then sliced, so a lane keeps its column
across a page boundary and each edge carries the lane it travels (`parentLanes`); paging stops 5,000
commits deep, and the first page carries `total` from `git rev-list --count`. A search result is one
lane with no edges, because a `--grep` match's parents are mostly commits that did not match. Git commands
receiving file pathspecs treat them literally; this is deliberately not a global environment setting,
because `git stash` uses its own special pathspecs internally. Dashboard Git mutations are serialized
per checkout, with Git's native locks still protecting against terminal clients.

## Backups

The job frontend selects runs through `?run=` and remembers archive selection per run. Shift ranges
and keyboard selection operate on the rendered entries; review and restore still use the route
contracts below. See [workspace interactions](../frontend/workspace-interactions.md).

Backup sources and local targets pass through the configured file resolver on create/update and again
before test/execution. Local artifacts are independently contained for inspection, restore and
retention. Existing invalid jobs remain viewable, editable and deletable; a stored path never overrides
a later root restriction. Each run uses a private staging directory and an artifact name containing
job/run IDs plus randomness. Artifact creation and publication refuse collisions instead of truncating
an existing archive.

`internal/backups` stores job definitions and run history in SQLite. A job has source paths, exclusion
globs, a destination (a directory here, S3, Backblaze B2, an SFTP server or an rclone remote — see
[SFTP and rclone destinations](#sftp-and-rclone-destinations)), a five-field cron schedule (validated by `Job.Validate`, so no
caller can store an expression that never fires), an enabled flag, a retention count, a retention age in
days (`retention_days`, additive), and the containers to pause for the archive step (`pause_containers`,
additive; names are trimmed and refused when option-shaped). `POST /backups/{id}/enabled` pauses or
resumes the schedule alone — audited as `backup.job.pause`/`backup.job.resume`, and never re-sending the
definition or touching sealed keys. The list and single-job reads carry the figures the page draws:
`stored` (retained successful artifacts and their bytes) and `lastSuccessAt`. The automatic overdue
detector and its response field were removed in 0.7.5. Displayable target configuration is separate
from access keys; credentials are sealed by
`auth.Sealer` and never returned by the API. A job may also encrypt its archives (see
[Archive encryption](#archive-encryption)). The target-test route checks local writability, object-store
bucket access, an SFTP sign-in and a probe file, or an rclone remote's write access before the operator
depends on a schedule.

`Scheduler` rebuilds its in-memory robfig/cron entries from enabled jobs at start and after edits. Manual
and scheduled runs share `Runner.Execute`; a per-job running set prevents overlap. Execution records a run,
streams a gzip-compressed tar archive to a `0600` staging file, fails on unreadable or changing files,
applies exclusions to both full paths and basenames, then moves locally or uploads through the
AWS S3 multipart client, SFTP or rclone. A source may contain the staging directory only when an exclusion covers it —
the dashboard's own data directory is the one source where that is legitimate, and the suggested job
excludes `staging`, `deployment-workspaces` and `install.env` (the copy of the key a kept uninstall leaves
beside the data, which must not travel in an archive of that data). Database dumps are taken first, while the engines can
still answer; then the job's containers are paused through `ContainerPauser` (the Docker owner's
`Lifecycle` pause/unpause, wired in `modules.go`) for exactly the filesystem walk, and resumed with a
context that cannot be cancelled, whatever the walk did. A stopped container needs no pause and is
logged as skipped; one already paused is left paused; any other refusal fails the run before a byte is
archived, and a job naming containers with no Docker owner fails with `ErrPauseUnavailable` rather than
archiving a volume it promised would be quiet. The manifest records `pausedContainers`. B2 uses the same client with its endpoint and path-style addressing. Run logs are
bounded before persistence.

New runs persist an immutable manifest of resolved sources, exclusions, destination and SHA-256 artifact
digest, and since 0.8.2 its origin (this install's random identity, the host name, the job and the
run), the archive's size and which sources are Docker volumes; the same manifest is written beside the
archive at the destination (see [Importing what a destination holds](#importing-what-a-destination-holds)).
Each source has a separate `source-0001`, `source-0002`, etc. archive root, so identically named
Docker volume directories restore without overwriting one another. Listing and restore verify the
artifact digest and use the run's original destination even after job edits. Legacy runs remain readable
but cannot prove deployment coverage. Jobs may explicitly select SQLite files for native `VACUUM INTO`
capture, with snapshot digests in the manifest and live sidecars omitted. Jobs may also name saved
database connections (`database_dumps`): each run asks the Databases owner for a native dump of every
one (`dbx.Dump`: pg_dump, mysqldump, mongodump, or the built-in driver dump, which for Redis is gzipped
JSON Lines of each key's `DUMP` payload and not an RDB file) into `database-NNNN/`
inside the archive and records connection, engine, method, file, digest and size in the manifest; a
failed dump fails the run. The Databases fleet and a connection's summary read those manifests back
(`Store.LastDatabaseDumps`): the start of the newest successful run whose manifest covers a
connection's dump is when a job last backed it up. `POST /backups/runs/{runID}/restore-database`
(destructive, ordinary confirmation) extracts one recorded dump bounded by its manifest size,
verifies its digest and restores it through `dbx.Restore` into the connection or a named drill
database. The extraction is `Runner.ExtractDatabaseDump`, which a deployment's migration rehearsal
also uses on the newest successful run that dumped the connection (`Store.LatestDatabaseDump`) to
fill its throwaway copy ([implementation](../deployments/implementation.md)). Other sources retain
ordinary filesystem capture. See
[`../deployments/backup-coverage.md`](../deployments/backup-coverage.md).

An optional recovery plan runs a pinned application image against a private extracted copy with no
network access or production mounts. Check status, exact-artifact evidence and cleanup results are
persisted in `backup_restore_tests` and shown in run history. Recovery can run after each backup, manually
through the admin `POST /backups/runs/{runID}/verify-restore`, or to satisfy a deployment gate. Existing
destination restores retain destructive capability and ordinary confirmation. See
[`../deployments/restore-verification.md`](../deployments/restore-verification.md) for the checker contract
and supported consistency protocols.

Retention deletes the artifacts beyond the keep count or older than the keep age (`pruneCandidates`),
the manifests beside them and their run rows, but never an imported job's or run's, and never the newest
successful artifact — a job whose schedule quietly stopped
firing must not age its last good backup away — and skips an artifact while archive listing or restore
holds a read reference; deletion reserves an exclusive lease against new readers.
Active or cleanup-pending recovery records also block pruning and job deletion. Cross-device local moves
fall back from rename to copy. Deleting a job deliberately leaves its existing artifacts alone.

Archive listing is bounded and does not extract. Restore requires a successful artifact, downloads remote
objects into private staging (an imported run's from the destination it was imported from, or from the
copy the import kept here while that still proves its digest), refuses `/`, and uses ordinary confirmation. The API resolves the
destination through `files.Resolve`; extraction uses `safepath` for every directory, regular file, and
symlink, refusing traversal and unsafe link targets. `restoreRequest` also takes `paths` — archive
entries (`source-0001/etc`, one file) that narrow the restore to themselves and what lies under them —
and `inPlace`, which `Runner.RestoreInPlace` answers by mapping each recorded `source-NNNN` root back
onto the path the manifest says it came from, resolving every original path through `files.Resolve`
again so a root restriction added since the backup still holds; it needs a complete manifest, so a
legacy archive can only be restored into a directory. A recorded source may be an individual file or
a directory: extraction uses the archive entry's type, restores a file's bytes at its original path,
and creates missing parents as needed. An existing directory at a file's path is an error, and a
filtered-out source is not created. A source the manifest records as a Docker volume is restored into
that volume's directory on this host, which the Docker owner (`backupVolumeProvisioner`) creates with the
volume's recorded labels when it does not exist, after the archive has been fetched and proved and before
a byte is written; without Docker such a restore is refused rather than made as a bare directory Docker
would not know. `GET /backups/runs/{runID}/restore-plan` answers, from the manifest alone, where each
source would go and what the restore would make (`creates: "volume"` or `"directory"`) or overwrite, and
the restore dialog shows it before its confirmation. Database dumps are never
written to disk by an in-place restore; they go back through the Databases owner as before.
`GET /backups/runs/{runID}/download` streams the verified artifact (`Runner.OpenArtifact`: the local
file, or a private download of a remote one, checksum proven first, decrypted when the run was
encrypted) as an attachment; it sits with the administrator routes, because an archive is the whole of
what it covers, and is audited as `backup.download`.

The Backups page keeps failed job and resource-report reads visible even with older data. New
protection actions are unavailable while coverage is stale; existing job logs, rerun, target tests,
configuration and recovery controls retain their owner and capability boundaries.

`GET /backups/resources` (`backup_resources.go`) is the coverage report: what the other owners hold that
a job could protect — the dashboard's data directory (with its SQLite file as a snapshot suggestion),
the nginx directory and the Caddyfile's directory when they exist, local Docker volumes with the
containers mounting them, compose stacks by working directory with their running containers, deployment
checkouts (by name, with the project's id for its mark), discovered Git repositories, and saved database connections (a SQLite connection also as its
file). Each carries the job it suggests (name, sources, excludes, SQLite paths, native dumps, containers
to pause) and the jobs that already cover it: a file resource is covered when every path sits under a
job source, a database when a job dumps it or, for SQLite, snapshots its file; only an enabled covering
job counts as protection. Owners that cannot answer are named in `unavailable` rather than read as
empty, and unprotected resources sort first. Backup-before-deploy behavior and restore-evidence
limits are covered in [`../deployments/implementation.md`](../deployments/implementation.md).

## SFTP and rclone destinations

Both are reached only during a transfer: `remote_targets.go` holds the transports and
`objectstore.go` the dispatch (`uploadArtifact`, `downloadArtifact`, `deleteArtifact`, `TestTarget`) that
the runner, retention and restore share with S3. An archive lands at `<path>/<name>` on the server or in
the remote; the run's manifest records the destination as it was, so a restore reads the archive where it
was written even after the job is edited.

**SFTP.** `target` holds `host`, `port` (22 when omitted), `username` and `path` (under the account's home
unless absolute); the sealed secrets hold a `password`, a `privateKey` (OpenSSH or PEM) with its
`passphrase`, or both, and a new job needs one of them. Sign-in uses `golang.org/x/crypto/ssh` and
`github.com/pkg/sftp`; an upload is written as `<name>.partial`, size-checked and renamed, so an
interrupted one never looks like a backup. The server's host key is pinned on first contact — by the
target test, or by the first run when nobody tested — and only after the server accepted the
credentials, so a server that cannot be signed in to pins nothing. The pin (`hostKey`, an authorized-keys
line, and `hostKeyFingerprint`, SHA256) lives in `target`; the client asks for the strongest key kind
(Ed25519, then ECDSA, then RSA) when nothing is pinned and for the pinned kind afterwards, the way OpenSSH
uses known_hosts. A server presenting another key is refused before any credential is sent, and the run
log names both fingerprints. An edit of the same host and port keeps the pin; another server is pinned
afresh; `forgetHostKey: true` on `PUT /backups/{id}` drops it for a reinstalled server. The test response
carries `hostKeyFingerprint` and, the first time, `hostKeyPinned`, audited as `backup.target.pin`, so the
page can show the fingerprint to compare with the server's own. The run that pins writes the key into its
own manifest, and a restore or prune of an archive whose server the job no longer names, never pinned, is
refused rather than trusted.

**rclone.** The secret is one remote's section as `rclone config show <name>` prints it (OAuth tokens
included); `ParseRcloneRemote` keeps its options and the page names the service from `remoteType`, read
from its `type`. Because rclone can reach this server's own disk, the configuration is vetted: only
storage backends are accepted (`local` and the wrappers that take a bare path — `alias`, `crypt`, `union`,
`combine`, `chunker`, `hasher`, `compress`, `cache` — are refused), and so are options that name a file
here or run a program (any key containing `file`, `command` or `socket`, and `ssh`) and `env_auth`, which
would sign in with the dashboard's own environment. Every command runs the image's own rclone (the
`rclone` stage of `backend/Dockerfile`, a pinned release checked against its published SHA-256 sums) with
a private `0600` configuration written for that command alone, a private cache directory, `HOME` and
`TMPDIR` in the same temporary directory, and no environment from the dashboard, so no `RCLONE_*`
variable reaches it. `copyto` uploads and restores, `deletefile` prunes, the test runs `mkdir`,
`rcat` and `deletefile` on a probe, and a catalogue lists the folder with `lsjson` and reads with `cat`.

## Archive encryption

A job's `encrypt` setting (additive column, default 0, so a job that predates it keeps writing what
it always wrote) seals each new archive before it is written to the destination. A new job takes
`true` when its destination leaves the server (S3, B2, SFTP or rclone) and `false` for a directory on
this server, unless the request
says otherwise; `PUT /backups/{id}` without the field keeps the stored value, so an older caller cannot
turn encryption off by omission. Archives already taken keep the form they were written in, whichever
way the setting is changed later.

The format is [age](https://age-encryption.org/v1) (`filippo.io/age`): the gzip-compressed tar is
streamed through ChaCha20-Poly1305 in 64 KiB chunks, each authenticated on its own, with a last-chunk
flag that makes a truncated file an error rather than a shorter archive. Nothing plaintext reaches the
staging directory — the tar, the gzip and the encryption are one stream into the staging file — and
the artifact's name ends in `.tar.gz.age`, so a bucket listing says which objects need the key. age
rather than a format of the dashboard's own, because the reason to export the key is to open an archive
where this dashboard is not, and the `age` command is packaged for every system that archive is likely
to end up on.

**One key per install.** `backups/encryption.go` makes an X25519 identity the first time a job that
encrypts is saved or run (or the key is exported), seals it with `auth.Sealer` and stores it under the
`backups.archive-key` settings row beside its public half, its creation time and when it was last
exported. The insert is `ON CONFLICT DO NOTHING` followed by a read, so two jobs encrypting for the first
time at once share one key. One key rather than one per job, because deleting a job leaves its archives
where they are and a key that went with the job would leave them unreadable, and because a recovery
plan the operator keeps somewhere safe should be one file. A read never makes a key: an encrypted run on
an install with no key answers `409 archive_key_unavailable` (`ErrNoArchiveKey`), and one sealed to
another key answers the same code by name (`ErrArchiveKey`), checked against the manifest's recipient
before anything is decrypted and again by age itself. Both are a `KeyError` naming the key the manifest
records, in the message and as the error's `resource`, so the operator knows which exported file to
bring. A key exported elsewhere can be imported (`POST /backups/key/import`, below). The secret half is never written to a run log,
the audit trail or the process log; the public half appears in manifests, the key's status and the
export's audit entry, and a run log says only that the archive was encrypted.

**What the manifest proves.** `artifactDigest` stays the SHA-256 of the bytes at the destination — the
ciphertext for an encrypted archive — because that is what retention, every read and the recovery
evidence (`RestoreVerification.ArtifactDigest`) bind to and check before a byte is decrypted.
`manifest.encryption` (`format`, `recipient`, `plaintextDigest`, `plaintextBytes`) records the key the
archive was sealed to and the digest of the `.tar.gz` inside, which is what a copy decrypted away from
the dashboard is checked against with `sha256sum`. The field is a pointer and omitted for a clear
archive, so a manifest written before encryption existed encodes, and digests for the recovery evidence
bound to it, exactly as it did. An older dashboard reading an encrypted archive fails at the gzip
header rather than misreading it.

**Every read decrypts.** `Runner.localArtifact` verifies the stored digest, then pairs the file with
the key its manifest names (`ArchiveFile`); listing, coverage checks, restore into a directory or in
place, the database restore, the deployment gate's checks and verify-restore all read through
`ArchiveFile.entries`, which decrypts as it streams. Verify-restore copies and re-hashes the ciphertext
into its private workspace and decrypts only while extracting, so the workspace holds no second
plaintext copy. A chunk that fails to authenticate is reported as an archive that is damaged or was
altered. `GET /backups/runs/{runID}/download` hands an administrator the archive inside — named
without `.age`, with `Content-Length` set to `plaintextBytes` so a transfer that stops short reads as
incomplete, and aborted rather than finished when a chunk fails — and is recorded as `backup.download`
with `decrypted: true` (a GET passes the audit middleware by, so the handler writes the entry itself
through `recordRead`, for a clear archive as well).

**The key's routes.** `GET /backups/key` (read) answers `exists`, `recipient`, `createdAt`,
`exportedAt`, `encryptingJobs` and `encryptedArchives` — never the secret half. `POST
/backups/key/export` (`system.admin`, inside `s.destructive`, because nothing can recall a copy that has
left) answers the key as an age identity file, `just-dashboard-backup-key.txt`, `Cache-Control:
no-store`, with its creation time, its public key and the command that opens an archive in its comment
lines; it records `exportedAt` and is audited as `backup.key.export` with the public key as the target.
The page confirms it with an ordinary confirmation. Job create and update audits carry `encrypt`.
`POST /backups/key/import` (`system.admin`) takes such a file, or its `AGE-SECRET-KEY-1` line, seals it
and keeps it (`keyring.go`): on a server that holds no key yet it becomes this install's own, with the
creation time the file records and marked exported, because a copy is already off the machine — so a
moved or rebuilt server keeps sealing to one key; on one that holds a key it joins
`backups.archive-keys.imported`, which only opens what was sealed to it. The answer says which
(`became`: `own`, `opens` or `held`); it is audited as `backup.key.import` with the public key as the
target, and `GET /backups/key` lists the imported keys by their public halves.

**Recovering without the dashboard.** With the exported file, on any machine with `age`:

```bash
age --decrypt --identity just-dashboard-backup-key.txt NAME.tar.gz.age > NAME.tar.gz
sha256sum NAME.tar.gz      # the run's "Archive inside" digest, where the record survives
tar -xzf NAME.tar.gz       # source-0001/, database-0001/<dump>, as the manifest lists them
```

A database dump inside is the engine's own format and is loaded with that engine's tool (or, on a new
dashboard, uploaded on the database's Backups page). The dashboard's own data directory is the case the
export exists for: its job's archive holds the database that holds the sealed key, and `install.env`
holding `JD_MASTER_KEY` is excluded from it, so an exported key is the one way to open it once the
server is gone.

The Backups page shows the key as a panel under the jobs once a job encrypts or a key exists — its
public key, what depends on it, and *not exported yet* in amber with a notice until a copy has left —
with **Export key…** for an administrator. A job's card and identity line say *encrypted*; a run's rail
row carries a lock and its inspector an Encryption block with the public key and the inside digest, and
its download is named **Download decrypted archive**. `encryption_test.go` covers the round trip through
every reader, tampering with the stored bytes, the payload, the header and the length, a missing and a
different key, an 80 MiB stream, clear archives around encrypted ones, one shared key and an export
that the age format itself opens; `backup_encryption_test.go` the defaults, older callers, the routes'
roles and audits and the decrypted download; `backup_encryption_live_test.go` a PostgreSQL and a MariaDB
dump restored from an encrypted archive; `encryption_live_test.go` a bucket; and
`backup-encryption.spec.ts` the page.

## Importing what a destination holds

The job and run rows live in the store of the server that took the backup, so a fresh server — a
migration, or a rebuild after the old one was lost — starts with none. Every run since 0.8.2 therefore
writes its manifest beside its archive, at the archive's name plus `.manifest.json` (`sidecar.go`), on
every kind of destination. It holds the archive's name, digest and size, its origin (`backups.install-id`,
a random value made once per install, the host name, the job's id and name, the run's id and start), how
many paths, volumes, dumps and snapshots it covers, and the run's manifest. An encrypted archive's
manifest is sealed to the same key, because its paths, database names and paused containers say what
the archive holds: who took it, when, its size, digest, counts and the public key that opens it stay
readable. A manifest that cannot be written is the run log's to say, not a failed run: the archive is
whole and restores on its own server. The archive cannot carry its own manifest (the digest is over its
bytes), so archives written before 0.8.2 have nothing beside them; they are listed by what their names
say (job, job id, run id, start) and their size, and are not imported, because there is nothing to
prove them against — download them, and open them with `tar` (and `age` with the exported key).

`POST /backups/catalogue` (`system.admin`, audited `backup.catalogue`) reads a destination: one of this
server's jobs' (`{"jobId": N}`, its sealed keys used where they are) or one entered with its keys
(`targetKind`, `target`, `secrets`), which are used for the request and kept only with an imported job.
A local directory is resolved through `files.Resolve`. It lists only where the runner writes — the
directory, the bucket's prefix (S3 `ListObjectsV2` with a delimiter), the SFTP directory over one session,
the rclone folder with `lsjson` — up to 20 000 objects, and reads the manifest beside each of up to
2 000 archives, eight at a time, each bounded to 1 MiB. Archives this server wrote and still has a run of
are left out and counted (`ours`); one written here whose record is gone (its job deleted, or the server
restored from an older copy of its data) is listed with `thisServer`. The rest are grouped by install
(by host name) and by job, newest first, each with `kinds` (`paths`, `volume`, `dump`, `snapshot`), size,
start, whether it is sealed and to which key, whether a key this server holds opens it (`openable`, or
`needsKey`), whether it can be imported, why not (`problem`), and the run it was imported as. An SFTP
server entered for the first time is pinned in memory to the key it presented and the catalogue answers
it (`hostKey`, `hostKeyFingerprint`) to be compared and sent back with the import; a different key at the
address is refused as for a job.

`POST /backups/import` (`system.admin`, audited `backup.import` with the runs and jobs it made) adopts
archives from the same destination body: by their keys, or every archive of one install's job
(`originId`, `originJobId`). Each archive's manifest is read again from the destination — never taken
from the request — opened (a sealed one needs its key here, and is refused naming it), checked against
what the readable half says, and the archive is fetched whole and hashed as it streams: a size or
digest that does not match its manifest is refused as `422 archive_digest_mismatch`, and nothing is
recorded for it. Only proved archives are recorded, oldest first, as **imported runs** (`imported = 1`,
trigger `import`, the origin's start time, the destination's key as the artifact, the manifest as it was
written) under an **imported job** per origin job and destination: named after the job and the host it
ran on, holding the destination as this server reaches it with its keys sealed, the recorded sources for
display, the recovery check the origin had (validated), and nothing it would run. `copyTo` (a directory,
through `files.Resolve`) keeps a copy of each archive here as it is hashed, recorded as the run's
`local_copy`; reads prefer it while it exists and proves its digest, and fall back to the destination's
object otherwise. Importing an archive twice adopts it once (`already imported as run N`); `importMu`
serialises imports. The answer lists what was skipped and why; an import that recorded nothing answers
its first refusal. The digest proves an archive is the one its manifest describes and arrived whole; it
does not prove who wrote the pair, since anyone who can write to the destination can write both — which
is why import is an administrator's act and restoring from it is confirmed like any other restore.

An imported job (`backup_jobs.imported`, `origin_json`; runs `backup_runs.imported`, `local_copy`, all
additive) never runs, is never scheduled and is never edited: `Runner.Execute`, the scheduler,
`PUT /backups/{id}`, `/enabled`, `/run` and `/database-dumps` refuse it (`409 backup_job_imported`).
Retention never prunes it or any imported run — those archives are another server's to keep — and a job
of this server's own writing to the same place prunes only its own. Deleting an imported job drops the
records and leaves the archives. It is left out of the coverage report (its sources are paths on another
server), the backup alerts, the host's event timeline, linked-account summaries and
`LastDatabaseDumps`/`LatestDatabaseDump` (its connection ids are the other server's), and the map marks
it imported. Its runs list, restore into a directory or in place, download, and verify-restore exactly as
this server's own, through `Runner.localArtifact`. A dump in an imported run is restored by where it lies
in the archive: `POST /backups/runs/{runID}/restore-database` takes `dump` (its archive path) and the
`connectionId` of a saved connection here of the same engine (`Runner.RestoreDatabaseDump`); without
`dump` an imported run is refused.

On `/backups`, **Import…** (and **Import from a destination…** on an empty page) opens the dialog
(`components/backups/import-dialog.tsx`, its words and grouping in the unit-tested `import-model.ts`):
the places this server's jobs write, each once with a bucket's prefix telling two apart, as choices, or
another destination typed in; then the catalogue by server and job, each archive with where it stands,
**Import all N** for a job's history or **Import** for one, the keys sealed archives need with
**Import a key…** in place (`KeyImport` in `backup-key.tsx`, also behind **Import key…** on the Backup key
panel, which lists imported keys), and *Keep a copy of each archive on this server*. An imported job's
card and identity line say where it came from instead of when it next runs, it sorts after this
server's jobs, its only verbs are Test destination and Remove imported job, and its runs' inspector says
which server, job and run each was and where it is read from. The restore dialog's in-place mode reads
the restore plan and says, per source, what it overwrites and what it makes. `import_test.go` covers the
manifest beside every archive, a sidecar that disagrees with itself, the catalogue's grouping and older
archives, an import and restore on a fresh store, the digest refusal, a sealed archive before and after
its key, key adoption, a volume made again with its labels, the kept copy and its fallback, retention,
records this server lost, and SFTP; `backup_import_test.go` the routes, roles, audits and refusals;
`backup-import.spec.ts` the page; and `TestLiveImportedBackupRestoresOnAFreshServer` a volume and a path
moved through S3 between two stores.

## Host users and SSH keys

`internal/linuxusers` manages local operating-system accounts, not dashboard identities. The whole
`/system-users` and `/system-accounts` route trees require `system.admin`, reads included, because they
expose shells, groups, login history and who can become root. `/system-accounts` is separate because
anything else under `/system-users` would shadow an account of that name.

**Reading.** `Service.read` is one reading of everything an account's standing depends on: `/etc/passwd`,
`/etc/group`, `/etc/shadow`, `lastlog` (or `lastlog2`), each `authorized_keys`, the sudoers files and
`sshd -T`. Every other judgement in the package, a guard included, starts from it.

- `password` is what is behind the shadow field, which `locked` cannot say: `set`, `locked` (a hash behind
  a `!`, which unlocking restores), `none` (`!`, `*`, `!!`: nothing to restore), `empty` (no password is
  asked for) or `unknown` (shadow unreadable). `usermod --unlock` on a bare `!` warns on stderr, changes
  nothing and **exits 0**, so `SetLocked` asks the file first, answers `nothing_to_unlock`, and re-reads the
  file after any lock or unlock rather than believing the exit status. `SetPassword` does the same.
- `signIn` (`access.go`) is whether SSH would admit the account by password and by key, with `why` naming
  the first thing closing each route: the account's expiry, its shell, sshd's AllowUsers, DenyUsers,
  AllowGroups and DenyGroups in sshd's own order, PermitRootLogin, the auth methods, `UsePAM no` over a
  locked password, then the password state and the key count. `Get` asks `sshd -T -C user=<name>,…`, so a
  Match block is in one account's answer; the listing asks once, because a process per account is not free
  on a page that polls. A socket-activated sshd (Ubuntu's default) has not made `/run/sshd` until somebody
  first connects and `-T` refuses to answer without it, so that directory is made and the question asked
  again. The answers are checked against real sign-ins in the live suite below.
- `rootRoutes` (`sudo.go`) is read from the sudoers files, following `@includedir`, `#includedir` and
  aliases (read before the rules, since sudo takes an alias defined below the rule using it), skipping what
  sudo skips (a name with a dot or ending in a tilde), and telling `#1000` from a comment. A rule read wrongly
  is root access the page, the guard and the alert all miss, so run-as and tags are followed command by
  command as sudo does, a second host spec is read, `Defaults !authenticate` counts as asking for no
  password, and `ALL, !/bin/su` counts as everything: a negated command is no protection, and calling it
  limited would understate root. `usable` is false for a rule that asks for a password the account does not have, which is what
  a member of `sudo` with a locked password is. `docker`, `lxd`, `lxc`, `libvirt` and `disk` are routes of
  kind `group`: root by another name. Where the sudoers files cannot be read at all, the conventional group
  names stand in. Root is always in the listing, whatever `?system=` says: it is the most important account
  on the machine, and hiding it as a system account is how a host could read "nobody can sign in" while
  root signed in with a password.

- `sshKeyCount` and `keysUnknown` read every file sshd would (`AuthorizedKeysFile` from `sshd -T`, so
  `authorized_keys2` and an absolute path are in the count), through `otherKeys`, which only reads: this page
  writes `.ssh/authorized_keys` and nothing else. A file it cannot read, a link where a file should be, or an
  `AuthorizedKeysCommand` is `keysUnknown`, and an unknown is reported as a route that may be open rather than
  as zero keys, because zero is what would let the guard close the last way in.

**Execution.** Every tool goes through `run.go`: the host's own copy through `hostexec.CommandOnHost` where
it has one (built against the host's PAM, `login.defs` and shadow suite), and the image's through
`hostexec.Command` otherwise, since `/etc` is the host's own directory in here too. Secrets travel on stdin,
never argv. A home and the keys in it are reached through `hostexec.HostPath`, because the container shares
`/home` and `/root` and little else, and a daemon's home under `/var/lib` is not one of those. For the same
reason an archive's directory is made by a tool: tar runs on the host, and a directory made from this
process would be one tar cannot see.

**Writes that must not lock anyone out.** `guard.go` judges a change against the reading before it is made.
The last way in is an account that could sign in over SSH and reach root with a usable, unlimited route, and
deleting, disabling, locking, removing a password or the last key, changing the shell, leaving a group or
removing or replacing a sudo rule, a home changed without its files, a rename outside AllowUsers, a main group
that carried a rule, or password rules that have already run out is refused with `409 last_way_in` when it
would leave none. Each judgement and its write happen under the service's lock, so two changes cannot each
pass on the strength of the other's account. A host with no way in has
nothing left to lose and is not stopped from mending itself. The dashboard's own terminal is not counted: it
stops working the day the stack does, and that is the day somebody needs SSH.

`protected` names accounts the system boots with: they cannot be deleted, locked, disabled, renamed, moved
or renumbered, and keep a login shell where they have one. Root may still lose its password (keys only is
how a server is meant to be, and the guard stops it being the last way in). Accounts a package made are
read-only over HTTP unless the request says `allowSystem=true` (`guardSystemAccount`, invariant 4), and
bulk routes never touch them. A system group keeps its name, number and existence the same way
(`guardSystemGroup`), though who is in one is the operator's to change. Making a second uid 0 is refused, and a shell must be in `/etc/shells` or be a
nologin.

**Shared numbers.** A process and a file have a number and no name, so anything aimed at an account's processes
or files lands on every account with that number. `EndSessions` refuses a uid 0 alias or a shared number
(`409 shares_number`): `pkill --uid` on one would end sshd, dockerd and the dashboard. Deleting such an account
removes the name and nothing else, with `userdel --force` because userdel looks for processes by number, and
the preview says so (`sharesNumber`) rather than listing the other account's processes as this one's. Usernames and group names follow a closed 32-character Unix pattern.

**Creating.** `POST /system-users/` checks everything it can before the account exists (a key, a password, a
sudo grant, the GitHub login), refuses a person's account with no way in unless `noSignIn` says that is the
intent (`400 no_way_in`), and takes the account back out if anything after `useradd` fails. It is
session-only, as are `POST …/password` and `POST …/keys/generate`: a password or a private key leaving the
dashboard is a way onto the machine, and a long-lived API key may not mint one. A generated password or
private key is in that one response and nowhere else, and none of them reaches the audit log.

**Sudo.** `SudoGrant` is the dashboard's own rule, one file per account (`/etc/sudoers.d/jd-<name>`): all
commands or named ones, with or without a password. It is written to a sibling sudo ignores, checked with
`visudo --check --file`, renamed into place, and the whole configuration is checked with `visudo --check`,
with the old file put back if that fails, because one bad line stops sudo for everyone. `sudoCommandRe` holds
what visudo was found to accept: `=` is escaped, and `@` is refused everywhere since visudo refuses it even
escaped. `RemoveSudoRule` takes a rule out of whatever file holds it, keeping the rest of the file and anyone
else the rule names; a group's rule is refused, because leaving the group is what takes it away.

**Removal.** `GET …/removal` counts what would go: the home, files the account owns elsewhere (a bounded
sample from `find -xdev`, against a clock), processes, its crontab, keys and sudo rules, and why it cannot go
at all. `DELETE …` takes `home=keep|archive|delete`, `endSessions` and `transferTo`. `home=delete` takes the
typed name (invariant 3). A home that is too near the root or shared with another account is never removed,
because `userdel --remove` takes whatever the passwd file says. An account with processes running is refused
unless `endSessions` says to stop them, an archive (`/var/backups/just-dashboard/accounts`, root-only) is read
back before the home goes. A delete closes the account first, under the lock: it is expired, its crontab goes and
its sudo rules are rewritten before anything long or irreversible starts, because an archive takes minutes and a
handover is a root chown across files the account controlled, and an account still able to act could swap a path
under it. Handing over uses `chown --from=<uid> -h` and `find -execdir`: `--from` leaves a hard link to somebody
else's file alone, `-h` stops at a symlink, and `-execdir` means a directory swapped for a link to `/etc` between
find listing it and chown running changes nothing. A home is not this account's to remove when it is a link, holds
another account's home, or is shared. Rules go with the account by every spelling that would outlive it, `#uid`
and a private `%group` as well as its name. A home that was already there when an account is created is reported
back (`inheritedHome`), and undoing a failed create leaves it alone. The
account's sudo rules, crontab and limit drop-in go with it, so none can land on whoever is next given that
name or number.

**Renaming** moves the private group, the crontab and the dashboard's sudo file. A rule in somebody else's file
that names the account is refused rather than rewritten: left behind, it would hand root to the next account
given the old name. The same goes for a group a sudo rule names.

**Keys.** Parsed with `x/crypto/ssh`, private and multi-line input refused, SHA-256 fingerprints, de-duplicated
by fingerprint. Home traversal and `.ssh`/`authorized_keys` opens refuse symlinks, directory descriptors anchor
reads and atomic replacements even if the account renames a directory, new `.ssh` directories belong to the
account at `0700`, and keys go into exclusive random `0600` files renamed into place, so a preexisting symlink
or hard link cannot redirect a privileged write. Removal and editing keep comments and unrecognised lines.
Restrictions are a closed list of what `authorized_keys` understands, and a composed line is parsed back and
must keep its fingerprint, so an option cannot smuggle in a second key or break the line. `lastUsed` comes from
the auth log, and `weak` flags DSA and short RSA. GitHub import fetches one fixed URL with a validated login and
bounds the response.

**Limits and defaults.** Per-account CPU, memory and tasks are a drop-in on `user-<uid>.slice`
(`/etc/systemd/system/user-<uid>.slice.d/50-just-dashboard.conf`), taken back if `daemon-reload` fails so a file
never claims a limit systemd did not take, with a floor below which no login could start. Disk is `setquota`,
offered only where the home's mount has quotas on. `Defaults` reads and writes a few keys of `login.defs`,
`/etc/default/useradd` and `pwquality.conf`, keeping every other line and comment. Templates are shapes (shell,
groups, sudo level), never credentials: the built-in ones are in the code and the operator's own in the settings
store.

**Tests.** The fast layer runs against a temporary `/etc` and a fake runner. `live_test.go` runs the real
shadow suite, visudo, gpasswd, tar and sshd, including real SSH sign-ins checked against what `signIn` said, and
changes real accounts, so it refuses to run without a marker file baked into its throwaway image:

```bash
go test -c -o /tmp/l/linuxusers.test ./internal/linuxusers
docker run --rm --network none -e JD_USERS_LIVE=1 -v /tmp/l:/t:ro <sandbox> /t/linuxusers.test -test.run Live -test.v
```

The sandbox is `ubuntu:24.04` with `sudo openssh-server cron procps rsyslog libpam-pwquality`, `ssh-keygen -A`
and `touch /.jd-users-live-sandbox`. `TestLiveSystemdLimitsAndTheHostPath` needs the same image with systemd as
PID 1 (`--privileged --cgroupns=private --tmpfs /run`, `JD_USERS_LIVE_SYSTEMD=1`), and
`TestLiveAcrossTheMountNamespace` runs that under `unshare --mount` with tmpfs over `/data` and `/var/backups`
(`JD_USERS_LIVE_NAMESPACE=1`), which is the boundary the dashboard's container sits behind.

The relationship between host execution and the documented invariant is recorded in
[`../reference/verification-findings.md`](../reference/verification-findings.md).

A backup job's destination can also be given to a PostgreSQL server's point-in-time recovery
(`fromJob` on `POST /databases/{id}/pitr`): its kind, configuration and sealed keys are copied into the
archive's own record, and a directory gets a `wal` folder beside the job's archives. WAL-G writes only
to a directory or an object store, so a job whose destination is SFTP or rclone is refused there. The archive is not
a run of the job and is not pruned by its retention; see
[Databases → Point-in-time recovery](databases-proxy-platform.md#point-in-time-recovery).

## Backup presentation in 0.7.5

Backups displays schedules, next runs, recorded outcomes, coverage and recovery controls. The automatic
attention list and overdue recommendation detector are removed, including the `overdue` response field.
The scheduler, retained run history, retention and restore safeguards are unchanged.

## Backup form inputs and host account access

Backup jobs expose a `revision`, migrated additively with default 1 on existing jobs.
`PUT /backups/{id}` accepts `expectedRevision`: a positive value must match the stored revision,
otherwise it returns `409 backup_job_conflict` without changing the definition or credentials.
Validation and sealing happen before one SQL statement publishes both halves and advances the
revision. Omitting `secrets` keeps the sealed keys. Legacy callers omitting `expectedRevision` (or
sending zero) retain their unconditional-save contract, and their saves still advance the revision.
Dashboard editors always send the revision captured with their held draft; polling and navigation
never silently adopt a newer one. A conflict keeps fields and entered keys in memory and offers
**Review current job**, a comparison of saved settings and draft, and an explicit
**Save draft over reviewed job** or **Discard draft and close**. A second edit after review conflicts
again. New keys and expected recovery output are called out before an explicit replacement. Successful
save, explicit close/discard and working-state clearing remove the held draft and keys; nothing is
persisted in browser storage. Pausing/resuming changes only `enabled` and advances the revision, so
an older full-definition save cannot silently undo the pause.

`POST /backups/{id}/database-dumps` accepts only `connectionId` and requires `system.admin`.
It refuses malformed stored dump-list JSON, validates saved connections, idempotently adds the
requested dump, and publishes only that list with a revision precondition. A concurrent publication
retries against the latest list up to five attempts, then returns the same conflict; other definition
fields and sealed keys are untouched.
It audits as `backup.job.database_dump.add` and reloads the schedule like other job edits.
The deployment Databases settings helper uses this route instead of a stale full-job save.
`store_update_test.go`, `recovery_migration_test.go`, `backup_job_updates_test.go`,
`backups-ui.spec.ts` and `deploy-settings-b.spec.ts` cover fault-injected publication, canceled saves,
competing editors, pause conflicts, simultaneous dump additions, legacy migration, authorization,
auditing, retained drafts, reviewed revisions and secret cleanup.

A backup job requires at least one filesystem source **or** one native database dump. Native-only
jobs use the existing manifest, retention, coverage, extraction and database-restore paths without
inventing a host file source. The `?database=` handoff is consumed only when the resource report can
open its suggested job. The picker tracks explicitly owned contributions: unselecting a resource
removes only its exclusive inputs, preserving manual and shared inputs. Explicit close or successful save of the job
editor clears its held draft; SPA navigation retains it. Failed run-history reads show an error/retry or an older-data warning;
they never become a successful empty history. `database_dumps_test.go`, `resource-picks.test.js` and
`backups-ui.spec.ts` exercise these behaviors.

Host `locked` describes a **password lock**, and the dashboard action is labelled Lock password.
It does not remove authorized keys or claim complete SSH/account disablement. A login shell plus
an unlocked password or authorized key is a known sign-in route; sshd/PAM and account policy still
decide actual acceptance. Administrator counts include UID 0 and sudo/wheel/admin/docker group routes.
Direct sudoers rules are not inspected, so this is an inventory of known routes, not complete effective
privilege analysis. `access.test.js` and `system-ui.spec.ts` cover key-only accounts and UID 0.
