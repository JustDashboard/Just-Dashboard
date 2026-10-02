"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import Link from "next/link"
import {
  Background,
  BackgroundVariant,
  MarkerType,
  MiniMap,
  ReactFlow,
  ReactFlowProvider,
  getViewportForBounds,
  useNodesState,
  useOnSelectionChange,
  useReactFlow,
  type Node,
  type Viewport,
} from "@xyflow/react"
import "@xyflow/react/dist/style.css"
import {
  ArrowLeftRight,
  ArrowUpDown,
  Check,
  Code,
  Copy,
  Cross,
  Crosshair,
  Download,
  Eye,
  EyeOff,
  Fullscreen,
  FullscreenClose,
  GridSquare,
  Image as ImageIcon,
  Inspect,
  Notes,
  RotateCounterClockwise,
  SettingsSliders,
  SidebarRightClose,
  SidebarRightOpen,
  Table as TableIcon,
  Trash,
} from "@/components/icons"
import { get } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { plural } from "@/lib/format"
import { hueFor, LANES } from "@/lib/hue"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useViewState } from "@/lib/view-state"
import { useAuth } from "@/hooks/use-auth"
import { useMediaQuery } from "@/hooks/use-mobile"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { Segments } from "@/components/deploy/settings/segments"
import { FormFact } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { SearchInput } from "@/components/page"
import { Pane, PaneHeader } from "@/components/panel"
import { EmptyState } from "@/components/state"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Skeleton } from "@/components/ui/skeleton"
import { useCatalog } from "@/components/database/data/use-table"
import { ReadFailed } from "@/components/database/fleet/read-failed"
import { EngineMark } from "@/components/database/kit"
import { tableParams } from "@/components/database/schema/address"
import { useFocusReturn } from "@/components/database/schema/focus"
import { selectStatement } from "@/components/database/schema/select"
import { useSettledAddress } from "@/components/database/schema/use-settled-address"
import { useDatabase } from "@/components/database/shell/database-context"
import {
  DiagramSchemaPicker,
  FIT,
  Legend,
  fitPadding,
  NoteDialog,
  Saved,
  ZoomControls,
} from "@/components/database/diagram/chrome"
import {
  DIAGRAM_COLORS,
  isArranged,
  migrateDocument,
  type DiagramColor,
  type DiagramDocument,
} from "@/components/database/diagram/document"
import {
  downloadPng,
  downloadText,
  renderSvg,
  toDbml,
  toJson,
  toMermaid,
} from "@/components/database/diagram/export"
import type { DiagramDetail } from "@/components/database/diagram/geometry"
import { Inspector } from "@/components/database/diagram/inspector"
import {
  applyPositions,
  buildEdges,
  edgeSides,
  layoutGraph,
  neighbourhood,
} from "@/components/database/diagram/layout"
import { useDiagramMemory, type MemoryStatus } from "@/components/database/diagram/memory"
import { RelationEdge, type RelationEdgeData } from "@/components/database/diagram/relation-edge"
import { NODE_MENU, TableNode, type TableNodeData } from "@/components/database/diagram/table-node"
import {
  readGraph,
  type DbGraphEdge,
  type DbGraphTable,
  type DbSchemaGraph,
} from "@/components/database/diagram/types"

const nodeTypes = { table: TableNode }
const edgeTypes = { relation: RelationEdge }

/** The most tables the server will put in one picture. */
const MOST = 1000
/** What "show more" asks for first: enough for most schemas, still a readable picture. */
const MORE = 400

/**
 * Below this the names on the canvas stop being read: a table's name is set
 * at 12px and its columns at 11, so seven tenths is eight pixels of type.
 */
const READABLE = 0.7
/** The picture never opens larger than this, however few tables it holds. */
const ARRIVE_AT_MOST = 1.15
const ZOOM = { min: 0.08, max: 2.5 }

/** How many tables the address asks for; 0 is the server's own default. */
function readLimit(raw: string): number {
  const n = Number(raw)
  return Number.isInteger(n) && n > 0 ? Math.min(n, MOST) : 0
}

/**
 * The schema, as a diagram you can work in and come back to.
 *
 * The tables are their columns, the edges land on the rows they relate, and
 * the canvas pans, zooms and drags — that much any schema tool does. What this
 * one adds is memory and reach. Every arrangement the operator makes — a
 * table dragged into place, a lookup table hidden, a note, a colour, the
 * level of detail — is saved against the connection and comes back on the
 * next visit (`memory.ts`). And a table on the canvas is a place to go from:
 * its menu opens it in Data, Schema or Query, focuses its neighbourhood, hides
 * it, colours it; the inspector beside the canvas reads its relations both
 * ways; the whole picture exports as SVG, PNG, Mermaid, DBML or JSON; and the
 * workbench goes full-screen for a schema that needs the whole monitor.
 *
 * Which schema it is a picture of is said in the toolbar and chosen there,
 * and a table is its schema and its name together everywhere: on the canvas,
 * in what is saved, in what is exported. A picture that had to leave tables
 * out says how many, and offers the rest.
 */
