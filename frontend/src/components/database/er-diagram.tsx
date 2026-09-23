"use client"

import { useCallback, useEffect, useId, useMemo, useRef, useState } from "react"
import {
  Background,
  BackgroundVariant,
  MarkerType,
  MiniMap,
  ReactFlow,
  ReactFlowProvider,
  useEdgesState,
  useNodesState,
  useOnSelectionChange,
  useReactFlow,
  type Edge,
  type Node,
  type Viewport,
} from "@xyflow/react"
import "@xyflow/react/dist/style.css"
import {
  ArrowLeftRight,
  ArrowRight,
  ArrowUpDown,
  Check,
  Code,
  Crosshair,
  Cross,
  Download,
  Eye,
  EyeOff,
  Fingerprint,
  Fullscreen,
  FullscreenClose,
  Image as ImageIcon,
  Inspect,
  Key,
  Layout,
  Linked,
  LockClosed,
  Minus,
  MoreHorizontal,
  NetworkDevice,
  Notes,
  Plus,
  RotateCounterClockwise,
  SidebarRightClose,
  SidebarRightOpen,
  Table as TableIcon,
  Trash,
} from "@/components/icons"
import { get } from "@/lib/api"
import { notify } from "@/lib/toast"
import { copyText } from "@/lib/clipboard"
import { plural, relativeTime } from "@/lib/format"
import { cn, ringSafeScroll } from "@/lib/utils"
import { useViewState } from "@/lib/view-state"
import type { DbConnection, DbGraphEdge, DbGraphTable, DbSchemaGraph } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Pane, PaneHeader } from "@/components/panel"
import { SearchInput } from "@/components/page"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Textarea } from "@/components/ui/textarea"
import { Toggle } from "@/components/ui/toggle"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
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
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { Modal } from "@/components/modal"
import { Field } from "@/components/form"
import { Tag } from "@/components/tag"
import { TableNode, type TableNodeData } from "@/components/database/diagram/table-node"
import { RelationEdge, type RelationEdgeData } from "@/components/database/diagram/relation-edge"
import { applyPositions, buildEdges, layoutGraph } from "@/components/database/diagram/layout"
import {
  DIAGRAM_COLORS,
  isArranged,
  useDiagramMemory,
  type DiagramColor,
  type DiagramDetail,
  type DiagramDocument,
  type MemoryStatus,
} from "@/components/database/diagram/memory"
import {
  downloadPng,
  downloadText,
  renderSvg,
  toDbml,
  toJson,
  toMermaid,
} from "@/components/database/diagram/export"

const nodeTypes = { table: TableNode }
const edgeTypes = { relation: RelationEdge }
const FIT = { padding: 0.15, duration: 300 }

/**
 * The schema, as a diagram you can work in and come back to.
 *
 * The tables are their columns, the edges land on the rows they relate, and
 * the canvas pans, zooms and drags — that much any schema tool does. What this
 * one adds is memory and reach. Every arrangement the operator makes — a
 * table dragged into place, a lookup table hidden, a note, a colour, the
 * level of detail — is saved against the connection and comes back on the
 * next visit (`diagram/memory.ts`). And a table on the canvas is a place to
 * go from: its menu opens it in Browse, Structure or Query, focuses its
 * neighbourhood, hides it, colours it; the inspector beside the canvas reads
 * its relations both ways; the whole picture exports as SVG, PNG, Mermaid,
 * DBML or JSON; and the workbench goes full-screen for a schema that needs
 * the whole monitor.
 *
 * Focus is the feature that makes a large schema readable: clicking a table
 * leaves it, what it references and what references it at full strength and
 * drops everything else back, so a forty-table schema can be read one
 * neighbourhood at a time.
 */
export function ErDiagram({
  conn,
  schema,
  canSave,
  onOpenTable,
  onOpenStructure,
  onQuery,
}: {
  conn: DbConnection
  schema: string
  /** Whether this role may save dashboard state; otherwise the browser keeps the layout. */
  canSave: boolean
  onOpenTable: (schema: string, table: string) => void
  onOpenStructure: (schema: string, table: string) => void
  onQuery: (sql: string) => void
}) {
  const graph = usePoll(
    (signal) => get<DbSchemaGraph>(`/databases/${conn.id}/graph`, { schema }, signal),
    0,
    [conn.id, schema],
  )
  const memory = useDiagramMemory({ connId: conn.id, schema, canSave })

  if (graph.loading || memory.loading) return <LoadingPanel />
  if (graph.error) return <ErrorState error={graph.error} onRetry={graph.refresh} />
  if (!graph.data || !memory.document) return null
  if (graph.data.tables.length === 0) {
    return (
      <EmptyState
        icon={NetworkDevice}
        title="No tables in this schema"
        description="Create one from Browse or Structure and it appears here."
      />
    )
  }

  return (
    <ReactFlowProvider>
      <Canvas
        conn={conn}
        schema={schema}
        graph={graph.data}
        doc={memory.document}
        status={memory.status}
        updatedAt={memory.updatedAt}
        update={memory.update}
        reset={memory.reset}
        onOpenTable={onOpenTable}
        onOpenStructure={onOpenStructure}
        onQuery={onQuery}
      />
    </ReactFlowProvider>
  )
}

type Menu = { table: DbGraphTable; x: number; y: number }

