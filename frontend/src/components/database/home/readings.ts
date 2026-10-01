import { bytes, duration, percent, plural } from "@/lib/format"
import { utilisationTone } from "@/components/meter"
import type { Tone } from "@/components/tone"
import type { EngineNouns } from "@/components/database/engine"
import {
  MONGO_OPS,
  TRANSACTIONS,
  counter,
  drawn,
  gauge,
  gauges,
  heldSeconds,
  heldShare,
  lastRate,
  rates,
  type Sample,
} from "@/components/database/home/samples"
import type {
  DbBackupFile,
  DbServerStats,
  MongoCollections,
  RedisServer,
} from "@/components/database/home/types"

/**
 * A home's headline figures, as data.
 *
 * Each engine family has its own six — sessions and transactions on a SQL
 * server, operations and memory on Redis, the file and its pages on SQLite —
 * and what differs between them is only which figures, in which words. So a
 * family is a function from what the page has read to a list of these, and
 * one component draws them all: the same tile, the same order of parts, the
 * same answer to "this could not be read".
 */
export type Reading = {
  key: string
  label: string
  /** The figure. `undefined` is one that could not be read, and is drawn as a dash. */
  value: string | undefined
  /**
   * The figure as the whole number it is, for a count that is read once in a
   * while and then stands — how many tables. The tile counts up to it when it
   * lands (§11 *arrived*). A reading that moves with every poll is not given
   * one: its figure would never be still enough to read.
   */
  count?: number
  /** The figure's unit or its ceiling, set beside it. */
  trailing?: string
  hint?: string
  /** 0–100, where the figure has a ceiling. */
  meter?: number
  tone?: Tone
  /** The figure's shape over the samples held, where it moves. */
  trend?: { values: number[]; color: string; label: string; max?: number }
  /** Not read yet: the tile holds its place. */
  pending?: boolean
}

const COMPACT = new Intl.NumberFormat("en-US", { notation: "compact", maximumFractionDigits: 1 })

/** A count in the width of a tile: every digit up to ten thousand, "16.1M" past it. */
export function compact(value: number): string {
  return Math.abs(value) < 10_000
    ? Math.round(value).toLocaleString("en-US")
    : COMPACT.format(value)
}

/** A rate a second: one decimal while it is small enough to need it. */
export function perSecond(value: number): string {
  if (value <= 0) return "0"
  if (value < 10) return value.toFixed(1).replace(/\.0$/, "")
  return compact(value)
}

/**
 * How long ago an instant was, against the page's newest reading rather than
 * the wall clock, in the one unit a tile's figure has room for: an age is
 * read at a glance, and "3h 10s ago" is two figures where one was asked for.
 */
export function ago(at: number, now: number): string {
  const seconds = (now - at) / 1000
  if (seconds < 45) return "just now"
  if (seconds < 3600) return `${Math.max(1, Math.round(seconds / 60))}m ago`
  if (seconds < 48 * 3600) return `${Math.floor(seconds / 3600)}h ago`
  return `${Math.floor(seconds / 86_400)}d ago`
}

const SINCE_OPENED = "since this page was opened"

function rateTrend(samples: readonly Sample[], keys: string | readonly string[], what: string) {
  return { values: drawn(rates(samples, keys)), label: `${what} a second ${SINCE_OPENED}` }
}

/** The line a rate's tile carries until its second sample lands. */
const NEEDS_SECOND = "The rate needs a second reading"

/** Sessions on the server against its limit. */
function sessionsReading(samples: readonly Sample[]): Reading {
  const open = gauge(samples, "sessions")
  if (open === undefined) {
    return {
      key: "sessions",
      label: "Sessions",
      value: undefined,
      hint: "Not reported to this account",
    }
  }
  const max = gauge(samples, "sessionsMax") ?? 0
  const used = max > 0 ? (open / max) * 100 : undefined
  const waiting = gauge(samples, "sessionsWaiting") ?? 0
  const inTransaction = gauge(samples, "sessionsIdleInTransaction") ?? 0
  const parts = [`${compact(gauge(samples, "sessionsActive") ?? 0)} active`]
  if (waiting > 0) parts.push(`${compact(waiting)} waiting on a lock`)
  else if (inTransaction > 0) parts.push(`${compact(inTransaction)} idle in a transaction`)
  else parts.push(`${compact(gauge(samples, "sessionsIdle") ?? 0)} idle`)
  return {
    key: "sessions",
    label: "Sessions",
    value: compact(open),
    trailing: max > 0 ? `of ${compact(max)}` : "open",
    meter: used,
    tone: used === undefined ? "default" : utilisationTone(used),
    hint: parts.join(" · "),
  }
}

