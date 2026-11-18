import { describe, expect, test } from "bun:test"
import { inkOpacity } from "./ink-opacity.ts"
import { expectCommandLine, expectFixture, fixture, lines } from "./test-kit.js"

describe("ink-opacity", () => {
  test("the legacy muted fades become text-faint, keeping their variants", () => {
    const findings = expectFixture(inkOpacity)
    expect(lines(findings, "rewrite")).toEqual([
      "4 rewrite text-muted-foreground/60 → text-faint",
      "10 rewrite text-muted-foreground/70 → text-faint",
      "11 rewrite text-muted-foreground/40 → text-faint",
      "12 rewrite placeholder:text-muted-foreground/60 → placeholder:text-faint",
      "13 rewrite hover:text-muted-foreground/45 → hover:text-faint",
    ])
  })

  test("a glyph's fade on an svg, an icon or an [&_svg] variant stays", () => {
    const after = fixture("ink-opacity", "after")
    expect(after).toContain('<ChevronRight className="size-3 text-muted-foreground/60" />')
    expect(after).toContain('<svg className="text-muted-foreground/40" />')
    expect(after).toContain('className="[&_svg]:text-muted-foreground/60"')
  })

  test("every other faded text ink is left for review with the nearest ink", () => {
    const findings = inkOpacity.apply(fixture("ink-opacity", "before"), "src/demo.tsx").findings
    expect(lines(findings, "review")).toEqual([
      "5 review text-muted-foreground/50 → text-faint",
      "17 review text-foreground/85 → text-foreground",
      "18 review text-foreground/75 → text-muted-foreground",
      "19 review text-muted-foreground/80 → text-faint",
      "20 review active:text-primary/80",
    ])
  })

  test("--suggested applies the suggestions and leaves the one without", () => {
    const out = inkOpacity.apply(fixture("ink-opacity", "before"), "src/demo.tsx", {
      suggested: true,
    })
    expect(out.text).toContain('const GLYPH = "size-3 shrink-0 text-faint"')
    expect(out.text).toContain('<span className="text-foreground">near</span>')
    expect(out.text).toContain('<span className="text-muted-foreground">less</span>')
    expect(out.text).toContain('<span className="text-faint">tag</span>')
    expect(out.text).toContain('className="active:text-primary/80"')
  })

  test("a template's interpolations are never cut into", () => {
    const text = "const a = `text-muted-foreground/60 ${x}text-muted-foreground/60 ${y}`\n"
    expect(inkOpacity.apply(text, "src/a.ts").text).toBe(
      "const a = `text-faint ${x}text-muted-foreground/60 ${y}`\n",
    )
  })

  test("--check fails on the old spelling, --dry-run writes nothing, a write fixes it", () => {
    expectCommandLine("ink-opacity")
  })
})
