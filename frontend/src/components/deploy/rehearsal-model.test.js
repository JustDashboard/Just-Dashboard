import { expect, test } from "bun:test"
import {
  DEFAULT_REHEARSAL_POLICY,
  heldLocks,
  lockMode,
  longestLock,
  millis,
  rehearsalCandidates,
  rehearsalOf,
  rehearsalPolicyLine,
  rehearsalSummary,
  sourceShort,
  sourceWords,
  tasksLine,
} from "./rehearsal-model"

const report = {
  rehearsed: true,
  database: { connectionId: 5, name: "orders-db", engine: "postgres", database: "app", image: "" },
  source: { kind: "dump", backupRunId: 9, takenAt: "2026-10-27T02:15:00Z", bytes: 4096 },
  restoreMs: 4423,
  tasks: [{ name: "migrate", durationMs: 3401, exitCode: 0 }],
  locks: {
    read: true,
    samples: 14,
    maxWaiting: 0,
    waitedMs: 0,
    held: [
      { relation: "items_pkey", mode: "ShareLock", heldMs: 250 },
      { relation: "items", mode: "AccessExclusiveLock", heldMs: 2003 },
    ],
  },
  cleanedUp: true,
  totalMs: 7900,
}

test("times read in the unit they are measured in", () => {
  expect(millis(850)).toBe("850 ms")
  expect(millis(2003)).toBe("2.0 s")
  expect(millis(72_000)).toBe("1m 12s")
  expect(millis(undefined)).toBe("—")
})

test("a report is told apart from a skipped step's reason", () => {
  expect(rehearsalOf(report)).toBe(report)
  expect(rehearsalOf({ reason: "migration rehearsal is turned off for this environment" })).toBe(
    undefined,
  )
  expect(rehearsalOf(undefined)).toBe(undefined)
})

test("locks are named the way PostgreSQL's documentation names them, longest first", () => {
  expect(lockMode("AccessExclusiveLock")).toBe("ACCESS EXCLUSIVE")
  expect(lockMode("ShareRowExclusiveLock")).toBe("SHARE ROW EXCLUSIVE")
  expect(longestLock(report).relation).toBe("items")
  expect(longestLock({ ...report, locks: { ...report.locks, held: [] } })).toBe(undefined)
})

test("the source says which backup the copy came from", () => {
  const at = (iso) => iso.slice(11, 16)
  expect(sourceWords(report.source, at)).toBe("the backup of 02:15")
  expect(sourceWords({ kind: "pitr", takenAt: "2026-10-27T02:20:00Z" }, at)).toBe(
    "the point-in-time archive, to 02:20",
  )
  expect(sourceWords(undefined, at)).toBe("a backup")
  expect(sourceShort(report.source, at)).toBe("dump of 02:15")
  expect(sourceShort({ kind: "pitr", takenAt: "2026-10-27T02:20:00Z" }, at)).toBe(
    "archive to 02:20",
  )
})

test("a weaker lock the stronger one covered is not read twice", () => {
  const locks = (held) => ({ ...report, locks: { ...report.locks, held } })
  // ALTER TABLE then CREATE INDEX in one transaction: the ALTER's lock is the one.
  const both = locks([
    { relation: "orders", mode: "ShareLock", heldMs: 481 },
    { relation: "orders", mode: "AccessExclusiveLock", heldMs: 481 },
  ])
  expect(heldLocks(both)).toEqual([
    { relation: "orders", mode: "AccessExclusiveLock", heldMs: 481 },
  ])
  expect(longestLock(both).mode).toBe("AccessExclusiveLock")
  // A CREATE INDEX that outlasted a short ALTER in another transaction is its own reading.
  const apart = locks([
    { relation: "orders", mode: "AccessExclusiveLock", heldMs: 120 },
    { relation: "orders", mode: "ShareLock", heldMs: 9000 },
    { relation: "items", mode: "ShareLock", heldMs: 300 },
  ])
  expect(heldLocks(apart).map((lock) => `${lock.relation}:${lock.mode}`)).toEqual([
    "orders:ShareLock",
    "items:ShareLock",
    "orders:AccessExclusiveLock",
  ])
})

test("the summary names the copy, the tasks and the longest lock", () => {
  expect(rehearsalSummary(report)).toBe("orders-db · migrate passed · items locked 2.0 s")
  const failed = {
    ...report,
    tasks: [
      { name: "migrate", durationMs: 1200, exitCode: 1, error: "ERROR: contains null values" },
    ],
    locks: { ...report.locks, held: [] },
  }
  expect(tasksLine(failed)).toBe("migrate failed (exit 1)")
  expect(rehearsalSummary(failed)).toBe("orders-db · migrate failed (exit 1)")
  expect(
    rehearsalSummary({
      ...report,
      rehearsed: false,
      reason: "no backup holds orders-db",
      tasks: [],
    }),
  ).toBe("Not rehearsed: no backup holds orders-db")
  expect(
    tasksLine({ ...report, tasks: [report.tasks[0], { ...report.tasks[0], name: "seed" }] }),
  ).toBe("2 tasks passed")
})

test("the settings line says what a release does now", () => {
  expect(rehearsalPolicyLine(DEFAULT_REHEARSAL_POLICY)).toBe(
    "Off · release tasks run against production directly",
  )
  expect(rehearsalPolicyLine({ ...DEFAULT_REHEARSAL_POLICY, enabled: true })).toBe(
    "On · a release that cannot be rehearsed is stopped",
  )
  expect(
    rehearsalPolicyLine({ ...DEFAULT_REHEARSAL_POLICY, enabled: true, whenUnavailable: "warn" }),
  ).toBe("On · a release that cannot be rehearsed continues with a warning")
})

test("the candidates are the databases a variable reaching release tasks references", () => {
  const variable = (name, target, scopes) => ({
    name,
    scopes,
    reference: target ? { kind: "database", target } : undefined,
  })
  expect(
    rehearsalCandidates([
      variable("DATABASE_URL", "5.url", ["runtime", "release_task"]),
      variable("ANALYTICS_URL", "9.url.events", ["release_task"]),
      variable("CACHE_URL", "7.url", ["runtime"]),
      variable("POOLED", "5.pooled", ["release_task"]),
      variable("PLAIN", undefined, ["release_task"]),
    ]),
  ).toEqual([5, 9])
})
