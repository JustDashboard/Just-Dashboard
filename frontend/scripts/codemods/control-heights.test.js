import { describe, expect, test } from "bun:test"
import { controlHeights } from "./control-heights.ts"
import { expectCommandLine, expectFixture, lines } from "./test-kit.js"

describe("control-heights", () => {
  test("phone overrides go wherever the desk height stays the same", () => {
    const findings = expectFixture(controlHeights)
    expect(lines(findings, "rewrite")).toEqual([
      "8 rewrite h-11 → ",
      "8 rewrite sm:h-9 → ",
      "9 rewrite h-11 → ",
      "9 rewrite sm:h-9 → ",
      "10 rewrite h-11 → ",
      "10 rewrite sm:h-9 → ",
      "19 rewrite max-sm:h-8 → ",
      "22 rewrite max-sm:h-10 → ",
      "26 rewrite h-8 → h-7",
    ])
  })

  test("a pair that would move a desk height, or that a fixed height outranks, is reported", () => {
    const findings = expectFixture(controlHeights)
    expect(lines(findings, "review")).toEqual([
      "13 review h-11",
      "16 review h-11",
      "23 review max-sm:h-8",
    ])
  })

  test("an element that is not a Button or an Input is not touched", () => {
    const text = 'export const a = <div className="h-11 sm:h-9" />\n'
    expect(controlHeights.apply(text, "a.tsx").text).toBe(text)
  })

  test("--check fails on the old spelling, --dry-run writes nothing, a write fixes it", () => {
    expectCommandLine("control-heights")
  })
})
