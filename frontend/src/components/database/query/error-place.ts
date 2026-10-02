import type { Dialect } from "@/components/database/query/dialect"
import { tokenize, type Token } from "@/components/database/query/sql-text"

/**
 * Where in a statement the engine tripped, read off the engine's own words.
 *
 * The server hands a failure over as text, with no position beside it. The
 * text usually names the place all the same — ClickHouse counts characters,
 * MySQL quotes the statement from the point of failure, and the others name
 * the word they could not read or the column they could not find. Where the
 * words point at exactly one place in the statement, that place is marked;
 * where they point at none or at several, nothing is claimed and the whole
 * statement is marked instead. A wrong mark is worse than a wide one.
 */
export interface Place {
  /** Offset in the statement. */
  start: number
  length: number
}

/** A name without the marks that quote it. */
function unquoted(raw: string): string {
  const pairs: [string, string][] = [
    ['"', '"'],
    ["`", "`"],
    ["[", "]"],
  ]
  for (const [open, close] of pairs) {
    if (raw.length >= 2 && raw.startsWith(open) && raw.endsWith(close)) {
      return raw.slice(1, -1)
    }
  }
  return raw
}

const END_OF_INPUT = /at end of input|incomplete input|\(end of query\)|near '' at line/i

/** What a message quotes or names: the candidates for the word it tripped on. */
function namedIn(message: string): string[] {
  const names: string[] = []
  const add = (name: string | undefined) => {
    const trimmed = name?.trim()
    if (trimmed) names.push(trimmed)
  }
  // "no such column: total", "no such table: main.t" — SQLite names without quoting.
  add(/no such (?:column|table|function|index|view): ([^\s(]+)/i.exec(message)?.[1])
  // "function nofunc(integer) does not exist", "FUNCTION db.nofunc does not exist".
  add(/function ([\w$.]+)\s*(?:\(|does not exist)/i.exec(message)?.[1])
  for (const quoted of message.matchAll(/"([^"\n]{1,128})"|'([^'\n]{1,128})'|`([^`\n]{1,128})`/g)) {
    add(quoted[1] ?? quoted[2] ?? quoted[3])
  }
  return names
}

// A text is never the place: the engine names words of the statement, not what a text holds.
const MARKABLE = new Set(["word", "name", "number", "operator", "comma", "open", "close"])

export function errorPlace(message: string, sql: string, dialect: Dialect): Place | null {
  const tokens = tokenize(sql, dialect).filter(
    (token) => token.kind !== "space" && token.kind !== "comment",
  )
  if (tokens.length === 0) return null
  const place = (token: Token): Place => ({ start: token.start, length: token.end - token.start })
  const textOf = (token: Token) => sql.slice(token.start, token.end)

  // ClickHouse counts: "failed at position 14 ('t')".
  const counted = /failed at position (\d+)/i.exec(message)
  if (counted && !END_OF_INPUT.test(message)) {
    const at = Number(counted[1]) - 1
    const token = tokens.find((entry) => entry.start === at)
    if (token) return place(token)
  }

  if (END_OF_INPUT.test(message)) return place(tokens[tokens.length - 1])

  // MySQL quotes the statement from where it stopped reading: "near 'fro t' at line 1".
  const near = /near '([\s\S]+)' at line (\d+)/.exec(message)
  if (near) {
    const line = Number(near[2])
    let from = 0
    for (let passed = 1; passed < line; passed++) {
      const next = sql.indexOf("\n", from)
      if (next < 0) break
      from = next + 1
    }
    // The quote is cut at eighty characters; its beginning is what places it.
    const fragment = near[1].slice(0, 60)
    const found = sql.indexOf(fragment, from)
    if (found >= 0 && sql.indexOf(fragment, found + 1) < 0) {
      const token = tokens.find((entry) => entry.start >= found)
      if (token) return place(token)
    }
    return null
  }

  for (const candidate of namedIn(message)) {
    // "db.table": the statement may name it with or without what qualifies it.
    const wanted = [candidate, candidate.slice(candidate.lastIndexOf(".") + 1)].map((name) =>
      name.toLowerCase(),
    )
    for (const name of new Set(wanted)) {
      const hits = tokens.filter(
        (token) => MARKABLE.has(token.kind) && unquoted(textOf(token)).toLowerCase() === name,
      )
      // Named once, it is the place. Named several times, any of them may be.
      if (hits.length === 1) return place(hits[0])
      if (hits.length > 1) return null
    }
  }
  return null
}
