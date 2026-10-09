import {
  dnsClassicEndpoint,
  dnsClientAddress,
  dnsDomainName,
  dnsLiteralAddress,
  dnsPrefix,
  dnsRecordZoneEditable,
  dnsUnicastAddress,
} from "./network-dns-service-policy"

export const DNS_SERVICE_BASE = "/network/dns/services"
export type DNSEngine = "adguard" | "pihole" | "technitium"
export const DNS_ENGINES: { value: DNSEngine; name: string }[] = [
  { value: "adguard", name: "AdGuard Home" },
  { value: "pihole", name: "Pi-hole" },
  { value: "technitium", name: "Technitium" },
]
export const dnsEngineName = (engine: DNSEngine) =>
  DNS_ENGINES.find((item) => item.value === engine)?.name ?? engine

export type DNSCredential = { username?: string; password?: string; token?: string }
export type DNSConnectionRequest = {
  name: string
  engine: DNSEngine
  endpoint: string
  serverName?: string
  ca?: string
  management: boolean
  credential: DNSCredential
}
export type DNSConnection = {
  id: string
  name: string
  engine: DNSEngine
  endpoint: string
  serverName?: string
  customCA: boolean
  management: boolean
  hasCredential: boolean
  generation: number
  ownership: string
  containerId?: string
  createdAt: string
  updatedAt: string
}
export type DNSNativeReading = { state: string; basis: string; summary: string }
export type DNSNativeGroup = {
  id?: number
  name: string
  enabled?: boolean
  clientScopes: string[]
  listenerScopes: string[]
  domains: string[]
  translations?: Record<string, string>
}
export type DNSRecordChange = { name: string; type: "A" | "AAAA"; value: string; ttl?: number }
export type DNSClientGroupChange = { address: string; groups: number[] }
export type DNSDomainFilterChange = {
  domain: string
  disposition: "allow" | "deny"
  match: "suffix" | "exact"
  groups?: number[]
}
export type DNSSelectedFilter = {
  domain: string
  disposition: "allow" | "deny"
  match: "suffix" | "exact"
  present: boolean
  enabled?: boolean
  groups?: number[]
  comment: string | null
  commentReported: boolean
  ruleFingerprint?: string
  otherPolicyFingerprint: string
  evidence: DNSNativeReading
  owners: number
  exact: number
  inventoryCount: number
}
export type DNSSelectedClient = {
  address: string
  groups: number[]
  comment: string | null
  commentFingerprint: string
  otherPolicyFingerprint: string
}
export type DNSZoneRecord = {
  name: string
  type: string
  value?: string
  ttl: number
  disabled: boolean
  editable: boolean
  comments?: string
  fingerprint: string
}
export type DNSRecordInventory = {
  zone: string
  type: string
  disabled: boolean
  internal: boolean | null
  nativeVersion: string
  dnssec: string
  records: DNSZoneRecord[]
  evidence: DNSNativeReading
  fingerprint: string
}
export type DNSServiceSnapshot = {
  policyFingerprint: string
  observedAt: string
  engine: DNSEngine
  version: string
  roles: string[]
  transport: DNSNativeReading
  runtime: DNSNativeReading
  process?: { pid: number; startedAt: string; startedBefore: string }
  listeners: { address: string; port: number; protocol: string; scope: string }[]
  access: DNSNativeReading
  allowedClients: string[]
  deniedClients: string[]
  protection: boolean
  protectionTemporary: boolean
  upstreams: string[]
  upstreamProtocol: string
  clients: {
    id: string
    name: string
    addresses: string[]
    groups?: number[]
    filtering?: boolean
    inherited: boolean
  }[]
  clientEvidence: DNSNativeReading
  zones: { name: string; type: string; disabled: boolean; dnssec: string }[]
  zoneEvidence: DNSNativeReading
  views: DNSNativeReading
  viewGroups: DNSNativeGroup[]
  namedNetworks: Record<string, string[]>
  translationEnabled?: boolean
  filterGroups: DNSNativeGroup[]
  appProtection?: boolean
  appClientEvidence: DNSNativeReading
  localOverrides: { name: string; value: string; type: string; enabled?: boolean }[]
  overrideEvidence: DNSNativeReading
  queries: {
    at: string
    client: string
    name: string
    type: string
    status: string
    protocol?: string
  }[]
  queryEvidence: DNSNativeReading
  limitations: string[]
  records?: DNSRecordInventory
  selectionFingerprint?: string
  selectedClient?: DNSSelectedClient
  selectedFilter?: DNSSelectedFilter
}
export type DNSServiceView = {
  connection: DNSConnection
  snapshot?: DNSServiceSnapshot
  state: string
  error?: string
}
export type DNSChangeRequest =
  | { action: "protection"; protection: boolean }
  | { action: "upstreams"; upstreams: string[] }
  | { action: "access"; allowedClients: string[]; deniedClients: string[] }
  | { action: "zone_create"; zone: string }
  | { action: "override_add" | "override_remove"; record: Omit<DNSRecordChange, "ttl"> }
  | {
      action: "record_add" | "record_remove"
      zone: string
      record: DNSRecordChange & { ttl: number }
    }
  | { action: "client_groups"; client: DNSClientGroupChange }
  | { action: "filter_add" | "filter_remove"; filter: DNSDomainFilterChange }
