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

/**
 * A certificate a staging authority signed, as the inventory lists one:
 * certbot's own, served by a site, with eighty days that mean nothing.
 */
export function stagingCertificate(overrides: Record<string, unknown> = {}) {
  return {
    name: "test.example.com",
    path: "/etc/letsencrypt/live/test.example.com/fullchain.pem",
    domains: ["test.example.com", "www.test.example.com"],
    issuer: "(STAGING) Riddling Rhubarb R12",
    notBefore: yesterday,
    notAfter: new Date(Date.now() + 80 * 86_400_000).toISOString(),
    daysLeft: 80,
    expired: false,
    expiring: false,
    selfSigned: false,
    source: "certbot",
    usedBy: ["test.example.com"],
    staging: true,
    ...overrides,
  }
}

/** One certbot lineage as the API reads it, with whatever a test needs changed. */
export function certbotLineage(overrides: Record<string, unknown> = {}) {
  return {
    name: "app.example.com",
    domains: ["app.example.com"],
    notBefore: new Date(Date.parse(inThirtyDays) - 90 * 86_400_000).toISOString(),
    expiry: inThirtyDays,
    daysLeft: 29,
    valid: true,
    certPath: "/etc/letsencrypt/live/app.example.com/fullchain.pem",
    authenticator: "nginx",
    installer: "nginx",
    ...overrides,
  }
}

/** certbot's state on the mocked host, with whatever a test needs changed. */
export function certbotState(overrides: Record<string, unknown> = {}) {
  return {
    available: true,
    version: "certbot 2.9.0",
    certs: [certbotLineage()],
    autoRenew: false,
    renewUnit: "certbot.timer",
    nginxReloads: true,
    reloadHook: reloadHook(),
    runtime: { onHost: true, plugins: ["nginx", "standalone", "webroot"] },
    ...overrides,
  }
}

/** The renewal deploy hook's state. */
export function reloadHook(state = "missing", others: string[] = []) {
  return {
    path: "/etc/letsencrypt/renewal-hooks/deploy/50-just-dashboard-reload-nginx",
    state,
    others,
  }
}

const hour = 3_600_000

/**
 * The renewal record as this host had it: certbot.timer active, the last run
 * of certbot.service an hour ago failing on the lineage, the next in eleven.
 */
export function renewalHealth(overrides: Record<string, unknown> = {}) {
  return {
    source: "certbot.service",
    service: "certbot.service",
    state: "failed",
    lastRun: new Date(Date.now() - hour).toISOString(),
    nextRun: new Date(Date.now() + 11 * hour).toISOString(),
    exitStatus: 1,
    failures: [{ lineage: "app.example.com", reason: "Some challenges have failed." }],
    ...overrides,
  }
}

/** certbot.service's journal for its last two runs, newest first. */
export function renewalLog() {
  const run = (hoursAgo: number) => {
    const at = (ms: number) => new Date(Date.now() - hoursAgo * hour + ms).toISOString()
    return {
      start: at(0),
      result: "failed",
      lines: [
        { time: at(0), text: "Starting certbot.service - Certbot...", systemd: true },
        {
          time: at(6000),
          text: "Failed to renew certificate app.example.com with error: Some challenges have failed.",
          error: true,
        },
        { time: at(6001), text: "1 renew failure(s), 0 parse failure(s)" },
        {
          time: at(6100),
          text: "certbot.service: Failed with result 'exit-code'.",
          systemd: true,
        },
      ],
    }
  }
  return { source: "certbot.service", runs: [run(1), run(13)] }
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

/**
 * What a site naming a certificate answered over a handshake: still the test
 * certificate unless a test says otherwise.
 */
export function servedCertificate(overrides: Record<string, unknown> = {}) {
  return {
    site: "test.example.com",
    name: "test.example.com",
    address: "127.0.0.1:443",
    issuer: "(STAGING) Riddling Rhubarb R12",
    serial: "0A:1B",
    staging: true,
    current: false,
    ...overrides,
  }
}

export const routes: ProxyRoutes = {
  "/certificates/": (route) => json(route, certs),
  "/certificates/certbot": (route) => json(route, certbotState()),
  "/certificates/dns-providers": (route) => json(route, []),
  "/certificates/served": (route) => json(route, []),
  "/certificates/findings": (route) => json(route, { findings: [], config: "nginx -T" }),
  "/certificates/coverage": (route) => json(route, { names: [], unused: [], config: "nginx -T" }),
  "/certificates/renewal/log": (route) => json(route, renewalLog()),
  "/certificates/renewal-hook": (route) =>
    json(route, reloadHook(route.request().method() === "DELETE" ? "missing" : "installed")),
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
