# Actionable findings across the dashboard

The operator asked for the shared findings UI to feel like the rest of the dashboard, to remove
routine advice from attention lists, and to make each offered action lead to the controls that can
address the finding. This change starts from `patch/0.7.1` at `c12fb880`.

## Scope and presentation

The source inventory contains 22 `FindingList` render locations in 17 original consumer files,
reaching 21 page routes. All use the same renderer. These are reading pages (design-system register
A); certificate issuance keeps its own advancing command, with secondary diagnostic actions.

Finding rows show the problem and its measured consequence before the disclosure. Expanded evidence
shows each reading once rather than repeating the same header and body text. The action is a
sibling control, visible on touch screens and without opening the evidence. Titles wrap rather than
truncate. Container queries put actions below the text in narrow panels, including paired panels on
a wide screen. An attention list gives the first offered remedy the command face; diagnostic reports
keep secondary controls. Arriving findings use the existing arrival motion. Nothing claims a polled
finding is a live socket reading. Async controls prevent repeat submission and report failures.

## Content policy

Attention lists retain critical and warning findings. Optional instrumentation, configuration
preferences, intentionally disabled sites and unused resource recommendations remain in
their own settings, inventory or diagnostic readings. Missing observations remain explicitly
unavailable evidence. They are never an all-clear result or a deployment fault.

Certificate expiry no longer claims that approaching expiry proves a stopped timer, or assumes every
issuer uses a ninety-day lifetime. Renewal advice asks the operator to inspect the schedule and runs.

