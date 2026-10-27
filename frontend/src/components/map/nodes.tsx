"use client"

import { createContext, memo, useContext } from "react"
import { Handle, Position, type Node, type NodeProps } from "@xyflow/react"
import {
  ArrowUpRight,
  ChevronDown,
  ChevronRight,
  Plus,
  RotateCounterClockwise,
} from "@/components/icons"
import { rowReveal } from "@/components/icon-action"
import { WireHost, WirePlaceholder } from "@/components/deploy/wire"
import { Meter, utilisationTone } from "@/components/meter"
import { ProductGlyph, ProductLogo, platformProduct } from "@/components/product-logo"
import { StatusDot } from "@/components/status-dot"
import { useMetrics } from "@/hooks/use-metrics"
import { duration, percent, plural } from "@/lib/format"
import type { Branch, KindDef } from "@/lib/server-map"
import type { MapItem, MapKindReading } from "@/lib/types"
import { cn } from "@/lib/utils"
import {
  BRANCH_GLYPH,
  HueTile,
  ItemGlyph,
  ItemMark,
  KIND_GLYPH,
  TONE_DOT,
  branchOf,
} from "@/components/map/marks"

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
  /** Put a thing pulled off its shelf back in its slot. */
  returnToShelf: (id: string) => void
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
 *   mid  — a thing is its mark, its name and its dot, and a shelf's offer
 *          to add the first one or a tool is its words alone;
 *   far  — a thing is its name and its dot, the name given the box's width.
 *
 * Past near no name is left to shrink with the map. The server's and the
 * branches' names read at four fifths of their size, a shelf's and a thing's
 * at 11px, so the eye goes heart, branch, shelf, thing however far out it is.
 * The names in a box grow by their font size (`--map-name`, `--map-label`,
 * written by the canvas in small steps), so they are laid out and cut short
 * inside their box rather than spilling over its buttons or its neighbour;
 * the server's and the branches' names have open ground round them and are
 * scaled instead (`--map-unzoom`, every frame, no layout), so they are always
 * read whole.
 */
const SECOND_LINE = "group-data-[zoom=mid]/map:hidden group-data-[zoom=far]/map:hidden"
const NOT_FAR = "group-data-[zoom=far]/map:hidden"
const ONLY_NEAR = SECOND_LINE
const NOT_NEAR = "group-data-[zoom=near]/map:hidden"
const ONLY_FAR = "hidden group-data-[zoom=far]/map:flex"
/** The words in a box held to the screen, from the ladder step they are set in. */
const NAME = {
  hint: "[font-size:calc(var(--text-hint)*var(--map-name,1))]",
  sm: "[font-size:calc(var(--text-sm)*var(--map-name,1))]",
  title: "[font-size:calc(var(--text-title)*var(--map-name,1))]",
}
/** A shelf's head held to the screen: its eyebrow, its count, its glyph and the room between. */
const SHELF = {
  label: "[font-size:calc(var(--text-micro)*var(--map-label,1))]",
  count: "[font-size:calc(var(--text-hint)*var(--map-label,1))]",
  glyph: "size-[min(2.75rem,calc(1rem*var(--map-label,1)))]",
  gap: "gap-[calc(0.5rem*var(--map-label,1))]",
}
/**
 * A name with open ground round it, held to the screen by scaling from its
 * left edge. Its box grows with it, so it lets the pointer through to what it
 * comes to lie over — a branch's fold, the box beneath it.
 */
const HELD =
  "pointer-events-none origin-left [transform:scale(max(1,calc(var(--map-unzoom,1)*0.8)))]"
/** A state's dot drawn on its own, a little larger the further out. */
const HELD_DOT = "[transform:scale(min(2.5,max(1,calc(var(--map-unzoom,1)*0.5))))]"
/** A thing's mark far out, beside its name and grown with it. */
const HELD_MARK = "size-[calc(1rem*var(--map-name,1))]"
/** The marks a folded shelf names what it holds by, grown with its head. */
const HELD_STRIP = "size-[calc(0.875rem*var(--map-label,1))]"

/** A row of bare marks after a count, as `ProductGlyphs` draws them, sized by the caller. */
function MarkStrip({ ids, className }: { ids: string[]; className: string }) {
  if (ids.length === 0) return null
  return (
    <span aria-hidden="true" className="flex shrink-0 items-center gap-1">
      {ids.map((id) => (
        <ProductGlyph key={id} id={id} className={className} />
      ))}
    </span>
  )
}

