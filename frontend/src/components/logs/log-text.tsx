"use client"

import { cn } from "@/lib/utils"
import { LANES, hueFor } from "@/lib/hue"
import type { LogLevel } from "@/lib/log-filter"
import { CLASS_TEXT } from "@/lib/requests"
import {
  leadingTime,
  pieces,
  statusClass,
  tokenize,
  type Span,
  type TokenKind,
} from "@/lib/log-tokens"
import type { LogLine } from "@/lib/types"

/**
 * A log line, drawn by its shapes (`lib/log-tokens.ts`).
 *
 * The colours are one map in one file, and they follow the product's rules
 * rather than a code editor's: the **status hues** go only to what is a
 * reading of state — a level, an HTTP status by its class, a word that says
 * something failed or succeeded — and everything else takes the `--tag-*`
 * hues, which sit at one lightness so no kind of token outshouts another.
 * What the eye should skip goes muted: the line's own timestamp, the host,
 * the pid, the punctuation, the keys. The message stays in the foreground,
 * because it is the part that was written to be read.
 *
 * A line never carries more than a handful of hues. The point is that a
 * failed password, the address it came from and a 502 are found without
 * reading, not that every word has a colour.
 */
const KIND: Record<TokenKind, string> = {
  time: "text-muted-foreground/60",
  host: "text-muted-foreground/50",
  proc: "font-medium",
  pid: "text-muted-foreground/50",
  level: "",
  key: "text-muted-foreground",
  string: "text-[var(--tag-slate)]",
  number: "text-[var(--tag-pink)]",
  ip: "text-[var(--tag-violet)]",
  url: "text-[var(--tag-cyan)]",
  path: "text-[var(--tag-cyan)]",
  method: "font-semibold text-[var(--tag-blue)]",
  status: "",
  id: "text-muted-foreground/70",
  bad: "font-medium text-destructive",
  good: "text-success",
  punct: "text-muted-foreground/50",
}

/** A level as a word on the line, and in the console's level column. */
export const LEVEL_WORD: Record<LogLevel, string> = {
  critical: "font-semibold text-destructive",
  error: "font-semibold text-destructive",
  warn: "font-semibold text-warning",
  info: "text-[var(--tag-blue)]",
  debug: "text-muted-foreground",
  unknown: "text-muted-foreground",
}

/**
 * A token's class, for a surface that draws the same shapes outside a log
 * line — a request's path and query, a client's address — so they read the
 * same as they do inside one. A level and a status depend on the token's
 * value rather than its kind, and take theirs from `LEVEL_WORD` and
 * `CLASS_TEXT`.
 */
export function tokenClass(kind: TokenKind): string {
  return KIND[kind]
}

/**
 * A status inside a line, in the request log's colours (`lib/requests.ts`),
 * so the host's console and a deployment's request console cannot drift
 * apart. Bold here, because inside a sentence the code has no column of its
 * own to set it apart.
 */
const STATUS: Record<ReturnType<typeof statusClass>, string> = {
  ok: `font-semibold ${CLASS_TEXT["2xx"]}`,
  redirect: `font-semibold ${CLASS_TEXT["3xx"]}`,
  client: `font-semibold ${CLASS_TEXT["4xx"]}`,
  server: `font-semibold ${CLASS_TEXT["5xx"]}`,
}

function classFor(span: Span) {
  if (span.kind === "level") return LEVEL_WORD[span.level ?? "unknown"]
  if (span.kind === "status") return STATUS[statusClass(span.status)]
  return tokenClass(span.kind)
}

/** A program's name in its lane, so one process can be followed down a busy page. */
export function laneStyle(name: string | undefined) {
  return name ? { color: hueFor(name, LANES) } : undefined
}

/**
 * The fields a structured line carries, most telling first. The server sends
 * them as a map, which JSON orders by key; "action" and "actor" would lead
 * every audit line while the status and the address sat at the end.
 */
const FIELD_ORDER = [
  "error",
  "err",
  "status",
  "code",
  "method",
  "path",
  "url",
  "uri",
  "duration",
  "latency",
  "took",
  "ip",
  "remote_ip",
  "client_ip",
  "user",
]

function fieldRank(key: string) {
  const at = FIELD_ORDER.indexOf(key.toLowerCase())
  return at < 0 ? FIELD_ORDER.length : at
}

/**
 * A structured line as the one sentence and its context — `audit  status=204
 * ip=100.84.53.82 user=wayy` — in the logfmt shape the tokenizer already reads,
 * so every field is coloured by the same rules as a line that was written that
 * way. Null where the line has no message, or where drawing anything but the
 * raw text would lose the search's match ranges, which are over the JSON.
 */
export function structuredText(line: LogLine): string | null {
  if (!line.message || line.match?.length) return null
  const fields = Object.entries(line.fields ?? {})
    .filter(([, value]) => value !== "")
    .sort(([a], [b]) => fieldRank(a) - fieldRank(b) || a.localeCompare(b))
    .map(([key, value]) => `${key}=${/[\s"=]/.test(value) ? JSON.stringify(value) : value}`)
  return fields.length ? `${line.message}  ${fields.join(" ")}` : line.message
}

export function LogText({
  text,
  hits,
  skipTime,
  hostname,
  className,
}: {
  text: string
  /** Search hits as ranges over `text`. */
  hits?: [number, number][]
  /** The console shows the time in a column, so the line's own is not drawn twice. */
  skipTime?: boolean
  /** This host's name, which syslog repeats on every line. */
  hostname?: string
  className?: string
}) {
  const spans = tokenize(text)
  const prefix = skipTime ? leadingTime(spans, text, hostname) : 0
  // A search that matched the prefix itself — a hostname, a second of the
  // clock — keeps it drawn, or the line would be a result with no hit on it.
  const from = hits?.some(([start]) => start < prefix) ? 0 : prefix
  return (
    <span className={className}>
      {pieces(text, spans, hits, from).map((piece, i) => {
        const cls = piece.span ? classFor(piece.span) : undefined
        const style =
          piece.span?.kind === "proc"
            ? laneStyle(text.slice(piece.span.start, piece.span.end))
            : undefined
        return piece.hit ? (
          <mark key={i} className={cn("rounded-sm bg-mark px-px", cls)} style={style}>
            {piece.text}
          </mark>
        ) : cls || style ? (
          <span key={i} className={cls} style={style}>
            {piece.text}
          </span>
        ) : (
          piece.text
        )
      })}
    </span>
  )
}
