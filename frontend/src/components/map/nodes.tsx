"use client"

import { createContext, memo, useContext } from "react"
import { Handle, Position, type Node, type NodeProps } from "@xyflow/react"
import { ArrowUpRight, ChevronDown, ChevronRight, Plus } from "@/components/icons"
import { WireHost, WirePlaceholder } from "@/components/deploy/wire"
import { Meter, utilisationTone } from "@/components/meter"
import { ProductGlyph, platformProduct } from "@/components/product-logo"
import { StatusDot } from "@/components/status-dot"
import { useMetrics } from "@/hooks/use-metrics"
import { duration, percent, plural } from "@/lib/format"
import type { Branch, KindDef } from "@/lib/server-map"
import type { MapItem, MapKindReading } from "@/lib/types"
import { cn } from "@/lib/utils"
import { BRANCH_GLYPH, ItemMark, KIND_GLYPH, TONE_DOT } from "@/components/map/marks"

/**
 * What a box can ask of the map: fold a branch or a shelf, open every thing
 * on a shelf, add to it, open a thing's page. Held in a context rather than
 * in each node's data so a node's data stays the same object between
 * renders — React Flow re-renders a node whose data changed, and a callback
 * made fresh on every render would re-render all three hundred of them.
 */
export type MapActions = {
  toggleFold: (id: string) => void
  expand: (kind: string) => void
  add: (kind: KindDef) => void
  open: (href: string) => void
  can: (kind: KindDef) => boolean
}

export const MapActionsContext = createContext<MapActions | null>(null)

function useActions() {
  const actions = useContext(MapActionsContext)
  if (!actions) throw new Error("A map node must be drawn inside the map")
  return actions
}

/**
 * Where lines meet a box: both sides, both ways, and never seen — the map is
 * read, not wired by hand (`nodesConnectable` is off), so a handle is only an
 * anchor. A tree line leaves the side facing away from the server and
 * arrives on the side facing it.
 */
function Ports() {
  const hidden =
    "!pointer-events-none !size-px !min-h-0 !min-w-0 !border-0 !bg-transparent !opacity-0"
  return (
    <>
      <Handle type="target" id="tl" position={Position.Left} className={hidden} />
      <Handle type="target" id="tr" position={Position.Right} className={hidden} />
      <Handle type="source" id="sl" position={Position.Left} className={hidden} />
      <Handle type="source" id="sr" position={Position.Right} className={hidden} />
    </>
  )
}

/*
 * The three distances the map is read from (`zoomBucket` in canvas.tsx sets
 * `data-zoom` on the canvas, and these read it, so zooming re-renders no box):
 *
 *   near — every word;
 *   mid  — names, without the line under them;
 *   far  — the overview: a thing is its mark and its dot, and the names of
 *          the server, the branches and the shelves are held at a size that
 *          reads on the screen, scaled against the zoom (`--map-unzoom`, kept
 *          on the canvas by the canvas itself), so the whole server is a map
 *          you can find your way round rather than three hundred specks.
 */
const SECOND_LINE = "group-data-[zoom=mid]/map:hidden group-data-[zoom=far]/map:hidden"
const NOT_FAR = "group-data-[zoom=far]/map:hidden"
const ONLY_FAR = "hidden group-data-[zoom=far]/map:flex"
/** A name held to the screen at the far zoom, growing from its left edge. */
const HELD =
  "origin-left group-data-[zoom=far]/map:[transform:scale(max(1,calc(var(--map-unzoom,1)*0.8)))]"

export type ServerNodeData = { things: number; down: number; hostname?: string }

/**
 * The heart of the map: this server, drawn as every picture draws it (the
 * rack on the brand's tint), named, with what it is running on and how hard
 * it is working now. The readings are the shell's own metrics stream, which
 * is open on every page already, so the server box costs no request; it is
 * the one box that re-renders on a frame, and nothing else does.
 */
