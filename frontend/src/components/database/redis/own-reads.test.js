import { describe, expect, test } from "bun:test"
import { lastRead, noteRead, ownEcho } from "./own-reads"

describe("an idle time that is the page's own doing", () => {
  const NOW = 1_800_000_000_000

  test("a key this page never read is idle for as long as the server says", () => {
    expect(ownEcho(2283, undefined, NOW)).toBe(false)
  })

  test("idle for exactly as long as since this page read it is the page's echo", () => {
    expect(ownEcho(15, NOW - 15_000, NOW)).toBe(true)
    expect(ownEcho(0, NOW - 300, NOW)).toBe(true)
    expect(ownEcho(16, NOW - 15_200, NOW)).toBe(true)
  })

  test("something else read it since: the figure is shorter, and true", () => {
    expect(ownEcho(3, NOW - 60_000, NOW)).toBe(false)
  })

  test("the server's clock was not reset by the read after all", () => {
    expect(ownEcho(2283, NOW - 60_000, NOW)).toBe(false)
  })
})

describe("the notes of what was read", () => {
  test("a key is noted per connection and database, by its bytes", () => {
    noteRead(4, 6, "user:1", 100)
    noteRead(4, 0, "user:1", 200)
    noteRead(4, 6, { base64: "/w==" }, 300)
    expect(lastRead(4, 6, "user:1")).toBe(100)
    expect(lastRead(4, 0, "user:1")).toBe(200)
    expect(lastRead(4, 6, { base64: "/w==" })).toBe(300)
    expect(lastRead(5, 6, "user:1")).toBeUndefined()
    expect(lastRead(4, 7, "user:1")).toBeUndefined()
  })

  test("a later read replaces the earlier", () => {
    noteRead(9, 0, "k", 1)
    noteRead(9, 0, "k", 2)
    expect(lastRead(9, 0, "k")).toBe(2)
  })
})