function Canvas({
  conn,
  schema,
  graph,
  doc,
  status,
  updatedAt,
  update,
  reset,
  onOpenTable,
  onOpenStructure,
  onQuery,
}: {
  conn: DbConnection
  schema: string
  graph: DbSchemaGraph
  doc: DiagramDocument
  status: MemoryStatus
  updatedAt?: string
  update: (next: Partial<DiagramDocument> | ((doc: DiagramDocument) => DiagramDocument)) => void
  reset: () => Promise<void>
  onOpenTable: (schema: string, table: string) => void
  onOpenStructure: (schema: string, table: string) => void
  onQuery: (sql: string) => void
}) {
  const flow = useReactFlow()
  const root = useRef<HTMLDivElement>(null)
  const searchRef = useRef<HTMLInputElement>(null)
  const [focus, setFocus] = useState<string | null>(null)
  const [pickedEdge, setPickedEdge] = useState<string | null>(null)
  const [hoverEdge, setHoverEdge] = useState<string | null>(null)
  const [query, setQuery] = useState("")
  const [menu, setMenu] = useState<Menu | null>(null)
  const [noteFor, setNoteFor] = useState<DbGraphTable | null>(null)
  const [inspector, setInspector] = useViewState("db.diagram.inspector", true)
  const [fullscreen, setFullscreen] = useState(false)
  const [immersive, setImmersive] = useState(false)
  const [selectedCount, setSelectedCount] = useState(0)
  // Bumped by Tidy and Reset, which change nothing the layout is keyed on but
  // still mean "lay it out again".
  const [epoch, setEpoch] = useState(0)

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
  const [edges, setEdges] = useEdgesState<Edge>([])

  // The saved positions are read through a ref: a drag writes them, and a
  // drag must not be the thing that lays the diagram out again.
  const positions = useRef(doc.positions)
  useEffect(() => {
    positions.current = doc.positions
  }, [doc.positions])
  const restored = useRef(false)

  // Re-laying out is a deliberate act — changing the direction or the detail,
  // hiding a table, pressing Tidy — not something that happens under a drag.
  useEffect(() => {
    setNodes(applyPositions(graph, laid, positions.current, layoutOpts))
    if (restored.current) {
      window.setTimeout(() => flow.fitView(FIT), 0)
      return
    }
    restored.current = true
    // The first paint: where the operator left the viewport, or fitted.
    window.setTimeout(() => {
      if (doc.viewport) void flow.setViewport(doc.viewport)
      else void flow.fitView({ padding: 0.15 })
    }, 0)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [laid, epoch, setNodes, flow])

  // Edges follow the tables: the side an edge leaves by is decided by where
  // the two tables are now, so dragging one across the diagram re-routes it.
  useEffect(() => {
    setEdges(buildEdges(graph, nodes, doc.detail))
  }, [graph, nodes, doc.detail, setEdges])

  useOnSelectionChange({
    onChange: useCallback(
      ({ nodes: picked }: { nodes: Node[] }) => setSelectedCount(picked.length),
      [],
    ),
  })

  /** Everything one table touches, in both directions. */
  const neighbourhood = useMemo(() => {
    if (!focus) return null
    const near = new Set<string>([focus])
    for (const e of graph.edges) {
      if (e.fromTable === focus) near.add(e.toTable)
      if (e.toTable === focus) near.add(e.fromTable)
    }
    return near
  }, [focus, graph.edges])

  /** Tables the search names, and which of their columns answered. */
  const matches = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return null
    const out = new Map<string, string[]>()
    for (const t of graph.tables) {
      if (hidden.has(t.name)) continue
      const cols = t.columns.filter((c) => c.name.toLowerCase().includes(q)).map((c) => c.name)
      if (t.name.toLowerCase().includes(q) || cols.length > 0) out.set(t.name, cols)
    }
    return out
  }, [query, graph.tables, hidden])

  const tableOf = useCallback(
    (name: string) => graph.tables.find((t) => t.name === name),
    [graph.tables],
  )

  const panTo = useCallback(
    (name: string) => {
      void flow.fitView({ nodes: [{ id: name }], padding: 0.6, duration: 300, maxZoom: 1.2 })
    },
    [flow],
  )

  const focusTable = useCallback((name: string | null) => {
    setFocus(name)
    setPickedEdge(null)
  }, [])

  const openMenu = useCallback((table: DbGraphTable, at: { x: number; y: number }) => {
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
    (names: string[]) => {
      if (names.length === 0) return
      update((d) => ({ ...d, hidden: [...new Set([...d.hidden, ...names])] }))
      setFocus((f) => (f && names.includes(f) ? null : f))
      notify.success(
        names.length === 1 ? `Hidden ${names[0]}` : `Hidden ${plural(names.length, "table")}`,
        {
          description: "Restore it from the hidden list in the toolbar.",
        },
      )
    },
    [update],
  )
  const show = (names: string[]) =>
    update((d) => ({ ...d, hidden: d.hidden.filter((h) => !names.includes(h)) }))

  const isolate = (name: string) => {
    const near = new Set<string>([name])
    for (const e of graph.edges) {
      if (e.fromTable === name) near.add(e.toTable)
      if (e.toTable === name) near.add(e.fromTable)
    }
    hide(graph.tables.map((t) => t.name).filter((t) => !near.has(t)))
    setFocus(name)
  }

  const isolated = useMemo(() => {
    const connected = new Set<string>()
    for (const e of graph.edges) {
      if (e.fromTable !== e.toTable) {
        connected.add(e.fromTable)
        connected.add(e.toTable)
      }
    }
    return graph.tables.map((t) => t.name).filter((n) => !connected.has(n) && !hidden.has(n))
  }, [graph, hidden])

  const setColor = (name: string, color: DiagramColor | null) =>
    update((d) => {
      const colors = { ...d.colors }
      if (color) colors[name] = color
      else delete colors[name]
      return { ...d, colors }
    })

  const setNote = (name: string, note: string) =>
    update((d) => {
      const notes = { ...d.notes }
      if (note.trim()) notes[name] = note.trim()
      else delete notes[name]
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
      const on = document.fullscreenElement === root.current && root.current !== null
      setFullscreen(on)
      if (!document.fullscreenElement) setImmersive(false)
      window.setTimeout(() => flow.fitView(FIT), 50)
    }
    document.addEventListener("fullscreenchange", onChange)
    return () => document.removeEventListener("fullscreenchange", onChange)
  }, [flow])

  // The handful of keys a diagram wants: fit, find, focus the inspector, hide
  // what is focused, and Escape for everything transient.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement | null
      const typing =
        target &&
        (target.tagName === "INPUT" ||
          target.tagName === "TEXTAREA" ||
          target.isContentEditable ||
          target.closest("[role=dialog],[role=menu]"))
      if (e.key === "Escape") {
        if (typing) return
        if (menu) setMenu(null)
        else if (focus || pickedEdge) focusTable(null)
        else if (immersive) setImmersive(false)
        return
      }
      if (typing || e.metaKey || e.ctrlKey || e.altKey) return
      if (e.key === "f") flow.fitView(FIT)
      else if (e.key === "/") {
        e.preventDefault()
        searchRef.current?.focus()
      } else if (e.key === "i") setInspector((v) => !v)
      else if (e.key === "h" && focus) hide([focus])
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [menu, focus, pickedEdge, immersive, flow, setInspector, hide, focusTable])

  const active = useMemo(
    () => (matches ? new Set(matches.keys()) : neighbourhood),
    [matches, neighbourhood],
  )
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
          onOpen: (t: DbGraphTable) => focusTable(focus === t.name ? null : t.name),
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
      focusTable,
      openMenu,
    ],
  )

  const shownEdges = useMemo(
    () =>
      edges.map((e) => {
        const rel = (e.data as { relation: DbGraphEdge }).relation
        const involved = active ? active.has(rel.fromTable) && active.has(rel.toTable) : false
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
    const base = `${conn.name}${schema ? `-${schema}` : ""}`.replace(/[^A-Za-z0-9_.-]+/g, "-")
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
          title: `${conn.name}${schema ? ` · ${schema}` : ""}`,
        })
        if (kind === "svg") downloadText(`${base}.svg`, svg, "image/svg+xml")
        else await downloadPng(`${base}.png`, svg)
      }
    } catch (err) {
      notify.error("Could not export the diagram", err)
    }
  }

  const focused = focus ? tableOf(focus) : undefined
  const picked = pickedEdge ? edges.find((e) => e.id === pickedEdge) : undefined
  const pickedRelation = picked ? (picked.data as { relation: DbGraphEdge }).relation : undefined
  const shownCount = graph.tables.length - hidden.size
  const hits = matches ? matches.size : null

  return (
    <div
      ref={root}
      className={cn(
        "flex min-h-0 min-w-0 flex-1 flex-col bg-background [&:fullscreen]:p-3",
        immersive && "fixed inset-0 z-50 p-3",
      )}
    >
      <Pane className="min-h-0 flex-1">
        <PaneHeader className="flex-wrap gap-x-2 gap-y-1.5">
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
            placeholder="Find a table or column…  /"
            aria-label="Find a table or column"
            containerClassName="w-56 sm:w-56"
            trailing={
              query ? (
                <Button
                  size="icon-xs"
                  variant="ghost"
                  aria-label="Clear search"
                  onClick={() => setQuery("")}
                >
                  <Cross />
                </Button>
              ) : undefined
            }
          />
          {hits !== null && (
            <span className="numeric text-hint text-muted-foreground">
              {hits === 0 ? "no matches" : plural(hits, "match", "matches")}
            </span>
          )}

          <span className="mx-1 hidden h-4 w-px bg-hairline sm:block" />

          <ToggleGroup
            type="single"
            size="sm"
            variant="outline"
            value={doc.detail}
            onValueChange={(v) => v && update({ detail: v as DiagramDetail })}
            aria-label="Level of detail"
          >
            <ToggleGroupItem value="all" className="h-7 text-hint">
              All columns
            </ToggleGroupItem>
            <ToggleGroupItem value="keys" className="h-7 text-hint">
              Keys
            </ToggleGroupItem>
            <ToggleGroupItem value="names" className="h-7 text-hint">
              Names
            </ToggleGroupItem>
          </ToggleGroup>
          <Button
            size="icon-sm"
            variant="ghost"
            className="size-7"
            onClick={() => update({ direction: doc.direction === "LR" ? "TB" : "LR" })}
            aria-label={doc.direction === "LR" ? "Lay out top to bottom" : "Lay out left to right"}
            title={doc.direction === "LR" ? "Lay out top to bottom" : "Lay out left to right"}
          >
            {doc.direction === "LR" ? (
              <ArrowLeftRight className="size-3.5" />
            ) : (
              <ArrowUpDown className="size-3.5" />
            )}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            className="h-7 px-2"
            onClick={tidy}
            title="Lay every table out again"
          >
            <RotateCounterClockwise className="size-3.5" />
            Tidy
          </Button>

          <span className="flex-1" />

          <Saved status={status} updatedAt={updatedAt} arranged={isArranged(doc)} />

          {hidden.size > 0 && (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button size="sm" variant="ghost" className="h-7 px-2 text-muted-foreground">
                  <EyeOff className="size-3.5" />
                  {plural(hidden.size, "table")} hidden
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="max-h-80 w-64 overflow-y-auto">
                <DropdownMenuItem onClick={() => show([...hidden])}>
                  <Eye />
                  Show all
                </DropdownMenuItem>
                <DropdownMenuSeparator />
                {[...hidden].sort().map((name) => (
                  <DropdownMenuItem
                    key={name}
                    onClick={() => show([name])}
                    className="font-mono text-xs"
                  >
                    {name}
                  </DropdownMenuItem>
                ))}
              </DropdownMenuContent>
            </DropdownMenu>
          )}

          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button size="sm" variant="ghost" className="h-7 px-2" aria-label="Diagram options">
                <MoreHorizontal className="size-3.5" />
                Options
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-64">
              <DropdownMenuLabel className="text-hint font-medium text-muted-foreground">
                Canvas
              </DropdownMenuLabel>
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
                <span className="flex flex-col">
                  <span>Lock layout</span>
                  <span className="text-hint text-muted-foreground">
                    Tables stop being draggable.
                  </span>
                </span>
              </DropdownMenuCheckboxItem>
              <DropdownMenuSeparator />
              <DropdownMenuLabel className="text-hint font-medium text-muted-foreground">
                Spacing
              </DropdownMenuLabel>
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
                  <DropdownMenuItem onClick={() => alignSelected("row")}>
                    <ArrowLeftRight />
                    Arrange {selectedCount} selected in a row
                  </DropdownMenuItem>
                  <DropdownMenuItem onClick={() => alignSelected("column")}>
                    <ArrowUpDown />
                    Arrange {selectedCount} selected in a column
                  </DropdownMenuItem>
                  <DropdownMenuSeparator />
                </>
              )}
              {isolated.length > 0 && (
                <DropdownMenuItem onClick={() => hide(isolated)}>
                  <EyeOff />
                  Hide {plural(isolated.length, "table")} with no relations
                </DropdownMenuItem>
              )}
              <DropdownMenuItem
                variant="destructive"
                onClick={() => {
                  void reset().then(() => setEpoch((e) => e + 1))
                  setFocus(null)
                }}
              >
                <Trash />
                <span className="flex flex-col">
                  <span>Forget this layout</span>
                  <span className="text-hint">Positions, hidden tables, notes and colours.</span>
                </span>
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>

          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                size="sm"
                variant="ghost"
                className="h-7 px-2"
                aria-label="Export the diagram"
              >
                <Download className="size-3.5" />
                Export
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-60">
              <DropdownMenuItem onClick={() => void exportAs("png")}>
                <ImageIcon />
                <Words title="PNG image" hint="For a document or a chat." />
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => void exportAs("svg")}>
                <ImageIcon />
                <Words title="SVG image" hint="Scales, and opens in a vector editor." />
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem onClick={() => void exportAs("mermaid")}>
                <Code />
                <Words title="Mermaid" hint="An erDiagram GitHub renders in a README." />
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => void exportAs("dbml")}>
                <Code />
                <Words title="DBML" hint="For dbdiagram.io and the tools that read it." />
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => void exportAs("json")}>
                <Code />
                <Words title="JSON" hint="Tables, relations and this layout." />
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>

          <Toggle
            size="sm"
            pressed={inspector}
            onPressedChange={setInspector}
            aria-label="Inspector"
            title="Inspector (i)"
            className="size-7 min-w-0 px-0"
          >
            {inspector ? (
              <SidebarRightClose className="size-3.5" />
            ) : (
              <SidebarRightOpen className="size-3.5" />
            )}
          </Toggle>
          <Button
            size="icon-sm"
            variant="ghost"
            className="size-7"
            onClick={toggleFullscreen}
            aria-label={fullscreen || immersive ? "Exit full screen" : "Full screen"}
            title={fullscreen || immersive ? "Exit full screen (Esc)" : "Full screen"}
          >
            {fullscreen || immersive ? (
              <FullscreenClose className="size-3.5" />
            ) : (
              <Fullscreen className="size-3.5" />
            )}
          </Button>
        </PaneHeader>

        {graph.truncated && (
          <div className="shrink-0 border-b border-hairline px-3 py-1.5 text-hint text-muted-foreground">
            Showing the first {graph.tables.length} tables — this schema has more than a diagram can
            usefully show.
          </div>
        )}

        <div
          className={cn(
            "grid min-h-0 flex-1",
            inspector ? "grid-cols-1 lg:grid-cols-[minmax(0,1fr)_18rem]" : "grid-cols-1",
          )}
        >
          <div className="relative min-h-0 min-w-0">
            <ReactFlow
              nodes={shown}
              edges={shownEdges}
              onNodesChange={onNodesChange}
              nodeTypes={nodeTypes}
              edgeTypes={edgeTypes}
              onNodeClick={(_, n) => focusTable(focus === n.id ? null : n.id)}
              onNodeDoubleClick={(_, n) => {
                const t = tableOf(n.id)
                if (t) onOpenTable(t.schema, t.name)
              }}
              onNodeContextMenu={(e, n) => {
                e.preventDefault()
                const t = tableOf(n.id)
                if (t) openMenu(t, { x: e.clientX, y: e.clientY })
              }}
              onNodeDragStop={() => update((d) => ({ ...d, positions: snapshotPositions() }))}
              onEdgeClick={(_, e) => {
                setPickedEdge((p) => (p === e.id ? null : e.id))
                setFocus(null)
              }}
              onEdgeMouseEnter={(_, e) => setHoverEdge(e.id)}
              onEdgeMouseLeave={() => setHoverEdge(null)}
              onPaneClick={() => {
                focusTable(null)
                setMenu(null)
              }}
              // A null event is a programmatic move — the first fit, a pan to a
              // search hit — and not something the operator asked to keep.
              onMoveEnd={(event, viewport: Viewport) => {
                if (event && restored.current) update({ viewport })
              }}
              minZoom={0.08}
              maxZoom={2.5}
              proOptions={{ hideAttribution: true }}
              nodesConnectable={false}
              nodesDraggable={!doc.locked}
              elementsSelectable
              snapToGrid={doc.snap}
              snapGrid={[16, 16]}
              deleteKeyCode={null}
              className="[&_.react-flow\_\_handle]:!border-0"
            >
              {doc.grid && (
                <Background
                  variant={BackgroundVariant.Dots}
                  gap={20}
                  size={1}
                  className="[&_circle]:fill-border"
                />
              )}
              {doc.minimap && (
                <MiniMap
                  pannable
                  zoomable
                  className="!right-3 !bottom-3 !h-24 !w-40 overflow-hidden !rounded-md !border !bg-card"
                  maskColor="color-mix(in oklab, var(--color-background) 70%, transparent)"
                  nodeColor="color-mix(in oklab, var(--color-chart-1) 55%, var(--color-muted))"
                  nodeStrokeWidth={0}
                />
              )}
            </ReactFlow>

            <ZoomControls
              focus={focus}
              locked={doc.locked}
              onClear={() => focusTable(null)}
              onUnlock={() => update({ locked: false })}
            />
            <Legend />
          </div>

          {inspector && (
            <Inspector
              graph={graph}
              doc={doc}
              hidden={hidden}
              shownCount={shownCount}
              focused={focused}
              relation={pickedRelation}
              onFocus={(name) => {
                focusTable(name)
                panTo(name)
              }}
              onToggleHidden={(name, visible) => (visible ? show([name]) : hide([name]))}
              onShowAll={() => show([...hidden])}
              onColor={setColor}
              onNote={(t) => setNoteFor(t)}
              onOpenTable={onOpenTable}
              onOpenStructure={onOpenStructure}
              onQuery={(t) => onQuery(selectSql(conn, t))}
              onClose={() => setInspector(false)}
            />
          )}
        </div>
      </Pane>

      {/* One menu for every table, anchored to the point it was asked for
          from — a header button or a right-click — rather than a menu per node. */}
      <DropdownMenu open={menu !== null} onOpenChange={(o) => !o && setMenu(null)}>
        <DropdownMenuTrigger asChild>
          <span
            aria-hidden
            className="pointer-events-none fixed size-0"
            style={{ left: menu?.x ?? 0, top: menu?.y ?? 0 }}
          />
        </DropdownMenuTrigger>
        {menu && (
          <DropdownMenuContent align="start" className="w-64">
            <DropdownMenuLabel className="truncate font-mono text-xs">
              {menu.table.name}
            </DropdownMenuLabel>
            <DropdownMenuItem onClick={() => onOpenTable(menu.table.schema, menu.table.name)}>
              <TableIcon />
              <Words title="Browse rows" hint="Double-click does the same." />
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => onOpenStructure(menu.table.schema, menu.table.name)}>
              <Layout />
              Structure
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => onQuery(selectSql(conn, menu.table))}>
              <Code />
              <Words title="Query" hint="Opens the editor on a SELECT of this table." />
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem
              onClick={() => {
                focusTable(focus === menu.table.name ? null : menu.table.name)
              }}
            >
              <Crosshair />
              {focus === menu.table.name ? "Clear focus" : "Focus its neighbourhood"}
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => isolate(menu.table.name)}>
              <Inspect />
              <Words title="Show only this and its relations" hint="Hides every other table." />
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => setNoteFor(menu.table)}>
              <Notes />
              {doc.notes[menu.table.name] ? "Edit note…" : "Add a note…"}
            </DropdownMenuItem>
            <DropdownMenuSub>
              <DropdownMenuSubTrigger>
                <span
                  className="size-2.5 rounded-full border"
                  style={{
                    background: doc.colors[menu.table.name]
                      ? `var(--tag-${doc.colors[menu.table.name]})`
                      : undefined,
                  }}
                />
                Colour
              </DropdownMenuSubTrigger>
              <DropdownMenuSubContent className="w-40">
                <DropdownMenuItem onClick={() => setColor(menu.table.name, null)}>
                  <span className="size-2.5 rounded-full border" />
                  None
                </DropdownMenuItem>
                {DIAGRAM_COLORS.map((c) => (
                  <DropdownMenuItem key={c} onClick={() => setColor(menu.table.name, c)}>
                    <span
                      className="size-2.5 rounded-full"
                      style={{ background: `var(--tag-${c})` }}
                    />
                    <span className="capitalize">{c}</span>
                    {doc.colors[menu.table.name] === c && <Check className="ml-auto size-3.5" />}
                  </DropdownMenuItem>
                ))}
              </DropdownMenuSubContent>
            </DropdownMenuSub>
            <DropdownMenuItem
              onClick={() =>
                void copyText(
                  menu.table.schema ? `${menu.table.schema}.${menu.table.name}` : menu.table.name,
                  "Copied",
                )
              }
            >
              <Linked />
              Copy name
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => hide([menu.table.name])}>
              <EyeOff />
              <Words title="Hide from the diagram" hint="The table itself is untouched. (h)" />
            </DropdownMenuItem>
          </DropdownMenuContent>
        )}
      </DropdownMenu>

      {noteFor && (
        <NoteDialog
          table={noteFor}
          initial={doc.notes[noteFor.name] ?? ""}
          onClose={() => setNoteFor(null)}
          onSave={(text) => {
            setNote(noteFor.name, text)
            setNoteFor(null)
          }}
        />
      )}
    </div>
  )
}

