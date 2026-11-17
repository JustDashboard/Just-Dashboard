"use client"

import Link from "next/link"
import { createRef, useMemo, useRef, type RefObject } from "react"
import { cn } from "@/lib/utils"
import { WireLaneTitle } from "@/components/wire/wire-lane"
import { useMotionHeld } from "@/components/wire/use-motion-held"
import { AnimatedBeam } from "@/components/ui/animated-beam"

/** A thing in a phone's row of marks: its mark, drawn small, and its name for a screen reader. */
export type WireMarksNode = {
  id: string
  /** The mark at the phone's size (`sm`), with its state in the corner where it has one. */
  mark: React.ReactNode
  /** The node in words. Not on the page: the list under the picture says it. */
  name?: string
  /** The mark is a link to here. */
  href?: string
  /** The mark is a button that does this. */
  onPress?: () => void
  /** The button is a toggle, and this is whether it is on: a network narrowing the list. */
  pressed?: boolean
  /** The link's or button's accessible name, when it is not `name`. */
  label?: string
  /** A dashed ring or an "N more" tile: a round focus ring, and no `data-node`. */
  round?: boolean
  /** Not a node of its own (an "N more" tile, a waiting mark): no `data-node`. */
  anonymous?: boolean
}

/** A row of marks: a lane of the wide picture, named for a screen reader. */
export type WireMarksRow = {
  title: string
  nodes: WireMarksNode[]
  /** Said in place of the row when it has no nodes. */
  empty?: React.ReactNode
  /**
   * The row's heading drawn on the page: above or below it. Left out, the
   * heading is the row's landmark name only, as the wide picture's lanes say
   * it by position. A heading with `href` opens the page that lists the row.
   */
  heading?: "above" | "below"
  href?: string
  count?: number
}

/** A node drawn whole: its mark at the wide picture's size and its words under it. */
export type WireMarksHub = {
  id: string
  mark: React.ReactNode
  eyebrow?: React.ReactNode
  title?: React.ReactNode
  hint?: React.ReactNode
}

export type WireMarksTier = WireMarksRow | WireMarksHub

/** A line between two of the picture's nodes, by id: what `AnimatedBeam` draws it as. */
export type WireMarksWire = {
  from: string
  to: string
  still?: boolean
  dashed?: boolean
  tone?: "default" | "warning" | "danger"
  reverse?: boolean
  duration?: number
  delay?: number
}

function isHub(tier: WireMarksTier): tier is WireMarksHub {
  return "mark" in tier
}

/**
 * A wiring picture on a phone: the one form every picture takes below `lg`
 * (`docs/internal/frontend/patterns/pictures-on-a-phone.md`).
 *
 * The picture shrinks to its marks. Each lane of the wide picture is a row of
 * small marks, centred; the hub stands between them at its own size with its
 * words under it; and the lines run down from row to hub to row, straight
 * from centre to centre. The names are not drawn: the list under the picture
 * says them, so a phone is not shown each thing twice. Each mark keeps its
 * name for a screen reader and its link.
 *
 * Tiers stand top to bottom in the order given: a row, a hub, a row is the
 * picture of things wired through one place (the proxy's domains, nginx and
 * the applications); two rows and no hub is the picture of things wired to
 * each other (the networks that reach the programs on Ports); a row and a
 * chain of hubs is a path (a tunnel's hostnames, Cloudflare, the connector
 * and this server).
 *
 * A line leaving a hub for the tier below leaves from under the hub's words,
 * not from its mark, so none runs through them. `WireFlow`'s `marks` form is
 * this component.
 */
