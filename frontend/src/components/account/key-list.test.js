import { expect, test } from "bun:test"
import { DAY } from "./access"
import { inKeyFilter, matchesKey, orderKeys, replacementRoles } from "./key-list"

const now = Date.parse("2026-10-15T12:00:00Z")
const iso = (ms) => new Date(ms).toISOString()
const key = (over = {}) => ({
  id: 1,
  userId: 1,
  name: "github-actions",
  prefix: "vpsd_ab12",
  role: "limited",
  createdAt: iso(now - 40 * DAY),
  revoked: false,
  ...over,
})

test("search matches visible key and owner facts, with every word required", () => {
  const owner = { username: "ion", displayName: "Ion Moisei" }
  expect(matchesKey(key(), "  GITHUB   limited ")).toBe(true)
  expect(matchesKey(key(), "ab12")).toBe(true)
  expect(matchesKey(key(), "ion moisei", owner)).toBe(true)
  expect(matchesKey(key(), "moisei")).toBe(false)
  expect(matchesKey(key(), "github admin")).toBe(false)
  expect(matchesKey(key(), "   ")).toBe(true)
})

test("unused for 30 days means recorded use at least 30 days ago", () => {
  expect(inKeyFilter(key(), "idle", now)).toBe(false)
  expect(inKeyFilter(key({ lastUsedAt: iso(now - 30 * DAY) }), "idle", now)).toBe(true)
  expect(inKeyFilter(key({ lastUsedAt: iso(now - 29 * DAY) }), "idle", now)).toBe(false)
  expect(inKeyFilter(key(), "unused", now)).toBe(true)
  expect(inKeyFilter(key(), "unbounded", now)).toBe(true)
  expect(inKeyFilter(key({ expiresAt: iso(now + DAY) }), "unbounded", now)).toBe(false)
})

test("sorting puts unbounded and never-used keys last without changing the input", () => {
  const keys = [
    key({ id: 1, name: "zebra" }),
    key({ id: 2, name: "alpha", expiresAt: iso(now + DAY), lastUsedAt: iso(now - DAY) }),
    key({ id: 3, name: "beta", expiresAt: iso(now + 2 * DAY), lastUsedAt: iso(now) }),
  ]
  expect(orderKeys(keys, "expiry").map((k) => k.id)).toEqual([2, 3, 1])
  expect(orderKeys(keys, "used").map((k) => k.id)).toEqual([3, 2, 1])
  expect(orderKeys(keys, "name").map((k) => k.id)).toEqual([2, 3, 1])
  expect(orderKeys(keys, "newest").map((k) => k.id)).toEqual([3, 2, 1])
  expect(keys.map((k) => k.id)).toEqual([1, 2, 3])
})

test("replacement roles never widen the original key or its current owner's role", () => {
  expect(replacementRoles("readonly", "admin")).toEqual(["readonly"])
  expect(replacementRoles("admin", "limited")).toEqual(["limited", "readonly"])
  expect(replacementRoles("limited", "admin")).toEqual(["limited", "readonly"])
})
