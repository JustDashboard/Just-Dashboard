/**
 * A stickyboard's document, as the server stores and validates it
 * (`handlers_stickyboards.go`), and the pure operations the editor performs on
 * it. Nothing here touches React or the canvas, so all of it is held by
 * `stickyboard.test.js` rather than by a browser.
 */

export const NOTE_KINDS = ["sticky", "text", "section"] as const
export type NoteKind = (typeof NOTE_KINDS)[number]

/** One palette for notes, text, sections and links. `graphite` is the neutral one. */
export const NOTE_COLORS = [
  "yellow",
  "orange",
  "red",
  "pink",
  "violet",
  "blue",
  "teal",
  "green",
  "graphite",
] as const
export type NoteColor = (typeof NOTE_COLORS)[number]

export const NOTE_COLOR_NAMES: Record<NoteColor, string> = {
  yellow: "Yellow",
  orange: "Orange",
  red: "Red",
  pink: "Pink",
  violet: "Violet",
  blue: "Blue",
  teal: "Teal",
  green: "Green",
  graphite: "Graphite",
}

export type NoteStyle = "paper" | "tint"
export type NoteSize = "s" | "m" | "l" | "xl"
export type NoteAlign = "left" | "center"
export type NoteFont = "sans" | "mono"
export type LinkStyle = "solid" | "dashed"
export type LinkArrow = "none" | "end" | "both"
export type LinkPath = "curved" | "straight" | "elbow"
export type Background = "dots" | "lines" | "none"

export type StickyNote = {
  id: string
  kind: NoteKind
  x: number
  y: number
  w: number
  h: number
  text: string
  color: NoteColor
  style: NoteStyle
  size: NoteSize
  align: NoteAlign
  font: NoteFont
  locked: boolean
}

export type StickyLink = {
  id: string
  from: string
  to: string
  label: string
  color: NoteColor
  style: LinkStyle
  arrow: LinkArrow
  path: LinkPath
}

export type StickyDocument = {
  version: 1
  background: Background
  /** Paint order: a later note is drawn over an earlier one. */
  notes: StickyNote[]
  links: StickyLink[]
}

export type StickyPreview = {
  notes: (Pick<StickyNote, "x" | "y" | "w" | "h" | "kind" | "color" | "style"> & {
    /** The note's words without their Markdown; absent on a board not saved since it was added. */
    text?: string
  })[]
  links: [number, number][]
}

export type StickySummary = {
  id: number
  name: string
  revision: number
  /** Sticky notes only: text and sections are not what a reader counts as notes. */
  notes: number
  links: number
  preview: StickyPreview
  createdAt: string
  updatedAt: string
}

export type StickyBoard = StickySummary & { document: StickyDocument }

export const EMPTY_DOCUMENT: StickyDocument = {
  version: 1,
  background: "dots",
  notes: [],
  links: [],
}

/** The server's limits, so the editor refuses before a save would. */
export const LIMITS = { notes: 5000, links: 10000, text: 20000, label: 200 } as const

export const MIN_SIDE = 24

/** Where a new note of each kind starts, in board units. */
export const DEFAULT_SIZE: Record<NoteKind, { w: number; h: number }> = {
  sticky: { w: 220, h: 220 },
  text: { w: 320, h: 64 },
  section: { w: 720, h: 480 },
}

/** The smallest a note of each kind may be resized to. */
export const MIN_SIZE: Record<NoteKind, { w: number; h: number }> = {
  sticky: { w: 120, h: 96 },
  text: { w: 80, h: 32 },
  section: { w: 240, h: 160 },
}

/**
 * Type sizes in board units, which the canvas scales with its zoom. These are
 * the content of the board rather than the product's chrome, so they are not
 * on the interface's type ladder: a sticky's XL has to read from across a
 * zoomed-out board the way a marker does on a real one.
 */
export const FONT_SIZE: Record<NoteKind, Record<NoteSize, number>> = {
  sticky: { s: 13, m: 16, l: 22, xl: 30 },
  text: { s: 16, m: 24, l: 36, xl: 56 },
  section: { s: 14, m: 18, l: 26, xl: 38 },
}

