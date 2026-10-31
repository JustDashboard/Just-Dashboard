"use client"

import Link from "next/link"
import { useRouter } from "next/navigation"
import { useState } from "react"
import { ArrowRight, Filter } from "@/components/icons"
import { rowReveal } from "@/components/icon-action"
import { LandedFigures } from "@/components/overview/readings"
import { Sparkline } from "@/components/metrics/sparkline"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import {
  pushTrails,
  sameOrder,
  settleOrder,
  spanPercent,
  tickPercent,
  trailMoves,
  trailSignature,
} from "@/components/bar-reading-model"
import { cn } from "@/lib/utils"

/**
 * The share each place takes of its measurement's hue, heaviest darkest. The
 * parts are told apart by their marks and names; the bar's colour says which
 * measurement it is, as every chart of it does (§10).
 */
const STEPS = [100, 74, 54, 40, 30]

export function shade(color: string, rank: number) {
  return `color-mix(in oklab, ${color} ${STEPS[rank] ?? 24}%, transparent)`
}

/** A span's part that a prune would take: its own colour, hatched. */
const hatched = (color: string) =>
  `repeating-linear-gradient(-45deg, ${color} 0 2px, color-mix(in oklab, ${color} 22%, transparent) 2px 5px)`

export type ReadingPart = {
  key: string
  /** What sizes its span, in the unit of `capacity`. Unused by a tick. */
  value: number
  /** On a `ticks` reading, its place on the axis from 0 to 1. */
  at?: number
  /** Its own hue. Without one it takes its place's step of the reading's `hue`. */
  color?: string
  /** The share of its span drawn hatched, from 0 to 1: what a prune would take. */
  hatch?: number
  /** Its small picture in the strip, the only part of it always on screen. */
  mark: React.ReactNode
  name: React.ReactNode
  /** The few words after its name. */
  detail?: React.ReactNode
  figure?: React.ReactNode
  /** Its name and figure as plain words, for the bar's accessible name. */
  label: string
  title?: string
  /** Its mark is itself a control, so the cell around it takes no focus of its own. */
  markIsControl?: boolean
  /** A control of its own, drawn on the line while it is the part on it. */
  control?: React.ReactNode
  /** What a press does, as a filter: `pressed` stays on the line and lit. */
  press?: { label: string; pressed?: boolean; onPress: () => void; opens?: boolean }
  /** What a press opens, as a page. */
  href?: { label: string; to: string }
}

/**
 * A bar and one line.
 *
 * The bands over the Docker, Processes, Proxy and Database pages were a bar
 * with a list under it: the five heaviest things as rows, and a sentence where
 * there were no rows yet. Each poll reordered the rows, and a block grew from
 * a sentence to five lines when its first reading landed, which pushed the
 * table under it down the page. The rows also repeated that table's columns.
 *
 * Here the bar is the reading and a part is on the one line beneath it only
 * while it is being looked at; at rest that is the heaviest, or the one the
 * table is narrowed to. Every state of a block — no reading yet, nothing to
 * say, one part, five — is the same height, because the line and the strip
 * under it are always there, so a page whose readings land does not move.
 *
 * Under the pointer the bar answers: the span lights while the rest fall back,
 * the line reads that part, and its trail is the readings since the page
 * opened. A sliver too narrow to point at is still reached by its mark in the
 * strip, which is also how a keyboard reaches any of them. The order holds
 * between polls (`settleOrder`), so a mark stays where it was pointed at.
 */
