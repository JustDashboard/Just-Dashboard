import { parseAddress } from "./cidr"
import type { DNSChangeRequest, DNSEngine, DNSRecordInventory } from "./network-dns-services"

export const dnsDomainName = (value: string) =>
  value.length <= 253 &&
  value.includes(".") &&
  value.split(".").every((part) => /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(part))

export function dnsLiteralAddress(value: string) {
  if (value.trim() !== value || value.includes("%")) return undefined
  const tail = value.slice(value.lastIndexOf(":") + 1)
  if (tail.includes(".") && tail.split(".").some((part) => !/^(0|[1-9]\d{0,2})$/.test(part)))
    return undefined
  const bytes = parseAddress(value)
  if (!bytes) return undefined
  if (bytes.length === 4) return { bytes, canonical: bytes.join("."), mapped: false }
  const mapped =
    bytes.slice(0, 10).every((byte) => byte === 0) && bytes[10] === 255 && bytes[11] === 255
  if (mapped) return { bytes, canonical: `::ffff:${bytes.slice(12).join(".")}`, mapped }
  const words = Array.from({ length: 8 }, (_, i) => (bytes[i * 2] << 8) | bytes[i * 2 + 1])
  let start = -1
  let length = 1
  for (let i = 0; i < words.length; i++) {
    if (words[i] !== 0) continue
    let end = i
    while (end < words.length && words[end] === 0) end++
    if (end - i > length) {
      start = i
      length = end - i
    }
    i = end - 1
  }
  const hex = words.map((word) => word.toString(16))
  const canonical =
    start < 0
      ? hex.join(":")
      : `${hex.slice(0, start).join(":")}::${hex.slice(start + length).join(":")}`
  return { bytes, canonical, mapped }
}

export function dnsUnicastAddress(value: string, canonical = false, allowMapped = false) {
  const ip = dnsLiteralAddress(value)
  return Boolean(
    ip &&
    (!canonical || ip.canonical === value) &&
    (allowMapped || !ip.mapped) &&
    !ip.bytes.every((byte) => byte === 0) &&
    !(ip.bytes.length === 4 && ip.bytes[0] >= 224 && ip.bytes[0] <= 239) &&
    !(ip.bytes.length === 16 && ip.bytes[0] === 255),
  )
}

export function dnsPrefix(value: string, canonical = false) {
  const [address, bits, extra] = value.split("/")
  const ip = dnsLiteralAddress(address)
  if (!ip || extra !== undefined || !bits || !/^\d+$/.test(bits)) return false
  const prefix = Number(bits)
  if (!Number.isInteger(prefix) || prefix > ip.bytes.length * 8) return false
  if (canonical && (ip.mapped || `${ip.canonical}/${prefix}` !== value)) return false
  for (let bit = prefix; bit < ip.bytes.length * 8; bit++) {
    if (ip.bytes[bit >> 3] & (128 >> (bit & 7))) return false
  }
  return true
}

export function dnsClientAddress(value: string) {
  if (!value.includes("/")) return dnsUnicastAddress(value, true)
  const address = value.split("/")[0]
  const ip = dnsLiteralAddress(address)
  return Boolean(
    dnsPrefix(value, true) &&
    ip &&
    !(ip.bytes.length === 4 && ip.bytes[0] >= 224 && ip.bytes[0] <= 239) &&
    !(ip.bytes.length === 16 && ip.bytes[0] === 255),
  )
}

export function dnsClassicEndpoint(value: string) {
  const match = /^(?:\[([^\]]+)\]|([^:]+)):(\d+)$/.exec(value)
  if (!match || Number(match[3]) < 1 || Number(match[3]) > 65535) return false
  const address = match[1] ?? match[2]
  const ip = dnsLiteralAddress(address)
  return Boolean(
    ip &&
    (match[1] ? ip.bytes.length === 16 : ip.bytes.length === 4) &&
    dnsUnicastAddress(address, false, true),
  )
}

export const dnsRecordZoneEditable = (zone: DNSRecordInventory) =>
  zone.type === "Primary" &&
  !zone.disabled &&
  zone.dnssec === "Unsigned" &&
  (zone.internal === false ||
    (zone.internal === null && (zone.nativeVersion === "15.6" || zone.nativeVersion === "15.6.0")))

export type DNSRecordDraft = {
  name: string
  type: string
  value: string
  zone?: string
  ttl?: string
}
export type DNSRecordErrors = Partial<Record<keyof DNSRecordDraft | "action", string>>
export type DNSRecordAction = "override_add" | "override_remove" | "record_add" | "record_remove"