/** A SELECT of the table, in the engine's own way of saying "the first hundred". */
function selectSql(conn: DbConnection, t: DbGraphTable) {
  const name = t.schema ? `${t.schema}.${t.name}` : t.name
  if (conn.driver === "sqlserver") return `SELECT TOP 100 * FROM ${name};`
  if (conn.driver === "oracle") return `SELECT * FROM ${name} FETCH FIRST 100 ROWS ONLY;`
  return `SELECT * FROM ${name} LIMIT 100;`
}

function Words({ title, hint }: { title: string; hint: string }) {
  return (
    <span className="flex min-w-0 flex-col">
      <span>{title}</span>
      <span className="text-hint text-muted-foreground">{hint}</span>
    </span>
  )
}

/** Whether the arrangement is kept, in a word the operator can trust. */
function Saved({
  status,
  updatedAt,
  arranged,
}: {
  status: MemoryStatus
  updatedAt?: string
  arranged: boolean
}) {
  let text: string | null = null
  let tone = "text-muted-foreground"
  if (status === "saving") text = "Saving…"
  else if (status === "unsaved") text = "Unsaved"
  else if (status === "failed") {
    text = "Not saved"
    tone = "text-destructive"
  } else if (status === "local") text = arranged ? "Kept in this browser" : null
  else if (status === "saved") text = updatedAt ? `Saved ${relativeTime(updatedAt)}` : null
  if (!text) return null
  return (
    <span className={cn("hidden text-hint sm:inline", tone)} aria-live="polite">
      {text}
    </span>
  )
}

