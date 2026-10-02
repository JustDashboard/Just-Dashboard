import type { MongoServer } from "@/components/database/mongo/types"

/**
 * The server's counters, sampled, as readings that move.
 *
 * `serverStatus` counts things from the moment the server started —
 * operations, bytes, documents returned — and a count since start says
 * nothing about now. Two samples do: the difference between them, over the
 * time between them on the server's own clock, is a rate. The page keeps the
 * samples it has taken since it was opened and derives every rate from
 * neighbouring pairs, so nothing here is recorded history and nothing claims
 * to be.
 */

/** How many samples are kept: a few minutes at the page's pace. */
export const SAMPLE_CAP = 120

/**
 * The samples with one more. A sample that is not newer than the last — a
 * poll answered twice — is not a second point; and one whose server has been
 * up for less time than the last is another run of the server, whose counters
 * began again from zero: the record starts again with it.
 */
export function addSample(
  samples: readonly MongoServer[],
  next: MongoServer,
  cap = SAMPLE_CAP,
): MongoServer[] {
  const last = samples[samples.length - 1]
  if (last && next.timestamp <= last.timestamp) return samples as MongoServer[]
  if (last && next.uptime < last.uptime) return [next]
  return [...samples, next].slice(-cap)
}

/**
 * How fast a counter moved between two samples, per second. `null` where it
 * cannot be said: no time passed, or it went down — a server that restarted,
 * not a negative rate.
 */
export function rate(
  before: number | undefined,
  after: number | undefined,
  seconds: number,
): number | null {
  if (before === undefined || after === undefined || seconds <= 0 || after < before) return null
  return (after - before) / seconds
}

/** The six kinds of operation the server counts, in the order a chart stacks them. */
export const OPERATIONS = ["query", "insert", "update", "delete", "getmore", "command"] as const
export type Operation = (typeof OPERATIONS)[number]

/** One moment of the page's own record: gauges as read, rates over the interval before it. */
export type StatRow = {
  ts: number
  /** Every operation a second, and each kind of it. */
  ops: number | null
  query: number | null
  insert: number | null
  update: number | null
  delete: number | null
  getmore: number | null
  command: number | null
  connections: number
  active: number
  /** The cache in use as a share of its ceiling, 0–100; `null` where the engine has none. */
  cache: number | null
  cacheBytes: number | null
  dirtyBytes: number | null
  netIn: number | null
  netOut: number | null
  returned: number | null
  scanned: number | null
  /** Documents examined for each returned in the interval; `null` when none was returned. */
  targeting: number | null
  /** Mean time of a read, a write and a command in the interval, in microseconds. */
  readLatency: number | null
  writeLatency: number | null
  commandLatency: number | null
  queuedReaders: number
  queuedWriters: number
  /** Resident memory, in bytes. */
  resident: number
}

const MEBIBYTE = 1024 * 1024

/** The samples as chart rows. The first has gauges and no rates: nothing came before it. */
export function statRows(samples: readonly MongoServer[]): StatRow[] {
  return samples.map((sample, index) => {
    const before = index > 0 ? samples[index - 1] : undefined
    const seconds = before ? (sample.timestamp - before.timestamp) / 1000 : 0
    const moved = (pick: (server: MongoServer) => number | undefined) =>
      before ? rate(pick(before), pick(sample), seconds) : null
    const kinds = Object.fromEntries(
      OPERATIONS.map((kind) => [kind, moved((server) => server.opcounters[kind])]),
    ) as Record<Operation, number | null>
    const known = OPERATIONS.map((kind) => kinds[kind]).filter((value) => value !== null)
    const returned = moved((server) => server.documents.returned)
    const scanned = moved((server) => server.scanned.documents)
    /** The mean of an interval: how far the time moved over how far the count did. */
    const mean = (
      time: (server: MongoServer) => number,
      count: (server: MongoServer) => number,
    ): number | null => {
      if (!before) return null
      const micros = time(sample) - time(before)
      const operations = count(sample) - count(before)
      return micros < 0 || operations <= 0 ? null : micros / operations
    }
    const cache = sample.cache
    return {
      ts: sample.timestamp,
      ops: known.length > 0 ? known.reduce((sum, value) => sum + value, 0) : null,
      ...kinds,
      connections: sample.connections.current,
      active: sample.connections.active,
      cache: cache && cache.maxBytes > 0 ? (cache.bytes / cache.maxBytes) * 100 : null,
      cacheBytes: cache ? cache.bytes : null,
      dirtyBytes: cache ? cache.dirtyBytes : null,
      netIn: moved((server) => server.network.bytesIn),
      netOut: moved((server) => server.network.bytesOut),
      returned,
      scanned,
      targeting: returned !== null && scanned !== null && returned > 0 ? scanned / returned : null,
      readLatency: mean(
        (server) => server.latency.readsMicros,
        (server) => server.latency.readsOps,
      ),
      writeLatency: mean(
        (server) => server.latency.writesMicros,
        (server) => server.latency.writesOps,
      ),
      commandLatency: mean(
        (server) => server.latency.commandsMicros,
        (server) => server.latency.commandsOps,
      ),
      queuedReaders: sample.queue.queuedReaders,
      queuedWriters: sample.queue.queuedWriters,
      resident: sample.memory.resident * MEBIBYTE,
    }
  })
}

type Numeric = {
  [K in keyof StatRow]: StatRow[K] extends number | null ? K : never
}[keyof StatRow]

/** One series of the rows, without the moments it has no reading for. */
export function seriesOf(rows: readonly StatRow[], key: Exclude<Numeric, "ts">): number[] {
  return rows.map((row) => row[key]).filter((value): value is number => value !== null)
}

/** The newest reading of a series that has one. */
export function latest(rows: readonly StatRow[], key: Exclude<Numeric, "ts">): number | null {
  for (let index = rows.length - 1; index >= 0; index--) {
    const value = rows[index][key]
    if (value !== null) return value
  }
  return null
}

/**
 * Documents examined for each one returned since the server started: the
 * figure the tile opens on before the page has two samples of its own.
 */
export function lifetimeTargeting(server: MongoServer): number | null {
  return server.documents.returned > 0 ? server.scanned.documents / server.documents.returned : null
}

/** "1.3", "12", "140": a ratio in the fewest digits that say it. */
export function ratio(value: number): string {
  if (value === 0) return "0"
  if (value >= 100) return Math.round(value).toLocaleString("en-US")
  if (value >= 10) return value.toFixed(0)
  return value.toFixed(1)
}

/** How long a server has been up, for a member's row: "3d 4h", "12m". */
export function uptimeWords(seconds: number): string {
  if (seconds < 60) return `${Math.max(Math.round(seconds), 0)}s`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  if (hours < 48) return `${hours}h ${minutes % 60}m`
  const days = Math.floor(hours / 24)
  return `${days}d ${hours % 24}h`
}

/** How long an operation has been running, in the unit a person reads it in. */
export function runningWords(seconds: number): string {
  if (seconds < 1) return `${Math.round(seconds * 1000)} ms`
  if (seconds < 60) return `${seconds.toFixed(seconds < 10 ? 1 : 0)} s`
  return uptimeWords(seconds)
}
