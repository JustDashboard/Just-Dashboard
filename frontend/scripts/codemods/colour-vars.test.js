import { describe, expect, test } from "bun:test"
import { colourVars, PLANNED_COLOURS, registeredColours } from "./colour-vars.ts"
import { expectCommandLine, expectFixture, lines } from "./test-kit.js"

describe("colour-vars", () => {
  test("a registered colour reached through var() becomes its utility, opacity kept", () => {
    const findings = expectFixture(colourVars, { colours: new Set(PLANNED_COLOURS) })
    expect(lines(findings)).toEqual([
      "4 rewrite text-[var(--tag-pink)] → text-tag-pink",
      "5 rewrite text-(--git-added) → text-git-added",
      "5 rewrite hover:text-(--git-deleted) → hover:text-git-deleted",
      "6 rewrite border-l-(--pull-open) → border-l-pull-open",
      "6 rewrite bg-[var(--tag-blue)]/15 → bg-tag-blue/15",
      "7 rewrite fill-(--color-tag-red) → fill-tag-red",
      "7 rewrite text-[color:var(--chart-2)] → text-chart-2",
      "9 review text-(--git-colour)",
    ])
  })

  test("only what globals.css registers is rewritten", () => {
    const colours = registeredColours("@theme inline {\n  --color-tag-pink: var(--tag-pink);\n}")
    expect([...colours]).toEqual(["tag-pink"])
    const out = colourVars.apply('const a = "text-(--tag-pink) text-(--tag-red)"\n', "a.ts", {
      colours,
    })
    expect(out.text).toBe('const a = "text-tag-pink text-(--tag-red)"\n')
    expect(lines(out.findings, "review")).toEqual(["1 review text-(--tag-red)"])
  })

  test("--check fails on the old spelling, --dry-run writes nothing, a write fixes it", () => {
    expectCommandLine("colour-vars")
  })
})
