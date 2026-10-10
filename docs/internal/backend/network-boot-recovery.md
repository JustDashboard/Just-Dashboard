# Interrupted network changes at boot

The independent recovery timer runs the installed backend binary as
`network-recovery --network-recover <network-directory> <change-id>`. It restores the durable journal's
file snapshots and undoes the resources changed by that generation. A normal timer, API startup or
synchronous rollback does not replay unchanged managed devices over a running host.

A reboot loses managed bridges, VLANs, veth pairs and named namespaces even when the pending change
did not edit them. Restoring an old route or address before recreating its unchanged bridge would
fail, leave recovery degraded and prevent the ordinary boot unit from restoring the bridge.

`--network-recover-boot` uses the same private journal, process lock, pending-generation check and
error reporting. The journal captures typed commands for the prior managed namespaces, links and
addresses before the candidate is applied. Boot recovery restores the previous files first, replays
those dependency commands, then performs the targeted undo. It does not replay unrelated routes,
policy rules, shaping or gateway policy as dependencies. Existing-object errors are accepted only
for creation commands; permission, parent-device and property-setting failures remain recovery
errors and preserve a degraded journal.

`just-dashboard-network-recovery.service` is enabled before runtime mutation. It wants
`network-online.target`, orders after local filesystems, native network managers, Docker and
firewalls, and before `just-dashboard-network.service`. This lets native owners create physical
parents and compatible chains before recovery uses them. The normal managed unit's `ExecStartPre`
also invokes the boot variant, so restarting that owner can repair an interrupted generation before
loading its saved batches. The transient timer continues to use the ordinary targeted variant.

Both command-line variants run in the namespaces supplied by their host systemd owner, without the
dashboard container, API, database or authentication stack. They execute explicit argv through the
same bounded process-group recovery executor. The dependency command array stays in the root-only
journal and is not exposed by the persistence status API.

`recovery_boot_test.go` verifies the dependency scope, snapshot/dependency/undo ordering, retained
failures, duplicate handling and generated unit ordering. With `JD_NETNS_LIVE=1`,
`TestLiveBootRecoveryRecreatesUnchangedManagedDependenciesAfterColdLoss` uses a disposable network
and mount namespace. A subprocess dies after writing a candidate that removes an address and route;
the fixture then removes its unchanged bridge, veth and named namespace. A fresh timer process
fails visibly without recreating them, while a fresh boot process restores the prior dependencies,
address, route, spec and healthy recovery phase. This cold fixture leaves the host's systemd unit
files and network untouched; it does not reboot the host.

`JD_NETNS_LIVE=1 JD_SYSTEMD_RECOVERY_LIVE=1 go test -race ./internal/netx -run '^TestLiveSystemdTimer'`
also exercises a reachable real host systemd manager. It builds a static standalone helper and
launches uniquely named transient timers with `NetworkNamespacePath` pointing to a disposable
namespace. An expired pending generation restores its private journal snapshot and removes its
candidate dummy device without a running backend. Later timers for a completed ID or an older ID
execute successfully and leave the current runtime and journal untouched. The test cleans its exact
transient units and namespace; it does not install persistent host units or change host networking.
The marker is an explicit systemd `ExecStartPost` command, so observing it proves the helper exited
successfully after timer dispatch. This is timer acceptance, separate from the cold-runtime boot
fixture and from an actual host reboot.

The [guest reboot acceptance](../../audits/2026-10-08-network-capability-report/implementation-evidence/network-vm-reboot-acceptance.md) adds actual
reboots of a disposable Ubuntu 24.04 guest (systemd 255): after a kill at each journal phase and an
immediate reboot, `just-dashboard-network-recovery.service` recovered the change with the backend never
started, the ordinary unit then restored the prior generation, and drift matched. That boot's ordinary
activation reads failed because its creation lines meet the devices the recovery already recreated.
