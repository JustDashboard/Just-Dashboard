import { bytes, percent } from "@/lib/format"
import type { SectionId } from "@/components/database/engine"
import { gauge, heldSeconds, type Sample } from "@/components/database/home/samples"
import type { RedisServer } from "@/components/database/home/types"

/**
 * What a home says needs somebody.
 *
 * A SQL server has an advisor on the server, and its home shows the top of
 * that report. Redis and MongoDB have none, so for them the home reads the
 * same answers its tiles are drawn from and says, of each reading that has
 * crossed a line, what it means and which page acts on it. These are
 * readings of now, stated as such — not an audit of the server.
 */
export type Concern = {
  id: string
  level: "critical" | "warning" | "notice"
  title: string
  detail: string
  advice?: string
  /** The page of this database that acts on it. */
  section?: SectionId
  /** What part of the server it is about, as the finding's right-hand word. */
  about: string
}

const ORDER: Record<Concern["level"], number> = { critical: 0, warning: 1, notice: 2 }

/** Worst first; within a level, in the order they were found. */
export function ranked<T extends { level: Concern["level"] }>(concerns: readonly T[]): T[] {
  return concerns
    .map((concern, index) => ({ concern, index }))
    .sort((a, b) => ORDER[a.concern.level] - ORDER[b.concern.level] || a.index - b.index)
    .map((entry) => entry.concern)
}

const DAY = 24 * 3600 * 1000

/**
 * What the connection's own summary gives away, for an engine whose server
 * has no advisor to say it: a port the internet can reach, and a database
 * nobody has dumped.
 */
export function placementConcerns(
  input: {
    name: string
    exposure: string
    lastBackup?: string
    /** The engine can be dumped from here. */
    dumps: boolean
    /** Where it is reachable from is the dashboard's to change. */
    managed: boolean
  },
  now: number,
): Concern[] {
  const found: Concern[] = []
  if (input.exposure === "public") {
    found.push({
      id: "published-everywhere",
      level: "warning",
      title: "Its port answers to the internet",
      detail: `${input.name} listens on every interface of this server, so anybody who can reach the machine can try to sign in to it.`,
      advice: input.managed
        ? "Restrict it to this server, or to this server and its containers, under Settings."
        : "Bind it to this server alone in its own configuration; where it listens is shown under Settings.",
      section: "settings",
      about: "security",
    })
  }
  if (input.dumps) {
    const at = input.lastBackup ? Date.parse(input.lastBackup) : Number.NaN
    if (Number.isNaN(at)) {
      found.push({
        id: "no-backup",
        level: "warning",
        title: "No backup of it has been taken from here",
        detail:
          "Nothing in this dashboard's dump directory would bring it back after a bad write or a lost disk. A backup made by another tool is not seen here.",
        advice: "Take one now, and schedule them under Backups.",
        section: "backups",
        about: "reliability",
      })
    } else if (now - at > 7 * DAY) {
      found.push({
        id: "stale-backup",
        level: "warning",
        title: `The newest backup is ${Math.floor((now - at) / DAY)} days old`,
        detail: "Everything written since then would be lost with the server.",
        advice: "Take one now, and check the schedule under Backups.",
        section: "backups",
        about: "reliability",
      })
    }
  }
  return found
}

function grew(samples: readonly Sample[], key: string): number {
  const first = samples[0]?.counters[key]
  const last = samples[samples.length - 1]?.counters[key]
  return typeof first === "number" && typeof last === "number" && last > first ? last - first : 0
}

