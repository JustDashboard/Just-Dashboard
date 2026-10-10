import { LANES, hueFor } from "@/lib/hue"

/**
 * The hues the home tells kinds and names apart with (§14): every one a
 * `--tag-*`, which sit at one lightness so no kind reads louder than another,
 * and none of them a reading of state.
 */

type Kind = { label: string; color: string }

const tag = (hue: string) => `var(--tag-${hue})`

/**
 * A key–value store's key types: one type, one hue, wherever the section
 * draws a key. The Keys page has the legend this is taken from, hue for hue;
 * a type neither knows — a module's own — keeps the quiet ink and its own
 * word rather than borrowing a neighbour's colour.
 */
const KEY_TYPES: Record<string, Kind> = {
  string: { label: "String", color: tag("blue") },
  hash: { label: "Hash", color: tag("violet") },
  list: { label: "List", color: tag("green") },
  set: { label: "Set", color: tag("amber") },
  zset: { label: "Sorted set", color: tag("pink") },
  stream: { label: "Stream", color: tag("cyan") },
  "ReJSON-RL": { label: "JSON", color: tag("slate") },
}

/** The order the types are listed in, where several are: the Keys page's own. */
const KEY_TYPE_ORDER = Object.keys(KEY_TYPES)

export function keyType(type: string): Kind {
  return Object.hasOwn(KEY_TYPES, type)
    ? KEY_TYPES[type]
    : { label: type || "Module type", color: "var(--muted-foreground)" }
}

/** Types in the legend's order, the ones it does not know after them by name. */
export function byKeyType(a: string, b: string): number {
  const at = (type: string) => {
    const index = KEY_TYPE_ORDER.indexOf(type)
    return index < 0 ? KEY_TYPE_ORDER.length : index
  }
  return at(a) - at(b) || a.localeCompare(b)
}

/**
 * What a statement does, read off its first word: it reads, it adds, it
 * changes, it removes, or it changes the schema. The hue is set on that word
 * alone, so a list of statements is told apart before one is read through —
 * and a list that is all reads says so in one colour.
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
