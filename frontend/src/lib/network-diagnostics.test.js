import { describe, expect, test } from "bun:test"
import {
  diagnosticCompatible,
  diagnosticFinished,
  diagnosticNameProblem,
  diagnosticReading,
  diagnosticRetentionProblem,
} from "./network-diagnostics"

const run = {
  id: "one",
  request: { tool: "dns", target: "example.test", record: "AAAA" },
  status: "completed",
  hasResult: true,
}

describe("retained diagnostic readings", () => {
  test("compares only distinct terminal runs with the exact normalized request and evidence", () => {
    expect(diagnosticCompatible(run, { ...run, id: "two" })).toBe(true)
    expect(
      diagnosticCompatible(run, {
        ...run,
        id: "two",
        request: { ...run.request, port: 0, option: "" },
      }),
    ).toBe(true)
    for (const other of [
      run,
      { ...run, id: "two", status: "running" },
      { ...run, id: "two", hasResult: false },
      { ...run, id: "two", request: { ...run.request, record: "A" } },
      { ...run, id: "two", request: { ...run.request, target: "other.test" } },
    ])
      expect(diagnosticCompatible(run, other)).toBe(false)
    expect(diagnosticCompatible(run, { ...run, id: "two", status: "failed" })).toBe(true)
    expect(diagnosticFinished({ status: "cancelling" })).toBe(false)
    expect(diagnosticFinished({ status: "interrupted" })).toBe(true)
  })
  test("names and retention respect server byte, control and numeric bounds", () => {
    expect(diagnosticNameProblem(" resolver baseline ")).toBeUndefined()
    for (const name of [" ", "bad\nname", "界".repeat(34)])
      expect(diagnosticNameProblem(name)).toBeDefined()
    expect(diagnosticNameProblem("界".repeat(33))).toBeUndefined()
    expect(diagnosticRetentionProblem("1", "1")).toBeUndefined()
    expect(diagnosticRetentionProblem("256", "2160")).toBeUndefined()
    for (const [runs, hours] of [
      ["0", "1"],
      ["257", "1"],
      ["2.5", "1"],
      ["1", "2161"],
      ["1", "0"],
      ["1", "1e2"],
    ])
      expect(diagnosticRetentionProblem(runs, hours)).toBeDefined()
  })
  test("keeps failure classes, missing answers and pending cleanup distinct", () => {
    for (const [outcome, label] of Object.entries({
      timed_out: "Timed out",
      refused: "Connection refused",
      dns_failure: "DNS failure",
      permission_denied: "Permission denied",
      unsupported: "Unsupported on this host",
      invalid_certificate: "Invalid certificate",
      completed_with_findings: "Completed with findings",
    }))
      expect(diagnosticReading({ status: "failed", outcome }).label).toBe(label)
    expect(diagnosticReading({ status: "cancelling", outcome: "completed" }).label).toBe("Stopping")
    expect(diagnosticReading({ status: "failed", outcome: "future_category" }).label).toBe(
      "No answer recorded",
    )
    expect(diagnosticReading({ status: "completed" }).label).toBe("Completed")
  })
})
