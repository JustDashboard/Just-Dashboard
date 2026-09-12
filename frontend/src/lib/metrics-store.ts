import { clock } from "@/lib/format"
import type { HostInfo, Snapshot } from "@/lib/types"

/**
 * The live metrics the Overview page draws, kept outside React.
 *
 * Two problems this solves, both of which come from the same mistake — owning
 * five minutes of history in a route component:
 *
 *  - Leaving the page threw the history away, so coming back showed an empty
 *    chart that took five minutes to fill in again. The stream now runs for as
 *    long as the dashboard shell is mounted, so the graph is continuous across
 *    navigation and there is no re-connect handshake on arrival.
 *  - Pushing every 2s frame through a context above the router would re-render
 *    the whole app — including the terminal and the log tail — twice a second.
 *    A store with explicit subscribers means only the components that actually
 *    read metrics re-render.
 *
 * The history is mirrored into sessionStorage so a browser reload keeps its
 * chart, and points older than STALE_MS are dropped on the way back in: a
 * graph that silently stitches this minute onto one from an hour ago is worse
 * than a graph that starts empty.
 */

export type MetricsPoint = {
  /** Clock label for the x-axis. */
  t: string
  /** Epoch ms, used for staleness and gap detection — never rendered. */
  ts: number
  cpu: number
  mem: number
  swap: number
  rx: number
  tx: number
  /** Fullest filesystem, as a percentage. The question is "is a disk about to fill up". */
  disk: number
  dread: number
  dwrite: number

  /** The CPU breakdown, so the live chart can answer "busy doing what". */
  cpuUser: number
  cpuSystem: number
  cpuIowait: number
  cpuSteal: number

  /** Pressure. -1 rather than 0 on a kernel without PSI, so the chart can
   *  leave a gap instead of drawing a confident flat line at zero. */
  psiCpu: number
  psiMem: number
  psiIo: number

  /** Operations per second and the worst device's service time. */
  dreads: number
  dwrites: number
  await: number
  busy: number

  tcp: number
  procsBlocked: number
}

export type ConnectionState = "connecting" | "open" | "closed" | "error"

export type MetricsState = {
  host?: HostInfo
  snapshot?: Snapshot
  history: MetricsPoint[]
  connection: ConnectionState
  /** Set only when the very first fetch failed and there is nothing to draw. */
  error?: string
}

/** How many samples the charts keep — roughly five minutes at 2s. */
export const HISTORY = 150

/** Restored history older than this is discarded rather than drawn. */
const STALE_MS = 10 * 60 * 1000

// v3: points gained the CPU breakdown, pressure, IOPS and socket counts. A
// point from an older shape restored into a newer chart would draw holes in
// the new series rather than a shorter line, so each version simply abandons
// the previous key instead of trying to migrate it.
const STORAGE_KEY = "just-dashboard.metrics.v3"

/** Persisting on every frame would be a synchronous JSON round trip at 2Hz. */
const PERSIST_EVERY = 5

const EMPTY: MetricsState = { history: [], connection: "closed" }

let state: MetricsState = EMPTY
let restored = false
let sincePersist = 0

const listeners = new Set<() => void>()

function emit() {
  for (const listener of listeners) listener()
}

function set(next: Partial<MetricsState>) {
  state = { ...state, ...next }
  emit()
}

export function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

export function getState(): MetricsState {
  if (!restored) {
    restored = true
    const history = readPersisted()
    if (history.length > 0) state = { ...state, history }
  }
  return state
}

/** The server renders no history at all; this keeps that snapshot stable. */
export function getServerState(): MetricsState {
  return EMPTY
}

export function setHost(host: HostInfo) {
  set({ host })
}

export function setConnection(connection: ConnectionState) {
  if (state.connection !== connection) set({ connection })
}

export function setError(error: string | undefined) {
  set({ error })
}

/** Seeds host and snapshot from the one-shot fetch, without clobbering the stream. */
export function seed(host: HostInfo, snapshot: Snapshot) {
  set({ host: state.host ?? host, snapshot: state.snapshot ?? snapshot })
}

