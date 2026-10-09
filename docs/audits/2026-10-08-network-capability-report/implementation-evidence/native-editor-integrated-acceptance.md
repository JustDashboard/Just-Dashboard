# Integrated native profile editor acceptance

The strict native profile editor, pending reconnection policy and mounted route admission checks
were tested together at `ac9d6e420c82599dc5bd1a7a6c1ad07261fdbc88`, against `4f9f2b4f`.
The [matching production build](native-editor-integrated-build.txt) passed and served only on
`127.0.0.1:43143`. This is the source before the later native domain/recovery corrections and DNS
service integration; their final reachable gate is separate.

The [original required invocation](native-editor-integrated-original-checks.txt) passed Prettier,
ESLint, TypeScript, 3,176 fast logic tests with 13,876 assertions in 237 files, Go build/vet and
the selected API/netx tests (10.187 and 1.528 seconds). Its 958 selected browser cases finished
with 897 passes, 60 optional skips and one failure in 42.9 minutes. The script exited 1.

The failure was the desktop database design-system walk timing out after 250 seconds at
`page.waitForLoadState("networkidle")`, while opening `/databases/1/query`. The original
[error context](native-editor-integrated-database-initial-error.md) and raw selected run are retained;
the full trace remains at
`/home/ubuntu/jd-native-int-tmp/native-integration-database-initial-failure/trace.zip`.
Checked-in build text and error context remove terminal/trailing whitespace only; the original build
and error context remain under `/home/ubuntu/jd-native-int-tmp/`.
This was not a native profile assertion or a command-sandbox initialization failure.

The [exact desktop case](native-editor-integrated-database-exact-rerun.txt) passed against the same
unchanged source and production server in 54.0 seconds (55.0 seconds including its wrapper).
No assertion or timeout was changed. A preceding command used an over-anchored title filter and
selected no tests; it did not execute an acceptance case. All 898 executable cases have therefore
passed, while the original required invocation remains nonzero and all 60 optional skips remain
unverified.

An earlier [interrupted iteration](native-editor-integrated-interrupted-veth-contract.txt) is also
retained. It was stopped when the mounted editor's runtime parser rejected a supported native veth
profile. The closed allowlist and focused real response fixture were corrected before the final
build above. That interruption is not a passing required run.

The backend admission tests reject immediate/pending-owner-less requests before recovery locking,
prior cleanup or file effects. Mounted `Server.Routes` tests verify session/capability gates,
mandatory pending confirmation, filename validation and destructive cleanup authorization.
The strict editor refuses unsupported states, retained stale owner reads, arbitrary formatting,
noncanonical wire values and foreign profile adoption; it preserves drafts after refusal.

## Subsequent native safety checkpoint

The independently frozen v6 source `d09666fe5a100ce15e78b7d09a53b3e4018197f5` adds selected/staged
byte/inode checks before native checkpoint rollback or profile activation. Refusal durably releases
only an exact saved, epoch-pinned, device-scoped checkpoint and verifies its disappearance. Unknown
release/scope/storage outcomes retain a degraded journal and report an unresolved native deadline.

Its [source manifest](native-v6-source-validation.json) records all six file hashes and commands.
The [required changed check](native-v6-required-final.log) passed Go build/vet and selected tests in
1.321 seconds. The [focused recovery race lane](native-v6-race-final.log) passed in 14.088 seconds.
Actual same-helper networkd and NetworkManager automatic recovery/deadline containment evidence
is recorded separately, together with the [combined final-source reachable gate and exact assembled
Ubuntu generated-origin pass](native-dns-integrated-acceptance.md).

## Scope still open

The subsequent [v8 authored Netplan proof](../../../internal/backend/evidence/native-manager-netplan-v8-2026-10-09.md)
passes an explicit standalone DHCPv4/SLAAC policy on its separately frozen source. Existing bond/VRF
structural editing, default Netplan DHCP MTU, DHCPv6 acquisition, wider native owners and actual host
reboot remain open in P11. Disposable fixture process death, native checkpoint
expiry and container restart are distinct from those acceptance requirements.
The [full ledger](../implementation-status.md) keeps P11 in progress.
