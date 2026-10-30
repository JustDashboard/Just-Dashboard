"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import {
  MiniMap,
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
  Cross,
  Fullscreen,
  FullscreenClose,
  Minus,
  Plus,
  RefreshClockwise,
  RotateCounterClockwise,
  SettingsSliders,
  SidebarRightClose,
  SidebarRightOpen,
  Trash,
  Warning,
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
import { Saved } from "@/components/database/diagram/chrome"
import { plural, relativeTime } from "@/lib/format"
import { isArranged, kindOf, type MapDocument, type Point } from "@/lib/server-map"
import {
  FRAME,
  SYSTEM_KINDS,
  detectSystems,
  layoutAtlas,
  neighbours,
  searchNodes,
  searchSystems,
  settle,
  systemTitle,
  type Placed,
  type Settled,
  type System,
  type SystemNode,
} from "@/lib/server-systems"
import { notify } from "@/lib/toast"
import type { ServerMap } from "@/lib/types"
import { cn } from "@/lib/utils"
import { edgeTypes, type WireData } from "@/components/map/edges"
import { MapInspector } from "@/components/map/inspector"
import type { MemoryStatus } from "@/components/map/memory"
import {
  nodeTypes,
  type FaceData,
  type FrameData,
  type NodeState,
  type Port,
  type StackData,
} from "@/components/map/nodes"
import { SystemRail, censusLine } from "@/components/map/rail"

const ZOOM = { min: 0.06, max: 2 }
/** A system flown to fills the canvas, but a small one is not blown up past its own size. */
const FLY = { padding: 0.04, maxZoom: 1 }

/**
 * How the canvas is moved, a whiteboard's (Excalidraw's): two fingers on a
 * trackpad slide it, a pinch zooms it about the fingers; a mouse drags it by
 * any open ground, or by its middle button anywhere, and its wheel slides it
 * too — Ctrl or ⌘ with the wheel zooms, Shift with it slides sideways. Space
 * held makes any drag a slide. Nothing on it changes size but by the zoom.
 */
const MOVE = {
  panOnScroll: true,
  panOnScrollSpeed: 1,
  zoomOnScroll: false,
  zoomOnPinch: true,
  zoomOnDoubleClick: false,
  panOnDrag: [0, 1],
}

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

/**
 * The map's canvas: every system's diagram in its own frame, side by side,
 * with the rail of systems down its left and the inspector down its right.
 *
 * Choosing a box lights what it is wired to inside its system, draws those
 * lines in the brand's blue with how each is known written on them, and steps
 * the rest of the system back. A box can be dragged where the reader wants
 * it; the place is kept against the account, inside its system, so a
 * reading that adds a container lays that one out and moves nothing else.
 */
