import { describe, expect, test } from "bun:test"
import { applyPatches, foldChange, patchesAfter } from "./live-patches"

const row = (name, size = 1, modified = "2026-11-20T10:00:00Z") => ({
  name,
  path: `/srv/app/${name}`,
  size,
  mode: "-rw-r--r--",
  modeOctal: "0644",
  isDir: false,
  isSymlink: false,
  modified,
  owner: "app",
  group: "app",
  uid: 1000,
  gid: 1000,
})

const listing = (entries) => ({ path: "/srv/app", parent: "/srv", roots: ["/"], entries })

describe("a followed folder's changes over its listing", () => {
  test("a new name is added, a removed one leaves and a grown one is replaced", () => {
    const kept = row("kept.txt")
    const base = listing([kept, row("grows.log", 10), row("going.txt")])
    const held = foldChange(
      new Map(),
      { entries: [row("grows.log", 20), row("new.txt")], removed: ["going.txt"] },
      1,
    )
    const merged = applyPatches(base, patchesAfter(held, 0))
    expect(merged.entries.map((e) => e.name)).toEqual(["kept.txt", "grows.log", "new.txt"])
    expect(merged.entries[1].size).toBe(20)
    // Rows nobody changed are the very same objects: nothing for React to redo.
    expect(merged.entries[0]).toBe(kept)
  })

  test("a row reported unchanged keeps its object", () => {
    const same = row("same.txt")
    const base = listing([same])
    const held = foldChange(new Map(), { entries: [{ ...same }], removed: [] }, 1)
    expect(applyPatches(base, patchesAfter(held, 0)).entries[0]).toBe(same)
  })

  test("the later batch wins for a name, and a rename is a removal and an arrival", () => {
    let held = foldChange(new Map(), { entries: [row("a.txt", 1)], removed: [] }, 1)
    held = foldChange(held, { entries: [row("b.txt")], removed: ["a.txt"] }, 2)
    const merged = applyPatches(listing([]), patchesAfter(held, 0))
    expect(merged.entries.map((e) => e.name)).toEqual(["b.txt"])
  })

  test("patches a fresh listing already holds are dropped by number", () => {
    let held = foldChange(new Map(), { entries: [row("old.txt", 5)], removed: [] }, 1)
    held = foldChange(held, { entries: [row("newer.txt")], removed: [] }, 2)
    expect([...patchesAfter(held, 1).keys()]).toEqual(["newer.txt"])
    expect(patchesAfter(held, 2).size).toBe(0)
  })

  test("no patches leave the listing as it was", () => {
    const base = listing([row("x")])
    expect(applyPatches(base, new Map())).toBe(base)
  })
})
