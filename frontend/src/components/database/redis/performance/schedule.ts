import { duration } from "@/lib/format"

/** `save 3600 1 300 100` as the sentences it means. */
export function scheduleWords(schedule: string): string[] {
  const numbers = schedule.trim().split(/\s+/).map(Number)
  const out: string[] = []
  for (let i = 0; i + 1 < numbers.length; i += 2) {
    if (!Number.isFinite(numbers[i]) || !Number.isFinite(numbers[i + 1])) return [schedule]
    out.push(
      `after ${duration(numbers[i])} if ${numbers[i + 1].toLocaleString()} ${numbers[i + 1] === 1 ? "key" : "keys"} changed`,
    )
  }
  return out
}