export function ErDiagram() {
  const { id, engine, selection, param, href, readOnly } = useDatabase()
  const { can } = useAuth()
  useSettledAddress()
  // Which picture: one schema's, or every schema's. "Every" is said in the
  // address in so many words (`every=1`), beside the schema the reader's
  // place is in — an address with no schema in it is completed from where
  // they last were, so leaving the schema out could not mean it. An address
  // with nothing to complete it from is the picture of everything too.
  const every = param("every") === "1" || selection.schema === ""
  const schema = every ? "" : selection.schema
  const limit = readLimit(param("limit"))
  const root = useRef<HTMLDivElement>(null)

  const read = usePoll(
    async (signal) =>
      readGraph(
        await get<Partial<DbSchemaGraph>>(
          `/databases/${id}/graph`,
          { schema, limit: limit || undefined },
          signal,
        ),
        schema,
      ),
    0,
    [id, schema, limit],
  )
  // The list of schemas to choose from; the catalogue is the route that has it.
  const catalog = useCatalog(id, schema)
  const memory = useDiagramMemory({ connId: id, schema, canSave: can("service.control") })
  const [fullscreen, setFullscreen] = useState(false)
  const [immersive, setImmersive] = useState(false)

  // A document written when a table was its bare name is re-keyed once the
  // picture says which table each name was.
  const graph = read.data
  const doc = memory.document
  const update = memory.update
  useEffect(() => {
    // As a function: laid over the document it replaces, the re-keyed one
    // would keep the mark that says it still needs re-keying.
    if (graph && doc?.legacy) update(() => migrateDocument(doc, graph.tables))
  }, [graph, doc, update])

  // Fullscreen: the browser's own where it is offered, and a fixed overlay
  // where it is refused (an iframe, a browser that asks and is told no) — the
  // operator asked for the whole window and gets it either way.
  const toggleFullscreen = useCallback(() => {
    if (fullscreen || immersive) {
      if (document.fullscreenElement) void document.exitFullscreen().catch(() => undefined)
      setImmersive(false)
      setFullscreen(false)
      return
    }
    const el = root.current
    if (el?.requestFullscreen) {
      el.requestFullscreen().then(
        () => setFullscreen(true),
        () => setImmersive(true),
      )
    } else {
      setImmersive(true)
    }
  }, [fullscreen, immersive])
  useEffect(() => {
    const onChange = () => {
      setFullscreen(document.fullscreenElement === root.current && root.current !== null)
      if (!document.fullscreenElement) setImmersive(false)
    }
    document.addEventListener("fullscreenchange", onChange)
    return () => document.removeEventListener("fullscreenchange", onChange)
  }, [])

  const picker = engine.can("schemas") ? (
    <DiagramSchemaPicker schemas={catalog.data?.schemas} current={schema} />
  ) : null
  const ready = graph && doc && !doc.legacy && graph.tables.length > 0
  const canCreate =
    can("service.control") && !readOnly && engine.capabilities.ddlOperations.includes("createTable")

  return (
    <div
      ref={root}
      data-slot="diagram"
      className={cn(
        "flex min-h-0 min-w-0 flex-1 flex-col bg-background [&:fullscreen]:p-3",
        immersive && "fixed inset-0 z-50 p-3",
      )}
    >
      {/* Framed: a canvas is a working region with its own scroll (§2). */}
      <Pane className="min-h-0 flex-1">
        {ready ? (
          <ReactFlowProvider>
            <Canvas
              // Another schema, or more of this one, is another picture.
              key={`${schema}:${limit}`}
              root={root}
              graph={graph}
              doc={doc}
              status={memory.status}
              updatedAt={memory.updatedAt}
              update={memory.update}
              reset={memory.reset}
              picker={picker}
              refresh={read.refresh}
              full={fullscreen || immersive}
              onFullscreen={toggleFullscreen}
              onLeaveImmersive={() => setImmersive(false)}
            />
          </ReactFlowProvider>
        ) : (
          <>
            <PaneHeader className="h-10">{picker}</PaneHeader>
            {read.error && !graph ? (
              <div className="p-4">
                <ReadFailed error={read.error} onRetry={read.refresh} />
              </div>
            ) : !graph || !doc || doc.legacy ? (
              <CanvasSkeleton />
            ) : (
              <div className="flex min-h-0 flex-1 items-center justify-center p-6">
                <EmptyState
                  className="border-0"
                  mark={<EngineMark engine={engine} />}
                  title={`No tables in ${schema || `this ${engine.nouns.container}`}`}
                  description="A schema's tables and views are drawn here, with the keys between them."
                  action={
                    <div className="flex flex-wrap justify-center gap-2">
                      {canCreate && engine.has("schema") && (
                        <Button size="sm" variant="outline" asChild>
                          <Link href={href("schema", { table: null, new: "table" })}>
                            New {engine.nouns.object}
                          </Link>
                        </Button>
                      )}
                      {schema !== "" && engine.can("schemas") && (
                        <Button size="sm" variant="outline" asChild>
                          <Link href={href("diagram", { every: "1", limit: null })}>
                            Show every {engine.nouns.container}
                          </Link>
                        </Button>
                      )}
                    </div>
                  }
                />
              </div>
            )}
          </>
        )}
      </Pane>
    </div>
  )
}

/** The coming silhouette: a few tables, each a header and its rows. */
function CanvasSkeleton() {
  const tables = [
    { left: "8%", top: "12%", rows: 5 },
    { left: "40%", top: "8%", rows: 7 },
    { left: "40%", top: "58%", rows: 3 },
    { left: "72%", top: "30%", rows: 4 },
  ]
  return (
    <div
      role="status"
      aria-label="Reading the schema"
      className="relative min-h-0 flex-1 overflow-hidden"
    >
      {tables.map((table, index) => (
        <div
          key={index}
          aria-hidden
          className="absolute w-52 space-y-2 rounded-lg border border-hairline p-2.5"
          style={{ left: table.left, top: table.top }}
        >
          <Skeleton className="h-3.5 w-24" />
          {Array.from({ length: table.rows }, (_, row) => (
            <Skeleton key={row} className="h-2.5 w-full" />
          ))}
        </div>
      ))}
    </div>
  )
}

type Menu = { table: DbGraphTable; x: number; y: number }