export const ServerNode = memo(function ServerNode({ data }: NodeProps<Node<ServerNodeData>>) {
  const { host, snapshot, connection } = useMetrics()
  const platform = platformProduct(host?.platform)
  const cpu = snapshot?.cpu.totalPercent
  const memory = snapshot?.memory.usedPercent
  return (
    <div className="flex size-full flex-col justify-between rounded-xl border border-rule-brand bg-card p-4">
      <Ports />
      <div className="flex min-w-0 items-center gap-3">
        <WireHost size="md" />
        <div className={cn("min-w-0", HELD)}>
          <div className="truncate text-title font-semibold">
            {host?.hostname ?? data.hostname ?? "This server"}
          </div>
          <div
            className={cn(
              "flex min-w-0 items-center gap-1.5 text-hint text-muted-foreground",
              SECOND_LINE,
            )}
          >
            {platform && <ProductGlyph id={platform} className="size-3" />}
            <span className="truncate">
              {host
                ? [
                    `${host.platform} ${host.platformVersion}`.trim(),
                    snapshot ? `up ${duration(snapshot.uptimeSeconds)}` : "",
                  ]
                    .filter(Boolean)
                    .join(" · ")
                : "Reading the host…"}
            </span>
          </div>
        </div>
      </div>
      <div className={cn("grid grid-cols-2 gap-4", NOT_FAR)}>
        <Reading label="CPU" value={cpu} live={connection === "open"} />
        <Reading label="Memory" value={memory} live={connection === "open"} />
      </div>
      <div className={cn("flex items-center gap-1.5 text-hint text-muted-foreground", SECOND_LINE)}>
        <span className="numeric text-foreground">{data.things.toLocaleString("en-US")}</span>
        {data.things === 1 ? "thing" : "things"} on the map
        {data.down > 0 && (
          <>
            <span aria-hidden>·</span>
            <StatusDot tone="danger" />
            <span className="numeric text-destructive">{data.down} down</span>
          </>
        )}
      </div>
    </div>
  )
})

function Reading({ label, value, live }: { label: string; value?: number; live: boolean }) {
  return (
    <div className="min-w-0 space-y-1">
      <div className="flex items-baseline justify-between gap-2 text-hint">
        <span className="flex items-center gap-1.5 text-muted-foreground">
          {live && <StatusDot tone="running" live />}
          {label}
        </span>
        <span className="numeric text-foreground">
          {value === undefined ? "—" : percent(value, 0)}
        </span>
      </div>
      <Meter value={value ?? 0} tone={utilisationTone(value ?? 0)} size="thin" />
    </div>
  )
}

export type BranchNodeData = {
  branch: Branch
  folded: boolean
  things: number
  down: number
  shelves: number
  dimmed?: boolean
}

/**
 * A branch: one of the seven things a server is for, with how much of it
 * there is. Its chevron folds every shelf it holds into this one head.
 */
export const BranchNode = memo(function BranchNode({ data }: NodeProps<Node<BranchNodeData>>) {
  const { toggleFold } = useActions()
  const Glyph = BRANCH_GLYPH[data.branch.key]
  const id = `branch:${data.branch.key}`
  return (
    <div
      className={cn(
        "flex size-full items-center gap-3 rounded-xl border border-border bg-card px-3 transition-opacity",
        data.dimmed && "opacity-40",
      )}
    >
      <Ports />
      <span
        className={cn(
          "flex size-10 shrink-0 items-center justify-center rounded-lg border border-hairline bg-control text-foreground [&>svg]:size-5",
          NOT_FAR,
        )}
      >
        <Glyph aria-hidden />
      </span>
      <div className={cn("min-w-0 flex-1", HELD)}>
        <div className="truncate text-title font-semibold">{data.branch.label}</div>
        <div
          className={cn("flex items-center gap-1.5 text-hint text-muted-foreground", SECOND_LINE)}
        >
          <span className="numeric">{data.things.toLocaleString("en-US")}</span>
          {data.down > 0 && (
            <>
              <span aria-hidden>·</span>
              <StatusDot tone="danger" />
              <span className="numeric text-destructive">{data.down} down</span>
            </>
          )}
          {data.folded && (
            <>
              <span aria-hidden>·</span>
              {plural(data.shelves, "shelf", "shelves")} folded
            </>
          )}
        </div>
      </div>
      <button
        type="button"
        aria-label={data.folded ? `Unfold ${data.branch.label}` : `Fold ${data.branch.label}`}
        aria-expanded={!data.folded}
        className={cn(
          "nodrag flex size-7 shrink-0 items-center justify-center rounded-md text-muted-foreground focus-ring transition-colors hover:bg-accent hover:text-foreground",
          NOT_FAR,
        )}
        onClick={(event) => {
          event.stopPropagation()
          toggleFold(id)
        }}
      >
        {data.folded ? <ChevronRight className="size-4" /> : <ChevronDown className="size-4" />}
      </button>
    </div>
  )
})

export type ShelfNodeData = {
  kind: KindDef
  reading?: MapKindReading
  folded: boolean
  expanded: boolean
  /** Drawn: on the shelf now, so "Show fewer" knows there is something to fold. */
  drawn: number
  dimmed?: boolean
}

const HEAD_BUTTON =
  "nodrag flex size-7 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus-ring"

/**
 * A shelf: one kind of thing — the containers, the domains — with its count,
 * the way to the page that lists them all and, where this account may, the
 * way to add one. The things themselves are the shelf's children, laid in
 * its grid, so dragging the shelf by its head carries them.
 */