export type Point = { x: number; y: number }
export type Rect = { x: number; y: number; w: number; h: number }

export function noteId(prefix = "n"): string {
  const bytes = new Uint8Array(8)
  crypto.getRandomValues(bytes)
  return `${prefix}_${Array.from(bytes, (b) => b.toString(36).padStart(2, "0")).join("")}`
}

/** A new note of a kind, centred on a point. */
export function createNote(
  kind: NoteKind,
  at: Point,
  options: Partial<Omit<StickyNote, "id" | "kind">> = {},
): StickyNote {
  const size = DEFAULT_SIZE[kind]
  const w = options.w ?? size.w
  const h = options.h ?? size.h
  return {
    id: noteId(),
    kind,
    text: "",
    color: kind === "sticky" ? "yellow" : "graphite",
    style: "paper",
    size: "m",
    align: "left",
    font: "sans",
    locked: false,
    ...options,
    x: Math.round(options.x ?? at.x - w / 2),
    y: Math.round(options.y ?? at.y - h / 2),
    w,
    h,
  }
}

export function createLink(
  from: string,
  to: string,
  options: Partial<Omit<StickyLink, "id" | "from" | "to">> = {},
): StickyLink {
  return {
    id: noteId("l"),
    from,
    to,
    label: "",
    color: "graphite",
    style: "solid",
    arrow: "end",
    path: "curved",
    ...options,
  }
}

export function rectOf(note: Pick<StickyNote, "x" | "y" | "w" | "h">): Rect {
  return { x: note.x, y: note.y, w: note.w, h: note.h }
}

export function center(rect: Rect): Point {
  return { x: rect.x + rect.w / 2, y: rect.y + rect.h / 2 }
}

export function boundsOf(rects: readonly Rect[]): Rect | null {
  if (rects.length === 0) return null
  let x1 = Infinity
  let y1 = Infinity
  let x2 = -Infinity
  let y2 = -Infinity
  for (const r of rects) {
    x1 = Math.min(x1, r.x)
    y1 = Math.min(y1, r.y)
    x2 = Math.max(x2, r.x + r.w)
    y2 = Math.max(y2, r.y + r.h)
  }
  return { x: x1, y: y1, w: x2 - x1, h: y2 - y1 }
}

export function containsRect(outer: Rect, inner: Rect): boolean {
  return (
    inner.x >= outer.x &&
    inner.y >= outer.y &&
    inner.x + inner.w <= outer.x + outer.w &&
    inner.y + inner.h <= outer.y + outer.h
  )
}

export function containsPoint(rect: Rect, point: Point, slack = 0): boolean {
  return (
    point.x >= rect.x - slack &&
    point.y >= rect.y - slack &&
    point.x <= rect.x + rect.w + slack &&
    point.y <= rect.y + rect.h + slack
  )
}

/**
 * The note a point lands on, the way the board paints them: the last-drawn
 * note under it, with sections only where no note covers the point, since a
 * section is the ground the notes stand on.
 */
export function noteAt(
  notes: readonly StickyNote[],
  point: Point,
  slack = 0,
  except?: string,
): StickyNote | null {
  let section: StickyNote | null = null
  for (let i = notes.length - 1; i >= 0; i--) {
    const note = notes[i]
    if (note.id === except || !containsPoint(rectOf(note), point, slack)) continue
    if (note.kind !== "section") return note
    section ??= note
  }
  return section
}

/**
 * The notes a section carries when it moves: those wholly inside it that are
 * not locked. A section inside a section goes with it, and so do its notes.
 */
export function carriedBy(notes: readonly StickyNote[], sectionId: string): string[] {
  const section = notes.find((n) => n.id === sectionId)
  if (!section || section.kind !== "section") return []
  const outer = rectOf(section)
  return notes
    .filter((n) => n.id !== sectionId && !n.locked && containsRect(outer, rectOf(n)))
    .map((n) => n.id)
}

export type Side = "top" | "right" | "bottom" | "left"

