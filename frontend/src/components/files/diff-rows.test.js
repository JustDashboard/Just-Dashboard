import { describe, expect, test } from "bun:test"
import { diffRows, gutterWidth } from "./diff-rows"

// The numbers down the side of a diff are only worth drawing if they are
// right: a line number off by one on a conflicted merge is worse than none.

const numbered = (rows) =>
  rows
    .filter((r) => r.kind === "add" || r.kind === "del" || r.kind === "context")
    .map((r) => `${r.oldNo ?? ""}|${r.newNo ?? ""}|${r.text}`)

describe("reading a unified diff", () => {
  test("selection ids stay attached to raw lines after hidden headers", () => {
    const body =
      "diff --git a/a b/a\nindex 111..222 100644\n--- a/a\n+++ b/a\n@@ -1 +1 @@\n-old\n+new\n\\ No newline at end of file"
    const rows = diffRows(body, true, true)
    expect(
      rows.filter((r) => r.kind === "del" || r.kind === "add").map((r) => r.sourceLine),
    ).toEqual([5, 6])
    expect(diffRows(body, true).every((r) => r.sourceLine === undefined)).toBe(true)
  })
  test("git's plumbing header goes, and the hunk header stays", () => {
    const rows = diffRows(
      [
        "diff --git a/a.txt b/a.txt",
        "index 03e0908..7bcfcaa 100644",
        "--- a/a.txt",
        "+++ b/a.txt",
        "@@ -1,3 +1,3 @@",
        " one",
        "-two",
        "+TWO",
        " three",
      ].join("\n"),
      true,
    )
    expect(rows.map((r) => r.kind)).toEqual(["hunk", "context", "del", "add", "context"])
  })

  test("each side is counted from its own start", () => {
    const rows = diffRows(
      ["@@ -10,4 +20,4 @@", " keep", "-gone", "+fresh", " keep"].join("\n"),
      true,
    )
    expect(numbered(rows)).toEqual(["10|20| keep", "11||-gone", "|21|+fresh", "12|22| keep"])
  })

  // `@@ -1 +1 @@` is what git prints for a one-line hunk. A parser that
  // insists on the comma numbers the whole file from line 1.
  test("a hunk header with no counts is still a hunk header", () => {
    const rows = diffRows(["@@ -7 +7 @@", "-old", "+new"].join("\n"), true)
    expect(rows[0].kind).toBe("hunk")
    expect(numbered(rows)).toEqual(["7||-old", "|7|+new"])
  })

  test("the no-newline note belongs to neither file", () => {
    const rows = diffRows(
      ["@@ -1,2 +1,2 @@", " a", "-b", "\\ No newline at end of file", "+b", ""].join("\n"),
      true,
    )
    expect(rows.find((r) => r.text.startsWith("\\")).kind).toBe("meta")
    expect(numbered(rows)).toEqual(["1|1| a", "2||-b", "|2|+b", "3|3|"])
  })

  test("a deleted line that looks like a header is content, not plumbing", () => {
    const rows = diffRows(
      [
        "diff --git a/q.sql b/q.sql",
        "--- a/q.sql",
        "+++ b/q.sql",
        "@@ -1,2 +1,1 @@",
        "--- a note",
        " select 1",
      ].join("\n"),
      true,
    )
    expect(rows.map((r) => r.kind)).toEqual(["hunk", "del", "context"])
    expect(rows[1].text).toBe("--- a note")
  })

  test("several files are separated by their names, and each restarts", () => {
    const rows = diffRows(
      [
        "diff --git a/one.txt b/one.txt",
        "@@ -1 +1 @@",
        "-a",
        "+b",
        "diff --git a/two.txt b/two.txt",
        "@@ -40 +40 @@",
        "-c",
        "+d",
      ].join("\n"),
    )
    expect(rows.filter((r) => r.kind === "heading").map((r) => r.text)).toEqual([
      "one.txt",
      "two.txt",
    ])
    expect(numbered(rows)).toEqual(["1||-a", "|1|+b", "40||-c", "|40|+d"])
  })

  test("a rename survives the header cull", () => {
    const rows = diffRows(
      [
        "diff --git a/old.txt b/new.txt",
        "similarity index 96%",
        "rename from old.txt",
        "rename to new.txt",
      ].join("\n"),
      true,
    )
    expect(rows.map((r) => r.kind)).toEqual(["rename", "rename"])
  })

  test("text that is not a diff at all is drawn, not counted", () => {
    const rows = diffRows("No textual diff (binary file, or no line changes).", true)
    expect(rows).toHaveLength(1)
    expect(rows[0].kind).toBe("meta")
  })
})

describe("the gutter", () => {
  test("is as wide as the widest number, and never narrower than two", () => {
    expect(gutterWidth(diffRows("@@ -1 +1 @@\n-a\n+b", true))).toBe(2)
    expect(gutterWidth(diffRows("@@ -998,3 +998,3 @@\n a\n-b\n+c\n d", true))).toBe(4)
  })
})
