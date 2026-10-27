import type { ApiToken, DashboardUser, Role } from "@/lib/types"
import { DAY, inKeyScope, type KeyScope } from "./access"

export type KeyFilter = KeyScope | "idle" | "unbounded"
export type KeyOrder = "newest" | "used" | "expiry" | "name"

export function inKeyFilter(token: ApiToken, filter: KeyFilter, now: number) {
  if (filter === "idle") {
    return Boolean(token.lastUsedAt && now - Date.parse(token.lastUsedAt) >= 30 * DAY)
  }
  if (filter === "unbounded") return !token.expiresAt
  return inKeyScope(token, filter, now)
}

/**
 * Search the facts the operator can see, including an administrator's owner
 * names. A key limited to one job is found by the job as well — "deploy",
 * "mcp" — since the row names the job where it would name the role.
 */
export function matchesKey(token: ApiToken, query: string, owner?: DashboardUser) {
  const words = query.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean)
  const facts = [
    token.name,
    token.prefix,
    token.role,
    token.scope,
    owner?.username,
    owner?.displayName,
  ]
    .join(" ")
    .toLocaleLowerCase()
  return words.every((word) => facts.includes(word))
}

/** Missing usage and expiry dates go last; the ID breaks ties without poll-time shuffling. */
export function orderKeys(tokens: ApiToken[], order: KeyOrder) {
  const time = (value?: string) => (value ? Date.parse(value) : 0)
  return [...tokens].sort((a, b) => {
    let diff = 0
    switch (order) {
      case "used":
        diff = time(b.lastUsedAt) - time(a.lastUsedAt)
        break
      case "expiry":
        diff = (time(a.expiresAt) || Infinity) - (time(b.expiresAt) || Infinity)
        break
      case "name":
        diff = a.name.localeCompare(b.name)
        break
      default:
        diff = time(b.createdAt) - time(a.createdAt)
    }
    return diff || b.id - a.id
  })
}

/** A replacement can keep or narrow a key's scope, including after its owner was demoted. */
export function replacementRoles(tokenRole: Role, ownerRole: Role): Role[] {
  const roles: Role[] = ["readonly", "limited", "admin"]
  const ceiling = Math.min(roles.indexOf(tokenRole), roles.indexOf(ownerRole))
  return roles.slice(0, ceiling + 1).reverse()
}
