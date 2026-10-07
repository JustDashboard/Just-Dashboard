import { expect, test } from "bun:test"
import { orderedRows } from "./inspection-order"
import { cronRowKeys } from "./row-identity"

test("an inspected ranking updates readings, removes exits and appends new identities", () => {
  const latest = [
    { id: "new", cpu: 90 },
    { id: "second", cpu: 60 },
    { id: "first", cpu: 10 },
  ]
  const result = orderedRows(latest, ["first", "exited", "second"], (row) => row.id)
  expect(result.map((row) => row.id)).toEqual(["first", "second", "new"])
  expect(result[0]).toBe(latest[2])
  expect(latest.map((row) => row.id)).toEqual(["new", "second", "first"])
})

test("a reused PID is a new arrival, never the inspected process", () => {
  const rows = [{ id: "42-new" }, { id: "7-original" }]
  expect(orderedRows(rows, ["42-original", "7-original"], (row) => row.id)).toEqual([
    rows[1],
    rows[0],
  ])
})

test("cron identities survive toggles and line shifts while duplicate commands stay distinct", () => {
  const jobs = [
    {
      schedule: "0 3 * * *",
      command: "/backup",
      line: 2,
      raw: "0 3 * * * /backup",
      disabled: false,
    },
    {
      schedule: "0 3 * * *",
      command: "/backup",
      line: 3,
      raw: "0 3 * * * /backup",
      disabled: false,
    },
  ]
  const before = cronRowKeys(jobs)
  const after = cronRowKeys(
    jobs.map((job) => ({ ...job, line: job.line + 1, raw: `# ${job.raw}`, disabled: true })),
  )
  expect(after).toEqual(before)
  expect(new Set(before).size).toBe(2)
})
