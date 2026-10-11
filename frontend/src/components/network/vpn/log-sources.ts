import type { VPNView } from "@/lib/types"
import { dockerSource, journalSource } from "@/lib/log-sources"
import type { ServiceLogSource } from "@/components/logs/service-logs"

/**
 * Where the tunnels write, from what the page already reads: each WireGuard
 * tunnel's unit where systemd brings it up from a file (`wg-quick@wg0` runs
 * once, at boot or on a press, and says why a tunnel did not come up);
 * tailscaled where Tailscale is installed; and Headscale's container, which
 * is the only form of it the page finds. The server detects each source's
 * lens: none of these has one of its own.
 */
export function vpnLogSources({ wireguard, tailscale, headscale }: VPNView): ServiceLogSource[] {
  const sources: ServiceLogSource[] = []
  if (wireguard.systemd) {
    for (const tunnel of wireguard.interfaces) {
      if (!tunnel.configured) continue
      const unit = `wg-quick@${tunnel.name}.service`
      sources.push({
        id: journalSource(unit),
        label: unit,
        kind: "journal",
        product: "wireguard",
      })
    }
  }
  if (tailscale.installed) {
    sources.push({
      id: journalSource("tailscaled.service"),
      label: "tailscaled.service",
      kind: "journal",
      product: "tailscale",
    })
  }
  if (headscale.installed && headscale.container) {
    sources.push({
      id: dockerSource(headscale.container),
      label: headscale.container,
      kind: "docker",
      product: "headscale",
    })
  }
  return sources
}
