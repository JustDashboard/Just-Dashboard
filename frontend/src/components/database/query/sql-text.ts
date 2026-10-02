import type { Dialect } from "@/components/database/query/dialect"

/**
 * SQL read as text, far enough to find a statement: its words, its quoted
 * names, its texts and its comments, and the semicolons that are none of
 * those.
 *
 * This is the editor's reading, used to pick the statement under the cursor,
 * to mark it, to lay it out and to complete a name. It is not what decides
 * what runs: the text chosen here is sent to the server, which splits and
 * judges it itself.
 */

export type TokenKind =
  | "space"
  | "word"
  | "number"
  | "text"
  | "name"
  | "comment"
  | "body"
  | "semicolon"
  | "open"
  | "close"
  | "comma"
  | "dot"
  | "operator"

export interface Token {
  kind: TokenKind
  start: number
  end: number
}

const WORD_START = /[A-Za-z_\u0080-￿]/
const WORD_PART = /[A-Za-z0-9_$#@\u0080-￿]/
const DIGIT = /[0-9]/
const OPERATOR = /[-+*/<>=!~|&%^:?@]/
const SINGLE: Partial<Record<string, TokenKind>> = {
  ";": "semicolon",
  "(": "open",
  ")": "close",
  ",": "comma",
  ".": "dot",
}

/** Where a quoted run ends: past its closing mark, a doubled mark being the mark itself. */
function quotedEnd(text: string, from: number, close: string, backslash: boolean): number {
  let at = from
  while (at < text.length) {
    const ch = text[at]
    if (backslash && ch === "\\") {
      at += 2
      continue
    }
    if (ch === close) {
      if (text[at + 1] === close) {
        at += 2
        continue
      }
      return at + 1
    }
    at++
  }
  return text.length
}

/** The text as tokens, end to end: every character belongs to exactly one. */
export function tokenize(text: string, dialect: Dialect): Token[] {
  const tokens: Token[] = []
  const push = (kind: TokenKind, start: number, end: number) => tokens.push({ kind, start, end })
  const nameQuotes = [dialect.quote, ...dialect.alsoQuotes].filter(
    ([open]) => !(dialect.doubleQuotedText && open === '"'),
  )
  let at = 0
  while (at < text.length) {
    const ch = text[at]
    const next = text[at + 1]

    if (/\s/.test(ch)) {
      let end = at + 1
      while (end < text.length && /\s/.test(text[end])) end++
      push("space", at, end)
      at = end
      continue
    }
    if ((ch === "-" && next === "-") || (dialect.hashComments && ch === "#")) {
      let end = text.indexOf("\n", at)
      if (end < 0) end = text.length
      push("comment", at, end)
      at = end
      continue
    }
    if (ch === "/" && next === "*") {
      const close = text.indexOf("*/", at + 2)
      const end = close < 0 ? text.length : close + 2
      push("comment", at, end)
      at = end
      continue
    }
    if (ch === "'" || (dialect.doubleQuotedText && ch === '"')) {
      // An `E'…'` text escapes with a backslash whatever plain texts do.
      const before = tokens[tokens.length - 1]
      const escaped =
        dialect.backslashEscapes ||
        (dialect.escapeStrings &&
          before?.kind === "word" &&
          /^e$/i.test(text.slice(before.start, before.end)))
      const end = quotedEnd(text, at + 1, ch, escaped)
      push("text", at, end)
      at = end
      continue
    }
    const quote = nameQuotes.find(([open]) => open === ch)
    if (quote) {
      const end = quotedEnd(text, at + 1, quote[1], false)
      push("name", at, end)
      at = end
      continue
    }
    if (dialect.dollarQuoting && ch === "$") {
      const tag = /^\$([A-Za-z_][A-Za-z0-9_]*)?\$/.exec(text.slice(at, at + 66))
      if (tag) {
        const close = text.indexOf(tag[0], at + tag[0].length)
        const end = close < 0 ? text.length : close + tag[0].length
        push("body", at, end)
        at = end
        continue
      }
    }
    if (WORD_START.test(ch)) {
      let end = at + 1
      while (
        end < text.length &&
        WORD_PART.test(text[end]) &&
        !(dialect.hashComments && text[end] === "#")
      )
        end++
      push("word", at, end)
      at = end
      continue
    }
    if (DIGIT.test(ch) || (ch === "." && next !== undefined && DIGIT.test(next))) {
      let end = at + 1
      while (end < text.length && /[0-9A-Za-z_.]/.test(text[end])) end++
      push("number", at, end)
      at = end
      continue
    }
    const kind = SINGLE[ch]
    if (kind) {
      push(kind, at, at + 1)
      at++
      continue
    }
    // A run of operator characters is one operator: `<=`, `::`, `->>`. It
    // stops where a comment opens, which is not part of any operator.
    let end = at + 1
    if (OPERATOR.test(ch)) {
      while (
        end < text.length &&
        OPERATOR.test(text[end]) &&
        !(text[end] === "-" && text[end + 1] === "-") &&
        !(text[end] === "/" && text[end + 1] === "*")
      )
        end++
    }
    push("operator", at, end)
    at = end
  }
  return tokens
}

/** One statement of a text, by where it sits in it. */
export interface StatementSpan {
  /** Its first character that is not space or a leading comment. */
  start: number
  /** Past its last character, the closing semicolon not included. */
  end: number
  /** The reach of the statement for a cursor: from the end of the one before to its own semicolon, inclusive. */
  from: number
  to: number
  /** 1-based line of `start`. */
  line: number
  text: string
}

const lineOf = (text: string, offset: number) => {
  let line = 1
  for (let at = text.indexOf("\n"); at >= 0 && at < offset; at = text.indexOf("\n", at + 1)) line++
  return line
}

/**
 * The statements of a text, split at the semicolons that are outside every
 * text, name, comment and dollar-quoted body. A stretch that holds nothing
 * but space and comments is not a statement.
 */
export function splitStatements(text: string, dialect: Dialect): StatementSpan[] {
  const tokens = tokenize(text, dialect)
  const spans: StatementSpan[] = []
  let from = 0
  let first = -1
  let last = -1
  const close = (to: number) => {
    if (first >= 0) {
      spans.push({
        start: first,
        end: last,
        from,
        to,
        line: lineOf(text, first),
        text: text.slice(first, last),
      })
    }
    from = to
    first = -1
    last = -1
  }
  for (const token of tokens) {
    if (token.kind === "semicolon") {
      close(token.end)
      continue
    }
    if (token.kind === "space") continue
    // A comment before the first word or after the last is not the statement; one inside it is kept.
    if (token.kind === "comment" && first < 0) continue
    if (first < 0) first = token.start
    if (token.kind !== "comment") last = token.end
  }
  close(text.length)
  // The reach of the last statement runs to the end of the text.
  if (spans.length > 0) spans[spans.length - 1].to = text.length
  return spans
}

/**
 * The statement a cursor is on: the one whose reach holds the offset. A
 * cursor in the blank stretch after the last semicolon is on the statement
 * before it — where it rests after a statement has just been typed.
 */
export function statementAt(
  spans: readonly StatementSpan[],
  offset: number,
): StatementSpan | undefined {
  if (spans.length === 0) return undefined
  for (const span of spans) {
    if (offset >= span.from && offset <= span.to) {
      // A cursor on the line break after a semicolon belongs to what follows
      // only once it has passed that statement's own semicolon.
      return span
    }
  }
  // Past everything, or in a stretch that held only comments: the nearest one before.
  let before: StatementSpan | undefined
  for (const span of spans) if (span.to <= offset) before = span
  return before ?? spans[0]
}

export type RunScope = "selection" | "statement" | "all"

/** What a run sends, and where it sits in the text. */
export interface RunTarget {
  scope: RunScope
  sql: string
  /** Offset of `sql` in the editor's text. */
  offset: number
  /** 1-based line of that offset. */
  line: number
}

/**
 * What Run sends: the selection when there is one, else the statement the
 * cursor is on, else everything.
 */
export function runTarget(
  text: string,
  selection: { start: number; end: number } | null,
  cursor: number,
  dialect: Dialect,
  all = false,
): RunTarget | null {
  if (!all && selection && selection.end > selection.start) {
    const picked = text.slice(selection.start, selection.end)
    if (picked.trim()) {
      const lead = picked.length - picked.trimStart().length
      const offset = selection.start + lead
      return { scope: "selection", sql: picked.trim(), offset, line: lineOf(text, offset) }
    }
  }
  const spans = splitStatements(text, dialect)
  if (spans.length === 0) return null
  if (!all) {
    const span = statementAt(spans, cursor)
    if (span && spans.length > 1) {
      return { scope: "statement", sql: span.text, offset: span.start, line: span.line }
    }
  }
  const start = spans[0].start
  const end = spans[spans.length - 1].end
  return {
    scope: spans.length === 1 ? "statement" : "all",
    sql: text.slice(start, end),
    offset: start,
    line: lineOf(text, start),
  }
}

/** A statement's first line, for a list: its leading comments and line breaks left out, cut to length. */
export function firstLine(sql: string, max = 120): string {
  const flat = sql
    .replace(/^\s*(?:--[^\n]*\n\s*|\/\*[\s\S]*?\*\/\s*)*/, "")
    .replace(/\s+/g, " ")
    .trim()
  return flat.length > max ? `${flat.slice(0, max - 1)}…` : flat
}
