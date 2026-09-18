import type { Edge, Node } from "@xyflow/react"
import type { DbGraphColumn, DbGraphTable, DbSchemaGraph } from "@/lib/types"
import type { DiagramColor, DiagramDetail, DiagramDocument } from "./memory"
import { visibleColumns } from "./layout"
import { HEADER_HEIGHT, NODE_WIDTH, NOTE_HEIGHT, ROW_HEIGHT, shortType } from "./table-node"

/**
 * The diagram, taken away.
 *
 * A picture of the schema is most useful somewhere this dashboard is not — a
 * pull request, a design document, a message to the person who has to build
 * the migration. Four forms, because four things are done with it: an SVG or
 * PNG for a document, Mermaid for a README GitHub will render, DBML for the
 * tools that read it, and the raw JSON for anything else.
 *
 * The SVG is drawn here rather than rasterised from the canvas: the canvas is
 * a hundred positioned `div`s with theme variables and a web font, none of
 * which survive a copy into a document. What is drawn is the same tables at
 * the same positions with the same edges, in fixed colours, in a font every
 * machine has.
 */

const PALETTE = {
  ground: "#161616",
  card: "#212121",
  header: "#272727",
  border: "#3a3a3a",
  hairline: "#2d2d2d",
  text: "#f4f4f5",
  muted: "#a1a1aa",
  faint: "#71717a",
  brand: "#CAE9FF",
  key: "#6f9ff7",
  note: "#c4c4cc",
}

const TAG_HEX: Record<DiagramColor, string> = {
  slate: "#8a94a8",
  red: "#e0524e",
  amber: "#e0a43a",
  green: "#3fbf84",
  cyan: "#3fb6c9",
  blue: "#4f86e6",
  violet: "#8b6fe0",
  pink: "#e0629a",
}

const MONO = "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace"
const SANS = "system-ui, -apple-system, 'Segoe UI', Roboto, sans-serif"

function esc(s: string) {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
}

export type SvgInput = {
  graph: DbSchemaGraph
  nodes: Node[]
  edges: Edge[]
  detail: DiagramDetail
  colors: Record<string, DiagramColor>
  notes: Record<string, string>
  title?: string
}

/** Where an edge attaches: the row of the column, or the header at names-only. */
function anchor(
  node: Node,
  table: DbGraphTable,
  column: string,
  side: "left" | "right",
  detail: DiagramDetail,
) {
  const x = side === "right" ? node.position.x + NODE_WIDTH : node.position.x
  const rows = visibleColumns(table, detail)
  const i = rows.findIndex((c) => c.name === column)
  const y =
    i >= 0
      ? node.position.y + HEADER_HEIGHT + i * ROW_HEIGHT + ROW_HEIGHT / 2
      : node.position.y + HEADER_HEIGHT / 2
  return { x, y }
}

