import { clsx, type ClassValue } from "clsx"
import { extendTailwindMerge } from "tailwind-merge"

/**
 * `cn`, taught the theme's own type scale.
 *
 * This is not a nicety. tailwind-merge decides which of two classes wins by
 * looking each one up in a table of class *groups*, and it builds that table
 * from stock Tailwind. `--text-micro`, `--text-hint`, `--text-body` and
 * `--text-title` are named in `@theme` and exist only in this product — so `text-micro` did not match the font-size group, fell
 * through to the generic `text-{colour}` matcher, and was treated as a colour.
 *
 * The consequence was silent and everywhere: any element whose classes carried
 * both a size and a colour lost the size, because the colour came later and
 * "won" a conflict the two never had. `Tag` is
 * `text-micro … text-muted-foreground`, so every one of the 117 tags in the
 * product rendered at the browser's default 16px instead of 10 — which is the
 * whole reason they read as oversized. The same went for the wordmark's
 * `LogoMark`, the per-core readouts, and every other call site that put a size
 * and a colour in one `cn()`.
 *
 * Registering the four names is the entire fix. The classes were always
 * correct; the merger was throwing them away.
 *
 * The same holds for every other name the theme adds (globals.css): a layer
 * (`z-raised`), a radius (`rounded-xs`, `rounded-tile-md`), a length or curve
 * of motion (`duration-fast`, `ease-arrive`), a tracking (`tracking-code`), a
 * control height, a named layout value and the safe-area padding. Each is put
 * in the group whose stock members it conflicts with, so `cn("z-10",
 * "z-overlay")` keeps the overlay and `cn("rounded-md", "rounded-tile-md")`
 * keeps the tile, as a caller's override should.
 */
const CONTROL = ["control-dense", "control-compact", "control-regular", "control-large"]

const twMerge = extendTailwindMerge({
  extend: {
    theme: {
      radius: ["tile-md", "tile-lg"],
      ease: ["arrive", "standard"],
      tracking: ["code"],
    },
    classGroups: {
      "font-size": [{ text: ["micro", "hint", "body", "title"] }],
      z: [{ z: ["raised", "sticky", "pane", "float", "immersive", "overlay", "tooltip"] }],
      duration: [{ duration: ["fast", "base", "slow", "fill"] }],
      transition: [{ transition: ["fill"] }],
      "grid-cols": [{ "grid-cols": ["main-end", "start-main", "start-main-end"] }],
      "grid-rows": [{ "grid-rows": ["fill"] }],
      w: [{ w: ["rail", "inspector"] }],
      h: [{ h: ["workbench", ...CONTROL] }],
      "min-h": [{ "min-h": CONTROL }],
      "max-h": [{ "max-h": ["rail", "workbench"] }],
      size: [{ size: CONTROL }],
      pt: [{ pt: ["safe"] }],
      pb: [{ pb: ["safe"] }],
      pl: [{ pl: ["safe"] }],
      pr: [{ pr: ["safe"] }],
      px: [{ px: ["safe"] }],
      mb: [{ mb: ["safe"] }],
    },
  },
})

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

/**
 * Padding for a scrolling container that holds focusable controls.
 *
 * A focus ring is drawn *outside* the control's border box — a 2px outline at a
 * 2px offset in this design system — and any container whose overflow is not
 * `visible` clips it. Setting one axis to `auto` makes the other `auto` too, so an
 * `overflow-y-auto` list clips the ring on all four sides rather than only the
 * two it scrolls on. The symptom is a highlight that looks bitten off along
 * whichever edge the control is nearest, which in a dense form is most of them.
 *
 * Four pixels clears the outline, its offset and the border it sits outside.
 * Scroll containers with focusable children should carry this instead of a
 * one-sided `pr-1`, which only ever gave the scrollbar room.
 */
export const ringSafeScroll = "p-1"
