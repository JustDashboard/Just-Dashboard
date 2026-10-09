export type NativeRoute = { destination: string; gateway?: string; metric: number; table: number }
export type NativeFamily = {
  method: string
  addresses: string[]
  dns: string[]
  domains: string[]
  ignoreAutoDns: boolean
  ignoreAutoRoutes: boolean
  routes: NativeRoute[]
}
export type NativeIntent = { ipv4: NativeFamily; ipv6: NativeFamily }
export type NativeProfile = {
  checkedAt: string
  device: string
  kind: string
  owner: string
  renderer?: string
  version?: string
  profile?: string
  generation?: string
  editable: boolean
  refusal?: string
  intent?: NativeIntent
  contract: { members: string[]; bondMode?: string; vrfTable?: number; master?: string }
  configured: { status: string; reason?: string }
  runtime: { status: string; reason?: string }
  boot: { status: string; reason?: string }
}

const object = (value: unknown): value is Record<string, unknown> =>
  typeof value === "object" && value !== null && !Array.isArray(value)
const text = (value: unknown): value is string =>
  typeof value === "string" && value.trim().length > 0
const optionalText = (value: unknown) => value === undefined || typeof value === "string"
const strings = (value: unknown, limit = Infinity): value is string[] =>
  Array.isArray(value) && value.length <= limit && value.every(text)
const integer = (value: unknown, low: number, high: number): value is number =>
  typeof value === "number" && Number.isInteger(value) && value >= low && value <= high
const evidence = (value: unknown) =>
  object(value) && text(value.status) && optionalText(value.reason)

function family(value: unknown, ipv6: boolean): value is NativeFamily {
  if (!object(value)) return false
  const methods = ["manual", "auto", "disabled"]
  if (ipv6) methods.push("dhcp", "slaac")
  return (
    text(value.method) &&
    methods.includes(value.method) &&
    strings(value.addresses, 16) &&
    strings(value.dns, 8) &&
    strings(value.domains, 16) &&
    typeof value.ignoreAutoDns === "boolean" &&
    typeof value.ignoreAutoRoutes === "boolean" &&
    Array.isArray(value.routes) &&
    value.routes.length <= 32 &&
    value.routes.every(
      (route) =>
        object(route) &&
        text(route.destination) &&
        optionalText(route.gateway) &&
        integer(route.metric, -1, 1_000_000) &&
        integer(route.table, 1, 2_147_483_647) &&
        ![52, 253, 255].includes(route.table),
    ) &&
    (value.method !== "manual" || value.addresses.length > 0) &&
    (value.method !== "disabled" ||
      value.addresses.length + value.dns.length + value.domains.length + value.routes.length === 0)
  )
}

/** Validate every rendered field before a poll can replace the retained native baseline. */
export function readNativeProfile(value: unknown, device: string): NativeProfile {
  if (
    !object(value) ||
    typeof value.editable !== "boolean" ||
    !text(value.device) ||
    !text(value.kind) ||
    !text(value.owner) ||
    typeof value.checkedAt !== "string" ||
    !Number.isFinite(Date.parse(value.checkedAt)) ||
    ![value.renderer, value.version, value.profile, value.generation, value.refusal].every(
      optionalText,
    )
  ) {
    throw new Error("The native profile response is incomplete or invalid.")
  }
  if (value.device !== device) {
    throw new Error("The native profile response belongs to a different device.")
  }
  if (
    !evidence(value.configured) ||
    !evidence(value.runtime) ||
    !evidence(value.boot) ||
    !object(value.contract) ||
    !strings(value.contract.members) ||
    !optionalText(value.contract.bondMode) ||
    !optionalText(value.contract.master) ||
    (value.contract.vrfTable !== undefined && !integer(value.contract.vrfTable, 1, 4_294_967_295))
  ) {
    throw new Error("Native ownership or lifecycle evidence is incomplete or invalid.")
  }
  if (
    value.intent !== undefined &&
    (!object(value.intent) || !family(value.intent.ipv4, false) || !family(value.intent.ipv6, true))
  ) {
    throw new Error("Native family intent is incomplete or invalid.")
  }
  if (
    value.editable &&
    (!value.intent ||
      !text(value.generation) ||
      !text(value.profile) ||
      !["NetworkManager", "networkd", "netplan"].includes(value.owner) ||
      !["NetworkManager", "networkd"].includes(String(value.renderer)) ||
      (value.owner !== "netplan" && value.renderer !== value.owner) ||
      (value.renderer === "NetworkManager" &&
        object(value.intent) &&
        object(value.intent.ipv6) &&
        value.intent.ipv6.method === "slaac") ||
      !["physical", "dummy", "veth", "vlan", "bridge", "bond", "vrf"].includes(value.kind) ||
      (value.kind === "vrf" && !integer(value.contract.vrfTable, 1, 4_294_967_295)))
  ) {
    throw new Error("Editable native ownership or intent is incomplete or invalid.")
  }
  const profile = value as NativeProfile
  if (
    profile.editable &&
    [profile.configured, profile.runtime, profile.boot].some((state) => state.status !== "matching")
  ) {
    throw new Error("Native editability contradicts its lifecycle evidence.")
  }
  return profile
}
