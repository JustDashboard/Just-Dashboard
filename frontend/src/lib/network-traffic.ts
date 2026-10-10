import type { DotTone } from "@/components/status-dot"
import type { MetricEvent } from "@/lib/types"
import { flowCounter, type FlowRow } from "@/lib/network-flows"

/** The host's TCP over one two-second step, every figure a second but `established`. */
export type TCPPoint = {
  t: number
  outSegs: number
  retrans: number
  opens: number
  failed: number
  resets: number
  established: number
}

/** TCP's own round-trip estimate over the host's non-loopback sockets. */
export type TCPLatency = {
  at: string
  sockets: number
  medianMs: number
  p90Ms: number
  maxMs: number
  retransmitting: number
  loopback: number
  truncated: boolean
  error?: string
}

export type Percentiles = {
  samples: number
  basisSeconds: number
  rxP50: number
  rxP95: number
  rxP99: number
  txP50: number
  txP95: number
  txP99: number
}

export type InterfaceQuota = {
  iface: string
  period: "day" | "week" | "month"
  direction: "both" | "rx" | "tx"
  limitBytes: number
  periodStart: number
  usedBytes: number
  estimatedBytes: number
  coverage: number
  retentionShort: boolean
  state: "ok" | "warning" | "exceeded" | "unmeasured"
  enforced: boolean
  createdBy?: string
  createdAt: number
}

export type TrafficAnnotation = MetricEvent & { source: "change" | "incident"; ref?: string }

export type ContainerTrafficDetail = {
  name: string
  rxBytes: number
  txBytes: number
  rxRate: number
  txRate: number
  lastSeen: number
  series: { t: number; rx: number; tx: number }[]
  windowSeconds: number
  id?: string
  image?: string
  state?: string
  project?: string
  service?: string
  networks: string[]
  dockerError?: string
}

export type RecordedHistory = {
  recording: boolean
  kernelObserver: boolean
  since?: string
  retentionDays: number
}

export type CakeTin = {
  name: string
  thresholdBytesPerSecond: number
  peakDelayUs: number
  avgDelayUs: number
  baseDelayUs: number
  sentPackets: number
  sentBytes: number
  drops: number
  ecnMarks: number
  sparseFlows: number
  bulkFlows: number
}

export type QdiscNode = { kind: string; handle: string; parent?: string; root: boolean }

export type ShapeOwnership = {
  verdict: "managed" | "kernel" | "preserved" | "refused"
  reason: string
}

export type AppliedQueue = {
  kind: string
  handle: string
  options: Record<string, string>
  appliedAt: string
}

export type Offload = {
  checked: boolean
  gro?: string
  lro?: string
  gso?: string
  tso?: string
  error?: string
}

export type UploadProfile = {
  diffserv: "besteffort" | "diffserv3" | "diffserv4"
  flowMode: "dual-srchost" | "triple-isolate" | "flows"
  nat: boolean
  wash: boolean
  ackFilter: boolean
  overhead: number
  mpu: number
  linkLayer: "noatm" | "atm" | "ptm"
  rttMillis: number
}

export type CongestionGroup = {
  algorithm: string
  sockets: number
  medianRttMs: number
  p90RttMs: number
  retransmitShare: number
  medianDeliveryMbit: number
  segmentsOut: number
  bytesSent: number
}

export type CongestionComparison = {
  at: string
  default: string
  groups: CongestionGroup[]
  loopback: number
  truncated: boolean
  error?: string
}

export type CongestionView = {
  now: CongestionComparison
  snapshots: {
    id: number
    at: string
    before: string
    after: string
    actor?: string
    comparison: CongestionComparison
  }[]
  note: string
}

export type EBPFPlatform = {
  kernel?: string
  jit?: string
  jitHarden?: string
  unprivilegedDisabled?: string
  btf: boolean
  bpffs: boolean
  statsEnabled: boolean
}

export type EBPFProgramDetail = {
  id: number
  type: string
  name?: string
  tag?: string
  loadedAt?: string
  uid: number
  bytesXlated: number
  bytesJited: number
  memlock: number
  mapIds: number[]
  runTimeNs?: number
  runCount?: number
  pinned?: string[]
  owners?: string[]
  gplCompatible: boolean
  jited: boolean
  btfId?: number
  verifiedInsns?: number
  avgRunNs?: number
  observer: boolean
  maps: {
    id: number
    type: string
    name?: string
    keyBytes: number
    valueBytes: number
    maxEntries: number
    memlock: number
    frozen: boolean
    pinned?: string[]
    error?: string
  }[]
  devices: { device: string; kind: string; programId: number; mode?: string }[]
  cgroups: {
    cgroup: string
    programId: number
    attachType: string
    flags?: string
    name?: string
  }[]
  links: {
    id: number
    type: string
    attachType?: string
    cgroupId?: number
    ifindex?: number
    netnsIno?: number
  }[]
  errors: string[]
}

