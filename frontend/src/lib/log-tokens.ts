import type { LogLevel } from "@/lib/log-filter"

/**
 * A log line, read for its shapes.
 *
 * Every line on a server is written by a different program and none of them
 * agree on a format, which is why a log viewer that draws them as one column
 * of white text is a wall: the reader has to parse each line by eye to find
 * the part that says what happened. The shapes are few and each program
 * repeats its own on every line — syslog's `time host proc[pid]:`, nginx's
 * common log format, logfmt's `key=value`, a JSON object, bracketed levels,
 * addresses, request lines, status codes — so they can be told apart without
 * knowing which program wrote them, and drawn so the eye skips the prefix and
 * lands on the message.
 *
 * The output is spans over the **original** text, never a rewritten string:
 * the search's match ranges are byte offsets into that text, and a tokenizer
 * that reformatted it would put every highlight in the wrong place. Anything a
 * span does not cover is the message, drawn in the foreground.
 *
 * Nothing here is a parser. Every rule is a shape, a line matching none of them
 * is left as it was, and a wrong guess costs a colour — never a character.
 */

export type TokenKind =
  /** A timestamp the line carries in its own text. */
  | "time"
  /** Syslog's hostname — this host, on every line. */
  | "host"
  /** The program that spoke: syslog's `sshd-session`, a logger name. */
  | "proc"
  | "pid"
  /** A level the line names: `[Error]`, `WARN`, `level=info`. */
  | "level"
  /** A logfmt or JSON key. */
  | "key"
  | "string"
  | "number"
  | "ip"
  | "url"
  | "path"
  | "method"
  /** An HTTP status, read by its class. */
  | "status"
  /** An id nobody reads — a uuid, a hash, a MAC address. */
  | "id"
  /** A word that says something went wrong: failed, denied, refused. */
  | "bad"
  /** A word that says it went right: accepted, started, healthy. */
  | "good"
  | "punct"

export type Span = {
  start: number
  end: number
  kind: TokenKind
  /** For `level`: which one, on the filter's scale. */
  level?: LogLevel
  /** For `status`: the code, so it can be read by its class. */
  status?: number
}

const LEVEL_OF: Record<string, LogLevel> = {
  emerg: "critical",
  alert: "critical",
  crit: "critical",
  critical: "critical",
  fatal: "critical",
  panic: "critical",
  error: "error",
  err: "error",
  eror: "error",
  warn: "warn",
  warning: "warn",
  wrn: "warn",
  notice: "info",
  info: "info",
  inf: "info",
  debug: "debug",
  dbg: "debug",
  trace: "debug",
}

export function levelOf(word: string): LogLevel | undefined {
  return LEVEL_OF[word.toLowerCase()]
}

const METHOD = "GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS|CONNECT|TRACE|PROPFIND"

// Syslog, as rsyslog writes it: an RFC 3339 or a BSD timestamp, the host, and
// the program with its pid. The journal's short output has the same shape.
const SYSLOG = new RegExp(
  String.raw`^(\d{4}-\d\d-\d\dT[\d:.]+(?:Z|[+-]\d\d:?\d\d)?|[A-Z][a-z]{2} [ \d]\d \d\d:\d\d:\d\d) (\S+) ([\w./@-]+?)(\[\d+\])?: `,
)

// The common and combined log formats nginx and Apache write by default.
const CLF = new RegExp(
  String.raw`^(\S+) (\S+) (\S+) (\[[^\]]+\]) "(${METHOD}) (\S+)(?: (HTTP\/[\d.]+))?" (\d{3}) (\d+|-)`,
)

// A status at the head of the line, as a Go or Node request logger prints
// it: `200 GET /api/health-check with 15 bytes took 1ms`.
const LEADING_STATUS = new RegExp(String.raw`^([1-5]\d\d) (?=(?:${METHOD}) )`)

