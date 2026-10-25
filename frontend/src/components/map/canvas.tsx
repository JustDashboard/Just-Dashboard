"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useRouter } from "next/navigation"
import {
  MiniMap,
  ReactFlow,
  useNodesState,
  useReactFlow,
  useStore,
  useStoreApi,
  type Edge,
  type Node,
  type Viewport,
} from "@xyflow/react"
import "@xyflow/react/dist/style.css"
import {
  ChevronDown,
  Cross,
  Eye,
  EyeOff,
  Fullscreen,
  FullscreenClose,
  Linked,
  Minus,
  Plus,
  RefreshClockwise,
  RotateCounterClockwise,
  SettingsSliders,
  SidebarRightClose,
  SidebarRightOpen,
  Trash,
} from "@/components/icons"
import { IconAction } from "@/components/icon-action"
import { SearchInput } from "@/components/page"
import { PaneHeader } from "@/components/panel"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { useConfirm } from "@/components/confirm-dialog"
import { FormFact } from "@/components/form"
import { WireHost } from "@/components/deploy/wire"
import { Saved, fitPadding } from "@/components/database/diagram/chrome"
import { useMediaQuery } from "@/hooks/use-mobile"
import { post } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import {
  BRANCHES,
  SIZE,
  branchId,
  census,
  followersOf,
  isArranged,
  isFolded,
  kindOfItem,
  layoutMap,
  linksOf,
  searchMap,
  shelfId,
  toggleFold,
  type KindDef,
  type Laid,
  type LaidNode,
  type MapDocument,
  type Point,
} from "@/lib/server-map"
import { notify } from "@/lib/toast"
import type { Board } from "@/lib/boards"
import type { Capability, ServerMap } from "@/lib/types"
import { cn } from "@/lib/utils"
import { KIND_GLYPH } from "@/components/map/marks"
import { VIA_LABEL, edgeTypes } from "@/components/map/edges"
import { MapActionsContext, nodeTypes, type MapActions } from "@/components/map/nodes"
import { MapInspector } from "@/components/map/inspector"
import type { MemoryStatus } from "@/components/map/memory"

const ZOOM = { min: 0.12, max: 2 }

/**
 * The zoom a box's words stop being read at. Below `mid` a box drops its
 * second line, below `far` it is its mark and its dot; the switch is one
 * attribute on the canvas that CSS reads, so zooming re-renders no box.
 */
function zoomBucket(zoom: number): "near" | "mid" | "far" {
  if (zoom < 0.5) return "far"
  if (zoom < 0.8) return "mid"
  return "near"
}

type Drag = {
  id: string
  start: Point
  followers: Map<string, Point>
}

/**
 * The map's canvas: pans, zooms and drags like the ER diagram it is built
 * beside, and remembers what the reader arranged.
 *
 * What it adds is the tree. Dragging a branch takes its shelves with it,
 * dragging the server takes the whole map, and a shelf carries its things as
 * its children; holding Alt drags one box alone. A thing dragged off its
 * shelf stays where it is put, hanging from its shelf by a dashed line, and
 * dropped back on its shelf it returns to its place in the grid.
 *
 * Choosing a thing lights the branch it hangs from and, where the server
 * knows them, its links to the rest of the map — the project a domain routes
 * to, the database a project is bound to, the volume a container mounts —
 * and steps everything else back; the inspector beside the canvas says each
 * one in words.
 */
