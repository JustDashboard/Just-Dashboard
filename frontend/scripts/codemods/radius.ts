/**
 * radius: a corner is a step of the radius ladder (FND-13).
 *
 *     2px xs (swatch) · 4px sm (mark) · 6px md (control) · 8px lg (fence, floating surface)
 *     12px xl (block) · 10px tile-md and 14px tile-lg (WireMark tiles)
 *
 * An arbitrary radius that is exactly a step becomes that step, side and corner forms included
 * (`rounded-t-[2px]` → `rounded-t-xs`). One that is near a step (1px and 3px swatches, a 0.6rem
 * mark) is reported with the step as the suggestion; the rest (a map node's 22px pill, 50%) are
 * reported for review. `rounded-[inherit]` is legitimate and stays.
 */
import { classTransform, type Rule, type Transform, withUtility } from "./core"

const STEPS: Record<number, string> = {
  2: "xs",
  4: "sm",
  6: "md",
  8: "lg",
  12: "xl",
  10: "tile-md",
  14: "tile-lg",
}
const NEAR: Record<number, string> = { 1: "xs", 3: "xs", 9.6: "tile-md" }

const ARBITRARY = /^rounded(-(?:[trblse]|[tb][lr]|s[se]|e[se]))?-\[(\d+(?:\.\d+)?)(px|rem)\]$/

const rule = (): Rule => (token) => {
  const u = token.utility
  if (u === "rounded-[inherit]") return undefined
  const m = ARBITRARY.exec(u)
  if (!m) {
    if (/^rounded(-[a-z]{1,2})?-\[/.test(u)) return { review: "an arbitrary radius off the ladder" }
    return undefined
  }
  const [, side = "", amount, unit] = m
  const px = Math.round(Number(amount) * (unit === "rem" ? 16 : 1) * 100) / 100
  const step = STEPS[px]
  if (step) return { to: withUtility(token, `rounded${side}-${step}`) }
  const near = NEAR[px]
  if (near)
    return {
      review: `${px}px is not a step; the nearest role is rounded-${near}`,
      suggest: withUtility(token, `rounded${side}-${near}`),
    }
  return { review: `${px}px is not a step of the radius ladder` }
}

export const radius: Transform = {
  name: "radius",
  summary:
    "arbitrary radii that are a step to the step: 2px swatch, 4px mark, tile rem values to rounded-tile-*",
  closes: ["FND-13"],
  requires: [
    {
      file: "src/app/globals.css",
      pattern: /--radius-xs\s*:[\s\S]*--radius-tile-md\s*:/,
      why: "the 2px swatch step and rounded-tile-* exist (WP-01)",
    },
  ],
  apply: (text, file, options) => classTransform("radius", rule)(text, file, options),
}