export type DNSServiceChange = {
  id: string
  connectionId: string
  generation: number
  request: DNSChangeRequest
  before?: DNSServiceSnapshot
  after?: DNSServiceSnapshot
  state: string
  createdAt: string
  expiresAt: string
  endedAt?: string
  error?: string
}
export type DNSProvisionRequest = {
  name: string
  engine: DNSEngine
  managementPort: number
  dnsPort: number
  memoryMiB: number
  cpus: number
  upstreams: string[]
  management: boolean
  username: string
  password?: string
}
export type DNSServiceProvision = {
  id: string
  request: DNSProvisionRequest
  image: string
  imageId: string
  owner: string
  resources: {
    networkName: string
    networkId?: string
    volumes: string[]
    containerName: string
    containerId?: string
    phase: string
  }
  connectionId?: string
  state: string
  createdAt: string
  expiresAt: string
  endedAt?: string
  error?: string
  limitations: string[]
}

const object = (value: unknown): value is Record<string, unknown> =>
  typeof value === "object" && value !== null && !Array.isArray(value)
const text = (value: unknown): value is string => typeof value === "string"
const optionalText = (value: unknown) => value === undefined || text(value)
const optionalNativeText = (value: unknown) =>
  value === undefined || (text(value) && value.length <= 512)
const date = (value: unknown) => text(value) && Number.isFinite(Date.parse(value))
const engine = (value: unknown): value is DNSEngine =>
  DNS_ENGINES.some((item) => item.value === value)
const integer = (value: unknown, min = 0, max = 2147483647): value is number =>
  typeof value === "number" && Number.isInteger(value) && value >= min && value <= max
const strings = (value: unknown): value is string[] =>
  Array.isArray(value) && value.length <= 2048 && value.every(text)
const list = (value: unknown, check: (item: unknown) => boolean) =>
  Array.isArray(value) && value.length <= 2048 && value.every(check)
const reading = (value: unknown) =>
  object(value) && text(value.state) && text(value.basis) && text(value.summary)
const optionalBool = (value: unknown) => value === undefined || typeof value === "boolean"
const digest = (value: unknown): value is string => text(value) && /^[a-f0-9]{64}$/.test(value)
const keys = (value: Record<string, unknown>, allowed: string[]) =>
  Object.keys(value).every((key) => allowed.includes(key))
const groups = (value: unknown, max: number): value is number[] =>
  Array.isArray(value) &&
  value.length <= max &&
  Array.from(value).every((id) => integer(id)) &&
  new Set(value).size === value.length
const optionalDate = (value: unknown) => value === undefined || date(value)
const stringMap = (value: unknown, arrays: boolean) =>
  object(value) &&
  Object.keys(value).length <= 2048 &&
  Object.values(value).every(arrays ? strings : text)
const group = (value: unknown) =>
  object(value) &&
  text(value.name) &&
  (value.id === undefined || integer(value.id)) &&
  optionalBool(value.enabled) &&
  strings(value.clientScopes) &&
  strings(value.listenerScopes) &&
  strings(value.domains) &&
  (value.translations === undefined || stringMap(value.translations, false))

export function readDNSConnection(value: unknown): DNSConnection {
  if (
    !object(value) ||
    !text(value.id) ||
    !value.id ||
    !text(value.name) ||
    !engine(value.engine) ||
    !text(value.endpoint) ||
    !optionalText(value.serverName) ||
    typeof value.customCA !== "boolean" ||
    typeof value.management !== "boolean" ||
    typeof value.hasCredential !== "boolean" ||
    !integer(value.generation, 1) ||
    !text(value.ownership) ||
    !optionalText(value.containerId) ||
    !date(value.createdAt) ||
    !date(value.updatedAt)
  )
    throw new Error("Native DNS connection identity or permissions are incomplete.")
  return {
    id: value.id,
    name: value.name,
    engine: value.engine,
    endpoint: value.endpoint,
    serverName: value.serverName as string | undefined,
    customCA: value.customCA,
    management: value.management,
    hasCredential: value.hasCredential,
    generation: value.generation,
    ownership: value.ownership,
    containerId: value.containerId as string | undefined,
    createdAt: value.createdAt as string,
    updatedAt: value.updatedAt as string,
  }
}

