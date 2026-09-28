/**
 * A restart's or an upgrade's transcript, read as the lines it is made of.
 *
 * The file is three voices interleaved: the dashboard's own sentences ("waiting
 * for the dashboard to answer at …"), the commands it ran (`$ docker compose …`),
 * and what those commands printed — mostly BuildKit's plain progress, which
 * numbers every step and repeats the number on each line the step writes, so
 * two images building at once arrive as one braid of `#12` and `#14`. Drawn as
 * one grey column that braid is unreadable; told apart, a step's header, its
 * output, its verdict and a container coming up are four shapes the eye can
 * follow down a thousand lines.
 *
 * Nothing here is a parser in the strict sense. Every rule is a prefix or a
 * shape the two runners and BuildKit print, and a line matching none of them
 * is output, which is always true of it.
 */

export type LineKind =
  /** A command the runner ran: `$ docker compose … up`. */
  | "command"
  /** A sentence of the dashboard's own, at the head or the foot of the run. */
  | "note"
  /** BuildKit opening a step: `#23 [frontend deps 4/4] RUN bun install`. */
  | "step"
  /** A step finishing: `#23 DONE 12.4s`. */
  | "done"
  /** A step answered from the cache. */
  | "cached"
  /** A line a step printed: `#23 0.412 installed next@16`. */
  | "output"
  /** A layer downloading or unpacking — progress, not output. */
  | "progress"
  /** Compose reporting a resource: ` Container jd-backend-1 Healthy`. */
  | "resource"
  | "error"
  | "warning"
  /** The server's marker that the start of the file was dropped. */
  | "trimmed"
  | "blank"

export type TranscriptLine = {
  /** 1-based, as the reader counts and as a copied range quotes them. */
  number: number
  text: string
  kind: LineKind
  /** The BuildKit step a line belongs to, for `step`, `done`, `output` and the rest. */
  step?: string
  /** What the step's header names in brackets: `frontend deps 4/4`. */
  target?: string
  /** The service a step or a resource is for — the first word of `target`. */
  service?: string
  /** A step's own clock for `output`, its total for `done`: `0.412`, `12.4s`. */
  time?: string
  /** For `resource`: what it is (`Container`) and what just happened to it. */
  resource?: string
  state?: string
}

export const TRIMMED_MARKER = "… earlier output trimmed …"

// The sentences the two runners write about themselves, as opposed to the
// output of the commands they run. Kept as prefixes so a reworded message
// degrades to plain output rather than to a wrong colour.
const NOTES = [
  /^(Rebuilding|Restarting|Applying|Updating) Just Dashboard\b/,
  /^New settings? .*requested by\b/,
  /^.* requested by \S+$/,
  /^stack: /,
  /^checkout: /,
  /^note: /,
  /^waiting for the dashboard to answer\b/,
  /^the dashboard is answering again\b/,
  /^The dashboard is running the new configuration\b/,
  /^The previous configuration is back\b/,
  /^Putting the previous configuration back\b/,
  /^Just Dashboard is now on\b/,
  /^Open https?:\/\//,
  /^Dashboard address: /,
  /^now at [0-9a-f]{7,}/,
  /^restored \S+ from \S+/,
]

const ERROR =
  /(^FAILED:|^! |\b(error|errors|fatal|panic|failed to|failed|unhealthy|exit code: [1-9])\b)/i
// "0 errors" and "--no-errors" are not failures, and a bun or Go build prints
// both on the way to succeeding.
const NOT_ERROR = /\b(0|no) (errors?|warnings?)\b|--no-\w*error/i
const WARNING = /\b(warn|warning|deprecated|retrying)\b/i

const STEP = /^#(\d+) \[([^\]]+)\] (.*)$/
const DONE = /^#(\d+) DONE (\d+(?:\.\d+)?s)$/
const CACHED = /^#(\d+) CACHED$/
const STEP_ERROR = /^#(\d+) ERROR\b/
const OUTPUT = /^#(\d+) (\d+\.\d+) (.*)$/
const PROGRESS =
  /^#(\d+) (?:sha256:|extracting |resolve |transferring |exporting |naming |unpacking |writing |preparing |\.\.\.)/
const ANY_STEP = /^#(\d+) (.*)$/
const RESOURCE = /^ ?(Container|Image|Network|Volume) (\S+) (.+?)\s*$/

/** The first word inside a step's brackets that names a service, if any. */
function serviceOf(target: string): string | undefined {
  const first = target.split(/\s+/)[0]
  return first && first !== "internal" ? first : undefined
}

/** A compose resource name's service: `just-dashboard-backend-1` is `backend`. */
function resourceService(name: string): string | undefined {
  const parts = name.split(":")[0].split(/[-_/]/).filter(Boolean)
  if (parts.length === 0) return undefined
  const last = parts[parts.length - 1]
  const tail = /^\d+$/.test(last) || last === "latest" ? parts.slice(0, -1) : parts
  return tail[tail.length - 1]
}

