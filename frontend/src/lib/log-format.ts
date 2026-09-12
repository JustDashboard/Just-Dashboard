import type { LogLine } from "@/lib/types"

/** The field names structured loggers use for the one part a human reads. */
const MESSAGE_KEYS = ["msg", "message", "@message", "event"]
const LEVEL_KEYS = ["level", "lvl", "severity", "@level"]
/** Timestamps are already in their own column, so they are dropped, not shown twice. */
const TIME_KEYS = ["time", "ts", "timestamp", "@timestamp"]

export type StructuredLine = {
  message: string
  level?: string
  /** Everything else, in the order the producer wrote it. */
  fields: [string, unknown][]
}

/**
 * A structured log line, read as the sentence it is.
 *
 * Backends that log JSON produce lines like
 * `{"time":"...","level":"INFO","msg":"listening","addr":":8080"}`. Printed
 * verbatim, the part a reader wants — "listening" — is four fields into a
 * blob, and the timestamp is repeated next to the one the viewer already
 * renders. Pulling out the message and demoting the rest is the difference
 * between logs you scan and logs you parse by eye.
 *
 * Returns null for anything that is not a JSON object carrying a message:
 * plain container output is the common case and must not be second-guessed,
 * and a JSON object with no message field has no sentence to lift out — with
 * one, reordering the rest would be rearranging somebody's data for no gain.
 */
export function parseStructured(text: string): StructuredLine | null {
  const trimmed = text.trim()
  if (!trimmed.startsWith("{") || !trimmed.endsWith("}")) return null

  let doc: unknown
  try {
    doc = JSON.parse(trimmed)
  } catch {
    return null
  }
  if (doc === null || typeof doc !== "object" || Array.isArray(doc)) return null

  const record = doc as Record<string, unknown>
  const messageKey = MESSAGE_KEYS.find((key) => typeof record[key] === "string")
  if (!messageKey) return null

  const levelKey = LEVEL_KEYS.find((key) => typeof record[key] === "string")

  return {
    message: record[messageKey] as string,
    level: levelKey ? (record[levelKey] as string).toLowerCase() : undefined,
    fields: Object.entries(record).filter(
      ([key]) => key !== messageKey && key !== levelKey && !TIME_KEYS.includes(key),
    ),
  }
}

/**
 * Parsing is cached against the line object rather than memoised over the
 * array: lines are appended, existing objects keep the same reference every
 * render, and re-parsing five thousand of them on every new line is how a log
 * pane starts dropping frames.
 */
const cache = new WeakMap<LogLine, StructuredLine | null>()

export function structuredOf(line: LogLine): StructuredLine | null {
  const cached = cache.get(line)
  if (cached !== undefined) return cached
  const parsed = parseStructured(line.text)
  cache.set(line, parsed)
  return parsed
}

/** A field value as one line of text, so an object does not break the row. */
export function fieldValue(value: unknown): string {
  return typeof value === "string" ? value : JSON.stringify(value)
}