export function readDNSSnapshot(value: unknown): DNSServiceSnapshot {
  if (
    !object(value) ||
    !engine(value.engine) ||
    !text(value.version) ||
    !text(value.policyFingerprint) ||
    !value.policyFingerprint ||
    !date(value.observedAt) ||
    !strings(value.roles) ||
    !strings(value.allowedClients) ||
    !strings(value.deniedClients) ||
    !strings(value.upstreams) ||
    !strings(value.limitations) ||
    !text(value.upstreamProtocol) ||
    typeof value.protection !== "boolean" ||
    typeof value.protectionTemporary !== "boolean" ||
    !optionalBool(value.translationEnabled) ||
    !optionalBool(value.appProtection) ||
    !stringMap(value.namedNetworks, true) ||
    !list(value.viewGroups, group) ||
    !list(value.filterGroups, group)
  )
    throw new Error("Native DNS inventory is incomplete or invalid.")
  for (const field of [
    "transport",
    "runtime",
    "access",
    "clientEvidence",
    "zoneEvidence",
    "views",
    "appClientEvidence",
    "overrideEvidence",
    "queryEvidence",
  ]) {
    if (!reading(value[field])) throw new Error("Native DNS evidence is incomplete or invalid.")
  }
  if (
    !list(
      value.listeners,
      (v) =>
        object(v) &&
        text(v.address) &&
        integer(v.port, 1, 65535) &&
        text(v.protocol) &&
        text(v.scope),
    ) ||
    !list(
      value.clients,
      (v) =>
        object(v) &&
        text(v.id) &&
        text(v.name) &&
        strings(v.addresses) &&
        optionalBool(v.filtering) &&
        typeof v.inherited === "boolean" &&
        (v.groups === undefined || list(v.groups, (n) => integer(n))),
    ) ||
    !list(
      value.zones,
      (v) =>
        object(v) &&
        text(v.name) &&
        text(v.type) &&
        typeof v.disabled === "boolean" &&
        text(v.dnssec),
    ) ||
    !list(
      value.localOverrides,
      (v) => object(v) && text(v.name) && text(v.value) && text(v.type) && optionalBool(v.enabled),
    ) ||
    !list(
      value.queries,
      (v) =>
        object(v) &&
        text(v.at) &&
        text(v.client) &&
        text(v.name) &&
        text(v.type) &&
        text(v.status) &&
        optionalText(v.protocol),
    )
  )
    throw new Error("Native DNS rows are incomplete or invalid.")
  if (
    value.process !== undefined &&
    (!object(value.process) ||
      !integer(value.process.pid, 1) ||
      !date(value.process.startedAt) ||
      !date(value.process.startedBefore))
  ) {
    throw new Error("Native DNS runtime identity is incomplete or invalid.")
  }
  if (value.selectionFingerprint !== undefined && !digest(value.selectionFingerprint))
    throw new Error("Native DNS selected policy fingerprint is invalid.")
  const records = value.records === undefined ? undefined : readDNSRecords(value.records)
  const selectedClient =
    value.selectedClient === undefined ? undefined : readDNSSelectedClient(value.selectedClient)
  const selectedFilter =
    value.selectedFilter === undefined
      ? undefined
      : readDNSSelectedFilter(value.selectedFilter, value.engine)
  if (
    ((records || selectedClient || selectedFilter) && !digest(value.selectionFingerprint)) ||
    [records, selectedClient, selectedFilter].filter(Boolean).length > 1 ||
    (records &&
      (value.engine !== "technitium" ||
        records.nativeVersion !== value.version ||
        records.fingerprint !== value.selectionFingerprint)) ||
    (selectedClient && value.engine !== "pihole") ||
    (selectedFilter &&
      !(value.engine === "adguard"
        ? /^v?0\.107\./.test(value.version)
        : value.engine === "pihole" && /^v?6\./.test(value.version)))
  )
    throw new Error("Native DNS selected inventory is inconsistent.")
  return { ...value, records, selectedClient, selectedFilter } as DNSServiceSnapshot
}