export function BarReading({
  title,
  figure,
  label,
  list,
  parts,
  capacity,
  rest = 0,
  restLabel = "Everything else",
  free,
  format,
  hue,
  palette,
  fixed,
  ticks,
  trail,
  live,
  working,
  message,
  note,
  className,
}: {
  title: React.ReactNode
  /** The block's own reading or control, at the end of its head. */
  figure?: React.ReactNode
  /** The block's accessible name. */
  label: string
  /** The strip's accessible name. */
  list?: string
  parts: ReadingPart[]
  /** What the whole bar stands for, in the parts' unit. Without one, their sum and `rest`. */
  capacity?: number
  /** Everything not named, drawn muted. */
  rest?: number
  restLabel?: string
  /** Names the track beyond the spans, for a bar with a real capacity. */
  free?: string
  /** Writes a `rest` or `free` value. */
  format?: (value: number) => string
  /** The hue a part without its own is shaded from, by its place. */
  hue?: string
  /** Hues a part without its own takes by its place, for parts that are not one measurement. */
  palette?: readonly string[]
  /** The parts come in an order of their own, kept: standings, kinds, newest first. */
  fixed?: boolean
  /** The bar is an axis, each part a tick at its `at`. */
  ticks?: boolean
  /** The readings move: the line draws the part's since the page opened. */
  trail?: boolean
  /** The figures arrive on an open socket (§11). A polled figure is not live. */
  live?: boolean
  /** Work on what the bar draws is running. */
  working?: boolean
  /** Said in place of the line and strip: no reading yet, or nothing to say. */
  message?: React.ReactNode
  /** What the strip leaves out, or a control for the whole block, at the strip's end. */
  note?: React.ReactNode
  className?: string
}) {
  const router = useRouter()

  const [order, setOrder] = useState<string[]>(() => settleOrder([], parts))
  const settled = fixed || ticks ? parts.map((part) => part.key) : settleOrder(order, parts)
  if (!sameOrder(settled, order)) setOrder(settled)
  const given = new Map(parts.map((part) => [part.key, part]))
  const drawn = settled.map((key, place) => {
    const part = given.get(key)!
    const color =
      palette?.[place % palette.length] ?? shade(hue ?? "var(--muted-foreground)", place)
    return { ...part, color: part.color ?? color }
  })

  const [trails, setTrails] = useState<{ signature: string; values: Map<string, number[]> }>(
    () => ({ signature: "", values: new Map() }),
  )
  const signature = trail ? trailSignature(parts) : ""
  if (signature !== trails.signature) {
    setTrails({ signature, values: pushTrails(trails.values, parts) })
  }

  const total = capacity ?? parts.reduce((sum, part) => sum + part.value, 0) + rest
  const filled = parts.reduce((sum, part) => sum + part.value, 0) + rest
  const freeValue = free && total > filled ? total - filled : 0

  // The pointer leaves a mark to reach the line's own control, so what it last
  // pointed at holds until it leaves the block.
  const [hovered, setHovered] = useState<string | null>(null)
  // A tap has no hover: it reads a part first and acts on the second press.
  const [touched, setTouched] = useState(false)
  const pressed = drawn.find((part) => part.press?.pressed)
  const pointed = drawn.find((part) => part.key === hovered)
  const active = pointed ?? pressed ?? drawn[0]
  const lit = hovered === REST || hovered === FREE ? hovered : (pointed?.key ?? pressed?.key)
  const faded = (key: string) => lit !== undefined && lit !== key

  const point = (event: React.PointerEvent, key: string) => {
    if (event.pointerType !== "touch") setHovered(key)
  }
  const act = (part: ReadingPart) => {
    if (part.press) part.press.onPress()
    else if (part.href) router.push(part.href.to)
  }
  // True when this press only brought the part onto the line.
  const previewed = (key: string) => {
    if (!touched || hovered === key) return false
    setHovered(key)
    return true
  }

  const accessible = [
    ...parts.map((part) => part.label),
    ...(rest > 0 && format ? [`${restLabel.toLowerCase()} ${format(rest)}`] : []),
  ].join(", ")

  return (
    <Panel
      plain
      aria-label={label}
      data-slot="bar-reading"
      className={className}
      onPointerLeave={() => setHovered(null)}
      onBlur={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget)) setHovered(null)
      }}
      onPointerDownCapture={(event) => setTouched(event.pointerType === "touch")}
    >
      <PanelHeader
        title={title}
        // One line and one height, whatever the head holds, so the hairlines of
        // neighbouring blocks meet and a figure arriving moves nothing.
        className="h-10 flex-nowrap"
        actions={figure}
      />
      <PanelBody className="pt-3">
        <div
          role="img"
          aria-label={`${typeof title === "string" ? title : label}: ${accessible}`}
          className="relative flex h-5 w-full items-center"
        >
          <span aria-hidden className="absolute inset-x-0 h-2.5 rounded-sm bg-meter-track" />
          {ticks ? (
            <>
              {drawn.map((part) => (
                <span
                  key={part.key}
                  aria-hidden
                  title={part.title}
                  data-slot="reading-tick"
                  onPointerEnter={(event) => point(event, part.key)}
                  onClick={() => {
                    if (!previewed(part.key)) act(part)
                  }}
                  className={cn(
                    "absolute flex h-full w-2 -translate-x-1/2 animate-rise items-center justify-center",
                    (part.press || part.href) && "cursor-pointer",
                  )}
                  style={{ left: `${tickPercent(part.at ?? 1)}%` }}
                >
                  <i
                    className="block h-2.5 w-0.5 rounded-full [transition:opacity_150ms_ease-out]"
                    style={{ background: part.color, opacity: faded(part.key) ? 0.25 : 1 }}
                  />
                </span>
              ))}
            </>
          ) : (
            <span className="relative flex h-full w-full min-w-0 items-center">
              {drawn.map((part) => (
                <span
                  key={part.key}
                  aria-hidden
                  title={part.title}
                  data-slot="reading-span"
                  onPointerEnter={(event) => point(event, part.key)}
                  onClick={() => {
                    if (!previewed(part.key)) act(part)
                  }}
                  className={cn(
                    "flex h-full items-center [transition:width_700ms_ease-out] first:[&>i]:rounded-l-sm",
                    (part.press || part.href) && "cursor-pointer",
                  )}
                  // A sliver is still a span: a container using a hundredth of
                  // a percent of the host stays on the bar it is named under.
                  style={{ width: `${spanPercent(part.value, total)}%`, minWidth: 3 }}
                >
                  <i
                    className="flex h-2.5 w-full overflow-hidden border-r border-background [transition:opacity_150ms_ease-out]"
                    style={{ opacity: faded(part.key) ? 0.3 : 1 }}
                  >
                    {(part.hatch ?? 0) < 1 && (
                      <i className="block h-full flex-1" style={{ background: part.color }} />
                    )}
                    {(part.hatch ?? 0) > 0 && (
                      <i
                        className="block h-full"
                        style={{
                          width: `${(part.hatch ?? 0) * 100}%`,
                          background: hatched(part.color),
                        }}
                      />
                    )}
                  </i>
                </span>
              ))}
              {rest > 0 && (
                <span
                  aria-hidden
                  onPointerEnter={(event) => point(event, REST)}
                  className="flex h-full min-w-[3px] items-center [transition:width_700ms_ease-out]"
                  style={{ width: `${spanPercent(rest, total)}%` }}
                >
                  <i
                    className="block h-2.5 w-full bg-muted-foreground/25 [transition:opacity_150ms_ease-out]"
                    style={{ opacity: faded(REST) ? 0.3 : 1 }}
                  />
                </span>
              )}
              {live && filled > 0 && (
                <span
                  aria-hidden
                  data-slot="reading-live"
                  className="pointer-events-none relative flex h-2.5 w-0.5 shrink-0"
                >
                  <i className="absolute inset-0 animate-breathe rounded-full bg-foreground" />
                  <i className="relative h-full w-full rounded-full bg-foreground/70" />
                </span>
              )}
              {freeValue > 0 && (
                <span
                  aria-hidden
                  onPointerEnter={(event) => point(event, FREE)}
                  className="h-full min-w-0 flex-1"
                />
              )}
            </span>
          )}
          {working && (
            <span className="pointer-events-none absolute inset-x-0 h-2.5 overflow-hidden rounded-sm">
              <i
                aria-hidden
                className="absolute inset-y-0 left-0 w-1/3 animate-sweep bg-foreground/20"
              />
            </span>
          )}
        </div>

        {message ? (
          <div
            data-slot="reading-message"
            className="mt-1 flex h-[62px] items-start pt-1.5 text-body text-muted-foreground"
          >
            <div className="line-clamp-2 min-w-0">{message}</div>
          </div>
        ) : (
          <>
            {hovered === REST || hovered === FREE ? (
              <Line
                key={hovered}
                name={hovered === REST ? restLabel : (free ?? "")}
                figure={format?.(hovered === REST ? rest : freeValue)}
                color={
                  hovered === REST
                    ? "color-mix(in oklab, var(--muted-foreground) 25%, transparent)"
                    : "var(--meter-track)"
                }
              />
            ) : active ? (
              <Line
                key={active.key}
                part={active}
                name={active.name}
                detail={active.detail}
                figure={active.figure}
                color={active.color}
                title={active.title}
                control={active.control}
                trail={trail ? trails.values.get(active.key) : undefined}
                onPress={active.press || active.href ? () => act(active) : undefined}
              />
            ) : (
              <div className="mt-1 h-8" />
            )}
            <div className="mt-0.5 flex h-7 min-w-0 items-center gap-2">
              <ul
                aria-label={list}
                data-slot="reading-strip"
                className="-mx-1 flex min-w-0 shrink-0 items-center gap-0.5"
              >
                {drawn.slice(0, MARKS).map((part) => {
                  const isActive = active?.key === part.key && hovered !== REST && hovered !== FREE
                  const face = (
                    <>
                      <span className="flex min-w-3.5 items-center justify-center">
                        {part.mark}
                      </span>
                      <i
                        aria-hidden
                        className="absolute inset-x-1.5 bottom-0.5 h-0.5 rounded-full [transition:opacity_150ms_ease-out]"
                        style={{ background: part.color, opacity: isActive ? 1 : 0.55 }}
                      />
                    </>
                  )
                  const cell = cn(
                    "relative flex h-6.5 min-w-6.5 items-center justify-center rounded-md px-1 pb-0.5 focus-ring-inset transition-colors",
                    part.press?.pressed
                      ? "bg-accent"
                      : isActive && lit !== undefined
                        ? "bg-row-hover"
                        : "hover:bg-row-hover",
                  )
                  const reach = {
                    onPointerEnter: (event: React.PointerEvent) => point(event, part.key),
                    onFocus: () => {
                      if (!touched) setHovered(part.key)
                    },
                  }
                  return (
                    <li key={part.key}>
                      {part.press ? (
                        <button
                          type="button"
                          aria-pressed={
                            part.press.opens && part.press.pressed === undefined
                              ? undefined
                              : Boolean(part.press.pressed)
                          }
                          aria-label={part.press.label}
                          title={part.title}
                          onClick={() => {
                            if (!previewed(part.key)) part.press?.onPress()
                          }}
                          className={cell}
                          {...reach}
                        >
                          {face}
                        </button>
                      ) : part.href ? (
                        <Link
                          href={part.href.to}
                          aria-label={part.href.label}
                          title={part.title}
                          onClick={(event) => {
                            if (previewed(part.key)) event.preventDefault()
                          }}
                          className={cell}
                          {...reach}
                        >
                          {face}
                        </Link>
                      ) : (
                        // Nothing to press, but a keyboard and a thumb can still
                        // bring it onto the line.
                        <span
                          title={part.title}
                          tabIndex={part.markIsControl ? undefined : 0}
                          aria-label={part.markIsControl ? undefined : part.label}
                          role={part.markIsControl ? undefined : "note"}
                          onClick={() => previewed(part.key)}
                          className={cell}
                          {...reach}
                        >
                          {face}
                        </span>
                      )}
                    </li>
                  )
                })}
              </ul>
              {note && (
                <div className="numeric ml-auto flex min-w-0 items-center justify-end gap-1.5 truncate text-hint text-muted-foreground">
                  {note}
                </div>
              )}
            </div>
          </>
        )}
      </PanelBody>
    </Panel>
  )
}