/** A cache's hits of everything asked of it. Under ninety is a server reading from disk. */
function cacheReading(samples: readonly Sample[]): Reading {
  const held = heldShare(samples, "blocksHit", "blocksRead")
  if (!held) {
    return { key: "cache", label: "Cache hit", value: undefined, hint: "No block reads reported" }
  }
  return {
    key: "cache",
    label: "Cache hit",
    value: percent(held.value, held.value >= 99.95 ? 0 : 1),
    // A share has a ceiling, so it fills a bar; how it moved is the chart's
    // Cache view. A level line pinned at a hundred drew a slab in its place.
    meter: held.value,
    tone: held.value < 90 ? "warning" : "default",
    hint:
      held.over === "window"
        ? `of block reads over the last ${duration(heldSeconds(samples))}`
        : "of block reads since the server started",
  }
}

function sizeReading(samples: readonly Sample[], label = "Size"): Reading {
  const size = gauge(samples, "databaseBytes")
  if (size === undefined) return { key: "size", label, value: undefined, hint: "Not reported" }
  const first = gauges(samples, "databaseBytes")[0] ?? size
  const grown = size - first
  return {
    key: "size",
    label,
    value: bytes(size),
    hint:
      grown === 0 ? "on disk" : `${grown > 0 ? "+" : "−"}${bytes(Math.abs(grown))} ${SINCE_OPENED}`,
  }
}

/**
 * A SQL server's own four: sessions against the limit, transactions, the
 * cache, and what it weighs. What it holds and when it was last backed up are
 * read elsewhere and joined to these by the page.
 */
export function sqlReadings(samples: readonly Sample[], stats?: DbServerStats): Reading[] {
  const rate = lastRate(samples, TRANSACTIONS)
  const counted = counter(samples, TRANSACTIONS)
  const read = lastRate(samples, "rowsRead")
  const written = lastRate(samples, "rowsWritten")
  const queries = lastRate(samples, "queries")
  const transactions: Reading =
    counted === undefined
      ? {
          key: "transactions",
          label: "Transactions",
          value: undefined,
          hint: stats?.notes?.[0] ?? "Not reported to this account",
        }
      : {
          key: "transactions",
          label: "Transactions",
          value: rate === undefined ? undefined : perSecond(rate),
          trailing: rate === undefined ? undefined : "a second",
          trend: { ...rateTrend(samples, TRANSACTIONS, "Transactions"), color: "var(--chart-1)" },
          hint:
            rate === undefined
              ? NEEDS_SECOND
              : read !== undefined && written !== undefined
                ? `${perSecond(read)} rows read · ${perSecond(written)} written`
                : queries !== undefined
                  ? `${perSecond(queries)} statements a second`
                  : `${compact(counted)} counted in all`,
        }
  return [sessionsReading(samples), transactions, cacheReading(samples), sizeReading(samples)]
}

/** A SQLite database is a file: what it weighs, how it is paged, how it journals. */
export function sqliteReadings(samples: readonly Sample[], stats?: DbServerStats): Reading[] {
  const file = gauge(samples, "fileBytes")
  const wal = gauge(samples, "walBytes")
  const pages = gauge(samples, "pageCount")
  const pageSize = gauge(samples, "pageSize")
  const free = gauge(samples, "freelistPages")
  const reclaimable = gauge(samples, "reclaimableBytes")
  const tables = gauge(samples, "tables")
  const indexes = gauge(samples, "indexes")
  const journal = stats?.facts?.journalMode
  const freeShare = pages && free !== undefined ? (free / pages) * 100 : undefined
  return [
    {
      key: "file",
      label: "File size",
      value: file === undefined ? undefined : bytes(file),
      hint:
        tables !== undefined
          ? `${plural(tables, "table")} · ${plural(indexes ?? 0, "index", "indexes")}`
          : undefined,
    },
    {
      key: "wal",
      label: "WAL size",
      value: wal === undefined ? undefined : bytes(wal),
      hint: journal
        ? journal.toLowerCase() === "wal"
          ? "the write-ahead log beside the file"
          : `no write-ahead log in ${journal.toLowerCase()} mode`
        : undefined,
    },
    {
      key: "pages",
      label: "Pages",
      value: pages === undefined ? undefined : compact(pages),
      trailing: pageSize ? `× ${bytes(pageSize, 0)}` : undefined,
      hint: stats?.facts?.encoding ? `${stats.facts.encoding} text` : undefined,
    },
    {
      key: "free",
      label: "Free pages",
      value: free === undefined ? undefined : compact(free),
      meter: freeShare,
      // A quarter of the file empty is space a VACUUM would hand back.
      tone: freeShare !== undefined && freeShare >= 25 ? "warning" : "default",
      hint: reclaimable !== undefined ? `${bytes(reclaimable)} a vacuum would return` : undefined,
    },
    {
      key: "journal",
      label: "Journal mode",
      value: journal ? journal.toUpperCase() : undefined,
      hint: stats?.facts?.synchronous ? `synchronous ${stats.facts.synchronous}` : undefined,
    },
  ]
}

