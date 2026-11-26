/*
 * The Logs page: sources and journal units, lines and buckets, searches,
 * facets and measures, streams, rotation and retention.
 */

export type LogSource = {
  id: string
  label: string
  kind:
    | "system"
    | "nginx"
    | "app"
    | "pm2"
    | "docker"
    | "journal"
    /** Every container of one compose project, merged by time. */
    | "stack"
    /** The journal of one or more syslog identifiers (`journalctl -t`). */
    | "journal-id"
    /** The kernel ring (`journalctl -k`). */
    | "kernel"
  path?: string
  size?: number
  modified?: string
  /** When the first record still kept was written: the whole journal's, from `/logs/source`. */
  oldest?: string
  rotated: boolean
  /** Rotated generations sitting next to a live file, and their total size. */
  archives?: number
  archiveBytes?: number
  /** One line saying what this source actually holds, for the reader who has never met auth.log. */
  detail?: string
  /** A live source's state — a stopped container still has logs worth reading. */
  status?: string
  /**
   * The lens the server reads this source through (`lib/log/lenses.ts`) —
   * detected from the path, the image or the unit. Absent where none applies,
   * and for a stack whose containers disagree.
   */
  lens?: string
  /** A stack's services' images, for drawing it as the products it runs. */
  images?: string[]
}

export type LogJournalUnit = {
  name: string
  description: string
  active: string
  /** The lens `journal:<unit>` is read through. */
  lens?: string
}

/**
 * Everything the viewer needs to offer a choice, in one request. The journal's
 * units ship with the sources rather than from a second fetch: putting every
 * unit in the source list would bury syslog under systemd's inventory, and
 * loading them separately leaves the unit picker empty for a second after the
 * journal is chosen, which reads as "this host has no units".
 */
export type LogSourceIndex = {
  sources: LogSource[]
  units: LogJournalUnit[]
  roots: string[]
  /** Why a source kind is absent — "no Docker here" is not the same as "no containers". */
  missing: Record<string, string>
}

export type LogLine = {
  text: string
  level?: string
  timestamp?: string
  source?: string
  /**
   * "stdout" or "stderr", sent wherever the producer distinguishes them — a
   * container and a PM2 process do, a plain file does not.
   */
  stream?: string
  /** 1-based line number within its file, set by the history search. */
  no?: number
  /** True for a line included because it sits next to a match, not because it matched. */
  context?: boolean
  /** Which file of a rotated set this came from. */
  file?: string
  /** Byte ranges of the search term, computed server-side where the browser cannot re-run a Go regexp. */
  match?: [number, number][]
  /**
   * The human sentence out of a structured line, and the context around it.
   * Both are absent for the plain text that is most of a host's logs; where
   * they are set, the line was JSON and the viewer shows the sentence with the
   * request id and the rest a keystroke away rather than making the reader
   * parse JSON by eye.
   */
  message?: string
  fields?: Record<string, string>
  /**
   * What the line records, in its lens's words ("auth_failed", "slow",
   * "ban"), and the values the lens read out of its text. A plain line keeps
   * being drawn as the text it is; these are what it can be filtered,
   * counted and labelled by.
   */
  event?: string
  attrs?: Record<string, string>
  /**
   * The line continues the record above it — a Postgres DETAIL, a stack
   * frame. It carries its head's level, took its head's verdict under the
   * filter, and is folded under it.
   */
  cont?: boolean
  /**
   * Where the message starts in `text`, past a prefix the lens read into the
   * line's time, level and attrs — Postgres's "[812] app@shop FATAL:  ".
   * Coloured, the console draws the line from there; `text` keeps every byte.
   */
  body?: number
  /** The lens that named `event`, where it is not the stream's own (a syslog line read as auth). */
  lens?: string
}

/** One column of the search histogram, counted by level — or by `histogramBy`'s values. */
export type LogBucket = {
  start: string
  total: number
  counts: Record<string, number>
}

export type LogSearchedFile = {
  path: string
  name: string
  archive: boolean
  scanned: number
  matched: number
  modified?: string
  error?: string
}

export type LogSearchResult = {
  lines: LogLine[]
  scanned: number
  matched: number
  truncated: boolean
  /** False when the scan ran out of time rather than out of file. */
  complete: boolean
  files: LogSearchedFile[]
  histogram: LogBucket[]
  bucketSeconds?: number
  first?: string
  last?: string
  tookMillis: number
  /** Top values per asked-for key, over every match in the window (`facets=`). */
  facets?: Record<string, LogFacet>
  /** The distribution of a numeric key over the matches (`measure=`). */
  measure?: LogMeasure
  /** The lens the search read the source through. */
  lens?: string
  /** What the histogram's counts are keyed by, when it is not the level. */
  histogramBy?: string
}

export type LogFacetValue = {
  value: string
  count: number
  /** How many of those lines were errors or worse. */
  errors: number
  sum?: number
  max?: number
  first?: string
  last?: string
  /** The last value seen of each `sample=` key, beside this one. */
  samples?: Record<string, string>
}

export type LogFacet = {
  values: LogFacetValue[]
  distinct: number
  /** The distinct count stopped being exact past the server's budget. */
  distinctCapped?: boolean
  /** Matches whose value is not among `values`. */
  other: number
  /** Matches that carry no value for the key at all. */
  missing: number
}

export type LogMeasure = {
  key: string
  count: number
  p50: number
  p75: number
  p90: number
  p95: number
  p99: number
  max: number
  mean: number
}

/** The frame the live socket opens with, before any lines. */
export type LogStreamMeta = {
  kind: LogSource["kind"]
  label: string
  path?: string
  filtered: boolean
  prefill?: { lines: number; complete: boolean }
  archives?: number
  note?: string
  /** The lens the stream reads its lines through. */
  lens?: string
}

export type LogRotateRule = {
  configFile: string
  patterns: string[]
  frequency?: string
  rotate?: string
  compress: boolean
  size?: string
  maxSize?: string
  missingOk: boolean
  options: string[]
}

/** The verdict for one file: is anything actually trimming this. */
export type LogRetention = {
  managed: boolean
  rule?: LogRotateRule
  pattern?: string
  summary: string
  level: "ok" | "warn" | "unknown"
  lastRun?: string
  available: boolean
}