export function prepareDNSRecordChange(
  engine: DNSEngine,
  action: DNSRecordAction,
  draft: DNSRecordDraft,
  inventory?: DNSRecordInventory,
): { request?: DNSChangeRequest; errors: DNSRecordErrors } {
  const errors: DNSRecordErrors = {}
  if (!["override_add", "override_remove", "record_add", "record_remove"].includes(action))
    errors.action = "Choose a supported native record action."
  const name = draft.name.trim()
  const value = draft.value.trim()
  const zone = draft.zone?.trim() ?? ""
  const authoritative = action === "record_add" || action === "record_remove"
  const ip = dnsLiteralAddress(value)
  if (!dnsDomainName(name))
    errors.name = "Use a complete lower-case DNS owner without a wildcard or trailing dot."
  if (draft.type !== "A" && draft.type !== "AAAA") errors.type = "Choose A or AAAA."
  if (
    !dnsUnicastAddress(value, true) ||
    !ip ||
    (draft.type === "A" ? ip.bytes.length !== 4 : ip.bytes.length !== 16)
  )
    errors.value = "Use a canonical unicast IP of the selected record family, without a zone."
  let ttl: number | undefined
  if (authoritative) {
    if (
      engine !== "technitium" ||
      !dnsDomainName(zone) ||
      !(name === zone || name.endsWith(`.${zone}`))
    )
      errors.zone = "Select an explicit containing Technitium primary zone."
    if (!inventory || inventory.zone !== zone || !dnsRecordZoneEditable(inventory))
      errors.zone = "Refresh a supported enabled unsigned Primary zone before reviewing records."
    const raw = draft.ttl?.trim() ?? ""
    ttl = Number(raw)
    if (!/^[1-9]\d*$/.test(raw) || !Number.isInteger(ttl) || ttl < 1 || ttl > 86400)
      errors.ttl = "Enter the exact TTL from 1 to 86400 seconds."
  } else {
    if (engine !== "adguard" && engine !== "pihole")
      errors.zone = "Local overrides require AdGuard Home or Pi-hole."
    if (zone) errors.zone = "A local override has no authoritative zone."
    if (draft.ttl?.trim()) errors.ttl = "Local override TTL is native; leave this field empty."
  }
  if (Object.keys(errors).length) return { errors }
  return {
    errors,
    request: authoritative
      ? {
          action: action as "record_add" | "record_remove",
          zone,
          record: { name, type: draft.type as "A" | "AAAA", value, ttl: ttl! },
        }
      : {
          action: action as "override_add" | "override_remove",
          record: { name, type: draft.type as "A" | "AAAA", value },
        },
  }
}

export function prepareDNSClientGroups(
  engine: DNSEngine,
  addressDraft: string,
  groups: unknown,
): { request?: DNSChangeRequest; errors: { address?: string; groups?: string } } {
  const errors: { address?: string; groups?: string } = {}
  const address = addressDraft.trim()
  if (engine !== "pihole" || !dnsClientAddress(address))
    errors.address =
      "Choose an existing Pi-hole client with a canonical literal IP or network prefix."
  if (
    !Array.isArray(groups) ||
    groups.length > 64 ||
    groups.some((id) => !Number.isInteger(id) || id < 0 || id > 2147483647) ||
    new Set(groups).size !== groups.length
  )
    errors.groups = "Choose 0–64 unique existing native group IDs; an empty list is explicit."
  if (Object.keys(errors).length) return { errors }
  return {
    errors,
    request: { action: "client_groups", client: { address, groups: [...(groups as number[])] } },
  }
}

export type DNSDomainFilterAction = "filter_add" | "filter_remove"
export type DNSDomainFilterErrors = {
  domain?: string
  disposition?: string
  groups?: string
  action?: string
}

export function prepareDNSDomainFilterChange(
  engine: DNSEngine,
  action: DNSDomainFilterAction,
  draft: { domain: string; disposition: string; groups?: unknown },
): { request?: DNSChangeRequest; errors: DNSDomainFilterErrors } {
  const errors: DNSDomainFilterErrors = {}
  const domain = draft.domain.trim()
  if (
    (action !== "filter_add" && action !== "filter_remove") ||
    (engine !== "adguard" && engine !== "pihole")
  )
    errors.action = "Choose a supported AdGuard suffix or Pi-hole exact domain action."
  if (!dnsDomainName(domain))
    errors.domain = "Use a complete lower-case DNS name without a wildcard or native rule syntax."
  if (draft.disposition !== "allow" && draft.disposition !== "deny")
    errors.disposition = "Choose allow or deny."
  const piAdd = engine === "pihole" && action === "filter_add"
  if (piAdd) {
    if (
      !Array.isArray(draft.groups) ||
      draft.groups.length > 64 ||
      Array.from(draft.groups).some((id) => !Number.isInteger(id) || id < 0 || id > 2147483647) ||
      new Set(draft.groups).size !== draft.groups.length
    )
      errors.groups = "Choose 0–64 unique existing native group IDs; an empty list is explicit."
  } else if (draft.groups !== undefined) {
    errors.groups = "Only a Pi-hole addition takes replacement group memberships."
  }
  if (Object.keys(errors).length) return { errors }
  return {
    errors,
    request: {
      action,
      filter: {
        domain,
        disposition: draft.disposition as "allow" | "deny",
        match: engine === "adguard" ? "suffix" : "exact",
        ...(piAdd ? { groups: [...(draft.groups as number[])] } : {}),
      },
    },
  }
}
