"use client"

import Link from "next/link"
import { createRef, useMemo, useRef, useState, type RefObject } from "react"
import { useMediaQuery } from "@/hooks/use-mobile"
import { cn } from "@/lib/utils"
import { WireNode } from "@/components/deploy/wire"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { Skeleton } from "@/components/ui/skeleton"
import {
  PORT,
  beamOf,
  fitColumn,
  flowTemplate,
  isLit,
  loadingRows,
  staggerOf,
  waitingLines,
  type WaitingHint,
  type WireLine,
} from "@/components/wire-flow-model"

export type { WaitingHint, WireLine, WireState } from "@/components/wire-flow-model"

type MarkSize = "lg" | "md" | "sm"
/** A mark, or one drawn at the size it is given: `md` wide, `sm` in the phone's rows. */
type Drawn = React.ReactNode | ((size: MarkSize) => React.ReactNode)

export type WireFlowNode = {
  id: string
  /** A `WireMark`, `WirePlaceholder` or a thing's own tile. */
  mark: Drawn
  eyebrow?: React.ReactNode
  title?: React.ReactNode
  hint?: React.ReactNode
  /** The node in words, where `title` is drawn: the link's name and the phone's caption. */
  name?: string
  /** The mark is a link to here. */
  href?: string
  /** The mark is a button that does this. */
  onPress?: () => void
  /** The link's or button's accessible name, when it is not `name`. */
  label?: string
  /** The line between this node and the hub. */
  line?: WireLine
  /**
   * A dashed ring where a thing would go: its focus ring is round, and it does
   * not take part in pointing (it has no flows to light).
   */
  placeholder?: boolean
}

/**
 * The shape a node waits in, for a column or a hub whose nodes are not a
 * grey tile beside three lines. A picture is one height loading and loaded
 * only if a waiting node holds the lines its words will: a node with no
 * eyebrow waits a line short, one with two lines of detail a line long, and
 * pages drew grey nodes of their own to get it right. What is known before
 * the read (this server's mark, a column's fixed eyebrow, the hub's name) is
 * drawn as itself rather than as a grey bar.
 */
export type WireFlowWaiting = {
  /** The mark, where it is known before the read. A grey tile otherwise. */
  mark?: Drawn
  /**
   * The eyebrow's line: grey by default, the words where they are known
   * before the read, `false` for a node that has no eyebrow.
   */
  eyebrow?: boolean | React.ReactNode
  /** The name, where it is known before the read. A grey line otherwise. */
  title?: React.ReactNode
  /** The lines of detail under the name: one by default. */
  hint?: WaitingHint
  /** What stands under the lines at one size whatever is read: a sparkline's box. */
  foot?: React.ReactNode
}

export type WireFlowColumn = {
  /** The column's name: its landmark's label and its heading. */
  title: string
  nodes: WireFlowNode[]
  /** A count beside the heading, where the column is a part of something larger. */
  count?: number
  /**
   * Where the heading is drawn: `narrow` only where the columns stack (the
   * wide picture says it by position), `always` hung above the column wide too.
   */
  heading?: "narrow" | "always"
  /** More than this many and the rest are one "N more" node. */
  max?: number
  /** The "N more" node: where it goes and what it says. */
  more?: { href?: string; onPress?: () => void; words?: (hidden: number) => string }
  /** Said in place of the column when it has no nodes. */
  empty?: React.ReactNode
  /** Rows the column holds while loading; by default `max` and its "more", or three. */
  loadingRows?: number
  /**
   * The shape of those rows, one for all or one for each by its place. By
   * default a grey tile beside an eyebrow, a name and one line of detail.
   */
  loadingLines?: WireFlowWaiting | ((row: number) => WireFlowWaiting)
}

export type WireFlowHub = {
  mark: Drawn
  eyebrow?: React.ReactNode
  title?: React.ReactNode
  hint?: React.ReactNode
  /** On the hub's cell: room above and below for a hub with long words. */
  className?: string
  /**
   * The lines under the hub's mark while loading. Given, a phone's rows of
   * marks draw them too; by default the hub waits on a name and one line
   * wide and on its name alone down a phone, whose rows of marks then stand
   * a line higher than they will for every line of detail the hub has.
   */
  loadingLines?: Pick<WireFlowWaiting, "title" | "hint" | "foot">
}

