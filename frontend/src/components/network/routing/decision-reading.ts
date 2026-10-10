import type { NetworkRouting, NetworkRule } from "@/lib/types"

export type RouteFamily = "inet" | "inet6"

export function addressFamily(address: string): RouteFamily {
  return address.includes(":") ? "inet6" : "inet"
}

export function decisionRules(routing: NetworkRouting, family: RouteFamily) {
  return routing.rules
    .filter((rule) => rule.family === family && rule.priority !== 0)
    .sort((a, b) => a.priority - b.priority)
}

export function decisionTables(routing: NetworkRouting, rules: NetworkRule[], family: RouteFamily) {
  const tables: NetworkRouting["tables"] = []
  for (const rule of rules) {
    if (
      rule.action !== "lookup" ||
      rule.table === undefined ||
      tables.some((t) => t.id === rule.table)
    )
      continue
    const table = routing.tables.find((t) => t.id === rule.table)
    tables.push({
      id: rule.table,
      name: table?.name ?? rule.tableName ?? String(rule.table),
      routes: table?.routes.filter((route) => route.family === family) ?? [],
    })
  }
  return tables
}

/** The path is kernel evidence; associating it with a policy rule remains an inference. */
export function inferredAnswer(routing: NetworkRouting, family: RouteFamily) {
  const path = routing.clientPath
  if (!path.device || addressFamily(path.address) !== family) return undefined
  for (const rule of decisionRules(routing, family)) {
    if (rule.action !== "lookup" || rule.table === undefined) continue
    if (rule.fwmark || rule.iif || rule.oif) continue
    if (rule.to && rule.to !== path.address) continue
    if (rule.from && rule.from !== path.source) continue
    const table = routing.tables.find((t) => t.id === rule.table)
    if (table?.routes.some((route) => route.family === family && route.device === path.device))
      return { rule, table: table.id }
  }
  return undefined
}
