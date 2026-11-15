import { describe, expect, test } from "bun:test"
import { readFileSync } from "node:fs"
import { join } from "node:path"
import { cn } from "./utils"

const css = readFileSync(join(import.meta.dir, "..", "app", "globals.css"), "utf8")

/** Every name a theme namespace adds, read from globals.css rather than listed twice. */
function themeNames(namespace) {
  const re = new RegExp(`--${namespace}-([a-z0-9-]+?):`, "g")
  return [...new Set([...css.matchAll(re)].map((m) => m[1]))].filter(
    (name) => !name.includes("--"),
  )
}

describe("cn knows the theme's own names", () => {
  test("a type step is a size, so a colour beside it does not drop it", () => {
    expect(cn("text-hint text-muted-foreground")).toBe("text-hint text-muted-foreground")
    expect(cn("text-xs", "text-hint")).toBe("text-hint")
    expect(cn("text-micro", "text-title")).toBe("text-title")
  })

  test("a touch step beside the body size is a second size, not a conflict", () => {
    expect(cn("text-body pointer-coarse:text-base")).toBe("text-body pointer-coarse:text-base")
    expect(cn("text-body max-sm:text-base", "text-xs")).toBe("max-sm:text-base text-xs")
  })

  test("each named layer overrides a raw one and the layer before it", () => {
    const layers = themeNames("z-index")
    expect(layers).toEqual(["raised", "sticky", "pane", "float", "immersive", "overlay", "tooltip"])
    for (const layer of layers) expect(cn("z-10", `z-${layer}`)).toBe(`z-${layer}`)
    expect(cn("z-overlay", "z-tooltip")).toBe("z-tooltip")
  })

  test("the swatch and tile radii are radii", () => {
    expect(cn("rounded-md", "rounded-xs")).toBe("rounded-xs")
    for (const name of themeNames("radius").filter((n) => n.startsWith("tile-"))) {
      expect(cn("rounded-lg", `rounded-${name}`)).toBe(`rounded-${name}`)
      expect(cn(`rounded-${name}`, "rounded-xl")).toBe("rounded-xl")
    }
    expect(themeNames("radius")).toContain("tile-md")
  })

  test("motion lengths and curves are durations and easings", () => {
    const lengths = themeNames("transition-duration")
    expect(lengths).toEqual(["fast", "base", "slow", "fill"])
    for (const name of lengths) expect(cn("duration-200", `duration-${name}`)).toBe(`duration-${name}`)
    expect(cn("ease-out", "ease-arrive")).toBe("ease-arrive")
    expect(cn("ease-arrive", "ease-standard")).toBe("ease-standard")
    expect(cn("transition-colors", "transition-fill")).toBe("transition-fill")
  })

  test("tracking, layout values, control heights and the safe area", () => {
    expect(cn("tracking-wide", "tracking-code")).toBe("tracking-code")
    expect(cn("grid-cols-2", "grid-cols-main-end")).toBe("grid-cols-main-end")
    expect(cn("grid-cols-start-main", "grid-cols-start-main-end")).toBe("grid-cols-start-main-end")
    expect(cn("grid-rows-2", "grid-rows-fill")).toBe("grid-rows-fill")
    expect(cn("w-72", "w-rail")).toBe("w-rail")
    expect(cn("w-rail", "w-inspector")).toBe("w-inspector")
    expect(cn("h-[min(75vh,40rem)]", "h-workbench")).toBe("h-workbench")
    expect(cn("max-h-96", "max-h-rail")).toBe("max-h-rail")
    expect(cn("h-9", "h-control-regular")).toBe("h-control-regular")
    expect(cn("size-8", "size-control-compact")).toBe("size-control-compact")
    expect(cn("pb-2", "pb-safe")).toBe("pb-safe")
  })
})

/*
 * The inks against the grounds they are set on, computed from the tokens in
 * globals.css: a new ink or a retuned ground is checked against every one of
 * them here, before any page draws it.
 */
const root = css.slice(css.indexOf(":root {"), css.indexOf("\n}", css.indexOf(":root {")))
const tokens = Object.fromEntries(
  [...root.matchAll(/--([a-z0-9-]+):\s*([^;]+);/g)].map((m) => [m[1], m[2].trim()]),
)