export function classify(text: string): Omit<TranscriptLine, "number" | "text"> {
  if (text.trim() === "") return { kind: "blank" }
  if (text === TRIMMED_MARKER) return { kind: "trimmed" }
  if (text.startsWith("$ ")) return { kind: "command" }
  if (NOTES.some((pattern) => pattern.test(text))) return { kind: "note" }

  let m = STEP_ERROR.exec(text)
  if (m) return { kind: "error", step: m[1] }
  m = STEP.exec(text)
  if (m) return { kind: "step", step: m[1], target: m[2], service: serviceOf(m[2]) }
  m = DONE.exec(text)
  if (m) return { kind: "done", step: m[1], time: m[2] }
  m = CACHED.exec(text)
  if (m) return { kind: "cached", step: m[1] }
  m = OUTPUT.exec(text)
  if (m) {
    const kind = tone(m[3]) ?? "output"
    return { kind, step: m[1], time: m[2] }
  }
  if (PROGRESS.test(text)) return { kind: "progress", step: ANY_STEP.exec(text)?.[1] }
  m = RESOURCE.exec(text)
  if (m) {
    return {
      kind: /error|unhealthy/i.test(m[3]) ? "error" : "resource",
      resource: m[1],
      state: m[3],
      service: resourceService(m[2]),
    }
  }
  m = ANY_STEP.exec(text)
  if (m) return { kind: tone(m[2]) ?? "output", step: m[1] }
  return { kind: tone(text) ?? "output" }
}

function tone(text: string): "error" | "warning" | undefined {
  if (NOT_ERROR.test(text)) return undefined
  if (ERROR.test(text)) return "error"
  if (WARNING.test(text)) return "warning"
  return undefined
}

/**
 * One line, numbered and classified. `note` is for a caller that knows the
 * line is the runner's own voice — the deploy engine's status stream says
 * "Resolved main to 3f2c1a9" — where the shapes above would only see output.
 * A blank line stays blank whoever wrote it.
 */
export function transcriptLine(number: number, text: string, note = false): TranscriptLine {
  const shape = note && text.trim() !== "" ? { kind: "note" as const } : classify(text)
  return { number, text, ...shape }
}

/** The transcript as numbered, classified lines. Terminal escapes are presentation. */
export function transcriptLines(text: string): TranscriptLine[] {
  const clean = text.replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, "").replace(/\r\n?/g, "\n")
  const raw = clean.replace(/\n+$/, "").split("\n")
  if (raw.length === 1 && raw[0] === "") return []
  // A run of blank lines is one pause in the story, not three rows of nothing.
  const lines: TranscriptLine[] = []
  for (const line of raw) {
    if (line.trim() === "" && lines.at(-1)?.kind === "blank") continue
    lines.push(transcriptLine(lines.length + 1, line))
  }
  return lines
}

/**
 * Where a search finds `needle` in a line, as `[start, end)` ranges, without
 * overlaps and ignoring case: the shape the log console's `LogText` marks
 * hits by, so a transcript line drawn through it is marked the same way. The
 * needle is expected lowercased, as the consoles keep it.
 */
export function hitRanges(text: string, needle: string): [number, number][] {
  if (!needle) return []
  const lower = text.toLowerCase()
  const out: [number, number][] = []
  for (let at = lower.indexOf(needle); at >= 0; at = lower.indexOf(needle, at + needle.length)) {
    out.push([at, at + needle.length])
  }
  return out
}

export function isTrimmed(text: string | undefined): boolean {
  return Boolean(text?.startsWith(`${TRIMMED_MARKER}\n`))
}

/**
 * The whole transcript read once, extended by the tail the report keeps
 * polling.
 *
 * The file only grows during a run, so the newest tail is the end of the whole
 * one plus whatever was written since. Its first line is found in the whole
 * copy, searched from the end — a build repeats itself, and the latest
 * occurrence is the one the tail starts at — and everything after it is new.
 * When the tail no longer overlaps (a new run truncated the file) the tail is
 * the answer and the caller asks for the whole file again.
 */
export function extendTranscript(
  full: string,
  tail: string,
): { text: string; overlapped: boolean } {
  // An untrimmed tail is the whole file already.
  if (!isTrimmed(tail)) return { text: tail, overlapped: true }
  const fresh = tail.slice(TRIMMED_MARKER.length + 1)
  // Anchored on half a kilobyte rather than one line, so a repeated
  // "#5 DONE 0.1s" cannot splice the tail onto the wrong copy of itself.
  const anchor = fresh.slice(0, 512)
  const at = full.lastIndexOf(anchor)
  if (at < 0) return { text: tail, overlapped: false }
  return { text: full.slice(0, at) + fresh, overlapped: true }
}

/** Where a transcript is in its run: the commands it has reached, in order. */
export function commandsIn(lines: TranscriptLine[]): string[] {
  return lines.filter((line) => line.kind === "command").map((line) => line.text.slice(2))
}
