import type { DotTone } from "@/components/status-dot"

export type FlowSettings = {
  enabled: boolean
  kernelObserverEnabled?: boolean
  intervalSeconds: number
  retentionDays: number
}
export type ObserverQuality = {
  events: string
  ringDrops: string
  budgetOmissions: string
  headerGaps: string
  identityGaps: string
  stateAdmissionGaps: string
  parserGaps: string
  byteGaps: string
  pendingOmissions: string
  attributionGaps: string
  readerBudgetPauses: string
  unsavedEvents: string
  timestampGaps: string
  shutdownTailUnknown: boolean
}
export type KernelObserverEvidence = {
  status: string
  reason: string
  batchId?: string
  attachmentsRetained?: boolean
  dockerStatus?: string
  dockerError?: string
  dockerCheckedAt?: string
  timestampReason?: string
  digest?: string
  kernelRelease?: string
  bootId?: string
  targetCgroup?: string
  startedAt?: string | null
  checkedAt?: string
  stoppedAt?: string | null
  programIds?: number[]
  linkIds?: number[]
  ringBytes?: number
  eventsPerSecond?: number
  socketCapacity?: number
  pendingCapacity?: number
  dockerSources?: number
  omittedDockerSources?: number
  quality?: ObserverQuality
}
export type FlowOwner = {
  status: string
  program?: string
  pid?: number
  startTicks?: string
  containerId?: string
  containerName?: string
  containerStartedAt?: string
  reason?: string
}
export type FlowRow = {
  id: string
  hour: string
  firstSeen: string
  lastSeen: string
  sourceId: string
  sourceName: string
  namespace: string
  bootId: string
  socket: {
    protocol: string
    state: string
    localAddress: string
    localEndpoint: string
    localPort: number
    remoteAddress?: string
    remoteEndpoint: string
    remotePort?: number
    cookie?: string
    inode?: string
    owner: FlowOwner
  }
  samples: number
  txBytes: string | null
  rxBytes: string | null
  retransmissions: string | null
  lostGaugeMax: string | null
  measuredIntervals: number
  txIntervals: number
  rxIntervals: number
  retransIntervals: number
  skippedIntervals: number
  evidence?: string
  socketCgroup?: string
  observedTxBytes?: string | null
  observedRxBytes?: string | null
  observedPackets?: string
  observedTxPackets?: string
  observedRxPackets?: string
  observedTxKnownPackets?: string
  observedRxKnownPackets?: string
  observedTxByteGaps?: string
  observedRxByteGaps?: string
  observedSyn?: string
  observedFin?: string
  observedRst?: string
  timestampUncertain?: boolean
}
export type FlowSource = {
  id: string
  name: string
  namespace?: string
  status: string
  error?: string
  sockets: number
  tcp: number
  udp: number
  tcpTxCounters: number
  tcpRxCounters: number
  unverifiedOwners: number
  truncated: boolean
  malformed: number
  identityUnavailable: number
  unconnectedUdp: number
  observedAt: string
}
export type FlowCycle = {
  at: string
  finishedAt: string
  bootId?: string
  kernelRelease?: string
  tool: string
  toolVersion?: string
  toolVersionError?: string
  toolVersionCheckedAt: string
  sources: FlowSource[]
  dockerStatus: string
  dockerError?: string
  omittedSources: number
  discardedIntervals: number
  elapsedMillis: number
  droppedEvents: string | null
  observer?: KernelObserverEvidence
}
export type FlowCoverageHour = {
  hour: string
  firstSampleAt: string
  lastSampleAt: string
  samples: number
  failedSources: number
  truncatedSources: number
  omittedSources: number
  discardedIntervals: number
  captureMillis: number
  maxCaptureMillis: number
  lastCycle: FlowCycle
  observerQuality?: ObserverQuality
}
export type FlowReport = {
  checkedAt: string
  from: string
  to: string
  status: string
  settings: FlowSettings
  recordingSince: string | null
  collectorStartedAt: string
  lastCycle: FlowCycle | null
  rows: FlowRow[]
  coverageHours: FlowCoverageHour[]
  truncated: boolean
  coverageTruncated: boolean
  retainedRows: number
  retainedBytes: number
  retainedFrom: string | null
  prunedRows: number
  error?: string
  coverage: string[]
  kernelObserver: KernelObserverEvidence
}