export function WireMarks({
  tiers,
  wires,
  className,
  ...props
}: {
  tiers: WireMarksTier[]
  wires: WireMarksWire[]
  className?: string
} & Omit<React.HTMLAttributes<HTMLDivElement>, "className" | "children">) {
  const container = useRef<HTMLDivElement>(null)
  const held = useMotionHeld()

  // One ref for every mark, and one under each hub's words; remade only when
  // the set of nodes changes, so a poll that changes nothing redraws no line.
  const ids = tiers
    .flatMap((tier) => (isHub(tier) ? [tier.id, `${tier.id}:foot`] : tier.nodes.map((n) => n.id)))
    .join("\n")
  const refs = useMemo(() => {
    const map = new Map<string, RefObject<HTMLDivElement | null>>()
    for (const id of ids.split("\n")) if (id) map.set(id, createRef<HTMLDivElement>())
    return map
  }, [ids])
  const hubs = new Set(tiers.filter(isHub).map((tier) => tier.id))
  const place = new Map<string, number>()
  tiers.forEach((tier, index) => {
    if (isHub(tier)) place.set(tier.id, index)
    else for (const node of tier.nodes) place.set(node.id, index)
  })

  const beams = wires.map((wire, index) => {
    // A line runs down the page: from whichever end stands higher.
    const down = (place.get(wire.from) ?? 0) <= (place.get(wire.to) ?? 0)
    const [top, bottom] = down ? [wire.from, wire.to] : [wire.to, wire.from]
    const from = refs.get(hubs.has(top) ? `${top}:foot` : top)
    const to = refs.get(bottom)
    if (!from || !to) return null
    return (
      <AnimatedBeam
        key={`${wire.from}→${wire.to}`}
        containerRef={container}
        fromRef={from}
        toRef={to}
        reverse={down ? wire.reverse : !wire.reverse}
        still={wire.still || held}
        dashed={wire.dashed}
        tone={wire.tone}
        duration={wire.duration}
        delay={wire.delay ?? (index % 5) * 0.3}
      />
    )
  })

  return (
    <div data-slot="wire-marks" className={cn("relative py-4", className)} {...props}>
      <div ref={container} className="relative flex flex-col items-center text-center">
        {beams}
        {tiers.map((tier, index) =>
          isHub(tier) ? (
            <Hub
              key={tier.id}
              hub={tier}
              markRef={refs.get(tier.id)}
              footRef={refs.get(`${tier.id}:foot`)}
              className={index > 0 ? "mt-10" : undefined}
            />
          ) : (
            <Row
              key={`row:${tier.title}`}
              row={tier}
              refs={refs}
              className={index > 0 ? "mt-10" : undefined}
            />
          ),
        )}
      </div>
    </div>
  )
}

function Hub({
  hub,
  markRef,
  footRef,
  className,
}: {
  hub: WireMarksHub
  markRef?: RefObject<HTMLDivElement | null>
  footRef?: RefObject<HTMLDivElement | null>
  className?: string
}) {
  return (
    <>
      <div ref={markRef} className={cn("relative z-10 flex", className)}>
        {hub.mark}
      </div>
      {/* The hub's words under its mark: the lines to the tier below leave
          from under them (the foot), so none runs through a word. */}
      <div data-slot="wire-hub-words" className="mt-2 max-w-full min-w-0">
        {hub.eyebrow && <p className="eyebrow">{hub.eyebrow}</p>}
        {hub.title && <div className="text-body leading-snug font-medium">{hub.title}</div>}
        {hub.hint && <div className="text-hint leading-snug text-muted-foreground">{hub.hint}</div>}
      </div>
      <div ref={footRef} aria-hidden className="mt-1 h-0 w-0" />
    </>
  )
}

function Row({
  row,
  refs,
  className,
}: {
  row: WireMarksRow
  refs: Map<string, RefObject<HTMLDivElement | null>>
  className?: string
}) {
  const heading = row.heading && (
    <WireLaneTitle
      title={row.title}
      count={row.count}
      href={row.href}
      className={row.heading === "above" ? "mb-3" : "mt-3"}
    />
  )
  return (
    <section aria-label={row.title} className={cn("max-w-full", className)}>
      {row.heading === "above" && heading}
      {row.nodes.length === 0 && row.empty ? (
        <p className="text-body text-muted-foreground">{row.empty}</p>
      ) : (
        <ol className="flex flex-wrap justify-center gap-4">
          {row.nodes.map((node) => (
            <li key={node.id} data-node={node.anonymous ? undefined : node.id} className="min-w-0">
              <div ref={refs.get(node.id)} className="relative z-10 flex">
                <Pressable node={node} />
              </div>
              {node.name && <span className="sr-only">{node.name}</span>}
            </li>
          ))}
        </ol>
      )}
      {row.heading === "below" && heading}
    </section>
  )
}

/** A mark as the link or button it is, or as itself. */
function Pressable({ node }: { node: WireMarksNode }) {
  const label = node.label ?? node.name
  const shape = node.round ? "rounded-full" : "rounded-xl"
  if (node.onPress)
    return (
      <button
        type="button"
        onClick={node.onPress}
        aria-label={label}
        aria-pressed={node.pressed}
        className={cn(
          shape,
          "focus-ring",
          // On, the mark stands in the brand's ring, as the current entry does.
          node.pressed && "ring-2 ring-rule-brand ring-offset-2 ring-offset-background",
        )}
      >
        {node.mark}
      </button>
    )
  if (node.href)
    return (
      <Link href={node.href} aria-label={label} className={cn(shape, "focus-ring")}>
        {node.mark}
      </Link>
    )
  return <>{node.mark}</>
}

const TILE_SIZE = { lg: "size-14", md: "size-11", sm: "size-9" } as const

/** The nodes a row has no room for, as a count on a tile of the marks' own shape. */
export function MoreTile({ count, size = "sm" }: { count: number; size?: "lg" | "md" | "sm" }) {
  return (
    <span className="flex shrink-0 rounded-xl bg-background">
      <span
        className={cn(
          "numeric flex items-center justify-center rounded-[inherit] border border-hairline text-hint text-muted-foreground",
          TILE_SIZE[size],
        )}
      >
        +{count}
      </span>
    </span>
  )
}