export function renderSvg({ graph, nodes, edges, detail, colors, notes, title }: SvgInput): string {
  const byName = new Map(graph.tables.map((t) => [t.name, t]))
  const nodeOf = new Map(nodes.map((n) => [n.id, n]))
  const PAD = 40
  let minX = Infinity
  let minY = Infinity
  let maxX = -Infinity
  let maxY = -Infinity
  for (const n of nodes) {
    minX = Math.min(minX, n.position.x)
    minY = Math.min(minY, n.position.y)
    maxX = Math.max(maxX, n.position.x + NODE_WIDTH)
    maxY = Math.max(maxY, n.position.y + (n.height ?? HEADER_HEIGHT))
  }
  if (!Number.isFinite(minX)) {
    minX = minY = 0
    maxX = maxY = 200
  }
  const titleHeight = title ? 36 : 0
  const width = Math.ceil(maxX - minX + PAD * 2)
  const height = Math.ceil(maxY - minY + PAD * 2 + titleHeight)
  const ox = PAD - minX
  const oy = PAD + titleHeight - minY

  const parts: string[] = []
  parts.push(
    `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" viewBox="0 0 ${width} ${height}" font-family="${SANS}">`,
    `<defs><marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="8" markerHeight="8" orient="auto-start-reverse"><path d="M 0 0 L 10 5 L 0 10 z" fill="${PALETTE.faint}"/></marker></defs>`,
    `<rect width="${width}" height="${height}" fill="${PALETTE.ground}"/>`,
  )
  if (title) {
    parts.push(
      `<text x="${PAD}" y="${PAD - 8}" fill="${PALETTE.text}" font-size="16" font-weight="600">${esc(title)}</text>`,
    )
  }

  // Edges first, so tables sit over them.
  for (const e of edges) {
    const rel = (e.data as { relation?: DbSchemaGraph["edges"][number] })?.relation
    const s = nodeOf.get(e.source)
    const t = nodeOf.get(e.target)
    const st = byName.get(e.source)
    const tt = byName.get(e.target)
    if (!rel || !s || !t || !st || !tt) continue
    const sSide = (e.sourceHandle ?? "").includes(".right.") ? "right" : "left"
    const tSide = (e.targetHandle ?? "").includes(".right.") ? "right" : "left"
    const a = anchor(s, st, detail === "names" ? "" : rel.fromColumn, sSide, detail)
    const b = anchor(t, tt, detail === "names" ? "" : rel.toColumn, tSide, detail)
    const x1 = a.x + ox
    const y1 = a.y + oy
    const x2 = b.x + ox
    const y2 = b.y + oy
    const out = sSide === "right" ? 1 : -1
    const into = tSide === "right" ? 1 : -1
    const ex = x1 + out * 24
    const tx = x2 + into * 24
    const mx = (ex + tx) / 2
    const d = `M ${x1} ${y1} H ${ex} H ${mx} V ${y2} H ${tx} H ${x2}`
    parts.push(
      `<path d="${d}" fill="none" stroke="${PALETTE.faint}" stroke-width="1.25" marker-end="url(#arrow)"${
        rel.cardinality === "one-to-one" ? ' stroke-dasharray="6 4"' : ""
      }/>`,
    )
  }

  for (const n of nodes) {
    const t = byName.get(n.id)
    if (!t) continue
    const x = n.position.x + ox
    const y = n.position.y + oy
    const rows = visibleColumns(t, detail)
    const more = detail === "keys" && rows.length < t.columns.length ? 1 : 0
    const note = notes[t.name]
    const h = n.height ?? HEADER_HEIGHT + rows.length * ROW_HEIGHT
    parts.push(`<g>`)
    parts.push(
      `<rect x="${x}" y="${y}" width="${NODE_WIDTH}" height="${h}" rx="8" fill="${PALETTE.card}" stroke="${PALETTE.border}"/>`,
      `<path d="M ${x} ${y + 8} a 8 8 0 0 1 8 -8 h ${NODE_WIDTH - 16} a 8 8 0 0 1 8 8 v ${HEADER_HEIGHT - 8} h ${-NODE_WIDTH} z" fill="${PALETTE.header}"/>`,
      `<line x1="${x}" y1="${y + HEADER_HEIGHT}" x2="${x + NODE_WIDTH}" y2="${y + HEADER_HEIGHT}" stroke="${PALETTE.hairline}"/>`,
    )
    const color = colors[t.name]
    if (color) {
      parts.push(
        `<rect x="${x}" y="${y + 8}" width="3" height="${h - 16}" fill="${TAG_HEX[color]}"/>`,
      )
    }
    parts.push(
      `<text x="${x + 10}" y="${y + HEADER_HEIGHT / 2 + 4}" fill="${PALETTE.text}" font-family="${MONO}" font-size="12" font-weight="600">${esc(t.name)}</text>`,
    )
    if (t.rows > 0) {
      parts.push(
        `<text x="${x + NODE_WIDTH - 10}" y="${y + HEADER_HEIGHT / 2 + 4}" fill="${PALETTE.muted}" font-size="10" text-anchor="end">${esc(compact(t.rows))}</text>`,
      )
    }
    rows.forEach((c, i) => {
      const cy = y + HEADER_HEIGHT + i * ROW_HEIGHT + ROW_HEIGHT / 2
      parts.push(marker(c, x + 12, cy))
      parts.push(
        `<text x="${x + 24}" y="${cy + 3.5}" fill="${c.primaryKey ? PALETTE.text : PALETTE.note}" font-family="${MONO}" font-size="11"${c.primaryKey ? ' font-weight="600"' : ""}>${esc(trim(c.name, 22))}</text>`,
        `<text x="${x + NODE_WIDTH - 10}" y="${cy + 3.5}" fill="${c.nullable ? PALETTE.faint : PALETTE.muted}" font-family="${MONO}" font-size="10" text-anchor="end">${esc(shortType(c.type))}</text>`,
      )
      if (i > 0) {
        parts.push(
          `<line x1="${x}" y1="${cy - ROW_HEIGHT / 2}" x2="${x + NODE_WIDTH}" y2="${cy - ROW_HEIGHT / 2}" stroke="${PALETTE.hairline}"/>`,
        )
      }
    })
    if (more) {
      const cy = y + HEADER_HEIGHT + rows.length * ROW_HEIGHT + ROW_HEIGHT / 2
      parts.push(
        `<text x="${x + 10}" y="${cy + 3.5}" fill="${PALETTE.faint}" font-size="10">${t.columns.length - rows.length} more columns</text>`,
      )
    }
    if (note) {
      const ny = y + h - NOTE_HEIGHT
      parts.push(
        `<line x1="${x}" y1="${ny}" x2="${x + NODE_WIDTH}" y2="${ny}" stroke="${PALETTE.hairline}"/>`,
        `<text x="${x + 10}" y="${ny + NOTE_HEIGHT / 2 + 3.5}" fill="${PALETTE.muted}" font-size="10">${esc(trim(note, 40))}</text>`,
      )
    }
    parts.push(`</g>`)
  }
  parts.push(`</svg>`)
  return parts.join("\n")
}

