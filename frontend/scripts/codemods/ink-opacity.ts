/**
 * ink-opacity: text is never faded with opacity (FND-07).
 *
 * `text-muted-foreground/40` to `/70` already draw as `--faint` through an override in
 * globals.css, everywhere but on an svg; this writes the ink they draw. On an svg (an `svg`
 * element, an icon component, or an `[&_svg]:` variant) the opacity is a glyph's fade, which is
 * allowed, so those stay.
 *
 * The other fades on a text ink (`text-foreground/85`, `text-muted-foreground/80`,
 * `text-primary/80` …) have no single answer: each is reported with the nearest ink as a
 * suggestion, for a person to accept or change.
 */
import ts from "typescript"
import { applyEdits, parse, rewriteClasses, type Rule, type Transform, withUtility } from "./core"

const LEGACY = /^text-muted-foreground\/(40|45|50|60|70)$/
const FADE = /^text-(foreground|muted-foreground|primary|faint)\/(\[[^\]]+\]|\d+)$/
const ICON_MODULE = /(^|\/)icons$|lucide|heroicons|@mdi\//

function iconNames(sf: ts.SourceFile) {
  const names = new Set<string>()
  for (const statement of sf.statements) {
    if (!ts.isImportDeclaration(statement) || !ts.isStringLiteral(statement.moduleSpecifier))
      continue
    if (!ICON_MODULE.test(statement.moduleSpecifier.text)) continue
    const bindings = statement.importClause?.namedBindings
    if (bindings && ts.isNamedImports(bindings))
      for (const e of bindings.elements) names.add(e.name.text)
  }
  return names
}

function suggestion(ink: string, amount: string): string | undefined {
  const n = Number(amount)
  if (ink === "foreground") return n >= 85 ? "text-foreground" : "text-muted-foreground"
  if (ink === "muted-foreground") return n >= 90 ? "text-muted-foreground" : "text-faint"
  if (ink === "faint") return "text-faint"
  return undefined
}

const rule =
  (icons: Set<string>): Rule =>
  (token, raw, literal, words) => {
    const onSvg =
      literal.tag === "svg" ||
      (literal.tag !== undefined && (/Icon$/.test(literal.tag) || icons.has(literal.tag))) ||
      token.variants.some((v) => /svg/.test(v))
    const legacy = LEGACY.test(token.utility)
    const fade = FADE.exec(token.utility)
    if (!legacy && !fade) return undefined
    if (onSvg) return undefined
    if (legacy) {
      // A class list outside JSX that sizes something is likely an icon's; say so rather than guess.
      if (!literal.element && words.some((w) => /^size-/.test(w)))
        return {
          review: "a glyph's fade or a word's? On an svg it may stay; on text it is text-faint",
          suggest: withUtility(token, "text-faint"),
        }
      return { to: withUtility(token, "text-faint") }
    }
    const [, ink, amount] = fade!
    const to = suggestion(ink, amount.replace(/[[\]]/g, ""))
    return {
      review: `text faded to ${amount}%: pick foreground, muted or faint`,
      suggest: to ? withUtility(token, to) : undefined,
    }
  }

export const inkOpacity: Transform = {
  name: "ink-opacity",
  summary: "text-muted-foreground/40-70 to text-faint; other faded text inks reported",
  closes: ["FND-07"],
  apply(text, file, options) {
    const sf = parse(text, file)
    const { edits, findings } = rewriteClasses(
      sf,
      file,
      "ink-opacity",
      rule(iconNames(sf)),
      options,
    )
    return { text: applyEdits(text, edits), findings }
  },
}
