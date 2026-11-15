import { describe, expect, test } from "bun:test"
import { utilities } from "./utilities.ts"
import { expectCommandLine, expectFixture, lines } from "./test-kit.js"

describe("utilities", () => {
  test("the recurring layout values become their names", () => {
    const findings = expectFixture(utilities)
    expect(lines(findings, "rewrite")).toEqual([
      "7 rewrite grid-cols-[minmax(0,1fr)_auto] → grid-cols-main-end",
      "8 rewrite grid-cols-[auto_minmax(0,_1fr)] → grid-cols-start-main",
      "9 rewrite sm:grid-cols-[auto_minmax(0,1fr)_auto] → sm:grid-cols-start-main-end",
      "10 rewrite xl:grid-rows-[minmax(0,1fr)] → xl:grid-rows-fill",
      "11 rewrite w-[18rem] → w-rail",
      "12 rewrite lg:w-[22rem] → lg:w-inspector",
      "13 rewrite h-[min(75svh,40rem)] → h-workbench",
      "13 rewrite max-h-[calc(100svh-13rem)] → max-h-rail",
      "14 rewrite align-[-2px] → glyph-inline",
      "16 rewrite text-[0.92em] → code-inline",
    ])
  })

  test("glyph-inline and code-inline take the class they include with them, or are reported", () => {
    const findings = expectFixture(utilities)
    expect(lines(findings, "review")).toEqual([
      "15 review align-[-2px]",
      "17 review text-[0.9em] → code-inline",
      "18 review xl:grid-cols-[minmax(0,1fr)_24rem] → xl:grid-cols-[minmax(0,1fr)_22rem]",
    ])
  })

  test("--check fails on the old spelling, --dry-run writes nothing, a write fixes it", () => {
    expectCommandLine("utilities")
  })
})