export type ServerNodeData = {
  things: number
  down: number
  hostname?: string
  /** What runs here most, as marks: the products the map's things are. */
  products: string[]
}

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
    <div className="flex size-full flex-col justify-between rounded-xl border border-rule-brand bg-card p-4 group-data-[zoom=far]/map:justify-center">
      <Ports />
      <div className="flex min-w-0 items-center gap-3">
        <WireHost size="md" />
        <div className={cn("min-w-0", HELD)}>
          <div className="truncate text-base font-semibold">
            {host?.hostname ?? data.hostname ?? "This server"}
          </div>
          <div
            className={cn(
              "flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground",
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
      <div className={cn("flex items-center gap-1.5 text-xs text-muted-foreground", SECOND_LINE)}>
        <span className="numeric text-foreground">{data.things.toLocaleString("en-US")}</span>
        {data.things === 1 ? "thing" : "things"} on the map
        {data.down > 0 && (
          <>
            <span aria-hidden>·</span>
            <StatusDot tone="danger" />
            <span className="numeric text-destructive">{data.down} down</span>
          </>
        )}
        <span className="flex-1" />
        <MarkStrip ids={data.products} className="size-4" />
      </div>
    </div>
  )
})

function Reading({ label, value, live }: { label: string; value?: number; live: boolean }) {
  return (
    <div className="min-w-0 space-y-1">
      <div className="flex items-baseline justify-between gap-2 text-xs">
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
  /** What the branch holds most, as marks. */
  products: string[]
  dimmed?: boolean
}

/**
 * A branch: one of the seven things a server is for, in its hue, with how
 * much of it there is and the products it holds most. Its chevron folds
 * every shelf it holds into this one head.
 */
export const BranchNode = memo(function BranchNode({ data }: NodeProps<Node<BranchNodeData>>) {
  const { toggleFold } = useActions()
  const Glyph = BRANCH_GLYPH[data.branch.key]
  const id = `branch:${data.branch.key}`
  return (
    <div
      data-branch={data.branch.key}
      className={cn(
        "flex size-full items-center gap-3 rounded-xl border border-(--hue-rule) bg-card px-3 transition-opacity",
        data.dimmed && "opacity-40",
      )}
    >
      <Ports />
      <span
        className={cn(
          "flex size-10 shrink-0 items-center justify-center rounded-lg border border-(--hue-rule) bg-(--hue-plot) text-(--hue) [&>svg]:size-5",
          ONLY_NEAR,
        )}
      >
        <Glyph aria-hidden />
      </span>
      <div className={cn("min-w-0 flex-1", HELD)}>
        <div className="flex min-w-0 items-center gap-2">
          {/* Past near the tile is gone; the hue stays, as a dot before the name. */}
          <span aria-hidden className={cn("size-2.5 shrink-0 rounded-full bg-(--hue)", NOT_NEAR)} />
          <span className="truncate text-base font-semibold">{data.branch.label}</span>
        </div>
        {/* One line however much it says: the marks come last, so a branch
            that is folded and has things down loses its marks, never its words. */}
        <div
          className={cn(
            "flex min-w-0 items-center gap-1.5 overflow-hidden text-xs whitespace-nowrap text-muted-foreground",
            SECOND_LINE,
          )}
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
          <MarkStrip ids={data.products} className="ml-1 size-3.5" />
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
  /** The reader pointing at it from the inspector. */
  hovered?: boolean
  /**
   * One of its things is being dragged: `away` while it is off the shelf,
   * `over` while letting go would put it back.
   */
  home?: "away" | "over"
  /** What it holds most, as marks: drawn in its head while it is folded. */
  products: string[]
}

/**
 * A shelf head's add and open: drawn near, gone far out where they would be
 * specks, and at mid shown while the head is pointed at or focused — so the
 * name held to the screen has the head's width to be read in, and gives a
 * little of it back when a way in is wanted.
 */
const HEAD_ACTION =
  "group-data-[zoom=far]/map:hidden group-data-[zoom=mid]/map:hidden group-hover/head:group-data-[zoom=mid]/map:flex group-focus-within/head:group-data-[zoom=mid]/map:flex"

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
      data-home={data.home}
      data-hovered={data.hovered || undefined}
      data-branch={kind.branch}
      className={cn(
        "size-full rounded-xl border bg-background transition-[opacity,border-color,background-color]",
        data.home === "away" && "border-dashed border-rule-brand",
        data.home === "over" && "border-rule-brand bg-wash-brand",
        !data.home && (data.hovered ? "border-ring" : "border-(--hue-rule)"),
        data.dimmed && !data.home && "opacity-40",
      )}
    >
      <Ports />
      {/* The head is washed in the branch's hue, so a shelf is found by its colour from far out. */}
      <div className="group/head flex h-14 items-center gap-2 rounded-t-xl bg-(--hue-wash) pr-2 pl-3">
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
        <span className={cn("flex min-w-0 items-center", SHELF.gap)}>
          {Glyph && <Glyph aria-hidden className={cn("shrink-0 text-(--hue)", SHELF.glyph)} />}
          <span className={cn("eyebrow truncate", SHELF.label)}>{kind.label}</span>
          {!kind.tool && reading?.state !== "unread" && (
            <span className={cn("numeric text-muted-foreground", SHELF.count)}>
              {total.toLocaleString("en-US")}
            </span>
          )}
          {down > 0 && (
            <span className={cn("flex shrink-0 items-center gap-1 text-destructive", SHELF.count)}>
              <StatusDot tone="danger" />
              <span className="numeric">{down} down</span>
            </span>
          )}
          {data.folded && <MarkStrip ids={data.products} className={HELD_STRIP} />}
        </span>
        <ShelfNote reading={reading} />
        <span className="flex-1" />
        {/* Where a thing being dragged belongs, said while it is dragged. */}
        {data.home && (
          <span className={cn("shrink-0 text-hint text-brand", SECOND_LINE)}>
            {data.home === "over" ? "Let go to put it back" : "Drop here to put it back"}
          </span>
        )}
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
            className={cn(HEAD_BUTTON, HEAD_ACTION)}
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
          className={cn(HEAD_BUTTON, HEAD_ACTION)}
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
  /** Drawn as a card (`KindDef.face`): its state and its fact on lines of their own. */
  card?: boolean
  selected?: boolean
  dimmed?: boolean
  /** Wired to the chosen box: drawn lit while the dimmed ones step back. */
  related?: boolean
  /** The reader pointing at it from the inspector. */
  hovered?: boolean
  /** Pulled off its shelf and placed by hand. */
  away?: boolean
}

/**
 * One thing: its mark, its name, and its state in a word with the dot that
 * colours it. The state is what was measured — a container's state and
 * health, a backup's last run, a certificate's days — never a verdict the
 * map reached on its own. A card is the same box made room in: the name a
 * step up, and the fact that says which one it is (a project's address, a
 * database's engine, where a domain goes) under the state rather than cut
 * short beside it.
 */
export const ItemNode = memo(function ItemNode({ data, dragging }: NodeProps<Node<ItemNodeData>>) {
  const { returnToShelf } = useActions()
  const { item, card } = data
  return (
    <div
      data-selected={data.selected || undefined}
      data-hovered={data.hovered || undefined}
      data-tone={item.tone}
      data-away={data.away || undefined}
      data-face={card ? "card" : undefined}
      data-branch={branchOf(data.kind)}
      className={cn(
        "group relative flex size-full items-center gap-3 rounded-lg border bg-card px-3 transition-[opacity,border-color,background-color]",
        card && "rounded-xl px-3.5",
        data.selected
          ? "border-border-strong bg-accent"
          : data.hovered
            ? "border-ring"
            : data.related
              ? "border-rule-brand"
              : dragging
                ? "border-rule-brand"
                : // A thing that is down is edged in the colour its dot is
                  // already: its own measured state, said by the box as well.
                  TONE_EDGE[item.tone],
        data.dimmed && "opacity-35",
      )}
    >
      <Ports />
      <ItemMark item={item} kind={data.kind} size="md" className={NOT_FAR} />
      {/* Far out the tile would be a speck: the mark alone, grown with the name. */}
      <span data-slot="map-mark" className={cn("shrink-0", HELD_MARK, ONLY_FAR)}>
        <ItemGlyph item={item} kind={data.kind} className="size-full" />
      </span>
      <div className="min-w-0 flex-1">
        <div
          data-slot="map-name"
          className={cn("truncate leading-tight font-medium", card ? NAME.title : NAME.sm)}
        >
          {item.name}
        </div>
        <div
          className={cn(
            "mt-1 flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground",
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
          {item.detail && !card && (
            <>
              <span aria-hidden>·</span>
              <span className="truncate">{item.detail}</span>
            </>
          )}
        </div>
        {item.detail && card && (
          <div className={cn("mt-0.5 truncate text-xs text-muted-foreground", SECOND_LINE)}>
            {item.detail}
          </div>
        )}
      </div>
      {/* Past near the state is the dot alone, at the end of the name. */}
      <span className={cn("flex shrink-0", NOT_NEAR)}>
        <StatusDot tone={TONE_DOT[item.tone]} className={cn("size-3", HELD_DOT)} />
      </span>
      {/* A thing placed by hand keeps the way back to its shelf on itself. */}
      {data.away && !dragging && (
        <button
          type="button"
          aria-label={`Put ${item.name} back on its shelf`}
          title="Back on its shelf"
          className={cn(
            "nodrag absolute -top-2.5 -right-2.5 flex size-6 items-center justify-center rounded-md border border-hairline bg-popover text-muted-foreground focus-ring hover:text-foreground",
            rowReveal(),
            NOT_FAR,
          )}
          onClick={(event) => {
            event.stopPropagation()
            returnToShelf(item.id)
          }}
        >
          <RotateCounterClockwise className="size-3.5" />
        </button>
      )}
    </div>
  )
})

/** A box's edge at rest, by its state: what is down or needs a look says so at its edge too. */
const TONE_EDGE: Record<MapItem["tone"], string> = {
  danger: "border-rule-danger hover:border-destructive",
  warning: "border-rule-warning hover:border-warning",
  ok: "border-hairline hover:border-border-strong",
  idle: "border-hairline hover:border-border-strong",
  unknown: "border-hairline hover:border-border-strong",
}

export type SlotNodeData = { kind: KindDef; count?: number; dimmed?: boolean }

/** "+N more": the rest of a long shelf, a press away. */
export const MoreNode = memo(function MoreNode({ data }: NodeProps<Node<SlotNodeData>>) {
  const { expand } = useActions()
  return (
    <button
      type="button"
      data-branch={data.kind.branch}
      className={cn(
        "nodrag flex size-full items-center justify-center gap-1.5 rounded-lg border border-dashed border-(--hue-rule) text-sm text-muted-foreground focus-ring transition-colors hover:bg-row-hover hover:text-foreground",
        data.dimmed && "opacity-35",
      )}
      onClick={(event) => {
        event.stopPropagation()
        expand(data.kind.key)
      }}
    >
      <Ports />
      <span className={cn("flex items-center gap-1.5", HELD, "origin-center")}>
        <span className="numeric text-(--hue)">+{(data.count ?? 0).toLocaleString("en-US")}</span>
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
      <div className="flex size-full items-center rounded-lg px-3 text-muted-foreground">
        <Ports />
        <span className={cn("truncate", NAME.hint)}>No {kind.label.toLowerCase()} yet</span>
      </div>
    )
  }
  return (
    <button
      type="button"
      data-branch={kind.branch}
      className="nodrag group/add flex size-full items-center gap-3 rounded-lg border border-dashed border-(--hue-rule) px-3 text-left focus-ring transition-colors hover:bg-row-hover"
      onClick={(event) => {
        event.stopPropagation()
        actions.add(kind)
      }}
    >
      <Ports />
      <WirePlaceholder size="md" fallback={Glyph} className={ONLY_NEAR} />
      <span className="min-w-0">
        <span className={cn("block truncate leading-tight font-medium", NAME.sm)}>
          {kind.add.label}
        </span>
        <span className={cn("mt-1 block truncate text-xs text-muted-foreground", SECOND_LINE)}>
          No {kind.noun} yet
        </span>
      </span>
    </button>
  )
})

/**
 * A tool: one box that opens its page, where its contents are not boxes.
 * Packages are the system's own, so that tool is drawn as the distribution
 * the server runs; Files is a glyph in its branch's hue.
 */
export const ToolNode = memo(function ToolNode({ data }: NodeProps<Node<SlotNodeData>>) {
  const { open } = useActions()
  const { kind } = data
  return (
    <button
      type="button"
      data-branch={kind.branch}
      className="nodrag flex size-full items-center gap-3 rounded-lg border border-hairline bg-card px-3 text-left focus-ring transition-colors hover:border-border-strong"
      onClick={(event) => {
        event.stopPropagation()
        open(kind.href)
      }}
    >
      <Ports />
      {kind.key === "packages" ? (
        <SystemMark fallback={kind.key} className={ONLY_NEAR} />
      ) : (
        <HueTile kind={kind.key} className={ONLY_NEAR} />
      )}
      <span className="min-w-0">
        <span className={cn("block truncate leading-tight font-medium", NAME.sm)}>
          {kind.tool?.label}
        </span>
        <span className={cn("mt-1 block truncate text-xs text-muted-foreground", SECOND_LINE)}>
          {kind.tool?.detail}
        </span>
      </span>
    </button>
  )
})

/** The distribution the server runs, from the shell's own host reading. */
function SystemMark({ fallback, className }: { fallback: string; className?: string }) {
  const { host } = useMetrics()
  const platform = platformProduct(host?.platform)
  if (!platform) return <HueTile kind={fallback} className={className} />
  return <ProductLogo id={platform} size="md" className={className} />
}

export const nodeTypes = {
  server: ServerNode,
  branch: BranchNode,
  shelf: ShelfNode,
  item: ItemNode,
  more: MoreNode,
  add: AddNode,
  tool: ToolNode,
}
