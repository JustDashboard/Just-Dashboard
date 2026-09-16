# Just Dashboard 0.6.7 — security and correctness audit

**Date:** 2026-09-15  
**Revision:** `e44aeb58f9ab241d1d3c903e02966cc78b168cc3` (`patch/0.6.7`)  
**Audit branch:** `audit/0.6.7-api-security`  
**Worktree:** `/home/ubuntu/Just-Dashboard-audit-0.6.7`

## Executive summary

**Fix the authorization, file-containment, credential-disclosure and database-integrity findings before treating this revision as ready for release.** The review found routes that bypass the intended Docker/SQL authorization model, file operations that escape configured roots, privileged host operations that trust user-controlled paths, deployment logs that disclose secrets, and browser workflows that send mutations for the wrong records.

This is a repository-wide, risk-driven audit with targeted safe reproductions. It is **not a certification that every line or every possible exploit has been checked**. The review covered the API security chain, backend packages, frontend/API integration, deployment and operational scripts, dependency advisories, and existing test gates. The detailed sections distinguish reproduced behavior, source-traced findings and conditional/latent risks.

The original worktree had uncommitted changes. They were excluded and left intact. The new worktree was created from the committed local `patch/0.6.7`, which matched the existing `origin/patch/0.6.7` reference. No fetch was performed, so this does not assert that the live remote has no newer commits. No application fixes, dependency upgrades, commits or pushes are included in this audit.

## Priority order

| Priority | Finding | Why it needs attention |
| --- | --- | --- |
| P1 | Alternate Docker volume/Compose paths bypass privileged-spec checks | A limited account can reach host-access settings that direct container creation restricts to administrators. |
| P1 | Read-only database explain can execute mutations | A nominal read route forwards SQL that can change or remove data. |
| P1 | File-copy destination symlinks escape configured roots | A permitted copy can overwrite files outside its authorized tree. |
| P1 | OPS-01: PM2 discovery executes user-owned binaries with backend privileges | A routine inventory read can execute a local user's file as the deployed root backend. |
| P1 | OPS-02: SSH key addition follows symlinks and changes target ownership | A local user's home can redirect a legitimate administrator action to an unrelated privileged file. |
| P1 | OPS-03: legacy deployment transcripts disclose environment secrets | Readers can obtain secrets through logs despite separate restrictions on revealing variables. |
| P1 | AUTH-01: enrolled users can use the unthrottled enrollment endpoint to verify codes | The verification throttle is bypassed by another partial-session route. |
| P1 | FE-01 / FE-02: database selection and stale table data target the wrong rows | Browser reproductions captured DELETE requests for a different row or table. |
| P1 | FE-03: exact numeric values are rounded before submission | Successful inserts and updates can silently store a different number. |
| P1 functional | OPS-05: exposed blueprint flows cannot complete deployment | Source materialization is unimplemented; some shipped defaults also fail validation. |
| P2 | AUTH-02 / AUTH-03: OTP replay and unenforced password-change flag | Authentication state does not enforce all its stated behavior. |
| P2 | Cron, nginx rollback, installer password serialization, security-only updates, Route 53 credentials | Operational actions can report success or intent that differs from the actual effect. |
| P2 | FE-04 / FE-05: polling ordering and confirmation encoding | Old results replace new results; valid resource names cannot be confirmed. |
| Before enabling blueprints | OPS-06: generated volume-name collisions | Different projects can acquire the same managed volume identity. |

P1 means address before release/use of the affected surface. P2 means a material correctness or security-control defect. Priorities reflect this root-equivalent, allowlisted, single-host product; authenticated admin functionality is not automatically an escalation vulnerability. The first three entries are expanded in the backend section where evidence is available.

## Authentication findings

### AUTH-01 — High: second-factor verification can bypass its rate limiter through enrollment

**Locations:** `backend/internal/api/routes.go:76-87`; `backend/internal/api/handlers_auth.go:141-210`; `backend/internal/auth/service.go:372-383`.

**Evidence and reachability:** the partial-session group mounts both `/auth/2fa/enable` and `/auth/2fa/verify`. Only `handleTOTPVerify` calls `s.loginLim.Allow`. `handleTOTPEnable` neither rejects an already-enrolled account nor applies the verification limit. `ConfirmTOTPEnrollment` validates against the user's stored secret even if enrollment is already complete; a correct code regenerates recovery codes and the handler calls `VerifySecondFactor` to elevate the session. The partial group has no general API limiter.

**Impact:** an attacker who has passed the password step for an enrolled account can direct code guesses to the enable route, avoiding the intended OTP throttle. A successful guess also replaces recovery codes. Network allowlisting, a valid partial-session cookie and the CSRF header remain prerequisites; this is not an unauthenticated internet bypass.

**Fix:** make enrollment confirmation an atomic state transition permitted only for a pending, unenrolled account. Apply a shared account-scoped OTP attempt budget to every route that checks an OTP, including enrollment. Test attempts through both URLs and concurrent setup/enable requests.

**Validation:** source-traced across router, handler and service. No brute-force or live-account testing was performed.

### AUTH-02 — Medium: TOTP codes are reusable within the accepted time window

**Locations:** `backend/internal/auth/service.go:372-383`, `:403-427`; misleading replay-related comment in `backend/internal/api/handlers_auth.go:166-178`.

`ConfirmTOTPEnrollment` and `VerifySecondFactor` call stateless OTP validation and store no consumed time-step marker. The same valid code can elevate multiple partial sessions during the accepted skew window. The handler comment says enrollment has consumed the code and replay will be refused; the implementation does not do that.

**Impact:** someone with the password and one observed live code can reuse it. This is a replay-resistance gap, not a claim that a password alone bypasses enrolled 2FA.

