import type { DbServerStats, MongoStats, RedisStats } from "@/components/database/home/types"

/**
 * The readings a home keeps while it is open.
 *
 * The server answers `GET /databases/{id}/stats` with raw totals and keeps
 * nothing between two asks, so a rate exists only on the page: two samples,
 * the difference of a counter, divided by the time between them on the clock
 * the answer itself states. The page holds the last sixty samples — five
 * minutes of them at one every five seconds — and everything a tile's trend
 * or the chart draws is derived from that run. Nothing here is recorded
 * history, and the page says so.
 */
export type Sample = {
  /** When it was read, in milliseconds, on the clock the answer states. */
  at: number
  /** Raw totals that only grow. Never a rate. */
  counters: Record<string, number>
  /** Readings of now. */
  gauges: Record<string, number>
}

/** How many samples a home holds. */
export const WINDOW = 60

/**
 * The run with one more sample, oldest dropped past `max`. A sample that is
 * not newer than the last — a poll answered from a cache, a clock that
 * stepped back — is not a new reading, and the run is handed back as it was.
 */
export function pushSample(held: readonly Sample[], next: Sample, max = WINDOW): readonly Sample[] {
  const last = held[held.length - 1]
  if (last && next.at <= last.at) return held
  return [...held, next].slice(-max)
}

function total(sample: Sample, keys: readonly string[]): number | undefined {
  let sum = 0
  for (const key of keys) {
    const value = sample.counters[key]
    if (typeof value !== "number" || !Number.isFinite(value)) return undefined
    sum += value
  }
  return sum
}

const list = (keys: string | readonly string[]) => (typeof keys === "string" ? [keys] : keys)

/** How far a counter (or the sum of several) moved between two samples. */
function moved(a: Sample, b: Sample, keys: readonly string[]): number | undefined {
  const before = total(a, keys)
  const after = total(b, keys)
  if (before === undefined || after === undefined) return undefined
  // A total that went down is a server that restarted or statistics that were
  // reset: the interval says nothing, and is left out rather than drawn as a
  // negative rate.
  return after < before ? undefined : after - before
}

/**
 * A counter as a rate a second between two samples. `undefined` where there
 * is none to state: the counter is missing, the clock did not move, or the
 * total went down.
 */
export function rateBetween(
  a: Sample,
  b: Sample,
  keys: string | readonly string[],
): number | undefined {
  const seconds = (b.at - a.at) / 1000
  if (!(seconds > 0)) return undefined
  const delta = moved(a, b, list(keys))
  return delta === undefined ? undefined : delta / seconds
}

/** One rate per interval of the run, aligned with its second sample onward. */
export function rates(
  samples: readonly Sample[],
  keys: string | readonly string[],
): (number | undefined)[] {
  return samples.slice(1).map((sample, index) => rateBetween(samples[index], sample, keys))
}

/** The newest interval's rate. */
export function lastRate(
  samples: readonly Sample[],
  keys: string | readonly string[],
): number | undefined {
  if (samples.length < 2) return undefined
  return rateBetween(samples[samples.length - 2], samples[samples.length - 1], keys)
}

/**
 * What share of two counters' movement the first one is, as a percentage: a
 * cache's hits of its hits and misses. `undefined` for an interval in which
 * neither moved — nothing was asked of the cache, which is not a 0% hit rate.
 */
export function shareBetween(
  a: Sample,
  b: Sample,
  hits: string | readonly string[],
  misses: string | readonly string[],
): number | undefined {
  const hit = moved(a, b, list(hits))
  const miss = moved(a, b, list(misses))
  if (hit === undefined || miss === undefined || hit + miss <= 0) return undefined
  return (hit / (hit + miss)) * 100
}

export function shares(
  samples: readonly Sample[],
  hits: string | readonly string[],
  misses: string | readonly string[],
): (number | undefined)[] {
  return samples.slice(1).map((sample, index) => shareBetween(samples[index], sample, hits, misses))
}

/**
 * The share over everything held, and failing that over the server's whole
 * life: an idle minute moves neither counter, and the tile still has a true
 * figure to show — the one the totals themselves give — as long as it says
 * which of the two it is.
 */
