import { LANES, hueFor } from "@/lib/hue"
import { KIND_ORDER, kindLabel, kindOf } from "@/components/database/redis/kinds"

/**
 * The hues the home tells kinds and names apart with (§14): every one a
 * `--tag-*`, which sit at one lightness so no kind reads louder than another,
 * and none of them a reading of state.
 */

type Kind = { label: string; color: string }

const tag = (hue: string) => `var(--tag-${hue})`

/**
 * A key–value store's key types: one type, one hue, wherever the section
 * draws a key. The Keys page's legend (`redis/kinds`) is the one table: this
 * was a copy of it, hue for hue, that had drifted — it knew "ReJSON-RL" but
 * not "json", so one JSON key was slate on Keys and muted here. A type
 * neither knows — a module's own — keeps the quiet ink and its own word
 * rather than borrowing a neighbour's colour.
 */
export function keyType(type: string): Kind {
  return { label: kindLabel(type), color: kindOf(type).color }
}

/** Types in the legend's order, the ones it does not know after them by name. */
export function byKeyType(a: string, b: string): number {
  const at = (type: string) => {
    const index = KIND_ORDER.indexOf(kindOf(type).id)
    return index < 0 ? KIND_ORDER.length : index
  }
  return at(a) - at(b) || a.localeCompare(b)
}

/**
 * What a statement does, read off its first word: it reads, it adds, it
 * changes, it removes, it changes the schema and what the catalogue says of
 * it, or it tells the server how to run — a setting, maintenance, a block of
 * code. The hue is set on that word alone, so a list of statements is told
 * apart before one is read through — and a list that is all reads says so in
 * one colour. A transaction's own words (BEGIN, COMMIT) do none of these and
 * keep the statement's ink.
 */
const STATEMENT_VERBS: Record<string, string> = {
  select: tag("blue"),
  with: tag("blue"),
  show: tag("blue"),
  explain: tag("blue"),
  values: tag("blue"),
  insert: tag("green"),
  copy: tag("green"),
  replace: tag("green"),
  merge: tag("green"),
  update: tag("violet"),
  delete: tag("pink"),
  truncate: tag("pink"),
  create: tag("cyan"),
  alter: tag("cyan"),
  drop: tag("cyan"),
  comment: tag("cyan"),
  grant: tag("cyan"),
  revoke: tag("cyan"),
  set: tag("amber"),
  reset: tag("amber"),
  discard: tag("amber"),
  vacuum: tag("amber"),
  analyze: tag("amber"),
  reindex: tag("amber"),
  cluster: tag("amber"),
  refresh: tag("amber"),
  checkpoint: tag("amber"),
  do: tag("amber"),
  call: tag("amber"),
}

/** A statement's first word, and the hue of what that word does. Neither for a statement with no word to read. */
export function statementVerb(text: string): { word: string; rest: string; color: string } | null {
  const match = /^\s*([A-Za-z]+)([\s\S]*)$/.exec(text)
  if (!match) return null
  const verb = match[1].toLowerCase()
  if (!Object.hasOwn(STATEMENT_VERBS, verb)) return null
  return { word: match[1], rest: match[2], color: STATEMENT_VERBS[verb] }
}

/**
 * A name's own hue: a schema, an account, a namespace. Hashed from the name
 * in lower case, from the hues that cannot be taken for a state, so one name
 * is one colour on every page that prints it.
 */
export function nameHue(name: string): string {
  return hueFor(name.toLowerCase(), LANES)
}
