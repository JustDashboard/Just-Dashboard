import { expect, test } from "bun:test"
import { backupOrder, failedBackups, recentDrawings, shellOrder, unitCounts } from "./order"

const at = (minutesAgo) =>
  new Date(Date.parse("2026-10-19T12:00:00Z") - minutesAgo * 60_000).toISOString()

test("drawings are newest first, capped", () => {
  const drawings = recentDrawings(
    [
      { id: 1, name: "Network map", updatedAt: at(30) },
      { id: 2, name: "Old sketch", updatedAt: at(9_000) },
      { id: 7, name: "Launch plan", updatedAt: at(5) },
      { id: 8, name: "Retro", updatedAt: at(600) },
    ],
    3,
  )
  expect(drawings.map((drawing) => drawing.id)).toEqual([7, 1, 8])
})

test("the list may be missing while it is hidden or unread", () => {
  expect(recentDrawings(undefined, 4)).toEqual([])
})

test("a working shell leads, then a busy one, then idle, then ended", () => {
  const session = (id, patch) => ({
    id,
    title: id,
    favourite: false,
    live: true,
    windows: 1,
    attached: 0,
    createdAt: at(60),
    ...patch,
  })
  const order = shellOrder([
    session("ended", { live: false, working: true }),
    session("idle-old", { createdAt: at(600) }),
    session("idle-new", { createdAt: at(1) }),
    session("busy", { busy: true }),
    session("working", { working: true, busy: true }),
    session("favourite", { favourite: true, createdAt: at(900) }),
  ])
  expect(order.map((s) => s.id)).toEqual([
    "working",
    "busy",
    "favourite",
    "idle-new",
    "idle-old",
    "ended",
  ])
})

test("a failed backup job leads even when a newer run elsewhere succeeded", () => {
  const job = (id, patch) => ({
    id,
    name: `job-${id}`,
    schedule: "0 3 * * *",
    enabled: true,
    nextRun: at(-60 * id),
    ...patch,
  })
  const jobs = [
    job(1, { lastRun: { status: "success", startedAt: at(5) } }),
    job(2, { lastRun: { status: "failed", startedAt: at(600) } }),
    job(3, { enabled: false }),
    job(4, { schedule: "" }),
    job(5, { lastRun: { status: "running", startedAt: at(1) } }),
  ]
  expect(backupOrder(jobs).map((j) => j.id)).toEqual([2, 5, 1, 4, 3])
  expect(failedBackups(jobs)).toBe(1)
})

test("units count running services and every failed unit", () => {
  const unit = (name, activeState) => ({ name, activeState })
  expect(
    unitCounts([
      unit("nginx.service", "active"),
      unit("docker.service", "active"),
      unit("docker.socket", "active"),
      unit("backup.service", "failed"),
      unit("fstrim.timer", "failed"),
      unit("cron.service", "inactive"),
    ]),
  ).toEqual({ running: 2, failed: 2 })
})
