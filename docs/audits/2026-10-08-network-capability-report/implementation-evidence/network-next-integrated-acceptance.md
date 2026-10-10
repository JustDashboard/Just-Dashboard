# Combined network implementation checkpoint

This checkpoint integrates the observer UI/backend lifecycle, IPAM reservation draft
lifecycles, DNS alias safety, mandatory selected drift confirmation and the preceding
capture, external-check, download SQM and dual-stack WireGuard slices. It retains all
187 report requirements. Six requirements are verified, 53 remain in progress and 128
remain pending; these checks do not establish a complete report or raise its historical
scores.

## Source and selected checks

The production frontend was built from application source `b8050335`, then served on
`127.0.0.1:43139`. The later VPN/DNS test corrections change only test files and evidence;
the frontend application matches that build. Go uses 1.26.8 with bounded compiler
parallelism and temporary data on the workspace filesystem.

```sh
cd frontend
bun run build
bun run start --hostname 127.0.0.1 --port 43139
```

```sh
GOMAXPROCS=2 GOFLAGS=-p=2 \
TMPDIR=/home/ubuntu/jd-network-validation-tmp/network-next \
JD_BROWSER_BASE_URL=http://127.0.0.1:43139 JD_BROWSER_WORKERS=1 \
scripts/test-changed.sh 40573e4d1fea9ace65df6aacb9fc71f115b1e2d1
```

The build, changed-file Prettier/ESLint, TypeScript, 3,131 Bun tests, Go build and
selected-package vet passed. The selector reached 25 browser specs and 509 cases. The
original browser run finished with **448 passed, 60 optional screenshot/evidence cases
skipped and one failed** in 16.2 minutes. The original script exited **1**, because that
DNS assertion and two stale VPN Go fixtures failed. Its failure remains in the raw log.

The DNS failure expected an evidence label to be the complete paragraph, although the
paragraph also displays its native basis. The corrected assertion verifies the label
and basis within the semantic definition. Its required selected gate passed all seven
DNS evidence cases on the same built application; **all 449 executable cases selected
by the original run have now passed**. The 60 optional cases are not counted as passes.
The original failure snapshot and corrected run are preserved in the
[DNS test evidence](dns-browser-test-contracts.md).

The original Go stage passed API, capture, Docker, flow, diagnostic, IPAM and controlled
vantage tests before finding the two stale VPN fixtures. The recorder now explicitly
answers the new read commands, and the JSON-key test ignores fields that are not
serialized. Existing count, secret-redaction and exported-key checks remain intact.
The corrected targeted race and required neighboring-test gate passed. The combined
netx stage then passed in **26.269 seconds**, and the previously skipped store stage
passed in **11.956 seconds**. See [VPN test evidence](vpn-summary-test-contracts.md).

Before push, the combined correction gate
`scripts/test-changed.sh b80503350e3f14ff345527bbc1a7a166f963b470` also exited **0**
in the integration worktree: changed-file formatting/lint, TypeScript, all 3,131 Bun
tests, Go build/vet, neighboring netx tests (0.910 seconds), and all seven selected DNS
browser cases (12.0 seconds) passed. Application source still matched the same build.

## Recovery and native evidence

The mounted private observer/DNS API race passed in **7.883 seconds**. The native
selected-drift fixture passed in **5.955 seconds**, including independent expiry
recovery while preserving foreign and unselected IPv6 resources. Its API/browser
checks show that repair still requires pending reconnection confirmation when the
global preference is off; missing ownership stops before effects. See
[mandatory confirmation](drift-required-confirmation.md).

Prior final-source native evidence remains attributed to its actual slice: bounded
kernel TCP/UDP observation and process-death cleanup, signed native split-DNS aliases,
actual Docker/IPAM handoffs, PCAP cleanup, WireGuard dual-family containment and
download SQM recovery/congestion fixtures. Those fixtures do not establish provider
coverage, real host reboot, general overhead or full application/tunnel connectivity.

An earlier combined attempt was deliberately interrupted during TypeScript checking
when review found the DNS lookup's unconditional split-policy promise. That copy was
corrected before the fresh build above. The interrupted attempt is retained and is not
counted as a completed check.

## Documentation and remaining scope

Before push, the complete diff against `7be11ba0` was compared with `docs/internal/`,
`AGENTS.md`, `README.md` and `CONTRIBUTING.md`. Network/recovery/drift/DNS/flow/IPAM/
capture/SQM/WireGuard and architecture/frontend maps document their actual behavior.
The selected repair's mandatory confirmation and configured-chain DNS limitations
were corrected in the operator and owning internal guides. The optional local browser
worker limit is documented in the contributor guides. The test-only corrections need
no product behavior, schema or configuration changes.

All 187 ledger IDs are unique; checked relative Markdown file links resolve. Evidence
exports had terminal carriage returns/trailing whitespace normalized without dropping
failed results; original workspace logs remain retained. No dependency, lockfile,
licensing, release machinery or CI change is included.

Persistent native-manager and managed DNS-service implementations have separate
committed/draft evidence and remain outside this integrated application. Existing
bond/VRF editing, automatic addressing, complete service policy/UI acceptance, broader
native owners, actual reboot/provider acceptance and production performance remain
open. Merge and branch deletion still require operator permission.

Raw checkpoint logs: [build](network-next-integrated-build.txt),
[original failed selected run](network-next-integrated-original-checks.txt),
[corrected netx/store](network-next-integrated-corrected-go.txt),
[passing combined correction gate](network-next-integrated-correction-checks.txt),
[mounted private API race](network-next-mounted-private-race.txt) and
[interrupted earlier attempt](network-next-initial-interrupted-checks.txt).