/** ClickHouse is read by its work: queries, inserts, parts, and the machine under it. */
export function clickhouseReadings(samples: readonly Sample[]): Reading[] {
  const queries = lastRate(samples, "queries")
  const failed = lastRate(samples, "failedQueries")
  const inserted = lastRate(samples, "insertedRows")
  const selected = lastRate(samples, "selectedRows")
  const running = gauge(samples, "runningQueries")
  const parts = gauge(samples, "totalParts")
  const widest = gauge(samples, "maxPartsPerPartition")
  const resident = gauge(samples, "memoryResident")
  const host = gauge(samples, "hostMemoryBytes")
  const diskFree = gauge(samples, "diskFreeBytes")
  const diskTotal = gauge(samples, "diskTotalBytes")
  const memoryUsed = resident !== undefined && host ? (resident / host) * 100 : undefined
  const diskUsed =
    diskFree !== undefined && diskTotal ? ((diskTotal - diskFree) / diskTotal) * 100 : undefined
  const counted = counter(samples, "queries")
  return [
    {
      key: "queries",
      label: "Queries",
      value: queries === undefined ? undefined : perSecond(queries),
      trailing: queries === undefined ? undefined : "a second",
      trend: { ...rateTrend(samples, "queries", "Queries"), color: "var(--chart-1)" },
      tone: failed !== undefined && failed > 0 ? "warning" : "default",
      hint:
        counted === undefined
          ? "Not reported to this account"
          : queries === undefined
            ? NEEDS_SECOND
            : failed !== undefined && failed > 0
              ? `${perSecond(failed)} failing a second`
              : `${compact(counted)} since it started`,
    },
    {
      key: "running",
      label: "Running queries",
      value: running === undefined ? undefined : compact(running),
      trend: {
        values: gauges(samples, "runningQueries"),
        color: "var(--chart-4)",
        label: `Running queries ${SINCE_OPENED}`,
      },
      hint: `${compact(gauge(samples, "runningMerges") ?? 0)} merges · ${compact(gauge(samples, "runningMutations") ?? 0)} mutations`,
    },
    {
      key: "inserted",
      label: "Rows inserted",
      value: inserted === undefined ? undefined : perSecond(inserted),
      trailing: inserted === undefined ? undefined : "a second",
      trend: { ...rateTrend(samples, "insertedRows", "Rows inserted"), color: "var(--chart-5)" },
      hint:
        counter(samples, "insertedRows") === undefined
          ? "Not reported to this account"
          : selected === undefined
            ? NEEDS_SECOND
            : `${perSecond(selected)} rows read a second`,
    },
    {
      key: "parts",
      label: "Parts",
      value: parts === undefined ? undefined : compact(parts),
      hint: widest !== undefined ? `at most ${compact(widest)} in one partition` : undefined,
    },
    {
      key: "memory",
      label: "Memory",
      value: resident === undefined ? undefined : bytes(resident),
      meter: memoryUsed,
      tone: memoryUsed === undefined ? "default" : utilisationTone(memoryUsed),
      hint: host ? `of this machine's ${bytes(host)}` : undefined,
    },
    {
      key: "disk",
      label: "Disk",
      value: diskFree === undefined ? undefined : bytes(diskFree),
      trailing: diskFree === undefined ? undefined : "free",
      meter: diskUsed,
      tone: diskUsed === undefined ? "default" : utilisationTone(diskUsed),
      hint:
        diskUsed !== undefined && diskTotal
          ? `${percent(diskUsed, 0)} of ${bytes(diskTotal)} used`
          : undefined,
    },
  ]
}

