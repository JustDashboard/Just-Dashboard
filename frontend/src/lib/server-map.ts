import type { Capability, MapItem, MapKindReading, MapLink, ServerMap } from "@/lib/types"
import type { SectionKey } from "@/lib/sidebar-sections"

/**
 * The server map's shape: what branches off the server, what each branch
 * holds, and where every box goes.
 *
 * The map is a mind map rather than a graph. The server is the heart in the
 * middle; seven branches leave it, three to the right and four to the left;
 * each branch holds the kinds of thing the dashboard manages (its *shelves*),
 * and each shelf holds the things themselves in a grid that grows away from
 * the server. A force layout would put the same server somewhere different on
 * every visit, and a free graph of three hundred boxes is the hairball nobody
 * reads; a tree with fixed sides is a shape somebody learns, the way the ER
 * diagram's dagre layout is.
 *
 * Everything here is pure and unit-tested (`server-map.test.js`): the canvas
 * hands this the reading and the saved document and draws what comes back.
 */

export type BranchKey = "deployed" | "web" | "host" | "jobs" | "workspace" | "people" | "protection"

export type Branch = {
  key: BranchKey
  label: string
  /** Which side of the server the branch grows on. Fixed, so the map keeps its shape. */
  side: "right" | "left"
}

/**
 * Right first, top to bottom, then left. Deployed is the biggest branch on
 * most servers and the reason most of them exist, so it takes the right's
 * first place; the four smaller branches share the left between them.
 */
export const BRANCHES: readonly Branch[] = [
  { key: "deployed", label: "Deployed", side: "right" },
  { key: "web", label: "Web", side: "right" },
  { key: "host", label: "Host", side: "right" },
  { key: "workspace", label: "Workspace", side: "left" },
  { key: "jobs", label: "Jobs", side: "left" },
  { key: "people", label: "People", side: "left" },
  { key: "protection", label: "Protection", side: "left" },
]

/** How a shelf's "Add" is reached: a page that opens on the form, or made in place. */
export type AddAction = {
  label: string
  capability: Capability
  href?: string
  /** Made where it is asked for and then opened, as the Overview's New drawing is. */
  create?: "drawing"
}

export type KindDef = {
  key: string
  branch: BranchKey
  /** The shelf's name, plural: "Containers". */
  label: string
  /** One of them: "container", for "Add your first container" and counts. */
  noun: string
  /** The page that lists every one of them. */
  href: string
  /** The sidebar section the kind lives in, for the sections an account hides. */
  section?: SectionKey
  /** Below this the role cannot open the kind's page, so the shelf is not drawn. */
  capability?: Capability
  add?: AddAction
  /** Columns in the shelf's grid. */
  cols?: number
  /**
   * A tool rather than an inventory: the shelf is one box that opens the
   * page, with nothing to read. Files is a tool — every file on the disk is
   * not a box.
   */
  tool?: { label: string; detail: string; product?: string }
}

