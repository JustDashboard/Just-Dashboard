/**
 * z-layers: stacking is a named layer, never a raw number (FND-16).
 *
 *     z-10 raised · z-20 sticky · z-30 pane · z-40 float · z-50 by context
 *
 * z-50 was every top layer at once. It becomes `z-immersive` when its class list is switched on
 * by an `immersive` (or full-screen) condition, `z-tooltip` in a tooltip's file, and `z-overlay`
 * otherwise: dialogs, sheets, menus, lightboxes, drag shields and confetti all belong over the
 * page. Arbitrary and negative values (`z-[1]`, `-z-10`) are reported. `z-0` and `z-auto` stay.
 */
import ts from "typescript"
import { classTransform, type ClassLiteral, type Rule, type Transform, withUtility } from "./core"

const LAYERS: Record<string, string> = {
  "10": "z-raised",
  "20": "z-sticky",
  "30": "z-pane",
  "40": "z-float",
}

const IMMERSIVE = /immersive|full-?screen/i

function immersive(literal: ClassLiteral) {
  for (let at: ts.Node | undefined = literal.node.parent; at; at = at.parent) {
    if (
      ts.isBinaryExpression(at) &&
      at.operatorToken.kind === ts.SyntaxKind.AmpersandAmpersandToken
    )
      if (IMMERSIVE.test(at.left.getText())) return true
    if (ts.isConditionalExpression(at) && IMMERSIVE.test(at.condition.getText())) return true
    if (ts.isJsxAttribute(at) || ts.isFunctionLike(at) || ts.isVariableDeclaration(at)) break
  }
  return false
}

const rule =
  (file: string): Rule =>
  (token, _raw, literal) => {
    const u = token.utility
    const plain = /^z-(\d+)$/.exec(u)
    if (plain) {
      const layer = LAYERS[plain[1]]
      if (layer) return { to: withUtility(token, layer) }
      if (plain[1] === "0") return undefined
      if (plain[1] === "50") {
        if (immersive(literal)) return { to: withUtility(token, "z-immersive") }
        if (/tooltip/i.test(file)) return { to: withUtility(token, "z-tooltip") }
        return { to: withUtility(token, "z-overlay") }
      }
      return { review: `z-${plain[1]} is not a layer` }
    }
    if (/^-z-\d+$/.test(u))
      return { review: "a negative z-index: is isolate on the parent enough?" }
    if (/^z-\[[^\]]+\]$/.test(u)) return { review: "an arbitrary z-index: which layer is it?" }
    return undefined
  }

export const zLayers: Transform = {
  name: "z-layers",
  summary:
    "z-10/20/30/40/50 to z-raised, z-sticky, z-pane, z-float, z-immersive, z-overlay, z-tooltip",
  closes: ["FND-16"],
  requires: [
    {
      file: "src/app/globals.css",
      pattern: /--z-index-raised\s*:|@utility z-raised/,
      why: "the named z layers exist (WP-01)",
    },
  ],
  apply: (text, file, options) => classTransform("z-layers", rule)(text, file, options),
}