// Words, looked up rather than alternated: a regular expression of ninety
// spellings tried at every character was most of the cost of a line, and one
// word match plus a set lookup is the same answer — the reason the backend's
// level scan is a map, too.
const BAD = new Set([
  "failed",
  "failure",
  "failures",
  "fail",
  "fails",
  "denied",
  "refused",
  "refusing",
  "error",
  "errors",
  "exception",
  "panic",
  "panicked",
  "timeout",
  "unreachable",
  "invalid",
  "unable",
  "cannot",
  "can't",
  "couldn't",
  "killed",
  "aborted",
  "aborting",
  "unhealthy",
  "disconnected",
  "rejected",
  "forbidden",
  "unauthorized",
  "segfault",
  "traceback",
])
const GOOD = new Set([
  "success",
  "succeeded",
  "successful",
  "successfully",
  "started",
  "accepted",
  "connected",
  "healthy",
  "listening",
  "ready",
  "completed",
  "passed",
  "established",
  "recovered",
])
// Counted only when shouted: "info" in a sentence is a word, INFO a level, and
// "drop" is a verb where DROP is the firewall's verdict.
const SHOUTED_LEVEL = new Set([
  "EMERG",
  "ALERT",
  "CRIT",
  "CRITICAL",
  "FATAL",
  "PANIC",
  "ERROR",
  "ERR",
  "WARN",
  "WARNING",
  "NOTICE",
  "INFO",
  "DEBUG",
  "TRACE",
])
const FIREWALL = new Set(["BLOCK", "BLOCKED", "DROP", "DROPPED", "REJECT", "DENY"])
const BRACKETED = Object.keys(LEVEL_OF)
  .flatMap((w) => [w, w[0].toUpperCase() + w.slice(1), w.toUpperCase()])
  .join("|")
const HEX = "[0-9a-fA-F]"

/**
 * One alternation, tried left to right at each position, so the order is the
 * precedence: a timestamp before a clock, a URL before a path, a quoted string
 * before the words inside it, a key=value before the key's own word.
 */
const SCAN = new RegExp(
  [
    String.raw`(?<time>\d{4}-\d\d-\d\d[T ]\d\d:\d\d:\d\d(?:[.,]\d+)?(?:Z|[+-]\d\d:?\d\d)?|\d{4}\/\d\d\/\d\d \d\d:\d\d:\d\d(?:\.\d+)?|\b\d\d:\d\d:\d\d(?:\.\d+)?\b)`,
    String.raw`(?<blevel>\[(?:${BRACKETED})\])`,
    String.raw`(?<url>\b(?:https?|wss?|ftp):\/\/[^\s"'<>\])}]+)`,
    String.raw`(?<kv>\b[A-Za-z_][\w.-]*)=(?<kvv>"(?:[^"\\]|\\.)*"|[^\s,;)\]]*)`,
    String.raw`(?<jkey>"(?:[^"\\]|\\.)*")(?=\s*:)`,
    String.raw`(?<string>"(?:[^"\\]|\\.)*")`,
    String.raw`(?<proto>HTTP\/[\d.]+"?\)?) (?<pstatus>[1-5]\d\d)\b`,
    String.raw`(?<uuid>\b${HEX}{8}-${HEX}{4}-${HEX}{4}-${HEX}{4}-${HEX}{12}\b)`,
    String.raw`(?<mac>\b(?:${HEX}{2}:){5}${HEX}{2}\b)`,
    String.raw`(?<ip4>\b(?:\d{1,3}\.){3}\d{1,3}(?::\d{1,5})?(?:\/\d{1,2})?\b)`,
    String.raw`(?<ip6>(?<![\w:])(?:${HEX}{0,4}:){2,7}${HEX}{0,4}(?![\w:]))`,
    String.raw`(?<method>\b(?:${METHOD})\b(?= \/))`,
    String.raw`(?<hash>\b(?=${HEX}*[a-fA-F])(?=${HEX}*\d)${HEX}{12,64}\b)`,
    String.raw`(?<source>\b[\w-]+\.(?:go|js|ts|py|rb|rs|c|java):\d+\b)`,
    String.raw`(?<path>(?<![\w.:\/~-])\/(?:[\w.@%+~,-]+\/?)+)`,
    String.raw`(?<unit>\b\d+(?:\.\d+)?(?:\s?(?:msecs?|ms|µs|us|ns|secs?|seconds|[KMGTP]i?B|bytes)|(?:s|m|h|d|%))(?![\w.]))`,
    // Last, so every shape that starts with a letter is tried first — and a
    // word consumed whole is a word whose letters are never scanned again.
    String.raw`(?<word>\b[A-Za-z][A-Za-z'_-]*)`,
  ].join("|"),
  "g",
)

