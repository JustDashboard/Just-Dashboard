# 0.6.7 audit remediation

Target: `/home/ubuntu/Just-Dashboard`, branch `patch/0.6.7`.
Starting revision: `ba51c62d5be2c706fe894359a14b0952a39475c7`.
Original audit baseline: `e44aeb5` (0.6.7). Review date: 2026-09-15.

Implementation and verification were completed in the primary checkout before committing or pushing
the changes. No release or running dashboard deployment was performed. The original architecture and
network/2FA/capability boundaries remain in place.

## Finding-by-finding status

The table records the final implementation after verification. Regression coverage accompanies the
security and data-integrity changes; operational limits are listed separately below.

| Finding | Remediation |
| --- | --- |
| AUTH-01: enrollment bypasses OTP attempt budget | Setup, enable and verify share one account-scoped limiter. Already-enrolled accounts cannot enroll again. |
| AUTH-02: reusable TOTP proofs | An additive `totp_last_step` migration records consumed counters. Code consumption, recovery-code use and session elevation are transactional; concurrent reuse is rejected. Enrollment completes its own session in the same transaction. |
| AUTH-03: temporary passwords grant full access | Backend sessions and API tokens cannot reach features until password replacement. Required second factors come first; replacement verifies the current password, rejects keeping it, and revokes sessions. The login UI follows this flow. |
| B01: alternative Docker paths bypass admin checks | Named-volume backing configurations, attached network drivers and shared/host namespaces are checked; custom volume drivers/options require admin. Effective recreate specs are authorized even when omitted. Compose creation, edits, validation and execution now require `system.admin` in the backend and UI. Non-admin stack details use static YAML service names without executing Compose or resolving the backend environment. |
| B02: explain executes database mutations | Every dialect accepts exactly one supported statement and uses fixed plan syntax. Execution options, extra statements and ambiguous dialect syntax are refused. SQL Server resets SHOWPLAN even after query errors or cancellation, discarding a connection if reset fails. |
| B03: file destination symlinks escape roots | Copy/move resolve the correct destination semantics and pin traversal beneath configured roots with `os.Root`. Recursive copies reject existing symlink children. Move replaces final file-link entries without touching their targets. |
| B04: mixed SQL batches classify too weakly | Statements are classified independently and the strongest risk wins. The execution endpoint accepts one statement per request; unknown or ambiguous syntax cannot be masked by a recognized statement. |
| B05: copying onto itself truncates the source | Same-inode, current-parent and descendant copies are refused. Regular-file replacement uses exclusive temporary files and atomic rename, preserving existing destination ownership, mode and POSIX access ACLs. Failure to preserve an ACL aborts replacement. |
| B06: backup paths bypass configured roots | Source/target paths are validated on save, test and execution; local artifact reads, restore destinations and retention obey current roots. Invalid legacy jobs remain readable and repairable. |
| B07: recreation deletes an unrelated parking-name container | The original container is parked by immutable ID under a random name using Docker's atomic rename. Name conflicts abort without removing anything. Failed candidates are cleaned up only by their returned ID. |
| B08: backup artifact collisions overwrite data | Names contain job/run IDs and random entropy, staging is private per run, archive creation is exclusive, and publishing cannot replace an existing file or symlink. |
| OPS-01: user-owned PM2 executable runs as root | PM2 executes as the account from the host account database, after namespace entry and privilege drop. The subprocess receives a minimal environment, bounded output, and no fallback to root execution. |
| OPS-02: SSH-key writes follow symlinks | Descriptor-anchored, no-follow access rejects symlink directories/files and unsafe hard links. Writes replace the account's key file atomically without chowning a followed target. |
| OPS-03: legacy deployment secrets appear in logs | Streaming redaction covers command transcripts before persistence, including secrets split across writes. Environment files use private random temporary files and atomic replacement. |
| OPS-04: cron edits the container spool | Reads and writes execute on the host. Writes pass the crontab through stdin, avoiding container-only temporary paths. |
| OPS-05: incomplete blueprint runtime | **Mitigated by disabling new blueprint deployments.** Catalog/render previews remain available with an explicit unsupported reason; source persistence, preflight, commit, queue admission and materialization fail early. Completing the runtime remains unfinished work. |
| OPS-06: blueprint volume names collide | Rendered managed-volume names include a digest of the original name and blueprint ID, preventing slug/case/truncation collisions. Existing persisted mappings are preserved; immutable project/environment ownership belongs to the unfinished runtime. |
| OPS-07: PM2 actions/logs select the wrong account | The client and API carry trusted daemon identity plus numeric process ID. Ambiguous legacy name-only requests fail rather than choosing a daemon. |
| OPS-08: nginx rollback loses an enabled symlink | Rollback restores the prior enabled entry and symlink target after failed validation. |
| OPS-09: installer changes passwords containing dollar syntax | Compose dotenv values are serialized literally, with regression coverage for interpolation, quoting and escape characters. |
| OPS-10: APT security-only upgrades install unrelated updates | Select exact security candidate versions for installed packages, simulate the restricted command, and reject extra changes/removals. There is no fallback to an unrestricted upgrade. |
| OPS-11: Route 53 ignores saved credentials | Saved credentials are validated as an AWS profile and passed privately through the environment for dashboard issuance and renewal. Host timer configuration is described below. |
| FE-01: sorting changes the selected row | Selection belongs to the exact query/result; sorting, filtering, paging and refresh clear it. |
| FE-02: old rows remain actionable in a new table | Resource changes hide old results immediately; late responses cannot populate the new resource. Editors remain bound to the original connection/table. |
| FE-03: exact database values round through JavaScript numbers | Integer and exact-decimal edits stay strings; non-finite floats are rejected. SQLite tests preserve adjacent bigint keys and decimal text; live PostgreSQL and MariaDB tests preserve BIGINT and DECIMAL(40,20) values. |
| FE-04: overlapping/stale polling | The next poll starts after the current request settles. Cleanup aborts and ignores obsolete responses; filter controls retain focus while results change. |
| FE-05: Unicode/whitespace confirmations fail | `X-Confirm-Encoding: uri` accompanies a percent-encoded exact phrase; the server decodes once and validates UTF-8. WebSocket phrases retain exact whitespace. |
| FE-06: CSV headers/CR are not escaped | One CSV encoder handles headers and cells, including commas, quotes, LF and CR. |

