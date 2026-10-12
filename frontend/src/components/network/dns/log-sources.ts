import type { DNSView } from "@/lib/types"
import type { DNSEngine, DNSServiceProvision } from "@/lib/network-dns-services"
import { dockerSource, journalSource } from "@/lib/log-sources"
import type { ServiceLogSource } from "@/components/logs/service-logs"
import { KIND_PRODUCT } from "@/components/network/dns/resolvers"

/** The units a program on the host's port 53 runs as, by what the listener turned out to be. */
const HOST_UNIT: Partial<Record<DNSView["listeners"][number]["kind"], string>> = {
  dnsmasq: "dnsmasq.service",
  unbound: "unbound.service",
  named: "named.service",
}

/** What the engines this dashboard provisions are drawn as; Technitium has no mark. */
const ENGINE_PRODUCT: Record<DNSEngine, string | undefined> = {
  adguard: "adguard",
  pihole: "pihole",
  technitium: undefined,
}

/**
 * Where the things that answer this host's lookups write, from what the page
 * already reads: systemd-resolved's unit; the unit of each resolver program
 * holding port 53 on the host; the container of each one that runs in Docker
 * — a listener's, an ad-blocker's, or a setup this dashboard provisioned.
 * Nothing for a program the listener table cannot name, since its unit would
 * be a guess. The server detects each source's lens: DNS has none of its own.
 *
 * A container is named once, by the most particular name the page has for
 * it: the setup's, then the product's, then Docker's.
 */
export function dnsLogSources(
  view: DNSView,
  provisions: DNSServiceProvision[] = [],
): ServiceLogSource[] {
  const sources: ServiceLogSource[] = []
  if (view.resolved.installed) {
    sources.push({
      id: journalSource("systemd-resolved.service"),
      label: "systemd-resolved.service",
      kind: "journal",
    })
  }
  for (const listener of view.listeners) {
    const unit = HOST_UNIT[listener.kind]
    if (listener.container || !unit) continue
    const id = journalSource(unit)
    if (sources.some((s) => s.id === id)) continue
    sources.push({ id, label: unit, kind: "journal", product: KIND_PRODUCT[listener.kind] })
  }

  const containers = new Map<string, ServiceLogSource>()
  const add = (name: string, source: Omit<ServiceLogSource, "id" | "kind">) => {
    if (!containers.has(name)) {
      containers.set(name, { id: dockerSource(name), kind: "docker", ...source })
    }
  }
  for (const setup of provisions) {
    const { containerId, containerName } = setup.resources
    if (!containerId || setup.state === "removed") continue
    add(containerName, {
      label: setup.request.name,
      product: ENGINE_PRODUCT[setup.request.engine] ?? "docker",
      detail: `Owned DNS setup · ${containerName}`,
    })
  }
  for (const blocker of view.adblock) {
    if (!blocker.container) continue
    add(blocker.container, {
      label: blocker.name,
      product: KIND_PRODUCT[blocker.kind] ?? "docker",
      detail: blocker.container,
    })
  }
  for (const listener of view.listeners) {
    if (!listener.container) continue
    add(listener.container, {
      label: listener.container,
      product: KIND_PRODUCT[listener.kind] ?? "docker",
    })
  }
  return [...sources, ...containers.values()]
}
