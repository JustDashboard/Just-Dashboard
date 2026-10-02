import { describe, expect, test } from "bun:test"
import {
  THRESHOLDS,
  behind,
  blockedBy,
  concernOf,
  countByState,
  directWaits,
  heldUp,
  lockTree,
  longestRunning,
  matchesSession,
  orderSessions,
  stateSeconds,
  statusOf,
  stopsFor,
  waitingSessions,
} from "./performance-activity"

const session = (pid, status, extra = {}) => ({ pid, status, seconds: 0, ...extra })

describe("how long a session has been in its state", () => {
  test("an idle session is measured by its idleness, never as a running statement", () => {
    const pooled = session("7", "idle", { seconds: 0, idleSeconds: 86_400 })
    expect(stateSeconds(pooled)).toBe(86_400)
    expect(concernOf(pooled)).toBeUndefined()
  })

  test("a session idle inside a transaction is measured by the transaction", () => {
    const held = session("8", "idle_in_transaction", { idleSeconds: 2, transactionSeconds: 95 })
    expect(stateSeconds(held)).toBe(95)
    expect(concernOf(held)).toBe("open-transaction")
  })

  test("each state has its own line for too long", () => {
    expect(concernOf(session("1", "active", { seconds: THRESHOLDS.active - 1 }))).toBeUndefined()
    expect(concernOf(session("1", "active", { seconds: THRESHOLDS.active }))).toBe("long-running")
    expect(concernOf(session("2", "blocked", { seconds: THRESHOLDS.blocked }))).toBe("long-blocked")
    expect(concernOf(session("3", "background", { seconds: 9_999 }))).toBeUndefined()
  })

  test("a server that sends no shared word is read by its own", () => {
    expect(statusOf({ pid: "1", seconds: 0, state: "idle in transaction (aborted)" })).toBe(
      "idle_in_transaction",
    )
    expect(statusOf({ pid: "1", seconds: 0, state: "Sleep" })).toBe("idle")
    expect(statusOf({ pid: "1", seconds: 0, state: "Query" })).toBe("active")
  })
})

describe("the longest-running statement", () => {
  test("is never an idle session, whatever its clock says", () => {
    const sessions = [
      session("1", "idle", { seconds: 0, idleSeconds: 40_000 }),
      session("2", "active", { seconds: 12 }),
      session("3", "blocked", { seconds: 48 }),
    ]
    expect(longestRunning(sessions)?.pid).toBe("3")
  })

  test("is not the dashboard's own read of the list", () => {
    const sessions = [session("1", "active", { seconds: 90, self: true })]
    expect(longestRunning(sessions)).toBeUndefined()
  })
})

describe("who holds whom up", () => {
  test("a pid with a comma in it is matched whole", () => {
    // Oracle's pid is "sid,serial#"; the joined form of two of them cannot be split.
    const sessions = [
      session("12,3301", "idle_in_transaction"),
      session("40,77", "blocked", { blockedBy: "12,3301", blockedByPids: ["12,3301"] }),
    ]
    expect(blockedBy(sessions).get("12,3301")).toEqual(["40,77"])
    expect(blockedBy(sessions).has("12")).toBe(false)
  })

  test("a session with only the joined form names nobody", () => {
    const sessions = [session("5", "blocked", { blockedBy: "1,2" })]
    expect(blockedBy(sessions).size).toBe(0)
  })

  test("the count behind a session is the whole queue, each session once", () => {
    const sessions = [
      session("a", "idle_in_transaction"),
      session("b", "blocked", { blockedByPids: ["a"] }),
      session("c", "blocked", { blockedByPids: ["a", "b"] }),
      session("d", "blocked", { blockedByPids: ["c"] }),
    ]
    const queued = heldUp(sessions)
    expect(queued.get("a")).toBe(3)
    expect(queued.get("b")).toBe(2)
    expect(queued.get("c")).toBe(1)
    expect(queued.has("d")).toBe(false)
  })

  test("two sessions waiting on each other do not count themselves", () => {
    const ring = [
      session("x", "blocked", { blockedByPids: ["y"] }),
      session("y", "blocked", { blockedByPids: ["x"] }),
    ]
    expect(heldUp(ring).get("x")).toBe(1)
  })
})

