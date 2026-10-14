# Current non-network audit repairs — 2026-10-14

This follow-up addresses the eight current defects and one concurrent-edit risk found when the
non-network inventory was rechecked at `a3a8e49e` on `patch/0.7.5`. The inventory contains 94 route
patterns, 911 page feature entries and 13 shared features. The 19 `/network` route patterns are
excluded; Docker Networks and Proxy remain in scope. The earlier 34 repairs are already present in
the target branch and are recorded in [the earlier repair matrix](../2026-10-14-non-network-fixes/README.md).

The task branch includes target `04f7f7af`, which already contains the T-01 auth-recovery test timing
fix from PR #227. That test waits for the reload's session probe before asserting its count; it passed
three focused repetitions during this follow-up. The task branch also integrates target `965fc96a`
(PR #228's account-page design); required-factor readings follow that design without restoring its
older tiles.

Cross-review extended the same fixes to delayed image encoding/upload after account cleanup,
temporary-upload cleanup after a native parent-directory replacement, and raw nginx validation
rollback after a concurrent native edit. The image conflict recovery flow also avoids a persistent
toast covering Save as; its inline refusal already explains the retained draft. Proxy password-file
exclusions also follow the protected directory through relative roots and internal aliases, retaining
the read-only account boundary when client paths are resolved. Session recovery that adopts a
different account now remounts the authenticated workspace, removing the previous account's mounted
editor buffers. Same-account recovery retains them. Raw proxy actions also check draft ownership
before validation or publication during the cleanup-to-remount interval.

| Finding | Resulting behavior | Regression evidence |
| --- | --- | --- |
| N-01 | Drawing and Stickyboard hold unsaved snapshots and their original revisions in memory across sidebar, palette and browser-history navigation. A prior in-flight acknowledgement cannot erase a newer draft. | Board draft logic tests, `boards-ui.spec.ts`, `stickyboard-ui.spec.ts` |
| N-02 | Raw proxy configuration reads return a revision. Saves require that revision, reject stale content and retain the operator's buffer for review against the current file. Native changes during validation are preserved. | Proxy service/API guarded-write tests, `proxy-engine-overview.spec.ts` |
| N-03 | The image canvas loads one immutable binary snapshot with its revision. Overwriting requires that original revision; a conflict retains the transformed canvas for Save as or explicit reload. | Binary snapshot/upload tests, `files-ui.spec.ts` |
| N-04 | Streamed exports, raw downloads and XHR uploads use the shared authentication-refusal observer while retaining streaming, upload progress and cancellation. | Response/XHR logic tests, database export and auth-recovery browser cases |
| N-05 | The Security overview reports only measured fail2ban state. An absent, stopped or unreadable fail2ban no longer implies that every repeated-failure blocker is absent. | `security-ui.spec.ts`; before/after captures below |
| N-06 | User identity and coverage readings, cards, filters, factor-reset guidance and Profile/Security diagrams follow the required-2FA policy. Missing enrollment under that policy is described as required before completing sign-in; the diagram has no password-only bypass. | `account-ui.spec.ts`; before/after captures below |
| N-07 | Raw proxy drafts survive same-document Back/Forward as well as shell navigation, with the original revision preserved across navigation and advanced only by save or explicit conflict review. | Draft logic tests, `proxy-engine-overview.spec.ts` |
| N-08 | Backup metadata, sealed destination credentials and the revision publish in one database statement. A rejected publication leaves the previous job and credentials intact; credentials cannot publish separately from settings. | Fault-injection and cancellation tests in `internal/backups` |
| P-01 | The backup form saves against its captured job revision and retains conflicting fields and credentials for explicit review. Pause/resume advances that revision. Deployment native-dump additions use a narrow, bounded compare-and-retry operation rather than overwriting an entire stale job. | Backup/store/API concurrency and migration tests, `backups-ui.spec.ts`, deployment settings regressions |

Drafts are session working state, held only in memory and cleared by explicit discard, deletion or
account working-state cleanup. They do not survive a browser reload or a closed tab. An unload
warning remains while protected editor drafts exist. Native tools do not participate in the dashboard's
writer locks; the final revision checks narrow their race window without claiming an atomic native
filesystem compare-and-swap. Backup revision checks remain optional for legacy API clients; the
dashboard form always supplies its captured positive revision.

As with other HTTP writes, losing the response or a post-publication read can leave the caller unsure
whether a save completed. Backup atomicity covers the publication itself: credentials and metadata
always change together. The captured revision makes a retry review the current job rather than
silently overwriting a completed publication.

## Operational limits from the audit

The twelve limit entries are retained as limits; they are not additional repaired product defects:

- **L-01:** The broad journal can contain authentication records despite narrower named-source gates.
  Its existing documented permission policy remains unchanged.
- **L-02:** Archive coverage/checksums do not prove application recovery. Native dumps, SQLite
  snapshots and an application-specific drill establish only their measured scope.
- **L-03:** Archive browsing fetches at most 5,000 entries and renders at most 1,000 matches; a filter
  cannot find entries outside the fetched subset.
- **L-04:** Native log exports remain capped and windowed, with downloaded partial-coverage metadata.
- **L-05:** Query exports rerun the statement. Concurrent rows or volatile expressions can differ
  from the previously displayed result.
- **L-06:** Database tools and provisioning remain engine-dependent; the capability catalogue is
  authoritative for which operations a connection supports.
- **L-07:** Cron forecasts depend on measured host timezone and daemon semantics; previews are
  estimates where native behavior cannot be verified.
- **L-08:** Password locks, known groups and authorized keys do not exhaust SSH/PAM/sudo policy.
- **L-09:** Remote readiness does not prove credentials, media playback or every browser capability.
- **L-10:** Unsupported native syntax remains restricted to its documented editing paths.
- **L-11:** Required-factor recovery still requires host access and account-scoped enrollment state.
- **L-12:** Fixture success does not certify every native integration on an operator's server.

These repairs do not change journal authorization, introduce broader database recovery, remove
export bounds or certify real package upgrades, SSH/account changes, production deployments,
certificates, reinstalls/uninstalls or remote media sessions. Applicable disposable native acceptance
and its precise result are recorded below.

## Visual and interaction evidence

The captures use deterministic API fixtures with a production frontend. Before captures reflect the
original audit baseline; after captures reflect the final repaired tree, including the subsequently
merged account-page design. They establish displayed policy and editor behavior, rather than live
host enforcement.

- Required-factor Users: [before](account-required-before.png), [after](account-required-after.png).
- Fail2ban-specific Security reading: [before](security-fail2ban-before.png),
  [after](security-fail2ban-after.png).
- Recordings: [drawing navigation](drawing-draft-navigation.webm),
  [Stickyboard navigation](sticky-draft-navigation.webm),
  [backup conflict review](backup-conflict-review.webm),
  [streamed export session recovery](streamed-export-auth-recovery.webm),
  [proxy history recovery](proxy-draft-history.webm),
  [proxy conflict review](proxy-conflict-review.webm),
  [image conflict recovery](image-conflict-recovery.webm),
  [different-account recovery](different-account-recovery.webm),
  [same-account recovery](same-account-recovery.webm),
  [raw editor account cleanup](raw-editor-account-cleanup.webm).

## Verification

The final `bun run build` passed after integrating all frontend repairs. The server used for the
browser evidence was restarted from that build on loopback port 43227.

- `go test -race ./internal/deploy ./internal/api ./internal/proxysvc ./internal/backups ./internal/store -count=1`
  passed on the integrated repair tree. The later pinned native-file corrections also receive focused
  race checks and the final changed-file gate.
- `JD_DEPLOY_LIVE=1 go test ./internal/api -run '^TestLiveSQLiteApplicationRecoveryVerification$' -count=1 -v`
  passed: a real disposable application restored its 8,192-byte SQLite artifact through its own
  connection, recovered the canary and rejected an incompatible schema; fixture resources were removed.
- `JD_DEPLOY_LIVE=1 go test ./internal/api -run '^TestLiveBackupDumpsAndRestoresAPostgresDatabase$' -count=1 -v`
  passed: a disposable PostgreSQL database produced a 2,588-byte native dump and restored its canary
  into a separate drill database. This extends the original audit's native acceptance evidence only
  for that fixture and the SQLite recovery fixture above.
- The broad `scripts/test-changed.sh origin/patch/0.7.5` run scheduled 1,385 browser cases:
  1,358 passed, 25 skipped and two failed. The two backup failures exposed stale revision fixtures and
  a missing navigation wait; both specs then passed all 25 cases with
  `scripts/test-changed.sh fcf1b123`, and the two corrected cases passed three focused repeats each.
  The original broad run was not green; the corrections and subsequent results are recorded separately.
- The broad Go selection exposed one missing-file diagnostic regression. After preserving that
  diagnostic, the same six-package selection was replayed successfully: API, contentguard, backups,
  proxysvc, store and files. Go build and vet passed. The final proxy package, focused proxy/password
  races and pinned-upload ancestor races also passed.
- Prettier, ESLint and TypeScript passed, as did `bun test src`: 3,606 passes, zero failures.
- The 25 browser skips are 24 optional Security screenshot cases and one existing Redis unsigned
  cursor `fixme`. Active Redis cursor coverage remains in its owning browser suite.
- The rebuilt account-boundary regressions passed all three cases: same-account draft retention,
  different-account remount and refusal to validate/save after working-state cleanup. The subsequent
  `scripts/test-changed.sh 1555c049` gate passed all 145 browser cases, along with static checks
  and all 3,606 Bun tests, before integrating the account-page redesign.
- The final account-design integration passed Prettier, ESLint, TypeScript and all 3,616 Bun tests;
  all 146 selected browser cases passed with `scripts/test-changed.sh b228d74b`, including required/
  optional policy, diagrams, authentication and navigation. The final enabled-only coverage caption
  also passed a direct disabled-account check on the final production build.
- `scripts/test-changed.sh 329c3cdc` then passed 145 browser cases and failed one palette touch-target
  assertion: Chromium reported 43.9999847412 pixels for a settled 44-pixel row. The assertion now
  rounds to 0.001 CSS-pixel precision, retaining the 44-pixel requirement and still refusing 43.99.
  `scripts/test-changed.sh 7e5e6b68` passed all 15 palette cases, static checks and all 3,616 Bun tests;
  the originally failing case also passed three focused repetitions. The original failed run is
  retained separately from these passing corrections.
- The final documentation review checked 154 local links with no missing destinations and parsed
  changed JSON successfully. `git diff --check` passed. The target remained `965fc96a` before push.

## Documentation review

The owning board, file, proxy, backup, deployment, authentication and observability documents are
updated in this change. The pre-push review compares the complete diff with `docs/internal/`,
`AGENTS.md`, `README.md` and `CONTRIBUTING.md`. [The pre-push review](pre-push-review.md) records
those updates, the unchanged contributor/installation contracts and the preserved security boundaries.
