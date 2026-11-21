import { cleanPath, extensionOf, parentOf } from "@/components/files/media"

/**
 * How a text file reads as what it is, rather than as numbered lines.
 *
 * The Files preview carries a bounded head of a text file (`files.Preview`:
 * 64 KiB or 500 lines, whichever is first). Everything here works on that head
 * alone and on the name, so a two-gigabyte CSV costs what a two-line one does,
 * and a head that was cut short is drawn as far as it goes and said to be cut.
 */

export type TextView = "markdown" | "csv" | "tsv" | "json"

const MARKDOWN = new Set(["md", "mdx", "markdown"])

/**
 * Whether a README with no extension is written in Markdown: a heading, a
 * list, a fence or a link near the top. A plain-text README stays lines.
 */
export function readsAsMarkdown(text: string): boolean {
  const head = text.slice(0, 4096)
  return (
    /^ {0,3}#{1,6} \S/m.test(head) ||
    /^ {0,3}(```|~~~)/m.test(head) ||
    /\[[^\]\n]+\]\([^)\s]+\)/.test(head) ||
    /^ {0,3}[-*] \S[^\n]*\n {0,3}[-*] \S/m.test(head)
  )
}

/** The view a text file gets, or nothing for one that is lines and nothing else. */
export function textViewOf(name: string, text: string): TextView | null {
  const ext = extensionOf(name)
  if (MARKDOWN.has(ext)) return "markdown"
  if (ext === "csv") return "csv"
  if (ext === "tsv" || ext === "tab") return "tsv"
  if (ext === "json" || ext === "geojson" || ext === "webmanifest") {
    // Only JSON that arrived on one line needs indenting; one written by a
    // person already is, and their layout is the one to keep.
    return isOneLine(text) ? "json" : null
  }
  if (!ext && /^readme$/i.test(name) && readsAsMarkdown(text)) return "markdown"
  return null
}

function isOneLine(text: string): boolean {
  const body = text.replace(/\s+$/, "")
  return body.length > 0 && !body.includes("\n")
}

/**
 * JSON indented two spaces, by walking its characters rather than parsing it:
 * a head cut off at 64 KiB is not valid JSON, and its first part still reads
 * better indented. Strings are copied exactly, numbers are never re-printed
 * (a parse would round a 64-bit id), and what is there is all that is shown.
 */
export function indentJson(text: string): string {
  let out = ""
  let depth = 0
  let inString = false
  let escaped = false
  const pad = () => "\n" + "  ".repeat(depth)
  const source = text.replace(/\s+$/, "")
  for (let i = 0; i < source.length; i++) {
    const ch = source[i]
    if (inString) {
      out += ch
      if (escaped) escaped = false
      else if (ch === "\\") escaped = true
      else if (ch === '"') inString = false
      continue
    }
    if (ch === '"') {
      inString = true
      out += ch
    } else if (ch === "{" || ch === "[") {
      // An empty object or array stays on its line.
      const close = ch === "{" ? "}" : "]"
      let j = i + 1
      while (j < source.length && /\s/.test(source[j])) j++
      if (source[j] === close) {
        out += ch + close
        i = j
        continue
      }
      depth++
      out += ch + pad()
    } else if (ch === "}" || ch === "]") {
      depth = Math.max(0, depth - 1)
      out += pad() + ch
    } else if (ch === ",") {
      out += "," + pad()
    } else if (ch === ":") {
      out += ": "
    } else if (!/\s/.test(ch)) {
      out += ch
    }
  }
  return out + "\n"
}

export type Delimited = {
  header: string[]
  rows: string[][]
  /** Per column: every filled cell is a number, so the column is set to the right. */
  numeric: boolean[]
  /** The last row was dropped because the head ended inside it. */
  cut: boolean
}

const NUMBER = /^[-+]?(\d{1,3}(,\d{3})+|\d+)?(\.\d+)?([eE][-+]?\d+)?%?$/

function isNumber(cell: string): boolean {
  const value = cell.trim()
  return value !== "" && /\d/.test(value) && NUMBER.test(value)
}

/**
 * RFC 4180, as people write it: a quoted field may hold the delimiter, a
 * doubled quote and line breaks. `truncated` says the text is a head, so a
 * last row that did not end (no line break after it, or inside quotes) is
 * dropped rather than shown as if it were whole.
 */
export function parseDelimited(text: string, delimiter: string, truncated: boolean): Delimited {
  const records: string[][] = []
  let row: string[] = []
  let field = ""
  let quoted = false
  let i = 0
  const source = text.replace(/^﻿/, "")
  const endRow = () => {
    row.push(field)
    field = ""
    if (!(row.length === 1 && row[0] === "")) records.push(row)
    row = []
  }
  while (i < source.length) {
    const ch = source[i]
    if (quoted) {
      if (ch === '"') {
        if (source[i + 1] === '"') {
          field += '"'
          i += 2
          continue
        }
        quoted = false
      } else {
        field += ch
      }
      i++
      continue
    }
    if (ch === '"' && field === "") {
      quoted = true
    } else if (ch === delimiter) {
      row.push(field)
      field = ""
    } else if (ch === "\n" || ch === "\r") {
      if (ch === "\r" && source[i + 1] === "\n") i++
      endRow()
    } else {
      field += ch
    }
    i++
  }
  let cut = false
  const ended = /[\r\n]$/.test(source)
  if (quoted || (truncated && !ended)) {
    // The head stopped part of the way through this row.
    cut = row.length > 0 || field !== ""
    row = []
    field = ""
  } else if (row.length > 0 || field !== "") {
    endRow()
  }
  const [header = [], ...rows] = records
  const width = Math.max(header.length, ...rows.map((r) => r.length))
  const filled = (r: string[]) => Array.from({ length: width }, (_, c) => r[c] ?? "")
  const body = rows.map(filled)
  const numeric = Array.from({ length: width }, (_, c) => {
    const cells = body.map((r) => r[c]).filter((v) => v.trim() !== "")
    return cells.length > 0 && cells.every(isNumber)
  })
  return { header: filled(header), rows: body, numeric, cut }
}

/**
 * Where a reference written in a file points, as a path on this server: a
 * sibling, something under it, a parent's, or an absolute path. The query and
 * the fragment are not part of a file's name; a trailing slash says folder.
 */
export function resolveRef(
  fromFile: string,
  ref: string,
): { path: string; folder: boolean } | undefined {
  let value = ref.split("#")[0].split("?")[0]
  if (!value) return undefined
  try {
    value = decodeURIComponent(value)
  } catch {
    return undefined
  }
  const folder = value.endsWith("/")
  const parts = value.startsWith("/") ? [] : parentOf(fromFile).split("/").filter(Boolean)
  for (const part of value.split("/")) {
    if (!part || part === ".") continue
    if (part === "..") parts.pop()
    else parts.push(part)
  }
  return { path: cleanPath("/" + parts.join("/")), folder }
}

/**
 * A Monaco token type (`keyword.ts`, `string.quote.yaml`, `comment.line.sh`)
 * as one of the few hues a code block is drawn in. Anything else is the ink
 * the block is set in.
 */
export type TokenTone = "keyword" | "string" | "comment" | "number" | "type" | "tag" | "attribute"

export function tokenTone(type: string): TokenTone | null {
  const head = type.split(".")[0]
  switch (head) {
    case "keyword":
      return "keyword"
    case "string":
    case "regexp":
      return "string"
    case "comment":
      return "comment"
    case "number":
    case "constant":
      return "number"
    case "type":
      return "type"
    case "tag":
    case "metatag":
      return "tag"
    case "attribute":
    case "key":
    case "variable":
      return "attribute"
    default:
      return null
  }
}
