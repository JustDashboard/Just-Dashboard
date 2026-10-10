# P12 owner-draft reservation lifecycle acceptance

The Docker and WireGuard owner forms consume each deep-link reservation identity once per retained
draft. Explicit edits detach that identity; delayed, missing, held or failed inventory reads cannot
reapply it or gate a different unreserved prefix. A selected reservation still requires a fresh valid
inventory before handoff. A new deep-link identity can initialize the same retained Docker draft.
Closing and reopening its form preserves the draft and the detachment decision. Delayed admin
authentication makes no private IPAM read until the capability is available.

The shared inventory reader rejects incomplete envelopes, malformed rows and unsupported
discriminants before they reach a picker. Failed refreshes retain dated data and the user's draft,
and block selected-reservation submission. Native owner admission and protection against borrowing
a held prefix remain enforced by the backend; no owner resource is released or deleted by this fix.

Validation used the isolated `fix/network-reservation-lifecycle` tree based on `0958c6e9`.

- `bun test src/lib/network-ipam.test.js`: **5 passed, 32 assertions**.
- Changed-file Prettier, ESLint and `tsc --noEmit`: **passed**.
- `bun run build`: **passed**, then the source-matched production server bound only
  `127.0.0.1:43135`. A first build failed because Turbopack refuses a dependency symlink outside its
  filesystem root; replacing that owned symlink with `bun install --frozen-lockfile` resolved it.
- `JD_BROWSER_BASE_URL=http://127.0.0.1:43135 scripts/test-changed.sh 0958c6e9`: the first attempt
  reported browser-test formatting; formatting was corrected before the next attempt. The next
  attempt passed static checks and **3,129 Bun tests**, then reported **256 browser passes,
  28 optional screenshot skips and 6 failures** in the required selected specs. **All 20 IPAM browser
  cases passed**, including all newly added lifecycle cases.
- The six failed cases were rerun explicitly against the same frozen production source with
  `--workers=1`: **6 passed in 1.0 minutes**. They cover the database switcher at both widths,
  centered audit rows, volume contents, container Back/focus restoration and read-only network
  pages. Their first failures are retained in the validation log; no source or assertions were
  changed to obtain the retry passes.

Exact browser retry:

```bash
cd frontend
JD_BROWSER_BASE_URL=http://127.0.0.1:43135 bunx playwright test \
  tests/browser/design-system.spec.ts:461 tests/browser/design-system.spec.ts:513 \
  tests/browser/docker-ui.spec.ts:1631 tests/browser/docker-ui.spec.ts:2006 \
  tests/browser/network-audit.spec.ts:13 --workers=1
```

Full logs are retained under `/home/ubuntu/jd-network-validation-tmp/reservation-lifecycle/`:
`frontend-build.log`, `changed-checks-formatting-failure.log`, `changed-checks.log` and
`targeted-browser-retry.log`. The fresh integrated `scripts/test-changed.sh` gate against the prior
PR head is still required before pushing. These form fixtures do not establish all P12 host-owner
or reboot acceptance; P12 remains in progress in the complete implementation ledger.