/**
 * Where a line from a note's centre towards a point leaves the note, and
 * which side it leaves by. Links are drawn between these, so a link always
 * meets the edge of each note it joins, whichever way the notes are moved.
 */
export function borderPoint(rect: Rect, toward: Point): Point & { side: Side } {
  const c = center(rect)
  const dx = toward.x - c.x
  const dy = toward.y - c.y
  if (dx === 0 && dy === 0) return { x: c.x, y: rect.y, side: "top" }
  const hw = rect.w / 2
  const hh = rect.h / 2
  // Scale the direction until it touches whichever pair of edges it reaches first.
  const sx = dx === 0 ? Infinity : hw / Math.abs(dx)
  const sy = dy === 0 ? Infinity : hh / Math.abs(dy)
  if (sx < sy) {
    return { x: c.x + dx * sx, y: c.y + dy * sx, side: dx > 0 ? "right" : "left" }
  }
  return { x: c.x + dx * sy, y: c.y + dy * sy, side: dy > 0 ? "bottom" : "top" }
}

/** The midpoint of the side a link leaves by, which is where an elbow path starts. */
export function sideMidpoint(rect: Rect, side: Side): Point {
  switch (side) {
    case "top":
      return { x: rect.x + rect.w / 2, y: rect.y }
    case "bottom":
      return { x: rect.x + rect.w / 2, y: rect.y + rect.h }
    case "left":
      return { x: rect.x, y: rect.y + rect.h / 2 }
    case "right":
      return { x: rect.x + rect.w, y: rect.y + rect.h / 2 }
  }
}

/** The ends of a link between two notes: each on the border facing the other. */
export function linkEnds(
  from: Rect,
  to: Rect,
): { source: Point & { side: Side }; target: Point & { side: Side } } {
  return { source: borderPoint(from, center(to)), target: borderPoint(to, center(from)) }
}

const NORMAL: Record<Side, Point> = {
  top: { x: 0, y: -1 },
  right: { x: 1, y: 0 },
  bottom: { x: 0, y: 1 },
  left: { x: -1, y: 0 },
}

/** An end of a link: where it touches the note, and the direction it arrives from. */
export type LinkEnd = Point & { angle: number }

export type LinkGeometry = { d: string; label: Point; start: LinkEnd; end: LinkEnd }

const round = (v: number) => Math.round(v * 10) / 10
const at = (p: Point) => `${round(p.x)},${round(p.y)}`

/**
 * The path a link draws between two notes, in board units, with the point its
 * label sits on and the angle each arrowhead points along. Computed from the
 * notes' rectangles rather than from fixed anchors, so a link follows a note
 * round to whichever side now faces the other.
 */
export function linkGeometry(a: Rect, b: Rect, path: LinkPath): LinkGeometry {
  if (path === "elbow") return elbowGeometry(a, b)
  const { source: p, target: q } = linkEnds(a, b)
  if (path === "straight") {
    const angle = Math.atan2(q.y - p.y, q.x - p.x)
    return {
      d: `M${at(p)} L${at(q)}`,
      label: { x: (p.x + q.x) / 2, y: (p.y + q.y) / 2 },
      start: { x: p.x, y: p.y, angle: angle + Math.PI },
      end: { x: q.x, y: q.y, angle },
    }
  }
  // A curve leaves each note square to the side it leaves by, pulled out
  // further the further apart the notes are.
  const reach = Math.min(240, Math.max(32, Math.hypot(q.x - p.x, q.y - p.y) * 0.35))
  const c1 = { x: p.x + NORMAL[p.side].x * reach, y: p.y + NORMAL[p.side].y * reach }
  const c2 = { x: q.x + NORMAL[q.side].x * reach, y: q.y + NORMAL[q.side].y * reach }
  return {
    d: `M${at(p)} C${at(c1)} ${at(c2)} ${at(q)}`,
    label: { x: (p.x + 3 * c1.x + 3 * c2.x + q.x) / 8, y: (p.y + 3 * c1.y + 3 * c2.y + q.y) / 8 },
    start: { x: p.x, y: p.y, angle: Math.atan2(p.y - c1.y, p.x - c1.x) },
    end: { x: q.x, y: q.y, angle: Math.atan2(q.y - c2.y, q.x - c2.x) },
  }
}

