import { describe, expect, test } from "bun:test"
import { diffCounts, lineDiff, unifiedBody } from "./line-diff"

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

describe("lineDiff, exact", () => {
  const exact = (before, after) => lineDiff(before, after, { exact: true })

  test("an absent file is no lines, so a new file is only what it adds", () => {
    const diff = exact("", "one\ntwo\n")
    expect(diffCounts(diff)).toEqual({ added: 2, removed: 0 })
    expect(diff).toEqual([
      { kind: "added", text: "one" },
      { kind: "added", text: "two" },
    ])
  })

  test("emptying a file removes every line and adds none", () => {
    expect(diffCounts(exact("one\ntwo\n", ""))).toEqual({ added: 0, removed: 2 })
  })

  test("a replacement that only changes a trailing space is a change", () => {
    const diff = exact("a\nkeep \n", "a\nkeep\n")
    expect(diffCounts(diff)).toEqual({ added: 1, removed: 1 })
    expect(diff.filter((l) => l.kind !== "same").map((l) => l.text)).toEqual(["keep ", "keep"])
    expect(diffCounts(exact("a", "a "))).toEqual({ added: 1, removed: 1 })
  })

  test("a newline added at the end is a change, and the body carries git's note", () => {
    const diff = exact("a\nb", "a\nb\n")
    expect(diff).toEqual([
      { kind: "same", text: "a" },
      { kind: "removed", text: "b", noNewline: true },
      { kind: "added", text: "b" },
    ])
    expect(unifiedBody(diff)).toBe(" a\n-b\n\\ No newline at end of file\n+b")
  })

  test("identical texts, final newline and all, are no lines at all", () => {
    expect(exact("same\n", "same\n")).toEqual([])
    expect(exact("same", "same")).toEqual([])
    expect(exact("", "")).toEqual([])
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
