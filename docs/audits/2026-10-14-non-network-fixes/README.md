# Non-network audit repairs — 2026-10-14

The audit inspected 94 routes and 924 feature entries on baseline `7690fb41` of `patch/0.7.5`.
`/network` and its descendants are excluded. This change handles all 34 findings: 30 confirmed defects,
three potential bugs and one exposed design limitation. Findings are implemented; combined validation
is recorded below before the PR is filed. This matrix records behavior and meaningful regression
coverage, rather than declaring every live host integration certified.

| Finding | Existing feature | Change | Regression coverage |
| --- | --- | --- | --- |
| AW-03 | Remote Desktop session tools and mode switch disappear below 1280px | Expose remote tools through a keyboard-accessible side panel below 1280px and restore trigger focus. | `remote-desktop.spec.ts` |
| AW-05 | Stickyboard duplicates stale content and leaves the editor after a failed save | Require acknowledged current board contents before duplication; keep editor after failed save. | `stickyboard-ui.spec.ts` |
| HT-06 | Git inline editor silently drops unsaved edits when another preview opens | Hold per-checkout/path Git editor drafts and their original revisions in memory; warn before unload and clear on sign-out. | `git-features.spec.ts` |
| HT-14 | System Users incorrectly presents a locked password as disabled SSH access | Label and count password authentication locks accurately, preserving authorized-key sign-in routes and explaining SSH/PAM policy. | `access.test.js; system-ui.spec.ts` |
| AW-01 | API-key dialog retains the previous secret and cannot create the next key after Done | Reset one-time API key and creation fields on every close/reopen. | `account-ui.spec.ts` |
| AW-02 | Password-only sessions falsely report password and code | Record password/TOTP/recovery provenance separately from access elevation; additive legacy unknown migration. | `session_method_test.go; session_method_migration_test.go; account-ui.spec.ts` |
| AW-04 | Add-computer flow displays endless skeleton when overview fails | Show failed computer-overview load with retry in Add computer. | `remote-desktop.spec.ts` |
| AW-06 | Sunshine readiness is described as a live video session before video exists | Distinguish backend Ready from observed video playback; clear playback after document navigation or stalled media. | `remote-desktop.spec.ts` |
| AW-08 | Remote session ignores failed computer inventory and stays blank/connecting | Show remote-session inventory error and retry before connection workspace. | `remote-desktop.spec.ts` |
| AW-09 | Escape on Stickyboard name saves the edit it was meant to cancel | Suppress rename commit on blur caused by Escape. | `stickyboard-ui.spec.ts` |
| AW-11 | Board navigation considers one flush successful even if edited snapshot has advanced | Drain board saves until the latest edited snapshot is acknowledged before navigating; retain dirty conflict unload warnings. | `board-save.test.js; boards-ui.spec.ts; stickyboard-ui.spec.ts` |
| DDP-01 | Suggested native-dump-only backup cannot be created | Allow a filesystem source or a native database dump; verify native-only archive, coverage and native restore. | `database_dumps_test.go; backups-ui.spec.ts` |
| DDP-02 | Database backup deep link clears its input before coverage can arrive | Consume database backup handoff only after resource data opens the suggested editor. | `backups-ui.spec.ts` |
| DDP-03 | Unpicking resource removes manually entered or shared backup inputs | Track picker-owned contributions and preserve manual or shared inputs when unselecting. | `resource-picks.test.js; backups-ui.spec.ts` |
| DDP-06 | Failed backup run history is shown as no runs | Expose failed run-history reads with retry or stale-data warning. | `backups-ui.spec.ts` |
| HT-01 | Unavailable or invalid Docker detail tabs leave an empty detail body | Normalize invalid/unavailable Docker detail views to the first available tab. | `docker-ui.spec.ts; stack-detail.spec.ts; navigation.spec.ts` |
| HT-02 | Container detail state remains stale after changes made outside this page | Poll container identity and route bindings every ten seconds and refresh after mutations while retaining child drafts. | `docker-ui.spec.ts` |
| HT-03 | Images can display a green current verdict when registry checks are unknown | Include matching secondary tags/digests; distinguish unknown checks from verified current status. | `images.test.js; docker-images.spec.ts` |
| HT-05 | Folder uploads bypass nested-name collision review | Plan folder-upload collisions in each actual directory, with explicit existing-folder merge and collision policy. | `upload-plan.test.js; files-ui.spec.ts` |
| HT-07 | Cron previews use browser timezone and can hide that disclosure | Report measured host and positional CRON_TZ metadata, forecast in that zone and disclose browser date display/fallback. | `cron_test.go; cron.test.js; schedule.test.js; processes-ui.spec.ts` |
| HT-09 | Native log exports are silently limited to 20,000 lines | Disclose native export limits, scans, missing sources and default journal window in downloaded coverage metadata; propagate read failures. | `handlers_logs_export_test.go; handlers_logs_stack_test.go; logs-lens.spec.ts` |
| HT-10 | Failed package installs remain marked Started and cannot be retried from result row | Track accepted package jobs independently of console, allow failed/canceled retries and refresh readings at terminal outcomes. | `install-jobs.test.js; packages-ui.spec.ts` |
| HT-11 | Package upgrade confirmation promises constraints not applied by every manager | Describe actual manager-specific dependency/replacement scope and possible service restarts. | `packages-ui.spec.ts` |
| HT-12 | File/config/crontab saves do not protect against concurrent external edits | Add guarded content revisions to Files/Git/Compose/crontab writes; retain conflicted drafts and serialize dashboard saves. | `revision_test.go; revision_shared_test.go; handlers_files_revision_test.go; compose_revision_test.go; cron_test.go; files-ui.spec.ts; git-features.spec.ts; stack-detail.spec.ts; processes-ui.spec.ts` |
| HT-16 | Editing an hourly cron job silently resets its minute to zero | Open nonzero hourly minutes as Custom and retain the original schedule on command-only edits. | `cron.test.js; processes-ui.spec.ts` |
| SH-01 | Expired API session stays inside the authenticated shell without a sign-in recovery path | Revalidate recognized feature authentication failures, redirect confirmed expiry to sign-in, retain identity on transient failures and fence login/logout response races. | `api-auth-recovery.test.js; auth-recovery.spec.ts` |
| AW-07 | Allowlist helper misidentifies which CIDR covers IPv4 loopback | Compute IPv4 CIDR membership for loopback instead of matching the host spelling. | `allowlist.test.js` |
| AW-10 | Empty merged block list can claim no engine blocks anyone despite omitted/unread data | Describe empty returned merged block view without claiming complete engine enforcement. | `security-ui.spec.ts` |
| DDP-04 | Saved-query URL handoff is discarded at full tab capacity | Keep saved-query handoff pending at 24-tab capacity and accept it when a slot opens. | `database-query.spec.ts` |
| DDP-05 | Backup job detail reopens a retained stale edit draft | Clear job editor drafts on explicit close and successful save. | `backups-ui.spec.ts` |
| HT-04 | Canceling a Cut paste clears the clipboard | Clear only successfully moved Cut sources; preserve canceled, skipped or failed sources. | `files-ui.spec.ts` |
| HT-08 | Cron schedule parser accepts malformed trailing syntax and can show invalid previews | Reject malformed field separators/bounds/steps, preserve environment whitespace and native leading-star day semantics. | `cron_test.go; cron.test.js` |
| HT-13 | Export crashes on a valid five-digit-year datetime input | Parse extended HTML years then show RFC3339 range/order errors; disable invalid exports/search and validate server download bounds. | `log-filter.test.js; logs-lens.spec.ts; handlers_logs_export_test.go` |
| HT-15 | Administrator filter/count omits UID 0 accounts and other root routes | Include UID 0 and known sudo/wheel/admin/docker group routes; state direct sudoers rules are not inspected. | `access.test.js; system-ui.spec.ts` |

