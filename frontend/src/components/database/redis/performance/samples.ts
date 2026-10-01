import type { RedisCounter, RedisStats } from "@/components/database/redis/types"

/**
 * The server's counters, sampled, as readings that move.
 *
 * Redis counts things from the moment it started — commands processed, hits,
 * misses, bytes in — and a count since start says nothing about now. Two
 * samples do: the difference between them over the time between them is a
 * rate. The page keeps the samples it has taken since it was opened and
 * derives every rate from neighbouring pairs, so nothing here is recorded
 * history and nothing claims to be.
 */
export type StatSample = {
  /** The dashboard's clock when the server answered, in milliseconds. */
  at: number
  counters: Partial<Record<RedisCounter, number>>
}

/** How many samples are kept: a few minutes at the page's pace. */
export const SAMPLE_CAP = 120

/** A new sample after the ones held; one taken at a moment already held is not a second point. */
export function addSample(
  samples: readonly StatSample[],
  stats: RedisStats["server"],
  cap = SAMPLE_CAP,
): StatSample[] {
  const last = samples[samples.length - 1]
  if (last && last.at >= stats.sampledAtMs) return samples as StatSample[]
  return [...samples, { at: stats.sampledAtMs, counters: stats.counters }].slice(-cap)
}

/**
 * How fast a counter moved between two samples, per second. `null` where it
 * cannot be said: the counter is missing from either, no time passed, or it
 * went down — which is a server that restarted, not a negative rate.
 */
export function rateBetween(
  before: StatSample,
  after: StatSample,
  counter: RedisCounter,
): number | null {
  const a = before.counters[counter]
  const b = after.counters[counter]
  const seconds = (after.at - before.at) / 1000
  if (a === undefined || b === undefined || seconds <= 0 || b < a) return null
  return (b - a) / seconds
}

/** One moment of the page's own record: gauges as read, rates over the interval before it. */
export type StatRow = {
  ts: number
  ops: number | null
  hits: number | null
  misses: number | null
  /** Hits as a share of lookups in the interval, 0–100; `null` when nothing was looked up. */
  hitRate: number | null
  memory: number | null
  rss: number | null
  clients: number | null
  blocked: number | null
  netIn: number | null
  netOut: number | null
  expired: number | null
  evicted: number | null
  keys: number | null
}

const gauge = (sample: StatSample, counter: RedisCounter): number | null => {
  const value = sample.counters[counter]
  // Dragonfly answers -1 for a counter it does not keep.
  return value === undefined || value < 0 ? null : value
}

/** The samples as chart rows. The first has gauges and no rates: nothing came before it. */
export function statRows(samples: readonly StatSample[]): StatRow[] {
  return samples.map((sample, index) => {
    const before = index > 0 ? samples[index - 1] : undefined
    const rate = (counter: RedisCounter) => (before ? rateBetween(before, sample, counter) : null)
    const hits = rate("keyspace_hits")
    const misses = rate("keyspace_misses")
    const lookups = (hits ?? 0) + (misses ?? 0)
    return {
      ts: sample.at,
      ops: rate("total_commands_processed"),
      hits,
      misses,
      hitRate: hits !== null && misses !== null && lookups > 0 ? (hits / lookups) * 100 : null,
      memory: gauge(sample, "used_memory"),
      rss: gauge(sample, "used_memory_rss"),
      clients: gauge(sample, "connected_clients"),
      blocked: gauge(sample, "blocked_clients"),
      netIn: rate("total_net_input_bytes"),
      netOut: rate("total_net_output_bytes"),
      expired: rate("expired_keys"),
      evicted: rate("evicted_keys"),
      keys: gauge(sample, "keys"),
    }
  })
}

/** One series of the rows, without the moments it has no reading for. */
export function seriesOf(rows: readonly StatRow[], key: Exclude<keyof StatRow, "ts">): number[] {
  return rows.map((row) => row[key]).filter((value): value is number => value !== null)
}

/** Hits as a share of every lookup since the server started, 0–100; `null` before the first. */
export function lifetimeHitRate(counters: StatSample["counters"]): number | null {
  const hits = counters.keyspace_hits
  const misses = counters.keyspace_misses
  if (hits === undefined || misses === undefined || hits + misses <= 0) return null
  return (hits / (hits + misses)) * 100
}

/** A count a second, in the fewest digits that say it: "2,146", "12.4", "0.3". */
export function perSecond(value: number): string {
  if (value >= 100) return Math.round(value).toLocaleString()
  if (value >= 10) return value.toFixed(1)
  return value === 0 ? "0" : value.toFixed(2).replace(/0$/, "")
}

/** Microseconds as the unit a person reads them in. */
export function micros(us: number): string {
  if (us >= 1_000_000) return `${(us / 1_000_000).toFixed(us >= 10_000_000 ? 0 : 1)} s`
  if (us >= 1000) return `${(us / 1000).toFixed(us >= 10_000 ? 0 : 1)} ms`
  return `${us >= 10 ? Math.round(us) : us.toFixed(1)} µs`
}