export function MapCanvas({
  map,
  doc,
  status,
  updatedAt,
  update,
  reset,
  refresh,
  full,
  onFullscreen,
  initialSystem,
}: {
  map: ServerMap
  doc: MapDocument
  status: MemoryStatus
  updatedAt?: string
  update: (next: Partial<MapDocument> | ((doc: MapDocument) => MapDocument)) => void
  reset: () => Promise<void>
  refresh: () => void
  full: boolean
  onFullscreen: () => void
  /** A system asked for by the address (`?system=`), flown to on arrival. */
  initialSystem?: string
}) {
  const flow = useReactFlow()
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  const canvasRef = useRef<HTMLDivElement>(null)
  const searchRef = useRef<HTMLInputElement>(null)
  const store = useStoreApi()

  const systems = useMemo(() => detectSystems(map), [map])
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
  const [query, setQuery] = useState("")
  const [inspectorOpen, setInspectorOpen] = useState(true)
  const inspecting = inspectorOpen

  const chosenSeat = chosen ? seats.get(chosen) : undefined
  const chosenNode = chosenSeat?.system.nodes.find((n) => n.id === chosen)
  const focusSystem = focus ? bySystem.get(focus)?.system : undefined
  const lit = useMemo(
    () => (chosenSeat && chosen ? neighbours(chosenSeat.system, chosen) : null),
    [chosenSeat, chosen],
  )
  const kept = useMemo(() => searchSystems(systems, query), [systems, query])
  const hits = useMemo(() => {
    if (!query.trim()) return null
    const out = new Set<string>()
    for (const s of systems) for (const id of searchNodes(s, query)) out.add(id)
    return out
  }, [systems, query])

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
      const mod = (e.ctrlKey || e.metaKey) && !e.altKey
      if (mod && (e.key === "=" || e.key === "+")) {
        e.preventDefault()
        void flow.zoomIn({ duration: 150 })
      } else if (mod && e.key === "-") {
        e.preventDefault()
        void flow.zoomOut({ duration: 150 })
      } else if (mod && e.key === "0") {
        e.preventDefault()
        void flow.zoomTo(1, { duration: 150 })
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

  // Safari sends a trackpad's pinch as gesture events rather than as the
  // Ctrl-wheel every other browser sends, and zooms the page with it unless
  // told not to. Its gestures carry a scale and no point, so the canvas zooms
  // about where the pointer last was, and any Ctrl-wheel it also sends while
  // one runs is let go so a pinch zooms once.
  useEffect(() => {
    const el = canvasRef.current
    if (!el) return
    let base: number | null = null
    let at = { x: 0, y: 0 }
    const track = (e: PointerEvent) => {
      at = { x: e.clientX, y: e.clientY }
    }
    const start = (e: Event) => {
      e.preventDefault()
      base = store.getState().transform[2]
    }
    const change = (e: Event) => {
      e.preventDefault()
      if (base === null) return
      const [x, y, k] = store.getState().transform
      const next = clampZoom(base * (e as Event & { scale: number }).scale)
      const rect = el.getBoundingClientRect()
      const px = at.x - rect.left
      const py = at.y - rect.top
      void flow.setViewport({
        x: px - ((px - x) * next) / k,
        y: py - ((py - y) * next) / k,
        zoom: next,
      })
    }
    const end = (e: Event) => {
      e.preventDefault()
      base = null
    }
    const once = (e: WheelEvent) => {
      if (base !== null && e.ctrlKey) {
        e.preventDefault()
        e.stopPropagation()
      }
    }
    el.addEventListener("pointermove", track)
    el.addEventListener("gesturestart", start)
    el.addEventListener("gesturechange", change)
    el.addEventListener("gestureend", end)
    el.addEventListener("wheel", once, { capture: true, passive: false })
    return () => {
      el.removeEventListener("pointermove", track)
      el.removeEventListener("gesturestart", start)
      el.removeEventListener("gesturechange", change)
      el.removeEventListener("gestureend", end)
      el.removeEventListener("wheel", once, { capture: true })
    }
  }, [flow, store])

  // The first view: the system the address or the last visit named, else
  // where the reader left the canvas, else every system at once.
  const restored = useRef(false)
  useEffect(() => {
    if (restored.current || placed.length === 0) return
    restored.current = true
    window.setTimeout(() => {
      const wanted = initialSystem ?? doc.focus
      if (wanted && bySystem.has(wanted)) flyTo(wanted, 0)
      else if (doc.viewport) void flow.setViewport(doc.viewport)
      else void flow.fitView({ padding: 0.06 })
    }, 0)
  }, [placed.length, initialSystem, doc.focus, doc.viewport, bySystem, flyTo, flow])

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
      const dimmed = kept !== null && !kept.has(sid)
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
          census: censusLine(p.system),
          chosen: focus === sid,
          dimmed,
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
        else if (hovered === n.id || hits?.has(n.id)) state = "hit"
        else if (lit && chosenSeat?.system.id === sid) state = lit.has(n.id) ? "lit" : "dim"
        else if (dimmed) state = "dim"
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
  }, [placed, settled, frameRect, corner, kept, focus, lit, chosenSeat, chosen, hovered, hits])

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
            dimmed: (inSystem && lit !== null && !on) || (kept !== null && !kept.has(sid)),
            // A stack's frame stands in for its containers; it carries while they run.
            carries: (s?.item.tone ?? "ok") === "ok" && (t?.item.tone ?? "ok") === "ok",
          } satisfies WireData,
        })
      }
    }
    return out
  }, [placed, chosenSeat, chosen, lit, kept])

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

  const forget = useCallback(() => {
    confirm({
      title: "Forget this layout",
      confirmLabel: "Forget layout",
      description: (
        <p>
          Where you put each box, and the system you were last reading, are thrown away. Your map is
          laid out fresh; nobody else&rsquo;s changes, and nothing on the server does.
        </p>
      ),
      action: async () => {
        await reset()
        setChosen(null)
        setFocus(null)
        window.setTimeout(() => void flow.fitView({ padding: 0.06, duration: 300 }), 0)
      },
    })
  }, [confirm, reset, flow])

  const choose = useCallback(
    (id: string) => {
      const seat = seats.get(id)
      if (!seat) return
      setChosen(id)
      setFocus(seat.system.id)
    },
    [seats],
  )

  const onKey = (e: React.KeyboardEvent) => {
    const target = e.target as HTMLElement
    if (target.closest("input, textarea, [role=menu], [role=dialog]")) return
    if (e.key === "/") {
      e.preventDefault()
      searchRef.current?.focus()
    } else if (e.key === "f") {
      if (focus) flyTo(focus)
      else void flow.fitView({ padding: 0.06, duration: 300 })
    } else if (e.key === "i") {
      setInspectorOpen((open) => !open)
    } else if (e.key === "Enter" && chosenNode) {
      router.push(chosenNode.item.href)
    } else if (e.key === "Escape") {
      if (chosen) setChosen(null)
      else if (query) setQuery("")
    }
  }

  const things = systems.reduce((n, s) => n + s.nodes.filter((x) => x.kind !== "logs").length, 0)
  const unread = map.kinds.filter((k) => SYSTEM_KINDS.has(k.key) && k.state === "unread")

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col" onKeyDown={onKey}>
      <PaneHeader className="min-h-11 flex-wrap gap-x-1.5 gap-y-1">
        <SearchInput
          ref={searchRef}
          dense
          value={query}
          onChange={(e) => {
            setQuery(e.target.value)
            setChosen(null)
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter" && hits && hits.size > 0) {
              const first = hits.values().next().value as string
              choose(first)
              frameNode(first)
            }
            if (e.key === "Escape") {
              setQuery("")
              ;(e.target as HTMLInputElement).blur()
            }
          }}
          placeholder="Find a system or anything in one"
          aria-label="Find a system or anything in one"
          containerClassName="w-60 sm:w-72"
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
            {hits.size === 0 ? "no match" : plural(hits.size, "match", "matches")}
          </span>
        )}
        <span className="ml-1 hidden items-center gap-1.5 text-hint text-muted-foreground md:flex">
          <span className="numeric text-foreground">{systems.length}</span>
          {systems.length === 1 ? "system" : "systems"}
          <span aria-hidden>·</span>
          <span className="numeric text-foreground">{things.toLocaleString("en-US")}</span>
          things
          <span aria-hidden>·</span>
          <span title={map.checkedAt}>read {relativeTime(map.checkedAt)}</span>
        </span>

        <span className="flex-1" />

        <Saved status={status} updatedAt={updatedAt} arranged={isArranged(doc)} />

        <IconAction
          label="Tidy up: put every system and every box back where the map puts it"
          className="size-7"
          onClick={tidy}
          disabled={!isArranged(doc)}
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
              checked={doc.minimap}
              onCheckedChange={(v) => update({ minimap: v })}
            >
              Minimap
            </DropdownMenuCheckboxItem>
            <DropdownMenuItem onSelect={refresh}>
              <RefreshClockwise />
              Read the server again
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem
              variant="destructive"
              disabled={!isArranged(doc) && !doc.focus}
              onSelect={forget}
            >
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
            ? "grid-cols-1 max-lg:grid-rows-[minmax(0,1fr)_minmax(0,45%)] lg:grid-cols-[15rem_minmax(0,1fr)_20rem]"
            : "grid-cols-1 lg:grid-cols-[15rem_minmax(0,1fr)]",
        )}
      >
        <SystemRail systems={systems} focus={focus} kept={kept} onFocus={focusOn} onAll={showAll} />
        <div
          ref={canvasRef}
          data-slot="server-map"
          onPointerDown={carry}
          className="group/map relative min-h-0 min-w-0"
        >
          {systems.length === 0 ? (
            <EmptyMap />
          ) : (
            <ReactFlow
              nodes={nodes}
              edges={edges}
              onNodesChange={onNodesChange}
              nodeTypes={nodeTypes}
              edgeTypes={edgeTypes}
              onNodeClick={(_, n) => {
                if (n.type === "systemFrame") {
                  // Put down after a carry: not a click.
                  if (carried.current) carried.current = false
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
              onPaneClick={() => setChosen(null)}
              onMoveEnd={(event, viewport: Viewport) => {
                if (event && restored.current) update({ viewport })
              }}
              minZoom={ZOOM.min}
              maxZoom={ZOOM.max}
              {...MOVE}
              proOptions={{ hideAttribution: true }}
              nodesConnectable={false}
              elementsSelectable
              onlyRenderVisibleElements
              deleteKeyCode={null}
              selectionKeyCode={null}
              multiSelectionKeyCode={null}
              aria-label="Map of the server's systems"
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
          )}
          {systems.length > 0 && (
            <ZoomButtons
              onFit={() =>
                focus ? flyTo(focus) : void flow.fitView({ padding: 0.06, duration: 300 })
              }
              onAll={showAll}
            />
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
                    <span className="text-foreground">{kindOf(k.key)?.label ?? k.key}s</span> not
                    read{k.reason ? `: ${k.reason}` : ""}
                  </span>
                </p>
              ))}
            </div>
          )}
        </div>

        {inspecting && (
          <MapInspector
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
            onClose={() => setInspectorOpen(false)}
          />
        )}
      </div>
      {dialog}
    </div>
  )
}

function ZoomButtons({ onFit, onAll }: { onFit: () => void; onAll: () => void }) {
  const flow = useReactFlow()
  const percent = useStore((s) => Math.round(s.transform[2] * 100))
  return (
    <div className="absolute bottom-3 left-3 flex items-center gap-0.5 rounded-md border border-hairline bg-popover p-0.5">
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

function minimapColor(node: Node) {
  if (node.type === "systemFrame") return "color-mix(in oklab, var(--color-muted) 55%, transparent)"
  if (node.type === "stackFrame") return "transparent"
  const tone = (node.data as Partial<FaceData>).node?.item.tone
  if (tone === "danger") return "var(--color-destructive)"
  if (tone === "warning") return "var(--color-warning)"
  if (node.type === "hero") return "var(--color-chart-1)"
  return "color-mix(in oklab, var(--color-muted-foreground) 55%, var(--color-muted))"
}
