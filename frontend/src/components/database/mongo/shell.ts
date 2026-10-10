/**
 * The shape of a piece of query text, read without deciding what it means.
 *
 * The server takes a filter, a sort or a pipeline as text in either of two
 * spellings — Extended JSON, or the shell's (`{ age: { $gt: 30 } }`,
 * `ObjectId("…")`, `/^a/i`, comments, trailing commas) — and it is the one
 * that parses it. What a page needs before the text is sent is smaller: is
 * this one whole value, where does each stage of a pipeline begin and end,
 * is the filter empty. This reads exactly that: brackets, strings, keys and
 * commas, and nothing about types. Anything it cannot place is left for the
 * server to judge, so it never refuses a spelling the server would accept.
 */

export type Span =
  | { kind: "document"; start: number; end: number; fields: SpanField[] }
  | { kind: "array"; start: number; end: number; items: Span[] }
  | { kind: "value"; start: number; end: number }

export type SpanField = {
  /** The key as written, without its quotes. */
  key: string
  start: number
  value: Span
}

export type ScanError = {
  message: string
  offset: number
  /** Both from 1, as the server's own messages count them. */
  line: number
  column: number
}

export type Scan = { ok: true; value: Span } | { ok: false; error: ScanError }

class Refusal extends Error {
  constructor(
    message: string,
    readonly offset: number,
  ) {
    super(message)
  }
}

const CLOSERS: Record<string, string> = { "{": "}", "[": "]", "(": ")" }

/** Where an offset sits in the text, counted the way the server counts it. */
export function positionOf(text: string, offset: number): { line: number; column: number } {
  let line = 1
  let column = 1
  for (let i = 0; i < offset && i < text.length; i++) {
    if (text[i] === "\n") {
      line++
      column = 1
    } else column++
  }
  return { line, column }
}

/** "unexpected '}' where a value was expected", or — at the end — "the text ends where …". */
const unexpected = (char: string | undefined, rest: string) =>
  char === undefined ? `the text ends ${rest}` : `unexpected '${char}' ${rest}`

