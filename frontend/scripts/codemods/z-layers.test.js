import { describe, expect, test } from "bun:test"
import { zLayers } from "./z-layers.ts"
import { expectCommandLine, expectFixture, lines } from "./test-kit.js"

describe("z-layers", () => {
  test("each number becomes its layer; z-50 by what it is", () => {
    const findings = expectFixture(zLayers)
    expect(lines(findings)).toEqual([
      "5 rewrite z-50 → z-immersive",
      "6 rewrite z-20 → z-sticky",
      "7 rewrite z-10 → z-raised",
      "7 rewrite focus-visible:z-10 → focus-visible:z-raised",
      "8 rewrite z-30 → z-pane",
      "8 rewrite max-lg:z-20 → max-lg:z-sticky",
      "9 rewrite z-40 → z-float",
      "10 rewrite z-50 → z-overlay",
      "12 review z-[1]",
      "13 review -z-10",
    ])
  })

  test("z-50 in a tooltip's file is the tooltip layer", () => {
    const out = zLayers.apply(
      'const a = "z-50 w-fit rounded-md"\n',
      "src/components/ui/tooltip.tsx",
    )
    expect(out.text).toBe('const a = "z-tooltip w-fit rounded-md"\n')
  })

  test("a full-screen view switched on by a ternary is immersive too", () => {
    const text = 'const a = fullScreen ? "fixed inset-0 z-50" : "relative"\n'
    expect(zLayers.apply(text, "src/a.ts").text).toBe(
      'const a = fullScreen ? "fixed inset-0 z-immersive" : "relative"\n',
    )
  })

  test("--check fails on the old spelling, --dry-run writes nothing, a write fixes it", () => {
    expectCommandLine("z-layers")
  })
})
