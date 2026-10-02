import type { DiagramDetail } from "@/components/database/diagram/geometry"
import type { DiagramDirection, DiagramSpacing } from "@/components/database/diagram/layout"

/**
 * What the diagram remembers.
 *
 * Every decision the operator makes about the picture — where a table sits,
 * which are hidden, a note, a colour, how much of each table is drawn — is one
 * document, kept per connection and schema. Everything in it that names a
 * table names it by its id, the schema and the name together.
 */
export const DIAGRAM_COLORS = [
  "slate",
  "red",
  "amber",
  "green",
  "cyan",
  "blue",
  "violet",
  "pink",
] as const
export type DiagramColor = (typeof DIAGRAM_COLORS)[number]

export type DiagramDocument = {
  /** 2: keyed by table id. 1 was keyed by the bare table name. */
  version: 2
  direction: DiagramDirection
  detail: DiagramDetail
  spacing: DiagramSpacing
  /** Only the tables the operator placed (or a Tidy placed for them), by id. */
  positions: Record<string, { x: number; y: number }>
  hidden: string[]
  notes: Record<string, string>
  colors: Record<string, DiagramColor>
  viewport?: { x: number; y: number; zoom: number }
  grid: boolean
  snap: boolean
  minimap: boolean
  /** Cardinality labels on every edge, rather than only on the focused ones. */
  labels: boolean
  /** Tables cannot be dragged — for a diagram that is finished. */
  locked: boolean
}

export const DEFAULT_DOCUMENT: DiagramDocument = {
  version: 2,
  direction: "LR",
  detail: "all",
  spacing: "comfortable",
  positions: {},
  hidden: [],
  notes: {},
  colors: {},
  grid: true,
  snap: false,
  minimap: true,
  labels: false,
  locked: false,
}

const isRecord = (v: unknown): v is Record<string, unknown> =>
  typeof v === "object" && v !== null && !Array.isArray(v)
const finite = (v: unknown): v is number => typeof v === "number" && Number.isFinite(v)

/**
 * A stored document, checked field by field and filled in with the defaults.
 * Keys outlive the code that wrote them, and a value of the wrong shape would
 * otherwise reach the canvas as if it were fine — a position of `null` is a
 * table that never renders.
 *
 * `legacy` says the document was written before tables were known by their
 * ids: its keys are bare names, and `migrateDocument` is owed.
 */
export function decodeDocument(raw: unknown): (DiagramDocument & { legacy?: true }) | null {
  if (!isRecord(raw)) return null
  const doc: DiagramDocument & { legacy?: true } = {
    ...DEFAULT_DOCUMENT,
    positions: {},
    hidden: [],
    notes: {},
    colors: {},
  }
  if (raw.version !== 2) doc.legacy = true
  if (raw.direction === "LR" || raw.direction === "TB") doc.direction = raw.direction
  if (raw.detail === "all" || raw.detail === "keys" || raw.detail === "names")
    doc.detail = raw.detail
  if (raw.spacing === "compact" || raw.spacing === "comfortable") doc.spacing = raw.spacing
  if (isRecord(raw.positions)) {
    for (const [name, p] of Object.entries(raw.positions)) {
      if (isRecord(p) && finite(p.x) && finite(p.y)) doc.positions[name] = { x: p.x, y: p.y }
    }
  }
  if (Array.isArray(raw.hidden))
    doc.hidden = raw.hidden.filter((h): h is string => typeof h === "string")
  if (isRecord(raw.notes)) {
    for (const [name, n] of Object.entries(raw.notes))
      if (typeof n === "string" && n) doc.notes[name] = n
  }
  if (isRecord(raw.colors)) {
    for (const [name, c] of Object.entries(raw.colors)) {
      if (typeof c === "string" && (DIAGRAM_COLORS as readonly string[]).includes(c)) {
        doc.colors[name] = c as DiagramColor
      }
    }
  }
  if (
    isRecord(raw.viewport) &&
    finite(raw.viewport.x) &&
    finite(raw.viewport.y) &&
    finite(raw.viewport.zoom)
  ) {
    doc.viewport = { x: raw.viewport.x, y: raw.viewport.y, zoom: raw.viewport.zoom }
  }
  for (const flag of ["grid", "snap", "minimap", "labels", "locked"] as const) {
    if (typeof raw[flag] === "boolean") doc[flag] = raw[flag]
  }
  return doc
}

