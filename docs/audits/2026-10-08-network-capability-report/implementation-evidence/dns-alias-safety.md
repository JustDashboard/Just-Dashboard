# Native DNS alias safety acceptance

This checkpoint strengthens P9/F10 in PR #173. It does not close the full networking ledger or prove
application NSS, an upstream forwarder's routing, provider behavior, off-host clients or every DNS
owner. Historical scores and the 187-item report scope stay unchanged.

The native adapter disables automatic CNAME/DNAME chasing, reads an exact-owner CNAME explicitly,
and checks the target's fresh complete split policy and unique bus owner before asking it. Inactive,
serverless, unreadable or unowned private scopes receive no target/default question. Each question
retains its owner, selected policy before/after, complete-policy digests, accepted records, answer
links, diagnostic and transport/TLS/DNSSEC provenance. Trust claims require the entire accepted
chain. An eight-redirect limit, shared twenty-second deadline and aggregate artifact/record bounds
limit work. Effective lookup reads the host namespace's resolver file, including when Docker's
container file disagrees, and uses this adapter only for supported resolved delegation.

## Native fixture

From the isolated task worktree's `backend/`, with temporary files on the workspace filesystem:

```bash
env TMPDIR=/home/ubuntu/Just-Dashboard/.network-worktrees/dns-alias-artifacts/tmp GOMAXPROCS=2 \
  go test -race -p 2 -c \
  -o /home/ubuntu/Just-Dashboard/.network-worktrees/dns-alias-artifacts/dns-evidence.test ./internal/netx
sudo -n env TMPDIR=/home/ubuntu/Just-Dashboard/.network-worktrees/dns-alias-artifacts/tmp GOMAXPROCS=2 \
  /home/ubuntu/Just-Dashboard/.network-worktrees/dns-alias-artifacts/dns-evidence.test \
  -test.run '^TestDNSEvidenceNativeDisposableResolver$' -test.count=1 -test.v
```

[The final run](dns-alias-native-pass.txt) passed with no skip: 5.94 seconds inside the private
namespace and 7.28 seconds for the wrapper. The fixture uses actual systemd-resolved 257.4, private
network/mount namespaces, private `/etc` and `/run/systemd`, private D-Bus, controlled IPv4/IPv6 TLS
servers and an Ed25519-signed private zone with a fixture-only trust anchor. It proves signed safe
CNAME answers, whole-chain native validation/encryption, blocked inactive private alias targets,
loop/DNAME refusal, foreign chain refusal, strict TLS identity rejection and genuine DNSSEC
rejection. A neutral EDE text spoof under an explicit nonvalidating fixture scope remains an ordinary
server/RCODE error; strict signed-zone policy is restored for genuine validation failures. Owned
processes are killed and waited during cleanup; namespaces and temporary state disappear. No host
resolver was restarted or configured. The native lane was released before the next team's fixture.
One whitespace-only line in the successful transcript is trimmed for repository checks; the
original output remains in the task artifact directory. Failed native transcripts are unchanged.

The native run predates a small per-question DNSSEC-failure label addition and the separate host-file
selection fix; focused races cover those final changes. The integrated gate must use the final source.

## Source contract corrections and preserved failures

