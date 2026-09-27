import { expect, test } from "bun:test"
import { logLineKey } from "./log-line-key"

test("eviction preserves retained line identities, including repeated text", () => {
  const lines = Array.from({ length: 4000 }, () => ({ text: "repeated" }))
  const keys = lines.map(logLineKey)
  const next = [...lines.slice(50), ...Array.from({ length: 50 }, () => ({ text: "repeated" }))]
  const nextKeys = next.map(logLineKey)
  expect(nextKeys.slice(0, 3950)).toEqual(keys.slice(50))
  expect(new Set([...keys, ...nextKeys]).size).toBe(4050)
})
