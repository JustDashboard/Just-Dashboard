import { describe, expect, test } from "bun:test"
import { countChanges, splitDiff } from "./diff-files"

const body = `diff --git a/src/app.ts b/src/app.ts
index 1111111..2222222 100644
--- a/src/app.ts
+++ b/src/app.ts
@@ -1,3 +1,4 @@
 import x from "x"
-const a = 1
+const a = 2
+const b = 3
diff --git a/docs/old.md b/docs/old.md
deleted file mode 100644
--- a/docs/old.md
+++ /dev/null
@@ -1,2 +0,0 @@
-# Old
--- a list item that starts with a dash
diff --git a/lib/before.ts b/lib/after.ts
similarity index 90%
rename from lib/before.ts
rename to lib/after.ts
`

describe("a working tree's diff, file by file", () => {
  test("each file is named by its new path and carries its own lines", () => {
    const files = splitDiff(body)
    expect(files.map((f) => f.path)).toEqual(["src/app.ts", "docs/old.md", "lib/after.ts"])
    expect(files[0].body).toStartWith("diff --git a/src/app.ts")
    expect(files[0].body).not.toContain("docs/old.md")
  })

  test("the counts skip the file headers but not a removed line that looks like one", () => {
    const [app, old, renamed] = splitDiff(body)
    expect([app.additions, app.deletions]).toEqual([2, 1])
    expect([old.additions, old.deletions]).toEqual([0, 2])
    expect([renamed.additions, renamed.deletions]).toEqual([0, 0])
  })

  test("nothing changed is no files", () => {
    expect(splitDiff("")).toEqual([])
    expect(countChanges("")).toEqual({ additions: 0, deletions: 0 })
  })
})
