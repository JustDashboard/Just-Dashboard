import { describe, expect, test } from "bun:test"
import { pendingHeadline, pendingItems } from "./settings-pending-model"

const waits = (name) => ({ name, mono: true, state: "restart" })

describe("what the strip at the head of a database's settings names", () => {
  test("the parameters the server marks, each once", () => {
    expect(pendingItems([waits("shared_buffers"), waits("wal_level")], [])).toEqual({
      waiting: [waits("shared_buffers"), waits("wal_level")],
      edits: [],
    })
  })

  test("a section that speaks for a parameter takes its place, first", () => {
    const libraries = [
      { name: "pg_cron", mono: true, state: "added" },
      { name: "auto_explain", mono: true, state: "removed" },
    ]
    const { waiting } = pendingItems(
      [waits("shared_preload_libraries"), waits("shared_buffers")],
      [{ waiting: libraries, covers: ["shared_preload_libraries"] }],
    )
    expect(waiting).toEqual([...libraries, waits("shared_buffers")])
  })

  test("what a page reports and what the server marks are one item when they name the same thing", () => {
    const { waiting } = pendingItems([waits("wal_level")], [{ waiting: [waits("wal_level")] }])
    expect(waiting).toHaveLength(1)
  })

  test("unsaved edits are every section's, in the order they reported", () => {
    const { edits } = pendingItems(
      [],
      [
        { edits: [{ name: "Address", state: "changed" }] },
        { edits: [{ name: "Name", state: "changed" }] },
      ],
    )
    expect(edits.map((item) => item.name)).toEqual(["Address", "Name"])
  })

  test("the headline counts what waits, and what is typed beside it", () => {
    expect(pendingHeadline(1, 0)).toBe("1 waiting for a restart")
    expect(pendingHeadline(2, 1)).toBe("2 waiting for a restart · 1 unsaved change")
    expect(pendingHeadline(1, 3)).toBe("1 waiting for a restart · 3 unsaved changes")
  })
})
