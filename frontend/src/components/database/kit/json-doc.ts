import { plural } from "@/lib/format"

/**
 * A JSON document read as a tree without being turned into JavaScript values,
 * and changed one node at a time by rewriting only that node's characters.
 *
 * The tree a reader edits a `jsonb` field or a RedisJSON key through must not
 * be `JSON.parse`'s: that reads `9007199254740993` as …992, moves the key
 * `"10"` ahead of `"b"` and keeps one of two keys that share a name — and
 * writing the document back after one field was changed would write all of
 * that with it. So a node here is where it stands in the text (`start`,
 * `end`), a number is the digits it was written with, an object is its pairs
 * in their order, and every edit is a splice: the rest of the document goes
 * back to the database character for character as it came.
 *
 * The one parser serves both editors. The data page's field edits by span;
 * the Redis key editor builds new nodes and prints them (`printJsonDoc`), and
 * reads only the span-free shape (`BareJsonNode`) every parsed node also is.
 */

export type JsonNode =
  | { kind: "object"; start: number; end: number; entries: JsonEntry[] }
  | { kind: "array"; start: number; end: number; items: JsonNode[] }
  | { kind: "string"; start: number; end: number; value: string }
  | { kind: "number"; start: number; end: number; raw: string }
  | { kind: "boolean"; start: number; end: number; value: boolean }
  | { kind: "null"; start: number; end: number }

export interface JsonEntry {
  key: string
  /** Where the key's own token stands, quotes included. */
  keyStart: number
  keyEnd: number
  value: JsonNode
}

/**
 * A node without its place in the text: what a node built in an editor is
 * before it is printed, and what every parsed `JsonNode` also is.
 */
export type BareJsonNode =
  | { kind: "object"; entries: { key: string; value: BareJsonNode }[] }
  | { kind: "array"; items: BareJsonNode[] }
  | { kind: "string"; value: string }
  | { kind: "number"; raw: string }
  | { kind: "boolean"; value: boolean }
  | { kind: "null" }

const WHITESPACE = " \t\n\r"

/** The document as a tree of spans, or null when the text is not one JSON value. */
export function parseJsonDoc(text: string): JsonNode | null {
  try {
    // The only question `JSON.parse` is asked: whether this is JSON at all.
    // The walk below then reads a text it knows to be well formed.
    JSON.parse(text)
  } catch {
    return null
  }
  let at = 0
  const skip = () => {
    while (at < text.length && WHITESPACE.includes(text[at])) at++
  }
  const string = (): { start: number; end: number; value: string } => {
    const start = at
    at++
    // A backslash takes the character after it, so `\"` does not end the string.
    while (text[at] !== '"') at += text[at] === "\\" ? 2 : 1
    at++
    return { start, end: at, value: JSON.parse(text.slice(start, at)) as string }
  }
  const value = (): JsonNode => {
    skip()
    const start = at
    const char = text[at]
    if (char === "{") {
      at++
      const entries: JsonEntry[] = []
      skip()
      while (text[at] !== "}") {
        skip()
        const key = string()
        skip()
        at++ // the colon
        const held = value()
        entries.push({ key: key.value, keyStart: key.start, keyEnd: key.end, value: held })
        skip()
        if (text[at] === ",") at++
        skip()
      }
      at++
      return { kind: "object", start, end: at, entries }
    }
    if (char === "[") {
      at++
      const items: JsonNode[] = []
      skip()
      while (text[at] !== "]") {
        items.push(value())
        skip()
        if (text[at] === ",") at++
        skip()
      }
      at++
      return { kind: "array", start, end: at, items }
    }
    if (char === '"') {
      const read = string()
      return { kind: "string", ...read }
    }
    while (at < text.length && !WHITESPACE.includes(text[at]) && !",]}".includes(text[at])) at++
    const raw = text.slice(start, at)
    if (raw === "true" || raw === "false") {
      return { kind: "boolean", start, end: at, value: raw === "true" }
    }
    if (raw === "null") return { kind: "null", start, end: at }
    return { kind: "number", start, end: at, raw }
  }
  return value()
}

const NUMBER = /^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$/

/** Whether a text is a JSON number, `true`, `false` or `null` as written. */
function isBareScalar(text: string): boolean {
  return NUMBER.test(text) || text === "true" || text === "false" || text === "null"
}

/**
 * What a node's editor opens on. A number, a true-or-false and a null are
 * their own literal. A string is its bare content — unless bare it would read
 * as something else (`"42"`, `"null"`, a string that starts with a quote or a
 * bracket, or is nothing but spaces), in which case it keeps its quotes, so a
 * field opened and left alone is still the string it was.
 */