/** Redis and its kin: commands, memory against its limit, the cache's worth, who is on it. */
export function redisReadings(samples: readonly Sample[], server?: RedisServer): Reading[] {
  const now = samples[samples.length - 1]?.at
  const ops = lastRate(samples, "total_commands_processed")
  const commands = counter(samples, "total_commands_processed")
  const used = gauge(samples, "used_memory")
  const limit = gauge(samples, "maxmemory") ?? 0
  const machine = server?.memory.systemTotal
  const ceiling = limit > 0 ? limit : machine
  const filled = used !== undefined && ceiling ? (used / ceiling) * 100 : undefined
  const hit = heldShare(samples, "keyspace_hits", "keyspace_misses")
  const clients = gauge(samples, "connected_clients")
  const blocked = gauge(samples, "blocked_clients") ?? 0
  const keys = gauge(samples, "keys")
  const expiring = gauge(samples, "expires")
  const saved = gauge(samples, "rdb_last_save_time")
  const unsaved = gauge(samples, "rdb_changes_since_last_save")
  const failedSave =
    server?.persistence.rdb.lastStatus !== undefined && server.persistence.rdb.lastStatus !== "ok"
  return [
    {
      key: "ops",
      label: "Commands",
      value: ops === undefined ? undefined : perSecond(ops),
      trailing: ops === undefined ? undefined : "a second",
      trend: {
        ...rateTrend(samples, "total_commands_processed", "Commands"),
        color: "var(--chart-1)",
      },
      hint:
        commands === undefined
          ? "Not reported"
          : ops === undefined
            ? NEEDS_SECOND
            : `${compact(commands)} since it started`,
    },
    {
      key: "memory",
      label: "Memory",
      value: used === undefined ? undefined : bytes(used),
      trailing: limit > 0 ? `of ${bytes(limit)}` : undefined,
      meter: filled,
      // Without a limit the bar is the machine's memory, which is a fact and
      // not a ceiling the server will stop at: it takes no tone.
      tone: limit > 0 && filled !== undefined ? utilisationTone(filled) : "default",
      hint:
        limit > 0
          ? server?.memory.policy
            ? `policy ${server.memory.policy}`
            : undefined
          : machine
            ? `no limit set · this machine has ${bytes(machine)}`
            : "no limit set",
    },
    {
      key: "hits",
      label: "Hit rate",
      value: hit ? percent(hit.value, hit.value >= 99.95 ? 0 : 1) : undefined,
      meter: hit?.value,
      hint: !hit
        ? "No key has been looked up yet"
        : hit.over === "window"
          ? `of lookups over the last ${duration(heldSeconds(samples))}`
          : "of lookups since it started",
    },
    {
      key: "clients",
      label: "Clients",
      value: clients === undefined ? undefined : compact(clients),
      trailing: clients === undefined ? undefined : "connected",
      trend: {
        values: gauges(samples, "connected_clients"),
        color: "var(--chart-4)",
        label: `Connected clients ${SINCE_OPENED}`,
      },
      tone: blocked > 0 ? "warning" : "default",
      hint: blocked > 0 ? `${compact(blocked)} blocked on a command` : "none blocked",
    },
    {
      key: "keys",
      label: "Keys",
      value: keys === undefined ? undefined : compact(keys),
      hint:
        expiring !== undefined
          ? `${compact(expiring)} with an expiry · every database`
          : "in every database",
    },
    {
      key: "saved",
      label: "Last save",
      value:
        saved === undefined || now === undefined
          ? undefined
          : saved > 0
            ? ago(saved * 1000, now)
            : "Never",
      tone: failedSave ? "danger" : "default",
      hint: failedSave
        ? "The last snapshot failed"
        : unsaved !== undefined
          ? `${compact(unsaved)} changes since`
          : undefined,
    },
  ]
}

