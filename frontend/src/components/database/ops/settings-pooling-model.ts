import type {
  DbPooler,
  DbPoolerPool,
  DbPoolerSettings,
  DbPoolerStat,
  DbPoolerStats,
} from "@/lib/types"

/**
 * What the Pooling page works out before it draws: what a form holds against
 * the limits the server states, what a save would send, and the pooler's
 * figures in the words and units a reader compares.
 */

/** The settings as the form holds them: numbers are what is typed until they are read. */
export type PoolingDraft = {
  poolMode: DbPoolerSettings["poolMode"]
  poolSize: string
  maxClientConn: string
  authMode: DbPoolerSettings["authMode"]
}

export function draftOfPooling(settings: DbPoolerSettings): PoolingDraft {
  return {
    poolMode: settings.poolMode,
    poolSize: String(settings.poolSize),
    maxClientConn: String(settings.maxClientConn),
    authMode: settings.authMode,
  }
}

/** A whole number, or `undefined` for anything else typed. */
function whole(text: string): number | undefined {
  return /^\d{1,6}$/.test(text.trim()) ? Number(text.trim()) : undefined
}

/** What is wrong with each typed number, held to the limits the server states. */
export function poolingProblems(
  draft: PoolingDraft,
  limits: DbPooler["limits"],
): { poolSize?: string; maxClientConn?: string } {
  const size = whole(draft.poolSize)
  const clients = whole(draft.maxClientConn)
  return {
    poolSize:
      size === undefined || size < 1 || size > limits.poolSize
        ? `A whole number from 1 to ${limits.poolSize}.`
        : undefined,
    maxClientConn:
      clients === undefined || clients < 1 || clients > limits.maxClientConn
        ? `A whole number from 1 to ${limits.maxClientConn}.`
        : undefined,
  }
}

/** The settings a valid draft means, or `undefined` while a number is out of range. */
export function poolingSettings(
  draft: PoolingDraft,
  limits: DbPooler["limits"],
): DbPoolerSettings | undefined {
  const problems = poolingProblems(draft, limits)
  if (problems.poolSize || problems.maxClientConn) return undefined
  return {
    poolMode: draft.poolMode,
    poolSize: Number(draft.poolSize.trim()),
    maxClientConn: Number(draft.maxClientConn.trim()),
    authMode: draft.authMode,
  }
}

/** How many of the four settings differ from what is saved. */
export function poolingChanges(saved: DbPoolerSettings, draft: PoolingDraft): number {
  const held = draftOfPooling(saved)
  return (Object.keys(held) as (keyof PoolingDraft)[]).filter(
    (key) => String(draft[key]).trim() !== held[key],
  ).length
}

/** Whether changing from one authentication to the other restarts the pooler, which drops its clients. */
export function restartsPooler(saved: DbPoolerSettings, draft: PoolingDraft): boolean {
  return saved.authMode !== draft.authMode
}

export const POOL_MODES: {
  value: DbPoolerSettings["poolMode"]
  label: string
  hint: string
}[] = [
  {
    value: "transaction",
    label: "Transaction",
    hint: "A client holds a server connection for one transaction. Many clients share few connections; session state such as SET and advisory locks does not carry over.",
  },
  {
    value: "session",
    label: "Session",
    hint: "A client holds a server connection until it disconnects. Everything a session can do works; the pool only saves reconnecting.",
  },
]

export const AUTH_MODES: {
  value: DbPoolerSettings["authMode"]
  label: string
  hint: string
}[] = [
  {
    value: "userlist",
    label: "Userlist",
    hint: "Only the account this connection signs in as. Its password is copied to the pooler and kept up to date; nothing is added to the database.",
  },
  {
    value: "auth_query",
    label: "auth_query",
    hint: "Any account the database has. PgBouncer asks the database for the password hash through a function this adds, owned by a superuser, and an account that can call only it.",
  },
]

/** The container's state as a reading: the tone and the word. */
export function poolerReading(pooler: Pick<DbPooler, "enabled" | "state">): {
  tone: "running" | "stopped" | "warning" | "unknown"
  word: string
} {
  if (!pooler.enabled) return { tone: "unknown", word: "Off" }
  switch (pooler.state) {
    case "running":
      return { tone: "running", word: "Running" }
    case "missing":
      return { tone: "warning", word: "Container missing" }
    case "restarting":
      return { tone: "warning", word: "Restarting" }
    case "exited":
    case "created":
    case "paused":
      return { tone: "stopped", word: "Stopped" }
    default:
      return { tone: "unknown", word: pooler.state ?? "Unknown" }
  }
}

/** Where a pooler can be reached, one line each, for the page and for the Connect popover. */
export function poolerAddresses(
  pooler: Pick<DbPooler, "port" | "linkedHost" | "linkedPort" | "exposure">,
  loopback: string,
): { id: "host" | "container"; label: string; address: string }[] {
  const rows: { id: "host" | "container"; label: string; address: string }[] = []
  if (pooler.port) {
    rows.push({
      id: "host",
      label: pooler.exposure === "public" ? "This server and anywhere" : "This server",
      address: `${loopback}:${pooler.port}`,
    })
  }
  rows.push({
    id: "container",
    label: "Inside Docker",
    address: `${pooler.linkedHost}:${pooler.linkedPort}`,
  })
  return rows
}

/** Microseconds as the unit a reader compares: "0.4 ms", "12 ms", "1.2 s". */
export function micros(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "0 ms"
  const ms = value / 1000
  if (ms >= 1000) return `${(ms / 1000).toFixed(1)} s`
  if (ms >= 10) return `${Math.round(ms)} ms`
  return `${ms.toFixed(1)} ms`
}

/** The totals across the pools, for the headline figures. */
export function poolTotals(pools: readonly DbPoolerPool[]) {
  return pools.reduce(
    (sum, pool) => ({
      clientsActive: sum.clientsActive + pool.clActive,
      clientsWaiting: sum.clientsWaiting + pool.clWaiting,
      serversActive: sum.serversActive + pool.svActive,
      serversIdle: sum.serversIdle + pool.svIdle + pool.svUsed,
      maxWaitSec: Math.max(sum.maxWaitSec, pool.maxWaitSec),
    }),
    { clientsActive: 0, clientsWaiting: 0, serversActive: 0, serversIdle: 0, maxWaitSec: 0 },
  )
}

/** The statistics of every database together: rates add, times average by the work they were spent on. */
export function statTotals(stats: readonly DbPoolerStat[]) {
  const queries = stats.reduce((sum, stat) => sum + stat.avgQueryCount, 0)
  const weigh = (pick: (stat: DbPoolerStat) => number) =>
    queries > 0
      ? stats.reduce((sum, stat) => sum + pick(stat) * stat.avgQueryCount, 0) / queries
      : 0
  return {
    queriesPerSecond: queries,
    transactionsPerSecond: stats.reduce((sum, stat) => sum + stat.avgXactCount, 0),
    queryTime: weigh((stat) => stat.avgQueryTime),
    waitTime: weigh((stat) => stat.avgWaitTime),
    receivedPerSecond: stats.reduce((sum, stat) => sum + stat.avgRecv, 0),
    sentPerSecond: stats.reduce((sum, stat) => sum + stat.avgSent, 0),
    queries: stats.reduce((sum, stat) => sum + stat.totalQueryCount, 0),
  }
}

/** Whether the pooler has served anything yet: a fresh one lists no pool and no statistics. */
export function poolerIsIdle(stats: Pick<DbPoolerStats, "pools" | "stats">): boolean {
  return stats.pools.length === 0 && stats.stats.every((stat) => stat.totalQueryCount === 0)
}