export function MapCanvas({
  map,
  kinds,
  byDefault,
  doc,
  status,
  updatedAt,
  update,
  reset,
  refresh,
  can,
  full,
  onFullscreen,
}: {
  map: ServerMap
  kinds: KindDef[]
  byDefault: ReadonlySet<string>
  doc: MapDocument
  status: MemoryStatus
  updatedAt?: string
  update: (next: Partial<MapDocument> | ((doc: MapDocument) => MapDocument)) => void
  reset: () => Promise<void>
  refresh: () => void
  can: (capability: Capability) => boolean
  full: boolean
  onFullscreen: () => void
}) {
  const router = useRouter()
  const flow = useReactFlow()
  const { confirm, dialog } = useConfirm()
  const searchRef = useRef<HTMLInputElement>(null)
  const canvasRef = useRef<HTMLDivElement>(null)
  const [selected, setSelected] = useState<string | null>(null)
  const [query, setQuery] = useState("")
  const wide = useMediaQuery("(min-width: 1024px)")
  const [inspectorOpen, setInspectorOpen] = useState(true)
  const zoom = useStore((s) => zoomBucket(s.transform[2]))
  // The far zoom holds names to the screen by scaling them against the zoom;
  // the factor is written straight onto the canvas as the view moves, so the
  // labels follow every frame of a zoom without React drawing anything.
  const store = useStoreApi()
  useEffect(() => {
    const apply = (z: number) =>
      canvasRef.current?.style.setProperty("--map-unzoom", String(1 / Math.max(z, 0.05)))
    apply(store.getState().transform[2])
    return store.subscribe((state) => apply(state.transform[2]))
  }, [store])

  const laid = useMemo(
    () => layoutMap({ map, kinds, doc, byDefault }),
    // The viewport and the two switches are in the document too, and none
    // of them moves a box.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [map, kinds, byDefault, doc.positions, doc.collapsed, doc.opened, doc.expanded],
  )

  const counts = useMemo(() => census(map, kinds), [map, kinds])
  const branchCounts = useMemo(() => {
    const out = new Map<string, { things: number; down: number }>()
    for (const kind of map.kinds) {
      const def = kinds.find((k) => k.key === kind.key)
      if (!def) continue
      const held = out.get(def.branch) ?? { things: 0, down: 0 }
      held.things += Math.max(kind.total, kind.items.length)
      held.down += kind.items.filter((item) => item.tone === "danger").length
      out.set(def.branch, held)
    }
    return out
  }, [map, kinds])

  const readings = useMemo(() => new Map(map.kinds.map((k) => [k.key, k])), [map.kinds])
  const base = useMemo(
    () => toFlowNodes(laid, { readings, branchCounts, counts, hostname: map.host.hostname }),
    [laid, readings, branchCounts, counts, map.host.hostname],
  )
  const [nodes, setNodes, onNodesChange] = useNodesState<Node>(base)
  const drag = useRef<Drag | null>(null)

  // A new reading or a new arrangement lays the canvas out again — but never
  // under a drag, which would snap the box out of the reader's hand.
  const pending = useRef<Node[] | null>(null)
  useEffect(() => {
    if (drag.current) pending.current = base
    else setNodes(base)
  }, [base, setNodes])

  const restored = useRef(false)
  useEffect(() => {
    if (restored.current) return
    restored.current = true
    window.setTimeout(() => {
      if (doc.viewport) void flow.setViewport(doc.viewport)
      else void flow.fitView({ padding: fitPadding(doc.minimap), maxZoom: 0.9, minZoom: 0.3 })
    }, 0)
  }, [doc.viewport, doc.minimap, flow])

  const links = useMemo(
    () => (selected && doc.connections ? linksOf(map, selected) : []),
    [selected, doc.connections, map],
  )
  const related = useMemo(() => {
    if (!selected) return null
    const ids = new Set<string>([selected])
    // A shelf chosen lights what is on it; nothing on the map links to a
    // shelf as such.
    if (selected.startsWith("shelf:")) {
      for (const item of map.kinds.find((k) => shelfId(k.key) === selected)?.items ?? []) {
        ids.add(item.id)
      }
      return ids
    }
    for (const link of linksOf(map, selected)) {
      ids.add(link.from)
      ids.add(link.to)
    }
    return ids
  }, [selected, map])
  const matches = useMemo(() => searchMap(map, query), [map, query])

  // What stays lit while something is chosen or searched: the things
  // themselves, and the shelves and branches they hang from, so the reader
  // can still see where each one lives.
  // A box chosen outranks the search that found it: following a link from the
  // inspector must light the new box, not the words still in the search.
  const lit = useMemo(() => {
    const things = related ?? matches
    if (!things) return null
    const out = new Set(things)
    for (const node of laid.nodes) {
      if (node.type === "item" && things.has(node.id)) {
        out.add(shelfId(node.kind.key))
        out.add(branchId(node.kind.branch))
      }
      if (node.type === "shelf" && things.has(node.id)) out.add(branchId(node.kind.branch))
    }
    out.add("server")
    return out
  }, [matches, related, laid.nodes])

  // The data each node draws, kept as the same object while nothing it
  // draws has changed (`flaggedData`), so a click re-renders the boxes it
  // changes and not the other three hundred.
  const shown = useMemo(
    () =>
      nodes.map((node) => {
        const data = flaggedData(node.data as Record<string, unknown>, {
          dimmed: lit ? !lit.has(node.id) : false,
          selected: node.id === selected,
          related: related ? related.has(node.id) && node.id !== selected : false,
        })
        return data === node.data ? node : { ...node, data }
      }),
    [nodes, lit, selected, related],
  )

  const edges = useMemo(() => {
    const at = absolutePositions(nodes)
    const out: Edge[] = laid.edges.map((edge) => {
      const side = edge.tier === "away" ? sideBetween(at, edge.source, edge.target) : edge.side
      return {
        id: edge.id,
        source: edge.source,
        target: edge.target,
        type: "branch",
        sourceHandle: side === "right" ? "sr" : "sl",
        targetHandle: side === "right" ? "tl" : "tr",
        data: {
          tier: edge.tier,
          lit: lit ? lit.has(edge.source) && lit.has(edge.target) && Boolean(selected) : false,
          dimmed: lit ? !(lit.has(edge.source) && lit.has(edge.target)) : false,
        },
        selectable: false,
        focusable: false,
      }
    })
    for (const link of links) {
      if (!at.has(link.from) || !at.has(link.to)) continue
      const handles = relationHandles(at, link.from, link.to)
      out.push({
        id: `r:${link.from}:${link.to}:${link.via}`,
        source: link.from,
        target: link.to,
        type: "relation",
        ...handles,
        data: { via: link.via, label: VIA_LABEL[link.via] ?? link.via },
        // Over the shelves' ground and under the boxes on them, so a link
        // never strikes through the name of a thing it passes.
        zIndex: 1,
        selectable: false,
        focusable: false,
      })
    }
    return out
  }, [laid.edges, links, nodes, lit, selected])

  // A thing chosen from the search or the inspector is brought into view
  // with what it is wired to: its branch and shelf unfolded and, where it
  // sat behind "+N more", its shelf opened. The view moves once the layout
  // has drawn it (`framing`, read when the nodes change).
  const framing = useRef<string | null>(null)
  const frame = useCallback(
    (id: string, drawn: Node[]) => {
      const around = new Set([id])
      for (const link of linksOf(map, id)) around.add(link.from === id ? link.to : link.from)
      const ids = [...around].filter((one) => drawn.some((n) => n.id === one))
      window.requestAnimationFrame(
        () =>
          void flow.fitView({
            nodes: ids.map((one) => ({ id: one })),
            padding: 0.3,
            duration: 400,
            maxZoom: 1,
          }),
      )
    },
    [map, flow],
  )
  const reveal = useCallback(
    (id: string) => {
      setSelected(id)
      const drawn = flow.getNodes()
      if (drawn.some((n) => n.id === id)) {
        frame(id, drawn)
        return
      }
      const def = kinds.find((k) => k.key === kindOfItem(map, id))
      if (!def) return
      framing.current = id
      update((d) => {
        let next = d
        const branch = branchId(def.branch)
        const shelf = shelfId(def.key)
        if (isFolded(next, branch, byDefault)) next = toggleFold(next, branch, byDefault)
        if (isFolded(next, shelf, byDefault)) next = toggleFold(next, shelf, byDefault)
        if (!next.expanded.includes(shelf)) next = { ...next, expanded: [...next.expanded, shelf] }
        return next
      })
    },
    [flow, frame, kinds, map, update, byDefault],
  )
  useEffect(() => {
    const id = framing.current
    if (!id || !nodes.some((n) => n.id === id)) return
    framing.current = null
    frame(id, nodes)
  }, [nodes, frame])

  const choose = useCallback(
    (id: string | null) => {
      setSelected((current) => (current === id ? null : id))
      if (id && !wide) setInspectorOpen(true)
    },
    [wide],
  )

  const add = useCallback(
    async (kind: KindDef) => {
      const action = kind.add
      if (!action) return
      if (action.create === "drawing") {
        try {
          const drawing = await post<Board>("/boards/", { name: "Untitled drawing" })
          router.push(`/excalidraw/${drawing.id}`)
        } catch (err) {
          notify.error("Could not make the drawing", err)
        }
        return
      }
      if (kind.key === "terminals") {
        const home = readings.get("terminals")?.facts?.find((f) => f.label === "home")?.value
        router.push(home ? `/terminal?cwd=${encodeURIComponent(home)}` : "/terminal")
        return
      }
      if (action.href) router.push(action.href)
    },
    [router, readings],
  )

  const canAdd = useCallback(
    (kind: KindDef) => Boolean(kind.add && can(kind.add.capability)),
    [can],
  )

  const actions = useMemo<MapActions>(
    () => ({
      toggleFold: (id) => update((d) => toggleFold(d, id, byDefault)),
      expand: (kind) =>
        update((d) => {
          const id = shelfId(kind)
          return {
            ...d,
            expanded: d.expanded.includes(id)
              ? d.expanded.filter((e) => e !== id)
              : [...d.expanded, id],
          }
        }),
      add: (kind) => void add(kind),
      open: (href) => router.push(href),
      can: canAdd,
    }),
    [update, byDefault, add, router, canAdd],
  )

  const onNodeDragStart = useCallback(
    (event: MouseEvent | TouchEvent, node: Node) => {
      const followers = new Map<string, Point>()
      if (!event.altKey) {
        const ids = new Set(followersOf(laid, node.id))
        for (const n of flow.getNodes()) if (ids.has(n.id)) followers.set(n.id, n.position)
      }
      drag.current = { id: node.id, start: node.position, followers }
    },
    [laid, flow],
  )

  const onNodeDrag = useCallback(
    (_: MouseEvent | TouchEvent, node: Node) => {
      const held = drag.current
      if (!held || held.id !== node.id || held.followers.size === 0) return
      const dx = node.position.x - held.start.x
      const dy = node.position.y - held.start.y
      setNodes((list) =>
        list.map((n) => {
          const from = held.followers.get(n.id)
          return from ? { ...n, position: { x: from.x + dx, y: from.y + dy } } : n
        }),
      )
    },
    [setNodes],
  )

  const onNodeDragStop = useCallback(
    (_: MouseEvent | TouchEvent, node: Node, dragged: Node[]) => {
      const held = drag.current
      drag.current = null
      const current = flow.getNodes()
      const byId = new Map(current.map((n) => [n.id, n]))
      const set: Record<string, Point> = {}
      const unset = new Set<string>()
      for (const moved of dragged.length > 0 ? dragged : [node]) {
        const laidNode = laid.nodes.find((n) => n.id === moved.id)
        if (!laidNode) continue
        if (laidNode.type === "item") {
          const shelf = byId.get(shelfId(laidNode.kind.key))
          const absolute = moved.parentId
            ? {
                x: (byId.get(moved.parentId)?.position.x ?? 0) + moved.position.x,
                y: (byId.get(moved.parentId)?.position.y ?? 0) + moved.position.y,
              }
            : moved.position
          const centre = {
            x: absolute.x + SIZE.item.width / 2,
            y: absolute.y + SIZE.item.height / 2,
          }
          const onShelf = shelf && inside(centre, shelf)
          if (onShelf) unset.add(moved.id)
          else set[moved.id] = absolute
          continue
        }
        set[moved.id] = moved.position
      }
      if (held && held.id === node.id) {
        for (const id of held.followers.keys()) {
          const follower = byId.get(id)
          if (follower) set[id] = follower.position
        }
      }
      update((d) => {
        const positions = { ...d.positions, ...set }
        for (const id of unset) delete positions[id]
        return { ...d, positions }
      })
      // A thing dropped back on its shelf, or a drop the layout does not
      // change, is put where the layout says.
      if (pending.current) {
        setNodes(pending.current)
        pending.current = null
      } else if (unset.size > 0) {
        setNodes(base)
      }
    },
    [flow, laid.nodes, update, setNodes, base],
  )

  const tidy = () => {
    const before = doc.positions
    update((d) => ({ ...d, positions: {} }))
    window.setTimeout(
      () => void flow.fitView({ padding: fitPadding(doc.minimap), duration: 300, maxZoom: 0.9 }),
      50,
    )
    notify.success("Laid out again", {
      description: "Every box is back in its place on the tree.",
      action: { label: "Undo", onClick: () => update((d) => ({ ...d, positions: before })) },
    })
  }

  const foldAll = (fold: boolean) =>
    update((d) => ({
      ...d,
      collapsed: fold
        ? BRANCHES.map((b) => branchId(b.key)).filter((id) => laid.nodes.some((n) => n.id === id))
        : [],
      opened: fold ? [] : [...byDefault],
    }))

  const forget = () =>
    confirm({
      title: "Forget this layout",
      confirmLabel: "Forget layout",
      subject: {
        mark: <WireHost size="sm" />,
        name: map.host.hostname || "This server",
        facts: (
          <>
            <FormFact label="Placed">{Object.keys(doc.positions).length}</FormFact>
            <FormFact label="Folded">{doc.collapsed.length}</FormFact>
            <FormFact label="Opened">{doc.expanded.length}</FormFact>
          </>
        ),
      },
      description: (
        <p>
          Where you put each box, and which branches and shelves you folded or opened, are thrown
          away. Your map is laid out fresh; nobody else&rsquo;s changes, and nothing on the server
          does.
        </p>
      ),
      action: async () => {
        await reset()
        setSelected(null)
        window.setTimeout(
          () => void flow.fitView({ padding: fitPadding(doc.minimap), duration: 300 }),
          50,
        )
      },
    })

  const addable = kinds.filter((kind) => kind.add && can(kind.add.capability))

  const onKey = (e: KeyboardEvent | React.KeyboardEvent) => {
    const target = e.target as HTMLElement
    const typing =
      target.tagName === "INPUT" ||
      target.tagName === "TEXTAREA" ||
      target.isContentEditable ||
      target.closest("[role=dialog],[role=menu]") !== null
    if (e.key === "Escape") {
      if (typing) return
      setSelected(null)
      setQuery("")
      return
    }
    if (typing || e.metaKey || e.ctrlKey || e.altKey) return
    if (e.key === "f") void flow.fitView({ padding: fitPadding(doc.minimap), duration: 300 })
    else if (e.key === "/") {
      e.preventDefault()
      searchRef.current?.focus()
    } else if (e.key === "i") setInspectorOpen((open) => !open)
    else if (e.key === "Enter" && selected) {
      const href = itemHref(map, selected)
      if (href) router.push(href)
    }
  }

  // The map is the whole page, so its keys are heard while the keyboard is
  // nowhere in particular as well as inside it: a press in the inspector
  // that redraws the inspector leaves the focus on the page's body, and
  // Escape must still let the chosen box go. Anywhere else on the page —
  // the sidebar, a menu — the keys are that control's.
  const keys = useRef(onKey)
  useEffect(() => {
    keys.current = onKey
  })
  useEffect(() => {
    const listen = (e: KeyboardEvent) => {
      if (document.activeElement === document.body || document.activeElement === null) {
        keys.current(e)
      }
    }
    window.addEventListener("keydown", listen)
    return () => window.removeEventListener("keydown", listen)
  }, [])

  const inspecting = inspectorOpen && (wide || selected !== null)
  const hits = matches?.size ?? null

  return (
    <MapActionsContext.Provider value={actions}>
      <div className="flex min-h-0 min-w-0 flex-1 flex-col" onKeyDown={onKey}>
        <PaneHeader className="min-h-11 flex-wrap gap-x-1.5 gap-y-1">
          <SearchInput
            ref={searchRef}
            dense
            value={query}
            onChange={(e) => {
              // Typing asks a new question: what was chosen lets go, so the
              // search's own answer is what lights.
              setQuery(e.target.value)
              setSelected(null)
            }}
            onKeyDown={(e) => {
              if (e.key === "Enter" && matches && matches.size > 0) {
                const first = matches.values().next().value as string
                reveal(first)
              }
              if (e.key === "Escape") {
                setQuery("")
                ;(e.target as HTMLInputElement).blur()
              }
            }}
            placeholder="Find anything on the server"
            aria-label="Find anything on the server"
            containerClassName="w-60 sm:w-64"
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
          <span className="ml-1 hidden items-center gap-1.5 text-hint text-muted-foreground md:flex">
            <span className="numeric text-foreground">{counts.things.toLocaleString("en-US")}</span>
            things
            {counts.down > 0 && (
              <>
                <span aria-hidden>·</span>
                <span className="numeric text-destructive">{counts.down} down</span>
              </>
            )}
            <span aria-hidden>·</span>
            <span title={map.checkedAt}>read {relativeTime(map.checkedAt)}</span>
          </span>

          <span className="flex-1" />

          <Saved status={status} updatedAt={updatedAt} arranged={isArranged(doc)} />

          {addable.length > 0 && (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button size="xs" className="h-7">
                  <Plus />
                  Add
                  <ChevronDown className="size-3" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="max-h-[70svh] w-60 overflow-y-auto">
                {BRANCHES.map((branch) => {
                  const here = addable.filter((kind) => kind.branch === branch.key)
                  if (here.length === 0) return null
                  return (
                    <div key={branch.key}>
                      <DropdownMenuLabel>{branch.label}</DropdownMenuLabel>
                      {here.map((kind) => {
                        const Glyph = KIND_GLYPH[kind.key]
                        return (
                          <DropdownMenuItem key={kind.key} onSelect={() => void add(kind)}>
                            {Glyph && <Glyph />}
                            {kind.add?.label}
                          </DropdownMenuItem>
                        )
                      })}
                    </div>
                  )
                })}
              </DropdownMenuContent>
            </DropdownMenu>
          )}

          <IconAction
            label="Tidy up: put every box back on the tree"
            className="size-7"
            onClick={tidy}
            disabled={Object.keys(doc.positions).length === 0}
          >
            <RotateCounterClockwise />
          </IconAction>

          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button size="xs" variant="ghost" className="h-7" aria-label="Map options">
                <SettingsSliders />
                <span className="max-md:sr-only">Options</span>
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-64">
              <DropdownMenuLabel>Canvas</DropdownMenuLabel>
              <DropdownMenuCheckboxItem
                checked={doc.connections}
                onCheckedChange={(v) => update({ connections: v })}
              >
                Draw what the chosen box is wired to
              </DropdownMenuCheckboxItem>
              <DropdownMenuCheckboxItem
                checked={doc.minimap}
                onCheckedChange={(v) => update({ minimap: v })}
              >
                Minimap
              </DropdownMenuCheckboxItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem onSelect={() => foldAll(true)}>
                <EyeOff />
                Fold every branch
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={() => foldAll(false)}>
                <Eye />
                Unfold everything
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={refresh}>
                <RefreshClockwise />
                Read the server again
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem variant="destructive" disabled={!isArranged(doc)} onSelect={forget}>
                <Trash />
                Forget this layout…
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>

          <IconAction
            label={inspecting ? "Hide the inspector (I)" : "Show the inspector (I)"}
            aria-pressed={inspecting}
            className="size-7"
            onClick={() => setInspectorOpen((open) => !open)}
          >
            {inspecting ? <SidebarRightClose /> : <SidebarRightOpen />}
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

        <div
          className={cn(
            "grid min-h-0 flex-1",
            inspecting
              ? "grid-cols-1 max-lg:grid-rows-[minmax(0,1fr)_minmax(0,45%)] lg:grid-cols-[minmax(0,1fr)_20rem]"
              : "grid-cols-1",
          )}
        >
          <div
            ref={canvasRef}
            data-zoom={zoom}
            data-slot="server-map"
            className="group/map relative min-h-0 min-w-0"
          >
            <ReactFlow
              nodes={shown}
              edges={edges}
              onNodesChange={onNodesChange}
              nodeTypes={nodeTypes}
              edgeTypes={edgeTypes}
              onNodeClick={(_, n) => {
                // The server is what the inspector shows with nothing chosen:
                // choosing it would only step every other box back.
                if (n.type === "server") setSelected(null)
                else if (n.type === "item" || n.type === "shelf") choose(n.id)
              }}
              onNodeDoubleClick={(_, n) => {
                const href = n.type === "item" ? itemHref(map, n.id) : undefined
                if (href) router.push(href)
              }}
              onNodeDragStart={onNodeDragStart}
              onNodeDrag={onNodeDrag}
              onNodeDragStop={onNodeDragStop}
              onPaneClick={() => setSelected(null)}
              onMoveEnd={(event, viewport: Viewport) => {
                // A null event is the canvas moving itself (the first fit, a
                // pan to a search hit), not something to keep.
                if (event && restored.current) update({ viewport })
              }}
              minZoom={ZOOM.min}
              maxZoom={ZOOM.max}
              proOptions={{ hideAttribution: true }}
              nodesConnectable={false}
              elementsSelectable
              // Most of a big server is off screen at any zoom that reads:
              // what is not seen is not drawn.
              onlyRenderVisibleElements
              deleteKeyCode={null}
              selectionKeyCode={null}
              multiSelectionKeyCode="Shift"
              aria-label="Map of the server"
            >
              {doc.minimap && (
                <MiniMap
                  pannable
                  zoomable
                  ariaLabel="Minimap"
                  className="!right-3 !bottom-3 !h-28 !w-44 overflow-hidden !rounded-md !border !bg-card max-sm:!hidden"
                  maskColor="color-mix(in oklab, var(--color-background) 70%, transparent)"
                  nodeColor={minimapColor}
                  nodeStrokeWidth={0}
                />
              )}
            </ReactFlow>
            <ZoomButtons
              onFit={() => void flow.fitView({ padding: fitPadding(doc.minimap), duration: 300 })}
            />
            {selected && (
              <div className="pointer-events-none absolute top-3 left-1/2 flex -translate-x-1/2 items-center gap-2">
                <span className="pointer-events-auto flex items-center gap-2 rounded-md border border-hairline bg-popover px-2.5 py-1 text-hint text-muted-foreground shadow-sm">
                  <Linked className="size-3.5" />
                  {links.length > 0
                    ? `${plural(links.length, "link")} drawn`
                    : doc.connections
                      ? "Nothing on the map is wired to it"
                      : "Links are switched off"}
                  <button
                    type="button"
                    className="rounded-sm px-1 text-foreground focus-ring hover:bg-accent"
                    onClick={() => setSelected(null)}
                  >
                    Clear
                  </button>
                </span>
              </div>
            )}
          </div>

          {inspecting && (
            <MapInspector
              map={map}
              kinds={kinds}
              selected={selected}
              onChoose={reveal}
              onClose={() => setInspectorOpen(false)}
              onReturn={(id) =>
                update((d) => {
                  const positions = { ...d.positions }
                  delete positions[id]
                  return { ...d, positions }
                })
              }
              placed={(id) => doc.positions[id] !== undefined}
              onAdd={(kind) => void add(kind)}
              canAdd={canAdd}
              folded={(id) => isFolded(doc, id, byDefault)}
            />
          )}
        </div>
      </div>
      {dialog}
    </MapActionsContext.Provider>
  )
}

