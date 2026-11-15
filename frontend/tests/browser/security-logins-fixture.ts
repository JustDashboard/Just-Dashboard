import type { Page, Route } from "@playwright/test"
import { host, snapshot } from "./host-fixture"
import { admin, exposure, iso } from "./network-fixture"

/**
 * What the Logins page and the Security overview read, mocked from the shapes
 * `backend/internal/netsec` writes: one shell held over the tailnet, a login
 * record with a second account and a boot in it, a failed-login record folded
 * to three addresses (one of which the firewall already turns away), sshd's
 * settings, fail2ban's jails, the peers connected now and the access boundary.
 * Addresses are documentation ranges and made-up tailnet ones.
 */

export type Mutation = { method: string; path: string; body: unknown }

export { admin, iso }

/** An account that may read the section and change nothing in it. */
export const viewer = {
  ...admin,
  capabilities: ["read"],
  user: { ...admin.user, username: "viewer", role: "readonly" },
}

/** An administrator whose session was narrowed: it may not end a session. */
export const narrowed = {
  ...admin,
  capabilities: admin.capabilities.filter((capability) => capability !== "destructive"),
}

export const sessions = [
  {
    user: "ubuntu",
    tty: "pts/0",
    from: "100.110.34.9",
    loginTime: iso(40),
    idle: "active",
    pid: 4242,
    isSsh: true,
  },
]

/** A busier server: two addresses, the console, and a session with no idle time or process. */
export const manySessions = [
  ...sessions,
  {
    user: "deploy",
    tty: "pts/1",
    from: "198.51.100.77",
    loginTime: iso(6),
    idle: "00:04",
    pid: 5151,
    isSsh: true,
  },
  { user: "root", tty: "tty1", from: "", loginTime: iso(60 * 30), isSsh: false },
]

export const logins = [
  {
    kind: "login",
    user: "ubuntu",
    tty: "pts/0",
    from: "100.110.34.9",
    loginTime: iso(40),
    active: true,
  },
  {
    kind: "login",
    user: "deploy",
    tty: "pts/1",
    from: "198.51.100.77",
    loginTime: iso(60 * 5),
    endTime: iso(60 * 4),
    duration: "1 hour",
    active: false,
  },
  {
    kind: "boot",
    user: "reboot",
    tty: "",
    from: "6.14.0-37-generic",
    loginTime: iso(60 * 24),
    duration: "1 day",
    active: false,
  },
]

export const failedLogins = [
  {
    kind: "login",
    user: "root",
    tty: "ssh:notty",
    from: "203.0.113.9",
    loginTime: iso(12),
    active: false,
  },
  {
    kind: "login",
    user: "admin",
    tty: "ssh:notty",
    from: "203.0.113.9",
    loginTime: iso(13),
    active: false,
  },
]

export const attackers = {
  attempts: 2330,
  addresses: 3,
  windowHours: 168,
  capped: false,
  since: iso(60 * 24 * 6),
  attackers: [
    {
      address: "203.0.113.9",
      attempts: 2200,
      users: ["root", "admin", "ubuntu"],
      first: iso(60 * 24 * 6),
      last: iso(12),
    },
    {
      address: "198.51.100.4",
      attempts: 112,
      users: ["deploy"],
      first: iso(60 * 8),
      last: iso(70),
    },
    {
      address: "2001:db8:52:7::19",
      attempts: 18,
      users: [],
      first: iso(60 * 30),
      last: iso(60 * 26),
    },
  ],
}

/** ufw refusing by default, open on 22 and 443, and turning the first attacker away by name. */
export const firewall = {
  backend: "ufw",
  available: true,
  enabled: true,
  defaultPolicy: "deny (incoming), allow (outgoing), disabled (routed)",
  policy: { incoming: "deny", outgoing: "allow", routed: "disabled" },
  logging: "off",
  capabilities: {
    editable: true,
    toggle: true,
    defaultPolicy: true,
    logging: true,
    reset: true,
    profiles: true,
  },
  rules: [
    {
      number: 1,
      action: "ALLOW",
      direction: "IN",
      to: "22/tcp",
      from: "Anywhere",
      port: "22",
      protocol: "tcp",
      service: "SSH",
      raw: "22/tcp ALLOW IN Anywhere",
    },
    {
      number: 2,
      action: "ALLOW",
      direction: "IN",
      to: "443/tcp",
      from: "Anywhere",
      port: "443",
      protocol: "tcp",
      service: "HTTPS",
      raw: "443/tcp ALLOW IN Anywhere",
    },
    {
      number: 3,
      action: "DENY",
      direction: "IN",
      to: "Anywhere",
      from: "203.0.113.9",
      comment: "repeat offender",
      raw: "Anywhere DENY IN 203.0.113.9 # repeat offender",
    },
  ],
}