## Remaining operational limits

- **HT-14:** Authorized keys are a known route; actual SSH acceptance depends on host SSH/PAM/account policy. No live account changes were exercised.
- **AW-06:** Canvas/WebCodecs-only or inaccessible player documents have no observed HTML video playback; their honest status remains Ready.
- **HT-07:** Native daemon CRON_TZ/DST behavior varies. POSIX or unavailable zones cannot be predicted; the page discloses forecast absence or estimates.
- **HT-09:** Native exports remain bounded to 20,000 selected lines/five-minute scans; journal with no start date reads 48 hours unless Current boot is selected. Omitted coverage is marked PARTIAL.
- **HT-12:** Legacy API callers may omit revision. Uncoordinated native writers can race between final comparison and publication; dashboard writes are serialized.
- **HT-15:** Direct sudoers rules and other unknown privilege routes are not parsed.

Host mutations in the regressions use temporary directories, mocked host commands and isolated API
fixtures. Live SSH/account changes, package upgrades, real native database restore and real remote
streams were not exercised against the operator's server. No production stack restart or deployment
is part of this PR. No CI, dependency, release version or `/network` page change is introduced.

## Verification

Combined fresh-build validation pending. The contributor-required `scripts/test-changed.sh patch/0.7.5`
selects only reachable checks and specs; the whole browser suite and `go test ./...` are not run.

## Documentation review

Owning internal documents describe session provenance/recovery, retained drafts, remote playback,
backup inputs/history, package outcomes, native log coverage, password locks and cron revisions/zones.
AGENTS.md, README.md and CONTRIBUTING.md were compared with the complete change: commands,
installation, contribution, release workflow, capabilities and public feature inventory need no change.