/** One whole value, with an optional `;` after it. */
export function scan(text: string): Scan {
  let i = 0

  const space = () => {
    for (;;) {
      const char = text[i]
      if (char === " " || char === "\t" || char === "\n" || char === "\r") i++
      else if (char === "/" && text[i + 1] === "/") {
        while (i < text.length && text[i] !== "\n") i++
      } else if (char === "/" && text[i + 1] === "*") {
        const close = text.indexOf("*/", i + 2)
        if (close < 0) throw new Refusal("a comment that is never closed", i)
        i = close + 2
      } else return
    }
  }

  const quoted = () => {
    const quote = text[i]
    const start = i
    i++
    while (i < text.length) {
      if (text[i] === "\\") i += 2
      else if (text[i] === quote) {
        i++
        return
      } else i++
    }
    throw new Refusal("a string that is never closed", start)
  }

  const regex = () => {
    const start = i
    i++
    let inClass = false
    while (i < text.length) {
      const char = text[i]
      if (char === "\\") i += 2
      else if (char === "\n") break
      else if (char === "[") {
        inClass = true
        i++
      } else if (char === "]") {
        inClass = false
        i++
      } else if (char === "/" && !inClass) {
        i++
        while (/[a-z]/i.test(text[i] ?? "")) i++
        return
      } else i++
    }
    throw new Refusal("a pattern that is never closed", start)
  }

  const atom = () => {
    const start = i
    while (i < text.length && !/[\s,:{}[\]()'"]/.test(text[i])) i++
    return text.slice(start, i)
  }

  const list = (open: string, each: () => void) => {
    const start = i
    const close = CLOSERS[open]
    i++
    for (;;) {
      space()
      if (text[i] === close) {
        i++
        return
      }
      if (i >= text.length) throw new Refusal(`a '${open}' that is never closed`, start)
      each()
      space()
      if (text[i] === ",") i++
      else if (text[i] !== close) {
        throw new Refusal(
          i >= text.length
            ? `a '${open}' that is never closed`
            : unexpected(text[i], `where ',' or '${close}' was expected`),
          i >= text.length ? start : i,
        )
      }
    }
  }

  const value = (): Span => {
    space()
    const start = i
    const char = text[i]
    if (char === "{") {
      const fields: SpanField[] = []
      list("{", () => {
        const keyStart = i
        let key: string
        if (text[i] === '"' || text[i] === "'") {
          quoted()
          key = unquote(text.slice(keyStart, i))
        } else {
          key = atom()
          if (!key) {
            throw new Refusal(unexpected(text[i], "where a field name was expected"), i)
          }
        }
        space()
        if (text[i] !== ":") {
          throw new Refusal(unexpected(text[i], "where ':' was expected"), i)
        }
        i++
        fields.push({ key, start: keyStart, value: value() })
      })
      return { kind: "document", start, end: i, fields }
    }
    if (char === "[") {
      const items: Span[] = []
      list("[", () => items.push(value()))
      return { kind: "array", start, end: i, items }
    }
    if (char === '"' || char === "'") {
      quoted()
      return { kind: "value", start, end: i }
    }
    if (char === "/") {
      regex()
      return { kind: "value", start, end: i }
    }
    let word = atom()
    if (!word) throw new Refusal(unexpected(char, "where a value was expected"), i)
    if (word === "new") {
      space()
      word = atom()
      if (!word) throw new Refusal(unexpected(text[i], "after 'new'"), i)
    }
    // A constructor's arguments belong to the value: ObjectId("…"), Timestamp(1, 2).
    const mark = i
    space()
    if (text[i] === "(") list("(", () => void value())
    else i = mark
    return { kind: "value", start, end: i }
  }

  try {
    const read = value()
    space()
    if (text[i] === ";") i++
    space()
    if (i < text.length) {
      throw new Refusal(unexpected(text[i], "after the end of the value"), i)
    }
    return { ok: true, value: read }
  } catch (err) {
    if (!(err instanceof Refusal)) throw err
    return {
      ok: false,
      error: { message: err.message, offset: err.offset, ...positionOf(text, err.offset) },
    }
  }
}

/** A quoted key as the name it spells. */
function unquote(literal: string): string {
  if (literal[0] === '"') {
    try {
      return JSON.parse(literal) as string
    } catch {
      // Not JSON's escapes: the shell's, read below.
    }
  }
  return literal.slice(1, -1).replace(/\\(.)/g, "$1")
}

export type TextShape = "document" | "list" | "document or list" | "value"

const SHAPE_WORDS: Record<TextShape, string> = {
  document: "a document: { … }",
  list: "a list: [ … ]",
  "document or list": "a document { … } or a list [ … ]",
  value: "a value",
}

/**
 * What is wrong with a field's text, as one sentence, or `null` when it reads
 * as one whole value of the shape the field takes. Empty text is "none" and
 * is never wrong here: a field that must be filled says so itself.
 */
export function shapeProblem(text: string, shape: TextShape = "document"): string | null {
  if (!text.trim()) return null
  const read = scan(text)
  if (!read.ok) {
    return `Line ${read.error.line}, column ${read.error.column}: ${read.error.message}.`
  }
  const kind = read.value.kind
  const fits =
    shape === "value" ||
    (shape === "document" && kind === "document") ||
    (shape === "list" && kind === "array") ||
    (shape === "document or list" && kind !== "value")
  return fits ? null : `Write ${SHAPE_WORDS[shape]}.`
}

/** Whether the text is no document at all, or one with no field: a filter that matches everything. */
export function isEmptyDocument(text: string): boolean {
  if (!text.trim()) return true
  const read = scan(text)
  return read.ok && read.value.kind === "document" && read.value.fields.length === 0
}

/**
 * A filter with one more condition. The two are joined with `$and` rather
 * than by writing the new field into the old text: the filter may already
 * name that field, and a document that says a key twice keeps only one of them.
 */
export function mergeFilter(existing: string, clause: string): string {
  if (isEmptyDocument(existing)) return clause
  const text = existing.trim().replace(/;\s*$/, "")
  if (text === clause) return clause
  const read = scan(text)
  // Already a list of conditions made here: the new one joins it.
  if (
    read.ok &&
    read.value.kind === "document" &&
    read.value.fields.length === 1 &&
    read.value.fields[0].key === "$and" &&
    read.value.fields[0].value.kind === "array"
  ) {
    const list = read.value.fields[0].value
    if (list.items.some((item) => text.slice(item.start, item.end) === clause)) return text
    const inner = text
      .slice(list.start + 1, list.end - 1)
      .trim()
      .replace(/,\s*$/, "")
    return `{ "$and": [${inner}, ${clause}] }`
  }
  return `{ "$and": [${text}, ${clause}] }`
}

/** The text of a span, as it was written. */
export function textOf(text: string, span: Span): string {
  return text.slice(span.start, span.end)
}