export function readDNSSelectedFilter(
  value: unknown,
  expectedEngine?: DNSEngine,
): DNSSelectedFilter {
  if (
    !object(value) ||
    !keys(value, [
      "domain",
      "disposition",
      "match",
      "present",
      "enabled",
      "groups",
      "comment",
      "commentReported",
      "ruleFingerprint",
      "otherPolicyFingerprint",
      "evidence",
      "owners",
      "exact",
      "inventoryCount",
    ]) ||
    !text(value.domain) ||
    !dnsDomainName(value.domain) ||
    (value.disposition !== "allow" && value.disposition !== "deny") ||
    (value.match !== "suffix" && value.match !== "exact") ||
    (expectedEngine && expectedEngine !== (value.match === "suffix" ? "adguard" : "pihole")) ||
    typeof value.present !== "boolean" ||
    !integer(value.inventoryCount, 0, 256) ||
    !integer(value.owners, 0, value.inventoryCount as number) ||
    !integer(value.exact, 0, value.owners as number) ||
    value.present !== (value.exact === 1) ||
    (value.exact > 0 ? !digest(value.ruleFingerprint) : value.ruleFingerprint !== undefined) ||
    !digest(value.otherPolicyFingerprint) ||
    !object(value.evidence) ||
    !keys(value.evidence, ["state", "basis", "summary"]) ||
    value.evidence.state !== "configured" ||
    value.evidence.basis !== "native_configuration" ||
    !text(value.evidence.summary) ||
    !value.evidence.summary ||
    value.evidence.summary.length > 512 ||
    !(value.comment === null || (text(value.comment) && value.comment.length <= 512)) ||
    typeof value.commentReported !== "boolean"
  )
    throw new Error("Native DNS selected domain-filter policy is incomplete or inconsistent.")
  const piRow = value.match === "exact" && value.exact > 0
  if (
    piRow
      ? typeof value.enabled !== "boolean" || !groups(value.groups, 64) || !value.commentReported
      : value.enabled !== undefined ||
        value.groups !== undefined ||
        value.comment !== null ||
        value.commentReported
  )
    throw new Error(
      "Native DNS selected domain-filter metadata does not match its engine or presence.",
    )
  return {
    domain: value.domain,
    disposition: value.disposition,
    match: value.match,
    present: value.present,
    ...(piRow
      ? { enabled: value.enabled as boolean, groups: [...(value.groups as number[])] }
      : {}),
    comment: value.comment,
    commentReported: value.commentReported,
    ...(value.ruleFingerprint === undefined
      ? {}
      : { ruleFingerprint: value.ruleFingerprint as string }),
    otherPolicyFingerprint: value.otherPolicyFingerprint,
    evidence: {
      state: value.evidence.state,
      basis: value.evidence.basis,
      summary: value.evidence.summary,
    },
    owners: value.owners,
    exact: value.exact,
    inventoryCount: value.inventoryCount,
  }
}

function readDNSSelectedClient(value: unknown): DNSSelectedClient {
  if (
    !object(value) ||
    !text(value.address) ||
    !dnsClientAddress(value.address) ||
    !groups(value.groups, 128) ||
    !(value.comment === null || (text(value.comment) && value.comment.length <= 512)) ||
    !digest(value.commentFingerprint) ||
    !digest(value.otherPolicyFingerprint)
  )
    throw new Error("Native DNS selected client policy is incomplete or invalid.")
  return {
    address: value.address,
    groups: [...value.groups],
    comment: value.comment,
    commentFingerprint: value.commentFingerprint,
    otherPolicyFingerprint: value.otherPolicyFingerprint,
  }
}

export function readDNSRecords(value: unknown, expectedZone?: string): DNSRecordInventory {
  if (
    !object(value) ||
    !text(value.zone) ||
    !dnsDomainName(value.zone) ||
    (expectedZone && value.zone !== expectedZone) ||
    !text(value.type) ||
    !value.type ||
    typeof value.disabled !== "boolean" ||
    !(value.internal === null || typeof value.internal === "boolean") ||
    !text(value.nativeVersion) ||
    !value.nativeVersion ||
    value.nativeVersion.length > 64 ||
    !text(value.dnssec) ||
    !reading(value.evidence) ||
    !digest(value.fingerprint) ||
    !Array.isArray(value.records) ||
    value.records.length > 256
  )
    throw new Error("Native authoritative record inventory is incomplete or changed.")
  const inventory = { ...value } as DNSRecordInventory
  inventory.records = value.records.map((record: unknown) => {
    if (
      !object(record) ||
      !text(record.name) ||
      !record.name ||
      record.name.length > 512 ||
      !(record.name === inventory.zone || record.name.endsWith(`.${inventory.zone}`)) ||
      !text(record.type) ||
      !record.type ||
      record.type.length > 512 ||
      !optionalNativeText(record.value) ||
      !integer(record.ttl, 0, 4294967295) ||
      typeof record.disabled !== "boolean" ||
      typeof record.editable !== "boolean" ||
      !optionalNativeText(record.comments) ||
      !digest(record.fingerprint)
    )
      throw new Error("Native authoritative record metadata is incomplete or invalid.")
    if (
      record.editable &&
      (!dnsRecordZoneEditable(inventory) ||
        record.disabled ||
        !validRecord(
          { name: record.name, type: record.type, value: record.value, ttl: record.ttl },
          true,
        ))
    )
      throw new Error("Unsupported native record cannot be declared editable.")
    return {
      name: record.name,
      type: record.type,
      value: record.value,
      ttl: record.ttl,
      disabled: record.disabled,
      editable: record.editable,
      comments: record.comments,
      fingerprint: record.fingerprint,
    } as DNSZoneRecord
  })
  return {
    zone: inventory.zone,
    type: inventory.type,
    disabled: inventory.disabled,
    internal: inventory.internal,
    nativeVersion: inventory.nativeVersion,
    dnssec: inventory.dnssec,
    records: inventory.records,
    evidence: inventory.evidence,
    fingerprint: inventory.fingerprint,
  }
}

