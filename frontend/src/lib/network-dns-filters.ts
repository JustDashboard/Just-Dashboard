import {
  readDNSConnection,
  type DNSConnection,
  type DNSEngine,
  type DNSNativeReading,
} from "./network-dns-services"

export type DNSFilterEntry = {
  id?: number
  kind: string
  origin?: string
  enabled?: boolean
  groups?: number[]
  ruleCount?: number
  updatedAt?: string
  status?: number
  fingerprint: string
}
export type DNSFilterSection = {
  evidence: DNSNativeReading
  identity: DNSNativeReading
  entries: DNSFilterEntry[]
  fingerprint?: string
}
export type DNSFilterInventory = {
  engine: DNSEngine
  nativeVersion: string
  observedAt: string
  transport: DNSNativeReading
  runtime: DNSNativeReading
  protection?: boolean
  filtering?: boolean
  sources: DNSFilterSection
  rules: DNSFilterSection
  appRules: DNSNativeReading
  fingerprint?: string
  limitations: string[]
}
export type DNSFilterView = {
  connection: DNSConnection
  inventory?: DNSFilterInventory
  state: "available" | "partial" | "unavailable" | "unsupported"
  error?: string
}

const object = (value: unknown): value is Record<string, unknown> =>
  typeof value === "object" && value !== null && !Array.isArray(value)
const text = (value: unknown, max = 512): value is string =>
  typeof value === "string" && value.length <= max
const keys = (value: Record<string, unknown>, allowed: string[]) =>
  Object.keys(value).every((key) => allowed.includes(key))
const number = (value: unknown, max = Number.MAX_SAFE_INTEGER): value is number =>
  typeof value === "number" && Number.isSafeInteger(value) && value >= 0 && value <= max
const digest = (value: unknown): value is string => text(value, 64) && /^[a-f0-9]{64}$/.test(value)
const bool = (value: unknown) => value === undefined || typeof value === "boolean"
const date = (value: unknown): value is string =>
  text(value, 64) && /^\d{4}-\d{2}-\d{2}T/.test(value) && Number.isFinite(Date.parse(value))
const fail = (): never => {
  throw new Error("Native DNS filter inventory is incomplete or outside its bounded contract.")
}

function reading(value: unknown): DNSNativeReading {
  if (
    !object(value) ||
    !keys(value, ["state", "basis", "summary"]) ||
    !text(value.state, 64) ||
    !value.state ||
    !text(value.basis, 128) ||
    !value.basis ||
    !text(value.summary, 2048)
  )
    return fail()
  return { state: value.state, basis: value.basis, summary: value.summary }
}

