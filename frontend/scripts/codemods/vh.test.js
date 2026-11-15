import { describe, expect, test } from "bun:test"
import { vh } from "./vh.ts"
import { expectCommandLine, expectFixture, lines } from "./test-kit.js"

describe("vh", () => {
  test("vh becomes svh, and the workbench well becomes h-workbench", () => {
    const findings = expectFixture(vh)
    expect(lines(findings)).toEqual([
      "3 rewrite h-[min(75vh,40rem)] → h-workbench",
      "4 rewrite max-h-[55vh] → max-h-[55svh]",
      "5 rewrite lg:h-[min(80vh,48rem)] → lg:h-[min(80svh,48rem)]",
      "7 review min-h-screen → min-h-svh",
    ])
  })

  test("svh, dvh and vw are left as they are", () => {
    const text = 'const a = "h-[100dvh] max-h-[60svh] w-[50vw]"\n'
    expect(vh.apply(text, "a.ts").text).toBe(text)
  })

  test("--check fails on the old spelling, --dry-run writes nothing, a write fixes it", () => {
    expectCommandLine("vh")
  })
})
