import { expect, test } from "bun:test"
import { healthProblems } from "./dashboard-health"

const store = (patch) => ({
  ok: false,
  checkedAt: "2026-11-10T09:00:15Z",
  path: "/var/lib/just-dashboard",
  freeBytes: 0,
  totalBytes: 414921494528,
  reserveBytes: 32 * 1024 * 1024,
  reserveHeld: false,
  reserveReleased: false,
  ...patch,
})

test("a healthy or unread report says nothing", () => {
  expect(healthProblems(undefined)).toEqual([])
  expect(healthProblems({ ok: true, store: store({ ok: true, reserveHeld: true }) })).toEqual([])
})

test("a full disk is named, with where and how much", () => {
  const [problem] = healthProblems({
    ok: false,
    store: store({ code: "disk_full", detail: "database or disk is full (13)" }),
  })
  expect(problem.key).toBe("disk_full")
  expect(problem.title).toBe("The server's disk is full")
  expect(problem.body).toContain("0 B of 386.4 GB is free")
  expect(problem.body).toContain("/var/lib/just-dashboard")
  expect(problem.body).toContain("may not be saved")
  expect(problem.detail).toBe("database or disk is full (13)")
})

test("a spent reserve is said, since it is why saving still works", () => {
  const [problem] = healthProblems({
    ok: false,
    store: store({ code: "disk_full", freeBytes: 31 * 1024 * 1024, reserveReleased: true }),
  })
  expect(problem.body).toContain("gave up its 32 MB reserve")
  expect(problem.body).not.toContain("may not be saved")
})

test("a store that refuses writes or reads is its own problem", () => {
  const since = new Date(Date.now() - 5 * 60_000).toISOString()
  const [unwritable] = healthProblems({
    ok: false,
    store: store({ code: "store_unwritable", since, detail: "database is locked (5)" }),
  })
  expect(unwritable.title).toBe("The dashboard cannot save changes")
  expect(unwritable.body).toContain("5m ago")
  const [unreadable] = healthProblems({
    ok: false,
    store: store({ code: "store_unreadable", since }),
  })
  expect(unreadable.title).toBe("The dashboard's database cannot be read")
})
