/** "+1:04", "+1:02:09": how far into the run a line was written. */
export function elapsed(ts: string, from: number): string {
  const seconds = Math.max(0, Math.round((new Date(ts).getTime() - from) / 1000))
  if (Number.isNaN(seconds)) return ""
  const h = Math.floor(seconds / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  const s = String(seconds % 60).padStart(2, "0")
  return h > 0 ? `+${h}:${String(m).padStart(2, "0")}:${s}` : `+${m}:${s}`
}

/**
 * The time column's width in `ch`, from the longest stamp the transcript
 * holds. A fixed width fits "+1:04" but not "+1:02:09", and a stamp wider than
 * its cell prints over the line beside it. Never narrower than "+00:00", so a
 * short run's column does not jump as it passes ten minutes.
 */
export function stampColumnCh(stamps: Iterable<string>): number {
  let widest = "+00:00".length
  for (const stamp of stamps) widest = Math.max(widest, stamp.length)
  return widest
}
