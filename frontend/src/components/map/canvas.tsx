"use client"

import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import {
  ReactFlow,
  useReactFlow,
  useStore,
  useStoreApi,
  type Edge,
  type Node,
  type NodeChange,
  type Viewport,
} from "@xyflow/react"
import "@xyflow/react/dist/style.css"
import {
  ArrowUpRight,
  Fullscreen,
  FullscreenClose,
  Minus,
  Plus,
  RotateCounterClockwise,
  SidebarRightClose,
  SidebarRightOpen,
  Warning,
} from "@/components/icons"
import { IconAction } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import { useMediaQuery } from "@/hooks/use-mobile"
import { isArranged, type MapDocument, type Point } from "@/lib/server-map"
import {
  FRAME,
  layoutAtlas,
  neighbours,
  settle,
  systemTitle,
  type Placed,
  type Settled,
  type System,
  type SystemNode,
} from "@/lib/server-systems"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { CANVAS_MOVE, useCanvasPinch, zoomKey } from "@/components/canvas-move"
import { SERVER_BOARD, type MapBoard } from "@/components/map/board"
import { edgeTypes, type WireData } from "@/components/map/edges"
import { MapInspector } from "@/components/map/inspector"
import {
  nodeTypes,
  type FaceData,
  type FrameData,
  type NodeState,
  type Port,
  type StackData,
} from "@/components/map/nodes"
import { SystemRail } from "@/components/map/rail"

const ZOOM = { min: 0.06, max: 2 }
/** A system flown to fills the canvas, but a small one is not blown up past its own size. */
const FLY = { padding: 0.04, maxZoom: 1 }

/**
 * Where the map is nothing but the map: below the width the rail and the
 * inspector fit beside a canvas, there is no chrome at all. A tap on a box or
 * a system's head offers one way on, to its page.
 */
const BARE = "(max-width: 1023px)"

/** The ground of a control that floats on the canvas. */
const FLOAT =
  "absolute flex items-center gap-0.5 rounded-md border border-hairline bg-popover p-0.5"

const clampZoom = (z: number) => Math.min(ZOOM.max, Math.max(ZOOM.min, z))

type Seat = { system: System; placed: Placed; origin: Point }

const atlasCorner = (p: Placed): Point => ({ x: p.x, y: p.y })

/**
 * A box's data, one object per box per state, so a click re-renders only the
 * boxes it lit or dimmed: React Flow redraws a node whose data changed. Keyed
 * by the laid-out node, so a new reading lets the old ones go.
 */
const faceCache = new WeakMap<SystemNode, Map<string, FaceData>>()
function faceData(node: SystemNode, state: NodeState, ports: Port[], sides: FaceData["sides"]) {
  const key = `${state ?? ""}|${sides.in ? 1 : 0}${sides.out ? 1 : 0}|${ports.map((p) => `${p.id}@${Math.round(p.left)}`).join(",")}`
  let per = faceCache.get(node)
  if (!per) {
    per = new Map()
    faceCache.set(node, per)
  }
  let data = per.get(key)
  if (!data) {
    data = { node, state, ports, sides }
    per.set(key, data)
  }
  return data
}

/** A reading the canvas could not draw from, said over it rather than drawn as empty. */
export type Unread = { key: string; label: string; reason?: string }

/**
 * The map's canvas: every system's diagram in its own frame, side by side,
 * with the rail of systems down its left and the inspector down its right, and
 * nothing over it but the few controls that float on it. On a phone it is the
 * canvas alone.
 *
 * The server map draws every system the server runs on it; the databases map
 * draws one diagram for each database on the same canvas, so both move,
 * arrange and are read the same way. What differs is the board (`board.ts`)
 * and what is kept of the arrangement (`memory.ts`).
 *
 * Choosing a box lights what it is wired to inside its system, draws those
 * lines in the brand's blue with how each is known written on them, and steps
 * the rest of the system back. A box can be dragged where the reader wants
 * it; the place is kept against the account, inside its system, so a
 * reading that adds a container lays that one out and moves nothing else.
 */
