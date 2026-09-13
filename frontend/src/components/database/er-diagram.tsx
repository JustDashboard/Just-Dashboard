"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import {
  Background,
  BackgroundVariant,
  ReactFlow,
  ReactFlowProvider,
  MiniMap,
  MarkerType,
  useEdgesState,
  useNodesState,
  useReactFlow,
  type Edge,
  type Node,
} from "@xyflow/react"
import "@xyflow/react/dist/style.css"
import {
  ArrowLeftRight,
  ArrowUpDown,
  Crosshair,
  Fingerprint,
  Fullscreen,
  Key,
  Layout,
  Linked,
  Minus,
  NetworkDevice,
  Plus,
  RotateCounterClockwise,
} from "@/components/icons"
import { get } from "@/lib/api"
import type { DbConnection, DbGraphEdge, DbSchemaGraph } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Panel, PanelHeader } from "@/components/panel"
import { SearchInput } from "@/components/page"
import { Button } from "@/components/ui/button"
import { EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { TableNode, type TableNodeData } from "@/components/database/diagram/table-node"
import { RelationEdge } from "@/components/database/diagram/relation-edge"
import { layoutGraph } from "@/components/database/diagram/layout"

const nodeTypes = { table: TableNode }
const edgeTypes = { relation: RelationEdge }

/**
 * The schema, as a diagram you can actually work in.
 *
 * What this replaces drew a box per table with the name in it and a line
 * between boxes, which answers "are these two related" and nothing else. The
 * questions somebody opening a schema diagram actually has are which column is
 * the key, which column points where, and what shape the whole thing is — so
 * the tables are their columns, the edges land on the rows they relate, and the
 * canvas pans, zooms and drags.
 *
 * Layout is dagre and therefore deterministic: the same schema is the same
 * picture every time it is opened, which is what makes one worth learning.
 * Dragging a table moves it for the session; Reset lays it out again.
 *
 * Focus is the feature that makes a large schema readable, and it is why the
 * node and edge components take `dimmed`: clicking a table leaves it, what it
 * references and what references it at full strength and drops everything else
 * back, so a forty-table schema can be read one neighbourhood at a time.
 */
export function ErDiagram({
  conn,
  schema,
  onOpenTable,
}: {
  conn: DbConnection
  schema: string
  onOpenTable?: (schema: string, table: string) => void
}) {
  const graph = usePoll(
    (signal) => get<DbSchemaGraph>(`/databases/${conn.id}/graph`, { schema }, signal),
    0,
    [conn.id, schema],
  )

  if (graph.loading) return <LoadingPanel />
  if (graph.error) return <ErrorState error={graph.error} />
  if (!graph.data) return null
  if (graph.data.tables.length === 0) {
    return (
      <Panel className="min-h-0 flex-1">
        <PanelHeader title="Schema" />
        <EmptyState icon={NetworkDevice} title="No tables in this schema" />
      </Panel>
    )
  }

  return (
    <ReactFlowProvider>
      <Canvas graph={graph.data} onOpenTable={onOpenTable} />
    </ReactFlowProvider>
  )
}

function Canvas({
  graph,
  onOpenTable,
}: {
  graph: DbSchemaGraph
  onOpenTable?: (schema: string, table: string) => void
}) {
  const flow = useReactFlow()
  const [keysOnly, setKeysOnly] = useState(false)
  const [direction, setDirection] = useState<"LR" | "TB">("LR")
  const [focus, setFocus] = useState<string | null>(null)
  const [query, setQuery] = useState("")

  const laid = useMemo(
    () => layoutGraph(graph, { keysOnly, direction }),
    [graph, keysOnly, direction],
  )
  const [nodes, setNodes, onNodesChange] = useNodesState<Node>(laid.nodes)
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>(laid.edges)

  // Re-laying out is a deliberate act — changing the direction or hiding the
  // non-key columns — not something that happens under a drag. Positions the
  // operator set by hand survive everything else.
  useEffect(() => {
    setNodes(laid.nodes)
    setEdges(laid.edges)
    window.setTimeout(() => flow.fitView({ padding: 0.15, duration: 300 }), 0)
  }, [laid, setNodes, setEdges, flow])

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

  const matches = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return null
    return new Set(
      graph.tables
        .filter(
          (t) =>
            t.name.toLowerCase().includes(q) ||
            t.columns.some((c) => c.name.toLowerCase().includes(q)),
        )
        .map((t) => t.name),
    )
  }, [query, graph.tables])

  const open = useCallback((s: string, t: string) => onOpenTable?.(s, t), [onOpenTable])

  // The dimming is applied to the rendered nodes rather than baked into the
  // layout, so focusing and searching never move anything.
  const shown = useMemo(() => {
    const active = matches ?? neighbourhood
    return nodes.map((n) => ({
      ...n,
      data: {
        ...(n.data as object),
        dimmed: active ? !active.has(n.id) : false,
        focused: focus === n.id,
        keysOnly,
        onOpen: open,
      } as TableNodeData,
    }))
  }, [nodes, matches, neighbourhood, focus, keysOnly, open])

  const shownEdges = useMemo(() => {
    const active = matches ?? neighbourhood
    return edges.map((e) => {
      const rel = (e.data as { relation: DbGraphEdge }).relation
      const involved = active ? active.has(rel.fromTable) && active.has(rel.toTable) : false
      return {
        ...e,
        data: { ...e.data, active: involved, dimmed: active ? !involved : false },
        markerEnd: {
          type: MarkerType.ArrowClosed,
          width: 16,
          height: 16,
          color: involved ? "var(--color-chart-1)" : "var(--color-muted-foreground)",
        },
      }
    })
  }, [edges, matches, neighbourhood])

  const relayout = () => {
    const next = layoutGraph(graph, { keysOnly, direction })
    setNodes(next.nodes)
    setEdges(next.edges)
    window.setTimeout(() => flow.fitView({ padding: 0.15, duration: 300 }), 0)
  }

  return (
    <Panel className="min-h-0 flex-1">
      <PanelHeader
        title="Schema"
        actions={
          <>
            <SearchInput
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Find a table or column…"
              containerClassName="w-56"
            />
            <Button
              size="sm"
              variant={keysOnly ? "default" : "outline"}
              onClick={() => setKeysOnly((v) => !v)}
              title="Show only primary, foreign and unique key columns"
            >
              <Layout className="size-3.5" />
              Keys only
            </Button>
            <Button
              size="sm"
              variant="outline"
              onClick={() => setDirection((d) => (d === "LR" ? "TB" : "LR"))}
              title={direction === "LR" ? "Lay out top to bottom" : "Lay out left to right"}
            >
              {direction === "LR" ? (
                <ArrowLeftRight className="size-3.5" />
              ) : (
                <ArrowUpDown className="size-3.5" />
              )}
            </Button>
            <Button size="sm" variant="outline" onClick={relayout} title="Lay out again">
              <RotateCounterClockwise className="size-3.5" />
            </Button>
          </>
        }
      />

      {graph.truncated && (
        <Notice className="mx-4 mt-3" title={`Showing the first ${graph.tables.length} tables`}>
          This schema has more tables than a diagram can usefully show.
        </Notice>
      )}

      <div className="relative min-h-0 flex-1">
        <ReactFlow
          nodes={shown}
          edges={shownEdges}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          nodeTypes={nodeTypes}
          edgeTypes={edgeTypes}
          onNodeClick={(_, n) => setFocus((f) => (f === n.id ? null : n.id))}
          onPaneClick={() => setFocus(null)}
          fitView
          fitViewOptions={{ padding: 0.15 }}
          minZoom={0.1}
          maxZoom={2}
          proOptions={{ hideAttribution: true }}
          nodesConnectable={false}
          elementsSelectable
          className="[&_.react-flow\_\_handle]:!border-0"
        >
          <Background
            variant={BackgroundVariant.Dots}
            gap={20}
            size={1}
            className="[&_circle]:fill-border"
          />
          <MiniMap
            pannable
            zoomable
            className="!right-3 !bottom-3 !h-24 !w-40 overflow-hidden !rounded-md !border !bg-card"
            maskColor="color-mix(in oklab, var(--color-background) 70%, transparent)"
            nodeColor="color-mix(in oklab, var(--color-chart-1) 55%, var(--color-muted))"
            nodeStrokeWidth={0}
          />
        </ReactFlow>

        <Toolbar focus={focus} onClear={() => setFocus(null)} />
        <Legend />
      </div>
    </Panel>
  )
}

/**
 * Zoom controls of our own rather than React Flow's, which ship their own
 * borders, shadows and icon set and look like a different product dropped onto
 * the page.
 */
function Toolbar({ focus, onClear }: { focus: string | null; onClear: () => void }) {
  const flow = useReactFlow()
  return (
    <div className="pointer-events-none absolute top-3 left-3 flex items-center gap-2">
      <div className="pointer-events-auto flex items-center gap-0.5 rounded-md border bg-card p-0.5 shadow-sm">
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
          onClick={() => flow.fitView({ padding: 0.15, duration: 300 })}
          aria-label="Fit to view"
        >
          <Fullscreen className="size-3.5" />
        </Button>
      </div>
      {focus && (
        <Button
          size="xs"
          variant="outline"
          className="pointer-events-auto"
          onClick={onClear}
          title="Show the whole schema again"
        >
          <Crosshair className="size-3" />
          {focus}
          <span className="text-muted-foreground">· clear</span>
        </Button>
      )}
    </div>
  )
}

function Legend() {
  return (
    <div className="pointer-events-none absolute bottom-3 left-3 flex items-center gap-3 rounded-md border bg-card/90 px-2.5 py-1.5 text-micro text-muted-foreground shadow-sm backdrop-blur">
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
