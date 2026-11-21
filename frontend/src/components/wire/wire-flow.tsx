"use client"

import Link from "next/link"
import { createRef, useLayoutEffect, useMemo, useRef, useState, type RefObject } from "react"
import { cn } from "@/lib/utils"
import { WireNode } from "@/components/wire/wire"
import { WireLaneTitle } from "@/components/wire/wire-lane"
import { useMotionHeld } from "@/components/wire/use-motion-held"
import { MoreTile, WireMarks } from "@/components/wire/wire-marks"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { Skeleton } from "@/components/ui/skeleton"
import {
  CELL,
  PORT,
  beamOf,
  beyondInRow,
  carriedBeam,
  columnStart,
  fitColumn,
  flowForm,
  flowTemplate,
  isLit,
  levelDrops,
  loadingRows,
  middleRows,
  nearerInRow,
  paintOrder,
  perRow,
  staggerOf,
  waitingLines,
  type WaitingHint,
  type WireLine,
} from "@/components/wire/wire-flow-model"
import { useBreakpoint } from "@/lib/breakpoints"
import { Bone } from "@/components/bone"

export type { WaitingHint, WireLine, WireState } from "@/components/wire/wire-flow-model"

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
   * The page that lists the column, which its heading then opens, on a phone
   * too: an account's sessions and keys.
   */
  href?: string
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
  /**
   * On the hub's cell, in the column form and stacked. The picture keeps room
   * under itself for the hub's words (`useHubRoom`), so this is for a picture
   * that holds its height open around the mark: a volume's, for three nodes
   * a side. In rows it is left off: the sides are dropped to the hub's level,
   * and room above the hub would be room above the whole picture.
   */
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
 * on `components/wire/wire` (design-system §2). A node is a mark with an eyebrow, a
 * name and a line of detail under it; its line to the hub is its state
 * (`WireLine`), drawn by `ui/animated-beam`, which holds the line still under
 * reduced motion. Pointing at a node steps every other node and line back,
 * keeping what `related` says travels with it.
 *
 * Wide, the picture is as wide as its page and as short as it can be
 * (`flowForm`'s `rows`): each side is rows of nodes, as many to a row as the
 * side has room for (`perRow`), and a row is one level line into the hub that
 * its marks stand on. One row a side is a single line across the page. More
 * rows bend into the hub in the gap beside it, the sides standing so that the
 * hub is on the middle of their rows (`useLevel`). Until 0.9.1 a side was
 * always one column with its words beside the marks and a line fanning from
 * each to the hub: eight nodes stood 480px tall in the middle third of the
 * page, with nothing on either side of them. That form (`column`) is kept
 * where a side has no room for three nodes across — beside a rail, on a page
 * with a narrow measure — since rows of one or two would stand taller there.
 *
 On a phone it takes the one phone form every picture takes
 * (`docs/internal/frontend/patterns/pictures-on-a-phone.md`):
 *
 * - `marks`: the picture shrinks to its marks (`WireMarks`): a row of the
 *   start column's marks, the hub with its words under it, and a row of the
 *   end column's, the lines running down between them. For a picture whose
 *   nodes the page names again in a list — Notifications, Backups, the
 *   proxy's alerts — where the words would be said twice.
 * - `stack` (the default, for now): the columns stack under their headings
 *   with every word kept, the hub at the lanes' size, and the lines not
 *   drawn. Only for a picture that is the only place its nodes are named —
 *   the Network overview, the gateway — since there the picture is the list.
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
  const wide = useBreakpoint("lg")
  const container = useRef<HTMLDivElement>(null)
  const hubRef = useRef<HTMLDivElement>(null)
  const [focus, setFocus] = useState<string | null>(null)
  const compact = phone === "marks" && !wide
  // A hidden tab or paused motion draws every line still, as reduced motion does.
  const held = useMotionHeld()

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
  const root = useRef<HTMLDivElement>(null)
  const room = useHubRoom(root, hubRef)
  const hubWaits = hub.loadingLines && waitingDetail(hub.loadingLines, HUB_WIDTHS)

  const startSide = useRef<HTMLDivElement>(null)
  const endSide = useRef<HTMLDivElement>(null)
  const hubCell = useRef<HTMLDivElement>(null)
  const startGap = useRef<HTMLDivElement>(null)
  const endGap = useRef<HTMLDivElement>(null)
  // Both sides are one width, so one of them is measured.
  const sideWidth = useWidth(startSide)
  const form = flowForm(sideWidth, { start: left.length, end: right.length }, size)
  const rows = form === "rows"
  const per = {
    start: rows ? perRow(sideWidth, left.length, size) : 1,
    end: rows ? perRow(sideWidth, right.length, size) : 1,
  }
  const middle = (nodes: Laid[], across: number) =>
    middleRows(nodes.length, across).map((index) => refs.get(nodes[index]?.id ?? ""))
  const [startAbove, startBelow] = middle(left, per.start)
  const [endAbove, endBelow] = middle(right, per.end)
  const drops = useLevel(
    wide && !compact && rows,
    { box: startSide, above: startAbove, below: startBelow },
    { box: hubCell, above: hubRef },
    { box: endSide, above: endAbove, below: endBelow },
  )
  const laidOut = `${form}:${per.start}:${per.end}:${drops.start}:${drops.hub}:${drops.end}`

  const beamsOf = (nodes: Laid[], side: "start" | "end") =>
    nodes.map((node, index) => {
      const ref = refs.get(node.id)
      if (!ref) return null
      const into = side === "start"
      const across = into ? per.start : per.end
      const line = loading ? { state: "up" as const } : node.line
      // In rows a node's line runs to the mark between it and the hub, and
      // only the nearest one's goes on into the hub.
      const nearer = rows ? nearerInRow(index, nodes.length, across, side) : undefined
      const from = nearer === undefined ? hubRef : refs.get(nodes[nearer].id)
      const out = into ? -1 : 1
      // The rest of the row reaches the hub along this line, so pointing at
      // one of them keeps it lit, and it is drawn whole while one of them is.
      const carries = rows ? beyondInRow(index, nodes.length, across, side) : []
      const beam = carriedBeam(
        beamOf(line),
        loading ? [] : carries.map((at) => beamOf(nodes[at].line)),
      )
      const shown = lit(node.id) || carries.some((at) => lit(nodes[at].id) && focus !== null)
      return {
        order: paintOrder(line),
        beam: (
          <AnimatedBeam
            key={node.id}
            containerRef={container}
            // Drawn from the hub's end out, so the dashes of the rows' lines
            // fall in the same places where they share the way in.
            fromRef={from ?? hubRef}
            toRef={ref}
            // In rows the nearest node's line leaves the hub level, bends in
            // the gap beside it and runs level to its mark. In a column each
            // bends all the way from the hub to its own.
            shape="s"
            acrossRef={rows && nearer === undefined ? (into ? startGap : endGap) : undefined}
            redrawOn={laidOut}
            startXOffset={out * (nearer === undefined ? PORT.lg : port)}
            endXOffset={-out * port}
            reverse={node.line?.reverse}
            still={beam.still || held}
            dashed={beam.dashed}
            tone={beam.tone}
            duration={node.line?.duration}
            delay={node.line?.delay ?? staggerOf(into ? index : left.length + index)}
            className={cn(
              "transition-opacity",
              phone === "stack" && "max-lg:hidden",
              !shown && "opacity-15",
            )}
          />
        ),
      }
    })
  const beams = [...beamsOf(left, "start"), ...beamsOf(right, "end")]
    .filter((drawn) => drawn !== null)
    .sort((a, b) => a.order - b.order)
    .map((drawn) => drawn.beam)

  if (compact) {
    // The phone's one form (`WireMarks`): the start's marks, the hub with its
    // words, the end's marks, and the lines running down between them.
    const row = (column: WireFlowColumn, nodes: Laid[], heading: "above" | "below") => ({
      title: column.title,
      count: column.count,
      href: column.href,
      heading: column.href ? heading : undefined,
      nodes: nodes.map((node) => ({
        id: node.id,
        mark: node.waiting
          ? waitingMark(typeof node.waiting === "object" ? node.waiting : undefined, "sm")
          : draw(node.mark, "sm"),
        name: node.name,
        href: node.waiting ? undefined : node.href,
        onPress: node.waiting ? undefined : node.onPress,
        label: node.label ?? node.name ?? (typeof node.title === "string" ? node.title : undefined),
        round: node.placeholder || node.more,
        anonymous: node.more,
      })),
    })
    return (
      <WireMarks
        data-slot="wire-flow"
        data-loading={loading || undefined}
        aria-busy={loading || undefined}
        className={className}
        tiers={[
          row(start, left, "above"),
          {
            id: HUB,
            mark: draw(hub.mark, "lg"),
            eyebrow: hub.eyebrow,
            title: loading
              ? (hub.loadingLines?.title ?? <WaitingWords className="w-32" />)
              : hub.title,
            hint: loading ? hubWaits : hub.hint,
          },
          row(end, right, "below"),
        ]}
        wires={all.map((node, index) => {
          const beam = beamOf(loading ? { state: "up" } : node.line)
          return {
            from: node.side === "start" ? node.id : HUB,
            to: node.side === "start" ? HUB : node.id,
            reverse: node.line?.reverse,
            still: beam.still,
            dashed: beam.dashed,
            tone: beam.tone,
            duration: node.line?.duration,
            delay: node.line?.delay ?? staggerOf(index),
          }
        })}
      />
    )
  }

  return (
    <div
      ref={root}
      data-slot="wire-flow"
      data-form={form}
      data-loading={loading || undefined}
      aria-busy={loading || undefined}
      className={cn("relative py-4", className)}
    >
      <div ref={container} className="relative">
        {beams}
        <div
          className={cn(
            "relative grid gap-y-8 lg:grid-cols-[var(--wire-flow-columns)]",
            rows ? "lg:items-start" : "lg:items-center",
          )}
          style={{ "--wire-flow-columns": flowTemplate(hubShare) } as React.CSSProperties}
        >
          <Column
            column={start}
            side="start"
            bare={left.length === 0}
            sideRef={startSide}
            rows={rows}
            per={per.start}
            drop={drops.start}
          >
            {left.map((node, index) => (
              <Item
                key={node.id}
                node={node}
                rows={rows}
                column={columnStart(index, left.length, per.start, "start")}
                nodeRef={refs.get(node.id)}
                size={size}
                dim={!node.placeholder && !lit(node.id)}
                loading={loading}
                {...watch(node)}
              />
            ))}
          </Column>

          <div ref={startGap} aria-hidden className="max-lg:hidden" />

          <div
            ref={hubCell}
            // Rows stand on the hub's level whatever its cell holds, so room
            // held open around it there is only room above the picture.
            className={cn("flex min-w-0 justify-center", !(wide && rows) && hub.className)}
            style={drops.hub ? { marginTop: drops.hub } : undefined}
          >
            <WireNode
              nodeRef={hubRef}
              align="center"
              // Stacked, the hub is one more row of its lanes, at their size, so
              // its words start where theirs do.
              mark={draw(hub.mark, wide ? "lg" : size)}
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

          <div ref={endGap} aria-hidden className="max-lg:hidden" />

          <Column
            column={end}
            side="end"
            bare={right.length === 0}
            sideRef={endSide}
            rows={rows}
            per={per.end}
            drop={drops.end}
          >
            {right.map((node) => (
              <Item
                key={node.id}
                node={node}
                rows={rows}
                nodeRef={refs.get(node.id)}
                size={size}
                dim={!node.placeholder && !lit(node.id)}
                loading={loading}
                {...watch(node)}
              />
            ))}
          </Column>
        </div>
        {/* Inside the lines' own box, not beside it: a page may lay the
            picture out as a grid or a centred column, where a second child
            would take a share of the room. */}
        <div ref={room} data-slot="wire-hub-room" aria-hidden />
      </div>
    </div>
  )
}