function marker(c: DbGraphColumn, cx: number, cy: number): string {
  if (c.primaryKey) return `<circle cx="${cx}" cy="${cy}" r="3" fill="${PALETTE.key}"/>`
  if (c.foreignKey) return `<circle cx="${cx}" cy="${cy}" r="3" fill="${PALETTE.brand}"/>`
  if (c.unique)
    return `<circle cx="${cx}" cy="${cy}" r="2.5" fill="none" stroke="${PALETTE.faint}"/>`
  return ""
}

function trim(s: string, max: number) {
  return s.length > max ? s.slice(0, max - 1) + "…" : s
}

function compact(n: number): string {
  if (n < 1000) return String(n)
  if (n < 1_000_000) return `${(n / 1000).toFixed(n < 10_000 ? 1 : 0)}k`
  return `${(n / 1_000_000).toFixed(1)}M`
}

/** A Mermaid `erDiagram`, for a README or a pull request that GitHub renders. */
export function toMermaid(graph: DbSchemaGraph, hidden: Set<string>): string {
  const ident = (s: string) => s.replace(/[^A-Za-z0-9_]/g, "_")
  const type = (s: string) => s.replace(/\s+/g, "_").replace(/[^A-Za-z0-9_()[\]]/g, "")
  const lines = ["erDiagram"]
  const shown = graph.tables.filter((t) => !hidden.has(t.name))
  for (const t of shown) {
    lines.push(`  ${ident(t.name)} {`)
    for (const c of t.columns) {
      const flags = [c.primaryKey && "PK", c.foreignKey && "FK", c.unique && !c.primaryKey && "UK"]
        .filter(Boolean)
        .join(",")
      lines.push(`    ${type(c.type) || "unknown"} ${ident(c.name)}${flags ? ` ${flags}` : ""}`)
    }
    lines.push("  }")
  }
  const present = new Set(shown.map((t) => t.name))
  for (const e of graph.edges) {
    if (!present.has(e.fromTable) || !present.has(e.toTable)) continue
    const crow = e.cardinality === "one-to-one" ? "||--||" : "}o--||"
    lines.push(`  ${ident(e.fromTable)} ${crow} ${ident(e.toTable)} : "${ident(e.fromColumn)}"`)
  }
  return lines.join("\n") + "\n"
}

