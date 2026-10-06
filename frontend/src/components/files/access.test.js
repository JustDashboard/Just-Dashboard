import { expect, test } from "bun:test"
import { accessRows, accessSummary } from "./access"

test("a mode reads as three rows of what each may do", () => {
  const rows = accessRows("0754", "ubuntu", "www")
  expect(rows.map((r) => [r.who, r.access])).toEqual([
    ["Owner", { read: true, write: true, run: true }],
    ["Group", { read: true, write: false, run: true }],
    ["Everyone", { read: true, write: false, run: false }],
  ])
  // A special leading digit is not part of the three rows.
  expect(accessRows("4755")[0].access.run).toBe(true)
})

test("the summary says who can change it and who can only look", () => {
  const s = (mode, dir = false) => accessSummary(accessRows(mode, "ubuntu", "ubuntu"), dir)
  expect(s("0644")).toBe("ubuntu can edit · everyone can read")
  expect(s("0664")).toBe("ubuntu and group ubuntu can edit · everyone can read")
  expect(s("0600")).toBe("ubuntu can edit · only ubuntu can read")
  expect(s("0750", true)).toBe("ubuntu can change · group ubuntu can open")
  expect(s("0777", true)).toBe("Anyone can change this")
  expect(s("0444")).toBe("No one can edit this · everyone can read")
})