export const KINDS: readonly KindDef[] = [
  // Deployed: what runs here because somebody put it here.
  {
    key: "projects",
    branch: "deployed",
    label: "Projects",
    noun: "project",
    href: "/deploy",
    section: "deploy",
    add: { label: "New project", capability: "system.admin", href: "/deploy/new" },
  },
  {
    key: "databases",
    branch: "deployed",
    label: "Databases",
    noun: "database",
    href: "/databases",
    section: "databases",
    add: { label: "Add a database", capability: "system.admin", href: "/databases/new" },
  },
  {
    key: "stacks",
    branch: "deployed",
    label: "Stacks",
    noun: "stack",
    href: "/docker/stacks",
    section: "docker",
  },
  {
    key: "containers",
    branch: "deployed",
    label: "Containers",
    noun: "container",
    href: "/docker/containers",
    section: "docker",
    cols: 4,
  },
  {
    key: "volumes",
    branch: "deployed",
    label: "Volumes",
    noun: "volume",
    href: "/docker/volumes",
    section: "docker",
  },
  {
    key: "networks",
    branch: "deployed",
    label: "Networks",
    noun: "network",
    href: "/docker/networks",
    section: "docker",
  },
  {
    key: "images",
    branch: "deployed",
    label: "Images",
    noun: "image",
    href: "/docker/images",
    section: "docker",
  },
  // Web: the names the server answers to and what keeps them private.
  {
    key: "domains",
    branch: "web",
    label: "Domains",
    noun: "domain",
    href: "/proxy/sites",
    section: "proxy",
    add: { label: "Add a site", capability: "system.admin", href: "/proxy/sites?new=1" },
  },
  {
    key: "certificates",
    branch: "web",
    label: "Certificates",
    noun: "certificate",
    href: "/proxy/certificates",
    section: "proxy",
  },
  {
    key: "streams",
    branch: "web",
    label: "Streams",
    noun: "stream",
    href: "/proxy/streams",
    section: "proxy",
  },
  // Host: the machine's own software.
  {
    key: "services",
    branch: "host",
    label: "Services",
    noun: "service",
    href: "/processes/services",
  },
  {
    key: "pm2",
    branch: "host",
    label: "PM2",
    noun: "application",
    href: "/processes/pm2",
  },
  {
    key: "dashboard",
    branch: "host",
    label: "Dashboard",
    noun: "version",
    href: "/dashboard",
  },
  {
    key: "packages",
    branch: "host",
    label: "Packages",
    noun: "package",
    href: "/packages",
    section: "packages",
    // A tool, not a shelf: what is installed and what can be updated is the
    // package manager's to say, and asking it on every read of the map is
    // the costliest thing the map could do.
    tool: { label: "Packages", detail: "Installed software and updates" },
  },
  // Workspace: the tools somebody works in.
  {
    key: "terminals",
    branch: "workspace",
    label: "Terminals",
    noun: "shell",
    href: "/terminal",
    section: "terminal",
    capability: "terminal",
    add: { label: "New shell", capability: "terminal", href: "/terminal" },
  },
  {
    key: "drawings",
    branch: "workspace",
    label: "Drawings",
    noun: "drawing",
    href: "/excalidraw",
    section: "excalidraw",
    add: { label: "New drawing", capability: "service.control", create: "drawing" },
  },
  {
    key: "repositories",
    branch: "workspace",
    label: "Repositories",
    noun: "repository",
    href: "/git",
    section: "git",
  },
  {
    key: "computers",
    branch: "workspace",
    label: "Computers",
    noun: "computer",
    href: "/remote-desktop",
    capability: "system.admin",
    section: "remote-desktop",
    add: { label: "Add a computer", capability: "system.admin", href: "/remote-desktop/new" },
  },
  {
    key: "files",
    branch: "workspace",
    label: "Files",
    noun: "folder",
    href: "/files",
    section: "files",
    tool: { label: "Browse files", detail: "Every folder on the server", product: "files" },
  },
  // Jobs: what runs by itself, on a schedule or when something happens.
  {
    key: "backups",
    branch: "jobs",
    label: "Backups",
    noun: "backup job",
    href: "/backups",
    section: "backups",
    add: { label: "New backup job", capability: "system.admin", href: "/backups" },
  },
  {
    key: "scheduled",
    branch: "jobs",
    label: "Scheduled",
    noun: "scheduled job",
    href: "/processes/scheduled",
  },
  {
    key: "alerts",
    branch: "jobs",
    label: "Alert rules",
    noun: "alert rule",
    href: "/metrics/alerts",
    capability: "system.admin",
    add: { label: "New alert rule", capability: "system.admin", href: "/metrics/alerts" },
  },
  {
    key: "channels",
    branch: "jobs",
    label: "Notifications",
    noun: "channel",
    href: "/deploy/notifications",
    section: "deploy",
    add: { label: "Add a channel", capability: "system.admin", href: "/deploy/notifications" },
  },
  // People: who can reach the server, and with what.
  {
    key: "accounts",
    branch: "people",
    label: "Accounts",
    noun: "account",
    href: "/account/users",
    capability: "system.admin",
    add: { label: "Add an account", capability: "system.admin", href: "/account/users" },
  },
  {
    key: "keys",
    branch: "people",
    label: "API keys",
    noun: "API key",
    href: "/account/keys",
    add: { label: "New API key", capability: "read", href: "/account/keys" },
  },
  {
    key: "systemUsers",
    branch: "people",
    label: "System users",
    noun: "system user",
    href: "/system-users",
    capability: "system.admin",
    section: "system-users",
  },
  // Protection: what stands between the server and everybody else.
  {
    key: "security",
    branch: "protection",
    label: "Security",
    noun: "check",
    href: "/security",
    section: "security",
  },
  {
    key: "tunnels",
    branch: "protection",
    label: "VPN",
    noun: "tunnel",
    href: "/network/vpn",
    capability: "system.admin",
    section: "network",
  },
]

