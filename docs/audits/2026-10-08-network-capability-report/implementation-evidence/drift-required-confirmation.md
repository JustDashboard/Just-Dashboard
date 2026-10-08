# Required confirmation for selected drift repair

Selected owned drift repairs now require pending mode at the service boundary. A request without a
positive authenticated pending owner returns `network_confirmation` before native inspection, lock
creation or change-journal creation. The mounted `Server.Routes()` regression verifies the HTTP 409
refusal. Existing selected-write, stale-review, foreign-replacement and recovery cases use pending
mode so they continue to exercise their deeper safety checks.

The frontend explicitly requests pending mode. The browser regression makes independent recovery
available, switches the global confirmation preference off, then checks that the reviewed repair
still sends `X-JD-Network-Apply: pending`. Its source-matched integrated browser gate is pending at
this checkpoint; formatting and ESLint passed.

With Go 1.26.8, from `backend/`:

```bash
GOMAXPROCS=2 GOFLAGS=-p=2 \
  TMPDIR=/home/ubuntu/jd-network-validation-tmp/drift-required-confirmation \
  go test -race ./internal/netx ./internal/api \
  -run '^(TestDriftRepair|TestNetworkReportMountedDriftRepair|TestNetworkDrift)' -count=1
```

**PASS:** `internal/netx` 89.073 seconds; `internal/api` 5.425 seconds.
The exact output is retained in [the targeted race log](drift-required-confirmation-race.txt).

The corresponding final-source native fixture also passed:

```bash
GOMAXPROCS=2 GOFLAGS=-p=2 \
  TMPDIR=/home/ubuntu/jd-network-validation-tmp/drift-required-confirmation JD_NETNS_LIVE=1 \
  go test -race ./internal/netx \
  -run '^TestLiveDriftRepairTouchesOnlySelectedAdmissionAndRestoresItAfterDeadline$' -count=1 -v
```

**PASS:** fixture 4.82 seconds; package 5.955 seconds, with no skip. It applies only a selected owned
IPv4 admission rule in a disposable network/mount namespace, preserves foreign rules, unselected
chains, IPv6 and an unrelated native device, then verifies exact restoration by expired-journal
boot-context recovery. Its owned namespace is removed by test cleanup.
[The native log](drift-required-confirmation-native.txt) records the result. This test records
watchdog preflight; real systemd timer dispatch remains the separately documented acceptance.