function elbowGeometry(a: Rect, b: Rect): LinkGeometry {
  const ca = center(a)
  const cb = center(b)
  // Across the wider of the two gaps between the notes, so the route runs
  // through the space between them rather than along their faces.
  const gapX = Math.max(b.x - (a.x + a.w), a.x - (b.x + b.w))
  const gapY = Math.max(b.y - (a.y + a.h), a.y - (b.y + b.h))
  const across = gapX >= gapY
  const from: Side = across ? (cb.x >= ca.x ? "right" : "left") : cb.y >= ca.y ? "bottom" : "top"
  const to: Side = across ? (cb.x >= ca.x ? "left" : "right") : cb.y >= ca.y ? "top" : "bottom"
  const s = sideMidpoint(a, from)
  const e = sideMidpoint(b, to)
  const mid = across ? (s.x + e.x) / 2 : (s.y + e.y) / 2
  const points = across
    ? [s, { x: mid, y: s.y }, { x: mid, y: e.y }, e]
    : [s, { x: s.x, y: mid }, { x: e.x, y: mid }, e]
  const route = simplify(points)
  const last = route[route.length - 2] ?? s
  const first = route[1] ?? e
  return {
    d: roundedPath(route, 12),
    label: { x: (points[1].x + points[2].x) / 2, y: (points[1].y + points[2].y) / 2 },
    start: { x: s.x, y: s.y, angle: Math.atan2(s.y - first.y, s.x - first.x) },
    end: { x: e.x, y: e.y, angle: Math.atan2(e.y - last.y, e.x - last.x) },
  }
}

/** Drops repeated points and the middle of any three in a line. */
function simplify(points: Point[]): Point[] {
  const out: Point[] = []
  for (const p of points) {
    const prev = out[out.length - 1]
    if (prev && Math.abs(prev.x - p.x) < 0.01 && Math.abs(prev.y - p.y) < 0.01) continue
    const before = out[out.length - 2]
    if (
      before &&
      prev &&
      Math.abs((prev.x - before.x) * (p.y - prev.y) - (prev.y - before.y) * (p.x - prev.x)) < 0.01
    ) {
      out[out.length - 1] = p
      continue
    }
    out.push(p)
  }
  return out
}

function roundedPath(points: Point[], radius: number): string {
  let d = `M${at(points[0])}`
  for (let i = 1; i < points.length - 1; i++) {
    const prev = points[i - 1]
    const cur = points[i]
    const next = points[i + 1]
    const r = Math.min(
      radius,
      Math.hypot(cur.x - prev.x, cur.y - prev.y) / 2,
      Math.hypot(next.x - cur.x, next.y - cur.y) / 2,
    )
    const toward = (p: Point) => {
      const length = Math.hypot(p.x - cur.x, p.y - cur.y) || 1
      return { x: cur.x + ((p.x - cur.x) / length) * r, y: cur.y + ((p.y - cur.y) / length) * r }
    }
    d += ` L${at(toward(prev))} Q${at(cur)} ${at(toward(next))}`
  }
  return `${d} L${at(points[points.length - 1])}`
}

/** The three points of an arrowhead whose tip is a link's end. */
export function arrowhead(end: LinkEnd, size: number): string {
  const spread = Math.PI / 7
  const left = {
    x: end.x - size * Math.cos(end.angle - spread),
    y: end.y - size * Math.sin(end.angle - spread),
  }
  const right = {
    x: end.x - size * Math.cos(end.angle + spread),
    y: end.y - size * Math.sin(end.angle + spread),
  }
  return `M${at(end)} L${at(left)} L${at(right)} Z`
}