const KIND_BY_KEY = new Map(KINDS.map((kind) => [kind.key, kind]))

export function kindOf(key: string): KindDef | undefined {
  return KIND_BY_KEY.get(key)
}

/**
 * The kinds this account is shown: every kind its role can open, in the
 * registry's order. A kind the server did not send at all — a module this
 * server does not run (PM2, Remote Desktop with no guacd) — is left out
 * rather than drawn as an empty shelf that cannot be filled; a tool needs no
 * reading and is always drawn.
 */
export function visibleKinds(
  map: ServerMap | undefined,
  can: (capability: Capability) => boolean,
): KindDef[] {
  const sent = new Map((map?.kinds ?? []).map((kind) => [kind.key, kind]))
  return KINDS.filter((kind) => {
    if (kind.capability && !can(kind.capability)) return false
    if (kind.tool) return true
    const reading = sent.get(kind.key)
    return reading !== undefined && reading.state !== "absent"
  })
}

// ---------------------------------------------------------------------------
// The document: what the reader arranged, kept per account on the server.
// ---------------------------------------------------------------------------

export type Point = { x: number; y: number }

export type MapDocument = {
  version: 1
  /**
   * Where a box was put by hand, absolute, by node id. A thing pulled off its
   * shelf keeps its place here; a thing still on its shelf has none, since
   * its place is its slot in the shelf's grid.
   */
  positions: Record<string, Point>
  /** Branches and shelves folded to their heads (`branch:jobs`, `shelf:images`). */
  collapsed: string[]
  /**
   * Shelves opened that start folded: the ones in a section the account
   * keeps out of its sidebar. Kept apart from `collapsed` so a section shown
   * again later is not still folded by a choice nobody made.
   */
  opened: string[]
  /** Shelves drawing every one of their things rather than the first few. */
  expanded: string[]
  viewport?: { x: number; y: number; zoom: number }
  minimap: boolean
  /** Draw what the chosen box is wired to — its domains, its database, its volumes. */
  connections: boolean
}

export const DEFAULT_DOCUMENT: MapDocument = {
  version: 1,
  positions: {},
  collapsed: [],
  opened: [],
  expanded: [],
  minimap: true,
  connections: true,
}

const finite = (n: unknown): n is number => typeof n === "number" && Number.isFinite(n)
const isRecord = (v: unknown): v is Record<string, unknown> =>
  typeof v === "object" && v !== null && !Array.isArray(v)
const strings = (v: unknown) =>
  Array.isArray(v) ? [...new Set(v.filter((s): s is string => typeof s === "string"))] : []

/**
 * A saved document read back defensively: it was written by an older page,
 * or by hand through the API, and a field the canvas cannot use is dropped
 * rather than taking the canvas down with it.
 */
