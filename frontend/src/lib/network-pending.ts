/** Paths backed by the serialized netx recovery journal. */
export function supportsPendingNetworkMutation(path: string, method: string): boolean {
  if (!["POST", "PUT", "DELETE"].includes(method.toUpperCase())) return false
  const clean = path.split("?")[0].replace(/\/$/, "")
  if (clean === "/network/drift/repairs") return method.toUpperCase() === "POST"
  return [
    "/network/links",
    "/network/native/profiles",
    "/network/routing/routes",
    "/network/routing/rules",
    "/network/forwarding",
    "/network/shaping",
    "/network/gateway/forwards",
    "/network/gateway/nat",
    "/network/protection/limits",
    "/network/protection/blocklists",
    "/network/protection/settings",
    "/network/protection/trusted",
    "/network/protection/exceptions",
  ].some((prefix) => clean === prefix || clean.startsWith(`${prefix}/`))
}

let pendingEnabled = false

export function setNetworkPendingApply(enabled: boolean) {
  pendingEnabled = enabled
}

export function networkPendingHeaders(path: string, method: string): Record<string, string> {
  const nativeProfile =
    method.toUpperCase() === "PUT" && path.startsWith("/network/native/profiles/")
  return (pendingEnabled || nativeProfile) && supportsPendingNetworkMutation(path, method)
    ? { "X-JD-Network-Apply": "pending" }
    : {}
}
