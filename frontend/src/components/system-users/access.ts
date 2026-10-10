import { hueFor, LANES } from "@/lib/hue"
import type { SystemUser } from "@/lib/types"
import { isAdmin } from "@/components/system-users/marks"

/** The windows the sign-in axis can span, shortest first. */
export const WINDOWS = [
  { key: "24h", label: "24h", ms: 24 * 3600_000 },
  { key: "7d", label: "7d", ms: 7 * 24 * 3600_000 },
  { key: "30d", label: "30d", ms: 30 * 24 * 3600_000 },
] as const

export type WindowKey = (typeof WINDOWS)[number]["key"]

/** The colour an account has everywhere on the page: its initials' hue. */
export function accountHue(username: string) {
  return hueFor(username.toLowerCase(), LANES)
}

/** An account that can be signed into without any password at all. */
export function isBare(user: Pick<SystemUser, "noPassword" | "locked">) {
  return user.noPassword && !user.locked
}

/**
 * What the line's verdict says: how many accounts can be signed into with no
 * password, and how many of those are administrators — root for anyone who
 * reaches the console. `scope` is the filter a press narrows the cards to,
 * which holds exactly the accounts counted.
 */
export function verdict(users: SystemUser[]): {
  tone: "warning" | "running"
  count: number
  admins: number
  scope?: "bare"
} {
  const bare = users.filter((u) => u.canLogin && isBare(u))
  if (bare.length === 0) return { tone: "running", count: 0, admins: 0 }
  return {
    tone: "warning",
    count: bare.length,
    admins: bare.filter(isAdmin).length,
    scope: "bare",
  }
}

export type RootRoute = { label: string; kind: "sudo" | "docker" | "root" }

/**
 * The ways an account can become root. `docker` counts: a member can mount the
 * host's root filesystem into a container, which is root by another name.
 */
export function rootRoutes(user: Pick<SystemUser, "groups" | "uid">): RootRoute[] {
  const routes: RootRoute[] = []
  if (user.uid === 0) routes.push({ label: "uid 0", kind: "root" })
  for (const group of user.groups) {
    if (group === "sudo" || group === "wheel" || group === "admin") {
      routes.push({ label: group, kind: "sudo" })
    }
  }
  if (user.groups.includes("docker")) routes.push({ label: "docker", kind: "docker" })
  return routes
}

/**
 * Everyone who can reach root, the accounts that need no password first and
 * the locked ones last — a locked account holds the power but cannot use it.
 */
export function rootReach(users: SystemUser[]) {
  const rank = (u: SystemUser) => (isBare(u) ? 0 : u.locked ? 2 : 1)
  return users
    .map((user) => ({ user, routes: rootRoutes(user) }))
    .filter((entry) => entry.routes.length > 0)
    .sort((a, b) => rank(a.user) - rank(b.user) || a.user.username.localeCompare(b.user.username))
}

export type SignIn = { user: SystemUser; at: number }

/**
 * Each account's last sign-in, newest first, split by whether it falls inside
 * the window. `never` counts only accounts that could have: a daemon with no
 * shell never signing in is not news.
 */
export function signIns(users: SystemUser[], now: number, windowMs: number) {
  const all: SignIn[] = users
    .filter((u) => u.lastLogin)
    .map((user) => ({ user, at: new Date(user.lastLogin as string).getTime() }))
    .filter((entry) => !Number.isNaN(entry.at))
    .sort((a, b) => b.at - a.at)
  return {
    within: all.filter((entry) => now - entry.at <= windowMs),
    older: all.filter((entry) => now - entry.at > windowMs).length,
    never: users.filter((u) => u.canLogin && !u.lastLogin).length,
    latest: all[0],
  }
}

/** Where a sign-in falls across the axis, 0 at the window's far end, 100 at now. */
export function placeOnAxis(at: number, now: number, windowMs: number) {
  return Math.min(100, Math.max(0, (1 - (now - at) / windowMs) * 100))
}

/** Whose keys open the host, most first, with the total the shares are of. */
export function keyShares(users: SystemUser[]) {
  const holders = users
    .filter((u) => u.sshKeyCount > 0)
    .sort((a, b) => b.sshKeyCount - a.sshKeyCount || a.username.localeCompare(b.username))
  return { holders, total: holders.reduce((sum, u) => sum + u.sshKeyCount, 0) }
}