export function decodeDocument(raw: unknown): MapDocument {
  if (!isRecord(raw)) return DEFAULT_DOCUMENT
  const positions: Record<string, Point> = {}
  if (isRecord(raw.positions)) {
    for (const [id, p] of Object.entries(raw.positions)) {
      if (isRecord(p) && finite(p.x) && finite(p.y)) positions[id] = { x: p.x, y: p.y }
    }
  }
  const viewport =
    isRecord(raw.viewport) &&
    finite(raw.viewport.x) &&
    finite(raw.viewport.y) &&
    finite(raw.viewport.zoom) &&
    raw.viewport.zoom > 0
      ? { x: raw.viewport.x, y: raw.viewport.y, zoom: raw.viewport.zoom }
      : undefined
  return {
    version: 1,
    positions,
    collapsed: strings(raw.collapsed),
    opened: strings(raw.opened),
    expanded: strings(raw.expanded),
    viewport,
    minimap: typeof raw.minimap === "boolean" ? raw.minimap : DEFAULT_DOCUMENT.minimap,
    connections:
      typeof raw.connections === "boolean" ? raw.connections : DEFAULT_DOCUMENT.connections,
  }
}

/** Whether anything was arranged by hand: what "Forget this layout" would throw away. */
export function isArranged(doc: MapDocument) {
  return (
    Object.keys(doc.positions).length > 0 ||
    doc.collapsed.length > 0 ||
    doc.opened.length > 0 ||
    doc.expanded.length > 0
  )
}

/** The shelves that start folded: the ones in a section this account hides. */
export function foldedByDefault(kinds: readonly KindDef[], hidden: ReadonlySet<string>) {
  return new Set(
    kinds.filter((kind) => kind.section && hidden.has(kind.section)).map((k) => shelfId(k.key)),
  )
}

/** Folded, whether by the reader or because the account hides the section. */
export function isFolded(doc: MapDocument, id: string, byDefault: ReadonlySet<string>) {
  if (doc.collapsed.includes(id)) return true
  return byDefault.has(id) && !doc.opened.includes(id)
}

/** The document after a branch or a shelf is folded or opened. */
export function toggleFold(
  doc: MapDocument,
  id: string,
  byDefault: ReadonlySet<string>,
): MapDocument {
  const folded = isFolded(doc, id, byDefault)
  const without = (list: string[]) => list.filter((entry) => entry !== id)
  if (folded) {
    return {
      ...doc,
      collapsed: without(doc.collapsed),
      opened: byDefault.has(id) ? [...without(doc.opened), id] : without(doc.opened),
    }
  }
  return {
    ...doc,
    collapsed: [...without(doc.collapsed), id],
    opened: without(doc.opened),
  }
}

// ---------------------------------------------------------------------------
// Where every box goes.
// ---------------------------------------------------------------------------

export const SIZE = {
  server: { width: 340, height: 148 },
  branch: { width: 216, height: 76 },
  item: { width: 220, height: 60 },
  shelfHead: 48,
  shelfPad: 12,
  gap: 10,
  /** Server → branch, and branch → shelf: room for the line to bend. */
  reach: 128,
  shelfReach: 104,
  shelfGap: 28,
  branchGap: 64,
  minShelfWidth: 280,
} as const

/** How many of a shelf's things are drawn before "+N more" — two rows of its grid. */
export function shelfCap(kind: KindDef) {
  return (kind.cols ?? 3) * 2
}

export const shelfId = (kind: string) => `shelf:${kind}`
export const branchId = (branch: BranchKey) => `branch:${branch}`
export const moreId = (kind: string) => `more:${kind}`
export const addId = (kind: string) => `add:${kind}`
export const toolId = (kind: string) => `tool:${kind}`

