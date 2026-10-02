import type {
  DbActivity,
  DbLockWait,
  DbSessionStatus,
} from "@/components/database/ops/performance-types"

/**
 * What the Sessions and Locks views decide before they draw: what state a
 * session is in and how long it has been in it, which sessions have been at
 * it too long, who is holding whom up, and the tree that makes of a list of
 * waits.
 *
 * Three of the old page's faults lived in decisions like these. It read
 * `seconds` as "running for" whatever the state, so an idle pooled connection
 * was the longest-running statement on the server; it found blockers by
 * splitting the joined `blockedBy` on commas, which an Oracle session id
 * contains, so a blocker was never matched; and it had no answer for a session
 * that holds a transaction open and runs nothing. Each is a rule here, with
 * its test beside it.
 */

/** The states in the order the page lists them: who is working, who is stuck, who is only connected. */
export const SESSION_STATES: readonly { id: DbSessionStatus; label: string; chip: string }[] = [
  { id: "active", label: "Active", chip: "Active" },
  { id: "blocked", label: "Blocked", chip: "Blocked" },
  { id: "idle_in_transaction", label: "Idle in transaction", chip: "Idle in transaction" },
  { id: "idle", label: "Idle", chip: "Idle" },
  { id: "background", label: "Background", chip: "Background" },
]

/**
 * How long is too long, in seconds: a statement still running after thirty, a
 * transaction left open and idle for ten, a session that has waited ten for a
 * lock; and how many sessions one has to hold up before it is called out as
 * the one to look at first.
 */
export const THRESHOLDS = { active: 30, idleInTransaction: 10, blocked: 10, blocking: 3 } as const

/**
 * The state a session is in, in the shared words. The server always sets
 * `status`; one that predates it is read from the engine's own word, where
 * "idle" is said plainly and anything else is work.
 */
export function statusOf(session: DbActivity): DbSessionStatus {
  if (session.status) return session.status
  const word = (session.state ?? "").toLowerCase()
  if (word.includes("idle in transaction")) return "idle_in_transaction"
  if (word === "idle" || word === "sleep" || word === "sleeping" || word === "inactive") {
    return "idle"
  }
  return "active"
}

/**
 * How long a session has been in the state it is in — the one figure its row
 * prints. A statement's running time is only a working session's; an idle
 * one has been idle that long, and one idle inside a transaction is measured
 * by how long the transaction has been open, which is what it is costing.
 * `undefined` where the engine does not say.
 */
export function stateSeconds(session: DbActivity): number | undefined {
  switch (statusOf(session)) {
    case "active":
    case "blocked":
      return session.seconds
    case "idle_in_transaction":
      return session.transactionSeconds ?? session.idleSeconds
    case "idle":
      return session.idleSeconds
    case "background":
      return undefined
  }
}

export type SessionConcern = "long-running" | "open-transaction" | "long-blocked"

/** What about a session has gone on too long, if anything. */
export function concernOf(session: DbActivity): SessionConcern | undefined {
  const seconds = stateSeconds(session) ?? 0
  switch (statusOf(session)) {
    case "active":
      return seconds >= THRESHOLDS.active ? "long-running" : undefined
    case "idle_in_transaction":
      return seconds >= THRESHOLDS.idleInTransaction ? "open-transaction" : undefined
    case "blocked":
      return seconds >= THRESHOLDS.blocked ? "long-blocked" : undefined
    default:
      return undefined
  }
}

/** The pids a session waits on. The joined `blockedBy` is never split: Oracle's pid holds a comma. */
export function blockersOf(session: DbActivity): string[] {
  return session.blockedByPids ?? []
}

/**
 * Who each session is holding up, directly: a blocker's pid to the pids
 * waiting on it. Compared whole, as the server lists them.
 */
export function blockedBy(sessions: readonly DbActivity[]): Map<string, string[]> {
  const held = new Map<string, string[]>()
  for (const session of sessions) {
    for (const pid of blockersOf(session)) {
      held.set(pid, [...(held.get(pid) ?? []), session.pid])
    }
  }
  return held
}

/**
 * How many sessions stand behind each one, however long the queue: the
 * sessions waiting on it, the ones waiting on those, and so on, each counted
 * once. It is the figure the Locks view prints for the same session.
 */
