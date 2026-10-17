/**
 * A comparison of two schemas as the server answers it
 * (`GET /databases/{id}/schema-diff`), and what the page makes of it. The
 * differences and the migration come from the server: this file reads them
 * whatever came back and words them, and decides nothing about a schema.
 */

export type Change = "added" | "removed" | "changed"

/** One difference inside an object, each side as it is written in a definition. */
export interface DiffPart {
  part: string
  name: string
  change: Change
  from: string
  to: string
}

export interface DiffObject {
  /** `table`, `view`, `materialized view`, `enum`, `sequence`, `function`, `procedure`. */
  kind: string
  name: string
  change: Change
  parts: DiffPart[]
}

export interface MigrationStep {
  title: string
  statements: string[]
  destructive: boolean
  /** The generator cannot write it for this engine; `note` says what to do. */
  manual: boolean
  note: string
}

export interface CompareSide {
  id: number
  name: string
  driver: string
  flavor: string
  flavorLabel: string
  schema: string
}

export interface Comparison {
  from: CompareSide
  to: CompareSide
  objects: DiffObject[]
  steps: MigrationStep[]
  script: string
  notes: string[]
}

const CHANGES: readonly string[] = ["added", "removed", "changed"]

const side = (raw: Partial<CompareSide> | undefined): CompareSide => ({
  id: raw?.id ?? 0,
  name: raw?.name ?? "",
  driver: raw?.driver ?? "",
  flavor: raw?.flavor ?? "",
  flavorLabel: raw?.flavorLabel ?? raw?.flavor ?? "",
  schema: raw?.schema ?? "",
})

/** The answer as the page reads it, whatever came back. */
export function readComparison(answer: unknown): Comparison {
  const given = (answer ?? {}) as Partial<Comparison>
  const objects = (Array.isArray(given.objects) ? given.objects : [])
    .filter((o) => typeof o?.name === "string" && CHANGES.includes(o.change))
    .map((o) => ({
      kind: o.kind ?? "",
      name: o.name,
      change: o.change,
      parts: (Array.isArray(o.parts) ? o.parts : []).map((p) => ({
        part: p.part ?? "",
        name: p.name ?? "",
        change: CHANGES.includes(p.change) ? p.change : "changed",
        from: p.from ?? "",
        to: p.to ?? "",
      })),
    }))
  return {
    from: side(given.from),
    to: side(given.to),
    objects,
    steps: (Array.isArray(given.steps) ? given.steps : []).map((s) => ({
      title: s.title ?? "",
      statements: Array.isArray(s.statements) ? s.statements : [],
      destructive: s.destructive === true,
      manual: s.manual === true,
      note: s.note ?? "",
    })),
    script: typeof given.script === "string" ? given.script : "",
    notes: Array.isArray(given.notes) ? given.notes : [],
  }
}

/** The kinds in the order the page lists them, with the heading each is listed under. */
const KINDS: { kind: string; label: string }[] = [
  { kind: "table", label: "Tables" },
  { kind: "view", label: "Views" },
  { kind: "materialized view", label: "Materialized views" },
  { kind: "enum", label: "Enum types" },
  { kind: "sequence", label: "Sequences" },
  { kind: "function", label: "Functions" },
  { kind: "procedure", label: "Procedures" },
]

export interface ObjectGroup {
  kind: string
  label: string
  objects: DiffObject[]
}

/** The differences by kind, in the page's order; a kind the server invents comes last. */
export function groupObjects(objects: readonly DiffObject[]): ObjectGroup[] {
  const groups = new Map<string, DiffObject[]>()
  for (const object of objects) {
    const held = groups.get(object.kind)
    if (held) held.push(object)
    else groups.set(object.kind, [object])
  }
  const known = KINDS.filter(({ kind }) => groups.has(kind)).map(({ kind, label }) => ({
    kind,
    label,
    objects: groups.get(kind)!,
  }))
  const rest = [...groups.keys()]
    .filter((kind) => !KINDS.some((entry) => entry.kind === kind))
    .map((kind) => ({ kind, label: kind, objects: groups.get(kind)! }))
  return [...known, ...rest]
}

/** How many differences there are of each change. */
export function changeCounts(objects: readonly DiffObject[]): Record<Change, number> {
  const counts: Record<Change, number> = { added: 0, removed: 0, changed: 0 }
  for (const object of objects) counts[object.change] += 1
  return counts
}

const count = (n: number, one: string, many = `${one}s`) =>
  `${n.toLocaleString("en-US")} ${n === 1 ? one : many}`

/** "7 differences: 2 added, 1 removed, 4 changed". */
export function summaryLine(objects: readonly DiffObject[]): string {
  const counts = changeCounts(objects)
  const said = (["added", "removed", "changed"] as const)
    .filter((change) => counts[change] > 0)
    .map((change) => `${counts[change]} ${change}`)
  return `${count(objects.length, "difference")}${said.length > 0 ? `: ${said.join(", ")}` : ""}`
}

/** What changed inside one object, in a phrase: "2 columns · 1 index". */
export function partsLine(parts: readonly DiffPart[]): string {
  const counts = new Map<string, number>()
  for (const part of parts) counts.set(part.part, (counts.get(part.part) ?? 0) + 1)
  return [...counts.entries()].map(([part, n]) => count(n, part)).join(" · ")
}