/**
 * Room under the picture for the hub's words.
 *
 * Wide, they hang under the hub's mark out of the flow (`WireNode`'s
 * `align="center"`), so every mark sits on one line; they take no room, and
 * in a picture whose columns are shorter than the hub and its lines they ran
 * into whatever stood under it. Pages padded their own hub by a guess at how
 * tall its words were. The picture keeps the room itself now: whatever of
 * the words reaches past its own box is measured and held open under it, so
 * a picture that already has the room (tall columns, a frame of its own) is
 * exactly what it was, and one that has not ends where its words do.
 *
 * Measured before the browser paints, and again when the words or the
 * picture change size, so nothing is seen to move. Stacked, the words are in
 * the flow and there is nothing to hold.
 */
function useHubRoom(root: RefObject<HTMLDivElement | null>, hub: RefObject<HTMLDivElement | null>) {
  const room = useRef<HTMLDivElement>(null)
  useLayoutEffect(() => {
    const box = root.current
    const held = room.current
    const words = hub.current?.nextElementSibling
    if (!box || !held || !(words instanceof HTMLElement)) return
    const measure = () => {
      const hung = getComputedStyle(words).position === "absolute"
      // The box without the room already held under it. Whole pixels up, so a
      // fraction of one does not set the two observers on each other.
      const foot = box.getBoundingClientRect().bottom - held.offsetHeight
      const over = hung ? Math.ceil(words.getBoundingClientRect().bottom - foot) : 0
      const height = over > 0 ? `${over}px` : ""
      if (held.style.height !== height) held.style.height = height
    }
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(words)
    observer.observe(box)
    return () => observer.disconnect()
  })
  return room
}