## Additional fixes found during implementation

- Update, apply, restart and rebuild share lifecycle admission before changing durable run records or
  `.env`. Pending/running operations block each other; corrupt records fail closed. If admission fails
  after saving settings, the original file is restored through the backend-visible path.
- PM2 discovery compares Node versions numerically. Daemon-returned usernames cannot choose execution
  credentials. PM2-supplied paths no longer automatically grant access outside `JD_LOG_ROOTS`.
- Game-property responses expose only declared public fields. Parsing preserves unrelated content and
  handles escaped/continued properties and duplicate keys; declared zero minima are enforced.
- Blueprint previews preserve commands and bounded health checks, distinguish UDP from TCP, use a
  read-only Docker socket link where declared, and omit Java save commands for Bedrock. Unsupported UDP
  execution is rejected explicitly.
- Redis cursors are opaque decimal strings, preserving the full unsigned 64-bit range.
- The dormant Docker-run parser honors explicit false flags and reports unsupported pull policy rather
  than claiming an equivalent conversion.
- The inherited `JD_TERMINAL_SHELL` environment no longer makes the configuration default test fail.
- The daemon-wide Docker prune integration tests require explicit opt-in and an explicitly selected
  disposable daemon; routine checks no longer trigger it merely because a Docker socket is present.
- PostgreSQL table and overview size queries tolerate a relation disappearing after the catalog
  snapshot. A real repeatable-read regression reproduces the NULL-size case deterministically.
- Terminal tests keep their private tmux server alive for the package lifetime and fail closed if
  private socket setup fails, removing startup/teardown races and protecting the default server.
- Documentation now acknowledges the absent historical deployment-plan directory instead of linking
  to unavailable checkpoint evidence.

## Dependencies

### Go

The backend module and Docker builder now require **Go 1.26.8**. The official image manifest was
verified. Binary download endpoints were unavailable in this environment, so validation uses the
official `golang/go` tag `go1.26.8`, commit `c293dd49cbe25e1fe8d97d94a5cb618e7b6d831e`, built locally with
the cached Go 1.25.7 bootstrap compiler. The resulting compiler reports `go1.26.8 linux/amd64`.

