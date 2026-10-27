import { networkOf, type NetworkKind } from "@/lib/clients"
import type { ApiToken, DashboardUser, Role, SessionInfo } from "@/lib/types"

/**
 * The account pages' reads, kept apart from what draws them so each one is a
 * fact a test can pin: whether a session is open right now, what proved it,
 * how a key's line is drawn, which accounts a password alone opens.
 *
 * A wire in these pictures says one of four things, the same four the
 * deployment and Configuration pictures say: it is carrying something now
 * (a pulse), it exists and is quiet (still), it ought to be looked at
 * (amber), or it has never carried anything (dashed).
 */

export const MINUTE = 60_000
export const HOUR = 60 * MINUTE
export const DAY = 24 * HOUR

/**
 * How recently a session has to have been seen to be "active now". The
 * server moves `lastSeenAt` at most every thirty seconds, and an open tab
 * polls well inside that, so two minutes is a tab that is open rather than
 * one that was.
 */
export const ACTIVE_MS = 2 * MINUTE

/** How close an expiry has to be before a key's state says so in amber. */
export const EXPIRING_MS = 14 * DAY

export type Line = { still: boolean; dashed?: boolean; tone: "default" | "warning" }

export function activeNow(session: Pick<SessionInfo, "lastSeenAt">, now: number) {
  return now - new Date(session.lastSeenAt).getTime() < ACTIVE_MS
}

/**
 * What let a session in. A session still owing its code reached nothing but
 * the 2FA routes, and one let in on a password alone is the one an operator
 * of a root-equivalent panel wants to see first.
 */
export type Guard = "code" | "recovery" | "password" | "passkey" | "pending" | "unknown"

export function sessionGuard(session: Pick<SessionInfo, "authMethod" | "twoFactorPassed">): Guard {
  if (!session.twoFactorPassed) return "pending"
  switch (session.authMethod) {
    case "totp":
    case "passkey+totp":
      return "code"
    case "recovery":
    case "passkey+recovery":
      return "recovery"
    case "password":
      return "password"
    // A passkey verifies its user (a PIN or a fingerprint) and cannot be
    // phished, so it is not drawn as the amber password-alone line.
    case "passkey":
      return "passkey"
    default:
      return "unknown"
  }
}

/** A session's wire: open now pulses; owing a code or let in on a password alone is amber. */
export function sessionLine(
  session: Pick<SessionInfo, "lastSeenAt" | "authMethod" | "twoFactorPassed">,
  now: number,
): Line {
  const guard = sessionGuard(session)
  const tone = guard === "pending" || guard === "password" ? "warning" : "default"
  return { still: !activeNow(session, now), dashed: guard === "pending", tone }
}

/** Where sessions arrive from, the tailnet first, the open internet last. */
const NETWORK_ORDER: NetworkKind[] = ["tailscale", "local", "server", "internet"]

export type NetworkGroup<T> = {
  kind: NetworkKind
  label: string
  product?: string
  sessions: T[]
}

/** The sessions by the network each one came in on, in `NETWORK_ORDER`. */
export function byNetwork<T extends Pick<SessionInfo, "ip">>(sessions: T[]): NetworkGroup<T>[] {
  const groups = new Map<NetworkKind, NetworkGroup<T>>()
  for (const session of sessions) {
    const network = networkOf(session.ip)
    const group = groups.get(network.kind) ?? { ...network, sessions: [] }
    group.sessions.push(session)
    groups.set(network.kind, group)
  }
  return NETWORK_ORDER.flatMap((kind) => groups.get(kind) ?? [])
}

export function expired(token: Pick<ApiToken, "expiresAt">, now: number) {
  return Boolean(token.expiresAt && new Date(token.expiresAt).getTime() < now)
}

/** Called within the last week — the keys something is actually using. */
export function usedThisWeek(token: Pick<ApiToken, "lastUsedAt">, now: number) {
  return Boolean(token.lastUsedAt && now - new Date(token.lastUsedAt).getTime() < 7 * DAY)
}

/** Called within the last day: a key whose wire carries something now. */
export function usedToday(token: Pick<ApiToken, "lastUsedAt">, now: number) {
  return Boolean(token.lastUsedAt && now - new Date(token.lastUsedAt).getTime() < DAY)
}

export function expiringSoon(token: Pick<ApiToken, "expiresAt" | "revoked">, now: number) {
  if (!token.expiresAt || token.revoked || expired(token, now)) return false
  return new Date(token.expiresAt).getTime() - now < EXPIRING_MS
}

/** The keys that would still open a door right now. */
export function usableKeys<T extends Pick<ApiToken, "revoked" | "expiresAt">>(
  tokens: T[],
  now: number,
) {
  return tokens.filter((k) => !k.revoked && !expired(k, now))
}

/**
 * A key's wire: called today pulses, about to stop is amber, never called is
 * dashed — a key nobody uses is a key nobody would notice being used.
 */
export function keyLine(
  token: Pick<ApiToken, "lastUsedAt" | "expiresAt" | "revoked">,
  now: number,
): Line {
  return {
    still: !usedToday(token, now),
    dashed: !token.lastUsedAt,
    tone: expiringSoon(token, now) ? "warning" : "default",
  }
}

/** The narrowings over the keys list, each a question asked of a list of credentials. */
export type KeyScope = "all" | "week" | "unused" | "expiring"

export function inKeyScope(
  token: Pick<ApiToken, "lastUsedAt" | "expiresAt" | "revoked">,
  scope: KeyScope,
  now: number,
) {
  switch (scope) {
    case "week":
      return usedThisWeek(token, now)
    case "unused":
      return !token.lastUsedAt
    case "expiring":
      return expiringSoon(token, now)
    default:
      return true
  }
}

/** The roles from the one that holds everything down, the order every list of them takes. */
export const ROLES: Role[] = ["admin", "limited", "readonly"]

export function neverSignedIn(user: Pick<DashboardUser, "lastLoginAt">) {
  return !user.lastLoginAt || user.lastLoginAt.startsWith("0001")
}

/** The narrowings over the accounts: a role, or a question asked of all of them. */
export type UserScope = "all" | Role | "bare" | "never" | "disabled"

/** The enabled accounts without an authenticator, regardless of the install policy. */
export function unenrolled(user: Pick<DashboardUser, "totpEnabled" | "disabled">) {
  return !user.disabled && !user.totpEnabled
}

export function inUserScope(
  user: Pick<DashboardUser, "role" | "totpEnabled" | "disabled" | "lastLoginAt">,
  scope: UserScope,
) {
  switch (scope) {
    case "all":
      return true
    case "bare":
      return unenrolled(user)
    case "never":
      return neverSignedIn(user)
    case "disabled":
      return user.disabled
    default:
      return user.role === scope
  }
}

/**
 * The users page counts missing authenticators, including administrators.
 * With required two-factor every missing authenticator blocks sign-in;
 * otherwise the warning highlights administrators reachable with a password.
 */
export function peopleVerdict(
  users: Pick<DashboardUser, "role" | "totpEnabled" | "disabled">[],
  require2fa = false,
): {
  tone: "warning" | "notice" | "running"
  bare: number
  admins: number
} {
  const bare = users.filter(unenrolled)
  const admins = bare.filter((u) => u.role === "admin").length
  if (bare.length === 0) return { tone: "running", bare: 0, admins: 0 }
  return { tone: require2fa || admins > 0 ? "warning" : "notice", bare: bare.length, admins }
}