export type LaidNode =
  | { id: "server"; type: "server"; position: Point; width: number; height: number }
  | {
      id: string
      type: "branch"
      branch: Branch
      position: Point
      width: number
      height: number
      folded: boolean
      kinds: string[]
    }
  | {
      id: string
      type: "shelf"
      kind: KindDef
      branch: Branch
      position: Point
      width: number
      height: number
      folded: boolean
      expanded: boolean
      /** Things off the shelf, by id: placed by hand elsewhere. */
      away: string[]
    }
  | {
      id: string
      type: "item"
      item: MapItem
      kind: KindDef
      side: Branch["side"]
      position: Point
      width: number
      height: number
      /** On its shelf (position relative to the shelf), or placed by hand (absolute). */
      parentId?: string
    }
  | {
      id: string
      type: "more" | "add" | "tool"
      kind: KindDef
      position: Point
      width: number
      height: number
      parentId: string
      /** What "+N more" stands for. */
      count?: number
    }

export type LaidEdge = {
  id: string
  source: string
  target: string
  /** The side of the source the line leaves from; the target's opposite side receives it. */
  side: Branch["side"]
  tier: "branch" | "shelf" | "away"
}

export type Laid = { nodes: LaidNode[]; edges: LaidEdge[] }

type ShelfPlan = {
  kind: KindDef
  reading?: MapKindReading
  folded: boolean
  expanded: boolean
  cols: number
  /** The things drawn on the shelf, in order; the others are off it or behind "+N more". */
  onShelf: MapItem[]
  away: MapItem[]
  more: number
  slots: ("add" | "tool" | "more")[]
  width: number
  height: number
}

function planShelf(
  kind: KindDef,
  reading: MapKindReading | undefined,
  doc: MapDocument,
  byDefault: ReadonlySet<string>,
): ShelfPlan {
  const id = shelfId(kind.key)
  const folded = isFolded(doc, id, byDefault)
  const expanded = doc.expanded.includes(id)
  const items = reading?.items ?? []
  const away = items.filter((item) => doc.positions[item.id] !== undefined)
  const staying = items.filter((item) => doc.positions[item.id] === undefined)
  const cap = shelfCap(kind)
  const total = Math.max(reading?.total ?? 0, items.length)
  // The things the server held back (a capped list) count towards "more" too.
  const unsent = total - items.length
  let onShelf = staying
  let more = unsent
  if (!expanded && staying.length + (unsent > 0 ? 1 : 0) > cap) {
    onShelf = staying.slice(0, cap - 1)
    more = staying.length - onShelf.length + unsent
  }
  const slots: ShelfPlan["slots"] = []
  if (kind.tool) slots.push("tool")
  else if (items.length === 0 && kind.add && reading?.state !== "unread") slots.push("add")
  if (more > 0) slots.push("more")
  const cells = onShelf.length + slots.length
  const cols = Math.max(1, Math.min(kind.cols ?? 3, Math.max(cells, 1)))
  const rows = Math.ceil(cells / cols)
  const gridWidth = cols * SIZE.item.width + (cols - 1) * SIZE.gap
  const width = Math.max(SIZE.minShelfWidth, gridWidth + SIZE.shelfPad * 2)
  const gridHeight = rows > 0 ? rows * SIZE.item.height + (rows - 1) * SIZE.gap : 0
  const height = folded ? SIZE.shelfHead : SIZE.shelfHead + gridHeight + SIZE.shelfPad
  return { kind, reading, folded, expanded, cols, onShelf, away, more, slots, width, height }
}

/** A cell of a shelf's grid, relative to the shelf. */
function cell(index: number, cols: number): Point {
  const col = index % cols
  const row = Math.floor(index / cols)
  return {
    x: SIZE.shelfPad + col * (SIZE.item.width + SIZE.gap),
    y: SIZE.shelfHead + row * (SIZE.item.height + SIZE.gap),
  }
}

/**
 * Every box and line of the map.
 *
 * The server's middle is the origin. Each side's branches are stacked and
 * centred on the server's line; each branch sits level with the middle of
 * its shelves; each shelf stands out from its branch with its grid reading
 * left to right whichever side it is on, so a shelf on the left keeps its
 * right edge towards the server. A box placed by hand keeps its place; the
 * rest is where the layout would put it, so a server that gains a container
 * puts the new one on its shelf without moving what the reader arranged.
 */
