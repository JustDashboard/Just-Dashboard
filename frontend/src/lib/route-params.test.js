import { expect, test } from "bun:test"
import { decodeSegment } from "./route-params"

test("a segment comes back as the name it encodes", () => {
  expect(decodeSegment("vpn%20clients")).toBe("vpn clients")
  expect(decodeSegment("br-9%2Fx")).toBe("br-9/x")
  expect(decodeSegment("g%2B%2B-13")).toBe("g++-13")
  expect(decodeSegment("nginx")).toBe("nginx")
})

test("a segment that does not decode is the name as typed, not an error", () => {
  expect(decodeSegment("50%off")).toBe("50%off")
  expect(decodeSegment("100%")).toBe("100%")
  expect(decodeSegment("%E0%A4%A")).toBe("%E0%A4%A")
})

test("a missing segment is empty", () => {
  expect(decodeSegment(undefined)).toBe("")
  expect(decodeSegment(null)).toBe("")
  expect(decodeSegment("")).toBe("")
})
