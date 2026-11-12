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

test("a crashing task is a warning that names the worst and counts the rest", () => {
  const lastPanicAt = new Date(Date.now() - 2 * 60_000).toISOString()
  const healthy = store({ ok: true, reserveHeld: true })
  const [one] = healthProblems({
    ok: false,
    store: healthy,
    tasks: [
      {
        name: "nginx status sampler",
        running: true,
        panics: 4,
        lastPanic: "index out of range [3] with length 2",
        lastPanicAt,
      },
    ],
  })
  expect(one.tone).toBe("warning")
  expect(one.title).toBe("A background task of the dashboard is crashing")
  expect(one.body).toContain("The nginx status sampler failed 4 times, last 2m ago.")
  expect(one.detail).toBe("index out of range [3] with length 2")

  const [several] = healthProblems({
    ok: false,
    store: healthy,
    tasks: [
      { name: "blocklist refresh", running: true, panics: 1, lastPanicAt },
      { name: "host alerts", running: false, panics: 9, lastPanic: "nil map", lastPanicAt },
    ],
  })
  expect(several.title).toBe("2 background tasks of the dashboard are crashing")
  expect(several.body).toContain("The host alerts failed 9 times")
  expect(several.body).toContain("and 1 more with it")
})

test("a full disk comes before a crashing task", () => {
  const problems = healthProblems({
    ok: false,
    store: store({ code: "disk_full" }),
    tasks: [{ name: "metrics recorder", running: true, panics: 2, lastPanicAt: undefined }],
  })
  expect(problems.map((problem) => problem.key)).toEqual(["disk_full", "tasks"])
  expect(problems[0].tone).toBe("danger")
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
