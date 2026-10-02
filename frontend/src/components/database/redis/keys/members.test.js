import { describe, expect, test } from "bun:test"
import { memberId, mergeMembers } from "./members"

describe("what tells one member from another", () => {
  test("a hash's rows are its fields", () => {
    expect(memberId("hash", { field: "name", value: "x" })).toBe("t:name")
    expect(memberId("hash", { field: "", value: "x" })).toBe("t:")
  })

  // Rows used to be keyed by score, and two members that share a score were
  // the same row.
  test("a sorted set's rows are its members, never their scores", () => {
    expect(memberId("zset", { value: "a", score: 1 })).not.toBe(
      memberId("zset", { value: "b", score: 1 }),
    )
  })

  test("a list's rows are positions and a stream's are entry ids", () => {
    expect(memberId("list", { index: 3, value: "x" })).toBe("i:3")
    expect(memberId("stream", { id: "1-0", fields: [] })).toBe("e:1-0")
  })

  test("bytes that are not text are told apart from text", () => {
    expect(memberId("set", { value: { base64: "/w==" } })).toBe("b:/w==")
  })
})

describe("pages of members", () => {
  test("a field a scan repeats is one row, with its newest value, where it stood", () => {
    const first = mergeMembers(undefined, {
      type: "hash",
      length: 3,
      rows: [
        { field: "a", value: "1" },
        { field: "b", value: "2" },
      ],
    })
    const both = mergeMembers(first, {
      type: "hash",
      length: 3,
      rows: [
        { field: "a", value: "changed" },
        { field: "c", value: "3" },
      ],
    })
    expect(both.rows).toEqual([
      { field: "a", value: "changed" },
      { field: "b", value: "2" },
      { field: "c", value: "3" },
    ])
  })

  test("the count is the key's own, not how many rows are loaded", () => {
    const page = mergeMembers(undefined, { type: "set", length: 1500, rows: [{ value: "m1" }] })
    expect(page.length).toBe(1500)
    expect(page.rows.length).toBe(1)
  })
})