/**
 * A node's data with the chosen box's flags laid over it, as one object per
 * data and flags, made once. React Flow re-renders a node whose data changed;
 * held here, the same base and the same flags are the same object, so a click
 * re-renders the boxes it lit or dimmed and leaves the rest alone. Keyed by
 * the base object, so a new reading lets the old ones go.
 */
const flagged = new WeakMap<object, Map<string, Record<string, unknown>>>()

function flaggedData(
  base: Record<string, unknown>,
  flags: { dimmed: boolean; selected: boolean; related: boolean },
) {
  if (!flags.dimmed && !flags.selected && !flags.related) return base
  const key = `${flags.dimmed ? 1 : 0}${flags.selected ? 1 : 0}${flags.related ? 1 : 0}`
  let variants = flagged.get(base)
  if (!variants) {
    variants = new Map()
    flagged.set(base, variants)
  }
  let data = variants.get(key)
  if (!data) {
    data = { ...base, ...flags }
    variants.set(key, data)
  }
  return data
}

/** Zoom controls of the product's own, as the ER diagram draws them. */
function ZoomButtons({ onFit }: { onFit: () => void }) {
  const flow = useReactFlow()
  return (
    <div className="absolute bottom-3 left-3 flex items-center gap-0.5 rounded-md border border-hairline bg-popover p-0.5">
      <IconAction
        label="Zoom out"
        className="size-7"
        onClick={() => void flow.zoomOut({ duration: 150 })}
      >
        <Minus />
      </IconAction>
      <IconAction
        label="Zoom in"
        className="size-7"
        onClick={() => void flow.zoomIn({ duration: 150 })}
      >
        <Plus />
      </IconAction>
      <Button size="xs" variant="ghost" className="h-7 px-2 text-hint" onClick={onFit}>
        Fit
      </Button>
    </div>
  )
}

