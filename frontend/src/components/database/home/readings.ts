import { compactCount } from "@/lib/format"
/** One part of a figure that is a sum. */
export type Part = {
  key: string
  label: string
  /** 0–1 of the whole figure. */
  share: number
  /** The part's own figure, for the pointer. */
  value: string
  color: string
}

/**
 * A count in the width of a tile: every digit up to ten thousand, "16.1M" past it.
 *
 * @deprecated use `compactCount(value, { exactBelow: 10_000 })` from lib/format.
 */
export function compact(value: number): string {
  return compactCount(value, { exactBelow: 10_000 })
}

/** A rate a second: one decimal while it is small enough to need it. */
export function perSecond(value: number): string {
  if (value <= 0) return "0"
  if (value < 10) return value.toFixed(1).replace(/\.0$/, "")
  return compact(value)
}

/** @deprecated use `ago` from lib/format, the one "how long ago" for an age held as a number. */
export { ago } from "@/lib/format"
