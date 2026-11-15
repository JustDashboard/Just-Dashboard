import type { VPNView } from "@/lib/types"

/**
 * The VPN page's views, and which one an address opens. The page was every
 * kind of VPN down one column: each tunnel with its picture, then Tailscale
 * with its offers and machines, then Headscale's nodes, then the log. Each
 * kind is a view named in the address (`?tab=`) now, so a link opens the
 * tailnet rather than the top of everything.
 */

export const VPN_VIEWS = ["wireguard", "tailscale", "headscale", "log"] as const
export type VPNViewKey = (typeof VPN_VIEWS)[number]

export const VPN_VIEW_NAME: Record<VPNViewKey, string> = {
  wireguard: "WireGuard",
  tailscale: "Tailscale",
  headscale: "Headscale",
  log: "Log",
}

type Kinds = { headscale: Pick<VPNView["headscale"], "installed" | "container"> }

/** Headscale has a view only where this server runs one. */
export function runsHeadscale(vpn: Kinds) {
  return Boolean(vpn.headscale.installed || vpn.headscale.container)
}

/** The views in the strip, in order. */
export function vpnViews(headscale: boolean): VPNViewKey[] {
  return VPN_VIEWS.filter((view) => view !== "headscale" || headscale)
}

/**
 * The view an address opens: its `?tab=` where this server has that view,
 * else WireGuard, the VPN the dashboard runs and the page's first block when
 * it was one column. A range carried from Address planning
 * (`ipamReservation`) opens the form that makes a tunnel, which is
 * WireGuard's, whatever else the address says.
 */
export function vpnViewOf(
  tab: string | null | undefined,
  headscale: boolean,
  reservation = false,
): VPNViewKey {
  if (reservation) return "wireguard"
  return vpnViews(headscale).find((view) => view === tab) ?? "wireguard"
}

/** The address of a view: the first one has no `tab`. */
export function vpnHref(view: VPNViewKey) {
  return view === "wireguard" ? "/network/vpn" : `/network/vpn?tab=${view}`
}

type Tone = "running" | "warning" | "stopped"

/**
 * The page's own state beside its name, whichever view is open: how many
 * tunnels are up, and whether this server is on its tailnet. Amber where a
 * tunnel is down or Tailscale is installed and not running; grey where this
 * server has neither.
 */
export function vpnReading(vpn: {
  wireguard: { interfaces: { up: boolean }[] }
  tailscale: Pick<VPNView["tailscale"], "installed" | "running">
}): { tone: Tone; label: string } {
  const tunnels = vpn.wireguard.interfaces
  const up = tunnels.filter((tunnel) => tunnel.up).length
  const words: string[] = []
  if (tunnels.length > 0)
    words.push(
      up === tunnels.length
        ? `${up} ${up === 1 ? "tunnel" : "tunnels"} up`
        : `${up} of ${tunnels.length} tunnels up`,
    )
  if (vpn.tailscale.installed)
    words.push(vpn.tailscale.running ? "on the tailnet" : "off the tailnet")
  if (words.length === 0) return { tone: "stopped", label: "No VPN yet" }
  const troubled = up < tunnels.length || (vpn.tailscale.installed && !vpn.tailscale.running)
  return { tone: troubled ? "warning" : "running", label: words.join(", ") }
}
