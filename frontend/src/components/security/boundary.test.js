import { expect, test } from "bun:test"
import { boundaryQuery, boundaryVerdict, judgeable } from "./boundary"

test("a proposal becomes the query the check reads, settings sorted", () => {
  expect(boundaryQuery({ kind: "ban", target: "203.0.113.9" })).toEqual({
    kind: "ban",
    target: "203.0.113.9",
  })
  expect(
    boundaryQuery({ kind: "ssh", settings: { port: "2222", allowtcpforwarding: "no" } }),
  ).toEqual({ kind: "ssh", setting: ["allowtcpforwarding=no", "port=2222"] })
})

test("only a complete proposal is judged", () => {
  expect(judgeable(undefined)).toBe(false)
  expect(judgeable({ kind: "ban", target: "" })).toBe(false)
  expect(judgeable({ kind: "ban", target: "scanning" })).toBe(false)
  expect(judgeable({ kind: "ban", target: "100.64.0.0/10" })).toBe(true)
  expect(judgeable({ kind: "ban", target: "2001:db8::1" })).toBe(true)
  expect(judgeable({ kind: "ssh", settings: {} })).toBe(false)
  expect(judgeable({ kind: "ssh", settings: { port: "2222" } })).toBe(true)
})

test("a cut is refused, an effect is acknowledged, nothing is clear", () => {
  expect(boundaryVerdict([])).toBe("clear")
  expect(boundaryVerdict([{ boundary: "allowlist", level: "affects", text: "x" }])).toBe(
    "acknowledge",
  )
  expect(
    boundaryVerdict([
      { boundary: "allowlist", level: "affects", text: "x" },
      { boundary: "ssh", level: "cuts", text: "y" },
    ]),
  ).toBe("refused")
})
