import { expect, test } from "bun:test"
import {
  NOT_POINTED,
  comparisonSummary,
  copiesOfRun,
  copiesPollMs,
  copyActive,
  copyLine,
  copyStatus,
  dataPointChanges,
  dataPointTag,
  dataPointWords,
  orderCopies,
  remaining,
  restoreInPlaceLink,
  restorablePoints,
  tableDiffers,
  tableDifference,
  utcMoment,
} from "./data-rollback-model"

const pitr = {
  connectionId: 5,
  name: "orders-db",
  engine: "postgres",
  kind: "pitr",
  at: "2026-09-03T12:00:00.250Z",
  archiveId: 3,
  recordedAt: "2026-09-03T12:00:01Z",
}
const dump = {
  connectionId: 6,
  name: "sessions-db",
  engine: "mysql",
  kind: "dump",
  at: "2026-09-03T02:15:00Z",
  backupRunId: 41,
  backupJobId: 2,
  recordedAt: "2026-09-03T12:00:01Z",
}
const none = {
  connectionId: 7,
  name: "cache-db",
  engine: "postgres",
  kind: "none",
  reason: "no backup holds cache-db and it has no point-in-time archive",
  recordedAt: "2026-09-03T12:00:01Z",
}

const copy = {
  id: 3,
  projectId: 7,
  environmentId: 12,
  releaseId: 19,
  releaseNumber: 1,
  connectionId: 5,
  name: "orders-db",
  engine: "postgres",
  database: "app",
  state: "ready",
  source: { kind: "pitr", at: "2026-09-03T12:00:00Z", archiveId: 3 },
  recoveredTo: "2026-09-03T11:59:58Z",
  host: "db-5-copy-3.jd.internal",
  port: 5432,
  expiresAt: "2026-09-03T14:00:00Z",
  createdBy: "operator",
  createdAt: "2026-09-03T12:00:00Z",
}

test("a moment is written to the second, in UTC, whatever the browser's zone", () => {
  expect(utcMoment("2026-09-03T12:00:00.250Z")).toBe("2026-09-03 12:00:00 UTC")
  expect(utcMoment("2026-09-03T14:00:00+02:00", false)).toBe("2026-09-03 12:00 UTC")
  expect(utcMoment(undefined)).toBe("")
})

test("each kind of point reads as what a copy would come from", () => {
  expect(dataPointWords(pitr)).toBe("point in time · 2026-09-03 12:00:00 UTC")
  expect(dataPointWords(dump)).toBe("backup of 2026-09-03 02:15 UTC, run 41")
  expect(dataPointWords(none)).toBe(none.reason)
  expect(dataPointWords({ kind: "none" })).toBe("no backup to restore from")
})

test("a recorded none is said on the row but never restored", () => {
  expect(restorablePoints([pitr, none, dump]).map((point) => point.connectionId)).toEqual([5, 6])
  expect(restorablePoints(undefined)).toEqual([])
  const tag = dataPointTag({ dataPoints: [pitr, none] })
  expect(tag?.restorable).toBe(true)
  expect(tag?.title).toBe(
    "orders-db: point in time · 2026-09-03 12:00:00 UTC\ncache-db: " + none.reason,
  )
  expect(dataPointTag({ dataPoints: [none] })?.restorable).toBe(false)
  expect(dataPointTag({})).toBeUndefined()
})

test("restoring in place goes to the route that owns it, with the point chosen", () => {
  expect(restoreInPlaceLink(5, pitr)).toBe(
    "/databases/5/backups?recover=2026-09-03T12%3A00%3A00.250Z",
  )
  expect(restoreInPlaceLink(6, dump)).toBe("/backups/2?run=41&restore=6")
  // Without the job, the run's page cannot be named.
  expect(restoreInPlaceLink(6, { ...dump, backupJobId: undefined })).toBeUndefined()
  expect(restoreInPlaceLink(7, none)).toBeUndefined()
})

