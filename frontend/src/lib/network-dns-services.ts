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
  localOverrides: { name: string; value: string; type: string }[]
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
      (v) => object(v) && text(v.name) && text(v.value) && text(v.type),
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
  return value as DNSServiceSnapshot
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

function readChangeRequest(value: unknown): DNSChangeRequest {
  if (!object(value)) throw new Error("Native DNS review has no intent.")
  switch (value.action) {
    case "protection":
      if (typeof value.protection === "boolean")
        return { action: value.action, protection: value.protection }
      break
    case "upstreams":
      if (strings(value.upstreams) && value.upstreams.length > 0 && value.upstreams.length <= 16)
        return { action: value.action, upstreams: value.upstreams }
      break
    case "access":
      if (
        strings(value.allowedClients) &&
        value.allowedClients.length > 0 &&
        strings(value.deniedClients ?? [])
      )
        return {
          action: value.action,
          allowedClients: value.allowedClients,
          deniedClients: (value.deniedClients ?? []) as string[],
        }
      break
    case "zone_create":
      if (text(value.zone) && value.zone) return { action: value.action, zone: value.zone }
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
    !optionalText(value.endedAt) ||
    !optionalText(value.error) ||
    (expectedId && value.id !== expectedId)
  )
    throw new Error("Retained native DNS review is incomplete or changed.")
  return {
    id: value.id,
    connectionId: value.connectionId,
    generation: value.generation,
    request: readChangeRequest(value.request),
    state: value.state,
    createdAt: value.createdAt as string,
    expiresAt: value.expiresAt as string,
    endedAt: value.endedAt as string | undefined,
    error: value.error as string | undefined,
    before: value.before === undefined ? undefined : readDNSSnapshot(value.before),
    after: value.after === undefined ? undefined : readDNSSnapshot(value.after),
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
  return undefined
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
      before.before?.policyFingerprint === current.before?.policyFingerprint
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
      change.before.engine !== connection.engine
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
  }
}
