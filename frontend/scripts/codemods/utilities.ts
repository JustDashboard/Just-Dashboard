/**
 * utilities: the recurring layout values get their names (FND-23, foundations §12).
 *
 *     grid-cols-[minmax(0,1fr)_auto]        → grid-cols-main-end
 *     grid-cols-[auto_minmax(0,1fr)]        → grid-cols-start-main
 *     grid-cols-[auto_minmax(0,1fr)_auto]   → grid-cols-start-main-end
 *     grid-rows-[minmax(0,1fr)]             → grid-rows-fill
 *     w-[18rem] → w-rail · w-[22rem] → w-inspector
 *     h-[min(75svh,40rem)] → h-workbench · max-h-[calc(100svh-13rem)] → max-h-rail
 *     align-[-2px] / [-1px] with inline     → glyph-inline
 *     font-mono text-[0.9em] / [0.92em] / [0.9375em] → code-inline
 *
 * `glyph-inline` is `display: inline` plus the alignment, so it replaces `align-[…]` and an
 * `inline` beside it; next to another display (`inline-flex`, `inline-block`) it is reported.
 * `code-inline` is the mono face at 0.9em, so it replaces the size and the `font-mono` beside it;
 * a size without the mono face is reported. An inspector column of 20rem or 24rem in a grid
 * template is reported with the inspector's 22rem as the suggestion; the template keeps the
 * literal width, since `--width-inspector` is an inline theme value with no runtime variable. A
 * template that appears once stays arbitrary.
 */
import { classTransform, type Rule, type Transform, withUtility } from "./core"

/** Arbitrary values written with or without the underscore after a comma are the same value. */
const tight = (value: string) => value.replace(/,_/g, ",")

const NAMED: Record<string, string> = {
  "grid-cols-[minmax(0,1fr)_auto]": "grid-cols-main-end",
  "grid-cols-[auto_minmax(0,1fr)]": "grid-cols-start-main",
  "grid-cols-[auto_minmax(0,1fr)_auto]": "grid-cols-start-main-end",
  "grid-rows-[minmax(0,1fr)]": "grid-rows-fill",
  "w-[18rem]": "w-rail",
  "w-[22rem]": "w-inspector",
  "h-[min(75svh,40rem)]": "h-workbench",
  "h-[min(75vh,40rem)]": "h-workbench",
  "max-h-[calc(100svh-13rem)]": "max-h-rail",
  "max-h-[calc(100vh-13rem)]": "max-h-rail",
}

const GLYPH = /^align-\[-(1px|2px|0\.125em)\]$/
const CODE = /^text-\[(0\.9em|0\.92em|0\.9375em)\]$/
const DISPLAY = /^(block|inline-block|inline-flex|inline-grid|flex|grid|contents|hidden|table)$/
const INSPECTOR = /^grid-cols-\[minmax\(0,1fr\)_(20|22|24)rem\]$/

const rule = (): Rule => (token, _raw, _literal, words) => {
  const u = tight(token.utility)
  const named = NAMED[u]
  if (named) return { to: withUtility(token, named) }
  const plain = (w: string) => !w.includes(":")
  if (GLYPH.test(u)) {
    const display = words.filter(plain).filter((w) => DISPLAY.test(w))
    if (display.length)
      return { review: `glyph-inline is display: inline, and this is ${display.join(" ")}` }
    const inline = words.filter((w) => w === "inline")
    return { to: withUtility(token, "glyph-inline"), drop: inline }
  }
  if (CODE.test(u)) {
    const mono = words.filter((w) => w === "font-mono")
    if (!mono.length)
      return {
        review: "code-inline is the mono face at 0.9em; this size has no font-mono",
        suggest: withUtility(token, "code-inline"),
      }
    return { to: withUtility(token, "code-inline"), drop: mono }
  }
  const inspector = INSPECTOR.exec(u)
  if (inspector && inspector[1] !== "22")
    return {
      review: `an inspector column of ${inspector[1]}rem; the inspector is 22rem`,
      suggest: withUtility(token, "grid-cols-[minmax(0,1fr)_22rem]"),
    }
  return undefined
}

export const utilities: Transform = {
  name: "utilities",
  summary:
    "recurring grid templates, rail and inspector widths, h-workbench, glyph-inline, code-inline",
  closes: ["FND-23"],
  requires: [
    {
      file: "src/app/globals.css",
      pattern: /--grid-template-columns-main-end\s*:[\s\S]*@utility glyph-inline/,
      why: "the named layout values exist (WP-01)",
    },
  ],
  apply: (text, file, options) => classTransform("utilities", rule)(text, file, options),
}
