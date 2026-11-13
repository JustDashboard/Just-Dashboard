/**
 * How long things took, in the words a person asks it in. The operator found
 * `p50 / p95 / p99` unclear ("notation asks the reader to learn it"), and a
 * figure called "p95" on one page and "19 in 20" on the next is one number
 * with two names. Every rendered label, legend and column head takes its words
 * from here; the term itself stays behind a tooltip (`latencyTerm`) for the
 * reader who looks for it.
 */
export type LatencyMark = "p50" | "p90" | "p95" | "p99"

/** The share of the whole a mark stands for: a figure's name. */
export const LATENCY_SHARE: Record<LatencyMark, string> = {
  p50: "Half",
  p90: "9 in 10",
  p95: "19 in 20",
  p99: "99 in 100",
}

const TERM: Record<LatencyMark, string> = {
  p50: "the median, p50",
  p90: "the 90th percentile, p90",
  p95: "the 95th percentile, p95",
  p99: "the 99th percentile, p99",
}

/**
 * A column head, a legend entry or a field's label, where no sentence stands
 * around the figure: "19 in 20 within".
 */
export function latencyHead(mark: LatencyMark) {
  return `${LATENCY_SHARE[mark]} within`
}

/** The reading in a sentence: "19 in 20 inside 180ms", "half inside 24ms". */
export function latencyWords(mark: LatencyMark, figure: string) {
  return `${LATENCY_SHARE[mark].toLowerCase()} inside ${figure}`
}

/**
 * What the words mean and the term they replace, for a `title`: "19 in 20
 * finished within this (the 95th percentile, p95)".
 */
export function latencyTerm(mark: LatencyMark, what = "finished") {
  return `${LATENCY_SHARE[mark]} ${what} within this (${TERM[mark]})`
}
