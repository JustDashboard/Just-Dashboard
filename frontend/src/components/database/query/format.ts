import type { Dialect } from "@/components/database/query/dialect"
import { RESERVED } from "@/components/database/query/keywords"
import { tokenize, type Token } from "@/components/database/query/sql-text"

/**
 * A statement laid out: one clause to a line, a long list one item to a line,
 * a subquery indented inside its parentheses, the words of the language in
 * capitals.
 *
 * It moves whitespace and changes the case of keywords, and nothing else. A
 * text, a quoted name, a comment and a dollar-quoted body are copied as they
 * were written, and a word is only capitalised where it cannot be a name
 * (never beside a dot). What comes out reads to the engine exactly as what
 * went in did.
 */

const WIDTH = 80
const INDENT = "  "

type Leaf = { kind: Token["kind"]; text: string; spaced: boolean; upper?: string }
type Group = { kind: "group"; open: Leaf; close?: Leaf; items: Item[] }
type Item = Leaf | Group

/** The words KEY completes: there it is the language's, anywhere else it is a column's name. */
const KEYED = new Set(["PRIMARY", "FOREIGN", "UNIQUE", "DUPLICATE"])

const isGroup = (item: Item): item is Group => item.kind === "group"
/** The keyword an item is, or nothing: a word beside a dot, or one that is no keyword, is a name. */
const wordOf = (item: Item | undefined) =>
  item && !isGroup(item) && item.kind === "word" ? (item.upper ?? "") : ""
/** Any word, in capitals, keyword or not. */
const spelled = (item: Item | undefined) =>
  item && !isGroup(item) && item.kind === "word" ? item.text.toUpperCase() : ""

/** The tokens as a tree of parentheses, with the space between them forgotten but for whether there was any. */
function parse(text: string, dialect: Dialect): Item[] {
  const tokens = tokenize(text, dialect)
  const root: Item[] = []
  const stack: Group[] = []
  let spaced = false
  let previous: Token | undefined
  let lastWord = ""
  tokens.forEach((token, index) => {
    if (token.kind === "space") {
      spaced = true
      return
    }
    const raw = text.slice(token.start, token.end)
    const leaf: Leaf = { kind: token.kind, text: raw, spaced }
    if (token.kind === "word") {
      const upper = raw.toUpperCase()
      const next = tokens[index + 1]
      // A word beside a dot is part of a name, whatever it spells. KEY is a
      // column to most tables and a keyword only after the word it completes.
      const keyword = RESERVED.has(upper) || (upper === "KEY" && KEYED.has(lastWord))
      if (keyword && previous?.kind !== "dot" && next?.kind !== "dot") leaf.upper = upper
      lastWord = upper
    }
    spaced = false
    previous = token
    const into = stack.length > 0 ? stack[stack.length - 1].items : root
    if (token.kind === "open") {
      const group: Group = { kind: "group", open: leaf, items: [] }
      into.push(group)
      stack.push(group)
    } else if (token.kind === "close" && stack.length > 0) {
      stack[stack.length - 1].close = leaf
      stack.pop()
    } else into.push(leaf)
  })
  return root
}

const lineComment = (item: Item) =>
  !isGroup(item) && item.kind === "comment" && !item.text.startsWith("/*")

/** Whether a space stands between two neighbours on one line. */
function gap(before: Item | undefined, item: Item): boolean {
  if (!before) return false
  const kind = isGroup(item) ? "open" : item.kind
  const prior = isGroup(before) ? "close" : before.kind
  if (kind === "comma" || kind === "semicolon") return false
  if (kind === "dot" || prior === "dot") return false
  if (kind === "open") {
    // A keyword stands off its parenthesis. After a name it is as it was
    // written: `count(*)` is a call and `INSERT INTO t (a, b)` is not, and
    // only the writer knew which.
    if (isGroup(before)) return true
    if (before.kind === "word" && before.upper !== undefined) return true
    if (before.kind === "word" || before.kind === "name") return (item as Group).open.spaced
    return before.kind !== "operator" || (item as Group).open.spaced
  }
  // An operator keeps the spacing it was written with: `a - b`, `-1`, `x::int`, `t.*`.
  if (kind === "operator" || prior === "operator") return (item as Leaf).spaced
  return true
}

/** A run of items on one line. */
function flat(items: Item[]): string {
  let out = ""
  let before: Item | undefined
  for (const item of items) {
    if (gap(before, item)) out += " "
    if (isGroup(item)) out += `(${flat(item.items)}${item.close ? ")" : ""}`
    else out += item.upper ?? item.text
    // What follows a line comment would be read as part of it.
    if (lineComment(item)) out += "\n"
    before = item
  }
  return out
}