/**
 * Things on the left, one hub in the middle, things on the right, and the
 * lines between them, from data — the picture six Network pages, the
 * deployment section's Notifications, Backups and the proxy each drew by hand
 * on `deploy/wire` (design-system §2). A node is a mark with an eyebrow, a
 * name and a line of detail; its line to the hub is its state (`WireLine`),
 * drawn by `ui/animated-beam`, which holds the line still under reduced
 * motion. Pointing at a node steps every other node and line back, keeping
 * what `related` says travels with it.
 *
 * On a phone it does one of the two things the hand-drawn pictures did:
 *
 * - `stack` (the default): the columns stack under their headings with every
 *   word kept and the lines not drawn. For a picture that is the only place its
 *   nodes are named — the Network overview, the gateway.
 * - `marks`: the hub's words over a row of the start column's marks, the hub,
 *   and a row of the end column's, the lines running down between them. For a
 *   picture whose nodes a list under it names again — Notifications, Backups,
 *   the proxy's alerts — where the words would be said twice.
 *
 * `loading` draws the picture's final shape — the hub, and grey rows where
 * the nodes will be — so nothing under it moves when the data lands. A column
 * says how many rows with `loadingRows` and what a row holds with
 * `loadingLines`, the hub what stands under its mark with its own
 * `loadingLines` (`WireFlowWaiting`).
 */