/**
 * A document written when tables were bare names, re-keyed by id.
 *
 * A name is matched to the table of that name in the picture — which is a
 * picture of the schema the document was saved for. Where the picture spans
 * several schemas and more than one holds the name, nobody can say which was
 * meant, and the entry is dropped rather than given to the wrong table. A
 * name that is already an id, or matches nothing, is kept as it is: a table
 * hidden behind the row limit is still the operator's to find arranged later.
 */
export function migrateDocument(
  doc: DiagramDocument & { legacy?: true },
  tables: readonly { id: string; name: string }[],
): DiagramDocument {
  const { legacy, ...rest } = doc
  if (!legacy) return rest
  const ids = new Set(tables.map((table) => table.id))
  const byName = new Map<string, string[]>()
  for (const table of tables) byName.set(table.name, [...(byName.get(table.name) ?? []), table.id])
  const rekey = (key: string): string | null => {
    if (ids.has(key)) return key
    const named = byName.get(key)
    if (!named) return key
    return named.length === 1 ? named[0] : null
  }
  const record = <T>(held: Record<string, T>) => {
    const next: Record<string, T> = {}
    for (const [key, value] of Object.entries(held)) {
      const id = rekey(key)
      if (id !== null) next[id] = value
    }
    return next
  }
  return {
    ...rest,
    version: 2,
    positions: record(rest.positions),
    notes: record(rest.notes),
    colors: record(rest.colors),
    hidden: [...new Set(rest.hidden.flatMap((key) => rekey(key) ?? []))],
  }
}

/** Whether the operator has changed anything from a fresh diagram. */
export function isArranged(doc: DiagramDocument): boolean {
  return (
    Object.keys(doc.positions).length > 0 ||
    doc.hidden.length > 0 ||
    Object.keys(doc.notes).length > 0 ||
    Object.keys(doc.colors).length > 0 ||
    doc.direction !== DEFAULT_DOCUMENT.direction ||
    doc.detail !== DEFAULT_DOCUMENT.detail ||
    doc.spacing !== DEFAULT_DOCUMENT.spacing ||
    doc.locked
  )
}

/** This browser's copy of an arrangement, and whether the server has it. */
export type Mirror = {
  doc: unknown
  /** When it was last changed here and the server has not yet confirmed that change. */
  dirtyAt?: string
}

export type Chosen = {
  document: DiagramDocument & { legacy?: true }
  /** The server holds exactly this; otherwise it is this browser's and is owed to the server. */
  saved: boolean
}

/**
 * Which copy of an arrangement opens: the server's, or this browser's.
 *
 * The server's, unless this browser holds a change the server never
 * confirmed and that change is the newer of the two. The page used to let the
 * server's copy overwrite the browser's unconditionally: a table dragged a
 * moment before the tab was closed — its save still waiting — was gone on the
 * next visit, replaced by what had been saved before it.
 */
export function chooseDocument(
  local: Mirror | null,
  remote: { layout: unknown; updatedAt?: string },
): Chosen {
  const mine = local ? decodeDocument(local.doc) : null
  const theirs = decodeDocument(remote.layout)
  const unconfirmed = Boolean(local?.dirtyAt)
  const newer =
    unconfirmed &&
    (!remote.updatedAt ||
      new Date(local!.dirtyAt!).getTime() > new Date(remote.updatedAt).getTime())
  if (mine && (newer || !theirs)) return { document: mine, saved: false }
  if (theirs) return { document: theirs, saved: true }
  return { document: DEFAULT_DOCUMENT, saved: true }
}