const TASK = /^(\s*(?:>\s?)*\s*(?:[-*+]|\d{1,9}[.)])\s+\[)([ xX])(\]\s)/
const FENCE = /^ {0,3}(`{3,}|~{3,})/

/**
 * Ticks or unticks the `index`th task in a note's Markdown, counting the way
 * the note draws them: in reading order, skipping fenced code, where a
 * `- [ ]` is text rather than a box.
 */
export function toggleTask(text: string, index: number): string {
  const lines = text.split("\n")
  let fence: string | null = null
  let seen = 0
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]
    const opens = FENCE.exec(line)
    if (fence) {
      if (line.trimStart().startsWith(fence)) fence = null
      continue
    }
    if (opens) {
      fence = opens[1]
      continue
    }
    const task = TASK.exec(line)
    if (!task) continue
    if (seen++ !== index) continue
    const ticked = task[2] === " " ? "x" : " "
    lines[i] = task[1] + ticked + line.slice(task[1].length + 1)
    return lines.join("\n")
  }
  return text
}

/** How many of a note's tasks are ticked, for the count a note shows in its corner. */
export function taskProgress(text: string): { done: number; total: number } {
  let fence: string | null = null
  let done = 0
  let total = 0
  for (const line of text.split("\n")) {
    const opens = FENCE.exec(line)
    if (fence) {
      if (line.trimStart().startsWith(fence)) fence = null
      continue
    }
    if (opens) {
      fence = opens[1]
      continue
    }
    const task = TASK.exec(line)
    if (!task) continue
    total++
    if (task[2] !== " ") done++
  }
  return { done, total }
}

/** A board with these notes gone, and every link that ran to one of them. */
export function removeNotes(doc: StickyDocument, ids: ReadonlySet<string>): StickyDocument {
  if (ids.size === 0) return doc
  return {
    ...doc,
    notes: doc.notes.filter((n) => !ids.has(n.id)),
    links: doc.links.filter((l) => !ids.has(l.from) && !ids.has(l.to)),
  }
}

export function removeLinks(doc: StickyDocument, ids: ReadonlySet<string>): StickyDocument {
  if (ids.size === 0) return doc
  return { ...doc, links: doc.links.filter((l) => !ids.has(l.id)) }
}

/**
 * Copies of some notes and of the links among them, offset and given new ids.
 * A link to a note that was not copied stays behind: it would join the copy
 * to the original, which is not what anyone copying a group means.
 */
export function copyNotes(
  notes: readonly StickyNote[],
  links: readonly StickyLink[],
  offset: Point,
): { notes: StickyNote[]; links: StickyLink[] } {
  const ids = new Map<string, string>()
  const copies = notes.map((n) => {
    const id = noteId()
    ids.set(n.id, id)
    return { ...n, id, x: Math.round(n.x + offset.x), y: Math.round(n.y + offset.y), locked: false }
  })
  const linked = links
    .filter((l) => ids.has(l.from) && ids.has(l.to))
    .map((l) => ({ ...l, id: noteId("l"), from: ids.get(l.from)!, to: ids.get(l.to)! }))
  return { notes: copies, links: linked }
}

/**
 * Moves notes to the front or the back of the paint order, keeping their order
 * among themselves. Sections stay under every note whichever way they move:
 * a section brought to the front would hide what is on it.
 */
export function reorder(
  notes: readonly StickyNote[],
  ids: ReadonlySet<string>,
  to: "front" | "back",
): StickyNote[] {
  const moved = notes.filter((n) => ids.has(n.id))
  const rest = notes.filter((n) => !ids.has(n.id))
  const ordered = to === "front" ? [...rest, ...moved] : [...moved, ...rest]
  return [
    ...ordered.filter((n) => n.kind === "section"),
    ...ordered.filter((n) => n.kind !== "section"),
  ]
}

/** Notes whose text holds every word of a query, in paint order. */
export function searchNotes(notes: readonly StickyNote[], query: string): StickyNote[] {
  const words = fold(query).split(/\s+/).filter(Boolean)
  if (words.length === 0) return []
  return notes.filter((n) => {
    const text = fold(n.text)
    return words.every((word) => text.includes(word))
  })
}

function fold(value: string): string {
  return value.normalize("NFKD").replace(/[̀-ͯ]/g, "").toLowerCase()
}

/** The first line of a note with its Markdown marks taken off, for a list or a label. */
export function noteTitle(note: Pick<StickyNote, "text">, fallback = "Empty note"): string {
  const line = note.text
    .split("\n")
    .map((l) =>
      l
        .replace(/^\s*(?:#{1,6}\s+|>\s?|[-*+]\s+(?:\[[ xX]\]\s+)?|\d+[.)]\s+)/, "")
        .replace(/[*_~`]/g, "")
        .trim(),
    )
    .find(Boolean)
  return line ? (line.length > 80 ? `${line.slice(0, 79)}…` : line) : fallback
}