export const jails = {
  available: true,
  running: true,
  jails: [
    {
      name: "sshd",
      currentlyFailed: 3,
      totalFailed: 812,
      currentlyBanned: 2,
      totalBanned: 41,
      bannedIps: ["203.0.113.9", "198.51.100.4"],
      fileList: ["/var/log/auth.log"],
    },
    {
      name: "nginx-http-auth",
      currentlyFailed: 0,
      totalFailed: 0,
      currentlyBanned: 0,
      totalBanned: 0,
      bannedIps: [],
      fileList: ["/var/log/nginx/error.log"],
    },
  ],
}

export const offenders = {
  total: 60,
  bans: 41,
  unbans: 19,
  offenders: [],
  byJail: { sshd: 41 },
  perDay: [
    { day: "2026-11-10", count: 12 },
    { day: "2026-11-11", count: 20 },
    { day: "2026-11-12", count: 9 },
  ],
  since: iso(60 * 24 * 6),
}

export const connections = {
  total: 14,
  listening: 6,
  loopback: 4,
  peers: [
    {
      address: "203.0.113.50",
      count: 3,
      established: 3,
      ports: [443],
      processes: ["caddy"],
      private: false,
      service: "HTTPS",
    },
    {
      address: "100.110.34.9",
      count: 2,
      established: 1,
      ports: [8443],
      processes: ["caddy"],
      private: true,
    },
  ],
}

export const sshd = {
  available: true,
  source: "/usr/sbin/sshd -T",
  ports: ["22"],
  keyedAccounts: [{ user: "ubuntu", keys: 2 }],
  hasMatchBlocks: false,
  settings: [
    {
      key: "permitrootlogin",
      label: "Root login",
      value: "prohibit-password",
      detail: "Whether root may log in over SSH at all.",
      options: ["no", "prohibit-password", "forced-commands-only", "yes"],
      kind: "choice",
    },
    {
      key: "passwordauthentication",
      label: "Password authentication",
      value: "yes",
      detail: "Whether a password alone is enough to get a shell.",
      options: ["no", "yes"],
      kind: "choice",
    },
  ],
}

export const boundary = {
  checkedAt: iso(0),
  allowlist: ["127.0.0.1/32", "100.64.0.0/10"],
  client: "100.110.34.9",
  caddyPort: 8443,
  sshPorts: ["22"],
  tailnetIp: "100.110.34.31",
  previewMin: 21000,
  previewMax: 21999,
  checks: [
    {
      id: "ingress",
      state: "held",
      title: "Caddy is the only routable listener",
      detail: "Caddy holds port 8443 on 100.110.34.31:8443, 127.0.0.1:8443.",
    },
    {
      id: "allowlist",
      state: "held",
      title: "The allowlist admits this session before sign-in",
      detail: "100.110.34.9 is inside the allowlist that runs before authentication.",
    },
    {
      id: "tailnet",
      state: "held",
      title: "The tailnet path is up",
      detail: "tailscale0 is up with 100.110.34.31.",
    },
    {
      id: "ssh",
      state: "held",
      title: "SSH answers for a tunnel",
      detail: "Port 22 is listening.",
    },
    {
      id: "previews",
      state: "broken",
      title: "Previews stay tailnet-only",
      detail:
        "Preview ports outside the boundary: 21001 is funnelled or serves something other than a loopback port.",
    },
  ],
}

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

/** An error as the API writes one. */
export const failure = (status: number, code: string, message: string) => ({
  __status: status,
  error: { code, message },
})

/** A reply that never arrives, for the page as it stands while it waits. */
export const PENDING = { __pending: true }

/**
 * The reads of both pages, answered from the fixtures above; every write is
 * recorded and answered as done. `overrides` swaps one path's answer: a body,
 * a `failure(...)`, or `PENDING` to leave the request hanging.
 */
export async function mockLogins(
  page: Page,
  mutations: Mutation[] = [],
  options: { session?: typeof admin; overrides?: Record<string, unknown> } = {},
) {
  const answers: Record<string, unknown> = {
    "/auth/session": options.session ?? admin,
    "/system/host": host,
    "/system/metrics": snapshot,
    "/exposure": exposure,
    "/firewall/": firewall,
    "/fail2ban/": jails,
    "/fail2ban/offenders": offenders,
    "/connections": connections,
    "/ssh-sessions": sessions,
    "/logins": logins,
    "/logins/failed": failedLogins,
    "/logins/attackers": attackers,
    "/ssh/config": sshd,
    "/security/boundary": boundary,
    "/updates/self": { current: "0.8.7", latest: "0.8.7" },
    ...options.overrides,
  }
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname.replace(/^\/api\/v1/, "")
    if (request.method() !== "GET") {
      mutations.push({ method: request.method(), path, body: request.postDataJSON() })
      return json(route, { output: "ok" })
    }
    const answer = answers[path]
    if (answer === undefined) return json(route, [])
    if (answer === PENDING) return
    const status = (answer as { __status?: number }).__status
    if (status) return json(route, { error: (answer as { error: unknown }).error }, status)
    return json(route, answer)
  })
}