function validRecord(value: Record<string, unknown>, authoritative: boolean) {
  if (
    !keys(value, authoritative ? ["name", "type", "value", "ttl"] : ["name", "type", "value"]) ||
    !text(value.name) ||
    !dnsDomainName(value.name) ||
    (value.type !== "A" && value.type !== "AAAA") ||
    !text(value.value) ||
    !dnsUnicastAddress(value.value, true)
  )
    return false
  const ip = dnsLiteralAddress(value.value)
  return Boolean(
    ip &&
    (value.type === "A" ? ip.bytes.length === 4 : ip.bytes.length === 16) &&
    (!authoritative || integer(value.ttl, 1, 86400)),
  )
}

export function readDNSView(value: unknown, expectedId?: string): DNSServiceView {
  if (!object(value) || !text(value.state) || !optionalText(value.error)) {
    throw new Error("Native DNS reading is incomplete.")
  }
  const connection = readDNSConnection(value.connection)
  if (expectedId && connection.id !== expectedId) throw new Error("Native DNS connection changed.")
  const snapshot = value.snapshot === undefined ? undefined : readDNSSnapshot(value.snapshot)
  if (snapshot && snapshot.engine !== connection.engine)
    throw new Error("Native DNS owner changed.")
  if (value.state === "available" && !snapshot)
    throw new Error("Native DNS reading has no inventory.")
  return { connection, snapshot, state: value.state, error: value.error as string | undefined }
}

export function readDNSChangeRequest(value: unknown, expectedEngine?: DNSEngine): DNSChangeRequest {
  if (!object(value)) throw new Error("Native DNS review has no intent.")
  switch (value.action) {
    case "protection":
      if (keys(value, ["action", "protection"]) && typeof value.protection === "boolean")
        return { action: value.action, protection: value.protection }
      break
    case "upstreams":
      if (
        keys(value, ["action", "upstreams"]) &&
        strings(value.upstreams) &&
        value.upstreams.length > 0 &&
        value.upstreams.length <= 16 &&
        value.upstreams.every(dnsClassicEndpoint)
      )
        return { action: value.action, upstreams: [...value.upstreams] }
      break
    case "access":
      if (
        (!expectedEngine || expectedEngine === "adguard") &&
        keys(value, ["action", "allowedClients", "deniedClients"]) &&
        strings(value.allowedClients) &&
        value.allowedClients.length > 0 &&
        value.allowedClients.length <= 128 &&
        (value.deniedClients === undefined ||
          (strings(value.deniedClients) && value.deniedClients.length <= 128)) &&
        [...value.allowedClients, ...((value.deniedClients as string[] | undefined) ?? [])].every(
          (prefix) => dnsPrefix(prefix),
        ) &&
        new Set([...value.allowedClients, ...((value.deniedClients as string[] | undefined) ?? [])])
          .size ===
          value.allowedClients.length + ((value.deniedClients as string[] | undefined) ?? []).length
      )
        return {
          action: value.action,
          allowedClients: value.allowedClients,
          deniedClients: (value.deniedClients ?? []) as string[],
        }
      break
    case "zone_create":
      if (
        (!expectedEngine || expectedEngine === "technitium") &&
        keys(value, ["action", "zone"]) &&
        text(value.zone) &&
        dnsDomainName(value.zone)
      )
        return { action: value.action, zone: value.zone }
      break
    case "override_add":
    case "override_remove":
      if (
        (!expectedEngine || expectedEngine === "adguard" || expectedEngine === "pihole") &&
        keys(value, ["action", "record"]) &&
        object(value.record) &&
        validRecord(value.record, false)
      )
        return {
          action: value.action,
          record: {
            name: value.record.name as string,
            type: value.record.type as "A" | "AAAA",
            value: value.record.value as string,
          },
        }
      break
    case "record_add":
    case "record_remove":
      if (
        (!expectedEngine || expectedEngine === "technitium") &&
        keys(value, ["action", "zone", "record"]) &&
        text(value.zone) &&
        dnsDomainName(value.zone) &&
        object(value.record) &&
        validRecord(value.record, true) &&
        text(value.record.name) &&
        (value.record.name === value.zone || value.record.name.endsWith(`.${value.zone}`))
      )
        return {
          action: value.action,
          zone: value.zone,
          record: {
            name: value.record.name,
            type: value.record.type as "A" | "AAAA",
            value: value.record.value as string,
            ttl: value.record.ttl as number,
          },
        }
      break
    case "client_groups":
      if (
        (!expectedEngine || expectedEngine === "pihole") &&
        keys(value, ["action", "client"]) &&
        object(value.client) &&
        keys(value.client, ["address", "groups"]) &&
        text(value.client.address) &&
        dnsClientAddress(value.client.address) &&
        groups(value.client.groups, 64)
      )
        return {
          action: value.action,
          client: { address: value.client.address, groups: [...value.client.groups] },
        }
      break
    case "filter_add":
    case "filter_remove": {
      const filter = value.filter
      if (
        keys(value, ["action", "filter"]) &&
        object(filter) &&
        keys(filter, ["domain", "disposition", "match", "groups"]) &&
        text(filter.domain) &&
        dnsDomainName(filter.domain) &&
        (filter.disposition === "allow" || filter.disposition === "deny") &&
        ((filter.match === "suffix" &&
          (!expectedEngine || expectedEngine === "adguard") &&
          filter.groups === undefined) ||
          (filter.match === "exact" &&
            (!expectedEngine || expectedEngine === "pihole") &&
            (value.action === "filter_add"
              ? groups(filter.groups, 64)
              : filter.groups === undefined)))
      )
        return {
          action: value.action,
          filter: {
            domain: filter.domain,
            disposition: filter.disposition,
            match: filter.match,
            ...(filter.groups === undefined ? {} : { groups: [...(filter.groups as number[])] }),
          },
        }
    }
  }
  throw new Error("Native DNS review intent is incomplete or unsupported.")
}