/** What the clipboard carries between boards and tabs, marked so plain text is never mistaken for it. */
export const CLIPBOARD_MARK = "just-dashboard/stickyboard"

export function clipboardText(notes: readonly StickyNote[], links: readonly StickyLink[]): string {
  return JSON.stringify({ [CLIPBOARD_MARK]: 1, notes, links })
}

export function readClipboard(text: string): { notes: StickyNote[]; links: StickyLink[] } | null {
  try {
    const value = JSON.parse(text) as Record<string, unknown>
    if (!value || value[CLIPBOARD_MARK] !== 1) return null
    if (!Array.isArray(value.notes) || !Array.isArray(value.links)) return null
    const notes = (value.notes as StickyNote[]).filter(
      (n) =>
        n &&
        typeof n.id === "string" &&
        (NOTE_KINDS as readonly string[]).includes(n.kind) &&
        [n.x, n.y, n.w, n.h].every((v) => typeof v === "number" && Number.isFinite(v)),
    )
    return {
      notes,
      links: (value.links as StickyLink[]).filter((l) => l && typeof l.from === "string"),
    }
  } catch {
    return null
  }
}

/** A document's thumbnail, the way the server derives it for a board it stores. */
export function previewOf(doc: StickyDocument): StickyPreview {
  const index = new Map(doc.notes.map((n, i) => [n.id, i]))
  return {
    notes: doc.notes.map(({ x, y, w, h, kind, color, style, text }) => ({
      x,
      y,
      w,
      h,
      kind,
      color,
      style,
      text: plainText(text),
    })),
    links: doc.links.flatMap((l) => {
      const from = index.get(l.from)
      const to = index.get(l.to)
      return from === undefined || to === undefined ? [] : [[from, to] as [number, number]]
    }),
  }
}

/**
 * A note's Markdown reduced to the words a thumbnail shows: four lines, 120
 * characters, the markers taken out. The server's `stickyPlainText` does the
 * same for a stored board; this one draws a template before it is one.
 */
