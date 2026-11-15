import { describe, expect, test } from "bun:test"
import { mediaQueries, readQuery } from "./media-queries.ts"
import { expectCommandLine, expectFixture, lines } from "./test-kit.js"

describe("media-queries", () => {
  test("a breakpoint's query becomes the hook by name, and the import is added", () => {
    const findings = expectFixture(mediaQueries)
    expect(lines(findings, "rewrite")).toEqual([
      '8 rewrite useMediaQuery("(min-width: 1024px)") → useBreakpoint("lg")',
      '9 rewrite useMediaQuery("(max-width: 639px)") → useBelow("sm")',
      "16 rewrite max-[1023px]:hidden → max-lg:hidden",
      "16 rewrite min-[1280px]:grid → xl:grid",
    ])
  })

  test("widths off the breakpoints, constants and matchMedia are reported", () => {
    const findings = expectFixture(mediaQueries)
    expect(lines(findings, "review")).toEqual([
      '5 review "(min-width: 1600px)"',
      '10 review useMediaQuery("(min-width: 1100px)")',
      "11 review useMediaQuery(WIDE)",
      '13 review window.matchMedia("(min-width: 1280px)")',
      "16 review min-[1400px]:flex",
    ])
  })

  test("useMediaQuery leaves the import once nothing uses it", () => {
    const text = [
      'import { useIsMobile, useMediaQuery } from "@/hooks/use-mobile"',
      'import { useBreakpoint } from "@/lib/breakpoints"',
      "",
      'export const a = () => [useMediaQuery("(width >= 40rem)"), useBreakpoint("xl"), useIsMobile()]',
      "",
    ].join("\n")
    expect(mediaQueries.apply(text, "a.ts").text).toBe(
      [
        'import { useIsMobile } from "@/hooks/use-mobile"',
        'import { useBreakpoint } from "@/lib/breakpoints"',
        "",
        'export const a = () => [useBreakpoint("sm"), useBreakpoint("xl"), useIsMobile()]',
        "",
      ].join("\n"),
    )
  })

  test("the last name of an import takes the import with it", () => {
    const text =
      'import { useMediaQuery } from "@/hooks/use-mobile"\n\nexport const a = () => useMediaQuery("(max-width: 767px)")\n'
    expect(mediaQueries.apply(text, "a.ts").text).toBe(
      'import { useBelow } from "@/lib/breakpoints"\n\nexport const a = () => useBelow("md")\n',
    )
  })

  test("readQuery knows the spellings of one width", () => {
    expect(readQuery("(min-width: 64rem)")).toEqual({ hook: "useBreakpoint", name: "lg" })
    expect(readQuery("(width < 1280px)")).toEqual({ hook: "useBelow", name: "xl" })
    expect(readQuery("(max-width: 479px)")).toEqual({ off: 479 })
    expect(readQuery("(prefers-reduced-motion: reduce)")).toBeUndefined()
  })

  test("--check fails on the old spelling, --dry-run writes nothing, a write fixes it", () => {
    expectCommandLine("media-queries")
  })
})