export function readDNSChange(value: unknown, expectedId?: string): DNSServiceChange {
  if (
    !object(value) ||
    !text(value.id) ||
    !value.id ||
    !text(value.connectionId) ||
    !integer(value.generation, 1) ||
    !text(value.state) ||
    !date(value.createdAt) ||
    !date(value.expiresAt) ||
    !optionalDate(value.endedAt) ||
    !optionalText(value.error) ||
    (expectedId && value.id !== expectedId)
  )
    throw new Error("Retained native DNS review is incomplete or changed.")
  const before = value.before === undefined ? undefined : readDNSSnapshot(value.before)
  const after = value.after === undefined ? undefined : readDNSSnapshot(value.after)
  if (before && after && before.engine !== after.engine)
    throw new Error("Retained native DNS owner changed.")
  const request = readDNSChangeRequest(value.request, before?.engine ?? after?.engine)
  for (const snapshot of [before, after]) {
    if (snapshot && dnsSelectedScopeProblem(request, snapshot))
      throw new Error("Retained native DNS selection is incomplete or changed.")
  }
  return {
    id: value.id,
    connectionId: value.connectionId,
    generation: value.generation,
    request,
    state: value.state,
    createdAt: value.createdAt as string,
    expiresAt: value.expiresAt as string,
    endedAt: value.endedAt as string | undefined,
    error: value.error as string | undefined,
    before,
    after,
  }
}

export function readDNSProvision(value: unknown, expectedId?: string): DNSServiceProvision {
  if (
    !object(value) ||
    !text(value.id) ||
    !value.id ||
    !text(value.state) ||
    !date(value.createdAt) ||
    !date(value.expiresAt) ||
    !optionalText(value.endedAt) ||
    !optionalText(value.error) ||
    !optionalText(value.connectionId) ||
    !text(value.image) ||
    !value.image ||
    !text(value.imageId) ||
    !value.imageId ||
    !text(value.owner) ||
    !value.owner ||
    !strings(value.limitations) ||
    !object(value.request) ||
    !text(value.request.name) ||
    !engine(value.request.engine) ||
    !integer(value.request.managementPort, 1024, 65535) ||
    !integer(value.request.dnsPort, 1024, 65535) ||
    !integer(value.request.memoryMiB, 128, 1024) ||
    typeof value.request.cpus !== "number" ||
    !Number.isFinite(value.request.cpus) ||
    value.request.cpus < 0.25 ||
    value.request.cpus > 2 ||
    !strings(value.request.upstreams) ||
    typeof value.request.management !== "boolean" ||
    !text(value.request.username) ||
    !object(value.resources) ||
    !text(value.resources.networkName) ||
    !optionalText(value.resources.networkId) ||
    !strings(value.resources.volumes) ||
    !text(value.resources.containerName) ||
    !optionalText(value.resources.containerId) ||
    !text(value.resources.phase) ||
    (expectedId && value.id !== expectedId)
  )
    throw new Error("Retained native DNS setup is incomplete or changed.")
  const request = value.request
  // A bootstrap password is request-only even if an unexpected server includes it.
  return {
    ...value,
    request: {
      name: request.name,
      engine: request.engine,
      managementPort: request.managementPort,
      dnsPort: request.dnsPort,
      memoryMiB: request.memoryMiB,
      cpus: request.cpus,
      upstreams: request.upstreams,
      management: request.management,
      username: request.username,
    },
  } as DNSServiceProvision
}

