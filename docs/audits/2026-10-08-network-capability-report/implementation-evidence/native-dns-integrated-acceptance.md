# Integrated native network and DNS service checkpoint

The native profile editor/recovery and P17 DNS service backend/UI are assembled on
`3e0c20161b721de9f769a13439b9813f3f7dfed3`. The required command
`scripts/test-changed.sh ac9d6e420c82599dc5bd1a7a6c1ad07261fdbc88` used merge-base
`1d45011e39e3c076d0651fad475d833f54735bcc`, selecting the remaining native corrections and DNS
service files. It ran against the matching production application on `127.0.0.1:43145` with two
browser workers, `GOMAXPROCS=2`, `GOFLAGS=-p=2` and a workspace-local `TMPDIR`.

The [original combined invocation](dns-services-native-combined-required.txt) exited **0**:

- Changed-file Prettier/ESLint, TypeScript and 3,232 logic tests passed (14,008 assertions, 239 files).
- Go build and vet passed. API tests passed in 194.029 seconds, netx in 2.729 seconds, store in
  5.134 seconds and dnsservice in 7.582 seconds.
- All 410 executable cases in the 470-case selection passed in 8.7 minutes. The 60 optional evidence
  skips remain unverified. The run includes the 13 new DNS service cases, native profile controls,
  pending confirmation, existing network pages and the required design-system spec.

The [matching DNS UI build and visual evidence](dns-services-ui-acceptance.md) explain source
identity, mounted interactions and measured phone/desktop layouts. The
[earlier native editor record](native-editor-integrated-acceptance.md) preserves its separate
nonzero 958-case invocation and exact unchanged-source database timeout rerun; that failure is
not relabeled as a zero exit by this later passing command.

## Native evidence and subsequent fixture

The [automatic owner record](../../../internal/backend/evidence/native-manager-automatic-2026-10-09.md)
attributes real DHCPv4/SLAAC, automatic DNS/domain/routes, manual transitions, process death,
fresh-helper rollback, durable confirmation and cleanup retry to frozen native helpers. The actual
NetworkManager v6 foreign-file case verifies exact checkpoint release and unchanged foreign
profile/stage bytes/inodes past its three-second deadline. Its isolated ancestry remains explicit.

The [exact assembled-source direct-owner proof](../../../internal/backend/evidence/native-manager-v6-assembly-2026-10-09.md)
also passed without skips: Debian NetworkManager in 30.69 seconds including wrapper / 23.18 seconds
in the child, and networkd in 17.08 / 10.45 seconds. The frozen branch contains exact `3e0c2016`
production files plus only the automatic fixture. Both cases independently built the same v6 helper,
SHA256 `8a6b5ad325bccacad61952506c6de28cc9e1309d592c3d566cd3d1aa6f6cf02e`.
They measured DHCPv4/SLAAC, acquired DNS/domain/routes, automatic/manual changes, suppression,
process death, fresh-helper rollback, durable confirmation and cleanup retry. The NetworkManager
case also preserved foreign file/stage bytes and inodes beyond the released three-second native
checkpoint deadline. Exact owned process and temporary-file cleanup passed; host networkd and
NetworkManager state remained unchanged. The guide links the unchanged raw logs and source manifest.

The [actual Ubuntu generated-origin fixture](native-v6-root-ubuntu-exact-origin-final.log) passed
on exact clean `3e0c2016` with the v6 helper: 53.53 seconds including wrapper, 20.00 seconds in the
child, zero skips. It verified the NoCheckpoint strategy, original authored/generated inodes and
bytes, ordinary rollback/confirmation, applying-process death, fresh helper recovery, failed durable
confirmation, post-confirm death and lost cleanup. Exact owned cleanup passed. Its
[source manifest](native-v6-root-ubuntu-source-validation.json) records the source and race binary.

The earlier [experimental ancestry run](native-v6-ubuntu-exact-origin-final.log) refused baseline
agreement before mutation. That tree includes additional saved/loaded structural checks absent from
the assembled product. It remains a separate failed acceptance for that future scope; the unchanged
assembled product was neither weakened nor edited to obtain the passing generated-origin result.
The checked-in failed log trims trailing terminal whitespace only; its original raw path/hash remain
in the source manifest. Passing native logs are copied unchanged.

After the combined invocation completed, the fixture-only handoff `1d63d964` was integrated as
`b1a3c748`. It adds no frontend or production behavior. Its required correction check against
`3e0c2016` [exited 0](native-automatic-assembly-fixture-required.txt), with Go build/vet and the
selected fixture test passing in 0.039 seconds. Normal opt-out is not live acceptance.

Actual pinned AdGuard, Pi-hole and Technitium engine acceptance is in the
[native DNS record](../../../internal/backend/evidence/dns-services-2026-10-09/README.md).
Those native engine passes are distinct from mocked browser inventories, host reboot and measured
client reachability.

## Open acceptance

The full [187-requirement ledger](../implementation-status.md) remains six verified, 53 in progress
and 128 pending. P11 still requires existing-controller structural editing, Netplan automatic
admission, wider owner/platform and reboot proof. P17 still requires broader query/zone/view/client
policy controls. The historical report scores are unchanged.

The [final pre-push documentation review](native-dns-pre-push-review.json) compares the complete diff
with internal guides, `AGENTS.md`, `README.md` and `CONTRIBUTING.md`. Existing-controller structural
admission, default Netplan automatic admission and cold host-runtime acceptance remain open.