/** How many statements the migration holds, and how many of them need care. */
export function stepCounts(steps: readonly MigrationStep[]) {
  return {
    steps: steps.length,
    statements: steps.reduce((n, step) => n + step.statements.length, 0),
    destructive: steps.filter((step) => step.destructive).length,
    manual: steps.filter((step) => step.manual).length,
  }
}

/** "3 steps · 2 destructive · 1 for you to do". */
export function stepsLine(steps: readonly MigrationStep[]): string {
  const counts = stepCounts(steps)
  return [
    count(counts.steps, "step"),
    counts.destructive > 0 ? `${counts.destructive} destructive` : "",
    counts.manual > 0 ? `${counts.manual} for you to do` : "",
  ]
    .filter(Boolean)
    .join(" · ")
}

/** One row of a definition read side by side. A side is `null` where it has no line. */
export interface DefinitionRow {
  from: string | null
  to: string | null
  same: boolean
}

/** Past this many lines a side is shown as it is, without being lined up. */
const MAX_LINES = 1500

/**
 * Two definitions, lined up by their common lines so what was added, removed
 * and rewritten stands out. Lines are matched ignoring trailing space; a run
 * of removed lines facing a run of added ones is rewritten lines, side by side.
 */
export function definitionRows(from: string, to: string): DefinitionRow[] {
  const a = from === "" ? [] : from.split("\n")
  const b = to === "" ? [] : to.split("\n")
  if (a.length > MAX_LINES || b.length > MAX_LINES) {
    const rows = Math.max(a.length, b.length)
    return Array.from({ length: rows }, (_, i) => ({
      from: a[i] ?? null,
      to: b[i] ?? null,
      same: a[i]?.trimEnd() === b[i]?.trimEnd(),
    }))
  }
  const left = a.map((line) => line.trimEnd())
  const right = b.map((line) => line.trimEnd())
  // lengths[i][j] is the longest run of common lines in a[i:] and b[j:].
  const lengths = Array.from({ length: a.length + 1 }, () =>
    new Array<number>(b.length + 1).fill(0),
  )
  for (let i = a.length - 1; i >= 0; i--) {
    for (let j = b.length - 1; j >= 0; j--) {
      lengths[i][j] =
        left[i] === right[j]
          ? lengths[i + 1][j + 1] + 1
          : Math.max(lengths[i + 1][j], lengths[i][j + 1])
    }
  }
  const rows: DefinitionRow[] = []
  let removed: string[] = []
  let added: string[] = []
  const flush = () => {
    for (let k = 0; k < Math.max(removed.length, added.length); k++) {
      rows.push({ from: removed[k] ?? null, to: added[k] ?? null, same: false })
    }
    removed = []
    added = []
  }
  let i = 0
  let j = 0
  while (i < a.length || j < b.length) {
    if (i < a.length && j < b.length && left[i] === right[j]) {
      flush()
      rows.push({ from: a[i], to: b[j], same: true })
      i++
      j++
    } else if (j >= b.length || (i < a.length && lengths[i + 1][j] >= lengths[i][j + 1])) {
      removed.push(a[i++])
    } else {
      added.push(b[j++])
    }
  }
  flush()
  return rows
}

const slug = (text: string) =>
  text
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")

/** The file the migration is saved as: "shop-public-to-shop-staging-public.sql". */
export function scriptFilename(comparison: Pick<Comparison, "from" | "to">): string {
  const name = (s: CompareSide) => [slug(s.name), slug(s.schema)].filter(Boolean).join("-")
  return `${name(comparison.from) || "from"}-to-${name(comparison.to) || "to"}.sql`
}

/** What the comparison reads, and what it leaves alone. */
export const COMPARED =
  "Tables with their columns, keys, indexes, constraints and foreign keys, views, sequences, enum types, functions and procedures."
export const NOT_COMPARED =
  "Triggers, events, grants, comments, storage options and rows. A renamed table or column reads as one dropped and one added."

export interface Candidate {
  id: number
  name: string
  driver: string
  environment: string
  readOnly: boolean
  broken?: boolean
}

/**
 * The connections a schema can be compared with: the same kind of engine,
 * able to be compared, and able to be opened. The connection itself is among
 * them, since a database's other schema is a fair thing to compare it with.
 */
export function comparableConnections<T extends Candidate>(
  connections: readonly T[],
  from: Pick<Candidate, "driver">,
  canCompare: (connection: T) => boolean,
): T[] {
  return connections
    .filter((c) => c.driver === from.driver && !c.broken && canCompare(c))
    .sort((a, b) => a.name.localeCompare(b.name))
}

/** The other side's address in the page's own: a connection's id, as the address spells it. */
export function readSideId(text: string): number | undefined {
  return /^[1-9]\d*$/.test(text) ? Number(text) : undefined
}

/**
 * The longest query string a statement is handed to Query in. The script
 * travels in the address, and the server that answers it reads a request line
 * of a few kilobytes at most.
 */
const MAX_HANDOFF = 7000

/** Whether the script is short enough to open in Query rather than be pasted there. */
export function canHandToQuery(script: string): boolean {
  return new URLSearchParams({ sql: script }).toString().length <= MAX_HANDOFF
}
