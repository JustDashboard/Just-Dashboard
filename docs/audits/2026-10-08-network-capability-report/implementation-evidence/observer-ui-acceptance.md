# Kernel observer controls and separate measurements

This frontend slice uses `/network/flows`' reading register and the existing plain panels,
status dots, statistic grid, table and quiet disclosure. Native TCP counter deltas and kernel
TCP/UDP transport-payload observations have separate displayed subtotals, even for the same socket.
Unknown evidence discriminators are not interpreted as either measurement channel. Exact decimal
counters use `BigInt`; missing bytes and gap counts remain unknown.

Observer activation and stop require an ordinary reviewed confirmation, a fresh administrator
reading and destructive capability. History recording is a prerequisite for activation. Opening
the page, enabling ordinary history and refreshing do not activate it. Retained attachments after
a failed stop remain visible with an explicit retry; ordinary history stop is disabled until the
observer is stopped. A read failure while an activation review is already open blocks submission.
Successful requests refresh actual state without a blanket completed notification.

The report shows per-direction known-length/observed packet counts, byte gaps, observed flags,
uncertain UTC, Docker attribution age/omissions, bounded collection and retained quality.
Delivery drops and omissions never become end-to-end packet loss or complete bandwidth claims.
Interrupted sessions retain dated program/link IDs without treating them as live attachment proof.

## Source-matched checks

The source was frozen in `implement/network-kernel-observer-ui`, based on `0958c6e9`.
`bun run build` passed, including TypeScript, and its production server ran on loopback port 43136.
[Build output](observer-ui-build.txt) records this result.

The focused flow spec passed all 16 cases in 38.3 seconds; see
[focused output](observer-ui-focused-browser.txt). The cases cover legacy history, private-read
gates, independent reviewed activation, history and destructive-capability prerequisites, failed
stop/retry, failed refresh during an open review, identical native/kernel TCP channels, UDP,
clock/byte gaps, interrupted sessions and viewport containment.

`scripts/test-changed.sh 0958c6e9` passed Prettier, ESLint, TypeScript and all 3,130 Bun tests.
Its selected 83 browser cases produced 77 passes and six timeout failures under concurrent host
load. Three were design-system walks of unrelated pages; three were previously passing flow cases.
The original invocation exited 1 and is retained in [changed-check output](observer-ui-changed-checks.txt).
The exact failed locations were rerun against the unchanged build with `--workers=1`; all seven
selected cases passed in 2.7 minutes. The database location selects both desktop and phone variants.
Thus every one of the 83 selected executable cases passed, with exact retries for the six initial
failures. No source or assertion was changed for these retries; see
[retry output](observer-ui-exact-retry.txt). No zero exit is claimed for the original invocation.

```sh
JD_BROWSER_BASE_URL=http://127.0.0.1:43136 bunx playwright test \
  tests/browser/design-system.spec.ts:433 tests/browser/design-system.spec.ts:622 \
  tests/browser/design-system.spec.ts:643 tests/browser/network-flows.spec.ts:134 \
  tests/browser/network-flows.spec.ts:261 tests/browser/network-flows.spec.ts:286 --workers=1
```

## Visual inspection and boundaries

The observer overview and expanded quality were inspected at
[390 px](kernel-observer-quality-390.png), [1280 px](kernel-observer-quality-1280.png) and
[1720 px](kernel-observer-quality-1720.png). Long cgroup identities and decimal counters remain
contained; the phone layout stacks controls and quality, and desktop readings use the available
width. The ordinary overview captures are [390 px](kernel-observer-390.png),
[1280 px](kernel-observer-1280.png) and [1720 px](kernel-observer-1720.png).

Browser fixtures establish rendered behavior and submitted requests, using simulated private API
responses. They do not establish kernel attachment, coverage, attribution or overhead. Those have
separate [native observer, Docker and process-death evidence](observer-kernel-acceptance.md).
Combined production-build
acceptance and the broader P8 platform/performance boundaries remain open.