export function layoutMap({
  map,
  kinds,
  doc,
  byDefault = new Set(),
}: {
  map: ServerMap | undefined
  kinds: readonly KindDef[]
  doc: MapDocument
  byDefault?: ReadonlySet<string>
}): Laid {
  const readings = new Map((map?.kinds ?? []).map((kind) => [kind.key, kind]))
  const nodes: LaidNode[] = []
  const edges: LaidEdge[] = []
  const placed = (id: string, auto: Point) => doc.positions[id] ?? auto

  nodes.push({
    id: "server",
    type: "server",
    position: placed("server", { x: -SIZE.server.width / 2, y: -SIZE.server.height / 2 }),
    ...SIZE.server,
  })

  for (const side of ["right", "left"] as const) {
    const branches = BRANCHES.filter((branch) => branch.side === side)
      .map((branch) => {
        const planned = kinds
          .filter((kind) => kind.branch === branch.key)
          .map((kind) => planShelf(kind, readings.get(kind.key), doc, byDefault))
        // One width for every shelf on a branch, so the shelves stand as one
        // column with both edges straight rather than a ragged fan.
        const width = Math.max(0, ...planned.map((shelf) => shelf.width))
        const shelves = planned.map((shelf) => ({ ...shelf, width }))
        const folded = isFolded(doc, branchId(branch.key), byDefault)
        const shelvesHeight =
          shelves.reduce((sum, shelf) => sum + shelf.height, 0) +
          Math.max(0, shelves.length - 1) * SIZE.shelfGap
        const height = folded ? SIZE.branch.height : Math.max(SIZE.branch.height, shelvesHeight)
        return { branch, shelves, folded, height, shelvesHeight }
      })
      .filter((plan) => plan.shelves.length > 0)
    const total =
      branches.reduce((sum, plan) => sum + plan.height, 0) +
      Math.max(0, branches.length - 1) * SIZE.branchGap
    let top = -total / 2
    const dir = side === "right" ? 1 : -1
    const branchX =
      side === "right"
        ? SIZE.server.width / 2 + SIZE.reach
        : -SIZE.server.width / 2 - SIZE.reach - SIZE.branch.width
    for (const plan of branches) {
      const id = branchId(plan.branch.key)
      const auto = { x: branchX, y: top + plan.height / 2 - SIZE.branch.height / 2 }
      const position = placed(id, auto)
      // A branch moved by hand takes its shelves with it: the shelves are laid
      // out against where the branch is, not where it would have been.
      const shift = { x: position.x - auto.x, y: position.y - auto.y }
      nodes.push({
        id,
        type: "branch",
        branch: plan.branch,
        position,
        ...SIZE.branch,
        folded: plan.folded,
        kinds: plan.shelves.map((shelf) => shelf.kind.key),
      })
      edges.push({ id: `e:server:${id}`, source: "server", target: id, side, tier: "branch" })

      let shelfTop = top + (plan.height - plan.shelvesHeight) / 2
      for (const shelf of plan.shelves) {
        const sid = shelfId(shelf.kind.key)
        const shelfAuto = {
          x:
            dir === 1
              ? branchX + SIZE.branch.width + SIZE.shelfReach + shift.x
              : branchX - SIZE.shelfReach - shelf.width + shift.x,
          y: shelfTop + shift.y,
        }
        shelfTop += shelf.height + SIZE.shelfGap
        if (plan.folded) continue
        const shelfAt = placed(sid, shelfAuto)
        nodes.push({
          id: sid,
          type: "shelf",
          kind: shelf.kind,
          branch: plan.branch,
          position: shelfAt,
          width: shelf.width,
          height: shelf.height,
          folded: shelf.folded,
          expanded: shelf.expanded,
          away: shelf.away.map((item) => item.id),
        })
        edges.push({ id: `e:${id}:${sid}`, source: id, target: sid, side, tier: "shelf" })
        if (!shelf.folded) {
          shelf.onShelf.forEach((item, index) => {
            nodes.push({
              id: item.id,
              type: "item",
              item,
              kind: shelf.kind,
              side,
              position: cell(index, shelf.cols),
              ...SIZE.item,
              parentId: sid,
            })
          })
          shelf.slots.forEach((slot, offset) => {
            const index = shelf.onShelf.length + offset
            nodes.push({
              id:
                slot === "more"
                  ? moreId(shelf.kind.key)
                  : slot === "add"
                    ? addId(shelf.kind.key)
                    : toolId(shelf.kind.key),
              type: slot,
              kind: shelf.kind,
              position: cell(index, shelf.cols),
              ...SIZE.item,
              parentId: sid,
              count: slot === "more" ? shelf.more : undefined,
            })
          })
        }
        // A thing off its shelf is drawn wherever it was put, folded shelf or
        // not: it was taken out to be seen.
        for (const item of shelf.away) {
          nodes.push({
            id: item.id,
            type: "item",
            item,
            kind: shelf.kind,
            side,
            position: doc.positions[item.id],
            ...SIZE.item,
          })
          edges.push({
            id: `e:${sid}:${item.id}`,
            source: sid,
            target: item.id,
            side,
            tier: "away",
          })
        }
      }
      top += plan.height + SIZE.branchGap
    }
  }
  return { nodes, edges }
}

