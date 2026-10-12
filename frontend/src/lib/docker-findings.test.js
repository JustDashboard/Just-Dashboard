import { expect, test } from "bun:test"
import { needsDockerAttention, priorityDockerFindings } from "./docker-findings"

test("priority Docker findings include failures, exclude housekeeping and honor the older API shape", () => {
  for (const severity of ["critical", "warning"])
    expect(needsDockerAttention({ severity, level: severity })).toBe(true)
  for (const severity of ["recommendation", "info"])
    expect(needsDockerAttention({ severity, level: "notice" })).toBe(false)
  expect(needsDockerAttention({ level: "warning" })).toBe(true)
  expect(needsDockerAttention({ level: "notice" })).toBe(false)
  expect(
    priorityDockerFindings([
      { id: "legacy", level: "critical" },
      { id: "tip", severity: "recommendation" },
    ]),
  ).toEqual([{ id: "legacy", level: "critical", severity: "critical" }])
})
