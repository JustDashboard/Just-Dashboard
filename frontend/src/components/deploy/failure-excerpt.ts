import type { ConsoleRow } from "./transcript-buffer"

/**
 * The lines of a failed step's output that say why it failed, for the run
 * page's failure and the failed step's record in Details.
 *
 * Both showed the step's last lines, and the last lines of a failed Docker
 * build are BuildKit's recap of it, not the failure: the Dockerfile around
 * the instruction that failed (the generated one, a screen of `--mount`
 * flags a line), then `ERROR: failed to build`, then the engine's own
 * sentence repeating the reason above. The program's error — a `TypeError`,
 * a `Type error:`, `npm ERR!` — was a dozen lines further up, where only the
 * console reached it.
 *
 * In order of what is known:
 * 1. the line the server's diagnosis proved (`lineSeq`), with the line before
 *    it, which for a compiler is often where;
 * 2. in a BuildKit build, the output of the instruction that failed (the one
 *    `#N ERROR` names), from its first line that reads as an error, else its
 *    last lines;
 * 3. otherwise, the step's own output from the last line that reads as an
 *    error, else its last lines.
 *
 * A stack trace is cut to its first frames: the frame that threw is the one
 * read, and the runtime's own frames under it fill the excerpt with nothing.
 */
export type FailureExcerpt = {
  rows: ConsoleRow[]
  /** The row the excerpt is about, held out from the rest where it is drawn. */
  marked?: string
}

/** Frames of a stack trace kept under the error that threw. */
const FRAMES = 2

/**
 * A line that opens an error, read after BuildKit's `#N 1.234 ` prefix: a
 * thrown error's name, a compiler's or a package manager's error prefix, a
 * test runner's or a framework's failure headline, a shell's missing command.
 */
const ERROR_START = [
  /^[A-Z]\w*(?:Error|Exception)\b/,
  /^(?:error|fatal|panic|traceback)\b/i,
  /^(?:npm |yarn |pnpm )?ERR!/,
  /^ERR_[A-Z_]+/,
  /^E[A-Z]{3,}\b:/,
  /^FAILED\b/,
  /^Failed to (?:compile|load|resolve)\b/,
  /^Type error\b/,
  /^Build error\b/,
  /^Module not found\b/,
  /^Cannot find (?:module|package)\b/,
  /^✖ /,
  /^× /,
  /: (?:error|fatal error)(?: TS\d+)?:/,
  /\berror\[E\d+\]/,
  /: (?:command )?not found$/,
]

/** BuildKit's prefix on a line an instruction printed: `#17 2.167 `. */
const STEP_PREFIX = /^#\d+ (?:\d+\.\d+ ?)?/

/** BuildKit naming the instruction that failed: `#17 ERROR: process …`. */
const STEP_ERROR = /^#(\d+) ERROR\b/

/** A stack frame: `    at Module._compile (node:internal/…)`, `  File "app.py", line 3`. */
const FRAME = /^\s+(?:at |File ")/

/** What a line says once BuildKit's prefix is off it. */
function said(row: ConsoleRow) {
  return row.line.text.replace(STEP_PREFIX, "")
}

function readsAsError(row: ConsoleRow) {
  const text = said(row).trimStart()
  return ERROR_START.some((pattern) => pattern.test(text))
}

/** A row with nothing to read: a blank line, or BuildKit's prefix with nothing after it. */
function empty(row: ConsoleRow) {
  return row.line.kind === "blank" || said(row).trim() === ""
}

/** `size` rows from `from`, with no more than `FRAMES` frames of any stack trace. */
function window(rows: ConsoleRow[], from: number, size: number) {
  const out: ConsoleRow[] = []
  let frames = 0
  for (let at = from; at < rows.length && out.length < size; at++) {
    const row = rows[at]
    if (FRAME.test(said(row))) {
      frames += 1
      if (frames > FRAMES) continue
    } else frames = 0
    out.push(row)
  }
  return out
}

export function failureExcerpt(
  rows: ConsoleRow[],
  stepId: number,
  lineSeq?: number,
  size = 6,
): FailureExcerpt {
  const own = rows.filter((row) => row.stepId === stepId && !empty(row))
  if (own.length === 0) return { rows: [] }

  // 1. The line the diagnosis proved, and the one before it.
  if (lineSeq) {
    const at = own.findIndex((row) => row.seq === lineSeq)
    if (at >= 0) {
      const from = Math.max(0, at - 1)
      return { rows: window(own, from, size), marked: own[at].id }
    }
  }

  // 2. The output of the BuildKit instruction that failed.
  let vertex: string | undefined
  for (let at = own.length - 1; at >= 0 && vertex === undefined; at--) {
    vertex = STEP_ERROR.exec(own[at].line.text)?.[1]
  }
  if (vertex !== undefined) {
    const printed = own.filter(
      (row) =>
        row.line.step === vertex &&
        (row.line.kind === "output" || row.line.kind === "warning" || row.line.kind === "error") &&
        !STEP_ERROR.test(row.line.text),
    )
    if (printed.length > 0) {
      const first = printed.findIndex(readsAsError)
      if (first >= 0) return { rows: window(printed, first, size), marked: printed[first].id }
      return { rows: printed.slice(-size) }
    }
  }

  // 3. The step's own output: the engine's closing sentences repeat the
  // reason the page already says, so its last line that is not one of them.
  const output = own.filter((row) => row.line.kind !== "note")
  const lines = output.length > 0 ? output : own
  for (let at = lines.length - 1; at >= Math.max(0, lines.length - 40); at--) {
    if (!readsAsError(lines[at])) continue
    // The first line of the error this one belongs to: a run of error lines
    // is one error, read from its top.
    let top = at
    while (top > 0 && readsAsError(lines[top - 1])) top -= 1
    return { rows: window(lines, top, size), marked: lines[top].id }
  }
  return { rows: lines.slice(-size) }
}
