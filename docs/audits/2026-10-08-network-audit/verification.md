# Networking verification

Base: `patch/0.7.1` at `0f276d8c`. All changes are integrated in a separate worktree on
`improve/network-audit`; the original worktree remains clean on its active branch.

## Final integrated checks

- Ordinary `bun run build` passed with its TypeScript gate. The production frontend is served from
  this tree on loopback port 43193; browser checks use `JD_BROWSER_BASE_URL=http://127.0.0.1:43193`.
- The complete `scripts/test-changed.sh patch/0.7.1` gate covers changed frontend formatting/lint,
  TypeScript, pure logic, backend build/vet/selected adjacent tests and selected browser specs,
  including the required design-system checks. **Passed**: 274 browser cases passed, zero failures,
  60 opt-in screenshot cases skipped (5.9m). All 22 new audit browser regressions passed. The skips
  are screenshot-capture opt-ins, not skipped product regressions; review artifacts are included below.
- `bun test src`: **3,063 passed**, zero failures, 13,401 assertions across 220 files.
- `go test -race ./internal/netx ./internal/netsec`: passed (14.477s and 2.517s).
- `go test -race ./internal/api -run '^Test(Network|Tailscale|WireGuard)' -count=1`: passed (13.026s).
- `JD_NETNS_LIVE=1 go test -race ./internal/netx -run Live -count=1 -v`: passed (71.219s).
  All seven real-kernel tests ran with zero skips, using disposable network/mount namespaces. They
  exercise device/namespace mutations, gateway rules and counters, shaping/clsact preservation,
  boot replay including IPv6 policy rules, capability reading and restoration artifacts. The name
  filter also selects the stale-container-rate regression, which passed.
- `GOOS=linux GOARCH=arm64 go build ./cmd/server`: passed with an isolated build cache. This verifies
  Linux ARM64 compilation; it does not establish execution on every ARM board/provider. Native
  tests use Go 1.26.8 on Linux AMD64.

DNS fixtures use ephemeral loopback UDP/TCP servers for all eight query types, hosts-file bypass,
response identity/owner validation and truncation fallback. Persistence/cancellation tests exercise
partial writes, failed recovery and real descendant-process cleanup. Docker dependency tests use a
fake Engine and assert refusal without executing a network mutation.

The initial integrated browser run exposed an existing proxy polling test whose long virtual-clock
jump overtook asynchronous responses. Its sequencing was corrected to await settled responses;
request-count expectations and product code remain unchanged. Five repeats passed before the final
complete changed-file run. Adding Fallback servers also exposed an ambiguous existing DNS-test
selector; it now selects the upstream Servers field exactly, retaining the TLS/no-write assertions.
One later run ended with SIGTERM before completion and is not counted as a pass; the final monitored
complete gate passed without further source changes. Independent review also caught the subprocess output-cap bypass and
misleading packet/IPv6 guarantees, which have code or wording corrections and regression evidence.

No production host interface, resolver, firewall or tunnel was changed for verification. Wake-on-LAN
frame sending is mocked; recipient hardware/firmware behavior was not tested. Browser API fixtures
validate UI contracts/permissions and do not replace real-provider/hardware acceptance. The whole
browser suite and `go test ./...` were not run, per the contributor workflow.

## UI review artifacts

A [21-second recording](review/network-controls.webm) and screenshots show the final forms:
[route preferred source](review/routing-preferred-source.png),
[policy outgoing interface/note](review/routing-outgoing-interface-note.png), and
[DNS fallback/cache confirmation](review/dns-fallback-cache-confirmation.png).
All three screenshots were inspected at original size without clipped fields/buttons. The
[recording metadata](review/review-metadata.json) includes the source revision and exact intercepted
API payloads. These use the existing network fixtures on the final production frontend; no host write
was performed. The recording shows interactions, while browser assertions verify request contracts.

## Documentation review

The complete diff was compared with `docs/internal/`, `README.md`, `AGENTS.md` and `CONTRIBUTING.md`.
Behavior changes update the network/backend/frontend guides and the full audit inventory. No
dependency, licence, CI or contributor command/workflow change is introduced; `AGENTS.md` and
`CONTRIBUTING.md` require no edit. No release is being cut and CHANGELOG is untouched.