/**
 * The boxes that move with one being dragged: a branch takes its shelves and
 * whatever was pulled off them, the server takes everything, a shelf carries
 * its own grid already (the things on it are its children) and takes only
 * the things pulled off it. A thing moves alone.
 */
export function followersOf(laid: Laid, id: string): string[] {
  if (id === "server")
    return laid.nodes.filter((n) => n.id !== "server" && !hasParent(n)).map((n) => n.id)
  const node = laid.nodes.find((n) => n.id === id)
  if (!node) return []
  if (node.type === "branch") {
    const shelves = laid.nodes.filter(
      (n): n is Extract<LaidNode, { type: "shelf" }> =>
        n.type === "shelf" && n.branch.key === node.branch.key,
    )
    return [...shelves.map((s) => s.id), ...shelves.flatMap((s) => s.away)]
  }
  if (node.type === "shelf") return node.away
  return []
}

function hasParent(node: LaidNode) {
  return "parentId" in node && node.parentId !== undefined
}

// ---------------------------------------------------------------------------
// Reading the map: what a box is wired to, and what a search finds.
// ---------------------------------------------------------------------------

export type Related = {
  item: MapItem
  kind: string
  via: string
  /** The other ways the server knows the pair is joined. */
  also: string[]
  direction: "to" | "from"
}

/**
 * What one thing is wired to, both ways: a project's domains and its
 * database, a container's volumes and the stack it is in. Only what the
 * server sent as a link; nothing is guessed from names.
 */
export function relatedTo(map: ServerMap | undefined, id: string): Related[] {
  if (!map) return []
  const where = itemIndex(map)
  const out: Related[] = []
  const seen = new Set<string>()
  for (const link of map.links) {
    const other = link.from === id ? link.to : link.to === id ? link.from : null
    if (!other || other === id || seen.has(other)) continue
    const found = where.get(other)
    if (!found) continue
    seen.add(other)
    out.push({
      item: found.item,
      kind: found.kind,
      via: link.via,
      also: link.also ?? [],
      direction: link.from === id ? "to" : "from",
    })
  }
  return out.sort(
    (a, b) =>
      KINDS.findIndex((k) => k.key === a.kind) - KINDS.findIndex((k) => k.key === b.kind) ||
      a.item.name.localeCompare(b.item.name),
  )
}

const indexes = new WeakMap<ServerMap, Map<string, { item: MapItem; kind: string }>>()