/**
 * Zoom controls of our own rather than React Flow's, which ship their own
 * borders, shadows and icon set and look like a different product dropped onto
 * the page.
 */
function ZoomControls({
  focus,
  locked,
  onClear,
  onUnlock,
}: {
  focus: string | null
  locked: boolean
  onClear: () => void
  onUnlock: () => void
}) {
  const flow = useReactFlow()
  return (
    <div className="pointer-events-none absolute top-3 left-3 flex flex-wrap items-center gap-2">
      <div className="pointer-events-auto flex items-center gap-0.5 rounded-md border bg-card p-0.5">
        <Button
          size="icon"
          variant="ghost"
          className="size-7"
          onClick={() => flow.zoomIn({ duration: 150 })}
          aria-label="Zoom in"
        >
          <Plus className="size-3.5" />
        </Button>
        <Button
          size="icon"
          variant="ghost"
          className="size-7"
          onClick={() => flow.zoomOut({ duration: 150 })}
          aria-label="Zoom out"
        >
          <Minus className="size-3.5" />
        </Button>
        <Button
          size="icon"
          variant="ghost"
          className="size-7"
          onClick={() => flow.fitView(FIT)}
          aria-label="Fit to view"
          title="Fit to view (f)"
        >
          <Crosshair className="size-3.5" />
        </Button>
      </div>
      {focus && (
        <Button
          size="xs"
          variant="outline"
          className="pointer-events-auto"
          onClick={onClear}
          title="Show the whole schema again (Esc)"
        >
          <Crosshair className="size-3" />
          {focus}
          <span className="text-muted-foreground">· clear</span>
        </Button>
      )}
      {locked && (
        <Button
          size="xs"
          variant="outline"
          className="pointer-events-auto text-muted-foreground"
          onClick={onUnlock}
          title="The layout is locked — tables cannot be dragged. Press to unlock."
        >
          <LockClosed className="size-3" />
          Locked
        </Button>
      )}
    </div>
  )
}

