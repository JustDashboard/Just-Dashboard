import type { LogLine } from "@/lib/types"

/** Opaque row identities survive a fresh response without persisting log text. */
export function logPlaceIdentities(lines: LogLine[]) {
  const seen = new Map<string, number>()
  return lines.map((line) => {
    const text = JSON.stringify([
      line.timestamp,
      line.source,
      line.file,
      line.no,
      line.stream,
      line.text,
    ])
    let left = 2166136261
    let right = 5381
    for (let i = 0; i < text.length; i++) {
      left = Math.imul(left ^ text.charCodeAt(i), 16777619)
      right = Math.imul(right, 33) ^ text.charCodeAt(i)
    }
    const digest = `${(left >>> 0).toString(16)}-${(right >>> 0).toString(16)}`
    const occurrence = seen.get(digest) ?? 0
    seen.set(digest, occurrence + 1)
    return `${digest}-${occurrence}`
  })
}
