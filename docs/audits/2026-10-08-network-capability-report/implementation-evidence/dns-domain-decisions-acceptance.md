# Measured native custom-domain decision acceptance

Actual pinned AdGuard Home 0.107.71 and Pi-hole FTL 6.7.1 engines now answer an owned default
client according to reviewed custom-domain additions and removals. Earlier engine runs kept
protection disabled and proved configuration only. This fixture enables protection and measures
every controlled name over UDP and TCP for A and AAAA. It covers protection disabled, the
seeded baseline, after reviewed additions, after an owned engine restart without any reapply,
and after reviewed removals. The [fixture contract](../../../internal/backend/network-dns-domain-decisions-fixture.md)
states the owned sidecar, closed helper, expected decision matrix, bounds and cleanup ownership.

Both passes use clean source `8301171bc48c0ce9c3bae605ad72ed56e494bc7b`. It descends from the
previous agent's prepared fixture and its restart diagnostics, which in turn descend from the PR
checkpoint `8888b56f`. The [compile receipt](dns-domain-decisions/compile-receipt.json) hashes all
1,869 backend Go files and both module files. It pins the static helper
`10ecefda2955e2863d1b856695d41d1c80b93c683425df1c3a693de12aa84ce8` and the Go 1.26.8 race binary
`e94307b06b9bf423faa26445fd02408754a4fb49335f6ebe16ada7e98c26a7ff`. The
[serial runner](dns-domain-decisions/native-runner.py) changes only literal paths and hashes from
the reviewed parent runner. Each invocation verified the unchanged source, assets, runner, host
resolver/networkd/NetworkManager witnesses, Docker socket and complete image-ID inventory. Each
also verified zero remaining owned processes, containers, networks, volumes, decision images and
TMP entries.

| Check | Wrapper / test time | Evidence |
| --- | --- | --- |
| Required changed-file gate against `17aeaae3` | 8.178s / DNS 0.748s | [raw](dns-domain-decisions/required.log.raw.gz), [result](dns-domain-decisions/required.result.json) |
| Focused decision/helper race selection | 14.812s / 10.004s + 1.018s | [raw](dns-domain-decisions/focused-race.log.raw.gz), [result](dns-domain-decisions/focused-race.result.json) |
| Pi-hole FTL 6.7.1, 186 questions | 95.588s / 94.56s | [raw](dns-domain-decisions/native-pihole.log.raw.gz), [result](dns-domain-decisions/native-pihole.result.json) |
| AdGuard Home 0.107.71, 184 questions | 79.426s / 78.39s | [raw](dns-domain-decisions/native-adguard.log.raw.gz), [result](dns-domain-decisions/native-adguard.result.json) |

Each engine's two current-phase native history reads corroborated the selected allow and deny
rules for A and AAAA. AdGuard retained its plain-DNS encryption marker; Pi-hole does not report
transport. Pi-hole additionally applied an explicit empty-group denial, which stayed positive for
the default client group zero. Final removals restored the complete literal seed, foreign
client, group and source inventory.

## Corrected failures

The parent fixture's last attempt, at `053f0951`, failed after the owned restart: each of 16
post-restart questions was refused before exec. Its receipts show Moby gave the restarted engine a
fresh endpoint ID, MAC and PID while every other captured identity, including the IPv4 target,
the sidecar and the two-endpoint roster, was unchanged
([raw](dns-domain-decisions/restart-diagnostic-adguard-failed.log.raw.gz),
[result](dns-domain-decisions/restart-diagnostic-adguard-failed.result.json)). Source `17aeaae3`
admits exactly that one transition after the fixture's own restart. Nine pure cases refuse an
unchanged PID, changed engine IPv4, malformed or split MAC, changed configuration, restarted or
changed client, extra endpoint, and later endpoint drift.

With that source, AdGuard passed ([raw](dns-domain-decisions/superseded-invalid-adguard-pass.log.raw.gz))
but Pi-hole's first question failed ([raw](dns-domain-decisions/superseded-invalid-pihole-failed.log.raw.gz)).
Pinned FTL always generates `server=/invalid/`, so it answered the `.invalid` fixture names locally
and never asked the helper. Source `8301171b` uses reserved `.example` names, which RFC 6761 tells
caching resolvers to forward normally. The `17aeaae3` AdGuard pass is superseded rather than
relabeled; only the two `8301171b` passes above are acceptance.
[Copy validation](dns-domain-decisions/log-copy-validation.json) records every original digest and
byte-exact gzip roundtrip.

## Boundaries

This establishes measured IPv4-client decisions for the controlled exact/suffix rules, seeded
parent and lookalike names on these two pinned engines and this host's Docker bridge. It does not
establish IPv6 client transport, subscribed list content, arbitrary rule precedence, other
clients or groups, Technitium filtering, timer or host reboot recovery, or complete
query/filter/zone/view/client/DHCP coverage. No browser, frontend, DTO or route change is
included. P17 and all 187 ledger statuses remain unchanged.
