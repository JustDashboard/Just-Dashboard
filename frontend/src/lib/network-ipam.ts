export type IPAMFamily = "inet" | "inet6"
export type IPAMOwner =
  | "docker_network"
  | "wireguard_server"
  | "interface_address"
  | "namespace_address"
  | "provider_range"
export type IPAMPool = {
  id: string
  name: string
  prefix: string
  family: IPAMFamily
  allocationBits: number
  createdAt: string
  retiredAt?: string
}
export type IPAMReservation = {
  id: string
  poolId: string
  prefix: string
  family: IPAMFamily
  owner: IPAMOwner
  resource: string
  state: "reserved" | "handing_off" | "observed" | "review_required" | "released"
  acknowledgedUnknown: boolean
  unknownSources: string[]
  createdAt: string
  updatedAt: string
  releasedAt?: string
  startedBy: string
  nativeId?: string
  detail?: string
}
export type IPAMCoverage = {
  source: string
  state: "observed" | "unknown" | "unreadable"
  detail: string
  checkedAt: string
}
export type IPAMObservation = {
  prefix: string
  owner: string
  resource: string
  domain: string
  basis: string
}
export type IPAMConflict = {
  prefix: string
  owner: string
  resource: string
  basis: string
  reservationId?: string
  domain?: string
}
export type IPAMPreview = {
  prefix: string
  status: "known_overlap" | "unknown_coverage" | "no_known_overlap"
  conflicts: IPAMConflict[]
  coverage: IPAMCoverage[]
  checkedAt: string
  limitations: string[]
}
export type IPAMUtilization = {
  poolId: string
  totalAddresses: string
  totalBlocks: string
  reservedBlocks: string
  observedBlocks: string
  unavailableBlocks: string
  candidateBlocks: string
  coverage: "observed" | "unknown"
}
export type IPAMView = {
  pools: IPAMPool[]
  reservations: IPAMReservation[]
  inventory: {
    checkedAt: string
    finishedAt: string
    observations: IPAMObservation[]
    coverage: IPAMCoverage[]
  }
  utilization: IPAMUtilization[]
  limitations: string[]
}
export const reservationState: Record<IPAMReservation["state"], string> = {
  reserved: "Reserved plan",
  handing_off: "Owner response pending · allocation held",
  observed: "Owner response recorded · allocation held",
  review_required: "Review required · allocation held",
  released: "Released plan",
}
export const ownerLabel: Record<IPAMOwner, string> = {
  docker_network: "Docker network",
  wireguard_server: "WireGuard server",
  interface_address: "Interface address (advisory)",
  namespace_address: "Namespace address (advisory)",
  provider_range: "Provider range (declared plan)",
}
export function selectedReservation(rows: IPAMReservation[], id: string, owner: IPAMOwner) {
  return rows.find((row) => row.id === id && row.owner === owner && row.state === "reserved")
}
export function previewReading(preview: IPAMPreview) {
  if (preview.status === "known_overlap")
    return {
      label: "Known overlap",
      detail: "A native observation or active planning reservation intersects this prefix.",
    }
  if (preview.status === "unknown_coverage")
    return {
      label: "Coverage incomplete",
      detail:
        "No overlap was found in the readable evidence; unreadable and provider ranges remain unknown.",
    }
  return {
    label: "No known overlap",
    detail: "A snapshot of the supported owners; this does not prove foreign or provider absence.",
  }
}
export function ipamCounts(view?: IPAMView) {
  return {
    pools: view?.pools.filter((pool) => !pool.retiredAt).length ?? 0,
    reservations: view?.reservations.filter((row) => row.state !== "released").length ?? 0,
    observed: view?.inventory.observations.length ?? 0,
    gaps: view?.inventory.coverage.filter((row) => row.state !== "observed").length ?? 0,
  }
}
export function reservationLink(row: IPAMReservation): string | undefined {
  if (row.state !== "reserved") return
  if (row.owner === "docker_network")
    return `/docker/networks?ipamReservation=${encodeURIComponent(row.id)}`
  if (row.owner === "wireguard_server")
    return `/network/vpn?ipamReservation=${encodeURIComponent(row.id)}`
  // Interface/namespace owners retain advisory plans until a typed native handoff exists.
}