export function readDNSList<T>(value: unknown, read: (item: unknown) => T, limit = 64): T[] {
  if (!Array.isArray(value) || value.length > limit)
    throw new Error("Native DNS list is incomplete or exceeds its retained limit.")
  return value.map((item) => read(item))
}

export const dnsAttemptKey = (kind: "change" | "provision", id: string) => `${kind}:${id}`

export function readDNSAttempts(value: string | null): Set<string> {
  if (value === null) return new Set()
  const parsed: unknown = JSON.parse(value)
  if (
    !Array.isArray(parsed) ||
    parsed.length > 256 ||
    parsed.some(
      (item) => typeof item !== "string" || !/^(change|provision):[A-Za-z0-9_-]{1,128}$/.test(item),
    )
  )
    throw new Error(
      "Saved DNS attempts are unreadable. Apply remains held; inspect the retained result.",
    )
  return new Set(parsed)
}

export function dnsRetainedReview<T extends DNSServiceChange | DNSServiceProvision>(
  local: T | undefined,
  remote: T | undefined,
): T | undefined {
  if (!local) return remote
  if (!remote) return local
  if (local.id !== remote.id) return remote
  // A late read must not erase the terminal response of the mutation just completed.
  if (local.state === "removed" && remote.state !== "removed") return local
  if (local.state !== "planned" && remote.state === "planned") return local
  if (local.endedAt && remote.state === "applying") return local
  if (local.endedAt && remote.endedAt && Date.parse(local.endedAt) > Date.parse(remote.endedAt))
    return local
  return remote
}

export function dnsChangeOwnerProblem(change: DNSServiceChange, view?: DNSServiceView) {
  if (!view || view.state !== "available" || view.error || !view.snapshot)
    return "The native owner could not be read. Refresh its current policy before applying."
  if (
    view.connection.id !== change.connectionId ||
    view.connection.generation !== change.generation ||
    !view.connection.management ||
    !change.before ||
    view.snapshot.engine !== change.before.engine ||
    view.snapshot.policyFingerprint !== change.before.policyFingerprint
  )
    return "The native policy or connection changed. Read it and create a new review."
  if (
    dnsSelectedScopeProblem(change.request, view.snapshot) ||
    dnsSelectedScopeProblem(change.request, change.before) ||
    dnsDomainFilterBaselineProblem(change.request, change.before)
  )
    return "Refresh the exact reviewed native selection before applying. Ordinary inventory cannot replace it."
  if (
    (change.request.action === "filter_add" || change.request.action === "filter_remove") &&
    (view.snapshot.selectionFingerprint !== change.before.selectionFingerprint ||
      JSON.stringify(view.snapshot.selectedFilter) !== JSON.stringify(change.before.selectedFilter))
  )
    return "The selected native filter policy changed. Read it and create a new review."
  return undefined
}

function dnsSelectedScopeProblem(request: DNSChangeRequest, snapshot?: DNSServiceSnapshot) {
  if (
    ![
      "override_add",
      "override_remove",
      "record_add",
      "record_remove",
      "client_groups",
      "filter_add",
      "filter_remove",
    ].includes(request.action)
  )
    return false
  if (!snapshot || !digest(snapshot.selectionFingerprint)) return true
  if (request.action === "filter_add" || request.action === "filter_remove") {
    const filter = snapshot.selectedFilter
    return (
      snapshot.engine !== (request.filter.match === "suffix" ? "adguard" : "pihole") ||
      !filter ||
      filter.domain !== request.filter.domain ||
      filter.disposition !== request.filter.disposition ||
      filter.match !== request.filter.match ||
      filter.evidence.state !== "configured" ||
      Boolean(snapshot.records || snapshot.selectedClient)
    )
  }
  if (snapshot.selectedFilter) return true
  if (request.action === "record_add" || request.action === "record_remove")
    return (
      snapshot.engine !== "technitium" ||
      !snapshot.records ||
      snapshot.records.zone !== request.zone ||
      snapshot.records.fingerprint !== snapshot.selectionFingerprint ||
      Boolean(snapshot.selectedClient)
    )
  if (request.action === "client_groups")
    return (
      snapshot.engine !== "pihole" ||
      !snapshot.selectedClient ||
      snapshot.selectedClient.address !== request.client.address ||
      Boolean(snapshot.records)
    )
  return snapshot.engine === "technitium" || Boolean(snapshot.records || snapshot.selectedClient)
}

function dnsDomainFilterBaselineProblem(request: DNSChangeRequest, snapshot?: DNSServiceSnapshot) {
  if (request.action !== "filter_add" && request.action !== "filter_remove") return false
  const filter = snapshot?.selectedFilter
  if (!filter) return true
  if (request.action === "filter_add")
    return (
      filter.present || filter.owners !== 0 || filter.exact !== 0 || filter.inventoryCount >= 256
    )
  return (
    !filter.present ||
    filter.owners !== 1 ||
    filter.exact !== 1 ||
    !digest(filter.ruleFingerprint) ||
    (snapshot?.engine === "pihole" && filter.enabled !== true)
  )
}