test("a copy's state, life and line", () => {
  const now = Date.parse("2026-09-03T12:01:00Z")
  expect(copyActive("ready")).toBe(true)
  expect(copyActive("waiting")).toBe(true)
  expect(copyActive("removed")).toBe(false)
  expect(copyStatus({ state: "waiting" }).label).toBe("Waiting for the rollback")
  expect(copyStatus({ state: "failed" }).tone).toBe("danger")
  expect(remaining("2026-09-03T14:00:00Z", now)).toBe("1h 59m")
  expect(remaining("2026-09-03T12:13:30Z", now)).toBe("12m")
  expect(remaining("2026-09-03T12:01:20Z", now)).toBe("under a minute")
  expect(remaining("2026-09-03T14:01:00Z", now)).toBe("2h")
  // The moment the copy reached, not the one asked for.
  expect(copyLine(copy, now)).toBe(
    "as of release #1 · point in time · 2026-09-03 11:59:58 UTC · removed in 1h 59m",
  )
  expect(
    copyLine(
      {
        ...copy,
        state: "removed",
        reason: "expired",
        source: { kind: "dump", at: dump.at, backupRunId: 41 },
      },
      now,
    ),
  ).toBe("as of release #1 · backup of 2026-09-03 02:15 UTC, run 41 · expired")
})

test("the copies are read often only while one is moving", () => {
  expect(copiesPollMs([copy])).toBe(30_000)
  expect(copiesPollMs([copy, { ...copy, id: 4, state: "restoring" }])).toBe(5_000)
  expect(copiesPollMs(undefined)).toBe(30_000)
})

test("a run's page links the copies that waited for it, and active copies lead", () => {
  const waiting = {
    ...copy,
    id: 4,
    state: "waiting",
    afterRunId: 90,
    createdAt: "2026-09-03T11:00:00Z",
  }
  const gone = { ...copy, id: 5, state: "removed", createdAt: "2026-09-03T12:30:00Z" }
  expect(copiesOfRun([copy, waiting], 90).map((entry) => entry.id)).toEqual([4])
  expect(orderCopies([gone, waiting, copy]).map((entry) => entry.id)).toEqual([3, 4, 5])
})

test("a table differs by its count, or by being on one side only", () => {
  const same = { schema: "public", name: "items", copyRows: 3, liveRows: 3 }
  const grew = { schema: "public", name: "orders", copyRows: 100, liveRows: 1_250 }
  const gone = { schema: "public", name: "coupons", copyRows: 4, liveRows: null, only: "copy" }
  const uncounted = { schema: "public", name: "events", copyRows: null, liveRows: 9 }
  expect(tableDiffers(same)).toBe(false)
  expect(tableDiffers(grew)).toBe(true)
  expect(tableDiffers(gone)).toBe(true)
  expect(tableDiffers(uncounted)).toBe(false)
  expect(tableDifference(grew)).toBe("+1,150 live")
  expect(tableDifference({ ...grew, liveRows: 90 })).toBe("−10 live")
  expect(tableDifference(gone)).toBe("only in the copy")
  expect(tableDifference(uncounted)).toBe("not counted")
  expect(tableDifference(same)).toBe("same")
  const comparison = {
    copyId: 3,
    database: "app",
    tables: [same, grew, gone],
    truncated: false,
    readAt: "",
  }
  expect(comparisonSummary(comparison)).toBe("2 of 3 tables differ")
  expect(comparisonSummary({ ...comparison, tables: [same] })).toBe("All 1 table match")
  expect(comparisonSummary({ ...comparison, tables: [] })).toBe("No tables to compare")
})

test("the what-changed sheet reads each database's point in both releases", () => {
  const later = { ...pitr, at: "2026-09-04T09:30:00Z" }
  const changes = dataPointChanges([pitr, dump], [later, none])
  expect(changes.map((change) => [change.name, change.change])).toEqual([
    ["orders-db", "changed"],
    ["sessions-db", "removed"],
    ["cache-db", "added"],
  ])
  expect(changes[0].to).toBe("point in time · 2026-09-04 09:30:00 UTC")
  expect(dataPointChanges([dump], [dump])[0].change).toBe("unchanged")
  expect(dataPointChanges(undefined, undefined)).toEqual([])
})

test("the sentence about the way that is not offered says why", () => {
  expect(NOT_POINTED).toContain("not offered")
  expect(NOT_POINTED).toContain("throwaway")
})
