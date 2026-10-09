# Reviewed custom-domain filter UI acceptance, 2026-10-09

The DNS engine sheet now mounts reviewed custom-domain additions and removals for AdGuard Home
and Pi-hole. AdGuard matches the named domain and its subdomains; Pi-hole matches an exact domain
and additions require explicit existing native group memberships. The matching production build,
reachable local checks and both actual owned engine runs pass. This establishes configured native
policy, selected readback, preservation and restart persistence. Effective client filtering,
precedence, subscription controls and complete P17/C074 acceptance remain open.

## Frozen application and retained records

The task starts at pushed `dc180843`. The assembled product freezes at
`b7038998c668fc915404b51a0b2236e68fe148fa`. A browser-only modal locator correction produces final
source `988207ea4d8559a11ac25a51a14179c703e1b9e4`; all frontend product, backend and module bytes
remain identical to `b7038998`. The final application is rebuilt on that clean source, in 16.755s,
with build ID `uCHVu0igB8u374T6N93rv`, served on `127.0.0.1:43151`.

The [final source manifest](dns-service-domain-filter-ui/source-final.json) records all 1,536
frontend source/package/lock files, both browser fixture/spec files, 1,863 backend Go files and both
module files. The [build record](dns-service-domain-filter-ui/build-final.result.json),
[results](dns-service-domain-filter-ui/results.json) and
[independent native comparison](dns-service-domain-filter-ui/native-root-verification.json)
bind the mounted application to the exact backend bytes compiled and exercised in the separate
clean proof worktree. That proof retains `b7038998` attribution; the earlier isolated `f3fe06aa`
backend tests and engine runs remain separately attributed in the
[owning native record](../../../internal/backend/evidence/dns-service-domain-filters-2026-10-09.md).

The [normalization manifest](dns-service-domain-filter-ui/log-copy-normalization.json) records
original/text/gzip hashes. Text copies remove only carriage returns and trailing line/EOF whitespace;
deterministic gzip copies decompress to the original bytes. Workspace originals and browser traces
remain under `/home/ubuntu/jd-network-validation-tmp`.

## Reachable local validation

The [required gate](dns-service-domain-filter-ui/required-final.log),
`scripts/test-changed.sh dc18084316313a978873b499ba8f80f7623814b5`, exits zero in 588.175s.
Formatting, lint, TypeScript, 3,267 Bun tests / 14,735 assertions and selected Go build/vet pass.
The selected API and DNS packages pass in 0.462s and 32.942s. Browser selection covers 213 cases:
205 pass and eight optional `JD_NETWORK_SHOTS` captures remain skipped and unverified. This is the
changed-surface gate; no whole browser suite or `go test ./...` is run.

All 24 custom-domain interactions pass, alongside the 45 existing DNS service cases and 18 filter
metadata cases. The new cases cover AdGuard allow/deny suffix semantics, Pi-hole explicit group
zero and empty memberships, removal without replacement groups, native null/empty/text comments,
retained refused drafts, unknown group holds, read-only permissions and unsupported Technitium
actions. A configured missing selected group remains visible until explicitly removed; the domain
and disposition survive, staging remains blocked and focus returns to the membership fieldset.
Unknown inventory retains the selection without claiming the group disappeared.

Open confirmations refuse selected metadata drift despite reused fingerprints, changed retained
metadata, missing selection and lost current reads. No apply request is sent in those cases.
Uncertain native readback distinguishes duplicate exact rules and another target policy from
absence, exposing native owner/exact/inventory counts. Verified reviews remove the consumed Apply
control. Configured rule evidence remains separate from runtime and measured client decisions.