**Fix:** atomically record and reject already-consumed TOTP counters per account, including enrollment; keep recovery-code consumption atomic. Use additive schema migration and a deliberate enrollment-to-session transition so enabling replay protection does not break enrollment. Test concurrent reuse and adjacent skew windows. [NIST's OTP guidance](https://pages.nist.gov/800-63-4/sp800-63b.html) requires single acceptance of a valid OTP and effective account rate limiting.

**Validation:** source-traced; no live authentication attempt was performed.

### AUTH-03 — Medium: `mustChangePassword` does not restrict access

**Locations:** `backend/internal/auth/service.go:60`, `:122-151`, `:183-193`, `:280-340`; `backend/internal/httpx/authmw.go:93-126`; `frontend/src/app/(dashboard)/account/page.tsx:714`.

New dashboard accounts are created with `must_change_pw` set. The value is returned as `mustChangePassword` and displayed in account management, but neither login/session authorization nor the frontend auth flow enforces a password-change step. A user can continue using the administrator-assigned password and reach their normal capabilities indefinitely.

**Fix:** either implement a restricted password-change state with a narrowly allowed route set, or explicitly define this as an advisory flag and remove the promise that the change is mandatory. If mandatory, block token creation and normal feature access until the password changes. Test with both optional and required 2FA.

**Validation:** repository-wide references plus login/middleware inspection; no behavioral reproduction completed.

## Verification results and limits

| Check | Observed result |
| --- | --- |
| Backend `go build ./...` | Passed using Go 1.25.7 selected from `go.mod`. |
| Backend `go vet ./...` | Passed. |
| Backend `go test ./... -count=1 -timeout=5m` | All tested packages except config passed. `TestEnvFallsBackToLegacyPrefix` failed because the environment supplied `/usr/bin/zsh`; record this as an environment-sensitive test failure, not a proven production configuration bug. The config package passed after removing the interfering environment variable; the initial full-suite failure is retained in the evidence log. |
| Frontend frozen-lock install, lint, build | Passed; no lockfile change. Bun 1.4.0 was available, versus packageManager 1.3.11. |
| Frontend unit tests | 15 passed, 0 failed. |
| Isolated Chromium suite | 75 passed, 1 navigation timing failure; that test passed on isolated rerun. The first full run was not clean. |
| Targeted browser reproductions | Confirmed wrong-row deletion, wrong-table deletion, numeric rounding, stale poll replacement and confirmation encoding failures against intercepted APIs. |
| Safe backend/operations fixtures | Confirmed file-copy and SQLite explain defects; legacy log leakage, PM2 inherited UID, blueprint contract failures, nginx link rollback, Compose dotenv interpolation and APT origin behavior. Details in the supplements. |
| Bun advisory scan | Eight advisories; applicability and upstream references are triaged in the frontend section. Package hits are not treated as demonstrated application exploits. |
| Go advisory scan | The final explicit-Go-1.25.7 govulncheck run completed and reported 20 reachable advisory findings in one module plus the Go standard library. This is a failing dependency check, not 20 demonstrated application exploits. Earlier scanner attempts failed; the final run supersedes them. Full results are in evidence/jd-audit-govulncheck-final.log. |
| Full race/live integration gates | Not completed. No disposable live deployment host, real database engine matrix, firewall/sshd mutation, ACME/cloud account, privileged account-file test, restart or self-update validation was performed. |

The default frontend browser command initially reused another server on port 3000; that result was discarded. Authoritative browser results used the audit worktree's own server on port 3197, which was stopped afterward. Tests with mocked owners and intercepted APIs prove the described code behavior, not production deployment readiness.

The audit inspected code throughout the repository using inventories and security-sink searches, then followed high-risk paths in detail. Coverage manifests distinguish focused reading from broad scans. The deployment-plan README required by project instructions is absent from this revision, and the internal repository map omits newer packages; this limits documentation-based contract checking. Existing documented discrepancies in `docs/internal/reference/verification-findings.md` remain discrepancies, not approved exceptions.

One delegated review pass was interrupted by a tool restriction; the backend reviewer resumed to preserve already-collected defensive findings. Near report completion the environment changed to a restricted filesystem and normal commands began failing with `bwrap: loopback: Failed RTM_NEWADDR: Operation not permitted`. Incomplete follow-up results are identified above rather than represented as passes.

## Remediation sequence

1. Close alternate authorization paths first: Docker volume/Compose configuration, database explain/classification and OTP enrollment. Add route-level tests for readonly, limited and admin principals.
2. Repair filesystem and privilege boundaries: copy/move destination handling, account SSH files and PM2 execution. Prefer descriptor-based operations and dropping privileges before loading user-controlled code.
3. Stop secret capture in legacy deployment output and review retained transcripts for credentials that may require rotation.
4. Repair database UI resource identity, stable row selection and exact numeric transport before enabling risky editing workflows.
5. Complete or disable incomplete blueprint flows; validate every builtin through the actual normalized planner and executor, including protocols, ownership and persistence.
6. Correct the operational and shared-client defects documented below. Add tests aimed at failure/rollback paths, ambiguous identities, encoding and slow/out-of-order responses.
7. Update affected internal/operator docs with fixes, refresh vulnerable dependencies after checking compatibility, then run the full required gate plus disposable live, race and browser checks for changed deployment surfaces.

## Detailed review supplements

The following sections preserve the reviewers' evidence, exact source references, safe reproduction descriptions, recommended fixes and coverage limitations. IDs are stable within each section: `AUTH-*`, `OPS-*`, `FE-*`, and the backend review's IDs.


---

# Backend boundary audit — 0.6.7

Reviewed worktree: `/home/ubuntu/Just-Dashboard-audit-0.6.7`, revision `e44aeb5`. This report contributes the files/database/Docker/backup/Git/host-execution/logging slice of the wider audit. No implementation changes remain. No Docker mutation or live-service test was performed.

## Summary

The strongest issues are inconsistent authorization across Docker creation paths, a database explain endpoint that executes destructive SQL for read-only users, and file-copy destinations that escape configured roots through existing symlinks. Three defects were reproduced using temporary filesystem/SQLite fixtures; the SQL classification defect was also reproduced directly. Other findings below are source-confirmed and have explicit runtime limits.

### B01 — Critical: alternative Docker creation paths bypass administrator checks

**Impact:** A signed-in limited account can obtain the host-filesystem/container privileges explicitly reserved for administrators.

**Evidence and routes:**

- `backend/internal/api/handlers_docker.go:118–124` allows volume creation with `service.control`. `handlers_docker_manage.go:418–425` passes the submitted volume specification directly to `CreateVolume`.
- `backend/internal/dockerx/resources.go:28–32,47–53` accepts an arbitrary driver and options and passes the options to Docker as `DriverOpts`.
- `backend/internal/api/handlers_docker_manage.go:106–109` skips authorization/containment checks for every non-bind mount. A named volume can itself be backed by a host bind mount; its mount type does not establish that it is safe. Docker documents this capability in [the Compose volume reference](https://docs.docker.com/reference/compose-file/volumes/).
- A separate path has the same security-policy failure: `handlers_docker.go:159–184` allows limited users to create/edit Compose files and bring stacks up. `handlers_docker_manage.go:747–760` contains the Compose file's own directory but passes its arbitrary content to `NewStack`; it does not apply the privileged-container/host-path policy to the services declared inside. `dockerx/composeops.go:55–56,99–126` executes the resulting Compose action.
- The intended stronger policy is explicit at `handlers_docker_manage.go:85–126`; role grants are at `auth/roles.go:42–47`.

**Preconditions:** Network-allowed authenticated limited account; available Docker daemon; for the Compose path, a writable configured Compose root. This is not unauthenticated access.

**Verification:** Complete route-to-sink review and official Docker semantics; no live Docker operation was run. Existing `api/docker_spec_test.go` tests the direct bind-mount path and allows named volumes, leaving these alternative paths uncovered.

**Fix:** Centralize effective Docker-spec authorization across container, volume, and Compose execution. Require administrator capability for custom volume drivers/options capable of mounting host resources and inspect existing volume backing options before allowing use by limited accounts. Resolve every effective host path against the file roots. Apply the same policy to the fully resolved Compose model at execution time, including includes, environment substitution, and existing editable stacks. Restrict these creation/execution paths to administrators until a complete shared policy is available.

### B02 — High: read-only database explain can execute mutations

**Impact:** A read-only account can change/delete data using the credentials of a configured database connection, bypassing destructive capability, rate budget, and typed confirmation.

**Evidence and route:** `POST /api/v1/databases/{id}/explain` is mounted on the read surface (`backend/internal/api/handlers_db.go:72`). The handler checks only for a nonempty query and invokes the dialect (`handlers_db.go:1423–1445`). SQLite prepends its explain form (`backend/internal/dbx/dialect_sqlite.go:263–265`) and PostgreSQL prepends plain EXPLAIN (`dialect_postgres.go:175–177`), then `dbx.RunQuery` submits the complete string (`dbx.go:305–324`). No single-statement validation or database-enforced read-only boundary is applied.

**Preconditions:** Authenticated read-only or stronger account; at least one configured SQL connection whose credentials allow writes.

**Verification:** A temporary SQLite database containing one row was passed through the actual `ExplainPlan` implementation with an additional statement. The call returned no error and the row count became zero. The HTTP route's lack of a stronger capability was source-reviewed; this probe did not traverse a real authenticated HTTP server. PostgreSQL separately allows execution through user-supplied explain options: [official EXPLAIN documentation](https://www.postgresql.org/docs/current/sql-explain.html) confirms that ANALYZE executes the statement. No PostgreSQL/MySQL server test was run.

**Fix:** Accept exactly one parsed statement, construct explain syntax from a closed option set, prohibit execution-enabling options, and use an engine-enforced read-only transaction/connection where supported. Treat engines without a trustworthy nonexecuting plan mode as requiring the query runner's authorization. Add route-level tests for read-only callers, multiple statements, and execution options.

### B03 — High: file-copy/move destination symlinks bypass JD_FILE_ROOTS

**Evidence and routes:** `POST /api/v1/files/copy` and `/move` require `file.write` (`backend/internal/api/handlers_files.go:36–46`). `backend/internal/files/files.go:552–567` validates the destination with `ResolveEntry`, which intentionally leaves the final symlink unresolved. It then follows that entry with `os.Stat` and ultimately `os.OpenFile` (`files.go:602`). The move path has the same destination-directory pattern (`files.go:526–545`). Recursive copying likewise opens destination children without checking them for symlinks (`files.go:582–607`).

**Impact:** An existing outward symlink can cause backend-privileged writes beyond the roots the operator configured. The source can be a regular allowed file. This is an actual containment bypass, not merely a path-normalization concern.

**Preconditions:** Limited or admin account with file.write; configured restricted roots; an existing relevant destination symlink, including one created by a host process. The dashboard's separate Symlink endpoint already rejects outward targets; this finding does not claim that endpoint creates them.

**Verification:** Two temporary-directory tests reproduced successful writes outside the permitted root: one with a symlink destination file and one with a symlink destination directory. Move and recursive-child variants were source-reviewed but not separately executed.

**Fix:** Resolve destinations according to whether the operation replaces an entry or writes through it; enforce containment again after adding the source basename. Use rooted filesystem operations/no-follow checks throughout recursive copy, including destination child components, rather than validating only the top-level name. Add regression coverage for outward destination links and existing nested symlinks.

### B04 — High: SQL fail-closed classification is skipped for mixed batches

**Evidence and route:** `POST /api/v1/databases/{id}/query` permits `service.control`, with destructive authorization decided by `Classify` (`backend/internal/api/handlers_db.go:94,644–683`). The classifier sets an overall risk by regex and only checks unrecognized operations when that overall risk remains `read` (`backend/internal/dbx/dbx.go:553–574`). A recognized medium-risk operation therefore suppresses fail-closed handling for another unrecognized statement in the same batch. The backend then executes the original whole string (`dbx.go:305–324`).

**Impact:** Limited accounts can submit statement combinations that would require destructive capability when the unrecognized operation is classified alone. Filesystem-capable database commands can additionally invalidate the assumption that containing the original SQLite connection path contains all database access.

**Preconditions:** Authenticated limited account and a configured SQL connection/driver that accepts a multi-statement batch; engine privileges determine the downstream impact.

**Verification:** Direct classification probe returned `Level:medium, Destructive:false` for a batch combining an ordinary create operation and an otherwise restricted operation. Execution of that particular batch was not tested; SQLite multi-statement execution was independently demonstrated in B02.

**Fix:** Parse and classify every statement independently, take the maximum risk, and fail closed for any unsupported construct regardless of other statements. Prefer one statement per request until correct dialect-aware parsing is available. Do not rely on a shared regex classifier to enforce filesystem access or database privilege boundaries.

### B05 — Medium: copying a file onto itself silently destroys its contents

**Evidence and route:** `POST /api/v1/files/copy`; `backend/internal/files/files.go:564–567` adds the basename when the destination is a directory. `copyPath` then opens the source and opens the destination with `O_TRUNC` (`files.go:597–607`) without checking whether they refer to the same file.

**Impact:** A duplicate/paste request targeting the source's current parent, or two names for the same file, reports success while emptying the original file.

**Preconditions:** Authorized file-write operation; no malicious symlink is necessary.

**Verification:** Temporary-file unit test: source contained data, copy-to-current-parent returned nil, then the source was zero bytes.

**Fix:** Compare source/destination file identity before opening a writable destination; refuse same-file copies and directory copies into their own descendants. Use atomic replacement for completed regular-file copies so transfer failure preserves existing contents. Add same-path and same-inode regression tests.

### B06 — Medium: backup sources and local targets ignore configured file roots

**Evidence and routes:** `POST /api/v1/backups/` and `PUT /api/v1/backups/{id}` pass submitted job data directly into the store (`backend/internal/api/handlers_backups.go:101–146`). `backend/internal/backups/model.go:116–148` validates presence/type but never calls the root resolver. The runner walks source paths and creates/moves into the submitted local destination (`backups/runner.go:124–141,178–187`). Target testing also creates the target directory (`backups/objectstore.go:123–133`). The restore destination does correctly go through `files.Resolve` in `api/handlers_backups.go:294–300`, so the policy differs by operation.

**Impact:** Restricting JD_FILE_ROOTS does not restrict where a configured backup reads or writes; later manual/scheduled runs continue to use those paths even after roots are narrowed.

**Preconditions:** Administrator can create or edit backup jobs; a limited account may run a previously configured job. This is a configuration-boundary failure, not an administrator-capability escalation.

**Verification:** Source-confirmed; no backup job or external storage call was run.

**Fix:** Resolve sources and local target paths on save and again at execution/test time. Ensure historical jobs fail explicitly if a configuration change makes their paths unavailable.

### B07 — High: container recreation force-removes an unrelated colliding container

**Evidence and route:** `POST /api/v1/docker/containers/{id}/recreate`; `backend/internal/dockerx/create.go:862–863` derives a predictable parking name and unconditionally invokes `ContainerRemove(..., Force:true)` on it before renaming the requested original. No identity, ownership-label, or prior-replacement check establishes that the occupant belongs to this operation.

**Impact:** Recreating one container can stop and delete a different live container whose legitimate name collides with the generated parking name. Its writable layer is lost. A remaining fallback from an earlier failed replacement can also be deleted.

**Preconditions:** Administrator performing an ordinary recreate; another container occupies the derived parking name.

**Verification:** Source-confirmed; no Docker daemon operation was executed.

**Fix:** Generate an unused unique parking name or fail on collision. Never remove a pre-existing container merely because its name matches a naming convention. Track parked container identity explicitly and use it for cleanup/rollback. Cover this with a fake Docker API test asserting zero removals on collisions.

### B08 — Medium: backup artifact names can collide and overwrite archives

**Evidence:** `backend/internal/backups/runner.go:124–125` builds staging and destination artifact names using only sanitized job name and second-resolution time. `runner.go:168–169` opens the staging artifact with `O_TRUNC`; `perform` moves/uploads to the same derived name. The running lock is keyed by job ID (`runner.go:65–81`), while job names are neither unique nor collision-free after sanitization (`backups/model.go:116–148`, `backups/store.go:121–125`).

**Impact:** Different jobs with the same or sanitization-colliding names can overwrite/race one staging file when they start in the same second. Consecutive short runs of one job can also reuse a destination key, making distinct successful run records refer to one archive; retention can later remove the artifact referenced by the retained run.

**Preconditions:** Coincident job starts with colliding names, or two fast runs in one second. No malicious input is required.

**Verification:** Source-confirmed; no concurrent or external-storage execution test was run.

**Fix:** Include immutable job ID and unique run ID in every local and remote artifact name, create staging files exclusively, and record a unique storage locator per run. Test colliding names and same-second runs with temporary storage.

## Verification and limits

- Ran narrowly scoped temporary tests with `go test ./internal/files ./internal/dbx -run TestAudit -v -count=1`; exit 0. Their PASS means the fault was reproduced. Output is preserved in `/tmp/jd-audit-backend-tests.txt`.
- Removed both temporary probe test files after execution; no implementation changes were made.
- Read the repository instructions, security invariants, request lifecycle/runtime boundary guidance, relevant backend documentation, and Go security-best-practice reference.
- Closely traced the API and implementation paths cited above. Git operations, hostexec's argv/owner handling, archive safepath controls, backup storage/scheduling, and log authorization were also inspected. Absence of a finding in those inspected paths is not a claim that the entire package is defect-free.
- This slice did not execute Docker, host-management operations, network storage, live SQL servers, or concurrent restore/retention workloads. The outer audit is responsible for baseline build/test results and coverage of auth/deploy/frontend modules.
- The inspected-files list distinguishes substantive review from sampled excerpts. It is not a claim of line-by-line coverage of every file in the worktree.


---

# Operations/API audit supplement — 0.6.7 (`e44aeb5`)

## Scope and method

Reviewed the host-operation and deployment boundaries in deploy, procs, linuxusers, proxysvc, netsec, selfcfg, selfupdate, updates, jobs, agent, blueprint, gameserver, portalloc and stackports, their API route/handler integrations, and installation/Compose/build scripts. The supplied scope comprises more than 63,000 Go lines in the original operations packages alone, including tests. This was a risk-driven code inspection and repository-wide signal scan, not a claim that every line received equal depth. The file list separately records focused inspection and inventory/signal-scan coverage. Auth/core routing and files/databases/Docker internals were assigned to other reviewers.

Read AGENTS.md, internal index, security invariants, runtime boundary and relevant feature docs. The mandated `docs/plans/0.6.7-deployments/README.md` does **not exist** at this revision despite links from AGENTS.md and docs/internal/README.md; its contracts could not be checked.

No production file, user, crontab, firewall, package database or container was changed. Reproductions use Go source overlays and temporary fixture directories, `docker compose config` (no daemon operation), and `apt-get -s` against a wholly synthetic package database. Source files were not edited by this reviewer. Proof fixtures are in `/tmp/jd-audit-operations-overlay/`.

## Confirmed security findings

### OPS-01 — HIGH: reading PM2 inventory executes any local user's binary as root

- **Locations:** `backend/internal/procs/pm2_host.go:55`, `:85`, `:139`; `backend/internal/procs/exec.go:106`; `backend/internal/procs/pm2.go:108`; `backend/internal/api/handlers_procs.go:19`, `:105`, `:407`.
- **Reachability:** authenticated read-only `GET /api/v1/pm2/`, process inventory/detail, and related consumers. The attacker needs write access to an ordinary host account's home; the reader triggering execution can be a different person.
- **Cause:** discovery accepts `.nvm/versions/node/*/bin/pm2` or `.local/bin/pm2` from every `/home/*` directory. `runPM2Host` invokes `runWithEnv`, which calls `hostexec.CommandOnHost` and `cmd.Run()` without setting a credential, switching user, or applying `AsOwner`. Changing HOME/PM2_HOME does not change UID. The shipped backend runs as root and enters host namespaces. A writable shebang interpreter directory also leads PATH.
- **Impact:** local unprivileged-to-root execution when a normal read occurs; no dashboard mutation capability is required of the local attacker. Legitimate stopped per-user PM2 daemons can also be started as root and leave root-owned state.
- **Evidence:** overlay `TestAuditPM2ExecutesDiscoveredUserBinary` creates only a temporary fake `pm2` containing `id -u`; discovery accepts it, execution reports the parent's UID (1000 in this audit). Dockerfile/Compose and the call chain establish that the deployed parent is root. No privileged exploitation was performed.
- **Fix:** resolve a real host account, cross namespaces and then drop to its UID/GID/supplementary groups before loading any user-controlled executable; pass a minimal environment. Bind daemon identity to the account rather than trusting PM2 JSON's username. Add a regression test asserting executable UID differs from privileged caller UID for a non-root account.

### OPS-02 — HIGH: adding an SSH key follows local-user symlinks and transfers ownership of their targets

- **Locations:** `backend/internal/linuxusers/sshkeys.go:127-150`; route `backend/internal/api/handlers_linuxusers.go:26`.
- **Reachability:** admin adds a key using `POST /api/v1/system-users/{name}/keys` for an existing unprivileged account whose home that account controls.
- **Cause:** `.ssh` is created/traversed with `MkdirAll`, then `Chown`/`Chmod` follow it. `authorized_keys` is opened with normal `OpenFile` (follows symlinks), appended to, and then `os.Chown(path, uid, gid)` and `os.Chmod(path, 0600)` follow the link again. No lstat/no-follow/open-directory containment is used, and metadata errors are discarded.
- **Impact:** an account can pre-position `~/.ssh/authorized_keys` as a symlink to a root-owned writable target, such as `/etc/shadow`. A legitimate admin key addition appends data and hands ownership of that target to the user; the user can subsequently rewrite it. A symlinked `.ssh` directory can similarly change ownership/mode of an unrelated directory. API admin authorization does not authorize the less-privileged target account to choose the file modified.
- **Evidence:** deterministic source call chain; intentionally not reproduced against actual account files. A safe future regression fixture should use an injectable account resolver and a temp directory/file owned by a different UID.
- **Fix:** perform user-home operations as the target account where practical; use dirfd-based/no-follow traversal for both `.ssh` and the final key file, validate regular-file type, and apply metadata to opened descriptors. Refuse symlinks and propagate ownership/mode failures. Keep atomic replacement and race resistance.

### OPS-03 — HIGH: legacy deployment secrets are persisted and exposed through reader-accessible transcripts

- **Locations:** `backend/internal/deploy/deployer.go:252-260`; `backend/internal/deploy/legacy_engine.go:43-44`, `:81-93`; read routes `backend/internal/api/handlers_deploy.go:38-42`.
- **Reachability:** legacy/0.6.6-compatible deployment run; an authorized stored pre/post command or subprocess emits a project environment secret (ordinary debug output/errors can do this). Any reader can fetch `/api/v1/deploy/{id}/runs`, run details/streams and compatibility log projections.
- **Cause:** legacy hooks get decrypted env and write stdout/stderr directly into `legacyOutputWriter`. That writer persists raw bytes to `StepOutput.Log`. Unlike normalized release tasks, no redactor wraps this path.
- **Impact:** bypasses separate admin-only env reveal; secrets remain in SQLite/run logs and live streams after the process finishes.
- **Evidence:** an overlay of the existing `TestLegacyCompatibilityPipelineRunsThroughPersistentEngine` changes only the fixture pre-command to `printf hook-ran > hook.txt; printf '%s\n' "$FIXTURE_SECRET"`. Existing test assertion fails with **`sealed environment value leaked into the compatibility log`**. The fixture value is synthetic (`must-not-appear-in-log`).
- **Fix:** redact every legacy stdout/stderr/error path using resolved secrets before publishing/persisting, with buffering across chunk boundaries. Add emitting-secret test rather than only checking that a non-emitting hook did not leak it. Consider scrubbing existing retained transcripts after fixing capture.

## Confirmed functional/edge-case findings

### OPS-04 — MEDIUM: host crontab UI reads and edits the container's unused crontab spool

- **Locations:** `backend/internal/procs/cron.go:47-51`, `:66-86`; direct executor `backend/internal/procs/exec.go:59`; `docker-compose.yml:102-121`; backend image installs cron but starts only dashboard.
- **Reachability:** `GET/PUT /api/v1/cron/{user}` on the normal Docker Compose installation.
- **Cause:** `crontab` uses direct local execution. `/var/spool/cron` is not mounted from the host; `/host` and `/etc` mounts do not change the binary's default spool. Setting `SYSTEMD_IGNORE_CHROOT` only affects systemd.
- **Impact:** real user crontabs are reported missing, API-written jobs can appear successfully saved but never execute, and disappear when backend container is replaced.
- **Evidence:** namespace/mount/entrypoint source proof; no live crontab was changed.
- **Fix:** run the host's crontab in host namespaces and send content on stdin (`crontab -u user -`) so a container-only temp filename does not become a second bug. Read/list consistently through that same boundary. Add disposable-container host-spool integration coverage.

### OPS-05 — HIGH functional blocker: blueprint catalogue/rendering is wired into a runtime that cannot deploy it

- **Locations:** `backend/internal/deploy/source_materializer.go:122-124`; executor `backend/internal/deploy/normalized_executor.go:156-160`; `backend/internal/deploy/blueprint_source.go:174-192`; `backend/internal/deploy/checks.go:65-67`; builtins `minecraft-java.json`/`minecraft-bedrock.json`.
- **Reachability:** admin uses the exposed blueprint renderer and deployment draft/commit/run flow (`/api/v1/deploy/blueprints/{id}/render`).
- **Cause:** `SourceModeBlueprint` materialization is still an explicit unimplemented error (`blueprint materialization is owned by checkpoint C9`). Both Minecraft plans also emit a 600-second readiness timeout, while normalized validation permits at most 60 seconds. Thus the displayed defaults cannot be accepted/executed end-to-end.
- **Evidence:** `TestAuditBlueprintContract` renders Java, Bedrock, Dozzle and PostgreSQL without Docker. All materializations return the explicit unsupported-source error. Java and Bedrock `Configuration.Validate()` return `invalid planned check "Server process is healthy": attempts and timing are outside the supported bounds`.
- **Fix:** complete blueprint-to-image materialization/execution and reconcile check timing contracts; test every shipped blueprint through normalized validation and a fake-owner end-to-end execution. Until then mark unsupported catalogue entries explicitly instead of presenting a runnable flow.
- **Important further contract defects before enabling execution:** `blueprint_source.go:125-143` discards port protocol, while `runtime_owner.go:139-142` always publishes TCP (Bedrock needs UDP); `blueprint_source.go:149-153` converts Dozzle's socket into a named volume and forces `ReadOnly:false` (proof output: `Source:audit-socket Target:/var/run/docker.sock ReadOnly:false`), so it does not mount a Docker socket; `blueprint_source.go:289-293` maps every `save` action to Java `save-all flush`, including Bedrock's default backup schedule. These downstream issues are latent behind the current deployment blocker; no claim is made that a stock blueprint already runs successfully.

### OPS-06 — HIGH data-integrity risk, currently latent behind OPS-05: distinct deployment names alias the same managed data volume

- **Locations:** `backend/internal/deploy/blueprint_source.go:148-157`, `:211-235` (`blueprintVolumePrefix`).
- **Cause/repro:** normalization maps underscores/spaces to hyphens, removes other characters, lowercases and truncates at 40 characters without adding a deployment ID or collision-resistant suffix. Valid distinct names `same_name` and `same-name` both produce **`same-name-data`** in `TestAuditBlueprintContract`.
- **Impact once blueprint runtime is enabled:** two projects can mount and modify the same database/world volume, and both record it as `OwnershipManaged`; removal of one can remove the other's data. Names differing only beyond 40 characters collide too.
- **Fix:** derive persistent volume identity from immutable project/environment IDs, preserve it across rename/releases, and detect pre-existing conflicting ownership before adoption/removal. Include normalization/truncation collisions in tests. Keep this separate from a current exploit claim because OPS-05 blocks normal fresh execution.

### OPS-07 — MEDIUM: PM2 control and logs target the wrong account when process names repeat

- **Locations:** `backend/internal/procs/pm2.go:254-286`, `:326-337`; route family `backend/internal/api/handlers_procs.go:18-30`.
- **Reachability:** host has per-user PM2 daemons, e.g. `alice` and `bob` both manage a process named `api`; operator uses `/api/v1/pm2/api/restart`, delete, stop, reload or logs.
- **Cause:** inventory merges all users but routes identify only `name`. `ownerOf` returns the first name match and `LogPaths` likewise returns the first match; there is no request parameter carrying daemon/user identity.
- **Impact:** restarting/deleting one displayed application can affect another account's application; the other instance cannot be addressed reliably.
- **Evidence:** deterministic route/data-flow inspection. Not executed against real PM2 processes.
- **Fix:** expose and require stable `(account/daemon, pm_id)` identity for control and logs; validate against a refreshed inventory. Reject ambiguous legacy name-only requests instead of picking the first.

### OPS-08 — MEDIUM: failed nginx site validation loses the previous enabled symlink

- **Locations:** `backend/internal/proxysvc/sites_apply.go:376-395`, rollback call `:345-348`.
- **Reachability:** an existing `sites-enabled/<name>` points to a different target than the new site; admin saves/enables an invalid site through `/api/v1/proxy/sites`.
- **Cause:** `linkEnabled` removes the prior link and creates the new one, but returned undo closure only removes the new link. It never recreates the old link. Failure after removing the old link but before creating the new link also lacks restoration.
- **Impact:** request returns validation failure and claims restoration, but the formerly enabled site has been disabled on disk; next reload/restart drops it.
- **Evidence:** temp-only `TestAuditLinkRollbackLosesOriginal` creates an old symlink, calls `linkEnabled(newTarget)` and invokes undo. `Readlink` returns `no such file or directory`.
- **Fix:** snapshot old link existence/target and restore it on every failure path; test enabled-link replacement plus validation failure, not only creation of a new link.

### OPS-09 — MEDIUM: installer silently changes user-chosen bootstrap passwords containing dollar syntax

- **Locations:** `install.sh:315-320`, `:463`; `docker-compose.yml` bootstrap password interpolation.
- **Reachability:** first install, operator chooses manual password containing `$name`, `${...}`, leading quoting or dotenv-significant comment/whitespace syntax.
- **Cause:** input accepts arbitrary non-newline characters, then writes `JD_BOOTSTRAP_PASSWORD=$ADMIN_PW` unquoted to a Compose `.env`. Compose interpolates unquoted/double-quoted env values before the backend receives them.
- **Impact:** entered password differs from created password (or becomes too short and prevents bootstrap), causing initial login failure and potentially weakened credentials.
- **Evidence:** temp `.env` with synthetic `JD_AUDIT_PASSWORD=Abcd1234$JD_AUDIT_MISSING!`; `docker compose ... config --format json` produces **`Abcd1234!`**. No container was started and no actual secret printed.
- **Fix:** serialize arbitrary passwords in a literal Compose dotenv-safe form (including quote/backslash edge cases), use a proper serializer or dedicated secret file, and integration-test rendered env bytes equal entered bytes.

### OPS-10 — MEDIUM: APT security-only updates still install unrelated non-security upgrades

- **Locations:** `backend/internal/updates/updates.go:144-150`; `backend/internal/api/handlers_packages.go:157-181`.
- **Reachability:** `POST /api/v1/packages/upgrade?security=true`, confirmation phrase `install security updates`.
- **Cause:** `apt-get -t <security-suite> upgrade` raises preference for that suite; it does not exclude upgrades for packages only present in regular suites.
- **Impact:** UI/audit/typed phrase promise a security-only update while unrelated packages/services can be upgraded.
- **Evidence:** completely offline synthetic apt lists/status fixture: installed `audit-package=1.0`, `2.0` exists only in `normal`, `audit-security` has no package. Running `apt-get -s ... -t audit-security upgrade` returns **`Inst audit-package [1.0] (2.0 Audit:normal [amd64])`**. No system package state was touched.
- **Fix:** restrict the candidate package/version set using trusted security-origin metadata (including necessary dependencies), or use distro-supported unattended-upgrades origin restrictions; show the actual planned changes before confirmation. Test with both security and regular-only candidates.

### OPS-11 — MEDIUM: saved Route 53 DNS credentials are never passed to certbot

- **Locations:** `backend/internal/proxysvc/dns01.go:53-54`, `:124-125`, `:205-208`; `backend/internal/proxysvc/certbot.go:260`; `/certificates/dns-credentials` handler.
- **Reachability:** operator saves AWS access/secret keys through the offered provider form, then issues a Route 53 DNS certificate on a host without a separate IAM role/AWS credential setup.
- **Cause:** save writes `/etc/letsencrypt/jd-dns/route53.ini`; the provider argv intentionally contains only `--dns-route53`. There is no reference to the saved file in execution, no `AWS_SHARED_CREDENTIALS_FILE`/AWS env wiring anywhere in these handlers/services, and no AWS profile section is added to the suggested input format.
- **Impact:** UI accepts and persists a secret that issuance never uses; issuance fails unless unrelated pre-existing machine credentials happen to work, in which case it may use the wrong AWS account.
- **Evidence:** full route/provider writer/argv call-chain inspection and repo search for AWS env wiring; no live ACME/AWS calls made.
- **Fix:** write a valid AWS credential profile and explicitly bind it to the certbot subprocess (and renewal configuration), or support only ambient IAM and remove the nonfunctional credential entry. Test a mocked subprocess receives exactly the intended credential source.

## Additional review items (not promoted to demonstrated findings)

- Lifecycle admission needs a concurrency test: `selfcfg.Service.Apply` reads run state, writes shared `.env.jd-tmp`/`.env.jd-previous`, and only later calls `Applier.Start`; state check/save/start is not protected by an operation-wide lock. `selfupdate.Installer.Start` similarly checks then saves then removes/recreates a fixed updater container without serialized admission. Concurrent requests can interleave and config apply/restart can race self-update. See `selfcfg/service.go:223`, `:269-289`, `selfcfg/apply.go:91-135`, `selfupdate/install.go:105`. Validate with barriers/fake Docker before shipping a fix; no live lifecycle race was attempted.
- `findPM2Bin` lexically sorts node version directory names, so v9 can outrank v22 and v22.9 can outrank v22.10. Owner execution (OPS-01) and identity (OPS-07) take precedence, but choose newest via semver afterwards.
- Game property read returns entire `server.properties` (`handlers_gameserver.go:315-317`) to all readers, which normally contains RCON password. Fresh blueprint execution is blocked (OPS-05), so current reachable setup should be established before calling this a shipped disclosure. Redact secret fields before enabling the feature end-to-end.
- Blueprint game properties use a deliberately incomplete Java-properties parser; escaped separators/continuation lines and duplicate keys need round-trip tests. Zero-valued numeric minima currently mean “unset,” so spawn-protection's declared minimum 0 does not reject negative values (`gameserver/properties.go:192`).
- Direct `os/exec`/parallel path validation drift remains documented in `docs/internal/reference/verification-findings.md`; do not count the mere presence of direct execution as an exploitable finding without tracing its namespace/caller/argument behavior.

## Coverage limits

No live host firewall/sshd changes, ACME issuance, cloud credential use, package upgrade, user/key management, actual cron scheduling, Docker deployment/rollback, server restart/self-update, privileged UID transition, or race stress against production services was performed. Remote supply-chain version/CVE verification is outside this supplement. Root reviewer owns full build/vet/unit/browser gate results. Missing deployment-plan documentation constrains contract verification. The temporary proofs demonstrate the narrowly named behavior, not blanket production readiness.


---

# Frontend audit — 0.6.7

Audited checkout: `/home/ubuntu/Just-Dashboard-audit-0.6.7`, commit `e44aeb5`.
Date: 2026-09-15. No application fixes, dependency changes, commits, pushes, Docker operations, or live backend mutations were made by this reviewer. Reproduction scripts use intercepted API responses and capture outgoing mutations without sending them to a real database.

## Executive summary

Three browser-confirmed database integrity defects deserve high priority: selection can move to another row after sorting, changing tables can combine old row data with a new mutation target, and the row editor rounds large integers before sending them. Shared polling also allows overlapping requests and stale response replacement. Typed confirmations cannot represent every legal object name. Existing checks largely pass but do not cover these cases.

No working frontend-origin authentication bypass, CSRF bypass, or DOM XSS was established. Dependency audit findings are triaged below; vulnerable package versions do not by themselves prove this deployment is remotely exploitable.

## Confirmed findings

### FE-01 — High / P1: sorting or filtering reassigns a selected row before deletion

- **Locations:** `frontend/src/components/database/browse-tab.tsx:115`, `:228-251`, `:313-318`, `:576-583`; `frontend/src/components/database/result-grid.tsx:81-84`, `:112-122`, `:203-212`.
- **Evidence:** selection is a `Set<number>` of array indices. `toggleSort` and filter handlers replace the row ordering without clearing selection. `deleteSelected` resolves those indices against the *new* `rows.data` when the user confirms.
- **Reproduction:** populate rows `[id=1, id=2]`; select row 1; sort so rows are `[id=2, id=1]`; press Delete 1 and confirm. Chromium captured `DELETE /databases/1/rows` with `{"schema":"public","table":"items","key":{"id":2}}`, despite the original selection being id 1. The same selection issue affects Copy as SQL; shrinking a filter below a selected index can throw while dereferencing the row.
- **Impact:** an authorized operator can delete a different record than the one selected. The confirmation only names a count and table, so it does not reveal the changed identity.
- **Fix:** keep selection by immutable primary-key identity, or clear it for every row-result identity change, including sorting, filters, connection changes and replaced results. Freeze the resource identity and key set for the dialog. Disable mutations while rows and table metadata are not current.
- **Regression test:** select one key, reverse order, and assert deletion still targets that key or requires reselection; repeat for filter shrink and connection change.
- **Proof:** `/tmp/jd-audit-frontend-repro.mjs`, `/tmp/jd-audit-frontend-repro.log`, `SORT_DELETE`.

### FE-02 — High / P1: stale rows remain actionable under a newly selected table

- **Locations:** `frontend/src/hooks/use-poll.ts:39-42`, `:55-96`, `:100`; `frontend/src/components/database/browse-tab.tsx:129-171`, `:228-251`, `:609-632`.
- **Evidence:** `usePoll` retains `data` and `loading=false` when its dependencies change. BrowseTab immediately derives the mutation target from the new `selection` but continues rendering old rows and exposing their mutation controls.
- **Reproduction:** load table `items`; hold responses for table `other`; select `other`; select a still-visible old row and delete it. Before any `other` result arrived, Chromium captured `DELETE /databases/1/rows` with `{"schema":"public","table":"other","key":{"id":1}}` based on the old `items` row. This does not depend on an overlap between interval polls: both database polls use interval zero.
- **Impact:** if both tables share a primary key, an unrelated row can be removed from the newly selected table. An unsuccessful or indefinitely slow load extends the dangerous window. Editing and duplicating stale rows can similarly apply data to the wrong target.
- **Fix:** distinguish refreshing the same resource from changing resource identity. Return a query/result identity with poll data, clear or hide data when that identity changes, and prevent mutation until rows, metadata, connection and selection all match. A keyed table workspace can provide a simple local boundary; avoid globally discarding same-resource data needed by restart/update polling.
- **Regression test:** hold new-table reads, change selection, and assert old rows have no enabled edit/delete actions; repeat across connections.
- **Proof:** `/tmp/jd-audit-frontend-repro.log`, `STALE_TABLE_DELETE_BEFORE_OTHER_READ`.

### FE-03 — High / P1: row editing silently changes BIGINT and exact-decimal values

- **Locations:** `frontend/src/components/database/row-editor.tsx:64-71`, `:164-178`; transport parsing at `frontend/src/lib/api.ts:163-164`.
- **Evidence:** integer/serial types are converted with `Number(value)` and accepted using `Number.isInteger`, which does not imply the integer is exact. Numeric/decimal types are also converted to binary floating point. Non-finite values pass `!Number.isNaN` and become JSON `null`.
- **Reproduction:** insert bigint `9007199254740993` in RowEditor. The captured request contains `9007199254740992`. Values like decimal `0.1234567890123456789` are also rounded; `1e400` becomes `null` during JSON serialization.
- **Impact:** successful inserts/updates can store a different value from the value entered. Large keys returned as JSON numbers can already be rounded before an edit or delete, so the backend response contract also needs review.
- **Fix:** carry exact numeric values as decimal strings with column/type-aware conversion on the backend. At minimum reject unsafe integers and non-finite values rather than changing them. Keep BIGINT, NUMERIC/DECIMAL and primary-key values exact through reads, edit state, request JSON and driver parameters; ordinary `JSON.parse` cannot preserve raw large-number tokens.
- **Regression test:** round-trip values above `2^53`, max signed/unsigned 64-bit values, long decimals and exponent overflow; assert exact outbound bytes and database values.
- **Proof:** `/tmp/jd-audit-frontend-repro.log`, `BIGINT_INSERT`.

### FE-04 — Medium / P2: shared polling stacks slow requests and permits an old response to overwrite a newer response

- **Locations:** `frontend/src/hooks/use-poll.ts:58-64`, `:75-88`; contradictory documented contract in `docs/internal/frontend/data-theming.md:7`.
- **Evidence:** one AbortController belongs to the whole effect; `setInterval` calls `run()` again without cancelling or awaiting the previous request. The only cancellation happens when the effect cleans up. Every completion can call `setData`.
- **Reproduction:** hold the audit request, advance its 15-second interval, fulfill the later request with `NEWER_RESPONSE`, then fulfill the earlier active request with `OLDER_RESPONSE`. The UI changes back to `OLDER_RESPONSE`. The development-mode probe saw three unanswered requests (one Strict Mode initial request plus two interval-related requests); the stale overwrite proves two active completions were accepted.
- **Impact:** stale statuses and audit results can replace current results; slow or stuck requests accumulate, adding load to an already slow server. This is independent from FE-02's resource-identity retention.
- **Fix:** use one in-flight request, schedule the next poll after completion, or abort the prior run with a new per-run controller and reject superseded generation IDs. Keep manual refresh, hidden-tab behavior and cleanup explicit.
- **Regression test:** delayed, out-of-order completions cannot regress displayed data; only one active fetch is allowed under a slow endpoint.
- **Proof:** `/tmp/jd-audit-frontend-poll-repro.mjs`, `/tmp/jd-audit-frontend-poll-repro.log` (`LATE_OLD_RESPONSE_REPLACED_NEWER true`).

### FE-05 — Medium / P2: typed confirmations cannot handle Unicode or whitespace-delimited object names

- **Locations:** `frontend/src/lib/api.ts:143-147`; `frontend/src/app/(dashboard)/files/page.tsx:290-308`; `frontend/src/components/database/browse-tab.tsx:380`, `:402`; `backend/internal/httpx/confirm.go:64-77`.
- **Evidence:** the exact phrase is inserted directly into an HTTP header. Browser Headers uses a byte-string conversion. Chromium rejects `new Headers({"X-Confirm":"目录"})` with `String contains non ISO-8859-1 code point`. Separately, the backend trims the supplied phrase before comparing it to the untrimmed resource name; `" folder "` can never match.
- **Reproduction:** request recursive deletion of a directory named `目录`, or drop a table with that name, and type the displayed name; fetch fails before sending. For a directory/table named ` folder `, any raw matching input is trimmed and refused. ASCII control characters likewise cannot be carried directly in headers.
- **Impact:** legitimate resources become undeletable through the affected UI/API confirmation path; retrying the displayed phrase cannot succeed. This is an availability/interoperability issue, not a confirmation bypass.
- **Fix:** define an ASCII-safe encoding for exact UTF-8 confirmation bytes and decode it server-side before exact comparison, or carry the phrase in validated JSON while retaining the custom CSRF proof. Version the protocol if necessary; do not silently remove confirmation or allow query parameters on ordinary HTTP mutations. Preserve exact decoded whitespace.
- **Regression test:** browser-to-handler round trips for non-Latin, emoji, quotes and leading/trailing spaces; wrong phrases remain rejected.
- **Proof:** `/tmp/jd-audit-frontend-unicode-repro.mjs`, `/tmp/jd-audit-frontend-unicode-repro.log`.

### FE-06 — Low / P3: query-result CSV is malformed for valid column names

- **Location:** `frontend/src/components/database/query-tab.tsx:423-431`.
- **Evidence:** values are escaped, but headers use `result.columns.join(",")`; escaping also omits carriage return.
- **Reproduction:** columns `["a,b","name"]`, row `[1,2]` export as `a,b,name\n1,2`, with three header columns and two value columns. SQL aliases may contain commas, quotes and newlines.
- **Impact:** imports and spreadsheet interpretation corrupt the exported shape for valid results.
- **Fix:** apply the same CSV escaping to headers and values and handle `\r` as well as `\n`; test round-trip parsing. Consider a separately documented spreadsheet-safe export policy for formula-bearing strings rather than silently changing raw data exports.
- **Proof:** `/tmp/jd-audit-frontend-csv-repro.log` was produced by extracting/transpiling the actual `toCSV` implementation.

## Dormant helper defect (not an exposed route finding)

`frontend/src/lib/docker-run.ts:253-270` ignores explicit boolean values. Parsing `docker run --privileged=false --rm=false alpine` produces `privileged:true, autoRemove:true` without warnings. `--pull=never` becomes `missing`. Confirmed using the actual exported parser; output is in `/tmp/jd-audit-frontend-docker-parser-repro.log`. **No imports/callers of this parser exist in the audited frontend**, so this is low-priority dormant code, not proof that the current UI creates privileged containers. Fix boolean semantics and unsupported pull policy before wiring the helper into a flow, or remove obsolete code.

## Dependency audit and applicability

`bun audit` reported eight advisories: 2 critical, 2 high, 2 moderate, 2 low. Evidence: `/tmp/jd-audit-frontend-audit.log`; pinned direct versions `frontend/package.json:29-33`, lock records `frontend/bun.lock:660`, `:878`, `:946`, `:956`, `:1072`.

| Package / advisory | Upstream fix | Applicability to this checkout |
| --- | --- | --- |
| Next 16.3.1, [AVIF optimizer RCE](https://github.com/vercel/next.js/security/advisories/GHSA-2xp9-vwfh-vxw4) | Next 16.3.3 | Upgrade promptly. Image optimizer remains enabled, but no `next/image` imports, remote image allowlist, or shipped AVIF assets were found. An attacker-controlled AVIF source reaching the optimizer was **not demonstrated**. |
| Sharp 0.35.3, [libheif flaws](https://github.com/advisories/GHSA-rgj7-g3m4-5g8c) | Sharp 0.35.4 / libheif 1.23.2 | Related AVIF decoder issue, relevant to glibc Linux if untrusted input reaches the decoder; not a second independent proven application exploit. Refresh the lock and confirm the runtime native binary version. |
| Next 16.3.1, [Windows-hosted RCE](https://github.com/advisories/GHSA-p293-qw3h-jr36) | Next 16.3.3 | Standard frontend Dockerfile uses Debian Linux; the Windows-filesystem prerequisite is absent from the supported deployment. |
| DOMPurify 3.4.8, [custom-element hook bypass](https://github.com/advisories/GHSA-c2j3-45gr-mqc4), [persistent config pollution](https://github.com/cure53/DOMPurify/security/advisories/GHSA-cmwh-pvxp-8882), [Trusted Types config lifetime](https://github.com/advisories/GHSA-vxr8-fq34-vvx9), [IN_PLACE detached subtree](https://github.com/cure53/DOMPurify/security/advisories/GHSA-55q2-fjhq-7xh7) | Version containing all four fixes, at least 3.4.13 | Monaco's inspected wrapper passes config to individual sanitize calls, removes hooks, and does not use the implicated `setConfig`, `clearConfig`, `IN_PLACE`, or `CUSTOM_ELEMENT_HANDLING` combinations. No working XSS established. Monaco also vendors DOMPurify under `esm/vs/base/browser/dompurify/`, so overriding only the npm dependency is insufficient evidence that the served `/monaco/vs` bundle is fixed. |
| js-yaml 4.3.1, [empty-merge CPU exhaustion](https://github.com/advisories/GHSA-2883-xcg3-v3hh) | 4.3.2 | Found under ESLint's dev-tool dependency path; no production frontend YAML parser entry point found. Treat as development dependency hygiene. |

Next's installed `fetchInternalImage` creates a fresh internal request without copying browser cookies. Therefore, pointing `/_next/image` at the authenticated file-preview API is not, by itself, evidence that arbitrary host AVIF files reach the decoder. No exploit payload or production attack was attempted.

## Verification results

| Check | Result | Log |
| --- | --- | --- |
| `bun install --frozen-lockfile` | Passed; lock unchanged | `/tmp/jd-audit-frontend-install.log` |
| `bun run lint` | Passed, exit 0 | `/tmp/jd-audit-frontend-lint.log` |
| `bun run build` | Passed, exit 0, compiled and typechecked | `/tmp/jd-audit-frontend-build.log` |
| `bun test src` | 15 passed, 0 failed, 32 assertions | `/tmp/jd-audit-frontend-unit.log` |
| `bun run test:browser:install` | Passed | `/tmp/jd-audit-frontend-browser-install.log` |
| Full Chromium browser suite against owned audit server | 75 passed, 1 failed | `/tmp/jd-audit-frontend-browser-isolated.log` |
| Rerun of sole failed test | Passed, 1/1 | `/tmp/jd-audit-frontend-browser-rerun.log` |
| `bun audit` | Exit 1; eight advisories, triaged above | `/tmp/jd-audit-frontend-audit.log` |
| Targeted browser reproductions | FE-01 through FE-05 behavior confirmed | Files named above |

The failed full-suite test was `deploy-ui.spec.ts:11`, quick setup → wizard success: its 5-second URL assertion expected `/deploy/77` but still saw the configuration step. It passed on isolated rerun in 6.0 seconds total. Cause is not established; report a transient test/navigation timing failure, not a proven application regression and not a completely clean one-shot gate. Do not hide the initial failure.

The default browser command initially reused an already-running port-3000 server. That run was stopped and is **not authoritative**. The full result above used `JD_BROWSER_BASE_URL=http://127.0.0.1:3197`, with `bun dev --hostname 127.0.0.1 --port 3197` started from this worktree. Dev server log: `/tmp/jd-audit-frontend-server.log`. Toolchain available was Bun 1.4.0, while packageManager records 1.3.11. No other package manager was used.

## Coverage and limits

- Inventoried frontend routes, shared components/hooks/libs, build/runtime config, local Monaco delivery and existing browser/unit tests. Broad searches covered browser HTML/JS sinks, URL assignment, external links, storage, direct fetches, mutation headers, WebSocket construction and suspicious coercion/error paths. Path manifest: `/tmp/jd-audit-frontend-files.txt`; this is the scanned surface, not a claim of a line-by-line proof of every file.
- Focused manual tracing covered auth/login/2FA state, capability affordances, API error/CSRF/confirmation transport, polling, socket reconnection and command rerun prevention, terminal input/upload/paste boundaries, file read/edit/upload/media previews, database read/edit/delete/import/export, deployment handoff/secret storage/run streams, Docker and Git actions, metrics state, CSP and local editor loading.
- Shared HTTP mutation paths send `X-JD-CSRF`; both direct multipart file uploads also use `mutationHeaders`. No client-only role check is treated as authorization. Session cookie behavior and capability enforcement remain backend responsibilities.
- Only two application `dangerouslySetInnerHTML` locations were found: static theme bootstrap with nonce and generated chart CSS. No attacker-controlled executable input was established for either. Media preview uses backend constrained content types; deployment preview backend escapes URL output and restricts its iframe. No ordinary HTML file preview injection was established.
- The browser suite uses mostly mocked APIs; it does not prove real Docker, PostgreSQL/MySQL/etc., terminal PTY, allowlist, authentication or TLS behavior. No live backend, Docker stack, real database, production CSP/browser integration, Firefox or WebKit run was performed here. No findings above should be described as an exhaustive guarantee that all possible bugs have been found.
- Known open cross-layer follow-up sent to parent: Redis SCAN cursors are backend uint64 JSON numbers, frontend JavaScript numbers, and parsed by `atoiDefault` server-side; inspect precision and signed-range handling independently.


---

# Go dependency advisory results

Command: `GOTOOLCHAIN=go1.25.7 go run golang.org/x/vuln/cmd/govulncheck@v1.1.4 ./...`.

The completed scanner reported 20 reachable advisory findings. Static call-graph reachability does not prove attacker-controlled inputs meet every vulnerability prerequisite. Prioritize updating the build toolchain and affected module, verify platform/configuration applicability, and rerun a current compatible scanner. No dependency was changed. Full traces: `evidence/jd-audit-govulncheck-final.log`.

## Vulnerability #1: GO-2026-6218

Avoid quadratic complexity in resolvePath in net/url  
[Go advisory](https://pkg.go.dev/vuln/GO-2026-6218)
Standard library  
Found in: net/url@go1.25.7  
Fixed in: net/url@go1.25.13  

## Vulnerability #2: GO-2026-6090

Limit handshake messages we are willing to accept post-handshake in  
crypto/tls  
[Go advisory](https://pkg.go.dev/vuln/GO-2026-6090)
Standard library  
Found in: crypto/tls@go1.25.7  
Fixed in: crypto/tls@go1.25.13  

## Vulnerability #3: GO-2026-6089

Apply ReadHeaderTimeout when doing unencrypted HTTP/2 check in net/http  
[Go advisory](https://pkg.go.dev/vuln/GO-2026-6089)
Standard library  
Found in: net/http@go1.25.7  
Fixed in: net/http@go1.25.13  

## Vulnerability #4: GO-2026-6088

Add recursion depth guard during decode in encoding/xml  
[Go advisory](https://pkg.go.dev/vuln/GO-2026-6088)
Standard library  
Found in: encoding/xml@go1.25.7  
Fixed in: encoding/xml@go1.25.13  

## Vulnerability #5: GO-2026-5972

Enforce maximum recursion depth in encoding/asn1  
[Go advisory](https://pkg.go.dev/vuln/GO-2026-5972)
Standard library  
Found in: encoding/asn1@go1.25.7  
Fixed in: encoding/asn1@go1.25.13  

## Vulnerability #6: GO-2026-5856

Invoking Encrypted Client Hello privacy leak in crypto/tls  
[Go advisory](https://pkg.go.dev/vuln/GO-2026-5856)
Standard library  
Found in: crypto/tls@go1.25.7  
Fixed in: crypto/tls@go1.25.12  

## Vulnerability #7: GO-2026-5039

Arbitrary inputs are included in errors without any escaping in  
net/textproto  
[Go advisory](https://pkg.go.dev/vuln/GO-2026-5039)
Standard library  
Found in: net/textproto@go1.25.7  
Fixed in: net/textproto@go1.25.11  

## Vulnerability #8: GO-2026-5037

Inefficient candidate hostname parsing in crypto/x509  
[Go advisory](https://pkg.go.dev/vuln/GO-2026-5037)
Standard library  
Found in: crypto/x509@go1.25.7  
Fixed in: crypto/x509@go1.25.11  

## Vulnerability #9: GO-2026-5026

Invoking failure to reject ASCII-only Punycode-encoded labels in  
golang.org/x/net/idna  
[Go advisory](https://pkg.go.dev/vuln/GO-2026-5026)
Standard library  
Found in: net/http@go1.25.7  
Fixed in: net/http@go1.25.13  

## Vulnerability #10: GO-2026-4981

Crash when handling long CNAME response in net  
[Go advisory](https://pkg.go.dev/vuln/GO-2026-4981)
Standard library  
Found in: net@go1.25.7  
Fixed in: net@go1.25.10  

## Vulnerability #11: GO-2026-4971

Panic in Dial and LookupPort when handling NUL byte on Windows in net  
[Go advisory](https://pkg.go.dev/vuln/GO-2026-4971)
Standard library  
Found in: net@go1.25.7  
Fixed in: net@go1.25.10  

## Vulnerability #12: GO-2026-4947

Unexpected work during chain building in crypto/x509  
[Go advisory](https://pkg.go.dev/vuln/GO-2026-4947)
Standard library  
Found in: crypto/x509@go1.25.7  
Fixed in: crypto/x509@go1.25.9  

## Vulnerability #13: GO-2026-4946

Inefficient policy validation in crypto/x509  
[Go advisory](https://pkg.go.dev/vuln/GO-2026-4946)
Standard library  
Found in: crypto/x509@go1.25.7  
Fixed in: crypto/x509@go1.25.9  

## Vulnerability #14: GO-2026-4918

Infinite loop in HTTP/2 transport when given bad SETTINGS_MAX_FRAME_SIZE in  
net/http/internal/http2 in golang.org/x/net  
[Go advisory](https://pkg.go.dev/vuln/GO-2026-4918)
Standard library  
Found in: net/http@go1.25.7  
Fixed in: net/http@go1.25.10  

## Vulnerability #15: GO-2026-4887

Moby has AuthZ plugin bypass when provided oversized request bodies in  
github.com/docker/docker  
[Go advisory](https://pkg.go.dev/vuln/GO-2026-4887)
Module: github.com/docker/docker  
Found in: github.com/docker/docker@v28.5.2+incompatible  
Fixed in: N/A  

## Vulnerability #16: GO-2026-4883

Moby has an Off-by-one error in its plugin privilege validation in  
github.com/docker/docker  
[Go advisory](https://pkg.go.dev/vuln/GO-2026-4883)
Module: github.com/docker/docker  
Found in: github.com/docker/docker@v28.5.2+incompatible  
Fixed in: N/A  

## Vulnerability #17: GO-2026-4870

Unauthenticated TLS 1.3 KeyUpdate record can cause persistent connection  
retention and DoS in crypto/tls  
[Go advisory](https://pkg.go.dev/vuln/GO-2026-4870)
Standard library  
Found in: crypto/tls@go1.25.7  
Fixed in: crypto/tls@go1.25.9  

## Vulnerability #18: GO-2026-4869

Unbounded allocation for old GNU sparse in archive/tar  
[Go advisory](https://pkg.go.dev/vuln/GO-2026-4869)
Standard library  
Found in: archive/tar@go1.25.7  
Fixed in: archive/tar@go1.25.9  

## Vulnerability #19: GO-2026-4602

FileInfo can escape from a Root in os  
[Go advisory](https://pkg.go.dev/vuln/GO-2026-4602)
Standard library  
Found in: os@go1.25.7  
Fixed in: os@go1.25.8  

## Vulnerability #20: GO-2026-4601

Incorrect parsing of IPv6 host literals in net/url  
[Go advisory](https://pkg.go.dev/vuln/GO-2026-4601)
Standard library  
Found in: net/url@go1.25.7  
Fixed in: net/url@go1.25.8  

The scan also reported 6 advisories in imported packages and 11 in required modules without an identified vulnerable call from this code. These require applicability review and are not counted as reproduced application findings.
