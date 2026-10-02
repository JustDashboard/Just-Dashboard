import { nodeAt, parseDocument, type BsonNode } from "@/components/database/mongo/bson"

/**
 * What one collection has been asked to do, as the server counts it: the
 * `latencyStats` of `$collStats`. Every figure is cumulative since the server
 * started, like the server's own counters, so a rate is the difference
 * between two readings.
 */
export type CollectionCounters = {
  /** The server's clock when the counters were read, ms since 1970. */
  at: number
  reads: number
  writes: number
  commands: number
  /** Microseconds spent on reads, and on writes, in all. */
  readMicros: number
  writeMicros: number
}

/** The pipeline that reads them: one stage, which the preview route runs as the read it is. */
export const COUNTERS_PIPELINE = '[{ "$collStats": { "latencyStats": {} } }]'

const figure = (root: BsonNode, path: string[]): number | null => {
  const node = nodeAt(root, path)
  if (!node || node.type === "object" || node.type === "array") return null
  const value = Number(node.text)
  return Number.isFinite(value) ? value : null
}

/** The counters in a `$collStats` document, or `null` when it holds none (a server that does not keep them). */
export function countersOf(canonical: string | undefined): CollectionCounters | null {
  if (!canonical) return null
  let root: BsonNode
  try {
    root = parseDocument(canonical)
  } catch {
    return null
  }
  const reads = figure(root, ["latencyStats", "reads", "ops"])
  const writes = figure(root, ["latencyStats", "writes", "ops"])
  if (reads === null || writes === null) return null
  const moment = nodeAt(root, ["localTime"])
  const at = moment && moment.type === "date" ? Date.parse(moment.text) : Number.NaN
  return {
    at: Number.isNaN(at) ? 0 : at,
    reads,
    writes,
    commands: figure(root, ["latencyStats", "commands", "ops"]) ?? 0,
    readMicros: figure(root, ["latencyStats", "reads", "latency"]) ?? 0,
    writeMicros: figure(root, ["latencyStats", "writes", "latency"]) ?? 0,
  }
}

export type CollectionActivity = {
  /** Reads and writes in all since the server started. */
  total: number
  /** Reads and writes a second between the two readings; `null` with only one, or across a restart. */
  perSecond: number | null
  readsPerSecond: number | null
  writesPerSecond: number | null
  /** What a read took on average between the two readings, in microseconds; `null` when none ran. */
  readMicros: number | null
}

/**
 * A collection's activity from two readings of its counters. With one
 * reading there is only the total; counters that went down mean the server
 * restarted, and the rate starts again from the next pair.
 *
 * Reading the counters is itself a read of the collection, and the server
 * counts it: `own` is how many of the reads between the two readings were
 * the watcher's, and they are taken out — a collection nobody else touched
 * is at rest, not at one read every ten seconds.
 */
export function activityOf(
  before: CollectionCounters | undefined,
  now: CollectionCounters,
  own = 0,
): CollectionActivity {
  const total = now.reads + now.writes
  const seconds = before ? (now.at - before.at) / 1000 : 0
  if (!before || seconds <= 0 || now.reads < before.reads || now.writes < before.writes) {
    return { total, perSecond: null, readsPerSecond: null, writesPerSecond: null, readMicros: null }
  }
  const reads = Math.max(now.reads - before.reads - own, 0)
  const writes = now.writes - before.writes
  return {
    total,
    perSecond: (reads + writes) / seconds,
    readsPerSecond: reads / seconds,
    writesPerSecond: writes / seconds,
    readMicros: reads > 0 ? (now.readMicros - before.readMicros) / reads : null,
  }
}

/** Which collections are watched: the ones that hold documents of the reader's own, largest first. */
export function watched<T extends { name: string; type: string; system: boolean; size: number }>(
  collections: readonly T[],
  most: number,
): T[] {
  return collections
    .filter((entry) => entry.type === "collection" && !entry.system)
    .sort((a, b) => b.size - a.size || a.name.localeCompare(b.name))
    .slice(0, most)
}