export function heldShare(
  samples: readonly Sample[],
  hits: string | readonly string[],
  misses: string | readonly string[],
): { value: number; over: "window" | "lifetime" } | undefined {
  const newest = samples[samples.length - 1]
  if (!newest) return undefined
  if (samples.length > 1) {
    const value = shareBetween(samples[0], newest, hits, misses)
    if (value !== undefined) return { value, over: "window" }
  }
  const hit = total(newest, list(hits))
  const miss = total(newest, list(misses))
  if (hit === undefined || miss === undefined || hit + miss <= 0) return undefined
  return { value: (hit / (hit + miss)) * 100, over: "lifetime" }
}

/** A gauge across the run, where it was reported. */
export function gauges(samples: readonly Sample[], key: string): number[] {
  return samples.flatMap((sample) => {
    const value = sample.gauges[key]
    return typeof value === "number" && Number.isFinite(value) ? [value] : []
  })
}

/** The newest sample's reading of a gauge. */
export function gauge(samples: readonly Sample[], key: string): number | undefined {
  const value = samples[samples.length - 1]?.gauges[key]
  return typeof value === "number" && Number.isFinite(value) ? value : undefined
}

/** The newest sample's total of a counter. */
export function counter(samples: readonly Sample[], key: string): number | undefined {
  const value = samples[samples.length - 1]?.counters[key]
  return typeof value === "number" && Number.isFinite(value) ? value : undefined
}

/** A series with its holes left out, for a trend that only wants a shape. */
export function drawn(values: readonly (number | undefined)[]): number[] {
  return values.filter((value): value is number => value !== undefined)
}

/** How long the held run covers, in seconds. */
export function heldSeconds(samples: readonly Sample[]): number {
  if (samples.length < 2) return 0
  return (samples[samples.length - 1].at - samples[0].at) / 1000
}

/** Where one line of a chart comes from in a sample. */
export type Source =
  | { key: string; gauge: string }
  | { key: string; rate: string | readonly string[] }
  | { key: string; share: readonly [hits: string, misses: string] }

/** Whether the newest sample carries what a source reads. */
export function sourced(sample: Sample | undefined, source: Source): boolean {
  if (!sample) return false
  if ("gauge" in source) return typeof sample.gauges[source.gauge] === "number"
  if ("rate" in source) return total(sample, list(source.rate)) !== undefined
  return total(sample, source.share) !== undefined
}

/**
 * The run as the rows a chart draws: one per sample, each line's value under
 * its key. A rate or a share belongs to the interval that ends at the sample,
 * so the first row holds gauges only, and a hole in a series is a key left
 * out of its row — a gap in the line, never a zero.
 */
export function chartRows(
  samples: readonly Sample[],
  sources: readonly Source[],
): ({ ts: number } & Record<string, number>)[] {
  return samples.map((sample, index) => {
    const row: { ts: number } & Record<string, number> = { ts: sample.at }
    const before = index > 0 ? samples[index - 1] : undefined
    for (const source of sources) {
      let value: number | undefined
      if ("gauge" in source) {
        const reading = sample.gauges[source.gauge]
        value = typeof reading === "number" && Number.isFinite(reading) ? reading : undefined
      } else if (before && "rate" in source) {
        value = rateBetween(before, sample, source.rate)
      } else if (before && "share" in source) {
        value = shareBetween(before, sample, source.share[0], source.share[1])
      }
      if (value !== undefined) row[source.key] = value
    }
    return row
  })
}

const numbers = (record: Record<string, unknown> | undefined): Record<string, number> =>
  Object.fromEntries(
    Object.entries(record ?? {}).filter(
      (entry): entry is [string, number] =>
        // A flavour that does not keep a figure reports it as -1.
        typeof entry[1] === "number" && Number.isFinite(entry[1]) && entry[1] >= 0,
    ),
  )

/** Every transaction a SQL engine counted, under one name. */
export const TRANSACTIONS = "transactionsAll"

