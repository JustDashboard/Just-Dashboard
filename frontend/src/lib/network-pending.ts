/** Paths backed by the serialized netx recovery journal. Separate native owners are excluded. */
export function supportsPendingNetworkMutation(path: string, method: string): boolean {
  if (!["POST", "PUT", "DELETE"].includes(method.toUpperCase())) return false
  const clean = path.split("?")[0].replace(/\/$/, "")
  return [
    "/network/links",
    "/network/routing/routes",
    "/network/routing/rules",
    "/network/forwarding",
    "/network/shaping",
    "/network/gateway",
    "/network/protection",
  ].some((prefix) => clean === prefix || clean.startsWith(`${prefix}/`))
}

let pendingEnabled = false

export function setNetworkPendingApply(enabled: boolean) {
  pendingEnabled = enabled
}

export function networkPendingHeaders(path: string, method: string): Record<string, string> {
  return pendingEnabled && supportsPendingNetworkMutation(path, method)
    ? { "X-JD-Network-Apply": "pending" }
    : {}
}