/**
 * An element's width, read before the browser paints and again when it
 * changes. Until it is read a side is taken to hold three nodes to a row,
 * which is what it holds beside the shell on a laptop.
 */
function useWidth(target: RefObject<HTMLElement | null>) {
  const [width, setWidth] = useState(CELL.md * 3 + CELL.gap * 2)
  useLayoutEffect(() => {
    const el = target.current
    if (!el) return
    const read = () => setWidth(el.getBoundingClientRect().width)
    read()
    const observer = new ResizeObserver(read)
    observer.observe(el)
    return () => observer.disconnect()
  }, [target])
  return width
}

type Part = {
  box: RefObject<HTMLElement | null>
  /** The marks of the rows about its middle: the hub's own, for the hub. */
  above?: RefObject<HTMLElement | null>
  below?: RefObject<HTMLElement | null>
}

const LEVEL = { start: 0, hub: 0, end: 0 }

/** A part's level, from its own top: the middle of its two marks, or of itself with none. */
function levelOf({ box, above, below }: Part) {
  const frame = box.current?.getBoundingClientRect()
  if (!frame) return 0
  const a = above?.current?.getBoundingClientRect()
  const b = below?.current?.getBoundingClientRect()
  if (!a || !b) return frame.height / 2
  return (a.top + a.height / 2 + b.top + b.height / 2) / 2 - frame.top
}