`govulncheck` v1.8.0 no longer reports the original 18 reachable standard-library findings. It still
flags GO-2026-4887 and GO-2026-4883 against `github.com/docker/docker` v28.5.2. These module-wide entries
describe **Docker daemon** AuthZ/plugin defects; this application links the client SDK and communicates
with a separately installed daemon. Updating the SDK alone does not patch that daemon. Docker's
advisories identify Engine 29.3.1 as fixed; the validation host reports Engine 29.8.0. Operators must
check their own host Engine, especially when using authorization plugins. See the
[AuthZ advisory](https://github.com/moby/moby/security/advisories/GHSA-x744-4wpc-v9h2) and
[plugin advisory](https://github.com/moby/moby/security/advisories/GHSA-pxq6-2prw-chj9).

The scanner therefore remains nonzero; this report does not describe it as a clean dependency gate.

`golang.org/x/crypto` is updated to v0.56.0 for the SSH channel denial-of-service fixes
[GO-2026-6354](https://pkg.go.dev/vuln/GO-2026-6354) and
[GO-2026-6355](https://pkg.go.dev/vuln/GO-2026-6355). The initial verbose scan reported these at package
level, without a reachable vulnerable call in this application. The unmaintained OpenPGP package and
three additional Docker daemon archive/copy advisories appear only as module-level findings; this
application does not import the affected packages.

### Frontend

Bun updates Next/eslint-config-next to 16.3.5, sharp to 0.35.4 and js-yaml to 4.3.2. The lockfile remains
`bun.lock`.

Four DOMPurify advisories remain in Monaco 0.56.0's bundled sanitizer. Monaco's shipped AMD bundle embeds
DOMPurify 3.4.8, so an npm override would not update the executed code. The inspected wrapper does not
enable the configurations required by those advisories; no exploitable application XSS was established.
This is an explicit dependency residual, awaiting a patched Monaco bundle. Details and primary advisory
links are in [the editor documentation](../../internal/frontend/shell-design.md#bundled-sanitizer-advisory-review-2026-09-15).

## Verification

Verification used the patched Go 1.26.8 compiler and Bun, with isolated PostgreSQL 16, MariaDB 11 and
Redis 7 fixtures, which were removed after verification. The running dashboard was not replaced. Detailed records are in
[the evidence directory](evidence/README.md).

| Check | Result |
| --- | --- |
| Backend `go build ./...` and `go vet ./...` | Passed, exit 0; final build also uses production `CGO_ENABLED=0`. |
| Backend `go test ./... -count=1 -timeout=5m` | Passed, exit 0, after the PostgreSQL and terminal-test fixes. |
| Required deployment/API/proxy/backups/store race gate | Passed, exit 0. |
| Focused auth, HTTP, lifecycle, updates and store race checks | Passed. |
| SQL statement/SHOWPLAN cleanup and rooted copy/move race checks | Passed; the final ACL follow-up also passed the complete files package under the race detector. |
| Opt-in Docker C4 artifact/build and C5 activation checks | Passed; test-owned workloads cleaned up. |
| PostgreSQL/MariaDB precision integration | Passed, including adjacent large IDs and exact DECIMAL(40,20). |
| Frontend lint and production build | Passed on the final frontend integration. |
| Frontend unit tests | 21 passed, 53 assertions. |
| Chromium browser tests | Earlier complete suite: 94/94. Final focused Docker suite: 26/26; security suite: 10/10. Across runs, 97 unique cases passed. |
| Installer dotenv regression and shell syntax | Passed: two Python tests including seven literal-value cases against real Compose interpolation. |
| Go and frontend dependency scans | Nonzero residual findings; see the dependency assessment above. |
| Formatting and `git diff --check` | Passed. |

The full backend run preceded the final ACL preservation follow-up; that follow-up passed files tests,
race checks, vet and the backend build.

The browser tests intercept APIs; they prove browser behavior rather than live end-to-end backend
integration. SQLite runs locally; PostgreSQL, MariaDB and Redis use disposable test containers.
SQL Server cleanup uses a fake driver to exercise the real connection pool. No live SQL Server,
Oracle, ClickHouse or MongoDB matrix was completed. Host account/key/cron/nginx/ACME and PM2 mutation
coverage uses focused fixtures or source review; it does not establish every production host setup.
Self-update/restart launch admission was tested without replacing the running dashboard.

Initial checks exposed a backup fixture outside its configured roots, unsafe automatic Docker prune
test admission, a validation-message formatting mistake, a PostgreSQL disappearing-relation edge,
and intermittent isolated tmux startup. Their final dispositions are recorded with the fixes and
final gates. Two new browser fixtures initially lacked a Docker ping mock; the corrected final security
run supersedes those failures. The first failing runs are not represented as passes.

Documentation was reviewed against the complete change, including `AGENTS.md`, `README.md`,
`CONTRIBUTING.md` and affected internal architecture, security, backend, frontend and deployment pages.

## Remaining work and operational limits

1. **Blueprint runtime completion remains outstanding.** This patch prevents unsupported new deployments;
   it does not implement the missing source/build/runtime/secret/schedule integration. Catalog previews
   and management of existing workloads remain available.
2. **Upgrade affected host Docker daemons; update Monaco when its patched bundle becomes available.**
   Scanner findings and applicability are recorded above; the scanners are not clean.
3. **Host-managed certbot timers need the Route 53 credential-file environment.** Dashboard-issued
   renewals pass it automatically. Independent timers must set
   `AWS_SHARED_CREDENTIALS_FILE=/etc/letsencrypt/jd-dns/route53.ini` and `AWS_PROFILE=default`; saved
   dashboard credentials do not change an unrelated system timer's environment.
4. Compose mutation/execution now requires `system.admin`; limited accounts retain read access. This
   closes the unvalidated effective-Compose privilege path.
5. Custom PM2 log locations now require an administrator to include them in `JD_LOG_ROOTS`. Automatic
   trust in account-controlled paths was removed.
6. No test proves historical damage was repaired. Existing overwritten backups, altered files, or
   previously exposed deployment secrets require operator review; the fixes address the identified
   causes. Existing secret-bearing transcripts are not rewritten; review them and rotate exposed
   credentials as needed.
7. Detached self-update/restart launch cancellation has a pre-existing uncertain-outcome edge: Docker
   may have created the sibling before its CLI reports cancellation. Lifecycle admission is serialized,
   but this patch does not add reconciliation of that launch outcome. Live dashboard replacement and
   cancellation recovery were not exercised.
