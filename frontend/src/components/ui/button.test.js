import { expect, test } from "bun:test"
import { buttonVariants, hitAreaClasses } from "./button"
import { cn } from "@/lib/utils"

/**
 * The control-size table in the foundations, as `buttonVariants` writes it:
 * every size has a coarse-pointer step, the two small densities carry the hit
 * area, and the icon size follows the density.
 */
const SIZES = ["default", "xs", "sm", "lg", "icon", "icon-xs", "icon-sm", "icon-lg"]
const rem = (n) => `${n / 16}rem`
const TABLE = {
  default: [36, 44],
  xs: [24, 32],
  sm: [32, 40],
  lg: [40, 48],
  icon: [36, 44],
  "icon-xs": [24, 32],
  "icon-sm": [32, 40],
  "icon-lg": [40, 48],
}

test("every size has a fine height and a coarse-pointer step from the table", () => {
  for (const size of SIZES) {
    const classes = buttonVariants({ size }).split(" ")
    const [fine, coarse] = TABLE[size]
    expect(classes).toContain(`[--control-h:${rem(fine)}]`)
    expect(classes).toContain(`pointer-coarse:[--control-h:${rem(coarse)}]`)
  }
})

test("dense and compact sizes carry the 44px hit area, regular and large do not need it", () => {
  for (const size of ["xs", "sm", "icon-xs", "icon-sm"]) {
    expect(buttonVariants({ size })).toContain(hitAreaClasses)
  }
  for (const size of ["default", "lg", "icon", "icon-lg"]) {
    expect(buttonVariants({ size })).not.toContain(hitAreaClasses)
  }
  expect(hitAreaClasses).toBe("hit-area")
})

test("icons follow the density: 12 dense, 14 compact, 16 regular", () => {
  const svg = (size) =>
    cn(buttonVariants({ size }))
      .split(" ")
      .filter((c) => c.startsWith("[&_svg:not([class*='size-'])]:"))
  expect(svg("sm")).toEqual(["[&_svg:not([class*='size-'])]:size-3.5"])
  expect(svg("icon-sm")).toEqual(["[&_svg:not([class*='size-'])]:size-3.5"])
  expect(svg("xs")).toEqual(["[&_svg:not([class*='size-'])]:size-3"])
  expect(svg("icon-xs")).toEqual(["[&_svg:not([class*='size-'])]:size-3"])
  expect(svg("default")).toEqual(["[&_svg:not([class*='size-'])]:size-4"])
})

test("the label is the body size, and the dense size keeps text-xs", () => {
  expect(cn(buttonVariants({ size: "default" }))).toContain("text-body")
  expect(cn(buttonVariants({ size: "sm" }))).toContain("text-body")
  expect(cn(buttonVariants({ size: "xs" }))).toContain("text-xs")
  expect(cn(buttonVariants({ size: "xs" }))).not.toContain("text-body")
})

test("a caller's height replaces the one height class, so the coarse step cannot outgrow it", () => {
  // An inline text button: the custom property stays, but nothing reads it.
  const inline = cn(buttonVariants({ variant: "link", size: "xs" }), "h-auto p-0").split(" ")
  expect(inline).toContain("h-auto")
  expect(inline).not.toContain("h-(--control-h)")
  const square = cn(buttonVariants({ size: "icon-sm" }), "size-7").split(" ")
  expect(square).toContain("size-7")
  expect(square).not.toContain("size-(--control-h)")
})

test("no face hand-mixes an opacity step or keeps a dark: pair", () => {
  for (const variant of ["default", "destructive", "outline", "secondary", "ghost", "link"]) {
    const classes = buttonVariants({ variant })
    expect(classes).not.toMatch(/(?:bg|text)-(?:destructive|secondary|primary)\/\d+/)
    expect(classes).not.toContain("dark:")
    expect(classes).not.toContain("text-white")
  }
})
