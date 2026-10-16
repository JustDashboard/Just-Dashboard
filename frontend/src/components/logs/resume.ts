import type { LogLine } from "@/lib/types"

/**
 * Where a reconnect resumes: the newest instant already on screen and the
 * texts of the window the server is about to replay. The server opens every
 * connection with the last few hundred lines, so a tunnel that dropped for a
 * second used to append those hundreds again under themselves.
 */
export type Resume = { stamp: number; texts: Set<string> }

export function resumeFrom(lines: LogLine[], window: number): Resume | null {
  if (lines.length === 0) return null
  let stamp = -Infinity
  for (let i = lines.length - 1; i >= 0; i--) {
    const at = lines[i].timestamp ? Date.parse(lines[i].timestamp!) : NaN
    if (!Number.isNaN(at)) {
      stamp = at
      break
    }
  }
  return { stamp, texts: new Set(lines.slice(-window).map((l) => l.text)) }
}

/**
 * The lines of a replayed batch that are new. Once one line is new, every
 * line after it is too: the replay is contiguous, and a line that merely
 * repeats an old text after that point is a line that was written again.
 */
export function unseen(batch: LogLine[], resume: Resume): { lines: LogLine[]; caughtUp: boolean } {
  for (let i = 0; i < batch.length; i++) {
    const line = batch[i]
    const at = line.timestamp ? Date.parse(line.timestamp) : NaN
    const stamped = !Number.isNaN(at)
    const old = stamped
      ? at < resume.stamp || (at === resume.stamp && resume.texts.has(line.text))
      : resume.texts.has(line.text)
    if (!old) return { lines: batch.slice(i), caughtUp: true }
  }
  return { lines: [], caughtUp: false }
}
