import { expect, test } from "bun:test"
import { logPlaceIdentities } from "./log-place"

test("fresh responses retain line identities while repeated identical records remain distinct", () => {
  const lines = [
    { text: "sensitive fixture", timestamp: "2026-10-03T12:00:00Z" },
    { text: "sensitive fixture", timestamp: "2026-10-03T12:00:00Z" },
    { text: "different" },
  ]
  const ids = logPlaceIdentities(lines)
  expect(logPlaceIdentities(structuredClone(lines))).toEqual(ids)
  expect(new Set(ids).size).toBe(3)
  expect(ids.join()).not.toContain("sensitive fixture")
  expect(logPlaceIdentities([{ ...lines[0], source: "another" }])[0]).not.toBe(ids[0])
})
