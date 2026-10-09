import {
  readDNSConnection,
  type DNSConnection,
  type DNSEngine,
  type DNSNativeReading,
} from "./network-dns-services"
import type { DNSView } from "./types"

/** A native engine's DHCP server, read only: its ranges and its own lease table. */
export type DNSDHCPRange = {
  name?: string
  family: "ipv4" | "ipv6"
  start: string
  end: string
  enabled?: boolean
}
export type DNSDHCPLease = {
  address: string
  hardware?: string
  hostname?: string
  expires?: string
  static: boolean
  scope?: string
}
export type DNSDHCPInventory = {
  engine: DNSEngine
  nativeVersion: string
  observedAt: string
  transport: DNSNativeReading
  enabled?: boolean
  configuration: DNSNativeReading
  interface?: string
  ranges: DNSDHCPRange[]
  leases: DNSDHCPLease[]
  leaseEvidence: DNSNativeReading
  limitations: string[]
}
export type DNSDHCPView = {
  connection: DNSConnection
  inventory?: DNSDHCPInventory
  state: "available" | "partial" | "unavailable" | "unsupported"
  error?: string
}

/** A detected DNS server joined with the connections that already reach it. */
export type DNSServiceHandoff = {
  detected: DNSView["adblock"][number]
  engine: DNSEngine
  endpoint?: string
  endpointProblem?: string
  connections: {
    id: string
    name: string
    management: boolean
    ownership: string
    match: "container" | "endpoint"
  }[]
}

const object = (value: unknown): value is Record<string, unknown> =>
  typeof value === "object" && value !== null && !Array.isArray(value)
const text = (value: unknown, max = 512): value is string =>
  typeof value === "string" && value.length <= max
const optional = (value: unknown, max = 512) => value === undefined || text(value, max)
const bool = (value: unknown) => value === undefined || typeof value === "boolean"
const date = (value: unknown): value is string =>
  text(value, 64) && /^\d{4}-\d{2}-\d{2}T/.test(value) && Number.isFinite(Date.parse(value))
const engines: DNSEngine[] = ["adguard", "pihole", "technitium"]
const fail = (what: string): never => {
  throw new Error(`${what} is incomplete or outside its bounded contract.`)
}

function reading(value: unknown, what: string): DNSNativeReading {
  if (
    !object(value) ||
    !text(value.state, 64) ||
    !value.state ||
    !text(value.basis, 128) ||
    !text(value.summary, 2048)
  )
    return fail(what)
  return { state: value.state, basis: value.basis, summary: value.summary }
}

/** Validate a DHCP reading before it can replace the one on screen. */
export function readDNSDHCPView(value: unknown, connection: DNSConnection): DNSDHCPView {
  const what = "Native DHCP inventory"
  if (
    !object(value) ||
    !["available", "partial", "unavailable", "unsupported"].includes(String(value.state)) ||
    !optional(value.error, 2048)
  )
    return fail(what)
  const owner = readDNSConnection(value.connection)
  if (
    owner.id !== connection.id ||
    owner.generation !== connection.generation ||
    owner.engine !== connection.engine
  )
    throw new Error("The DHCP reading belongs to another connection or generation.")
  const view: DNSDHCPView = {
    connection: owner,
    state: value.state as DNSDHCPView["state"],
    error: value.error as string | undefined,
  }
  if (value.inventory === undefined) return view
  const i = value.inventory
  if (
    !object(i) ||
    !engines.includes(i.engine as DNSEngine) ||
    i.engine !== owner.engine ||
    !text(i.nativeVersion, 64) ||
    !date(i.observedAt) ||
    !bool(i.enabled) ||
    !optional(i.interface, 64) ||
    !Array.isArray(i.ranges) ||
    i.ranges.length > 64 ||
    !Array.isArray(i.leases) ||
    i.leases.length > 256 ||
    !Array.isArray(i.limitations) ||
    !i.limitations.every((line) => text(line, 2048))
  )
    return fail(what)
  const ranges = i.ranges.map((range: unknown): DNSDHCPRange => {
    if (
      !object(range) ||
      (range.family !== "ipv4" && range.family !== "ipv6") ||
      !text(range.start, 64) ||
      !text(range.end, 64) ||
      !optional(range.name, 255) ||
      !bool(range.enabled)
    )
      return fail(what)
    return range as DNSDHCPRange
  })
  const leases = i.leases.map((lease: unknown): DNSDHCPLease => {
    if (
      !object(lease) ||
      !text(lease.address, 64) ||
      !lease.address ||
      !optional(lease.hardware, 64) ||
      !optional(lease.hostname, 512) ||
      !(lease.expires === undefined || date(lease.expires)) ||
      typeof lease.static !== "boolean" ||
      !optional(lease.scope, 512)
    )
      return fail(what)
    return lease as DNSDHCPLease
  })
  view.inventory = {
    engine: i.engine as DNSEngine,
    nativeVersion: i.nativeVersion,
    observedAt: i.observedAt,
    transport: reading(i.transport, what),
    enabled: i.enabled as boolean | undefined,
    configuration: reading(i.configuration, what),
    interface: i.interface as string | undefined,
    ranges,
    leases,
    leaseEvidence: reading(i.leaseEvidence, what),
    limitations: i.limitations as string[],
  }
  return view
}

/** Validate the detection-to-connection handoffs. */
export function readDNSHandoffs(value: unknown): DNSServiceHandoff[] {
  const what = "The DNS server handoff"
  if (!Array.isArray(value) || value.length > 64) return fail(what)
  return value.map((row): DNSServiceHandoff => {
    if (
      !object(row) ||
      !object(row.detected) ||
      !engines.includes(row.engine as DNSEngine) ||
      !optional(row.endpoint, 128) ||
      !optional(row.endpointProblem, 1024) ||
      !Array.isArray(row.connections) ||
      row.connections.length > 32 ||
      !text(row.detected.name, 128) ||
      !["adguardhome", "pihole", "technitium"].includes(String(row.detected.kind))
    )
      return fail(what)
    for (const c of row.connections) {
      if (
        !object(c) ||
        !text(c.id, 128) ||
        !c.id ||
        !text(c.name, 80) ||
        typeof c.management !== "boolean" ||
        !text(c.ownership, 32) ||
        (c.match !== "container" && c.match !== "endpoint")
      )
        return fail(what)
    }
    return row as DNSServiceHandoff
  })
}
