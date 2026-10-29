import { json, now, type ProxyRoutes } from "./shared"

/** Cloudflare Tunnel (`GET /proxy/tunnel`): none connected, and one carrying three names. */

const tunnelId = "6a1b9c4e-2f3d-4a5b-8c7d-9e0f1a2b3c4d"
const origin = {
  service: "http://just-dashboard-ingress:80",
  network: "just-dashboard-tunnel",
  proxy: "the Caddy ingress just-dashboard-ingress",
}

export const disconnectedTunnel = {
  connected: false,
  connector: { state: "missing", version: "2025.10.0" },
  ready: false,
  readyConnections: 0,
  api: { available: true },
  origin,
  hostnames: [],
}

export const connectedTunnel = {
  connected: true,
  connection: {
    mode: "api",
    tunnelId,
    tunnelName: "production-edge",
    accountId: "0123456789abcdef0123456789abcdef",
    createdBy: "operator",
    createdAt: now,
    updatedAt: now,
  },
  connector: {
    state: "running",
    version: "2025.10.0",
    address: "172.20.0.2",
    startedAt: now,
    origin: origin.service,
  },
  ready: true,
  readyConnections: 4,
  checkedAt: now,
  api: { available: true },
  origin,
  hostnames: [
    {
      hostname: "shop.example.com",
      projectId: 7,
      environmentId: 12,
      project: "shop",
      routed: true,
      dns: "created",
      checkedAt: now,
    },
    {
      hostname: "status.internal-tools-for-the-operations-team.example.com",
      projectId: 9,
      environmentId: 15,
      project: "status-page",
      routed: true,
      dns: "present",
      checkedAt: now,
    },
    {
      hostname: "www.example.com",
      projectId: 7,
      environmentId: 12,
      project: "shop",
      routed: true,
      dns: "error",
      error:
        "www.example.com already has a A record to 203.0.113.7 in Cloudflare, which is not this dashboard's; remove it, and the next pass points the name at the tunnel",
      checkedAt: now,
    },
  ],
  routes: [
    { hostname: "shop.example.com", service: origin.service },
    {
      hostname: "status.internal-tools-for-the-operations-team.example.com",
      service: origin.service,
    },
    { hostname: "www.example.com", service: origin.service },
    { service: "http_status:404" },
  ],
  syncedAt: now,
}

export const routes: ProxyRoutes = {
  "/proxy/tunnel": (route) => json(route, disconnectedTunnel),
}

export const showcase: ProxyRoutes = {
  "/proxy/tunnel": (route) => json(route, connectedTunnel),
}
