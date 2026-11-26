import { describe, expect, test } from "bun:test"
import { readFileSync } from "node:fs"
import { join } from "node:path"

const css = readFileSync(join(import.meta.dir, "globals.css"), "utf8")

/** The body of `@utility <name> { ... }`, braces balanced. */
function utility(name) {
  const start = css.indexOf(`@utility ${name} {`)
  expect(start).toBeGreaterThanOrEqual(0)
  let depth = 0
  for (let i = css.indexOf("{", start); i < css.length; i++) {
    if (css[i] === "{") depth++
    if (css[i] === "}" && --depth === 0) return css.slice(start, i + 1)
  }
  throw new Error(`${name} never closes`)
}

// Forced colours paint a transparent outline in the text's colour, so a ring
// that rests as a transparent line was drawn on every control at once.
describe("the shared focus ring under forced colours", () => {
  for (const [name, focused] of [
    ["focus-ring", ":focus-visible"],
    ["focus-ring-inset", ":focus-visible"],
    ["focus-ring-within", ":has(:focus-visible)"],
  ]) {
    test(`${name} is absent at rest there, and the transparent line elsewhere`, () => {
      const body = utility(name)
      expect(body).toContain("outline: 2px solid transparent;")
      expect(body).toMatch(
        new RegExp(
          `@media \\(forced-colors: active\\) \\{\\s*&:not\\(${focused.replace(/[()]/g, "\\$&")}\\) \\{\\s*outline-style: none;`,
        ),
      )
    })
  }
})
