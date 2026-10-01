import { describe, expect, test } from "bun:test"
import { leafName, mergeKeys, mergeLevel, scanProgress } from "./scan"

const key = (name, type = "string") => ({ key: name, type, ttl: -1, size: 1 })
const folder = (name, count, over = {}) => ({
  name,
  prefix: `${name}:`,
  pattern: `${name}:*`,
  count,
  keyCount: count,
  folders: 0,
  types: { string: count },
  ...over,
})
const tree = (over) => ({
  db: 6,
  delimiter: ":",
  prefix: "",
  folders: [],
  keys: [],
  keyCount: 0,
  count: 0,
  types: {},
  scanned: 0,
  cursor: "0",
  complete: true,
  total: 100,
  elapsedMs: 1,
  ...over,
})

describe("pages of a scan", () => {
  test("a key the scan hands over twice is listed once", () => {
    const first = mergeKeys(undefined, {
      keys: [key("a"), key("b")],
      cursor: "17",
      done: false,
      db: 6,
      total: 9,
    })
    const both = mergeKeys(first, {
      keys: [key("b"), key("c"), key({ base64: "/w==" })],
      cursor: "0",
      done: true,
      db: 6,
      total: 9,
    })
    expect(both.keys.map((entry) => entry.key)).toEqual(["a", "b", "c", { base64: "/w==" }])
    expect(both.total).toBe(9)
    expect(both.db).toBe(6)
  })

  test("the held list is not changed in place", () => {
    const first = mergeKeys(undefined, {
      keys: [key("a")],
      cursor: "1",
      done: false,
      db: 0,
      total: 2,
    })
    mergeKeys(first, { keys: [key("b")], cursor: "0", done: true, db: 0, total: 2 })
    expect(first.keys.length).toBe(1)
  })
})

describe("calls of the namespace walk", () => {
  test("each call counts what it examined, so the pieces are added by namespace", () => {
    const first = mergeLevel(
      undefined,
      tree({
        folders: [folder("session", 600), folder("user", 300)],
        count: 900,
        scanned: 1000,
        types: { string: 900 },
        complete: false,
        cursor: "512",
      }),
    )
    const whole = mergeLevel(
      first,
      tree({
        folders: [folder("session", 900), folder("cache", 50), folder("user", 500)],
        count: 1450,
        scanned: 1500,
        types: { string: 1400, hash: 50 },
      }),
    )
    expect(whole.folders.map((f) => [f.name, f.count])).toEqual([
      ["session", 1500],
      ["user", 800],
      ["cache", 50],
    ])
    expect(whole.folders[0].types).toEqual({ string: 1500 })
    expect(whole.count).toBe(2350)
    expect(whole.scanned).toBe(2500)
    expect(whole.types).toEqual({ string: 2300, hash: 50 })
    expect(whole.complete).toBe(true)
  })

  test("namespaces are largest first, then by name, with numbers in number order", () => {
    const level = mergeLevel(
      undefined,
      tree({ folders: [folder("10", 2), folder("2", 2), folder("big", 4), folder("1", 2)] }),
    )
    expect(level.folders.map((f) => f.name)).toEqual(["big", "1", "2", "10"])
  })

  test("direct keys are listed once, by name", () => {
    const first = mergeLevel(undefined, tree({ keys: [key("b"), key("a")], keyCount: 2 }))
    const both = mergeLevel(first, tree({ keys: [key("a"), key("c")], keyCount: 2 }))
    expect(both.keys.map((entry) => entry.key)).toEqual(["a", "b", "c"])
    expect(both.keyCount).toBe(4)
  })

  test("what the server left out is carried", () => {
    const level = mergeLevel(mergeLevel(undefined, tree({ foldersOmitted: 3 })), tree({}))
    expect(level.foldersOmitted).toBe(3)
  })
})

describe("a key under its namespace", () => {
  test("is drawn as what follows the prefix", () => {
    expect(leafName("user:1:profile", "user:1:")).toBe("profile")
    expect(leafName("counter", "")).toBe("counter")
    expect(leafName("other:key", "user:")).toBe("other:key")
  })
})

describe("how far a scan has come", () => {
  test("a walk says how many keys it examined of how many there are", () => {
    expect(
      scanProgress({ found: 2000, scanned: 2000, total: 48_000, complete: false, filtered: false }),
    ).toBe("Scanned 2,000 of about 48,000")
    expect(
      scanProgress({ found: 2860, scanned: 2860, total: 2860, complete: true, filtered: false }),
    ).toBe("Scanned all 2,860 keys")
  })

  test("a listing says what it holds, and 'so far' until the cursor has come round", () => {
    expect(scanProgress({ found: 200, total: 2860, complete: false, filtered: false })).toBe(
      "200 of about 2,860 keys",
    )
    expect(scanProgress({ found: 37, total: 2860, complete: false, filtered: true })).toBe(
      "37 found so far in 2,860 keys",
    )
    expect(scanProgress({ found: 37, total: 2860, complete: true, filtered: true })).toBe(
      "37 of 2,860 keys match",
    )
    expect(scanProgress({ found: 9, total: 9, complete: true, filtered: false })).toBe("All 9 keys")
  })
})