export function heldUp(sessions: readonly DbActivity[]): Map<string, number> {
  const direct = blockedBy(sessions)
  const counts = new Map<string, number>()
  for (const pid of direct.keys()) {
    const seen = new Set<string>()
    const walk = (blocker: string) => {
      for (const waiter of direct.get(blocker) ?? []) {
        if (waiter === pid || seen.has(waiter)) continue
        seen.add(waiter)
        walk(waiter)
      }
    }
    walk(pid)
    counts.set(pid, seen.size)
  }
  return counts
}

/** How many sessions are in each state. */
export function countByState(sessions: readonly DbActivity[]): Record<DbSessionStatus, number> {
  const counts: Record<DbSessionStatus, number> = {
    active: 0,
    blocked: 0,
    idle_in_transaction: 0,
    idle: 0,
    background: 0,
  }
  for (const session of sessions) counts[statusOf(session)] += 1
  return counts
}

/**
 * The statement that has run the longest, among the sessions that are running
 * one. An idle session is never it, whatever its clock says, and neither is
 * the dashboard's own read of this list.
 */
export function longestRunning(sessions: readonly DbActivity[]): DbActivity | undefined {
  let longest: DbActivity | undefined
  for (const session of sessions) {
    const status = statusOf(session)
    if (session.self || (status !== "active" && status !== "blocked")) continue
    if (!longest || session.seconds > longest.seconds) longest = session
  }
  return longest
}

const STATE_RANK: Record<DbSessionStatus, number> = {
  blocked: 0,
  active: 1,
  idle_in_transaction: 2,
  background: 3,
  idle: 4,
}

/**
 * The order the list is read in: a session holding others up first, then the
 * stuck, the working, the ones idle in a transaction, and inside each the one
 * that has been at it longest. The dashboard's own read goes last among its
 * peers — it is always there and never the answer.
 */
export function orderSessions(sessions: readonly DbActivity[]): DbActivity[] {
  const held = blockedBy(sessions)
  const rank = (session: DbActivity) => (held.has(session.pid) ? -1 : STATE_RANK[statusOf(session)])
  return [...sessions].sort(
    (a, b) =>
      rank(a) - rank(b) ||
      Number(Boolean(a.self)) - Number(Boolean(b.self)) ||
      (stateSeconds(b) ?? 0) - (stateSeconds(a) ?? 0) ||
      a.pid.localeCompare(b.pid, undefined, { numeric: true }),
  )
}

/** Whether a session answers a typed filter: by who, from where, with what, doing what. */
export function matchesSession(session: DbActivity, filter: string): boolean {
  const needle = filter.trim().toLowerCase()
  if (!needle) return true
  return [
    session.pid,
    session.user,
    session.application,
    session.database,
    session.client,
    session.query,
    session.wait,
  ].some((value) => value?.toLowerCase().includes(needle))
}

/**
 * Which ways of stopping a session apply to it. Cancelling stops a statement
 * and keeps the session, so it needs one running; a session idle inside a
 * transaction has nothing to cancel, and only ending it frees its locks. The
 * dashboard's own session is never offered either.
 */
export function stopsFor(
  session: DbActivity,
  engine: { cancel: boolean; kill: boolean },
): { cancel: boolean; terminate: boolean } {
  if (session.self) return { cancel: false, terminate: false }
  const status = statusOf(session)
  return {
    cancel: engine.cancel && (status === "active" || status === "blocked"),
    terminate: engine.kill && status !== "background",
  }
}

/** One session in the picture of who waits on whom. */
export type LockNode = {
  pid: string
  user?: string
  query?: string
  /** A blocker's state, in the engine's words: "idle in transaction". */
  state?: string
  /**
   * The wait that put it under the node above: what it asked for, on what.
   * A root — a session that waits on nobody — has none.
   */
  wait?: DbLockWait
  /** A root: how long its transaction has been open. A waiter: how long it has waited. */
  seconds: number | undefined
  /** The sessions waiting on this one. */
  waiters: LockNode[]
  /**
   * It is already drawn above itself: the sessions wait on each other in a
   * ring, which the engine breaks by ending one. Not followed further.
   */
  cycle?: boolean
}