/**
 * Writes the buffered history out now.
 *
 * Called when the page is about to go away, because persisting every fifth
 * sample otherwise loses up to ten seconds of chart across a reload — and a
 * reload is exactly the moment the gap is visible.
 */
export function flush() {
  if (sincePersist === 0) return
  sincePersist = 0
  persist(state.history)
}

export function pushSnapshot(snapshot: Snapshot) {
  const point = toPoint(snapshot)
  const history = getState().history
  const next =
    history.length >= HISTORY ? history.slice(history.length - HISTORY + 1) : [...history]
  next.push(point)
  set({ snapshot, history: next, error: undefined })

  if (++sincePersist >= PERSIST_EVERY) {
    sincePersist = 0
    persist(next)
  }
}

function toPoint(snapshot: Snapshot): MetricsPoint {
  let rx = 0
  let tx = 0
  for (const n of snapshot.net) {
    rx += n.recvRate
    tx += n.sendRate
  }
  let dread = 0
  let dwrite = 0
  let disk = 0
  let dreads = 0
  let dwrites = 0
  let await_ = 0
  let busy = 0
  for (const m of snapshot.mounts) {
    dread += m.readRate
    dwrite += m.writeRate
    dreads += m.readOps ?? 0
    dwrites += m.writeOps ?? 0
    // The fullest mount, not the mean: averaging a full /boot with an empty
    // /srv answers "is a disk about to fill up" wrongly, in the reassuring
    // direction. This matches what the backend records.
    if (m.usedPercent > disk) disk = m.usedPercent
    // Likewise the busiest device rather than the average of all of them: one
    // saturated disk is the problem, and three idle ones do not dilute it.
    if ((m.busyPercent ?? 0) > busy) busy = m.busyPercent
    const latency = Math.max(m.readLatencyMs ?? 0, m.writeLatencyMs ?? 0)
    if (latency > await_) await_ = latency
  }
  const psi = snapshot.pressure
  const supported = psi?.supported ?? false
  const ts = new Date(snapshot.ts).getTime()
  return {
    t: clock(snapshot.ts),
    ts: Number.isNaN(ts) ? Date.now() : ts,
    cpu: snapshot.cpu.totalPercent,
    mem: snapshot.memory.usedPercent,
    swap: snapshot.swap.usedPercent,
    rx,
    tx,
    disk,
    dread,
    dwrite,
    cpuUser: snapshot.cpu.modes?.user ?? 0,
    cpuSystem:
      (snapshot.cpu.modes?.system ?? 0) +
      (snapshot.cpu.modes?.irq ?? 0) +
      (snapshot.cpu.modes?.softirq ?? 0),
    cpuIowait: snapshot.cpu.modes?.iowait ?? 0,
    cpuSteal: snapshot.cpu.modes?.steal ?? 0,
    psiCpu: supported ? psi.cpuSome : -1,
    psiMem: supported ? psi.memSome : -1,
    psiIo: supported ? psi.ioSome : -1,
    dreads,
    dwrites,
    await: await_,
    busy,
    tcp: snapshot.sockets?.tcpInUse ?? 0,
    procsBlocked: snapshot.procs?.blocked ?? 0,
  }
}

function persist(history: MetricsPoint[]) {
  try {
    window.sessionStorage.setItem(STORAGE_KEY, JSON.stringify(history))
  } catch {
    // A full or disabled sessionStorage costs continuity across a reload, not
    // the live chart. Not worth surfacing.
  }
}

function readPersisted(): MetricsPoint[] {
  if (typeof window === "undefined") return []
  try {
    const raw = window.sessionStorage.getItem(STORAGE_KEY)
    if (!raw) return []
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    const cutoff = Date.now() - STALE_MS
    return parsed
      .filter(
        (p): p is MetricsPoint =>
          !!p && typeof p === "object" && typeof (p as MetricsPoint).ts === "number",
      )
      .filter((p) => p.ts >= cutoff)
      .slice(-HISTORY)
  } catch {
    return []
  }
}
