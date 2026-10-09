# Native profile editor acceptance

This record covers the administrator's existing-profile L3 editor and the persistent native cleanup
notice. It does not complete P11's structural bond/VRF transactions, actual DHCP/SLAAC acquisition,
or cold runtime reconstruction. Those require the native backend's separate ownership and live
acceptance evidence.

## Source and isolated environment

The UI worktree is `/home/ubuntu/Just-Dashboard-network-native-profile-ui`, branch
`implement/network-native-profile-ui-acceptance`, based on `cf85bd67b6aa0475166e58aa50e93067a2077621`.
It includes the native UI draft `3018e120943d3b2db2362c1f86354d7bdcc61afd` and the local browser-worker
override `f6864816`. No production network manager or interface was mutated by these browser tests.
The fixture mocks HTTP API replies; it cannot establish native owner or recovery correctness.

The final production build passed on 2026-10-08, with compilation in 10.1 seconds, TypeScript in
9.1 seconds and all 73 static pages generated. Build ID: `PoDLe7pCYIXKxCuCyUc8E`. The owned server
bound only `127.0.0.1:43138`. The source hashes at build and acceptance were:

| File | SHA-256 |
| --- | --- |
| `src/components/network/interfaces/native-profile.tsx` | `c08a14d4b902585bd4d174f97df786bf84bf18651fe2a1030aa15f82e981e01e` |
| `src/lib/network-native-profile.ts` | `1647bf6dc968d3f8fdc3764343b26fe971ad65fc9d40bfb1f7787e5a9bcd8cb9` |

All build and test temporary files used the owned workspace, because shared
`/tmp` had little free space. Browser runs used one worker.

## Contract and interactions

The leaf parser rejects wrong-device responses, malformed rendered metadata and lifecycle evidence,
invalid family methods/preferences, non-string collection items and malformed or out-of-range route
entries. Editable views must contain supported owner/renderer/profile/generation and matching
configured, runtime and boot evidence. A read-only refused view may still expose its supported
intent and drift/unknown evidence. Backend literal and transaction validation remains authoritative.

An invalid later poll retains the last successful read and every typed draft field, exposes a dated
read failure, and blocks apply. A changed generation requires an explicit reviewed rebase that
changes only the generation. A changed owner/profile cannot be rebased into a different profile.
An already-open confirmation checks the latest rendered draft, ownership, generation and read state
before sending any write. A stale confirmation sends zero writes and retains the draft.

The 14 focused browser cases passed in 45.8 seconds against that final build. They cover:

- Exact typed native intent and mandatory pending apply with the ordinary preference disabled.
- Retained fields after HTTP 409/500 writes, a later HTTP 500 read and a later wrong-device reply.
- Explicit generation-only rebase; initial wrong-device refusal before draft creation.
- New IPv4/IPv6 VRF routes inheriting table 1001, including a 390-pixel phone viewport.
- Read accounts opening a device without requesting the administrator-only native profile.
- A confirmed decision retaining its cleanup warning outside Network, with a failed explicit retry
  followed by a successful retry; incomplete cleanup cannot be dismissed.
- A malformed `routes: [null]` poll becoming a retained read failure rather than crashing the editor.
- Already-open confirmations refusing a changed generation, owner or failed read with zero writes.

The editable VRF reply is an HTTP fixture for the table-inheritance interaction. It does not show
that the native backend admits VRF writes. Native structural ownership remains refused until its
saved, loaded, applied and kernel contracts have independent verification.

The leaf parser's 43 pure tests passed with 44 assertions. The final required gate passed on
2026-10-09: Prettier, ESLint, TypeScript, all 3,173 Bun tests with 13,857 assertions, and 132 selected
browser cases with 14 optional visual skips in 6.3 minutes. It ran against the same frozen build:

```sh
JD_BROWSER_BASE_URL=http://127.0.0.1:43138 JD_BROWSER_WORKERS=1 \
  TMPDIR="$PWD/frontend/out/native-profile-checks" scripts/test-changed.sh a1a98a10
```

The selected specs were `design-system`, `network-audit`, `network-native-profile`, `network-tunnels`
and `network-ui`. No whole browser suite or whole Go test tree was run. Raw results are preserved in
`native-profile-ui-changed-checks.txt`; the final build, focused browser and parser outputs have
matching `native-profile-ui-*` files beside this record.
Copied terminal logs normalize carriage returns and trailing whitespace; the original workspace
logs remain preserved. Test results and failures are unchanged.

## Preserved earlier results and visual check

The earlier two-guard source (requested-device identity and VRF table default) passed the required
changed-file gate: 3,130 Bun tests, 128 browser cases and 14 optional screenshot skips in 8.8 minutes.
The first focused run had nine passes and one assertion failure: cleanup had correctly removed the
banner outside Network, but a negated text assertion targeted the now-absent banner. Changing that
test-only assertion to check a zero element count passed its focused retry, followed by the complete
required gate. The original failure and retry output are preserved.

The final build's 1720- and 390-pixel screenshots were inspected. The native owner, separate
configured/runtime/boot evidence, VRF table and draft fields fit the sheet; phone content scrolls
vertically. The browser phone case additionally checks the sheet has no horizontal overflow.

Frontend-only server logs include WebSocket proxy connection refusals to absent `127.0.0.1:8080`
for the system stream. The fixtures mock HTTP routes and do not start the Go server. These messages
are separate from the original execution sandbox's loopback initialization failure.

Documentation review covered the complete draft and follow-up diff against `docs/internal/`,
`AGENTS.md`, `README.md` and `CONTRIBUTING.md`. The frontend feature map and data contract were updated.
The worker override's AGENTS/CONTRIBUTING changes were already reviewed in `f6864816`. This UI
follow-up adds no dependency, license, release, deployment or backend authorization change; no
additional README or CONTRIBUTING update is needed. The backend native-manager guide is a separate
required integration dependency.