export function flowCounter(raw: string | null | undefined): bigint | null {
  if (typeof raw !== "string" || !/^\d{1,20}$/.test(raw)) return null
  const n = BigInt(raw)
  return n <= BigInt("18446744073709551615") ? n : null
}
export function flowBytes(raw: string | bigint | null | undefined): string {
  const n = typeof raw === "bigint" ? raw : flowCounter(raw)
  if (n === null) return "Unknown"
  const units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB", "EiB"]
  let unit = 0
  let divisor = BigInt(1)
  while (unit < units.length - 1 && n >= divisor * BigInt(1024)) {
    divisor *= BigInt(1024)
    unit++
  }
  if (unit === 0) return `${n} B`
  const tenths = (n * BigInt(10) + divisor / BigInt(2)) / divisor
  return `${tenths / BigInt(10)}.${tenths % BigInt(10)} ${units[unit]}`
}
export function flowTotals(rows: FlowRow[]) {
  const total = (key: "txBytes" | "rxBytes" | "retransmissions") => {
    const values = rows
      .filter((row) => row.socket.protocol === "tcp" && !row.evidence)
      .map((row) => flowCounter(row[key]))
    const known = values.filter((n): n is bigint => n !== null)
    return {
      value: known.length ? known.reduce((a, b) => a + b, BigInt(0)) : null,
      known: known.length,
    }
  }
  return { tx: total("txBytes"), rx: total("rxBytes"), retrans: total("retransmissions") }
}
export function flowIsKernelRow(row: FlowRow): boolean {
  return row.evidence === "kernel_transport_payload_observed"
}
export function flowKernelTotals(rows: FlowRow[]) {
  const observed = rows.filter(flowIsKernelRow)
  const sum = (key: keyof FlowRow) => {
    const values = observed.map((row) => flowCounter(row[key] as string | null | undefined))
    const known = values.filter((n): n is bigint => n !== null)
    return {
      value: known.length ? known.reduce((a, b) => a + b, BigInt(0)) : null,
      known: known.length,
      unknown: values.length - known.length,
    }
  }
  return {
    rows: observed.length,
    tx: sum("observedTxBytes"),
    rx: sum("observedRxBytes"),
    packets: sum("observedPackets"),
    txPackets: sum("observedTxPackets"),
    rxPackets: sum("observedRxPackets"),
    txKnown: sum("observedTxKnownPackets"),
    rxKnown: sum("observedRxKnownPackets"),
    txGaps: sum("observedTxByteGaps"),
    rxGaps: sum("observedRxByteGaps"),
  }
}
export function observerReading(status: string): { label: string; tone: DotTone } {
  switch (status) {
    case "off":
      return { label: "Observer off", tone: "unknown" }
    case "starting":
      return { label: "Observer starting", tone: "warning" }
    case "recording":
      return { label: "Observer active", tone: "running" }
    case "partial":
      return { label: "Partial observer coverage", tone: "warning" }
    case "interrupted":
      return { label: "Previous observer interrupted", tone: "warning" }
    default:
      return { label: "Observer unavailable", tone: "unknown" }
  }
}
export function flowReading(status: string): { label: string; tone: DotTone } {
  switch (status) {
    case "off":
      return { label: "Recorder off", tone: "unknown" }
    case "waiting":
      return { label: "Waiting for a sample", tone: "unknown" }
    case "recording":
      return { label: "Recorder active", tone: "running" }
    case "partial":
      return { label: "Partial native coverage", tone: "warning" }
    case "stale":
      return { label: "Latest sample is old", tone: "warning" }
    default:
      return { label: "Recorder unavailable", tone: "unknown" }
  }
}
export function yesterdayUTC(now = new Date()) {
  return new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate()) - 86400000)
    .toISOString()
    .slice(0, 10)
}
export function flowDateRange(day: string): { from: string; to: string } | null {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(day)) return null
  const at = new Date(`${day}T00:00:00Z`)
  if (!Number.isFinite(at.getTime()) || at.toISOString().slice(0, 10) !== day) return null
  return { from: at.toISOString(), to: new Date(at.getTime() + 86400000).toISOString() }
}
export function flowPolicyProblem(interval: string, days: string): string | null {
  if (!/^\d+$/.test(interval) || Number(interval) < 10 || Number(interval) > 300)
    return "Sample interval must be 10–300 seconds."
  if (!/^\d+$/.test(days) || Number(days) < 1 || Number(days) > 31)
    return "Retention must be 1–31 days."
  return null
}
export function flowOwnerName(owner: FlowOwner): string {
  if (owner.status === "verified_container" && owner.containerId)
    return owner.containerName || owner.containerId.slice(0, 12)
  if (owner.status === "verified_process" && owner.program)
    return `${owner.program} · PID ${owner.pid}`
  return "Owner unknown"
}
