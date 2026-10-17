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
  /**
   * It holds statements of its own: `block` for a body its engine reads by
   * its blocks, and `plsql` and `routine` for an Oracle PL/SQL unit and a SQL
   * Server procedure, function or trigger, which their engines end at a
   * separator line rather than at their END.
   */
  body?: "block" | "plsql" | "routine"
  /**
   * Its separator did not end it: a PL/SQL unit with no `/` line after it
   * runs to the end of the text, and a SQL Server routine that went on past
   * its BEGIN … END took in what followed, up to the next GO.
   */
  runsOn?: boolean
}

/** A text read as a script. */
export interface Script {
  spans: StatementSpan[]
  /**
   * From the first thing the server reads to the last: the statements and
   * their client's separators — a DELIMITER line, a GO line with its count, a
   * `/` line. It is what Run everything sends.
   */
  reach: { start: number; end: number } | null
  /** A separator of the client's was read: a DELIMITER, GO or `/` line, or a chosen delimiter. */
  separators: boolean
}

const lineOf = (text: string, offset: number) => {
  let line = 1
  for (let at = text.indexOf("\n"); at >= 0 && at < offset; at = text.indexOf("\n", at + 1)) line++
  return line
}

/** A token the reader of a script looks at, or a delimiter a MySQL script chose with DELIMITER. */
type Item = { kind: TokenKind | "delimiter"; start: number; end: number }

const OPAQUE = new Set<TokenKind>(["text", "name", "body"])

/**
 * The tokens of `text` from `from` on, without space and comments and with
 * offsets into the whole text. A delimiter the script chose ends a statement
 * wherever it stands outside a text, a name and a comment, inside a word too
 * (`END$$`), so a run of code is cut where one stands.
 */
function codeItems(text: string, dialect: Dialect, from: number, delimiter: string): Item[] {
  const items: Item[] = []
  let run: Item[] = []
  const flush = () => {
    if (run.length === 0) return
    if (!delimiter) items.push(...run)
    else {
      const first = run[0].start
      const chunk = text.slice(first, run[run.length - 1].end)
      let cut = first
      const upTo = (to: number) => {
        for (const token of run) {
          const start = Math.max(token.start, cut)
          const end = Math.min(token.end, to)
          if (start < end) items.push({ kind: token.kind, start, end })
        }
      }
      for (
        let at = chunk.indexOf(delimiter);
        at >= 0;
        at = chunk.indexOf(delimiter, at + delimiter.length)
      ) {
        upTo(first + at)
        items.push({ kind: "delimiter", start: first + at, end: first + at + delimiter.length })
        cut = first + at + delimiter.length
      }
      upTo(first + chunk.length)
    }
    run = []
  }
  for (const token of tokenize(text.slice(from), dialect)) {
    const item = { kind: token.kind, start: from + token.start, end: from + token.end }
    if (token.kind === "space" || token.kind === "comment" || OPAQUE.has(token.kind)) {
      flush()
      if (OPAQUE.has(token.kind)) items.push(item)
    } else run.push(item)
  }
  flush()
  return items
}

/** Whether only spaces stand between the start of the line and `offset`. */
function lineStartsAt(text: string, offset: number): boolean {
  let at = offset - 1
  while (at >= 0 && (text[at] === " " || text[at] === "\t")) at--
  return at < 0 || text[at] === "\n"
}

/**
 * Where the line ends and the next begins, when only spaces — and, with
 * `comment`, a `--` comment — stand between `offset` and the end of its line.
 */
function lineEndsAt(
  text: string,
  offset: number,
  comment: boolean,
): { lineEnd: number; next: number } | null {
  let at = offset
  while (at < text.length && (text[at] === " " || text[at] === "\t")) at++
  if (at === text.length) return { lineEnd: at, next: at }
  if (text[at] === "\n") return { lineEnd: at, next: at + 1 }
  if (text[at] === "\r" && text[at + 1] === "\n") return { lineEnd: at, next: at + 2 }
  if (comment && text.startsWith("--", at)) {
    const nl = text.indexOf("\n", at)
    return nl < 0 ? { lineEnd: text.length, next: text.length } : { lineEnd: nl, next: nl + 1 }
  }
  return null
}

/** Words that, after BEGIN, make it a T-SQL statement rather than a block. */
const TSQL_NOT_BLOCKS = new Set(["tran", "transaction", "distributed", "dialog", "conversation"])

/**
 * The statements of a text, cut where the engine itself ends each: at the
 * semicolons outside every text, name, comment and dollar-quoted body, except
 * those inside the body of a statement that holds statements of its own, and
 * at its client's separators (`dialect.bodies`). A stretch that holds nothing
 * but space and comments is not a statement.
 *
 * This is the server's reading of the same engine (`dbx/statement_split.go`)
 * written again, rule for rule, and both are held to one file of scripts:
 * the statement Run sends from the cursor is one the server reads as one.
 */