const fits = (line: string, indent: string) =>
  !line.includes("\n") && indent.length + line.length <= WIDTH

/** The words that open a clause, each on a line of its own level. */
const CLAUSES = new Set([
  "SELECT",
  "FROM",
  "WHERE",
  "HAVING",
  "LIMIT",
  "OFFSET",
  "FETCH",
  "UNION",
  "EXCEPT",
  "INTERSECT",
  "VALUES",
  "SET",
  "RETURNING",
  "WINDOW",
  "WITH",
  "JOIN",
  "INSERT",
  "UPDATE",
  "DELETE",
  "QUALIFY",
  "SETTINGS",
])
const JOIN_LEADS = new Set([
  "LEFT",
  "RIGHT",
  "FULL",
  "INNER",
  "CROSS",
  "NATURAL",
  "OUTER",
  "ANY",
  "ALL",
  "ASOF",
  "GLOBAL",
])
/** Clauses whose body is a list separated by commas. */
const LISTS = new Set([
  "SELECT",
  "GROUP BY",
  "ORDER BY",
  "SET",
  "VALUES",
  "RETURNING",
  "WITH",
  "FROM",
])
/** Clauses whose body is conditions joined by AND / OR. */
const CONDITIONS = new Set(["WHERE", "HAVING", "QUALIFY", "ON"])

type Clause = { head: Leaf[]; body: Item[] }

/** Whether the word at `index` opens a clause where it stands. */
function opensClause(items: Item[], index: number): number {
  const word = wordOf(items[index])
  if (!word) return 0
  const before = wordOf(items[index - 1])
  const after = wordOf(items[index + 1])
  if (word === "GROUP" || word === "ORDER") return after === "BY" && before !== "WITHIN" ? 2 : 0
  if (JOIN_LEADS.has(word)) {
    // `LEFT JOIN`, `LEFT OUTER JOIN`, `ALL INNER JOIN` — a run of join words that ends in JOIN.
    let at = index
    while (JOIN_LEADS.has(wordOf(items[at]))) at++
    return wordOf(items[at]) === "JOIN" && !JOIN_LEADS.has(before) ? at - index + 1 : 0
  }
  if (!CLAUSES.has(word) && word !== "ON") return 0
  if (word === "JOIN") return JOIN_LEADS.has(before) ? 0 : 1
  if (word === "FROM") {
    // `IS DISTINCT FROM` compares; `DELETE FROM` is one head.
    if (before === "DISTINCT" && /^(IS|NOT)$/.test(wordOf(items[index - 2]))) return 0
    return before === "DELETE" ? 0 : 1
  }
  if (word === "UPDATE") return before === "DO" || before === "FOR" || before === "ON" ? 0 : 1
  if (word === "DELETE") return before === "ON" ? 0 : after === "FROM" ? 2 : 1
  if (word === "INSERT") return after === "INTO" ? 2 : 1
  if (word === "ON") {
    // An upsert's `ON CONFLICT …` / `ON DUPLICATE KEY UPDATE …` is a clause; a join's ON is not.
    if (after === "CONFLICT") return 2
    if (spelled(items[index + 1]) === "DUPLICATE")
      return spelled(items[index + 3]) === "UPDATE" ? 4 : 0
    return 0
  }
  if (word === "SET") {
    if (before === "ON" || spelled(items[index - 1]) === "CHARACTER") return 0
    return before === "UPDATE" && wordOf(items[index - 2]) === "DO" ? 0 : 1
  }
  if (word === "UNION" || word === "EXCEPT" || word === "INTERSECT") {
    return after === "ALL" || after === "DISTINCT" ? 2 : 1
  }
  // `WITH TIME ZONE`, `WITH TIES`, `WITH (…)` options belong to what they follow.
  if (word === "WITH") return index === 0 ? 1 : 0
  if (word === "FETCH") return after === "FIRST" || after === "NEXT" ? 1 : 0
  return 1
}

function clausesOf(items: Item[]): Clause[] {
  const clauses: Clause[] = [{ head: [], body: [] }]
  let at = 0
  while (at < items.length) {
    const taken = opensClause(items, at)
    if (taken > 0) {
      clauses.push({ head: items.slice(at, at + taken) as Leaf[], body: [] })
      at += taken
      continue
    }
    clauses[clauses.length - 1].body.push(items[at])
    at++
  }
  return clauses.filter((clause) => clause.head.length > 0 || clause.body.length > 0)
}