export function plainText(markdown: string) {
  const lines = markdown
    .split("\n")
    .map((line) =>
      line
        .trim()
        .replace(/^(?:[-*_]\s*){3,}$|^(?:```|~~~).*$/, "")
        .replace(/^>\s?/, "")
        .replace(/^#{1,6}\s+/, "")
        .replace(/^(?:[-*+]|\d{1,9}[.)])\s+/, "")
        .replace(/^\[[ xX]\]\s+/, "")
        .replace(/!?\[([^\]]*)\]\([^)]*\)/g, "$1")
        .replace(/\*\*|__|~~|`/g, "")
        .trim(),
    )
    .filter(Boolean)
    .slice(0, 4)
  return [...lines.join("\n")].slice(0, 120).join("")
}

/**
 * Words broken into lines of about `width` characters, at most `max` of
 * them, the last ending in an ellipsis when the words run on. Measured in
 * characters because a thumbnail only has to look like the note, not set it.
 */
export function wrapWords(text: string, width: number, max: number): string[] {
  const lines: string[] = []
  const limit = Math.max(1, Math.floor(width))
  for (const paragraph of text.split("\n")) {
    let line = ""
    for (const word of paragraph.split(/\s+/).filter(Boolean)) {
      const next = line ? `${line} ${word}` : word
      if (next.length <= limit || !line) {
        line = next.length > limit ? `${next.slice(0, limit - 1)}…` : next
        continue
      }
      lines.push(line)
      line = word.length > limit ? `${word.slice(0, limit - 1)}…` : word
    }
    if (line) lines.push(line)
  }
  if (lines.length <= max) return lines
  const kept = lines.slice(0, max)
  const last = kept[max - 1]
  kept[max - 1] = `${last.length >= limit ? last.slice(0, limit - 1) : last}…`
  return kept
}

export type Template = {
  id: string
  name: string
  hint: string
  build: () => StickyDocument
}

function section(text: string, x: number, y: number, w: number, h: number, color: NoteColor) {
  return createNote("section", { x: 0, y: 0 }, { text, x, y, w, h, color })
}

function sticky(
  text: string,
  x: number,
  y: number,
  color: NoteColor,
  extra: Partial<StickyNote> = {},
) {
  return createNote("sticky", { x: 0, y: 0 }, { text, x, y, color, ...extra })
}

function heading(text: string, x: number, y: number, w = 640) {
  return createNote("text", { x: 0, y: 0 }, { text, x, y, w, h: 72, size: "l" })
}

/** Starting points for a new board. Every note in one is an ordinary note. */
export const TEMPLATES: Template[] = [
  {
    id: "blank",
    name: "Blank",
    hint: "An empty board",
    build: () => ({ ...EMPTY_DOCUMENT }),
  },
  {
    id: "kanban",
    name: "Kanban",
    hint: "To do, doing and done",
    build: () => {
      const columns: [string, NoteColor][] = [
        ["To do", "graphite"],
        ["Doing", "blue"],
        ["Done", "green"],
      ]
      const notes = columns.map(([name, color], i) => section(name, i * 300, 0, 280, 760, color))
      notes.push(
        heading("# Kanban", 0, -120),
        sticky("Plan the release\n\n- [ ] Changelog\n- [ ] Tag", 30, 60, "yellow"),
        sticky("Rotate the backup key", 30, 300, "orange"),
        sticky("Upgrade Postgres to 17", 330, 60, "blue"),
        sticky("Move DNS to the new provider\n\n- [x] Export zone\n- [x] Import", 630, 60, "green"),
      )
      return { ...EMPTY_DOCUMENT, notes }
    },
  },
  {
    id: "retro",
    name: "Retrospective",
    hint: "What went well, what to change",
    build: () => {
      const pager = sticky("Alerts paged at 3 a.m. for a disk that was fine", 590, 60, "orange")
      const owner = sticky("Tune the disk alert\n\n**Owner:** ops", 1150, 60, "blue")
      const notes = [
        section("Went well", 0, 0, 520, 560, "green"),
        section("To improve", 560, 0, 520, 560, "orange"),
        section("Actions", 1120, 0, 520, 560, "blue"),
        heading("# Retrospective", 0, -120),
        sticky("Deploys were quiet all week", 30, 60, "green"),
        pager,
        owner,
      ]
      const links = [createLink(pager.id, owner.id, { label: "fix", color: "orange" })]
      return { ...EMPTY_DOCUMENT, notes, links }
    },
  },
  {
    id: "map",
    name: "Mind map",
    hint: "One idea in the middle, linked out",
    build: () => {
      const middle = sticky("## Main idea", -130, -130, "violet", {
        w: 260,
        h: 260,
        size: "l",
        align: "center",
      })
      const around: [string, number, number, NoteColor][] = [
        ["Why", -560, -360, "yellow"],
        ["Who", 340, -360, "teal"],
        ["How", -560, 140, "pink"],
        ["When", 340, 140, "green"],
      ]
      const notes = [middle]
      const links: StickyLink[] = []
      for (const [text, x, y, color] of around) {
        const note = sticky(`### ${text}\n\n`, x, y, color)
        notes.push(note)
        links.push(createLink(middle.id, note.id, { arrow: "none", color: "graphite" }))
      }
      return { ...EMPTY_DOCUMENT, notes, links }
    },
  },
]
