import type { Page, Route } from "@playwright/test"

/**
 * A mocked host with enough of a trail for the audit log's every mark to have
 * something to draw: a person, an API key, the assistant, a session Just Hub
 * opened, a webhook, the dashboard itself and a caller with no name; changes
 * that went through and ones that were refused; rows with a path, a target, a
 * detail and a key, and rows with none of them.
 */

const HOUR = 3_600_000

export type Entry = {
  id: number
  ts: string
  userId: number
  username: string
  role: string
  ip: string
  actor: string
  action: string
  target: string
  method: string
  path: string
  status: number
  success: boolean
  detail: string
  keyId?: number
  keyName?: string
  sessionId?: string
  hubUser?: string
}

export const session = (capabilities: string[], role = "admin") => ({
  authenticated: true,
  needsTotp: false,
  needsEnrollment: false,
  require2fa: false,
  capabilities,
  user: {
    id: 1,
    username: "operator",
    role,
    totpEnabled: true,
    disabled: false,
    mustChangePassword: false,
    lastLoginAt: new Date().toISOString(),
    createdAt: new Date().toISOString(),
  },
})

export const ADMIN = [
  "read",
  "service.control",
  "file.write",
  "terminal",
  "destructive",
  "system.admin",
]

/** The trail, newest first, dated from the moment it is asked for. */
export function trail(now = Date.now()): Entry[] {
  const ago = (hours: number) => new Date(now - hours * HOUR).toISOString()
  let id = 100
  const entry = (fields: Partial<Entry> & Pick<Entry, "ts" | "action">): Entry => ({
    id: id--,
    userId: 1,
    username: "operator",
    role: "admin",
    ip: "100.64.1.2",
    actor: "session",
    target: "",
    method: "POST",
    path: "",
    status: 200,
    success: true,
    detail: "",
    ...fields,
  })
  return [
    entry({
      ts: ago(0.1),
      action: "docker.container.restart",
      target: "web",
      path: "/api/v1/docker/containers/web/restart",
    }),
    entry({
      ts: ago(0.5),
      actor: "webhook",
      username: "",
      userId: 0,
      ip: "140.82.115.4",
      action: "deploy.run",
      target: "14",
      path: "/api/v1/hooks/github-app",
    }),
    entry({
      ts: ago(1),
      actor: "system",
      username: "",
      userId: 0,
      ip: "",
      method: "",
      status: 0,
      action: "proxy.ingress.reconcile",
      target: "jd-ingress",
    }),
    entry({
      ts: ago(2),
      actor: "anonymous",
      username: "admin",
      userId: 0,
      ip: "45.12.3.4",
      action: "auth.login",
      path: "/api/v1/auth/login",
      status: 401,
      success: false,
      detail: "invalid credentials",
    }),
    entry({ ts: ago(2.2), action: "auth.login", path: "/api/v1/auth/login" }),
    entry({
      ts: ago(3),
      actor: "token",
      username: "mira",
      userId: 2,
      role: "operator",
      keyId: 12,
      keyName: "ci-deploy",
      ip: "192.168.1.20",
      method: "PUT",
      action: "file.write",
      target:
        "/etc/nginx/sites-available/a-rather-long-name-for-a-site-configuration.example.test.conf",
      path: "/api/v1/files/write",
    }),
    entry({
      ts: ago(4),
      action: "system.packages.install",
      target: "htop",
      path: "/api/v1/packages/install",
    }),
    entry({
      ts: ago(5),
      action: "git.clone",
      target: "acme/api",
      path: "/api/v1/git/clone",
    }),
    entry({
      ts: ago(7),
      actor: "assistant",
      action: "systemd.unit.restart",
      target: "nginx.service",
      path: "/api/v1/systemd/units/nginx.service/restart",
      detail: "asked for in the assistant's chat",
    }),
    entry({
      ts: ago(9),
      actor: "hub-session",
      username: "dana",
      userId: 3,
      sessionId: "hs_4f2a",
      hubUser: "dana@hub",
      ip: "203.0.113.9",
      action: "terminal.create",
      path: "/api/v1/terminal/sessions",
    }),
    entry({
      ts: ago(26),
      method: "DELETE",
      action: "database.row.delete",
      target: "app.users",
      path: "/api/v1/databases/2/rows",
      status: 403,
      success: false,
      detail: "missing capability destructive",
    }),
    entry({
      ts: ago(50),
      action: "firewall.rule.add",
      target: "allow 443/tcp",
      path: "/api/v1/firewall/rules",
    }),
  ]
}

/** The audit query, answered the way the server's filter would. */
export function answer(entries: Entry[], url: URL) {
  const since = url.searchParams.get("since")
  const action = url.searchParams.get("action") ?? ""
  const user = url.searchParams.get("username") ?? ""
  const failed = url.searchParams.get("failed") === "true"
  const matching = entries.filter(
    (e) =>
      (!since || e.ts >= since) &&
      e.action.includes(action) &&
      e.username.includes(user) &&
      (!failed || !e.success),
  )
  const limit = Number(url.searchParams.get("limit") ?? 100)
  const offset = Number(url.searchParams.get("offset") ?? 0)
  return { entries: matching.slice(offset, offset + limit), total: matching.length }
}

export async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

export const host = {
  hostname: "atlas",
  os: "linux",
  platform: "ubuntu",
  platformVersion: "24.04",
  kernelVersion: "6.8.0",
  kernelArch: "x86_64",
  virtualization: "kvm",
  bootTime: new Date(Date.now() - 40 * 24 * HOUR).toISOString(),
  uptimeSeconds: 40 * 24 * 3600,
  processes: 180,
  cpuModel: "AMD EPYC",
  cpuCores: 4,
  cpuMhz: 2400,
}

export async function mockAudit(
  page: Page,
  {
    entries = trail(),
    capabilities = ADMIN,
    role = "admin",
    audit,
  }: {
    entries?: Entry[]
    capabilities?: string[]
    role?: string
    /** Answers the audit route in the fixture's place: a failure, a wait, a refusal. */
    audit?: (route: Route, url: URL) => Promise<unknown> | undefined
  } = {},
) {
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    if (path === "/auth/session") return json(route, session(capabilities, role))
    if (path === "/updates/self") return json(route, { current: "0.8.7", latest: "0.8.7" })
    if (path === "/system/host") return json(route, host)
    if (path === "/audit/") {
      const own = audit?.(route, url)
      if (own) return own
      return json(route, answer(entries, url))
    }
    return json(route, [])
  })
}

export const forbidden = (route: Route) =>
  json(route, { error: { code: "forbidden", message: "forbidden" } }, 403)