/**
 * How far each side and the hub are dropped to stand on one level.
 *
 * A side is as tall as its rows and the words under the last of them, and
 * the hub as tall as its mark, so centring the three boxes put a single row
 * above the hub's line by half its words and bent every line that should
 * have been level. They stand at the top of the picture instead, and each
 * is dropped until the middle of its rows' marks is on the hub's mark
 * (`middleRows`, `levelDrops`). Read from the marks' own places before the
 * browser paints, and again when a part changes size: a name that wraps
 * moves the rows under it. A side with nothing in it stands on its own
 * middle.
 */
function useLevel(on: boolean, start: Part, hub: Part, end: Part) {
  const [drops, setDrops] = useState(LEVEL)
  const [sBox, sAbove, sBelow] = [start.box, start.above, start.below]
  const [hBox, hMark] = [hub.box, hub.above]
  const [eBox, eAbove, eBelow] = [end.box, end.above, end.below]
  useLayoutEffect(() => {
    const read = () => {
      const next = on
        ? levelDrops({
            start: levelOf({ box: sBox, above: sAbove, below: sBelow }),
            hub: levelOf({ box: hBox, above: hMark, below: hMark }),
            end: levelOf({ box: eBox, above: eAbove, below: eBelow }),
          })
        : LEVEL
      setDrops((held) =>
        held.start === next.start && held.hub === next.hub && held.end === next.end ? held : next,
      )
    }
    read()
    if (!on) return
    const observer = new ResizeObserver(read)
    for (const box of [sBox, hBox, eBox]) if (box.current) observer.observe(box.current)
    return () => observer.disconnect()
  }, [on, sBox, sAbove, sBelow, hBox, hMark, eBox, eAbove, eBelow])
  return drops
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

/**
 * A side's landmark, its heading, and its nodes or what it says when empty.
 * In `rows` the nodes are a grid `per` across; in a column and stacked, one
 * under the other.
 */
function Column({
  column,
  side,
  bare,
  sideRef,
  rows,
  per,
  drop,
  children,
}: {
  column: WireFlowColumn
  side: "start" | "end"
  /** Nothing to draw: the column says `empty` instead. */
  bare: boolean
  sideRef: RefObject<HTMLDivElement | null>
  /** The wide picture is rows on level lines, not a column a side. */
  rows: boolean
  /** Nodes to a row, wide. */
  per: number
  /** How far the side is dropped to stand on the hub's level, wide. */
  drop: number
  children: React.ReactNode
}) {
  const always = column.heading === "always"
  const heading = (
    <WireLaneTitle
      title={column.title}
      count={column.count}
      href={column.href}
      className={cn(
        "mb-4",
        rows && "lg:text-center",
        always ? "lg:absolute lg:inset-x-0 lg:bottom-full" : "lg:hidden",
      )}
    />
  )
  const body =
    bare && column.empty ? (
      // A div: a page's `empty` can be a node of its own, which a paragraph
      // cannot hold.
      <div className={cn("text-body text-muted-foreground", rows && "lg:text-center")}>
        {column.empty}
      </div>
    ) : (
      <ol
        className={cn(
          "grid gap-5",
          rows && "lg:grid-cols-[repeat(var(--wire-per),minmax(0,1fr))] lg:gap-x-3 lg:gap-y-6",
        )}
        style={{ "--wire-per": per } as React.CSSProperties}
      >
        {children}
      </ol>
    )
  return (
    <section
      aria-label={column.title}
      data-side={side}
      className={cn("min-w-0", !rows && side === "start" && "lg:text-right")}
    >
      {/* Hung above the rows wide rather than sharing their box: in the box it
          pushed the marks below the hub's, and every wire bent to meet them. */}
      {/* A flow root, so the nodes' drop stays inside it and does not carry
          its top, and the heading hung from it, down with them. */}
      <div className={cn("flow-root", always && "lg:relative")}>
        {heading}
        {/* The nodes are dropped under the heading, not the heading with
            them: two sides of different heights then keep their headings on
            one line at the top of the picture. */}
        <div ref={sideRef} style={drop ? { marginTop: drop } : undefined}>
          {body}
        </div>
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
  rows,
  column,
  ...handlers
}: {
  node: Laid
  nodeRef?: RefObject<HTMLDivElement | null>
  size: MarkSize
  dim: boolean
  loading: boolean
  /** Its words stand under its mark, in a row; beside it otherwise. */
  rows: boolean
  /** The grid column it starts in, where its row is short (`columnStart`). */
  column?: number
} & React.HTMLAttributes<HTMLLIElement>) {
  const align = rows ? "under" : node.side === "start" ? "end" : "start"
  const place = cn("min-w-0", rows && column && "lg:col-start-(--wire-column)")
  const style = column ? ({ "--wire-column": column } as React.CSSProperties) : undefined
  if (loading) {
    const shape = typeof node.waiting === "object" ? node.waiting : undefined
    return (
      <li className={place} style={style} aria-hidden>
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
      className={cn(place, "transition-opacity", dim && "opacity-40")}
      style={style}
    >
      <WireNode
        nodeRef={nodeRef}
        align={align}
        mark={
          <span className={cn("flex items-center justify-center", MARK_BOX[size])}>
            <Pressable node={node}>{draw(node.mark, size)}</Pressable>
          </span>
        }
        eyebrow={node.eyebrow}
        title={node.title}
        hint={node.hint}
      />
    </li>
  )
}

const SKELETON_SIZE = { lg: "size-14", md: "size-11", sm: "size-9" } as const

/**
 * The box a node's mark stands in: its nominal height, whatever the mark's
 * own box comes to with a ring or a corner badge. The lines of a row are read
 * from these boxes and lie on each other, so two marks a pixel apart in
 * height drew two lines a hair apart all the way to the hub.
 */
const MARK_BOX = { lg: "h-14", md: "h-11", sm: "h-9" } as const

/** How long each waiting line of detail is drawn, by its place: no two alike. */
const NODE_WIDTHS = ["w-24", "w-20", "w-16"]
const HUB_WIDTHS = ["w-32", "w-24", "w-28"]

/** The hub's id among the phone's marks. */
const HUB = "hub"

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
  return <Bone className={className} />
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