/** A run cut at its commas, each part keeping its comma. */
function byComma(items: Item[]): Item[][] {
  const parts: Item[][] = [[]]
  for (const item of items) {
    parts[parts.length - 1].push(item)
    if (!isGroup(item) && item.kind === "comma") parts.push([])
  }
  return parts.filter((part) => part.length > 0)
}

/** A run cut before each AND / OR that joins conditions (the AND of a BETWEEN is not one). */
function byCondition(items: Item[]): Item[][] {
  const parts: Item[][] = [[]]
  let between = false
  for (const item of items) {
    const word = wordOf(item)
    if (word === "BETWEEN") between = true
    else if (word === "AND" && between) between = false
    else if ((word === "AND" || word === "OR") && parts[parts.length - 1].length > 0) parts.push([])
    parts[parts.length - 1].push(item)
  }
  return parts
}

const SUBQUERY = new Set(["SELECT", "WITH", "INSERT", "UPDATE", "DELETE", "VALUES"])

/** A run that may hold groups too long for the line: those are opened up, one item to a line. */
function render(items: Item[], indent: string, open = false): string {
  const line = flat(items)
  const query = (item: Item) =>
    isGroup(item) &&
    SUBQUERY.has(wordOf(item.items.find((child) => isGroup(child) || child.kind !== "comment")))
  if (fits(line, indent) && !(open && items.some(query))) return line
  let out = ""
  let before: Item | undefined
  for (const item of items) {
    if (gap(before, item)) out += " "
    if (!isGroup(item)) {
      out += item.upper ?? item.text
      if (lineComment(item)) out += `\n${indent}`
    } else {
      const inner = flat(item.items)
      const column = indent.length + out.length - (out.lastIndexOf("\n") + 1)
      const nested = indent + INDENT
      if (query(item)) {
        out += `(\n${statement(item.items, nested)}\n${indent})`
      } else if (!inner.includes("\n") && column + inner.length + 2 <= WIDTH) {
        out += `(${inner}${item.close ? ")" : ""}`
      } else {
        const parts = byComma(item.items).map((part) => render(part, nested))
        out += `(\n${parts.map((part) => nested + part).join("\n")}\n${indent})`
      }
    }
    before = item
  }
  return out
}

function clause({ head, body }: Clause, indent: string): string {
  const name = head.map((leaf) => leaf.upper ?? leaf.text.toUpperCase()).join(" ")
  const key = name.endsWith("JOIN") ? "JOIN" : name
  const lead = name ? `${name} ` : ""
  const whole = lead + flat(body)
  if (body.length === 0) return indent + name
  if (fits(whole, indent) && key !== "WITH") return indent + whole
  const nested = indent + INDENT
  if (LISTS.has(key)) {
    const parts = byComma(body)
    if (parts.length > 1 || key === "SELECT" || key === "WITH") {
      // A common table expression is a query of its own: always opened up.
      const lines = parts.map((part) => nested + render(part, nested, key === "WITH"))
      return `${indent}${name}\n${lines.join("\n")}`
    }
  }
  if (CONDITIONS.has(key) || key === "JOIN") {
    const parts = byCondition(body)
    const [first, ...rest] = parts
    const lines = rest.map((part) => nested + render(part, nested))
    return [indent + lead + render(first, indent), ...lines].join("\n")
  }
  return indent + lead + render(body, indent)
}

/** One statement (or the inside of a subquery) as clauses, one to a line. */
function statement(items: Item[], indent: string): string {
  // Comments that stand before the first word keep lines of their own.
  let at = 0
  const lines: string[] = []
  while (at < items.length && !isGroup(items[at]) && (items[at] as Leaf).kind === "comment") {
    lines.push(indent + (items[at] as Leaf).text)
    at++
  }
  for (const part of clausesOf(items.slice(at))) lines.push(clause(part, indent))
  return lines.join("\n")
}

/** A text of one or more statements, laid out. An empty text stays empty. */
export function formatSql(text: string, dialect: Dialect): string {
  const items = parse(text, dialect)
  const statements: Item[][] = [[]]
  let closed = false
  for (const item of items) {
    if (!isGroup(item) && item.kind === "semicolon") {
      statements.push([])
      closed = true
      continue
    }
    closed = false
    statements[statements.length - 1].push(item)
  }
  const written = statements.filter((part) => part.length > 0).map((part) => statement(part, ""))
  if (written.length === 0) return text.trim() === "" ? "" : text
  const separator = ";\n\n"
  return (
    written.map((part) => part.replace(/[ \t]+$/gm, "")).join(separator) +
    (closed || written.length > 1 ? ";" : "") +
    "\n"
  )
}