function minimapColor(node: Node) {
  const tone = (node.data as { item?: { tone?: string } }).item?.tone
  if (tone === "danger") return "var(--color-destructive)"
  if (tone === "warning") return "var(--color-warning)"
  if (node.type === "server") return "var(--color-chart-1)"
  if (node.type === "shelf") return "color-mix(in oklab, var(--color-muted) 60%, transparent)"
  return "color-mix(in oklab, var(--color-muted-foreground) 55%, var(--color-muted))"
}

function inside(point: Point, node: Node) {
  const width = node.width ?? 0
  const height = node.height ?? 0
  return (
    point.x >= node.position.x &&
    point.x <= node.position.x + width &&
    point.y >= node.position.y &&
    point.y <= node.position.y + height
  )
}

/** Every node's absolute place: a child's position is its shelf's plus its own. */
function absolutePositions(nodes: Node[]) {
  const byId = new Map(nodes.map((n) => [n.id, n]))
  const out = new Map<string, { x: number; w: number }>()
  for (const node of nodes) {
    const parent = node.parentId ? byId.get(node.parentId) : undefined
    out.set(node.id, {
      x: (parent?.position.x ?? 0) + node.position.x,
      w: node.width ?? 0,
    })
  }
  return out
}

/** Which side a line leaves from: towards wherever the other end is. */
function sideBetween(at: Map<string, { x: number; w: number }>, from: string, to: string) {
  const a = at.get(from)
  const b = at.get(to)
  if (!a || !b) return "right" as const
  return a.x + a.w / 2 <= b.x + b.w / 2 ? ("right" as const) : ("left" as const)
}

