/**
 * control-heights: a control's touch height comes from its size, not from its call site
 * (FND-08, MOB-06).
 *
 * Once `Button` and `Input` carry a `pointer-coarse:` step (WP-02), the phone overrides written
 * at call sites are noise. The rule this transform keeps is that a desktop height never moves:
 *
 *     <Button className="h-11 sm:h-9">    the pair goes, when the Button is the default size (36)
 *     <Button className="max-sm:h-8">     goes; under 640px the size's own coarse step applies
 *     <Button className="max-sm:h-10">    goes, likewise
 *     <Input className="h-8 sm:h-7">      becomes h-7: 28 at the desk as before, 44 under a finger
 *
 * A `h-11 sm:h-9` on a Button of another size would change its desktop height if dropped, so it
 * is reported, as is the pair split across two strings or under a condition.
 */
import {
  attributeOf,
  classTransform,
  type Rule,
  stringValue,
  type Transform,
  type Verdict,
} from "./core"

const DEFAULT_SIZE = new Set([undefined, "default"])

function pair(element: Parameters<typeof attributeOf>[0]): Verdict {
  const sizeAttribute = attributeOf(element, "size")
  const size = stringValue(sizeAttribute)
  if (sizeAttribute && size === undefined)
    return { review: "h-11 sm:h-9 on a Button whose size is an expression" }
  if (!DEFAULT_SIZE.has(size))
    return {
      review: `h-11 sm:h-9 on a size="${size}" Button: dropping it moves its desktop height`,
    }
  return { to: "" }
}

const rule = (): Rule => (token, raw, literal, words) => {
  const tag = literal.tag
  if (tag !== "Button" && tag !== "Input") return undefined
  const element = literal.element!
  if (tag === "Button") {
    if (raw === "max-sm:h-8" || raw === "max-sm:h-10") {
      // A height of the caller's own beats the size's touch step at every pointer, so without
      // this override the phone would get the desk height.
      const fixed = words.find((w) => /^(h|size)-[\d[]/.test(w))
      if (fixed)
        return { review: `${raw} beside ${fixed}: the fixed height wins under a finger too` }
      return { to: "" }
    }
    if (raw !== "h-11" && raw !== "sm:h-9") return undefined
    const partner = raw === "h-11" ? "sm:h-9" : "h-11"
    if (!words.includes(partner)) return undefined
    const verdict = pair(element)
    // A pair left for review is reported once, on its h-11.
    if ("review" in verdict && raw === "sm:h-9") return undefined
    if (literal.conditional)
      return raw === "h-11" ? { review: "h-11 sm:h-9 under a condition" } : undefined
    return verdict
  }
  // Input
  if (raw === "h-8" && words.includes("sm:h-7")) {
    if (literal.conditional) return { review: "h-8 sm:h-7 under a condition" }
    return { to: "h-7", drop: ["sm:h-7"] }
  }
  return undefined
}

export const controlHeights: Transform = {
  name: "control-heights",
  summary: "drop h-11 sm:h-9, max-sm:h-8 and max-sm:h-10 on Button; h-8 sm:h-7 to h-7 on Input",
  closes: ["FND-08", "MOB-06"],
  requires: [
    {
      file: "src/components/ui/button.tsx",
      pattern: /pointer-coarse:/,
      why: "Button carries its own touch height (WP-02)",
    },
    {
      file: "src/components/ui/input.tsx",
      pattern: /pointer-coarse:h-11/,
      why: "Input carries its own touch height (WP-02)",
    },
  ],
  apply: (text, file, options) => classTransform("control-heights", rule)(text, file, options),
}
