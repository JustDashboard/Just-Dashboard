import { describe, expect, test } from "bun:test"
import { KIND_ORDER, kindCounts } from "./release-kinds"

const release = (version, kinds) => ({
  version,
  date: "2026-10-13",
  title: version,
  changes: kinds.map((kind, i) => ({ kind, text: `${kind} ${i}` })),
})

describe("kindCounts", () => {
  test("counts every release in the run together, security first", () => {
    const counts = kindCounts([
      release("0.7.5", ["added", "fixed", "added"]),
      release("0.7.4", ["security", "fixed"]),
    ])
    expect(counts).toEqual([
      { kind: "security", n: 1 },
      { kind: "added", n: 2 },
      { kind: "fixed", n: 2 },
    ])
  })

  test("leaves out the kinds nothing changed under", () => {
    expect(kindCounts([release("0.7.5", ["removed"])])).toEqual([{ kind: "removed", n: 1 }])
    expect(kindCounts([release("0.7.5", [])])).toEqual([])
    expect(kindCounts([])).toEqual([])
  })

  test("follows the order the kinds are listed in, whatever order the changes came in", () => {
    const counts = kindCounts([
      release("0.7.5", ["removed", "deprecated", "fixed", "changed", "added", "security"]),
    ])
    expect(counts.map((count) => count.kind)).toEqual(KIND_ORDER)
  })
})