export function WireFlow({
  start,
  hub,
  end,
  size = "md",
  phone = "stack",
  hubShare = 0.8,
  related,
  loading = false,
  className,
}: {
  start: WireFlowColumn
  hub: WireFlowHub
  end: WireFlowColumn
  /** The size of the nodes' marks, wide. The phone's `marks` rows draw them `sm`. */
  size?: Exclude<MarkSize, "lg">
  phone?: "stack" | "marks"
  /** The hub column's share of the width against one for each side. */
  hubShare?: number
  /** What lights with a pointed node besides itself. */
  related?: (focus: string, id: string) => boolean
  loading?: boolean
  className?: string
}) {
  const wide = useMediaQuery("(min-width: 1024px)")
  const container = useRef<HTMLDivElement>(null)
  const hubRef = useRef<HTMLDivElement>(null)
  const [focus, setFocus] = useState<string | null>(null)
  const compact = phone === "marks" && !wide

  const left = useMemo(() => laid(start, "start", loading), [start, loading])
  const right = useMemo(() => laid(end, "end", loading), [end, loading])
  const all = [...left, ...right]
  const ids = all.map((n) => n.id).join("\n")
  const refs = useMemo(() => {
    const map = new Map<string, RefObject<HTMLDivElement | null>>()
    for (const id of ids.split("\n")) if (id) map.set(id, createRef<HTMLDivElement>())
    return map
  }, [ids])

  const lit = (id: string) => isLit(focus, id, related)
  const watch = (node: Laid) =>
    node.placeholder || node.more || loading
      ? {}
      : {
          onPointerEnter: () => setFocus(node.id),
          onPointerLeave: () => setFocus((held) => (held === node.id ? null : held)),
          onFocus: () => setFocus(node.id),
          onBlur: () => setFocus((held) => (held === node.id ? null : held)),
        }
  const port = PORT[size]
  const hubWaits = hub.loadingLines && waitingDetail(hub.loadingLines, HUB_WIDTHS)

  const beams = all.map((node, index) => {
    const ref = refs.get(node.id)
    if (!ref) return null
    const into = node.side === "start"
    const beam = beamOf(loading ? { state: "up" } : node.line)
    return (
      <AnimatedBeam
        key={node.id}
        containerRef={container}
        fromRef={into ? ref : hubRef}
        toRef={into ? hubRef : ref}
        // Wide, a wire leaves a mark level from its edge and arrives level at
        // the other; down a phone the rows are above and below, so it runs
        // straight from centre to centre.
        shape={compact ? "arc" : "s"}
        startXOffset={compact ? 0 : port}
        endXOffset={compact ? 0 : -port}
        reverse={node.line?.reverse}
        still={beam.still}
        dashed={beam.dashed}
        tone={beam.tone}
        duration={node.line?.duration}
        delay={node.line?.delay ?? staggerOf(index)}
        className={cn(
          "transition-opacity",
          phone === "stack" && "max-lg:hidden",
          !lit(node.id) && "opacity-15",
        )}
      />
    )
  })

  if (compact)
    return (
      <div
        data-slot="wire-flow"
        data-loading={loading || undefined}
        aria-busy={loading || undefined}
        className={cn("relative py-4", className)}
      >
        <div ref={container} className="relative flex flex-col items-center text-center">
          {beams}
          {hub.eyebrow && <p className="eyebrow">{hub.eyebrow}</p>}
          {loading ? (
            <>
              <p className="text-body leading-snug font-medium">
                {hub.loadingLines?.title ?? <WaitingWords className="w-32" />}
              </p>
              {hubWaits && (
                <div className="text-hint leading-snug text-muted-foreground">{hubWaits}</div>
              )}
            </>
          ) : (
            <>
              {hub.title && <p className="text-body leading-snug font-medium">{hub.title}</p>}
              {hub.hint && (
                <div className="text-hint leading-snug text-muted-foreground">{hub.hint}</div>
              )}
            </>
          )}
          <MarkRow column={start} nodes={left} refs={refs} />
          <div ref={hubRef} className="relative z-10 mt-10 flex">
            {draw(hub.mark, "lg")}
          </div>
          <MarkRow column={end} nodes={right} refs={refs} className="mt-10" />
        </div>
      </div>
    )

  return (
    <div
      data-slot="wire-flow"
      data-loading={loading || undefined}
      aria-busy={loading || undefined}
      className={cn("relative py-4", className)}
    >
      <div ref={container} className="relative">
        {beams}
        <div
          className="relative grid gap-y-8 lg:grid-cols-[var(--wire-flow-columns)] lg:items-center"
          style={{ "--wire-flow-columns": flowTemplate(hubShare) } as React.CSSProperties}
        >
          <Column column={start} side="start" bare={left.length === 0}>
            {left.map((node) => (
              <Item
                key={node.id}
                node={node}
                nodeRef={refs.get(node.id)}
                size={size}
                dim={!node.placeholder && !lit(node.id)}
                loading={loading}
                {...watch(node)}
              />
            ))}
          </Column>

          <div aria-hidden className="max-lg:hidden" />

          <div className={cn("flex min-w-0 justify-center", hub.className)}>
            <WireNode
              nodeRef={hubRef}
              align="center"
              mark={draw(hub.mark, "lg")}
              eyebrow={hub.eyebrow}
              title={
                loading ? (hub.loadingLines?.title ?? <WaitingWords className="w-28" />) : hub.title
              }
              hint={
                !loading ? (
                  hub.hint
                ) : hub.loadingLines ? (
                  hubWaits
                ) : (
                  <WaitingWords className="w-36" />
                )
              }
            />
          </div>

          <div aria-hidden className="max-lg:hidden" />

          <Column column={end} side="end" bare={right.length === 0}>
            {right.map((node) => (
              <Item
                key={node.id}
                node={node}
                nodeRef={refs.get(node.id)}
                size={size}
                dim={!node.placeholder && !lit(node.id)}
                loading={loading}
                {...watch(node)}
              />
            ))}
          </Column>
        </div>
      </div>
    </div>
  )
}

type Laid = WireFlowNode & {
  side: "start" | "end"
  more?: boolean
  /** A row drawn while loading, and the shape its column gave it. */
  waiting?: WireFlowWaiting | true
}