/**
 * Where a link leaves and arrives: across the gap between two boxes side by
 * side, and round the left of a column for two boxes stacked in one, where a
 * line between their facing sides would cut through the column.
 */
function relationHandles(at: Map<string, { x: number; w: number }>, from: string, to: string) {
  const a = at.get(from)
  const b = at.get(to)
  if (a && b && Math.abs(a.x - b.x) < Math.max(a.w, b.w)) {
    return { sourceHandle: "sl", targetHandle: "tl" }
  }
  return sideBetween(at, from, to) === "right"
    ? { sourceHandle: "sr", targetHandle: "tl" }
    : { sourceHandle: "sl", targetHandle: "tr" }
}

function itemHref(map: ServerMap, id: string) {
  for (const kind of map.kinds) {
    const item = kind.items.find((one) => one.id === id)
    if (item) return item.href
  }
  return undefined
}

/** The layout as React Flow's nodes, with the data each box draws. */
function toFlowNodes(
  laid: Laid,
  ctx: {
    readings: Map<string, ServerMap["kinds"][number]>
    branchCounts: Map<string, { things: number; down: number }>
    counts: { things: number; down: number }
    hostname: string
  },
): Node[] {
  return laid.nodes.map((node: LaidNode) => {
    // Every box's size is the layout's, so it is handed over as already
    // measured: React Flow keeps a node's handles — where its lines attach —
    // only while the node says it has been measured, and a reading or a click
    // that hands it fresh nodes without that drops every line on the canvas.
    const size = { width: node.width, height: node.height }
    const common = {
      id: node.id,
      type: node.type,
      position: node.position,
      ...size,
      measured: size,
      style: size,
    }
    switch (node.type) {
      case "server":
        return {
          ...common,
          data: { things: ctx.counts.things, down: ctx.counts.down, hostname: ctx.hostname },
          zIndex: 2,
        }
      case "branch": {
        const counted = ctx.branchCounts.get(node.branch.key) ?? { things: 0, down: 0 }
        return {
          ...common,
          selectable: false,
          data: {
            branch: node.branch,
            folded: node.folded,
            things: counted.things,
            down: counted.down,
            shelves: node.kinds.length,
          },
        }
      }
      case "shelf": {
        const reading = ctx.readings.get(node.kind.key)
        return {
          ...common,
          data: {
            kind: node.kind,
            reading,
            folded: node.folded,
            expanded: node.expanded,
            drawn: reading?.items.length ?? 0,
          },
        }
      }
      case "item":
        return {
          ...common,
          parentId: node.parentId,
          data: { item: node.item, kind: node.kind.key },
          // A box placed by hand sits over whatever it was put on, the next
          // shelf's boxes included.
          zIndex: node.parentId ? 2 : 3,
        }
      default:
        return {
          ...common,
          parentId: node.parentId,
          draggable: false,
          selectable: false,
          data: { kind: node.kind, count: node.count },
        }
    }
  })
}
