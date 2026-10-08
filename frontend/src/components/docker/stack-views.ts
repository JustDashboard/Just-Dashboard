import type { DiffLine, StackDeployment } from "@/lib/types"
import { LANES } from "@/lib/hue"

/**
 * What a stack's other views read out of its compose file, its deployment
 * records and a diff between two files — pure, so each reading is tested
 * apart from the page that draws it.
 */

/* ------------------------------------------------------------- outline -- */

export type OutlineEntry = { key: string; line: number }
export type OutlineSection = OutlineEntry & { children: OutlineEntry[] }

const KEY = /^([A-Za-z0-9_.-]+|"[^"]+"|'[^']+'):(\s|$)/

/**
 * The file's top-level keys and the names under each — the services, the
 * networks, the volumes — with the line each starts on, read from
 * indentation the way compose's own files are written. A child is a key at
 * the first indentation its section uses, so a service's `image:` four
 * spaces in is never mistaken for a service.
 */
export function composeOutline(text: string): OutlineSection[] {
  const out: OutlineSection[] = []
  let section: OutlineSection | undefined
  let childIndent = -1
  text.split("\n").forEach((raw, i) => {
    const line = raw.replace(/\s+$/, "")
    const body = line.trimStart()
    if (!body || body.startsWith("#") || body === "---") return
    const indent = line.length - body.length
    const key = KEY.exec(body)?.[1]?.replace(/^["']|["']$/g, "")
    if (indent === 0) {
      section = key ? { key, line: i + 1, children: [] } : undefined
      childIndent = -1
      if (section) out.push(section)
      return
    }
    if (!section || !key) return
    if (childIndent < 0) childIndent = indent
    if (indent === childIndent) section.children.push({ key, line: i + 1 })
  })
  return out
}

/** The section and child a line sits in: where the cursor is, said as the outline says it. */
export function outlineAt(outline: OutlineSection[], line: number) {
  const section = [...outline].reverse().find((s) => s.line <= line)
  const child = section && [...section.children].reverse().find((c) => c.line <= line)
  return { section: section?.key, child: child?.key }
}

/** Compose's complaint names the line it stopped on, when it names one. */
export function errorLine(message: string | undefined): number | undefined {
  const match = message && /line (\d+)/i.exec(message)
  return match ? Number(match[1]) : undefined
}

/* ---------------------------------------------------------------- diff -- */

/**
 * Which service a line belongs to, from indentation: the server's
 * `trackSection`, so a diff made here groups as the preview's does — and a
 * key with a comment after it is still the key.
 */
function trackSection(current: string, line: string) {
  const trimmed = line.replace(/[ \t]+$/, "")
  const body = trimmed.trim().replace(/\s+#.*$/, "")
  if (!body || body.startsWith("#")) return current
  const indent = trimmed.length - trimmed.trimStart().length
  if (indent === 0) {
    const cut = body.indexOf(":")
    return cut >= 0 ? body.slice(0, cut) : current
  }
  if (indent === 2 && body.endsWith(":")) return body.slice(0, -1)
  return current
}

const CONTEXT = 3

/**
 * Two versions of a file as the lines that changed between them, with three
 * unchanged lines around each change and a gap where more were left out —
 * the shape the server's preview diff has, so the same view draws both.
 */
export function lineDiff(before: string, after: string): DiffLine[] {
  const left = before.replace(/\s+$/, "").split("\n")
  const right = after.replace(/\s+$/, "").split("\n")
  const n = left.length
  const m = right.length
  // The longest common subsequence from each pair of positions to the end.
  const lcs = Array.from({ length: n + 1 }, () => new Int32Array(m + 1))
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      lcs[i][j] =
        left[i] === right[j] ? lcs[i + 1][j + 1] + 1 : Math.max(lcs[i + 1][j], lcs[i][j + 1])
    }
  }
  const lines: DiffLine[] = []
  let section = ""
  let i = 0
  let j = 0
  while (i < n && j < m) {
    if (left[i] === right[j]) {
      section = trackSection(section, left[i])
      lines.push({ kind: "same", text: left[i], section })
      i++
      j++
    } else if (lcs[i + 1][j] >= lcs[i][j + 1]) {
      // A removed line names its own section without moving the running one:
      // a removed `db:` is db's change, and the file after it has no such line.
      lines.push({ kind: "removed", text: left[i], section: trackSection(section, left[i]) })
      i++
    } else {
      section = trackSection(section, right[j])
      lines.push({ kind: "added", text: right[j], section })
      j++
    }
  }
  for (; i < n; i++) {
    lines.push({ kind: "removed", text: left[i], section: trackSection(section, left[i]) })
  }
  for (; j < m; j++) {
    section = trackSection(section, right[j])
    lines.push({ kind: "added", text: right[j], section })
  }

  const keep = lines.map(() => false)
  lines.forEach((line, k) => {
    if (line.kind === "same") return
    for (let c = Math.max(0, k - CONTEXT); c <= Math.min(lines.length - 1, k + CONTEXT); c++) {
      keep[c] = true
    }
  })
  const out: DiffLine[] = []
  let gap = false
  lines.forEach((line, k) => {
    if (!keep[k]) {
      gap = out.length > 0
      return
    }
    if (gap) out.push({ kind: "gap", text: "…" })
    gap = false
    out.push(line)
  })
  return out
}

export type DiffHunk = { section?: string; lines: DiffLine[]; added: number; removed: number }

/**
 * A diff as its hunks — the runs between two gaps — each named for the
 * service its first change is under, so a hunk is headed by what it changes.
 */
export function diffHunks(lines: DiffLine[]): DiffHunk[] {
  const hunks: DiffHunk[] = []
  let hunk: DiffHunk | undefined
  for (const line of lines) {
    if (line.kind === "gap") {
      hunk = undefined
      continue
    }
    if (!hunk) {
      hunk = { lines: [], added: 0, removed: 0 }
      hunks.push(hunk)
    }
    hunk.lines.push(line)
    if (line.kind === "added") hunk.added++
    if (line.kind === "removed") hunk.removed++
    if (!hunk.section && line.kind !== "same" && line.section) hunk.section = line.section
  }
  return hunks
}

export function diffCounts(lines: DiffLine[]) {
  return {
    added: lines.filter((l) => l.kind === "added").length,
    removed: lines.filter((l) => l.kind === "removed").length,
  }
}

/** An image reference's repository and tag: the tag is after the last colon past the last slash. */
export function splitImage(image: string) {
  const slash = image.lastIndexOf("/")
  const colon = image.lastIndexOf(":")
  if (colon > slash) return { repo: image.slice(0, colon), tag: image.slice(colon + 1) }
  return { repo: image, tag: "latest" }
}

/* --------------------------------------------------------------- files -- */

export type FileRole = {
  /** The path inside the stack's directory, without `./` or a trailing slash. */
  path: string
  service: string
  role: "build" | "mount" | "env"
  /** Where a mount lands in the container. */
  target?: string
}

/** A relative path of the stack's own, normalised; nothing for one outside it or absolute. */
function own(path: string) {
  const clean = path.trim().replace(/^["']|["']$/g, "")
  if (!clean.startsWith("./") && clean !== ".") {
    if (clean.startsWith("/") || clean.startsWith("../") || clean.startsWith("~")) return undefined
    if (!/^[A-Za-z0-9_.-]/.test(clean)) return undefined
  }
  const rel = clean.replace(/^\.\//, "").replace(/\/+$/, "")
  return rel === "." ? "" : rel
}

/**
 * What each service takes from the stack's directory: the folder it is built
 * from, the folders and files mounted into it, and the env files it reads.
 * Read from the file's lines rather than a YAML parse, for the short and
 * long forms compose files are written in; anything stranger is left
 * unsaid rather than guessed.
 */
export function fileRoles(text: string): FileRole[] {
  const out: FileRole[] = []
  const services = composeOutline(text).find((s) => s.key === "services")
  if (!services) return out
  const lines = text.split("\n")
  services.children.forEach((service, index) => {
    const end = services.children[index + 1]?.line ?? sectionEnd(lines, services.line)
    const block = lines.slice(service.line, end - 1)
    let list: "volumes" | "env_file" | "build" | undefined
    let listIndent = -1
    for (const raw of block) {
      const body = raw.trim()
      if (!body || body.startsWith("#")) continue
      const indent = raw.length - raw.trimStart().length
      // A list may sit at its key's own indentation, as long as each item is one.
      if (list && (indent < listIndent || (indent === listIndent && !body.startsWith("-")))) {
        list = undefined
      }
      const key = /^([a-z_]+):\s*(.*)$/.exec(body)
      if (!list && key) {
        const [, name, value] = key
        if (name === "build" || name === "volumes" || name === "env_file") {
          if (value && !value.startsWith("[")) {
            const path = own(value)
            if (path !== undefined && name === "build") {
              out.push({ path, service: service.key, role: "build" })
            }
            if (path !== undefined && name === "env_file") {
              out.push({ path, service: service.key, role: "env" })
            }
          } else if (value.startsWith("[")) {
            for (const item of value.slice(1, -1).split(",")) {
              push(out, name, item, service.key)
            }
          } else {
            list = name
            listIndent = indent
          }
        }
        continue
      }
      if (!list) continue
      const item = /^-\s*(.*)$/.exec(body)?.[1]
      const field = /^(?:-\s*)?([a-z_]+):\s*(.+)$/.exec(body)
      if (list === "build" && field?.[1] === "context") {
        const path = own(field[2])
        if (path !== undefined) out.push({ path, service: service.key, role: "build" })
      } else if (list === "volumes" && field?.[1] === "source" && field[2].startsWith(".")) {
        // A bind's source is a path compose wants written with its dot; a
        // named volume's is a name.
        const path = own(field[2])
        if (path !== undefined) out.push({ path, service: service.key, role: "mount" })
      } else if (list === "volumes" && field?.[1] === "target") {
        const last = out.at(-1)
        if (last?.service === service.key && last.role === "mount" && !last.target) {
          last.target = field[2].trim()
        }
      } else if (item && !field) {
        push(out, list, item, service.key)
      } else if (list === "env_file" && field?.[1] === "path") {
        const path = own(field[2])
        if (path !== undefined) out.push({ path, service: service.key, role: "env" })
      }
    }
  })
  return out
}

function push(out: FileRole[], list: string, item: string, service: string) {
  const value = item.trim().replace(/^["']|["']$/g, "")
  if (list === "env_file") {
    const path = own(value)
    if (path !== undefined) out.push({ path, service, role: "env" })
    return
  }
  if (list !== "volumes") return
  // `./nginx:/etc/nginx/conf.d:ro` — a named volume has no path to begin with.
  const [source, target] = value.split(":")
  if (!source.startsWith(".")) return
  const path = own(source)
  if (path !== undefined) out.push({ path, service, role: "mount", target })
}

/** The line after the last one indented under the section starting at `start`. */
function sectionEnd(lines: string[], start: number) {
  for (let i = start; i < lines.length; i++) {
    const line = lines[i]
    if (line.trim() && !line.startsWith(" ") && !line.startsWith("#")) return i + 1
  }
  return lines.length + 1
}

/* ------------------------------------------------------------- history -- */

/** What a recorded action did, as the past tense the list reads in. */
export const DONE: Record<string, string> = {
  up: "Deployed",
  update: "Pulled and redeployed",
  restart: "Restarted",
  recreate: "Recreated",
  stop: "Stopped",
  start: "Started",
  down: "Stopped and removed",
}

export function doneWord(action: string | undefined) {
  return DONE[action ?? "up"] ?? (action ? action[0].toUpperCase() + action.slice(1) : "Deployed")
}

/**
 * Each configuration's colour, so two deployments of the same file are seen
 * to be the same before their hashes are read: the lanes in the order the
 * files first appear, oldest first, so the versions a history holds never
 * share one — a hash's own hue would put two of three on the same colour.
 * The lanes, not every hue: a red file would read as a failed one.
 */
export function configHues(records: Pick<StackDeployment, "configHash" | "createdAt">[]) {
  const out = new Map<string, string>()
  const ordered = [...records].sort((a, b) => Date.parse(a.createdAt) - Date.parse(b.createdAt))
  for (const record of ordered) {
    if (!out.has(record.configHash)) out.set(record.configHash, LANES[out.size % LANES.length])
  }
  return out
}

export type HistorySpan = {
  id: number
  hash: string
  /** Where the span starts and how far it runs, as fractions of the strip. */
  start: number
  width: number
  /** The file was not the one the record before this used. */
  changed: boolean
}

/**
 * The records as spans of time, oldest on the left: each runs from its
 * moment to the next one's, and the newest runs to now. A span is the file
 * that action used, so the strip reads as which configuration was live when.
 */
export function historySpans(records: StackDeployment[], now: number): HistorySpan[] {
  const ordered = [...records].sort((a, b) => Date.parse(a.createdAt) - Date.parse(b.createdAt))
  if (ordered.length === 0) return []
  const first = Date.parse(ordered[0].createdAt)
  const total = Math.max(now - first, 1)
  return ordered.map((record, i) => {
    const from = Date.parse(record.createdAt)
    const to = i + 1 < ordered.length ? Date.parse(ordered[i + 1].createdAt) : now
    return {
      id: record.id,
      hash: record.configHash,
      start: (from - first) / total,
      width: Math.max(to - from, 0) / total,
      changed: i > 0 && ordered[i - 1].configHash !== record.configHash,
    }
  })
}

/** Whether a record's file is the one on disk now, ignoring trailing whitespace as the hash does. */
export function sameFile(a: string | undefined, b: string | undefined) {
  return a !== undefined && b !== undefined && a.replace(/\s+$/, "") === b.replace(/\s+$/, "")
}

/** A digest as a reader can tell two apart: the repository, then the hash's head and tail. */
export function shortDigest(digest: string) {
  const at = digest.indexOf("@")
  const repo = at > 0 ? digest.slice(0, at) : ""
  const hash = (at > 0 ? digest.slice(at + 1) : digest).replace(/^sha256:/, "")
  return { repo, hash: hash.length > 16 ? `${hash.slice(0, 12)}…${hash.slice(-4)}` : hash }
}