export function readScript(text: string, dialect: Dialect): Script {
  const rules = dialect.bodies
  const spans: StatementSpan[] = []
  let reach: Script["reach"] = null
  let separators = false
  const extend = (start: number, end: number) => {
    reach = reach
      ? { start: Math.min(reach.start, start), end: Math.max(reach.end, end) }
      : { start, end }
  }

  let delimiter = ""
  let items = codeItems(text, dialect, 0, delimiter)
  let at = 0
  const resume = (next: number) => {
    items = codeItems(text, dialect, next, delimiter)
    at = 0
  }

  // The statement being read: its items, its first words outside
  // parentheses, how deep in parentheses it is and the blocks of its body
  // that are open, by the word that opened each.
  let from = 0
  let cur: Item[] = []
  let words: string[] = []
  let parens = 0
  let blocks: string[] = []
  // A SQL Server routine's outermost BEGIN … END has ended, and whether
  // anything has been read since.
  let closed = false
  let past = false

  const wordOf = (item: Item | undefined) =>
    item?.kind === "word" ? text.slice(item.start, item.end).toLowerCase() : ""
  const spelled = (item: Item | undefined, mark: string) =>
    item !== undefined && item.kind === "operator" && text.slice(item.start, item.end) === mark
  const lastWord = () => wordOf(cur[cur.length - 1])
  const nextWord = () => wordOf(items[at])

  const routine = (): boolean => {
    const w = words
    if (w.length < 2) return false
    switch (rules) {
      case "psql": {
        const kind = w.length >= 4 && w[1] === "or" && w[2] === "replace" ? w[3] : w[1]
        return w[0] === "create" && (kind === "function" || kind === "procedure")
      }
      case "mysql": {
        if (w[0] === "alter") return w[1] === "event"
        if (w[0] !== "create") return false
        for (const word of w.slice(1)) {
          if (["procedure", "function", "trigger", "event", "package"].includes(word)) return true
          if (
            [
              "table",
              "view",
              "index",
              "database",
              "schema",
              "user",
              "role",
              "server",
              "tablespace",
              "sequence",
              "logfile",
            ].includes(word)
          )
            return false
        }
        return false
      }
      case "tsql": {
        if (w[0] !== "create" && w[0] !== "alter") return false
        const kind = w.slice(1).find((word) => word !== "or" && word !== "alter")
        return kind === "proc" || kind === "procedure" || kind === "function" || kind === "trigger"
      }
      case "sqlite":
        return (
          w[0] === "create" &&
          w.slice(1).find((word) => word !== "temp" && word !== "temporary") === "trigger"
        )
    }
    return false
  }

  const plsql = (): boolean => {
    if (rules !== "plsql" || cur.length === 0) return false
    if (cur[0].kind === "operator" && text.slice(cur[0].start, cur[0].end).startsWith("<<"))
      return true
    const w = words
    switch (w[0]) {
      case "declare":
      case "begin":
        return true
      case "with":
        return w[1] === "function" || w[1] === "procedure"
      case "create": {
        const skip = [
          "or",
          "replace",
          "editionable",
          "noneditionable",
          "and",
          "compile",
          "resolve",
          "noforce",
        ]
        const kind = w.slice(1).find((word) => !skip.includes(word))
        return ["procedure", "function", "package", "trigger", "type", "library", "java"].includes(
          kind ?? "",
        )
      }
    }
    return false
  }

  const open = (word: string) => blocks.push(word)
  const close = () => {
    if (blocks.length === 0) return
    if (blocks.length === 1 && blocks[0] === "begin") closed = true
    blocks.pop()
  }

  // A statement of a MySQL body begins here: after a semicolon or a label,
  // after BEGIN, THEN, ELSE, LOOP or REPEAT, after a body's DO, after FOR
  // EACH ROW — or it is the statement's first word.
  const statementStart = (body: boolean): boolean => {
    const last = cur[cur.length - 1]
    if (!last || last.kind === "semicolon" || spelled(last, ":")) return true
    switch (lastWord()) {
      case "begin":
      case "then":
      case "else":
      case "loop":
      case "repeat":
        return true
      case "do":
        return body
      case "row":
        return wordOf(cur[cur.length - 2]) === "each"
    }
    return false
  }

  const mysqlBlock = (word: string) => {
    const body = blocks.length > 0 || routine()
    const after = lastWord()
    const start = statementStart(body)
    switch (word) {
      case "begin":
        if (
          body ||
          spelled(cur[cur.length - 1], ":") ||
          (wordOf(items[at]) === "not" && wordOf(items[at + 1]) === "atomic")
        )
          open(word)
        break
      case "case":
        if (after !== "end" && (start || body)) open(word)
        break
      case "if": {
        // Neither the function IF(…) nor DDL's IF [NOT] EXISTS.
        const next = items[at]
        const flow =
          next !== undefined &&
          next.kind !== "open" &&
          wordOf(next) !== "exists" &&
          !(wordOf(next) === "not" && wordOf(items[at + 1]) === "exists")
        if (after !== "end" && (start || (body && flow))) open(word)
        break
      }
      case "while":
      case "loop":
        if (after !== "end" && (start || body)) open(word)
        break
      case "repeat":
        if (start) open(word)
        break
      case "end":
        close()
    }
  }

  const block = (word: string) => {
    switch (rules) {
      case "psql":
        if (!routine()) return
        if (word === "begin") open(word)
        else if (word === "case" && blocks.length > 0) open(word)
        else if (word === "end") close()
        return
      case "sqlite":
        if (!routine()) return
        if (word === "begin" || word === "case") open(word)
        else if (word === "end") close()
        return
      case "tsql":
        if (word === "begin" && !TSQL_NOT_BLOCKS.has(nextWord())) open(word)
        else if (word === "case" && blocks.length > 0) open(word)
        else if (word === "end" && nextWord() !== "conversation") close()
        return
      case "mysql":
        mysqlBlock(word)
    }
  }

  const track = (item: Item) => {
    if (item.kind === "open") parens++
    else if (item.kind === "close") parens = Math.max(0, parens - 1)
    else if (item.kind === "word" && parens === 0) {
      const word = wordOf(item)
      if (!delimiter) block(word)
      if (words.length < 8) words.push(word)
    }
  }

  const ends = (): boolean => {
    if (delimiter || blocks.length > 0) return false
    switch (rules) {
      case "psql":
        return parens === 0
      case "tsql":
        return !(routine() && !closed) && nextWord() !== "else"
      case "plsql":
        return !plsql()
    }
    return true
  }

  // Closes the statement being read; `separated` when its separator did.
  const end = (to: number, separated: boolean) => {
    const unit = plsql()
    let held = cur
    while (!unit && held.length > 0 && held[held.length - 1].kind === "semicolon")
      held = held.slice(0, -1)
    if (held.length > 0) {
      const start = held[0].start
      const stop = held[held.length - 1].end
      const inner = held.slice(0, -1).some((item) => item.kind === "semicolon")
      const body = unit
        ? "plsql"
        : rules === "tsql" && routine()
          ? "routine"
          : inner
            ? "block"
            : undefined
      const runsOn = (body === "plsql" && !separated) || (body === "routine" && past)
      spans.push({
        start,
        end: stop,
        from,
        to,
        line: lineOf(text, start),
        text: text.slice(start, stop),
        ...(body ? { body } : {}),
        ...(runsOn ? { runsOn } : {}),
      })
      extend(start, stop)
    }
    from = to
    cur = []
    words = []
    parens = 0
    blocks = []
    closed = false
    past = false
  }

  while (at < items.length) {
    const item = items[at++]
    if (item.kind === "delimiter") {
      end(item.end, true)
      extend(item.start, item.end)
      separators = true
      continue
    }
    if (rules === "mysql" && cur.length === 0 && wordOf(item) === "delimiter") {
      // DELIMITER where a statement would begin names the delimiter from the
      // next line on; the rest of its line is the client's.
      const after = text[item.end]
      const nl = text.indexOf("\n", item.end)
      const lineEnd = nl < 0 ? text.length : nl
      const named = text.slice(item.end, lineEnd).trim().split(/\s+/)[0]
      if ((after === undefined || /\s/.test(after)) && named) {
        delimiter = named === ";" ? "" : named
        extend(item.start, text.indexOf(named, item.end) + named.length)
        separators = true
        resume(nl < 0 ? text.length : nl + 1)
        continue
      }
    }
    if (rules === "tsql" && wordOf(item) === "go" && lineStartsAt(text, item.start)) {
      let count = item.end
      while (text[count] === " " || text[count] === "\t") count++
      const digits = count
      while (/[0-9]/.test(text[count] ?? "")) count++
      const line = lineEndsAt(text, count, true)
      if (line) {
        end(line.lineEnd, true)
        extend(item.start, count > digits ? count : item.end)
        separators = true
        resume(line.next)
        continue
      }
    }
    if (rules === "plsql" && spelled(item, "/") && lineStartsAt(text, item.start)) {
      const line = lineEndsAt(text, item.end, false)
      if (line) {
        end(line.lineEnd, true)
        extend(item.start, item.end)
        separators = true
        resume(line.next)
        continue
      }
    }
    if (item.kind === "semicolon" && ends()) {
      end(item.end, true)
      continue
    }
    if (closed) past = true
    track(item)
    cur.push(item)
  }
  end(text.length, false)
  // The reach of the last statement runs to the end of the text.
  if (spans.length > 0) spans[spans.length - 1].to = text.length
  return { spans, reach, separators }
}

/** The statements of a text (`readScript`). */
export function splitStatements(text: string, dialect: Dialect): StatementSpan[] {
  return readScript(text, dialect).spans
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
  const { spans, reach } = readScript(text, dialect)
  if (spans.length === 0 || !reach) return null
  if (!all) {
    const span = statementAt(spans, cursor)
    if (span && spans.length > 1) {
      return { scope: "statement", sql: span.text, offset: span.start, line: span.line }
    }
  }
  // Everything goes with its separators: the DELIMITER that says where its
  // statements end, the GO that says how many times a batch runs.
  return {
    scope: spans.length === 1 ? "statement" : "all",
    sql: text.slice(reach.start, reach.end),
    offset: reach.start,
    line: lineOf(text, reach.start),
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