/** What a Redis-family server's own readings say is wrong with it now. */
export function redisConcerns(
  server: RedisServer | undefined,
  samples: readonly Sample[],
): Concern[] {
  const found: Concern[] = []
  const window = heldSeconds(samples)
  const over = window > 0 ? `in the last ${Math.round(window)} seconds` : "so far"

  if (server?.replication.role === "replica" && server.replication.primary?.up === false) {
    found.push({
      id: "replica-link-down",
      level: "critical",
      title: "The link to its primary is down",
      detail: `This replica last heard from ${server.replication.primary.addr} ${server.replication.primary.lastIoSecondsAgo} seconds ago, and is serving whatever it had then.`,
      section: "performance",
      about: "replication",
    })
  }
  const rdb = server?.persistence.rdb
  if (rdb?.lastStatus !== undefined && rdb.lastStatus !== "ok") {
    found.push({
      id: "save-failed",
      level: "critical",
      title: "The last snapshot failed",
      detail: `The server could not write its snapshot (${rdb.lastStatus}); ${rdb.changesSinceSave.toLocaleString("en-US")} changes are in memory only. By default it also refuses writes until one succeeds.`,
      advice: "The server's log says why — most often a full disk or a directory it may not write.",
      section: "logs",
      about: "persistence",
    })
  }
  const aof = server?.persistence.aof
  if (aof?.enabled && aof.lastWriteStatus !== undefined && aof.lastWriteStatus !== "ok") {
    found.push({
      id: "aof-write-failed",
      level: "critical",
      title: "The append-only file cannot be written",
      detail: `The last write to it ended ${aof.lastWriteStatus}, so commands since then are not on disk.`,
      section: "logs",
      about: "persistence",
    })
  }

  const used = gauge(samples, "used_memory") ?? server?.memory.used
  const limit = gauge(samples, "maxmemory") ?? server?.memory.max ?? 0
  if (used !== undefined && limit > 0) {
    const filled = (used / limit) * 100
    if (filled >= 90) {
      const evicts = server?.memory.policy !== undefined && server.memory.policy !== "noeviction"
      found.push({
        id: "memory-near-limit",
        level: evicts ? "warning" : "critical",
        title: `Memory is at ${percent(filled, 0)} of its limit`,
        detail: evicts
          ? `${bytes(used)} of ${bytes(limit)} is in use; past the limit the server evicts keys (${server?.memory.policy}).`
          : `${bytes(used)} of ${bytes(limit)} is in use; past the limit the server refuses every write.`,
        advice: "Raise maxmemory, or see what holds the memory under Performance.",
        section: "performance",
        about: "memory",
      })
    }
  }
  const evicted = grew(samples, "evicted_keys")
  if (evicted > 0) {
    found.push({
      id: "evicting",
      level: "warning",
      title: "Keys are being evicted",
      detail: `${evicted.toLocaleString("en-US")} keys were removed to make room ${over}. That is the policy working, and it is also data an application may expect to find.`,
      section: "performance",
      about: "memory",
    })
  }
  const rejected = grew(samples, "rejected_connections")
  if (rejected > 0) {
    found.push({
      id: "rejecting-connections",
      level: "warning",
      title: "Connections are being turned away",
      detail: `${rejected.toLocaleString("en-US")} connections were refused ${over}: the server is at its client limit.`,
      advice:
        "Raise maxclients under Settings, or find the client that does not close its connections.",
      section: "settings",
      about: "clients",
    })
  }
  if (rdb && aof && rdb.schedule === "" && !aof.enabled) {
    found.push({
      id: "nothing-saved",
      level: "notice",
      title: "Nothing is saved to disk",
      detail:
        "Snapshots are off and there is no append-only file, so a restart starts from an empty server. That is right for a cache and wrong for anything else.",
      section: "settings",
      about: "persistence",
    })
  }
  if (server && server.memory.max === 0 && server.memory.systemTotal) {
    const share = (server.memory.used / server.memory.systemTotal) * 100
    if (share >= 50) {
      found.push({
        id: "no-memory-limit",
        level: "warning",
        title: `No memory limit, and it holds ${percent(share, 0)} of this machine's memory`,
        detail: `With maxmemory unset the server grows until the machine runs out, and the kernel then ends a process — often this one. It uses ${bytes(server.memory.used)} of ${bytes(server.memory.systemTotal)}.`,
        advice: "Set maxmemory and an eviction policy under Settings.",
        section: "settings",
        about: "memory",
      })
    }
  }
  if (server?.notice) {
    found.push({
      id: "topology",
      level: "notice",
      title: server.mode === "cluster" ? "This is one node of a cluster" : "This is a sentinel",
      detail: server.notice,
      about: "topology",
    })
  }
  return found
}

/** What a MongoDB server's own readings say is wrong with it now. */
export function mongoConcerns(samples: readonly Sample[]): Concern[] {
  const found: Concern[] = []
  const open = gauge(samples, "connections")
  const left = gauge(samples, "connectionsAvailable")
  if (open !== undefined && left !== undefined && open + left > 0) {
    const taken = (open / (open + left)) * 100
    if (taken >= 80) {
      found.push({
        id: "connections-near-limit",
        level: taken >= 95 ? "critical" : "warning",
        title: `${percent(taken, 0)} of its connections are in use`,
        detail: `${open.toLocaleString("en-US")} are open and ${left.toLocaleString("en-US")} are left; when none are, every new client is refused.`,
        advice: "Who holds them is under Performance.",
        section: "performance",
        about: "connections",
      })
    }
  }
  const queued = (gauge(samples, "queuedReaders") ?? 0) + (gauge(samples, "queuedWriters") ?? 0)
  if (queued > 0) {
    found.push({
      id: "operations-queued",
      level: "warning",
      title: `${queued.toLocaleString("en-US")} operations are waiting for a lock`,
      detail:
        "Operations queue when the ones ahead of them hold what they need. A queue that stays is one slow operation in front of everything else.",
      advice: "The operations running now, and the way to stop one, are under Performance.",
      section: "performance",
      about: "locks",
    })
  }
  const cache = gauge(samples, "cachePercent")
  if (cache !== undefined && cache >= 95) {
    found.push({
      id: "cache-full",
      level: "warning",
      title: `The cache is ${percent(cache, 0)} full`,
      detail:
        "The storage engine keeps its cache about four-fifths full and evicts behind the work. Past that, the operations themselves are made to evict, and every one of them slows down.",
      section: "performance",
      about: "memory",
    })
  }
  const first = samples[0]
  const last = samples[samples.length - 1]
  if (first && last && first !== last) {
    const scanned = last.counters["scanned.documents"] - first.counters["scanned.documents"]
    const returned = last.counters["documents.returned"] - first.counters["documents.returned"]
    if (returned > 0 && scanned / returned >= 1000) {
      found.push({
        id: "query-targeting",
        level: "notice",
        title: `Queries read ${Math.round(scanned / returned).toLocaleString("en-US")} documents for each one they return`,
        detail:
          "A query that finds its documents through an index reads about as many as it returns. This many more is a collection being scanned whole.",
        advice: "The slow operations and the indexes each collection has are a page away.",
        section: "performance",
        about: "indexes",
      })
    }
  }
  return found
}