function Canvas({
  root,
  graph,
  doc,
  status,
  updatedAt,
  update,
  reset,
  picker,
  refresh,
  full,
  onFullscreen,
  onLeaveImmersive,
}: {
  root: React.RefObject<HTMLDivElement | null>
  graph: DbSchemaGraph
  doc: DiagramDocument
  status: MemoryStatus
  updatedAt?: string
  update: (next: Partial<DiagramDocument> | ((doc: DiagramDocument) => DiagramDocument)) => void
  reset: () => Promise<void>
  picker: React.ReactNode
  refresh: () => void
  full: boolean
  onFullscreen: () => void
  onLeaveImmersive: () => void
}) {
  const { conn, engine, select, href, goto } = useDatabase()
  const flow = useReactFlow()
  const { confirm, dialog } = useConfirm()
  useFocusReturn((dialog.props as { request: unknown }).request !== null)
  const searchRef = useRef<HTMLInputElement>(null)
  const [focus, setFocus] = useState<string | null>(null)
  const [pickedEdge, setPickedEdge] = useState<string | null>(null)
  const [hoverEdge, setHoverEdge] = useState<string | null>(null)
  const [query, setQuery] = useState("")
  const [menu, setMenu] = useState<Menu | null>(null)
  const menuFor = useRef("")
  const [noteFor, setNoteFor] = useState<DbGraphTable | null>(null)
  // The inspector is what a table or a relation says about itself, so it
  // opens when one is chosen and gives the canvas back when nothing is. A
  // reader who wants it beside the canvas all the time pins it. On a narrow
  // window it would take the lower half of a canvas that has none to spare:
  // there it opens only when asked for, and is closed on every arrival.
  const wide = useMediaQuery("(min-width: 1024px)")
  const [pinned, setPinned] = useViewState("databases.diagram.inspector", false)
  const [askedFor, setAskedFor] = useState(false)
  const [closedOn, setClosedOn] = useState<string | null>(null)
  const [selectedCount, setSelectedCount] = useState(0)
  // Bumped by Tidy and Reset, which change nothing the layout is keyed on but
  // still mean "lay it out again".
  const [epoch, setEpoch] = useState(0)

  const byId = useMemo(() => new Map(graph.tables.map((t) => [t.id, t])), [graph.tables])
  // More than one schema in the picture: a name is said with its schema.
  const qualified = useMemo(
    () => new Set(graph.tables.map((t) => t.schema)).size > 1,
    [graph.tables],
  )
  const schemas = useMemo(
    () => [...new Set(graph.tables.map((t) => t.schema))].filter(Boolean).sort(),
    [graph.tables],
  )
  const nameOf = useCallback(
    (table: DbGraphTable) =>
      qualified && table.schema ? `${table.schema}.${table.name}` : table.name,
    [qualified],
  )
  const hidden = useMemo(() => new Set(doc.hidden), [doc.hidden])
  const notesKey = JSON.stringify(doc.notes)
  const layoutOpts = useMemo(
    () => ({
      direction: doc.direction,
      detail: doc.detail,
      spacing: doc.spacing,
      hidden,
      notes: doc.notes,
    }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [doc.direction, doc.detail, doc.spacing, hidden, notesKey],
  )
  const laid = useMemo(() => layoutGraph(graph, layoutOpts), [graph, layoutOpts])
  const [nodes, setNodes, onNodesChange] = useNodesState<Node>(laid.nodes)

  // The saved positions are read through a ref: a drag writes them, and a
  // drag must not be the thing that lays the diagram out again.
  const positions = useRef(doc.positions)
  useEffect(() => {
    positions.current = doc.positions
  }, [doc.positions])
  const restored = useRef(false)

  /**
   * The picture fitted to the canvas — as much of it as can be read. A schema
   * of ten tables used to open with its names five pixels tall, every table
   * on screen and none of them legible: a picture is for reading, so what is
   * fitted is the widest part of it that leaves the type readable.
   */
  const canvas = useRef<HTMLDivElement>(null)
  const minimap = doc.minimap
  const fitPicture = useCallback(
    (duration: number, all = false) => {
      const nodes = flow.getNodes()
      const box = canvas.current?.getBoundingClientRect()
      if (nodes.length === 0 || !box || box.width === 0) return
      // "Fit to view" is the reader asking for everything, however small.
      const padding = fitPadding(minimap && box.width >= 640)
      if (all) return void flow.fitView({ padding, duration })
      const zoomFor = (part: Node[]) =>
        getViewportForBounds(
          flow.getNodesBounds(part),
          box.width,
          box.height,
          ZOOM.min,
          ZOOM.max,
          padding,
        ).zoom
      // What is fitted on arrival, widest first: everything; the tables that
      // have relations; the best-connected table with what it touches. The
      // first that can be read is the one drawn, and the last is drawn at the
      // floor even if it overflows — the rest is a pan away, on the minimap.
      const degree = new Map<string, number>()
      for (const e of graph.edges) {
        if (e.from === e.to) continue
        degree.set(e.from, (degree.get(e.from) ?? 0) + 1)
        degree.set(e.to, (degree.get(e.to) ?? 0) + 1)
      }
      const related = nodes.filter((n) => degree.has(n.id))
      const hub = [...related].sort((a, b) => (degree.get(b.id) ?? 0) - (degree.get(a.id) ?? 0))[0]
      const around = hub ? neighbourhood(graph, hub.id) : null
      const tiers = [nodes, related, around ? nodes.filter((n) => around.has(n.id)) : []].filter(
        (tier) => tier.length > 0,
      )
      const target = tiers.find((tier) => zoomFor(tier) >= READABLE) ?? tiers[tiers.length - 1]
      void flow.fitView({
        nodes: target.length === nodes.length ? undefined : target.map((n) => ({ id: n.id })),
        padding,
        duration,
        minZoom: READABLE,
        maxZoom: ARRIVE_AT_MOST,
      })
    },
    [flow, graph, minimap],
  )

  // Re-laying out is a deliberate act — changing the direction or the detail,
  // hiding a table, pressing Tidy — not something that happens under a drag.
  useEffect(() => {
    setNodes(applyPositions(graph, laid, positions.current, layoutOpts))
    if (restored.current) {
      window.setTimeout(() => fitPicture(FIT.duration), 0)
      return
    }
    restored.current = true
    // The first paint: where the operator left the viewport, or fitted.
    window.setTimeout(() => {
      if (doc.viewport) void flow.setViewport(doc.viewport)
      else fitPicture(0)
    }, 0)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [laid, epoch, setNodes, flow])

  // The window changed size under the canvas: the picture is fitted again.
  const sized = useRef(full)
  useEffect(() => {
    if (sized.current === full) return
    sized.current = full
    const timer = window.setTimeout(() => fitPicture(FIT.duration), 60)
    return () => window.clearTimeout(timer)
  }, [full, fitPicture])

  // Edges follow the tables: the side an edge leaves by is decided by where
  // the two tables are now, so dragging one across the diagram re-routes it.
  // They are rebuilt when a table changes sides, not on every frame of a drag.
  const sides = edgeSides(graph, nodes)
  const edges = useMemo(() => buildEdges(graph, sides, doc.detail), [graph, sides, doc.detail])

  useOnSelectionChange({
    onChange: useCallback(
      ({ nodes: picked }: { nodes: Node[] }) => setSelectedCount(picked.length),
      [],
    ),
  })

  /** Everything the focused table touches, in both directions. */
  const near = useMemo(() => (focus ? neighbourhood(graph, focus) : null), [focus, graph])

  /** Tables the search names, and which of their columns answered. */
  const matches = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return null
    const out = new Map<string, string[]>()
    for (const t of graph.tables) {
      if (hidden.has(t.id)) continue
      const cols = t.columns.filter((c) => c.name.toLowerCase().includes(q)).map((c) => c.name)
      if (t.id.toLowerCase().includes(q) || cols.length > 0) out.set(t.id, cols)
    }
    return out
  }, [query, graph.tables, hidden])

  const panTo = useCallback(
    (table: string) => {
      void flow.fitView({ nodes: [{ id: table }], padding: 0.6, duration: 300, maxZoom: 1.2 })
    },
    [flow],
  )

  const focusTable = useCallback((table: string | null) => {
    setFocus(table)
    setPickedEdge(null)
  }, [])

  /** A table's own menu button on the canvas: where the keyboard goes back to. */
  const menuButton = (table: string) =>
    root.current?.querySelector<HTMLElement>(`[${NODE_MENU}="${CSS.escape(table)}"]`) ?? null

  const openMenu = useCallback((table: DbGraphTable, at: { x: number; y: number }) => {
    menuFor.current = table.id
    setMenu({ table, ...at })
  }, [])

  /** The positions of every table on the canvas, as the document keeps them. */
  const snapshotPositions = useCallback(() => {
    const out: Record<string, { x: number; y: number }> = { ...positions.current }
    for (const n of flow.getNodes()) out[n.id] = { x: n.position.x, y: n.position.y }
    return out
  }, [flow])

  const tidy = () => {
    const next = layoutGraph(graph, layoutOpts)
    update((d) => ({
      ...d,
      positions: Object.fromEntries(next.nodes.map((n) => [n.id, n.position])),
    }))
    setEpoch((e) => e + 1)
  }

  const hide = useCallback(
    (ids: string[]) => {
      if (ids.length === 0) return
      update((d) => ({ ...d, hidden: [...new Set([...d.hidden, ...ids])] }))
      setFocus((f) => (f && ids.includes(f) ? null : f))
      const one = ids.length === 1 ? byId.get(ids[0]) : undefined
      notify.success(one ? `Hidden ${nameOf(one)}` : `Hidden ${plural(ids.length, "table")}`, {
        description: "Bring it back from the hidden list in the toolbar.",
      })
    },
    [update, byId, nameOf],
  )
  const show = (ids: string[]) =>
    update((d) => ({ ...d, hidden: d.hidden.filter((h) => !ids.includes(h)) }))

  const isolate = (table: string) => {
    const keep = neighbourhood(graph, table)
    hide(graph.tables.map((t) => t.id).filter((t) => !keep.has(t)))
    setFocus(table)
  }

  const unrelated = useMemo(() => {
    const connected = new Set<string>()
    for (const e of graph.edges) {
      if (e.from !== e.to) {
        connected.add(e.from)
        connected.add(e.to)
      }
    }
    return graph.tables.map((t) => t.id).filter((t) => !connected.has(t) && !hidden.has(t))
  }, [graph, hidden])

  const setColor = (table: string, color: DiagramColor | null) =>
    update((d) => {
      const colors = { ...d.colors }
      if (color) colors[table] = color
      else delete colors[table]
      return { ...d, colors }
    })

  const setNote = (table: string, note: string) =>
    update((d) => {
      const notes = { ...d.notes }
      if (note.trim()) notes[table] = note.trim()
      else delete notes[table]
      return { ...d, notes }
    })

  const alignSelected = (axis: "row" | "column") => {
    const picked = flow.getNodes().filter((n) => n.selected)
    if (picked.length < 2) return
    const gap = doc.spacing === "compact" ? 32 : 56
    const sorted = [...picked].sort((a, b) =>
      axis === "row" ? a.position.x - b.position.x : a.position.y - b.position.y,
    )
    let cursor = axis === "row" ? sorted[0].position.x : sorted[0].position.y
    const line = axis === "row" ? sorted[0].position.y : sorted[0].position.x
    const moved = new Map<string, { x: number; y: number }>()
    for (const n of sorted) {
      moved.set(n.id, axis === "row" ? { x: cursor, y: line } : { x: line, y: cursor })
      cursor += (axis === "row" ? (n.width ?? 264) : (n.height ?? 80)) + gap
    }
    setNodes((ns) => ns.map((n) => (moved.has(n.id) ? { ...n, position: moved.get(n.id)! } : n)))
    update((d) => ({ ...d, positions: { ...snapshotPositions(), ...Object.fromEntries(moved) } }))
  }

  const forget = () =>
    confirm({
      title: "Forget this layout",
      confirmLabel: "Forget layout",
      subject: {
        mark: <EngineMark engine={engine} size="sm" />,
        name: <span className="font-mono">{graph.schema || conn.name}</span>,
        facts: (
          <>
            <FormFact label="Placed">{Object.keys(doc.positions).length}</FormFact>
            <FormFact label="Hidden">{doc.hidden.length}</FormFact>
            <FormFact label="Notes">{Object.keys(doc.notes).length}</FormFact>
            <FormFact label="Coloured">{Object.keys(doc.colors).length}</FormFact>
          </>
        ),
      },
      description: (
        <p>
          Where every table was placed, which are hidden, and the notes and colours on them are
          thrown away, for everybody who opens this diagram. The schema itself is untouched.
        </p>
      ),
      action: async () => {
        await reset()
        setFocus(null)
        setEpoch((e) => e + 1)
      },
    })

  const openQuery = (table: DbGraphTable) =>
    goto("query", { sql: selectStatement(engine, table.schema, table.name) })

  // What the inspector would be about, and whether it is on screen.
  const subject = focus ?? pickedEdge
  const inspector = wide ? pinned || (subject !== null && closedOn !== subject) : askedFor
  const toggleInspector = () => {
    if (!wide) return setAskedFor(!inspector)
    setPinned(!inspector)
    // Closed over something chosen: it stays closed for that thing.
    setClosedOn(inspector ? subject : null)
  }

  // The handful of keys a diagram wants — fit, find, the inspector, hide what
  // is focused, Escape for everything transient — and only while the keyboard
  // is in the diagram: on `window` they fired from anywhere on the page.
  const onKey = (e: React.KeyboardEvent) => {
    const target = e.target as HTMLElement
    const typing =
      target.tagName === "INPUT" ||
      target.tagName === "TEXTAREA" ||
      target.isContentEditable ||
      target.closest("[role=dialog],[role=menu]") !== null
    if (e.key === "Escape") {
      if (typing) return
      if (focus || pickedEdge) focusTable(null)
      else onLeaveImmersive()
      return
    }
    if (typing || e.metaKey || e.ctrlKey || e.altKey) return
    if (e.key === "f") fitPicture(FIT.duration, true)
    else if (e.key === "/") {
      e.preventDefault()
      searchRef.current?.focus()
    } else if (e.key === "i") toggleInspector()
    else if (e.key === "h" && focus) hide([focus])
  }

  const active = useMemo(() => (matches ? new Set(matches.keys()) : near), [matches, near])
  const shown = useMemo(
    () =>
      nodes.map((n) => ({
        ...n,
        draggable: !doc.locked,
        data: {
          table: (n.data as { table: DbGraphTable }).table,
          dimmed: active ? !active.has(n.id) : false,
          focused: focus === n.id,
          detail: doc.detail,
          color: doc.colors[n.id],
          note: doc.notes[n.id],
          matches: matches?.get(n.id),
          locked: doc.locked,
          qualified,
          onOpen: (t: DbGraphTable) => focusTable(focus === t.id ? null : t.id),
          onMenu: openMenu,
        } satisfies TableNodeData,
      })),
    [
      nodes,
      active,
      focus,
      doc.detail,
      doc.colors,
      doc.notes,
      doc.locked,
      matches,
      qualified,
      focusTable,
      openMenu,
    ],
  )

  const shownEdges = useMemo(
    () =>
      edges.map((e) => {
        const rel = (e.data as { relation: DbGraphEdge }).relation
        const involved = active ? active.has(rel.from) && active.has(rel.to) : false
        const lit = involved || pickedEdge === e.id || hoverEdge === e.id
        return {
          ...e,
          data: {
            relation: rel,
            active: involved,
            dimmed: active ? !involved : false,
            selected: pickedEdge === e.id,
            hovered: hoverEdge === e.id,
            labelled: doc.labels,
          } satisfies RelationEdgeData,
          markerEnd: {
            type: MarkerType.ArrowClosed,
            width: 16,
            height: 16,
            color: lit ? "var(--color-chart-1)" : "var(--color-muted-foreground)",
          },
        }
      }),
    [edges, active, pickedEdge, hoverEdge, doc.labels],
  )

  const exportAs = async (kind: "svg" | "png" | "mermaid" | "dbml" | "json") => {
    const base = `${conn.name}${graph.schema ? `-${graph.schema}` : ""}`.replace(
      /[^A-Za-z0-9_.-]+/g,
      "-",
    )
    try {
      if (kind === "mermaid") downloadText(`${base}.mmd`, toMermaid(graph, hidden), "text/plain")
      else if (kind === "dbml")
        downloadText(`${base}.dbml`, toDbml(graph, hidden, doc.notes), "text/plain")
      else if (kind === "json") downloadText(`${base}.json`, toJson(graph, doc), "application/json")
      else {
        const svg = renderSvg({
          graph,
          nodes: flow.getNodes(),
          edges,
          detail: doc.detail,
          colors: doc.colors,
          notes: doc.notes,
          title: `${conn.name}${graph.schema ? ` · ${graph.schema}` : ""}`,
        })
        if (kind === "svg") downloadText(`${base}.svg`, svg, "image/svg+xml")
        else await downloadPng(`${base}.png`, svg)
      }
    } catch (err) {
      notify.error("Could not export the diagram", err)
    }
  }

  const focused = focus ? byId.get(focus) : undefined
  const picked = pickedEdge ? edges.find((e) => e.id === pickedEdge) : undefined
  const pickedRelation = picked ? (picked.data as { relation: DbGraphEdge }).relation : undefined
  const hits = matches ? matches.size : null
  const hiddenHere = graph.tables.filter((t) => hidden.has(t.id))
  const more = Math.min(graph.total, MOST)

  return (
    // The keys are the diagram's own: heard while focus is inside it.
    <div className="flex min-h-0 min-w-0 flex-1 flex-col" onKeyDown={onKey}>
      <PaneHeader className="min-h-10 flex-wrap gap-x-1.5 gap-y-1">
        {picker}
        <SearchInput
          ref={searchRef}
          dense
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && matches && matches.size > 0) {
              const first = matches.keys().next().value as string
              focusTable(first)
              panTo(first)
            }
            if (e.key === "Escape") {
              setQuery("")
              ;(e.target as HTMLInputElement).blur()
            }
          }}
          placeholder="Find a table or column"
          aria-label="Find a table or column"
          containerClassName="w-52 sm:w-52"
          trailing={
            query ? (
              <Button
                size="icon-xs"
                variant="ghost"
                aria-label="Clear the search"
                onClick={() => setQuery("")}
              >
                <Cross />
              </Button>
            ) : undefined
          }
        />
        {hits !== null && (
          <span aria-live="polite" className="numeric text-hint text-muted-foreground">
            {hits === 0 ? "no match" : plural(hits, "match", "matches")}
          </span>
        )}

        <Segments<DiagramDetail>
          label="How much of each table is drawn"
          value={doc.detail}
          options={[
            { value: "all", label: "Columns" },
            { value: "keys", label: "Keys" },
            { value: "names", label: "Names" },
          ]}
          onChange={(detail) => update({ detail })}
          className="ml-1 [&_button]:h-7 [&_button]:px-2 [&_button]:text-hint"
        />
        <IconAction
          label={doc.direction === "LR" ? "Lay out top to bottom" : "Lay out left to right"}
          className="size-7"
          onClick={() => update({ direction: doc.direction === "LR" ? "TB" : "LR" })}
        >
          {doc.direction === "LR" ? <ArrowLeftRight /> : <ArrowUpDown />}
        </IconAction>
        <IconAction label="Tidy: lay every table out again" className="size-7" onClick={tidy}>
          <RotateCounterClockwise />
        </IconAction>

        <span className="flex-1" />

        <Saved status={status} updatedAt={updatedAt} arranged={isArranged(doc)} />

        {hiddenHere.length > 0 && (
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button size="xs" variant="ghost" className="h-7 text-muted-foreground">
                <EyeOff />
                {hiddenHere.length} hidden
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="max-h-80 w-64 overflow-y-auto">
              <DropdownMenuItem onSelect={() => show(hiddenHere.map((t) => t.id))}>
                <Eye />
                Show all
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              {[...hiddenHere]
                .sort((a, b) => a.id.localeCompare(b.id))
                .map((table) => (
                  <DropdownMenuItem
                    key={table.id}
                    onSelect={() => show([table.id])}
                    className="font-mono text-xs"
                  >
                    {nameOf(table)}
                  </DropdownMenuItem>
                ))}
            </DropdownMenuContent>
          </DropdownMenu>
        )}

        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button size="xs" variant="ghost" className="h-7" aria-label="Diagram options">
              <SettingsSliders />
              <span className="max-md:sr-only">Options</span>
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-64">
            <DropdownMenuLabel>Canvas</DropdownMenuLabel>
            <DropdownMenuCheckboxItem
              checked={doc.grid}
              onCheckedChange={(v) => update({ grid: v })}
            >
              Show grid
            </DropdownMenuCheckboxItem>
            <DropdownMenuCheckboxItem
              checked={doc.snap}
              onCheckedChange={(v) => update({ snap: v })}
            >
              Snap to grid
            </DropdownMenuCheckboxItem>
            <DropdownMenuCheckboxItem
              checked={doc.minimap}
              onCheckedChange={(v) => update({ minimap: v })}
            >
              Minimap
            </DropdownMenuCheckboxItem>
            <DropdownMenuCheckboxItem
              checked={doc.labels}
              onCheckedChange={(v) => update({ labels: v })}
            >
              Label every relation
            </DropdownMenuCheckboxItem>
            <DropdownMenuCheckboxItem
              checked={doc.locked}
              onCheckedChange={(v) => update({ locked: v })}
            >
              Lock the layout
            </DropdownMenuCheckboxItem>
            <DropdownMenuSeparator />
            <DropdownMenuLabel>Spacing</DropdownMenuLabel>
            <DropdownMenuRadioGroup
              value={doc.spacing}
              onValueChange={(v) => update({ spacing: v as DiagramDocument["spacing"] })}
            >
              <DropdownMenuRadioItem value="comfortable">Comfortable</DropdownMenuRadioItem>
              <DropdownMenuRadioItem value="compact">Compact</DropdownMenuRadioItem>
            </DropdownMenuRadioGroup>
            <DropdownMenuSeparator />
            {selectedCount > 1 && (
              <>
                <DropdownMenuItem onSelect={() => alignSelected("row")}>
                  <ArrowLeftRight />
                  Arrange {selectedCount} selected in a row
                </DropdownMenuItem>
                <DropdownMenuItem onSelect={() => alignSelected("column")}>
                  <ArrowUpDown />
                  Arrange {selectedCount} selected in a column
                </DropdownMenuItem>
                <DropdownMenuSeparator />
              </>
            )}
            {unrelated.length > 0 && (
              <DropdownMenuItem onSelect={() => hide(unrelated)}>
                <EyeOff />
                Hide {plural(unrelated.length, "table")} with no relations
              </DropdownMenuItem>
            )}
            <DropdownMenuItem onSelect={refresh}>
              <RotateCounterClockwise />
              Read the schema again
            </DropdownMenuItem>
            <DropdownMenuItem variant="destructive" disabled={!isArranged(doc)} onSelect={forget}>
              <Trash />
              Forget this layout…
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>

        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button size="xs" variant="ghost" className="h-7" aria-label="Export the diagram">
              <Download />
              <span className="max-md:sr-only">Export</span>
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-48">
            <DropdownMenuLabel>A picture</DropdownMenuLabel>
            <DropdownMenuItem onSelect={() => void exportAs("png")}>
              <ImageIcon />
              PNG
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={() => void exportAs("svg")}>
              <ImageIcon />
              SVG
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuLabel>As text</DropdownMenuLabel>
            <DropdownMenuItem onSelect={() => void exportAs("mermaid")}>
              <Code />
              Mermaid
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={() => void exportAs("dbml")}>
              <Code />
              DBML
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={() => void exportAs("json")}>
              <Code />
              JSON, with this layout
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>

        <IconAction
          label={inspector ? "Hide the inspector (I)" : "Show the inspector (I)"}
          aria-pressed={inspector}
          className="size-7"
          onClick={toggleInspector}
        >
          {inspector ? <SidebarRightClose /> : <SidebarRightOpen />}
        </IconAction>
        <IconAction
          label={full ? "Leave full screen" : "Full screen"}
          aria-pressed={full}
          className="size-7"
          onClick={onFullscreen}
        >
          {full ? <FullscreenClose /> : <Fullscreen />}
        </IconAction>
      </PaneHeader>

      {graph.truncated && (
        <div
          role="status"
          className="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-b border-hairline px-3 py-1.5 text-hint text-muted-foreground"
        >
          <span>
            {plural(graph.tables.length, "table")} of {graph.total.toLocaleString("en-US")} are
            drawn: tables first, then views, in the server&rsquo;s order.
          </span>
          {graph.tables.length < more && (
            <Button
              size="xs"
              variant="outline"
              onClick={() =>
                select({ limit: String(graph.tables.length < MORE ? Math.min(MORE, more) : more) })
              }
            >
              Draw {graph.tables.length < MORE ? Math.min(MORE, more) : more}
            </Button>
          )}
          {graph.total > MOST && (
            <span>A picture holds {MOST.toLocaleString("en-US")} at most: choose a schema.</span>
          )}
        </div>
      )}

      <div
        className={cn(
          "grid min-h-0 flex-1",
          inspector
            ? "grid-cols-1 max-lg:grid-rows-[minmax(0,1fr)_minmax(0,42%)] lg:grid-cols-[minmax(0,1fr)_18rem]"
            : "grid-cols-1",
        )}
      >
        <div ref={canvas} className="relative min-h-0 min-w-0">
          <ReactFlow
            nodes={shown}
            edges={shownEdges}
            onNodesChange={onNodesChange}
            nodeTypes={nodeTypes}
            edgeTypes={edgeTypes}
            onNodeClick={(_, n) => focusTable(focus === n.id ? null : n.id)}
            onNodeDoubleClick={(_, n) => {
              const t = byId.get(n.id)
              if (t) goto("data", { schema: t.schema || null, table: t.name })
            }}
            onNodeContextMenu={(e, n) => {
              e.preventDefault()
              const t = byId.get(n.id)
              if (t) openMenu(t, { x: e.clientX, y: e.clientY })
            }}
            onNodeDragStop={() => update((d) => ({ ...d, positions: snapshotPositions() }))}
            onEdgeClick={(_, e) => {
              setPickedEdge((p) => (p === e.id ? null : e.id))
              setFocus(null)
            }}
            onEdgeMouseEnter={(_, e) => setHoverEdge(e.id)}
            onEdgeMouseLeave={() => setHoverEdge(null)}
            onPaneClick={() => focusTable(null)}
            // A null event is a programmatic move — the first fit, a pan to a
            // search hit — and not something the operator asked to keep.
            onMoveEnd={(event, viewport: Viewport) => {
              if (event && restored.current) update({ viewport })
            }}
            minZoom={ZOOM.min}
            maxZoom={ZOOM.max}
            proOptions={{ hideAttribution: true }}
            nodesConnectable={false}
            nodesDraggable={!doc.locked}
            elementsSelectable
            // A large schema is mostly off screen: what is not seen is not drawn.
            onlyRenderVisibleElements
            snapToGrid={doc.snap}
            snapGrid={[16, 16]}
            deleteKeyCode={null}
            aria-label={`Diagram of ${graph.schema || conn.name}`}
            className="[&_.react-flow\_\_handle]:!border-0"
          >
            {doc.grid && (
              <Background
                variant={BackgroundVariant.Dots}
                gap={24}
                size={1}
                // A prop rather than a class: React Flow's own
                // `.react-flow__background-pattern.dots` fill outranks a
                // utility on the circle, which left the dots at its default
                // grey — the brightest thing on the canvas.
                color="var(--grid-dot)"
              />
            )}
            {doc.minimap && (
              <MiniMap
                pannable
                zoomable
                ariaLabel="Minimap"
                className="!right-3 !bottom-3 !h-24 !w-40 overflow-hidden !rounded-md !border !bg-card max-sm:!hidden"
                maskColor="color-mix(in oklab, var(--color-background) 70%, transparent)"
                // Several schemas in one picture: each table is its schema's hue.
                nodeColor={(n) => {
                  const of = byId.get(n.id)?.schema
                  return qualified && of
                    ? `color-mix(in oklab, ${hueFor(of.toLowerCase(), LANES)} 70%, var(--color-muted))`
                    : "color-mix(in oklab, var(--color-chart-1) 55%, var(--color-muted))"
                }}
                nodeStrokeWidth={0}
              />
            )}
          </ReactFlow>

          <ZoomControls
            onFit={() => fitPicture(FIT.duration, true)}
            focus={focused ? nameOf(focused) : null}
            locked={doc.locked}
            onClear={() => focusTable(null)}
            onUnlock={() => update({ locked: false })}
          />
          <Legend schemas={qualified ? schemas : undefined} />
        </div>

        {inspector && (
          <Inspector
            graph={graph}
            doc={doc}
            hidden={hidden}
            qualified={qualified}
            focused={focused}
            relation={pickedRelation}
            canQuery={engine.has("query")}
            onFocus={(table) => {
              if (!byId.has(table) || hidden.has(table)) return
              focusTable(table)
              panTo(table)
            }}
            onToggleHidden={(table, visible) => (visible ? show([table]) : hide([table]))}
            onShowAll={() => show(hiddenHere.map((t) => t.id))}
            onColor={setColor}
            onNote={setNoteFor}
            onOpenTable={(t) => goto("data", { schema: t.schema || null, table: t.name })}
            onOpenStructure={(t) => goto("schema", { schema: t.schema || null, table: t.name })}
            onQuery={openQuery}
            hrefOutside={(tableSchema, table) => href("schema", tableParams(tableSchema, table))}
            onClose={toggleInspector}
          />
        )}
      </div>

      {/* One menu for every table, opened at the point it was asked for from —
          a table's own button or a right-click — rather than a menu per node.
          When it closes, the keyboard goes back to that table's button. */}
      <DropdownMenu open={menu !== null} onOpenChange={(open) => !open && setMenu(null)}>
        <DropdownMenuTrigger asChild>
          {/* Where the menu opens, not a control: the table's own button asked for it. */}
          <span
            aria-hidden
            className="pointer-events-none fixed size-0"
            style={{ left: menu?.x ?? 0, top: menu?.y ?? 0 }}
          />
        </DropdownMenuTrigger>
        {menu && (
          <DropdownMenuContent
            align="start"
            className="w-60"
            onCloseAutoFocus={(event) => {
              event.preventDefault()
              menuButton(menuFor.current)?.focus()
            }}
          >
            <DropdownMenuLabel className="truncate font-mono text-xs">
              {nameOf(menu.table)}
            </DropdownMenuLabel>
            <DropdownMenuItem
              onSelect={() =>
                goto("data", { schema: menu.table.schema || null, table: menu.table.name })
              }
            >
              <GridSquare />
              Open data
            </DropdownMenuItem>
            <DropdownMenuItem
              onSelect={() =>
                goto("schema", { schema: menu.table.schema || null, table: menu.table.name })
              }
            >
              <TableIcon />
              Open in Schema
            </DropdownMenuItem>
            {engine.has("query") && (
              <DropdownMenuItem onSelect={() => openQuery(menu.table)}>
                <Code />
                Open a SELECT in Query
              </DropdownMenuItem>
            )}
            <DropdownMenuSeparator />
            <DropdownMenuItem
              onSelect={() => focusTable(focus === menu.table.id ? null : menu.table.id)}
            >
              <Crosshair />
              {focus === menu.table.id ? "Clear focus" : "Focus its neighbourhood"}
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={() => isolate(menu.table.id)}>
              <Inspect />
              Show only this and its relations
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={() => setNoteFor(menu.table)}>
              <Notes />
              {doc.notes[menu.table.id] ? "Edit note…" : "Add a note…"}
            </DropdownMenuItem>
            <DropdownMenuSub>
              <DropdownMenuSubTrigger>
                {/* In the slot the other rows' glyphs stand in, so the words line up. */}
                <span aria-hidden className="flex size-4 items-center justify-center">
                  <span
                    className="size-2.5 rounded-full border"
                    style={{
                      background: doc.colors[menu.table.id]
                        ? `var(--tag-${doc.colors[menu.table.id]})`
                        : undefined,
                    }}
                  />
                </span>
                Colour
              </DropdownMenuSubTrigger>
              <DropdownMenuSubContent className="w-40">
                <DropdownMenuItem onSelect={() => setColor(menu.table.id, null)}>
                  <span aria-hidden className="size-2.5 rounded-full border" />
                  None
                </DropdownMenuItem>
                {DIAGRAM_COLORS.map((c) => (
                  <DropdownMenuItem key={c} onSelect={() => setColor(menu.table.id, c)}>
                    <span
                      aria-hidden
                      className="size-2.5 rounded-full"
                      style={{ background: `var(--tag-${c})` }}
                    />
                    <span className="capitalize">{c}</span>
                    {doc.colors[menu.table.id] === c && <Check className="ml-auto size-3.5" />}
                  </DropdownMenuItem>
                ))}
              </DropdownMenuSubContent>
            </DropdownMenuSub>
            <DropdownMenuItem
              onSelect={() =>
                void copyText(
                  menu.table.schema ? `${menu.table.schema}.${menu.table.name}` : menu.table.name,
                  "Name copied",
                )
              }
            >
              <Copy />
              Copy name
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem onSelect={() => hide([menu.table.id])}>
              <EyeOff />
              Hide from the diagram
            </DropdownMenuItem>
          </DropdownMenuContent>
        )}
      </DropdownMenu>

      {noteFor && (
        <NoteDialog
          name={nameOf(noteFor)}
          returnTo={() => menuButton(noteFor.id)}
          initial={doc.notes[noteFor.id] ?? ""}
          onClose={() => setNoteFor(null)}
          onSave={(text) => {
            setNote(noteFor.id, text)
            setNoteFor(null)
          }}
        />
      )}
      {dialog}
    </div>
  )
}