describe("the list's order and filters", () => {
  const sessions = [
    session("1", "idle", { idleSeconds: 500 }),
    session("2", "active", { seconds: 3, user: "app", query: "SELECT 1" }),
    session("3", "blocked", { seconds: 20, blockedByPids: ["4"], application: "billing" }),
    session("4", "idle_in_transaction", { transactionSeconds: 60 }),
    session("5", "active", { seconds: 40, self: true }),
  ]

  test("the session holding others up leads, then the stuck, the working, the idle", () => {
    expect(orderSessions(sessions).map((s) => s.pid)).toEqual(["4", "3", "2", "5", "1"])
  })

  test("counts are by state", () => {
    expect(countByState(sessions)).toEqual({
      active: 2,
      blocked: 1,
      idle_in_transaction: 1,
      idle: 1,
      background: 0,
    })
  })

  test("a filter is matched against who, from what, and the statement", () => {
    expect(sessions.filter((s) => matchesSession(s, "BILL")).map((s) => s.pid)).toEqual(["3"])
    expect(sessions.filter((s) => matchesSession(s, "select")).map((s) => s.pid)).toEqual(["2"])
    expect(sessions.filter((s) => matchesSession(s, "  ")).length).toBe(5)
  })
})

describe("the ways of stopping a session", () => {
  const both = { cancel: true, kill: true }

  test("a session idle in a transaction has nothing to cancel", () => {
    expect(stopsFor(session("1", "idle_in_transaction"), both)).toEqual({
      cancel: false,
      terminate: true,
    })
  })

  test("a working session can be cancelled or terminated", () => {
    expect(stopsFor(session("1", "active"), both)).toEqual({ cancel: true, terminate: true })
    expect(stopsFor(session("1", "blocked"), both)).toEqual({ cancel: true, terminate: true })
  })

  test("the dashboard's own session and the server's own threads are never offered", () => {
    expect(stopsFor(session("1", "active", { self: true }), both)).toEqual({
      cancel: false,
      terminate: false,
    })
    expect(stopsFor(session("1", "background"), both).terminate).toBe(false)
  })

  test("an engine that cannot cancel offers only the end of the session", () => {
    expect(stopsFor(session("1", "active"), { cancel: false, kill: true })).toEqual({
      cancel: false,
      terminate: true,
    })
  })
})

describe("the tree of waits", () => {
  const wait = (waitingPid, blockingPid, extra = {}) => ({
    waitingPid,
    blockingPid,
    waitSeconds: 1,
    ...extra,
  })

  test("a queue is drawn once: a wait the queue already says is left out", () => {
    // d waits on b and on a; b waits on a. The second is said by the first.
    const waits = [wait("b", "a"), wait("d", "b"), wait("d", "a"), wait("e", "d")]
    expect(directWaits(waits).map((w) => `${w.waitingPid}>${w.blockingPid}`)).toEqual([
      "b>a",
      "d>b",
      "e>d",
    ])
    const tree = lockTree(waits)
    expect(tree.map((node) => node.pid)).toEqual(["a"])
    expect(tree[0].waiters.map((node) => node.pid)).toEqual(["b"])
    expect(tree[0].waiters[0].waiters[0].waiters.map((node) => node.pid)).toEqual(["e"])
    expect(behind(tree[0])).toBe(3)
    expect(waitingSessions(waits)).toBe(3)
  })

  test("a session waiting on two unrelated sessions stands under both", () => {
    const tree = lockTree([wait("c", "a"), wait("c", "b")])
    expect(tree.map((node) => node.pid).sort()).toEqual(["a", "b"])
    expect(tree.every((node) => node.waiters[0].pid === "c")).toBe(true)
  })

  test("the top is the session with the most behind it", () => {
    const tree = lockTree([wait("x", "small"), wait("p", "big"), wait("q", "big"), wait("r", "q")])
    expect(tree.map((node) => node.pid)).toEqual(["big", "small"])
  })

  test("a root carries what the server says of the blocker", () => {
    const tree = lockTree([
      wait("b", "a", {
        blockingUser: "app",
        blockingState: "idle in transaction",
        blockingQuery: "UPDATE t SET x = 1",
        blockingSeconds: 300,
        waitSeconds: 12,
        object: "public.t",
        mode: "RowExclusiveLock",
      }),
    ])
    expect(tree[0]).toMatchObject({
      pid: "a",
      user: "app",
      state: "idle in transaction",
      seconds: 300,
    })
    expect(tree[0].waiters[0]).toMatchObject({ pid: "b", seconds: 12 })
    expect(tree[0].waiters[0].wait.object).toBe("public.t")
  })

  test("a ring is entered once and not followed round", () => {
    const tree = lockTree([wait("a", "b", { waitSeconds: 9 }), wait("b", "a", { waitSeconds: 4 })])
    expect(tree.length).toBe(1)
    expect(tree[0].pid).toBe("b")
    const again = tree[0].waiters[0].waiters[0]
    expect(again.cycle).toBe(true)
    expect(again.waiters).toEqual([])
  })

  test("no waits is no tree", () => {
    expect(lockTree([])).toEqual([])
  })
})