export function MapCanvas({
  systems,
  unread,
  board = SERVER_BOARD,
  empty = <EmptyMap />,
  doc,
  update,
  full,
  onFullscreen,
  initialSystem,
}: {
  /** Every system, laid out on its own (`detectSystems`, `detectDatabaseSystems`). */
  systems: System[]
  /** What could not be read, each said over the canvas. */
  unread: Unread[]
  board?: MapBoard
  /** What the canvas says with no system to draw. */
  empty?: ReactNode
  doc: MapDocument
  update: (next: Partial<MapDocument> | ((doc: MapDocument) => MapDocument)) => void
  full: boolean
  onFullscreen: () => void
  /** A system asked for by the address (`?system=`), flown to on arrival. */
  initialSystem?: string
}) {
  const flow = useReactFlow()
  const router = useRouter()
  const canvasRef = useRef<HTMLDivElement>(null)
  const store = useStoreApi()
  const bare = useMediaQuery(BARE)

  const placed = useMemo(() => layoutAtlas(systems), [systems])

  // Where the reader carried whole systems by their heads: the document's,
  // with the carry in hand laid over it, the same way as a box's drag below.
  // A system nobody carried keeps the place the atlas gives it.
  const [carrying, setCarrying] = useState<Record<string, Point> | null>(null)
  const carryingRef = useRef<Record<string, Point> | null>(null)
  const frames = carrying ?? doc.frames
  const corner = useCallback((p: Placed): Point => frames[p.system.id] ?? atlasCorner(p), [frames])

  const seats = useMemo(() => {
    const out = new Map<string, Seat>()
    for (const p of placed) {
      const at = corner(p)
      const origin = { x: at.x + FRAME.pad, y: at.y + FRAME.head + FRAME.pad }
      for (const n of p.system.nodes) out.set(n.id, { system: p.system, placed: p, origin })
    }
    return out
  }, [placed, corner])
  const bySystem = useMemo(() => new Map(placed.map((p) => [p.system.id, p])), [placed])

  // Where the reader dragged boxes: the document's, with the drag in hand laid
  // over it so a box moves at the pointer's pace and is saved once, when it
  // is let go.
  const [dragging, setDragging] = useState<Record<string, Point> | null>(null)
  const draggingRef = useRef<Record<string, Point> | null>(null)
  const positions = dragging ?? doc.positions
  const settled = useMemo(() => {
    const out = new Map<string, Settled>()
    for (const p of placed) out.set(p.system.id, settle(p.system, positions))
    return out
  }, [placed, positions])

  const [chosen, setChosen] = useState<string | null>(null)
  const [focus, setFocus] = useState<string | null>(initialSystem ?? doc.focus ?? null)
  const [hovered, setHovered] = useState<string | null>(null)
  // The system whose head was tapped on a phone, for the way on to its page.
  const [tapped, setTapped] = useState<string | null>(null)
  const [inspectorOpen, setInspectorOpen] = useState(true)
  const inspecting = inspectorOpen && !bare

  const chosenSeat = chosen ? seats.get(chosen) : undefined
  const chosenNode = chosenSeat?.system.nodes.find((n) => n.id === chosen)
  const focusSystem = focus ? bySystem.get(focus)?.system : undefined
  const lit = useMemo(
    () => (chosenSeat && chosen ? neighbours(chosenSeat.system, chosen) : null),
    [chosenSeat, chosen],
  )
  const frameRect = useCallback(
    (id: string) => {
      const p = bySystem.get(id)
      const s = settled.get(id)
      if (!p || !s) return null
      const at = corner(p)
      return {
        x: at.x + s.minX,
        y: at.y + s.minY,
        width: Math.max(p.width, s.maxX - s.minX + FRAME.pad * 2),
        height: Math.max(p.height, s.maxY - s.minY + FRAME.head + FRAME.pad * 2),
      }
    },
    [bySystem, settled, corner],
  )

  const flyTo = useCallback(
    (id: string, duration = 450) => {
      void flow.fitView({ ...FLY, nodes: [{ id: `frame:${id}` }], duration })
    },
    [flow],
  )
  const showAll = useCallback(() => {
    setFocus(null)
    setChosen(null)
    update({ focus: undefined })
    void flow.fitView({ padding: 0.06, duration: 450 })
  }, [flow, update])
  const focusOn = useCallback(
    (id: string, fly = true) => {
      setFocus(id)
      setChosen(null)
      update({ focus: id })
      if (fly) flyTo(id)
    },
    [flyTo, update],
  )

  const frameNode = useCallback(
    (id: string) => {
      const seat = seats.get(id)
      if (!seat) return
      const ids = [...neighbours(seat.system, id)]
      void flow.fitView({
        nodes: ids.map((n) => ({ id: n })),
        padding: 0.25,
        duration: 400,
        maxZoom: 1.1,
      })
    },
    [flow, seats],
  )

  // A whiteboard's keys, wherever the page has the keyboard: Ctrl or ⌘ with
  // + and − zooms, with 0 is the real size; Shift+1 fits everything, Shift+2
  // what is chosen.
  const chosenRef = useRef<{ chosen: string | null; focus: string | null }>({
    chosen: null,
    focus: null,
  })
  useEffect(() => {
    chosenRef.current = { chosen, focus }
  }, [chosen, focus])
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement | null
      if (
        target?.closest?.("input, textarea, select, [contenteditable], [role=dialog], [role=menu]")
      )
        return
      const zoom = zoomKey(e)
      if (zoom) {
        e.preventDefault()
        if (zoom === "in") void flow.zoomIn({ duration: 150 })
        else if (zoom === "out") void flow.zoomOut({ duration: 150 })
        else void flow.zoomTo(1, { duration: 150 })
      } else if (e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey && e.code === "Digit1") {
        e.preventDefault()
        void flow.fitView({ padding: 0.06, duration: 300 })
      } else if (e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey && e.code === "Digit2") {
        e.preventDefault()
        const { chosen: id, focus: system } = chosenRef.current
        if (id) frameNode(id)
        else if (system) flyTo(system)
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [flow, flyTo, frameNode])

  useCanvasPinch(canvasRef, ZOOM)

  // The first view: the system the address or the last visit named, else
  // where the reader left the canvas, else every system at once. A phone opens
  // on a system whatever the desk left behind: every system at once is too
  // small there to read or to tap.
  const restored = useRef(false)
  useEffect(() => {
    if (restored.current || placed.length === 0) return
    restored.current = true
    window.setTimeout(() => {
      const wanted = initialSystem ?? doc.focus
      const width = canvasRef.current?.clientWidth
      const rect = bare
        ? frameRect(wanted && bySystem.has(wanted) ? wanted : placed[0].system.id)
        : null
      if (rect && width) {
        // Its width across the phone and its head at the top: a wide diagram
        // centred on a tall screen leaves half of it empty above.
        const zoom = clampZoom(Math.min(1, (width - 16) / rect.width))
        void flow.setViewport({
          x: (width - rect.width * zoom) / 2 - rect.x * zoom,
          y: 8 - rect.y * zoom,
          zoom,
        })
      } else if (wanted && bySystem.has(wanted)) flyTo(wanted, 0)
      else if (doc.viewport) void flow.setViewport(doc.viewport)
      else void flow.fitView({ padding: 0.06 })
    }, 0)
  }, [placed, bare, frameRect, initialSystem, doc.focus, doc.viewport, bySystem, flyTo, flow])

  // Carrying a system: its head is pressed, and the whole diagram follows the
  // pointer once it has gone a few pixels — less is a click, which the frame
  // still answers. Saved once, when it is put down.
  const carried = useRef(false)
  const carry = useCallback(
    (e: React.PointerEvent<HTMLElement>) => {
      if (e.button !== 0) return
      const grip = (e.target as HTMLElement).closest<HTMLElement>(".map-frame-grip")
      const frameId = grip?.closest<HTMLElement>(".react-flow__node")?.dataset.id
      if (!grip || !frameId) return
      const sid = frameId.slice("frame:".length)
      const p = bySystem.get(sid)
      if (!p) return
      carried.current = false
      const pointer = e.pointerId
      const start = { x: e.clientX, y: e.clientY }
      const from = (carryingRef.current ?? doc.frames)[sid] ?? atlasCorner(p)
      const zoom = store.getState().transform[2]
      grip.setPointerCapture(pointer)
      const move = (ev: PointerEvent) => {
        const dx = ev.clientX - start.x
        const dy = ev.clientY - start.y
        if (!carried.current && Math.hypot(dx, dy) < 4) return
        if (!carried.current) {
          carried.current = true
          canvasRef.current?.setAttribute("data-carrying", "")
        }
        const next = {
          ...(carryingRef.current ?? doc.frames),
          [sid]: { x: Math.round(from.x + dx / zoom), y: Math.round(from.y + dy / zoom) },
        }
        carryingRef.current = next
        setCarrying(next)
      }
      const end = () => {
        grip.removeEventListener("pointermove", move)
        grip.removeEventListener("pointerup", end)
        grip.removeEventListener("pointercancel", end)
        if (grip.hasPointerCapture(pointer)) grip.releasePointerCapture(pointer)
        canvasRef.current?.removeAttribute("data-carrying")
        const put = carryingRef.current?.[sid]
        carryingRef.current = null
        setCarrying(null)
        if (put) update((d) => ({ ...d, frames: { ...d.frames, [sid]: put } }))
      }
      grip.addEventListener("pointermove", move)
      grip.addEventListener("pointerup", end)
      grip.addEventListener("pointercancel", end)
    },
    [bySystem, doc.frames, store, update],
  )

  const nodes = useMemo(() => {
    const out: Node[] = []
    for (const p of placed) {
      const sid = p.system.id
      const s = settled.get(sid)!
      const rect = frameRect(sid)!
      const base = corner(p)
      const origin = { x: base.x + FRAME.pad, y: base.y + FRAME.head + FRAME.pad }
      out.push({
        id: `frame:${sid}`,
        type: "systemFrame",
        position: { x: rect.x, y: rect.y },
        width: rect.width,
        height: rect.height,
        measured: { width: rect.width, height: rect.height },
        draggable: false,
        selectable: false,
        zIndex: 0,
        data: {
          kind: p.system.kind,
          title: systemTitle(p.system),
          anchor: p.system.anchor,
          anchorKind: p.system.anchorKind,
          census: board.census(p.system),
          chosen: focus === sid,
          fixed: bare,
        } satisfies FrameData,
      })
      for (const f of s.frames) {
        out.push({
          id: f.id,
          type: "stackFrame",
          position: { x: origin.x + f.x, y: origin.y + f.y },
          width: f.width,
          height: f.height,
          measured: { width: f.width, height: f.height },
          draggable: false,
          selectable: false,
          zIndex: 0,
          data: {
            frame: f,
            // Stepped back with the rest unless it, or one of its containers, is lit.
            dimmed:
              lit !== null &&
              chosenSeat?.system.id === sid &&
              !lit.has(f.id) &&
              !s.nodes.some((n) => n.frame === f.itemId && lit.has(n.id)),
          } satisfies StackData,
        })
      }
      const sides = new Map<string, { in: boolean; out: boolean }>()
      for (const e of p.system.edges) {
        if (e.shape !== "flow") continue
        sides.set(e.source, { in: sides.get(e.source)?.in ?? false, out: true })
        sides.set(e.target, { in: true, out: sides.get(e.target)?.out ?? false })
      }
      const at = new Map(s.nodes.map((n) => [n.id, n]))
      for (const n of s.nodes) {
        const ports: Port[] = s.nodes
          .filter((sat) => sat.host === n.id)
          .map((sat) => ({
            id: sat.id,
            label: sat.port ?? "",
            left: Math.min(n.width - 14, Math.max(14, sat.x + sat.width / 2 - n.x)),
          }))
        let state: NodeState
        if (chosen === n.id) state = "chosen"
        else if (hovered === n.id) state = "hit"
        else if (lit && chosenSeat?.system.id === sid) state = lit.has(n.id) ? "lit" : "dim"
        out.push({
          id: n.id,
          type: n.face,
          position: { x: origin.x + n.x, y: origin.y + n.y },
          width: n.width,
          height: n.height,
          measured: { width: n.width, height: n.height },
          zIndex: 2,
          data: faceData(at.get(n.id)!, state, ports, sides.get(n.id) ?? { in: false, out: false }),
        })
      }
    }
    return out
  }, [placed, settled, frameRect, corner, board, bare, focus, lit, chosenSeat, chosen, hovered])

  const edges = useMemo(() => {
    const out: Edge[] = []
    for (const p of placed) {
      const sid = p.system.id
      const inSystem = chosenSeat?.system.id === sid
      const byId = new Map(p.system.nodes.map((n) => [n.id, n]))
      for (const e of p.system.edges) {
        const on = inSystem && chosen !== null && (e.source === chosen || e.target === chosen)
        const s = byId.get(e.source)
        const t = byId.get(e.target)
        out.push({
          id: e.id,
          source: e.source,
          target: e.target,
          type: "wire",
          sourceHandle: e.shape === "sub" ? `sub:${e.target}` : e.shape === "down" ? "down" : "out",
          targetHandle: e.shape === "flow" ? "in" : "up",
          zIndex: 1,
          selectable: false,
          data: {
            via: e.via,
            also: e.also,
            shape: e.shape,
            lit: on,
            dimmed: inSystem && lit !== null && !on,
            // A stack's frame stands in for its containers; it carries while they run.
            carries: (s?.item.tone ?? "ok") === "ok" && (t?.item.tone ?? "ok") === "ok",
            live: (e.sessions ?? 0) > 0,
            maybe: [e.via, ...e.also].every((via) => via === "stack" || via === "network"),
            tone: e.tone,
          } satisfies WireData,
        })
      }
    }
    return out
  }, [placed, chosenSeat, chosen, lit])

  const onNodesChange = useCallback(
    (changes: NodeChange[]) => {
      let next: Record<string, Point> | null = null
      for (const c of changes) {
        if (c.type !== "position" || !c.position) continue
        const seat = seats.get(c.id)
        if (!seat) continue
        next ??= { ...(draggingRef.current ?? doc.positions) }
        next[c.id] = { x: c.position.x - seat.origin.x, y: c.position.y - seat.origin.y }
      }
      if (next) {
        draggingRef.current = next
        setDragging(next)
      }
    },
    [seats, doc.positions],
  )
  const onNodeDragStop = useCallback(() => {
    const moved = draggingRef.current
    draggingRef.current = null
    setDragging(null)
    if (moved) update((d) => ({ ...d, positions: moved }))
  }, [update])

  const tidy = useCallback(() => {
    const before = { positions: doc.positions, frames: doc.frames }
    update((d) => ({ ...d, positions: {}, frames: {} }))
    notify.success("Laid out again", {
      description: "Every system and every box is back where the map puts it.",
      action: { label: "Undo", onClick: () => update((d) => ({ ...d, ...before })) },
    })
  }, [doc.positions, doc.frames, update])

  const choose = useCallback(
    (id: string) => {
      const seat = seats.get(id)
      if (!seat) return
      setChosen(id)
      setTapped(null)
      setFocus(seat.system.id)
    },
    [seats],
  )

  const onKey = (e: React.KeyboardEvent) => {
    const target = e.target as HTMLElement
    if (target.closest("input, textarea, [role=menu], [role=dialog]")) return
    if (e.key === "f") {
      if (focus) flyTo(focus)
      else void flow.fitView({ padding: 0.06, duration: 300 })
    } else if (e.key === "i") {
      setInspectorOpen((open) => !open)
    } else if (e.key === "Enter" && chosenNode) {
      router.push(chosenNode.item.href)
    } else if (e.key === "Escape") {
      setChosen(null)
    }
  }

  const tappedSystem = tapped ? bySystem.get(tapped)?.system : undefined
  // What a phone's one button opens: nothing draws "Everything else" a page of its own.
  const wayOn = chosenNode
    ? { name: chosenNode.item.name, href: chosenNode.item.href }
    : tappedSystem && tappedSystem.kind !== "loose"
      ? { name: systemTitle(tappedSystem), href: tappedSystem.anchor.href }
      : null

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col" onKeyDown={onKey}>
      <div
        className={cn(
          "grid min-h-0 flex-1",
          bare
            ? "grid-cols-1"
            : inspecting
              ? "grid-cols-[15rem_minmax(0,1fr)_20rem]"
              : "grid-cols-[15rem_minmax(0,1fr)]",
        )}
      >
        {!bare && <SystemRail board={board} systems={systems} focus={focus} onFocus={focusOn} />}
        <div
          ref={canvasRef}
          data-slot="server-map"
          onPointerDown={bare ? undefined : carry}
          className="group/map relative min-h-0 min-w-0"
        >
          {systems.length === 0 ? (
            empty
          ) : (
            <ReactFlow
              nodes={nodes}
              edges={edges}
              onNodesChange={onNodesChange}
              nodeTypes={nodeTypes}
              edgeTypes={edgeTypes}
              onNodeClick={(_, n) => {
                if (n.type === "systemFrame") {
                  // A phone only looks: where it last was stays the desk's to remember.
                  if (bare) {
                    const sid = n.id.slice("frame:".length)
                    setChosen(null)
                    setFocus(sid)
                    setTapped(sid)
                  }
                  // Put down after a carry: not a click.
                  else if (carried.current) carried.current = false
                  else focusOn(n.id.slice("frame:".length), false)
                } else if (n.type !== "stackFrame") choose(n.id)
              }}
              onNodeDoubleClick={(_, n) => {
                if (n.type === "systemFrame") {
                  const s = bySystem.get(n.id.slice("frame:".length))?.system
                  if (s && s.kind !== "loose") router.push(s.anchor.href)
                  return
                }
                const href = (n.data as Partial<FaceData>).node?.item.href
                if (href) router.push(href)
              }}
              onNodeDragStop={onNodeDragStop}
              onPaneClick={() => {
                setChosen(null)
                setTapped(null)
              }}
              onMoveEnd={(event, viewport: Viewport) => {
                if (event && restored.current && !bare) update({ viewport })
              }}
              minZoom={ZOOM.min}
              maxZoom={ZOOM.max}
              {...CANVAS_MOVE}
              proOptions={{ hideAttribution: true }}
              nodesConnectable={false}
              nodesDraggable={!bare}
              elementsSelectable
              onlyRenderVisibleElements
              deleteKeyCode={null}
              selectionKeyCode={null}
              multiSelectionKeyCode={null}
              aria-label={board.label}
            />
          )}
          {systems.length > 0 && !bare && (
            <ZoomButtons
              onFit={() =>
                focus ? flyTo(focus) : void flow.fitView({ padding: 0.06, duration: 300 })
              }
              onAll={showAll}
              onTidy={isArranged(doc) ? tidy : undefined}
            />
          )}
          {!bare && (
            <div className={cn(FLOAT, "top-3 right-3")}>
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
            </div>
          )}
          {bare && wayOn && (
            <div className="pointer-events-none absolute inset-x-0 bottom-[max(1rem,env(safe-area-inset-bottom))] flex justify-center px-4">
              <Button asChild className="pointer-events-auto max-w-full min-w-0">
                <Link href={wayOn.href}>
                  <span className="truncate">Open {wayOn.name}</span>
                  <ArrowUpRight />
                </Link>
              </Button>
            </div>
          )}
          {unread.length > 0 && (
            <div className="pointer-events-none absolute top-3 left-3 max-w-[min(28rem,80%)] space-y-1">
              {unread.map((k) => (
                <p
                  key={k.key}
                  className="pointer-events-auto flex items-start gap-2 rounded-md border border-hairline bg-popover px-2.5 py-1.5 text-hint text-muted-foreground shadow-sm"
                >
                  <Warning className="mt-0.5 size-3.5 shrink-0 text-warning" />
                  <span>
                    <span className="text-foreground">{k.label}</span> not read
                    {k.reason ? `: ${k.reason}` : ""}
                  </span>
                </p>
              ))}
            </div>
          )}
        </div>

        {inspecting && (
          <MapInspector
            board={board}
            systems={systems}
            system={chosenSeat?.system ?? focusSystem}
            node={chosenNode}
            onChoose={(id) => {
              choose(id)
              frameNode(id)
            }}
            onHover={setHovered}
            onFocus={focusOn}
            onFrame={frameNode}
          />
        )}
      </div>
    </div>
  )
}

