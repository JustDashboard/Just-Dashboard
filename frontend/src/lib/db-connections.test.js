import { describe, expect, test } from "bun:test"
import { unusableReason } from "./db-connections"

// The list of saved connections carries the ones that no longer open, flagged.
// Every page outside the Databases section that picks from it asks here, so
// none of them offers a row whose every data route fails.
describe("saved connections that cannot be opened", () => {
  test("a usable row has no reason", () => {
    expect(unusableReason({})).toBeUndefined()
    expect(unusableReason({ broken: false, brokenReason: "left over" })).toBeUndefined()
  })

  test("a broken row says why, in the server's words", () => {
    expect(
      unusableReason({ broken: true, brokenReason: "its file is outside the file roots" }),
    ).toBe("its file is outside the file roots")
  })

  test("a broken row with no reason still reads as one", () => {
    expect(unusableReason({ broken: true })).toBe("This saved connection cannot be opened.")
    expect(unusableReason({ broken: true, brokenReason: "" })).toBe(
      "This saved connection cannot be opened.",
    )
  })
})
