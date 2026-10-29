import type { MapItem, ServerMap } from "@/lib/types"

/**
 * What the map calls each kind of thing it draws, and what the reader kept of
 * their arrangement. The systems themselves — which things make up a
 * deployment, a stack or this dashboard, and where each one goes — are
 * `server-systems.ts`'s.
 */

export type KindDef = {
  key: string
  /** One of them, for the inspector's head: "Container". */
  label: string
  /** The page that lists every one of them. */
  href: string
}

export const KINDS: readonly KindDef[] = [
  { key: "dashboard", label: "This dashboard", href: "/dashboard" },
  { key: "projects", label: "Deployment", href: "/deploy" },
  { key: "stacks", label: "Compose stack", href: "/docker/stacks" },
  { key: "containers", label: "Container", href: "/docker/containers" },
  { key: "databases", label: "Database", href: "/databases" },
  { key: "volumes", label: "Volume", href: "/docker/volumes" },
  { key: "domains", label: "Domain", href: "/proxy/sites" },
  { key: "certificates", label: "Certificate", href: "/proxy/certificates" },
  { key: "streams", label: "Stream", href: "/proxy/streams" },
  { key: "repositories", label: "Repository", href: "/git" },
  { key: "backups", label: "Backup job", href: "/backups" },
  { key: "services", label: "Service", href: "/processes/services" },
  { key: "pm2", label: "PM2 app", href: "/processes/pm2" },
  { key: "tunnels", label: "Private network", href: "/network/vpn" },
  { key: "logs", label: "Logs", href: "/logs" },
]

const KIND_BY_KEY = new Map(KINDS.map((kind) => [kind.key, kind]))

export function kindOf(key: string): KindDef | undefined {
  return KIND_BY_KEY.get(key)
}

/** The dashboard's own address and store are its kind's items, named for what they are. */
export function kindLabel(kind: string, itemId: string) {
  if (itemId === "dashboard:site") return "Dashboard address"
  if (itemId === "dashboard:store") return "Dashboard store"
  return kindOf(kind)?.label ?? kind
}

const indexes = new WeakMap<ServerMap, Map<string, { item: MapItem; kind: string }>>()

/** Every thing on the reading by its id, built once per reading. */
export function itemIndex(map: ServerMap) {
  let index = indexes.get(map)
  if (!index) {
    index = new Map()
    for (const kind of map.kinds)
      for (const item of kind.items) index.set(item.id, { item, kind: kind.key })
    indexes.set(map, index)
  }
  return index
}

// ---------------------------------------------------------------------------
// The document: what the reader arranged, kept per account on the server.
// ---------------------------------------------------------------------------

export type Point = { x: number; y: number }

export type MapDocument = {
  version: 2
  /**
   * Where a box was dragged to, inside its system, by node id
   * (`system:project:1/db:3`). A box nobody moved has none: its place is
   * where its system's layout puts it, so a server that gains a container
   * lays it out without moving anything the reader arranged.
   */
  positions: Record<string, Point>
  /** The system the reader was last looking at, flown back to on the next visit. */
  focus?: string
  viewport?: { x: number; y: number; zoom: number }
  minimap: boolean
}

export const DEFAULT_DOCUMENT: MapDocument = { version: 2, positions: {}, minimap: true }

const finite = (n: unknown): n is number => typeof n === "number" && Number.isFinite(n)
const isRecord = (v: unknown): v is Record<string, unknown> =>
  typeof v === "object" && v !== null && !Array.isArray(v)

/**
 * A saved document read back defensively: it was written by an older page,
 * or by hand through the API, and a field the canvas cannot use is dropped
 * rather than taking the canvas down with it. A version-1 document placed the
 * boxes of the mind map this replaced; its positions mean nothing here, and
 * only its minimap switch is kept.
 */
export function decodeDocument(raw: unknown): MapDocument {
  if (!isRecord(raw)) return DEFAULT_DOCUMENT
  const minimap = typeof raw.minimap === "boolean" ? raw.minimap : DEFAULT_DOCUMENT.minimap
  if (raw.version !== 2) return { ...DEFAULT_DOCUMENT, minimap }
  const positions: Record<string, Point> = {}
  if (isRecord(raw.positions)) {
    for (const [id, p] of Object.entries(raw.positions)) {
      if (isRecord(p) && finite(p.x) && finite(p.y)) positions[id] = { x: p.x, y: p.y }
    }
  }
  const v = raw.viewport
  const viewport =
    isRecord(v) && finite(v.x) && finite(v.y) && finite(v.zoom) && v.zoom > 0
      ? { x: v.x, y: v.y, zoom: v.zoom }
      : undefined
  return {
    version: 2,
    positions,
    focus: typeof raw.focus === "string" ? raw.focus : undefined,
    viewport,
    minimap,
  }
}

export function isArranged(doc: MapDocument) {
  return Object.keys(doc.positions).length > 0
}