/** A SQL engine's snapshot as a sample. The session counts ride with the gauges. */
export function sqlSample(stats: DbServerStats): Sample | undefined {
  const at = Date.parse(stats.at)
  if (Number.isNaN(at)) return undefined
  const sessions = stats.connections
  const counters = numbers(stats.counters)
  // Engines count transactions two ways — one total, or commits and rollbacks
  // apart — and the home reads one figure whichever it was.
  const transactions =
    counters.transactions ??
    (counters.transactionsCommitted !== undefined
      ? counters.transactionsCommitted + (counters.transactionsRolledBack ?? 0)
      : undefined)
  if (transactions !== undefined) counters[TRANSACTIONS] = transactions
  return {
    at,
    counters,
    gauges: {
      ...numbers(stats.gauges),
      // A snapshot the engine refused carries no size; a zero there is the
      // absence of an answer, and is left out rather than drawn as one.
      ...(stats.supported === false ? {} : { databaseBytes: stats.databaseBytes }),
      // A server that answers has the session that asked on it. A count of
      // none is an account that may not list sessions (MySQL without
      // PROCESS answers a list with nothing in it): no reading, not a zero.
      ...(sessions && sessions.total > 0
        ? numbers({
            sessions: sessions.total,
            sessionsActive: sessions.active,
            sessionsIdle: sessions.idle,
            sessionsIdleInTransaction: sessions.idleInTransaction,
            sessionsWaiting: sessions.waiting,
            sessionsMax: sessions.max,
          })
        : {}),
      ...(stats.uptimeSeconds !== undefined ? { uptimeSeconds: stats.uptimeSeconds } : {}),
    },
  }
}

/** INFO's totals that only grow; every other figure of it is a reading of now. */
const REDIS_COUNTERS = new Set([
  "total_commands_processed",
  "total_connections_received",
  "total_net_input_bytes",
  "total_net_output_bytes",
  "rejected_connections",
  "expired_keys",
  "evicted_keys",
  "keyspace_hits",
  "keyspace_misses",
])

export function redisSample(stats: RedisStats): Sample | undefined {
  const at = stats.server?.sampledAtMs
  if (typeof at !== "number") return undefined
  const all = numbers(stats.server.counters)
  const counters: Record<string, number> = {}
  const readings: Record<string, number> = {}
  for (const [key, value] of Object.entries(all)) {
    if (REDIS_COUNTERS.has(key)) counters[key] = value
    else readings[key] = value
  }
  return { at, counters, gauges: readings }
}

const MEBIBYTE = 1024 * 1024

/** MongoDB's `serverStatus`, flattened to the names the home's tiles and chart read. */
export function mongoSample(stats: MongoStats): Sample | undefined {
  const server = stats.server
  const at = server?.timestamp
  if (typeof at !== "number") return undefined
  const ops = numbers(server.opcounters)
  const cache = server.cache
  return {
    at,
    counters: numbers({
      ...Object.fromEntries(Object.entries(ops).map(([name, value]) => [`ops.${name}`, value])),
      "network.in": server.network?.bytesIn,
      "network.out": server.network?.bytesOut,
      "network.requests": server.network?.numRequests,
      "documents.returned": server.documents?.returned,
      "scanned.documents": server.scanned?.documents,
    }),
    gauges: numbers({
      connections: server.connections?.current,
      connectionsAvailable: server.connections?.available,
      connectionsActive: server.connections?.active,
      cacheBytes: cache?.bytes,
      cacheMaxBytes: cache?.maxBytes,
      cacheDirtyBytes: cache?.dirtyBytes,
      cachePercent:
        cache?.bytes !== undefined && cache.maxBytes ? (cache.bytes / cache.maxBytes) * 100 : -1,
      // The server states its memory in mebibytes.
      residentBytes:
        server.mem?.resident !== undefined ? server.mem.resident * MEBIBYTE : undefined,
      queuedReaders: server.queue?.queuedReaders,
      queuedWriters: server.queue?.queuedWriters,
      uptimeSeconds: server.uptime,
    }),
  }
}

/** Every operation counter MongoDB keeps, as one rate. */
export const MONGO_OPS = [
  "ops.insert",
  "ops.query",
  "ops.update",
  "ops.delete",
  "ops.getmore",
  "ops.command",
] as const