export const ShelfNode = memo(function ShelfNode({ data }: NodeProps<Node<ShelfNodeData>>) {
  const actions = useActions()
  const { kind, reading } = data
  const Glyph = KIND_GLYPH[kind.key]
  const id = `shelf:${kind.key}`
  const total = reading ? Math.max(reading.total, reading.items.length) : 0
  const down = reading?.items.filter((item) => item.tone === "danger").length ?? 0
  const addable = kind.add && actions.can(kind)
  return (
    <div
      className={cn(
        "size-full rounded-xl border border-hairline bg-background transition-opacity",
        data.dimmed && "opacity-40",
      )}
    >
      <Ports />
      <div className="flex h-12 items-center gap-2 pr-2 pl-3">
        <button
          type="button"
          aria-label={data.folded ? `Unfold ${kind.label}` : `Fold ${kind.label}`}
          aria-expanded={!data.folded}
          className={cn(HEAD_BUTTON, "-ml-1 size-6", NOT_FAR)}
          onClick={(event) => {
            event.stopPropagation()
            actions.toggleFold(id)
          }}
        >
          {data.folded ? (
            <ChevronRight className="size-3.5" />
          ) : (
            <ChevronDown className="size-3.5" />
          )}
        </button>
        <span className={cn("flex min-w-0 items-center gap-2", HELD)}>
          {Glyph && <Glyph aria-hidden className="size-4 shrink-0 text-muted-foreground" />}
          <span className="eyebrow truncate">{kind.label}</span>
          {!kind.tool && reading?.state !== "unread" && (
            <span className="numeric text-hint text-muted-foreground">
              {total.toLocaleString("en-US")}
            </span>
          )}
          {down > 0 && (
            <span className="flex items-center gap-1 text-hint text-destructive">
              <StatusDot tone="danger" />
              <span className="numeric">{down} down</span>
            </span>
          )}
        </span>
        <ShelfNote reading={reading} />
        <span className="flex-1" />
        {data.expanded && data.drawn > 0 && (
          <button
            type="button"
            className={cn(
              "nodrag rounded-md px-1.5 py-0.5 text-hint text-muted-foreground focus-ring transition-colors hover:bg-accent hover:text-foreground",
              SECOND_LINE,
            )}
            onClick={(event) => {
              event.stopPropagation()
              actions.expand(kind.key)
            }}
          >
            Show fewer
          </button>
        )}
        {addable && kind.add && (
          <button
            type="button"
            aria-label={kind.add.label}
            title={kind.add.label}
            className={cn(HEAD_BUTTON, NOT_FAR)}
            onClick={(event) => {
              event.stopPropagation()
              actions.add(kind)
            }}
          >
            <Plus className="size-4" />
          </button>
        )}
        <button
          type="button"
          aria-label={`Open ${kind.label}`}
          title={`Open ${kind.label}`}
          className={cn(HEAD_BUTTON, NOT_FAR)}
          onClick={(event) => {
            event.stopPropagation()
            actions.open(kind.href)
          }}
        >
          <ArrowUpRight className="size-4" />
        </button>
      </div>
    </div>
  )
})

/**
 * What the shelf's head says about how its count was read: a source that
 * failed is said in the warning's colour and never as an empty shelf, and a
 * role that is shown part of a kind is told it is a part (security
 * invariants: an unavailable reading carries its reason).
 */
function ShelfNote({ reading }: { reading?: MapKindReading }) {
  if (!reading || (reading.state !== "unread" && reading.state !== "limited")) return null
  return (
    <span
      title={reading.reason}
      className={cn(
        "min-w-0 truncate text-hint",
        reading.state === "unread" ? "text-warning" : "text-muted-foreground",
        SECOND_LINE,
      )}
    >
      {reading.state === "unread"
        ? reading.reason === "Still being read"
          ? "reading…"
          : "not read"
        : "yours only"}
    </span>
  )
}

export type ItemNodeData = {
  item: MapItem
  kind: string
  selected?: boolean
  dimmed?: boolean
  /** Wired to the chosen box: drawn lit while the dimmed ones step back. */
  related?: boolean
}

/**
 * One thing: its mark, its name, and its state in a word with the dot that
 * colours it. The state is what was measured — a container's state and
 * health, a backup's last run, a certificate's days — never a verdict the
 * map reached on its own.
 */