export function readDNSCurrentChange(value: unknown, change: DNSServiceChange): DNSServiceView {
  const view = readDNSView(value, change.connectionId)
  if (
    view.connection.generation !== change.generation ||
    !change.before ||
    view.connection.engine !== change.before.engine
  )
    throw new Error("The current native DNS review owner or generation changed.")
  if (view.state === "available" && dnsSelectedScopeProblem(change.request, view.snapshot))
    throw new Error("The current reading does not cover the exact reviewed native selection.")
  return view
}

export function dnsSameProvisionResources(
  before: DNSServiceProvision,
  current: DNSServiceProvision,
) {
  return (
    before.id === current.id &&
    before.state === current.state &&
    before.owner === current.owner &&
    before.imageId === current.imageId &&
    JSON.stringify(before.resources) === JSON.stringify(current.resources)
  )
}

export function dnsSameReviewedIntent(
  before: DNSServiceChange | DNSServiceProvision,
  current: DNSServiceChange | DNSServiceProvision,
) {
  if (
    before.id !== current.id ||
    before.createdAt !== current.createdAt ||
    before.expiresAt !== current.expiresAt ||
    JSON.stringify(before.request) !== JSON.stringify(current.request)
  )
    return false
  if ("resources" in before && "resources" in current)
    return (
      before.owner === current.owner &&
      before.imageId === current.imageId &&
      before.image === current.image &&
      before.resources.containerName === current.resources.containerName &&
      before.resources.networkName === current.resources.networkName &&
      JSON.stringify(before.resources.volumes) === JSON.stringify(current.resources.volumes)
    )
  if (!("resources" in before) && !("resources" in current))
    return (
      before.connectionId === current.connectionId &&
      before.generation === current.generation &&
      before.before?.policyFingerprint === current.before?.policyFingerprint &&
      before.before?.selectionFingerprint === current.before?.selectionFingerprint &&
      JSON.stringify(before.before?.records) === JSON.stringify(current.before?.records) &&
      JSON.stringify(before.before?.selectedClient) ===
        JSON.stringify(current.before?.selectedClient) &&
      JSON.stringify(before.before?.selectedFilter) ===
        JSON.stringify(current.before?.selectedFilter)
    )
  return false
}

export function dnsReviewProblem(
  review: DNSServiceChange | DNSServiceProvision,
  kind: "change" | "provision",
  attempted: ReadonlySet<string>,
  now = Date.now(),
  connection?: DNSConnection,
): string | undefined {
  if (attempted.has(dnsAttemptKey(kind, review.id)))
    return "This review has an attempted apply. Read its retained outcome before creating another review."
  if (review.state !== "planned")
    return "This review has already been consumed or needs native-owner review."
  const expires = Date.parse(review.expiresAt)
  if (!Number.isFinite(expires) || !Number.isFinite(now) || expires <= now)
    return "This review expired. Refresh the native reading and create a new review."
  if (kind === "change") {
    const change = review as DNSServiceChange
    if (
      !connection?.management ||
      connection.id !== change.connectionId ||
      connection.generation !== change.generation ||
      !change.before ||
      change.before.engine !== connection.engine ||
      dnsSelectedScopeProblem(change.request, change.before) ||
      dnsDomainFilterBaselineProblem(change.request, change.before)
    ) {
      return "The connection or reviewed baseline changed. Refresh the native reading and review the draft again."
    }
  }
  if (kind === "provision" && (review as DNSServiceProvision).resources.phase !== "planned")
    return "This setup has already started. Read its retained outcome before creating another review."
  return undefined
}

export function dnsChangeName(request: DNSChangeRequest): string {
  switch (request.action) {
    case "protection":
      return request.protection ? "Enable native protection" : "Disable native protection"
    case "upstreams":
      return "Change native upstreams"
    case "access":
      return "Change allowed client scope"
    case "zone_create":
      return `Create primary zone ${request.zone}`
    case "override_add":
      return `Add local override ${request.record.name}`
    case "override_remove":
      return `Remove local override ${request.record.name}`
    case "record_add":
      return `Add ${request.record.type} record ${request.record.name}`
    case "record_remove":
      return `Remove ${request.record.type} record ${request.record.name}`
    case "client_groups":
      return `Change native client groups for ${request.client.address}`
    case "filter_add":
    case "filter_remove":
      return `${request.action === "filter_add" ? "Add" : "Remove"} ${request.filter.disposition} ${request.filter.match === "suffix" ? "domain-suffix" : "exact-domain"} filter ${request.filter.domain}`
  }
}