export type ConnectionQuality = {
  readAt: string
  intervalSeconds: number
  closedSinceLast: number
  unconnectedUdp: number
  limits: string[]
}

export type ConnectionSocket = {
  protocol: string
  localAddress: string
  localPort: number
  remoteAddress: string
  remotePort: number
  status: string
  pid?: number
  process?: string
  firstSeen?: string
  rxBytes?: number
  txBytes?: number
  rttMs?: number
  retransmitted?: number
  congestion?: string
}

export type ClosedConnection = {
  protocol: string
  localAddress: string
  localPort: number
  remoteAddress: string
  remotePort: number
  process?: string
  firstSeen: string
  lastSeen: string
  goneBy: string
  observedSeconds: number
}

export type ConnectionDetail = {
  address: string
  private: boolean
  sockets: ConnectionSocket[]
  closed: ClosedConnection[]
  quality: ConnectionQuality
  countersError?: string
  countersTruncated: boolean
}

export type AddressBlock = {
  id: string
  address: string
  reason: string
  comment: string
  incidentRunId?: string
  createdBy?: string
  createdAt: string
  expiresAt?: string
  state: "active" | "expired" | "lifted"
  endedAt?: string
  endedBy?: string
  endError?: string
  rulePresent?: boolean
}

export type HandoffPhase = {
  key: "installed" | "configured" | "active" | "verified"
  status: "done" | "pending" | "failed" | "unknown" | "not_applicable"
  detail: string
}

export type PackageHandoff = {
  package: string
  checkedAt: string
  phases: HandoffPhase[]
  working: boolean
}

/**
 * How old the newest live reading is. The sampler steps every two seconds, so
 * three missed steps is a sampler that stopped, not a quiet link.
 */
export function observationAge(
  sampledAt: number | undefined,
  now: number,
  stepSeconds = 2,
): { seconds: number; stale: boolean; label: string } {
  if (!sampledAt) return { seconds: 0, stale: false, label: "Waiting for the first reading" }
  const seconds = Math.max(0, Math.round(now - sampledAt))
  const stale = seconds > stepSeconds * 3
  return { seconds, stale, label: stale ? `${seconds}s old — stale` : `${seconds}s old` }
}

/**
 * The share of TCP segments sent again over the last `steps` two-second
 * readings, or undefined where nothing was sent.
 */
export function retransmitShare(points: TCPPoint[] | undefined, steps = 30): number | undefined {
  const tail = (points ?? []).slice(-steps)
  const sent = tail.reduce((n, p) => n + p.outSegs, 0)
  if (sent <= 0) return undefined
  return tail.reduce((n, p) => n + p.retrans, 0) / sent
}

/** A share as a percent with one decimal, and below a tenth as "under 0.1%". */
export function shareLabel(share: number | undefined): string {
  if (share === undefined) return "—"
  if (share > 0 && share < 0.001) return "under 0.1%"
  return `${(share * 100).toFixed(1)}%`
}

/** A packet rate as the page writes it: 950/s, 1.2k/s, 3.4M/s. */
export function packetRate(perSecond: number | undefined): string {
  if (perSecond === undefined || Number.isNaN(perSecond)) return "—"
  if (perSecond >= 1e6) return `${(perSecond / 1e6).toFixed(1)}M/s`
  if (perSecond >= 1e3) return `${(perSecond / 1e3).toFixed(1)}k/s`
  return `${Math.round(perSecond)}/s`
}

/** Milliseconds the way a round trip is read: 0.4 ms, 12 ms, 1.2 s. */
export function millis(ms: number | undefined): string {
  if (ms === undefined || Number.isNaN(ms)) return "—"
  if (ms >= 1000) return `${(ms / 1000).toFixed(1)} s`
  if (ms < 1) return `${ms.toFixed(2)} ms`
  if (ms < 10) return `${ms.toFixed(1)} ms`
  return `${Math.round(ms)} ms`
}

/** A queueing delay in microseconds as milliseconds. */
export function delayLabel(us: number | undefined): string {
  return us === undefined ? "—" : millis(us / 1000)
}

/** A budget's state as its reading: percent used, a word and a tone. */
export function quotaReading(
  q: Pick<InterfaceQuota, "state" | "usedBytes" | "estimatedBytes" | "limitBytes">,
): {
  percent: number
  label: string
  tone: DotTone
} {
  const used = q.usedBytes + q.estimatedBytes
  const percent = q.limitBytes > 0 ? Math.min(100, (used / q.limitBytes) * 100) : 0
  switch (q.state) {
    case "exceeded":
      return { percent, label: "passed", tone: "danger" }
    case "warning":
      return { percent, label: "over 80%", tone: "warning" }
    case "ok":
      return { percent, label: "within", tone: "running" }
    default:
      return { percent: 0, label: "not measured", tone: "unknown" }
  }
}

