import { duration } from "@/lib/format"
import type { Tone } from "@/components/tone"
import type { DotTone } from "@/components/status-dot"
import type { ProxyConfigEntry, ProxyEffectiveConfig, ProxyPlacedDirective } from "@/lib/types"

/**
 * The Configuration page's reading of the nginx directory and of what nginx
 * loads: the tree grouped by folder and filtered, each file's state in a
 * word, and the search over `nginx -T` by text, pattern or directive.
 */

/** A path under the nginx directory as the page names it: relative, or whole when it is elsewhere. */
export function relativePath(path: string, root: string): string {
  const dir = root.replace(/\/+$/, "")
  return path.startsWith(`${dir}/`) ? path.slice(dir.length + 1) : path
}

/** The folder a file sits in, relative to the root: "" for the root itself. */
export function folderOf(path: string, root: string): string {
  const relative = relativePath(path, root)
  if (relative === path) return path.replace(/\/[^/]*$/, "")
  const slash = relative.lastIndexOf("/")
  return slash < 0 ? "" : relative.slice(0, slash)
}

export type FileGroup = { folder: string; entries: ProxyConfigEntry[] }

/** The files by folder: the root's own first, then each folder by name. */
export function groupByFolder(entries: ProxyConfigEntry[], root: string): FileGroup[] {
  const groups = new Map<string, ProxyConfigEntry[]>()
  for (const entry of entries) {
    const folder = folderOf(entry.path, root)
    groups.set(folder, [...(groups.get(folder) ?? []), entry])
  }
  return [...groups.entries()]
    .sort(([a], [b]) => (a === "" ? -1 : b === "" ? 1 : a.localeCompare(b)))
    .map(([folder, list]) => ({
      folder,
      entries: [...list].sort((a, b) => a.path.localeCompare(b.path)),
    }))
}

export type FileFilter = "all" | "read" | "unread" | "managed" | "hand"

/**
 * Whether a file counts under a filter. A link is the way to its file rather
 * than a file of its own, so it is under "read" or "not read" and neither
 * "managed" nor "by hand"; a password file is none of the four.
 */
export function inFilter(entry: ProxyConfigEntry, filter: FileFilter): boolean {
  switch (filter) {
    case "all":
      return true
    case "read":
      return entry.included
    case "unread":
      return !entry.included && !entry.protected
    case "managed":
      return entry.managed && entry.kind !== "link" && !entry.protected
    case "hand":
      return !entry.managed && entry.kind !== "link" && !entry.protected
  }
}

export function filterCounts(entries: ProxyConfigEntry[]): Record<FileFilter, number> {
  const filters: FileFilter[] = ["all", "read", "unread", "managed", "hand"]
  return Object.fromEntries(
    filters.map((filter) => [filter, entries.filter((e) => inFilter(e, filter)).length]),
  ) as Record<FileFilter, number>
}

/** The files a filter and a search leave, the search matched against the path under the root. */
export function visibleFiles(
  entries: ProxyConfigEntry[],
  root: string,
  filter: FileFilter,
  query: string,
): ProxyConfigEntry[] {
  const needle = query.trim().toLowerCase()
  return entries.filter(
    (entry) =>
      inFilter(entry, filter) &&
      (!needle || relativePath(entry.path, root).toLowerCase().includes(needle)),
  )
}

/**
 * How many files nginx reads. A link and the file it points at are one file
 * read once, so each is counted as the file it resolves to.
 */
export function readCount(entries: ProxyConfigEntry[]): number {
  return new Set(entries.filter((e) => e.included).map((e) => e.target ?? e.path)).size
}

/** The files nginx does not read, among those it could: not a link, not a password file. */
export function unreadCount(entries: ProxyConfigEntry[]): number {
  return entries.filter((e) => !e.included && !e.protected && e.kind !== "link").length
}

/** The files the dashboard wrote, and those written by hand, links and password files left out. */
export function authorship(entries: ProxyConfigEntry[]): { managed: number; hand: number } {
  const files = entries.filter((e) => e.kind !== "link" && !e.protected)
  const managed = files.filter((e) => e.managed).length
  return { managed, hand: files.length - managed }
}

export type ReadState = { label: string; tone: DotTone }

/**
 * Whether nginx reads a file, in a word. A file the walk did not reach while
 * the includes could not all be followed is not known rather than unread.
 */
export function readState(entry: ProxyConfigEntry, includesKnown: boolean): ReadState {
  if (entry.missing) return { label: "points at nothing", tone: "danger" }
  if (entry.included) return { label: "read", tone: "running" }
  if (!includesKnown) return { label: "not known", tone: "unknown" }
  return { label: "not read", tone: "stopped" }
}

/** The file the editor opens for a row, or nothing when there is none it may open. */
export function editorPath(entry: ProxyConfigEntry): string | undefined {
  if (entry.protected || entry.outside || entry.missing) return undefined
  return entry.kind === "link" ? entry.target : entry.path
}

/** The row's second line: where a link points, or why nginx reads the file, then its size and age. */
export function entryNote(
  entry: ProxyConfigEntry,
  root: string,
  size: (bytes: number) => string,
  age: (iso: string) => string,
): string {
  if (entry.protected) {
    return entry.managed ? "the dashboard's password file, hidden" : "password file, hidden"
  }
  if (entry.kind === "link") {
    if (entry.missing) return `→ ${entry.target ? relativePath(entry.target, root) : "nothing"}`
    const target = `→ ${relativePath(entry.target ?? "", root)}`
    return entry.outside ? `${target}, outside ${root}` : target
  }
  const parts: string[] = []
  if (entry.kind === "main") parts.push("the main file")
  else if (entry.includedBy?.via) parts.push(`via ${relativePath(entry.includedBy.via, root)}`)
  else if (entry.includedBy) {
    parts.push(`included by ${relativePath(entry.includedBy.file, root)}:${entry.includedBy.line}`)
  }
  parts.push(size(entry.size), age(entry.modified))
  return parts.join(" · ")
}

