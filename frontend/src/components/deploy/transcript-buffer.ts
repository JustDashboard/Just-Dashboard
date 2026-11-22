import { transcriptLine, type TranscriptLine } from "@/lib/transcript"

/**
 * One `step.log` event as the stream delivers it: a chunk, not a line, from
 * the step that wrote it, on the stream it was written to — `status` for the
 * engine's own sentences, `stdout`/`stderr` for what the step ran.
 */
export type BuildLogEvent = {
  seq: number
  stepId: number
  ts: string
  stream: string
  text: string
  truncated: boolean
}

/** A line of the console: the transcript's shape, plus where and when it was written. */
export type ConsoleRow = {
  id: string
  /** The persisted event the line came from. */
  seq: number
  stepId: number
  ts: string
  truncated: boolean
  line: TranscriptLine
}

/** The transcript's counts: its lines, its errors and each step's lines. */
export type TranscriptCounts = {
  lines: number
  errors: number
  perStep: Map<number, number>
}

/**
 * A run's transcript as the console holds it while the stream writes on: the
 * events, the lines read from them, and their counts, each kept up to date by
 * what arrives rather than read again from the start.
 *
 * A chatty build lands sixty-four lines ten times a second. Read from scratch
 * on every batch — sorted, de-duplicated, every line classified again — the
 * whole transcript was the page's work for each one, and three thousand lines
 * in the console stopped keeping up at all. Events that follow the last one
 * are read alone and added; anything else (a replayed sequence, one out of
 * order) rebuilds the whole, and `epoch` moves so a reader can tell lines
 * appended from lines replaced.
 *
 * Line numbers are the transcript's own: when the oldest events go past the
 * cap, the lines after them keep their numbers, so line 812 stays line 812.
 */
export type TranscriptBuffer = TranscriptCounts & {
  events: BuildLogEvent[]
  rows: ConsoleRow[]
  /** Moves whenever `rows` was rebuilt rather than appended to. */
  epoch: number
  /** The number the next line takes. */
  next: number
}

export const EMPTY_TRANSCRIPT: TranscriptBuffer = {
  events: [],
  rows: [],
  lines: 0,
  errors: 0,
  perStep: new Map(),
  epoch: 0,
  next: 1,
}

/** The buffer emptied, as a resync does: what follows is a new transcript. */
export function clearTranscript(buffer: TranscriptBuffer): TranscriptBuffer {
  return { ...EMPTY_TRANSCRIPT, perStep: new Map(), epoch: buffer.epoch + 1 }
}

/** `buffer` with `incoming` added, holding at most `cap` events, the newest. */
export function appendTranscript(
  buffer: TranscriptBuffer,
  incoming: BuildLogEvent[],
  cap = Infinity,
): TranscriptBuffer {
  if (incoming.length === 0) return buffer
  const fresh = ascending(incoming)
  const last = buffer.events.at(-1)?.seq ?? -Infinity
  let next: TranscriptBuffer
  if (fresh[0].seq > last) {
    const rows: ConsoleRow[] = []
    let number = buffer.next
    for (const event of fresh) number = readEvent(event, number, rows)
    const counts = counted(rows, buffer)
    next = {
      ...counts,
      events: buffer.events.concat(fresh),
      rows: buffer.rows.concat(rows),
      epoch: buffer.epoch,
      next: number,
    }
  } else {
    // A sequence already held, or one before the end: the whole is read again
    // in order, the later copy of a sequence winning.
    const bySequence = new Map<number, BuildLogEvent>()
    for (const event of buffer.events) bySequence.set(event.seq, event)
    for (const event of fresh) bySequence.set(event.seq, event)
    const events = [...bySequence.values()].sort((a, b) => a.seq - b.seq)
    const rows: ConsoleRow[] = []
    let number = 1
    for (const event of events) number = readEvent(event, number, rows)
    next = {
      ...counted(rows),
      events,
      rows,
      epoch: buffer.epoch + 1,
      next: number,
    }
  }
  return next.events.length > cap ? trimmed(next, next.events.length - cap) : next
}

/** Every line of `events`, numbered from one: a transcript read whole. */
export function consoleRows(events: BuildLogEvent[]): ConsoleRow[] {
  return appendTranscript(EMPTY_TRANSCRIPT, events).rows
}

/**
 * Persisted events are chunks, not lines. Each displayed line keeps its
 * event's identity and gets a stable number, including when one chunk holds
 * several lines, and is read into the shapes `lib/transcript.ts` knows —
 * except that a line on the `status` stream is the engine's own voice
 * ("Resolved main to a12bc34") whatever it looks like, which only the stream
 * can say. Returns the number the next line takes.
 */
function readEvent(event: BuildLogEvent, number: number, into: ConsoleRow[]) {
  // Terminal escape sequences are presentation, never markup from the build.
  const clean = event.text.replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, "").replace(/\r\n?/g, "\n")
  const parts = clean.replace(/\n$/, "").split("\n")
  parts.forEach((text, index) => {
    into.push({
      id: `${event.seq}:${index}`,
      seq: event.seq,
      stepId: event.stepId,
      ts: event.ts,
      // The engine marks the chunk; the note goes after its last line.
      truncated: event.truncated && index === parts.length - 1,
      line: transcriptLine(number, text, event.stream === "status"),
    })
    number += 1
  })
  return number
}

/** The counts after `rows` are added to `base`'s; a new map, so a reader sees it change. */
function counted(rows: ConsoleRow[], base?: TranscriptCounts): TranscriptCounts {
  const perStep = new Map(base?.perStep)
  let lines = base?.lines ?? 0
  let errors = base?.errors ?? 0
  for (const row of rows) {
    lines += 1
    if (row.line.kind === "error") errors += 1
    perStep.set(row.stepId, (perStep.get(row.stepId) ?? 0) + 1)
  }
  return { lines, errors, perStep }
}

/** The buffer without its `drop` oldest events and their lines. */
function trimmed(buffer: TranscriptBuffer, drop: number): TranscriptBuffer {
  const through = buffer.events[drop - 1].seq
  let gone = 0
  while (gone < buffer.rows.length && buffer.rows[gone].seq <= through) gone += 1
  const perStep = new Map(buffer.perStep)
  let errors = buffer.errors
  for (let index = 0; index < gone; index++) {
    const row = buffer.rows[index]
    if (row.line.kind === "error") errors -= 1
    const left = (perStep.get(row.stepId) ?? 0) - 1
    if (left > 0) perStep.set(row.stepId, left)
    else perStep.delete(row.stepId)
  }
  return {
    ...buffer,
    events: buffer.events.slice(drop),
    rows: buffer.rows.slice(gone),
    lines: buffer.lines - gone,
    errors,
    perStep,
  }
}

/** The batch in sequence order, each sequence once (the later copy winning). */
function ascending(events: BuildLogEvent[]) {
  let sorted = true
  for (let index = 1; index < events.length; index++) {
    if (events[index].seq <= events[index - 1].seq) {
      sorted = false
      break
    }
  }
  if (sorted) return events
  const bySequence = new Map<number, BuildLogEvent>()
  for (const event of events) bySequence.set(event.seq, event)
  return [...bySequence.values()].sort((a, b) => a.seq - b.seq)
}