/** DBML, for dbdiagram.io and the tools that read it. */
export function toDbml(
  graph: DbSchemaGraph,
  hidden: Set<string>,
  notes: Record<string, string>,
): string {
  const q = (s: string) => (/^[A-Za-z_][A-Za-z0-9_]*$/.test(s) ? s : `"${s.replace(/"/g, '\\"')}"`)
  const shown = graph.tables.filter((t) => !hidden.has(t.name))
  const out: string[] = []
  for (const t of shown) {
    const name = t.schema ? `${q(t.schema)}.${q(t.name)}` : q(t.name)
    out.push(`Table ${name} {`)
    for (const c of t.columns) {
      const settings = [
        c.primaryKey && "pk",
        !c.nullable && !c.primaryKey && "not null",
        c.unique && !c.primaryKey && "unique",
      ].filter(Boolean)
      const type = /\s/.test(c.type) ? `"${c.type}"` : c.type
      out.push(`  ${q(c.name)} ${type}${settings.length ? ` [${settings.join(", ")}]` : ""}`)
    }
    if (notes[t.name]) out.push(`  Note: '${notes[t.name].replace(/'/g, "\\'")}'`)
    out.push("}", "")
  }
  const present = new Set(shown.map((t) => t.name))
  for (const e of graph.edges) {
    if (!present.has(e.fromTable) || !present.has(e.toTable)) continue
    const rel = e.cardinality === "one-to-one" ? "-" : ">"
    const del =
      e.onDelete && e.onDelete !== "NO ACTION" ? ` [delete: ${e.onDelete.toLowerCase()}]` : ""
    out.push(
      `Ref: ${q(e.fromTable)}.${q(e.fromColumn)} ${rel} ${q(e.toTable)}.${q(e.toColumn)}${del}`,
    )
  }
  return out.join("\n") + "\n"
}

export function toJson(graph: DbSchemaGraph, doc: DiagramDocument): string {
  return JSON.stringify(
    { schema: graph.schema, tables: graph.tables, edges: graph.edges, layout: doc },
    null,
    2,
  )
}

export function downloadBlob(name: string, blob: Blob) {
  const url = URL.createObjectURL(blob)
  const a = document.createElement("a")
  a.href = url
  a.download = name
  a.click()
  URL.revokeObjectURL(url)
}

export function downloadText(name: string, text: string, type: string) {
  downloadBlob(name, new Blob([text], { type }))
}

/**
 * The SVG rasterised through an `<img>` and a canvas, at twice the size so
 * the text survives being pasted into a document and scaled.
 */
export function downloadPng(name: string, svg: string, scale = 2): Promise<void> {
  return new Promise((resolve, reject) => {
    const size = /width="(\d+)" height="(\d+)"/.exec(svg)
    const width = size ? Number(size[1]) : 1200
    const height = size ? Number(size[2]) : 800
    const url = URL.createObjectURL(new Blob([svg], { type: "image/svg+xml;charset=utf-8" }))
    const img = new Image()
    img.onload = () => {
      const canvas = document.createElement("canvas")
      canvas.width = width * scale
      canvas.height = height * scale
      const ctx = canvas.getContext("2d")
      if (!ctx) {
        URL.revokeObjectURL(url)
        reject(new Error("canvas unavailable"))
        return
      }
      ctx.scale(scale, scale)
      ctx.drawImage(img, 0, 0)
      URL.revokeObjectURL(url)
      canvas.toBlob((blob) => {
        if (!blob) {
          reject(new Error("could not encode the image"))
          return
        }
        downloadBlob(name, blob)
        resolve()
      }, "image/png")
    }
    img.onerror = () => {
      URL.revokeObjectURL(url)
      reject(new Error("could not draw the diagram"))
    }
    img.src = url
  })
}