The authoritative [v257 definitions](https://github.com/systemd/systemd/blob/v257/src/resolve/resolved-def.h)
place `NO_CNAME` at bit 5 and `NO_VALIDATE` at bit 10. The final caller mask is exactly
`(1<<0)|(1<<5)|(1<<11)|(1<<12)|(1<<13)|(1<<14)|(1<<24)`. It never disables DNSSEC validation.
`NO_TRUST_ANCHOR` excludes a local anchor answer, preserving the validator's trust policy.
[ResolveRecord](https://github.com/systemd/systemd/blob/v257/src/resolve/resolved-bus.c) rejects caller
`NO_SEARCH` and adds it internally. Only known reply protocol/origin/authentication/confidentiality
bits establish evidence; unknown future bits leave measurements unknown.

The actual fixture caught mistakes that transcript mocks had missed. No mistaken iteration was
committed or pushed. The original failed outputs are retained:

| Log | Failure and correction |
| --- | --- |
| [Initial flags](dns-alias-native-initial-flags-failed.txt) | An incorrect `NO_CNAME` literal used bit 10 and disabled validation. The real signed reply lacked authentication. Corrected against the primary header, then added an independent literal-mask assertion. |
| [EDE code](dns-alias-native-ede-code-failed.txt) | EDE code 6 legitimately became a native DNSSEC upstream-failure. The spoof test now uses neutral code 0. |
| [Caller search bit](dns-alias-native-search-bit-failed.txt) | Native ResolveRecord rejected caller `NO_SEARCH`. Removed it after inspecting the actual method's allowed mask. |
| [Unsigned error in strict zone](dns-alias-native-strict-ede-failed.txt) | The unsigned SERVFAIL response in an anchored strict zone legitimately failed with `no-signature`. The ordinary RCODE/EDE test now declares its nonvalidating fixture scope, then restores strict policy. |

The implementation recognizes a DNSSEC failure only from the first native diagnostic line's exact
`Call failed: DNSSEC validation failed:` prefix. A server-controlled occurrence inside a generic
RCODE diagnostic, including an embedded newline, cannot acquire this identity. This is native
reported error evidence, not independent cryptographic validation.

## Focused checks and remaining gate

The focused netx races cover safe/blocked/looped aliases, eight redirects, exact native flags,
whole-chain trust, empty/malformed policy, changed owners, host-vs-container resolver disagreement,
unreadable host chains and native error spoofing. API races cover private capabilities, immutable
history/export, alias provenance and audited deletion. From `backend/`:

```bash
env TMPDIR=/home/ubuntu/Just-Dashboard/.network-worktrees/dns-alias-artifacts/tmp GOMAXPROCS=2 \
  go test -race -p 2 ./internal/netx \
  -run '^Test(DNSAlias|DNSEvidence|EffectiveLookup|Lookup|ReadResolvConf)' -count=1 -v
env TMPDIR=/home/ubuntu/Just-Dashboard/.network-worktrees/dns-alias-artifacts/tmp GOMAXPROCS=2 \
  go test -race -p 2 ./internal/api -run '^Test(DNSEvidence|NetworkDNS)' -count=1 -v
env TMPDIR=/home/ubuntu/Just-Dashboard/.network-worktrees/dns-alias-artifacts/tmp GOMAXPROCS=2 \
  go vet -p 2 ./internal/netx
env TMPDIR=/home/ubuntu/Just-Dashboard/.network-worktrees/dns-alias-artifacts/tmp GOMAXPROCS=2 \
  go build -p 2 ./...
```

[Final selected netx races](dns-alias-netx-race-final.txt) pass in 11.954 seconds, including the
host-file selection fix. [The earlier API races](dns-alias-api-race.txt) pass in 8.359 seconds and
[the earlier netx package races](dns-alias-netx-race-initial.txt) pass in 9.532 seconds. An initial
[compiled-unit-binary invocation](dns-alias-unit-cwd-failed.txt) from `backend/` failed because its
relative `testdata` fixtures require the package directory; the package `go test` commands above
corrected the invocation. Vet and build pass.

Frontend Prettier, ESLint, TypeScript and two provenance unit cases pass. The DNS browser spec
enumerates seven cases, including per-question evidence at 390 and 1280 pixels. Browser cases and
the final selected `scripts/test-changed.sh` gate remain for the integrated fresh production build;
enumeration alone is not rendered acceptance.

Documentation review covers `docs/internal/`, `AGENTS.md`, `README.md` and `CONTRIBUTING.md` against
the complete diff. Native DNS, effective lookup and frontend provenance docs are updated. Root
architecture, licensing, dependencies, contributor workflow and deployment configuration are
unchanged, so the latter three files require no edit. No CI or hosted check was added.