function origin(value: unknown) {
  if (value === undefined) return true
  if (!text(value) || !/^https?:\/\/[^\s/?#]+$/.test(value)) return false
  try {
    const url = new URL(value)
    return Boolean(
      url.hostname &&
      !url.username &&
      !url.password &&
      !url.search &&
      !url.hash &&
      url.pathname === "/",
    )
  } catch {
    return false
  }
}

function entry(value: unknown, engine: DNSEngine, section: "sources" | "rules"): DNSFilterEntry {
  if (
    !object(value) ||
    !keys(value, [
      "id",
      "kind",
      "origin",
      "enabled",
      "groups",
      "ruleCount",
      "updatedAt",
      "status",
      "fingerprint",
    ]) ||
    !digest(value.fingerprint) ||
    !origin(value.origin) ||
    !bool(value.enabled) ||
    (value.id !== undefined && !number(value.id)) ||
    (value.ruleCount !== undefined && !number(value.ruleCount)) ||
    (value.status !== undefined && !number(value.status)) ||
    (value.updatedAt !== undefined &&
      !(
        text(value.updatedAt, 64) &&
        (/^\d{1,16}$/.test(value.updatedAt) || date(value.updatedAt))
      )) ||
    (value.groups !== undefined &&
      (!Array.isArray(value.groups) ||
        value.groups.length > 64 ||
        !value.groups.every((id) => number(id, 2147483647)) ||
        new Set(value.groups).size !== value.groups.length))
  )
    return fail()
  const kinds =
    section === "sources"
      ? [
          "block_subscription",
          "allow_subscription",
          ...(engine === "technitium" ? ["subscription_comment"] : []),
        ]
      : engine === "adguard"
        ? ["native_rule"]
        : engine === "pihole"
          ? ["allow_exact", "allow_regex", "deny_exact", "deny_regex"]
          : []
  if (!text(value.kind, 64) || !kinds.includes(value.kind)) return fail()
  if (
    (engine === "pihole" &&
      (value.id === undefined || value.enabled === undefined || value.groups === undefined)) ||
    (engine === "adguard" &&
      section === "sources" &&
      (value.id === undefined || value.enabled === undefined || value.ruleCount === undefined)) ||
    (engine !== "pihole" && value.groups !== undefined) ||
    (engine !== "pihole" && value.status !== undefined) ||
    (section === "rules" &&
      [value.origin, value.ruleCount, value.updatedAt, value.status].some(
        (item) => item !== undefined,
      )) ||
    (engine === "technitium" &&
      [value.id, value.enabled, value.ruleCount, value.updatedAt].some(
        (item) => item !== undefined,
      )) ||
    (engine === "adguard" &&
      section === "rules" &&
      [value.id, value.enabled].some((item) => item !== undefined))
  )
    return fail()
  return {
    id: value.id as number | undefined,
    kind: value.kind,
    origin: value.origin as string | undefined,
    enabled: value.enabled as boolean | undefined,
    groups: value.groups === undefined ? undefined : [...(value.groups as number[])],
    ruleCount: value.ruleCount as number | undefined,
    updatedAt: value.updatedAt as string | undefined,
    status: value.status as number | undefined,
    fingerprint: value.fingerprint,
  }
}

function section(value: unknown, engine: DNSEngine, name: "sources" | "rules"): DNSFilterSection {
  if (
    !object(value) ||
    !keys(value, ["evidence", "identity", "entries", "fingerprint"]) ||
    !Array.isArray(value.entries) ||
    value.entries.length > 256
  )
    return fail()
  const evidence = reading(value.evidence)
  const identity = reading(value.identity)
  if (
    !["configured", "unknown", "unsupported"].includes(evidence.state) ||
    identity.state !== "redacted"
  )
    return fail()
  if (
    (evidence.state === "configured" && !digest(value.fingerprint)) ||
    (evidence.state !== "configured" &&
      (value.entries.length > 0 || value.fingerprint !== undefined))
  )
    return fail()
  const entries = value.entries.map((item) => entry(item, engine, name))
  const ids = entries.filter((item) => item.id !== undefined).map((item) => item.id)
  if (new Set(ids).size !== ids.length) return fail()
  return { evidence, identity, entries, fingerprint: value.fingerprint as string | undefined }
}

export function readDNSFilterView(value: unknown, expected: DNSConnection): DNSFilterView {
  if (
    !object(value) ||
    !keys(value, ["connection", "inventory", "state", "error"]) ||
    !text(value.state) ||
    !["available", "partial", "unavailable", "unsupported"].includes(value.state) ||
    (value.error !== undefined && !text(value.error))
  )
    return fail()
  const connection = readDNSConnection(value.connection)
  if (JSON.stringify(connection) !== JSON.stringify(readDNSConnection(expected)))
    throw new Error(
      "Native DNS filter connection changed. Read the current engine before retrying.",
    )
  let inventory: DNSFilterInventory | undefined
  if (value.inventory !== undefined) {
    const i = value.inventory
    if (
      !object(i) ||
      !keys(i, [
        "engine",
        "nativeVersion",
        "observedAt",
        "transport",
        "runtime",
        "protection",
        "filtering",
        "sources",
        "rules",
        "appRules",
        "fingerprint",
        "limitations",
      ]) ||
      i.engine !== connection.engine ||
      !text(i.nativeVersion, 64) ||
      !i.nativeVersion ||
      !date(i.observedAt) ||
      !bool(i.protection) ||
      !bool(i.filtering) ||
      (i.filtering !== undefined && i.engine !== "adguard") ||
      (i.fingerprint !== undefined && !digest(i.fingerprint)) ||
      !Array.isArray(i.limitations) ||
      i.limitations.length > 32 ||
      !i.limitations.every((item) => text(item, 2048))
    )
      return fail()
    inventory = {
      engine: connection.engine,
      nativeVersion: i.nativeVersion,
      observedAt: i.observedAt,
      transport: reading(i.transport),
      runtime: reading(i.runtime),
      protection: i.protection as boolean | undefined,
      filtering: i.filtering as boolean | undefined,
      sources: section(i.sources, connection.engine, "sources"),
      rules: section(i.rules, connection.engine, "rules"),
      appRules: reading(i.appRules),
      fingerprint: i.fingerprint as string | undefined,
      limitations: [...i.limitations] as string[],
    }
  }
  const incomplete =
    inventory &&
    [inventory.sources, inventory.rules].some((item) => item.evidence.state === "unknown")
  if (
    (value.state === "available" &&
      (!inventory || incomplete || !inventory.fingerprint || value.error)) ||
    (value.state === "partial" && (!inventory || !incomplete)) ||
    (["unavailable", "unsupported"].includes(value.state) && inventory)
  )
    return fail()
  return {
    connection,
    inventory,
    state: value.state as DNSFilterView["state"],
    error: value.error as string | undefined,
  }
}

const filterNames: Record<string, string> = {
  block_subscription: "Block subscription",
  allow_subscription: "Allow subscription",
  subscription_comment: "Subscription comment",
  native_rule: "Custom rule",
  allow_exact: "Exact allow rule",
  allow_regex: "Regex allow rule",
  deny_exact: "Exact deny rule",
  deny_regex: "Regex deny rule",
}
export const dnsFilterKindName = (kind: string) => filterNames[kind] ?? kind