function Legend() {
  return (
    <div className="pointer-events-none absolute bottom-3 left-3 flex items-center gap-3 rounded-md border bg-card/90 px-2.5 py-1.5 text-micro text-muted-foreground backdrop-blur">
      <span className="flex items-center gap-1">
        <Key className="size-3 text-chart-2" /> primary key
      </span>
      <span className="flex items-center gap-1">
        <Linked className="size-3 text-chart-1" /> foreign key
      </span>
      <span className="flex items-center gap-1">
        <Fingerprint className="size-3 text-muted-foreground/60" /> unique
      </span>
    </div>
  )
}

/**
 * The column beside the canvas: what the schema amounts to and every table in
 * it while nothing is focused; the focused table's relations in both
 * directions, its columns and its annotations once something is.
 */
function Inspector({
  graph,
  doc,
  hidden,
  shownCount,
  focused,
  relation,
  onFocus,
  onToggleHidden,
  onShowAll,
  onColor,
  onNote,
  onOpenTable,
  onOpenStructure,
  onQuery,
  onClose,
}: {
  graph: DbSchemaGraph
  doc: DiagramDocument
  hidden: Set<string>
  shownCount: number
  focused?: DbGraphTable
  relation?: DbGraphEdge
  onFocus: (name: string) => void
  onToggleHidden: (name: string, visible: boolean) => void
  onShowAll: () => void
  onColor: (name: string, color: DiagramColor | null) => void
  onNote: (table: DbGraphTable) => void
  onOpenTable: (schema: string, table: string) => void
  onOpenStructure: (schema: string, table: string) => void
  onQuery: (table: DbGraphTable) => void
  onClose: () => void
}) {
  const [filter, setFilter] = useState("")
  const columns = graph.tables.reduce((n, t) => n + t.columns.length, 0)
  const outgoing = focused ? graph.edges.filter((e) => e.fromTable === focused.name) : []
  const incoming = focused ? graph.edges.filter((e) => e.toTable === focused.name) : []
  const sorted = useMemo(() => {
    const q = filter.trim().toLowerCase()
    return [...graph.tables]
      .filter((t) => !q || t.name.toLowerCase().includes(q))
      .sort((a, b) => a.name.localeCompare(b.name))
  }, [graph.tables, filter])

  return (
    <aside className="flex min-h-0 min-w-0 flex-col border-t border-hairline lg:border-t-0 lg:border-l">
      <PaneHeader className="justify-between">
        <span className="min-w-0 truncate text-body font-medium">
          {focused ? (
            <span className="font-mono text-xs">{focused.name}</span>
          ) : relation ? (
            "Relation"
          ) : (
            "Schema"
          )}
        </span>
        <Button
          size="icon-xs"
          variant="ghost"
          aria-label="Close the inspector"
          onClick={onClose}
          className="text-muted-foreground"
        >
          <Cross />
        </Button>
      </PaneHeader>
      <div className={cn("min-h-0 flex-1 overflow-y-auto", ringSafeScroll)}>
        {focused ? (
          <div className="space-y-4 p-3">
            <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-hint">
              {focused.schema && (
                <>
                  <dt className="text-muted-foreground">Schema</dt>
                  <dd className="truncate font-mono">{focused.schema}</dd>
                </>
              )}
              <dt className="text-muted-foreground">Rows</dt>
              <dd className="numeric">{focused.rows > 0 ? focused.rows.toLocaleString() : "—"}</dd>
              <dt className="text-muted-foreground">Columns</dt>
              <dd className="numeric">{focused.columns.length}</dd>
              {focused.type && focused.type.toLowerCase() !== "table" && (
                <>
                  <dt className="text-muted-foreground">Kind</dt>
                  <dd>{focused.type}</dd>
                </>
              )}
            </dl>

            <div className="flex flex-wrap gap-1.5">
              <Button
                size="xs"
                variant="outline"
                onClick={() => onOpenTable(focused.schema, focused.name)}
              >
                <TableIcon />
                Browse
              </Button>
              <Button
                size="xs"
                variant="outline"
                onClick={() => onOpenStructure(focused.schema, focused.name)}
              >
                <Layout />
                Structure
              </Button>
              <Button size="xs" variant="outline" onClick={() => onQuery(focused)}>
                <Code />
                Query
              </Button>
            </div>

            <div className="space-y-1.5">
              <div className="flex items-center justify-between">
                <p className="eyebrow">Note</p>
                <Button size="xs" variant="ghost" onClick={() => onNote(focused)}>
                  <Notes />
                  {doc.notes[focused.name] ? "Edit" : "Add"}
                </Button>
              </div>
              {doc.notes[focused.name] ? (
                <p className="text-hint leading-relaxed whitespace-pre-wrap">
                  {doc.notes[focused.name]}
                </p>
              ) : (
                <p className="text-hint text-muted-foreground">
                  What this table is for, kept with the diagram.
                </p>
              )}
            </div>

            <div className="space-y-1.5">
              <p className="eyebrow">Colour</p>
              <div className="flex flex-wrap gap-1.5">
                <button
                  type="button"
                  aria-label="No colour"
                  title="None"
                  onClick={() => onColor(focused.name, null)}
                  className={cn(
                    "size-5 rounded-full border focus-ring transition-colors",
                    !doc.colors[focused.name]
                      ? "border-foreground"
                      : "border-border hover:border-border-strong",
                  )}
                />
                {DIAGRAM_COLORS.map((c) => (
                  <button
                    key={c}
                    type="button"
                    aria-label={c}
                    title={c}
                    onClick={() => onColor(focused.name, c)}
                    className={cn(
                      "size-5 rounded-full border-2 focus-ring transition-colors",
                      doc.colors[focused.name] === c ? "border-foreground" : "border-transparent",
                    )}
                    style={{ background: `var(--tag-${c})` }}
                  />
                ))}
              </div>
            </div>

            <RelationList
              title="References"
              empty="Points at nothing."
              edges={outgoing}
              other={(e) => e.toTable}
              describe={(e) => `${e.fromColumn} → ${e.toTable}.${e.toColumn}`}
              onFocus={onFocus}
            />
            <RelationList
              title="Referenced by"
              empty="Nothing points here."
              edges={incoming}
              other={(e) => e.fromTable}
              describe={(e) => `${e.fromTable}.${e.fromColumn} → ${e.toColumn}`}
              onFocus={onFocus}
            />

            <div className="space-y-1.5">
              <p className="eyebrow">Columns</p>
              <ul className="divide-y divide-hairline">
                {focused.columns.map((c) => (
                  <li key={c.name} className="flex items-center gap-2 py-1 text-hint">
                    {c.primaryKey ? (
                      <Key className="size-3 shrink-0 text-chart-2" />
                    ) : c.foreignKey ? (
                      <Linked className="size-3 shrink-0 text-chart-1" />
                    ) : c.unique ? (
                      <Fingerprint className="size-3 shrink-0 text-muted-foreground/60" />
                    ) : (
                      <span className="size-3 shrink-0" />
                    )}
                    <span className="min-w-0 flex-1 truncate font-mono">{c.name}</span>
                    <span className="shrink-0 truncate font-mono text-micro text-muted-foreground">
                      {c.type.toLowerCase()}
                      {c.nullable ? "" : " · not null"}
                    </span>
                  </li>
                ))}
              </ul>
            </div>
          </div>
        ) : relation ? (
          <div className="space-y-4 p-3">
            <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-hint">
              <dt className="text-muted-foreground">Constraint</dt>
              <dd className="truncate font-mono">{relation.name || "—"}</dd>
              <dt className="text-muted-foreground">From</dt>
              <dd className="truncate font-mono">
                {relation.fromTable}.{relation.fromColumn}
              </dd>
              <dt className="text-muted-foreground">To</dt>
              <dd className="truncate font-mono">
                {relation.toTable}.{relation.toColumn}
              </dd>
              <dt className="text-muted-foreground">Cardinality</dt>
              <dd>{relation.cardinality === "one-to-one" ? "one to one" : "many to one"}</dd>
              <dt className="text-muted-foreground">On delete</dt>
              <dd className={cn(relation.onDelete === "CASCADE" && "text-destructive")}>
                {relation.onDelete?.toLowerCase() || "no action"}
              </dd>
            </dl>
            <div className="flex flex-wrap gap-1.5">
              <Button size="xs" variant="outline" onClick={() => onFocus(relation.fromTable)}>
                {relation.fromTable}
                <ArrowRight />
              </Button>
              <Button size="xs" variant="outline" onClick={() => onFocus(relation.toTable)}>
                {relation.toTable}
                <ArrowRight />
              </Button>
            </div>
          </div>
        ) : (
          <div className="space-y-4 p-3">
            <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-hint">
              <dt className="text-muted-foreground">Tables</dt>
              <dd className="numeric">
                {shownCount}
                {hidden.size > 0 && (
                  <span className="text-muted-foreground"> shown · {hidden.size} hidden</span>
                )}
              </dd>
              <dt className="text-muted-foreground">Relations</dt>
              <dd className="numeric">{graph.edges.length}</dd>
              <dt className="text-muted-foreground">Columns</dt>
              <dd className="numeric">{columns}</dd>
            </dl>
            <div className="space-y-1.5">
              <div className="flex items-center justify-between gap-2">
                <p className="eyebrow">Tables</p>
                {hidden.size > 0 && (
                  <Button size="xs" variant="ghost" onClick={onShowAll}>
                    <Eye />
                    Show all
                  </Button>
                )}
              </div>
              <SearchInput
                dense
                value={filter}
                onChange={(e) => setFilter(e.target.value)}
                placeholder="Filter…"
                aria-label="Filter the table list"
                containerClassName="w-full sm:w-full"
              />
              <ul className="divide-y divide-hairline">
                {sorted.map((t) => {
                  const visible = !hidden.has(t.name)
                  return (
                    <li key={t.name} className="flex items-center gap-2 py-1">
                      <Checkbox
                        checked={visible}
                        onCheckedChange={(v) => onToggleHidden(t.name, Boolean(v))}
                        aria-label={`Show ${t.name} on the diagram`}
                      />
                      <button
                        type="button"
                        onClick={() => visible && onFocus(t.name)}
                        disabled={!visible}
                        className={cn(
                          "min-w-0 flex-1 truncate rounded-sm text-left font-mono text-xs focus-ring-inset hover:underline",
                          !visible && "text-muted-foreground",
                        )}
                        title={visible ? `Focus ${t.name}` : `${t.name} is hidden`}
                      >
                        {t.name}
                      </button>
                      {doc.colors[t.name] && (
                        <span
                          className="size-2 shrink-0 rounded-full"
                          style={{ background: `var(--tag-${doc.colors[t.name]})` }}
                        />
                      )}
                      {t.rows > 0 && (
                        <span className="numeric shrink-0 text-micro text-muted-foreground">
                          {t.rows.toLocaleString()}
                        </span>
                      )}
                    </li>
                  )
                })}
              </ul>
            </div>
          </div>
        )}
      </div>
    </aside>
  )
}