function ZoomButtons({
  onFit,
  onAll,
  onTidy,
}: {
  onFit: () => void
  onAll: () => void
  /** Only while something has been moved: with nothing to put back it would be noise. */
  onTidy?: () => void
}) {
  const flow = useReactFlow()
  const percent = useStore((s) => Math.round(s.transform[2] * 100))
  return (
    <div className={cn(FLOAT, "bottom-3 left-3")}>
      <IconAction
        label="Zoom out"
        className="size-7"
        onClick={() => void flow.zoomOut({ duration: 150 })}
      >
        <Minus />
      </IconAction>
      <Button
        size="xs"
        variant="ghost"
        title="Back to the real size (Ctrl 0)"
        className="numeric h-7 min-w-12 px-1.5 text-hint"
        onClick={() => void flow.zoomTo(1, { duration: 150 })}
      >
        {percent}%
      </Button>
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
      <Button size="xs" variant="ghost" className="h-7 px-2 text-hint" onClick={onAll}>
        All
      </Button>
      {onTidy && (
        <IconAction
          label="Tidy up: put every system and every box back where the map puts it"
          className="size-7"
          onClick={onTidy}
        >
          <RotateCounterClockwise />
        </IconAction>
      )}
    </div>
  )
}

/** A server with nothing the map draws yet: the way to put the first thing on it. */
function EmptyMap() {
  return (
    <div className="flex h-full items-center justify-center p-8">
      <div className="max-w-sm text-center">
        <p className="text-title font-semibold">Nothing to draw yet</p>
        <p className="mt-1.5 text-body text-muted-foreground">
          A deployment, a Compose stack or a container shows up here as its own diagram, wired to
          its domains, databases, volumes and backups.
        </p>
        <Button size="sm" asChild className="mt-4">
          <Link href="/deploy/new">Deploy a project</Link>
        </Button>
      </div>
    </div>
  )
}
