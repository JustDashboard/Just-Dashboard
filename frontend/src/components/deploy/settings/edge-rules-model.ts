import type {
  DeploymentEdgeAction,
  DeploymentEdgeMethod,
  DeploymentEdgeRule,
  DeploymentRateLimit,
  RequestFacet,
} from "@/lib/types"

/** The methods a rule can match, in the order the editor offers them. */
export const EDGE_METHODS: DeploymentEdgeMethod[] = [
  "GET",
  "HEAD",
  "POST",
  "PUT",
  "PATCH",
  "DELETE",
  "OPTIONS",
]

export const EDGE_ACTIONS: { value: DeploymentEdgeAction["kind"]; label: string }[] = [
  { value: "block", label: "Block (403)" },
  { value: "status", label: "Answer with a status" },
  { value: "redirect", label: "Redirect" },
  { value: "allow", label: "Allow" },
]

export const REDIRECT_CODES = [301, 302, 307, 308] as const

const NAME_RE = /^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/

/** A rule's name as the server keeps it: lower-case letters, digits and dashes. */
export function edgeRuleName(raw: string) {
  return raw
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, "-")
    .replace(/-{2,}/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 40)
}

/** A fresh rule, named so it is not one of the names already taken. */
export function newEdgeRule(taken: string[]): DeploymentEdgeRule {
  let index = taken.length + 1
  while (taken.includes(`rule-${index}`)) index++
  return {
    name: `rule-${index}`,
    match: { pathPrefix: "/" },
    action: { kind: "block", status: 403 },
  }
}

/**
 * The rule as the plan keeps it: text trimmed, empty conditions left out,
 * methods once each in their sorted order, a block's 403 and a redirect's
 * code filled in. The server holds a save to exactly this spelling, so the
 * page writes it rather than being refused for a stray space.
 */
export function savedEdgeRule(rule: DeploymentEdgeRule): DeploymentEdgeRule {
  const match: DeploymentEdgeRule["match"] = {}
  const prefix = rule.match.pathPrefix?.trim()
  const pattern = rule.match.pathRegex?.trim()
  const agent = rule.match.userAgent?.trim()
  if (prefix) match.pathPrefix = prefix
  if (pattern) match.pathRegex = pattern
  const methods = [...new Set(rule.match.methods ?? [])].sort()
  if (methods.length) match.methods = methods
  if (agent) match.userAgent = agent
  const clients = [...new Set((rule.match.clientFrom ?? []).map((entry) => entry.trim()))].filter(
    Boolean,
  )
  if (clients.length) match.clientFrom = clients
  let action: DeploymentEdgeAction
  switch (rule.action.kind) {
    case "block":
      action = { kind: "block", status: 403 }
      break
    case "status":
      action = { kind: "status", status: rule.action.status }
      break
    case "redirect":
      action = { kind: "redirect", location: rule.action.location.trim(), code: rule.action.code }
      break
    default:
      action = { kind: "allow" }
  }
  return { name: rule.name.trim(), match, action }
}

/** What is wrong with a rule before the server is asked, or nothing. */
export function edgeRuleProblem(rule: DeploymentEdgeRule, others: string[]): string | undefined {
  if (!NAME_RE.test(rule.name)) return "A name is lower-case letters, digits and dashes."
  if (others.includes(rule.name)) return "Another rule has this name."
  const { pathPrefix, pathRegex, methods, userAgent, clientFrom } = savedEdgeRule(rule).match
  if (!pathPrefix && !pathRegex && !methods?.length && !userAgent && !clientFrom?.length)
    return "Match a path, a method, a user agent or an address."
  if (pathPrefix && !pathPrefix.startsWith("/")) return "A path prefix starts with /."
  if (rule.action.kind === "status") {
    const status = rule.action.status
    if (!Number.isInteger(status) || status < 200 || status > 599 || Math.floor(status / 100) === 3)
      return "A status is 200 to 599, other than a redirect's 3xx."
  }
  if (rule.action.kind === "redirect" && !/^(https?:\/\/[^\s/]+)?\/\S*$/.test(rule.action.location))
    return "A redirect goes to an http or https address, or a path starting with /."
  return undefined
}

/** One line for what a rule asks and does, in the order a request meets it. */
export function edgeRuleSummary(rule: DeploymentEdgeRule) {
  const { pathPrefix, pathRegex, methods, userAgent, clientFrom } = rule.match
  const parts: string[] = []
  if (methods?.length) parts.push(methods.join(" "))
  if (pathPrefix) parts.push(`${pathPrefix}…`)
  if (pathRegex) parts.push(`path ~ ${pathRegex}`)
  if (userAgent) parts.push(`agent ~ ${userAgent}`)
  if (clientFrom?.length) parts.push(`from ${clientFrom.join(", ")}`)
  const condition = parts.join(" · ") || "every request"
  switch (rule.action.kind) {
    case "block":
      return `${condition} → 403`
    case "status":
      return `${condition} → ${rule.action.status}`
    case "redirect":
      return `${condition} → ${rule.action.code} ${rule.action.location}`
    default:
      return `${condition} → let through`
  }
}

/** A rule's hits in the window the readout reads, or 0. */
export function hitsOf(hits: RequestFacet[] | undefined, name: string) {
  return hits?.find((facet) => facet.value === name)?.count ?? 0
}

/** The rules moved: one place up (-1) or down (+1), the list unchanged at an end. */
export function moveRule(rules: DeploymentEdgeRule[], index: number, by: -1 | 1) {
  const to = index + by
  if (to < 0 || to >= rules.length) return rules
  const next = [...rules]
  const [moved] = next.splice(index, 1)
  next.splice(to, 0, moved)
  return next
}

/**
 * The host-level limit the Docker Caddy ingress is given in place of the
 * route's: Caddy has no rate-limit module, so the network gateway counts
 * new connections per source address on ports 80 and 443 — for every
 * domain the ingress serves, and connections rather than requests, which
 * the page says beside the button. Reject, so a client past it is refused
 * at once rather than left waiting for a dropped packet.
 */
export function ingressLimitRequest(limit: DeploymentRateLimit, deployment: string) {
  const name = `Ingress rate limit: ${deployment}`.slice(0, 64)
  return {
    name,
    protocol: "tcp",
    ports: "80,443",
    rate: limit.requests,
    per: limit.per,
    burst: limit.burst ?? 0,
    perSource: true,
    maxConnections: 0,
    globalConnections: 0,
    action: "reject",
  }
}

/** The section's state line: how many rules, and the limit. */
export function edgeRulesState(
  rules: DeploymentEdgeRule[],
  limit: DeploymentRateLimit | undefined,
  crowdSec: boolean,
) {
  const parts: string[] = []
  if (rules.length) parts.push(rules.length === 1 ? "1 rule" : `${rules.length} rules`)
  if (limit) parts.push(`${limit.requests} per ${limit.per}`)
  if (crowdSec) parts.push("CrowdSec")
  return parts.join(" · ") || "Off"
}

/** The rule index a refusal names ("runtime.edgeRules: edge rule admin: …"), or -1. */
export function refusedRuleIndex(message: string, rules: DeploymentEdgeRule[]) {
  const named = /edge rule ([a-z0-9-]+)/.exec(message)?.[1]
  if (!named) return -1
  const index = rules.findIndex((rule) => rule.name === named)
  // A rule whose name is not valid yet is named by its place instead.
  if (index < 0 && /^\d+$/.test(named)) return Number(named) - 1
  return index
}