export const ItemNode = memo(function ItemNode({ data }: NodeProps<Node<ItemNodeData>>) {
  const { item } = data
  return (
    <div
      data-selected={data.selected || undefined}
      data-tone={item.tone}
      className={cn(
        "flex size-full items-center gap-2.5 rounded-lg border bg-card px-2.5 transition-[opacity,border-color,background-color]",
        data.selected
          ? "border-border-strong bg-accent"
          : data.related
            ? "border-rule-brand"
            : "border-hairline hover:border-border-strong",
        data.dimmed && "opacity-35",
      )}
    >
      <Ports />
      <ItemMark item={item} kind={data.kind} />
      <div className={cn("min-w-0 flex-1", NOT_FAR)}>
        <div className="truncate text-body leading-tight font-medium">{item.name}</div>
        <div
          className={cn(
            "mt-0.5 flex min-w-0 items-center gap-1.5 text-hint text-muted-foreground",
            SECOND_LINE,
          )}
        >
          <StatusDot tone={TONE_DOT[item.tone]} />
          <span
            className={cn(
              "shrink-0",
              item.tone === "danger" && "text-destructive",
              item.tone === "warning" && "text-warning",
            )}
          >
            {item.state}
          </span>
          {item.detail && (
            <>
              <span aria-hidden>·</span>
              <span className="truncate">{item.detail}</span>
            </>
          )}
        </div>
      </div>
      {/* From afar the state is the dot alone, drawn where the words were. */}
      <span className={cn("flex-1 items-center justify-end", ONLY_FAR)}>
        <StatusDot tone={TONE_DOT[item.tone]} className="size-3" />
      </span>
    </div>
  )
})

export type SlotNodeData = { kind: KindDef; count?: number; dimmed?: boolean }

/** "+N more": the rest of a long shelf, a press away. */
export const MoreNode = memo(function MoreNode({ data }: NodeProps<Node<SlotNodeData>>) {
  const { expand } = useActions()
  return (
    <button
      type="button"
      className={cn(
        "nodrag flex size-full items-center justify-center gap-1.5 rounded-lg border border-dashed border-border text-body text-muted-foreground focus-ring transition-colors hover:bg-row-hover hover:text-foreground",
        data.dimmed && "opacity-35",
      )}
      onClick={(event) => {
        event.stopPropagation()
        expand(data.kind.key)
      }}
    >
      <Ports />
      <span className={cn("flex items-center gap-1.5", HELD, "origin-center")}>
        <span className="numeric">+{(data.count ?? 0).toLocaleString("en-US")}</span>
        <span className={NOT_FAR}>more</span>
      </span>
    </button>
  )
})

/**
 * An empty shelf the account can fill: the dashed ring of a thing not added
 * yet, the only circle a picture keeps (`WirePlaceholder`), with the verb.
 * An empty shelf it cannot fill says so in words instead.
 */
export const AddNode = memo(function AddNode({ data }: NodeProps<Node<SlotNodeData>>) {
  const actions = useActions()
  const { kind } = data
  const Glyph = KIND_GLYPH[kind.key]
  if (!kind.add || !actions.can(kind)) {
    return (
      <div className="flex size-full items-center rounded-lg px-3 text-hint text-muted-foreground">
        <Ports />
        <span className={NOT_FAR}>No {kind.label.toLowerCase()} yet</span>
      </div>
    )
  }
  return (
    <button
      type="button"
      className="nodrag group/add flex size-full items-center gap-2.5 rounded-lg border border-dashed border-border px-2.5 text-left focus-ring transition-colors hover:bg-row-hover"
      onClick={(event) => {
        event.stopPropagation()
        actions.add(kind)
      }}
    >
      <Ports />
      <WirePlaceholder size="sm" fallback={Glyph} />
      <span className={cn("min-w-0", NOT_FAR)}>
        <span className="block truncate text-body font-medium">{kind.add.label}</span>
        <span className={cn("block truncate text-hint text-muted-foreground", SECOND_LINE)}>
          No {kind.noun} yet
        </span>
      </span>
    </button>
  )
})

/** A tool: one box that opens its page, where its contents are not boxes. */
export const ToolNode = memo(function ToolNode({ data }: NodeProps<Node<SlotNodeData>>) {
  const { open } = useActions()
  const { kind } = data
  const Glyph = KIND_GLYPH[kind.key]
  return (
    <button
      type="button"
      className="nodrag flex size-full items-center gap-2.5 rounded-lg border border-hairline bg-card px-2.5 text-left focus-ring transition-colors hover:border-border-strong"
      onClick={(event) => {
        event.stopPropagation()
        open(kind.href)
      }}
    >
      <Ports />
      <span className="flex size-8 shrink-0 items-center justify-center rounded-lg border border-hairline bg-background text-muted-foreground [&>svg]:size-4">
        {Glyph && <Glyph aria-hidden />}
      </span>
      <span className={cn("min-w-0", NOT_FAR)}>
        <span className="block truncate text-body font-medium">{kind.tool?.label}</span>
        <span className={cn("block truncate text-hint text-muted-foreground", SECOND_LINE)}>
          {kind.tool?.detail}
        </span>
      </span>
    </button>
  )
})

export const nodeTypes = {
  server: ServerNode,
  branch: BranchNode,
  shelf: ShelfNode,
  item: ItemNode,
  more: MoreNode,
  add: AddNode,
  tool: ToolNode,
}