Six engine/width cases capture forms and retained reviews at 390, 1280 and 1720 pixels. They wait
for the actual sheet rectangle to settle within the viewport and require internal horizontal
overflow of at most one pixel. The [12 captures](dns-service-domain-filter-ui/screenshots.json)
include six visually inspected examples: both phone forms and reviews and both wide reviews.
Long fingerprints wrap; vertical scrolling remains available. Examples are the
[Pi-hole phone form](dns-service-domain-filter-ui/domain-filter-form-pihole-390.png),
[AdGuard phone review](dns-service-domain-filter-ui/domain-filter-review-adguard-390.png) and
[wide Pi-hole review](dns-service-domain-filter-ui/domain-filter-review-pihole-1720.png).
These are controlled browser fixtures, distinct from actual native engine evidence.

## Actual assembled native engine validation

Both runs use the same clean `b7038998` source and Go 1.26.8 race/CGO binary
`1874158aaf8b7a852573d214325f1c568919ae386c04e74fc745a73d9669a178`.
The compiler and every engine manifest retain all 1,863 Go hashes, both module hashes, exact argv,
environment and host baseline. Independent comparison matches every input against the mounted
`988207ea` tree. The [assembled native records](../../../internal/backend/evidence/dns-service-domain-filters-2026-10-09.md)
retain the raw logs, results, compiler, reviewed wrappers and original preflight failure.

| Engine / authenticated version | Wrapper / test child | Selected current reads |
| --- | --- | --- |
| AdGuard Home `v0.107.71` | 70.471s / 69.44s | 16, including eight custom-domain reads |
| Pi-hole FTL `v6.7.1` | 91.545s / 90.49s | 20, including eight custom-domain reads |

Both exit zero without skips or image pulls. They review/apply allow and deny policy, reject
duplicate staging and consumed replay, inspect exact native selection before/after effects,
restart the owned engine and remove the reviewed rules. AdGuard restores its original ordered
comments, blank-string entry and unrelated rule fingerprint. Pi-hole verifies allow membership
`[0]`, deny membership `[]` and restoration of the original row/comment/group/source fingerprints.
The existing override/client, real UDP/TCP local-answer, credential/config restart and cold
interrupted-predecessor no-replay checks also pass. Local answers are not new filter decisions;
the fixture deliberately keeps protection disabled.

Each final result records zero owned test processes, containers, networks, volumes and temporary
entries, with its task directory removed. Resolver SHA256 remains `9b5b6ef9…f0ff5a02`, production
networkd remains PID 883/start 452 with its recorded UID/GID 998 identity, and host NetworkManager
paths remain absent. No host manager/configuration change, independent timer, VM or host reboot
acceptance is claimed by these DNS runs.

## Preserved failed preparation and correction

The [first browser run](dns-service-domain-filter-ui/browser-first.log) on rebuilt `b7038998`
passes 20 cases and fails four in 80.376s. Its new role locator omitted `includeHidden` while the
confirmation dialog hides background controls from accessibility queries. The existing policy
helper already includes that option. A browser-only correction follows it; the
[four-case retest](dns-service-domain-filter-ui/browser-confirmation-corrected.log) passes in
10.415s on identical product bytes. The fresh final build and complete gate above include the
corrected spec. Original logs/results/traces and the prior builds remain preserved.

The original native wrapper fails preflight before spawning the race binary because the task
account cannot dereference `/proc/883/exe`; it creates no native log, task directory or resource.
The separately named corrected wrapper records the readable exact PID/start, comm/name and all
UID/GID fields instead, refusing any unreadable or changed identity. Its linked receipt preserves
the original compiler and wrapper hashes and reuses the unchanged compiled binary without a new
compilation claim. Both actual passes above use that reviewed correction. The earlier isolated
backend fixture failure remains in its own native record.

Private administrator/no-store routes, sealed literal-origin/TLS authentication, generation and
expiry checks, complete raw native baselines, destructive audit and single-use claims remain
enforced. Native sequential APIs have no atomic compare-and-swap; uncertainty stays `needs_review`
without replay or restoration of foreign policy. No subscription/regex/DSL, Technitium manual/app
mutation, new group, detector-linked handoff or broader query/zone/view/client/DHCP coverage is
established here. All 187 ledger requirements and their existing statuses remain intact.