The database backup wording says what is known: no dump is recorded by this dashboard; external
backups are not assessed. MongoDB cache occupancy below the configured capacity is no longer presented
as proof that every operation is slow. Reaching capacity remains critical and opens current operations;
the other cache readings remain on Performance. WiredTiger documents both its configurable eviction
trigger and [stalling at capacity](https://source.wiredtiger.com/develop/tune_cache.html).
Operator-requested diagnostic reports retain measured details and external guidance where the
website cannot perform a remedy.

## Page and action inventory

| Pages | Findings and checked destination contract |
| --- | --- |
| `/databases/[id]` | SQL advisor priorities; Redis and MongoDB observed priorities. Backups stays in this connection, a statement is handed to Query for review, object findings open Schema, replication and memory open their supported performance views, client and memory limits select their parameter. Protected connections and viewers inspect instead of receiving a write action. |
| `/databases/[id]/advisor` | Same eligibility and destination rules as Home. Severity/category counts describe the retained list. Statements and applying a fix remain behind explicit review. A linked finding opens its disclosure. |
| `/deploy` | Failed run, failed live readiness check and excessive request failures. The request action selects Requests on Logs. |
| `/deploy/[id]` | Current deployment faults; pending drafts remain the project's own pending state. Runtime absence opens this project's Runtime. Route absence opens the project's Domains settings; missing and expiring certificates open issuance and renewal on Certificates. Missing jobs open Backups instead of a deleted job. Unavailable observations are a separate disclosure. |
| `/docker`, `/docker/containers`, `/docker/containers/[id]` | Priority risks and failures. Grouped findings expose their containers and each one's measured evidence and remedy. Recommendations remain in Configure, where restart policy and log rotation retain their existing reviewed remedies. Named configuration actions use the existing reviewed controls; viewers inspect. Restart policy, log rotation, reclaim and Compose ownership retain their confirmations and capability gates. |
| `/network` | Priority observations. Labels name the destination. Boot restoration points to the actual `just-dashboard-network.service` control; routing, protection and interfaces use their owner pages. |
| `/network/firewall`, `/network/connections` | Relevant security priorities, with enforcement review through the existing firewall controls or listener owner inspection. |
| `/network/tools`, `/network/runs` | Quick and saved probe findings share visible navigation to their actual owner. Unknown/external owners receive guidance instead of an invented dashboard fix. |
| `/security`, `/security/ssh`, `/security/intrusion` | Observed security risks, supported fixes only. Allowlist review points to Access in Dashboard configuration. An account that cannot change the allowlist is told an administrator must do it. SSH actions focus their named directive and use the full settings access preview, test/apply confirmation and recovery flow. Viewers receive administrator guidance. |
| `/proxy` | Priority proxy findings and snoozes; permitted renewal remedies are inline and report progress. Grouped error evidence opens the upstream's listening port, site or nginx configuration. |
| `/proxy/certificates` | Priority certificate hygiene and issuance failures. Issuance links prefill the matching DNS/HTTP tool; lineage checks focus the form's names field. Issuance/removal controls respect their actual capabilities. |
| `/proxy/tls` | Scan and deep-scan evidence. Existing reviewed remedies remain; reload cannot submit twice and owner actions require their real capability. |
| `/proxy/config` | The linter's measured source location still opens that file and line. |
| `/proxy/traffic` | Actual error groups, their samples and counts; routine log notices are excluded. Owner actions use the site or filtered listening port. |
| `/backups` | One failure per job, excluding intentionally paused schedules. Failed runs open their history; overdue jobs can run for a permitted role, and an already running job offers its history instead of submitting another run. |

The API's diagnosis records, capability enforcement, audit, destructive confirmations, protected
connection guard and database statements are not changed.

## Verification

The production build, changed-file formatting and lint, TypeScript, and all 3,691 unit tests pass after integration with `patch/0.7.1` at `076d9e0a`.
`scripts/test-changed.sh patch/0.7.1` selected 1,799 browser cases: 1,704 passed, 63 were skipped,
and 32 initially failed. After removing duplicated evidence and correcting selectors for visible
priority actions, all 32 failed cases passed in focused reruns against a fresh production build.
Multi-page responsive checks have a timeout that covers their complete route iteration; the
MongoDB keyboard check waits for focus to settle.

An additional 21 targeted browser contracts passed against the corrected renderer. They cover
consequences, actions, wrapping and severity marks at 390, 1280 and 1720 pixels; database Backups;
Query with the complete statement without execution; protected/viewer inspection; advisor filtering
and reviewed fixes; Compose ownership; and restart recommendations remaining actionable in Configure.
The SSH directive-focus and dashboard allowlist-anchor checks also passed.

Both live Docker advisor acceptance tests passed with temporary, isolated fixtures:
`JD_ADVISOR_DOCKER_LIVE=1 go test ./internal/dockerx -run '^TestLiveAdvisor' -count=1 -v`.
The restart-policy test preserved the running container's PID and start time. The reviewed log
remedy preserved image identity, environment and network ownership while replacing only its fixture.

Final integration against `patch/0.7.1` at `076d9e0a` passed changed-file Prettier and ESLint,
`tsc --noEmit`, all 3,691 unit tests and the production build. Fourteen browser cases passed against
that fresh build: findings at phone and desktop widths, actual database actions and permission
fallbacks, security evidence and confirmation, the allowlist anchor, Configuration editing, and
compatibility with the release branch's uninstall controls.

The pre-push documentation review updated the design system, shell, feature map, terminal/features
and Docker findings documentation. `README.md`, `AGENTS.md`, `CONTRIBUTING.md` and other backend or
deployment documentation need no change: installation, backend contracts, security invariants and
contributor workflow are unchanged.

Screenshots use mocked API data. The attention inputs include two priority findings and two routine
notices; the two notices are absent in the resulting attention list. The before capture has sparse
Home readings; the after captures use the populated Home fixture. The relevant comparison is the
attention rows and their controls.

| Evidence | Artifact |
| --- | --- |
| Before, database Home at 1280 | [Screenshot](database-before-1280.png) |
| After, database Home at 1280 / 1720 / 390 | [1280](database-after-1280.png), [1720](database-after-1720.png), [390](database-after-390.png) |
| Attention rows at 1280 / 1720 / 390 | [1280](attention-after-1280.png), [1720](attention-after-1720.png), [390](attention-after-390.png) |
| Docker priority findings at 1280 / 1720 | [1280](docker-after-1280.png), [1720](docker-after-1720.png) |
| Deployment findings at 1280 / 1720 | [1280](deployment-after-1280.png), [1720](deployment-after-1720.png) |
| Proxy priority findings at 1280 / 1720 | [1280](proxy-after-1280.png), [1720](proxy-after-1720.png) |
| Review a statement, return, open this database's Backups | [Interaction recording](finding-actions.webm) |

Browser checks mock server responses. They verify the offered controls and requests; they do not
perform mutations on a live operator's server.
