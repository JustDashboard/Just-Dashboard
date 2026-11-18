import { describe, expect, test } from "bun:test"
import { radius } from "./radius.ts"
import { expectCommandLine, expectFixture, lines } from "./test-kit.js"

// The swatch step's name is held apart from its prefix: the lint's radius-scale rule refused it
// before 0.9.0 put it on the ladder.
const XS = "xs"

describe("radius", () => {
  test("an arbitrary radius that is a step becomes the step, sides and corners included", () => {
    const findings = expectFixture(radius)
    expect(lines(findings, "rewrite")).toEqual([
      `1 rewrite rounded-[2px] → rounded-${XS}`,
      "7 rewrite rounded-[4px] → rounded-sm",
      "8 rewrite rounded-tl-[0.25rem] → rounded-tl-sm",
      `8 rewrite first:rounded-t-[2px] → first:rounded-t-${XS}`,
      "9 rewrite rounded-[0.625rem] → rounded-tile-md",
      "10 rewrite rounded-[0.875rem] → rounded-tile-lg",
      "14 rewrite rounded-[12px] → rounded-xl",
      "14 rewrite md:rounded-[6px] → md:rounded-md",
    ])
  })

  test("one off the ladder is reported, with the near step as a suggestion; inherit stays", () => {
    const findings = expectFixture(radius)
    expect(lines(findings, "review")).toEqual([
      "11 review rounded-[1.125rem]",
      `12 review rounded-[3px] → rounded-${XS}`,
      "13 review rounded-[50%]",
    ])
    expect(findings.some((f) => f.from === "rounded-[inherit]")).toBe(false)
  })

  test("--check fails on the old spelling, --dry-run writes nothing, a write fixes it", () => {
    expectCommandLine("radius")
  })
})
