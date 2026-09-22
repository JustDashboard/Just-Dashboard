# Invariants that must not regress

A change that weakens any of these has to say so explicitly.

1. The network allowlist runs **before** authentication.
2. Two-factor is *enforced where it applies*, and this is the one invariant that is now a policy rather
   than a constant — see [Invariant 2](#invariant-2-what-two-factor-still-guarantees) below. An account
   with an authenticator is **always** asked for a code, and a session that owes one reaches nothing but
   the 2FA routes. What `JD_REQUIRE_2FA` decides is whether an account that has *not* enrolled may sign
   in at all. It defaults to false.
3. Every destructive action is behind `s.destructive` — capability, `destrLim`, audit entry — and pauses
   the operator with a confirmation dialog. A **subset** also requires the typed `X-Confirm` phrase,
   enforced server-side inside the handler. See below. A route that also serves routine, non-destructive
   operations — the deployment run route's `stop`/`restart` alongside `deploy`/`redeploy` — cannot be
   wrapped in `s.destructive` wholesale, so it enforces the same capability and `destrLim` budget by hand.
4. Capability checks live on the route, never in the UI alone. Where the answer depends on what is *in* the
   request, the handler checks by hand and fails closed: `dbx.Classify` for SQL, `api.authoriseSpec` for a
   container spec that is privileged or mounts a host path.
5. Every state-changing request lands in the audit log.
6. Client-supplied paths go through `files.Resolve` — including the ones that do not look like file
   operations (bind-mount source, build context, a new stack's directory). Host commands go through
   `hostexec` with an argv, never a shell string. Request-defined shell source is confined to `deploy.Deployer.shell`, deliberately:
   those are pipelines an admin stored for their own project, not anything supplied per request. Do not add
   a second request-defined shell, and do not "fix" that one into an argv. A git clone's parent and an
   `init` target go through `gitx.ResolveDir` against `JD_GIT_ROOTS` — the Git page's own boundary — and a
   clone can only create a directory that does not exist. Terminal startup also uses
   a bundled constant bootstrap to load the native prompt; paths remain separate positional arguments. `dockerx` invokes the `docker` binary in three places
   (compose, the streaming runner, `Build`) because the Engine API has no equivalent; all three build argv
   explicitly.
7. Nothing but Caddy binds a routable address.
8. Store schema changes are additive and tolerate an existing database. `CREATE TABLE IF NOT EXISTS` is a
   no-op against a table that exists, so a **column** added later also goes in `store.addedColumns`, which
   `applyAddedColumns` ALTERs in at open. Every entry needs a `DEFAULT` (SQLite refuses a NOT NULL column
   on a populated table without one) and **no entry is ever removed** — the list is the path from every
   shipped schema to the current one, not a description of the current one.

9. A blueprint is data, never a script. `internal/blueprint` parses with unknown fields rejected and
   validates at package initialization, so a built-in that breaks the supply-chain policy cannot reach a
   running dashboard. Install/release/startup/stop steps come from a closed operation vocabulary; there is
   no "run this string" kind, and adding one would be adding a second request-defined shell by another
   name. No blueprint may ship a default credential, publish a database port, declare a stateful workload
   without persistence, download without https plus a size limit and a checksum or declared version
   source, name an uncontained path, or take privilege without a written reason.
10. The game console is not a shell. `gameserver.ValidateCommand` refuses anything carrying a shell
    metacharacter, newline or NUL, the command reaches Docker exec as separate argv elements, and player
    actions come from a closed set with the account name validated before interpolation. The same
    validator gates the console route and the scheduler, so a schedule cannot send what the console
    refuses.
11. Diagnosis claims only what an owner returned. An unavailable module is recorded as a *silence* with a
    reason — never as a healthy result, and never as a problem. A finding that cannot name an action or a
    deep link says which external action is required instead.

## Invariant 2: what two-factor still guarantees

Two-factor used to be unconditional, and the reasoning was sound for the install the product was first
written for. It was wrong for the one it is actually installed into: this dashboard is reachable only
over a tailnet or an ssh tunnel, both of which authenticate the network before a packet reaches the login
page, and making an authenticator app compulsory turned the first ninety seconds of a single-operator
install into a chore that could not be skipped.

What did **not** change, and must not:

- An account with `totp_enabled` is asked for a code at every sign-in, whatever `JD_REQUIRE_2FA` says.
  `Service.Login` decides on the account, not on the policy.
- A session that owes a second factor is never elevated. `httpx.Authenticator.resolve` refuses it
  everywhere but the 2FA routes, exactly as before.
- Where `JD_REQUIRE_2FA` is true, an account with no authenticator gets a password-only session that
  reaches nothing but enrolment — the old behaviour, unchanged.
- Turning an authenticator off is the account holder's own action, requires their password, and is
  refused outright by `Service.DisableTOTP` where the policy demands one.
- Setup, enrollment confirmation and code verification share one account-scoped attempt budget.
  Enrollment cannot be used again after TOTP is enabled. An accepted TOTP counter or recovery code is
  consumed atomically with session elevation; another session cannot reuse it.
- A temporary password does not grant feature access. After required factors, only authentication
  status/logout and the session-only password change are available until `must_change_pw` is
  cleared. API tokens cannot bypass this restriction.

The one deliberate loosening: with the policy off, an account that has never enrolled signs in on a
password alone. `auth.Service.Login` elevates that session at creation, and `ResolveSession` completes a
session that was left half-authenticated when the policy changed under it — without that, turning the
setting off would strand everyone who was mid-flow with a session that can never be elevated.

## Invariant 3: which routes take a typed phrase

**The test is frequency, not severity.** A phrase in front of something done a dozen times a day is not
read, it is typed — and the operator who has learned to type one table name without looking types the next
one the same way. That habit is exactly what the phrase protects on the routes that keep it, so every route
added to the typed set makes the set weaker. The question is not "is this dangerous" (they all are — that
is what `s.destructive` marks) but **"how often does somebody do this, and can they get it back"**.

**Typed — rare, and no way back:** `DROP DATABASE`, `DROP TABLE`, `DROP COLUMN`, `TRUNCATE`, an import that
truncates first, dropping a Mongo collection, a Mongo pipeline with `$out`/`$merge`, a `critical` statement
in the query runner, restoring a database or backup over live data (a backup restore types the destination directory, or the phrase `restore in place` when the archive goes back over the paths it was taken from), `compose down`, removing a Docker
volume, a prune that also sweeps volumes, deleting a dashboard or Linux account, a recursive directory
delete, `git discard`, `git reset --hard` and dropping a git stash, aborting a Git operation
(`abort operation`, which discards conflict-resolution edits), choosing an entire conflict side or deletion
(`discard changes`), toggling the firewall, resetting it, switching the inbound
default to deny, changing sshd's configuration, revoking a certificate, applying package updates,
**purging** a package (removing it *and* deleting its /etc configuration), and installing a new version of
the dashboard itself.

The firewall and sshd entries are not about losing data: get one wrong and the way back into the machine is
gone, and no undo here helps, because reaching this UI is what you lost. They are also rare — an inbound
default is set once, an sshd hardening pass happens on the day the server is built. The self-update phrase
is the **version being installed** (`0.6`), not a fixed sentence: it names the object, as every other typed
route does, and *which version* is what has to be read before pressing a button in the sidebar.

**Not typed — routine, recoverable, or both:** deleting rows, documents and Redis keys; dropping an index;
forgetting a connection; stopping a database session; stopping/restarting/killing/removing/recreating a
container; removing an image or network; any prune that spares volumes; deleting one file; signalling a
process; ending an SSH session; stopping or restarting a service; revoking a token or SSH key; deleting a
backup job or deploy project; rolling back a deploy; disabling **or deleting** a vhost; deleting an nginx
stream, htpasswd file or saved DNS-provider credential (pasted again in a minute); deleting a git branch, a branch on the remote, a tag or a remote; adding, **editing** or deleting a firewall rule; tuning a
fail2ban jail; unbanning an address; stopping a running job; closing a terminal session, window or pane;
and **removing a package without purging it** — undone by installing it again, where the /etc files
somebody spent an afternoon on have no path back at all.

Editing a firewall rule is a write, not a destructive one, and is mounted accordingly: the replacement goes
in before the original comes out, so there is no moment the rule is missing. Stopping a job is the same
argument from the other side — interrupting is how you *avoid* a bad outcome, and a phrase in front of a
stop button is one somebody types while something is going wrong.

The 0.6.1 review narrowed the set: a prune sparing volumes (containers, networks and images come back from
a registry or a compose file), deleting a proxy site, nginx stream or htpasswd file (each recreated from
the same form), deleting a git branch (a pointer whose commits survive in the reflog and on the remote),
and ending an SSH session (a SIGHUP the operator reconnects past). All keep `s.destructive` and an ordinary
confirm dialog. `compose down` was reviewed and **kept** — it is the one compose action that removes rather
than stops containers, and on a host running several stacks typing the name guards against `down`-ing the
wrong one. `git discard` and `git reset --hard` were kept too: they overwrite uncommitted work, which the
reflog does not cover — and dropping a stash joined them for the same reason, since the work in it
exists nowhere else.

Several routes decide by content, and the narrowing lives at the call site: `handleDBQuery` types only for
`critical`, `handleFileDelete` only when `recursive`, `handleGitReset` only when `--hard`, `handleDBImport`
only when `truncate`, `handlePruneAll` only when `volumes=true`, `handlePackageRemove` only when `purge`,
`composeNeedsPhrase` only for `down` — with `requireComposePhraseWS` applying the same narrowing on the
socket, so the two entry points cannot disagree. The frontend mirrors each with a conditional `phrase`, and
the server re-decides regardless.

One relaxation: `httpx.RequireTypedConfirmationWS` also accepts the phrase as a query parameter, used only
by WebSocket routes where a browser cannot set a header at all and `wsx`'s origin check supplies what the
header guarded. **Do not reach for it from an ordinary handler.**

The browser sends `X-Confirm-Encoding: uri` and an `encodeURIComponent`-encoded `X-Confirm` value.
The backend percent-decodes it once, requires valid UTF-8 and compares the exact phrase, including
leading/trailing whitespace. This supports Unicode database and file names without relying on HTTP
header normalization. Legacy unencoded headers remain supported for ordinary phrases; the WebSocket
query parameter is compared exactly after normal URL decoding.

`api/handlers_db_test.go` pins both directions for the database surface
(`TestIrreversibleDatabaseRoutesDemandAPhrase`, `TestRoutineDatabaseRoutesDoNotAskForAPhrase`), because the
line has two failure modes and the second — a phrase creeping back onto routine work, one defensible route
at a time — is the quieter of the two.