/** Past these a part keeps its span, read under the pointer, but draws no mark. */
const MARKS = 6

/** The bar's muted span and its empty track, which have no part of their own to carry a key. */
const REST = "\0rest"
const FREE = "\0free"

/**
 * The one line: the part's key, name and detail, its trail, its figure. The
 * figures in it have landed already, so reading another part shows its value
 * rather than counting up to it. It is keyed by part, so it rises each time
 * the part on it changes (§11).
 */
function Line({
  part,
  name,
  detail,
  figure,
  color,
  title,
  control,
  trail,
  onPress,
}: {
  part?: ReadingPart
  name: React.ReactNode
  detail?: React.ReactNode
  figure?: React.ReactNode
  color: string
  title?: string
  control?: React.ReactNode
  trail?: number[]
  onPress?: () => void
}) {
  const filtering = part?.press && !part.press.opens
  return (
    <div
      data-slot="reading-line"
      title={title}
      onClick={onPress}
      className={cn(
        "group -mx-2 mt-1 flex h-8 min-w-0 animate-rise items-center gap-2 rounded-md px-2 text-body transition-colors",
        onPress && "cursor-pointer hover:bg-row-hover",
      )}
    >
      <span aria-hidden className="h-3 w-0.5 shrink-0 rounded-full" style={{ background: color }} />
      <span className="min-w-0 truncate font-medium">{name}</span>
      {detail && (
        <span className="numeric min-w-0 shrink-[2] truncate text-hint text-muted-foreground">
          {detail}
        </span>
      )}
      <span className="ml-auto flex shrink-0 items-center gap-2.5">
        {trailMoves(trail) && (
          <Sparkline
            values={trail}
            color={color}
            width={56}
            height={16}
            className="shrink-0 max-sm:hidden"
          />
        )}
        {onPress &&
          (filtering ? (
            <Filter
              aria-hidden
              className={cn(
                "size-3.5 text-muted-foreground",
                part?.press?.pressed ? "text-foreground" : rowReveal(),
              )}
            />
          ) : (
            <ArrowRight aria-hidden className={cn("size-3.5 text-muted-foreground", rowReveal())} />
          ))}
        {figure !== undefined && (
          <span className="numeric font-medium">
            <LandedFigures>{figure}</LandedFigures>
          </span>
        )}
        {control && <span onClick={(event) => event.stopPropagation()}>{control}</span>}
      </span>
    </div>
  )
}
