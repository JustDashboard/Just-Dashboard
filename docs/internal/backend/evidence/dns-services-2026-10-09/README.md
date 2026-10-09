# DNS service core/API acceptance, 2026-10-09

The native runs used the product/test source captured by `final-native-source.sha256` and the
race binary captured by `final-native-binary.sha256`. All three runs passed without a skip. The
later metadata-only `history.go`, its private API route and tests are covered separately by
`final-history-http-private-api-race.log` and the final required selected gate. P17 UI and fresh
integrated acceptance remain open. Failed native attempts are separate logs, not passing evidence.

From `/home/ubuntu/Just-Dashboard/.network-worktrees/dns-services-safety/backend`, the native binary
was compiled with:

```bash
env TMPDIR=/home/ubuntu/Just-Dashboard/.network-worktrees/dns-services-artifacts/tmp GOMAXPROCS=2 \
  go test -race -p 2 -c \
  -o /home/ubuntu/Just-Dashboard/.network-worktrees/dns-services-artifacts/dns-services-live.test \
  ./internal/dnsservice
```

Each engine was run serially with the following command, using `adguard`, `pihole` or `technitium`
for `JD_DNS_SERVICES_LIVE_ENGINE`:

```bash
env TMPDIR=/home/ubuntu/Just-Dashboard/.network-worktrees/dns-services-artifacts/tmp GOMAXPROCS=2 \
  JD_DNS_SERVICES_LIVE_ENGINE=adguard \
  JD_DNS_SERVICES_NATIVE_LOG_DIR=/home/ubuntu/Just-Dashboard/.network-worktrees/dns-services-artifacts/native-logs \
  /home/ubuntu/Just-Dashboard/.network-worktrees/dns-services-artifacts/dns-services-live.test \
  -test.run '^TestDNSServiceNativeOwnedEngine$' -test.count=1 -test.v
```

Final logs: AdGuard `native-adguard-final-source.log` (25.17 seconds), Pi-hole
`native-pihole-final-source.log` (46.21 seconds), and Technitium
`native-technitium-head-readiness.log` (16.69 seconds). Each verifies exact owned resource absence
after removal and cold interrupted-predecessor cleanup. Each command exited zero; owned children
ended. After all three, these independent read-only inventories again exited zero with empty output:

```bash
docker ps -a --filter label=io.justdashboard.dns.owner --format '{{.ID}} {{.Names}}'
docker network ls --filter label=io.justdashboard.dns.owner --format '{{.ID}} {{.Name}}'
docker volume ls --filter label=io.justdashboard.dns.owner --format '{{.Name}}'
```

The final focused race command, from the same backend directory, was:

```bash
env TMPDIR=/home/ubuntu/Just-Dashboard/p17-t.P45lLQ GOMAXPROCS=2 \
  go test -race -p 2 ./internal/dnsservice ./internal/api \
  -run '^(TestDNSNative.*|TestDNSServiceAPIPrivateCapabilitiesSealingAndAudit|TestDNSServicePiHoleWaitsForNativeRestartWithoutRepeatingMutation|TestDNSServiceRetainedHistoryIsBoundedPrivateMetadataWithoutNativeAccess)$' \
  -count=1 -v
```

It passed DNS services in 13.678 seconds and the private API in 4.640 seconds. The TLS bad-certificate
messages are expected negative identity checks. The required selected gate, from the worktree root,
was:

```bash
env TMPDIR=/home/ubuntu/Just-Dashboard/p17-t.P45lLQ GOMAXPROCS=2 GOFLAGS='-p=2' \
  bash scripts/test-changed.sh 148e4f38
```

`test-changed-core-nonhidden-tmpdir.log` records terminal success: build/vet plus API 174.290 seconds,
store 4.533 seconds and DNS services 10.337 seconds. No frontend files changed or browser cases were
selected. Earlier API gate failures from Unix-socket path length and hidden-directory classification
remain in the separate workspace artifact logs described in the module guide. No product change
bypassed those existing path rules. The runtime code captured in the native source manifest was
checked again against its hashes before this handoff; every entry matched.