/** A `datetime-local` value as unix seconds, or undefined when it is not one. */
export function localInstant(value: string): number | undefined {
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2})?$/.test(value)) return undefined
  const ms = new Date(value).getTime()
  return Number.isNaN(ms) ? undefined : Math.floor(ms / 1000)
}

/** Unix seconds as a `datetime-local` value in the browser's zone. */
export function localInput(seconds: number): string {
  const d = new Date(seconds * 1000)
  const pad = (n: number) => String(n).padStart(2, "0")
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

/** What is wrong with a typed range, or nothing; at most 31 days, ending by now. */
export function rangeProblem(from: number | undefined, to: number | undefined, now: number) {
  if (from === undefined || to === undefined) return "Choose both ends of the range."
  if (to <= from) return "The range ends before it starts."
  if (to - from > 31 * 86400) return "A range is at most 31 days."
  if (from >= now) return "The range starts in the future; nothing is recorded there."
  return undefined
}

function ipv4Bytes(s: string): number[] | undefined {
  const parts = s.split(".")
  if (parts.length !== 4) return undefined
  const out = parts.map((p) => (/^\d{1,3}$/.test(p) ? Number(p) : NaN))
  return out.every((n) => n >= 0 && n <= 255) ? out : undefined
}

function ipv6Bytes(s: string): number[] | undefined {
  if (!s.includes(":")) return undefined
  let text = s.split("%")[0].toLowerCase()
  let tail: number[] = []
  const lastColon = text.lastIndexOf(":")
  const v4 = ipv4Bytes(text.slice(lastColon + 1))
  if (v4) {
    tail = v4
    text = text.slice(0, lastColon) + ":0:0"
  }
  const halves = text.split("::")
  if (halves.length > 2) return undefined
  const words = (part: string) => (part === "" ? [] : part.split(":"))
  const head = words(halves[0])
  const rest = halves.length === 2 ? words(halves[1]) : []
  const missing = 8 - head.length - rest.length
  if ((halves.length === 1 && missing !== 0) || missing < 0) return undefined
  const all = [...head, ...Array(halves.length === 2 ? missing : 0).fill("0"), ...rest]
  const out: number[] = []
  for (const w of all) {
    if (!/^[0-9a-f]{1,4}$/.test(w)) return undefined
    const n = parseInt(w, 16)
    out.push(n >> 8, n & 255)
  }
  if (tail.length) out.splice(12, 4, ...tail)
  return out
}

/** An address as its bytes, IPv4-mapped IPv6 unmapped; undefined when it is not one. */
export function addressBytes(s: string): number[] | undefined {
  const v4 = ipv4Bytes(s.trim())
  if (v4) return v4
  const v6 = ipv6Bytes(s.trim())
  if (!v6) return undefined
  const mapped = v6.slice(0, 10).every((b) => b === 0) && v6[10] === 255 && v6[11] === 255
  return mapped ? v6.slice(12) : v6
}

/** Whether a rule's source — an address, a network, or anywhere — covers an address. */
export function sourceCovers(source: string | undefined, address: string): boolean {
  const s = (source ?? "").trim()
  const target = addressBytes(address)
  if (!target) return false
  if (!s || /^(any|anywhere|anywhere \(v6\))$/i.test(s)) return true
  const [net, bitsRaw] = s.split("/")
  const base = addressBytes(net)
  if (!base || base.length !== target.length) return false
  const bits = bitsRaw === undefined ? base.length * 8 : Number(bitsRaw)
  if (!Number.isInteger(bits) || bits < 0 || bits > base.length * 8) return false
  for (let i = 0; i < base.length; i++) {
    const take = Math.max(0, Math.min(8, bits - i * 8))
    const mask = take === 0 ? 0 : (0xff << (8 - take)) & 0xff
    if ((base[i] & mask) !== (target[i] & mask)) return false
  }
  return true
}

/** One remote address folded from retained socket history. */
export type HistoricalPeer = {
  address: string
  sockets: number
  protocols: string[]
  localPorts: number[]
  owners: string[]
  /** Exact native TCP counter deltas; null where no row measured any. */
  txBytes: bigint | null
  rxBytes: bigint | null
  firstSeen: string
  lastSeen: string
}

/** Retained rows folded by remote address, busiest first, in the live table's shape. */
export function foldHistoricalFlows(
  rows: FlowRow[],
  ownerName: (row: FlowRow) => string,
): HistoricalPeer[] {
  const by = new Map<
    string,
    HistoricalPeer & { ports: Set<number>; protos: Set<string>; who: Set<string> }
  >()
  for (const row of rows) {
    const address = row.socket.remoteAddress
    if (!address) continue
    let p = by.get(address)
    if (!p) {
      p = {
        address,
        sockets: 0,
        protocols: [],
        localPorts: [],
        owners: [],
        txBytes: null,
        rxBytes: null,
        firstSeen: row.firstSeen,
        lastSeen: row.lastSeen,
        ports: new Set(),
        protos: new Set(),
        who: new Set(),
      }
      by.set(address, p)
    }
    p.sockets++
    p.ports.add(row.socket.localPort)
    p.protos.add(row.socket.protocol)
    p.who.add(ownerName(row))
    const tx = flowCounter(row.txBytes)
    const rx = flowCounter(row.rxBytes)
    if (tx !== null) p.txBytes = (p.txBytes ?? BigInt(0)) + tx
    if (rx !== null) p.rxBytes = (p.rxBytes ?? BigInt(0)) + rx
    if (row.firstSeen < p.firstSeen) p.firstSeen = row.firstSeen
    if (row.lastSeen > p.lastSeen) p.lastSeen = row.lastSeen
  }
  return [...by.values()]
    .map(({ ports, protos, who, ...p }) => ({
      ...p,
      localPorts: [...ports].sort((a, b) => a - b),
      protocols: [...protos].sort(),
      owners: [...who].sort(),
    }))
    .sort((a, b) => b.sockets - a.sockets || a.address.localeCompare(b.address))
}

/** The last `n` whole UTC hours, newest first, as the flows API's hour bounds. */
export function recentHours(now: Date, n = 24): { from: string; to: string; label: string }[] {
  const top = Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate(), now.getUTCHours())
  const out = []
  for (let i = 0; i < n; i++) {
    const from = new Date(top - i * 3_600_000)
    const to = new Date(top - (i - 1) * 3_600_000)
    const hh = String(from.getUTCHours()).padStart(2, "0")
    out.push({
      from: from.toISOString().replace(".000Z", "Z"),
      to: to.toISOString().replace(".000Z", "Z"),
      label: `${from.toISOString().slice(0, 10)} ${hh}:00 UTC${i === 0 ? " (this hour)" : ""}`,
    })
  }
  return out
}

