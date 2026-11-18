import { describe, expect, test } from "bun:test"
import { rise } from "./rise.ts"
import { expectCommandLine, expectFixture, lines } from "./test-kit.js"

describe("rise", () => {
  test("animate-rise on a Page becomes the rise prop", () => {
    const findings = expectFixture(rise)
    expect(lines(findings)).toEqual([
      "7 rewrite animate-rise → ",
      "8 rewrite animate-rise → ",
      "11 rewrite animate-rise → ",
      "12 rewrite animate-rise → ",
      "13 review animate-rise",
    ])
  })

  test("a Page that already has the prop does not get it twice", () => {
    const text = 'export const a = <Page rise className="animate-rise gap-0" />\n'
    expect(rise.apply(text, "a.tsx").text).toBe(
      'export const a = <Page rise className="gap-0" />\n',
    )
  })

  test("--check fails on the old spelling, --dry-run writes nothing, a write fixes it", () => {
    expectCommandLine("rise")
  })
})