/**
 * Tokens by text. A server's log repeats itself — a health check every thirty
 * seconds, the same audit line per request — and the live pane redraws every
 * line it holds whenever one arrives.
 */
const CACHE = new Map<string, Span[]>()
const CACHE_SIZE = 8000

/** The spans of one line. */
export function tokenize(text: string): Span[] {
  const known = CACHE.get(text)
  if (known) return known
  const spans = scan(text)
  if (CACHE.size >= CACHE_SIZE) CACHE.delete(CACHE.keys().next().value!)
  CACHE.set(text, spans)
  return spans
}

function scan(text: string): Span[] {
  if (text.startsWith("{") && text.trimEnd().endsWith("}")) return tokenizeJSON(text)

  const spans: Span[] = []
  let from = 0

  const syslog = SYSLOG.exec(text)
  if (syslog) {
    let at = 0
    const push = (part: string | undefined, kind: TokenKind) => {
      if (!part) return
      const start = text.indexOf(part, at)
      spans.push({ start, end: start + part.length, kind })
      at = start + part.length
    }
    push(syslog[1], "time")
    push(syslog[2], "host")
    push(syslog[3], "proc")
    push(syslog[4], "pid")
    spans.push({ start: syslog[0].length - 2, end: syslog[0].length - 1, kind: "punct" })
    from = syslog[0].length
  } else {
    const clf = CLF.exec(text)
    if (clf) {
      let at = 0
      const push = (part: string | undefined, kind: TokenKind, extra?: Partial<Span>) => {
        if (!part) return
        const start = text.indexOf(part, at)
        spans.push({ start, end: start + part.length, kind, ...extra })
        at = start + part.length
      }
      push(clf[1], /^[\d.:a-f]+$/i.test(clf[1]) ? "ip" : "host")
      push(clf[4], "time")
      push(clf[5], "method")
      push(clf[6], "path")
      push(clf[7], "punct")
      push(clf[8], "status", { status: Number(clf[8]) })
      push(clf[9], "number")
      from = clf[0].length
    } else {
      const lead = LEADING_STATUS.exec(text)
      if (lead) {
        spans.push({ start: 0, end: 3, kind: "status", status: Number(lead[1]) })
        from = 4
      }
    }
  }

  SCAN.lastIndex = from
  for (let m = SCAN.exec(text); m; m = SCAN.exec(text)) {
    if (m[0] === "") {
      SCAN.lastIndex++
      continue
    }
    const g = m.groups!
    const start = m.index
    const end = start + m[0].length
    const name = Object.keys(g).find(
      (key) => g[key] !== undefined && key !== "kvv" && key !== "pstatus",
    )!
    switch (name) {
      case "time":
        spans.push({ start, end, kind: "time" })
        break
      case "blevel":
        spans.push({ start, end, kind: "level", level: levelOf(m[0].slice(1, -1)) })
        break
      case "word": {
        const word = m[0]
        if (SHOUTED_LEVEL.has(word)) spans.push({ start, end, kind: "level", level: levelOf(word) })
        else if (FIREWALL.has(word)) spans.push({ start, end, kind: "bad" })
        else if (BAD.has(word.toLowerCase())) spans.push({ start, end, kind: "bad" })
        else if (GOOD.has(word.toLowerCase())) spans.push({ start, end, kind: "good" })
        break
      }
      case "url":
        spans.push({ start, end, kind: "url" })
        break
      case "kv":
        pushPair(spans, text, start, g.kv, g.kvv ?? "")
        break
      case "jkey":
        spans.push({ start, end, kind: "key" })
        break
      case "string":
        spans.push({ start, end, kind: "string" })
        break
      case "proto": {
        const code = start + m[0].lastIndexOf(g.pstatus)
        spans.push({ start, end: start + g.proto.length, kind: "punct" })
        spans.push({ start: code, end: code + 3, kind: "status", status: Number(g.pstatus) })
        break
      }
      case "ip6":
        // A clock, a MAC and `a:b` in prose all fit the shape; an address
        // either compresses (`::`) or spells a hex letter.
        if (!/::|[a-f]/i.test(m[0]) || (m[0].match(/:/g) ?? []).length < 2) {
          SCAN.lastIndex = start + 1
          continue
        }
        spans.push({ start, end, kind: "ip" })
        break
      case "ip4":
        spans.push({ start, end, kind: "ip" })
        break
      case "uuid":
      case "mac":
      case "hash":
        spans.push({ start, end, kind: "id" })
        break
      case "method":
        spans.push({ start, end, kind: "method" })
        break
      case "source":
      case "path":
        spans.push({ start, end, kind: "path" })
        break
      case "unit":
        spans.push({ start, end, kind: "number" })
        break
    }
    // A pair's value is read by a nested call that shares this expression,
    // and so its `lastIndex`: put it back where this match ended.
    SCAN.lastIndex = end
  }
  return spans
}

