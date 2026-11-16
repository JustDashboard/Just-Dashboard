import { describe, expect, test } from "bun:test"
import { diffCounts, lineDiff, unifiedBody } from "./diff"

describe("lineDiff", () => {
  test("marks what was removed and added, with no section unless asked for one", () => {
    const diff = lineDiff("a\nb\nc\n", "a\nB\nc\n")
    expect(diff).toEqual([
      { kind: "same", text: "a" },
      { kind: "removed", text: "b" },
      { kind: "added", text: "B" },
      { kind: "same", text: "c" },
    ])
  })

  test("keeps three lines around a change and leaves one gap for the rest", () => {
    const lines = Array.from({ length: 20 }, (_, i) => `line ${i}`)
    const after = [...lines]
    after[2] = "changed early"
    after[17] = "changed late"
    const diff = lineDiff(lines.join("\n"), after.join("\n"))
    expect(diff.filter((l) => l.kind === "gap")).toEqual([{ kind: "gap", text: "…" }])
    expect(diff[0]).toEqual({ kind: "same", text: "line 0" })
    expect(diff.at(-1)).toEqual({ kind: "same", text: "line 19" })
  })

  test("a file that was absent is every line added", () => {
    const diff = lineDiff("", "one\ntwo")
    expect(diff.filter((l) => l.kind === "added").map((l) => l.text)).toEqual(["one", "two"])
  })

  test("a section reader names each line's section; a removed line does not move it", () => {
    const sectionOf = (current, line) => (line.endsWith(":") ? line.slice(0, -1) : current)
    const diff = lineDiff("api:\n  x\ndb:\n  y\n", "api:\n  x2\ndb:\n  y\n", { sectionOf })
    expect(diff.filter((l) => l.kind !== "same")).toEqual([
      { kind: "removed", text: "  x", section: "api" },
      { kind: "added", text: "  x2", section: "api" },
    ])
  })

  test("identical texts are no lines at all", () => {
    expect(lineDiff("same\n", "same")).toEqual([])
  })
})

describe("diffCounts", () => {
  test("counts the added and the removed lines, not the context or the gaps", () => {
    const diff = lineDiff("a\nb\nc\nd\n", "a\nc\nd\ne\nf\n")
    expect(diffCounts(diff)).toEqual({ added: 2, removed: 1 })
    expect(diffCounts([])).toEqual({ added: 0, removed: 0 })
  })
})

describe("unifiedBody", () => {
  test("prefixes each line the way a unified diff does", () => {
    expect(unifiedBody(lineDiff("a\nb\n", "a\nc\n"))).toBe(" a\n-b\n+c")
  })
})