/** A colour as OKLab, from an `oklch()`, a `#hex`, a `var()` or a `color-mix(in oklab …)`. */
function oklab(value) {
  value = value.trim()
  const ref = value.match(/^var\(--([a-z0-9-]+)\)$/)
  if (ref) return oklab(tokens[ref[1]])
  const lch = value.match(/^oklch\(([0-9.]+) ([0-9.]+) ([0-9.]+)\)$/)
  if (lch) {
    const [L, C, h] = lch.slice(1).map(Number)
    const rad = (h * Math.PI) / 180
    return [L, C * Math.cos(rad), C * Math.sin(rad)]
  }
  if (value === "#000") return [0, 0, 0]
  const mix = value.match(/^color-mix\(in oklab, (.+) ([0-9.]+)%, (.+?)(?: ([0-9.]+)%)?\)$/)
  if (mix && mix[3] !== "transparent") {
    const p = Number(mix[2]) / 100
    const a = oklab(mix[1])
    const b = oklab(mix[3])
    return a.map((x, i) => x * p + b[i] * (1 - p))
  }
  throw new Error(`cannot read the colour ${value}`)
}

function luminance([L, a, b]) {
  const l = (L + 0.3963377774 * a + 0.2158037573 * b) ** 3
  const m = (L - 0.1055613458 * a - 0.0638541728 * b) ** 3
  const s = (L - 0.0894841775 * a - 1.291485548 * b) ** 3
  const rgb = [
    4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
    -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
    -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s,
  ].map((c) => Math.min(1, Math.max(0, c)))
  return 0.2126 * rgb[0] + 0.7152 * rgb[1] + 0.0722 * rgb[2]
}

/** WCAG contrast between two tokens. */
function contrast(ink, ground) {
  const [x, y] = [luminance(oklab(`var(--${ink})`)), luminance(oklab(`var(--${ground})`))]
  return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05)
}

describe("contrast, from the tokens", () => {
  // The selection ground is in the list: a selected rail row or ChoiceCard is
  // where the faint ink and the red of a failure fell to 4.0 and 3.9 (A11Y-01).
  const grounds = ["background", "card", "popover", "muted", "accent"]

  test("the faint ink, the red and the muted ink keep 4.5:1 on every text ground", () => {
    const low = []
    for (const ink of ["faint", "destructive", "muted-foreground"]) {
      for (const ground of grounds) {
        const ratio = contrast(ink, ground)
        if (ratio < 4.5) low.push(`${ink} on ${ground}: ${ratio.toFixed(2)}`)
      }
    }
    expect(low).toEqual([])
    expect(contrast("faint", "accent")).toBeGreaterThanOrEqual(4.5)
    expect(contrast("destructive", "accent")).toBeGreaterThanOrEqual(4.5)
    expect(contrast("faint", "background")).toBeGreaterThanOrEqual(4.5)
    expect(contrast("destructive", "background")).toBeGreaterThanOrEqual(4.5)
  })

  test("the selection ground is still a step off the card it selects on", () => {
    expect(contrast("accent", "card")).toBeGreaterThan(1.05)
  })

  // WCAG 1.4.11: the edge that says "this is a field" holds 3:1 against the
  // page and against the grounds a field sits on, including the control face
  // a checkbox is filled with. The hairline beside it is decoration and is not.
  test("a control's edge holds 3:1 on the page and on every field ground", () => {
    const ratios = Object.fromEntries(
      ["background", "card", "popover", "control"].map((g) => [g, contrast("control-edge", g)]),
    )
    for (const [ground, ratio] of Object.entries(ratios)) {
      expect(`${ground} ${ratio >= 3}`).toBe(`${ground} true`)
    }
    expect(contrast("border", "background")).toBeLessThan(3)
  })

  // The received half of every in/out pair: off the success hue, and a
  // graphic that still holds 3:1 on the page and on a card.
  test("--chart-5 is a cyan apart from success, and reads on the page", () => {
    const hue = (name) => Number(tokens[name].match(/oklch\([0-9.]+ [0-9.]+ ([0-9.]+)\)/)[1])
    expect(Math.abs(hue("chart-5") - hue("success"))).toBeGreaterThan(25)
    expect(hue("chart-5")).toBeGreaterThanOrEqual(190)
    expect(hue("chart-5")).toBeLessThanOrEqual(210)
    expect(contrast("chart-5", "background")).toBeGreaterThanOrEqual(3)
    expect(contrast("chart-5", "card")).toBeGreaterThanOrEqual(3)
  })
})
