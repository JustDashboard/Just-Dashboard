# Private PCAP acceptance

This source slice implements bounded private capture and preserves the existing quick packet
summary. All observations below are local fixture evidence; provider paths, offload and general
production cost remain unverified.

- `TMPDIR=/home/ubuntu/jd-network-validation-tmp GOMAXPROCS=2 go test -race ./internal/netcapture -count=1`: PASS, 11.565s. Includes closed filters, PCAP formats/integrity, durable restart without replay, pending cleanup, failed final recording without recapture, provisional progress, concurrency and total retention with old active records.
- `go test -race ./internal/api -run TestCaptureAPI -count=1`: PASS, 4.075s. Actual router covers private download/audit/redaction, read/limited/narrowed-admin-token refusal, generic-job privacy and cancellation lifecycle.
- `JD_NETCAPTURE_LIVE=1 go test -race ./internal/netcapture -run Live -count=1 -v`: PASS, 9.229s. Root-owned disposable namespace; IPv4 2 packets/184 bytes, IPv6 2 packets/224 bytes, genuine native drop counters and SHA-256, quiet timeout, cancellation and independent host timeout after applying-process SIGKILL. The timeout removed all namespace PIDs after 3.023s. Native transcript is `pcap-native-race.txt`.
- `go build ./...`, selected-package vet, TypeScript, changed-file Prettier/ESLint and two pure Bun tests (20 assertions) pass. No dependency or CI change.
- Six browser cases cover 390/1440 layouts, no recapture on reload, retained rejected IPv6 scope/incident, dated failed polls, pending cleanup/interrupted startup and read-account privacy. They await a matching combined production build before their results can be recorded.

The backend-death fixture proves a bounded quiet native process ends independently. An interrupted
persisted row still labels final cleanup unverified because the restarted backend did not observe
the predecessor exit. The artifact contains original packet bytes; only the separate support JSON
is redacted.
