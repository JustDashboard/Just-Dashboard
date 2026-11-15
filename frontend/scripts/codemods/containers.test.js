import { describe, expect, test } from "bun:test"
import { containers } from "./containers.ts"
import { expectCommandLine, expectFixture, lines } from "./test-kit.js"

describe("containers", () => {
  test("a container width that is a size takes its name, and a named container keeps its name", () => {
    const findings = expectFixture(containers)
    expect(lines(findings)).toEqual([
      "4 rewrite @min-[36rem]:grid-cols-2 → @xl:grid-cols-2",
      "5 rewrite @min-[36rem]/detail:flex → @xl/detail:flex",
      "6 rewrite @max-[38rem]:flex-col → @max-stack:flex-col",
      "7 rewrite @max-[36rem]/strip:hidden → @max-xl/strip:hidden",
      "8 rewrite @min-[56rem]:grid-cols-3 → @4xl:grid-cols-3",
      "8 rewrite @[64rem]:gap-4 → @5xl:gap-4",
      "9 review @max-[34rem]:block → @max-stack:block",
      "10 review @max-[46rem]:hidden",
      "11 review @min-[900px]:flex",
    ])
  })

  test("--check fails on the old spelling, --dry-run writes nothing, a write fixes it", () => {
    expectCommandLine("containers")
  })
})