/** The lengths a block can be chosen for, in seconds; zero is until lifted. */
export const BLOCK_DURATIONS: { value: number; label: string }[] = [
  { value: 3600, label: "1 hour" },
  { value: 86400, label: "1 day" },
  { value: 7 * 86400, label: "7 days" },
  { value: 30 * 86400, label: "30 days" },
  { value: 0, label: "Until lifted" },
]

/** A block's standing in words: when it ends, or how it ended. */
export function blockStanding(b: AddressBlock, now: number): string {
  if (b.state === "lifted") return `Lifted${b.endedBy ? ` by ${b.endedBy}` : ""}`
  if (b.state === "expired") return "Ended on schedule"
  if (!b.expiresAt) return "Until lifted"
  const left = Math.round((new Date(b.expiresAt).getTime() - now) / 1000)
  if (left <= 0) return "Due to end now"
  const units: [number, string][] = [
    [86400, "d"],
    [3600, "h"],
    [60, "m"],
  ]
  for (const [size, unit] of units) {
    if (left >= size) return `Ends in ${Math.floor(left / size)}${unit}`
  }
  return `Ends in ${left}s`
}

const PHASE_LABEL: Record<HandoffPhase["key"], string> = {
  installed: "Installed",
  configured: "Configured",
  active: "Active",
  verified: "Verified",
}

/** A hand-off phase as its label and the tone its status takes. */
export function phaseReading(p: HandoffPhase): { label: string; tone: DotTone; word: string } {
  const label = PHASE_LABEL[p.key] ?? p.key
  switch (p.status) {
    case "done":
      return { label, tone: "running", word: "done" }
    case "failed":
      return { label, tone: "danger", word: "failed" }
    case "pending":
      return { label, tone: "warning", word: "not yet" }
    case "not_applicable":
      return { label, tone: "stopped", word: "not applicable" }
    default:
      return { label, tone: "unknown", word: "unknown" }
  }
}

/**
 * A span in unix seconds widened to whole UTC hours, the bounds socket history
 * is kept and read in, at most 31 days back from its end.
 */
export function hourBounds(from: number, to: number): { from: string; to: string } {
  const hour = 3600
  const end = Math.ceil(to / hour) * hour
  const start = Math.max(Math.floor(from / hour) * hour, end - 31 * 24 * hour)
  const iso = (s: number) => new Date(s * 1000).toISOString().replace(".000Z", "Z")
  return { from: iso(start), to: iso(end === start ? end + hour : end) }
}