function RelationList({
  title,
  empty,
  edges,
  other,
  describe,
  onFocus,
}: {
  title: string
  empty: string
  edges: DbGraphEdge[]
  other: (e: DbGraphEdge) => string
  describe: (e: DbGraphEdge) => string
  onFocus: (name: string) => void
}) {
  return (
    <div className="space-y-1.5">
      <p className="eyebrow">
        {title}
        {edges.length > 0 && <span className="numeric ml-1.5 normal-case">{edges.length}</span>}
      </p>
      {edges.length === 0 ? (
        <p className="text-hint text-muted-foreground">{empty}</p>
      ) : (
        <ul className="divide-y divide-hairline">
          {edges.map((e, i) => (
            <li key={`${e.name}-${i}`}>
              <button
                type="button"
                onClick={() => onFocus(other(e))}
                className="flex w-full items-center gap-2 rounded-md py-1 text-left text-hint focus-ring-inset transition-colors hover:bg-row-hover"
                title={`Focus ${other(e)}`}
              >
                <span className="min-w-0 flex-1 truncate font-mono">{describe(e)}</span>
                {e.cardinality === "one-to-one" && <Tag>1:1</Tag>}
                <ArrowRight className="size-3 shrink-0 text-muted-foreground" />
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

function NoteDialog({
  table,
  initial,
  onClose,
  onSave,
}: {
  table: DbGraphTable
  initial: string
  onClose: () => void
  onSave: (text: string) => void
}) {
  const id = useId()
  const [text, setText] = useState(initial)
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      size="sm"
      title={initial ? "Edit note" : "Add a note"}
      description={`A short note about ${table.name}, shown on the diagram and kept with it.`}
      footer={
        <>
          {initial && (
            <Button variant="ghost" className="mr-auto text-destructive" onClick={() => onSave("")}>
              <Trash />
              Remove
            </Button>
          )}
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={() => onSave(text)} disabled={text.trim() === initial.trim()}>
            Save
          </Button>
        </>
      }
    >
      <Field
        label="Note"
        htmlFor={`${id}-note`}
        hint="One line shows on the table; the whole note is in the inspector."
      >
        <Textarea
          id={`${id}-note`}
          value={text}
          onChange={(e) => setText(e.target.value)}
          className="min-h-24"
          placeholder="What this table is for, who writes to it, what to be careful of…"
          autoFocus
        />
      </Field>
    </Modal>
  )
}