/** What a filter's chip reads. */
export const FILTER_LABEL: Record<FileFilter, string> = {
  all: "All",
  read: "Read by nginx",
  unread: "Not read",
  managed: "Managed",
  hand: "By hand",
}

/** A file's tags: the dashboard wrote it, or it is a backup nginx reads anyway. */
export function entryTags(entry: ProxyConfigEntry): { label: string; tone: Tone }[] {
  const tags: { label: string; tone: Tone }[] = []
  if (entry.backup && entry.included) tags.push({ label: "backup", tone: "warning" })
  if (entry.managed && !entry.protected && entry.kind !== "link") {
    tags.push({ label: "managed", tone: "default" })
  }
  return tags
}

export type SearchMode = "text" | "regex" | "directive"

/** One place in the configuration a search found. */
export type SearchMatch = {
  /** The file as nginx printed it, and the file the editor opens for it. */
  file: string
  open: string
  line: number
  /** The line, or the directive as one line, without its indent. */
  text: string
  /** Where the match is in `text`, to mark it. */
  start: number
  end: number
  /** The blocks the line sits in, where it is a directive's. */
  within?: string[]
}

export type SearchResult = {
  matches: SearchMatch[]
  /** Every match, of which `matches` holds the first MAX_MATCHES. */
  total: number
  error?: string
}

/** The most matches drawn; the rest are counted. */
export const MAX_MATCHES = 200

/** A file's lines, the last line's own line feed not being a line after it. */
export function fileLines(content: string): string[] {
  if (content === "") return []
  const lines = content.split("\n")
  if (lines[lines.length - 1] === "") lines.pop()
  return lines
}

/** A directive as it would be written on one line. */
export function directiveText(d: ProxyPlacedDirective): string {
  const args = d.args.map((arg) =>
    arg === "" || /[\s;{}"']/.test(arg) ? `"${arg.replace(/(["\\])/g, "\\$1")}"` : arg,
  )
  return `${[d.name, ...args].join(" ")}${d.opens ? " {" : ";"}`
}

/**
 * Searches what nginx loads. Text is found in any case; a pattern is a
 * JavaScript regular expression, also in any case; a directive search is the
 * directive's name and then words its arguments contain — "listen 443",
 * "proxy_pass 127.0.0.1". A name typed part-way matches the names it begins
 * until one of them is typed out, so the list follows the typing rather than
 * going blank until the last letter.
 */
export function searchEffective(
  config: ProxyEffectiveConfig,
  query: string,
  mode: SearchMode,
): SearchResult {
  const trimmed = query.trim()
  if (!trimmed) return { matches: [], total: 0 }
  const target = new Map(config.files.map((f) => [f.path, f.target ?? f.path]))
  const within = new Map<string, string[]>()
  for (const d of config.directives ?? []) within.set(`${d.file}:${d.line}`, d.within)

  const matches: SearchMatch[] = []
  let total = 0
  const add = (match: Omit<SearchMatch, "open" | "within">) => {
    total++
    if (matches.length < MAX_MATCHES) {
      matches.push({
        ...match,
        open: target.get(match.file) ?? match.file,
        within: within.get(`${match.file}:${match.line}`),
      })
    }
  }

  if (mode === "directive") {
    if (!config.directives) {
      return {
        matches: [],
        total: 0,
        error: `A search by directive needs the configuration read as nginx reads it, which failed: ${config.treeError ?? "unknown error"}`,
      }
    }
    const [name, ...words] = trimmed.toLowerCase().split(/\s+/)
    const exact = config.directives.some((d) => d.name === name)
    for (const d of config.directives) {
      const named = exact ? d.name === name : words.length === 0 && d.name.startsWith(name)
      if (!named) continue
      const args = d.args.join(" ").toLowerCase()
      if (!words.every((word) => args.includes(word))) continue
      add({ file: d.file, line: d.line, text: directiveText(d), start: 0, end: d.name.length })
    }
    return { matches, total }
  }

  let pattern: RegExp | undefined
  if (mode === "regex") {
    try {
      pattern = new RegExp(trimmed, "i")
    } catch (err) {
      return {
        matches: [],
        total: 0,
        error: `Not a regular expression: ${err instanceof Error ? err.message : String(err)}`,
      }
    }
  }
  const needle = trimmed.toLowerCase()
  for (const file of config.files) {
    fileLines(file.content).forEach((raw, index) => {
      const text = raw.trimStart()
      let start = -1
      let end = -1
      if (pattern) {
        const found = pattern.exec(text)
        if (found) {
          start = found.index
          end = found.index + found[0].length
        }
      } else {
        start = text.toLowerCase().indexOf(needle)
        end = start + needle.length
      }
      if (start >= 0) add({ file: file.path, line: index + 1, text, start, end })
    })
  }
  return { matches, total }
}

/** Lines added and removed in a unified diff, its file headers left out. */
export function diffStat(diff: string): { added: number; removed: number } {
  let added = 0
  let removed = 0
  for (const line of diff.split("\n")) {
    if (line.startsWith("+++") || line.startsWith("---")) continue
    if (line.startsWith("+")) added++
    else if (line.startsWith("-")) removed++
  }
  return { added, removed }
}

/** "read 12s ago", in its largest unit, as the config test's age is said. */
export function readLabel(at: number, now: number): string {
  const age = Math.max(0, (now - at) / 1000)
  return age < 5 ? "read just now" : `read ${duration(age).split(" ")[0]} ago`
}
