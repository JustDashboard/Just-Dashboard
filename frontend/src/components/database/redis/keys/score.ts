import type { RedisScore } from "@/components/database/redis/types"

/**
 * A sorted-set score as typed: a number, or one of the two infinities Redis
 * allows and JSON cannot carry. Anything else — nothing, a word, NaN — is
 * `null`, and is not sent.
 */
export function parseScore(text: string): RedisScore | null {
  const typed = text.trim().toLowerCase()
  if (typed === "inf" || typed === "+inf" || typed === "infinity" || typed === "+infinity") {
    return "inf"
  }
  if (typed === "-inf" || typed === "-infinity") return "-inf"
  if (!/^[+-]?(\d+\.?\d*|\.\d+)(e[+-]?\d+)?$/.test(typed)) return null
  const value = Number(typed)
  return Number.isFinite(value) ? value : null
}

/** A score as it is written. */
export function scoreText(score: RedisScore | undefined): string {
  return score === undefined ? "" : String(score)
}
