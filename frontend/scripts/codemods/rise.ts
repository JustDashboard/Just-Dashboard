/**
 * rise: a page arrives through Page's `rise` prop, not a class at the call site (FND-11).
 *
 *     <Page className="animate-rise">          → <Page rise>
 *     <Page className="animate-rise px-4">     → <Page rise className="px-4">
 *
 * An `animate-rise` under a condition (`cn(open && "animate-rise")`) is reported: the prop would
 * make it unconditional. Other elements that rise are not pages and stay as they are.
 */
import {
  applyEdits,
  attributeOf,
  type Edit,
  parse,
  type Rule,
  rewriteClasses,
  type Transform,
} from "./core"

const rule = (): Rule => (_token, raw, literal) => {
  if (literal.tag !== "Page" || raw !== "animate-rise") return undefined
  if (literal.conditional) return { review: "animate-rise under a condition; the rise prop is not" }
  return { to: "" }
}

export const rise: Transform = {
  name: "rise",
  summary: 'className="animate-rise" on Page to the rise prop',
  closes: ["FND-11"],
  requires: [
    {
      file: "src/components/page.tsx",
      pattern: /\brise\??\s*:/,
      why: "Page takes a rise prop (WP-09)",
    },
  ],
  apply(text, file, options) {
    const sf = parse(text, file)
    const { edits, findings, changed } = rewriteClasses(sf, file, "rise", rule(), options)
    const all: Edit[] = [...edits]
    const done = new Set<number>()
    for (const literal of changed) {
      const element = literal.element!
      if (done.has(element.pos) || attributeOf(element, "rise")) continue
      done.add(element.pos)
      const at = element.tagName.getEnd()
      all.push({ start: at, end: at, text: " rise" })
    }
    return { text: applyEdits(text, all), findings }
  },
}
