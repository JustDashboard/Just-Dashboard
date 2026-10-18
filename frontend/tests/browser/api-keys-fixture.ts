import type { Page, Route } from "@playwright/test"
import type { ApiToken, DashboardUser, Role } from "../../src/lib/types"

const now = Date.now()
const DAY = 86_400_000
const iso = (days: number) => new Date(now + days * DAY).toISOString()

export const keyOwner: DashboardUser = {
  id: 1,
  username: "ion",
  displayName: "Ion Moisei",
  avatarVersion: 0,
  role: "admin",
  totpEnabled: true,
  disabled: false,
  mustChangePassword: false,
  needsOnboarding: false,
  hiddenSections: [],
  createdAt: iso(-200),
  lastLoginAt: iso(-1),
}
const otherOwner: DashboardUser = {
  ...keyOwner,
  id: 2,
  username: "maria",
  displayName: "Maria Rusu",
}

export const keyTokens: ApiToken[] = [
  {
    id: 1,
    userId: 1,
    name: "github-actions",
    prefix: "vpsd_ab12cd34",
    role: "limited",
    createdAt: iso(-60),
    expiresAt: iso(5),
    lastUsedAt: iso(-0.02),
    revoked: false,
  },
  {
    id: 2,
    userId: 1,
    name: "backup-cron",
    prefix: "vpsd_cd34ef56",
    role: "readonly",
    createdAt: iso(-20),
    revoked: false,
  },
  {
    id: 3,
    userId: 2,
    name: "grafana",
    prefix: "vpsd_ef56ab12",
    role: "readonly",
    createdAt: iso(-12),
    expiresAt: iso(200),
    lastUsedAt: iso(-1),
    revoked: false,
  },
  {
    id: 4,
    userId: 1,
    name: "old-laptop",
    prefix: "vpsd_0000abcd",
    role: "admin",
    createdAt: iso(-300),
    revoked: true,
  },
  {
    id: 5,
    userId: 1,
    name: "ansible",
    prefix: "vpsd_ac57bd68",
    role: "limited",
    createdAt: iso(-80),
    expiresAt: iso(70),
    lastUsedAt: iso(-35),
    revoked: false,
  },
  {
    id: 6,
    userId: 1,
    name: "uptime-kuma",
    prefix: "vpsd_abcd0000",
    role: "readonly",
    createdAt: iso(-100),
    expiresAt: iso(-2),
    lastUsedAt: iso(-3),
    revoked: false,
  },
]

export async function mockKeys(page: Page, role: Role = "admin", initial = keyTokens) {
  let tokens = structuredClone(initial).filter((token) => role === "admin" || token.userId === 1)
  const calls: { method: string; path: string; body: unknown }[] = []
  let sequence = 100
  const json = (route: Route, body: unknown, status = 200) =>
    route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname.replace(/^\/api\/v1/, "")
    const method = request.method()
    if (path === "/auth/session")
      return json(route, {
        authenticated: true,
        needsTotp: false,
        needsEnrollment: false,
        require2fa: false,
        user: { ...keyOwner, role },
        capabilities:
          role === "admin"
            ? ["read", "service.control", "file.write", "terminal", "destructive", "system.admin"]
            : role === "limited"
              ? ["read", "service.control", "file.write"]
              : ["read"],
      })
    if (path === "/dashboard-users/") return json(route, [keyOwner, otherOwner])
    if (path === "/tokens/" && method === "GET") return json(route, tokens)
    if (path.startsWith("/tokens/")) {
      const body = request.postData() ? request.postDataJSON() : undefined
      calls.push({ method, path, body })
      if (method === "POST" && path === "/tokens/") {
        const token: ApiToken = {
          id: ++sequence,
          userId: 1,
          prefix: "vpsd_synthetic",
          name: body.name,
          role: body.scope ? "readonly" : body.role,
          scope: body.scope ?? "",
          createdAt: new Date().toISOString(),
          expiresAt: body.ttlDays ? iso(body.ttlDays) : undefined,
          revoked: false,
        }
        tokens.push(token)
        return json(route, { token, secret: `vpsd_synthetic_key_${sequence}` }, 201)
      }
      const id = Number(path.split("/")[2])
      if (method === "DELETE" && path.endsWith("/record"))
        tokens = tokens.filter((token) => token.id !== id)
      else if (method === "DELETE")
        tokens = tokens.map((token) => (token.id === id ? { ...token, revoked: true } : token))
      return route.fulfill({ status: 204 })
    }
    return json(route, [])
  })
  return { calls }
}
