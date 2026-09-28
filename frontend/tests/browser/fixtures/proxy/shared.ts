import type { Route } from "@playwright/test"

/** What every proxy route table is built from, and the helpers each one uses. */

export const now = new Date().toISOString()
export const inThirtyDays = new Date(Date.now() + 30 * 86_400_000).toISOString()
export const yesterday = new Date(Date.now() - 86_400_000).toISOString()

export const user = {
  authenticated: true,
  needsTotp: false,
  needsEnrollment: false,
  require2fa: false,
  capabilities: [
    "read",
    "service.control",
    "file.write",
    "terminal",
    "destructive",
    "system.admin",
  ],
  user: {
    id: 1,
    username: "operator",
    role: "admin",
    totpEnabled: true,
    disabled: false,
    mustChangePassword: false,
    lastLoginAt: now,
    createdAt: now,
  },
}

export async function json(route: Route, body: unknown) {
  await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) })
}

export type ProxyMockOptions = {
  /** Whether nginx.conf includes the stream directory. */
  included: boolean
}

/**
 * One area's API answers, by path with /api/v1 and the query stripped. Each
 * area keeps its own table, so a new endpoint is a line in that area's file.
 */
export type ProxyRoutes = Record<
  string,
  (route: Route, options: ProxyMockOptions) => Promise<void> | void
>