/** A column's nodes as drawn: what fits, its "N more", or grey rows while loading. */
function laid(column: WireFlowColumn, side: "start" | "end", loading: boolean): Laid[] {
  if (loading)
    return Array.from({ length: loadingRows(column) }, (_, i) => ({
      id: `${side}:loading:${i}`,
      side,
      mark: null,
      waiting:
        (typeof column.loadingLines === "function"
          ? column.loadingLines(i)
          : column.loadingLines) ?? true,
    }))
  const { shown, hidden } = fitColumn(column.nodes, column.max)
  const nodes: Laid[] = shown.map((node) => ({ ...node, side }))
  if (hidden > 0) {
    const words = column.more?.words?.(hidden) ?? `${hidden} more`
    nodes.push({
      id: `${side}:more`,
      side,
      more: true,
      mark: (size) => <MoreTile count={hidden} size={size} />,
      title: <span className="font-normal text-muted-foreground">{words}</span>,
      name: words,
      href: column.more?.href,
      onPress: column.more?.onPress,
    })
  }
  return nodes
}

function draw(mark: Drawn, size: MarkSize) {
  return typeof mark === "function" ? mark(size) : mark
}

/** The column's landmark, its heading, and its list or what it says when empty. */
function Column({
  column,
  side,
  bare,
  children,
}: {
  column: WireFlowColumn
  side: "start" | "end"
  /** Nothing to draw: the column says `empty` instead. */
  bare: boolean
  children: React.ReactNode
}) {
  const always = column.heading === "always"
  const heading = (
    <p
      className={cn(
        "eyebrow mb-4",
        always ? "lg:absolute lg:inset-x-0 lg:bottom-full" : "lg:hidden",
      )}
    >
      {column.title}
      {column.count !== undefined && column.count > 0 && (
        <span className="numeric ml-1.5 font-medium tracking-normal text-muted-foreground/70">
          {column.count}
        </span>
      )}
    </p>
  )
  const body =
    bare && column.empty ? (
      <p className="text-body text-muted-foreground">{column.empty}</p>
    ) : (
      <ol className="flex flex-col gap-5">{children}</ol>
    )
  return (
    <section
      aria-label={column.title}
      className={cn("min-w-0", side === "start" && "lg:text-right")}
    >
      {/* Hung above the list wide rather than sharing its box: in the box it
          pushed the marks below the hub's, and every wire bent to meet them. */}
      <div className={cn(always && "lg:relative")}>
        {heading}
        {body}
      </div>
    </section>
  )
}

function Item({
  node,
  nodeRef,
  size,
  dim,
  loading,
  ...handlers
}: {
  node: Laid
  nodeRef?: RefObject<HTMLDivElement | null>
  size: MarkSize
  dim: boolean
  loading: boolean
} & React.HTMLAttributes<HTMLLIElement>) {
  const align = node.side === "start" ? "end" : "start"
  if (loading) {
    const shape = typeof node.waiting === "object" ? node.waiting : undefined
    return (
      <li className="min-w-0" aria-hidden>
        <WireNode
          nodeRef={nodeRef}
          align={align}
          mark={waitingMark(shape, size)}
          eyebrow={
            shape?.eyebrow === false ? undefined : shape?.eyebrow === undefined ||
              shape.eyebrow === true ? (
              <WaitingWords className="w-16" />
            ) : (
              shape.eyebrow
            )
          }
          title={shape?.title ?? <WaitingWords className="w-28" />}
          hint={shape ? waitingDetail(shape, NODE_WIDTHS) : <WaitingWords className="w-20" />}
        />
      </li>
    )
  }
  return (
    <li
      {...handlers}
      data-node={node.more ? undefined : node.id}
      data-dim={dim ? "" : undefined}
      className={cn("min-w-0 transition-opacity", dim && "opacity-40")}
    >
      <WireNode
        nodeRef={nodeRef}
        align={align}
        mark={<Pressable node={node}>{draw(node.mark, size)}</Pressable>}
        eyebrow={node.eyebrow}
        title={node.title}
        hint={node.hint}
      />
    </li>
  )
}

const SKELETON_SIZE = { lg: "size-14", md: "size-11", sm: "size-9" } as const

