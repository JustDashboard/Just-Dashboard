import { inThirtyDays, json, now, yesterday, type ProxyRoutes } from "./shared"

export const certs = [
  {
    name: "app.example.com",
    path: "/etc/letsencrypt/live/app.example.com/fullchain.pem",
    domains: ["app.example.com"],
    issuer: "R11",
    notBefore: yesterday,
    notAfter: inThirtyDays,
    daysLeft: 29,
    expired: false,
    expiring: true,
    selfSigned: false,
    source: "certbot",
    usedBy: ["app.example.com"],
  },
  {
    name: "old.example.com",
    path: "/etc/ssl/just-dashboard/old.example.com/fullchain.pem",
    domains: ["old.example.com"],
    issuer: "R11",
    notBefore: yesterday,
    notAfter: yesterday,
    daysLeft: -1,
    expired: true,
    expiring: false,
    selfSigned: false,
    source: "imported",
    usedBy: [],
  },
]

/** certbot's state on the mocked host, with whatever a test needs changed. */
export function certbotState(overrides: Record<string, unknown> = {}) {
  return {
    available: true,
    version: "certbot 2.9.0",
    certs: [
      {
        name: "app.example.com",
        domains: ["app.example.com"],
        expiry: inThirtyDays,
        daysLeft: 29,
        valid: true,
      },
    ],
    autoRenew: false,
    renewUnit: "certbot.timer",
    ...overrides,
  }
}

/** A certbot job as the API answers one: running unless a test says otherwise. */
export function certbotJob(overrides: Record<string, unknown> = {}) {
  return {
    id: "job-1",
    kind: "certbot.renew",
    title: "Renewing app.example.com",
    target: "app.example.com",
    status: "running",
    exitCode: 0,
    startedAt: now,
    startedBy: "operator",
    lines: 0,
    ...overrides,
  }
}

export const routes: ProxyRoutes = {
  "/certificates/": (route) => json(route, certs),
  "/certificates/certbot": (route) => json(route, certbotState()),
  "/certificates/dns-providers": (route) => json(route, []),
}

export const showcase: ProxyRoutes = {
  "/certificates/dns-providers": (route) =>
    json(route, [
      {
        key: "cloudflare",
        name: "Cloudflare",
        plugin: "dns-cloudflare",
        installed: true,
        hasCredentials: true,
        defaultWait: 30,
      },
    ]),
}
