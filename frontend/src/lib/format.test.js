import { expect, test } from "bun:test"
import { byteParts, bytes, rate } from "./format"

test("a value under one byte stays in bytes instead of reaching below the ladder", () => {
  expect(bytes(0.5)).toBe("1 B")
  expect(bytes(0.2)).toBe("0 B")
  expect(bytes(-0.2)).toBe("0 B")
  expect(bytes(-0.5)).not.toContain("undefined")
  expect(rate(0.5)).toBe("1 B/s")
  expect(rate(0.2)).toBe("0 B/s")
})

test("zero and an unknown value read as themselves", () => {
  expect(bytes(0)).toBe("0 B")
  expect(rate(0)).toBe("0 B/s")
  expect(bytes(undefined)).toBe("—")
  expect(bytes(null)).toBe("—")
  expect(bytes(Number.NaN)).toBe("—")
  expect(rate(undefined)).toBe("0 B/s")
})

test("each size takes its unit, at the precision asked for", () => {
  expect(bytes(1)).toBe("1 B")
  expect(bytes(1023)).toBe("1023 B")
  expect(bytes(1024)).toBe("1.0 KB")
  expect(bytes(1536, 2)).toBe("1.50 KB")
  expect(bytes(5 * 1024 ** 3)).toBe("5.0 GB")
  expect(rate(2048)).toBe("2.0 KB/s")
})

test("a negative value scales by its size and keeps its sign", () => {
  expect(bytes(-1)).toBe("-1 B")
  expect(bytes(-2048)).toBe("-2.0 KB")
  expect(rate(-2048)).toBe("-2.0 KB/s")
})

test("past the top of the ladder a value stays in its last unit", () => {
  expect(bytes(1024 ** 5)).toBe("1.0 PB")
  expect(bytes(2 ** 70)).toBe("1048576.0 PB")
})

test("byteParts and bytes agree about values under one byte", () => {
  expect(byteParts(0.5)).toEqual({ value: 0.5, unit: "B", decimals: 0 })
  expect(byteParts(-3)).toEqual({ value: 0, unit: "B", decimals: 0 })
})