/** How long each waiting line of detail is drawn, by its place: no two alike. */
const NODE_WIDTHS = ["w-24", "w-20", "w-16"]
const HUB_WIDTHS = ["w-32", "w-24", "w-28"]

/** A waiting row's mark: the one its column knows before the read, or a grey tile. */
function waitingMark(shape: WireFlowWaiting | undefined, size: MarkSize) {
  return shape?.mark !== undefined ? (
    draw(shape.mark, size)
  ) : (
    <Skeleton className={cn("rounded-xl", SKELETON_SIZE[size])} />
  )
}

/**
 * A waiting node's lines of detail and what stands under them, each line a
 * block as a drawn node's lines are; nothing at all for a node that has
 * neither, so its name is not followed by an empty line.
 */
function waitingDetail(shape: WireFlowWaiting, widths: string[]) {
  const lines = waitingLines(shape.hint)
  if (lines.length === 0 && !shape.foot) return undefined
  return (
    <>
      {lines.map((where, i) => (
        <span
          key={i}
          className={cn(
            "block",
            where === "wide" && "max-lg:hidden",
            where === "narrow" && "lg:hidden",
          )}
        >
          <WaitingWords className={widths[i % widths.length]} />
        </span>
      ))}
      {shape.foot}
    </>
  )
}

/**
 * A grey bar where a line of words will be. It is an inline box shorter than
 * the line it stands in, set on the line's middle, so the line keeps exactly
 * the height its words will give it: a block-level `Skeleton` with margins
 * made a waiting node 11px taller than a loaded one, and a picture with a
 * dozen nodes shrank when its data landed. A `span`, because two of the three
 * lines it stands in are paragraphs.
 */
function WaitingWords({ className }: { className?: string }) {
  return (
    <span
      data-slot="skeleton"
      className={cn(
        "inline-block h-[0.7em] max-w-full animate-pulse rounded-md bg-muted align-middle",
        className,
      )}
    />
  )
}

/** A node's mark as the link or button it is, or as itself. */
function Pressable({ node, children }: { node: Laid; children: React.ReactNode }) {
  const label = node.label ?? node.name ?? (typeof node.title === "string" ? node.title : undefined)
  const shape = node.placeholder || node.more ? "rounded-full" : "rounded-xl"
  if (node.onPress)
    return (
      <button
        type="button"
        onClick={node.onPress}
        aria-label={label}
        className={cn(shape, "focus-ring")}
      >
        {children}
      </button>
    )
  if (node.href)
    return (
      <Link href={node.href} aria-label={label} className={cn(shape, "focus-ring")}>
        {children}
      </Link>
    )
  return <>{children}</>
}

/** The phone's row of marks, each named for a screen reader rather than on the page. */
function MarkRow({
  column,
  nodes,
  refs,
  className,
}: {
  column: WireFlowColumn
  nodes: Laid[]
  refs: Map<string, RefObject<HTMLDivElement | null>>
  className?: string
}) {
  return (
    <section aria-label={column.title} className={cn("mt-5", className)}>
      <ol className="flex flex-wrap justify-center gap-4">
        {nodes.map((node) => (
          <li key={node.id} data-node={node.more ? undefined : node.id} className="min-w-0">
            <div ref={refs.get(node.id)} className="relative z-10 flex">
              {node.waiting ? (
                waitingMark(typeof node.waiting === "object" ? node.waiting : undefined, "sm")
              ) : (
                <Pressable node={node}>{draw(node.mark, "sm")}</Pressable>
              )}
            </div>
            {node.name && <span className="sr-only">{node.name}</span>}
          </li>
        ))}
      </ol>
    </section>
  )
}

/** The nodes a column has no room for, as a count on a tile of the marks' own shape. */
function MoreTile({ count, size }: { count: number; size: MarkSize }) {
  return (
    <span className="flex shrink-0 rounded-xl bg-background">
      <span
        className={cn(
          "numeric flex items-center justify-center rounded-[inherit] border border-hairline text-hint text-muted-foreground",
          SKELETON_SIZE[size],
        )}
      >
        +{count}
      </span>
    </span>
  )
}
