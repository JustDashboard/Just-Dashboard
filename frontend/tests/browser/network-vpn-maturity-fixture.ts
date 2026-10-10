import { type Page } from "@playwright/test"
import { json, mockNetwork, vpn, type Mutation } from "./network-fixture"

// Recorded API shapes for the WireGuard record, peer lifecycle, site checks,
// archive and the tailnet's approval on the VPN page.

export const now = () => Math.floor(Date.now() / 1000)
export const base = vpn.wireguard.interfaces[0]
export const peerNamed = (name: string) => base.peers.find((p) => p.name === name)!

export function maturityVPN() {
  const office = peerNamed("Office")
  const backup = peerNamed("Backup server")
  const peers = base.peers.map((p) => {
    switch (p.name) {
      case "Ana's phone":
        return { ...p, handshakeState: "online", clientRoutes: ["10.8.0.0/24"] }
      case "Work laptop":
        return { ...p, handshakeState: "online", clientRoutes: ["0.0.0.0/0", "::/0"] }
      case "Tablet":
        return { ...p, handshakeState: "idle", clientRoutes: ["10.8.0.0/24", "172.17.0.0/16"] }
      case "Office":
        return {
          ...p,
          online: false,
          endpoint: "198.51.100.50:51820",
          latestHandshake: now() - 900,
          handshakeState: "stale",
          transport: {
            endpoint: "198.51.100.50:51820",
            state: "native",
            device: "eth0",
            checkedAt: now() - 60,
          },
          quota: {
            period: "month",
            limitBytes: 2 * 2 ** 30,
            usedBytes: 3 * 2 ** 30,
            periodStart: now() - 86400 * 8,
            state: "exceeded",
            enforced: false,
          },
        }
      case "Backup server":
        return {
          ...p,
          endpoint: "172.20.0.9:51820",
          handshakeState: "never",
          transport: {
            endpoint: "172.20.0.9:51820",
            state: "captured",
            device: "wg0",
            reason:
              "the route to 172.20.0.9 goes into the WireGuard device wg0, so the tunnel would carry its own transport",
            checkedAt: now() - 120,
          },
        }
    }
    return { ...p, handshakeState: p.online ? "online" : "idle" }
  })
  const family = (subnet: string, uplink: string, translated: number) => ({
    configured: true,
    subnet,
    runtime: "present",
    exit: {
      configured: true,
      interface: uplink,
      runtime: "verified",
      translated,
      capability: { writable: true, firewall: "nftables", docker: false },
    },
  })
  return {
    ...vpn,
    wireguard: {
      ...vpn.wireguard,
      interfaces: [
        {
          ...base,
          peers,
          families: {
            ipv4: family("10.8.0.0/24", "eth0", 1234),
            ipv6: {
              configured: false,
              runtime: "not_configured",
              exit: {
                configured: false,
                runtime: "disabled",
                capability: {
                  writable: false,
                  reason: "No opted-in address allocation exists for this family.",
                },
              },
            },
          },
          alerts: [
            {
              kind: "stale_handshake",
              peer: office.publicKey,
              peerName: "Office",
              since: now() - 900,
              message:
                "Office keeps its session alive but has not completed a handshake for over 3 minutes",
            },
            {
              kind: "transport_captured",
              peer: backup.publicKey,
              peerName: "Backup server",
              since: now() - 120,
              message:
                "Backup server: the route to 172.20.0.9 goes into the WireGuard device wg0, so the tunnel would carry its own transport",
            },
            {
              kind: "quota_exceeded",
              peer: office.publicKey,
              peerName: "Office",
              since: now() - 86400 * 8,
              message:
                "Office has passed its month budget; the budget alerts and does not disconnect",
            },
          ],
        },
      ],
    },
    tailscale: {
      ...vpn.tailscale,
      self: { ...vpn.tailscale.self, keyExpiry: now() + 86400 * 5 },
      health: ["Tailscale could not reach the coordination server for a while."],
      warnings: [
        "This server's Tailscale key expires on 14 October 2026. When it does the server leaves the tailnet, and a dashboard reached through it goes with it: disable key expiry for this machine on the control server, or reauthenticate it from a shell before then.",
      ],
      prefs: { ...vpn.tailscale.prefs, advertiseRoutes: ["10.0.4.0/24", "192.168.60.0/24"] },
      approval: {
        exitNode: "not_serving",
        routes: [
          { route: "10.0.4.0/24", state: "serving" },
          { route: "192.168.60.0/24", state: "not_serving" },
        ],
      },
    },
    headscale: {
      installed: false,
      container: "headscale",
      nodes: [
        {
          id: "7",
          name: "router",
          givenName: "bob-router",
          ipAddresses: ["100.64.0.7"],
          online: true,
          lastSeen: now() - 10,
          user: "bob",
          expiry: now() - 3600,
          availableRoutes: ["192.168.20.0/24", "192.168.30.0/24"],
          approvedRoutes: ["192.168.20.0/24"],
          routesKnown: true,
        },
      ],
      users: [{ id: "2", name: "bob", nodes: 1 }],
    },
  }
}

export function history(peer?: string) {
  const t = now()
  return {
    iface: "wg0",
    peer,
    recording: true,
    window: 86400,
    bucket: 300,
    points: Array.from({ length: 12 }, (_, i) => ({
      t: t - (12 - i) * 300,
      rx: 1000 + i * 200,
      tx: 400 + i * 50,
    })),
    endpoints: peer
      ? [
          {
            endpoint: "198.51.100.50:51820",
            firstSeen: t - 86400,
            lastSeen: t - 900,
            observations: 280,
          },
          {
            endpoint: "203.0.113.77:40001",
            firstSeen: t - 86400 * 3,
            lastSeen: t - 86400,
            observations: 12,
          },
        ]
      : [],
    events: [
      {
        id: 9,
        iface: "wg0",
        at: t - 900,
        kind: "handshake_stale",
        outcome: "degraded",
        peer,
        peerName: "Office",
        detail: "no handshake since 2026-10-09T10:00:00Z",
      },
      {
        id: 3,
        iface: "wg0",
        at: t - 86400 * 2,
        kind: "peer_add_failed",
        outcome: "failed",
        actor: "admin",
        detail: "reloading wg-quick@wg0: exit status 1; rolled back",
      },
      {
        id: 1,
        iface: "wg0",
        at: t - 86400 * 12,
        kind: "created",
        outcome: "ok",
        actor: "admin",
        detail: "udp 51820, 10.8.0.0/24",
      },
    ],
    usage: [],
  }
}

export async function openMaturity(
  page: Page,
  mutations: Mutation[],
  extra: Record<string, unknown> = {},
) {
  await mockNetwork(page, mutations, {
    overrides: {
      "/network/vpn": maturityVPN(),
      "/network/vpn/wireguard/wg0/history": history(),
      ...extra,
    },
  })
  // A peer's own record is asked with its key; answer with that peer's.
  await page.route("**/api/v1/network/vpn/wireguard/wg0/history?*", (route) => {
    const url = new URL(route.request().url())
    mutations.push({ method: "GET", path: `history?${url.searchParams.toString()}`, body: null })
    return json(route, history(url.searchParams.get("peer") ?? undefined))
  })
}