export function scalarText(node: JsonNode, text: string): string {
  if (node.kind !== "string") return text.slice(node.start, node.end)
  const content = node.value
  const plain =
    content !== "" &&
    content === content.trim() &&
    !isBareScalar(content) &&
    !'"[{'.includes(content[0]) &&
    !/[\n\r\t]/.test(content)
  return plain ? content : text.slice(node.start, node.end)
}

/**
 * The literal a typed text stands for, by one rule: what is written as a
 * number, `true`, `false`, `null`, a quoted string or a whole object or array
 * is that; anything else is a string of exactly what was typed. The digits of
 * a number are kept as typed — they are never read into a float.
 */
export function scalarLiteral(
  input: string,
): { ok: true; literal: string } | { ok: false; error: string } {
  const typed = input.trim()
  if (isBareScalar(typed)) return { ok: true, literal: typed }
  if (typed.startsWith('"') || typed.startsWith("{") || typed.startsWith("[")) {
    try {
      JSON.parse(typed)
      return { ok: true, literal: typed }
    } catch {
      return {
        ok: false,
        error: typed.startsWith('"')
          ? "A quoted string has to close, with a backslash before a quote inside it"
          : "Not valid JSON",
      }
    }
  }
  return { ok: true, literal: JSON.stringify(input) }
}

/** The document with one node written over. */
export function replaceNode(text: string, node: JsonNode, literal: string): string {
  return text.slice(0, node.start) + literal + text.slice(node.end)
}

/** Where each child of a container starts and ends, key included. */
function childSpans(parent: JsonNode): { start: number; end: number }[] {
  if (parent.kind === "object") {
    return parent.entries.map((entry) => ({ start: entry.keyStart, end: entry.value.end }))
  }
  if (parent.kind === "array") return parent.items.map(({ start, end }) => ({ start, end }))
  return []
}

/** The document without the `index`-th child of an object or an array, and without its comma. */
export function removeChild(text: string, parent: JsonNode, index: number): string {
  const spans = childSpans(parent)
  const span = spans[index]
  if (!span) return text
  // Not the last: it goes with the comma after it, up to the next child.
  if (index < spans.length - 1) {
    return text.slice(0, span.start) + text.slice(spans[index + 1].start)
  }
  // The last of several: it goes with the comma before it.
  if (index > 0) return text.slice(0, spans[index - 1].end) + text.slice(span.end)
  // The only one: the container is left empty.
  return text.slice(0, parent.start + 1) + text.slice(parent.end - 1)
}

/**
 * The document with a child added at the end of an object (`key` given) or an
 * array. A key the object already has is refused: two keys of one name is a
 * document only one of whose values is ever read.
 */
export function addChild(
  text: string,
  parent: JsonNode,
  literal: string,
  key?: string,
): { ok: true; text: string } | { ok: false; error: string } {
  if (parent.kind !== "object" && parent.kind !== "array") {
    return { ok: false, error: "Only an object or an array holds other values" }
  }
  if (parent.kind === "object") {
    if (key === undefined) return { ok: false, error: "A value in an object needs a key" }
    if (parent.entries.some((entry) => entry.key === key)) {
      return { ok: false, error: `This object already has a key called ${key}` }
    }
  }
  const spans = childSpans(parent)
  const piece = parent.kind === "object" ? `${JSON.stringify(key)}: ${literal}` : literal
  if (spans.length === 0) {
    return {
      ok: true,
      text: text.slice(0, parent.start + 1) + piece + text.slice(parent.end - 1),
    }
  }
  const last = spans[spans.length - 1].end
  return { ok: true, text: `${text.slice(0, last)}, ${piece}${text.slice(last)}` }
}

/**
 * The document with the `index`-th key of an object given another name. Only
 * the key's own token is rewritten: its value, and where the entry stands
 * among the others, are untouched. A name another entry of the object already
 * has is refused, for the reason a new key is.
 */
export function renameKey(
  text: string,
  parent: JsonNode,
  index: number,
  key: string,
): { ok: true; text: string } | { ok: false; error: string } {
  if (parent.kind !== "object") return { ok: false, error: "Only an object has keys" }
  const entry = parent.entries[index]
  if (!entry) return { ok: true, text }
  if (key === "") return { ok: false, error: "A key needs a name" }
  if (parent.entries.some((other, at) => at !== index && other.key === key)) {
    return { ok: false, error: `This object already has a key called ${key}` }
  }
  if (key === entry.key) return { ok: true, text }
  return {
    ok: true,
    text: text.slice(0, entry.keyStart) + JSON.stringify(key) + text.slice(entry.keyEnd),
  }
}

/**
 * The document with the `from`-th child of a container moved to stand `to`-th.
 * The children change places and nothing else does: the commas, the spaces and
 * the line breaks between them stay where they were, so a document laid out
 * one item a line is still laid out so.
 */
