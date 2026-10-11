import type { SystemdUnit } from "@/lib/types"
import { journalSource, kernelSource } from "@/lib/log-sources"
import type { ServiceLogSource } from "@/components/logs/service-logs"

/** The daemons that bring links up, and so say why one did not. */
const NETWORK_UNITS = ["systemd-networkd.service", "NetworkManager.service"]

/**
 * Where a link's changes are written: the kernel's ring, which says when a
 * device came up, lost its carrier or was renamed, and the journal of
 * whichever daemon manages the links — read from the host's units, since the
 * page itself does not know which one that is.
 */
export function interfaceLogSources(
  listed: Pick<SystemdUnit, "name" | "description" | "activeState">[] | undefined,
): ServiceLogSource[] {
  const units = (listed ?? []).filter(
    (unit) => NETWORK_UNITS.includes(unit.name) && unit.activeState !== "inactive",
  )
  return [
    {
      id: kernelSource(),
      label: "Kernel ring",
      kind: "kernel",
      lens: "kernel",
      product: "linux",
      detail: "Links coming up and going down, drivers, the OOM killer",
    },
    ...units.map((unit): ServiceLogSource => ({
      id: journalSource(unit.name),
      label: unit.name,
      kind: "journal",
      detail: unit.description,
    })),
  ]
}