/**
 * The waits that say something the others do not. A session queued behind a
 * second one, which is itself waiting on a third, is reported as waiting on
 * both; drawn as given, it would stand in the tree twice — once in the queue
 * and once beside it. A wait is left out when the same session is already
 * behind its blocker through another: the queue says it.
 */
export function directWaits(waits: readonly DbLockWait[]): DbLockWait[] {
  const blockers = new Map<string, Set<string>>()
  for (const wait of waits) {
    if (!blockers.has(wait.waitingPid)) blockers.set(wait.waitingPid, new Set())
    blockers.get(wait.waitingPid)?.add(wait.blockingPid)
  }
  const reaches = (from: string, to: string, seen: Set<string>): boolean => {
    if (from === to) return true
    if (seen.has(from)) return false
    seen.add(from)
    for (const next of blockers.get(from) ?? []) {
      if (reaches(next, to, seen)) return true
    }
    return false
  }
  return waits.filter((wait) => {
    for (const other of blockers.get(wait.waitingPid) ?? []) {
      if (other === wait.blockingPid) continue
      // The waiter itself is out of the walk: a ring through it proves nothing.
      if (reaches(other, wait.blockingPid, new Set([wait.waitingPid]))) return false
    }
    return true
  })
}

/**
 * The list of waits as the tree it describes: at the top the sessions that
 * hold others up and wait on nobody themselves, under each the sessions
 * waiting on it, and under those the ones waiting on them.
 *
 * A session waiting on two unrelated others is drawn under both — both have
 * to let go — while one that is only further back in a queue is drawn once,
 * at its place in it (`directWaits`). The top is ordered by how many sessions
 * stand behind each, since that is the one to deal with first.
 */
export function lockTree(all: readonly DbLockWait[]): LockNode[] {
  const waits = directWaits(all)
  const byBlocker = new Map<string, DbLockWait[]>()
  const waiting = new Set<string>()
  for (const wait of waits) {
    byBlocker.set(wait.blockingPid, [...(byBlocker.get(wait.blockingPid) ?? []), wait])
    waiting.add(wait.waitingPid)
  }

  const under = (pid: string, path: readonly string[]): LockNode[] =>
    (byBlocker.get(pid) ?? []).map((wait) => {
      const cycle = path.includes(wait.waitingPid)
      return {
        pid: wait.waitingPid,
        user: wait.waitingUser,
        query: wait.waitingQuery,
        wait,
        seconds: wait.waitSeconds,
        waiters: cycle ? [] : under(wait.waitingPid, [...path, wait.waitingPid]),
        ...(cycle ? { cycle: true } : {}),
      }
    })

  const root = (pid: string): LockNode => {
    // Every wait that names it, the ones the tree leaves out too: they all
    // carry what the server says about it.
    const named = all.filter((wait) => wait.blockingPid === pid)
    const first = named[0]
    return {
      pid,
      user: first?.blockingUser,
      query: first?.blockingQuery,
      state: first?.blockingState,
      seconds: named.reduce<number | undefined>(
        (most, wait) =>
          wait.blockingSeconds === undefined ? most : Math.max(most ?? 0, wait.blockingSeconds),
        undefined,
      ),
      waiters: under(pid, [pid]),
    }
  }

  let tops = [...byBlocker.keys()].filter((pid) => !waiting.has(pid))
  // Every blocker is itself waiting: a ring. It is entered at the session
  // that has been waited on the longest.
  if (tops.length === 0 && waits.length > 0) {
    const longest = [...waits].sort((a, b) => b.waitSeconds - a.waitSeconds)[0]
    tops = [longest.blockingPid]
  }
  return tops.map(root).sort((a, b) => behind(b) - behind(a) || (b.seconds ?? 0) - (a.seconds ?? 0))
}

/** How many sessions stand behind a node, however deep. */
export function behind(node: LockNode): number {
  const seen = new Set<string>()
  const walk = (current: LockNode) => {
    for (const waiter of current.waiters) {
      seen.add(waiter.pid)
      walk(waiter)
    }
  }
  walk(node)
  return seen.size
}

/** How many sessions are waiting in all, each counted once. */
export function waitingSessions(waits: readonly DbLockWait[]): number {
  return new Set(waits.map((wait) => wait.waitingPid)).size
}
