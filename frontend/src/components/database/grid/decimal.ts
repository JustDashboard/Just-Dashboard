/**
 * Exact decimal arithmetic on the strings the API sends.
 *
 * A `bigint` or a `numeric(38,10)` arrives as its digits because a JavaScript
 * number cannot hold it. Summing a selected column through `Number` would then
 * print a total that is wrong in its last digits, in the one place a reader
 * came to check a figure — so the status line adds, compares and divides on
 * scaled `BigInt`s and never leaves base ten.
 */

export interface Decimal {
  /** The digits with the point removed, signed. */
  int: bigint
  /** How many of those digits are after the point. */
  scale: number
}

const PLAIN = /^[+-]?(?:\d+\.?\d*|\.\d+)$/

const ZERO = BigInt(0)
const TEN = BigInt(10)

function pow10(n: number): bigint {
  return TEN ** BigInt(n)
}

/** Reads a plain decimal literal. Exponents, NaN and Infinity are not decimals: null. */
export function parseDecimal(input: string | number): Decimal | null {
  const text = typeof input === "number" ? plainNumber(input) : input.trim()
  if (text === null || !PLAIN.test(text)) return null
  const negative = text.startsWith("-")
  const unsigned = text.replace(/^[+-]/, "")
  const [whole, fraction = ""] = unsigned.split(".")
  const int = BigInt((whole || "0") + fraction)
  return { int: negative ? -int : int, scale: fraction.length }
}

/** A float as plain digits, or null where it has none (NaN, Infinity, 1e+30). */
function plainNumber(value: number): string | null {
  if (!Number.isFinite(value)) return null
  const text = String(value)
  return /e/i.test(text) ? null : text
}

function align(a: Decimal, b: Decimal): [bigint, bigint, number] {
  const scale = Math.max(a.scale, b.scale)
  return [a.int * pow10(scale - a.scale), b.int * pow10(scale - b.scale), scale]
}

export function addDecimal(a: Decimal, b: Decimal): Decimal {
  const [x, y, scale] = align(a, b)
  return { int: x + y, scale }
}

export function compareDecimal(a: Decimal, b: Decimal): number {
  const [x, y] = align(a, b)
  return x < y ? -1 : x > y ? 1 : 0
}

/**
 * `a / n`, rounded half away from zero to `scale` places. The divisor is a
 * count of cells, so it is always a positive whole number.
 */
export function divideDecimal(a: Decimal, n: number, scale: number): Decimal {
  const divisor = BigInt(n)
  const target = Math.max(scale, a.scale)
  const numerator = a.int * pow10(target - a.scale + 1)
  let quotient = numerator / divisor
  // One guard digit was carried so the last kept place can be rounded.
  const guard = quotient % TEN
  quotient = quotient / TEN
  if (guard >= BigInt(5)) quotient += BigInt(1)
  else if (guard <= BigInt(-5)) quotient -= BigInt(1)
  return { int: quotient, scale: target }
}

/** Back to text, with trailing zeros kept only up to `minScale`. */
export function formatDecimal(value: Decimal, minScale = 0): string {
  const negative = value.int < ZERO
  let int = negative ? -value.int : value.int
  let scale = value.scale
  while (scale > minScale && int % TEN === ZERO) {
    int /= TEN
    scale--
  }
  let digits = int.toString()
  if (scale > 0) {
    digits = digits.padStart(scale + 1, "0")
    digits = `${digits.slice(0, -scale)}.${digits.slice(-scale)}`
  }
  return negative && /[1-9]/.test(digits) ? `-${digits}` : digits
}

/** Thousands separators for the whole part; the fraction is left exactly as it is. */
export function groupDigits(text: string): string {
  const match = /^(-?)(\d+)(\.\d+)?$/.exec(text)
  if (!match) return text
  return `${match[1]}${match[2].replace(/\B(?=(\d{3})+(?!\d))/g, ",")}${match[3] ?? ""}`
}

/** Whether two numeric spellings are the same number: `4193.40` and `4193.4`, `+7` and `7`. */
export function sameNumber(a: string | number, b: string | number): boolean {
  const x = parseDecimal(a)
  const y = parseDecimal(b)
  if (x && y) return compareDecimal(x, y) === 0
  // Exponent forms and the float specials: equal only when they read as the same float.
  const p = Number(a)
  const q = Number(b)
  if (Number.isNaN(p) || Number.isNaN(q)) return String(a).trim() === String(b).trim()
  return p === q
}

export interface Aggregate {
  /** Cells that held a number. */
  count: number
  sum: string
  avg: string
  min: string
  max: string
  /** False when a float with no exact digits took part, and the figures are approximate. */
  exact: boolean
}

/**
 * Count, sum, average, minimum and maximum of the numbers in a selection.
 * Nulls and anything that is not a number are skipped, not counted as zero.
 */
export function aggregate(values: Iterable<string | number | null | undefined>): Aggregate | null {
  let count = 0
  let sum: Decimal = { int: ZERO, scale: 0 }
  let min: Decimal | null = null
  let max: Decimal | null = null
  // The fallback for floats written with an exponent.
  let approximate = 0
  let approxCount = 0
  let approxMin = Infinity
  let approxMax = -Infinity

  for (const value of values) {
    if (value === null || value === undefined || value === "") continue
    const decimal = parseDecimal(value)
    if (decimal) {
      count++
      sum = addDecimal(sum, decimal)
      if (!min || compareDecimal(decimal, min) < 0) min = decimal
      if (!max || compareDecimal(decimal, max) > 0) max = decimal
      continue
    }
    const float = Number(value)
    if (typeof value === "string" && value.trim() === "") continue
    if (!Number.isFinite(float)) continue
    approxCount++
    approximate += float
    approxMin = Math.min(approxMin, float)
    approxMax = Math.max(approxMax, float)
  }

  if (count + approxCount === 0) return null
  if (approxCount === 0 && min && max) {
    return {
      count,
      sum: formatDecimal(sum),
      avg: formatDecimal(divideDecimal(sum, count, sum.scale + 4)),
      min: formatDecimal(min),
      max: formatDecimal(max),
      exact: true,
    }
  }
  const total = approximate + Number(formatDecimal(sum))
  const all = count + approxCount
  return {
    count: all,
    sum: String(total),
    avg: String(total / all),
    min: String(min ? Math.min(approxMin, Number(formatDecimal(min))) : approxMin),
    max: String(max ? Math.max(approxMax, Number(formatDecimal(max))) : approxMax),
    exact: false,
  }
}