/** Every item by id, built once per reading. */
export function itemIndex(map: ServerMap) {
  let index = indexes.get(map)
  if (!index) {
    index = new Map()
    for (const kind of map.kinds) {
      for (const item of kind.items) index.set(item.id, { item, kind: kind.key })
    }
    indexes.set(map, index)
  }
  return index
}

/** The links worth drawing for one chosen thing: its own, to things on the map. */
export function linksOf(map: ServerMap | undefined, id: string): MapLink[] {
  if (!map) return []
  const where = itemIndex(map)
  return map.links.filter(
    (link) => (link.from === id || link.to === id) && where.has(link.from) && where.has(link.to),
  )
}

/** The things a search names: by name, by what it says of itself, or by its kind. */
export function searchMap(map: ServerMap | undefined, query: string): Set<string> | null {
  const q = query.trim().toLowerCase()
  if (!q || !map) return null
  const out = new Set<string>()
  for (const kind of map.kinds) {
    const def = kindOf(kind.key)
    const kindHit = def ? def.label.toLowerCase().includes(q) : false
    for (const item of kind.items) {
      if (
        kindHit ||
        item.name.toLowerCase().includes(q) ||
        (item.detail ?? "").toLowerCase().includes(q) ||
        (item.product ?? "").toLowerCase().includes(q)
      ) {
        out.add(item.id)
      }
    }
  }
  return out
}

/** How many things the map holds, and how many of them are down, for the toolbar. */
export function census(map: ServerMap | undefined, kinds: readonly KindDef[]) {
  const shown = new Set(kinds.map((kind) => kind.key))
  let things = 0
  let down = 0
  for (const kind of map?.kinds ?? []) {
    if (!shown.has(kind.key)) continue
    things += Math.max(kind.total, kind.items.length)
    down += kind.items.filter((item) => item.tone === "danger").length
  }
  return { things, down }
}

/** The shelf a thing sits on, by the reading it came in. */
export function kindOfItem(map: ServerMap | undefined, id: string) {
  return map ? itemIndex(map).get(id)?.kind : undefined
}

// ---------------------------------------------------------------------------
// Where a box dropped by hand may land.
// ---------------------------------------------------------------------------

export type Rect = { x: number; y: number; width: number; height: number }

function overlaps(a: Rect, b: Rect, gap: number) {
  return (
    a.x < b.x + b.width + gap &&
    b.x < a.x + a.width + gap &&
    a.y < b.y + b.height + gap &&
    b.y < a.y + a.height + gap
  )
}

/**
 * The nearest place to where a box was dropped that covers nothing else on
 * the map. A thing pulled off its shelf and let go over another shelf was
 * drawn on top of that shelf's things, looking dropped into a shelf it is not
 * on; it slides instead to the closest clear ground, searched ring by ring
 * outwards from the drop, nearest first. Where nothing is clear within reach
 * it stays where it was let go.
 */
export function freeSpot(
  want: Rect,
  others: readonly Rect[],
  { gap = 14, step = 16, reach = 1600 } = {},
): Point {
  const clear = (x: number, y: number) =>
    !others.some((other) => overlaps({ ...want, x, y }, other, gap))
  if (clear(want.x, want.y)) return { x: want.x, y: want.y }
  for (let r = step; r <= reach; r += step) {
    const ring: Point[] = []
    for (let d = -r; d <= r; d += step) {
      ring.push({ x: d, y: -r }, { x: d, y: r })
      if (d !== -r && d !== r) ring.push({ x: -r, y: d }, { x: r, y: d })
    }
    ring.sort((a, b) => Math.hypot(a.x, a.y) - Math.hypot(b.x, b.y))
    for (const offset of ring) {
      if (clear(want.x + offset.x, want.y + offset.y)) {
        return { x: want.x + offset.x, y: want.y + offset.y }
      }
    }
  }
  return { x: want.x, y: want.y }
}
