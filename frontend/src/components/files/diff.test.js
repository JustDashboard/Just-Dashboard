import { describe, expect, test } from "bun:test"
import { unifiedDiff } from "./diff"

describe("unifiedDiff", () => {
  test("an unchanged draft is an empty diff", () => {
    expect(unifiedDiff("a\nb\nc\n", "a\nb\nc\n", "x.conf")).toBe("")
  })

  test("a change in the middle keeps three lines of context and names the file", () => {
    const before = Array.from({ length: 20 }, (_, i) => `line ${i + 1}`).join("\n") + "\n"
    const after = before.replace("line 10", "line ten")
    const diff = unifiedDiff(before, after, "x.conf")
    expect(diff).not.toBeNull()
    const lines = diff.split("\n")
    expect(lines[0]).toBe("--- a/x.conf")
    expect(lines[1]).toBe("+++ b/x.conf")
    expect(lines[2]).toBe("@@ -7,7 +7,7 @@")
    expect(lines).toContain("-line 10")
    expect(lines).toContain("+line ten")
    // Three lines either side, and nothing from the far ends of the file.
    expect(lines).toContain(" line 7")
    expect(lines).toContain(" line 13")
    expect(lines).not.toContain(" line 6")
    expect(lines).not.toContain(" line 14")
  })

  test("two distant changes are two hunks", () => {
    const before = Array.from({ length: 40 }, (_, i) => `${i + 1}`).join("\n")
    const after = before.replace("\n5\n", "\nfive\n").replace("\n35\n", "\nthirty-five\n")
    const diff = unifiedDiff(before, after, "n")
    expect(diff.split("\n").filter((l) => l.startsWith("@@"))).toHaveLength(2)
  })

  test("an insertion at the end and a deletion at the start are both reported", () => {
    expect(unifiedDiff("a\nb\n", "a\nb\nc\n", "f")).toContain("+c")
    expect(unifiedDiff("a\nb\n", "b\n", "f")).toContain("-a")
  })

  test("a rewrite too large to align is refused rather than attempted", () => {
    const before = Array.from({ length: 3000 }, (_, i) => `old ${i}`).join("\n")
    const after = Array.from({ length: 3000 }, (_, i) => `new ${i}`).join("\n")
    expect(unifiedDiff(before, after, "big")).toBeNull()
  })
})