/** MongoDB's own three: operations, connections against what is left, the cache. */
export function mongoReadings(samples: readonly Sample[]): Reading[] {
  const ops = lastRate(samples, MONGO_OPS)
  const reads = lastRate(samples, ["ops.query", "ops.getmore"])
  const writes = lastRate(samples, ["ops.insert", "ops.update", "ops.delete"])
  const open = gauge(samples, "connections")
  const left = gauge(samples, "connectionsAvailable")
  const taken = open !== undefined && left !== undefined ? (open / (open + left)) * 100 : undefined
  const cache = gauge(samples, "cacheBytes")
  const cacheMax = gauge(samples, "cacheMaxBytes")
  const cacheUsed = gauge(samples, "cachePercent")
  const dirty = gauge(samples, "cacheDirtyBytes")
  return [
    {
      key: "ops",
      label: "Operations",
      value: ops === undefined ? undefined : perSecond(ops),
      trailing: ops === undefined ? undefined : "a second",
      trend: { ...rateTrend(samples, MONGO_OPS, "Operations"), color: "var(--chart-1)" },
      hint:
        counter(samples, "ops.command") === undefined
          ? "Not reported"
          : ops === undefined
            ? NEEDS_SECOND
            : `${perSecond(reads ?? 0)} reads · ${perSecond(writes ?? 0)} writes`,
    },
    {
      key: "connections",
      label: "Connections",
      value: open === undefined ? undefined : compact(open),
      trailing: open !== undefined && left !== undefined ? `of ${compact(open + left)}` : undefined,
      meter: taken,
      tone: taken === undefined ? "default" : utilisationTone(taken),
      hint: `${compact(gauge(samples, "connectionsActive") ?? 0)} active`,
    },
    {
      key: "cache",
      label: "Cache used",
      value: cache === undefined ? undefined : bytes(cache),
      trailing: cacheMax ? `of ${bytes(cacheMax, 0)}` : undefined,
      meter: cacheUsed,
      // The storage engine keeps its cache four-fifths full on purpose; only
      // one it can no longer evict from is a reading to act on.
      tone: cacheUsed !== undefined && cacheUsed >= 95 ? "warning" : "default",
      hint:
        cache === undefined
          ? "This storage engine reports no cache"
          : dirty !== undefined
            ? `${bytes(dirty)} waiting to be written`
            : undefined,
    },
  ]
}

/** What a database holds, counted in its engine's words. */
export type Holdings = {
  objects: number
  /** More exist than were counted. */
  more: boolean
  /** `undefined` where the engine keeps no estimate. */
  rows: number | undefined
}

export function holdingsReading(
  nouns: EngineNouns,
  holdings: Holdings | undefined,
  failed?: string,
): Reading {
  const label = nouns.objects[0].toUpperCase() + nouns.objects.slice(1)
  if (!holdings) {
    return failed
      ? { key: "objects", label, value: undefined, hint: "Could not be counted" }
      : { key: "objects", label, value: undefined, pending: true }
  }
  return {
    key: "objects",
    label,
    value: `${compact(holdings.objects)}${holdings.more ? "+" : ""}`,
    count: holdings.more ? undefined : holdings.objects,
    hint:
      holdings.rows === undefined
        ? `the engine keeps no ${nouns.row} estimate`
        : `about ${compact(holdings.rows)} ${holdings.rows === 1 ? nouns.row : nouns.rows}`,
  }
}

/** A document database's weight and count, from its collections. */
export function collectionReadings(
  nouns: EngineNouns,
  data: MongoCollections | undefined,
  failed?: string,
): Reading[] {
  if (!data) {
    return [
      failed
        ? { key: "size", label: "Data size", value: undefined, hint: "Could not be measured" }
        : { key: "size", label: "Data size", value: undefined, pending: true },
      holdingsReading(nouns, undefined, failed),
    ]
  }
  const own = data.collections.filter((collection) => !collection.system)
  const measured = own.filter((collection) => collection.statsKnown)
  const sum = (pick: (collection: (typeof own)[number]) => number) =>
    measured.reduce((total, collection) => total + pick(collection), 0)
  return [
    {
      key: "size",
      label: "Data size",
      value: bytes(sum((collection) => collection.size)),
      hint: `${bytes(sum((collection) => collection.storageSize))} on disk · ${bytes(sum((collection) => collection.indexSize))} of indexes`,
    },
    holdingsReading(nouns, {
      objects: own.length,
      more: false,
      rows: measured.length > 0 ? sum((collection) => collection.count) : undefined,
    }),
  ]
}

const WEEK = 7 * 24 * 3600 * 1000

/**
 * When the database was last dumped from here. Never, and older than a week,
 * are the two answers somebody has to act on.
 */
export function backupReading(
  input: {
    lastBackup?: string
    newest?: DbBackupFile
    /** A dump is being taken now. */
    running?: boolean
  },
  now: number,
): Reading {
  const taken = input.newest?.takenAt ?? input.lastBackup
  const at = taken ? Date.parse(taken) : Number.NaN
  if (Number.isNaN(at)) {
    return {
      key: "backup",
      label: "Last backup",
      value: "Never",
      tone: "warning",
      hint: input.running ? "A dump is being taken now" : "No dump of it has been taken here",
    }
  }
  const newest = input.newest
  return {
    key: "backup",
    label: "Last backup",
    value: ago(at, now),
    tone: now - at > WEEK ? "warning" : "default",
    hint: input.running
      ? "A dump is being taken now"
      : newest
        ? [bytes(newest.size), newest.tool ?? newest.format].join(" · ")
        : "in this dashboard's dump directory",
  }
}