/**
 * A logfmt pair. The key is always a key; the value is read by what the key
 * says it is, because `status=204` is a status and `level=warn` a level in a
 * way that neither `204` nor `warn` is on its own. The message itself stays
 * the message — `msg="database initialized"` is the one value on the line
 * that is meant to be read, so it is not dimmed into a string.
 */
function pushPair(spans: Span[], text: string, start: number, key: string, value: string) {
  const eq = start + key.length
  spans.push({ start, end: eq, kind: "key" })
  spans.push({ start: eq, end: eq + 1, kind: "punct" })
  if (!value) return
  const vs = eq + 1
  const ve = vs + value.length
  const bare = value.replace(/^"|"$/g, "")
  const k = key.toLowerCase()
  if (/^(level|lvl|severity|loglevel)$/.test(k) && levelOf(bare)) {
    spans.push({ start: vs, end: ve, kind: "level", level: levelOf(bare) })
  } else if (/^(time|ts|timestamp|t)$/.test(k)) {
    spans.push({ start: vs, end: ve, kind: "time" })
  } else if (/^(msg|message)$/.test(k)) {
    if (value.startsWith('"')) {
      spans.push({ start: vs, end: vs + 1, kind: "punct" })
      spans.push({ start: ve - 1, end: ve, kind: "punct" })
    }
  } else if (/(^|_)(status|code|status_code)$/.test(k) && /^[1-5]\d\d$/.test(bare)) {
    spans.push({ start: vs, end: ve, kind: "status", status: Number(bare) })
  } else if (/^(err|error|errors|exception)$/.test(k)) {
    spans.push({ start: vs, end: ve, kind: "bad" })
  } else {
    // Anything else is read for its own shape — an address, a path, a
    // duration — and left in the foreground when it has none.
    for (const span of tokenize(value)) {
      spans.push({ ...span, start: vs + span.start, end: vs + span.end })
    }
  }
}

const JSON_SCAN = new RegExp(
  String.raw`(?<key>"(?:[^"\\]|\\.)*")(?=\s*:)|(?<string>"(?:[^"\\]|\\.)*")|(?<number>-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)|(?<literal>\btrue\b|\bfalse\b|\bnull\b)|(?<punct>[{}[\],:])`,
  "g",
)

/**
 * A JSON line, drawn raw: keys muted, and each value read by its key the way
 * a logfmt pair is. The structured view — the message and its fields — is the
 * console's, from what the server already parsed; this is for the moment the
 * reader asks to see the line as it was written.
 */