export function moveChild(text: string, parent: JsonNode, from: number, to: number): string {
  const spans = childSpans(parent)
  if (!spans[from] || !spans[to] || from === to) return text
  const pieces = spans.map((span) => text.slice(span.start, span.end))
  const [moved] = pieces.splice(from, 1)
  pieces.splice(to, 0, moved)
  let out = text.slice(0, spans[0].start)
  pieces.forEach((piece, at) => {
    out += piece
    out += text.slice(spans[at].end, at + 1 < spans.length ? spans[at + 1].start : parent.end)
  })
  return out + text.slice(parent.end)
}

/**
 * The document as text. With `indent` it is laid out a level per line, as
 * `JSON.stringify(value, null, indent)` would; without, it is compact. A
 * number comes out as the digits it went in as.
 */
export function printJsonDoc(node: BareJsonNode, indent = 0, depth = 0): string {
  const line = indent ? `\n${" ".repeat(indent * (depth + 1))}` : ""
  const close = indent ? `\n${" ".repeat(indent * depth)}` : ""
  const colon = indent ? ": " : ":"
  switch (node.kind) {
    case "object":
      if (node.entries.length === 0) return "{}"
      return `{${node.entries
        .map(
          (entry) =>
            `${line}${JSON.stringify(entry.key)}${colon}${printJsonDoc(entry.value, indent, depth + 1)}`,
        )
        .join(",")}${close}}`
    case "array":
      if (node.items.length === 0) return "[]"
      return `[${node.items.map((item) => `${line}${printJsonDoc(item, indent, depth + 1)}`).join(",")}${close}]`
    case "string":
      return JSON.stringify(node.value)
    case "number":
      return node.raw
    case "boolean":
      return String(node.value)
    case "null":
      return "null"
  }
}

/**
 * The same document with other spacing: laid out a level per line with
 * `indent`, or compact without. Only the space between tokens is touched —
 * every string, number and name is copied as it was written, so `1.10` stays
 * `1.10`, `1e3` stays `1e3`, a twenty-digit integer keeps its digits and an
 * escape stays the escape it was. `null` when the text is not one JSON value.
 */
export function respaceJson(text: string, indent = 0): string | null {
  try {
    JSON.parse(text)
  } catch {
    return null
  }
  let out = ""
  let depth = 0
  let at = 0
  const line = () => (indent ? `\n${" ".repeat(indent * depth)}` : "")
  while (at < text.length) {
    const ch = text[at]
    if (WHITESPACE.includes(ch)) {
      at++
    } else if (ch === '"') {
      const start = at
      at++
      while (text[at] !== '"') at += text[at] === "\\" ? 2 : 1
      at++
      out += text.slice(start, at)
    } else if (ch === "{" || ch === "[") {
      let next = at + 1
      while (WHITESPACE.includes(text[next])) next++
      if (text[next] === "}" || text[next] === "]") {
        // An empty container stays on its line, as `JSON.stringify` writes it.
        out += ch + text[next]
        at = next + 1
      } else {
        depth++
        out += ch + line()
        at++
      }
    } else if (ch === "}" || ch === "]") {
      depth--
      out += line() + ch
      at++
    } else if (ch === ",") {
      out += `,${line()}`
      at++
    } else if (ch === ":") {
      out += indent ? ": " : ":"
      at++
    } else {
      const start = at
      while (at < text.length && !WHITESPACE.includes(text[at]) && !",]}".includes(text[at])) at++
      out += text.slice(start, at)
    }
  }
  return out
}

/** The node under a run of keys and positions, or `undefined` where the path leads nowhere. */
export function nodeAt(
  node: BareJsonNode,
  path: readonly (string | number)[],
): BareJsonNode | undefined {
  let at: BareJsonNode | undefined = node
  for (const segment of path) {
    if (!at) return undefined
    if (at.kind === "object" && typeof segment === "string") {
      at = at.entries.find((entry) => entry.key === segment)?.value
    } else if (at.kind === "array" && typeof segment === "number") {
      at = at.items[segment]
    } else {
      return undefined
    }
  }
  return at
}

/** How many pairs or items a container holds, as its closed row says it: "3 keys", "1 item". */
export function nodeSize(node: BareJsonNode): string {
  if (node.kind === "object") return plural(node.entries.length, "key")
  if (node.kind === "array") return plural(node.items.length, "item")
  return ""
}

/**
 * Whether a JSONPath names exactly one place: `$`, then only member names and
 * positions. A path with a wildcard, a filter, a slice or `..` matches a set
 * of places, and what is under one of its matches has no address to write to.
 */
export function definitePath(path: string): boolean {
  return /^\$(?:\.[A-Za-z_$][\w$]*|\[\d+\]|\["(?:[^"\\]|\\.)*"\])*$/.test(path.trim())
}