function tokenizeJSON(text: string): Span[] {
  const spans: Span[] = []
  let lastKey = ""
  JSON_SCAN.lastIndex = 0
  for (let m = JSON_SCAN.exec(text); m; m = JSON_SCAN.exec(text)) {
    const g = m.groups!
    const start = m.index
    const end = start + m[0].length
    if (g.key) {
      lastKey = g.key.slice(1, -1).toLowerCase()
      spans.push({ start, end, kind: "key" })
    } else if (g.string) {
      const bare = g.string.slice(1, -1)
      if (/^(level|lvl|severity)$/.test(lastKey) && levelOf(bare)) {
        spans.push({ start, end, kind: "level", level: levelOf(bare) })
      } else if (/^(time|ts|timestamp|@timestamp)$/.test(lastKey)) {
        spans.push({ start, end, kind: "time" })
      } else if (/^(msg|message|@message)$/.test(lastKey)) {
        // The sentence stays in the foreground.
      } else if (/^(err|error|exception)$/.test(lastKey)) {
        spans.push({ start, end, kind: "bad" })
      } else {
        const inner = tokenize(bare).filter((s) => s.kind !== "bad" && s.kind !== "good")
        if (inner.length === 1 && inner[0].start === 0 && inner[0].end === bare.length) {
          spans.push({ start, end, kind: inner[0].kind, status: inner[0].status })
        } else {
          spans.push({ start, end, kind: "string" })
        }
        JSON_SCAN.lastIndex = end
      }
      lastKey = ""
    } else if (g.number) {
      if (/(^|_)(status|code)$/.test(lastKey) && /^[1-5]\d\d$/.test(g.number)) {
        spans.push({ start, end, kind: "status", status: Number(g.number) })
      } else if (/^(ts|time)$/.test(lastKey)) {
        spans.push({ start, end, kind: "time" })
      } else {
        spans.push({ start, end, kind: "number" })
      }
      lastKey = ""
    } else if (g.literal) {
      spans.push({
        start,
        end,
        kind: m[0] === "true" ? "good" : m[0] === "false" ? "bad" : "punct",
      })
      lastKey = ""
    } else {
      spans.push({ start, end, kind: "punct" })
    }
  }
  return spans
}

/** The class an HTTP status is read as. */
export function statusClass(code: number | undefined): "ok" | "redirect" | "client" | "server" {
  if (!code || code < 300) return "ok"
  if (code < 400) return "redirect"
  if (code < 500) return "client"
  return "server"
}

/**
 * The line cut into pieces for drawing: every span, the gaps between them as
 * plain text, and each piece split again wherever a search hit starts or
 * stops, so a hit inside an address keeps the address's colour under its mark.
 */
export function pieces(
  text: string,
  spans: Span[],
  hits: [number, number][] | undefined,
  from = 0,
): { text: string; span?: Span; hit: boolean }[] {
  const ordered = [...spans].sort((a, b) => a.start - b.start)
  const cuts = new Set<number>([from, text.length])
  for (const s of ordered) {
    if (s.end > from) cuts.add(Math.max(s.start, from)).add(s.end)
  }
  for (const [a, b] of hits ?? []) {
    if (b > from) cuts.add(Math.max(a, from)).add(Math.min(b, text.length))
  }
  const points = [...cuts].filter((n) => n >= from && n <= text.length).sort((a, b) => a - b)
  const out: { text: string; span?: Span; hit: boolean }[] = []
  let k = 0
  for (let i = 0; i < points.length - 1; i++) {
    const a = points[i]
    const b = points[i + 1]
    if (a === b) continue
    while (k < ordered.length && ordered[k].end <= a) k++
    const span = ordered.slice(k).find((s) => s.start <= a && s.end >= b)
    const hit = (hits ?? []).some(([x, y]) => x <= a && y >= b)
    out.push({ text: text.slice(a, b), span, hit })
  }
  return out
}

/**
 * Where the line's own prefix ends, for a console that already shows the time
 * in a column: past the timestamp, and past syslog's hostname too when it is
 * this host's — the same name on every line of a one-server log is a third of
 * the width saying nothing. A line forwarded from another machine keeps it.
 */
export function leadingTime(spans: Span[], text = "", hostname?: string): number {
  const first = spans.find((s) => s.start === 0)
  if (first?.kind !== "time") return 0
  const at = first.end + 1
  const host = spans.find((s) => s.start === at && s.kind === "host")
  if (host && hostname && sameHost(text.slice(host.start, host.end), hostname)) {
    return host.end + 1
  }
  return at
}

/** A short name and its fully qualified form are one host. */
function sameHost(a: string, b: string) {
  const x = a.toLowerCase()
  const y = b.toLowerCase()
  return x === y || x.startsWith(`${y}.`) || y.startsWith(`${x}.`)
}
